package lockfreedeque

import (
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestPushPopStealAreConsistent mirrors go_src/internal/dfs's own
// TestDequePushPopStealAreConsistent: a pushed item must be poppable
// from the bottom, and StealTop must take from the opposite end (the
// earliest-pushed item still present), never the same end PopBottom
// would.
func TestPushPopStealAreConsistent(t *testing.T) {
	d := New[int]()
	for _, v := range []int{1, 2, 3} {
		d.PushBottom(v)
	}

	stolen, ok := d.StealTop()
	if !ok || stolen != 1 {
		t.Fatalf("StealTop() = (%v, %v), want (1, true) -- the oldest-pushed item", stolen, ok)
	}

	popped, ok := d.PopBottom()
	if !ok || popped != 3 {
		t.Fatalf("PopBottom() = (%v, %v), want (3, true) -- the most-recently-pushed item", popped, ok)
	}

	if d.IsEmpty() {
		t.Fatal("deque should still have one item left (2)")
	}
	popped, ok = d.PopBottom()
	if !ok || popped != 2 {
		t.Fatalf("PopBottom() = (%v, %v), want (2, true)", popped, ok)
	}
	if !d.IsEmpty() {
		t.Error("deque should now be empty")
	}
	if _, ok := d.PopBottom(); ok {
		t.Error("PopBottom() on an empty deque returned ok = true")
	}
	if _, ok := d.StealTop(); ok {
		t.Error("StealTop() on an empty deque returned ok = true")
	}
}

// TestGrowPreservesOrderAndContents pushes far more items than
// initialCapacity (forcing several growTo calls) purely sequentially,
// then confirms every one comes back out via PopBottom in exact LIFO
// order with the right contents -- the simplest possible check that
// growTo's copy is correct before any concurrency is layered on top.
func TestGrowPreservesOrderAndContents(t *testing.T) {
	const n = 500 // far more than initialCapacity (8); forces multiple doublings
	d := New[int]()
	for i := 0; i < n; i++ {
		d.PushBottom(i)
	}
	for i := n - 1; i >= 0; i-- {
		got, ok := d.PopBottom()
		if !ok {
			t.Fatalf("PopBottom() reported empty with %d items still expected", i+1)
		}
		if got != i {
			t.Fatalf("PopBottom() = %d, want %d", got, i)
		}
	}
	if !d.IsEmpty() {
		t.Error("deque should be empty after popping every pushed item")
	}
}

// TestGrowPreservesContentsForStealing is TestGrowPreservesOrderAndContents's
// counterpart for the other end: push far more than initialCapacity,
// then drain entirely via StealTop, confirming FIFO order (oldest
// first) and that growth didn't corrupt or lose anything reachable
// from the top.
func TestGrowPreservesContentsForStealing(t *testing.T) {
	const n = 500
	d := New[int]()
	for i := 0; i < n; i++ {
		d.PushBottom(i)
	}
	for i := 0; i < n; i++ {
		got, ok := d.StealTop()
		if !ok {
			t.Fatalf("StealTop() reported empty with %d items still expected", n-i)
		}
		if got != i {
			t.Fatalf("StealTop() = %d, want %d", got, i)
		}
	}
	if !d.IsEmpty() {
		t.Error("deque should be empty after stealing every pushed item")
	}
}

// exactlyOnceCollector is a test-only (deliberately not lock-free
// itself -- it exists purely to observe the Deque under test, not to
// be part of what's under test) recorder used by every stress test
// below: every goroutine reports each item it successfully removed,
// and the final check confirms the whole expected set was seen with
// no duplicates and nothing missing.
type exactlyOnceCollector struct {
	mu   sync.Mutex
	seen map[int]int // item -> how many times it was observed
}

func newExactlyOnceCollector() *exactlyOnceCollector {
	return &exactlyOnceCollector{seen: make(map[int]int)}
}

func (c *exactlyOnceCollector) record(item int) {
	c.mu.Lock()
	c.seen[item]++
	c.mu.Unlock()
}

// checkExactlyOnce fails t unless every item in [0, n) was recorded
// exactly once.
func (c *exactlyOnceCollector) checkExactlyOnce(t *testing.T, n int) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.seen) != n {
		t.Errorf("saw %d distinct items, want %d", len(c.seen), n)
	}
	for i := 0; i < n; i++ {
		if got := c.seen[i]; got != 1 {
			t.Errorf("item %d observed %d times, want exactly 1", i, got)
		}
	}
}

