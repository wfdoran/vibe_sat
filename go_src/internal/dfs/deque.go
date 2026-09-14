package dfs

import "sync"

// deque is a simple mutex-guarded double-ended queue for one parallel
// DFS worker (STAGE18.md): the owner pushes and pops one end
// ("bottom", LIFO -- this is how the owner's own depth-first descent
// continues), while any other worker may steal from the opposite end
// ("top", FIFO). This asymmetry is the point (per STAGE18.md/
// REPORT16.md, citing the Chase-Lev deque): stealing from the same
// end the owner works from tends to hand a thief the smallest,
// freshest branch, causing lots of small, unproductive steals;
// stealing from the far end instead hands the thief the oldest, and
// typically largest, unexplored branch.
//
// This is a deliberately simple, correctness-first implementation (a
// single mutex around a slice) rather than a lock-free structure --
// the same choice this project's Rust implementation deliberately
// does *not* make (it uses the crossbeam-deque crate's real Chase-Lev
// deque instead, per your go-ahead), continuing the asymmetry between
// the two languages' idiomatic approaches already established for
// work-stealing in this project (see reports/REPORT16.md/REPORT18.md).
// This design (including the API shape) was validated standalone
// first in util/termination/go/deque.go, a separate module nothing
// here imports; this is an independent, from-scratch reimplementation
// for real search nodes, per STAGE18.md's explicit requirement that
// go_src not depend on util.
type deque struct {
	mu    sync.Mutex
	items []searchNode
}

// pushBottom adds node to the owner's end of the deque.
func (d *deque) pushBottom(node searchNode) {
	d.mu.Lock()
	d.items = append(d.items, node)
	d.mu.Unlock()
}

// popBottom removes and returns the node at the owner's end of the
// deque, if any. Only the owning worker should call this.
func (d *deque) popBottom() (node searchNode, ok bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.items) == 0 {
		return node, false
	}
	last := len(d.items) - 1
	node = d.items[last]
	d.items = d.items[:last]
	return node, true
}

// stealTop removes and returns the node at the opposite end of the
// deque from pushBottom/popBottom, if any. Any worker (including the
// owner, though it never needs to) may call this.
func (d *deque) stealTop() (node searchNode, ok bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.items) == 0 {
		return node, false
	}
	node = d.items[0]
	d.items = d.items[1:]
	return node, true
}

// isEmpty reports whether the deque currently holds no nodes. Used by
// terminator's recheck step (see terminator.go) to confirm quiescence
// across every worker's deque.
func (d *deque) isEmpty() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.items) == 0
}
