package term

import (
	"errors"
	"math"
	"testing"
	"unicode/utf8"

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
	if k.ulStyle != ULNone || k.strikethrough {
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
	cell := Cell{Ch: 'C', Width: 1, Attrs: AttrUnderline, ULStyle: ULSingle, ULColor: DefaultColor}
	k := cellRunKey(cell, base, g, -1, -1)
	if k.ulStyle != ULSingle {
		t.Errorf("underline attr: expected ULSingle in key, got %d", k.ulStyle)
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
	if k.ulStyle == ULNone {
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

func TestTerm_OnKeyDown_AltLetter(t *testing.T) {
	cases := []struct {
		key  gui.KeyCode
		want string
	}{
		{gui.KeyF, "\x1bf"},
		{gui.KeyB, "\x1bb"},
		{gui.KeyA, "\x1ba"},
		{gui.KeyZ, "\x1bz"},
	}
	for _, c := range cases {
		term, buf := newTestTermCapture()
		e := &gui.Event{KeyCode: c.key, Modifiers: gui.ModAlt}
		term.onKeyDown(nil, e, &gui.Window{})
		if got := string(*buf); got != c.want {
			t.Errorf("Alt+%v = %q, want %q", c.key, got, c.want)
		}
		if !e.IsHandled {
			t.Errorf("Alt+%v: event should be handled", c.key)
		}
	}
}

func TestTerm_OnKeyDown_AltArrow(t *testing.T) {
	cases := []struct {
		key  gui.KeyCode
		want string
	}{
		{gui.KeyUp, "\x1b\x1b[A"},
		{gui.KeyDown, "\x1b\x1b[B"},
		{gui.KeyRight, "\x1b\x1b[C"},
		{gui.KeyLeft, "\x1b\x1b[D"},
	}
	for _, c := range cases {
		term, buf := newTestTermCapture()
		e := &gui.Event{KeyCode: c.key, Modifiers: gui.ModAlt}
		term.onKeyDown(nil, e, &gui.Window{})
		if got := string(*buf); got != c.want {
			t.Errorf("Alt+%v = %q, want %q", c.key, got, c.want)
		}
	}
}

func TestTerm_OnKeyDown_AltCtrlLetter(t *testing.T) {
	term, buf := newTestTermCapture()
	// Alt+Ctrl+B → ESC + 0x02
	e := &gui.Event{KeyCode: gui.KeyB, Modifiers: gui.ModAlt | gui.ModCtrl}
	term.onKeyDown(nil, e, &gui.Window{})
	want := "\x1b\x02"
	if got := string(*buf); got != want {
		t.Fatalf("Alt+Ctrl+B = %q, want %q", got, want)
	}
}

func TestModParam(t *testing.T) {
	cases := []struct {
		shift, alt, ctrl bool
		want             int
	}{
		{false, false, false, 0},
		{true, false, false, 2},
		{false, true, false, 3},
		{true, true, false, 4},
		{false, false, true, 5},
		{true, false, true, 6},
		{false, true, true, 7},
		{true, true, true, 8},
	}
	for _, c := range cases {
		if got := modParam(c.shift, c.alt, c.ctrl); got != c.want {
			t.Errorf("modParam(%v,%v,%v)=%d want %d", c.shift, c.alt, c.ctrl, got, c.want)
		}
	}
}

func TestFuncKeySeq_NoModifier(t *testing.T) {
	cases := []struct {
		key  gui.KeyCode
		want string
	}{
		{gui.KeyInsert, "\x1b[2~"},
		{gui.KeyF1, "\x1bOP"},
		{gui.KeyF2, "\x1bOQ"},
		{gui.KeyF3, "\x1bOR"},
		{gui.KeyF4, "\x1bOS"},
		{gui.KeyF5, "\x1b[15~"},
		{gui.KeyF6, "\x1b[17~"},
		{gui.KeyF7, "\x1b[18~"},
		{gui.KeyF8, "\x1b[19~"},
		{gui.KeyF9, "\x1b[20~"},
		{gui.KeyF10, "\x1b[21~"},
		{gui.KeyF11, "\x1b[23~"},
		{gui.KeyF12, "\x1b[24~"},
	}
	for _, c := range cases {
		got := string(funcKeySeq(c.key, false, false))
		if got != c.want {
			t.Errorf("funcKeySeq(%v)=%q want %q", c.key, got, c.want)
		}
	}
}

func TestFuncKeySeq_ShiftModifier(t *testing.T) {
	// Shift+F1 → \x1b[1;2P, Shift+F5 → \x1b[15;2~
	if got := string(funcKeySeq(gui.KeyF1, true, false)); got != "\x1b[1;2P" {
		t.Errorf("Shift+F1=%q want %q", got, "\x1b[1;2P")
	}
	if got := string(funcKeySeq(gui.KeyF5, true, false)); got != "\x1b[15;2~" {
		t.Errorf("Shift+F5=%q want %q", got, "\x1b[15;2~")
	}
}

func TestFuncKeySeq_CtrlModifier(t *testing.T) {
	// Ctrl+F1 → \x1b[1;5P, Ctrl+F10 → \x1b[21;5~
	if got := string(funcKeySeq(gui.KeyF1, false, true)); got != "\x1b[1;5P" {
		t.Errorf("Ctrl+F1=%q want %q", got, "\x1b[1;5P")
	}
	if got := string(funcKeySeq(gui.KeyF10, false, true)); got != "\x1b[21;5~" {
		t.Errorf("Ctrl+F10=%q want %q", got, "\x1b[21;5~")
	}
}

func TestTerm_OnKeyDown_FuncKeys(t *testing.T) {
	cases := []struct {
		key  gui.KeyCode
		mods gui.Modifier
		want string
	}{
		{gui.KeyF1, 0, "\x1bOP"},
		{gui.KeyF4, 0, "\x1bOS"},
		{gui.KeyF5, 0, "\x1b[15~"},
		{gui.KeyF12, 0, "\x1b[24~"},
		{gui.KeyInsert, 0, "\x1b[2~"},
		{gui.KeyF1, gui.ModShift, "\x1b[1;2P"},
		{gui.KeyF5, gui.ModCtrl, "\x1b[15;5~"},
		{gui.KeyF1, gui.ModAlt, "\x1b\x1bOP"}, // alt as ESC prefix
	}
	for _, c := range cases {
		term, buf := newTestTermCapture()
		e := &gui.Event{KeyCode: c.key, Modifiers: c.mods}
		term.onKeyDown(nil, e, &gui.Window{})
		if got := string(*buf); got != c.want {
			t.Errorf("key=%v mods=%v: got %q want %q", c.key, c.mods, got, c.want)
		}
		if !e.IsHandled {
			t.Errorf("key=%v mods=%v: event not handled", c.key, c.mods)
		}
	}
}

func TestScrollbarGeometry_ZeroTotal_NoPanic(t *testing.T) {
	// sbLen=0, rows=0 → total=0: must not divide by zero.
	y, h := scrollbarGeometry(0, 0, 0, 100)
	if y != 0 || h != 0 {
		t.Errorf("zero total: got y=%v h=%v, want (0,0)", y, h)
	}
}

func TestTerm_PosToCell_NaNInfCollapseToZero(t *testing.T) {
	term := &Term{
		grid:  NewGrid(24, 80),
		cellW: 8,
		cellH: 16,
	}
	nan := float32(math.NaN())
	inf := float32(math.Inf(1))
	ninf := float32(math.Inf(-1))
	cases := []struct{ x, y float32 }{
		{nan, 16}, {inf, 16}, {ninf, 16},
		{8, nan}, {8, inf}, {8, ninf},
		{nan, nan},
	}
	for _, c := range cases {
		r, col := term.posToCell(c.x, c.y)
		if r < 0 || r >= term.grid.Rows || col < 0 || col >= term.grid.Cols {
			t.Errorf("posToCell(%v,%v)=(%d,%d): outside grid [0,%d)x[0,%d)",
				c.x, c.y, r, col, term.grid.Rows, term.grid.Cols)
		}
	}
}

func TestTerm_OnChar_SearchMode_AppendAndCap(t *testing.T) {
	term, _ := newTestTermCapture()
	term.win = &gui.Window{}
	term.searchActive = true

	e := &gui.Event{CharCode: 'a'}
	term.onChar(nil, e, nil)
	if term.searchQuery != "a" {
		t.Fatalf("query = %q, want \"a\"", term.searchQuery)
	}
	if !e.IsHandled {
		t.Error("event must be handled in search mode")
	}

	// Fill to exactly MaxGridDim runes (already have 1 'a').
	for i := 1; i < MaxGridDim; i++ {
		term.onChar(nil, &gui.Event{CharCode: 'x'}, nil)
	}
	if utf8.RuneCountInString(term.searchQuery) != MaxGridDim {
		t.Fatalf("query rune count = %d, want %d", utf8.RuneCountInString(term.searchQuery), MaxGridDim)
	}
	// Next char must be rejected (at cap).
	before := term.searchQuery
	term.onChar(nil, &gui.Event{CharCode: 'z'}, nil)
	if term.searchQuery != before {
		t.Errorf("query grew past MaxGridDim cap: len now %d", utf8.RuneCountInString(term.searchQuery))
	}
}

func TestTerm_SearchJump_ForwardFindsMatch(t *testing.T) {
	term, _ := newTestTermCapture()
	term.win = &gui.Window{}
	putRow(term.grid, "hello")
	term.searchQuery = "hello"
	term.searchJump(true, &gui.Window{})
	term.grid.Mu.Lock()
	off := term.grid.ViewOffset
	term.grid.Mu.Unlock()
	if off != 0 {
		t.Errorf("ViewOffset = %d after live match, want 0", off)
	}
}

func TestTerm_SearchJump_NoMatchDoesNotPanic(t *testing.T) {
	term, _ := newTestTermCapture()
	term.win = &gui.Window{}
	term.searchQuery = "xyzzy_not_present"
	term.searchJump(true, &gui.Window{}) // must not panic
}

func TestTerm_SearchJump_EmptyQuery_Nop(t *testing.T) {
	term, _ := newTestTermCapture()
	term.win = &gui.Window{}
	term.searchQuery = ""
	term.searchJump(true, &gui.Window{}) // early return, must not panic
}

func TestTerm_OnKeyDown_ModifiedCursorKeys(t *testing.T) {
	cases := []struct {
		key  gui.KeyCode
		mods gui.Modifier
		want string
	}{
		{gui.KeyUp, gui.ModShift, "\x1b[1;2A"},
		{gui.KeyDown, gui.ModCtrl, "\x1b[1;5B"},
		{gui.KeyRight, gui.ModShift | gui.ModCtrl, "\x1b[1;6C"},
		{gui.KeyLeft, gui.ModShift, "\x1b[1;2D"},
		// No modifier → normal sequences.
		{gui.KeyUp, 0, "\x1b[A"},
		{gui.KeyDown, 0, "\x1b[B"},
	}
	for _, c := range cases {
		term, buf := newTestTermCapture()
		e := &gui.Event{KeyCode: c.key, Modifiers: c.mods}
		term.onKeyDown(nil, e, &gui.Window{})
		if got := string(*buf); got != c.want {
			t.Errorf("key=%v mods=%v: got %q want %q", c.key, c.mods, got, c.want)
		}
	}
}

// --- Kitty Keyboard Protocol (Phase 27) ---

func TestKittyKeySeq_Disabled(t *testing.T) {
	// flags==0 means legacy mode; must return nil for all inputs.
	if got := kittyKeySeq(13, 0, 0, false); got != nil {
		t.Fatalf("flags=0: got %q, want nil", got)
	}
}

func TestKittyKeySeq_NoMods(t *testing.T) {
	cases := []struct {
		cp   int
		want string
	}{
		{13, "\x1b[13u"},   // Enter
		{9, "\x1b[9u"},     // Tab
		{27, "\x1b[27u"},   // Escape
		{127, "\x1b[127u"}, // Backspace
	}
	for _, c := range cases {
		got := kittyKeySeq(c.cp, 0, 1, false)
		if string(got) != c.want {
			t.Errorf("cp=%d: got %q, want %q", c.cp, got, c.want)
		}
	}
}

func TestKittyKeySeq_WithMods(t *testing.T) {
	cases := []struct {
		cp   int
		mods gui.Modifier
		want string
	}{
		{13, gui.ModCtrl, "\x1b[13;5u"},                  // Ctrl+Enter → mod=5
		{127, gui.ModShift | gui.ModCtrl, "\x1b[127;6u"}, // Shift+Ctrl+Backspace → mod=6
		{99, gui.ModCtrl, "\x1b[99;5u"},                  // Ctrl+C
		{97, gui.ModAlt, "\x1b[97;3u"},                   // Alt+A → mod=3
		{65, gui.ModSuper, "\x1b[65;9u"},                 // Super+A → mod=9
	}
	for _, c := range cases {
		got := kittyKeySeq(c.cp, c.mods, 1, false)
		if string(got) != c.want {
			t.Errorf("cp=%d mods=%v: got %q, want %q", c.cp, c.mods, got, c.want)
		}
	}
}

func TestTerm_KittyKey_Backspace(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 1
	e := &gui.Event{KeyCode: gui.KeyBackspace}
	term.onKeyDown(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1b[127u" {
		t.Fatalf("KKP backspace: got %q, want %q", got, "\x1b[127u")
	}
}

func TestTerm_KittyKey_Enter(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 1
	e := &gui.Event{KeyCode: gui.KeyEnter}
	term.onKeyDown(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1b[13u" {
		t.Fatalf("KKP enter: got %q, want %q", got, "\x1b[13u")
	}
}

func TestTerm_KittyKey_Tab(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 1
	e := &gui.Event{KeyCode: gui.KeyTab}
	term.onKeyDown(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1b[9u" {
		t.Fatalf("KKP tab: got %q, want %q", got, "\x1b[9u")
	}
}

func TestTerm_KittyKey_Escape(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 1
	e := &gui.Event{KeyCode: gui.KeyEscape}
	term.onKeyDown(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1b[27u" {
		t.Fatalf("KKP escape: got %q, want %q", got, "\x1b[27u")
	}
}

func TestTerm_KittyKey_CtrlC(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 1
	// Ctrl+C: KeyCode=KeyC, Modifiers=ModCtrl. Codepoint for 'c' is 99.
	e := &gui.Event{KeyCode: gui.KeyC, Modifiers: gui.ModCtrl}
	term.onKeyDown(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1b[99;5u" {
		t.Fatalf("KKP Ctrl+C: got %q, want %q", got, "\x1b[99;5u")
	}
}

func TestKittyKeySeq_Release(t *testing.T) {
	// Test key release sequence generation (event-type 3).
	// Modifier field is mandatory even when mod==1 (no modifiers).
	cases := []struct {
		cp   int
		mods gui.Modifier
		want string
	}{
		{13, 0, "\x1b[13;1:3u"},             // Enter release, no mods
		{9, gui.ModShift, "\x1b[9;2:3u"},    // Shift+Tab release
		{27, gui.ModCtrl, "\x1b[27;5:3u"},   // Ctrl+Escape release
		{65, gui.ModAlt, "\x1b[65;3:3u"},    // Alt+A release
	}
	for _, c := range cases {
		got := kittyKeySeq(c.cp, c.mods, 1, true)
		if string(got) != c.want {
			t.Errorf("release cp=%d mods=%v: got %q, want %q", c.cp, c.mods, got, c.want)
		}
	}
}

func TestTerm_KittyKey_Release(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 2 // Enable event type reporting (flag bit 2)

	// Test Enter key release
	e := &gui.Event{KeyCode: gui.KeyEnter}
	term.onKeyUp(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1b[13;1:3u" {
		t.Fatalf("KKP Enter release: got %q, want %q", got, "\x1b[13;1:3u")
	}

	// Clear buffer for next test
	*buf = (*buf)[:0]

	// Test Shift+Tab release
	e = &gui.Event{KeyCode: gui.KeyTab, Modifiers: gui.ModShift}
	term.onKeyUp(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1b[9;2:3u" {
		t.Fatalf("KKP Shift+Tab release: got %q, want %q", got, "\x1b[9;2:3u")
	}
}

func TestTerm_KittyKey_ModifierOnly(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 2 // Enable event type reporting (flag bit 2)

	// Test Shift key release
	e := &gui.Event{KeyCode: gui.KeyLeftShift}
	term.onKeyUp(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1b[57441;1:3u" {
		t.Fatalf("KKP Shift release: got %q, want %q", got, "\x1b[57441;1:3u")
	}

	// Clear buffer for next test
	*buf = (*buf)[:0]

	// Test Ctrl key release
	e = &gui.Event{KeyCode: gui.KeyLeftControl}
	term.onKeyUp(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1b[57442;1:3u" {
		t.Fatalf("KKP Ctrl release: got %q, want %q", got, "\x1b[57442;1:3u")
	}

	// Clear buffer for next test
	*buf = (*buf)[:0]

	// Test Alt key release
	e = &gui.Event{KeyCode: gui.KeyLeftAlt}
	term.onKeyUp(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1b[57443;1:3u" {
		t.Fatalf("KKP Alt release: got %q, want %q", got, "\x1b[57443;1:3u")
	}
}

func TestTerm_KittyKey_ReleaseDisabled(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 1 // Event type reporting disabled (flag bit 2 not set)

	// Test that no release events are generated when flag bit 2 is not set
	e := &gui.Event{KeyCode: gui.KeyEnter}
	term.onKeyUp(nil, e, &gui.Window{})
	if len(*buf) != 0 {
		t.Fatalf("KKP release with flag bit 2 disabled: got %q, want empty", string(*buf))
	}
}

func TestKittyKeySeq_ZeroCodepointReturnsNil(t *testing.T) {
	if got := kittyKeySeq(0, 0, 1, false); got != nil {
		t.Fatalf("codepoint=0: got %q, want nil", got)
	}
}

func TestKittyKeySeq_NegativeCodepointReturnsNil(t *testing.T) {
	if got := kittyKeySeq(-1, 0, 1, false); got != nil {
		t.Fatalf("codepoint=-1: got %q, want nil", got)
	}
	if got := kittyKeySeq(-1, 0, 1, true); got != nil {
		t.Fatalf("codepoint=-1 release: got %q, want nil", got)
	}
}

func TestTerm_KittyKey_RightModifiers(t *testing.T) {
	cases := []struct {
		key  gui.KeyCode
		want string
	}{
		{gui.KeyRightShift, "\x1b[57447;1:3u"},
		{gui.KeyRightControl, "\x1b[57448;1:3u"},
		{gui.KeyRightAlt, "\x1b[57449;1:3u"},
		{gui.KeyLeftSuper, "\x1b[57444;1:3u"},
		{gui.KeyRightSuper, "\x1b[57450;1:3u"},
	}
	for _, c := range cases {
		term, buf := newTestTermCapture()
		term.grid.KittyKeyFlags = 2
		term.onKeyUp(nil, &gui.Event{KeyCode: c.key}, &gui.Window{})
		if got := string(*buf); got != c.want {
			t.Errorf("key=%v: got %q, want %q", c.key, got, c.want)
		}
	}
}

func TestTerm_KittyKey_NavRelease(t *testing.T) {
	cases := []struct {
		key  gui.KeyCode
		want string
	}{
		{gui.KeyInsert, "\x1b[57348;1:3u"},
		{gui.KeyDelete, "\x1b[57349;1:3u"},
		{gui.KeyLeft, "\x1b[57350;1:3u"},
		{gui.KeyRight, "\x1b[57351;1:3u"},
		{gui.KeyUp, "\x1b[57352;1:3u"},
		{gui.KeyDown, "\x1b[57353;1:3u"},
		{gui.KeyPageUp, "\x1b[57354;1:3u"},
		{gui.KeyPageDown, "\x1b[57355;1:3u"},
		{gui.KeyHome, "\x1b[57356;1:3u"},
		{gui.KeyEnd, "\x1b[57357;1:3u"},
	}
	for _, c := range cases {
		term, buf := newTestTermCapture()
		term.grid.KittyKeyFlags = 2
		term.onKeyUp(nil, &gui.Event{KeyCode: c.key}, &gui.Window{})
		if got := string(*buf); got != c.want {
			t.Errorf("key=%v: got %q, want %q", c.key, got, c.want)
		}
	}
}

func TestTerm_KittyKey_FKeyRelease(t *testing.T) {
	cases := []struct {
		key  gui.KeyCode
		want string
	}{
		{gui.KeyF1, "\x1b[57364;1:3u"},
		{gui.KeyF2, "\x1b[57365;1:3u"},
		{gui.KeyF3, "\x1b[57366;1:3u"},
		{gui.KeyF4, "\x1b[57367;1:3u"},
		{gui.KeyF5, "\x1b[57368;1:3u"},
		{gui.KeyF6, "\x1b[57369;1:3u"},
		{gui.KeyF7, "\x1b[57370;1:3u"},
		{gui.KeyF8, "\x1b[57371;1:3u"},
		{gui.KeyF9, "\x1b[57372;1:3u"},
		{gui.KeyF10, "\x1b[57373;1:3u"},
		{gui.KeyF11, "\x1b[57374;1:3u"},
		{gui.KeyF12, "\x1b[57375;1:3u"},
	}
	for _, c := range cases {
		term, buf := newTestTermCapture()
		term.grid.KittyKeyFlags = 2
		term.onKeyUp(nil, &gui.Event{KeyCode: c.key}, &gui.Window{})
		if got := string(*buf); got != c.want {
			t.Errorf("key=%v: got %q, want %q", c.key, got, c.want)
		}
	}
}

func TestTerm_KittyKey_PrintableRelease(t *testing.T) {
	cases := []struct {
		key  gui.KeyCode
		want string
	}{
		{gui.KeyA, "\x1b[97;1:3u"},  // 'a'
		{gui.KeyZ, "\x1b[122;1:3u"}, // 'z'
		{gui.Key0, "\x1b[48;1:3u"},  // '0'
		{gui.Key9, "\x1b[57;1:3u"},  // '9'
	}
	for _, c := range cases {
		term, buf := newTestTermCapture()
		term.grid.KittyKeyFlags = 2
		term.onKeyUp(nil, &gui.Event{KeyCode: c.key}, &gui.Window{})
		if got := string(*buf); got != c.want {
			t.Errorf("key=%v: got %q, want %q", c.key, got, c.want)
		}
	}
}

func TestTerm_KittyKey_KPEnterRelease(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 2
	term.onKeyUp(nil, &gui.Event{KeyCode: gui.KeyKPEnter}, &gui.Window{})
	if got := string(*buf); got != "\x1b[13;1:3u" {
		t.Fatalf("KPEnter release: got %q, want %q", got, "\x1b[13;1:3u")
	}
}

func TestTerm_KittyKey_UnknownKeyNoOutput(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 2
	// KeyF13 is not in the switch; should produce no output.
	term.onKeyUp(nil, &gui.Event{KeyCode: gui.KeyF13}, &gui.Window{})
	if len(*buf) != 0 {
		t.Fatalf("unknown key: got %q, want empty", string(*buf))
	}
}

func TestTerm_KittyKey_LegacyFallback(t *testing.T) {
	// When KKP is disabled (flags=0), legacy sequences still emitted.
	cases := []struct {
		key  gui.KeyCode
		want string
	}{
		{gui.KeyBackspace, "\x7f"},
		{gui.KeyEnter, "\r"},
		{gui.KeyTab, "\t"},
		{gui.KeyEscape, "\x1b"},
	}
	for _, c := range cases {
		term, buf := newTestTermCapture()
		// flags=0 by default
		e := &gui.Event{KeyCode: c.key}
		term.onKeyDown(nil, e, &gui.Window{})
		if got := string(*buf); got != c.want {
			t.Errorf("legacy key=%v: got %q, want %q", c.key, got, c.want)
		}
	}
}

func TestParser_MousePixelMode_Toggle(t *testing.T) {
	g := NewGrid(5, 10)
	p := NewParser(g)
	p.Feed([]byte("\x1b[?1016h"))
	if !g.MouseSGRPixels {
		t.Error("?1016h should set MouseSGRPixels")
	}
	p.Feed([]byte("\x1b[?1016l"))
	if g.MouseSGRPixels {
		t.Error("?1016l should clear MouseSGRPixels")
	}
}

func TestWriteMouse_CellVsPixelCoords(t *testing.T) {
	cases := []struct {
		name   string
		col    int
		row    int
		pixX   float32
		pixY   float32
		pixels bool
		press  bool
		want   string
	}{
		// Cell mode: col+1 / row+1
		{"cell press", 4, 9, 50.0, 90.0, false, true, "\x1b[<0;5;10M"},
		{"cell release", 0, 0, 0, 0, false, false, "\x1b[<0;1;1m"},
		// Pixel mode: int(pixX)+1 / int(pixY)+1
		{"pixel press", 4, 9, 50.7, 90.3, true, true, "\x1b[<0;51;91M"},
		{"pixel release", 0, 0, 9.9, 19.1, true, false, "\x1b[<0;10;20m"},
		// Pixel mode at origin maps to (1,1) per 1-based spec
		{"pixel origin", 3, 3, 0, 0, true, true, "\x1b[<0;1;1M"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tm, buf := newTestTermCapture()
			tm.writeMouse(0, c.col, c.row, c.pixX, c.pixY, c.pixels, c.press)
			if got := string(*buf); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
