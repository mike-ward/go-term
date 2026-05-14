package term

import (
	"math"
	"testing"
)

func TestGrid_ScrollUp(t *testing.T) {
	g := NewGrid(3, 2)
	for r := range g.Rows {
		for c := range g.Cols {
			g.At(r, c).Ch = rune('a' + r)
		}
	}
	g.scrollUpRegion(1)
	if g.At(0, 0).Ch != 'b' || g.At(1, 0).Ch != 'c' {
		t.Errorf("scrollUp shift wrong: %v %v", g.At(0, 0).Ch, g.At(1, 0).Ch)
	}
	if g.At(2, 0).Ch != ' ' || g.At(2, 1).Ch != ' ' {
		t.Errorf("scrollUp last row not cleared: %v %v",
			g.At(2, 0).Ch, g.At(2, 1).Ch)
	}
}

func TestGrid_ScrollViewClamp(t *testing.T) {
	g := NewGrid(3, 2)
	g.ScrollbackCap = 10

	for range 4 {
		g.scrollUpRegion(1)
	}
	if g.Scrollback.Len() != 4 {
		t.Fatalf("setup: len=%d", g.Scrollback.Len())
	}
	g.ScrollView(2)
	if g.ViewOffset != 2 {
		t.Errorf("ScrollView(2): %d", g.ViewOffset)
	}
	g.ScrollView(100)
	if g.ViewOffset != 4 {
		t.Errorf("upper clamp: %d", g.ViewOffset)
	}
	g.ScrollView(-100)
	if g.ViewOffset != 0 {
		t.Errorf("lower clamp: %d", g.ViewOffset)
	}
	g.ScrollViewTop()
	if g.ViewOffset != 4 {
		t.Errorf("ScrollViewTop: %d", g.ViewOffset)
	}
	g.ResetView()
	if g.ViewOffset != 0 {
		t.Errorf("ResetView: %d", g.ViewOffset)
	}
}

func TestGrid_ScrollView_SaturatingAdd(t *testing.T) {
	g := NewGrid(2, 2)
	g.ScrollbackCap = 10
	for range 5 {
		g.scrollUpRegion(1)
	}
	if got := g.Scrollback.Len(); got != 5 {
		t.Fatalf("setup: scrollback len=%d", got)
	}

	g.ViewOffset = 3
	g.ScrollView(math.MaxInt)
	if g.ViewOffset != 5 {
		t.Errorf("MaxInt delta: got %d, want 5", g.ViewOffset)
	}

	g.ViewOffset = 3
	g.ScrollView(math.MinInt)
	if g.ViewOffset != 0 {
		t.Errorf("MinInt delta: got %d, want 0", g.ViewOffset)
	}

	g.ViewOffset = 0
	g.ScrollView(2)
	if g.ViewOffset != 2 {
		t.Errorf("normal delta: got %d, want 2", g.ViewOffset)
	}
}

func TestGrid_SetScrollRegion(t *testing.T) {
	g := NewGrid(10, 4)
	g.SetScrollRegion(2, 5)
	if g.Top != 2 || g.Bottom != 5 {
		t.Errorf("region = %d..%d, want 2..5", g.Top, g.Bottom)
	}
	if g.CursorR != 0 || g.CursorC != 0 {
		t.Errorf("cursor not homed: %d,%d", g.CursorR, g.CursorC)
	}

	g.SetScrollRegion(5, 5)
	if g.Top != 0 || g.Bottom != g.Rows-1 {
		t.Errorf("degenerate not reset: %d..%d", g.Top, g.Bottom)
	}

	g.SetScrollRegion(-1, 3)
	if g.Top != 0 || g.Bottom != g.Rows-1 {
		t.Errorf("negative top not reset: %d..%d", g.Top, g.Bottom)
	}
	g.SetScrollRegion(0, g.Rows)
	if g.Top != 0 || g.Bottom != g.Rows-1 {
		t.Errorf("oversize bottom not reset: %d..%d", g.Top, g.Bottom)
	}
}

func TestGrid_ScrollUpRegion_Partial(t *testing.T) {
	g := NewGrid(5, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		fillRow(g, i, ch)
	}
	g.Top, g.Bottom = 1, 3
	g.ScrollbackCap = 100
	g.scrollUpRegion(1)
	want := []rune{'A', 'C', 'D', ' ', 'E'}
	for i, w := range want {
		if got := rowChar(g, i); got != w {
			t.Errorf("row %d = %q, want %q", i, got, w)
		}
	}

	if g.Scrollback.Len() != 0 {
		t.Errorf("partial-region scroll polluted scrollback: %d", g.Scrollback.Len())
	}
}

