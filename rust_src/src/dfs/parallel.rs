//! Parallel depth-first search (STAGE18.md): a genuine divide-and-
//! conquer parallel search, not a portfolio solver -- every worker
//! explores a disjoint region of the same search tree that
//! [`super::run`] alone would have explored, redistributing work
//! between workers via stealing rather than each independently
//! searching the whole tree.
//!
//! The design (and the tricky termination-detection part
//! specifically) was worked out and stress-tested standalone first,
//! per STAGE18.md's instruction, in `util/termination/rust` (a
//! separate crate at the repository root; nothing here depends on
//! it, and nothing there depends on this crate -- this module is an
//! independent, from-scratch port of the same validated design,
//! adapted from an abstract task simulation to real search nodes,
//! and using the real `crossbeam-deque` crate for the work-stealing
//! deque, per your go-ahead to use it here).
//!
//! Step 1 ([`bfs_seed`]) does a breadth-first expansion of the tree
//! (pop the front, branch, push survivors to the back, rather than
//! DFS's LIFO stack) until either it has collected `num_threads`
//! nodes -- one seed per worker, at roughly the same depth, so each
//! starts with a roughly similarly-sized disjoint subtree -- or the
//! tree resolves on its own first (every branch pruned: proven
//! UNSAT; some branch completed: SAT), in which case that answer is
//! returned directly without ever spawning a worker (STAGE18.md:
//! "the vast majority of the time it returns UNKNOWN and you carry
//! on ... but with odd cases which return SAT or UNSAT, exit with
//! that value"). If the tree simply has fewer than `num_threads`
//! leaves to hand out, fewer workers than requested are spawned --
//! one per seed actually produced.
//!
//! Step 2: each worker owns one [`crossbeam_deque::Worker`] deque
//! (LIFO for the owner's own push/pop, matching how `run`'s own
//! stack works), preloaded with one seed, and explores it exactly
//! like `run`'s own stack -- pop, branch, push survivors -- except
//! stealing from a peer's [`crossbeam_deque::Stealer`] (which always
//! removes from the *opposite* end -- see [`steal_retrying`]) when
//! its own empties, and participating in shared quiescence detection
//! (see [`Terminator`]) when a full sweep of every peer comes up
//! empty too. The moment any worker completes an assignment, it
//! signals every other worker to stop (the same single-winner
//! compare-and-swap protocol Stage 17's parallel hillclimb/walksat
//! use) and that worker's result -- and only that worker's -- is
//! reported.

use std::sync::atomic::{AtomicBool, AtomicI32, AtomicI64, Ordering};
use std::thread;
use std::time::{Duration, Instant};

use crossbeam_deque::{Steal, Stealer, Worker};
use rand::rngs::StdRng;
use rand::{Rng, RngExt, SeedableRng};

use super::{
    SearchNode, SelectVarVariant, SolveResult, Status, TIME_CHECK_INTERVAL, all_assigned, bcp,
    bootstrap, describe_params, select_var, select_var_fast_pick,
};
use crate::assignment::{self, Value};
use crate::cnf::{Clause, Problem};
use crate::occurrence::Lists;

/// What [`bfs_seed`] concluded.
enum BfsResult {
    /// The tree was not resolved during seeding; the `Vec<SearchNode>`
    /// holds between 1 and `num_threads` nodes to hand out to
    /// workers, and the `usize` is the seeding phase's own node
    /// count.
    Unknown(Vec<SearchNode>, usize),
    /// Some branch completed a satisfying assignment during seeding
    /// itself.
    Sat(Box<SolveResult>),
    /// Every branch was pruned during seeding, proving the whole
    /// problem unsatisfiable before any worker was ever spawned. The
    /// `usize` is the seeding phase's own node count.
    Unsat(usize),
    /// The overall time limit was exceeded during seeding itself. The
    /// `usize` is the seeding phase's own node count.
    TimedOut(usize),
}

