// Package term implements a minimal terminal-emulator widget for go-gui.
//
// Layers: grid (this file) holds the cell buffer and cursor; parser feeds
// bytes into it; pty spawns the shell; widget binds it all to a go-gui
// DrawCanvas.
package term

import (
	"strings"
	"sync"
	"unicode"

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

// Charset designator bytes used in ESC ( F / ESC ) F sequences.
const (
	charsetASCII      byte = 'B' // ECMA-48 default (US ASCII)
	charsetDECSpecial byte = '0' // DEC Special Graphics line-drawing set
)

// Underline style constants for Cell.ULStyle and Grid.CurULStyle.
// ULNone means no underline. The others select the decoration shape.
const (
	ULNone   uint8 = 0
	ULSingle uint8 = 1
	ULDouble uint8 = 2
	ULCurly  uint8 = 3
	ULDotted uint8 = 4
	ULDashed uint8 = 5
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
//
// ULColor uses the same packed uint32 encoding as FG/BG. DefaultColor
// means "use the cell's foreground color." ULStyle selects the decoration
// shape; 0 (ULNone) means no underline regardless of ULColor.
type Cell struct {
	Ch      rune
	FG      uint32 // packed Color (palette index, RGB, or DefaultColor)
	BG      uint32
	ULColor uint32 // packed underline color; DefaultColor = use FG
	Attrs   uint8
	Width   uint8
	ULStyle uint8  // ULNone..ULDashed
	LinkID  uint16 // 0 = no link; non-zero indexes Grid.links
}

func defaultCell() Cell {
	return Cell{Ch: ' ', FG: DefaultColor, BG: DefaultColor, ULColor: DefaultColor, Width: 1}
}

// blankCell returns a space-filled cell carrying the supplied SGR
// state. Used by erase / insert / scroll paths that need to clear
// runs to the *current* attributes (so e.g. an Erase under inverse
// fills with inverse background). Blank cells never carry underline
// decoration (invisible on spaces; ULStyle=0 signals that).
func blankCell(fg, bg uint32, attrs uint8) Cell {
	return Cell{Ch: ' ', FG: fg, BG: bg, ULColor: DefaultColor, Attrs: attrs, Width: 1}
}

// savedCursor holds the snapshot taken by SaveCursor (DECSC / CSI s).
// Stores position and SGR state per VT100 spec. Zero value means no
// snapshot has been taken yet (valid == false).
type savedCursor struct {
	r, c       int
	fg, bg     uint32
	attrs      uint8
	ulStyle    uint8
	ulColor    uint32
	charsetG0  byte
	charsetG1  byte
	activeG    uint8
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
	rowWrapped       []bool
	cursorR, cursorC int
	curFG, curBG     uint32
	curAttrs         uint8
	curULStyle       uint8
	curULColor       uint32
	charsetG0        byte
	charsetG1        byte
	activeG          uint8
	autoWrap         bool
	originMode       bool
	insertMode       bool
	top, bottom      int
	saved            savedCursor
}

// MarkKind classifies an OSC 133 semantic shell-integration mark.
type MarkKind uint8

const (
	MarkPromptStart  MarkKind = iota // OSC 133;A — beginning of prompt
	MarkCommandStart                 // OSC 133;B — start of user input
	MarkOutputStart                  // OSC 133;C — command submitted, output begins
	MarkCommandEnd                   // OSC 133;D — command finished (optional exit code)
)

// Mark records a command-boundary position in content coordinates.
// Row is a content row index (scrollback + live), stable across ViewOffset
// changes. Adjusted when scrollback is trimmed or the grid is resized.
type Mark struct {
	Row  int
	Kind MarkKind
}

// maxMarks caps the mark ring to avoid unbounded growth in very long sessions.
const maxMarks = 10000

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
	CurULStyle     uint8  // current underline style (ULNone..ULDashed)
	CurULColor     uint32 // current underline color; DefaultColor = use fg
	CharsetG0      byte   // ESC ( F — designated set for GL when ActiveG=0
	CharsetG1      byte   // ESC ) F — designated set for GL when ActiveG=1
	ActiveG        uint8  // 0 = SI selects G0 into GL, 1 = SO selects G1
	AutoWrap       bool   // DEC ?7 — autowrap at right margin
	OriginMode     bool   // DEC ?6 — CUP/HVP/VPA relative to scroll region
	InsertMode     bool   // CSI 4 h/l — insert vs replace on Put
	CursorVisible  bool   // hidden via DEC ?25 l, shown via ?25 h
	BracketedPaste bool   // DEC ?2004 — wrap pasted text in markers
	FocusReporting bool   // DEC ?1004 — report focus in/out to host
	SyncOutput     bool   // DEC ?2026 — allow synchronized updates
	SyncActive     bool   // currently inside a synchronized update block
	AppCursorKeys  bool   // DEC ?1 — application cursor key mode
	AppKeypad      bool   // DECNKM — application keypad mode

	// BellCount is incremented each time the terminal receives BEL (0x07).
	// The widget watches for changes to trigger a visual flash.
	BellCount uint64

	// Cursor shape + blink. Set via DECSCUSR (CSI Ps SP q). Default is
	// a steady block. Embedders can override blink via
	// Cfg.CursorBlink without overriding shape.
	CursorShape CursorShape
	CursorBlink bool

	// Mouse reporting modes. Multiple may be active at once; the
	// widget emits the broadest report any of them enables. SGR
	// (?1006) is an encoding flag layered on top — without it, the
	// widget drops reports rather than fall back to legacy X10
	// byte-encoding.
	MouseTrack     bool // ?1000 — button press/release
	MouseTrackBtn  bool // ?1002 — press/release + drag (button held)
	MouseTrackAny  bool // ?1003 — any motion, even with no button
	MouseSGR       bool // ?1006 — SGR-style "<b;c;rM/m" encoding
	MouseSGRPixels bool // ?1016 — pixel-precise coordinates in SGR reports

	// Cwd is the most recent value reported via OSC 7 (e.g.
	// "file://host/path"). Embedders read it through Term.Cwd().
	// Empty until the shell emits an OSC 7.
	Cwd string

	// Hyperlink registry (OSC 8). CurLinkID is the active link applied
	// by Put; 0 means no link. links/linkIDs are a sidecar map so Cell
	// stays compact — URLs live here, not in each Cell. The maps grow
	// only, never shrink; sessions are short and links are rare.
	CurLinkID uint16
	links     map[uint16]string
	linkIDs   map[string]uint16
	nextLink  uint16
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
	Scrollback        [][]Cell
	ScrollbackWrapped []bool // parallel to Scrollback; true = row was soft-wrapped
	ScrollbackCap     int
	ViewOffset        int

	// RowWrapped[r] is true when row r ended with an autowrap (the cursor
	// reached the right margin and wrapped onto row r+1). During Resize,
	// runs of wrapped rows are joined into a logical line and re-wrapped
	// at the new width. Reset to false whenever a row is filled with blank
	// cells (erase, insert, scroll).
	RowWrapped []bool // len = Rows, parallel to live cell buffer

	// Dirty[r] is set whenever row r has a cell-level mutation since the
	// last render. The widget's readLoop reads this (under Mu) to decide
	// whether to bump drawVersion; onDraw calls ClearDirty at the start
	// of each render cycle. Allocation mirrors RowWrapped: len = Rows.
	Dirty []bool

	// TabStops[c] is true when column c has a tab stop set. Initialized to
	// every 8 columns (xterm default). ESC H sets; CSI g clears. Tab()
	// advances to the next set stop, or to Cols-1 when none remains.
	TabStops [MaxGridDim]bool

	// Theme controls the 16 ANSI base colors and the default fg/bg used
	// when rendering cells. Set via Term.SetTheme; defaults to DefaultTheme.
	Theme Theme

	// Marks records OSC 133 semantic shell-integration boundaries in
	// content-row coordinates (same space as ContentPos). Appended by
	// AddMark; adjusted by scrollback trim and Resize; capped at maxMarks.
	Marks []Mark

	// Alt-screen state. EnterAlt swaps g.Cells with a fresh blank buffer
	// and stashes main-screen state in mainSaved; ExitAlt restores it.
	// While AltActive, scrollback writes are suppressed (kitty/iTerm/
	// ghostty default) so vim/htop/less don't fill history with their
	// repaint output.
	AltActive bool
	mainSaved altSavedScreen

	// Selection state in content coordinates (scrollback + live, stable across
	// ViewOffset changes). SelActive == false means no selection (single-click
	// position pre-drag). Anchor and Head may appear in any order; helpers
	// normalize. ContentPos row: 0..len(Scrollback)-1 for scrollback rows,
	// len(Scrollback)..len(Scrollback)+Rows-1 for live rows.
	SelAnchor ContentPos
	SelHead   ContentPos
	SelActive bool

	// Kitty Keyboard Protocol state. KittyKeyFlags is the current effective
	// flags bitset (0 = legacy mode). Flag bits:
	//   1 (bit 0) — disambiguate escape codes (Tab≠Ctrl+I, Enter≠Ctrl+M, …)
	//   2 (bit 1) — report event types (press/repeat/release)
	//   4 (bit 2) — report alternate keys
	//   8 (bit 3) — report all keys as escape codes
	//  16 (bit 4) — report associated text
	// kittyFlagStack supports CSI > u (push) / CSI < u (pop) nesting.
	KittyKeyFlags  uint32
	kittyFlagStack []uint32
}

// SelPos identifies a viewport cell (row, col). Kept for callers that
// predate Phase 17; new code should use ContentPos for selection.
type SelPos struct{ Row, Col int }

// ContentPos is a stable content-row coordinate, independent of ViewOffset.
// Rows 0..len(Scrollback)-1 index scrollback oldest-first;
// rows len(Scrollback)..len(Scrollback)+Rows-1 index the live grid.
type ContentPos struct{ Row, Col int }

// PushKittyKeyFlags saves the current KittyKeyFlags on the stack and ORs in
// the new flags. Called by CSI > flags u. The stack is capped at 8 entries
// so runaway nesting can't grow it without bound.
func (g *Grid) PushKittyKeyFlags(flags uint32) {
	const maxStack = 8
	if len(g.kittyFlagStack) < maxStack {
		g.kittyFlagStack = append(g.kittyFlagStack, g.KittyKeyFlags)
	}
	g.KittyKeyFlags |= flags
}

// PopKittyKeyFlags pops n entries from the KKP flag stack, restoring the
// last pushed flags each time. Called by CSI < n u. Popping past an empty
// stack sets flags to 0 (legacy mode).
func (g *Grid) PopKittyKeyFlags(n int) {
	if n < 1 {
		n = 1
	}
	for range n {
		if len(g.kittyFlagStack) == 0 {
			g.KittyKeyFlags = 0
			return
		}
		last := len(g.kittyFlagStack) - 1
		g.KittyKeyFlags = g.kittyFlagStack[last]
		g.kittyFlagStack = g.kittyFlagStack[:last]
	}
}

// SetKittyKeyFlags sets KittyKeyFlags to flags without touching the stack.
// Called by CSI = flags u.
func (g *Grid) SetKittyKeyFlags(flags uint32) { g.KittyKeyFlags = flags }

// selOrder returns the selection bounds in forward order (start <= end).
func (g *Grid) selOrder() (start, end ContentPos) {
	a, b := g.SelAnchor, g.SelHead
	if b.Row < a.Row || (b.Row == a.Row && b.Col < a.Col) {
		a, b = b, a
	}
	return a, b
}

// viewportToContent converts a viewport row (0..Rows-1) to its content row
// (0..len(Scrollback)+Rows-1) at the current ViewOffset. Caller must hold Mu.
func (g *Grid) viewportToContent(r int) int {
	sb := len(g.Scrollback)
	off := clamp(g.ViewOffset, 0, sb)
	return sb - off + r
}

// MouseReporting reports whether any of the press/drag/any-motion
// modes (?1000/?1002/?1003) are active. The widget consults this to
// decide between local selection and host-side report emission.
func (g *Grid) MouseReporting() bool {
	return g.MouseTrack || g.MouseTrackBtn || g.MouseTrackAny
}

// Bell increments BellCount. Called by the parser on 0x07 (BEL). Caller
// holds Mu.
func (g *Grid) Bell() { g.BellCount++ }

// AddMark records an OSC 133 command boundary at the cursor's current
// content row. Caller holds Mu. Marks in the alt screen are not recorded
// (full-screen apps like vim/htop don't emit OSC 133).
func (g *Grid) AddMark(kind MarkKind) {
	if g.AltActive {
		return
	}
	row := len(g.Scrollback) + g.CursorR
	g.Marks = append(g.Marks, Mark{Row: row, Kind: kind})
	if len(g.Marks) > maxMarks {
		g.Marks = g.Marks[len(g.Marks)-maxMarks:]
	}
}

// PrevMark returns the content row of the last mark of kind strictly
// before row, and true. Returns 0, false when no such mark exists.
// Caller holds Mu.
func (g *Grid) PrevMark(row int, kind MarkKind) (int, bool) {
	for i := len(g.Marks) - 1; i >= 0; i-- {
		if g.Marks[i].Kind == kind && g.Marks[i].Row < row {
			return g.Marks[i].Row, true
		}
	}
	return 0, false
}

// NextMark returns the content row of the first mark of kind strictly
// after row, and true. Returns 0, false when no such mark exists.
// Caller holds Mu.
func (g *Grid) NextMark(row int, kind MarkKind) (int, bool) {
	for _, m := range g.Marks {
		if m.Kind == kind && m.Row > row {
			return m.Row, true
		}
	}
	return 0, false
}

// trimMarks removes extra rows from the front of all mark row indices and
// drops marks that fall below 0. Called after scrollback is trimmed.
// Caller holds Mu.
func (g *Grid) trimMarks(extra int) {
	j := 0
	for _, m := range g.Marks {
		m.Row -= extra
		if m.Row >= 0 {
			g.Marks[j] = m
			j++
		}
	}
	g.Marks = g.Marks[:j]
}

// shiftMarks applies delta to all mark row indices, dropping marks that
// fall outside [0, total). Called after resize changes scrollback depth.
// Caller holds Mu.
func (g *Grid) shiftMarks(delta, total int) {
	j := 0
	for _, m := range g.Marks {
		m.Row += delta
		if m.Row >= 0 && m.Row < total {
			g.Marks[j] = m
			j++
		}
	}
	g.Marks = g.Marks[:j]
}

func (g *Grid) markDirty(r int) {
	if r >= 0 && r < len(g.Dirty) {
		g.Dirty[r] = true
	}
}

func (g *Grid) markAllDirty() {
	for i := range g.Dirty {
		g.Dirty[i] = true
	}
}

// HasDirtyRows reports whether any row is marked dirty since the last
// ClearDirty call. Called under Mu by the widget's readLoop.
func (g *Grid) HasDirtyRows() bool {
	for _, d := range g.Dirty {
		if d {
			return true
		}
	}
	return false
}

// ClearDirty resets all dirty flags. Called by onDraw under Mu at the
// start of each render cycle so new mutations are captured next frame.
func (g *Grid) ClearDirty() {
	for i := range g.Dirty {
		g.Dirty[i] = false
	}
}

// InSelection reports whether viewport (r, c) is inside the selection.
// r is a viewport row; it is converted to content coordinates internally
// so the highlight follows content regardless of ViewOffset. False when
// SelActive is off.
func (g *Grid) InSelection(r, c int) bool {
	if !g.SelActive {
		return false
	}
	contentR := g.viewportToContent(r)
	s, e := g.selOrder()
	if contentR < s.Row || contentR > e.Row {
		return false
	}
	if contentR == s.Row && c < s.Col {
		return false
	}
	if contentR == e.Row && c > e.Col {
		return false
	}
	return true
}

// SelectedText extracts the selection as a UTF-8 string. Trailing
// blanks per row are trimmed; row breaks emit '\n' (kitty convention).
// Returns "" when nothing is selected. Coordinates are content-relative
// and are clamped to [0, len(Scrollback)+Rows-1] so stale coords from
// a Resize never produce a negative span.
func (g *Grid) SelectedText() string {
	if !g.SelActive || g.Rows <= 0 || g.Cols <= 0 {
		return ""
	}
	total := len(g.Scrollback) + g.Rows
	s, e := g.selOrder()
	s.Row, s.Col = clamp(s.Row, 0, total-1), clamp(s.Col, 0, g.Cols-1)
	e.Row, e.Col = clamp(e.Row, 0, total-1), clamp(e.Col, 0, g.Cols-1)
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
			if g.ContentCellAt(r, c).Ch != ' ' {
				end = c
			}
		}
		for c := c0; c <= end; c++ {
			b.WriteRune(g.ContentCellAt(r, c).Ch)
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
	g.SelAnchor = ContentPos{}
	g.SelHead = ContentPos{}
}

// NewGrid allocates a rows×cols grid filled with default cells.
func NewGrid(rows, cols int) *Grid {
	rows = clampDim(rows)
	cols = clampDim(cols)
	g := &Grid{
		Rows:          rows,
		Cols:          cols,
		Cells:         make([]Cell, rows*cols),
		RowWrapped:    make([]bool, rows),
		Dirty:         make([]bool, rows),
		CurFG:         DefaultColor,
		CurBG:         DefaultColor,
		CurULColor:    DefaultColor,
		CharsetG0:     charsetASCII,
		CharsetG1:     charsetASCII,
		AutoWrap:      true,
		CursorVisible: true,
		CursorShape:   CursorBlock,
		CursorBlink:   false,
		Top:           0,
		Bottom:        rows - 1,
		Theme:         DefaultTheme,
		links:         make(map[uint16]string),
		linkIDs:       make(map[string]uint16),
		nextLink:      1,
	}
	for i := range g.Cells {
		g.Cells[i] = defaultCell()
	}
	for c := 8; c < MaxGridDim; c += 8 {
		g.TabStops[c] = true
	}
	return g
}

// maxLinkEntries caps the hyperlink registry so an OSC 8 stream with many
// unique URLs can't grow the maps without bound.
const maxLinkEntries = 4096

// internLink returns the ID for url, creating one if needed. Called under Mu.
func (g *Grid) internLink(url string) uint16 {
	if id, ok := g.linkIDs[url]; ok {
		return id
	}
	if len(g.linkIDs) >= maxLinkEntries {
		return 0
	}
	id := g.nextLink
	g.nextLink++
	if g.nextLink == 0 {
		g.nextLink = 1 // wrap: ID 0 is reserved as "no link"
	}
	g.links[id] = url
	g.linkIDs[url] = id
	return id
}

// LinkURL returns the URL for the given link ID, or "" for ID 0 / unknown.
func (g *Grid) LinkURL(id uint16) string {
	if id == 0 {
		return ""
	}
	return g.links[id]
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

// physRow is used internally by the logical reflow pipeline.
// wrapped == true means this row ended with an autowrap and the next
// row is its soft-wrapped continuation.
type physRow struct {
	cells   []Cell
	wrapped bool
}

// isDefaultBlank reports whether c is an untouched default blank cell —
// i.e., no content was ever written to it. Used by logicalReflow to trim
// trailing padding from the last physical row of a logical line.
func isDefaultBlank(c Cell) bool {
	return c.Ch == ' ' && c.FG == DefaultColor && c.BG == DefaultColor &&
		c.Attrs == 0 && c.Width == 1 && c.LinkID == 0 && c.ULStyle == 0
}

// rewrapLine re-wraps a flat slice of cells (the content of one logical
// line, with continuation cells already stripped) into physical rows of
// newCols columns. All rows except the last are marked wrapped=true.
// An empty input produces a single blank row.
func rewrapLine(cells []Cell, newCols int) []physRow {
	if len(cells) == 0 {
		blank := make([]Cell, newCols)
		for i := range blank {
			blank[i] = defaultCell()
		}
		return []physRow{{cells: blank, wrapped: false}}
	}

	var rows []physRow
	cur := make([]Cell, 0, newCols)

	for i := 0; i < len(cells); {
		c := cells[i]
		// Skip continuation cells — they are regenerated with their wide head.
		if c.Width == 0 && c.Ch == 0 {
			i++
			continue
		}
		w := 1
		if c.Width == 2 {
			w = 2
		}
		// Would this glyph overflow the current row?
		if len(cur)+w > newCols {
			// Pad to full width and flush as a wrapped row.
			for len(cur) < newCols {
				cur = append(cur, defaultCell())
			}
			rows = append(rows, physRow{cells: cur, wrapped: true})
			cur = make([]Cell, 0, newCols)
		}
		cur = append(cur, c)
		if w == 2 {
			// Re-emit continuation cell.
			cur = append(cur, Cell{Ch: 0, FG: c.FG, BG: c.BG, Attrs: c.Attrs, Width: 0, ULStyle: c.ULStyle, ULColor: c.ULColor})
		}
		i++
	}

	// Final (or only) row — not wrapped.
	for len(cur) < newCols {
		cur = append(cur, defaultCell())
	}
	rows = append(rows, physRow{cells: cur, wrapped: false})
	return rows
}

// logicalReflow joins soft-wrapped physical rows into logical lines,
// re-wraps them at newCols, and returns the new cell buffer, wrap flags,
// scrollback, and cursor position. Hard newlines (wrapped==false) are
// never joined across.
//
// Parameters:
//   - cells/rowWrapped: live cell buffer and per-row wrap flags (oldRows×oldCols)
//   - scrollback/sbWrapped: scrollback ring and its wrap flags
//   - oldRows, oldCols: current grid dims
//   - newRows, newCols: target dims
//   - cursorR, cursorC: cursor in the live buffer
//   - scrollbackCap: maximum scrollback rows (0 = unlimited trim handled by caller)
func logicalReflow(
	cells []Cell, rowWrapped []bool,
	scrollback [][]Cell, sbWrapped []bool,
	oldRows, oldCols, newRows, newCols int,
	cursorR, cursorC int,
	scrollbackCap int,
) (newCells []Cell, newRowWrapped []bool, newScrollback [][]Cell, newSbWrapped []bool, newCursorR, newCursorC int) {

	// --- Build flat physical-row list (scrollback + live) ---
	nSB := len(scrollback)
	total := nSB + oldRows
	phys := make([]physRow, total)
	for i, row := range scrollback {
		w := false
		if i < len(sbWrapped) {
			w = sbWrapped[i]
		}
		phys[i] = physRow{cells: row, wrapped: w}
	}
	for r := 0; r < oldRows; r++ {
		row := make([]Cell, oldCols)
		copy(row, cells[r*oldCols:(r+1)*oldCols])
		w := false
		if r < len(rowWrapped) {
			w = rowWrapped[r]
		}
		phys[nSB+r] = physRow{cells: row, wrapped: w}
	}

	cursorPhys := nSB + clamp(cursorR, 0, oldRows-1) // cursor's index in phys[]

	// --- Identify logical lines and the one containing the cursor ---
	type logLine struct {
		start, end int // inclusive indices into phys[]
	}
	var lines []logLine
	lineStart := 0
	cursorLineIdx := 0
	cursorLineFound := false
	for i, pr := range phys {
		if !pr.wrapped {
			ll := logLine{lineStart, i}
			if !cursorLineFound && cursorPhys >= lineStart && cursorPhys <= i {
				cursorLineIdx = len(lines)
				cursorLineFound = true
			}
			lines = append(lines, ll)
			lineStart = i + 1
		}
	}
	// Unclosed line at the end (shouldn't happen — last live row wrapped==false,
	// but guard defensively).
	if lineStart < len(phys) {
		if !cursorLineFound && cursorPhys >= lineStart {
			cursorLineIdx = len(lines)
			cursorLineFound = true
		}
		lines = append(lines, logLine{lineStart, len(phys) - 1})
	}
	if !cursorLineFound && len(lines) > 0 {
		cursorLineIdx = len(lines) - 1
	}

	// Cursor's display-column offset within its logical line.
	// Each preceding wrapped physical row contributes oldCols columns.
	// A pending-wrap cursor sits one column past the right margin after a
	// glyph was written in the last cell; keep it anchored to that last
	// cell instead of treating it as content beyond the row.
	var cursorLogCol int
	if len(lines) > 0 && cursorLineIdx < len(lines) {
		ll := lines[cursorLineIdx]
		effectiveCursorC := cursorC
		if effectiveCursorC >= oldCols {
			effectiveCursorC = oldCols - 1
		}
		if effectiveCursorC < 0 {
			effectiveCursorC = 0
		}
		cursorLogCol = (cursorPhys-ll.start)*oldCols + effectiveCursorC
	}

	// --- Re-wrap all logical lines ---
	var allNew []physRow
	cursorNewPhysStart := 0 // index into allNew where cursor's logical line starts
	var cursorLineRewrapped []physRow

	for li, ll := range lines {
		// Collect cells for this logical line. Trim trailing default
		// blanks from the last physical row to avoid padding from creating
		// spurious extra physical rows after re-wrap. Only preserve cells
		// up to and including the cursor column when the cursor is within
		// the row bounds (cursorC < len(row)). When cursorC >= len(row)
		// (pending-wrap state past the right margin), don't preserve blanks
		// — the cursor position will be clamped to the rewrapped line's end.
		var lineCells []Cell
		for pi := ll.start; pi <= ll.end; pi++ {
			row := phys[pi].cells
			trimTo := len(row)
			if pi < ll.end && phys[pi].wrapped {
				// Wide-char autowrap pads the final column blank before the
				// glyph is emitted at column 0 of the next row. Drop that
				// synthetic padding so logical reflow rejoins the glyph.
				next := phys[pi+1].cells
				if len(next) > 0 && next[0].Width == 2 {
					for trimTo > 0 && isDefaultBlank(row[trimTo-1]) {
						trimTo--
					}
				}
			}
			if pi == ll.end {
				for trimTo > 0 && isDefaultBlank(row[trimTo-1]) {
					trimTo--
				}
				// Preserve cells for cursor only when it is within the row.
				if pi == cursorPhys && cursorC < len(row) && cursorC+1 > trimTo {
					trimTo = cursorC + 1
				}
			}
			lineCells = append(lineCells, row[:trimTo]...)
		}

		rewrapped := rewrapLine(lineCells, newCols)
		if li == cursorLineIdx {
			cursorNewPhysStart = len(allNew)
			cursorLineRewrapped = rewrapped
		}
		allNew = append(allNew, rewrapped...)
		// Trim oldest pre-cursor rows to bound allNew growth when reflowing
		// deep scrollback to narrow columns (worst case: oldCols/newCols
		// expansion per row). Only safe before the cursor line is added;
		// cursorNewPhysStart is set in the same iteration so indices remain
		// relative to the (possibly trimmed) slice.
		if li < cursorLineIdx {
			capRows := newRows + scrollbackCap
			if capRows < newRows*2 {
				capRows = newRows * 2
			}
			if len(allNew) > capRows {
				allNew = allNew[len(allNew)-capRows:]
			}
		}
	}

	// --- Cursor position in new layout ---
	// Clamp cursorLogCol to within the actual rewrapped content of the cursor
	// line so out-of-bounds column positions (e.g. pending-wrap state where
	// cursorC == oldCols) don't produce a newCursorPhys past the line's end.
	rowOffset := 0
	colOffset := 0
	if newCols > 0 && len(cursorLineRewrapped) > 0 {
		maxLogCol := len(cursorLineRewrapped)*newCols - 1
		if maxLogCol < 0 {
			maxLogCol = 0
		}
		effective := cursorLogCol
		if effective > maxLogCol {
			effective = maxLogCol
		}
		rowOffset = effective / newCols
		colOffset = effective % newCols
		if rowOffset >= len(cursorLineRewrapped) {
			rowOffset = len(cursorLineRewrapped) - 1
		}
	}
	newCursorPhys := cursorNewPhysStart + rowOffset

	// --- Split into scrollback + live ---
	// Anchor the live buffer so the cursor ends up near the bottom of the
	// screen (at row newRows-1). This keeps content visible instead of
	// pushing it into scrollback when the cursor is near the top of the
	// old screen. Clamp to [0, len(allNew)-newRows] so we never read
	// past the end of allNew.
	maxStart := len(allNew) - newRows
	if maxStart < 0 {
		maxStart = 0
	}
	liveStart := newCursorPhys - (newRows - 1)
	if liveStart > maxStart {
		liveStart = maxStart
	}
	if liveStart < 0 {
		liveStart = 0
	}

	// Scrollback = allNew[0..liveStart-1]
	newScrollback = make([][]Cell, 0, liveStart)
	newSbWrapped = make([]bool, 0, liveStart)
	for _, pr := range allNew[:liveStart] {
		newScrollback = append(newScrollback, pr.cells)
		newSbWrapped = append(newSbWrapped, pr.wrapped)
	}
	if scrollbackCap > 0 && len(newScrollback) > scrollbackCap {
		trim := len(newScrollback) - scrollbackCap
		newScrollback = newScrollback[trim:]
		newSbWrapped = newSbWrapped[trim:]
	}

	// Live buffer = allNew[liveStart..end]
	newCells = make([]Cell, newRows*newCols)
	for i := range newCells {
		newCells[i] = defaultCell()
	}
	newRowWrapped = make([]bool, newRows)
	liveRows := allNew[liveStart:]
	for r, pr := range liveRows {
		if r >= newRows {
			break
		}
		copy(newCells[r*newCols:(r+1)*newCols], pr.cells)
		newRowWrapped[r] = pr.wrapped
	}

	// Cursor row in the live buffer.
	newCursorR = newCursorPhys - liveStart
	newCursorC = colOffset
	if newCursorR < 0 {
		newCursorR = 0
	}
	if newCursorR >= newRows {
		newCursorR = newRows - 1
	}
	if newCursorC < 0 {
		newCursorC = 0
	}
	if newCursorC >= newCols {
		newCursorC = newCols - 1
	}
	return
}

// Resize reflows to new dims using logical line wrapping. Rows that ended
// with an autowrap (RowWrapped[r]==true) are joined with their successor
// into a single logical line and re-wrapped at the new column width, so
// terminal output reflowed like a modern terminal instead of cropping.
// Cursor position is tracked through the reflow. Rows separated by an
// explicit newline (RowWrapped[r]==false) are never joined.
//
// When alt-screen is active the alt buffer is reflowed with simple
// crop/pad (full-screen apps control every cell), while the saved main
// buffer receives logical reflow.
//
// The scroll region is reset after resize; apps re-issue DECSTBM after
// SIGWINCH. Selection is dropped. ViewOffset is reset to the live view.
func (g *Grid) Resize(rows, cols int) {
	rows = clampDim(rows)
	cols = clampDim(cols)
	if rows == g.Rows && cols == g.Cols {
		return
	}

	oldSbLen := len(g.Scrollback)

	if g.AltActive {
		// Alt buffer: simple crop/pad reflow (full-screen app controls cells).
		g.Cells = reflowBuffer(g.Cells, g.Rows, g.Cols, rows, cols)
		newRW := make([]bool, rows)
		copy(newRW, g.RowWrapped)
		g.RowWrapped = newRW

		// Main (saved) buffer: logical reflow with cursor tracking.
		if len(g.mainSaved.cells) == g.Rows*g.Cols {
			savedRW := g.mainSaved.rowWrapped
			if len(savedRW) != g.Rows {
				savedRW = make([]bool, g.Rows)
			}
			newCells, newRW2, newSB, newSBW, newCR, newCC := logicalReflow(
				g.mainSaved.cells, savedRW,
				g.Scrollback, g.ScrollbackWrapped,
				g.Rows, g.Cols, rows, cols,
				g.mainSaved.cursorR, g.mainSaved.cursorC,
				g.ScrollbackCap,
			)
			g.mainSaved.cells = newCells
			g.mainSaved.rowWrapped = newRW2
			g.Scrollback = newSB
			g.ScrollbackWrapped = newSBW
			g.mainSaved.cursorR = newCR
			g.mainSaved.cursorC = newCC
			g.mainSaved.top = 0
			g.mainSaved.bottom = rows - 1
		}
	} else {
		newCells, newRW, newSB, newSBW, newCR, newCC := logicalReflow(
			g.Cells, g.RowWrapped,
			g.Scrollback, g.ScrollbackWrapped,
			g.Rows, g.Cols, rows, cols,
			g.CursorR, g.CursorC,
			g.ScrollbackCap,
		)
		g.Cells = newCells
		g.RowWrapped = newRW
		g.Scrollback = newSB
		g.ScrollbackWrapped = newSBW
		g.CursorR = newCR
		g.CursorC = newCC
	}

	g.Rows = rows
	g.Cols = cols
	g.Dirty = make([]bool, rows)
	g.markAllDirty()
	// Reset scroll region to full screen on resize; apps re-issue DECSTBM
	// after SIGWINCH. ViewOffset reset: scrollback row indices changed.
	g.Top = 0
	g.Bottom = rows - 1
	g.ViewOffset = 0
	// Shift selection content rows by the change in scrollback depth so the
	// highlight follows the same content after reflow. Clear if entirely
	// scrolled off (clamp handles out-of-range silently).
	if g.SelActive {
		delta := len(g.Scrollback) - oldSbLen
		total := len(g.Scrollback) + rows
		g.SelAnchor.Row = clamp(g.SelAnchor.Row+delta, 0, total-1)
		g.SelHead.Row = clamp(g.SelHead.Row+delta, 0, total-1)
	}
	// Shift mark rows by the same delta, dropping any that fell off.
	if len(g.Marks) > 0 {
		delta := len(g.Scrollback) - oldSbLen
		total := len(g.Scrollback) + rows
		g.shiftMarks(delta, total)
	}
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
	ch = g.translateRune(ch)
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
		g.RowWrapped[g.CursorR] = true
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
		g.RowWrapped[g.CursorR] = true
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
			Attrs: g.CurAttrs, Width: uint8(w), LinkID: g.CurLinkID,
			ULStyle: g.CurULStyle, ULColor: g.CurULColor,
		}
	}
	if w == 2 {
		if c := g.At(g.CursorR, g.CursorC+1); c != nil {
			*c = Cell{
				Ch: 0, FG: g.CurFG, BG: g.CurBG,
				Attrs: g.CurAttrs, Width: 0, LinkID: g.CurLinkID,
				ULStyle: g.CurULStyle, ULColor: g.CurULColor,
			}
		}
	}
	g.markDirty(g.CursorR)
	g.CursorC += w
	if !g.AutoWrap && g.CursorC >= g.Cols {
		g.CursorC = g.Cols - 1
	}
}

