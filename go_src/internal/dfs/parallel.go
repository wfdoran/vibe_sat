// Parallel depth-first search (STAGE18.md): a genuine divide-and-
// conquer parallel search, not a portfolio solver -- every worker
// explores a disjoint region of the same search tree that Run alone
// would have explored, redistributing work between workers via
// stealing rather than each independently searching the whole tree.
//
// The design (and the tricky termination-detection part specifically)
// was worked out and stress-tested standalone first, per STAGE18.md's
// instruction, in util/termination/go (a separate module at the
// repository root; nothing here imports it, and nothing there is
// imported here -- this file is an independent, from-scratch port of
// the same validated design, adapted from an abstract task simulation
// to real search nodes).
//
// Step 1 (bfsSeed) does a breadth-first expansion of the tree (pop
// the front, branch, push survivors to the back, rather than DFS's
// LIFO stack) until either it has collected numThreads nodes -- one
// seed per worker, at roughly the same depth, so each starts with a
// roughly similarly-sized disjoint subtree -- or the tree resolves on
// its own first (every branch pruned: proven UNSAT; some branch
// completed: SAT), in which case that answer is returned directly
// without ever spawning a worker (STAGE18.md: "the vast majority of
// the time it returns UNKNOWN and you carry on ... but with odd cases
// which return SAT or UNSAT, exit with that value"). If the tree is
// small enough to fully resolve before reaching numThreads seeds but
// without emptying either (impossible: bfsSeed only stops early via
// UNSAT-by-exhaustion or SAT-by-completion, both handled above), or
// simply has fewer than numThreads leaves to hand out, fewer workers
// than requested are spawned -- one per seed actually produced;
// REPORT16.md's termination-detection protocol (Step 2) already has
// to handle a worker starting with nothing to do, so a worker that
// would have started with nothing is simply never created at all.
//
// Step 2: each worker owns one deque (see deque.go) preloaded with
// one seed, and explores it exactly like Run's own stack -- pop,
// branch, push survivors -- except stealing from a peer's deque when
// its own empties, and participating in shared quiescence detection
// (see terminator.go) when a full sweep of every peer comes up empty
// too. The moment any worker completes an assignment, it signals
// every other worker to stop (the same single-winner
// compare-and-swap protocol Stage 17's parallel hillclimb/walksat
// use) and that worker's result -- and only that worker's -- is
// reported.
package dfs

import (
	"fmt"
	"math/rand/v2"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/occurrence"
)

// bfsVerdict identifies what bfsSeed concluded.
type bfsVerdict int

const (
	// bfsUnknown means the tree was not resolved during seeding; seeds
	// holds between 1 and numThreads nodes to hand out to workers.
	bfsUnknown bfsVerdict = iota
	// bfsSAT means some branch completed a satisfying assignment
	// during seeding itself.
	bfsSAT
	// bfsUNSAT means every branch was pruned during seeding, proving
	// the whole problem unsatisfiable before any worker was ever
	// spawned.
	bfsUNSAT
)

// bfsResult is bfsSeed's return value; see bfsVerdict for which
// fields are meaningful for which verdict.
type bfsResult struct {
	verdict    bfsVerdict
	assignment assign.Assignment // meaningful only if verdict == bfsSAT
	seeds      []searchNode      // meaningful only if verdict == bfsUnknown
	numNodes   int
	timedOut   bool // if true, every other field except numNodes is meaningless
}

