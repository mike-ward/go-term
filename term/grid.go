// Package term implements a minimal terminal-emulator widget for go-gui.
//
// Layers: grid (this file) holds the cell buffer and cursor; parser feeds
// bytes into it; pty spawns the shell; widget binds it all to a go-gui
// DrawCanvas.
package term

import "sync"

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

// Grid is a fixed-size character grid. All public methods are safe for
// concurrent callers via Mu; the parser writes under Mu, OnDraw reads
// under Mu.
type Grid struct {
	Mu       sync.Mutex
	Rows     int
	Cols     int
	Cells    []Cell // row-major, len = Rows*Cols
	CursorR  int
	CursorC  int
	CurFG    uint32 // packed Color
	CurBG    uint32
	CurAttrs uint8
}

// NewGrid allocates a rows×cols grid filled with default cells.
func NewGrid(rows, cols int) *Grid {
	rows = clampDim(rows)
	cols = clampDim(cols)
	g := &Grid{
		Rows:  rows,
		Cols:  cols,
		Cells: make([]Cell, rows*cols),
		CurFG: DefaultColor,
		CurBG: DefaultColor,
	}
	for i := range g.Cells {
		g.Cells[i] = defaultCell()
	}
	return g
}

// Resize reflows to new dims, copying the intersecting region from the
// top-left corner. New space is filled with default cells. Cursor is
// clamped.
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
	g.Rows = rows
	g.Cols = cols
	g.Cells = next
	if g.CursorR >= rows {
		g.CursorR = rows - 1
	}
	if g.CursorC >= cols {
		g.CursorC = cols - 1
	}
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

// scrollUp drops the top row and clears the bottom.
func (g *Grid) scrollUp() {
	copy(g.Cells, g.Cells[g.Cols:])
	last := g.Cells[(g.Rows-1)*g.Cols:]
	for i := range last {
		last[i] = defaultCell()
	}
}
