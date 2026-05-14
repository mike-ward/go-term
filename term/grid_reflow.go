package term

import "slices"

// reflowBuffer copies src (oldRows×oldCols) into a freshly allocated
// newRows×newCols buffer, preserving the top-left intersection and
// padding the rest with default cells. Used by Resize for both the
// active cell buffer and (when alt-active) the saved main buffer.
func reflowBuffer(src []Cell, oldRows, oldCols, newRows, newCols int) []Cell {
	next := make([]Cell, newRows*newCols)
	for i := range next {
		next[i] = defaultCell()
	}
	if len(src) == 0 || oldRows <= 0 || oldCols <= 0 {
		return next
	}
	rcopy := min(newRows, oldRows)
	ccopy := min(newCols, oldCols)
	for r := range rcopy {
		copy(next[r*newCols:r*newCols+ccopy], src[r*oldCols:r*oldCols+ccopy])
	}
	return next
}

// physRow is used internally by the logical reflow pipeline.
// wrapped == true means this row ended with an autowrap and the next
// row is its soft-wrapped continuation.
type physRow struct {
	cells   []Cell
	wrapped bool
}

// isDefaultBlank reports whether c is an untouched default blank cell —
// i.e., no content was ever written to it. Used by logicalReflow to trim
// trailing padding from the last physical row of a logical line.
func isDefaultBlank(c Cell) bool {
	return c.Ch == ' ' && c.FG == DefaultColor && c.BG == DefaultColor &&
		c.Attrs == 0 && c.Width == 1 && c.LinkID == 0 && c.ULStyle == 0
}

// rewrapLine re-wraps a flat slice of cells (the content of one logical
// line, with continuation cells already stripped) into physical rows of
// newCols columns. All rows except the last are marked wrapped=true.
// An empty input produces a single blank row.
func rewrapLine(cells []Cell, newCols int) []physRow {
	if len(cells) == 0 {
		blank := make([]Cell, newCols)
		for i := range blank {
			blank[i] = defaultCell()
		}
		return []physRow{{cells: blank, wrapped: false}}
	}

	var rows []physRow
	cur := make([]Cell, 0, newCols)

	for i := 0; i < len(cells); {
		c := cells[i]

		if c.Width == 0 && c.Ch == 0 {
			i++
			continue
		}
		w := 1
		if c.Width == 2 {
			w = 2
		}

		if len(cur)+w > newCols {

			for len(cur) < newCols {
				cur = append(cur, defaultCell())
			}
			rows = append(rows, physRow{cells: cur, wrapped: true})
			cur = make([]Cell, 0, newCols)
		}
		cur = append(cur, c)
		if w == 2 {

			cur = append(cur, Cell{Ch: 0, FG: c.FG, BG: c.BG, Attrs: c.Attrs, Width: 0, ULStyle: c.ULStyle, ULColor: c.ULColor})
		}
		i++
	}

	for len(cur) < newCols {
		cur = append(cur, defaultCell())
	}
	rows = append(rows, physRow{cells: cur, wrapped: false})
	return rows
}

