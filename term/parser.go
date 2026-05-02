package term

import "unicode/utf8"

type parserState uint8

const (
	stGround parserState = iota
	stEsc
	stCSI
	stOSC    // collecting OSC payload, waiting for BEL or ESC \
	stOSCEsc // saw ESC inside OSC, waiting for terminating '\'
)

// maxOSCBytes caps the OSC payload size so a malicious or runaway
// stream can't grow p.osc without bound. Real titles are tiny;
// anything beyond this is truncated and the rest of the OSC is
// silently swallowed up to its terminator.
const maxOSCBytes = 4096

// da1Reply is the Primary Device Attribute response: VT100 with
// advanced video. Apps like fish probe with CSI c at startup and
// stall briefly waiting for it.
var da1Reply = []byte("\x1b[?1;2c")

// maxCSIParams caps the SGR/CSI parameter list to bound memory use against
// pathological streams like "\x1b[1;1;1;...m".
const maxCSIParams = 32

// maxCSIParamValue caps a single accumulated parameter so a digit-only
// run "\x1b[99999...9m" can't overflow int. Real terminals never need
// values above this.
const maxCSIParamValue = 1 << 20

// Parser is a minimal VT/xterm subset: CR, LF, BS, TAB, BEL plus CSI ... m
// (SGR) for color + attribute state, and a handful of CSI cursor/erase
// commands. SGR supports the 16-color, 256-color (38/48 ;5;n) and 24-bit
// truecolor (38/48 ;2;r;g;b) forms. All other escape sequences are
// silently consumed so they don't print as garbage.
type Parser struct {
	g       *Grid
	state   parserState
	params  []int   // SGR params accumulated in current CSI
	curP    int     // value being accumulated
	hasP    bool    // any digit seen for curP
	private bool    // DEC private mode: `?` prefix seen in current CSI
	intermediate byte // last intermediate byte (0x20..0x2F) seen, 0 if none
	utf     [4]byte // UTF-8 carry-over between Feed calls
	utfLen  int

	// osc accumulates the payload of the in-progress OSC (Operating
	// System Command). Reset on entry to stOSC; capped at maxOSCBytes.
	osc []byte

	// onTitle, if non-nil, is invoked for OSC 0/1/2 (window title).
	// onReply, if non-nil, is invoked when the parser needs to write
	// bytes back toward the application (e.g. DA1 response). Both run
	// while Grid.Mu is held — handlers must not re-enter the grid.
	onTitle func(string)
	onReply func([]byte)
}

// SetTitleHandler registers a callback for OSC 0/1/2. Pass nil to
// disable. Called while Grid.Mu is held.
func (p *Parser) SetTitleHandler(fn func(string)) { p.onTitle = fn }

// SetReplyHandler registers a callback for parser-originated host
// writes (DA1 today; future: cursor position reports, etc.). Called
// while Grid.Mu is held.
func (p *Parser) SetReplyHandler(fn func([]byte)) { p.onReply = fn }

// NewParser binds a parser to a grid. Callers must hold g.Mu while calling
// Feed.
func NewParser(g *Grid) *Parser {
	return &Parser{g: g, params: make([]int, 0, 8)}
}

