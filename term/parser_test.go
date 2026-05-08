package term

import (
	"bytes"
	"strconv"
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
	feed(t, g, p, []byte{0x07}) // BEL: drop
	if g.CursorC != 2 {
		t.Errorf("BEL moved cursor: %d", g.CursorC)
	}
	feed(t, g, p, []byte{0x08}) // BS
	if g.CursorC != 1 {
		t.Errorf("BS: %d", g.CursorC)
	}
	g.CursorC = 0
	feed(t, g, p, []byte{0x09}) // TAB
	if g.CursorC != 4 {         // clamps to Cols-1=4 since 8>5
		t.Errorf("TAB: %d", g.CursorC)
	}
	feed(t, g, p, []byte{0x0D}) // CR
	if g.CursorC != 0 {
		t.Errorf("CR: %d", g.CursorC)
	}
	feed(t, g, p, []byte{0x0A}) // LF
	if g.CursorR != 1 {
		t.Errorf("LF: %d", g.CursorR)
	}
	// other C0 silently dropped
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

func TestParser_CSIParamCountCappedAt32(t *testing.T) {
	g, p := newParserGrid(1, 5)
	input := []byte("\x1b[")
	for range 100 {
		input = append(input, '1', ';')
	}
	input = append(input, '0', 'm')
	feed(t, g, p, input)
	if len(p.params) > maxCSIParams {
		t.Errorf("params grew past cap: %d", len(p.params))
	}
}

func TestParser_CSIParamValueCapped(t *testing.T) {
	g, p := newParserGrid(1, 5)
	// 30 nines would overflow int64; verify accumulator stays bounded.
	input := append([]byte("\x1b["), bytes.Repeat([]byte("9"), 30)...)
	input = append(input, 'm')
	feed(t, g, p, input)
	for i, v := range p.params {
		if v > maxCSIParamValue {
			t.Errorf("param[%d]=%d exceeds cap %d", i, v, maxCSIParamValue)
		}
	}
}

func TestParser_SGR_Reset(t *testing.T) {
	g, p := newParserGrid(1, 1)
	g.CurFG = 5
	g.CurBG = 6
	g.CurAttrs = AttrBold | AttrUnderline
	feed(t, g, p, []byte("\x1b[m")) // bare m == 0
	if g.CurFG != DefaultColor || g.CurBG != DefaultColor || g.CurAttrs != 0 {
		t.Errorf("SGR reset failed: fg=%d bg=%d attrs=%d",
			g.CurFG, g.CurBG, g.CurAttrs)
	}
}

func TestParser_SGR_BoldUnderlineInverse(t *testing.T) {
	g, p := newParserGrid(1, 1)
	feed(t, g, p, []byte("\x1b[1;4;7m"))
	if g.CurAttrs != AttrBold|AttrUnderline|AttrInverse {
		t.Errorf("attrs: %d", g.CurAttrs)
	}
	feed(t, g, p, []byte("\x1b[22;24;27m"))
	if g.CurAttrs != 0 {
		t.Errorf("clear: %d", g.CurAttrs)
	}
}

func TestParser_SGR_FG_BG(t *testing.T) {
	g, p := newParserGrid(1, 1)
	feed(t, g, p, []byte("\x1b[31;42m"))
	if g.CurFG != 1 || g.CurBG != 2 {
		t.Errorf("fg/bg: %d %d", g.CurFG, g.CurBG)
	}
	feed(t, g, p, []byte("\x1b[39;49m"))
	if g.CurFG != DefaultColor || g.CurBG != DefaultColor {
		t.Errorf("default: %d %d", g.CurFG, g.CurBG)
	}
}

func TestParser_SGR_Bright(t *testing.T) {
	g, p := newParserGrid(1, 1)
	feed(t, g, p, []byte("\x1b[91;102m"))
	if g.CurFG != 9 || g.CurBG != 10 {
		t.Errorf("bright: %d %d", g.CurFG, g.CurBG)
	}
}

func TestParser_SGR38_5Swallowed(t *testing.T) {
	g, p := newParserGrid(1, 1)
	feed(t, g, p, []byte("\x1b[38;5;200;31m"))
	if g.CurFG != 1 {
		t.Errorf("trailing SGR after 38;5 not applied: fg=%d", g.CurFG)
	}
}

func TestParser_SGR38_2Swallowed(t *testing.T) {
	g, p := newParserGrid(1, 1)
	feed(t, g, p, []byte("\x1b[38;2;100;200;50;31m"))
	if g.CurFG != 1 {
		t.Errorf("trailing SGR after 38;2 not applied: fg=%d", g.CurFG)
	}
}

func TestParser_SGR256_AppliesPaletteIndex(t *testing.T) {
	g, p := newParserGrid(1, 1)
	feed(t, g, p, []byte("\x1b[38;5;200m"))
	if got, want := g.CurFG, paletteColor(200); got != want {
		t.Errorf("38;5;200 fg: got %#x want %#x", got, want)
	}
	feed(t, g, p, []byte("\x1b[48;5;17m"))
	if got, want := g.CurBG, paletteColor(17); got != want {
		t.Errorf("48;5;17 bg: got %#x want %#x", got, want)
	}
}

func TestParser_SGR256_OutOfRangeClamps(t *testing.T) {
	g, p := newParserGrid(1, 1)
	feed(t, g, p, []byte("\x1b[38;5;9999m"))
	if got, want := g.CurFG, paletteColor(255); got != want {
		t.Errorf("clamped 256-color: got %#x want %#x", got, want)
	}
}

func TestParser_SGRTruecolor_AppliesRGB(t *testing.T) {
	g, p := newParserGrid(1, 1)
	feed(t, g, p, []byte("\x1b[38;2;255;100;0m"))
	if got, want := g.CurFG, rgbColor(255, 100, 0); got != want {
		t.Errorf("38;2 fg: got %#x want %#x", got, want)
	}
	feed(t, g, p, []byte("\x1b[48;2;10;20;30m"))
	if got, want := g.CurBG, rgbColor(10, 20, 30); got != want {
		t.Errorf("48;2 bg: got %#x want %#x", got, want)
	}
}

func TestParser_SGRTruecolor_ChannelsClamp(t *testing.T) {
	g, p := newParserGrid(1, 1)
	// CSI params are unsigned; oversize values must saturate at 255
	// rather than overflow.
	feed(t, g, p, []byte("\x1b[38;2;300;500;128m"))
	if got, want := g.CurFG, rgbColor(255, 255, 128); got != want {
		t.Errorf("clamped channels: got %#x want %#x", got, want)
	}
}

func TestParser_SGR38_NoSelectorIsNoop(t *testing.T) {
	// "\x1b[38m" — extended-color introducer with no sub-form
	// selector. Must not change FG, must not panic.
	g, p := newParserGrid(1, 1)
	g.CurFG = paletteColor(7)
	feed(t, g, p, []byte("\x1b[38m"))
	if got, want := g.CurFG, paletteColor(7); got != want {
		t.Errorf("bare 38 should not change FG: got %#x want %#x", got, want)
	}
}

func TestParser_SGR_UnknownExtendedSelectorConsumesRest(t *testing.T) {
	g, p := newParserGrid(1, 1)
	g.CurFG = paletteColor(7)
	// Selector "9" is not 5 or 2; remaining params should be dropped,
	// leaving CurFG untouched.
	feed(t, g, p, []byte("\x1b[38;9;1;2;3;4m"))
	if got, want := g.CurFG, paletteColor(7); got != want {
		t.Errorf("unknown selector should not change FG: got %#x want %#x", got, want)
	}
}

func TestParser_SGR38_2Truncated(t *testing.T) {
	g, p := newParserGrid(1, 1)
	// only 2 of 4 follow-up params present — must not panic / read past end
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic on truncated 38;2: %v", r)
		}
	}()
	feed(t, g, p, []byte("\x1b[38;2;1m"))
}

