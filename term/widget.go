package term

import (
	"log"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/mike-ward/go-gui/gui"
)

// Bracketed-paste markers (DEC ?2004). Sent around clipboard payloads
// when the application has enabled the mode; stripped from incoming
// payloads unconditionally so a clipboard exit-marker can't break out.
const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// realNumber reports whether f is non-NaN and non-Inf. Used for inputs
// (mouse coords, scroll deltas) where zero and negative are legal.
func realNumber(f float32) bool {
	x := float64(f)
	return !math.IsNaN(x) && !math.IsInf(x, 0)
}

// finite reports whether f is a usable, positive cell metric. Rejects
// NaN, Inf, and non-positive values which would otherwise produce
// garbage row/col counts in OnDraw.
func finite(f float32) bool { return realNumber(f) && f > 0 }

// linesFromScroll converts a wheel/trackpad pixel delta into a row
// count using cellH. Returns 0 for unusable inputs (non-finite cellH,
// non-real scrollY, or no movement). Sub-cell deltas round up to a
// single line in their direction so trackpad nudges aren't lost.
func linesFromScroll(scrollY, cellH float32) int {
	if !finite(cellH) {
		return 0
	}
	if !realNumber(scrollY) {
		return 0
	}
	lines := int(scrollY / cellH)
	if lines != 0 {
		return lines
	}
	switch {
	case scrollY > 0:
		return 1
	case scrollY < 0:
		return -1
	}
	return 0
}

