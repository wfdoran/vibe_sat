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
//!
//! STAGE34.md adds "Literal Block Distance" (LBD) clause management
//! (Audemard & Simon, "Predicting Learnt Clauses Quality in Modern SAT
//! Solvers," IJCAI 2009 -- the paper introducing Glucose): the number
//! of distinct decision levels represented among a learned clause's
//! literals (see [`analyze`]'s third return value), computed once per
//! conflict in the same pass that already computes `backtrack_level`.
//! A low LBD ("glue clause," at or below `GLUE_CLAUSE_LBD_THRESHOLD`)
//! means the clause is "compact" and likely to remain useful; LBD is
//! used two ways here, both new with this stage:
//!
//! - [`reduce_clause_database`] now sorts eligible clauses by LBD
//!   first (higher LBD deleted first) with activity only a tiebreak
//!   among equal LBDs, and never considers a glue clause eligible for
//!   deletion at all, regardless of its activity.
//! - `RestartStrategy::Glucose` (see [`glucose_should_restart`])
//!   restarts not on a fixed conflict-count schedule but whenever a
//!   short-term moving average of recent LBDs looks close to or worse
//!   than the all-time average -- a sign the search has drifted into
//!   learning less useful clauses than its own history.
//!
//! Selected via `--alg-params` val2=5; the default remains
//! `RestartStrategy::Polynomial` (see [`run`]'s doc comment) since
//! this project's benchmark-driven-default convention means
//! `RestartStrategy::Glucose` earns that status only once a real
//! benchmark comparison shows it ahead on this project's own set.
//!
//! STAGE20.md/STAGE21.md add multithreading: [`run_parallel`]
//! implements Option B of `reports/REPORT20.md` (a continuously
//! shared clause pool; every worker otherwise runs the ordinary
//! single-threaded search, independently, over the *complete*
//! original problem -- no divide-and-conquer, no work
//! redistribution). See [`run_parallel`]'s own doc comment, and
//! [`clause_share`] for the lock-free clause-sharing mechanism.
//! STAGE21.md also adds a fourth [`RestartStrategy`],
//! `RestartStrategy::RoundRobin`, so that a multithreaded run's
//! workers don't all race with the identical restart cadence; see
//! its own doc comment and [`resolve_restart_strategy`].
use std::sync::atomic::{AtomicBool, AtomicI32, Ordering};
use std::thread;
use std::time::{Duration, Instant};

use rand::rngs::StdRng;
use rand::{Rng, RngExt, SeedableRng};

use crate::assignment::{self, Assignment, Value};
use crate::cnf::{self, Clause, Literal, Problem};
use crate::dfs::{select_var, select_var_fast_pick};
use crate::hillclimb::walksat::{WalkSatParams, run_walksat};
use crate::occurrence::{self, Lists};
use crate::params;
use crate::preprocess;

mod clause_share;
use clause_share::ExportBuffer;

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
    /// STAGE34.md's addition: Glucose's own data-driven restart policy
    /// (see the module doc comment and [`glucose_should_restart`]),
    /// rather than a fixed conflict-count schedule like the three
    /// strategies above. STAGE44.md moved this variant's `--alg-params`
    /// numeral to 4 (previously 5) specifically so `RoundRobin` --
    /// the one "meta" choice that isn't itself a schedule -- keeps the
    /// highest numeral as the list of real strategies grows, rather
    /// than sitting in the middle of it.
    Glucose,
    /// STAGE21.md's addition, meaningful only as a value
    /// [`run_parallel`]/[`run`] resolve away before ever calling
    /// [`run_loop`] -- `run_loop`'s own `restart_strategy` parameter
    /// is never `RoundRobin`, and `restart_threshold` never needs to
    /// handle it. It means: worker i uses
    /// `ROUND_ROBIN_STRATEGIES[i % 4]` (quadratic, geometric, Luby,
    /// Glucose, quadratic, ... -- see [`resolve_restart_strategy`]),
    /// so each of the four strategies runs on as close to an equal
    /// share of workers as `num_threads` allows, rather than every
    /// worker racing with the identical restart cadence. STAGE44.md
    /// moved this variant's numeral to 5 (previously 4) and folded
    /// `Glucose` into the rotation (previously three strategies,
    /// quadratic/geometric/Luby only) -- see `ROUND_ROBIN_STRATEGIES`.
    RoundRobin,
}

/// [`RestartStrategy::RoundRobin`]'s resolution order (see
/// [`resolve_restart_strategy`]): worker 0 uses quadratic
/// (`Polynomial`), worker 1 geometric, worker 2 Luby, worker 3 uses
/// Glucose (folded into the rotation by STAGE44.md), worker 4 cycles
/// back to quadratic, and so on. Assigning by spawn-order index -- a
/// plain `usize` [`run_parallel`] already hands every worker thread,
/// the same way Stage 17/18's winner index and per-worker sub-RNG
/// already are -- gives an exactly even split with no extra
/// bookkeeping, which is why this doesn't fall back to randomizing
/// among the four (something STAGE21.md allowed for in case a
/// deterministic per-thread index turned out to be awkward to get at;
/// it isn't, in either language -- though STAGE44.md notes randomizing
/// may be worth revisiting once this rotation is combined with
/// `PhaseStrategy::RoundRobin`'s own, see `PHASE_ROUND_ROBIN_STRATEGIES`).
const ROUND_ROBIN_STRATEGIES: [RestartStrategy; 4] = [
    RestartStrategy::Polynomial,
    RestartStrategy::Geometric,
    RestartStrategy::Luby,
    RestartStrategy::Glucose,
];

/// Returns the concrete restart strategy worker `thread_index` should
/// actually use: `restart_strategy` unchanged, unless it is
/// `RestartStrategy::RoundRobin`, in which case it resolves to
/// `ROUND_ROBIN_STRATEGIES[thread_index % 4]`. Called with
/// `thread_index` 0 for [`run`] (so an explicit `--alg-params`
/// restart=5 with `--num-threads=1` still behaves sensibly -- it
/// resolves to the same `Polynomial` worker 0 of a round-robin
/// `run_parallel` run would get, rather than being silently
/// mishandled) and with each of `run_parallel`'s workers' own spawn
/// index.
///
/// STAGE44.md deliberately keeps this rotation's period (4) coprime
/// with `PhaseStrategy::RoundRobin`'s (3, see
/// `PHASE_ROUND_ROBIN_STRATEGIES`): since both resolve from the very
/// same `thread_index`, `gcd(4, 3) = 1` means the combined (restart
/// strategy, phase strategy) pair a worker gets is unique for 12
/// consecutive thread indices (`lcm(4, 3)`) before any repeat, rather
/// than the two rotations' patterns colliding every 3 threads the way
/// two same-period-3 rotations would -- a cheap, structural way to
/// broaden portfolio diversity across a reasonably large thread count
/// without needing a dedicated benchmark to justify it (see
/// reports/REPORT43.md's own open question about whether phase
/// round-robin's diversity value needed independent verification, and
/// reports/REPORT44.md for the fuller reasoning).
fn resolve_restart_strategy(
    restart_strategy: RestartStrategy,
    thread_index: usize,
) -> RestartStrategy {
    if restart_strategy == RestartStrategy::RoundRobin {
        ROUND_ROBIN_STRATEGIES[thread_index % 4]
    } else {
        restart_strategy
    }
}

/// Identifies which technique [`decision_literal`] uses to guess a
/// newly-decided variable's polarity (STAGE43.md; see the module doc
/// comment for the literature behind each).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum PhaseStrategy {
    /// STAGE14.md's original, and `cdcl`'s default: guess the
    /// polarity the variable last held before becoming unassigned
    /// (`saved_phase`), or `False` if it has never been assigned
    /// before.
    Saving,
    /// STAGE43.md's addition: guess the polarity recorded in
    /// `target_phase`, the assignment snapshotted at the search's
    /// deepest trail so far (see [`update_target_phase`]), rather
    /// than the most recently held one.
    Target,
    /// STAGE43.md's addition: behaves exactly like `Saving`
    /// (`decision_literal` still reads `saved_phase`) except that
    /// `saved_phase` is also periodically overwritten wholesale by a
    /// WalkSAT burst's result (see [`maybe_rephase`] and
    /// [`maybe_restart`]).
    RephaseWalkSAT,
    /// Like `RestartStrategy::RoundRobin`, meaningful only as a value
    /// [`run_parallel`] resolves away before ever calling
    /// [`run_loop`] -- `run_loop`'s own `phase_strategy` parameter is
    /// never `PhaseRoundRobin`. It means: worker i uses
    /// `PHASE_ROUND_ROBIN_STRATEGIES[i % 3]`, diversifying
    /// phase-selection technique across the portfolio the same way
    /// `RestartStrategy::RoundRobin` already diversifies restart
    /// schedules.
    RoundRobin,
}

/// [`PhaseStrategy::RoundRobin`]'s resolution order (see
/// [`resolve_phase_strategy`]): worker 0 uses `Saving`, worker 1
/// `Target`, worker 2 `RephaseWalkSAT`, worker 3 cycles back to
/// `Saving`, and so on -- mirroring [`ROUND_ROBIN_STRATEGIES`]
/// exactly, one entry per implemented strategy.
const PHASE_ROUND_ROBIN_STRATEGIES: [PhaseStrategy; 3] = [
    PhaseStrategy::Saving,
    PhaseStrategy::Target,
    PhaseStrategy::RephaseWalkSAT,
];

/// Returns the concrete phase strategy worker `thread_index` should
/// actually use: `phase_strategy` unchanged, unless it is
/// `PhaseStrategy::RoundRobin`, in which case it resolves to
/// `PHASE_ROUND_ROBIN_STRATEGIES[thread_index % 3]`. Called with
/// `thread_index` 0 for [`run`], exactly like
/// [`resolve_restart_strategy`].
fn resolve_phase_strategy(phase_strategy: PhaseStrategy, thread_index: usize) -> PhaseStrategy {
    if phase_strategy == PhaseStrategy::RoundRobin {
        PHASE_ROUND_ROBIN_STRATEGIES[thread_index % 3]
    } else {
        phase_strategy
    }
}

// `LUBY_BASE_CONFLICTS` and `POLYNOMIAL_BASE_CONFLICTS` are
// STAGE15.md's "b" and "a": the scale constants for the Luby and
// polynomial restart sequences respectively (see
// [`restart_threshold`]), expressed in conflicts (see the module doc
// comment for why conflicts, not decisions, is the chosen restart
// statistic). Both are internal parameters, not exposed via
// `--alg-params`, per STAGE15.md's explicit instruction that they're
// meant to be optimized later instead.
//
// `LUBY_BASE_CONFLICTS` uses MiniSat's own default Luby restart base
// (its `-rfirst` option, 100 conflicts) -- a genuinely standard
// value in the literature/practice, inherited unchanged by most
// MiniSat-lineage solvers (Glucose, CryptoMiniSat, etc.), and exactly
// the kind of standard STAGE15.md asks to prefer when one exists.
//
// `POLYNOMIAL_BASE_CONFLICTS` has no such standard to inherit: the
// quadratic sequence STAGE15.md originally specified under the name
// "geometric" growth (a*k^2) is not itself a geometric sequence (see
// the module doc comment), so no standard constant applies to this
// exact formula. Per STAGE15.md's fallback instruction, this is
// instead picked empirically to be about one second of work on this
// project's own uf250/uuf250 benchmark sample: measured at
// ~17,300-18,900 conflicts/second across ten sampled instances (five
// uf250-1065, five uuf250-1065) under this project's current default
// `cdcl` configuration (VSIDS + phase saving), rounded to 18000.
//
// `GEOMETRIC_BASE_CONFLICTS` and `GEOMETRIC_GROWTH_FACTOR` are "c"
// and "r" for the true geometric schedule (`RestartStrategy::Geometric`):
// the restart interval starts at c conflicts and is multiplied by r
// after every restart. Unlike `POLYNOMIAL_BASE_CONFLICTS`, a standard
// pairing of these two constants does exist in the literature:
// MiniSat 1.13/1.14's geometric restart scheme (the scheme Luby
// restarts later replaced as MiniSat's default) used a base restart
// interval of 100 conflicts -- the same "rfirst" constant reused
// here as `LUBY_BASE_CONFLICTS` -- and a growth factor of 1.5. That
// 1.5 was itself a practical (not theoretical) choice: a value a bit
// below the golden ratio (~1.618), the same growth-factor reasoning
// used when picking dynamic array growth factors to allow memory
// reuse (a factor at or above the golden ratio can never reuse
// previously freed memory as it grows). Per STAGE15.md's preference
// for a standard value when one exists, both constants are taken
// from that standard MiniSat pairing rather than re-derived
// empirically.
//
// STAGE39.md: all four of these moved from compile-time constants to
// runtime-configurable fields on [`params::Cdcl`]
// (`luby_base_conflicts`, `polynomial_base_conflicts`,
// `geometric_base_conflicts`, `geometric_growth_factor`), so they can
// be experimented with via `.vibe_sat.json` without a rebuild; the
// values described above are exactly [`params::default`]'s values for
// these fields, unchanged by the migration.

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
fn restart_threshold(
    restart_strategy: RestartStrategy,
    restart_count: usize,
    p: &params::Cdcl,
) -> usize {
    match restart_strategy {
        RestartStrategy::Luby => p.luby_base_conflicts * luby_term(restart_count),
        RestartStrategy::Polynomial => {
            let k = restart_count + 1;
            p.polynomial_base_conflicts * k * k
        }
        RestartStrategy::Geometric => {
            (p.geometric_base_conflicts as f64
                * p.geometric_growth_factor.powi(restart_count as i32)) as usize
        }
        RestartStrategy::None => 0, // never actually consulted (see maybe_restart)
        // Never actually reaches a real solver: run/run_parallel
        // resolve RoundRobin away via resolve_restart_strategy before
        // any restart_threshold call is possible with it.
        RestartStrategy::RoundRobin => 0,
        // Never actually consulted: maybe_restart special-cases
        // RestartStrategy::Glucose before ever calling this function
        // (see glucose_should_restart instead).
        RestartStrategy::Glucose => 0,
    }
}

// STAGE34.md: `GLUE_CLAUSE_LBD_THRESHOLD` is the LBD at or below
// which a learned clause is a "glue clause" -- protected from
// [`reduce_clause_database`] regardless of activity. `GLUCOSE_K` and
// `GLUCOSE_WINDOW_SIZE` are Glucose's own restart policy's parameters
// (see [`glucose_should_restart`]).
//
// `GLUCOSE_WINDOW_SIZE` keeps Glucose's own originally published
// value (50): a benchmark sweep (STAGE35.md; see
// `reports/REPORT35.md`) tried 30 and 100 against it and found
// neither a clear win -- 30 solved fewer instances outright, 100 was
// a statistical wash -- so there was no real evidence to move off the
// literature default.
//
// `GLUCOSE_K` does NOT keep Glucose's own value (0.8): the same
// sweep found 0.6 solving as many or more instances as 0.8 while
// roughly halving mean solve time among those solved (2.44s/2.18s
// vs. 3.27s/2.93s Go/Rust on the report's 52-file sample), a real,
// measured win rather than a rounding-error difference -- restarting
// less often than Glucose's own default suggests turned out to
// matter on this project's own benchmark mix, matching the pattern
// already found for VSIDS-over-LRB (`reports/REPORT13.md`) and
// polynomial-over-Luby (`reports/REPORT15.md`): trust this project's
// own measurement over a technique's published default once they
// disagree.
// STAGE39.md: `GLUE_CLAUSE_LBD_THRESHOLD`, `GLUCOSE_WINDOW_SIZE`, and
// `GLUCOSE_K` all moved from compile-time constants to
// runtime-configurable fields on [`params::Cdcl`]
// (`glue_clause_lbd_threshold`, `glucose_window_size`, `glucose_k`);
// the values described above are exactly [`params::default`]'s values
// for these fields, unchanged by the migration.

/// STAGE34.md's Glucose-restart bookkeeping: a ring buffer (STAGE39.md:
/// `Vec`, sized at construction time to the runtime-configurable
/// `glucose_window_size` -- was a fixed-size array when that was a
/// compile-time constant) of the most recent learned clauses' LBDs
/// (kept as an incrementally-updated sum, so [`record_lbd`] is O(1)
/// regardless of window size) alongside an all-time running sum/count
/// -- never reset, including across restarts, since restarts don't
/// erase learned clauses or their LBDs either. Allocated
/// unconditionally (cheap) but only ever consulted when
/// `restart_strategy` is actually `RestartStrategy::Glucose`.
struct GlucoseState {
    recent_buf: Vec<usize>,
    recent_pos: usize,
    recent_sum: usize,
    recent_filled: bool,
    global_sum: u64,
    global_count: u64,
}

impl GlucoseState {
    fn new(window_size: usize) -> Self {
        Self {
            recent_buf: vec![0; window_size],
            recent_pos: 0,
            recent_sum: 0,
            recent_filled: false,
            global_sum: 0,
            global_count: 0,
        }
    }
}

