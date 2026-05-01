package term

import "github.com/mike-ward/go-gui/gui"

// ANSI 16-color palette (VS Code Dark+ approximation). Indices 0..7 are
// the standard colors; 8..15 are the bright variants. SGR 30+, 40+, 90+,
// 100+ map onto this table.
var palette = [16]gui.Color{
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
}

// Default fg/bg used when a cell has DefaultColor.
var (
	defaultFG = gui.RGB(229, 229, 229)
	defaultBG = gui.RGB(20, 20, 24)
)

// fg resolves a cell's foreground to a Color, honoring inverse.
func fg(c Cell) gui.Color {
	col := defaultFG
	if c.FG != DefaultColor {
		col = palette[c.FG]
	}
	if c.Attrs&AttrInverse != 0 {
		return bgRaw(c)
	}
	return col
}

// bg resolves a cell's background to a Color, honoring inverse.
func bg(c Cell) gui.Color {
	if c.Attrs&AttrInverse != 0 {
		return fgRaw(c)
	}
	return bgRaw(c)
}

func fgRaw(c Cell) gui.Color {
	if c.FG == DefaultColor {
		return defaultFG
	}
	return palette[c.FG]
}

func bgRaw(c Cell) gui.Color {
	if c.BG == DefaultColor {
		return defaultBG
	}
	return palette[c.BG]
}