// TestConcurrentOwnerAndThievesExactlyOnce is this package's main
// stress test: one owner goroutine pushes n items (interleaved with
// occasionally popping its own, to exercise PopBottom concurrently
// with stealing too, not just PushBottom), while several thief
// goroutines hammer StealTop concurrently until the deque drains.
// Every item removed by anyone (owner's pops, any thief's steals) is
// recorded; the test then confirms the full set [0, n) was delivered
// exactly once each -- no item lost, none duplicated -- which is the
// core correctness property a work-stealing deque must have
// regardless of how push/pop/steal happen to interleave.
func TestConcurrentOwnerAndThievesExactlyOnce(t *testing.T) {
	const n = 20000
	const numThieves = 8

	d := New[int]()
	collector := newExactlyOnceCollector()
	var pushedCount atomic.Int64
	done := make(chan struct{})

	var thieves sync.WaitGroup
	for th := 0; th < numThieves; th++ {
		thieves.Add(1)
		go func(seed uint64) {
			defer thieves.Done()
			rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
			for {
				if item, ok := d.StealTop(); ok {
					collector.record(item)
					continue
				}
				select {
				case <-done:
					// Owner has finished pushing and popping; drain any
					// remaining items before giving up, since a steal
					// can legitimately race and fail even when items
					// are still present.
					if item, ok := d.StealTop(); ok {
						collector.record(item)
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
		d.PushBottom(i)
		pushedCount.Add(1)
		// Occasionally pop the owner's own item back out immediately,
		// exercising PopBottom concurrently with the thieves above --
		// but only sometimes, so plenty of items are left for stealing
		// too.
		if rng.IntN(3) == 0 {
			if item, ok := d.PopBottom(); ok {
				collector.record(item)
			}
		}
	}
	// Drain whatever the owner didn't already pop, via PopBottom, same
	// as Run's own loop would at the end of a search.
	for {
		item, ok := d.PopBottom()
		if !ok {
			break
		}
		collector.record(item)
	}
	close(done)
	thieves.Wait()

	collector.checkExactlyOnce(t, n)
}

// TestConcurrentGrowDuringSteals specifically targets the trickiest
// part of this design (see circularBuffer.growTo's and
// Deque.StealTop's doc comments): an owner growing the backing buffer
// (via PushBottom) while multiple thieves are concurrently stealing,
// so that some thieves are guaranteed to observe a buffer pointer
// swap mid-flight. Uses a deliberately tiny fixed workload run many
// times (rather than one huge run) to maximize how often a steal's
// buf.Load() and its subsequent CAS straddle a concurrent growTo.
func TestConcurrentGrowDuringSteals(t *testing.T) {
	const itemsPerRound = 64 // several multiples of initialCapacity (8): guarantees growTo runs repeatedly
	const numThieves = 16
	const rounds = 200

	for round := 0; round < rounds; round++ {
		d := New[int]()
		collector := newExactlyOnceCollector()
		done := make(chan struct{})

		var thieves sync.WaitGroup
		for th := 0; th < numThieves; th++ {
			thieves.Add(1)
			go func() {
				defer thieves.Done()
				for {
					if item, ok := d.StealTop(); ok {
						collector.record(item)
						continue
					}
					select {
					case <-done:
						if item, ok := d.StealTop(); ok {
							collector.record(item)
							continue
						}
						return
					default:
					}
				}
			}()
		}

		for i := 0; i < itemsPerRound; i++ {
			d.PushBottom(i)
		}
		// Give thieves a chance to race the growth above before the
		// owner starts draining its own end too.
		for {
			item, ok := d.PopBottom()
			if !ok {
				break
			}
			collector.record(item)
		}
		close(done)
		thieves.Wait()

		collector.checkExactlyOnce(t, itemsPerRound)
	}
}
