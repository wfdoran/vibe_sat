// Package hillclimb implements the simplest local-search SAT solving
// method used by vibe_sat: repeatedly start from a random complete
// assignment and greedily flip single variables as long as doing so
// increases the number of satisfied clauses, restarting from a new
// random assignment whenever no single flip can improve further.
package hillclimb

import (
	"fmt"
	"math/rand/v2"
	"time"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/occurrence"
)

// Params configures a single call to Run: how many random restarts to
// attempt, how long to keep searching, or both (in which case the
// search stops as soon as either limit is reached). A nil field means
// that limit does not apply.
type Params struct {
	NumStarts *int
	TimeLimit *time.Duration
}

// Result describes the outcome of a hill-climbing run.
type Result struct {
	Satisfiable bool              // whether a fully satisfying assignment was found
	Assignment  assign.Assignment // the satisfying assignment; only meaningful if Satisfiable
	Score       int               // the score of Assignment (number of satisfied clauses)
	Starts      int               // number of random restarts performed
}

// Score returns the number of clauses in problem that are satisfied
// by assignment.
func Score(problem *cnf.Problem, assignment assign.Assignment) int {
	score := 0
	for _, clause := range problem.Clauses {
		if clauseIsSatisfied(clause, assignment) {
			score++
		}
	}
	return score
}

// clauseIsSatisfied reports whether at least one literal of clause is
// true under assignment.
func clauseIsSatisfied(clause cnf.Clause, assignment assign.Assignment) bool {
	for _, lit := range clause {
		if assignment.LiteralIsTrue(lit) {
			return true
		}
	}
	return false
}

// clauseTrueCounts computes, for every clause of problem, the number
// of its literals that are currently true under assignment, along
// with the resulting score (the number of clauses whose count is
// greater than zero, i.e. satisfied).
func clauseTrueCounts(problem *cnf.Problem, assignment assign.Assignment) (counts []int, score int) {
	counts = make([]int, len(problem.Clauses))
	for i, clause := range problem.Clauses {
		for _, lit := range clause {
			if assignment.LiteralIsTrue(lit) {
				counts[i]++
			}
		}
		if counts[i] > 0 {
			score++
		}
	}
	return counts, score
}

// climbState holds the mutable bookkeeping needed to evaluate and
// apply single-variable flips in time proportional to that variable's
// occurrence count, rather than rescanning every clause on every
// flip.
type climbState struct {
	lists      *occurrence.Lists
	assignment assign.Assignment
	counts     []int // counts[c] = number of true literals in clause c
	score      int   // number of clauses with counts[c] > 0
}

// newClimbState builds a climbState from a complete starting
// assignment, computing the initial per-clause true-literal counts
// and score from scratch.
func newClimbState(problem *cnf.Problem, lists *occurrence.Lists, assignment assign.Assignment) *climbState {
	counts, score := clauseTrueCounts(problem, assignment)
	return &climbState{lists: lists, assignment: assignment, counts: counts, score: score}
}

// flip flips the value of variable v in place, updates the per-clause
// true-literal counts and the overall score to match, and returns the
// resulting change in score. Calling flip a second time with the same
// v exactly reverses the first call, since flipping is its own
// inverse.
func (s *climbState) flip(v int) int {
	wasTrue := s.assignment[v] == assign.True

	var losing, gaining []int
	if wasTrue {
		losing, gaining = s.lists.Positive[v], s.lists.Negative[v]
	} else {
		losing, gaining = s.lists.Negative[v], s.lists.Positive[v]
	}

	delta := 0
	for _, c := range losing {
		s.counts[c]--
		if s.counts[c] == 0 {
			delta--
		}
	}
	for _, c := range gaining {
		s.counts[c]++
		if s.counts[c] == 1 {
			delta++
		}
	}

	if wasTrue {
		s.assignment[v] = assign.False
	} else {
		s.assignment[v] = assign.True
	}
	s.score += delta
	return delta
}

// sweep performs one pass over the variables in the order given by
// order, attempting to flip each one in turn. A flip that strictly
// increases the score is kept, in which case onImproved (if non-nil)
// is invoked with the new score; a flip that does not strictly
// improve the score is reverted before moving on. sweep returns true
// if at least one flip was kept during the pass.
func (s *climbState) sweep(order []int, onImproved func(newScore int)) bool {
	changed := false
	for _, v := range order {
		delta := s.flip(v)
		if delta > 0 {
			changed = true
			if onImproved != nil {
				onImproved(s.score)
			}
		} else {
			s.flip(v) // revert
		}
	}
	return changed
}

// randomPermutation returns a random permutation of the variable
// numbers 1 through numVars, ordered using rng.
func randomPermutation(numVars int, rng *rand.Rand) []int {
	order := make([]int, numVars)
	for i := range order {
		order[i] = i + 1
	}
	rng.Shuffle(numVars, func(i, j int) { order[i], order[j] = order[j], order[i] })
	return order
}

// Run performs the hill-climbing search described by STAGE2.md:
// repeatedly starting from a random complete assignment and greedily
// flipping variables until no single flip can improve the score,
// stopping as soon as a satisfying assignment is found or the limits
// in params are reached. rng supplies all the randomness used (the
// initial assignment and flip order of every start), and verbose
// controls how much progress is printed to stdout:
//
//   - verbose >= 1: prints "hillclimb" with the configured limits
//     before searching, and "SAT" or "UNKNOWN" after searching.
//   - verbose >= 2: prints the score every time it sets a new best
//     score across all starts so far.
//   - verbose >= 3: prints the score every time a start gets stuck at
//     a local optimum that does not satisfy every clause.
func Run(problem *cnf.Problem, lists *occurrence.Lists, params Params, rng *rand.Rand, verbose int) Result {
	if verbose >= 1 {
		fmt.Println("hillclimb:", describeParams(params))
	}

	startTime := time.Now()
	bestScore := -1
	recordIfBest := func(score int) {
		if score > bestScore {
			bestScore = score
			if verbose >= 2 {
				fmt.Printf("new best score: %d/%d\n", score, problem.NumClauses())
			}
		}
	}

	starts := 0
	for {
		if params.NumStarts != nil && starts >= *params.NumStarts {
			break
		}
		if params.TimeLimit != nil && time.Since(startTime) >= *params.TimeLimit {
			break
		}
		starts++

		assignment := assign.NewRandom(problem.NumVars, rng)
		state := newClimbState(problem, lists, assignment)
		recordIfBest(state.score)

		for {
			order := randomPermutation(problem.NumVars, rng)
			if !state.sweep(order, recordIfBest) {
				break
			}
		}

		if state.score == problem.NumClauses() {
			if verbose >= 1 {
				fmt.Println("SAT")
			}
			return Result{Satisfiable: true, Assignment: state.assignment, Score: state.score, Starts: starts}
		}

		if verbose >= 3 {
			fmt.Printf("stuck at score %d/%d after start %d\n", state.score, problem.NumClauses(), starts)
		}
	}

	if verbose >= 1 {
		fmt.Println("UNKNOWN")
	}
	return Result{Satisfiable: false, Starts: starts}
}

// describeParams formats the configured restart/time limits for the
// "hillclimb:" announcement printed at verbose level 1.
func describeParams(params Params) string {
	description := ""
	if params.NumStarts != nil {
		description += fmt.Sprintf("num_starts=%d ", *params.NumStarts)
	}
	if params.TimeLimit != nil {
		description += fmt.Sprintf("time_limit_secs=%d ", int(params.TimeLimit.Seconds()))
	}
	if description == "" {
		return "(no limit)"
	}
	return description[:len(description)-1]
}
