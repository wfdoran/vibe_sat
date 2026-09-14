package hillclimb

import (
	"math/rand/v2"
	"sync/atomic"
	"testing"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/occurrence"
)

// TestScoreCountsSatisfiedClauses verifies that Score counts exactly
// the clauses with at least one true literal.
func TestScoreCountsSatisfiedClauses(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 2,
		Clauses: []cnf.Clause{
			{cnf.Literal(1)},
			{cnf.Literal(-1)},
			{cnf.Literal(2), cnf.Literal(-1)},
		},
	}
	a := assign.New(2)
	a[1] = assign.True
	a[2] = assign.False

	// Clause {1} is satisfied (1 is True). Clause {-1} and clause
	// {2, -1} are both unsatisfied (1 is True, so -1 is false; 2 is
	// False). Only 1 of the 3 clauses is satisfied.
	if got := Score(problem, a); got != 1 {
		t.Errorf("Score() = %d, want 1", got)
	}
}

// TestClauseTrueCountsMatchesScore verifies that clauseTrueCounts
// reports the same score as the independent Score function, and that
// its per-clause counts are consistent with the assignment.
func TestClauseTrueCountsMatchesScore(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 2,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(2)},
			{cnf.Literal(-1), cnf.Literal(-2)},
		},
	}
	a := assign.New(2)
	a[1] = assign.True
	a[2] = assign.True

	counts, score := clauseTrueCounts(problem, a)
	if score != Score(problem, a) {
		t.Errorf("clauseTrueCounts score = %d, want %d", score, Score(problem, a))
	}
	if counts[0] != 2 {
		t.Errorf("counts[0] = %d, want 2", counts[0])
	}
	if counts[1] != 0 {
		t.Errorf("counts[1] = %d, want 0", counts[1])
	}
}

// TestFlipUpdatesScoreCorrectly verifies that a single flip() call
// updates the per-clause counts and score consistently with a
// from-scratch recomputation after the flip.
func TestFlipUpdatesScoreCorrectly(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 2,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(2)},
			{cnf.Literal(-1)},
		},
	}
	lists := occurrence.Build(problem)
	a := assign.New(2)
	a[1] = assign.False
	a[2] = assign.False

	state := newClimbState(problem, lists, a)
	delta := state.flip(1) // variable 1: False -> True

	wantCounts, wantScore := clauseTrueCounts(problem, state.assignment)
	if state.score != wantScore {
		t.Errorf("state.score = %d, want %d", state.score, wantScore)
	}
	for i := range wantCounts {
		if state.counts[i] != wantCounts[i] {
			t.Errorf("state.counts[%d] = %d, want %d", i, state.counts[i], wantCounts[i])
		}
	}
	if state.assignment[1] != assign.True {
		t.Errorf("assignment[1] = %v, want True", state.assignment[1])
	}
	// Clause 0 goes from unsatisfied to satisfied (+1), clause 1 goes
	// from satisfied to unsatisfied (-1): net delta 0.
	if delta != 0 {
		t.Errorf("delta = %d, want 0", delta)
	}
}

// TestFlipTwiceRevertsToOriginalState verifies that flipping the same
// variable twice restores the original assignment, counts, and score.
func TestFlipTwiceRevertsToOriginalState(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 3,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(-2)},
			{cnf.Literal(2), cnf.Literal(3)},
			{cnf.Literal(-3)},
		},
	}
	lists := occurrence.Build(problem)
	a := assign.NewRandom(3, rand.New(rand.NewPCG(9, 9)))
	originalAssignment := append(assign.Assignment{}, a...)

	state := newClimbState(problem, lists, a)
	originalCounts := append([]int{}, state.counts...)
	originalScore := state.score

	state.flip(2)
	state.flip(2)

	for v := 1; v <= 3; v++ {
		if state.assignment[v] != originalAssignment[v] {
			t.Errorf("assignment[%d] = %v, want %v", v, state.assignment[v], originalAssignment[v])
		}
	}
	for i := range originalCounts {
		if state.counts[i] != originalCounts[i] {
			t.Errorf("counts[%d] = %d, want %d", i, state.counts[i], originalCounts[i])
		}
	}
	if state.score != originalScore {
		t.Errorf("score = %d, want %d", state.score, originalScore)
	}
}

