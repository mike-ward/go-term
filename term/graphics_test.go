package term

import (
	"image/color"
	"os"
	"strings"
	"testing"
)

// helper: build a DCS Sixel payload as the parser sees it (bytes between
// DCS introducer and ST). Form: "<params>q<sixel-body>".
func sixelPayload(params, body string) string {
	return params + "q" + body
}

// feedSixel wraps payload in ESC P … ESC \ and feeds it through a
// fresh Parser so tests exercise the DCS state machine end-to-end.
// Decoded PNGs land in t.TempDir() so they're cleaned up automatically.
func feedSixel(t *testing.T, rows, cols int, params, body string) *Grid {
	t.Helper()
	g := NewGrid(rows, cols)
	g.CellPxW, g.CellPxH = 8, 16
	p := NewParser(g)
	p.SetGraphicsDir(t.TempDir())
	p.Feed([]byte("\x1bP" + sixelPayload(params, body) + "\x1b\\"))
	return g
}

func TestDecodeSixel_SinglePixel(t *testing.T) {
	// One sixel char '~' (0x7E) → mask 0x3F → all six pixels in column 0 set.
	// Color register 0 defaults to black. Body: "#0~"
	img := decodeSixel([]byte("#0~"))
	if img == nil {
		t.Fatal("decode returned nil")
	}
	b := img.Bounds()
	if b.Dx() != 1 || b.Dy() != 6 {
		t.Fatalf("got %dx%d; want 1x6", b.Dx(), b.Dy())
	}
	for y := range 6 {
		got := img.NRGBAAt(0, y)
		if got != (color.NRGBA{0, 0, 0, 0xFF}) {
			t.Errorf("pixel (0,%d) = %v; want black", y, got)
		}
	}
}

func TestDecodeSixel_ColorDefRGB(t *testing.T) {
	// #1;2;100;0;0 → register 1 = pure red (RGB, channels 0..100).
	// Then "#1~" paints column 0 in band 0 with red.
	img := decodeSixel([]byte("#1;2;100;0;0#1~"))
	if img == nil {
		t.Fatal("decode returned nil")
	}
	want := color.NRGBA{0xFF, 0, 0, 0xFF}
	if got := img.NRGBAAt(0, 0); got != want {
		t.Fatalf("pixel (0,0) = %v; want %v", got, want)
	}
}

func TestDecodeSixel_RunLength(t *testing.T) {
	// "!5~" repeats '~' five times → 5 columns of full bands.
	img := decodeSixel([]byte("#0!5~"))
	if img == nil {
		t.Fatal("decode returned nil")
	}
	b := img.Bounds()
	if b.Dx() != 5 || b.Dy() != 6 {
		t.Fatalf("got %dx%d; want 5x6", b.Dx(), b.Dy())
	}
	for x := range 5 {
		if got := img.NRGBAAt(x, 0); got.A != 0xFF {
			t.Errorf("pixel (%d,0) not opaque: %v", x, got)
		}
	}
}

func TestDecodeSixel_BandAdvance(t *testing.T) {
	// "~-~" paints a full 6px column, line-feeds, then paints another at
	// y=6..11. Expected height = 12.
	img := decodeSixel([]byte("#0~-~"))
	if img == nil {
		t.Fatal("decode returned nil")
	}
	b := img.Bounds()
	if b.Dy() != 12 {
		t.Fatalf("height = %d; want 12", b.Dy())
	}
	if got := img.NRGBAAt(0, 11); got.A != 0xFF {
		t.Errorf("expected opaque pixel at (0,11), got %v", got)
	}
}

func TestDecodeSixel_CarriageReturn(t *testing.T) {
	// "~~$#1!2~" — paint two cols red after a CR; verifies $ resets col
	// but stays on the same band. Cols 0,1 from black '~~'; then CR
	// resets to col 0 and overwrites col 0,1 with red.
	img := decodeSixel([]byte("#0~~$#1;2;100;0;0!2~"))
	if img == nil {
		t.Fatal("decode returned nil")
	}
	for x := range 2 {
		if got := img.NRGBAAt(x, 0); got.R != 0xFF || got.G != 0 || got.B != 0 {
			t.Errorf("col %d = %v; want red", x, got)
		}
	}
}

