// Package cdcl implements conflict-driven clause learning with
// non-chronological backtracking (CDCL), per STAGE11.md: Marques-Silva
// & Sakallah, "GRASP: A Search Algorithm for Propositional
// Satisfiability," IEEE Trans. Computers, 1999 (originally 1996), and
// Moskewicz, Madigan, Zhao, Zhang & Malik, "Chaff: Engineering an
// Efficient SAT Solver," DAC 2001.
//
// Unlike dfs (internal/dfs), which explores an explicit stack of
// independent, fully cloned partial assignments and simply abandons a
// branch that reaches a contradiction, cdcl maintains a single,
// persistent assignment trail with real backtracking. When
// propagation reaches a contradiction, it walks the implication graph
// backward (the reason clause of each forced literal, transitively)
// to derive a new clause that explains the conflict -- the "first
// unique implication point" (first-UIP) scheme -- adds that learned
// clause to the formula, and jumps directly back to the decision
// level where the learned clause becomes a unit clause, rather than
// just retrying the other branch one level up. This is what makes
// watched literals (STAGE9.md) pay for themselves: BCP now runs once
// per conflict as well as once per decision, and a persistent trail
// (rather than dfs's per-branch clones) is exactly what watched
// literals were designed to support with no extra bookkeeping on
// backtrack (see backtrackTo's doc comment).
//
// Per STAGE11.md, dfs is left as-is; cdcl is a wholly separate
// algorithm selectable via --algorithm=cdcl/-a cdcl, sharing dfs's
// SelectVar heuristics (dfs.SelectVar/dfs.SelectVarFastPick) but
// nothing else.
//
// STAGE12.md adds learned-clause database management: once the
// estimated size of the clause database (see clauseByteCost) exceeds
// an optional user-supplied memory limit, reduceClauseDatabase deletes
// the least "active" half of the learned clauses that are safe to
// delete (see its doc comment), MiniSat-style (Eén & Sörensson,
// "An Extensible SAT-solver," SAT 2003).
package cdcl

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"time"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/dfs"
	"vibe_sat/internal/occurrence"
	"vibe_sat/internal/preprocess"
)

// noReason marks a variable's reason slot as having no antecedent
// clause: either it was fixed by the bootstrap unit propagation below
// (permanently, at level 0) or it is a decision. -1 is never a valid
// clause index.
const noReason = -1

// timeCheckInterval controls how often the time limit is checked
// against the number of decisions+conflicts processed, matching
// dfs's timeCheckInterval and for the same reason: checking the clock
// on every single step would add needless overhead.
const timeCheckInterval = 0xfff

// bytesPerLiteral and perClauseOverheadBytes approximate the memory
// footprint of one clause, for comparing against a user-supplied
// --alg-params memory limit (STAGE12.md): 4 bytes per cnf.Literal
// (an int32), plus a fixed overhead per clause standing in for its
// slice header and its parallel watch/activity slots (see solver's
// watch/activity fields). This is deliberately an approximation, not
// an exact accounting of Go's actual heap usage -- the point is a
// reduction policy that responds sensibly to a size budget, not a
// byte-for-byte memory profiler.
const (
	bytesPerLiteral        = 4
	perClauseOverheadBytes = 40
)

// clauseActivityDecay and clauseActivityRescaleThreshold implement
// MiniSat's clause activity bookkeeping (see solver's activity
// field): rather than multiplying every clause's activity by
// clauseActivityDecay after every conflict (an O(clauses) cost per
// conflict), the single clauseActivityIncrement is grown by
// 1/clauseActivityDecay instead, which has the same relative effect
// (older bumps count for less compared to newer ones) at O(1) cost.
// If the increment ever grows past clauseActivityRescaleThreshold,
// every clause's activity and the increment itself are divided back
// down by the same factor, to stay well within float64's range over
// a very long run.
const (
	clauseActivityDecay            = 0.999
	clauseActivityRescaleThreshold = 1e100
)

// Result describes the outcome of a CDCL search.
type Result struct {
	Satisfiable  bool              // whether a satisfying assignment was found
	Assignment   assign.Assignment // the satisfying assignment; only meaningful if Satisfiable
	NumDecisions int               // number of branching decisions made
	NumConflicts int               // number of conflicts encountered (= number of clauses learned)
	TimedOut     bool              // true if the search was abandoned due to the time limit, rather than exhausting the search space
}

