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

use std::cell::Cell;
use std::sync::atomic::{AtomicBool, Ordering};
use std::thread;

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
///
/// STAGE25.md: `num_threads` uses this many OS threads to speed up
/// subsumption elimination, the technique profiling found dominates
/// preprocessing cost on real, clause-count-heavy instances (see
/// [`eliminate_subsumed_clauses_parallel`]'s doc comment; REPORT25.md
/// has the full measurement). `num_threads <= 1` runs the exact same
/// single-threaded code path as before Stage 25 (bit-for-bit
/// identical output, matching the `num_threads <= 1` delegates-to-the-
/// original-function convention this project has used for every other
/// algorithm's own `--num-threads` support since Stage 17). Every
/// other technique here (unit propagation, pure literal elimination,
/// and bounded variable elimination) remains single-threaded
/// regardless of `num_threads`; see REPORT25.md for why BVE in
/// particular was not also threaded this stage.
pub fn run(problem: &Problem, verbose: i32, num_threads: usize) -> PreprocessResult {
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

        let subsumed =
            eliminate_subsumed_clauses_parallel(&mut clauses, problem.num_vars, num_threads);
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
///
/// Exposed beyond this module (see [`crate::dfs::run`]'s bootstrap
/// step, STAGE9.md) since watched literals require every clause to
/// have at least two literals, which this guarantees.
pub fn unit_propagate(clauses: &mut Vec<Clause>, assignment: &mut Assignment) -> (bool, usize) {
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

/// Bounds the total number of subsumer/candidate pairs
/// `eliminate_subsumed_clauses`/`eliminate_subsumed_clauses_parallel`
/// will ever examine, as a multiple of the clause count -- see
/// `eliminate_subsumed_clauses`'s own doc comment for why this exists
/// (STAGE30.md/REPORT30.md) and what it trades away. Chosen generously
/// (most real instances' occurrence lists are far smaller than this)
/// but small enough to guarantee the whole function is `O(clauses)` in
/// the worst case: even a literal appearing in every single clause can
/// only ever contribute this many candidate checks in total before the
/// budget runs out.
const SUBSUMPTION_WORK_BUDGET_FACTOR: usize = 64;

/// Maps each literal (by variable and sign, the same convention
/// `eliminate_variables`'s `positive`/`negative` arrays use) to the
/// indices of every clause in which it appears.
struct LiteralOccurrence {
    positive: Vec<Vec<usize>>,
    negative: Vec<Vec<usize>>,
}

impl LiteralOccurrence {
    /// Computes, once, the clause indices each literal appears in --
    /// shared, read-only input for every subsumer clause's candidate
    /// search below, so it only ever needs building a single time
    /// regardless of how many threads
    /// `eliminate_subsumed_clauses_parallel` uses.
    fn build(clauses: &[Clause], num_vars: usize) -> Self {
        let mut occ = LiteralOccurrence {
            positive: vec![Vec::new(); num_vars + 1],
            negative: vec![Vec::new(); num_vars + 1],
        };
        for (i, clause) in clauses.iter().enumerate() {
            for &lit in clause {
                if cnf::literal_is_negative(lit) {
                    occ.negative[cnf::literal_var(lit)].push(i);
                } else {
                    occ.positive[cnf::literal_var(lit)].push(i);
                }
            }
        }
        occ
    }

    /// Returns the occurrence list for `lit` specifically (as opposed
    /// to its variable's other polarity).
    fn of(&self, lit: Literal) -> &[usize] {
        if cnf::literal_is_negative(lit) {
            &self.negative[cnf::literal_var(lit)]
        } else {
            &self.positive[cnf::literal_var(lit)]
        }
    }
}

/// Returns the occurrence list of whichever literal in `clause`
/// appears in the fewest clauses overall -- the smallest set that is
/// guaranteed to contain every clause `eliminate_subsumed_clauses`'s
/// caller could possibly subsume via `clause` (see
/// `eliminate_subsumed_clauses`'s own doc comment for why that
/// guarantee holds). Returns `None` only for a genuinely empty clause,
/// which (by construction -- `run` always calls `unit_propagate`,
/// which detects an empty clause as UNSAT and returns immediately,
/// before any subsumption pass ever sees the clause set) should never
/// actually reach this function in practice.
fn rarest_literal_occurrences<'a>(
    occ: &'a LiteralOccurrence,
    clause: &Clause,
) -> Option<&'a [usize]> {
    let (&first, rest) = clause.split_first()?;
    let mut best = occ.of(first);
    for &lit in rest {
        let candidate = occ.of(lit);
        if candidate.len() < best.len() {
            best = candidate;
        }
    }
    Some(best)
}

