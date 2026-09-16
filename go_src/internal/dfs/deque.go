package dfs

import "sync/atomic"

// deque is a lock-free, Chase-Lev-style double-ended queue for one
// parallel DFS worker (STAGE18.md, rewritten lock-free per STAGE26.md):
// the owner pushes and pops one end ("bottom", LIFO -- this is how the
// owner's own depth-first descent continues), while any other worker
// may steal from the opposite end ("top", FIFO). This asymmetry is the
// point (per STAGE18.md/REPORT16.md, citing the Chase-Lev deque):
// stealing from the same end the owner works from tends to hand a
// thief the smallest, freshest branch, causing lots of small,
// unproductive steals; stealing from the far end instead hands the
// thief the oldest, and typically largest, unexplored branch.
//
// Reference: Chase & Lev, "Dynamic Circular Work-Stealing Deque,"
// SPAA 2005.
//
// This replaces the original mutex-guarded implementation (STAGE18.md)
// per STAGE26.md/REPORT22.md item 3: two independent benchmarks
// (REPORT18.md's uf250 SAT numbers, REPORT19.md's uuf175 UNSAT
// numbers) showed the mutex-guarded version plateauing, and even
// regressing, past 4 threads on this machine, while Rust's lock-free
// crossbeam-deque kept scaling to 16 -- see REPORT26.md for the
// before/after remeasurement.
//
// Per this project's established practice for tricky concurrency
// primitives (STAGE18.md's termination detection, STAGE20.md's shared
// clause pool), this design was validated standalone first, generic
// and stress-tested under go test -race, in
// util/lockfreedeque/go/deque.go -- including specifically exercising
// a buffer grow happening concurrently with active steals, the
// trickiest part of this algorithm. This file is an independent,
// from-scratch reimplementation specialized to searchNode, per this
// project's standing rule that go_src may not depend on util; nothing
// here imports that package.
type deque struct {
	top    atomic.Int64
	bottom atomic.Int64
	buf    atomic.Pointer[circularBuffer]
}

// dequeInitialCapacity is the starting size of a new deque's backing
// buffer. Must be a power of two -- see circularBuffer's doc comment
// for why.
const dequeInitialCapacity = 32

// circularBuffer is one fixed-capacity ring buffer backing a deque at
// a single point in time. Growing a deque (see growTo) allocates a
// new, bigger circularBuffer and atomically swaps it in rather than
// mutating this one in place -- a concurrent stealTop that already
// holds a reference to an older circularBuffer (captured in a local
// variable before the swap) can keep reading from it safely, since
// Go's garbage collector keeps any value alive for as long as
// something still references it. This is exactly why classic
// Chase-Lev deques implemented in a garbage-collected language never
// explicitly free an old buffer, unlike the original C/C++
// formulations, which need hazard pointers or epoch-based reclamation
// to solve the same problem safely.
//
// Capacity is required to be a power of two purely so that "index mod
// capacity" can be computed with a bitmask (index & mask) instead of a
// division -- a standard, cheap trick that only works because of that
// power-of-two constraint.
//
// Each slot is an atomic pointer, not a plain searchNode, for a
// subtlety util/lockfreedeque/go's own stress tests caught under
// go test -race: a thief that stalls for a long time between reading a
// stale top index and finally reading the buffer can have that index
// wrap around (mod a since-grown, larger capacity) to alias a slot the
// owner is concurrently overwriting for a much later push. The
// algorithm is still logically correct even then -- whatever the
// thief speculatively read is only ever trusted if its later CAS on
// top succeeds, and that CAS is guaranteed to fail whenever top has
// actually moved past the thief's stale index (see stealTop's doc
// comment) -- but Go's race detector has no way to know a torn or
// stale plain-value read will be discarded; it correctly flags the
// unsynchronized concurrent access itself as a race regardless of what
// happens to the value afterward. Using an atomic pointer per slot
// makes the access itself properly synchronized (no torn reads
// possible, and go test -race is satisfied), while the
// discard-on-failed-CAS argument above still carries all the actual
// correctness weight.
type circularBuffer struct {
	mask int64 // capacity - 1
	data []atomic.Pointer[searchNode]
}

// newCircularBuffer allocates a circularBuffer of the given capacity,
// which must be a power of two.
func newCircularBuffer(capacity int64) *circularBuffer {
	return &circularBuffer{mask: capacity - 1, data: make([]atomic.Pointer[searchNode], capacity)}
}

// capacity returns how many slots b has.
func (b *circularBuffer) capacity() int64 {
	return b.mask + 1
}

// get returns the node logically at position i (i is an ever-
// increasing index into the deque's whole history, not a raw array
// index -- see put's doc comment for why that distinction matters),
// and whether that slot had actually been written. A false return
// happens only for a sufficiently stale, ultimately-discarded
// speculative read (see the type's own doc comment); every caller
// already treats it exactly like a lost race, never a bug.
func (b *circularBuffer) get(i int64) (node searchNode, ok bool) {
	p := b.data[i&b.mask].Load()
	if p == nil {
		return node, false
	}
	return *p, true
}

// put stores node at logical position i. i is the deque's own
// monotonically increasing top/bottom counter, not wrapped by the
// caller -- wrapping (via the mask) happens only here, so the *same*
// logical index always maps to the same slot for a given buffer's
// capacity, regardless of how many times the deque has wrapped around
// it. This is what makes growTo's copy (below) correct: copying
// logical index i from an old, smaller buffer into a new, bigger one
// preserves the same logical index, just re-homed to a larger mask.
func (b *circularBuffer) put(i int64, node searchNode) {
	b.data[i&b.mask].Store(&node)
}

