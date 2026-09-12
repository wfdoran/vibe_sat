// Package dfs implements the depth-first search SAT solving algorithm
// used by vibe_sat's "dfs" algorithm: a complete DPLL-style search
// using boolean constraint propagation (BCP) and a weighted
// variable-selection heuristic. Unlike the hill-climb-family
// algorithms (internal/hillclimb), this search is complete: if it
// exhausts its search space without finding a satisfying assignment,
// the problem is proven UNSAT, not merely "not found yet".
package dfs

import (
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/occurrence"
)

// Status is the outcome of a single call to BCP.
type Status int

const (
	// OK means propagation finished without contradiction, but the
	// assignment is still partial.
	OK Status = iota
	// Contra means a clause can no longer be satisfied under this
	// assignment; it cannot be extended to a solution.
	Contra
	// Done means the assignment is now complete and (by construction)
	// satisfies every clause.
	Done
)

// timeCheckInterval controls how often the time limit is checked
// against the number of search nodes explored, per STAGE5.md's
// suggestion ("Maybe only when num_nodes & 0xfff == 0"): checking the
// clock on every single node would add needless overhead, since nodes
// are cheap and the clock only needs to be checked often enough to
// respond to a time limit reasonably promptly.
const timeCheckInterval = 0xfff

// Result describes the outcome of a depth-first search.
type Result struct {
	Satisfiable bool              // whether a satisfying assignment was found
	Assignment  assign.Assignment // the satisfying assignment; only meaningful if Satisfiable
	NumNodes    int               // number of search-tree nodes explored
	TimedOut    bool              // true if the search was abandoned due to the time limit, rather than exhausting the search space
}

// Run performs the depth-first search described in STAGE5.md: starting
// from the fully unassigned partial assignment, repeatedly pop a
// partial assignment from an explicit stack, pick a variable to
// branch on with SelectVar, and try setting it to each of False and
// True in turn, applying BCP after each attempt. A branch that leads
// to a contradiction is abandoned; a branch that completes the
// assignment means problem is satisfiable; a branch that is merely
// consistent but incomplete is pushed back onto the stack to be
// explored later. If the stack empties without ever completing an
// assignment, problem is proven unsatisfiable.
//
// If timeLimit is non-nil, the search gives up and reports an
// inconclusive result (Satisfiable == false, TimedOut == true) once
// it is exceeded, checked only periodically (see timeCheckInterval)
// rather than after every node. rng supplies the randomness
// SelectVar uses to break ties, and verbose controls progress output:
// at verbose >= 1, "dfs" and the configured time limit (if any) are
// printed before searching, and "SAT", "UNSAT", or "UNKNOWN" (on
// timeout) are printed after.
func Run(problem *cnf.Problem, lists *occurrence.Lists, timeLimit *time.Duration, rng *rand.Rand, verbose int) Result {
	if verbose >= 1 {
		fmt.Println("dfs:", describeParams(timeLimit))
	}

	// A problem with no variables can only contain empty clauses (no
	// literal can reference a variable beyond NumVars), each of which
	// is unsatisfiable by construction; guard this degenerate case
	// explicitly so SelectVar is never asked to choose a variable that
	// does not exist.
	if problem.NumVars == 0 {
		satisfiable := len(problem.Clauses) == 0
		if verbose >= 1 {
			fmt.Println(map[bool]string{true: "SAT", false: "UNSAT"}[satisfiable])
		}
		return Result{Satisfiable: satisfiable, Assignment: assign.New(0)}
	}

	startTime := time.Now()
	stack := []assign.Assignment{assign.New(problem.NumVars)}
	numNodes := 0

	for len(stack) > 0 {
		numNodes++
		if timeLimit != nil && numNodes&timeCheckInterval == 0 && time.Since(startTime) >= *timeLimit {
			if verbose >= 1 {
				fmt.Println("UNKNOWN")
			}
			return Result{Satisfiable: false, NumNodes: numNodes, TimedOut: true}
		}

		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		i := SelectVar(problem, x, rng)

		for _, v := range [2]assign.Value{assign.False, assign.True} {
			branch := append(assign.Assignment(nil), x...)
			branch[i] = v

			switch BCP(problem, lists, branch, i) {
			case Contra:
				continue
			case Done:
				if verbose >= 1 {
					fmt.Println("SAT")
				}
				return Result{Satisfiable: true, Assignment: branch, NumNodes: numNodes}
			case OK:
				stack = append(stack, branch)
			}
		}
	}

	if verbose >= 1 {
		fmt.Println("UNSAT")
	}
	return Result{Satisfiable: false, NumNodes: numNodes}
}