/// `eliminate_subsumed_clauses`'/`eliminate_subsumed_clauses_parallel`'s
/// shared core: for every subsumer index `i` in `lo..hi`, look only at
/// the candidates `rarest_literal_occurrences` says could possibly be
/// subsumed by `cs[i]`, and mark each genuine subset removed via
/// `mark_removed`. `*budget` is decremented by the number of
/// candidates actually examined, and processing stops early, leaving
/// every remaining clause's keep bit untouched, the moment it runs out
/// -- see `eliminate_subsumed_clauses`'s doc comment for why that is
/// always a safe (if possibly less thorough) outcome.
///
/// `is_kept`/`mark_removed` read and write the caller's "keep" bits,
/// rather than this function taking a keep slice directly, so this one
/// implementation serves both `eliminate_subsumed_clauses` (plain
/// `Vec<bool>`, no concurrency at all) and
/// `eliminate_subsumed_clauses_parallel` (`Vec<AtomicBool>`, shared
/// across threads) without duplicating this loop for each.
#[allow(clippy::too_many_arguments)]
fn try_subsume_from(
    cs: &[Clause],
    occ: &LiteralOccurrence,
    n: usize,
    is_kept: impl Fn(usize) -> bool,
    mark_removed: impl Fn(usize),
    lo: usize,
    hi: usize,
    budget: &mut usize,
) {
    for i in lo..hi {
        if *budget == 0 {
            return;
        }
        let Some(candidates) = rarest_literal_occurrences(occ, &cs[i]) else {
            continue;
        };
        *budget = budget.saturating_sub(candidates.len());
        for &j in candidates {
            if j == i || j >= n || !is_kept(j) {
                continue;
            }
            if cs[i].len() > cs[j].len() {
                continue;
            }
            if cs[i].len() == cs[j].len() && i > j {
                // Equal-length duplicates: keep only the first one seen.
                continue;
            }
            if cs[i].iter().all(|lit| cs[j].contains(lit)) {
                mark_removed(j);
            }
        }
    }
}

