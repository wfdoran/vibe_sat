package preprocess

import (
	"slices"
	"sync"
	"sync/atomic"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
)

// simplifyWithAssignment rewrites clauses in place under assignment:
// every clause already satisfied by some true literal is dropped, and
// every other clause has its false literals (if any) removed,
// leaving only unassigned literals. It reports whether a contradiction
// was found (a clause with no unassigned literals and none true).
//
// STAGE35.md: reduced is allocated lazily, only once some literal in
// clause actually needs dropping (is false under assignment) -- not
// on every clause regardless. UnitPropagate calls this once per
// propagation round over the *entire* remaining clause set, and by
// the second round onward the overwhelming majority of clauses
// contain nothing assigned since the previous round at all, so they
// need no new slice at all: clause itself is still exactly the
// reduced form, and can be kept as-is (kept = append(kept, clause),
// a cheap slice-header copy, not a literal copy). When a literal
// does need dropping, reduced is allocated once at len(clause)
// capacity -- its maximum possible size, since every literal is
// either kept, drops the clause entirely (satisfied), or discarded
// (false), never growing past the original literal count -- and
// backfilled with every unassigned literal seen so far
// (clause[:i]), so no further reallocation happens for the rest of
// this clause either.
//
// A CPU profile taken while investigating REPORT29.md's 14x
// time-limit overrun found the original code (a fresh, unpreallocated
// `var reduced cnf.Clause` on literally every clause, every round,
// regardless of whether anything changed) responsible for the
// overwhelming majority of newSolver's total time on a 6.3M-clause
// file via runtime.growslice/mallocgc -- not the search loop's
// time-check granularity STAGE35.md was originally scoped to fix, but
// the real dominant cost behind that specific 42.65-second
// measurement. See reports/REPORT35.md for the full before/after
// numbers.
func simplifyWithAssignment(clauses *[]cnf.Clause, assignment assign.Assignment) (unsat bool) {
	kept := (*clauses)[:0]
	for _, clause := range *clauses {
		var reduced cnf.Clause // lazily allocated; nil means "clause unchanged so far"
		satisfied := false
		for i, lit := range clause {
			value := assignment[lit.Var()]
			if value == assign.Unassigned {
				if reduced != nil {
					reduced = append(reduced, lit)
				}
				continue
			}
			if assignment.LiteralIsTrue(lit) {
				satisfied = true
				break
			}
			// lit is false and must be dropped: the first time this
			// happens for this clause, allocate reduced and backfill
			// every unassigned literal already seen (clause[:i], which
			// excludes this false one); every literal after this point
			// goes through the reduced != nil branch above.
			if reduced == nil {
				reduced = make(cnf.Clause, 0, len(clause))
				reduced = append(reduced, clause[:i]...)
			}
		}
		if satisfied {
			continue
		}
		if reduced == nil {
			// No literal was ever false: clause is either empty (unsat,
			// matching the len(reduced) == 0 check below) or entirely
			// unassigned literals, unchanged -- keep the original slice,
			// no allocation needed.
			if len(clause) == 0 {
				return true
			}
			kept = append(kept, clause)
			continue
		}
		if len(reduced) == 0 {
			return true
		}
		kept = append(kept, reduced)
	}
	*clauses = kept
	return false
}

// UnitPropagate repeatedly finds a clause with exactly one unassigned
// literal and forces that literal true, removing now-satisfied
// clauses and shrinking others as it goes (via simplifyWithAssignment),
// until no clause is a unit clause or a contradiction is found. It
// returns whether a contradiction was found, and how many variables
// were newly fixed.
func UnitPropagate(clauses *[]cnf.Clause, assignment assign.Assignment) (unsat bool, numFixed int) {
	for {
		if unsat := simplifyWithAssignment(clauses, assignment); unsat {
			return true, numFixed
		}

		foundUnit := false
		for _, clause := range *clauses {
			if len(clause) != 1 {
				continue
			}
			lit := clause[0]
			if lit.IsNegative() {
				assignment[lit.Var()] = assign.False
			} else {
				assignment[lit.Var()] = assign.True
			}
			numFixed++
			foundUnit = true
		}
		if !foundUnit {
			return false, numFixed
		}
	}
}

