package term

import (
	"errors"
	"math"
	"testing"

	glyph "github.com/mike-ward/go-glyph"
	"github.com/mike-ward/go-gui/gui"
)

// scrollbarThumb delegates to scrollbarGeometry so tests share the production formula.
func scrollbarThumb(sbRows, liveRows, viewOffset int, viewH float32) (thumbY, thumbH float32) {
	return scrollbarGeometry(sbRows, liveRows, viewOffset, viewH)
}

func TestScrollbarGeometry_LiveView(t *testing.T) {
	// ViewOffset=0: thumb bottom should align with viewport bottom.
	const sb, rows, h = 100, 24, 480.0
	y, th := scrollbarThumb(sb, rows, 0, h)
	bottom := y + th
	if math.Abs(float64(bottom-h)) > 0.001 {
		t.Errorf("live view: thumb bottom = %.3f, want %.3f", bottom, float32(h))
	}
}

func TestScrollbarGeometry_TopView(t *testing.T) {
	// ViewOffset=len(Scrollback): thumb top should be at 0.
	const sb, rows, h = 100, 24, 480.0
	y, _ := scrollbarThumb(sb, rows, sb, h)
	if math.Abs(float64(y)) > 0.001 {
		t.Errorf("top view: thumbY = %.3f, want 0", y)
	}
}

func TestScrollbarGeometry_MidView(t *testing.T) {
	// ViewOffset=half scrollback: thumb midpoint should be near viewport midpoint.
	const sb, rows, h = 100, 0, 100.0 // rows=0 so total=sb; mid is exact
	mid := sb / 2
	y, th := scrollbarThumb(sb, rows, mid, h)
	thumbMid := y + th/2
	if math.Abs(float64(thumbMid-h/2)) > 1.0 {
		t.Errorf("mid view: thumb midpoint = %.3f, want ~%.3f", thumbMid, float32(h/2))
	}
}

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

func newTestTermCapture() (*Term, *[]byte) {
	buf := make([]byte, 0, 64)
	t := &Term{grid: NewGrid(4, 8), lastMouseR: -1, lastMouseC: -1}
	t.writeHost = func(b []byte) error {
		buf = append(buf, b...)
		return nil
	}
	return t, &buf
}

func TestTerm_OnWindowEvent_NoReportWhenFocusOff(t *testing.T) {
	term, buf := newTestTermCapture()
	// FocusReporting defaults to false
	term.onWindowEvent(&gui.Event{Type: gui.EventFocused})
	term.onWindowEvent(&gui.Event{Type: gui.EventUnfocused})
	if got := string(*buf); got != "" {
		t.Fatalf("focus off: got %q, want empty", got)
	}
}

func TestTerm_OnWindowEvent_NilEventNoPanic(t *testing.T) {
	term := &Term{grid: NewGrid(1, 5), writeHost: func([]byte) error { return nil }}
	term.onWindowEvent(nil) // must not panic
}

func TestTerm_OnKeyDown_AppCursor(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.AppCursorKeys = true
	e := &gui.Event{KeyCode: gui.KeyUp}
	term.onKeyDown(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1bOA" {
		t.Fatalf("app cursor = %q, want %q", got, "\x1bOA")
	}
	if !e.IsHandled {
		t.Fatal("event should be handled")
	}
}

func TestTerm_OnKeyDown_AppKeypad(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.AppKeypad = true
	e := &gui.Event{KeyCode: gui.KeyKP1}
	term.onKeyDown(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1bOq" {
		t.Fatalf("app keypad = %q, want %q", got, "\x1bOq")
	}
}

func TestTerm_OnWindowEvent_FocusReporting(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.FocusReporting = true
	term.onWindowEvent(&gui.Event{Type: gui.EventFocused})
	term.onWindowEvent(&gui.Event{Type: gui.EventUnfocused})
	if got := string(*buf); got != "\x1b[I\x1b[O" {
		t.Fatalf("focus reports = %q, want %q", got, "\x1b[I\x1b[O")
	}
}

func TestTerm_WriteBytes_UsesWriteHost(t *testing.T) {
	term := &Term{}
	term.writeHost = func([]byte) error { return errors.New("boom") }
	term.writeBytes([]byte("x"))
}

