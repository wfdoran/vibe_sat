package cdcl

import (
	"slices"
	"testing"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/params"
)

// TestNewSolverPopulatesWatcherLists verifies STAGE45.md's invariant
// directly: immediately after construction, every clause's two
// current watches (s.watch[c]) appear in that literal's own watcher
// list (s.watchersFor(lit)), for every clause.
func TestNewSolverPopulatesWatcherLists(t *testing.T) {
	problem := &cnf.Problem{NumVars: 4, Clauses: []cnf.Clause{
		{1, 2, 3}, {-1, 2, -3}, {1, -4},
	}}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	for c, w := range s.watch {
		for _, lit := range w {
			if !slices.Contains(*s.watchersFor(lit), c) {
				t.Errorf("clause %d watches %v but does not appear in watchersFor(%v) = %v", c, lit, lit, *s.watchersFor(lit))
			}
		}
	}
}

// TestPropagateWatcherListCompaction is STAGE45.md's central
// correctness test: a single propagate() call where, for the literal
// being falsified, one watcher moves away (found a replacement),
// two more stay (no replacement available), and the *last* of those
// two conflicts -- deliberately positioned so a real gap already
// exists in the watcher list (from the earlier move-away) by the time
// the conflict is found, and at least one further, not-yet-scanned
// entry exists after it in the original list. Every one of these
// cases has to be handled correctly by the in-place compaction for
// the literal's own watcher list to end up correct afterward.
//
// Clauses, by index:
//
//	0 (D): {1, 6, 7} -- 3 literals; watches 1 and 6 initially, so a
//	  replacement (7) is available once literal 1 goes false: moves
//	  away from watchersFor(+1) to watchersFor(+7).
//	1 (B): {1, 4} -- 2 literals; no replacement possible once literal
//	  1 goes false (only literal 4 remains, and it's unassigned, not
//	  a valid *replacement* target since chooseWatch only replaces
//	  the falsified slot, not both) -- stays a watcher of +1, and
//	  forces variable 4 true.
//	2 (A): {1, 2} -- 2 literals; variable 2 is pre-set False (as pure
//	  test setup, not via propagate, so nothing else propagates as a
//	  side effect), so once literal 1 also goes false, this clause is
//	  fully falsified: a conflict, but it still stays a watcher of +1
//	  (the "no replacement" case applies regardless of whether the
//	  clause conflicts).
//	3 (C): {1, 3} -- 2 literals; positioned after the conflict in
//	  watchersFor(+1)'s scan order, so propagate must never even reach
//	  it this call -- it must survive in the watcher list untouched
//	  for a future call to find.
//
// All four clauses pick literal 1 as their first watch (chooseWatch
// scans each clause left to right at construction, and 1 is each
// clause's first literal), so watchersFor(+1) starts as [0, 1, 2, 3]
// in exactly this order.
func TestPropagateWatcherListCompaction(t *testing.T) {
	problem := &cnf.Problem{NumVars: 7, Clauses: []cnf.Clause{
		{1, 6, 7}, // D
		{1, 4},    // B
		{1, 2},    // A
		{1, 3},    // C
	}}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	if got := *s.watchersFor(cnf.Literal(1)); !slices.Equal(got, []int{0, 1, 2, 3}) {
		t.Fatalf("watchersFor(+1) before propagate = %v, want [0 1 2 3] (test setup assumption violated)", got)
	}

	// Pure test setup: variable 2 is false, with no trail entry and no
	// propagation triggered by it -- isolating this test to exactly
	// the one propagate() call under test, over variable 1's own
	// assignment.
	s.x[2] = assign.False

	s.assignLiteral(cnf.Literal(-1), 0, noReason) // variable 1 := false
	conflict := s.propagate()

	if conflict != 2 {
		t.Fatalf("propagate() returned conflict %d, want 2 (clause A, {1 2})", conflict)
	}

	wantWatchersOf1 := []int{1, 2, 3} // B, A, C -- D moved away
	if got := *s.watchersFor(cnf.Literal(1)); !slices.Equal(got, wantWatchersOf1) {
		t.Errorf("watchersFor(+1) after propagate = %v, want %v", got, wantWatchersOf1)
	}
	wantWatchersOf7 := []int{0} // D, moved in
	if got := *s.watchersFor(cnf.Literal(7)); !slices.Equal(got, wantWatchersOf7) {
		t.Errorf("watchersFor(+7) after propagate = %v, want %v (D should have moved here)", got, wantWatchersOf7)
	}
	if s.watch[0] != [2]cnf.Literal{7, 6} && s.watch[0] != [2]cnf.Literal{6, 7} {
		t.Errorf("clause 0's (D's) watch = %v, want {6 7} in either order", s.watch[0])
	}
	if s.x[4] != assign.True {
		t.Errorf("variable 4 = %v, want assign.True (forced by clause B, {1 4})", s.x[4])
	}
	if s.x[3] != assign.Unassigned {
		t.Errorf("variable 3 = %v, want assign.Unassigned (clause C, {1 3}, must never have been scanned this call)", s.x[3])
	}
}

