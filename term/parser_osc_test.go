package term

import (
	"strings"
	"testing"
)

func TestParser_OSCTitle_BELTerminator(t *testing.T) {
	g, p := newParserGrid(1, 5)
	var got string
	p.SetTitleHandler(func(s string) { got = s })
	feed(t, g, p, []byte("\x1b]0;hello world\x07"))
	if got != "hello world" {
		t.Errorf("title via BEL: %q", got)
	}
	if p.state != stGround {
		t.Errorf("state not ground after BEL: %d", p.state)
	}
}

func TestParser_OSCTitle_STTerminator(t *testing.T) {
	g, p := newParserGrid(1, 5)
	var got string
	p.SetTitleHandler(func(s string) { got = s })
	feed(t, g, p, []byte("\x1b]2;tabby\x1b\\"))
	if got != "tabby" {
		t.Errorf("title via ST: %q", got)
	}
	if p.state != stGround {
		t.Errorf("state not ground after ST: %d", p.state)
	}
}

func TestParser_OSCTitle_Ps0And1And2(t *testing.T) {
	for _, ps := range []string{"0", "1", "2"} {
		g, p := newParserGrid(1, 5)
		var got string
		p.SetTitleHandler(func(s string) { got = s })
		feed(t, g, p, []byte("\x1b]"+ps+";title-"+ps+"\x07"))
		if got != "title-"+ps {
			t.Errorf("Ps=%s: %q", ps, got)
		}
	}
}

func TestParser_OSCTitle_SplitAcrossFeeds(t *testing.T) {
	g, p := newParserGrid(1, 5)
	var got string
	p.SetTitleHandler(func(s string) { got = s })
	feed(t, g, p, []byte("\x1b]0;he"))
	feed(t, g, p, []byte("llo"))
	feed(t, g, p, []byte("\x07"))
	if got != "hello" {
		t.Errorf("split-feed title: %q", got)
	}
}

func TestParser_OSC7_SetsCwd(t *testing.T) {
	g, p := newParserGrid(1, 5)
	feed(t, g, p, []byte("\x1b]7;file://host/Users/me\x07"))
	if g.Cwd != "file://host/Users/me" {
		t.Errorf("Cwd: %q", g.Cwd)
	}
}

func TestParser_OSC_UnknownPsDropped(t *testing.T) {
	g, p := newParserGrid(1, 5)
	titles := 0
	p.SetTitleHandler(func(string) { titles++ })
	feed(t, g, p, []byte("\x1b]52;c;Zm9v\x07"))
	if titles != 0 {
		t.Errorf("OSC 52 fired title handler %d times", titles)
	}
	if g.Cwd != "" {
		t.Errorf("OSC 52 leaked into Cwd: %q", g.Cwd)
	}
}

func TestParser_OSC_NoSeparatorDropped(t *testing.T) {
	g, p := newParserGrid(1, 5)
	titles := 0
	p.SetTitleHandler(func(string) { titles++ })
	feed(t, g, p, []byte("\x1b]nopayload\x07"))
	if titles != 0 {
		t.Errorf("malformed OSC fired handler")
	}
}

func TestParser_OSC_OverflowTruncated(t *testing.T) {
	g, p := newParserGrid(1, 5)
	var got string
	p.SetTitleHandler(func(s string) { got = s })
	huge := make([]byte, 0, maxOSCBytes+200)
	huge = append(huge, []byte("\x1b]0;")...)
	for range maxOSCBytes + 100 {
		huge = append(huge, 'A')
	}
	huge = append(huge, 0x07)
	feed(t, g, p, huge)

	if len(got) != maxOSCBytes-2 {
		t.Errorf("truncated len=%d, want %d", len(got), maxOSCBytes-2)
	}
}

