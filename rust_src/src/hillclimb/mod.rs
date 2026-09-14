//! Implements the hill-climb-like local-search SAT solving methods
//! used by vibe_sat:
//!
//! - [`run`] implements the simple hill-climb from STAGE2.md:
//!   repeatedly start from a random complete assignment and greedily
//!   flip single variables as long as doing so increases the number
//!   of satisfied clauses, restarting whenever no single flip can
//!   improve further.
//! - [`walksat::run_walksat`] implements WalkSAT (STAGE4.md's more
//!   advanced hill-climb), which always flips a variable from some
//!   currently unsatisfied clause -- usually the one that breaks the
//!   fewest other clauses, but occasionally a random one -- which
//!   lets it escape the local optima that can trap the simple
//!   hill-climb.
//!
//! Both algorithms are built on the same [`ClimbState`], which tracks
//! a complete assignment together with enough per-clause bookkeeping
//! to evaluate and apply a single-variable flip in time proportional
//! to that variable's occurrence count. `walksat` is a private
//! submodule (not a sibling file) specifically so it can share
//! `ClimbState`'s private fields and methods, the same way both
//! algorithms share one `climbState` type in the Go version's
//! `hillclimb` package.

pub mod walksat;

use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicI64, Ordering};
use std::thread;
use std::time::{Duration, Instant};

use rand::rngs::StdRng;
use rand::seq::SliceRandom;
use rand::{Rng, RngExt, SeedableRng};

use crate::assignment::{self, Assignment, Value};
use crate::cnf::{Clause, Problem};
use crate::occurrence::Lists;

/// Configures a single call to [`run`]: how many random restarts to
/// attempt, how long to keep searching, or both (in which case the
/// search stops as soon as either limit is reached). `None` means
/// that limit does not apply.
///
/// `stop` and `best_score` (STAGE17.md) exist to let [`run_parallel`]
/// coordinate several concurrently running instances of the same
/// search; every single-threaded caller (including [`run`] itself)
/// leaves them `None`, in which case they have no effect whatsoever
/// and behavior is identical to before Stage 17. Not `Copy` (unlike
/// before Stage 17) because `Arc` isn't; `Clone` still is, and every
/// existing caller only ever needs one owned copy anyway.
#[derive(Debug, Clone, Default)]
pub struct Params {
    pub num_starts: Option<usize>,
    pub time_limit: Option<Duration>,

    /// If `Some`, checked before every restart; if it is already
    /// `true`, the search stops immediately, exactly as if
    /// `num_starts`/`time_limit` had been reached. [`run_parallel`]
    /// gives every worker a clone of the same `Arc<AtomicBool>`, so
    /// that the moment any one of them finds a satisfying assignment,
    /// every other worker notices at its next restart and stops
    /// promptly instead of continuing to search for a solution that
    /// is no longer needed.
    pub stop: Option<Arc<AtomicBool>>,

    /// If `Some`, replaces `run`'s own local best-score tracking:
    /// instead of comparing against a private variable,
    /// `record_if_best` compare-and-swaps against this shared value,
    /// so that several concurrently running instances
    /// ([`run_parallel`]'s workers) track one global best score
    /// between them, and each only prints "new best score"
    /// (`verbose >= 2`) when it actually raised that global maximum --
    /// not merely its own, possibly stale, local one.
    pub best_score: Option<Arc<AtomicI64>>,
}

/// Describes the outcome of a hill-climbing run.
pub struct SolveResult {
    /// Whether a fully satisfying assignment was found.
    pub satisfiable: bool,
    /// The satisfying assignment; only meaningful if `satisfiable`.
    pub assignment: Assignment,
    /// The score of `assignment` (number of satisfied clauses). Not
    /// read by `main` in this stage, but kept as diagnostic output
    /// for callers (and covered directly by tests).
    #[allow(dead_code)]
    pub score: usize,
    /// The number of random restarts performed. Same status as
    /// `score` above.
    #[allow(dead_code)]
    pub starts: usize,
}