func TestGrid_ScrollUpRegion_FullScreenScrollback(t *testing.T) {
	g := NewGrid(3, 2)
	g.ScrollbackCap = 10
	for i, ch := range []rune{'A', 'B', 'C'} {
		fillRow(g, i, ch)
	}

	g.scrollUpRegion(1)
	if g.Scrollback.Len() != 1 || g.Scrollback.Row(0)[0].Ch != 'A' {
		t.Errorf("full-screen scroll didn't push: %+v", g.Scrollback)
	}
}

func TestGrid_ScrollUpRegion_OverHeight(t *testing.T) {
	g := NewGrid(5, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		fillRow(g, i, ch)
	}
	g.Top, g.Bottom = 1, 3
	g.scrollUpRegion(99)
	want := []rune{'A', ' ', ' ', ' ', 'E'}
	for i, w := range want {
		if got := rowChar(g, i); got != w {
			t.Errorf("row %d = %q, want %q", i, got, w)
		}
	}
}

func TestGrid_ScrollDownRegion_Partial(t *testing.T) {
	g := NewGrid(5, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		fillRow(g, i, ch)
	}
	g.Top, g.Bottom = 1, 3
	g.ScrollbackCap = 100
	g.scrollDownRegion(1)
	want := []rune{'A', ' ', 'B', 'C', 'E'}
	for i, w := range want {
		if got := rowChar(g, i); got != w {
			t.Errorf("row %d = %q, want %q", i, got, w)
		}
	}
	if g.Scrollback.Len() != 0 {
		t.Errorf("scrollDown polluted scrollback")
	}
}

func TestGrid_ScrollUp_ShiftsWrappedFlags(t *testing.T) {
	g := NewGrid(3, 4)
	g.ScrollbackCap = 10

	g.RowWrapped[0] = true
	g.RowWrapped[1] = false
	g.RowWrapped[2] = false
	g.scrollUpRegion(1)

	if g.Scrollback.Len() != 1 || !g.Scrollback.Wrapped(0) {
		t.Errorf("Scrollback wrap = len %d wrapped(0)=%v, want 1/true",
			g.Scrollback.Len(), g.Scrollback.Len() > 0 && g.Scrollback.Wrapped(0))
	}

	if g.RowWrapped[0] {
		t.Error("RowWrapped[0] should be false after scroll up")
	}
}

func TestGrid_ScrollUp_TrimsScrollbackWrapped(t *testing.T) {
	g := NewGrid(2, 2)
	g.ScrollbackCap = 2
	for range 4 {
		g.RowWrapped[0] = true
		g.scrollUpRegion(1)
	}

	if g.Scrollback.Len() != 2 {
		t.Errorf("scrollback len %d, want 2 (cap)", g.Scrollback.Len())
	}
}

func TestGrid_ScrollDown_ShiftsWrappedFlags(t *testing.T) {
	g := NewGrid(3, 4)
	g.RowWrapped[0] = true
	g.RowWrapped[1] = false
	g.RowWrapped[2] = false
	g.scrollDownRegion(1)

	if g.RowWrapped[0] {
		t.Error("RowWrapped[0] should be false after scroll down (inserted row)")
	}

	if !g.RowWrapped[1] {
		t.Error("RowWrapped[1] should be true (shifted from row 0)")
	}
}

func TestGrid_ScrollViewTop_PinsToOldestRow(t *testing.T) {
	g := NewGrid(3, 5)
	g.ScrollbackCap = 10
	for range 5 {
		g.scrollUpRegion(1)
	}
	sb := g.Scrollback.Len()
	if sb == 0 {
		t.Skip("scrollback not populated")
	}
	g.ScrollViewTop()
	if g.ViewOffset != sb {
		t.Errorf("ViewOffset = %d, want %d (len(Scrollback))", g.ViewOffset, sb)
	}

	g2 := NewGrid(3, 5)
	g2.ScrollViewTop()
	if g2.ViewOffset != 0 {
		t.Errorf("empty scrollback: ViewOffset = %d, want 0", g2.ViewOffset)
	}
}