// truncatePaste caps s at max bytes, backing up to the start of any
// trailing partial UTF-8 sequence so the PTY never receives a split
// rune. Returns s unchanged when already within budget.
func truncatePaste(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// mouseSGRBaseButton maps a go-gui MouseButton to its SGR (?1006) base
// code. Returns false for unsupported buttons (e.g. MouseInvalid),
// signaling "do not report".
func mouseSGRBaseButton(b gui.MouseButton) (int, bool) {
	switch b {
	case gui.MouseLeft:
		return 0, true
	case gui.MouseMiddle:
		return 1, true
	case gui.MouseRight:
		return 2, true
	}
	return 0, false
}

// mouseModBits encodes shift/alt/ctrl modifier bits into the xterm
// mouse-button byte. Values from xterm ctlseqs: shift=4, alt/meta=8,
// ctrl=16. Super/Cmd has no standard mapping and is ignored.
func mouseModBits(m gui.Modifier) int {
	bits := 0
	if m.Has(gui.ModShift) {
		bits += 4
	}
	if m.Has(gui.ModAlt) {
		bits += 8
	}
	if m.Has(gui.ModCtrl) {
		bits += 16
	}
	return bits
}

// encodeMouseSGR appends an SGR-1006 mouse report to buf:
// "\x1b[<{cb};{col};{row}{M|m}". Coordinates are converted to 1-based
// per spec. press=true emits 'M' (press / motion / wheel-tick);
// press=false emits 'm' (release).
func encodeMouseSGR(buf []byte, cb, col, row int, press bool) []byte {
	final := byte('M')
	if !press {
		final = 'm'
	}
	buf = append(buf, '\x1b', '[', '<')
	buf = strconv.AppendInt(buf, int64(cb), 10)
	buf = append(buf, ';')
	buf = strconv.AppendInt(buf, int64(col+1), 10)
	buf = append(buf, ';')
	buf = strconv.AppendInt(buf, int64(row+1), 10)
	buf = append(buf, final)
	return buf
}

// asciiStr caches single-rune strings for runes 0..127 to avoid the
// per-cell allocation that string(rune) incurs in the OnDraw hot path.
var asciiStr = func() [128]string {
	var a [128]string
	for i := range a {
		a[i] = string(rune(i))
	}
	return a
}()

// runeString returns a string for r without allocating in the ASCII case.
func runeString(r rune) string {
	if uint32(r) < 128 {
		return asciiStr[r]
	}
	return string(r)
}

// Cfg configures a Term widget. All fields optional.
type Cfg struct {
	// TextStyle overrides the default monospace style. Zero value
	// uses gui.CurrentTheme().M5.
	TextStyle gui.TextStyle

	// ScrollbackRows caps the scrollback ring buffer. Zero uses the
	// default (defaultScrollbackRows). Negative disables scrollback.
	ScrollbackRows int

	// OnTitle, if non-nil, receives OSC 0/1/2 window-title updates on
	// the main goroutine (delivered via Window.QueueCommand). When
	// nil, the widget calls win.SetTitle directly. Embedders set this
	// to wrap the title in app-specific framing.
	OnTitle func(string)

	// CursorBlink, if non-nil, overrides the application's DECSCUSR
	// blink request. Use *true to force blinking on, *false to force
	// steady. Leave nil to honor whatever the shell asks for (default
	// blink for a brand-new grid).
	CursorBlink *bool
}

// cursorBlinkPeriod is the half-cycle duration: cursor visible for
// blinkPeriod, then hidden for blinkPeriod. 500 ms matches xterm
// defaults.
const cursorBlinkPeriod = 500 * time.Millisecond

// defaultScrollbackRows is the cap applied when Cfg.ScrollbackRows == 0.
const defaultScrollbackRows = 5000

// maxPasteBytes caps clipboard payloads written to the PTY. Multi-MB
// pastes can wedge the shell and stall the reader goroutine; truncate
// silently — nothing useful types thousands of lines at once.
const maxPasteBytes = 1 << 20

// Term is a terminal-emulator widget bound to a single PTY-backed shell.
// Use New to construct, View to embed in a layout, Close to tear down.
type Term struct {
	cfg    Cfg
	grid   *Grid
	parser *Parser
	pty    *PTY
	win    *gui.Window

	// Cell metrics measured on first draw and reused thereafter. Both
	// zero until the first OnDraw.
	cellW, cellH float32

	// dragging tracks the button-held state set in onClick, extended
	// in onMouseMove, finalized in onMouseUp. Used both for local
	// selection drag and host-side drag reports — distinguished by
	// dragReport.
	dragging   bool
	dragButton gui.MouseButton
	dragReport bool // true when this drag is being reported to the PTY

	// lastMouseR/C dedupe motion reports under ?1003 so a still
	// pointer doesn't flood the PTY with identical coordinates each
	// frame. Set to (-1, -1) when no prior report.
	lastMouseR int
	lastMouseC int

	// closed guards Close so multiple calls are safe.
	closed atomic.Bool

	// cursorEpoch is the reference time for blink-phase calculation.
	// Set in New so the cursor starts in the "on" half-cycle.
	cursorEpoch time.Time

	// blinkDone signals the blink ticker goroutine to exit. Closed by
	// Close.
	blinkDone chan struct{}
}

// New starts a shell in a PTY and returns a Term widget. The reader
// goroutine is spawned immediately; subsequent PTY output schedules a
// redraw via win.QueueCommand.
func New(w *gui.Window, cfg Cfg) (*Term, error) {
	const initRows, initCols = 24, 80
	pty, err := Start(initRows, initCols)
	if err != nil {
		return nil, err
	}
	g := NewGrid(initRows, initCols)
	switch {
	case cfg.ScrollbackRows == 0:
		g.ScrollbackCap = defaultScrollbackRows
	case cfg.ScrollbackRows > 0:
		g.ScrollbackCap = clampScrollback(cfg.ScrollbackRows)
	default:
		// Negative: leave ScrollbackCap = 0 (scrollback disabled).
	}
	t := &Term{
		cfg:         cfg,
		grid:        g,
		parser:      NewParser(g),
		pty:         pty,
		win:         w,
		lastMouseR:  -1,
		lastMouseC:  -1,
		cursorEpoch: time.Now(),
		blinkDone:   make(chan struct{}),
	}
	t.parser.SetTitleHandler(t.onParserTitle)
	t.parser.SetReplyHandler(t.onParserReply)
	w.SetIDFocus(focusID)
	go t.readLoop()
	go t.blinkLoop()
	return t, nil
}

// blinkLoop wakes every cursorBlinkPeriod and forces a redraw when the
// cursor is currently blinking + visible at the live viewport. Other
// states (steady cursor, scrolled-back view, hidden cursor) need no
// periodic redraw and the loop simply skips.
func (t *Term) blinkLoop() {
	tk := time.NewTicker(cursorBlinkPeriod)
	defer tk.Stop()
	for {
		select {
		case <-t.blinkDone:
			return
		case <-tk.C:
			t.grid.Mu.Lock()
			redraw := t.grid.CursorVisible &&
				t.grid.ViewOffset == 0 &&
				t.cursorBlinks()
			t.grid.Mu.Unlock()
			if redraw {
				t.win.QueueCommand(func(w *gui.Window) {
					w.UpdateWindow()
				})
			}
		}
	}
}

// cursorBlinks reports whether the cursor should currently blink,
// honoring the Cfg.CursorBlink override over the grid's DECSCUSR
// state. Caller holds Grid.Mu.
func (t *Term) cursorBlinks() bool {
	if t.cfg.CursorBlink != nil {
		return *t.cfg.CursorBlink
	}
	return t.grid.CursorBlink
}

// onParserTitle is the OSC 0/1/2 handler. Runs on the reader goroutine
// while Grid.Mu is held — must not touch *gui.Window state directly,
// hence the QueueCommand hop.
func (t *Term) onParserTitle(title string) {
	fn := t.cfg.OnTitle
	t.win.QueueCommand(func(w *gui.Window) {
		if fn != nil {
			fn(title)
			return
		}
		w.SetTitle(title)
	})
}

// onParserReply writes parser-originated bytes (e.g. DA1 reply) back
// to the PTY. Called under Grid.Mu; pty.Write is independent of that
// lock so this is safe.
func (t *Term) onParserReply(b []byte) {
	if _, err := t.pty.Write(b); err != nil {
		log.Printf("term: pty reply: %v", err)
	}
}

// Cwd returns the most recent working directory reported via OSC 7,
// or "" if the shell has never emitted one. Typical payload format
// is "file://host/path"; embedders parse as needed.
func (t *Term) Cwd() string {
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	return t.grid.Cwd
}

// focusID is the IDFocus value claimed by the terminal container.
const focusID uint32 = 1

// View returns the go-gui view tree for this terminal. Usable as a
// gui.Window UpdateView generator: w.UpdateView(t.View).
func (t *Term) View(w *gui.Window) gui.View {
	ww, wh := w.WindowSize()
	return gui.Column(gui.ContainerCfg{
		Width:     float32(ww),
		Height:    float32(wh),
		Sizing:    gui.FixedFixed,
		Padding:   gui.Some(gui.Padding{}),
		Spacing:   gui.SomeF(0),
		Color:     defaultBG,
		IDFocus:   focusID,
		OnChar:    t.onChar,
		OnKeyDown: t.onKeyDown,
		Content: []gui.View{
			gui.DrawCanvas(gui.DrawCanvasCfg{
				Sizing:        gui.FillFill,
				OnDraw:        t.onDraw,
				OnMouseScroll: t.onMouseScroll,
				OnClick:       t.onClick,
				OnMouseMove:   t.onMouseMove,
				OnMouseUp:     t.onMouseUp,
			}),
		},
	})
}

// Close stops the shell, reader, and blink goroutine. Safe to call
// once; subsequent calls are no-ops.
func (t *Term) Close() error {
	if t.closed.Swap(true) {
		return nil
	}
	close(t.blinkDone)
	return t.pty.Close()
}

// readLoop forwards PTY output through the parser and schedules a
// render. Exits when the PTY is closed or returns EOF.
func (t *Term) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := t.pty.Read(buf)
		if n > 0 {
			t.grid.Mu.Lock()
			t.parser.Feed(buf[:n])
			t.grid.Mu.Unlock()
			t.win.QueueCommand(func(w *gui.Window) {
				w.UpdateWindow()
			})
		}
		if err != nil {
			return
		}
	}
}

