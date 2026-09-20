// Package cdcl implements conflict-driven clause learning with
// non-chronological backtracking (CDCL), per STAGE11.md: Marques-Silva
// & Sakallah, "GRASP: A Search Algorithm for Propositional
// Satisfiability," IEEE Trans. Computers, 1999 (originally 1996), and
// Moskewicz, Madigan, Zhao, Zhang & Malik, "Chaff: Engineering an
// Efficient SAT Solver," DAC 2001.
//
// Unlike dfs (internal/dfs), which explores an explicit stack of
// independent, fully cloned partial assignments and simply abandons a
// branch that reaches a contradiction, cdcl maintains a single,
// persistent assignment trail with real backtracking. When
// propagation reaches a contradiction, it walks the implication graph
// backward (the reason clause of each forced literal, transitively)
// to derive a new clause that explains the conflict -- the "first
// unique implication point" (first-UIP) scheme -- adds that learned
// clause to the formula, and jumps directly back to the decision
// level where the learned clause becomes a unit clause, rather than
// just retrying the other branch one level up. This is what makes
// watched literals (STAGE9.md) pay for themselves: BCP now runs once
// per conflict as well as once per decision, and a persistent trail
// (rather than dfs's per-branch clones) is exactly what watched
// literals were designed to support with no extra bookkeeping on
// backtrack (see backtrackTo's doc comment).
//
// Per STAGE11.md, dfs is left as-is; cdcl is a wholly separate
// algorithm selectable via --algorithm=cdcl/-a cdcl, sharing dfs's
// SelectVar heuristics (dfs.SelectVar/dfs.SelectVarFastPick) but
// nothing else.
//
// STAGE12.md adds learned-clause database management: once the
// estimated size of the clause database (see clauseByteCost) exceeds
// an optional user-supplied memory limit, reduceClauseDatabase deletes
// the least "active" half of the learned clauses that are safe to
// delete (see its doc comment), MiniSat-style (Eén & Sörensson,
// "An Extensible SAT-solver," SAT 2003).
//
// STAGE13.md adds two modern activity-based SelectVar variants that
// only make sense once conflicts exist to learn from, so unlike
// SelectVarWeighted/SelectVarFast (still delegated to dfs's identical
// heuristics), they are implemented here rather than in dfs:
//
//   - SelectVarVsids: classic VSIDS (Moskewicz et al., Chaff, DAC
//     2001). Every variable touched while resolving a conflict (see
//     analyze) has its activity bumped; the highest-activity
//     unassigned variable is picked at each decision.
//   - SelectVarLrb: a simplified Learning Rate Branching (Liang,
//     Ganesh, Poupart & Czarnecki, "Learning Rate Based Branching
//     Heuristic for SAT Solvers," SAT 2016), which the paper reports
//     beating both VSIDS and the Conflict History-Based heuristic on
//     SAT Competition 2009-2014 instances (1279 vs. 1179 vs. 1235
//     solved). Every variable's "learning rate" -- how often it has
//     recently participated in producing a learned clause, per
//     conflict it has been assigned for -- is tracked and used as its
//     score instead. This implementation omits the paper's "reason
//     side rate" bonus and its annealed learning-rate schedule for
//     the Q-value update itself (kept at a fixed alpha instead); see
//     backtrackTo's doc comment for exactly what is and isn't
//     implemented.
//
// Per STAGE13.md, SelectVarVsids is the default for --algorithm=cdcl
// (see Run's doc comment for why: the paper above favors LRB on SAT
// Competition instances, but this project's own benchmark comparison,
// reports/REPORT13.md, found VSIDS clearly ahead of both LRB and the
// older structural heuristics on this project's actual, uniform
// random 3-SAT benchmark set).
//
// STAGE14.md adds phase saving (see solver's savedPhase field, and
// decide/backtrackTo, which read and write it respectively): a
// consistently measured win in MiniSat-lineage solvers, per
// Pipatsrisawat & Darwiche's discussion of component caching and
// related techniques (2007-era MiniSat/RSat literature) cited there.
// Unconditionally on for every SelectVarVariant, since STAGE14.md
// asks that it always be, with no --alg-params toggle.
//
// STAGE15.md adds restarts: periodically abandoning the current
// decision stack and starting over from decision level 0 -- exactly
// backtrackTo(0), already used for conflict-driven backjumping --
// while every learned clause, and all other persistent state
// (activity scores, saved phases), is left untouched (see
// maybeRestart). Per Luby, Sinclair & Zuckerman, "Optimal Speedup of
// Las Vegas Algorithms," 1993, and Gomes, Selman & Kautz, "Boosting
// Combinatorial Search Through Randomization," AAAI 1998, this
// escapes runs of unlucky early decisions. Three schedules are
// implemented (see RestartStrategy):
//
//   - RestartLuby: the classic Luby sequence (see lubyTerm).
//   - RestartPolynomial: the quadratic sequence STAGE15.md originally
//     specified under the name "geometric growth" -- a*k^2 for
//     k = 1, 2, 3, ... -- which is *not* the conventional, geometric
//     (equivalently, exponential-in-k) meaning of "geometric
//     restarts" found in the literature: a geometric sequence has a
//     constant ratio between consecutive terms, and a*k^2's ratio
//     shrinks (4, 2.25, 1.78, ...) rather than staying fixed. It is
//     implemented exactly as originally specified regardless, now
//     under the accurate name "polynomial" rather than "geometric."
//   - RestartGeometric: the true geometric schedule, added once the
//     naming mismatch above was caught -- c*r^k for k = 0, 1, 2, ...,
//     a constant ratio r between consecutive restart intervals. This
//     is what "geometric restarts" conventionally means in the SAT
//     literature (e.g. MiniSat 1.13/1.14's restart scheme, before
//     Luby restarts became the default in later MiniSat versions).
//
// STAGE21.md adds a fourth choice, RestartRoundRobin, once
// multithreaded cdcl (STAGE20.md/STAGE21.md's Option B: a
// continuously shared clause pool, each thread otherwise running the
// ordinary single-threaded search independently) made "which restart
// strategy should each thread use" a real question -- see
// RestartRoundRobin's own doc comment and resolveRestartStrategy.
//
// All three schedules are driven by conflict count, not decision
// count: STAGE15.md assumes "node count" as the restart statistic,
// and conflict count is this implementation's reading of that,
// matching both the convention in the restart literature cited above
// and MiniSat-lineage implementations generally (dfs has an explicit
// node count; cdcl does not, but does already track NumConflicts
// separately from NumDecisions for exactly this kind of purpose). See
// lubyBaseConflicts/polynomialBaseConflicts/geometricBaseConflicts and
// geometricGrowthFactor for how each schedule's scale constant(s) were
// chosen.
//
// STAGE34.md adds LBD-based clause management (Audemard & Simon,
// "Predicting Learnt Clauses Quality in Modern SAT Solvers," IJCAI
// 2009 -- the paper that introduced Glucose): every learned clause's
// "Literal Block Distance" (see computeLBD) is computed once, when it
// is learned, and used two ways:
//
//   - reduceClauseDatabase now sorts eligible learned clauses by LBD
//     ascending (lowest first) rather than by activity, and never
//     deletes a "glue clause" (LBD <= glueClauseLBDThreshold) at all,
//     matching Glucose's own policy -- a clause spanning very few
//     decision levels is disproportionately likely to be useful again
//     regardless of how recently it fired, which is exactly what LBD
//     measures and pure activity does not.
//   - RestartGlucose is a fifth restart schedule (see RestartStrategy),
//     unlike the four schedule-based ones entirely data-driven: it
//     restarts whenever the moving average LBD of the last
//     glucoseWindowSize learned clauses is close to or worse than the
//     global average LBD since the search began (see
//     glucoseShouldRestart), the sign Glucose's own authors use for
//     "the search is currently producing low-quality clauses, a fresh
//     start is more likely to help than persisting." Selected via
//     --alg-params val2=5; the default remains RestartPolynomial (see
//     Run's doc comment for why), since this project's own
//     benchmark-driven-default convention means RestartGlucose earns
//     that status only if a future stage's measurement shows it
//     deserves to, not by literature reputation alone.
//
// STAGE36.md adds learned-clause minimization (Sörensson & Biere,
// "Minimizing Learned Clauses," SAT 2009 -- formalizing a heuristic
// MiniSat itself has used since 2005): once analyze derives a learned
// clause via first-UIP resolution, minimizeClause removes any literal
// that is redundant -- already implied by the clause's other literals
// together with the implication graph -- via a recursive
// (self-subsumption) check over each candidate literal's reason
// clause, and its reasons' reasons, and so on (see literalRedundant).
// A shorter learned clause is strictly better on every axis this
// project already tracks: cheaper to store, cheaper to re-examine in
// future conflicts, and it can only lower (never raise) both
// backtrackLevel and LBD, since both are computed from the clause
// literalRedundant leaves behind, not the one analyze first derives.
//
// Two concerns STAGE36.md raised directly, both addressed by design
// rather than left as open risks:
//
//   - Cost: literalRedundant memoizes every variable it visits
//     (minState, cleared incrementally via minTouched rather than a
//     full numVars-sized reset per call) so no variable's reason
//     clause is ever re-scanned twice within one minimizeClause call,
//     and minimizeClause itself enforces a hard, O(n log n)-shaped
//     work budget (minimizeWorkBudget) on the *total* number of
//     reason-clause literals examined across the whole call -- once
//     exceeded, every remaining literal in the clause is kept as-is
//     (always sound; a skipped minimization opportunity, never an
//     incorrect one) rather than letting a single pathological
//     implication chain run unbounded. See reports/REPORT36.md for
//     the measured cost on this project's own benchmark set.
//   - Threading: minimizeClause reads and writes only this solver's
//     own private state (s.clauses, s.reason, s.level, s.minState,
//     ...) -- exactly the same state analyze itself already owns
//     exclusively per STAGE20.md/STAGE21.md's Option B (each thread
//     runs a wholly independent search over its own solver instance;
//     the only cross-thread interaction is the lock-free clause
//     export/import buffer, which minimization neither reads nor
//     writes). There is no shared clause database for minimization to
//     contend with, no "stop the world" pause, and no multithreaded-
//     safety design needed beyond what analyze already has.
package cdcl

import (
	"fmt"
	"math"
	"math/bits"
	"math/rand/v2"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/dfs"
	"vibe_sat/internal/occurrence"
	"vibe_sat/internal/preprocess"
)

