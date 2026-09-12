//! Implements the depth-first search SAT solving algorithm used by
//! vibe_sat's "dfs" algorithm: a complete DPLL-style search using
//! boolean constraint propagation (BCP) and a weighted
//! variable-selection heuristic. Unlike the hill-climb-family
//! algorithms ([`crate::hillclimb`]), this search is complete: if it
//! exhausts its search space without finding a satisfying assignment,
//! the problem is proven UNSAT, not merely "not found yet".

use std::time::{Duration, Instant};

use rand::{Rng, RngExt};

use crate::assignment::{self, Assignment, Value};
use crate::cnf::{self, Clause, Literal, Problem};
use crate::occurrence::Lists;

/// The outcome of a single call to [`bcp`].
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Status {
    /// Propagation finished without contradiction, but the assignment
    /// is still partial.
    Ok,
    /// A clause can no longer be satisfied under this assignment; it
    /// cannot be extended to a solution.
    Contra,
    /// The assignment is now complete and, by construction, satisfies
    /// every clause.
    Done,
}

/// How often the time limit is checked, in number of search nodes,
/// per STAGE5.md's suggestion ("Maybe only when num_nodes & 0xfff ==
/// 0"): checking the clock on every single node would add needless
/// overhead, since nodes are cheap and the clock only needs to be
/// checked often enough to respond to a time limit reasonably
/// promptly.
const TIME_CHECK_INTERVAL: usize = 0xfff;

/// Describes the outcome of a depth-first search.
pub struct SolveResult {
    /// Whether a satisfying assignment was found.
    pub satisfiable: bool,
    /// The satisfying assignment; only meaningful if `satisfiable`.
    pub assignment: Assignment,
    /// Number of search-tree nodes explored.
    #[allow(dead_code)]
    pub num_nodes: usize,
    /// True if the search was abandoned due to the time limit, rather
    /// than exhausting the search space.
    #[allow(dead_code)]
    pub timed_out: bool,
}