// solver holds all of the mutable state of one CDCL run.
type solver struct {
	numVars int

	// clauses grows over time as clauses are learned (length >= 2
	// only; a learned clause of length 1 is applied directly as a
	// permanent level-0 fact instead, see addLearnedClause). watch is
	// always the same length as clauses and holds, for every clause,
	// the two literals it currently watches (see STAGE9.md's
	// watchState, which this mirrors but keeps as a single persistent
	// structure rather than one cloned per branch -- see
	// backtrackTo's doc comment for why that's sound here). lists is
	// the occurrence index from Stage 2, extended in place every time
	// a clause is learned; unlike dfs's, this one must be mutable.
	clauses []cnf.Clause
	watch   [][2]cnf.Literal
	lists   *occurrence.Lists

	// numOriginalClauses is len(clauses) immediately after bootstrap,
	// before any clause is learned. Every clause at an index below
	// this is part of the original (bootstrapped) problem and must
	// never be deleted; only clauses at or above it are learned, and
	// so are eligible for reduceClauseDatabase to consider. Since
	// reduceClauseDatabase only ever removes entries at indices >=
	// numOriginalClauses while preserving the relative order of
	// everything else, this threshold stays valid across any number
	// of reduction passes without needing to be updated.
	numOriginalClauses int

	// activity holds one MiniSat-style "activity" score per clause
	// (see clauseActivityDecay), parallel to clauses; only entries at
	// index >= numOriginalClauses are ever read or written, since
	// original clauses are never deletion candidates.
	activity                []float64
	clauseActivityIncrement float64

	// estimatedBytes tracks clauseByteCost summed over every current
	// clause, kept up to date incrementally as clauses are learned or
	// deleted (rather than recomputed from scratch on every check) --
	// except immediately after a reduction pass, where recomputing it
	// once from the (now much shorter) clause list is simpler than
	// trying to track the exact amount subtracted.
	estimatedBytes int64
	// memoryLimitBytes is the optional --alg-params-supplied limit
	// (STAGE12.md); nil means "no limit" (STAGE11.md's original,
	// unbounded behavior).
	memoryLimitBytes *int64

	x            assign.Assignment // current (partial) assignment
	level        []int             // level[v] = decision level at which v was assigned (meaningless if x[v] is Unassigned)
	reason       []int             // reason[v] = index into clauses of the clause that forced v, or noReason for a decision or a level-0 fact
	trail        []int             // variable numbers, in the order they were assigned
	trailLim     []int             // trailLim[d] = len(trail) at the moment decision level d began; trailLim[0] == 0
	currentLevel int
	qHead        int // propagate() has already processed trail[:qHead]

	seen []bool // scratch space for analyze, sized numVars+1

	numDecisions int
	numConflicts int
}

// clauseByteCost estimates clause's contribution to the clause
// database's memory footprint; see bytesPerLiteral/perClauseOverheadBytes.
func clauseByteCost(clause cnf.Clause) int64 {
	return perClauseOverheadBytes + int64(len(clause))*bytesPerLiteral
}