// eliminatePureLiterals repeatedly finds variables that appear with
// only one polarity across all of clauses (never negated, or always
// negated) and fixes them to the value that satisfies every clause
// containing them, removing those now-satisfied clauses. It returns
// how many variables were fixed this way.
func eliminatePureLiterals(clauses *[]cnf.Clause, assignment assign.Assignment) (numFixed int) {
	for {
		seenPositive := make([]bool, len(assignment))
		seenNegative := make([]bool, len(assignment))
		for _, clause := range *clauses {
			for _, lit := range clause {
				if lit.IsNegative() {
					seenNegative[lit.Var()] = true
				} else {
					seenPositive[lit.Var()] = true
				}
			}
		}

		fixedThisPass := false
		for v := 1; v < len(assignment); v++ {
			if assignment[v] != assign.Unassigned {
				continue
			}
			switch {
			case seenPositive[v] && !seenNegative[v]:
				assignment[v] = assign.True
			case seenNegative[v] && !seenPositive[v]:
				assignment[v] = assign.False
			default:
				continue
			}
			numFixed++
			fixedThisPass = true
		}
		if !fixedThisPass {
			return numFixed
		}
		// Fixing a pure literal can only satisfy clauses (it never
		// introduces a new false literal elsewhere, since the
		// variable never appears with the opposite polarity), so this
		// cannot discover a contradiction.
		_ = simplifyWithAssignment(clauses, assignment)
	}
}

// isSubsetOf reports whether every literal of small appears in big.
//
// STAGE25.md: this used to check membership against a
// map[cnf.Literal]bool built once per clause (clauseLiteralSet,
// removed). Profiling (perf on the Rust side; see REPORT25.md) found
// that building and probing those maps/HashSets -- once per pair, in
// what is otherwise an O(len(small)) scan -- was the single largest
// cost in preprocessing on real benchmark instances (bw_large.c/.d:
// 87-94% of total preprocessing time), not the eliminateVariables
// (BVE) pass REPORT22.md guessed. This project's clauses are almost
// always short (a handful of literals, occasionally a few dozen for
// structured instances), and a hash lookup's own overhead (computing
// the hash, probing a bucket) is more expensive than just scanning
// that many literals directly -- slices.Contains has no hashing, no
// allocation, and better cache locality for a slice this small.
func isSubsetOf(small, big cnf.Clause) bool {
	for _, lit := range small {
		if !slices.Contains(big, lit) {
			return false
		}
	}
	return true
}

// subsumptionWorkBudgetFactor bounds the total number of
// subsumer/candidate pairs eliminateSubsumedClauses/
// eliminateSubsumedClausesParallel will ever examine, as a multiple of
// the clause count -- see eliminateSubsumedClauses's own doc comment
// for why this exists (STAGE30.md/REPORT30.md) and what it trades
// away. Chosen generously (most real instances' occurrence lists are
// far smaller than this) but small enough to guarantee the whole
// function is O(clauses) in the worst case: even a literal appearing
// in every single clause can only ever contribute this many candidate
// checks in total before the budget runs out.
const subsumptionWorkBudgetFactor = 64

