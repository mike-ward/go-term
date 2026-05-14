package term

import (
	"math"
	"strconv"
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

func TestGrid_CarriageReturn(t *testing.T) {
	g := NewGrid(1, 5)
	g.CursorC = 3
	g.CarriageReturn()
	if g.CursorC != 0 {
		t.Errorf("CR should reset col: %d", g.CursorC)
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

func TestGrid_ScrollbackFillTrim(t *testing.T) {
	g := NewGrid(3, 4)
	g.ScrollbackCap = 2

	for i, r := range []rune{'A', 'B', 'C'} {
		for c := range g.Cols {
			g.At(i, c).Ch = r
		}
	}

	g.scrollUpRegion(1)
	g.scrollUpRegion(1)
	g.scrollUpRegion(1)
	if g.Scrollback.Len() != 2 {
		t.Fatalf("len(Scrollback) = %d, want 2 (trim)", g.Scrollback.Len())
	}
	if g.Scrollback.Row(0)[0].Ch != 'B' || g.Scrollback.Row(1)[0].Ch != 'C' {
		t.Errorf("scrollback ordering: %v %v",
			g.Scrollback.Row(0)[0].Ch, g.Scrollback.Row(1)[0].Ch)
	}

	g2 := NewGrid(2, 2)
	g2.At(0, 0).Ch = 'X'
	g2.scrollUpRegion(1)
	if g2.Scrollback.Len() != 0 {
		t.Errorf("cap=0 must not retain rows: %d", g2.Scrollback.Len())
	}
}

func TestGrid_ViewCellAt(t *testing.T) {
	g := NewGrid(2, 3)
	g.ScrollbackCap = 5

	for c := range g.Cols {
		g.At(0, c).Ch = 'L'
	}
	g.scrollUpRegion(1)

	g.ViewOffset = 1
	for c := range g.Cols {
		if got := g.ViewCellAt(0, c).Ch; got != 'L' {
			t.Errorf("view row 0 col %d = %v, want L", c, got)
		}
	}

	if g.ViewCellAt(1, 0).Ch != ' ' {
		t.Errorf("view row 1 should fall to live, got %v",
			g.ViewCellAt(1, 0).Ch)
	}

	if g.ViewCellAt(-1, 0).Ch != ' ' || g.ViewCellAt(0, 99).Ch != ' ' {
		t.Errorf("out-of-range cells should default")
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

func TestGrid_ReverseIndexAtTop(t *testing.T) {
	g := NewGrid(5, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		fillRow(g, i, ch)
	}
	g.Top, g.Bottom = 1, 3
	g.CursorR = 1
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

// rowText concatenates non-blank cells in a row for readable assertions.
func rowText(g *Grid, r int) string {
	var b []rune
	for c := 0; c < g.Cols; c++ {
		b = append(b, g.At(r, c).Ch)
	}
	return string(b)
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
	g.Put('A')
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
	sb := g.Scrollback.Len()
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
	g.Newline()
	putRow(g, "secnd")
	if g.Scrollback.Len() == 0 {
		t.Skip("no scrollback produced")
	}
	cell := g.ContentCellAt(0, 0)
	if cell.Ch != 'f' {
		t.Errorf("ContentCellAt scrollback row 0 col 0 = %q, want 'f'", cell.Ch)
	}
}

func TestGrid_ContentCellAt_OutOfRange(t *testing.T) {
	g := NewGrid(3, 5)

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
	sb := g.Scrollback.Len()

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
	if g.Scrollback.Len() == 0 {
		t.Skip("no scrollback produced")
	}

	g.ScrollView(1)
	vr, ok := g.ContentRowToViewport(0)
	if !ok {
		t.Errorf("scrollback row 0 should be visible, got ok=false")
	}
	if vr < 0 || vr >= g.Rows {
		t.Errorf("scrollback row 0 → viewport %d, out of range [0, %d)", vr, g.Rows)
	}
}

func TestGrid_TranslateRune_DECGraphicsFullTable(t *testing.T) {
	g := NewGrid(1, 1)
	g.CharsetG0 = '0'
	g.ActiveG = 0
	cases := []struct {
		in, want rune
	}{
		{'`', '◆'}, {'a', '▒'}, {'f', '°'}, {'g', '±'},
		{'h', '␤'}, {'i', '␋'}, {'j', '┘'}, {'k', '┐'},
		{'l', '┌'}, {'m', '└'}, {'n', '┼'}, {'o', '⎺'},
		{'p', '⎻'}, {'q', '─'}, {'r', '⎼'}, {'s', '⎽'},
		{'t', '├'}, {'u', '┤'}, {'v', '┴'}, {'w', '┬'},
		{'x', '│'}, {'y', '≤'}, {'z', '≥'}, {'{', 'π'},
		{'|', '≠'}, {'}', '£'}, {'~', '·'},
	}
	for _, tc := range cases {
		if got := g.translateRune(tc.in); got != tc.want {
			t.Errorf("translateRune(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestGrid_DefaultCell_ULColor(t *testing.T) {

	c := defaultCell()
	if c.ULStyle != ULNone {
		t.Errorf("defaultCell: ULStyle = %d, want 0", c.ULStyle)
	}
	if c.ULColor != DefaultColor {
		t.Errorf("defaultCell: ULColor = %#x, want DefaultColor", c.ULColor)
	}
}

func TestGrid_InternLink_CapReturnsZero(t *testing.T) {
	g := NewGrid(5, 20)

	for i := range maxLinkEntries {
		url := "https://example.com/" + strconv.Itoa(i)
		id := g.internLink(url)
		if id == 0 {
			t.Fatalf("internLink returned 0 at entry %d (before cap %d)", i, maxLinkEntries)
		}
	}

	id := g.internLink("https://overflow.example.com")
	if id != 0 {
		t.Errorf("internLink beyond cap: got %d, want 0", id)
	}

	id2 := g.internLink("https://example.com/0")
	if id2 == 0 {
		t.Error("existing URL in full registry should still return its ID, not 0")
	}
}

func TestGrid_DirtyTracking_HasDirtyRows(t *testing.T) {
	g := NewGrid(5, 10)
	if g.HasDirtyRows() {
		t.Fatal("new grid should have no dirty rows")
	}
	g.markDirty(2)
	if !g.HasDirtyRows() {
		t.Fatal("expected dirty after markDirty")
	}
	g.ClearDirty()
	if g.HasDirtyRows() {
		t.Fatal("expected clean after ClearDirty")
	}
}

func TestGrid_DirtyTracking_ScrollUpRegionMarksAll(t *testing.T) {
	g := NewGrid(5, 10)
	g.ClearDirty()
	g.scrollUpRegion(1)
	for r := range g.Rows {
		if !g.Dirty[r] {
			t.Errorf("row %d should be dirty after scrollUpRegion", r)
		}
	}
}

func TestGrid_DirtyTracking_ClearAllMarksAll(t *testing.T) {
	g := NewGrid(5, 10)
	g.ClearDirty()
	g.ClearAll()
	for r := range g.Rows {
		if !g.Dirty[r] {
			t.Errorf("row %d should be dirty after ClearAll", r)
		}
	}
}

func TestGrid_DirtyTracking_ResizeReallocates(t *testing.T) {
	g := NewGrid(5, 10)
	g.ClearDirty()
	g.Resize(8, 10)
	if len(g.Dirty) != 8 {
		t.Fatalf("Dirty len = %d, want 8", len(g.Dirty))
	}
	for r := range g.Rows {
		if !g.Dirty[r] {
			t.Errorf("row %d should be dirty after Resize", r)
		}
	}
}

func TestGrid_DirtyTracking_EnterExitAltMarksAll(t *testing.T) {
	g := NewGrid(5, 10)
	g.ClearDirty()
	g.EnterAlt()
	for r := range g.Rows {
		if !g.Dirty[r] {
			t.Errorf("row %d should be dirty after EnterAlt", r)
		}
	}
	g.ClearDirty()
	g.ExitAlt()
	for r := range g.Rows {
		if !g.Dirty[r] {
			t.Errorf("row %d should be dirty after ExitAlt", r)
		}
	}
}
