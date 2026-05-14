package term

import (
	"math"
	"sync"

	"github.com/mike-ward/go-gui/gui"
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

const (
	CursorBlock CursorShape = iota
	CursorUnderline
	CursorBar
)

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

const (
	MarkPromptStart  MarkKind = iota // OSC 133;A — beginning of prompt
	MarkCommandStart                 // OSC 133;B — start of user input
	MarkOutputStart                  // OSC 133;C — command submitted, output begins
	MarkCommandEnd                   // OSC 133;D — command finished (optional exit code)
)

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
	// CursorColor is the fill color for the block cursor, set via OSC 12.
	// DefaultColor means "invert the cell under the cursor" (the default).
	CursorColor uint32

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

	// Scrollback ring of rows that have scrolled off the top. Index 0
	// is oldest, Len()-1 is newest. Cap of 0 disables scrollback (rows
	// are dropped on scrollUp). ViewOffset > 0 freezes the viewport at
	// `ViewOffset` rows back from live; OnDraw renders accordingly.
	Scrollback    scrollbackRing
	ScrollbackCap int
	ViewOffset    int

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

	// Graphics holds decoded images (Phase 32). Origin is in content
	// coordinates so images travel through scrollback alongside the
	// text near them. Capped at maxGraphics; oldest evicted first.
	Graphics []Graphic

	// CellPxW, CellPxH are advisory cell-pixel sizes set by the widget
	// after its first measurement (under Mu in onDraw). Used to convert
	// pixel-space image dimensions into cell-space cursor advancement
	// at AddGraphic time. Zero before the first measurement.
	CellPxW, CellPxH float32
}

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

