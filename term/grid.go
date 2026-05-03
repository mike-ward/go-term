// Package term implements a minimal terminal-emulator widget for go-gui.
//
// Layers: grid (this file) holds the cell buffer and cursor; parser feeds
// bytes into it; pty spawns the shell; widget binds it all to a go-gui
// DrawCanvas.
package term

import (
	"strings"
	"sync"

	"github.com/rivo/uniseg"
)

// runeWidth returns the display width of r in cells: 0 (drop / combining),
// 1 (normal), or 2 (east-asian wide, emoji). ASCII fast-paths to 1
// without entering uniseg. Non-ASCII allocates a 1- to 4-byte string for
// the uniseg call — acceptable for correctness, optimized later if the
// Put path becomes hot.
func runeWidth(r rune) int {
	if r < 0x80 {
		if r < 0x20 {
			return 0
		}
		return 1
	}
	w := uniseg.StringWidth(string(r))
	switch {
	case w <= 0:
		return 0
	case w >= 2:
		return 2
	}
	return 1
}

// Cell attribute bits.
const (
	AttrBold uint8 = 1 << iota
	AttrUnderline
	AttrInverse
	AttrDim
	AttrItalic
	AttrStrikethrough
)

// CursorShape selects the cursor glyph: filled block, baseline
// underline, or vertical bar at the leading edge of the cell.
type CursorShape uint8

const (
	CursorBlock CursorShape = iota
	CursorUnderline
	CursorBar
)

// ApplyDECSCUSR applies the DECSCUSR (CSI Ps SP q) parameter,
// setting cursor shape + blink. Unknown values fall back to the
// xterm default (blinking block, matching Ps=0/1).
func (g *Grid) ApplyDECSCUSR(ps int) {
	switch ps {
	case 0, 1:
		g.CursorShape, g.CursorBlink = CursorBlock, true
	case 2:
		g.CursorShape, g.CursorBlink = CursorBlock, false
	case 3:
		g.CursorShape, g.CursorBlink = CursorUnderline, true
	case 4:
		g.CursorShape, g.CursorBlink = CursorUnderline, false
	case 5:
		g.CursorShape, g.CursorBlink = CursorBar, true
	case 6:
		g.CursorShape, g.CursorBlink = CursorBar, false
	default:
		g.CursorShape, g.CursorBlink = CursorBlock, true
	}
}

// DECSCUSRParam returns the current cursor-style parameter for DECRQSS.
func (g *Grid) DECSCUSRParam() int {
	switch g.CursorShape {
	case CursorUnderline:
		if g.CursorBlink {
			return 3
		}
		return 4
	case CursorBar:
		if g.CursorBlink {
			return 5
		}
		return 6
	default:
		if g.CursorBlink {
			return 1
		}
		return 2
	}
}

// MaxGridDim caps each dimension of the cell buffer. Real terminals stay
// well below this; the cap exists so a runaway resize (huge canvas, NaN
// metrics, malicious caller) can't allocate hundreds of megabytes.
const MaxGridDim = 1024

// MaxScrollbackCap bounds ScrollbackCap so a malicious or mistaken
// Cfg.ScrollbackRows can't lead to multi-GB allocations as rows scroll.
// At MaxGridDim cols and ~17 B/cell this is roughly 1.7 GB worst case;
// callers should pick a value far below this.
const MaxScrollbackCap = 100000

// clampScrollback bounds n to [0, MaxScrollbackCap].
func clampScrollback(n int) int {
	if n < 0 {
		return 0
	}
	if n > MaxScrollbackCap {
		return MaxScrollbackCap
	}
	return n
}

// clampDim bounds a row or column count to [1, MaxGridDim].
func clampDim(n int) int {
	if n < 1 {
		return 1
	}
	if n > MaxGridDim {
		return MaxGridDim
	}
	return n
}

// Cell.FG and Cell.BG are packed uint32 values. The high byte is the
// encoding tag:
//
//	0x00       — palette index, low byte 0..255 (xterm 256-color table)
//	0x01       — direct RGB,   low 24 bits = R<<16 | G<<8 | B
//	0xFF       — default-color sentinel (defer to defaultFG/defaultBG)
//
// SGR 39/49 reset to DefaultColor. Plain palette indices encode as
// their numeric value (paletteColor(1) == 1) so equality comparisons
// against small int literals keep working in tests.
const (
	colorPalette uint32 = 0x00 << 24
	colorRGB     uint32 = 0x01 << 24
	DefaultColor uint32 = 0xFF << 24
)

// paletteColor encodes a 256-color palette index.
func paletteColor(i uint8) uint32 { return colorPalette | uint32(i) }

// rgbColor encodes a 24-bit RGB triple.
func rgbColor(r, g, b uint8) uint32 {
	return colorRGB | uint32(r)<<16 | uint32(g)<<8 | uint32(b)
}

