package term

import (
	"strings"
	"time"

	glyph "github.com/go-gui-org/go-glyph"
	"github.com/go-gui-org/go-gui/gui"
)

// asciiStr caches single-rune strings for runes 0..127 to avoid the
// per-cell allocation that string(rune) incurs in the OnDraw hot path.
var asciiStr = func() [128]string {
	var a [128]string
	for i := range a {
		a[i] = string(rune(i))
	}
	return a
}()

// termRuneStr returns string(r) without allocating for runes already in the
// per-Term cache. Wide-char cells and the cursor call this once per distinct
// rune seen, then reuse the cached string every subsequent frame.
func (t *Term) termRuneStr(r rune) string {
	if uint32(r) < 128 {
		return asciiStr[r]
	}
	if s, ok := t.draw.runeCache[r]; ok {
		return s
	}
	s := string(r)
	if t.draw.runeCache == nil {
		t.draw.runeCache = make(map[rune]string, 64)
	}
	t.draw.runeCache[r] = s
	return s
}

// cellText returns the text to render for a cell: the interned grapheme
// cluster string for multi-codepoint cells, otherwise the (cached) single
// rune. Caller holds grid.Mu (OnDraw does).
func (t *Term) cellText(c cell) string {
	if c.clusterID != 0 && int(c.clusterID) < len(t.grid.clusters) {
		return t.grid.clusters[c.clusterID]
	}
	return t.termRuneStr(c.Ch)
}

func isGeometryGlyph(r rune) bool {
	switch {
	case r >= 0x2500 && r <= 0x25FF: // Box Drawing, Block Elements, Geometric Shapes
		return true
	case r >= 0x23BA && r <= 0x23BD: // Misc Technical horizontal scan lines (⎺⎻⎼⎽)
		return true
	case r >= 0x2800 && r <= 0x28FF: // Braille Patterns
		return true
	default:
		return false
	}
}

// runKey captures the rendering-relevant properties of a cell for
// run-coalescing in the foreground pass. Two cells with equal runKey
// can be drawn in a single dc.Text call.
//
// Hyperlink ID is deliberately not part of the key: adjacent cells
// belonging to different links but rendered with the same visual style
// (color, underline, typeface) can coalesce into one dc.Text call, and
// thus one go-glyph layout-cache entry. With OSC 8 streams like ls
// --hyperlink, every filename has a unique link ID — keying on it
// fragments runs that visually merge anyway. Hover-induced recolor
// already lives in the color field, so hovered vs non-hovered cells
// break the run correctly. Click hit-testing reads cell.LinkID
// directly via ViewCellAt and is unaffected.
type runKey struct {
	typeface      glyph.Typeface
	color         gui.Color
	ulColor       gui.Color
	ulStyle       uint8 // ulNone..ulDashed; drives underline rendering
	strikethrough bool
	overline      bool // SGR 53; drawn in runKey.color, not ulColor
}

