package term

import "testing"

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
	g.Put('c')
	g.Put('d')
	g.Put('e')
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
	g.Newline()
	if g.At(1, 0).Ch != 'x' {
		t.Errorf("scroll not preserving x: %v", g.At(1, 0).Ch)
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

func TestGrid_NewlineAtRegionBottom(t *testing.T) {
	g := NewGrid(5, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		fillRow(g, i, ch)
	}
	g.Top, g.Bottom = 1, 3
	g.CursorR = 3
	g.Newline()

	if g.CursorR != 3 {
		t.Errorf("cursor moved off Bottom: %d", g.CursorR)
	}
	if rowChar(g, 1) != 'C' || rowChar(g, 2) != 'D' || rowChar(g, 3) != ' ' {
		t.Errorf("region rows wrong after Newline at Bottom")
	}
	if rowChar(g, 0) != 'A' || rowChar(g, 4) != 'E' {
		t.Errorf("rows outside region disturbed")
	}
}

func TestGrid_NewlineBelowRegionDoesNotScroll(t *testing.T) {
	g := NewGrid(5, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		fillRow(g, i, ch)
	}
	g.Top, g.Bottom = 1, 3
	g.CursorR = 4
	g.Newline()
	if g.CursorR != 4 {
		t.Errorf("cursor moved past last row: %d", g.CursorR)
	}
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		if got := rowChar(g, i); got != ch {
			t.Errorf("row %d disturbed: got %q", i, got)
		}
	}
}

func TestGrid_InsertLines(t *testing.T) {
	g := NewGrid(5, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		fillRow(g, i, ch)
	}
	g.Top, g.Bottom = 1, 3
	g.CursorR, g.CursorC = 2, 1
	g.InsertLines(1)
	want := []rune{'A', 'B', ' ', 'C', 'E'}
	for i, w := range want {
		if got := rowChar(g, i); got != w {
			t.Errorf("row %d = %q, want %q", i, got, w)
		}
	}
	if g.CursorC != 0 {
		t.Errorf("InsertLines must home cursor column: %d", g.CursorC)
	}
}

func TestGrid_InsertLines_OutsideRegion(t *testing.T) {
	g := NewGrid(5, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		fillRow(g, i, ch)
	}
	g.Top, g.Bottom = 1, 3
	g.CursorR = 4
	g.InsertLines(2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		if got := rowChar(g, i); got != ch {
			t.Errorf("row %d disturbed by IL outside region: %q", i, got)
		}
	}
}

func TestGrid_DeleteLines(t *testing.T) {
	g := NewGrid(5, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		fillRow(g, i, ch)
	}
	g.Top, g.Bottom = 1, 3
	g.CursorR = 1
	g.DeleteLines(1)
	want := []rune{'A', 'C', 'D', ' ', 'E'}
	for i, w := range want {
		if got := rowChar(g, i); got != w {
			t.Errorf("row %d = %q, want %q", i, got, w)
		}
	}
}