// noReason marks a variable's reason slot as having no antecedent
// clause: either it was fixed by the bootstrap unit propagation below
// (permanently, at level 0) or it is a decision. -1 is never a valid
// clause index.
const noReason = -1

// bytesPerLiteral and perClauseOverheadBytes approximate the memory
// footprint of one clause, for comparing against a user-supplied
// --alg-params memory limit (STAGE12.md): 4 bytes per cnf.Literal
// (an int32), plus a fixed overhead per clause standing in for its
// slice header and its parallel watch/activity slots (see solver's
// watch/activity fields). This is deliberately an approximation, not
// an exact accounting of Go's actual heap usage -- the point is a
// reduction policy that responds sensibly to a size budget, not a
// byte-for-byte memory profiler.
const (
	bytesPerLiteral        = 4
	perClauseOverheadBytes = 40
)

// clauseActivityDecay and activityRescaleThreshold implement
// MiniSat's clause activity bookkeeping (see solver's clauseActivity
// field): rather than multiplying every clause's activity by
// clauseActivityDecay after every conflict (an O(clauses) cost per
// conflict), the single clauseActivityIncrement is grown by
// 1/clauseActivityDecay instead, which has the same relative effect
// (older bumps count for less compared to newer ones) at O(1) cost.
// If the increment ever grows past activityRescaleThreshold, every
// clause's activity and the increment itself are divided back down by
// the same factor, to stay well within float64's range over a very
// long run. varActivityDecay is the analogous decay rate for
// SelectVarVsids's per-variable activity (solver's varActivity
// field); VSIDS conventionally decays faster than clause activity
// does (MiniSat's own defaults: 0.95 for variables, 0.999 for
// clauses), which is why these are two separate constants rather than
// one shared rate.
const (
	clauseActivityDecay      = 0.999
	varActivityDecay         = 0.95
	activityRescaleThreshold = 1e100
)

// lrbAlpha is the fixed learning-rate weight SelectVarLrb uses when
// updating a variable's Q-value (see backtrackTo's doc comment): the
// paper anneals this over the course of the search, starting high and
// decaying toward a floor; this implementation keeps it fixed at a
// value from within that range instead, as a documented
// simplification (see the package doc comment).
const lrbAlpha = 0.4

// glueClauseLBDThreshold is Glucose's own "glue clause" cutoff
// (Audemard & Simon 2009): a learned clause whose LBD is at or below
// this is never a reduceClauseDatabase deletion candidate, regardless
// of activity or memory pressure -- treated as close to permanently
// useful, the same protection original (bootstrapped) clauses already
// get. 2 is Glucose's own published value and the one essentially
// every LBD-using solver since has kept unchanged.
//
// glucoseWindowSize and glucoseK are RestartGlucose's own parameters
// (see glucoseShouldRestart): glucoseWindowSize is how many of the
// most recently learned clauses' LBDs make up the "recent" moving
// average, and glucoseK is the factor the recent average is compared
// against the all-time global average by.
//
// glucoseWindowSize keeps Glucose's own originally published value
// (50): a benchmark sweep (STAGE35.md; see reports/REPORT35.md)
// tried 30 and 100 against it and found neither a clear win --
// 30 solved fewer instances outright, 100 was a statistical wash --
// so there was no real evidence to move off the literature default.
//
// glucoseK does NOT keep Glucose's own value (0.8): the same sweep
// found 0.6 solving as many or more instances as 0.8 while roughly
// halving mean solve time among those solved (2.44s/2.18s vs.
// 3.27s/2.93s Go/Rust on the report's 52-file sample), a real,
// measured win rather than a rounding-error difference -- restarting
// less often than Glucose's own default suggests turned out to matter
// on this project's own benchmark mix, matching the pattern already
// found for VSIDS-over-LRB (REPORT13.md) and polynomial-over-Luby
// (REPORT15.md): trust this project's own measurement over a
// technique's published default once they disagree.
const (
	glueClauseLBDThreshold = 2
	glucoseWindowSize      = 50
	glucoseK               = 0.6
)

// minUndef/minRemovable/minFailed are literalRedundant's three-state
// memoization marks for s.minState (parallel to a variable, sized
// numVars+1, like s.seen), letting a whole minimizeClause call reuse
// one variable's redundancy verdict without re-scanning its reason
// clause: minRemovable means this variable was already proven
// redundant (safe to treat as such wherever else it's encountered);
// minFailed means it was already proven NOT redundant (a decision
// variable, or itself blocked by one, found while checking some
// earlier literal). minUndef (the zero value) means neither yet
// applies. s.minTouched lists every variable whose minState is
// currently non-zero, so minimizeClause can reset exactly those
// entries at the start of its next call rather than the whole
// numVars-sized array.
const (
	minUndef byte = iota
	minRemovable
	minFailed
)

// minimizeWorkBudgetFactor scales minimizeClause's hard cap on total
// reason-clause literals examined across one call (see
// minimizeWorkBudget): STAGE36.md explicitly asked for a bound close
// to O(n log n) in the size of the learned clause, and for an
// absolute limit "just in case," matching the same concern that drove
// Stage 30/31's subsumption/BVE work budgets. 20 was chosen the same
// way those were: generous enough that it is never observed to
// trigger on this project's own benchmark set (see
// reports/REPORT36.md), while still being a real, finite cap rather
// than no cap at all.
const minimizeWorkBudgetFactor = 20

// minimizeWorkBudget returns the total number of reason-clause
// literals minimizeClause may examine (summed across every candidate
// literal's literalRedundant call) while minimizing a clause of
// length n, before giving up and keeping every remaining literal
// unminimized. bits.Len approximates log2(n)+1 -- cheap, integer-only,
// and already this project's convention for "a log-shaped bound"
// (see e.g. lubyTerm's own iterative doubling).
func minimizeWorkBudget(n int) int {
	return minimizeWorkBudgetFactor * n * (bits.Len(uint(n)) + 1)
}

// lubyBaseConflicts and polynomialBaseConflicts are STAGE15.md's "b"
// and "a": the scale constants for the Luby and polynomial restart
// sequences respectively (see restartThreshold), expressed in
// conflicts (see the package doc comment for why conflicts, not
// decisions, is the chosen restart statistic). Both are internal
// parameters, not exposed via --alg-params, per STAGE15.md's explicit
// instruction that they're meant to be optimized later instead.
//
// lubyBaseConflicts uses MiniSat's own default Luby restart base (its
// "-rfirst" option, 100 conflicts) -- a genuinely standard value in
// the literature/practice, inherited unchanged by most MiniSat-
// lineage solvers (Glucose, CryptoMiniSat, etc.), and exactly the
// kind of standard STAGE15.md asks to prefer when one exists.
//
// polynomialBaseConflicts has no such standard to inherit: the
// quadratic sequence STAGE15.md originally specified under the name
// "geometric" growth (a*k^2) is not itself a geometric sequence (see
// the package doc comment), so no standard constant applies to this
// exact formula. Per STAGE15.md's fallback instruction, this is
// instead picked empirically to be about one second of work on this
// project's own uf250/uuf250 benchmark sample: measured at
// ~17,300-18,900 conflicts/second across ten sampled instances (five
// uf250-1065, five uuf250-1065) under this project's current default
// cdcl configuration (VSIDS + phase saving), rounded to 18000.
//
// geometricBaseConflicts and geometricGrowthFactor are "c" and "r"
// for the true geometric schedule (RestartGeometric): the restart
// interval starts at c conflicts and is multiplied by r after every
// restart. Unlike polynomialBaseConflicts, a standard pairing of
// these two constants does exist in the literature: MiniSat
// 1.13/1.14's geometric restart scheme (the scheme Luby restarts
// later replaced as MiniSat's default) used a base restart interval
// of 100 conflicts -- the same "rfirst" constant reused here as
// lubyBaseConflicts -- and a growth factor of 1.5. That 1.5 was
// itself a practical (not theoretical) choice: a value a bit below
// the golden ratio (~1.618), the same growth-factor reasoning used
// when picking dynamic array growth factors to allow memory reuse
// (a factor at or above the golden ratio can never reuse previously
// freed memory as it grows). Per STAGE15.md's preference for a
// standard value when one exists, both constants are taken from that
// standard MiniSat pairing rather than re-derived empirically.
const (
	lubyBaseConflicts       = 100
	polynomialBaseConflicts = 18000
	geometricBaseConflicts  = lubyBaseConflicts
	geometricGrowthFactor   = 1.5
)

// SelectVarVariant identifies which heuristic Run should use to pick
// the next branching variable. Values 0 and 1 match dfs.SelectVarVariant
// exactly (and are delegated to dfs.SelectVar/dfs.SelectVarFastPick
// unchanged); 2 and 3 are STAGE13.md's new activity-based heuristics,
// which only exist here since they require conflict-driven learning's
// bookkeeping to have anything to work from.
type SelectVarVariant int

const (
	// SelectVarWeighted matches dfs.SelectVarWeighted: STAGE5.md's
	// clause-weighted heuristic, delegated to dfs.SelectVar unchanged.
	SelectVarWeighted SelectVarVariant = 0
	// SelectVarFast matches dfs.SelectVarFast: the cheap static-order
	// heuristic, delegated to dfs.SelectVarFastPick unchanged.
	SelectVarFast SelectVarVariant = 1
	// SelectVarVsids is STAGE13.md's VSIDS heuristic (see the package
	// doc comment); this is Run's default.
	SelectVarVsids SelectVarVariant = 2
	// SelectVarLrb is STAGE13.md's (simplified) LRB heuristic (see
	// the package doc comment).
	SelectVarLrb SelectVarVariant = 3
)

// RestartStrategy identifies which restart schedule maybeRestart
// applies for --algorithm=cdcl (STAGE15.md; see the package doc
// comment).
type RestartStrategy int

