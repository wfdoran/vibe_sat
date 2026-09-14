package dfs

import (
	"math/rand/v2"
	"testing"
	"time"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/occurrence"
)

// TestRunParallelWithOneThreadMatchesRun verifies STAGE18.md's core
// compatibility guarantee (the same one Stage 17 established for the
// parallel hillclimb/walksat entry points): RunParallel with
// numThreads <= 1 must behave identically to calling Run directly,
// down to every random choice made, since it delegates straight to
// Run rather than going through any of the seeding/worker/stealing
// machinery at all.
func TestRunParallelWithOneThreadMatchesRun(t *testing.T) {
	problem := pigeonholeProblem(t, 4, 3)
	lists := occurrence.Build(problem)

	want := Run(problem, lists, nil, SelectVarWeighted, rand.New(rand.NewPCG(31, 31)), 0)
	got := RunParallel(problem, lists, nil, SelectVarWeighted, 1, rand.New(rand.NewPCG(31, 31)), 0)

	if got.Satisfiable != want.Satisfiable || got.NumNodes != want.NumNodes || got.TimedOut != want.TimedOut {
		t.Fatalf("RunParallel(numThreads=1) = %+v, want identical to Run() = %+v", got, want)
	}
}

// TestRunParallelFindsSatisfiableFormula verifies that splitting a
// search across several concurrent workers still finds a genuine
// satisfying assignment and reports it correctly, across a range of
// thread counts (including more threads than the problem has
// variables, to exercise the "fewer seeds than threads" path
// STAGE18.md describes).
func TestRunParallelFindsSatisfiableFormula(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 3,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(2)},
			{cnf.Literal(-1), cnf.Literal(3)},
			{cnf.Literal(-2), cnf.Literal(-3)},
		},
	}
	lists := occurrence.Build(problem)

	for _, numThreads := range []int{2, 4, 8, 32} {
		rng := rand.New(rand.NewPCG(uint64(numThreads), uint64(numThreads)))
		result := RunParallel(problem, lists, nil, SelectVarWeighted, numThreads, rng, 0)

		if !result.Satisfiable {
			t.Fatalf("numThreads=%d: expected Satisfiable = true", numThreads)
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

// TestRunParallelProvesUnsatisfiablePigeonhole verifies that the
// parallel search still proves UNSAT correctly (not just "gives up"),
// across a range of thread counts, on the same pigeonhole instance
// Run's own single-threaded test uses.
func TestRunParallelProvesUnsatisfiablePigeonhole(t *testing.T) {
	problem := pigeonholeProblem(t, 4, 3)
	lists := occurrence.Build(problem)

	for _, numThreads := range []int{2, 4, 8, 16} {
		rng := rand.New(rand.NewPCG(uint64(numThreads)+100, uint64(numThreads)+100))
		result := RunParallel(problem, lists, nil, SelectVarWeighted, numThreads, rng, 0)

		if result.Satisfiable {
			t.Fatalf("numThreads=%d: expected Satisfiable = false for an unsatisfiable pigeonhole instance", numThreads)
		}
		if result.TimedOut {
			t.Errorf("numThreads=%d: expected TimedOut = false (a genuine UNSAT proof, not a timeout)", numThreads)
		}
	}
}

// TestRunParallelProvesUnsatisfiableLargerPigeonhole is a heavier
// version of the above, specifically to stress work redistribution:
// large enough that no single worker's initial seed alone would
// exhaust the tree quickly, so correctness here also exercises
// genuine stealing (not just BFS resolving everything up front) and
// termination detection under real search-tree load, at a thread
// count (64) well past what REPORT16.md speculatively cited as a
// scaling ceiling ("no more than 16 workers") but consistent with
// STAGE18.md's actual requirement (128 or more).
func TestRunParallelProvesUnsatisfiableLargerPigeonhole(t *testing.T) {
	problem := pigeonholeProblem(t, 6, 5)
	lists := occurrence.Build(problem)
	rng := rand.New(rand.NewPCG(77, 77))

	result := RunParallel(problem, lists, nil, SelectVarWeighted, 64, rng, 0)

	if result.Satisfiable {
		t.Fatal("expected Satisfiable = false for an unsatisfiable pigeonhole instance")
	}
	if result.TimedOut {
		t.Error("expected TimedOut = false (a genuine UNSAT proof, not a timeout)")
	}
	if result.NumNodes == 0 {
		t.Error("expected NumNodes > 0")
	}
}

// TestBFSSeedReturnsSATDirectlyWithoutSpawningWorkers verifies
// STAGE18.md's "BFS solves the problem" SAT case: a formula trivial
// enough that the very first branch bfsSeed tries already completes
// a satisfying assignment, before numThreads seeds are ever
// collected. This must be detected and returned directly by
// RunParallel, not passed on to a (nonexistent, single-node) worker
// pool.
func TestBFSSeedReturnsSATDirectlyWithoutSpawningWorkers(t *testing.T) {
	// Every clause is a single positive literal: BCP alone (triggered
	// by the very first decision) completes the assignment.
	problem := &cnf.Problem{
		NumVars: 2,
		Clauses: []cnf.Clause{{cnf.Literal(1)}, {cnf.Literal(2)}},
	}
	lists := occurrence.Build(problem)
	clauses, root, ok := bootstrap(problem)
	if !ok {
		t.Fatal("bootstrap reported UNSAT unexpectedly")
	}
	workingProblem := &cnf.Problem{NumVars: problem.NumVars, Clauses: clauses}

	result := bfsSeed(clauses, lists, workingProblem, root, 8, SelectVarWeighted, rand.New(rand.NewPCG(1, 1)), nil, time.Now())

	if result.verdict != bfsSAT {
		t.Fatalf("verdict = %d, want bfsSAT", result.verdict)
	}
	for ci, clause := range problem.Clauses {
		satisfied := false
		for _, lit := range clause {
			if result.assignment.LiteralIsTrue(lit) {
				satisfied = true
				break
			}
		}
		if !satisfied {
			t.Errorf("clause %d (%v) not satisfied by %v", ci, clause, result.assignment)
		}
	}
}

// TestBFSSeedReturnsUNSATDirectlyWithoutSpawningWorkers verifies
// STAGE18.md's "BFS solves the problem" UNSAT case: a formula small
// enough that BFS's own expansion exhausts the entire tree (every
// branch pruned) before ever collecting numThreads seeds.
func TestBFSSeedReturnsUNSATDirectlyWithoutSpawningWorkers(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 1,
		Clauses: []cnf.Clause{{cnf.Literal(1)}, {cnf.Literal(-1)}},
	}
	lists := occurrence.Build(problem)
	clauses, root, ok := bootstrap(problem)
	if !ok {
		// The bootstrap's own unit propagation may already catch this
		// particular contradiction; either way is a correct UNSAT.
		return
	}
	workingProblem := &cnf.Problem{NumVars: problem.NumVars, Clauses: clauses}

	result := bfsSeed(clauses, lists, workingProblem, root, 8, SelectVarWeighted, rand.New(rand.NewPCG(2, 2)), nil, time.Now())

	if result.verdict != bfsUNSAT {
		t.Fatalf("verdict = %d, want bfsUNSAT", result.verdict)
	}
}

// TestBFSSeedProducesFewerSeedsThanThreadsForASmallTree verifies
// STAGE18.md's explicit "fewer seeds than threads" allowance: a
// formula whose full search tree has fewer leaves than the requested
// thread count must still resolve correctly (via RunParallel, which
// must fall back to spawning only as many workers as there are
// seeds), without hanging or erroring.
func TestBFSSeedProducesFewerSeedsThanThreadsForASmallTree(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 2,
		Clauses: []cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}, {cnf.Literal(-1), cnf.Literal(-2)}},
	}
	lists := occurrence.Build(problem)
	rng := rand.New(rand.NewPCG(3, 3))

	// 2 variables can produce at most a handful of live branches, far
	// fewer than 50 requested threads.
	result := RunParallel(problem, lists, nil, SelectVarWeighted, 50, rng, 0)

	if !result.Satisfiable {
		t.Fatalf("expected Satisfiable = true (score irrelevant here, just must not hang/error)")
	}
}

