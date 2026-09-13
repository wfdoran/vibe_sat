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
//!
//! STAGE12.md adds learned-clause database management: once the
//! estimated size of the clause database (see [`clause_byte_cost`])
//! exceeds an optional user-supplied memory limit,
//! [`reduce_clause_database`] deletes the least "active" half of the
//! learned clauses that are safe to delete (see its doc comment),
//! MiniSat-style (Eén & Sörensson, "An Extensible SAT-solver," SAT
//! 2003).
//!
//! STAGE13.md adds two modern activity-based [`SelectVarVariant`]
//! values that only make sense once conflicts exist to learn from, so
//! unlike `Weighted`/`Fast` (still delegated to dfs's identical
//! heuristics), they are implemented here rather than in `dfs`:
//!
//! - `Vsids`: classic VSIDS (Moskewicz et al., Chaff, DAC 2001).
//!   Every variable touched while resolving a conflict (see
//!   [`analyze`]) has its activity bumped ([`VsidsState`]); the
//!   highest-activity unassigned variable is picked at each decision.
//! - `Lrb`: a simplified Learning Rate Branching (Liang, Ganesh,
//!   Poupart & Czarnecki, "Learning Rate Based Branching Heuristic
//!   for SAT Solvers," SAT 2016), which the paper reports beating
//!   both VSIDS and the Conflict History-Based heuristic on SAT
//!   Competition 2009-2014 instances (1279 vs. 1179 vs. 1235 solved).
//!   Every variable's "learning rate" -- how often it has recently
//!   participated in producing a learned clause, per conflict it has
//!   been assigned for -- is tracked ([`LrbState`]) and used as its
//!   score instead. This implementation omits the paper's "reason
//!   side rate" bonus and its annealed learning-rate schedule for the
//!   Q-value update itself (kept at a fixed `LRB_ALPHA` instead); see
//!   [`backtrack_to`]'s doc comment for exactly what is and isn't
//!   implemented.
//!
//! Per STAGE13.md, `SelectVarVariant::Vsids` is the default for
//! `--algorithm=cdcl` (see [`run`]'s doc comment for why: the paper
//! above favors LRB on SAT Competition instances, but this project's
//! own benchmark comparison, `reports/REPORT13.md`, found VSIDS
//! clearly ahead of both LRB and the older structural heuristics on
//! this project's actual, uniform random 3-SAT benchmark set).
//!
//! STAGE14.md adds phase saving (see [`run`]'s `saved_phase` local
//! and [`backtrack_to`], which reads and writes it respectively): a
//! consistently measured win in MiniSat-lineage solvers, per
//! Pipatsrisawat & Darwiche's discussion of component caching and
//! related techniques (2007-era MiniSat/RSat literature) cited there.
//! Unconditionally on for every `SelectVarVariant`, since STAGE14.md
//! asks that it always be, with no `--alg-params` toggle.
//!
//! STAGE15.md adds restarts: periodically abandoning the current
//! decision stack and starting over from decision level 0 -- exactly
//! `backtrack_to(0, ...)`, already used for conflict-driven
//! backjumping -- while every learned clause, and all other
//! persistent state (activity scores, saved phases), is left
//! untouched (see [`maybe_restart`]). Per Luby, Sinclair & Zuckerman,
//! "Optimal Speedup of Las Vegas Algorithms," 1993, and Gomes, Selman
//! & Kautz, "Boosting Combinatorial Search Through Randomization,"
//! AAAI 1998, this escapes runs of unlucky early decisions. Three
//! schedules are implemented (see [`RestartStrategy`]):
//!
//! - `RestartStrategy::Luby`: the classic Luby sequence (see
//!   [`luby_term`]).
//! - `RestartStrategy::Polynomial`: the quadratic sequence STAGE15.md
//!   originally specified under the name "geometric" growth -- a*k^2
//!   for k = 1, 2, 3, ... -- which is *not* the conventional,
//!   geometric (equivalently, exponential-in-k) meaning of "geometric
//!   restarts" found in the literature: a geometric sequence has a
//!   constant ratio between consecutive terms, and a*k^2's ratio
//!   shrinks (4, 2.25, 1.78, ...) rather than staying fixed. It is
//!   implemented exactly as originally specified regardless, now
//!   under the accurate name "polynomial" rather than "geometric."
//! - `RestartStrategy::Geometric`: the true geometric schedule, added
//!   once the naming mismatch above was caught -- c*r^k for
//!   k = 0, 1, 2, ..., a constant ratio r between consecutive restart
//!   intervals. This is what "geometric restarts" conventionally
//!   means in the SAT literature (e.g. MiniSat 1.13/1.14's restart
//!   scheme, before Luby restarts became the default in later MiniSat
//!   versions).
//!
//! All three schedules are driven by conflict count, not decision
//! count: STAGE15.md assumes "node count" as the restart statistic,
//! and conflict count is this implementation's reading of that,
//! matching both the convention in the restart literature cited above
//! and MiniSat-lineage implementations generally (`dfs` has an
//! explicit node count; `cdcl` does not, but does already track
//! `num_conflicts` separately from `num_decisions` for exactly this
//! kind of purpose). See `LUBY_BASE_CONFLICTS`/
//! `POLYNOMIAL_BASE_CONFLICTS`/`GEOMETRIC_BASE_CONFLICTS` and
//! `GEOMETRIC_GROWTH_FACTOR` for how each schedule's scale constant(s)
//! were chosen.
use std::time::{Duration, Instant};

use rand::Rng;

use crate::assignment::{self, Assignment, Value};
use crate::cnf::{self, Clause, Literal, Problem};
use crate::dfs::{select_var, select_var_fast_pick};
use crate::occurrence::{self, Lists};
use crate::preprocess;

/// Identifies which heuristic [`run`] should use to pick the next
/// branching variable. `Weighted`/`Fast` match
/// [`dfs::SelectVarVariant`] exactly (and are delegated to
/// [`select_var`]/[`select_var_fast_pick`] unchanged); `Vsids`/`Lrb`
/// are STAGE13.md's new activity-based heuristics, which only exist
/// here since they require conflict-driven learning's bookkeeping to
/// have anything to work from.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SelectVarVariant {
    /// Matches [`dfs::SelectVarVariant::Weighted`]: STAGE5.md's
    /// clause-weighted heuristic, delegated to [`select_var`]
    /// unchanged.
    Weighted,
    /// Matches [`dfs::SelectVarVariant::Fast`]: the cheap static-order
    /// heuristic, delegated to [`select_var_fast_pick`] unchanged.
    Fast,
    /// STAGE13.md's VSIDS heuristic (see the module doc comment);
    /// this is [`run`]'s default.
    Vsids,
    /// STAGE13.md's (simplified) LRB heuristic (see the module doc
    /// comment).
    Lrb,
}

/// Identifies which restart schedule [`maybe_restart`] applies for
/// `--algorithm=cdcl` (STAGE15.md; see the module doc comment).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RestartStrategy {
    /// Disables restarts entirely -- `cdcl`'s original (pre-Stage-15)
    /// behavior.
    None,
    /// Restarts on the classic Luby, Sinclair & Zuckerman sequence
    /// (see [`luby_term`]), scaled by `LUBY_BASE_CONFLICTS`.
    Luby,
    /// Restarts on the quadratic sequence STAGE15.md originally
    /// specified under the name "geometric" growth (a*k^2 for
    /// k = 1, 2, 3, ...; see the module doc comment for why that name
    /// was wrong and has been corrected here), scaled by
    /// `POLYNOMIAL_BASE_CONFLICTS`. This is [`run`]'s default (see its
    /// doc comment for why).
    Polynomial,
    /// Restarts on the true geometric sequence (see the module doc
    /// comment): c*r^k for k = 0, 1, 2, ..., a constant ratio
    /// `GEOMETRIC_GROWTH_FACTOR` between consecutive restart
    /// intervals, scaled by `GEOMETRIC_BASE_CONFLICTS`.
    Geometric,
}