// translateRune maps printable bytes through the active GL charset.
// Today we honor the DEC Special Graphics set (`0`), which TUIs use for
// box-drawing via SI/SO or ESC ( 0 / ESC ) 0 designation.
func (g *Grid) translateRune(ch rune) rune {
	if ch < 0x20 || ch > 0x7e {
		return ch
	}
	charset := g.CharsetG0
	if g.ActiveG == 1 {
		charset = g.CharsetG1
	}
	if charset != charsetDECSpecial {
		return ch
	}
	switch ch {
	case '`':
		return '◆'
	case 'a':
		return '▒'
	case 'f':
		return '°'
	case 'g':
		return '±'
	case 'h':
		return '␤'
	case 'i':
		return '␋'
	case 'j':
		return '┘'
	case 'k':
		return '┐'
	case 'l':
		return '┌'
	case 'm':
		return '└'
	case 'n':
		return '┼'
	case 'o':
		return '⎺'
	case 'p':
		return '⎻'
	case 'q':
		return '─'
	case 'r':
		return '⎼'
	case 's':
		return '⎽'
	case 't':
		return '├'
	case 'u':
		return '┤'
	case 'v':
		return '┴'
	case 'w':
		return '┬'
	case 'x':
		return '│'
	case 'y':
		return '≤'
	case 'z':
		return '≥'
	case '{':
		return 'π'
	case '|':
		return '≠'
	case '}':
		return '£'
	case '~':
		return '·'
	default:
		return ch
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

// Tab advances the cursor to the next tab stop. Scans TabStops from
// CursorC+1; if no stop exists within the row, clamps to Cols-1.
func (g *Grid) Tab() {
	if g.CursorC < 0 {
		g.CursorC = 0
	}
	for c := g.CursorC + 1; c < g.Cols; c++ {
		if g.TabStops[c] {
			g.CursorC = c
			return
		}
	}
	g.CursorC = g.Cols - 1
}

// SetTabStop sets a tab stop at the current cursor column. Implements ESC H (HTS).
func (g *Grid) SetTabStop() {
	if g.CursorC >= 0 && g.CursorC < MaxGridDim {
		g.TabStops[g.CursorC] = true
	}
}

// ClearTabStop clears the tab stop at the current cursor column (all==false)
// or clears all tab stops (all==true). Implements CSI g (TBC).
func (g *Grid) ClearTabStop(all bool) {
	if all {
		g.TabStops = [MaxGridDim]bool{}
		return
	}
	if g.CursorC >= 0 && g.CursorC < MaxGridDim {
		g.TabStops[g.CursorC] = false
	}
}

// ClearAll wipes every cell to default and homes the cursor.
func (g *Grid) ClearAll() {
	for i := range g.Cells {
		g.Cells[i] = defaultCell()
	}
	g.CursorR, g.CursorC = 0, 0
	g.markAllDirty()
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
	g.markDirty(row)
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
		g.markAllDirty()
	case 1:
		g.EraseInLine(1)
		for r := range g.CursorR {
			for c := range g.Cols {
				g.Cells[r*g.Cols+c] = blank
			}
		}
		g.markAllDirty()
	case 2, 3:
		for i := range g.Cells {
			g.Cells[i] = blank
		}
		g.markAllDirty()
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
		ulStyle:    g.CurULStyle,
		ulColor:    g.CurULColor,
		charsetG0:  g.CharsetG0,
		charsetG1:  g.CharsetG1,
		activeG:    g.ActiveG,
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
		g.CurULStyle = 0
		g.CurULColor = DefaultColor
		return
	}
	g.MoveCursor(g.saved.r, g.saved.c)
	g.CurFG = g.saved.fg
	g.CurBG = g.saved.bg
	g.CurAttrs = g.saved.attrs
	g.CurULStyle = g.saved.ulStyle
	g.CurULColor = g.saved.ulColor
	g.CharsetG0 = g.saved.charsetG0
	g.CharsetG1 = g.saved.charsetG1
	g.ActiveG = g.saved.activeG
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
			g.ScrollbackWrapped = append(g.ScrollbackWrapped, g.RowWrapped[g.Top+r])
		}
		if extra := len(g.Scrollback) - g.ScrollbackCap; extra > 0 {
			g.Scrollback = g.Scrollback[extra:]
			g.ScrollbackWrapped = g.ScrollbackWrapped[extra:]
			g.trimMarks(extra)
		}
	}
	// Shift surviving rows up.
	if n < height {
		copy(
			g.Cells[g.Top*g.Cols:(g.Bottom+1)*g.Cols],
			g.Cells[(g.Top+n)*g.Cols:(g.Bottom+1)*g.Cols],
		)
		copy(g.RowWrapped[g.Top:g.Bottom+1-n], g.RowWrapped[g.Top+n:g.Bottom+1])
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for r := g.Bottom + 1 - n; r <= g.Bottom; r++ {
		row := g.Cells[r*g.Cols : (r+1)*g.Cols]
		for i := range row {
			row[i] = blank
		}
		g.RowWrapped[r] = false
	}
	g.markAllDirty()
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
			g.RowWrapped[r] = g.RowWrapped[r-n]
		}
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for r := g.Top; r < g.Top+n && r <= g.Bottom; r++ {
		row := g.Cells[r*g.Cols : (r+1)*g.Cols]
		for i := range row {
			row[i] = blank
		}
		g.RowWrapped[r] = false
	}
	g.markAllDirty()
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
			g.RowWrapped[r] = g.RowWrapped[r-n]
		}
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for r := g.CursorR; r < g.CursorR+n && r <= g.Bottom; r++ {
		row := g.Cells[r*g.Cols : (r+1)*g.Cols]
		for i := range row {
			row[i] = blank
		}
		g.RowWrapped[r] = false
	}
	g.CursorC = 0
	g.markAllDirty()
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
		copy(g.RowWrapped[g.CursorR:g.Bottom+1-n], g.RowWrapped[g.CursorR+n:g.Bottom+1])
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for r := g.Bottom - n + 1; r <= g.Bottom; r++ {
		row := g.Cells[r*g.Cols : (r+1)*g.Cols]
		for i := range row {
			row[i] = blank
		}
		g.RowWrapped[r] = false
	}
	g.CursorC = 0
	g.markAllDirty()
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
	g.markDirty(g.CursorR)
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
	g.markDirty(g.CursorR)
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
		rowWrapped: g.RowWrapped,
		cursorR:    g.CursorR,
		cursorC:    g.CursorC,
		curFG:      g.CurFG,
		curBG:      g.CurBG,
		curAttrs:   g.CurAttrs,
		curULStyle: g.CurULStyle,
		curULColor: g.CurULColor,
		charsetG0:  g.CharsetG0,
		charsetG1:  g.CharsetG1,
		activeG:    g.ActiveG,
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
	g.RowWrapped = make([]bool, g.Rows)
	g.CursorR, g.CursorC = 0, 0
	g.CurFG, g.CurBG, g.CurAttrs = DefaultColor, DefaultColor, 0
	g.CurULStyle = 0
	g.CurULColor = DefaultColor
	g.CharsetG0 = charsetASCII
	g.CharsetG1 = charsetASCII
	g.ActiveG = 0
	g.AutoWrap = true
	g.OriginMode = false
	g.InsertMode = false
	g.Top, g.Bottom = 0, g.Rows-1
	g.saved = savedCursor{}
	g.AltActive = true
	g.ResetView()
	g.ClearSelection()
	g.markAllDirty()
}