// newSolver builds the initial solver state for problem: a defensive
// bootstrap round of unit propagation (see dfs.Run's identical
// bootstrap step for why this is needed even though Stage 8's
// preprocessing already does this by default), followed by initial
// watch state for whatever clauses survive it. memoryLimitBytes is
// the optional learned-clause database memory limit (STAGE12.md); nil
// means unbounded. ok is false if this bootstrap alone already proves
// problem unsatisfiable.
func newSolver(problem *cnf.Problem, memoryLimitBytes *int64) (s *solver, ok bool) {
	clauses := append([]cnf.Clause(nil), problem.Clauses...)
	x := assign.New(problem.NumVars)
	if unsat, _ := preprocess.UnitPropagate(&clauses, x); unsat {
		return nil, false
	}

	watch := make([][2]cnf.Literal, len(clauses))
	var estimatedBytes int64
	for c, clause := range clauses {
		first, foundFirst := chooseWatch(clause, x, 0)
		if !foundFirst {
			return nil, false
		}
		second, foundSecond := chooseWatch(clause, x, first)
		if !foundSecond {
			return nil, false
		}
		watch[c] = [2]cnf.Literal{first, second}
		estimatedBytes += clauseByteCost(clause)
	}

	reason := make([]int, problem.NumVars+1)
	for v := range reason {
		reason[v] = noReason
	}

	return &solver{
		numVars:                 problem.NumVars,
		clauses:                 clauses,
		watch:                   watch,
		lists:                   occurrence.Build(&cnf.Problem{NumVars: problem.NumVars, Clauses: clauses}),
		numOriginalClauses:      len(clauses),
		activity:                make([]float64, len(clauses)),
		clauseActivityIncrement: 1.0,
		estimatedBytes:          estimatedBytes,
		memoryLimitBytes:        memoryLimitBytes,
		x:                       x,
		level:                   make([]int, problem.NumVars+1),
		reason:                  reason,
		trailLim:                []int{0},
		seen:                    make([]bool, problem.NumVars+1),
	}, true
}

// Run performs a CDCL search: repeatedly propagate, and on a
// conflict, learn a clause and backjump; on reaching a fixpoint with
// no conflict, either the assignment is complete (satisfiable) or a
// new variable is chosen to branch on (a decision, always tried
// False first -- see decide's doc comment). If propagate ever
// conflicts while at decision level 0 (nothing left to backjump to),
// the problem is proven unsatisfiable.
//
// variant selects which of dfs.SelectVar/dfs.SelectVarFastPick is
// used to pick the branching variable at every decision (see
// dfs.SelectVarVariant); per STAGE11.md, this is the first algorithm
// parameter cdcl accepts, same as dfs. memoryLimitBytes is the
// second, optional, STAGE12.md algorithm parameter: once the
// estimated size of the learned-clause database exceeds it, the
// least active learned clauses are periodically deleted (see
// reduceClauseDatabase); nil means unbounded, matching cdcl's
// original (Stage 11) behavior.
//
// If timeLimit is non-nil, the search gives up and reports an
// inconclusive result (Satisfiable == false, TimedOut == true) once
// it is exceeded, checked only periodically (see timeCheckInterval).
// rng supplies the randomness SelectVar uses to break ties, and
// verbose controls progress output, matching dfs.Run.
func Run(problem *cnf.Problem, timeLimit *time.Duration, variant dfs.SelectVarVariant, memoryLimitBytes *int64, rng *rand.Rand, verbose int) Result {
	if verbose >= 1 {
		fmt.Println("cdcl:", describeParams(timeLimit, variant)+describeMemoryLimit(memoryLimitBytes))
	}

	if problem.NumVars == 0 {
		satisfiable := len(problem.Clauses) == 0
		if verbose >= 1 {
			fmt.Println(map[bool]string{true: "SAT", false: "UNSAT"}[satisfiable])
		}
		return Result{Satisfiable: satisfiable, Assignment: assign.New(0)}
	}

	s, ok := newSolver(problem, memoryLimitBytes)
	if !ok {
		if verbose >= 1 {
			fmt.Println("UNSAT")
		}
		return Result{Satisfiable: false}
	}

	startTime := time.Now()
	step := 0

	for {
		step++
		if timeLimit != nil && step&timeCheckInterval == 0 && time.Since(startTime) >= *timeLimit {
			if verbose >= 1 {
				fmt.Println("UNKNOWN")
			}
			return Result{Satisfiable: false, NumDecisions: s.numDecisions, NumConflicts: s.numConflicts, TimedOut: true}
		}

		if confl := s.propagate(); confl != noReason {
			s.numConflicts++
			if s.currentLevel == 0 {
				if verbose >= 1 {
					fmt.Println("UNSAT")
				}
				return Result{Satisfiable: false, NumDecisions: s.numDecisions, NumConflicts: s.numConflicts}
			}
			s.learnAndBackjump(confl)
			continue
		}

		if s.allAssigned() {
			if verbose >= 1 {
				fmt.Println("SAT")
			}
			return Result{Satisfiable: true, Assignment: s.x, NumDecisions: s.numDecisions, NumConflicts: s.numConflicts}
		}

		s.decide(variant, rng)
	}
}