// eliminateSubsumedClauses removes every clause that is subsumed by
// some other (shorter or equal-length) clause in clauses: if clause A
// is a subset of clause B, then A already enforces at least as much
// as B does, making B redundant. It returns how many clauses were
// removed. numVars must be at least as large as the largest variable
// number appearing in clauses (Run always passes the problem's own
// NumVars).
//
// STAGE30.md/REPORT30.md: this used to check every one of the
// O(clauses^2) ordered pairs directly (REPORT25.md's own
// multithreaded version of that same all-pairs loop). REPORT29.md
// found that quadratic cost is a severe, widely-triggered problem on
// real SAT Competition instances -- not just the handful of largest
// files REPORT25.md had seen -- so this stage replaces the all-pairs
// scan with the standard technique real preprocessors use (SatELite,
// MiniSat): for clause A to subsume any clause B, every literal of A,
// including whichever one of A's own literals occurs in the *fewest*
// clauses overall, must appear in B -- so B can only ever be found in
// that one literal's occurrence list, never anywhere else. Checking
// only that list instead of every other clause in the formula turns
// the common case from O(clauses) candidates per clause into O(how
// often A's rarest literal actually recurs), which for most real CNF
// instances (each variable appearing in a bounded number of clauses)
// is close to a small constant -- i.e. close to linear overall, not
// quadratic. This is an exact restriction, not an approximation: it
// can never miss a real subsumption, since any B that A subsumes is
// *guaranteed* to be in that occurrence list by definition.
//
// This does not change the worst-case complexity class in general --
// a literal that occurs in every clause (or a formula constructed
// adversarially so every clause's rarest literal still has a huge
// occurrence list) still makes this O(clauses^2), and there is no
// known algorithm that avoids that in the worst case; see
// REPORT30.md's discussion of the Orthogonal Vectors problem for why
// this project believes (without proving) that no such algorithm
// exists. subsumptionWorkBudgetFactor is this function's answer to
// that: a hard cap on total candidate-pair work, expressed as a
// multiple of the clause count, so a pathological or merely very
// dense formula degrades to "less subsumption found" rather than
// "this function's running time is unbounded." Running out of budget
// is always safe: skipping a possible subsumption never changes
// whether the formula is satisfiable, only how compact it ends up.
func eliminateSubsumedClauses(clauses *[]cnf.Clause, numVars int) (numRemoved int) {
	cs := *clauses
	occ := buildLiteralOccurrenceLists(cs, numVars)
	keep := make([]bool, len(cs))
	for i := range cs {
		keep[i] = true
	}

	budget := subsumptionWorkBudgetFactor * len(cs)
	trySubsumeFromGeneric(cs, occ, len(cs),
		func(i int) bool { return keep[i] },
		func(i int) { keep[i] = false },
		0, len(cs), &budget)

	kept := cs[:0]
	for i, clause := range cs {
		if keep[i] {
			kept = append(kept, clause)
		} else {
			numRemoved++
		}
	}
	*clauses = kept
	return numRemoved
}

// literalOccurrence maps each literal (by variable and sign, the same
// convention eliminateVariables' positive/negative arrays use) to the
// indices of every clause in which it appears.
type literalOccurrence struct {
	positive [][]int // positive[v] = indices of clauses containing +v
	negative [][]int // negative[v] = indices of clauses containing -v
}

// buildLiteralOccurrenceLists computes, once, the clause indices each
// literal appears in -- shared, read-only input for every subsumer
// clause's candidate search below, so it only ever needs building a
// single time regardless of how many threads
// eliminateSubsumedClausesParallel uses.
func buildLiteralOccurrenceLists(cs []cnf.Clause, numVars int) *literalOccurrence {
	occ := &literalOccurrence{
		positive: make([][]int, numVars+1),
		negative: make([][]int, numVars+1),
	}
	for i, clause := range cs {
		for _, lit := range clause {
			if lit.IsNegative() {
				occ.negative[lit.Var()] = append(occ.negative[lit.Var()], i)
			} else {
				occ.positive[lit.Var()] = append(occ.positive[lit.Var()], i)
			}
		}
	}
	return occ
}

// of returns the occurrence list for lit specifically (as opposed to
// its variable's other polarity).
func (occ *literalOccurrence) of(lit cnf.Literal) []int {
	if lit.IsNegative() {
		return occ.negative[lit.Var()]
	}
	return occ.positive[lit.Var()]
}

// rarestLiteralOccurrences returns the occurrence list of whichever
// literal in clause appears in the fewest clauses overall -- the
// smallest set that is guaranteed to contain every clause
// eliminateSubsumedClauses's caller could possibly subsume via clause
// (see eliminateSubsumedClauses's own doc comment for why that
// guarantee holds). ok is false only for
// a genuinely empty clause, which (by construction -- Run always runs
// UnitPropagate, which detects an empty clause as UNSAT and returns
// immediately, before any subsumption pass ever sees the clause set)
// should never actually reach this function in practice.
func rarestLiteralOccurrences(occ *literalOccurrence, clause cnf.Clause) (list []int, ok bool) {
	if len(clause) == 0 {
		return nil, false
	}
	best := occ.of(clause[0])
	for _, lit := range clause[1:] {
		if candidate := occ.of(lit); len(candidate) < len(best) {
			best = candidate
		}
	}
	return best, true
}

