package preprocess

import (
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

// unitPropagate repeatedly finds a clause with exactly one unassigned
// literal and forces that literal true, removing now-satisfied
// clauses and shrinking others as it goes (via simplifyWithAssignment),
// until no clause is a unit clause or a contradiction is found. It
// returns whether a contradiction was found, and how many variables
// were newly fixed.
func unitPropagate(clauses *[]cnf.Clause, assignment assign.Assignment) (unsat bool, numFixed int) {
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

// clauseLiteralSet returns clause's literals as a set, for efficient
// subset testing.
func clauseLiteralSet(clause cnf.Clause) map[cnf.Literal]bool {
	set := make(map[cnf.Literal]bool, len(clause))
	for _, lit := range clause {
		set[lit] = true
	}
	return set
}

// isSubsetOf reports whether every literal of small appears in big's
// literal set.
func isSubsetOf(small cnf.Clause, big map[cnf.Literal]bool) bool {
	for _, lit := range small {
		if !big[lit] {
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
	sets := make([]map[cnf.Literal]bool, len(cs))
	for i, clause := range cs {
		sets[i] = clauseLiteralSet(clause)
	}

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
			if isSubsetOf(cs[i], sets[j]) {
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

// resolve computes the resolvent of clauses pos and neg on variable
// v (which pos contains positively and neg contains negatively),
// omitting the literal on v itself. ok is false if the resolvent
// would be a tautology (containing both some literal and its
// negation), in which case it is always true and carries no
// information, so it is discarded rather than returned.
func resolve(pos, neg cnf.Clause, v int) (resolvent cnf.Clause, ok bool) {
	seen := make(map[cnf.Literal]bool, len(pos)+len(neg))
	add := func(lit cnf.Literal) bool {
		if lit.Var() == v {
			return true
		}
		if seen[-lit] {
			return false // tautology: lit and -lit both present
		}
		if !seen[lit] {
			seen[lit] = true
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

			removeSet := make(map[int]bool, removedCount)
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
