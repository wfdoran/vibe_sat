package cdcl

import (
	"math/rand/v2"
	"testing"
	"time"

	"vibe_sat/internal/cnf"
	"vibe_sat/internal/params"
)

// TestSwitchSolverStartsFocusedAndFlipsAtEveryRestart checks STAGE66.md's
// mode switching: a SelectVarSwitch solver starts as VMTF with Glucose
// restarts, and each restart flips it to VSIDS with quadratic restarts and
// back. Only the stable stretches advance the quadratic schedule's index.
func TestSwitchSolverStartsFocusedAndFlipsAtEveryRestart(t *testing.T) {
	problem := pigeonholeProblem(5, 5)
	s, ok, _ := newSolver(problem, nil, SelectVarSwitch, RestartLuby, params.Default().CDCL, PhaseSaving, nil, time.Time{})
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	rng := rand.New(rand.NewPCG(1, 1))
	if s.variant != SelectVarVmtf || s.restartStrategy != RestartGlucose || !s.switching {
		t.Fatalf("initial mode = variant %d strategy %d switching %v, want VMTF/Glucose/true", s.variant, s.restartStrategy, s.switching)
	}
	for i := 0; i < 6; i++ {
		wantFocusedBefore := i%2 == 0
		if (s.variant == SelectVarVmtf) != wantFocusedBefore {
			t.Fatalf("before restart %d: variant %d", i, s.variant)
		}
		s.decide(rng)
		s.restart(rng)
		if wantFocusedBefore && (s.variant != SelectVarVsids || s.restartStrategy != RestartPolynomial) {
			t.Fatalf("restart %d: got variant %d strategy %d, want VSIDS/quadratic", i, s.variant, s.restartStrategy)
		}
		if !wantFocusedBefore && (s.variant != SelectVarVmtf || s.restartStrategy != RestartGlucose) {
			t.Fatalf("restart %d: got variant %d strategy %d, want VMTF/Glucose", i, s.variant, s.restartStrategy)
		}
		if want := (i + 1) / 2; s.scheduleIndex() != want {
			t.Fatalf("after restart %d: scheduleIndex = %d, want %d", i, s.scheduleIndex(), want)
		}
	}
	if s.restartCount != 6 {
		t.Fatalf("restartCount = %d, want 6", s.restartCount)
	}
	checkVmtfCursor(t, s)
}

// TestSwitchKeepsVmtfCursorValidWhileStable checks that backtracking in
// the stable (VSIDS) mode still maintains the VMTF cursor, since the list
// is used again at the next flip.
func TestSwitchKeepsVmtfCursorValidWhileStable(t *testing.T) {
	problem := pigeonholeProblem(5, 5)
	s, ok, _ := newSolver(problem, nil, SelectVarSwitch, RestartNone, params.Default().CDCL, PhaseSaving, nil, time.Time{})
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	rng := rand.New(rand.NewPCG(2, 2))
	s.flipMode() // stable
	for i := 0; i < 3; i++ {
		s.decide(rng)
	}
	s.backtrackTo(0)
	checkVmtfCursor(t, s)
	vmtfOrder(t, s)
}

// TestRunSwitchAgreesWithBruteForce fuzzes the single-threaded switching
// mode against exhaustive enumeration, with restart parameters small
// enough that many mode flips happen.
func TestRunSwitchAgreesWithBruteForce(t *testing.T) {
	p := params.Default().CDCL
	p.PolynomialBaseConflicts = 1
	p.GlucoseWindowSize = 2
	rng := rand.New(rand.NewPCG(66, 66))
	for trial := 0; trial < 150; trial++ {
		numVars := 8 + rng.IntN(5)
		numClauses := int(4.3 * float64(numVars))
		var clauses []cnf.Clause
		for len(clauses) < numClauses {
			var clause cnf.Clause
			used := map[int]bool{}
			for len(clause) < 3 {
				v := 1 + rng.IntN(numVars)
				if used[v] {
					continue
				}
				used[v] = true
				lit := cnf.Literal(v)
				if rng.IntN(2) == 0 {
					lit = -lit
				}
				clause = append(clause, lit)
			}
			clauses = append(clauses, clause)
		}
		problem := &cnf.Problem{NumVars: numVars, Clauses: clauses}
		want := vmtfBruteForceSatisfiable(numVars, clauses)
		result := Run(problem, nil, SelectVarSwitch, RestartNone, nil, rand.New(rand.NewPCG(uint64(trial), 7)), 0, p, PhaseSaving)
		if result.TimedOut || result.Satisfiable != want {
			t.Fatalf("trial %d: Run satisfiable=%v timedOut=%v, brute force says %v\n%v", trial, result.Satisfiable, result.TimedOut, want, clauses)
		}
		if result.Satisfiable {
			for ci, clause := range clauses {
				sat := false
				for _, lit := range clause {
					if result.Assignment.LiteralIsTrue(lit) {
						sat = true
					}
				}
				if !sat {
					t.Fatalf("trial %d: clause %d (%v) not satisfied", trial, ci, clause)
				}
			}
		}
	}
}

