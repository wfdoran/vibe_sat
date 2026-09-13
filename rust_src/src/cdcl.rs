//! Implements conflict-driven clause learning with non-chronological
//! backtracking (CDCL), per STAGE11.md: Marques-Silva & Sakallah,
//! "GRASP: A Search Algorithm for Propositional Satisfiability," IEEE
//! Trans. Computers, 1999 (originally 1996), and Moskewicz, Madigan,
//! Zhao, Zhang & Malik, "Chaff: Engineering an Efficient SAT Solver,"
//! DAC 2001.
//!
//! Unlike [`crate::dfs`], which explores an explicit stack of
//! independent, fully cloned partial assignments and simply abandons
//! a branch that reaches a contradiction, `cdcl` maintains a single,
//! persistent assignment trail with real backtracking. When
//! propagation reaches a contradiction, it walks the implication
//! graph backward (the reason clause of each forced literal,
//! transitively) to derive a new clause that explains the conflict --
//! the "first unique implication point" (first-UIP) scheme -- adds
//! that learned clause to the formula, and jumps directly back to the
//! decision level where the learned clause becomes a unit clause,
//! rather than just retrying the other branch one level up. This is
//! what makes watched literals (STAGE9.md) pay for themselves: BCP
//! now runs once per conflict as well as once per decision, and a
//! persistent trail (rather than dfs's per-branch clones) is exactly
//! what watched literals were designed to support with no extra
//! bookkeeping on backtrack (see [`backtrack_to`]'s doc comment).
//!
//! Per STAGE11.md, `dfs` is left as-is; `cdcl` is a wholly separate
//! algorithm selectable via `--algorithm=cdcl`/`-a cdcl`, sharing
//! dfs's `SelectVar` heuristics ([`crate::dfs::select_var`]/
//! [`crate::dfs::select_var_fast_pick`]) but nothing else. Unlike
//! [`crate::dfs::run`], this module is written as free functions
//! operating on loose local state (rather than a struct with
//! methods), which sidesteps Rust's borrow checker entirely: every
//! piece of state below is genuinely just a separate local variable
//! in [`run`], so passing disjoint pieces of it (some by mutable
//! reference, some not) into helper functions is always
//! straightforward, unlike it would be if they were all fields of one
//! shared struct being threaded through `&mut self` methods.
use std::time::{Duration, Instant};

use rand::Rng;

use crate::assignment::{self, Assignment, Value};
use crate::cnf::{self, Clause, Literal, Problem};
use crate::dfs::{SelectVarVariant, select_var, select_var_fast_pick};
use crate::occurrence::{self, Lists};
use crate::preprocess;

/// How often the time limit is checked, in number of decisions plus
/// conflicts processed, matching [`crate::dfs::TIME_CHECK_INTERVAL`]
/// and for the same reason: checking the clock on every single step
/// would add needless overhead.
const TIME_CHECK_INTERVAL: usize = 0xfff;

/// Describes the outcome of a CDCL search.
pub struct SolveResult {
    /// Whether a satisfying assignment was found.
    pub satisfiable: bool,
    /// The satisfying assignment; only meaningful if `satisfiable`.
    pub assignment: Assignment,
    /// Number of branching decisions made.
    #[allow(dead_code)]
    pub num_decisions: usize,
    /// Number of conflicts encountered (= number of clauses learned).
    #[allow(dead_code)]
    pub num_conflicts: usize,
    /// True if the search was abandoned due to the time limit, rather
    /// than exhausting the search space.
    #[allow(dead_code)]
    pub timed_out: bool,
}

