package term

import "testing"

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

func TestGrid_Resize_ReflowsScrollback(t *testing.T) {

	g := NewGrid(2, 4)
	g.ScrollbackCap = 10

	for _, r := range "abcd" {
		g.Put(r)
	}

	g.scrollUpRegion(1)
	if g.Scrollback.Len() != 1 {
		t.Fatalf("setup: scrollback len %d, want 1", g.Scrollback.Len())
	}

	g.Resize(2, 2)
	if g.Scrollback.Len() == 0 {
		t.Fatalf("shrink: scrollback empty, want at least 1 row")
	}
	if len(g.Scrollback.Row(0)) != 2 {
		t.Errorf("shrink: scrollback[0] width %d, want 2", len(g.Scrollback.Row(0)))
	}
	if g.Scrollback.Row(0)[0].Ch != 'a' || g.Scrollback.Row(0)[1].Ch != 'b' {
		t.Errorf("shrink: scrollback[0] = %v %v, want a b",
			g.Scrollback.Row(0)[0].Ch, g.Scrollback.Row(0)[1].Ch)
	}
	if g.At(0, 0).Ch != 'c' || g.At(0, 1).Ch != 'd' {
		t.Errorf("shrink: live[0] = %v %v, want c d",
			g.At(0, 0).Ch, g.At(0, 1).Ch)
	}

	g.Resize(2, 5)
	if g.At(0, 0).Ch != 'a' || g.At(0, 1).Ch != 'b' ||
		g.At(0, 2).Ch != 'c' || g.At(0, 3).Ch != 'd' {
		t.Errorf("grow: live[0] = %v%v%v%v, want abcd",
			g.At(0, 0).Ch, g.At(0, 1).Ch, g.At(0, 2).Ch, g.At(0, 3).Ch)
	}
	if c := g.At(0, 4); c.Ch != ' ' || c.FG != DefaultColor || c.BG != DefaultColor {
		t.Errorf("grow: live[0][4] not default blank: %+v", *c)
	}
}

func TestGrid_Resize_AdjustsSelectionByScrollbackDelta(t *testing.T) {

	g := NewGrid(4, 4)
	g.ScrollbackCap = 10
	g.SelAnchor = ContentPos{Row: 0, Col: 0}
	g.SelHead = ContentPos{Row: 3, Col: 3}
	g.SelActive = true
	g.Resize(2, 2)
	if !g.SelActive {
		t.Error("Resize should preserve active selection (Phase 17)")
	}

	total := g.Scrollback.Len() + g.Rows
	if g.SelAnchor.Row < 0 || g.SelAnchor.Row >= total {
		t.Errorf("SelAnchor.Row %d out of [0,%d)", g.SelAnchor.Row, total)
	}
	if g.SelHead.Row < 0 || g.SelHead.Row >= total {
		t.Errorf("SelHead.Row %d out of [0,%d)", g.SelHead.Row, total)
	}
}

func TestGrid_ResizeResetsRegion(t *testing.T) {
	g := NewGrid(10, 4)
	g.SetScrollRegion(2, 5)
	g.Resize(8, 4)
	if g.Top != 0 || g.Bottom != 7 {
		t.Errorf("Resize did not reset region: %d..%d", g.Top, g.Bottom)
	}
}

func TestGrid_Resize_Reflow_GrowWidth(t *testing.T) {

	g := NewGrid(3, 5)
	for _, r := range "helloworld" {
		g.Put(r)
	}

	if g.At(0, 0).Ch != 'h' || g.At(1, 0).Ch != 'w' {
		t.Fatalf("setup: row0[0]=%c row1[0]=%c", g.At(0, 0).Ch, g.At(1, 0).Ch)
	}
	if !g.RowWrapped[0] {
		t.Fatal("setup: RowWrapped[0] not set")
	}

	g.Resize(3, 10)

	want := "helloworld"
	for i, r := range want {
		if g.At(0, i).Ch != r {
			t.Errorf("col %d: got %c, want %c", i, g.At(0, i).Ch, r)
		}
	}
	if g.RowWrapped[0] {
		t.Error("RowWrapped[0] should be false after join")
	}
}