/// Performs the breadth-first seeding phase described in the module
/// doc comment, starting from `root`. `clauses`/`lists`/
/// `working_problem`/`variant`/`rng` are exactly what [`super::run`]'s
/// own loop already needs for the same purpose (branch selection and
/// BCP); `time_limit`/`start_time` let it respect the overall
/// deadline during seeding too, since a pathological formula could in
/// principle take a while to even reach `num_threads` seeds.
#[allow(clippy::too_many_arguments)]
fn bfs_seed<R: Rng>(
    clauses: &[Clause],
    lists: &Lists,
    working_problem: &Problem,
    root: SearchNode,
    num_threads: usize,
    variant: SelectVarVariant,
    rng: &mut R,
    time_limit: Option<Duration>,
    start_time: Instant,
) -> BfsResult {
    if all_assigned(&root.assignment) {
        // Bootstrap's own unit propagation alone already fully solved
        // the formula; see all_assigned's doc comment for why this
        // check only ever needs to happen for the root (every other
        // node this function enqueues below is only ever pushed
        // after bcp reports Status::Ok, which by construction means
        // it still has an unassigned variable).
        return BfsResult::Sat(Box::new(SolveResult {
            satisfiable: true,
            assignment: root.assignment,
            num_nodes: 0,
            timed_out: false,
        }));
    }

    let mut queue: Vec<SearchNode> = vec![root];
    let mut num_nodes = 0usize;

    while !queue.is_empty() && queue.len() < num_threads {
        num_nodes += 1;
        if let Some(limit) = time_limit
            && num_nodes & TIME_CHECK_INTERVAL == 0
            && start_time.elapsed() >= limit
        {
            return BfsResult::TimedOut(num_nodes);
        }

        let node = queue.remove(0);

        let i = match variant {
            SelectVarVariant::Fast => select_var_fast_pick(working_problem, &node.assignment),
            SelectVarVariant::Weighted => select_var(working_problem, &node.assignment, rng),
        };

        for &v in &[Value::False, Value::True] {
            let mut branch_assignment = node.assignment.clone();
            branch_assignment[i] = v;
            let mut branch_watch = node.watch.clone();

            match bcp(clauses, lists, &mut branch_watch, &mut branch_assignment, i) {
                Status::Contra => continue,
                Status::Done => {
                    return BfsResult::Sat(Box::new(SolveResult {
                        satisfiable: true,
                        assignment: branch_assignment,
                        num_nodes,
                        timed_out: false,
                    }));
                }
                Status::Ok => queue.push(SearchNode {
                    assignment: branch_assignment,
                    watch: branch_watch,
                }),
            }
        }
    }

    if queue.is_empty() {
        BfsResult::Unsat(num_nodes)
    } else {
        BfsResult::Unknown(queue, num_nodes)
    }
}