// Feed processes b, mutating the grid. Caller holds g.Mu.
func (p *Parser) Feed(b []byte) {
	// Prepend any partial UTF-8 sequence from last call.
	if p.utfLen > 0 {
		buf := make([]byte, p.utfLen+len(b))
		copy(buf, p.utf[:p.utfLen])
		copy(buf[p.utfLen:], b)
		p.utfLen = 0
		b = buf
	}
	for i := 0; i < len(b); {
		c := b[i]
		switch p.state {
		case stGround:
			switch {
			case c == 0x07: // BEL
				i++
			case c == 0x08: // BS
				p.g.Backspace()
				i++
			case c == 0x09: // TAB
				p.g.Tab()
				i++
			case c == 0x0A: // LF
				p.g.Newline()
				i++
			case c == 0x0D: // CR
				p.g.CarriageReturn()
				i++
			case c == 0x1B: // ESC
				p.state = stEsc
				i++
			case c < 0x20: // other C0 — drop
				i++
			default:
				// Decode UTF-8. If incomplete at end of buffer,
				// stash and exit.
				r, sz := utf8.DecodeRune(b[i:])
				if r == utf8.RuneError && sz == 1 && !utf8.FullRune(b[i:]) {
					n := copy(p.utf[:], b[i:])
					p.utfLen = n
					return
				}
				p.g.Put(r)
				i += sz
			}
		case stEsc:
			switch c {
			case '[':
				p.state = stCSI
				p.params = p.params[:0]
				p.curP = 0
				p.hasP = false
				p.private = false
				p.intermediate = 0
			case ']': // OSC introducer
				p.state = stOSC
				p.osc = p.osc[:0]
			case '7': // DECSC — save cursor + SGR
				p.g.SaveCursor()
				p.state = stGround
			case '8': // DECRC — restore cursor + SGR
				p.g.RestoreCursor()
				p.state = stGround
			case 'D': // IND — index (down + scroll-up at Bottom)
				p.g.Newline()
				p.state = stGround
			case 'M': // RI — reverse index (up + scroll-down at Top)
				p.g.ReverseIndex()
				p.state = stGround
			case 'E': // NEL — next line (CR + LF)
				p.g.NextLine()
				p.state = stGround
			default:
				// 2-byte ESC sequences: ignore.
				p.state = stGround
			}
			i++
		case stCSI:
			switch {
			case c == '?' && !p.hasP && len(p.params) == 0:
				p.private = true
			case c >= '0' && c <= '9':
				p.curP = p.curP*10 + int(c-'0')
				if p.curP > maxCSIParamValue {
					p.curP = maxCSIParamValue
				}
				p.hasP = true
			case c == ';':
				if len(p.params) < maxCSIParams {
					p.params = append(p.params, p.curP)
				}
				p.curP = 0
				p.hasP = false
			case c >= 0x40 && c <= 0x7E:
				if (p.hasP || len(p.params) > 0) && len(p.params) < maxCSIParams {
					p.params = append(p.params, p.curP)
				}
				p.dispatchCSI(c)
				p.state = stGround
				p.curP = 0
				p.hasP = false
				p.private = false
				p.intermediate = 0
			case c >= 0x20 && c <= 0x2F:
				// Intermediate byte (e.g. SP for DECSCUSR " q"). Last
				// one wins — multi-intermediate sequences are rare and
				// none of the ones we honor use more than one.
				p.intermediate = c
			default:
				// Unsupported byte — keep going.
			}
			i++
		case stOSC:
			switch c {
			case 0x07: // BEL — terminator
				p.dispatchOSC()
				p.state = stGround
			case 0x1B: // ESC — possible start of ST (ESC \)
				p.state = stOSCEsc
			default:
				if len(p.osc) < maxOSCBytes {
					p.osc = append(p.osc, c)
				}
			}
			i++
		case stOSCEsc:
			if c == '\\' { // ST terminator: ESC \
				p.dispatchOSC()
				p.state = stGround
				i++
			} else {
				// Bare ESC inside OSC: abort the OSC, restart ESC
				// processing on the current byte (don't consume it).
				p.osc = p.osc[:0]
				p.state = stEsc
			}
		}
	}
}

// dispatchOSC parses the accumulated OSC payload as "Ps;Pt" and
// dispatches recognized commands. Anything malformed or unknown is
// silently dropped (xterm behavior). Called with g.Mu held.
func (p *Parser) dispatchOSC() {
	if len(p.osc) == 0 {
		return
	}
	sep := -1
	for i, b := range p.osc {
		if b == ';' {
			sep = i
			break
		}
	}
	if sep <= 0 {
		return
	}
	ps := 0
	for i := range sep {
		c := p.osc[i]
		if c < '0' || c > '9' {
			return
		}
		ps = ps*10 + int(c-'0')
		if ps > 1<<20 {
			return
		}
	}
	pt := string(p.osc[sep+1:])
	switch ps {
	case 0, 1, 2:
		// 0 = icon name + window title, 1 = icon name, 2 = window
		// title. Treat all three as title updates; widget surfaces
		// via Cfg.OnTitle (defaulting to win.SetTitle).
		if p.onTitle != nil {
			p.onTitle(pt)
		}
	case 7:
		// Working directory notification (iTerm/VTE convention).
		// Payload is typically "file://host/path"; embedders parse.
		p.g.Cwd = pt
	}
}

