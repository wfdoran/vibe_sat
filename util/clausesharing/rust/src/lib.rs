//! Standalone, language-agnostic prototype for STAGE20.md's question
//! "can you implement [continuous clause sharing] with minimal
//! contention?" -- the Rust counterpart to
//! `util/clausesharing/go/ringbuffer.go`; see that file's doc comment
//! for the full design rationale (lossy sharing is safe because a
//! missed clause is never a correctness problem, only a missed
//! optimization; a hand-rolled seqlock was tried first and rejected
//! because it requires touching plain memory in a way that isn't
//! actually race-free under stricter tooling, not just Go's -- an
//! atomic pointer swap to an immutable published value is the fix).
//!
//! This is NOT imported by go_src or rust_src, per STAGE16.md/
//! STAGE18.md's `util/` authorization; it exists purely to validate
//! the design before it's (re)implemented, independently, inside
//! rust_src's own `cdcl` module.
//!
//! # Why `arc-swap`, not a hand-rolled `AtomicPtr`
//!
//! Go's version uses the standard library's `atomic.Pointer[T]`
//! directly, safely, because Go's garbage collector keeps a clause
//! alive for as long as any reader holds a loaded pointer to it --
//! there's no manual reclamation problem. Rust has no GC: a raw
//! `AtomicPtr<T>` swap needs its own answer to "when is it safe to
//! free the *old* value a swap just replaced, given some reader might
//! still be mid-read of it," which is exactly the hard, easy-to-get-
//! wrong part of building a lock-free data structure by hand (hazard
//! pointers, epoch-based reclamation, and similar). Rather than
//! reimplementing that from scratch, this uses the `arc-swap` crate:
//! `ArcSwapOption<ClauseData>` gives an atomically swappable
//! `Option<Arc<ClauseData>>` with its reclamation already solved (an
//! `Arc`'s refcount keeps a value alive for exactly as long as any
//! reader's `.load()` still holds it, the same guarantee Go's GC gives
//! for free). This is the same "Rust reaches for a well-tested crate
//! for a genuine concurrency primitive" pattern this project already
//! used for `crossbeam-deque` in Stage 18.

use arc_swap::ArcSwapOption;
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};

/// Bounds how many literals a single shared clause can carry. Real
/// clause-sharing filters (ManySAT: length <= 8) already restrict
/// what gets shared to short clauses, so this is realistic, not a
/// real limitation.
pub const MAX_CLAUSE_LEN: usize = 8;

/// The read-side view of a published clause: its publish-order ID
/// (for dedup/ordering by a caller that cares) and its literals.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ClauseData {
    pub id: u64,
    pub lits: Vec<i32>,
}

/// One thread's outbox: a fixed-capacity ring buffer of atomically
/// swappable clause pointers that thread alone writes to (via
/// `publish`), and every other thread may read from concurrently (via
/// `try_read`) without ever taking a lock or blocking the writer.
pub struct ExportBuffer {
    slots: Vec<ArcSwapOption<ClauseData>>,
    capacity: u64,
    write_idx: AtomicU64,
}

impl ExportBuffer {
    /// Creates a ring buffer with room for `capacity` clauses. Once
    /// full, `publish` silently overwrites the oldest entry -- a slow
    /// reader simply loses ground, which is fine under this buffer's
    /// lossy, best-effort contract (see module doc comment).
    pub fn new(capacity: usize) -> Self {
        let mut slots = Vec::with_capacity(capacity);
        slots.resize_with(capacity, || ArcSwapOption::from(None));
        Self {
            slots,
            capacity: capacity as u64,
            write_idx: AtomicU64::new(0),
        }
    }

