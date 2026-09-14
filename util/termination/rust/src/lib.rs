//! A standalone spike (STAGE18.md) validating a shared-memory
//! quiescence/termination-detection protocol for a fixed pool of
//! work-stealing workers, in isolation from any real search logic,
//! before it gets ported into `rust_src/src/dfs.rs`'s parallel
//! depth-first search. Per STAGE18.md's explicit instruction, nothing
//! in `go_src`/`rust_src` may depend on this crate (or its Go
//! counterpart in `../go`) -- it exists purely to work out and stress
//! test the tricky part on its own first.
//!
//! This mirrors `../go`'s [`Terminator`]/[`WorkerState`] design
//! exactly (see that package's doc comment for the full reasoning:
//! why "active worker count reaches zero" alone isn't sufficient, and
//! why a worker must stay counted active for an entire sweep of every
//! peer rather than decrementing speculatively partway through). The
//! one difference: this version uses `crossbeam-deque`'s real
//! Chase-Lev work-stealing deque ([`Worker`]/[`Stealer`]) instead of
//! a hand-rolled mutex-guarded one, per your go-ahead to use it in
//! the real Rust implementation. `Stealer::steal` can report
//! [`Steal::Retry`] (a lock-free steal that couldn't get a definitive
//! answer under contention) distinctly from [`Steal::Empty`]; this
//! implementation loops on `Retry` rather than treating it as "no
//! work here," which is what makes the "sweep every peer once, fully,
//! before declaring idle" step actually complete and trustworthy.

use std::sync::atomic::{AtomicI64, Ordering};

use crossbeam_deque::Steal;

/// Tracks how many of a fixed pool of `num_workers` workers are
/// currently active (i.e. have not yet confirmed, via a complete
/// sweep of every peer, that no work is available to them). See the
/// module doc comment and `../go`'s `Terminator` for the full
/// reasoning.
pub struct Terminator {
    active: AtomicI64,
}

impl Terminator {
    /// Returns a `Terminator` for a pool of `num_workers` workers,
    /// all initially considered active.
    pub fn new(num_workers: usize) -> Self {
        Terminator {
            active: AtomicI64::new(num_workers as i64),
        }
    }

    /// Must be called by a worker only after it has completed one
    /// full sweep of every other worker and found nothing stealable
    /// (see the module doc comment for why the sweep must be
    /// complete, and why the worker must have remained counted active
    /// for the sweep's entire duration rather than decrementing
    /// speculatively partway through).
    ///
    /// `recheck` is called at most once, and only if this decrement
    /// brings the shared count to zero (every worker appears
    /// simultaneously idle). It must perform one more independent,
    /// complete check of every worker's deque and report whether it
    /// also finds nothing.
    ///
    /// Returns `(terminated, still_idle)`:
    /// - `recheck` returns `true`: `(true, true)` -- global
    ///   termination confirmed; the caller must stop.
    /// - the count didn't reach zero, or `recheck` returns `false`:
    ///   `(false, still_idle)`, where `still_idle` says whether this
    ///   decrement stands (`true`) or was undone because `recheck`
    ///   found residual work belonging to some other worker
    ///   (`false` -- the caller is once again counted active, even
    ///   though it itself still has no work; this is just how the
    ///   shared count stays a safe, if occasionally briefly
    ///   conservative, upper bound). Either way the caller should
    ///   retry its own sweep.
    ///
    /// Exactly one call to `decrement` must be made per complete
    /// failed sweep, with no other call to `decrement` from the same
    /// worker in between (see [`WorkerState`], which enforces this).
    pub fn decrement(&self, recheck: impl FnOnce() -> bool) -> (bool, bool) {
        if self.active.fetch_sub(1, Ordering::SeqCst) - 1 != 0 {
            return (false, true);
        }
        if recheck() {
            return (true, true);
        }
        self.active.fetch_add(1, Ordering::SeqCst);
        (false, false)
    }

    /// Must be called by a worker the instant it acquires real work
    /// (a successful local pop or steal) if it might currently be
    /// counted idle. See [`WorkerState`].
    pub fn increment(&self) {
        self.active.fetch_add(1, Ordering::SeqCst);
    }

    /// The current active count, for tests only.
    #[cfg(test)]
    fn active_count(&self) -> i64 {
        self.active.load(Ordering::SeqCst)
    }
}

/// A safe per-worker wrapper around [`Terminator`] that tracks,
/// locally (no synchronization needed -- only the owning worker ever
/// touches its own `WorkerState`), whether this worker's last-known
/// state is active or idle, so [`Self::mark_active`]/
/// [`Self::mark_idle`] can be called freely (including redundantly)
/// without ever double-counting a transition against the shared
/// `Terminator`.
pub struct WorkerState<'a> {
    term: &'a Terminator,
    is_active: bool,
}