func TestParser_OSC_AbortedByBareESC(t *testing.T) {
	g, p := newParserGrid(1, 5)
	titles := 0
	p.SetTitleHandler(func(string) { titles++ })

	feed(t, g, p, []byte("\x1b]0;abc\x1b[31m"))
	if titles != 0 {
		t.Errorf("aborted OSC dispatched title")
	}
	if g.CurFG != paletteColor(1) {
		t.Errorf("CSI after aborted OSC not applied: CurFG=%#x", g.CurFG)
	}
}

func TestParser_OSC8_OpenLink(t *testing.T) {
	g, p := newParserGrid(5, 40)

	feed(t, g, p, []byte("\x1b]8;;https://example.com\x07"))
	g.Mu.Lock()
	defer g.Mu.Unlock()
	if g.CurLinkID == 0 {
		t.Fatal("CurLinkID is 0 after OSC 8 open")
	}
	if got := g.LinkURL(g.CurLinkID); got != "https://example.com" {
		t.Errorf("LinkURL = %q, want https://example.com", got)
	}
}

func TestParser_OSC8_CloseLink(t *testing.T) {
	g, p := newParserGrid(5, 40)
	feed(t, g, p, []byte("\x1b]8;;https://example.com\x07"))
	feed(t, g, p, []byte("\x1b]8;;\x07"))
	g.Mu.Lock()
	defer g.Mu.Unlock()
	if g.CurLinkID != 0 {
		t.Errorf("CurLinkID = %d after OSC 8 close, want 0", g.CurLinkID)
	}
}

func TestParser_OSC8_MalformedNoSecondSemi(t *testing.T) {
	g, p := newParserGrid(5, 40)

	feed(t, g, p, []byte("\x1b]8;https://example.com\x07"))
	g.Mu.Lock()
	defer g.Mu.Unlock()
	if g.CurLinkID != 0 {
		t.Errorf("CurLinkID = %d, want 0 (malformed OSC 8)", g.CurLinkID)
	}
}

func TestParser_OSC8_DeduplicatesURL(t *testing.T) {
	g, p := newParserGrid(5, 40)
	feed(t, g, p, []byte("\x1b]8;;https://same.com\x07"))
	g.Mu.Lock()
	id1 := g.CurLinkID
	g.Mu.Unlock()
	feed(t, g, p, []byte("\x1b]8;;\x07"))
	feed(t, g, p, []byte("\x1b]8;;https://same.com\x07"))
	g.Mu.Lock()
	id2 := g.CurLinkID
	g.Mu.Unlock()
	if id1 == 0 || id2 == 0 {
		t.Fatal("CurLinkID is 0, expected nonzero")
	}
	if id1 != id2 {
		t.Errorf("same URL assigned different IDs: %d vs %d", id1, id2)
	}
}

func TestParser_OSC52_Write(t *testing.T) {
	g, p := newParserGrid(5, 40)
	var got []byte
	p.SetClipboardHandler(func(data []byte) {
		got = append([]byte(nil), data...)
	})

	feed(t, g, p, []byte("\x1b]52;c;aGVsbG8gd29ybGQ=\x07"))
	if string(got) != "hello world" {
		t.Errorf("clipboard = %q, want \"hello world\"", got)
	}
}

func TestParser_OSC52_InvalidBase64(t *testing.T) {
	g, p := newParserGrid(5, 40)
	called := false
	p.SetClipboardHandler(func(_ []byte) { called = true })
	feed(t, g, p, []byte("\x1b]52;c;!!!notbase64!!!\x07"))
	if called {
		t.Error("onClipboard called for invalid base64")
	}
}

func TestParser_OSC52_ReadIgnored(t *testing.T) {
	g, p := newParserGrid(5, 40)
	called := false
	p.SetClipboardHandler(func(_ []byte) { called = true })
	feed(t, g, p, []byte("\x1b]52;c;?\x07"))
	if called {
		t.Error("onClipboard called for read request (?)")
	}
}