func (p *Parser) dispatchCSI(final byte) {
	if p.private {
		switch final {
		case 'h':
			p.applyDECMode(true)
		case 'l':
			p.applyDECMode(false)
		}
		return
	}
	switch final {
	case 'm':
		p.applySGR()
	case 's':
		p.g.SaveCursor()
	case 'u':
		p.g.RestoreCursor()
	case 'A':
		p.g.CursorUp(p.param(0, 1))
	case 'B', 'e':
		p.g.CursorDown(p.param(0, 1))
	case 'C', 'a':
		p.g.CursorForward(p.param(0, 1))
	case 'D':
		p.g.CursorBack(p.param(0, 1))
	case 'E':
		p.g.MoveCursor(p.g.CursorR+p.param(0, 1), 0)
	case 'F':
		p.g.MoveCursor(p.g.CursorR-p.param(0, 1), 0)
	case 'G', '`':
		p.g.MoveCursor(p.g.CursorR, p.param(0, 1)-1)
	case 'd':
		p.g.MoveCursor(p.param(0, 1)-1, p.g.CursorC)
	case 'H', 'f':
		p.g.MoveCursor(p.param(0, 1)-1, p.param(1, 1)-1)
	case 'J':
		p.g.EraseInDisplay(p.param(0, 0))
	case 'K':
		p.g.EraseInLine(p.param(0, 0))
	case 'r':
		// DECSTBM — set top/bottom margins. Defaults: top=1,
		// bottom=Rows. Convert to 0-based inclusive.
		top := p.param(0, 1) - 1
		bot := p.param(1, p.g.Rows) - 1
		p.g.SetScrollRegion(top, bot)
	case 'L':
		p.g.InsertLines(p.param(0, 1))
	case 'M':
		p.g.DeleteLines(p.param(0, 1))
	case '@':
		p.g.InsertChars(p.param(0, 1))
	case 'P':
		p.g.DeleteChars(p.param(0, 1))
	case 'S':
		p.g.ScrollUp(p.param(0, 1))
	case 'T':
		p.g.ScrollDown(p.param(0, 1))
	case 'c':
		// DA1 — Primary Device Attributes. Reply only for the
		// no-parameter or "0" form (the DEC private "?c" / DA2
		// "> c" forms have intermediates we don't track and are
		// ignored). fish probes this at startup.
		if p.param(0, 0) == 0 && p.onReply != nil {
			p.onReply(da1Reply)
		}
	case 'q':
		// DECSCUSR — set cursor style + blink (CSI Ps SP q).
		// Without the SP intermediate this is a different sequence
		// (DECSCA / DECLL); ignore those.
		if p.intermediate == ' ' {
			p.g.ApplyDECSCUSR(p.param(0, 0))
		}
	default:
		// Unknown CSI — drop. Includes scroll regions, mode set/reset,
		// device status, etc.
	}
}

