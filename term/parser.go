package term

import (
	"encoding/base64"
	"strconv"
	"strings"
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

// maxDCSBytes caps DCS payloads. Sixel images can be sizable (a small
// 320×240 sample is ~50 KB of sixel data); 1 MiB tolerates real-world
// frames while keeping a malicious stream bounded.
const maxDCSBytes = 1 << 20

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
	paramSub     []bool  // paramSub[i] true when params[i] was colon-separated from params[i-1]
	curP         int     // value being accumulated
	hasP         bool    // any digit seen for curP
	nextIsSub    bool    // pending: next param pushed will be marked as sub-param
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
	// bytes back toward the application (e.g. DA1 response).
	// onClipboard, if non-nil, is invoked for OSC 52 clipboard-write
	// requests. All three run while Grid.Mu is held — handlers must not
	// re-enter the grid.
	onTitle     func(string)
	onReply     func([]byte)
	onClipboard func([]byte)

	// graphicsDir is the directory where decoded Sixel PNGs are written.
	// Empty = os.TempDir(). Set via SetGraphicsDir; the widget creates a
	// per-Term subdirectory and removes it on Close.
	graphicsDir string
}

// SetGraphicsDir tells the parser where to write decoded Sixel images.
// Empty string falls back to os.TempDir(). The widget creates a private
// subdir per Term so cleanup on Close removes only its own files.
func (p *Parser) SetGraphicsDir(dir string) { p.graphicsDir = dir }

// SetTitleHandler registers a callback for OSC 0/1/2. Pass nil to
// disable. Called while Grid.Mu is held.
func (p *Parser) SetTitleHandler(fn func(string)) { p.onTitle = fn }

// SetReplyHandler registers a callback for parser-originated host
// writes (DA1 today; future: cursor position reports, etc.). Called
// while Grid.Mu is held.
func (p *Parser) SetReplyHandler(fn func([]byte)) { p.onReply = fn }

// SetClipboardHandler registers a callback for OSC 52 clipboard-write
// requests. data is the decoded (raw) clipboard payload. Pass nil to
// disable. Called while Grid.Mu is held.
func (p *Parser) SetClipboardHandler(fn func([]byte)) { p.onClipboard = fn }

// NewParser binds a parser to a grid. Callers must hold g.Mu while calling
// Feed.
func NewParser(g *Grid) *Parser {
	return &Parser{g: g, params: make([]int, 0, 8), paramSub: make([]bool, 0, 8)}
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
				p.g.Bell()
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
			case c == 0x0E: // SO — shift out: invoke G1 into GL
				p.g.ActiveG = 1
				i++
			case c == 0x0F: // SI — shift in: invoke G0 into GL
				p.g.ActiveG = 0
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
				p.paramSub = p.paramSub[:0]
				p.curP = 0
				p.hasP = false
				p.nextIsSub = false
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
			case 'H': // HTS — set tab stop at current column
				p.g.SetTabStop()
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
			// Finalize ESC intermediate sequences such as ESC ( 0 and
			// ESC ) B, then return to ground.
			switch p.escInter {
			case '(':
				p.g.CharsetG0 = c
			case ')':
				p.g.CharsetG1 = c
			}
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
					p.paramSub = append(p.paramSub, p.nextIsSub)
				}
				p.curP = 0
				p.hasP = false
				p.nextIsSub = false // semicolon resets sub-param chain
			case c == ':':
				// Colon introduces a sub-parameter within the same logical
				// parameter group (e.g. "4:3" = curly underline).
				if len(p.params) < maxCSIParams {
					p.params = append(p.params, p.curP)
					p.paramSub = append(p.paramSub, p.nextIsSub)
				}
				p.curP = 0
				p.hasP = false
				p.nextIsSub = true // next param is a sub-param of the one just pushed
			case c >= 0x40 && c <= 0x7E:
				if (p.hasP || len(p.params) > 0) && len(p.params) < maxCSIParams {
					p.params = append(p.params, p.curP)
					p.paramSub = append(p.paramSub, p.nextIsSub)
				}
				p.dispatchCSI(c)
				p.state = stGround
				p.curP = 0
				p.hasP = false
				p.leader = 0
				p.intermediate = 0
				p.nextIsSub = false
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
				if len(p.dcs) < maxDCSBytes {
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
	case 10, 11, 12:
		// Dynamic colors: 10=foreground, 11=background, 12=cursor.
		// "?" queries the current color; anything else sets it.
		if pt == "?" {
			r, g, b := p.g.dynColorRGB(ps)
			reply := "\x1b]" + strconv.Itoa(ps) + ";rgb:" +
				oscHexWord(r) + "/" + oscHexWord(g) + "/" + oscHexWord(b) + "\x1b\\"
			if p.onReply != nil {
				p.onReply([]byte(reply))
			}
			return
		}
		if c, ok := parseXColor(pt); ok {
			p.g.SetDynColor(ps, c)
		}
	case 8:
		// Hyperlink: OSC 8;params;URI ST
		// params (before the ';') may carry an id= hint for multiplexers;
		// we ignore it and dedup solely by URL.
		semiIdx := strings.IndexByte(pt, ';')
		if semiIdx < 0 {
			return // malformed: no second semicolon
		}
		uri := pt[semiIdx+1:]
		if uri == "" {
			p.g.CurLinkID = 0 // close link
		} else {
			p.g.CurLinkID = p.g.internLink(uri)
		}
	case 133:
		// Semantic shell integration (FinalTerm / iTerm2 protocol).
		// Payload: single letter A–D optionally followed by ";key=value" pairs.
		// Only the first byte determines the mark kind; extra params ignored.
		if len(pt) == 0 {
			return
		}
		switch pt[0] {
		case 'A':
			p.g.AddMark(MarkPromptStart)
		case 'B':
			p.g.AddMark(MarkCommandStart)
		case 'C':
			p.g.AddMark(MarkOutputStart)
		case 'D':
			p.g.AddMark(MarkCommandEnd)
		}
	case 52:
		// Clipboard write: OSC 52;c;base64data ST
		// c = clipboard target selector (we accept any value, treat as
		// the system clipboard). "?" as base64 data is a read request —
		// ignored (requires async UI-thread access to GetClipboard).
		semiIdx := strings.IndexByte(pt, ';')
		if semiIdx < 0 {
			return
		}
		b64 := pt[semiIdx+1:]
		if b64 == "?" {
			return
		}
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return
		}
		if p.onClipboard != nil {
			p.onClipboard(data)
		}
	}
}