func TestParser_CSI_CursorMoves(t *testing.T) {
	g, p := newParserGrid(5, 5)
	feed(t, g, p, []byte("\x1b[3;4H")) // 1-based row;col → (2,3)
	if g.CursorR != 2 || g.CursorC != 3 {
		t.Errorf("H: %d %d", g.CursorR, g.CursorC)
	}
	feed(t, g, p, []byte("\x1b[A")) // up 1
	if g.CursorR != 1 {
		t.Errorf("A: %d", g.CursorR)
	}
	feed(t, g, p, []byte("\x1b[2B")) // down 2
	if g.CursorR != 3 {
		t.Errorf("B: %d", g.CursorR)
	}
	feed(t, g, p, []byte("\x1b[2C")) // forward 2
	if g.CursorC != 4 {              // clamped at last col
		t.Errorf("C: %d", g.CursorC)
	}
	feed(t, g, p, []byte("\x1b[2D")) // back 2
	if g.CursorC != 2 {
		t.Errorf("D: %d", g.CursorC)
	}
}

func TestParser_CSI_EraseInDisplayLine(t *testing.T) {
	g, p := newParserGrid(2, 3)
	g.At(0, 0).Ch = 'a'
	g.At(0, 1).Ch = 'b'
	g.At(0, 2).Ch = 'c'
	g.MoveCursor(0, 1)
	feed(t, g, p, []byte("\x1b[K")) // EL mode 0 = cursor to EOL
	if g.At(0, 0).Ch != 'a' || g.At(0, 1).Ch != ' ' || g.At(0, 2).Ch != ' ' {
		t.Errorf("EL 0: %v %v %v",
			g.At(0, 0).Ch, g.At(0, 1).Ch, g.At(0, 2).Ch)
	}
	g.At(1, 0).Ch = 'x'
	g.MoveCursor(0, 0)
	feed(t, g, p, []byte("\x1b[2J")) // ED mode 2 = clear all
	for _, c := range g.Cells {
		if c.Ch != ' ' {
			t.Fatalf("ED 2 left content: %v", c.Ch)
		}
	}
}

func TestParser_CSI_UnknownDropped(t *testing.T) {
	g, p := newParserGrid(1, 5)
	g.Put('z')
	feed(t, g, p, []byte("\x1b[Z")) // CBT — unknown to dispatcher
	if g.At(0, 0).Ch != 'z' || g.CursorC != 1 {
		t.Errorf("unknown CSI mutated state: %v cursor=%d",
			g.At(0, 0).Ch, g.CursorC)
	}
}

func TestParser_CursorSaveRestore_ESC78(t *testing.T) {
	g, p := newParserGrid(5, 10)
	g.MoveCursor(2, 4)
	feed(t, g, p, []byte("\x1b[31m")) // FG red
	feed(t, g, p, []byte("\x1b7"))    // DECSC
	g.MoveCursor(0, 0)
	feed(t, g, p, []byte("\x1b[32m")) // FG green
	feed(t, g, p, []byte("\x1b8"))    // DECRC
	if g.CursorR != 2 || g.CursorC != 4 {
		t.Errorf("cursor not restored: r=%d c=%d", g.CursorR, g.CursorC)
	}
	if g.CurFG != paletteColor(1) {
		t.Errorf("FG not restored: %#x", g.CurFG)
	}
}

func TestParser_CursorSaveRestore_CSIsu(t *testing.T) {
	g, p := newParserGrid(5, 10)
	g.MoveCursor(3, 7)
	g.CurAttrs = AttrBold
	feed(t, g, p, []byte("\x1b[s"))
	g.MoveCursor(0, 0)
	g.CurAttrs = 0
	feed(t, g, p, []byte("\x1b[u"))
	if g.CursorR != 3 || g.CursorC != 7 {
		t.Errorf("CSI u: r=%d c=%d", g.CursorR, g.CursorC)
	}
	if g.CurAttrs != AttrBold {
		t.Errorf("CSI u attrs: %d", g.CurAttrs)
	}
}

func TestParser_RestoreWithoutSaveResets(t *testing.T) {
	g, p := newParserGrid(5, 10)
	g.MoveCursor(2, 3)
	g.CurFG = paletteColor(5)
	g.CurAttrs = AttrUnderline
	feed(t, g, p, []byte("\x1b8")) // DECRC, no prior save
	if g.CursorR != 0 || g.CursorC != 0 {
		t.Errorf("home: r=%d c=%d", g.CursorR, g.CursorC)
	}
	if g.CurFG != DefaultColor || g.CurAttrs != 0 {
		t.Errorf("SGR not reset: fg=%#x attrs=%d", g.CurFG, g.CurAttrs)
	}
}