func TestDecodeSixel_RasterAttrsSkipped(t *testing.T) {
	// `"1;1;10;6` is a raster-attrs prefix. Should be silently skipped.
	img := decodeSixel([]byte(`"1;1;10;6#0~`))
	if img == nil {
		t.Fatal("decode returned nil after raster attrs")
	}
	if img.Bounds().Dx() != 1 || img.Bounds().Dy() != 6 {
		t.Fatalf("got %v; want 1x6", img.Bounds())
	}
}

func TestDecodeSixel_OutOfRangeColorIgnored(t *testing.T) {
	// Color register 999 is out of range; define silently dropped, default
	// register 0 (black) still paints.
	img := decodeSixel([]byte("#999;2;100;0;0#0~"))
	if img == nil {
		t.Fatal("decode returned nil")
	}
	if got := img.NRGBAAt(0, 0); got != (color.NRGBA{0, 0, 0, 0xFF}) {
		t.Errorf("(0,0) = %v; want black", got)
	}
}

func TestDecodeSixel_EmptyPayload(t *testing.T) {
	if got := decodeSixel(nil); got != nil {
		t.Fatalf("expected nil for empty payload, got %v", got)
	}
	if got := decodeSixel([]byte("")); got != nil {
		t.Fatalf("expected nil for empty payload, got %v", got)
	}
}

func TestEncodePNGFile_NilEmpty(t *testing.T) {
	if encodePNGFile(nil, t.TempDir()) != "" {
		t.Fatal("nil image should yield empty path")
	}
}

