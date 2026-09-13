//! Implements the depth-first search SAT solving algorithm used by
//! vibe_sat's "dfs" algorithm: a complete DPLL-style search using
//! boolean constraint propagation (BCP, via watched literals -- see
//! [`WatchState`] and STAGE9.md) and a choice of variable-selection
//! heuristics (see [`SelectVarVariant`]). Unlike the hill-climb-family
//! algorithms ([`crate::hillclimb`]), this search is complete: if it
//! exhausts its search space without finding a satisfying assignment,
//! the problem is proven UNSAT, not merely "not found yet".

use std::time::{Duration, Instant};

use rand::{Rng, RngExt};

use crate::assignment::{self, Assignment, Value};
use crate::cnf::{self, Clause, Literal, Problem};
use crate::occurrence::Lists;
use crate::preprocess;

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

/// Identifies which `select_var*` heuristic [`run`] should use to
/// pick the next branching variable.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum SelectVarVariant {
    /// The default heuristic from STAGE5.md: for every
    /// not-yet-satisfied clause, every unassigned variable in it earns
    /// `0.7^(n-2)` (`n` = that clause's unassigned literal count), and
    /// the highest-scoring variable is picked. It costs time
    /// proportional to the total size of the formula on every single
    /// node (it rescans every clause), in exchange for making a more
    /// informed choice that tends to keep the search tree small.
    #[default]
    Weighted,
    /// The cheaper alternative from STAGE6.md: picks the
    /// lowest-numbered still-unassigned variable, looking at no clause
    /// contents at all. This is the "static/lexicographic ordering"
    /// branching rule discussed in the SAT branching heuristic
    /// literature (e.g. J. Marques-Silva, "The Impact of Branching
    /// Heuristics in Propositional Satisfiability Algorithms," 1999)
    /// as the cheap baseline that smarter dynamic heuristics are
    /// compared against: it costs at most O(num_vars) per node with no
    /// clause scanning, but ignores problem structure entirely, which
    /// typically grows the search tree substantially.
    Fast,
}

/// How often the time limit is checked, in number of search nodes,
/// per STAGE5.md's suggestion ("Maybe only when num_nodes & 0xfff ==
/// 0"): checking the clock on every single node would add needless
/// overhead, since nodes are cheap and the clock only needs to be
/// checked often enough to respond to a time limit reasonably
/// promptly.
const TIME_CHECK_INTERVAL: usize = 0xfff;

/// Holds, for every clause, the two literals it is currently watching
/// (see Chaff: Moskewicz, Madigan, Zhao, Zhang & Malik, "Chaff:
/// Engineering an Efficient SAT Solver," DAC 2001). [`bcp`] only ever
/// has to look closely at a clause when one of these two literals
/// becomes false, instead of every clause containing that literal: a
/// clause "sheds" a watch away from an about-to-be-false literal onto
/// some other not-yet-false literal whenever it can, which is what
/// makes propagation cheap in the steady state.
///
/// A `WatchState` belongs to exactly one node of the search: each
/// branch gets its own clone (it derives [`Clone`]), since sibling
/// branches make different assignments and so may need different
/// literals watched. This keeps the search's existing "explicit stack
/// of self-contained nodes" structure from STAGE5.md intact, rather
/// than requiring the single shared, incrementally backtracked trail
/// a from-scratch CDCL implementation would normally use.
///
/// `watch[c]` is always exactly 2 distinct literals, which requires
/// every clause to have at least 2 literals; callers must guarantee
/// this (via an initial round of unit propagation removing any unit
/// or empty clauses) before calling [`new_watch_state`].
#[derive(Clone)]
struct WatchState {
    watch: Vec<[Literal; 2]>,
}

/// Builds a [`WatchState`] for `clauses`, choosing for every clause
/// two literals that are not false under `assignment`. Returns `None`
/// if some clause has fewer than two such literals (a contradiction,
/// given the precondition above; checked defensively rather than
/// assumed).
fn new_watch_state(clauses: &[Clause], assignment: &Assignment) -> Option<WatchState> {
    let mut watch = Vec::with_capacity(clauses.len());
    for clause in clauses {
        let first = choose_watch(clause, assignment, None)?;
        let second = choose_watch(clause, assignment, Some(first))?;
        watch.push([first, second]);
    }
    Some(WatchState { watch })
}