// cellRunKey computes the runKey for cell, applying attribute and
// hyperlink-hover color transforms. Must be called under grid.Mu.
// Link underline is always applied; hover recolor is gated on cmdHeld.
// urlHover is true when this cell is inside the Cmd-hovered implicit-URL span
// (issue 72); it gets the same underline + blue recolor as an OSC 8 link.
func cellRunKey(cell cell, base gui.TextStyle, g *grid, hoverR, hoverC int, cmdHeld, urlHover bool) runKey {
	rawFG := g.fgOf(cell)
	color := rawFG
	if cell.Attrs&attrDim != 0 {
		color = gui.RGB(rawFG.R/2, rawFG.G/2, rawFG.B/2)
	}
	tf := base.Typeface
	bold, italic := cell.Attrs&attrBold != 0, cell.Attrs&attrItalic != 0
	if isGeometryGlyph(cell.Ch) {
		bold = false
	}
	switch {
	case bold && italic:
		tf = glyph.TypefaceBoldItalic
	case bold:
		tf = glyph.TypefaceBold
	case italic:
		tf = glyph.TypefaceItalic
	}
	ulStyle := cell.ULStyle
	// An explicit SGR 58 color is honored as-is; the default means "follow the
	// text", so it is resolved at the end from the color actually painted —
	// after dim, hover recolor and the contrast clamp — instead of from the raw
	// foreground. Resolving it here would underline dim text at full strength.
	ulFollowsFG := cell.ULColor == defaultColor
	var ulColor gui.Color
	if !ulFollowsFG {
		ulColor = g.resolveColor(cell.ULColor, rawFG)
	}
	if cell.LinkID != 0 {
		if ulStyle == ulNone {
			ulStyle = ulSingle
		}
		if cmdHeld && hoverR >= 0 && hoverC >= 0 {
			if g.ViewCellAt(hoverR, hoverC).LinkID == cell.LinkID {
				col := color
				color = gui.RGB(col.R/2, col.G/2, 255)
			}
		}
	} else if urlHover {
		// Implicit URL under the Cmd-hovered pointer: reveal it exactly like an
		// OSC 8 link — underline plus the same blue recolor. The span membership
		// is computed by the caller from ds.rowURL, so no per-cell lookup here.
		if ulStyle == ulNone {
			ulStyle = ulSingle
		}
		col := color
		color = gui.RGB(col.R/2, col.G/2, 255)
	}
	// Minimum contrast, last: it must see the color that will actually be
	// painted, after dim and hover recolor, or it would raise a color those
	// then push back below the floor. Gated so a Term with the clamp off pays
	// one float comparison and skips the bgOf resolve entirely.
	//
	// An explicit SGR 58 underline color is deliberately left alone — it
	// decorates text that has already been made legible, and clamping it too
	// would flatten the distinction a colored underline exists to draw. A
	// default underline color instead copies the clamped text color below.
	if g.MinContrast > contrastDisabled {
		color = g.applyMinContrast(color, g.bgOf(cell))
	}
	if ulFollowsFG {
		ulColor = color
	}
	return runKey{
		color:         color,
		ulColor:       ulColor,
		typeface:      tf,
		ulStyle:       ulStyle,
		strikethrough: cell.Attrs&attrStrikethrough != 0,
		overline:      cell.Attrs&attrOverline != 0,
	}
}

// drawBgPass paints background-color runs. One call to fillRun per
// contiguous same-color span. Skips DefaultBG cells (canvas already filled).
func (t *Term) drawBgPass(ds *drawState) {
	dc := ds.dc
	yOff := ds.renderYOff
	cols := ds.cols
	if ds.partialRow != nil {
		t.drawBgPrecomputed(dc, -1, ds.partialRow, yOff, cols, ds.g)
	}
	for r := ds.renderTop; r < ds.renderRows; r++ {
		t.drawBgResolved(dc, r, yOff, ds)
	}
}

// drawBgPrecomputed coalesces background-color runs from a pre-resolved cell
// slice (partial row or BiDi-reordered row).
func (t *Term) drawBgPrecomputed(dc *gui.DrawContext, r int, row []cell, yOff float32, cols int, g *grid) {
	if len(row) == 0 {
		return
	}
	runStart := 0
	runColor := g.bgOf(row[0])
	for c := 1; c < cols; c++ {
		cur := g.bgOf(row[c])
		if cur != runColor {
			t.fillRun(dc, r, runStart, c, runColor, yOff, false)
			runStart = c
			runColor = cur
		}
	}
	// Never the bottom-most row: this path draws the partial row *above* the
	// viewport (r == -1), so it must not bleed downward.
	t.fillRun(dc, r, runStart, cols, runColor, yOff, false)
}

// drawBgResolved coalesces background-color runs for a single row.
// Uses resolveVisual so BiDi-reordered rows and regular rows share one path.
func (t *Term) drawBgResolved(dc *gui.DrawContext, r int, yOff float32, ds *drawState) {
	g := ds.g
	cols := ds.cols
	// Only the bottom-most drawn row may bleed into the sub-cell remainder
	// below it, and only when that whole row is one color — see bleedToEdge.
	// A row that ends as a single run (runStart still 0 at the final fill) is
	// exactly that uniform case, so the flag costs no extra scan.
	last := r == ds.renderRows-1
	runStart := 0
	runColor := g.bgOf(ds.resolveVisual(r, 0))
	for c := 1; c < cols; c++ {
		cur := g.bgOf(ds.resolveVisual(r, c))
		if cur != runColor {
			t.fillRun(dc, r, runStart, c, runColor, yOff, false)
			runStart = c
			runColor = cur
		}
	}
	t.fillRun(dc, r, runStart, cols, runColor, yOff, last && runStart == 0)
}

