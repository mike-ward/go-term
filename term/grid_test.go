package term

import (
	"math"
	"testing"
)

func TestClampDim(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{-1, 1},
		{0, 1},
		{1, 1},
		{MaxGridDim, MaxGridDim},
		{MaxGridDim + 1, MaxGridDim},
		{math.MaxInt32, MaxGridDim},
	}
	for _, c := range cases {
		if got := clampDim(c.in); got != c.want {
			t.Errorf("clampDim(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestNewGrid_DefaultsAndClamping(t *testing.T) {
	g := NewGrid(0, 0)
	if g.Rows != 1 || g.Cols != 1 {
		t.Errorf("zero dims not clamped: %dx%d", g.Rows, g.Cols)
	}
	if g.CurFG != DefaultColor || g.CurBG != DefaultColor {
		t.Errorf("default colors wrong: fg=%d bg=%d", g.CurFG, g.CurBG)
	}
	g = NewGrid(MaxGridDim+10, MaxGridDim+10)
	if g.Rows != MaxGridDim || g.Cols != MaxGridDim {
		t.Errorf("oversize dims not clamped: %dx%d", g.Rows, g.Cols)
	}
	g = NewGrid(2, 3)
	for i, c := range g.Cells {
		if c.Ch != ' ' || c.FG != DefaultColor || c.BG != DefaultColor {
			t.Fatalf("cell[%d] not default: %+v", i, c)
		}
	}
}

func TestGrid_AtBounds(t *testing.T) {
	g := NewGrid(2, 3)
	if g.At(-1, 0) != nil || g.At(0, -1) != nil {
		t.Error("negative index should return nil")
	}
	if g.At(2, 0) != nil || g.At(0, 3) != nil {
		t.Error("out-of-range index should return nil")
	}
	if g.At(0, 0) == nil || g.At(1, 2) == nil {
		t.Error("in-range index should not return nil")
	}
}

func TestGrid_PutBasic(t *testing.T) {
	g := NewGrid(2, 3)
	g.Put('a')
	g.Put('b')
	if g.At(0, 0).Ch != 'a' || g.At(0, 1).Ch != 'b' {
		t.Errorf("put failed: %v %v", g.At(0, 0).Ch, g.At(0, 1).Ch)
	}
	if g.CursorC != 2 {
		t.Errorf("cursor advance: got %d want 2", g.CursorC)
	}
}

func TestGrid_PutWrapsAndScrollsAtBottomRight(t *testing.T) {
	g := NewGrid(2, 2)
	g.Put('a')
	g.Put('b')
	g.Put('c') // wraps + newline (no scroll, only 2 rows)
	g.Put('d')
	g.Put('e') // wraps + scrolls
	if g.At(0, 0).Ch != 'c' || g.At(0, 1).Ch != 'd' {
		t.Errorf("scroll lost row: %v %v", g.At(0, 0).Ch, g.At(0, 1).Ch)
	}
	if g.At(1, 0).Ch != 'e' {
		t.Errorf("e not at row 1 col 0: %v", g.At(1, 0).Ch)
	}
}

func TestGrid_Newline(t *testing.T) {
	g := NewGrid(3, 2)
	g.CursorC = 1
	g.Newline()
	if g.CursorR != 1 || g.CursorC != 1 {
		t.Errorf("newline column should not change: r=%d c=%d", g.CursorR, g.CursorC)
	}
	g.CursorR = 2
	g.CursorC = 0
	g.Put('x')
	g.Newline() // scrolls
	if g.At(1, 0).Ch != 'x' {
		t.Errorf("scroll not preserving x: %v", g.At(1, 0).Ch)
	}
}

func TestGrid_CarriageReturn(t *testing.T) {
	g := NewGrid(1, 5)
	g.CursorC = 3
	g.CarriageReturn()
	if g.CursorC != 0 {
		t.Errorf("CR should reset col: %d", g.CursorC)
	}
}

func TestGrid_Backspace(t *testing.T) {
	g := NewGrid(1, 5)
	g.Backspace()
	if g.CursorC != 0 {
		t.Errorf("backspace at 0 should noop: %d", g.CursorC)
	}
	g.CursorC = 3
	g.Backspace()
	if g.CursorC != 2 {
		t.Errorf("backspace 3->2: %d", g.CursorC)
	}
}

func TestGrid_Tab(t *testing.T) {
	g := NewGrid(1, 20)
	g.Tab()
	if g.CursorC != 8 {
		t.Errorf("tab from 0: %d", g.CursorC)
	}
	g.CursorC = 9
	g.Tab()
	if g.CursorC != 16 {
		t.Errorf("tab from 9: %d", g.CursorC)
	}
	g.CursorC = 17
	g.Tab()
	if g.CursorC != 19 {
		t.Errorf("tab clamp at right margin: %d", g.CursorC)
	}
}

func TestGrid_TabNegativeCursor(t *testing.T) {
	g := NewGrid(1, 20)
	g.CursorC = -5
	g.Tab()
	if g.CursorC != 8 {
		t.Errorf("tab from negative should normalize: %d", g.CursorC)
	}
}

func TestGrid_ClearAll(t *testing.T) {
	g := NewGrid(2, 2)
	g.Put('x')
	g.Put('y')
	g.ClearAll()
	if g.CursorR != 0 || g.CursorC != 0 {
		t.Errorf("clear should home cursor")
	}
	for i, c := range g.Cells {
		if c.Ch != ' ' {
			t.Fatalf("cell[%d] not cleared: %v", i, c.Ch)
		}
	}
}

func TestGrid_MoveCursorClamps(t *testing.T) {
	g := NewGrid(3, 4)
	g.MoveCursor(-1, -1)
	if g.CursorR != 0 || g.CursorC != 0 {
		t.Errorf("clamp low: %d %d", g.CursorR, g.CursorC)
	}
	g.MoveCursor(99, 99)
	if g.CursorR != 2 || g.CursorC != 3 {
		t.Errorf("clamp high: %d %d", g.CursorR, g.CursorC)
	}
}

func TestGrid_CursorMoveByLargeNClamps(t *testing.T) {
	g := NewGrid(5, 5)
	g.MoveCursor(2, 2)
	g.CursorUp(100)
	if g.CursorR != 0 {
		t.Errorf("up: %d", g.CursorR)
	}
	g.CursorDown(100)
	if g.CursorR != 4 {
		t.Errorf("down: %d", g.CursorR)
	}
	g.CursorBack(100)
	if g.CursorC != 0 {
		t.Errorf("back: %d", g.CursorC)
	}
	g.CursorForward(100)
	if g.CursorC != 4 {
		t.Errorf("forward: %d", g.CursorC)
	}
}

func TestGrid_Resize_Shrink(t *testing.T) {
	g := NewGrid(3, 3)
	g.Put('a')
	g.Put('b')
	g.Put('c')
	g.Resize(2, 2)
	if g.At(0, 0).Ch != 'a' || g.At(0, 1).Ch != 'b' {
		t.Errorf("shrink should preserve top-left: %v %v",
			g.At(0, 0).Ch, g.At(0, 1).Ch)
	}
}

func TestGrid_Resize_Grow(t *testing.T) {
	g := NewGrid(2, 2)
	g.Put('x')
	g.Resize(4, 5)
	if g.At(0, 0).Ch != 'x' {
		t.Errorf("grow should preserve content: %v", g.At(0, 0).Ch)
	}
	if g.At(3, 4).Ch != ' ' || g.At(3, 4).FG != DefaultColor {
		t.Errorf("new cell not default: %+v", *g.At(3, 4))
	}
}

func TestGrid_Resize_Clamp(t *testing.T) {
	g := NewGrid(2, 2)
	g.Resize(MaxGridDim+5, MaxGridDim+5)
	if g.Rows != MaxGridDim || g.Cols != MaxGridDim {
		t.Errorf("resize not clamped: %dx%d", g.Rows, g.Cols)
	}
}

func TestGrid_Resize_ClampsCursor(t *testing.T) {
	g := NewGrid(10, 10)
	g.MoveCursor(9, 9)
	g.Resize(5, 5)
	if g.CursorR != 4 || g.CursorC != 4 {
		t.Errorf("cursor not clamped: %d %d", g.CursorR, g.CursorC)
	}
}

func TestGrid_EraseInLine_Modes(t *testing.T) {
	g := NewGrid(1, 5)
	for i := range g.Cols {
		g.At(0, i).Ch = rune('a' + i)
	}
	g.CursorC = 2
	g.EraseInLine(0)
	if g.At(0, 1).Ch != 'b' || g.At(0, 2).Ch != ' ' || g.At(0, 4).Ch != ' ' {
		t.Errorf("mode 0 wrong: %+v", g.Cells)
	}

	g = NewGrid(1, 5)
	for i := range g.Cols {
		g.At(0, i).Ch = rune('a' + i)
	}
	g.CursorC = 2
	g.EraseInLine(1)
	if g.At(0, 0).Ch != ' ' || g.At(0, 2).Ch != ' ' || g.At(0, 3).Ch != 'd' {
		t.Errorf("mode 1 wrong: %+v", g.Cells)
	}

	g = NewGrid(1, 5)
	for i := range g.Cols {
		g.At(0, i).Ch = rune('a' + i)
	}
	g.EraseInLine(2)
	for i := range g.Cols {
		if g.At(0, i).Ch != ' ' {
			t.Errorf("mode 2 col %d: %v", i, g.At(0, i).Ch)
		}
	}

	// invalid mode is a no-op
	g = NewGrid(1, 3)
	g.At(0, 0).Ch = 'z'
	g.EraseInLine(99)
	if g.At(0, 0).Ch != 'z' {
		t.Errorf("invalid mode mutated grid")
	}
}

func TestGrid_EraseInLine_UsesCurAttrs(t *testing.T) {
	g := NewGrid(1, 3)
	g.CurAttrs = AttrUnderline
	g.CurFG = 1
	g.CurBG = 2
	g.EraseInLine(2)
	c := g.At(0, 0)
	if c.Attrs != AttrUnderline || c.FG != 1 || c.BG != 2 {
		t.Errorf("blank attrs not propagated: %+v", *c)
	}
}

func TestGrid_EraseInDisplay_Modes(t *testing.T) {
	mk := func() *Grid {
		g := NewGrid(3, 3)
		for r := range g.Rows {
			for c := range g.Cols {
				g.At(r, c).Ch = 'x'
			}
		}
		return g
	}

	g := mk()
	g.MoveCursor(1, 1)
	g.EraseInDisplay(0)
	if g.At(0, 0).Ch != 'x' || g.At(1, 0).Ch != 'x' {
		t.Errorf("mode 0: above cursor should remain")
	}
	if g.At(1, 1).Ch != ' ' || g.At(2, 2).Ch != ' ' {
		t.Errorf("mode 0: from cursor should clear")
	}

	g = mk()
	g.MoveCursor(1, 1)
	g.EraseInDisplay(1)
	if g.At(0, 0).Ch != ' ' || g.At(1, 1).Ch != ' ' {
		t.Errorf("mode 1: up-to-cursor should clear")
	}
	if g.At(1, 2).Ch != 'x' || g.At(2, 2).Ch != 'x' {
		t.Errorf("mode 1: after cursor should remain")
	}

	for _, mode := range []int{2, 3} {
		g = mk()
		g.EraseInDisplay(mode)
		for _, c := range g.Cells {
			if c.Ch != ' ' {
				t.Errorf("mode %d should clear all: %v", mode, c.Ch)
			}
		}
	}
}

func TestGrid_ScrollUp(t *testing.T) {
	g := NewGrid(3, 2)
	for r := range g.Rows {
		for c := range g.Cols {
			g.At(r, c).Ch = rune('a' + r)
		}
	}
	g.scrollUp()
	if g.At(0, 0).Ch != 'b' || g.At(1, 0).Ch != 'c' {
		t.Errorf("scrollUp shift wrong: %v %v", g.At(0, 0).Ch, g.At(1, 0).Ch)
	}
	if g.At(2, 0).Ch != ' ' || g.At(2, 1).Ch != ' ' {
		t.Errorf("scrollUp last row not cleared: %v %v",
			g.At(2, 0).Ch, g.At(2, 1).Ch)
	}
}