const (
	// RestartNone disables restarts entirely -- cdcl's original
	// (pre-Stage-15) behavior.
	RestartNone RestartStrategy = 0
	// RestartLuby restarts on the classic Luby, Sinclair & Zuckerman
	// sequence (see lubyTerm), scaled by lubyBaseConflicts.
	RestartLuby RestartStrategy = 1
	// RestartPolynomial restarts on the quadratic sequence STAGE15.md
	// originally specified under the name "geometric" growth (a*k^2
	// for k = 1, 2, 3, ...; see the package doc comment for why that
	// name was wrong and has been corrected here), scaled by
	// polynomialBaseConflicts. This is Run's default (see Run's doc
	// comment for why).
	RestartPolynomial RestartStrategy = 2
	// RestartGeometric restarts on the true geometric sequence (see
	// the package doc comment): c*r^k for k = 0, 1, 2, ..., a constant
	// ratio geometricGrowthFactor between consecutive restart
	// intervals, scaled by geometricBaseConflicts.
	RestartGeometric RestartStrategy = 3
	// RestartRoundRobin is STAGE21.md's addition, meaningful only as a
	// value RunParallel/Run resolve away before ever constructing a
	// *solver -- no solver's own restartStrategy field is ever
	// RestartRoundRobin, and restartThreshold never needs to handle
	// it. It means: thread i uses roundRobinStrategies[i%3] (quadratic,
	// geometric, Luby, quadratic, ... -- see resolveRestartStrategy),
	// so each of the three strategies runs on as close to an equal
	// share of threads as num_threads allows, rather than every thread
	// racing with the identical restart cadence.
	RestartRoundRobin RestartStrategy = 4
	// RestartGlucose is STAGE34.md's addition: Glucose's own data-driven
	// restart policy (see the package doc comment and
	// glucoseShouldRestart), rather than a fixed conflict-count
	// schedule like the four strategies above. Not part of
	// RestartRoundRobin's rotation -- see roundRobinStrategies.
	RestartGlucose RestartStrategy = 5
)

// roundRobinStrategies is RestartRoundRobin's resolution order (see
// resolveRestartStrategy): thread 0 uses quadratic (RestartPolynomial),
// thread 1 uses geometric (RestartGeometric), thread 2 uses Luby
// (RestartLuby), thread 3 cycles back to quadratic, and so on. Assigning
// by spawn-order index -- a plain int RunParallel already hands every
// worker goroutine/thread, the same way Stage 17/18's winner index and
// per-worker sub-RNG already are -- gives an exactly even split with no
// extra bookkeeping, which is why this doesn't fall back to
// randomizing among the three (something STAGE21.md allowed for in
// case a deterministic per-thread index turned out to be awkward to
// get at in Go; it isn't).
var roundRobinStrategies = [3]RestartStrategy{RestartPolynomial, RestartGeometric, RestartLuby}

// resolveRestartStrategy returns the concrete restart strategy thread
// threadIndex should actually use: restartStrategy unchanged, unless
// it is RestartRoundRobin, in which case it resolves to
// roundRobinStrategies[threadIndex%3]. Called with threadIndex 0 for
// Run (so an explicit --alg-params restart=4 with --num-threads=1
// still behaves sensibly -- it resolves to the same RestartPolynomial
// thread 0 of a round-robin RunParallel run would get, rather than
// being silently mishandled) and with the worker's own spawn index for
// each of RunParallel's workers.
func resolveRestartStrategy(restartStrategy RestartStrategy, threadIndex int) RestartStrategy {
	if restartStrategy == RestartRoundRobin {
		return roundRobinStrategies[threadIndex%3]
	}
	return restartStrategy
}

// Result describes the outcome of a CDCL search.
type Result struct {
	Satisfiable  bool              // whether a satisfying assignment was found
	Assignment   assign.Assignment // the satisfying assignment; only meaningful if Satisfiable
	NumDecisions int               // number of branching decisions made
	NumConflicts int               // number of conflicts encountered (= number of clauses learned)
	// TimedOut is true if the search was abandoned before reaching a
	// verdict, rather than exhausting the search space: either the
	// time limit was reached, or (RunParallel only, STAGE20.md/
	// STAGE21.md's Option B) another worker already reached a genuine
	// verdict first and this one gave up early rather than duplicate
	// work nobody needs anymore. Satisfiable/Assignment are meaningless
	// whenever this is true.
	TimedOut bool
}

// solver holds all of the mutable state of one CDCL run.
type solver struct {
	numVars int

	// clauses grows over time as clauses are learned (length >= 2
	// only; a learned clause of length 1 is applied directly as a
	// permanent level-0 fact instead, see addLearnedClause). watch is
	// always the same length as clauses and holds, for every clause,
	// the two literals it currently watches (see STAGE9.md's
	// watchState, which this mirrors but keeps as a single persistent
	// structure rather than one cloned per branch -- see
	// backtrackTo's doc comment for why that's sound here). lists is
	// the occurrence index from Stage 2, extended in place every time
	// a clause is learned; unlike dfs's, this one must be mutable.
	clauses []cnf.Clause
	watch   [][2]cnf.Literal
	lists   *occurrence.Lists

	// numOriginalClauses is len(clauses) immediately after bootstrap,
	// before any clause is learned. Every clause at an index below
	// this is part of the original (bootstrapped) problem and must
	// never be deleted; only clauses at or above it are learned, and
	// so are eligible for reduceClauseDatabase to consider. Since
	// reduceClauseDatabase only ever removes entries at indices >=
	// numOriginalClauses while preserving the relative order of
	// everything else, this threshold stays valid across any number
	// of reduction passes without needing to be updated.
	numOriginalClauses int

	// clauseActivity holds one MiniSat-style "activity" score per
	// clause (see clauseActivityDecay), parallel to clauses; only
	// entries at index >= numOriginalClauses are ever read or
	// written, since original clauses are never deletion candidates.
	clauseActivity          []float64
	clauseActivityIncrement float64

	// clauseLBD holds one STAGE34.md Literal Block Distance value
	// (see computeLBD) per clause, parallel to clauses/clauseActivity;
	// like clauseActivity, only entries at index >= numOriginalClauses
	// are ever meaningful (original clauses are stored as 0, never
	// read). lbdScratch is computeLBD's own reusable scratch space (an
	// unsorted linear-scan set of decision levels seen so far), reused
	// across calls purely to avoid an allocation per conflict -- see
	// computeLBD's doc comment for why a linear scan beats a map here,
	// the same reasoning used throughout this project for small-N sets.
	//
	// lbdRecentBuf/lbdRecentPos/lbdRecentSum/lbdRecentFilled implement
	// RestartGlucose's "recent" moving average as a fixed-size ring
	// buffer of the last glucoseWindowSize learned clauses' LBDs (see
	// recordLBD): lbdRecentSum is always the current sum of whatever is
	// in the buffer, maintained incrementally (subtract the slot being
	// overwritten, add the new value) rather than resummed each time.
	// lbdGlobalSum/lbdGlobalCount are the corresponding all-time
	// running sum/count since the search began (never reset, including
	// across restarts -- see the package doc comment for why Glucose's
	// own policy compares "recent" against "ever," not "since the last
	// restart").
	clauseLBD       []int
	lbdScratch      []int
	lbdRecentBuf    [glucoseWindowSize]int
	lbdRecentPos    int
	lbdRecentSum    int
	lbdRecentFilled bool
	lbdGlobalSum    int64
	lbdGlobalCount  int64

	// estimatedBytes tracks clauseByteCost summed over every current
	// clause, kept up to date incrementally as clauses are learned or
	// deleted (rather than recomputed from scratch on every check) --
	// except immediately after a reduction pass, where recomputing it
	// once from the (now much shorter) clause list is simpler than
	// trying to track the exact amount subtracted.
	estimatedBytes int64
	// memoryLimitBytes is the optional --alg-params-supplied limit
	// (STAGE12.md); nil means "no limit" (STAGE11.md's original,
	// unbounded behavior).
	memoryLimitBytes *int64

	// variant selects which SelectVar heuristic decide uses
	// (STAGE13.md); fixed for the lifetime of one solver/Run call.
	variant SelectVarVariant

	// varActivity holds one VSIDS activity score per variable
	// (SelectVarVsids only), sized numVars+1, bumped in analyze and
	// decayed in learnAndBackjump exactly like clauseActivity, but
	// with its own increment and decay rate (see varActivityDecay).
	varActivity          []float64
	varActivityIncrement float64

	// lrbQ holds one learning-rate score per variable (SelectVarLrb
	// only), sized numVars+1: see backtrackTo's doc comment for how
	// it's updated. lrbParticipated counts, for each variable
	// currently assigned, how many conflicts it has contributed a
	// literal to since it was last assigned; lrbAssignedAtConflict
	// records numConflicts's value at the moment each variable was
	// last assigned, so backtrackTo can compute how many conflicts
	// elapsed while it was assigned.
	lrbQ                  []float64
	lrbParticipated       []int
	lrbAssignedAtConflict []int

	// savedPhase holds, for every variable, the value it last held
	// before becoming unassigned (STAGE14.md), sized numVars+1;
	// decide consults this to guess the same polarity again next time
	// that variable is chosen, rather than always guessing False. Its
	// zero value is assign.False (see assign.Value's declaration
	// order), which is exactly the polarity a variable that has never
	// been assigned before should default to, so no separate
	// initialization is needed.
	savedPhase assign.Assignment

	// restartStrategy selects which restart schedule maybeRestart
	// applies (STAGE15.md); fixed for the lifetime of one solver/Run
	// call. restartCount is how many restarts have happened so far,
	// indexing into the Luby/geometric sequence (see
	// restartThreshold); conflictsSinceRestart counts conflicts since
	// the previous restart, or since the search began if there
	// hasn't been one yet.
	restartStrategy       RestartStrategy
	restartCount          int
	conflictsSinceRestart int

	// stop, export, peers, and peerCursors (STAGE20.md/STAGE21.md's
	// Option B) are nil for every single-threaded caller (Run itself),
	// matching the nil-safe optional-field pattern Stage 17 established
	// for hc/ws's Params.Stop/BestScore: newSolver never sets any of
	// these, and runLoop only wires them in for RunParallel's workers.
	//
	// stop is checked once per loop iteration (see runLoop); the
	// moment any worker reaches a genuine verdict, it CASes stop from
	// false to true, and every other worker notices at its very next
	// iteration and abandons its own (now-redundant) search.
	//
	// export is this thread's own outbox: every learned clause short
	// enough to qualify (see exportMaxClauseLen) is published to it
	// continuously, in addLearnedClause, as soon as it's learned --
	// not batched to restart boundaries, which is the whole point of
	// Option B over the batched alternatives REPORT20.md considered
	// and rejected. peers is every OTHER thread's own outbox (this
	// thread's own is deliberately excluded); peerCursors[i] is this
	// thread's own next-read index into peers[i] (see maybeImport).
	stop        *atomic.Bool
	export      *exportBuffer
	peers       []*exportBuffer
	peerCursors []uint64

	x            assign.Assignment // current (partial) assignment
	level        []int             // level[v] = decision level at which v was assigned (meaningless if x[v] is Unassigned)
	reason       []int             // reason[v] = index into clauses of the clause that forced v, or noReason for a decision or a level-0 fact
	trail        []int             // variable numbers, in the order they were assigned
	trailLim     []int             // trailLim[d] = len(trail) at the moment decision level d began; trailLim[0] == 0
	currentLevel int
	qHead        int // propagate() has already processed trail[:qHead]

	seen []bool // scratch space for analyze, sized numVars+1

	// minState/minTouched/minStack/minWork are minimizeClause's own
	// scratch space (STAGE36.md; see minUndef/minRemovable/minFailed
	// and minimizeClause's doc comment): minState is sized numVars+1,
	// like seen; minTouched and minStack grow via append and are reset
	// with a [:0] slice, like lbdScratch, rather than reallocated.
	// minWork counts reason-clause literals examined so far in the
	// current minimizeClause call, checked against minimizeWorkBudget.
	minState   []byte
	minTouched []int
	minStack   []minimizeFrame
	minWork    int

	numDecisions int
	numConflicts int
}

