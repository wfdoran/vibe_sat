package preprocess

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/params"
)

// pastDeadline is a deadline that has certainly already expired.
func pastDeadline() time.Time { return time.Now().Add(-time.Hour) }

// futureDeadline is a deadline that will not expire during a test.
func futureDeadline() time.Time { return time.Now().Add(time.Hour) }

func TestDeadlineNilLimitMeansNoDeadline(t *testing.T) {
	if got := Deadline(nil, time.Now()); !got.IsZero() {
		t.Errorf("Deadline(nil) = %v, want the zero time", got)
	}
}

func TestDeadlineIsOneTenthOfLimit(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		limit time.Duration
		want  time.Duration
	}{
		{10 * time.Second, time.Second},
		{100 * time.Second, 10 * time.Second},
		{time.Second, 100 * time.Millisecond},
		{0, 0},
	} {
		limit := tc.limit
		got := Deadline(&limit, now)
		if want := now.Add(tc.want); !got.Equal(want) {
			t.Errorf("Deadline(%v) = %v, want %v", tc.limit, got, want)
		}
	}
}

func TestExpired(t *testing.T) {
	if expired(time.Time{}) {
		t.Errorf("the zero time must never expire")
	}
	if !expired(pastDeadline()) {
		t.Errorf("a past deadline must be expired")
	}
	if expired(futureDeadline()) {
		t.Errorf("a future deadline must not be expired")
	}
}

// chainClauses returns (x1), (-x1 v x2), ..., (-x_{n-1} v x_n).
func chainClauses(n int) []cnf.Clause {
	clauses := []cnf.Clause{{cnf.Literal(1)}}
	for i := 1; i < n; i++ {
		clauses = append(clauses, cnf.Clause{cnf.Literal(-i), cnf.Literal(i + 1)})
	}
	return clauses
}

func TestUnitPropagateByDeadlineMatchesUnitPropagateWithoutDeadline(t *testing.T) {
	rng := rand.New(rand.NewPCG(57, 1))
	for trial := 0; trial < 200; trial++ {
		numVars := 3 + rng.IntN(8)
		base := randomClauseSet(rng, numVars, 2+rng.IntN(14))
		// Add some units so there is something to propagate.
		for u := 0; u < 1+rng.IntN(3); u++ {
			lit := cnf.Literal(1 + rng.IntN(numVars))
			if rng.IntN(2) == 0 {
				lit = -lit
			}
			base = append(base, cnf.Clause{lit})
		}

		want := cloneClauses(base)
		wantAssign := assign.New(numVars)
		wantUnsat, wantFixed, _ := UnitPropagate(&want, wantAssign, nil, time.Time{})

		got := cloneClauses(base)
		gotAssign := assign.New(numVars)
		gotUnsat, gotFixed, gotTimedOut := unitPropagateByDeadline(&got, gotAssign, time.Time{})

		if gotTimedOut {
			t.Fatalf("trial %d: timed out with no deadline", trial)
		}
		if gotUnsat != wantUnsat {
			t.Fatalf("trial %d: unsat = %v, want %v", trial, gotUnsat, wantUnsat)
		}
		if gotUnsat {
			continue
		}
		if gotFixed != wantFixed {
			t.Errorf("trial %d: numFixed = %d, want %d", trial, gotFixed, wantFixed)
		}
		if !slices.Equal(gotAssign, wantAssign) {
			t.Errorf("trial %d: assignment = %v, want %v", trial, gotAssign, wantAssign)
		}
		if len(got) != len(want) {
			t.Errorf("trial %d: %d clauses left, want %d", trial, len(got), len(want))
		}
	}
}

func TestUnitPropagateByDeadlineStopsConsistentlyWhenExpired(t *testing.T) {
	clauses := chainClauses(5)
	original := cloneClauses(clauses)
	assignment := assign.New(5)

	unsat, numFixed, timedOut := unitPropagateByDeadline(&clauses, assignment, pastDeadline())
	if unsat {
		t.Fatalf("unexpected contradiction")
	}
	if !timedOut {
		t.Errorf("timedOut = false, want true")
	}
	if numFixed != 0 {
		t.Errorf("numFixed = %d, want 0 (stop before the first unit scan)", numFixed)
	}
	if len(clauses) != len(original) {
		t.Errorf("clauses changed: %v, want %v", clauses, original)
	}
}

func TestUnitPropagateByDeadlineReachesFixpointWithFarDeadline(t *testing.T) {
	clauses := chainClauses(6)
	assignment := assign.New(6)
	_, _, timedOut := unitPropagateByDeadline(&clauses, assignment, futureDeadline())
	if timedOut {
		t.Fatalf("timedOut with a far-future deadline")
	}
	for v := 1; v <= 6; v++ {
		if assignment[v] != assign.True {
			t.Errorf("variable %d = %v, want True", v, assignment[v])
		}
	}
	if len(clauses) != 0 {
		t.Errorf("clauses = %v, want all consumed", clauses)
	}
}

