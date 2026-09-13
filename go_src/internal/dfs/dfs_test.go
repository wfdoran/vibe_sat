package dfs

import (
	"math/rand/v2"
	"testing"
	"time"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/occurrence"
)

// TestChooseWatchSkipsFalseLiterals verifies that chooseWatch never
// returns a literal that is currently false.
func TestChooseWatchSkipsFalseLiterals(t *testing.T) {
	x := assign.New(2)
	x[1] = assign.False // literal 1 is now false
	lit, ok := chooseWatch(cnf.Clause{cnf.Literal(1), cnf.Literal(2)}, x, 0)
	if !ok || lit != cnf.Literal(2) {
		t.Errorf("chooseWatch() = (%v, %v), want (2, true)", lit, ok)
	}
}

// TestChooseWatchHonorsAvoid verifies that chooseWatch never returns
// the literal passed as avoid, even if it would otherwise be a valid
// (non-false) choice.
func TestChooseWatchHonorsAvoid(t *testing.T) {
	x := assign.New(2)
	lit, ok := chooseWatch(cnf.Clause{cnf.Literal(1), cnf.Literal(2)}, x, cnf.Literal(1))
	if !ok || lit != cnf.Literal(2) {
		t.Errorf("chooseWatch() = (%v, %v), want (2, true)", lit, ok)
	}
}

// TestChooseWatchFailsWhenNoneAvailable verifies that chooseWatch
// reports ok = false when every literal is either false or excluded.
func TestChooseWatchFailsWhenNoneAvailable(t *testing.T) {
	x := assign.New(2)
	x[1] = assign.False
	x[2] = assign.True
	if _, ok := chooseWatch(cnf.Clause{cnf.Literal(1), cnf.Literal(-2)}, x, 0); ok {
		t.Errorf("chooseWatch() ok = true, want false (both literals are false)")
	}
}

// TestNewWatchStatePicksTwoNonFalseLiterals verifies that
// newWatchState's initial choice of watches for each clause never
// includes a literal that is currently false.
func TestNewWatchStatePicksTwoNonFalseLiterals(t *testing.T) {
	x := assign.New(3)
	x[1] = assign.False // literal 1 is false; literal -1 is true
	clauses := []cnf.Clause{{cnf.Literal(1), cnf.Literal(2), cnf.Literal(3)}}

	ws, ok := newWatchState(clauses, x)
	if !ok {
		t.Fatalf("expected ok = true")
	}
	for _, lit := range ws.watch[0] {
		if isFalse(lit, x) {
			t.Errorf("watch[0] = %v contains a false literal", ws.watch[0])
		}
	}
}

// TestNewWatchStateFailsOnContradiction verifies that newWatchState
// reports ok = false for a clause with fewer than two non-false
// literals.
func TestNewWatchStateFailsOnContradiction(t *testing.T) {
	x := assign.New(2)
	x[1] = assign.False
	x[2] = assign.True
	clauses := []cnf.Clause{{cnf.Literal(1), cnf.Literal(-2)}} // both literals false
	if _, ok := newWatchState(clauses, x); ok {
		t.Errorf("expected ok = false for a clause with no valid watches")
	}
}

// TestCloneWatchStateIsIndependent verifies that mutating a cloned
// watchState does not affect the original.
func TestCloneWatchStateIsIndependent(t *testing.T) {
	x := assign.New(2)
	ws, ok := newWatchState([]cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}}, x)
	if !ok {
		t.Fatalf("expected ok = true")
	}
	original := ws.watch[0]

	clone := cloneWatchState(ws)
	clone.watch[0][0] = 99

	if ws.watch[0] != original {
		t.Errorf("original watch state changed: %v, want unchanged %v", ws.watch[0], original)
	}
}

// TestBCPPropagatesUnitChain verifies that BCP follows a chain of
// forced unit propagations to a complete, consistent assignment.
func TestBCPPropagatesUnitChain(t *testing.T) {
	// 1 forces -2 true (via clause {-1, -2}) which forces 3 true (via
	// clause {2, 3}), completing the assignment.
	problem := &cnf.Problem{
		NumVars: 3,
		Clauses: []cnf.Clause{
			{cnf.Literal(-1), cnf.Literal(-2)},
			{cnf.Literal(2), cnf.Literal(3)},
		},
	}
	lists := occurrence.Build(problem)
	x := assign.New(3)
	ws, ok := newWatchState(problem.Clauses, x)
	if !ok {
		t.Fatalf("expected ok = true")
	}
	x[1] = assign.True

	status := BCP(problem.Clauses, lists, ws, x, 1)
	if status != Done {
		t.Fatalf("status = %v, want Done", status)
	}
	if x[2] != assign.False {
		t.Errorf("x[2] = %v, want False", x[2])
	}
	if x[3] != assign.True {
		t.Errorf("x[3] = %v, want True", x[3])
	}
}