// trySubsumeFromGeneric is eliminateSubsumedClauses'/
// eliminateSubsumedClausesParallel's shared core: for every subsumer
// index i in [lo, hi), look only at the candidates
// rarestLiteralOccurrences says could possibly be subsumed by cs[i],
// and mark each genuine subset removed via markRemoved. *budget is
// decremented by the number of candidates actually examined, and
// processing stops early, leaving every remaining clause's keep bit
// untouched, the moment it runs out -- see eliminateSubsumedClauses's
// doc comment for why that is always a safe (if possibly less
// thorough) outcome.
//
// isKept/markRemoved read and write the caller's "keep" bits, rather
// than this function taking a keep slice directly, so this one
// implementation serves both eliminateSubsumedClauses (plain []bool,
// no concurrency at all) and eliminateSubsumedClausesParallel
// ([]atomic.Bool, shared across goroutines) without duplicating this
// loop for each.
func trySubsumeFromGeneric(cs []cnf.Clause, occ *literalOccurrence, n int, isKept func(int) bool, markRemoved func(int), lo, hi int, budget *int) {
	for i := lo; i < hi; i++ {
		if *budget <= 0 {
			return
		}
		candidates, ok := rarestLiteralOccurrences(occ, cs[i])
		if !ok {
			continue
		}
		*budget -= len(candidates)
		for _, j := range candidates {
			if j == i || j >= n || !isKept(j) {
				continue
			}
			if len(cs[i]) > len(cs[j]) {
				continue
			}
			if len(cs[i]) == len(cs[j]) && i > j {
				// Equal-length duplicates: keep only the first one seen.
				continue
			}
			if isSubsetOf(cs[i], cs[j]) {
				markRemoved(j)
			}
		}
	}
}

// eliminateSubsumedClausesParallel computes exactly the same result as
// eliminateSubsumedClauses (the set of clauses removed, and the
// surviving clauses in their original relative order) whenever both
// are given the same, unexhausted work budget, but splits the outer
// subsumer-clause loop across numThreads goroutines. numThreads <= 1
// just calls eliminateSubsumedClauses directly.
//
// STAGE25.md/REPORT25.md: profiling found subsumption elimination is
// the single largest cost in preprocessing on real, clause-count-heavy
// benchmark instances (87-94% of total preprocessing time on
// benchmark/blocksworld/bw_large.c.cnf/.d.cnf) -- its (worst-case)
// O(clauses^2) pairwise comparison is the natural target for
// --num-threads, far more than eliminateVariables (BVE), which
// REPORT22.md had guessed was the bottleneck (see Run's doc comment
// for why BVE itself is not also threaded).
//
// The key fact that makes this a safe, provably-equivalent
// parallelization, not just an approximation, is unchanged from
// REPORT25.md's original version of this function (see its own
// historical discussion, preserved in version control, for the full
// transitivity argument): eliminateSubsumedClauses's own
// "if !keep[i] { continue }" skip is a pure optimization, never
// required for correctness, so every goroutine's chunk of subsumer
// indices can run against the same read-only clauses/occurrence-list
// data with no coordination beyond keep's atomic writes. Restricting
// each subsumer's candidates to its rarest literal's occurrence list
// (this stage's change) does not affect that argument at all: it is an
// exact restriction (see eliminateSubsumedClauses's doc comment), so
// it changes *which pairs are ever compared*, never *what the
// comparison would have found* had it been made.
//
// The work budget is split evenly across threads (budget/numThreads
// each) rather than shared through one atomic counter: a shared
// counter would need its own synchronization (and associated
// contention) for a value that only exists to bound worst-case time in
// the first place, which would be a strange thing to spend
// synchronization overhead on. Splitting it evenly still bounds total
// work at exactly the same budget, just distributed rather than
// pooled, which only matters for exactly how much subsumption gets
// found in the (rare, already-degraded) case where the budget actually
// runs out.
func eliminateSubsumedClausesParallel(clauses *[]cnf.Clause, numVars int, numThreads int) (numRemoved int) {
	if numThreads <= 1 {
		return eliminateSubsumedClauses(clauses, numVars)
	}

	cs := *clauses
	n := len(cs)
	occ := buildLiteralOccurrenceLists(cs, numVars)
	keep := make([]atomic.Bool, n)
	for i := range keep {
		keep[i].Store(true)
	}

	perThreadBudget := (subsumptionWorkBudgetFactor * n) / numThreads

	chunk := (n + numThreads - 1) / numThreads
	var wg sync.WaitGroup
	for start := 0; start < n; start += chunk {
		end := min(start+chunk, n)
		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			budget := perThreadBudget
			trySubsumeFromGeneric(cs, occ, n,
				func(i int) bool { return keep[i].Load() },
				func(i int) { keep[i].Store(false) },
				start, end, &budget)
		}(start, end)
	}
	wg.Wait()

	kept := cs[:0]
	for i, clause := range cs {
		if keep[i].Load() {
			kept = append(kept, clause)
		} else {
			numRemoved++
		}
	}
	*clauses = kept
	return numRemoved
}

