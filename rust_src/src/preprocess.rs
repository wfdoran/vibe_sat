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
use std::time::{Duration, Instant};

use crate::assignment::{self, Assignment, Value};
use crate::cnf::{self, Clause, Literal, Problem};
use crate::params;

/// Bounds how many times the full technique pipeline (unit
/// propagation, pure literals, subsumption, variable elimination) is
/// repeated. Each round that changes anything makes the formula
/// strictly smaller in some way, so in practice this converges in far
/// fewer rounds; the bound is just a safety net.
const MAX_ROUNDS: usize = 1000;

/// The share of `--time-limit-secs` preprocessing may use (STAGE57.md):
/// when a time limit is given, preprocessing stops simplifying after
/// `limit / TIME_FRACTION` and hands whatever it has so far to the
/// search, which still gets the whole limit. With no time limit
/// preprocessing runs to its fixpoint, however long that takes.
pub const TIME_FRACTION: u32 = 10;

/// How many subsumers (or variables, in the elimination scan) are
/// processed between two looks at the clock; a power of two so the
/// check is a cheap modulus.
const DEADLINE_CHECK_INTERVAL: usize = 64;

/// Returns the moment preprocessing must stop, given the run's
/// `--time-limit-secs` as `time_limit` (`None` meaning no limit) and
/// `now`, the moment preprocessing starts: `now + time_limit /
/// TIME_FRACTION`. `None` means "no deadline" and is what a `None`
/// limit gives.
pub fn deadline(time_limit: Option<Duration>, now: Instant) -> Option<Instant> {
    time_limit.map(|limit| now + limit / TIME_FRACTION)
}