/// Returns whether at least one literal of `clause` is true under
/// `assignment`.
fn clause_is_satisfied(clause: &Clause, assignment: &Assignment) -> bool {
    clause
        .iter()
        .any(|&literal| assignment::literal_is_true(assignment, literal))
}

/// Returns the number of clauses in `problem` that are satisfied by
/// `assignment`. Not called outside of tests in this stage (the
/// hill-climb loop tracks its score incrementally instead), but kept
/// as a straightforward, independently-testable reference
/// implementation that the incremental bookkeeping is checked
/// against.
#[allow(dead_code)]
pub fn score(problem: &Problem, assignment: &Assignment) -> usize {
    problem
        .clauses
        .iter()
        .filter(|clause| clause_is_satisfied(clause, assignment))
        .count()
}

/// Computes, for every clause of `problem`, the number of its
/// literals that are currently true under `assignment`, along with
/// the resulting score (the number of clauses whose count is greater
/// than zero, i.e. satisfied).
fn clause_true_counts(problem: &Problem, assignment: &Assignment) -> (Vec<usize>, usize) {
    let mut counts = vec![0usize; problem.clauses.len()];
    let mut current_score = 0;
    for (i, clause) in problem.clauses.iter().enumerate() {
        for &literal in clause {
            if assignment::literal_is_true(assignment, literal) {
                counts[i] += 1;
            }
        }
        if counts[i] > 0 {
            current_score += 1;
        }
    }
    (counts, current_score)
}

/// Holds the mutable bookkeeping needed to evaluate and apply
/// single-variable flips in time proportional to that variable's
/// occurrence count, rather than rescanning every clause on every
/// flip. Shared by every local-search algorithm in this module (the
/// simple hill-climb in [`run`], and WalkSAT in
/// [`walksat::run_walksat`]), since both are built on the same
/// incremental satisfied-clause tracking.
struct ClimbState<'a> {
    problem: &'a Problem,
    lists: &'a Lists,
    assignment: Assignment,
    counts: Vec<usize>,        // counts[c] = number of true literals in clause c
    score: usize,              // number of clauses with counts[c] > 0
    unsat_clauses: Vec<usize>, // indices of every clause currently unsatisfied, in no particular order
    unsat_pos: Vec<Option<usize>>, // unsat_pos[c] = index of c within unsat_clauses, or None if c is satisfied
}

impl<'a> ClimbState<'a> {
    /// Builds a `ClimbState` from a complete starting assignment,
    /// computing the initial per-clause true-literal counts, score,
    /// and unsatisfied-clause set from scratch.
    fn new(problem: &'a Problem, lists: &'a Lists, assignment: Assignment) -> Self {
        let (counts, score) = clause_true_counts(problem, &assignment);
        let mut unsat_clauses = Vec::new();
        let mut unsat_pos = vec![None; problem.clauses.len()];
        for (c, &count) in counts.iter().enumerate() {
            if count == 0 {
                unsat_pos[c] = Some(unsat_clauses.len());
                unsat_clauses.push(c);
            }
        }
        ClimbState {
            problem,
            lists,
            assignment,
            counts,
            score,
            unsat_clauses,
            unsat_pos,
        }
    }

    /// Records that clause `c` has just become unsatisfied, in O(1).
    fn mark_unsatisfied(&mut self, c: usize) {
        self.unsat_pos[c] = Some(self.unsat_clauses.len());
        self.unsat_clauses.push(c);
    }

    /// Records that clause `c` has just become satisfied, in O(1), by
    /// swapping it with the last entry of `unsat_clauses` before
    /// shrinking the vector.
    fn mark_satisfied(&mut self, c: usize) {
        let pos = self.unsat_pos[c].expect("clause must be tracked as unsatisfied");
        let last = self.unsat_clauses.len() - 1;
        let moved_clause = self.unsat_clauses[last];
        self.unsat_clauses[pos] = moved_clause;
        self.unsat_pos[moved_clause] = Some(pos);
        self.unsat_clauses.pop();
        self.unsat_pos[c] = None;
    }