// resolve computes the resolvent of clauses pos and neg on variable
// v (which pos contains positively and neg contains negatively),
// omitting the literal on v itself. ok is false if the resolvent
// would be a tautology (containing both some literal and its
// negation), in which case it is always true and carries no
// information, so it is discarded rather than returned.
func resolve(pos, neg cnf.Clause, v int) (resolvent cnf.Clause, ok bool) {
	// STAGE25.md: seen was a map[cnf.Literal]bool; a resolvent clause
	// is typically just as short as any other clause in this project,
	// so (per isSubsetOf's doc comment above) a plain slice scan beats
	// a map here too, and this also drops a per-call map allocation
	// that resolve paid on every single one of its (often numerous:
	// pos-occurrences x neg-occurrences per candidate variable) calls.
	//
	// STAGE31.md: this O(clause length^2) version (each of up to
	// len(pos)+len(neg) literals does an O(seen) slices.Contains scan,
	// twice) is kept only as eliminateVariables's test oracle now --
	// REPORT31.md's profiling found it was 74% of one real file's
	// entire preprocessing time by itself, called by the thousands for
	// a single high-degree variable. resolveWithMarks below computes
	// the identical result in O(clause length) using a reusable
	// mark array instead of repeated linear scans; eliminateVariables
	// uses that version exclusively.
	seen := make(cnf.Clause, 0, len(pos)+len(neg))
	add := func(lit cnf.Literal) bool {
		if lit.Var() == v {
			return true
		}
		if slices.Contains(seen, -lit) {
			return false // tautology: lit and -lit both present
		}
		if !slices.Contains(seen, lit) {
			seen = append(seen, lit)
			resolvent = append(resolvent, lit)
		}
		return true
	}
	for _, lit := range pos {
		if !add(lit) {
			return nil, false
		}
	}
	for _, lit := range neg {
		if !add(lit) {
			return nil, false
		}
	}
	return resolvent, true
}

// resolveWithMarks computes exactly the same result as resolve (the
// resolvent of pos and neg on variable v, or ok=false if it would be
// a tautology), but in O(len(pos)+len(neg)) instead of resolve's
// O((len(pos)+len(neg))^2): mark[w] records whether variable w has
// been seen in this call's resolvent so far, and if so with which
// sign (+1, -1, or 0 for not yet seen), replacing resolve's repeated
// slices.Contains scans with a single O(1) array lookup per literal.
//
// mark must be sized numVars+1 and start (and end, which this
// function guarantees by cleaning up after itself via *touched)
// entirely zeroed, so the same mark/touched pair can be reused across
// every call in a single eliminateVariables run without reallocating
// or re-zeroing an O(numVars) array each time -- only the entries
// this call actually touches are reset, in *touched's O(resolvent
// length) cleanup pass at the end.
func resolveWithMarks(pos, neg cnf.Clause, v int, mark []int8, touched *[]int) (resolvent cnf.Clause, ok bool) {
	ts := (*touched)[:0]
	reset := func() {
		for _, w := range ts {
			mark[w] = 0
		}
		*touched = ts[:0]
	}

	consider := func(lit cnf.Literal) bool {
		w := lit.Var()
		if w == v {
			return true
		}
		sign := int8(1)
		if lit.IsNegative() {
			sign = -1
		}
		switch mark[w] {
		case sign:
			return true // duplicate literal, already in resolvent
		case -sign:
			return false // tautology: lit and -lit both present
		default:
			mark[w] = sign
			ts = append(ts, w)
			resolvent = append(resolvent, lit)
			return true
		}
	}

	for _, lit := range pos {
		if !consider(lit) {
			reset()
			return nil, false
		}
	}
	for _, lit := range neg {
		if !consider(lit) {
			reset()
			return nil, false
		}
	}
	reset()
	return resolvent, true
}

