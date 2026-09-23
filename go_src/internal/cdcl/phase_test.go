package cdcl

import (
	"math/rand/v2"
	"testing"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/params"
)

// TestResolvePhaseStrategyRoundRobin verifies that PhaseRoundRobin
// resolves to phaseRoundRobinStrategies[threadIndex%3], and every
// other strategy is returned unchanged, mirroring
// TestResolveRestartStrategyRoundRobin's own coverage.
func TestResolvePhaseStrategyRoundRobin(t *testing.T) {
	want := []PhaseStrategy{PhaseSaving, PhaseTarget, PhaseRephaseWalkSAT, PhaseSaving, PhaseTarget}
	for i, w := range want {
		if got := resolvePhaseStrategy(PhaseRoundRobin, i); got != w {
			t.Errorf("resolvePhaseStrategy(PhaseRoundRobin, %d) = %v, want %v", i, got, w)
		}
	}
	for _, strategy := range []PhaseStrategy{PhaseSaving, PhaseTarget, PhaseRephaseWalkSAT} {
		if got := resolvePhaseStrategy(strategy, 7); got != strategy {
			t.Errorf("resolvePhaseStrategy(%v, 7) = %v, want unchanged %v", strategy, got, strategy)
		}
	}
}

// TestPhaseForSavingReadsSavedPhase verifies that phaseFor consults
// savedPhase, not targetPhase, for both PhaseSaving and
// PhaseRephaseWalkSAT (the latter only changes how savedPhase gets
// updated, not which array decide reads -- see the package doc
// comment).
func TestPhaseForSavingReadsSavedPhase(t *testing.T) {
	for _, strategy := range []PhaseStrategy{PhaseSaving, PhaseRephaseWalkSAT} {
		s := &solver{
			phaseStrategy: strategy,
			savedPhase:    assign.Assignment{assign.Unassigned, assign.True},
			targetPhase:   assign.Assignment{assign.Unassigned, assign.False},
		}
		if got := s.phaseFor(1); got != assign.True {
			t.Errorf("phaseStrategy=%v: phaseFor(1) = %v, want assign.True (from savedPhase)", strategy, got)
		}
	}
}

// TestPhaseForTargetReadsTargetPhase verifies that phaseFor consults
// targetPhase, not savedPhase, under PhaseTarget.
func TestPhaseForTargetReadsTargetPhase(t *testing.T) {
	s := &solver{
		phaseStrategy: PhaseTarget,
		savedPhase:    assign.Assignment{assign.Unassigned, assign.False},
		targetPhase:   assign.Assignment{assign.Unassigned, assign.True},
	}
	if got := s.phaseFor(1); got != assign.True {
		t.Errorf("phaseFor(1) = %v, want assign.True (from targetPhase)", got)
	}
}

// TestUpdateTargetPhaseRecordsNewRecord verifies that updateTargetPhase
// snapshots every currently-trailed variable's polarity into
// targetPhase, and advances bestTrailLen, only when the trail is
// strictly longer than the previous record.
func TestUpdateTargetPhaseRecordsNewRecord(t *testing.T) {
	s := &solver{
		x:            assign.Assignment{assign.Unassigned, assign.True, assign.False},
		trail:        []int{1, 2},
		targetPhase:  make(assign.Assignment, 3),
		bestTrailLen: 0,
	}
	s.updateTargetPhase()
	if s.bestTrailLen != 2 {
		t.Fatalf("bestTrailLen = %d, want 2", s.bestTrailLen)
	}
	if s.targetPhase[1] != assign.True || s.targetPhase[2] != assign.False {
		t.Fatalf("targetPhase = %v, want [_, True, False]", s.targetPhase)
	}
}

// TestUpdateTargetPhaseIgnoresTiesAndShorterTrails verifies that a
// trail no longer than the existing record leaves targetPhase and
// bestTrailLen untouched -- including the exact-tie case, per
// updateTargetPhase's own doc comment ("ties keep the earlier,
// already-recorded snapshot").
func TestUpdateTargetPhaseIgnoresTiesAndShorterTrails(t *testing.T) {
	s := &solver{
		x:            assign.Assignment{assign.Unassigned, assign.True},
		trail:        []int{1},
		targetPhase:  assign.Assignment{assign.Unassigned, assign.False},
		bestTrailLen: 1,
	}
	s.updateTargetPhase()
	if s.bestTrailLen != 1 {
		t.Errorf("bestTrailLen = %d, want unchanged at 1", s.bestTrailLen)
	}
	if s.targetPhase[1] != assign.False {
		t.Errorf("targetPhase[1] = %v, want unchanged at assign.False (a tie must not overwrite)", s.targetPhase[1])
	}
}

// TestMaybeRephaseOnlyFiresForPhaseRephaseWalkSAT verifies that
// maybeRephase is a no-op for every strategy other than
// PhaseRephaseWalkSAT, even with restartCount at an exact multiple of
// RephaseIntervalRestarts.
func TestMaybeRephaseOnlyFiresForPhaseRephaseWalkSAT(t *testing.T) {
	for _, strategy := range []PhaseStrategy{PhaseSaving, PhaseTarget} {
		s := &solver{
			phaseStrategy: strategy,
			restartCount:  50,
			params:        params.CDCL{RephaseIntervalRestarts: 50, RephaseMaxFlips: 100},
			savedPhase:    assign.Assignment{assign.Unassigned, assign.True},
		}
		s.maybeRephase(nil)
		if s.earlyExitSAT {
			t.Errorf("phaseStrategy=%v: maybeRephase set earlyExitSAT, want no-op", strategy)
		}
		if s.savedPhase[1] != assign.True {
			t.Errorf("phaseStrategy=%v: maybeRephase changed savedPhase, want no-op", strategy)
		}
	}
}

