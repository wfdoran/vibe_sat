// Package occurrence precomputes, for each variable of a SAT problem,
// which clauses contain that variable positively and which contain it
// negatively. Local-search solvers use this index to evaluate and
// apply the effect of flipping a single variable in time proportional
// to that variable's number of occurrences, rather than rescanning
// every clause in the formula.
package occurrence

import (
	"time"

	"vibe_sat/internal/cnf"
)

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
// literal of every clause once. Equivalent to BuildWithDeadline with
// no time limit, for every caller that doesn't need one (every caller
// except dfs.bootstrap/cdcl.newSolver's own time-bounded bootstrap
// path -- see BuildWithDeadline's doc comment).
func Build(problem *cnf.Problem) *Lists {
	lists, _ := BuildWithDeadline(problem, nil, time.Time{})
	return lists
}

// BuildWithDeadline is Build, plus a timeLimit/startTime pair (nil/zero
// meaning no limit, the same convention dfs.go/cdcl.go's own search
// loops use) checked once per clause: timedOut is true the moment the
// deadline is noticed, in which case the returned Lists is a partial,
// incomplete index that must not be used for anything.
//
// STAGE52.md: added so cdcl.newSolver's bootstrap -- which already
// builds a Lists for rephaseFromWalkSAT's benefit, see cdcl.go's own
// watchersPositive/watchersNegative doc comment for why this index is
// still needed there -- doesn't have an unbounded, uninterruptible
// single pass sitting after its own now-interruptible UnitPropagate
// and watch-selection steps (REPORT35.md's own disclosed limitation).
// Every other caller (dfs no longer needs a Lists at all since
// STAGE46.md; hillclimb, ws, and cdcl's own reduceClauseDatabase all
// call the plain, unbounded Build) is unaffected.
func BuildWithDeadline(problem *cnf.Problem, timeLimit *time.Duration, startTime time.Time) (lists *Lists, timedOut bool) {
	lists = &Lists{
		Positive: make([][]int, problem.NumVars+1),
		Negative: make([][]int, problem.NumVars+1),
	}
	for clauseIndex, clause := range problem.Clauses {
		if timeLimit != nil && time.Since(startTime) >= *timeLimit {
			return lists, true
		}
		for _, lit := range clause {
			v := lit.Var()
			if lit.IsNegative() {
				lists.Negative[v] = append(lists.Negative[v], clauseIndex)
			} else {
				lists.Positive[v] = append(lists.Positive[v], clauseIndex)
			}
		}
	}
	return lists, false
}