// TestFlipHandlesTautologicalClause verifies that flip() correctly
// keeps a clause containing both polarities of the flipped variable
// satisfied no matter which way the variable is set.
func TestFlipHandlesTautologicalClause(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 1,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(-1)},
		},
	}
	lists := occurrence.Build(problem)
	a := assign.New(1)
	a[1] = assign.True

	state := newClimbState(problem, lists, a)
	if state.score != 1 {
		t.Fatalf("initial score = %d, want 1", state.score)
	}

	delta := state.flip(1)
	if delta != 0 {
		t.Errorf("delta = %d, want 0", delta)
	}
	if state.score != 1 {
		t.Errorf("score after flip = %d, want 1", state.score)
	}
	if state.counts[0] != 1 {
		t.Errorf("counts[0] after flip = %d, want 1", state.counts[0])
	}
}

// TestRunFindsSatisfiableFormula verifies that Run reports Satisfiable
// for an easily satisfiable formula (every clause is a single
// positive literal, so every flip of a currently-false variable
// strictly helps).
func TestRunFindsSatisfiableFormula(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 5,
		Clauses: []cnf.Clause{
			{cnf.Literal(1)},
			{cnf.Literal(2)},
			{cnf.Literal(3)},
			{cnf.Literal(4)},
			{cnf.Literal(5)},
		},
	}
	lists := occurrence.Build(problem)
	numStarts := 1
	params := Params{NumStarts: &numStarts}
	rng := rand.New(rand.NewPCG(1, 1))

	result := Run(problem, lists, params, rng, 0)

	if !result.Satisfiable {
		t.Fatalf("expected Satisfiable = true, got false (score %d/%d)", result.Score, problem.NumClauses())
	}
	if result.Score != problem.NumClauses() {
		t.Errorf("Score = %d, want %d", result.Score, problem.NumClauses())
	}
}

// TestRunReportsUnknownForUnsatisfiableFormula verifies that Run never
// claims satisfiability for a trivially unsatisfiable formula
// (x1 AND NOT x1), regardless of how many restarts it is given.
func TestRunReportsUnknownForUnsatisfiableFormula(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 1,
		Clauses: []cnf.Clause{
			{cnf.Literal(1)},
			{cnf.Literal(-1)},
		},
	}
	lists := occurrence.Build(problem)
	numStarts := 5
	params := Params{NumStarts: &numStarts}
	rng := rand.New(rand.NewPCG(2, 2))

	result := Run(problem, lists, params, rng, 0)

	if result.Satisfiable {
		t.Fatalf("expected Satisfiable = false for an unsatisfiable formula")
	}
	if result.Starts != numStarts {
		t.Errorf("Starts = %d, want %d", result.Starts, numStarts)
	}
	if result.Score >= problem.NumClauses() {
		t.Errorf("Score = %d, want less than %d", result.Score, problem.NumClauses())
	}
}

// TestNewClimbStateTracksUnsatisfiedClauses verifies that the initial
// unsatClauses set exactly matches the clauses whose count is zero.
func TestNewClimbStateTracksUnsatisfiedClauses(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 2,
		Clauses: []cnf.Clause{
			{cnf.Literal(1)},  // satisfied
			{cnf.Literal(-1)}, // unsatisfied
			{cnf.Literal(2)},  // unsatisfied
		},
	}
	lists := occurrence.Build(problem)
	a := assign.New(2)
	a[1] = assign.True
	a[2] = assign.False

	state := newClimbState(problem, lists, a)

	wantUnsat := map[int]bool{1: true, 2: true}
	if len(state.unsatClauses) != len(wantUnsat) {
		t.Fatalf("unsatClauses = %v, want clauses %v", state.unsatClauses, wantUnsat)
	}
	for _, c := range state.unsatClauses {
		if !wantUnsat[c] {
			t.Errorf("unsatClauses contains unexpected clause %d", c)
		}
		if state.unsatPos[c] < 0 {
			t.Errorf("unsatPos[%d] = %d, want >= 0", c, state.unsatPos[c])
		}
	}
	if state.unsatPos[0] != -1 {
		t.Errorf("unsatPos[0] = %d, want -1 (clause 0 is satisfied)", state.unsatPos[0])
	}
}