// TestBCPDetectsContradiction verifies that BCP reports Contra when
// propagation is forced to falsify every literal of some clause. Every
// clause here has at least two literals, as newWatchState requires (a
// unit clause has nothing to move a watch onto); Run guarantees this
// precondition in practice via a bootstrap call to
// preprocess.UnitPropagate before ever building watch state.
func TestBCPDetectsContradiction(t *testing.T) {
	// 1 forces 2=False (via {-1,-2}), which forces 3=True (via
	// {2,3}), which then makes clauses {-3,-4} and {-3,4} jointly
	// unsatisfiable regardless of variable 4's value.
	problem := &cnf.Problem{
		NumVars: 4,
		Clauses: []cnf.Clause{
			{cnf.Literal(-1), cnf.Literal(-2)},
			{cnf.Literal(2), cnf.Literal(3)},
			{cnf.Literal(-3), cnf.Literal(-4)},
			{cnf.Literal(-3), cnf.Literal(4)},
		},
	}
	lists := occurrence.Build(problem)
	x := assign.New(4)
	ws, ok := newWatchState(problem.Clauses, x)
	if !ok {
		t.Fatalf("expected ok = true")
	}
	x[1] = assign.True

	if status := BCP(problem.Clauses, lists, ws, x, 1); status != Contra {
		t.Errorf("status = %v, want Contra", status)
	}
}

// TestBCPLeavesPartialAssignmentOK verifies that BCP returns OK,
// without forcing any further variables, when no clause becomes unit
// or contradictory.
func TestBCPLeavesPartialAssignmentOK(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 3,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(2), cnf.Literal(3)},
		},
	}
	lists := occurrence.Build(problem)
	x := assign.New(3)
	ws, ok := newWatchState(problem.Clauses, x)
	if !ok {
		t.Fatalf("expected ok = true")
	}
	x[1] = assign.False

	if status := BCP(problem.Clauses, lists, ws, x, 1); status != OK {
		t.Errorf("status = %v, want OK", status)
	}
	if x[2] != assign.Unassigned || x[3] != assign.Unassigned {
		t.Errorf("x = %v, want variables 2 and 3 still Unassigned", x)
	}
}

// TestBCPMovesWatchAwayFromFalsifiedLiteral verifies the core watched-
// literal behavior: when a watched literal becomes false but another
// non-false literal is available, BCP moves the watch there instead
// of reporting unit/contradiction.
func TestBCPMovesWatchAwayFromFalsifiedLiteral(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 3,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(2), cnf.Literal(3)},
		},
	}
	lists := occurrence.Build(problem)
	x := assign.New(3)
	ws, ok := newWatchState(problem.Clauses, x)
	if !ok {
		t.Fatalf("expected ok = true")
	}
	initialWatch := ws.watch[0]

	x[1] = assign.False
	if status := BCP(problem.Clauses, lists, ws, x, 1); status != OK {
		t.Fatalf("status = %v, want OK", status)
	}

	if ws.watch[0] == initialWatch && (initialWatch[0] == cnf.Literal(1) || initialWatch[1] == cnf.Literal(1)) {
		t.Errorf("watch[0] = %v still references the falsified literal 1", ws.watch[0])
	}
	for _, lit := range ws.watch[0] {
		if isFalse(lit, x) {
			t.Errorf("watch[0] = %v contains a false literal after BCP", ws.watch[0])
		}
	}
}

// TestSelectVarPrefersShorterUnsatisfiedClauses verifies that
// SelectVar's weighting favors a variable that appears in a shorter
// not-yet-satisfied clause over one that only appears in a longer one.
func TestSelectVarPrefersShorterUnsatisfiedClauses(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 3,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(2)},                 // n=2, weight 0.7^0 = 1 each
			{cnf.Literal(3), cnf.Literal(1), cnf.Literal(2)}, // n=3, weight 0.7^1 = 0.7 each (but 1, 2 already scored above too)
		},
	}
	x := assign.New(3)
	rng := rand.New(rand.NewPCG(1, 1))

	// Variable 3 only appears in the 3-literal clause (score 0.7);
	// variables 1 and 2 appear in both (score 1 + 0.7 = 1.7 each), so
	// SelectVar must never choose 3.
	for i := 0; i < 20; i++ {
		v := SelectVar(problem, x, rng)
		if v == 3 {
			t.Fatalf("SelectVar() = 3, want 1 or 2 (higher combined score)")
		}
	}
}

