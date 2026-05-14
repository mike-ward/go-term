package term

// CursorShape selects the cursor glyph: filled block, baseline
// underline, or vertical bar at the leading edge of the cell.
type CursorShape uint8

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
	g.markDirty(g.CursorR)
	g.CursorR, g.CursorC = r, c
	g.markDirty(r)
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
	g.markDirty(g.CursorR)
	g.CursorR, g.CursorC = r, c
	g.markDirty(r)
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

func (g *Grid) CursorBack(n int) { g.MoveCursor(g.CursorR, g.CursorC-n) }

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