/// `LUBY_BASE_CONFLICTS` and `POLYNOMIAL_BASE_CONFLICTS` are
/// STAGE15.md's "b" and "a": the scale constants for the Luby and
/// polynomial restart sequences respectively (see
/// [`restart_threshold`]), expressed in conflicts (see the module doc
/// comment for why conflicts, not decisions, is the chosen restart
/// statistic). Both are internal parameters, not exposed via
/// `--alg-params`, per STAGE15.md's explicit instruction that they're
/// meant to be optimized later instead.
///
/// `LUBY_BASE_CONFLICTS` uses MiniSat's own default Luby restart base
/// (its `-rfirst` option, 100 conflicts) -- a genuinely standard
/// value in the literature/practice, inherited unchanged by most
/// MiniSat-lineage solvers (Glucose, CryptoMiniSat, etc.), and exactly
/// the kind of standard STAGE15.md asks to prefer when one exists.
///
/// `POLYNOMIAL_BASE_CONFLICTS` has no such standard to inherit: the
/// quadratic sequence STAGE15.md originally specified under the name
/// "geometric" growth (a*k^2) is not itself a geometric sequence (see
/// the module doc comment), so no standard constant applies to this
/// exact formula. Per STAGE15.md's fallback instruction, this is
/// instead picked empirically to be about one second of work on this
/// project's own uf250/uuf250 benchmark sample: measured at
/// ~17,300-18,900 conflicts/second across ten sampled instances (five
/// uf250-1065, five uuf250-1065) under this project's current default
/// `cdcl` configuration (VSIDS + phase saving), rounded to 18000.
///
/// `GEOMETRIC_BASE_CONFLICTS` and `GEOMETRIC_GROWTH_FACTOR` are "c"
/// and "r" for the true geometric schedule (`RestartStrategy::Geometric`):
/// the restart interval starts at c conflicts and is multiplied by r
/// after every restart. Unlike `POLYNOMIAL_BASE_CONFLICTS`, a standard
/// pairing of these two constants does exist in the literature:
/// MiniSat 1.13/1.14's geometric restart scheme (the scheme Luby
/// restarts later replaced as MiniSat's default) used a base restart
/// interval of 100 conflicts -- the same "rfirst" constant reused
/// here as `LUBY_BASE_CONFLICTS` -- and a growth factor of 1.5. That
/// 1.5 was itself a practical (not theoretical) choice: a value a bit
/// below the golden ratio (~1.618), the same growth-factor reasoning
/// used when picking dynamic array growth factors to allow memory
/// reuse (a factor at or above the golden ratio can never reuse
/// previously freed memory as it grows). Per STAGE15.md's preference
/// for a standard value when one exists, both constants are taken
/// from that standard MiniSat pairing rather than re-derived
/// empirically.
const LUBY_BASE_CONFLICTS: usize = 100;
const POLYNOMIAL_BASE_CONFLICTS: usize = 18000;
const GEOMETRIC_BASE_CONFLICTS: usize = LUBY_BASE_CONFLICTS;
const GEOMETRIC_GROWTH_FACTOR: f64 = 1.5;

/// Returns the i-th term (0-indexed) of the Luby, Sinclair & Zuckerman
/// restart sequence: 1, 1, 2, 1, 1, 2, 4, 1, 1, 2, 1, 1, 2, 4, 8, ...
/// -- formally, t_i = 2^(k-1) if i+1 = 2^k - 1, else
/// t_i = t_(i+1 - 2^(k-1) + 1) - 1 (1-indexed in the original
/// definition; this is the standard 0-indexed iterative form used by
/// MiniSat and its descendants to compute it without recursion).
fn luby_term(i: usize) -> usize {
    let mut size = 1usize;
    let mut seq = 0u32;
    while size < i + 1 {
        seq += 1;
        size = 2 * size + 1;
    }
    let mut i = i;
    while size - 1 != i {
        size = (size - 1) / 2;
        seq -= 1;
        i %= size;
    }
    1usize << seq
}

/// Returns the number of conflicts that must elapse since the
/// previous restart (or since the search began, before the first one)
/// before the next restart is due, per `restart_strategy` and
/// `restart_count` (how many restarts have already happened, used to
/// index into the sequence).
///
/// For `RestartStrategy::Luby`, this is
/// `LUBY_BASE_CONFLICTS * luby_term(restart_count)` -- the classic
/// Luby sequence 1, 1, 2, 1, 1, 2, 4, ... (see [`luby_term`]), scaled
/// by "b" (STAGE15.md).
///
/// For `RestartStrategy::Polynomial`, this is
/// `POLYNOMIAL_BASE_CONFLICTS * k^2`, where `k = restart_count + 1`:
/// STAGE15.md's specified sequence a*1^2, a*2^2, a*3^2, ..., scaled by
/// "a".
///
/// For `RestartStrategy::Geometric`, this is
/// `GEOMETRIC_BASE_CONFLICTS * GEOMETRIC_GROWTH_FACTOR^restart_count`
/// -- the true geometric sequence c, c*r, c*r^2, ..., scaled by "c"
/// and with constant ratio "r" between consecutive terms. Truncated
/// (rather than rounded) to a `usize`, matching MiniSat's own
/// geometric restart implementation.
fn restart_threshold(restart_strategy: RestartStrategy, restart_count: usize) -> usize {
    match restart_strategy {
        RestartStrategy::Luby => LUBY_BASE_CONFLICTS * luby_term(restart_count),
        RestartStrategy::Polynomial => {
            let k = restart_count + 1;
            POLYNOMIAL_BASE_CONFLICTS * k * k
        }
        RestartStrategy::Geometric => {
            (GEOMETRIC_BASE_CONFLICTS as f64 * GEOMETRIC_GROWTH_FACTOR.powi(restart_count as i32))
                as usize
        }
        RestartStrategy::None => 0, // never actually consulted (see maybe_restart)
    }
}

/// Implements STAGE15.md's restart schedules (see the module doc
/// comment): once `*conflicts_since_restart` reaches the threshold
/// for `restart_strategy` and the current restart index (see
/// [`restart_threshold`]), the search abandons its current decision
/// stack and starts over from decision level 0 -- exactly
/// `backtrack_to(0, ...)`, the same primitive conflict-driven
/// backjumping already uses -- while every learned clause and all
/// other persistent state (clause and variable activity, saved
/// phases) is left untouched, since `backtrack_to` never removes
/// clauses or resets activity/phase bookkeeping. Called once after
/// every conflict is otherwise handled (i.e. after the backjump and
/// its bookkeeping, in [`run`]); a no-op if `restart_strategy` is
/// `RestartStrategy::None`.
#[allow(clippy::too_many_arguments)]
fn maybe_restart(
    restart_strategy: RestartStrategy,
    conflicts_since_restart: &mut usize,
    restart_count: &mut usize,
    trail: &mut Vec<usize>,
    trail_lim: &mut Vec<usize>,
    x: &mut Assignment,
    q_head: &mut usize,
    current_level: &mut usize,
    variant: SelectVarVariant,
    num_conflicts: usize,
    lrb: &mut LrbState,
    saved_phase: &mut [Value],
) {
    if restart_strategy == RestartStrategy::None {
        return;
    }
    if *conflicts_since_restart < restart_threshold(restart_strategy, *restart_count) {
        return;
    }
    backtrack_to(
        0,
        trail,
        trail_lim,
        x,
        q_head,
        current_level,
        variant,
        num_conflicts,
        lrb,
        saved_phase,
    );
    *conflicts_since_restart = 0;
    *restart_count += 1;
}

/// `VAR_ACTIVITY_DECAY` is `SelectVarVariant::Vsids`'s per-variable
/// analogue of `CLAUSE_ACTIVITY_DECAY` (see [`VsidsState`]); VSIDS
/// conventionally decays faster than clause activity does (MiniSat's
/// own defaults: 0.95 for variables, 0.999 for clauses), which is why
/// this is a separate constant rather than reusing
/// `CLAUSE_ACTIVITY_DECAY`.
const VAR_ACTIVITY_DECAY: f64 = 0.95;

/// `LRB_ALPHA` is the fixed learning-rate weight `SelectVarVariant::Lrb`
/// uses when updating a variable's Q-value (see [`backtrack_to`]'s doc
/// comment): the paper anneals this over the course of the search,
/// starting high and decaying toward a floor; this implementation
/// keeps it fixed at a value from within that range instead, as a
/// documented simplification (see the module doc comment).
const LRB_ALPHA: f64 = 0.4;

/// Per-variable VSIDS bookkeeping (`SelectVarVariant::Vsids` only):
/// `activity[v]` is bumped by `increment` every time variable `v` is
/// touched while resolving a conflict (see [`analyze`]); `increment`
/// itself grows once per conflict (see [`run`]) rather than decaying
/// every entry of `activity` individually, the same O(1) trick
/// `CLAUSE_ACTIVITY_DECAY` uses.
struct VsidsState {
    activity: Vec<f64>,
    increment: f64,
}

