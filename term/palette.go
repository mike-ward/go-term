package term

import "github.com/mike-ward/go-gui/gui"

// palette is the xterm 256-color table. Indices 0..15 are the standard
// ANSI 16 (VS Code Dark+ approximation); 16..231 form the 6×6×6 RGB
// cube; 232..255 are 24 grayscale steps.
var palette [256]gui.Color

func init() {
	// 0..7: standard ANSI.
	palette[0] = gui.RGB(0, 0, 0)       // black
	palette[1] = gui.RGB(205, 49, 49)   // red
	palette[2] = gui.RGB(13, 188, 121)  // green
	palette[3] = gui.RGB(229, 229, 16)  // yellow
	palette[4] = gui.RGB(36, 114, 200)  // blue
	palette[5] = gui.RGB(188, 63, 188)  // magenta
	palette[6] = gui.RGB(17, 168, 205)  // cyan
	palette[7] = gui.RGB(229, 229, 229) // white
	// 8..15: bright variants.
	palette[8] = gui.RGB(102, 102, 102)  // bright black
	palette[9] = gui.RGB(241, 76, 76)    // bright red
	palette[10] = gui.RGB(35, 209, 139)  // bright green
	palette[11] = gui.RGB(245, 245, 67)  // bright yellow
	palette[12] = gui.RGB(59, 142, 234)  // bright blue
	palette[13] = gui.RGB(214, 112, 214) // bright magenta
	palette[14] = gui.RGB(41, 184, 219)  // bright cyan
	palette[15] = gui.RGB(229, 229, 229) // bright white

	// 16..231: 6×6×6 RGB cube. xterm step values per channel.
	levels := [6]uint8{0, 95, 135, 175, 215, 255}
	for r := range 6 {
		for g := range 6 {
			for b := range 6 {
				palette[16+36*r+6*g+b] = gui.RGB(levels[r], levels[g], levels[b])
			}
		}
	}

	// 232..255: 24 grayscale steps (8, 18, ..., 238).
	for i := range 24 {
		v := uint8(8 + 10*i)
		palette[232+i] = gui.RGB(v, v, v)
	}
}

// Default fg/bg used when a cell color is DefaultColor.
var (
	defaultFG = gui.RGB(229, 229, 229)
	defaultBG = gui.RGB(20, 20, 24)
)

// resolve decodes a packed color value, returning def for the
// DefaultColor sentinel. Unknown high-byte tags fall through to a
// palette lookup so a corrupt value renders as some valid color
// rather than panicking.
func resolve(c uint32, def gui.Color) gui.Color {
	if c == DefaultColor {
		return def
	}
	if c&0xFF000000 == colorRGB {
		return gui.RGB(uint8(c>>16), uint8(c>>8), uint8(c))
	}
	return palette[c&0xFF]
}

// fg resolves a cell's foreground to a Color, honoring inverse.
func fg(c Cell) gui.Color {
	if c.Attrs&AttrInverse != 0 {
		return resolve(c.BG, defaultBG)
	}
	return resolve(c.FG, defaultFG)
}

// bg resolves a cell's background to a Color, honoring inverse.
func bg(c Cell) gui.Color {
	if c.Attrs&AttrInverse != 0 {
		return resolve(c.FG, defaultFG)
	}
	return resolve(c.BG, defaultBG)
}
