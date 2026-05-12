package term

import (
	"crypto/sha1"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

// Graphic is a decoded image (e.g. Sixel) anchored at a content-row
// origin. The origin row is in *content* coordinates (same space as
// Marks and selection: 0..Scrollback.Len()-1 indexes scrollback,
// rows above that index the live grid). Origin is adjusted by the
// same scroll-eviction / shift logic as Marks so the image follows
// its surrounding text through history.
//
// Cells covered by the image are blanked at AddGraphic time so the
// text passes don't overstrike the image; the rendering pass paints
// the image on top of background fill.
type Graphic struct {
	Src      string // file path passed to dc.Image (PNG on disk)
	OriginR  int    // content-row index
	OriginC  int    // column at origin row
	Cols     int    // width in cells (covered rectangle)
	Rows     int    // height in cells
	WidthPx  int
	HeightPx int
}

const (
	// Hard caps on a single decoded Sixel frame. The widest in-the-wild
	// Sixel images are screenshots ≈1920×1080, so 4k×4k is plenty and
	// caps a single decode at ~64 MB of intermediate NRGBA buffer.
	maxSixelWidth  = 4096
	maxSixelHeight = 4096
	// Cap on Graphics retained by a Grid. Oldest are evicted first.
	maxGraphics = 256
)

// sixelDefaultPalette is the VT340 16-entry palette used when a Sixel
// stream selects a color register without defining it. Indices 16..255
// fall back to a grayscale ramp so reads are deterministic.
var sixelDefaultPalette = [16]color.NRGBA{
	{0x00, 0x00, 0x00, 0xFF}, // 0  Black
	{0x33, 0x33, 0xCC, 0xFF}, // 1  Blue
	{0xCC, 0x24, 0x24, 0xFF}, // 2  Red
	{0x33, 0xCC, 0x33, 0xFF}, // 3  Green
	{0xCC, 0x33, 0xCC, 0xFF}, // 4  Magenta
	{0x33, 0xCC, 0xCC, 0xFF}, // 5  Cyan
	{0xCC, 0xCC, 0x24, 0xFF}, // 6  Yellow
	{0x80, 0x80, 0x80, 0xFF}, // 7  Gray-50
	{0x44, 0x44, 0x44, 0xFF}, // 8  Gray-25
	{0x57, 0x57, 0x99, 0xFF}, // 9  Light blue
	{0x99, 0x57, 0x57, 0xFF}, // 10 Light red
	{0x57, 0x99, 0x57, 0xFF}, // 11 Light green
	{0x99, 0x57, 0x99, 0xFF}, // 12 Light magenta
	{0x57, 0x99, 0x99, 0xFF}, // 13 Light cyan
	{0x99, 0x99, 0x57, 0xFF}, // 14 Light yellow
	{0xCC, 0xCC, 0xCC, 0xFF}, // 15 Gray-75
}

// decodeSixel parses a Sixel DCS body (the bytes after the introducer's
// terminating 'q', i.e. the raster-attribute prefix + color definitions
// + sixel data). Returns the decoded image cropped to its actual extent.
// Returns nil for empty / oversized / unparseable input — the parser
// drops the graphic silently in that case.
func decodeSixel(data []byte) *image.NRGBA {
	var pal [256]color.NRGBA
	copy(pal[:], sixelDefaultPalette[:])
	for i := 16; i < 256; i++ {
		v := uint8(((i - 16) * 0xFF) / (256 - 16))
		pal[i] = color.NRGBA{v, v, v, 0xFF}
	}

	var (
		cur       uint8
		col       int
		band      int  // 6-row band index; band 0 covers pixels y=0..5
		repeat    int  // pending run-length (1 when not active)
		repActive bool
		width     int
		height    int
		img       *image.NRGBA
	)

	// grow reallocates img so x,y are addressable. Returns false on cap.
	grow := func(x, y int) bool {
		if x < 0 || y < 0 || x >= maxSixelWidth || y >= maxSixelHeight {
			return false
		}
		if img == nil {
			w, h := 64, 64
			for w <= x {
				w *= 2
			}
			for h <= y {
				h *= 2
			}
			img = image.NewNRGBA(image.Rect(0, 0, w, h))
			return true
		}
		b := img.Bounds()
		w, h := b.Dx(), b.Dy()
		nw, nh := w, h
		for nw <= x {
			nw *= 2
		}
		for nh <= y {
			nh *= 2
		}
		if nw == w && nh == h {
			return true
		}
		if nw > maxSixelWidth {
			nw = maxSixelWidth
		}
		if nh > maxSixelHeight {
			nh = maxSixelHeight
		}
		ni := image.NewNRGBA(image.Rect(0, 0, nw, nh))
		for y2 := range h {
			copy(ni.Pix[y2*ni.Stride:y2*ni.Stride+w*4],
				img.Pix[y2*img.Stride:y2*img.Stride+w*4])
		}
		img = ni
		return true
	}

	setPx := func(x, y int, c color.NRGBA) {
		if !grow(x, y) {
			return
		}
		off := y*img.Stride + x*4
		img.Pix[off+0] = c.R
		img.Pix[off+1] = c.G
		img.Pix[off+2] = c.B
		img.Pix[off+3] = c.A
		if x+1 > width {
			width = x + 1
		}
		if y+1 > height {
			height = y + 1
		}
	}

	writeSixel := func(s byte, count int) {
		if s < 0x3F || s > 0x7E || count <= 0 {
			return
		}
		bits := s - 0x3F // 6-bit vertical pixel mask, LSB = topmost
		baseY := band * 6
		c := pal[cur]
		for r := range count {
			x := col + r
			if x >= maxSixelWidth {
				break
			}
			for j := range 6 {
				if bits&(1<<j) != 0 {
					setPx(x, baseY+j, c)
				}
			}
		}
		col += count
	}

	i := 0
	n := len(data)
	for i < n {
		c := data[i]
		switch {
		case c == '"':
			// Raster attributes: "Pan;Pad;Ph;Pv — skip params; we
			// reflow on actual pixel writes anyway.
			i++
			for i < n && (isDigit(data[i]) || data[i] == ';') {
				i++
			}
		case c == '#':
			i++
			reg, ni := readInt(data, i)
			i = ni
			if i < n && data[i] == ';' {
				i++
				pu, ni := readInt(data, i)
				i = ni
				if i < n && data[i] == ';' {
					i++
				}
				px, ni := readInt(data, i)
				i = ni
				if i < n && data[i] == ';' {
					i++
				}
				py, ni := readInt(data, i)
				i = ni
				if i < n && data[i] == ';' {
					i++
				}
				pz, ni := readInt(data, i)
				i = ni
				if reg >= 0 && reg < 256 {
					switch pu {
					case 2:
						pal[reg] = color.NRGBA{
							uint8(clamp100(px) * 0xFF / 100),
							uint8(clamp100(py) * 0xFF / 100),
							uint8(clamp100(pz) * 0xFF / 100),
							0xFF,
						}
					default: // 1 = HLS (or unspecified — treat as HLS)
						pal[reg] = hlsToRGB(px, py, pz)
					}
					// Per DEC: a color-definition also selects the
					// register as current, so subsequent sixel data
					// without a separate "# Pc" still uses it.
					cur = uint8(reg)
				}
			} else if reg >= 0 && reg < 256 {
				cur = uint8(reg)
			}
		case c == '!':
			i++
			n1, ni := readInt(data, i)
			i = ni
			if n1 < 1 {
				n1 = 1
			}
			repeat = n1
			repActive = true
		case c == '$':
			col = 0
			i++
		case c == '-':
			col = 0
			band++
			i++
		case c >= 0x3F && c <= 0x7E:
			cnt := 1
			if repActive {
				cnt = repeat
				repActive = false
			}
			writeSixel(c, cnt)
			i++
		default:
			// Whitespace and unknown bytes: drop.
			i++
		}
	}
	if img == nil || width == 0 || height == 0 {
		return nil
	}
	return img.SubImage(image.Rect(0, 0, width, height)).(*image.NRGBA)
}

// readInt scans an unsigned decimal at data[i:]. Returns the value and
// the index of the first non-digit byte. Returns -1 when no digits
// are present (so callers can distinguish "empty param" from "0").
func readInt(data []byte, i int) (int, int) {
	const cap = 1 << 20
	n := 0
	seen := false
	for i < len(data) && isDigit(data[i]) {
		n = n*10 + int(data[i]-'0')
		if n > cap {
			n = cap
		}
		i++
		seen = true
	}
	if !seen {
		return -1, i
	}
	return n, i
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func clamp100(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// hlsToRGB converts DEC HLS (H 0..360, L 0..100, S 0..100) to NRGBA.
// DEC's hue origin is +120° relative to modern HSL — H=0 is blue, not
// red. We rotate before the standard conversion.
func hlsToRGB(h, l, s int) color.NRGBA {
	H := math.Mod(float64(h)+240, 360)
	if H < 0 {
		H += 360
	}
	L := float64(clamp100(l)) / 100.0
	S := float64(clamp100(s)) / 100.0
	C := (1 - math.Abs(2*L-1)) * S
	X := C * (1 - math.Abs(math.Mod(H/60.0, 2)-1))
	m := L - C/2
	var r, g, b float64
	switch {
	case H < 60:
		r, g, b = C, X, 0
	case H < 120:
		r, g, b = X, C, 0
	case H < 180:
		r, g, b = 0, C, X
	case H < 240:
		r, g, b = 0, X, C
	case H < 300:
		r, g, b = X, 0, C
	default:
		r, g, b = C, 0, X
	}
	return color.NRGBA{
		uint8(math.Round((r + m) * 0xFF)),
		uint8(math.Round((g + m) * 0xFF)),
		uint8(math.Round((b + m) * 0xFF)),
		0xFF,
	}
}

// encodePNGFile writes a PNG-encoded copy of img into dir (defaulting
// to os.TempDir() when empty) and returns the absolute file path. The
// filename is the SHA-1 of the encoded bytes so repeated decodes of
// the same Sixel stream dedupe to one file on disk. The desktop GPU
// backends (metal/gl/sdl2) load images via filesystem path; data:
// URLs aren't decoded by those backends, so the on-disk hop is
// required. Returns "" on encode/write failure.
func encodePNGFile(img *image.NRGBA, dir string) string {
	if img == nil || img.Bounds().Empty() {
		return ""
	}
	if dir == "" {
		dir = os.TempDir()
	}
	tmp, err := os.CreateTemp(dir, "term-sixel-*.png")
	if err != nil {
		return ""
	}
	tmpPath := tmp.Name()
	h := sha1.New()
	if err := png.Encode(tmp, img); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return ""
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return ""
	}
	// Re-open to compute a stable content hash for dedup.
	f, err := os.Open(tmpPath)
	if err != nil {
		return tmpPath
	}
	if _, err := f.Seek(0, 0); err == nil {
		buf := make([]byte, 4096)
		for {
			n, rerr := f.Read(buf)
			if n > 0 {
				h.Write(buf[:n])
			}
			if rerr != nil {
				break
			}
		}
	}
	_ = f.Close()
	hash := hex.EncodeToString(h.Sum(nil))
	finalPath := filepath.Join(dir, "term-sixel-"+hash+".png")
	if finalPath == tmpPath {
		return tmpPath
	}
	if _, err := os.Stat(finalPath); err == nil {
		_ = os.Remove(tmpPath)
		return finalPath
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return tmpPath
	}
	return finalPath
}
