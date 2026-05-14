package term

import (
	"regexp"
	"testing"
)

func TestGrid_Find_Basic(t *testing.T) {
	g := NewGrid(3, 10)
	putRow(g, "hello")
	sb := g.Scrollback.Len()
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
	sb := g.Scrollback.Len()
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

	g := NewGrid(2, 10)
	putRow(g, "target")
	g.CursorR = 1
	g.CursorC = 0
	for range 10 {
		g.Put(' ')
	}
	sb := g.Scrollback.Len()

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

	g.CursorR = 1
	g.CursorC = 0
	for _, r := range "target" {
		g.Put(r)
	}
	sb := g.Scrollback.Len()

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

func TestGrid_FindRegex_Forward(t *testing.T) {
	g := NewGrid(3, 20)
	putRow(g, "hello world")
	sb := g.Scrollback.Len()
	re := regexp.MustCompile(`w\w+`)
	pos, l, ok := g.FindRegex(re, ContentPos{Row: sb, Col: -1}, true)
	if !ok {
		t.Fatal("FindRegex did not find 'w\\w+'")
	}
	if pos.Row != sb || pos.Col != 6 {
		t.Errorf("FindRegex pos {%d,%d}, want {%d,6}", pos.Row, pos.Col, sb)
	}
	if l != 5 {
		t.Errorf("FindRegex match len %d, want 5", l)
	}
}

func TestGrid_FindRegex_Backward(t *testing.T) {
	g := NewGrid(3, 20)
	putRow(g, "foo bar foo")
	sb := g.Scrollback.Len()
	re := regexp.MustCompile(`foo`)

	pos, _, ok := g.FindRegex(re, ContentPos{Row: sb, Col: 11}, false)
	if !ok {
		t.Fatal("FindRegex backward did not find 'foo'")
	}
	if pos.Col != 8 {
		t.Errorf("FindRegex backward col %d, want 8", pos.Col)
	}
}

func TestGrid_FindRegex_NoMatch(t *testing.T) {
	g := NewGrid(3, 20)
	putRow(g, "hello world")
	re := regexp.MustCompile(`\d+`)
	_, _, ok := g.FindRegex(re, ContentPos{}, true)
	if ok {
		t.Error("FindRegex should return false for no match")
	}
}

func TestGrid_FindRegex_Wrap(t *testing.T) {
	g := NewGrid(2, 20)
	putRow(g, "target99")
	g.CursorR = 1
	g.CursorC = 0
	for range 20 {
		g.Put(' ')
	}
	sb := g.Scrollback.Len()
	re := regexp.MustCompile(`target\d+`)

	pos, _, ok := g.FindRegex(re, ContentPos{Row: sb + 1, Col: 0}, true)
	if !ok {
		t.Fatal("FindRegex did not wrap forward")
	}
	if pos.Row != sb || pos.Col != 0 {
		t.Errorf("FindRegex wrap: {%d,%d}, want {%d,0}", pos.Row, pos.Col, sb)
	}
}

func TestGrid_FindRegex_IPAddress(t *testing.T) {
	g := NewGrid(3, 40)
	putRow(g, "addr 192.168.1.1 end")
	sb := g.Scrollback.Len()
	re := regexp.MustCompile(`[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}`)
	pos, l, ok := g.FindRegex(re, ContentPos{Row: sb, Col: -1}, true)
	if !ok {
		t.Fatal("FindRegex did not find IP address")
	}
	if pos.Col != 5 {
		t.Errorf("FindRegex IP col %d, want 5", pos.Col)
	}
	if l != 11 {
		t.Errorf("FindRegex IP len %d, want 11", l)
	}
}

func TestGrid_FindRegex_InScrollback(t *testing.T) {
	const rows, cols = 3, 20
	g := NewGrid(rows, cols)
	g.ScrollbackCap = 10
	putRow(g, "error: 42")
	g.scrollUpRegion(1)
	if g.Scrollback.Len() == 0 {
		t.Skip("scrollback not populated")
	}
	sb := g.Scrollback.Len()
	re := regexp.MustCompile(`error: \d+`)
	pos, _, ok := g.FindRegex(re, ContentPos{Row: sb, Col: -1}, true)
	if !ok {
		t.Fatal("FindRegex did not find match in scrollback")
	}
	if pos.Row >= sb {
		t.Errorf("FindRegex found match in live grid (row %d), expected scrollback (row < %d)", pos.Row, sb)
	}
}

func TestGrid_FindRegex_NilPattern(t *testing.T) {
	g := NewGrid(3, 20)
	putRow(g, "hello")
	_, _, ok := g.FindRegex(nil, ContentPos{}, true)
	if ok {
		t.Error("FindRegex with nil pattern should return false")
	}
}

func TestGrid_ViewportMatchesRegex_Basic(t *testing.T) {
	g := NewGrid(2, 30)
	putRow(g, "foo 123 bar 456")
	re := regexp.MustCompile(`\d+`)
	matches := g.ViewportMatchesRegex(re)
	if len(matches) != 2 {
		t.Fatalf("ViewportMatchesRegex found %d matches, want 2", len(matches))
	}
	if matches[0].Col != 4 || matches[0].Len != 3 {
		t.Errorf("first match col=%d len=%d, want col=4 len=3", matches[0].Col, matches[0].Len)
	}
	if matches[1].Col != 12 || matches[1].Len != 3 {
		t.Errorf("second match col=%d len=%d, want col=12 len=3", matches[1].Col, matches[1].Len)
	}
}

func TestGrid_ViewportMatchesRegex_VariableLen(t *testing.T) {
	g := NewGrid(2, 30)
	putRow(g, "a bb ccc")
	re := regexp.MustCompile(`[a-z]+`)
	matches := g.ViewportMatchesRegex(re)
	if len(matches) != 3 {
		t.Fatalf("ViewportMatchesRegex found %d matches, want 3", len(matches))
	}
	wantLens := []int{1, 2, 3}
	for i, m := range matches {
		if m.Len != wantLens[i] {
			t.Errorf("match %d len=%d, want %d", i, m.Len, wantLens[i])
		}
	}
}

func TestGrid_ViewportMatchesRegex_AltActiveReturnsNil(t *testing.T) {
	g := NewGrid(2, 20)
	putRow(g, "hello")
	g.EnterAlt()
	re := regexp.MustCompile(`hello`)
	if m := g.ViewportMatchesRegex(re); m != nil {
		t.Errorf("ViewportMatchesRegex during alt = %v, want nil", m)
	}
}

func TestGrid_ViewportMatchesRegex_NilPatternReturnsNil(t *testing.T) {
	g := NewGrid(2, 20)
	putRow(g, "hello")
	if m := g.ViewportMatchesRegex(nil); m != nil {
		t.Errorf("ViewportMatchesRegex(nil) = %v, want nil", m)
	}
}

func TestGrid_ViewportMatches_WithScrollback(t *testing.T) {
	const rows, cols = 4, 10
	g := NewGrid(rows, cols)
	g.ScrollbackCap = 10
	putRow(g, "hello")
	g.scrollUpRegion(1)
	if g.Scrollback.Len() == 0 {
		t.Skip("scrollback not populated")
	}

	g.ViewOffset = 1
	matches := g.ViewportMatches("hello")
	if len(matches) == 0 {
		t.Error("expected match for 'hello' in scrollback viewport, got none")
	}

	g.ViewOffset = 0
	matches = g.ViewportMatches("hello")
	if len(matches) != 0 {
		t.Errorf("expected no matches in live view, got %d", len(matches))
	}
}