// style returns the resolved text style for this terminal.
func (t *Term) style() gui.TextStyle {
	if t.cfg.TextStyle.Size > 0 {
		return t.cfg.TextStyle
	}
	return gui.CurrentTheme().M5
}

// onDraw is the DrawCanvas callback. Measures cell size on first call,
// reflows the grid + PTY when the canvas size changes, then paints the
// grid as a sequence of background rects + per-cell text + cursor.
func (t *Term) onDraw(dc *gui.DrawContext) {
	style := t.style()
	if t.cellW == 0 {
		t.cellW = dc.TextWidth("M", style)
		t.cellH = dc.FontHeight(style)
	}
	if !finite(t.cellW) || !finite(t.cellH) {
		return
	}
	if !finite(dc.Width) || !finite(dc.Height) {
		return
	}
	cols := clampDim(int(dc.Width / t.cellW))
	rows := clampDim(int(dc.Height / t.cellH))

	t.grid.Mu.Lock()
	if rows != t.grid.Rows || cols != t.grid.Cols {
		t.grid.Resize(rows, cols)
		if err := t.pty.Resize(rows, cols); err != nil {
			log.Printf("term: pty resize: %v", err)
		}
	}

	// Fast path: live viewport with no selection reads directly from
	// the cell buffer, skipping ViewOffset / scrollback branches and
	// per-cell InSelection work.
	g := t.grid
	rows, cols = g.Rows, g.Cols
	live := g.ViewOffset == 0 && !g.SelActive
	cells := g.Cells
	resolveCell := func(r, c int) Cell {
		if live {
			return cells[r*cols+c]
		}
		cell := g.ViewCellAt(r, c)
		if g.InSelection(r, c) {
			cell.Attrs ^= AttrInverse
		}
		return cell
	}

	// Background pass: coalesce runs of equal bg color per row.
	for r := range rows {
		runStart := 0
		runColor := bg(resolveCell(r, 0))
		for c := 1; c < cols; c++ {
			cur := bg(resolveCell(r, c))
			if cur != runColor {
				t.fillRun(dc, r, runStart, c, runColor)
				runStart = c
				runColor = cur
			}
		}
		t.fillRun(dc, r, runStart, cols, runColor)
	}

	// Foreground pass: draw each cell's glyph. Skip spaces (already
	// covered by bg) when there are no attrs (e.g. underline).
	// Continuation cells (right half of a wide char) carry Width==0
	// and a NUL rune — skip them entirely; the wide head's glyph
	// renders into both columns at draw time.
	for r := range rows {
		for c := range cols {
			cell := resolveCell(r, c)
			if cell.Width == 0 && cell.Ch == 0 {
				continue
			}
			if cell.Ch == ' ' && cell.Attrs == 0 {
				continue
			}
			cs := style
			cs.Color = fg(cell)
			cs.Underline = cell.Attrs&AttrUnderline != 0
			dc.Text(float32(c)*t.cellW, float32(r)*t.cellH,
				runeString(cell.Ch), cs)
		}
	}

	// Cursor: shape per DECSCUSR (block / underline / bar). Suppress
	// entirely when DEC ?25 has hidden it OR when the viewport is
	// scrolled back into history. Honor blink-off half-cycle when
	// blinking is enabled.
	if g.CursorVisible && g.ViewOffset == 0 && !t.cursorBlinkOff() {
		cc := g.CursorC
		if cc >= cols {
			cc = cols - 1
		}
		if cell := g.At(g.CursorR, cc); cell != nil {
			t.drawCursor(dc, cc, g.CursorR, *cell, g.CursorShape, style)
		}
	}
	t.grid.Mu.Unlock()
}

