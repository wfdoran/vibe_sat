// Package dfs implements the depth-first search SAT solving algorithm
// used by vibe_sat's "dfs" algorithm: a complete DPLL-style search
// using boolean constraint propagation (BCP, see watch.go for
// STAGE9.md's watched-literal implementation) and a choice of
// variable-selection heuristics (see SelectVarVariant). Unlike the
// hill-climb-family algorithms (internal/hillclimb), this search is
// complete: if it exhausts its search space without finding a
// satisfying assignment, the problem is proven UNSAT, not merely "not
// found yet".
package dfs

import (
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/occurrence"
	"vibe_sat/internal/preprocess"
)

// Status is the outcome of a single call to BCP.
type Status int

const (
	// OK means propagation finished without contradiction, but the
	// assignment is still partial.
	OK Status = iota
	// Contra means a clause can no longer be satisfied under this
	// assignment; it cannot be extended to a solution.
	Contra
	// Done means the assignment is now complete and (by construction)
	// satisfies every clause.
	Done
)

// SelectVarVariant identifies which SelectVar heuristic Run should
// use to pick the next branching variable.
type SelectVarVariant int

const (
	// SelectVarWeighted is the default heuristic from STAGE5.md: for
	// every not-yet-satisfied clause, every unassigned variable in it
	// earns 0.7^(n-2) (n = that clause's unassigned literal count),
	// and the highest-scoring variable is picked. It costs time
	// proportional to the total size of the formula on every single
	// node (it rescans every clause), in exchange for making a more
	// informed choice that tends to keep the search tree small.
	SelectVarWeighted SelectVarVariant = 0
	// SelectVarFast is the cheaper alternative from STAGE6.md: it
	// picks the lowest-numbered still-unassigned variable, looking at
	// no clause contents at all. This is the "static/lexicographic
	// ordering" branching rule discussed in the SAT branching
	// heuristic literature (e.g. J. Marques-Silva, "The Impact of
	// Branching Heuristics in Propositional Satisfiability
	// Algorithms," 1999) as the cheap baseline that smarter dynamic
	// heuristics are compared against: it costs at most O(NumVars)
	// per node with no clause scanning, but ignores problem structure
	// entirely, which typically grows the search tree substantially.
	SelectVarFast SelectVarVariant = 1
)

// timeCheckInterval controls how often the time limit is checked
// against the number of search nodes explored, per STAGE5.md's
// suggestion ("Maybe only when num_nodes & 0xfff == 0"): checking the
// clock on every single node would add needless overhead, since nodes
// are cheap and the clock only needs to be checked often enough to
// respond to a time limit reasonably promptly.
const timeCheckInterval = 0xfff

// Result describes the outcome of a depth-first search.
type Result struct {
	Satisfiable bool              // whether a satisfying assignment was found
	Assignment  assign.Assignment // the satisfying assignment; only meaningful if Satisfiable
	NumNodes    int               // number of search-tree nodes explored
	TimedOut    bool              // true if the search was abandoned due to the time limit, rather than exhausting the search space
}

// searchNode is a self-contained snapshot of one point in the search
// tree: a partial assignment together with the watched-literal state
// describing it. Before STAGE32.md, Run kept an explicit stack of
// these -- one full clone per branch -- matching STAGE5.md's original
// design. REPORT29.md found that design unsafe at scale (a single
// stack entry's watch-state clone alone runs 100+ MB on a
// 17.7-million-clause file, and a search stack routinely holds many
// such entries at once); STAGE32.md/REPORT32.md replaces it with
// searchState's single, incrementally-backtracked assignment and
// watch state below, matching the persistent-trail design cdcl has
// used since Stage 11 (see cdcl's backtrackTo doc comment for the
// same "a watch stays valid across backtracking" argument this reuses).
//
// searchNode itself still exists, and is still exactly this expensive
// to create, but is now used only where a genuinely independent,
// self-contained snapshot is actually needed: RunParallel's initial
// per-worker seeds (bfsSeed), and a worker's occasional, deliberate
// materialization of one snapshot to offer another worker via its
// deque (see parallel.go's shedWork) -- both rare compared to the
// total number of nodes explored, unlike Stage 5-31's one-per-branch
// cost.
type searchNode struct {
	assignment assign.Assignment
	watch      *watchState
}