// clauseByteCost estimates clause's contribution to the clause
// database's memory footprint; see bytesPerLiteral/perClauseOverheadBytes.
func clauseByteCost(clause cnf.Clause) int64 {
	return perClauseOverheadBytes + int64(len(clause))*bytesPerLiteral
}

// newSolver builds the initial solver state for problem: a defensive
// bootstrap round of unit propagation (see dfs.Run's identical
// bootstrap step for why this is needed even though Stage 8's
// preprocessing already does this by default), followed by initial
// watch state for whatever clauses survive it. memoryLimitBytes is
// the optional learned-clause database memory limit (STAGE12.md); nil
// means unbounded. variant is the SelectVar heuristic to use
// (STAGE13.md). restartStrategy is the restart schedule to use
// (STAGE15.md). ok is false if this bootstrap alone already proves
// problem unsatisfiable.
func newSolver(problem *cnf.Problem, memoryLimitBytes *int64, variant SelectVarVariant, restartStrategy RestartStrategy) (s *solver, ok bool) {
	clauses := append([]cnf.Clause(nil), problem.Clauses...)
	x := assign.New(problem.NumVars)
	if unsat, _ := preprocess.UnitPropagate(&clauses, x); unsat {
		return nil, false
	}

	watch := make([][2]cnf.Literal, len(clauses))
	var estimatedBytes int64
	for c, clause := range clauses {
		first, foundFirst := chooseWatch(clause, x, 0)
		if !foundFirst {
			return nil, false
		}
		second, foundSecond := chooseWatch(clause, x, first)
		if !foundSecond {
			return nil, false
		}
		watch[c] = [2]cnf.Literal{first, second}
		estimatedBytes += clauseByteCost(clause)
	}

	reason := make([]int, problem.NumVars+1)
	for v := range reason {
		reason[v] = noReason
	}

	return &solver{
		numVars:                 problem.NumVars,
		clauses:                 clauses,
		watch:                   watch,
		lists:                   occurrence.Build(&cnf.Problem{NumVars: problem.NumVars, Clauses: clauses}),
		numOriginalClauses:      len(clauses),
		clauseActivity:          make([]float64, len(clauses)),
		clauseActivityIncrement: 1.0,
		clauseLBD:               make([]int, len(clauses)),
		estimatedBytes:          estimatedBytes,
		memoryLimitBytes:        memoryLimitBytes,
		variant:                 variant,
		restartStrategy:         restartStrategy,
		varActivity:             make([]float64, problem.NumVars+1),
		varActivityIncrement:    1.0,
		lrbQ:                    make([]float64, problem.NumVars+1),
		lrbParticipated:         make([]int, problem.NumVars+1),
		lrbAssignedAtConflict:   make([]int, problem.NumVars+1),
		savedPhase:              make(assign.Assignment, problem.NumVars+1),
		x:                       x,
		level:                   make([]int, problem.NumVars+1),
		reason:                  reason,
		trailLim:                []int{0},
		seen:                    make([]bool, problem.NumVars+1),
		minState:                make([]byte, problem.NumVars+1),
	}, true
}

// Run performs a CDCL search: repeatedly propagate, and on a
// conflict, learn a clause and backjump; on reaching a fixpoint with
// no conflict, either the assignment is complete (satisfiable) or a
// new variable is chosen to branch on (a decision, always tried
// False first -- see decide's doc comment). If propagate ever
// conflicts while at decision level 0 (nothing left to backjump to),
// the problem is proven unsatisfiable.
//
// variant selects which SelectVar heuristic is used to pick the
// branching variable at every decision (see SelectVarVariant); per
// STAGE11.md, this is the first algorithm parameter cdcl accepts,
// same as dfs, but STAGE13.md extends the range dfs's own values
// don't cover (2 = VSIDS, 3 = LRB). STAGE13.md leaves the default up
// to this implementation: it is SelectVarVsids. The paper cited in
// the package doc comment reports LRB solving more instances than
// VSIDS on SAT Competition instances, but this project's own
// benchmark comparison (reports/REPORT13.md) found VSIDS clearly
// ahead of both LRB and the older, purely structural heuristics this
// project started with in Stages 5-6 on this project's actual
// (uniform random 3-SAT) benchmark set -- real measurement on the
// relevant benchmarks wins out over a priori literature reasoning
// here. Callers that want one of the others still get it by passing
// 0, 1, or 3 explicitly.
//
// restartStrategy is the third algorithm parameter, STAGE15.md: which
// restart schedule to use (see RestartStrategy and the package doc
// comment). STAGE15.md leaves the default up to this implementation:
// it is RestartPolynomial. Most MiniSat-lineage solvers (MiniSat,
// Glucose, CaDiCaL) default to Luby restarts instead, but this
// project's own benchmark comparison (reports/REPORT15.md) found the
// polynomial schedule clearly ahead on this project's actual (uniform
// random 3-SAT) benchmark set -- especially on UNSAT instances (10/10
// solved within a fixed budget, vs. 6/10 with no restarts and just
// 3/10, worse than no restarts at all, with Luby) -- so, as in
// STAGE13.md's SelectVar default, real measurement on the relevant
// benchmarks wins out over the a priori literature default here. See
// reports/REPORT15.md for how RestartGeometric (added later, once the
// original "geometric" name for RestartPolynomial turned out to be
// wrong) compared. Callers that want one of the others still get it
// by passing RestartNone, RestartLuby, or RestartGeometric explicitly.
//
// memoryLimitBytes is the second, optional, STAGE12.md algorithm
// parameter (shifted to the third --alg-params slot by STAGE15.md):
// once the estimated size of the learned-clause database exceeds it,
// the least active learned clauses are periodically deleted (see
// reduceClauseDatabase); nil means unbounded, matching cdcl's original
// (Stage 11) behavior.
//
// If timeLimit is non-nil, the search gives up and reports an
// inconclusive result (Satisfiable == false, TimedOut == true) once
// it is exceeded, checked after every step (STAGE35.md; see
// runLoop's own comment). rng supplies the randomness
// SelectVarWeighted uses to break ties,
// and verbose controls progress output, matching dfs.Run.
func Run(problem *cnf.Problem, timeLimit *time.Duration, variant SelectVarVariant, restartStrategy RestartStrategy, memoryLimitBytes *int64, rng *rand.Rand, verbose int) Result {
	if verbose >= 1 {
		fmt.Println("cdcl:", describeParams(timeLimit, variant, restartStrategy)+describeMemoryLimit(memoryLimitBytes))
	}
	result := runLoop(problem, timeLimit, variant, resolveRestartStrategy(restartStrategy, 0), memoryLimitBytes, rng, nil, nil, nil)
	if verbose >= 1 {
		fmt.Println(verdict(result))
	}
	return result
}

// RunParallel runs the same search as Run, split across numThreads
// concurrent workers implementing STAGE20.md/STAGE21.md's Option B:
// every worker independently runs the ordinary single-threaded search
// over the *complete* original problem -- no divide-and-conquer, no
// work redistribution, nothing shared between workers except learned
// clauses (see exportBuffer/maybeImport) -- so unlike dfs's parallel
// search (STAGE18.md), any single worker's own verdict, SAT or UNSAT,
// is already the authoritative answer for the whole problem the
// moment it's reached; there is no termination-detection protocol to
// build here at all, only the same CAS-based single-winner shutdown
// Stage 17/18 already established.
//
// numThreads <= 1 delegates straight to Run, with rng used exactly as
// it always has been, so behavior is bit-for-bit identical to calling
// Run directly whenever multithreading isn't in use, matching the
// same guarantee Stage 17/18 established for the parallel hillclimb/
// walksat/dfs entry points.
//
// restartStrategy is resolved per worker via resolveRestartStrategy:
// RestartRoundRobin (STAGE21.md) assigns worker i
// roundRobinStrategies[i%3] (an even split of quadratic/geometric/
// Luby across the workers); any other explicit strategy, including
// RestartNone, is used unchanged by every worker. Per STAGE21.md,
// callers (see main.go's runCDCL) are expected to pass
// RestartRoundRobin as the default restart strategy whenever
// numThreads > 1 and the caller didn't explicitly ask for something
// else, and whatever was explicitly asked for otherwise -- that
// policy lives in main.go, not here, since RunParallel has no way to
// tell "the caller's default" from "the caller's explicit choice"
// apart once it's just a RestartStrategy value.
//
// rng is used, before any worker starts, to derive one independent
// sub-generator per worker (see newSubRand), so the overall result is
// fully reproducible given (rng's state, numThreads) even though
// which worker's answer wins a race to a verdict is not.
// Result.NumDecisions/NumConflicts are summed across every worker,
// win or lose, matching Stage 18's Result.NumNodes convention.
func RunParallel(problem *cnf.Problem, timeLimit *time.Duration, variant SelectVarVariant, restartStrategy RestartStrategy, memoryLimitBytes *int64, numThreads int, rng *rand.Rand, verbose int) Result {
	if numThreads <= 1 {
		return Run(problem, timeLimit, variant, restartStrategy, memoryLimitBytes, rng, verbose)
	}

	if verbose >= 1 {
		fmt.Println("cdcl:", describeParallelParams(timeLimit, variant, restartStrategy, numThreads)+describeMemoryLimit(memoryLimitBytes))
	}

	exportBuffers := make([]*exportBuffer, numThreads)
	for i := range exportBuffers {
		exportBuffers[i] = newExportBuffer(exportBufferCapacity)
	}
	subRands := make([]*rand.Rand, numThreads)
	for i := range subRands {
		subRands[i] = newSubRand(rng)
	}

	var stop atomic.Bool
	var winner atomic.Int32
	winner.Store(-1)

	results := make([]Result, numThreads)
	var wg sync.WaitGroup
	for i := 0; i < numThreads; i++ {
		peers := make([]*exportBuffer, 0, numThreads-1)
		for j, buf := range exportBuffers {
			if j != i {
				peers = append(peers, buf)
			}
		}
		wg.Add(1)
		go func(i int, peers []*exportBuffer) {
			defer wg.Done()
			threadStrategy := resolveRestartStrategy(restartStrategy, i)
			results[i] = runLoop(problem, timeLimit, variant, threadStrategy, memoryLimitBytes, subRands[i], &stop, exportBuffers[i], peers)
			if !results[i].TimedOut && stop.CompareAndSwap(false, true) {
				winner.Store(int32(i))
			}
		}(i, peers)
	}
	wg.Wait()

	var final Result
	for _, r := range results {
		final.NumDecisions += r.NumDecisions
		final.NumConflicts += r.NumConflicts
	}
	if idx := winner.Load(); idx >= 0 {
		w := results[idx]
		final.Satisfiable = w.Satisfiable
		final.Assignment = w.Assignment
	} else {
		final.TimedOut = true
	}

	if verbose >= 1 {
		fmt.Println(verdict(final))
	}
	return final
}