func TestParser_DEC25_CursorVisibility(t *testing.T) {
	g, p := newParserGrid(2, 5)
	if !g.CursorVisible {
		t.Fatal("default CursorVisible should be true")
	}
	feed(t, g, p, []byte("\x1b[?25l"))
	if g.CursorVisible {
		t.Errorf("?25l: still visible")
	}
	feed(t, g, p, []byte("\x1b[?25h"))
	if !g.CursorVisible {
		t.Errorf("?25h: still hidden")
	}
}

func TestParser_DEC2004_BracketedPaste(t *testing.T) {
	g, p := newParserGrid(2, 5)
	if g.BracketedPaste {
		t.Fatal("default should be off")
	}
	feed(t, g, p, []byte("\x1b[?2004h"))
	if !g.BracketedPaste {
		t.Errorf("?2004h: still off")
	}
	feed(t, g, p, []byte("\x1b[?2004l"))
	if g.BracketedPaste {
		t.Errorf("?2004l: still on")
	}
}

func TestParser_DEC7_AutoWrap(t *testing.T) {
	g, p := newParserGrid(1, 3)
	if !g.AutoWrap {
		t.Fatal("default autowrap should be on")
	}
	feed(t, g, p, []byte("\x1b[?7l"))
	if g.AutoWrap {
		t.Fatal("?7l should disable autowrap")
	}
	feed(t, g, p, []byte("abcd"))
	if got := string([]rune{g.At(0, 0).Ch, g.At(0, 1).Ch, g.At(0, 2).Ch}); got != "abd" {
		t.Fatalf("nowrap overwrite = %q, want %q", got, "abd")
	}
	if g.CursorC != 2 {
		t.Fatalf("nowrap cursor = %d, want 2", g.CursorC)
	}
	feed(t, g, p, []byte("\x1b[?7h"))
	if !g.AutoWrap {
		t.Fatal("?7h should enable autowrap")
	}
}

func TestParser_DEC6_OriginMode(t *testing.T) {
	g, p := newParserGrid(6, 5)
	feed(t, g, p, []byte("\x1b[2;5r"))
	feed(t, g, p, []byte("\x1b[?6h"))
	if !g.OriginMode {
		t.Fatal("?6h should enable origin mode")
	}
	if g.CursorR != 1 || g.CursorC != 0 {
		t.Fatalf("origin home = %d,%d, want 1,0", g.CursorR, g.CursorC)
	}
	feed(t, g, p, []byte("\x1b[2;3H"))
	if g.CursorR != 2 || g.CursorC != 2 {
		t.Fatalf("origin CUP = %d,%d, want 2,2", g.CursorR, g.CursorC)
	}
	feed(t, g, p, []byte("\x1b[99B"))
	if g.CursorR != 4 {
		t.Fatalf("origin CUD clamp = %d, want 4", g.CursorR)
	}
	feed(t, g, p, []byte("\x1b[?6l"))
	if g.OriginMode {
		t.Fatal("?6l should disable origin mode")
	}
}

func TestParser_CSI4_InsertMode(t *testing.T) {
	g, p := newParserGrid(1, 4)
	feed(t, g, p, []byte("abcd"))
	g.CursorR, g.CursorC = 0, 1
	feed(t, g, p, []byte("\x1b[4h"))
	if !g.InsertMode {
		t.Fatal("CSI 4 h should enable insert mode")
	}
	feed(t, g, p, []byte("X"))
	got := string([]rune{g.At(0, 0).Ch, g.At(0, 1).Ch, g.At(0, 2).Ch, g.At(0, 3).Ch})
	if got != "aXbc" {
		t.Fatalf("IRM row = %q, want %q", got, "aXbc")
	}
	feed(t, g, p, []byte("\x1b[4l"))
	if g.InsertMode {
		t.Fatal("CSI 4 l should disable insert mode")
	}
}

func TestParser_DECMode_FocusSyncCursorKeypad(t *testing.T) {
	g, p := newParserGrid(1, 5)
	feed(t, g, p, []byte("\x1b[?1004;2026;1h\x1b="))
	if !g.FocusReporting || !g.SyncOutput || !g.AppCursorKeys || !g.AppKeypad {
		t.Fatalf("mode set failed: focus=%v sync=%v ckm=%v keypad=%v",
			g.FocusReporting, g.SyncOutput, g.AppCursorKeys, g.AppKeypad)
	}
	feed(t, g, p, []byte("\x1bP=1s\x1b\\"))
	if !g.SyncActive {
		t.Fatal("sync begin not set")
	}
	feed(t, g, p, []byte("\x1bP=2s\x1b\\"))
	if g.SyncActive {
		t.Fatal("sync end not cleared")
	}
	feed(t, g, p, []byte("\x1b[?1004;2026;1l\x1b>"))
	if g.FocusReporting || g.SyncOutput || g.SyncActive || g.AppCursorKeys || g.AppKeypad {
		t.Fatalf("mode reset failed: focus=%v sync=%v active=%v ckm=%v keypad=%v",
			g.FocusReporting, g.SyncOutput, g.SyncActive, g.AppCursorKeys, g.AppKeypad)
	}
}

func TestParser_DECPrivateResetBetweenSequences(t *testing.T) {
	// Ensure the `?` prefix on one CSI doesn't leak into the next CSI,
	// which would cause CSI 25 m (a no-op SGR) to be misread as DEC mode.
	g, p := newParserGrid(2, 5)
	feed(t, g, p, []byte("\x1b[?25l")) // hides cursor
	feed(t, g, p, []byte("\x1b[31m"))  // plain SGR FG red
	if g.CursorVisible {
		t.Fatal("?25l should still be in effect")
	}
	if g.CurFG != paletteColor(1) {
		t.Errorf("SGR after DEC mode: fg=%#x", g.CurFG)
	}
}

func TestParser_NonDECPrivateLeaderDoesNotFallThroughToSGR(t *testing.T) {
	// fish emits xterm-private CSI > 4 ; 1 m during startup. Treating
	// that as plain SGR 4;1m leaves subsequent erased blanks underlined.
	g, p := newParserGrid(2, 5)
	feed(t, g, p, []byte("\x1b[>4;1m"))
	if g.CurAttrs != 0 {
		t.Fatalf("CSI > 4;1m changed attrs: %#x", g.CurAttrs)
	}
	feed(t, g, p, []byte("\x1b[K"))
	for c := range g.Cols {
		cell := g.At(0, c)
		if cell.Attrs != 0 {
			t.Fatalf("erased cell %d kept attrs %#x", c, cell.Attrs)
		}
	}
}