// TestSelectVarOnlyReturnsUnassignedVariables verifies that SelectVar
// never returns a variable that is already assigned.
func TestSelectVarOnlyReturnsUnassignedVariables(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 2,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(2)},
		},
	}
	x := assign.New(2)
	x[1] = assign.True
	rng := rand.New(rand.NewPCG(2, 2))

	if v := SelectVar(problem, x, rng); v != 2 {
		t.Errorf("SelectVar() = %d, want 2 (the only unassigned variable)", v)
	}
}

// TestSelectVarFastPickReturnsLowestUnassigned verifies that
// SelectVarFastPick returns the smallest-numbered unassigned
// variable, ignoring clause contents entirely.
func TestSelectVarFastPickReturnsLowestUnassigned(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 4,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(4)},
		},
	}
	x := assign.New(4)
	x[1] = assign.True
	x[2] = assign.False

	if v := SelectVarFastPick(problem, x); v != 3 {
		t.Errorf("SelectVarFastPick() = %d, want 3", v)
	}
}

// TestSelectVarFastPickSkipsAllAssigned verifies that
// SelectVarFastPick returns -1 when every variable is already
// assigned (the degenerate case; Run never actually reaches this for
// NumVars >= 1, since BCP-returned OK states always have at least one
// unassigned variable).
func TestSelectVarFastPickSkipsAllAssigned(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2}
	x := assign.New(2)
	x[1] = assign.True
	x[2] = assign.False

	if v := SelectVarFastPick(problem, x); v != -1 {
		t.Errorf("SelectVarFastPick() = %d, want -1", v)
	}
}

// TestRunWithFastSelectVarFindsSatisfiableFormula verifies that Run
// still finds a correct satisfying assignment when configured to use
// SelectVarFast instead of the default weighted heuristic.
func TestRunWithFastSelectVarFindsSatisfiableFormula(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 3,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(2)},
			{cnf.Literal(-1), cnf.Literal(3)},
			{cnf.Literal(-2), cnf.Literal(-3)},
		},
	}
	lists := occurrence.Build(problem)
	rng := rand.New(rand.NewPCG(8, 8))

	result := Run(problem, lists, nil, SelectVarFast, rng, 0)

	if !result.Satisfiable {
		t.Fatalf("expected Satisfiable = true")
	}
	for ci, clause := range problem.Clauses {
		satisfied := false
		for _, lit := range clause {
			if result.Assignment.LiteralIsTrue(lit) {
				satisfied = true
				break
			}
		}
		if !satisfied {
			t.Errorf("clause %d (%v) not satisfied by returned assignment %v", ci, clause, result.Assignment)
		}
	}
}

// TestRunWithFastSelectVarProvesUnsatisfiablePigeonhole verifies that
// Run, configured to use SelectVarFast, still correctly proves the
// pigeonhole instance unsatisfiable (the cheaper heuristic must still
// be sound, even though it typically explores a larger tree).
func TestRunWithFastSelectVarProvesUnsatisfiablePigeonhole(t *testing.T) {
	problem := pigeonholeProblem(t, 4, 3)
	lists := occurrence.Build(problem)
	rng := rand.New(rand.NewPCG(9, 9))

	result := Run(problem, lists, nil, SelectVarFast, rng, 0)

	if result.Satisfiable {
		t.Fatalf("expected Satisfiable = false for an unsatisfiable pigeonhole instance")
	}
	if result.TimedOut {
		t.Errorf("expected TimedOut = false (a genuine UNSAT proof, not a timeout)")
	}
}

// TestRunFindsSatisfiableFormula verifies that Run finds a satisfying
// assignment for a small satisfiable formula and that the returned
// assignment actually satisfies every clause.
func TestRunFindsSatisfiableFormula(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 3,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(2)},
			{cnf.Literal(-1), cnf.Literal(3)},
			{cnf.Literal(-2), cnf.Literal(-3)},
		},
	}
	lists := occurrence.Build(problem)
	rng := rand.New(rand.NewPCG(3, 3))

	result := Run(problem, lists, nil, SelectVarWeighted, rng, 0)

	if !result.Satisfiable {
		t.Fatalf("expected Satisfiable = true")
	}
	for ci, clause := range problem.Clauses {
		satisfied := false
		for _, lit := range clause {
			if result.Assignment.LiteralIsTrue(lit) {
				satisfied = true
				break
			}
		}
		if !satisfied {
			t.Errorf("clause %d (%v) not satisfied by returned assignment %v", ci, clause, result.Assignment)
		}
	}
}

