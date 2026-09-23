package cdcl

import (
	"fmt"
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/params"
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
	_, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving)
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
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving)
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
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving)
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

	learned, backtrackLevel, lbd := s.analyze(confl)
	if backtrackLevel != 0 {
		t.Errorf("backtrackLevel = %d, want 0", backtrackLevel)
	}
	if len(learned) != 1 || learned[0] != cnf.Literal(-2) {
		t.Errorf("learned = %v, want [-2]", learned)
	}
	// Both x2 and x4 (the conflicting clause's falsified literals) sit
	// at decision level 1, and the seed level (s.currentLevel, also 1)
	// coincides with them, so the distinct-level count is exactly 1.
	if lbd != 1 {
		t.Errorf("lbd = %d, want 1 (x2 and x4 are both at decision level 1)", lbd)
	}
}

// TestLiteralRedundantDirectCase verifies literalRedundant's simplest
// case: candidate literal -2's reason clause {1,2} has one other
// literal (var 1), and var 1 is already "seen" (accounted for by
// analyze's own resolution, simulated here by setting s.seen[1]
// directly) -- so -2 must be redundant.
func TestLiteralRedundantDirectCase(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2, Clauses: []cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}}}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	s.level[1], s.level[2] = 1, 1
	s.reason[1] = noReason
	s.reason[2] = 0
	s.seen[1] = true

	if !s.literalRedundant(cnf.Literal(-2), 1000) {
		t.Error("expected -2 to be redundant: its reason {1,2}'s only other literal (var 1) is already seen")
	}
}

// TestLiteralRedundantRecursiveCase verifies the "recursive" half of
// STAGE36.md's request: candidate -2's reason {1,2} references var 1,
// which is NOT itself seen, but var 1's own reason {3,1} references
// var 3, which is seen -- this only succeeds if literalRedundant
// recurses into var 1's reason rather than stopping at the first
// level (a plain one-hop check would wrongly report -2 as not
// redundant here).
func TestLiteralRedundantRecursiveCase(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 3,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(2)},
			{cnf.Literal(3), cnf.Literal(1)},
		},
	}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	s.level[1], s.level[2], s.level[3] = 1, 1, 1
	s.reason[1] = 1 // clause {3,1}
	s.reason[2] = 0 // clause {1,2}
	s.reason[3] = noReason
	s.seen[3] = true

	if !s.literalRedundant(cnf.Literal(-2), 1000) {
		t.Error("expected -2 to be redundant via recursion through var 1's own reason clause")
	}
}

// TestLiteralRedundantBlockedByUncoveredDecision verifies that a
// candidate literal is NOT redundant when its reason clause depends on
// a decision variable (no reason) that isn't otherwise accounted for.
func TestLiteralRedundantBlockedByUncoveredDecision(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2, Clauses: []cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}}}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	s.level[1], s.level[2] = 1, 1
	s.reason[1] = noReason // an uncovered decision: not seen, no reason
	s.reason[2] = 0

	if s.literalRedundant(cnf.Literal(-2), 1000) {
		t.Error("expected -2 NOT to be redundant: var 1 is an uncovered decision")
	}
}

// TestLiteralRedundantRespectsZeroWorkBudget is STAGE36.md's own
// explicit "absolute time limit, just in case" concern, tested
// directly and deterministically (matching
// TestTrySubsumeFromGenericRespectsZeroWorkBudget's precedent) rather
// than trying to construct a clause large enough to exhaust the real
// default budget: even a genuinely redundant literal (the exact same
// setup as TestLiteralRedundantDirectCase) must come back as NOT
// redundant -- the safe, conservative fallback -- once the budget is
// exhausted, rather than panicking or ignoring the budget entirely.
func TestLiteralRedundantRespectsZeroWorkBudget(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2, Clauses: []cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}}}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	s.level[1], s.level[2] = 1, 1
	s.reason[1] = noReason
	s.reason[2] = 0
	s.seen[1] = true

	if s.literalRedundant(cnf.Literal(-2), 0) {
		t.Error("expected a zero work budget to force a conservative false, even though -2 is genuinely redundant")
	}
}