// allAssigned reports whether every variable of the solver currently
// has a value.
func (s *solver) allAssigned() bool {
	for _, value := range s.x[1:] {
		if value == assign.Unassigned {
			return false
		}
	}
	return true
}

// decide chooses the next branching variable via SelectVar/
// SelectVarFastPick (matching dfs's variant selection exactly) and
// pushes it onto the trail as a new decision level, always trying
// False first -- the same order dfs's branch loop uses, chosen here
// for consistency rather than any phase-saving heuristic (CDCL
// doesn't get to try both polarities at one level the way dfs does;
// if False turns out wrong, conflict analysis is what corrects it, by
// deriving a clause that forces True once it backjumps here again).
func (s *solver) decide(variant dfs.SelectVarVariant, rng *rand.Rand) {
	// Rebuilding this on every decision (rather than caching it) keeps
	// it trivially correct as s.clauses grows via learned clauses --
	// this is also the answer to STAGE11.md's question of whether
	// learned clauses feed back into SelectVar: yes, for the weighted
	// variant, since it scans every not-yet-satisfied clause,
	// including learned ones (SelectVarFast ignores clause contents
	// entirely either way).
	workingProblem := &cnf.Problem{NumVars: s.numVars, Clauses: s.clauses}

	var v int
	if variant == dfs.SelectVarFast {
		v = dfs.SelectVarFastPick(workingProblem, s.x)
	} else {
		v = dfs.SelectVar(workingProblem, s.x, rng)
	}

	s.numDecisions++
	s.currentLevel++
	s.trailLim = append(s.trailLim, len(s.trail))
	s.assignLiteral(cnf.Literal(-v), s.currentLevel, noReason)
}

// assignLiteral records lit as true (setting its variable's value,
// level, and reason accordingly) and appends it to the trail.
func (s *solver) assignLiteral(lit cnf.Literal, level int, reason int) {
	v := lit.Var()
	if lit.IsNegative() {
		s.x[v] = assign.False
	} else {
		s.x[v] = assign.True
	}
	s.level[v] = level
	s.reason[v] = reason
	s.trail = append(s.trail, v)
}

// propagate applies boolean constraint propagation via watched
// literals (see STAGE9.md's BCP, which this mirrors closely) starting
// from wherever it last left off (s.qHead), continuing until either
// the trail is exhausted (no conflict: returns noReason) or some
// clause becomes fully falsified, in which case that clause's index
// is returned as the conflict.
func (s *solver) propagate() int {
	for s.qHead < len(s.trail) {
		v := s.trail[s.qHead]
		s.qHead++

		var falsifiedLiteral cnf.Literal
		var candidates []int
		if s.x[v] == assign.True {
			falsifiedLiteral = cnf.Literal(-v)
			candidates = s.lists.Negative[v]
		} else {
			falsifiedLiteral = cnf.Literal(v)
			candidates = s.lists.Positive[v]
		}

		for _, c := range candidates {
			watch := &s.watch[c]
			var otherWatch cnf.Literal
			switch {
			case watch[0] == falsifiedLiteral:
				otherWatch = watch[1]
			case watch[1] == falsifiedLiteral:
				otherWatch = watch[0]
			default:
				continue // this clause isn't watching the falsified literal
			}

			if replacement, found := chooseWatch(s.clauses[c], s.x, otherWatch); found {
				if watch[0] == falsifiedLiteral {
					watch[0] = replacement
				} else {
					watch[1] = replacement
				}
				continue
			}

			if isFalse(otherWatch, s.x) {
				return c // conflict: clause c is now fully false
			}
			forcedVar := otherWatch.Var()
			if s.x[forcedVar] == assign.Unassigned {
				s.assignLiteral(otherWatch, s.currentLevel, c)
			}
			// Otherwise otherWatch is already true: the clause is
			// satisfied through it, and there is nothing to do.
		}
	}
	return noReason
}

