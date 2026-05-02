// Package term implements a minimal terminal-emulator widget for go-gui.
//
// Layers: grid (this file) holds the cell buffer and cursor; parser feeds
// bytes into it; pty spawns the shell; widget binds it all to a go-gui
// DrawCanvas.
package term

import (
	"strings"
	"sync"
)

// Cell attribute bits.
const (
	AttrBold uint8 = 1 << iota
	AttrUnderline
	AttrInverse
)

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
type Cell struct {
	Ch    rune
	FG    uint32 // packed Color (palette index, RGB, or DefaultColor)
	BG    uint32
	Attrs uint8
}

func defaultCell() Cell {
	return Cell{Ch: ' ', FG: DefaultColor, BG: DefaultColor}
}

// savedCursor holds the snapshot taken by SaveCursor (DECSC / CSI s).
// Stores position and SGR state per VT100 spec. Zero value means no
// snapshot has been taken yet (valid == false).
type savedCursor struct {
	r, c   int
	fg, bg uint32
	attrs  uint8
	valid  bool
}

// Grid is a fixed-size character grid. All public methods are safe for
// concurrent callers via Mu; the parser writes under Mu, OnDraw reads
// under Mu.
type Grid struct {
	Mu            sync.Mutex
	Rows          int
	Cols          int
	Cells         []Cell // row-major, len = Rows*Cols
	CursorR       int
	CursorC       int
	CurFG         uint32 // packed Color
	CurBG         uint32
	CurAttrs      uint8
	CursorVisible  bool // hidden via DEC ?25 l, shown via ?25 h
	BracketedPaste bool // DEC ?2004 — wrap pasted text in markers
	saved          savedCursor

	// Scrollback ring of rows that have scrolled off the top. Newest
	// row is the last element. Cap of 0 disables scrollback (rows are
	// dropped on scrollUp). ViewOffset > 0 freezes the viewport at
	// `ViewOffset` rows back from live; OnDraw renders accordingly.
	Scrollback    [][]Cell
	ScrollbackCap int
	ViewOffset    int

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
		CursorVisible: true,
	}
	for i := range g.Cells {
		g.Cells[i] = defaultCell()
	}
	return g
}

// Resize reflows to new dims, copying the intersecting region from the
// top-left corner. New space is filled with default cells. Cursor is
// clamped. Scrollback rows are reflowed to the new column width
// (truncated when narrower, padded with default cells when wider) so
// ViewCellAt does not need to special-case stale row widths.
func (g *Grid) Resize(rows, cols int) {
	rows = clampDim(rows)
	cols = clampDim(cols)
	if rows == g.Rows && cols == g.Cols {
		return
	}
	next := make([]Cell, rows*cols)
	for i := range next {
		next[i] = defaultCell()
	}
	rcopy := min(rows, g.Rows)
	ccopy := min(cols, g.Cols)
	for r := range rcopy {
		copy(next[r*cols:r*cols+ccopy], g.Cells[r*g.Cols:r*g.Cols+ccopy])
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

// Put writes ch at the cursor with current attrs and advances. Wraps to
// the next line at right margin; scrolls up at bottom.
func (g *Grid) Put(ch rune) {
	if g.CursorC >= g.Cols {
		g.Newline()
		g.CursorC = 0
	}
	if c := g.At(g.CursorR, g.CursorC); c != nil {
		*c = Cell{Ch: ch, FG: g.CurFG, BG: g.CurBG, Attrs: g.CurAttrs}
	}
	g.CursorC++
}

// Newline moves to next row, scrolling if needed. Column unchanged
// (LF only); shells emit CRLF.
func (g *Grid) Newline() {
	if g.CursorR+1 >= g.Rows {
		g.scrollUp()
		return
	}
	g.CursorR++
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

// CursorUp/Down/Forward/Back move the cursor by n cells, clamped.
func (g *Grid) CursorUp(n int)      { g.MoveCursor(g.CursorR-n, g.CursorC) }
func (g *Grid) CursorDown(n int)    { g.MoveCursor(g.CursorR+n, g.CursorC) }
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
	blank := Cell{Ch: ' ', FG: g.CurFG, BG: g.CurBG, Attrs: g.CurAttrs}
	for c := cFrom; c < cTo; c++ {
		g.Cells[row*g.Cols+c] = blank
	}
}

// EraseInDisplay implements CSI J. mode: 0 = cursor to end of screen,
// 1 = start of screen to cursor, 2/3 = entire screen.
func (g *Grid) EraseInDisplay(mode int) {
	blank := Cell{Ch: ' ', FG: g.CurFG, BG: g.CurBG, Attrs: g.CurAttrs}
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
		r:     g.CursorR,
		c:     g.CursorC,
		fg:    g.CurFG,
		bg:    g.CurBG,
		attrs: g.CurAttrs,
		valid: true,
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
}

// scrollUp drops the top row and clears the bottom. When ScrollbackCap
// > 0 the dropped row is pushed to the scrollback ring; the ring is
// trimmed by removing oldest entries once it exceeds the cap.
func (g *Grid) scrollUp() {
	if g.ScrollbackCap > 0 {
		row := make([]Cell, g.Cols)
		copy(row, g.Cells[:g.Cols])
		g.Scrollback = append(g.Scrollback, row)
		if extra := len(g.Scrollback) - g.ScrollbackCap; extra > 0 {
			// Drop oldest entries; reslice rather than copy to keep
			// the operation amortized O(1) when cap is reached.
			g.Scrollback = g.Scrollback[extra:]
		}
	}
	copy(g.Cells, g.Cells[g.Cols:])
	last := g.Cells[(g.Rows-1)*g.Cols:]
	for i := range last {
		last[i] = defaultCell()
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
		g.ViewOffset = clampViewOffset(g.ViewOffset+delta, max)
	}
}

// ResetView snaps the viewport back to the live grid.
func (g *Grid) ResetView() { g.ViewOffset = 0 }

// ScrollViewTop moves the viewport to the oldest scrollback row.
func (g *Grid) ScrollViewTop() { g.ViewOffset = len(g.Scrollback) }

// clampViewOffset bounds off to [0, max].
func clampViewOffset(off, max int) int {
	if off < 0 {
		return 0
	}
	if off > max {
		return max
	}
	return off
}

// ViewCellAt returns the cell visible at viewport position (r, c)
// honoring ViewOffset. When the viewport row falls inside scrollback,
// that row's stored cells are returned. Outside-range coords yield a
// default cell (never panics). Resize keeps scrollback row widths in
// sync with Cols, so no per-row width clamp is needed here.
func (g *Grid) ViewCellAt(r, c int) Cell {
	if r < 0 || r >= g.Rows || c < 0 || c >= g.Cols {
		return defaultCell()
	}
	off := clampViewOffset(g.ViewOffset, len(g.Scrollback))
	if off == 0 {
		return g.Cells[r*g.Cols+c]
	}
	n := min(off, g.Rows)
	if r < n {
		return g.Scrollback[len(g.Scrollback)-off+r][c]
	}
	return g.Cells[(r-n)*g.Cols+c]
}
