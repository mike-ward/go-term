package term

import (
	"testing"
)

func feed(t *testing.T, g *Grid, p *Parser, b []byte) {
	t.Helper()
	g.Mu.Lock()
	defer g.Mu.Unlock()
	p.Feed(b)
}

func newParserGrid(rows, cols int) (*Grid, *Parser) {
	g := NewGrid(rows, cols)
	return g, NewParser(g)
}

func TestParser_C0Bytes(t *testing.T) {
	g, p := newParserGrid(2, 5)
	g.Put('x')
	g.Put('y')
	feed(t, g, p, []byte{0x07})
	if g.CursorC != 2 {
		t.Errorf("BEL moved cursor: %d", g.CursorC)
	}
	feed(t, g, p, []byte{0x08})
	if g.CursorC != 1 {
		t.Errorf("BS: %d", g.CursorC)
	}
	g.CursorC = 0
	feed(t, g, p, []byte{0x09})
	if g.CursorC != 4 {
		t.Errorf("TAB: %d", g.CursorC)
	}
	feed(t, g, p, []byte{0x0D})
	if g.CursorC != 0 {
		t.Errorf("CR: %d", g.CursorC)
	}
	feed(t, g, p, []byte{0x0A})
	if g.CursorR != 1 {
		t.Errorf("LF: %d", g.CursorR)
	}

	feed(t, g, p, []byte{0x01, 0x02, 0x05})
	if g.CursorR != 1 || g.CursorC != 0 {
		t.Errorf("other C0 should not move: r=%d c=%d", g.CursorR, g.CursorC)
	}
}

func TestParser_UTF8SplitAcrossFeeds(t *testing.T) {
	cases := []struct {
		name  string
		parts [][]byte
		want  rune
	}{
		{"2-byte split 1+1", [][]byte{{0xC3}, {0xA9}}, 0x00E9},
		{"3-byte split 1+2", [][]byte{{0xE2}, {0x98, 0x83}}, 0x2603},
		{"3-byte split 2+1", [][]byte{{0xE2, 0x98}, {0x83}}, 0x2603},
		{"4-byte split 1+3", [][]byte{{0xF0}, {0x9F, 0x98, 0x80}}, 0x1F600},
		{"4-byte split 2+2", [][]byte{{0xF0, 0x9F}, {0x98, 0x80}}, 0x1F600},
		{"4-byte split 3+1", [][]byte{{0xF0, 0x9F, 0x98}, {0x80}}, 0x1F600},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, p := newParserGrid(1, 5)
			for _, part := range c.parts {
				feed(t, g, p, part)
			}
			if g.At(0, 0).Ch != c.want {
				t.Errorf("got %U, want %U", g.At(0, 0).Ch, c.want)
			}
		})
	}
}

func TestParser_InvalidUTF8YieldsReplacement(t *testing.T) {
	g, p := newParserGrid(1, 5)
	feed(t, g, p, []byte{0xFF})
	if g.At(0, 0).Ch != 0xFFFD {
		t.Errorf("invalid byte should produce U+FFFD, got %U", g.At(0, 0).Ch)
	}
}

func TestParser_ESCNonBracketIgnored(t *testing.T) {
	g, p := newParserGrid(1, 5)
	feed(t, g, p, []byte("\x1b("))
	if g.CursorC != 0 {
		t.Errorf("ESC ( should be swallowed: cursor=%d", g.CursorC)
	}
	if p.state != stEscInter {
		t.Errorf("state should await ESC intermediate final: %d", p.state)
	}
}

func TestParser_ESCCharsetDesignationSwallowed(t *testing.T) {
	g, p := newParserGrid(1, 5)
	feed(t, g, p, []byte("\x1b(BX"))
	if got := g.At(0, 0).Ch; got != 'X' {
		t.Fatalf("ESC(B leaked into grid: got %q want %q", got, 'X')
	}
	if g.CursorC != 1 {
		t.Fatalf("cursor after ESC(BX = %d, want 1", g.CursorC)
	}
}

func TestParser_RestoreWithoutSaveResets(t *testing.T) {
	g, p := newParserGrid(5, 10)
	g.MoveCursor(2, 3)
	g.CurFG = paletteColor(5)
	g.CurAttrs = AttrUnderline
	feed(t, g, p, []byte("\x1b8"))
	if g.CursorR != 0 || g.CursorC != 0 {
		t.Errorf("home: r=%d c=%d", g.CursorR, g.CursorC)
	}
	if g.CurFG != DefaultColor || g.CurAttrs != 0 {
		t.Errorf("SGR not reset: fg=%#x attrs=%d", g.CurFG, g.CurAttrs)
	}
}