/// Per-variable LRB bookkeeping (`SelectVarVariant::Lrb` only): `q[v]`
/// is variable `v`'s current learning-rate score; `participated[v]`
/// counts how many conflicts it has contributed a literal to since it
/// was last assigned; `assigned_at_conflict[v]` records how many
/// conflicts had occurred when it was last assigned, so
/// [`backtrack_to`] can compute how many elapsed while it was
/// assigned. See [`backtrack_to`]'s doc comment for the update rule.
struct LrbState {
    q: Vec<f64>,
    participated: Vec<usize>,
    assigned_at_conflict: Vec<usize>,
}

impl VsidsState {
    fn new(num_vars: usize) -> Self {
        VsidsState {
            activity: vec![0.0; num_vars + 1],
            increment: 1.0,
        }
    }
}

impl LrbState {
    fn new(num_vars: usize) -> Self {
        LrbState {
            q: vec![0.0; num_vars + 1],
            participated: vec![0; num_vars + 1],
            assigned_at_conflict: vec![0; num_vars + 1],
        }
    }
}

/// Returns the unassigned variable with the highest score in `scores`
/// (a linear scan, same asymptotic cost class as `Weighted`'s clause
/// scan; ties keep whichever variable was found first). `scores` is
/// `vsids.activity` for `SelectVarVariant::Vsids`, or `lrb.q` for
/// `SelectVarVariant::Lrb`. Before any conflicts have occurred, every
/// score is still 0, so this falls back to the lowest-numbered
/// unassigned variable -- the same choice `SelectVarVariant::Fast`
/// would make.
fn select_var_by_activity(scores: &[f64], x: &Assignment, num_vars: usize) -> usize {
    let mut best: Option<usize> = None;
    let mut best_score = 0.0;
    for v in 1..=num_vars {
        if x[v] != Value::Unassigned {
            continue;
        }
        if best.is_none() || scores[v] > best_score {
            best = Some(v);
            best_score = scores[v];
        }
    }
    best.expect("at least one unassigned variable must exist when select_var_by_activity is called")
}

/// How often the time limit is checked, in number of decisions plus
/// conflicts processed, matching [`crate::dfs::TIME_CHECK_INTERVAL`]
/// and for the same reason: checking the clock on every single step
/// would add needless overhead.
const TIME_CHECK_INTERVAL: usize = 0xfff;

/// `BYTES_PER_LITERAL` and `PER_CLAUSE_OVERHEAD_BYTES` approximate the
/// memory footprint of one clause, for comparing against a
/// user-supplied `--alg-params` memory limit (STAGE12.md): 4 bytes
/// per [`Literal`] (an i32), plus a fixed overhead per clause
/// standing in for its `Vec` header and its parallel watch/activity
/// slots. This is deliberately an approximation, not an exact
/// accounting of Rust's actual heap usage -- the point is a reduction
/// policy that responds sensibly to a size budget, not a
/// byte-for-byte memory profiler.
const BYTES_PER_LITERAL: i64 = 4;
const PER_CLAUSE_OVERHEAD_BYTES: i64 = 40;

/// `CLAUSE_ACTIVITY_DECAY` and `ACTIVITY_RESCALE_THRESHOLD`
/// implement MiniSat's clause activity bookkeeping (see
/// [`analyze`]'s activity bump): rather than multiplying every
/// clause's activity by `CLAUSE_ACTIVITY_DECAY` after every conflict
/// (an O(clauses) cost per conflict), the single
/// clause-activity-increment is grown by `1/CLAUSE_ACTIVITY_DECAY`
/// instead, which has the same relative effect (older bumps count
/// for less compared to newer ones) at O(1) cost. If the increment
/// ever grows past `ACTIVITY_RESCALE_THRESHOLD`, every
/// clause's activity and the increment itself are divided back down
/// by the same factor, to stay well within f64's range over a very
/// long run.
const CLAUSE_ACTIVITY_DECAY: f64 = 0.999;
const ACTIVITY_RESCALE_THRESHOLD: f64 = 1e100;