// bfsSeed performs the breadth-first seeding phase described in the
// package doc comment, starting from root. clauses/lists/
// workingProblem/variant/rng are exactly what Run's own loop already
// needed for the same purpose (branch selection and BCP); timeLimit/
// startTime let it respect the overall deadline during seeding too,
// since a pathological formula could in principle take a while to
// even reach numThreads seeds.
func bfsSeed(clauses []cnf.Clause, lists *occurrence.Lists, workingProblem *cnf.Problem, root searchNode, numThreads int, variant SelectVarVariant, rng *rand.Rand, timeLimit *time.Duration, startTime time.Time) bfsResult {
	if allAssigned(root.assignment) {
		// Bootstrap's own unit propagation alone already fully solved
		// the formula; see allAssigned's doc comment for why this
		// check only ever needs to happen for the root (every other
		// node this function enqueues below is only ever pushed after
		// BCP reports OK, which by construction means it still has an
		// unassigned variable).
		return bfsResult{verdict: bfsSAT, assignment: root.assignment}
	}

	queue := []searchNode{root}
	numNodes := 0

	for len(queue) > 0 && len(queue) < numThreads {
		numNodes++
		if timeLimit != nil && numNodes&timeCheckInterval == 0 && time.Since(startTime) >= *timeLimit {
			return bfsResult{timedOut: true, numNodes: numNodes}
		}

		node := queue[0]
		queue = queue[1:]

		var i int
		if variant == SelectVarFast {
			i = SelectVarFastPick(workingProblem, node.assignment)
		} else {
			i = SelectVar(workingProblem, node.assignment, rng)
		}

		for _, v := range [2]assign.Value{assign.False, assign.True} {
			branchAssignment := append(assign.Assignment(nil), node.assignment...)
			branchAssignment[i] = v
			branchWatch := cloneWatchState(node.watch)

			switch BCP(clauses, lists, branchWatch, branchAssignment, i) {
			case Contra:
				continue
			case Done:
				return bfsResult{verdict: bfsSAT, assignment: branchAssignment, numNodes: numNodes}
			case OK:
				queue = append(queue, searchNode{assignment: branchAssignment, watch: branchWatch})
			}
		}
	}

	if len(queue) == 0 {
		return bfsResult{verdict: bfsUNSAT, numNodes: numNodes}
	}
	return bfsResult{verdict: bfsUnknown, seeds: queue, numNodes: numNodes}
}