/// Removes every clause that is subsumed by some other (shorter or
/// equal-length) clause in `clauses`: if clause A is a subset of
/// clause B, then A already enforces at least as much as B does,
/// making B redundant. Returns how many clauses were removed.
/// `num_vars` must be at least as large as the largest variable number
/// appearing in `clauses` (`run` always passes the problem's own
/// `num_vars`).
///
/// STAGE30.md/REPORT30.md: this used to check every one of the
/// `O(clauses^2)` ordered pairs directly (REPORT25.md's own
/// multithreaded version of that same all-pairs loop). REPORT29.md
/// found that quadratic cost is a severe, widely-triggered problem on
/// real SAT Competition instances -- not just the handful of largest
/// files REPORT25.md had seen -- so this stage replaces the all-pairs
/// scan with the standard technique real preprocessors use (SatELite,
/// MiniSat): for clause A to subsume any clause B, every literal of A,
/// including whichever one of A's own literals occurs in the *fewest*
/// clauses overall, must appear in B -- so B can only ever be found in
/// that one literal's occurrence list, never anywhere else. Checking
/// only that list instead of every other clause in the formula turns
/// the common case from `O(clauses)` candidates per clause into
/// `O(how often A's rarest literal actually recurs)`, which for most
/// real CNF instances (each variable appearing in a bounded number of
/// clauses) is close to a small constant -- i.e. close to linear
/// overall, not quadratic. This is an exact restriction, not an
/// approximation: it can never miss a real subsumption, since any B
/// that A subsumes is *guaranteed* to be in that occurrence list by
/// definition.
///
/// This does not change the worst-case complexity class in general --
/// a literal that occurs in every clause (or a formula constructed
/// adversarially so every clause's rarest literal still has a huge
/// occurrence list) still makes this `O(clauses^2)`, and there is no
/// known algorithm that avoids that in the worst case; see
/// REPORT30.md's discussion of the Orthogonal Vectors problem for why
/// this project believes (without proving) that no such algorithm
/// exists. `SUBSUMPTION_WORK_BUDGET_FACTOR` is this function's answer
/// to that: a hard cap on total candidate-pair work, expressed as a
/// multiple of the clause count, so a pathological or merely very
/// dense formula degrades to "less subsumption found" rather than
/// "this function's running time is unbounded." Running out of budget
/// is always safe: skipping a possible subsumption never changes
/// whether the formula is satisfiable, only how compact it ends up.
fn eliminate_subsumed_clauses(clauses: &mut Vec<Clause>, num_vars: usize) -> usize {
    let occ = LiteralOccurrence::build(clauses, num_vars);
    // Cell, not a plain bool, purely so the read closure and the write
    // closure below can both capture `keep` (immutably, via Cell's
    // interior mutability) at once -- there is no actual concurrency
    // here (this whole function is single-threaded), just two
    // `Fn` closures that would otherwise alias a `&mut`.
    let keep: Vec<Cell<bool>> = vec![Cell::new(true); clauses.len()];

    let mut budget = SUBSUMPTION_WORK_BUDGET_FACTOR * clauses.len();
    try_subsume_from(
        clauses,
        &occ,
        clauses.len(),
        |i| keep[i].get(),
        |i| keep[i].set(false),
        0,
        clauses.len(),
        &mut budget,
    );

    let mut num_removed = 0;
    let old = std::mem::take(clauses);
    let mut kept = Vec::with_capacity(old.len());
    for (i, clause) in old.into_iter().enumerate() {
        if keep[i].get() {
            kept.push(clause);
        } else {
            num_removed += 1;
        }
    }
    *clauses = kept;
    num_removed
}

