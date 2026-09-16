package dfs

import (
	"math/rand/v2"
	"sync"
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
	d := newDeque()
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

// TestDequeGrowPreservesOrderAndContents pushes far more nodes than
// dequeInitialCapacity (forcing several growTo calls) purely
// sequentially, then confirms every one comes back out via popBottom
// in exact LIFO order with the right identity (encoded, as in
// TestDequePushPopStealAreConsistent, by each node's assignment
// length) -- the simplest possible check that growTo's copy is
// correct before any concurrency is layered on top. This and the
// three tests below mirror util/lockfreedeque/go/deque_test.go's own
// suite, which validated this design (generically) before this
// STAGE26.md port; see deque.go's package doc comment.
func TestDequeGrowPreservesOrderAndContents(t *testing.T) {
	const n = 500 // far more than dequeInitialCapacity (32); forces multiple doublings
	d := newDeque()
	for i := 0; i < n; i++ {
		d.pushBottom(searchNode{assignment: assign.New(i)})
	}
	for i := n - 1; i >= 0; i-- {
		got, ok := d.popBottom()
		if !ok {
			t.Fatalf("popBottom() reported empty with %d nodes still expected", i+1)
		}
		if len(got.assignment) != i+1 {
			t.Fatalf("popBottom() returned node %d, want %d", len(got.assignment)-1, i)
		}
	}
	if !d.isEmpty() {
		t.Error("deque should be empty after popping every pushed node")
	}
}

// TestDequeGrowPreservesContentsForStealing is
// TestDequeGrowPreservesOrderAndContents's counterpart for the other
// end: push far more than dequeInitialCapacity, then drain entirely
// via stealTop, confirming FIFO order (oldest first) and that growth
// didn't corrupt or lose anything reachable from the top.
func TestDequeGrowPreservesContentsForStealing(t *testing.T) {
	const n = 500
	d := newDeque()
	for i := 0; i < n; i++ {
		d.pushBottom(searchNode{assignment: assign.New(i)})
	}
	for i := 0; i < n; i++ {
		got, ok := d.stealTop()
		if !ok {
			t.Fatalf("stealTop() reported empty with %d nodes still expected", n-i)
		}
		if len(got.assignment) != i+1 {
			t.Fatalf("stealTop() returned node %d, want %d", len(got.assignment)-1, i)
		}
	}
	if !d.isEmpty() {
		t.Error("deque should be empty after stealing every pushed node")
	}
}

// dequeExactlyOnceCollector is a test-only (deliberately not lock-free
// itself -- it exists purely to observe the deque under test, not to
// be part of what's under test) recorder used by both concurrent
// stress tests below: every goroutine reports the identity (assignment
// length) of each node it successfully removed, and the final check
// confirms the whole expected set was seen with no duplicates and
// nothing missing.
type dequeExactlyOnceCollector struct {
	mu   sync.Mutex
	seen map[int]int // node identity -> how many times it was observed
}

func newDequeExactlyOnceCollector() *dequeExactlyOnceCollector {
	return &dequeExactlyOnceCollector{seen: make(map[int]int)}
}

func (c *dequeExactlyOnceCollector) record(node searchNode) {
	c.mu.Lock()
	c.seen[len(node.assignment)]++
	c.mu.Unlock()
}

// checkExactlyOnce fails t unless every identity in [1, n] was
// recorded exactly once.
func (c *dequeExactlyOnceCollector) checkExactlyOnce(t *testing.T, n int) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.seen) != n {
		t.Errorf("saw %d distinct nodes, want %d", len(c.seen), n)
	}
	for i := 1; i <= n; i++ {
		if got := c.seen[i]; got != 1 {
			t.Errorf("node %d observed %d times, want exactly 1", i, got)
		}
	}
}

// TestDequeConcurrentOwnerAndThievesExactlyOnce is this file's main
// deque stress test: one owner goroutine pushes n nodes (interleaved
// with occasionally popping its own, to exercise popBottom
// concurrently with stealing too, not just pushBottom), while several
// thief goroutines hammer stealTop concurrently until the deque
// drains. Every node removed by anyone (owner's pops, any thief's
// steals) is recorded; the test then confirms the full set was
// delivered exactly once each -- no node lost, none duplicated --
// which is the core correctness property a work-stealing deque must
// have regardless of how push/pop/steal happen to interleave.
func TestDequeConcurrentOwnerAndThievesExactlyOnce(t *testing.T) {
	const n = 20000
	const numThieves = 8

	d := newDeque()
	collector := newDequeExactlyOnceCollector()
	done := make(chan struct{})

	var thieves sync.WaitGroup
	for th := 0; th < numThieves; th++ {
		thieves.Add(1)
		go func(seed uint64) {
			defer thieves.Done()
			rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
			for {
				if node, ok := d.stealTop(); ok {
					collector.record(node)
					continue
				}
				select {
				case <-done:
					if node, ok := d.stealTop(); ok {
						collector.record(node)
						continue
					}
					return
				default:
					if rng.IntN(4) == 0 {
						time.Sleep(time.Microsecond)
					}
				}
			}
		}(uint64(th) + 1)
	}

	rng := rand.New(rand.NewPCG(99, 99))
	for i := 0; i < n; i++ {
		d.pushBottom(searchNode{assignment: assign.New(i)})
		if rng.IntN(3) == 0 {
			if node, ok := d.popBottom(); ok {
				collector.record(node)
			}
		}
	}
	for {
		node, ok := d.popBottom()
		if !ok {
			break
		}
		collector.record(node)
	}
	close(done)
	thieves.Wait()

	collector.checkExactlyOnce(t, n)
}

// TestDequeConcurrentGrowDuringSteals specifically targets the
// trickiest part of this design (see deque.go's and circularBuffer's
// doc comments): an owner growing the backing buffer (via pushBottom)
// while multiple thieves are concurrently stealing, so that some
// thieves are guaranteed to observe a buffer pointer swap mid-flight.
// Uses a deliberately tiny fixed workload run many times (rather than
// one huge run) to maximize how often a steal's buf load and its
// subsequent CAS straddle a concurrent growTo.
func TestDequeConcurrentGrowDuringSteals(t *testing.T) {
	const nodesPerRound = 128 // several multiples of dequeInitialCapacity (32): guarantees growTo runs repeatedly
	const numThieves = 16
	const rounds = 50

	for round := 0; round < rounds; round++ {
		d := newDeque()
		collector := newDequeExactlyOnceCollector()
		done := make(chan struct{})

		var thieves sync.WaitGroup
		for th := 0; th < numThieves; th++ {
			thieves.Add(1)
			go func() {
				defer thieves.Done()
				for {
					if node, ok := d.stealTop(); ok {
						collector.record(node)
						continue
					}
					select {
					case <-done:
						if node, ok := d.stealTop(); ok {
							collector.record(node)
							continue
						}
						return
					default:
					}
				}
			}()
		}

		for i := 0; i < nodesPerRound; i++ {
			d.pushBottom(searchNode{assignment: assign.New(i)})
		}
		for {
			node, ok := d.popBottom()
			if !ok {
				break
			}
			collector.record(node)
		}
		close(done)
		thieves.Wait()

		collector.checkExactlyOnce(t, nodesPerRound)
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