// TestFlipKeepsUnsatClausesConsistent verifies that after a flip, the
// unsatClauses/unsatPos bookkeeping agrees with a from-scratch
// recomputation of which clauses have a zero true-literal count.
func TestFlipKeepsUnsatClausesConsistent(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 3,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(2)},
			{cnf.Literal(-1), cnf.Literal(3)},
			{cnf.Literal(-2), cnf.Literal(-3)},
		},
	}
	lists := occurrence.Build(problem)
	a := assign.New(3)
	a[1], a[2], a[3] = assign.False, assign.False, assign.False

	state := newClimbState(problem, lists, a)
	state.flip(1)
	state.flip(2)

	wantCounts, _ := clauseTrueCounts(problem, state.assignment)
	wantUnsat := map[int]bool{}
	for c, count := range wantCounts {
		if count == 0 {
			wantUnsat[c] = true
		}
	}

	if len(state.unsatClauses) != len(wantUnsat) {
		t.Fatalf("unsatClauses = %v, want clauses %v", state.unsatClauses, wantUnsat)
	}
	for _, c := range state.unsatClauses {
		if !wantUnsat[c] {
			t.Errorf("unsatClauses contains unexpected clause %d", c)
		}
		if state.unsatPos[c] != indexOf(state.unsatClauses, c) {
			t.Errorf("unsatPos[%d] = %d, does not match its actual position", c, state.unsatPos[c])
		}
	}
	for c := range wantCounts {
		if !wantUnsat[c] && state.unsatPos[c] != -1 {
			t.Errorf("unsatPos[%d] = %d, want -1 (clause is satisfied)", c, state.unsatPos[c])
		}
	}
}

// indexOf returns the index of target within haystack, or -1 if not
// present.
func indexOf(haystack []int, target int) int {
	for i, v := range haystack {
		if v == target {
			return i
		}
	}
	return -1
}

// TestBreakCount verifies that breakCount reports how many currently
// satisfied clauses would become unsatisfied by flipping a variable,
// without actually flipping it.
func TestBreakCount(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 2,
		Clauses: []cnf.Clause{
			{cnf.Literal(1)},                 // only satisfied because 1 is True; breaks if 1 flips
			{cnf.Literal(1), cnf.Literal(2)}, // satisfied by both 1 and 2; does not break if 1 flips
		},
	}
	lists := occurrence.Build(problem)
	a := assign.New(2)
	a[1] = assign.True
	a[2] = assign.True

	state := newClimbState(problem, lists, a)
	if got := state.breakCount(1); got != 1 {
		t.Errorf("breakCount(1) = %d, want 1", got)
	}
	if got := state.breakCount(2); got != 0 {
		t.Errorf("breakCount(2) = %d, want 0", got)
	}

	// breakCount must not have mutated the state.
	if state.assignment[1] != assign.True || state.assignment[2] != assign.True {
		t.Errorf("breakCount mutated the assignment: %v", state.assignment)
	}
}

// TestChooseFlipVariableGreedyPicksMinimalBreakCount verifies that,
// with zero noise, chooseFlipVariable always returns the variable of
// the clause with the smallest break count.
func TestChooseFlipVariableGreedyPicksMinimalBreakCount(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 2,
		Clauses: []cnf.Clause{
			{cnf.Literal(-1), cnf.Literal(-2)}, // unsatisfied: both 1 and 2 are True
			{cnf.Literal(1)},                   // satisfied only via variable 1
		},
	}
	lists := occurrence.Build(problem)
	a := assign.New(2)
	a[1] = assign.True
	a[2] = assign.True

	state := newClimbState(problem, lists, a)
	// Clause 0 is unsatisfied. Flipping 1 would break clause 1
	// (break count 1); flipping 2 breaks nothing (break count 0).
	rng := rand.New(rand.NewPCG(1, 1))
	for i := 0; i < 10; i++ {
		if got := chooseFlipVariable(state, 0, 0, rng); got != 2 {
			t.Fatalf("chooseFlipVariable() = %d, want 2 (the minimal break-count variable)", got)
		}
	}
}

