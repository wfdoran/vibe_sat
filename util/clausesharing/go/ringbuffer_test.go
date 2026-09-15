package clausesharing

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// clauseForID deterministically derives a fake clause's literals from
// its publish ID, so a reader can independently verify a clause it
// received is exactly the one that was actually published under that
// ID -- catching any corruption (a torn write, a mixed-up slot,
// anything) rather than merely trusting the buffer.
func clauseForID(id uint64) []int32 {
	n := int(id%MaxLen) + 1
	lits := make([]int32, n)
	for i := range lits {
		lits[i] = int32(id) + int32(i)
	}
	return lits
}

// MaxLen bounds the fake clause lengths this test generates; real
// callers would filter to short clauses anyway (see ringbuffer.go's
// package doc).
const MaxLen = 8

func clauseMatchesID(c ClauseData) bool {
	want := clauseForID(c.ID)
	if len(want) != len(c.Lits) {
		return false
	}
	for i := range want {
		if want[i] != c.Lits[i] {
			return false
		}
	}
	return true
}

// TestSingleWriterMultiReaderNoCorruption is the core validation for
// STAGE20.md's "can you implement [continuous clause sharing] with
// minimal contention" question: one writer goroutine continuously
// publishes into a small ring buffer while several reader goroutines
// continuously drain it, all with no locks anywhere. Every clause any
// reader observes must exactly match what was actually published
// under that ID -- any mismatch means the lock-free design corrupted
// data, which would be a real bug, not just a missed optimization.
func TestSingleWriterMultiReaderNoCorruption(t *testing.T) {
	const (
		numReaders  = 16
		capacity    = 64
		runDuration = 300 * time.Millisecond
	)

	buf := NewExportBuffer(capacity)
	var nextID atomic.Uint64
	stop := make(chan struct{})
	var totalReads atomic.Int64
	var corrupted atomic.Int64

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			id := nextID.Load()
			buf.Publish(id, clauseForID(id))
			nextID.Store(id + 1)
		}
	}()

	for r := 0; r < numReaders; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var idx uint64
			for {
				select {
				case <-stop:
					return
				default:
				}
				c, ok := buf.TryRead(idx)
				idx = (idx + 1) % buf.Capacity()
				if !ok {
					continue
				}
				totalReads.Add(1)
				if !clauseMatchesID(c) {
					corrupted.Add(1)
				}
			}
		}()
	}

	time.Sleep(runDuration)
	close(stop)
	wg.Wait()

	t.Logf("published=%d totalReads=%d corrupted=%d", nextID.Load(), totalReads.Load(), corrupted.Load())
	if corrupted.Load() != 0 {
		t.Fatalf("observed %d corrupted clause reads out of %d -- lock-free ring buffer is not safe",
			corrupted.Load(), totalReads.Load())
	}
	if nextID.Load() == 0 {
		t.Fatal("writer never published anything; test is not exercising the buffer")
	}
	if totalReads.Load() == 0 {
		t.Fatal("readers never observed anything; test is not exercising the buffer")
	}
}

// TestManyIndependentBuffersManyReaders simulates the real shape: N
// worker threads, each with its own ExportBuffer it alone publishes
// to, while every other worker continuously drains everyone else's
// buffer -- the actual N-to-N clause-sharing topology a multithreaded
// CDCL design would use, not just one buffer in isolation.
func TestManyIndependentBuffersManyReaders(t *testing.T) {
	const (
		numWorkers  = 32
		capacity    = 32
		runDuration = 300 * time.Millisecond
	)

	buffers := make([]*ExportBuffer, numWorkers)
	for i := range buffers {
		buffers[i] = NewExportBuffer(capacity)
	}

	stop := make(chan struct{})
	var corrupted atomic.Int64
	var totalReads atomic.Int64
	var wg sync.WaitGroup

	for w := 0; w < numWorkers; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			var nextID uint64
			for {
				select {
				case <-stop:
					return
				default:
				}
				buffers[w].Publish(nextID, clauseForID(nextID))
				nextID++
			}
		}()
	}

	for reader := 0; reader < numWorkers; reader++ {
		reader := reader
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Track a read cursor per peer buffer (every buffer but our own).
			cursors := make([]uint64, numWorkers)
			for {
				select {
				case <-stop:
					return
				default:
				}
				for peer := 0; peer < numWorkers; peer++ {
					if peer == reader {
						continue
					}
					c, ok := buffers[peer].TryRead(cursors[peer])
					cursors[peer] = (cursors[peer] + 1) % buffers[peer].Capacity()
					if !ok {
						continue
					}
					totalReads.Add(1)
					if !clauseMatchesID(c) {
						corrupted.Add(1)
					}
				}
			}
		}()
	}

	time.Sleep(runDuration)
	close(stop)
	wg.Wait()

	t.Logf("workers=%d totalReads=%d corrupted=%d", numWorkers, totalReads.Load(), corrupted.Load())
	if corrupted.Load() != 0 {
		t.Fatalf("observed %d corrupted clause reads across %d worker buffers", corrupted.Load(), numWorkers)
	}
	if totalReads.Load() == 0 {
		t.Fatal("no reads observed; test is not exercising the buffers")
	}
}
