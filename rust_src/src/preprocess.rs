//! Implements the Stage 8 preprocessing pipeline shared by every
//! solving algorithm ("hc", "ws", and "dfs"): unit propagation, pure
//! literal elimination, subsumption elimination, and bounded
//! (NiVER-style) variable elimination, iterated together to a
//! fixpoint. The result is a simplified, renumbered problem with
//! (typically) fewer variables, clauses, and literals than the
//! original, plus enough bookkeeping to reconstruct a full solution
//! to the *original* problem from a solution to the simplified one.
//!
//! Reference: Eén & Biere, "Effective Preprocessing in SAT Through
//! Variable and Clause Elimination" (SatELite), SAT 2005; Subbarayan
//! & Pradhan, "NiVER: Non-Increasing Variable Elimination Resolution
//! for Preprocessing SAT Instances," SAT 2004 (the specific, simpler
//! elimination bound used here).

use std::collections::HashSet;

use crate::assignment::{self, Assignment, Value};
use crate::cnf::{self, Clause, Literal, Problem};

/// Bounds how many times the full technique pipeline (unit
/// propagation, pure literals, subsumption, variable elimination) is
/// repeated. Each round that changes anything makes the formula
/// strictly smaller in some way, so in practice this converges in far
/// fewer rounds; the bound is just a safety net.
const MAX_ROUNDS: usize = 1000;

/// Records enough information to reconstruct the value of one
/// variable removed by bounded variable elimination, once every other
/// variable's value is known. `positive` and `negative` are every
/// clause (in the *original* variable numbering) that mentioned `var`,
/// at the moment `var` was eliminated.
#[derive(Debug, Clone)]
pub struct EliminationStep {
    pub var: usize,
    /// Kept for symmetry with the clauses actually used
    /// (`negative`, below) and for inspection/debugging; reconstruction
    /// only ever needs to examine `negative` (see
    /// `reconstruct_eliminated_var`'s doc comment for why).
    #[allow(dead_code)]
    pub positive: Vec<Clause>,
    pub negative: Vec<Clause>,
}

/// Summarizes the effect of one call to [`run`], for the
/// "preprocess:" announcement printed at verbose level 1.
#[derive(Debug, Clone, Copy, Default)]
pub struct Stats {
    pub clauses_before: usize,
    pub clauses_after: usize,
    pub vars_before: usize,
    pub vars_after: usize,
    pub units_propagated: usize,
    pub pure_literals_fixed: usize,
    pub clauses_subsumed: usize,
    pub variables_eliminated: usize,
}

/// Describes the outcome of preprocessing a [`Problem`].
pub struct PreprocessResult {
    /// The simplified, renumbered problem to hand to a solving
    /// algorithm, or `None` if `unsat` is true. Its variables (when
    /// present) are numbered 1..=problem.num_vars and do not
    /// correspond directly to the original problem's variable
    /// numbers; see `new_to_original`.
    pub problem: Option<Problem>,

    /// The variable count of the problem given to `run`, before any
    /// simplification.
    pub original_num_vars: usize,

    /// Maps a variable number in `problem` to the original variable
    /// number it came from: `new_to_original[v]` is the original
    /// variable corresponding to `problem`'s variable `v`. Index 0 is
    /// unused.
    pub new_to_original: Vec<usize>,

    /// Every value pinned directly by unit propagation or pure
    /// literal elimination, in original variable numbering (as
    /// opposed to bounded variable elimination, which is recorded in
    /// `eliminated` instead). Sized `original_num_vars + 1`.
    pub fixed_assignment: Assignment,

    /// Every variable removed by bounded variable elimination, in the
    /// order they were eliminated ([`PreprocessResult::reconstruct`]
    /// processes this in reverse).
    pub eliminated: Vec<EliminationStep>,

    /// True if preprocessing alone already proved the original
    /// problem unsatisfiable (independent of whichever solving
    /// algorithm was requested).
    pub unsat: bool,

    #[allow(dead_code)]
    pub stats: Stats,
}