    /// Flips the value of variable `v` in place, updates the
    /// per-clause true-literal counts, the overall score, and the
    /// unsatisfied-clause set to match, and returns the resulting
    /// change in score. Calling `flip` a second time with the same
    /// `v` exactly reverses the first call, since flipping is its own
    /// inverse.
    fn flip(&mut self, v: usize) -> i64 {
        let was_true = self.assignment[v] == Value::True;
        let (losing, gaining) = if was_true {
            (&self.lists.positive[v], &self.lists.negative[v])
        } else {
            (&self.lists.negative[v], &self.lists.positive[v])
        };

        let mut delta: i64 = 0;
        let mut newly_unsatisfied = Vec::new();
        let mut newly_satisfied = Vec::new();
        for &c in losing {
            self.counts[c] -= 1;
            if self.counts[c] == 0 {
                delta -= 1;
                newly_unsatisfied.push(c);
            }
        }
        for &c in gaining {
            self.counts[c] += 1;
            if self.counts[c] == 1 {
                delta += 1;
                newly_satisfied.push(c);
            }
        }
        for c in newly_unsatisfied {
            self.mark_unsatisfied(c);
        }
        for c in newly_satisfied {
            self.mark_satisfied(c);
        }

        self.assignment[v] = if was_true { Value::False } else { Value::True };
        self.score = (self.score as i64 + delta) as usize;
        delta
    }

    /// Returns the number of currently satisfied clauses that would
    /// become unsatisfied if variable `v` were flipped right now,
    /// without actually flipping it. WalkSAT (see [`walksat`]) uses
    /// this to prefer flips that break as few other clauses as
    /// possible.
    fn break_count(&self, v: usize) -> usize {
        let was_true = self.assignment[v] == Value::True;
        let losing = if was_true {
            &self.lists.positive[v]
        } else {
            &self.lists.negative[v]
        };
        losing.iter().filter(|&&c| self.counts[c] == 1).count()
    }

    /// Performs one pass over the variables in the order given by
    /// `order`, attempting to flip each one in turn. A flip that
    /// strictly increases the score is kept, in which case
    /// `on_improved` is invoked with the new score; a flip that does
    /// not strictly improve the score is reverted before moving on.
    /// Returns true if at least one flip was kept during the pass.
    fn sweep(&mut self, order: &[usize], mut on_improved: impl FnMut(usize)) -> bool {
        let mut changed = false;
        for &v in order {
            let delta = self.flip(v);
            if delta > 0 {
                changed = true;
                on_improved(self.score);
            } else {
                self.flip(v); // revert
            }
        }
        changed
    }
}

/// Returns a random permutation of the variable numbers 1 through
/// `num_vars`, ordered using `rng`.
fn random_permutation<R: Rng>(num_vars: usize, rng: &mut R) -> Vec<usize> {
    let mut order: Vec<usize> = (1..=num_vars).collect();
    order.shuffle(rng);
    order
}

/// Formats the configured restart/time limits for the "hillclimb:"
/// announcement printed at verbose level 1.
fn describe_params(params: &Params) -> String {
    let mut parts = Vec::new();
    if let Some(n) = params.num_starts {
        parts.push(format!("num_starts={n}"));
    }
    if let Some(limit) = params.time_limit {
        parts.push(format!("time_limit_secs={}", limit.as_secs()));
    }
    if parts.is_empty() {
        "(no limit)".to_string()
    } else {
        parts.join(" ")
    }
}

/// [`describe_params`], extended with the worker count, for
/// [`run_parallel`]'s "hillclimb: ..." announcement.
fn describe_parallel_params(params: &Params, num_threads: usize) -> String {
    format!("num_threads={num_threads} {}", describe_params(params))
}

/// Returns "SAT" or "UNKNOWN", matching what [`run`]/[`walksat::run_walksat`]
/// print at verbose level 1 once a search concludes.
fn verdict(satisfiable: bool) -> &'static str {
    if satisfiable { "SAT" } else { "UNKNOWN" }
}