func TestParser_DECSTBM_SetAndReset(t *testing.T) {
	g, p := newParserGrid(10, 4)
	feed(t, g, p, []byte("\x1b[3;7r"))
	if g.Top != 2 || g.Bottom != 6 {
		t.Errorf("DECSTBM 3;7 → %d..%d, want 2..6", g.Top, g.Bottom)
	}
	if g.CursorR != 0 || g.CursorC != 0 {
		t.Errorf("DECSTBM did not home cursor")
	}
	// Bare CSI r resets to full screen.
	feed(t, g, p, []byte("\x1b[r"))
	if g.Top != 0 || g.Bottom != 9 {
		t.Errorf("bare DECSTBM reset failed: %d..%d", g.Top, g.Bottom)
	}
}

func TestParser_IND_RI_NEL(t *testing.T) {
	g, p := newParserGrid(5, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		for c := range g.Cols {
			g.At(i, c).Ch = ch
		}
	}
	feed(t, g, p, []byte("\x1b[2;4r")) // region rows B..D
	g.CursorR = 3                      // at Bottom
	feed(t, g, p, []byte("\x1bD"))     // IND scrolls region
	if g.At(1, 0).Ch != 'C' || g.At(2, 0).Ch != 'D' || g.At(3, 0).Ch != ' ' {
		t.Errorf("IND scroll wrong: %q %q %q",
			g.At(1, 0).Ch, g.At(2, 0).Ch, g.At(3, 0).Ch)
	}
	g.CursorR = 1 // at Top
	feed(t, g, p, []byte("\x1bM"))
	if g.At(1, 0).Ch != ' ' || g.At(2, 0).Ch != 'C' || g.At(3, 0).Ch != 'D' {
		t.Errorf("RI scroll wrong: %q %q %q",
			g.At(1, 0).Ch, g.At(2, 0).Ch, g.At(3, 0).Ch)
	}
	g.CursorR, g.CursorC = 1, 1
	feed(t, g, p, []byte("\x1bE")) // NEL: CR + LF
	if g.CursorC != 0 || g.CursorR != 2 {
		t.Errorf("NEL cursor: %d,%d", g.CursorR, g.CursorC)
	}
}

func TestParser_InsertDeleteLines(t *testing.T) {
	g, p := newParserGrid(5, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		for c := range g.Cols {
			g.At(i, c).Ch = ch
		}
	}
	feed(t, g, p, []byte("\x1b[2;4r")) // region B..D
	g.CursorR = 2
	feed(t, g, p, []byte("\x1b[L")) // IL 1 default
	// expect rows: A B (blank) C E
	want := []rune{'A', 'B', ' ', 'C', 'E'}
	for i, w := range want {
		if g.At(i, 0).Ch != w {
			t.Errorf("after IL row %d = %q, want %q", i, g.At(i, 0).Ch, w)
		}
	}
	// DL 2 at row 2 (still in region B..D): deletes rows 2 and 3, no
	// rows below to shift up within region, fills with blank.
	feed(t, g, p, []byte("\x1b[2M"))
	want = []rune{'A', 'B', ' ', ' ', 'E'}
	for i, w := range want {
		if g.At(i, 0).Ch != w {
			t.Errorf("after DL row %d = %q, want %q", i, g.At(i, 0).Ch, w)
		}
	}
}

func TestParser_InsertDeleteChars(t *testing.T) {
	g, p := newParserGrid(1, 6)
	for c := range g.Cols {
		g.At(0, c).Ch = rune('a' + c)
	}
	g.CursorC = 2
	feed(t, g, p, []byte("\x1b[2@")) // ICH 2
	want := []rune{'a', 'b', ' ', ' ', 'c', 'd'}
	for i, w := range want {
		if g.At(0, i).Ch != w {
			t.Errorf("after ICH col %d = %q, want %q", i, g.At(0, i).Ch, w)
		}
	}
	g.CursorC = 0
	feed(t, g, p, []byte("\x1b[3P")) // DCH 3 at col 0
	// row was a b _ _ c d ; deleting 3 from col 0 → _ c d _ _ _
	want = []rune{' ', 'c', 'd', ' ', ' ', ' '}
	for i, w := range want {
		if g.At(0, i).Ch != w {
			t.Errorf("after DCH col %d = %q, want %q", i, g.At(0, i).Ch, w)
		}
	}
}

func TestParser_SU_SD(t *testing.T) {
	g, p := newParserGrid(4, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D'} {
		for c := range g.Cols {
			g.At(i, c).Ch = ch
		}
	}
	feed(t, g, p, []byte("\x1b[S")) // SU 1
	want := []rune{'B', 'C', 'D', ' '}
	for i, w := range want {
		if g.At(i, 0).Ch != w {
			t.Errorf("after SU row %d = %q, want %q", i, g.At(i, 0).Ch, w)
		}
	}
	feed(t, g, p, []byte("\x1b[T")) // SD 1
	want = []rune{' ', 'B', 'C', 'D'}
	for i, w := range want {
		if g.At(i, 0).Ch != w {
			t.Errorf("after SD row %d = %q, want %q", i, g.At(i, 0).Ch, w)
		}
	}
}

func TestParser_DEC47_AltScreen(t *testing.T) {
	g, p := newParserGrid(2, 3)
	feed(t, g, p, []byte("hi"))
	feed(t, g, p, []byte("\x1b[?47h"))
	if !g.AltActive {
		t.Fatal("?47h: AltActive should be true")
	}
	feed(t, g, p, []byte("\x1b[?47l"))
	if g.AltActive {
		t.Fatal("?47l: AltActive should be false")
	}
	if g.At(0, 0).Ch != 'h' || g.At(0, 1).Ch != 'i' {
		t.Errorf("main not restored: %q%q", g.At(0, 0).Ch, g.At(0, 1).Ch)
	}
}

func TestParser_DEC1047_AltScreen(t *testing.T) {
	g, p := newParserGrid(2, 3)
	feed(t, g, p, []byte("ab"))
	feed(t, g, p, []byte("\x1b[?1047h"))
	if !g.AltActive {
		t.Fatal("?1047h: AltActive should be true")
	}
	feed(t, g, p, []byte("\x1b[?1047l"))
	if g.AltActive {
		t.Fatal("?1047l: AltActive should be false")
	}
	if g.At(0, 0).Ch != 'a' {
		t.Errorf("main row 0 col 0 = %q, want a", g.At(0, 0).Ch)
	}
}