/// Preprocesses `problem`: repeatedly applies unit propagation, pure
/// literal elimination, subsumption elimination, and bounded variable
/// elimination until none of them can simplify the formula any
/// further (or [`MAX_ROUNDS`] is reached, as a safety bound), then
/// renumbers the surviving variables into a compact range starting at
/// one. If `verbose >= 1`, a summary line is printed reporting how
/// much the formula shrank.
///
/// If preprocessing alone discovers a contradiction,
/// `PreprocessResult::unsat` is true and `PreprocessResult::problem`
/// is `None`; the caller should report the original problem as
/// unsatisfiable without running any solving algorithm at all.
pub fn run(problem: &Problem, verbose: i32) -> PreprocessResult {
    let mut clauses = problem.clauses.clone();
    let mut assignment = assignment::new(problem.num_vars);
    let mut eliminated = Vec::new();
    let mut stats = Stats {
        clauses_before: problem.clauses.len(),
        vars_before: problem.num_vars,
        ..Stats::default()
    };

    for _ in 0..MAX_ROUNDS {
        let mut changed = false;

        let (unsat, units_fixed) = unit_propagate(&mut clauses, &mut assignment);
        stats.units_propagated += units_fixed;
        if unsat {
            return PreprocessResult {
                problem: None,
                original_num_vars: problem.num_vars,
                new_to_original: Vec::new(),
                fixed_assignment: assignment,
                eliminated,
                unsat: true,
                stats,
            };
        }
        changed |= units_fixed > 0;

        let pure_fixed = eliminate_pure_literals(&mut clauses, &mut assignment);
        stats.pure_literals_fixed += pure_fixed;
        changed |= pure_fixed > 0;

        let subsumed = eliminate_subsumed_clauses(&mut clauses);
        stats.clauses_subsumed += subsumed;
        changed |= subsumed > 0;

        let new_steps = eliminate_variables(&mut clauses, &assignment, problem.num_vars);
        if !new_steps.is_empty() {
            stats.variables_eliminated += new_steps.len();
            eliminated.extend(new_steps);
            changed = true;
        }

        if !changed {
            break;
        }
    }

    let (new_clauses, new_to_original) = renumber(&clauses, problem.num_vars);
    let reduced = Problem {
        num_vars: new_to_original.len() - 1,
        clauses: new_clauses,
    };
    stats.clauses_after = reduced.clauses.len();
    stats.vars_after = reduced.num_vars;

    if verbose >= 1 {
        println!(
            "preprocess: vars {}->{} clauses {}->{} (units={} pure={} subsumed={} eliminated={})",
            stats.vars_before,
            stats.vars_after,
            stats.clauses_before,
            stats.clauses_after,
            stats.units_propagated,
            stats.pure_literals_fixed,
            stats.clauses_subsumed,
            stats.variables_eliminated,
        );
    }

    PreprocessResult {
        problem: Some(reduced),
        original_num_vars: problem.num_vars,
        new_to_original,
        fixed_assignment: assignment,
        eliminated,
        unsat: false,
        stats,
    }
}

/// Compacts the surviving variables of `clauses` (those that still
/// appear in at least one clause) into a dense range starting at 1,
/// preserving their relative order of first appearance. Returns the
/// renumbered clauses and a vector mapping each new variable number
/// back to the original one it came from (index 0 unused).
fn renumber(clauses: &[Clause], original_num_vars: usize) -> (Vec<Clause>, Vec<usize>) {
    let mut original_to_new = vec![0usize; original_num_vars + 1];
    let mut new_to_original = vec![0usize];

    for clause in clauses {
        for &lit in clause {
            let v = cnf::literal_var(lit);
            if original_to_new[v] == 0 {
                original_to_new[v] = new_to_original.len();
                new_to_original.push(v);
            }
        }
    }

    let new_clauses: Vec<Clause> = clauses
        .iter()
        .map(|clause| {
            clause
                .iter()
                .map(|&lit| {
                    let new_var = original_to_new[cnf::literal_var(lit)] as Literal;
                    if cnf::literal_is_negative(lit) {
                        -new_var
                    } else {
                        new_var
                    }
                })
                .collect()
        })
        .collect();

    (new_clauses, new_to_original)
}