// TestRunSwitchProvesUnsatisfiablePigeonhole checks that switching
// modes still proves a formula that needs many conflicts UNSAT.
func TestRunSwitchProvesUnsatisfiablePigeonhole(t *testing.T) {
	p := params.Default().CDCL
	p.PolynomialBaseConflicts = 3
	result := Run(pigeonholeProblem(6, 5), nil, SelectVarSwitch, RestartNone, nil, rand.New(rand.NewPCG(9, 9)), 0, p, PhaseSaving)
	if result.Satisfiable || result.TimedOut {
		t.Fatalf("satisfiable=%v timedOut=%v, want a proven UNSAT", result.Satisfiable, result.TimedOut)
	}
}

// TestSwitchModeAssignment checks the multi-threaded split: the focused
// count is round(fraction*n) clamped to [1, n-1], and the same seed picks
// the same threads.
func TestSwitchModeAssignment(t *testing.T) {
	cases := []struct {
		n        int
		fraction float64
		want     int
	}{
		{2, 0.5, 1}, {2, 0, 1}, {2, 1, 1},
		{4, 0.5, 2}, {4, 0.25, 1}, {4, 0.75, 3}, {4, 0.01, 1}, {4, 0.99, 3},
		{5, 0.5, 3}, {8, 0.5, 4}, {8, 1, 7}, {8, 0, 1}, {3, 0.5, 2},
	}
	for _, c := range cases {
		got := switchModeAssignment(c.n, c.fraction, rand.New(rand.NewPCG(5, 5)))
		count := 0
		for _, f := range got {
			if f {
				count++
			}
		}
		if len(got) != c.n || count != c.want {
			t.Errorf("n=%d fraction=%v: %d focused of %d, want %d of %d", c.n, c.fraction, count, len(got), c.want, c.n)
		}
		again := switchModeAssignment(c.n, c.fraction, rand.New(rand.NewPCG(5, 5)))
		for i := range got {
			if got[i] != again[i] {
				t.Errorf("n=%d fraction=%v: same seed gave different assignments", c.n, c.fraction)
				break
			}
		}
	}

	// Different seeds must be able to pick different threads.
	seen := map[string]bool{}
	for seed := uint64(0); seed < 40; seed++ {
		a := switchModeAssignment(6, 0.5, rand.New(rand.NewPCG(seed, 1)))
		key := ""
		for _, f := range a {
			if f {
				key += "F"
			} else {
				key += "S"
			}
		}
		seen[key] = true
	}
	if len(seen) < 5 {
		t.Errorf("only %d distinct assignments over 40 seeds; selection does not look random", len(seen))
	}
}

// TestRunParallelWithSwitch runs the multi-threaded switching mode on an
// UNSAT and a SAT formula, with the extreme fractions as well.
func TestRunParallelWithSwitch(t *testing.T) {
	for _, fraction := range []float64{0, 0.5, 1} {
		p := params.Default().CDCL
		p.VmtfThreadFraction = fraction

		result := RunParallel(pigeonholeProblem(6, 5), nil, SelectVarSwitch, RestartRoundRobin, nil, 4, rand.New(rand.NewPCG(3, 3)), 0, p, PhaseRoundRobin)
		if result.Satisfiable || result.TimedOut {
			t.Fatalf("fraction %v pigeonhole: satisfiable=%v timedOut=%v, want a proven UNSAT", fraction, result.Satisfiable, result.TimedOut)
		}

		sat := pigeonholeProblem(5, 5)
		result = RunParallel(sat, nil, SelectVarSwitch, RestartRoundRobin, nil, 4, rand.New(rand.NewPCG(4, 4)), 0, p, PhaseRoundRobin)
		if !result.Satisfiable {
			t.Fatalf("fraction %v: 5 pigeons in 5 holes reported unsatisfiable", fraction)
		}
		for ci, clause := range sat.Clauses {
			ok := false
			for _, lit := range clause {
				if result.Assignment.LiteralIsTrue(lit) {
					ok = true
				}
			}
			if !ok {
				t.Fatalf("fraction %v: clause %d (%v) not satisfied", fraction, ci, clause)
			}
		}
	}
}