func TestParser_OSC52_NoSemicolon(t *testing.T) {
	g, p := newParserGrid(5, 40)
	called := false
	p.SetClipboardHandler(func(_ []byte) { called = true })
	feed(t, g, p, []byte("\x1b]52;aGVsbG8=\x07"))
	if called {
		t.Error("onClipboard called for malformed OSC 52 (no second semicolon)")
	}
}

func TestParser_OSC133_PromptStart(t *testing.T) {
	g, p := newParserGrid(4, 80)
	feed(t, g, p, []byte("\x1b]133;A\x07"))
	g.Mu.Lock()
	defer g.Mu.Unlock()
	if len(g.Marks) != 1 {
		t.Fatalf("want 1 mark, got %d", len(g.Marks))
	}
	if g.Marks[0].Kind != MarkPromptStart {
		t.Errorf("kind: got %d, want MarkPromptStart", g.Marks[0].Kind)
	}

	if g.Marks[0].Row != 0 {
		t.Errorf("row: got %d, want 0", g.Marks[0].Row)
	}
}

func TestParser_OSC133_AllKinds(t *testing.T) {
	tests := []struct {
		seq  string
		kind MarkKind
	}{
		{"\x1b]133;A\x07", MarkPromptStart},
		{"\x1b]133;B\x07", MarkCommandStart},
		{"\x1b]133;C\x07", MarkOutputStart},
		{"\x1b]133;D\x07", MarkCommandEnd},
	}
	for _, tt := range tests {
		g, p := newParserGrid(4, 80)
		feed(t, g, p, []byte(tt.seq))
		g.Mu.Lock()
		n := len(g.Marks)
		var kind MarkKind
		if n > 0 {
			kind = g.Marks[0].Kind
		}
		g.Mu.Unlock()
		if n != 1 {
			t.Errorf("seq %q: want 1 mark, got %d", tt.seq, n)
			continue
		}
		if kind != tt.kind {
			t.Errorf("seq %q: kind got %d, want %d", tt.seq, kind, tt.kind)
		}
	}
}

func TestParser_OSC133_UnknownSubcommandDropped(t *testing.T) {
	g, p := newParserGrid(4, 80)
	feed(t, g, p, []byte("\x1b]133;Z\x07"))
	g.Mu.Lock()
	defer g.Mu.Unlock()
	if len(g.Marks) != 0 {
		t.Errorf("unknown subcommand: want 0 marks, got %d", len(g.Marks))
	}
}

func TestParser_OSC133_ExtraParamsIgnored(t *testing.T) {
	g, p := newParserGrid(4, 80)

	feed(t, g, p, []byte("\x1b]133;D;exitcode=0\x07"))
	g.Mu.Lock()
	defer g.Mu.Unlock()
	if len(g.Marks) != 1 || g.Marks[0].Kind != MarkCommandEnd {
		t.Errorf("D with extra params: want 1 MarkCommandEnd, got %v", g.Marks)
	}
}

func TestParser_OSC133_AltScreenSuppressed(t *testing.T) {
	g, p := newParserGrid(4, 80)
	g.Mu.Lock()
	g.EnterAlt()
	g.Mu.Unlock()
	feed(t, g, p, []byte("\x1b]133;A\x07"))
	g.Mu.Lock()
	defer g.Mu.Unlock()
	if len(g.Marks) != 0 {
		t.Errorf("alt screen: want 0 marks, got %d", len(g.Marks))
	}
}