/// Draws a fresh `u64` from `rng` to seed a new, independent
/// [`StdRng`]. Used by [`run_parallel`]/[`walksat::run_walksat_parallel`]
/// to give every worker its own generator (drained sequentially from
/// the caller's `rng`, before any worker thread starts, so the whole
/// sequence of derived seeds is itself deterministic given `rng`'s
/// own state) rather than sharing one generator across threads, which
/// would need a lock (contention, and still scheduling-dependent
/// nondeterminism) or simply wouldn't compile (`Rng`'s methods take
/// `&mut self`).
fn new_sub_rng<R: Rng>(rng: &mut R) -> StdRng {
    StdRng::seed_from_u64(rng.random::<u64>())
}

/// Performs the hill-climbing search described by STAGE2.md:
/// repeatedly starting from a random complete assignment and greedily
/// flipping variables until no single flip can improve the score,
/// stopping as soon as a satisfying assignment is found or the limits
/// in `params` are reached. `rng` supplies all the randomness used
/// (the initial assignment and flip order of every start), and
/// `verbose` controls how much progress is printed to stdout:
///
/// - `verbose >= 1`: prints "hillclimb" with the configured limits
///   before searching, and "SAT" or "UNKNOWN" after searching.
/// - `verbose >= 2`: prints the score every time it sets a new best
///   score across all starts so far.
/// - `verbose >= 3`: prints the score every time a start gets stuck
///   at a local optimum that does not satisfy every clause.
pub fn run<R: Rng>(
    problem: &Problem,
    lists: &Lists,
    params: Params,
    rng: &mut R,
    verbose: i32,
) -> SolveResult {
    if verbose >= 1 {
        println!("hillclimb: {}", describe_params(&params));
    }
    let result = run_loop(problem, lists, params, rng, verbose);
    if verbose >= 1 {
        println!("{}", verdict(result.satisfiable));
    }
    result
}

/// Runs the same search as [`run`], split across `num_threads`
/// concurrent workers (STAGE17.md). `num_threads <= 1` delegates
/// straight to [`run`], with `rng` used exactly as it always has
/// been -- so behavior (including every random choice made) is
/// bit-for-bit identical to calling [`run`] directly whenever
/// multithreading isn't actually in use.
///
/// If `params.num_starts` is given, it is split as evenly as
/// possible across the workers (each doing
/// `ceil(num_starts/num_threads)` starts, so the total may run a few
/// more starts than asked for, never fewer); `params.time_limit`, if
/// given, is handed to every worker unchanged rather than divided,
/// since the workers run concurrently and dividing it would just
/// shorten the wall-clock time spent without letting any more work
/// fit in it. `rng` is used, before any worker starts, to derive one
/// independent [`StdRng`] per worker (see [`new_sub_rng`]), so the
/// overall result is fully reproducible given `(rng`'s state,
/// `num_threads)` even though which worker's answer wins a race to a
/// solution is not.
///
/// The moment any worker finds a satisfying assignment, it signals
/// every other worker to stop at its next restart (see
/// `Params::stop`), and that worker's result -- and only that
/// worker's -- is the one `run_parallel` reports; every other
/// worker's own result, even a simultaneously-successful one, is
/// discarded, so exactly one solution is ever reported, per
/// STAGE17.md. `SolveResult::starts` is the sum of every worker's own
/// start count, win or lose.
pub fn run_parallel<R: Rng>(
    problem: &Problem,
    lists: &Lists,
    params: Params,
    num_threads: usize,
    rng: &mut R,
    verbose: i32,
) -> SolveResult {
    if num_threads <= 1 {
        return run(problem, lists, params, rng, verbose);
    }

    if verbose >= 1 {
        println!(
            "hillclimb: {}",
            describe_parallel_params(&params, num_threads)
        );
    }

    let stop = Arc::new(AtomicBool::new(false));
    let best_score = Arc::new(AtomicI64::new(-1));
    // Sub-generators are derived sequentially from rng up front (not
    // inside the scope below), so the whole sequence is deterministic
    // given rng's own state regardless of thread scheduling.
    let mut sub_rngs: Vec<StdRng> = (0..num_threads).map(|_| new_sub_rng(rng)).collect();

    let mut total_starts = 0usize;
    let mut winner: Option<SolveResult> = None;

    // A scoped spawn (rather than thread::spawn) is what lets each
    // worker closure below borrow problem/lists/sub_rngs directly
    // instead of requiring 'static + owned copies of them: the scope
    // guarantees every spawned thread has finished before it returns,
    // so those borrows can never outlive what they point to.
    thread::scope(|scope| {
        let handles: Vec<_> = sub_rngs
            .iter_mut()
            .enumerate()
            .map(|(i, sub_rng)| {
                let mut worker_params = params.clone();
                worker_params.stop = Some(Arc::clone(&stop));
                worker_params.best_score = Some(Arc::clone(&best_score));
                if let Some(n) = params.num_starts {
                    worker_params.num_starts = Some(ceil_div(n, num_threads));
                }
                let stop_for_worker = Arc::clone(&stop);

                scope.spawn(move || {
                    let result = run_loop(problem, lists, worker_params, sub_rng, verbose);
                    let won = result.satisfiable
                        && stop_for_worker
                            .compare_exchange(false, true, Ordering::SeqCst, Ordering::SeqCst)
                            .is_ok();
                    (i, won, result)
                })
            })
            .collect();

        for handle in handles {
            let (_, won, result) = handle.join().expect("hillclimb worker thread panicked");
            total_starts += result.starts;
            if won {
                winner = Some(result);
            }
        }
    });

    let final_result = match winner {
        Some(result) => SolveResult {
            satisfiable: true,
            assignment: result.assignment,
            score: result.score,
            starts: total_starts,
        },
        None => SolveResult {
            satisfiable: false,
            assignment: assignment::new(problem.num_vars),
            score: 0,
            starts: total_starts,
        },
    };

    if verbose >= 1 {
        println!("{}", verdict(final_result.satisfiable));
    }
    final_result
}

