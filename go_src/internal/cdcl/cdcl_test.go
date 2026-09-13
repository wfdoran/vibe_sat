package cdcl

import (
	"math/rand/v2"
	"testing"
	"time"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/dfs"
)

// TestChooseWatchSkipsFalseLiterals verifies that chooseWatch never
// returns a literal that is currently false.
func TestChooseWatchSkipsFalseLiterals(t *testing.T) {
	x := assign.New(2)
	x[1] = assign.False
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
	x[2] = assign.False
	_, ok := chooseWatch(cnf.Clause{cnf.Literal(1), cnf.Literal(2)}, x, 0)
	if ok {
		t.Errorf("chooseWatch() reported ok = true, want false")
	}
}

// TestNewSolverDetectsBootstrapContradiction verifies that newSolver
// reports failure when the bootstrap unit propagation alone already
// proves the problem unsatisfiable.
func TestNewSolverDetectsBootstrapContradiction(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 1,
		Clauses: []cnf.Clause{{cnf.Literal(1)}, {cnf.Literal(-1)}},
	}
	_, ok := newSolver(problem)
	if ok {
		t.Errorf("newSolver() reported ok = true for a contradictory unit-clause pair")
	}
}

// TestPropagatePropagatesUnitChain verifies that propagate carries a
// single decision through a chain of forced consequences via watched
// literals, matching STAGE9.md's dfs.BCP behavior.
func TestPropagatePropagatesUnitChain(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 3,
		Clauses: []cnf.Clause{{cnf.Literal(-1), cnf.Literal(-2)}, {cnf.Literal(2), cnf.Literal(3)}},
	}
	s, ok := newSolver(problem)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}

	s.currentLevel = 1
	s.trailLim = append(s.trailLim, len(s.trail))
	s.assignLiteral(cnf.Literal(1), s.currentLevel, noReason)

	if confl := s.propagate(); confl != noReason {
		t.Fatalf("propagate() = conflict at clause %d, want none", confl)
	}
	if s.x[2] != assign.False {
		t.Errorf("x[2] = %v, want False", s.x[2])
	}
	if s.x[3] != assign.True {
		t.Errorf("x[3] = %v, want True", s.x[3])
	}
}

// TestAnalyzeDerivesUnitClauseIndependentOfDecision exercises the
// trickiest part of CDCL end to end against a hand-verified example:
// clauses {-2,4} and {-2,-4} alone force x2 = False regardless of any
// other variable, but the conflict that exposes this is only reached
// after deciding x1 = False and having that decision propagate x2 =
// True via clause {1,2}. First-UIP resolution must still recognize
// that x1's decision is irrelevant to the actual contradiction and
// learn the unit clause {-2} (backtracking all the way to level 0),
// not some clause that (correctly, but needlessly) also mentions x1.
//
// This was verified by hand, tracing propagate/analyze's exact
// execution order against this formula, before being written down
// here: with x1 = False, clause {1,2} forces x2 = True; x2 = True
// then forces x4 = True via {-2,4}; x4 = True then falsifies {-2,-4}
// outright. Resolving that conflict against x4's reason ({-2,4})
// immediately reduces the current-level literal count to just x2
// (the first UIP), independent of x1 (whose own reason, {1,2}, is
// never even inspected).
func TestAnalyzeDerivesUnitClauseIndependentOfDecision(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 4,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(2)},
			{cnf.Literal(-1), cnf.Literal(3)},
			{cnf.Literal(-1), cnf.Literal(-3)},
			{cnf.Literal(-2), cnf.Literal(4)},
			{cnf.Literal(-2), cnf.Literal(-4)},
		},
	}
	s, ok := newSolver(problem)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}

	s.currentLevel = 1
	s.trailLim = append(s.trailLim, len(s.trail))
	s.assignLiteral(cnf.Literal(-1), s.currentLevel, noReason)

	confl := s.propagate()
	if confl == noReason {
		t.Fatalf("propagate() found no conflict; expected clause {-2,-4} to be falsified")
	}

	learned, backtrackLevel := s.analyze(confl)
	if backtrackLevel != 0 {
		t.Errorf("backtrackLevel = %d, want 0", backtrackLevel)
	}
	if len(learned) != 1 || learned[0] != cnf.Literal(-2) {
		t.Errorf("learned = %v, want [-2]", learned)
	}
}