// cursorBlinkOff reports whether the cursor is currently in the
// hidden half of its blink cycle. Returns false (always visible) for
// steady cursors. Caller holds Grid.Mu.
func (t *Term) cursorBlinkOff() bool {
	if !t.cursorBlinks() {
		return false
	}
	elapsed := time.Since(t.cursorEpoch)
	return (elapsed/cursorBlinkPeriod)%2 == 1
}

// drawCursor renders the cursor at viewport (row, col) using the
// current shape. Block inverts the cell (filled bg + cell glyph in
// fg's color); underline/bar overlay a thin filled rect on top of the
// regular foreground glyph already drawn in the foreground pass.
func (t *Term) drawCursor(dc *gui.DrawContext, col, row int, cell Cell,
	shape CursorShape, style gui.TextStyle) {
	x := float32(col) * t.cellW
	y := float32(row) * t.cellH
	switch shape {
	case CursorUnderline:
		// Bottom-aligned bar 1/8th of the cell height (min 2px) so it
		// stays visible at smaller font sizes.
		h := t.cellH / 8
		if h < 2 {
			h = 2
		}
		dc.FilledRect(x, y+t.cellH-h, t.cellW, h, fg(cell))
	case CursorBar:
		w := t.cellW / 6
		if w < 2 {
			w = 2
		}
		dc.FilledRect(x, y, w, t.cellH, fg(cell))
	default: // CursorBlock
		dc.FilledRect(x, y, t.cellW, t.cellH, fg(cell))
		cs := style
		cs.Color = bg(cell)
		dc.Text(x, y, runeString(cell.Ch), cs)
	}
}