impl PreprocessResult {
    /// Takes `solution`, a complete assignment produced by a solving
    /// algorithm over `self.problem`'s (renumbered) variables, and
    /// returns a complete assignment over the *original* problem's
    /// variables: every surviving variable's value is carried over
    /// directly, every variable pinned by unit propagation/pure
    /// literal elimination is set to its fixed value, and every
    /// variable removed by bounded variable elimination is
    /// reconstructed in reverse elimination order.
    ///
    /// Before that reverse-order pass runs, every variable that is
    /// still `Unassigned` *and* is not itself the subject of some
    /// elimination step is pinned to `False`. This covers two cases:
    /// variables that never appeared in any clause at all
    /// (unconstrained from the start), and variables that disappeared
    /// without their own elimination step because every resolvent
    /// mentioning them turned out to be a tautology (their last
    /// remaining constraint vanished as a side effect of eliminating
    /// some other variable). Either way such a variable is free to
    /// fix arbitrarily, but it must be fixed *before* the
    /// reverse-order pass below, since an earlier-eliminated
    /// variable's reconstruction (processed later, in reverse) may
    /// need to examine its value.
    pub fn reconstruct(&self, solution: &Assignment) -> Assignment {
        let mut full = assignment::new(self.original_num_vars);

        for (new_var, &orig) in self.new_to_original.iter().enumerate().skip(1) {
            full[orig] = solution[new_var];
        }
        for (v, &value) in self.fixed_assignment.iter().enumerate() {
            if value != Value::Unassigned {
                full[v] = value;
            }
        }

        let mut is_eliminated = vec![false; self.original_num_vars + 1];
        for step in &self.eliminated {
            is_eliminated[step.var] = true;
        }
        for (v, value) in full.iter_mut().enumerate().skip(1) {
            if *value == Value::Unassigned && !is_eliminated[v] {
                *value = Value::False;
            }
        }

        for step in self.eliminated.iter().rev() {
            full[step.var] = reconstruct_eliminated_var(step, &full);
        }

        full
    }
}

/// Picks a value for `step.var`, given that `full` already holds a
/// value for every other variable `step`'s clauses reference.
/// `full[step.var]` is still `Unassigned` at this point, which (by
/// [`assignment::literal_is_true`]'s definition) makes any literal of
/// `step.var` evaluate to false while checking whether `step.negative`
/// is already satisfied by some *other* literal -- exactly the
/// condition this needs to check.
///
/// Setting `var` to `True` trivially satisfies every clause in
/// `step.positive` (each contains the literal `+var`). The only
/// question is whether that breaks some clause in `step.negative`; if
/// every such clause is already satisfied by another literal, `True`
/// is safe. Otherwise `var` must be `False` -- and the correctness of
/// variable elimination (every resolvent of a positive/negative pair
/// was already satisfied when `var` was eliminated) guarantees that in
/// that case, every clause in `step.positive` is in turn already
/// satisfied by some other literal, so `False` is safe too.
fn reconstruct_eliminated_var(step: &EliminationStep, full: &Assignment) -> Value {
    for clause in &step.negative {
        if !clause_satisfied(clause, full) {
            return Value::False;
        }
    }
    Value::True
}

/// Returns whether at least one literal of `clause` is true under
/// `assignment`.
fn clause_satisfied(clause: &Clause, assignment: &Assignment) -> bool {
    clause
        .iter()
        .any(|&lit| assignment::literal_is_true(assignment, lit))
}