// logicalReflow joins soft-wrapped physical rows into logical lines,
// re-wraps them at newCols, and returns the new cell buffer, wrap flags,
// scrollback, and cursor position. Hard newlines (wrapped==false) are
// never joined across.
//
// Parameters:
//   - cells/rowWrapped: live cell buffer and per-row wrap flags (oldRows×oldCols)
//   - scrollback/sbWrapped: scrollback ring and its wrap flags
//   - oldRows, oldCols: current grid dims
//   - newRows, newCols: target dims
//   - cursorR, cursorC: cursor in the live buffer
//   - scrollbackCap: maximum scrollback rows (0 = unlimited trim handled by caller)
func logicalReflow(
	cells []Cell, rowWrapped []bool,
	scrollback [][]Cell, sbWrapped []bool,
	oldRows, oldCols, newRows, newCols int,
	cursorR, cursorC int,
	scrollbackCap int,
) (newCells []Cell, newRowWrapped []bool, newScrollback [][]Cell, newSbWrapped []bool, newCursorR, newCursorC int) {

	nSB := len(scrollback)
	total := nSB + oldRows
	phys := make([]physRow, total)
	for i, row := range scrollback {
		w := false
		if i < len(sbWrapped) {
			w = sbWrapped[i]
		}
		phys[i] = physRow{cells: row, wrapped: w}
	}
	for r := 0; r < oldRows; r++ {
		row := make([]Cell, oldCols)
		copy(row, cells[r*oldCols:(r+1)*oldCols])
		w := false
		if r < len(rowWrapped) {
			w = rowWrapped[r]
		}
		phys[nSB+r] = physRow{cells: row, wrapped: w}
	}

	cursorPhys := nSB + clamp(cursorR, 0, oldRows-1)

	// --- Identify logical lines and the one containing the cursor ---
	type logLine struct {
		start, end int // inclusive indices into phys[]
	}
	var lines []logLine
	lineStart := 0
	cursorLineIdx := 0
	cursorLineFound := false
	for i, pr := range phys {
		if !pr.wrapped {
			ll := logLine{lineStart, i}
			if !cursorLineFound && cursorPhys >= lineStart && cursorPhys <= i {
				cursorLineIdx = len(lines)
				cursorLineFound = true
			}
			lines = append(lines, ll)
			lineStart = i + 1
		}
	}

	if lineStart < len(phys) {
		if !cursorLineFound && cursorPhys >= lineStart {
			cursorLineIdx = len(lines)
			cursorLineFound = true
		}
		lines = append(lines, logLine{lineStart, len(phys) - 1})
	}
	if !cursorLineFound && len(lines) > 0 {
		cursorLineIdx = len(lines) - 1
	}

	// Cursor's display-column offset within its logical line.
	// Each preceding wrapped physical row contributes oldCols columns.
	// A pending-wrap cursor sits one column past the right margin after a
	// glyph was written in the last cell; keep it anchored to that last
	// cell instead of treating it as content beyond the row.
	var cursorLogCol int
	if len(lines) > 0 && cursorLineIdx < len(lines) {
		ll := lines[cursorLineIdx]
		effectiveCursorC := cursorC
		if effectiveCursorC >= oldCols {
			effectiveCursorC = oldCols - 1
		}
		if effectiveCursorC < 0 {
			effectiveCursorC = 0
		}
		cursorLogCol = (cursorPhys-ll.start)*oldCols + effectiveCursorC
	}

	// --- Re-wrap all logical lines ---
	var allNew []physRow
	cursorNewPhysStart := 0
	var cursorLineRewrapped []physRow

	for li, ll := range lines {
		// Collect cells for this logical line. Trim trailing default
		// blanks from the last physical row to avoid padding from creating
		// spurious extra physical rows after re-wrap. Only preserve cells
		// up to and including the cursor column when the cursor is within
		// the row bounds (cursorC < len(row)). When cursorC >= len(row)
		// (pending-wrap state past the right margin), don't preserve blanks
		// — the cursor position will be clamped to the rewrapped line's end.
		var lineCells []Cell
		for pi := ll.start; pi <= ll.end; pi++ {
			row := phys[pi].cells
			trimTo := len(row)
			if pi < ll.end && phys[pi].wrapped {

				next := phys[pi+1].cells
				if len(next) > 0 && next[0].Width == 2 {
					for trimTo > 0 && isDefaultBlank(row[trimTo-1]) {
						trimTo--
					}
				}
			}
			if pi == ll.end {
				for trimTo > 0 && isDefaultBlank(row[trimTo-1]) {
					trimTo--
				}

				if pi == cursorPhys && cursorC < len(row) && cursorC+1 > trimTo {
					trimTo = cursorC + 1
				}
			}
			lineCells = append(lineCells, row[:trimTo]...)
		}

		rewrapped := rewrapLine(lineCells, newCols)
		if li == cursorLineIdx {
			cursorNewPhysStart = len(allNew)
			cursorLineRewrapped = rewrapped
		}
		allNew = append(allNew, rewrapped...)

		if li < cursorLineIdx {
			capRows := newRows + scrollbackCap
			if capRows < newRows*2 {
				capRows = newRows * 2
			}
			if len(allNew) > capRows {
				allNew = allNew[len(allNew)-capRows:]
			}
		}
	}

	rowOffset := 0
	colOffset := 0
	if newCols > 0 && len(cursorLineRewrapped) > 0 {
		maxLogCol := len(cursorLineRewrapped)*newCols - 1
		if maxLogCol < 0 {
			maxLogCol = 0
		}
		effective := cursorLogCol
		if effective > maxLogCol {
			effective = maxLogCol
		}
		rowOffset = effective / newCols
		colOffset = effective % newCols
		if rowOffset >= len(cursorLineRewrapped) {
			rowOffset = len(cursorLineRewrapped) - 1
		}
	}
	newCursorPhys := cursorNewPhysStart + rowOffset

	maxStart := len(allNew) - newRows
	if maxStart < 0 {
		maxStart = 0
	}
	liveStart := newCursorPhys - (newRows - 1)
	if liveStart > maxStart {
		liveStart = maxStart
	}
	if liveStart < 0 {
		liveStart = 0
	}

	newScrollback = make([][]Cell, 0, liveStart)
	newSbWrapped = make([]bool, 0, liveStart)
	for _, pr := range allNew[:liveStart] {
		newScrollback = append(newScrollback, pr.cells)
		newSbWrapped = append(newSbWrapped, pr.wrapped)
	}
	if scrollbackCap > 0 && len(newScrollback) > scrollbackCap {
		trim := len(newScrollback) - scrollbackCap
		newScrollback = newScrollback[trim:]
		newSbWrapped = newSbWrapped[trim:]
	}

	newCells = make([]Cell, newRows*newCols)
	for i := range newCells {
		newCells[i] = defaultCell()
	}
	newRowWrapped = make([]bool, newRows)
	liveRows := allNew[liveStart:]
	for r, pr := range liveRows {
		if r >= newRows {
			break
		}
		copy(newCells[r*newCols:(r+1)*newCols], pr.cells)
		newRowWrapped[r] = pr.wrapped
	}

	newCursorR = newCursorPhys - liveStart
	newCursorC = colOffset
	if newCursorR < 0 {
		newCursorR = 0
	}
	if newCursorR >= newRows {
		newCursorR = newRows - 1
	}
	if newCursorC < 0 {
		newCursorC = 0
	}
	if newCursorC >= newCols {
		newCursorC = newCols - 1
	}
	return
}