func TestParser_DEC1049_SavesAndRestoresCursor(t *testing.T) {
	g, p := newParserGrid(4, 6)
	feed(t, g, p, []byte("hello\r\nworld"))
	mainR, mainC := g.CursorR, g.CursorC
	feed(t, g, p, []byte("\x1b[?1049h"))
	if !g.AltActive {
		t.Fatal("?1049h: AltActive should be true")
	}
	if g.CursorR != 0 || g.CursorC != 0 {
		t.Errorf("alt entry: cursor not homed: %d,%d", g.CursorR, g.CursorC)
	}
	// Mutate alt-buffer cursor + DECSC slot to verify isolation.
	feed(t, g, p, []byte("\x1b[3;3HALT")) // move + write
	feed(t, g, p, []byte("\x1b[s"))       // alt-buffer save
	feed(t, g, p, []byte("\x1b[?1049l"))
	if g.AltActive {
		t.Fatal("?1049l: AltActive should be false")
	}
	if g.CursorR != mainR || g.CursorC != mainC {
		t.Errorf("?1049l: cursor not restored: got %d,%d want %d,%d",
			g.CursorR, g.CursorC, mainR, mainC)
	}
	row1 := []rune{'w', 'o', 'r', 'l', 'd'}
	for i, w := range row1 {
		if g.At(1, i).Ch != w {
			t.Errorf("main row 1 col %d = %q, want %q",
				i, g.At(1, i).Ch, w)
		}
	}
}

func TestParser_DEC1049_SuppressesScrollback(t *testing.T) {
	g, p := newParserGrid(2, 3)
	g.ScrollbackCap = 50
	feed(t, g, p, []byte("\x1b[?1049h"))
	for i := 0; i < 8; i++ {
		feed(t, g, p, []byte("x\r\n"))
	}
	if len(g.Scrollback) != 0 {
		t.Errorf("scrollback grew under ?1049: %d rows",
			len(g.Scrollback))
	}
	feed(t, g, p, []byte("\x1b[?1049l"))
	for i := 0; i < 5; i++ {
		feed(t, g, p, []byte("y\r\n"))
	}
	if len(g.Scrollback) == 0 {
		t.Errorf("scrollback inert after ?1049l")
	}
}

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
	feed(t, g, p, []byte("\x1b]52;c;Zm9v\x07")) // OSC 52 — not handled
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
	// Payload truncated at cap; "0;" prefix consumes 2 bytes of the
	// budget so title length is maxOSCBytes - 2.
	if len(got) != maxOSCBytes-2 {
		t.Errorf("truncated len=%d, want %d", len(got), maxOSCBytes-2)
	}
}

func TestParser_OSC_AbortedByBareESC(t *testing.T) {
	g, p := newParserGrid(1, 5)
	titles := 0
	p.SetTitleHandler(func(string) { titles++ })
	// ESC followed by something other than '\' aborts the OSC; the
	// trailing CSI must still be processed.
	feed(t, g, p, []byte("\x1b]0;abc\x1b[31m"))
	if titles != 0 {
		t.Errorf("aborted OSC dispatched title")
	}
	if g.CurFG != paletteColor(1) {
		t.Errorf("CSI after aborted OSC not applied: CurFG=%#x", g.CurFG)
	}
}

func TestParser_DA1_Reply(t *testing.T) {
	g, p := newParserGrid(1, 5)
	var replies [][]byte
	p.SetReplyHandler(func(b []byte) {
		replies = append(replies, append([]byte(nil), b...))
	})
	feed(t, g, p, []byte("\x1b[c"))
	if len(replies) != 1 || !bytes.Equal(replies[0], []byte("\x1b[?1;2c")) {
		t.Errorf("DA1 reply: %q", replies)
	}
}

func TestParser_DA1_ExplicitZero(t *testing.T) {
	g, p := newParserGrid(1, 5)
	got := 0
	p.SetReplyHandler(func([]byte) { got++ })
	feed(t, g, p, []byte("\x1b[0c"))
	if got != 1 {
		t.Errorf("CSI 0 c reply count=%d", got)
	}
}

func TestParser_DA1_NonZeroIgnored(t *testing.T) {
	g, p := newParserGrid(1, 5)
	got := 0
	p.SetReplyHandler(func([]byte) { got++ })
	feed(t, g, p, []byte("\x1b[1c"))
	if got != 0 {
		t.Errorf("CSI 1 c should not reply: %d", got)
	}
}

func TestParser_DA1_PrivateIgnored(t *testing.T) {
	g, p := newParserGrid(1, 5)
	got := 0
	p.SetReplyHandler(func([]byte) { got++ })
	feed(t, g, p, []byte("\x1b[?c"))
	if got != 0 {
		t.Errorf("CSI ? c should not reply: %d", got)
	}
}

func TestParser_CPRReply(t *testing.T) {
	g, p := newParserGrid(4, 8)
	g.CursorR, g.CursorC = 2, 5
	var replies [][]byte
	p.SetReplyHandler(func(b []byte) {
		replies = append(replies, append([]byte(nil), b...))
	})
	feed(t, g, p, []byte("\x1b[6n"))
	if len(replies) != 1 || string(replies[0]) != "\x1b[3;6R" {
		t.Fatalf("CPR reply = %q", replies)
	}
}

func TestParser_DCS_UnknownSwallowed(t *testing.T) {
	g, p := newParserGrid(1, 5)
	feed(t, g, p, []byte("\x1bPignored\x1b\\X"))
	if got := g.At(0, 0).Ch; got != 'X' {
		t.Fatalf("DCS leaked into grid: got %q want X", got)
	}
}

func TestParser_DECRQSS_Replies(t *testing.T) {
	g, p := newParserGrid(6, 8)
	g.CurAttrs = AttrBold | AttrUnderline
	g.CurFG = paletteColor(2)
	g.Top, g.Bottom = 1, 4
	g.ApplyDECSCUSR(6)
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	feed(t, g, p, []byte("\x1bP$qm\x1b\\"))
	feed(t, g, p, []byte("\x1bP$qr\x1b\\"))
	feed(t, g, p, []byte("\x1bP$q q\x1b\\"))
	want := []string{
		"\x1bP1$r1;4;32m\x1b\\",
		"\x1bP1$r2;5r\x1b\\",
		"\x1bP1$r6 q\x1b\\",
	}
	if len(replies) != len(want) {
		t.Fatalf("DECRQSS reply count = %d, want %d", len(replies), len(want))
	}
	for i := range want {
		if replies[i] != want[i] {
			t.Fatalf("reply[%d] = %q, want %q", i, replies[i], want[i])
		}
	}
}