func (t *Term) fillRun(dc *gui.DrawContext, row, c0, c1 int, color gui.Color) {
	if color == defaultBG {
		return // canvas already painted with default bg.
	}
	x := float32(c0) * t.cellW
	y := float32(row) * t.cellH
	w := float32(c1-c0) * t.cellW
	dc.FilledRect(x, y, w, t.cellH, color)
}

// onChar receives printable character input from the OS.
func (t *Term) onChar(_ *gui.Layout, e *gui.Event, _ *gui.Window) {
	if e.CharCode == 0 {
		return
	}
	t.snapToLive()
	r := rune(e.CharCode)
	var buf [4]byte
	n := utf8.EncodeRune(buf[:], r)
	if n > 0 {
		if _, err := t.pty.Write(buf[:n]); err != nil {
			log.Printf("term: pty write: %v", err)
		}
	}
	e.IsHandled = true
}

// snapToLive clears any scrollback view-offset so subsequent input is
// rendered at the live grid. No-op when already at the bottom.
func (t *Term) snapToLive() {
	t.grid.Mu.Lock()
	if t.grid.ViewOffset != 0 {
		t.grid.ResetView()
	}
	t.grid.Mu.Unlock()
}

// posToCell maps shape-local (x, y) pixels to viewport (row, col).
// Returns clamped coordinates so out-of-bounds drag positions still
// pin to the nearest cell. NaN/Inf inputs collapse to (0, 0) — int()
// of a non-finite float is undefined and would otherwise leak through
// to selection logic as a pseudo-random row/col.
func (t *Term) posToCell(x, y float32) (int, int) {
	if !finite(t.cellW) || !finite(t.cellH) {
		return 0, 0
	}
	if !realNumber(x) {
		x = 0
	}
	if !realNumber(y) {
		y = 0
	}
	r := int(y / t.cellH)
	c := int(x / t.cellW)
	if r < 0 {
		r = 0
	}
	if c < 0 {
		c = 0
	}
	t.grid.Mu.Lock()
	if r >= t.grid.Rows {
		r = t.grid.Rows - 1
	}
	if c >= t.grid.Cols {
		c = t.grid.Cols - 1
	}
	t.grid.Mu.Unlock()
	return r, c
}

// mouseSnap reports the current mouse-mode state under the grid lock.
// Reporting requires SGR encoding (?1006) and a live viewport — when
// scrolled back into history we suppress reports so the user can
// select / scroll without the host consuming the events.
type mouseSnap struct {
	report bool // any of ?1000/?1002/?1003 active
	drag   bool // ?1002
	any    bool // ?1003
	sgr    bool // ?1006
	live   bool // ViewOffset == 0
}

