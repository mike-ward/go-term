package term

import "github.com/mike-ward/go-gui/gui"

// Theme holds the 16 ANSI base colors plus default fg/bg for a terminal
// color scheme. Indices 0–7 are standard ANSI; 8–15 are bright variants.
// The 240 extended colors (16–255) are computed and not themeable.
type Theme struct {
	ANSI      [16]gui.Color
	DefaultFG gui.Color
	DefaultBG gui.Color
}

// Predefined themes. DefaultTheme is applied when a new Grid is created.
var (
	DefaultTheme       Theme // VS Code Dark+ approximation
	GruvboxTheme       Theme // Gruvbox Dark
	NordTheme          Theme // Nord
	SolarizedDarkTheme Theme // Solarized Dark
)

// palette holds the xterm 256-color table. Indices 0–15 mirror
// DefaultTheme.ANSI (for backwards-compat lookup); 16–231 are the
// 6×6×6 RGB cube; 232–255 are 24 grayscale steps. Theme.resolve uses
// Theme.ANSI for 0–15 and this table for 16–255.
var palette [256]gui.Color

func init() {
	DefaultTheme = Theme{
		ANSI: [16]gui.Color{
			gui.RGB(0, 0, 0),       // 0  black
			gui.RGB(205, 49, 49),   // 1  red
			gui.RGB(13, 188, 121),  // 2  green
			gui.RGB(229, 229, 16),  // 3  yellow
			gui.RGB(36, 114, 200),  // 4  blue
			gui.RGB(188, 63, 188),  // 5  magenta
			gui.RGB(17, 168, 205),  // 6  cyan
			gui.RGB(229, 229, 229), // 7  white
			gui.RGB(102, 102, 102), // 8  bright black
			gui.RGB(241, 76, 76),   // 9  bright red
			gui.RGB(35, 209, 139),  // 10 bright green
			gui.RGB(245, 245, 67),  // 11 bright yellow
			gui.RGB(59, 142, 234),  // 12 bright blue
			gui.RGB(214, 112, 214), // 13 bright magenta
			gui.RGB(41, 184, 219),  // 14 bright cyan
			gui.RGB(229, 229, 229), // 15 bright white
		},
		DefaultFG: gui.RGB(229, 229, 229),
		DefaultBG: gui.RGB(20, 20, 24),
	}

	// https://github.com/morhetz/gruvbox
	GruvboxTheme = Theme{
		ANSI: [16]gui.Color{
			gui.RGB(40, 40, 40),    // 0  bg0_h
			gui.RGB(204, 36, 29),   // 1  red
			gui.RGB(152, 151, 26),  // 2  green
			gui.RGB(215, 153, 33),  // 3  yellow
			gui.RGB(69, 133, 136),  // 4  blue
			gui.RGB(177, 98, 134),  // 5  magenta
			gui.RGB(104, 157, 106), // 6  cyan
			gui.RGB(168, 153, 132), // 7  fg4
			gui.RGB(146, 131, 116), // 8  fg3
			gui.RGB(251, 73, 52),   // 9  bright red
			gui.RGB(184, 187, 38),  // 10 bright green
			gui.RGB(250, 189, 47),  // 11 bright yellow
			gui.RGB(131, 165, 152), // 12 bright blue
			gui.RGB(211, 134, 155), // 13 bright magenta
			gui.RGB(142, 192, 124), // 14 bright cyan
			gui.RGB(235, 219, 178), // 15 fg1
		},
		DefaultFG: gui.RGB(235, 219, 178),
		DefaultBG: gui.RGB(40, 40, 40),
	}

	// https://www.nordtheme.com
	NordTheme = Theme{
		ANSI: [16]gui.Color{
			gui.RGB(46, 52, 64),    // 0  nord0
			gui.RGB(191, 97, 106),  // 1  nord11
			gui.RGB(163, 190, 140), // 2  nord14
			gui.RGB(235, 203, 139), // 3  nord13
			gui.RGB(129, 161, 193), // 4  nord9
			gui.RGB(180, 142, 173), // 5  nord15
			gui.RGB(136, 192, 208), // 6  nord8
			gui.RGB(229, 233, 240), // 7  nord4
			gui.RGB(76, 86, 106),   // 8  nord3
			gui.RGB(191, 97, 106),  // 9  bright red
			gui.RGB(163, 190, 140), // 10 bright green
			gui.RGB(235, 203, 139), // 11 bright yellow
			gui.RGB(129, 161, 193), // 12 bright blue
			gui.RGB(180, 142, 173), // 13 bright magenta
			gui.RGB(143, 188, 187), // 14 nord7
			gui.RGB(236, 239, 244), // 15 nord6
		},
		DefaultFG: gui.RGB(229, 233, 240),
		DefaultBG: gui.RGB(46, 52, 64),
	}

	// https://ethanschoonover.com/solarized
	SolarizedDarkTheme = Theme{
		ANSI: [16]gui.Color{
			gui.RGB(7, 54, 66),     // 0  base02
			gui.RGB(220, 50, 47),   // 1  red
			gui.RGB(133, 153, 0),   // 2  green
			gui.RGB(181, 137, 0),   // 3  yellow
			gui.RGB(38, 139, 210),  // 4  blue
			gui.RGB(211, 54, 130),  // 5  magenta
			gui.RGB(42, 161, 152),  // 6  cyan
			gui.RGB(238, 232, 213), // 7  base2
			gui.RGB(0, 43, 54),     // 8  base03
			gui.RGB(203, 75, 22),   // 9  orange
			gui.RGB(88, 110, 117),  // 10 base01
			gui.RGB(101, 123, 131), // 11 base00
			gui.RGB(131, 148, 150), // 12 base0
			gui.RGB(108, 113, 196), // 13 violet
			gui.RGB(147, 161, 161), // 14 base1
			gui.RGB(253, 246, 227), // 15 base3
		},
		DefaultFG: gui.RGB(131, 148, 150),
		DefaultBG: gui.RGB(0, 43, 54),
	}

	// Mirror DefaultTheme ANSI 0–15 into palette so legacy palette-index
	// fallback in resolve (for unknown high-byte tags) stays consistent.
	for i := range 16 {
		palette[i] = DefaultTheme.ANSI[i]
	}
	// 16–231: 6×6×6 RGB cube. xterm step values per channel.
	levels := [6]uint8{0, 95, 135, 175, 215, 255}
	for r := range 6 {
		for g := range 6 {
			for b := range 6 {
				palette[16+36*r+6*g+b] = gui.RGB(levels[r], levels[g], levels[b])
			}
		}
	}
	// 232–255: 24 grayscale steps (8, 18, …, 238).
	for i := range 24 {
		v := uint8(8 + 10*i)
		palette[232+i] = gui.RGB(v, v, v)
	}
}

// resolve decodes a packed color value. Indices 0–15 use th.ANSI;
// indices 16–255 use the global xterm table. DefaultColor returns def.
// Unknown high-byte tags fall through to palette[low byte] so a corrupt
// value renders as some valid color rather than panicking.
func (th *Theme) resolve(c uint32, def gui.Color) gui.Color {
	if c == DefaultColor {
		return def
	}
	if c&0xFF000000 == colorRGB {
		return gui.RGB(uint8(c>>16), uint8(c>>8), uint8(c))
	}
	idx := c & 0xFF
	if idx < 16 {
		return th.ANSI[idx]
	}
	return palette[idx]
}

// fg resolves a cell's foreground to a Color, honoring inverse.
func (th *Theme) fg(c Cell) gui.Color {
	if c.Attrs&AttrInverse != 0 {
		return th.resolve(c.BG, th.DefaultBG)
	}
	return th.resolve(c.FG, th.DefaultFG)
}

// bg resolves a cell's background to a Color, honoring inverse.
func (th *Theme) bg(c Cell) gui.Color {
	if c.Attrs&AttrInverse != 0 {
		return th.resolve(c.FG, th.DefaultFG)
	}
	return th.resolve(c.BG, th.DefaultBG)
}