func TestParser_XTGETTCAP_Reply(t *testing.T) {
	g, p := newParserGrid(1, 5)
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	feed(t, g, p, []byte("\x1bP+q544e;6b63757531\x1b\\")) // TN ; kcuu1
	want := "\x1bP1+r544e=787465726d2d323536636f6c6f72;6b63757531=1b5b41\x1b\\"
	if len(replies) != 1 || replies[0] != want {
		t.Fatalf("XTGETTCAP = %q, want %q", replies, want)
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

func TestParser_DECSCUSR_AllPs(t *testing.T) {
	cases := []struct {
		ps    int
		shape CursorShape
		blink bool
	}{
		{0, CursorBlock, true},
		{1, CursorBlock, true},
		{2, CursorBlock, false},
		{3, CursorUnderline, true},
		{4, CursorUnderline, false},
		{5, CursorBar, true},
		{6, CursorBar, false},
		{99, CursorBlock, true}, // unknown → default
	}
	for _, c := range cases {
		g, p := newParserGrid(1, 5)
		// Build "CSI Ps SP q" with Ps as decimal.
		seq := append([]byte("\x1b["), []byte(strconv.Itoa(c.ps))...)
		seq = append(seq, ' ', 'q')
		feed(t, g, p, seq)
		if g.CursorShape != c.shape || g.CursorBlink != c.blink {
			t.Errorf("Ps=%d: shape=%d blink=%v, want shape=%d blink=%v",
				c.ps, g.CursorShape, g.CursorBlink, c.shape, c.blink)
		}
	}
}

func TestParser_DECSCUSR_RequiresSpaceIntermediate(t *testing.T) {
	g, p := newParserGrid(1, 5)
	g.CursorShape = CursorBar
	g.CursorBlink = false
	// Same final byte 'q' without the space intermediate: must not
	// touch DECSCUSR state.
	feed(t, g, p, []byte("\x1b[2q"))
	if g.CursorShape != CursorBar || g.CursorBlink != false {
		t.Errorf("CSI 2 q (no SP) clobbered shape=%d blink=%v",
			g.CursorShape, g.CursorBlink)
	}
}

func TestParser_DECSCUSR_DefaultParam(t *testing.T) {
	g, p := newParserGrid(1, 5)
	feed(t, g, p, []byte("\x1b[ q")) // no Ps → 0 → blinking block
	if g.CursorShape != CursorBlock || !g.CursorBlink {
		t.Errorf("default DECSCUSR: shape=%d blink=%v",
			g.CursorShape, g.CursorBlink)
	}
}

func TestCurrentSGRString_AllDefault(t *testing.T) {
	_, p := newParserGrid(1, 5)
	if got := p.currentSGRString(); got != "0m" {
		t.Errorf("all-default = %q, want %q", got, "0m")
	}
}

func TestCurrentSGRString_AttrInverse(t *testing.T) {
	g, p := newParserGrid(1, 5)
	g.CurAttrs = AttrInverse
	if got := p.currentSGRString(); got != "7m" {
		t.Errorf("inverse = %q, want %q", got, "7m")
	}
}

func TestCurrentSGRString_BrightPalette(t *testing.T) {
	cases := []struct {
		fg   uint32
		want string
	}{
		{paletteColor(8), "90m"},
		{paletteColor(15), "97m"},
	}
	for _, c := range cases {
		g, p := newParserGrid(1, 5)
		g.CurFG = c.fg
		if got := p.currentSGRString(); got != c.want {
			t.Errorf("fg=%#x: got %q, want %q", c.fg, got, c.want)
		}
	}
}

func TestCurrentSGRString_TruecolorFGBG(t *testing.T) {
	g, p := newParserGrid(1, 5)
	g.CurFG = rgbColor(255, 100, 0)
	g.CurBG = rgbColor(0, 128, 255)
	want := "38;2;255;100;0;48;2;0;128;255m"
	if got := p.currentSGRString(); got != want {
		t.Errorf("truecolor = %q, want %q", got, want)
	}
}

func TestParser_XTGETTCAP_UnknownCapReturnsHexName(t *testing.T) {
	// "unknown" hex-encoded = "756e6b6e6f776e"
	g, p := newParserGrid(1, 5)
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	feed(t, g, p, []byte("\x1bP+q756e6b6e6f776e\x1b\\"))
	want := "\x1bP0+r756e6b6e6f776e\x1b\\"
	if len(replies) != 1 || replies[0] != want {
		t.Fatalf("unknown cap reply = %q, want %q", replies, want)
	}
}

func TestParser_XTGETTCAP_InvalidHexReturnsErrorWithPart(t *testing.T) {
	// "54e" is 3 bytes (odd) — decodeHexBytes must reject it
	g, p := newParserGrid(1, 5)
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	feed(t, g, p, []byte("\x1bP+q54e\x1b\\"))
	want := "\x1bP0+r54e\x1b\\"
	if len(replies) != 1 || replies[0] != want {
		t.Fatalf("invalid hex reply = %q, want %q", replies, want)
	}
}

func TestParser_XTGETTCAP_EmptyBodyReturnsError(t *testing.T) {
	g, p := newParserGrid(1, 5)
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	feed(t, g, p, []byte("\x1bP+q\x1b\\"))
	want := "\x1bP0+r\x1b\\"
	if len(replies) != 1 || replies[0] != want {
		t.Fatalf("empty body reply = %q, want %q", replies, want)
	}
}

func TestSplitSemis_CapsAtMax(t *testing.T) {
	// 41 fields separated by 40 semicolons — must be capped at maxXTGETTCAPParts
	var b []byte
	for i := 0; i < 41; i++ {
		if i > 0 {
			b = append(b, ';')
		}
		b = append(b, 'x')
	}
	parts := splitSemis(b)
	if len(parts) > maxXTGETTCAPParts {
		t.Errorf("splitSemis returned %d parts, want ≤%d", len(parts), maxXTGETTCAPParts)
	}
}

func TestParser_DECRQSS_LongBodyReturnsNotOk(t *testing.T) {
	// 6-byte body exceeds the 4-byte early-exit guard
	g, p := newParserGrid(1, 5)
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	feed(t, g, p, []byte("\x1bP$qfoobar\x1b\\"))
	want := "\x1bP0$r\x1b\\"
	if len(replies) != 1 || replies[0] != want {
		t.Fatalf("long body DECRQSS = %q, want %q", replies, want)
	}
}

func TestParser_CursorSaveRestore_PreservesAutoWrapOriginInsert(t *testing.T) {
	g, p := newParserGrid(5, 8)
	g.AutoWrap = false
	g.OriginMode = true
	g.InsertMode = true
	feed(t, g, p, []byte("\x1b7")) // DECSC
	g.AutoWrap = true
	g.OriginMode = false
	g.InsertMode = false
	feed(t, g, p, []byte("\x1b8")) // DECRC
	if g.AutoWrap {
		t.Error("AutoWrap should be restored to false")
	}
	if !g.OriginMode {
		t.Error("OriginMode should be restored to true")
	}
	if !g.InsertMode {
		t.Error("InsertMode should be restored to true")
	}
}

func TestParser_SGR_NewAttrs_Set(t *testing.T) {
	tests := []struct {
		name string
		seq  string
		want uint8
	}{
		{"dim", "\x1b[2m", AttrDim},
		{"italic", "\x1b[3m", AttrItalic},
		{"strikethrough", "\x1b[9m", AttrStrikethrough},
		{"bold+dim", "\x1b[1m\x1b[2m", AttrBold | AttrDim},
		{"bold+italic", "\x1b[1m\x1b[3m", AttrBold | AttrItalic},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, p := newParserGrid(1, 1)
			feed(t, g, p, []byte(tt.seq))
			if g.CurAttrs&tt.want != tt.want {
				t.Errorf("attrs=%08b want %08b set", g.CurAttrs, tt.want)
			}
		})
	}
}

