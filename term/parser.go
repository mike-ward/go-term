package term

import "unicode/utf8"

type parserState uint8

const (
	stGround parserState = iota
	stEsc
	stCSI
)

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
	g      *Grid
	state  parserState
	params []int   // SGR params accumulated in current CSI
	curP   int     // value being accumulated
	hasP   bool    // any digit seen for curP
	utf    [4]byte // UTF-8 carry-over between Feed calls
	utfLen int
}

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
			default:
				// 2-byte ESC sequences: ignore.
				p.state = stGround
			}
			i++
		case stCSI:
			switch {
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
			default:
				// Intermediate or unsupported byte — keep going.
			}
			i++
		}
	}
}

func (p *Parser) dispatchCSI(final byte) {
	switch final {
	case 'm':
		p.applySGR()
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
	default:
		// Unknown CSI — drop. Includes scroll regions, mode set/reset,
		// device status, etc.
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
