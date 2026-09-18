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

			switch BCP(clauses, lists, branchWatch, branchAssignment, i, nil) {
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

// shedCheckInterval controls how often (in nodes explored) a worker
// considers shedding a spare branch onto its own deque for another
// worker to steal (see searchState.shedFrame), whenever that deque
// currently looks empty.
//
// STAGE32.md/REPORT32.md: this was originally 0xff (checking often
// costs almost nothing -- deque.isEmpty() is two atomic loads, not a
// syscall -- and checking often was meant to keep an idle peer from
// waiting long for something stealable). Direct measurement on a real
// benchmark file (benchmark/uuf175-753/uuf175-083.cnf) found that
// value catastrophic instead: 2 threads took over 90 seconds and 1.5M+
// nodes without finishing, versus 1.86 seconds and ~40,000 nodes
// (matching single-threaded almost exactly) with shedding effectively
// disabled. The cause is not a correctness bug -- every shed subtree
// is still explored exactly once -- but a real cost specific to this
// stage's design: each shed hands a subtree to a *freshly started*
// search, which calls SelectVar's weighted heuristic (and its
// rng-driven tie-breaking) starting over from that point, rather than
// inheriting whatever sequence of choices the original, continuous
// exploration would have made. For a heuristic this sensitive to tie-
// break luck, restarting it often enough can turn a well-behaved
// search into a much larger one, and this project has no evidence
// (only this one measurement) about how which instances are
// vulnerable to it or by how much.
//
// A much larger interval -- shedding only after many hundreds of
// thousands of nodes -- keeps this from ever triggering at all on
// typical benchmark-sized searches (thousands to low millions of
// nodes total), which is exactly what eliminated the regression above,
// while still providing occasional, if infrequent, rebalancing on the
// genuinely huge searches (REPORT29.md's 17.7-million-clause files)
// this stage's memory fix was actually written for -- and, crucially,
// memory safety does not depend on this constant at all: an unshed
// frame costs a few bytes on this worker's own stack, not a clause-
// sized clone, regardless of how rarely (or never) shedding fires. See
// REPORT32.md for the full investigation and the case for erring this
// conservative rather than searching further for a safer middle
// ground under this stage's time budget.
const shedCheckInterval = 0xfffff

// dfsWorker is one worker's main loop (STAGE32.md/REPORT32.md): unlike
// Stage 18 through Stage 31, a worker no longer clones a new
// searchNode for every branch it explores -- it runs a single local
// searchState (exactly like Run's own, see dfs.go), mutating one
// shared assignment and watch state in place via an explicit,
// incrementally-backtracked trail, for as long as it has its own local
// work. Only when that local search pauses (checkPause below) does it
// consider the relatively rare, still O(clauses)-costly operations:
// shedding one spare branch onto its own deque so an idle peer has
// something to steal (searchState.shedFrame), and checking the shared
// stop/deadline signals. Once its own local search is fully exhausted
// (stepUNSAT), it falls back to exactly the same steal-then-
// terminate-detect protocol every previous stage's version used
// (stealFromPeers, terminator) -- that part of the design needed no
// change, since it already only ever handles self-contained
// searchNode snapshots, regardless of how rarely they are now created.
func dfsWorker(cfg dfsWorkerConfig) Result {
	workerState := newWorkerState(cfg.term)
	peers := shuffledPeers(len(cfg.deques), cfg.self, cfg.rng)
	numNodes := 0

	checkPause := func(numNodesThisCall int) bool {
		total := numNodes + numNodesThisCall
		if cfg.stop.Load() {
			return true
		}
		if cfg.timeLimit != nil && total&timeCheckInterval == 0 && time.Since(cfg.startTime) >= *cfg.timeLimit {
			return true
		}
		// numNodesThisCall > 0 matters, not just total&shedCheckInterval:
		// without it, a shed-triggered pause that finds nothing to shed
		// (search.shedFrame's shedNone -- e.g. before this call has
		// committed to any frame's False branch yet) would return to
		// step() and immediately see the exact same total again, since
		// nothing changed, pausing forever with zero progress made.
		// Requiring real progress since this call started guarantees the
		// next check is against a different total.
		return numNodesThisCall > 0 && total&shedCheckInterval == 0 && cfg.deques[cfg.self].isEmpty()
	}

	// findWork tries this worker's own deque first, then peers, then
	// (if both fail) participates in termination detection, retrying
	// until either work turns up or global termination is confirmed.
	// ok=false means the whole search is over (UNSAT, or another
	// worker already found SAT/timed out); cfg.stop is already set in
	// that case.
	//
	// STAGE32.md/REPORT32.md: this is also used for a worker's very
	// first node, not just for resuming after its own local search
	// exhausts -- an earlier version treated "my own initial seed is
	// missing from my own deque" as unreachable and returned
	// immediately without telling the terminator, which is wrong: a
	// peer can steal that seed (via stealTop) before this worker ever
	// gets to popBottom it itself, since nothing orders "worker N's
	// goroutine starts running" before "some other already-running
	// worker's steal sweep reaches worker N's deque." When that
	// race's loser silently returned, it permanently undercounted the
	// terminator's active total by one, so the shared active count
	// could never reach zero and global termination was never
	// confirmed -- observed directly as one worker spinning forever
	// on a fully torn-down, provably-empty set of deques. Routing the
	// initial pop through the exact same path as every later
	// exhaustion closes this: whichever worker loses that race for
	// its own seed just finds (or fails to find) other work exactly
	// like any other idle worker would.
	findWork := func() (node searchNode, ok bool) {
		for {
			node, ok = cfg.deques[cfg.self].popBottom()
			if !ok {
				node, ok = stealFromPeers(cfg.deques, peers)
			}
			if ok {
				workerState.markActive()
				return node, true
			}

			terminated := workerState.markIdle(func() bool {
				for _, d := range cfg.deques {
					if !d.isEmpty() {
						return false
					}
				}
				return true
			})
			if terminated {
				cfg.stop.Store(true)
				return searchNode{}, false
			}
			if cfg.stop.Load() {
				return searchNode{}, false
			}
			runtime.Gosched()
		}
	}

	node, ok := findWork()
	if !ok {
		return Result{NumNodes: numNodes}
	}
	search := startSearch(cfg.clauses, cfg.lists, cfg.workingProblem, node, cfg.variant, cfg.rng)
	numNodes++ // this node's own first frame, matching Run's "root frame counts as node 1" convention

	for {
		outcome, stepped := search.step(cfg.workingProblem, cfg.variant, cfg.rng, checkPause)
		numNodes += stepped

		switch outcome {
		case stepSAT:
			return Result{Satisfiable: true, Assignment: search.x, NumNodes: numNodes}

		case stepPaused:
			if cfg.stop.Load() {
				return Result{NumNodes: numNodes}
			}
			if cfg.timeLimit != nil && time.Since(cfg.startTime) >= *cfg.timeLimit {
				cfg.timedOut.Store(true)
				cfg.stop.Store(true)
				return Result{NumNodes: numNodes, TimedOut: true}
			}
			// Otherwise this was a shed checkpoint: our own deque looked
			// empty, so offer a peer our shallowest spare branch, if we
			// have one, then resume our own local search unchanged.
			if shedResult, satAssignment := search.shedFrame(cfg.deques[cfg.self]); shedResult == shedSAT {
				return Result{Satisfiable: true, Assignment: satAssignment, NumNodes: numNodes}
			}

		case stepUNSAT:
			// Our own local search is fully exhausted; look for more
			// work exactly like this worker's very first node did.
			node, ok := findWork()
			if !ok {
				return Result{NumNodes: numNodes}
			}
			search = startSearch(cfg.clauses, cfg.lists, cfg.workingProblem, node, cfg.variant, cfg.rng)
			numNodes++ // this node's own first frame
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