/// Rewrites `clauses` in place under `assignment`: every clause
/// already satisfied by some true literal is dropped, and every other
/// clause has its false literals (if any) removed, leaving only
/// unassigned literals. Returns whether a contradiction was found (a
/// clause with no unassigned literals and none true).
fn simplify_with_assignment(clauses: &mut Vec<Clause>, assignment: &Assignment) -> bool {
    let old = std::mem::take(clauses);
    let mut kept = Vec::with_capacity(old.len());
    for clause in old {
        let mut satisfied = false;
        let mut reduced = Clause::new();
        for &lit in &clause {
            let value = assignment[cnf::literal_var(lit)];
            if value == Value::Unassigned {
                reduced.push(lit);
                continue;
            }
            if assignment::literal_is_true(assignment, lit) {
                satisfied = true;
                break;
            }
        }
        if satisfied {
            continue;
        }
        if reduced.is_empty() {
            return true;
        }
        kept.push(reduced);
    }
    *clauses = kept;
    false
}

/// Repeatedly finds a clause with exactly one unassigned literal and
/// forces that literal true, removing now-satisfied clauses and
/// shrinking others as it goes (via [`simplify_with_assignment`]),
/// until no clause is a unit clause or a contradiction is found.
/// Returns whether a contradiction was found, and how many variables
/// were newly fixed.
fn unit_propagate(clauses: &mut Vec<Clause>, assignment: &mut Assignment) -> (bool, usize) {
    let mut num_fixed = 0;
    loop {
        if simplify_with_assignment(clauses, assignment) {
            return (true, num_fixed);
        }

        let mut found_unit = false;
        for clause in clauses.iter() {
            if clause.len() != 1 {
                continue;
            }
            let lit = clause[0];
            let v = cnf::literal_var(lit);
            assignment[v] = if cnf::literal_is_negative(lit) {
                Value::False
            } else {
                Value::True
            };
            num_fixed += 1;
            found_unit = true;
        }
        if !found_unit {
            return (false, num_fixed);
        }
    }
}

/// Repeatedly finds variables that appear with only one polarity
/// across all of `clauses` (never negated, or always negated) and
/// fixes them to the value that satisfies every clause containing
/// them, removing those now-satisfied clauses. Returns how many
/// variables were fixed this way.
fn eliminate_pure_literals(clauses: &mut Vec<Clause>, assignment: &mut Assignment) -> usize {
    let mut num_fixed = 0;
    loop {
        let n = assignment.len();
        let mut seen_positive = vec![false; n];
        let mut seen_negative = vec![false; n];
        for clause in clauses.iter() {
            for &lit in clause {
                let v = cnf::literal_var(lit);
                if cnf::literal_is_negative(lit) {
                    seen_negative[v] = true;
                } else {
                    seen_positive[v] = true;
                }
            }
        }

        let mut fixed_this_pass = false;
        for v in 1..n {
            if assignment[v] != Value::Unassigned {
                continue;
            }
            if seen_positive[v] && !seen_negative[v] {
                assignment[v] = Value::True;
            } else if seen_negative[v] && !seen_positive[v] {
                assignment[v] = Value::False;
            } else {
                continue;
            }
            num_fixed += 1;
            fixed_this_pass = true;
        }
        if !fixed_this_pass {
            return num_fixed;
        }
        // Fixing a pure literal can only satisfy clauses (it never
        // introduces a new false literal elsewhere, since the
        // variable never appears with the opposite polarity), so this
        // cannot discover a contradiction.
        let _ = simplify_with_assignment(clauses, assignment);
    }
}

