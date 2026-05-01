package term

import (
	"math"
	"testing"
)

func TestClampWinsize(t *testing.T) {
	cases := []struct {
		in   int
		want uint16
	}{
		{-1, 1},
		{0, 1},
		{1, 1},
		{0xFFFF, 0xFFFF},
		{0x10000, 0xFFFF},
		{math.MaxInt32, 0xFFFF},
	}
	for _, c := range cases {
		if got := clampWinsize(c.in); got != c.want {
			t.Errorf("clampWinsize(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestPTY_StartResizeClose(t *testing.T) {
	p, err := Start(24, 80)
	if err != nil {
		t.Skipf("Start failed (no shell available?): %v", err)
	}
	if err := p.Resize(30, 100); err != nil {
		t.Errorf("Resize: %v", err)
	}
	// Close kills the child and reaps it; the file.Close error is what
	// is returned. Either nil or "file already closed" is acceptable —
	// the contract is that it doesn't panic and is safe to call.
	_ = p.Close()
}