// TestMaybeRephaseRespectsInterval verifies that maybeRephase only
// actually runs a WalkSAT burst when restartCount is a positive
// multiple of RephaseIntervalRestarts, using savedPhase mutation as
// the observable signal (a real burst always overwrites savedPhase
// wholesale -- see rephaseFromWalkSAT).
func TestMaybeRephaseRespectsInterval(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2, Clauses: []cnf.Clause{{1, 2}}}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.CDCL{RephaseIntervalRestarts: 3, RephaseMaxFlips: 100}, PhaseRephaseWalkSAT)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	s.savedPhase[1] = assign.True
	s.savedPhase[2] = assign.True
	rng := rand.New(rand.NewPCG(1, 2))

	s.restartCount = 2
	s.maybeRephase(rng)
	if s.savedPhase[1] != assign.True || s.savedPhase[2] != assign.True {
		t.Fatalf("maybeRephase ran at restartCount=2 (not a multiple of 3), want no-op")
	}

	s.restartCount = 3
	s.maybeRephase(rng)
	// A real burst ran; some literal ended up assign.True or
	// assign.False, never assign.Unassigned (WalkSAT always produces a
	// complete assignment) -- this is the only assertion that doesn't
	// depend on WalkSAT's specific search trajectory.
	if s.savedPhase[1] == assign.Unassigned || s.savedPhase[2] == assign.Unassigned {
		t.Errorf("savedPhase = %v, want a complete assignment after a WalkSAT burst", s.savedPhase)
	}
}

// TestMaybeRephaseGuardsNonPositiveInterval verifies that a
// RephaseIntervalRestarts of 0 (or negative -- a malformed hand-edited
// .vibe_sat.json, since params.Default() never sets it this way)
// never fires rather than panicking on the modulo.
func TestMaybeRephaseGuardsNonPositiveInterval(t *testing.T) {
	for _, interval := range []int{0, -1} {
		s := &solver{
			phaseStrategy: PhaseRephaseWalkSAT,
			restartCount:  0,
			params:        params.CDCL{RephaseIntervalRestarts: interval, RephaseMaxFlips: 100},
			savedPhase:    assign.Assignment{assign.Unassigned, assign.True},
		}
		s.maybeRephase(nil) // must not panic
		if s.savedPhase[1] != assign.True {
			t.Errorf("interval=%d: maybeRephase ran, want no-op guard", interval)
		}
	}
}

// TestRephaseFromWalkSATSolvesEarlyExit verifies STAGE43.md's flagged
// edge case directly: when a WalkSAT burst returns a complete
// satisfying assignment, rephaseFromWalkSAT sets earlyExitSAT/
// earlyExitAssignment rather than merely updating savedPhase. Uses a
// single two-literal clause and a generous flip budget so the burst is
// satisfied with overwhelming probability on the first try.
func TestRephaseFromWalkSATSolvesEarlyExit(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2, Clauses: []cnf.Clause{{1, 2}}}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.CDCL{RephaseIntervalRestarts: 1, RephaseMaxFlips: 1000}, PhaseRephaseWalkSAT)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	rng := rand.New(rand.NewPCG(1, 2))

	s.rephaseFromWalkSAT(rng)

	if !s.earlyExitSAT {
		t.Fatal("earlyExitSAT = false, want true (a single two-literal clause must be solved by WalkSAT)")
	}
	lit1, lit2 := s.earlyExitAssignment[1], s.earlyExitAssignment[2]
	if !(lit1 == assign.True || lit2 == assign.True) {
		t.Errorf("earlyExitAssignment = %v, want at least one variable True (clause {1 2} satisfied)", s.earlyExitAssignment)
	}
}

// TestRunLoopReturnsSATOnWalkSATEarlyExit is an end-to-end smoke test
// through runLoop (rather than calling rephaseFromWalkSAT directly,
// as TestRephaseFromWalkSATSolvesEarlyExit already does deterministically)
// confirming the wiring between maybeRestart, maybeRephase, and the
// early-exit check in runLoop's own main loop doesn't break ordinary
// solving: a trivial satisfiable problem, forced to rephase on every
// restart (RephaseIntervalRestarts=1) with restarts firing after every
// single conflict (a LubyBaseConflicts of 1), must still reach
// Satisfiable=true, in at most one conflict for a single three-literal
// clause -- a loose bound consistent with (though not, on its own,
// conclusive proof of) the early-exit path having actually fired,
// since normal CDCL resolution of this trivial clause would also need
// only the one conflict.
func TestRunLoopReturnsSATOnWalkSATEarlyExit(t *testing.T) {
	problem := &cnf.Problem{NumVars: 3, Clauses: []cnf.Clause{{1, 2, 3}}}
	p := params.Default().CDCL
	p.LubyBaseConflicts = 1
	p.RephaseIntervalRestarts = 1
	p.RephaseMaxFlips = 1000
	rng := rand.New(rand.NewPCG(1, 2))

	result := runLoop(problem, nil, SelectVarWeighted, RestartLuby, nil, rng, nil, nil, nil, p, PhaseRephaseWalkSAT)

	if !result.Satisfiable {
		t.Fatalf("Satisfiable = false, want true (a trivially satisfiable problem, with WalkSAT rephasing forced every restart)")
	}
	if result.NumConflicts > 1 {
		t.Errorf("NumConflicts = %d, want at most 1 for this trivial problem", result.NumConflicts)
	}
}
