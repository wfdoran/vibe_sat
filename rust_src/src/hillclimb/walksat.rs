//! WalkSAT (Selman, Kautz & Cohen, 1994): repeatedly start from a
//! random complete assignment, then repeatedly pick a currently
//! unsatisfied clause at random and flip one of its variables --
//! usually the one that breaks the fewest other clauses, but
//! occasionally ("noise") a uniformly random one -- until either
//! every clause is satisfied or a per-try flip budget is exhausted, in
//! which case a new random assignment is tried.
//!
//! This is a submodule of [`super`] rather than a sibling file
//! specifically so it can share [`super::ClimbState`]'s private
//! fields and methods (see that module's doc comment).

use std::time::{Duration, Instant};

use rand::{Rng, RngExt};

use super::ClimbState;
use crate::assignment;
use crate::cnf::Problem;
use crate::occurrence::Lists;

/// Default value of [`WalkSatParams::max_flips_per_try`] and
/// [`WalkSatParams::noise_percent`] when the corresponding
/// `--alg-params` value is not given on the command line.
pub const DEFAULT_MAX_FLIPS_PER_TRY: usize = 10000;
pub const DEFAULT_NOISE_PERCENT: u32 = 50;

/// Configures a single call to [`run_walksat`].
#[derive(Debug, Clone, Copy)]
pub struct WalkSatParams {
    /// Number of random restarts ("tries") to attempt. `None` means
    /// unlimited, relying on `time_limit` instead to decide when to
    /// stop.
    pub num_tries: Option<usize>,
    /// How many flips a single try may make before giving up and
    /// starting a fresh try.
    pub max_flips_per_try: usize,
    /// Probability, as a percentage from 0 to 100, of flipping a
    /// uniformly random variable of the chosen unsatisfied clause
    /// instead of the one that breaks the fewest other clauses.
    pub noise_percent: u32,
    /// How long to keep searching. `None` means unlimited, relying on
    /// `num_tries` instead.
    pub time_limit: Option<Duration>,
}

impl Default for WalkSatParams {
    fn default() -> Self {
        WalkSatParams {
            num_tries: None,
            max_flips_per_try: DEFAULT_MAX_FLIPS_PER_TRY,
            noise_percent: DEFAULT_NOISE_PERCENT,
            time_limit: None,
        }
    }
}

/// Performs a WalkSAT search. Unlike the simple hill-climb in
/// [`super::run`], WalkSAT always commits the chosen flip, even when
/// it makes the score worse; this, combined with the noise parameter,
/// is what lets it escape local optima that trap the simple
/// hill-climb. The restart/time-limit handling, the result returned,
/// and the meaning of the verbose levels are otherwise identical to
/// `super::run`; see its documentation for details.
pub fn run_walksat<R: Rng>(
    problem: &Problem,
    lists: &Lists,
    params: WalkSatParams,
    rng: &mut R,
    verbose: i32,
) -> super::SolveResult {
    if verbose >= 1 {
        println!("walksat: {}", describe_params(params));
    }

    let start_time = Instant::now();
    let num_clauses = problem.num_clauses();
    let mut best_score: i64 = -1;

    let mut record_if_best = |score: usize| {
        if score as i64 > best_score {
            best_score = score as i64;
            if verbose >= 2 {
                println!("new best score: {score}/{num_clauses}");
            }
        }
    };

    let mut tries = 0usize;
    loop {
        if let Some(limit) = params.num_tries
            && tries >= limit
        {
            break;
        }
        if let Some(limit) = params.time_limit
            && start_time.elapsed() >= limit
        {
            break;
        }
        tries += 1;

        let initial_assignment = assignment::new_random(problem.num_vars, rng);
        let mut state = ClimbState::new(problem, lists, initial_assignment);
        record_if_best(state.score);

        let mut flips = 0;
        while state.score != num_clauses && flips < params.max_flips_per_try {
            let clause_index = state.unsat_clauses[rng.random_range(0..state.unsat_clauses.len())];
            let v = choose_flip_variable(&state, clause_index, params.noise_percent, rng);
            state.flip(v);
            record_if_best(state.score);
            flips += 1;
        }

        if state.score == num_clauses {
            if verbose >= 1 {
                println!("SAT");
            }
            return super::SolveResult {
                satisfiable: true,
                assignment: state.assignment,
                score: state.score,
                starts: tries,
            };
        }

        if verbose >= 3 {
            println!(
                "gave up after {} flips at score {}/{num_clauses} (try {tries})",
                params.max_flips_per_try, state.score
            );
        }
    }

    if verbose >= 1 {
        println!("UNKNOWN");
    }
    super::SolveResult {
        satisfiable: false,
        assignment: assignment::new(problem.num_vars),
        score: 0,
        starts: tries,
    }
}

