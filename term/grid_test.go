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
	g.scrollUpRegion(1)
	if g.At(0, 0).Ch != 'b' || g.At(1, 0).Ch != 'c' {
		t.Errorf("scrollUp shift wrong: %v %v", g.At(0, 0).Ch, g.At(1, 0).Ch)
	}
	if g.At(2, 0).Ch != ' ' || g.At(2, 1).Ch != ' ' {
		t.Errorf("scrollUp last row not cleared: %v %v",
			g.At(2, 0).Ch, g.At(2, 1).Ch)
	}
}

func TestGrid_ScrollbackFillTrim(t *testing.T) {
	g := NewGrid(3, 4)
	g.ScrollbackCap = 2
	// Fill rows so each carries a recognizable marker.
	for i, r := range []rune{'A', 'B', 'C'} {
		for c := range g.Cols {
			g.At(i, c).Ch = r
		}
	}
	// Three scrollUps push A, B, C into scrollback (cap=2 trims A).
	g.scrollUpRegion(1)
	g.scrollUpRegion(1)
	g.scrollUpRegion(1)
	if len(g.Scrollback) != 2 {
		t.Fatalf("len(Scrollback) = %d, want 2 (trim)", len(g.Scrollback))
	}
	if g.Scrollback[0][0].Ch != 'B' || g.Scrollback[1][0].Ch != 'C' {
		t.Errorf("scrollback ordering: %v %v",
			g.Scrollback[0][0].Ch, g.Scrollback[1][0].Ch)
	}
	// Cap=0 disables scrollback entirely.
	g2 := NewGrid(2, 2)
	g2.At(0, 0).Ch = 'X'
	g2.scrollUpRegion(1)
	if len(g2.Scrollback) != 0 {
		t.Errorf("cap=0 must not retain rows: %d", len(g2.Scrollback))
	}
}