// runLoop is Run's search loop, without the "cdcl: ..."/"SAT"/
// "UNSAT"/"UNKNOWN" announcements -- those are the caller's
// responsibility (Run prints its own; RunParallel prints one combined
// announcement/verdict for the whole parallel search instead of one
// per worker), matching Stage 17/18's identical runLoop/dfsWorker
// pattern. stop/export/peers are nil for Run's own single-threaded
// call; see the solver struct's doc comment for what each does.
func runLoop(problem *cnf.Problem, timeLimit *time.Duration, variant SelectVarVariant, restartStrategy RestartStrategy, memoryLimitBytes *int64, rng *rand.Rand, stop *atomic.Bool, export *exportBuffer, peers []*exportBuffer) Result {
	// STAGE35.md: captured before newSolver's bootstrap, not after --
	// see the package doc comment's time-check paragraph
	// (reports/REPORT35.md) for why: a slow bootstrap (unit propagation
	// + initial watch selection) used to count for nothing against
	// timeLimit, silently giving every run a full, uncounted timeLimit's
	// worth of bootstrap time on top of the requested budget.
	startTime := time.Now()

	if problem.NumVars == 0 {
		satisfiable := len(problem.Clauses) == 0
		return Result{Satisfiable: satisfiable, Assignment: assign.New(0)}
	}

	s, ok := newSolver(problem, memoryLimitBytes, variant, restartStrategy)
	if !ok {
		return Result{Satisfiable: false}
	}
	s.stop = stop
	s.export = export
	s.peers = peers
	if peers != nil {
		s.peerCursors = make([]uint64, len(peers))
	}

	for {
		if s.stop != nil && s.stop.Load() {
			return Result{NumDecisions: s.numDecisions, NumConflicts: s.numConflicts, TimedOut: true}
		}
		// STAGE35.md: the time limit is checked on every single step
		// (decision or conflict), not periodically (previously gated by
		// a now-removed timeCheckInterval bitmask, "check every 4096
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
		// Measured directly (this stage) rather than assumed: time.Since
		// costs ~14ns/call on this hardware, against ~318,000ns for a
		// typical conflict on this project's own hard benchmark instance
		// -- unmeasurable overhead, and dfs.go's Run/checkPause comment
		// found the same holds even for dfs's much cheaper nodes. See
		// reports/REPORT35.md.
		if timeLimit != nil && time.Since(startTime) >= *timeLimit {
			return Result{NumDecisions: s.numDecisions, NumConflicts: s.numConflicts, TimedOut: true}
		}

		if confl := s.propagate(); confl != noReason {
			s.numConflicts++
			if s.currentLevel == 0 {
				return Result{Satisfiable: false, NumDecisions: s.numDecisions, NumConflicts: s.numConflicts}
			}
			s.learnAndBackjump(confl)
			s.conflictsSinceRestart++
			s.maybeRestart()
			continue
		}

		if s.allAssigned() {
			return Result{Satisfiable: true, Assignment: s.x, NumDecisions: s.numDecisions, NumConflicts: s.numConflicts}
		}

		s.decide(rng)
	}
}

// verdict formats a Result's outcome for the "SAT"/"UNSAT"/"UNKNOWN"
// line Run/RunParallel print at verbose >= 1.
func verdict(r Result) string {
	if r.TimedOut {
		return "UNKNOWN"
	}
	if r.Satisfiable {
		return "SAT"
	}
	return "UNSAT"
}

// newSubRand draws two fresh uint64s from rng to seed a new,
// independent *rand.Rand, matching Stage 17/18's identical helper in
// internal/hillclimb/internal/dfs (see either's doc comment for why:
// giving every worker its own generator, derived deterministically
// and sequentially before any worker starts, rather than sharing one
// *rand.Rand across goroutines).
func newSubRand(rng *rand.Rand) *rand.Rand {
	return rand.New(rand.NewPCG(rng.Uint64(), rng.Uint64()))
}

// allAssigned reports whether every variable of the solver currently
// has a value.
func (s *solver) allAssigned() bool {
	for _, value := range s.x[1:] {
		if value == assign.Unassigned {
			return false
		}
	}
	return true
}

// decide chooses the next branching variable according to s.variant
// and pushes it onto the trail as a new decision level, guessing its
// saved phase (STAGE14.md, see solver.savedPhase's doc comment) as
// the polarity to try -- False, for a variable that has never been
// assigned before, matching the fixed order earlier stages always
// used. CDCL doesn't get to try both polarities at one level the way
// dfs does; if the guessed polarity turns out wrong, conflict
// analysis is what corrects it, by deriving a clause that forces the
// other one once it backjumps here again.
func (s *solver) decide(rng *rand.Rand) {
	var v int
	switch s.variant {
	case SelectVarFast:
		// Delegated to dfs unchanged; dfs.SelectVarFastPick ignores
		// clause contents entirely, so it needs no *cnf.Problem wrapper.
		v = dfs.SelectVarFastPick(&cnf.Problem{NumVars: s.numVars}, s.x)
	case SelectVarVsids:
		v = s.selectVarByActivity(s.varActivity)
	case SelectVarLrb:
		v = s.selectVarByActivity(s.lrbQ)
	default: // SelectVarWeighted
		// Rebuilding this on every decision (rather than caching it)
		// keeps it trivially correct as s.clauses grows via learned
		// clauses -- this is also the answer to STAGE11.md's question
		// of whether learned clauses feed back into SelectVar: yes,
		// for the weighted variant, since it scans every
		// not-yet-satisfied clause, including learned ones.
		workingProblem := &cnf.Problem{NumVars: s.numVars, Clauses: s.clauses}
		v = dfs.SelectVar(workingProblem, s.x, rng)
	}

	s.numDecisions++
	s.currentLevel++
	s.trailLim = append(s.trailLim, len(s.trail))
	lit := cnf.Literal(-v)
	if s.savedPhase[v] == assign.True {
		lit = cnf.Literal(v)
	}
	s.assignLiteral(lit, s.currentLevel, noReason)
}

// selectVarByActivity returns the unassigned variable with the
// highest score in scores (a linear scan, same asymptotic cost class
// as SelectVarWeighted's clause scan; ties keep whichever variable
// was found first). scores is s.varActivity for SelectVarVsids, or
// s.lrbQ for SelectVarLrb. Before any conflicts have occurred, every
// score is still 0, so this falls back to the lowest-numbered
// unassigned variable -- the same choice SelectVarFast would make.
func (s *solver) selectVarByActivity(scores []float64) int {
	best := -1
	bestScore := 0.0
	for v := 1; v <= s.numVars; v++ {
		if s.x[v] != assign.Unassigned {
			continue
		}
		if best == -1 || scores[v] > bestScore {
			best, bestScore = v, scores[v]
		}
	}
	return best
}

// assignLiteral records lit as true (setting its variable's value,
// level, and reason accordingly) and appends it to the trail. For
// SelectVarLrb (STAGE13.md), it also records the current conflict
// count, so backtrackTo can later tell how many conflicts elapsed
// while this variable was assigned.
func (s *solver) assignLiteral(lit cnf.Literal, level int, reason int) {
	v := lit.Var()
	if lit.IsNegative() {
		s.x[v] = assign.False
	} else {
		s.x[v] = assign.True
	}
	s.level[v] = level
	s.reason[v] = reason
	s.trail = append(s.trail, v)
	if s.variant == SelectVarLrb {
		s.lrbAssignedAtConflict[v] = s.numConflicts
	}
}

// propagate applies boolean constraint propagation via watched
// literals (see STAGE9.md's BCP, which this mirrors closely) starting
// from wherever it last left off (s.qHead), continuing until either
// the trail is exhausted (no conflict: returns noReason) or some
// clause becomes fully falsified, in which case that clause's index
// is returned as the conflict.
//
// STAGE23.md: profiling (see reports/REPORT23.md) found this loop
// responsible for ~98% of total CPU time on a hard benchmark
// instance, with chooseWatch's linear scan for a replacement watch
// alone accounting for roughly 45% of it -- unsurprising, since this
// is the innermost loop of the whole algorithm. One candidate fix was
// tried and rejected: MiniSat-lineage solvers check whether the
// *other* watched literal is already true before ever scanning the
// clause for a new watch, skipping the scan entirely when it is,
// since a clause satisfied via a true literal needs no attention
// until some future backtrack. Measured here, this made things
// worse, not better (27.0s -> ~29.6s on the same hard instance
// profiled above), reproducibly. The likely reason: this project's
// benchmark clauses are mostly length 3 (uniform random 3-SAT), so
// chooseWatch's "expensive" scan is already down to checking a
// single remaining literal -- there is very little for the added
// check to skip, and it costs a real branch and an extra
// Literal.Var() call on every single candidate, on every single call,
// whether or not it ever pays off. See reports/REPORT23.md for the
// full profile comparison and why this is left as a "bigger possible
// change" (worth revisiting conditionally, e.g. only for longer
// learned/imported clauses) rather than applied outright.
func (s *solver) propagate() int {
	for s.qHead < len(s.trail) {
		v := s.trail[s.qHead]
		s.qHead++

		var falsifiedLiteral cnf.Literal
		var candidates []int
		if s.x[v] == assign.True {
			falsifiedLiteral = cnf.Literal(-v)
			candidates = s.lists.Negative[v]
		} else {
			falsifiedLiteral = cnf.Literal(v)
			candidates = s.lists.Positive[v]
		}

		for _, c := range candidates {
			watch := &s.watch[c]
			var otherWatch cnf.Literal
			var falsifiedSlot int
			switch {
			case watch[0] == falsifiedLiteral:
				otherWatch, falsifiedSlot = watch[1], 0
			case watch[1] == falsifiedLiteral:
				otherWatch, falsifiedSlot = watch[0], 1
			default:
				continue // this clause isn't watching the falsified literal
			}

			if replacement, found := chooseWatch(s.clauses[c], s.x, otherWatch); found {
				watch[falsifiedSlot] = replacement
				continue
			}

			if isFalse(otherWatch, s.x) {
				return c // conflict: clause c is now fully false
			}
			forcedVar := otherWatch.Var()
			if s.x[forcedVar] == assign.Unassigned {
				s.assignLiteral(otherWatch, s.currentLevel, c)
			}
			// Otherwise otherWatch is already true: the clause is
			// satisfied through it, and there is nothing to do.
		}
	}
	return noReason
}