/// Removes every clause that is subsumed by some other (shorter or
/// equal-length) clause in `clauses`: if clause A is a subset of
/// clause B, then A already enforces at least as much as B does,
/// making B redundant. Returns how many clauses were removed.
fn eliminate_subsumed_clauses(clauses: &mut Vec<Clause>) -> usize {
    let sets: Vec<HashSet<Literal>> = clauses
        .iter()
        .map(|clause| clause.iter().copied().collect())
        .collect();
    let mut keep = vec![true; clauses.len()];

    for i in 0..clauses.len() {
        if !keep[i] {
            continue;
        }
        for j in 0..clauses.len() {
            if i == j || !keep[j] {
                continue;
            }
            if clauses[i].len() > clauses[j].len() {
                continue;
            }
            if clauses[i].len() == clauses[j].len() && i > j {
                // Equal-length duplicates: keep only the first one seen.
                continue;
            }
            if clauses[i].iter().all(|lit| sets[j].contains(lit)) {
                keep[j] = false;
            }
        }
    }

    let mut num_removed = 0;
    let old = std::mem::take(clauses);
    let mut kept = Vec::with_capacity(old.len());
    for (i, clause) in old.into_iter().enumerate() {
        if keep[i] {
            kept.push(clause);
        } else {
            num_removed += 1;
        }
    }
    *clauses = kept;
    num_removed
}

/// Computes the resolvent of clauses `pos` and `neg` on variable `v`
/// (which `pos` contains positively and `neg` contains negatively),
/// omitting the literal on `v` itself. Returns `None` if the
/// resolvent would be a tautology (containing both some literal and
/// its negation), in which case it is always true and carries no
/// information, so it is discarded rather than returned.
fn resolve(pos: &Clause, neg: &Clause, v: usize) -> Option<Clause> {
    let mut seen: HashSet<Literal> = HashSet::new();
    let mut resolvent = Clause::new();
    for &lit in pos.iter().chain(neg.iter()) {
        if cnf::literal_var(lit) == v {
            continue;
        }
        if seen.contains(&-lit) {
            return None;
        }
        if seen.insert(lit) {
            resolvent.push(lit);
        }
    }
    Some(resolvent)
}

