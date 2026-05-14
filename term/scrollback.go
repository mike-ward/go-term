package term

// scrollbackRing stores scrolled-off rows in a fixed-capacity ring backed
// by a single contiguous []Cell. Push overwrites the oldest slot when
// full, so steady-state scrolling allocates zero per-row memory.
//
// Row returns a slice that aliases the backing buffer; callers must not
// retain it across a subsequent Push/SetGeom/Reset. All current readers
// consume the returned cells immediately under Grid.Mu.
type scrollbackRing struct {
	cells   []Cell // len = cap*cols (nil when disabled)
	wrapped []bool // len = cap     (nil when disabled)
	cols    int
	cap     int
	head    int // slot of oldest row
	size    int // 0..cap
}

func (r *scrollbackRing) Len() int { return r.size }

func (r *scrollbackRing) slot(i int) int { return (r.head + i) % r.cap }

func (r *scrollbackRing) Row(i int) []Cell {
	if i < 0 || i >= r.size || r.cols <= 0 {
		return nil
	}
	s := r.slot(i)
	return r.cells[s*r.cols : (s+1)*r.cols]
}

func (r *scrollbackRing) Wrapped(i int) bool {
	if i < 0 || i >= r.size {
		return false
	}
	return r.wrapped[r.slot(i)]
}

// Push appends src (cols wide) as the newest row. Returns true when an
// existing row was evicted. Short src is zero-padded; over-long src is
// truncated.
func (r *scrollbackRing) Push(src []Cell, wrapped bool) bool {
	if r.cap == 0 || r.cols == 0 {
		return false
	}
	var slot int
	evicted := r.size == r.cap
	if evicted {
		slot = r.head
		r.head = (r.head + 1) % r.cap
	} else {
		slot = (r.head + r.size) % r.cap
		r.size++
	}
	dst := r.cells[slot*r.cols : (slot+1)*r.cols]
	n := copy(dst, src)
	clear(dst[n:])
	r.wrapped[slot] = wrapped
	return evicted
}

func (r *scrollbackRing) Reset() { r.head, r.size = 0, 0 }

// SetGeom reallocates at (capacity, cols), dropping stored rows. Negative
// inputs clamp to zero; an absurd capacity*cols is bounded so make can't
// panic. Inputs are normally clamped upstream — this is belt-and-braces.
func (r *scrollbackRing) SetGeom(capacity, cols int) {
	if capacity < 0 {
		capacity = 0
	}
	if cols < 0 {
		cols = 0
	}
	const maxCells = 1 << 28
	if capacity > 0 && cols > 0 && capacity > maxCells/cols {
		capacity = maxCells / cols
	}
	r.cap, r.cols = capacity, cols
	r.head, r.size = 0, 0
	if capacity > 0 && cols > 0 {
		r.cells = make([]Cell, capacity*cols)
		r.wrapped = make([]bool, capacity)
		return
	}
	r.cells = nil
	r.wrapped = nil
}

// EnsureGeom adjusts geometry if it differs. cols change resets content
// (caller repushes). Capacity-only change preserves the newest rows up to
// the new capacity in a single copy pass; oldest are discarded when
// shrinking.
func (r *scrollbackRing) EnsureGeom(capacity, cols int) {
	if capacity < 0 {
		capacity = 0
	}
	if cols < 0 {
		cols = 0
	}
	if r.cap == capacity && r.cols == cols {
		return
	}
	if cols != r.cols || r.size == 0 || capacity == 0 || cols == 0 {
		r.SetGeom(capacity, cols)
		return
	}
	keep := min(r.size, capacity)
	newCells := make([]Cell, capacity*cols)
	newWrap := make([]bool, capacity)
	for i := range keep {
		s := r.slot(r.size - keep + i)
		copy(newCells[i*cols:(i+1)*cols], r.cells[s*cols:(s+1)*cols])
		newWrap[i] = r.wrapped[s]
	}
	r.cells = newCells
	r.wrapped = newWrap
	r.cap = capacity
	r.head = 0
	r.size = keep
}