func TestGrid_Resize_Reflow_ShrinkWidth(t *testing.T) {

	g := NewGrid(3, 10)
	for _, r := range "helloworld" {
		g.Put(r)
	}

	g.Resize(3, 5)

	if g.At(0, 0).Ch != 'h' || g.At(0, 4).Ch != 'o' {
		t.Errorf("row0 = %c..%c, want h..o", g.At(0, 0).Ch, g.At(0, 4).Ch)
	}
	if g.At(1, 0).Ch != 'w' || g.At(1, 4).Ch != 'd' {
		t.Errorf("row1 = %c..%c, want w..d", g.At(1, 0).Ch, g.At(1, 4).Ch)
	}
	if !g.RowWrapped[0] {
		t.Error("RowWrapped[0] should be true (soft-wrap after shrink)")
	}
}

func TestGrid_Resize_Reflow_ExplicitNewline(t *testing.T) {

	g := NewGrid(3, 5)
	for _, r := range "hello" {
		g.Put(r)
	}
	g.Newline()
	g.CursorC = 0
	for _, r := range "world" {
		g.Put(r)
	}

	if g.RowWrapped[0] {
		t.Fatal("setup: RowWrapped[0] should be false")
	}

	g.Resize(3, 10)

	for i, r := range "hello" {
		if g.At(0, i).Ch != r {
			t.Errorf("row0 col%d: got %c, want %c", i, g.At(0, i).Ch, r)
		}
	}
	for i, r := range "world" {
		if g.At(1, i).Ch != r {
			t.Errorf("row1 col%d: got %c, want %c", i, g.At(1, i).Ch, r)
		}
	}
}

func TestGrid_Resize_Reflow_CursorTracking(t *testing.T) {

	g := NewGrid(3, 5)
	for _, r := range "abcde" {
		g.Put(r)
	}

	g.Resize(3, 3)
	if g.CursorR != 1 || g.CursorC != 1 {
		t.Errorf("cursor = (%d,%d), want (1,1)", g.CursorR, g.CursorC)
	}
}

func TestGrid_Resize_Reflow_WideChar(t *testing.T) {

	g := NewGrid(2, 4)

	for _, r := range "abc" {
		g.Put(r)
	}
	g.Put('你')
	if g.At(0, 0).Ch != 'a' {
		t.Fatalf("setup: At(0,0)=%c, want a", g.At(0, 0).Ch)
	}
	if !g.RowWrapped[0] {
		t.Fatal("setup: RowWrapped[0] not set")
	}

	g.Resize(2, 6)
	if g.At(0, 0).Ch != 'a' || g.At(0, 1).Ch != 'b' || g.At(0, 2).Ch != 'c' {
		t.Errorf("chars: a=%c b=%c c=%c", g.At(0, 0).Ch, g.At(0, 1).Ch, g.At(0, 2).Ch)
	}
	if g.At(0, 3).Ch != '你' || g.At(0, 3).Width != 2 {
		t.Errorf("wide char: ch=%c width=%d, want 你 width 2", g.At(0, 3).Ch, g.At(0, 3).Width)
	}
	if g.At(0, 4).Width != 0 {
		t.Errorf("continuation cell width=%d, want 0", g.At(0, 4).Width)
	}
}

func TestGrid_Resize_Reflow_DeepScrollbackNarrow_CursorSurvives(t *testing.T) {
	// Fill a wide grid with scrollback, then shrink to 1 column.
	// Each wide row explodes into oldCols new rows; the allNew trim must
	// keep the cursor row valid and scrollback within its cap.
	const rows, cols = 5, 20
	g := NewGrid(rows, cols)
	g.ScrollbackCap = 50
	for range 20 {
		for c := range cols {
			if cell := g.At(0, c); cell != nil {
				cell.Ch = 'x'
			}
		}
		g.scrollUpRegion(1)
	}
	g.CursorR, g.CursorC = rows-1, cols/2
	g.Resize(rows, 1)

	if g.CursorR < 0 || g.CursorR >= g.Rows {
		t.Errorf("cursor row %d out of bounds [0,%d)", g.CursorR, g.Rows)
	}
	if g.CursorC < 0 || g.CursorC >= g.Cols {
		t.Errorf("cursor col %d out of bounds [0,%d)", g.CursorC, g.Cols)
	}
	if g.At(g.CursorR, g.CursorC) == nil {
		t.Error("cursor cell nil after narrow reflow")
	}
	if g.Scrollback.Len() > g.ScrollbackCap {
		t.Errorf("scrollback len %d exceeds cap %d", g.Scrollback.Len(), g.ScrollbackCap)
	}
}
