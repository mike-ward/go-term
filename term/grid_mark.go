package term

// MarkKind classifies an OSC 133 semantic shell-integration mark.
type MarkKind uint8

// Mark records a command-boundary position in content coordinates.
// Row is a content row index (scrollback + live), stable across ViewOffset
// changes. Adjusted when scrollback is trimmed or the grid is resized.
type Mark struct {
	Row  int
	Kind MarkKind
}

// AddMark records an OSC 133 command boundary at the cursor's current
// content row. Caller holds Mu. Marks in the alt screen are not recorded
// (full-screen apps like vim/htop don't emit OSC 133).
func (g *Grid) AddMark(kind MarkKind) {
	if g.AltActive {
		return
	}
	row := g.Scrollback.Len() + g.CursorR
	g.Marks = append(g.Marks, Mark{Row: row, Kind: kind})
	if len(g.Marks) > maxMarks {
		g.Marks = g.Marks[len(g.Marks)-maxMarks:]
	}
}

// PrevMark returns the content row of the last mark of kind strictly
// before row, and true. Returns 0, false when no such mark exists.
// Caller holds Mu.
func (g *Grid) PrevMark(row int, kind MarkKind) (int, bool) {
	for i := len(g.Marks) - 1; i >= 0; i-- {
		if g.Marks[i].Kind == kind && g.Marks[i].Row < row {
			return g.Marks[i].Row, true
		}
	}
	return 0, false
}

// NextMark returns the content row of the first mark of kind strictly
// after row, and true. Returns 0, false when no such mark exists.
// Caller holds Mu.
func (g *Grid) NextMark(row int, kind MarkKind) (int, bool) {
	for _, m := range g.Marks {
		if m.Kind == kind && m.Row > row {
			return m.Row, true
		}
	}
	return 0, false
}

// trimMarks removes extra rows from the front of all mark row indices and
// drops marks that fall below 0. Called after scrollback is trimmed.
// Caller holds Mu.
func (g *Grid) trimMarks(extra int) {
	j := 0
	for _, m := range g.Marks {
		m.Row -= extra
		if m.Row >= 0 {
			g.Marks[j] = m
			j++
		}
	}
	g.Marks = g.Marks[:j]
}

// shiftMarks applies delta to all mark row indices, dropping marks that
// fall outside [0, total). Called after resize changes scrollback depth.
// Caller holds Mu.
func (g *Grid) shiftMarks(delta, total int) {
	j := 0
	for _, m := range g.Marks {
		m.Row += delta
		if m.Row >= 0 && m.Row < total {
			g.Marks[j] = m
			j++
		}
	}
	g.Marks = g.Marks[:j]
}
