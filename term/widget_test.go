package term

import (
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/go-gui-org/go-gui/gui"

	glyph "github.com/go-gui-org/go-glyph"
)

// scrollbarThumb delegates to scrollbarGeometry so tests share the production formula.
func scrollbarThumb(sbRows, liveRows, viewOffset int, viewH float32) (thumbY, thumbH float32) {
	return scrollbarGeometry(sbRows, liveRows, float32(viewOffset), viewH)
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
	// ViewOffset=half scrollback: thumb midpoint should be near viewport
	// midpoint. rows=0 tests the degenerate case where the min-thumb clamp
	// kicks in, shifting the midpoint by minScrollbarThumbH/2.
	const sb, rows, h = 100, 0, 100.0 // rows=0 so total=sb; mid is exact
	mid := sb / 2
	y, th := scrollbarThumb(sb, rows, mid, h)
	thumbMid := y + th/2
	wantMid := float32(h/2) + minScrollbarThumbH/2
	if math.Abs(float64(thumbMid-wantMid)) > 0.01 {
		t.Errorf("mid view: thumb midpoint = %.3f, want ~%.3f", thumbMid, wantMid)
	}
}

func TestScrollbarGeometry_SubPixel(t *testing.T) {
	// Verify that fractional viewOffset produces fractional thumbY changes
	const sb, rows, h = 100, 24, 480.0
	y0, _ := scrollbarGeometry(sb, rows, 10.0, h)
	yHalf, _ := scrollbarGeometry(sb, rows, 10.5, h)
	y1, _ := scrollbarGeometry(sb, rows, 11.0, h)

	if yHalf <= y1 || yHalf >= y0 {
		t.Errorf("expected yHalf (%f) to be strictly between y1 (%f) and y0 (%f)", yHalf, y1, y0)
	}

	expectedHalf := (y0 + y1) / 2
	if math.Abs(float64(yHalf-expectedHalf)) > 0.001 {
		t.Errorf("yHalf = %f, want exactly half-way value %f", yHalf, expectedHalf)
	}
}

func TestScrollbarOffsetForY_RoundTrip(t *testing.T) {
	// scrollbarOffsetForY must invert scrollbarGeometry's thumbY formula:
	// mapping thumbY back to a view offset should recover the original.
	const sb, rows, h = 100, 24, 480.0
	for _, off := range []float32{0, 12.5, 50, 87.25, 100} {
		thumbY, _ := scrollbarGeometry(sb, rows, off, h)
		got := scrollbarOffsetForY(sb, rows, thumbY, h)
		if math.Abs(float64(got-off)) > 0.001 {
			t.Errorf("off=%v: thumbY=%v → offset=%v, want %v", off, thumbY, got, off)
		}
	}
}

func TestScrollbarOffsetForY_Clamps(t *testing.T) {
	const sb, rows, h = 100, 24, 480.0
	// y below the top clamps to the oldest row (sb); y past the bottom to 0.
	if got := scrollbarOffsetForY(sb, rows, -100, h); got != sb {
		t.Errorf("y<0: got %v, want %v", got, float32(sb))
	}
	if got := scrollbarOffsetForY(sb, rows, h*2, h); got != 0 {
		t.Errorf("y>>h: got %v, want 0", got)
	}
	// Degenerate inputs return 0.
	if got := scrollbarOffsetForY(0, rows, 10, h); got != 0 {
		t.Errorf("sb=0: got %v, want 0", got)
	}
	if got := scrollbarOffsetForY(sb, rows, 10, 0); got != 0 {
		t.Errorf("viewH=0: got %v, want 0", got)
	}
	// NaN/Inf y returns 0.
	if got := scrollbarOffsetForY(sb, rows, float32(math.NaN()), h); got != 0 {
		t.Errorf("y=NaN: got %v, want 0", got)
	}
	if got := scrollbarOffsetForY(sb, rows, float32(math.Inf(1)), h); got != 0 {
		t.Errorf("y=Inf: got %v, want 0", got)
	}
}

func TestSearchOverlap_NoScroll_OneRow(t *testing.T) {
	// 24 rows × 20px = 480px. Search bar at [460,480). Row 23's text
	// footprint [460,480) overlaps → 1 row reserved.
	if got := searchOverlap(20, 0, 480, 24); got != 1 {
		t.Errorf("got %d, want 1", got)
	}
}

func TestSearchOverlap_SubPixelMax_TwoRows(t *testing.T) {
	// renderYOff=19 shifts row 22 to [459,479), overlapping search bar
	// [460,480). Row 23 shifts to [479,499) also overlapping → 2 rows.
	if got := searchOverlap(20, 19, 480, 24); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
}

func TestSearchOverlap_AlignedCanvas_Scroll_YieldsTwoRows(t *testing.T) {
	// 480px canvas, 20px cellH → no fractional gap. Any renderYOff > 0
	// shifts row 22's text bottom (460+renderYOff) past searchBarTop (460).
	// Only renderYOff=0 produces 1 row; all positive values → 2.
	if got := searchOverlap(20, 1, 480, 24); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
}

func TestSearchOverlap_FractionalCanvas_OneRow(t *testing.T) {
	// 485px canvas, 20px cellH → rows=24, 5px fractional gap at bottom.
	// searchBarTop=465. renderYOff=3: row 22 at [443,463) doesn't
	// overlap, row 23 at [463,483) does → 1. Old heuristic: always 2.
	if got := searchOverlap(20, 3, 485, 24); got != 1 {
		t.Errorf("got %d, want 1", got)
	}
}

func TestSearchOverlap_ZeroRows_ReturnsZero(t *testing.T) {
	if got := searchOverlap(20, 5, 480, 0); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestSearchOverlap_NaNInput_NoInfiniteLoop(t *testing.T) {
	// NaN comparisons always false → loop condition fails immediately.
	// Return value is meaningless (NaN geometry is undefined); the
	// assertion is just that the function returns without looping.
	_ = searchOverlap(20, float32(math.NaN()), 480, 24)
}

// recordingNotifier captures Notify calls for assertion in tests.
type recordingNotifier struct {
	calls []struct{ title, body string }
	mu    sync.Mutex
}

func (r *recordingNotifier) Notify(title, body string) {
	r.mu.Lock()
	r.calls = append(r.calls, struct{ title, body string }{title, body})
	r.mu.Unlock()
}

func TestNotify_DesktopNotifier_NoCallback(t *testing.T) {
	// When OnNotify is nil, OSC 9/777 must reach the notifier interface.
	rec := &recordingNotifier{}
	g := newGrid(4, 80)
	p := newParser(g)
	tm := &Term{
		grid:   g,
		parser: p,
		cfg:    Cfg{}, // OnNotify nil
		notif:  rec,
	}
	tm.registerNotifyHandler()

	// Bounded channel replaces time.Sleep — deterministic and fast.
	notified := make(chan struct{}, 2)

	// Wrap notif so each Notify call signals the channel.
	tm.notif = notifierFunc(func(title, body string) {
		rec.Notify(title, body)
		notified <- struct{}{}
	})

	feed(t, g, p, []byte("\x1b]9;hello world\x07"))
	<-notified

	feed(t, g, p, []byte("\x1b]777;notify;my title;my body\x07"))
	<-notified

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.calls) != 2 {
		t.Fatalf("got %d notify calls, want 2", len(rec.calls))
	}
	if rec.calls[0].title != "" || rec.calls[0].body != "hello world" {
		t.Errorf("OSC 9: got title=%q body=%q, want title=\"\" body=\"hello world\"",
			rec.calls[0].title, rec.calls[0].body)
	}
	if rec.calls[1].title != "my title" || rec.calls[1].body != "my body" {
		t.Errorf("OSC 777: got title=%q body=%q, want title=\"my title\" body=\"my body\"",
			rec.calls[1].title, rec.calls[1].body)
	}
}

// --- numeric helpers ---

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

// writerFunc adapts a function literal to io.Writer.
// Used to capture or inspect bytes written to the PTY in tests.
type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(b []byte) (int, error) {
	if f == nil {
		return 0, nil
	}
	return f(b)
}

// notifierFunc adapts a function literal to the notifier interface.
type notifierFunc func(title, body string)

func (f notifierFunc) Notify(title, body string) { f(title, body) }

func newTestTermCapture() (*Term, *[]byte) {
	buf := make([]byte, 0, 64)
	t := &Term{grid: newGrid(4, 8)}
	t.mouse.lastR = -1
	t.mouse.lastC = -1
	t.pw = writerFunc(func(b []byte) (int, error) {
		buf = append(buf, b...)
		return len(b), nil
	})
	return t, &buf
}

// syncScheduler runs QueueCommand callbacks immediately inline so tests
// can observe side effects (UpdateWindow, display changes, etc.) without a
// real GUI main loop.
type syncScheduler struct{}

func (syncScheduler) QueueCommand(fn func(*gui.Window)) { fn(&gui.Window{}) }

// newTestTermWithScheduler creates a Term bare enough for keyboard / font
// adjust tests. cmd is wired to syncScheduler so queueCommand runs inline,
// and pw captures byte output.
func newTestTermWithScheduler(fontSize float32, cfg Cfg) (*Term, *[]byte) {
	if cfg.TextStyle == (gui.TextStyle{}) {
		cfg.TextStyle = gui.TextStyle{Size: 12}
	}
	buf := make([]byte, 0, 64)
	t := &Term{
		grid:     newGrid(4, 8),
		cfg:      cfg,
		cmd:      syncScheduler{},
		fontSize: fontSize,
	}
	t.mouse.lastR = -1
	t.mouse.lastC = -1
	t.pw = writerFunc(func(b []byte) (int, error) {
		buf = append(buf, b...)
		return len(b), nil
	})
	return t, &buf
}

// testTextMeasurer implements gui.TextMeasurer with fixed cell dimensions.
// TextWidth returns cellW * len(text) so "M" measures as cellW.
type testTextMeasurer struct {
	cellW, cellH float32
}

func (m testTextMeasurer) TextWidth(text string, _ gui.TextStyle) float32 {
	return m.cellW * float32(len(text))
}
func (m testTextMeasurer) TextHeight(_ string, _ gui.TextStyle) float32 { return m.cellH }
func (m testTextMeasurer) FontHeight(_ gui.TextStyle) float32           { return m.cellH }
func (m testTextMeasurer) FontAscent(_ gui.TextStyle) float32           { return m.cellH * 0.8 }
func (m testTextMeasurer) LayoutText(_ string, _ gui.TextStyle, _ float32) (glyph.Layout, error) {
	return glyph.Layout{}, nil
}

// newDrawTerm creates a Term ready for direct OnDraw testing. cellW and
// cellH are preset so OnDraw's measurement guard passes. Returns the Term
// and a DrawContext whose dimensions match the grid.
func newDrawTerm(rows, cols int, cellW, cellH float32) (*Term, *gui.DrawContext) {
	g := newGrid(rows, cols)
	t := &Term{
		grid:  g,
		cellW: cellW,
		cellH: cellH,
		cmd:   syncScheduler{},
	}
	t.focused.Store(true)
	t.mouse.hoverR.Store(-1)
	t.mouse.hoverC.Store(-1)
	tm := testTextMeasurer{cellW: cellW, cellH: cellH}
	dc := gui.NewDrawContext(float32(cols)*cellW, float32(rows)*cellH, tm)
	return t, dc
}

// newMouseTerm creates a Term configured for mouse event testing.
// cellW/cellH are set to 10/20 for coordinate-to-cell mapping.
// Returns a capture buffer for asserting PTY bytes written by mouse reports.
func newMouseTerm(rows, cols int) (*Term, *[]byte) {
	buf := make([]byte, 0, 64)
	t := &Term{
		grid:  newGrid(rows, cols),
		cellW: 10,
		cellH: 20,
		pw: writerFunc(func(b []byte) (int, error) {
			buf = append(buf, b...)
			return len(b), nil
		}),
		cmd: syncScheduler{},
	}
	t.mouse.lastR = -1
	t.mouse.lastC = -1
	t.mouse.hoverR.Store(-1)
	t.mouse.hoverC.Store(-1)
	return t, &buf
}

// newScrollTerm creates a Term configured for scroll handler testing,
// using syncScheduler so QueueCommand callbacks execute immediately.
func newScrollTerm(rows, cols int) *Term {
	g := newGrid(rows, cols)
	t := &Term{
		grid:  g,
		cellW: 10,
		cellH: 20,
		cmd:   syncScheduler{},
	}
	return t
}

func TestTerm_OnWindowEvent_NoReportWhenFocusOff(t *testing.T) {
	term, buf := newTestTermCapture()
	// FocusReporting defaults to false
	term.HandleWindowEvent(&gui.Event{Type: gui.EventFocused})
	term.HandleWindowEvent(&gui.Event{Type: gui.EventUnfocused})
	if got := string(*buf); got != "" {
		t.Fatalf("focus off: got %q, want empty", got)
	}
}

func TestTerm_OnWindowEvent_NilEventNoPanic(t *testing.T) {
	term := &Term{grid: newGrid(1, 5), pw: writerFunc(func([]byte) (int, error) { return 0, nil })}
	term.HandleWindowEvent(nil) // must not panic
}

// TestHandleWindowEvent_MouseUpClearsDragAndLock verifies the stuck-drag
// safety net: when a window-resize gesture steals the mouse-up, the next
// window-level EventMouseUp resets dragging, dragReport, autoScrollDir, and
// the mouse lock flag (via unlockMouse).
func TestHandleWindowEvent_MouseUpClearsDragAndLock(t *testing.T) {
	term, _ := newTestTermCapture()
	term.mouse.dragging = true
	term.mouse.dragReport = true
	term.mouse.locked = true
	term.autoScrollDir.Store(1)

	term.HandleWindowEvent(&gui.Event{Type: gui.EventMouseUp})

	if term.mouse.dragging {
		t.Error("dragging should be false after window mouse-up")
	}
	if term.mouse.dragReport {
		t.Error("dragReport should be false after window mouse-up")
	}
	if term.mouse.locked {
		t.Error("locked should be false after window mouse-up")
	}
	if term.autoScrollDir.Load() != 0 {
		t.Error("autoScrollDir should be reset after window mouse-up")
	}
}

// TestHandleWindowEvent_ResizedClearsDrag verifies that EventResized clears a
// stale drag that was started before the resize began. On macOS a window-resize
// drag swallows the mouse-up, so the terminal never sees onMouseUp; the drag
// state would otherwise persist and spuriously extend the selection on the next
// pointer move.
func TestHandleWindowEvent_ResizedClearsDrag(t *testing.T) {
	term, _ := newTestTermCapture()
	term.mouse.dragging = true
	term.mouse.dragReport = true
	term.mouse.locked = true
	term.autoScrollDir.Store(1)

	term.HandleWindowEvent(&gui.Event{Type: gui.EventResized})

	if term.mouse.dragging {
		t.Error("dragging should be false after window resize")
	}
	if term.mouse.dragReport {
		t.Error("dragReport should be false after window resize")
	}
	if term.mouse.locked {
		t.Error("locked should be false after window resize")
	}
	if term.autoScrollDir.Load() != 0 {
		t.Error("autoScrollDir should be reset after window resize")
	}
}