// ExitAlt restores the main-screen state captured by EnterAlt: cells,
// cursor, SGR, scroll region, and DECSC slot. The alt buffer is dropped.
// No-op if not currently in alt.
func (g *Grid) ExitAlt() {
	if !g.AltActive {
		return
	}
	g.Cells = g.mainSaved.cells
	g.RowWrapped = g.mainSaved.rowWrapped
	g.CursorR, g.CursorC = g.mainSaved.cursorR, g.mainSaved.cursorC
	g.CurFG = g.mainSaved.curFG
	g.CurBG = g.mainSaved.curBG
	g.CurAttrs = g.mainSaved.curAttrs
	g.CurULStyle = g.mainSaved.curULStyle
	g.CurULColor = g.mainSaved.curULColor
	g.CharsetG0 = g.mainSaved.charsetG0
	g.CharsetG1 = g.mainSaved.charsetG1
	g.ActiveG = g.mainSaved.activeG
	g.AutoWrap = g.mainSaved.autoWrap
	g.OriginMode = g.mainSaved.originMode
	g.InsertMode = g.mainSaved.insertMode
	g.Top, g.Bottom = g.mainSaved.top, g.mainSaved.bottom
	g.saved = g.mainSaved.saved
	g.mainSaved = altSavedScreen{}
	g.AltActive = false
	g.ResetView()
	g.ClearSelection()
	g.markAllDirty()
}

