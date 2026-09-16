package preprocess

import (
	"path/filepath"
	"slices"
	"testing"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
)

// clauseSatisfiedByFull is a small test helper checking a full
// (original-numbering) clause against a full assignment.
func allClausesSatisfied(t *testing.T, problem *cnf.Problem, full assign.Assignment) {
	t.Helper()
	for i, clause := range problem.Clauses {
		if !clauseSatisfied(clause, full) {
			t.Errorf("clause %d (%v) not satisfied by %v", i, clause, full)
		}
	}
}

// TestUnitPropagateChain verifies that UnitPropagate follows a chain
// of forced unit propagations and shrinks/removes clauses as it goes.
func TestUnitPropagateChain(t *testing.T) {
	clauses := []cnf.Clause{
		{cnf.Literal(1)},
		{cnf.Literal(-1), cnf.Literal(2)},
		{cnf.Literal(-2), cnf.Literal(3)},
	}
	assignment := assign.New(3)

	unsat, numFixed := UnitPropagate(&clauses, assignment)
	if unsat {
		t.Fatalf("expected no contradiction")
	}
	if numFixed != 3 {
		t.Errorf("numFixed = %d, want 3", numFixed)
	}
	if assignment[1] != assign.True || assignment[2] != assign.True || assignment[3] != assign.True {
		t.Errorf("assignment = %v, want all True", assignment)
	}
	if len(clauses) != 0 {
		t.Errorf("clauses = %v, want all consumed", clauses)
	}
}

// TestUnitPropagateDetectsContradiction verifies that UnitPropagate
// reports unsat for a formula with conflicting unit clauses.
func TestUnitPropagateDetectsContradiction(t *testing.T) {
	clauses := []cnf.Clause{
		{cnf.Literal(1)},
		{cnf.Literal(-1)},
	}
	assignment := assign.New(1)

	unsat, _ := UnitPropagate(&clauses, assignment)
	if !unsat {
		t.Fatalf("expected unsat = true")
	}
}

// TestEliminatePureLiterals verifies that a variable appearing with
// only one polarity is fixed to satisfy all its clauses, which are
// then removed. (Variable 2 appears with both polarities here, so it
// is not pure and is left for the caller's other techniques.)
func TestEliminatePureLiterals(t *testing.T) {
	clauses := []cnf.Clause{
		{cnf.Literal(1), cnf.Literal(2)},
		{cnf.Literal(1), cnf.Literal(-2)},
	}
	assignment := assign.New(2)

	numFixed := eliminatePureLiterals(&clauses, assignment)
	if numFixed != 1 {
		t.Fatalf("numFixed = %d, want 1", numFixed)
	}
	if assignment[1] != assign.True {
		t.Errorf("assignment[1] = %v, want True (1 is pure positive)", assignment[1])
	}
	if len(clauses) != 0 {
		t.Fatalf("clauses = %v, want both clauses consumed (both satisfied by 1 = True)", clauses)
	}
}

// TestEliminateSubsumedClausesRemovesSuperset verifies that a longer
// clause subsumed by a shorter one is removed.
func TestEliminateSubsumedClausesRemovesSuperset(t *testing.T) {
	clauses := []cnf.Clause{
		{cnf.Literal(1), cnf.Literal(2)},
		{cnf.Literal(1), cnf.Literal(2), cnf.Literal(3)},
	}
	numRemoved := eliminateSubsumedClauses(&clauses)
	if numRemoved != 1 {
		t.Fatalf("numRemoved = %d, want 1", numRemoved)
	}
	if len(clauses) != 1 {
		t.Fatalf("clauses = %v, want only the 2-literal clause remaining", clauses)
	}
}

// TestEliminateSubsumedClausesRemovesDuplicates verifies that an
// exact duplicate clause is removed, keeping only one copy.
func TestEliminateSubsumedClausesRemovesDuplicates(t *testing.T) {
	clauses := []cnf.Clause{
		{cnf.Literal(1), cnf.Literal(-2)},
		{cnf.Literal(1), cnf.Literal(-2)},
	}
	numRemoved := eliminateSubsumedClauses(&clauses)
	if numRemoved != 1 {
		t.Fatalf("numRemoved = %d, want 1", numRemoved)
	}
	if len(clauses) != 1 {
		t.Fatalf("clauses = %v, want exactly one copy remaining", clauses)
	}
}

