//! The stress-test harness for [`crate::Terminator`]/[`crate::WorkerState`]:
//! a randomized tree of tasks processed entirely through work-stealing
//! over real `crossbeam-deque` deques, with an independently-checked
//! invariant (every task spawned gets processed exactly once) that a
//! termination-detection bug -- either declaring done too early
//! (losing work) or never declaring done at all (hanging) -- would
//! violate.

use std::sync::atomic::{AtomicI64, Ordering};
use std::thread;

use crossbeam_deque::{Steal, Stealer, Worker};
use rand::rngs::StdRng;
use rand::{RngExt, SeedableRng};

use crate::{Terminator, WorkerState, steal_retrying};

/// One unit of simulated work: processing it produces zero, one, or
/// two child tasks with strictly smaller budget than their parent,
/// which guarantees the whole simulated tree is finite (every task
/// eventually reaches budget 0, a forced leaf) regardless of how the
/// random branching decisions land.
#[derive(Clone, Copy)]
struct Task {
    budget: i32,
}

/// Decides how many children (0, 1, or 2) a task with the given
/// budget produces, mimicking a real search node's
/// Contra/OK/OK-both-branches outcomes. Budget <= 0 is a forced leaf;
/// budget > 0 gives every outcome real probability, weighted so the
/// tree tends to shrink (fewer than 2 children on average) and stays
/// a manageable size for a stress test repeated many times.
fn spawn_children(budget: i32, rng: &mut StdRng) -> Vec<Task> {
    if budget <= 0 {
        return Vec::new();
    }
    let child = Task { budget: budget - 1 };
    match rng.random_range(0..10) {
        0..3 => Vec::new(),      // 30%: pruned, like a Contra branch.
        3..7 => vec![child],     // 40%: one surviving branch.
        _ => vec![child, child], // 30%: both branches survive.
    }
}

/// The outcome of one [`simulate`] run, for the caller to check
/// against the ground truth it independently expects.
pub struct SimulateResult {
    /// Number of tasks actually processed.
    pub processed: i64,
    /// Number of tasks created (including the root).
    pub spawned: i64,
}

/// Runs `num_workers` concurrent workers cooperatively processing the
/// randomized tree of tasks rooted at one task of the given budget,
/// entirely through work-stealing: only worker 0 starts with
/// anything (mirroring the real search's BFS-then-seed-one-per-worker
/// setup, collapsed here to the single-seed extreme, which stresses
/// the stealing/termination logic hardest since every other worker
/// starts completely idle). Returns once every worker has confirmed
/// global termination (see [`crate::Terminator`]) and exited.
///
/// `seed` makes the random branching decisions (and steal order, see
/// `shuffled_peers`) reproducible for a given `(num_workers, budget,
/// seed)`, even though the actual interleaving of which worker
/// processes which task is still scheduler-dependent -- that's fine,
/// since what the caller checks is the aggregate invariant
/// `processed == spawned`, not any particular execution order.
pub fn simulate(num_workers: usize, budget: i32, seed: u64) -> SimulateResult {
    assert!(num_workers >= 1, "num_workers must be at least 1");

    let workers: Vec<Worker<Task>> = (0..num_workers).map(|_| Worker::new_lifo()).collect();
    let stealers: Vec<Stealer<Task>> = workers.iter().map(|w| w.stealer()).collect();

    let processed = AtomicI64::new(0);
    let spawned = AtomicI64::new(1); // the root, pushed below
    workers[0].push(Task { budget });

    let term = Terminator::new(num_workers);
    let stop = std::sync::atomic::AtomicBool::new(false);

    thread::scope(|scope| {
        for (i, worker) in workers.into_iter().enumerate() {
            let stealers = &stealers;
            let processed = &processed;
            let spawned = &spawned;
            let term = &term;
            let stop = &stop;

            scope.spawn(move || {
                let mut rng = StdRng::seed_from_u64(seed.wrapping_add(i as u64 + 1));
                let mut state = WorkerState::new(term);
                let peers = shuffled_peers(num_workers, i, &mut rng);

                loop {
                    if stop.load(Ordering::SeqCst) {
                        return;
                    }

                    if let Some(t) = worker.pop() {
                        state.mark_active();
                        processed.fetch_add(1, Ordering::SeqCst);
                        for child in spawn_children(t.budget, &mut rng) {
                            spawned.fetch_add(1, Ordering::SeqCst);
                            worker.push(child);
                        }
                        continue;
                    }

                    let mut found = false;
                    for &p in &peers {
                        if let Some(t) = steal_retrying(&stealers[p]) {
                            state.mark_active();
                            processed.fetch_add(1, Ordering::SeqCst);
                            for child in spawn_children(t.budget, &mut rng) {
                                spawned.fetch_add(1, Ordering::SeqCst);
                                worker.push(child);
                            }
                            found = true;
                            break;
                        }
                    }
                    if found {
                        continue;
                    }

                    let terminated = state
                        .mark_idle(|| stealers.iter().all(|s| matches!(s.steal(), Steal::Empty)));
                    if terminated {
                        stop.store(true, Ordering::SeqCst);
                        return;
                    }
                    if stop.load(Ordering::SeqCst) {
                        return;
                    }
                    thread::yield_now();
                }
            });
        }
    });

    SimulateResult {
        processed: processed.load(Ordering::SeqCst),
        spawned: spawned.load(Ordering::SeqCst),
    }
}

