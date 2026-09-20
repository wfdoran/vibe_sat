package preprocess

import (
	"math/rand/v2"
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

// TestSimplifyWithAssignmentReusesUnchangedClauses verifies STAGE35.md's
// lazy-allocation optimization directly: a clause containing no
// assigned literal at all must be kept as the exact same underlying
// slice (no allocation), while a clause with a false literal to drop
// must come back as a genuinely new, correctly-reduced slice.
func TestSimplifyWithAssignmentReusesUnchangedClauses(t *testing.T) {
	unchanged := cnf.Clause{cnf.Literal(1), cnf.Literal(2)}
	toReduce := cnf.Clause{cnf.Literal(-3), cnf.Literal(4), cnf.Literal(5)}
	clauses := []cnf.Clause{unchanged, toReduce}

	assignment := assign.New(5)
	assignment[3] = assign.True // makes literal -3 false, dropping it from toReduce

	if unsat := simplifyWithAssignment(&clauses, assignment); unsat {
		t.Fatal("expected no contradiction")
	}
	if len(clauses) != 2 {
		t.Fatalf("clauses = %v, want 2 clauses kept", clauses)
	}
	if &clauses[0][0] != &unchanged[0] {
		t.Error("the untouched clause should be the exact same underlying slice, not a copy")
	}
	want := cnf.Clause{cnf.Literal(4), cnf.Literal(5)}
	if !slices.Equal(clauses[1], want) {
		t.Errorf("reduced clause = %v, want %v", clauses[1], want)
	}
}

// TestSimplifyWithAssignmentDropsSatisfiedAndDetectsEmptyClause covers
// the two other outcomes simplifyWithAssignment can produce for a
// clause: dropped entirely (some literal is true), and unsat (every
// literal is false, or the clause started out empty).
func TestSimplifyWithAssignmentDropsSatisfiedAndDetectsEmptyClause(t *testing.T) {
	assignment := assign.New(2)
	assignment[1] = assign.True

	satisfied := []cnf.Clause{{cnf.Literal(1), cnf.Literal(-2)}}
	if unsat := simplifyWithAssignment(&satisfied, assignment); unsat {
		t.Fatal("expected no contradiction")
	}
	if len(satisfied) != 0 {
		t.Errorf("satisfied clause should have been dropped entirely, got %v", satisfied)
	}

	allFalse := []cnf.Clause{{cnf.Literal(-1)}}
	if unsat := simplifyWithAssignment(&allFalse, assignment); !unsat {
		t.Error("expected unsat = true when every literal in a clause is false")
	}

	empty := []cnf.Clause{{}}
	if unsat := simplifyWithAssignment(&empty, assignment); !unsat {
		t.Error("expected unsat = true for an originally empty clause")
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

// TestTrySubsumeFromGenericRespectsZeroWorkBudget confirms the
// STAGE30.md/REPORT30.md work-budget safety net actually does nothing
// (removes nothing, touches no keep bit) once the budget is exhausted,
// rather than, say, panicking on an unexpected state or ignoring the
// budget entirely -- directly, deterministically, rather than trying
// to organically construct a formula large enough to exhaust the real
// default budget.
func TestTrySubsumeFromGenericRespectsZeroWorkBudget(t *testing.T) {
	clauses := []cnf.Clause{
		{cnf.Literal(1)},
		{cnf.Literal(1), cnf.Literal(2)},
	}
	occ := buildLiteralOccurrenceLists(clauses, 2)
	keep := []bool{true, true}
	budget := 0

	trySubsumeFromGeneric(clauses, occ, len(clauses),
		func(i int) bool { return keep[i] },
		func(i int) { keep[i] = false },
		0, len(clauses), &budget)

	if !keep[0] || !keep[1] {
		t.Errorf("a zero work budget should have removed nothing; keep = %v", keep)
	}
}

// TestEliminateSubsumedClausesRemovesSuperset verifies that a longer
// clause subsumed by a shorter one is removed.
func TestEliminateSubsumedClausesRemovesSuperset(t *testing.T) {
	clauses := []cnf.Clause{
		{cnf.Literal(1), cnf.Literal(2)},
		{cnf.Literal(1), cnf.Literal(2), cnf.Literal(3)},
	}
	numRemoved := eliminateSubsumedClauses(&clauses, 3)
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
	numRemoved := eliminateSubsumedClauses(&clauses, 2)
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
	wantRemoved := eliminateSubsumedClauses(&want, 4)

	for _, numThreads := range []int{1, 2, 3, 4, 8} {
		got := cloneClauses(original)
		gotRemoved := eliminateSubsumedClausesParallel(&got, 4, numThreads)
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
	wantRemoved := eliminateSubsumedClauses(&want, problem.NumVars)
	if wantRemoved == 0 {
		t.Fatal("test file has nothing to subsume; pick a different file")
	}

	for _, numThreads := range []int{1, 2, 3, 4, 8, 16} {
		got := cloneClauses(problem.Clauses)
		gotRemoved := eliminateSubsumedClausesParallel(&got, problem.NumVars, numThreads)
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

// bruteForceSubsumedClauses is a deliberately naive, obviously-correct
// O(clauses^2) reference implementation of subsumption elimination --
// exactly what eliminateSubsumedClauses itself was before STAGE30.md's
// occurrence-list rewrite (see reports/REPORT30.md), kept here purely
// as a test oracle rather than shipped in the real preprocessing path.
// Used by TestEliminateSubsumedClausesMatchesBruteForceOnRandomFormulas
// to build confidence in the fast version's correctness across many
// random cases, not just the small number of hand-picked and
// real-file examples above.
func bruteForceSubsumedClauses(clauses []cnf.Clause) []cnf.Clause {
	keep := make([]bool, len(clauses))
	for i := range keep {
		keep[i] = true
	}
	for i := range clauses {
		if !keep[i] {
			continue
		}
		for j := range clauses {
			if i == j || !keep[j] {
				continue
			}
			if len(clauses[i]) > len(clauses[j]) {
				continue
			}
			if len(clauses[i]) == len(clauses[j]) && i > j {
				continue
			}
			if isSubsetOf(clauses[i], clauses[j]) {
				keep[j] = false
			}
		}
	}
	var kept []cnf.Clause
	for i, c := range clauses {
		if keep[i] {
			kept = append(kept, c)
		}
	}
	return kept
}

// randomClauseSet generates a random, small clause set over variables
// 1..numVars, with clause lengths and repeated/overlapping literals
// deliberately likely (a small numVars relative to clause count all
// but guarantees plenty of real subsumption relationships to exercise,
// unlike sampling from a huge variable space where clauses would
// almost never overlap at all).
func randomClauseSet(rng *rand.Rand, numVars, numClauses int) []cnf.Clause {
	clauses := make([]cnf.Clause, numClauses)
	for i := range clauses {
		length := 1 + rng.IntN(4)
		clause := make(cnf.Clause, length)
		for j := range clause {
			v := 1 + rng.IntN(numVars)
			if rng.IntN(2) == 0 {
				v = -v
			}
			clause[j] = cnf.Literal(v)
		}
		clauses[i] = clause
	}
	return clauses
}

// TestEliminateSubsumedClausesMatchesBruteForceOnRandomFormulas fuzz-
// tests the occurrence-list-based rewrite (STAGE30.md/REPORT30.md)
// against bruteForceSubsumedClauses across many random small clause
// sets, small enough (numVars/numClauses chosen so
// subsumptionWorkBudgetFactor's default budget is never remotely
// exhausted) that any discrepancy can only be a correctness bug in the
// occurrence-list restriction itself, not the work-budget safety net
// kicking in. This is the strongest correctness check in this file for
// the rewrite: REPORT30.md's whole argument for why restricting
// candidates to a clause's rarest literal's occurrence list can never
// miss a real subsumption only has to be right once in the reasoning,
// but many random trials are cheap insurance against a transcription
// bug in the code that implements that reasoning.
func TestEliminateSubsumedClausesMatchesBruteForceOnRandomFormulas(t *testing.T) {
	rng := rand.New(rand.NewPCG(12345, 67890))
	const trials = 500
	for trial := 0; trial < trials; trial++ {
		numVars := 1 + rng.IntN(8)
		numClauses := rng.IntN(20)
		original := randomClauseSet(rng, numVars, numClauses)

		want := bruteForceSubsumedClauses(original)

		got := cloneClauses(original)
		eliminateSubsumedClauses(&got, numVars)

		if len(got) != len(want) {
			t.Fatalf("trial %d (numVars=%d, clauses=%v): got %d surviving clauses %v, want %d %v",
				trial, numVars, original, len(got), got, len(want), want)
		}
		for i := range want {
			if !slices.Equal(got[i], want[i]) {
				t.Fatalf("trial %d (numVars=%d, clauses=%v): clauses[%d] = %v, want %v",
					trial, numVars, original, i, got[i], want[i])
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

// hugeBudget returns a bveWorkBudgetFactor-style budget large enough
// that no test using it is exercising STAGE31.md's work-budget cap --
// that cap has its own dedicated tests below; every other
// eliminateVariables test wants the pre-STAGE31.md, uncapped
// behavior.
func hugeBudget() *int {
	budget := 1 << 30
	return &budget
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

	steps := eliminateVariables(&clauses, assignment, 3, hugeBudget())
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

	steps := eliminateVariables(&clauses, assignment, 6, hugeBudget())
	if len(steps) != 0 {
		t.Fatalf("steps = %v, want none (elimination would increase clause count 5 -> 6)", steps)
	}
}

// TestResolveWithMarksMatchesResolveAcrossReusedBuffers checks
// resolveWithMarks against resolve directly (tautology detection,
// intra-clause duplicate-literal handling, and the resolvent
// contents), across several calls that deliberately reuse the same
// mark/touched buffers and revisit overlapping variables --
// STAGE31.md/REPORT31.md: mark is reset only for the entries a call
// actually touched, not with a full clear, so a bug in that cleanup
// would only show up as a leftover mark corrupting a *later* call
// that happens to touch the same variable, which is exactly what this
// sequence of cases is designed to exercise. The final loop over mark
// itself confirms nothing was left set after the last call.
func TestResolveWithMarksMatchesResolveAcrossReusedBuffers(t *testing.T) {
	mark := make([]int8, 6)
	var touched []int

	cases := []struct {
		pos, neg cnf.Clause
		v        int
	}{
		{cnf.Clause{cnf.Literal(1), cnf.Literal(2)}, cnf.Clause{cnf.Literal(-1), cnf.Literal(3)}, 1},
		{cnf.Clause{cnf.Literal(2), cnf.Literal(3)}, cnf.Clause{cnf.Literal(-2), cnf.Literal(4)}, 2},
		{cnf.Clause{cnf.Literal(1), cnf.Literal(2)}, cnf.Clause{cnf.Literal(-1), cnf.Literal(-2)}, 1},
		{cnf.Clause{cnf.Literal(3), cnf.Literal(4)}, cnf.Clause{cnf.Literal(-3), cnf.Literal(4)}, 3},
		{cnf.Clause{cnf.Literal(5)}, cnf.Clause{cnf.Literal(-5)}, 5},
	}

	for i, c := range cases {
		wantResolvent, wantOK := resolve(c.pos, c.neg, c.v)
		gotResolvent, gotOK := resolveWithMarks(c.pos, c.neg, c.v, mark, &touched)
		if gotOK != wantOK {
			t.Fatalf("case %d: ok = %v, want %v", i, gotOK, wantOK)
		}
		if wantOK && !sameLiteralSet(gotResolvent, wantResolvent) {
			t.Errorf("case %d: resolvent = %v, want %v (as sets)", i, gotResolvent, wantResolvent)
		}
	}

	for v, m := range mark {
		if m != 0 {
			t.Errorf("mark[%d] = %d after all calls, want 0 (leaked mark)", v, m)
		}
	}
}

// sameLiteralSet reports whether a and b contain the same literals,
// ignoring order (resolve/resolveWithMarks both build their resolvent
// by appending in first-seen order across pos then neg, which the
// two implementations can, in principle, do compatibly but which this
// test does not want to over-assume).
func sameLiteralSet(a, b cnf.Clause) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[cnf.Literal]bool{}
	for _, lit := range a {
		seen[lit] = true
	}
	for _, lit := range b {
		if !seen[lit] {
			return false
		}
	}
	return true
}

// bruteForceEliminateVariables is a deliberately naive, obviously-
// correct reference implementation of bounded variable elimination --
// exactly what eliminateVariables itself was before STAGE31.md's
// incremental-occurrence-list rewrite (see reports/REPORT31.md), kept
// here purely as a test oracle rather than shipped in the real
// preprocessing path. Used by
// TestEliminateVariablesMatchesBruteForceOnRandomFormulas to build
// confidence in the fast version's correctness across many random
// cases, not just the small number of hand-picked examples above.
func bruteForceEliminateVariables(clauses *[]cnf.Clause, assignment assign.Assignment, numVars int) []EliminationStep {
	var steps []EliminationStep

	for {
		positive := make([][]int, numVars+1)
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
				continue
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
				continue
			}

			step := EliminationStep{Var: v}
			for _, pi := range posIdx {
				step.Positive = append(step.Positive, (*clauses)[pi])
			}
			for _, ni := range negIdx {
				step.Negative = append(step.Negative, (*clauses)[ni])
			}
			steps = append(steps, step)

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
			break
		}

		if !eliminatedThisPass {
			return steps
		}
	}
}

// sameEliminationStep reports whether a and b record the same
// eliminated variable from the same positive/negative clauses, in the
// same order.
func sameEliminationStep(a, b EliminationStep) bool {
	if a.Var != b.Var {
		return false
	}
	if len(a.Positive) != len(b.Positive) || len(a.Negative) != len(b.Negative) {
		return false
	}
	for i := range a.Positive {
		if !slices.Equal(a.Positive[i], b.Positive[i]) {
			return false
		}
	}
	for i := range a.Negative {
		if !slices.Equal(a.Negative[i], b.Negative[i]) {
			return false
		}
	}
	return true
}

// TestEliminateVariablesMatchesBruteForceOnRandomFormulas fuzz-tests
// the incremental-occurrence-list rewrite (STAGE31.md/REPORT31.md)
// against bruteForceEliminateVariables across many random small
// clause sets, reusing Stage 30's randomClauseSet generator. Both
// algorithms restart their elimination scan from v=1 after every
// single elimination and call resolve/resolveWithMarks in the same
// order, so REPORT31.md's claim that this stage changed only internal
// bookkeeping -- never which variables get eliminated, in what order,
// or what the final clauses are -- is checked here as exact equality
// (steps and surviving clauses, both in order), not merely a weaker
// "still satisfiable" property.
func TestEliminateVariablesMatchesBruteForceOnRandomFormulas(t *testing.T) {
	rng := rand.New(rand.NewPCG(24680, 13579))
	const trials = 500
	for trial := 0; trial < trials; trial++ {
		numVars := 1 + rng.IntN(8)
		numClauses := rng.IntN(20)
		original := randomClauseSet(rng, numVars, numClauses)

		want := cloneClauses(original)
		wantSteps := bruteForceEliminateVariables(&want, assign.New(numVars), numVars)

		got := cloneClauses(original)
		gotSteps := eliminateVariables(&got, assign.New(numVars), numVars, hugeBudget())

		if !slices.EqualFunc(gotSteps, wantSteps, sameEliminationStep) {
			t.Fatalf("trial %d (numVars=%d, clauses=%v): steps = %v, want %v",
				trial, numVars, original, gotSteps, wantSteps)
		}
		if len(got) != len(want) {
			t.Fatalf("trial %d (numVars=%d, clauses=%v): got %d surviving clauses %v, want %d %v",
				trial, numVars, original, len(got), got, len(want), want)
		}
		for i := range want {
			if !slices.Equal(got[i], want[i]) {
				t.Fatalf("trial %d (numVars=%d, clauses=%v): clauses[%d] = %v, want %v",
					trial, numVars, original, i, got[i], want[i])
			}
		}
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