/// [`run`]'s search loop, without the "hillclimb: ..."/"SAT"/"UNKNOWN"
/// announcements -- those are the caller's responsibility ([`run`]
/// prints its own; [`run_parallel`] prints one combined
/// announcement/verdict for the whole parallel search instead of one
/// per worker), so that multithreaded runs don't produce
/// `num_threads` duplicate announcement lines.
fn run_loop<R: Rng>(
    problem: &Problem,
    lists: &Lists,
    params: Params,
    rng: &mut R,
    verbose: i32,
) -> SolveResult {
    let start_time = Instant::now();
    let mut local_best_score: i64 = -1;
    let num_clauses = problem.num_clauses();

    let mut record_if_best = |score: usize| {
        if let Some(shared) = &params.best_score {
            loop {
                let old = shared.load(Ordering::SeqCst);
                if score as i64 <= old {
                    return;
                }
                if shared
                    .compare_exchange(old, score as i64, Ordering::SeqCst, Ordering::SeqCst)
                    .is_ok()
                {
                    if verbose >= 2 {
                        println!("new best score: {score}/{num_clauses}");
                    }
                    return;
                }
                // Another worker updated it concurrently; retry against
                // whatever the new value is.
            }
        }
        if score as i64 > local_best_score {
            local_best_score = score as i64;
            if verbose >= 2 {
                println!("new best score: {score}/{num_clauses}");
            }
        }
    };

    let mut starts = 0usize;
    loop {
        if let Some(stop) = &params.stop
            && stop.load(Ordering::SeqCst)
        {
            break;
        }
        if let Some(limit) = params.num_starts
            && starts >= limit
        {
            break;
        }
        if let Some(limit) = params.time_limit
            && start_time.elapsed() >= limit
        {
            break;
        }
        starts += 1;

        let initial_assignment = assignment::new_random(problem.num_vars, rng);
        let mut state = ClimbState::new(problem, lists, initial_assignment);
        record_if_best(state.score);

        loop {
            let order = random_permutation(problem.num_vars, rng);
            if !state.sweep(&order, &mut record_if_best) {
                break;
            }
        }

        if state.score == num_clauses {
            return SolveResult {
                satisfiable: true,
                assignment: state.assignment,
                score: state.score,
                starts,
            };
        }

        if verbose >= 3 {
            println!(
                "stuck at score {}/{num_clauses} after start {starts}",
                state.score
            );
        }
    }

    SolveResult {
        satisfiable: false,
        assignment: assignment::new(problem.num_vars),
        score: 0,
        starts,
    }
}

