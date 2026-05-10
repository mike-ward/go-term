package term

import (
	"log"
	"math"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/mike-ward/go-glyph"
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
	// steady. Leave nil to honor whatever the shell asks for (steady
	// by default for a brand-new grid).
	CursorBlink *bool

	// Themes, if non-empty, adds a right-click context menu for selecting
	// a color theme at runtime. The first entry is used as the initial theme.
	Themes []NamedTheme
}

// NamedTheme pairs a display name with a Theme for use in menus.
type NamedTheme struct {
	Name  string
	Theme Theme
}

// cursorBlinkPeriod is the half-cycle duration: cursor visible for
// blinkPeriod, then hidden for blinkPeriod. 500 ms matches xterm
// defaults.
const cursorBlinkPeriod = 500 * time.Millisecond

// defaultScrollbackRows is the cap applied when Cfg.ScrollbackRows == 0.
const defaultScrollbackRows = 5000

// bellFlashDuration is how long the visual-bell overlay remains visible.
const bellFlashDuration = 100 * time.Millisecond

// scrollbarWidth is the pixel width of the scrollbar thumb.
const scrollbarWidth float32 = 4

// scrollbarDuration is how long the scrollbar stays visible after the last
// scroll event while the viewport is back at the live bottom.
const scrollbarDuration = 1500 * time.Millisecond

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

	// hoverR/hoverC track the cell under the pointer for hyperlink
	// hover highlighting. Updated in onMouseMove; read (unsynchronized)
	// in onDraw. A benign data race: worst case is one stale frame.
	// Set to (-1, -1) until the first mouse move.
	hoverR int
	hoverC int

	// closed guards Close so multiple calls are safe.
	closed atomic.Bool

	// cursorEpoch is the reference time for blink-phase calculation.
	// Set in New so the cursor starts in the "on" half-cycle.
	cursorEpoch time.Time

	// blinkDone signals the blink ticker goroutine to exit. Closed by
	// Close.
	blinkDone chan struct{}

	// autoScrollDir drives the selection auto-scroll goroutine during a
	// drag that extends outside the widget (-1 = toward live,
	// +1 = into scrollback, 0 = no scroll). Written on the main
	// thread; read in autoScrollLoop — atomic for safety.
	autoScrollDir atomic.Int32

	// drawVersion is incremented on every visual state change so that
	// go-gui's DrawCanvas tessellation cache can skip OnDraw on unchanged
	// frames. Reads happen on the main thread (View); writes happen on
	// both the main thread and the reader goroutine, hence atomic.
	drawVersion atomic.Uint64

	// writeHost forwards bytes to the PTY. Tests replace this with a
	// buffer sink so key/focus behavior can be asserted without a live PTY.
	writeHost func([]byte) error

	// scrollAcc carries sub-cell pixel remainder between live scroll events
	// so no movement is lost. Main-thread only; no lock needed.
	scrollAcc float32

	// Search state. All fields accessed on the GUI goroutine only (onChar,
	// onKeyDown, onDraw) — no lock required.
	searchActive  bool
	searchQuery   string
	searchMatches []ContentPos // viewport matches refreshed each onDraw
	searchIdx     int          // index of last jump target in searchMatches

	// Bell flash state. Both fields are main-thread only (written inside
	// QueueCommand callbacks and read in onDraw). bellSeenCount tracks
	// the last BellCount observed so new bells are detected exactly once.
	bellSeenCount  uint64
	bellFlashUntil time.Time

	// readBellCount tracks the BellCount seen by the readLoop goroutine so
	// bell events (which dirty no cells) still trigger a version bump.
	// Only accessed from readLoop; no synchronization needed.
	readBellCount uint64

	// scrollbarUntil is the deadline until which the scrollbar thumb is
	// rendered, even after ViewOffset returns to 0. Main-thread only.
	scrollbarUntil time.Time
	// scrollbarTimer is the single debounce timer that schedules the hide
	// redraw. Reset on each scroll event; avoids spawning a goroutine per
	// event. Main-thread only (created lazily in showScrollbar).
	scrollbarTimer *time.Timer

	// themeMenuItems is the precomputed ContextMenu item list for runtime
	// theme switching. Built once in New; nil when no themes are configured.
	themeMenuItems []gui.MenuItemCfg

	// Momentum scroll state. momentumVel/Acc/CellH/Coasting protected by
	// momentumMu. momentumTimer and momentumKick owned by the GUI goroutine
	// (onMouseScroll) except for the timer callback, which only touches
	// momentumMu-protected fields.
	momentumMu       sync.Mutex
	momentumVel      float64       // EMA of recent scroll deltas (pixels)
	momentumAcc      float64       // sub-cell pixel remainder for coast
	momentumCellH    float32       // cellH snapshot at last scroll event
	momentumCoasting bool          // true while goroutine is decelerating
	momentumKick     chan struct{} // buffered 1; wakes momentumLoop
	momentumTimer    *time.Timer   // reset on each scroll; fires kickMomentum

	// runBuf reused across onDraw calls; grows once, never freed.
	runBuf strings.Builder
}

type keyModes struct {
	appCursor     bool
	appKeypad     bool
	kittyKeyFlags uint32
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
	if len(cfg.Themes) > 0 {
		g.Theme = cfg.Themes[0].Theme
	}
	var themeMenuItems []gui.MenuItemCfg
	if len(cfg.Themes) > 0 {
		themeMenuItems = make([]gui.MenuItemCfg, 0, len(cfg.Themes)+1)
		themeMenuItems = append(themeMenuItems, gui.MenuSubtitle("Theme"))
		for i, nt := range cfg.Themes {
			themeMenuItems = append(themeMenuItems, gui.MenuItemCfg{ID: strconv.Itoa(i), Text: nt.Name})
		}
	}
	switch {
	case cfg.ScrollbackRows == 0:
		g.ScrollbackCap = defaultScrollbackRows
	case cfg.ScrollbackRows > 0:
		g.ScrollbackCap = clampScrollback(cfg.ScrollbackRows)
	default:
		// Negative: leave ScrollbackCap = 0 (scrollback disabled).
	}
	t := &Term{
		cfg:            cfg,
		grid:           g,
		parser:         NewParser(g),
		pty:            pty,
		win:            w,
		lastMouseR:     -1,
		lastMouseC:     -1,
		hoverR:         -1,
		hoverC:         -1,
		cursorEpoch:    time.Now(),
		blinkDone:      make(chan struct{}),
		momentumKick:   make(chan struct{}, 1),
		themeMenuItems: themeMenuItems,
	}
	t.writeHost = func(b []byte) error {
		_, err := t.pty.Write(b)
		return err
	}
	t.parser.SetTitleHandler(t.onParserTitle)
	t.parser.SetReplyHandler(t.onParserReply)
	t.parser.SetClipboardHandler(func(data []byte) {
		text := string(data)
		t.win.QueueCommand(func(w *gui.Window) {
			w.SetClipboard(text)
		})
	})
	prevOnEvent := w.OnEvent
	w.OnEvent = func(e *gui.Event, w *gui.Window) {
		t.onWindowEvent(e)
		if prevOnEvent != nil {
			prevOnEvent(e, w)
		}
	}
	w.SetIDFocus(focusID)
	go t.readLoop()
	go t.blinkLoop()
	go t.autoScrollLoop()
	go t.momentumLoop()
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
				t.bumpVersion()
				t.win.QueueCommand(func(w *gui.Window) {
					w.UpdateWindow()
				})
			}
		}
	}
}

