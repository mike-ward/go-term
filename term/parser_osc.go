package term

import (
	"encoding/base64"
	"strconv"
	"strings"
)

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

		if p.onTitle != nil {
			p.onTitle(pt)
		}
	case 7:

		p.g.Cwd = pt
	case 10, 11, 12:

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

		semiIdx := strings.IndexByte(pt, ';')
		if semiIdx < 0 {
			return
		}
		uri := pt[semiIdx+1:]
		if uri == "" {
			p.g.CurLinkID = 0
		} else {
			p.g.CurLinkID = p.g.internLink(uri)
		}
	case 133:

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