/// Performs a CDCL search: repeatedly propagate, and on a conflict,
/// learn a clause and backjump; on reaching a fixpoint with no
/// conflict, either the assignment is complete (satisfiable) or a new
/// variable is chosen to branch on (a decision, always tried `False`
/// first -- see the decision step below). If propagation ever
/// conflicts while at decision level 0 (nothing left to backjump to),
/// the problem is proven unsatisfiable.
///
/// `variant` selects which of [`crate::dfs::select_var`]/
/// [`crate::dfs::select_var_fast_pick`] is used to pick the branching
/// variable at every decision (see [`SelectVarVariant`]); per
/// STAGE11.md, this is the only algorithm parameter `cdcl` currently
/// accepts, same as `dfs`.
///
/// If `time_limit` is `Some`, the search gives up and reports an
/// inconclusive result (`satisfiable == false`, `timed_out == true`)
/// once it is exceeded, checked only periodically (see
/// [`TIME_CHECK_INTERVAL`]). `rng` supplies the randomness
/// `select_var` uses to break ties, and `verbose` controls progress
/// output, matching [`crate::dfs::run`].
pub fn run<R: Rng>(
    problem: &Problem,
    time_limit: Option<Duration>,
    variant: SelectVarVariant,
    rng: &mut R,
    verbose: i32,
) -> SolveResult {
    if verbose >= 1 {
        println!("cdcl: {}", describe_params(time_limit, variant));
    }

    // A problem with no variables can only contain empty clauses (no
    // literal can reference a variable beyond num_vars), each of
    // which is unsatisfiable by construction; guard this degenerate
    // case explicitly, matching dfs::run.
    if problem.num_vars == 0 {
        let satisfiable = problem.clauses.is_empty();
        if verbose >= 1 {
            println!("{}", if satisfiable { "SAT" } else { "UNSAT" });
        }
        return SolveResult {
            satisfiable,
            assignment: assignment::new(0),
            num_decisions: 0,
            num_conflicts: 0,
            timed_out: false,
        };
    }

    let Some((mut working_problem, mut watch, mut x)) = bootstrap(problem) else {
        if verbose >= 1 {
            println!("UNSAT");
        }
        return SolveResult {
            satisfiable: false,
            assignment: assignment::new(problem.num_vars),
            num_decisions: 0,
            num_conflicts: 0,
            timed_out: false,
        };
    };

    let mut lists = occurrence::build(&working_problem);
    let mut level = vec![0usize; problem.num_vars + 1];
    let mut reason: Vec<Option<usize>> = vec![None; problem.num_vars + 1];
    let mut trail: Vec<usize> = Vec::new();
    let mut trail_lim: Vec<usize> = vec![0];
    let mut current_level = 0usize;
    let mut q_head = 0usize;
    let mut seen = vec![false; problem.num_vars + 1];
    let mut num_decisions = 0usize;
    let mut num_conflicts = 0usize;

    let start_time = Instant::now();
    let mut step: usize = 0;

    loop {
        step += 1;
        if let Some(limit) = time_limit
            && step & TIME_CHECK_INTERVAL == 0
            && start_time.elapsed() >= limit
        {
            if verbose >= 1 {
                println!("UNKNOWN");
            }
            return SolveResult {
                satisfiable: false,
                assignment: assignment::new(problem.num_vars),
                num_decisions,
                num_conflicts,
                timed_out: true,
            };
        }

        let confl = propagate(
            &working_problem.clauses,
            &lists,
            &mut watch,
            &mut x,
            &mut trail,
            &mut q_head,
            current_level,
            &mut level,
            &mut reason,
        );

        if let Some(c) = confl {
            num_conflicts += 1;
            if current_level == 0 {
                if verbose >= 1 {
                    println!("UNSAT");
                }
                return SolveResult {
                    satisfiable: false,
                    assignment: assignment::new(problem.num_vars),
                    num_decisions,
                    num_conflicts,
                    timed_out: false,
                };
            }

            let (learned, backtrack_level) = analyze(
                c,
                &working_problem.clauses,
                &trail,
                &level,
                &reason,
                &x,
                &mut seen,
                current_level,
            );
            backtrack_to(
                backtrack_level,
                &mut trail,
                &mut trail_lim,
                &mut x,
                &mut q_head,
                &mut current_level,
            );
            let new_clause = add_learned_clause(
                &learned,
                &mut working_problem.clauses,
                &mut lists,
                &mut watch,
                &level,
            );
            assign_literal(
                learned[0],
                backtrack_level,
                new_clause,
                &mut x,
                &mut level,
                &mut reason,
                &mut trail,
            );
            continue;
        }

        if !x[1..].contains(&Value::Unassigned) {
            if verbose >= 1 {
                println!("SAT");
            }
            return SolveResult {
                satisfiable: true,
                assignment: x,
                num_decisions,
                num_conflicts,
                timed_out: false,
            };
        }

        // Decide: pick the next branching variable (see
        // SelectVarVariant; this is the same heuristic dfs uses, and
        // -- since working_problem.clauses grows to include learned
        // clauses -- the Weighted variant does see learned clauses
        // when scoring, since it scans every not-yet-satisfied clause
        // in working_problem.clauses; Fast ignores clause contents
        // entirely either way). Always try False first, matching the
        // order dfs's branch loop uses; CDCL doesn't get to try both
        // polarities at one level the way dfs does -- if False turns
        // out wrong, conflict analysis is what corrects it, by
        // deriving a clause that forces True once it backjumps here
        // again.
        num_decisions += 1;
        let v = match variant {
            SelectVarVariant::Fast => select_var_fast_pick(&working_problem, &x),
            SelectVarVariant::Weighted => select_var(&working_problem, &x, rng),
        };
        current_level += 1;
        trail_lim.push(trail.len());
        assign_literal(
            -(v as Literal),
            current_level,
            None,
            &mut x,
            &mut level,
            &mut reason,
            &mut trail,
        );
    }
}