func TestParser_SGR_NewAttrs_Clear(t *testing.T) {
	tests := []struct {
		name     string
		setSeq   string
		clearSeq string
		bits     uint8
	}{
		{"dim via 22", "\x1b[2m", "\x1b[22m", AttrDim},
		{"bold+dim via 22", "\x1b[1m\x1b[2m", "\x1b[22m", AttrBold | AttrDim},
		{"italic via 23", "\x1b[3m", "\x1b[23m", AttrItalic},
		{"strikethrough via 29", "\x1b[9m", "\x1b[29m", AttrStrikethrough},
		{"all via SGR 0", "\x1b[1m\x1b[2m\x1b[3m\x1b[9m", "\x1b[0m", AttrBold | AttrDim | AttrItalic | AttrStrikethrough},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, p := newParserGrid(1, 1)
			feed(t, g, p, []byte(tt.setSeq))
			if g.CurAttrs&tt.bits != tt.bits {
				t.Errorf("after set: attrs=%08b want %08b set", g.CurAttrs, tt.bits)
			}
			feed(t, g, p, []byte(tt.clearSeq))
			if g.CurAttrs&tt.bits != 0 {
				t.Errorf("after clear: attrs=%08b want %08b clear", g.CurAttrs, tt.bits)
			}
		})
	}
}

// OSC 8 — hyperlinks

func TestParser_OSC8_OpenLink(t *testing.T) {
	g, p := newParserGrid(5, 40)
	// OSC 8 ;params;URI BEL
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
	feed(t, g, p, []byte("\x1b]8;;\x07")) // empty URI = close
	g.Mu.Lock()
	defer g.Mu.Unlock()
	if g.CurLinkID != 0 {
		t.Errorf("CurLinkID = %d after OSC 8 close, want 0", g.CurLinkID)
	}
}

