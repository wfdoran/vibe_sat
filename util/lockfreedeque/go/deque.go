// Package lockfreedeque is a standalone spike (STAGE26.md) validating
// a lock-free, Chase-Lev-style work-stealing double-ended queue in Go,
// before it gets ported into go_src/internal/dfs -- the same
// "prototype tricky concurrency here first" discipline STAGE18.md
// established for termination detection (util/termination) and
// STAGE20.md established for the shared clause pool
// (util/clausesharing).
//
// REPORT22.md's item 3 named this the strongest already-confirmed,
// unaddressed performance finding in the project: two independent
// benchmarks (REPORT18.md's uf250 SAT numbers, REPORT19.md's uuf175
// UNSAT numbers) showed go_src/internal/dfs's hand-rolled,
// sync.Mutex-guarded deque plateauing (and, at higher thread counts,
// regressing) past 4 threads, while Rust's lock-free crossbeam-deque
// kept scaling to 16. This package is a from-scratch reimplementation
// of the deque half of that comparison, generic (Deque[T]) since this
// spike has no reason to depend on go_src's searchNode type; the real
// go_src/internal/dfs/deque.go is an independent, from-scratch port
// specialized to searchNode, per this project's standing rule that
// go_src/rust_src may not depend on util.
//
// Reference: Chase & Lev, "Dynamic Circular Work-Stealing Deque,"
// SPAA 2005.
package lockfreedeque

import "sync/atomic"

// initialCapacity is the starting size of a new Deque's backing
// buffer. Must be a power of two -- see circularBuffer's doc comment
// for why. Deliberately small so ordinary test runs exercise the grow
// path without needing thousands of pushes first.
const initialCapacity = 8

// circularBuffer is one fixed-capacity ring buffer backing a Deque at
// a single point in time. Growing a Deque (see growTo) allocates a
// new, bigger circularBuffer and atomically swaps it in rather than
// mutating this one in place -- a concurrent Steal that already holds
// a reference to an older circularBuffer (captured in a local variable
// before the swap) can keep reading from it safely, since Go's garbage
// collector keeps any value alive for as long as something still
// references it. This is exactly why classic Chase-Lev deques
// implemented in a garbage-collected language never explicitly free an
// old buffer, unlike the original C/C++ formulations, which need
// hazard pointers or epoch-based reclamation to solve the same
// problem safely.
//
// Capacity is required to be a power of two purely so that "index mod
// capacity" can be computed with a bitmask (index & mask) instead of a
// division -- a standard, cheap trick that only works because of that
// power-of-two constraint.
// Each slot is an atomic pointer, not a plain T, for a subtlety this
// package's own stress tests caught under go test -race (see
// TestConcurrentGrowDuringSteals): a thief that stalls for a long time
// between reading a stale top index and finally reading the buffer can
// have that index wrap around (mod a since-grown, larger capacity) to
// alias a slot the owner is concurrently overwriting for a much later
// push. The algorithm is still logically correct even then -- whatever
// the thief speculatively read is only ever trusted if its later CAS
// on top succeeds, and that CAS is guaranteed to fail whenever top has
// actually moved past the thief's stale index (see StealTop's doc
// comment) -- but Go's race detector has no way to know a torn or
// stale plain-value read will be discarded; it correctly flags the
// unsynchronized concurrent access itself as a race regardless of
// what happens to the value afterward. Using atomic.Pointer[T] per
// slot makes the access itself properly synchronized (no torn reads
// possible, and go test -race is satisfied), while the discard-on-
// failed-CAS argument above still carries all the actual correctness
// weight.
type circularBuffer[T any] struct {
	mask int64 // capacity - 1
	data []atomic.Pointer[T]
}

// newCircularBuffer allocates a circularBuffer of the given capacity,
// which must be a power of two.
func newCircularBuffer[T any](capacity int64) *circularBuffer[T] {
	return &circularBuffer[T]{mask: capacity - 1, data: make([]atomic.Pointer[T], capacity)}
}

// capacity returns how many slots b has.
func (b *circularBuffer[T]) capacity() int64 {
	return b.mask + 1
}