    /// Stores a newly learned clause into the buffer. Must only ever
    /// be called by the single owning thread -- concurrent callers
    /// would race on `write_idx` (two writers could target the same
    /// slot simultaneously); nothing prevents a caller from doing
    /// that, so it's a documented precondition, not something
    /// enforced here (matching `ExportBuffer::Publish`'s precondition
    /// in the Go version).
    pub fn publish(&self, id: u64, lits: &[i32]) {
        let lits = if lits.len() > MAX_CLAUSE_LEN {
            &lits[..MAX_CLAUSE_LEN]
        } else {
            lits
        };
        let idx = self.write_idx.load(Ordering::SeqCst);
        let slot = &self.slots[(idx % self.capacity) as usize];
        slot.store(Some(Arc::new(ClauseData {
            id,
            lits: lits.to_vec(),
        })));
        self.write_idx.store(idx + 1, Ordering::SeqCst);
    }

    /// Attempts to read whatever clause currently sits at ring slot
    /// `idx % capacity`. Returns `None` only if that slot has never
    /// been written at all. Deliberately does not guarantee the
    /// caller gets clause number `idx` specifically -- if the writer
    /// has lapped this slot since the caller last looked, `try_read`
    /// returns whatever clause (identified by its own `.id`) is there
    /// now, which is fine under the lossy, best-effort, dedup-on-
    /// import contract this buffer offers.
    pub fn try_read(&self, idx: u64) -> Option<ClauseData> {
        let slot = &self.slots[(idx % self.capacity) as usize];
        slot.load_full().map(|arc| (*arc).clone())
    }

    /// Returns the next index `publish` will use.
    pub fn write_index(&self) -> u64 {
        self.write_idx.load(Ordering::SeqCst)
    }

