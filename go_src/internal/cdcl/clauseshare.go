// Continuous clause sharing for multithreaded cdcl (STAGE20.md/
// STAGE21.md's Option B): a lock-free, single-writer/multi-reader ring
// buffer used as each search thread's export "outbox," and the
// per-solver import logic that drains other threads' outboxes.
//
// The design (atomic-pointer-swap to an immutable published clause,
// not a hand-rolled seqlock) was worked out and stress-tested
// standalone first, per STAGE20.md's instruction, in
// util/clausesharing/go (a separate module at the repository root;
// nothing here imports it, and nothing there is imported here -- this
// file is an independent, from-scratch port of the same validated
// design, adapted from generic byte payloads to cnf.Clause). See that
// package's doc comment for the full rationale: losing a shared
// clause is never a correctness problem (only a missed optimization),
// which is what licenses a lossy, non-blocking design in the first
// place; and why the first version tried (a classic seqlock) was
// rejected once go test -race correctly flagged its torn-read case as
// a genuine data race, in favor of the atomic-pointer-swap version
// used here.
package cdcl

import (
	"sync/atomic"

	"vibe_sat/internal/cnf"
)

// exportMaxClauseLen bounds how long a learned clause can be and still
// qualify for sharing (see addLearnedClause) -- ManySAT's own
// convention (clauses of length <= 8 are shared) is reused here rather
// than re-derived, per this project's general preference for a
// literature standard when one exists. Unit clauses (length 1) never
// reach this check at all: addLearnedClause returns before it for
// those, since a unit clause becomes a permanent level-0 fact applied
// directly rather than a stored, watched clause (see its doc
// comment), and sharing that fact usefully would need its own,
// different import path (assigning a literal permanently in a peer's
// trail, not adding a watched clause) that this first implementation
// does not attempt -- a documented simplification, not an oversight;
// see reports/REPORT21.md.
const exportMaxClauseLen = 8

// exportBufferCapacity is how many recently learned clauses each
// thread's exportBuffer retains before Publish starts silently
// overwriting the oldest entries. Lossy is fine (see the package doc
// comment), so this just needs to be "big enough that a peer checking
// in every importCheckMask conflicts or so has a reasonable chance of
// seeing a given clause before it's overwritten," not an exact
// bound -- 256 is a generous, cheap-to-hold guess, not a tuned value.
const exportBufferCapacity = 256

// importCheckMask gates maybeImport to once every 32 conflicts
// (s.numConflicts & importCheckMask == 0, a cheap bitmask check --
// unlike the wall-clock time limit fixed by STAGE35.md, checking
// imports less often than every conflict is a pure amortization
// choice with no reliability implication, so a fixed conflict-count
// gate is still the right tool here): frequent enough, relative to
// how often a search's own local database changes shape at all, to
// deserve the name "continuous" rather than "restart-batched," while
// keeping the O(numPeers) cost of a check bounded to a small fraction
// of conflicts even at large thread counts (STAGE18.md's "should
// scale to 128 threads, maybe more" applies here too).
const importCheckMask = 0x1f

// sharedClause is an immutable published clause: constructed once by
// exportBuffer.Publish and never modified afterward, so any number of
// reader threads can safely hold a pointer to the same instance
// concurrently -- see the package doc comment for why this,
// specifically, is what makes the design race-free without any lock.
type sharedClause struct {
	lits cnf.Clause
}

// exportBuffer is one thread's outbox: a fixed-capacity ring buffer of
// atomic clause pointers that thread alone writes to (via Publish),
// and every other thread may read from concurrently (via TryRead)
// without ever taking a lock or blocking the writer.
type exportBuffer struct {
	slots    []atomic.Pointer[sharedClause]
	capacity uint64
	writeIdx atomic.Uint64
}

// newExportBuffer creates a ring buffer with room for capacity
// clauses.
func newExportBuffer(capacity int) *exportBuffer {
	return &exportBuffer{slots: make([]atomic.Pointer[sharedClause], capacity), capacity: uint64(capacity)}
}

// publish stores a newly learned clause into the buffer. Must only
// ever be called by the single owning thread; see the analogous
// precondition on util/clausesharing/go's identical method.
func (b *exportBuffer) publish(lits cnf.Clause) {
	owned := append(cnf.Clause(nil), lits...) // sharedClause must own its data; never alias the caller's slice
	idx := b.writeIdx.Load()
	b.slots[idx%b.capacity].Store(&sharedClause{lits: owned})
	b.writeIdx.Store(idx + 1)
}