// learnAndBackjump handles one conflict discovered by propagate at
// clause index confl: it derives a learned clause via analyze, jumps
// back to the decision level analyze computed, adds the learned
// clause to the database (unless it's a unit clause, see
// addLearnedClause), and immediately asserts its asserting literal --
// which is now a forced consequence of the learned clause, not a
// fresh decision, so the next call to propagate() will pick it up
// from the trail exactly like any other propagated literal.
//
// Once per conflict (matching MiniSat's claDecayActivity), the clause
// activity increment is grown so that future activity bumps (in
// analyze) count for relatively more than past ones -- the O(1)
// equivalent of decaying every clause's activity individually. If a
// memory limit was given (STAGE12.md) and the database's estimated
// size has grown past it, reduceClauseDatabase is triggered.
func (s *solver) learnAndBackjump(confl int) {
	learned, backtrackLevel := s.analyze(confl)
	s.backtrackTo(backtrackLevel)
	newClause := s.addLearnedClause(learned)
	s.assignLiteral(learned[0], backtrackLevel, newClause)

	s.clauseActivityIncrement /= clauseActivityDecay
	if s.clauseActivityIncrement > clauseActivityRescaleThreshold {
		for i := range s.activity {
			s.activity[i] /= clauseActivityRescaleThreshold
		}
		s.clauseActivityIncrement /= clauseActivityRescaleThreshold
	}

	if s.memoryLimitBytes != nil && s.estimatedBytes > *s.memoryLimitBytes {
		s.reduceClauseDatabase()
	}
}

// analyze walks the implication graph backward from the clause at
// index confl (which propagate just found to be fully false) to
// derive a learned clause via first-UIP resolution: repeatedly resolve
// the current clause against the reason of the most-recently-assigned
// still-unresolved literal at the conflict's own decision level, until
// exactly one such literal remains -- the "first unique implication
// point." That literal's negation becomes the learned clause's
// asserting literal (returned first in learned); every other literal
// collected along the way is already false at some level below the
// conflict's, which is exactly what makes the learned clause a unit
// clause (modulo the asserting literal) the moment the search
// backjumps to backtrackLevel, the highest level among those other
// literals (0 if there are none).
//
// This is the standard GRASP/Chaff conflict analysis (see the package
// doc comment for references); level-0 literals are omitted entirely,
// since they are permanent facts that can never become unassigned
// again and so need no antecedent recorded in the learned clause.
//
// As a side effect (STAGE12.md), every learned clause visited along
// the way (every reasonClause at index >= s.numOriginalClauses) has
// its activity bumped by s.clauseActivityIncrement, MiniSat's measure
// of how useful a clause has recently been to conflict analysis --
// this is what reduceClauseDatabase later uses to decide which
// learned clauses to keep.
func (s *solver) analyze(confl int) (learned cnf.Clause, backtrackLevel int) {
	for i := range s.seen {
		s.seen[i] = false
	}

	var p cnf.Literal // 0 = "no literal yet", used only for the very first (conflicting) clause
	counter := 0
	trailIdx := len(s.trail) - 1
	reasonClause := confl

	for {
		if reasonClause >= s.numOriginalClauses {
			s.activity[reasonClause] += s.clauseActivityIncrement
		}
		for _, lit := range s.clauses[reasonClause] {
			if p != 0 && lit.Var() == p.Var() {
				continue
			}
			v := lit.Var()
			if s.seen[v] || s.level[v] == 0 {
				continue
			}
			s.seen[v] = true
			if s.level[v] == s.currentLevel {
				counter++
			} else {
				learned = append(learned, lit)
			}
		}

		var v int
		for {
			v = s.trail[trailIdx]
			trailIdx--
			if s.seen[v] {
				break
			}
		}
		p = literalAssignedTrue(v, s.x)
		counter--
		if counter == 0 {
			break
		}
		if s.reason[v] == noReason {
			// Should be unreachable: the decision variable of the
			// current level is always a valid (if late) UIP, and is
			// always the last current-level literal this scan can
			// reach, so counter must hit 0 at or before it.
			panic("cdcl: analyze reached a variable with no reason while literals of the current level remain unresolved")
		}
		reasonClause = s.reason[v]
	}

	learned = append(cnf.Clause{-p}, learned...)

	backtrackLevel = 0
	for _, lit := range learned[1:] {
		if lv := s.level[lit.Var()]; lv > backtrackLevel {
			backtrackLevel = lv
		}
	}
	return learned, backtrackLevel
}