/// Runs the same search as [`super::run`], split across `num_threads`
/// concurrent workers (STAGE18.md). `num_threads <= 1` delegates
/// straight to [`super::run`], with `rng` used exactly as it always
/// has been -- so behavior (including every random choice made) is
/// bit-for-bit identical to calling [`super::run`] directly whenever
/// multithreading isn't actually in use, matching the same guarantee
/// Stage 17 established for the parallel hillclimb/walksat entry
/// points.
///
/// See the module doc comment for the two-step design (`bfs_seed`,
/// then per-worker stealing/termination-detection). `rng` is used,
/// before any worker starts, to derive one independent sub-generator
/// per worker actually spawned, exactly as Stage 17's parallel
/// hillclimb/walksat do, so the overall result is fully reproducible
/// given `(rng`'s state, `num_threads)` even though which worker's
/// answer wins a race to a solution is not. `SolveResult::num_nodes`
/// is the seeding phase's own node count plus the sum of every
/// spawned worker's own count, win or lose -- the total search effort
/// expended, matching Stage 17's convention for `starts`.
pub fn run_parallel<R: Rng>(
    problem: &Problem,
    lists: &Lists,
    time_limit: Option<Duration>,
    variant: SelectVarVariant,
    num_threads: usize,
    rng: &mut R,
    verbose: i32,
) -> SolveResult {
    if num_threads <= 1 {
        return super::run(problem, lists, time_limit, variant, rng, verbose);
    }

    if verbose >= 1 {
        println!(
            "dfs: num_threads={num_threads} {}",
            describe_params(time_limit, variant)
        );
    }

    if problem.num_vars == 0 {
        let satisfiable = problem.clauses.is_empty();
        if verbose >= 1 {
            println!("{}", if satisfiable { "SAT" } else { "UNSAT" });
        }
        return SolveResult {
            satisfiable,
            assignment: assignment::new(0),
            num_nodes: 0,
            timed_out: false,
        };
    }

    let Some((clauses, root)) = bootstrap(problem) else {
        if verbose >= 1 {
            println!("UNSAT");
        }
        return SolveResult {
            satisfiable: false,
            assignment: assignment::new(problem.num_vars),
            num_nodes: 0,
            timed_out: false,
        };
    };
    let working_problem = Problem {
        num_vars: problem.num_vars,
        clauses,
    };

    let start_time = Instant::now();
    let seeding = bfs_seed(
        &working_problem.clauses,
        lists,
        &working_problem,
        root,
        num_threads,
        variant,
        rng,
        time_limit,
        start_time,
    );

    let seeds = match seeding {
        BfsResult::Sat(result) => {
            if verbose >= 1 {
                println!("SAT");
            }
            return *result;
        }
        BfsResult::Unsat(num_nodes) => {
            if verbose >= 1 {
                println!("UNSAT");
            }
            return SolveResult {
                satisfiable: false,
                assignment: assignment::new(problem.num_vars),
                num_nodes,
                timed_out: false,
            };
        }
        BfsResult::TimedOut(num_nodes) => {
            if verbose >= 1 {
                println!("UNKNOWN");
            }
            return SolveResult {
                satisfiable: false,
                assignment: assignment::new(problem.num_vars),
                num_nodes,
                timed_out: true,
            };
        }
        BfsResult::Unknown(seeds, num_nodes) => (seeds, num_nodes),
    };
    let (seeds, seeding_num_nodes) = seeds;

    // Genuinely parallelize across seeds.len() workers, which may be
    // fewer than num_threads if the tree had fewer than num_threads
    // leaves to hand out (see the module doc comment).
    let num_workers = seeds.len();
    let workers: Vec<Worker<SearchNode>> = (0..num_workers).map(|_| Worker::new_lifo()).collect();
    let stealers: Vec<Stealer<SearchNode>> = workers.iter().map(|w| w.stealer()).collect();
    for (worker, seed) in workers.iter().zip(seeds) {
        worker.push(seed);
    }

    let mut sub_rngs: Vec<StdRng> = (0..num_workers)
        .map(|_| StdRng::seed_from_u64(rng.random::<u64>()))
        .collect();

    let term = Terminator::new(num_workers);
    let stop = AtomicBool::new(false);
    let timed_out = AtomicBool::new(false);
    let winner = AtomicI32::new(-1);

    let mut results: Vec<SolveResult> = Vec::new();
    thread::scope(|scope| {
        let handles: Vec<_> = workers
            .into_iter()
            .zip(sub_rngs.iter_mut())
            .enumerate()
            .map(|(i, (worker, sub_rng))| {
                let stealers = &stealers;
                let clauses = &working_problem.clauses;
                let working_problem = &working_problem;
                let term = &term;
                let stop = &stop;
                let timed_out = &timed_out;
                let winner = &winner;

                scope.spawn(move || {
                    // The winner CAS must happen here, inside this
                    // worker's own thread, immediately after
                    // dfs_worker returns -- not deferred to after
                    // every thread has already been joined -- so
                    // that a still-running peer actually observes
                    // stop/winner promptly (via its own periodic
                    // stop.load check) instead of only learning about
                    // it once it has already finished on its own.
                    // This mirrors Stage 17's identical goroutine
                    // closure structure for parallel hillclimb/
                    // walksat exactly.
                    let result = dfs_worker(DfsWorkerConfig {
                        self_idx: i,
                        worker,
                        stealers,
                        clauses,
                        lists,
                        working_problem,
                        variant,
                        rng: sub_rng,
                        term,
                        stop,
                        timed_out,
                        time_limit,
                        start_time,
                    });
                    if result.satisfiable
                        && stop
                            .compare_exchange(false, true, Ordering::SeqCst, Ordering::SeqCst)
                            .is_ok()
                    {
                        // This worker is the one whose CAS actually
                        // flipped stop from false to true -- the
                        // single, unambiguous winner, exactly as
                        // Stage 17's parallel hillclimb/walksat pick
                        // theirs. Every other worker, even one that
                        // also finds a satisfying assignment at
                        // nearly the same moment, loses this race and
                        // is simply not recorded here.
                        winner.store(i as i32, Ordering::SeqCst);
                    }
                    result
                })
            })
            .collect();

        for handle in handles {
            results.push(handle.join().expect("dfs worker thread panicked"));
        }
    });

    let total_nodes: usize = seeding_num_nodes + results.iter().map(|r| r.num_nodes).sum::<usize>();

    let winner_idx = winner.load(Ordering::SeqCst);
    if winner_idx >= 0 {
        let winning = &results[winner_idx as usize];
        if verbose >= 1 {
            println!("SAT");
        }
        return SolveResult {
            satisfiable: true,
            assignment: winning.assignment.clone(),
            num_nodes: total_nodes,
            timed_out: false,
        };
    }

    if timed_out.load(Ordering::SeqCst) {
        if verbose >= 1 {
            println!("UNKNOWN");
        }
        return SolveResult {
            satisfiable: false,
            assignment: assignment::new(problem.num_vars),
            num_nodes: total_nodes,
            timed_out: true,
        };
    }

    if verbose >= 1 {
        println!("UNSAT");
    }
    SolveResult {
        satisfiable: false,
        assignment: assignment::new(problem.num_vars),
        num_nodes: total_nodes,
        timed_out: false,
    }
}