// tryRead attempts to read whatever clause currently sits at ring
// slot idx%capacity. Returns ok=false only if that slot has never
// been written at all. Deliberately does not guarantee the caller
// gets clause number idx specifically -- if the writer has lapped
// this slot since the caller last looked, tryRead returns whatever
// clause is there now, which is fine under this buffer's lossy,
// best-effort contract (see the package doc comment); the caller
// (maybeImport) doesn't need exactly-once delivery, only "eventually
// sees most short clauses learned by its peers."
func (b *exportBuffer) tryRead(idx uint64) (cnf.Clause, bool) {
	ptr := b.slots[idx%b.capacity].Load()
	if ptr == nil {
		return nil, false
	}
	return ptr.lits, true
}

// maybeImport is runLoop's periodic opportunity (called once per
// conflict from learnAndBackjump, gated by importCheckMask) to pull in
// clauses other threads have learned. A no-op for every single-
// threaded solver (s.peers == nil). For each peer, at most one
// candidate clause is read and handed to importClause -- bounded work
// per call, regardless of how many clauses a fast-learning peer has
// published since the last check (excess ones are simply skipped over
// as the cursor advances, again per the lossy contract above).
func (s *solver) maybeImport() {
	if s.peers == nil {
		return
	}
	if s.numConflicts&importCheckMask != 0 {
		return
	}
	for i, peer := range s.peers {
		if lits, ok := peer.tryRead(s.peerCursors[i]); ok {
			s.importClause(lits)
		}
		s.peerCursors[i]++
	}
}

// importClause attempts to fold a clause learned by another thread
// into this thread's own local database, exactly as if this thread
// had learned it itself: same clause list, same activity bookkeeping
// (starting at 0, same as any freshly learned clause -- so an
// imported clause competes for survival under reduceClauseDatabase on
// identical terms to a self-learned one, no special-casing needed),
// same memory-limit accounting.
//
// STAGE34.md: the exporting thread's true LBD for this clause isn't
// available here (it was computed against that thread's own decision
// levels, meaningless in this thread's), so this uses len(clause) as
// a safe stand-in -- LBD can never exceed a clause's literal count,
// since each literal contributes at most one distinct level, so this
// is always a conservative (never too favorable) estimate. A useful
// side effect: since only clauses of length <= exportMaxClauseLen are
// ever exported, and exportMaxClauseLen's own doc comment notes most
// shared clauses are short, this correctly treats every exported
// binary clause as automatically glue (length 2 = glueClauseLBDThreshold),
// which is actually exact, not just conservative, for those.
//
// The one real question importing raises that self-learning never
// does: lits was derived from a *different* thread's search state, so
// unlike a clause this thread just learned itself (whose asserting
// literal addLearnedClause's caller already knows is unassigned),
// nothing here is known about how lits's literals currently stand
// under this thread's own assignment. chooseWatch (the same helper
// newSolver's bootstrap already uses to pick two watches for the
// *original* problem's clauses against a possibly-non-empty starting
// assignment) answers that safely: if it can find two literals that
// are not currently false, the clause is added with those as its
// watches, live, right now. If it can't -- the clause is already
// fully or mostly falsified under this thread's current trail -- the
// import is simply dropped. This is a deliberate simplification, not
// a missed case: a dropped import is never a correctness problem
// (see the package doc comment), only a missed optimization, and
// treating "this externally-derived clause is already unsatisfiable
// here" as a live conflict to analyze mid-decide would be real
// additional complexity for a case that resolves itself for free --
// if the clause is genuinely useful to this thread, its exporter (or
// some other thread) is likely to keep it available, and this thread
// will pick it up on a later check once its own trail has changed.
func (s *solver) importClause(lits cnf.Clause) {
	first, foundFirst := chooseWatch(lits, s.x, 0)
	if !foundFirst {
		return
	}
	second, foundSecond := chooseWatch(lits, s.x, first)
	if !foundSecond {
		return
	}

	clause := append(cnf.Clause(nil), lits...) // own copy; must not alias the exporter's published slice
	idx := len(s.clauses)
	s.clauses = append(s.clauses, clause)
	s.clauseActivity = append(s.clauseActivity, 0.0)
	s.clauseLBD = append(s.clauseLBD, len(clause))
	s.estimatedBytes += clauseByteCost(clause)
	for _, lit := range clause {
		v := lit.Var()
		if lit.IsNegative() {
			s.lists.Negative[v] = append(s.lists.Negative[v], idx)
		} else {
			s.lists.Positive[v] = append(s.lists.Positive[v], idx)
		}
	}
	s.watch = append(s.watch, [2]cnf.Literal{first, second})
	*s.watchersFor(first) = append(*s.watchersFor(first), idx)
	*s.watchersFor(second) = append(*s.watchersFor(second), idx)

	if s.memoryLimitBytes != nil && s.estimatedBytes > *s.memoryLimitBytes {
		s.reduceClauseDatabase()
	}
}