// TestAnalyzeMinimizesLearnedClause is a full analyze()-level
// integration test (solver state set directly rather than derived via
// real propagate() calls, for full control over the implication
// graph's shape): a conflict clause {-2,-3,-4} whose first-UIP
// resolution naturally derives learned = [-2,-1,-5] (asserting literal
// -2, from resolving var4 then var3 down to var2 as the UIP), where
// literal -1's reason is a decision (kept) and literal -5's reason
// {-1,5} references var 1 -- already seen, from resolving -1 into the
// clause along the way -- so -5 must be minimized away, leaving
// [-2,-1]. backtrackLevel and lbd must reflect the clause literalRedundant
// leaves behind, not the one first-UIP first derives.
func TestAnalyzeMinimizesLearnedClause(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 5,
		Clauses: []cnf.Clause{
			{cnf.Literal(-2), cnf.Literal(-3), cnf.Literal(-4)}, // 0: conflict
			{cnf.Literal(-1), cnf.Literal(4)},                   // 1: reason for var 4
			{cnf.Literal(-5), cnf.Literal(3)},                   // 2: reason for var 3
			{cnf.Literal(-1), cnf.Literal(5)},                   // 3: reason for var 5
		},
	}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	s.currentLevel = 2
	s.level[1], s.level[5] = 1, 1
	s.level[2], s.level[3], s.level[4] = 2, 2, 2
	s.reason[1] = noReason
	s.reason[5] = 3
	s.reason[3] = 2
	s.reason[4] = 1
	for _, v := range []int{1, 2, 3, 4, 5} {
		s.x[v] = assign.True
	}
	s.trail = []int{1, 5, 2, 3, 4}

	learned, backtrackLevel, lbd := s.analyze(0)

	want := cnf.Clause{cnf.Literal(-2), cnf.Literal(-1)}
	if fmt.Sprint(learned) != fmt.Sprint(want) {
		t.Errorf("learned = %v, want %v (literal -5 should have been minimized away)", learned, want)
	}
	if backtrackLevel != 1 {
		t.Errorf("backtrackLevel = %d, want 1 (level of the only surviving non-asserting literal, -1)", backtrackLevel)
	}
	if lbd != 2 {
		t.Errorf("lbd = %d, want 2 (currentLevel 2 and level 1, after minimization)", lbd)
	}
}

// TestAddLearnedClauseSkipsWatchesForUnitClause verifies that a
// length-1 learned clause is not added to the watched clause
// database (it has nowhere to shed a second watch onto, and needs
// none: it becomes a permanent level-0 fact instead).
func TestAddLearnedClauseSkipsWatchesForUnitClause(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2, Clauses: []cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}}}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	before := len(s.clauses)

	idx := s.addLearnedClause(cnf.Clause{cnf.Literal(-1)}, 1)
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
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	s.level[2] = 1
	s.level[3] = 3 // higher than variable 2's level

	idx := s.addLearnedClause(cnf.Clause{cnf.Literal(-1), cnf.Literal(2), cnf.Literal(3)}, 3)
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

	for _, variant := range []SelectVarVariant{SelectVarWeighted, SelectVarFast, SelectVarVsids, SelectVarLrb} {
		rng := rand.New(rand.NewPCG(1, 2))
		result := Run(problem, nil, variant, RestartNone, nil, rng, 0, params.Default().CDCL, PhaseSaving)
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

	for _, variant := range []SelectVarVariant{SelectVarWeighted, SelectVarFast, SelectVarVsids, SelectVarLrb} {
		rng := rand.New(rand.NewPCG(1, 2))
		result := Run(problem, nil, variant, RestartNone, nil, rng, 0, params.Default().CDCL, PhaseSaving)
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

	result := Run(problem, nil, SelectVarFast, RestartNone, nil, rng, 0, params.Default().CDCL, PhaseSaving)

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
	result := Run(satProblem, nil, SelectVarWeighted, RestartNone, nil, rng, 0, params.Default().CDCL, PhaseSaving)
	if !result.Satisfiable {
		t.Error("Run() reported unsatisfiable for an empty problem")
	}

	unsatProblem := &cnf.Problem{NumVars: 0, Clauses: []cnf.Clause{{}}}
	result = Run(unsatProblem, nil, SelectVarWeighted, RestartNone, nil, rng, 0, params.Default().CDCL, PhaseSaving)
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

	result := Run(problem, &tiny, SelectVarWeighted, RestartNone, nil, rng, 0, params.Default().CDCL, PhaseSaving)

	if !result.TimedOut {
		t.Error("expected TimedOut = true with a 1ns time limit")
	}
	if result.Satisfiable {
		t.Error("expected Satisfiable = false when timed out")
	}
}

// TestClauseByteCost verifies the memory-estimate formula directly.
func TestClauseByteCost(t *testing.T) {
	got := clauseByteCost(cnf.Clause{1, 2, 3})
	want := int64(perClauseOverheadBytes + 3*bytesPerLiteral)
	if got != want {
		t.Errorf("clauseByteCost() = %d, want %d", got, want)
	}
}

// TestReduceClauseDatabaseKeepsLockedAndActiveClauses exercises
// reduceClauseDatabase directly: given three learned clauses -- one
// locked (currently some variable's reason), one unlocked with low
// activity, and one unlocked with high activity -- only the unlocked,
// low-activity one should be deleted, and every remaining reference
// (reason[v] for the locked clause's variable, plus the occurrence
// lists) must still be correct afterward.
func TestReduceClauseDatabaseKeepsLockedAndActiveClauses(t *testing.T) {
	problem := &cnf.Problem{NumVars: 5, Clauses: []cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}}}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}

	// LBD 3 (above glueClauseLBDThreshold) on all three, so this test
	// exercises the activity tiebreak among equal-LBD clauses, not the
	// glue-clause protection (see TestReduceClauseDatabaseProtectsGlueClauses).
	idxLow := s.addLearnedClause(cnf.Clause{cnf.Literal(-1), cnf.Literal(3)}, 3)
	idxLocked := s.addLearnedClause(cnf.Clause{cnf.Literal(-2), cnf.Literal(4)}, 3)
	idxHigh := s.addLearnedClause(cnf.Clause{cnf.Literal(-3), cnf.Literal(5)}, 3)
	s.clauseActivity[idxLow] = 1.0
	s.clauseActivity[idxHigh] = 100.0

	s.x[4] = assign.True
	s.reason[4] = idxLocked

	before := len(s.clauses)
	s.reduceClauseDatabase()

	if len(s.clauses) != before-1 {
		t.Fatalf("len(s.clauses) = %d, want %d (exactly the low-activity clause removed)", len(s.clauses), before-1)
	}

	found := map[string]bool{}
	for _, c := range s.clauses {
		found[fmt.Sprint(c)] = true
	}
	if found[fmt.Sprint(cnf.Clause{cnf.Literal(-1), cnf.Literal(3)})] {
		t.Error("the unlocked, low-activity clause {-1,3} should have been deleted")
	}
	if !found[fmt.Sprint(cnf.Clause{cnf.Literal(-2), cnf.Literal(4)})] {
		t.Error("the locked clause {-2,4} should have survived")
	}
	if !found[fmt.Sprint(cnf.Clause{cnf.Literal(-3), cnf.Literal(5)})] {
		t.Error("the unlocked, high-activity clause {-3,5} should have survived")
	}

	if got := s.clauses[s.reason[4]]; fmt.Sprint(got) != fmt.Sprint(cnf.Clause{cnf.Literal(-2), cnf.Literal(4)}) {
		t.Errorf("reason[4] after reduction points to %v, want {-2,4}", got)
	}
	if len(s.lists.Negative[1]) != 0 {
		t.Errorf("lists.Negative[1] = %v, want empty (its only clause, {-1,3}, was deleted)", s.lists.Negative[1])
	}
}