// frame is one entry of searchState's explicit backtracking stack: it
// records enough to retry variable with its other value, or abandon
// it entirely, without ever needing a stored copy of the assignment
// or watch state it was created under -- backtracking instead undoes
// exactly the trail entries made since mark (see searchState.undoTo).
type frame struct {
	variable int
	mark     int       // len(trail) immediately before variable was first assigned by this frame
	next     frameNext // which value (if any) this frame has not yet tried
}

// frameNext identifies which of False/True a frame has left to try,
// or that neither is left (frameExhausted: pop this frame).
type frameNext uint8

const (
	frameTryFalse frameNext = iota
	frameTryTrue
	frameExhausted
)

// stepOutcome is step's own report of why it stopped, distinct from
// Status (BCP's per-attempt report) since step runs a whole sequence
// of attempts, not just one.
type stepOutcome int

const (
	stepPaused stepOutcome = iota // checkPause returned true; frames may still hold pending work
	stepSAT                       // x is now a complete satisfying assignment
	stepUNSAT                     // frames is empty: this subtree is fully exhausted
)

// searchState is the mutable engine behind one sequential depth-first
// exploration (STAGE32.md/REPORT32.md): a single assignment and a
// single watchState, both shared across the entire exploration and
// mutated in place, plus an explicit trail recording every variable
// assigned (by either a branch decision or BCP's own forced
// propagation) since the search began, and an explicit frame stack
// mirroring what a recursive "try False, try True" call stack would
// hold. Backtracking undoes trail entries back to a frame's mark
// instead of restoring a stored clone -- there is only ever one
// assignment and one watch state alive for the whole exploration, not
// one per branch.
type searchState struct {
	clauses []cnf.Clause
	lists   *occurrence.Lists
	x       assign.Assignment
	ws      *watchState
	trail   []int
	frames  []frame
}

// newSearchState builds a searchState ready to explore beneath root:
// it takes ownership of root's assignment and watch state (the caller
// must not read or mutate either afterward), matching the old
// per-branch clone's ownership convention except there is now exactly
// one live copy for the whole exploration, not one per node.
func newSearchState(clauses []cnf.Clause, lists *occurrence.Lists, root searchNode) *searchState {
	return &searchState{
		clauses: clauses,
		lists:   lists,
		x:       root.assignment,
		ws:      root.watch,
		trail:   make([]int, 0, 64),
	}
}

// startSearch builds a searchState for root (via newSearchState) and
// selects and pushes its first frame, ready for step to run -- every
// caller must already know root.assignment is not yet complete (see
// allAssigned), exactly as every caller did before STAGE32.md too.
// Both Run and dfsWorker use this, the latter both for its initial
// seed and for every subsequent node it pops or steals after
// exhausting one search.
func startSearch(clauses []cnf.Clause, lists *occurrence.Lists, workingProblem *cnf.Problem, root searchNode, variant SelectVarVariant, rng *rand.Rand) *searchState {
	state := newSearchState(clauses, lists, root)
	var v int
	if variant == SelectVarFast {
		v = SelectVarFastPick(workingProblem, state.x)
	} else {
		v = SelectVar(workingProblem, state.x, rng)
	}
	state.frames = append(state.frames, frame{variable: v, mark: len(state.trail), next: frameTryFalse})
	return state
}

// undoTo truncates s.trail back to mark, resetting every variable
// assigned since then back to assign.Unassigned. A no-op if the trail
// is already at (or, defensively, before) mark.
func (s *searchState) undoTo(mark int) {
	for i := len(s.trail) - 1; i >= mark; i-- {
		s.x[s.trail[i]] = assign.Unassigned
	}
	if mark < len(s.trail) {
		s.trail = s.trail[:mark]
	}
}

