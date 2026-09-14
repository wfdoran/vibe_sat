// Package termination is a standalone spike (STAGE18.md) validating a
// shared-memory quiescence/termination-detection protocol for a fixed
// pool of work-stealing workers, in isolation from any real search
// logic, before it gets ported into go_src/internal/dfs's parallel
// depth-first search. Per STAGE18.md's explicit instruction, nothing
// in go_src or rust_src may import this package (or its Rust
// counterpart in ../rust) -- it exists purely to work out and stress
// test the tricky part on its own first.
//
// # The problem
//
// N workers cooperatively process a dynamically growing/shrinking
// pool of work items via work-stealing (see Deque): each worker owns
// one deque, pushes/pops its own end, and steals from peers' opposite
// ends when its own is empty. New work items are only ever created as
// a side effect of a worker actively processing an item it already
// has (in the real search, expanding a node produces child nodes).
// The question termination detection answers is: when has every
// worker run out of work, with no possibility of more ever appearing?
//
// Naively, "count of currently-active workers reaches zero" seems
// sufficient, since only an active worker can create new work. But a
// worker's own "am I active" transitions and the shared counter are
// two different pieces of state, and if a worker is allowed to
// decrement the counter separately for each failed peer it checks
// (rather than once, after checking every peer), there is a window
// where a steal has already removed an item from the victim's queue
// but the thief hasn't yet reflected that in the shared counter --
// during which some other worker's decrement could reach zero and
// declare termination while that stolen item is still unprocessed.
//
// # The fix this package implements
//
// A worker is considered (and counted) active for its *entire*
// sweep of every peer, not decremented after each individual failed
// steal attempt; the shared counter is decremented exactly once, only
// after a *complete* sweep of every peer has failed to find anything.
// Since a worker can only create new work while actively processing
// something it already holds, and it only decrements after
// confirming (via that complete sweep) it holds nothing and no peer
// has anything either, the shared active count reaching zero is a
// reliable signal: nothing currently held by anyone could produce
// more work from this point on. A second race remains -- the
// decrementing worker's own view of "every peer is empty" could be
// stale relative to a peer that legitimately still has (or is about
// to be found to have) something -- so the count reaching zero
// triggers one more, explicit recheck of every worker before
// confirming; if that recheck finds anything, the decrement is
// undone (the worker becomes "still idle" rather than "terminated",
// and retries) rather than declaring termination.
//
// See WorkerState for the safe per-worker wrapper enforcing "exactly
// one decrement per complete failed sweep, exactly one increment per
// transition back to having real work."
package termination

import "sync/atomic"

// Terminator tracks how many of a fixed pool of numWorkers workers
// are currently active (i.e. have not yet confirmed, via a complete
// sweep of every peer, that no work is available to them).
type Terminator struct {
	active atomic.Int64
}

// New returns a Terminator for a pool of numWorkers workers, all
// initially considered active.
func New(numWorkers int) *Terminator {
	t := &Terminator{}
	t.active.Store(int64(numWorkers))
	return t
}

// Decrement must be called by a worker only after it has completed
// one full sweep of every other worker and found nothing stealable
// (see the package doc comment for why the sweep must be complete,
// and why the worker must have remained counted active for the
// sweep's entire duration rather than decrementing speculatively
// partway through).
//
// recheck is called at most once, and only if this decrement brings
// the shared count to zero (every worker appears simultaneously
// idle). It must perform one more independent, complete check of
// every worker's deque and report whether it also finds nothing.
//
//   - If recheck reports true: Decrement returns (true, true).
//     Global termination is confirmed -- no worker will ever produce
//     more work from this point on -- and the caller must stop.
//   - If recheck reports false, or the count didn't reach zero at
//     all: Decrement returns (false, ...). The caller is not
//     terminated. stillIdle reports whether the caller's decrement
//     stands (true: genuinely idle now, contributing 0) or was
//     undone because recheck found residual work (false: the caller
//     is once again counted active, even though -- to be clear -- it
//     itself still has no work of its own; the residual work belongs
//     to some other worker, and this is just how the shared count
//     stays a safe, if occasionally briefly conservative, upper
//     bound). Either way the caller should retry its own sweep.
//
// Exactly one call to Decrement must be made per complete failed
// sweep, with no other call to Decrement from the same worker in
// between (see WorkerState, which enforces this).
func (t *Terminator) Decrement(recheck func() bool) (terminated, stillIdle bool) {
	if t.active.Add(-1) != 0 {
		return false, true
	}
	if recheck() {
		return true, true
	}
	t.active.Add(1)
	return false, false
}

// Increment must be called by a worker the instant it acquires real
// work (a successful local pop or steal) if it might currently be
// counted idle (i.e. its last sweep ended in a call to Decrement).
// See WorkerState.
func (t *Terminator) Increment() {
	t.active.Add(1)
}

// WorkerState is a safe per-worker wrapper around Terminator that
// tracks, locally (no synchronization needed -- only the owning
// worker ever touches its own WorkerState), whether this worker's
// last-known state is active or idle, so MarkActive/MarkIdle can be
// called freely (including redundantly) without ever double-counting
// a transition against the shared Terminator.
type WorkerState struct {
	term     *Terminator
	isActive bool
}

// NewWorkerState returns a WorkerState for one worker of term's pool,
// initially active (matching Terminator.New's initial count).
func NewWorkerState(term *Terminator) *WorkerState {
	return &WorkerState{term: term, isActive: true}
}

// MarkActive must be called the instant this worker acquires real
// work (a local pop success, or a successful steal mid-sweep). A
// no-op if the worker is already considered active (the common
// case -- most calls to MarkActive happen while already active, and
// must stay cheap).
func (w *WorkerState) MarkActive() {
	if w.isActive {
		return
	}
	w.isActive = true
	w.term.Increment()
}

// MarkIdle must be called once, after this worker completes one full
// sweep of every peer and finds nothing stealable (see the package
// doc comment). recheck is forwarded to Terminator.Decrement
// unchanged. Returns true if this call confirmed global termination,
// in which case the caller must stop.
//
// Safe to call redundantly (e.g. in a retry loop) if the worker is
// already marked idle from a previous sweep: since nothing new has
// been learned, this is a no-op that returns false without touching
// the shared Terminator again.
func (w *WorkerState) MarkIdle(recheck func() bool) bool {
	if !w.isActive {
		return false
	}
	terminated, stillIdle := w.term.Decrement(recheck)
	w.isActive = !stillIdle
	return terminated
}