/// One worker's fixed inputs, bundled since there are enough of them
/// (and enough shared cross-worker state) that a plain parameter list
/// would be unwieldy.
struct DfsWorkerConfig<'a> {
    self_idx: usize,
    worker: Worker<SearchNode>,
    stealers: &'a [Stealer<SearchNode>],
    clauses: &'a [Clause],
    lists: &'a Lists,
    working_problem: &'a Problem,
    variant: SelectVarVariant,
    rng: &'a mut StdRng,
    term: &'a Terminator,
    stop: &'a AtomicBool,
    timed_out: &'a AtomicBool,
    time_limit: Option<Duration>,
    start_time: Instant,
}

/// One worker's main loop: repeatedly take a node (its own deque
/// first, then stealing from a peer), branch it exactly like `run`'s
/// own loop does, and push any surviving children onto its own
/// deque -- until it either completes a satisfying assignment, the
/// shared deadline passes, another worker signals stop (found a
/// solution, or the search timed out), or every worker's deque is
/// confirmed empty (see [`Terminator`]), which proves the whole
/// problem unsatisfiable.
fn dfs_worker(cfg: DfsWorkerConfig) -> SolveResult {
    let mut state = WorkerState::new(cfg.term);
    let peers = shuffled_peers(cfg.stealers.len(), cfg.self_idx, cfg.rng);
    let mut num_nodes = 0usize;
    let mut step = 0usize;

    let empty_result = |num_nodes: usize, timed_out: bool| SolveResult {
        satisfiable: false,
        assignment: assignment::new(cfg.working_problem.num_vars),
        num_nodes,
        timed_out,
    };

    loop {
        if cfg.stop.load(Ordering::SeqCst) {
            return empty_result(num_nodes, false);
        }
        step += 1;
        if let Some(limit) = cfg.time_limit
            && step & TIME_CHECK_INTERVAL == 0
            && cfg.start_time.elapsed() >= limit
        {
            cfg.timed_out.store(true, Ordering::SeqCst);
            cfg.stop.store(true, Ordering::SeqCst);
            return empty_result(num_nodes, true);
        }

        let node = match cfg.worker.pop() {
            Some(node) => node,
            None => match steal_from_peers(cfg.stealers, &peers) {
                Some(node) => node,
                None => {
                    let terminated = state.mark_idle(|| {
                        cfg.stealers
                            .iter()
                            .all(|s| matches!(s.steal(), Steal::Empty))
                    });
                    if terminated {
                        cfg.stop.store(true, Ordering::SeqCst);
                        return empty_result(num_nodes, false);
                    }
                    if cfg.stop.load(Ordering::SeqCst) {
                        return empty_result(num_nodes, false);
                    }
                    thread::yield_now();
                    continue;
                }
            },
        };

        state.mark_active();
        num_nodes += 1;

        let i = match cfg.variant {
            SelectVarVariant::Fast => select_var_fast_pick(cfg.working_problem, &node.assignment),
            SelectVarVariant::Weighted => {
                select_var(cfg.working_problem, &node.assignment, cfg.rng)
            }
        };

        for &v in &[Value::False, Value::True] {
            let mut branch_assignment = node.assignment.clone();
            branch_assignment[i] = v;
            let mut branch_watch = node.watch.clone();

            match bcp(
                cfg.clauses,
                cfg.lists,
                &mut branch_watch,
                &mut branch_assignment,
                i,
            ) {
                Status::Contra => continue,
                Status::Done => {
                    return SolveResult {
                        satisfiable: true,
                        assignment: branch_assignment,
                        num_nodes,
                        timed_out: false,
                    };
                }
                Status::Ok => cfg.worker.push(SearchNode {
                    assignment: branch_assignment,
                    watch: branch_watch,
                }),
            }
        }
    }
}

/// Tries, in order, to steal one node from each of `peers`' stealers,
/// returning the first success. Loops on [`Steal::Retry`] (a
/// lock-free steal that couldn't get a definitive answer under
/// contention) rather than treating it as "empty" for that one peer.
fn steal_from_peers(stealers: &[Stealer<SearchNode>], peers: &[usize]) -> Option<SearchNode> {
    for &p in peers {
        if let Some(node) = steal_retrying(&stealers[p]) {
            return Some(node);
        }
    }
    None
}

