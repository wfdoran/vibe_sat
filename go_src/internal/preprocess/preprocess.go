// Package preprocess implements the Stage 8 preprocessing pipeline
// shared by every solving algorithm ("hc", "ws", and "dfs"): unit
// propagation, pure literal elimination, subsumption elimination, and
// bounded (NiVER-style) variable elimination, iterated together to a
// fixpoint. The result is a simplified, renumbered problem with
// (typically) fewer variables, clauses, and literals than the
// original, plus enough bookkeeping to reconstruct a full solution to
// the *original* problem from a solution to the simplified one.
//
// Reference: Eén & Biere, "Effective Preprocessing in SAT Through
// Variable and Clause Elimination" (SatELite), SAT 2005; Subbarayan &
// Pradhan, "NiVER: Non-Increasing Variable Elimination Resolution for
// Preprocessing SAT Instances," SAT 2004 (the specific, simpler
// elimination bound used here).
package preprocess

import (
	"fmt"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
)

// maxRounds bounds how many times the full technique pipeline
// (unit propagation, pure literals, subsumption, variable
// elimination) is repeated. Each round that changes anything makes
// the formula strictly smaller in some way, so in practice this
// converges in far fewer rounds; the bound is just a safety net.
const maxRounds = 1000

// EliminationStep records enough information to reconstruct the
// value of one variable removed by bounded variable elimination,
// once every other variable's value is known. Positive and Negative
// are every clause (in the *original* variable numbering) that
// mentioned Var, at the moment Var was eliminated.
type EliminationStep struct {
	Var      int
	Positive []cnf.Clause
	Negative []cnf.Clause
}

// Result describes the outcome of preprocessing a Problem.
type Result struct {
	// Problem is the simplified, renumbered problem to hand to a
	// solving algorithm. Its variables are numbered 1..Problem.NumVars
	// and do not correspond directly to the original problem's
	// variable numbers; see NewToOriginal.
	Problem *cnf.Problem

	// OriginalNumVars is the variable count of the problem given to
	// Run, before any simplification.
	OriginalNumVars int

	// NewToOriginal maps a variable number in Problem to the
	// original variable number it came from: NewToOriginal[v] is the
	// original variable corresponding to Problem's variable v, for v
	// from 1 to Problem.NumVars. Index 0 is unused.
	NewToOriginal []int

	// FixedAssignment holds, in original variable numbering, every
	// value pinned directly by unit propagation or pure literal
	// elimination (as opposed to bounded variable elimination, which
	// is recorded in Eliminated instead). Sized OriginalNumVars+1.
	FixedAssignment assign.Assignment

	// Eliminated lists every variable removed by bounded variable
	// elimination, in the order they were eliminated (Reconstruct
	// processes this in reverse).
	Eliminated []EliminationStep

	// Unsat is true if preprocessing alone already proved the
	// original problem unsatisfiable (independent of whichever
	// solving algorithm was requested).
	Unsat bool

	// Stats summarizes what preprocessing did, for verbose reporting.
	Stats Stats
}

// Stats summarizes the effect of one call to Run, for the "preprocess:"
// announcement printed at verbose level 1.
type Stats struct {
	ClausesBefore       int
	ClausesAfter        int
	VarsBefore          int
	VarsAfter           int
	UnitsPropagated     int
	PureLiteralsFixed   int
	ClausesSubsumed     int
	VariablesEliminated int
}

// Run preprocesses problem: it repeatedly applies unit propagation,
// pure literal elimination, subsumption elimination, and bounded
// variable elimination until none of them can simplify the formula
// any further (or maxRounds is reached, as a safety bound), then
// renumbers the surviving variables into a compact range starting at
// 1. If verbose >= 1, a summary line is printed reporting how much
// the formula shrank.
//
// If preprocessing alone discovers a contradiction, Result.Unsat is
// true and Result.Problem is nil; the caller should report the
// original problem as unsatisfiable without running any solving
// algorithm at all.
//
// STAGE25.md: numThreads uses this many goroutines to speed up
// subsumption elimination, the technique profiling found dominates
// preprocessing cost on real, clause-count-heavy instances (see
// eliminateSubsumedClausesParallel's doc comment; REPORT25.md has the
// full measurement). numThreads <= 1 runs the exact same single-
// threaded code path as before Stage 25 (bit-for-bit identical
// output, per the numThreads<=1-delegates-to-the-original-function
// convention this project has used for every other algorithm's own
// --num-threads support since Stage 17). Every other technique here
// (unit propagation, pure literal elimination, and bounded variable
// elimination) remains single-threaded regardless of numThreads; see
// REPORT25.md for why BVE in particular was not also threaded this
// stage.
func Run(problem *cnf.Problem, verbose int, numThreads int) *Result {
	clauses := append([]cnf.Clause(nil), problem.Clauses...)
	assignment := assign.New(problem.NumVars)
	var eliminated []EliminationStep
	stats := Stats{ClausesBefore: len(problem.Clauses), VarsBefore: problem.NumVars}

	for round := 0; round < maxRounds; round++ {
		changed := false

		unsat, unitsFixed := UnitPropagate(&clauses, assignment)
		stats.UnitsPropagated += unitsFixed
		if unsat {
			return &Result{OriginalNumVars: problem.NumVars, Unsat: true, Stats: stats}
		}
		if unitsFixed > 0 {
			changed = true
		}

		if pureFixed := eliminatePureLiterals(&clauses, assignment); pureFixed > 0 {
			stats.PureLiteralsFixed += pureFixed
			changed = true
		}

		if subsumed := eliminateSubsumedClausesParallel(&clauses, numThreads); subsumed > 0 {
			stats.ClausesSubsumed += subsumed
			changed = true
		}

		newSteps := eliminateVariables(&clauses, assignment, problem.NumVars)
		if len(newSteps) > 0 {
			eliminated = append(eliminated, newSteps...)
			stats.VariablesEliminated += len(newSteps)
			changed = true
		}

		if !changed {
			break
		}
	}

	newClauses, newToOriginal := renumber(clauses, problem.NumVars)
	reduced := &cnf.Problem{NumVars: len(newToOriginal) - 1, Clauses: newClauses}
	stats.ClausesAfter = len(newClauses)
	stats.VarsAfter = reduced.NumVars

	if verbose >= 1 {
		fmt.Printf(
			"preprocess: vars %d->%d clauses %d->%d (units=%d pure=%d subsumed=%d eliminated=%d)\n",
			stats.VarsBefore, stats.VarsAfter, stats.ClausesBefore, stats.ClausesAfter,
			stats.UnitsPropagated, stats.PureLiteralsFixed, stats.ClausesSubsumed, stats.VariablesEliminated,
		)
	}

	return &Result{
		Problem:         reduced,
		OriginalNumVars: problem.NumVars,
		NewToOriginal:   newToOriginal,
		FixedAssignment: assignment,
		Eliminated:      eliminated,
		Stats:           stats,
	}
}