func TestEliminatePureLiteralsStopsAfterOnePassWhenExpired(t *testing.T) {
	// (x1 v -x2), (x2 v -x3), (x3 v -x4): x1 is pure, then x2 becomes
	// pure, and so on; one pass per link.
	clauses := []cnf.Clause{
		{cnf.Literal(1), cnf.Literal(-2)},
		{cnf.Literal(2), cnf.Literal(-3)},
		{cnf.Literal(3), cnf.Literal(-4)},
	}
	assignment := assign.New(4)

	numFixed, timedOut := eliminatePureLiterals(&clauses, assignment, pastDeadline())
	if !timedOut {
		t.Errorf("timedOut = false, want true")
	}
	if numFixed < 1 {
		t.Fatalf("numFixed = %d, want at least one full pass", numFixed)
	}
	// Consistency: no remaining clause may be satisfied or contain a
	// false literal under the assignment.
	for _, clause := range clauses {
		for _, lit := range clause {
			if assignment[lit.Var()] != assign.Unassigned {
				t.Errorf("clause %v still mentions assigned variable %d", clause, lit.Var())
			}
		}
	}

	// Without a deadline the whole chain resolves.
	clauses = []cnf.Clause{
		{cnf.Literal(1), cnf.Literal(-2)},
		{cnf.Literal(2), cnf.Literal(-3)},
		{cnf.Literal(3), cnf.Literal(-4)},
	}
	assignment = assign.New(4)
	numFixed, timedOut = eliminatePureLiterals(&clauses, assignment, time.Time{})
	if timedOut {
		t.Errorf("timedOut with no deadline")
	}
	if len(clauses) != 0 {
		t.Errorf("clauses = %v, want none left", clauses)
	}
	if numFixed < 3 {
		t.Errorf("numFixed = %d, want at least 3", numFixed)
	}
}

func TestEliminateSubsumedClausesRemovesNothingWhenExpired(t *testing.T) {
	clauses := []cnf.Clause{
		{cnf.Literal(1), cnf.Literal(2)},
		{cnf.Literal(1), cnf.Literal(2), cnf.Literal(3)},
	}
	numRemoved, timedOut := eliminateSubsumedClauses(&clauses, 3, 64, pastDeadline())
	if !timedOut {
		t.Errorf("timedOut = false, want true")
	}
	if numRemoved != 0 || len(clauses) != 2 {
		t.Errorf("numRemoved = %d, %d clauses left; want 0 removed and both clauses kept", numRemoved, len(clauses))
	}

	numRemoved, timedOut = eliminateSubsumedClauses(&clauses, 3, 64, time.Time{})
	if timedOut || numRemoved != 1 || len(clauses) != 1 {
		t.Errorf("no deadline: numRemoved = %d, timedOut = %v, %d clauses left; want 1, false, 1", numRemoved, timedOut, len(clauses))
	}
}

func TestEliminateSubsumedClausesParallelReportsTimedOut(t *testing.T) {
	clauses := []cnf.Clause{
		{cnf.Literal(1), cnf.Literal(2)},
		{cnf.Literal(1), cnf.Literal(2), cnf.Literal(3)},
	}
	numRemoved, timedOut := eliminateSubsumedClausesParallel(&clauses, 3, 4, 64, pastDeadline())
	if !timedOut {
		t.Errorf("timedOut = false, want true")
	}
	if numRemoved != 0 || len(clauses) != 2 {
		t.Errorf("numRemoved = %d, %d clauses left; want 0 removed and both clauses kept", numRemoved, len(clauses))
	}
}

func TestEliminateVariablesDoesNothingWhenExpired(t *testing.T) {
	clauses := []cnf.Clause{
		{cnf.Literal(1), cnf.Literal(2)},
		{cnf.Literal(-1), cnf.Literal(3)},
		{cnf.Literal(-2), cnf.Literal(-3)},
	}
	assignment := assign.New(3)
	steps, timedOut := eliminateVariables(&clauses, assignment, 3, hugeBudget(), pastDeadline())
	if !timedOut {
		t.Errorf("timedOut = false, want true")
	}
	if len(steps) != 0 || len(clauses) != 3 {
		t.Errorf("%d steps, %d clauses; want no steps and the 3 original clauses", len(steps), len(clauses))
	}

	steps, timedOut = eliminateVariables(&clauses, assignment, 3, hugeBudget(), time.Time{})
	if timedOut || len(steps) == 0 {
		t.Errorf("no deadline: %d steps, timedOut = %v; want at least one step and false", len(steps), timedOut)
	}
}