/// Steals from `stealer`, looping on [`Steal::Retry`] rather than
/// treating it as "empty" -- see the module doc comment for why this
/// distinction is what makes a full sweep of every peer trustworthy.
fn steal_retrying<T>(stealer: &Stealer<T>) -> Option<T> {
    loop {
        match stealer.steal() {
            Steal::Success(item) => return Some(item),
            Steal::Empty => return None,
            Steal::Retry => continue,
        }
    }
}

/// Returns every worker index except `self_idx`, in a random order
/// (fixed once per worker, using `rng`) so that many
/// simultaneously-idle workers sweeping for a steal don't all hammer
/// the same victim first.
fn shuffled_peers(num_workers: usize, self_idx: usize, rng: &mut StdRng) -> Vec<usize> {
    use rand::seq::SliceRandom;
    let mut peers: Vec<usize> = (0..num_workers).filter(|&i| i != self_idx).collect();
    peers.shuffle(rng);
    peers
}

/// Tracks how many of a fixed pool of `num_workers` workers are
/// currently active (i.e. have not yet confirmed, via a complete
/// sweep of every peer, that no work is available to them).
///
/// "Active worker count reaches zero" alone is not a sufficient
/// signal: a worker's own "am I active" transition and the shared
/// counter are two different pieces of state, and if a worker
/// decremented the counter separately for each failed peer it
/// checked (rather than once, after checking every peer), there
/// would be a window where a steal has already removed a node from
/// the victim's deque but the thief hasn't yet reflected that in the
/// shared counter -- during which some other worker's decrement
/// could reach zero and declare termination while that stolen node
/// is still unprocessed.
///
/// The fix: a worker is considered (and counted) active for its
/// *entire* sweep of every peer, not decremented after each
/// individual failed steal attempt; the shared counter is decremented
/// exactly once, only after a *complete* sweep of every peer has
/// failed to find anything. Since a worker can only create new work
/// (push a child node) while actively processing a node it already
/// holds, and it only decrements after confirming (via that complete
/// sweep) it holds nothing and no peer has anything either, the
/// shared active count reaching zero is a reliable signal. A second
/// race remains -- the decrementing worker's own view of "every peer
/// is empty" could be stale relative to a peer that legitimately
/// still has something -- so the count reaching zero triggers one
/// more, explicit recheck of every worker before confirming; if that
/// recheck finds anything, the decrement is undone rather than
/// declaring termination.
///
/// This design was worked out and stress-tested standalone first, per
/// STAGE18.md's instruction, in `util/termination/rust` (a separate
/// crate nothing here depends on); this is an independent,
/// from-scratch reimplementation of the same validated protocol. See
/// [`WorkerState`] for the safe per-worker wrapper enforcing "exactly
/// one decrement per complete failed sweep, exactly one increment per
/// transition back to having real work."
struct Terminator {
    active: AtomicI64,
}

impl Terminator {
    fn new(num_workers: usize) -> Self {
        Terminator {
            active: AtomicI64::new(num_workers as i64),
        }
    }

    /// Must be called by a worker only after it has completed one
    /// full sweep of every other worker and found nothing stealable
    /// (see the struct doc comment for why the sweep must be
    /// complete, and why the worker must have remained counted active
    /// for the sweep's entire duration rather than decrementing
    /// speculatively partway through).
    ///
    /// `recheck` is called at most once, and only if this decrement
    /// brings the shared count to zero. Returns `(terminated,
    /// still_idle)`: see [`WorkerState::mark_idle`] for the full
    /// contract.
    fn decrement(&self, recheck: impl FnOnce() -> bool) -> (bool, bool) {
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
    /// if it might currently be counted idle. See [`WorkerState`].
    fn increment(&self) {
        self.active.fetch_add(1, Ordering::SeqCst);
    }
}

/// A safe per-worker wrapper around [`Terminator`] that tracks,
/// locally (no synchronization needed -- only the owning worker ever
/// touches its own `WorkerState`), whether this worker's last-known
/// state is active or idle, so [`Self::mark_active`]/
/// [`Self::mark_idle`] can be called freely (including redundantly)
/// without ever double-counting a transition against the shared
/// `Terminator`.
struct WorkerState<'a> {
    term: &'a Terminator,
    is_active: bool,
}

impl<'a> WorkerState<'a> {
    fn new(term: &'a Terminator) -> Self {
        WorkerState {
            term,
            is_active: true,
        }
    }

