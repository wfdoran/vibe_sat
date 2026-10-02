package cdcl

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/params"
)

// TestRunParallelWithOneThreadMatchesRun verifies STAGE20.md/
// STAGE21.md's compatibility guarantee: RunParallel(..., 1, ...) is
// bit-for-bit identical to calling Run directly, matching Stage
// 17/18's identical guarantee for hillclimb/walksat/dfs.
func TestRunParallelWithOneThreadMatchesRun(t *testing.T) {
	problem := pigeonholeProblem(4, 3)

	rng1 := rand.New(rand.NewPCG(7, 7))
	want := Run(problem, nil, SelectVarVsids, RestartPolynomial, nil, rng1, 0, params.Default().CDCL, PhaseSaving)

	rng2 := rand.New(rand.NewPCG(7, 7))
	got := RunParallel(problem, nil, SelectVarVsids, RestartPolynomial, nil, 1, rng2, 0, params.Default().CDCL, PhaseSaving)

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
		result := RunParallel(problem, nil, SelectVarVsids, RestartRoundRobin, nil, numThreads, rng, 0, params.Default().CDCL, PhaseSaving)
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
		result := RunParallel(problem, nil, SelectVarVsids, RestartRoundRobin, nil, numThreads, rng, 0, params.Default().CDCL, PhaseSaving)
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

	result := RunParallel(problem, nil, SelectVarVsids, RestartRoundRobin, nil, 64, rng, 0, params.Default().CDCL, PhaseSaving)

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

	result := RunParallel(problem, &limit, SelectVarVsids, RestartRoundRobin, nil, 8, rng, 0, params.Default().CDCL, PhaseSaving)

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
		result := RunParallel(problem, nil, SelectVarVsids, RestartRoundRobin, nil, 16, rng, 0, params.Default().CDCL, PhaseSaving)
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
// resolution order STAGE21.md specifies (STAGE44.md folded Glucose
// into the rotation): thread 0 quadratic, thread 1 geometric, thread 2
// Luby, thread 3 Glucose, then repeating.
func TestResolveRestartStrategyRoundRobin(t *testing.T) {
	want := []RestartStrategy{
		RestartPolynomial, RestartGeometric, RestartLuby, RestartGlucose,
		RestartPolynomial, RestartGeometric, RestartLuby,
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
	for _, strategy := range []RestartStrategy{RestartNone, RestartLuby, RestartPolynomial, RestartGeometric, RestartGlucose} {
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
	s, ok, _ := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving, nil, time.Time{})
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
	s, ok, _ := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving, nil, time.Time{})
	if !ok {
		t.Fatal("newSolver() reported ok = false unexpectedly")
	}

	before := len(s.clauses)
	s.importClause(cnf.Clause{cnf.Literal(2), cnf.Literal(3)})
	if len(s.clauses) != before+1 {
		t.Errorf("importClause() did not add a live clause; len(s.clauses) = %d, want %d", len(s.clauses), before+1)
	}
}

// TestImportClauseRegistersWatcherLists verifies STAGE45.md's watcher
// lists (watchersPositive/watchersNegative), not just watch itself,
// are updated for an imported clause -- otherwise propagate (which
// reads only the watcher lists now, not the old occurrence index)
// would never find this clause as a candidate at all, silently making
// clause sharing inert until the next reduceClauseDatabase rebuild
// (which, with no memory limit set, may never happen).
func TestImportClauseRegistersWatcherLists(t *testing.T) {
	problem := &cnf.Problem{NumVars: 3, Clauses: []cnf.Clause{{cnf.Literal(1)}}}
	s, ok, _ := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving, nil, time.Time{})
	if !ok {
		t.Fatal("newSolver() reported ok = false unexpectedly")
	}

	s.importClause(cnf.Clause{cnf.Literal(2), cnf.Literal(3)})
	idx := len(s.clauses) - 1
	if idx < 0 {
		t.Fatal("importClause() did not add the clause")
	}
	for _, lit := range s.watch[idx] {
		if !slices.Contains(*s.watchersFor(lit), idx) {
			t.Errorf("imported clause %d watches %v but does not appear in watchersFor(%v) = %v", idx, lit, lit, *s.watchersFor(lit))
		}
	}
}

// TestExportBufferPublished verifies published() counts every publish,
// including ones that have wrapped the ring, since maybeImport relies on
// it as the peer's write index.
func TestExportBufferPublished(t *testing.T) {
	buf := newExportBuffer(4)
	if got := buf.published(); got != 0 {
		t.Errorf("published() = %d on an empty buffer, want 0", got)
	}
	for i := 0; i < 10; i++ {
		buf.publish(cnf.Clause{1, 2})
	}
	if got := buf.published(); got != 10 {
		t.Errorf("published() = %d after 10 publishes into a 4-slot ring, want 10", got)
	}
}