/// Builds the initial bootstrapped clause set, watch state, and
/// assignment for `problem`: a defensive round of unit propagation
/// (see [`crate::dfs::run`]'s identical bootstrap step for why this is
/// needed even though Stage 8's preprocessing already does this by
/// default), followed by initial watch state for whatever clauses
/// survive it. Returns `None` if this bootstrap alone already proves
/// `problem` unsatisfiable.
fn bootstrap(problem: &Problem) -> Option<(Problem, Vec<[Literal; 2]>, Assignment)> {
    let mut clauses = problem.clauses.clone();
    let mut x = assignment::new(problem.num_vars);
    if preprocess::unit_propagate(&mut clauses, &mut x).0 {
        return None;
    }

    let mut watch = Vec::with_capacity(clauses.len());
    for clause in &clauses {
        let first = choose_watch(clause, &x, None)?;
        let second = choose_watch(clause, &x, Some(first))?;
        watch.push([first, second]);
    }

    Some((
        Problem {
            num_vars: problem.num_vars,
            clauses,
        },
        watch,
        x,
    ))
}

/// Records `lit` as true (setting its variable's value, decision
/// level, and reason accordingly) and appends it to `trail`.
fn assign_literal(
    lit: Literal,
    level_now: usize,
    reason_now: Option<usize>,
    x: &mut Assignment,
    level: &mut [usize],
    reason: &mut [Option<usize>],
    trail: &mut Vec<usize>,
) {
    let v = cnf::literal_var(lit);
    x[v] = if cnf::literal_is_negative(lit) {
        Value::False
    } else {
        Value::True
    };
    level[v] = level_now;
    reason[v] = reason_now;
    trail.push(v);
}

