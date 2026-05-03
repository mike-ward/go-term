package term

import (
	"strconv"
	"unicode/utf8"
)

type parserState uint8

const (
	stGround parserState = iota
	stEsc
	stEscInter
	stCSI
	stDCS
	stDCSEsc
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
	g            *Grid
	state        parserState
	params       []int   // SGR params accumulated in current CSI
	curP         int     // value being accumulated
	hasP         bool    // any digit seen for curP
	leader       byte    // optional CSI private leader: one of < = > ?
	intermediate byte    // last intermediate byte (0x20..0x2F) seen, 0 if none
	escInter     byte    // ESC intermediate introducer like '(' in ESC(B
	utf          [4]byte // UTF-8 carry-over between Feed calls
	utfLen       int

	// osc accumulates the payload of the in-progress OSC (Operating
	// System Command). Reset on entry to stOSC; capped at maxOSCBytes.
	osc []byte
	dcs []byte

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
				p.leader = 0
				p.intermediate = 0
			case ']': // OSC introducer
				p.state = stOSC
				p.osc = p.osc[:0]
			case 'P': // DCS introducer
				p.state = stDCS
				p.dcs = p.dcs[:0]
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
			case '=': // DECPAM — application keypad
				p.g.AppKeypad = true
				p.state = stGround
			case '>': // DECPNM — numeric keypad
				p.g.AppKeypad = false
				p.state = stGround
			case '(', ')', '*', '+', '-', '.', '/':
				// Character-set / other ESC-intermediate sequences such
				// as ESC ( B are common in TUIs. We don't implement the
				// designation, but must swallow the final byte so it
				// doesn't render as a literal 'B'.
				p.escInter = c
				p.state = stEscInter
			default:
				// 2-byte ESC sequences: ignore.
				p.state = stGround
			}
			i++
		case stEscInter:
			// Swallow the final byte of an ESC intermediate sequence
			// like ESC ( B, then return to ground.
			p.escInter = 0
			p.state = stGround
			i++
		case stCSI:
			switch {
			case c >= '<' && c <= '?' && p.leader == 0 && !p.hasP && len(p.params) == 0:
				// CSI private leader bytes (0x3C..0x3F) select
				// non-standard/xterm families such as DEC private
				// modes (`?`) and modifyOtherKeys (`>`). Record the
				// leader so we don't later misread `>4;1m` as plain
				// SGR 4;1m.
				p.leader = c
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
				p.leader = 0
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
		case stDCS:
			switch c {
			case 0x1B:
				p.state = stDCSEsc
			default:
				if len(p.dcs) < maxOSCBytes {
					p.dcs = append(p.dcs, c)
				}
			}
			i++
		case stDCSEsc:
			if c == '\\' {
				p.dispatchDCS()
				p.state = stGround
				i++
			} else {
				p.dcs = p.dcs[:0]
				p.state = stEsc
			}
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

func encodeHexBytes(s string) []byte {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 0, len(s)*2)
	for i := 0; i < len(s); i++ {
		b := s[i]
		out = append(out, hexdigits[b>>4], hexdigits[b&0x0F])
	}
	return out
}

func decodeHexBytes(b []byte) (string, bool) {
	if len(b)%2 != 0 {
		return "", false
	}
	out := make([]byte, len(b)/2)
	for i := 0; i < len(b); i += 2 {
		hi := fromHexNibble(b[i])
		lo := fromHexNibble(b[i+1])
		if hi < 0 || lo < 0 {
			return "", false
		}
		out[i/2] = byte(hi<<4 | lo)
	}
	return string(out), true
}

func fromHexNibble(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	default:
		return -1
	}
}

// maxXTGETTCAPParts caps the number of capability names in one XTGETTCAP
// query so a pathological DCS (4096 semicolons) can't force a large
// allocation or iteration. Real apps query 1–3 caps at a time.
const maxXTGETTCAPParts = 32