// renumber compacts the surviving variables of clauses (those that
// still appear in at least one clause) into a dense range starting at
// 1, preserving their relative order of first appearance. It returns
// the renumbered clauses and a slice mapping each new variable number
// back to the original one it came from (index 0 unused).
func renumber(clauses []cnf.Clause, originalNumVars int) (newClauses []cnf.Clause, newToOriginal []int) {
	originalToNew := make([]int, originalNumVars+1)
	newToOriginal = []int{0}

	for _, clause := range clauses {
		for _, lit := range clause {
			v := lit.Var()
			if originalToNew[v] == 0 {
				originalToNew[v] = len(newToOriginal)
				newToOriginal = append(newToOriginal, v)
			}
		}
	}

	newClauses = make([]cnf.Clause, len(clauses))
	for i, clause := range clauses {
		newClause := make(cnf.Clause, len(clause))
		for j, lit := range clause {
			newVar := cnf.Literal(originalToNew[lit.Var()])
			if lit.IsNegative() {
				newClause[j] = -newVar
			} else {
				newClause[j] = newVar
			}
		}
		newClauses[i] = newClause
	}
	return newClauses, newToOriginal
}

// Reconstruct takes solution, a complete assignment produced by a
// solving algorithm over Result.Problem's (renumbered) variables, and
// returns a complete assignment over the *original* problem's
// variables: every surviving variable's value is carried over
// directly, every variable pinned by unit propagation/pure literal
// elimination is set to its fixed value, and every variable removed
// by bounded variable elimination is reconstructed in reverse
// elimination order.
//
// Before that reverse-order pass runs, every variable that is still
// Unassigned *and* is not itself the subject of some elimination step
// is pinned to False. This covers two cases: variables that never
// appeared in any clause at all (unconstrained from the start), and
// variables that disappeared without their own elimination step
// because every resolvent mentioning them turned out to be a
// tautology (their last remaining constraint vanished as a side
// effect of eliminating some other variable). Either way such a
// variable is free to fix arbitrarily, but it must be fixed *before*
// the reverse-order pass below, since an earlier-eliminated variable's
// reconstruction (processed later, in reverse) may need to examine
// its value.
func (r *Result) Reconstruct(solution assign.Assignment) assign.Assignment {
	full := assign.New(r.OriginalNumVars)

	for newVar := 1; newVar < len(r.NewToOriginal); newVar++ {
		full[r.NewToOriginal[newVar]] = solution[newVar]
	}
	for v, value := range r.FixedAssignment {
		if value != assign.Unassigned {
			full[v] = value
		}
	}

	isEliminated := make([]bool, r.OriginalNumVars+1)
	for _, step := range r.Eliminated {
		isEliminated[step.Var] = true
	}
	for v := 1; v <= r.OriginalNumVars; v++ {
		if full[v] == assign.Unassigned && !isEliminated[v] {
			full[v] = assign.False
		}
	}

	for i := len(r.Eliminated) - 1; i >= 0; i-- {
		step := r.Eliminated[i]
		full[step.Var] = reconstructEliminatedVar(step, full)
	}
	return full
}

// reconstructEliminatedVar picks a value for step.Var, given that
// full already holds a value for every other variable step's clauses
// reference. full[step.Var] is still Unassigned at this point, which
// (by assign.Assignment.LiteralIsTrue's definition) makes any literal
// of step.Var evaluate to false while checking whether step.Negative
// is already satisfied by some *other* literal -- exactly the
// condition this needs to check.
//
// Setting Var to True trivially satisfies every clause in
// step.Positive (each contains the literal +Var). The only question
// is whether that breaks some clause in step.Negative; if every such
// clause is already satisfied by another literal, True is safe.
// Otherwise Var must be False -- and the correctness of variable
// elimination (every resolvent of a Positive/Negative pair was
// already satisfied when Var was eliminated) guarantees that in that
// case, every clause in step.Positive is in turn already satisfied by
// some other literal, so False is safe too.
func reconstructEliminatedVar(step EliminationStep, full assign.Assignment) assign.Value {
	for _, clause := range step.Negative {
		if !clauseSatisfied(clause, full) {
			return assign.False
		}
	}
	return assign.True
}

// clauseSatisfied reports whether at least one literal of clause is
// true under assignment.
func clauseSatisfied(clause cnf.Clause, assignment assign.Assignment) bool {
	for _, lit := range clause {
		if assignment.LiteralIsTrue(lit) {
			return true
		}
	}
	return false
}