/// Folds one freshly learned clause's LBD into `glucose`'s moving
/// averages (STAGE34.md). Called once per conflict, immediately after
/// [`analyze`], regardless of which restart strategy is active, so
/// that switching strategies mid-run (not currently possible, but
/// kept simple) would never find the averages cold.
fn record_lbd(glucose: &mut GlucoseState, lbd: usize) {
    glucose.recent_sum -= glucose.recent_buf[glucose.recent_pos];
    glucose.recent_buf[glucose.recent_pos] = lbd;
    glucose.recent_sum += lbd;
    glucose.recent_pos += 1;
    if glucose.recent_pos == glucose.recent_buf.len() {
        glucose.recent_pos = 0;
        glucose.recent_filled = true;
    }

    glucose.global_sum += lbd as u64;
    glucose.global_count += 1;
}

/// Implements Glucose's own data-driven restart policy (Audemard &
/// Simon, IJCAI 2009; see the module doc comment): restart when the
/// recent window's average LBD is close to or worse than (i.e. at
/// least `GLUCOSE_K` times) the all-time global average -- a sign
/// the search has drifted into a region where it's learning less
/// compact, less reusable clauses than its own history, and is better
/// off abandoning the current decision stack. Requires at least one
/// full recent window's worth of learned clauses before ever
/// triggering, both because the ring buffer isn't a meaningful
/// average until then and because `global_count` must be positive to
/// divide by.
fn glucose_should_restart(glucose: &GlucoseState, glucose_k: f64) -> bool {
    if !glucose.recent_filled {
        return false;
    }
    let recent_avg = glucose.recent_sum as f64 / glucose.recent_buf.len() as f64;
    let global_avg = glucose.global_sum as f64 / glucose.global_count as f64;
    recent_avg * glucose_k >= global_avg
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
///
/// STAGE34.md: `RestartStrategy::Glucose` is handled separately from
/// the four fixed-schedule strategies above -- it doesn't consult
/// [`restart_threshold`] or `*conflicts_since_restart`'s count against
/// a precomputed number at all, only [`glucose_should_restart`]'s
/// data-driven comparison of recent vs. global average LBD (see the
/// module doc comment).
///
/// STAGE34.md also fixes a latent bug this stage's testing surfaced:
/// `backtrack_to(0, ...)` indexes `trail_lim[1]`, which only exists if
/// `*current_level > 0`. Immediately after a conflict resolves to a
/// level-0 learned unit clause, `*current_level` is already 0 by the
/// time this runs (the conflict-handling code's own `backtrack_to`
/// already got there); calling `backtrack_to(0, ...)` again here would
/// panic. The fixed-threshold strategies' large bases (hundreds to
/// tens of thousands of conflicts) made this coincidence essentially
/// unreachable in practice, but `RestartStrategy::Glucose` can trigger
/// every ~`GLUCOSE_WINDOW_SIZE` conflicts, hitting it on real
/// benchmark files. There is nothing to restart when already at the
/// root regardless of strategy, so this simply defers to the next
/// conflict -- without resetting `*conflicts_since_restart` or
/// advancing `*restart_count`, since no restart actually happened.
/// Returns `Some(assignment)` if a periodic WalkSAT rephasing burst
/// (see [`maybe_rephase`]) happened to solve the whole problem
/// outright -- the rare edge case STAGE43.md flags explicitly, since
/// WalkSAT is a complete SAT-solving method on its own, not just a
/// phase-quality heuristic. `None` in every other case (including
/// every call where no restart happens at all, or `phase_strategy`
/// isn't [`PhaseStrategy::RephaseWalkSAT`]).
#[allow(clippy::too_many_arguments)]
fn maybe_restart<R: Rng>(
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
    glucose: &GlucoseState,
    p: &params::Cdcl,
    phase_strategy: PhaseStrategy,
    working_problem: &Problem,
    lists: &Lists,
    rng: &mut R,
) -> Option<Assignment> {
    if restart_strategy == RestartStrategy::None {
        return None;
    }
    if *current_level == 0 {
        return None;
    }
    if restart_strategy == RestartStrategy::Glucose {
        if !glucose_should_restart(glucose, p.glucose_k) {
            return None;
        }
    } else if *conflicts_since_restart < restart_threshold(restart_strategy, *restart_count, p) {
        return None;
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
        p.lrb_alpha,
    );
    *conflicts_since_restart = 0;
    *restart_count += 1;
    maybe_rephase(
        phase_strategy,
        *restart_count,
        p,
        working_problem,
        lists,
        saved_phase,
        rng,
    )
}

/// STAGE43.md's hook into every restart: a no-op unless
/// `phase_strategy` is [`PhaseStrategy::RephaseWalkSAT`] and
/// `restart_count` is a multiple of `p.rephase_interval_restarts` (so
/// a WalkSAT burst runs roughly every that-many restarts, not every
/// single one -- `reports/REPORT33.md`'s own "every so often (every N
/// restarts, or N conflicts)" framing, resolved here in favor of
/// restart count since [`maybe_restart`] is already this function's
/// only caller and already tracks it). A `rephase_interval_restarts`
/// of 0 would divide by zero; [`params::default`] never sets it that
/// low, but a hand-edited `.vibe_sat.json` could, so this guards
/// against that explicitly rather than panicking on a malformed
/// config.
fn maybe_rephase<R: Rng>(
    phase_strategy: PhaseStrategy,
    restart_count: usize,
    p: &params::Cdcl,
    working_problem: &Problem,
    lists: &Lists,
    saved_phase: &mut [Value],
    rng: &mut R,
) -> Option<Assignment> {
    if phase_strategy != PhaseStrategy::RephaseWalkSAT {
        return None;
    }
    if p.rephase_interval_restarts == 0
        || !restart_count.is_multiple_of(p.rephase_interval_restarts)
    {
        return None;
    }
    rephase_from_walksat(working_problem, lists, p, saved_phase, rng)
}

/// Runs one bounded WalkSAT burst ([`run_walksat`], reusing `lists`
/// -- the current, learned-clause-augmented occurrence lists
/// [`clause_share::maybe_import`]/`add_learned_clause` already
/// maintain, not a fresh rebuild -- so the burst benefits from
/// everything CDCL has learned so far too) and copies its resulting
/// assignment into `saved_phase` wholesale, overwriting whatever
/// backtracking had saved there. A single try (`num_tries = Some(1)`),
/// bounded to `p.rephase_max_flips` flips: this is a periodic nudge,
/// not a full local-search run in its own right, so it needs to stay
/// cheap relative to how rarely it fires. Returns `Some(assignment)`
/// if that burst happens to be a complete satisfying assignment on
/// its own (see [`maybe_restart`]'s doc comment).
fn rephase_from_walksat<R: Rng>(
    working_problem: &Problem,
    lists: &Lists,
    p: &params::Cdcl,
    saved_phase: &mut [Value],
    rng: &mut R,
) -> Option<Assignment> {
    let result = run_walksat(
        working_problem,
        lists,
        WalkSatParams {
            num_tries: Some(1),
            max_flips_per_try: p.rephase_max_flips,
            ..WalkSatParams::default()
        },
        rng,
        0,
    );
    if result.satisfiable {
        return Some(result.assignment);
    }
    saved_phase.copy_from_slice(&result.assignment);
    None
}

// `VAR_ACTIVITY_DECAY` is `SelectVarVariant::Vsids`'s per-variable
// analogue of `CLAUSE_ACTIVITY_DECAY` (see [`VsidsState`]); VSIDS
// conventionally decays faster than clause activity does (MiniSat's
// own defaults: 0.95 for variables, 0.999 for clauses), which is why
// this is a separate constant rather than reusing
// `CLAUSE_ACTIVITY_DECAY`.
//
// STAGE39.md: moved to [`params::Cdcl::var_activity_decay`]
// (runtime-configurable); the value above is
// [`params::default`]'s value for it, unchanged.

// The fixed learning-rate weight `SelectVarVariant::Lrb` uses when
// updating a variable's Q-value (see [`backtrack_to`]'s doc comment):
// the paper anneals this over the course of the search, starting high
// and decaying toward a floor; this implementation keeps it fixed at
// a value from within that range instead, as a documented
// simplification (see the module doc comment).
//
// STAGE39.md: moved to [`params::Cdcl::lrb_alpha`]
// (runtime-configurable); 0.4, the value named above, is
// [`params::default`]'s value for it, unchanged.

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
///
/// STAGE39.md: `CLAUSE_ACTIVITY_DECAY` moved to
/// [`params::Cdcl::clause_activity_decay`] (runtime-configurable;
/// 0.999, named above, is [`params::default`]'s value, unchanged).
/// `ACTIVITY_RESCALE_THRESHOLD` stays a plain compile-time constant --
/// it is a structural numerical-stability safety cap (keeping
/// activities well within f64's range), never a search-quality tuning
/// knob, so there is no reason to expose it via `.vibe_sat.json`.
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
    /// True if the search was abandoned before reaching a verdict,
    /// rather than exhausting the search space: either the time limit
    /// was reached, or (`run_parallel` only, STAGE20.md/STAGE21.md's
    /// Option B) another worker already reached a genuine verdict
    /// first and this one gave up early rather than duplicate work
    /// nobody needs anymore. `satisfiable`/`assignment` are
    /// meaningless whenever this is true.
    #[allow(dead_code)]
    pub timed_out: bool,
}

/// Performs a CDCL search: repeatedly propagate, and on a conflict,
/// learn a clause and backjump; on reaching a fixpoint with no
/// conflict, either the assignment is complete (satisfiable) or a new
/// variable is chosen to branch on. If propagation ever conflicts
/// while at decision level 0 (nothing left to backjump to), the
/// problem is proven unsatisfiable. See [`run_loop`]'s doc comment
/// for what every parameter here means -- `run` is a thin wrapper
/// (prints the "cdcl: ..." announcement and final verdict) around it,
/// matching Stage 17/18's identical `run`/`run_loop` split; the only
/// difference from calling `run_loop` directly is that `verbose`
/// controls progress output here.
#[allow(clippy::too_many_arguments)]
pub fn run<R: Rng>(
    problem: &Problem,
    time_limit: Option<Duration>,
    variant: SelectVarVariant,
    restart_strategy: RestartStrategy,
    memory_limit_bytes: Option<i64>,
    rng: &mut R,
    verbose: i32,
    p: &params::Cdcl,
    phase_strategy: PhaseStrategy,
) -> SolveResult {
    if verbose >= 1 {
        println!(
            "cdcl: {}{}",
            describe_params(time_limit, variant, restart_strategy, phase_strategy),
            describe_memory_limit(memory_limit_bytes)
        );
    }
    let result = run_loop(
        problem,
        time_limit,
        variant,
        resolve_restart_strategy(restart_strategy, 0),
        memory_limit_bytes,
        rng,
        None,
        None,
        &[],
        p,
        resolve_phase_strategy(phase_strategy, 0),
    );
    if verbose >= 1 {
        println!("{}", verdict(&result));
    }
    result
}

/// Runs the same search as [`run`], split across `num_threads`
/// concurrent workers implementing STAGE20.md/STAGE21.md's Option B:
/// every worker independently runs the ordinary single-threaded
/// search over the *complete* original problem -- no divide-and-
/// conquer, no work redistribution, nothing shared between workers
/// except learned clauses (see [`clause_share`]) -- so unlike `dfs`'s
/// parallel search (STAGE18.md), any single worker's own verdict, SAT
/// or UNSAT, is already the authoritative answer for the whole
/// problem the moment it's reached; there is no termination-detection
/// protocol to build here at all, only the same CAS-based
/// single-winner shutdown Stage 17/18 already established.
///
/// `num_threads <= 1` delegates straight to [`run`], with `rng` used
/// exactly as it always has been, so behavior is bit-for-bit
/// identical to calling `run` directly whenever multithreading isn't
/// in use, matching the same guarantee Stage 17/18 established for
/// the parallel hillclimb/walksat/dfs entry points.
///
/// `restart_strategy` is resolved per worker via
/// [`resolve_restart_strategy`]: `RestartStrategy::RoundRobin`
/// (STAGE21.md) assigns worker i `ROUND_ROBIN_STRATEGIES[i % 3]` (an
/// even split of quadratic/geometric/Luby across the workers); any
/// other explicit strategy, including `RestartStrategy::None`, is
/// used unchanged by every worker. Per STAGE21.md, callers (see
/// `main.rs`'s `run_cdcl`) are expected to pass `RoundRobin` as the
/// default restart strategy whenever `num_threads > 1` and the caller
/// didn't explicitly ask for something else, and whatever was
/// explicitly asked for otherwise -- that policy lives in `main.rs`,
/// not here.
///
/// `rng` is used, before any worker starts, to derive one independent
/// sub-generator per worker (matching Stage 17/18's identical
/// pattern), so the overall result is fully reproducible given
/// (`rng`'s state, `num_threads`) even though which worker's answer
/// wins a race to a verdict is not. `num_decisions`/`num_conflicts`
/// are summed across every worker, win or lose, matching Stage 18's
/// `num_nodes` convention.
#[allow(clippy::too_many_arguments)]
pub fn run_parallel<R: Rng>(
    problem: &Problem,
    time_limit: Option<Duration>,
    variant: SelectVarVariant,
    restart_strategy: RestartStrategy,
    memory_limit_bytes: Option<i64>,
    num_threads: usize,
    rng: &mut R,
    verbose: i32,
    p: &params::Cdcl,
    phase_strategy: PhaseStrategy,
) -> SolveResult {
    if num_threads <= 1 {
        return run(
            problem,
            time_limit,
            variant,
            restart_strategy,
            memory_limit_bytes,
            rng,
            verbose,
            p,
            phase_strategy,
        );
    }

    if verbose >= 1 {
        println!(
            "cdcl: {}{}",
            describe_parallel_params(
                time_limit,
                variant,
                restart_strategy,
                phase_strategy,
                num_threads
            ),
            describe_memory_limit(memory_limit_bytes)
        );
    }

    let export_buffers: Vec<ExportBuffer> = (0..num_threads)
        .map(|_| ExportBuffer::new(clause_share::EXPORT_BUFFER_CAPACITY))
        .collect();
    // peers_for_worker[i] is every OTHER worker's export buffer --
    // this worker's own is deliberately excluded, matching dfs's
    // shuffled_peers convention of never stealing from/importing
    // one's own outbox.
    let peers_for_worker: Vec<Vec<&ExportBuffer>> = (0..num_threads)
        .map(|i| {
            export_buffers
                .iter()
                .enumerate()
                .filter(|&(j, _)| j != i)
                .map(|(_, b)| b)
                .collect()
        })
        .collect();
    let mut sub_rngs: Vec<StdRng> = (0..num_threads)
        .map(|_| StdRng::seed_from_u64(rng.random::<u64>()))
        .collect();

    let stop = AtomicBool::new(false);
    let winner = AtomicI32::new(-1);

    let mut results: Vec<SolveResult> = Vec::new();
    thread::scope(|scope| {
        let handles: Vec<_> = sub_rngs
            .iter_mut()
            .enumerate()
            .map(|(i, sub_rng)| {
                let export = &export_buffers[i];
                let peers = &peers_for_worker[i];
                let stop = &stop;
                let winner = &winner;
                let thread_strategy = resolve_restart_strategy(restart_strategy, i);
                let thread_phase_strategy = resolve_phase_strategy(phase_strategy, i);

                scope.spawn(move || {
                    // The winner CAS must happen here, inside this
                    // worker's own thread, immediately after run_loop
                    // returns -- not deferred to after every thread
                    // has already been joined -- exactly matching
                    // dfs::run_parallel's identical structure and the
                    // reasoning documented there (Stage 18).
                    let result = run_loop(
                        problem,
                        time_limit,
                        variant,
                        thread_strategy,
                        memory_limit_bytes,
                        sub_rng,
                        Some(stop),
                        Some(export),
                        peers,
                        p,
                        thread_phase_strategy,
                    );
                    if !result.timed_out
                        && stop
                            .compare_exchange(false, true, Ordering::SeqCst, Ordering::SeqCst)
                            .is_ok()
                    {
                        winner.store(i as i32, Ordering::SeqCst);
                    }
                    result
                })
            })
            .collect();

        for handle in handles {
            results.push(handle.join().expect("cdcl worker thread panicked"));
        }
    });

    let mut final_result = SolveResult {
        satisfiable: false,
        assignment: assignment::new(0),
        num_decisions: 0,
        num_conflicts: 0,
        timed_out: false,
    };
    for r in &results {
        final_result.num_decisions += r.num_decisions;
        final_result.num_conflicts += r.num_conflicts;
    }
    match winner.load(Ordering::SeqCst) {
        idx if idx >= 0 => {
            let w = &results[idx as usize];
            final_result.satisfiable = w.satisfiable;
            final_result.assignment = w.assignment.clone();
        }
        _ => final_result.timed_out = true,
    }

    if verbose >= 1 {
        println!("{}", verdict(&final_result));
    }
    final_result
}