// TestReduceClauseDatabaseNoOpWhenNothingEligible verifies that
// reduceClauseDatabase does nothing (and, importantly, does not
// panic) when every learned clause is currently locked.
func TestReduceClauseDatabaseNoOpWhenNothingEligible(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2, Clauses: []cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}}}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	idx := s.addLearnedClause(cnf.Clause{cnf.Literal(-1), cnf.Literal(2)}, 3)
	s.x[2] = assign.True
	s.reason[2] = idx

	before := len(s.clauses)
	s.reduceClauseDatabase()

	if len(s.clauses) != before {
		t.Errorf("len(s.clauses) = %d, want unchanged at %d", len(s.clauses), before)
	}
}

// TestRunWithTinyMemoryLimitStillProvesUnsatisfiablePigeonhole is the
// strongest available test of the whole reduction pipeline: a memory
// limit set far below the problem's own baseline size forces
// reduceClauseDatabase to run, and very likely actually delete
// clauses, on nearly every conflict -- exactly the index-remapping
// path (reason[], watch, occurrence lists all rebuilt together) that
// would be easiest to get subtly wrong. The verdict must still match
// Stage 11's (memory-limit-free) result for the same problem.
func TestRunWithTinyMemoryLimitStillProvesUnsatisfiablePigeonhole(t *testing.T) {
	problem := pigeonholeProblem(4, 3)
	rng := rand.New(rand.NewPCG(9, 9))
	limit := int64(200)

	result := Run(problem, nil, SelectVarFast, RestartNone, &limit, rng, 0, params.Default().CDCL, PhaseSaving)

	if result.Satisfiable {
		t.Fatal("Run() reported satisfiable for the 4-pigeon/3-hole problem")
	}
	if result.TimedOut {
		t.Error("Run() timed out with no time limit set")
	}
}

// TestRunWithMemoryLimitStillFindsSatisfiableFormula checks that a
// memory limit doesn't interfere with the satisfiable path: the
// returned assignment must still satisfy every clause.
func TestRunWithMemoryLimitStillFindsSatisfiableFormula(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 3,
		Clauses: []cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}, {cnf.Literal(-1), cnf.Literal(3)}, {cnf.Literal(-2), cnf.Literal(-3)}},
	}
	rng := rand.New(rand.NewPCG(1, 2))
	limit := int64(64)

	result := Run(problem, nil, SelectVarWeighted, RestartNone, &limit, rng, 0, params.Default().CDCL, PhaseSaving)

	if !result.Satisfiable {
		t.Fatal("Run() reported unsatisfiable for a satisfiable formula")
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
			t.Errorf("clause %d (%v) not satisfied by %v", ci, clause, result.Assignment)
		}
	}
}