func splitSemis(b []byte) [][]byte {
	if len(b) == 0 {
		return nil
	}
	out := make([][]byte, 0, 4)
	start := 0
	for i, c := range b {
		if c == ';' {
			if len(out) >= maxXTGETTCAPParts {
				return out
			}
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	out = append(out, b[start:])
	return out
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

func appendReply(out []byte, body []byte) []byte {
	out = append(out, '\x1b', 'P')
	out = append(out, body...)
	out = append(out, '\x1b', '\\')
	return out
}

func (p *Parser) currentSGRString() string {
	if p.g.CurFG == DefaultColor && p.g.CurBG == DefaultColor && p.g.CurAttrs == 0 {
		return "0m"
	}
	params := make([]byte, 0, 32)
	appendParam := func(s string) {
		if len(params) > 0 {
			params = append(params, ';')
		}
		params = append(params, s...)
	}
	if p.g.CurAttrs&AttrBold != 0 {
		appendParam("1")
	}
	if p.g.CurAttrs&AttrUnderline != 0 {
		appendParam("4")
	}
	if p.g.CurAttrs&AttrInverse != 0 {
		appendParam("7")
	}
	switch p.g.CurFG >> 24 {
	case 0x00:
		v := int(p.g.CurFG & 0xFF)
		switch {
		case v <= 7:
			appendParam(strconv.Itoa(v + 30))
		case v <= 15:
			appendParam(strconv.Itoa(v - 8 + 90))
		default:
			appendParam("38")
			appendParam("5")
			appendParam(strconv.Itoa(v))
		}
	case 0x01:
		appendParam("38")
		appendParam("2")
		appendParam(strconv.Itoa(int((p.g.CurFG >> 16) & 0xFF)))
		appendParam(strconv.Itoa(int((p.g.CurFG >> 8) & 0xFF)))
		appendParam(strconv.Itoa(int(p.g.CurFG & 0xFF)))
	}
	switch p.g.CurBG >> 24 {
	case 0x00:
		v := int(p.g.CurBG & 0xFF)
		switch {
		case v <= 7:
			appendParam(strconv.Itoa(v + 40))
		case v <= 15:
			appendParam(strconv.Itoa(v - 8 + 100))
		default:
			appendParam("48")
			appendParam("5")
			appendParam(strconv.Itoa(v))
		}
	case 0x01:
		appendParam("48")
		appendParam("2")
		appendParam(strconv.Itoa(int((p.g.CurBG >> 16) & 0xFF)))
		appendParam(strconv.Itoa(int((p.g.CurBG >> 8) & 0xFF)))
		appendParam(strconv.Itoa(int(p.g.CurBG & 0xFF)))
	}
	if len(params) == 0 {
		return "0m"
	}
	return string(params) + "m"
}

func (p *Parser) replyDECRQSS(body []byte) {
	if p.onReply == nil {
		return
	}
	// Valid DECRQSS bodies are "m", "r", " q" — max 2 bytes. Reject
	// longer bodies early so we never convert a 4 KB body to a string.
	if len(body) > 4 {
		p.onReply(appendReply(nil, []byte("0$r")))
		return
	}
	out := make([]byte, 0, 32)
	switch string(body) {
	case "m":
		out = appendReply(out, append([]byte("1$r"), []byte(p.currentSGRString())...))
	case "r":
		top := p.g.Top + 1
		bot := p.g.Bottom + 1
		out = appendReply(out, []byte("1$r"+strconv.Itoa(top)+";"+strconv.Itoa(bot)+"r"))
	case " q":
		out = appendReply(out, []byte("1$r"+strconv.Itoa(p.g.DECSCUSRParam())+" q"))
	default:
		out = appendReply(out, []byte("0$r"))
	}
	p.onReply(out)
}

func xtgettcapValue(name string) (string, bool) {
	switch name {
	case "TN", "name":
		return "xterm-256color", true
	case "Co", "colors":
		return "256", true
	case "RGB":
		return "8/8/8", true
	case "kcuu1":
		return "\x1b[A", true
	case "kcud1":
		return "\x1b[B", true
	case "kcub1":
		return "\x1b[D", true
	case "kcuf1":
		return "\x1b[C", true
	case "khome":
		return "\x1b[H", true
	case "kend":
		return "\x1b[F", true
	case "kich1":
		return "\x1b[2~", true
	case "kdch1":
		return "\x1b[3~", true
	case "kpp":
		return "\x1b[5~", true
	case "knp":
		return "\x1b[6~", true
	case "indn":
		return "\x1b[%p1%dS", true
	case "query-os-name":
		return "\x1b]0;?\x07", true
	case "smkx":
		return "\x1b[?1h\x1b=", true
	case "rmkx":
		return "\x1b[?1l\x1b>", true
	default:
		return "", false
	}
}

func (p *Parser) replyXTGETTCAP(body []byte) {
	if p.onReply == nil {
		return
	}
	parts := splitSemis(body)
	if len(parts) == 0 {
		p.onReply(appendReply(nil, []byte("0+r")))
		return
	}
	payload := make([]byte, 0, len(body)+32)
	payload = append(payload, "1+r"...)
	for i, part := range parts {
		name, ok := decodeHexBytes(part)
		if !ok {
			p.onReply(appendReply(nil, append([]byte("0+r"), part...)))
			return
		}
		value, ok := xtgettcapValue(name)
		if !ok {
			p.onReply(appendReply(nil, append([]byte("0+r"), part...)))
			return
		}
		if i > 0 {
			payload = append(payload, ';')
		}
		payload = append(payload, part...)
		payload = append(payload, '=')
		payload = append(payload, encodeHexBytes(value)...)
	}
	p.onReply(appendReply(nil, payload))
}

func (p *Parser) dispatchDCS() {
	if len(p.dcs) < 2 {
		return
	}
	switch {
	case p.dcs[0] == '$' && p.dcs[1] == 'q':
		p.replyDECRQSS(p.dcs[2:])
	case p.dcs[0] == '+' && p.dcs[1] == 'q':
		p.replyXTGETTCAP(p.dcs[2:])
	case p.g.SyncOutput && len(p.dcs) >= 3 && p.dcs[0] == '=' && p.dcs[2] == 's':
		switch p.dcs[1] {
		case '1':
			p.g.SyncActive = true
		case '2':
			p.g.SyncActive = false
		}
	}
}

func (p *Parser) dispatchCSI(final byte) {
	if p.leader != 0 {
		switch p.leader {
		case '?':
			switch final {
			case 'h':
				p.applyDECMode(true)
			case 'l':
				p.applyDECMode(false)
			}
		}
		return
	}
	switch final {
	case 'h':
		p.applyMode(true)
	case 'l':
		p.applyMode(false)
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
		p.g.CursorDown(p.param(0, 1))
		p.g.MoveCursor(p.g.CursorR, 0)
	case 'F':
		p.g.CursorUp(p.param(0, 1))
		p.g.MoveCursor(p.g.CursorR, 0)
	case 'G', '`':
		p.g.MoveCursor(p.g.CursorR, p.param(0, 1)-1)
	case 'd':
		p.g.MoveCursorOrigin(p.param(0, 1)-1, p.g.CursorC)
	case 'H', 'f':
		p.g.MoveCursorOrigin(p.param(0, 1)-1, p.param(1, 1)-1)
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
	case 'n':
		// DSR / CPR.
		if p.param(0, 0) == 6 && p.onReply != nil {
			row, col := p.g.CursorR+1, p.g.CursorC+1
			p.onReply([]byte("\x1b[" + strconv.Itoa(row) + ";" + strconv.Itoa(col) + "R"))
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
		case 1004: // focus in/out reports
			p.g.FocusReporting = set
		case 2026: // synchronized updates mode
			p.g.SyncOutput = set
			if !set {
				p.g.SyncActive = false
			}
		case 7: // DECAWM — autowrap at right margin
			p.g.AutoWrap = set
		case 6: // DECOM — origin mode
			p.g.OriginMode = set
			if set && p.g.regionValid() {
				p.g.CursorR, p.g.CursorC = p.g.Top, 0
			} else if !set {
				p.g.CursorR, p.g.CursorC = 0, 0
			}
		case 1: // DECCKM — application cursor keys
			p.g.AppCursorKeys = set
		case 66: // DECNKM — application keypad
			p.g.AppKeypad = set
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

func (p *Parser) applyMode(set bool) {
	for _, n := range p.params {
		switch n {
		case 4: // IRM — insert/replace mode
			p.g.InsertMode = set
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