// compactLiveOccurrences filters list in place, dropping any index no
// longer live (STAGE31.md: eliminateVariables's occurrence lists are
// maintained incrementally -- entries are tombstoned via live, not
// physically removed from the list, when the clause they refer to is
// replaced -- so a variable's occurrence list must be compacted like
// this immediately before it is used, to skip stale indices left
// over from an earlier elimination). Reuses list's own backing array
// (the write cursor never exceeds the read cursor), so this never
// allocates.
func compactLiveOccurrences(list []int, live []bool) []int {
	out := list[:0]
	for _, idx := range list {
		if live[idx] {
			out = append(out, idx)
		}
	}
	return out
}

// eliminateVariables applies bounded variable elimination (the
// NiVER rule: Subbarayan & Pradhan, SAT 2004): a variable v is
// eliminated by resolving every clause containing +v against every
// clause containing -v, replacing all of them with the (non-
// tautological) resolvents, but only when doing so does not increase
// the number of clauses -- i.e. only when it cannot make the formula
// larger. Eliminated variables are recorded (with the clauses they
// were eliminated from, for later reconstruction) and returned.
//
// Variables that are unassigned but do not appear with both
// polarities (pure literals) or do not appear at all are left alone
// here; eliminatePureLiterals and Run's outer fixpoint loop handle
// those cases.
//
// STAGE31.md: before this stage, every single elimination rebuilt
// both occurrence-list arrays and rescanned the entire clause set
// from scratch (both O(clauses)) before looking for the next one --
// REPORT31.md's profiling found this was the dominant cost on some
// real SAT Competition files, to the point of not finishing at all
// within any reasonable time. This version builds the occurrence
// lists once and maintains them incrementally: a clause is never
// physically removed (that would require renumbering every later
// index, right back to an O(clauses) cost), only tombstoned in live;
// a variable's occurrence list is compacted -- lazily, only when that
// variable is next considered, and reusing its own backing array, so
// this never costs more in total than the number of tombstones ever
// created -- immediately before use via compactLiveOccurrences.
// Newly created resolvents are appended to the end of clauses (never
// inserted or reordered), and their literals' occurrence lists get
// exactly the new append each needs, in O(resolvent length).
//
// The elimination order, and therefore the exact sequence of Steps
// and the exact final surviving clauses, is unchanged from before
// this stage: this still restarts its scan from v=1 after every
// single elimination (mirroring the pre-STAGE31.md control flow
// exactly), which is what lets TestEliminateVariablesMatchesBruteForceOnRandomFormulas
// assert byte-for-byte equality against bruteForceEliminateVariables
// (a kept-for-testing copy of the pre-STAGE31.md algorithm) rather
// than only a weaker "still satisfiability-preserving" property. What
// changed is entirely internal bookkeeping, never what gets
// eliminated or in what order -- up to bveWorkBudgetFactor's cap
// below, which is new this stage and can change the outcome (always
// towards "less eliminated", never towards incorrectness) once
// tripped.
//
// bveWorkBudgetFactor sizes the total number of resolveWithMarks
// calls Run allows bounded variable elimination across a *whole*
// preprocessing run (every round, not just one call to
// eliminateVariables -- see budget's doc comment on the parameter
// below for why that distinction matters), as a multiple of the
// original clause count. The same kind of safety net
// STAGE30.md/REPORT30.md added for subsumption elimination, once that
// technique's own occurrence-list rewrite stopped being the dominant
// cost and exposed BVE as the next one (REPORT31.md). Even with the
// algorithmic fixes above, a single variable that genuinely occurs in
// thousands of clauses on both polarities still costs
// O(occurrences^2) resolveWithMarks calls if its elimination is
// ultimately accepted (the early-exit above only helps the *rejected*
// case) -- REPORT31.md's profiling measured real, legitimate files
// needing up to ~994x their clause count in resolve calls to complete
// BVE fully, and a genuinely pathological one exceeding 2845x (and
// still climbing) without this cap. 2000 sits comfortably above every
// legitimate value measured while cutting off runaway growth well
// before it becomes a multi-second, let alone unbounded, cost.
// Running out of budget is always safe, for the same reason it was
// for subsumption: abandoning an in-progress or not-yet-attempted
// elimination never changes whether the formula is satisfiable, only
// how compact it ends up.
const bveWorkBudgetFactor = 2000