// TestDequePushPopStealAreConsistent exercises deque directly: a
// pushed node must be poppable from the bottom, and stealTop must
// take from the opposite end (the earliest-pushed item still
// present), never the same end popBottom would.
func TestDequePushPopStealAreConsistent(t *testing.T) {
	d := &deque{}
	nodes := make([]searchNode, 3)
	for i := range nodes {
		nodes[i] = searchNode{assignment: assign.New(i + 1)}
		d.pushBottom(nodes[i])
	}

	stolen, ok := d.stealTop()
	if !ok || len(stolen.assignment) != len(nodes[0].assignment) {
		t.Fatalf("stealTop() did not return the oldest-pushed node")
	}

	popped, ok := d.popBottom()
	if !ok || len(popped.assignment) != len(nodes[2].assignment) {
		t.Fatalf("popBottom() did not return the most-recently-pushed node")
	}

	if d.isEmpty() {
		t.Fatal("deque should still have one node left")
	}
	if _, ok := d.popBottom(); !ok {
		t.Fatal("expected one more poppable node")
	}
	if !d.isEmpty() {
		t.Error("deque should now be empty")
	}
}

// TestTerminatorDecrementConfirmsTerminationOnlyWhenTrulyQuiescent
// exercises terminator directly (no deque/goroutines involved)
// against the exact race the package doc comment describes: with two
// workers, the first decrement (bringing the count to 1) must never
// be reported as terminated; the second (bringing it to 0) must
// consult recheck, and only report termination if recheck says so,
// correctly undoing the count if not.
func TestTerminatorDecrementConfirmsTerminationOnlyWhenTrulyQuiescent(t *testing.T) {
	term := newTerminator(2)

	terminated, stillIdle := term.decrement(func() bool {
		t.Fatal("recheck must not be called before the count reaches zero")
		return false
	})
	if terminated || !stillIdle {
		t.Fatalf("first decrement: got (%v, %v), want (false, true)", terminated, stillIdle)
	}

	recheckCalled := false
	terminated, stillIdle = term.decrement(func() bool {
		recheckCalled = true
		return false
	})
	if !recheckCalled {
		t.Fatal("recheck was not called when the count reached zero")
	}
	if terminated || stillIdle {
		t.Fatalf("second decrement (recheck=false): got (%v, %v), want (false, false)", terminated, stillIdle)
	}
	if got := term.active.Load(); got != 1 {
		t.Errorf("active = %d after an undone decrement, want 1 (restored)", got)
	}

	terminated, stillIdle = term.decrement(func() bool { return true })
	if !terminated || !stillIdle {
		t.Fatalf("third decrement (recheck=true): got (%v, %v), want (true, true)", terminated, stillIdle)
	}
}