// TestSelectVarByActivityPicksHighestScoringUnassignedVariable
// verifies the shared VSIDS/LRB selection helper directly: it must
// skip already-assigned variables and pick the highest score among
// the rest, ignoring ties in favor of whichever it finds first.
func TestSelectVarByActivityPicksHighestScoringUnassignedVariable(t *testing.T) {
	problem := &cnf.Problem{NumVars: 4, Clauses: []cnf.Clause{{1, 2}}}
	s, ok := newSolver(problem, nil, SelectVarVsids, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	s.x[1] = assign.True // no longer a candidate
	scores := []float64{0, 5.0, 9.0, 9.0, 3.0}

	got := s.selectVarByActivity(scores)
	if got != 2 {
		t.Errorf("selectVarByActivity() = %d, want 2 (highest score among unassigned variables)", got)
	}
}

// TestAnalyzeBumpsVsidsActivity checks that resolving through a
// conflict under SelectVarVsids bumps every variable touched along
// the way, using the same hand-verified formula as
// TestAnalyzeDerivesUnitClauseIndependentOfDecision.
func TestAnalyzeBumpsVsidsActivity(t *testing.T) {
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
	s, ok := newSolver(problem, nil, SelectVarVsids, RestartNone, params.Default().CDCL, PhaseSaving)
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
	s.analyze(confl)

	if s.varActivity[2] <= 0 {
		t.Errorf("varActivity[2] = %v, want > 0 (variable 2 is touched while resolving this conflict)", s.varActivity[2])
	}
	if s.varActivity[4] <= 0 {
		t.Errorf("varActivity[4] = %v, want > 0 (variable 4 is touched while resolving this conflict)", s.varActivity[4])
	}
}

// TestBacktrackToUpdatesLrbQ verifies LRB's core update directly: a
// variable assigned when numConflicts was 5, that participated in 3
// of the 5 conflicts that occurred before it was unassigned at
// numConflicts=10, should get Q = lrbAlpha * (3.0/5.0) (starting from
// Q=0, so the exponential moving average's "old value" term drops
// out), and its participated counter should reset to 0.
func TestBacktrackToUpdatesLrbQ(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2, Clauses: []cnf.Clause{{1, 2}}}
	s, ok := newSolver(problem, nil, SelectVarLrb, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}

	s.currentLevel = 1
	s.trailLim = append(s.trailLim, len(s.trail))
	s.numConflicts = 5
	s.assignLiteral(cnf.Literal(1), s.currentLevel, noReason)
	s.lrbParticipated[1] = 3
	s.numConflicts = 10

	s.backtrackTo(0)

	wantQ := s.params.LRBAlpha * (3.0 / 5.0)
	if math.Abs(s.lrbQ[1]-wantQ) > 1e-9 {
		t.Errorf("lrbQ[1] = %v, want %v", s.lrbQ[1], wantQ)
	}
	if s.lrbParticipated[1] != 0 {
		t.Errorf("lrbParticipated[1] = %d, want 0 (reset on unassignment)", s.lrbParticipated[1])
	}
}

// TestBacktrackToSavesPhase verifies STAGE14.md's core mechanism
// directly: a variable assigned True and then backtracked over
// should have its phase saved as True, regardless of SelectVar
// variant.
func TestBacktrackToSavesPhase(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2, Clauses: []cnf.Clause{{1, 2}}}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}

	s.currentLevel = 1
	s.trailLim = append(s.trailLim, len(s.trail))
	s.assignLiteral(cnf.Literal(1), s.currentLevel, noReason) // x1 = True

	s.backtrackTo(0)

	if s.savedPhase[1] != assign.True {
		t.Errorf("savedPhase[1] = %v, want True", s.savedPhase[1])
	}
}

// TestDecideGuessesSavedPhase verifies that decide consults
// savedPhase rather than always guessing False: with savedPhase[1]
// pre-set to True (as if variable 1 had previously been unassigned
// while True), the next decision on variable 1 (forced via
// SelectVarFast, which always picks the lowest-numbered unassigned
// variable) must assign it True, not False.
func TestDecideGuessesSavedPhase(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2, Clauses: []cnf.Clause{{1, 2}}}
	s, ok := newSolver(problem, nil, SelectVarFast, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	s.savedPhase[1] = assign.True

	s.decide(nil)

	if s.x[1] != assign.True {
		t.Errorf("x[1] = %v, want True (guessed from savedPhase)", s.x[1])
	}
}

// TestDecideDefaultsToFalseWithNoSavedPhase verifies the fallback: a
// variable that has never been assigned before (savedPhase still its
// zero value) is guessed False, matching every earlier stage's fixed
// decision order.
func TestDecideDefaultsToFalseWithNoSavedPhase(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2, Clauses: []cnf.Clause{{1, 2}}}
	s, ok := newSolver(problem, nil, SelectVarFast, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}

	s.decide(nil)

	if s.x[1] != assign.False {
		t.Errorf("x[1] = %v, want False (no saved phase yet)", s.x[1])
	}
}