// Cell is one terminal grid cell.
//
// Width encodes east-asian wide / emoji handling:
//
//	1 — normal single-cell glyph (default, including ASCII space)
//	2 — wide head; the cell at column+1 is its right-half continuation
//	0 — continuation cell (right half of a width-2 char to the left).
//	    Ch == 0 in this state; the renderer skips it.
type Cell struct {
	Ch    rune
	FG    uint32 // packed Color (palette index, RGB, or DefaultColor)
	BG    uint32
	Attrs uint8
	Width uint8
}

func defaultCell() Cell {
	return Cell{Ch: ' ', FG: DefaultColor, BG: DefaultColor, Width: 1}
}

// blankCell returns a space-filled cell carrying the supplied SGR
// state. Used by erase / insert / scroll paths that need to clear
// runs to the *current* attributes (so e.g. an Erase under inverse
// fills with inverse background).
func blankCell(fg, bg uint32, attrs uint8) Cell {
	return Cell{Ch: ' ', FG: fg, BG: bg, Attrs: attrs, Width: 1}
}

// savedCursor holds the snapshot taken by SaveCursor (DECSC / CSI s).
// Stores position and SGR state per VT100 spec. Zero value means no
// snapshot has been taken yet (valid == false).
type savedCursor struct {
	r, c       int
	fg, bg     uint32
	attrs      uint8
	autoWrap   bool
	originMode bool
	insertMode bool
	valid      bool
}

// altSavedScreen captures everything needed to restore the main screen
// when ExitAlt is called: the cell buffer plus cursor/SGR/scroll-region
// state and the DECSC slot (so DECSC/DECRC inside the alt buffer don't
// clobber the main-buffer save).
type altSavedScreen struct {
	cells            []Cell
	cursorR, cursorC int
	curFG, curBG     uint32
	curAttrs         uint8
	autoWrap         bool
	originMode       bool
	insertMode       bool
	top, bottom      int
	saved            savedCursor
}

// Grid is a fixed-size character grid. All public methods are safe for
// concurrent callers via Mu; the parser writes under Mu, OnDraw reads
// under Mu.
type Grid struct {
	Mu             sync.Mutex
	Rows           int
	Cols           int
	Cells          []Cell // row-major, len = Rows*Cols
	CursorR        int
	CursorC        int
	CurFG          uint32 // packed Color
	CurBG          uint32
	CurAttrs       uint8
	AutoWrap       bool // DEC ?7 — autowrap at right margin
	OriginMode     bool // DEC ?6 — CUP/HVP/VPA relative to scroll region
	InsertMode     bool // CSI 4 h/l — insert vs replace on Put
	CursorVisible  bool // hidden via DEC ?25 l, shown via ?25 h
	BracketedPaste bool // DEC ?2004 — wrap pasted text in markers
	FocusReporting bool // DEC ?1004 — report focus in/out to host
	SyncOutput     bool // DEC ?2026 — allow synchronized updates
	SyncActive     bool // currently inside a synchronized update block
	AppCursorKeys  bool // DEC ?1 — application cursor key mode
	AppKeypad      bool // DECNKM — application keypad mode

	// Cursor shape + blink. Set via DECSCUSR (CSI Ps SP q). Default is
	// blinking block (Ps=0/1). Embedders can override blink via
	// Cfg.CursorBlink without overriding shape.
	CursorShape CursorShape
	CursorBlink bool

	// Mouse reporting modes. Multiple may be active at once; the
	// widget emits the broadest report any of them enables. SGR
	// (?1006) is an encoding flag layered on top — without it, the
	// widget drops reports rather than fall back to legacy X10
	// byte-encoding.
	MouseTrack    bool // ?1000 — button press/release
	MouseTrackBtn bool // ?1002 — press/release + drag (button held)
	MouseTrackAny bool // ?1003 — any motion, even with no button
	MouseSGR      bool // ?1006 — SGR-style "<b;c;rM/m" encoding

	// Cwd is the most recent value reported via OSC 7 (e.g.
	// "file://host/path"). Embedders read it through Term.Cwd().
	// Empty until the shell emits an OSC 7.
	Cwd string
	// Top, Bottom define the scroll region (inclusive, 0-based).
	// Default 0..Rows-1 (full screen). Set via DECSTBM (CSI Pt;Pb r).
	// scrollUpRegion / scrollDownRegion / IND / RI / IL / DL all
	// honor this window; rows outside are untouched.
	Top    int
	Bottom int
	saved  savedCursor

	// Scrollback ring of rows that have scrolled off the top. Newest
	// row is the last element. Cap of 0 disables scrollback (rows are
	// dropped on scrollUp). ViewOffset > 0 freezes the viewport at
	// `ViewOffset` rows back from live; OnDraw renders accordingly.
	Scrollback    [][]Cell
	ScrollbackCap int
	ViewOffset    int

	// Alt-screen state. EnterAlt swaps g.Cells with a fresh blank buffer
	// and stashes main-screen state in mainSaved; ExitAlt restores it.
	// While AltActive, scrollback writes are suppressed (kitty/iTerm/
	// ghostty default) so vim/htop/less don't fill history with their
	// repaint output.
	AltActive bool
	mainSaved altSavedScreen

	// Selection state in viewport coordinates (not content coordinates):
	// when ViewOffset changes mid-selection the highlighted cells follow
	// the viewport, not the underlying text. SelActive == false means no
	// selection (single-click position pre-drag). Anchor and Head may
	// appear in any order; helpers normalize.
	SelAnchor SelPos
	SelHead   SelPos
	SelActive bool
}