// learnAndBackjump handles one conflict discovered by propagate at
// clause index confl: it derives a learned clause via analyze, jumps
// back to the decision level analyze computed, adds the learned
// clause to the database (unless it's a unit clause, see
// addLearnedClause), and immediately asserts its asserting literal --
// which is now a forced consequence of the learned clause, not a
// fresh decision, so the next call to propagate() will pick it up
// from the trail exactly like any other propagated literal.
//
// Once per conflict (matching MiniSat's claDecayActivity), the clause
// activity increment is grown so that future activity bumps (in
// analyze) count for relatively more than past ones -- the O(1)
// equivalent of decaying every clause's activity individually. If a
// memory limit was given (STAGE12.md) and the database's estimated
// size has grown past it, reduceClauseDatabase is triggered.
//
// Finally (STAGE20.md/STAGE21.md's Option B), if this solver is part
// of a parallel run (s.peers != nil), maybeImport gives it a chance to
// pull in clauses other threads have learned -- once per conflict is
// the natural point to check, since it's already the point where this
// thread's own local database just changed shape.
func (s *solver) learnAndBackjump(confl int) {
	learned, backtrackLevel, lbd := s.analyze(confl)
	s.recordLBD(lbd)
	s.backtrackTo(backtrackLevel)
	newClause := s.addLearnedClause(learned, lbd)
	s.assignLiteral(learned[0], backtrackLevel, newClause)

	s.clauseActivityIncrement /= clauseActivityDecay
	if s.clauseActivityIncrement > activityRescaleThreshold {
		for i := range s.clauseActivity {
			s.clauseActivity[i] /= activityRescaleThreshold
		}
		s.clauseActivityIncrement /= activityRescaleThreshold
	}

	if s.variant == SelectVarVsids {
		s.varActivityIncrement /= varActivityDecay
		if s.varActivityIncrement > activityRescaleThreshold {
			for v := range s.varActivity {
				s.varActivity[v] /= activityRescaleThreshold
			}
			s.varActivityIncrement /= activityRescaleThreshold
		}
	}

	if s.memoryLimitBytes != nil && s.estimatedBytes > *s.memoryLimitBytes {
		s.reduceClauseDatabase()
	}

	s.maybeImport()
}

// maybeRestart implements STAGE15.md's restart schedules (see the
// package doc comment): once s.conflictsSinceRestart reaches the
// threshold for the current strategy and restart index (see
// restartThreshold), the search abandons its current decision stack
// and starts over from decision level 0 -- exactly backtrackTo(0),
// the same primitive conflict-driven backjumping already uses --
// while every learned clause and all other persistent state (clause
// and variable activity, saved phases) is left untouched, since
// backtrackTo never removes clauses or resets activity/phase
// bookkeeping. Called once after every conflict is otherwise handled
// (i.e. after learnAndBackjump); a no-op if s.restartStrategy is
// RestartNone.
//
// STAGE34.md: RestartGlucose is handled separately from the four
// fixed-schedule strategies below -- it doesn't consult
// restartThreshold or s.conflictsSinceRestart's count against a
// precomputed number at all, only glucoseShouldRestart's data-driven
// comparison of recent vs. global average LBD (see the package doc
// comment).
//
// STAGE34.md also fixes a latent bug this stage's testing surfaced:
// backtrackTo(0) indexes s.trailLim[1], which only exists if
// s.currentLevel > 0. Immediately after a conflict resolves to a
// level-0 learned unit clause, s.currentLevel is already 0 by the
// time this runs (learnAndBackjump's own backtrackTo already got
// there); calling backtrackTo(0) again here would panic. The fixed-
// threshold strategies' large bases (hundreds to tens of thousands of
// conflicts) made this coincidence essentially unreachable in
// practice, but RestartGlucose can trigger every ~glucoseWindowSize
// conflicts, hitting it on real benchmark files. There is nothing to
// restart when already at the root regardless of strategy, so this
// simply defers to the next conflict -- without resetting
// conflictsSinceRestart or advancing restartCount, since no restart
// actually happened.
func (s *solver) maybeRestart() {
	if s.restartStrategy == RestartNone {
		return
	}
	if s.currentLevel == 0 {
		return
	}
	if s.restartStrategy == RestartGlucose {
		if !s.glucoseShouldRestart() {
			return
		}
		s.backtrackTo(0)
		s.conflictsSinceRestart = 0
		s.restartCount++
		return
	}
	if s.conflictsSinceRestart < s.restartThreshold() {
		return
	}
	s.backtrackTo(0)
	s.conflictsSinceRestart = 0
	s.restartCount++
}

// recordLBD folds one freshly learned clause's LBD into both of
// glucoseShouldRestart's moving averages (STAGE34.md): the
// fixed-size "recent" window (a ring buffer of the last
// glucoseWindowSize LBDs, incrementally summed so updating it is
// O(1) regardless of window size) and the all-time global average
// (a running sum/count, never reset -- including across restarts,
// since restarts don't erase learned clauses or their LBDs either).
// Called once per conflict, immediately after analyze, regardless of
// which restart strategy is active, so that switching strategies
// mid-run (not currently possible, but kept simple) would never find
// the averages cold.
func (s *solver) recordLBD(lbd int) {
	s.lbdRecentSum -= s.lbdRecentBuf[s.lbdRecentPos]
	s.lbdRecentBuf[s.lbdRecentPos] = lbd
	s.lbdRecentSum += lbd
	s.lbdRecentPos++
	if s.lbdRecentPos == glucoseWindowSize {
		s.lbdRecentPos = 0
		s.lbdRecentFilled = true
	}

	s.lbdGlobalSum += int64(lbd)
	s.lbdGlobalCount++
}

// glucoseShouldRestart implements Glucose's own data-driven restart
// policy (Audemard & Simon, IJCAI 2009; see the package doc comment):
// restart when the recent window's average LBD is close to or worse
// than (i.e. at least glucoseK times) the all-time global average --
// a sign that the search has drifted into a region where it's
// learning less compact, less reusable clauses than its own history,
// and is better off abandoning the current decision stack. Requires
// at least one full recent window's worth of learned clauses before
// ever triggering, both because the ring buffer isn't a meaningful
// average until then and because lbdGlobalCount must be positive to
// divide by.
func (s *solver) glucoseShouldRestart() bool {
	if !s.lbdRecentFilled {
		return false
	}
	recentAvg := float64(s.lbdRecentSum) / float64(glucoseWindowSize)
	globalAvg := float64(s.lbdGlobalSum) / float64(s.lbdGlobalCount)
	return recentAvg*glucoseK >= globalAvg
}

// restartThreshold returns the number of conflicts that must elapse
// since the previous restart (or since the search began, before the
// first one) before the next restart is due, per s.restartStrategy
// and s.restartCount (how many restarts have already happened, used
// to index into the sequence).
//
// For RestartLuby, this is lubyBaseConflicts * lubyTerm(s.restartCount)
// -- the classic Luby sequence 1, 1, 2, 1, 1, 2, 4, ... (see lubyTerm),
// scaled by "b" (STAGE15.md).
//
// For RestartPolynomial, this is polynomialBaseConflicts * k^2, where
// k = s.restartCount + 1: STAGE15.md's specified sequence a*1^2,
// a*2^2, a*3^2, ..., scaled by "a".
//
// For RestartGeometric, this is
// geometricBaseConflicts * geometricGrowthFactor^s.restartCount --
// the true geometric sequence c, c*r, c*r^2, ..., scaled by "c" and
// with constant ratio "r" between consecutive terms. Truncated
// (rather than rounded) to an int, matching MiniSat's own geometric
// restart implementation.
func (s *solver) restartThreshold() int {
	switch s.restartStrategy {
	case RestartLuby:
		return lubyBaseConflicts * lubyTerm(s.restartCount)
	case RestartPolynomial:
		k := s.restartCount + 1
		return polynomialBaseConflicts * k * k
	case RestartGeometric:
		return int(geometricBaseConflicts * math.Pow(geometricGrowthFactor, float64(s.restartCount)))
	default: // RestartNone; never actually consulted (see maybeRestart)
		return 0
	}
}

// lubyTerm returns the i-th term (0-indexed) of the Luby, Sinclair &
// Zuckerman restart sequence: 1, 1, 2, 1, 1, 2, 4, 1, 1, 2, 1, 1, 2,
// 4, 8, ... -- formally, t_i = 2^(k-1) if i+1 = 2^k - 1, else
// t_i = t_(i+1 - 2^(k-1) + 1) - 1 (1-indexed in the original
// definition; this is the standard 0-indexed iterative form used by
// MiniSat and its descendants to compute it without recursion).
func lubyTerm(i int) int {
	size, seq := 1, 0
	for size < i+1 {
		seq++
		size = 2*size + 1
	}
	for size-1 != i {
		size = (size - 1) / 2
		seq--
		i = i % size
	}
	return 1 << seq
}