// TestRunWithVsidsProvesUnsatisfiablePigeonhole and
// TestRunWithLrbProvesUnsatisfiablePigeonhole check the new
// heuristics end to end against a problem the older variants are
// already verified against (TestRunProvesUnsatisfiablePigeonhole),
// confirming they don't just avoid crashing but reach the correct
// verdict.
func TestRunWithVsidsProvesUnsatisfiablePigeonhole(t *testing.T) {
	problem := pigeonholeProblem(4, 3)
	rng := rand.New(rand.NewPCG(9, 9))

	result := Run(problem, nil, SelectVarVsids, RestartNone, nil, rng, 0, params.Default().CDCL, PhaseSaving)

	if result.Satisfiable {
		t.Fatal("Run() reported satisfiable for the 4-pigeon/3-hole problem")
	}
	if result.TimedOut {
		t.Error("Run() timed out with no time limit set")
	}
}

func TestRunWithLrbProvesUnsatisfiablePigeonhole(t *testing.T) {
	problem := pigeonholeProblem(4, 3)
	rng := rand.New(rand.NewPCG(9, 9))

	result := Run(problem, nil, SelectVarLrb, RestartNone, nil, rng, 0, params.Default().CDCL, PhaseSaving)

	if result.Satisfiable {
		t.Fatal("Run() reported satisfiable for the 4-pigeon/3-hole problem")
	}
	if result.TimedOut {
		t.Error("Run() timed out with no time limit set")
	}
}

// TestLubyTermMatchesHandVerifiedSequence verifies lubyTerm against
// the first seven terms of the Luby, Sinclair & Zuckerman sequence as
// STAGE15.md quotes them (1-indexed there; lubyTerm is 0-indexed, so
// term i here is STAGE15.md's term i+1): 1, 1, 2, 1, 1, 2, 4.
func TestLubyTermMatchesHandVerifiedSequence(t *testing.T) {
	want := []int{1, 1, 2, 1, 1, 2, 4}
	for i, w := range want {
		if got := lubyTerm(i); got != w {
			t.Errorf("lubyTerm(%d) = %d, want %d", i, got, w)
		}
	}
}

// TestLubyTermContinuesPastFirstBlock verifies the next block of the
// sequence (terms 8-15, 0-indexed 7-14): 1, 1, 2, 1, 1, 2, 4, 8 --
// the same first block again, followed by 8, per the recursive
// definition (t_i = 2^(k-1) exactly at the end of each doubling block).
func TestLubyTermContinuesPastFirstBlock(t *testing.T) {
	want := []int{1, 1, 2, 1, 1, 2, 4, 8}
	for offset, w := range want {
		i := 7 + offset
		if got := lubyTerm(i); got != w {
			t.Errorf("lubyTerm(%d) = %d, want %d", i, got, w)
		}
	}
}

// TestRestartThresholdLuby verifies restartThreshold's Luby case
// directly: threshold(k) = lubyBaseConflicts * lubyTerm(k).
func TestRestartThresholdLuby(t *testing.T) {
	s := &solver{restartStrategy: RestartLuby, params: params.Default().CDCL}
	for k, term := range []int{1, 1, 2, 1, 1, 2, 4} {
		s.restartCount = k
		want := s.params.LubyBaseConflicts * term
		if got := s.restartThreshold(); got != want {
			t.Errorf("restartThreshold() at restartCount=%d = %d, want %d", k, got, want)
		}
	}
}

// TestRestartThresholdPolynomial verifies restartThreshold's
// polynomial case directly against STAGE15.md's originally-specified
// sequence (there under the incorrect name "geometric"): threshold at
// restart index k (0-indexed) is polynomialBaseConflicts * (k+1)^2,
// matching a*1^2, a*2^2, a*3^2, ....
func TestRestartThresholdPolynomial(t *testing.T) {
	s := &solver{restartStrategy: RestartPolynomial, params: params.Default().CDCL}
	for k := 0; k < 4; k++ {
		s.restartCount = k
		want := s.params.PolynomialBaseConflicts * (k + 1) * (k + 1)
		if got := s.restartThreshold(); got != want {
			t.Errorf("restartThreshold() at restartCount=%d = %d, want %d", k, got, want)
		}
	}
}

