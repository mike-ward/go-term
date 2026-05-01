package term

import (
	"math"
	"testing"
)

func TestRuneString_ASCIINoAlloc(t *testing.T) {
	var sink string
	avg := testing.AllocsPerRun(100, func() {
		sink = runeString('A')
	})
	if sink != "A" {
		t.Errorf("got %q", sink)
	}
	if avg != 0 {
		t.Errorf("ASCII path should not allocate, got %v allocs/op", avg)
	}
}

func TestRuneString_ASCIIAllRunes(t *testing.T) {
	for r := rune(0); r < 128; r++ {
		got := runeString(r)
		if got != string(r) {
			t.Errorf("runeString(%d) = %q, want %q", r, got, string(r))
		}
	}
}

func TestRuneString_NonASCII(t *testing.T) {
	cases := []rune{0x00E9, 0x2603, 0x1F600, 0xFFFD}
	for _, r := range cases {
		if got := runeString(r); got != string(r) {
			t.Errorf("runeString(%U) = %q, want %q", r, got, string(r))
		}
	}
}

func TestFinite(t *testing.T) {
	cases := []struct {
		in   float32
		want bool
	}{
		{1, true},
		{0.5, true},
		{0, false},
		{-1, false},
		{float32(math.NaN()), false},
		{float32(math.Inf(1)), false},
		{float32(math.Inf(-1)), false},
	}
	for _, c := range cases {
		if got := finite(c.in); got != c.want {
			t.Errorf("finite(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