func TestGrid_ScrollViewClamp(t *testing.T) {
	g := NewGrid(3, 2)
	g.ScrollbackCap = 10
	// Push 4 rows into scrollback.
	for range 4 {
		g.scrollUpRegion(1)
	}
	if len(g.Scrollback) != 4 {
		t.Fatalf("setup: len=%d", len(g.Scrollback))
	}
	g.ScrollView(2)
	if g.ViewOffset != 2 {
		t.Errorf("ScrollView(2): %d", g.ViewOffset)
	}
	g.ScrollView(100) // clamp upper
	if g.ViewOffset != 4 {
		t.Errorf("upper clamp: %d", g.ViewOffset)
	}
	g.ScrollView(-100) // clamp lower
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

func TestGrid_ViewCellAt(t *testing.T) {
	g := NewGrid(2, 3)
	g.ScrollbackCap = 5
	// Mark live row 0 with 'L', then scroll once so 'L' lands in
	// scrollback as its only entry.
	for c := range g.Cols {
		g.At(0, c).Ch = 'L'
	}
	g.scrollUpRegion(1)
	// After scrollUp, live row 0 holds whatever was previously row 1
	// (default cells); scrollback[0] holds 'L's.
	g.ViewOffset = 1
	for c := range g.Cols {
		if got := g.ViewCellAt(0, c).Ch; got != 'L' {
			t.Errorf("view row 0 col %d = %v, want L", c, got)
		}
	}
	// Row 1 of the viewport falls past scrollback → live row 0.
	if g.ViewCellAt(1, 0).Ch != ' ' {
		t.Errorf("view row 1 should fall to live, got %v",
			g.ViewCellAt(1, 0).Ch)
	}
	// Out-of-range coords return default cell, never panic.
	if g.ViewCellAt(-1, 0).Ch != ' ' || g.ViewCellAt(0, 99).Ch != ' ' {
		t.Errorf("out-of-range cells should default")
	}
}

func TestGrid_SelectedText_RowRange(t *testing.T) {
	g := NewGrid(3, 5)
	for c, r := range "hello" {
		g.At(0, c).Ch = r
	}
	for c, r := range "world" {
		g.At(1, c).Ch = r
	}
	g.SelAnchor = ContentPos{Row: 0, Col: 0}
	g.SelHead = ContentPos{Row: 1, Col: 4}
	g.SelActive = true
	if got := g.SelectedText(); got != "hello\nworld" {
		t.Errorf("got %q, want %q", got, "hello\nworld")
	}
}

func TestGrid_SelectedText_TrailingBlankTrim(t *testing.T) {
	g := NewGrid(2, 8)
	for c, r := range "abc" {
		g.At(0, c).Ch = r
	}
	for c, r := range "de" {
		g.At(1, c).Ch = r
	}
	g.SelAnchor = ContentPos{Row: 0, Col: 0}
	g.SelHead = ContentPos{Row: 1, Col: 7}
	g.SelActive = true
	// Trailing spaces past 'abc' / 'de' must be trimmed per row.
	if got := g.SelectedText(); got != "abc\nde" {
		t.Errorf("got %q, want %q", got, "abc\nde")
	}
}

func TestGrid_SelectedText_ColumnRangeWithinRow(t *testing.T) {
	g := NewGrid(1, 10)
	for c, r := range "abcdefghij" {
		g.At(0, c).Ch = r
	}
	g.SelAnchor = ContentPos{Row: 0, Col: 3}
	g.SelHead = ContentPos{Row: 0, Col: 6}
	g.SelActive = true
	if got := g.SelectedText(); got != "defg" {
		t.Errorf("got %q, want %q", got, "defg")
	}
}

func TestGrid_SelectedText_BackwardDragNormalized(t *testing.T) {
	g := NewGrid(2, 4)
	for c, r := range "ab" {
		g.At(0, c).Ch = r
	}
	for c, r := range "cd" {
		g.At(1, c).Ch = r
	}
	// Anchor below/right of head — must normalize.
	g.SelAnchor = ContentPos{Row: 1, Col: 1}
	g.SelHead = ContentPos{Row: 0, Col: 0}
	g.SelActive = true
	if got := g.SelectedText(); got != "ab\ncd" {
		t.Errorf("got %q, want %q", got, "ab\ncd")
	}
}

func TestGrid_SelectedText_InactiveOrEmpty(t *testing.T) {
	g := NewGrid(1, 3)
	if got := g.SelectedText(); got != "" {
		t.Errorf("inactive selection returned %q", got)
	}
	g.SelAnchor = ContentPos{Row: 0, Col: 1}
	g.SelHead = ContentPos{Row: 0, Col: 1}
	g.SelActive = true
	if got := g.SelectedText(); got != "" {
		t.Errorf("zero-width selection returned %q", got)
	}
}

func TestGrid_Resize_ReflowsScrollback(t *testing.T) {
	// Phase 13: logical reflow joins soft-wrapped row halves back together
	// when the window grows. This test uses Put so autowrap tracking fires.
	g := NewGrid(2, 4)
	g.ScrollbackCap = 10
	// Write 'abcd' via Put so it fills the row but does NOT trigger autowrap
	// (the wrap check fires on the NEXT Put, not after CursorC reaches Cols).
	for _, r := range "abcd" {
		g.Put(r)
	}
	// Scroll row off the top into scrollback; this also pushes RowWrapped[0].
	g.scrollUpRegion(1)
	if len(g.Scrollback) != 1 {
		t.Fatalf("setup: scrollback len %d, want 1", len(g.Scrollback))
	}

	// Shrink to 2 cols: 'abcd' is one logical line and re-wraps as ['ab','cd'].
	// Since the cursor is on a blank row below the content, liveStart anchors
	// the cursor near the bottom, so the split puts 'ab' in scrollback and
	// 'cd' in the live buffer.
	g.Resize(2, 2)
	if len(g.Scrollback) == 0 {
		t.Fatalf("shrink: scrollback empty, want at least 1 row")
	}
	if len(g.Scrollback[0]) != 2 {
		t.Errorf("shrink: scrollback[0] width %d, want 2", len(g.Scrollback[0]))
	}
	if g.Scrollback[0][0].Ch != 'a' || g.Scrollback[0][1].Ch != 'b' {
		t.Errorf("shrink: scrollback[0] = %v %v, want a b",
			g.Scrollback[0][0].Ch, g.Scrollback[0][1].Ch)
	}
	if g.At(0, 0).Ch != 'c' || g.At(0, 1).Ch != 'd' {
		t.Errorf("shrink: live[0] = %v %v, want c d",
			g.At(0, 0).Ch, g.At(0, 1).Ch)
	}

	// Grow to 5 cols: the soft-wrapped halves ['ab'(wrapped), 'cd'] rejoin into
	// the single logical line 'abcd' and appear in the live buffer.
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

func TestGrid_SelectedText_AcrossScrollbackBoundary(t *testing.T) {
	g := NewGrid(2, 3)
	g.ScrollbackCap = 5
	// Live row 0 = "abc", scroll once → scrollback[0]="abc", live cleared.
	for c, r := range "abc" {
		g.At(0, c).Ch = r
	}
	g.scrollUpRegion(1)
	// Live row 0 now = "xyz".
	for c, r := range "xyz" {
		g.At(0, c).Ch = r
	}
	// Content row 0 = scrollback[0]="abc", content row 1 = live row 0="xyz".
	// Selection must work regardless of ViewOffset.
	g.SelAnchor = ContentPos{Row: 0, Col: 0}
	g.SelHead = ContentPos{Row: 1, Col: 2}
	g.SelActive = true
	if got := g.SelectedText(); got != "abc\nxyz" {
		t.Errorf("ViewOffset=0: got %q, want %q", got, "abc\nxyz")
	}
	// Same result with ViewOffset=1 (scrolled back into scrollback).
	g.ViewOffset = 1
	if got := g.SelectedText(); got != "abc\nxyz" {
		t.Errorf("ViewOffset=1: got %q, want %q", got, "abc\nxyz")
	}
}

func TestGrid_InSelection(t *testing.T) {
	g := NewGrid(3, 5)
	g.SelAnchor = ContentPos{Row: 0, Col: 2}
	g.SelHead = ContentPos{Row: 1, Col: 1}
	g.SelActive = true
	cases := []struct {
		r, c int
		want bool
	}{
		{0, 1, false},
		{0, 2, true},
		{0, 4, true},
		{1, 0, true},
		{1, 1, true},
		{1, 2, false},
		{2, 0, false},
	}
	for _, tc := range cases {
		if got := g.InSelection(tc.r, tc.c); got != tc.want {
			t.Errorf("InSelection(%d,%d)=%v want %v",
				tc.r, tc.c, got, tc.want)
		}
	}
}

func TestClampScrollback_Bounds(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{-1, 0},
		{0, 0},
		{1, 1},
		{MaxScrollbackCap, MaxScrollbackCap},
		{MaxScrollbackCap + 1, MaxScrollbackCap},
		{math.MaxInt32, MaxScrollbackCap},
	}
	for _, c := range cases {
		if got := clampScrollback(c.in); got != c.want {
			t.Errorf("clampScrollback(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestGrid_SelectedText_ClampsOutOfRangeCoords(t *testing.T) {
	// Stale coords (e.g. after a Resize-shrink) must clamp into range
	// rather than feed a negative cap to make().
	g := NewGrid(2, 3)
	for c, r := range "abc" {
		g.At(0, c).Ch = r
	}
	for c, r := range "xyz" {
		g.At(1, c).Ch = r
	}
	g.SelAnchor = ContentPos{Row: -10, Col: -10}
	g.SelHead = ContentPos{Row: 99, Col: 99}
	g.SelActive = true
	got := g.SelectedText()
	if got != "abc\nxyz" {
		t.Errorf("got %q, want %q", got, "abc\nxyz")
	}
}

func TestGrid_SelectedText_RowWithEmptySpan(t *testing.T) {
	// Construct anchor/head positions where, after clamping, a middle
	// row would have c1 < c0. Our 1-row grid skips that and emits no
	// trailing newline for an empty span.
	g := NewGrid(1, 3)
	g.At(0, 0).Ch = 'a'
	g.At(0, 1).Ch = 'b'
	g.At(0, 2).Ch = 'c'
	g.SelAnchor = ContentPos{Row: 0, Col: 0}
	g.SelHead = ContentPos{Row: 0, Col: 2}
	g.SelActive = true
	if got := g.SelectedText(); got != "abc" {
		t.Errorf("baseline: got %q want %q", got, "abc")
	}
}

func TestGrid_Resize_AdjustsSelectionByScrollbackDelta(t *testing.T) {
	// Phase 17: Resize adjusts selection content rows by scrollback-depth
	// change rather than clearing. Selection stays active and clamped to the
	// new content space.
	g := NewGrid(4, 4)
	g.ScrollbackCap = 10
	g.SelAnchor = ContentPos{Row: 0, Col: 0}
	g.SelHead = ContentPos{Row: 3, Col: 3}
	g.SelActive = true
	g.Resize(2, 2)
	if !g.SelActive {
		t.Error("Resize should preserve active selection (Phase 17)")
	}
	// Rows must be clamped to [0, total-1] after reflow.
	total := len(g.Scrollback) + g.Rows
	if g.SelAnchor.Row < 0 || g.SelAnchor.Row >= total {
		t.Errorf("SelAnchor.Row %d out of [0,%d)", g.SelAnchor.Row, total)
	}
	if g.SelHead.Row < 0 || g.SelHead.Row >= total {
		t.Errorf("SelHead.Row %d out of [0,%d)", g.SelHead.Row, total)
	}
}

func TestGrid_InSelection_SurvivesViewOffsetChange(t *testing.T) {
	// Phase 17: selection is content-relative so InSelection returns the
	// same result regardless of ViewOffset.
	g := NewGrid(2, 3)
	g.ScrollbackCap = 5
	for c, ch := range "abc" {
		g.At(0, c).Ch = ch
	}
	g.scrollUpRegion(1)
	for c, ch := range "xyz" {
		g.At(0, c).Ch = ch
	}
	// Content row 0 = scrollback "abc", content row 1 = live "xyz".
	g.SelAnchor = ContentPos{Row: 0, Col: 0}
	g.SelHead = ContentPos{Row: 1, Col: 2}
	g.SelActive = true

	// With ViewOffset=0: viewport row 0 = live, not in scrollback row.
	// viewportToContent(0) = 1 (= len(Scrollback)=1 - 0 + 0)
	g.ViewOffset = 0
	if !g.InSelection(0, 0) {
		t.Error("ViewOffset=0: viewport row 0 (content row 1) should be selected")
	}
	if g.InSelection(1, 0) {
		t.Error("ViewOffset=0: viewport row 1 (content row 2) should not be selected")
	}

	// With ViewOffset=1: viewport row 0 = scrollback "abc" (content row 0).
	g.ViewOffset = 1
	if !g.InSelection(0, 0) {
		t.Error("ViewOffset=1: viewport row 0 (content row 0) should be selected")
	}
	if !g.InSelection(1, 2) {
		t.Error("ViewOffset=1: viewport row 1 (content row 1) should be selected")
	}
}

func TestGrid_SelectedText_ContentCoords_IndependentOfViewOffset(t *testing.T) {
	// Phase 17: SelectedText uses content coords; result identical for any ViewOffset.
	g := NewGrid(2, 3)
	g.ScrollbackCap = 5
	for c, ch := range "abc" {
		g.At(0, c).Ch = ch
	}
	g.scrollUpRegion(1)
	for c, ch := range "xyz" {
		g.At(0, c).Ch = ch
	}
	g.SelAnchor = ContentPos{Row: 0, Col: 0}
	g.SelHead = ContentPos{Row: 1, Col: 2}
	g.SelActive = true

	for _, off := range []int{0, 1} {
		g.ViewOffset = off
		if got := g.SelectedText(); got != "abc\nxyz" {
			t.Errorf("ViewOffset=%d: got %q, want %q", off, got, "abc\nxyz")
		}
	}
}

func TestGrid_ScrollView_SaturatingAdd(t *testing.T) {
	g := NewGrid(2, 2)
	g.ScrollbackCap = 10
	for range 5 {
		g.scrollUpRegion(1)
	}
	if got := len(g.Scrollback); got != 5 {
		t.Fatalf("setup: scrollback len=%d", got)
	}
	// Positive overflow: ViewOffset+delta would wrap past MaxInt.
	g.ViewOffset = 3
	g.ScrollView(math.MaxInt)
	if g.ViewOffset != 5 {
		t.Errorf("MaxInt delta: got %d, want 5", g.ViewOffset)
	}
	// Negative overflow: ViewOffset+delta would wrap below MinInt.
	g.ViewOffset = 3
	g.ScrollView(math.MinInt)
	if g.ViewOffset != 0 {
		t.Errorf("MinInt delta: got %d, want 0", g.ViewOffset)
	}
	// Sanity: ordinary deltas still work.
	g.ViewOffset = 0
	g.ScrollView(2)
	if g.ViewOffset != 2 {
		t.Errorf("normal delta: got %d, want 2", g.ViewOffset)
	}
}

// fillRow writes ch into every cell of row r. Test helper for region
// scroll/insert/delete coverage where each row needs a unique marker.
func fillRow(g *Grid, r int, ch rune) {
	for c := range g.Cols {
		g.At(r, c).Ch = ch
	}
}

// rowChar returns the character at (r, 0) — sufficient since tests
// fill rows with a single repeated rune.
func rowChar(g *Grid, r int) rune { return g.At(r, 0).Ch }

func TestGrid_SetScrollRegion(t *testing.T) {
	g := NewGrid(10, 4)
	g.SetScrollRegion(2, 5)
	if g.Top != 2 || g.Bottom != 5 {
		t.Errorf("region = %d..%d, want 2..5", g.Top, g.Bottom)
	}
	if g.CursorR != 0 || g.CursorC != 0 {
		t.Errorf("cursor not homed: %d,%d", g.CursorR, g.CursorC)
	}
	// Invalid: top >= bottom resets to full.
	g.SetScrollRegion(5, 5)
	if g.Top != 0 || g.Bottom != g.Rows-1 {
		t.Errorf("degenerate not reset: %d..%d", g.Top, g.Bottom)
	}
	// Out of bounds resets.
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
	g.Top, g.Bottom = 1, 3 // region rows B..D
	g.ScrollbackCap = 100
	g.scrollUpRegion(1)
	want := []rune{'A', 'C', 'D', ' ', 'E'}
	for i, w := range want {
		if got := rowChar(g, i); got != w {
			t.Errorf("row %d = %q, want %q", i, got, w)
		}
	}
	// Partial region must not push to scrollback.
	if len(g.Scrollback) != 0 {
		t.Errorf("partial-region scroll polluted scrollback: %d", len(g.Scrollback))
	}
}

func TestGrid_ScrollUpRegion_FullScreenScrollback(t *testing.T) {
	g := NewGrid(3, 2)
	g.ScrollbackCap = 10
	for i, ch := range []rune{'A', 'B', 'C'} {
		fillRow(g, i, ch)
	}
	// Default region == full screen.
	g.scrollUpRegion(1)
	if len(g.Scrollback) != 1 || g.Scrollback[0][0].Ch != 'A' {
		t.Errorf("full-screen scroll didn't push: %+v", g.Scrollback)
	}
}

func TestGrid_ScrollUpRegion_OverHeight(t *testing.T) {
	g := NewGrid(5, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		fillRow(g, i, ch)
	}
	g.Top, g.Bottom = 1, 3
	g.scrollUpRegion(99) // > region height clears region
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
	if len(g.Scrollback) != 0 {
		t.Errorf("scrollDown polluted scrollback")
	}
}

func TestGrid_NewlineAtRegionBottom(t *testing.T) {
	g := NewGrid(5, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		fillRow(g, i, ch)
	}
	g.Top, g.Bottom = 1, 3
	g.CursorR = 3 // at Bottom
	g.Newline()
	// Region scrolled up, cursor stays at Bottom.
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
	g.CursorR = 4 // below Bottom; at last row
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

func TestGrid_ReverseIndexAtTop(t *testing.T) {
	g := NewGrid(5, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		fillRow(g, i, ch)
	}
	g.Top, g.Bottom = 1, 3
	g.CursorR = 1 // at Top
	g.ReverseIndex()
	if g.CursorR != 1 {
		t.Errorf("cursor moved: %d", g.CursorR)
	}
	want := []rune{'A', ' ', 'B', 'C', 'E'}
	for i, w := range want {
		if got := rowChar(g, i); got != w {
			t.Errorf("row %d = %q, want %q", i, got, w)
		}
	}
}

func TestGrid_NextLine(t *testing.T) {
	g := NewGrid(3, 4)
	g.CursorR, g.CursorC = 1, 3
	g.NextLine()
	if g.CursorR != 2 || g.CursorC != 0 {
		t.Errorf("NextLine cursor: %d,%d", g.CursorR, g.CursorC)
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
	want := []rune{'A', 'B', ' ', 'C', 'E'} // D pushed past Bottom, lost
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
	g.CursorR = 4 // below region
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
	g.InsertChars(99) // clears from CursorC to end
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

func TestGrid_ResizeResetsRegion(t *testing.T) {
	g := NewGrid(10, 4)
	g.SetScrollRegion(2, 5)
	g.Resize(8, 4)
	if g.Top != 0 || g.Bottom != 7 {
		t.Errorf("Resize did not reset region: %d..%d", g.Top, g.Bottom)
	}
}

// rowText concatenates non-blank cells in a row for readable assertions.
func rowText(g *Grid, r int) string {
	var b []rune
	for c := 0; c < g.Cols; c++ {
		b = append(b, g.At(r, c).Ch)
	}
	return string(b)
}

func TestGrid_EnterAlt_BlanksAndSwaps(t *testing.T) {
	g := NewGrid(3, 4)
	g.Put('m')
	g.Put('a')
	g.Put('i')
	g.Put('n')
	g.MoveCursor(1, 2)
	g.CurAttrs = AttrBold
	g.EnterAlt()
	if !g.AltActive {
		t.Fatal("AltActive should be true after EnterAlt")
	}
	if g.CursorR != 0 || g.CursorC != 0 {
		t.Errorf("alt cursor not homed: %d,%d", g.CursorR, g.CursorC)
	}
	if g.CurAttrs != 0 || g.CurFG != DefaultColor || g.CurBG != DefaultColor {
		t.Errorf("alt SGR not reset: attrs=%d fg=%#x bg=%#x",
			g.CurAttrs, g.CurFG, g.CurBG)
	}
	for i, cell := range g.Cells {
		if cell.Ch != ' ' {
			t.Fatalf("alt cell %d not blank: %q", i, cell.Ch)
		}
	}
}

func TestGrid_EnterExitAlt_RestoresMain(t *testing.T) {
	g := NewGrid(3, 4)
	g.Put('m')
	g.Put('a')
	g.Put('i')
	g.Put('n')
	g.SetScrollRegion(0, 1)
	g.MoveCursor(2, 1)
	g.CurAttrs = AttrUnderline
	g.CurFG = paletteColor(3)

	g.EnterAlt()
	g.Put('A') // mutate alt buffer
	g.MoveCursor(2, 3)

	g.ExitAlt()
	if g.AltActive {
		t.Fatal("AltActive should be false after ExitAlt")
	}
	if got := rowText(g, 0); got != "main" {
		t.Errorf("main row 0 = %q, want main", got)
	}
	if g.CursorR != 2 || g.CursorC != 1 {
		t.Errorf("main cursor not restored: %d,%d", g.CursorR, g.CursorC)
	}
	if g.CurAttrs != AttrUnderline {
		t.Errorf("main attrs not restored: %d", g.CurAttrs)
	}
	if g.CurFG != paletteColor(3) {
		t.Errorf("main fg not restored: %#x", g.CurFG)
	}
	if g.Top != 0 || g.Bottom != 1 {
		t.Errorf("main region not restored: %d..%d", g.Top, g.Bottom)
	}
}

func TestGrid_EnterAlt_Idempotent(t *testing.T) {
	g := NewGrid(2, 3)
	g.Put('x')
	g.EnterAlt()
	g.Put('Y')
	g.EnterAlt() // should be no-op; must not stash alt over alt
	g.ExitAlt()
	if g.AltActive {
		t.Fatal("still alt after ExitAlt")
	}
	if g.At(0, 0).Ch != 'x' {
		t.Errorf("main row 0 col 0 = %q, want x", g.At(0, 0).Ch)
	}
}

func TestGrid_ExitAlt_NoOpWhenInactive(t *testing.T) {
	g := NewGrid(2, 3)
	g.Put('x')
	g.ExitAlt() // no-op
	if g.AltActive {
		t.Fatal("ExitAlt flipped state from inactive")
	}
	if g.At(0, 0).Ch != 'x' {
		t.Errorf("buffer corrupted by no-op ExitAlt")
	}
}

func TestGrid_AltSuppressesScrollback(t *testing.T) {
	g := NewGrid(2, 3)
	g.ScrollbackCap = 100
	g.EnterAlt()
	for i := 0; i < 10; i++ {
		g.Put('a' + rune(i))
		g.Newline()
	}
	if len(g.Scrollback) != 0 {
		t.Errorf("scrollback grew while alt active: %d rows",
			len(g.Scrollback))
	}
	g.ExitAlt()
	// Main scrollback writes still work after exit.
	for i := 0; i < 5; i++ {
		g.Put('m')
		g.Newline()
	}
	if len(g.Scrollback) == 0 {
		t.Errorf("scrollback not restored after ExitAlt")
	}
}

func TestGrid_EnterAlt_ResetsView(t *testing.T) {
	g := NewGrid(2, 3)
	g.ScrollbackCap = 10
	for i := 0; i < 4; i++ {
		g.Put('a')
		g.Newline()
	}
	g.ScrollView(2)
	if g.ViewOffset == 0 {
		t.Fatal("setup: ViewOffset should be > 0")
	}
	g.EnterAlt()
	if g.ViewOffset != 0 {
		t.Errorf("EnterAlt did not reset ViewOffset: %d", g.ViewOffset)
	}
}

func TestGrid_AltResize_ReflowsMainBuffer(t *testing.T) {
	g := NewGrid(3, 4)
	g.Put('a')
	g.Put('b')
	g.Put('c')
	g.MoveCursor(1, 0)
	g.Put('x')
	g.EnterAlt()
	g.Resize(3, 6) // grow cols while alt is active
	g.ExitAlt()
	if g.Cols != 6 {
		t.Fatalf("Cols = %d, want 6", g.Cols)
	}
	if g.At(0, 0).Ch != 'a' || g.At(0, 1).Ch != 'b' || g.At(0, 2).Ch != 'c' {
		t.Errorf("main row 0 lost on alt resize: %q%q%q",
			g.At(0, 0).Ch, g.At(0, 1).Ch, g.At(0, 2).Ch)
	}
	if g.At(1, 0).Ch != 'x' {
		t.Errorf("main row 1 col 0 = %q, want x", g.At(1, 0).Ch)
	}
	if g.At(0, 5).Ch != ' ' {
		t.Errorf("padding cell not blank: %q", g.At(0, 5).Ch)
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

func TestGrid_AltScreen_PreservesInsertOriginWrap(t *testing.T) {
	g := NewGrid(3, 4)
	g.AutoWrap = false
	g.OriginMode = true
	g.InsertMode = true

	g.EnterAlt()
	if !g.AutoWrap {
		t.Error("alt screen should reset AutoWrap to true")
	}
	if g.OriginMode {
		t.Error("alt screen should reset OriginMode to false")
	}
	if g.InsertMode {
		t.Error("alt screen should reset InsertMode to false")
	}

	g.ExitAlt()
	if g.AutoWrap {
		t.Error("AutoWrap should be restored to false after ExitAlt")
	}
	if !g.OriginMode {
		t.Error("OriginMode should be restored to true after ExitAlt")
	}
	if !g.InsertMode {
		t.Error("InsertMode should be restored to true after ExitAlt")
	}
}

func TestGrid_MoveCursorOrigin_WhenOriginModeOff(t *testing.T) {
	g := NewGrid(5, 8)
	g.SetScrollRegion(1, 3)
	// OriginMode defaults to false — MoveCursorOrigin must delegate to MoveCursor
	g.MoveCursorOrigin(2, 3)
	if g.CursorR != 2 || g.CursorC != 3 {
		t.Errorf("cursor = %d,%d, want 2,3", g.CursorR, g.CursorC)
	}
}

func TestGrid_AltDECSC_DoesNotClobberMainSave(t *testing.T) {
	g := NewGrid(3, 4)
	g.MoveCursor(2, 3)
	g.CurAttrs = AttrBold
	g.SaveCursor() // main save: (2,3,bold)
	g.EnterAlt()
	g.MoveCursor(0, 1)
	g.CurAttrs = AttrUnderline
	g.SaveCursor() // alt save (separate slot)
	g.MoveCursor(1, 2)
	g.RestoreCursor()
	if g.CursorR != 0 || g.CursorC != 1 || g.CurAttrs != AttrUnderline {
		t.Errorf("alt DECRC: cursor=%d,%d attrs=%d",
			g.CursorR, g.CursorC, g.CurAttrs)
	}
	g.ExitAlt()
	g.RestoreCursor()
	if g.CursorR != 2 || g.CursorC != 3 || g.CurAttrs != AttrBold {
		t.Errorf("main DECRC after alt round-trip: cursor=%d,%d attrs=%d",
			g.CursorR, g.CursorC, g.CurAttrs)
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
		{0x1F600, 2}, // emoji
		{'é', 1},     // U+00E9 — narrow Latin
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
	// CursorC=3 (last col); a width-2 char must wrap to next row.
	g.Put('你')
	if g.CursorR != 1 || g.CursorC != 2 {
		t.Errorf("post-wrap cursor: r=%d c=%d", g.CursorR, g.CursorC)
	}
	// Original col 3 must have been padded blank rather than left
	// holding stale state.
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
	g.Put(0x0301) // combining acute accent
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

// --- Phase 13: Logical line wrapping (reflow) tests ---

func TestGrid_Put_SetsWrappedFlag(t *testing.T) {
	g := NewGrid(3, 4)
	// Fill row 0 to right margin; the NEXT Put triggers autowrap.
	for _, r := range "abcd" {
		g.Put(r)
	}
	if g.RowWrapped[0] {
		t.Error("RowWrapped[0] set before autowrap trigger")
	}
	// This Put sees CursorC >= Cols, sets wrap flag, then writes 'e' on row 1.
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

func TestGrid_ScrollUp_ShiftsWrappedFlags(t *testing.T) {
	g := NewGrid(3, 4)
	g.ScrollbackCap = 10
	// Mark row 0 as wrapped.
	g.RowWrapped[0] = true
	g.RowWrapped[1] = false
	g.RowWrapped[2] = false
	g.scrollUpRegion(1)
	// The wrapped flag from row 0 should now be in scrollback.
	if len(g.ScrollbackWrapped) != 1 || !g.ScrollbackWrapped[0] {
		t.Errorf("ScrollbackWrapped = %v, want [true]", g.ScrollbackWrapped)
	}
	// After shifting, row 0 gets the old row 1's flag (false).
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
	if len(g.ScrollbackWrapped) != len(g.Scrollback) {
		t.Errorf("ScrollbackWrapped len %d != Scrollback len %d",
			len(g.ScrollbackWrapped), len(g.Scrollback))
	}
}

func TestGrid_ScrollDown_ShiftsWrappedFlags(t *testing.T) {
	g := NewGrid(3, 4)
	g.RowWrapped[0] = true
	g.RowWrapped[1] = false
	g.RowWrapped[2] = false
	g.scrollDownRegion(1)
	// Row 0 is now blank (inserted) — flag must be false.
	if g.RowWrapped[0] {
		t.Error("RowWrapped[0] should be false after scroll down (inserted row)")
	}
	// Old row 0 (wrapped=true) shifted to row 1.
	if !g.RowWrapped[1] {
		t.Error("RowWrapped[1] should be true (shifted from row 0)")
	}
}

func TestGrid_InsertLines_ShiftsWrappedFlags(t *testing.T) {
	g := NewGrid(4, 4)
	g.RowWrapped[0] = true
	g.RowWrapped[1] = false
	g.MoveCursor(0, 0)
	g.InsertLines(1)
	// Row 0 is the new blank row.
	if g.RowWrapped[0] {
		t.Error("RowWrapped[0] should be false (new blank row)")
	}
	// Old row 0 (wrapped=true) shifted to row 1.
	if !g.RowWrapped[1] {
		t.Error("RowWrapped[1] should be true (shifted from row 0)")
	}
}

func TestGrid_DeleteLines_ShiftsWrappedFlags(t *testing.T) {
	g := NewGrid(4, 4)
	g.RowWrapped[0] = true // row 0 was soft-wrapped
	g.RowWrapped[1] = false
	g.MoveCursor(0, 0)
	g.DeleteLines(1)
	// Row 0 now has old row 1's flag.
	if g.RowWrapped[0] {
		t.Error("RowWrapped[0] should be false (was row 1, not wrapped)")
	}
	// Last row is blank, flag must be false.
	if g.RowWrapped[3] {
		t.Error("RowWrapped[3] should be false (blank fill row)")
	}
}

func TestGrid_Resize_Reflow_GrowWidth(t *testing.T) {
	// Two 5-col rows form a single soft-wrapped logical line. Growing to
	// 10 cols should join them back into one row.
	g := NewGrid(3, 5)
	for _, r := range "helloworld" {
		g.Put(r)
	}
	// After "hello" (5 chars) the NEXT Put wraps: "world" goes to row 1.
	if g.At(0, 0).Ch != 'h' || g.At(1, 0).Ch != 'w' {
		t.Fatalf("setup: row0[0]=%c row1[0]=%c", g.At(0, 0).Ch, g.At(1, 0).Ch)
	}
	if !g.RowWrapped[0] {
		t.Fatal("setup: RowWrapped[0] not set")
	}

	g.Resize(3, 10)

	// "helloworld" should now appear on a single row.
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
	// A 10-char line on a 10-col terminal shrunk to 5 cols should produce
	// two soft-wrapped physical rows.
	g := NewGrid(3, 10)
	for _, r := range "helloworld" {
		g.Put(r)
	}
	// After 10 chars cursor is at (0,10) — pending wrap, no wrap yet.
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
	// Two rows separated by explicit newline must NOT join on resize.
	g := NewGrid(3, 5)
	for _, r := range "hello" {
		g.Put(r)
	}
	g.Newline()
	g.CursorC = 0
	for _, r := range "world" {
		g.Put(r)
	}
	// RowWrapped[0] must be false (explicit newline, not autowrap).
	if g.RowWrapped[0] {
		t.Fatal("setup: RowWrapped[0] should be false")
	}

	g.Resize(3, 10)

	// Row 0 must still be "hello", row 1 still "world" (no join).
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
	// Cursor should follow its logical column after reflow.
	// Write "abcde" (5 chars) on a 5-col terminal; cursor ends at (0,5).
	// Shrink to 3 cols: "abc" on row 0, "de" on row 1. Cursor was at
	// logical col 4 ('e'), so after reflow: row 1, col 1.
	g := NewGrid(3, 5)
	for _, r := range "abcde" {
		g.Put(r)
	}
	// Cursor at (0, 5) — pending wrap.
	g.Resize(3, 3)
	if g.CursorR != 1 || g.CursorC != 1 {
		t.Errorf("cursor = (%d,%d), want (1,1)", g.CursorR, g.CursorC)
	}
}

func TestGrid_Resize_Reflow_WideChar(t *testing.T) {
	// Wide char (width 2) that starts at the last column of a row forces a
	// wrap to the next row in the new layout; the continuation cell must
	// follow immediately.
	g := NewGrid(2, 4)
	// Write 3 normal chars then a wide char; the wide char wraps at col 3.
	for _, r := range "abc" {
		g.Put(r)
	}
	g.Put('你') // width 2 — Put pads col3 and wraps to row 1.
	if g.At(0, 0).Ch != 'a' {
		t.Fatalf("setup: At(0,0)=%c, want a", g.At(0, 0).Ch)
	}
	if !g.RowWrapped[0] {
		t.Fatal("setup: RowWrapped[0] not set")
	}

	// Grow to 6 cols: "abc你" (total display width 5) fits on one row.
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

func TestGrid_InternLink_Dedup(t *testing.T) {
	g := NewGrid(5, 20)
	id1 := g.internLink("https://example.com")
	id2 := g.internLink("https://example.com")
	if id1 == 0 {
		t.Fatal("internLink returned 0 (reserved sentinel)")
	}
	if id1 != id2 {
		t.Errorf("same URL got different IDs: %d != %d", id1, id2)
	}
}

func TestGrid_InternLink_Counter(t *testing.T) {
	g := NewGrid(5, 20)
	id1 := g.internLink("https://a.com")
	id2 := g.internLink("https://b.com")
	if id1 == 0 || id2 == 0 {
		t.Fatal("internLink returned 0 for non-empty URL")
	}
	if id1 == id2 {
		t.Errorf("distinct URLs got same ID: %d", id1)
	}
	if g.LinkURL(id1) != "https://a.com" {
		t.Errorf("LinkURL(%d) = %q, want https://a.com", id1, g.LinkURL(id1))
	}
	if g.LinkURL(id2) != "https://b.com" {
		t.Errorf("LinkURL(%d) = %q, want https://b.com", id2, g.LinkURL(id2))
	}
}

func TestGrid_LinkURL_Zero(t *testing.T) {
	g := NewGrid(5, 20)
	if got := g.LinkURL(0); got != "" {
		t.Errorf("LinkURL(0) = %q, want empty", got)
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

// putRow writes a string of characters starting at column 0 of row 0 in g.
func putRow(g *Grid, s string) {
	g.CursorR, g.CursorC = 0, 0
	for _, r := range s {
		g.Put(r)
	}
}

func TestGrid_ContentCellAt_Live(t *testing.T) {
	g := NewGrid(3, 5)
	putRow(g, "hello")
	sb := len(g.Scrollback)
	cell := g.ContentCellAt(sb, 0)
	if cell.Ch != 'h' {
		t.Errorf("ContentCellAt live row 0 col 0 = %q, want 'h'", cell.Ch)
	}
	cell = g.ContentCellAt(sb, 4)
	if cell.Ch != 'o' {
		t.Errorf("ContentCellAt live row 0 col 4 = %q, want 'o'", cell.Ch)
	}
}

func TestGrid_ContentCellAt_Scrollback(t *testing.T) {
	g := NewGrid(2, 5)
	putRow(g, "first")
	g.Newline() // scrolls "first" into scrollback
	putRow(g, "secnd")
	if len(g.Scrollback) == 0 {
		t.Skip("no scrollback produced")
	}
	cell := g.ContentCellAt(0, 0)
	if cell.Ch != 'f' {
		t.Errorf("ContentCellAt scrollback row 0 col 0 = %q, want 'f'", cell.Ch)
	}
}

func TestGrid_ContentCellAt_OutOfRange(t *testing.T) {
	g := NewGrid(3, 5)
	// Should never panic; returns default cell.
	c := g.ContentCellAt(-1, 0)
	if c.Ch != ' ' {
		t.Errorf("out-of-range row -1 = %q, want ' '", c.Ch)
	}
	c = g.ContentCellAt(g.ContentRows(), 0)
	if c.Ch != ' ' {
		t.Errorf("out-of-range row past end = %q, want ' '", c.Ch)
	}
	c = g.ContentCellAt(0, -1)
	if c.Ch != ' ' {
		t.Errorf("out-of-range col -1 = %q, want ' '", c.Ch)
	}
}

func TestGrid_ContentRowToViewport_Live(t *testing.T) {
	g := NewGrid(3, 5)
	sb := len(g.Scrollback)
	// ViewOffset=0: live rows visible.
	vr, ok := g.ContentRowToViewport(sb)
	if !ok || vr != 0 {
		t.Errorf("live row 0 → viewport %d ok=%v, want vr=0 ok=true", vr, ok)
	}
	vr, ok = g.ContentRowToViewport(sb + 2)
	if !ok || vr != 2 {
		t.Errorf("live row 2 → viewport %d ok=%v, want vr=2 ok=true", vr, ok)
	}
}

func TestGrid_ContentRowToViewport_OutOfView(t *testing.T) {
	g := NewGrid(3, 5)
	_, ok := g.ContentRowToViewport(-1)
	if ok {
		t.Error("content row -1 should be off-screen")
	}
	_, ok = g.ContentRowToViewport(g.ContentRows())
	if ok {
		t.Error("content row past end should be off-screen")
	}
}

func TestGrid_ContentRowToViewport_Scrollback(t *testing.T) {
	g := NewGrid(2, 5)
	putRow(g, "first")
	g.Newline()
	putRow(g, "secnd")
	if len(g.Scrollback) == 0 {
		t.Skip("no scrollback produced")
	}
	// Freeze viewport at 1 row back so scrollback row 0 is visible.
	g.ScrollView(1)
	vr, ok := g.ContentRowToViewport(0)
	if !ok {
		t.Errorf("scrollback row 0 should be visible, got ok=false")
	}
	if vr < 0 || vr >= g.Rows {
		t.Errorf("scrollback row 0 → viewport %d, out of range [0, %d)", vr, g.Rows)
	}
}

func TestGrid_Find_Basic(t *testing.T) {
	g := NewGrid(3, 10)
	putRow(g, "hello")
	sb := len(g.Scrollback)
	pos, ok := g.Find("hello", ContentPos{Row: sb, Col: -1}, true)
	if !ok {
		t.Fatal("Find did not find 'hello'")
	}
	if pos.Row != sb || pos.Col != 0 {
		t.Errorf("Find 'hello' at {%d,%d}, want {%d,0}", pos.Row, pos.Col, sb)
	}
}

func TestGrid_Find_CaseInsensitive(t *testing.T) {
	g := NewGrid(3, 10)
	putRow(g, "HELLO")
	sb := len(g.Scrollback)
	pos, ok := g.Find("hello", ContentPos{Row: sb, Col: -1}, true)
	if !ok {
		t.Fatal("Find case-insensitive did not match")
	}
	if pos.Col != 0 {
		t.Errorf("Find 'hello' (case-insensitive) at col %d, want 0", pos.Col)
	}
}

func TestGrid_Find_EmptyQuery_ReturnsFalse(t *testing.T) {
	g := NewGrid(3, 10)
	putRow(g, "hello")
	_, ok := g.Find("", ContentPos{}, true)
	if ok {
		t.Error("Find with empty query should return false")
	}
}

func TestGrid_Find_QueryWiderThanCols_ReturnsFalse(t *testing.T) {
	g := NewGrid(3, 5)
	_, ok := g.Find("toolong", ContentPos{}, true)
	if ok {
		t.Error("Find with query wider than Cols should return false")
	}
}

func TestGrid_Find_NoMatch(t *testing.T) {
	g := NewGrid(3, 10)
	putRow(g, "hello")
	_, ok := g.Find("xyz", ContentPos{}, true)
	if ok {
		t.Error("Find non-existent query should return false")
	}
}

func TestGrid_Find_Wrap_Forward(t *testing.T) {
	// Two live rows; query in row 0, start in row 1 → wraps to find it.
	g := NewGrid(2, 10)
	putRow(g, "target")
	g.CursorR = 1
	g.CursorC = 0
	for range 10 {
		g.Put(' ')
	}
	sb := len(g.Scrollback)
	// Start search from row 1 (past the match), forward → should wrap and find.
	pos, ok := g.Find("target", ContentPos{Row: sb + 1, Col: 0}, true)
	if !ok {
		t.Fatal("Find did not wrap forward to find 'target'")
	}
	if pos.Row != sb || pos.Col != 0 {
		t.Errorf("Find wrapped forward: {%d,%d}, want {%d,0}", pos.Row, pos.Col, sb)
	}
}

func TestGrid_Find_Wrap_Backward(t *testing.T) {
	g := NewGrid(2, 10)
	// Row 0: spaces (already blank).
	// Row 1: "target".
	g.CursorR = 1
	g.CursorC = 0
	for _, r := range "target" {
		g.Put(r)
	}
	sb := len(g.Scrollback)
	// Start from row 0, backward → wraps around and finds "target" on row 1.
	pos, ok := g.Find("target", ContentPos{Row: sb, Col: 0}, false)
	if !ok {
		t.Fatal("Find did not wrap backward to find 'target'")
	}
	if pos.Row != sb+1 || pos.Col != 0 {
		t.Errorf("Find wrapped backward: {%d,%d}, want {%d,0}", pos.Row, pos.Col, sb+1)
	}
}

func TestGrid_ViewportMatches_All(t *testing.T) {
	g := NewGrid(2, 20)
	putRow(g, "foo bar foo")
	matches := g.ViewportMatches("foo")
	if len(matches) != 2 {
		t.Errorf("ViewportMatches found %d matches, want 2", len(matches))
	}
	if matches[0].Col != 0 {
		t.Errorf("first match col %d, want 0", matches[0].Col)
	}
	if matches[1].Col != 8 {
		t.Errorf("second match col %d, want 8", matches[1].Col)
	}
}

func TestGrid_ViewportMatches_EmptyQuery(t *testing.T) {
	g := NewGrid(2, 10)
	putRow(g, "hello")
	if m := g.ViewportMatches(""); m != nil {
		t.Errorf("ViewportMatches(\"\") = %v, want nil", m)
	}
}

func TestGrid_ViewportMatches_AltActiveReturnsNil(t *testing.T) {
	g := NewGrid(2, 10)
	putRow(g, "hello")
	g.EnterAlt()
	if m := g.ViewportMatches("hello"); m != nil {
		t.Errorf("ViewportMatches during alt = %v, want nil", m)
	}
}
