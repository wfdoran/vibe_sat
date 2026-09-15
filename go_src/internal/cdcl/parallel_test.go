package cdcl

import (
	"math/rand/v2"
	"testing"
	"time"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
)

// TestRunParallelWithOneThreadMatchesRun verifies STAGE20.md/
// STAGE21.md's compatibility guarantee: RunParallel(..., 1, ...) is
// bit-for-bit identical to calling Run directly, matching Stage
// 17/18's identical guarantee for hillclimb/walksat/dfs.
func TestRunParallelWithOneThreadMatchesRun(t *testing.T) {
	problem := pigeonholeProblem(4, 3)

	rng1 := rand.New(rand.NewPCG(7, 7))
	want := Run(problem, nil, SelectVarVsids, RestartPolynomial, nil, rng1, 0)

	rng2 := rand.New(rand.NewPCG(7, 7))
	got := RunParallel(problem, nil, SelectVarVsids, RestartPolynomial, nil, 1, rng2, 0)

	if got.Satisfiable != want.Satisfiable || got.NumDecisions != want.NumDecisions || got.NumConflicts != want.NumConflicts {
		t.Errorf("RunParallel(..., 1, ...) = %+v, want %+v (from Run)", got, want)
	}
}

// TestRunParallelFindsSatisfiableFormula verifies a satisfiable
// formula is solved correctly across several thread counts, and that
// the returned assignment genuinely satisfies every clause.
func TestRunParallelFindsSatisfiableFormula(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 3,
		Clauses: []cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}, {cnf.Literal(-1), cnf.Literal(3)}, {cnf.Literal(-2), cnf.Literal(-3)}},
	}

	for _, numThreads := range []int{2, 4, 8, 32} {
		rng := rand.New(rand.NewPCG(1, uint64(numThreads)))
		result := RunParallel(problem, nil, SelectVarVsids, RestartRoundRobin, nil, numThreads, rng, 0)
		if !result.Satisfiable {
			t.Fatalf("numThreads=%d: RunParallel() reported unsatisfiable for a satisfiable formula", numThreads)
		}
		if result.TimedOut {
			t.Errorf("numThreads=%d: RunParallel() reported TimedOut with no time limit set", numThreads)
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
				t.Errorf("numThreads=%d: clause %d (%v) not satisfied by %v", numThreads, ci, clause, result.Assignment)
			}
		}
	}
}

// TestRunParallelProvesUnsatisfiablePigeonhole verifies an
// unsatisfiable formula is correctly proven UNSAT across several
// thread counts -- unlike dfs's parallel search, every worker here
// covers the *whole* problem, so any single worker reaching UNSAT is
// already authoritative; there is no termination-detection sweep to
// get right the way there was for Stage 18's divide-and-conquer
// design.
func TestRunParallelProvesUnsatisfiablePigeonhole(t *testing.T) {
	problem := pigeonholeProblem(4, 3)

	for _, numThreads := range []int{2, 4, 8, 16} {
		rng := rand.New(rand.NewPCG(9, uint64(numThreads)))
		result := RunParallel(problem, nil, SelectVarVsids, RestartRoundRobin, nil, numThreads, rng, 0)
		if result.Satisfiable {
			t.Errorf("numThreads=%d: RunParallel() reported satisfiable for an unsatisfiable formula", numThreads)
		}
		if result.TimedOut {
			t.Errorf("numThreads=%d: RunParallel() reported TimedOut with no time limit set", numThreads)
		}
	}
}

// TestRunParallelProvesUnsatisfiableLargerPigeonhole exercises a
// harder instance (6 pigeons, 5 holes) at a large thread count,
// matching Stage 18's equivalent "does this actually scale" check.
func TestRunParallelProvesUnsatisfiableLargerPigeonhole(t *testing.T) {
	problem := pigeonholeProblem(6, 5)
	rng := rand.New(rand.NewPCG(11, 13))

	result := RunParallel(problem, nil, SelectVarVsids, RestartRoundRobin, nil, 64, rng, 0)

	if result.Satisfiable {
		t.Error("RunParallel() reported satisfiable for an unsatisfiable formula")
	}
	if result.TimedOut {
		t.Error("RunParallel() reported TimedOut with no time limit set")
	}
}

// TestRunParallelRespectsTimeLimit verifies that a hard instance with
// a very short time limit reports TimedOut rather than hanging or
// silently reporting an incorrect verdict.
func TestRunParallelRespectsTimeLimit(t *testing.T) {
	problem := pigeonholeProblem(9, 8)
	rng := rand.New(rand.NewPCG(3, 5))
	limit := time.Nanosecond

	result := RunParallel(problem, &limit, SelectVarVsids, RestartRoundRobin, nil, 8, rng, 0)

	if !result.TimedOut {
		t.Error("RunParallel() did not report TimedOut with a near-zero time limit")
	}
	if result.Satisfiable {
		t.Error("RunParallel() reported Satisfiable alongside TimedOut")
	}
}

// TestRunParallelReportsExactlyOneWinner runs many trials of a
// satisfiable formula at a high thread count and checks every result
// is a genuine, independently-verified satisfying assignment -- i.e.
// the single-winner CAS protocol never lets a stale/aborted worker's
// result leak through as the final answer.
func TestRunParallelReportsExactlyOneWinner(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 5,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(2)},
			{cnf.Literal(-1), cnf.Literal(3)},
			{cnf.Literal(-2), cnf.Literal(4)},
			{cnf.Literal(-3), cnf.Literal(5)},
			{cnf.Literal(-4), cnf.Literal(-5)},
		},
	}

	for trial := 0; trial < 20; trial++ {
		rng := rand.New(rand.NewPCG(uint64(trial), 42))
		result := RunParallel(problem, nil, SelectVarVsids, RestartRoundRobin, nil, 16, rng, 0)
		if !result.Satisfiable {
			t.Fatalf("trial %d: RunParallel() reported unsatisfiable for a satisfiable formula", trial)
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
				t.Errorf("trial %d: clause %d (%v) not satisfied by %v", trial, ci, clause, result.Assignment)
			}
		}
	}
}