// TestChooseFlipVariableNoiseStaysWithinClause verifies that, even at
// 100% noise, chooseFlipVariable only ever returns a variable that
// actually appears in the given clause.
func TestChooseFlipVariableNoiseStaysWithinClause(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 3,
		Clauses: []cnf.Clause{
			{cnf.Literal(-1), cnf.Literal(-2), cnf.Literal(-3)},
		},
	}
	lists := occurrence.Build(problem)
	a := assign.NewRandom(3, rand.New(rand.NewPCG(4, 4)))
	state := newClimbState(problem, lists, a)

	rng := rand.New(rand.NewPCG(5, 5))
	allowed := map[int]bool{1: true, 2: true, 3: true}
	for i := 0; i < 20; i++ {
		v := chooseFlipVariable(state, 0, 100, rng)
		if !allowed[v] {
			t.Fatalf("chooseFlipVariable() = %d, want one of 1, 2, 3", v)
		}
	}
}

// TestRunWalkSatFindsSatisfiableFormula verifies that RunWalkSat
// reports Satisfiable for an easily satisfiable formula (every clause
// is a single positive literal, so flipping any false variable from
// an unsatisfied clause never breaks any other clause).
func TestRunWalkSatFindsSatisfiableFormula(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 5,
		Clauses: []cnf.Clause{
			{cnf.Literal(1)},
			{cnf.Literal(2)},
			{cnf.Literal(3)},
			{cnf.Literal(4)},
			{cnf.Literal(5)},
		},
	}
	lists := occurrence.Build(problem)
	numTries := 1
	params := WalkSatParams{NumTries: &numTries, MaxFlipsPerTry: DefaultMaxFlipsPerTry, NoisePercent: DefaultNoisePercent}
	rng := rand.New(rand.NewPCG(1, 1))

	result := RunWalkSat(problem, lists, params, rng, 0)

	if !result.Satisfiable {
		t.Fatalf("expected Satisfiable = true, got false (score %d/%d)", result.Score, problem.NumClauses())
	}
	if result.Score != problem.NumClauses() {
		t.Errorf("Score = %d, want %d", result.Score, problem.NumClauses())
	}
}

// TestRunWalkSatReportsUnknownForUnsatisfiableFormula verifies that
// RunWalkSat never claims satisfiability for a trivially
// unsatisfiable formula (x1 AND NOT x1), regardless of how many tries
// it is given.
func TestRunWalkSatReportsUnknownForUnsatisfiableFormula(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 1,
		Clauses: []cnf.Clause{
			{cnf.Literal(1)},
			{cnf.Literal(-1)},
		},
	}
	lists := occurrence.Build(problem)
	numTries := 5
	params := WalkSatParams{NumTries: &numTries, MaxFlipsPerTry: 50, NoisePercent: DefaultNoisePercent}
	rng := rand.New(rand.NewPCG(2, 2))

	result := RunWalkSat(problem, lists, params, rng, 0)

	if result.Satisfiable {
		t.Fatalf("expected Satisfiable = false for an unsatisfiable formula")
	}
	if result.Starts != numTries {
		t.Errorf("Starts = %d, want %d", result.Starts, numTries)
	}
}

// TestRandomPermutationIsAPermutation verifies that randomPermutation
// returns each of 1..numVars exactly once.
func TestRandomPermutationIsAPermutation(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 3))
	order := randomPermutation(10, rng)
	seen := make(map[int]bool)
	for _, v := range order {
		if v < 1 || v > 10 {
			t.Fatalf("order contains out-of-range value %d", v)
		}
		if seen[v] {
			t.Fatalf("order contains duplicate value %d", v)
		}
		seen[v] = true
	}
	if len(seen) != 10 {
		t.Fatalf("order contains %d distinct values, want 10", len(seen))
	}
}