    /// Must be called the instant this worker acquires real work (a
    /// local pop success, or a successful steal mid-sweep). A no-op
    /// if the worker is already considered active.
    fn mark_active(&mut self) {
        if self.is_active {
            return;
        }
        self.is_active = true;
        self.term.increment();
    }

    /// Must be called once, after this worker completes one full
    /// sweep of every peer and finds nothing stealable. `recheck` is
    /// forwarded to [`Terminator::decrement`] unchanged. Returns
    /// `true` if this call confirmed global termination, in which
    /// case the caller must stop.
    ///
    /// Safe to call redundantly if the worker is already marked idle
    /// from a previous sweep: since nothing new has been learned,
    /// this is a no-op that returns `false` without touching the
    /// shared `Terminator` again.
    fn mark_idle(&mut self, recheck: impl FnOnce() -> bool) -> bool {
        if !self.is_active {
            return false;
        }
        let (terminated, still_idle) = self.term.decrement(recheck);
        self.is_active = !still_idle;
        terminated
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::assignment;
    use rand::SeedableRng;
    use rand::rngs::StdRng;

    /// Builds the standard CNF encoding of "num_pigeons pigeons
    /// cannot be placed into num_holes holes with no two pigeons
    /// sharing a hole", which is unsatisfiable whenever num_pigeons >
    /// num_holes. Variable (p-1)*num_holes+h represents "pigeon p is
    /// in hole h". Duplicated from `super::tests`'s identical helper
    /// (a different, sibling test module, so not directly reusable)
    /// rather than restructuring existing code to share it.
    fn pigeonhole_problem(num_pigeons: usize, num_holes: usize) -> Problem {
        let v = |p: usize, h: usize| -> crate::cnf::Literal {
            ((p - 1) * num_holes + h) as crate::cnf::Literal
        };

        let mut clauses = Vec::new();
        for p in 1..=num_pigeons {
            clauses.push((1..=num_holes).map(|h| v(p, h)).collect());
        }
        for h in 1..=num_holes {
            for p1 in 1..=num_pigeons {
                for p2 in (p1 + 1)..=num_pigeons {
                    clauses.push(vec![-v(p1, h), -v(p2, h)]);
                }
            }
        }

        Problem {
            num_vars: num_pigeons * num_holes,
            clauses,
        }
    }

    /// Verifies STAGE18.md's core compatibility guarantee (the same
    /// one Stage 17 established for the parallel hillclimb/walksat
    /// entry points): `run_parallel` with `num_threads <= 1` must
    /// behave identically to calling `run` directly, since it
    /// delegates straight to `run` rather than going through any of
    /// the seeding/worker/stealing machinery at all.
    #[test]
    fn test_run_parallel_with_one_thread_matches_run() {
        let problem = pigeonhole_problem(4, 3);
        let lists = crate::occurrence::build(&problem);

        let want = super::super::run(
            &problem,
            &lists,
            None,
            SelectVarVariant::Weighted,
            &mut StdRng::seed_from_u64(31),
            0,
        );
        let got = run_parallel(
            &problem,
            &lists,
            None,
            SelectVarVariant::Weighted,
            1,
            &mut StdRng::seed_from_u64(31),
            0,
        );

        assert_eq!(got.satisfiable, want.satisfiable);
        assert_eq!(got.num_nodes, want.num_nodes);
        assert_eq!(got.timed_out, want.timed_out);
    }

    /// Verifies that splitting a search across several concurrent
    /// workers still finds a genuine satisfying assignment and
    /// reports it correctly, across a range of thread counts
    /// (including more threads than the problem has variables, to
    /// exercise the "fewer seeds than threads" path STAGE18.md
    /// describes).
    #[test]
    fn test_run_parallel_finds_satisfiable_formula() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![1, 2], vec![-1, 3], vec![-2, -3]],
        };
        let lists = crate::occurrence::build(&problem);

        for num_threads in [2usize, 4, 8, 32] {
            let mut rng = StdRng::seed_from_u64(num_threads as u64);
            let result = run_parallel(
                &problem,
                &lists,
                None,
                SelectVarVariant::Weighted,
                num_threads,
                &mut rng,
                0,
            );

            assert!(
                result.satisfiable,
                "num_threads={num_threads}: expected satisfiable"
            );
            for (ci, clause) in problem.clauses.iter().enumerate() {
                let satisfied = clause
                    .iter()
                    .any(|&lit| assignment::literal_is_true(&result.assignment, lit));
                assert!(
                    satisfied,
                    "num_threads={num_threads}: clause {ci} ({clause:?}) not satisfied"
                );
            }
        }
    }

    /// Verifies that the parallel search still proves UNSAT correctly
    /// (not just "gives up"), across a range of thread counts, on the
    /// same pigeonhole instance `run`'s own single-threaded test
    /// uses.
    #[test]
    fn test_run_parallel_proves_unsatisfiable_pigeonhole() {
        let problem = pigeonhole_problem(4, 3);
        let lists = crate::occurrence::build(&problem);

        for num_threads in [2usize, 4, 8, 16] {
            let mut rng = StdRng::seed_from_u64(num_threads as u64 + 100);
            let result = run_parallel(
                &problem,
                &lists,
                None,
                SelectVarVariant::Weighted,
                num_threads,
                &mut rng,
                0,
            );

            assert!(
                !result.satisfiable,
                "num_threads={num_threads}: expected unsatisfiable"
            );
            assert!(
                !result.timed_out,
                "num_threads={num_threads}: expected a genuine UNSAT proof, not a timeout"
            );
        }
    }

    /// A heavier version of the above, specifically to stress work
    /// redistribution: large enough that no single worker's initial
    /// seed alone would exhaust the tree quickly, so correctness here
    /// also exercises genuine stealing (not just BFS resolving
    /// everything up front) and termination detection under real
    /// search-tree load, at a thread count (64) well past what
    /// REPORT16.md speculatively cited as a scaling ceiling ("no more
    /// than 16 workers") but consistent with STAGE18.md's actual
    /// requirement (128 or more).
    #[test]
    fn test_run_parallel_proves_unsatisfiable_larger_pigeonhole() {
        let problem = pigeonhole_problem(6, 5);
        let lists = crate::occurrence::build(&problem);
        let mut rng = StdRng::seed_from_u64(77);

        let result = run_parallel(
            &problem,
            &lists,
            None,
            SelectVarVariant::Weighted,
            64,
            &mut rng,
            0,
        );

        assert!(!result.satisfiable);
        assert!(!result.timed_out);
        assert!(result.num_nodes > 0);
    }

    /// Verifies STAGE18.md's "BFS solves the problem" SAT case: a
    /// formula trivial enough that the very first branch `bfs_seed`
    /// tries already completes a satisfying assignment, before
    /// `num_threads` seeds are ever collected.
    #[test]
    fn test_bfs_seed_returns_sat_directly_without_spawning_workers() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1], vec![2]],
        };
        let lists = crate::occurrence::build(&problem);
        let (clauses, root) = bootstrap(&problem).expect("bootstrap reported UNSAT unexpectedly");
        let working_problem = Problem {
            num_vars: problem.num_vars,
            clauses: clauses.clone(),
        };

        let result = bfs_seed(
            &clauses,
            &lists,
            &working_problem,
            root,
            8,
            SelectVarVariant::Weighted,
            &mut StdRng::seed_from_u64(1),
            None,
            Instant::now(),
        );

        match result {
            BfsResult::Sat(solve_result) => {
                for (ci, clause) in problem.clauses.iter().enumerate() {
                    let satisfied = clause
                        .iter()
                        .any(|&lit| assignment::literal_is_true(&solve_result.assignment, lit));
                    assert!(satisfied, "clause {ci} ({clause:?}) not satisfied");
                }
            }
            _ => panic!("expected BfsResult::Sat"),
        }
    }

    /// Verifies STAGE18.md's "BFS solves the problem" UNSAT case: a
    /// formula small enough that BFS's own expansion exhausts the
    /// entire tree (every branch pruned) before ever collecting
    /// `num_threads` seeds.
    #[test]
    fn test_bfs_seed_returns_unsat_directly_without_spawning_workers() {
        let problem = Problem {
            num_vars: 1,
            clauses: vec![vec![1], vec![-1]],
        };
        let lists = crate::occurrence::build(&problem);
        let Some((clauses, root)) = bootstrap(&problem) else {
            // The bootstrap's own unit propagation may already catch
            // this particular contradiction; either way is a correct
            // UNSAT.
            return;
        };
        let working_problem = Problem {
            num_vars: problem.num_vars,
            clauses: clauses.clone(),
        };

        let result = bfs_seed(
            &clauses,
            &lists,
            &working_problem,
            root,
            8,
            SelectVarVariant::Weighted,
            &mut StdRng::seed_from_u64(2),
            None,
            Instant::now(),
        );

        assert!(matches!(result, BfsResult::Unsat(_)));
    }

    /// Verifies STAGE18.md's explicit "fewer seeds than threads"
    /// allowance: a formula whose full search tree has fewer leaves
    /// than the requested thread count must still resolve correctly
    /// (via `run_parallel`, which must fall back to spawning only as
    /// many workers as there are seeds), without hanging or erroring.
    #[test]
    fn test_bfs_seed_produces_fewer_seeds_than_threads_for_a_small_tree() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1, 2], vec![-1, -2]],
        };
        let lists = crate::occurrence::build(&problem);
        let mut rng = StdRng::seed_from_u64(3);

        // 2 variables can produce at most a handful of live branches,
        // far fewer than 50 requested threads.
        let result = run_parallel(
            &problem,
            &lists,
            None,
            SelectVarVariant::Weighted,
            50,
            &mut rng,
            0,
        );

        assert!(result.satisfiable);
    }

    /// Exercises `Terminator` directly (no deque/threads involved)
    /// against the exact race the struct doc comment describes: with
    /// two workers, the first decrement (bringing the count to 1)
    /// must never be reported as terminated; the second (bringing it
    /// to 0) must consult `recheck`, and only report termination if
    /// `recheck` says so, correctly undoing the count if not.
    #[test]
    fn test_terminator_decrement_confirms_termination_only_when_truly_quiescent() {
        let term = Terminator::new(2);

        let (terminated, still_idle) =
            term.decrement(|| panic!("recheck must not be called before the count reaches zero"));
        assert!(!terminated && still_idle);

        let mut recheck_called = false;
        let (terminated, still_idle) = term.decrement(|| {
            recheck_called = true;
            false
        });
        assert!(recheck_called);
        assert!(!terminated && !still_idle);
        assert_eq!(term.active.load(Ordering::SeqCst), 1);

        let (terminated, still_idle) = term.decrement(|| true);
        assert!(terminated && still_idle);
    }

    /// Verifies that calling `mark_idle` repeatedly without an
    /// intervening `mark_active` only touches the shared `Terminator`
    /// once.
    #[test]
    fn test_worker_state_mark_idle_is_idempotent() {
        let term = Terminator::new(3);
        let mut w = WorkerState::new(&term);

        w.mark_idle(|| false);
        assert_eq!(term.active.load(Ordering::SeqCst), 2);

        for _ in 0..3 {
            let terminated =
                w.mark_idle(|| panic!("recheck must not be called on a redundant mark_idle"));
            assert!(!terminated);
        }
        assert_eq!(term.active.load(Ordering::SeqCst), 2);

        w.mark_active();
        assert_eq!(term.active.load(Ordering::SeqCst), 3);
    }

    /// Mirrors `run`'s own equivalent test: a vanishingly small time
    /// limit against a problem hard enough to not finish instantly,
    /// split across several threads.
    #[test]
    fn test_run_parallel_respects_time_limit() {
        let problem = pigeonhole_problem(9, 8);
        let lists = crate::occurrence::build(&problem);
        let mut rng = StdRng::seed_from_u64(9);
        let tiny = Duration::from_nanos(1);

        let result = run_parallel(
            &problem,
            &lists,
            Some(tiny),
            SelectVarVariant::Weighted,
            4,
            &mut rng,
            0,
        );

        assert!(
            result.timed_out,
            "expected timed_out = true with a 1ns time limit"
        );
        assert!(!result.satisfiable);
    }

    /// Runs several times on a formula with many satisfying
    /// assignments and many threads, to exercise (probabilistically)
    /// the case where more than one worker might complete an
    /// assignment at nearly the same time -- the CAS-based
    /// single-winner protocol (mirroring Stage 17's hillclimb/
    /// walksat) must still report exactly one coherent satisfying
    /// assignment every time, never a mix or a panic.
    #[test]
    fn test_run_parallel_reports_exactly_one_winner() {
        let problem = Problem {
            num_vars: 6,
            clauses: vec![vec![1], vec![2], vec![3], vec![4], vec![5], vec![6]],
        };
        let lists = crate::occurrence::build(&problem);

        for trial in 0u64..20 {
            let mut rng = StdRng::seed_from_u64(trial);
            let result = run_parallel(
                &problem,
                &lists,
                None,
                SelectVarVariant::Weighted,
                16,
                &mut rng,
                0,
            );
            assert!(result.satisfiable, "trial {trial}: expected satisfiable");
            for (ci, clause) in problem.clauses.iter().enumerate() {
                let satisfied = clause
                    .iter()
                    .any(|&lit| assignment::literal_is_true(&result.assignment, lit));
                assert!(
                    satisfied,
                    "trial {trial}: clause {ci} ({clause:?}) not satisfied"
                );
            }
        }
    }
}