/// Reports whether `deadline` has passed. `None` means "no deadline",
/// which never expires.
fn expired(deadline: Option<Instant>) -> bool {
    deadline.is_some_and(|d| Instant::now() >= d)
}

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
    /// How many new variables Bounded Variable Addition introduced
    /// (STAGE61.md). They are numbered after the original problem's
    /// variables and only ever appear in the reduced problem.
    pub variables_added: usize,

    /// True if preprocessing stopped before reaching its fixpoint
    /// because the STAGE57.md deadline passed. The result is still a
    /// valid, satisfiability-equivalent problem, just possibly less
    /// simplified.
    pub timed_out: bool,
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
///
/// STAGE57.md: `deadline` (`None` meaning none; see [`deadline`]) bounds
/// how long preprocessing may keep simplifying. It is checked between
/// rounds and techniques, between the passes of unit propagation and
/// pure literal elimination, and inside subsumption and variable
/// elimination, always at a point where the clause set and the
/// assignment are mutually consistent, so stopping early never produces
/// a wrong answer: it only leaves the formula less simplified, and sets
/// `Stats::timed_out`. A single pass of unit propagation or pure
/// literal elimination (linear in the formula) is never interrupted
/// midway.
pub fn run(
    problem: &Problem,
    verbose: i32,
    num_threads: usize,
    p: params::Preprocess,
    deadline: Option<Instant>,
) -> PreprocessResult {
    let mut clauses = problem.clauses.clone();
    let mut assignment = assignment::new(problem.num_vars);
    let mut eliminated = Vec::new();
    let mut stats = Stats {
        clauses_before: problem.clauses.len(),
        vars_before: problem.num_vars,
        ..Stats::default()
    };

    // STAGE31.md: shared across every round below, not reset per
    // round -- see eliminate_variables's doc comment on its own
    // budget parameter for why a fresh-every-call budget would fail
    // to bound this loop's total work.
    let mut bve_budget = p.bve_work_budget_factor * problem.clauses.len();

    for _ in 0..MAX_ROUNDS {
        if expired(deadline) {
            stats.timed_out = true;
            break;
        }
        let mut changed = false;

        let (unsat, units_fixed, timed_out) =
            unit_propagate_by_deadline(&mut clauses, &mut assignment, deadline);
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
        if timed_out {
            stats.timed_out = true;
            break;
        }

        let (pure_fixed, timed_out) =
            eliminate_pure_literals(&mut clauses, &mut assignment, deadline);
        stats.pure_literals_fixed += pure_fixed;
        changed |= pure_fixed > 0;
        if timed_out {
            stats.timed_out = true;
            break;
        }

        let (subsumed, timed_out) = eliminate_subsumed_clauses_parallel(
            &mut clauses,
            problem.num_vars,
            num_threads,
            p.subsumption_work_budget_factor,
            deadline,
        );
        stats.clauses_subsumed += subsumed;
        changed |= subsumed > 0;
        if timed_out {
            stats.timed_out = true;
            break;
        }

        let (new_steps, timed_out) = eliminate_variables(
            &mut clauses,
            &assignment,
            problem.num_vars,
            &mut bve_budget,
            deadline,
        );
        if !new_steps.is_empty() {
            stats.variables_eliminated += new_steps.len();
            eliminated.extend(new_steps);
            changed = true;
        }
        if timed_out {
            stats.timed_out = true;
            break;
        }

        if !changed {
            break;
        }
    }

    // STAGE61.md: Bounded Variable Addition runs once, after the other
    // techniques have reached their fixpoint, so its new variables are
    // never seen by (and never undone by) variable elimination. It is
    // skipped if preprocessing already ran out of time, and when
    // bva_work_budget_factor is 0.
    let mut work_vars = problem.num_vars;
    if !stats.timed_out && p.bva_work_budget_factor > 0 {
        let mut bva_budget = (p.bva_work_budget_factor * clauses.len()) as i64;
        let (added, timed_out) =
            add_bounded_variables(&mut clauses, problem.num_vars, &mut bva_budget, deadline);
        stats.variables_added = added;
        work_vars += added;
        if timed_out {
            stats.timed_out = true;
        }
    }

    let (new_clauses, new_to_original) = renumber(&clauses, work_vars);
    let reduced = Problem {
        num_vars: new_to_original.len() - 1,
        clauses: new_clauses,
    };
    stats.clauses_after = reduced.clauses.len();
    stats.vars_after = reduced.num_vars;

    if verbose >= 1 {
        if stats.timed_out {
            println!("preprocess: time budget reached; stopped before reaching a fixpoint");
        }
        println!(
            "preprocess: vars {}->{} clauses {}->{} (units={} pure={} subsumed={} eliminated={} added={})",
            stats.vars_before,
            stats.vars_after,
            stats.clauses_before,
            stats.clauses_after,
            stats.units_propagated,
            stats.pure_literals_fixed,
            stats.clauses_subsumed,
            stats.variables_eliminated,
            stats.variables_added,
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
            // Variables added by Bounded Variable Addition (STAGE61.md)
            // are numbered past original_num_vars and have no original
            // counterpart: they are only an encoding aid, so their
            // values are dropped.
            if orig > self.original_num_vars {
                continue;
            }
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
///
/// STAGE35.md: `reduced` is allocated lazily, only once some literal
/// in `clause` actually needs dropping (is false under `assignment`)
/// -- not on every clause regardless. `unit_propagate` calls this once
/// per propagation round over the *entire* remaining clause set, and
/// by the second round onward the overwhelming majority of clauses
/// contain nothing assigned since the previous round at all, so they
/// need no new allocation at all: `clause` (already owned, moved out
/// of `old` by the loop below) is still exactly the reduced form and
/// can be kept as-is. When a literal does need dropping, `reduced` is
/// allocated once at `clause.len()` capacity -- its maximum possible
/// size, since every literal is either kept, drops the clause entirely
/// (satisfied), or discarded (false), never growing past the original
/// literal count -- and backfilled with every unassigned literal seen
/// so far (`clause[..i]`), so no further reallocation happens for the
/// rest of this clause either.
///
/// A CPU profile taken while investigating REPORT29.md's 14x
/// time-limit overrun found the original code (a fresh,
/// unpreallocated `Clause::new()` on literally every clause, every
/// round, regardless of whether anything changed) responsible for the
/// overwhelming majority of bootstrap's total time on a 6.3M-clause
/// file -- not the search loop's time-check granularity STAGE35.md was
/// originally scoped to fix, but the real dominant cost behind that
/// specific 42.65-second measurement. See reports/REPORT35.md for the
/// full before/after numbers.
///
/// STAGE52.md: `time_limit`/`start_time` (`None`/any `Instant` meaning
/// no limit, the same convention `dfs::run`'s/`cdcl::run_loop`'s own
/// search loops already use) let a caller doing time-bounded bootstrap
/// work bail out rather than running this whole clause set to
/// completion regardless of how long that takes -- see
/// `unit_propagate`'s own doc comment for why this was previously the
/// one part of a time-limited run that couldn't be interrupted at all.
/// Checked once up front (before `clauses` is even taken, so a
/// deadline that's already passed leaves `clauses` completely
/// untouched -- this is what
/// `test_simplify_with_assignment_reports_timed_out_without_mutating_clauses`
/// relies on) and again once per clause during the scan. A timeout
/// noticed mid-scan does *not* attempt to restore `clauses` to its
/// pre-call contents (Rust's owned-`Vec` `mem::take` here, unlike Go's
/// aliased in-place compaction, makes that non-trivial to do cheaply)
/// -- this is safe because every real caller (`cdcl::bootstrap`,
/// `dfs::bootstrap`) discards `clauses` entirely the moment `timed_out`
/// comes back true, so a mid-scan timeout's exact partial state is
/// never observed by anyone.
fn simplify_with_assignment(
    clauses: &mut Vec<Clause>,
    assignment: &Assignment,
    time_limit: Option<Duration>,
    start_time: Instant,
) -> (bool, bool) {
    if let Some(limit) = time_limit
        && start_time.elapsed() >= limit
    {
        return (false, true);
    }
    let old = std::mem::take(clauses);
    let mut kept = Vec::with_capacity(old.len());
    for clause in old {
        if let Some(limit) = time_limit
            && start_time.elapsed() >= limit
        {
            return (false, true);
        }
        let mut satisfied = false;
        let mut reduced: Option<Clause> = None; // lazily allocated; None means "clause unchanged so far"
        for i in 0..clause.len() {
            let lit = clause[i];
            let value = assignment[cnf::literal_var(lit)];
            if value == Value::Unassigned {
                if let Some(r) = reduced.as_mut() {
                    r.push(lit);
                }
                continue;
            }
            if assignment::literal_is_true(assignment, lit) {
                satisfied = true;
                break;
            }
            // lit is false and must be dropped: the first time this
            // happens for this clause, allocate reduced and backfill
            // every unassigned literal already seen (clause[..i],
            // which excludes this false one); every literal after this
            // point goes through the reduced.as_mut() branch above.
            if reduced.is_none() {
                let mut r = Vec::with_capacity(clause.len());
                r.extend_from_slice(&clause[..i]);
                reduced = Some(r);
            }
        }
        if satisfied {
            continue;
        }
        match reduced {
            None => {
                // No literal was ever false: clause is either empty
                // (unsat, matching the reduced.is_empty() check below)
                // or entirely unassigned literals, unchanged -- keep
                // the original (already owned) clause, no allocation
                // needed.
                if clause.is_empty() {
                    return (true, false);
                }
                kept.push(clause);
            }
            Some(reduced) => {
                if reduced.is_empty() {
                    return (true, false);
                }
                kept.push(reduced);
            }
        }
    }
    *clauses = kept;
    (false, false)
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
///
/// STAGE52.md: `time_limit`/`start_time` (`None`/any `Instant` meaning
/// no limit) are threaded straight through to
/// [`simplify_with_assignment`], and also checked once per clause in
/// the unit-clause-finding loop below -- every call site that needs
/// unbounded behavior ([`run`]'s own pipeline, which is not currently
/// time-limited at all -- see its doc comment) simply passes
/// `None`/`Instant::now()`, exactly the previous, unconditional
/// behavior. Callers doing time-bounded bootstrap work
/// (`dfs::bootstrap`, `cdcl::bootstrap`) instead get a real, prompt
/// exit: `timed_out` is true the moment either loop notices the
/// deadline has passed, mid-round if necessary, rather than only
/// between whole rounds -- this was previously the one part of a
/// time-limited run that could not be interrupted at all once started
/// (`REPORT35.md`'s own disclosed limitation; see `reports/REPORT52.md`).
pub fn unit_propagate(
    clauses: &mut Vec<Clause>,
    assignment: &mut Assignment,
    time_limit: Option<Duration>,
    start_time: Instant,
) -> (bool, usize, bool) {
    let mut num_fixed = 0;
    loop {
        let (clauses_unsat, timed_out) =
            simplify_with_assignment(clauses, assignment, time_limit, start_time);
        if timed_out {
            return (false, num_fixed, true);
        }
        if clauses_unsat {
            return (true, num_fixed, false);
        }

        let mut found_unit = false;
        for clause in clauses.iter() {
            if let Some(limit) = time_limit
                && start_time.elapsed() >= limit
            {
                return (false, num_fixed, true);
            }
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
            return (false, num_fixed, false);
        }
    }
}

/// Computes the same fixpoint as [`unit_propagate`] (every variable
/// forced by a chain of unit clauses is fixed, satisfied clauses are
/// dropped, false literals removed) but stops at the first round
/// boundary at or after `deadline`, for [`run`]'s STAGE57.md time
/// budget. `None` means no deadline.
///
/// The check sits right after a complete [`simplify_with_assignment`]
/// pass and before the unit scan assigns anything new, the one point
/// where the clause set is fully simplified under `assignment`: unit
/// clauses found but not yet applied just stay in `clauses` as ordinary
/// clauses, which every later stage (and every solver's own bootstrap)
/// already handles. That is why this does not reuse `unit_propagate`'s
/// own `time_limit`, which aborts mid-scan and leaves `clauses`
/// half-rewritten (fine for a caller that throws them away, wrong for
/// preprocessing, which keeps them). Each round is linear in the
/// formula, so a deadline is noticed within one linear pass; a long
/// implication chain still costs one pass per link (see REPORT57.md),
/// which is exactly what the deadline bounds.
///
/// Returns `(unsat, num_fixed, timed_out)`.
fn unit_propagate_by_deadline(
    clauses: &mut Vec<Clause>,
    assignment: &mut Assignment,
    deadline: Option<Instant>,
) -> (bool, usize, bool) {
    let mut num_fixed = 0;
    loop {
        let (clauses_unsat, _) =
            simplify_with_assignment(clauses, assignment, None, Instant::now());
        if clauses_unsat {
            return (true, num_fixed, false);
        }
        if expired(deadline) {
            return (false, num_fixed, true);
        }

        let mut found_unit = false;
        for clause in clauses.iter() {
            if clause.len() != 1 {
                continue;
            }
            let lit = clause[0];
            assignment[cnf::literal_var(lit)] = if cnf::literal_is_negative(lit) {
                Value::False
            } else {
                Value::True
            };
            num_fixed += 1;
            found_unit = true;
        }
        if !found_unit {
            return (false, num_fixed, false);
        }
    }
}

/// Repeatedly finds variables that appear with only one polarity
/// across all of `clauses` (never negated, or always negated) and
/// fixes them to the value that satisfies every clause containing
/// them, removing those now-satisfied clauses. Returns how many
/// variables were fixed this way.
///
/// STAGE57.md: `deadline` (`None` meaning none) is checked after each
/// complete pass (fix, then simplify), the point where `clauses` and
/// `assignment` agree; the returned flag is true if it stops there with
/// work possibly left. Each pass is linear, but a chain where fixing
/// one variable makes the next one pure needs one pass per link.
fn eliminate_pure_literals(
    clauses: &mut Vec<Clause>,
    assignment: &mut Assignment,
    deadline: Option<Instant>,
) -> (usize, bool) {
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
            return (num_fixed, false);
        }
        // Fixing a pure literal can only satisfy clauses (it never
        // introduces a new false literal elsewhere, since the
        // variable never appears with the opposite polarity), so this
        // cannot discover a contradiction. This simplify is never
        // interrupted (None): stopping mid-scan would leave `clauses`
        // inconsistent with `assignment`.
        let _ = simplify_with_assignment(clauses, assignment, None, Instant::now());
        if expired(deadline) {
            return (num_fixed, true);
        }
    }
}

// Bounds the total number of subsumer/candidate pairs
// `eliminate_subsumed_clauses`/`eliminate_subsumed_clauses_parallel`
// will ever examine, as a multiple of the clause count -- see
// `eliminate_subsumed_clauses`'s own doc comment for why this exists
// (STAGE30.md/REPORT30.md) and what it trades away. Chosen generously
// (most real instances' occurrence lists are far smaller than this)
// but small enough to guarantee the whole function is `O(clauses)` in
// the worst case: even a literal appearing in every single clause can
// only ever contribute this many candidate checks in total before the
// budget runs out.
//
// STAGE39.md: moved to
// [`params::Preprocess::subsumption_work_budget_factor`]
// (runtime-configurable); 64, named above, is [`params::default`]'s
// value for it, unchanged.

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
/// STAGE57.md: `deadline` (`None` meaning none) is looked at every
/// `DEADLINE_CHECK_INTERVAL` subsumers, counted from `lo`; the return
/// value is true if the loop stopped because it had passed. Stopping
/// there is safe for the same reason running out of budget is: keep
/// bits already cleared stay valid, the rest are untouched.
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
    deadline: Option<Instant>,
) -> bool {
    for i in lo..hi {
        if *budget == 0 {
            return false;
        }
        if (i - lo).is_multiple_of(DEADLINE_CHECK_INTERVAL) && expired(deadline) {
            return true;
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
    false
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
///
/// STAGE57.md: `deadline` (`None` meaning none) makes it stop early, at
/// a subsumer boundary, removing only what it had already found; the
/// returned flag is true if it did. Returns `(num_removed, timed_out)`.
fn eliminate_subsumed_clauses(
    clauses: &mut Vec<Clause>,
    num_vars: usize,
    work_budget_factor: usize,
    deadline: Option<Instant>,
) -> (usize, bool) {
    let occ = LiteralOccurrence::build(clauses, num_vars);
    // Cell, not a plain bool, purely so the read closure and the write
    // closure below can both capture `keep` (immutably, via Cell's
    // interior mutability) at once -- there is no actual concurrency
    // here (this whole function is single-threaded), just two
    // `Fn` closures that would otherwise alias a `&mut`.
    let keep: Vec<Cell<bool>> = vec![Cell::new(true); clauses.len()];

    let mut budget = work_budget_factor * clauses.len();
    let timed_out = try_subsume_from(
        clauses,
        &occ,
        clauses.len(),
        |i| keep[i].get(),
        |i| keep[i].set(false),
        0,
        clauses.len(),
        &mut budget,
        deadline,
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
    (num_removed, timed_out)
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
///
/// STAGE57.md: `deadline` is handed to every thread; the returned flag
/// is true if any thread stopped early because of it.
fn eliminate_subsumed_clauses_parallel(
    clauses: &mut Vec<Clause>,
    num_vars: usize,
    num_threads: usize,
    work_budget_factor: usize,
    deadline: Option<Instant>,
) -> (usize, bool) {
    if num_threads <= 1 {
        return eliminate_subsumed_clauses(clauses, num_vars, work_budget_factor, deadline);
    }

    let n = clauses.len();
    let occ = LiteralOccurrence::build(clauses, num_vars);
    let keep: Vec<AtomicBool> = (0..n).map(|_| AtomicBool::new(true)).collect();
    let cs: &Vec<Clause> = clauses;
    let chunk = n.div_ceil(num_threads).max(1);
    let per_thread_budget = (work_budget_factor * n) / num_threads;
    let any_timed_out = AtomicBool::new(false);

    thread::scope(|scope| {
        let mut start = 0;
        while start < n {
            let end = (start + chunk).min(n);
            let keep = &keep;
            let occ = &occ;
            let any_timed_out = &any_timed_out;
            scope.spawn(move || {
                let mut budget = per_thread_budget;
                if try_subsume_from(
                    cs,
                    occ,
                    n,
                    |i| keep[i].load(Ordering::SeqCst),
                    |i| keep[i].store(false, Ordering::SeqCst),
                    start,
                    end,
                    &mut budget,
                    deadline,
                ) {
                    any_timed_out.store(true, Ordering::SeqCst);
                }
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
    (num_removed, any_timed_out.load(Ordering::SeqCst))
}

/// Computes the resolvent of clauses `pos` and `neg` on variable `v`
/// (which `pos` contains positively and `neg` contains negatively),
/// omitting the literal on `v` itself. Returns `None` if the
/// resolvent would be a tautology (containing both some literal and
/// its negation), in which case it is always true and carries no
/// information, so it is discarded rather than returned.
///
/// STAGE31.md/REPORT31.md: computes exactly the same result as
/// `resolve` (a naive `O((len(pos)+len(neg))^2)` version kept in `mod
/// tests` below purely as `eliminate_variables`'s test oracle -- see
/// its doc comment there), but in `O(len(pos)+len(neg))`: `mark[w]`
/// records whether variable `w` has been seen in this call's
/// resolvent so far, and with which sign, replacing repeated linear
/// scans with a single O(1) array lookup per literal.
///
/// `mark` must be sized `num_vars+1` and start (and end, which this
/// function guarantees by cleaning up after itself via `touched`)
/// entirely zeroed, so the same `mark`/`touched` pair can be reused
/// across every call in a single `eliminate_variables` run without
/// reallocating or re-zeroing an O(num_vars) array each time -- only
/// the entries this call actually touches are reset, in `touched`'s
/// O(resolvent length) cleanup pass at the end.
fn resolve_with_marks(
    pos: &Clause,
    neg: &Clause,
    v: usize,
    mark: &mut [i8],
    touched: &mut Vec<usize>,
) -> Option<Clause> {
    touched.clear();
    let mut resolvent = Clause::new();
    let mut tautology = false;

    for &lit in pos.iter().chain(neg.iter()) {
        let w = cnf::literal_var(lit);
        if w == v {
            continue;
        }
        let sign: i8 = if cnf::literal_is_negative(lit) { -1 } else { 1 };
        if mark[w] == sign {
            continue; // duplicate literal, already in resolvent
        }
        if mark[w] == -sign {
            tautology = true;
            break;
        }
        mark[w] = sign;
        touched.push(w);
        resolvent.push(lit);
    }

    for &w in touched.iter() {
        mark[w] = 0;
    }
    if tautology { None } else { Some(resolvent) }
}

/// Filters `list` in place, dropping any index no longer live
/// (STAGE31.md: `eliminate_variables`'s occurrence lists are
/// maintained incrementally -- entries are tombstoned via `live`, not
/// physically removed from the list, when the clause they refer to is
/// replaced -- so a variable's occurrence list must be compacted like
/// this immediately before it is used, to skip stale indices left
/// over from an earlier elimination).
fn compact_live_occurrences(list: &mut Vec<usize>, live: &[bool]) {
    list.retain(|&idx| live[idx]);
}

// Bounds the total number of `resolve_with_marks` calls `run` allows
// bounded variable elimination across a *whole* preprocessing run
// (every round, not just one call to `eliminate_variables` -- see
// `budget`'s doc comment on the parameter below for why that
// distinction matters), as a multiple of the original clause count.
// The same kind of safety net STAGE30.md/REPORT30.md added for
// subsumption elimination, once that technique's own occurrence-list
// rewrite stopped being the dominant cost and exposed BVE as the
// next one (REPORT31.md). Even with the algorithmic fixes above, a
// single variable that genuinely occurs in thousands of clauses on
// both polarities still costs `O(occurrences^2)` `resolve_with_marks`
// calls if its elimination is ultimately accepted (the early-exit
// above only helps the *rejected* case) -- REPORT31.md's profiling
// measured real, legitimate files needing up to ~994x their clause
// count in resolve calls to complete BVE fully, and a genuinely
// pathological one exceeding 2845x (and still climbing) without this
// cap. 2000 sits comfortably above every legitimate value measured
// while cutting off runaway growth well before it becomes a
// multi-second, let alone unbounded, cost. Running out of budget is
// always safe, for the same reason it was for subsumption:
// abandoning an in-progress or not-yet-attempted elimination never
// changes whether the formula is satisfiable, only how compact it
// ends up.
//
// STAGE39.md: moved to [`params::Preprocess::bve_work_budget_factor`]
// (runtime-configurable); 2000, named above, is [`params::default`]'s
// value for it, unchanged. See `reports/REPORT39.md` for whether a
// sweep against `apn-sbox5-cut3-symmbreak.cnf` (REPORT31.md's
// pathological file) found a better default.

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
///
/// STAGE31.md: before this stage, every single elimination rebuilt
/// both occurrence-list arrays and rescanned the entire clause set
/// from scratch (both `O(clauses)`) before looking for the next one --
/// REPORT31.md's profiling found this was the dominant cost on some
/// real SAT Competition files, to the point of not finishing at all
/// within any reasonable time. This version builds the occurrence
/// lists once and maintains them incrementally: a clause is never
/// physically removed (that would require renumbering every later
/// index, right back to an `O(clauses)` cost), only tombstoned in
/// `live`; a variable's occurrence list is compacted -- lazily, only
/// when that variable is next considered, via `compact_live_occurrences`
/// -- so this never costs more in total than the number of tombstones
/// ever created. Newly created resolvents are appended to the end of
/// `cs` (never inserted or reordered), and their literals' occurrence
/// lists get exactly the new entry each needs, in `O(resolvent
/// length)`.
///
/// The elimination order, and therefore the exact sequence of `steps`
/// and the exact final surviving clauses, is unchanged from before
/// this stage (up to `budget` running out, which is new this stage
/// and can only change the outcome towards "less eliminated," never
/// towards incorrectness): this still restarts its scan from `v=1`
/// after every single elimination, mirroring the pre-STAGE31.md
/// control flow exactly, which is what lets
/// `test_eliminate_variables_matches_brute_force_on_random_formulas`
/// assert byte-for-byte equality against `brute_force_eliminate_variables`
/// (a kept-for-testing copy of the pre-STAGE31.md algorithm) rather
/// than only a weaker "still satisfiability-preserving" property.
///
/// `budget` is a `&mut usize`, not a value, because `run` calls this
/// once per preprocessing round: a budget freshly reset to
/// `BVE_WORK_BUDGET_FACTOR * clauses.len()` on every call would let a
/// single variable whose exploration alone exceeds the whole budget
/// get retried from scratch, with a brand new budget, every single
/// round -- up to `MAX_ROUNDS` times -- since exhausting the budget
/// partway through examining `v` neither eliminates `v` (so it stays
/// a candidate) nor marks it rejected (so nothing remembers to leave
/// it alone next round either). A caller-owned budget shared across
/// every round closes that hole: once it reaches zero, every later
/// call in the same `run` bails out on its very first candidate pair,
/// for the cost of a handful of array accesses, rather than
/// re-attempting the same expensive variable from zero each time.
///
/// STAGE57.md: `deadline` (`None` meaning none) is checked at the top of
/// every scan restart, every `DEADLINE_CHECK_INTERVAL` variables within
/// a scan, and every `DEADLINE_CHECK_INTERVAL` resolutions. A variable
/// whose resolvents were only partly computed when time ran out is not
/// eliminated, so stopping leaves every clause and every recorded step
/// consistent. The scan restarting from `v=1` after each elimination is
/// itself `O(num_vars)` per elimination (see REPORT57.md), which is
/// what this deadline bounds. Returns `(steps, timed_out)`.
fn eliminate_variables(
    clauses: &mut Vec<Clause>,
    assignment: &Assignment,
    num_vars: usize,
    budget: &mut usize,
    deadline: Option<Instant>,
) -> (Vec<EliminationStep>, bool) {
    let mut steps = Vec::new();
    let mut timed_out = false;

    let mut cs: Vec<Clause> = std::mem::take(clauses);
    let mut live: Vec<bool> = vec![true; cs.len()];

    let mut positive: Vec<Vec<usize>> = vec![Vec::new(); num_vars + 1];
    let mut negative: Vec<Vec<usize>> = vec![Vec::new(); num_vars + 1];
    for (i, clause) in cs.iter().enumerate() {
        for &lit in clause {
            let v = cnf::literal_var(lit);
            if cnf::literal_is_negative(lit) {
                negative[v].push(i);
            } else {
                positive[v].push(i);
            }
        }
    }

    // Reused across every resolve_with_marks call in this run; see
    // that function's doc comment for why this avoids an O(num_vars)
    // allocate-and-zero on every single resolution.
    let mut mark: Vec<i8> = vec![0; num_vars + 1];
    let mut touched: Vec<usize> = Vec::new();

    // rejected[v] is true once v has been fully evaluated and found to
    // increase the clause count. Whether v is eliminable is a pure
    // function of the live clauses containing v, so that verdict stays
    // valid until an elimination removes or adds a clause mentioning
    // v (see the reset below). Without this memo, the restart-from-1
    // scan re-resolves every rejected variable after every single
    // elimination -- on a large instance that dominates preprocessing
    // time and drains the work budget on repeats of the same rejection.
    let mut rejected: Vec<bool> = vec![false; num_vars + 1];

    loop {
        let mut eliminated_this_pass = false;
        let mut budget_exhausted = false;
        if expired(deadline) {
            timed_out = true;
            break;
        }
        for v in 1..=num_vars {
            if v.is_multiple_of(DEADLINE_CHECK_INTERVAL) && expired(deadline) {
                timed_out = true;
                break;
            }
            if assignment[v] != Value::Unassigned || rejected[v] {
                continue;
            }
            compact_live_occurrences(&mut positive[v], &live);
            compact_live_occurrences(&mut negative[v], &live);
            if positive[v].is_empty() || negative[v].is_empty() {
                continue; // not eliminable here: pure or absent
            }
            let pos_idx = positive[v].clone();
            let neg_idx = negative[v].clone();

            let removed_count = pos_idx.len() + neg_idx.len();
            let mut resolvents: Vec<Clause> = Vec::new();
            let mut exceeded = false;
            'resolve_loop: for &pi in &pos_idx {
                for &ni in &neg_idx {
                    if *budget == 0 {
                        budget_exhausted = true;
                        break 'resolve_loop;
                    }
                    if (*budget).is_multiple_of(DEADLINE_CHECK_INTERVAL) && expired(deadline) {
                        timed_out = true;
                        break 'resolve_loop;
                    }
                    *budget -= 1;
                    if let Some(resolvent) =
                        resolve_with_marks(&cs[pi], &cs[ni], v, &mut mark, &mut touched)
                    {
                        resolvents.push(resolvent);
                        if resolvents.len() > removed_count {
                            // STAGE31.md: bail out as soon as the
                            // non-increasing check below is guaranteed
                            // to reject v, instead of computing every
                            // remaining resolvent (REPORT31.md's
                            // profiling found resolve calls for
                            // ultimately-rejected high-degree
                            // variables were a large share of total
                            // preprocessing time). Once this count is
                            // exceeded it can only grow, never shrink,
                            // so the eventual decision below is
                            // already determined.
                            exceeded = true;
                            break 'resolve_loop;
                        }
                    }
                }
            }
            if budget_exhausted || timed_out {
                // Not enough budget (or time) left to even fully evaluate
                // this variable, let alone any variable after it -- stop
                // attempting further eliminations entirely rather
                // than accept v based on an incomplete resolvent set.
                break;
            }
            if exceeded {
                rejected[v] = true;
                continue; // would increase the clause count; not worth it
            }

            let positive_clauses: Vec<Clause> = pos_idx.iter().map(|&i| cs[i].clone()).collect();
            let negative_clauses: Vec<Clause> = neg_idx.iter().map(|&i| cs[i].clone()).collect();
            steps.push(EliminationStep {
                var: v,
                positive: positive_clauses,
                negative: negative_clauses,
            });

            for &idx in pos_idx.iter().chain(neg_idx.iter()) {
                live[idx] = false;
                for &lit in &cs[idx] {
                    rejected[cnf::literal_var(lit)] = false;
                }
            }
            for resolvent in resolvents {
                let new_idx = cs.len();
                for &lit in &resolvent {
                    let w = cnf::literal_var(lit);
                    rejected[w] = false;
                    if cnf::literal_is_negative(lit) {
                        negative[w].push(new_idx);
                    } else {
                        positive[w].push(new_idx);
                    }
                }
                cs.push(resolvent);
                live.push(true);
            }

            eliminated_this_pass = true;
            break; // restart from v=1, exactly as before STAGE31.md
        }

        if budget_exhausted || timed_out || !eliminated_this_pass {
            break;
        }
    }

    let final_clauses: Vec<Clause> = cs
        .into_iter()
        .zip(live)
        .filter(|(_, live)| *live)
        .map(|(clause, _)| clause)
        .collect();
    *clauses = final_clauses;
    (steps, timed_out)
}

// STAGE61.md: Bounded Variable Addition (BVA), the complement of
// bounded variable elimination. Where BVE removes a variable by
// resolving it away, BVA introduces a fresh variable `x` to replace a
// grid of clauses that share structure, shrinking the clause count.
//
// If a formula contains every clause `(lit_i OR C_j)` for a set of
// literals `{lit_1..lit_a}` and a set of clause remainders
// `{C_1..C_b}`, that is `a*b` clauses. Adding a variable `x` and
// replacing the grid with
//
//     (lit_i OR x)       for every i      (a clauses)
//     (NOT x OR C_j)     for every j      (b clauses)
//
// gives `a+b` clauses, and resolving the new clauses on `x` gives back
// exactly the old grid. So the new formula is satisfiable iff the old
// one is, and every model of the new formula, restricted to the old
// variables, is a model of the old one. The reduction is `a*b - a - b`,
// which is positive from a 2x3 grid up.
//
// This is the SimpleBVA procedure of Manthey, Heule & Biere,
// "Automated Reencoding of Boolean Formulas," HVC 2012: take the
// literal with the most occurrences, grow the set of literals greedily
// by whichever literal pairs with the most clauses, and keep growing
// while the reduction improves. The classic target is a pairwise
// at-most-one constraint, which BVA reencodes with far fewer clauses.
//
// Reference: Manthey, Heule & Biere, "Automated Reencoding of Boolean
// Formulas," Haifa Verification Conference 2012.

/// Maps a literal to a dense non-negative index: `2*var` for a positive
/// literal and `2*var+1` for a negative one. Ties between equally good
/// choices in BVA always go to the smaller code, which keeps the Go and
/// Rust implementations making identical choices.
fn literal_code(lit: Literal) -> usize {
    2 * cnf::literal_var(lit) + usize::from(cnf::literal_is_negative(lit))
}

/// Returns how many clauses replacing a grid of `num_lits` literals by
/// `num_clauses` clause remainders saves: `num_lits*num_clauses`
/// clauses become `num_lits+num_clauses`.
fn bva_reduction(num_lits: usize, num_clauses: usize) -> i64 {
    (num_lits * num_clauses) as i64 - num_lits as i64 - num_clauses as i64
}

/// Applies Bounded Variable Addition once to `clauses`, whose variables
/// are numbered `1..=num_vars`, and returns how many new variables it
/// added plus whether the deadline (not the budget) stopped it. New
/// variables are numbered `num_vars+1`, `num_vars+2`, ... in the order
/// they are added; the clause set is rewritten in place (replaced
/// clauses are dropped and the new clauses go at the end).
///
/// The formula must already be free of unit clauses, tautologies, and
/// variables fixed by an assignment, as [`run`] guarantees; a clause
/// with fewer than two literals is simply never used. The result is
/// satisfiability-equivalent to the input and every model of it,
/// restricted to variables `1..=num_vars`, is a model of the input.
///
/// `budget` is the remaining work allowance, in clause comparisons (one
/// per candidate partner clause examined); when it reaches zero no
/// further literal is tried. `deadline` is checked every
/// `DEADLINE_CHECK_INTERVAL` units of work. Both limits are always
/// safe: a replacement is applied only after it has been fully worked
/// out, so stopping just leaves the formula less reduced.
fn add_bounded_variables(
    clauses: &mut Vec<Clause>,
    num_vars: usize,
    budget: &mut i64,
    deadline: Option<Instant>,
) -> (usize, bool) {
    use std::cmp::Reverse;
    use std::collections::{BinaryHeap, HashMap, HashSet};

    let mut cs = std::mem::take(clauses);
    let mut num_vars = num_vars;
    let mut num_added = 0usize;
    let mut timed_out = false;
    let mut live = vec![true; cs.len()];

    // occurrences[code] lists the indices of clauses containing that
    // literal, in increasing order; dead clauses are dropped lazily.
    // count[code] is the number of live clauses containing it.
    let mut occurrences: Vec<Vec<usize>> = vec![Vec::new(); 2 * (num_vars + 1)];
    let mut count = vec![0usize; 2 * (num_vars + 1)];
    for (i, clause) in cs.iter().enumerate() {
        for &lit in clause {
            let code = literal_code(lit);
            occurrences[code].push(i);
            count[code] += 1;
        }
    }
    // mark[code] == stamp means "in the current clause".
    let mut mark = vec![0usize; 2 * (num_vars + 1)];
    let mut stamp = 0usize;

    // Max-heap by higher count first, then smaller code first. An entry
    // goes stale when its count changes; a stale entry is skipped when
    // popped (a fresh one was pushed at the change).
    let mut queue: BinaryHeap<(usize, Reverse<usize>)> = BinaryHeap::new();
    for (code, &c) in count.iter().enumerate().skip(2) {
        if c >= 2 {
            queue.push((c, Reverse(code)));
        }
    }

    let mut work_since_check = 0usize;
    'literals: while let Some((entry_count, Reverse(l))) = queue.pop() {
        if expired(deadline) {
            timed_out = true;
            break;
        }
        if *budget <= 0 {
            break;
        }
        if entry_count != count[l] {
            continue; // stale
        }
        compact_live_occurrences(&mut occurrences[l], &live);

        // grid: the clauses still in the candidate grid. chosen: the
        // literals chosen so far (always starting with l itself).
        let mut grid: Vec<usize> = occurrences[l]
            .iter()
            .copied()
            .filter(|&idx| cs[idx].len() >= 2)
            .collect();
        if grid.len() < 2 {
            continue;
        }
        let mut chosen = vec![l];
        // partner_of[k][c] = clause for chosen[k+1] paired with clause c.
        let mut partner_of: Vec<HashMap<usize, usize>> = Vec::new();

        loop {
            let mut pairs: HashMap<usize, Vec<(usize, usize)>> = HashMap::new();
            for &c in &grid {
                stamp += 1;
                let clause_len = cs[c].len();
                let mut lmin: Option<usize> = None;
                for &lit in &cs[c] {
                    let code = literal_code(lit);
                    if code == l {
                        continue;
                    }
                    mark[code] = stamp;
                    if lmin.is_none_or(|m| count[code] < count[m]) {
                        lmin = Some(code);
                    }
                }
                let Some(lmin) = lmin else {
                    continue; // a clause of one repeated literal; nothing to pair
                };
                compact_live_occurrences(&mut occurrences[lmin], &live);
                for &d in &occurrences[lmin] {
                    if d == c || cs[d].len() != clause_len {
                        continue;
                    }
                    *budget -= 1;
                    work_since_check += 1;
                    if work_since_check >= DEADLINE_CHECK_INTERVAL {
                        work_since_check = 0;
                        if expired(deadline) {
                            timed_out = true;
                            break 'literals;
                        }
                    }
                    if *budget <= 0 {
                        break 'literals;
                    }
                    let mut marked = 0usize;
                    let mut other: Option<usize> = None;
                    for &lit in &cs[d] {
                        let code = literal_code(lit);
                        if mark[code] == stamp {
                            marked += 1;
                        } else {
                            other = Some(code);
                        }
                    }
                    // d must hold every literal of c except l, plus exactly
                    // one other literal, which is not on l's variable.
                    let Some(other) = other else { continue };
                    if marked != clause_len - 1 || other >> 1 == l >> 1 {
                        continue;
                    }
                    pairs.entry(other).or_default().push((c, d));
                }
            }

            let mut best: Option<usize> = None;
            for (&code, list) in &pairs {
                if chosen.contains(&code) {
                    continue;
                }
                let better = match best {
                    None => true,
                    Some(b) => {
                        list.len() > pairs[&b].len() || (list.len() == pairs[&b].len() && code < b)
                    }
                };
                if better {
                    best = Some(code);
                }
            }
            let Some(best) = best else { break };
            if bva_reduction(chosen.len() + 1, pairs[&best].len())
                <= bva_reduction(chosen.len(), grid.len())
            {
                break;
            }
            let mut partners = HashMap::with_capacity(pairs[&best].len());
            grid.clear();
            for &(c, d) in &pairs[&best] {
                partners.insert(c, d);
                grid.push(c);
            }
            chosen.push(best);
            partner_of.push(partners);
        }
        if chosen.len() < 2 || bva_reduction(chosen.len(), grid.len()) <= 0 {
            continue;
        }

        // The grid can name the same clause twice (clause {a,b,r} is both
        // "a with remainder {b,r}" and "b with remainder {a,r}"), so count
        // the distinct clauses actually removed: a replacement that does
        // not strictly shrink the formula is skipped, which also rules out
        // an endless cycle of replacements.
        let mut removed_ids: HashSet<usize> = HashSet::new();
        for &c in &grid {
            removed_ids.insert(c);
            for partners in &partner_of {
                removed_ids.insert(partners[&c]);
            }
        }
        if removed_ids.len() <= chosen.len() + grid.len() {
            continue;
        }
        *budget -= removed_ids.len() as i64;

        // Apply the replacement. Remove the grid first, collecting the
        // literals whose counts drop so they can be requeued.
        let mut touched: Vec<usize> = Vec::new();
        let mut remove =
            |idx: usize, live: &mut Vec<bool>, count: &mut Vec<usize>, cs: &Vec<Clause>| {
                if !live[idx] {
                    return;
                }
                live[idx] = false;
                for &lit in &cs[idx] {
                    let code = literal_code(lit);
                    count[code] -= 1;
                    touched.push(code);
                }
            };
        for &c in &grid {
            remove(c, &mut live, &mut count, &cs);
            for partners in &partner_of {
                remove(partners[&c], &mut live, &mut count, &cs);
            }
        }

        num_vars += 1;
        num_added += 1;
        occurrences.push(Vec::new());
        occurrences.push(Vec::new());
        count.push(0);
        count.push(0);
        mark.push(0);
        mark.push(0);
        let x = num_vars as Literal;
        let mut new_clauses: Vec<Clause> = Vec::with_capacity(chosen.len() + grid.len());
        for &code in &chosen {
            let lit = (code >> 1) as Literal;
            new_clauses.push(vec![if code & 1 == 1 { -lit } else { lit }, x]);
        }
        for &c in &grid {
            let mut rest: Clause = cs[c]
                .iter()
                .copied()
                .filter(|&lit| literal_code(lit) != l)
                .collect();
            rest.push(-x);
            new_clauses.push(rest);
        }
        for clause in new_clauses {
            let idx = cs.len();
            for &lit in &clause {
                let code = literal_code(lit);
                occurrences[code].push(idx);
                count[code] += 1;
                touched.push(code);
            }
            cs.push(clause);
            live.push(true);
        }

        stamp += 1;
        for code in touched {
            if mark[code] == stamp || count[code] < 2 {
                continue;
            }
            mark[code] = stamp;
            queue.push((count[code], Reverse(code)));
        }
    }

    *clauses = cs
        .into_iter()
        .zip(live)
        .filter_map(|(clause, alive)| alive.then_some(clause))
        .collect();
    (num_added, timed_out)
}

#[cfg(test)]
mod tests {
    use super::*;
    use rand::RngExt;
    use rand::SeedableRng;
    use rand::rngs::StdRng;
    use std::collections::HashSet;

    /// Computes the resolvent of clauses `pos` and `neg` on variable
    /// `v`, omitting the literal on `v` itself, or `None` if it would
    /// be a tautology. This is exactly what `eliminate_variables`
    /// itself used before STAGE31.md's `resolve_with_marks` rewrite
    /// (see reports/REPORT31.md) -- kept here, deliberately naive and
    /// `O((len(pos)+len(neg))^2)`, purely as a test oracle for
    /// `resolve_with_marks` and `brute_force_eliminate_variables`
    /// below, not shipped in the real preprocessing path.
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

        let (unsat, num_fixed, _) =
            unit_propagate(&mut clauses, &mut assignment, None, Instant::now());
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

        let (unsat, _, _) = unit_propagate(&mut clauses, &mut assignment, None, Instant::now());
        assert!(unsat);
    }

    /// STAGE52.md's core mechanism: with an already-elapsed deadline (a
    /// zero time_limit against Instant::now()), unit_propagate must
    /// report timed_out = true immediately, before ever propagating the
    /// chain of unit clauses it would otherwise find -- unsat must be
    /// false (timing out means "don't know", never "proven
    /// unsatisfiable") and num_fixed must be 0, confirming no work
    /// happened after the deadline was noticed.
    #[test]
    fn test_unit_propagate_reports_timed_out_before_finding_unit_clause() {
        let mut clauses = vec![vec![1], vec![-1, 2]];
        let mut assignment = assignment::new(2);
        let already_elapsed = Duration::from_secs(0);

        let (unsat, num_fixed, timed_out) = unit_propagate(
            &mut clauses,
            &mut assignment,
            Some(already_elapsed),
            Instant::now(),
        );
        assert!(
            timed_out,
            "expected timed_out = true with an already-elapsed deadline"
        );
        assert!(
            !unsat,
            "expected unsat = false on timeout -- timing out is 'unknown', never 'proven unsatisfiable'"
        );
        assert_eq!(
            num_fixed, 0,
            "num_fixed = {num_fixed}, want 0 (deadline noticed before any unit clause was propagated)"
        );
    }

    /// STAGE35.md: verifies simplify_with_assignment's lazy-allocation
    /// optimization directly -- a clause containing no assigned
    /// literal at all must come back as the exact same underlying
    /// allocation (no reallocation), while a clause with a false
    /// literal to drop must come back as a genuinely new, correctly
    /// reduced Vec.
    #[test]
    fn test_simplify_with_assignment_reuses_unchanged_clauses() {
        let unchanged = vec![1, 2];
        let unchanged_ptr = unchanged.as_ptr();
        let to_reduce = vec![-3, 4, 5];
        let mut clauses = vec![unchanged, to_reduce];

        let mut assignment = assignment::new(5);
        assignment[3] = Value::True; // makes literal -3 false, dropping it from to_reduce

        let (unsat, _) = simplify_with_assignment(&mut clauses, &assignment, None, Instant::now());
        assert!(!unsat, "expected no contradiction");
        assert_eq!(clauses.len(), 2, "clauses = {clauses:?}, want 2 kept");
        assert_eq!(
            clauses[0].as_ptr(),
            unchanged_ptr,
            "the untouched clause should be the exact same underlying allocation, not a copy"
        );
        assert_eq!(clauses[1], vec![4, 5]);
    }

    /// STAGE35.md: covers the two other outcomes simplify_with_assignment
    /// can produce for a clause: dropped entirely (some literal is
    /// true), and unsat (every literal is false, or the clause started
    /// out empty).
    #[test]
    fn test_simplify_with_assignment_drops_satisfied_and_detects_empty_clause() {
        let mut assignment = assignment::new(2);
        assignment[1] = Value::True;

        let mut satisfied = vec![vec![1, -2]];
        assert!(!simplify_with_assignment(&mut satisfied, &assignment, None, Instant::now()).0);
        assert!(
            satisfied.is_empty(),
            "satisfied clause should have been dropped entirely, got {satisfied:?}"
        );

        let mut all_false = vec![vec![-1]];
        assert!(
            simplify_with_assignment(&mut all_false, &assignment, None, Instant::now()).0,
            "expected unsat when every literal in a clause is false"
        );

        let mut empty: Vec<Clause> = vec![vec![]];
        assert!(
            simplify_with_assignment(&mut empty, &assignment, None, Instant::now()).0,
            "expected unsat for an originally empty clause"
        );
    }

    /// STAGE52.md's core mechanism: with an already-elapsed deadline (a
    /// zero time_limit against Instant::now()), simplify_with_assignment
    /// must report timed_out = true and leave `clauses` completely
    /// untouched -- checked before `clauses` is ever taken, so a caller
    /// that abandons bootstrap on timeout never observes a
    /// partially-simplified, inconsistent clause set.
    #[test]
    fn test_simplify_with_assignment_reports_timed_out_without_mutating_clauses() {
        let original = vec![vec![1, 2], vec![-1]];
        let mut clauses = original.clone();
        let mut assignment = assignment::new(2);
        assignment[1] = Value::True; // would satisfy clause 0 and drop clause 1's only literal if this ran
        let already_elapsed = Duration::from_secs(0);

        let (unsat, timed_out) = simplify_with_assignment(
            &mut clauses,
            &assignment,
            Some(already_elapsed),
            Instant::now(),
        );
        assert!(
            timed_out,
            "expected timed_out = true with an already-elapsed deadline"
        );
        assert!(!unsat, "expected unsat = false on timeout");
        assert_eq!(
            clauses, original,
            "clauses = {clauses:?}, want unchanged {original:?} (timeout must be noticed before any compaction)"
        );
    }

    #[test]
    fn test_eliminate_pure_literals() {
        let mut clauses = vec![vec![1, 2], vec![1, -2]];
        let mut assignment = assignment::new(2);

        let (num_fixed, timed_out) = eliminate_pure_literals(&mut clauses, &mut assignment, None);
        assert!(!timed_out);
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
            None,
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
        let (num_removed, _) = eliminate_subsumed_clauses(
            &mut clauses,
            3,
            params::default().preprocess.subsumption_work_budget_factor,
            None,
        );
        assert_eq!(num_removed, 1);
        assert_eq!(clauses.len(), 1);
    }

    #[test]
    fn test_eliminate_subsumed_clauses_removes_duplicates() {
        let mut clauses = vec![vec![1, -2], vec![1, -2]];
        let (num_removed, _) = eliminate_subsumed_clauses(
            &mut clauses,
            2,
            params::default().preprocess.subsumption_work_budget_factor,
            None,
        );
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
        let (want_removed, _) = eliminate_subsumed_clauses(
            &mut want,
            4,
            params::default().preprocess.subsumption_work_budget_factor,
            None,
        );

        for num_threads in [1, 2, 3, 4, 8] {
            let mut got = original.clone();
            let (got_removed, _) = eliminate_subsumed_clauses_parallel(
                &mut got,
                4,
                num_threads,
                params::default().preprocess.subsumption_work_budget_factor,
                None,
            );
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
        let (want_removed, _) = eliminate_subsumed_clauses(
            &mut want,
            problem.num_vars,
            params::default().preprocess.subsumption_work_budget_factor,
            None,
        );
        assert!(
            want_removed > 0,
            "test file has nothing to subsume; pick a different file"
        );

        for num_threads in [1, 2, 3, 4, 8, 16] {
            let mut got = problem.clauses.clone();
            let (got_removed, _) = eliminate_subsumed_clauses_parallel(
                &mut got,
                problem.num_vars,
                num_threads,
                params::default().preprocess.subsumption_work_budget_factor,
                None,
            );
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
            eliminate_subsumed_clauses(
                &mut got,
                num_vars as usize,
                params::default().preprocess.subsumption_work_budget_factor,
                None,
            );

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

        let want = run(&problem, 0, 1, params::default().preprocess, None);
        let want_problem = want
            .problem
            .as_ref()
            .expect("expected a simplified problem");
        for num_threads in [1, 2, 4, 8, 16] {
            let got = run(&problem, 0, num_threads, params::default().preprocess, None);
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

    /// Checks `resolve_with_marks` against `resolve` directly
    /// (tautology detection, intra-clause duplicate-literal handling,
    /// and the resolvent contents), across several calls that
    /// deliberately reuse the same mark/touched buffers and revisit
    /// overlapping variables -- STAGE31.md/REPORT31.md: `mark` is
    /// reset only for the entries a call actually touched, not with a
    /// full clear, so a bug in that cleanup would only show up as a
    /// leftover mark corrupting a *later* call that happens to touch
    /// the same variable, which is exactly what this sequence of
    /// cases is designed to exercise. The final check on `mark` itself
    /// confirms nothing was left set after the last call.
    #[test]
    fn test_resolve_with_marks_matches_resolve_across_reused_buffers() {
        let mut mark: Vec<i8> = vec![0; 6];
        let mut touched: Vec<usize> = Vec::new();

        let cases: Vec<(Clause, Clause, usize)> = vec![
            (vec![1, 2], vec![-1, 3], 1),
            (vec![2, 3], vec![-2, 4], 2),
            (vec![1, 2], vec![-1, -2], 1),
            (vec![3, 4], vec![-3, 4], 3),
            (vec![5], vec![-5], 5),
        ];

        for (pos, neg, v) in &cases {
            let want = resolve(pos, neg, *v);
            let got = resolve_with_marks(pos, neg, *v, &mut mark, &mut touched);
            match (&want, &got) {
                (None, None) => {}
                (Some(w), Some(g)) => {
                    let want_set: HashSet<Literal> = w.iter().copied().collect();
                    let got_set: HashSet<Literal> = g.iter().copied().collect();
                    assert_eq!(got_set, want_set, "pos={pos:?} neg={neg:?} v={v}");
                }
                _ => panic!(
                    "pos={pos:?} neg={neg:?} v={v}: resolve = {want:?}, resolve_with_marks = {got:?}"
                ),
            }
        }

        assert!(mark.iter().all(|&m| m == 0), "leaked mark: {mark:?}");
    }

    /// A `BVE_WORK_BUDGET_FACTOR`-style budget large enough that no
    /// test using it is exercising STAGE31.md's work-budget cap --
    /// that cap has its own dedicated tests below; every other
    /// `eliminate_variables` test wants the pre-STAGE31.md, uncapped
    /// behavior.
    fn huge_budget() -> usize {
        1 << 30
    }

    #[test]
    fn test_eliminate_variables_non_increasing() {
        let mut clauses = vec![vec![1, 2], vec![-1, 3]];
        let assignment = assignment::new(3);

        let (steps, _) =
            eliminate_variables(&mut clauses, &assignment, 3, &mut huge_budget(), None);
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

        let (steps, _) =
            eliminate_variables(&mut clauses, &assignment, 6, &mut huge_budget(), None);
        assert!(steps.is_empty());
    }

    /// Verifies the rejection memo: variable 1 (2 positive x 3
    /// negative occurrences = 6 resolvents > 5 clauses) is rejected
    /// after costing 6 budget units, variable 7 is then eliminated for
    /// 1 unit, and the restart-from-1 scan that follows must skip
    /// variable 1 instead of paying 6 more. Total spend is therefore
    /// exactly 7 (13 without the memo).
    #[test]
    fn test_eliminate_variables_does_not_re_evaluate_rejected() {
        let mut clauses = vec![
            vec![1, 2],
            vec![1, 3],
            vec![-1, 4],
            vec![-1, 5],
            vec![-1, 6],
            vec![7, 8],
            vec![-7, 9],
        ];
        let assignment = assignment::new(9);
        let mut budget = 100;

        let (steps, _) = eliminate_variables(&mut clauses, &assignment, 9, &mut budget, None);
        assert_eq!(steps.len(), 1);
        assert_eq!(steps[0].var, 7);
        assert_eq!(
            100 - budget,
            7,
            "rejected variable 1 must not be re-resolved"
        );
    }

    /// Verifies the memo is dropped when it stops being true. Variable
    /// 1 is rejected at first (2 x 3 = 6 resolvents > 5 clauses).
    /// Eliminating variable 7 merges the two clauses {-1,4,7} and
    /// {-1,4,-7} into {-1,4}, leaving variable 1 with 2 x 2 = 4
    /// resolvents for 4 clauses, which is acceptable -- so variable 1
    /// must be eliminated on a later scan.
    #[test]
    fn test_eliminate_variables_rejection_invalidated_by_neighbour() {
        let mut clauses = vec![
            vec![1, 2],
            vec![1, 3],
            vec![-1, 4, 7],
            vec![-1, 4, -7],
            vec![-1, 5],
        ];
        let assignment = assignment::new(7);

        let (steps, _) =
            eliminate_variables(&mut clauses, &assignment, 7, &mut huge_budget(), None);
        let vars: Vec<usize> = steps.iter().map(|s| s.var).collect();
        assert_eq!(vars, vec![7, 1]);
    }

    /// A deliberately naive, obviously-correct reference implementation
    /// of bounded variable elimination -- exactly what
    /// `eliminate_variables` itself was before STAGE31.md's
    /// incremental-occurrence-list rewrite (see reports/REPORT31.md),
    /// kept here purely as a test oracle rather than shipped in the
    /// real preprocessing path. Used by
    /// `test_eliminate_variables_matches_brute_force_on_random_formulas`
    /// to build confidence in the fast version's correctness across
    /// many random cases, not just the small number of hand-picked
    /// examples above.
    fn brute_force_eliminate_variables(
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
                    continue;
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
                    continue;
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
                break;
            }

            if !eliminated_this_pass {
                return steps;
            }
        }
    }

    /// Reports whether `a` and `b` record the same eliminated variable
    /// from the same positive/negative clauses.
    fn same_elimination_step(a: &EliminationStep, b: &EliminationStep) -> bool {
        a.var == b.var && a.positive == b.positive && a.negative == b.negative
    }

    /// Fuzz-tests the incremental-occurrence-list rewrite
    /// (STAGE31.md/REPORT31.md) against `brute_force_eliminate_variables`
    /// across many random small clause sets, reusing Stage 30's
    /// `random_clause_set` generator. Both algorithms restart their
    /// elimination scan from `v=1` after every single elimination and
    /// call `resolve`/`resolve_with_marks` in the same order, so
    /// REPORT31.md's claim that this stage changed only internal
    /// bookkeeping -- never which variables get eliminated, in what
    /// order, or what the final clauses are -- is checked here as
    /// exact equality (steps and surviving clauses, both in order),
    /// not merely a weaker "still satisfiable" property.
    #[test]
    fn test_eliminate_variables_matches_brute_force_on_random_formulas() {
        let mut rng = StdRng::seed_from_u64(2468013579);
        const TRIALS: usize = 500;
        for trial in 0..TRIALS {
            let num_vars: i32 = 1 + rng.random_range(0..8);
            let num_clauses = rng.random_range(0..20);
            let original = random_clause_set(&mut rng, num_vars, num_clauses);

            let mut want = original.clone();
            let want_steps = brute_force_eliminate_variables(
                &mut want,
                &assignment::new(num_vars as usize),
                num_vars as usize,
            );

            let mut got = original.clone();
            let (got_steps, _) = eliminate_variables(
                &mut got,
                &assignment::new(num_vars as usize),
                num_vars as usize,
                &mut huge_budget(),
                None,
            );

            assert_eq!(
                got_steps.len(),
                want_steps.len(),
                "trial {trial} (num_vars={num_vars}, clauses={original:?})"
            );
            for (g, w) in got_steps.iter().zip(want_steps.iter()) {
                assert!(
                    same_elimination_step(g, w),
                    "trial {trial}: step {g:?}, want {w:?}"
                );
            }
            assert_eq!(
                got, want,
                "trial {trial} (num_vars={num_vars}, clauses={original:?})"
            );
        }
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

        let result = run(&problem, 0, 1, params::default().preprocess, None);
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
        let result = run(&problem, 0, 1, params::default().preprocess, None);
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

        let result = run(&problem, 0, 1, params::default().preprocess, None);
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
        let result = run(&problem, 0, 1, params::default().preprocess, None);
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

    // ---- STAGE57.md: preprocessing deadline ----

    /// A deadline that has certainly already expired.
    fn past_deadline() -> Option<Instant> {
        Instant::now().checked_sub(Duration::from_secs(3600))
    }

    /// A deadline that will not expire during a test.
    fn future_deadline() -> Option<Instant> {
        Some(Instant::now() + Duration::from_secs(3600))
    }

    /// `(x1), (-x1 v x2), ..., (-x_{n-1} v x_n)`.
    fn chain_clauses(n: i32) -> Vec<Clause> {
        let mut clauses: Vec<Clause> = vec![vec![1]];
        for i in 1..n {
            clauses.push(vec![-i, i + 1]);
        }
        clauses
    }

    fn brute_force_satisfiable(clauses: &[Clause], num_vars: usize) -> bool {
        (0u32..(1 << num_vars)).any(|mask| {
            let mut a = assignment::new(num_vars);
            for (v, value) in a.iter_mut().enumerate().skip(1) {
                *value = if mask & (1 << (v - 1)) != 0 {
                    Value::True
                } else {
                    Value::False
                };
            }
            all_satisfied(clauses, &a)
        })
    }

    #[test]
    fn test_deadline_none_limit_means_no_deadline() {
        assert!(deadline(None, Instant::now()).is_none());
    }

    #[test]
    fn test_deadline_is_one_tenth_of_limit() {
        let now = Instant::now();
        for (limit, want) in [
            (Duration::from_secs(10), Duration::from_secs(1)),
            (Duration::from_secs(100), Duration::from_secs(10)),
            (Duration::from_secs(1), Duration::from_millis(100)),
            (Duration::ZERO, Duration::ZERO),
        ] {
            assert_eq!(deadline(Some(limit), now), Some(now + want), "{limit:?}");
        }
    }

    #[test]
    fn test_expired() {
        assert!(!expired(None), "no deadline must never expire");
        assert!(expired(past_deadline()), "a past deadline must be expired");
        assert!(
            !expired(future_deadline()),
            "a future deadline must not be expired"
        );
    }

    #[test]
    fn test_unit_propagate_by_deadline_matches_unit_propagate_without_deadline() {
        let mut rng = StdRng::seed_from_u64(57);
        for trial in 0..200 {
            let num_vars: i32 = 3 + rng.random_range(0..8);
            let num_base = 2 + rng.random_range(0..14);
            let mut base = random_clause_set(&mut rng, num_vars, num_base);
            for _ in 0..(1 + rng.random_range(0..3)) {
                let v = 1 + rng.random_range(0..num_vars);
                base.push(vec![if rng.random_range(0..2) == 0 { -v } else { v }]);
            }

            let mut want = base.clone();
            let mut want_assignment = assignment::new(num_vars as usize);
            let (want_unsat, want_fixed, _) =
                unit_propagate(&mut want, &mut want_assignment, None, Instant::now());

            let mut got = base.clone();
            let mut got_assignment = assignment::new(num_vars as usize);
            let (got_unsat, got_fixed, got_timed_out) =
                unit_propagate_by_deadline(&mut got, &mut got_assignment, None);

            assert!(!got_timed_out, "trial {trial}: timed out with no deadline");
            assert_eq!(got_unsat, want_unsat, "trial {trial}");
            if got_unsat {
                continue;
            }
            assert_eq!(got_fixed, want_fixed, "trial {trial}");
            assert_eq!(got_assignment, want_assignment, "trial {trial}");
            assert_eq!(got.len(), want.len(), "trial {trial}");
        }
    }

    #[test]
    fn test_unit_propagate_by_deadline_stops_consistently_when_expired() {
        let mut clauses = chain_clauses(5);
        let original = clauses.clone();
        let mut assignment = assignment::new(5);

        let (unsat, num_fixed, timed_out) =
            unit_propagate_by_deadline(&mut clauses, &mut assignment, past_deadline());
        assert!(!unsat);
        assert!(timed_out);
        assert_eq!(num_fixed, 0, "stop before the first unit scan");
        assert_eq!(clauses, original);
    }

    #[test]
    fn test_unit_propagate_by_deadline_reaches_fixpoint_with_far_deadline() {
        let mut clauses = chain_clauses(6);
        let mut assignment = assignment::new(6);
        let (_, _, timed_out) =
            unit_propagate_by_deadline(&mut clauses, &mut assignment, future_deadline());
        assert!(!timed_out);
        for (v, &value) in assignment.iter().enumerate().skip(1) {
            assert_eq!(value, Value::True, "variable {v}");
        }
        assert!(clauses.is_empty());
    }

    #[test]
    fn test_eliminate_pure_literals_stops_after_one_pass_when_expired() {
        // (x1 v -x2), (x2 v -x3), (x3 v -x4): x1 is pure, then x2
        // becomes pure, and so on; one pass per link.
        let chain = || -> Vec<Clause> { vec![vec![1, -2], vec![2, -3], vec![3, -4]] };

        let mut clauses = chain();
        let mut assignment = assignment::new(4);
        let (num_fixed, timed_out) =
            eliminate_pure_literals(&mut clauses, &mut assignment, past_deadline());
        assert!(timed_out);
        assert!(num_fixed >= 1, "want at least one full pass");
        for clause in &clauses {
            for &lit in clause {
                assert_eq!(
                    assignment[cnf::literal_var(lit)],
                    Value::Unassigned,
                    "clause {clause:?} still mentions an assigned variable"
                );
            }
        }

        // Without a deadline the whole chain resolves.
        let mut clauses = chain();
        let mut assignment = assignment::new(4);
        let (num_fixed, timed_out) = eliminate_pure_literals(&mut clauses, &mut assignment, None);
        assert!(!timed_out);
        assert!(clauses.is_empty());
        assert!(num_fixed >= 3);
    }

    #[test]
    fn test_eliminate_subsumed_clauses_removes_nothing_when_expired() {
        let mut clauses: Vec<Clause> = vec![vec![1, 2], vec![1, 2, 3]];
        let (num_removed, timed_out) =
            eliminate_subsumed_clauses(&mut clauses, 3, 64, past_deadline());
        assert!(timed_out);
        assert_eq!(num_removed, 0);
        assert_eq!(clauses.len(), 2);

        let (num_removed, timed_out) = eliminate_subsumed_clauses(&mut clauses, 3, 64, None);
        assert!(!timed_out);
        assert_eq!(num_removed, 1);
        assert_eq!(clauses.len(), 1);
    }

    #[test]
    fn test_eliminate_subsumed_clauses_parallel_reports_timed_out() {
        let mut clauses: Vec<Clause> = vec![vec![1, 2], vec![1, 2, 3]];
        let (num_removed, timed_out) =
            eliminate_subsumed_clauses_parallel(&mut clauses, 3, 4, 64, past_deadline());
        assert!(timed_out);
        assert_eq!(num_removed, 0);
        assert_eq!(clauses.len(), 2);
    }

    #[test]
    fn test_eliminate_variables_does_nothing_when_expired() {
        let mut clauses: Vec<Clause> = vec![vec![1, 2], vec![-1, 3], vec![-2, -3]];
        let assignment = assignment::new(3);
        let (steps, timed_out) = eliminate_variables(
            &mut clauses,
            &assignment,
            3,
            &mut huge_budget(),
            past_deadline(),
        );
        assert!(timed_out);
        assert!(steps.is_empty());
        assert_eq!(clauses.len(), 3);

        let (steps, timed_out) =
            eliminate_variables(&mut clauses, &assignment, 3, &mut huge_budget(), None);
        assert!(!timed_out);
        assert!(!steps.is_empty());
    }

    /// The safety property: an early stop at any point leaves a problem
    /// that is satisfiable exactly when the original is, and every model
    /// of it reconstructs to a model of the original.
    #[test]
    fn test_run_with_expired_deadline_stays_equivalent() {
        let mut rng = StdRng::seed_from_u64(58);
        for trial in 0..150 {
            let num_vars: i32 = 3 + rng.random_range(0..7);
            let num_clauses = 3 + rng.random_range(0..20);
            let problem = Problem {
                num_vars: num_vars as usize,
                clauses: random_clause_set(&mut rng, num_vars, num_clauses),
            };
            let want_sat = brute_force_satisfiable(&problem.clauses, problem.num_vars);

            let result = run(
                &problem,
                0,
                1,
                params::default().preprocess,
                past_deadline(),
            );
            if result.unsat {
                assert!(
                    !want_sat,
                    "trial {trial}: UNSAT claimed on a satisfiable formula"
                );
                continue;
            }
            assert!(
                result.stats.timed_out,
                "trial {trial}: an expired deadline must set stats.timed_out"
            );
            let reduced = result.problem.as_ref().expect("expected a reduced problem");
            let n = reduced.num_vars;
            assert_eq!(
                brute_force_satisfiable(&reduced.clauses, n),
                want_sat,
                "trial {trial}"
            );
            for mask in 0u32..(1 << n) {
                let mut solution = assignment::new(n);
                for (v, value) in solution.iter_mut().enumerate().skip(1) {
                    *value = if mask & (1 << (v - 1)) != 0 {
                        Value::True
                    } else {
                        Value::False
                    };
                }
                if all_satisfied(&reduced.clauses, &solution) {
                    all_clauses_satisfied(&problem, &result.reconstruct(&solution));
                }
            }
        }
    }

    #[test]
    fn test_run_with_far_deadline_matches_run_without_deadline() {
        let mut rng = StdRng::seed_from_u64(59);
        for trial in 0..50 {
            let num_vars: i32 = 4 + rng.random_range(0..10);
            let num_clauses = 4 + rng.random_range(0..30);
            let problem = Problem {
                num_vars: num_vars as usize,
                clauses: random_clause_set(&mut rng, num_vars, num_clauses),
            };

            let none = run(&problem, 0, 1, params::default().preprocess, None);
            let far = run(
                &problem,
                0,
                1,
                params::default().preprocess,
                future_deadline(),
            );
            assert!(
                !none.stats.timed_out && !far.stats.timed_out,
                "trial {trial}"
            );
            assert_eq!(none.unsat, far.unsat, "trial {trial}");
            if none.unsat {
                continue;
            }
            let (a, b) = (none.problem.unwrap(), far.problem.unwrap());
            assert_eq!(a.num_vars, b.num_vars, "trial {trial}");
            assert_eq!(a.clauses, b.clauses, "trial {trial}");
        }
    }

    #[test]
    fn test_run_stops_promptly_on_long_unit_chain() {
        // The chain needs one linear pass per link (quadratic overall); a
        // short deadline must cut it off long before it finishes.
        const N: i32 = 60000;
        let problem = Problem {
            num_vars: N as usize,
            clauses: chain_clauses(N),
        };
        let limit = Duration::from_millis(200);
        let start = Instant::now();
        let result = run(
            &problem,
            0,
            1,
            params::default().preprocess,
            deadline(Some(limit), start),
        );
        // The deadline here is limit/10 = 20 ms; allow generous slack for
        // a loaded machine, but far below the many seconds a full run takes.
        assert!(
            start.elapsed() < Duration::from_secs(3),
            "run took {:?} with a 20 ms deadline",
            start.elapsed()
        );
        assert!(
            result.stats.timed_out,
            "expected stats.timed_out on a 60000-link chain with a 20 ms deadline"
        );
    }

    // ---- STAGE61.md: Bounded Variable Addition ----

    /// A work budget large enough never to bind.
    fn bva_huge_budget() -> i64 {
        1 << 40
    }

    /// Builds a complete assignment of variables 1..=n where bit v-1 of
    /// `mask` gives variable v's value.
    fn assignment_from_mask(n: usize, mask: usize) -> Assignment {
        let mut a = assignment::new(n);
        for (v, slot) in a.iter_mut().enumerate().skip(1) {
            *slot = if mask & (1 << (v - 1)) != 0 {
                Value::True
            } else {
                Value::False
            };
        }
        a
    }

    /// For each assignment of the original variables 1..=old_vars,
    /// reports whether the clause set (which may use up to total_vars
    /// variables, the extras being existentially quantified) has a
    /// satisfying extension, indexed by the old-variable mask.
    fn satisfiable_over_original(
        clauses: &[Clause],
        old_vars: usize,
        total_vars: usize,
    ) -> Vec<bool> {
        let mut result = vec![false; 1 << old_vars];
        for mask in 0..(1usize << total_vars) {
            if all_satisfied(clauses, &assignment_from_mask(total_vars, mask)) {
                result[mask & ((1 << old_vars) - 1)] = true;
            }
        }
        result
    }

    /// Verifies the defining property of Bounded Variable Addition: an
    /// assignment of the original variables satisfies the original
    /// formula exactly when it extends to a model of the new one.
    fn check_bva_equivalent(
        original: &[Clause],
        rewritten: &[Clause],
        old_vars: usize,
        new_vars: usize,
    ) {
        let before = satisfiable_over_original(original, old_vars, old_vars);
        let after = satisfiable_over_original(rewritten, old_vars, new_vars);
        for mask in 0..before.len() {
            assert_eq!(
                before[mask], after[mask],
                "assignment {mask:b}: original satisfied={}, rewritten extendable={}\noriginal: {original:?}\nrewritten: {rewritten:?}",
                before[mask], after[mask]
            );
        }
    }

    /// The clauses (NOT a OR NOT b) for every pair of variables 1..=n,
    /// the classic target of Bounded Variable Addition.
    fn pairwise_at_most_one(n: i32) -> Vec<Clause> {
        let mut clauses = Vec::new();
        for i in 1..=n {
            for j in (i + 1)..=n {
                clauses.push(vec![-i, -j]);
            }
        }
        clauses
    }

    /// A pairwise at-most-one over 8 variables (28 clauses) is
    /// reencoded with fewer clauses and one or more added variables,
    /// and stays equivalent.
    #[test]
    fn bva_reencodes_pairwise_at_most_one() {
        let original = pairwise_at_most_one(8);
        let mut clauses = original.clone();
        let mut budget = bva_huge_budget();
        let (added, timed_out) = add_bounded_variables(&mut clauses, 8, &mut budget, None);
        assert!(!timed_out);
        assert!(added > 0, "expected BVA to add at least one variable");
        assert!(clauses.len() < original.len());
        check_bva_equivalent(&original, &clauses, 8, 8 + added);
    }

    /// The textbook case exactly: the 3x3 grid (a OR C_j) for a in
    /// {1,2,3} and C_j in {4,5,6} (9 clauses) becomes 3+3 clauses over
    /// one new variable.
    #[test]
    fn bva_grid_is_replaced_by_two_sets() {
        let mut original = Vec::new();
        for a in 1..=3 {
            for c in 4..=6 {
                original.push(vec![a, c]);
            }
        }
        let mut clauses = original.clone();
        let mut budget = bva_huge_budget();
        let (added, _) = add_bounded_variables(&mut clauses, 6, &mut budget, None);
        assert_eq!(added, 1);
        assert_eq!(clauses.len(), 6, "{clauses:?}");
        check_bva_equivalent(&original, &clauses, 6, 7);
    }

    /// A formula with no shared structure is returned untouched.
    #[test]
    fn bva_leaves_formula_without_pattern_alone() {
        let original = vec![
            vec![1, 2, 3],
            vec![-1, 4, 5],
            vec![-2, -4, 6],
            vec![3, -5, -6],
        ];
        let mut clauses = original.clone();
        let mut budget = bva_huge_budget();
        let (added, _) = add_bounded_variables(&mut clauses, 6, &mut budget, None);
        assert_eq!(added, 0);
        assert_eq!(clauses, original);
    }

    /// An exhausted budget leaves the formula alone and does not report
    /// a timeout.
    #[test]
    fn bva_zero_budget_does_nothing() {
        let original = pairwise_at_most_one(8);
        let mut clauses = original.clone();
        let mut budget = 0;
        let (added, timed_out) = add_bounded_variables(&mut clauses, 8, &mut budget, None);
        assert_eq!(added, 0);
        assert!(!timed_out);
        assert_eq!(clauses, original);
    }

    /// An already-passed deadline stops BVA before it changes anything
    /// and reports a timeout.
    #[test]
    fn bva_expired_deadline_does_nothing() {
        let original = pairwise_at_most_one(8);
        let mut clauses = original.clone();
        let mut budget = bva_huge_budget();
        let deadline = Instant::now().checked_sub(Duration::from_secs(1));
        let (added, timed_out) = add_bounded_variables(&mut clauses, 8, &mut budget, deadline);
        assert_eq!(added, 0);
        assert!(timed_out);
        assert_eq!(clauses, original);
    }

    /// A random formula over 8 variables made of a product grid
    /// (literals over variables 1..=4 each paired with several
    /// remainders over variables 5..=8), plus a few unrelated random
    /// clauses.
    fn random_grid_formula(rng: &mut StdRng) -> Vec<Clause> {
        fn random_lit(rng: &mut StdRng, lo: i32, hi: i32) -> Literal {
            let v = rng.random_range(lo..=hi);
            if rng.random_range(0..2) == 0 { -v } else { v }
        }
        let mut heads: Vec<Literal> = Vec::new();
        let wanted = 2 + rng.random_range(0..3);
        while heads.len() < wanted {
            let lit = random_lit(rng, 1, 4);
            if !heads.iter().any(|h| h.abs() == lit.abs()) {
                heads.push(lit);
            }
        }
        let mut clauses = Vec::new();
        for _ in 0..(3 + rng.random_range(0..3)) {
            let a = random_lit(rng, 5, 8);
            let b = random_lit(rng, 5, 8);
            if a.abs() == b.abs() {
                continue;
            }
            for &h in &heads {
                clauses.push(vec![h, a, b]);
            }
        }
        for _ in 0..rng.random_range(0..5) {
            let x = random_lit(rng, 1, 8);
            let y = random_lit(rng, 1, 8);
            let z = random_lit(rng, 1, 8);
            if x.abs() == y.abs() || y.abs() == z.abs() || x.abs() == z.abs() {
                continue;
            }
            clauses.push(vec![x, y, z]);
        }
        clauses
    }

    /// Fuzzes `add_bounded_variables` against exhaustive enumeration: on
    /// every random formula, the rewritten clauses must be equivalent
    /// over the original variables.
    #[test]
    fn bva_preserves_satisfiability_on_random_grids() {
        let mut rng = StdRng::seed_from_u64(61);
        let mut rewritten_at_least_once = 0;
        for trial in 0..300 {
            let original = random_grid_formula(&mut rng);
            let mut clauses = original.clone();
            let mut budget = bva_huge_budget();
            let (added, _) = add_bounded_variables(&mut clauses, 8, &mut budget, None);
            if added > 0 {
                rewritten_at_least_once += 1;
            }
            assert!(
                added <= 6,
                "trial {trial}: {added} added variables is more than this test can enumerate"
            );
            check_bva_equivalent(&original, &clauses, 8, 8 + added);
        }
        assert!(
            rewritten_at_least_once >= 50,
            "only {rewritten_at_least_once} of 300 random formulas were rewritten; the generator is not exercising BVA"
        );
    }

    /// Runs the whole pipeline on a satisfiable formula BVA rewrites,
    /// then reconstructs every model of the reduced problem and checks
    /// it satisfies the original.
    #[test]
    fn run_with_bva_reconstructs_correctly() {
        // "at most one of 1..7" plus (1 OR ... OR 7) forces exactly one.
        let mut original = pairwise_at_most_one(7);
        original.push(vec![1, 2, 3, 4, 5, 6, 7]);
        let problem = Problem {
            num_vars: 7,
            clauses: original,
        };

        // Variable elimination would remove these low-degree variables
        // before BVA ever runs, so turn it off to hand BVA the formula
        // intact.
        let mut pp = params::default().preprocess;
        pp.bva_work_budget_factor = 100;
        pp.bve_work_budget_factor = 0;
        let result = run(&problem, 0, 1, pp, None);
        assert!(!result.unsat);
        assert!(
            result.stats.variables_added > 0,
            "expected BVA to add a variable"
        );
        let reduced = result.problem.as_ref().unwrap();
        let n = reduced.num_vars;
        assert!(
            n <= 18,
            "reduced problem has {n} variables, too many to enumerate"
        );
        let mut models = 0;
        for mask in 0..(1usize << n) {
            let solution = assignment_from_mask(n, mask);
            if !all_satisfied(&reduced.clauses, &solution) {
                continue;
            }
            models += 1;
            all_clauses_satisfied(&problem, &result.reconstruct(&solution));
        }
        assert!(
            models > 0,
            "reduced problem has no models, but the original is satisfiable"
        );
    }

    /// A factor of 0 turns BVA off.
    #[test]
    fn run_with_bva_zero_factor_adds_nothing() {
        let problem = Problem {
            num_vars: 8,
            clauses: pairwise_at_most_one(8),
        };
        let mut pp = params::default().preprocess;
        pp.bva_work_budget_factor = 0;
        let result = run(&problem, 0, 1, pp, None);
        assert_eq!(result.stats.variables_added, 0);
    }
}