// TestImportWindow checks every way importWindow can move the start of
// the range: untouched (small backlog), raised past overwritten slots,
// raised to honor maxPerPeer, and clamped when the cursor is ahead of
// the peer. end must always equal written.
func TestImportWindow(t *testing.T) {
	tests := []struct {
		name                                  string
		cursor, written, capacity, maxPerPeer uint64
		wantStart, wantEnd                    uint64
	}{
		{"nothing published", 0, 0, 8, 100, 0, 0},
		{"caught up", 5, 5, 8, 100, 5, 5},
		{"small backlog is read whole", 2, 5, 8, 100, 2, 5},
		{"ring has not wrapped, cursor at start", 0, 3, 8, 100, 0, 3},
		{"slots before written-capacity were overwritten", 0, 20, 8, 100, 12, 20},
		{"cursor inside the surviving range", 15, 20, 8, 100, 15, 20},
		{"maxPerPeer keeps only the newest clauses", 0, 6, 8, 2, 4, 6},
		{"maxPerPeer larger than the backlog changes nothing", 4, 6, 8, 5, 4, 6},
		{"maxPerPeer of one", 0, 6, 8, 1, 5, 6},
		{"cursor ahead of written is clamped", 9, 5, 8, 100, 5, 5},
		{"cursor far ahead after a wrapped ring", 1000, 300, 256, 100, 300, 300},
	}
	for _, tc := range tests {
		start, end := importWindow(tc.cursor, tc.written, tc.capacity, tc.maxPerPeer)
		if start != tc.wantStart || end != tc.wantEnd {
			t.Errorf("%s: importWindow(%d, %d, %d, %d) = [%d, %d), want [%d, %d)",
				tc.name, tc.cursor, tc.written, tc.capacity, tc.maxPerPeer, start, end, tc.wantStart, tc.wantEnd)
		}
	}
}

// newImportTestSolver returns a solver for a 6-variable problem with
// one peer (a capacity-slot export buffer) wired in, with
// numConflicts = 0 so maybeImport's check gate is open.
func newImportTestSolver(t *testing.T, capacity int) (*solver, *exportBuffer) {
	t.Helper()
	problem := &cnf.Problem{NumVars: 6, Clauses: []cnf.Clause{{cnf.Literal(1)}}}
	s, ok, _ := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving, nil, time.Time{})
	if !ok {
		t.Fatal("newSolver() reported ok = false unexpectedly")
	}
	peer := newExportBuffer(capacity)
	s.peers = []*exportBuffer{peer}
	s.peerCursors = make([]uint64, 1)
	s.numConflicts = 0
	return s, peer
}

// TestMaybeImportDrainsBacklogAndResynchronizesCursor publishes several
// clauses between two checks and verifies the first check imports the
// newest min(backlog, importMaxPerPeer) of them (not just one per
// check, the old behavior) and leaves the cursor at the peer's write
// index, so the second check imports nothing.
func TestMaybeImportDrainsBacklogAndResynchronizesCursor(t *testing.T) {
	s, peer := newImportTestSolver(t, 8)
	for i := 0; i < 5; i++ {
		peer.publish(cnf.Clause{cnf.Literal(2), cnf.Literal(3)})
	}
	before := len(s.clauses)
	s.maybeImport()
	want := min(5, importMaxPerPeer)
	if got := len(s.clauses) - before; got != want {
		t.Errorf("first maybeImport imported %d clauses, want %d", got, want)
	}
	if s.peerCursors[0] != 5 {
		t.Errorf("cursor = %d after the check, want 5 (the peer's write index)", s.peerCursors[0])
	}
	before = len(s.clauses)
	s.maybeImport()
	if got := len(s.clauses) - before; got != 0 {
		t.Errorf("second maybeImport with nothing new imported %d clauses, want 0", got)
	}
}

// TestMaybeImportNeverReadsPastOverwrittenSlots lets the peer lap a
// small ring several times before the first check; the check must read
// at most one ring's worth (capacity) of clauses and end at the write
// index.
func TestMaybeImportNeverReadsPastOverwrittenSlots(t *testing.T) {
	s, peer := newImportTestSolver(t, 4)
	for i := 0; i < 23; i++ {
		peer.publish(cnf.Clause{cnf.Literal(2), cnf.Literal(3)})
	}
	before := len(s.clauses)
	s.maybeImport()
	if got, limit := len(s.clauses)-before, min(4, importMaxPerPeer); got != limit {
		t.Errorf("imported %d clauses from a lapped 4-slot ring, want %d", got, limit)
	}
	if s.peerCursors[0] != 23 {
		t.Errorf("cursor = %d, want 23", s.peerCursors[0])
	}
}

// TestMaybeImportClampsCursorAheadOfPeer reproduces the old duplicate-
// import hazard: a cursor ahead of the peer's write index (the old
// code advanced it unconditionally) must import nothing and be pulled
// back to the write index, instead of re-reading already-imported slots.
func TestMaybeImportClampsCursorAheadOfPeer(t *testing.T) {
	s, peer := newImportTestSolver(t, 8)
	for i := 0; i < 3; i++ {
		peer.publish(cnf.Clause{cnf.Literal(2), cnf.Literal(3)})
	}
	s.peerCursors[0] = 50
	before := len(s.clauses)
	s.maybeImport()
	if got := len(s.clauses) - before; got != 0 {
		t.Errorf("imported %d clauses with the cursor ahead of the peer, want 0", got)
	}
	if s.peerCursors[0] != 3 {
		t.Errorf("cursor = %d, want it clamped to 3", s.peerCursors[0])
	}
}

// TestMaybeImportRespectsCheckGate verifies maybeImport does nothing
// (and leaves the cursor alone) unless numConflicts is a multiple of
// importCheckMask+1, and is a no-op without peers.
func TestMaybeImportRespectsCheckGate(t *testing.T) {
	s, peer := newImportTestSolver(t, 8)
	peer.publish(cnf.Clause{cnf.Literal(2), cnf.Literal(3)})
	s.numConflicts = 1
	before := len(s.clauses)
	s.maybeImport()
	if len(s.clauses) != before || s.peerCursors[0] != 0 {
		t.Errorf("gated-off check changed state: %d new clauses, cursor %d", len(s.clauses)-before, s.peerCursors[0])
	}

	s.peers, s.peerCursors, s.numConflicts = nil, nil, 0
	s.maybeImport() // must not panic
}