// TestRestartThresholdGeometric verifies restartThreshold's true
// geometric case directly: threshold at restart index k (0-indexed)
// is geometricBaseConflicts * geometricGrowthFactor^k, a sequence
// with a constant ratio (geometricGrowthFactor) between consecutive
// terms, unlike the polynomial case above.
func TestRestartThresholdGeometric(t *testing.T) {
	s := &solver{restartStrategy: RestartGeometric, params: params.Default().CDCL}
	for k := 0; k < 4; k++ {
		s.restartCount = k
		want := int(float64(s.params.GeometricBaseConflicts) * math.Pow(s.params.GeometricGrowthFactor, float64(k)))
		if got := s.restartThreshold(); got != want {
			t.Errorf("restartThreshold() at restartCount=%d = %d, want %d", k, got, want)
		}
	}
	// Directly pin the first four terms against the known constants
	// (base 100, ratio 1.5), so a future change to the constants
	// themselves is caught by TestRestartThresholdGeometric's own
	// formula-based check above, while this pins the actual numbers
	// STAGE15.md's default configuration produces today.
	want := []int{100, 150, 225, 337}
	for k, w := range want {
		s.restartCount = k
		if got := s.restartThreshold(); got != w {
			t.Errorf("restartThreshold() at restartCount=%d = %d, want %d", k, got, w)
		}
	}
}

// TestMaybeRestartIsNoOpForRestartNone verifies that maybeRestart
// never triggers a restart when s.restartStrategy is RestartNone,
// regardless of how many conflicts have accumulated.
func TestMaybeRestartIsNoOpForRestartNone(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2, Clauses: []cnf.Clause{{1, 2}}}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	s.conflictsSinceRestart = 1_000_000

	s.maybeRestart(nil)

	if s.restartCount != 0 {
		t.Errorf("restartCount = %d, want 0 (RestartNone must never restart)", s.restartCount)
	}
	if s.conflictsSinceRestart != 1_000_000 {
		t.Errorf("conflictsSinceRestart = %d, want unchanged at 1000000", s.conflictsSinceRestart)
	}
}

// TestMaybeRestartTriggersAtThresholdAndResets verifies the actual
// restart mechanics for RestartLuby: below threshold, nothing
// happens; at or above it, backtrackTo(0) runs (undoing the level-1
// assignment), the conflict counter resets, and restartCount
// advances -- while the learned clause added beforehand survives the
// restart untouched, per STAGE15.md's explicit requirement.
func TestMaybeRestartTriggersAtThresholdAndResets(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2, Clauses: []cnf.Clause{{1, 2}}}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartLuby, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	learnedIdx := s.addLearnedClause(cnf.Clause{cnf.Literal(-1), cnf.Literal(2)}, 3)
	beforeClauses := len(s.clauses)

	s.currentLevel = 1
	s.trailLim = append(s.trailLim, len(s.trail))
	s.assignLiteral(cnf.Literal(1), s.currentLevel, noReason)

	s.conflictsSinceRestart = s.params.LubyBaseConflicts*lubyTerm(0) - 1
	s.maybeRestart(nil)
	if s.restartCount != 0 {
		t.Fatalf("restartCount = %d, want 0 (below threshold, must not restart yet)", s.restartCount)
	}
	if s.x[1] == assign.Unassigned {
		t.Fatal("variable 1 was unassigned before the threshold was reached")
	}

	s.conflictsSinceRestart++
	s.maybeRestart(nil)

	if s.restartCount != 1 {
		t.Errorf("restartCount = %d, want 1 (threshold reached)", s.restartCount)
	}
	if s.conflictsSinceRestart != 0 {
		t.Errorf("conflictsSinceRestart = %d, want reset to 0", s.conflictsSinceRestart)
	}
	if s.currentLevel != 0 {
		t.Errorf("currentLevel = %d, want 0 after restart", s.currentLevel)
	}
	if s.x[1] != assign.Unassigned {
		t.Errorf("x[1] = %v, want Unassigned after restart", s.x[1])
	}
	if len(s.clauses) != beforeClauses {
		t.Errorf("len(s.clauses) = %d, want unchanged at %d (learned clauses must survive a restart)", len(s.clauses), beforeClauses)
	}
	if s.clauses[learnedIdx][0] != cnf.Literal(-1) {
		t.Errorf("learned clause at %d was disturbed by the restart", learnedIdx)
	}
}

// TestRunWithRestartsStillProvesUnsatisfiablePigeonhole checks both
// restart strategies end to end against a problem already verified
// without restarts (TestRunProvesUnsatisfiablePigeonhole), confirming
// restarts (which repeatedly discard the decision stack but must keep
// every learned clause) don't change the verdict.
func TestRunWithRestartsStillProvesUnsatisfiablePigeonhole(t *testing.T) {
	for _, restartStrategy := range []RestartStrategy{RestartLuby, RestartPolynomial, RestartGeometric} {
		problem := pigeonholeProblem(4, 3)
		rng := rand.New(rand.NewPCG(9, 9))

		result := Run(problem, nil, SelectVarFast, restartStrategy, nil, rng, 0, params.Default().CDCL, PhaseSaving)

		if result.Satisfiable {
			t.Errorf("restart strategy %v: Run() reported satisfiable for the 4-pigeon/3-hole problem", restartStrategy)
		}
		if result.TimedOut {
			t.Errorf("restart strategy %v: Run() timed out with no time limit set", restartStrategy)
		}
	}
}