// get returns the item logically at position i (i is an ever-
// increasing index into the deque's whole history, not a raw array
// index -- see put's doc comment for why that distinction matters),
// and whether that slot had actually been written. A false return
// happens only for a sufficiently stale, ultimately-discarded
// speculative read (see the type's own doc comment); every caller
// already treats it exactly like a lost race, never a bug.
func (b *circularBuffer[T]) get(i int64) (item T, ok bool) {
	p := b.data[i&b.mask].Load()
	if p == nil {
		return item, false
	}
	return *p, true
}

// put stores item at logical position i. i is the deque's own
// monotonically increasing top/bottom counter, not wrapped by the
// caller -- wrapping (via the mask) happens only here, so the *same*
// logical index always maps to the same slot for a given buffer's
// capacity, regardless of how many times the deque has wrapped around
// it. This is what makes growTo's copy (below) correct: copying
// logical index i from an old, smaller buffer into a new, bigger one
// preserves the same logical index, just re-homed to a larger mask.
func (b *circularBuffer[T]) put(i int64, item T) {
	b.data[i&b.mask].Store(&item)
}

// growTo returns a new circularBuffer with double b's capacity,
// containing a copy of every logical index in [lo, hi) -- the live
// range of the deque at the moment growth is needed (see
// Deque.PushBottom). Copying a slightly wider range than strictly
// necessary (if some prefix of [lo, hi) was concurrently stolen
// between the caller reading lo and calling growTo) is harmless: see
// the package doc comment's correctness discussion and
// TestConcurrentGrowDuringSteals for why a thief can never observe
// incorrect data even in that case.
func (b *circularBuffer[T]) growTo(lo, hi int64) *circularBuffer[T] {
	grown := newCircularBuffer[T](b.capacity() * 2)
	for i := lo; i < hi; i++ {
		// ok is always true here in practice: the owner (the only
		// caller of growTo, via PushBottom) is copying its own
		// previously-written range. Guarded anyway rather than assumed,
		// consistent with every other reader of a circularBuffer slot
		// in this file.
		if item, ok := b.get(i); ok {
			grown.put(i, item)
		}
	}
	return grown
}

// Deque is a lock-free, Chase-Lev-style double-ended queue for one
// work-stealing worker: the owner pushes and pops one end ("bottom",
// LIFO -- this is how the owner's own depth-first descent continues),
// while any other worker may steal from the opposite end ("top",
// FIFO). See go_src/internal/dfs/deque.go's doc comment (the real,
// specialized port of this design) for why that asymmetry is the
// point.
//
// PushBottom and PopBottom must only ever be called by the single
// owning goroutine (never concurrently with each other -- there is
// only ever one owner at a time). StealTop may be called by any
// goroutine, including concurrently with the owner's own operations
// and with other thieves.
//
// Every field is accessed exclusively through atomic operations
// (top/bottom as atomic.Int64, buf as atomic.Pointer), per this
// project's standing direction to use sequentially consistent
// semantics throughout its concurrent code -- Go's sync/atomic typed
// operations are already sequentially consistent by construction
// (Go, unlike Rust, does not expose a weaker-ordering option), so no
// explicit ordering parameter is needed here the way rust_src's own
// atomics require one.
type Deque[T any] struct {
	top    atomic.Int64
	bottom atomic.Int64
	buf    atomic.Pointer[circularBuffer[T]]
}

// New returns an empty Deque ready for use.
func New[T any]() *Deque[T] {
	d := &Deque[T]{}
	d.buf.Store(newCircularBuffer[T](initialCapacity))
	return d
}

// PushBottom adds item to the owner's end of the deque, growing the
// backing buffer first if it is full. Only the owning goroutine may
// call this.
func (d *Deque[T]) PushBottom(item T) {
	b := d.bottom.Load()
	t := d.top.Load()
	buf := d.buf.Load()

	if size := b - t; size >= buf.capacity() {
		buf = buf.growTo(t, b)
		d.buf.Store(buf)
	}

	buf.put(b, item)
	// Publishing the new bottom is the point at which this item
	// becomes visible to a concurrent Steal; it must happen after the
	// put above so a thief that observes the new bottom always finds
	// valid data already in place.
	d.bottom.Store(b + 1)
}

