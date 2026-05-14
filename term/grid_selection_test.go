package term

import "testing"

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

func TestGrid_SelectedText_AcrossScrollbackBoundary(t *testing.T) {
	g := NewGrid(2, 3)
	g.ScrollbackCap = 5

	for c, r := range "abc" {
		g.At(0, c).Ch = r
	}
	g.scrollUpRegion(1)

	for c, r := range "xyz" {
		g.At(0, c).Ch = r
	}

	g.SelAnchor = ContentPos{Row: 0, Col: 0}
	g.SelHead = ContentPos{Row: 1, Col: 2}
	g.SelActive = true
	if got := g.SelectedText(); got != "abc\nxyz" {
		t.Errorf("ViewOffset=0: got %q, want %q", got, "abc\nxyz")
	}

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

func TestGrid_SelectedText_ClampsOutOfRangeCoords(t *testing.T) {

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

func TestGrid_InSelection_SurvivesViewOffsetChange(t *testing.T) {

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

	g.ViewOffset = 0
	if !g.InSelection(0, 0) {
		t.Error("ViewOffset=0: viewport row 0 (content row 1) should be selected")
	}
	if g.InSelection(1, 0) {
		t.Error("ViewOffset=0: viewport row 1 (content row 2) should not be selected")
	}

	g.ViewOffset = 1
	if !g.InSelection(0, 0) {
		t.Error("ViewOffset=1: viewport row 0 (content row 0) should be selected")
	}
	if !g.InSelection(1, 2) {
		t.Error("ViewOffset=1: viewport row 1 (content row 1) should be selected")
	}
}

func TestGrid_SelectedText_ContentCoords_IndependentOfViewOffset(t *testing.T) {

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