    /// Returns the ring buffer's slot count.
    pub fn capacity(&self) -> u64 {
        self.capacity
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::thread;
    use std::time::{Duration, Instant};

    /// Deterministically derives a fake clause's literals from its
    /// publish ID, so a reader can independently verify a clause it
    /// received is exactly the one that was actually published under
    /// that ID -- catching any corruption rather than merely trusting
    /// the buffer. Mirrors the Go prototype's `clauseForID`.
    fn clause_for_id(id: u64) -> Vec<i32> {
        let n = (id % MAX_CLAUSE_LEN as u64) as usize + 1;
        (0..n).map(|i| id as i32 + i as i32).collect()
    }

    fn clause_matches_id(c: &ClauseData) -> bool {
        clause_for_id(c.id) == c.lits
    }

    /// Mirrors Go's `TestSingleWriterMultiReaderNoCorruption`: one
    /// writer thread continuously publishes into a small ring buffer
    /// while several reader threads continuously drain it, all
    /// lock-free. Every clause any reader observes must exactly match
    /// what was actually published under that ID.
    #[test]
    fn test_single_writer_multi_reader_no_corruption() {
        const NUM_READERS: usize = 16;
        const CAPACITY: usize = 64;
        const RUN_DURATION: Duration = Duration::from_millis(300);

        let buf = Arc::new(ExportBuffer::new(CAPACITY));
        let stop = Arc::new(std::sync::atomic::AtomicBool::new(false));
        let total_reads = Arc::new(AtomicU64::new(0));
        let corrupted = Arc::new(AtomicU64::new(0));
        let published = Arc::new(AtomicU64::new(0));

        thread::scope(|scope| {
            {
                let buf = Arc::clone(&buf);
                let stop = Arc::clone(&stop);
                let published = Arc::clone(&published);
                scope.spawn(move || {
                    let mut id: u64 = 0;
                    while !stop.load(Ordering::SeqCst) {
                        buf.publish(id, &clause_for_id(id));
                        id += 1;
                    }
                    published.store(id, Ordering::SeqCst);
                });
            }

            for _ in 0..NUM_READERS {
                let buf = Arc::clone(&buf);
                let stop = Arc::clone(&stop);
                let total_reads = Arc::clone(&total_reads);
                let corrupted = Arc::clone(&corrupted);
                scope.spawn(move || {
                    let mut idx: u64 = 0;
                    while !stop.load(Ordering::SeqCst) {
                        if let Some(c) = buf.try_read(idx) {
                            total_reads.fetch_add(1, Ordering::SeqCst);
                            if !clause_matches_id(&c) {
                                corrupted.fetch_add(1, Ordering::SeqCst);
                            }
                        }
                        idx = (idx + 1) % buf.capacity();
                    }
                });
            }

            let deadline = Instant::now() + RUN_DURATION;
            while Instant::now() < deadline {
                thread::yield_now();
            }
            stop.store(true, Ordering::SeqCst);
        });

        println!(
            "published={} total_reads={} corrupted={}",
            published.load(Ordering::SeqCst),
            total_reads.load(Ordering::SeqCst),
            corrupted.load(Ordering::SeqCst)
        );
        assert_eq!(
            corrupted.load(Ordering::SeqCst),
            0,
            "lock-free ring buffer corrupted a read"
        );
        assert!(
            published.load(Ordering::SeqCst) > 0,
            "writer never published anything"
        );
        assert!(
            total_reads.load(Ordering::SeqCst) > 0,
            "readers never observed anything"
        );
    }

    /// Mirrors Go's `TestManyIndependentBuffersManyReaders`: N worker
    /// threads, each with its own `ExportBuffer` it alone publishes
    /// to, while every other worker continuously drains everyone
    /// else's buffer -- the actual N-to-N clause-sharing topology a
    /// multithreaded CDCL design would use.
    #[test]
    fn test_many_independent_buffers_many_readers() {
        const NUM_WORKERS: usize = 32;
        const CAPACITY: usize = 32;
        const RUN_DURATION: Duration = Duration::from_millis(300);

        let buffers: Vec<Arc<ExportBuffer>> = (0..NUM_WORKERS)
            .map(|_| Arc::new(ExportBuffer::new(CAPACITY)))
            .collect();
        let stop = Arc::new(std::sync::atomic::AtomicBool::new(false));
        let total_reads = Arc::new(AtomicU64::new(0));
        let corrupted = Arc::new(AtomicU64::new(0));

        thread::scope(|scope| {
            for buffer in &buffers {
                let buf = Arc::clone(buffer);
                let stop = Arc::clone(&stop);
                scope.spawn(move || {
                    let mut id: u64 = 0;
                    while !stop.load(Ordering::SeqCst) {
                        buf.publish(id, &clause_for_id(id));
                        id += 1;
                    }
                });
            }

            for reader in 0..NUM_WORKERS {
                let buffers = buffers.clone();
                let stop = Arc::clone(&stop);
                let total_reads = Arc::clone(&total_reads);
                let corrupted = Arc::clone(&corrupted);
                scope.spawn(move || {
                    let mut cursors = vec![0u64; NUM_WORKERS];
                    while !stop.load(Ordering::SeqCst) {
                        for (peer, buf) in buffers.iter().enumerate() {
                            if peer == reader {
                                continue;
                            }
                            if let Some(c) = buf.try_read(cursors[peer]) {
                                total_reads.fetch_add(1, Ordering::SeqCst);
                                if !clause_matches_id(&c) {
                                    corrupted.fetch_add(1, Ordering::SeqCst);
                                }
                            }
                            cursors[peer] = (cursors[peer] + 1) % buf.capacity();
                        }
                    }
                });
            }

            let deadline = Instant::now() + RUN_DURATION;
            while Instant::now() < deadline {
                thread::yield_now();
            }
            stop.store(true, Ordering::SeqCst);
        });

        println!(
            "workers={} total_reads={} corrupted={}",
            NUM_WORKERS,
            total_reads.load(Ordering::SeqCst),
            corrupted.load(Ordering::SeqCst)
        );
        assert_eq!(corrupted.load(Ordering::SeqCst), 0);
        assert!(total_reads.load(Ordering::SeqCst) > 0);
    }
}
