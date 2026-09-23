package dfs

import (
	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
)

// watchState holds, for every clause, the two literals it is
// currently watching (see Chaff: Moskewicz, Madigan, Zhao, Zhang &
// Malik, "Chaff: Engineering an Efficient SAT Solver," DAC 2001), plus
// (STAGE46.md) watchersPositive[v]/watchersNegative[v]: the indices of
// clauses currently watching literal +v/-v, the genuine dynamic index
// BCP actually needs to find them cheaply. BCP only ever has to look
// closely at a clause when one of its two watches becomes false,
// instead of every clause containing that literal: a clause "sheds" a
// watch away from an about-to-be-false literal onto some other
// not-yet-false literal whenever it can, which is what makes
// propagation cheap in the steady state -- but only if the structure
// used to *find* candidate clauses actually shrinks to match, which
// is exactly what watchersPositive/watchersNegative do and the old
// design (see below) did not.
//
// STAGE32.md/REPORT32.md: before that stage, every branch got its own
// clone of watchState (see cloneWatchState), matching STAGE5.md's
// "explicit stack of self-contained nodes" design rather than the
// single shared, incrementally backtracked trail cdcl uses. That was
// found unsafe at scale (REPORT29.md: a single clone runs 100+ MB on
// a 17.7-million-clause file). searchState (dfs.go) now shares one
// watchState for an entire sequential exploration, mutated in place
// and never cloned on backtrack -- a watch remains valid as long as
// it isn't watching a literal that's currently false, and
// backtracking only ever turns assigned literals back into unassigned
// ones, so every watch already in place is still legal afterward,
// with nothing to undo (see cdcl's backtrackTo doc comment for the
// identical argument). cloneWatchState still exists, but is now used
// only where a genuinely independent snapshot is needed: bfsSeed's
// initial per-worker seeds and searchState.shedFrame's occasional,
// deliberate materialization of one snapshot for another worker to
// steal -- both rare compared to the total number of nodes explored.
//
// STAGE46.md: from Stage 9 through Stage 45, BCP found its candidate
// clauses via the *full* static occurrence index (internal/occurrence,
// every clause that ever mentions a literal), the same design cdcl's
// own BCP used before STAGE45.md's fix -- see reports/REPORT45.md for
// the full diagnosis (in short: this makes propagating a literal cost
// O(occurrences) instead of O(current watchers), defeating most of
// the point of watching only two literals per clause in the first
// place). watchersPositive/watchersNegative are STAGE45.md's fix,
// ported here: unlike cdcl, dfs has no other consumer of the old
// occurrence index once BCP stops needing it (no WalkSAT-style
// rephasing reuses it here), so internal/occurrence.Lists is no
// longer threaded through this package at all -- see Run/RunParallel's
// own doc comments.
//
// watch[c] is always exactly 2 distinct literals, which requires
// every clause to have at least 2 literals; callers must guarantee
// this (via an initial round of unit propagation removing any unit
// or empty clauses) before calling newWatchState.
type watchState struct {
	watch            [][2]cnf.Literal
	watchersPositive [][]int
	watchersNegative [][]int
}

// appendWatcher (STAGE46.md) records that clause index c is now one
// of lit's watchers, in whichever of pos/neg actually corresponds to
// lit's sign -- mirrors cdcl.go's identical helper exactly (kept as a
// separate, duplicated function here rather than shared, per
// STAGE11.md's "dfs is left as its own, separate implementation").
func appendWatcher(pos, neg [][]int, lit cnf.Literal, c int) {
	if lit.IsNegative() {
		neg[lit.Var()] = append(neg[lit.Var()], c)
	} else {
		pos[lit.Var()] = append(pos[lit.Var()], c)
	}
}

// watchersFor returns a pointer to the slice of clause indices
// currently watching lit, so BCP can append to or compact it in
// place; mirrors cdcl.go's solver.watchersFor exactly.
func (ws *watchState) watchersFor(lit cnf.Literal) *[]int {
	if lit.IsNegative() {
		return &ws.watchersNegative[lit.Var()]
	}
	return &ws.watchersPositive[lit.Var()]
}

// newWatchState builds a watchState for clauses, choosing for every
// clause two literals that are not false under assignment. ok is
// false if some clause has fewer than two such literals (a
// contradiction, given the precondition above; checked defensively
// rather than assumed).
func newWatchState(clauses []cnf.Clause, assignment assign.Assignment) (ws *watchState, ok bool) {
	watch := make([][2]cnf.Literal, len(clauses))
	watchersPositive := make([][]int, len(assignment))
	watchersNegative := make([][]int, len(assignment))
	for c, clause := range clauses {
		first, foundFirst := chooseWatch(clause, assignment, 0)
		if !foundFirst {
			return nil, false
		}
		second, foundSecond := chooseWatch(clause, assignment, first)
		if !foundSecond {
			return nil, false
		}
		watch[c] = [2]cnf.Literal{first, second}
		appendWatcher(watchersPositive, watchersNegative, first, c)
		appendWatcher(watchersPositive, watchersNegative, second, c)
	}
	return &watchState{watch: watch, watchersPositive: watchersPositive, watchersNegative: watchersNegative}, true
}