// parseXColor parses an X11 color string into a packed rgbColor.
// Accepts "rgb:H/H/H" through "rgb:HHHH/HHHH/HHHH" and "#RRGGBB".
func parseXColor(s string) (uint32, bool) {
	if strings.HasPrefix(s, "rgb:") {
		parts := strings.SplitN(s[4:], "/", 3)
		if len(parts) != 3 {
			return 0, false
		}
		var ch [3]uint8
		for i, p := range parts {
			if len(p) == 0 || len(p) > 4 {
				return 0, false
			}
			n, err := strconv.ParseUint(p, 16, 64)
			if err != nil {
				return 0, false
			}
			// Scale to 8-bit. XParseColor convention: 1-digit repeats
			// the nibble, 2-digit is exact, 3/4-digit take the high byte.
			switch len(p) {
			case 1:
				ch[i] = uint8(n * 0x11)
			case 2:
				ch[i] = uint8(n)
			case 3:
				ch[i] = uint8(n >> 4)
			case 4:
				ch[i] = uint8(n >> 8)
			}
		}
		return rgbColor(ch[0], ch[1], ch[2]), true
	}
	if len(s) == 7 && s[0] == '#' {
		n, err := strconv.ParseUint(s[1:], 16, 32)
		if err != nil {
			return 0, false
		}
		return rgbColor(uint8(n>>16), uint8(n>>8), uint8(n)), true
	}
	return 0, false
}

// oscHexWord expands an 8-bit color component to a 4-hex-digit string
// by repeating the byte (e.g. 0xAB → "abab"), matching xterm convention.
func oscHexWord(n uint8) string {
	v := uint16(n)<<8 | uint16(n)
	const hx = "0123456789abcdef"
	return string([]byte{hx[v>>12], hx[(v>>8)&0xF], hx[(v>>4)&0xF], hx[v&0xF]})
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
		// Bare "Pq…" (no params, no data) is a malformed sixel introducer;
		// also covers the single-char DCS form.
		if len(p.dcs) == 1 && p.dcs[0] == 'q' {
			p.handleSixel(nil)
		}
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
	default:
		// Sixel: DCS introducer is `Pp1;Pp2;Pp3 q <data>`. Params are
		// digits and semicolons, terminated by 'q' (the sixel final
		// byte). Anything else falls through.
		if q := indexSixelFinal(p.dcs); q >= 0 {
			p.handleSixel(p.dcs[q+1:])
		}
	}
}

// indexSixelFinal returns the index of the 'q' final byte that
// introduces a Sixel data stream, or -1 if the payload prefix is not a
// valid sixel param list. The prefix may be empty (bare 'q') or a
// sequence of digits / semicolons.
func indexSixelFinal(dcs []byte) int {
	for i, b := range dcs {
		switch {
		case b == 'q':
			return i
		case b >= '0' && b <= '9', b == ';':
			// param byte; keep scanning
		default:
			return -1
		}
	}
	return -1
}

