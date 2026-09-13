// Package dfs implements the depth-first search SAT solving algorithm
// used by vibe_sat's "dfs" algorithm: a complete DPLL-style search
// using boolean constraint propagation (BCP, see watch.go for
// STAGE9.md's watched-literal implementation) and a choice of
// variable-selection heuristics (see SelectVarVariant). Unlike the
// hill-climb-family algorithms (internal/hillclimb), this search is
// complete: if it exhausts its search space without finding a
// satisfying assignment, the problem is proven UNSAT, not merely "not
// found yet".
package dfs

import (
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/occurrence"
	"vibe_sat/internal/preprocess"
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

// SelectVarVariant identifies which SelectVar heuristic Run should
// use to pick the next branching variable.
type SelectVarVariant int

const (
	// SelectVarWeighted is the default heuristic from STAGE5.md: for
	// every not-yet-satisfied clause, every unassigned variable in it
	// earns 0.7^(n-2) (n = that clause's unassigned literal count),
	// and the highest-scoring variable is picked. It costs time
	// proportional to the total size of the formula on every single
	// node (it rescans every clause), in exchange for making a more
	// informed choice that tends to keep the search tree small.
	SelectVarWeighted SelectVarVariant = 0
	// SelectVarFast is the cheaper alternative from STAGE6.md: it
	// picks the lowest-numbered still-unassigned variable, looking at
	// no clause contents at all. This is the "static/lexicographic
	// ordering" branching rule discussed in the SAT branching
	// heuristic literature (e.g. J. Marques-Silva, "The Impact of
	// Branching Heuristics in Propositional Satisfiability
	// Algorithms," 1999) as the cheap baseline that smarter dynamic
	// heuristics are compared against: it costs at most O(NumVars)
	// per node with no clause scanning, but ignores problem structure
	// entirely, which typically grows the search tree substantially.
	SelectVarFast SelectVarVariant = 1
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

// searchNode is one entry of Run's explicit search stack: a partial
// assignment together with the watched-literal state describing it.
// Every branch gets its own independent searchNode (see
// cloneWatchState), matching STAGE5.md's original "stack of full
// assignments" design -- STAGE9.md's watched literals speed up the
// BCP call made when creating each node, not the overall search
// structure.
type searchNode struct {
	assignment assign.Assignment
	watch      *watchState
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
// variant selects which of SelectVar/SelectVarFast is used to pick
// the branching variable at every node (see SelectVarVariant).
//
// If timeLimit is non-nil, the search gives up and reports an
// inconclusive result (Satisfiable == false, TimedOut == true) once
// it is exceeded, checked only periodically (see timeCheckInterval)
// rather than after every node. rng supplies the randomness the
// selected heuristic uses to break ties, and verbose controls
// progress output: at verbose >= 1, "dfs" and the configured time
// limit (if any) are printed before searching, and "SAT", "UNSAT", or
// "UNKNOWN" (on timeout) are printed after.
func Run(problem *cnf.Problem, lists *occurrence.Lists, timeLimit *time.Duration, variant SelectVarVariant, rng *rand.Rand, verbose int) Result {
	if verbose >= 1 {
		fmt.Println("dfs:", describeParams(timeLimit, variant))
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

	// The watched-literal scheme (see watch.go) requires every clause
	// to have at least two literals: a unit clause has nothing to
	// "shed" its second watch onto. Bootstrap by unit-propagating the
	// root once, with the same routine internal/preprocess already
	// uses for exactly this purpose, before ever building watch
	// state. When --no-preprocessing is not given, Stage 8's
	// preprocessing has already done this (so this is a no-op); this
	// guarantees correctness either way.
	clauses := append([]cnf.Clause(nil), problem.Clauses...)
	rootAssignment := assign.New(problem.NumVars)
	if unsat, _ := preprocess.UnitPropagate(&clauses, rootAssignment); unsat {
		if verbose >= 1 {
			fmt.Println("UNSAT")
		}
		return Result{Satisfiable: false}
	}
	rootWatch, ok := newWatchState(clauses, rootAssignment)
	if !ok {
		// Defensive: UnitPropagate above should already rule this
		// out, since every surviving clause has at least one
		// unassigned literal (otherwise it would have been a unit
		// clause caught above, or a contradiction).
		if verbose >= 1 {
			fmt.Println("UNSAT")
		}
		return Result{Satisfiable: false}
	}

	// workingProblem wraps the (possibly bootstrap-simplified) clause
	// set for SelectVar/SelectVarFastPick, which only ever need the
	// clauses and variable count, not the original Problem value.
	workingProblem := &cnf.Problem{NumVars: problem.NumVars, Clauses: clauses}

	startTime := time.Now()
	stack := []searchNode{{assignment: rootAssignment, watch: rootWatch}}
	numNodes := 0

	for len(stack) > 0 {
		numNodes++
		if timeLimit != nil && numNodes&timeCheckInterval == 0 && time.Since(startTime) >= *timeLimit {
			if verbose >= 1 {
				fmt.Println("UNKNOWN")
			}
			return Result{Satisfiable: false, NumNodes: numNodes, TimedOut: true}
		}

		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		var i int
		if variant == SelectVarFast {
			i = SelectVarFastPick(workingProblem, node.assignment)
		} else {
			i = SelectVar(workingProblem, node.assignment, rng)
		}

		for _, v := range [2]assign.Value{assign.False, assign.True} {
			branchAssignment := append(assign.Assignment(nil), node.assignment...)
			branchAssignment[i] = v
			branchWatch := cloneWatchState(node.watch)

			switch BCP(clauses, lists, branchWatch, branchAssignment, i) {
			case Contra:
				continue
			case Done:
				if verbose >= 1 {
					fmt.Println("SAT")
				}
				return Result{Satisfiable: true, Assignment: branchAssignment, NumNodes: numNodes}
			case OK:
				stack = append(stack, searchNode{assignment: branchAssignment, watch: branchWatch})
			}
		}
	}

	if verbose >= 1 {
		fmt.Println("UNSAT")
	}
	return Result{Satisfiable: false, NumNodes: numNodes}
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

// SelectVarFastPick implements SelectVarFast (see SelectVarVariant):
// it returns the lowest-numbered variable that is still Unassigned in
// x, without examining any clause. There is nothing to break ties
// between (the choice is always unique), so unlike SelectVar this
// needs no random source.
func SelectVarFastPick(problem *cnf.Problem, x assign.Assignment) int {
	for v := 1; v <= problem.NumVars; v++ {
		if x[v] == assign.Unassigned {
			return v
		}
	}
	return -1
}

// describeParams formats the configured time limit and SelectVar
// variant for the "dfs:" announcement printed at verbose level 1.
func describeParams(timeLimit *time.Duration, variant SelectVarVariant) string {
	description := fmt.Sprintf("select_var=%d", variant)
	if timeLimit != nil {
		description += fmt.Sprintf(" time_limit_secs=%d", int(timeLimit.Seconds()))
	}
	return description
}
