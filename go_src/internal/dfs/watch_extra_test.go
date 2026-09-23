package dfs

import (
	"slices"
	"testing"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
)

// TestNewWatchStatePopulatesWatcherLists verifies STAGE46.md's
// invariant directly, mirroring cdcl's identical test: immediately
// after construction, every clause's two current watches
// (ws.watch[c]) appear in that literal's own watcher list
// (ws.watchersFor(lit)).
func TestNewWatchStatePopulatesWatcherLists(t *testing.T) {
	clauses := []cnf.Clause{
		{1, 2, 3}, {-1, 2, -3}, {1, -4},
	}
	x := assign.New(4)
	ws, ok := newWatchState(clauses, x)
	if !ok {
		t.Fatal("newWatchState reported ok = false unexpectedly")
	}
	for c, w := range ws.watch {
		for _, lit := range w {
			if !slices.Contains(*ws.watchersFor(lit), c) {
				t.Errorf("clause %d watches %v but does not appear in watchersFor(%v) = %v", c, lit, lit, *ws.watchersFor(lit))
			}
		}
	}
}

// TestBCPWatcherListCompaction is STAGE46.md's central correctness
// test, mirroring cdcl's TestPropagateWatcherListCompaction exactly
// (same clause structure, same reasoning -- see that test's own doc
// comment in internal/cdcl/watch_test.go for the full trace): a
// single BCP call where, for the literal being falsified, one watcher
// moves away, two more stay (the second of which conflicts), and a
// fourth, positioned after the conflict in scan order, must survive
// completely untouched.
//
// Clauses, by index: 0 (D): {1,6,7}, moves away to watch +7 instead.
// 1 (B): {1,4}, stays, forces variable 4 true. 2 (A): {1,2}, stays,
// conflicts (variable 2 is pre-set False as pure test setup). 3 (C):
// {1,3}, positioned after the conflict, must never be scanned.
func TestBCPWatcherListCompaction(t *testing.T) {
	clauses := []cnf.Clause{
		{1, 6, 7}, // D
		{1, 4},    // B
		{1, 2},    // A
		{1, 3},    // C
	}
	x := assign.New(7)
	ws, ok := newWatchState(clauses, x)
	if !ok {
		t.Fatal("newWatchState reported ok = false unexpectedly")
	}
	if got := *ws.watchersFor(cnf.Literal(1)); !slices.Equal(got, []int{0, 1, 2, 3}) {
		t.Fatalf("watchersFor(+1) before BCP = %v, want [0 1 2 3] (test setup assumption violated)", got)
	}

	x[2] = assign.False // pure test setup; no trail entry, no propagation triggered by it
	x[1] = assign.False // the branch variable BCP is about to be called for

	status := BCP(clauses, ws, x, 1, nil)

	if status != Contra {
		t.Fatalf("BCP() = %v, want Contra (clause A, {1 2})", status)
	}

	wantWatchersOf1 := []int{1, 2, 3} // B, A, C -- D moved away
	if got := *ws.watchersFor(cnf.Literal(1)); !slices.Equal(got, wantWatchersOf1) {
		t.Errorf("watchersFor(+1) after BCP = %v, want %v", got, wantWatchersOf1)
	}
	wantWatchersOf7 := []int{0} // D, moved in
	if got := *ws.watchersFor(cnf.Literal(7)); !slices.Equal(got, wantWatchersOf7) {
		t.Errorf("watchersFor(+7) after BCP = %v, want %v (D should have moved here)", got, wantWatchersOf7)
	}
	if ws.watch[0] != [2]cnf.Literal{7, 6} && ws.watch[0] != [2]cnf.Literal{6, 7} {
		t.Errorf("clause 0's (D's) watch = %v, want {6 7} in either order", ws.watch[0])
	}
	if x[4] != assign.True {
		t.Errorf("variable 4 = %v, want assign.True (forced by clause B, {1 4})", x[4])
	}
	if x[3] != assign.Unassigned {
		t.Errorf("variable 3 = %v, want assign.Unassigned (clause C, {1 3}, must never have been scanned this call)", x[3])
	}
}

// TestCloneWatchStateWatcherListsAreIndependent is dfs-specific (cdcl
// has nothing analogous, since cdcl's solver is never cloned):
// watchState is genuinely cloned in two real places (bfsSeed's
// per-worker seeds, searchState.shedFrame's stolen snapshots), so
// watchersPositive/watchersNegative -- slices of slices, unlike
// watch's plain array values -- must be deep-copied, not merely
// slice-header-copied. A shallow copy would leave the clone's and the
// original's inner []int slices sharing the same backing array, so an
// in-place *value* write during BCP's compaction (list[keep] = c,
// changing what's stored at some index the original's own,
// unreplaced slice header would still read) would corrupt the
// original too -- reassigning the clone's own header at the end
// (*watchers = list[:keep]) is not enough on its own to catch this,
// since a header reassignment only affects the clone's slot in its
// own outer slice, not the original's. This test needs at least one
// clause to actually stay in the compacted list (triggering that
// value write) to exercise the bug at all -- reusing the same
// multi-clause structure as TestBCPWatcherListCompaction, with D
// moving away and B staying, to make sure the write actually happens.
func TestCloneWatchStateWatcherListsAreIndependent(t *testing.T) {
	clauses := []cnf.Clause{
		{1, 6, 7}, // D: moves away when literal 1 is falsified
		{1, 4},    // B: stays, writing into the compacted list at index 0
	}
	x := assign.New(7)
	original, ok := newWatchState(clauses, x)
	if !ok {
		t.Fatal("newWatchState reported ok = false unexpectedly")
	}
	originalWatchersOf1Before := append([]int(nil), *original.watchersFor(cnf.Literal(1))...)
	if !slices.Equal(originalWatchersOf1Before, []int{0, 1}) {
		t.Fatalf("watchersFor(+1) before BCP = %v, want [0 1] (test setup assumption violated)", originalWatchersOf1Before)
	}

	clone := cloneWatchState(original)
	cloneX := append(assign.Assignment(nil), x...)
	cloneX[1] = assign.False

	if status := BCP(clauses, clone, cloneX, 1, nil); status != OK {
		t.Fatalf("BCP() on clone = %v, want OK", status)
	}

	if got := *original.watchersFor(cnf.Literal(1)); !slices.Equal(got, originalWatchersOf1Before) {
		t.Errorf("original's watchersFor(+1) changed to %v after mutating only the clone, want unchanged %v", got, originalWatchersOf1Before)
	}
	if x[4] != assign.Unassigned {
		t.Errorf("original assignment x[4] = %v, want Unassigned (only cloneX should have been touched)", x[4])
	}
}