impl<'a> WorkerState<'a> {
    /// Returns a `WorkerState` for one worker of `term`'s pool,
    /// initially active (matching `Terminator::new`'s initial count).
    pub fn new(term: &'a Terminator) -> Self {
        WorkerState {
            term,
            is_active: true,
        }
    }

    /// Must be called the instant this worker acquires real work (a
    /// local pop success, or a successful steal mid-sweep). A no-op
    /// if the worker is already considered active (the common case).
    pub fn mark_active(&mut self) {
        if self.is_active {
            return;
        }
        self.is_active = true;
        self.term.increment();
    }

    /// Must be called once, after this worker completes one full
    /// sweep of every peer and finds nothing stealable (see the
    /// module doc comment). `recheck` is forwarded to
    /// [`Terminator::decrement`] unchanged. Returns `true` if this
    /// call confirmed global termination, in which case the caller
    /// must stop.
    ///
    /// Safe to call redundantly (e.g. in a retry loop) if the worker
    /// is already marked idle from a previous sweep: since nothing
    /// new has been learned, this is a no-op that returns `false`
    /// without touching the shared `Terminator` again.
    pub fn mark_idle(&mut self, recheck: impl FnOnce() -> bool) -> bool {
        if !self.is_active {
            return false;
        }
        let (terminated, still_idle) = self.term.decrement(recheck);
        self.is_active = !still_idle;
        terminated
    }
}

/// Steals from `stealer`, looping on [`Steal::Retry`] (a lock-free
/// steal that couldn't get a definitive answer under contention)
/// rather than treating it as "empty" -- see the module doc comment
/// for why this distinction is exactly what makes a sweep
/// trustworthy.
pub fn steal_retrying<T>(stealer: &crossbeam_deque::Stealer<T>) -> Option<T> {
    loop {
        match stealer.steal() {
            Steal::Success(item) => return Some(item),
            Steal::Empty => return None,
            Steal::Retry => continue,
        }
    }
}

pub mod simulate;

#[cfg(test)]
mod tests {
    use super::*;

    /// Exercises `Terminator` directly (no deque/threads involved)
    /// against the exact race the module doc comment describes: with
    /// two workers, the first decrement (bringing the count to 1)
    /// must never be reported as terminated; the second (bringing it
    /// to 0) must consult `recheck`, and only report termination if
    /// `recheck` says so, correctly undoing the count if not.
    #[test]
    fn test_decrement_confirms_termination_only_when_truly_quiescent() {
        let term = Terminator::new(2);

        let (terminated, still_idle) =
            term.decrement(|| panic!("recheck must not be called before the count reaches zero"));
        assert!(
            !terminated && still_idle,
            "first decrement: got ({terminated}, {still_idle}), want (false, true)"
        );

        let mut recheck_called = false;
        let (terminated, still_idle) = term.decrement(|| {
            recheck_called = true;
            false // simulate: some peer still has visible work
        });
        assert!(recheck_called, "recheck was not called at count zero");
        assert!(
            !terminated && !still_idle,
            "second decrement (recheck=false): got ({terminated}, {still_idle}), want (false, false)"
        );
        assert_eq!(
            term.active_count(),
            1,
            "active count after undone decrement"
        );

        let (terminated, still_idle) = term.decrement(|| true);
        assert!(
            terminated && still_idle,
            "third decrement (recheck=true): got ({terminated}, {still_idle}), want (true, true)"
        );
    }

    /// Verifies that calling `mark_idle` repeatedly without an
    /// intervening `mark_active` only touches the shared `Terminator`
    /// once, matching the "exactly one decrement per complete failed
    /// sweep" contract the module doc comment describes.
    #[test]
    fn test_worker_state_mark_idle_is_idempotent() {
        let term = Terminator::new(3);
        let mut w = WorkerState::new(&term);

        w.mark_idle(|| false);
        assert_eq!(term.active_count(), 2);

        for _ in 0..3 {
            let terminated = w.mark_idle(|| panic!("recheck must not be called redundantly"));
            assert!(
                !terminated,
                "redundant mark_idle must never report termination"
            );
        }
        assert_eq!(
            term.active_count(),
            2,
            "unchanged by redundant mark_idle calls"
        );

        w.mark_active();
        assert_eq!(term.active_count(), 3);
        w.mark_active();
        assert_eq!(term.active_count(), 3, "unchanged by redundant mark_active");
    }
}