// eliminateVariables applies bounded variable elimination once,
// consuming from *budget as it goes. budget is a pointer, not a
// value, because Run calls this once per preprocessing round: a
// budget freshly reset to bveWorkBudgetFactor*len(clauses) on every
// call would let a single variable whose exploration alone exceeds
// the whole budget get retried from scratch, with a brand new budget,
// every single round -- up to maxRounds times -- since exhausting the
// budget partway through examining v neither eliminates v (so it
// stays a candidate) nor marks it rejected (so nothing else remembers
// to leave it alone next round either. A caller-owned budget shared
// across every round closes that hole: once it reaches zero, every
// later call in the same Run bails out on its very first candidate
// pair, for the cost of a handful of array accesses, rather than
// re-attempting the same expensive variable from zero each time.
func eliminateVariables(clauses *[]cnf.Clause, assignment assign.Assignment, numVars int, budget *int) []EliminationStep {
	var steps []EliminationStep

	cs := *clauses
	live := make([]bool, len(cs))
	for i := range live {
		live[i] = true
	}

	positive := make([][]int, numVars+1) // positive[v] = indices of clauses containing +v
	negative := make([][]int, numVars+1)
	for i, clause := range cs {
		for _, lit := range clause {
			if lit.IsNegative() {
				negative[lit.Var()] = append(negative[lit.Var()], i)
			} else {
				positive[lit.Var()] = append(positive[lit.Var()], i)
			}
		}
	}

	// Reused across every resolveWithMarks call in this run; see that
	// function's doc comment for why this avoids an O(numVars)
	// allocate-and-zero on every single resolution.
	mark := make([]int8, numVars+1)
	var touched []int

	for {
		eliminatedThisPass := false
		budgetExhausted := false
		for v := 1; v <= numVars; v++ {
			if assignment[v] != assign.Unassigned {
				continue
			}
			positive[v] = compactLiveOccurrences(positive[v], live)
			negative[v] = compactLiveOccurrences(negative[v], live)
			posIdx, negIdx := positive[v], negative[v]
			if len(posIdx) == 0 || len(negIdx) == 0 {
				continue // not eliminable here: pure or absent
			}

			removedCount := len(posIdx) + len(negIdx)
			var resolvents []cnf.Clause
			exceeded := false
		resolveLoop:
			for _, pi := range posIdx {
				for _, ni := range negIdx {
					if *budget <= 0 {
						budgetExhausted = true
						break resolveLoop
					}
					*budget--
					resolvent, ok := resolveWithMarks(cs[pi], cs[ni], v, mark, &touched)
					if !ok {
						continue
					}
					resolvents = append(resolvents, resolvent)
					if len(resolvents) > removedCount {
						// STAGE31.md: bail out as soon as the
						// non-increasing check below is guaranteed to
						// reject v, instead of computing every
						// remaining resolvent (REPORT31.md's
						// profiling found resolve calls for
						// ultimately-rejected high-degree variables
						// were a large share of total preprocessing
						// time). Once this count is exceeded it can
						// only grow, never shrink, so the eventual
						// decision below is already determined.
						exceeded = true
						break resolveLoop
					}
				}
			}
			if budgetExhausted {
				// Not enough budget left to even fully evaluate this
				// variable, let alone any variable after it -- stop
				// attempting further eliminations entirely rather
				// than accept v based on an incomplete resolvent set.
				break
			}
			if exceeded {
				continue // would increase the clause count; not worth it
			}

			step := EliminationStep{Var: v}
			for _, pi := range posIdx {
				step.Positive = append(step.Positive, cs[pi])
			}
			for _, ni := range negIdx {
				step.Negative = append(step.Negative, cs[ni])
			}
			steps = append(steps, step)

			for _, idx := range posIdx {
				live[idx] = false
			}
			for _, idx := range negIdx {
				live[idx] = false
			}
			for _, resolvent := range resolvents {
				newIdx := len(cs)
				cs = append(cs, resolvent)
				live = append(live, true)
				for _, lit := range resolvent {
					if lit.IsNegative() {
						negative[lit.Var()] = append(negative[lit.Var()], newIdx)
					} else {
						positive[lit.Var()] = append(positive[lit.Var()], newIdx)
					}
				}
			}

			eliminatedThisPass = true
			break // restart from v=1, exactly as before STAGE31.md
		}

		if budgetExhausted || !eliminatedThisPass {
			break
		}
	}

	final := make([]cnf.Clause, 0, len(cs))
	for i, clause := range cs {
		if live[i] {
			final = append(final, clause)
		}
	}
	*clauses = final
	return steps
}