// TestRunWithRestartsStillFindsSatisfiableFormula is the satisfiable-
// path analog of TestRunWithRestartsStillProvesUnsatisfiablePigeonhole.
func TestRunWithRestartsStillFindsSatisfiableFormula(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 3,
		Clauses: []cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}, {cnf.Literal(-1), cnf.Literal(3)}, {cnf.Literal(-2), cnf.Literal(-3)}},
	}

	for _, restartStrategy := range []RestartStrategy{RestartLuby, RestartPolynomial, RestartGeometric} {
		rng := rand.New(rand.NewPCG(1, 2))
		result := Run(problem, nil, SelectVarVsids, restartStrategy, nil, rng, 0, params.Default().CDCL, PhaseSaving)
		if !result.Satisfiable {
			t.Fatalf("restart strategy %v: Run() reported unsatisfiable for a satisfiable formula", restartStrategy)
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
				t.Errorf("restart strategy %v: clause %d (%v) not satisfied by %v", restartStrategy, ci, clause, result.Assignment)
			}
		}
	}
}

// TestReduceClauseDatabaseProtectsGlueClauses verifies STAGE34.md's
// glue-clause protection: a clause at glueClauseLBDThreshold, given
// the lowest possible activity (0.0, the single most "deletable"
// value under the old pure-activity policy), must still survive,
// while the correct clause -- the lower-activity one among the
// remaining, non-glue-eligible clauses -- is deleted instead.
func TestReduceClauseDatabaseProtectsGlueClauses(t *testing.T) {
	problem := &cnf.Problem{NumVars: 6, Clauses: []cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}}}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}

	idxGlue := s.addLearnedClause(cnf.Clause{cnf.Literal(-1), cnf.Literal(3)}, s.params.GlueClauseLBDThreshold)
	idxNonGlueLow := s.addLearnedClause(cnf.Clause{cnf.Literal(-2), cnf.Literal(4)}, s.params.GlueClauseLBDThreshold+1)
	idxNonGlueHigh := s.addLearnedClause(cnf.Clause{cnf.Literal(-5), cnf.Literal(6)}, s.params.GlueClauseLBDThreshold+1)
	s.clauseActivity[idxGlue] = 0.0
	s.clauseActivity[idxNonGlueLow] = 0.5
	s.clauseActivity[idxNonGlueHigh] = 100.0

	before := len(s.clauses)
	s.reduceClauseDatabase()

	if len(s.clauses) != before-1 {
		t.Fatalf("len(s.clauses) = %d, want %d (exactly one clause removed)", len(s.clauses), before-1)
	}
	found := map[string]bool{}
	for _, c := range s.clauses {
		found[fmt.Sprint(c)] = true
	}
	if !found[fmt.Sprint(cnf.Clause{cnf.Literal(-1), cnf.Literal(3)})] {
		t.Error("glue clause {-1,3} should have survived despite its rock-bottom activity")
	}
	if found[fmt.Sprint(cnf.Clause{cnf.Literal(-2), cnf.Literal(4)})] {
		t.Error("non-glue clause {-2,4} (lower activity among the non-glue clauses) should have been deleted")
	}
	if !found[fmt.Sprint(cnf.Clause{cnf.Literal(-5), cnf.Literal(6)})] {
		t.Error("non-glue clause {-5,6} (higher activity) should have survived")
	}
}

// TestReduceClauseDatabaseSortsByLBDBeforeActivity verifies that,
// among eligible clauses, a higher LBD is deleted before a lower one
// even when the higher-LBD clause has much higher activity -- LBD is
// the primary sort key, activity only a tiebreak among equal LBDs.
func TestReduceClauseDatabaseSortsByLBDBeforeActivity(t *testing.T) {
	problem := &cnf.Problem{NumVars: 4, Clauses: []cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}}}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}

	idxHighLBDHighActivity := s.addLearnedClause(cnf.Clause{cnf.Literal(-1), cnf.Literal(3)}, 10)
	idxLowLBDLowActivity := s.addLearnedClause(cnf.Clause{cnf.Literal(-2), cnf.Literal(4)}, 3)
	s.clauseActivity[idxHighLBDHighActivity] = 1000.0
	s.clauseActivity[idxLowLBDLowActivity] = 0.0

	before := len(s.clauses)
	s.reduceClauseDatabase()

	if len(s.clauses) != before-1 {
		t.Fatalf("len(s.clauses) = %d, want %d (exactly one clause removed)", len(s.clauses), before-1)
	}
	found := map[string]bool{}
	for _, c := range s.clauses {
		found[fmt.Sprint(c)] = true
	}
	if found[fmt.Sprint(cnf.Clause{cnf.Literal(-1), cnf.Literal(3)})] {
		t.Error("the high-LBD clause {-1,3} should have been deleted despite its high activity")
	}
	if !found[fmt.Sprint(cnf.Clause{cnf.Literal(-2), cnf.Literal(4)})] {
		t.Error("the low-LBD clause {-2,4} should have survived despite its low activity")
	}
}

