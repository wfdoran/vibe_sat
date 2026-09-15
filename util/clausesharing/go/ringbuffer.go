// Package clausesharing is a standalone, language-agnostic prototype for
// STAGE20.md's question "can you implement [continuous clause sharing]
// with minimal contention?" It validates a lock-free, single-writer/
// multi-reader (SPMC) ring buffer suitable as each search thread's
// "export outbox" in a multithreaded CDCL design: the owning thread
// publishes newly learned clauses that pass a sharing filter (short
// length / good LBD) into its own buffer with no locking at all; every
// other thread drains other threads' buffers independently, also with
// no locking, at whatever cadence it likes (e.g. once per restart).
//
// This is NOT imported by go_src or rust_src (util/ is a standalone
// playground, per STAGE16.md/STAGE18.md's authorization) -- it exists
// purely to validate the design before it's (re)implemented for real
// inside internal/cdcl, independently, the same way
// util/termination/ was validated ahead of Stage 18's real dfs work.
//
// # Why lock-free is even possible here
//
// A shared clause pool has a property termination detection (Stage 18)
// does not: losing a share is never a correctness problem. If a reader
// misses a clause because the writer overwrote its slot first, the
// only cost is a missed optimization -- some other thread might
// re-derive that clause itself later, at some CPU cost, but the search
// itself stays perfectly sound either way (nothing about SAT/UNSAT
// correctness depends on which shared clauses arrive, or when). That
// asymmetry (best-effort, not exactly-once) is exactly what licenses a
// lossy, non-blocking design: a slow reader can simply be lapped by
// the writer rather than requiring backpressure or locking.
//
// # Design: atomic pointer swap, not a hand-rolled seqlock
//
// The first version of this prototype used a classic seqlock (a
// sequence counter flanking a plain in-place write, with readers
// discarding "torn" reads caught mid-update). That's the standard C/
// kernel technique, but it turned out to be the wrong tool here: it
// requires plain (non-atomic) reads and writes of the payload itself,
// and `go test -race` correctly flags the in-progress-write case as a
// genuine data race (two goroutines touching the same plain memory
// with no happens-before edge between them for that specific
// interleaving) even though the seqlock protocol safely discards that
// exact read afterward. Real seqlock implementations are "benign
// races" tolerated by construction in C, but Go's race detector (and
// Rust's aliasing rules, for that matter) don't have a way to
// special-case "this particular race is fine, trust me."
//
// The fix: never touch a slot's payload in place. Each slot is instead
// an `atomic.Pointer[clauseData]` to an *immutable* published clause.
// Publish allocates a brand new `clauseData` and atomically swaps the
// slot's pointer to it; readers atomically load the pointer and read
// the (immutable, never mutated after construction) clause it points
// to. There is now exactly one atomic operation on each side, no
// plain-memory interleaving ever occurs, Go's garbage collector keeps
// a clause alive for as long as any reader holds a loaded pointer to
// it (no manual reclamation/ABA hazard the way raw pointers in C or
// unsafe Rust would need), and `go test -race` is clean by
// construction, not by argument. Every atomic here uses SeqCst, per
// this project's Stage 18 directive to keep using it "even in hot
// code."
package clausesharing

import "sync/atomic"

// clauseData is an immutable published clause: constructed once by
// Publish and never modified afterward, so any number of readers can
// safely hold a pointer to the same instance concurrently. ID is a
// per-buffer monotonically increasing publish counter, exposed so
// tests (and, in a real caller, dedup-on-import logic) can identify
// which specific publish a read observed, independent of which ring
// slot it landed in.
type clauseData struct {
	id   uint64
	lits []int32
}

// ExportBuffer is one thread's outbox: a fixed-capacity ring buffer of
// atomic clause pointers that thread alone writes to (via Publish),
// and every other thread may read from concurrently (via TryRead)
// without ever taking a lock or blocking the writer.
type ExportBuffer struct {
	slots    []atomic.Pointer[clauseData]
	capacity uint64
	writeIdx atomic.Uint64 // next slot index to write, monotonically increasing
}

// NewExportBuffer creates a ring buffer with room for capacity
// clauses. Once full, Publish silently overwrites the oldest entry --
// a slow reader simply loses ground, per the "lossy is fine" reasoning
// above.
func NewExportBuffer(capacity int) *ExportBuffer {
	return &ExportBuffer{slots: make([]atomic.Pointer[clauseData], capacity), capacity: uint64(capacity)}
}

// Publish stores a newly learned clause into the buffer. Must only
// ever be called by the single owning thread -- concurrent callers
// would race on writeIdx (two writers could target the same slot
// simultaneously); nothing prevents a caller from doing that, so it's
// a documented precondition, not something enforced here.
func (b *ExportBuffer) Publish(id uint64, lits []int32) {
	owned := append([]int32(nil), lits...) // clauseData must own its data; never alias caller's slice
	idx := b.writeIdx.Load()
	b.slots[idx%b.capacity].Store(&clauseData{id: id, lits: owned})
	b.writeIdx.Store(idx + 1)
}

// TryRead attempts to read whatever clause currently sits at ring
// slot idx%capacity. Returns ok=false only if that slot has never
// been written at all (buffer not yet full once, from a fresh
// reader). Note this deliberately does NOT guarantee the caller gets
// clause number idx specifically -- if the writer has lapped this
// slot since the caller last looked, TryRead returns whatever clause
// (identified by its own .ID()) is there now, which is fine under the
// "lossy, best-effort, dedup on import" contract this buffer offers:
// see ClauseData.ID.
func (b *ExportBuffer) TryRead(idx uint64) (c ClauseData, ok bool) {
	ptr := b.slots[idx%b.capacity].Load()
	if ptr == nil {
		return ClauseData{}, false
	}
	return ClauseData{ID: ptr.id, Lits: ptr.lits}, true
}

// ClauseData is the read-side view of a published clause: its
// publish-order ID (for dedup/ordering by a caller that cares) and its
// literals.
type ClauseData struct {
	ID   uint64
	Lits []int32
}

// WriteIndex returns the next index Publish will use -- readers use
// this (read once, then track their own "next index to try" locally)
// to know how far ahead the writer currently is.
func (b *ExportBuffer) WriteIndex() uint64 {
	return b.writeIdx.Load()
}

// Capacity returns the ring buffer's slot count.
func (b *ExportBuffer) Capacity() uint64 {
	return b.capacity
}