// step runs the search forward from s's current frames until one of
// three things happens: a satisfying assignment is found (stepSAT, s.x
// is the assignment); every frame is exhausted, proving this
// exploration's subtree unsatisfiable (stepUNSAT); or checkPause
// (which may be nil) reports true, pausing with frames still holding
// unfinished work for a later call to resume (stepPaused) -- used for
// the time-limit check and, in RunParallel's workers, to periodically
// consider shedding work for other workers to steal (see parallel.go).
// checkPause is passed this call's own running node count (not
// counting nodes from any earlier call), since a single call can run
// for a long time and the caller's own total is otherwise unavailable
// to it until step finally returns. numNodes counts how many new
// frames were pushed (branch points created) during this call,
// matching the pre-STAGE32.md convention of counting once per node
// expanded.
func (s *searchState) step(workingProblem *cnf.Problem, variant SelectVarVariant, rng *rand.Rand, checkPause func(numNodesThisCall int) bool) (outcome stepOutcome, numNodes int) {
	for len(s.frames) > 0 {
		if checkPause != nil && checkPause(numNodes) {
			return stepPaused, numNodes
		}

		top := &s.frames[len(s.frames)-1]
		if top.next == frameExhausted {
			s.undoTo(top.mark)
			s.frames = s.frames[:len(s.frames)-1]
			continue
		}

		s.undoTo(top.mark) // no-op except when returning here after a pushed child's subtree was fully exhausted
		value := assign.False
		if top.next == frameTryTrue {
			value = assign.True
		}
		s.x[top.variable] = value
		s.trail = append(s.trail, top.variable)
		top.next++

		switch BCP(s.clauses, s.lists, s.ws, s.x, top.variable, &s.trail) {
		case Contra:
			continue // top is unchanged; next iteration retries it (next value, or exhausted)
		case Done:
			return stepSAT, numNodes
		case OK:
			var i int
			if variant == SelectVarFast {
				i = SelectVarFastPick(workingProblem, s.x)
			} else {
				i = SelectVar(workingProblem, s.x, rng)
			}
			numNodes++
			s.frames = append(s.frames, frame{variable: i, mark: len(s.trail), next: frameTryFalse})
		}
	}
	return stepUNSAT, numNodes
}

// shedOutcome is shedFrame's own report of what happened.
type shedOutcome int

const (
	shedNone   shedOutcome = iota // no frame had an untried sibling to shed
	shedPushed                    // a snapshot was pushed onto deque (or the sibling was immediately Contra -- either way, handled)
	shedSAT                       // materializing the sibling and running BCP on it directly completed a solution
)

// shedFrame looks for the shallowest frame in s whose False branch has
// been committed to (frameTryTrue: already explored, or still being
// explored by a descendant frame further up the stack) but whose True
// branch has not, and hands that True branch off: it materializes an
// independent searchNode snapshot (an assignment clone covering only
// the ancestor variables locked in before that frame, per REPORT32.md's
// argument for why the *current* watch state remains a legal watch
// for any less-constrained ancestor assignment too) and pushes it onto
// deque for another worker to steal, exactly like the per-branch
// clones Stage 5 through Stage 31 made unconditionally for *every*
// branch -- this is the one place that cost still exists, but now only
// when a worker deliberately decides to make its own spare capacity
// available, not once per node.
//
// The shed-from frame's own next is set to frameExhausted regardless
// of outcome, since either way this worker no longer owns that
// branch: shedPushed means some worker (this one or a thief) now owns
// it independently; a Contra discovered while materializing it means
// it's already provably dead, so there is nothing left to shed at all,
// but this worker still must not retry it itself.
func (s *searchState) shedFrame(deque *deque) (outcome shedOutcome, satisfyingAssignment assign.Assignment) {
	for i := range s.frames {
		f := &s.frames[i]
		if f.next != frameTryTrue {
			continue // untouched (nothing committed yet) or already exhausted/shed
		}

		snapAssignment := assign.New(len(s.x) - 1)
		for _, v := range s.trail[:f.mark] {
			snapAssignment[v] = s.x[v]
		}
		snapAssignment[f.variable] = assign.True
		snapWatch := cloneWatchState(s.ws)

		f.next = frameExhausted
		switch BCP(s.clauses, s.lists, snapWatch, snapAssignment, f.variable, nil) {
		case Done:
			return shedSAT, snapAssignment
		case OK:
			deque.pushBottom(searchNode{assignment: snapAssignment, watch: snapWatch})
		}
		// Contra: the sibling is already provably dead; nothing to push,
		// but f.next is already set above, so this worker won't revisit it.
		return shedPushed, nil
	}
	return shedNone, nil
}