// ContentRows returns the total number of content rows (scrollback + live).
func (g *Grid) ContentRows() int { return len(g.Scrollback) + g.Rows }

// ContentCellAt returns the cell at content-coordinate (row, col).
// Bounds-safe: out-of-range inputs return a default cell (never panics).
// Caller must hold Mu.
func (g *Grid) ContentCellAt(row, col int) Cell {
	sb := len(g.Scrollback)
	if row < 0 || row >= sb+g.Rows || col < 0 || col >= g.Cols {
		return defaultCell()
	}
	if row < sb {
		return g.Scrollback[row][col]
	}
	return g.Cells[(row-sb)*g.Cols+col]
}

// ContentRowToViewport maps a content row to its viewport row at the current
// ViewOffset. Returns (vr, true) when the content row is visible, (0, false)
// when it is off-screen.
func (g *Grid) ContentRowToViewport(contentRow int) (int, bool) {
	sb := len(g.Scrollback)
	off := clamp(g.ViewOffset, 0, sb)
	n := min(off, g.Rows)
	if contentRow < sb {
		vr := contentRow - (sb - off)
		if vr >= 0 && vr < n {
			return vr, true
		}
		return 0, false
	}
	liveRow := contentRow - sb
	vr := liveRow + n
	if vr >= n && vr < g.Rows {
		return vr, true
	}
	return 0, false
}

