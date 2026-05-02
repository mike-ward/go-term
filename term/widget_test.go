package term

import (
	"math"
	"testing"

	"github.com/mike-ward/go-gui/gui"
)

func TestRuneString_ASCIINoAlloc(t *testing.T) {
	var sink string
	avg := testing.AllocsPerRun(100, func() {
		sink = runeString('A')
	})
	if sink != "A" {
		t.Errorf("got %q", sink)
	}
	if avg != 0 {
		t.Errorf("ASCII path should not allocate, got %v allocs/op", avg)
	}
}

func TestRuneString_ASCIIAllRunes(t *testing.T) {
	for r := rune(0); r < 128; r++ {
		got := runeString(r)
		if got != string(r) {
			t.Errorf("runeString(%d) = %q, want %q", r, got, string(r))
		}
	}
}

func TestRuneString_NonASCII(t *testing.T) {
	cases := []rune{0x00E9, 0x2603, 0x1F600, 0xFFFD}
	for _, r := range cases {
		if got := runeString(r); got != string(r) {
			t.Errorf("runeString(%U) = %q, want %q", r, got, string(r))
		}
	}
}

func TestFinite(t *testing.T) {
	cases := []struct {
		in   float32
		want bool
	}{
		{1, true},
		{0.5, true},
		{0, false},
		{-1, false},
		{float32(math.NaN()), false},
		{float32(math.Inf(1)), false},
		{float32(math.Inf(-1)), false},
	}
	for _, c := range cases {
		if got := finite(c.in); got != c.want {
			t.Errorf("finite(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestStripPasteEnd_NoMarker(t *testing.T) {
	in := "hello world\nlinetwo"
	if got := stripPasteEnd(in); got != in {
		t.Errorf("got %q, want unchanged", got)
	}
}

func TestStripPasteEnd_RemovesEmbeddedMarker(t *testing.T) {
	in := "before\x1b[201~middle\x1b[201~after"
	want := "beforemiddleafter"
	if got := stripPasteEnd(in); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripPasteEnd_PartialMarkerLeftAlone(t *testing.T) {
	// "\x1b[20" alone is not a marker.
	in := "x\x1b[20y"
	if got := stripPasteEnd(in); got != in {
		t.Errorf("got %q, want unchanged", got)
	}
}

func TestRealNumber(t *testing.T) {
	cases := []struct {
		in   float32
		want bool
	}{
		{0, true},
		{1, true},
		{-1, true},
		{float32(math.NaN()), false},
		{float32(math.Inf(1)), false},
		{float32(math.Inf(-1)), false},
	}
	for _, c := range cases {
		if got := realNumber(c.in); got != c.want {
			t.Errorf("realNumber(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestLinesFromScroll_NaNInfReturnsZero(t *testing.T) {
	cases := []struct {
		scrollY, cellH float32
	}{
		{float32(math.NaN()), 16},
		{float32(math.Inf(1)), 16},
		{float32(math.Inf(-1)), 16},
		{10, float32(math.NaN())},
		{10, float32(math.Inf(1))},
		{10, 0},  // non-positive cellH rejected by finite()
		{10, -1}, // ditto
	}
	for _, c := range cases {
		if got := linesFromScroll(c.scrollY, c.cellH); got != 0 {
			t.Errorf("linesFromScroll(%v, %v) = %d, want 0",
				c.scrollY, c.cellH, got)
		}
	}
}

func TestLinesFromScroll_SubCellNudge(t *testing.T) {
	// Pixel delta below cellH should still move one line in its
	// direction so trackpad inputs aren't lost.
	if got := linesFromScroll(5, 16); got != 1 {
		t.Errorf("positive sub-cell: got %d, want 1", got)
	}
	if got := linesFromScroll(-5, 16); got != -1 {
		t.Errorf("negative sub-cell: got %d, want -1", got)
	}
	if got := linesFromScroll(0, 16); got != 0 {
		t.Errorf("zero scroll: got %d, want 0", got)
	}
}

func TestLinesFromScroll_FullCellSteps(t *testing.T) {
	if got := linesFromScroll(48, 16); got != 3 {
		t.Errorf("3 cells: got %d, want 3", got)
	}
	if got := linesFromScroll(-48, 16); got != -3 {
		t.Errorf("-3 cells: got %d, want -3", got)
	}
}

func TestTruncatePaste_ShortReturnsUnchanged(t *testing.T) {
	if got := truncatePaste("abc", 10); got != "abc" {
		t.Errorf("got %q, want %q", got, "abc")
	}
}

func TestTruncatePaste_AsciiCutAtMax(t *testing.T) {
	in := "abcdefghij"
	if got := truncatePaste(in, 4); got != "abcd" {
		t.Errorf("got %q, want %q", got, "abcd")
	}
}

func TestTruncatePaste_BacksOffPartialUTF8(t *testing.T) {
	// "é" is 0xC3 0xA9 (2 bytes). Cutting at the second byte mid-rune
	// must back up to the start so no half-rune escapes.
	in := "aé" // 1 + 2 = 3 bytes
	got := truncatePaste(in, 2)
	if got != "a" {
		t.Errorf("got %q, want %q", got, "a")
	}
}

func TestTruncatePaste_MultiByteAtBoundary(t *testing.T) {
	// "☃" is 0xE2 0x98 0x83 (3 bytes). max=4 lands inside the second
	// rune; result should keep the complete first snowman only.
	in := "☃☃" // 6 bytes
	got := truncatePaste(in, 4)
	if got != "☃" {
		t.Errorf("got %q, want %q", got, "☃")
	}
}

func TestTruncatePaste_ZeroOrNegativeMaxIsEmpty(t *testing.T) {
	if got := truncatePaste("abc", 0); got != "" {
		t.Errorf("max=0: got %q, want \"\"", got)
	}
	if got := truncatePaste("abc", -1); got != "" {
		t.Errorf("max=-1: got %q, want \"\"", got)
	}
}

func TestEncodeMouseSGR_Press(t *testing.T) {
	got := string(encodeMouseSGR(nil, 0, 4, 9, true))
	if got != "\x1b[<0;5;10M" {
		t.Errorf("press: %q", got)
	}
}

func TestEncodeMouseSGR_Release(t *testing.T) {
	got := string(encodeMouseSGR(nil, 0, 0, 0, false))
	if got != "\x1b[<0;1;1m" {
		t.Errorf("release: %q", got)
	}
}

func TestEncodeMouseSGR_WheelUp(t *testing.T) {
	got := string(encodeMouseSGR(nil, 64, 10, 20, true))
	if got != "\x1b[<64;11;21M" {
		t.Errorf("wheel up: %q", got)
	}
}

func TestEncodeMouseSGR_DragWithMods(t *testing.T) {
	got := string(encodeMouseSGR(nil, 48, 7, 3, true))
	if got != "\x1b[<48;8;4M" {
		t.Errorf("drag+ctrl: %q", got)
	}
}

func TestMouseSGRBaseButton_KnownButtons(t *testing.T) {
	cases := []struct {
		btn  gui.MouseButton
		want int
		ok   bool
	}{
		{gui.MouseLeft, 0, true},
		{gui.MouseRight, 2, true},
		{gui.MouseMiddle, 1, true},
		{gui.MouseInvalid, 0, false},
	}
	for _, c := range cases {
		got, ok := mouseSGRBaseButton(c.btn)
		if got != c.want || ok != c.ok {
			t.Errorf("btn=%d: got (%d,%v), want (%d,%v)",
				c.btn, got, ok, c.want, c.ok)
		}
	}
}

func TestCursorBlinks_HonorsGridDefault(t *testing.T) {
	g := NewGrid(1, 5)
	tm := &Term{grid: g}
	if !tm.cursorBlinks() {
		t.Error("default cursor should blink")
	}
	g.CursorBlink = false
	if tm.cursorBlinks() {
		t.Error("steady cursor should not blink")
	}
}

func TestCursorBlinks_CfgOverridesGrid(t *testing.T) {
	g := NewGrid(1, 5)
	g.CursorBlink = true
	off := false
	tm := &Term{cfg: Cfg{CursorBlink: &off}, grid: g}
	if tm.cursorBlinks() {
		t.Error("Cfg override (false) should win over grid blink=true")
	}
	on := true
	g.CursorBlink = false
	tm.cfg.CursorBlink = &on
	if !tm.cursorBlinks() {
		t.Error("Cfg override (true) should win over grid blink=false")
	}
}

func TestMouseModBits(t *testing.T) {
	cases := []struct {
		m    gui.Modifier
		want int
	}{
		{0, 0},
		{gui.ModShift, 4},
		{gui.ModAlt, 8},
		{gui.ModCtrl, 16},
		{gui.ModCtrl | gui.ModShift, 20},
		{gui.ModCtrl | gui.ModAlt | gui.ModShift, 28},
		{gui.ModSuper, 0},
	}
	for _, c := range cases {
		if got := mouseModBits(c.m); got != c.want {
			t.Errorf("mod=%d: got %d, want %d", c.m, got, c.want)
		}
	}
}
