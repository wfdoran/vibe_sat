package dfs

import "sync/atomic"

// terminator implements shared-memory quiescence/termination
// detection (STAGE18.md) for RunParallel's fixed pool of work-stealing
// workers: it tracks how many workers are currently active (i.e. have
// not yet confirmed, via a complete sweep of every peer, that no work
// is available to them).
//
// "Active worker count reaches zero" alone is not a sufficient
// signal: a worker's own "am I active" transition and the shared
// counter are two different pieces of state, and if a worker
// decremented the counter separately for each failed peer it checked
// (rather than once, after checking every peer), there would be a
// window where a steal has already removed a node from the victim's
// deque but the thief hasn't yet reflected that in the shared
// counter -- during which some other worker's decrement could reach
// zero and declare termination while that stolen node is still
// unprocessed.
//
// The fix: a worker is considered (and counted) active for its
// *entire* sweep of every peer, not decremented after each individual
// failed steal attempt; the shared counter is decremented exactly
// once, only after a *complete* sweep of every peer has failed to
// find anything. Since a worker can only create new work (push a
// child node) while actively processing a node it already holds, and
// it only decrements after confirming (via that complete sweep) it
// holds nothing and no peer has anything either, the shared active
// count reaching zero is a reliable signal. A second race remains --
// the decrementing worker's own view of "every peer is empty" could
// be stale relative to a peer that legitimately still has something
// -- so the count reaching zero triggers one more, explicit recheck
// of every worker before confirming; if that recheck finds anything,
// the decrement is undone rather than declaring termination.
//
// This design was worked out and stress-tested standalone first, per
// STAGE18.md's instruction, in util/termination/go/terminator.go (a
// separate module nothing here imports); this is an independent,
// from-scratch reimplementation of the same validated protocol, per
// STAGE18.md's explicit requirement that go_src not depend on util.
// See workerState for the safe per-worker wrapper enforcing "exactly
// one decrement per complete failed sweep, exactly one increment per
// transition back to having real work."
type terminator struct {
	active atomic.Int64
}

// newTerminator returns a terminator for a pool of numWorkers
// workers, all initially considered active.
func newTerminator(numWorkers int) *terminator {
	t := &terminator{}
	t.active.Store(int64(numWorkers))
	return t
}

// decrement must be called by a worker only after it has completed
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
//   - If recheck reports true: decrement returns (true, true).
//     Global termination is confirmed and the caller must stop.
//   - Otherwise: decrement returns (false, stillIdle), where stillIdle
//     reports whether this decrement stands (true) or was undone
//     because recheck found residual work belonging to some other
//     worker (false -- the caller is once again counted active, even
//     though it itself still has no work; this is just how the
//     shared count stays a safe, if occasionally briefly
//     conservative, upper bound). Either way the caller should retry
//     its own sweep.
func (t *terminator) decrement(recheck func() bool) (terminated, stillIdle bool) {
	if t.active.Add(-1) != 0 {
		return false, true
	}
	if recheck() {
		return true, true
	}
	t.active.Add(1)
	return false, false
}

// increment must be called by a worker the instant it acquires real
// work (a successful local pop or steal) if it might currently be
// counted idle. See workerState.
func (t *terminator) increment() {
	t.active.Add(1)
}

// workerState is a safe per-worker wrapper around terminator that
// tracks, locally (no synchronization needed -- only the owning
// worker ever touches its own workerState), whether this worker's
// last-known state is active or idle, so markActive/markIdle can be
// called freely (including redundantly) without ever double-counting
// a transition against the shared terminator.
type workerState struct {
	term     *terminator
	isActive bool
}

// newWorkerState returns a workerState for one worker of term's pool,
// initially active (matching newTerminator's initial count).
func newWorkerState(term *terminator) *workerState {
	return &workerState{term: term, isActive: true}
}

// markActive must be called the instant this worker acquires real
// work (a local pop success, or a successful steal mid-sweep). A
// no-op if the worker is already considered active.
func (w *workerState) markActive() {
	if w.isActive {
		return
	}
	w.isActive = true
	w.term.increment()
}

// markIdle must be called once, after this worker completes one full
// sweep of every peer and finds nothing stealable. recheck is
// forwarded to terminator.decrement unchanged. Returns true if this
// call confirmed global termination, in which case the caller must
// stop.
//
// Safe to call redundantly if the worker is already marked idle from
// a previous sweep: since nothing new has been learned, this is a
// no-op that returns false without touching the shared terminator
// again.
func (w *workerState) markIdle(recheck func() bool) bool {
	if !w.isActive {
		return false
	}
	terminated, stillIdle := w.term.decrement(recheck)
	w.isActive = !stillIdle
	return terminated
}
