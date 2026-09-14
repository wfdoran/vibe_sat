package termination

import (
	"testing"
	"time"
)

// TestDecrementConfirmsTerminationOnlyWhenTrulyQuiescent exercises
// Terminator directly (no Deque/goroutines involved) against the
// exact race the package doc comment describes: with two workers,
// the first Decrement (bringing the count to 1) must never be
// reported as terminated; the second (bringing it to 0) must consult
// recheck, and only report termination if recheck says so, correctly
// undoing the count if not.
func TestDecrementConfirmsTerminationOnlyWhenTrulyQuiescent(t *testing.T) {
	term := New(2)

	terminated, stillIdle := term.Decrement(func() bool {
		t.Fatal("recheck must not be called before the count reaches zero")
		return false
	})
	if terminated || !stillIdle {
		t.Fatalf("first Decrement: got (%v, %v), want (false, true)", terminated, stillIdle)
	}

	recheckCalled := false
	terminated, stillIdle = term.Decrement(func() bool {
		recheckCalled = true
		return false // simulate: some peer still has visible work
	})
	if !recheckCalled {
		t.Fatal("recheck was not called when the count reached zero")
	}
	if terminated || stillIdle {
		t.Fatalf("second Decrement (recheck=false): got (%v, %v), want (false, false)", terminated, stillIdle)
	}
	if got := term.active.Load(); got != 1 {
		t.Errorf("active = %d after an undone decrement, want 1 (restored)", got)
	}

	// Now genuinely terminate: bring the count back to zero and let
	// recheck confirm quiescence this time.
	terminated, stillIdle = term.Decrement(func() bool { return true })
	if !terminated || !stillIdle {
		t.Fatalf("third Decrement (recheck=true): got (%v, %v), want (true, true)", terminated, stillIdle)
	}
}

// TestWorkerStateMarkIdleIsIdempotent verifies that calling MarkIdle
// repeatedly without an intervening MarkActive only touches the
// shared Terminator once, matching the "exactly one decrement per
// complete failed sweep" contract the package doc comment describes.
func TestWorkerStateMarkIdleIsIdempotent(t *testing.T) {
	term := New(3)
	w := NewWorkerState(term)

	w.MarkIdle(func() bool { return false })
	if got := term.active.Load(); got != 2 {
		t.Fatalf("active after first MarkIdle = %d, want 2", got)
	}

	// A second, third, ... MarkIdle call with no MarkActive in
	// between must be a no-op: this worker already reported idle and
	// learned nothing new.
	for i := 0; i < 3; i++ {
		if w.MarkIdle(func() bool {
			t.Fatal("recheck must not be called on a redundant MarkIdle")
			return false
		}) {
			t.Fatal("redundant MarkIdle must never report termination")
		}
	}
	if got := term.active.Load(); got != 2 {
		t.Fatalf("active after redundant MarkIdle calls = %d, want unchanged at 2", got)
	}

	w.MarkActive()
	if got := term.active.Load(); got != 3 {
		t.Fatalf("active after MarkActive = %d, want 3 (restored)", got)
	}
	// A second MarkActive with no MarkIdle in between must also be a
	// no-op.
	w.MarkActive()
	if got := term.active.Load(); got != 3 {
		t.Fatalf("active after redundant MarkActive = %d, want unchanged at 3", got)
	}
}

// TestSimulateProcessesEveryTaskExactlyOnce is the core stress test:
// across a range of worker counts (including large ones -- STAGE18.md
// asks this to scale to 128, maybe more, not the "16" REPORT16.md
// speculatively cited) and many random seeds, every run must process
// exactly as many tasks as were spawned (no task lost, and none
// double-processed), and must actually terminate (return at all)
// rather than hang -- both are checked with a per-run timeout so a
// genuine deadlock/livelock fails the test loudly instead of stalling
// the whole suite.
func TestSimulateProcessesEveryTaskExactlyOnce(t *testing.T) {
	workerCounts := []int{1, 2, 3, 4, 8, 16, 32, 64, 128}
	budget := 14 // a few thousand tasks per run at this branching rate

	for _, numWorkers := range workerCounts {
		for seed := uint64(0); seed < 20; seed++ {
			numWorkers, seed := numWorkers, seed // capture for t.Run closures
			t.Run("", func(t *testing.T) {
				done := make(chan SimulateResult, 1)
				go func() {
					done <- Simulate(numWorkers, budget, seed)
				}()

				select {
				case result := <-done:
					if result.Processed != result.Spawned {
						t.Errorf(
							"numWorkers=%d seed=%d: processed=%d spawned=%d (lost or duplicated work)",
							numWorkers, seed, result.Processed, result.Spawned,
						)
					}
				case <-time.After(10 * time.Second):
					t.Fatalf("numWorkers=%d seed=%d: Simulate did not return within 10s (deadlock/livelock in termination detection)", numWorkers, seed)
				}
			})
		}
	}
}

// TestSimulateSingleWorkerAlwaysTerminates is a focused edge case:
// with only one worker (so every sweep-of-peers is trivially empty,
// exercising Decrement/recheck with zero peers to check), the
// simulation must still terminate correctly and process every task
// the single worker itself spawns.
func TestSimulateSingleWorkerAlwaysTerminates(t *testing.T) {
	for seed := uint64(0); seed < 50; seed++ {
		result := Simulate(1, 12, seed)
		if result.Processed != result.Spawned {
			t.Errorf("seed=%d: processed=%d spawned=%d", seed, result.Processed, result.Spawned)
		}
	}
}