// TestGlucoseShouldRestart exercises glucoseShouldRestart directly
// with a fabricated sequence of LBDs: it must not trigger before the
// recent window is full, and its trigger decision afterward must
// match a hand-computed recentAvg*glucoseK >= globalAvg comparison.
func TestGlucoseShouldRestart(t *testing.T) {
	problem := &cnf.Problem{NumVars: 1, Clauses: nil}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartGlucose, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}

	// Fill all but one slot of the recent window with LBD 2 (low/good);
	// the window isn't full yet, so this must never trigger regardless
	// of how bad a single additional LBD looks.
	for i := 0; i < len(s.lbdRecentBuf)-1; i++ {
		s.recordLBD(2)
		if s.glucoseShouldRestart() {
			t.Fatalf("glucoseShouldRestart() = true before the recent window (size %d) is full (i=%d)", len(s.lbdRecentBuf), i)
		}
	}

	// One more low-LBD conflict fills the window at a recent average of
	// 2, equal to the global average (also 2) -- recentAvg*glucoseK
	// (2*0.8=1.6) is well below globalAvg (2), so no restart yet.
	s.recordLBD(2)
	if s.glucoseShouldRestart() {
		t.Fatal("glucoseShouldRestart() = true with recent and global averages both at their best (LBD 2)")
	}

	// Now drive the recent window to a much worse (higher) LBD than the
	// global history: after glucoseWindowSize more conflicts at LBD 20,
	// the recent window average is 20 (all glucoseWindowSize slots hold
	// 20), while the global average is dragged only partway up, so
	// recentAvg*glucoseK must exceed it and a restart must be signaled.
	for i := 0; i < len(s.lbdRecentBuf); i++ {
		s.recordLBD(20)
	}
	recentAvg := float64(s.lbdRecentSum) / float64(len(s.lbdRecentBuf))
	globalAvg := float64(s.lbdGlobalSum) / float64(s.lbdGlobalCount)
	want := recentAvg*s.params.GlucoseK >= globalAvg
	if got := s.glucoseShouldRestart(); got != want {
		t.Errorf("glucoseShouldRestart() = %v, want %v (recentAvg=%v globalAvg=%v)", got, want, recentAvg, globalAvg)
	}
	if !want {
		t.Fatal("test setup error: expected the LBD-20 run to make recentAvg*glucoseK >= globalAvg true")
	}
}

// TestMaybeRestartDoesNotPanicAtRootLevel is a regression test for a
// bug this stage's own benchcompare sweep found (STAGE34.md): a
// restart signaled immediately after a conflict resolves to a
// level-0 learned unit clause -- s.currentLevel is already 0 by then
// -- used to panic inside backtrackTo(0), which indexes
// s.trailLim[1], an index that only exists when s.currentLevel > 0.
// RestartGlucose's much higher restart frequency (roughly every
// glucoseWindowSize conflicts, versus the fixed strategies' bases in
// the hundreds to tens of thousands) made this reachable on real
// benchmark files (benchmark/blocksworld/bw_large.{c,d}.cnf);
// maybeRestart now returns immediately whenever s.currentLevel == 0,
// regardless of strategy, since there is nothing to restart from the
// root anyway.
func TestMaybeRestartDoesNotPanicAtRootLevel(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2, Clauses: []cnf.Clause{{1, 2}}}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartGlucose, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	// s.currentLevel is 0 here (freshly constructed, no decisions
	// made yet) -- exactly the state learnAndBackjump leaves it in
	// right after a level-0 learned unit clause.
	if s.currentLevel != 0 {
		t.Fatalf("test setup error: currentLevel = %d, want 0", s.currentLevel)
	}
	// Force glucoseShouldRestart() to report true, so maybeRestart
	// actually attempts a restart rather than skipping for an
	// unrelated reason: establish a low-LBD baseline global average,
	// then drive the recent window to a much worse LBD (mirroring
	// TestGlucoseShouldRestart's own setup).
	for i := 0; i < len(s.lbdRecentBuf); i++ {
		s.recordLBD(2)
	}
	for i := 0; i < len(s.lbdRecentBuf); i++ {
		s.recordLBD(20)
	}
	if !s.glucoseShouldRestart() {
		t.Fatal("test setup error: expected glucoseShouldRestart() = true")
	}

	conflictsBefore := s.conflictsSinceRestart
	restartCountBefore := s.restartCount

	s.maybeRestart(nil) // must not panic

	if s.restartCount != restartCountBefore {
		t.Errorf("restartCount = %d, want unchanged at %d (no restart actually happens at the root)", s.restartCount, restartCountBefore)
	}
	if s.conflictsSinceRestart != conflictsBefore {
		t.Errorf("conflictsSinceRestart = %d, want unchanged at %d", s.conflictsSinceRestart, conflictsBefore)
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