func (t *Term) mouseSnap() mouseSnap {
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	return mouseSnap{
		report: t.grid.MouseReporting(),
		drag:   t.grid.MouseTrackBtn,
		any:    t.grid.MouseTrackAny,
		sgr:    t.grid.MouseSGR,
		live:   t.grid.ViewOffset == 0,
	}
}

// shouldReport reports whether mouse events should encode to the PTY
// rather than drive local selection. Requires reporting on, SGR
// encoding on, and a live viewport.
func (m mouseSnap) shouldReport() bool { return m.report && m.sgr && m.live }

// writeMouse emits an SGR-1006 mouse report. Allocates a small stack-
// sized buffer; the per-event cost is the strconv.AppendInt path.
func (t *Term) writeMouse(cb, col, row int, press bool) {
	var buf [24]byte
	out := encodeMouseSGR(buf[:0], cb, col, row, press)
	if _, err := t.pty.Write(out); err != nil {
		log.Printf("term: pty mouse: %v", err)
	}
}

// onClick handles a button-down event. Under mouse reporting, encodes
// a press report for any supported button and arms drag tracking.
// Otherwise (the default) starts a left-button selection anchor.
func (t *Term) onClick(_ *gui.Layout, e *gui.Event, w *gui.Window) {
	r, c := t.posToCell(e.MouseX, e.MouseY)
	snap := t.mouseSnap()
	if snap.shouldReport() {
		base, ok := mouseSGRBaseButton(e.MouseButton)
		if !ok {
			return
		}
		cb := base + mouseModBits(e.Modifiers)
		t.writeMouse(cb, c, r, true)
		t.dragging = true
		t.dragButton = e.MouseButton
		t.dragReport = true
		t.lastMouseR, t.lastMouseC = r, c
		e.IsHandled = true
		return
	}
	if e.MouseButton != gui.MouseLeft {
		return
	}
	t.grid.Mu.Lock()
	t.grid.SelAnchor = SelPos{Row: r, Col: c}
	t.grid.SelHead = SelPos{Row: r, Col: c}
	t.grid.SelActive = false
	t.grid.Mu.Unlock()
	t.dragging = true
	t.dragButton = e.MouseButton
	t.dragReport = false
	w.UpdateWindow()
	e.IsHandled = true
}

// onMouseMove handles pointer motion. Under ?1002 with a button held,
// emits a drag report; under ?1003 even with no button, emits an
// any-motion report. Falls through to selection extension when this
// drag was started outside of a reporting mode.
func (t *Term) onMouseMove(_ *gui.Layout, e *gui.Event, w *gui.Window) {
	r, c := t.posToCell(e.MouseX, e.MouseY)
	snap := t.mouseSnap()
	if snap.sgr && snap.live {
		// Dedupe: only emit when crossing a cell boundary.
		if r == t.lastMouseR && c == t.lastMouseC {
			if t.dragReport {
				return
			}
			// Local-selection drag: still fall through to update
			// SelHead at unchanged coords (cheap; avoids stale state).
		}
		switch {
		case t.dragReport && snap.drag:
			base, ok := mouseSGRBaseButton(t.dragButton)
			if !ok {
				return
			}
			cb := base + mouseModBits(e.Modifiers) + 32
			t.writeMouse(cb, c, r, true)
			t.lastMouseR, t.lastMouseC = r, c
			return
		case !t.dragging && snap.any:
			cb := 35 + mouseModBits(e.Modifiers) // 3+32 = motion, no button
			t.writeMouse(cb, c, r, true)
			t.lastMouseR, t.lastMouseC = r, c
			return
		}
	}
	if !t.dragging || t.dragReport {
		return
	}
	t.grid.Mu.Lock()
	t.grid.SelHead = SelPos{Row: r, Col: c}
	if t.grid.SelHead != t.grid.SelAnchor {
		t.grid.SelActive = true
	}
	t.grid.Mu.Unlock()
	w.UpdateWindow()
}

