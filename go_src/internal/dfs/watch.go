package dfs

import (
	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/occurrence"
)

// watchState holds, for every clause, the two literals it is
// currently watching (see Chaff: Moskewicz, Madigan, Zhao, Zhang &
// Malik, "Chaff: Engineering an Efficient SAT Solver," DAC 2001).
// BCP only ever has to look closely at a clause when one of these two
// literals becomes false, instead of every clause containing that
// literal: a clause "sheds" a watch away from an about-to-be-false
// literal onto some other not-yet-false literal whenever it can,
// which is what makes propagation cheap in the steady state.
//
// A watchState belongs to exactly one node of the search: each branch
// gets its own clone (see cloneWatchState), since sibling branches
// make different assignments and so may need different literals
// watched. This keeps the search's existing "explicit stack of
// self-contained nodes" structure from STAGE5.md intact, rather than
// requiring the single shared, incrementally backtracked trail a
// from-scratch CDCL implementation would normally use.
//
// watch[c] is always exactly 2 distinct literals, which requires
// every clause to have at least 2 literals; callers must guarantee
// this (via an initial round of unit propagation removing any unit
// or empty clauses) before calling newWatchState.
type watchState struct {
	watch [][2]cnf.Literal
}

// newWatchState builds a watchState for clauses, choosing for every
// clause two literals that are not false under assignment. ok is
// false if some clause has fewer than two such literals (a
// contradiction, given the precondition above; checked defensively
// rather than assumed).
func newWatchState(clauses []cnf.Clause, assignment assign.Assignment) (ws *watchState, ok bool) {
	watch := make([][2]cnf.Literal, len(clauses))
	for c, clause := range clauses {
		first, foundFirst := chooseWatch(clause, assignment, 0)
		if !foundFirst {
			return nil, false
		}
		second, foundSecond := chooseWatch(clause, assignment, first)
		if !foundSecond {
			return nil, false
		}
		watch[c] = [2]cnf.Literal{first, second}
	}
	return &watchState{watch: watch}, true
}

// cloneWatchState returns an independent copy of ws, for a new search
// branch to mutate without affecting its sibling.
func cloneWatchState(ws *watchState) *watchState {
	clone := make([][2]cnf.Literal, len(ws.watch))
	copy(clone, ws.watch)
	return &watchState{watch: clone}
}

// chooseWatch scans clause for a literal that is not false under
// assignment and is not equal to avoid (0 is never a valid literal,
// so passing 0 imposes no exclusion; this is used when picking a
// clause's first watch, before a second literal exists to avoid
// re-picking).
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

// BCP applies boolean constraint propagation to partial assignment x,
// which must already have variable i set to its just-chosen branch
// value, using ws's watched literals to find only the clauses that
// might need attention as a result. x and ws are both modified in
// place. lists is the static (never mutated) per-variable occurrence
// index from Stage 2, used here only to enumerate the *candidate*
// clauses to examine when a literal becomes false -- most of them are
// dismissed in O(1) because they are not currently watching that
// literal; only the ones that are get the deeper look a plain
// occurrence-list scan would have given every candidate.
func BCP(clauses []cnf.Clause, lists *occurrence.Lists, ws *watchState, x assign.Assignment, i int) Status {
	queue := []int{i}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]

		// falsifiedLiteral is the literal on v that just became false
		// because of v's new assignment; candidates lists every clause
		// that mentions it (only some of which are actually watching
		// it right now).
		var falsifiedLiteral cnf.Literal
		var candidates []int
		if x[v] == assign.True {
			falsifiedLiteral = cnf.Literal(-v)
			candidates = lists.Negative[v]
		} else {
			falsifiedLiteral = cnf.Literal(v)
			candidates = lists.Positive[v]
		}

		for _, c := range candidates {
			watch := &ws.watch[c]
			var otherWatch cnf.Literal
			switch {
			case watch[0] == falsifiedLiteral:
				otherWatch = watch[1]
			case watch[1] == falsifiedLiteral:
				otherWatch = watch[0]
			default:
				continue // this clause isn't watching the falsified literal
			}

			if replacement, found := chooseWatch(clauses[c], x, otherWatch); found {
				if watch[0] == falsifiedLiteral {
					watch[0] = replacement
				} else {
					watch[1] = replacement
				}
				continue
			}

			// No replacement: otherWatch is the clause's only literal
			// that isn't currently false.
			if isFalse(otherWatch, x) {
				return Contra
			}
			forcedVar := otherWatch.Var()
			if x[forcedVar] == assign.Unassigned {
				if otherWatch.IsNegative() {
					x[forcedVar] = assign.False
				} else {
					x[forcedVar] = assign.True
				}
				queue = append(queue, forcedVar)
			}
			// Otherwise otherWatch is already true: the clause is
			// satisfied through it, and there is nothing to do.
		}
	}

	for _, value := range x[1:] {
		if value == assign.Unassigned {
			return OK
		}
	}
	return Done
}