// cloneWatchState returns an independent copy of ws, for a new search
// branch to mutate without affecting its sibling. STAGE46.md: unlike
// watch (a slice of fixed-size [2]cnf.Literal arrays, so the shallow
// copy below already duplicates every value), watchersPositive/
// watchersNegative are slices of slices -- a shallow copy of the outer
// slice would leave the clone's and the original's inner []int
// slices sharing the same backing arrays, so a later in-place
// compaction write in BCP on one branch's watcher list would corrupt
// the other's. Each inner slice is therefore copied independently.
func cloneWatchState(ws *watchState) *watchState {
	clone := make([][2]cnf.Literal, len(ws.watch))
	copy(clone, ws.watch)

	clonePositive := make([][]int, len(ws.watchersPositive))
	for v, list := range ws.watchersPositive {
		clonePositive[v] = append([]int(nil), list...)
	}
	cloneNegative := make([][]int, len(ws.watchersNegative))
	for v, list := range ws.watchersNegative {
		cloneNegative[v] = append([]int(nil), list...)
	}

	return &watchState{watch: clone, watchersPositive: clonePositive, watchersNegative: cloneNegative}
}

// chooseWatch scans clause for a literal that is not false under
// assignment and is not equal to avoid (0 is never a valid literal,
// so passing 0 imposes no exclusion; this is used when picking a
// clause's first watch, before a second literal exists to avoid
// re-picking).
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

// BCP applies boolean constraint propagation to partial assignment x,
// which must already have variable i set to its just-chosen branch
// value, using ws's watchersPositive/watchersNegative to find only
// the clauses genuinely watching a literal that just became false. x
// and ws are both modified in place.
//
// STAGE46.md: the scan below is MiniSat's own standard in-place
// watch-list compaction pattern, exactly mirroring cdcl.go's
// propagate (see reports/REPORT45.md's fuller explanation of why this
// replaced a full-occurrence-index scan): two indices, scanned (how
// many entries read) and keep (how many of those are kept in this
// same list), since a clause whose watch moves away must leave this
// literal's watcher list while every other clause visited stays. A
// clause that finds no replacement always stays a watcher of the
// literal that just went false -- there's nowhere better for it to
// watch until some future backtrack un-falsifies that literal again
// -- which is why "no replacement" is the only case that writes into
// the kept prefix. On a conflict, the loop stops immediately, but any
// not-yet-scanned entries are still genuine, valid watchers of this
// literal and must be copied into the compacted prefix before
// returning -- skipping this would silently drop clauses from their
// own watch list, surfacing later as a missed propagation or missed
// conflict, not here.
//
// STAGE32.md/REPORT32.md: trail, if non-nil, has every variable BCP
// itself assigns (by forced propagation, not counting i, which the
// caller is responsible for recording -- BCP only ever *reads* i, it
// never appends it) appended in the order they were assigned. This is
// what lets a caller undo exactly this call's effects later (see
// searchState.undoTo) without needing its own clone of x to restore
// from, the way every branch used to before this stage.
func BCP(clauses []cnf.Clause, ws *watchState, x assign.Assignment, i int, trail *[]int) Status {
	queue := []int{i}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]

		var falsifiedLiteral cnf.Literal
		if x[v] == assign.True {
			falsifiedLiteral = cnf.Literal(-v)
		} else {
			falsifiedLiteral = cnf.Literal(v)
		}

		watchers := ws.watchersFor(falsifiedLiteral)
		list := *watchers
		keep := 0
		scanned := 0
		conflict := false

	scan:
		for scanned < len(list) {
			c := list[scanned]
			scanned++

			watch := &ws.watch[c]
			var otherWatch cnf.Literal
			if watch[0] == falsifiedLiteral {
				otherWatch = watch[1]
			} else {
				otherWatch = watch[0]
			}

			if replacement, found := chooseWatch(clauses[c], x, otherWatch); found {
				if watch[0] == falsifiedLiteral {
					watch[0] = replacement
				} else {
					watch[1] = replacement
				}
				*ws.watchersFor(replacement) = append(*ws.watchersFor(replacement), c)
				continue scan
			}

			// No replacement: c keeps watching falsifiedLiteral.
			list[keep] = c
			keep++

			if isFalse(otherWatch, x) {
				conflict = true
				break scan
			}
			forcedVar := otherWatch.Var()
			if x[forcedVar] == assign.Unassigned {
				if otherWatch.IsNegative() {
					x[forcedVar] = assign.False
				} else {
					x[forcedVar] = assign.True
				}
				if trail != nil {
					*trail = append(*trail, forcedVar)
				}
				queue = append(queue, forcedVar)
			}
			// Otherwise otherWatch is already true: the clause is
			// satisfied through it, and there is nothing to do.
		}

		if scanned < len(list) {
			copy(list[keep:], list[scanned:])
			keep += len(list) - scanned
		}
		*watchers = list[:keep]

		if conflict {
			return Contra
		}
	}

	for _, value := range x[1:] {
		if value == assign.Unassigned {
			return OK
		}
	}
	return Done
}