func TestParser_OSC8_MalformedNoSecondSemi(t *testing.T) {
	g, p := newParserGrid(5, 40)
	// Missing second semicolon — entire sequence should be ignored
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

// OSC 52 — clipboard

func TestParser_OSC52_Write(t *testing.T) {
	g, p := newParserGrid(5, 40)
	var got []byte
	p.SetClipboardHandler(func(data []byte) {
		got = append([]byte(nil), data...)
	})
	// base64("hello world") = "aGVsbG8gd29ybGQ="
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
	feed(t, g, p, []byte("\x1b]52;aGVsbG8=\x07")) // missing second semicolon
	if called {
		t.Error("onClipboard called for malformed OSC 52 (no second semicolon)")
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
	// BEL as OSC terminator must not double-count as a bell.
	g, p := newParserGrid(5, 40)
	var title string
	p.SetTitleHandler(func(s string) { title = s })
	feed(t, g, p, []byte("\x1b]0;hello\x07"))
	if title != "hello" {
		t.Errorf("OSC title = %q, want %q", title, "hello")
	}
	// The BEL here terminated the OSC; BellCount must remain 0.
	if g.BellCount != 0 {
		t.Errorf("OSC-terminator BEL incremented BellCount = %d, want 0", g.BellCount)
	}
}

// --- Phase 20: Extended Underline Styles & Colors ---

func TestParser_SGR4_NoSubparam_SingleUnderline(t *testing.T) {
	g, p := newParserGrid(2, 10)
	feed(t, g, p, []byte("\x1b[4m"))
	if g.CurAttrs&AttrUnderline == 0 {
		t.Error("SGR 4: AttrUnderline not set")
	}
	if g.CurULStyle != ULSingle {
		t.Errorf("SGR 4: CurULStyle = %d, want ULSingle (%d)", g.CurULStyle, ULSingle)
	}
}

func TestParser_SGR4_ColonSubparam_Styles(t *testing.T) {
	cases := []struct {
		seq     string
		style   uint8
		hasAttr bool
	}{
		{"\x1b[4:0m", ULNone, false},  // explicit no-underline
		{"\x1b[4:1m", ULSingle, true},
		{"\x1b[4:2m", ULDouble, true},
		{"\x1b[4:3m", ULCurly, true},
		{"\x1b[4:4m", ULDotted, true},
		{"\x1b[4:5m", ULDashed, true},
	}
	for _, c := range cases {
		g, p := newParserGrid(2, 10)
		// pre-set underline so 4:0 can clear it
		g.CurAttrs |= AttrUnderline
		g.CurULStyle = ULSingle
		feed(t, g, p, []byte(c.seq))
		if g.CurULStyle != c.style {
			t.Errorf("seq %q: CurULStyle = %d, want %d", c.seq, g.CurULStyle, c.style)
		}
		if c.hasAttr && g.CurAttrs&AttrUnderline == 0 {
			t.Errorf("seq %q: AttrUnderline not set", c.seq)
		}
		if !c.hasAttr && g.CurAttrs&AttrUnderline != 0 {
			t.Errorf("seq %q: AttrUnderline should be cleared", c.seq)
		}
	}
}

func TestParser_SGR21_DoubleUnderline(t *testing.T) {
	g, p := newParserGrid(2, 10)
	feed(t, g, p, []byte("\x1b[21m"))
	if g.CurAttrs&AttrUnderline == 0 {
		t.Error("SGR 21: AttrUnderline not set")
	}
	if g.CurULStyle != ULDouble {
		t.Errorf("SGR 21: CurULStyle = %d, want ULDouble (%d)", g.CurULStyle, ULDouble)
	}
}

func TestParser_SGR24_ClearsUnderline(t *testing.T) {
	g, p := newParserGrid(2, 10)
	g.CurAttrs |= AttrUnderline
	g.CurULStyle = ULCurly
	g.CurULColor = rgbColor(255, 0, 0)
	feed(t, g, p, []byte("\x1b[24m"))
	if g.CurAttrs&AttrUnderline != 0 {
		t.Error("SGR 24: AttrUnderline should be cleared")
	}
	if g.CurULStyle != ULNone {
		t.Errorf("SGR 24: CurULStyle = %d, want 0", g.CurULStyle)
	}
	if g.CurULColor != DefaultColor {
		t.Errorf("SGR 24: CurULColor = %#x, want DefaultColor", g.CurULColor)
	}
}

func TestParser_SGR58_ULColor_RGB(t *testing.T) {
	g, p := newParserGrid(2, 10)
	feed(t, g, p, []byte("\x1b[58;2;255;128;0m"))
	want := rgbColor(255, 128, 0)
	if g.CurULColor != want {
		t.Errorf("SGR 58 RGB: CurULColor = %#x, want %#x", g.CurULColor, want)
	}
}

func TestParser_SGR58_ULColor_Palette(t *testing.T) {
	g, p := newParserGrid(2, 10)
	feed(t, g, p, []byte("\x1b[58;5;196m"))
	want := paletteColor(196)
	if g.CurULColor != want {
		t.Errorf("SGR 58 palette: CurULColor = %#x, want %#x", g.CurULColor, want)
	}
}

func TestParser_SGR59_ResetsULColor(t *testing.T) {
	g, p := newParserGrid(2, 10)
	g.CurULColor = rgbColor(0, 255, 0)
	feed(t, g, p, []byte("\x1b[59m"))
	if g.CurULColor != DefaultColor {
		t.Errorf("SGR 59: CurULColor = %#x, want DefaultColor", g.CurULColor)
	}
}

func TestParser_SGRReset_ClearsULState(t *testing.T) {
	g, p := newParserGrid(2, 10)
	g.CurULStyle = ULCurly
	g.CurULColor = rgbColor(100, 200, 50)
	g.CurAttrs |= AttrUnderline
	feed(t, g, p, []byte("\x1b[0m"))
	if g.CurULStyle != ULNone {
		t.Errorf("SGR 0: CurULStyle = %d, want 0", g.CurULStyle)
	}
	if g.CurULColor != DefaultColor {
		t.Errorf("SGR 0: CurULColor = %#x, want DefaultColor", g.CurULColor)
	}
	if g.CurAttrs&AttrUnderline != 0 {
		t.Error("SGR 0: AttrUnderline should be cleared")
	}
}

func TestParser_SGR4_Semicolon_NotSubparam(t *testing.T) {
	// "4;3m" = SGR 4 (underline) + SGR 3 (italic) — NOT curly underline.
	g, p := newParserGrid(2, 10)
	feed(t, g, p, []byte("\x1b[4;3m"))
	if g.CurULStyle != ULSingle {
		t.Errorf("4;3m: CurULStyle = %d, want ULSingle (semicolon ≠ colon)", g.CurULStyle)
	}
	if g.CurAttrs&AttrItalic == 0 {
		t.Error("4;3m: AttrItalic should also be set")
	}
}

func TestParser_HTS_SetTabStop(t *testing.T) {
	g, p := newParserGrid(1, 80)
	g.Mu.Lock()
	g.CursorC = 12
	g.Mu.Unlock()
	feed(t, g, p, []byte("\x1bH")) // ESC H = HTS
	g.Mu.Lock()
	got := g.TabStops[12]
	g.Mu.Unlock()
	if !got {
		t.Error("ESC H: tab stop not set at col 12")
	}
}

func TestParser_TBC_ClearAtCursor(t *testing.T) {
	g, p := newParserGrid(1, 80)
	// Default stop at 8; position cursor there and clear it.
	g.Mu.Lock()
	g.CursorC = 8
	g.Mu.Unlock()
	feed(t, g, p, []byte("\x1b[g")) // CSI g = TBC Ps=0
	g.Mu.Lock()
	got := g.TabStops[8]
	g.Mu.Unlock()
	if got {
		t.Error("CSI g: stop at col 8 should be cleared")
	}
}

func TestParser_TBC_ClearAll(t *testing.T) {
	g, p := newParserGrid(1, 80)
	feed(t, g, p, []byte("\x1b[3g")) // CSI 3 g = TBC clear all
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
	// Clear all, then set custom stops at 5 and 10.
	feed(t, g, p, []byte("\x1b[3g")) // clear all
	g.Mu.Lock()
	g.CursorC = 5
	g.Mu.Unlock()
	feed(t, g, p, []byte("\x1bH")) // set stop at 5
	g.Mu.Lock()
	g.CursorC = 10
	g.Mu.Unlock()
	feed(t, g, p, []byte("\x1bH")) // set stop at 10

	g.Mu.Lock()
	defer g.Mu.Unlock()
	// Only stops at 5 and 10 should be set.
	for c := 0; c < 20; c++ {
		want := c == 5 || c == 10
		if g.TabStops[c] != want {
			t.Errorf("col %d: TabStops=%v, want %v", c, g.TabStops[c], want)
		}
	}
	// Tab from 0 → 5 → 10 → clamp.
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