/// Computes exactly the same result as [`eliminate_subsumed_clauses`]
/// (the set of clauses removed, and the surviving clauses in their
/// original relative order) whenever both are given the same,
/// unexhausted work budget, but splits the outer subsumer-clause loop
/// across `num_threads` OS threads. `num_threads <= 1` just calls
/// `eliminate_subsumed_clauses` directly.
///
/// STAGE25.md/REPORT25.md: profiling found subsumption elimination is
/// the single largest cost in preprocessing on real, clause-count-heavy
/// benchmark instances (87-94% of total preprocessing time on
/// `benchmark/blocksworld/bw_large.c.cnf`/`.d.cnf`) -- its (worst-case)
/// `O(clauses^2)` pairwise comparison is the natural target for
/// `--num-threads`, far more than `eliminate_variables` (BVE), which
/// REPORT22.md had guessed was the bottleneck (see `run`'s doc comment
/// for why BVE itself is not also threaded).
///
/// The key fact that makes this a safe, provably-equivalent
/// parallelization, not just an approximation, is unchanged from
/// REPORT25.md's original version of this function (see its own
/// historical discussion, preserved in version control, for the full
/// transitivity argument): `eliminate_subsumed_clauses`'s own
/// `if !keep[i] { continue }` skip is a pure optimization, never
/// required for correctness, so every thread's chunk of subsumer
/// indices can run against the same read-only clauses/occurrence-list
/// data with no coordination beyond `keep`'s atomic writes. Restricting
/// each subsumer's candidates to its rarest literal's occurrence list
/// (this stage's change) does not affect that argument at all: it is an
/// exact restriction (see `eliminate_subsumed_clauses`'s doc comment),
/// so it changes *which pairs are ever compared*, never *what the
/// comparison would have found* had it been made.
///
/// The work budget is split evenly across threads
/// (`budget / num_threads` each) rather than shared through one atomic
/// counter: a shared counter would need its own synchronization (and
/// associated contention) for a value that only exists to bound
/// worst-case time in the first place, which would be a strange thing
/// to spend synchronization overhead on. Splitting it evenly still
/// bounds total work at exactly the same budget, just distributed
/// rather than pooled, which only matters for exactly how much
/// subsumption gets found in the (rare, already-degraded) case where
/// the budget actually runs out.
fn eliminate_subsumed_clauses_parallel(
    clauses: &mut Vec<Clause>,
    num_vars: usize,
    num_threads: usize,
) -> usize {
    if num_threads <= 1 {
        return eliminate_subsumed_clauses(clauses, num_vars);
    }

    let n = clauses.len();
    let occ = LiteralOccurrence::build(clauses, num_vars);
    let keep: Vec<AtomicBool> = (0..n).map(|_| AtomicBool::new(true)).collect();
    let cs: &Vec<Clause> = clauses;
    let chunk = n.div_ceil(num_threads).max(1);
    let per_thread_budget = (SUBSUMPTION_WORK_BUDGET_FACTOR * n) / num_threads;

    thread::scope(|scope| {
        let mut start = 0;
        while start < n {
            let end = (start + chunk).min(n);
            let keep = &keep;
            let occ = &occ;
            scope.spawn(move || {
                let mut budget = per_thread_budget;
                try_subsume_from(
                    cs,
                    occ,
                    n,
                    |i| keep[i].load(Ordering::SeqCst),
                    |i| keep[i].store(false, Ordering::SeqCst),
                    start,
                    end,
                    &mut budget,
                );
            });
            start = end;
        }
    });

    let mut num_removed = 0;
    let old = std::mem::take(clauses);
    let mut kept = Vec::with_capacity(old.len());
    for (i, clause) in old.into_iter().enumerate() {
        if keep[i].load(Ordering::SeqCst) {
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
///
/// STAGE25.md: `seen` used to be a `HashSet<Literal>`; per
/// `eliminate_subsumed_clauses`'s doc comment above, a resolvent is
/// typically just as short as any other clause in this project, so a
/// plain `Vec` scan beats a `HashSet` here too -- and this also drops
/// a per-call heap allocation that `resolve` paid on *every* one of
/// its (often numerous: `pos`-occurrences x `neg`-occurrences per
/// candidate variable) calls.
fn resolve(pos: &Clause, neg: &Clause, v: usize) -> Option<Clause> {
    let mut seen: Clause = Vec::with_capacity(pos.len() + neg.len());
    let mut resolvent = Clause::new();
    for &lit in pos.iter().chain(neg.iter()) {
        if cnf::literal_var(lit) == v {
            continue;
        }
        if seen.contains(&-lit) {
            return None;
        }
        if !seen.contains(&lit) {
            seen.push(lit);
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

            // STAGE25.md: remove_set used to be a HashSet<usize>;
            // clause indices are already a dense range, so a plain
            // Vec<bool> (direct O(1) indexing, no hashing) is both
            // simpler and faster.
            let mut remove_set = vec![false; clauses.len()];
            for &idx in pos_idx.iter().chain(neg_idx.iter()) {
                remove_set[idx] = true;
            }
            let mut survivors: Vec<Clause> = clauses
                .iter()
                .enumerate()
                .filter(|(i, _)| !remove_set[*i])
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
    use rand::RngExt;
    use rand::SeedableRng;
    use rand::rngs::StdRng;
    use std::collections::HashSet;

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

    /// Confirms the STAGE30.md/REPORT30.md work-budget safety net
    /// actually does nothing (removes nothing, touches no keep bit)
    /// once the budget is exhausted, rather than, say, panicking on an
    /// unexpected state or ignoring the budget entirely -- directly,
    /// deterministically, rather than trying to organically construct
    /// a formula large enough to exhaust the real default budget.
    #[test]
    fn test_try_subsume_from_respects_zero_work_budget() {
        let clauses: Vec<Clause> = vec![vec![1], vec![1, 2]];
        let occ = LiteralOccurrence::build(&clauses, 2);
        let keep = [Cell::new(true), Cell::new(true)];
        let mut budget = 0;

        try_subsume_from(
            &clauses,
            &occ,
            clauses.len(),
            |i| keep[i].get(),
            |i| keep[i].set(false),
            0,
            clauses.len(),
            &mut budget,
        );

        assert!(
            keep[0].get() && keep[1].get(),
            "a zero work budget should have removed nothing; keep = [{}, {}]",
            keep[0].get(),
            keep[1].get()
        );
    }

    #[test]
    fn test_eliminate_subsumed_clauses_removes_superset() {
        let mut clauses = vec![vec![1, 2], vec![1, 2, 3]];
        let num_removed = eliminate_subsumed_clauses(&mut clauses, 3);
        assert_eq!(num_removed, 1);
        assert_eq!(clauses.len(), 1);
    }

    #[test]
    fn test_eliminate_subsumed_clauses_removes_duplicates() {
        let mut clauses = vec![vec![1, -2], vec![1, -2]];
        let num_removed = eliminate_subsumed_clauses(&mut clauses, 2);
        assert_eq!(num_removed, 1);
        assert_eq!(clauses.len(), 1);
    }

    /// Exercises exactly the transitivity chain
    /// `eliminate_subsumed_clauses_parallel`'s doc comment argues makes
    /// omitting the `if !keep[i] { continue }` skip safe: clause 0
    /// (`{1}`) subsumes clause 1 (`{1,2}`), which is itself long enough
    /// that -- were it not itself about to be marked non-keep -- it
    /// would be the one to catch clause 2 (`{1,2,4}`). Since `{1}` is
    /// also a subset of `{1,2,4}` directly, this asserts the parallel
    /// version (at several thread counts, including more threads than
    /// clauses) removes exactly the same two clauses the sequential
    /// version does.
    #[test]
    fn test_eliminate_subsumed_clauses_parallel_matches_sequential_chained() {
        let original: Vec<Clause> = vec![vec![1], vec![1, 2], vec![1, 2, 4]];

        let mut want = original.clone();
        let want_removed = eliminate_subsumed_clauses(&mut want, 4);

        for num_threads in [1, 2, 3, 4, 8] {
            let mut got = original.clone();
            let got_removed = eliminate_subsumed_clauses_parallel(&mut got, 4, num_threads);
            assert_eq!(got_removed, want_removed, "num_threads={num_threads}");
            assert_eq!(got, want, "num_threads={num_threads}");
        }
    }

    /// Runs both the sequential and parallel (several thread counts)
    /// implementations against a real benchmark file and asserts they
    /// remove exactly the same clauses.
    #[test]
    fn test_eliminate_subsumed_clauses_parallel_matches_sequential_on_real_file() {
        let path = "../benchmark/blocksworld/anomaly.cnf";
        let problem =
            cnf::read_dimacs(path, 0).unwrap_or_else(|e| panic!("failed to read {path}: {e}"));

        let mut want = problem.clauses.clone();
        let want_removed = eliminate_subsumed_clauses(&mut want, problem.num_vars);
        assert!(
            want_removed > 0,
            "test file has nothing to subsume; pick a different file"
        );

        for num_threads in [1, 2, 3, 4, 8, 16] {
            let mut got = problem.clauses.clone();
            let got_removed =
                eliminate_subsumed_clauses_parallel(&mut got, problem.num_vars, num_threads);
            assert_eq!(got_removed, want_removed, "num_threads={num_threads}");
            assert_eq!(got, want, "num_threads={num_threads}");
        }
    }

    /// A deliberately naive, obviously-correct `O(clauses^2)` reference
    /// implementation of subsumption elimination -- exactly what
    /// `eliminate_subsumed_clauses` itself was before STAGE30.md's
    /// occurrence-list rewrite (see reports/REPORT30.md), kept here
    /// purely as a test oracle rather than shipped in the real
    /// preprocessing path. Used by
    /// `test_eliminate_subsumed_clauses_matches_brute_force_on_random_formulas`
    /// to build confidence in the fast version's correctness across
    /// many random cases, not just the small number of hand-picked and
    /// real-file examples above.
    fn brute_force_subsumed_clauses(clauses: &[Clause]) -> Vec<Clause> {
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
                    continue;
                }
                if clauses[i].iter().all(|lit| clauses[j].contains(lit)) {
                    keep[j] = false;
                }
            }
        }
        clauses
            .iter()
            .zip(keep)
            .filter(|&(_, k)| k)
            .map(|(c, _)| c.clone())
            .collect()
    }

    /// Generates a random, small clause set over variables `1..=num_vars`,
    /// with clause lengths and repeated/overlapping literals
    /// deliberately likely (a small `num_vars` relative to clause count
    /// all but guarantees plenty of real subsumption relationships to
    /// exercise, unlike sampling from a huge variable space where
    /// clauses would almost never overlap at all).
    fn random_clause_set(rng: &mut StdRng, num_vars: i32, num_clauses: usize) -> Vec<Clause> {
        (0..num_clauses)
            .map(|_| {
                let length = 1 + rng.random_range(0..4);
                (0..length)
                    .map(|_| {
                        let v = 1 + rng.random_range(0..num_vars);
                        if rng.random_range(0..2) == 0 { -v } else { v }
                    })
                    .collect()
            })
            .collect()
    }

    /// Fuzz-tests the occurrence-list-based rewrite
    /// (STAGE30.md/REPORT30.md) against `brute_force_subsumed_clauses`
    /// across many random small clause sets, small enough
    /// (`num_vars`/`num_clauses` chosen so
    /// `SUBSUMPTION_WORK_BUDGET_FACTOR`'s default budget is never
    /// remotely exhausted) that any discrepancy can only be a
    /// correctness bug in the occurrence-list restriction itself, not
    /// the work-budget safety net kicking in. This is the strongest
    /// correctness check in this file for the rewrite: REPORT30.md's
    /// whole argument for why restricting candidates to a clause's
    /// rarest literal's occurrence list can never miss a real
    /// subsumption only has to be right once in the reasoning, but many
    /// random trials are cheap insurance against a transcription bug in
    /// the code that implements that reasoning.
    #[test]
    fn test_eliminate_subsumed_clauses_matches_brute_force_on_random_formulas() {
        let mut rng = StdRng::seed_from_u64(1234567890);
        const TRIALS: usize = 500;
        for trial in 0..TRIALS {
            let num_vars: i32 = 1 + rng.random_range(0..8);
            let num_clauses = rng.random_range(0..20);
            let original = random_clause_set(&mut rng, num_vars, num_clauses);

            let want = brute_force_subsumed_clauses(&original);

            let mut got = original.clone();
            eliminate_subsumed_clauses(&mut got, num_vars as usize);

            assert_eq!(
                got, want,
                "trial {trial} (num_vars={num_vars}, clauses={original:?})"
            );
        }
    }

    /// Confirms preprocessing is still fully deterministic (it always
    /// has been -- nothing in this module uses randomness) even with
    /// Stage 25's multithreaded subsumption elimination active: the
    /// same input produces byte-for-byte identical stats and
    /// simplified-problem output regardless of `num_threads`.
    #[test]
    fn test_run_produces_identical_results_regardless_of_num_threads() {
        let path = "../benchmark/blocksworld/anomaly.cnf";
        let problem =
            cnf::read_dimacs(path, 0).unwrap_or_else(|e| panic!("failed to read {path}: {e}"));

        let want = run(&problem, 0, 1);
        let want_problem = want
            .problem
            .as_ref()
            .expect("expected a simplified problem");
        for num_threads in [1, 2, 4, 8, 16] {
            let got = run(&problem, 0, num_threads);
            let got_problem = got.problem.as_ref().expect("expected a simplified problem");
            assert_eq!(
                got.stats.clauses_before, want.stats.clauses_before,
                "num_threads={num_threads}"
            );
            assert_eq!(
                got.stats.clauses_after, want.stats.clauses_after,
                "num_threads={num_threads}"
            );
            assert_eq!(
                got.stats.vars_before, want.stats.vars_before,
                "num_threads={num_threads}"
            );
            assert_eq!(
                got.stats.vars_after, want.stats.vars_after,
                "num_threads={num_threads}"
            );
            assert_eq!(
                got.stats.units_propagated, want.stats.units_propagated,
                "num_threads={num_threads}"
            );
            assert_eq!(
                got.stats.pure_literals_fixed, want.stats.pure_literals_fixed,
                "num_threads={num_threads}"
            );
            assert_eq!(
                got.stats.clauses_subsumed, want.stats.clauses_subsumed,
                "num_threads={num_threads}"
            );
            assert_eq!(
                got.stats.variables_eliminated, want.stats.variables_eliminated,
                "num_threads={num_threads}"
            );
            assert_eq!(
                got_problem.clauses, want_problem.clauses,
                "num_threads={num_threads}"
            );
        }
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

        let result = run(&problem, 0, 1);
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
        let result = run(&problem, 0, 1);
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

        let result = run(&problem, 0, 1);
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
        let result = run(&problem, 0, 1);
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