func TestGrid_InsertChars(t *testing.T) {
	g := NewGrid(2, 6)
	for c := range g.Cols {
		g.At(0, c).Ch = rune('a' + c)
	}
	g.CursorR, g.CursorC = 0, 2
	g.InsertChars(2)
	got := []rune{
		g.At(0, 0).Ch, g.At(0, 1).Ch, g.At(0, 2).Ch,
		g.At(0, 3).Ch, g.At(0, 4).Ch, g.At(0, 5).Ch,
	}
	want := []rune{'a', 'b', ' ', ' ', 'c', 'd'}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("col %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestGrid_InsertChars_OverWidth(t *testing.T) {
	g := NewGrid(1, 4)
	for c := range g.Cols {
		g.At(0, c).Ch = rune('a' + c)
	}
	g.CursorC = 1
	g.InsertChars(99)
	for c := 1; c < g.Cols; c++ {
		if g.At(0, c).Ch != ' ' {
			t.Errorf("col %d not cleared: %q", c, g.At(0, c).Ch)
		}
	}
	if g.At(0, 0).Ch != 'a' {
		t.Errorf("col 0 disturbed: %q", g.At(0, 0).Ch)
	}
}

func TestGrid_DeleteChars(t *testing.T) {
	g := NewGrid(1, 6)
	for c := range g.Cols {
		g.At(0, c).Ch = rune('a' + c)
	}
	g.CursorC = 2
	g.DeleteChars(2)
	got := []rune{
		g.At(0, 0).Ch, g.At(0, 1).Ch, g.At(0, 2).Ch,
		g.At(0, 3).Ch, g.At(0, 4).Ch, g.At(0, 5).Ch,
	}
	want := []rune{'a', 'b', 'e', 'f', ' ', ' '}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("col %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRuneWidth_ASCII(t *testing.T) {
	cases := []struct {
		r    rune
		want int
	}{
		{' ', 1}, {'A', 1}, {'~', 1},
		{0x00, 0}, {0x07, 0}, {0x1F, 0},
	}
	for _, c := range cases {
		if got := runeWidth(c.r); got != c.want {
			t.Errorf("runeWidth(%U)=%d want %d", c.r, got, c.want)
		}
	}
}

func TestRuneWidth_CJKAndEmoji(t *testing.T) {
	cases := []struct {
		r    rune
		want int
	}{
		{'你', 2},
		{'好', 2},
		{0x1F600, 2},
		{'é', 1},
	}
	for _, c := range cases {
		if got := runeWidth(c.r); got != c.want {
			t.Errorf("runeWidth(%U)=%d want %d", c.r, got, c.want)
		}
	}
}

func TestGrid_Put_WideAdvancesTwoColumns(t *testing.T) {
	g := NewGrid(2, 8)
	g.Put('你')
	if g.CursorC != 2 {
		t.Errorf("after wide put, cursor C=%d, want 2", g.CursorC)
	}
	if c := g.At(0, 0); c.Ch != '你' || c.Width != 2 {
		t.Errorf("cell[0,0]: ch=%U width=%d", c.Ch, c.Width)
	}
	if c := g.At(0, 1); c.Ch != 0 || c.Width != 0 {
		t.Errorf("cell[0,1] continuation: ch=%U width=%d", c.Ch, c.Width)
	}
}

func TestGrid_Put_WideWrapsAtRightEdge(t *testing.T) {
	g := NewGrid(2, 4)
	g.Put('a')
	g.Put('b')
	g.Put('c')

	g.Put('你')
	if g.CursorR != 1 || g.CursorC != 2 {
		t.Errorf("post-wrap cursor: r=%d c=%d", g.CursorR, g.CursorC)
	}

	if c := g.At(0, 3); c.Ch != ' ' || c.Width != 1 {
		t.Errorf("padded cell[0,3]: ch=%U width=%d", c.Ch, c.Width)
	}
	if c := g.At(1, 0); c.Ch != '你' || c.Width != 2 {
		t.Errorf("wrapped wide head: ch=%U width=%d", c.Ch, c.Width)
	}
}

func TestGrid_Put_OverwriteWideHeadClearsContinuation(t *testing.T) {
	g := NewGrid(1, 5)
	g.Put('好')
	g.MoveCursor(0, 0)
	g.Put('x')
	if c := g.At(0, 0); c.Ch != 'x' || c.Width != 1 {
		t.Errorf("overwrote head: ch=%U width=%d", c.Ch, c.Width)
	}
	if c := g.At(0, 1); c.Ch != ' ' || c.Width != 1 {
		t.Errorf("orphaned continuation: ch=%U width=%d", c.Ch, c.Width)
	}
}

func TestGrid_Put_OverwriteContinuationClearsHead(t *testing.T) {
	g := NewGrid(1, 5)
	g.Put('好')
	g.MoveCursor(0, 1)
	g.Put('y')
	if c := g.At(0, 1); c.Ch != 'y' || c.Width != 1 {
		t.Errorf("overwrote continuation: ch=%U width=%d", c.Ch, c.Width)
	}
	if c := g.At(0, 0); c.Ch != ' ' || c.Width != 1 {
		t.Errorf("orphaned head: ch=%U width=%d", c.Ch, c.Width)
	}
}

func TestGrid_Put_DropsZeroWidth(t *testing.T) {
	g := NewGrid(1, 5)
	g.Put('a')
	startC := g.CursorC
	g.Put(0x0301)
	if g.CursorC != startC {
		t.Errorf("zero-width char advanced cursor: %d → %d",
			startC, g.CursorC)
	}
	if c := g.At(0, 0); c.Ch != 'a' {
		t.Errorf("zero-width char clobbered prior cell: ch=%U", c.Ch)
	}
}

func TestGrid_Put_WideThenNarrowLayout(t *testing.T) {
	g := NewGrid(1, 8)
	g.Put('你')
	g.Put('好')
	g.Put('!')
	want := []struct {
		ch rune
		w  uint8
	}{
		{'你', 2}, {0, 0}, {'好', 2}, {0, 0}, {'!', 1},
	}
	for i, exp := range want {
		c := g.At(0, i)
		if c.Ch != exp.ch || c.Width != exp.w {
			t.Errorf("col %d: ch=%U width=%d, want ch=%U width=%d",
				i, c.Ch, c.Width, exp.ch, exp.w)
		}
	}
}

func TestGrid_Put_SetsWrappedFlag(t *testing.T) {
	g := NewGrid(3, 4)

	for _, r := range "abcd" {
		g.Put(r)
	}
	if g.RowWrapped[0] {
		t.Error("RowWrapped[0] set before autowrap trigger")
	}

	g.Put('e')
	if !g.RowWrapped[0] {
		t.Error("RowWrapped[0] not set after autowrap")
	}
	if g.RowWrapped[1] {
		t.Error("RowWrapped[1] should be false after one more char")
	}
}

func TestGrid_Put_ExplicitNewlineNoWrappedFlag(t *testing.T) {
	g := NewGrid(3, 10)
	g.Put('a')
	g.Newline()
	if g.RowWrapped[0] {
		t.Error("RowWrapped[0] should be false after explicit Newline")
	}
}

func TestGrid_InsertLines_ShiftsWrappedFlags(t *testing.T) {
	g := NewGrid(4, 4)
	g.RowWrapped[0] = true
	g.RowWrapped[1] = false
	g.MoveCursor(0, 0)
	g.InsertLines(1)

	if g.RowWrapped[0] {
		t.Error("RowWrapped[0] should be false (new blank row)")
	}

	if !g.RowWrapped[1] {
		t.Error("RowWrapped[1] should be true (shifted from row 0)")
	}
}

func TestGrid_DeleteLines_ShiftsWrappedFlags(t *testing.T) {
	g := NewGrid(4, 4)
	g.RowWrapped[0] = true
	g.RowWrapped[1] = false
	g.MoveCursor(0, 0)
	g.DeleteLines(1)

	if g.RowWrapped[0] {
		t.Error("RowWrapped[0] should be false (was row 1, not wrapped)")
	}

	if g.RowWrapped[3] {
		t.Error("RowWrapped[3] should be false (blank fill row)")
	}
}

func TestGrid_Put_LinkID(t *testing.T) {
	g := NewGrid(5, 20)
	id := g.internLink("https://example.com")
	g.CurLinkID = id
	g.Put('A')
	if got := g.At(0, 0).LinkID; got != id {
		t.Errorf("cell.LinkID = %d, want %d", got, id)
	}
}

func TestGrid_Put_LinkID_ZeroAfterReset(t *testing.T) {
	g := NewGrid(5, 20)
	g.CurLinkID = 0
	g.Put('A')
	if got := g.At(0, 0).LinkID; got != 0 {
		t.Errorf("cell.LinkID = %d, want 0", got)
	}
}

func TestGrid_Bell_IncrementsCount(t *testing.T) {
	g := NewGrid(5, 20)
	if g.BellCount != 0 {
		t.Fatalf("initial BellCount = %d, want 0", g.BellCount)
	}
	g.Bell()
	if g.BellCount != 1 {
		t.Fatalf("BellCount after 1 bell = %d, want 1", g.BellCount)
	}
	g.Bell()
	g.Bell()
	if g.BellCount != 3 {
		t.Fatalf("BellCount after 3 bells = %d, want 3", g.BellCount)
	}
}

func TestGrid_Put_PropagatesULStyle(t *testing.T) {
	g := NewGrid(2, 10)
	g.CurULStyle = ULCurly
	g.CurULColor = rgbColor(255, 0, 128)
	g.Put('X')
	cell := g.At(0, 0)
	if cell == nil {
		t.Fatal("At(0,0) returned nil")
	}
	if cell.ULStyle != ULCurly {
		t.Errorf("Put: ULStyle = %d, want ULCurly (%d)", cell.ULStyle, ULCurly)
	}
	if cell.ULColor != rgbColor(255, 0, 128) {
		t.Errorf("Put: ULColor = %#x, want %#x", cell.ULColor, rgbColor(255, 0, 128))
	}
}

func TestGrid_Put_BlankCellNoUL(t *testing.T) {

	g := NewGrid(2, 10)
	g.CurULStyle = ULDashed
	g.EraseInLine(2)
	for c := range 10 {
		cell := g.At(0, c)
		if cell == nil {
			continue
		}
		if cell.ULStyle != ULNone {
			t.Errorf("erased cell[0,%d]: ULStyle = %d, want 0", c, cell.ULStyle)
		}
	}
}

func TestGrid_TabDefaultStops(t *testing.T) {
	g := NewGrid(1, 80)

	for _, want := range []int{8, 16, 24, 32} {
		if !g.TabStops[want] {
			t.Errorf("default stop missing at col %d", want)
		}
	}

	if g.TabStops[0] {
		t.Error("col 0 should not be a default stop")
	}
}

func TestGrid_Tab_AdvancesToNextStop(t *testing.T) {
	g := NewGrid(1, 80)
	g.CursorC = 0
	g.Tab()
	if g.CursorC != 8 {
		t.Errorf("Tab from 0: got %d, want 8", g.CursorC)
	}
	g.Tab()
	if g.CursorC != 16 {
		t.Errorf("Tab from 8: got %d, want 16", g.CursorC)
	}
}

func TestGrid_Tab_ClampsWhenNoStop(t *testing.T) {

	g := NewGrid(1, 5)
	g.CursorC = 0
	g.Tab()
	if g.CursorC != 4 {
		t.Errorf("Tab with no stop: got %d, want Cols-1=4", g.CursorC)
	}
}

func TestGrid_SetTabStop(t *testing.T) {
	g := NewGrid(1, 80)
	g.CursorC = 5
	g.SetTabStop()
	if !g.TabStops[5] {
		t.Error("SetTabStop: stop not set at col 5")
	}

	g.CursorC = 0
	g.Tab()
	if g.CursorC != 5 {
		t.Errorf("Tab after SetTabStop(5): got %d, want 5", g.CursorC)
	}
	g.Tab()
	if g.CursorC != 8 {
		t.Errorf("Tab after SetTabStop(5) from 5: got %d, want 8", g.CursorC)
	}
}

func TestGrid_ClearTabStop_AtCursor(t *testing.T) {
	g := NewGrid(1, 80)

	g.CursorC = 8
	g.ClearTabStop(false)
	if g.TabStops[8] {
		t.Error("ClearTabStop(false): stop at 8 should be cleared")
	}

	g.CursorC = 0
	g.Tab()
	if g.CursorC != 16 {
		t.Errorf("Tab after clearing stop at 8: got %d, want 16", g.CursorC)
	}
}

func TestGrid_ClearTabStop_All(t *testing.T) {
	g := NewGrid(1, 80)
	g.ClearTabStop(true)
	for c := 0; c < MaxGridDim; c++ {
		if g.TabStops[c] {
			t.Errorf("ClearTabStop(true): stop still set at col %d", c)
		}
	}

	g.CursorC = 0
	g.Tab()
	if g.CursorC != g.Cols-1 {
		t.Errorf("Tab with all stops cleared: got %d, want %d", g.CursorC, g.Cols-1)
	}
}

func TestGrid_DirtyTracking_PutMarksDirty(t *testing.T) {
	g := NewGrid(5, 10)
	g.CursorR, g.CursorC = 2, 0
	g.ClearDirty()
	g.Put('A')
	if !g.Dirty[2] {
		t.Error("Put should mark cursor row dirty")
	}
	for r := range g.Rows {
		if r != 2 && g.Dirty[r] {
			t.Errorf("row %d should not be dirty after Put at row 2", r)
		}
	}
}

func TestGrid_DirtyTracking_EraseInLineMarksDirty(t *testing.T) {
	g := NewGrid(5, 10)
	g.CursorR, g.CursorC = 3, 0
	g.ClearDirty()
	g.EraseInLine(2)
	if !g.Dirty[3] {
		t.Error("EraseInLine should mark cursor row dirty")
	}
}