// onMouseUp handles button-release. A drag started under reporting
// emits a release report regardless of whether the mode is still on
// (the host expects every press to be paired with a release).
func (t *Term) onMouseUp(_ *gui.Layout, e *gui.Event, w *gui.Window) {
	if !t.dragging {
		return
	}
	r, c := t.posToCell(e.MouseX, e.MouseY)
	if t.dragReport {
		snap := t.mouseSnap()
		if snap.sgr {
			base, ok := mouseSGRBaseButton(t.dragButton)
			if ok {
				cb := base + mouseModBits(e.Modifiers)
				t.writeMouse(cb, c, r, false)
			}
		}
		t.dragging = false
		t.dragReport = false
		e.IsHandled = true
		return
	}
	t.dragging = false
	t.grid.Mu.Lock()
	active := t.grid.SelActive
	text := ""
	if active {
		text = t.grid.SelectedText()
	} else {
		t.grid.ClearSelection()
	}
	t.grid.Mu.Unlock()
	if active && text != "" {
		w.SetClipboard(text)
	}
	w.UpdateWindow()
	e.IsHandled = true
}

// stripPasteEnd removes any embedded paste-end markers from s. Without
// stripping, a clipboard payload containing pasteEnd could exit
// bracketed-paste mode early and feed the rest as commands. C0 controls
// (CR, ^C, ...) are passed through, matching xterm — without bracketed
// paste enabled by the application the shell cannot distinguish pasted
// bytes from typed bytes anyway. ReplaceAll returns the original string
// when the marker is absent (common case), so no extra fast path needed.
func stripPasteEnd(s string) string {
	return strings.ReplaceAll(s, pasteEnd, "")
}

// pasteFromClipboard reads the clipboard, strips paste-end markers, and
// writes the payload to the PTY — wrapped in bracketed-paste markers
// when the application has enabled DEC ?2004.
func (t *Term) pasteFromClipboard(w *gui.Window) {
	text := w.GetClipboard()
	if text == "" {
		return
	}
	text = truncatePaste(text, maxPasteBytes)
	t.snapToLive()
	clean := stripPasteEnd(text)
	t.grid.Mu.Lock()
	bracketed := t.grid.BracketedPaste
	t.grid.Mu.Unlock()
	payload := clean
	if bracketed {
		payload = pasteStart + clean + pasteEnd
	}
	if _, err := t.pty.Write([]byte(payload)); err != nil {
		log.Printf("term: pty paste: %v", err)
	}
}

// copySelection writes the current selection to the system clipboard
// and returns true if anything was copied.
func (t *Term) copySelection(w *gui.Window) bool {
	t.grid.Mu.Lock()
	text := t.grid.SelectedText()
	t.grid.Mu.Unlock()
	if text == "" {
		return false
	}
	w.SetClipboard(text)
	return true
}

// onMouseScroll forwards wheel events to the application as SGR mouse
// reports when reporting + SGR are active and the viewport is live;
// otherwise moves the local scrollback viewport. Positive ScrollY
// reveals older content (wheel-up); negative reveals newer (down).
func (t *Term) onMouseScroll(_ *gui.Layout, e *gui.Event, w *gui.Window) {
	if e.ScrollY == 0 {
		return
	}
	snap := t.mouseSnap()
	if snap.shouldReport() {
		r, c := t.posToCell(e.MouseX, e.MouseY)
		base := 64
		if e.ScrollY < 0 {
			base = 65
		}
		t.writeMouse(base+mouseModBits(e.Modifiers), c, r, true)
		e.IsHandled = true
		return
	}
	lines := linesFromScroll(e.ScrollY, t.cellH)
	if lines == 0 {
		return
	}
	t.grid.Mu.Lock()
	t.grid.ScrollView(lines)
	t.grid.Mu.Unlock()
	w.UpdateWindow()
	e.IsHandled = true
}