/// [`run`]'s (and each of [`run_parallel`]'s workers') search loop,
/// without the "cdcl: ..."/"SAT"/"UNSAT"/"UNKNOWN" announcements --
/// those are the caller's responsibility (`run` prints its own;
/// `run_parallel` prints one combined announcement/verdict for the
/// whole parallel search instead of one per worker), matching Stage
/// 17/18's identical `run_loop`/`dfs_worker` pattern. `stop`/`export`
/// are `None` and `peers` is empty for `run`'s own single-threaded
/// call; see [`clause_share`] for what each does.
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
/// once it is exceeded, checked after every step (STAGE35.md; see
/// this function's own time-check comment below). `rng` supplies the
/// randomness `SelectVarVariant::Weighted` uses to break ties.
#[allow(clippy::too_many_arguments)]
fn run_loop<R: Rng>(
    problem: &Problem,
    time_limit: Option<Duration>,
    variant: SelectVarVariant,
    restart_strategy: RestartStrategy,
    memory_limit_bytes: Option<i64>,
    rng: &mut R,
    stop: Option<&AtomicBool>,
    export: Option<&ExportBuffer>,
    peers: &[&ExportBuffer],
    p: &params::Cdcl,
    phase_strategy: PhaseStrategy,
) -> SolveResult {
    // STAGE35.md: captured before bootstrap's, not after -- see the
    // module doc comment's time-check paragraph (reports/REPORT35.md)
    // for why: a slow bootstrap (unit propagation + initial watch
    // selection) used to count for nothing against time_limit, silently
    // giving every run a full, uncounted time_limit's worth of
    // bootstrap time on top of the requested budget.
    let start_time = Instant::now();

    // A problem with no variables can only contain empty clauses (no
    // literal can reference a variable beyond num_vars), each of
    // which is unsatisfiable by construction; guard this degenerate
    // case explicitly, matching dfs::run.
    if problem.num_vars == 0 {
        let satisfiable = problem.clauses.is_empty();
        return SolveResult {
            satisfiable,
            assignment: assignment::new(0),
            num_decisions: 0,
            num_conflicts: 0,
            timed_out: false,
        };
    }

    let Some((mut working_problem, mut watch, mut x)) = bootstrap(problem) else {
        return SolveResult {
            satisfiable: false,
            assignment: assignment::new(problem.num_vars),
            num_decisions: 0,
            num_conflicts: 0,
            timed_out: false,
        };
    };

    let mut peer_cursors = vec![0u64; peers.len()];
    let mut lists = occurrence::build(&working_problem);
    let (mut watchers_positive, mut watchers_negative) =
        build_watchers(&watch, working_problem.num_vars);
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

    // STAGE34.md: clause_lbd is kept parallel to clauses/clause_activity
    // (see reduce_clause_database and add_learned_clause); the original
    // problem's clauses never participate in LBD-based eligibility
    // (reduce_clause_database only ever considers indices >=
    // num_original_clauses to begin with), so their placeholder value
    // here is never read. lbd_scratch is analyze's reused scratch
    // buffer for its distinct-decision-level count; glucose is
    // RestartStrategy::Glucose's own moving-average bookkeeping (see
    // the module doc comment), allocated unconditionally like vsids/lrb
    // below but only consulted when restart_strategy actually calls
    // for it.
    let mut clause_lbd = vec![0usize; num_original_clauses];
    let mut lbd_scratch: Vec<usize> = Vec::new();
    let mut glucose = GlucoseState::new(p.glucose_window_size);

    // STAGE36.md: minimize_clause's own scratch space (see
    // literal_redundant and MIN_UNDEF/MIN_REMOVABLE/MIN_FAILED).
    // min_state is sized num_vars+1, like seen; min_touched and
    // min_stack grow via push and are reset with .clear(), like
    // lbd_scratch, rather than reallocated. min_work counts
    // reason-clause literals examined so far in the current
    // minimize_clause call, checked against its work budget.
    let mut min_state = vec![MIN_UNDEF; problem.num_vars + 1];
    let mut min_touched: Vec<usize> = Vec::new();
    let mut min_stack: Vec<MinimizeFrame> = Vec::new();
    let mut min_work: usize = 0;

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

    // STAGE43.md's `PhaseStrategy::Target` bookkeeping (see
    // update_target_phase): meaningless, and left at their zero
    // values, for every other strategy.
    let mut target_phase = vec![Value::False; problem.num_vars + 1];
    let mut best_trail_len = 0usize;

    // STAGE15.md's restart bookkeeping: conflicts_since_restart counts
    // conflicts since the previous restart (or since the search
    // began, if none yet); restart_count is how many restarts have
    // happened so far, indexing into the Luby/geometric sequence (see
    // restart_threshold).
    let mut conflicts_since_restart = 0usize;
    let mut restart_count = 0usize;

    loop {
        if let Some(s) = stop
            && s.load(Ordering::SeqCst)
        {
            return SolveResult {
                satisfiable: false,
                assignment: assignment::new(problem.num_vars),
                num_decisions,
                num_conflicts,
                timed_out: true,
            };
        }
        // STAGE35.md: the time limit is checked on every single step
        // (decision or conflict), not periodically (previously gated by
        // a now-removed TIME_CHECK_INTERVAL bitmask, "check every 4096
        // steps"). That periodic check held up fine until REPORT29.md
        // measured it failing badly on real, large instances: a
        // --time-limit-secs=3 run blew its budget to 42.65 seconds (a
        // 14x overrun) because a single conflict's own cost had grown
        // large enough (large watch/occurrence lists) that waiting for
        // 4096 of them before the next clock check was no longer a
        // cheap amortization, just an unbounded-in-practice delay. A
        // fixed count-based interval can't fix this in general -- no
        // interval is both small enough to bound overrun on expensive
        // steps and large enough to stay "periodic" on cheap ones,
        // since both cases can occur in the same run.
        //
        // Measured directly (this stage) rather than assumed:
        // Instant::now() costs ~14ns/call on this hardware, against
        // ~318,000ns for a typical conflict on this project's own hard
        // benchmark instance -- unmeasurable overhead. See
        // reports/REPORT35.md.
        if let Some(limit) = time_limit
            && start_time.elapsed() >= limit
        {
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
            &mut watchers_positive,
            &mut watchers_negative,
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
                return SolveResult {
                    satisfiable: false,
                    assignment: assignment::new(problem.num_vars),
                    num_decisions,
                    num_conflicts,
                    timed_out: false,
                };
            }

            let (learned, backtrack_level, lbd) = analyze(
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
                &mut lbd_scratch,
                &mut min_state,
                &mut min_touched,
                &mut min_stack,
                &mut min_work,
                p.minimize_work_budget_factor,
            );
            record_lbd(&mut glucose, lbd);
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
                p.lrb_alpha,
            );
            let new_clause = add_learned_clause(
                &learned,
                lbd,
                &mut working_problem.clauses,
                &mut lists,
                &mut watchers_positive,
                &mut watchers_negative,
                &mut watch,
                &level,
                &mut clause_activity,
                &mut clause_lbd,
                &mut estimated_bytes,
            );
            // STAGE20.md/STAGE21.md's Option B: publish learned to
            // this thread's export buffer, continuously, the moment
            // it's learned -- not batched to some later point -- if
            // it qualifies (see clause_share::EXPORT_MAX_CLAUSE_LEN)
            // and this run is actually part of a parallel run
            // (export is Some). new_clause is None for a unit
            // learned clause (length 1), which is never shared; see
            // EXPORT_MAX_CLAUSE_LEN's doc comment for why.
            if let Some(export) = export
                && new_clause.is_some()
                && learned.len() <= clause_share::EXPORT_MAX_CLAUSE_LEN
            {
                export.publish(&learned);
            }
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
            clause_activity_increment /= p.clause_activity_decay;
            if clause_activity_increment > ACTIVITY_RESCALE_THRESHOLD {
                for a in clause_activity.iter_mut() {
                    *a /= ACTIVITY_RESCALE_THRESHOLD;
                }
                clause_activity_increment /= ACTIVITY_RESCALE_THRESHOLD;
            }
            if variant == SelectVarVariant::Vsids {
                vsids.increment /= p.var_activity_decay;
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
                    &mut watchers_positive,
                    &mut watchers_negative,
                    &mut watch,
                    &mut clause_activity,
                    &mut clause_lbd,
                    &mut estimated_bytes,
                    &x,
                    &mut reason,
                    num_original_clauses,
                    p.glue_clause_lbd_threshold,
                );
            }

            // STAGE20.md/STAGE21.md's Option B: give this thread a
            // chance to pull in clauses other threads have learned --
            // a no-op whenever peers is empty (every single-threaded
            // call). Once per conflict is the natural point to check,
            // since it's already the point where this thread's own
            // local database just changed shape.
            clause_share::maybe_import(
                peers,
                &mut peer_cursors,
                num_conflicts,
                &mut working_problem.clauses,
                &mut lists,
                &mut watchers_positive,
                &mut watchers_negative,
                &mut watch,
                &x,
                &mut clause_activity,
                &mut clause_lbd,
                &mut estimated_bytes,
            );

            conflicts_since_restart += 1;
            if let Some(solution) = maybe_restart(
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
                &glucose,
                p,
                phase_strategy,
                &working_problem,
                &lists,
                rng,
            ) {
                // STAGE43.md: a periodic WalkSAT rephasing burst is
                // itself a complete SAT-solving method, so it can --
                // rarely, but really -- return a complete satisfying
                // assignment on its own; report it directly rather
                // than discarding it and continuing to search via
                // CDCL.
                return SolveResult {
                    satisfiable: true,
                    assignment: solution,
                    num_decisions,
                    num_conflicts,
                    timed_out: false,
                };
            }
            continue;
        }

        if !x[1..].contains(&Value::Unassigned) {
            return SolveResult {
                satisfiable: true,
                assignment: x,
                num_decisions,
                num_conflicts,
                timed_out: false,
            };
        }

        if phase_strategy == PhaseStrategy::Target {
            update_target_phase(&trail, &x, &mut target_phase, &mut best_trail_len);
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
            decision_literal(v, phase_strategy, &saved_phase, &target_phase),
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

/// Records that clause index `c` is now one of `lit`'s watchers, in
/// whichever of `positive`/`negative` (both indexed by variable
/// number, mirroring [`Lists`]' own `positive`/`negative` shape)
/// actually corresponds to `lit`'s sign. A plain function taking both
/// slices explicitly (rather than a method on some combined watcher
/// state) for the same reason `propagate` itself takes independent
/// `&mut` parameters instead of `&mut self` -- see its own doc
/// comment.
fn append_watcher(
    positive: &mut [Vec<usize>],
    negative: &mut [Vec<usize>],
    lit: Literal,
    c: usize,
) {
    let v = cnf::literal_var(lit);
    if cnf::literal_is_negative(lit) {
        negative[v].push(c);
    } else {
        positive[v].push(c);
    }
}

/// Returns a mutable reference to the slice of clause indices
/// currently watching `lit` (`positive[lit.var()]` or
/// `negative[lit.var()]`, matching [`append_watcher`]'s own
/// `positive`/`negative` convention), so callers can push to or
/// compact it in place.
fn watchers_for<'a>(
    positive: &'a mut [Vec<usize>],
    negative: &'a mut [Vec<usize>],
    lit: Literal,
) -> &'a mut Vec<usize> {
    let v = cnf::literal_var(lit);
    if cnf::literal_is_negative(lit) {
        &mut negative[v]
    } else {
        &mut positive[v]
    }
}