// textBlinkOff reports whether SGR 5/6 text is in the hidden half of its blink
// cycle. The phase comes from the wall clock rather than a per-Term epoch so
// every pane in a window blinks in step, and so the phase does not restart on
// unrelated events (a keystroke resets the *cursor* epoch, not this one).
func textBlinkOff(now time.Time) bool {
	return (now.UnixNano()/int64(cursorBlinkPeriod))%2 == 1
}

// maskGlyph blanks a cell's glyph when SGR 8 (conceal) is set, when SGR 5/6
// (blink) is set and the cycle is currently in its hidden half, or when the
// cell is a Kitty Unicode placeholder. Background, selection inversion and
// underline decoration are untouched — only the glyph disappears, matching
// xterm.
//
// Conceal must be honored: ncurses maps A_INVIS to SGR 8 and password prompts
// rely on it, so ignoring the attribute would show the typed secret. A
// placeholder is a graphics instruction wearing text's clothes — the cell says
// "show tile (row, col) of image N here" and drawPlaceholders paints it, so
// rendering the character itself would put a private-use tofu box under every
// image. The background still paints, which is what the protocol wants: it
// shows through a transparent image.
func maskGlyph(c cell, blinkOff bool) cell {
	if c.Attrs&attrConceal != 0 || (blinkOff && c.Attrs&attrBlink != 0) ||
		c.Ch == kgpPlaceholderRune {
		c.Ch = ' '
		c.clusterID = 0
	}
	return c
}

// drawFgPass paints foreground text, coalescing adjacent cells with identical
// visual style into single dc.Text calls. Wide chars break the run and emit
// individually. Continuation cells are skipped. Plain spaces extend same-style
// runs without starting new ones.
func (t *Term) drawFgPass(ds *drawState) {
	dc := ds.dc
	style := ds.style
	yOff := ds.renderYOff
	cols := ds.cols
	g := ds.g
	hR, hC := int(t.mouse.hoverR.Load()), int(t.mouse.hoverC.Load())
	cmdHeld := t.mouse.cmdHeld.Load()

	// sawBlink tracks whether any painted cell carries SGR 5/6, so the blink
	// ticker knows whether periodic repaints are needed at all. Recomputed
	// every frame: it clears itself once the blinking text scrolls away.
	sawBlink := false

	// Partial top row: per-cell emit, no run coalescing.
	if ds.partialRow != nil {
		partialY := t.rowY(-1, yOff)
		for c := range cols {
			cell := ds.partialRow[c]
			if cell.Width == 0 && cell.Ch == 0 {
				continue
			}
			sawBlink = sawBlink || cell.Attrs&attrBlink != 0
			cell = maskGlyph(cell, ds.blinkOff)
			if cell.Ch == ' ' && cell.Attrs&attrVisual == 0 && cell.LinkID == 0 {
				continue
			}
			// The partial top row sits above viewport row 0; hover coords never
			// land there (posToCell clamps to 0..Rows-1), so no URL highlight.
			k := cellRunKey(cell, style, g, hR, hC, cmdHeld, false)
			t.emitCell(dc, t.colX(c), partialY,
				t.spanW(c, c+int(cell.Width)), cell, k, style)
		}
	}

	var fr flushState
	for r := ds.renderTop; r < ds.renderRows; r++ {
		fr.open = false
		t.draw.runBuf.Reset()
		fr.cols = 0
		for c := range cols {
			cell := ds.resolveVisual(r, c)
			if cell.Width == 0 && cell.Ch == 0 {
				continue // continuation cell; skip without breaking run
			}
			sawBlink = sawBlink || cell.Attrs&attrBlink != 0
			cell = maskGlyph(cell, ds.blinkOff)
			urlHover := false
			if ds.rowURL != nil {
				if rb := ds.rowURL[r]; rb.active && c >= rb.c0 && c <= rb.c1 {
					urlHover = true
				}
			}
			k := cellRunKey(cell, style, g, hR, hC, cmdHeld, urlHover)
			isPlainSpace := cell.Ch == ' ' && cell.Attrs&attrVisual == 0 && cell.LinkID == 0
			if cell.Width == 2 {
				t.flushRun(dc, r, style, yOff, &fr)
				t.emitCell(dc, t.colX(c), t.rowY(r, yOff),
					t.spanW(c, c+int(cell.Width)), cell, k, style)
				continue
			}
			// Non-ASCII glyphs may trigger font fallback with metrics
			// that differ from the monospace cellW measured via 'M'.
			// Accumulated drift inside a coalesced text run can cause
			// visual overlap with the next run. Emit individually so
			// each glyph stays pinned to its cell origin. Multi-codepoint
			// clusters (clusterID != 0) must also emit individually so the
			// full cluster string is drawn even when the base rune is ASCII.
			if cell.Ch > 0x7F || cell.clusterID != 0 {
				t.flushRun(dc, r, style, yOff, &fr)
				t.emitCell(dc, t.colX(c), t.rowY(r, yOff),
					t.spanW(c, c+int(cell.Width)), cell, k, style)
				continue
			}
			if isPlainSpace {
				if fr.open && k == fr.key {
					t.draw.runBuf.WriteRune(' ')
					fr.cols++
				} else {
					t.flushRun(dc, r, style, yOff, &fr)
				}
				continue
			}
			if fr.open && k == fr.key {
				t.draw.runBuf.WriteRune(cell.Ch)
				fr.cols++
			} else {
				t.flushRun(dc, r, style, yOff, &fr)
				fr.open = true
				fr.start = c
				fr.cols = 1
				fr.key = k
				t.draw.runBuf.WriteRune(cell.Ch)
			}
		}
		t.flushRun(dc, r, style, yOff, &fr)
	}
	t.blinkCells.Store(sawBlink)
}

