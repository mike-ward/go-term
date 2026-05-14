package term

import "strconv"

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

func (p *Parser) replyDECRQSS(body []byte) {
	if p.onReply == nil {
		return
	}

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
