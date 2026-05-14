package term

// scrollUpRegion shifts rows [Top..Bottom] up by n, clearing the bottom
// n rows of the region with default cells. When the region spans the
// full screen and ScrollbackCap > 0, the displaced top rows are pushed
// to the scrollback ring (oldest first) and trimmed to cap. n is
// clamped: n <= 0 is a no-op, n >= region height clears the region.
func (g *Grid) scrollUpRegion(n int) {
	if n <= 0 || !g.regionValid() {
		return
	}
	height := g.Bottom - g.Top + 1
	if n > height {
		n = height
	}
	full := g.regionFullScreen()
	if full && g.ScrollbackCap > 0 && !g.AltActive {
		g.Scrollback.EnsureGeom(g.ScrollbackCap, g.Cols)
		evicted := 0
		for r := 0; r < n; r++ {
			src := g.Cells[(g.Top+r)*g.Cols : (g.Top+r+1)*g.Cols]
			if g.Scrollback.Push(src, g.RowWrapped[g.Top+r]) {
				evicted++
			}
		}
		if evicted > 0 {
			g.trimMarks(evicted)
			g.trimGraphics(evicted)
		}
	}

	if n < height {
		copy(
			g.Cells[g.Top*g.Cols:(g.Bottom+1)*g.Cols],
			g.Cells[(g.Top+n)*g.Cols:(g.Bottom+1)*g.Cols],
		)
		copy(g.RowWrapped[g.Top:g.Bottom+1-n], g.RowWrapped[g.Top+n:g.Bottom+1])
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for r := g.Bottom + 1 - n; r <= g.Bottom; r++ {
		row := g.Cells[r*g.Cols : (r+1)*g.Cols]
		for i := range row {
			row[i] = blank
		}
		g.RowWrapped[r] = false
	}
	g.markAllDirty()
}

// scrollDownRegion shifts rows [Top..Bottom] down by n, clearing the
// top n rows with default cells. Never writes to scrollback (down-scroll
// reveals erased space, not displaced history).
func (g *Grid) scrollDownRegion(n int) {
	if n <= 0 || !g.regionValid() {
		return
	}
	height := g.Bottom - g.Top + 1
	if n > height {
		n = height
	}
	if n < height {

		for r := g.Bottom; r >= g.Top+n; r-- {
			copy(
				g.Cells[r*g.Cols:(r+1)*g.Cols],
				g.Cells[(r-n)*g.Cols:(r-n+1)*g.Cols],
			)
			g.RowWrapped[r] = g.RowWrapped[r-n]
		}
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for r := g.Top; r < g.Top+n && r <= g.Bottom; r++ {
		row := g.Cells[r*g.Cols : (r+1)*g.Cols]
		for i := range row {
			row[i] = blank
		}
		g.RowWrapped[r] = false
	}
	g.markAllDirty()
}

// SetScrollRegion implements DECSTBM (CSI Pt;Pb r). top/bottom are
// 0-based inclusive. Invalid or degenerate ranges (top >= bottom,
// out of bounds) reset to full screen. Cursor is homed to (0, 0)
// per DEC convention.
func (g *Grid) SetScrollRegion(top, bottom int) {
	if top < 0 || bottom >= g.Rows || top >= bottom {
		g.Top = 0
		g.Bottom = g.Rows - 1
	} else {
		g.Top = top
		g.Bottom = bottom
	}
	if g.OriginMode && g.regionValid() {
		g.CursorR, g.CursorC = g.Top, 0
		return
	}
	g.CursorR, g.CursorC = 0, 0
}

// ScrollUp implements CSI Ps S — scroll the region up by n rows,
// cursor unchanged. Wrapper around scrollUpRegion.
func (g *Grid) ScrollUp(n int) { g.scrollUpRegion(n) }

// ScrollDown implements CSI Ps T — scroll the region down by n rows.
func (g *Grid) ScrollDown(n int) { g.scrollDownRegion(n) }

// ScrollView shifts the viewport by `delta` rows: positive = back into
// scrollback (toward older content), negative = forward (toward live).
// Result clamped to [0, len(Scrollback)]. Saturating add: a delta near
// math.MinInt/MaxInt (e.g. derived from NaN/Inf wheel deltas) would
// overflow ViewOffset+delta before clamp, so detect the wrap.
func (g *Grid) ScrollView(delta int) {
	max := g.Scrollback.Len()
	switch {
	case delta > 0 && g.ViewOffset > max-delta:
		g.ViewOffset = max
	case delta < 0 && g.ViewOffset < -delta:
		g.ViewOffset = 0
	default:
		g.ViewOffset = clamp(g.ViewOffset+delta, 0, max)
	}
}

// ResetView snaps the viewport back to the live grid.
func (g *Grid) ResetView() { g.ViewOffset = 0 }

// ScrollViewTop moves the viewport to the oldest scrollback row.
func (g *Grid) ScrollViewTop() { g.ViewOffset = g.Scrollback.Len() }