// cloneClauses makes an independent copy of clauses (and each clause
// within it), so two calls under test can each mutate their own copy
// without one affecting the other.
func cloneClauses(clauses []cnf.Clause) []cnf.Clause {
	out := make([]cnf.Clause, len(clauses))
	for i, c := range clauses {
		out[i] = append(cnf.Clause(nil), c...)
	}
	return out
}

// TestEliminateSubsumedClausesParallelMatchesSequentialChained
// exercises exactly the transitivity chain
// eliminateSubsumedClausesParallel's doc comment argues makes omitting
// the "if !keep[i] { continue }" skip safe: clause 0 ({1}) subsumes
// clause 1 ({1,2}), which is itself long enough that -- were it not
// itself about to be marked non-keep -- it would be the one to catch
// clause 2 ({1,2,4}). Since {1} is also a subset of {1,2,4} directly,
// this asserts the parallel version (at several thread counts,
// including more threads than clauses) removes exactly the same two
// clauses the sequential version does.
func TestEliminateSubsumedClausesParallelMatchesSequentialChained(t *testing.T) {
	original := []cnf.Clause{
		{cnf.Literal(1)},
		{cnf.Literal(1), cnf.Literal(2)},
		{cnf.Literal(1), cnf.Literal(2), cnf.Literal(4)},
	}

	want := cloneClauses(original)
	wantRemoved := eliminateSubsumedClauses(&want)

	for _, numThreads := range []int{1, 2, 3, 4, 8} {
		got := cloneClauses(original)
		gotRemoved := eliminateSubsumedClausesParallel(&got, numThreads)
		if gotRemoved != wantRemoved {
			t.Errorf("numThreads=%d: numRemoved = %d, want %d", numThreads, gotRemoved, wantRemoved)
		}
		if len(got) != len(want) {
			t.Fatalf("numThreads=%d: clauses = %v, want %v", numThreads, got, want)
		}
		for i := range want {
			if !slices.Equal(got[i], want[i]) {
				t.Errorf("numThreads=%d: clauses[%d] = %v, want %v", numThreads, i, got[i], want[i])
			}
		}
	}
}

// TestEliminateSubsumedClausesParallelMatchesSequentialOnRealFile runs
// both the sequential and parallel (several thread counts)
// implementations against a real benchmark file and asserts they
// remove exactly the same clauses.
func TestEliminateSubsumedClausesParallelMatchesSequentialOnRealFile(t *testing.T) {
	path := filepath.Join("..", "..", "..", "benchmark", "blocksworld", "anomaly.cnf")
	problem, err := cnf.ReadDIMACS(path, 0)
	if err != nil {
		t.Fatalf("failed to read benchmark CNF file %s: %v", path, err)
	}

	want := cloneClauses(problem.Clauses)
	wantRemoved := eliminateSubsumedClauses(&want)
	if wantRemoved == 0 {
		t.Fatal("test file has nothing to subsume; pick a different file")
	}

	for _, numThreads := range []int{1, 2, 3, 4, 8, 16} {
		got := cloneClauses(problem.Clauses)
		gotRemoved := eliminateSubsumedClausesParallel(&got, numThreads)
		if gotRemoved != wantRemoved {
			t.Errorf("numThreads=%d: numRemoved = %d, want %d", numThreads, gotRemoved, wantRemoved)
		}
		if len(got) != len(want) {
			t.Fatalf("numThreads=%d: got %d surviving clauses, want %d", numThreads, len(got), len(want))
		}
		for i := range want {
			if !slices.Equal(got[i], want[i]) {
				t.Errorf("numThreads=%d: clauses[%d] = %v, want %v", numThreads, i, got[i], want[i])
			}
		}
	}
}

