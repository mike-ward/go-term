package term

import (
	"log"
	"math"
	"sync/atomic"
	"unicode/utf8"

	"github.com/mike-ward/go-gui/gui"
)

// finite reports whether f is a usable, positive cell metric. Rejects
// NaN, Inf, and non-positive values which would otherwise produce
// garbage row/col counts in OnDraw.
func finite(f float32) bool {
	x := float64(f)
	return !math.IsNaN(x) && !math.IsInf(x, 0) && f > 0
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
}

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
				Sizing: gui.FillFill,
				OnDraw: t.onDraw,
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

	// Background pass: coalesce runs of equal bg color per row.
	for r := range t.grid.Rows {
		runStart := 0
		runColor := bg(*t.grid.At(r, 0))
		for c := 1; c < t.grid.Cols; c++ {
			cur := bg(*t.grid.At(r, c))
			if cur != runColor {
				t.fillRun(dc, r, runStart, c, runColor)
				runStart = c
				runColor = cur
			}
		}
		t.fillRun(dc, r, runStart, t.grid.Cols, runColor)
	}

	// Foreground pass: draw each cell's glyph. Skip spaces (already
	// covered by bg) but still draw if attrs (e.g. underline) apply.
	for r := range t.grid.Rows {
		for c := range t.grid.Cols {
			cell := *t.grid.At(r, c)
			if cell.Ch == ' ' && cell.Attrs == 0 {
				continue
			}
			cs := style
			cs.Color = fg(cell)
			cs.Underline = cell.Attrs&AttrUnderline != 0
			x := float32(c) * t.cellW
			y := float32(r) * t.cellH
			dc.Text(x, y, runeString(cell.Ch), cs)
		}
	}

	// Cursor: invert the cell at the cursor position. Clamp the column so
	// the cursor stays visible at the right margin where CursorC may
	// equal Cols (pending wrap before the next Put).
	cr := t.grid.CursorR
	cc := t.grid.CursorC
	if cc >= t.grid.Cols {
		cc = t.grid.Cols - 1
	}
	if cell := t.grid.At(cr, cc); cell != nil {
		x := float32(cc) * t.cellW
		y := float32(cr) * t.cellH
		dc.FilledRect(x, y, t.cellW, t.cellH, fg(*cell))
		cs := style
		cs.Color = bg(*cell)
		dc.Text(x, y, runeString(cell.Ch), cs)
	}
	t.grid.Mu.Unlock()
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

// onKeyDown receives non-character keys (arrows, Enter, Backspace,
// Ctrl+letter combinations, etc.) and emits the corresponding terminal
// byte sequence.
func (t *Term) onKeyDown(_ *gui.Layout, e *gui.Event, _ *gui.Window) {
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
	case gui.KeyPageUp:
		out = []byte("\x1b[5~")
	case gui.KeyPageDown:
		out = []byte("\x1b[6~")
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
	if _, err := t.pty.Write(out); err != nil {
		log.Printf("term: pty write: %v", err)
	}
	e.IsHandled = true
}

