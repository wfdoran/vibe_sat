package cdcl

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"vibe_sat/internal/cnf"
	"vibe_sat/internal/params"
)

func newReduceTestSolver(t *testing.T, numVars int, p params.CDCL) *solver {
	t.Helper()
	problem := &cnf.Problem{NumVars: numVars, Clauses: []cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}}}
	s, ok, _ := newSolver(problem, nil, SelectVarWeighted, RestartNone, p, PhaseSaving, nil, time.Time{})
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	return s
}

func clauseSet(s *solver) map[string]bool {
	found := map[string]bool{}
	for _, c := range s.clauses {
		found[fmt.Sprint(c)] = true
	}
	return found
}

// TestReduceTiersProtectRecentlyUsedClauses checks STAGE67.md's tiers: a
// tier-2 clause (LBD <= Tier2LBD) is protected for Tier2UsedReductions
// reductions after it is learned or used, a tier-3 clause for
// Tier3UsedReductions, and the protection ages by one per reduction.
func TestReduceTiersProtectRecentlyUsedClauses(t *testing.T) {
	p := params.ScheduledDatabasePolicy(params.Default().CDCL)
	p.ReduceFraction = 1 // delete every unprotected candidate
	s := newReduceTestSolver(t, 8, p)

	tier1 := s.addLearnedClause(cnf.Clause{cnf.Literal(-1), cnf.Literal(3)}, p.GlueClauseLBDThreshold)
	tier2 := s.addLearnedClause(cnf.Clause{cnf.Literal(-2), cnf.Literal(4)}, p.Tier2LBD)
	tier3 := s.addLearnedClause(cnf.Clause{cnf.Literal(-5), cnf.Literal(6)}, p.Tier2LBD+1)
	if got := s.clauseUsed[tier2]; got != p.Tier2UsedReductions {
		t.Fatalf("tier-2 clause starts with used = %d, want %d", got, p.Tier2UsedReductions)
	}
	if got := s.clauseUsed[tier3]; got != p.Tier3UsedReductions {
		t.Fatalf("tier-3 clause starts with used = %d, want %d", got, p.Tier3UsedReductions)
	}
	_ = tier1
	has := func(c cnf.Clause) bool { return clauseSet(s)[fmt.Sprint(c)] }
	c1 := cnf.Clause{cnf.Literal(-1), cnf.Literal(3)}
	c2 := cnf.Clause{cnf.Literal(-2), cnf.Literal(4)}
	c3 := cnf.Clause{cnf.Literal(-5), cnf.Literal(6)}

	s.reduceClauseDatabase() // first: tier 3 had window 1, tier 2 window 2: nothing deleted
	if !has(c1) || !has(c2) || !has(c3) {
		t.Fatalf("after reduction 1 the clauses are %v, want all three", s.clauses)
	}
	s.reduceClauseDatabase() // second: tier 3 unprotected and deleted, tier 2 still has 1
	if !has(c1) || !has(c2) || has(c3) {
		t.Fatalf("after reduction 2 the clauses are %v, want tier 1 and 2 only", s.clauses)
	}
	s.reduceClauseDatabase() // third: tier 2 unprotected and deleted
	if !has(c1) || has(c2) {
		t.Fatalf("after reduction 3 the clauses are %v, want tier 1 only", s.clauses)
	}
	if len(s.clauseUsed) != len(s.clauses) {
		t.Fatalf("clauseUsed has %d entries for %d clauses", len(s.clauseUsed), len(s.clauses))
	}
}