// RunParallel runs the same search as Run, split across numThreads
// concurrent workers (STAGE18.md). numThreads <= 1 delegates straight
// to Run, with rng used exactly as it always has been -- so behavior
// (including every random choice made) is bit-for-bit identical to
// calling Run directly whenever multithreading isn't actually in
// use, matching the same guarantee Stage 17 established for the
// parallel hillclimb/walksat entry points.
//
// See the package doc comment for the two-step design (bfsSeed, then
// per-worker stealing/termination-detection). rng is used, before any
// worker starts, to derive one independent sub-generator per worker
// actually spawned (see newSubRand), exactly as Stage 17's parallel
// hillclimb/walksat do, so the overall result is fully reproducible
// given (rng's state, numThreads) even though which worker's answer
// wins a race to a solution is not. Result.NumNodes is the seeding
// phase's own node count plus the sum of every spawned worker's own
// count, win or lose -- the total search effort expended, matching
// Stage 17's convention for Result.Starts.
func RunParallel(problem *cnf.Problem, lists *occurrence.Lists, timeLimit *time.Duration, variant SelectVarVariant, numThreads int, rng *rand.Rand, verbose int) Result {
	if numThreads <= 1 {
		return Run(problem, lists, timeLimit, variant, rng, verbose)
	}

	if verbose >= 1 {
		fmt.Println("dfs:", describeParallelParams(timeLimit, variant, numThreads))
	}

	if problem.NumVars == 0 {
		satisfiable := len(problem.Clauses) == 0
		if verbose >= 1 {
			fmt.Println(map[bool]string{true: "SAT", false: "UNSAT"}[satisfiable])
		}
		return Result{Satisfiable: satisfiable, Assignment: assign.New(0)}
	}

	clauses, root, ok := bootstrap(problem)
	if !ok {
		if verbose >= 1 {
			fmt.Println("UNSAT")
		}
		return Result{Satisfiable: false}
	}
	workingProblem := &cnf.Problem{NumVars: problem.NumVars, Clauses: clauses}

	startTime := time.Now()
	seeding := bfsSeed(clauses, lists, workingProblem, root, numThreads, variant, rng, timeLimit, startTime)

	if seeding.timedOut {
		if verbose >= 1 {
			fmt.Println("UNKNOWN")
		}
		return Result{Satisfiable: false, NumNodes: seeding.numNodes, TimedOut: true}
	}
	switch seeding.verdict {
	case bfsSAT:
		if verbose >= 1 {
			fmt.Println("SAT")
		}
		return Result{Satisfiable: true, Assignment: seeding.assignment, NumNodes: seeding.numNodes}
	case bfsUNSAT:
		if verbose >= 1 {
			fmt.Println("UNSAT")
		}
		return Result{Satisfiable: false, NumNodes: seeding.numNodes}
	}

	// bfsVerdict == bfsUnknown: genuinely parallelize across
	// len(seeding.seeds) workers, which may be fewer than numThreads
	// if the tree had fewer than numThreads leaves to hand out (see
	// the package doc comment).
	seeds := seeding.seeds
	numWorkers := len(seeds)

	deques := make([]*deque, numWorkers)
	subRands := make([]*rand.Rand, numWorkers)
	for i, seed := range seeds {
		deques[i] = newDeque()
		deques[i].pushBottom(seed)
		subRands[i] = newSubRand(rng)
	}

	term := newTerminator(numWorkers)
	var stop atomic.Bool
	var timedOut atomic.Bool
	var winner atomic.Int32
	winner.Store(-1)

	results := make([]Result, numWorkers)
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = dfsWorker(dfsWorkerConfig{
				self:           i,
				deques:         deques,
				clauses:        clauses,
				lists:          lists,
				workingProblem: workingProblem,
				variant:        variant,
				rng:            subRands[i],
				term:           term,
				stop:           &stop,
				timedOut:       &timedOut,
				timeLimit:      timeLimit,
				startTime:      startTime,
			})
			if results[i].Satisfiable && stop.CompareAndSwap(false, true) {
				winner.Store(int32(i))
			}
		}(i)
	}
	wg.Wait()

	totalNodes := seeding.numNodes
	for _, r := range results {
		totalNodes += r.NumNodes
	}

	var final Result
	switch {
	case winner.Load() >= 0:
		w := results[winner.Load()]
		final = Result{Satisfiable: true, Assignment: w.Assignment, NumNodes: totalNodes}
	case timedOut.Load():
		final = Result{Satisfiable: false, NumNodes: totalNodes, TimedOut: true}
	default:
		final = Result{Satisfiable: false, NumNodes: totalNodes}
	}

	if verbose >= 1 {
		if final.TimedOut {
			fmt.Println("UNKNOWN")
		} else {
			fmt.Println(map[bool]string{true: "SAT", false: "UNSAT"}[final.Satisfiable])
		}
	}
	return final
}

// dfsWorkerConfig bundles one worker's fixed inputs, since there are
// enough of them (and enough shared cross-worker state) that a plain
// parameter list would be unwieldy.
type dfsWorkerConfig struct {
	self           int
	deques         []*deque
	clauses        []cnf.Clause
	lists          *occurrence.Lists
	workingProblem *cnf.Problem
	variant        SelectVarVariant
	rng            *rand.Rand
	term           *terminator
	stop           *atomic.Bool
	timedOut       *atomic.Bool
	timeLimit      *time.Duration
	startTime      time.Time
}

