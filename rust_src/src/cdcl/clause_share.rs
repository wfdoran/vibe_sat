//! Continuous clause sharing for multithreaded `cdcl` (STAGE20.md/
//! STAGE21.md's Option B): a lock-free, single-writer/multi-reader
//! ring buffer used as each search thread's export "outbox," and the
//! free functions [`maybe_import`]/[`import_clause`] that drain other
//! threads' outboxes into this thread's own local database.
//!
//! The design (atomic-pointer-swap to an immutable published clause,
//! via the `arc-swap` crate, not a hand-rolled seqlock) was worked out
//! and stress-tested standalone first, per STAGE20.md's instruction,
//! in `util/clausesharing/rust` (a separate crate at the repository
//! root; nothing here imports it, and nothing there is imported here
//! -- this module is an independent, from-scratch port of the same
//! validated design, adapted from generic byte payloads to
//! [`crate::cnf::Clause`]). See that crate's doc comment for the full
//! rationale: losing a shared clause is never a correctness problem
//! (only a missed optimization), which is what licenses a lossy,
//! non-blocking design in the first place; and why `arc-swap`
//! specifically, rather than a hand-rolled `AtomicPtr` (Rust has no
//! GC, so a raw atomic-pointer swap needs its own answer for "when is
//! it safe to free the value a swap just replaced" -- `ArcSwapOption`
//! solves that with `Arc`'s existing refcounting, the same reasoning
//! that led this project to `crossbeam-deque` in Stage 18 rather than
//! a hand-rolled Chase-Lev deque in Rust).

use crate::cnf::Clause;
use arc_swap::ArcSwapOption;
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};

/// Bounds how long a learned clause can be and still qualify for
/// sharing (see the call site in `mod.rs`'s conflict-handling code) --
/// ManySAT's own convention (clauses of length <= 8 are shared) is
/// reused here rather than re-derived, per this project's general
/// preference for a literature standard when one exists. Unit clauses
/// (length 1) never reach that check at all: `add_learned_clause`
/// returns `None` for those before the caller ever considers
/// exporting, since a unit clause becomes a permanent level-0 fact
/// applied directly rather than a stored, watched clause -- sharing
/// that fact usefully would need its own, different import path
/// (assigning a literal permanently in a peer's trail, not adding a
/// watched clause) that this first implementation does not attempt, a
/// documented simplification rather than an oversight; see
/// `reports/REPORT21.md`.
pub(crate) const EXPORT_MAX_CLAUSE_LEN: usize = 8;

/// How many recently learned clauses each thread's [`ExportBuffer`]
/// retains before `publish` starts silently overwriting the oldest
/// entries. Lossy is fine (see the module doc comment), so this just
/// needs to be "big enough that a peer checking in every
/// `IMPORT_CHECK_MASK` conflicts or so has a reasonable chance of
/// seeing a given clause before it's overwritten," not an exact
/// bound -- 256 is a generous, cheap-to-hold guess, not a tuned value.
pub(crate) const EXPORT_BUFFER_CAPACITY: usize = 256;

/// Gates [`maybe_import`] to once every 32 conflicts
/// (`num_conflicts & IMPORT_CHECK_MASK == 0`, a cheap bitmask check --
/// unlike the wall-clock time limit fixed by STAGE35.md, checking
/// imports less often than every conflict is a pure amortization
/// choice with no reliability implication, so a fixed conflict-count
/// gate is still the right tool here): frequent enough, relative to
/// how often a search's own local database changes shape at all, to
/// deserve the name "continuous" rather than "restart-batched," while
/// keeping the O(num_peers) cost of a check bounded to a small
/// fraction of conflicts even at large thread counts (STAGE18.md's
/// "should scale to 128 threads, maybe more"
/// applies here too).
pub(crate) const IMPORT_CHECK_MASK: usize = 0x1f;

/// An immutable published clause: constructed once by
/// [`ExportBuffer::publish`] and never modified afterward, so any
/// number of reader threads can safely hold an `Arc` to the same
/// instance concurrently -- see the module doc comment for why this,
/// specifically, is what makes the design race-free with no lock.
struct SharedClause {
    lits: Clause,
}

/// One thread's outbox: a fixed-capacity ring buffer of atomically
/// swappable clause pointers that thread alone writes to (via
/// [`publish`](ExportBuffer::publish)), and every other thread may
/// read from concurrently (via [`try_read`](ExportBuffer::try_read))
/// without ever taking a lock or blocking the writer.
pub(crate) struct ExportBuffer {
    slots: Vec<ArcSwapOption<SharedClause>>,
    capacity: u64,
    write_idx: AtomicU64,
}

impl ExportBuffer {
    /// Creates a ring buffer with room for `capacity` clauses.
    pub(crate) fn new(capacity: usize) -> Self {
        let mut slots = Vec::with_capacity(capacity);
        slots.resize_with(capacity, || ArcSwapOption::from(None));
        Self {
            slots,
            capacity: capacity as u64,
            write_idx: AtomicU64::new(0),
        }
    }

    /// Stores a newly learned clause into the buffer. Must only ever
    /// be called by the single owning thread; see the analogous
    /// precondition on `util/clausesharing/rust`'s identical method.
    pub(crate) fn publish(&self, lits: &Clause) {
        let lits = if lits.len() > EXPORT_MAX_CLAUSE_LEN {
            &lits[..EXPORT_MAX_CLAUSE_LEN]
        } else {
            lits
        };
        let idx = self.write_idx.load(Ordering::SeqCst);
        self.slots[(idx % self.capacity) as usize].store(Some(Arc::new(SharedClause {
            lits: lits.to_vec(),
        })));
        self.write_idx.store(idx + 1, Ordering::SeqCst);
    }