/// Returns every worker index except `self_idx`, in a random order
/// (per-call, using `rng`), so that many simultaneously-idle workers
/// sweeping for a steal don't all hammer the same victim first (which
/// would just move the contention hotspot rather than removing it).
fn shuffled_peers(num_workers: usize, self_idx: usize, rng: &mut StdRng) -> Vec<usize> {
    use rand::seq::SliceRandom;
    let mut peers: Vec<usize> = (0..num_workers).filter(|&i| i != self_idx).collect();
    peers.shuffle(rng);
    peers
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::mpsc;
    use std::time::Duration;

    /// The core stress test: across a range of worker counts
    /// (including large ones -- STAGE18.md asks this to scale to
    /// 128, maybe more, not the "16" REPORT16.md speculatively cited)
    /// and many random seeds, every run must process exactly as many
    /// tasks as were spawned (no task lost, and none double-processed),
    /// and must actually terminate (return at all) rather than hang --
    /// both are checked with a per-run timeout so a genuine
    /// deadlock/livelock fails the test loudly instead of stalling
    /// the whole suite.
    #[test]
    fn test_simulate_processes_every_task_exactly_once() {
        let worker_counts = [1usize, 2, 3, 4, 8, 16, 32, 64, 128];
        let budget = 14; // a few thousand tasks per run at this branching rate

        for &num_workers in &worker_counts {
            for seed in 0u64..20 {
                let (tx, rx) = mpsc::channel();
                thread::spawn(move || {
                    let _ = tx.send(simulate(num_workers, budget, seed));
                });

                match rx.recv_timeout(Duration::from_secs(10)) {
                    Ok(result) => assert_eq!(
                        result.processed, result.spawned,
                        "num_workers={num_workers} seed={seed}: processed={} spawned={} (lost or duplicated work)",
                        result.processed, result.spawned
                    ),
                    Err(_) => panic!(
                        "num_workers={num_workers} seed={seed}: simulate did not return within 10s (deadlock/livelock in termination detection)"
                    ),
                }
            }
        }
    }

    /// A focused edge case: with only one worker (so every
    /// sweep-of-peers is trivially empty, exercising
    /// decrement/recheck with zero peers to check), the simulation
    /// must still terminate correctly and process every task the
    /// single worker itself spawns.
    #[test]
    fn test_simulate_single_worker_always_terminates() {
        for seed in 0u64..50 {
            let result = simulate(1, 12, seed);
            assert_eq!(
                result.processed, result.spawned,
                "seed={seed}: processed={} spawned={}",
                result.processed, result.spawned
            );
        }
    }
}