// dfsWorker is one worker's main loop: repeatedly take a node (its
// own deque first, then stealing from a peer), branch it exactly like
// Run's own loop does, and push any surviving children onto its own
// deque -- until it either completes a satisfying assignment, the
// shared deadline passes, another worker signals stop (found a
// solution, or the search timed out), or every worker's deque is
// confirmed empty (see terminator.go), which proves the whole problem
// unsatisfiable.
func dfsWorker(cfg dfsWorkerConfig) Result {
	state := newWorkerState(cfg.term)
	peers := shuffledPeers(len(cfg.deques), cfg.self, cfg.rng)
	numNodes := 0
	step := 0

	for {
		if cfg.stop.Load() {
			return Result{NumNodes: numNodes}
		}
		step++
		if cfg.timeLimit != nil && step&timeCheckInterval == 0 && time.Since(cfg.startTime) >= *cfg.timeLimit {
			cfg.timedOut.Store(true)
			cfg.stop.Store(true)
			return Result{NumNodes: numNodes, TimedOut: true}
		}

		node, ok := cfg.deques[cfg.self].popBottom()
		if !ok {
			node, ok = stealFromPeers(cfg.deques, peers)
		}
		if !ok {
			terminated := state.markIdle(func() bool {
				for _, d := range cfg.deques {
					if !d.isEmpty() {
						return false
					}
				}
				return true
			})
			if terminated {
				cfg.stop.Store(true)
				return Result{NumNodes: numNodes}
			}
			if cfg.stop.Load() {
				return Result{NumNodes: numNodes}
			}
			runtime.Gosched()
			continue
		}

		state.markActive()
		numNodes++

		var i int
		if cfg.variant == SelectVarFast {
			i = SelectVarFastPick(cfg.workingProblem, node.assignment)
		} else {
			i = SelectVar(cfg.workingProblem, node.assignment, cfg.rng)
		}

		for _, v := range [2]assign.Value{assign.False, assign.True} {
			branchAssignment := append(assign.Assignment(nil), node.assignment...)
			branchAssignment[i] = v
			branchWatch := cloneWatchState(node.watch)

			switch BCP(cfg.clauses, cfg.lists, branchWatch, branchAssignment, i) {
			case Contra:
				continue
			case Done:
				return Result{Satisfiable: true, Assignment: branchAssignment, NumNodes: numNodes}
			case OK:
				cfg.deques[cfg.self].pushBottom(searchNode{assignment: branchAssignment, watch: branchWatch})
			}
		}
	}
}

// stealFromPeers tries, in order, to steal one node from each of
// peers' deques, returning the first success.
func stealFromPeers(deques []*deque, peers []int) (searchNode, bool) {
	for _, p := range peers {
		if node, ok := deques[p].stealTop(); ok {
			return node, ok
		}
	}
	return searchNode{}, false
}

// shuffledPeers returns every worker index except self, in a random
// order (fixed once per worker, using rng, matching
// util/termination/go's identical helper) so that many
// simultaneously-idle workers sweeping for a steal don't all hammer
// the same victim first.
func shuffledPeers(numWorkers, self int, rng *rand.Rand) []int {
	peers := make([]int, 0, numWorkers-1)
	for i := 0; i < numWorkers; i++ {
		if i != self {
			peers = append(peers, i)
		}
	}
	rng.Shuffle(len(peers), func(a, b int) { peers[a], peers[b] = peers[b], peers[a] })
	return peers
}

// newSubRand draws two fresh uint64s from rng to seed a new,
// independent *rand.Rand, matching Stage 17's identical helper in
// internal/hillclimb (see its doc comment for why: giving every
// worker its own generator, derived deterministically and
// sequentially before any worker starts, rather than sharing one
// *rand.Rand across goroutines).
func newSubRand(rng *rand.Rand) *rand.Rand {
	return rand.New(rand.NewPCG(rng.Uint64(), rng.Uint64()))
}

// describeParallelParams is describeParams, extended with the worker
// count, for RunParallel's "dfs: ..." announcement.
func describeParallelParams(timeLimit *time.Duration, variant SelectVarVariant, numThreads int) string {
	return fmt.Sprintf("num_threads=%d %s", numThreads, describeParams(timeLimit, variant))
}