/// Performs the depth-first search described in STAGE5.md: starting
/// from the fully unassigned partial assignment, repeatedly pop a
/// partial assignment from an explicit stack, pick a variable to
/// branch on with [`select_var`], and try setting it to each of
/// `False` and `True` in turn, applying [`bcp`] after each attempt. A
/// branch that leads to a contradiction is abandoned; a branch that
/// completes the assignment means `problem` is satisfiable; a branch
/// that is merely consistent but incomplete is pushed back onto the
/// stack to be explored later. If the stack empties without ever
/// completing an assignment, `problem` is proven unsatisfiable.
///
/// If `time_limit` is `Some`, the search gives up and reports an
/// inconclusive result (`satisfiable == false`, `timed_out == true`)
/// once it is exceeded, checked only periodically (see
/// [`TIME_CHECK_INTERVAL`]) rather than after every node. `rng`
/// supplies the randomness [`select_var`] uses to break ties, and
/// `verbose` controls progress output: at verbose >= 1, "dfs" and the
/// configured time limit (if any) are printed before searching, and
/// "SAT", "UNSAT", or "UNKNOWN" (on timeout) are printed after.
pub fn run<R: Rng>(
    problem: &Problem,
    lists: &Lists,
    time_limit: Option<Duration>,
    rng: &mut R,
    verbose: i32,
) -> SolveResult {
    if verbose >= 1 {
        println!("dfs: {}", describe_params(time_limit));
    }

    // A problem with no variables can only contain empty clauses (no
    // literal can reference a variable beyond num_vars), each of
    // which is unsatisfiable by construction; guard this degenerate
    // case explicitly so select_var is never asked to choose a
    // variable that does not exist.
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

    let start_time = Instant::now();
    let mut stack: Vec<Assignment> = vec![assignment::new(problem.num_vars)];
    let mut num_nodes: usize = 0;

    while let Some(x) = stack.pop() {
        num_nodes += 1;
        if let Some(limit) = time_limit
            && num_nodes & TIME_CHECK_INTERVAL == 0
            && start_time.elapsed() >= limit
        {
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

        let i = select_var(problem, &x, rng);

        for &v in &[Value::False, Value::True] {
            let mut branch = x.clone();
            branch[i] = v;

            match bcp(problem, lists, &mut branch, i) {
                Status::Contra => continue,
                Status::Done => {
                    if verbose >= 1 {
                        println!("SAT");
                    }
                    return SolveResult {
                        satisfiable: true,
                        assignment: branch,
                        num_nodes,
                        timed_out: false,
                    };
                }
                Status::Ok => stack.push(branch),
            }
        }
    }

    if verbose >= 1 {
        println!("UNSAT");
    }
    SolveResult {
        satisfiable: false,
        assignment: assignment::new(problem.num_vars),
        num_nodes,
        timed_out: false,
    }
}

/// Applies boolean constraint propagation to partial assignment `x`,
/// which must already have variable `i` set to its just-chosen branch
/// value. It repeatedly finds clauses left with exactly one
/// unassigned, not-yet-satisfied literal and forces that literal
/// true, continuing until propagation settles or a contradiction is
/// found. `x` is modified in place.
pub fn bcp(problem: &Problem, lists: &Lists, x: &mut Assignment, i: usize) -> Status {
    let mut queue = vec![i];
    let mut head = 0;
    while head < queue.len() {
        let v = queue[head];
        head += 1;

        // falsified lists the clauses containing the literal of v
        // that just became false because of v's new assignment: those
        // are the only clauses whose status could have changed.
        let falsified = if x[v] == Value::True {
            &lists.negative[v]
        } else {
            &lists.positive[v]
        };

        for &c in falsified {
            let (satisfied, contradiction, unit_literal) = evaluate_clause(&problem.clauses[c], x);
            if contradiction {
                return Status::Contra;
            }
            if satisfied || unit_literal.is_none() {
                continue;
            }
            let literal = unit_literal.expect("checked above");
            let forced_var = cnf::literal_var(literal);
            x[forced_var] = if cnf::literal_is_negative(literal) {
                Value::False
            } else {
                Value::True
            };
            queue.push(forced_var);
        }
    }

    if x[1..].contains(&Value::Unassigned) {
        Status::Ok
    } else {
        Status::Done
    }
}

/// Examines `clause` under partial assignment `x` and reports:
/// whether it is already satisfied by some literal; whether it is a
/// contradiction (no unassigned literals, and none true); and, if it
/// has exactly one unassigned literal and is not satisfied, that
/// literal (the one `bcp` must now force true), or `None` otherwise.
fn evaluate_clause(clause: &Clause, x: &Assignment) -> (bool, bool, Option<Literal>) {
    let mut unassigned_count = 0;
    let mut last_unassigned = None;
    for &literal in clause {
        let value = x[cnf::literal_var(literal)];
        if value == Value::Unassigned {
            unassigned_count += 1;
            last_unassigned = Some(literal);
            continue;
        }
        if literal_is_true(literal, value) {
            return (true, false, None);
        }
    }
    match unassigned_count {
        0 => (false, true, None),
        1 => (false, false, last_unassigned),
        _ => (false, false, None),
    }
}

/// Returns whether `literal` evaluates to true when its variable
/// holds `value` (which must not be `Value::Unassigned`).
fn literal_is_true(literal: Literal, value: Value) -> bool {
    if cnf::literal_is_negative(literal) {
        value == Value::False
    } else {
        value == Value::True
    }
}

/// Chooses which unassigned variable of `problem` to branch on next,
/// given partial assignment `x`, following STAGE5.md's heuristic: for
/// every not-yet-satisfied clause, every currently unassigned variable
/// in it earns a share of weight `0.7^(n-2)`, where `n` is the number
/// of unassigned variables in that clause (`n >= 2` for every clause
/// reached after at least one round of BCP, since BCP would already
/// have propagated or rejected any clause with fewer unassigned
/// literals; the very first call, on the wholly unassigned root, is
/// the only exception, and the formula still produces a well-defined,
/// if unrepresentative, weight there). The variable with the highest
/// total score is selected; ties are broken uniformly at random using
/// `rng`.
pub fn select_var<R: Rng>(problem: &Problem, x: &Assignment, rng: &mut R) -> usize {
    let mut score = vec![0.0f64; problem.num_vars + 1];

    for clause in &problem.clauses {
        let mut satisfied = false;
        let mut unassigned_vars = Vec::new();
        for &literal in clause {
            let value = x[cnf::literal_var(literal)];
            if value == Value::Unassigned {
                unassigned_vars.push(cnf::literal_var(literal));
                continue;
            }
            if literal_is_true(literal, value) {
                satisfied = true;
                break;
            }
        }
        if satisfied {
            continue;
        }
        let weight = 0.7f64.powi(unassigned_vars.len() as i32 - 2);
        for v in unassigned_vars {
            score[v] += weight;
        }
    }

    let mut best_var: Option<usize> = None;
    let mut best_score = 0.0f64;
    let mut tie_count: u32 = 0;
    for v in 1..=problem.num_vars {
        if x[v] != Value::Unassigned {
            continue;
        }
        if best_var.is_none() || score[v] > best_score {
            best_var = Some(v);
            best_score = score[v];
            tie_count = 1;
        } else if score[v] == best_score {
            tie_count += 1;
            // Reservoir sampling: keep the new candidate with
            // probability 1/tie_count, so every tied candidate seen
            // so far remains equally likely to be selected.
            if rng.random_range(0..tie_count) == 0 {
                best_var = Some(v);
            }
        }
    }
    best_var.expect("at least one unassigned variable must exist when select_var is called")
}

/// Formats the configured time limit for the "dfs:" announcement
/// printed at verbose level 1.
fn describe_params(time_limit: Option<Duration>) -> String {
    match time_limit {
        Some(limit) => format!("time_limit_secs={}", limit.as_secs()),
        None => "(no limit)".to_string(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use rand::SeedableRng;
    use rand::rngs::StdRng;

    #[test]
    fn test_evaluate_clause_satisfied() {
        let mut x = assignment::new(2);
        x[1] = Value::True;
        let (satisfied, contradiction, unit) = evaluate_clause(&vec![1, -2], &x);
        assert!(satisfied);
        assert!(!contradiction);
        assert_eq!(unit, None);
    }

    #[test]
    fn test_evaluate_clause_contradiction() {
        let mut x = assignment::new(2);
        x[1] = Value::False;
        x[2] = Value::True;
        let (satisfied, contradiction, unit) = evaluate_clause(&vec![1, -2], &x);
        assert!(!satisfied);
        assert!(contradiction);
        assert_eq!(unit, None);
    }

    #[test]
    fn test_evaluate_clause_unit() {
        let mut x = assignment::new(2);
        x[1] = Value::False;
        let (satisfied, contradiction, unit) = evaluate_clause(&vec![1, -2], &x);
        assert!(!satisfied);
        assert!(!contradiction);
        assert_eq!(unit, Some(-2));
    }

    #[test]
    fn test_evaluate_clause_unresolved() {
        let x = assignment::new(3);
        let (satisfied, contradiction, unit) = evaluate_clause(&vec![1, -2, 3], &x);
        assert!(!satisfied);
        assert!(!contradiction);
        assert_eq!(unit, None);
    }

    #[test]
    fn test_bcp_propagates_unit_chain() {
        // 1 forces -2 true (via clause {-1, -2}) which forces 3 true
        // (via clause {2, 3}), completing the assignment.
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![-1, -2], vec![2, 3]],
        };
        let lists = crate::occurrence::build(&problem);
        let mut x = assignment::new(3);
        x[1] = Value::True;

        let status = bcp(&problem, &lists, &mut x, 1);
        assert_eq!(status, Status::Done);
        assert_eq!(x[2], Value::False);
        assert_eq!(x[3], Value::True);
    }

    #[test]
    fn test_bcp_detects_contradiction() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![-1, -2], vec![2, 3], vec![-3]],
        };
        let lists = crate::occurrence::build(&problem);
        let mut x = assignment::new(3);
        x[1] = Value::True;

        assert_eq!(bcp(&problem, &lists, &mut x, 1), Status::Contra);
    }

    #[test]
    fn test_bcp_leaves_partial_assignment_ok() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![1, 2, 3]],
        };
        let lists = crate::occurrence::build(&problem);
        let mut x = assignment::new(3);
        x[1] = Value::False;

        assert_eq!(bcp(&problem, &lists, &mut x, 1), Status::Ok);
        assert_eq!(x[2], Value::Unassigned);
        assert_eq!(x[3], Value::Unassigned);
    }

    #[test]
    fn test_select_var_prefers_shorter_unsatisfied_clauses() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![
                vec![1, 2],    // n=2, weight 0.7^0 = 1 each
                vec![3, 1, 2], // n=3, weight 0.7^1 = 0.7 each
            ],
        };
        let x = assignment::new(3);
        let mut rng = StdRng::seed_from_u64(1);

        // Variable 3 only appears in the 3-literal clause (score 0.7);
        // variables 1 and 2 appear in both (score 1.7 each), so
        // select_var must never choose 3.
        for _ in 0..20 {
            assert_ne!(select_var(&problem, &x, &mut rng), 3);
        }
    }

    #[test]
    fn test_select_var_only_returns_unassigned_variables() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1, 2]],
        };
        let mut x = assignment::new(2);
        x[1] = Value::True;
        let mut rng = StdRng::seed_from_u64(2);

        assert_eq!(select_var(&problem, &x, &mut rng), 2);
    }

    #[test]
    fn test_run_finds_satisfiable_formula() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![1, 2], vec![-1, 3], vec![-2, -3]],
        };
        let lists = crate::occurrence::build(&problem);
        let mut rng = StdRng::seed_from_u64(3);

        let result = run(&problem, &lists, None, &mut rng, 0);

        assert!(result.satisfiable);
        for (ci, clause) in problem.clauses.iter().enumerate() {
            let satisfied = clause
                .iter()
                .any(|&lit| assignment::literal_is_true(&result.assignment, lit));
            assert!(satisfied, "clause {ci} ({clause:?}) not satisfied");
        }
    }

    #[test]
    fn test_run_proves_unsatisfiable_formula() {
        // x1 AND NOT x1: trivially unsatisfiable.
        let problem = Problem {
            num_vars: 1,
            clauses: vec![vec![1], vec![-1]],
        };
        let lists = crate::occurrence::build(&problem);
        let mut rng = StdRng::seed_from_u64(4);

        let result = run(&problem, &lists, None, &mut rng, 0);

        assert!(!result.satisfiable);
        assert!(!result.timed_out);
    }

    /// Builds the standard CNF encoding of "num_pigeons pigeons cannot
    /// be placed into num_holes holes with no two pigeons sharing a
    /// hole", which is unsatisfiable whenever num_pigeons > num_holes.
    /// Variable (p-1)*num_holes+h represents "pigeon p is in hole h".
    fn pigeonhole_problem(num_pigeons: usize, num_holes: usize) -> Problem {
        let v = |p: usize, h: usize| -> Literal { ((p - 1) * num_holes + h) as Literal };

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

    #[test]
    fn test_run_proves_unsatisfiable_pigeonhole() {
        let problem = pigeonhole_problem(4, 3);
        let lists = crate::occurrence::build(&problem);
        let mut rng = StdRng::seed_from_u64(5);

        let result = run(&problem, &lists, None, &mut rng, 0);

        assert!(!result.satisfiable);
        assert!(!result.timed_out);
    }

    #[test]
    fn test_run_handles_zero_variable_problems() {
        let mut rng = StdRng::seed_from_u64(6);

        let sat_problem = Problem {
            num_vars: 0,
            clauses: vec![],
        };
        let result = run(
            &sat_problem,
            &crate::occurrence::build(&sat_problem),
            None,
            &mut rng,
            0,
        );
        assert!(result.satisfiable);

        let unsat_problem = Problem {
            num_vars: 0,
            clauses: vec![vec![]],
        };
        let result = run(
            &unsat_problem,
            &crate::occurrence::build(&unsat_problem),
            None,
            &mut rng,
            0,
        );
        assert!(!result.satisfiable);
    }

    #[test]
    fn test_run_respects_time_limit() {
        let problem = pigeonhole_problem(9, 8);
        let lists = crate::occurrence::build(&problem);
        let mut rng = StdRng::seed_from_u64(7);
        let tiny = Duration::from_nanos(1);

        let result = run(&problem, &lists, Some(tiny), &mut rng, 0);

        assert!(
            result.timed_out,
            "expected timed_out = true with a 1ns time limit"
        );
        assert!(!result.satisfiable);
    }
}
