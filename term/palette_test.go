package term

import "testing"

func TestPalette_FGBG_Default(t *testing.T) {
	c := defaultCell()
	if got := fg(c); got != defaultFG {
		t.Errorf("default fg: %+v want %+v", got, defaultFG)
	}
	if got := bg(c); got != defaultBG {
		t.Errorf("default bg: %+v want %+v", got, defaultBG)
	}
}

func TestPalette_FGBG_Indexed(t *testing.T) {
	c := Cell{Ch: ' ', FG: 1, BG: 2}
	if got := fg(c); got != palette[1] {
		t.Errorf("fg index 1: %+v want %+v", got, palette[1])
	}
	if got := bg(c); got != palette[2] {
		t.Errorf("bg index 2: %+v want %+v", got, palette[2])
	}
}

func TestPalette_Inverse_SwapsFGBG(t *testing.T) {
	c := Cell{Ch: ' ', FG: 1, BG: 2, Attrs: AttrInverse}
	if got := fg(c); got != palette[2] {
		t.Errorf("inverse fg should be bg color: %+v want %+v",
			got, palette[2])
	}
	if got := bg(c); got != palette[1] {
		t.Errorf("inverse bg should be fg color: %+v want %+v",
			got, palette[1])
	}
}

func TestPalette_Inverse_DefaultsSwap(t *testing.T) {
	c := Cell{Ch: ' ', FG: DefaultColor, BG: DefaultColor, Attrs: AttrInverse}
	if got := fg(c); got != defaultBG {
		t.Errorf("inverse default fg: %+v", got)
	}
	if got := bg(c); got != defaultFG {
		t.Errorf("inverse default bg: %+v", got)
	}
}