// autoScrollLoop scrolls the viewport while autoScrollDir is non-zero.
// Handles the case where onMouseMove stops firing when the mouse leaves
// the window (e.g. above the title bar). Exits when blinkDone is closed.
func (t *Term) autoScrollLoop() {
	const rate = 80 * time.Millisecond
	tk := time.NewTicker(rate)
	defer tk.Stop()
	for {
		select {
		case <-t.blinkDone:
			return
		case <-tk.C:
			dir := int(t.autoScrollDir.Load())
			if dir == 0 {
				continue
			}
			t.grid.Mu.Lock()
			t.grid.ScrollView(dir)
			t.grid.Mu.Unlock()
			t.bumpVersion()
			t.win.QueueCommand(func(w *gui.Window) {
				t.showScrollbar()
				w.UpdateWindow()
			})
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
	if err := t.writeHost(b); err != nil {
		log.Printf("term: pty reply: %v", err)
	}
}

func (t *Term) onWindowEvent(e *gui.Event) {
	if e == nil {
		return
	}
	var report []byte
	t.grid.Mu.Lock()
	focus := t.grid.FocusReporting
	t.grid.Mu.Unlock()
	if !focus {
		return
	}
	switch e.Type {
	case gui.EventFocused:
		report = []byte("\x1b[I")
	case gui.EventUnfocused:
		report = []byte("\x1b[O")
	default:
		return
	}
	if err := t.writeHost(report); err != nil {
		log.Printf("term: pty focus report: %v", err)
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

// SetTheme replaces the active color theme and schedules a redraw.
// Safe to call from the main thread at any time.
func (t *Term) SetTheme(th Theme) {
	t.grid.Mu.Lock()
	t.grid.Theme = th
	t.grid.Mu.Unlock()
	t.bumpVersion()
}

// focusID is the IDFocus value claimed by the terminal container.
const focusID uint32 = 1

// View returns the go-gui view tree for this terminal. Usable as a
// gui.Window UpdateView generator: w.UpdateView(t.View).
func (t *Term) View(w *gui.Window) gui.View {
	ww, wh := w.WindowSize()
	canvas := gui.DrawCanvas(gui.DrawCanvasCfg{
		ID:            "term-canvas",
		Version:       t.drawVersion.Load(),
		Sizing:        gui.FillFill,
		OnDraw:        t.onDraw,
		OnMouseScroll: t.onMouseScroll,
		OnClick:       t.onClick,
		OnMouseMove:   t.onMouseMove,
		OnMouseUp:     t.onMouseUp,
	})
	colCfg := gui.ContainerCfg{
		Padding:   gui.Some(gui.Padding{}),
		Spacing:   gui.SomeF(0),
		Color:     t.grid.Theme.DefaultBG,
		IDFocus:   focusID,
		OnChar:    t.onChar,
		OnKeyDown: t.onKeyDown,
		OnKeyUp:   t.onKeyUp,
		Content:   []gui.View{canvas},
	}
	if len(t.themeMenuItems) > 0 {
		colCfg.Sizing = gui.FillFill
		return gui.ContextMenu(w, gui.ContextMenuCfg{
			ID:      "term-theme-menu",
			Width:   float32(ww),
			Height:  float32(wh),
			Sizing:  gui.FixedFixed,
			Padding: gui.NoPadding,
			Items:   t.themeMenuItems,
			Action: func(id string, _ *gui.Event, w *gui.Window) {
				i, err := strconv.Atoi(id)
				if err != nil || i < 0 || i >= len(t.cfg.Themes) {
					return
				}
				t.SetTheme(t.cfg.Themes[i].Theme)
				w.UpdateWindow()
			},
			Content: []gui.View{gui.Column(colCfg)},
		})
	}
	colCfg.Width = float32(ww)
	colCfg.Height = float32(wh)
	colCfg.Sizing = gui.FixedFixed
	return gui.Column(colCfg)
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
			bellCount := t.grid.BellCount
			redraw := !t.grid.SyncOutput || !t.grid.SyncActive
			// Gate the version bump on actual visual changes: cell mutations
			// (HasDirtyRows) or a new BEL (which marks no cells but needs a
			// flash). Pure no-op sequences (swallowed queries, etc.) skip the
			// version bump so the tessellation cache stays valid.
			dirty := t.grid.HasDirtyRows() || bellCount != t.readBellCount
			if redraw && dirty {
				t.readBellCount = bellCount
				t.bumpVersion()
			}
			t.grid.Mu.Unlock()
			if redraw && dirty {
				t.win.QueueCommand(func(w *gui.Window) {
					if bellCount > t.bellSeenCount {
						t.bellSeenCount = bellCount
						t.bellFlashUntil = time.Now().Add(bellFlashDuration)
						// Schedule a redraw to clear the flash overlay.
						time.AfterFunc(bellFlashDuration+time.Millisecond, func() {
							if !t.closed.Load() {
								t.bumpVersion()
								t.win.QueueCommand(func(w *gui.Window) { w.UpdateWindow() })
							}
						})
					}
					w.UpdateWindow()
				})
			}
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

// bumpVersion increments drawVersion so the next View call produces a
// new cache key, forcing go-gui to re-invoke OnDraw for this frame.
func (t *Term) bumpVersion() { t.drawVersion.Add(1) }

// scrollbarGeometry computes the scrollbar thumb Y position and height.
// sbLen = len(Scrollback), viewH = canvas pixel height. Caller ensures sbLen > 0.
func scrollbarGeometry(sbLen, rows, viewOffset int, viewH float32) (thumbY, thumbH float32) {
	total := float32(sbLen + rows)
	if total <= 0 {
		return
	}
	thumbH = float32(rows) / total * viewH
	thumbY = float32(sbLen-viewOffset) / total * viewH
	return
}

// showScrollbar arms the auto-hide timer for the scrollbar thumb. Call on
// the main thread whenever the viewport scrolls. Uses a single debounced
// timer so rapid scroll events don't accumulate goroutines.
func (t *Term) showScrollbar() {
	t.scrollbarUntil = time.Now().Add(scrollbarDuration)
	if t.scrollbarTimer == nil {
		t.scrollbarTimer = time.AfterFunc(scrollbarDuration+time.Millisecond, func() {
			if !t.closed.Load() {
				t.bumpVersion()
				t.win.QueueCommand(func(w *gui.Window) { w.UpdateWindow() })
			}
		})
	} else {
		t.scrollbarTimer.Reset(scrollbarDuration + time.Millisecond)
	}
}

// runKey captures the rendering-relevant properties of a cell for
// run-coalescing in the foreground pass. Two cells with equal runKey
// can be drawn in a single dc.Text call.
type runKey struct {
	color         gui.Color
	ulColor       gui.Color
	typeface      glyph.Typeface
	ulStyle       uint8 // ULNone..ULDashed; drives underline rendering
	strikethrough bool
	linkID        uint16
}

// cellRunKey computes the runKey for cell, applying attribute and
// hyperlink-hover color transforms. Must be called under Grid.Mu.
func cellRunKey(cell Cell, base gui.TextStyle, g *Grid, hoverR, hoverC int) runKey {
	rawFG := g.Theme.fg(cell)
	color := rawFG
	if cell.Attrs&AttrDim != 0 {
		color = gui.RGB(rawFG.R/2, rawFG.G/2, rawFG.B/2)
	}
	tf := base.Typeface
	bold, italic := cell.Attrs&AttrBold != 0, cell.Attrs&AttrItalic != 0
	switch {
	case bold && italic:
		tf = glyph.TypefaceBoldItalic
	case bold:
		tf = glyph.TypefaceBold
	case italic:
		tf = glyph.TypefaceItalic
	}
	ulStyle := cell.ULStyle
	ulColor := g.Theme.resolve(cell.ULColor, rawFG)
	if cell.LinkID != 0 {
		if ulStyle == ULNone {
			ulStyle = ULSingle
		}
		if hoverR >= 0 && hoverC >= 0 {
			if g.ViewCellAt(hoverR, hoverC).LinkID == cell.LinkID {
				col := color
				color = gui.RGB(col.R/2, col.G/2, 255)
			}
		}
	}
	return runKey{
		color:         color,
		ulColor:       ulColor,
		typeface:      tf,
		ulStyle:       ulStyle,
		strikethrough: cell.Attrs&AttrStrikethrough != 0,
		linkID:        cell.LinkID,
	}
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
	t.grid.ClearDirty()

	// Fast path: live viewport with no selection and no active search reads
	// directly from the cell buffer, skipping ViewOffset / scrollback
	// branches and per-cell InSelection / search-match work.
	g := t.grid
	rows, cols = g.Rows, g.Cols
	live := g.ViewOffset == 0 && !g.SelActive && !t.searchActive
	cells := g.Cells

	// Pre-map search matches and selection to viewport rows to avoid O(N)
	// checks inside the per-cell loop.
	type vMatch struct{ col, len int }
	var (
		vMatchesByRow [][]vMatch
		qRunes        []rune
	)
	if t.searchActive && t.searchQuery != "" {
		vMatchesByRow = make([][]vMatch, rows)
		matches := g.ViewportMatches(t.searchQuery)
		qRunes = []rune(t.searchQuery)
		t.searchMatches = matches
		qLen := len(qRunes)
		for _, m := range matches {
			if vr, ok := g.ContentRowToViewport(m.Row); ok && vr < rows {
				vMatchesByRow[vr] = append(vMatchesByRow[vr], vMatch{m.Col, qLen})
			}
		}
	}

	type rowBounds struct {
		c0, c1 int
		active bool
	}
	var rowSel []rowBounds
	if g.SelActive {
		rowSel = make([]rowBounds, rows)
		s, e := g.selOrder()
		for r := range rows {
			cr := g.viewportToContent(r)
			if cr < s.Row || cr > e.Row {
				continue
			}
			c0, c1 := 0, cols-1
			if cr == s.Row {
				c0 = s.Col
			}
			if cr == e.Row {
				c1 = e.Col
			}
			rowSel[r] = rowBounds{c0, c1, true}
		}
	}

	resolveCell := func(r, c int) Cell {
		if live {
			return cells[r*cols+c]
		}
		cell := g.ViewCellAt(r, c)
		if rowSel != nil {
			if rb := rowSel[r]; rb.active && c >= rb.c0 && c <= rb.c1 {
				cell.Attrs ^= AttrInverse
			}
		}
		if vMatchesByRow != nil {
			for _, m := range vMatchesByRow[r] {
				if c >= m.col && c < m.col+m.len {
					cell.Attrs ^= AttrInverse
					break
				}
			}
		}
		return cell
	}

	// When the search bar is active it owns the last row — skip that row in
	// both cell passes so terminal text doesn't bleed through the overlay.
	renderRows := rows
	if t.searchActive {
		renderRows = rows - 1
		if renderRows < 0 {
			renderRows = 0
		}
	}

	// Background pass: coalesce runs of equal bg color per row.
	for r := range renderRows {
		runStart := 0
		runColor := g.Theme.bg(resolveCell(r, 0))
		for c := 1; c < cols; c++ {
			cur := g.Theme.bg(resolveCell(r, c))
			if cur != runColor {
				t.fillRun(dc, r, runStart, c, runColor)
				runStart = c
				runColor = cur
			}
		}
		t.fillRun(dc, r, runStart, cols, runColor)
	}

	// Foreground pass: coalesce adjacent cells with identical style into a
	// single dc.Text call. Wide chars break the run and are emitted
	// individually (their glyph spans two columns). Continuation cells
	// (right half of a wide char, Width==0 Ch==0) are skipped without
	// breaking the current run. Plain spaces with no attrs or link don't
	// start a new run but extend an existing same-style one.
	hR, hC := t.hoverR, t.hoverC // benign unsynchronized read; see updateHover
	t.runBuf.Reset()
	var (
		runStart int
		runCols  int // columns spanned by the open run (for underline width)
		runStyle runKey
		runOpen  bool
	)
	flushRun := func(r int) {
		if !runOpen || t.runBuf.Len() == 0 {
			runOpen = false
			return
		}
		cs := style
		cs.Color = runStyle.color
		cs.Typeface = runStyle.typeface
		cs.Underline = false
		cs.Strikethrough = runStyle.strikethrough
		dc.Text(float32(runStart)*t.cellW, float32(r)*t.cellH,
			t.runBuf.String(), cs)
		if runStyle.ulStyle != ULNone {
			t.drawUnderlineDecor(dc,
				float32(runStart)*t.cellW, float32(r)*t.cellH,
				float32(runCols)*t.cellW,
				runStyle.ulStyle, runStyle.ulColor)
		}
		runOpen = false
		t.runBuf.Reset()
		runCols = 0
	}
	for r := range renderRows {
		runOpen = false
		t.runBuf.Reset()
		runCols = 0
		for c := range cols {
			cell := resolveCell(r, c)
			if cell.Width == 0 && cell.Ch == 0 {
				continue // continuation cell; skip without breaking run
			}
			k := cellRunKey(cell, style, g, hR, hC)
			isPlainSpace := cell.Ch == ' ' && cell.Attrs == 0 && cell.LinkID == 0
			if cell.Width == 2 {
				flushRun(r)
				cs := style
				cs.Color = k.color
				cs.Typeface = k.typeface
				cs.Underline = false
				cs.Strikethrough = k.strikethrough
				dc.Text(float32(c)*t.cellW, float32(r)*t.cellH,
					runeString(cell.Ch), cs)
				if k.ulStyle != ULNone {
					t.drawUnderlineDecor(dc,
						float32(c)*t.cellW, float32(r)*t.cellH,
						2*t.cellW, k.ulStyle, k.ulColor)
				}
				continue
			}
			if isPlainSpace {
				if runOpen && k == runStyle {
					t.runBuf.WriteRune(' ')
					runCols++
				} else {
					flushRun(r)
				}
				continue
			}
			if runOpen && k == runStyle {
				t.runBuf.WriteRune(cell.Ch)
				runCols++
			} else {
				flushRun(r)
				runOpen = true
				runStart = c
				runCols = 1
				runStyle = k
				t.runBuf.WriteRune(cell.Ch)
			}
		}
		flushRun(r)
	}

	now := time.Now()

	// Cursor: shape per DECSCUSR (block / underline / bar). Suppress
	// entirely when DEC ?25 has hidden it OR when the viewport is
	// scrolled back into history. Honor blink-off half-cycle when
	// blinking is enabled.
	if g.CursorVisible && g.ViewOffset == 0 && !t.cursorBlinkOff(now) {
		cc := g.CursorC
		if cc >= cols {
			cc = cols - 1
		}
		if cell := g.At(g.CursorR, cc); cell != nil {
			t.drawCursor(dc, cc, g.CursorR, *cell, g.CursorShape, style)
		}
	}

	if t.searchActive {
		t.drawSearchBar(dc, rows, cols, style)
	}

	// Visual bell: brief semi-transparent overlay that fades within bellFlashDuration.
	if now.Before(t.bellFlashUntil) {
		dc.FilledRect(0, 0, dc.Width, dc.Height, gui.RGBA(255, 255, 255, 40))
	}

	// Scrollbar: pill-shaped thumb on the right edge. Visible while scrolled
	// back or within scrollbarDuration of the last scroll event.
	sb := len(g.Scrollback)
	if (now.Before(t.scrollbarUntil) || g.ViewOffset > 0) && sb > 0 && dc.Width >= scrollbarWidth {
		thumbY, thumbH := scrollbarGeometry(sb, g.Rows, g.ViewOffset, dc.Height)
		dc.FilledRoundedRect(dc.Width-scrollbarWidth, thumbY, scrollbarWidth, thumbH,
			scrollbarWidth/2, gui.RGBA(128, 128, 128, 120))
	}

	t.grid.Mu.Unlock()
}

// cursorBlinkOff reports whether the cursor is currently in the
// hidden half of its blink cycle. Returns false (always visible) for
// steady cursors. Caller holds Grid.Mu.
func (t *Term) cursorBlinkOff(now time.Time) bool {
	if !t.cursorBlinks() {
		return false
	}
	elapsed := now.Sub(t.cursorEpoch)
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
		dc.FilledRect(x, y+t.cellH-h, t.cellW, h, t.grid.Theme.fg(cell))
	case CursorBar:
		w := t.cellW / 6
		if w < 2 {
			w = 2
		}
		dc.FilledRect(x, y, w, t.cellH, t.grid.Theme.fg(cell))
	default: // CursorBlock
		dc.FilledRect(x, y, t.cellW, t.cellH, t.grid.Theme.fg(cell))
		cs := style
		cs.Color = t.grid.Theme.bg(cell)
		dc.Text(x, y, runeString(cell.Ch), cs)
	}
}

// drawUnderlineDecor renders underline decorations for a text run.
// x,y are the top-left of the run; w is its pixel width. Handles all
// ULStyle values including ULSingle (drawn as a rect so ulColor is honored).
func (t *Term) drawUnderlineDecor(dc *gui.DrawContext, x, y, w float32, ulStyle uint8, ulColor gui.Color) {
	thick := t.cellH / 14
	if thick < 1 {
		thick = 1
	}
	baseY := y + t.cellH - 2*thick - 1
	switch ulStyle {
	case ULSingle:
		dc.FilledRect(x, baseY, w, thick, ulColor)
	case ULDouble:
		dc.FilledRect(x, baseY-thick-1, w, thick, ulColor)
		dc.FilledRect(x, baseY, w, thick, ulColor)
	case ULCurly:
		// Approximate curly as alternating up/down segments.
		seg := t.cellW * 2
		if seg < 4 {
			seg = 4
		}
		xi := x
		up := true
		for xi < x+w {
			ww := seg
			if xi+ww > x+w {
				ww = x + w - xi
			}
			yy := baseY
			if up {
				yy = baseY - thick - 1
			}
			dc.FilledRect(xi, yy, ww, thick, ulColor)
			xi += ww
			up = !up
		}
	case ULDotted:
		step := thick * 3
		if step < 3 {
			step = 3
		}
		xi := x
		for xi+thick <= x+w {
			dc.FilledRect(xi, baseY, thick, thick, ulColor)
			xi += step
		}
	case ULDashed:
		dash := t.cellW * 3
		if dash < 6 {
			dash = 6
		}
		gap := dash / 2
		xi := x
		for xi < x+w {
			ww := dash
			if xi+ww > x+w {
				ww = x + w - xi
			}
			dc.FilledRect(xi, baseY, ww, thick, ulColor)
			xi += dash + gap
		}
	}
}

func (t *Term) fillRun(dc *gui.DrawContext, row, c0, c1 int, color gui.Color) {
	if color == t.grid.Theme.DefaultBG {
		return // canvas already painted with default bg.
	}
	x := float32(c0) * t.cellW
	y := float32(row) * t.cellH
	w := float32(c1-c0) * t.cellW
	dc.FilledRect(x, y, w, t.cellH, color)
}

// drawSearchBar paints a status bar over the bottom row of the canvas
// showing the active search query. Called under Mu (inside onDraw).
func (t *Term) drawSearchBar(dc *gui.DrawContext, rows, cols int, style gui.TextStyle) {
	y := float32(rows-1) * t.cellH
	noMatch := t.searchQuery != "" && len(t.searchMatches) == 0
	bgColor := gui.RGB(40, 40, 90)
	if noMatch {
		bgColor = gui.RGB(90, 20, 20)
	}
	dc.FilledRect(0, y, float32(cols)*t.cellW, t.cellH, bgColor)
	label := "Find: " + t.searchQuery + "▌"
	cs := style
	cs.Color = gui.RGB(220, 220, 220)
	cs.Typeface = glyph.TypefaceRegular
	dc.Text(0, y, label, cs)
}

// onChar receives printable character input from the OS.
func (t *Term) onChar(_ *gui.Layout, e *gui.Event, _ *gui.Window) {
	if e.CharCode == 0 {
		return
	}
	if t.searchActive {
		if utf8.RuneCountInString(t.searchQuery) < MaxGridDim {
			t.searchQuery += string(rune(e.CharCode))
		}
		e.IsHandled = true
		t.bumpVersion()
		t.win.QueueCommand(func(w *gui.Window) { w.UpdateWindow() })
		return
	}
	t.snapToLive()
	r := rune(e.CharCode)

	// KKP flag 8: report all printable keys as CSI u escape codes.
	// The codepoint is the base (unshifted) form; Shift is in the modifier.
	t.grid.Mu.Lock()
	kkpFlags := t.grid.KittyKeyFlags
	t.grid.Mu.Unlock()
	if kkpFlags&8 != 0 {
		cp := int(r)
		if r >= 'A' && r <= 'Z' && e.Modifiers.Has(gui.ModShift) {
			cp = int(r-'A') + 'a'
		}
		if seq := kittyKeySeq(cp, e.Modifiers, kkpFlags, false); seq != nil {
			t.writeBytes(seq)
			e.IsHandled = true
			return
		}
	}

	var buf [4]byte
	n := utf8.EncodeRune(buf[:], r)
	if n > 0 {
		t.writeBytes(buf[:n])
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
	pixels bool // ?1016 — pixel-precise SGR coordinates
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
		pixels: t.grid.MouseSGRPixels,
		live:   t.grid.ViewOffset == 0,
	}
}

// shouldReport reports whether mouse events should encode to the PTY
// rather than drive local selection. Requires reporting on, SGR
// encoding on, and a live viewport.
func (m mouseSnap) shouldReport() bool { return m.report && m.sgr && m.live }

// writeMouse emits an SGR mouse report. When pixels is true (?1016 active),
// pixX/pixY (0-based widget pixels) are used; otherwise col/row (0-based
// cell indices) are used. Both forms report 1-based coordinates per spec.
func (t *Term) writeMouse(cb, col, row int, pixX, pixY float32, pixels, press bool) {
	var buf [32]byte
	var out []byte
	if pixels {
		out = encodeMouseSGR(buf[:0], cb, int(pixX), int(pixY), press)
	} else {
		out = encodeMouseSGR(buf[:0], cb, col, row, press)
	}
	if err := t.writeHost(out); err != nil {
		log.Printf("term: pty mouse: %v", err)
	}
}

func (t *Term) writeBytes(out []byte) {
	if err := t.writeHost(out); err != nil {
		log.Printf("term: pty write: %v", err)
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
		t.writeMouse(cb, c, r, e.MouseX, e.MouseY, snap.pixels, true)
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
	contentR := t.grid.viewportToContent(r)
	t.grid.SelAnchor = ContentPos{Row: contentR, Col: c}
	t.grid.SelHead = ContentPos{Row: contentR, Col: c}
	t.grid.SelActive = false
	t.grid.Mu.Unlock()
	t.dragging = true
	t.dragButton = e.MouseButton
	t.dragReport = false
	w.MouseLock(gui.MouseLockCfg{
		MouseMove: t.onMouseMove,
		MouseUp:   t.onMouseUp,
	})
	t.bumpVersion()
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
			t.writeMouse(cb, c, r, e.MouseX, e.MouseY, snap.pixels, true)
			t.lastMouseR, t.lastMouseC = r, c
			return
		case !t.dragging && snap.any:
			cb := 35 + mouseModBits(e.Modifiers) // 3+32 = motion, no button
			t.writeMouse(cb, c, r, e.MouseX, e.MouseY, snap.pixels, true)
			t.lastMouseR, t.lastMouseC = r, c
			return
		}
	}
	if !t.dragging || t.dragReport {
		// Update hover for hyperlink highlighting even when not dragging.
		t.updateHover(r, c, w)
		return
	}
	t.grid.Mu.Lock()
	rows := t.grid.Rows
	widgetH := float32(rows) * t.cellH
	if t.cellH > 0 {
		switch {
		case e.MouseY < 0:
			t.grid.ScrollView(1)
		case e.MouseY > widgetH:
			t.grid.ScrollView(-1)
		}
	}
	contentR := t.grid.viewportToContent(r)
	t.grid.SelHead = ContentPos{Row: contentR, Col: c}
	if t.grid.SelHead != t.grid.SelAnchor {
		t.grid.SelActive = true
	}
	t.grid.Mu.Unlock()
	// Persist scroll direction so autoScrollLoop keeps scrolling if
	// onMouseMove stops firing (mouse above title bar / window edge).
	if t.cellH > 0 {
		switch {
		case e.MouseY < 0:
			t.autoScrollDir.Store(1)
		case e.MouseY > widgetH:
			t.autoScrollDir.Store(-1)
		default:
			t.autoScrollDir.Store(0)
		}
	}
	t.bumpVersion()
	w.UpdateWindow()
	t.updateHover(r, c, w)
}

// updateHover updates t.hoverR/C and requests a redraw when entering or
// leaving a hyperlinked cell run.
func (t *Term) updateHover(r, c int, w *gui.Window) {
	if r == t.hoverR && c == t.hoverC {
		return
	}
	oldR, oldC := t.hoverR, t.hoverC
	t.hoverR, t.hoverC = r, c
	t.grid.Mu.Lock()
	var prevLink, curLink uint16
	if oldR >= 0 && oldC >= 0 {
		prevLink = t.grid.ViewCellAt(oldR, oldC).LinkID
	}
	curLink = t.grid.ViewCellAt(r, c).LinkID
	t.grid.Mu.Unlock()
	if prevLink != 0 || curLink != 0 {
		t.bumpVersion()
		w.UpdateWindow()
	}
}

// onMouseUp handles button-release. A drag started under reporting
// emits a release report regardless of whether the mode is still on
// (the host expects every press to be paired with a release).
func (t *Term) onMouseUp(_ *gui.Layout, e *gui.Event, w *gui.Window) {
	if !t.dragging {
		return
	}
	t.autoScrollDir.Store(0)
	w.MouseUnlock()
	r, c := t.posToCell(e.MouseX, e.MouseY)
	if t.dragReport {
		snap := t.mouseSnap()
		if snap.sgr {
			base, ok := mouseSGRBaseButton(t.dragButton)
			if ok {
				cb := base + mouseModBits(e.Modifiers)
				t.writeMouse(cb, c, r, e.MouseX, e.MouseY, snap.pixels, false)
			}
		}
		t.dragging = false
		t.dragReport = false
		e.IsHandled = true
		return
	}
	t.dragging = false
	// Single click (no drag) with Cmd/Ctrl on a hyperlink → open URL.
	if !t.grid.SelActive {
		if e.Modifiers&gui.ModSuper != 0 || e.Modifiers&gui.ModCtrl != 0 {
			t.grid.Mu.Lock()
			cell := t.grid.ViewCellAt(r, c)
			url := t.grid.LinkURL(cell.LinkID)
			t.grid.Mu.Unlock()
			if url != "" {
				openURL(url)
				e.IsHandled = true
				return
			}
		}
	}
	if !t.copySelection(w) {
		t.grid.Mu.Lock()
		t.grid.ClearSelection()
		t.grid.Mu.Unlock()
	}
	t.bumpVersion()
	w.UpdateWindow()
	e.IsHandled = true
}

// openURL opens url with the OS default browser/handler.
func openURL(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
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
	if err := t.writeHost([]byte(payload)); err != nil {
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
// Each event also feeds the momentum EMA so that releasing the trackpad
// produces a brief coast rather than an abrupt stop.
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
		t.writeMouse(base+mouseModBits(e.Modifiers), c, r, e.MouseX, e.MouseY, snap.pixels, true)
		e.IsHandled = true
		return
	}
	if !realNumber(e.ScrollY) || !finite(t.cellH) {
		return
	}
	// Accumulate scaled pixels so sub-cell deltas carry over between events.
	// scrollSensitivity converts raw trackpad px to a faster scroll speed;
	// the accumulator ensures no movement is lost to integer truncation.
	const scrollSensitivity float32 = 12
	t.scrollAcc += e.ScrollY * scrollSensitivity
	lines := int(t.scrollAcc / t.cellH)
	if lines != 0 {
		t.scrollAcc -= float32(lines) * t.cellH
		t.grid.Mu.Lock()
		t.grid.ScrollView(lines)
		t.grid.Mu.Unlock()
		t.showScrollbar()
		t.bumpVersion()
		w.UpdateWindow()
	}
	// Track peak velocity of the current gesture, scaled up so coast covers a
	// meaningful number of lines. Ignore decelerating OS-momentum samples by
	// only updating when the new sample is larger in magnitude or direction
	// reverses. Cap prevents a huge flick from coasting forever.
	const (
		momentumScale = 5.0
		momentumCap   = 300.0
	)
	t.momentumMu.Lock()
	newVel := math.Max(-momentumCap, math.Min(momentumCap, float64(e.ScrollY)*momentumScale))
	if math.Abs(newVel) >= math.Abs(t.momentumVel) || (t.momentumVel > 0) != (newVel > 0) {
		t.momentumVel = newVel
	}
	t.momentumAcc = 0
	t.momentumCellH = t.cellH
	t.momentumCoasting = false
	t.momentumMu.Unlock()
	// Arm/reset a timer: coast starts 80 ms after the last scroll event.
	if t.momentumTimer == nil {
		t.momentumTimer = time.AfterFunc(80*time.Millisecond, t.kickMomentum)
	} else {
		t.momentumTimer.Reset(80 * time.Millisecond)
	}
	e.IsHandled = true
}

// kickMomentum is the AfterFunc callback fired 80 ms after the last scroll
// event. It marks the momentum state as coasting and wakes momentumLoop.
func (t *Term) kickMomentum() {
	t.momentumMu.Lock()
	t.momentumCoasting = true
	t.momentumMu.Unlock()
	select {
	case t.momentumKick <- struct{}{}:
	default:
	}
}

// momentumLoop decelerates the scroll velocity after the user lifts their
// finger. Ticks at ~60 fps; each tick applies the decaying velocity to a
// pixel accumulator and converts whole cells into ScrollView calls.
func (t *Term) momentumLoop() {
	const (
		tickDur  = 16 * time.Millisecond
		friction = 0.96 // velocity multiplier per 16 ms tick (~1.8 s to 1 % of start)
		minVel   = 0.3  // px/tick below which coast stops
	)
	tk := time.NewTicker(tickDur)
	defer tk.Stop()
	for {
		select {
		case <-t.blinkDone:
			return
		case <-t.momentumKick:
			// coasting flag already set; next tick starts the coast
		case <-tk.C:
			t.momentumMu.Lock()
			if !t.momentumCoasting {
				t.momentumMu.Unlock()
				continue
			}
			t.momentumVel *= friction
			t.momentumAcc += t.momentumVel
			cellH := t.momentumCellH
			lines := 0
			if finite(cellH) {
				lines = int(t.momentumAcc / float64(cellH))
				if lines != 0 {
					t.momentumAcc -= float64(lines) * float64(cellH)
				}
			}
			if math.Abs(t.momentumVel) < minVel {
				t.momentumCoasting = false
				t.momentumVel = 0
				t.momentumAcc = 0
			}
			t.momentumMu.Unlock()
			if lines != 0 {
				t.grid.Mu.Lock()
				t.grid.ScrollView(lines)
				t.grid.Mu.Unlock()
				t.bumpVersion()
				t.win.QueueCommand(func(w *gui.Window) {
					t.showScrollbar()
					w.UpdateWindow()
				})
			}
		}
	}
}

func (t *Term) keyModes() keyModes {
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	return keyModes{
		appCursor:     t.grid.AppCursorKeys,
		appKeypad:     t.grid.AppKeypad,
		kittyKeyFlags: t.grid.KittyKeyFlags,
	}
}

// kittyKeySeq encodes a key in Kitty Keyboard Protocol format: CSI codepoint u
// or CSI codepoint ; modifiers u. Returns nil when flags == 0 (legacy mode).
// The modifier parameter follows the KKP spec: 1=none, 2=shift, 3=shift+alt,
// 5=ctrl, 6=shift+ctrl, 9=super, … (1 + sum of modifier bits).
// When release is true, generates a key release sequence (event-type 3):
// CSI codepoint ; modifiers : 3 u. The modifier field is mandatory when
// event-type is present, even when mod==1 (no modifiers).
func kittyKeySeq(codepoint int, mods gui.Modifier, flags uint32, release bool) []byte {
	if flags == 0 || codepoint <= 0 {
		return nil
	}
	mod := 1
	if mods.Has(gui.ModShift) {
		mod += 1
	}
	if mods.Has(gui.ModAlt) {
		mod += 2
	}
	if mods.Has(gui.ModCtrl) {
		mod += 4
	}
	if mods.Has(gui.ModSuper) {
		mod += 8
	}
	b := []byte("\x1b[")
	b = strconv.AppendInt(b, int64(codepoint), 10)
	if mod != 1 || release {
		b = append(b, ';')
		b = strconv.AppendInt(b, int64(mod), 10)
	}
	if release {
		b = append(b, ':', '3')
	}
	b = append(b, 'u')
	return b
}

func keypadSeq(k gui.KeyCode) []byte {
	switch k {
	case gui.KeyKP0:
		return []byte("\x1bOp")
	case gui.KeyKP1:
		return []byte("\x1bOq")
	case gui.KeyKP2:
		return []byte("\x1bOr")
	case gui.KeyKP3:
		return []byte("\x1bOs")
	case gui.KeyKP4:
		return []byte("\x1bOt")
	case gui.KeyKP5:
		return []byte("\x1bOu")
	case gui.KeyKP6:
		return []byte("\x1bOv")
	case gui.KeyKP7:
		return []byte("\x1bOw")
	case gui.KeyKP8:
		return []byte("\x1bOx")
	case gui.KeyKP9:
		return []byte("\x1bOy")
	case gui.KeyKPDecimal:
		return []byte("\x1bOn")
	case gui.KeyKPDivide:
		return []byte("\x1bOo")
	case gui.KeyKPMultiply:
		return []byte("\x1bOj")
	case gui.KeyKPSubtract:
		return []byte("\x1bOm")
	case gui.KeyKPAdd:
		return []byte("\x1bOk")
	case gui.KeyKPEqual:
		return []byte("\x1bOX")
	default:
		return nil
	}
}

// modParam returns the xterm modifier parameter (2..8) for shift/alt/ctrl
// combinations, or 0 when no modifiers are active.
func modParam(shift, alt, ctrl bool) int {
	n := 1
	if shift {
		n++
	}
	if alt {
		n += 2
	}
	if ctrl {
		n += 4
	}
	if n == 1 {
		return 0
	}
	return n
}

// modTilde returns \x1b[Ps~ (no modifier) or \x1b[Ps;N~ (with modifier).
func modTilde(ps string, mod int) []byte {
	if mod == 0 {
		return []byte("\x1b[" + ps + "~")
	}
	b := append([]byte("\x1b["), ps...)
	b = append(b, ';')
	b = strconv.AppendInt(b, int64(mod), 10)
	b = append(b, '~')
	return b
}

// modSS3 returns \x1bOl (no modifier) or \x1b[1;Nl (with modifier).
func modSS3(letter byte, mod int) []byte {
	if mod == 0 {
		return []byte{0x1b, 'O', letter}
	}
	b := []byte("\x1b[1;")
	b = strconv.AppendInt(b, int64(mod), 10)
	b = append(b, letter)
	return b
}

// funcKeySeq returns the xterm sequence for Insert and F1–F12, with optional
// modifier encoding. Alt is excluded: callers prepend ESC separately.
func funcKeySeq(k gui.KeyCode, shift, ctrl bool) []byte {
	mod := modParam(shift, false, ctrl)
	switch k {
	case gui.KeyInsert:
		return modTilde("2", mod)
	case gui.KeyF1:
		return modSS3('P', mod)
	case gui.KeyF2:
		return modSS3('Q', mod)
	case gui.KeyF3:
		return modSS3('R', mod)
	case gui.KeyF4:
		return modSS3('S', mod)
	case gui.KeyF5:
		return modTilde("15", mod)
	case gui.KeyF6:
		return modTilde("17", mod)
	case gui.KeyF7:
		return modTilde("18", mod)
	case gui.KeyF8:
		return modTilde("19", mod)
	case gui.KeyF9:
		return modTilde("20", mod)
	case gui.KeyF10:
		return modTilde("21", mod)
	case gui.KeyF11:
		return modTilde("23", mod)
	case gui.KeyF12:
		return modTilde("24", mod)
	}
	return nil
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
	alt := e.Modifiers.Has(gui.ModAlt)
	modes := t.keyModes()

	// Search: Cmd+F opens the search bar.
	if e.KeyCode == gui.KeyF && cmd {
		t.searchActive = true
		t.searchQuery = ""
		t.searchMatches = nil
		t.searchIdx = 0
		e.IsHandled = true
		t.bumpVersion()
		w.UpdateWindow()
		return
	}

	// Cmd+Up/Down: jump between OSC 133 prompt marks (shell integration).
	if cmd && !ctrl && !alt && (e.KeyCode == gui.KeyUp || e.KeyCode == gui.KeyDown) {
		t.jumpToMark(e.KeyCode == gui.KeyUp, w)
		e.IsHandled = true
		return
	}

	// While in search mode, intercept navigation and editing keys.
	if t.searchActive {
		switch e.KeyCode {
		case gui.KeyEnter, gui.KeyKPEnter:
			t.searchJump(!shift, w)
		case gui.KeyBackspace:
			if len(t.searchQuery) > 0 {
				rr := []rune(t.searchQuery)
				t.searchQuery = string(rr[:len(rr)-1])
				t.bumpVersion()
				w.UpdateWindow()
			}
		case gui.KeyEscape:
			t.searchActive = false
			t.searchQuery = ""
			t.searchMatches = nil
			t.bumpVersion()
			w.UpdateWindow()
		}
		e.IsHandled = true
		return
	}

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
		t.grid.Mu.Lock()
		inAlt := t.grid.AltActive
		t.grid.Mu.Unlock()
		if shift || !inAlt {
			t.scrollByPage(+1, w)
			e.IsHandled = true
			return
		}
	case gui.KeyPageDown:
		t.grid.Mu.Lock()
		inAlt := t.grid.AltActive
		t.grid.Mu.Unlock()
		if shift || !inAlt {
			t.scrollByPage(-1, w)
			e.IsHandled = true
			return
		}
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
	case gui.KeyPageUp:
		out = []byte("\x1b[5~")
	case gui.KeyPageDown:
		out = []byte("\x1b[6~")
	case gui.KeyEnter, gui.KeyKPEnter:
		// Application keypad Enter takes priority; KKP applies to regular Enter.
		if modes.appKeypad && e.KeyCode == gui.KeyKPEnter {
			out = []byte("\x1bOM")
		} else if kkp := kittyKeySeq(13, e.Modifiers, modes.kittyKeyFlags, false); kkp != nil {
			out = kkp
		} else {
			out = []byte{'\r'}
		}
	case gui.KeyBackspace:
		if kkp := kittyKeySeq(127, e.Modifiers, modes.kittyKeyFlags, false); kkp != nil {
			out = kkp
		} else {
			out = []byte{0x7F}
		}
	case gui.KeyTab:
		if kkp := kittyKeySeq(9, e.Modifiers, modes.kittyKeyFlags, false); kkp != nil {
			out = kkp
		} else {
			out = []byte{'\t'}
		}
	case gui.KeyEscape:
		if kkp := kittyKeySeq(27, e.Modifiers, modes.kittyKeyFlags, false); kkp != nil {
			out = kkp
		} else {
			out = []byte{0x1B}
		}
	case gui.KeyUp:
		if mod := modParam(shift, false, ctrl); mod != 0 {
			out = modSS3('A', mod)
		} else if modes.appCursor {
			out = []byte("\x1bOA")
		} else {
			out = []byte("\x1b[A")
		}
	case gui.KeyDown:
		if mod := modParam(shift, false, ctrl); mod != 0 {
			out = modSS3('B', mod)
		} else if modes.appCursor {
			out = []byte("\x1bOB")
		} else {
			out = []byte("\x1b[B")
		}
	case gui.KeyRight:
		if mod := modParam(shift, false, ctrl); mod != 0 {
			out = modSS3('C', mod)
		} else if modes.appCursor {
			out = []byte("\x1bOC")
		} else {
			out = []byte("\x1b[C")
		}
	case gui.KeyLeft:
		if mod := modParam(shift, false, ctrl); mod != 0 {
			out = modSS3('D', mod)
		} else if modes.appCursor {
			out = []byte("\x1bOD")
		} else {
			out = []byte("\x1b[D")
		}
	case gui.KeyHome:
		if mod := modParam(false, false, ctrl); mod != 0 {
			// Shift+Home is consumed by scrollToTop above; only Ctrl here.
			out = modSS3('H', mod)
		} else if modes.appCursor {
			out = []byte("\x1bOH")
		} else {
			out = []byte("\x1b[H")
		}
	case gui.KeyEnd:
		if mod := modParam(false, false, ctrl); mod != 0 {
			// Shift+End is consumed by scrollToBottom above; only Ctrl here.
			out = modSS3('F', mod)
		} else if modes.appCursor {
			out = []byte("\x1bOF")
		} else {
			out = []byte("\x1b[F")
		}
	case gui.KeyDelete:
		out = []byte("\x1b[3~")
	case gui.KeyInsert,
		gui.KeyF1, gui.KeyF2, gui.KeyF3, gui.KeyF4,
		gui.KeyF5, gui.KeyF6, gui.KeyF7, gui.KeyF8,
		gui.KeyF9, gui.KeyF10, gui.KeyF11, gui.KeyF12:
		out = funcKeySeq(e.KeyCode, shift, ctrl)
	default:
		if modes.appKeypad {
			out = keypadSeq(e.KeyCode)
			if len(out) > 0 {
				break
			}
		}
		// Alt+letter → lowercase letter; ESC prefix applied below.
		// Handled here so onChar sees IsHandled=true and does not also
		// send the OS-translated glyph (e.g. macOS Alt+F → ƒ).
		if alt && !ctrl && e.KeyCode >= gui.KeyA && e.KeyCode <= gui.KeyZ {
			out = []byte{byte('a' + (e.KeyCode - gui.KeyA))}
			break
		}
		// Ctrl+letter → control byte, or KKP CSI u when active.
		if e.Modifiers.Has(gui.ModCtrl) &&
			e.KeyCode >= gui.KeyA && e.KeyCode <= gui.KeyZ {
			if kkp := kittyKeySeq(int('a')+int(e.KeyCode-gui.KeyA),
				e.Modifiers, modes.kittyKeyFlags, false); kkp != nil {
				out = kkp
			} else {
				out = []byte{byte(e.KeyCode-gui.KeyA) + 1}
			}
		}
	}
	// Alt/Meta key: prefix any outbound sequence with ESC.
	if alt && len(out) > 0 {
		out = append([]byte{0x1b}, out...)
	}
	if len(out) == 0 {
		return
	}
	t.snapToLive()
	t.writeBytes(out)
	e.IsHandled = true
}

// onKeyUp generates KKP key-release sequences (event-type 3) when flag bit 2 is set.
func (t *Term) onKeyUp(_ *gui.Layout, e *gui.Event, _ *gui.Window) {
	modes := t.keyModes()
	if modes.kittyKeyFlags&2 == 0 {
		return
	}

	// KKP private-use-area codepoints (spec §7 table) for left/right modifiers,
	// functional keys, nav keys, and F-keys. ASCII codepoints for printable keys.
	var codepoint int
	switch e.KeyCode {
	case gui.KeyLeftShift:
		codepoint = 57441
	case gui.KeyRightShift:
		codepoint = 57447
	case gui.KeyLeftControl:
		codepoint = 57442
	case gui.KeyRightControl:
		codepoint = 57448
	case gui.KeyLeftAlt:
		codepoint = 57443
	case gui.KeyRightAlt:
		codepoint = 57449
	case gui.KeyLeftSuper:
		codepoint = 57444
	case gui.KeyRightSuper:
		codepoint = 57450
	case gui.KeyEnter, gui.KeyKPEnter:
		codepoint = 13
	case gui.KeyBackspace:
		codepoint = 127
	case gui.KeyTab:
		codepoint = 9
	case gui.KeyEscape:
		codepoint = 27
	case gui.KeyInsert:
		codepoint = 57348
	case gui.KeyDelete:
		codepoint = 57349
	case gui.KeyLeft:
		codepoint = 57350
	case gui.KeyRight:
		codepoint = 57351
	case gui.KeyUp:
		codepoint = 57352
	case gui.KeyDown:
		codepoint = 57353
	case gui.KeyPageUp:
		codepoint = 57354
	case gui.KeyPageDown:
		codepoint = 57355
	case gui.KeyHome:
		codepoint = 57356
	case gui.KeyEnd:
		codepoint = 57357
	case gui.KeyF1:
		codepoint = 57364
	case gui.KeyF2:
		codepoint = 57365
	case gui.KeyF3:
		codepoint = 57366
	case gui.KeyF4:
		codepoint = 57367
	case gui.KeyF5:
		codepoint = 57368
	case gui.KeyF6:
		codepoint = 57369
	case gui.KeyF7:
		codepoint = 57370
	case gui.KeyF8:
		codepoint = 57371
	case gui.KeyF9:
		codepoint = 57372
	case gui.KeyF10:
		codepoint = 57373
	case gui.KeyF11:
		codepoint = 57374
	case gui.KeyF12:
		codepoint = 57375
	default:
		if e.KeyCode >= gui.KeyA && e.KeyCode <= gui.KeyZ {
			codepoint = int('a') + int(e.KeyCode-gui.KeyA)
		} else if e.KeyCode >= gui.Key0 && e.KeyCode <= gui.Key9 {
			codepoint = int('0') + int(e.KeyCode-gui.Key0)
		} else {
			return
		}
	}

	if seq := kittyKeySeq(codepoint, e.Modifiers, modes.kittyKeyFlags, true); seq != nil {
		t.writeBytes(seq)
		e.IsHandled = true
	}
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
	t.showScrollbar()
	t.bumpVersion()
	w.UpdateWindow()
}

// scrollToTop pins the viewport at the oldest scrollback row.
func (t *Term) scrollToTop(w *gui.Window) {
	t.grid.Mu.Lock()
	t.grid.ScrollViewTop()
	t.grid.Mu.Unlock()
	t.showScrollbar()
	t.bumpVersion()
	w.UpdateWindow()
}

// scrollToBottom snaps the viewport back to the live grid.
func (t *Term) scrollToBottom(w *gui.Window) {
	t.grid.Mu.Lock()
	t.grid.ResetView()
	t.grid.Mu.Unlock()
	t.showScrollbar()
	t.bumpVersion()
	w.UpdateWindow()
}

// jumpToMark scrolls the viewport to the previous (backward=true) or next
// (backward=false) MarkPromptStart mark. No-op when no marks exist or no
// mark is found in that direction. Suppressed while the alt screen is active.
func (t *Term) jumpToMark(backward bool, w *gui.Window) {
	t.grid.Mu.Lock()
	if t.grid.AltActive {
		t.grid.Mu.Unlock()
		return
	}
	sb := len(t.grid.Scrollback)
	off := clamp(t.grid.ViewOffset, 0, sb)
	viewTop := sb - off
	var row int
	var ok bool
	if backward {
		row, ok = t.grid.PrevMark(viewTop, MarkPromptStart)
	} else {
		row, ok = t.grid.NextMark(viewTop, MarkPromptStart)
	}
	if ok {
		if row >= sb {
			t.grid.ViewOffset = 0
		} else {
			t.grid.ViewOffset = sb - row
		}
	}
	t.grid.Mu.Unlock()
	if ok {
		t.showScrollbar()
		t.bumpVersion()
		w.UpdateWindow()
	}
}

// searchJump finds the next (forward=true) or previous (forward=false) match
// for the current search query and scrolls the viewport to show it.
func (t *Term) searchJump(forward bool, w *gui.Window) {
	if t.searchQuery == "" {
		return
	}
	g := t.grid
	g.Mu.Lock()
	sb := len(g.Scrollback)
	var start ContentPos
	if len(t.searchMatches) > 0 && t.searchIdx < len(t.searchMatches) {
		start = t.searchMatches[t.searchIdx]
	} else {
		start = ContentPos{Row: sb - clamp(g.ViewOffset, 0, sb)}
	}
	pos, ok := g.Find(t.searchQuery, start, forward)
	if ok {
		liveRow := pos.Row - sb
		if liveRow >= 0 {
			g.ViewOffset = 0
		} else {
			g.ViewOffset = clamp(sb-pos.Row, 0, sb)
		}
	}
	g.Mu.Unlock()
	if ok {
		w.UpdateWindow()
	}
}