// bruteForceSatisfiable reports whether clauses over numVars variables
// has a model.
func bruteForceSatisfiable(clauses []cnf.Clause, numVars int) bool {
	for mask := 0; mask < 1<<numVars; mask++ {
		a := assign.New(numVars)
		for v := 1; v <= numVars; v++ {
			if mask&(1<<(v-1)) != 0 {
				a[v] = assign.True
			} else {
				a[v] = assign.False
			}
		}
		if allSatisfied(clauses, a) {
			return true
		}
	}
	return false
}

// TestRunWithExpiredDeadlineStaysEquivalent is the safety property: an
// early stop at any point leaves a problem that is satisfiable exactly
// when the original is, and every model of it reconstructs to a model of
// the original.
func TestRunWithExpiredDeadlineStaysEquivalent(t *testing.T) {
	rng := rand.New(rand.NewPCG(57, 2))
	pp := params.Default().Preprocess
	for trial := 0; trial < 150; trial++ {
		numVars := 3 + rng.IntN(7)
		problem := &cnf.Problem{NumVars: numVars, Clauses: randomClauseSet(rng, numVars, 3+rng.IntN(20))}
		wantSat := bruteForceSatisfiable(problem.Clauses, numVars)

		result := Run(problem, 0, 1, pp, pastDeadline())
		if !result.Stats.TimedOut && !result.Unsat {
			t.Fatalf("trial %d: an expired deadline must set Stats.TimedOut", trial)
		}
		if result.Unsat {
			if wantSat {
				t.Fatalf("trial %d: Run claimed UNSAT on a satisfiable formula", trial)
			}
			continue
		}
		n := result.Problem.NumVars
		gotSat := bruteForceSatisfiable(result.Problem.Clauses, n)
		if gotSat != wantSat {
			t.Fatalf("trial %d: reduced satisfiable = %v, original = %v", trial, gotSat, wantSat)
		}
		for mask := 0; mask < 1<<n; mask++ {
			solution := assign.New(n)
			for v := 1; v <= n; v++ {
				if mask&(1<<(v-1)) != 0 {
					solution[v] = assign.True
				} else {
					solution[v] = assign.False
				}
			}
			if allSatisfied(result.Problem.Clauses, solution) {
				allClausesSatisfied(t, problem, result.Reconstruct(solution))
			}
		}
	}
}

func TestRunWithFarDeadlineMatchesRunWithoutDeadline(t *testing.T) {
	rng := rand.New(rand.NewPCG(57, 3))
	pp := params.Default().Preprocess
	for trial := 0; trial < 50; trial++ {
		numVars := 4 + rng.IntN(10)
		problem := &cnf.Problem{NumVars: numVars, Clauses: randomClauseSet(rng, numVars, 4+rng.IntN(30))}

		none := Run(problem, 0, 1, pp, time.Time{})
		far := Run(problem, 0, 1, pp, futureDeadline())
		if none.Stats.TimedOut || far.Stats.TimedOut {
			t.Fatalf("trial %d: TimedOut set without an expired deadline", trial)
		}
		if none.Unsat != far.Unsat {
			t.Fatalf("trial %d: Unsat differs", trial)
		}
		if none.Unsat {
			continue
		}
		if none.Problem.NumVars != far.Problem.NumVars || len(none.Problem.Clauses) != len(far.Problem.Clauses) {
			t.Errorf("trial %d: reduced problem differs (%d vars/%d clauses vs %d/%d)", trial,
				none.Problem.NumVars, len(none.Problem.Clauses), far.Problem.NumVars, len(far.Problem.Clauses))
		}
	}
}

func TestRunStopsPromptlyOnLongUnitChain(t *testing.T) {
	// The chain needs one linear pass per link (quadratic overall); a
	// short deadline must cut it off long before it finishes.
	const n = 60000
	problem := &cnf.Problem{NumVars: n, Clauses: chainClauses(n)}
	limit := 200 * time.Millisecond
	start := time.Now()
	result := Run(problem, 0, 1, params.Default().Preprocess, Deadline(&limit, start))
	elapsed := time.Since(start)
	// The deadline here is limit/10 = 20 ms; allow generous slack for a
	// loaded machine, but far below the many seconds a full run takes.
	if elapsed > 3*time.Second {
		t.Errorf("Run took %v with a 20 ms deadline", elapsed)
	}
	if !result.Stats.TimedOut {
		t.Errorf("expected Stats.TimedOut on a 60000-link chain with a 20 ms deadline")
	}
}
