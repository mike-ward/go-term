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
	g.scrollUp()
	g.scrollUp()
	g.scrollUp()
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
	g2.scrollUp()
	if len(g2.Scrollback) != 0 {
		t.Errorf("cap=0 must not retain rows: %d", len(g2.Scrollback))
	}
}

func TestGrid_ScrollViewClamp(t *testing.T) {
	g := NewGrid(3, 2)
	g.ScrollbackCap = 10
	// Push 4 rows into scrollback.
	for range 4 {
		g.scrollUp()
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
	g.scrollUp()
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
	g.SelAnchor = SelPos{Row: 0, Col: 0}
	g.SelHead = SelPos{Row: 1, Col: 4}
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
	g.SelAnchor = SelPos{Row: 0, Col: 0}
	g.SelHead = SelPos{Row: 1, Col: 7}
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
	g.SelAnchor = SelPos{Row: 0, Col: 3}
	g.SelHead = SelPos{Row: 0, Col: 6}
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
	g.SelAnchor = SelPos{Row: 1, Col: 1}
	g.SelHead = SelPos{Row: 0, Col: 0}
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
	g.SelAnchor = SelPos{Row: 0, Col: 1}
	g.SelHead = SelPos{Row: 0, Col: 1}
	g.SelActive = true
	if got := g.SelectedText(); got != "" {
		t.Errorf("zero-width selection returned %q", got)
	}
}

func TestGrid_Resize_ReflowsScrollback(t *testing.T) {
	g := NewGrid(2, 4)
	g.ScrollbackCap = 5
	// Fill row 0 with 'a','b','c','d' then scroll into scrollback.
	for c, r := range "abcd" {
		g.At(0, c).Ch = r
	}
	g.scrollUp()
	if len(g.Scrollback) != 1 || len(g.Scrollback[0]) != 4 {
		t.Fatalf("setup: scrollback=%v", g.Scrollback)
	}
	// Shrink columns: stored row must be truncated.
	g.Resize(2, 2)
	if len(g.Scrollback[0]) != 2 {
		t.Errorf("shrink: row width %d, want 2", len(g.Scrollback[0]))
	}
	if g.Scrollback[0][0].Ch != 'a' || g.Scrollback[0][1].Ch != 'b' {
		t.Errorf("shrink lost content: %v %v",
			g.Scrollback[0][0].Ch, g.Scrollback[0][1].Ch)
	}
	// Grow columns: stored row must be padded with default cells.
	g.Resize(2, 5)
	if len(g.Scrollback[0]) != 5 {
		t.Errorf("grow: row width %d, want 5", len(g.Scrollback[0]))
	}
	if g.Scrollback[0][0].Ch != 'a' || g.Scrollback[0][1].Ch != 'b' {
		t.Errorf("grow lost content: %+v", g.Scrollback[0])
	}
	for i := 2; i < 5; i++ {
		c := g.Scrollback[0][i]
		if c.Ch != ' ' || c.FG != DefaultColor || c.BG != DefaultColor {
			t.Errorf("grow padding cell %d not default: %+v", i, c)
		}
	}
}

func TestGrid_SelectedText_AcrossScrollbackBoundary(t *testing.T) {
	g := NewGrid(2, 3)
	g.ScrollbackCap = 5
	// Live row 0 = "abc", scroll once → scrollback[0]="abc", live cleared.
	for c, r := range "abc" {
		g.At(0, c).Ch = r
	}
	g.scrollUp()
	// Live row 0 now = "xyz" (the formerly empty bottom shifted up).
	for c, r := range "xyz" {
		g.At(0, c).Ch = r
	}
	g.ViewOffset = 1
	// Viewport row 0 = scrollback "abc"; viewport row 1 = live "xyz".
	g.SelAnchor = SelPos{Row: 0, Col: 0}
	g.SelHead = SelPos{Row: 1, Col: 2}
	g.SelActive = true
	if got := g.SelectedText(); got != "abc\nxyz" {
		t.Errorf("got %q, want %q", got, "abc\nxyz")
	}
}

func TestGrid_InSelection(t *testing.T) {
	g := NewGrid(3, 5)
	g.SelAnchor = SelPos{Row: 0, Col: 2}
	g.SelHead = SelPos{Row: 1, Col: 1}
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
	g.SelAnchor = SelPos{Row: -10, Col: -10}
	g.SelHead = SelPos{Row: 99, Col: 99}
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
	g.SelAnchor = SelPos{Row: 0, Col: 0}
	g.SelHead = SelPos{Row: 0, Col: 2}
	g.SelActive = true
	if got := g.SelectedText(); got != "abc" {
		t.Errorf("baseline: got %q want %q", got, "abc")
	}
}

func TestGrid_Resize_ClearsSelection(t *testing.T) {
	g := NewGrid(4, 4)
	g.SelAnchor = SelPos{Row: 0, Col: 0}
	g.SelHead = SelPos{Row: 3, Col: 3}
	g.SelActive = true
	g.Resize(2, 2)
	if g.SelActive {
		t.Errorf("Resize should clear active selection")
	}
	if g.SelAnchor != (SelPos{}) || g.SelHead != (SelPos{}) {
		t.Errorf("Resize should zero selection coords: anchor=%v head=%v",
			g.SelAnchor, g.SelHead)
	}
}

func TestGrid_ScrollView_SaturatingAdd(t *testing.T) {
	g := NewGrid(2, 2)
	g.ScrollbackCap = 10
	for range 5 {
		g.scrollUp()
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