/// Scans `clause` for a literal that is not false under `assignment`
/// and is not equal to `avoid` (used when picking a clause's second
/// watch, to avoid re-picking the first).
fn choose_watch(
    clause: &Clause,
    assignment: &Assignment,
    avoid: Option<Literal>,
) -> Option<Literal> {
    clause
        .iter()
        .copied()
        .find(|&lit| Some(lit) != avoid && !is_false(lit, assignment))
}

/// Returns whether `literal` currently evaluates to false under
/// `assignment` (an `Unassigned` variable makes every literal on it
/// neither true nor false yet, so this returns false for those).
fn is_false(literal: Literal, assignment: &Assignment) -> bool {
    let value = assignment[cnf::literal_var(literal)];
    if value == Value::Unassigned {
        return false;
    }
    if cnf::literal_is_negative(literal) {
        value == Value::True
    } else {
        value == Value::False
    }
}

/// One entry of [`run`]'s explicit search stack: a partial assignment
/// together with the watched-literal state describing it. Every
/// branch gets its own independent `SearchNode` (via `WatchState`'s
/// `Clone`), matching STAGE5.md's original "stack of full
/// assignments" design -- STAGE9.md's watched literals speed up the
/// [`bcp`] call made when creating each node, not the overall search
/// structure.
struct SearchNode {
    assignment: Assignment,
    watch: WatchState,
}

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
/// `variant` selects which of [`select_var`]/[`select_var_fast_pick`]
/// is used to pick the branching variable at every node (see
/// [`SelectVarVariant`]).
///
/// If `time_limit` is `Some`, the search gives up and reports an
/// inconclusive result (`satisfiable == false`, `timed_out == true`)
/// once it is exceeded, checked only periodically (see
/// [`TIME_CHECK_INTERVAL`]) rather than after every node. `rng`
/// supplies the randomness the selected heuristic uses to break ties,
/// and `verbose` controls progress output: at verbose >= 1, "dfs" and
/// the configured time limit (if any) are printed before searching,
/// and "SAT", "UNSAT", or "UNKNOWN" (on timeout) are printed after.
pub fn run<R: Rng>(
    problem: &Problem,
    lists: &Lists,
    time_limit: Option<Duration>,
    variant: SelectVarVariant,
    rng: &mut R,
    verbose: i32,
) -> SolveResult {
    if verbose >= 1 {
        println!("dfs: {}", describe_params(time_limit, variant));
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

    // The watched-literal scheme (see WatchState) requires every
    // clause to have at least two literals: a unit clause has nothing
    // to "shed" its second watch onto. Bootstrap by unit-propagating
    // the root once, with the same routine crate::preprocess already
    // uses for exactly this purpose, before ever building watch
    // state. When --no-preprocessing is not given, Stage 8's
    // preprocessing has already done this (so this is a no-op); this
    // guarantees correctness either way.
    let mut clauses = problem.clauses.clone();
    let mut root_assignment = assignment::new(problem.num_vars);
    if preprocess::unit_propagate(&mut clauses, &mut root_assignment).0 {
        if verbose >= 1 {
            println!("UNSAT");
        }
        return SolveResult {
            satisfiable: false,
            assignment: assignment::new(problem.num_vars),
            num_nodes: 0,
            timed_out: false,
        };
    }
    let root_watch = match new_watch_state(&clauses, &root_assignment) {
        Some(ws) => ws,
        // Defensive: unit_propagate above should already rule this
        // out, since every surviving clause has at least one
        // unassigned literal (otherwise it would have been a unit
        // clause caught above, or a contradiction).
        None => {
            if verbose >= 1 {
                println!("UNSAT");
            }
            return SolveResult {
                satisfiable: false,
                assignment: assignment::new(problem.num_vars),
                num_nodes: 0,
                timed_out: false,
            };
        }
    };

    // working_problem wraps the (possibly bootstrap-simplified) clause
    // set for select_var/select_var_fast_pick, which only ever need
    // the clauses and variable count, not the original Problem value.
    // clauses moves in here; bcp below reads it back out via
    // working_problem.clauses, rather than keeping a second owner.
    let working_problem = Problem {
        num_vars: problem.num_vars,
        clauses,
    };

    let start_time = Instant::now();
    let mut stack: Vec<SearchNode> = vec![SearchNode {
        assignment: root_assignment,
        watch: root_watch,
    }];
    let mut num_nodes: usize = 0;

    while let Some(node) = stack.pop() {
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

        let i = match variant {
            SelectVarVariant::Fast => select_var_fast_pick(&working_problem, &node.assignment),
            SelectVarVariant::Weighted => select_var(&working_problem, &node.assignment, rng),
        };

        for &v in &[Value::False, Value::True] {
            let mut branch_assignment = node.assignment.clone();
            branch_assignment[i] = v;
            let mut branch_watch = node.watch.clone();

            match bcp(
                &working_problem.clauses,
                lists,
                &mut branch_watch,
                &mut branch_assignment,
                i,
            ) {
                Status::Contra => continue,
                Status::Done => {
                    if verbose >= 1 {
                        println!("SAT");
                    }
                    return SolveResult {
                        satisfiable: true,
                        assignment: branch_assignment,
                        num_nodes,
                        timed_out: false,
                    };
                }
                Status::Ok => stack.push(SearchNode {
                    assignment: branch_assignment,
                    watch: branch_watch,
                }),
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
/// value, using `ws`'s watched literals to find only the clauses that
/// might need attention as a result. `x` and `ws` are both modified
/// in place. `lists` is the static (never mutated) per-variable
/// occurrence index from Stage 2, used here only to enumerate the
/// *candidate* clauses to examine when a literal becomes false --
/// most of them are dismissed in O(1) because they are not currently
/// watching that literal; only the ones that are get the deeper look
/// a plain occurrence-list scan would have given every candidate.
fn bcp(
    clauses: &[Clause],
    lists: &Lists,
    ws: &mut WatchState,
    x: &mut Assignment,
    i: usize,
) -> Status {
    let mut queue = vec![i];
    let mut head = 0;
    while head < queue.len() {
        let v = queue[head];
        head += 1;

        // falsified_literal is the literal on v that just became
        // false because of v's new assignment; candidates lists every
        // clause that mentions it (only some of which are actually
        // watching it right now).
        let (falsified_literal, candidates) = if x[v] == Value::True {
            (-(v as Literal), &lists.negative[v])
        } else {
            (v as Literal, &lists.positive[v])
        };

        for &c in candidates {
            let watch = ws.watch[c];
            let other_watch = if watch[0] == falsified_literal {
                watch[1]
            } else if watch[1] == falsified_literal {
                watch[0]
            } else {
                continue; // this clause isn't watching the falsified literal
            };

            if let Some(replacement) = choose_watch(&clauses[c], x, Some(other_watch)) {
                if watch[0] == falsified_literal {
                    ws.watch[c][0] = replacement;
                } else {
                    ws.watch[c][1] = replacement;
                }
                continue;
            }

            // No replacement: other_watch is the clause's only
            // literal that isn't currently false.
            if is_false(other_watch, x) {
                return Status::Contra;
            }
            let forced_var = cnf::literal_var(other_watch);
            if x[forced_var] == Value::Unassigned {
                x[forced_var] = if cnf::literal_is_negative(other_watch) {
                    Value::False
                } else {
                    Value::True
                };
                queue.push(forced_var);
            }
            // Otherwise other_watch is already true: the clause is
            // satisfied through it, and there is nothing to do.
        }
    }

    if x[1..].contains(&Value::Unassigned) {
        Status::Ok
    } else {
        Status::Done
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

/// Implements [`SelectVarVariant::Fast`]: returns the lowest-numbered
/// variable that is still `Unassigned` in `x`, without examining any
/// clause. There is nothing to break ties between (the choice is
/// always unique), so unlike [`select_var`] this needs no random
/// source.
pub fn select_var_fast_pick(problem: &Problem, x: &Assignment) -> usize {
    (1..=problem.num_vars)
        .find(|&v| x[v] == Value::Unassigned)
        .expect("at least one unassigned variable must exist when select_var_fast_pick is called")
}

/// Formats the configured time limit and `SelectVar` variant for the
/// "dfs:" announcement printed at verbose level 1.
fn describe_params(time_limit: Option<Duration>, variant: SelectVarVariant) -> String {
    let variant_code = match variant {
        SelectVarVariant::Weighted => 0,
        SelectVarVariant::Fast => 1,
    };
    match time_limit {
        Some(limit) => format!(
            "select_var={variant_code} time_limit_secs={}",
            limit.as_secs()
        ),
        None => format!("select_var={variant_code}"),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use rand::SeedableRng;
    use rand::rngs::StdRng;

    #[test]
    fn test_choose_watch_skips_false_literals() {
        let mut x = assignment::new(3);
        x[1] = Value::False;
        assert_eq!(choose_watch(&vec![1, 2, 3], &x, None), Some(2));
    }

    #[test]
    fn test_choose_watch_honors_avoid() {
        let x = assignment::new(2);
        assert_eq!(choose_watch(&vec![1, 2], &x, Some(1)), Some(2));
    }

    #[test]
    fn test_choose_watch_fails_when_none_available() {
        let mut x = assignment::new(2);
        x[1] = Value::False;
        x[2] = Value::False;
        assert_eq!(choose_watch(&vec![1, 2], &x, None), None);
    }

    #[test]
    fn test_new_watch_state_picks_two_non_false_literals() {
        let clauses = vec![vec![1, 2, 3]];
        let mut x = assignment::new(3);
        x[1] = Value::False;

        let ws = new_watch_state(&clauses, &x).expect("expected two non-false literals");
        assert_eq!(ws.watch.len(), 1);
        assert!(ws.watch[0].contains(&2));
        assert!(ws.watch[0].contains(&3));
    }

    #[test]
    fn test_new_watch_state_fails_on_contradiction() {
        // Only one literal (-2) is not false, so no second watch exists.
        let clauses = vec![vec![1, -2]];
        let mut x = assignment::new(2);
        x[1] = Value::False;
        x[2] = Value::True;

        assert!(new_watch_state(&clauses, &x).is_none());
    }

    #[test]
    fn test_clone_watch_state_is_independent() {
        let clauses = vec![vec![1, 2]];
        let x = assignment::new(2);
        let ws = new_watch_state(&clauses, &x).expect("expected a valid watch state");
        let mut clone = ws.clone();
        clone.watch[0][0] = 99;
        assert_ne!(ws.watch[0][0], clone.watch[0][0]);
    }

    #[test]
    fn test_bcp_moves_watch_away_from_falsified_literal() {
        let clauses = vec![vec![1, 2, 3]];
        let lists = crate::occurrence::build(&Problem {
            num_vars: 3,
            clauses: clauses.clone(),
        });
        let mut x = assignment::new(3);
        let mut ws = new_watch_state(&clauses, &x).expect("expected a valid watch state");
        // Force the clause to watch {1, 2} explicitly, then falsify 1;
        // bcp must shed that watch onto 3 rather than forcing anything.
        ws.watch[0] = [1, 2];
        x[1] = Value::False;

        assert_eq!(bcp(&clauses, &lists, &mut ws, &mut x, 1), Status::Ok);
        assert!(ws.watch[0].contains(&3));
        assert_eq!(x[2], Value::Unassigned);
        assert_eq!(x[3], Value::Unassigned);
    }

    #[test]
    fn test_bcp_propagates_unit_chain() {
        // 1 forces -2 true (via clause {-1, -2}) which forces 3 true
        // (via clause {2, 3}), completing the assignment.
        let clauses = vec![vec![-1, -2], vec![2, 3]];
        let problem = Problem {
            num_vars: 3,
            clauses: clauses.clone(),
        };
        let lists = crate::occurrence::build(&problem);
        let mut x = assignment::new(3);
        let mut ws = new_watch_state(&clauses, &x).expect("expected a valid watch state");
        x[1] = Value::True;

        let status = bcp(&clauses, &lists, &mut ws, &mut x, 1);
        assert_eq!(status, Status::Done);
        assert_eq!(x[2], Value::False);
        assert_eq!(x[3], Value::True);
    }

    #[test]
    fn test_bcp_detects_contradiction() {
        // 1=True forces 2=False (via {-1,-2}), which forces 3=True
        // (via {2,3}); clauses {-3,-4} and {-3,4} are then jointly
        // unsatisfiable regardless of 4's value -- a genuine
        // contradiction discovered purely through watch movement,
        // with no pre-existing unit clause (every clause here has at
        // least two literals, satisfying new_watch_state's
        // precondition).
        let clauses = vec![vec![-1, -2], vec![2, 3], vec![-3, -4], vec![-3, 4]];
        let problem = Problem {
            num_vars: 4,
            clauses: clauses.clone(),
        };
        let lists = crate::occurrence::build(&problem);
        let mut x = assignment::new(4);
        let mut ws = new_watch_state(&clauses, &x).expect("expected a valid watch state");
        x[1] = Value::True;

        assert_eq!(bcp(&clauses, &lists, &mut ws, &mut x, 1), Status::Contra);
    }

    #[test]
    fn test_bcp_leaves_partial_assignment_ok() {
        let clauses = vec![vec![1, 2, 3]];
        let problem = Problem {
            num_vars: 3,
            clauses: clauses.clone(),
        };
        let lists = crate::occurrence::build(&problem);
        let mut x = assignment::new(3);
        let mut ws = new_watch_state(&clauses, &x).expect("expected a valid watch state");
        x[1] = Value::False;

        assert_eq!(bcp(&clauses, &lists, &mut ws, &mut x, 1), Status::Ok);
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
    fn test_select_var_fast_pick_returns_lowest_unassigned() {
        let problem = Problem {
            num_vars: 4,
            clauses: vec![vec![1, 4]],
        };
        let mut x = assignment::new(4);
        x[1] = Value::True;
        x[2] = Value::False;

        assert_eq!(select_var_fast_pick(&problem, &x), 3);
    }

    #[test]
    #[should_panic]
    fn test_select_var_fast_pick_panics_when_all_assigned() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![],
        };
        let mut x = assignment::new(2);
        x[1] = Value::True;
        x[2] = Value::False;

        select_var_fast_pick(&problem, &x);
    }

    #[test]
    fn test_run_with_fast_select_var_finds_satisfiable_formula() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![1, 2], vec![-1, 3], vec![-2, -3]],
        };
        let lists = crate::occurrence::build(&problem);
        let mut rng = StdRng::seed_from_u64(8);

        let result = run(&problem, &lists, None, SelectVarVariant::Fast, &mut rng, 0);

        assert!(result.satisfiable);
        for (ci, clause) in problem.clauses.iter().enumerate() {
            let satisfied = clause
                .iter()
                .any(|&lit| assignment::literal_is_true(&result.assignment, lit));
            assert!(satisfied, "clause {ci} ({clause:?}) not satisfied");
        }
    }

    #[test]
    fn test_run_with_fast_select_var_proves_unsatisfiable_pigeonhole() {
        let problem = pigeonhole_problem(4, 3);
        let lists = crate::occurrence::build(&problem);
        let mut rng = StdRng::seed_from_u64(9);

        let result = run(&problem, &lists, None, SelectVarVariant::Fast, &mut rng, 0);

        assert!(!result.satisfiable);
        assert!(!result.timed_out);
    }

    #[test]
    fn test_run_finds_satisfiable_formula() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![1, 2], vec![-1, 3], vec![-2, -3]],
        };
        let lists = crate::occurrence::build(&problem);
        let mut rng = StdRng::seed_from_u64(3);

        let result = run(
            &problem,
            &lists,
            None,
            SelectVarVariant::Weighted,
            &mut rng,
            0,
        );

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

        let result = run(
            &problem,
            &lists,
            None,
            SelectVarVariant::Weighted,
            &mut rng,
            0,
        );

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

        let result = run(
            &problem,
            &lists,
            None,
            SelectVarVariant::Weighted,
            &mut rng,
            0,
        );

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
            SelectVarVariant::Weighted,
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
            SelectVarVariant::Weighted,
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

        let result = run(
            &problem,
            &lists,
            Some(tiny),
            SelectVarVariant::Weighted,
            &mut rng,
            0,
        );

        assert!(
            result.timed_out,
            "expected timed_out = true with a 1ns time limit"
        );
        assert!(!result.satisfiable);
    }
}