func TestParseXColor(t *testing.T) {
	cases := []struct {
		in      string
		r, g, b uint8
		ok      bool
	}{
		{"rgb:ff/00/80", 0xFF, 0x00, 0x80, true},
		{"rgb:ffff/0000/8080", 0xFF, 0x00, 0x80, true},
		{"rgb:f/0/8", 0xFF, 0x00, 0x88, true},
		{"#ff0080", 0xFF, 0x00, 0x80, true},
		{"rgb:gg/00/00", 0, 0, 0, false},
		{"rgb:ff/00", 0, 0, 0, false},
		{"#ff008", 0, 0, 0, false},
		{"red", 0, 0, 0, false},
	}
	for _, tc := range cases {
		c, ok := parseXColor(tc.in)
		if ok != tc.ok {
			t.Errorf("parseXColor(%q) ok=%v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		r, g, b := uint8(c>>16), uint8(c>>8), uint8(c)
		if r != tc.r || g != tc.g || b != tc.b {
			t.Errorf("parseXColor(%q) = rgb(%d,%d,%d), want rgb(%d,%d,%d)",
				tc.in, r, g, b, tc.r, tc.g, tc.b)
		}
	}
}

func TestParser_OSC11_SetBackground(t *testing.T) {
	g, p := newParserGrid(4, 8)
	feed(t, g, p, []byte("\x1b]11;rgb:ff/00/00\x07"))
	g.Mu.Lock()
	bg := g.Theme.DefaultBG
	g.Mu.Unlock()
	if bg.R != 0xFF || bg.G != 0 || bg.B != 0 {
		t.Fatalf("DefaultBG = rgb(%d,%d,%d), want rgb(255,0,0)", bg.R, bg.G, bg.B)
	}
}

func TestParser_OSC10_SetForeground(t *testing.T) {
	g, p := newParserGrid(4, 8)
	feed(t, g, p, []byte("\x1b]10;#00ff80\x07"))
	g.Mu.Lock()
	fg := g.Theme.DefaultFG
	g.Mu.Unlock()
	if fg.R != 0x00 || fg.G != 0xFF || fg.B != 0x80 {
		t.Fatalf("DefaultFG = rgb(%d,%d,%d), want rgb(0,255,128)", fg.R, fg.G, fg.B)
	}
}

func TestParser_OSC12_SetCursorColor(t *testing.T) {
	g, p := newParserGrid(4, 8)
	feed(t, g, p, []byte("\x1b]12;rgb:00/80/ff\x07"))
	g.Mu.Lock()
	cc := g.CursorColor
	g.Mu.Unlock()
	if uint8(cc>>16) != 0x00 || uint8(cc>>8) != 0x80 || uint8(cc) != 0xFF {
		t.Fatalf("CursorColor = %06x, want 0080ff", cc&0xFFFFFF)
	}
}

func TestParser_OSC10_Query(t *testing.T) {
	g, p := newParserGrid(4, 8)
	// DefaultTheme fg is rgb(229,229,229) → e5e5/e5e5/e5e5
	var got []byte
	p.SetReplyHandler(func(b []byte) { got = append(got, b...) })
	feed(t, g, p, []byte("\x1b]10;?\x07"))
	if !strings.HasPrefix(string(got), "\x1b]10;rgb:") {
		t.Fatalf("OSC 10 query reply = %q, want prefix \\x1b]10;rgb:", got)
	}
}

func TestParser_OSC11_Query(t *testing.T) {
	g, p := newParserGrid(4, 8)
	var got []byte
	p.SetReplyHandler(func(b []byte) { got = append(got, b...) })
	feed(t, g, p, []byte("\x1b]11;?\x07"))
	if !strings.HasPrefix(string(got), "\x1b]11;rgb:") {
		t.Fatalf("OSC 11 query reply = %q, want prefix \\x1b]11;rgb:", got)
	}
}

func TestParser_OSCDynColor_InvalidIgnored(t *testing.T) {
	g, p := newParserGrid(4, 8)
	origFG := g.Theme.DefaultFG
	feed(t, g, p, []byte("\x1b]10;notacolor\x07"))
	g.Mu.Lock()
	fg := g.Theme.DefaultFG
	g.Mu.Unlock()
	if fg != origFG {
		t.Fatalf("invalid color changed DefaultFG: got rgb(%d,%d,%d)", fg.R, fg.G, fg.B)
	}
}