// analyze walks the implication graph backward from the clause at
// index confl (which propagate just found to be fully false) to
// derive a learned clause via first-UIP resolution: repeatedly resolve
// the current clause against the reason of the most-recently-assigned
// still-unresolved literal at the conflict's own decision level, until
// exactly one such literal remains -- the "first unique implication
// point." That literal's negation becomes the learned clause's
// asserting literal (returned first in learned); every other literal
// collected along the way is already false at some level below the
// conflict's, which is exactly what makes the learned clause a unit
// clause (modulo the asserting literal) the moment the search
// backjumps to backtrackLevel, the highest level among those other
// literals (0 if there are none).
//
// This is the standard GRASP/Chaff conflict analysis (see the package
// doc comment for references); level-0 literals are omitted entirely,
// since they are permanent facts that can never become unassigned
// again and so need no antecedent recorded in the learned clause.
//
// As a side effect (STAGE12.md), every learned clause visited along
// the way (every reasonClause at index >= s.numOriginalClauses) has
// its activity bumped by s.clauseActivityIncrement, MiniSat's measure
// of how useful a clause has recently been to conflict analysis --
// this is what reduceClauseDatabase later uses to decide which
// learned clauses to keep.
//
// As a second side effect (STAGE13.md), every *variable* newly marked
// seen here also has its SelectVarVsids/SelectVarLrb bookkeeping
// updated, whichever s.variant calls for: VSIDS bumps varActivity by
// varActivityIncrement (the same increment-growth trick as clause
// activity, decayed once per conflict in learnAndBackjump); LRB
// increments lrbParticipated, counting this as one more conflict the
// variable has contributed to since it was last assigned (see
// backtrackTo for where that turns into an updated Q-value).
//
// STAGE36.md: before backtrackLevel/lbd are computed, minimizeClause
// gets a chance to drop any literal from the just-derived learned
// clause that turns out to be redundant (see its own doc comment).
// This must happen here, before learnAndBackjump's backtrackTo runs --
// once the search backjumps, some of the now-unassigned variables'
// reason/level bookkeeping minimizeClause depends on is gone -- and
// before backtrackLevel/lbd are derived, since removing a literal can
// only lower both (they are computed from whichever literals survive
// minimization, not the ones analyze first derived).
//
// STAGE34.md: lbd is the learned (and now possibly minimized) clause's
// Literal Block Distance -- the number of distinct decision levels
// represented among its literals, computed in the same pass that
// already finds backtrackLevel (both need exactly the same per-literal
// level lookups, so this costs nothing extra beyond a small
// scratch-set membership check per literal; see s.lbdScratch's doc
// comment for why that's a plain slice, not a map). The asserting
// literal itself (learned[0], at s.currentLevel by construction -- the
// first-UIP is always a current-level variable) counts toward the
// distinct-level set too, matching Audemard & Simon's own definition
// of LBD as being over the *whole* clause, not just its non-asserting
// literals.
func (s *solver) analyze(confl int) (learned cnf.Clause, backtrackLevel int, lbd int) {
	for i := range s.seen {
		s.seen[i] = false
	}

	var p cnf.Literal // 0 = "no literal yet", used only for the very first (conflicting) clause
	counter := 0
	trailIdx := len(s.trail) - 1
	reasonClause := confl

	for {
		if reasonClause >= s.numOriginalClauses {
			s.clauseActivity[reasonClause] += s.clauseActivityIncrement
		}
		for _, lit := range s.clauses[reasonClause] {
			if p != 0 && lit.Var() == p.Var() {
				continue
			}
			v := lit.Var()
			if s.seen[v] || s.level[v] == 0 {
				continue
			}
			s.seen[v] = true
			switch s.variant {
			case SelectVarVsids:
				s.varActivity[v] += s.varActivityIncrement
			case SelectVarLrb:
				s.lrbParticipated[v]++
			}
			if s.level[v] == s.currentLevel {
				counter++
			} else {
				learned = append(learned, lit)
			}
		}

		var v int
		for {
			v = s.trail[trailIdx]
			trailIdx--
			if s.seen[v] {
				break
			}
		}
		p = literalAssignedTrue(v, s.x)
		counter--
		if counter == 0 {
			break
		}
		if s.reason[v] == noReason {
			// Should be unreachable: the decision variable of the
			// current level is always a valid (if late) UIP, and is
			// always the last current-level literal this scan can
			// reach, so counter must hit 0 at or before it.
			panic("cdcl: analyze reached a variable with no reason while literals of the current level remain unresolved")
		}
		reasonClause = s.reason[v]
	}

	learned = append(cnf.Clause{-p}, learned...)
	learned = s.minimizeClause(learned)

	backtrackLevel = 0
	s.lbdScratch = append(s.lbdScratch[:0], s.currentLevel)
	for _, lit := range learned[1:] {
		lv := s.level[lit.Var()]
		if lv > backtrackLevel {
			backtrackLevel = lv
		}
		if !slices.Contains(s.lbdScratch, lv) {
			s.lbdScratch = append(s.lbdScratch, lv)
		}
	}
	return learned, backtrackLevel, len(s.lbdScratch)
}

// minimizeFrame is one entry in literalRedundant's explicit,
// iterative-DFS stack: lit is the literal whose reason clause is
// being scanned, and idx is the next index into that clause to
// examine. An explicit stack (rather than a recursive function call
// per implication-graph edge) keeps a long chain of reasons from
// costing real call-stack depth, matching MiniSat's own iterative
// implementation of this exact algorithm.
type minimizeFrame struct {
	lit cnf.Literal
	idx int
}

// minimizeClause implements STAGE36.md's learned-clause minimization
// (Sörensson & Biere, "Minimizing Learned Clauses," SAT 2009): a
// literal in learned is redundant -- safe to drop without weakening
// the clause -- if it is already implied by the clause's other
// literals together with the implication graph, checked recursively
// via literalRedundant. The asserting literal (learned[0]) is never a
// candidate: it is the first-UIP itself, not a resolution byproduct,
// and a literal whose variable was a decision (no reason) can never be
// redundant either, so literalRedundant is only ever called for the
// rest.
//
// s.minWork (reset here) counts total reason-clause literals examined
// across every literalRedundant call this pass makes; once it exceeds
// budget (see minimizeWorkBudget), every remaining literal is kept
// unminimized rather than examined at all -- a hard, whole-call cap,
// not a per-literal one, so one pathological clause can never cost
// more than a bounded amount of work regardless of how many literals
// it has left to check.
//
// learned is filtered in place (kept shares learned's own backing
// array, safe since kept's write position never runs ahead of the
// read position), matching this project's established in-place-filter
// convention (see e.g. preprocess.simplifyWithAssignment).
func (s *solver) minimizeClause(learned cnf.Clause) cnf.Clause {
	for _, v := range s.minTouched {
		s.minState[v] = minUndef
	}
	s.minTouched = s.minTouched[:0]
	s.minWork = 0

	budget := minimizeWorkBudget(len(learned))
	kept := learned[:1]
	for _, lit := range learned[1:] {
		if s.minWork > budget || s.reason[lit.Var()] == noReason || !s.literalRedundant(lit, budget) {
			kept = append(kept, lit)
		}
	}
	return kept
}

// literalRedundant reports whether lit -- which must have a reason
// clause (checked by minimizeClause before calling) -- is redundant:
// every literal that lit's reason clause depends on (other than lit
// itself) is either a permanent level-0 fact, already accounted for
// by analyze's own resolution (s.seen), already known redundant, or
// itself (recursively) redundant by the same rule. If any dependency
// is a decision variable not already covered by one of those (no
// reason, not seen), lit is not redundant.
//
// Implemented iteratively (an explicit stack of minimizeFrame, not a
// recursive function per edge) to bound native call-stack depth
// regardless of implication-chain length, and memoized via s.minState
// so no variable's reason clause is scanned more than once per
// minimizeClause call: once a variable's redundancy is settled
// (removable or failed), every later reference to it anywhere in this
// pass is an O(1) lookup, not a re-scan. budget bounds s.minWork the
// same way across every call within one minimizeClause pass, checked
// here too (not just between calls) so a single literal's own
// redundancy chain can never itself blow past it.
func (s *solver) literalRedundant(lit cnf.Literal, budget int) bool {
	s.minStack = append(s.minStack[:0], minimizeFrame{lit: lit, idx: 0})

	for len(s.minStack) > 0 {
		if s.minWork > budget {
			return false
		}

		i := len(s.minStack) - 1
		frameLit := s.minStack[i].lit
		frameIdx := s.minStack[i].idx
		reasonClause := s.clauses[s.reason[frameLit.Var()]]

		if frameIdx >= len(reasonClause) {
			// Finished scanning this frame's reason clause without
			// finding anything that blocks redundancy: frameLit itself
			// is removable.
			v := frameLit.Var()
			if s.minState[v] == minUndef {
				s.minState[v] = minRemovable
				s.minTouched = append(s.minTouched, v)
			}
			s.minStack = s.minStack[:i]
			continue
		}

		l := reasonClause[frameIdx]
		s.minStack[i].idx++
		if l.Var() == frameLit.Var() {
			continue // skip the literal whose reason this is
		}

		s.minWork++
		v := l.Var()
		if s.level[v] == 0 || s.seen[v] || s.minState[v] == minRemovable {
			continue
		}
		if s.reason[v] == noReason || s.minState[v] == minFailed {
			// v is a decision (or already known unremovable) and isn't
			// otherwise accounted for: everything on the stack right
			// now -- lit and every literal recursion reached to get
			// here -- depends on v, so none of them is redundant.
			return false
		}
		s.minStack = append(s.minStack, minimizeFrame{lit: l, idx: 0})
	}
	return true
}

// literalAssignedTrue returns the literal on variable v that
// evaluates to true under x (v must not be Unassigned in x).
func literalAssignedTrue(v int, x assign.Assignment) cnf.Literal {
	if x[v] == assign.True {
		return cnf.Literal(v)
	}
	return cnf.Literal(-v)
}