/// Builds `watchers_positive`/`watchers_negative` from `watch` and
/// `num_vars`: every clause index appended to both of its two current
/// watches' watcher lists. Used wherever `watch` is freshly built or
/// wholesale-rebuilt ([`bootstrap`]'s callers and
/// [`reduce_clause_database`]), mirroring the same
/// `occurrence::build`-after-`watch`-is-known pattern [`Lists`] itself
/// already uses.
fn build_watchers(watch: &[[Literal; 2]], num_vars: usize) -> (Vec<Vec<usize>>, Vec<Vec<usize>>) {
    let mut positive = vec![Vec::new(); num_vars + 1];
    let mut negative = vec![Vec::new(); num_vars + 1];
    for (c, w) in watch.iter().enumerate() {
        append_watcher(&mut positive, &mut negative, w[0], c);
        append_watcher(&mut positive, &mut negative, w[1], c);
    }
    (positive, negative)
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
///
/// STAGE23.md: profiling (see `reports/REPORT23.md`) found this loop
/// responsible for essentially all (~98.6%) of total wall-clock time
/// on a hard benchmark instance -- the same finding as the Go port's
/// identical `propagate`, since both implement the same two-watched-
/// literal algorithm. `falsified_slot` (which of `watch[c]`'s two
/// entries currently holds `falsified_literal`) is remembered from
/// the lookup above rather than re-derived with a second `w[0] ==
/// falsified_literal` comparison when writing a replacement back --
/// a pure, behavior-preserving simplification, matching the
/// equivalent fix applied to the Go port. A second, more aggressive
/// idea (skip `choose_watch`'s scan entirely whenever `other_watch`
/// is already true) was tried and rejected: measured on this
/// project's own benchmark instances (mostly length-3 clauses), it
/// made things reproducibly *worse*, not better -- see
/// `reports/REPORT23.md` for why, and for why it's listed there as a
/// possible future change rather than applied here.
///
/// STAGE45.md: from Stage 9 through Stage 44, this loop's own
/// candidate source was `lists.positive`/`lists.negative` -- the
/// *full*, static occurrence index (every clause that ever mentions
/// the falsified literal), filtered down to actual watchers with a
/// per-candidate check-and-skip (the `else { continue; }` arm below
/// used to be reachable; it no longer is). `REPORT41.md`'s re-profile
/// found this loop still dominating at 97.66% of single-threaded CPU
/// time -- unsurprising in hindsight: reusing the occurrence index
/// this way meant paying O(occurrences of a literal) work on every
/// single propagation of it, not O(current watchers of a literal),
/// which is the entire point of the two-watched-literal scheme in the
/// first place (Moskewicz et al., Chaff, DAC 2001) and exactly the
/// gap "a genuinely different watch-list representation"
/// (`REPORT23.md`/`REPORT38.md`'s own phrasing) was gesturing at
/// without quite naming. `watchers_positive`/`watchers_negative` are
/// the fix: a dynamic, per-literal index that shrinks the moment a
/// watch moves away and grows the moment one moves in, so this loop
/// now only ever visits clauses genuinely watching the literal that
/// just became false. `lists` is kept only because
/// `rephase_from_walksat` (STAGE43.md) still needs a real full
/// occurrence index for WalkSAT's flip-scoring.
///
/// The scan below is MiniSat's own standard in-place compaction
/// pattern: since a clause whose watch moves away (`choose_watch`
/// found a replacement) must be removed from this literal's watcher
/// list, while every other clause visited stays, the list is
/// compacted in place with two indices -- `scanned` (how many entries
/// have been read) and `keep` (how many entries, of those scanned,
/// are being kept in this same list) -- rather than building a fresh
/// list or doing an O(n) shift per removal. A clause that finds no
/// replacement always stays a watcher of the literal that just went
/// false (there's nowhere better for it to watch until some future
/// backtrack un-falsifies that literal again), which is why "no
/// replacement" is the only case that writes into the kept prefix. If
/// a conflict is found mid-scan, the loop stops early (`break`), but
/// any not-yet-scanned entries are still genuine, valid watchers of
/// this literal and must be copied into the compacted prefix before
/// returning -- skipping this step would silently drop clauses from
/// their own watch list, a correctness bug that would only surface
/// later, as a missed propagation or a missed conflict, not here.
#[allow(clippy::too_many_arguments)]
fn propagate(
    clauses: &[Clause],
    watchers_positive: &mut [Vec<usize>],
    watchers_negative: &mut [Vec<usize>],
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

        let falsified_literal: Literal = if x[v] == Value::True {
            -(v as Literal)
        } else {
            v as Literal
        };

        let mut list = std::mem::take(watchers_for(
            watchers_positive,
            watchers_negative,
            falsified_literal,
        ));
        let mut keep = 0usize;
        let mut scanned = 0usize;
        let mut conflict = None;

        while scanned < list.len() {
            let c = list[scanned];
            scanned += 1;

            let w = watch[c];
            let (other_watch, falsified_slot) = if w[0] == falsified_literal {
                (w[1], 0)
            } else {
                (w[0], 1)
            };

            if is_true(other_watch, x) {
                // STAGE48.md's blocking-literal check: the clause is
                // already satisfied through its other watch, so there
                // is nothing to gain by scanning it for a new watch --
                // skip choose_watch entirely and just keep watching
                // falsified_literal until some future backtrack makes
                // it worth revisiting. This reverses an earlier,
                // narrower-context rejection of the same idea (see
                // reports/REPORT23.md/REPORT48.md): with propagate now
                // visiting only genuine watchers (STAGE45.md), rather
                // than every occurrence, choose_watch's own cost is a
                // large enough share of the total that skipping it
                // whenever possible is a clear net win.
                list[keep] = c;
                keep += 1;
                continue;
            }

            if let Some(replacement) = choose_watch(&clauses[c], x, Some(other_watch)) {
                watch[c][falsified_slot] = replacement;
                watchers_for(watchers_positive, watchers_negative, replacement).push(c);
                continue;
            }

            // No replacement available: c keeps watching falsified_literal.
            list[keep] = c;
            keep += 1;

            if is_false(other_watch, x) {
                conflict = Some(c);
                break;
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

        if scanned < list.len() {
            list.copy_within(scanned.., keep);
            keep += list.len() - scanned;
        }
        list.truncate(keep);
        *watchers_for(watchers_positive, watchers_negative, falsified_literal) = list;

        if conflict.is_some() {
            return conflict;
        }
    }
    None
}

/// `MIN_UNDEF`/`MIN_REMOVABLE`/`MIN_FAILED` are [`literal_redundant`]'s
/// three-state memoization marks for `min_state` (parallel to a
/// variable, sized `num_vars+1`, like `seen`), letting one
/// `minimize_clause` call reuse one variable's redundancy verdict
/// without re-scanning its reason clause: `MIN_REMOVABLE` means this
/// variable was already proven redundant (safe to treat as such
/// wherever else it's encountered); `MIN_FAILED` means it was already
/// proven NOT redundant (a decision variable, or itself blocked by
/// one, found while checking some earlier literal). `MIN_UNDEF` (the
/// default) means neither yet applies. `min_touched` lists every
/// variable whose `min_state` is currently non-default, so
/// `minimize_clause` can reset exactly those entries at the start of
/// its next call rather than the whole `num_vars`-sized array.
const MIN_UNDEF: u8 = 0;
const MIN_REMOVABLE: u8 = 1;
const MIN_FAILED: u8 = 2;

// `MINIMIZE_WORK_BUDGET_FACTOR` scales [`minimize_clause`]'s hard cap
// on total reason-clause literals examined across one call (see
// [`minimize_work_budget`]): STAGE36.md explicitly asked for a bound
// close to O(n log n) in the size of the learned clause, and for an
// absolute limit "just in case," matching the same concern that drove
// Stage 30/31's subsumption/BVE work budgets. 20 was chosen the same
// way those were: generous enough that it is never observed to
// trigger on this project's own benchmark set (see
// `reports/REPORT36.md`), while still being a real, finite cap rather
// than no cap at all.
//
// STAGE39.md: moved to [`params::Cdcl::minimize_work_budget_factor`]
// (runtime-configurable); 20, named above, is [`params::default`]'s
// value for it, unchanged.

/// Returns the total number of reason-clause literals
/// [`minimize_clause`] may examine (summed across every candidate
/// literal's [`literal_redundant`] call) while minimizing a clause of
/// length `n`, before giving up and keeping every remaining literal
/// unminimized. `n.leading_zeros()` gives a cheap, integer-only
/// approximation of `log2(n)+1` -- already this project's convention
/// for "a log-shaped bound" (see e.g. `luby_term`'s own iterative
/// doubling).
fn minimize_work_budget(n: usize, work_budget_factor: usize) -> usize {
    let bits_len = (usize::BITS - n.leading_zeros()) as usize;
    work_budget_factor * n * (bits_len + 1)
}

/// One entry in [`literal_redundant`]'s explicit, iterative-DFS stack:
/// `lit` is the literal whose reason clause is being scanned, and
/// `idx` is the next index into that clause to examine. An explicit
/// stack (rather than a recursive function call per implication-graph
/// edge) keeps a long chain of reasons from costing real call-stack
/// depth, matching MiniSat's own iterative implementation of this
/// exact algorithm.
struct MinimizeFrame {
    lit: Literal,
    idx: usize,
}

/// Implements STAGE36.md's learned-clause minimization (Sörensson &
/// Biere, "Minimizing Learned Clauses," SAT 2009 -- formalizing a
/// heuristic MiniSat itself has used since 2005): a literal in
/// `learned` is redundant -- safe to drop without weakening the
/// clause -- if it is already implied by the clause's other literals
/// together with the implication graph, checked recursively via
/// [`literal_redundant`]. The asserting literal (`learned[0]`) is
/// never a candidate: it is the first-UIP itself, not a resolution
/// byproduct, and a literal whose variable was a decision (no reason)
/// can never be redundant either, so `literal_redundant` is only ever
/// called for the rest.
///
/// `min_work` (reset here) counts total reason-clause literals
/// examined across every `literal_redundant` call this pass makes;
/// once it exceeds `budget` (see [`minimize_work_budget`]), every
/// remaining literal is kept unminimized rather than examined at all
/// -- a hard, whole-call cap, not a per-literal one, so one
/// pathological clause can never cost more than a bounded amount of
/// work regardless of how many literals it has left to check.
#[allow(clippy::too_many_arguments)]
fn minimize_clause(
    learned: Clause,
    clauses: &[Clause],
    level: &[usize],
    reason: &[Option<usize>],
    seen: &[bool],
    min_state: &mut [u8],
    min_touched: &mut Vec<usize>,
    min_stack: &mut Vec<MinimizeFrame>,
    min_work: &mut usize,
    work_budget_factor: usize,
) -> Clause {
    for &v in min_touched.iter() {
        min_state[v] = MIN_UNDEF;
    }
    min_touched.clear();
    *min_work = 0;

    let budget = minimize_work_budget(learned.len(), work_budget_factor);
    let mut kept = Vec::with_capacity(learned.len());
    kept.push(learned[0]);
    for &lit in &learned[1..] {
        let v = cnf::literal_var(lit);
        if *min_work > budget
            || reason[v].is_none()
            || !literal_redundant(
                lit,
                clauses,
                level,
                reason,
                seen,
                min_state,
                min_touched,
                min_stack,
                min_work,
                budget,
            )
        {
            kept.push(lit);
        }
    }
    kept
}

/// Reports whether `lit` -- which must have a reason clause (checked
/// by [`minimize_clause`] before calling) -- is redundant: every
/// literal that `lit`'s reason clause depends on (other than `lit`
/// itself) is either a permanent level-0 fact, already accounted for
/// by `analyze`'s own resolution (`seen`), already known redundant, or
/// itself (recursively) redundant by the same rule. If any dependency
/// is a decision variable not already covered by one of those (no
/// reason, not seen), `lit` is not redundant.
///
/// Implemented iteratively (an explicit stack of [`MinimizeFrame`],
/// not a recursive function per edge) to bound native call-stack depth
/// regardless of implication-chain length, and memoized via
/// `min_state` so no variable's reason clause is scanned more than
/// once per `minimize_clause` call: once a variable's redundancy is
/// settled (removable or failed), every later reference to it anywhere
/// in this pass is an O(1) lookup, not a re-scan. `budget` bounds
/// `*min_work` the same way across every call within one
/// `minimize_clause` pass, checked here too (not just between calls)
/// so a single literal's own redundancy chain can never itself blow
/// past it.
#[allow(clippy::too_many_arguments)]
fn literal_redundant(
    lit: Literal,
    clauses: &[Clause],
    level: &[usize],
    reason: &[Option<usize>],
    seen: &[bool],
    min_state: &mut [u8],
    min_touched: &mut Vec<usize>,
    min_stack: &mut Vec<MinimizeFrame>,
    min_work: &mut usize,
    budget: usize,
) -> bool {
    min_stack.clear();
    min_stack.push(MinimizeFrame { lit, idx: 0 });

    while !min_stack.is_empty() {
        if *min_work > budget {
            return false;
        }

        let i = min_stack.len() - 1;
        let frame_lit = min_stack[i].lit;
        let frame_idx = min_stack[i].idx;
        let reason_idx = reason[cnf::literal_var(frame_lit)]
            .expect("literal_redundant only ever pushes literals with a reason");
        let reason_clause = &clauses[reason_idx];

        if frame_idx >= reason_clause.len() {
            // Finished scanning this frame's reason clause without
            // finding anything that blocks redundancy: frame_lit
            // itself is removable.
            let v = cnf::literal_var(frame_lit);
            if min_state[v] == MIN_UNDEF {
                min_state[v] = MIN_REMOVABLE;
                min_touched.push(v);
            }
            min_stack.truncate(i);
            continue;
        }

        let l = reason_clause[frame_idx];
        min_stack[i].idx += 1;
        if cnf::literal_var(l) == cnf::literal_var(frame_lit) {
            continue; // skip the literal whose reason this is
        }

        *min_work += 1;
        let v = cnf::literal_var(l);
        if level[v] == 0 || seen[v] || min_state[v] == MIN_REMOVABLE {
            continue;
        }
        if reason[v].is_none() || min_state[v] == MIN_FAILED {
            // v is a decision (or already known unremovable) and isn't
            // otherwise accounted for: everything on the stack right
            // now -- lit and every literal recursion reached to get
            // here -- depends on v, so none of them is redundant.
            return false;
        }
        min_stack.push(MinimizeFrame { lit: l, idx: 0 });
    }
    true
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
///
/// STAGE36.md: before `backtrack_level`/the LBD are computed,
/// [`minimize_clause`] gets a chance to drop any literal from the
/// just-derived learned clause that turns out to be redundant (see its
/// own doc comment). This must happen here, before the conflict-
/// handling code's own `backtrack_to` runs -- once the search
/// backjumps, some of the now-unassigned variables' reason/level
/// bookkeeping `minimize_clause` depends on is gone -- and before
/// `backtrack_level`/the LBD are derived, since removing a literal can
/// only lower both (they are computed from whichever literals survive
/// minimization, not the ones `analyze` first derives).
///
/// STAGE34.md's third return value is the learned (and now possibly
/// minimized) clause's LBD -- the number of distinct decision levels
/// represented among its literals (see the module doc comment) --
/// computed in this same pass via `lbd_scratch` (reused scratch space,
/// like `seen`, cleared at the start of this function rather than
/// reallocated) since every literal that will end up in the learned
/// clause is already being visited here regardless.
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
    lbd_scratch: &mut Vec<usize>,
    min_state: &mut [u8],
    min_touched: &mut Vec<usize>,
    min_stack: &mut Vec<MinimizeFrame>,
    min_work: &mut usize,
    minimize_work_budget_factor: usize,
) -> (Clause, usize, usize) {
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
    let result = minimize_clause(
        result,
        clauses,
        level,
        reason,
        seen,
        min_state,
        min_touched,
        min_stack,
        min_work,
        minimize_work_budget_factor,
    );

    let mut backtrack_level = 0;
    lbd_scratch.clear();
    lbd_scratch.push(current_level);
    for &lit in &result[1..] {
        let lv = level[cnf::literal_var(lit)];
        if lv > backtrack_level {
            backtrack_level = lv;
        }
        if !lbd_scratch.contains(&lv) {
            lbd_scratch.push(lv);
        }
    }
    (result, backtrack_level, lbd_scratch.len())
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

/// Returns the polarity [`decision_literal`] should guess for `v`:
/// `target_phase[v]` under [`PhaseStrategy::Target`], `saved_phase[v]`
/// otherwise -- which covers both [`PhaseStrategy::Saving`] and
/// [`PhaseStrategy::RephaseWalkSAT`] (the latter only changes how
/// `saved_phase` gets updated, in [`maybe_rephase`], not which array
/// this reads).
fn phase_for(
    v: usize,
    phase_strategy: PhaseStrategy,
    saved_phase: &[Value],
    target_phase: &[Value],
) -> Value {
    if phase_strategy == PhaseStrategy::Target {
        target_phase[v]
    } else {
        saved_phase[v]
    }
}

/// Returns the literal [`run`]'s decision step should assign when
/// branching on variable `v` (STAGE14.md/STAGE43.md): its phase (see
/// [`phase_for`]), i.e. whatever polarity that phase source held for
/// it, or `False` if it has never been recorded there (a variable
/// that has never been assigned before), matching the fixed order
/// earlier stages always used.
fn decision_literal(
    v: usize,
    phase_strategy: PhaseStrategy,
    saved_phase: &[Value],
    target_phase: &[Value],
) -> Literal {
    if phase_for(v, phase_strategy, saved_phase, target_phase) == Value::True {
        v as Literal
    } else {
        -(v as Literal)
    }
}

/// [`PhaseStrategy::Target`]'s own bookkeeping (STAGE43.md), called
/// once per main-loop iteration whenever propagation has just settled
/// without conflict ([`run_loop`]) -- the same point a decision is
/// about to be made, i.e. exactly when the trail's current length is
/// meaningful to compare. Chanseok Oh's "target phase": whenever the
/// trail is longer than it has ever been before in this run (a new
/// record, not merely equal -- ties keep the earlier, already-recorded
/// snapshot, an arbitrary but harmless choice since both are equally
/// deep), snapshot every currently-assigned variable's polarity into
/// `target_phase`. Variables not yet assigned at the time of a given
/// snapshot simply keep whatever `target_phase` already held for them
/// -- the same "leave untouched, don't reset to `Unassigned`" merge
/// behavior `saved_phase`'s own update already relies on.
fn update_target_phase(
    trail: &[usize],
    x: &Assignment,
    target_phase: &mut [Value],
    best_trail_len: &mut usize,
) {
    if trail.len() <= *best_trail_len {
        return;
    }
    *best_trail_len = trail.len();
    for &v in trail {
        target_phase[v] = x[v];
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
    lrb_alpha: f64,
) {
    let cut = trail_lim[level_target + 1];
    for &v in &trail[cut..] {
        if variant == SelectVarVariant::Lrb {
            let interval = num_conflicts - lrb.assigned_at_conflict[v];
            if interval > 0 {
                let r = lrb.participated[v] as f64 / interval as f64;
                lrb.q[v] = (1.0 - lrb_alpha) * lrb.q[v] + lrb_alpha * r;
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
/// was stored. `activity` gets a fresh `0.0` entry, `clause_lbd` gets
/// `lbd` (STAGE34.md), and `estimated_bytes` is increased by
/// [`clause_byte_cost`] for the new clause (STAGE12.md), all kept
/// parallel to `clauses`/`watch`.
#[allow(clippy::too_many_arguments)]
fn add_learned_clause(
    learned: &Clause,
    lbd: usize,
    clauses: &mut Vec<Clause>,
    lists: &mut Lists,
    watchers_positive: &mut [Vec<usize>],
    watchers_negative: &mut [Vec<usize>],
    watch: &mut Vec<[Literal; 2]>,
    level: &[usize],
    clause_activity: &mut Vec<f64>,
    clause_lbd: &mut Vec<usize>,
    estimated_bytes: &mut i64,
) -> Option<usize> {
    if learned.len() == 1 {
        return None;
    }

    let idx = clauses.len();
    clauses.push(learned.clone());
    clause_activity.push(0.0);
    clause_lbd.push(lbd);
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
    append_watcher(watchers_positive, watchers_negative, learned[0], idx);
    append_watcher(watchers_positive, watchers_negative, learned[best], idx);
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
///
/// STAGE34.md: a clause is also excluded from `eligible` -- "glue
/// clause" protection, on top of the pre-existing `locked` exclusion
/// -- if its LBD is at or below `GLUE_CLAUSE_LBD_THRESHOLD`,
/// regardless of how low its activity has fallen, since a low LBD is
/// itself strong independent evidence the clause is worth keeping
/// even if it hasn't happened to participate in a conflict recently.
/// Among the clauses that remain eligible, the sort is now LBD
/// ascending first (higher LBD = less "compact" = deleted first) with
/// activity descending only as a tiebreak among equal-LBD clauses,
/// rather than pure activity as before.
#[allow(clippy::too_many_arguments)]
fn reduce_clause_database(
    clauses: &mut Vec<Clause>,
    lists: &mut Lists,
    watchers_positive: &mut Vec<Vec<usize>>,
    watchers_negative: &mut Vec<Vec<usize>>,
    watch: &mut Vec<[Literal; 2]>,
    clause_activity: &mut Vec<f64>,
    clause_lbd: &mut Vec<usize>,
    estimated_bytes: &mut i64,
    x: &Assignment,
    reason: &mut [Option<usize>],
    num_original_clauses: usize,
    glue_clause_lbd_threshold: usize,
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
        .filter(|&idx| !locked[idx] && clause_lbd[idx] > glue_clause_lbd_threshold)
        .collect();
    eligible.sort_by(|&a, &b| match clause_lbd[b].cmp(&clause_lbd[a]) {
        std::cmp::Ordering::Equal => clause_activity[a].partial_cmp(&clause_activity[b]).unwrap(),
        other => other,
    });

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
    let mut new_lbd = Vec::with_capacity(clauses.len() - num_to_delete);
    for (idx, clause) in clauses.iter().enumerate() {
        if to_delete[idx] {
            continue;
        }
        old_to_new[idx] = Some(new_clauses.len());
        new_clauses.push(clause.clone());
        new_watch.push(watch[idx]);
        new_activity.push(clause_activity[idx]);
        new_lbd.push(clause_lbd[idx]);
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
    // STAGE45.md: watchers_positive/watchers_negative index by clause
    // index too, exactly like watch itself, so they need the same
    // wholesale rebuild-from-new_watch treatment as everything else
    // above -- patching them in place would mean walking old_to_new
    // for every entry in every watcher list, no cheaper than just
    // rebuilding from watch directly, and far more error-prone. Fine
    // either way: reduce_clause_database only runs when the (optional)
    // memory limit is actually exceeded, not on every conflict, so
    // this O(current clauses) rebuild is not the hot path.
    let (new_watchers_positive, new_watchers_negative) = build_watchers(watch, x.len() - 1);
    *watchers_positive = new_watchers_positive;
    *watchers_negative = new_watchers_negative;
    *clause_activity = new_activity;
    *clause_lbd = new_lbd;
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

/// Returns whether `literal` currently evaluates to true under
/// `assignment` (an `Unassigned` variable makes every literal on it
/// neither true nor false yet, so this returns false for those).
/// STAGE48.md's blocking-literal check (`propagate`) is this
/// function's only caller.
fn is_true(literal: Literal, assignment: &Assignment) -> bool {
    let value = assignment[cnf::literal_var(literal)];
    if value == Value::Unassigned {
        return false;
    }
    if cnf::literal_is_negative(literal) {
        value == Value::False
    } else {
        value == Value::True
    }
}

/// Formats the configured time limit and `SelectVar` variant for the
/// "cdcl:" announcement printed at verbose level 1.
fn describe_params(
    time_limit: Option<Duration>,
    variant: SelectVarVariant,
    restart_strategy: RestartStrategy,
    phase_strategy: PhaseStrategy,
) -> String {
    let variant_code = match variant {
        SelectVarVariant::Weighted => 0,
        SelectVarVariant::Fast => 1,
        SelectVarVariant::Vsids => 2,
        SelectVarVariant::Lrb => 3,
    };
    // STAGE44.md swaps which numeral is which (Glucose 5 -> 4,
    // RoundRobin 4 -> 5) to match cliargs' own --alg-params mapping.
    let restart_code = match restart_strategy {
        RestartStrategy::None => 0,
        RestartStrategy::Luby => 1,
        RestartStrategy::Polynomial => 2,
        RestartStrategy::Geometric => 3,
        RestartStrategy::Glucose => 4,
        RestartStrategy::RoundRobin => 5,
    };
    let phase_code = match phase_strategy {
        PhaseStrategy::Saving => 0,
        PhaseStrategy::Target => 1,
        PhaseStrategy::RephaseWalkSAT => 2,
        PhaseStrategy::RoundRobin => 3,
    };
    match time_limit {
        Some(limit) => format!(
            "select_var={variant_code} restart={restart_code} phase={phase_code} time_limit_secs={}",
            limit.as_secs()
        ),
        None => format!("select_var={variant_code} restart={restart_code} phase={phase_code}"),
    }
}

/// [`describe_params`], extended with the worker count, for
/// [`run_parallel`]'s "cdcl: ..." announcement (STAGE20.md/
/// STAGE21.md), matching `dfs`'s identical
/// `describe_parallel_params`.
fn describe_parallel_params(
    time_limit: Option<Duration>,
    variant: SelectVarVariant,
    restart_strategy: RestartStrategy,
    phase_strategy: PhaseStrategy,
    num_threads: usize,
) -> String {
    format!(
        "num_threads={num_threads} {}",
        describe_params(time_limit, variant, restart_strategy, phase_strategy)
    )
}

/// Formats an optional STAGE12.md memory limit for the "cdcl:"
/// announcement printed at verbose level 1, in bytes.
fn describe_memory_limit(memory_limit_bytes: Option<i64>) -> String {
    match memory_limit_bytes {
        Some(limit) => format!(" memory_limit_bytes={limit}"),
        None => String::new(),
    }
}

/// Formats a [`SolveResult`]'s outcome for the "SAT"/"UNSAT"/
/// "UNKNOWN" line [`run`]/[`run_parallel`] print at verbose >= 1.
fn verdict(result: &SolveResult) -> &'static str {
    if result.timed_out {
        "UNKNOWN"
    } else if result.satisfiable {
        "SAT"
    } else {
        "UNSAT"
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
        let (mut watchers_positive, mut watchers_negative) =
            build_watchers(&watch, working_problem.num_vars);
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
            &mut watchers_positive,
            &mut watchers_negative,
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
        let (mut watchers_positive, mut watchers_negative) =
            build_watchers(&watch, working_problem.num_vars);
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
            &mut watchers_positive,
            &mut watchers_negative,
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
        let mut lbd_scratch: Vec<usize> = Vec::new();
        let mut min_state = vec![MIN_UNDEF; 5];
        let mut min_touched: Vec<usize> = Vec::new();
        let mut min_stack: Vec<MinimizeFrame> = Vec::new();
        let mut min_work: usize = 0;

        let (learned, backtrack_level, lbd) = analyze(
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
            &mut lbd_scratch,
            &mut min_state,
            &mut min_touched,
            &mut min_stack,
            &mut min_work,
            params::default().cdcl.minimize_work_budget_factor,
        );

        assert_eq!(backtrack_level, 0);
        assert_eq!(learned, vec![-2]);
        // Both x2 and x4 (the conflicting clause's falsified literals)
        // sit at decision level 1, and the seed level (current_level,
        // also 1) coincides with them, so the distinct-level count is
        // exactly 1.
        assert_eq!(lbd, 1);
    }

    /// Verifies literal_redundant's simplest case: candidate literal
    /// -2's reason clause {1,2} has one other literal (var 1), and
    /// var 1 is already "seen" (accounted for by analyze's own
    /// resolution, simulated here by setting seen[1] directly) -- so
    /// -2 must be redundant.
    #[test]
    fn test_literal_redundant_direct_case() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1, 2]],
        };
        let (working_problem, _watch, _x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let mut level = vec![0usize; 3];
        let mut reason: Vec<Option<usize>> = vec![None; 3];
        let mut seen = vec![false; 3];
        let mut min_state = vec![MIN_UNDEF; 3];
        let mut min_touched: Vec<usize> = Vec::new();
        let mut min_stack: Vec<MinimizeFrame> = Vec::new();
        let mut min_work: usize = 0;

        level[1] = 1;
        level[2] = 1;
        reason[1] = None;
        reason[2] = Some(0);
        seen[1] = true;

        assert!(
            literal_redundant(
                -2,
                &working_problem.clauses,
                &level,
                &reason,
                &seen,
                &mut min_state,
                &mut min_touched,
                &mut min_stack,
                &mut min_work,
                1000,
            ),
            "expected -2 to be redundant: its reason {{1,2}}'s only other literal (var 1) is already seen"
        );
    }

    /// Verifies the "recursive" half of STAGE36.md's request: candidate
    /// -2's reason {1,2} references var 1, which is NOT itself seen,
    /// but var 1's own reason {3,1} references var 3, which is seen --
    /// this only succeeds if literal_redundant recurses into var 1's
    /// reason rather than stopping at the first level.
    #[test]
    fn test_literal_redundant_recursive_case() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![1, 2], vec![3, 1]],
        };
        let (working_problem, _watch, _x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let mut level = vec![0usize; 4];
        let mut reason: Vec<Option<usize>> = vec![None; 4];
        let mut seen = vec![false; 4];
        let mut min_state = vec![MIN_UNDEF; 4];
        let mut min_touched: Vec<usize> = Vec::new();
        let mut min_stack: Vec<MinimizeFrame> = Vec::new();
        let mut min_work: usize = 0;

        level[1] = 1;
        level[2] = 1;
        level[3] = 1;
        reason[1] = Some(1); // clause {3,1}
        reason[2] = Some(0); // clause {1,2}
        reason[3] = None;
        seen[3] = true;

        assert!(
            literal_redundant(
                -2,
                &working_problem.clauses,
                &level,
                &reason,
                &seen,
                &mut min_state,
                &mut min_touched,
                &mut min_stack,
                &mut min_work,
                1000,
            ),
            "expected -2 to be redundant via recursion through var 1's own reason clause"
        );
    }

    /// Verifies that a candidate literal is NOT redundant when its
    /// reason clause depends on a decision variable (no reason) that
    /// isn't otherwise accounted for.
    #[test]
    fn test_literal_redundant_blocked_by_uncovered_decision() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1, 2]],
        };
        let (working_problem, _watch, _x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let mut level = vec![0usize; 3];
        let mut reason: Vec<Option<usize>> = vec![None; 3];
        let seen = vec![false; 3];
        let mut min_state = vec![MIN_UNDEF; 3];
        let mut min_touched: Vec<usize> = Vec::new();
        let mut min_stack: Vec<MinimizeFrame> = Vec::new();
        let mut min_work: usize = 0;

        level[1] = 1;
        level[2] = 1;
        reason[1] = None; // an uncovered decision: not seen, no reason
        reason[2] = Some(0);

        assert!(
            !literal_redundant(
                -2,
                &working_problem.clauses,
                &level,
                &reason,
                &seen,
                &mut min_state,
                &mut min_touched,
                &mut min_stack,
                &mut min_work,
                1000,
            ),
            "expected -2 NOT to be redundant: var 1 is an uncovered decision"
        );
    }

    /// STAGE36.md's own explicit "absolute time limit, just in case"
    /// concern, tested directly and deterministically: even a
    /// genuinely redundant literal (the exact same setup as
    /// test_literal_redundant_direct_case) must come back as NOT
    /// redundant -- the safe, conservative fallback -- once the budget
    /// is exhausted, rather than panicking or ignoring the budget
    /// entirely.
    #[test]
    fn test_literal_redundant_respects_zero_work_budget() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1, 2]],
        };
        let (working_problem, _watch, _x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let mut level = vec![0usize; 3];
        let mut reason: Vec<Option<usize>> = vec![None; 3];
        let mut seen = vec![false; 3];
        let mut min_state = vec![MIN_UNDEF; 3];
        let mut min_touched: Vec<usize> = Vec::new();
        let mut min_stack: Vec<MinimizeFrame> = Vec::new();
        let mut min_work: usize = 0;

        level[1] = 1;
        level[2] = 1;
        reason[1] = None;
        reason[2] = Some(0);
        seen[1] = true;

        assert!(
            !literal_redundant(
                -2,
                &working_problem.clauses,
                &level,
                &reason,
                &seen,
                &mut min_state,
                &mut min_touched,
                &mut min_stack,
                &mut min_work,
                0,
            ),
            "expected a zero work budget to force a conservative false, even though -2 is genuinely redundant"
        );
    }

    /// A full analyze()-level integration test (solver state set
    /// directly rather than derived via real propagate() calls, for
    /// full control over the implication graph's shape): a conflict
    /// clause {-2,-3,-4} whose first-UIP resolution naturally derives
    /// learned = [-2,-1,-5] (asserting literal -2, from resolving var
    /// 4 then var 3 down to var 2 as the UIP), where literal -1's
    /// reason is a decision (kept) and literal -5's reason {-1,5}
    /// references var 1 -- already seen, from resolving -1 into the
    /// clause along the way -- so -5 must be minimized away, leaving
    /// [-2,-1]. backtrack_level and lbd must reflect the clause
    /// literal_redundant leaves behind, not the one first-UIP first
    /// derives.
    #[test]
    fn test_analyze_minimizes_learned_clause() {
        let problem = Problem {
            num_vars: 5,
            clauses: vec![
                vec![-2, -3, -4], // 0: conflict
                vec![-1, 4],      // 1: reason for var 4
                vec![-5, 3],      // 2: reason for var 3
                vec![-1, 5],      // 3: reason for var 5
            ],
        };
        let (working_problem, _watch, mut x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let current_level = 2;
        let mut level = vec![0usize; 6];
        let mut reason: Vec<Option<usize>> = vec![None; 6];
        let mut seen = vec![false; 6];
        let mut vsids = VsidsState::new(5);
        let mut lrb = LrbState::new(5);
        let mut clause_activity = vec![0.0f64; working_problem.clauses.len()];
        let mut lbd_scratch: Vec<usize> = Vec::new();
        let mut min_state = vec![MIN_UNDEF; 6];
        let mut min_touched: Vec<usize> = Vec::new();
        let mut min_stack: Vec<MinimizeFrame> = Vec::new();
        let mut min_work: usize = 0;

        level[1] = 1;
        level[5] = 1;
        level[2] = 2;
        level[3] = 2;
        level[4] = 2;
        reason[1] = None;
        reason[5] = Some(3);
        reason[3] = Some(2);
        reason[4] = Some(1);
        for xv in x.iter_mut().skip(1) {
            *xv = Value::True;
        }
        let trail = vec![1, 5, 2, 3, 4];

        let (learned, backtrack_level, lbd) = analyze(
            0,
            &working_problem.clauses,
            &trail,
            &level,
            &reason,
            &x,
            &mut seen,
            current_level,
            &mut clause_activity,
            1.0,
            working_problem.clauses.len(),
            SelectVarVariant::Weighted,
            &mut vsids,
            &mut lrb,
            &mut lbd_scratch,
            &mut min_state,
            &mut min_touched,
            &mut min_stack,
            &mut min_work,
            params::default().cdcl.minimize_work_budget_factor,
        );

        assert_eq!(
            learned,
            vec![-2, -1],
            "literal -5 should have been minimized away"
        );
        assert_eq!(
            backtrack_level, 1,
            "want the level of the only surviving non-asserting literal, -1"
        );
        assert_eq!(
            lbd, 2,
            "want current_level 2 and level 1, after minimization"
        );
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
        let (mut watchers_positive, mut watchers_negative) =
            build_watchers(&watch, working_problem.num_vars);
        let level = vec![0usize; 3];
        let mut clause_activity = vec![0.0f64; working_problem.clauses.len()];
        let mut estimated_bytes = 0i64;
        let before = working_problem.clauses.len();

        let mut clause_lbd: Vec<usize> = Vec::new();
        let idx = add_learned_clause(
            &vec![-1],
            1,
            &mut working_problem.clauses,
            &mut lists,
            &mut watchers_positive,
            &mut watchers_negative,
            &mut watch,
            &level,
            &mut clause_activity,
            &mut clause_lbd,
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
        let (mut watchers_positive, mut watchers_negative) =
            build_watchers(&watch, working_problem.num_vars);
        let mut level = vec![0usize; 4];
        level[2] = 1;
        level[3] = 3; // higher than variable 2's level
        let mut clause_activity = vec![0.0f64; working_problem.clauses.len()];
        let mut estimated_bytes = 0i64;

        let mut clause_lbd: Vec<usize> = Vec::new();
        let idx = add_learned_clause(
            &vec![-1, 2, 3],
            3,
            &mut working_problem.clauses,
            &mut lists,
            &mut watchers_positive,
            &mut watchers_negative,
            &mut watch,
            &level,
            &mut clause_activity,
            &mut clause_lbd,
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
                &params::default().cdcl,
                PhaseStrategy::Saving,
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
                &params::default().cdcl,
                PhaseStrategy::Saving,
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
        let (mut watchers_positive, mut watchers_negative) =
            build_watchers(&watch, working_problem.num_vars);
        let level = vec![0usize; 6];
        let num_original_clauses = working_problem.clauses.len();
        let mut clause_activity: Vec<f64> = vec![0.0; num_original_clauses];
        let mut clause_lbd: Vec<usize> = vec![0; num_original_clauses];
        let mut estimated_bytes: i64 = 0;
        let mut reason: Vec<Option<usize>> = vec![None; 6];

        // LBD 3 (above GLUE_CLAUSE_LBD_THRESHOLD) on all three, so this
        // test exercises the activity tiebreak among equal-LBD clauses,
        // not the glue-clause protection (see
        // test_reduce_clause_database_protects_glue_clauses).
        let idx_low = add_learned_clause(
            &vec![-1, 3],
            3,
            &mut working_problem.clauses,
            &mut lists,
            &mut watchers_positive,
            &mut watchers_negative,
            &mut watch,
            &level,
            &mut clause_activity,
            &mut clause_lbd,
            &mut estimated_bytes,
        )
        .expect("expected a stored clause");
        let idx_locked = add_learned_clause(
            &vec![-2, 4],
            3,
            &mut working_problem.clauses,
            &mut lists,
            &mut watchers_positive,
            &mut watchers_negative,
            &mut watch,
            &level,
            &mut clause_activity,
            &mut clause_lbd,
            &mut estimated_bytes,
        )
        .expect("expected a stored clause");
        let idx_high = add_learned_clause(
            &vec![-3, 5],
            3,
            &mut working_problem.clauses,
            &mut lists,
            &mut watchers_positive,
            &mut watchers_negative,
            &mut watch,
            &level,
            &mut clause_activity,
            &mut clause_lbd,
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
            &mut watchers_positive,
            &mut watchers_negative,
            &mut watch,
            &mut clause_activity,
            &mut clause_lbd,
            &mut estimated_bytes,
            &x,
            &mut reason,
            num_original_clauses,
            params::default().cdcl.glue_clause_lbd_threshold,
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
        let (mut watchers_positive, mut watchers_negative) =
            build_watchers(&watch, working_problem.num_vars);
        let level = vec![0usize; 3];
        let num_original_clauses = working_problem.clauses.len();
        let mut clause_activity: Vec<f64> = vec![0.0; num_original_clauses];
        let mut clause_lbd: Vec<usize> = vec![0; num_original_clauses];
        let mut estimated_bytes: i64 = 0;
        let mut reason: Vec<Option<usize>> = vec![None; 3];

        let idx = add_learned_clause(
            &vec![-1, 2],
            3,
            &mut working_problem.clauses,
            &mut lists,
            &mut watchers_positive,
            &mut watchers_negative,
            &mut watch,
            &level,
            &mut clause_activity,
            &mut clause_lbd,
            &mut estimated_bytes,
        )
        .expect("expected a stored clause");
        x[2] = Value::True;
        reason[2] = Some(idx);

        let before = working_problem.clauses.len();
        reduce_clause_database(
            &mut working_problem.clauses,
            &mut lists,
            &mut watchers_positive,
            &mut watchers_negative,
            &mut watch,
            &mut clause_activity,
            &mut clause_lbd,
            &mut estimated_bytes,
            &x,
            &mut reason,
            num_original_clauses,
            params::default().cdcl.glue_clause_lbd_threshold,
        );

        assert_eq!(working_problem.clauses.len(), before);
    }

    /// STAGE34.md: verifies glue-clause protection -- a clause at
    /// GLUE_CLAUSE_LBD_THRESHOLD, given the lowest possible activity
    /// (0.0, the single most "deletable" value under the old
    /// pure-activity policy), must still survive, while the correct
    /// clause -- the lower-activity one among the remaining,
    /// non-glue-eligible clauses -- is deleted instead.
    #[test]
    fn test_reduce_clause_database_protects_glue_clauses() {
        let problem = Problem {
            num_vars: 6,
            clauses: vec![vec![1, 2]],
        };
        let (mut working_problem, mut watch, x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let mut lists = occurrence::build(&working_problem);
        let (mut watchers_positive, mut watchers_negative) =
            build_watchers(&watch, working_problem.num_vars);
        let level = vec![0usize; 7];
        let num_original_clauses = working_problem.clauses.len();
        let mut clause_activity: Vec<f64> = vec![0.0; num_original_clauses];
        let mut clause_lbd: Vec<usize> = vec![0; num_original_clauses];
        let mut estimated_bytes: i64 = 0;
        let mut reason: Vec<Option<usize>> = vec![None; 7];
        let glue_clause_lbd_threshold = params::default().cdcl.glue_clause_lbd_threshold;

        let idx_glue = add_learned_clause(
            &vec![-1, 3],
            glue_clause_lbd_threshold,
            &mut working_problem.clauses,
            &mut lists,
            &mut watchers_positive,
            &mut watchers_negative,
            &mut watch,
            &level,
            &mut clause_activity,
            &mut clause_lbd,
            &mut estimated_bytes,
        )
        .expect("expected a stored clause");
        let idx_non_glue_low = add_learned_clause(
            &vec![-2, 4],
            glue_clause_lbd_threshold + 1,
            &mut working_problem.clauses,
            &mut lists,
            &mut watchers_positive,
            &mut watchers_negative,
            &mut watch,
            &level,
            &mut clause_activity,
            &mut clause_lbd,
            &mut estimated_bytes,
        )
        .expect("expected a stored clause");
        let idx_non_glue_high = add_learned_clause(
            &vec![-5, 6],
            glue_clause_lbd_threshold + 1,
            &mut working_problem.clauses,
            &mut lists,
            &mut watchers_positive,
            &mut watchers_negative,
            &mut watch,
            &level,
            &mut clause_activity,
            &mut clause_lbd,
            &mut estimated_bytes,
        )
        .expect("expected a stored clause");
        clause_activity[idx_glue] = 0.0;
        clause_activity[idx_non_glue_low] = 0.5;
        clause_activity[idx_non_glue_high] = 100.0;

        let before = working_problem.clauses.len();
        reduce_clause_database(
            &mut working_problem.clauses,
            &mut lists,
            &mut watchers_positive,
            &mut watchers_negative,
            &mut watch,
            &mut clause_activity,
            &mut clause_lbd,
            &mut estimated_bytes,
            &x,
            &mut reason,
            num_original_clauses,
            params::default().cdcl.glue_clause_lbd_threshold,
        );

        assert_eq!(working_problem.clauses.len(), before - 1);
        assert!(
            working_problem.clauses.contains(&vec![-1, 3]),
            "glue clause {{-1,3}} should have survived despite its rock-bottom activity"
        );
        assert!(
            !working_problem.clauses.contains(&vec![-2, 4]),
            "non-glue clause {{-2,4}} (lower activity among the non-glue clauses) should have been deleted"
        );
        assert!(
            working_problem.clauses.contains(&vec![-5, 6]),
            "non-glue clause {{-5,6}} (higher activity) should have survived"
        );
    }

    /// STAGE34.md: verifies that, among eligible clauses, a higher LBD
    /// is deleted before a lower one even when the higher-LBD clause
    /// has much higher activity -- LBD is the primary sort key,
    /// activity only a tiebreak among equal LBDs.
    #[test]
    fn test_reduce_clause_database_sorts_by_lbd_before_activity() {
        let problem = Problem {
            num_vars: 4,
            clauses: vec![vec![1, 2]],
        };
        let (mut working_problem, mut watch, x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let mut lists = occurrence::build(&working_problem);
        let (mut watchers_positive, mut watchers_negative) =
            build_watchers(&watch, working_problem.num_vars);
        let level = vec![0usize; 5];
        let num_original_clauses = working_problem.clauses.len();
        let mut clause_activity: Vec<f64> = vec![0.0; num_original_clauses];
        let mut clause_lbd: Vec<usize> = vec![0; num_original_clauses];
        let mut estimated_bytes: i64 = 0;
        let mut reason: Vec<Option<usize>> = vec![None; 5];

        let idx_high_lbd_high_activity = add_learned_clause(
            &vec![-1, 3],
            10,
            &mut working_problem.clauses,
            &mut lists,
            &mut watchers_positive,
            &mut watchers_negative,
            &mut watch,
            &level,
            &mut clause_activity,
            &mut clause_lbd,
            &mut estimated_bytes,
        )
        .expect("expected a stored clause");
        let idx_low_lbd_low_activity = add_learned_clause(
            &vec![-2, 4],
            3,
            &mut working_problem.clauses,
            &mut lists,
            &mut watchers_positive,
            &mut watchers_negative,
            &mut watch,
            &level,
            &mut clause_activity,
            &mut clause_lbd,
            &mut estimated_bytes,
        )
        .expect("expected a stored clause");
        clause_activity[idx_high_lbd_high_activity] = 1000.0;
        clause_activity[idx_low_lbd_low_activity] = 0.0;

        let before = working_problem.clauses.len();
        reduce_clause_database(
            &mut working_problem.clauses,
            &mut lists,
            &mut watchers_positive,
            &mut watchers_negative,
            &mut watch,
            &mut clause_activity,
            &mut clause_lbd,
            &mut estimated_bytes,
            &x,
            &mut reason,
            num_original_clauses,
            params::default().cdcl.glue_clause_lbd_threshold,
        );

        assert_eq!(working_problem.clauses.len(), before - 1);
        assert!(
            !working_problem.clauses.contains(&vec![-1, 3]),
            "the high-LBD clause {{-1,3}} should have been deleted despite its high activity"
        );
        assert!(
            working_problem.clauses.contains(&vec![-2, 4]),
            "the low-LBD clause {{-2,4}} should have survived despite its low activity"
        );
    }

    /// STAGE34.md: exercises glucose_should_restart directly with a
    /// fabricated sequence of LBDs: it must not trigger before the
    /// recent window is full, and its trigger decision afterward must
    /// match a hand-computed recent_avg*GLUCOSE_K >= global_avg
    /// comparison.
    #[test]
    fn test_glucose_should_restart() {
        let p = params::default().cdcl;
        let glucose_window_size = p.glucose_window_size;
        let mut glucose = GlucoseState::new(glucose_window_size);

        // Fill all but one slot of the recent window with LBD 2
        // (low/good); the window isn't full yet, so this must never
        // trigger regardless of how bad a single additional LBD looks.
        for i in 0..glucose_window_size - 1 {
            record_lbd(&mut glucose, 2);
            assert!(
                !glucose_should_restart(&glucose, p.glucose_k),
                "glucose_should_restart() = true before the recent window (size {glucose_window_size}) is full (i={i})"
            );
        }

        // One more low-LBD conflict fills the window at a recent
        // average of 2, equal to the global average (also 2) --
        // recent_avg*glucose_k (2*0.8=1.6) is well below global_avg
        // (2), so no restart yet.
        record_lbd(&mut glucose, 2);
        assert!(
            !glucose_should_restart(&glucose, p.glucose_k),
            "glucose_should_restart() = true with recent and global averages both at their best (LBD 2)"
        );

        // Now drive the recent window to a much worse (higher) LBD
        // than the global history: after glucose_window_size more
        // conflicts at LBD 20, the recent window average is 20 (all
        // glucose_window_size slots hold 20), while the global average
        // is dragged only partway up, so recent_avg*glucose_k must
        // exceed it and a restart must be signaled.
        for _ in 0..glucose_window_size {
            record_lbd(&mut glucose, 20);
        }
        let recent_avg = glucose.recent_sum as f64 / glucose_window_size as f64;
        let global_avg = glucose.global_sum as f64 / glucose.global_count as f64;
        let want = recent_avg * p.glucose_k >= global_avg;
        assert!(
            want,
            "test setup error: expected the LBD-20 run to make recent_avg*glucose_k >= global_avg true"
        );
        assert_eq!(
            glucose_should_restart(&glucose, p.glucose_k),
            want,
            "recent_avg={recent_avg} global_avg={global_avg}"
        );
    }

    /// Regression test for a bug this stage's own benchcompare sweep
    /// found (STAGE34.md): a restart signaled immediately after a
    /// conflict resolves to a level-0 learned unit clause --
    /// `current_level` is already 0 by then -- used to panic inside
    /// `backtrack_to(0, ...)`, which indexes `trail_lim[1]`, an index
    /// that only exists when `current_level > 0`.
    /// `RestartStrategy::Glucose`'s much higher restart frequency
    /// (roughly every `GLUCOSE_WINDOW_SIZE` conflicts, versus the
    /// fixed strategies' bases in the hundreds to tens of thousands)
    /// made this reachable on real benchmark files
    /// (`benchmark/blocksworld/bw_large.{c,d}.cnf`); `maybe_restart`
    /// now returns immediately whenever `*current_level == 0`,
    /// regardless of strategy, since there is nothing to restart from
    /// the root anyway.
    #[test]
    fn test_maybe_restart_does_not_panic_at_root_level() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1, 2]],
        };
        let (_working_problem, _watch, mut x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let mut trail: Vec<usize> = Vec::new();
        let mut trail_lim: Vec<usize> = vec![0];
        let mut current_level = 0usize; // matches the state right after a level-0 learned unit clause
        let mut q_head = 0usize;
        let mut lrb = LrbState::new(2);
        let mut saved_phase = vec![Value::False; 3];
        let mut conflicts_since_restart = 0usize;
        let mut restart_count = 0usize;

        // Force glucose_should_restart to report true, so maybe_restart
        // actually attempts a restart rather than skipping for an
        // unrelated reason: establish a low-LBD baseline global
        // average, then drive the recent window to a much worse LBD
        // (mirroring test_glucose_should_restart's own setup).
        let p = params::default().cdcl;
        let mut glucose = GlucoseState::new(p.glucose_window_size);
        for _ in 0..p.glucose_window_size {
            record_lbd(&mut glucose, 2);
        }
        for _ in 0..p.glucose_window_size {
            record_lbd(&mut glucose, 20);
        }
        assert!(
            glucose_should_restart(&glucose, p.glucose_k),
            "test setup error: expected glucose_should_restart() = true"
        );

        let lists = occurrence::build(&_working_problem);
        let mut rng = StdRng::seed_from_u64(1);
        maybe_restart(
            // must not panic
            RestartStrategy::Glucose,
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
            &glucose,
            &p,
            PhaseStrategy::Saving,
            &_working_problem,
            &lists,
            &mut rng,
        );

        assert_eq!(restart_count, 0, "no restart actually happens at the root");
        assert_eq!(conflicts_since_restart, 0);
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
            &params::default().cdcl,
            PhaseStrategy::Saving,
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
            &params::default().cdcl,
            PhaseStrategy::Saving,
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
        let (mut watchers_positive, mut watchers_negative) =
            build_watchers(&watch, working_problem.num_vars);
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
            &mut watchers_positive,
            &mut watchers_negative,
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
        let mut lbd_scratch: Vec<usize> = Vec::new();
        let mut min_state = vec![MIN_UNDEF; 5];
        let mut min_touched: Vec<usize> = Vec::new();
        let mut min_stack: Vec<MinimizeFrame> = Vec::new();
        let mut min_work: usize = 0;
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
            &mut lbd_scratch,
            &mut min_state,
            &mut min_touched,
            &mut min_stack,
            &mut min_work,
            params::default().cdcl.minimize_work_budget_factor,
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
            params::default().cdcl.lrb_alpha,
        );

        let want_q = params::default().cdcl.lrb_alpha * (3.0 / 5.0);
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
            params::default().cdcl.lrb_alpha,
        );

        assert_eq!(saved_phase[1], Value::True);
    }

    /// Verifies that decision_literal consults saved_phase rather
    /// than always guessing False.
    #[test]
    fn test_decision_literal_uses_saved_phase() {
        let saved_phase = vec![Value::False, Value::True];
        let target_phase = vec![Value::False, Value::False];
        assert_eq!(
            decision_literal(1, PhaseStrategy::Saving, &saved_phase, &target_phase),
            1
        );
    }

    /// Verifies the fallback: a variable that has never been assigned
    /// before (saved_phase still its default) is guessed False.
    #[test]
    fn test_decision_literal_defaults_to_false() {
        let saved_phase = vec![Value::False, Value::False];
        let target_phase = vec![Value::False, Value::False];
        assert_eq!(
            decision_literal(1, PhaseStrategy::Saving, &saved_phase, &target_phase),
            -1
        );
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
            &params::default().cdcl,
            PhaseStrategy::Saving,
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
            &params::default().cdcl,
            PhaseStrategy::Saving,
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
        let p = params::default().cdcl;
        for (k, term) in [1, 1, 2, 1, 1, 2, 4].into_iter().enumerate() {
            let want = p.luby_base_conflicts * term;
            assert_eq!(restart_threshold(RestartStrategy::Luby, k, &p), want);
        }
    }

    /// Verifies restart_threshold's polynomial case directly against
    /// STAGE15.md's originally-specified sequence (there under the
    /// incorrect name "geometric"): threshold at restart index k
    /// (0-indexed) is POLYNOMIAL_BASE_CONFLICTS * (k+1)^2, matching
    /// a*1^2, a*2^2, a*3^2, ....
    #[test]
    fn test_restart_threshold_polynomial() {
        let p = params::default().cdcl;
        for k in 0..4 {
            let want = p.polynomial_base_conflicts * (k + 1) * (k + 1);
            assert_eq!(restart_threshold(RestartStrategy::Polynomial, k, &p), want);
        }
    }

    /// Verifies restart_threshold's true geometric case directly:
    /// threshold at restart index k (0-indexed) is
    /// GEOMETRIC_BASE_CONFLICTS * GEOMETRIC_GROWTH_FACTOR^k, a
    /// sequence with a constant ratio (GEOMETRIC_GROWTH_FACTOR)
    /// between consecutive terms, unlike the polynomial case above.
    #[test]
    fn test_restart_threshold_geometric() {
        let p = params::default().cdcl;
        for k in 0..4 {
            let want = (p.geometric_base_conflicts as f64
                * p.geometric_growth_factor.powi(k as i32)) as usize;
            assert_eq!(restart_threshold(RestartStrategy::Geometric, k, &p), want);
        }
        // Directly pin the first four terms against the known
        // constants (base 100, ratio 1.5), so a future change to the
        // constants themselves is caught by this test's own
        // formula-based check above, while this pins the actual
        // numbers STAGE15.md's default configuration produces today.
        for (k, want) in [100, 150, 225, 337].into_iter().enumerate() {
            assert_eq!(restart_threshold(RestartStrategy::Geometric, k, &p), want);
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
        let glucose = GlucoseState::new(params::default().cdcl.glucose_window_size);
        let lists = occurrence::build(&_working_problem);
        let mut rng = StdRng::seed_from_u64(1);

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
            &glucose,
            &params::default().cdcl,
            PhaseStrategy::Saving,
            &_working_problem,
            &lists,
            &mut rng,
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
        let (mut watchers_positive, mut watchers_negative) =
            build_watchers(&watch, working_problem.num_vars);
        let mut level = vec![0usize; 3];
        let mut clause_activity = vec![0.0f64; working_problem.clauses.len()];
        let mut clause_lbd: Vec<usize> = vec![0; working_problem.clauses.len()];
        let mut estimated_bytes = 0i64;
        let learned_idx = add_learned_clause(
            &vec![-1, 2],
            3,
            &mut working_problem.clauses,
            &mut lists,
            &mut watchers_positive,
            &mut watchers_negative,
            &mut watch,
            &level,
            &mut clause_activity,
            &mut clause_lbd,
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
        let glucose = GlucoseState::new(params::default().cdcl.glucose_window_size);

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

        let mut conflicts_since_restart =
            params::default().cdcl.luby_base_conflicts * luby_term(0) - 1;
        let mut restart_count = 0usize;
        let mut rng = StdRng::seed_from_u64(1);
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
            &glucose,
            &params::default().cdcl,
            PhaseStrategy::Saving,
            &working_problem,
            &lists,
            &mut rng,
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
            &glucose,
            &params::default().cdcl,
            PhaseStrategy::Saving,
            &working_problem,
            &lists,
            &mut rng,
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
                &params::default().cdcl,
                PhaseStrategy::Saving,
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
                &params::default().cdcl,
                PhaseStrategy::Saving,
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
            &params::default().cdcl,
            PhaseStrategy::Saving,
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
            &params::default().cdcl,
            PhaseStrategy::Saving,
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
            &params::default().cdcl,
            PhaseStrategy::Saving,
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
            &params::default().cdcl,
            PhaseStrategy::Saving,
        );

        assert!(
            result.timed_out,
            "expected timed_out = true with a 1ns time limit"
        );
        assert!(!result.satisfiable);
    }

    /// Verifies STAGE20.md/STAGE21.md's compatibility guarantee:
    /// `run_parallel(..., 1, ...)` is bit-for-bit identical to calling
    /// `run` directly, matching Stage 17/18's identical guarantee for
    /// hillclimb/walksat/dfs.
    #[test]
    fn test_run_parallel_with_one_thread_matches_run() {
        let problem = pigeonhole_problem(4, 3);

        let mut rng1 = StdRng::seed_from_u64(7);
        let want = run(
            &problem,
            None,
            SelectVarVariant::Vsids,
            RestartStrategy::Polynomial,
            None,
            &mut rng1,
            0,
            &params::default().cdcl,
            PhaseStrategy::Saving,
        );

        let mut rng2 = StdRng::seed_from_u64(7);
        let got = run_parallel(
            &problem,
            None,
            SelectVarVariant::Vsids,
            RestartStrategy::Polynomial,
            None,
            1,
            &mut rng2,
            0,
            &params::default().cdcl,
            PhaseStrategy::Saving,
        );

        assert_eq!(got.satisfiable, want.satisfiable);
        assert_eq!(got.num_decisions, want.num_decisions);
        assert_eq!(got.num_conflicts, want.num_conflicts);
    }

    /// Verifies a satisfiable formula is solved correctly across
    /// several thread counts, and that the returned assignment
    /// genuinely satisfies every clause.
    #[test]
    fn test_run_parallel_finds_satisfiable_formula() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![1, 2], vec![-1, 3], vec![-2, -3]],
        };

        for num_threads in [2, 4, 8, 32] {
            let mut rng = StdRng::seed_from_u64(num_threads as u64);
            let result = run_parallel(
                &problem,
                None,
                SelectVarVariant::Vsids,
                RestartStrategy::RoundRobin,
                None,
                num_threads,
                &mut rng,
                0,
                &params::default().cdcl,
                PhaseStrategy::Saving,
            );
            assert!(
                result.satisfiable,
                "num_threads={num_threads}: expected satisfiable"
            );
            assert!(!result.timed_out, "num_threads={num_threads}");
            for (ci, clause) in problem.clauses.iter().enumerate() {
                let satisfied = clause.iter().any(|&lit| {
                    let v = cnf::literal_var(lit);
                    let value = result.assignment[v];
                    if cnf::literal_is_negative(lit) {
                        value == Value::False
                    } else {
                        value == Value::True
                    }
                });
                assert!(
                    satisfied,
                    "num_threads={num_threads}: clause {ci} ({clause:?}) not satisfied by {:?}",
                    result.assignment
                );
            }
        }
    }

    /// Verifies an unsatisfiable formula is correctly proven UNSAT
    /// across several thread counts -- unlike dfs's parallel search,
    /// every worker here covers the *whole* problem, so any single
    /// worker reaching UNSAT is already authoritative.
    #[test]
    fn test_run_parallel_proves_unsatisfiable_pigeonhole() {
        let problem = pigeonhole_problem(4, 3);

        for num_threads in [2, 4, 8, 16] {
            let mut rng = StdRng::seed_from_u64(9 + num_threads as u64);
            let result = run_parallel(
                &problem,
                None,
                SelectVarVariant::Vsids,
                RestartStrategy::RoundRobin,
                None,
                num_threads,
                &mut rng,
                0,
                &params::default().cdcl,
                PhaseStrategy::Saving,
            );
            assert!(
                !result.satisfiable,
                "num_threads={num_threads}: expected unsatisfiable"
            );
            assert!(!result.timed_out, "num_threads={num_threads}");
        }
    }

    /// Exercises a harder instance (6 pigeons, 5 holes) at a large
    /// thread count, matching Stage 18's equivalent "does this
    /// actually scale" check.
    #[test]
    fn test_run_parallel_proves_unsatisfiable_larger_pigeonhole() {
        let problem = pigeonhole_problem(6, 5);
        let mut rng = StdRng::seed_from_u64(11);

        let result = run_parallel(
            &problem,
            None,
            SelectVarVariant::Vsids,
            RestartStrategy::RoundRobin,
            None,
            64,
            &mut rng,
            0,
            &params::default().cdcl,
            PhaseStrategy::Saving,
        );

        assert!(!result.satisfiable);
        assert!(!result.timed_out);
    }

    /// Verifies that a hard instance with a very short time limit
    /// reports `timed_out` rather than hanging or silently reporting
    /// an incorrect verdict.
    #[test]
    fn test_run_parallel_respects_time_limit() {
        let problem = pigeonhole_problem(9, 8);
        let mut rng = StdRng::seed_from_u64(3);
        let tiny = Duration::from_nanos(1);

        let result = run_parallel(
            &problem,
            Some(tiny),
            SelectVarVariant::Vsids,
            RestartStrategy::RoundRobin,
            None,
            8,
            &mut rng,
            0,
            &params::default().cdcl,
            PhaseStrategy::Saving,
        );

        assert!(result.timed_out);
        assert!(!result.satisfiable);
    }

    /// Runs many trials of a satisfiable formula at a high thread
    /// count and checks every result is a genuine, independently
    /// verified satisfying assignment -- i.e. the single-winner CAS
    /// protocol never lets a stale/aborted worker's result leak
    /// through as the final answer.
    #[test]
    fn test_run_parallel_reports_exactly_one_winner() {
        let problem = Problem {
            num_vars: 5,
            clauses: vec![
                vec![1, 2],
                vec![-1, 3],
                vec![-2, 4],
                vec![-3, 5],
                vec![-4, -5],
            ],
        };

        for trial in 0..20u64 {
            let mut rng = StdRng::seed_from_u64(trial);
            let result = run_parallel(
                &problem,
                None,
                SelectVarVariant::Vsids,
                RestartStrategy::RoundRobin,
                None,
                16,
                &mut rng,
                0,
                &params::default().cdcl,
                PhaseStrategy::Saving,
            );
            assert!(result.satisfiable, "trial {trial}: expected satisfiable");
            for (ci, clause) in problem.clauses.iter().enumerate() {
                let satisfied = clause.iter().any(|&lit| {
                    let v = cnf::literal_var(lit);
                    let value = result.assignment[v];
                    if cnf::literal_is_negative(lit) {
                        value == Value::False
                    } else {
                        value == Value::True
                    }
                });
                assert!(
                    satisfied,
                    "trial {trial}: clause {ci} ({clause:?}) not satisfied"
                );
            }
        }
    }

    /// Verifies the round-robin resolution order STAGE21.md specifies
    /// (STAGE44.md folded Glucose into the rotation): worker 0
    /// quadratic, worker 1 geometric, worker 2 Luby, worker 3 Glucose,
    /// then repeating.
    #[test]
    fn test_resolve_restart_strategy_round_robin() {
        let want = [
            RestartStrategy::Polynomial,
            RestartStrategy::Geometric,
            RestartStrategy::Luby,
            RestartStrategy::Glucose,
            RestartStrategy::Polynomial,
            RestartStrategy::Geometric,
            RestartStrategy::Luby,
        ];
        for (i, &w) in want.iter().enumerate() {
            assert_eq!(resolve_restart_strategy(RestartStrategy::RoundRobin, i), w);
        }
    }

    /// Verifies that any explicit (non-round-robin) restart strategy,
    /// including `RestartStrategy::None`, is returned unchanged
    /// regardless of thread index -- STAGE21.md: "If the user
    /// explicitly sets a different restart strategy, even 0, all
    /// threads will use that."
    #[test]
    fn test_resolve_restart_strategy_passes_explicit_value_through() {
        for strategy in [
            RestartStrategy::None,
            RestartStrategy::Luby,
            RestartStrategy::Polynomial,
            RestartStrategy::Geometric,
            RestartStrategy::Glucose,
        ] {
            for thread_index in [0, 1, 2, 5, 127] {
                assert_eq!(resolve_restart_strategy(strategy, thread_index), strategy);
            }
        }
    }

    /// Verifies the basic single-slot round-trip: a clause published
    /// at some index can be read back with its literals intact.
    #[test]
    fn test_export_buffer_publish_and_try_read() {
        let buf = clause_share::ExportBuffer::new(4);
        buf.publish(&vec![1, -2, 3]);

        let got = buf
            .try_read(0)
            .expect("expected Some immediately after publish");
        assert_eq!(got, vec![1, -2, 3]);
    }

    /// Verifies that reading a never-written slot reports `None`
    /// rather than a zero-value clause.
    #[test]
    fn test_export_buffer_try_read_fails_before_any_publish() {
        let buf = clause_share::ExportBuffer::new(4);
        assert_eq!(buf.try_read(0), None);
    }

    /// Verifies that `publish` copies the literal slice rather than
    /// aliasing the caller's, so a caller mutating its own slice
    /// afterward can't corrupt an already-published clause.
    #[test]
    fn test_export_buffer_publish_owns_its_data() {
        let buf = clause_share::ExportBuffer::new(4);
        let mut lits = vec![1, 2, 3];
        buf.publish(&lits);
        lits[0] = 99;

        let got = buf.try_read(0).expect("expected Some");
        assert_eq!(got[0], 1, "publish must not alias the caller's slice");
    }

    // STAGE43.md: phase-selection strategy tests, mirroring
    // go_src/internal/cdcl/phase_test.go's own coverage exactly.

    /// Verifies that PhaseStrategy::RoundRobin resolves to
    /// PHASE_ROUND_ROBIN_STRATEGIES[thread_index % 3], and every other
    /// strategy is returned unchanged, mirroring
    /// resolve_restart_strategy's own round-robin coverage.
    #[test]
    fn test_resolve_phase_strategy_round_robin() {
        let want = [
            PhaseStrategy::Saving,
            PhaseStrategy::Target,
            PhaseStrategy::RephaseWalkSAT,
            PhaseStrategy::Saving,
            PhaseStrategy::Target,
        ];
        for (i, &w) in want.iter().enumerate() {
            assert_eq!(resolve_phase_strategy(PhaseStrategy::RoundRobin, i), w);
        }
        for strategy in [
            PhaseStrategy::Saving,
            PhaseStrategy::Target,
            PhaseStrategy::RephaseWalkSAT,
        ] {
            assert_eq!(resolve_phase_strategy(strategy, 7), strategy);
        }
    }

    /// Verifies that phase_for consults saved_phase, not target_phase,
    /// for both PhaseStrategy::Saving and PhaseStrategy::RephaseWalkSAT
    /// (the latter only changes how saved_phase gets updated, not
    /// which array decision_literal reads -- see the module doc
    /// comment).
    #[test]
    fn test_phase_for_saving_reads_saved_phase() {
        for strategy in [PhaseStrategy::Saving, PhaseStrategy::RephaseWalkSAT] {
            let saved_phase = vec![Value::Unassigned, Value::True];
            let target_phase = vec![Value::Unassigned, Value::False];
            assert_eq!(
                phase_for(1, strategy, &saved_phase, &target_phase),
                Value::True
            );
        }
    }

    /// Verifies that phase_for consults target_phase, not saved_phase,
    /// under PhaseStrategy::Target.
    #[test]
    fn test_phase_for_target_reads_target_phase() {
        let saved_phase = vec![Value::Unassigned, Value::False];
        let target_phase = vec![Value::Unassigned, Value::True];
        assert_eq!(
            phase_for(1, PhaseStrategy::Target, &saved_phase, &target_phase),
            Value::True
        );
    }

    /// Verifies that update_target_phase snapshots every
    /// currently-trailed variable's polarity into target_phase, and
    /// advances best_trail_len, only when the trail is strictly longer
    /// than the previous record.
    #[test]
    fn test_update_target_phase_records_new_record() {
        let x: Assignment = vec![Value::Unassigned, Value::True, Value::False];
        let trail = vec![1, 2];
        let mut target_phase = vec![Value::False; 3];
        let mut best_trail_len = 0usize;
        update_target_phase(&trail, &x, &mut target_phase, &mut best_trail_len);
        assert_eq!(best_trail_len, 2);
        assert_eq!(target_phase[1], Value::True);
        assert_eq!(target_phase[2], Value::False);
    }

    /// Verifies that a trail no longer than the existing record leaves
    /// target_phase and best_trail_len untouched -- including the
    /// exact-tie case, per update_target_phase's own doc comment
    /// ("ties keep the earlier, already-recorded snapshot").
    #[test]
    fn test_update_target_phase_ignores_ties_and_shorter_trails() {
        let x: Assignment = vec![Value::Unassigned, Value::True];
        let trail = vec![1];
        let mut target_phase = vec![Value::Unassigned, Value::False];
        let mut best_trail_len = 1usize;
        update_target_phase(&trail, &x, &mut target_phase, &mut best_trail_len);
        assert_eq!(best_trail_len, 1, "must stay unchanged at 1");
        assert_eq!(
            target_phase[1],
            Value::False,
            "a tie must not overwrite target_phase"
        );
    }

    /// Verifies that maybe_rephase is a no-op for every strategy other
    /// than PhaseStrategy::RephaseWalkSAT, even with restart_count at
    /// an exact multiple of rephase_interval_restarts.
    #[test]
    fn test_maybe_rephase_only_fires_for_rephase_walksat() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1, 2]],
        };
        let lists = occurrence::build(&problem);
        for strategy in [PhaseStrategy::Saving, PhaseStrategy::Target] {
            let mut saved_phase = vec![Value::Unassigned, Value::True];
            let p = params::Cdcl {
                rephase_interval_restarts: 50,
                rephase_max_flips: 100,
                ..params::default().cdcl
            };
            let mut rng = StdRng::seed_from_u64(1);
            let solution = maybe_rephase(
                strategy,
                50,
                &p,
                &problem,
                &lists,
                &mut saved_phase,
                &mut rng,
            );
            assert!(solution.is_none(), "strategy={strategy:?}: want no-op");
            assert_eq!(
                saved_phase[1],
                Value::True,
                "strategy={strategy:?}: maybe_rephase changed saved_phase, want no-op"
            );
        }
    }

    /// Verifies that maybe_rephase only actually runs a WalkSAT burst
    /// when restart_count is a positive multiple of
    /// rephase_interval_restarts, using saved_phase mutation as the
    /// observable signal (a real burst always overwrites saved_phase
    /// wholesale -- see rephase_from_walksat).
    #[test]
    fn test_maybe_rephase_respects_interval() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1, 2]],
        };
        let lists = occurrence::build(&problem);
        let p = params::Cdcl {
            rephase_interval_restarts: 3,
            rephase_max_flips: 100,
            ..params::default().cdcl
        };
        let mut saved_phase = vec![Value::True, Value::True, Value::True];
        let mut rng = StdRng::seed_from_u64(1);

        let solution = maybe_rephase(
            PhaseStrategy::RephaseWalkSAT,
            2,
            &p,
            &problem,
            &lists,
            &mut saved_phase,
            &mut rng,
        );
        assert!(solution.is_none());
        assert_eq!(
            saved_phase,
            vec![Value::True, Value::True, Value::True],
            "must not run at restart_count=2 (not a multiple of 3)"
        );

        maybe_rephase(
            PhaseStrategy::RephaseWalkSAT,
            3,
            &p,
            &problem,
            &lists,
            &mut saved_phase,
            &mut rng,
        );
        // A real burst ran; every entry ended up True or False, never
        // Unassigned (WalkSAT always produces a complete assignment)
        // -- this is the only assertion that doesn't depend on
        // WalkSAT's specific search trajectory.
        assert!(
            saved_phase[1] != Value::Unassigned && saved_phase[2] != Value::Unassigned,
            "saved_phase = {saved_phase:?}, want a complete assignment after a WalkSAT burst"
        );
    }

    /// Verifies that a rephase_interval_restarts of 0 never fires
    /// rather than panicking on the modulo -- a hand-edited
    /// `.vibe_sat.json` could set this, since params::default never
    /// does.
    #[test]
    fn test_maybe_rephase_guards_non_positive_interval() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1, 2]],
        };
        let lists = occurrence::build(&problem);
        let p = params::Cdcl {
            rephase_interval_restarts: 0,
            rephase_max_flips: 100,
            ..params::default().cdcl
        };
        let mut saved_phase = vec![Value::Unassigned, Value::True];
        let mut rng = StdRng::seed_from_u64(1);
        let solution = maybe_rephase(
            PhaseStrategy::RephaseWalkSAT,
            0,
            &p,
            &problem,
            &lists,
            &mut saved_phase,
            &mut rng,
        );
        assert!(solution.is_none(), "must not panic, must not run");
        assert_eq!(saved_phase[1], Value::True);
    }

    /// Verifies STAGE43.md's flagged edge case directly: when a
    /// WalkSAT burst returns a complete satisfying assignment,
    /// rephase_from_walksat returns Some(assignment) rather than
    /// merely updating saved_phase. Uses a single two-literal clause
    /// and a generous flip budget so the burst is satisfied with
    /// overwhelming probability on the first try.
    #[test]
    fn test_rephase_from_walksat_solves_early_exit() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1, 2]],
        };
        let lists = occurrence::build(&problem);
        let p = params::Cdcl {
            rephase_max_flips: 1000,
            ..params::default().cdcl
        };
        let mut saved_phase = vec![Value::Unassigned, Value::Unassigned, Value::Unassigned];
        let mut rng = StdRng::seed_from_u64(1);

        let solution = rephase_from_walksat(&problem, &lists, &p, &mut saved_phase, &mut rng);

        let solution = solution
            .expect("a single two-literal clause must be solved by WalkSAT within 1000 flips");
        assert!(
            solution[1] == Value::True || solution[2] == Value::True,
            "solution = {solution:?}, want at least one variable True (clause [1, 2] satisfied)"
        );
    }

    /// End-to-end smoke test through run_loop (rather than calling
    /// rephase_from_walksat directly, as
    /// test_rephase_from_walksat_solves_early_exit already does
    /// deterministically) confirming the wiring between maybe_restart,
    /// maybe_rephase, and the early-exit check in run_loop's own main
    /// loop doesn't break ordinary solving: a trivial satisfiable
    /// problem, forced to rephase on every restart
    /// (rephase_interval_restarts=1) with restarts firing after every
    /// single conflict (a luby_base_conflicts of 1), must still reach
    /// satisfiable=true, in at most one conflict for a single
    /// three-literal clause -- a loose bound consistent with (though
    /// not, on its own, conclusive proof of) the early-exit path
    /// having actually fired, since normal CDCL resolution of this
    /// trivial clause would also need only the one conflict.
    #[test]
    fn test_run_loop_returns_sat_on_walksat_early_exit() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![1, 2, 3]],
        };
        let p = params::Cdcl {
            luby_base_conflicts: 1,
            rephase_interval_restarts: 1,
            rephase_max_flips: 1000,
            ..params::default().cdcl
        };
        let mut rng = StdRng::seed_from_u64(1);

        let result = run_loop(
            &problem,
            None,
            SelectVarVariant::Weighted,
            RestartStrategy::Luby,
            None,
            &mut rng,
            None,
            None,
            &[],
            &p,
            PhaseStrategy::RephaseWalkSAT,
        );

        assert!(
            result.satisfiable,
            "want satisfiable=true (a trivially satisfiable problem, with WalkSAT rephasing forced every restart)"
        );
        assert!(
            result.num_conflicts <= 1,
            "num_conflicts = {}, want at most 1 for this trivial problem",
            result.num_conflicts
        );
    }

    /// Verifies STAGE45.md's invariant directly: immediately after
    /// bootstrap, every clause's two current watches appear in that
    /// literal's own watcher list.
    #[test]
    fn test_build_watchers_matches_watch() {
        let problem = Problem {
            num_vars: 4,
            clauses: vec![vec![1, 2, 3], vec![-1, 2, -3], vec![1, -4]],
        };
        let (working_problem, watch, _x) = bootstrap(&problem).expect("expected a valid bootstrap");
        let (mut watchers_positive, mut watchers_negative) =
            build_watchers(&watch, working_problem.num_vars);

        for (c, w) in watch.iter().enumerate() {
            for &lit in w {
                assert!(
                    watchers_for(&mut watchers_positive, &mut watchers_negative, lit).contains(&c),
                    "clause {c} watches {lit} but does not appear in watchers_for({lit})"
                );
            }
        }
    }

    /// STAGE45.md's central correctness test: a single `propagate`
    /// call where, for the literal being falsified, one watcher moves
    /// away (found a replacement), two more stay (no replacement
    /// available), and the *last* of those two conflicts --
    /// deliberately positioned so a real gap already exists in the
    /// watcher list (from the earlier move-away) by the time the
    /// conflict is found, and at least one further, not-yet-scanned
    /// entry exists after it in the original list. Every one of these
    /// cases has to be handled correctly by the in-place compaction
    /// for the literal's own watcher list to end up correct
    /// afterward.
    ///
    /// Clauses, by index:
    ///
    ///   0 (D): {1, 6, 7} -- 3 literals; watches 1 and 6 initially, so
    ///     a replacement (7) is available once literal 1 goes false:
    ///     moves away from watchers_for(+1) to watchers_for(+7).
    ///   1 (B): {1, 4} -- 2 literals; no replacement possible once
    ///     literal 1 goes false (only literal 4 remains, and it's
    ///     unassigned, not a valid *replacement* target since
    ///     choose_watch only replaces the falsified slot, not both)
    ///     -- stays a watcher of +1, and forces variable 4 true.
    ///   2 (A): {1, 2} -- 2 literals; variable 2 is pre-set False (as
    ///     pure test setup, not via propagate, so nothing else
    ///     propagates as a side effect), so once literal 1 also goes
    ///     false, this clause is fully falsified: a conflict, but it
    ///     still stays a watcher of +1 (the "no replacement" case
    ///     applies regardless of whether the clause conflicts).
    ///   3 (C): {1, 3} -- 2 literals; positioned after the conflict
    ///     in watchers_for(+1)'s scan order, so propagate must never
    ///     even reach it this call -- it must survive in the watcher
    ///     list untouched for a future call to find.
    ///
    /// All four clauses pick literal 1 as their first watch
    /// (choose_watch scans each clause left to right at construction,
    /// and 1 is each clause's first literal), so watchers_for(+1)
    /// starts as [0, 1, 2, 3] in exactly this order.
    #[test]
    fn test_propagate_watcher_list_compaction() {
        let problem = Problem {
            num_vars: 7,
            clauses: vec![
                vec![1, 6, 7], // D
                vec![1, 4],    // B
                vec![1, 2],    // A
                vec![1, 3],    // C
            ],
        };
        let (working_problem, mut watch, mut x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let (mut watchers_positive, mut watchers_negative) =
            build_watchers(&watch, working_problem.num_vars);

        assert_eq!(
            *watchers_for(&mut watchers_positive, &mut watchers_negative, 1),
            vec![0, 1, 2, 3],
            "watchers_for(+1) before propagate (test setup assumption violated)"
        );

        // Pure test setup: variable 2 is false, with no trail entry
        // and no propagation triggered by it -- isolating this test
        // to exactly the one propagate() call under test, over
        // variable 1's own assignment.
        x[2] = Value::False;

        let mut level = vec![0usize; 8];
        let mut reason: Vec<Option<usize>> = vec![None; 8];
        let mut trail: Vec<usize> = Vec::new();
        let mut q_head = 0usize;
        let mut lrb = LrbState::new(7);

        assign_literal(
            -1,
            0,
            None,
            &mut x,
            &mut level,
            &mut reason,
            &mut trail,
            SelectVarVariant::Weighted,
            0,
            &mut lrb,
        ); // variable 1 := false

        let conflict = propagate(
            &working_problem.clauses,
            &mut watchers_positive,
            &mut watchers_negative,
            &mut watch,
            &mut x,
            &mut trail,
            &mut q_head,
            0,
            &mut level,
            &mut reason,
            SelectVarVariant::Weighted,
            0,
            &mut lrb,
        );

        assert_eq!(conflict, Some(2), "expected clause A ({{1 2}}) to conflict");

        assert_eq!(
            *watchers_for(&mut watchers_positive, &mut watchers_negative, 1),
            vec![1, 2, 3], // B, A, C -- D moved away
            "watchers_for(+1) after propagate"
        );
        assert_eq!(
            *watchers_for(&mut watchers_positive, &mut watchers_negative, 7),
            vec![0], // D, moved in
            "watchers_for(+7) after propagate (D should have moved here)"
        );
        assert!(
            watch[0] == [7, 6] || watch[0] == [6, 7],
            "clause 0's (D's) watch = {:?}, want [6, 7] in either order",
            watch[0]
        );
        assert_eq!(
            x[4],
            Value::True,
            "variable 4 should have been forced by clause B ({{1 4}})"
        );
        assert_eq!(
            x[3],
            Value::Unassigned,
            "variable 3 (clause C, {{1 3}}) must never have been scanned this call"
        );
    }

    /// STAGE48.md: verifies the blocking-literal check directly. When
    /// a clause's *other* watch is already true, propagate must not
    /// call choose_watch to find a new watch for the literal that just
    /// went false, even though a valid replacement exists -- the
    /// clause is already satisfied, so there is nothing to gain from
    /// moving the watch. Clause [1, 2, 3]: bootstrap (all unassigned)
    /// picks watches [1, 2] in that order (choose_watch scans left to
    /// right); variable 2 is then set true directly (pure test setup,
    /// not via propagate, so this alone triggers nothing), and
    /// variable 1 is set false via a real propagate() call. Literal 3
    /// is unassigned and would be a valid replacement watch if
    /// choose_watch were called -- the whole point of this test is
    /// confirming it is not: watch[0] must still be [1, 2], unchanged,
    /// not moved to [3, 2].
    #[test]
    fn test_propagate_skips_choose_watch_for_blocking_literal() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![1, 2, 3]],
        };
        let (working_problem, mut watch, mut x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let (mut watchers_positive, mut watchers_negative) =
            build_watchers(&watch, working_problem.num_vars);

        assert!(
            watch[0] == [1, 2],
            "watch[0] after bootstrap = {:?}, want [1, 2] (test setup assumption violated)",
            watch[0]
        );

        x[2] = Value::True; // pure test setup: makes clause 0 already satisfied via literal 2

        let mut level = vec![0usize; 4];
        let mut reason: Vec<Option<usize>> = vec![None; 4];
        let mut trail: Vec<usize> = Vec::new();
        let mut q_head = 0usize;
        let mut lrb = LrbState::new(3);

        assign_literal(
            -1,
            0,
            None,
            &mut x,
            &mut level,
            &mut reason,
            &mut trail,
            SelectVarVariant::Weighted,
            0,
            &mut lrb,
        ); // variable 1 := false

        let conflict = propagate(
            &working_problem.clauses,
            &mut watchers_positive,
            &mut watchers_negative,
            &mut watch,
            &mut x,
            &mut trail,
            &mut q_head,
            0,
            &mut level,
            &mut reason,
            SelectVarVariant::Weighted,
            0,
            &mut lrb,
        );

        assert_eq!(
            conflict, None,
            "expected no conflict (nothing to force, clause already satisfied)"
        );
        assert!(
            watch[0] == [1, 2],
            "watch[0] after propagate() = {:?}, want unchanged [1, 2] (choose_watch must not have been called, since clause 0 is already satisfied via literal 2)",
            watch[0]
        );
        assert_eq!(
            x[3],
            Value::Unassigned,
            "variable 3 should be Unassigned (clause 0 needed no attention at all)"
        );
    }

    /// Verifies that a freshly learned clause's two initial watches
    /// are recorded in watchers_positive/watchers_negative, not just
    /// in watch itself -- otherwise a future propagate() would never
    /// find this clause at all.
    #[test]
    fn test_add_learned_clause_updates_watcher_lists() {
        let problem = Problem {
            num_vars: 4,
            clauses: vec![vec![1, 2, 3, 4]],
        };
        let (mut working_problem, mut watch, _x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let mut lists = occurrence::build(&working_problem);
        let (mut watchers_positive, mut watchers_negative) =
            build_watchers(&watch, working_problem.num_vars);
        let mut level = vec![0usize; 5];
        level[3] = 2;
        level[4] = 5; // higher level than 3, so add_learned_clause's own "watch the highest-level other literal" tiebreak picks literal 4 (best)
        let mut clause_activity: Vec<f64> = vec![0.0; working_problem.clauses.len()];
        let mut clause_lbd: Vec<usize> = vec![0; working_problem.clauses.len()];
        let mut estimated_bytes: i64 = 0;

        let idx = add_learned_clause(
            &vec![-1, 3, 4],
            2,
            &mut working_problem.clauses,
            &mut lists,
            &mut watchers_positive,
            &mut watchers_negative,
            &mut watch,
            &level,
            &mut clause_activity,
            &mut clause_lbd,
            &mut estimated_bytes,
        )
        .expect("expected a stored clause for a length-3 learned clause");

        assert!(
            watchers_for(&mut watchers_positive, &mut watchers_negative, -1).contains(&idx),
            "watchers_for(-1) should contain the new clause {idx}"
        );
        assert!(
            watchers_for(&mut watchers_positive, &mut watchers_negative, 4).contains(&idx),
            "watchers_for(4) should contain the new clause {idx}"
        );
        assert_eq!(watch[idx], [-1, 4]);
    }

    /// Verifies that after a reduction pass deletes and renumbers
    /// clauses, watchers_positive/watchers_negative are rebuilt
    /// consistently with the new watch array -- every remaining
    /// clause's current watches must appear in the corresponding
    /// (renumbered) watcher lists, and nothing should reference a
    /// clause index that no longer exists.
    #[test]
    fn test_reduce_clause_database_rebuilds_watcher_lists() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1, 2]],
        };
        let (mut working_problem, mut watch, x) =
            bootstrap(&problem).expect("expected a valid bootstrap");
        let mut lists = occurrence::build(&working_problem);
        let (mut watchers_positive, mut watchers_negative) =
            build_watchers(&watch, working_problem.num_vars);
        let level = vec![0usize; 3];
        let mut clause_activity: Vec<f64> = vec![0.0; working_problem.clauses.len()];
        let mut clause_lbd: Vec<usize> = vec![0; working_problem.clauses.len()];
        let mut estimated_bytes: i64 = 0;
        let mut reason: Vec<Option<usize>> = vec![None; 3];
        let num_original_clauses = working_problem.clauses.len();
        let glue_threshold = params::default().cdcl.glue_clause_lbd_threshold;

        // Learn enough distinct, unlocked, non-glue (LBD > threshold)
        // clauses that reduce_clause_database actually has something
        // eligible to delete.
        for _ in 0..6 {
            add_learned_clause(
                &vec![1, 2],
                glue_threshold + 1,
                &mut working_problem.clauses,
                &mut lists,
                &mut watchers_positive,
                &mut watchers_negative,
                &mut watch,
                &level,
                &mut clause_activity,
                &mut clause_lbd,
                &mut estimated_bytes,
            );
        }
        reduce_clause_database(
            &mut working_problem.clauses,
            &mut lists,
            &mut watchers_positive,
            &mut watchers_negative,
            &mut watch,
            &mut clause_activity,
            &mut clause_lbd,
            &mut estimated_bytes,
            &x,
            &mut reason,
            num_original_clauses,
            glue_threshold,
        );

        for (c, w) in watch.iter().enumerate() {
            for &lit in w {
                assert!(
                    watchers_for(&mut watchers_positive, &mut watchers_negative, lit).contains(&c),
                    "after reduce_clause_database: clause {c} watches {lit} but is missing from watchers_for({lit})"
                );
            }
        }
        for lit in [1, -1, 2, -2] {
            for &c in watchers_for(&mut watchers_positive, &mut watchers_negative, lit).iter() {
                assert!(
                    c < working_problem.clauses.len(),
                    "watchers_for({lit}) contains out-of-range clause index {c} (len = {})",
                    working_problem.clauses.len()
                );
            }
        }
    }
}