func TestEncodePNGFile_WritesPNG(t *testing.T) {
	g := feedSixel(t, 4, 10, "", "#0~")
	if len(g.Graphics) != 1 {
		t.Fatalf("got %d graphics; want 1", len(g.Graphics))
	}
	path := g.Graphics[0].Src
	if !strings.HasSuffix(path, ".png") {
		t.Fatalf("Src %q lacks .png suffix", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("PNG file is empty")
	}
	// PNG magic bytes.
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var magic [8]byte
	_, _ = f.Read(magic[:])
	if string(magic[:]) != "\x89PNG\r\n\x1a\n" {
		t.Errorf("not a PNG: %q", magic)
	}
}

func TestParser_DCS_SixelDispatch(t *testing.T) {
	// End-to-end: ESC P q ~ ESC \ should produce one Graphic with a
	// non-empty PNG data URL and advance the cursor below it.
	g := feedSixel(t, 10, 80, "", "#0~")
	if len(g.Graphics) != 1 {
		t.Fatalf("got %d graphics; want 1", len(g.Graphics))
	}
	gr := g.Graphics[0]
	if !strings.HasSuffix(gr.Src, ".png") {
		t.Errorf("Src should be a .png path; got %q", gr.Src)
	}
	if _, err := os.Stat(gr.Src); err != nil {
		t.Errorf("PNG file missing on disk: %v", err)
	}
	if gr.WidthPx != 1 || gr.HeightPx != 6 {
		t.Errorf("size = %dx%d; want 1x6", gr.WidthPx, gr.HeightPx)
	}
	if gr.OriginR != 0 || gr.OriginC != 0 {
		t.Errorf("origin = (%d,%d); want (0,0)", gr.OriginR, gr.OriginC)
	}
	// Cursor should sit *below* the graphic. With cellPxH=16 and pixel
	// height 6, ceil(6/16) = 1 cell row → cursor advances from 0 to 1.
	if g.CursorR != 1 {
		t.Errorf("CursorR = %d; want 1 (advanced below 1-cell image)", g.CursorR)
	}
}

func TestParser_DCS_SixelWithParams(t *testing.T) {
	// `0;0;0q` is a valid sixel introducer with three numeric params.
	g := feedSixel(t, 10, 80, "0;0;0", "#0~")
	if len(g.Graphics) != 1 {
		t.Fatalf("got %d graphics; want 1", len(g.Graphics))
	}
}

func TestParser_DCS_SixelBlanksCells(t *testing.T) {
	// Multi-cell image: 16px tall at cellPxH=16 occupies 1 cell row, and
	// 24px wide at cellPxW=8 occupies 3 cell cols. We force this by
	// repeating sixel chars. "!24~" = 24 cols of 6-px column → 24x6 image.
	g := feedSixel(t, 10, 80, "", "#0!24~")
	if len(g.Graphics) != 1 {
		t.Fatalf("got %d graphics; want 1", len(g.Graphics))
	}
	gr := g.Graphics[0]
	if gr.Cols != 3 || gr.Rows != 1 {
		t.Errorf("cell rect = %dx%d; want 3x1", gr.Cols, gr.Rows)
	}
	// Cells under the image must be blank (Ch==' ').
	for c := range gr.Cols {
		if ch := g.Cells[c].Ch; ch != ' ' {
			t.Errorf("cell %d under image has Ch=%q; want space", c, ch)
		}
	}
}

func TestParser_DCS_SixelOversizedDropped(t *testing.T) {
	// A bare 'q' with no body produces a nil image and no graphic, but
	// also no panic.
	g := feedSixel(t, 10, 80, "", "")
	if len(g.Graphics) != 0 {
		t.Fatalf("got %d graphics for empty payload; want 0", len(g.Graphics))
	}
}

func TestGrid_TrimGraphics_EvictsOffTop(t *testing.T) {
	g := NewGrid(4, 10)
	g.CellPxW, g.CellPxH = 8, 16
	// Inject two graphics: one anchored at content row 1 (height 1),
	// another at row 5 (height 2).
	g.Graphics = []Graphic{
		{OriginR: 1, Cols: 1, Rows: 1, Src: "x"},
		{OriginR: 5, Cols: 1, Rows: 2, Src: "y"},
	}
	g.trimGraphics(2)
	if len(g.Graphics) != 1 {
		t.Fatalf("survivors = %d; want 1", len(g.Graphics))
	}
	if g.Graphics[0].OriginR != 3 {
		t.Errorf("OriginR shifted to %d; want 3", g.Graphics[0].OriginR)
	}
}

func TestGrid_TrimGraphics_PartialKeep(t *testing.T) {
	g := NewGrid(4, 10)
	// Image straddling the trim boundary (rows 1..3 at height 3, trim 2)
	// keeps its visible tail: OriginR becomes -1, Rows=3 → still covers
	// content row 1, so we keep it.
	g.Graphics = []Graphic{{OriginR: 1, Cols: 1, Rows: 3, Src: "x"}}
	g.trimGraphics(2)
	if len(g.Graphics) != 1 {
		t.Fatalf("expected to keep partially visible graphic")
	}
	if g.Graphics[0].OriginR != -1 {
		t.Errorf("OriginR = %d; want -1", g.Graphics[0].OriginR)
	}
}

func TestGrid_ShiftGraphics_DropsOutOfRange(t *testing.T) {
	g := NewGrid(4, 10)
	g.Graphics = []Graphic{
		{OriginR: 0, Cols: 1, Rows: 1, Src: "a"},
		{OriginR: 5, Cols: 1, Rows: 1, Src: "b"},
	}
	// total=4, delta=-3: a moves to -3 (drop), b moves to 2 (keep).
	g.shiftGraphics(-3, 4)
	if len(g.Graphics) != 1 || g.Graphics[0].Src != "b" {
		t.Fatalf("survivors = %v; want only 'b'", g.Graphics)
	}
	if g.Graphics[0].OriginR != 2 {
		t.Errorf("OriginR = %d; want 2", g.Graphics[0].OriginR)
	}
}

func TestGrid_AddGraphic_CapEvictsOldest(t *testing.T) {
	g := NewGrid(4, 10)
	g.CellPxW, g.CellPxH = 8, 16
	for i := 0; i <= maxGraphics; i++ {
		g.AddGraphic("/tmp/fake.png", 8, 16)
	}
	if len(g.Graphics) != maxGraphics {
		t.Fatalf("len(Graphics) = %d; want %d", len(g.Graphics), maxGraphics)
	}
}

func TestIndexSixelFinal(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"q", 0},
		{"0q", 1},
		{"0;0;0q", 5},
		{";q", 1},
		{"$q", -1}, // '$' is DECRQSS, not sixel
		{"+q", -1}, // '+' is XTGETTCAP
		{"abcq", -1},
	}
	for _, tt := range tests {
		got := indexSixelFinal([]byte(tt.in))
		if got != tt.want {
			t.Errorf("indexSixelFinal(%q) = %d; want %d", tt.in, got, tt.want)
		}
	}
}

func TestHLSToRGB_Cardinals(t *testing.T) {
	// DEC HLS H=0 is blue per VT340 spec. With L=50, S=100, hue 0 → blue.
	got := hlsToRGB(0, 50, 100)
	if got.B < 0xC0 || got.R > 0x40 || got.G > 0x40 {
		t.Errorf("DEC H=0 L=50 S=100 = %v; want roughly blue", got)
	}
}