/// Returns `ceil(a/b)` for positive `a` and `b`, without floating
/// point.
fn ceil_div(a: usize, b: usize) -> usize {
    a.div_ceil(b)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::occurrence;
    use rand::SeedableRng;
    use rand::rngs::StdRng;

    #[test]
    fn test_score_counts_satisfied_clauses() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1], vec![-1], vec![2, -1]],
        };
        let mut a = assignment::new(2);
        a[1] = Value::True;
        a[2] = Value::False;

        // Clause {1} is satisfied (1 is True). Clause {-1} and clause
        // {2, -1} are both unsatisfied (1 is True, so -1 is false; 2
        // is False). Only 1 of the 3 clauses is satisfied.
        assert_eq!(score(&problem, &a), 1);
    }

    #[test]
    fn test_clause_true_counts_matches_score() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1, 2], vec![-1, -2]],
        };
        let mut a = assignment::new(2);
        a[1] = Value::True;
        a[2] = Value::True;

        let (counts, computed_score) = clause_true_counts(&problem, &a);
        assert_eq!(computed_score, score(&problem, &a));
        assert_eq!(counts[0], 2);
        assert_eq!(counts[1], 0);
    }

    #[test]
    fn test_flip_updates_score_correctly() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1, 2], vec![-1]],
        };
        let lists = occurrence::build(&problem);
        let mut a = assignment::new(2);
        a[1] = Value::False;
        a[2] = Value::False;

        let mut state = ClimbState::new(&problem, &lists, a);
        let delta = state.flip(1); // variable 1: False -> True

        let (want_counts, want_score) = clause_true_counts(&problem, &state.assignment);
        assert_eq!(state.score, want_score);
        assert_eq!(state.counts, want_counts);
        assert_eq!(state.assignment[1], Value::True);
        // Clause 0 goes from unsatisfied to satisfied (+1), clause 1
        // goes from satisfied to unsatisfied (-1): net delta 0.
        assert_eq!(delta, 0);
    }

    #[test]
    fn test_flip_twice_reverts_to_original_state() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![1, -2], vec![2, 3], vec![-3]],
        };
        let lists = occurrence::build(&problem);
        let a = assignment::new_random(3, &mut StdRng::seed_from_u64(9));
        let original_assignment = a.clone();

        let mut state = ClimbState::new(&problem, &lists, a);
        let original_counts = state.counts.clone();
        let original_score = state.score;

        state.flip(2);
        state.flip(2);

        assert_eq!(state.assignment, original_assignment);
        assert_eq!(state.counts, original_counts);
        assert_eq!(state.score, original_score);
    }

    #[test]
    fn test_new_climb_state_tracks_unsatisfied_clauses() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1], vec![-1], vec![2]],
        };
        let lists = occurrence::build(&problem);
        let mut a = assignment::new(2);
        a[1] = Value::True;
        a[2] = Value::False;

        let state = ClimbState::new(&problem, &lists, a);

        let want_unsat: std::collections::HashSet<usize> = [1, 2].into_iter().collect();
        let got_unsat: std::collections::HashSet<usize> =
            state.unsat_clauses.iter().copied().collect();
        assert_eq!(got_unsat, want_unsat);
        for &c in &state.unsat_clauses {
            assert_eq!(state.unsat_pos[c], Some(index_of(&state.unsat_clauses, c)));
        }
        assert_eq!(state.unsat_pos[0], None);
    }

    #[test]
    fn test_flip_keeps_unsat_clauses_consistent() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![1, 2], vec![-1, 3], vec![-2, -3]],
        };
        let lists = occurrence::build(&problem);
        let mut a = assignment::new(3);
        a[1] = Value::False;
        a[2] = Value::False;
        a[3] = Value::False;

        let mut state = ClimbState::new(&problem, &lists, a);
        state.flip(1);
        state.flip(2);

        let (want_counts, _) = clause_true_counts(&problem, &state.assignment);
        let want_unsat: std::collections::HashSet<usize> = want_counts
            .iter()
            .enumerate()
            .filter(|&(_, &count)| count == 0)
            .map(|(c, _)| c)
            .collect();
        let got_unsat: std::collections::HashSet<usize> =
            state.unsat_clauses.iter().copied().collect();
        assert_eq!(got_unsat, want_unsat);

        for (c, _) in want_counts.iter().enumerate() {
            if want_unsat.contains(&c) {
                assert_eq!(state.unsat_pos[c], Some(index_of(&state.unsat_clauses, c)));
            } else {
                assert_eq!(state.unsat_pos[c], None);
            }
        }
    }

    /// Returns the index of `target` within `haystack`, panicking if
    /// absent; a small test helper for checking `unsat_pos` entries.
    fn index_of(haystack: &[usize], target: usize) -> usize {
        haystack
            .iter()
            .position(|&v| v == target)
            .expect("target must be present in haystack")
    }

    #[test]
    fn test_flip_handles_tautological_clause() {
        let problem = Problem {
            num_vars: 1,
            clauses: vec![vec![1, -1]],
        };
        let lists = occurrence::build(&problem);
        let mut a = assignment::new(1);
        a[1] = Value::True;

        let mut state = ClimbState::new(&problem, &lists, a);
        assert_eq!(state.score, 1);

        let delta = state.flip(1);
        assert_eq!(delta, 0);
        assert_eq!(state.score, 1);
        assert_eq!(state.counts[0], 1);
    }

    #[test]
    fn test_run_finds_satisfiable_formula() {
        // Every clause is a single positive literal, so flipping any
        // currently-false variable to true strictly helps; hill
        // climbing is guaranteed to reach full satisfaction.
        let problem = Problem {
            num_vars: 5,
            clauses: vec![vec![1], vec![2], vec![3], vec![4], vec![5]],
        };
        let lists = occurrence::build(&problem);
        let params = Params {
            num_starts: Some(1),
            time_limit: None,
            ..Default::default()
        };
        let mut rng = StdRng::seed_from_u64(1);

        let result = run(&problem, &lists, params, &mut rng, 0);

        assert!(
            result.satisfiable,
            "score {}/{}",
            result.score,
            problem.num_clauses()
        );
        assert_eq!(result.score, problem.num_clauses());
    }

    #[test]
    fn test_run_reports_unknown_for_unsatisfiable_formula() {
        // x1 AND NOT x1: never satisfiable, regardless of restarts.
        let problem = Problem {
            num_vars: 1,
            clauses: vec![vec![1], vec![-1]],
        };
        let lists = occurrence::build(&problem);
        let params = Params {
            num_starts: Some(5),
            time_limit: None,
            ..Default::default()
        };
        let mut rng = StdRng::seed_from_u64(2);

        let result = run(&problem, &lists, params, &mut rng, 0);

        assert!(!result.satisfiable);
        assert_eq!(result.starts, 5);
    }

    #[test]
    fn test_random_permutation_is_a_permutation() {
        let mut rng = StdRng::seed_from_u64(3);
        let order = random_permutation(10, &mut rng);
        let mut seen = std::collections::HashSet::new();
        for &v in &order {
            assert!((1..=10).contains(&v));
            assert!(seen.insert(v), "duplicate value {v}");
        }
        assert_eq!(seen.len(), 10);
    }

    /// Verifies STAGE17.md's core compatibility guarantee:
    /// `run_parallel` with `num_threads <= 1` must behave identically
    /// to calling `run` directly, down to every random choice made,
    /// since it delegates straight to `run` rather than going through
    /// any thread/atomic machinery at all.
    #[test]
    fn test_run_parallel_with_one_thread_matches_run() {
        let problem = Problem {
            num_vars: 4,
            clauses: vec![vec![1, 2], vec![-1, 3], vec![-2, -3], vec![4]],
        };
        let lists = occurrence::build(&problem);
        let params = Params {
            num_starts: Some(3),
            ..Default::default()
        };

        let want = run(
            &problem,
            &lists,
            params.clone(),
            &mut StdRng::seed_from_u64(11),
            0,
        );
        let got = run_parallel(
            &problem,
            &lists,
            params,
            1,
            &mut StdRng::seed_from_u64(11),
            0,
        );

        assert_eq!(got.satisfiable, want.satisfiable);
        assert_eq!(got.score, want.score);
        assert_eq!(got.starts, want.starts);
        if got.satisfiable {
            assert_eq!(got.assignment, want.assignment);
        }
    }

    /// Verifies that splitting a search across several concurrent
    /// workers still finds a genuine satisfying assignment and
    /// reports it correctly.
    #[test]
    fn test_run_parallel_finds_satisfiable_formula() {
        let problem = Problem {
            num_vars: 5,
            clauses: vec![vec![1], vec![2], vec![3], vec![4], vec![5]],
        };
        let lists = occurrence::build(&problem);
        let params = Params {
            num_starts: Some(8),
            ..Default::default()
        };
        let mut rng = StdRng::seed_from_u64(12);

        let result = run_parallel(&problem, &lists, params, 4, &mut rng, 0);

        assert!(
            result.satisfiable,
            "score {}/{}",
            result.score,
            problem.num_clauses()
        );
        assert_eq!(result.score, problem.num_clauses());
    }

    /// Verifies that, for an unsatisfiable formula (so every worker
    /// runs its full local quota with no early stop), the aggregate
    /// `starts` is at least `num_starts` (each of 4 workers does
    /// `ceil(num_starts/4)`, so the total may be rounded up, never
    /// down).
    #[test]
    fn test_run_parallel_splits_num_starts_across_threads() {
        let problem = Problem {
            num_vars: 1,
            clauses: vec![vec![1], vec![-1]],
        };
        let lists = occurrence::build(&problem);
        let params = Params {
            num_starts: Some(20),
            ..Default::default()
        };
        let mut rng = StdRng::seed_from_u64(13);

        let result = run_parallel(&problem, &lists, params, 4, &mut rng, 0);

        assert!(!result.satisfiable);
        assert!(
            result.starts >= 20,
            "starts = {}, want >= 20",
            result.starts
        );
        assert!(
            result.starts < 20 + 4,
            "starts = {}, want <= 23 (ceil rounding across 4 workers)",
            result.starts
        );
    }

    /// Verifies the stop signal directly: `run_loop` must perform zero
    /// starts if `params.stop` is already true before it is ever
    /// called, mirroring what happens to every other `run_parallel`
    /// worker the instant one of them finds a solution.
    #[test]
    fn test_run_loop_stops_immediately_when_stop_is_already_set() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1, 2]],
        };
        let lists = occurrence::build(&problem);
        let params = Params {
            stop: Some(Arc::new(AtomicBool::new(true))),
            ..Default::default()
        };

        let result = run_loop(&problem, &lists, params, &mut StdRng::seed_from_u64(14), 0);

        assert_eq!(result.starts, 0);
        assert!(!result.satisfiable);
    }

    /// Verifies the shared-`best_score` path directly: given a
    /// `best_score` already at 5, `run_loop`'s `record_if_best` must
    /// not report (or lower) it for a score of 5 or less, matching
    /// the single-threaded local-best-score behavior it replaces for
    /// `run_parallel`'s workers.
    #[test]
    fn test_run_loop_best_score_only_records_genuine_improvements() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![1], vec![2], vec![3]],
        };
        let lists = occurrence::build(&problem);
        let best_score = Arc::new(AtomicI64::new(5));
        let params = Params {
            num_starts: Some(1),
            best_score: Some(Arc::clone(&best_score)),
            ..Default::default()
        };

        run_loop(&problem, &lists, params, &mut StdRng::seed_from_u64(15), 0);

        assert!(best_score.load(Ordering::SeqCst) >= 5);
    }
}