func TestParser_IND_RI_NEL(t *testing.T) {
	g, p := newParserGrid(5, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		for c := range g.Cols {
			g.At(i, c).Ch = ch
		}
	}
	feed(t, g, p, []byte("\x1b[2;4r"))
	g.CursorR = 3
	feed(t, g, p, []byte("\x1bD"))
	if g.At(1, 0).Ch != 'C' || g.At(2, 0).Ch != 'D' || g.At(3, 0).Ch != ' ' {
		t.Errorf("IND scroll wrong: %q %q %q",
			g.At(1, 0).Ch, g.At(2, 0).Ch, g.At(3, 0).Ch)
	}
	g.CursorR = 1
	feed(t, g, p, []byte("\x1bM"))
	if g.At(1, 0).Ch != ' ' || g.At(2, 0).Ch != 'C' || g.At(3, 0).Ch != 'D' {
		t.Errorf("RI scroll wrong: %q %q %q",
			g.At(1, 0).Ch, g.At(2, 0).Ch, g.At(3, 0).Ch)
	}
	g.CursorR, g.CursorC = 1, 1
	feed(t, g, p, []byte("\x1bE"))
	if g.CursorC != 0 || g.CursorR != 2 {
		t.Errorf("NEL cursor: %d,%d", g.CursorR, g.CursorC)
	}
}

func TestParser_MouseModes_Toggle(t *testing.T) {
	g, p := newParserGrid(1, 5)
	feed(t, g, p, []byte("\x1b[?1000;1006h"))
	if !g.MouseTrack || !g.MouseSGR {
		t.Errorf("?1000;1006h: track=%v sgr=%v", g.MouseTrack, g.MouseSGR)
	}
	feed(t, g, p, []byte("\x1b[?1002h"))
	if !g.MouseTrackBtn {
		t.Error("?1002h not set")
	}
	feed(t, g, p, []byte("\x1b[?1003h"))
	if !g.MouseTrackAny {
		t.Error("?1003h not set")
	}
	feed(t, g, p, []byte("\x1b[?1000;1002;1003;1006l"))
	if g.MouseTrack || g.MouseTrackBtn || g.MouseTrackAny || g.MouseSGR {
		t.Errorf("reset failed: track=%v btn=%v any=%v sgr=%v",
			g.MouseTrack, g.MouseTrackBtn, g.MouseTrackAny, g.MouseSGR)
	}
}

func TestParser_MouseReporting_Aggregate(t *testing.T) {
	g, _ := newParserGrid(1, 5)
	if g.MouseReporting() {
		t.Error("default should be off")
	}
	g.MouseTrack = true
	if !g.MouseReporting() {
		t.Error("MouseTrack should imply reporting")
	}
	g.MouseTrack = false
	g.MouseTrackAny = true
	if !g.MouseReporting() {
		t.Error("MouseTrackAny should imply reporting")
	}
}

func TestParser_BEL_IncrementsBellCount(t *testing.T) {
	g, p := newParserGrid(5, 20)
	feed(t, g, p, []byte{0x07})
	if g.BellCount != 1 {
		t.Fatalf("BellCount after BEL = %d, want 1", g.BellCount)
	}
	feed(t, g, p, []byte{0x07, 0x07})
	if g.BellCount != 3 {
		t.Fatalf("BellCount after 3 BELs = %d, want 3", g.BellCount)
	}
}

func TestParser_BEL_DoesNotMoveCursor(t *testing.T) {
	g, p := newParserGrid(5, 20)
	g.Put('A')
	feed(t, g, p, []byte{0x07})
	if g.CursorC != 1 {
		t.Errorf("BEL moved cursor: col = %d, want 1", g.CursorC)
	}
}

func TestParser_BEL_InOSCTerminatesPayload(t *testing.T) {

	g, p := newParserGrid(5, 40)
	var title string
	p.SetTitleHandler(func(s string) { title = s })
	feed(t, g, p, []byte("\x1b]0;hello\x07"))
	if title != "hello" {
		t.Errorf("OSC title = %q, want %q", title, "hello")
	}

	if g.BellCount != 0 {
		t.Errorf("OSC-terminator BEL incremented BellCount = %d, want 0", g.BellCount)
	}
}

func TestParser_HTS_SetTabStop(t *testing.T) {
	g, p := newParserGrid(1, 80)
	g.Mu.Lock()
	g.CursorC = 12
	g.Mu.Unlock()
	feed(t, g, p, []byte("\x1bH"))
	g.Mu.Lock()
	got := g.TabStops[12]
	g.Mu.Unlock()
	if !got {
		t.Error("ESC H: tab stop not set at col 12")
	}
}

func TestParser_TBC_ClearAtCursor(t *testing.T) {
	g, p := newParserGrid(1, 80)

	g.Mu.Lock()
	g.CursorC = 8
	g.Mu.Unlock()
	feed(t, g, p, []byte("\x1b[g"))
	g.Mu.Lock()
	got := g.TabStops[8]
	g.Mu.Unlock()
	if got {
		t.Error("CSI g: stop at col 8 should be cleared")
	}
}

func TestParser_TBC_ClearAll(t *testing.T) {
	g, p := newParserGrid(1, 80)
	feed(t, g, p, []byte("\x1b[3g"))
	g.Mu.Lock()
	defer g.Mu.Unlock()
	for c := 0; c < MaxGridDim; c++ {
		if g.TabStops[c] {
			t.Errorf("CSI 3g: stop still set at col %d", c)
		}
	}
}