/// Picks which variable to flip next, given that `clause_index` names
/// a currently unsatisfied clause of `state`: with probability
/// `noise_percent`/100, a uniformly random variable of the clause;
/// otherwise, whichever variable of the clause has the smallest break
/// count (see [`ClimbState::break_count`]), with ties broken
/// uniformly at random.
fn choose_flip_variable<R: Rng>(
    state: &ClimbState,
    clause_index: usize,
    noise_percent: u32,
    rng: &mut R,
) -> usize {
    let clause = &state.problem.clauses[clause_index];

    if noise_percent > 0 && rng.random_range(0..100) < noise_percent {
        let literal = clause[rng.random_range(0..clause.len())];
        return crate::cnf::literal_var(literal);
    }

    let mut best_var = None;
    let mut best_break_count = usize::MAX;
    let mut tie_count = 0u32;
    for &literal in clause {
        let v = crate::cnf::literal_var(literal);
        let break_count = state.break_count(v);
        if best_var.is_none() || break_count < best_break_count {
            best_var = Some(v);
            best_break_count = break_count;
            tie_count = 1;
        } else if break_count == best_break_count {
            tie_count += 1;
            // Reservoir sampling: keep the new candidate with
            // probability 1/tie_count, so that every tied candidate
            // seen so far remains equally likely to be selected.
            if rng.random_range(0..tie_count) == 0 {
                best_var = Some(v);
            }
        }
    }
    best_var.expect("an unsatisfied clause always has at least one literal")
}

/// Formats the configured limits/noise for the "walksat:"
/// announcement printed at verbose level 1.
fn describe_params(params: WalkSatParams) -> String {
    let mut description = format!(
        "max_flips_per_try={} noise_percent={}",
        params.max_flips_per_try, params.noise_percent
    );
    if let Some(n) = params.num_tries {
        description += &format!(" num_tries={n}");
    }
    if let Some(limit) = params.time_limit {
        description += &format!(" time_limit_secs={}", limit.as_secs());
    }
    description
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::assignment::Value;
    use crate::occurrence;
    use rand::SeedableRng;
    use rand::rngs::StdRng;

    #[test]
    fn test_break_count() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1], vec![1, 2]],
        };
        let lists = occurrence::build(&problem);
        let mut a = assignment::new(2);
        a[1] = Value::True;
        a[2] = Value::True;

        let state = ClimbState::new(&problem, &lists, a);
        // Clause 0 is only satisfied because 1 is True: flipping 1
        // breaks it. Clause 1 is satisfied by both 1 and 2: flipping
        // 1 does not break it (2 still satisfies it).
        assert_eq!(state.break_count(1), 1);
        assert_eq!(state.break_count(2), 0);
        // break_count must not mutate the state.
        assert_eq!(state.assignment[1], Value::True);
        assert_eq!(state.assignment[2], Value::True);
    }

    #[test]
    fn test_choose_flip_variable_greedy_picks_minimal_break_count() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![-1, -2], vec![1]],
        };
        let lists = occurrence::build(&problem);
        let mut a = assignment::new(2);
        a[1] = Value::True;
        a[2] = Value::True;
        let state = ClimbState::new(&problem, &lists, a);

        // Clause 0 is unsatisfied. Flipping 1 would break clause 1
        // (break count 1); flipping 2 breaks nothing (break count 0).
        let mut rng = StdRng::seed_from_u64(1);
        for _ in 0..10 {
            assert_eq!(choose_flip_variable(&state, 0, 0, &mut rng), 2);
        }
    }

    #[test]
    fn test_choose_flip_variable_noise_stays_within_clause() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![-1, -2, -3]],
        };
        let lists = occurrence::build(&problem);
        let a = assignment::new_random(3, &mut StdRng::seed_from_u64(4));
        let state = ClimbState::new(&problem, &lists, a);

        let mut rng = StdRng::seed_from_u64(5);
        for _ in 0..20 {
            let v = choose_flip_variable(&state, 0, 100, &mut rng);
            assert!((1..=3).contains(&v));
        }
    }

    #[test]
    fn test_run_walksat_finds_satisfiable_formula() {
        // Every clause is a single positive literal, so flipping any
        // false variable from an unsatisfied clause never breaks any
        // other clause.
        let problem = Problem {
            num_vars: 5,
            clauses: vec![vec![1], vec![2], vec![3], vec![4], vec![5]],
        };
        let lists = occurrence::build(&problem);
        let params = WalkSatParams {
            num_tries: Some(1),
            ..WalkSatParams::default()
        };
        let mut rng = StdRng::seed_from_u64(1);

        let result = run_walksat(&problem, &lists, params, &mut rng, 0);

        assert!(
            result.satisfiable,
            "score {}/{}",
            result.score,
            problem.num_clauses()
        );
        assert_eq!(result.score, problem.num_clauses());
    }

    #[test]
    fn test_run_walksat_reports_unknown_for_unsatisfiable_formula() {
        // x1 AND NOT x1: never satisfiable, regardless of tries.
        let problem = Problem {
            num_vars: 1,
            clauses: vec![vec![1], vec![-1]],
        };
        let lists = occurrence::build(&problem);
        let params = WalkSatParams {
            num_tries: Some(5),
            max_flips_per_try: 50,
            ..WalkSatParams::default()
        };
        let mut rng = StdRng::seed_from_u64(2);

        let result = run_walksat(&problem, &lists, params, &mut rng, 0);

        assert!(!result.satisfiable);
        assert_eq!(result.starts, 5);
    }
}
