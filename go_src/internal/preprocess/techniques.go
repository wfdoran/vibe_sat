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
func simplifyWithAssignment(clauses *[]cnf.Clause, assignment assign.Assignment) (unsat bool) {
	kept := (*clauses)[:0]
	for _, clause := range *clauses {
		satisfied := false
		var reduced cnf.Clause
		for _, lit := range clause {
			value := assignment[lit.Var()]
			if value == assign.Unassigned {
				reduced = append(reduced, lit)
				continue
			}
			if assignment.LiteralIsTrue(lit) {
				satisfied = true
				break
			}
		}
		if satisfied {
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

// eliminateSubsumedClauses removes every clause that is subsumed by
// some other (shorter or equal-length) clause in clauses: if clause A
// is a subset of clause B, then A already enforces at least as much
// as B does, making B redundant. It returns how many clauses were
// removed.
func eliminateSubsumedClauses(clauses *[]cnf.Clause) (numRemoved int) {
	cs := *clauses
	keep := make([]bool, len(cs))
	for i := range cs {
		keep[i] = true
	}

	for i := range cs {
		if !keep[i] {
			continue
		}
		for j := range cs {
			if i == j || !keep[j] {
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
				keep[j] = false
			}
		}
	}

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

// eliminateSubsumedClausesParallel computes exactly the same result as
// eliminateSubsumedClauses (the set of clauses removed, and the
// surviving clauses in their original relative order), but splits the
// outer i loop across numThreads goroutines. numThreads <= 1 just
// calls eliminateSubsumedClauses directly.
//
// STAGE25.md/REPORT25.md: profiling found subsumption elimination is
// the single largest cost in preprocessing on real, clause-count-heavy
// benchmark instances (87-94% of total preprocessing time on
// benchmark/blocksworld/bw_large.c.cnf/.d.cnf) -- its O(clauses^2)
// pairwise comparison is the natural target for --num-threads, far
// more than eliminateVariables (BVE), which REPORT22.md had guessed
// was the bottleneck (see Run's doc comment for why BVE itself is not
// also threaded this stage).
//
// The key fact that makes this a safe, provably-equivalent
// parallelization, not just an approximation: eliminateSubsumedClauses's
// own "if !keep[i] { continue }" skip is a pure optimization, never
// required for correctness. If some clause i is itself later found to
// be subsumed by another clause i2 (i2 is a subset of i), then i2 is
// also, by transitivity of the subset relation, a subset of anything i
// itself is a subset of -- so whatever j's i would go on to mark
// non-keep, i2 marks too, independently, when i2's own turn as an
// outer index comes around (whether that happens before or after i's
// own turn does not matter: either i2 runs first and already caught
// every such j directly, in which case i's own attempt would have been
// entirely redundant anyway, or i2 runs later, in which case i had
// already done its own full pass -- including marking every j it
// subsumes -- before anyone marked i itself non-keep). So evaluating
// every ordered pair (i, j) against the fixed input snapshot,
// regardless of any other pair's outcome, yields the exact same final
// "keep" set as the sequential, short-circuiting version. That is
// exactly what this function does: it never reads keep[i] as an
// outer-loop skip (only keep[j], purely to avoid redundant writes to
// an already-false entry -- itself just as harmless to skip or not),
// so every goroutine's chunk of i values can run against a read-only
// clauses slice with no coordination beyond keep's atomic writes.
// TestEliminateSubsumedClausesParallelMatchesSequential and
// TestEliminateSubsumedClausesParallelHandlesChainedSubsumption verify
// this holds in practice, including the exact "i2 subsumes i, i
// subsumes j" chain this argument depends on, not just in theory.
//
// keep is monotonic (true -> false, never back) and every write
// stores the same value (false) every time, so concurrent unsynchronized
// writes from different goroutines are semantically harmless -- but
// Go's memory model still requires a real synchronization primitive
// for two goroutines to safely touch the same memory location at all
// (this is exactly what go test -race checks and would catch), hence
// atomic.Bool rather than a plain []bool here.
func eliminateSubsumedClausesParallel(clauses *[]cnf.Clause, numThreads int) (numRemoved int) {
	if numThreads <= 1 {
		return eliminateSubsumedClauses(clauses)
	}

	cs := *clauses
	n := len(cs)
	keep := make([]atomic.Bool, n)
	for i := range keep {
		keep[i].Store(true)
	}

	chunk := (n + numThreads - 1) / numThreads
	var wg sync.WaitGroup
	for start := 0; start < n; start += chunk {
		end := min(start+chunk, n)
		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			for i := start; i < end; i++ {
				for j := 0; j < n; j++ {
					if i == j || !keep[j].Load() {
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
						keep[j].Store(false)
					}
				}
			}
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
func eliminateVariables(clauses *[]cnf.Clause, assignment assign.Assignment, numVars int) []EliminationStep {
	var steps []EliminationStep

	for {
		positive := make([][]int, numVars+1) // positive[v] = indices of clauses containing +v
		negative := make([][]int, numVars+1)
		for i, clause := range *clauses {
			for _, lit := range clause {
				if lit.IsNegative() {
					negative[lit.Var()] = append(negative[lit.Var()], i)
				} else {
					positive[lit.Var()] = append(positive[lit.Var()], i)
				}
			}
		}

		eliminatedThisPass := false
		for v := 1; v <= numVars; v++ {
			if assignment[v] != assign.Unassigned {
				continue
			}
			posIdx, negIdx := positive[v], negative[v]
			if len(posIdx) == 0 || len(negIdx) == 0 {
				continue // not eliminable here: pure or absent
			}

			var resolvents []cnf.Clause
			for _, pi := range posIdx {
				for _, ni := range negIdx {
					if resolvent, ok := resolve((*clauses)[pi], (*clauses)[ni], v); ok {
						resolvents = append(resolvents, resolvent)
					}
				}
			}

			removedCount := len(posIdx) + len(negIdx)
			if len(resolvents) > removedCount {
				continue // would increase the clause count; not worth it
			}

			step := EliminationStep{Var: v}
			for _, pi := range posIdx {
				step.Positive = append(step.Positive, (*clauses)[pi])
			}
			for _, ni := range negIdx {
				step.Negative = append(step.Negative, (*clauses)[ni])
			}
			steps = append(steps, step)

			// STAGE25.md: removeSet was a map[int]bool; clause indices
			// are already a dense range, so a plain boolean slice
			// (direct O(1) indexing, no hashing) is both simpler and
			// faster.
			removeSet := make([]bool, len(*clauses))
			for _, idx := range posIdx {
				removeSet[idx] = true
			}
			for _, idx := range negIdx {
				removeSet[idx] = true
			}
			var survivors []cnf.Clause
			for i, clause := range *clauses {
				if !removeSet[i] {
					survivors = append(survivors, clause)
				}
			}
			*clauses = append(survivors, resolvents...)

			eliminatedThisPass = true
			break // occurrence lists above are now stale; rebuild and retry
		}

		if !eliminatedThisPass {
			return steps
		}
	}
}