/// Estimates `clause`'s contribution to the clause database's memory
/// footprint; see `BYTES_PER_LITERAL`/`PER_CLAUSE_OVERHEAD_BYTES`.
fn clause_byte_cost(clause: &Clause) -> i64 {
    PER_CLAUSE_OVERHEAD_BYTES + clause.len() as i64 * BYTES_PER_LITERAL
}

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
/// `variant` selects which heuristic is used to pick the branching
/// variable at every decision (see [`SelectVarVariant`]); per
/// STAGE11.md, this is the first algorithm parameter `cdcl` accepts,
/// same as `dfs`, but STAGE13.md extends the range `dfs`'s own values
/// don't cover (`Vsids`, `Lrb`). STAGE13.md leaves the default up to
/// this implementation: it is `SelectVarVariant::Vsids`. The paper
/// cited in the module doc comment reports LRB solving more instances
/// than VSIDS on SAT Competition instances, but this project's own
/// benchmark comparison (`reports/REPORT13.md`) found VSIDS clearly
/// ahead of both LRB and the older, purely structural heuristics this
/// project started with in Stages 5-6 on this project's actual
/// (uniform random 3-SAT) benchmark set -- real measurement on the
/// relevant benchmarks wins out over a priori literature reasoning
/// here. Callers that want one of the others still get it by passing
/// `Weighted`, `Fast`, or `Lrb` explicitly.
///
/// `restart_strategy` is the third algorithm parameter, STAGE15.md:
/// which restart schedule to use (see [`RestartStrategy`] and the
/// module doc comment). STAGE15.md leaves the default up to this
/// implementation: it is `RestartStrategy::Polynomial`. Most
/// MiniSat-lineage solvers (MiniSat, Glucose, CaDiCaL) default to
/// Luby restarts instead, but this project's own benchmark comparison
/// (`reports/REPORT15.md`) found the polynomial schedule clearly
/// ahead on this project's actual (uniform random 3-SAT) benchmark
/// set -- especially on UNSAT instances (10/10 solved within a fixed
/// budget, vs. 6/10 with no restarts and just 3/10, worse than no
/// restarts at all, with Luby) -- so, as in STAGE13.md's `SelectVar`
/// default, real measurement on the relevant benchmarks wins out over
/// the a priori literature default here. See `reports/REPORT15.md`
/// for how `RestartStrategy::Geometric` (added later, once the
/// original "geometric" name for `RestartStrategy::Polynomial` turned
/// out to be wrong) compared. Callers that want one of the others
/// still get it by passing `RestartStrategy::None`,
/// `RestartStrategy::Luby`, or `RestartStrategy::Geometric`
/// explicitly.
///
/// `memory_limit_bytes` is the second, optional, STAGE12.md algorithm
/// parameter (shifted to the third `--alg-params` slot by
/// STAGE15.md): once the estimated size of the learned-clause
/// database exceeds it, the least active learned clauses are
/// periodically deleted (see [`reduce_clause_database`]); `None`
/// means unbounded, matching `cdcl`'s original (Stage 11) behavior.
///
/// If `time_limit` is `Some`, the search gives up and reports an
/// inconclusive result (`satisfiable == false`, `timed_out == true`)
/// once it is exceeded, checked only periodically (see
/// [`TIME_CHECK_INTERVAL`]). `rng` supplies the randomness
/// `SelectVarVariant::Weighted` uses to break ties, and `verbose`
/// controls progress output, matching [`crate::dfs::run`].
#[allow(clippy::too_many_arguments)]
pub fn run<R: Rng>(
    problem: &Problem,
    time_limit: Option<Duration>,
    variant: SelectVarVariant,
    restart_strategy: RestartStrategy,
    memory_limit_bytes: Option<i64>,
    rng: &mut R,
    verbose: i32,
) -> SolveResult {
    if verbose >= 1 {
        println!(
            "cdcl: {}{}",
            describe_params(time_limit, variant, restart_strategy),
            describe_memory_limit(memory_limit_bytes)
        );
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

    // num_original_clauses marks the boundary below which a clause is
    // part of the original (bootstrapped) problem and must never be
    // deleted; only clauses at or above it are learned, and so are
    // eligible for reduce_clause_database to consider (STAGE12.md).
    let num_original_clauses = working_problem.clauses.len();
    let mut clause_activity = vec![0.0f64; num_original_clauses];
    let mut clause_activity_increment = 1.0f64;
    let mut estimated_bytes: i64 = working_problem.clauses.iter().map(clause_byte_cost).sum();

    // STAGE13.md's VSIDS/LRB bookkeeping; always allocated (cheap,
    // O(num_vars)) but only ever read or written when variant
    // actually calls for it.
    let mut vsids = VsidsState::new(problem.num_vars);
    let mut lrb = LrbState::new(problem.num_vars);

    // STAGE14.md's phase saving: the value each variable last held
    // before becoming unassigned. Starts all `Value::False`, which is
    // exactly the polarity a variable that has never been assigned
    // before should default to (see backtrack_to's doc comment).
    let mut saved_phase = vec![Value::False; problem.num_vars + 1];

    // STAGE15.md's restart bookkeeping: conflicts_since_restart counts
    // conflicts since the previous restart (or since the search
    // began, if none yet); restart_count is how many restarts have
    // happened so far, indexing into the Luby/geometric sequence (see
    // restart_threshold).
    let mut conflicts_since_restart = 0usize;
    let mut restart_count = 0usize;

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
            variant,
            num_conflicts,
            &mut lrb,
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
                &mut clause_activity,
                clause_activity_increment,
                num_original_clauses,
                variant,
                &mut vsids,
                &mut lrb,
            );
            backtrack_to(
                backtrack_level,
                &mut trail,
                &mut trail_lim,
                &mut x,
                &mut q_head,
                &mut current_level,
                variant,
                num_conflicts,
                &mut lrb,
                &mut saved_phase,
            );
            let new_clause = add_learned_clause(
                &learned,
                &mut working_problem.clauses,
                &mut lists,
                &mut watch,
                &level,
                &mut clause_activity,
                &mut estimated_bytes,
            );
            assign_literal(
                learned[0],
                backtrack_level,
                new_clause,
                &mut x,
                &mut level,
                &mut reason,
                &mut trail,
                variant,
                num_conflicts,
                &mut lrb,
            );

            // Once per conflict (matching MiniSat's claDecayActivity),
            // grow the increment so future activity bumps count for
            // relatively more than past ones -- the O(1) equivalent of
            // decaying every clause's activity individually.
            clause_activity_increment /= CLAUSE_ACTIVITY_DECAY;
            if clause_activity_increment > ACTIVITY_RESCALE_THRESHOLD {
                for a in clause_activity.iter_mut() {
                    *a /= ACTIVITY_RESCALE_THRESHOLD;
                }
                clause_activity_increment /= ACTIVITY_RESCALE_THRESHOLD;
            }
            if variant == SelectVarVariant::Vsids {
                vsids.increment /= VAR_ACTIVITY_DECAY;
                if vsids.increment > ACTIVITY_RESCALE_THRESHOLD {
                    for a in vsids.activity.iter_mut() {
                        *a /= ACTIVITY_RESCALE_THRESHOLD;
                    }
                    vsids.increment /= ACTIVITY_RESCALE_THRESHOLD;
                }
            }

            if let Some(limit) = memory_limit_bytes
                && estimated_bytes > limit
            {
                reduce_clause_database(
                    &mut working_problem.clauses,
                    &mut lists,
                    &mut watch,
                    &mut clause_activity,
                    &mut estimated_bytes,
                    &x,
                    &mut reason,
                    num_original_clauses,
                );
            }

            conflicts_since_restart += 1;
            maybe_restart(
                restart_strategy,
                &mut conflicts_since_restart,
                &mut restart_count,
                &mut trail,
                &mut trail_lim,
                &mut x,
                &mut q_head,
                &mut current_level,
                variant,
                num_conflicts,
                &mut lrb,
                &mut saved_phase,
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
        // SelectVarVariant; this is the same heuristic dfs uses for
        // Weighted/Fast, and -- since working_problem.clauses grows
        // to include learned clauses -- the Weighted variant does see
        // learned clauses when scoring, since it scans every
        // not-yet-satisfied clause in working_problem.clauses; Fast
        // ignores clause contents entirely either way. Vsids/Lrb
        // (STAGE13.md) pick the highest-scoring unassigned variable
        // by their own per-variable bookkeeping instead (see
        // select_var_by_activity). The polarity guessed is its saved
        // phase (STAGE14.md; see backtrack_to's doc comment) -- False
        // for a variable that has never been assigned before,
        // matching the fixed order earlier stages always used. CDCL
        // doesn't get to try both polarities at one level the way dfs
        // does -- if the guessed polarity turns out wrong, conflict
        // analysis is what corrects it, by deriving a clause that
        // forces the other one once it backjumps here again.
        num_decisions += 1;
        let v = match variant {
            SelectVarVariant::Fast => select_var_fast_pick(&working_problem, &x),
            SelectVarVariant::Weighted => select_var(&working_problem, &x, rng),
            SelectVarVariant::Vsids => {
                select_var_by_activity(&vsids.activity, &x, problem.num_vars)
            }
            SelectVarVariant::Lrb => select_var_by_activity(&lrb.q, &x, problem.num_vars),
        };
        current_level += 1;
        trail_lim.push(trail.len());
        assign_literal(
            decision_literal(v, &saved_phase),
            current_level,
            None,
            &mut x,
            &mut level,
            &mut reason,
            &mut trail,
            variant,
            num_conflicts,
            &mut lrb,
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
/// level, and reason accordingly) and appends it to `trail`. For
/// `SelectVarVariant::Lrb` (STAGE13.md), it also records the current
/// conflict count in `lrb.assigned_at_conflict`, so [`backtrack_to`]
/// can later tell how many conflicts elapsed while this variable was
/// assigned.
#[allow(clippy::too_many_arguments)]
fn assign_literal(
    lit: Literal,
    level_now: usize,
    reason_now: Option<usize>,
    x: &mut Assignment,
    level: &mut [usize],
    reason: &mut [Option<usize>],
    trail: &mut Vec<usize>,
    variant: SelectVarVariant,
    num_conflicts: usize,
    lrb: &mut LrbState,
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
    if variant == SelectVarVariant::Lrb {
        lrb.assigned_at_conflict[v] = num_conflicts;
    }
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
    variant: SelectVarVariant,
    num_conflicts: usize,
    lrb: &mut LrbState,
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
                assign_literal(
                    other_watch,
                    current_level,
                    Some(c),
                    x,
                    level,
                    reason,
                    trail,
                    variant,
                    num_conflicts,
                    lrb,
                );
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
///
/// As a side effect (STAGE12.md), every learned clause visited along
/// the way (every `reason_clause` at index >= `num_original_clauses`)
/// has its activity bumped by `clause_activity_increment`, MiniSat's
/// measure of how useful a clause has recently been to conflict
/// analysis -- this is what [`reduce_clause_database`] later uses to
/// decide which learned clauses to keep.
///
/// As a second side effect (STAGE13.md), every *variable* newly
/// marked seen here also has its `SelectVarVariant::Vsids`/`Lrb`
/// bookkeeping updated, whichever `variant` calls for: VSIDS bumps
/// `vsids.activity` by `vsids.increment` (the same increment-growth
/// trick as clause activity, decayed once per conflict in [`run`]);
/// LRB increments `lrb.participated`, counting this as one more
/// conflict the variable has contributed to since it was last
/// assigned (see [`backtrack_to`] for where that turns into an
/// updated Q-value).
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
    clause_activity: &mut [f64],
    clause_activity_increment: f64,
    num_original_clauses: usize,
    variant: SelectVarVariant,
    vsids: &mut VsidsState,
    lrb: &mut LrbState,
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
        if reason_clause >= num_original_clauses {
            clause_activity[reason_clause] += clause_activity_increment;
        }
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
            match variant {
                SelectVarVariant::Vsids => vsids.activity[v] += vsids.increment,
                SelectVarVariant::Lrb => lrb.participated[v] += 1,
                _ => {}
            }
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

/// Returns the literal [`run`]'s decision step should assign when
/// branching on variable `v` (STAGE14.md): its saved phase, i.e.
/// whatever polarity it held the last time it was assigned before
/// becoming unassigned again, or `False` if `saved_phase[v]` is still
/// its default (a variable that has never been assigned before),
/// matching the fixed order earlier stages always used.
fn decision_literal(v: usize, saved_phase: &[Value]) -> Literal {
    if saved_phase[v] == Value::True {
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
///
/// For `SelectVarVariant::Lrb` (STAGE13.md), the moment a variable
/// becomes unassigned is also exactly when its learning rate can be
/// computed: the "interval" is how many conflicts occurred while it
/// was assigned (`num_conflicts` now, minus its value when the
/// variable was last assigned, recorded by [`assign_literal`] in
/// `lrb.assigned_at_conflict`), and the reward `r` is how many of
/// those conflicts it actually participated in
/// (`lrb.participated[v]`) divided by that interval. Its Q-value is
/// then nudged toward `r` by `LRB_ALPHA` (an exponential moving
/// average), and `lrb.participated` is reset to 0 for its next stint
/// as an assigned variable. This is the paper's core learning-rate
/// idea; the "reason side rate" bonus and the annealed (rather than
/// fixed) alpha it also describes are both omitted here (see the
/// module doc comment).
///
/// For every variable, regardless of `variant`, the moment it becomes
/// unassigned is also when its phase is saved (STAGE14.md): whatever
/// value it held (`x[v]`, `True` or `False`) right before this loop
/// overwrites it with `Unassigned` is remembered in `saved_phase[v]`,
/// so [`run`]'s decision step can guess the same polarity again next
/// time this variable is chosen, rather than always guessing `False`.
#[allow(clippy::too_many_arguments)]
fn backtrack_to(
    level_target: usize,
    trail: &mut Vec<usize>,
    trail_lim: &mut Vec<usize>,
    x: &mut Assignment,
    q_head: &mut usize,
    current_level: &mut usize,
    variant: SelectVarVariant,
    num_conflicts: usize,
    lrb: &mut LrbState,
    saved_phase: &mut [Value],
) {
    let cut = trail_lim[level_target + 1];
    for &v in &trail[cut..] {
        if variant == SelectVarVariant::Lrb {
            let interval = num_conflicts - lrb.assigned_at_conflict[v];
            if interval > 0 {
                let r = lrb.participated[v] as f64 / interval as f64;
                lrb.q[v] = (1.0 - LRB_ALPHA) * lrb.q[v] + LRB_ALPHA * r;
            }
            lrb.participated[v] = 0;
        }
        saved_phase[v] = x[v];
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
/// was stored. `activity` gets a fresh `0.0` entry and
/// `estimated_bytes` is increased by [`clause_byte_cost`] for the new
/// clause (STAGE12.md), kept parallel to `clauses`/`watch`.
#[allow(clippy::too_many_arguments)]
fn add_learned_clause(
    learned: &Clause,
    clauses: &mut Vec<Clause>,
    lists: &mut Lists,
    watch: &mut Vec<[Literal; 2]>,
    level: &[usize],
    clause_activity: &mut Vec<f64>,
    estimated_bytes: &mut i64,
) -> Option<usize> {
    if learned.len() == 1 {
        return None;
    }

    let idx = clauses.len();
    clauses.push(learned.clone());
    clause_activity.push(0.0);
    *estimated_bytes += clause_byte_cost(learned);
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

/// Deletes roughly the least active half of the learned clauses that
/// are safe to delete, MiniSat-style (see the module doc comment), to
/// bring the database's estimated size back under control. A learned
/// clause is "safe to delete" if it is not currently locked: locked
/// means it is some currently assigned variable's reason (`reason[v]`
/// for some `v` with `x[v] != Value::Unassigned`), since [`analyze`]
/// may still need to walk through it if that variable's assignment
/// participates in a future conflict. Clauses at index <
/// `num_original_clauses` (the original, bootstrapped problem) are
/// never candidates at all -- deleting one of those would be
/// unsound, not just wasteful.
///
/// Deleting from the middle of `clauses` would silently invalidate
/// every other index into it (every watch entry, every `reason[v]`,
/// and every occurrence [`Lists`] entry), so this rebuilds all of
/// them together from an old-index-to-new-index map, rather than
/// trying to patch each in place. This is an O(current clauses +
/// literals) operation, which is fine since it only runs when the
/// configured memory limit is actually exceeded, not on every
/// conflict.
#[allow(clippy::too_many_arguments)]
fn reduce_clause_database(
    clauses: &mut Vec<Clause>,
    lists: &mut Lists,
    watch: &mut Vec<[Literal; 2]>,
    clause_activity: &mut Vec<f64>,
    estimated_bytes: &mut i64,
    x: &Assignment,
    reason: &mut [Option<usize>],
    num_original_clauses: usize,
) {
    let mut locked = vec![false; clauses.len()];
    for (v, &value) in x.iter().enumerate().skip(1) {
        if value != Value::Unassigned
            && let Some(r) = reason[v]
        {
            locked[r] = true;
        }
    }

    let mut eligible: Vec<usize> = (num_original_clauses..clauses.len())
        .filter(|&idx| !locked[idx])
        .collect();
    eligible.sort_by(|&a, &b| clause_activity[a].partial_cmp(&clause_activity[b]).unwrap());

    let num_to_delete = eligible.len() / 2;
    if num_to_delete == 0 {
        return; // nothing eligible to delete; not worth a full rebuild
    }
    let mut to_delete = vec![false; clauses.len()];
    for &idx in &eligible[..num_to_delete] {
        to_delete[idx] = true;
    }

    let mut old_to_new = vec![None; clauses.len()];
    let mut new_clauses = Vec::with_capacity(clauses.len() - num_to_delete);
    let mut new_watch = Vec::with_capacity(clauses.len() - num_to_delete);
    let mut new_activity = Vec::with_capacity(clauses.len() - num_to_delete);
    for (idx, clause) in clauses.iter().enumerate() {
        if to_delete[idx] {
            continue;
        }
        old_to_new[idx] = Some(new_clauses.len());
        new_clauses.push(clause.clone());
        new_watch.push(watch[idx]);
        new_activity.push(clause_activity[idx]);
    }

    for (v, &value) in x.iter().enumerate().skip(1) {
        if value != Value::Unassigned
            && let Some(r) = reason[v]
        {
            reason[v] = old_to_new[r];
        }
    }

    *estimated_bytes = new_clauses.iter().map(clause_byte_cost).sum();
    let temp_problem = Problem {
        num_vars: x.len() - 1,
        clauses: new_clauses,
    };
    *lists = occurrence::build(&temp_problem);
    *clauses = temp_problem.clauses;
    *watch = new_watch;
    *clause_activity = new_activity;
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
fn describe_params(
    time_limit: Option<Duration>,
    variant: SelectVarVariant,
    restart_strategy: RestartStrategy,
) -> String {
    let variant_code = match variant {
        SelectVarVariant::Weighted => 0,
        SelectVarVariant::Fast => 1,
        SelectVarVariant::Vsids => 2,
        SelectVarVariant::Lrb => 3,
    };
    let restart_code = match restart_strategy {
        RestartStrategy::None => 0,
        RestartStrategy::Luby => 1,
        RestartStrategy::Polynomial => 2,
        RestartStrategy::Geometric => 3,
    };
    match time_limit {
        Some(limit) => format!(
            "select_var={variant_code} restart={restart_code} time_limit_secs={}",
            limit.as_secs()
        ),
        None => format!("select_var={variant_code} restart={restart_code}"),
    }
}

/// Formats an optional STAGE12.md memory limit for the "cdcl:"
/// announcement printed at verbose level 1, in bytes.
fn describe_memory_limit(memory_limit_bytes: Option<i64>) -> String {
    match memory_limit_bytes {
        Some(limit) => format!(" memory_limit_bytes={limit}"),
        None => String::new(),
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
        let mut lrb = LrbState::new(3);

        assign_literal(
            1,
            1,
            None,
            &mut x,
            &mut level,
            &mut reason,
            &mut trail,
            SelectVarVariant::Weighted,
            0,
            &mut lrb,
        );
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
            SelectVarVariant::Weighted,
            0,
            &mut lrb,
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
        let mut vsids = VsidsState::new(4);
        let mut lrb = LrbState::new(4);

        assign_literal(
            -1,
            1,
            None,
            &mut x,
            &mut level,
            &mut reason,
            &mut trail,
            SelectVarVariant::Weighted,
            0,
            &mut lrb,
        );
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
            SelectVarVariant::Weighted,
            0,
            &mut lrb,
        )
        .expect("expected clause {-2,-4} to be falsified");

        let mut clause_activity = vec![0.0f64; working_problem.clauses.len()];

        let (learned, backtrack_level) = analyze(
            confl,
            &working_problem.clauses,
            &trail,
            &level,
            &reason,
            &x,
            &mut seen,
            1,
            &mut clause_activity,
            1.0,
            working_problem.clauses.len(),
            SelectVarVariant::Weighted,
            &mut vsids,
            &mut lrb,
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
        let mut clause_activity = vec![0.0f64; working_problem.clauses.len()];
        let mut estimated_bytes = 0i64;
        let before = working_problem.clauses.len();

        let idx = add_learned_clause(
            &vec![-1],
            &mut working_problem.clauses,
            &mut lists,
            &mut watch,
            &level,
            &mut clause_activity,
            &mut estimated_bytes,
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
        let mut clause_activity = vec![0.0f64; working_problem.clauses.len()];
        let mut estimated_bytes = 0i64;

        let idx = add_learned_clause(
            &vec![-1, 2, 3],
            &mut working_problem.clauses,
            &mut lists,
            &mut watch,
            &level,
            &mut clause_activity,
            &mut estimated_bytes,
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

        for variant in [
            SelectVarVariant::Weighted,
            SelectVarVariant::Fast,
            SelectVarVariant::Vsids,
            SelectVarVariant::Lrb,
        ] {
            let mut rng = StdRng::seed_from_u64(1);
            let result = run(
                &problem,
                None,
                variant,
                RestartStrategy::None,
                None,
                &mut rng,
                0,
            );
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

        for variant in [
            SelectVarVariant::Weighted,
            SelectVarVariant::Fast,
            SelectVarVariant::Vsids,
            SelectVarVariant::Lrb,
        ] {
            let mut rng = StdRng::seed_from_u64(1);
            let result = run(
                &problem,
                None,
                variant,
                RestartStrategy::None,
                None,
                &mut rng,
                0,
            );
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

    #[test]
    fn test_clause_byte_cost() {
        let got = clause_byte_cost(&vec![1, 2, 3]);
        let want = PER_CLAUSE_OVERHEAD_BYTES + 3 * BYTES_PER_LITERAL;
        assert_eq!(got, want);
    }

    /// Exercises reduce_clause_database directly: given three learned
    /// clauses -- one locked (currently some variable's reason), one
    /// unlocked with low activity, and one unlocked with high
    /// activity -- only the unlocked, low-activity one should be
    /// deleted, and every remaining reference (reason[v] for the
    /// locked clause's variable, plus the occurrence lists) must
    /// still be correct afterward.
    #[test]
    fn test_reduce_clause_database_keeps_locked_and_active_clauses() {
        let problem = Problem {
            num_vars: 5,
            clauses: vec![vec![1, 2]],
        };
        let (mut working_problem, mut watch, mut x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let mut lists = occurrence::build(&working_problem);
        let level = vec![0usize; 6];
        let num_original_clauses = working_problem.clauses.len();
        let mut clause_activity: Vec<f64> = vec![0.0; num_original_clauses];
        let mut estimated_bytes: i64 = 0;
        let mut reason: Vec<Option<usize>> = vec![None; 6];

        let idx_low = add_learned_clause(
            &vec![-1, 3],
            &mut working_problem.clauses,
            &mut lists,
            &mut watch,
            &level,
            &mut clause_activity,
            &mut estimated_bytes,
        )
        .expect("expected a stored clause");
        let idx_locked = add_learned_clause(
            &vec![-2, 4],
            &mut working_problem.clauses,
            &mut lists,
            &mut watch,
            &level,
            &mut clause_activity,
            &mut estimated_bytes,
        )
        .expect("expected a stored clause");
        let idx_high = add_learned_clause(
            &vec![-3, 5],
            &mut working_problem.clauses,
            &mut lists,
            &mut watch,
            &level,
            &mut clause_activity,
            &mut estimated_bytes,
        )
        .expect("expected a stored clause");
        clause_activity[idx_low] = 1.0;
        clause_activity[idx_high] = 100.0;

        x[4] = Value::True;
        reason[4] = Some(idx_locked);

        let before = working_problem.clauses.len();
        reduce_clause_database(
            &mut working_problem.clauses,
            &mut lists,
            &mut watch,
            &mut clause_activity,
            &mut estimated_bytes,
            &x,
            &mut reason,
            num_original_clauses,
        );

        assert_eq!(working_problem.clauses.len(), before - 1);
        assert!(
            !working_problem.clauses.contains(&vec![-1, 3]),
            "the unlocked, low-activity clause {{-1,3}} should have been deleted"
        );
        assert!(
            working_problem.clauses.contains(&vec![-2, 4]),
            "the locked clause {{-2,4}} should have survived"
        );
        assert!(
            working_problem.clauses.contains(&vec![-3, 5]),
            "the unlocked, high-activity clause {{-3,5}} should have survived"
        );
        assert_eq!(
            working_problem.clauses[reason[4].expect("reason[4] should still be set")],
            vec![-2, 4]
        );
        assert!(
            lists.negative[1].is_empty(),
            "lists.negative[1] should be empty (its only clause, {{-1,3}}, was deleted)"
        );
    }

    /// Verifies that reduce_clause_database does nothing (and,
    /// importantly, does not panic) when every learned clause is
    /// currently locked.
    #[test]
    fn test_reduce_clause_database_no_op_when_nothing_eligible() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1, 2]],
        };
        let (mut working_problem, mut watch, mut x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let mut lists = occurrence::build(&working_problem);
        let level = vec![0usize; 3];
        let num_original_clauses = working_problem.clauses.len();
        let mut clause_activity: Vec<f64> = vec![0.0; num_original_clauses];
        let mut estimated_bytes: i64 = 0;
        let mut reason: Vec<Option<usize>> = vec![None; 3];

        let idx = add_learned_clause(
            &vec![-1, 2],
            &mut working_problem.clauses,
            &mut lists,
            &mut watch,
            &level,
            &mut clause_activity,
            &mut estimated_bytes,
        )
        .expect("expected a stored clause");
        x[2] = Value::True;
        reason[2] = Some(idx);

        let before = working_problem.clauses.len();
        reduce_clause_database(
            &mut working_problem.clauses,
            &mut lists,
            &mut watch,
            &mut clause_activity,
            &mut estimated_bytes,
            &x,
            &mut reason,
            num_original_clauses,
        );

        assert_eq!(working_problem.clauses.len(), before);
    }

    /// The strongest available test of the whole reduction pipeline:
    /// a memory limit set far below the problem's own baseline size
    /// forces reduce_clause_database to run, and very likely
    /// actually delete clauses, on nearly every conflict -- exactly
    /// the index-remapping path (reason[], watch, occurrence lists
    /// all rebuilt together) that would be easiest to get subtly
    /// wrong. The verdict must still match Stage 11's
    /// (memory-limit-free) result for the same problem.
    #[test]
    fn test_run_with_tiny_memory_limit_still_proves_unsatisfiable_pigeonhole() {
        let problem = pigeonhole_problem(4, 3);
        let mut rng = StdRng::seed_from_u64(9);

        let result = run(
            &problem,
            None,
            SelectVarVariant::Fast,
            RestartStrategy::None,
            Some(200),
            &mut rng,
            0,
        );

        assert!(!result.satisfiable);
        assert!(!result.timed_out);
    }

    /// Checks that a memory limit doesn't interfere with the
    /// satisfiable path: the returned assignment must still satisfy
    /// every clause.
    #[test]
    fn test_run_with_memory_limit_still_finds_satisfiable_formula() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![1, 2], vec![-1, 3], vec![-2, -3]],
        };
        let mut rng = StdRng::seed_from_u64(1);

        let result = run(
            &problem,
            None,
            SelectVarVariant::Weighted,
            RestartStrategy::None,
            Some(64),
            &mut rng,
            0,
        );

        assert!(result.satisfiable, "expected satisfiable");
        for (ci, clause) in problem.clauses.iter().enumerate() {
            let satisfied = clause
                .iter()
                .any(|&lit| assignment::literal_is_true(&result.assignment, lit));
            assert!(satisfied, "clause {ci} ({clause:?}) not satisfied");
        }
    }

    /// Verifies the shared VSIDS/LRB selection helper directly: it
    /// must skip already-assigned variables and pick the highest
    /// score among the rest, ignoring ties in favor of whichever it
    /// finds first.
    #[test]
    fn test_select_var_by_activity_picks_highest_scoring_unassigned_variable() {
        let mut x = assignment::new(4);
        x[1] = Value::True; // no longer a candidate
        let scores = vec![0.0, 5.0, 9.0, 9.0, 3.0];

        let got = select_var_by_activity(&scores, &x, 4);
        assert_eq!(
            got, 2,
            "select_var_by_activity() = {got}, want 2 (highest score among unassigned variables)"
        );
    }

    /// Checks that resolving through a conflict under
    /// SelectVarVariant::Vsids bumps every variable touched along the
    /// way, using the same hand-verified formula as
    /// test_analyze_derives_unit_clause_independent_of_decision.
    #[test]
    fn test_analyze_bumps_vsids_activity() {
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
        let mut vsids = VsidsState::new(4);
        let mut lrb = LrbState::new(4);

        assign_literal(
            -1,
            1,
            None,
            &mut x,
            &mut level,
            &mut reason,
            &mut trail,
            SelectVarVariant::Vsids,
            0,
            &mut lrb,
        );
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
            SelectVarVariant::Vsids,
            0,
            &mut lrb,
        )
        .expect("expected clause {-2,-4} to be falsified");

        let mut clause_activity = vec![0.0f64; working_problem.clauses.len()];
        analyze(
            confl,
            &working_problem.clauses,
            &trail,
            &level,
            &reason,
            &x,
            &mut seen,
            1,
            &mut clause_activity,
            1.0,
            working_problem.clauses.len(),
            SelectVarVariant::Vsids,
            &mut vsids,
            &mut lrb,
        );

        assert!(
            vsids.activity[2] > 0.0,
            "activity[2] = {}, want > 0 (variable 2 is touched while resolving this conflict)",
            vsids.activity[2]
        );
        assert!(
            vsids.activity[4] > 0.0,
            "activity[4] = {}, want > 0 (variable 4 is touched while resolving this conflict)",
            vsids.activity[4]
        );
    }

    /// Verifies LRB's core update directly: a variable assigned when
    /// num_conflicts was 5, that participated in 3 of the 5 conflicts
    /// that occurred before it was unassigned at num_conflicts=10,
    /// should get Q = LRB_ALPHA * (3.0/5.0) (starting from Q=0, so
    /// the exponential moving average's "old value" term drops out),
    /// and its participated counter should reset to 0.
    #[test]
    fn test_backtrack_to_updates_lrb_q() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1, 2]],
        };
        let (_working_problem, _watch, mut x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let mut level = vec![0usize; 3];
        let mut reason: Vec<Option<usize>> = vec![None; 3];
        let mut trail: Vec<usize> = Vec::new();
        let mut trail_lim: Vec<usize> = vec![0, 0];
        let mut current_level = 1usize;
        let mut q_head = 0usize;
        let mut lrb = LrbState::new(2);
        let mut saved_phase = vec![Value::False; 3];

        assign_literal(
            1,
            1,
            None,
            &mut x,
            &mut level,
            &mut reason,
            &mut trail,
            SelectVarVariant::Lrb,
            5,
            &mut lrb,
        );
        lrb.participated[1] = 3;

        backtrack_to(
            0,
            &mut trail,
            &mut trail_lim,
            &mut x,
            &mut q_head,
            &mut current_level,
            SelectVarVariant::Lrb,
            10,
            &mut lrb,
            &mut saved_phase,
        );

        let want_q = LRB_ALPHA * (3.0 / 5.0);
        assert!(
            (lrb.q[1] - want_q).abs() < 1e-9,
            "lrb.q[1] = {}, want {}",
            lrb.q[1],
            want_q
        );
        assert_eq!(
            lrb.participated[1], 0,
            "lrb.participated[1] = {}, want 0 (reset on unassignment)",
            lrb.participated[1]
        );
    }

    /// Verifies STAGE14.md's core mechanism directly: a variable
    /// assigned True and then backtracked over should have its phase
    /// saved as True, regardless of SelectVar variant.
    #[test]
    fn test_backtrack_to_saves_phase() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1, 2]],
        };
        let (_working_problem, _watch, mut x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let mut level = vec![0usize; 3];
        let mut reason: Vec<Option<usize>> = vec![None; 3];
        let mut trail: Vec<usize> = Vec::new();
        let mut trail_lim: Vec<usize> = vec![0, 0];
        let mut current_level = 1usize;
        let mut q_head = 0usize;
        let mut lrb = LrbState::new(2);
        let mut saved_phase = vec![Value::False; 3];

        assign_literal(
            1, // x1 = True
            1,
            None,
            &mut x,
            &mut level,
            &mut reason,
            &mut trail,
            SelectVarVariant::Weighted,
            0,
            &mut lrb,
        );

        backtrack_to(
            0,
            &mut trail,
            &mut trail_lim,
            &mut x,
            &mut q_head,
            &mut current_level,
            SelectVarVariant::Weighted,
            0,
            &mut lrb,
            &mut saved_phase,
        );

        assert_eq!(saved_phase[1], Value::True);
    }

    /// Verifies that decision_literal consults saved_phase rather
    /// than always guessing False.
    #[test]
    fn test_decision_literal_uses_saved_phase() {
        let saved_phase = vec![Value::False, Value::True];
        assert_eq!(decision_literal(1, &saved_phase), 1);
    }

    /// Verifies the fallback: a variable that has never been assigned
    /// before (saved_phase still its default) is guessed False.
    #[test]
    fn test_decision_literal_defaults_to_false() {
        let saved_phase = vec![Value::False, Value::False];
        assert_eq!(decision_literal(1, &saved_phase), -1);
    }

    /// test_run_with_vsids_proves_unsatisfiable_pigeonhole and
    /// test_run_with_lrb_proves_unsatisfiable_pigeonhole check the new
    /// heuristics end to end against a problem the older variants are
    /// already verified against (test_run_proves_unsatisfiable_pigeonhole),
    /// confirming they don't just avoid crashing but reach the correct
    /// verdict.
    #[test]
    fn test_run_with_vsids_proves_unsatisfiable_pigeonhole() {
        let problem = pigeonhole_problem(4, 3);
        let mut rng = StdRng::seed_from_u64(9);

        let result = run(
            &problem,
            None,
            SelectVarVariant::Vsids,
            RestartStrategy::None,
            None,
            &mut rng,
            0,
        );

        assert!(!result.satisfiable);
        assert!(!result.timed_out);
    }

    #[test]
    fn test_run_with_lrb_proves_unsatisfiable_pigeonhole() {
        let problem = pigeonhole_problem(4, 3);
        let mut rng = StdRng::seed_from_u64(9);

        let result = run(
            &problem,
            None,
            SelectVarVariant::Lrb,
            RestartStrategy::None,
            None,
            &mut rng,
            0,
        );

        assert!(!result.satisfiable);
        assert!(!result.timed_out);
    }

    /// Verifies luby_term against the first seven terms of the Luby,
    /// Sinclair & Zuckerman sequence as STAGE15.md quotes them
    /// (1-indexed there; luby_term is 0-indexed, so term i here is
    /// STAGE15.md's term i+1): 1, 1, 2, 1, 1, 2, 4.
    #[test]
    fn test_luby_term_matches_hand_verified_sequence() {
        let want = [1, 1, 2, 1, 1, 2, 4];
        for (i, w) in want.into_iter().enumerate() {
            assert_eq!(luby_term(i), w, "luby_term({i})");
        }
    }

    /// Verifies the next block of the sequence (terms 8-15, 0-indexed
    /// 7-14): 1, 1, 2, 1, 1, 2, 4, 8 -- the same first block again,
    /// followed by 8, per the recursive definition (t_i = 2^(k-1)
    /// exactly at the end of each doubling block).
    #[test]
    fn test_luby_term_continues_past_first_block() {
        let want = [1, 1, 2, 1, 1, 2, 4, 8];
        for (offset, w) in want.into_iter().enumerate() {
            let i = 7 + offset;
            assert_eq!(luby_term(i), w, "luby_term({i})");
        }
    }

    /// Verifies restart_threshold's Luby case directly:
    /// threshold(k) = LUBY_BASE_CONFLICTS * luby_term(k).
    #[test]
    fn test_restart_threshold_luby() {
        for (k, term) in [1, 1, 2, 1, 1, 2, 4].into_iter().enumerate() {
            let want = LUBY_BASE_CONFLICTS * term;
            assert_eq!(restart_threshold(RestartStrategy::Luby, k), want);
        }
    }

    /// Verifies restart_threshold's polynomial case directly against
    /// STAGE15.md's originally-specified sequence (there under the
    /// incorrect name "geometric"): threshold at restart index k
    /// (0-indexed) is POLYNOMIAL_BASE_CONFLICTS * (k+1)^2, matching
    /// a*1^2, a*2^2, a*3^2, ....
    #[test]
    fn test_restart_threshold_polynomial() {
        for k in 0..4 {
            let want = POLYNOMIAL_BASE_CONFLICTS * (k + 1) * (k + 1);
            assert_eq!(restart_threshold(RestartStrategy::Polynomial, k), want);
        }
    }

    /// Verifies restart_threshold's true geometric case directly:
    /// threshold at restart index k (0-indexed) is
    /// GEOMETRIC_BASE_CONFLICTS * GEOMETRIC_GROWTH_FACTOR^k, a
    /// sequence with a constant ratio (GEOMETRIC_GROWTH_FACTOR)
    /// between consecutive terms, unlike the polynomial case above.
    #[test]
    fn test_restart_threshold_geometric() {
        for k in 0..4 {
            let want =
                (GEOMETRIC_BASE_CONFLICTS as f64 * GEOMETRIC_GROWTH_FACTOR.powi(k as i32)) as usize;
            assert_eq!(restart_threshold(RestartStrategy::Geometric, k), want);
        }
        // Directly pin the first four terms against the known
        // constants (base 100, ratio 1.5), so a future change to the
        // constants themselves is caught by this test's own
        // formula-based check above, while this pins the actual
        // numbers STAGE15.md's default configuration produces today.
        for (k, want) in [100, 150, 225, 337].into_iter().enumerate() {
            assert_eq!(restart_threshold(RestartStrategy::Geometric, k), want);
        }
    }

    /// Verifies that maybe_restart never triggers a restart when
    /// restart_strategy is RestartStrategy::None, regardless of how
    /// many conflicts have accumulated.
    #[test]
    fn test_maybe_restart_is_no_op_for_restart_none() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1, 2]],
        };
        let (_working_problem, _watch, mut x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let mut trail: Vec<usize> = Vec::new();
        let mut trail_lim: Vec<usize> = vec![0];
        let mut current_level = 0usize;
        let mut q_head = 0usize;
        let mut lrb = LrbState::new(2);
        let mut saved_phase = vec![Value::False; 3];
        let mut conflicts_since_restart = 1_000_000usize;
        let mut restart_count = 0usize;

        maybe_restart(
            RestartStrategy::None,
            &mut conflicts_since_restart,
            &mut restart_count,
            &mut trail,
            &mut trail_lim,
            &mut x,
            &mut q_head,
            &mut current_level,
            SelectVarVariant::Weighted,
            0,
            &mut lrb,
            &mut saved_phase,
        );

        assert_eq!(restart_count, 0, "RestartStrategy::None must never restart");
        assert_eq!(conflicts_since_restart, 1_000_000);
    }

    /// Verifies the actual restart mechanics for RestartStrategy::Luby:
    /// below threshold, nothing happens; at or above it,
    /// backtrack_to(0, ...) runs (undoing the level-1 assignment), the
    /// conflict counter resets, and restart_count advances -- while
    /// the learned clause added beforehand survives the restart
    /// untouched, per STAGE15.md's explicit requirement.
    #[test]
    fn test_maybe_restart_triggers_at_threshold_and_resets() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1, 2]],
        };
        let (mut working_problem, mut watch, mut x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let mut lists = occurrence::build(&working_problem);
        let mut level = vec![0usize; 3];
        let mut clause_activity = vec![0.0f64; working_problem.clauses.len()];
        let mut estimated_bytes = 0i64;
        let learned_idx = add_learned_clause(
            &vec![-1, 2],
            &mut working_problem.clauses,
            &mut lists,
            &mut watch,
            &level,
            &mut clause_activity,
            &mut estimated_bytes,
        )
        .expect("expected a stored clause");
        let before_clauses = working_problem.clauses.len();

        let mut reason: Vec<Option<usize>> = vec![None; 3];
        let mut trail: Vec<usize> = Vec::new();
        let mut trail_lim: Vec<usize> = vec![0];
        let mut current_level = 0usize;
        let mut q_head = 0usize;
        let mut lrb = LrbState::new(2);
        let mut saved_phase = vec![Value::False; 3];

        current_level += 1;
        trail_lim.push(trail.len());
        assign_literal(
            1,
            current_level,
            None,
            &mut x,
            &mut level,
            &mut reason,
            &mut trail,
            SelectVarVariant::Weighted,
            0,
            &mut lrb,
        );

        let mut conflicts_since_restart = LUBY_BASE_CONFLICTS * luby_term(0) - 1;
        let mut restart_count = 0usize;
        maybe_restart(
            RestartStrategy::Luby,
            &mut conflicts_since_restart,
            &mut restart_count,
            &mut trail,
            &mut trail_lim,
            &mut x,
            &mut q_head,
            &mut current_level,
            SelectVarVariant::Weighted,
            0,
            &mut lrb,
            &mut saved_phase,
        );
        assert_eq!(restart_count, 0, "below threshold, must not restart yet");
        assert_ne!(
            x[1],
            Value::Unassigned,
            "variable 1 was unassigned before the threshold was reached"
        );

        conflicts_since_restart += 1;
        maybe_restart(
            RestartStrategy::Luby,
            &mut conflicts_since_restart,
            &mut restart_count,
            &mut trail,
            &mut trail_lim,
            &mut x,
            &mut q_head,
            &mut current_level,
            SelectVarVariant::Weighted,
            0,
            &mut lrb,
            &mut saved_phase,
        );

        assert_eq!(restart_count, 1, "threshold reached");
        assert_eq!(conflicts_since_restart, 0, "reset to 0");
        assert_eq!(current_level, 0, "current_level after restart");
        assert_eq!(x[1], Value::Unassigned, "x[1] after restart");
        assert_eq!(
            working_problem.clauses.len(),
            before_clauses,
            "learned clauses must survive a restart"
        );
        assert_eq!(working_problem.clauses[learned_idx][0], -1);
    }

    /// Checks both restart strategies end to end against a problem
    /// already verified without restarts
    /// (test_run_proves_unsatisfiable_pigeonhole), confirming restarts
    /// (which repeatedly discard the decision stack but must keep
    /// every learned clause) don't change the verdict.
    #[test]
    fn test_run_with_restarts_still_proves_unsatisfiable_pigeonhole() {
        for restart_strategy in [
            RestartStrategy::Luby,
            RestartStrategy::Polynomial,
            RestartStrategy::Geometric,
        ] {
            let problem = pigeonhole_problem(4, 3);
            let mut rng = StdRng::seed_from_u64(9);

            let result = run(
                &problem,
                None,
                SelectVarVariant::Fast,
                restart_strategy,
                None,
                &mut rng,
                0,
            );

            assert!(
                !result.satisfiable,
                "restart strategy {restart_strategy:?}: expected unsatisfiable"
            );
            assert!(
                !result.timed_out,
                "restart strategy {restart_strategy:?}: unexpected timeout"
            );
        }
    }

    /// The satisfiable-path analog of
    /// test_run_with_restarts_still_proves_unsatisfiable_pigeonhole.
    #[test]
    fn test_run_with_restarts_still_finds_satisfiable_formula() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![1, 2], vec![-1, 3], vec![-2, -3]],
        };

        for restart_strategy in [
            RestartStrategy::Luby,
            RestartStrategy::Polynomial,
            RestartStrategy::Geometric,
        ] {
            let mut rng = StdRng::seed_from_u64(1);
            let result = run(
                &problem,
                None,
                SelectVarVariant::Vsids,
                restart_strategy,
                None,
                &mut rng,
                0,
            );
            assert!(
                result.satisfiable,
                "restart strategy {restart_strategy:?}: expected satisfiable"
            );
            for (ci, clause) in problem.clauses.iter().enumerate() {
                let satisfied = clause
                    .iter()
                    .any(|&lit| assignment::literal_is_true(&result.assignment, lit));
                assert!(
                    satisfied,
                    "restart strategy {restart_strategy:?}: clause {ci} ({clause:?}) not satisfied"
                );
            }
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

        let result = run(
            &problem,
            None,
            SelectVarVariant::Fast,
            RestartStrategy::None,
            None,
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
            None,
            SelectVarVariant::Weighted,
            RestartStrategy::None,
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
            None,
            SelectVarVariant::Weighted,
            RestartStrategy::None,
            None,
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
            RestartStrategy::None,
            None,
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