// TestRunProducesIdenticalResultsRegardlessOfNumThreads confirms
// preprocessing is still fully deterministic (it always has been --
// nothing in this package uses randomness) even with Stage 25's
// multithreaded subsumption elimination active: the same input
// produces byte-for-byte identical Stats and simplified-problem output
// regardless of numThreads.
func TestRunProducesIdenticalResultsRegardlessOfNumThreads(t *testing.T) {
	path := filepath.Join("..", "..", "..", "benchmark", "blocksworld", "anomaly.cnf")
	problem, err := cnf.ReadDIMACS(path, 0)
	if err != nil {
		t.Fatalf("failed to read benchmark CNF file %s: %v", path, err)
	}

	want := Run(problem, 0, 1)
	for _, numThreads := range []int{1, 2, 4, 8, 16} {
		got := Run(problem, 0, numThreads)
		if got.Stats != want.Stats {
			t.Errorf("numThreads=%d: Stats = %+v, want %+v", numThreads, got.Stats, want.Stats)
		}
		if len(got.Problem.Clauses) != len(want.Problem.Clauses) {
			t.Fatalf("numThreads=%d: got %d clauses, want %d", numThreads, len(got.Problem.Clauses), len(want.Problem.Clauses))
		}
		for i := range want.Problem.Clauses {
			if !slices.Equal(got.Problem.Clauses[i], want.Problem.Clauses[i]) {
				t.Errorf("numThreads=%d: Problem.Clauses[%d] = %v, want %v", numThreads, i, got.Problem.Clauses[i], want.Problem.Clauses[i])
			}
		}
	}
}

// TestResolveOmitsTautology verifies that resolve reports ok = false
// when the resolvent would contain a literal and its negation.
func TestResolveOmitsTautology(t *testing.T) {
	pos := cnf.Clause{cnf.Literal(1), cnf.Literal(2)}
	neg := cnf.Clause{cnf.Literal(-1), cnf.Literal(-2)}
	_, ok := resolve(pos, neg, 1)
	if ok {
		t.Fatalf("expected a tautological resolvent to be rejected")
	}
}

// TestResolveProducesExpectedClause verifies the non-tautological
// resolution case.
func TestResolveProducesExpectedClause(t *testing.T) {
	pos := cnf.Clause{cnf.Literal(1), cnf.Literal(2)}
	neg := cnf.Clause{cnf.Literal(-1), cnf.Literal(3)}
	resolvent, ok := resolve(pos, neg, 1)
	if !ok {
		t.Fatalf("expected a valid resolvent")
	}
	got := map[cnf.Literal]bool{}
	for _, lit := range resolvent {
		got[lit] = true
	}
	want := map[cnf.Literal]bool{cnf.Literal(2): true, cnf.Literal(3): true}
	if len(got) != len(want) || !got[2] || !got[3] {
		t.Errorf("resolvent = %v, want {2, 3}", resolvent)
	}
}

// TestEliminateVariablesNonIncreasing verifies that a variable with
// one positive and one negative occurrence is eliminated (a single
// resolvent replacing two clauses is non-increasing).
func TestEliminateVariablesNonIncreasing(t *testing.T) {
	clauses := []cnf.Clause{
		{cnf.Literal(1), cnf.Literal(2)},
		{cnf.Literal(-1), cnf.Literal(3)},
	}
	assignment := assign.New(3)

	steps := eliminateVariables(&clauses, assignment, 3)
	if len(steps) != 1 || steps[0].Var != 1 {
		t.Fatalf("steps = %v, want one step eliminating variable 1", steps)
	}
	if len(clauses) != 1 {
		t.Fatalf("clauses = %v, want exactly the resolvent {2, 3}", clauses)
	}
}

// TestEliminateVariablesSkipsWhenIncreasing verifies that a variable
// is left alone when elimination would increase the clause count:
// here, 2 positive and 3 negative occurrences of variable 1 would
// produce 2*3=6 resolvents, replacing only 5 clauses.
func TestEliminateVariablesSkipsWhenIncreasing(t *testing.T) {
	clauses := []cnf.Clause{
		{cnf.Literal(1), cnf.Literal(2)},
		{cnf.Literal(1), cnf.Literal(3)},
		{cnf.Literal(-1), cnf.Literal(4)},
		{cnf.Literal(-1), cnf.Literal(5)},
		{cnf.Literal(-1), cnf.Literal(6)},
	}
	assignment := assign.New(6)

	steps := eliminateVariables(&clauses, assignment, 6)
	if len(steps) != 0 {
		t.Fatalf("steps = %v, want none (elimination would increase clause count 5 -> 6)", steps)
	}
}