/// Applies boolean constraint propagation via watched literals (see
/// STAGE9.md's `bcp`, which this mirrors closely) starting from
/// wherever it last left off (`*q_head`), continuing until either the
/// trail is exhausted (no conflict: returns `None`) or some clause
/// becomes fully falsified, in which case that clause's index is
/// returned as the conflict.
///
/// This takes each piece of solver state as its own parameter rather
/// than bundling them into a struct deliberately (see the module doc
/// comment): a struct threaded through `&mut self` would need the
/// borrow checker to prove `lists` (borrowed immutably, for
/// `candidates`) and `watch`/`x`/`trail` (borrowed mutably) never
/// alias, which it cannot do across a method boundary the way it can
/// across a plain function's independent parameters.
#[allow(clippy::too_many_arguments)]
fn propagate(
    clauses: &[Clause],
    lists: &Lists,
    watch: &mut [[Literal; 2]],
    x: &mut Assignment,
    trail: &mut Vec<usize>,
    q_head: &mut usize,
    current_level: usize,
    level: &mut [usize],
    reason: &mut [Option<usize>],
) -> Option<usize> {
    while *q_head < trail.len() {
        let v = trail[*q_head];
        *q_head += 1;

        let (falsified_literal, candidates): (Literal, &Vec<usize>) = if x[v] == Value::True {
            (-(v as Literal), &lists.negative[v])
        } else {
            (v as Literal, &lists.positive[v])
        };

        for &c in candidates {
            let w = watch[c];
            let other_watch = if w[0] == falsified_literal {
                w[1]
            } else if w[1] == falsified_literal {
                w[0]
            } else {
                continue; // this clause isn't watching the falsified literal
            };

            if let Some(replacement) = choose_watch(&clauses[c], x, Some(other_watch)) {
                if w[0] == falsified_literal {
                    watch[c][0] = replacement;
                } else {
                    watch[c][1] = replacement;
                }
                continue;
            }

            // No replacement: other_watch is the clause's only
            // literal that isn't currently false.
            if is_false(other_watch, x) {
                return Some(c); // conflict: clause c is now fully false
            }
            let forced_var = cnf::literal_var(other_watch);
            if x[forced_var] == Value::Unassigned {
                assign_literal(other_watch, current_level, Some(c), x, level, reason, trail);
            }
            // Otherwise other_watch is already true: the clause is
            // satisfied through it, and there is nothing to do.
        }
    }
    None
}

/// Walks the implication graph backward from the clause at index
/// `confl` (which [`propagate`] just found to be fully false) to
/// derive a learned clause via first-UIP resolution: repeatedly
/// resolve the current clause against the reason of the
/// most-recently-assigned still-unresolved literal at the conflict's
/// own decision level, until exactly one such literal remains -- the
/// "first unique implication point." That literal's negation becomes
/// the learned clause's asserting literal (returned first in the
/// result); every other literal collected along the way is already
/// false at some level below the conflict's, which is exactly what
/// makes the learned clause a unit clause (modulo the asserting
/// literal) the moment the search backjumps to the returned
/// `backtrack_level`, the highest level among those other literals
/// (0 if there are none).
///
/// This is the standard GRASP/Chaff conflict analysis (see the module
/// doc comment for references); level-0 literals are omitted
/// entirely, since they are permanent facts that can never become
/// unassigned again and so need no antecedent recorded in the learned
/// clause. `seen` is scratch space, reset at the start of every call.
#[allow(clippy::too_many_arguments)]
fn analyze(
    confl: usize,
    clauses: &[Clause],
    trail: &[usize],
    level: &[usize],
    reason: &[Option<usize>],
    x: &Assignment,
    seen: &mut [bool],
    current_level: usize,
) -> (Clause, usize) {
    for s in seen.iter_mut() {
        *s = false;
    }

    let mut p: Option<Literal> = None;
    let mut counter: i64 = 0;
    let mut trail_idx: i64 = trail.len() as i64 - 1;
    let mut reason_clause = confl;
    let mut learned: Clause = Vec::new();

    loop {
        for &lit in &clauses[reason_clause] {
            if let Some(pl) = p
                && cnf::literal_var(lit) == cnf::literal_var(pl)
            {
                continue;
            }
            let v = cnf::literal_var(lit);
            if seen[v] || level[v] == 0 {
                continue;
            }
            seen[v] = true;
            if level[v] == current_level {
                counter += 1;
            } else {
                learned.push(lit);
            }
        }

        let mut v;
        loop {
            v = trail[trail_idx as usize];
            trail_idx -= 1;
            if seen[v] {
                break;
            }
        }
        p = Some(literal_assigned_true(v, x));
        counter -= 1;
        if counter == 0 {
            break;
        }
        reason_clause = reason[v].expect(
            "cdcl: analyze reached a variable with no reason while literals of the current level remain unresolved",
        );
    }

    let asserting = -p.expect("p is always set by the time the loop above breaks");
    let mut result = vec![asserting];
    result.extend(learned);

    let mut backtrack_level = 0;
    for &lit in &result[1..] {
        let lv = level[cnf::literal_var(lit)];
        if lv > backtrack_level {
            backtrack_level = lv;
        }
    }
    (result, backtrack_level)
}