// rowRunes returns the rune slice for a content row with length == g.Cols,
// so rune index == cell column. Continuation cells (Width==0, Ch==0) appear
// as NUL runes and will never match printable query characters.
func (g *Grid) rowRunes(contentRow int) []rune {
	sb := len(g.Scrollback)
	var src []Cell
	if contentRow < sb {
		if contentRow < 0 {
			return nil
		}
		src = g.Scrollback[contentRow]
	} else {
		liveRow := contentRow - sb
		if liveRow < 0 || liveRow >= g.Rows || g.Cols == 0 {
			return nil
		}
		base := liveRow * g.Cols
		src = g.Cells[base : base+g.Cols]
	}
	rr := make([]rune, len(src))
	for i, cell := range src {
		rr[i] = cell.Ch
	}
	return rr
}

// equalFoldRune reports whether a and b are equal under Unicode case-folding.
func equalFoldRune(a, b rune) bool {
	return unicode.ToLower(a) == unicode.ToLower(b)
}

// runeSliceSearch returns the first column index >= fromCol where needle
// occurs in haystack. Returns -1 when not found. Case-insensitive.
func runeSliceSearch(haystack, needle []rune, fromCol int) int {
	n, m := len(haystack), len(needle)
	if m == 0 || fromCol > n-m {
		return -1
	}
	if fromCol < 0 {
		fromCol = 0
	}
	for i := fromCol; i <= n-m; i++ {
		match := true
		for j := 0; j < m; j++ {
			if !equalFoldRune(haystack[i+j], needle[j]) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// runeSliceSearchLast returns the rightmost column index < upToCol where
// needle occurs in haystack. Returns -1 when not found. Case-insensitive.
func runeSliceSearchLast(haystack, needle []rune, upToCol int) int {
	n, m := len(haystack), len(needle)
	if m == 0 || n < m {
		return -1
	}
	maxStart := n - m
	if upToCol-1 < maxStart {
		maxStart = upToCol - 1
	}
	if maxStart < 0 {
		return -1
	}
	for i := maxStart; i >= 0; i-- {
		match := true
		for j := 0; j < m; j++ {
			if !equalFoldRune(haystack[i+j], needle[j]) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// Find searches for query (case-insensitive) starting at start, walking
// forward or backward through all content rows (scrollback + live), wrapping
// once. Multi-row spanning is not supported; matches must fit within one row.
// Returns the ContentPos of the first cell of the match and true on success.
// Called under Mu.
func (g *Grid) Find(query string, start ContentPos, forward bool) (ContentPos, bool) {
	if query == "" || g.Cols <= 0 {
		return ContentPos{}, false
	}
	qRunes := []rune(query)
	if len(qRunes) > g.Cols {
		return ContentPos{}, false
	}
	total := g.ContentRows()
	if total == 0 {
		return ContentPos{}, false
	}
	start.Row = clamp(start.Row, 0, total-1)
	for i := 0; i < total; i++ {
		var row int
		if forward {
			row = (start.Row + i) % total
		} else {
			row = (start.Row - i + total) % total
		}
		rr := g.rowRunes(row)
		if forward {
			fromCol := 0
			if i == 0 {
				fromCol = start.Col + 1
			}
			if col := runeSliceSearch(rr, qRunes, fromCol); col >= 0 {
				return ContentPos{Row: row, Col: col}, true
			}
		} else {
			upToCol := len(rr) + 1
			if i == 0 {
				upToCol = start.Col
			}
			if col := runeSliceSearchLast(rr, qRunes, upToCol); col >= 0 {
				return ContentPos{Row: row, Col: col}, true
			}
		}
	}
	return ContentPos{}, false
}

// ViewportMatches returns the content positions of all query matches visible
// at the current ViewOffset. Returns nil for an empty query, a zero-column
// grid, or while the alt screen is active. Called under Mu.
func (g *Grid) ViewportMatches(query string) []ContentPos {
	if query == "" || g.Cols <= 0 || g.AltActive {
		return nil
	}
	qRunes := []rune(query)
	if len(qRunes) > g.Cols {
		return nil
	}
	sb := len(g.Scrollback)
	off := clamp(g.ViewOffset, 0, sb)
	n := min(off, g.Rows)
	var matches []ContentPos
	for vr := range g.Rows {
		var contentRow int
		if vr < n {
			contentRow = sb - off + vr
		} else {
			contentRow = sb + (vr - n)
		}
		rr := g.rowRunes(contentRow)
		col := 0
		for {
			idx := runeSliceSearch(rr, qRunes, col)
			if idx < 0 {
				break
			}
			matches = append(matches, ContentPos{Row: contentRow, Col: idx})
			col = idx + 1
		}
	}
	return matches
}