// backtrackTo undoes every assignment made after decision level
// level, resetting the trail, trail limits, and propagation queue
// accordingly. Unlike dfs's per-branch clones, cdcl's watch state is
// never cloned or explicitly restored on backtrack: a watch remains
// valid as long as it isn't watching a literal that's currently
// false, and backtracking only ever turns assigned literals back into
// unassigned ones -- it can never turn a non-false literal into a
// false one -- so every watch already in place is still a legal watch
// after backtracking, with nothing to undo. This is precisely the
// property that makes watched literals cheap under non-chronological
// backtracking, and is why cdcl uses a single persistent trail
// instead of dfs's cloned-per-branch approach (see STAGE9.md's
// watchState doc comment for the tension this resolves).
//
// For SelectVarLrb (STAGE13.md), the moment a variable becomes
// unassigned is also exactly when its learning rate can be computed:
// the "interval" is how many conflicts occurred while it was assigned
// (s.numConflicts now, minus its value when the variable was last
// assigned, recorded by assignLiteral), and the reward r is how many
// of those conflicts it actually participated in
// (s.lrbParticipated[v]) divided by that interval. Its Q-value is
// then nudged toward r by lrbAlpha (an exponential moving average),
// and lrbParticipated is reset to 0 for its next stint as an assigned
// variable. This is the paper's core learning-rate idea; the "reason
// side rate" bonus and the annealed (rather than fixed) alpha it also
// describes are both omitted here (see the package doc comment).
//
// For every variable, regardless of s.variant, the moment it becomes
// unassigned is also when its phase is saved (STAGE14.md): whatever
// value it held (s.x[v], True or False) right before this loop
// overwrites it with Unassigned is remembered in s.savedPhase[v], so
// decide can guess the same polarity again next time this variable is
// chosen, rather than always guessing False.
func (s *solver) backtrackTo(level int) {
	cut := s.trailLim[level+1]
	for i := len(s.trail) - 1; i >= cut; i-- {
		v := s.trail[i]
		if s.variant == SelectVarLrb {
			if interval := s.numConflicts - s.lrbAssignedAtConflict[v]; interval > 0 {
				r := float64(s.lrbParticipated[v]) / float64(interval)
				s.lrbQ[v] = (1-lrbAlpha)*s.lrbQ[v] + lrbAlpha*r
			}
			s.lrbParticipated[v] = 0
		}
		s.savedPhase[v] = s.x[v] // STAGE14.md: remember this polarity for decide's next guess
		s.x[v] = assign.Unassigned
	}
	s.trail = s.trail[:cut]
	s.trailLim = s.trailLim[:level+1]
	s.currentLevel = level
	s.qHead = len(s.trail)
}

// addLearnedClause appends learned to the clause database and
// extends the occurrence lists and watch state for it, unless it's a
// unit clause (length 1): a unit clause has only one literal, which
// has nowhere to shed a second watch onto, and needs none anyway --
// analyze's backtrackLevel is always 0 for a unit learned clause, so
// its (asserting) literal is about to become a permanent level-0
// fact, exactly like a variable fixed by the bootstrap unit
// propagation in newSolver. Returns the new clause's index, or
// noReason if none was stored.
//
// STAGE20.md/STAGE21.md's Option B: if this solver is part of a
// parallel run (s.export != nil) and learned is short enough to
// qualify (see exportMaxClauseLen -- ManySAT's own convention is
// length <= 8), it is published to this thread's export buffer here,
// continuously, the moment it's learned -- not batched to some later
// point -- which is the whole reason RunParallel doesn't need any
// restart synchronization across workers at all. Unit clauses
// (returned above, before ever reaching here) are not shared; see
// exportMaxClauseLen's doc comment for why that's a deliberate,
// documented simplification rather than an oversight.
func (s *solver) addLearnedClause(learned cnf.Clause, lbd int) int {
	if len(learned) == 1 {
		return noReason
	}
	if s.export != nil && len(learned) <= exportMaxClauseLen {
		s.export.publish(learned)
	}

	idx := len(s.clauses)
	s.clauses = append(s.clauses, learned)
	s.clauseActivity = append(s.clauseActivity, 0.0)
	s.clauseLBD = append(s.clauseLBD, lbd)
	s.estimatedBytes += clauseByteCost(learned)
	for _, lit := range learned {
		v := lit.Var()
		if lit.IsNegative() {
			s.lists.Negative[v] = append(s.lists.Negative[v], idx)
		} else {
			s.lists.Positive[v] = append(s.lists.Positive[v], idx)
		}
	}

	// learned[0] is the asserting literal, currently unassigned;
	// watch it directly (chooseWatch would find it too, but it's
	// about to be assigned true by the caller regardless). The second
	// watch is whichever other literal has the highest decision
	// level, since that is the one that will become unassigned
	// soonest on some future backtrack, keeping this watch valid the
	// longest before it needs to shed anywhere.
	best := 1
	for i := 2; i < len(learned); i++ {
		if s.level[learned[i].Var()] > s.level[learned[best].Var()] {
			best = i
		}
	}
	s.watch = append(s.watch, [2]cnf.Literal{learned[0], learned[best]})
	return idx
}

// reduceClauseDatabase deletes roughly the least active half of the
// learned clauses that are safe to delete, MiniSat-style (see the
// package doc comment), to bring the database's estimated size back
// under control. A learned clause is "safe to delete" if it is not
// currently locked: locked means it is some currently assigned
// variable's reason (reason[v] for some v with x[v] != Unassigned),
// since analyze may still need to walk through it if that variable's
// assignment participates in a future conflict. Clauses at index <
// s.numOriginalClauses (the original, bootstrapped problem) are never
// candidates at all -- deleting one of those would be unsound, not
// just wasteful.
//
// Deleting from the middle of s.clauses would silently invalidate
// every other index into it (every watch entry, every reason[v], and
// every occurrence.Lists entry), so this rebuilds all of them
// together from an old-index-to-new-index map, rather than trying to
// patch each in place. This is an O(current clauses + literals)
// operation, which is fine since it only runs when the configured
// memory limit is actually exceeded, not on every conflict.
//
// STAGE34.md: a clause is also excluded from eligible -- "glue
// clause" protection, on top of the pre-existing locked exclusion --
// if its LBD is at or below glueClauseLBDThreshold, regardless of how
// low its activity has fallen, since a low LBD is itself strong
// independent evidence the clause is worth keeping even if it hasn't
// happened to participate in a conflict recently. Among the clauses
// that remain eligible, the sort is now LBD ascending first (higher
// LBD = less "compact" = deleted first) with activity descending only
// as a tiebreak among equal-LBD clauses, rather than pure activity as
// before.
func (s *solver) reduceClauseDatabase() {
	locked := make([]bool, len(s.clauses))
	for v := 1; v <= s.numVars; v++ {
		if s.x[v] != assign.Unassigned && s.reason[v] != noReason {
			locked[s.reason[v]] = true
		}
	}

	var eligible []int
	for idx := s.numOriginalClauses; idx < len(s.clauses); idx++ {
		if !locked[idx] && s.clauseLBD[idx] > glueClauseLBDThreshold {
			eligible = append(eligible, idx)
		}
	}
	sort.Slice(eligible, func(i, j int) bool {
		a, b := eligible[i], eligible[j]
		if s.clauseLBD[a] != s.clauseLBD[b] {
			return s.clauseLBD[a] > s.clauseLBD[b]
		}
		return s.clauseActivity[a] < s.clauseActivity[b]
	})

	numToDelete := len(eligible) / 2
	if numToDelete == 0 {
		return // nothing eligible to delete; not worth a full rebuild
	}
	toDelete := make([]bool, len(s.clauses))
	for _, idx := range eligible[:numToDelete] {
		toDelete[idx] = true
	}

	oldToNew := make([]int, len(s.clauses))
	newClauses := make([]cnf.Clause, 0, len(s.clauses)-numToDelete)
	newWatch := make([][2]cnf.Literal, 0, len(s.clauses)-numToDelete)
	newActivity := make([]float64, 0, len(s.clauses)-numToDelete)
	newLBD := make([]int, 0, len(s.clauses)-numToDelete)
	for idx, clause := range s.clauses {
		if toDelete[idx] {
			oldToNew[idx] = noReason
			continue
		}
		oldToNew[idx] = len(newClauses)
		newClauses = append(newClauses, clause)
		newWatch = append(newWatch, s.watch[idx])
		newActivity = append(newActivity, s.clauseActivity[idx])
		newLBD = append(newLBD, s.clauseLBD[idx])
	}

	for v := 1; v <= s.numVars; v++ {
		if s.x[v] != assign.Unassigned && s.reason[v] != noReason {
			s.reason[v] = oldToNew[s.reason[v]]
		}
	}

	s.clauses = newClauses
	s.watch = newWatch
	s.clauseActivity = newActivity
	s.clauseLBD = newLBD
	s.lists = occurrence.Build(&cnf.Problem{NumVars: s.numVars, Clauses: newClauses})

	s.estimatedBytes = 0
	for _, clause := range newClauses {
		s.estimatedBytes += clauseByteCost(clause)
	}
}

// chooseWatch scans clause for a literal that is not false under
// assignment and is not equal to avoid (0 is never a valid literal,
// so passing 0 imposes no exclusion). Identical in spirit to
// STAGE9.md's dfs/watch.go helper of the same name; duplicated here
// (rather than exported from internal/dfs) since STAGE11.md asks that
// dfs be left as it is.
func chooseWatch(clause cnf.Clause, assignment assign.Assignment, avoid cnf.Literal) (cnf.Literal, bool) {
	for _, lit := range clause {
		if lit == avoid {
			continue
		}
		if !isFalse(lit, assignment) {
			return lit, true
		}
	}
	return 0, false
}

// isFalse reports whether lit currently evaluates to false under
// assignment (an Unassigned variable makes every literal on it
// neither true nor false yet, so this returns false for those).
func isFalse(lit cnf.Literal, assignment assign.Assignment) bool {
	value := assignment[lit.Var()]
	if value == assign.Unassigned {
		return false
	}
	if lit.IsNegative() {
		return value == assign.True
	}
	return value == assign.False
}

// describeParams formats the configured time limit and SelectVar
// variant for the "cdcl:" announcement printed at verbose level 1.
func describeParams(timeLimit *time.Duration, variant SelectVarVariant, restartStrategy RestartStrategy) string {
	description := fmt.Sprintf("select_var=%d restart=%d", variant, restartStrategy)
	if timeLimit != nil {
		description += fmt.Sprintf(" time_limit_secs=%d", int(timeLimit.Seconds()))
	}
	return description
}

// describeParallelParams is describeParams, extended with the worker
// count, for RunParallel's "cdcl: ..." announcement (STAGE20.md/
// STAGE21.md), matching dfs.describeParallelParams's identical role.
func describeParallelParams(timeLimit *time.Duration, variant SelectVarVariant, restartStrategy RestartStrategy, numThreads int) string {
	return fmt.Sprintf("num_threads=%d %s", numThreads, describeParams(timeLimit, variant, restartStrategy))
}

// describeMemoryLimit formats an optional STAGE12.md memory limit for
// the "cdcl:" announcement printed at verbose level 1, in whichever
// unit main.go's --alg-params parsing recorded it in bytes as.
func describeMemoryLimit(memoryLimitBytes *int64) string {
	if memoryLimitBytes == nil {
		return ""
	}
	return fmt.Sprintf(" memory_limit_bytes=%d", *memoryLimitBytes)
}
