package termination

import (
	"math/rand/v2"
	"runtime"
	"sync"
	"sync/atomic"
)

// task is one unit of simulated work: processing it produces zero,
// one, or two child tasks with strictly smaller budget than their
// parent, which guarantees the whole simulated tree is finite (every
// task eventually reaches budget 0, a forced leaf) regardless of how
// the random branching decisions land.
type task struct {
	budget int
}

// spawnChildren decides how many children (0, 1, or 2) a task with
// the given budget produces, mimicking a real search node's
// Contra/OK/OK-both-branches outcomes. Budget 0 is a forced leaf
// (0 children), matching a real search hitting some depth/resource
// limit; budget > 0 gives every outcome real probability, weighted so
// the tree tends to shrink (fewer than 2 children on average) and so
// stays a manageable size for a stress test repeated many times.
func spawnChildren(budget int, rng *rand.Rand) []task {
	if budget <= 0 {
		return nil
	}
	child := task{budget: budget - 1}
	switch n := rng.IntN(10); {
	case n < 3: // 30%: pruned, like a Contra branch.
		return nil
	case n < 7: // 40%: one surviving branch.
		return []task{child}
	default: // 30%: both branches survive.
		return []task{child, child}
	}
}

// SimulateResult reports the outcome of one Simulate run, for the
// caller to check against the ground truth it independently expects.
type SimulateResult struct {
	Processed int64 // number of tasks actually processed
	Spawned   int64 // number of tasks created (including the root)
}

// Simulate runs numWorkers concurrent workers cooperatively processing
// the randomized tree of tasks rooted at one task of the given
// budget, entirely through work-stealing: only worker 0 starts with
// anything (mirroring the real search's BFS-then-seed-one-per-worker
// setup, collapsed here to the single-seed extreme, which stresses
// the stealing/termination logic hardest since every other worker
// starts completely idle). It returns once every worker has confirmed
// global termination (see Terminator) and exited.
//
// seed makes the random branching decisions (and steal order, see
// shuffledPeers) reproducible for a given (numWorkers, budget, seed),
// even though the actual interleaving of which worker processes which
// task is still scheduler-dependent -- that's fine, since what the
// caller checks is the aggregate invariant Processed == Spawned, not
// any particular execution order.
func Simulate(numWorkers int, budget int, seed uint64) SimulateResult {
	if numWorkers < 1 {
		panic("numWorkers must be at least 1")
	}

	deques := make([]*Deque[task], numWorkers)
	for i := range deques {
		deques[i] = &Deque[task]{}
	}

	var processed atomic.Int64
	var spawned atomic.Int64
	spawned.Store(1) // the root, below
	deques[0].PushBottom(task{budget: budget})

	term := New(numWorkers)
	var stop atomic.Bool

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(seed, uint64(i)+1))
			state := NewWorkerState(term)
			peers := shuffledPeers(numWorkers, i, rng)

			for {
				if stop.Load() {
					return
				}

				t, ok := deques[i].PopBottom()
				if ok {
					state.MarkActive()
					processed.Add(1)
					for _, child := range spawnChildren(t.budget, rng) {
						spawned.Add(1)
						deques[i].PushBottom(child)
					}
					continue
				}

				found := false
				for _, p := range peers {
					if t, ok := deques[p].StealTop(); ok {
						state.MarkActive()
						processed.Add(1)
						for _, child := range spawnChildren(t.budget, rng) {
							spawned.Add(1)
							deques[i].PushBottom(child)
						}
						found = true
						break
					}
				}
				if found {
					continue
				}

				terminated := state.MarkIdle(func() bool {
					for _, d := range deques {
						if !d.IsEmpty() {
							return false
						}
					}
					return true
				})
				if terminated {
					stop.Store(true)
					return
				}
				if stop.Load() {
					return
				}
				runtime.Gosched()
			}
		}(i)
	}
	wg.Wait()

	return SimulateResult{Processed: processed.Load(), Spawned: spawned.Load()}
}

// shuffledPeers returns every worker index except self, in a random
// order (per-call, using rng), so that many simultaneously-idle
// workers sweeping for a steal don't all hammer the same victim
// first (which would just move the contention hotspot rather than
// removing it).
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
