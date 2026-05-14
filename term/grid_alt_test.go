package term

import "testing"

func TestGrid_EnterAlt_BlanksAndSwaps(t *testing.T) {
	g := NewGrid(3, 4)
	g.Put('m')
	g.Put('a')
	g.Put('i')
	g.Put('n')
	g.MoveCursor(1, 2)
	g.CurAttrs = AttrBold
	g.EnterAlt()
	if !g.AltActive {
		t.Fatal("AltActive should be true after EnterAlt")
	}
	if g.CursorR != 0 || g.CursorC != 0 {
		t.Errorf("alt cursor not homed: %d,%d", g.CursorR, g.CursorC)
	}
	if g.CurAttrs != 0 || g.CurFG != DefaultColor || g.CurBG != DefaultColor {
		t.Errorf("alt SGR not reset: attrs=%d fg=%#x bg=%#x",
			g.CurAttrs, g.CurFG, g.CurBG)
	}
	for i, cell := range g.Cells {
		if cell.Ch != ' ' {
			t.Fatalf("alt cell %d not blank: %q", i, cell.Ch)
		}
	}
}

func TestGrid_EnterAlt_Idempotent(t *testing.T) {
	g := NewGrid(2, 3)
	g.Put('x')
	g.EnterAlt()
	g.Put('Y')
	g.EnterAlt()
	g.ExitAlt()
	if g.AltActive {
		t.Fatal("still alt after ExitAlt")
	}
	if g.At(0, 0).Ch != 'x' {
		t.Errorf("main row 0 col 0 = %q, want x", g.At(0, 0).Ch)
	}
}

func TestGrid_ExitAlt_NoOpWhenInactive(t *testing.T) {
	g := NewGrid(2, 3)
	g.Put('x')
	g.ExitAlt()
	if g.AltActive {
		t.Fatal("ExitAlt flipped state from inactive")
	}
	if g.At(0, 0).Ch != 'x' {
		t.Errorf("buffer corrupted by no-op ExitAlt")
	}
}

func TestGrid_AltSuppressesScrollback(t *testing.T) {
	g := NewGrid(2, 3)
	g.ScrollbackCap = 100
	g.EnterAlt()
	for i := 0; i < 10; i++ {
		g.Put('a' + rune(i))
		g.Newline()
	}
	if g.Scrollback.Len() != 0 {
		t.Errorf("scrollback grew while alt active: %d rows",
			g.Scrollback.Len())
	}
	g.ExitAlt()

	for i := 0; i < 5; i++ {
		g.Put('m')
		g.Newline()
	}
	if g.Scrollback.Len() == 0 {
		t.Errorf("scrollback not restored after ExitAlt")
	}
}

func TestGrid_EnterAlt_ResetsView(t *testing.T) {
	g := NewGrid(2, 3)
	g.ScrollbackCap = 10
	for i := 0; i < 4; i++ {
		g.Put('a')
		g.Newline()
	}
	g.ScrollView(2)
	if g.ViewOffset == 0 {
		t.Fatal("setup: ViewOffset should be > 0")
	}
	g.EnterAlt()
	if g.ViewOffset != 0 {
		t.Errorf("EnterAlt did not reset ViewOffset: %d", g.ViewOffset)
	}
}

func TestGrid_AltResize_ReflowsMainBuffer(t *testing.T) {
	g := NewGrid(3, 4)
	g.Put('a')
	g.Put('b')
	g.Put('c')
	g.MoveCursor(1, 0)
	g.Put('x')
	g.EnterAlt()
	g.Resize(3, 6)
	g.ExitAlt()
	if g.Cols != 6 {
		t.Fatalf("Cols = %d, want 6", g.Cols)
	}
	if g.At(0, 0).Ch != 'a' || g.At(0, 1).Ch != 'b' || g.At(0, 2).Ch != 'c' {
		t.Errorf("main row 0 lost on alt resize: %q%q%q",
			g.At(0, 0).Ch, g.At(0, 1).Ch, g.At(0, 2).Ch)
	}
	if g.At(1, 0).Ch != 'x' {
		t.Errorf("main row 1 col 0 = %q, want x", g.At(1, 0).Ch)
	}
	if g.At(0, 5).Ch != ' ' {
		t.Errorf("padding cell not blank: %q", g.At(0, 5).Ch)
	}
}

func TestGrid_AltScreen_PreservesInsertOriginWrap(t *testing.T) {
	g := NewGrid(3, 4)
	g.AutoWrap = false
	g.OriginMode = true
	g.InsertMode = true

	g.EnterAlt()
	if !g.AutoWrap {
		t.Error("alt screen should reset AutoWrap to true")
	}
	if g.OriginMode {
		t.Error("alt screen should reset OriginMode to false")
	}
	if g.InsertMode {
		t.Error("alt screen should reset InsertMode to false")
	}

	g.ExitAlt()
	if g.AutoWrap {
		t.Error("AutoWrap should be restored to false after ExitAlt")
	}
	if !g.OriginMode {
		t.Error("OriginMode should be restored to true after ExitAlt")
	}
	if !g.InsertMode {
		t.Error("InsertMode should be restored to true after ExitAlt")
	}
}

func TestGrid_AltScreen_PreservesCharsetState(t *testing.T) {
	g := NewGrid(3, 4)
	g.CharsetG0 = '0'
	g.CharsetG1 = 'B'
	g.ActiveG = 1

	g.EnterAlt()
	if g.CharsetG0 != 'B' || g.CharsetG1 != 'B' || g.ActiveG != 0 {
		t.Fatalf("alt defaults: G0=%q G1=%q active=%d", g.CharsetG0, g.CharsetG1, g.ActiveG)
	}

	g.ExitAlt()
	if g.CharsetG0 != '0' || g.CharsetG1 != 'B' || g.ActiveG != 1 {
		t.Fatalf("restored charsets: G0=%q G1=%q active=%d", g.CharsetG0, g.CharsetG1, g.ActiveG)
	}
}

func TestGrid_AltDECSC_DoesNotClobberMainSave(t *testing.T) {
	g := NewGrid(3, 4)
	g.MoveCursor(2, 3)
	g.CurAttrs = AttrBold
	g.SaveCursor()
	g.EnterAlt()
	g.MoveCursor(0, 1)
	g.CurAttrs = AttrUnderline
	g.SaveCursor()
	g.MoveCursor(1, 2)
	g.RestoreCursor()
	if g.CursorR != 0 || g.CursorC != 1 || g.CurAttrs != AttrUnderline {
		t.Errorf("alt DECRC: cursor=%d,%d attrs=%d",
			g.CursorR, g.CursorC, g.CurAttrs)
	}
	g.ExitAlt()
	g.RestoreCursor()
	if g.CursorR != 2 || g.CursorC != 3 || g.CurAttrs != AttrBold {
		t.Errorf("main DECRC after alt round-trip: cursor=%d,%d attrs=%d",
			g.CursorR, g.CursorC, g.CurAttrs)
	}
}
