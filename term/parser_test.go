package term

import (
	"bytes"
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
	if g.CursorC != 4 { // clamps to Cols-1=4 since 8>5
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
	if p.state != stGround {
		t.Errorf("state not back to ground: %d", p.state)
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
	if g.CursorC != 4 { // clamped at last col
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
	feed(t, g, p, []byte("\x1b[?25h")) // hide cursor — unknown to parser
	if g.At(0, 0).Ch != 'z' || g.CursorC != 1 {
		t.Errorf("unknown CSI mutated state: %v cursor=%d",
			g.At(0, 0).Ch, g.CursorC)
	}
}