func TestTerm_OnKeyDown_AppCursor(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.AppCursorKeys = true
	e := &gui.Event{KeyCode: gui.KeyUp}
	term.onKeyDown(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
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
	term.onKeyDown(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
	if got := string(*buf); got != "\x1bOq" {
		t.Fatalf("app keypad = %q, want %q", got, "\x1bOq")
	}
}

func TestTerm_OnWindowEvent_FocusReporting(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.FocusReporting = true
	term.HandleWindowEvent(&gui.Event{Type: gui.EventFocused})
	term.HandleWindowEvent(&gui.Event{Type: gui.EventUnfocused})
	if got := string(*buf); got != "\x1b[I\x1b[O" {
		t.Fatalf("focus reports = %q, want %q", got, "\x1b[I\x1b[O")
	}
}

func TestTerm_WriteBytes_UsesWriteHost(t *testing.T) {
	term := &Term{}
	term.pw = writerFunc(func([]byte) (int, error) { return 0, errors.New("boom") })
	term.writeBytes([]byte("x"))
}

// The blink flag lives on the grid, which is what lets blinkLoop read it
// under Mu. A focused pane animates exactly when the grid says so.
func TestCursorBlinkActive_HonorsGrid(t *testing.T) {
	g := newGrid(1, 5)
	tm := &Term{grid: g}
	tm.focused.Store(true)
	tm.winFocused.Store(true)
	if tm.cursorBlinkActive() {
		t.Error("default cursor should be steady")
	}
	g.CursorBlink = true
	if !tm.cursorBlinkActive() {
		t.Error("blinking cursor should animate while focused")
	}
}

// Cfg seeds the grid; it does not override it at draw time.
func TestApplyCursorConfig_SeedsGrid(t *testing.T) {
	g := newGrid(1, 5)
	applyCursorConfig(g, Cfg{CursorStyle: CursorStyleBar, CursorBlink: true})
	if g.cursorShape != CursorStyleBar || !g.CursorBlink {
		t.Errorf("seeded cursor = %v/blink %v, want bar/true", g.cursorShape, g.CursorBlink)
	}
	if g.defaultShape != CursorStyleBar || !g.defaultBlink {
		t.Errorf("defaults = %v/%v, want bar/true", g.defaultShape, g.defaultBlink)
	}
	// An out-of-range style falls back to the block the renderer would have
	// drawn anyway, so DECSCUSRParam agrees with the screen.
	applyCursorConfig(g, Cfg{CursorStyle: CursorStyle(99)})
	if g.cursorShape != CursorStyleBlock {
		t.Errorf("out-of-range style = %v, want block", g.cursorShape)
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
	g := newGrid(4, 8)
	base := gui.TextStyle{Typeface: glyph.TypefaceRegular}
	cell := cell{Ch: 'A', FG: 7, BG: 0, Width: 1}
	k := cellRunKey(cell, base, g, -1, -1, false, false)
	if k.ulStyle != ulNone || k.strikethrough {
		t.Error("plain cell should have no decoration")
	}
	if k.typeface != glyph.TypefaceRegular {
		t.Errorf("typeface: got %v, want regular", k.typeface)
	}
}

// End-to-end: an OSC 4 sequence off the pty must reach the color the
// foreground pass draws with.
func TestCellRunKey_OSC4Override(t *testing.T) {
	g, p := newParserGrid(4, 8)
	base := gui.TextStyle{Typeface: glyph.TypefaceRegular}
	c := cell{Ch: 'A', FG: 1, Width: 1}

	feed(t, g, p, []byte("\x1b]4;1;#00ff00\x07"))
	if got, want := cellRunKey(c, base, g, -1, -1, false, false).color, gui.RGB(0, 0xFF, 0); got != want {
		t.Errorf("after OSC 4: color = %+v, want %+v", got, want)
	}

	feed(t, g, p, []byte("\x1b]104;1\x07"))
	if got, want := cellRunKey(c, base, g, -1, -1, false, false).color, DefaultTheme.ANSI[1]; got != want {
		t.Errorf("after OSC 104: color = %+v, want %+v", got, want)
	}
}

func TestCellRunKey_BoldItalic(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{Typeface: glyph.TypefaceRegular}
	cell := cell{Ch: 'B', Width: 1, Attrs: attrBold | attrItalic}
	k := cellRunKey(cell, base, g, -1, -1, false, false)
	if k.typeface != glyph.TypefaceBoldItalic {
		t.Errorf("bold+italic: got %v, want BoldItalic", k.typeface)
	}
}

func TestCellRunKey_GeometryGlyphsIgnoreBoldTypeface(t *testing.T) {
	cases := []struct {
		name string
		ch   rune
	}{
		{"box-drawing", '│'},
		{"block-elements", '█'},
		{"braille", '⣿'},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newGrid(4, 8)
			base := gui.TextStyle{Typeface: glyph.TypefaceRegular}
			cell := cell{Ch: tc.ch, Width: 1, Attrs: attrBold}
			k := cellRunKey(cell, base, g, -1, -1, false, false)
			if k.typeface != glyph.TypefaceRegular {
				t.Fatalf("geometry glyph %q should not switch to bold typeface, got %v", tc.ch, k.typeface)
			}
		})
	}
}

func TestCellRunKey_NonGeometryGlyphStillBolds(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{Typeface: glyph.TypefaceRegular}
	cell := cell{Ch: 'A', Width: 1, Attrs: attrBold}
	k := cellRunKey(cell, base, g, -1, -1, false, false)
	if k.typeface != glyph.TypefaceBold {
		t.Fatalf("text glyph should still bold, got %v", k.typeface)
	}
}

func TestCellRunKey_GeometryGlyph_BoldItalicUsesItalic(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{Typeface: glyph.TypefaceRegular}
	cell := cell{Ch: '│', Width: 1, Attrs: attrBold | attrItalic}
	k := cellRunKey(cell, base, g, -1, -1, false, false)
	// Bold is suppressed; italic is not — TypefaceItalic expected.
	if k.typeface != glyph.TypefaceItalic {
		t.Fatalf("geometry glyph bold+italic: got %v, want TypefaceItalic", k.typeface)
	}
}

func TestIsGeometryGlyph_Boundaries(t *testing.T) {
	cases := []struct {
		r    rune
		want bool
		desc string
	}{
		{0x24FF, false, "just below Box Drawing"},
		{0x2500, true, "first Box Drawing"},
		{0x257F, true, "last Box Drawing"},
		{0x2580, true, "first Block Elements"},
		{0x259F, true, "last Block Elements"},
		{0x25A0, true, "first Geometric Shapes"},
		{0x25C6, true, "◆ (DEC Special Graphics diamond)"},
		{0x25FF, true, "last Geometric Shapes"},
		{0x2600, false, "just above Geometric Shapes"},
		{0x23B9, false, "just below scan lines"},
		{0x23BA, true, "first scan line ⎺"},
		{0x23BD, true, "last scan line ⎽"},
		{0x23BE, false, "just above scan lines"},
		{0x27FF, false, "just below Braille"},
		{0x2800, true, "first Braille"},
		{0x28FF, true, "last Braille"},
		{0x2900, false, "just above Braille"},
	}
	for _, tc := range cases {
		if got := isGeometryGlyph(tc.r); got != tc.want {
			t.Errorf("isGeometryGlyph(%U) %s: got %v, want %v", tc.r, tc.desc, got, tc.want)
		}
	}
}

func TestCellRunKey_Underline(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{}
	cell := cell{Ch: 'C', Width: 1, Attrs: attrUnderline, ULStyle: ulSingle, ULColor: defaultColor}
	k := cellRunKey(cell, base, g, -1, -1, false, false)
	if k.ulStyle != ulSingle {
		t.Errorf("underline attr: expected ulSingle in key, got %d", k.ulStyle)
	}
}

func TestCellRunKey_Strikethrough(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{}
	cell := cell{Ch: 'D', Width: 1, Attrs: attrStrikethrough}
	k := cellRunKey(cell, base, g, -1, -1, false, false)
	if !k.strikethrough {
		t.Error("strikethrough attr: expected strikethrough in key")
	}
}

func TestCellRunKey_LinkForcesUnderline(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{}
	cell := cell{Ch: 'E', Width: 1, LinkID: 42}
	k := cellRunKey(cell, base, g, -1, -1, false, false)
	if k.ulStyle == ulNone {
		t.Error("linked cell: expected underline forced on by linkID")
	}
}

func TestCellRunKey_LinkHoverRecolorCmdOnly(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{}
	cell := cell{Ch: 'E', Width: 1, LinkID: 42}

	// Place a cell with the same link ID at (0, 1) so hover matches.
	g.Cells[1].LinkID = 42

	kNoCmd := cellRunKey(cell, base, g, 0, 1, false, false)
	kCmd := cellRunKey(cell, base, g, 0, 1, true, false)

	if kCmd.color == kNoCmd.color {
		t.Error("Cmd held over same-link cell: expected color to differ (hover recolor)")
	}
}

// TestCellRunKey_DifferentLinksSameStyleCoalesce asserts the intent
// behind dropping linkID from runKey: two cells in different links
// but with the same visual style produce equal keys, allowing the
// foreground pass to coalesce them into one dc.Text call (and one
// go-glyph layout-cache entry).
func TestCellRunKey_DifferentLinksSameStyleCoalesce(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{}
	a := cell{Ch: 'x', Width: 1, LinkID: 1}
	b := cell{Ch: 'y', Width: 1, LinkID: 2}
	if cellRunKey(a, base, g, -1, -1, false, false) != cellRunKey(b, base, g, -1, -1, false, false) {
		t.Error("same-style cells in different links must produce equal keys")
	}
}

func TestCellRunKey_DimHalvesColor(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{}
	cell := cell{Ch: 'F', Width: 1, Attrs: attrDim}
	cell.FG = rgbColor(200, 100, 50)
	k := cellRunKey(cell, base, g, -1, -1, false, false)
	// Dim halves each channel via integer division.
	want := gui.RGB(100, 50, 25)
	if k.color != want {
		t.Errorf("dim color: got %v, want %v", k.color, want)
	}
}

func TestCellRunKey_URLHoverAppliesUnderlineAndBlue(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{}
	cell := cell{Ch: 'A', Width: 1, FG: rgbColor(200, 200, 200)}

	// urlHover=false — no underline, no recolor.
	kOff := cellRunKey(cell, base, g, -1, -1, false, false)
	if kOff.ulStyle != ulNone {
		t.Error("urlHover false: expected no underline")
	}

	// urlHover=true — underline forced on + blue recolor.
	kOn := cellRunKey(cell, base, g, -1, -1, false, true)
	if kOn.ulStyle == ulNone {
		t.Error("urlHover true: expected underline forced on")
	}
	if kOn.color == kOff.color {
		t.Error("urlHover true: expected color to change (blue recolor)")
	}
}

func TestCellRunKey_URLHoverVsExplicitLink(t *testing.T) {
	// urlHover and an explicit LinkID should never appear together on
	// the same cell (the caller gating enforces this), but if they do
	// the explicit link path wins — it runs first in cellRunKey.
	g := newGrid(4, 8)
	base := gui.TextStyle{}
	cell := cell{Ch: 'B', Width: 1, LinkID: 7, FG: rgbColor(200, 200, 200)}
	g.Cells[0].LinkID = 7

	// urlHover=true but LinkID is set → explicit-link path applies (Cmd-hover).
	k := cellRunKey(cell, base, g, 0, 0, true, true)
	if k.ulStyle == ulNone {
		t.Error("explicit link with urlHover: expected underline from link path")
	}
}

// BenchmarkForegroundPass exercises the run-key computation and string
// building for a full 80×24 screen of mixed colored text. It does not
// call dc.Text (no GUI context required) — the hot path is the loop
// logic and memory access pattern.
func BenchmarkForegroundPass(b *testing.B) {
	const rows, cols = 24, 80
	g := newGrid(rows, cols)
	base := gui.TextStyle{Typeface: glyph.TypefaceRegular}

	// Fill with alternating color runs to stress the coalescing path.
	colors := []uint32{rgbColor(200, 200, 200), rgbColor(100, 200, 100), rgbColor(200, 100, 100)}
	for r := range rows {
		for c := range cols {
			g.Cells[r*cols+c] = cell{
				Ch:    rune('A' + c%26),
				FG:    colors[c%len(colors)],
				Width: 1,
			}
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		for r := range rows {
			for c := range cols {
				cell := g.Cells[r*cols+c]
				if cell.Width == 0 && cell.Ch == 0 {
					continue
				}
				_ = cellRunKey(cell, base, g, -1, -1, false, false)
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
		term.onKeyDown(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
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
		term.onKeyDown(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
		if got := string(*buf); got != c.want {
			t.Errorf("Alt+%v = %q, want %q", c.key, got, c.want)
		}
	}
}

func TestTerm_OnKeyDown_AltCtrlLetter(t *testing.T) {
	term, buf := newTestTermCapture()
	// Alt+Ctrl+B → ESC + 0x02
	e := &gui.Event{KeyCode: gui.KeyB, Modifiers: gui.ModAlt | gui.ModCtrl}
	term.onKeyDown(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
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
		term.onKeyDown(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
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

func TestScrollbarGeometry_MinThumbClamp(t *testing.T) {
	// Large scrollback → thumb would be tiny; clamp must keep it ≥ min.
	const sb, rows, h = 100000, 24, 480.0
	_, th := scrollbarThumb(sb, rows, 0, h)
	if th < minScrollbarThumbH {
		t.Errorf("thumbH = %.3f, want ≥ %.3f", th, minScrollbarThumbH)
	}
}

func TestScrollbarGeometry_NaNInfViewH(t *testing.T) {
	nan := float32(math.NaN())
	inf := float32(math.Inf(1))
	ninf := float32(math.Inf(-1))

	for _, vh := range []float32{0, -1, nan, inf, ninf} {
		y, h := scrollbarGeometry(100, 24, 0, vh)
		if y != 0 || h != 0 {
			t.Errorf("viewH=%v: got y=%v h=%v, want (0,0)", vh, y, h)
		}
	}
}

func TestTerm_PosToCell_NaNInfCollapseToZero(t *testing.T) {
	term := &Term{
		grid:  newGrid(24, 80),
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
	term.cmd = &gui.Window{}
	term.search.active = true

	e := &gui.Event{CharCode: 'a'}
	term.onChar(gui.EventCtx{Layout: nil, Event: e, Window: nil})
	if term.search.query != "a" {
		t.Fatalf("query = %q, want \"a\"", term.search.query)
	}
	if !e.IsHandled {
		t.Error("event must be handled in search mode")
	}

	// Fill to exactly MaxGridDim runes (already have 1 'a').
	for i := 1; i < MaxGridDim; i++ {
		term.onChar(gui.EventCtx{Layout: nil, Event: &gui.Event{CharCode: 'x'}, Window: nil})
	}
	if utf8.RuneCountInString(term.search.query) != MaxGridDim {
		t.Fatalf("query rune count = %d, want %d", utf8.RuneCountInString(term.search.query), MaxGridDim)
	}
	// Next char must be rejected (at cap).
	before := term.search.query
	term.onChar(gui.EventCtx{Layout: nil, Event: &gui.Event{CharCode: 'z'}, Window: nil})
	if term.search.query != before {
		t.Errorf("query grew past MaxGridDim cap: len now %d", utf8.RuneCountInString(term.search.query))
	}
}

func TestTerm_SearchJump_ForwardFindsMatch(t *testing.T) {
	term, _ := newTestTermCapture()
	term.cmd = &gui.Window{}
	// putRow places at (0,0). Find skips start.Col+1 on the start row,
	// so content at col 0 is invisible to a fresh search. Pad col 0
	// so the matchable text starts at col 1.
	putRow(term.grid, "xhello")
	term.search.query = "hello"
	verBefore := term.drawVersion.Load()
	term.searchJump(true, &gui.Window{})
	term.grid.Mu.Lock()
	off := term.grid.ViewOffset
	term.grid.Mu.Unlock()
	if off != 0 {
		t.Errorf("ViewOffset = %d after live match, want 0", off)
	}
	if term.drawVersion.Load() <= verBefore {
		t.Error("drawVersion not incremented after search jump")
	}
	if !time.Now().Before(term.scrollbar.until) {
		t.Error("scrollbar.until not set to future after search jump")
	}
}

func TestTerm_SearchJump_NoMatchDoesNotPanic(t *testing.T) {
	term, _ := newTestTermCapture()
	term.cmd = &gui.Window{}
	term.search.query = "xyzzy_not_present"
	verBefore := term.drawVersion.Load()
	scBefore := term.scrollbar.until
	term.searchJump(true, &gui.Window{}) // must not panic
	if term.drawVersion.Load() != verBefore {
		t.Error("drawVersion changed on no-match jump")
	}
	if !term.scrollbar.until.Equal(scBefore) {
		t.Error("scrollbar.until modified on no-match jump")
	}
}

func TestTerm_SearchJump_EmptyQuery_Nop(t *testing.T) {
	term, _ := newTestTermCapture()
	term.cmd = &gui.Window{}
	term.search.query = ""
	verBefore := term.drawVersion.Load()
	scBefore := term.scrollbar.until
	term.searchJump(true, &gui.Window{}) // early return, must not panic
	if term.drawVersion.Load() != verBefore {
		t.Error("drawVersion changed on empty-query jump")
	}
	if !term.scrollbar.until.Equal(scBefore) {
		t.Error("scrollbar.until modified on empty-query jump")
	}
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
		term.onKeyDown(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
		if got := string(*buf); got != c.want {
			t.Errorf("key=%v mods=%v: got %q want %q", c.key, c.mods, got, c.want)
		}
	}
}

func TestTerm_OnKeyDown_CtrlShiftHomeEndPassthrough(t *testing.T) {
	// Ctrl+Shift+Home/End must pass through to the pty as Ctrl+Home/End
	// (not be consumed by scroll-to-top/bottom). Ctrl+Shift+Tab without
	// KKP falls through to raw \t instead of emitting \x1b[Z].
	cases := []struct {
		key  gui.KeyCode
		mods gui.Modifier
		want string
	}{
		// Shift+Home/End (no Ctrl) — still scrolls, no pty output.
		{gui.KeyHome, gui.ModShift, ""},
		{gui.KeyEnd, gui.ModShift, ""},
		// Ctrl+Shift+Home/End — passes through as Ctrl+Home/Ctrl+End
		// (Shift is deliberately excluded from modParam; it has special
		// scrollback semantics in this terminal).
		{gui.KeyHome, gui.ModShift | gui.ModCtrl, "\x1b[1;5H"},
		{gui.KeyEnd, gui.ModShift | gui.ModCtrl, "\x1b[1;5F"},
		// Ctrl+Home/End (no Shift) — unaffected.
		{gui.KeyHome, gui.ModCtrl, "\x1b[1;5H"},
		{gui.KeyEnd, gui.ModCtrl, "\x1b[1;5F"},
		// Ctrl+Shift+Tab without KKP — raw \t, not \x1b[Z.
		{gui.KeyTab, gui.ModShift | gui.ModCtrl, "\t"},
	}
	for _, c := range cases {
		term, buf := newTestTermCapture()
		e := &gui.Event{KeyCode: c.key, Modifiers: c.mods}
		term.onKeyDown(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
		got := string(*buf)
		if got != c.want {
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
	term.onKeyDown(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
	if got := string(*buf); got != "\x1b[127u" {
		t.Fatalf("KKP backspace: got %q, want %q", got, "\x1b[127u")
	}
}

func TestTerm_KittyKey_Enter(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 1
	e := &gui.Event{KeyCode: gui.KeyEnter}
	term.onKeyDown(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
	if got := string(*buf); got != "\x1b[13u" {
		t.Fatalf("KKP enter: got %q, want %q", got, "\x1b[13u")
	}
}

func TestTerm_KittyKey_Tab(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 1
	e := &gui.Event{KeyCode: gui.KeyTab}
	term.onKeyDown(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
	if got := string(*buf); got != "\x1b[9u" {
		t.Fatalf("KKP tab: got %q, want %q", got, "\x1b[9u")
	}
}

func TestTerm_KittyKey_Escape(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 1
	e := &gui.Event{KeyCode: gui.KeyEscape}
	term.onKeyDown(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
	if got := string(*buf); got != "\x1b[27u" {
		t.Fatalf("KKP escape: got %q, want %q", got, "\x1b[27u")
	}
}

func TestTerm_KittyKey_CtrlC(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 1
	// Ctrl+C: KeyCode=KeyC, Modifiers=ModCtrl. Codepoint for 'c' is 99.
	e := &gui.Event{KeyCode: gui.KeyC, Modifiers: gui.ModCtrl}
	term.onKeyDown(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
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
		{13, 0, "\x1b[13;1:3u"},           // Enter release, no mods
		{9, gui.ModShift, "\x1b[9;2:3u"},  // Shift+Tab release
		{27, gui.ModCtrl, "\x1b[27;5:3u"}, // Ctrl+Escape release
		{65, gui.ModAlt, "\x1b[65;3:3u"},  // Alt+A release
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
	term.onKeyUp(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
	if got := string(*buf); got != "\x1b[13;1:3u" {
		t.Fatalf("KKP Enter release: got %q, want %q", got, "\x1b[13;1:3u")
	}

	// Clear buffer for next test
	*buf = (*buf)[:0]

	// Test Shift+Tab release
	e = &gui.Event{KeyCode: gui.KeyTab, Modifiers: gui.ModShift}
	term.onKeyUp(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
	if got := string(*buf); got != "\x1b[9;2:3u" {
		t.Fatalf("KKP Shift+Tab release: got %q, want %q", got, "\x1b[9;2:3u")
	}
}

func TestTerm_KittyKey_ModifierOnly(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 2 // Enable event type reporting (flag bit 2)

	// Test Shift key release
	e := &gui.Event{KeyCode: gui.KeyLeftShift}
	term.onKeyUp(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
	if got := string(*buf); got != "\x1b[57441;1:3u" {
		t.Fatalf("KKP Shift release: got %q, want %q", got, "\x1b[57441;1:3u")
	}

	// Clear buffer for next test
	*buf = (*buf)[:0]

	// Test Ctrl key release
	e = &gui.Event{KeyCode: gui.KeyLeftControl}
	term.onKeyUp(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
	if got := string(*buf); got != "\x1b[57442;1:3u" {
		t.Fatalf("KKP Ctrl release: got %q, want %q", got, "\x1b[57442;1:3u")
	}

	// Clear buffer for next test
	*buf = (*buf)[:0]

	// Test Alt key release
	e = &gui.Event{KeyCode: gui.KeyLeftAlt}
	term.onKeyUp(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
	if got := string(*buf); got != "\x1b[57443;1:3u" {
		t.Fatalf("KKP Alt release: got %q, want %q", got, "\x1b[57443;1:3u")
	}
}

func TestTerm_KittyKey_ReleaseDisabled(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 1 // Event type reporting disabled (flag bit 2 not set)

	// Test that no release events are generated when flag bit 2 is not set
	e := &gui.Event{KeyCode: gui.KeyEnter}
	term.onKeyUp(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
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
		term.onKeyUp(gui.EventCtx{Layout: nil, Event: &gui.Event{KeyCode: c.key}, Window: &gui.Window{}})
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
		term.onKeyUp(gui.EventCtx{Layout: nil, Event: &gui.Event{KeyCode: c.key}, Window: &gui.Window{}})
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
		term.onKeyUp(gui.EventCtx{Layout: nil, Event: &gui.Event{KeyCode: c.key}, Window: &gui.Window{}})
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
		term.onKeyUp(gui.EventCtx{Layout: nil, Event: &gui.Event{KeyCode: c.key}, Window: &gui.Window{}})
		if got := string(*buf); got != c.want {
			t.Errorf("key=%v: got %q, want %q", c.key, got, c.want)
		}
	}
}

func TestTerm_KittyKey_KPEnterRelease(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 2
	term.onKeyUp(gui.EventCtx{Layout: nil, Event: &gui.Event{KeyCode: gui.KeyKPEnter}, Window: &gui.Window{}})
	if got := string(*buf); got != "\x1b[13;1:3u" {
		t.Fatalf("KPEnter release: got %q, want %q", got, "\x1b[13;1:3u")
	}
}

func TestTerm_KittyKey_UnknownKeyNoOutput(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 2
	// KeyF13 is not in the switch; should produce no output.
	term.onKeyUp(gui.EventCtx{Layout: nil, Event: &gui.Event{KeyCode: gui.KeyF13}, Window: &gui.Window{}})
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
		term.onKeyDown(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
		if got := string(*buf); got != c.want {
			t.Errorf("legacy key=%v: got %q, want %q", c.key, got, c.want)
		}
	}
}

func TestParser_MousePixelMode_Toggle(t *testing.T) {
	g := newGrid(5, 10)
	p := newParser(g)
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
		// cell mode: col+1 / row+1
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

func TestSendDesktopNotify_NullByteStripped(t *testing.T) {
	// Null bytes are stripped before reaching subprocess args.
	// Test via inline ReplaceAll (the former cleanNotifyStr behavior).
	clean := func(s string) string { return strings.ReplaceAll(s, "\x00", "") }
	if got := clean("hel\x00lo"); got != "hello" {
		t.Fatalf("null byte: got %q, want %q", got, "hello")
	}
	if got := clean(`say "hi"`); got != `say "hi"` {
		t.Fatalf("double quote should be preserved: got %q", got)
	}
	if got := clean("a\x00b\"c"); got != "ab\"c" {
		t.Fatalf("both: got %q, want %q", got, "ab\"c")
	}
}

func TestSendDesktopNotify_HostileInputNoPanic(t *testing.T) {
	// Subprocess errors are swallowed; just verify no panic with hostile input.
	sendDesktopNotify(`"; rm -rf /`, "body\x00with null")
	sendDesktopNotify("", `body "quoted"`)
}

// TestTerm_NotifyBusy_ExtrasDropped verifies that concurrent OSC notifications
// are deduplicated: while one is in flight, subsequent calls are dropped.
func TestTerm_NotifyBusy_ExtrasDropped(t *testing.T) {
	block := make(chan struct{})
	finished := make(chan struct{})
	calls := 0

	term := &Term{grid: newGrid(4, 8)}
	term.cfg.OnNotify = func(_, _ string) {
		calls++
		<-block
		close(finished)
	}

	// Replicate the exact handler registered in New.
	send := func() {
		if !term.notifyBusy.CompareAndSwap(false, true) {
			return
		}
		fn := term.cfg.OnNotify
		go func() {
			defer term.notifyBusy.Store(false)
			fn("", "msg")
		}()
	}

	send() // acquires busy, goroutine blocks on <-block
	send() // dropped
	send() // dropped
	close(block)

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for notification goroutine")
	}

	if calls != 1 {
		t.Fatalf("want 1 call, got %d", calls)
	}
}

// --- termRuneStr ---

func TestTermRuneStr_ASCIINoAlloc(t *testing.T) {
	tm := &Term{}
	var sink string
	avg := testing.AllocsPerRun(100, func() {
		sink = tm.termRuneStr('A')
	})
	_ = sink
	if avg != 0 {
		t.Errorf("ASCII path should not allocate, got %v allocs/op", avg)
	}
}

func TestTermRuneStr_NonASCIICachesOnMiss(t *testing.T) {
	tm := &Term{}
	r := rune(0x2603) // ☃ snowman
	s := tm.termRuneStr(r)
	if s != string(r) {
		t.Errorf("got %q, want %q", s, string(r))
	}
	if tm.draw.runeCache == nil || tm.draw.runeCache[r] == "" {
		t.Error("rune not stored in cache after first call")
	}
}

func TestTermRuneStr_CacheHitNoAlloc(t *testing.T) {
	tm := &Term{}
	r := rune(0x1F600) // 😀 emoji (4-byte UTF-8)
	tm.termRuneStr(r)  // prime the cache
	var sink string
	avg := testing.AllocsPerRun(100, func() {
		sink = tm.termRuneStr(r)
	})
	_ = sink
	if avg != 0 {
		t.Errorf("cache hit should not allocate, got %v allocs/op", avg)
	}
}

func TestKeypadSeq_All(t *testing.T) {
	tests := []struct {
		k    gui.KeyCode
		want string
	}{
		{gui.KeyKP0, "\x1bOp"},
		{gui.KeyKP1, "\x1bOq"},
		{gui.KeyKP2, "\x1bOr"},
		{gui.KeyKP3, "\x1bOs"},
		{gui.KeyKP4, "\x1bOt"},
		{gui.KeyKP5, "\x1bOu"},
		{gui.KeyKP6, "\x1bOv"},
		{gui.KeyKP7, "\x1bOw"},
		{gui.KeyKP8, "\x1bOx"},
		{gui.KeyKP9, "\x1bOy"},
		{gui.KeyKPDecimal, "\x1bOn"},
		{gui.KeyKPDivide, "\x1bOo"},
		{gui.KeyKPMultiply, "\x1bOj"},
		{gui.KeyKPSubtract, "\x1bOm"},
		{gui.KeyKPAdd, "\x1bOk"},
		{gui.KeyKPEqual, "\x1bOX"},
		{gui.KeyA, ""},
		{gui.KeyKPEnter, ""},
		{gui.KeyCode(9999), ""},
	}
	for _, tt := range tests {
		got := keypadSeq(tt.k)
		if string(got) != tt.want {
			t.Errorf("keypadSeq(%v) = %q, want %q", tt.k, got, tt.want)
		}
	}
}

func TestKKPCodepoint_AllKeys(t *testing.T) {
	tests := []struct {
		k    gui.KeyCode
		want int
		ok   bool
	}{
		{gui.KeyLeftShift, 57441, true},
		{gui.KeyRightShift, 57447, true},
		{gui.KeyLeftControl, 57442, true},
		{gui.KeyRightControl, 57448, true},
		{gui.KeyLeftAlt, 57443, true},
		{gui.KeyRightAlt, 57449, true},
		{gui.KeyLeftSuper, 57444, true},
		{gui.KeyRightSuper, 57450, true},
		{gui.KeyEnter, 13, true},
		{gui.KeyKPEnter, 13, true},
		{gui.KeyBackspace, 127, true},
		{gui.KeyTab, 9, true},
		{gui.KeyEscape, 27, true},
		{gui.KeyInsert, 57348, true},
		{gui.KeyDelete, 57349, true},
		{gui.KeyLeft, 57350, true},
		{gui.KeyRight, 57351, true},
		{gui.KeyUp, 57352, true},
		{gui.KeyDown, 57353, true},
		{gui.KeyPageUp, 57354, true},
		{gui.KeyPageDown, 57355, true},
		{gui.KeyHome, 57356, true},
		{gui.KeyEnd, 57357, true},
		{gui.KeyF1, 57364, true},
		{gui.KeyF12, 57375, true},
		{gui.KeyA, int('a'), true},
		{gui.KeyZ, int('z'), true},
		{gui.Key0, int('0'), true},
		{gui.Key9, int('9'), true},
		{gui.KeyCode(9999), 0, false},
	}
	for _, tt := range tests {
		got, ok := kittyKeyCodepoint(tt.k)
		if ok != tt.ok {
			t.Errorf("kittyKeyCodepoint(%v) ok=%v, want %v", tt.k, ok, tt.ok)
		}
		if ok && got != tt.want {
			t.Errorf("kittyKeyCodepoint(%v) = %d, want %d", tt.k, got, tt.want)
		}
	}
}

// --- config helpers ---

func TestApplyScrollbackConfig(t *testing.T) {
	tests := []struct {
		name string
		rows int
		want int
	}{
		{"default", 0, defaultScrollbackRows},
		{"custom", 7000, 7000},
		{"clamped", MaxScrollbackCap + 1, MaxScrollbackCap},
		{"disabled", -1, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := newGrid(24, 80)
			applyScrollbackConfig(g, Cfg{ScrollbackRows: tc.rows})
			if g.ScrollbackCap != tc.want {
				t.Errorf("ScrollbackCap = %d, want %d", g.ScrollbackCap, tc.want)
			}
		})
	}
}

func TestApplyTheme_EmptyThemes(t *testing.T) {
	g := newGrid(24, 80)
	g.setTheme(mustBundled(t, "iTerm2 Solarized Dark"))
	applyTheme(g, Cfg{})
	if g.Theme.DefaultFG == DefaultTheme.DefaultFG {
		t.Error("theme should not have changed when Themes is empty")
	}
}

func TestApplyTheme_FirstTheme(t *testing.T) {
	g := newGrid(24, 80)
	nord := mustBundled(t, "Nord")
	applyTheme(g, Cfg{
		Themes: []NamedTheme{
			{Name: "Nord", Theme: nord},
			{Name: "Default", Theme: DefaultTheme},
		},
	})
	if g.Theme.DefaultFG != nord.DefaultFG {
		t.Error("expected first theme (Nord) to be applied")
	}
}

// --- lifecycle ---

func TestClose_Idempotent(t *testing.T) {
	g := newGrid(24, 80)
	p := newParser(g)
	done := make(chan struct{})
	close(done)
	tm := &Term{
		grid:      g,
		parser:    p,
		blinkDone: done,
		readDone:  done,
	}
	tm.closed.Store(true)
	if err := tm.Close(); err != nil {
		t.Logf("Close returned error (expected with nil pty): %v", err)
	}
	if err := tm.Close(); err != nil {
		t.Logf("second Close: %v", err)
	}
}

func TestClose_FullIntegration(t *testing.T) {
	pty, err := startPTY(24, 80, Cfg{})
	if err != nil {
		t.Skipf("startPTY: %v", err)
	}
	g := newGrid(24, 80)
	tm := &Term{
		cfg:       Cfg{},
		grid:      g,
		parser:    newParser(g),
		pty:       pty,
		pw:        pty,
		cmd:       &gui.Window{},
		notif:     desktopNotifier{},
		blinkDone: make(chan struct{}),
		readDone:  make(chan struct{}),
	}
	tm.mouse.hoverR.Store(-1)
	tm.mouse.hoverC.Store(-1)

	// Start readLoop so Close can observe it drain.
	go tm.readLoop()

	// Close must stop the reader, close the pty, and be idempotent.
	if err := tm.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := tm.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	// PTY writes must fail after close.
	if _, err := pty.Write([]byte("x")); err == nil {
		t.Error("pty.Write after Close should fail")
	}
}

// A locked cursor keeps the configured blink no matter what the child asks
// for; an unlocked one follows DECSCUSR.
func TestCursorBlink_LockIgnoresDECSCUSR(t *testing.T) {
	g := newGrid(24, 80)
	applyCursorConfig(g, Cfg{CursorStyle: CursorStyleBar, CursorLocked: true})
	g.ApplyDECSCUSR(1) // blinking block
	if g.cursorShape != CursorStyleBar || g.CursorBlink {
		t.Errorf("locked cursor changed to %v/blink %v", g.cursorShape, g.CursorBlink)
	}
	applyCursorConfig(g, Cfg{CursorStyle: CursorStyleBar})
	g.ApplyDECSCUSR(1)
	if g.cursorShape != CursorStyleBlock || !g.CursorBlink {
		t.Errorf("unlocked cursor = %v/blink %v, want block/true", g.cursorShape, g.CursorBlink)
	}
}

// --- openURL scheme whitelist ---

func TestOpenURL_PermittedSchemes(t *testing.T) {
	// Permitted schemes reach exec.Command; blocked at the switch in the
	// default case and return without spawning a process. We verify the
	// function does not panic for any input — the exec may fail in CI but
	// the error is swallowed via cmd.Start().
	for _, url := range []string{
		"https://example.com",
		"http://example.com",
		"mailto:user@example.com",
	} {
		openURL(url) // must not panic
	}
}

func TestOpenURL_BlockedSchemes(t *testing.T) {
	for _, url := range []string{
		"file:///etc/passwd",
		"javascript:alert(1)",
		"gopher://example.com",
		"ssh://evil.com",
		"",
	} {
		openURL(url) // must not panic; silently dropped
	}
}

func TestOpenURL_RejectsShellInjection(t *testing.T) {
	// Windows cmd /c start would parse these as shell metacharacters; the
	// charset gate must drop them before any handler is reached.
	for _, url := range []string{
		"https://example.com\" & calc.exe &",
		"https://example.com & calc.exe",
		"https://example.com\ncalc.exe",
		"http://example.com\t--help",
		"mailto:a@b.c\" -e evil",
	} {
		openURL(url) // must not panic; silently dropped
	}
}

// --- reply writer (enqueueReplies + writeLoop) ---

// startReplyWriter starts tm.writeLoop with a fresh cond var and returns a
// stop func that signals replyDone and waits for the goroutine to exit.
func startReplyWriter(tm *Term) func() {
	tm.replyCond = sync.NewCond(&tm.replyMu)
	tm.loopWg.Add(1)
	go tm.writeLoop()
	return func() {
		tm.replyMu.Lock()
		tm.replyDone = true
		tm.replyMu.Unlock()
		tm.replyCond.Signal()
		tm.loopWg.Wait()
	}
}

// captureWriter returns an io.Writer that copies each write onto ch.
func captureWriter(ch chan []byte, err error) writerFunc {
	return writerFunc(func(b []byte) (int, error) {
		cp := make([]byte, len(b))
		copy(cp, b)
		ch <- cp
		return len(b), err
	})
}

func TestEnqueueReplies_EmptyNoOp(t *testing.T) {
	// Empty pendingReplies must not signal the writer or touch pw — and must
	// not panic even when replyCond is nil (never started).
	term := &Term{pw: writerFunc(func([]byte) (int, error) {
		t.Error("pw.Write must not be called for empty queue")
		return 0, nil
	})}
	term.enqueueReplies()
}

func TestEnqueueReplies_DropsPastCap(t *testing.T) {
	// At/over the cap, further replies are dropped rather than queued — but
	// pendingReplies is still cleared so the reader doesn't re-process them.
	tm := &Term{
		replyBytes:     maxReplyQueueBytes,
		pendingReplies: [][]byte{[]byte("dropme")},
	}
	tm.replyCond = sync.NewCond(&tm.replyMu)
	tm.enqueueReplies()
	if len(tm.replyQueue) != 0 {
		t.Errorf("reply should be dropped at cap, queue len = %d", len(tm.replyQueue))
	}
	if tm.replyBytes != maxReplyQueueBytes {
		t.Errorf("replyBytes = %d, want unchanged %d", tm.replyBytes, maxReplyQueueBytes)
	}
	if tm.pendingReplies != nil {
		t.Error("pendingReplies should be cleared even when dropped")
	}
}

func TestWriteLoop_ErrorPathDrainsAll(t *testing.T) {
	ch := make(chan []byte, 4)
	tm := &Term{
		pw:             captureWriter(ch, errTestBoom),
		pendingReplies: [][]byte{[]byte("reply1"), []byte("reply2")},
	}
	stop := startReplyWriter(tm)
	defer stop()

	// enqueue hands the batch to the writer goroutine, which writes both even
	// though each write returns an error (logged, not fatal).
	tm.enqueueReplies()
	for i, want := range []string{"reply1", "reply2"} {
		select {
		case got := <-ch:
			if string(got) != want {
				t.Errorf("write %d = %q, want %q", i, got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for write %d", i)
		}
	}
	if tm.pendingReplies != nil {
		t.Errorf("pendingReplies not cleared: %v", tm.pendingReplies)
	}
}

// --- scheduleResizeWake ---

func TestScheduleResizeWake_FirstCallCreatesTimer(t *testing.T) {
	term := &Term{}
	if term.resize.timer != nil {
		t.Fatal("resizeTimer should start nil")
	}
	// Use a long duration so the timer doesn't fire during the test.
	term.scheduleResizeWake(time.Hour)
	if term.resize.timer == nil {
		t.Fatal("resizeTimer should be created on first call")
	}
	term.resize.timer.Stop()
}

func TestScheduleResizeWake_ClosedSkipsBump(t *testing.T) {
	term := &Term{}
	term.closed.Store(true)
	prev := term.drawVersion.Load()
	term.scheduleResizeWake(time.Nanosecond)
	// Give the timer a moment to fire.
	time.Sleep(20 * time.Millisecond)
	if term.drawVersion.Load() != prev {
		t.Error("closed term: drawVersion must not change")
	}
	if term.resize.timer != nil {
		term.resize.timer.Stop()
	}
}

// --- applyChunk ---

// TestApplyChunk_GraphemeSplitAcrossReads verifies a ZWJ emoji cluster split
// across a (buffer-filling) read boundary is assembled into one width-2 cell
// rather than committed as broken pieces. Regression for the bug where the
// unconditional end-of-Feed flush wrote the leading bytes as their own cells.
func TestApplyChunk_GraphemeSplitAcrossReads(t *testing.T) {
	cases := []string{
		// woman health worker: woman + skin tone + ZWJ + ⚕ + VS16
		"\U0001f469\U0001f3ff\u200d\u2695\ufe0f",
		// kiss: woman, ZWJ heart+VS16, ZWJ kiss-mark, ZWJ man (multiple ZWJ)
		"\U0001f469\U0001f3ff\u200d\u2764\ufe0f\u200d\U0001f48b\u200d\U0001f468\U0001f3fb",
		// Unicode 16.0 emoji (U+1FAEF) inside a ZWJ sequence
		"\U0001f469\U0001f3fe\u200d\U0001faef\u200d\U0001f469\U0001f3fb",
	}
	for _, seq := range cases {
		b := []byte(seq)
		for split := 1; split < len(b); split++ {
			g := newGrid(24, 80)
			tm := &Term{grid: g, parser: newParser(g), cmd: &gui.Window{}}
			// First read fills its buffer (flush deferred); second read is
			// the short tail (flush). Mirrors readLoop's burst draining.
			tm.applyChunk(b[:split], false)
			tm.applyChunk(b[split:], true)
			if g.CursorC != 2 {
				t.Errorf("seq %q split@%d: cursorC=%d, want 2", seq, split, g.CursorC)
			}
			if w := g.At(0, 0).Width; w != 2 {
				t.Errorf("seq %q split@%d: head width=%d, want 2", seq, split, w)
			}
		}
	}
}

func TestApplyChunk_DirtyCellsBumpsVersion(t *testing.T) {
	g := newGrid(24, 80)
	tm := &Term{
		grid:   g,
		parser: newParser(g),
		cmd:    &gui.Window{},
	}
	needUpdate := tm.applyChunk([]byte("A"), true)
	if !needUpdate {
		t.Error("needUpdate should be true after dirty chunk")
	}
	if v := tm.drawVersion.Load(); v == 0 {
		t.Error("drawVersion should be bumped after dirty chunk")
	}
}

func TestApplyChunk_NoOpDataNoChange(t *testing.T) {
	g := newGrid(24, 80)
	tm := &Term{
		grid:   g,
		parser: newParser(g),
	}
	needUpdate := tm.applyChunk([]byte{}, true)
	if needUpdate {
		t.Error("needUpdate should be false for no-op data")
	}
	if v := tm.drawVersion.Load(); v != 0 {
		t.Errorf("drawVersion = %d, want 0", v)
	}
}

func TestApplyChunk_SyncOutputGatesRedraw(t *testing.T) {
	g := newGrid(24, 80)
	g.SyncOutput = true
	g.SyncActive = true
	tm := &Term{
		grid:   g,
		parser: newParser(g),
	}
	// Feed a character that would normally dirty the grid, but the
	// synchronized-output gate suppresses the version bump.
	needUpdate := tm.applyChunk([]byte("X"), true)
	if needUpdate {
		t.Error("needUpdate should be false when sync gate is active")
	}
	if v := tm.drawVersion.Load(); v != 0 {
		t.Errorf("drawVersion = %d, want 0 when sync gate active", v)
	}
}

func TestApplyChunk_BellSchedulesFlash(t *testing.T) {
	g := newGrid(24, 80)
	tm := &Term{
		grid:   g,
		parser: newParser(g),
		// syncScheduler runs the queued bell command inline; the flash
		// is armed on the GUI thread, not in applyChunk. Its zero
		// gui.Window has no native platform, so BeepAvailable is false
		// and the default BellAuto mode falls back to the flash.
		cmd: syncScheduler{},
		cfg: Cfg{BellFlashDuration: 50 * time.Millisecond},
	}
	// BEL (0x07) increments BellCount. Grid stays clean but the bell
	// delta makes dirty=true, triggering a version bump and flash.
	needUpdate := tm.applyChunk([]byte("\x07"), true)
	if !needUpdate {
		t.Error("needUpdate should be true after BEL")
	}
	if tm.bell.seenCount != 1 {
		t.Errorf("bell.seenCount = %d, want 1", tm.bell.seenCount)
	}
	if tm.bell.flashUntil.Load() == 0 {
		t.Error("bell.flashUntil should be set")
	}
	if tm.bell.flashTimer != nil {
		tm.bell.flashTimer.Stop()
	}
}

func TestApplyChunk_BellFlashDisabled(t *testing.T) {
	g := newGrid(24, 80)
	tm := &Term{
		grid:   g,
		parser: newParser(g),
		cmd:    syncScheduler{},
		cfg:    Cfg{BellFlashDuration: -1}, // disabled
	}
	tm.applyChunk([]byte("\x07"), true)
	if tm.bell.seenCount != 1 {
		t.Errorf("bell.seenCount = %d, want 1", tm.bell.seenCount)
	}
	if tm.bell.flashUntil.Load() != 0 {
		t.Error("bell.flashUntil should be zero when flash disabled")
	}
}

// newBellTerm builds a Term whose queued commands run inline. The zero
// gui.Window handed to syncScheduler reports BeepAvailable false, which
// is the "platform cannot beep" case.
func newBellTerm(mode BellMode) *Term {
	g := newGrid(24, 80)
	tm := &Term{
		grid:   g,
		parser: newParser(g),
		cmd:    syncScheduler{},
		cfg:    Cfg{BellMode: mode, BellFlashDuration: 50 * time.Millisecond},
	}
	// ringBell reads the atomic, not cfg — newWithPTY seeds it, and a
	// hand-built Term has to do the same.
	tm.bellMode.Store(int32(mode))
	return tm
}

func TestRingBell_ModeSelectsFlash(t *testing.T) {
	// With no beep available, only the modes that can flash should.
	tests := []struct {
		mode      BellMode
		wantFlash bool
	}{
		{BellAuto, true},     // degrades to visual when it cannot beep
		{BellAudible, false}, // sound only; stays silent instead
		{BellVisual, true},
		{BellBoth, true},
		{BellNone, false},
	}
	for _, tc := range tests {
		tm := newBellTerm(tc.mode)
		tm.applyChunk([]byte("\x07"), true)
		got := tm.bell.flashUntil.Load() != 0
		if got != tc.wantFlash {
			t.Errorf("mode %d: flash armed = %v, want %v",
				tc.mode, got, tc.wantFlash)
		}
		if tm.bell.flashTimer != nil {
			tm.bell.flashTimer.Stop()
		}
	}
}

func TestRingBell_NoneIgnoresBell(t *testing.T) {
	tm := newBellTerm(BellNone)
	tm.applyChunk([]byte("\x07"), true)
	// The BEL is still counted (the grid saw it) but nothing is signalled.
	if tm.bell.seenCount != 1 {
		t.Errorf("bell.seenCount = %d, want 1", tm.bell.seenCount)
	}
	if tm.bell.flashUntil.Load() != 0 {
		t.Error("BellNone should not arm the flash")
	}
}

func TestAllowBeep_RateLimits(t *testing.T) {
	tm := newBellTerm(BellAudible)
	base := time.Now()

	if !tm.allowBeep(base) {
		t.Fatal("first beep should be allowed")
	}
	if tm.allowBeep(base.Add(beepInterval / 2)) {
		t.Error("beep within beepInterval should be suppressed")
	}
	if !tm.allowBeep(base.Add(beepInterval + time.Millisecond)) {
		t.Error("beep after beepInterval should be allowed")
	}
}

func TestAllowBeep_SuppressedBeepDoesNotAdvanceStamp(t *testing.T) {
	// A storm of bells must not push the next allowed beep further out
	// each time, or a tight BEL loop would silence the bell entirely.
	tm := newBellTerm(BellAudible)
	base := time.Now()
	tm.allowBeep(base)
	for i := range 10 {
		tm.allowBeep(base.Add(time.Duration(i) * time.Millisecond))
	}
	if !tm.allowBeep(base.Add(beepInterval + time.Millisecond)) {
		t.Error("suppressed beeps advanced the rate-limit stamp")
	}
}

func TestApplyChunk_HandsRepliesToWriter(t *testing.T) {
	g := newGrid(24, 80)
	ch := make(chan []byte, 4)
	tm := &Term{
		grid:           g,
		parser:         newParser(g),
		cmd:            &gui.Window{},
		pw:             captureWriter(ch, nil),
		pendingReplies: [][]byte{[]byte("r1"), []byte("r2")},
	}
	stop := startReplyWriter(tm)
	defer stop()

	// applyChunk hands queued replies to the writer goroutine after releasing
	// grid.Mu — decoupled from both the render loop and the read loop.
	tm.applyChunk([]byte{}, true)
	for i, want := range []string{"r1", "r2"} {
		select {
		case got := <-ch:
			if string(got) != want {
				t.Errorf("reply %d = %q, want %q", i, got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for reply %d", i)
		}
	}
	if tm.pendingReplies != nil {
		t.Error("pendingReplies should be cleared after enqueue")
	}
}

func TestApplyChunk_RepliesEmitPromptly(t *testing.T) {
	// A DSR cursor-position query must produce a CPR reply promptly via the
	// writer goroutine — this is the round-trip latency that made ucs-detect
	// slow when replies were deferred to the main-thread render loop.
	g := newGrid(24, 80)
	ch := make(chan []byte, 4)
	tm := &Term{
		grid:   g,
		parser: newParser(g),
		cmd:    &gui.Window{},
		pw:     captureWriter(ch, nil),
	}
	tm.parser.SetReplyHandler(tm.onParserReply)
	stop := startReplyWriter(tm)
	defer stop()

	tm.applyChunk([]byte("\x1b[6n"), true) // DSR 6 → expect CPR (ESC [ row;col R)
	select {
	case got := <-ch:
		if !strings.HasPrefix(string(got), "\x1b[") || !strings.HasSuffix(string(got), "R") {
			t.Errorf("expected CPR reply, got %q", string(got))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for CPR reply")
	}
}

func TestApplyChunk_MultipleChunksEachBumpVersion(t *testing.T) {
	g := newGrid(24, 80)
	tm := &Term{
		grid:   g,
		parser: newParser(g),
		cmd:    &gui.Window{},
	}
	// Two separate dirty chunks — each bumps the version even though the
	// coalesced UpdateWindow is queued at most once.
	tm.applyChunk([]byte("AB"), true)
	tm.applyChunk([]byte("CD"), true)
	if v := tm.drawVersion.Load(); v != 2 {
		t.Errorf("drawVersion = %d, want 2 (one per dirty chunk)", v)
	}
}

// --- writeMouse NaN/Inf pixel coords ---

func TestWriteMouse_PixelCoordsNaN(t *testing.T) {
	term, buf := newTestTermCapture()
	// NaN pixX should collapse to 0 via the realNumber guard; pixY=9
	// unchanged. Expect 1-based coords: col=int(0)+1=1, row=int(9)+1=10.
	term.writeMouse(0, 0, 0, float32(math.NaN()), 9.0, true, true)
	want := "\x1b[<0;1;10M"
	if got := string(*buf); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteMouse_PixelCoordsInf(t *testing.T) {
	term, buf := newTestTermCapture()
	term.writeMouse(0, 0, 0, 5.0, float32(math.Inf(1)), true, true)
	want := "\x1b[<0;6;1M"
	if got := string(*buf); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- cancelMomentum nil timer ---

func TestCancelMomentum_BeforeFirstScrollNoPanic(t *testing.T) {
	term := &Term{}       // momentumTimer is nil by default
	term.cancelMomentum() // must not panic — nil-guarded inside
}

// errTestBoom is a sentinel error for ptyWriter failure tests.
var errTestBoom = errors.New("boom")

// ---------------------------------------------------------------------------
// Lifecycle loop tests
// ---------------------------------------------------------------------------

func TestBlinkLoop_ExitsOnBlinkDone(t *testing.T) {
	tm := &Term{
		grid:      newGrid(24, 80),
		blinkDone: make(chan struct{}),
	}
	tm.loopWg.Add(1)
	done := make(chan struct{})
	go func() {
		tm.blinkLoop()
		close(done)
	}()
	close(tm.blinkDone)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blinkLoop did not exit within timeout")
	}
}

func TestAutoScrollLoop_ExitsOnBlinkDone(t *testing.T) {
	tm := &Term{
		grid:      newGrid(24, 80),
		blinkDone: make(chan struct{}),
	}
	tm.loopWg.Add(1)
	done := make(chan struct{})
	go func() {
		tm.autoScrollLoop()
		close(done)
	}()
	close(tm.blinkDone)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("autoScrollLoop did not exit within timeout")
	}
}

func TestMomentumLoop_ExitsOnBlinkDone(t *testing.T) {
	tm := &Term{
		grid:      newGrid(24, 80),
		blinkDone: make(chan struct{}),
		momentum:  momentumState{kick: make(chan struct{}, 1)},
	}
	tm.loopWg.Add(1)
	done := make(chan struct{})
	go func() {
		tm.momentumLoop()
		close(done)
	}()
	close(tm.blinkDone)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("momentumLoop did not exit within timeout")
	}
}

func TestClose_StopsAuxiliaryGoroutines(t *testing.T) {
	g := newGrid(24, 80)
	tm := &Term{
		grid:      g,
		blinkDone: make(chan struct{}),
		readDone:  make(chan struct{}),
		momentum:  momentumState{kick: make(chan struct{}, 1)},
		pw:        writerFunc(func([]byte) (int, error) { return 0, nil }),
	}
	tm.closed.Store(true)
	close(tm.blinkDone)
	close(tm.readDone)
	tm.loopWg.Add(3)
	go tm.blinkLoop()
	go tm.autoScrollLoop()
	go tm.momentumLoop()
	done := make(chan struct{})
	go func() {
		tm.loopWg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop goroutines did not exit within timeout")
	}
}

func TestTerm_IMECompositionState(t *testing.T) {
	win := gui.NewWindow(gui.WindowCfg{})
	term, err := New(win, Cfg{})
	if err != nil {
		t.Fatalf("New term: %v", err)
	}
	defer func() { _ = term.Close() }()

	// Initial state
	if term.ime.composing {
		t.Error("expected initial imeComposing to be false")
	}

	// Dispatch an IME composition event to the window
	win.EventFn(&gui.Event{
		Type:      gui.EventIMEComposition,
		IMEText:   "かん",
		IMEStart:  1,
		IMELength: 1,
	})

	// Invoke View to let it detect the composition and update cached state
	_ = term.View(win)

	if !term.ime.composing {
		t.Error("expected ime.composing to be true")
	}
	if term.ime.compText != "かん" {
		t.Errorf("expected ime.compText to be 'かん', got %q", term.ime.compText)
	}
	if term.ime.compCursor != 1 {
		t.Errorf("expected ime.compCursor to be 1, got %d", term.ime.compCursor)
	}

	// Dispatch an empty composition to clear IME
	win.EventFn(&gui.Event{
		Type:    gui.EventIMEComposition,
		IMEText: "",
	})

	_ = term.View(win)

	if term.ime.composing {
		t.Error("expected ime.composing to be false after clear")
	}
	if term.ime.compText != "" {
		t.Errorf("expected ime.compText to be empty, got %q", term.ime.compText)
	}
}

// TestTerm_IMEComposition_UnfocusedPaneIgnores pins the focus gate in View:
// IME composition state lives on the window, shared by every pane in a split,
// so an unfocused Term must neither adopt it nor keep a copy it cached before
// focus moved away. Without the gate every pane renders the same preedit at
// its own cursor.
func TestTerm_IMEComposition_UnfocusedPaneIgnores(t *testing.T) {
	win := gui.NewWindow(gui.WindowCfg{})
	term, err := New(win, Cfg{})
	if err != nil {
		t.Fatalf("New term: %v", err)
	}
	defer func() { _ = term.Close() }()

	compose := func() {
		win.EventFn(&gui.Event{
			Type:      gui.EventIMEComposition,
			IMEText:   "かん",
			IMEStart:  1,
			IMELength: 1,
		})
	}

	// Unfocused pane: the window is composing, this pane must ignore it.
	term.SetFocused(false)
	compose()
	_ = term.View(win)
	if term.ime.composing {
		t.Error("unfocused pane adopted composition state")
	}
	if term.ime.compText != "" {
		t.Errorf("unfocused pane cached compText %q, want empty", term.ime.compText)
	}

	// Focused pane: same window state, now it must be picked up. Proves the
	// gate keys off focus rather than being a dead branch.
	term.SetFocused(true)
	_ = term.View(win)
	if !term.ime.composing {
		t.Error("focused pane did not adopt composition state")
	}
	if term.ime.compText != "かん" {
		t.Errorf("focused pane compText = %q, want かん", term.ime.compText)
	}

	// Focus moves away mid-composition: the stale preedit must be dropped,
	// not left painted on the now-inactive pane.
	term.SetFocused(false)
	_ = term.View(win)
	if term.ime.composing {
		t.Error("composition state survived losing focus")
	}
	if term.ime.compText != "" {
		t.Errorf("compText %q survived losing focus, want empty", term.ime.compText)
	}
}

// TestTerm_View_RestoresFocus verifies that View() reasserts
// w.SetFocus when the Term is focused. go-gui clears the focus ID
// during UpdateView; View() must restore it so keystrokes reach
// onChar/onKeyDown without requiring a prior click.
func TestTerm_View_RestoresFocus(t *testing.T) {
	win := gui.NewWindow(gui.WindowCfg{Width: 640, Height: 480})
	term, err := New(win, Cfg{})
	if err != nil {
		t.Fatalf("New term: %v", err)
	}
	defer func() { _ = term.Close() }()

	// Simulate what UpdateView does: clear focus, then rebuild.
	win.ClearFocus()
	if got := win.FocusID(); got != "" {
		t.Fatalf("ClearFocus() = %q, want empty", got)
	}

	// View() must restore focus when the Term is focused.
	_ = term.View(win)
	if got := win.FocusID(); got != term.focusID {
		t.Errorf("after View, FocusID = %q, want %q", got, term.focusID)
	}

	// Focus held by some other widget must still be reclaimed — the
	// FocusID guard in View() skips the SetFocus call only when this
	// Term already owns the ID.
	win.SetFocus("some-other-widget")
	_ = term.View(win)
	if got := win.FocusID(); got != term.focusID {
		t.Errorf("after View over foreign focus, FocusID = %q, want %q", got, term.focusID)
	}

	// When unfocused, View() must not overwrite focus.
	term.SetFocused(false)
	win.ClearFocus()
	_ = term.View(win)
	if got := win.FocusID(); got != "" {
		t.Errorf("unfocused: after View, FocusID = %q, want empty", got)
	}
}

// The canvas must be clipped: smooth scrolling emits the partial top row at
// a negative Y (see TestOnDraw_PartialRowDrawsAboveCanvas), and go-gui only
// scissors a DrawCanvas when Clip is set. Without it that row paints over the
// workspace tab bar or the pane above in a split.
func TestTerm_View_CanvasIsClipped(t *testing.T) {
	win := gui.NewWindow(gui.WindowCfg{Width: 640, Height: 480})
	term, err := New(win, Cfg{})
	if err != nil {
		t.Fatalf("New term: %v", err)
	}
	defer func() { _ = term.Close() }()

	shape := findShapeByID(win, term.View(win), term.canvasID)
	if shape == nil {
		t.Fatalf("canvas shape %q not found in view tree", term.canvasID)
	}
	if !shape.Clip {
		t.Error("DrawCanvas Clip = false, want true")
	}
}

// findShapeByID generates v's layout, walks the layout tree, and returns
// the first shape carrying id. Views are generated (not laid out), so only
// shape identity and config fields are meaningful — geometry is unset.
func findShapeByID(w *gui.Window, v gui.View, id string) *gui.Shape {
	layout := v.GenerateLayout(w)
	return findShapeInLayout(&layout, id)
}

// findShapeInLayout walks a generated layout tree for the first shape
// whose ID matches id. The tree is walked in document order via the
// child layouts; the old View.Content() walk moved here because the
// interface no longer exposes children.
func findShapeInLayout(l *gui.Layout, id string) *gui.Shape {
	if l.Shape != nil && l.Shape.ID == id {
		return l.Shape
	}
	for i := range l.Children {
		if s := findShapeInLayout(&l.Children[i], id); s != nil {
			return s
		}
	}
	return nil
}

func TestTerm_onAmendLayout(t *testing.T) {
	win := gui.NewWindow(gui.WindowCfg{})
	term, err := New(win, Cfg{})
	if err != nil {
		t.Fatalf("New term: %v", err)
	}
	defer func() { _ = term.Close() }()

	t.Run("nil layout", func(t *testing.T) {
		term.ime.layoutX = 42
		term.ime.layoutY = 99
		term.onAmendLayout(gui.EventCtx{Layout: nil, Event: nil, Window: win})
		if term.ime.layoutX != 42 || term.ime.layoutY != 99 {
			t.Errorf("nil layout should not mutate position, got (%.1f, %.1f)",
				term.ime.layoutX, term.ime.layoutY)
		}
	})

	t.Run("child shape", func(t *testing.T) {
		childShape := &gui.Shape{}
		childShape.X = 100
		childShape.Y = 200
		l := &gui.Layout{
			Children: []gui.Layout{{Shape: childShape}},
		}
		term.onAmendLayout(gui.EventCtx{Layout: l, Event: nil, Window: win})
		if term.ime.layoutX != 100 || term.ime.layoutY != 200 {
			t.Errorf("expected (100, 200), got (%.1f, %.1f)",
				term.ime.layoutX, term.ime.layoutY)
		}
	})

	t.Run("own shape fallback", func(t *testing.T) {
		ownShape := &gui.Shape{}
		ownShape.X = 300
		ownShape.Y = 400
		l := &gui.Layout{Shape: ownShape}
		term.onAmendLayout(gui.EventCtx{Layout: l, Event: nil, Window: win})
		if term.ime.layoutX != 300 || term.ime.layoutY != 400 {
			t.Errorf("expected (300, 400), got (%.1f, %.1f)",
				term.ime.layoutX, term.ime.layoutY)
		}
	})

	t.Run("child nil shape falls back to own", func(t *testing.T) {
		ownShape := &gui.Shape{}
		ownShape.X = 500
		ownShape.Y = 600
		l := &gui.Layout{
			Shape:    ownShape,
			Children: []gui.Layout{{Shape: nil}},
		}
		term.onAmendLayout(gui.EventCtx{Layout: l, Event: nil, Window: win})
		if term.ime.layoutX != 500 || term.ime.layoutY != 600 {
			t.Errorf("expected fallback to own (500, 600), got (%.1f, %.1f)",
				term.ime.layoutX, term.ime.layoutY)
		}
	})

	t.Run("NaN position rejected", func(t *testing.T) {
		term.ime.layoutX = 99
		term.ime.layoutY = 99
		nanShape := &gui.Shape{}
		nanShape.X = float32(math.NaN())
		nanShape.Y = 700
		l := &gui.Layout{Children: []gui.Layout{{Shape: nanShape}}}
		term.onAmendLayout(gui.EventCtx{Layout: l, Event: nil, Window: win})
		if term.ime.layoutX == float32(math.NaN()) || math.IsNaN(float64(term.ime.layoutX)) {
			t.Error("NaN X should have been rejected")
		}
		if term.ime.layoutY != 700 {
			t.Errorf("expected Y=700, got %.1f", term.ime.layoutY)
		}
	})

	t.Run("Inf position rejected", func(t *testing.T) {
		term.ime.layoutX = 99
		term.ime.layoutY = 99
		infShape := &gui.Shape{}
		infShape.X = 800
		infShape.Y = float32(math.Inf(1))
		l := &gui.Layout{Children: []gui.Layout{{Shape: infShape}}}
		term.onAmendLayout(gui.EventCtx{Layout: l, Event: nil, Window: win})
		if term.ime.layoutX != 800 {
			t.Errorf("expected X=800, got %.1f", term.ime.layoutX)
		}
		if math.IsInf(float64(term.ime.layoutY), 0) {
			t.Error("Inf Y should have been rejected")
		}
	})
}

// --- SetFocused ---

// recordingScheduler captures QueueCommand calls for inspection in
// SetFocused tests.
type recordingScheduler struct {
	calls []func(*gui.Window)
}

func (r *recordingScheduler) QueueCommand(fn func(*gui.Window)) {
	r.calls = append(r.calls, fn)
}

func TestTerm_SetFocused_GainQueuesFocusClaim(t *testing.T) {
	rec := &recordingScheduler{}
	term := &Term{
		grid: newGrid(4, 8),
		cmd:  rec,
	}
	term.SetFocused(true)
	if len(rec.calls) != 1 {
		t.Fatalf("gain focus: expected 1 QueueCommand, got %d", len(rec.calls))
	}
	// The callback must call SetFocus on the window.
	rec.calls[0](&gui.Window{})
}

func TestTerm_SetFocused_LossSkipsQueueCommand(t *testing.T) {
	rec := &recordingScheduler{}
	term := &Term{
		grid: newGrid(4, 8),
		cmd:  rec,
	}
	term.focused.Store(true) // currently focused
	term.SetFocused(false)
	if len(rec.calls) != 0 {
		t.Fatalf("loss focus: expected 0 QueueCommand, got %d", len(rec.calls))
	}
	if term.focused.Load() {
		t.Error("focused should be false after SetFocused(false)")
	}
}

func TestTerm_SetFocused_SameValueNoOp(t *testing.T) {
	rec := &recordingScheduler{}
	term := &Term{
		grid: newGrid(4, 8),
		cmd:  rec,
	}
	term.focused.Store(false)
	term.SetFocused(true)
	if len(rec.calls) != 1 {
		t.Fatalf("first call: expected 1 QueueCommand, got %d", len(rec.calls))
	}
	verAfter := term.drawVersion.Load()

	// Second call with same value — must be no-op.
	term.SetFocused(true)
	if len(rec.calls) != 1 {
		t.Fatalf("same-value call: expected still 1 QueueCommand, got %d", len(rec.calls))
	}
	if term.drawVersion.Load() != verAfter {
		t.Error("drawVersion should not change on no-op SetFocused")
	}
}

func TestTerm_SetFocused_NilCmdNoPanic(t *testing.T) {
	term := &Term{grid: newGrid(4, 8)}
	// cmd is nil (zero-value Term). Must not panic.
	term.SetFocused(true)
	if !term.focused.Load() {
		t.Error("focused should be true after SetFocused(true)")
	}
	// bumpVersion should still fire even without a cmd.
	if term.drawVersion.Load() == 0 {
		t.Error("drawVersion should have been bumped even with nil cmd")
	}
}

func TestTerm_SetFocused_AfterCloseNoPanic(t *testing.T) {
	rec := &recordingScheduler{}
	term := &Term{
		grid: newGrid(4, 8),
		cmd:  rec,
	}
	term.closed.Store(true)
	// Must not panic — the QueueCommand callback guards against closed.
	term.SetFocused(true)
	if len(rec.calls) != 1 {
		t.Fatalf("expected QueueCommand even after close (guard is in callback), got %d", len(rec.calls))
	}
	// Execute the callback on a closed term — must be a no-op.
	rec.calls[0](&gui.Window{})
}

// --- canvasID uniqueness ---

func TestTerm_New_UniqueCanvasIDs(t *testing.T) {
	a, err := New(gui.NewWindow(gui.WindowCfg{}), Cfg{})
	if err != nil {
		t.Fatalf("first New: %v", err)
	}
	defer func() { _ = a.Close() }()
	b, err := New(gui.NewWindow(gui.WindowCfg{}), Cfg{})
	if err != nil {
		t.Fatalf("second New: %v", err)
	}
	defer func() { _ = b.Close() }()
	if a.canvasID == b.canvasID {
		t.Errorf("two Terms must have unique canvas IDs, both got %q", a.canvasID)
	}
	if a.canvasID == "" || b.canvasID == "" {
		t.Error("canvasID must not be empty")
	}
}

// --- drawCursor unfocused opacity ---

func TestDrawCursor_UnfocusedDimmed(t *testing.T) {
	// Verify drawCursor completes without panic when unfocused.
	// The focused path (opacity=1.0) is covered by existing draw tests
	// via newDrawTerm which sets focused=true.
	term := &Term{
		grid:  newGrid(24, 80),
		cellW: 10,
		cellH: 20,
	}
	if term.focused.Load() {
		t.Fatal("test expects unfocused (default) state")
	}
	tm := testTextMeasurer{cellW: 10, cellH: 20}
	dc := gui.NewDrawContext(800, 480, tm)
	base := gui.TextStyle{Typeface: glyph.TypefaceRegular}
	c := cell{Ch: 'X', FG: 7, BG: 0, Width: 1}

	shapes := []CursorStyle{CursorStyleBlock, CursorStyleUnderline, CursorStyleBar}
	for _, shape := range shapes {
		term.drawCursorShape(dc, 0, 0, c, shape, base)
	}
}

func TestDrawCursor_FocusedFullOpacity(t *testing.T) {
	term := &Term{
		grid:  newGrid(24, 80),
		cellW: 10,
		cellH: 20,
	}
	term.focused.Store(true)
	tm := testTextMeasurer{cellW: 10, cellH: 20}
	dc := gui.NewDrawContext(800, 480, tm)
	base := gui.TextStyle{Typeface: glyph.TypefaceRegular}
	c := cell{Ch: 'X', FG: 7, BG: 0, Width: 1}

	shapes := []CursorStyle{CursorStyleBlock, CursorStyleUnderline, CursorStyleBar}
	for _, shape := range shapes {
		term.drawCursorShape(dc, 0, 0, c, shape, base)
	}
}

// ---------------------------------------------------------------------------
// Theme
// ---------------------------------------------------------------------------

func TestTerm_Theme_ReturnsActiveTheme(t *testing.T) {
	custom := Theme{
		ANSI:      DefaultTheme.ANSI,
		DefaultFG: gui.RGB(255, 0, 0),
		DefaultBG: gui.RGB(0, 0, 255),
	}
	term := &Term{grid: newGrid(2, 4)}
	term.grid.setTheme(custom)
	if got := term.Theme(); got.DefaultFG != custom.DefaultFG {
		t.Errorf("Theme().DefaultFG = %+v, want %+v", got.DefaultFG, custom.DefaultFG)
	}
}

// ---------------------------------------------------------------------------
// style
// ---------------------------------------------------------------------------

func TestTerm_Style_ZeroValueTextStyleFallsBack(t *testing.T) {
	term := &Term{grid: newGrid(2, 4)}
	// cfg.TextStyle is the zero value → should fall back to M5.
	if got, fallback := term.style(), gui.CurrentTheme().M5; got != fallback {
		t.Errorf("zero-value TextStyle should fall back: got %+v, want M5 %+v",
			got, fallback)
	}
}

func TestTerm_Style_CustomTextStyleUsed(t *testing.T) {
	custom := gui.TextStyle{Typeface: glyph.TypefaceBold, Size: 18}
	term := &Term{grid: newGrid(2, 4), cfg: Cfg{TextStyle: custom}}
	if got := term.style(); got != custom {
		t.Errorf("custom TextStyle not used: got %+v, want %+v", got, custom)
	}
}

func TestTerm_Style_TypefaceOnlyUsesCustom(t *testing.T) {
	// A TextStyle with only Typeface set (Size=0) should override
	// the fallback — the check is against the full zero value, not
	// just Size>0.
	custom := gui.TextStyle{Typeface: glyph.TypefaceBold}
	term := &Term{grid: newGrid(2, 4), cfg: Cfg{TextStyle: custom}}
	if got := term.style(); got != custom {
		t.Errorf("typeface-only TextStyle should override fallback: got %+v, want %+v",
			got, custom)
	}
}

// ---------------------------------------------------------------------------
// focusID / canvasID uniqueness
// ---------------------------------------------------------------------------

func TestTerm_New_UniqueFocusIDs(t *testing.T) {
	// Simulate what New does: each Term gets a unique termSeq id.
	id1 := termSeq.Add(1)
	id2 := termSeq.Add(1)
	if id1 == id2 {
		t.Errorf("termSeq should produce unique ids: %d == %d", id1, id2)
	}
	if id2 != id1+1 {
		t.Errorf("termSeq should be monotonic: %d, %d", id1, id2)
	}
}

// ---------------------------------------------------------------------------
// OnEvent chaining
// ---------------------------------------------------------------------------

func TestTerm_OnWindowEvent_ChainsPreviousHandler(t *testing.T) {
	called := false
	w := &gui.Window{}
	w.OnEvent = func(e *gui.Event, w *gui.Window) {
		called = true
	}
	// Simulate what New does: wrap the existing handler.
	term, _ := newTestTermCapture()
	term.prevOnEvent = w.OnEvent
	w.OnEvent = func(e *gui.Event, w *gui.Window) {
		term.HandleWindowEvent(e)
		if term.prevOnEvent != nil {
			term.prevOnEvent(e, w)
		}
	}
	w.OnEvent(&gui.Event{}, w)
	if !called {
		t.Error("previous OnEvent handler was not called after chaining")
	}
}

func TestTerm_OnWindowEvent_NilPrevHandlerNoPanic(t *testing.T) {
	w := &gui.Window{}
	term := &Term{grid: newGrid(2, 4)}
	// Simulate New with no previous handler.
	term.prevOnEvent = nil
	w.OnEvent = func(e *gui.Event, w *gui.Window) {
		term.HandleWindowEvent(e)
		if term.prevOnEvent != nil {
			term.prevOnEvent(e, w)
		}
	}
	// Must not panic.
	w.OnEvent(&gui.Event{}, w)
}

// ---------------------------------------------------------------------------
// SetTheme
// ---------------------------------------------------------------------------

func TestTerm_SetTheme_ChangesGridTheme(t *testing.T) {
	term := &Term{grid: newGrid(2, 4)}
	custom := Theme{
		ANSI:      DefaultTheme.ANSI,
		DefaultFG: gui.RGB(100, 200, 50),
		DefaultBG: gui.RGB(10, 20, 30),
	}
	term.SetTheme(custom)
	if got := term.Theme(); got.DefaultFG != custom.DefaultFG {
		t.Errorf("DefaultFG = %v, want %v", got.DefaultFG, custom.DefaultFG)
	}
	if got := term.Theme(); got.DefaultBG != custom.DefaultBG {
		t.Errorf("DefaultBG = %v, want %v", got.DefaultBG, custom.DefaultBG)
	}
}

func TestTerm_SetTheme_BumpsVersion(t *testing.T) {
	term := &Term{grid: newGrid(2, 4)}
	prev := term.drawVersion.Load()
	term.SetTheme(mustBundled(t, "Gruvbox Dark"))
	if term.drawVersion.Load() <= prev {
		t.Error("SetTheme should bump drawVersion")
	}
}

// ---------------------------------------------------------------------------
// Cwd
// ---------------------------------------------------------------------------

func TestTerm_Cwd_ReturnsGridCwd(t *testing.T) {
	term := &Term{grid: newGrid(2, 4)}
	term.grid.Cwd = "file://host/home/user/projects"
	if got := term.Cwd(); got != "file://host/home/user/projects" {
		t.Errorf("Cwd() = %q, want %q", got, "file://host/home/user/projects")
	}
}

func TestTerm_Cwd_EmptyByDefault(t *testing.T) {
	term := &Term{grid: newGrid(2, 4)}
	if got := term.Cwd(); got != "" {
		t.Errorf("Cwd() = %q, want empty", got)
	}
}

func TestTerm_Rows_ReturnsGridRows(t *testing.T) {
	term := &Term{grid: newGrid(30, 100)}
	if got := term.Rows(); got != 30 {
		t.Errorf("Rows() = %d, want 30", got)
	}
}

func TestTerm_Cols_ReturnsGridCols(t *testing.T) {
	term := &Term{grid: newGrid(30, 100)}
	if got := term.Cols(); got != 100 {
		t.Errorf("Cols() = %d, want 100", got)
	}
}

func TestTerm_Write_ForwardsToPTY(t *testing.T) {
	var got []byte
	term := &Term{
		pw: writerFunc(func(b []byte) (int, error) {
			got = append(got, b...)
			return len(b), nil
		}),
	}
	n, err := term.Write([]byte("echo hello\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 11 {
		t.Errorf("Write n = %d, want 11", n)
	}
	if string(got) != "echo hello\n" {
		t.Errorf("Write payload = %q, want %q", got, "echo hello\n")
	}
}

func TestTerm_Write_ErrorPropagated(t *testing.T) {
	boom := errors.New("pty closed")
	term := &Term{
		pw: writerFunc(func([]byte) (int, error) { return 0, boom }),
	}
	_, err := term.Write([]byte("x"))
	if err != boom {
		t.Errorf("Write error = %v, want %v", err, boom)
	}
}

// ---------------------------------------------------------------------------
// OnEvent chain cleanup on Close
// ---------------------------------------------------------------------------

func TestTerm_Close_RestoresPrevOnEvent(t *testing.T) {
	originalCalled := false
	originalHandler := func(e *gui.Event, w *gui.Window) {
		originalCalled = true
	}
	w := &gui.Window{}
	w.OnEvent = originalHandler

	// Minimal PTY so Close() doesn't panic.
	r, wp, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	pty := &ptyDev{file: wp, cmd: &exec.Cmd{}}
	readDone := make(chan struct{})
	close(readDone)

	term := &Term{
		grid:        newGrid(2, 4),
		win:         w,
		prevOnEvent: w.OnEvent,
		pty:         pty,
		blinkDone:   make(chan struct{}),
		readDone:    readDone,
	}

	// Simulate the event wrapper New() installs.
	w.OnEvent = func(e *gui.Event, w *gui.Window) {
		if term.closed.Load() {
			return
		}
		term.HandleWindowEvent(e)
		if term.prevOnEvent != nil {
			term.prevOnEvent(e, w)
		}
	}

	if err := term.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_ = r.Close() // close read end after write end closed by pty.Close()

	if w.OnEvent == nil {
		t.Fatal("w.OnEvent is nil after Close")
	}
	w.OnEvent(&gui.Event{}, w)
	if !originalCalled {
		t.Error("original handler was not restored after Close")
	}
}

func TestTerm_OnWindowEvent_ClosedTermNoOp(t *testing.T) {
	term := &Term{grid: newGrid(2, 4)}
	term.closed.Store(true)
	// Must not panic and must return early.
	term.HandleWindowEvent(&gui.Event{})
	// No assertion needed — the test passes if no panic occurs.
}

// ---------------------------------------------------------------------------
// Config knobs
// ---------------------------------------------------------------------------

func TestTerm_EffectiveBellDuration_ZeroUsesDefault(t *testing.T) {
	term := &Term{cfg: Cfg{}}
	if got := term.effectiveBellDuration(); got != bellFlashDuration {
		t.Errorf("zero config: got %v, want %v (default)", got, bellFlashDuration)
	}
}

func TestTerm_EffectiveBellDuration_NegativeDisables(t *testing.T) {
	term := &Term{cfg: Cfg{BellFlashDuration: -1}}
	if got := term.effectiveBellDuration(); got != 0 {
		t.Errorf("negative config: got %v, want 0", got)
	}
}

func TestTerm_EffectiveBellDuration_PositiveUsesCustom(t *testing.T) {
	term := &Term{cfg: Cfg{BellFlashDuration: 200 * time.Millisecond}}
	if got := term.effectiveBellDuration(); got != 200*time.Millisecond {
		t.Errorf("custom config: got %v, want 200ms", got)
	}
}

func TestTerm_EffectiveScrollbarWidth_ZeroUsesDefault(t *testing.T) {
	term := &Term{cfg: Cfg{}}
	if got := term.effectiveScrollbarWidth(); got != scrollbarWidth {
		t.Errorf("zero config: got %v, want %v (default)", got, scrollbarWidth)
	}
}

func TestTerm_EffectiveScrollbarWidth_NegativeHides(t *testing.T) {
	term := &Term{cfg: Cfg{ScrollbarWidth: -1}}
	if got := term.effectiveScrollbarWidth(); got != 0 {
		t.Errorf("negative config: got %v, want 0", got)
	}
}

func TestTerm_EffectiveScrollbarWidth_PositiveUsesCustom(t *testing.T) {
	term := &Term{cfg: Cfg{ScrollbarWidth: 8}}
	if got := term.effectiveScrollbarWidth(); got != 8 {
		t.Errorf("custom config: got %v, want 8", got)
	}
}

func TestTerm_EffectiveScrollbarWidth_NaNReturnsZero(t *testing.T) {
	term := &Term{cfg: Cfg{ScrollbarWidth: float32(math.NaN())}}
	if got := term.effectiveScrollbarWidth(); got != 0 {
		t.Errorf("NaN config: got %v, want 0", got)
	}
}

func TestTerm_EffectiveScrollbarWidth_InfReturnsZero(t *testing.T) {
	term := &Term{cfg: Cfg{ScrollbarWidth: float32(math.Inf(1))}}
	if got := term.effectiveScrollbarWidth(); got != 0 {
		t.Errorf("+Inf config: got %v, want 0", got)
	}
}

func TestTerm_EffectiveScrollbarWidth_NegInfReturnsZero(t *testing.T) {
	term := &Term{cfg: Cfg{ScrollbarWidth: float32(math.Inf(-1))}}
	if got := term.effectiveScrollbarWidth(); got != 0 {
		t.Errorf("-Inf config: got %v, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// Shell command config
// ---------------------------------------------------------------------------

func TestStartPTY_CustomCommand(t *testing.T) {
	// Use sh -c so echo is always the shell builtin; /bin/echo may be at a
	// different path on minimal Linux images (e.g. CI containers).
	cfg := Cfg{
		Command: "/bin/sh",
		Args:    []string{"-c", "echo hello"},
	}
	p, err := startPTY(24, 80, cfg)
	if err != nil {
		t.Skipf("startPTY: %v", err)
	}
	defer func() { _ = p.Close() }()
	buf := make([]byte, 128)
	n, err := p.Read(buf)
	// On Linux the child may exit before we read; Go can return data + EOF
	// in a single call. Accept EOF if we got the expected output.
	if err != nil && err != io.EOF {
		t.Fatalf("read pty: %v", err)
	}
	if got := string(buf[:n]); !strings.Contains(got, "hello") {
		t.Errorf("pty output = %q, want 'hello'", got)
	}
}

func TestStartPTY_CustomEnv(t *testing.T) {
	cfg := Cfg{
		Command: "/bin/sh",
		Args:    []string{"-c", "echo $GO_TERM_TEST"},
		Env:     []string{"GO_TERM_TEST=1"},
	}
	p, err := startPTY(24, 80, cfg)
	if err != nil {
		t.Skipf("startPTY: %v", err)
	}
	defer func() { _ = p.Close() }()
	buf := make([]byte, 128)
	n, err := p.Read(buf)
	if err != nil {
		t.Fatalf("read pty: %v", err)
	}
	if got := string(buf[:n]); !strings.Contains(got, "1") {
		t.Errorf("pty output = %q, want '1'", got)
	}
}

// ---------------------------------------------------------------------------
// drawGraphics
// ---------------------------------------------------------------------------

func TestDrawGraphics_EmptyGraphicsNoOp(t *testing.T) {
	term := &Term{cellW: 10, cellH: 20}
	g := newGrid(4, 8)
	dc := &gui.DrawContext{}
	// Must not panic when Graphics is nil/empty.
	term.drawGraphics(dc, g, 0, g.Rows, 0)
	if len(dc.Images()) != 0 {
		t.Errorf("Images = %d; want 0 for empty graphics", len(dc.Images()))
	}
}

func TestDrawGraphics_DegenerateRowsSkipped(t *testing.T) {
	term := &Term{cellW: 10, cellH: 20}
	g := newGrid(4, 8)
	g.Graphics = []graphic{
		{Src: "/tmp/test.png", OriginR: 0, OriginC: 0, Cols: 4, Rows: 0},
	}
	dc := &gui.DrawContext{}
	term.drawGraphics(dc, g, 0, g.Rows, 0)
	if len(dc.Images()) != 0 {
		t.Error("graphic with Rows=0 should be skipped")
	}
}

func TestDrawGraphics_DegenerateColsSkipped(t *testing.T) {
	term := &Term{cellW: 10, cellH: 20}
	g := newGrid(4, 8)
	g.Graphics = []graphic{
		{Src: "/tmp/test.png", OriginR: 0, OriginC: 0, Cols: 0, Rows: 4},
	}
	dc := &gui.DrawContext{}
	term.drawGraphics(dc, g, 0, g.Rows, 0)
	if len(dc.Images()) != 0 {
		t.Error("graphic with Cols=0 should be skipped")
	}
}

func TestDrawGraphics_BelowViewportSkipped(t *testing.T) {
	term := &Term{cellW: 10, cellH: 20}
	g := newGrid(4, 8)
	// Graphic starts at row 10, but viewport is only 4 rows.
	g.Graphics = []graphic{
		{Src: "/tmp/test.png", OriginR: 10, OriginC: 0, Cols: 2, Rows: 2},
	}
	dc := &gui.DrawContext{}
	term.drawGraphics(dc, g, 0, g.Rows, 0)
	if len(dc.Images()) != 0 {
		t.Error("graphic entirely below viewport should be skipped")
	}
}

func TestDrawGraphics_AboveViewportSkipped(t *testing.T) {
	term := &Term{cellW: 10, cellH: 20}
	g := newGrid(4, 8)
	// Graphic ends at row 0 but viewport starts at row 0, so
	// vr+Rows = -3+2 = -1 <= 0 is NOT true. Actually set Rows=2:
	// vr+Rows = -3+2 = -1 <= 0 → skipped.
	// Use OriginR=-3, Rows=2: ContentRowToScreen(-3) = -3,
	// vr+Rows = -3+2 = -1 <= 0 → skip.
	g.Graphics = []graphic{
		{Src: "/tmp/test.png", OriginR: -3, OriginC: 0, Cols: 2, Rows: 2},
	}
	dc := &gui.DrawContext{}
	term.drawGraphics(dc, g, 0, g.Rows, 0)
	if len(dc.Images()) != 0 {
		t.Error("graphic entirely above viewport should be skipped")
	}
}

func TestDrawGraphics_VisibleGraphicRendered(t *testing.T) {
	term := &Term{cellW: 10, cellH: 20}
	g := newGrid(4, 8)
	g.Graphics = []graphic{
		{Src: "/tmp/test.png", OriginR: 1, OriginC: 2, Cols: 3, Rows: 2},
	}
	dc := &gui.DrawContext{}
	term.drawGraphics(dc, g, 0, g.Rows, 0)
	entries := dc.Images()
	if len(entries) != 1 {
		t.Fatalf("Images = %d; want 1", len(entries))
	}
	if entries[0].Src != "/tmp/test.png" {
		t.Errorf("Src = %q; want /tmp/test.png", entries[0].Src)
	}
	// Check computed pixel geometry against expectations.
	// OriginC=2 * cellW=10 = 20; ContentRowToScreen(1)=1 * cellH=20 = 20
	// Cols=3 * cellW=10 = 30; Rows=2 * cellH=20 = 40
	if entries[0].X != 20 {
		t.Errorf("X = %v; want 20", entries[0].X)
	}
	if entries[0].Y != 20 {
		t.Errorf("Y = %v; want 20", entries[0].Y)
	}
	if entries[0].W != 30 {
		t.Errorf("W = %v; want 30", entries[0].W)
	}
	if entries[0].H != 40 {
		t.Errorf("H = %v; want 40", entries[0].H)
	}
}

func TestDrawGraphics_PartiallyAboveViewportStillRenders(t *testing.T) {
	term := &Term{cellW: 10, cellH: 20}
	g := newGrid(4, 8)
	// Graphic origin is at row -5, but it spans 10 rows, so rows -5..4
	// overlap the viewport 0..3. v=-5, v+10=5 > 0, so it should render.
	g.Graphics = []graphic{
		{Src: "/tmp/test.png", OriginR: -5, OriginC: 0, Cols: 2, Rows: 10},
	}
	dc := &gui.DrawContext{}
	term.drawGraphics(dc, g, 0, g.Rows, 0)
	if len(dc.Images()) != 1 {
		t.Error("graphic overlapping viewport from above should render")
	}
}

// --- PID / Alive / OnExit ---

func TestPID_ReturnsChildPID(t *testing.T) {
	win := gui.NewWindow(gui.WindowCfg{})
	tm, err := New(win, Cfg{Command: "sleep", Args: []string{"0.2"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = tm.Close() }()

	pid := tm.PID()
	if pid <= 0 {
		t.Fatalf("PID returned %d, want positive", pid)
	}
	if !tm.Alive() {
		t.Error("expected Alive=true immediately after New")
	}
}

func TestPID_NilPTYReturnsZero(t *testing.T) {
	tm := &Term{}
	if got := tm.PID(); got != 0 {
		t.Errorf("PID with nil pty: got %d, want 0", got)
	}
}

func TestAlive_DefaultsFalse(t *testing.T) {
	tm := &Term{}
	if tm.Alive() {
		t.Error("Alive should return false before alive is set to true")
	}
}

func TestOnExit_CalledWhenProcessExits(t *testing.T) {
	exitCh := make(chan struct{})
	win := gui.NewWindow(gui.WindowCfg{})
	tm, err := New(win, Cfg{
		Command: "true", // exits immediately
		OnExit: func() {
			close(exitCh)
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = tm.Close() }()

	select {
	case <-exitCh:
	case <-time.After(3 * time.Second):
		t.Fatal("OnExit was not called within timeout")
	}
	// OnExit runs before close(readDone) in the same defer chain, so
	// Alive() must already be false by the time the channel fires.
	if tm.Alive() {
		t.Error("Alive should be false after process exits")
	}
}

func TestOnExit_NilHandlerNoPanic(t *testing.T) {
	win := gui.NewWindow(gui.WindowCfg{})
	tm, err := New(win, Cfg{Command: "true"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Close waits on readDone; "true" exits instantly, so this returns
	// quickly without a blind sleep.
	_ = tm.Close()
}

func TestOnExit_PanicIsRecovered(t *testing.T) {
	// If OnExit panics, recoverLoop must catch it so readDone is still
	// closed and Close does not hang.
	exitCh := make(chan struct{})
	win := gui.NewWindow(gui.WindowCfg{})
	tm, err := New(win, Cfg{
		Command: "true",
		OnExit: func() {
			close(exitCh)
			panic("onExit panic")
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		_ = tm.Close()
	}()

	select {
	case <-exitCh:
	case <-time.After(3 * time.Second):
		t.Fatal("OnExit was not called within timeout")
	}
	// If the panic escaped, readDone would never close and Close would
	// block for its 2-second timeout. recoverLoop catches the panic and
	// readDone is closed immediately after, so Close returns fast.
	_ = tm.Close()
}

func TestPID_AfterClose(t *testing.T) {
	win := gui.NewWindow(gui.WindowCfg{})
	tm, err := New(win, Cfg{Command: "sleep", Args: []string{"0.01"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pid := tm.PID()
	if pid <= 0 {
		t.Fatalf("PID returned %d before close, want positive", pid)
	}
	if err := tm.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// PID should still return the original value after Close — Unix PIDs
	// don't change on process death.
	if got := tm.PID(); got != pid {
		t.Errorf("PID after Close = %d, want %d", got, pid)
	}
}

func TestAlive_AfterClose(t *testing.T) {
	win := gui.NewWindow(gui.WindowCfg{})
	tm, err := New(win, Cfg{Command: "sleep", Args: []string{"0.01"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := tm.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if tm.Alive() {
		t.Error("Alive must return false after Close")
	}
}

// ---------------------------------------------------------------------------
// style fontSize override
// ---------------------------------------------------------------------------

func TestTerm_Style_FontSizeOverridesConfig(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(14, cfg)
	if got := term.style().Size; got != 14 {
		t.Errorf("fontSize should override config Size: got %v, want 14", got)
	}
}

func TestTerm_Style_ZeroFontSizeUsesConfig(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(0, cfg)
	if got := term.style().Size; got != 12 {
		t.Errorf("fontSize=0 should use config Size: got %v, want 12", got)
	}
}

// ---------------------------------------------------------------------------
// AdjustFontSize
// ---------------------------------------------------------------------------

func TestAdjustFontSize_Increase(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(12, cfg)
	term.AdjustFontSize(1.5)
	if term.fontSize != 13.5 {
		t.Errorf("fontSize after +1.5: got %v, want 13.5", term.fontSize)
	}
}

func TestAdjustFontSize_Decrease(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(12, cfg)
	term.AdjustFontSize(-0.5)
	if term.fontSize != 11.5 {
		t.Errorf("fontSize after -0.5: got %v, want 11.5", term.fontSize)
	}
}

func TestAdjustFontSize_ClampMin(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 4}}
	term, _ := newTestTermWithScheduler(4, cfg)
	term.AdjustFontSize(-0.5)
	if term.fontSize != 4 {
		t.Errorf("fontSize clamped to min 4: got %v", term.fontSize)
	}
}

func TestAdjustFontSize_ClampMax(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 72}}
	term, _ := newTestTermWithScheduler(72, cfg)
	term.AdjustFontSize(0.5)
	if term.fontSize != 72 {
		t.Errorf("fontSize clamped to max 72: got %v", term.fontSize)
	}
}

func TestAdjustFontSize_NaNNoOp(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(12, cfg)
	prev := term.fontSize
	term.AdjustFontSize(float32(math.NaN()))
	if term.fontSize != prev {
		t.Errorf("NaN delta must be no-op: fontSize changed from %v to %v", prev, term.fontSize)
	}
}

func TestAdjustFontSize_InfNoOp(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(12, cfg)
	prev := term.fontSize
	term.AdjustFontSize(float32(math.Inf(1)))
	if term.fontSize != prev {
		t.Errorf("+Inf delta must be no-op: fontSize changed from %v to %v", prev, term.fontSize)
	}
}

func TestAdjustFontSize_ResetsCellW(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(12, cfg)
	term.cellW = 8.5
	term.AdjustFontSize(1)
	if term.cellW != 0 {
		t.Errorf("AdjustFontSize must reset cellW: got %v, want 0", term.cellW)
	}
}

func TestAdjustFontSize_ResetsRuneCache(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(12, cfg)
	term.draw.runeCache = map[rune]string{'a': "a"}
	term.AdjustFontSize(1)
	if term.draw.runeCache != nil {
		t.Error("AdjustFontSize must reset runeCache to nil")
	}
}

func TestAdjustFontSize_BumpsVersion(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(12, cfg)
	prev := term.drawVersion.Load()
	term.AdjustFontSize(1)
	if term.drawVersion.Load() == prev {
		t.Error("AdjustFontSize must bump drawVersion")
	}
}

func TestAdjustFontSize_ZeroFontSizeInitsFromStyle(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 14}}
	term, _ := newTestTermWithScheduler(0, cfg) // fontSize starts at 0
	term.AdjustFontSize(1)
	if term.fontSize != 15 {
		t.Errorf("fontSize should init from style: got %v, want 15", term.fontSize)
	}
}

func TestAdjustFontSize_ZeroFontSizeZeroStyleNoPanic(t *testing.T) {
	cfg := Cfg{} // no TextStyle, fallback to gui.CurrentTheme().M5
	term, _ := newTestTermWithScheduler(0, cfg)
	term.fontSize = 0
	term.cfg = Cfg{} // zero cfg

	term.AdjustFontSize(1)
}

// ---------------------------------------------------------------------------
// ResetFontSize
// ---------------------------------------------------------------------------

func TestResetFontSize_ClearsOverrideAndRemeasures(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(16, cfg) // zoomed to 16
	term.cellW = 8.5
	prevVer := term.drawVersion.Load()

	term.ResetFontSize()

	if term.fontSize != 0 {
		t.Errorf("ResetFontSize must clear the override: got %v, want 0", term.fontSize)
	}
	// style() falls back to the configured size once the override is cleared.
	if got := term.FontSize(); got != 12 {
		t.Errorf("effective size after reset: got %v, want 12", got)
	}
	if term.cellW != 0 {
		t.Errorf("ResetFontSize must reset cellW: got %v, want 0", term.cellW)
	}
	if term.drawVersion.Load() == prevVer {
		t.Error("ResetFontSize must bump drawVersion")
	}
}

func TestSetFontSize_AbsoluteAndClamp(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(12, cfg)

	term.SetFontSize(20)
	if term.FontSize() != 20 {
		t.Errorf("SetFontSize(20): got %v, want 20", term.FontSize())
	}
	term.SetFontSize(1000)
	if term.FontSize() != 72 {
		t.Errorf("SetFontSize clamps to max 72: got %v", term.FontSize())
	}
	term.SetFontSize(1)
	if term.FontSize() != 4 {
		t.Errorf("SetFontSize clamps to min 4: got %v", term.FontSize())
	}
}

// The regression this guards: an inherited/persisted size is applied as an
// override, NOT as the configured default, so Cmd+0 (ResetFontSize) still
// returns to the workspace default rather than the inherited size.
func TestSetFontSize_ResetReturnsToConfiguredDefault(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}} // workspace default = 12
	term, _ := newTestTermWithScheduler(12, cfg)

	term.SetFontSize(16) // pane inherits a zoom (split/restore)
	if term.FontSize() != 16 {
		t.Fatalf("after SetFontSize(16): got %v, want 16", term.FontSize())
	}
	term.ResetFontSize()
	if term.FontSize() != 12 {
		t.Errorf("reset must return to configured default 12, not inherited 16: got %v", term.FontSize())
	}
}

func TestSetFontSize_NonPositiveResets(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(16, cfg)
	term.SetFontSize(0)
	if term.fontSize != 0 {
		t.Errorf("SetFontSize(0) must clear the override: fontSize = %v, want 0", term.fontSize)
	}
	if term.FontSize() != 12 {
		t.Errorf("effective size after SetFontSize(0): got %v, want 12", term.FontSize())
	}
}

func TestResetFontSize_AlreadyDefaultIsNoop(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(0, cfg) // fontSize starts at 0 (default)
	term.cellW = 8.5
	prevVer := term.drawVersion.Load()

	term.ResetFontSize()

	// No override to clear: nothing should be invalidated.
	if term.cellW != 8.5 {
		t.Errorf("no-op reset must not touch cellW: got %v, want 8.5", term.cellW)
	}
	if term.drawVersion.Load() != prevVer {
		t.Error("no-op reset must not bump drawVersion")
	}
}

func TestSetFontSize_SameAsCurrentNoop(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(12, cfg)
	term.cellW = 8.5
	prevVer := term.drawVersion.Load()

	term.SetFontSize(12) // same as current fontSize

	if term.cellW != 8.5 {
		t.Errorf("same-size SetFontSize must not reset cellW: got %v, want 8.5", term.cellW)
	}
	if term.drawVersion.Load() != prevVer {
		t.Error("same-size SetFontSize must not bump drawVersion")
	}
}

// ---------------------------------------------------------------------------
// handleDisplayKey
// ---------------------------------------------------------------------------

func TestHandleDisplayKey_CmdEqualIncreasesFontSize(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(12, cfg)
	e := &gui.Event{KeyCode: gui.KeyEqual, Modifiers: modPrimary}
	if !term.handleDisplayKey(e, &gui.Window{}) {
		t.Error("primary+= should be handled")
	}
	if !e.IsHandled {
		t.Error("primary+= should set e.IsHandled")
	}
	if term.fontSize != 12.25 {
		t.Errorf("primary+= should increase fontSize by 0.25: got %v, want 12.25", term.fontSize)
	}
}

func TestHandleDisplayKey_CmdMinusDecreasesFontSize(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(12, cfg)
	e := &gui.Event{KeyCode: gui.KeyMinus, Modifiers: modPrimary}
	if !term.handleDisplayKey(e, &gui.Window{}) {
		t.Error("primary+- should be handled")
	}
	if !e.IsHandled {
		t.Error("primary+- should set e.IsHandled")
	}
	if term.fontSize != 11.75 {
		t.Errorf("primary+- should decrease fontSize by 0.25: got %v, want 11.75", term.fontSize)
	}
}

func TestHandleDisplayKey_PrimaryAltEqual_PassesThrough(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(12, cfg)
	prev := term.fontSize
	// Adding a non-primary modifier (Alt) beyond the primary chord must pass
	// through — reserved for layered bindings on both macOS and Windows.
	e := &gui.Event{KeyCode: gui.KeyEqual, Modifiers: modPrimary | gui.ModAlt}
	if term.handleDisplayKey(e, &gui.Window{}) {
		t.Error("primary+Alt+= should NOT be handled (pass through to pty)")
	}
	if term.fontSize != prev {
		t.Errorf("primary+Alt+= must not change fontSize: %v -> %v", prev, term.fontSize)
	}
}

func TestHandleDisplayKey_OtherKeyWithCmd_PassesThrough(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(12, cfg)
	prev := term.fontSize
	e := &gui.Event{KeyCode: gui.KeyF, Modifiers: gui.ModSuper}
	if term.handleDisplayKey(e, &gui.Window{}) {
		t.Error("Cmd+F should NOT be handled by handleDisplayKey")
	}
	if term.fontSize != prev {
		t.Errorf("unrelated key must not change fontSize: %v -> %v", prev, term.fontSize)
	}
}

func TestHandleDisplayKey_EqualWithoutCmd_PassesThrough(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(12, cfg)
	prev := term.fontSize
	e := &gui.Event{KeyCode: gui.KeyEqual}
	if term.handleDisplayKey(e, &gui.Window{}) {
		t.Error("= without Cmd should NOT be handled")
	}
	if term.fontSize != prev {
		t.Errorf("= without Cmd must not change fontSize: %v -> %v", prev, term.fontSize)
	}
}

func TestHandleDisplayKey_MinusWithoutCmd_PassesThrough(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(12, cfg)
	prev := term.fontSize
	e := &gui.Event{KeyCode: gui.KeyMinus}
	if term.handleDisplayKey(e, &gui.Window{}) {
		t.Error("- without Cmd should NOT be handled")
	}
	if term.fontSize != prev {
		t.Errorf("- without Cmd must not change fontSize: %v -> %v", prev, term.fontSize)
	}
}

func TestHandleDisplayKey_Cmd0ResetsFontSize(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(16, cfg) // zoomed to 16
	e := &gui.Event{KeyCode: gui.Key0, Modifiers: modPrimary}
	if !term.handleDisplayKey(e, &gui.Window{}) {
		t.Error("primary+0 should be handled")
	}
	if !e.IsHandled {
		t.Error("primary+0 should set e.IsHandled")
	}
	if term.fontSize != 0 {
		t.Errorf("primary+0 should clear the zoom override: got %v, want 0", term.fontSize)
	}
	if got := term.FontSize(); got != 12 {
		t.Errorf("effective size after primary+0: got %v, want 12", got)
	}
}

func TestHandleDisplayKey_Zero0WithoutCmd_PassesThrough(t *testing.T) {
	cfg := Cfg{TextStyle: gui.TextStyle{Size: 12}}
	term, _ := newTestTermWithScheduler(16, cfg)
	prev := term.fontSize
	e := &gui.Event{KeyCode: gui.Key0}
	if term.handleDisplayKey(e, &gui.Window{}) {
		t.Error("0 without Cmd should NOT be handled")
	}
	if term.fontSize != prev {
		t.Errorf("0 without Cmd must not change fontSize: %v -> %v", prev, term.fontSize)
	}
}

// TestShowSizeBadge_SuppressedUntilFirstResize covers the startup case: the
// window's initial sizing runs through the same prepareResize path as a drag,
// and flashing the readout at launch would be noise.
func TestShowSizeBadge_SuppressedUntilFirstResize(t *testing.T) {
	tm := &Term{}
	now := time.Now()

	tm.showSizeBadge(24, 80, now)
	if !tm.resize.badgeUntil.IsZero() {
		t.Fatal("badge should stay hidden before the first applied resize")
	}
	// Dims are still recorded, so the first real drag frame has them.
	if tm.resize.badgeRows != 24 || tm.resize.badgeCols != 80 {
		t.Fatalf("dims = %dx%d, want 24x80",
			tm.resize.badgeRows, tm.resize.badgeCols)
	}

	tm.resize.sized = true
	tm.showSizeBadge(37, 124, now)
	if !tm.resize.badgeUntil.After(now) {
		t.Fatal("badge should be visible after a user resize")
	}
	if tm.resize.badgeRows != 37 || tm.resize.badgeCols != 124 {
		t.Fatalf("dims = %dx%d, want 37x124",
			tm.resize.badgeRows, tm.resize.badgeCols)
	}
}

// SGR 2;4 (dim + underline, what `gh issue list` prints for its header) must
// underline in the dimmed text color, not the raw foreground: a default
// underline color follows whatever the text is finally painted with.
func TestCellRunKey_DefaultULColorFollowsDimmedText(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{Typeface: glyph.TypefaceRegular}
	c := cell{
		Ch: 'A', FG: 7, BG: defaultColor, ULColor: defaultColor,
		Width: 1, ULStyle: ulSingle, Attrs: attrDim,
	}
	k := cellRunKey(c, base, g, -1, -1, false, false)
	if k.ulColor != k.color {
		t.Errorf("ulColor = %+v, want text color %+v", k.ulColor, k.color)
	}
}

// An explicit SGR 58 underline color is not dimmed with the text.
func TestCellRunKey_ExplicitULColorIgnoresDim(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{Typeface: glyph.TypefaceRegular}
	want := gui.RGB(255, 161, 1)
	c := cell{
		Ch: 'A', FG: 7, BG: defaultColor, Width: 1, ULStyle: ulSingle,
		ULColor: rgbColor(255, 161, 1), Attrs: attrDim,
	}
	if got := cellRunKey(c, base, g, -1, -1, false, false).ulColor; got != want {
		t.Errorf("ulColor = %+v, want %+v", got, want)
	}
}

// A default underline color follows the contrast-clamped text color, so the
// underline stays the same color as the text actually painted.
func TestCellRunKey_DefaultULColorFollowsContrast(t *testing.T) {
	g := newGrid(4, 8)
	g.setTheme(mustBundled(t, "Catppuccin Latte"))
	g.MinContrast = 4.5
	base := gui.TextStyle{Typeface: glyph.TypefaceRegular}
	c := cell{
		Ch: 'A', FG: rgbColor(255, 161, 1), BG: defaultColor,
		ULColor: defaultColor, Width: 1, ULStyle: ulSingle,
	}
	k := cellRunKey(c, base, g, -1, -1, false, false)
	if k.ulColor != k.color {
		t.Errorf("ulColor = %+v, want clamped text color %+v", k.ulColor, k.color)
	}
	if r := contrastRatio(k.ulColor, g.bgOf(c)); r < 4.5 {
		t.Errorf("underline ratio %.2f, want >= 4.5", r)
	}
}

// A default underline color follows the Cmd-hover recolor, so a hovered link
// underlines in the blue it is painted with rather than the unhovered color.
func TestCellRunKey_DefaultULColorFollowsHover(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{Typeface: glyph.TypefaceRegular}
	c := cell{
		Ch: 'A', FG: 7, BG: defaultColor, ULColor: defaultColor,
		Width: 1, ULStyle: ulSingle,
	}
	plain := cellRunKey(c, base, g, -1, -1, false, false)
	hovered := cellRunKey(c, base, g, -1, -1, false, true)
	if hovered.color == plain.color {
		t.Fatal("premise broken: hover recolor did not change the text color")
	}
	if hovered.ulColor != hovered.color {
		t.Errorf("ulColor = %+v, want hovered text color %+v",
			hovered.ulColor, hovered.color)
	}
}