// TestScheduledReductionTriggersOnConflictCount checks the schedule: the
// first reduction at ReduceFirstConflicts conflicts, then gaps growing by
// ReduceIncrement; ReduceFirstConflicts = 0 never reduces.
func TestScheduledReductionTriggersOnConflictCount(t *testing.T) {
	p := params.ScheduledDatabasePolicy(params.Default().CDCL)
	p.ReduceFirstConflicts = 10
	p.ReduceIncrement = 5
	s, ok, _ := newSolver(pigeonholeProblem(7, 6), nil, SelectVarVsids, RestartNone, p, PhaseSaving, nil, time.Time{})
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	if s.nextReduceAt != 10 {
		t.Fatalf("nextReduceAt = %d, want 10", s.nextReduceAt)
	}
	s.numConflicts = 9
	s.maybeScheduledReduce()
	if s.scheduledReductions != 0 {
		t.Fatalf("reduced at 9 conflicts, want the first at 10")
	}
	s.numConflicts = 10
	s.maybeScheduledReduce()
	if s.scheduledReductions != 1 || s.nextReduceAt != 10+10+5 {
		t.Fatalf("after 10 conflicts: reductions %d, next at %d, want 1 and 25", s.scheduledReductions, s.nextReduceAt)
	}
	s.numConflicts = 25
	s.maybeScheduledReduce()
	if s.scheduledReductions != 2 || s.nextReduceAt != 25+10+10 {
		t.Fatalf("after 25 conflicts: reductions %d, next at %d, want 2 and 45", s.scheduledReductions, s.nextReduceAt)
	}

	off := params.LegacyDatabasePolicy(p)
	s2, _, _ := newSolver(pigeonholeProblem(7, 6), nil, SelectVarVsids, RestartNone, off, PhaseSaving, nil, time.Time{})
	s2.numConflicts = 1000000
	s2.maybeScheduledReduce()
	if s2.scheduledReductions != 0 {
		t.Fatalf("legacy policy reduced on a schedule")
	}
}

// TestRunWithFrequentReductionAgreesWithBruteForce reduces after every
// conflict, with both policies, to stress the index remapping and the
// parallel clauseUsed slice.
func TestRunWithFrequentReductionAgreesWithBruteForce(t *testing.T) {
	rng := rand.New(rand.NewPCG(67, 67))
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
		for _, fraction := range []float64{0.5, 1} {
			p := params.ScheduledDatabasePolicy(params.Default().CDCL)
			p.ReduceFirstConflicts = 1
			p.ReduceIncrement = 0
			p.Tier2UsedReductions = 1
			p.Tier3UsedReductions = 0
			p.ReduceFraction = fraction
			result := Run(problem, nil, SelectVarVsids, RestartNone, nil, rand.New(rand.NewPCG(uint64(trial), 7)), 0, p, PhaseSaving)
			if result.TimedOut || result.Satisfiable != want {
				t.Fatalf("trial %d fraction %v: satisfiable=%v timedOut=%v, brute force says %v\n%v", trial, fraction, result.Satisfiable, result.TimedOut, want, clauses)
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
}

// TestRunWithFrequentReductionProvesUnsatisfiablePigeonhole needs many
// conflicts, so the database is reduced many times, in parallel too.
func TestRunWithFrequentReductionProvesUnsatisfiablePigeonhole(t *testing.T) {
	p := params.ScheduledDatabasePolicy(params.Default().CDCL)
	p.ReduceFirstConflicts = 5
	p.ReduceIncrement = 1
	p.ReduceFraction = 1
	for _, threads := range []int{1, 3} {
		result := RunParallel(pigeonholeProblem(7, 6), nil, SelectVarVsids, RestartPolynomial, nil, threads, rand.New(rand.NewPCG(5, 5)), 0, p, PhaseSaving)
		if result.Satisfiable || result.TimedOut {
			t.Fatalf("%d threads: satisfiable=%v timedOut=%v, want a proven UNSAT", threads, result.Satisfiable, result.TimedOut)
		}
	}
}

// TestToggleDatabasePolicy checks the presets and that toggling twice
// returns to the start.
func TestToggleDatabasePolicy(t *testing.T) {
	base := params.Default().CDCL
	scheduled := params.ScheduledDatabasePolicy(base)
	legacy := params.LegacyDatabasePolicy(base)
	if legacy.ReduceFirstConflicts != 0 || legacy.Tier2LBD != legacy.GlueClauseLBDThreshold || legacy.ReduceFraction != 0.5 {
		t.Fatalf("legacy preset = %+v", legacy)
	}
	if got := params.ToggleDatabasePolicy(scheduled); got != legacy {
		t.Errorf("toggle(scheduled) = %+v, want legacy %+v", got, legacy)
	}
	if got := params.ToggleDatabasePolicy(legacy); got != scheduled {
		t.Errorf("toggle(legacy) = %+v, want scheduled %+v", got, scheduled)
	}
}
