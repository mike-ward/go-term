package term

import "strconv"

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

				if p.onReply != nil {
					b := make([]byte, 0, 16)
					b = append(b, "\x1b[?"...)
					b = strconv.AppendUint(b, uint64(p.g.KittyKeyFlags), 10)
					b = append(b, 'u')
					p.onReply(b)
				}
			}
		case '>':

			if final == 'u' {
				p.g.PushKittyKeyFlags(uint32(p.param(0, 0)))
			}
		case '<':

			if final == 'u' {
				p.g.PopKittyKeyFlags(p.param(0, 1))
			}
		case '=':

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

		if p.param(0, 0) == 0 && p.onReply != nil {
			p.onReply(da1Reply)
		}
	case 'n':

		if p.param(0, 0) == 6 && p.onReply != nil {
			row, col := p.g.CursorR+1, p.g.CursorC+1
			p.onReply([]byte("\x1b[" + strconv.Itoa(row) + ";" + strconv.Itoa(col) + "R"))
		}
	case 'g':

		switch p.param(0, 0) {
		case 0:
			p.g.ClearTabStop(false)
		case 3:
			p.g.ClearTabStop(true)
		}
	case 'q':

		if p.intermediate == ' ' {
			p.g.ApplyDECSCUSR(p.param(0, 0))
		}
	default:

	}
}

// applyDECMode handles DEC private mode set/reset (CSI ? Pn h / l).
// Only the modes the widget honors are wired; unknown modes are
// silently dropped so apps that probe many modes don't break.
func (p *Parser) applyDECMode(set bool) {
	for _, n := range p.params {
		switch n {
		case 25:
			p.g.CursorVisible = set
		case 47, 1047:
			if set {
				p.g.EnterAlt()
			} else {
				p.g.ExitAlt()
			}
		case 1049:
			if set {
				p.g.SaveCursor()
				p.g.EnterAlt()
			} else {
				p.g.ExitAlt()
				p.g.RestoreCursor()
			}
		case 2004:
			p.g.BracketedPaste = set
		case 1004:
			p.g.FocusReporting = set
		case 2026:
			p.g.SyncOutput = set
			if !set {
				p.g.SyncActive = false
			}
		case 7:
			p.g.AutoWrap = set
		case 6:
			p.g.OriginMode = set
			if set && p.g.regionValid() {
				p.g.CursorR, p.g.CursorC = p.g.Top, 0
			} else if !set {
				p.g.CursorR, p.g.CursorC = 0, 0
			}
		case 1:
			p.g.AppCursorKeys = set
		case 66:
			p.g.AppKeypad = set
		case 1000:
			p.g.MouseTrack = set
		case 1002:
			p.g.MouseTrackBtn = set
		case 1003:
			p.g.MouseTrackAny = set
		case 1006:
			p.g.MouseSGR = set
		case 1016:
			p.g.MouseSGRPixels = set
		}
	}
}

func (p *Parser) applyMode(set bool) {
	for _, n := range p.params {
		switch n {
		case 4:
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

			ulStyle := ULSingle
			if i+1 < len(p.params) && i+1 < len(p.paramSub) && p.paramSub[i+1] {
				sub := p.params[i+1]
				i++
				if sub == 0 {

					g.CurAttrs &^= AttrUnderline
					g.CurULStyle = 0
					continue
				}
				if sub < 1 || sub > 5 {
					continue
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

			target := &g.CurFG
			if n == 48 {
				target = &g.CurBG
			}
			i = applyExtendedColor(p.params, i, target)
		case n == 58:

			i = applyExtendedColor(p.params, i, &g.CurULColor)
		case n == 59:

			g.CurULColor = DefaultColor
		}
	}
}