// bootstrap builds the root searchNode for problem: a defensive round
// of unit propagation (needed even though Stage 8's preprocessing
// already does this by default, since --no-preprocessing skips that)
// followed by initial watch state for whatever clauses survive it --
// the same two-step bootstrap Run always performed inline, now shared
// with RunParallel (see parallel.go), which needs the exact same root
// before branching into its BFS seeding phase. ok is false if this
// bootstrap alone already proves problem unsatisfiable.
func bootstrap(problem *cnf.Problem) (clauses []cnf.Clause, root searchNode, ok bool) {
	clauses = append([]cnf.Clause(nil), problem.Clauses...)
	rootAssignment := assign.New(problem.NumVars)
	if unsat, _ := preprocess.UnitPropagate(&clauses, rootAssignment); unsat {
		return clauses, searchNode{}, false
	}
	rootWatch, foundWatch := newWatchState(clauses, rootAssignment)
	if !foundWatch {
		// Defensive: UnitPropagate above should already rule this out,
		// since every surviving clause has at least one unassigned
		// literal (otherwise it would have been a unit clause caught
		// above, or a contradiction).
		return clauses, searchNode{}, false
	}
	return clauses, searchNode{assignment: rootAssignment, watch: rootWatch}, true
}

// allAssigned reports whether every variable of x currently has a
// value. Only ever meaningful for the root node bootstrap produces:
// every other node in the search (in Run's stack, or bfsSeed/
// dfsWorker's queues/deques in parallel.go) is only ever created by
// BCP explicitly reporting OK (not Done), which by construction means
// it still has at least one unassigned variable -- so it is only the
// bootstrap-propagated root itself that might, in the rare case where
// unit propagation alone already fully solves the formula (e.g. a
// formula made entirely of unit clauses), turn out to already be
// complete. Checking this once, right after bootstrap, avoids
// SelectVar/SelectVarFastPick ever being asked to choose from an
// assignment with nothing left unassigned, which they are not
// prepared for (SelectVar returns -1, which would panic the very
// next indexing operation).
func allAssigned(x assign.Assignment) bool {
	for _, value := range x[1:] {
		if value == assign.Unassigned {
			return false
		}
	}
	return true
}

// Run performs the depth-first search described in STAGE5.md: starting
// from the fully unassigned partial assignment, repeatedly pop a
// partial assignment from an explicit stack, pick a variable to
// branch on with SelectVar, and try setting it to each of False and
// True in turn, applying BCP after each attempt. A branch that leads
// to a contradiction is abandoned; a branch that completes the
// assignment means problem is satisfiable; a branch that is merely
// consistent but incomplete is pushed back onto the stack to be
// explored later. If the stack empties without ever completing an
// assignment, problem is proven unsatisfiable.
//
// variant selects which of SelectVar/SelectVarFast is used to pick
// the branching variable at every node (see SelectVarVariant).
//
// If timeLimit is non-nil, the search gives up and reports an
// inconclusive result (Satisfiable == false, TimedOut == true) once
// it is exceeded, checked only periodically (see timeCheckInterval)
// rather than after every node. rng supplies the randomness the
// selected heuristic uses to break ties, and verbose controls
// progress output: at verbose >= 1, "dfs" and the configured time
// limit (if any) are printed before searching, and "SAT", "UNSAT", or
// "UNKNOWN" (on timeout) are printed after.
func Run(problem *cnf.Problem, lists *occurrence.Lists, timeLimit *time.Duration, variant SelectVarVariant, rng *rand.Rand, verbose int) Result {
	if verbose >= 1 {
		fmt.Println("dfs:", describeParams(timeLimit, variant))
	}

	// A problem with no variables can only contain empty clauses (no
	// literal can reference a variable beyond NumVars), each of which
	// is unsatisfiable by construction; guard this degenerate case
	// explicitly so SelectVar is never asked to choose a variable that
	// does not exist.
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
	if allAssigned(root.assignment) {
		// Bootstrap's own unit propagation alone already fully solved
		// the formula (e.g. one made entirely of unit clauses); see
		// allAssigned's doc comment for why this is the only node
		// that ever needs this check.
		if verbose >= 1 {
			fmt.Println("SAT")
		}
		return Result{Satisfiable: true, Assignment: root.assignment}
	}

	// workingProblem wraps the (possibly bootstrap-simplified) clause
	// set for SelectVar/SelectVarFastPick, which only ever need the
	// clauses and variable count, not the original Problem value.
	workingProblem := &cnf.Problem{NumVars: problem.NumVars, Clauses: clauses}

	startTime := time.Now()
	state := startSearch(clauses, lists, workingProblem, root, variant, rng)

	checkPause := func(numNodesThisCall int) bool {
		// The root frame counts as node 1 (see below), so it's folded
		// into this call's own count for the periodic-check bitmask to
		// stay meaningful from the very first call.
		return timeLimit != nil && (numNodesThisCall+1)&timeCheckInterval == 0 && time.Since(startTime) >= *timeLimit
	}
	outcome, stepped := state.step(workingProblem, variant, rng, checkPause)
	numNodes := 1 + stepped // the root frame itself, matching step's "one push = one node" convention

	switch outcome {
	case stepSAT:
		if verbose >= 1 {
			fmt.Println("SAT")
		}
		return Result{Satisfiable: true, Assignment: state.x, NumNodes: numNodes}
	case stepUNSAT:
		if verbose >= 1 {
			fmt.Println("UNSAT")
		}
		return Result{Satisfiable: false, NumNodes: numNodes}
	default: // stepPaused
		if verbose >= 1 {
			fmt.Println("UNKNOWN")
		}
		return Result{Satisfiable: false, NumNodes: numNodes, TimedOut: true}
	}
}