// TestAddLearnedClauseSkipsWatchesForUnitClause verifies that a
// length-1 learned clause is not added to the watched clause
// database (it has nowhere to shed a second watch onto, and needs
// none: it becomes a permanent level-0 fact instead).
func TestAddLearnedClauseSkipsWatchesForUnitClause(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2, Clauses: []cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}}}
	s, ok := newSolver(problem)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	before := len(s.clauses)

	idx := s.addLearnedClause(cnf.Clause{cnf.Literal(-1)})
	if idx != noReason {
		t.Errorf("addLearnedClause() = %d, want noReason", idx)
	}
	if len(s.clauses) != before {
		t.Errorf("len(s.clauses) = %d, want unchanged at %d", len(s.clauses), before)
	}
}

// TestAddLearnedClauseWatchesAssertingLiteralAndHighestLevel verifies
// that a length->=2 learned clause is watched on its asserting
// literal (learned[0]) and on whichever other literal has the
// highest decision level.
func TestAddLearnedClauseWatchesAssertingLiteralAndHighestLevel(t *testing.T) {
	problem := &cnf.Problem{NumVars: 3, Clauses: []cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}}}
	s, ok := newSolver(problem)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	s.level[2] = 1
	s.level[3] = 3 // higher than variable 2's level

	idx := s.addLearnedClause(cnf.Clause{cnf.Literal(-1), cnf.Literal(2), cnf.Literal(3)})
	if idx == noReason {
		t.Fatal("addLearnedClause() returned noReason for a length-3 clause")
	}
	got := s.watch[idx]
	want := [2]cnf.Literal{cnf.Literal(-1), cnf.Literal(3)}
	if got != want {
		t.Errorf("watch[%d] = %v, want %v", idx, got, want)
	}
}

// TestRunFindsSatisfiableFormula mirrors dfs's equivalent test: a
// small satisfiable formula, checked against both SelectVar variants.
func TestRunFindsSatisfiableFormula(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 3,
		Clauses: []cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}, {cnf.Literal(-1), cnf.Literal(3)}, {cnf.Literal(-2), cnf.Literal(-3)}},
	}

	for _, variant := range []dfs.SelectVarVariant{dfs.SelectVarWeighted, dfs.SelectVarFast} {
		rng := rand.New(rand.NewPCG(1, 2))
		result := Run(problem, nil, variant, rng, 0)
		if !result.Satisfiable {
			t.Fatalf("variant %v: Run() reported unsatisfiable for a satisfiable formula", variant)
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
				t.Errorf("variant %v: clause %d (%v) not satisfied by %v", variant, ci, clause, result.Assignment)
			}
		}
	}
}

// TestRunProvesUnsatisfiableSmallFormula uses the same hand-verified
// formula as TestAnalyzeDerivesUnitClauseIndependentOfDecision (which
// is unsatisfiable overall: x2 must be False, which forces x1 = True,
// which then forces x3 both True and False) to check the full Run
// loop end to end, across both SelectVar variants, and confirms at
// least one clause was actually learned (i.e. this isn't trivially
// passing via the level-0 bootstrap alone).
func TestRunProvesUnsatisfiableSmallFormula(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 4,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(2)},
			{cnf.Literal(-1), cnf.Literal(3)},
			{cnf.Literal(-1), cnf.Literal(-3)},
			{cnf.Literal(-2), cnf.Literal(4)},
			{cnf.Literal(-2), cnf.Literal(-4)},
		},
	}

	for _, variant := range []dfs.SelectVarVariant{dfs.SelectVarWeighted, dfs.SelectVarFast} {
		rng := rand.New(rand.NewPCG(1, 2))
		result := Run(problem, nil, variant, rng, 0)
		if result.Satisfiable {
			t.Fatalf("variant %v: Run() reported satisfiable for an unsatisfiable formula", variant)
		}
		if result.TimedOut {
			t.Errorf("variant %v: Run() timed out with no time limit set", variant)
		}
		if result.NumConflicts == 0 {
			t.Errorf("variant %v: NumConflicts = 0, want at least one conflict", variant)
		}
	}
}