// viewportToContent converts a viewport row (0..Rows-1) to its content row
// (0..len(Scrollback)+Rows-1) at the current ViewOffset. Caller must hold Mu.
func (g *Grid) viewportToContent(r int) int {
	sb := g.Scrollback.Len()
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

func (g *Grid) markDirty(r int) {
	if r >= 0 && r < len(g.Dirty) {
		g.Dirty[r] = true
	}
}

// trimGraphics drops `extra` rows from the front of all graphic origins,
// discarding any whose covered range falls entirely above row 0. Called
// after scrollback is trimmed. Caller holds Mu.
func (g *Grid) trimGraphics(extra int) {
	if extra <= 0 || len(g.Graphics) == 0 {
		return
	}
	j := 0
	for _, gr := range g.Graphics {
		gr.OriginR -= extra
		if gr.OriginR+gr.Rows > 0 {
			g.Graphics[j] = gr
			j++
		}
	}
	g.Graphics = g.Graphics[:j]
}

// shiftGraphics applies delta to all graphic origin rows, dropping those
// that fall entirely outside [0, total). Called after a resize changes
// scrollback depth. Caller holds Mu.
func (g *Grid) shiftGraphics(delta, total int) {
	if len(g.Graphics) == 0 {
		return
	}
	j := 0
	for _, gr := range g.Graphics {
		gr.OriginR += delta
		if gr.OriginR+gr.Rows > 0 && gr.OriginR < total {
			g.Graphics[j] = gr
			j++
		}
	}
	g.Graphics = g.Graphics[:j]
}

// AddGraphic registers a decoded image at the cursor's current content
// position and blanks the cells it covers. cellPxW/cellPxH from the
// most recent measurement determine the cell rectangle; if those are
// zero (no frame drawn yet) a single-cell footprint is used. Caller
// holds Mu.
func (g *Grid) AddGraphic(src string, widthPx, heightPx int) (int, int) {
	if src == "" || widthPx <= 0 || heightPx <= 0 {
		return 0, 0
	}
	cols, rows := 1, 1
	if g.CellPxW > 0 && g.CellPxH > 0 {
		cols = int(math.Ceil(float64(widthPx) / float64(g.CellPxW)))
		rows = int(math.Ceil(float64(heightPx) / float64(g.CellPxH)))
		if cols < 1 {
			cols = 1
		}
		if rows < 1 {
			rows = 1
		}
	}
	originR := g.Scrollback.Len() + g.CursorR
	originC := g.CursorC
	if originC+cols > g.Cols {
		cols = g.Cols - originC
		if cols <= 0 {
			return 0, 0
		}
	}
	if len(g.Graphics) >= maxGraphics {
		copy(g.Graphics, g.Graphics[1:])
		g.Graphics = g.Graphics[:maxGraphics-1]
	}
	g.Graphics = append(g.Graphics, Graphic{
		Src:      src,
		OriginR:  originR,
		OriginC:  originC,
		Cols:     cols,
		Rows:     rows,
		WidthPx:  widthPx,
		HeightPx: heightPx,
	})
	blank := blankCell(DefaultColor, DefaultColor, 0)
	for r := range rows {
		lr := g.CursorR + r
		if lr < 0 || lr >= g.Rows {
			continue
		}
		for c := range cols {
			cc := originC + c
			if cc < 0 || cc >= g.Cols {
				continue
			}
			g.Cells[lr*g.Cols+cc] = blank
		}
		g.RowWrapped[lr] = false
		g.markDirty(lr)
	}
	return cols, rows
}

func (g *Grid) markAllDirty() {
	for i := range g.Dirty {
		g.Dirty[i] = true
	}
}

// SetDynColor updates the OSC dynamic color for ps (10=foreground,
// 11=background, 12=cursor). c must be an rgbColor-tagged packed value.
// Marks all rows dirty so the next render picks up the change.
// Called from the parser while Mu is held.
func (g *Grid) SetDynColor(ps int, c uint32) {
	col := gui.RGB(uint8(c>>16), uint8(c>>8), uint8(c))
	switch ps {
	case 10:
		g.Theme.DefaultFG = col
	case 11:
		g.Theme.DefaultBG = col
	case 12:
		g.CursorColor = c
	}
	g.markAllDirty()
}

// dynColorRGB returns the r,g,b components of the dynamic color for ps.
// 10=foreground, 11=background, 12=cursor (falls back to DefaultFG when
// CursorColor is unset). Called from the parser while Mu is held.
func (g *Grid) dynColorRGB(ps int) (r, gr, b uint8) {
	switch ps {
	case 10:
		c := g.Theme.DefaultFG
		return c.R, c.G, c.B
	case 11:
		c := g.Theme.DefaultBG
		return c.R, c.G, c.B
	default:
		if g.CursorColor != DefaultColor {
			return uint8(g.CursorColor >> 16), uint8(g.CursorColor >> 8), uint8(g.CursorColor)
		}
		c := g.Theme.DefaultFG
		return c.R, c.G, c.B
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
		CursorColor:   DefaultColor,
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
		g.nextLink = 1
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

// At returns a pointer to the cell at (r,c) or nil if out of range.
func (g *Grid) At(r, c int) *Cell {
	if r < 0 || c < 0 || r >= g.Rows || c >= g.Cols {
		return nil
	}
	return &g.Cells[r*g.Cols+c]
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

// ReverseIndex moves the cursor up one row, scrolling the region down
// when at Top. Above Top (outside region) the cursor moves up without
// scrolling. Implements ESC M (RI).
func (g *Grid) ReverseIndex() {
	switch {
	case g.CursorR == g.Top:
		g.scrollDownRegion(1)
	case g.CursorR > 0:
		g.markDirty(g.CursorR)
		g.CursorR--
		g.markDirty(g.CursorR)
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

// ViewCellAt returns the cell visible at viewport position (r, c)
// honoring ViewOffset. When the viewport row falls inside scrollback,
// that row's stored cells are returned. Outside-range coords yield a
// default cell (never panics). Resize keeps scrollback row widths in
// sync with Cols, so no per-row width clamp is needed here.
func (g *Grid) ViewCellAt(r, c int) Cell {
	if r < 0 || r >= g.Rows || c < 0 || c >= g.Cols {
		return defaultCell()
	}
	sb := g.Scrollback.Len()
	off := clamp(g.ViewOffset, 0, sb)
	if off == 0 {
		return g.Cells[r*g.Cols+c]
	}
	n := min(off, g.Rows)
	if r < n {
		return g.Scrollback.Row(sb - off + r)[c]
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
func (g *Grid) ContentRows() int { return g.Scrollback.Len() + g.Rows }

// ContentCellAt returns the cell at content-coordinate (row, col).
// Bounds-safe: out-of-range inputs return a default cell (never panics).
// Caller must hold Mu.
func (g *Grid) ContentCellAt(row, col int) Cell {
	sb := g.Scrollback.Len()
	if row < 0 || row >= sb+g.Rows || col < 0 || col >= g.Cols {
		return defaultCell()
	}
	if row < sb {
		return g.Scrollback.Row(row)[col]
	}
	return g.Cells[(row-sb)*g.Cols+col]
}

// ContentRowToViewport maps a content row to its viewport row at the current
// ViewOffset. Returns (vr, true) when the content row is visible, (0, false)
// when it is off-screen.
func (g *Grid) ContentRowToViewport(contentRow int) (int, bool) {
	sb := g.Scrollback.Len()
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
	sb := g.Scrollback.Len()
	var src []Cell
	if contentRow < sb {
		if contentRow < 0 {
			return nil
		}
		src = g.Scrollback.Row(contentRow)
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