// flushRun draws the accumulated text run as a single dc.Text call with
// optional underline decoration, then resets the run state.
func (t *Term) flushRun(dc *gui.DrawContext, r int, style gui.TextStyle, yOff float32, fr *flushState) {
	if !fr.open || t.draw.runBuf.Len() == 0 {
		fr.open = false
		return
	}
	text := t.draw.runBuf.String()
	// Trim trailing spaces when no decoration spans them: "abc   " and
	// "abc" share a layout-cache entry, so trimming keeps cache hits
	// stable as tail padding wobbles frame to frame.
	if fr.key.ulStyle == ulNone && !fr.key.strikethrough && !fr.key.overline {
		text = strings.TrimRight(text, " ")
		if text == "" {
			fr.open = false
			t.draw.runBuf.Reset()
			fr.cols = 0
			return
		}
	}
	cs := style
	cs.Color = fr.key.color
	cs.Typeface = fr.key.typeface
	cs.Underline = false
	cs.Strikethrough = fr.key.strikethrough
	rowY := t.rowY(r, yOff)
	runX := t.colX(fr.start)
	dc.Text(runX, rowY, text, cs)
	// Run width is only needed by the decorations, which most runs don't have.
	if fr.key.ulStyle != ulNone || fr.key.overline {
		runW := t.colX(fr.start+fr.cols) - runX
		if fr.key.ulStyle != ulNone {
			t.drawUnderlineDecor(dc, runX, rowY, runW,
				fr.key.ulStyle, fr.key.ulColor)
		}
		if fr.key.overline {
			t.drawOverlineDecor(dc, runX, rowY, runW, fr.key.color)
		}
	}
	fr.open = false
	t.draw.runBuf.Reset()
	fr.cols = 0
}

// emitCell draws one cell's glyph at the pixel-snapped origin (x, y). w is the
// snapped width of the cell box (cell.Width columns), passed in rather than
// recomputed here so it is the difference of two snapped column origins and
// therefore tiles exactly with the neighbouring cells' boxes.
func (t *Term) emitCell(dc *gui.DrawContext, x, y, w float32, cell cell, k runKey, base gui.TextStyle) {
	cs := base
	cs.Color = k.color
	cs.Typeface = k.typeface
	cs.Underline = false
	cs.Strikethrough = k.strikethrough
	// Tell go-glyph the cell box this glyph occupies so color/emoji fill the
	// full reserved width (e.g. a width-2 emoji fills 2 cells) instead of the
	// font's narrower natural emoji advance. Ignored for non-color glyphs.
	cs.EmojiBoxWidth = w
	dc.Text(x, y, t.cellText(cell), cs)
	if k.ulStyle != ulNone {
		t.drawUnderlineDecor(dc, x, y, w, k.ulStyle, k.ulColor)
	}
	if k.overline {
		t.drawOverlineDecor(dc, x, y, w, k.color)
	}
}