// TestRunProvesUnsatisfiableFormula verifies that Run returns
// Satisfiable = false, TimedOut = false (a proof of UNSAT, not merely
// "not found") for a small unsatisfiable formula.
func TestRunProvesUnsatisfiableFormula(t *testing.T) {
	// x1 AND NOT x1: trivially unsatisfiable.
	problem := &cnf.Problem{
		NumVars: 1,
		Clauses: []cnf.Clause{
			{cnf.Literal(1)},
			{cnf.Literal(-1)},
		},
	}
	lists := occurrence.Build(problem)
	rng := rand.New(rand.NewPCG(4, 4))

	result := Run(problem, lists, nil, SelectVarWeighted, rng, 0)

	if result.Satisfiable {
		t.Fatalf("expected Satisfiable = false")
	}
	if result.TimedOut {
		t.Errorf("expected TimedOut = false (a genuine UNSAT proof, not a timeout)")
	}
}

// TestRunProvesUnsatisfiablePigeonhole verifies Run against a modest
// pigeonhole-principle instance (4 pigeons, 3 holes: unsatisfiable),
// which requires a non-trivial number of branches and BCP steps to
// resolve, unlike the trivial x1/NOT x1 case above.
func TestRunProvesUnsatisfiablePigeonhole(t *testing.T) {
	problem := pigeonholeProblem(t, 4, 3)
	lists := occurrence.Build(problem)
	rng := rand.New(rand.NewPCG(5, 5))

	result := Run(problem, lists, nil, SelectVarWeighted, rng, 0)

	if result.Satisfiable {
		t.Fatalf("expected Satisfiable = false for an unsatisfiable pigeonhole instance")
	}
	if result.TimedOut {
		t.Errorf("expected TimedOut = false (a genuine UNSAT proof, not a timeout)")
	}
}

// pigeonholeProblem builds the standard CNF encoding of "numPigeons
// pigeons cannot be placed into numHoles holes with no two pigeons
// sharing a hole", which is unsatisfiable whenever numPigeons >
// numHoles. Variable (p-1)*numHoles+h represents "pigeon p is in hole
// h".
func pigeonholeProblem(t *testing.T, numPigeons, numHoles int) *cnf.Problem {
	t.Helper()
	v := func(p, h int) cnf.Literal {
		return cnf.Literal((p-1)*numHoles + h)
	}

	var clauses []cnf.Clause
	// Every pigeon is in at least one hole.
	for p := 1; p <= numPigeons; p++ {
		var clause cnf.Clause
		for h := 1; h <= numHoles; h++ {
			clause = append(clause, v(p, h))
		}
		clauses = append(clauses, clause)
	}
	// No two pigeons share a hole.
	for h := 1; h <= numHoles; h++ {
		for p1 := 1; p1 <= numPigeons; p1++ {
			for p2 := p1 + 1; p2 <= numPigeons; p2++ {
				clauses = append(clauses, cnf.Clause{-v(p1, h), -v(p2, h)})
			}
		}
	}

	return &cnf.Problem{NumVars: numPigeons * numHoles, Clauses: clauses}
}

// TestRunHandlesZeroVariableProblems verifies that Run does not panic
// and reports the correct result for the degenerate case of a problem
// with no variables at all.
func TestRunHandlesZeroVariableProblems(t *testing.T) {
	rng := rand.New(rand.NewPCG(6, 6))

	satProblem := &cnf.Problem{NumVars: 0, Clauses: nil}
	if result := Run(satProblem, occurrence.Build(satProblem), nil, SelectVarWeighted, rng, 0); !result.Satisfiable {
		t.Errorf("expected a 0-variable, 0-clause problem to be Satisfiable")
	}

	unsatProblem := &cnf.Problem{NumVars: 0, Clauses: []cnf.Clause{{}}}
	if result := Run(unsatProblem, occurrence.Build(unsatProblem), nil, SelectVarWeighted, rng, 0); result.Satisfiable {
		t.Errorf("expected a 0-variable problem with an empty clause to be unsatisfiable")
	}
}

// TestRunRespectsTimeLimit verifies that Run gives up and reports
// TimedOut = true, rather than hanging, when given an extremely short
// time limit on a problem too large to finish immediately.
func TestRunRespectsTimeLimit(t *testing.T) {
	// A large satisfiable-but-unconstrained pigeonhole-like problem is
	// unnecessary; a moderately sized unsatisfiable pigeonhole instance
	// gives the search plenty of tree to explore.
	problem := pigeonholeProblem(t, 9, 8)
	lists := occurrence.Build(problem)
	rng := rand.New(rand.NewPCG(7, 7))
	tiny := 1 * time.Nanosecond

	result := Run(problem, lists, &tiny, SelectVarWeighted, rng, 0)

	if !result.TimedOut {
		t.Fatalf("expected TimedOut = true with a 1ns time limit")
	}
	if result.Satisfiable {
		t.Errorf("a timed-out search must not claim Satisfiable")
	}
}