// onKeyDown receives non-character keys (arrows, Enter, Backspace,
// Ctrl+letter combinations, etc.) and emits the corresponding terminal
// byte sequence. Scrollback navigation keys (PgUp/PgDn, Shift+Home/End)
// move the viewport instead of writing to the PTY; any other key snaps
// the viewport back to live.
func (t *Term) onKeyDown(_ *gui.Layout, e *gui.Event, w *gui.Window) {
	shift := e.Modifiers.Has(gui.ModShift)
	cmd := e.Modifiers.Has(gui.ModSuper)
	ctrl := e.Modifiers.Has(gui.ModCtrl)

	// Copy: Cmd+C (macOS) or Ctrl+Shift+C. Only suppress when there
	// is a non-empty selection so plain Ctrl+C still SIGINTs the child.
	if e.KeyCode == gui.KeyC && (cmd || (ctrl && shift)) {
		if t.copySelection(w) {
			e.IsHandled = true
			return
		}
		if cmd {
			// Cmd+C without selection is a no-op; never reaches PTY.
			e.IsHandled = true
			return
		}
		// Ctrl+Shift+C without selection falls through to Ctrl+letter
		// (sends 0x03 = SIGINT) below.
	}

	// Paste: Cmd+V (macOS) or Ctrl+Shift+V. Always suppresses so the
	// 'v' character isn't sent in addition to the paste payload.
	if e.KeyCode == gui.KeyV && (cmd || (ctrl && shift)) {
		t.pasteFromClipboard(w)
		e.IsHandled = true
		return
	}

	switch e.KeyCode {
	case gui.KeyPageUp:
		t.scrollByPage(+1, w)
		e.IsHandled = true
		return
	case gui.KeyPageDown:
		t.scrollByPage(-1, w)
		e.IsHandled = true
		return
	case gui.KeyHome:
		if shift {
			t.scrollToTop(w)
			e.IsHandled = true
			return
		}
	case gui.KeyEnd:
		if shift {
			t.scrollToBottom(w)
			e.IsHandled = true
			return
		}
	}

	var out []byte
	switch e.KeyCode {
	case gui.KeyEnter, gui.KeyKPEnter:
		out = []byte{'\r'}
	case gui.KeyBackspace:
		out = []byte{0x7F}
	case gui.KeyTab:
		out = []byte{'\t'}
	case gui.KeyEscape:
		out = []byte{0x1B}
	case gui.KeyUp:
		out = []byte("\x1b[A")
	case gui.KeyDown:
		out = []byte("\x1b[B")
	case gui.KeyRight:
		out = []byte("\x1b[C")
	case gui.KeyLeft:
		out = []byte("\x1b[D")
	case gui.KeyHome:
		out = []byte("\x1b[H")
	case gui.KeyEnd:
		out = []byte("\x1b[F")
	case gui.KeyDelete:
		out = []byte("\x1b[3~")
	default:
		// Ctrl+letter → control byte. Letter keys are KeyA..KeyZ.
		if e.Modifiers.Has(gui.ModCtrl) &&
			e.KeyCode >= gui.KeyA && e.KeyCode <= gui.KeyZ {
			out = []byte{byte(e.KeyCode-gui.KeyA) + 1}
		}
	}
	if len(out) == 0 {
		return
	}
	t.snapToLive()
	if _, err := t.pty.Write(out); err != nil {
		log.Printf("term: pty write: %v", err)
	}
	e.IsHandled = true
}

// scrollByPage moves the viewport one page (rows-1) in `dir` direction.
func (t *Term) scrollByPage(dir int, w *gui.Window) {
	t.grid.Mu.Lock()
	step := t.grid.Rows - 1
	if step < 1 {
		step = 1
	}
	t.grid.ScrollView(dir * step)
	t.grid.Mu.Unlock()
	w.UpdateWindow()
}

// scrollToTop pins the viewport at the oldest scrollback row.
func (t *Term) scrollToTop(w *gui.Window) {
	t.grid.Mu.Lock()
	t.grid.ScrollViewTop()
	t.grid.Mu.Unlock()
	w.UpdateWindow()
}

// scrollToBottom snaps the viewport back to the live grid.
func (t *Term) scrollToBottom(w *gui.Window) {
	t.grid.Mu.Lock()
	t.grid.ResetView()
	t.grid.Mu.Unlock()
	w.UpdateWindow()
}

