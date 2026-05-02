package term

import (
	"log"
	"math"
	"strings"
	"sync/atomic"
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
}

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

	// dragging tracks the left-button-held selection state set in
	// onClick, extended in onMouseMove, finalized in onMouseUp.
	dragging bool

	// closed guards Close so multiple calls are safe.
	closed atomic.Bool
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
		cfg:    cfg,
		grid:   g,
		parser: NewParser(g),
		pty:    pty,
		win:    w,
	}
	w.SetIDFocus(focusID)
	go t.readLoop()
	return t, nil
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

// Close stops the shell and reader goroutine. Safe to call once.
func (t *Term) Close() error {
	if t.closed.Swap(true) {
		return nil
	}
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
	// covered by bg) but still draw if attrs (e.g. underline) apply.
	for r := range rows {
		for c := range cols {
			cell := resolveCell(r, c)
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

	// Cursor: invert the cell at the cursor position. Clamp the column so
	// it stays visible at the right margin where CursorC may equal Cols
	// (pending wrap before the next Put). Suppress entirely when DEC ?25
	// has hidden it OR when the viewport is scrolled back into history.
	if g.CursorVisible && g.ViewOffset == 0 {
		cc := g.CursorC
		if cc >= cols {
			cc = cols - 1
		}
		if cell := g.At(g.CursorR, cc); cell != nil {
			x := float32(cc) * t.cellW
			y := float32(g.CursorR) * t.cellH
			dc.FilledRect(x, y, t.cellW, t.cellH, fg(*cell))
			cs := style
			cs.Color = bg(*cell)
			dc.Text(x, y, runeString(cell.Ch), cs)
		}
	}
	g.Mu.Unlock()
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

// onClick (left-button down) starts a new selection anchor.
func (t *Term) onClick(_ *gui.Layout, e *gui.Event, w *gui.Window) {
	if e.MouseButton != gui.MouseLeft {
		return
	}
	r, c := t.posToCell(e.MouseX, e.MouseY)
	t.grid.Mu.Lock()
	t.grid.SelAnchor = SelPos{Row: r, Col: c}
	t.grid.SelHead = SelPos{Row: r, Col: c}
	t.grid.SelActive = false
	t.grid.Mu.Unlock()
	t.dragging = true
	w.UpdateWindow()
	e.IsHandled = true
}

// onMouseMove extends the selection while the left button is held.
func (t *Term) onMouseMove(_ *gui.Layout, e *gui.Event, w *gui.Window) {
	if !t.dragging {
		return
	}
	r, c := t.posToCell(e.MouseX, e.MouseY)
	t.grid.Mu.Lock()
	t.grid.SelHead = SelPos{Row: r, Col: c}
	if t.grid.SelHead != t.grid.SelAnchor {
		t.grid.SelActive = true
	}
	t.grid.Mu.Unlock()
	w.UpdateWindow()
}

// onMouseUp finalizes the selection and copies to the clipboard if
// non-empty. A click without drag clears any prior selection.
func (t *Term) onMouseUp(_ *gui.Layout, e *gui.Event, w *gui.Window) {
	if !t.dragging {
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

// onMouseScroll moves the viewport over scrollback. Positive ScrollY
// reveals older content, negative reveals newer. Wheel deltas are
// converted from pixels to rows using the measured cell height.
func (t *Term) onMouseScroll(_ *gui.Layout, e *gui.Event, w *gui.Window) {
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