// TestAddLearnedClauseUpdatesWatcherLists verifies that a freshly
// learned clause's two initial watches are recorded in
// watchersPositive/watchersNegative, not just in watch itself --
// otherwise a future propagate() would never find this clause at all.
func TestAddLearnedClauseUpdatesWatcherLists(t *testing.T) {
	problem := &cnf.Problem{NumVars: 4, Clauses: []cnf.Clause{{1, 2, 3, 4}}}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	s.level[3] = 2
	s.level[4] = 5 // higher level than 3, so addLearnedClause's own "watch the highest-level other literal" tiebreak picks literal 4 (best)

	learned := cnf.Clause{cnf.Literal(-1), cnf.Literal(3), cnf.Literal(4)}
	idx := s.addLearnedClause(learned, 2)
	if idx == noReason {
		t.Fatal("addLearnedClause returned noReason for a length-3 clause")
	}

	if !slices.Contains(*s.watchersFor(cnf.Literal(-1)), idx) {
		t.Errorf("watchersFor(-1) = %v, want it to contain the new clause %d", *s.watchersFor(cnf.Literal(-1)), idx)
	}
	if !slices.Contains(*s.watchersFor(cnf.Literal(4)), idx) {
		t.Errorf("watchersFor(4) = %v, want it to contain the new clause %d", *s.watchersFor(cnf.Literal(4)), idx)
	}
	if s.watch[idx] != [2]cnf.Literal{cnf.Literal(-1), cnf.Literal(4)} {
		t.Errorf("watch[%d] = %v, want {-1 4}", idx, s.watch[idx])
	}
}

// TestReduceClauseDatabaseRebuildsWatcherLists verifies that after a
// reduction pass deletes and renumbers clauses, watchersPositive/
// watchersNegative are rebuilt consistently with the new watch array
// -- every remaining clause's current watches must appear in the
// corresponding (renumbered) watcher lists, and nothing should
// reference a clause index that no longer exists.
func TestReduceClauseDatabaseRebuildsWatcherLists(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2, Clauses: []cnf.Clause{{1, 2}}}
	s, ok := newSolver(problem, nil, SelectVarWeighted, RestartNone, params.Default().CDCL, PhaseSaving)
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	// Learn enough distinct, unlocked, non-glue (LBD > threshold)
	// clauses that reduceClauseDatabase actually has something
	// eligible to delete.
	for i := 0; i < 6; i++ {
		s.addLearnedClause(cnf.Clause{cnf.Literal(1), cnf.Literal(2)}, s.params.GlueClauseLBDThreshold+1)
	}
	s.reduceClauseDatabase()

	for c, w := range s.watch {
		for _, lit := range w {
			if !slices.Contains(*s.watchersFor(lit), c) {
				t.Errorf("after reduceClauseDatabase: clause %d watches %v but is missing from watchersFor(%v) = %v", c, lit, lit, *s.watchersFor(lit))
			}
		}
	}
	for _, lit := range []cnf.Literal{1, -1, 2, -2} {
		for _, c := range *s.watchersFor(lit) {
			if c < 0 || c >= len(s.clauses) {
				t.Errorf("watchersFor(%v) contains out-of-range clause index %d (len(s.clauses) = %d)", lit, c, len(s.clauses))
			}
		}
	}
}