// literalIsTrue reports whether lit evaluates to true when its
// variable holds value (which must not be assign.Unassigned).
func literalIsTrue(lit cnf.Literal, value assign.Value) bool {
	if lit.IsNegative() {
		return value == assign.False
	}
	return value == assign.True
}

// SelectVar chooses which unassigned variable of problem to branch on
// next, given partial assignment x, following STAGE5.md's heuristic:
// for every not-yet-satisfied clause, every currently unassigned
// variable in it earns a share of weight 0.7^(n-2), where n is the
// number of unassigned variables in that clause (n >= 2 for every
// clause reached after at least one round of BCP, since BCP would
// already have propagated or rejected any clause with fewer
// unassigned literals; the very first call, on the wholly unassigned
// root, is the only exception, and the formula still produces a
// well-defined, if unrepresentative, weight there). The variable with
// the highest total score is selected; ties are broken uniformly at
// random using rng.
func SelectVar(problem *cnf.Problem, x assign.Assignment, rng *rand.Rand) int {
	score := make([]float64, problem.NumVars+1)

	for _, clause := range problem.Clauses {
		satisfied := false
		var unassignedVars []int
		for _, lit := range clause {
			value := x[lit.Var()]
			if value == assign.Unassigned {
				unassignedVars = append(unassignedVars, lit.Var())
				continue
			}
			if literalIsTrue(lit, value) {
				satisfied = true
				break
			}
		}
		if satisfied {
			continue
		}
		weight := math.Pow(0.7, float64(len(unassignedVars)-2))
		for _, v := range unassignedVars {
			score[v] += weight
		}
	}

	bestVar := -1
	bestScore := 0.0
	tieCount := 0
	for v := 1; v <= problem.NumVars; v++ {
		if x[v] != assign.Unassigned {
			continue
		}
		switch {
		case bestVar == -1 || score[v] > bestScore:
			bestVar, bestScore, tieCount = v, score[v], 1
		case score[v] == bestScore:
			tieCount++
			// Reservoir sampling: keep the new candidate with
			// probability 1/tieCount, so every tied candidate seen so
			// far remains equally likely to be selected.
			if rng.IntN(tieCount) == 0 {
				bestVar = v
			}
		}
	}
	return bestVar
}

// SelectVarFastPick implements SelectVarFast (see SelectVarVariant):
// it returns the lowest-numbered variable that is still Unassigned in
// x, without examining any clause. There is nothing to break ties
// between (the choice is always unique), so unlike SelectVar this
// needs no random source.
func SelectVarFastPick(problem *cnf.Problem, x assign.Assignment) int {
	for v := 1; v <= problem.NumVars; v++ {
		if x[v] == assign.Unassigned {
			return v
		}
	}
	return -1
}

// describeParams formats the configured time limit and SelectVar
// variant for the "dfs:" announcement printed at verbose level 1.
func describeParams(timeLimit *time.Duration, variant SelectVarVariant) string {
	description := fmt.Sprintf("select_var=%d", variant)
	if timeLimit != nil {
		description += fmt.Sprintf(" time_limit_secs=%d", int(timeLimit.Seconds()))
	}
	return description
}