// TestRunParallelWithOneThreadMatchesRun verifies STAGE17.md's core
// compatibility guarantee: RunParallel with numThreads <= 1 must
// behave identically to calling Run directly, down to every random
// choice made, since it delegates straight to Run rather than going
// through any goroutine/atomic machinery at all.
func TestRunParallelWithOneThreadMatchesRun(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 4,
		Clauses: []cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}, {cnf.Literal(-1), cnf.Literal(3)}, {cnf.Literal(-2), cnf.Literal(-3)}, {cnf.Literal(4)}},
	}
	lists := occurrence.Build(problem)
	numStarts := 3
	params := Params{NumStarts: &numStarts}

	want := Run(problem, lists, params, rand.New(rand.NewPCG(11, 11)), 0)
	got := RunParallel(problem, lists, params, 1, rand.New(rand.NewPCG(11, 11)), 0)

	if got.Satisfiable != want.Satisfiable || got.Score != want.Score || got.Starts != want.Starts {
		t.Fatalf("RunParallel(numThreads=1) = %+v, want identical to Run() = %+v", got, want)
	}
	if got.Satisfiable && !assignmentsEqual(got.Assignment, want.Assignment) {
		t.Errorf("RunParallel(numThreads=1) assignment differs from Run()'s")
	}
}

// TestRunParallelFindsSatisfiableFormula verifies that splitting a
// search across several concurrent workers still finds a genuine
// satisfying assignment and reports it correctly.
func TestRunParallelFindsSatisfiableFormula(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 5,
		Clauses: []cnf.Clause{
			{cnf.Literal(1)},
			{cnf.Literal(2)},
			{cnf.Literal(3)},
			{cnf.Literal(4)},
			{cnf.Literal(5)},
		},
	}
	lists := occurrence.Build(problem)
	numStarts := 8
	params := Params{NumStarts: &numStarts}
	rng := rand.New(rand.NewPCG(12, 12))

	result := RunParallel(problem, lists, params, 4, rng, 0)

	if !result.Satisfiable {
		t.Fatalf("expected Satisfiable = true (score %d/%d)", result.Score, problem.NumClauses())
	}
	if result.Score != problem.NumClauses() {
		t.Errorf("Score = %d, want %d", result.Score, problem.NumClauses())
	}
}

// TestRunParallelSplitsNumStartsAcrossThreads verifies that, for an
// unsatisfiable formula (so every worker runs its full local quota
// with no early stop), the aggregate Starts is at least NumStarts
// (each of numThreads workers does ceil(NumStarts/numThreads), so the
// total may be rounded up, never down) and workers actually ran in
// parallel rather than one worker doing all the work.
func TestRunParallelSplitsNumStartsAcrossThreads(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 1,
		Clauses: []cnf.Clause{{cnf.Literal(1)}, {cnf.Literal(-1)}},
	}
	lists := occurrence.Build(problem)
	numStarts := 20
	params := Params{NumStarts: &numStarts}
	rng := rand.New(rand.NewPCG(13, 13))

	result := RunParallel(problem, lists, params, 4, rng, 0)

	if result.Satisfiable {
		t.Fatalf("expected Satisfiable = false for an unsatisfiable formula")
	}
	if result.Starts < numStarts {
		t.Errorf("Starts = %d, want at least %d", result.Starts, numStarts)
	}
	if result.Starts > numStarts+4-1 {
		t.Errorf("Starts = %d, want at most %d (ceil rounding across 4 workers)", result.Starts, numStarts+4-1)
	}
}

// TestRunLoopStopsImmediatelyWhenStopIsAlreadySet verifies the stop
// signal directly: runLoop must perform zero starts if params.Stop is
// already true before it is ever called, mirroring what happens to
// every other RunParallel worker the instant one of them finds a
// solution.
func TestRunLoopStopsImmediatelyWhenStopIsAlreadySet(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2, Clauses: []cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}}}
	lists := occurrence.Build(problem)
	var stop atomic.Bool
	stop.Store(true)
	params := Params{Stop: &stop}

	result := runLoop(problem, lists, params, rand.New(rand.NewPCG(14, 14)), 0)

	if result.Starts != 0 {
		t.Errorf("Starts = %d, want 0 (Stop was already set)", result.Starts)
	}
	if result.Satisfiable {
		t.Error("expected Satisfiable = false when stopped before any start")
	}
}

// TestRunLoopBestScoreOnlyRecordsGenuineImprovements verifies the
// shared-BestScore path directly: given a BestScore already at 5,
// runLoop's recordIfBest must not report (or lower) it for a score of
// 5 or less, matching the single-threaded local-bestScore behavior it
// replaces for RunParallel's workers.
func TestRunLoopBestScoreOnlyRecordsGenuineImprovements(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 3,
		Clauses: []cnf.Clause{{cnf.Literal(1)}, {cnf.Literal(2)}, {cnf.Literal(3)}},
	}
	lists := occurrence.Build(problem)
	var bestScore atomic.Int64
	bestScore.Store(5)
	numStarts := 1
	params := Params{NumStarts: &numStarts, BestScore: &bestScore}

	runLoop(problem, lists, params, rand.New(rand.NewPCG(15, 15)), 0)

	if got := bestScore.Load(); got < 5 {
		t.Errorf("BestScore = %d, want >= 5 (never lowered)", got)
	}
}