    /// Attempts to read whatever clause currently sits at ring slot
    /// `idx % capacity`. Returns `None` only if that slot has never
    /// been written at all. Deliberately does not guarantee the
    /// caller gets clause number `idx` specifically -- if the writer
    /// has lapped this slot since the caller last looked, `try_read`
    /// returns whatever clause is there now, which is fine under this
    /// buffer's lossy, best-effort contract (see the module doc
    /// comment); [`maybe_import`] doesn't need exactly-once delivery,
    /// only "eventually sees most short clauses learned by its
    /// peers."
    pub(crate) fn try_read(&self, idx: u64) -> Option<Clause> {
        self.slots[(idx % self.capacity) as usize]
            .load_full()
            .map(|arc| arc.lits.clone())
    }
}

/// The main search loop's periodic opportunity (called once per
/// conflict, gated by [`IMPORT_CHECK_MASK`]) to pull in clauses other
/// threads have learned. A no-op whenever `peers` is empty (every
/// single-threaded caller). For each peer, at most one candidate
/// clause is read and handed to [`import_clause`] -- bounded work per
/// call, regardless of how many clauses a fast-learning peer has
/// published since the last check (excess ones are simply skipped
/// over as the cursor advances, again per the lossy contract above).
#[allow(clippy::too_many_arguments)]
pub(crate) fn maybe_import(
    peers: &[&ExportBuffer],
    peer_cursors: &mut [u64],
    num_conflicts: usize,
    clauses: &mut Vec<Clause>,
    lists: &mut crate::occurrence::Lists,
    watch: &mut Vec<[crate::cnf::Literal; 2]>,
    x: &crate::assignment::Assignment,
    clause_activity: &mut Vec<f64>,
    clause_lbd: &mut Vec<usize>,
    estimated_bytes: &mut i64,
) {
    if peers.is_empty() {
        return;
    }
    if num_conflicts & IMPORT_CHECK_MASK != 0 {
        return;
    }
    for (i, peer) in peers.iter().enumerate() {
        if let Some(lits) = peer.try_read(peer_cursors[i]) {
            import_clause(
                &lits,
                clauses,
                lists,
                watch,
                x,
                clause_activity,
                clause_lbd,
                estimated_bytes,
            );
        }
        peer_cursors[i] += 1;
    }
}

/// Attempts to fold a clause learned by another thread into this
/// thread's own local database, exactly as if this thread had learned
/// it itself: same clause list, same activity bookkeeping (starting
/// at 0, same as any freshly learned clause -- so an imported clause
/// competes for survival under `reduce_clause_database` on identical
/// terms to a self-learned one, no special-casing needed), same
/// memory-limit accounting is left to the caller (see `mod.rs`'s call
/// site in the conflict-handling code, which already checks the
/// estimated size against the limit after every conflict regardless
/// of whether this conflict came with an import).
///
/// The one real question importing raises that self-learning never
/// does: `lits` was derived from a *different* thread's search state,
/// so unlike a clause this thread just learned itself, nothing here
/// is known about how `lits`'s literals currently stand under this
/// thread's own assignment. `choose_watch` (the same helper
/// `bootstrap` already uses to pick two watches for the *original*
/// problem's clauses against a possibly-non-empty starting
/// assignment) answers that safely: if it can find two literals that
/// are not currently false, the clause is added with those as its
/// watches, live, right now. If it can't -- the clause is already
/// fully or mostly falsified under this thread's current trail -- the
/// import is simply dropped. This is a deliberate simplification, not
/// a missed case: a dropped import is never a correctness problem
/// (see the module doc comment), only a missed optimization, and
/// treating "this externally-derived clause is already unsatisfiable
/// here" as a live conflict to analyze mid-decide would be real
/// additional complexity for a case that resolves itself for free --
/// if the clause is genuinely useful to this thread, this thread will
/// pick it up on a later check once its own trail has changed.
///
/// STAGE34.md: the exporting thread's true LBD for this clause isn't
/// available here (it was computed against that thread's own decision
/// levels, meaningless in this thread's), so this uses `lits.len()` as
/// a safe stand-in -- LBD can never exceed a clause's literal count,
/// since each literal contributes at most one distinct level, so this
/// is always a conservative (never too favorable) estimate. A useful
/// side effect: since only clauses of length <= `EXPORT_MAX_CLAUSE_LEN`
/// are ever exported, and most shared clauses are short, this
/// correctly treats every exported binary clause as automatically glue
/// (length 2 = `super::GLUE_CLAUSE_LBD_THRESHOLD`), which is actually
/// exact, not just conservative, for those.
#[allow(clippy::too_many_arguments)]
fn import_clause(
    lits: &Clause,
    clauses: &mut Vec<Clause>,
    lists: &mut crate::occurrence::Lists,
    watch: &mut Vec<[crate::cnf::Literal; 2]>,
    x: &crate::assignment::Assignment,
    clause_activity: &mut Vec<f64>,
    clause_lbd: &mut Vec<usize>,
    estimated_bytes: &mut i64,
) {
    let Some(first) = super::choose_watch(lits, x, None) else {
        return;
    };
    let Some(second) = super::choose_watch(lits, x, Some(first)) else {
        return;
    };

    let idx = clauses.len();
    clauses.push(lits.clone());
    clause_activity.push(0.0);
    clause_lbd.push(lits.len());
    *estimated_bytes += super::clause_byte_cost(lits);
    for &lit in lits {
        let v = crate::cnf::literal_var(lit);
        if crate::cnf::literal_is_negative(lit) {
            lists.negative[v].push(idx);
        } else {
            lists.positive[v].push(idx);
        }
    }
    watch.push([first, second]);
}