// applyDECMode handles DEC private mode set/reset (CSI ? Pn h / l).
// Only the modes the widget honors are wired; unknown modes are
// silently dropped so apps that probe many modes don't break.
func (p *Parser) applyDECMode(set bool) {
	for _, n := range p.params {
		switch n {
		case 25: // DECTCEM — text cursor enable
			p.g.CursorVisible = set
		case 47, 1047: // alt screen (no save/restore of cursor)
			if set {
				p.g.EnterAlt()
			} else {
				p.g.ExitAlt()
			}
		case 1049: // alt screen with cursor save/restore (xterm)
			if set {
				p.g.SaveCursor()
				p.g.EnterAlt()
			} else {
				p.g.ExitAlt()
				p.g.RestoreCursor()
			}
		case 2004: // bracketed paste mode
			p.g.BracketedPaste = set
		case 1000: // mouse: press/release
			p.g.MouseTrack = set
		case 1002: // mouse: press/release + drag
			p.g.MouseTrackBtn = set
		case 1003: // mouse: any motion
			p.g.MouseTrackAny = set
		case 1006: // mouse: SGR encoding
			p.g.MouseSGR = set
		}
	}
}

// param returns params[i] or def if missing or zero (per VT semantics
// where "0" often means "1" for cursor moves).
func (p *Parser) param(i, def int) int {
	if i >= len(p.params) {
		return def
	}
	if p.params[i] == 0 {
		return def
	}
	return p.params[i]
}

// applyExtendedColor handles SGR 38/48 sub-forms (;5;n and ;2;r;g;b)
// starting at params[i] (the 38 or 48 itself). Returns the new value
// of i — the outer loop's `i++` advances past the last param consumed.
// On truncation, returns len(params)-1 so the outer loop exits cleanly.
func applyExtendedColor(g *Grid, params []int, i int, isBG bool) int {
	// Defensive: caller is always applySGR with i in [0, len(params)),
	// but guard against future misuse (negative i, empty/nil params).
	if i < 0 || i+1 >= len(params) {
		return len(params) - 1
	}
	target := &g.CurFG
	if isBG {
		target = &g.CurBG
	}
	switch params[i+1] {
	case 5:
		if i+2 >= len(params) {
			return len(params) - 1
		}
		*target = paletteColor(clampU8(params[i+2]))
		return i + 2
	case 2:
		if i+4 >= len(params) {
			return len(params) - 1
		}
		*target = rgbColor(
			clampU8(params[i+2]),
			clampU8(params[i+3]),
			clampU8(params[i+4]),
		)
		return i + 4
	default:
		// Unknown selector; consume the rest of the SGR sequence.
		return len(params) - 1
	}
}

// clampU8 saturates an int to 0..255.
func clampU8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func (p *Parser) applySGR() {
	g := p.g
	if len(p.params) == 0 {
		// Bare "CSI m" == "CSI 0 m".
		g.CurFG, g.CurBG, g.CurAttrs = DefaultColor, DefaultColor, 0
		return
	}
	for i := 0; i < len(p.params); i++ {
		n := p.params[i]
		switch {
		case n == 0:
			g.CurFG, g.CurBG, g.CurAttrs = DefaultColor, DefaultColor, 0
		case n == 1:
			g.CurAttrs |= AttrBold
		case n == 4:
			g.CurAttrs |= AttrUnderline
		case n == 7:
			g.CurAttrs |= AttrInverse
		case n == 22:
			g.CurAttrs &^= AttrBold
		case n == 24:
			g.CurAttrs &^= AttrUnderline
		case n == 27:
			g.CurAttrs &^= AttrInverse
		case n >= 30 && n <= 37:
			g.CurFG = paletteColor(uint8(n - 30))
		case n == 39:
			g.CurFG = DefaultColor
		case n >= 40 && n <= 47:
			g.CurBG = paletteColor(uint8(n - 40))
		case n == 49:
			g.CurBG = DefaultColor
		case n >= 90 && n <= 97:
			g.CurFG = paletteColor(uint8(n - 90 + 8))
		case n >= 100 && n <= 107:
			g.CurBG = paletteColor(uint8(n - 100 + 8))
		case n == 38 || n == 48:
			// Extended color: 38=fg, 48=bg. Sub-form selector lives
			// in the next param. ;5;n  → 256-color palette index.
			// ;2;r;g;b → 24-bit truecolor. Truncated forms consume
			// whatever params remain (no panic, no half-applied).
			i = applyExtendedColor(g, p.params, i, n == 48)
		}
	}
}