func TestParser_HTS_TBC_RoundTrip(t *testing.T) {
	g, p := newParserGrid(1, 80)

	feed(t, g, p, []byte("\x1b[3g"))
	g.Mu.Lock()
	g.CursorC = 5
	g.Mu.Unlock()
	feed(t, g, p, []byte("\x1bH"))
	g.Mu.Lock()
	g.CursorC = 10
	g.Mu.Unlock()
	feed(t, g, p, []byte("\x1bH"))

	g.Mu.Lock()
	defer g.Mu.Unlock()

	for c := 0; c < 20; c++ {
		want := c == 5 || c == 10
		if g.TabStops[c] != want {
			t.Errorf("col %d: TabStops=%v, want %v", c, g.TabStops[c], want)
		}
	}

	g.CursorC = 0
	g.Tab()
	if g.CursorC != 5 {
		t.Errorf("Tab from 0: got %d, want 5", g.CursorC)
	}
	g.Tab()
	if g.CursorC != 10 {
		t.Errorf("Tab from 5: got %d, want 10", g.CursorC)
	}
	g.Tab()
	if g.CursorC != g.Cols-1 {
		t.Errorf("Tab from 10 (no more stops): got %d, want %d", g.CursorC, g.Cols-1)
	}
}

func TestParser_KittyKeyPush(t *testing.T) {
	g, p := newParserGrid(4, 8)

	feed(t, g, p, []byte("\x1b[>1u"))
	if g.KittyKeyFlags != 1 {
		t.Fatalf("after CSI>1u: flags=%d, want 1", g.KittyKeyFlags)
	}

	feed(t, g, p, []byte("\x1b[>2u"))
	if g.KittyKeyFlags != 3 {
		t.Fatalf("after CSI>2u: flags=%d, want 3", g.KittyKeyFlags)
	}
	if len(g.kittyFlagStack) != 2 {
		t.Fatalf("stack depth=%d, want 2", len(g.kittyFlagStack))
	}
}

func TestParser_KittyKeyPop(t *testing.T) {
	g, p := newParserGrid(4, 8)
	feed(t, g, p, []byte("\x1b[>1u"))
	feed(t, g, p, []byte("\x1b[>2u"))
	feed(t, g, p, []byte("\x1b[<1u"))
	if g.KittyKeyFlags != 1 {
		t.Fatalf("after pop: flags=%d, want 1", g.KittyKeyFlags)
	}
	feed(t, g, p, []byte("\x1b[<1u"))
	if g.KittyKeyFlags != 0 {
		t.Fatalf("after second pop: flags=%d, want 0", g.KittyKeyFlags)
	}
}

func TestParser_KittyKeyPopN(t *testing.T) {
	g, p := newParserGrid(4, 8)
	feed(t, g, p, []byte("\x1b[>1u"))
	feed(t, g, p, []byte("\x1b[>2u"))
	feed(t, g, p, []byte("\x1b[>4u"))
	feed(t, g, p, []byte("\x1b[<2u"))
	if g.KittyKeyFlags != 1 {
		t.Fatalf("after pop 2: flags=%d, want 1", g.KittyKeyFlags)
	}
}

func TestParser_KittyKeyPopEmpty(t *testing.T) {
	g, p := newParserGrid(4, 8)
	g.KittyKeyFlags = 7
	feed(t, g, p, []byte("\x1b[<1u"))
	if g.KittyKeyFlags != 0 {
		t.Fatalf("pop empty: flags=%d, want 0", g.KittyKeyFlags)
	}
}

func TestParser_KittyKeySet(t *testing.T) {
	g, p := newParserGrid(4, 8)
	feed(t, g, p, []byte("\x1b[>1u"))
	feed(t, g, p, []byte("\x1b[=5u"))
	if g.KittyKeyFlags != 5 {
		t.Fatalf("after CSI=5u: flags=%d, want 5", g.KittyKeyFlags)
	}

	if len(g.kittyFlagStack) != 1 {
		t.Fatalf("stack depth=%d, want 1 (set does not push)", len(g.kittyFlagStack))
	}
}

func TestParser_KittyKeyQuery(t *testing.T) {
	g, p := newParserGrid(4, 8)
	g.KittyKeyFlags = 3
	var got []byte
	p.SetReplyHandler(func(b []byte) { got = append(got, b...) })
	feed(t, g, p, []byte("\x1b[?u"))
	want := "\x1b[?3u"
	if string(got) != want {
		t.Fatalf("query reply: got %q, want %q", got, want)
	}
}

func TestParser_KittyKeyQueryZero(t *testing.T) {
	g, p := newParserGrid(4, 8)
	var got []byte
	p.SetReplyHandler(func(b []byte) { got = append(got, b...) })
	feed(t, g, p, []byte("\x1b[?u"))
	want := "\x1b[?0u"
	if string(got) != want {
		t.Fatalf("query zero: got %q, want %q", got, want)
	}
}