// literalAssignedTrue returns the literal on variable v that
// evaluates to true under x (v must not be Unassigned in x).
func literalAssignedTrue(v int, x assign.Assignment) cnf.Literal {
	if x[v] == assign.True {
		return cnf.Literal(v)
	}
	return cnf.Literal(-v)
}

// backtrackTo undoes every assignment made after decision level
// level, resetting the trail, trail limits, and propagation queue
// accordingly. Unlike dfs's per-branch clones, cdcl's watch state is
// never cloned or explicitly restored on backtrack: a watch remains
// valid as long as it isn't watching a literal that's currently
// false, and backtracking only ever turns assigned literals back into
// unassigned ones -- it can never turn a non-false literal into a
// false one -- so every watch already in place is still a legal watch
// after backtracking, with nothing to undo. This is precisely the
// property that makes watched literals cheap under non-chronological
// backtracking, and is why cdcl uses a single persistent trail
// instead of dfs's cloned-per-branch approach (see STAGE9.md's
// watchState doc comment for the tension this resolves).
func (s *solver) backtrackTo(level int) {
	cut := s.trailLim[level+1]
	for i := len(s.trail) - 1; i >= cut; i-- {
		s.x[s.trail[i]] = assign.Unassigned
	}
	s.trail = s.trail[:cut]
	s.trailLim = s.trailLim[:level+1]
	s.currentLevel = level
	s.qHead = len(s.trail)
}

// addLearnedClause appends learned to the clause database and
// extends the occurrence lists and watch state for it, unless it's a
// unit clause (length 1): a unit clause has only one literal, which
// has nowhere to shed a second watch onto, and needs none anyway --
// analyze's backtrackLevel is always 0 for a unit learned clause, so
// its (asserting) literal is about to become a permanent level-0
// fact, exactly like a variable fixed by the bootstrap unit
// propagation in newSolver. Returns the new clause's index, or
// noReason if none was stored.
func (s *solver) addLearnedClause(learned cnf.Clause) int {
	if len(learned) == 1 {
		return noReason
	}

	idx := len(s.clauses)
	s.clauses = append(s.clauses, learned)
	s.activity = append(s.activity, 0.0)
	s.estimatedBytes += clauseByteCost(learned)
	for _, lit := range learned {
		v := lit.Var()
		if lit.IsNegative() {
			s.lists.Negative[v] = append(s.lists.Negative[v], idx)
		} else {
			s.lists.Positive[v] = append(s.lists.Positive[v], idx)
		}
	}

	// learned[0] is the asserting literal, currently unassigned;
	// watch it directly (chooseWatch would find it too, but it's
	// about to be assigned true by the caller regardless). The second
	// watch is whichever other literal has the highest decision
	// level, since that is the one that will become unassigned
	// soonest on some future backtrack, keeping this watch valid the
	// longest before it needs to shed anywhere.
	best := 1
	for i := 2; i < len(learned); i++ {
		if s.level[learned[i].Var()] > s.level[learned[best].Var()] {
			best = i
		}
	}
	s.watch = append(s.watch, [2]cnf.Literal{learned[0], learned[best]})
	return idx
}