// TestRunSimplifiesAndPreservesSatisfiability runs the full pipeline
// on a small satisfiable formula and verifies that a solution to the
// reduced problem, once reconstructed, satisfies every clause of the
// original problem.
func TestRunSimplifiesAndPreservesSatisfiability(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 4,
		Clauses: []cnf.Clause{
			{cnf.Literal(1)},                                 // forces 1 = True (unit)
			{cnf.Literal(-1), cnf.Literal(2)},                // forces 2 = True (unit prop)
			{cnf.Literal(3), cnf.Literal(4)},                 // left over for the solver
			{cnf.Literal(3), cnf.Literal(4), cnf.Literal(2)}, // subsumed by the clause above
		},
	}

	result := Run(problem, 0, 1)
	if result.Unsat {
		t.Fatalf("expected a satisfiable problem")
	}
	if result.Problem.NumVars > 2 {
		t.Errorf("Problem.NumVars = %d, want at most 2 (only 3 and 4 should survive)", result.Problem.NumVars)
	}

	// Fabricate a solution to the reduced problem: every surviving
	// variable set to True satisfies {3, 4}.
	solution := assign.New(result.Problem.NumVars)
	for v := 1; v <= result.Problem.NumVars; v++ {
		solution[v] = assign.True
	}

	full := result.Reconstruct(solution)
	allClausesSatisfied(t, problem, full)
}

// TestRunDetectsUnsat verifies that Run itself proves a trivially
// unsatisfiable problem unsatisfiable, without needing a solver.
func TestRunDetectsUnsat(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 1,
		Clauses: []cnf.Clause{
			{cnf.Literal(1)},
			{cnf.Literal(-1)},
		},
	}
	result := Run(problem, 0, 1)
	if !result.Unsat {
		t.Fatalf("expected Unsat = true")
	}
}

// TestRunWithVariableEliminationReconstructsCorrectly exercises the
// trickiest reconstruction path: a variable removed entirely by
// bounded variable elimination (not just fixed by unit propagation or
// pure literal elimination) must still be recoverable. Every variable
// here appears with both polarities somewhere, so neither unit
// propagation nor pure literal elimination can fire; only resolution
// can simplify this formula.
//
// Rather than assume exactly which variable Run's pipeline chooses to
// eliminate (an implementation detail of iteration order, not a
// correctness property), this brute-forces every assignment of the
// *reduced* problem's variables, and for every one that actually
// satisfies the reduced problem, checks that reconstructing it yields
// a full assignment satisfying the *original* problem.
func TestRunWithVariableEliminationReconstructsCorrectly(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 3,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(2)},
			{cnf.Literal(-1), cnf.Literal(3)},
			{cnf.Literal(-2), cnf.Literal(-3)},
		},
	}

	result := Run(problem, 0, 1)
	if result.Unsat {
		t.Fatalf("expected a satisfiable problem")
	}
	if len(result.Eliminated) == 0 {
		t.Fatalf("expected at least one variable to be eliminated via resolution")
	}

	n := result.Problem.NumVars
	checked := 0
	for mask := 0; mask < (1 << n); mask++ {
		solution := assign.New(n)
		for v := 1; v <= n; v++ {
			if mask&(1<<(v-1)) != 0 {
				solution[v] = assign.True
			} else {
				solution[v] = assign.False
			}
		}
		if !allSatisfied(result.Problem.Clauses, solution) {
			continue
		}
		checked++
		full := result.Reconstruct(solution)
		allClausesSatisfied(t, problem, full)
	}
	if checked == 0 {
		t.Fatalf("no assignment of the reduced problem's %d variables satisfied it; test setup is broken", n)
	}
}

// allSatisfied reports whether every clause in clauses is satisfied
// by assignment.
func allSatisfied(clauses []cnf.Clause, assignment assign.Assignment) bool {
	for _, clause := range clauses {
		if !clauseSatisfied(clause, assignment) {
			return false
		}
	}
	return true
}

// TestRunLeavesUnconstrainedVariablesArbitrarilyFalse verifies that a
// variable that never appears in any clause ends up with a definite
// (if arbitrary) value after reconstruction, rather than Unassigned.
func TestRunLeavesUnconstrainedVariablesArbitrarilyFalse(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 2, // variable 2 never appears in any clause
		Clauses: []cnf.Clause{
			{cnf.Literal(1)},
		},
	}
	result := Run(problem, 0, 1)
	if result.Unsat {
		t.Fatalf("expected a satisfiable problem")
	}

	solution := assign.New(result.Problem.NumVars)
	for v := 1; v <= result.Problem.NumVars; v++ {
		solution[v] = assign.True
	}
	full := result.Reconstruct(solution)
	if full[2] == assign.Unassigned {
		t.Errorf("full[2] = Unassigned, want a definite value")
	}
	allClausesSatisfied(t, problem, full)
}