// growTo returns a new circularBuffer with double b's capacity,
// containing a copy of every logical index in [lo, hi) -- the live
// range of the deque at the moment growth is needed (see
// deque.pushBottom). Copying a slightly wider range than strictly
// necessary (if some prefix of [lo, hi) was concurrently stolen
// between the caller reading lo and calling growTo) is harmless: see
// circularBuffer's doc comment.
func (b *circularBuffer) growTo(lo, hi int64) *circularBuffer {
	grown := newCircularBuffer(b.capacity() * 2)
	for i := lo; i < hi; i++ {
		// ok is always true here in practice: the owner (the only
		// caller of growTo, via pushBottom) is copying its own
		// previously-written range. Guarded anyway rather than assumed,
		// consistent with every other reader of a circularBuffer slot
		// in this file.
		if node, ok := b.get(i); ok {
			grown.put(i, node)
		}
	}
	return grown
}

// newDeque returns an empty deque ready for use.
func newDeque() *deque {
	d := &deque{}
	d.buf.Store(newCircularBuffer(dequeInitialCapacity))
	return d
}

// pushBottom adds node to the owner's end of the deque, growing the
// backing buffer first if it is full. Only the owning worker should
// call this.
func (d *deque) pushBottom(node searchNode) {
	b := d.bottom.Load()
	t := d.top.Load()
	buf := d.buf.Load()

	if size := b - t; size >= buf.capacity() {
		buf = buf.growTo(t, b)
		d.buf.Store(buf)
	}

	buf.put(b, node)
	// Publishing the new bottom is the point at which this node
	// becomes visible to a concurrent stealTop; it must happen after
	// the put above so a thief that observes the new bottom always
	// finds valid data already in place.
	d.bottom.Store(b + 1)
}

// popBottom removes and returns the node at the owner's end of the
// deque, if any. Only the owning worker should call this.
//
// The single-remaining-item case (b == t below) races directly against
// every concurrent stealTop targeting the same slot; a CAS on top is
// what decides the single winner, exactly mirroring stealTop's own
// CAS. This is the crux of the Chase-Lev algorithm: every other case
// (multiple nodes left, or none left) needs no synchronization with
// thieves at all, since a thief only ever competes for the single
// oldest node.
func (d *deque) popBottom() (node searchNode, ok bool) {
	b := d.bottom.Load() - 1
	buf := d.buf.Load() // safe to read once: only the (single) owner ever resizes, and never concurrently with its own popBottom.
	d.bottom.Store(b)
	t := d.top.Load()

	if b < t {
		// Already empty (this can happen if a thief raced ahead and
		// took the only remaining node between this call starting and
		// the tentative decrement above); restore bottom to the
		// canonical empty state before reporting failure.
		d.bottom.Store(t)
		return node, false
	}

	node, valid := buf.get(b)
	if !valid {
		// Cannot happen in practice: b is an index the owner itself
		// wrote (via pushBottom) earlier in its own single-threaded
		// history. Guarded anyway, consistent with every other reader
		// of a circularBuffer slot in this file.
		return searchNode{}, false
	}
	if b > t {
		// More than one node remains (before this pop); no thief can
		// be racing for this exact slot, since stealTop always targets
		// top, which is strictly less than b here.
		return node, true
	}

	// Exactly one node remained (b == t): a concurrent stealTop may be
	// racing for it right now. Whichever of this CAS and a thief's own
	// CAS on top wins gets the node; the other gets nothing.
	won := d.top.CompareAndSwap(t, t+1)
	// Either way, the deque is now canonically empty (top == bottom ==
	// t+1); publish that regardless of who won.
	d.bottom.Store(t + 1)
	if !won {
		return searchNode{}, false
	}
	return node, true
}

// stealTop removes and returns the node at the opposite end of the
// deque from pushBottom/popBottom, if any. Any worker (including the
// owner, though it never needs to) may call this concurrently with
// anything else.
//
// A false ok means either the deque was empty, or (indistinguishably,
// from the caller's point of view) this call lost a race for the
// single remaining node to another stealTop or to the owner's own
// popBottom -- exactly like the mutex-guarded predecessor's same
// method, callers are expected to treat both as "try another victim,
// or give up," never as an error.
func (d *deque) stealTop() (node searchNode, ok bool) {
	t := d.top.Load()
	b := d.bottom.Load()
	if b <= t {
		return node, false
	}

	buf := d.buf.Load()
	node, valid := buf.get(t)
	if !valid {
		// A sufficiently stale t (see circularBuffer's doc comment):
		// treated exactly like a lost race below.
		return searchNode{}, false
	}
	if !d.top.CompareAndSwap(t, t+1) {
		// Lost the race -- to another thief, or to the owner's own
		// popBottom -- for this slot. node may have been read from a
		// buffer snapshot that a concurrent grow has since replaced,
		// but since the CAS above failed, this value is discarded
		// unread by the caller: it is never a possible source of
		// incorrect data, only ever a wasted (and safe) speculative
		// read.
		return searchNode{}, false
	}
	return node, true
}

// isEmpty reports whether the deque currently holds no nodes at the
// moment of the call. Like every other observation of concurrently-
// mutated state in this file, this is a snapshot that may already be
// stale by the time the caller acts on it -- terminator's recheck
// step (see terminator.go) is built to tolerate exactly that.
func (d *deque) isEmpty() bool {
	b := d.bottom.Load()
	t := d.top.Load()
	return b <= t
}
