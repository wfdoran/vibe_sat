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

use std::time::{Duration, Instant};

use rand::Rng;
use rand::seq::SliceRandom;

use crate::assignment::{self, Assignment, Value};
use crate::cnf::{Clause, Problem};
use crate::occurrence::Lists;

/// Configures a single call to [`run`]: how many random restarts to
/// attempt, how long to keep searching, or both (in which case the
/// search stops as soon as either limit is reached). `None` means
/// that limit does not apply.
#[derive(Debug, Clone, Copy, Default)]
pub struct Params {
    pub num_starts: Option<usize>,
    pub time_limit: Option<Duration>,
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
fn describe_params(params: Params) -> String {
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
        println!("hillclimb: {}", describe_params(params));
    }

    let start_time = Instant::now();
    let mut best_score: i64 = -1;
    let num_clauses = problem.num_clauses();

    let mut record_if_best = |score: usize| {
        if score as i64 > best_score {
            best_score = score as i64;
            if verbose >= 2 {
                println!("new best score: {score}/{num_clauses}");
            }
        }
    };

    let mut starts = 0usize;
    loop {
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
            if verbose >= 1 {
                println!("SAT");
            }
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

    if verbose >= 1 {
        println!("UNKNOWN");
    }
    SolveResult {
        satisfiable: false,
        assignment: assignment::new(problem.num_vars),
        score: 0,
        starts,
    }
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
}
