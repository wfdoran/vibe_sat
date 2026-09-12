// Package occurrence precomputes, for each variable of a SAT problem,
// which clauses contain that variable positively and which contain it
// negatively. Local-search solvers use this index to evaluate and
// apply the effect of flipping a single variable in time proportional
// to that variable's number of occurrences, rather than rescanning
// every clause in the formula.
package occurrence

import "vibe_sat/internal/cnf"

// Lists holds, for every variable of a problem, the indices of the
// clauses in which that variable appears positively (as a literal
// +v) and the indices of the clauses in which it appears negatively
// (as a literal -v). Both slices are indexed directly by variable
// number, 1 through NumVars; index 0 is unused.
type Lists struct {
	Positive [][]int // Positive[v] = indices of clauses containing literal +v
	Negative [][]int // Negative[v] = indices of clauses containing literal -v
}

// Build computes the occurrence Lists for problem by scanning every
// literal of every clause once.
func Build(problem *cnf.Problem) *Lists {
	lists := &Lists{
		Positive: make([][]int, problem.NumVars+1),
		Negative: make([][]int, problem.NumVars+1),
	}
	for clauseIndex, clause := range problem.Clauses {
		for _, lit := range clause {
			v := lit.Var()
			if lit.IsNegative() {
				lists.Negative[v] = append(lists.Negative[v], clauseIndex)
			} else {
				lists.Positive[v] = append(lists.Positive[v], clauseIndex)
			}
		}
	}
	return lists
}