func TestCursorBlinks_HonorsGridDefault(t *testing.T) {
	g := NewGrid(1, 5)
	tm := &Term{grid: g}
	if tm.cursorBlinks() {
		t.Error("default cursor should be steady")
	}
	g.CursorBlink = true
	if !tm.cursorBlinks() {
		t.Error("blinking cursor should blink")
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

func TestCellRunKey_PlainCell(t *testing.T) {
	g := NewGrid(4, 8)
	base := gui.TextStyle{Typeface: glyph.TypefaceRegular}
	cell := Cell{Ch: 'A', FG: 7, BG: 0, Width: 1}
	k := cellRunKey(cell, base, g, -1, -1)
	if k.underline || k.strikethrough {
		t.Error("plain cell should have no decoration")
	}
	if k.typeface != glyph.TypefaceRegular {
		t.Errorf("typeface: got %v, want regular", k.typeface)
	}
	if k.linkID != 0 {
		t.Error("no link expected")
	}
}

func TestCellRunKey_BoldItalic(t *testing.T) {
	g := NewGrid(4, 8)
	base := gui.TextStyle{Typeface: glyph.TypefaceRegular}
	cell := Cell{Ch: 'B', Width: 1, Attrs: AttrBold | AttrItalic}
	k := cellRunKey(cell, base, g, -1, -1)
	if k.typeface != glyph.TypefaceBoldItalic {
		t.Errorf("bold+italic: got %v, want BoldItalic", k.typeface)
	}
}

func TestCellRunKey_Underline(t *testing.T) {
	g := NewGrid(4, 8)
	base := gui.TextStyle{}
	cell := Cell{Ch: 'C', Width: 1, Attrs: AttrUnderline}
	k := cellRunKey(cell, base, g, -1, -1)
	if !k.underline {
		t.Error("underline attr: expected underline in key")
	}
}

func TestCellRunKey_Strikethrough(t *testing.T) {
	g := NewGrid(4, 8)
	base := gui.TextStyle{}
	cell := Cell{Ch: 'D', Width: 1, Attrs: AttrStrikethrough}
	k := cellRunKey(cell, base, g, -1, -1)
	if !k.strikethrough {
		t.Error("strikethrough attr: expected strikethrough in key")
	}
}

func TestCellRunKey_LinkForcesUnderline(t *testing.T) {
	g := NewGrid(4, 8)
	base := gui.TextStyle{}
	cell := Cell{Ch: 'E', Width: 1, LinkID: 42}
	k := cellRunKey(cell, base, g, -1, -1)
	if !k.underline {
		t.Error("linked cell: expected underline forced on by linkID")
	}
	if k.linkID != 42 {
		t.Errorf("linkID: got %d, want 42", k.linkID)
	}
}

func TestCellRunKey_DimHalvesColor(t *testing.T) {
	g := NewGrid(4, 8)
	base := gui.TextStyle{}
	cell := Cell{Ch: 'F', Width: 1, Attrs: AttrDim}
	cell.FG = rgbColor(200, 100, 50)
	k := cellRunKey(cell, base, g, -1, -1)
	// Dim halves each channel via integer division.
	want := gui.RGB(100, 50, 25)
	if k.color != want {
		t.Errorf("dim color: got %v, want %v", k.color, want)
	}
}

// BenchmarkForegroundPass exercises the run-key computation and string
// building for a full 80×24 screen of mixed colored text. It does not
// call dc.Text (no GUI context required) — the hot path is the loop
// logic and memory access pattern.
func BenchmarkForegroundPass(b *testing.B) {
	const rows, cols = 24, 80
	g := NewGrid(rows, cols)
	base := gui.TextStyle{Typeface: glyph.TypefaceRegular}

	// Fill with alternating color runs to stress the coalescing path.
	colors := []uint32{rgbColor(200, 200, 200), rgbColor(100, 200, 100), rgbColor(200, 100, 100)}
	p := NewParser(g)
	_ = p
	for r := range rows {
		for c := range cols {
			g.Cells[r*cols+c] = Cell{
				Ch:    rune('A' + c%26),
				FG:    colors[c%len(colors)],
				Width: 1,
			}
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		for r := range rows {
			for c := range cols {
				cell := g.Cells[r*cols+c]
				if cell.Width == 0 && cell.Ch == 0 {
					continue
				}
				_ = cellRunKey(cell, base, g, -1, -1)
			}
		}
	}
}