// handleSixel decodes a Sixel payload (bytes after the 'q' introducer)
// and stashes the resulting image as a Grid graphic anchored at the
// cursor. Cursor advances past the image's vertical extent so following
// text starts below it (xterm convention). Decode failures are silent.
func (p *Parser) handleSixel(data []byte) {
	img := decodeSixel(data)
	if img == nil {
		return
	}
	b := img.Bounds()
	path := encodePNGFile(img, p.graphicsDir)
	if path == "" {
		return
	}
	_, rows := p.g.AddGraphic(path, b.Dx(), b.Dy())
	for range rows {
		p.g.Newline()
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
			case 'u':
				// KKP query: reply CSI ? flags u with current flags.
				if p.onReply != nil {
					b := make([]byte, 0, 16)
					b = append(b, "\x1b[?"...)
					b = strconv.AppendUint(b, uint64(p.g.KittyKeyFlags), 10)
					b = append(b, 'u')
					p.onReply(b)
				}
			}
		case '>':
			// CSI > flags u — push current flags, OR in new flags.
			if final == 'u' {
				p.g.PushKittyKeyFlags(uint32(p.param(0, 0)))
			}
		case '<':
			// CSI < n u — pop n levels from KKP flag stack.
			if final == 'u' {
				p.g.PopKittyKeyFlags(p.param(0, 1))
			}
		case '=':
			// CSI = flags u — set KKP flags without stack push.
			if final == 'u' {
				p.g.SetKittyKeyFlags(uint32(p.param(0, 0)))
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
	case 'g':
		// TBC — tabulation clear. Ps=0 (default): clear stop at cursor.
		// Ps=3: clear all tab stops.
		switch p.param(0, 0) {
		case 0:
			p.g.ClearTabStop(false)
		case 3:
			p.g.ClearTabStop(true)
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
		case 1016: // mouse: SGR pixel-precise coordinates
			p.g.MouseSGRPixels = set
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

// applyExtendedColor handles SGR 38/48/58 sub-forms (;5;n and ;2;r;g;b)
// starting at params[i] (the 38/48/58 itself). target receives the result.
// Returns the new value of i; the outer loop's `i++` advances past the last
// param consumed. On truncation, returns len(params)-1.
func applyExtendedColor(params []int, i int, target *uint32) int {
	if i < 0 || i+1 >= len(params) {
		return len(params) - 1
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
		g.CurULStyle = 0
		g.CurULColor = DefaultColor
		return
	}
	for i := 0; i < len(p.params); i++ {
		n := p.params[i]
		switch {
		case n == 0:
			g.CurFG, g.CurBG, g.CurAttrs = DefaultColor, DefaultColor, 0
			g.CurULStyle = 0
			g.CurULColor = DefaultColor
		case n == 1:
			g.CurAttrs |= AttrBold
		case n == 2:
			g.CurAttrs |= AttrDim
		case n == 3:
			g.CurAttrs |= AttrItalic
		case n == 4:
			// SGR 4 with a colon sub-parameter selects the underline style:
			// 4:0=none 4:1=single 4:2=double 4:3=curly 4:4=dotted 4:5=dashed.
			// Plain SGR 4 (no sub-param) means single underline.
			ulStyle := ULSingle
			if i+1 < len(p.params) && i+1 < len(p.paramSub) && p.paramSub[i+1] {
				sub := p.params[i+1]
				i++ // consume sub-param; outer loop's i++ moves to next group
				if sub == 0 {
					// 4:0 = explicitly remove underline
					g.CurAttrs &^= AttrUnderline
					g.CurULStyle = 0
					continue
				}
				if sub < 1 || sub > 5 {
					continue // unknown sub-param: no-op per xterm convention
				}
				ulStyle = uint8(sub)
			}
			g.CurAttrs |= AttrUnderline
			g.CurULStyle = ulStyle
		case n == 7:
			g.CurAttrs |= AttrInverse
		case n == 9:
			g.CurAttrs |= AttrStrikethrough
		case n == 21:
			// Doubly underlined (SGR 21). Sets double-underline style.
			g.CurAttrs |= AttrUnderline
			g.CurULStyle = ULDouble
		case n == 22:
			g.CurAttrs &^= AttrBold | AttrDim
		case n == 23:
			g.CurAttrs &^= AttrItalic
		case n == 24:
			g.CurAttrs &^= AttrUnderline
			g.CurULStyle = 0
			g.CurULColor = DefaultColor
		case n == 27:
			g.CurAttrs &^= AttrInverse
		case n == 29:
			g.CurAttrs &^= AttrStrikethrough
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
			target := &g.CurFG
			if n == 48 {
				target = &g.CurBG
			}
			i = applyExtendedColor(p.params, i, target)
		case n == 58:
			// SGR 58: underline color. Same sub-forms as 38/48.
			i = applyExtendedColor(p.params, i, &g.CurULColor)
		case n == 59:
			// SGR 59: reset underline color to default (= use fg).
			g.CurULColor = DefaultColor
		}
	}
}