// TestRunProvesUnsatisfiablePigeonhole mirrors dfs's pigeonhole test:
// 4 pigeons cannot be placed into 3 holes with no two sharing a hole.
func TestRunProvesUnsatisfiablePigeonhole(t *testing.T) {
	problem := pigeonholeProblem(4, 3)
	rng := rand.New(rand.NewPCG(9, 9))

	result := Run(problem, nil, dfs.SelectVarFast, rng, 0)

	if result.Satisfiable {
		t.Fatal("Run() reported satisfiable for the 4-pigeon/3-hole problem")
	}
	if result.TimedOut {
		t.Error("Run() timed out with no time limit set")
	}
}

// TestRunHandlesZeroVariableProblems mirrors dfs's equivalent test.
func TestRunHandlesZeroVariableProblems(t *testing.T) {
	rng := rand.New(rand.NewPCG(6, 6))

	satProblem := &cnf.Problem{NumVars: 0, Clauses: nil}
	result := Run(satProblem, nil, dfs.SelectVarWeighted, rng, 0)
	if !result.Satisfiable {
		t.Error("Run() reported unsatisfiable for an empty problem")
	}

	unsatProblem := &cnf.Problem{NumVars: 0, Clauses: []cnf.Clause{{}}}
	result = Run(unsatProblem, nil, dfs.SelectVarWeighted, rng, 0)
	if result.Satisfiable {
		t.Error("Run() reported satisfiable for a problem with an empty clause")
	}
}

// TestRunRespectsTimeLimit mirrors dfs's equivalent test: a
// vanishingly small time limit against a problem hard enough to not
// finish instantly.
func TestRunRespectsTimeLimit(t *testing.T) {
	problem := pigeonholeProblem(9, 8)
	rng := rand.New(rand.NewPCG(7, 7))
	tiny := time.Duration(1)

	result := Run(problem, &tiny, dfs.SelectVarWeighted, rng, 0)

	if !result.TimedOut {
		t.Error("expected TimedOut = true with a 1ns time limit")
	}
	if result.Satisfiable {
		t.Error("expected Satisfiable = false when timed out")
	}
}

// pigeonholeProblem builds the standard CNF encoding of "numPigeons
// pigeons cannot be placed into numHoles holes with no two pigeons
// sharing a hole", which is unsatisfiable whenever numPigeons >
// numHoles. Variable (p-1)*numHoles+h represents "pigeon p is in hole
// h". Mirrors dfs's identical helper.
func pigeonholeProblem(numPigeons, numHoles int) *cnf.Problem {
	v := func(p, h int) cnf.Literal { return cnf.Literal((p-1)*numHoles + h) }

	var clauses []cnf.Clause
	for p := 1; p <= numPigeons; p++ {
		var clause cnf.Clause
		for h := 1; h <= numHoles; h++ {
			clause = append(clause, v(p, h))
		}
		clauses = append(clauses, clause)
	}
	for h := 1; h <= numHoles; h++ {
		for p1 := 1; p1 <= numPigeons; p1++ {
			for p2 := p1 + 1; p2 <= numPigeons; p2++ {
				clauses = append(clauses, cnf.Clause{-v(p1, h), -v(p2, h)})
			}
		}
	}

	return &cnf.Problem{NumVars: numPigeons * numHoles, Clauses: clauses}
}
