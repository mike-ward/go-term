package term

import "testing"

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

func TestGrid_DECSCUSRParam_RoundTrip(t *testing.T) {
	cases := []struct{ ps, want int }{
		{1, 1}, {2, 2}, {3, 3}, {4, 4}, {5, 5}, {6, 6},
	}
	for _, c := range cases {
		g := NewGrid(1, 5)
		g.ApplyDECSCUSR(c.ps)
		if got := g.DECSCUSRParam(); got != c.want {
			t.Errorf("ApplyDECSCUSR(%d) → DECSCUSRParam() = %d, want %d", c.ps, got, c.want)
		}
	}
}

func TestGrid_MoveCursorOrigin_WhenOriginModeOff(t *testing.T) {
	g := NewGrid(5, 8)
	g.SetScrollRegion(1, 3)

	g.MoveCursorOrigin(2, 3)
	if g.CursorR != 2 || g.CursorC != 3 {
		t.Errorf("cursor = %d,%d, want 2,3", g.CursorR, g.CursorC)
	}
}

func TestGrid_SaveRestoreCursor_ULState(t *testing.T) {
	g := NewGrid(2, 10)
	g.CurULStyle = ULDouble
	g.CurULColor = rgbColor(0, 128, 255)
	g.SaveCursor()

	g.CurULStyle = ULDotted
	g.CurULColor = DefaultColor

	g.RestoreCursor()
	if g.CurULStyle != ULDouble {
		t.Errorf("RestoreCursor: CurULStyle = %d, want ULDouble (%d)", g.CurULStyle, ULDouble)
	}
	if g.CurULColor != rgbColor(0, 128, 255) {
		t.Errorf("RestoreCursor: CurULColor = %#x, want %#x", g.CurULColor, rgbColor(0, 128, 255))
	}
}

func TestGrid_SaveRestoreCursor_CharsetState(t *testing.T) {
	g := NewGrid(2, 10)
	g.CharsetG0 = 'B'
	g.CharsetG1 = '0'
	g.ActiveG = 1
	g.SaveCursor()

	g.CharsetG0 = '0'
	g.CharsetG1 = 'B'
	g.ActiveG = 0

	g.RestoreCursor()
	if g.CharsetG0 != 'B' || g.CharsetG1 != '0' || g.ActiveG != 1 {
		t.Fatalf("RestoreCursor charsets: G0=%q G1=%q active=%d", g.CharsetG0, g.CharsetG1, g.ActiveG)
	}
}

func TestGrid_CursorMovementMarksDirty(t *testing.T) {
	g := NewGrid(10, 10)
	g.ClearDirty()

	g.MoveCursor(5, 5)
	if !g.Dirty[0] {
		t.Errorf("MoveCursor: old row 0 not marked dirty")
	}
	if !g.Dirty[5] {
		t.Errorf("MoveCursor: new row 5 not marked dirty")
	}
	if !g.HasDirtyRows() {
		t.Errorf("MoveCursor: HasDirtyRows should be true")
	}

	g.ClearDirty()
	g.CursorForward(2)
	if !g.Dirty[5] {
		t.Errorf("CursorForward: row 5 not marked dirty")
	}

	g.ClearDirty()
	g.CursorDown(1)
	if !g.Dirty[5] || !g.Dirty[6] {
		t.Errorf("CursorDown: rows 5 and 6 not marked dirty")
	}

	g.ClearDirty()
	g.Newline()
	if !g.Dirty[6] || !g.Dirty[7] {
		t.Errorf("Newline: rows 6 and 7 not marked dirty")
	}

	g.ClearDirty()
	g.ReverseIndex()
	if !g.Dirty[6] || !g.Dirty[7] {
		t.Errorf("ReverseIndex: rows 6 and 7 not marked dirty")
	}
}