// Resize reflows to new dims using logical line wrapping. Rows that ended
// with an autowrap (RowWrapped[r]==true) are joined with their successor
// into a single logical line and re-wrapped at the new column width, so
// terminal output reflowed like a modern terminal instead of cropping.
// Cursor position is tracked through the reflow. Rows separated by an
// explicit newline (RowWrapped[r]==false) are never joined.
//
// When alt-screen is active the alt buffer is reflowed with simple
// crop/pad (full-screen apps control every cell), while the saved main
// buffer receives logical reflow.
//
// The scroll region is reset after resize; apps re-issue DECSTBM after
// SIGWINCH. Selection is dropped. ViewOffset is reset to the live view.
func (g *Grid) Resize(rows, cols int) {
	rows = clampDim(rows)
	cols = clampDim(cols)
	if rows == g.Rows && cols == g.Cols {
		return
	}

	oldSbLen := g.Scrollback.Len()

	sbRows := make([][]Cell, oldSbLen)
	sbWrap := make([]bool, oldSbLen)
	for i := range oldSbLen {
		sbRows[i] = slices.Clone(g.Scrollback.Row(i))
		sbWrap[i] = g.Scrollback.Wrapped(i)
	}

	if g.AltActive {

		g.Cells = reflowBuffer(g.Cells, g.Rows, g.Cols, rows, cols)
		newRW := make([]bool, rows)
		copy(newRW, g.RowWrapped)
		g.RowWrapped = newRW

		if len(g.mainSaved.cells) == g.Rows*g.Cols {
			savedRW := g.mainSaved.rowWrapped
			if len(savedRW) != g.Rows {
				savedRW = make([]bool, g.Rows)
			}
			newCells, newRW2, newSB, newSBW, newCR, newCC := logicalReflow(
				g.mainSaved.cells, savedRW,
				sbRows, sbWrap,
				g.Rows, g.Cols, rows, cols,
				g.mainSaved.cursorR, g.mainSaved.cursorC,
				g.ScrollbackCap,
			)
			g.mainSaved.cells = newCells
			g.mainSaved.rowWrapped = newRW2
			g.repopulateScrollback(newSB, newSBW, cols)
			g.mainSaved.cursorR = newCR
			g.mainSaved.cursorC = newCC
			g.mainSaved.top = 0
			g.mainSaved.bottom = rows - 1
		}
	} else {
		newCells, newRW, newSB, newSBW, newCR, newCC := logicalReflow(
			g.Cells, g.RowWrapped,
			sbRows, sbWrap,
			g.Rows, g.Cols, rows, cols,
			g.CursorR, g.CursorC,
			g.ScrollbackCap,
		)
		g.Cells = newCells
		g.RowWrapped = newRW
		g.repopulateScrollback(newSB, newSBW, cols)
		g.CursorR = newCR
		g.CursorC = newCC
	}

	g.Rows = rows
	g.Cols = cols
	g.Dirty = make([]bool, rows)
	g.markAllDirty()

	g.Top = 0
	g.Bottom = rows - 1
	g.ViewOffset = 0

	if g.SelActive {
		delta := g.Scrollback.Len() - oldSbLen
		total := g.Scrollback.Len() + rows
		g.SelAnchor.Row = clamp(g.SelAnchor.Row+delta, 0, total-1)
		g.SelHead.Row = clamp(g.SelHead.Row+delta, 0, total-1)
	}

	if len(g.Marks) > 0 {
		delta := g.Scrollback.Len() - oldSbLen
		total := g.Scrollback.Len() + rows
		g.shiftMarks(delta, total)
	}

	if len(g.Graphics) > 0 {
		delta := g.Scrollback.Len() - oldSbLen
		total := g.Scrollback.Len() + rows
		g.shiftGraphics(delta, total)
	}
}

// repopulateScrollback resets the ring to (ScrollbackCap, cols) and
// pushes the freshly reflowed rows back in oldest-first. Used by Resize
// at the reflow boundary so a single backing allocation replaces the
// per-row slices reflow produced.
func (g *Grid) repopulateScrollback(rows [][]Cell, wrapped []bool, cols int) {
	g.Scrollback.SetGeom(g.ScrollbackCap, cols)
	for i, row := range rows {
		w := false
		if i < len(wrapped) {
			w = wrapped[i]
		}
		g.Scrollback.Push(row, w)
	}
}