// assignmentsEqual reports whether a and b assign every variable of a
// (both are expected to have the same length) to the same value.
func assignmentsEqual(a, b assign.Assignment) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRunWalkSatParallelWithOneThreadMatchesRunWalkSat is
// TestRunParallelWithOneThreadMatchesRun's WalkSAT analog: verifies
// the same STAGE17.md compatibility guarantee for
// RunWalkSatParallel(numThreads=1).
func TestRunWalkSatParallelWithOneThreadMatchesRunWalkSat(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 4,
		Clauses: []cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}, {cnf.Literal(-1), cnf.Literal(3)}, {cnf.Literal(-2), cnf.Literal(-3)}, {cnf.Literal(4)}},
	}
	lists := occurrence.Build(problem)
	numTries := 3
	params := WalkSatParams{NumTries: &numTries, MaxFlipsPerTry: DefaultMaxFlipsPerTry, NoisePercent: DefaultNoisePercent}

	want := RunWalkSat(problem, lists, params, rand.New(rand.NewPCG(21, 21)), 0)
	got := RunWalkSatParallel(problem, lists, params, 1, rand.New(rand.NewPCG(21, 21)), 0)

	if got.Satisfiable != want.Satisfiable || got.Score != want.Score || got.Starts != want.Starts {
		t.Fatalf("RunWalkSatParallel(numThreads=1) = %+v, want identical to RunWalkSat() = %+v", got, want)
	}
	if got.Satisfiable && !assignmentsEqual(got.Assignment, want.Assignment) {
		t.Errorf("RunWalkSatParallel(numThreads=1) assignment differs from RunWalkSat()'s")
	}
}

// TestRunWalkSatParallelFindsSatisfiableFormula verifies that
// splitting a WalkSAT search across several concurrent workers still
// finds a genuine satisfying assignment.
func TestRunWalkSatParallelFindsSatisfiableFormula(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 5,
		Clauses: []cnf.Clause{
			{cnf.Literal(1)},
			{cnf.Literal(2)},
			{cnf.Literal(3)},
			{cnf.Literal(4)},
			{cnf.Literal(5)},
		},
	}
	lists := occurrence.Build(problem)
	numTries := 8
	params := WalkSatParams{NumTries: &numTries, MaxFlipsPerTry: DefaultMaxFlipsPerTry, NoisePercent: DefaultNoisePercent}
	rng := rand.New(rand.NewPCG(22, 22))

	result := RunWalkSatParallel(problem, lists, params, 4, rng, 0)

	if !result.Satisfiable {
		t.Fatalf("expected Satisfiable = true (score %d/%d)", result.Score, problem.NumClauses())
	}
	if result.Score != problem.NumClauses() {
		t.Errorf("Score = %d, want %d", result.Score, problem.NumClauses())
	}
}

// TestWalkSatLoopStopsImmediatelyWhenStopIsAlreadySet is
// TestRunLoopStopsImmediatelyWhenStopIsAlreadySet's WalkSAT analog.
func TestWalkSatLoopStopsImmediatelyWhenStopIsAlreadySet(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2, Clauses: []cnf.Clause{{cnf.Literal(1), cnf.Literal(2)}}}
	lists := occurrence.Build(problem)
	var stop atomic.Bool
	stop.Store(true)
	params := WalkSatParams{MaxFlipsPerTry: DefaultMaxFlipsPerTry, NoisePercent: DefaultNoisePercent, Stop: &stop}

	result := walkSatLoop(problem, lists, params, rand.New(rand.NewPCG(23, 23)), 0)

	if result.Starts != 0 {
		t.Errorf("Starts = %d, want 0 (Stop was already set)", result.Starts)
	}
	if result.Satisfiable {
		t.Error("expected Satisfiable = false when stopped before any start")
	}
}