// PopBottom removes and returns the item at the owner's end of the
// deque, if any. Only the owning goroutine may call this.
//
// The single-remaining-item case (b == t below) races directly against
// every concurrent StealTop targeting the same slot; a CAS on top is
// what decides the single winner, exactly mirroring StealTop's own
// CAS. This is the crux of the Chase-Lev algorithm: every other case
// (multiple items left, or none left) needs no synchronization with
// thieves at all, since a thief only ever competes for the single
// oldest item.
func (d *Deque[T]) PopBottom() (item T, ok bool) {
	b := d.bottom.Load() - 1
	buf := d.buf.Load() // safe to read once: only the (single) owner ever resizes, and never concurrently with its own PopBottom.
	d.bottom.Store(b)
	t := d.top.Load()

	if b < t {
		// Already empty (this can happen if a thief raced ahead and
		// took the only remaining item between this call starting and
		// the tentative decrement above); restore bottom to the
		// canonical empty state before reporting failure.
		d.bottom.Store(t)
		var zero T
		return zero, false
	}

	item, valid := buf.get(b)
	if !valid {
		// Cannot happen in practice: b is an index the owner itself
		// wrote (via PushBottom) earlier in its own single-threaded
		// history. Guarded anyway, consistent with every other reader
		// of a circularBuffer slot in this file.
		var zero T
		return zero, false
	}
	if b > t {
		// More than one item remains (before this pop); no thief can
		// be racing for this exact slot, since StealTop always targets
		// top, which is strictly less than b here.
		return item, true
	}

	// Exactly one item remained (b == t): a concurrent StealTop may be
	// racing for it right now. Whichever of this CAS and a thief's own
	// CAS on top wins gets the item; the other gets nothing.
	won := d.top.CompareAndSwap(t, t+1)
	// Either way, the deque is now canonically empty (top == bottom ==
	// t+1); publish that regardless of who won.
	d.bottom.Store(t + 1)
	if !won {
		var zero T
		return zero, false
	}
	return item, true
}

// StealTop removes and returns the item at the opposite end of the
// deque from PushBottom/PopBottom, if any. Any goroutine, including
// the owner (though it never needs to), may call this concurrently
// with anything else.
//
// A `false` return means either the deque was empty, or (indistin-
// guishably, from the caller's point of view) this call lost a race
// for the single remaining item to another StealTop or to the owner's
// own PopBottom -- exactly like the mutex-guarded predecessor's same
// method, callers are expected to treat both as "try another victim,
// or give up," never as an error.
func (d *Deque[T]) StealTop() (item T, ok bool) {
	t := d.top.Load()
	b := d.bottom.Load()
	if b <= t {
		var zero T
		return zero, false
	}

	buf := d.buf.Load()
	item, valid := buf.get(t)
	if !valid {
		// A sufficiently stale t (see circularBuffer's doc comment):
		// treated exactly like a lost race below.
		var zero T
		return zero, false
	}
	if !d.top.CompareAndSwap(t, t+1) {
		// Lost the race -- to another thief, or to the owner's own
		// PopBottom -- for this slot. item may have been read from a
		// buffer snapshot that a concurrent grow has since replaced,
		// but since the CAS above failed, this value is discarded
		// unread by the caller: it is never a possible source of
		// incorrect data, only ever a wasted (and safe) speculative
		// read. See the package doc comment and
		// TestConcurrentGrowDuringSteals.
		var zero T
		return zero, false
	}
	return item, true
}

// IsEmpty reports whether the deque holds no items at the moment of
// the call. Like every other observation of concurrently-mutated
// state in this package, this is a snapshot that may already be stale
// by the time the caller acts on it -- callers that need a stronger
// guarantee (e.g. confirming quiescence across several workers) must
// build that on top, as go_src/internal/dfs/terminator.go does.
func (d *Deque[T]) IsEmpty() bool {
	b := d.bottom.Load()
	t := d.top.Load()
	return b <= t
}