/// Returns the literal on variable `v` that evaluates to true under
/// `x` (`v` must not be `Unassigned` in `x`).
fn literal_assigned_true(v: usize, x: &Assignment) -> Literal {
    if x[v] == Value::True {
        v as Literal
    } else {
        -(v as Literal)
    }
}

/// Undoes every assignment made after decision level `level_target`,
/// resetting the trail, trail limits, and propagation queue
/// accordingly. Unlike dfs's per-branch clones, `cdcl`'s watch state
/// is never cloned or explicitly restored on backtrack: a watch
/// remains valid as long as it isn't watching a literal that's
/// currently false, and backtracking only ever turns assigned
/// literals back into unassigned ones -- it can never turn a
/// non-false literal into a false one -- so every watch already in
/// place is still a legal watch after backtracking, with nothing to
/// undo. This is precisely the property that makes watched literals
/// cheap under non-chronological backtracking, and is why `cdcl` uses
/// a single persistent trail instead of dfs's cloned-per-branch
/// approach (see STAGE9.md's `WatchState` doc comment for the tension
/// this resolves).
fn backtrack_to(
    level_target: usize,
    trail: &mut Vec<usize>,
    trail_lim: &mut Vec<usize>,
    x: &mut Assignment,
    q_head: &mut usize,
    current_level: &mut usize,
) {
    let cut = trail_lim[level_target + 1];
    for &v in &trail[cut..] {
        x[v] = Value::Unassigned;
    }
    trail.truncate(cut);
    trail_lim.truncate(level_target + 1);
    *current_level = level_target;
    *q_head = trail.len();
}

/// Appends `learned` to the clause database and extends the
/// occurrence lists and watch state for it, unless it's a unit clause
/// (length 1): a unit clause has only one literal, which has nowhere
/// to shed a second watch onto, and needs none anyway -- `analyze`'s
/// `backtrack_level` is always 0 for a unit learned clause, so its
/// (asserting) literal is about to become a permanent level-0 fact,
/// exactly like a variable fixed by the bootstrap unit propagation in
/// [`bootstrap`]. Returns the new clause's index, or `None` if none
/// was stored.
fn add_learned_clause(
    learned: &Clause,
    clauses: &mut Vec<Clause>,
    lists: &mut Lists,
    watch: &mut Vec<[Literal; 2]>,
    level: &[usize],
) -> Option<usize> {
    if learned.len() == 1 {
        return None;
    }

    let idx = clauses.len();
    clauses.push(learned.clone());
    for &lit in learned {
        let v = cnf::literal_var(lit);
        if cnf::literal_is_negative(lit) {
            lists.negative[v].push(idx);
        } else {
            lists.positive[v].push(idx);
        }
    }

    // learned[0] is the asserting literal, currently unassigned;
    // watch it directly (choose_watch would find it too, but it's
    // about to be assigned true by the caller regardless). The second
    // watch is whichever other literal has the highest decision
    // level, since that is the one that will become unassigned
    // soonest on some future backtrack, keeping this watch valid the
    // longest before it needs to shed anywhere.
    let mut best = 1;
    for i in 2..learned.len() {
        if level[cnf::literal_var(learned[i])] > level[cnf::literal_var(learned[best])] {
            best = i;
        }
    }
    watch.push([learned[0], learned[best]]);
    Some(idx)
}

