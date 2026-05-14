package term

import "strings"

// SelPos identifies a viewport cell (row, col). Kept for callers that
// predate Phase 17; new code should use ContentPos for selection.
type SelPos struct{ Row, Col int }

// selOrder returns the selection bounds in forward order (start <= end).
func (g *Grid) selOrder() (start, end ContentPos) {
	a, b := g.SelAnchor, g.SelHead
	if b.Row < a.Row || (b.Row == a.Row && b.Col < a.Col) {
		a, b = b, a
	}
	return a, b
}

// InSelection reports whether viewport (r, c) is inside the selection.
// r is a viewport row; it is converted to content coordinates internally
// so the highlight follows content regardless of ViewOffset. False when
// SelActive is off.
func (g *Grid) InSelection(r, c int) bool {
	if !g.SelActive {
		return false
	}
	contentR := g.viewportToContent(r)
	s, e := g.selOrder()
	if contentR < s.Row || contentR > e.Row {
		return false
	}
	if contentR == s.Row && c < s.Col {
		return false
	}
	if contentR == e.Row && c > e.Col {
		return false
	}
	return true
}

// SelectedText extracts the selection as a UTF-8 string. Trailing
// blanks per row are trimmed; row breaks emit '\n' (kitty convention).
// Returns "" when nothing is selected. Coordinates are content-relative
// and are clamped to [0, len(Scrollback)+Rows-1] so stale coords from
// a Resize never produce a negative span.
func (g *Grid) SelectedText() string {
	if !g.SelActive || g.Rows <= 0 || g.Cols <= 0 {
		return ""
	}
	total := g.Scrollback.Len() + g.Rows
	s, e := g.selOrder()
	s.Row, s.Col = clamp(s.Row, 0, total-1), clamp(s.Col, 0, g.Cols-1)
	e.Row, e.Col = clamp(e.Row, 0, total-1), clamp(e.Col, 0, g.Cols-1)
	if s == e {
		return ""
	}
	var b strings.Builder
	b.Grow((e.Row-s.Row+1)*g.Cols + (e.Row - s.Row))
	for r := s.Row; r <= e.Row; r++ {
		c0, c1 := 0, g.Cols-1
		if r == s.Row {
			c0 = s.Col
		}
		if r == e.Row {
			c1 = e.Col
		}

		end := c0 - 1
		for c := c0; c <= c1; c++ {
			if g.ContentCellAt(r, c).Ch != ' ' {
				end = c
			}
		}
		for c := c0; c <= end; c++ {
			b.WriteRune(g.ContentCellAt(r, c).Ch)
		}
		if r < e.Row {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// ClearSelection drops any active selection.
func (g *Grid) ClearSelection() {
	g.SelActive = false
	g.SelAnchor = ContentPos{}
	g.SelHead = ContentPos{}
}