/// Applies bounded variable elimination (the NiVER rule: Subbarayan &
/// Pradhan, SAT 2004): a variable `v` is eliminated by resolving every
/// clause containing `+v` against every clause containing `-v`,
/// replacing all of them with the (non-tautological) resolvents, but
/// only when doing so does not increase the number of clauses -- i.e.
/// only when it cannot make the formula larger. Eliminated variables
/// are recorded (with the clauses they were eliminated from, for
/// later reconstruction) and returned.
///
/// Variables that are unassigned but do not appear with both
/// polarities (pure literals) or do not appear at all are left alone
/// here; [`eliminate_pure_literals`] and `run`'s outer fixpoint loop
/// handle those cases.
fn eliminate_variables(
    clauses: &mut Vec<Clause>,
    assignment: &Assignment,
    num_vars: usize,
) -> Vec<EliminationStep> {
    let mut steps = Vec::new();

    loop {
        let mut positive: Vec<Vec<usize>> = vec![Vec::new(); num_vars + 1];
        let mut negative: Vec<Vec<usize>> = vec![Vec::new(); num_vars + 1];
        for (i, clause) in clauses.iter().enumerate() {
            for &lit in clause {
                let v = cnf::literal_var(lit);
                if cnf::literal_is_negative(lit) {
                    negative[v].push(i);
                } else {
                    positive[v].push(i);
                }
            }
        }

        let mut eliminated_this_pass = false;
        for v in 1..=num_vars {
            if assignment[v] != Value::Unassigned {
                continue;
            }
            let pos_idx = &positive[v];
            let neg_idx = &negative[v];
            if pos_idx.is_empty() || neg_idx.is_empty() {
                continue; // not eliminable here: pure or absent
            }

            let mut resolvents = Vec::new();
            for &pi in pos_idx {
                for &ni in neg_idx {
                    if let Some(resolvent) = resolve(&clauses[pi], &clauses[ni], v) {
                        resolvents.push(resolvent);
                    }
                }
            }

            let removed_count = pos_idx.len() + neg_idx.len();
            if resolvents.len() > removed_count {
                continue; // would increase the clause count; not worth it
            }

            let positive_clauses: Vec<Clause> =
                pos_idx.iter().map(|&i| clauses[i].clone()).collect();
            let negative_clauses: Vec<Clause> =
                neg_idx.iter().map(|&i| clauses[i].clone()).collect();
            steps.push(EliminationStep {
                var: v,
                positive: positive_clauses,
                negative: negative_clauses,
            });

            let remove_set: HashSet<usize> =
                pos_idx.iter().chain(neg_idx.iter()).copied().collect();
            let mut survivors: Vec<Clause> = clauses
                .iter()
                .enumerate()
                .filter(|(i, _)| !remove_set.contains(i))
                .map(|(_, clause)| clause.clone())
                .collect();
            survivors.extend(resolvents);
            *clauses = survivors;

            eliminated_this_pass = true;
            break; // occurrence lists above are now stale; rebuild and retry
        }

        if !eliminated_this_pass {
            return steps;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn all_satisfied(clauses: &[Clause], assignment: &Assignment) -> bool {
        clauses.iter().all(|c| clause_satisfied(c, assignment))
    }

    fn all_clauses_satisfied(problem: &Problem, full: &Assignment) {
        for (i, clause) in problem.clauses.iter().enumerate() {
            assert!(
                clause_satisfied(clause, full),
                "clause {i} ({clause:?}) not satisfied by {full:?}"
            );
        }
    }

    #[test]
    fn test_unit_propagate_chain() {
        let mut clauses = vec![vec![1], vec![-1, 2], vec![-2, 3]];
        let mut assignment = assignment::new(3);

        let (unsat, num_fixed) = unit_propagate(&mut clauses, &mut assignment);
        assert!(!unsat);
        assert_eq!(num_fixed, 3);
        assert_eq!(assignment[1], Value::True);
        assert_eq!(assignment[2], Value::True);
        assert_eq!(assignment[3], Value::True);
        assert!(clauses.is_empty());
    }

    #[test]
    fn test_unit_propagate_detects_contradiction() {
        let mut clauses = vec![vec![1], vec![-1]];
        let mut assignment = assignment::new(1);

        let (unsat, _) = unit_propagate(&mut clauses, &mut assignment);
        assert!(unsat);
    }

    #[test]
    fn test_eliminate_pure_literals() {
        let mut clauses = vec![vec![1, 2], vec![1, -2]];
        let mut assignment = assignment::new(2);

        let num_fixed = eliminate_pure_literals(&mut clauses, &mut assignment);
        assert_eq!(num_fixed, 1);
        assert_eq!(assignment[1], Value::True);
        assert!(clauses.is_empty());
    }

    #[test]
    fn test_eliminate_subsumed_clauses_removes_superset() {
        let mut clauses = vec![vec![1, 2], vec![1, 2, 3]];
        let num_removed = eliminate_subsumed_clauses(&mut clauses);
        assert_eq!(num_removed, 1);
        assert_eq!(clauses.len(), 1);
    }

    #[test]
    fn test_eliminate_subsumed_clauses_removes_duplicates() {
        let mut clauses = vec![vec![1, -2], vec![1, -2]];
        let num_removed = eliminate_subsumed_clauses(&mut clauses);
        assert_eq!(num_removed, 1);
        assert_eq!(clauses.len(), 1);
    }

    #[test]
    fn test_resolve_omits_tautology() {
        let pos = vec![1, 2];
        let neg = vec![-1, -2];
        assert_eq!(resolve(&pos, &neg, 1), None);
    }

    #[test]
    fn test_resolve_produces_expected_clause() {
        let pos = vec![1, 2];
        let neg = vec![-1, 3];
        let resolvent = resolve(&pos, &neg, 1).expect("expected a valid resolvent");
        let got: HashSet<Literal> = resolvent.into_iter().collect();
        assert_eq!(got, HashSet::from([2, 3]));
    }

    #[test]
    fn test_eliminate_variables_non_increasing() {
        let mut clauses = vec![vec![1, 2], vec![-1, 3]];
        let assignment = assignment::new(3);

        let steps = eliminate_variables(&mut clauses, &assignment, 3);
        assert_eq!(steps.len(), 1);
        assert_eq!(steps[0].var, 1);
        assert_eq!(clauses.len(), 1);
    }

    #[test]
    fn test_eliminate_variables_skips_when_increasing() {
        // 2 positive and 3 negative occurrences of variable 1 would
        // produce 2*3=6 resolvents, replacing only 5 clauses.
        let mut clauses = vec![
            vec![1, 2],
            vec![1, 3],
            vec![-1, 4],
            vec![-1, 5],
            vec![-1, 6],
        ];
        let assignment = assignment::new(6);

        let steps = eliminate_variables(&mut clauses, &assignment, 6);
        assert!(steps.is_empty());
    }

    #[test]
    fn test_run_simplifies_and_preserves_satisfiability() {
        let problem = Problem {
            num_vars: 4,
            clauses: vec![
                vec![1],
                vec![-1, 2],
                vec![3, 4],
                vec![3, 4, 2], // subsumed by the clause above
            ],
        };

        let result = run(&problem, 0);
        assert!(!result.unsat);
        let reduced = result.problem.as_ref().expect("expected a reduced problem");
        assert!(reduced.num_vars <= 2);

        let mut solution = assignment::new(reduced.num_vars);
        for value in solution.iter_mut().skip(1) {
            *value = Value::True;
        }

        let full = result.reconstruct(&solution);
        all_clauses_satisfied(&problem, &full);
    }

    #[test]
    fn test_run_detects_unsat() {
        let problem = Problem {
            num_vars: 1,
            clauses: vec![vec![1], vec![-1]],
        };
        let result = run(&problem, 0);
        assert!(result.unsat);
        assert!(result.problem.is_none());
    }

    /// Exercises the trickiest reconstruction path: a variable
    /// removed entirely by bounded variable elimination (not just
    /// fixed by unit propagation or pure literal elimination) must
    /// still be recoverable. Every variable here appears with both
    /// polarities somewhere, so neither unit propagation nor pure
    /// literal elimination can fire; only resolution can simplify
    /// this formula.
    ///
    /// Rather than assume exactly which variable the pipeline chooses
    /// to eliminate (an implementation detail of iteration order),
    /// this brute-forces every assignment of the *reduced* problem's
    /// variables, and for every one that actually satisfies the
    /// reduced problem, checks that reconstructing it yields a full
    /// assignment satisfying the *original* problem.
    #[test]
    fn test_run_with_variable_elimination_reconstructs_correctly() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![1, 2], vec![-1, 3], vec![-2, -3]],
        };

        let result = run(&problem, 0);
        assert!(!result.unsat);
        assert!(
            !result.eliminated.is_empty(),
            "expected at least one variable to be eliminated via resolution"
        );

        let reduced = result.problem.as_ref().expect("expected a reduced problem");
        let n = reduced.num_vars;
        let mut checked = 0;
        for mask in 0u32..(1 << n) {
            let mut solution = assignment::new(n);
            for (v, value) in solution.iter_mut().enumerate().skip(1) {
                *value = if mask & (1 << (v - 1)) != 0 {
                    Value::True
                } else {
                    Value::False
                };
            }
            if !all_satisfied(&reduced.clauses, &solution) {
                continue;
            }
            checked += 1;
            let full = result.reconstruct(&solution);
            all_clauses_satisfied(&problem, &full);
        }
        assert!(
            checked > 0,
            "no assignment of the reduced problem's {n} variables satisfied it; test setup is broken"
        );
    }

    #[test]
    fn test_run_leaves_unconstrained_variables_arbitrarily_false() {
        let problem = Problem {
            num_vars: 2, // variable 2 never appears in any clause
            clauses: vec![vec![1]],
        };
        let result = run(&problem, 0);
        assert!(!result.unsat);
        let reduced = result.problem.as_ref().expect("expected a reduced problem");

        let mut solution = assignment::new(reduced.num_vars);
        for value in solution.iter_mut().skip(1) {
            *value = Value::True;
        }
        let full = result.reconstruct(&solution);
        assert_ne!(full[2], Value::Unassigned);
        all_clauses_satisfied(&problem, &full);
    }
}