// TestResolveRestartStrategyRoundRobin verifies the round-robin
// resolution order STAGE21.md specifies: thread 0 quadratic, thread 1
// geometric, thread 2 Luby, then repeating.
func TestResolveRestartStrategyRoundRobin(t *testing.T) {
	want := []RestartStrategy{
		RestartPolynomial, RestartGeometric, RestartLuby,
		RestartPolynomial, RestartGeometric, RestartLuby,
		RestartPolynomial,
	}
	for i, w := range want {
		if got := resolveRestartStrategy(RestartRoundRobin, i); got != w {
			t.Errorf("resolveRestartStrategy(RestartRoundRobin, %d) = %v, want %v", i, got, w)
		}
	}
}

// TestResolveRestartStrategyPassesExplicitValueThrough verifies that
// any explicit (non-round-robin) restart strategy, including
// RestartNone, is returned unchanged regardless of thread index --
// STAGE21.md: "If the user explicitly sets a different restart
// strategy, even 0, all threads will use that."
func TestResolveRestartStrategyPassesExplicitValueThrough(t *testing.T) {
	for _, strategy := range []RestartStrategy{RestartNone, RestartLuby, RestartPolynomial, RestartGeometric} {
		for _, threadIndex := range []int{0, 1, 2, 5, 127} {
			if got := resolveRestartStrategy(strategy, threadIndex); got != strategy {
				t.Errorf("resolveRestartStrategy(%v, %d) = %v, want %v (unchanged)", strategy, threadIndex, got, strategy)
			}
		}
	}
}

// TestExportBufferPublishAndTryRead verifies the basic single-slot
// round-trip: a clause published at some index can be read back with
// its literals intact.
func TestExportBufferPublishAndTryRead(t *testing.T) {
	buf := newExportBuffer(4)
	buf.publish(cnf.Clause{1, -2, 3})

	got, ok := buf.tryRead(0)
	if !ok {
		t.Fatal("tryRead(0) = (_, false), want (_, true) immediately after publish")
	}
	want := cnf.Clause{1, -2, 3}
	if len(got) != len(want) {
		t.Fatalf("tryRead(0) = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tryRead(0)[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestExportBufferTryReadFailsBeforeAnyPublish verifies that reading
// a never-written slot reports ok = false rather than a zero-value
// clause.
func TestExportBufferTryReadFailsBeforeAnyPublish(t *testing.T) {
	buf := newExportBuffer(4)
	if _, ok := buf.tryRead(0); ok {
		t.Error("tryRead(0) = (_, true) before any publish, want false")
	}
}

// TestExportBufferPublishOwnsItsData verifies that publish copies the
// literal slice rather than aliasing the caller's, so a caller
// mutating its own slice afterward can't corrupt an already-published
// clause -- a real hazard here specifically, since importClause and
// addLearnedClause both hand publish a slice they go on to use again.
func TestExportBufferPublishOwnsItsData(t *testing.T) {
	buf := newExportBuffer(4)
	lits := cnf.Clause{1, 2, 3}
	buf.publish(lits)
	lits[0] = 99

	got, ok := buf.tryRead(0)
	if !ok {
		t.Fatal("tryRead(0) = (_, false), want (_, true)")
	}
	if got[0] != 1 {
		t.Errorf("tryRead(0)[0] = %v, want 1 (publish must not alias the caller's slice)", got[0])
	}
}

// TestImportClauseSkipsAlreadyFalsifiedClause verifies that
// importClause discards (rather than incorrectly adding) a clause
// that is already fully falsified under the importing solver's
// current assignment -- see importClause's doc comment for why
// dropping, not analyzing a "found" conflict, is the deliberate
// design here.
func TestImportClauseSkipsAlreadyFalsifiedClause(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2, Clauses: []cnf.Clause{{cnf.Literal(1)}}}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone)
	if !ok {
		t.Fatal("newSolver() reported ok = false unexpectedly")
	}
	// Variable 2 is still unassigned; falsify it directly to set up a
	// clause that's fully false under s.x without needing a real
	// search.
	s.x[2] = assign.False

	before := len(s.clauses)
	s.importClause(cnf.Clause{cnf.Literal(2)}) // {2} is false since x[2] == False
	if len(s.clauses) != before {
		t.Errorf("importClause() added a clause that was already fully falsified; len(s.clauses) = %d, want %d", len(s.clauses), before)
	}
}

// TestImportClauseAddsLiveClause verifies that importClause does add
// a clause when at least two of its literals are not currently false.
func TestImportClauseAddsLiveClause(t *testing.T) {
	problem := &cnf.Problem{NumVars: 3, Clauses: []cnf.Clause{{cnf.Literal(1)}}}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone)
	if !ok {
		t.Fatal("newSolver() reported ok = false unexpectedly")
	}

	before := len(s.clauses)
	s.importClause(cnf.Clause{cnf.Literal(2), cnf.Literal(3)})
	if len(s.clauses) != before+1 {
		t.Errorf("importClause() did not add a live clause; len(s.clauses) = %d, want %d", len(s.clauses), before+1)
	}
}
