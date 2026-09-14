package termination

import "sync"

// Deque is a simple mutex-guarded double-ended queue for one
// work-stealing worker: the owner pushes and pops one end
// ("bottom", LIFO -- this is how the owner's own depth-first descent
// continues), while any other worker may steal from the opposite end
// ("top", FIFO). This asymmetry is the point (per STAGE18.md/
// REPORT16.md, citing the Chase-Lev deque): stealing from the same
// end the owner works from tends to hand a thief the smallest,
// freshest, most-recently-created (and so typically least-explored,
// or already-nearly-exhausted) branch, causing lots of small,
// unproductive steals; stealing from the far end instead hands the
// thief the oldest, and typically largest, unexplored branch, giving
// it a meaningfully sized chunk of work per steal.
//
// This is a deliberately simple, correctness-first implementation (a
// single mutex around a slice) rather than a lock-free structure like
// the real Chase-Lev deque: it is only ever used here to validate the
// termination-detection protocol in isolation (see terminator.go),
// not to measure or optimize steal throughput. The real Go dfs
// implementation (go_src/internal/dfs) uses the same design, but is
// independent code -- this package is a standalone spike, per
// STAGE18.md, and nothing in go_src may import it.
type Deque[T any] struct {
	mu    sync.Mutex
	items []T
}

// PushBottom adds item to the owner's end of the deque.
func (d *Deque[T]) PushBottom(item T) {
	d.mu.Lock()
	d.items = append(d.items, item)
	d.mu.Unlock()
}

// PopBottom removes and returns the item at the owner's end of the
// deque, if any. Only the owning worker should call this.
func (d *Deque[T]) PopBottom() (item T, ok bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.items) == 0 {
		return item, false
	}
	last := len(d.items) - 1
	item = d.items[last]
	d.items = d.items[:last]
	return item, true
}

// StealTop removes and returns the item at the opposite end of the
// deque from PushBottom/PopBottom, if any. Any worker (including the
// owner, though it never needs to) may call this.
func (d *Deque[T]) StealTop() (item T, ok bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.items) == 0 {
		return item, false
	}
	item = d.items[0]
	d.items = d.items[1:]
	return item, true
}

// IsEmpty reports whether the deque currently holds no items. Used by
// Terminator's recheck step (see terminator.go) to confirm quiescence
// across every worker's deque.
func (d *Deque[T]) IsEmpty() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.items) == 0
}