// TestWorkerStateMarkIdleIsIdempotent verifies that calling markIdle
// repeatedly without an intervening markActive only touches the
// shared terminator once.
func TestWorkerStateMarkIdleIsIdempotent(t *testing.T) {
	term := newTerminator(3)
	w := newWorkerState(term)

	w.markIdle(func() bool { return false })
	if got := term.active.Load(); got != 2 {
		t.Fatalf("active after first markIdle = %d, want 2", got)
	}

	for i := 0; i < 3; i++ {
		if w.markIdle(func() bool {
			t.Fatal("recheck must not be called on a redundant markIdle")
			return false
		}) {
			t.Fatal("redundant markIdle must never report termination")
		}
	}
	if got := term.active.Load(); got != 2 {
		t.Fatalf("active after redundant markIdle calls = %d, want unchanged at 2", got)
	}

	w.markActive()
	if got := term.active.Load(); got != 3 {
		t.Fatalf("active after markActive = %d, want 3 (restored)", got)
	}
}

// TestRunParallelRespectsTimeLimit mirrors Run's own equivalent test:
// a vanishingly small time limit against a problem hard enough to
// not finish instantly, split across several threads.
func TestRunParallelRespectsTimeLimit(t *testing.T) {
	problem := pigeonholeProblem(t, 9, 8)
	lists := occurrence.Build(problem)
	rng := rand.New(rand.NewPCG(9, 9))
	tiny := time.Duration(1)

	result := RunParallel(problem, lists, &tiny, SelectVarWeighted, 4, rng, 0)

	if !result.TimedOut {
		t.Error("expected TimedOut = true with a 1ns time limit")
	}
	if result.Satisfiable {
		t.Error("expected Satisfiable = false when timed out")
	}
}

// TestRunParallelReportsExactlyOneWinner runs several times on a
// formula with many satisfying assignments and many threads, to
// exercise (probabilistically) the case where more than one worker
// might complete an assignment at nearly the same time -- the
// CAS-based single-winner protocol (mirroring Stage 17's
// hillclimb/walksat) must still report exactly one coherent
// satisfying assignment every time, never a mix or a panic.
func TestRunParallelReportsExactlyOneWinner(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 6,
		Clauses: []cnf.Clause{
			{cnf.Literal(1)}, {cnf.Literal(2)}, {cnf.Literal(3)},
			{cnf.Literal(4)}, {cnf.Literal(5)}, {cnf.Literal(6)},
		},
	}
	lists := occurrence.Build(problem)

	for trial := 0; trial < 20; trial++ {
		rng := rand.New(rand.NewPCG(uint64(trial), uint64(trial)+1))
		result := RunParallel(problem, lists, nil, SelectVarWeighted, 16, rng, 0)
		if !result.Satisfiable {
			t.Fatalf("trial %d: expected Satisfiable = true", trial)
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
