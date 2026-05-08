package term

import (
	"testing"

	"github.com/mike-ward/go-gui/gui"
)

func TestPalette_FGBG_Default(t *testing.T) {
	c := defaultCell()
	if got := DefaultTheme.fg(c); got != DefaultTheme.DefaultFG {
		t.Errorf("default fg: %+v want %+v", got, DefaultTheme.DefaultFG)
	}
	if got := DefaultTheme.bg(c); got != DefaultTheme.DefaultBG {
		t.Errorf("default bg: %+v want %+v", got, DefaultTheme.DefaultBG)
	}
}

func TestPalette_FGBG_Indexed(t *testing.T) {
	c := Cell{Ch: ' ', FG: 1, BG: 2}
	if got := DefaultTheme.fg(c); got != DefaultTheme.ANSI[1] {
		t.Errorf("fg index 1: %+v want %+v", got, DefaultTheme.ANSI[1])
	}
	if got := DefaultTheme.bg(c); got != DefaultTheme.ANSI[2] {
		t.Errorf("bg index 2: %+v want %+v", got, DefaultTheme.ANSI[2])
	}
}

func TestPalette_Inverse_SwapsFGBG(t *testing.T) {
	c := Cell{Ch: ' ', FG: 1, BG: 2, Attrs: AttrInverse}
	if got := DefaultTheme.fg(c); got != DefaultTheme.ANSI[2] {
		t.Errorf("inverse fg should be bg color: %+v want %+v",
			got, DefaultTheme.ANSI[2])
	}
	if got := DefaultTheme.bg(c); got != DefaultTheme.ANSI[1] {
		t.Errorf("inverse bg should be fg color: %+v want %+v",
			got, DefaultTheme.ANSI[1])
	}
}

func TestPalette_256_Cube(t *testing.T) {
	// xterm cube: index 16 + 36*r + 6*g + b with levels {0,95,135,175,215,255}.
	// Index 196 = 16 + 36*5 + 0 + 0 → pure red (255, 0, 0).
	if got, want := palette[196], gui.RGB(255, 0, 0); got != want {
		t.Errorf("palette[196]: got %+v want %+v", got, want)
	}
	// Index 21 = 16 + 0 + 0 + 5 → pure blue (0, 0, 255).
	if got, want := palette[21], gui.RGB(0, 0, 255); got != want {
		t.Errorf("palette[21]: got %+v want %+v", got, want)
	}
}

func TestPalette_256_Grayscale(t *testing.T) {
	// 232 = first gray, value 8.
	if got, want := palette[232], gui.RGB(8, 8, 8); got != want {
		t.Errorf("palette[232]: got %+v want %+v", got, want)
	}
	// 255 = last gray, value 8 + 23*10 = 238.
	if got, want := palette[255], gui.RGB(238, 238, 238); got != want {
		t.Errorf("palette[255]: got %+v want %+v", got, want)
	}
}

func TestPalette_TruecolorRoundtrip(t *testing.T) {
	c := Cell{Ch: ' ', FG: rgbColor(255, 100, 0), BG: rgbColor(10, 20, 30)}
	if got, want := DefaultTheme.fg(c), gui.RGB(255, 100, 0); got != want {
		t.Errorf("truecolor fg: got %+v want %+v", got, want)
	}
	if got, want := DefaultTheme.bg(c), gui.RGB(10, 20, 30); got != want {
		t.Errorf("truecolor bg: got %+v want %+v", got, want)
	}
}

func TestPalette_ResolveUnknownTagUsesPaletteByte(t *testing.T) {
	// Tag 0x42 is neither colorPalette (0x00) nor colorRGB (0x01) nor
	// DefaultColor (0xFF). Decoder must not panic; falls back to
	// palette[low byte] which is in-bounds (palette has 256 entries).
	bad := uint32(0x42)<<24 | uint32(5)
	if got, want := DefaultTheme.resolve(bad, DefaultTheme.DefaultFG), DefaultTheme.ANSI[5]; got != want {
		t.Errorf("resolve unknown tag (FG default): got %+v want %+v", got, want)
	}
	if got, want := DefaultTheme.resolve(bad, DefaultTheme.DefaultBG), DefaultTheme.ANSI[5]; got != want {
		t.Errorf("resolve unknown tag (BG default): got %+v want %+v", got, want)
	}
}

func TestPalette_TruecolorInverse(t *testing.T) {
	c := Cell{
		Ch:    ' ',
		FG:    rgbColor(1, 2, 3),
		BG:    rgbColor(10, 20, 30),
		Attrs: AttrInverse,
	}
	if got, want := DefaultTheme.fg(c), gui.RGB(10, 20, 30); got != want {
		t.Errorf("inverse truecolor fg: got %+v want %+v", got, want)
	}
	if got, want := DefaultTheme.bg(c), gui.RGB(1, 2, 3); got != want {
		t.Errorf("inverse truecolor bg: got %+v want %+v", got, want)
	}
}

func TestPalette_Inverse_DefaultsSwap(t *testing.T) {
	c := Cell{Ch: ' ', FG: DefaultColor, BG: DefaultColor, Attrs: AttrInverse}
	if got := DefaultTheme.fg(c); got != DefaultTheme.DefaultBG {
		t.Errorf("inverse default fg: %+v", got)
	}
	if got := DefaultTheme.bg(c); got != DefaultTheme.DefaultFG {
		t.Errorf("inverse default bg: %+v", got)
	}
}

func TestPalette_ThemeOverridesANSI(t *testing.T) {
	custom := Theme{
		ANSI:      DefaultTheme.ANSI,
		DefaultFG: DefaultTheme.DefaultFG,
		DefaultBG: DefaultTheme.DefaultBG,
	}
	custom.ANSI[1] = gui.RGB(255, 0, 128) // override red
	c := Cell{Ch: ' ', FG: 1}
	if got, want := custom.fg(c), gui.RGB(255, 0, 128); got != want {
		t.Errorf("theme ANSI override: got %+v want %+v", got, want)
	}
	// Extended colors (≥16) should be unaffected by ANSI override.
	c2 := Cell{Ch: ' ', FG: paletteColor(196)}
	if got, want := custom.fg(c2), palette[196]; got != want {
		t.Errorf("extended color unchanged: got %+v want %+v", got, want)
	}
}