// BCP applies boolean constraint propagation to partial assignment x,
// which must already have variable i set to its just-chosen branch
// value. It repeatedly finds clauses left with exactly one unassigned,
// not-yet-satisfied literal and forces that literal true, continuing
// until propagation settles or a contradiction is found. x is
// modified in place.
func BCP(problem *cnf.Problem, lists *occurrence.Lists, x assign.Assignment, i int) Status {
	queue := []int{i}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]

		// falsified lists the clauses containing the literal of v that
		// just became false because of v's new assignment: those are
		// the only clauses whose status could have changed.
		var falsified []int
		if x[v] == assign.True {
			falsified = lists.Negative[v]
		} else {
			falsified = lists.Positive[v]
		}

		for _, c := range falsified {
			satisfied, contradiction, unitLiteral := evaluateClause(problem.Clauses[c], x)
			if contradiction {
				return Contra
			}
			if satisfied || unitLiteral == nil {
				continue
			}
			forcedVar := unitLiteral.Var()
			if unitLiteral.IsNegative() {
				x[forcedVar] = assign.False
			} else {
				x[forcedVar] = assign.True
			}
			queue = append(queue, forcedVar)
		}
	}

	for _, value := range x[1:] {
		if value == assign.Unassigned {
			return OK
		}
	}
	return Done
}

// evaluateClause examines clause under partial assignment x and
// reports: whether it is already satisfied by some literal; whether
// it is a contradiction (no unassigned literals, and none true); and,
// if it has exactly one unassigned literal and is not satisfied, that
// literal (the one BCP must now force true), or nil otherwise.
func evaluateClause(clause cnf.Clause, x assign.Assignment) (satisfied bool, contradiction bool, unitLiteral *cnf.Literal) {
	unassignedCount := 0
	var lastUnassigned cnf.Literal
	for _, lit := range clause {
		value := x[lit.Var()]
		if value == assign.Unassigned {
			unassignedCount++
			lastUnassigned = lit
			continue
		}
		if literalIsTrue(lit, value) {
			return true, false, nil
		}
	}
	switch unassignedCount {
	case 0:
		return false, true, nil
	case 1:
		return false, false, &lastUnassigned
	default:
		return false, false, nil
	}
}

// literalIsTrue reports whether lit evaluates to true when its
// variable holds value (which must not be assign.Unassigned).
func literalIsTrue(lit cnf.Literal, value assign.Value) bool {
	if lit.IsNegative() {
		return value == assign.False
	}
	return value == assign.True
}

// SelectVar chooses which unassigned variable of problem to branch on
// next, given partial assignment x, following STAGE5.md's heuristic:
// for every not-yet-satisfied clause, every currently unassigned
// variable in it earns a share of weight 0.7^(n-2), where n is the
// number of unassigned variables in that clause (n >= 2 for every
// clause reached after at least one round of BCP, since BCP would
// already have propagated or rejected any clause with fewer
// unassigned literals; the very first call, on the wholly unassigned
// root, is the only exception, and the formula still produces a
// well-defined, if unrepresentative, weight there). The variable with
// the highest total score is selected; ties are broken uniformly at
// random using rng.
func SelectVar(problem *cnf.Problem, x assign.Assignment, rng *rand.Rand) int {
	score := make([]float64, problem.NumVars+1)

	for _, clause := range problem.Clauses {
		satisfied := false
		var unassignedVars []int
		for _, lit := range clause {
			value := x[lit.Var()]
			if value == assign.Unassigned {
				unassignedVars = append(unassignedVars, lit.Var())
				continue
			}
			if literalIsTrue(lit, value) {
				satisfied = true
				break
			}
		}
		if satisfied {
			continue
		}
		weight := math.Pow(0.7, float64(len(unassignedVars)-2))
		for _, v := range unassignedVars {
			score[v] += weight
		}
	}

	bestVar := -1
	bestScore := 0.0
	tieCount := 0
	for v := 1; v <= problem.NumVars; v++ {
		if x[v] != assign.Unassigned {
			continue
		}
		switch {
		case bestVar == -1 || score[v] > bestScore:
			bestVar, bestScore, tieCount = v, score[v], 1
		case score[v] == bestScore:
			tieCount++
			// Reservoir sampling: keep the new candidate with
			// probability 1/tieCount, so every tied candidate seen so
			// far remains equally likely to be selected.
			if rng.IntN(tieCount) == 0 {
				bestVar = v
			}
		}
	}
	return bestVar
}

// describeParams formats the configured time limit for the "dfs:"
// announcement printed at verbose level 1.
func describeParams(timeLimit *time.Duration) string {
	if timeLimit == nil {
		return "(no limit)"
	}
	return fmt.Sprintf("time_limit_secs=%d", int(timeLimit.Seconds()))
}