// SelPos identifies a viewport cell (row, col).
type SelPos struct{ Row, Col int }

// selOrder returns the selection bounds in forward order (start <= end).
func (g *Grid) selOrder() (start, end SelPos) {
	a, b := g.SelAnchor, g.SelHead
	if b.Row < a.Row || (b.Row == a.Row && b.Col < a.Col) {
		a, b = b, a
	}
	return a, b
}

// MouseReporting reports whether any of the press/drag/any-motion
// modes (?1000/?1002/?1003) are active. The widget consults this to
// decide between local selection and host-side report emission.
func (g *Grid) MouseReporting() bool {
	return g.MouseTrack || g.MouseTrackBtn || g.MouseTrackAny
}

// InSelection reports whether viewport (r, c) is inside the half-open
// selection [start, end] (inclusive of end). False when SelActive is off.
func (g *Grid) InSelection(r, c int) bool {
	if !g.SelActive {
		return false
	}
	s, e := g.selOrder()
	if r < s.Row || r > e.Row {
		return false
	}
	if r == s.Row && c < s.Col {
		return false
	}
	if r == e.Row && c > e.Col {
		return false
	}
	return true
}

// SelectedText extracts the selection as a UTF-8 string. Trailing
// blanks per row are trimmed; row breaks emit '\n' (kitty convention).
// Returns "" when nothing is selected. Coordinates outside the grid
// (e.g. stale from a Resize-shrink) are clamped so the per-row cap is
// never negative.
func (g *Grid) SelectedText() string {
	if !g.SelActive || g.Rows <= 0 || g.Cols <= 0 {
		return ""
	}
	s, e := g.selOrder()
	s.Row, s.Col = clamp(s.Row, 0, g.Rows-1), clamp(s.Col, 0, g.Cols-1)
	e.Row, e.Col = clamp(e.Row, 0, g.Rows-1), clamp(e.Col, 0, g.Cols-1)
	if s == e {
		return ""
	}
	var b strings.Builder
	b.Grow((e.Row-s.Row+1)*g.Cols + (e.Row - s.Row))
	for r := s.Row; r <= e.Row; r++ {
		c0, c1 := 0, g.Cols-1
		if r == s.Row {
			c0 = s.Col
		}
		if r == e.Row {
			c1 = e.Col
		}
		// Find the last non-blank in the row span so trailing spaces
		// are dropped without a second pass.
		end := c0 - 1
		for c := c0; c <= c1; c++ {
			if g.ViewCellAt(r, c).Ch != ' ' {
				end = c
			}
		}
		for c := c0; c <= end; c++ {
			b.WriteRune(g.ViewCellAt(r, c).Ch)
		}
		if r < e.Row {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// clamp bounds v to [lo, hi]. lo <= hi assumed.
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ClearSelection drops any active selection.
func (g *Grid) ClearSelection() {
	g.SelActive = false
	g.SelAnchor = SelPos{}
	g.SelHead = SelPos{}
}

// NewGrid allocates a rows×cols grid filled with default cells.
func NewGrid(rows, cols int) *Grid {
	rows = clampDim(rows)
	cols = clampDim(cols)
	g := &Grid{
		Rows:          rows,
		Cols:          cols,
		Cells:         make([]Cell, rows*cols),
		CurFG:         DefaultColor,
		CurBG:         DefaultColor,
		AutoWrap:      true,
		CursorVisible: true,
		CursorShape:   CursorBlock,
		CursorBlink:   true,
		Top:           0,
		Bottom:        rows - 1,
	}
	for i := range g.Cells {
		g.Cells[i] = defaultCell()
	}
	return g
}

// reflowBuffer copies src (oldRows×oldCols) into a freshly allocated
// newRows×newCols buffer, preserving the top-left intersection and
// padding the rest with default cells. Used by Resize for both the
// active cell buffer and (when alt-active) the saved main buffer.
func reflowBuffer(src []Cell, oldRows, oldCols, newRows, newCols int) []Cell {
	next := make([]Cell, newRows*newCols)
	for i := range next {
		next[i] = defaultCell()
	}
	if len(src) == 0 || oldRows <= 0 || oldCols <= 0 {
		return next
	}
	rcopy := min(newRows, oldRows)
	ccopy := min(newCols, oldCols)
	for r := range rcopy {
		copy(next[r*newCols:r*newCols+ccopy], src[r*oldCols:r*oldCols+ccopy])
	}
	return next
}

// Resize reflows to new dims, copying the intersecting region from the
// top-left corner. New space is filled with default cells. Cursor is
// clamped. Scrollback rows are reflowed to the new column width
// (truncated when narrower, padded with default cells when wider) so
// ViewCellAt does not need to special-case stale row widths. When alt
// is active, the saved main buffer is reflowed too so ExitAlt restores
// to the new dims.
func (g *Grid) Resize(rows, cols int) {
	rows = clampDim(rows)
	cols = clampDim(cols)
	if rows == g.Rows && cols == g.Cols {
		return
	}
	next := reflowBuffer(g.Cells, g.Rows, g.Cols, rows, cols)
	if g.AltActive && len(g.mainSaved.cells) == g.Rows*g.Cols {
		g.mainSaved.cells = reflowBuffer(
			g.mainSaved.cells, g.Rows, g.Cols, rows, cols,
		)
		if g.mainSaved.cursorR >= rows {
			g.mainSaved.cursorR = rows - 1
		}
		if g.mainSaved.cursorC >= cols {
			g.mainSaved.cursorC = cols - 1
		}
		g.mainSaved.top = 0
		g.mainSaved.bottom = rows - 1
	}
	if cols != g.Cols {
		for i, row := range g.Scrollback {
			switch {
			case len(row) == cols:
				// no change
			case len(row) > cols:
				// Shrink in place; backing array is retained and the
				// suffix simply becomes unreachable.
				g.Scrollback[i] = row[:cols]
			default:
				old := len(row)
				row = append(row, make([]Cell, cols-old)...)
				for j := old; j < cols; j++ {
					row[j] = defaultCell()
				}
				g.Scrollback[i] = row
			}
		}
	}
	g.Rows = rows
	g.Cols = cols
	g.Cells = next
	if g.CursorR >= rows {
		g.CursorR = rows - 1
	}
	if g.CursorC >= cols {
		g.CursorC = cols - 1
	}
	// Reset scroll region to full screen on resize. Reflowing a custom
	// region across a row-count change is ambiguous (which app-relative
	// rows survive?), so drop it; apps re-issue DECSTBM after SIGWINCH.
	g.Top = 0
	g.Bottom = rows - 1
	// Selection coordinates were viewport-relative; the viewport
	// dimensions just changed, so the highlight no longer maps to the
	// content the user grabbed. Drop it rather than try to reflow.
	g.ClearSelection()
}

// At returns a pointer to the cell at (r,c) or nil if out of range.
func (g *Grid) At(r, c int) *Cell {
	if r < 0 || c < 0 || r >= g.Rows || c >= g.Cols {
		return nil
	}
	return &g.Cells[r*g.Cols+c]
}

// Put writes ch at the cursor with current attrs and advances. Wraps
// to the next line at right margin; scrolls up at bottom. Honors east-
// asian wide / emoji widths via runeWidth: a width-2 rune occupies the
// current cell and the cell to its right (the "continuation"), and
// wraps early if only one column remains. Width-0 runes (combining
// marks, ZWJ, etc.) are dropped — Phase 11 doesn't model combining.
func (g *Grid) Put(ch rune) {
	w := runeWidth(ch)
	if w == 0 {
		return
	}
	if !g.AutoWrap {
		if g.CursorC >= g.Cols {
			g.CursorC = g.Cols - 1
		}
		if w == 2 && g.CursorC+1 >= g.Cols {
			w = 1
		}
	} else if g.CursorC >= g.Cols {
		g.Newline()
		g.CursorC = 0
	}
	// Wide-char would overflow the right margin: pad the trailing
	// column blank, wrap, then place the wide char at column 0. The
	// blank pad inherits current SGR so background runs stay coherent.
	if g.AutoWrap && w == 2 && g.CursorC+1 >= g.Cols {
		if c := g.At(g.CursorR, g.CursorC); c != nil {
			*c = blankCell(g.CurFG, g.CurBG, g.CurAttrs)
		}
		g.Newline()
		g.CursorC = 0
	}
	// About to overwrite a cell that's part of an existing wide pair?
	// Clear the orphaned partner so we don't leave a stale glyph.
	if g.InsertMode {
		g.InsertChars(w)
	}
	g.eraseWideAt(g.CursorR, g.CursorC)
	if w == 2 {
		g.eraseWideAt(g.CursorR, g.CursorC+1)
	}
	if c := g.At(g.CursorR, g.CursorC); c != nil {
		*c = Cell{
			Ch: ch, FG: g.CurFG, BG: g.CurBG,
			Attrs: g.CurAttrs, Width: uint8(w),
		}
	}
	if w == 2 {
		if c := g.At(g.CursorR, g.CursorC+1); c != nil {
			*c = Cell{
				Ch: 0, FG: g.CurFG, BG: g.CurBG,
				Attrs: g.CurAttrs, Width: 0,
			}
		}
	}
	g.CursorC += w
	if !g.AutoWrap && g.CursorC >= g.Cols {
		g.CursorC = g.Cols - 1
	}
}

// eraseWideAt sanitizes the wide-char pair (if any) covering (r,c) so
// a subsequent overwrite doesn't leave half a glyph behind. If (r,c)
// is a wide head, blanks the continuation to its right. If it's a
// continuation, blanks the head to its left. No-op for normal cells.
func (g *Grid) eraseWideAt(r, c int) {
	cell := g.At(r, c)
	if cell == nil {
		return
	}
	switch {
	case cell.Width == 2:
		if right := g.At(r, c+1); right != nil &&
			right.Width == 0 && right.Ch == 0 {
			*right = defaultCell()
		}
	case cell.Width == 0 && cell.Ch == 0:
		if left := g.At(r, c-1); left != nil && left.Width == 2 {
			*left = defaultCell()
		}
	}
}

// Newline moves to next row, scrolling the region if needed. Column
// unchanged (LF only); shells emit CRLF. When the cursor sits on the
// scroll region's Bottom row, scrollUpRegion is invoked so apps that
// shrink the active area (less, vim status line) don't blow away
// untouched rows below. When the cursor is below Bottom (outside the
// region), it advances toward Rows-1 without scrolling.
func (g *Grid) Newline() {
	switch {
	case g.CursorR == g.Bottom:
		g.scrollUpRegion(1)
	case g.CursorR+1 < g.Rows:
		g.CursorR++
	}
}

// ReverseIndex moves the cursor up one row, scrolling the region down
// when at Top. Above Top (outside region) the cursor moves up without
// scrolling. Implements ESC M (RI).
func (g *Grid) ReverseIndex() {
	switch {
	case g.CursorR == g.Top:
		g.scrollDownRegion(1)
	case g.CursorR > 0:
		g.CursorR--
	}
}

// NextLine implements ESC E (NEL): CR + LF.
func (g *Grid) NextLine() {
	g.CarriageReturn()
	g.Newline()
}

// CarriageReturn moves to column 0.
func (g *Grid) CarriageReturn() { g.CursorC = 0 }

// Backspace moves cursor left one column. No erase.
func (g *Grid) Backspace() {
	if g.CursorC > 0 {
		g.CursorC--
	}
}

// Tab advances to next column that's a multiple of 8.
func (g *Grid) Tab() {
	if g.CursorC < 0 {
		g.CursorC = 0
	}
	g.CursorC = ((g.CursorC / 8) + 1) * 8
	if g.CursorC >= g.Cols {
		g.CursorC = g.Cols - 1
	}
}

// ClearAll wipes every cell to default and homes the cursor.
func (g *Grid) ClearAll() {
	for i := range g.Cells {
		g.Cells[i] = defaultCell()
	}
	g.CursorR, g.CursorC = 0, 0
}

// MoveCursor sets the cursor to (r,c), clamped to grid bounds. Used by
// CSI cursor-position sequences which are 1-based; callers convert.
func (g *Grid) MoveCursor(r, c int) {
	if r < 0 {
		r = 0
	}
	if r >= g.Rows {
		r = g.Rows - 1
	}
	if c < 0 {
		c = 0
	}
	if c >= g.Cols {
		c = g.Cols - 1
	}
	g.CursorR, g.CursorC = r, c
}

// MoveCursorOrigin applies DECOM semantics: r is relative to Top when
// OriginMode is enabled, and the row is clamped to the active scroll
// region. Column handling remains full-width.
func (g *Grid) MoveCursorOrigin(r, c int) {
	if !g.OriginMode || !g.regionValid() {
		g.MoveCursor(r, c)
		return
	}
	r += g.Top
	if r < g.Top {
		r = g.Top
	}
	if r > g.Bottom {
		r = g.Bottom
	}
	if c < 0 {
		c = 0
	}
	if c >= g.Cols {
		c = g.Cols - 1
	}
	g.CursorR, g.CursorC = r, c
}

// CursorUp/Down/Forward/Back move the cursor by n cells, clamped.
func (g *Grid) CursorUp(n int) {
	r := g.CursorR - n
	if g.OriginMode && g.regionValid() && g.CursorR >= g.Top && g.CursorR <= g.Bottom && r < g.Top {
		r = g.Top
	}
	g.MoveCursor(r, g.CursorC)
}

func (g *Grid) CursorDown(n int) {
	r := g.CursorR + n
	if g.OriginMode && g.regionValid() && g.CursorR >= g.Top && g.CursorR <= g.Bottom && r > g.Bottom {
		r = g.Bottom
	}
	g.MoveCursor(r, g.CursorC)
}
func (g *Grid) CursorForward(n int) { g.MoveCursor(g.CursorR, g.CursorC+n) }
func (g *Grid) CursorBack(n int)    { g.MoveCursor(g.CursorR, g.CursorC-n) }

// EraseInLine implements CSI K. mode: 0 = cursor to EOL, 1 = SOL to
// cursor, 2 = entire line. Cleared cells use current bg/attrs so
// painted backgrounds persist.
func (g *Grid) EraseInLine(mode int) {
	row := g.CursorR
	if row < 0 || row >= g.Rows {
		return
	}
	cFrom, cTo := 0, g.Cols
	switch mode {
	case 0:
		cFrom = g.CursorC
	case 1:
		cTo = g.CursorC + 1
	case 2:
		// full line
	default:
		return
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for c := cFrom; c < cTo; c++ {
		g.Cells[row*g.Cols+c] = blank
	}
}

// EraseInDisplay implements CSI J. mode: 0 = cursor to end of screen,
// 1 = start of screen to cursor, 2/3 = entire screen.
func (g *Grid) EraseInDisplay(mode int) {
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	switch mode {
	case 0:
		g.EraseInLine(0)
		for r := g.CursorR + 1; r < g.Rows; r++ {
			for c := range g.Cols {
				g.Cells[r*g.Cols+c] = blank
			}
		}
	case 1:
		g.EraseInLine(1)
		for r := range g.CursorR {
			for c := range g.Cols {
				g.Cells[r*g.Cols+c] = blank
			}
		}
	case 2, 3:
		for i := range g.Cells {
			g.Cells[i] = blank
		}
	}
}

// SaveCursor snapshots cursor position and SGR state. Implements
// DECSC (ESC 7) and CSI s. Subsequent SaveCursor calls overwrite.
func (g *Grid) SaveCursor() {
	g.saved = savedCursor{
		r:          g.CursorR,
		c:          g.CursorC,
		fg:         g.CurFG,
		bg:         g.CurBG,
		attrs:      g.CurAttrs,
		autoWrap:   g.AutoWrap,
		originMode: g.OriginMode,
		insertMode: g.InsertMode,
		valid:      true,
	}
}

// RestoreCursor restores the snapshot from SaveCursor. If no save has
// occurred, homes the cursor and resets SGR per VT100 spec.
func (g *Grid) RestoreCursor() {
	if !g.saved.valid {
		g.MoveCursor(0, 0)
		g.CurFG, g.CurBG, g.CurAttrs = DefaultColor, DefaultColor, 0
		return
	}
	g.MoveCursor(g.saved.r, g.saved.c)
	g.CurFG = g.saved.fg
	g.CurBG = g.saved.bg
	g.CurAttrs = g.saved.attrs
	g.AutoWrap = g.saved.autoWrap
	g.OriginMode = g.saved.originMode
	g.InsertMode = g.saved.insertMode
}

// regionValid reports whether Top/Bottom describe a usable region.
// A degenerate region (Top > Bottom or out of bounds) is treated as
// "no region active" so callers fall back to full-screen behavior.
func (g *Grid) regionValid() bool {
	return g.Top >= 0 && g.Bottom < g.Rows && g.Top <= g.Bottom
}

// regionFullScreen reports whether the scroll region spans every row.
// Only full-screen scrolls push to scrollback (DEC convention shared
// by xterm/iTerm/kitty); a status-line app shouldn't fill history with
// its top pane every keystroke.
func (g *Grid) regionFullScreen() bool {
	return g.regionValid() && g.Top == 0 && g.Bottom == g.Rows-1
}

// scrollUpRegion shifts rows [Top..Bottom] up by n, clearing the bottom
// n rows of the region with default cells. When the region spans the
// full screen and ScrollbackCap > 0, the displaced top rows are pushed
// to the scrollback ring (oldest first) and trimmed to cap. n is
// clamped: n <= 0 is a no-op, n >= region height clears the region.
func (g *Grid) scrollUpRegion(n int) {
	if n <= 0 || !g.regionValid() {
		return
	}
	height := g.Bottom - g.Top + 1
	if n > height {
		n = height
	}
	full := g.regionFullScreen()
	if full && g.ScrollbackCap > 0 && !g.AltActive {
		for r := 0; r < n; r++ {
			row := make([]Cell, g.Cols)
			copy(row, g.Cells[(g.Top+r)*g.Cols:(g.Top+r+1)*g.Cols])
			g.Scrollback = append(g.Scrollback, row)
		}
		if extra := len(g.Scrollback) - g.ScrollbackCap; extra > 0 {
			g.Scrollback = g.Scrollback[extra:]
		}
	}
	// Shift surviving rows up.
	if n < height {
		copy(
			g.Cells[g.Top*g.Cols:(g.Bottom+1)*g.Cols],
			g.Cells[(g.Top+n)*g.Cols:(g.Bottom+1)*g.Cols],
		)
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for r := g.Bottom - n + 1; r <= g.Bottom; r++ {
		row := g.Cells[r*g.Cols : (r+1)*g.Cols]
		for i := range row {
			row[i] = blank
		}
	}
}

// scrollDownRegion shifts rows [Top..Bottom] down by n, clearing the
// top n rows with default cells. Never writes to scrollback (down-scroll
// reveals erased space, not displaced history).
func (g *Grid) scrollDownRegion(n int) {
	if n <= 0 || !g.regionValid() {
		return
	}
	height := g.Bottom - g.Top + 1
	if n > height {
		n = height
	}
	if n < height {
		// copy is destination-overlapping safe only when src precedes
		// dst; here dst > src so iterate bottom-up.
		for r := g.Bottom; r >= g.Top+n; r-- {
			copy(
				g.Cells[r*g.Cols:(r+1)*g.Cols],
				g.Cells[(r-n)*g.Cols:(r-n+1)*g.Cols],
			)
		}
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for r := g.Top; r < g.Top+n && r <= g.Bottom; r++ {
		row := g.Cells[r*g.Cols : (r+1)*g.Cols]
		for i := range row {
			row[i] = blank
		}
	}
}

// SetScrollRegion implements DECSTBM (CSI Pt;Pb r). top/bottom are
// 0-based inclusive. Invalid or degenerate ranges (top >= bottom,
// out of bounds) reset to full screen. Cursor is homed to (0, 0)
// per DEC convention.
func (g *Grid) SetScrollRegion(top, bottom int) {
	if top < 0 || bottom >= g.Rows || top >= bottom {
		g.Top = 0
		g.Bottom = g.Rows - 1
	} else {
		g.Top = top
		g.Bottom = bottom
	}
	if g.OriginMode && g.regionValid() {
		g.CursorR, g.CursorC = g.Top, 0
		return
	}
	g.CursorR, g.CursorC = 0, 0
}

// ScrollUp implements CSI Ps S — scroll the region up by n rows,
// cursor unchanged. Wrapper around scrollUpRegion.
func (g *Grid) ScrollUp(n int) { g.scrollUpRegion(n) }

// ScrollDown implements CSI Ps T — scroll the region down by n rows.
func (g *Grid) ScrollDown(n int) { g.scrollDownRegion(n) }

// InsertLines implements CSI Ps L (IL): insert n blank lines at the
// cursor row, pushing existing rows toward Bottom; rows pushed past
// Bottom are discarded. No-op when the cursor is outside the active
// scroll region (DEC behavior).
func (g *Grid) InsertLines(n int) {
	if n <= 0 || !g.regionValid() {
		return
	}
	if g.CursorR < g.Top || g.CursorR > g.Bottom {
		return
	}
	height := g.Bottom - g.CursorR + 1
	if n > height {
		n = height
	}
	if n < height {
		for r := g.Bottom; r >= g.CursorR+n; r-- {
			copy(
				g.Cells[r*g.Cols:(r+1)*g.Cols],
				g.Cells[(r-n)*g.Cols:(r-n+1)*g.Cols],
			)
		}
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for r := g.CursorR; r < g.CursorR+n && r <= g.Bottom; r++ {
		row := g.Cells[r*g.Cols : (r+1)*g.Cols]
		for i := range row {
			row[i] = blank
		}
	}
	g.CursorC = 0
}

// DeleteLines implements CSI Ps M (DL): delete n lines starting at the
// cursor row, shifting rows below up; blank rows fill the bottom of
// the region. No-op when cursor is outside the region.
func (g *Grid) DeleteLines(n int) {
	if n <= 0 || !g.regionValid() {
		return
	}
	if g.CursorR < g.Top || g.CursorR > g.Bottom {
		return
	}
	height := g.Bottom - g.CursorR + 1
	if n > height {
		n = height
	}
	if n < height {
		copy(
			g.Cells[g.CursorR*g.Cols:(g.Bottom+1)*g.Cols],
			g.Cells[(g.CursorR+n)*g.Cols:(g.Bottom+1)*g.Cols],
		)
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for r := g.Bottom - n + 1; r <= g.Bottom; r++ {
		row := g.Cells[r*g.Cols : (r+1)*g.Cols]
		for i := range row {
			row[i] = blank
		}
	}
	g.CursorC = 0
}

// InsertChars implements CSI Ps @ (ICH): insert n blanks at the cursor,
// shifting existing cells right within the row; cells past the right
// margin are discarded. Blanks use current SGR bg/attrs.
func (g *Grid) InsertChars(n int) {
	if n <= 0 || g.CursorR < 0 || g.CursorR >= g.Rows {
		return
	}
	if g.CursorC < 0 || g.CursorC >= g.Cols {
		return
	}
	width := g.Cols - g.CursorC
	if n > width {
		n = width
	}
	row := g.Cells[g.CursorR*g.Cols : (g.CursorR+1)*g.Cols]
	if n < width {
		copy(row[g.CursorC+n:], row[g.CursorC:g.Cols-n])
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for c := g.CursorC; c < g.CursorC+n; c++ {
		row[c] = blank
	}
}

// DeleteChars implements CSI Ps P (DCH): delete n cells at the cursor,
// shifting cells from the right inward; blanks fill at the right edge.
func (g *Grid) DeleteChars(n int) {
	if n <= 0 || g.CursorR < 0 || g.CursorR >= g.Rows {
		return
	}
	if g.CursorC < 0 || g.CursorC >= g.Cols {
		return
	}
	width := g.Cols - g.CursorC
	if n > width {
		n = width
	}
	row := g.Cells[g.CursorR*g.Cols : (g.CursorR+1)*g.Cols]
	if n < width {
		copy(row[g.CursorC:], row[g.CursorC+n:g.Cols])
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for c := g.Cols - n; c < g.Cols; c++ {
		row[c] = blank
	}
}

// ScrollView shifts the viewport by `delta` rows: positive = back into
// scrollback (toward older content), negative = forward (toward live).
// Result clamped to [0, len(Scrollback)]. Saturating add: a delta near
// math.MinInt/MaxInt (e.g. derived from NaN/Inf wheel deltas) would
// overflow ViewOffset+delta before clamp, so detect the wrap.
func (g *Grid) ScrollView(delta int) {
	max := len(g.Scrollback)
	switch {
	case delta > 0 && g.ViewOffset > max-delta:
		g.ViewOffset = max
	case delta < 0 && g.ViewOffset < -delta:
		g.ViewOffset = 0
	default:
		g.ViewOffset = clamp(g.ViewOffset+delta, 0, max)
	}
}

// ResetView snaps the viewport back to the live grid.
func (g *Grid) ResetView() { g.ViewOffset = 0 }

// ScrollViewTop moves the viewport to the oldest scrollback row.
func (g *Grid) ScrollViewTop() { g.ViewOffset = len(g.Scrollback) }


// ViewCellAt returns the cell visible at viewport position (r, c)
// honoring ViewOffset. When the viewport row falls inside scrollback,
// that row's stored cells are returned. Outside-range coords yield a
// default cell (never panics). Resize keeps scrollback row widths in
// sync with Cols, so no per-row width clamp is needed here.
func (g *Grid) ViewCellAt(r, c int) Cell {
	if r < 0 || r >= g.Rows || c < 0 || c >= g.Cols {
		return defaultCell()
	}
	off := clamp(g.ViewOffset, 0, len(g.Scrollback))
	if off == 0 {
		return g.Cells[r*g.Cols+c]
	}
	n := min(off, g.Rows)
	if r < n {
		return g.Scrollback[len(g.Scrollback)-off+r][c]
	}
	return g.Cells[(r-n)*g.Cols+c]
}

// EnterAlt swaps the active cell buffer with a fresh blank one and
// stashes the main-screen state (cells, cursor, SGR, scroll region,
// DECSC slot) into mainSaved. While alt is active, scrollback writes
// are suppressed and ViewOffset is reset. No-op if already active.
//
// The DECSC save slot (g.saved) is also swapped so a DECSC/DECRC pair
// inside the alt buffer can't clobber the main-buffer save. ?1049
// callers typically SaveCursor *before* EnterAlt; that save lands in
// g.saved at call time and is correctly stashed here.
func (g *Grid) EnterAlt() {
	if g.AltActive {
		return
	}
	g.mainSaved = altSavedScreen{
		cells:      g.Cells,
		cursorR:    g.CursorR,
		cursorC:    g.CursorC,
		curFG:      g.CurFG,
		curBG:      g.CurBG,
		curAttrs:   g.CurAttrs,
		autoWrap:   g.AutoWrap,
		originMode: g.OriginMode,
		insertMode: g.InsertMode,
		top:        g.Top,
		bottom:     g.Bottom,
		saved:      g.saved,
	}
	cells := make([]Cell, g.Rows*g.Cols)
	blank := defaultCell()
	for i := range cells {
		cells[i] = blank
	}
	g.Cells = cells
	g.CursorR, g.CursorC = 0, 0
	g.CurFG, g.CurBG, g.CurAttrs = DefaultColor, DefaultColor, 0
	g.AutoWrap = true
	g.OriginMode = false
	g.InsertMode = false
	g.Top, g.Bottom = 0, g.Rows-1
	g.saved = savedCursor{}
	g.AltActive = true
	g.ResetView()
	g.ClearSelection()
}

// ExitAlt restores the main-screen state captured by EnterAlt: cells,
// cursor, SGR, scroll region, and DECSC slot. The alt buffer is dropped.
// No-op if not currently in alt.
func (g *Grid) ExitAlt() {
	if !g.AltActive {
		return
	}
	g.Cells = g.mainSaved.cells
	g.CursorR, g.CursorC = g.mainSaved.cursorR, g.mainSaved.cursorC
	g.CurFG = g.mainSaved.curFG
	g.CurBG = g.mainSaved.curBG
	g.CurAttrs = g.mainSaved.curAttrs
	g.AutoWrap = g.mainSaved.autoWrap
	g.OriginMode = g.mainSaved.originMode
	g.InsertMode = g.mainSaved.insertMode
	g.Top, g.Bottom = g.mainSaved.top, g.mainSaved.bottom
	g.saved = g.mainSaved.saved
	g.mainSaved = altSavedScreen{}
	g.AltActive = false
	g.ResetView()
	g.ClearSelection()
}
