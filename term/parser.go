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
			case c == 0x07:
				p.g.Bell()
				i++
			case c == 0x08:
				p.g.Backspace()
				i++
			case c == 0x09:
				p.g.Tab()
				i++
			case c == 0x0A:
				p.g.Newline()
				i++
			case c == 0x0D:
				p.g.CarriageReturn()
				i++
			case c == 0x0E:
				p.g.ActiveG = 1
				i++
			case c == 0x0F:
				p.g.ActiveG = 0
				i++
			case c == 0x1B:
				p.state = stEsc
				i++
			case c < 0x20:
				i++
			default:

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
			case ']':
				p.state = stOSC
				p.osc = p.osc[:0]
			case 'P':
				p.state = stDCS
				p.dcs = p.dcs[:0]
			case '7':
				p.g.SaveCursor()
				p.state = stGround
			case '8':
				p.g.RestoreCursor()
				p.state = stGround
			case 'D':
				p.g.Newline()
				p.state = stGround
			case 'M':
				p.g.ReverseIndex()
				p.state = stGround
			case 'E':
				p.g.NextLine()
				p.state = stGround
			case 'H':
				p.g.SetTabStop()
				p.state = stGround
			case '=':
				p.g.AppKeypad = true
				p.state = stGround
			case '>':
				p.g.AppKeypad = false
				p.state = stGround
			case '(', ')', '*', '+', '-', '.', '/':

				p.escInter = c
				p.state = stEscInter
			default:

				p.state = stGround
			}
			i++
		case stEscInter:

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
				p.nextIsSub = false
			case c == ':':

				if len(p.params) < maxCSIParams {
					p.params = append(p.params, p.curP)
					p.paramSub = append(p.paramSub, p.nextIsSub)
				}
				p.curP = 0
				p.hasP = false
				p.nextIsSub = true
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

				p.intermediate = c
			default:

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
			case 0x07:
				p.dispatchOSC()
				p.state = stGround
			case 0x1B:
				p.state = stOSCEsc
			default:
				if len(p.osc) < maxOSCBytes {
					p.osc = append(p.osc, c)
				}
			}
			i++
		case stOSCEsc:
			if c == '\\' {
				p.dispatchOSC()
				p.state = stGround
				i++
			} else {

				p.osc = p.osc[:0]
				p.state = stEsc
			}
		}
	}
}

// maxXTGETTCAPParts caps the number of capability names in one XTGETTCAP
// query so a pathological DCS (4096 semicolons) can't force a large
// allocation or iteration. Real apps query 1–3 caps at a time.
const maxXTGETTCAPParts = 32

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