/// Scans `clause` for a literal that is not false under `assignment`
/// and is not equal to `avoid` (used when picking a clause's second
/// watch, to avoid re-picking the first). Identical in spirit to
/// STAGE9.md's `dfs::choose_watch`; duplicated here (rather than
/// exported from `crate::dfs`) since STAGE11.md asks that dfs be left
/// as it is.
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

/// Formats the configured time limit and `SelectVar` variant for the
/// "cdcl:" announcement printed at verbose level 1.
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
        let mut x = assignment::new(2);
        x[1] = Value::False;
        assert_eq!(choose_watch(&vec![1, 2], &x, None), Some(2));
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
    fn test_bootstrap_detects_contradiction() {
        let problem = Problem {
            num_vars: 1,
            clauses: vec![vec![1], vec![-1]],
        };
        assert!(bootstrap(&problem).is_none());
    }

    #[test]
    fn test_propagate_propagates_unit_chain() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![-1, -2], vec![2, 3]],
        };
        let (working_problem, mut watch, mut x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let lists = occurrence::build(&working_problem);
        let mut level = vec![0usize; 4];
        let mut reason: Vec<Option<usize>> = vec![None; 4];
        let mut trail: Vec<usize> = Vec::new();
        let mut q_head = 0usize;

        assign_literal(1, 1, None, &mut x, &mut level, &mut reason, &mut trail);
        let confl = propagate(
            &working_problem.clauses,
            &lists,
            &mut watch,
            &mut x,
            &mut trail,
            &mut q_head,
            1,
            &mut level,
            &mut reason,
        );

        assert_eq!(confl, None);
        assert_eq!(x[2], Value::False);
        assert_eq!(x[3], Value::True);
    }

    /// Exercises the trickiest part of CDCL end to end against a
    /// hand-verified example: clauses {-2,4} and {-2,-4} alone force
    /// x2 = False regardless of any other variable, but the conflict
    /// that exposes this is only reached after deciding x1 = False
    /// and having that decision propagate x2 = True via clause
    /// {1,2}. First-UIP resolution must still recognize that x1's
    /// decision is irrelevant to the actual contradiction and learn
    /// the unit clause {-2} (backtracking all the way to level 0),
    /// not some clause that (correctly, but needlessly) also mentions
    /// x1.
    ///
    /// This was verified by hand, tracing propagate/analyze's exact
    /// execution order against this formula, before being written
    /// down here: with x1 = False, clause {1,2} forces x2 = True; x2
    /// = True then forces x4 = True via {-2,4}; x4 = True then
    /// falsifies {-2,-4} outright. Resolving that conflict against
    /// x4's reason ({-2,4}) immediately reduces the current-level
    /// literal count to just x2 (the first UIP), independent of x1
    /// (whose own reason, {1,2}, is never even inspected).
    #[test]
    fn test_analyze_derives_unit_clause_independent_of_decision() {
        let problem = Problem {
            num_vars: 4,
            clauses: vec![
                vec![1, 2],
                vec![-1, 3],
                vec![-1, -3],
                vec![-2, 4],
                vec![-2, -4],
            ],
        };
        let (working_problem, mut watch, mut x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let lists = occurrence::build(&working_problem);
        let mut level = vec![0usize; 5];
        let mut reason: Vec<Option<usize>> = vec![None; 5];
        let mut trail: Vec<usize> = Vec::new();
        let mut q_head = 0usize;
        let mut seen = vec![false; 5];

        assign_literal(-1, 1, None, &mut x, &mut level, &mut reason, &mut trail);
        let confl = propagate(
            &working_problem.clauses,
            &lists,
            &mut watch,
            &mut x,
            &mut trail,
            &mut q_head,
            1,
            &mut level,
            &mut reason,
        )
        .expect("expected clause {-2,-4} to be falsified");

        let (learned, backtrack_level) = analyze(
            confl,
            &working_problem.clauses,
            &trail,
            &level,
            &reason,
            &x,
            &mut seen,
            1,
        );

        assert_eq!(backtrack_level, 0);
        assert_eq!(learned, vec![-2]);
    }

    #[test]
    fn test_add_learned_clause_skips_watches_for_unit_clause() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1, 2]],
        };
        let (mut working_problem, mut watch, _x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let mut lists = occurrence::build(&working_problem);
        let level = vec![0usize; 3];
        let before = working_problem.clauses.len();

        let idx = add_learned_clause(
            &vec![-1],
            &mut working_problem.clauses,
            &mut lists,
            &mut watch,
            &level,
        );

        assert_eq!(idx, None);
        assert_eq!(working_problem.clauses.len(), before);
    }

    #[test]
    fn test_add_learned_clause_watches_asserting_literal_and_highest_level() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![1, 2]],
        };
        let (mut working_problem, mut watch, _x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let mut lists = occurrence::build(&working_problem);
        let mut level = vec![0usize; 4];
        level[2] = 1;
        level[3] = 3; // higher than variable 2's level

        let idx = add_learned_clause(
            &vec![-1, 2, 3],
            &mut working_problem.clauses,
            &mut lists,
            &mut watch,
            &level,
        )
        .expect("expected a stored clause for a length-3 learned clause");

        assert_eq!(watch[idx], [-1, 3]);
    }

    #[test]
    fn test_run_finds_satisfiable_formula() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![1, 2], vec![-1, 3], vec![-2, -3]],
        };

        for variant in [SelectVarVariant::Weighted, SelectVarVariant::Fast] {
            let mut rng = StdRng::seed_from_u64(1);
            let result = run(&problem, None, variant, &mut rng, 0);
            assert!(
                result.satisfiable,
                "variant {variant:?}: expected satisfiable"
            );
            for (ci, clause) in problem.clauses.iter().enumerate() {
                let satisfied = clause
                    .iter()
                    .any(|&lit| assignment::literal_is_true(&result.assignment, lit));
                assert!(
                    satisfied,
                    "variant {variant:?}: clause {ci} ({clause:?}) not satisfied"
                );
            }
        }
    }

    /// Uses the same hand-verified formula as
    /// test_analyze_derives_unit_clause_independent_of_decision (which
    /// is unsatisfiable overall: x2 must be False, which forces x1 =
    /// True, which then forces x3 both True and False) to check the
    /// full run loop end to end, across both SelectVar variants, and
    /// confirms at least one clause was actually learned.
    #[test]
    fn test_run_proves_unsatisfiable_small_formula() {
        let problem = Problem {
            num_vars: 4,
            clauses: vec![
                vec![1, 2],
                vec![-1, 3],
                vec![-1, -3],
                vec![-2, 4],
                vec![-2, -4],
            ],
        };

        for variant in [SelectVarVariant::Weighted, SelectVarVariant::Fast] {
            let mut rng = StdRng::seed_from_u64(1);
            let result = run(&problem, None, variant, &mut rng, 0);
            assert!(
                !result.satisfiable,
                "variant {variant:?}: expected unsatisfiable"
            );
            assert!(!result.timed_out, "variant {variant:?}: unexpected timeout");
            assert!(
                result.num_conflicts > 0,
                "variant {variant:?}: expected at least one conflict"
            );
        }
    }

    /// Builds the standard CNF encoding of "num_pigeons pigeons cannot
    /// be placed into num_holes holes with no two pigeons sharing a
    /// hole", which is unsatisfiable whenever num_pigeons > num_holes.
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
        let mut rng = StdRng::seed_from_u64(9);

        let result = run(&problem, None, SelectVarVariant::Fast, &mut rng, 0);

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
        let result = run(&sat_problem, None, SelectVarVariant::Weighted, &mut rng, 0);
        assert!(result.satisfiable);

        let unsat_problem = Problem {
            num_vars: 0,
            clauses: vec![vec![]],
        };
        let result = run(
            &unsat_problem,
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
        let mut rng = StdRng::seed_from_u64(7);
        let tiny = Duration::from_nanos(1);

        let result = run(
            &problem,
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