// reduceClauseDatabase deletes roughly the least active half of the
// learned clauses that are safe to delete, MiniSat-style (see the
// package doc comment), to bring the database's estimated size back
// under control. A learned clause is "safe to delete" if it is not
// currently locked: locked means it is some currently assigned
// variable's reason (reason[v] for some v with x[v] != Unassigned),
// since analyze may still need to walk through it if that variable's
// assignment participates in a future conflict. Clauses at index <
// s.numOriginalClauses (the original, bootstrapped problem) are never
// candidates at all -- deleting one of those would be unsound, not
// just wasteful.
//
// Deleting from the middle of s.clauses would silently invalidate
// every other index into it (every watch entry, every reason[v], and
// every occurrence.Lists entry), so this rebuilds all of them
// together from an old-index-to-new-index map, rather than trying to
// patch each in place. This is an O(current clauses + literals)
// operation, which is fine since it only runs when the configured
// memory limit is actually exceeded, not on every conflict.
func (s *solver) reduceClauseDatabase() {
	locked := make([]bool, len(s.clauses))
	for v := 1; v <= s.numVars; v++ {
		if s.x[v] != assign.Unassigned && s.reason[v] != noReason {
			locked[s.reason[v]] = true
		}
	}

	var eligible []int
	for idx := s.numOriginalClauses; idx < len(s.clauses); idx++ {
		if !locked[idx] {
			eligible = append(eligible, idx)
		}
	}
	sort.Slice(eligible, func(i, j int) bool {
		return s.activity[eligible[i]] < s.activity[eligible[j]]
	})

	numToDelete := len(eligible) / 2
	if numToDelete == 0 {
		return // nothing eligible to delete; not worth a full rebuild
	}
	toDelete := make([]bool, len(s.clauses))
	for _, idx := range eligible[:numToDelete] {
		toDelete[idx] = true
	}

	oldToNew := make([]int, len(s.clauses))
	newClauses := make([]cnf.Clause, 0, len(s.clauses)-numToDelete)
	newWatch := make([][2]cnf.Literal, 0, len(s.clauses)-numToDelete)
	newActivity := make([]float64, 0, len(s.clauses)-numToDelete)
	for idx, clause := range s.clauses {
		if toDelete[idx] {
			oldToNew[idx] = noReason
			continue
		}
		oldToNew[idx] = len(newClauses)
		newClauses = append(newClauses, clause)
		newWatch = append(newWatch, s.watch[idx])
		newActivity = append(newActivity, s.activity[idx])
	}

	for v := 1; v <= s.numVars; v++ {
		if s.x[v] != assign.Unassigned && s.reason[v] != noReason {
			s.reason[v] = oldToNew[s.reason[v]]
		}
	}

	s.clauses = newClauses
	s.watch = newWatch
	s.activity = newActivity
	s.lists = occurrence.Build(&cnf.Problem{NumVars: s.numVars, Clauses: newClauses})

	s.estimatedBytes = 0
	for _, clause := range newClauses {
		s.estimatedBytes += clauseByteCost(clause)
	}
}

// chooseWatch scans clause for a literal that is not false under
// assignment and is not equal to avoid (0 is never a valid literal,
// so passing 0 imposes no exclusion). Identical in spirit to
// STAGE9.md's dfs/watch.go helper of the same name; duplicated here
// (rather than exported from internal/dfs) since STAGE11.md asks that
// dfs be left as it is.
func chooseWatch(clause cnf.Clause, assignment assign.Assignment, avoid cnf.Literal) (cnf.Literal, bool) {
	for _, lit := range clause {
		if lit == avoid {
			continue
		}
		if !isFalse(lit, assignment) {
			return lit, true
		}
	}
	return 0, false
}

// isFalse reports whether lit currently evaluates to false under
// assignment (an Unassigned variable makes every literal on it
// neither true nor false yet, so this returns false for those).
func isFalse(lit cnf.Literal, assignment assign.Assignment) bool {
	value := assignment[lit.Var()]
	if value == assign.Unassigned {
		return false
	}
	if lit.IsNegative() {
		return value == assign.True
	}
	return value == assign.False
}

// describeParams formats the configured time limit and SelectVar
// variant for the "cdcl:" announcement printed at verbose level 1.
func describeParams(timeLimit *time.Duration, variant dfs.SelectVarVariant) string {
	description := fmt.Sprintf("select_var=%d", variant)
	if timeLimit != nil {
		description += fmt.Sprintf(" time_limit_secs=%d", int(timeLimit.Seconds()))
	}
	return description
}

// describeMemoryLimit formats an optional STAGE12.md memory limit for
// the "cdcl:" announcement printed at verbose level 1, in whichever
// unit main.go's --alg-params parsing recorded it in bytes as.
func describeMemoryLimit(memoryLimitBytes *int64) string {
	if memoryLimitBytes == nil {
		return ""
	}
	return fmt.Sprintf(" memory_limit_bytes=%d", *memoryLimitBytes)
}