// lastRow says this run covers the whole bottom-most drawn row, the only run
// allowed to bleed downward — see bleedToEdge.
func (t *Term) fillRun(dc *gui.DrawContext, row, c0, c1 int, color gui.Color, yOff float32, lastRow bool) {
	if color == t.grid.defaultBG() {
		return // canvas already painted with default bg.
	}
	// Snapped like the text so a run's edges land on the same pixel columns
	// as the glyphs drawn over it — an unsnapped fill leaves a half-lit seam
	// between adjacent background colors.
	x := t.colX(c0)
	y := t.rowY(row, yOff)
	// Sizes are differences against the origins already computed above, which
	// is both cheaper than spanW/rowH here and the same value.
	// Both axes bleed into the sub-cell remainder — see bleedToEdge. A run
	// whose color *is* the default returned above, so the remainder correctly
	// keeps the canvas fill in the case where the two agree.
	w := bleedSnapped(x, t.colX(c1)-x,
		float32(c0)*t.cellW, float32(c1-c0)*t.cellW, dc.Width, t.cellW)
	h := t.rowY(row+1, yOff) - y
	if lastRow {
		h = bleedSnapped(y, h,
			float32(row)*t.cellH+yOff, t.cellH, dc.Height, t.cellH)
	}
	dc.FilledRect(x, y, w, h, color)
}

// bleedToEdge extends a run that already abuts the canvas edge so it covers
// the sub-cell remainder beyond it, and returns interior runs unchanged.
//
// rows and cols are floor(canvas/cell), so up to one cell short of the right
// edge and one short of the bottom belongs to no cell and keeps whatever the
// canvas was filled with — the theme's background. That is invisible while the
// theme's background is also what the content uses, and obvious the moment it
// isn't: a full-screen app with a dark palette running under a light theme
// gets a bright rim down its right side and along its bottom. Extending the
// run that already abuts the edge is what every other emulator does, and it
// costs nothing — the rect was being drawn anyway.
//
// start/size describe the run along one axis, extent is the canvas size on
// that axis, and cell is the cell size. The test is "does this run reach
// within one cell of the edge"; an interior run that stretched would paint
// over its neighbour.
//
// The vertical bleed carries one further restriction, matching Ghostty's
// window-padding-color=extend: it applies only when the whole last row is a
// single background color. A partial-width colored run — fish's history-search
// highlight sitting on the bottom row, a shell prompt segment — would otherwise
// paint a detached block of color into the strip under the text, which reads as
// a phantom highlight on a line that does not exist. Falling back to the canvas
// fill for a multi-colored last row costs at most a thin default-colored strip
// under a status bar, which is what the other emulators show there anyway.
//
// The abutment test alone identifies the last column, but not the last row: smooth
// scrolling shifts every row down by ViewSubPx (0 ≤ ViewSubPx < cellH), so
// once the offset exceeds the canvas remainder the *second*-to-last row also
// ends within a cell of the bottom while a row still sits below it. fillRun
// therefore gates the vertical call on the row index, and this function only
// decides how far the bleed reaches.
// bleedSnapped applies bleedToEdge to a pixel-snapped run: it decides on the
// *unsnapped* geometry and measures the extension from the snapped origin.
//
// The decision cannot use the snapped edge. Snapping moves it up to half a
// pixel either way, so on a canvas whose remainder is just under one cell —
// roughly one window width in cell-width/0.5, which is common enough to see —
// a snap in the shrinking direction pushes the remainder past a full cell and
// the test stops firing. The result is the unpainted rim bleedToEdge exists to
// prevent. Snapping in the growing direction is harmless by comparison: the
// remainder goes slightly negative, the `rest > size` guard rejects it, and
// the run keeps its own width.
func bleedSnapped(snapStart, snapSize, idealStart, idealSize, extent, cell float32) float32 {
	if bleedToEdge(idealStart, idealSize, extent, cell) == idealSize {
		return snapSize
	}
	// Only reachable with a degenerate (sub-half-pixel) cell metric, where the
	// snapped origin can land past the canvas edge the ideal one sat inside.
	// A negative dimension is worse than no bleed.
	if extent-snapStart < snapSize {
		return snapSize
	}
	return extent - snapStart
}

func bleedToEdge(start, size, extent, cell float32) float32 {
	if rest := extent - start; rest > size && extent-(start+size) < cell {
		return rest
	}
	return size
}
