package term

import (
	"regexp"
	"unicode"
	"unicode/utf8"
)

// ContentPos is a stable content-row coordinate, independent of ViewOffset.
// Rows 0..len(Scrollback)-1 index scrollback oldest-first;
// rows len(Scrollback)..len(Scrollback)+Rows-1 index the live grid.
type ContentPos struct{ Row, Col int }

// SearchMatch pairs a content position with the column-span of the match.
// Len is in rune columns (not bytes), matching the Cell column space.
type SearchMatch struct {
	ContentPos
	Len int
}

// equalFoldRune reports whether a and b are equal under Unicode case-folding.
func equalFoldRune(a, b rune) bool {
	return unicode.ToLower(a) == unicode.ToLower(b)
}

// runeSliceSearch returns the first column index >= fromCol where needle
// occurs in haystack. Returns -1 when not found. Case-insensitive.
func runeSliceSearch(haystack, needle []rune, fromCol int) int {
	n, m := len(haystack), len(needle)
	if m == 0 || fromCol > n-m {
		return -1
	}
	if fromCol < 0 {
		fromCol = 0
	}
	for i := fromCol; i <= n-m; i++ {
		match := true
		for j := 0; j < m; j++ {
			if !equalFoldRune(haystack[i+j], needle[j]) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// runeSliceSearchLast returns the rightmost column index < upToCol where
// needle occurs in haystack. Returns -1 when not found. Case-insensitive.
func runeSliceSearchLast(haystack, needle []rune, upToCol int) int {
	n, m := len(haystack), len(needle)
	if m == 0 || n < m {
		return -1
	}
	maxStart := n - m
	if upToCol-1 < maxStart {
		maxStart = upToCol - 1
	}
	if maxStart < 0 {
		return -1
	}
	for i := maxStart; i >= 0; i-- {
		match := true
		for j := 0; j < m; j++ {
			if !equalFoldRune(haystack[i+j], needle[j]) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// Find searches for query (case-insensitive) starting at start, walking
// forward or backward through all content rows (scrollback + live), wrapping
// once. Multi-row spanning is not supported; matches must fit within one row.
// Returns the ContentPos of the first cell of the match and true on success.
// Called under Mu.
func (g *Grid) Find(query string, start ContentPos, forward bool) (ContentPos, bool) {
	if query == "" || g.Cols <= 0 {
		return ContentPos{}, false
	}
	qRunes := []rune(query)
	if len(qRunes) > g.Cols {
		return ContentPos{}, false
	}
	total := g.ContentRows()
	if total == 0 {
		return ContentPos{}, false
	}
	start.Row = clamp(start.Row, 0, total-1)
	for i := 0; i < total; i++ {
		var row int
		if forward {
			row = (start.Row + i) % total
		} else {
			row = (start.Row - i + total) % total
		}
		rr := g.rowRunes(row)
		if forward {
			fromCol := 0
			if i == 0 {
				fromCol = start.Col + 1
			}
			if col := runeSliceSearch(rr, qRunes, fromCol); col >= 0 {
				return ContentPos{Row: row, Col: col}, true
			}
		} else {
			upToCol := len(rr) + 1
			if i == 0 {
				upToCol = start.Col
			}
			if col := runeSliceSearchLast(rr, qRunes, upToCol); col >= 0 {
				return ContentPos{Row: row, Col: col}, true
			}
		}
	}
	return ContentPos{}, false
}

// ViewportMatches returns all plain-text matches visible at the current
// ViewOffset. Returns nil for an empty query, a zero-column grid, or while
// the alt screen is active. Called under Mu.
func (g *Grid) ViewportMatches(query string) []SearchMatch {
	if query == "" || g.Cols <= 0 || g.AltActive {
		return nil
	}
	qRunes := []rune(query)
	qLen := len(qRunes)
	if qLen > g.Cols {
		return nil
	}
	sb := g.Scrollback.Len()
	off := clamp(g.ViewOffset, 0, sb)
	n := min(off, g.Rows)
	var matches []SearchMatch
	for vr := range g.Rows {
		var contentRow int
		if vr < n {
			contentRow = sb - off + vr
		} else {
			contentRow = sb + (vr - n)
		}
		rr := g.rowRunes(contentRow)
		col := 0
		for {
			idx := runeSliceSearch(rr, qRunes, col)
			if idx < 0 {
				break
			}
			matches = append(matches, SearchMatch{ContentPos: ContentPos{Row: contentRow, Col: idx}, Len: qLen})
			col = idx + 1
		}
	}
	return matches
}

// regexSearchForward returns the first regex match in rr with rune column >=
// fromCol. Returns the column, match length in rune columns, and true on
// success.
func regexSearchForward(rr []rune, re *regexp.Regexp, fromCol int) (col, matchLen int, found bool) {
	s := string(rr)
	for _, loc := range re.FindAllStringIndex(s, -1) {
		c := utf8.RuneCountInString(s[:loc[0]])
		if c >= fromCol {
			return c, utf8.RuneCountInString(s[:loc[1]]) - c, true
		}
	}
	return 0, 0, false
}

// regexSearchLast returns the last regex match in rr with rune column <
// upToCol.
func regexSearchLast(rr []rune, re *regexp.Regexp, upToCol int) (col, matchLen int, found bool) {
	s := string(rr)
	col = -1
	for _, loc := range re.FindAllStringIndex(s, -1) {
		c := utf8.RuneCountInString(s[:loc[0]])
		if c < upToCol {
			col = c
			matchLen = utf8.RuneCountInString(s[:loc[1]]) - c
		}
	}
	if col < 0 {
		return 0, 0, false
	}
	return col, matchLen, true
}

// FindRegex searches for the first match of re starting at start, walking
// forward or backward through all content rows (scrollback + live), wrapping
// once. Returns the ContentPos, match length in rune columns, and true on
// success. Called under Mu.
func (g *Grid) FindRegex(re *regexp.Regexp, start ContentPos, forward bool) (ContentPos, int, bool) {
	if re == nil || g.Cols <= 0 {
		return ContentPos{}, 0, false
	}
	total := g.ContentRows()
	if total == 0 {
		return ContentPos{}, 0, false
	}
	start.Row = clamp(start.Row, 0, total-1)
	for i := range total {
		var row int
		if forward {
			row = (start.Row + i) % total
		} else {
			row = (start.Row - i + total) % total
		}
		rr := g.rowRunes(row)
		if forward {
			fromCol := 0
			if i == 0 {
				fromCol = start.Col + 1
			}
			if c, l, ok := regexSearchForward(rr, re, fromCol); ok {
				return ContentPos{Row: row, Col: c}, l, true
			}
		} else {
			upToCol := len(rr) + 1
			if i == 0 {
				upToCol = start.Col
			}
			if c, l, ok := regexSearchLast(rr, re, upToCol); ok {
				return ContentPos{Row: row, Col: c}, l, true
			}
		}
	}
	return ContentPos{}, 0, false
}

// ViewportMatchesRegex returns all regex matches visible at the current
// ViewOffset. Returns nil for a nil pattern or while the alt screen is active.
// Called under Mu.
func (g *Grid) ViewportMatchesRegex(re *regexp.Regexp) []SearchMatch {
	if re == nil || g.Cols <= 0 || g.AltActive {
		return nil
	}
	sb := g.Scrollback.Len()
	off := clamp(g.ViewOffset, 0, sb)
	n := min(off, g.Rows)
	var matches []SearchMatch
	for vr := range g.Rows {
		var contentRow int
		if vr < n {
			contentRow = sb - off + vr
		} else {
			contentRow = sb + (vr - n)
		}
		rr := g.rowRunes(contentRow)
		col := 0
		for {
			c, l, ok := regexSearchForward(rr, re, col)
			if !ok {
				break
			}
			matches = append(matches, SearchMatch{ContentPos: ContentPos{Row: contentRow, Col: c}, Len: l})
			col = c + max(l, 1)
		}
	}
	return matches
}
