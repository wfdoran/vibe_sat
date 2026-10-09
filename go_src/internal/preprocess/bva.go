package preprocess

import (
	"container/heap"
	"time"

	"vibe_sat/internal/cnf"
)

// STAGE61.md: Bounded Variable Addition (BVA), the complement of
// bounded variable elimination. Where BVE removes a variable by
// resolving it away, BVA introduces a fresh variable x to replace a
// grid of clauses that share structure, shrinking the clause count.
//
// If a formula contains every clause (lit_i OR C_j) for a set of
// literals {lit_1..lit_a} and a set of clause remainders {C_1..C_b},
// that is a*b clauses. Adding a variable x and replacing the grid with
//
//	(lit_i OR x)       for every i      (a clauses)
//	(NOT x OR C_j)     for every j      (b clauses)
//
// gives a+b clauses, and resolving the new clauses on x gives back
// exactly the old grid. So the new formula is satisfiable iff the old
// one is, and every model of the new formula, restricted to the old
// variables, is a model of the old one. The reduction is a*b - a - b,
// which is positive from a 2x3 grid up.
//
// This is the SimpleBVA procedure of Manthey, Heule & Biere,
// "Automated Reencoding of Boolean Formulas," HVC 2012: take the
// literal with the most occurrences, grow the set of literals greedily
// by whichever literal pairs with the most clauses, and keep growing
// while the reduction improves. The classic target is a pairwise
// at-most-one constraint (every pair of a group of variables
// excluded), which BVA reencodes with far fewer clauses.
//
// Reference: Manthey, Heule & Biere, "Automated Reencoding of Boolean
// Formulas," Haifa Verification Conference 2012.

// literalCode maps a literal to a dense non-negative index: 2*var for a
// positive literal and 2*var+1 for a negative one. Ties between
// equally good choices below always go to the smaller code, which keeps
// the Go and Rust implementations making identical choices.
func literalCode(lit cnf.Literal) int {
	if lit.IsNegative() {
		return 2*lit.Var() + 1
	}
	return 2 * lit.Var()
}

// bvaReduction returns how many clauses replacing a grid of numLits
// literals by numClauses clause remainders saves: numLits*numClauses
// clauses become numLits+numClauses.
func bvaReduction(numLits, numClauses int) int {
	return numLits*numClauses - numLits - numClauses
}

// bvaQueueEntry is one entry of the max-priority queue of literals
// still to try: the literal's occurrence count when the entry was
// pushed, and its code. Entries go stale when the count changes; a stale
// entry is skipped when popped (a fresh one was pushed at the change).
type bvaQueueEntry struct {
	count int
	code  int
}

// bvaQueue is a container/heap max-heap ordered by higher count first,
// then smaller code first.
type bvaQueue []bvaQueueEntry

func (q bvaQueue) Len() int { return len(q) }
func (q bvaQueue) Less(i, j int) bool {
	if q[i].count != q[j].count {
		return q[i].count > q[j].count
	}
	return q[i].code < q[j].code
}
func (q bvaQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *bvaQueue) Push(x any)   { *q = append(*q, x.(bvaQueueEntry)) }
func (q *bvaQueue) Pop() any {
	old := *q
	n := len(old)
	entry := old[n-1]
	*q = old[:n-1]
	return entry
}

// bvaPair records that clause D equals clause C with the literal being
// processed replaced by some other literal: C is the clause index of
// the processed literal's own clause and D the index of its partner.
type bvaPair struct {
	c int
	d int
}

// addBoundedVariables applies Bounded Variable Addition once to
// *clauses, whose variables are numbered 1..numVars, and returns how
// many new variables it added. New variables are numbered numVars+1,
// numVars+2, ... in the order they are added, and the clause set is
// rewritten in place (clauses that were replaced are dropped; the new
// clauses go at the end).
//
// The formula must already be free of unit clauses, tautologies, and
// variables fixed by an assignment, as Run guarantees; a clause with
// fewer than two literals is simply never used. The result is
// satisfiability-equivalent to the input and every model of it,
// restricted to variables 1..numVars, is a model of the input.
//
// *budget is the remaining work allowance, in clause comparisons
// (one per candidate partner clause examined); when it reaches zero no
// further literal is tried. deadline (the zero time.Time meaning none)
// is checked every deadlineCheckInterval units of work. Both limits
// are always safe: a replacement is applied only after it has been
// fully worked out, so stopping just leaves the formula less reduced,
// and timedOut reports that the deadline (not the budget) was why.
func addBoundedVariables(clauses *[]cnf.Clause, numVars int, budget *int, deadline time.Time) (numAdded int, timedOut bool) {
	cs := *clauses
	live := make([]bool, len(cs))
	for i := range live {
		live[i] = true
	}

	// occurrences[code] lists the indices of clauses containing that
	// literal, in increasing order; dead clauses are dropped lazily.
	// count[code] is the number of live clauses containing it.
	occurrences := make([][]int, 2*(numVars+1))
	count := make([]int, 2*(numVars+1))
	for i, clause := range cs {
		for _, lit := range clause {
			code := literalCode(lit)
			occurrences[code] = append(occurrences[code], i)
			count[code]++
		}
	}
	mark := make([]int, 2*(numVars+1)) // mark[code] == stamp means "in the current clause"
	stamp := 0

	var queue bvaQueue
	for code := 2; code < len(count); code++ {
		if count[code] >= 2 {
			queue = append(queue, bvaQueueEntry{count: count[code], code: code})
		}
	}
	heap.Init(&queue)

	workSinceCheck := 0
	for queue.Len() > 0 {
		if expired(deadline) {
			timedOut = true
			break
		}
		if *budget <= 0 {
			break
		}
		entry := heap.Pop(&queue).(bvaQueueEntry)
		if entry.count != count[entry.code] {
			continue // stale: the count changed and a fresh entry was pushed
		}
		l := entry.code
		occurrences[l] = compactLiveOccurrences(occurrences[l], live)

		// Mcls: the clauses still in the candidate grid. Mlit: the
		// literals chosen so far (always starting with l itself).
		var grid []int
		for _, idx := range occurrences[l] {
			if len(cs[idx]) >= 2 {
				grid = append(grid, idx)
			}
		}
		if len(grid) < 2 {
			continue
		}
		chosen := []int{l}
		var partnerOf []map[int]int // partnerOf[k][c] = clause for chosen[k+1] paired with c

		abandoned := false
		for {
			pairs := make(map[int][]bvaPair)
			for _, c := range grid {
				clause := cs[c]
				stamp++
				lmin := -1
				for _, lit := range clause {
					code := literalCode(lit)
					if code == l {
						continue
					}
					mark[code] = stamp
					if lmin < 0 || count[code] < count[lmin] {
						lmin = code
					}
				}
				if lmin < 0 {
					continue // a clause of one repeated literal; nothing to pair
				}
				occurrences[lmin] = compactLiveOccurrences(occurrences[lmin], live)
				for _, d := range occurrences[lmin] {
					if d == c || len(cs[d]) != len(clause) {
						continue
					}
					*budget--
					workSinceCheck++
					if workSinceCheck >= deadlineCheckInterval {
						workSinceCheck = 0
						if expired(deadline) {
							timedOut = true
							abandoned = true
							break
						}
					}
					if *budget <= 0 {
						abandoned = true
						break
					}
					marked, other := 0, -1
					for _, lit := range cs[d] {
						code := literalCode(lit)
						if mark[code] == stamp {
							marked++
						} else {
							other = code
						}
					}
					// d must hold every literal of c except l, plus exactly
					// one other literal, which is not on l's variable.
					if marked != len(clause)-1 || other < 0 || other>>1 == l>>1 {
						continue
					}
					pairs[other] = append(pairs[other], bvaPair{c: c, d: d})
				}
				if abandoned {
					break
				}
			}
			if abandoned {
				break
			}

			best := -1
			for code, list := range pairs {
				if slicesContainsInt(chosen, code) {
					continue
				}
				if best < 0 || len(list) > len(pairs[best]) || (len(list) == len(pairs[best]) && code < best) {
					best = code
				}
			}
			if best < 0 || bvaReduction(len(chosen)+1, len(pairs[best])) <= bvaReduction(len(chosen), len(grid)) {
				break
			}
			partners := make(map[int]int, len(pairs[best]))
			grid = grid[:0]
			for _, pair := range pairs[best] {
				partners[pair.c] = pair.d
				grid = append(grid, pair.c)
			}
			chosen = append(chosen, best)
			partnerOf = append(partnerOf, partners)
		}
		if abandoned {
			break
		}
		if len(chosen) < 2 || bvaReduction(len(chosen), len(grid)) <= 0 {
			continue
		}

		// The grid can name the same clause twice (clause {a,b,r} is both
		// "a with remainder {b,r}" and "b with remainder {a,r}"), so count
		// the distinct clauses actually removed: a replacement that does
		// not strictly shrink the formula is skipped, which also rules out
		// an endless cycle of replacements.
		removedIDs := map[int]bool{}
		for _, c := range grid {
			removedIDs[c] = true
			for k := range partnerOf {
				removedIDs[partnerOf[k][c]] = true
			}
		}
		if len(removedIDs) <= len(chosen)+len(grid) {
			continue
		}
		*budget -= len(removedIDs)

		// Apply the replacement. Remove the grid first, collecting the
		// literals whose counts drop so they can be requeued.
		var touched []int
		remove := func(idx int) {
			if !live[idx] {
				return
			}
			live[idx] = false
			for _, lit := range cs[idx] {
				code := literalCode(lit)
				count[code]--
				touched = append(touched, code)
			}
		}
		for _, c := range grid {
			remove(c)
			for k := range partnerOf {
				remove(partnerOf[k][c])
			}
		}

		numVars++
		numAdded++
		occurrences = append(occurrences, nil, nil)
		count = append(count, 0, 0)
		mark = append(mark, 0, 0)
		x := cnf.Literal(numVars)
		addClause := func(clause cnf.Clause) {
			idx := len(cs)
			cs = append(cs, clause)
			live = append(live, true)
			for _, lit := range clause {
				code := literalCode(lit)
				occurrences[code] = append(occurrences[code], idx)
				count[code]++
				touched = append(touched, code)
			}
		}
		for _, code := range chosen {
			lit := cnf.Literal(code >> 1)
			if code&1 == 1 {
				lit = -lit
			}
			addClause(cnf.Clause{lit, x})
		}
		for _, c := range grid {
			// c was just marked dead but its literals are still readable.
			rest := make(cnf.Clause, 0, len(cs[c]))
			for _, lit := range cs[c] {
				if literalCode(lit) != l {
					rest = append(rest, lit)
				}
			}
			addClause(append(rest, -x))
		}

		stamp++
		for _, code := range touched {
			if mark[code] == stamp || count[code] < 2 {
				continue
			}
			mark[code] = stamp
			heap.Push(&queue, bvaQueueEntry{count: count[code], code: code})
		}
	}

	final := make([]cnf.Clause, 0, len(cs))
	for i, clause := range cs {
		if live[i] {
			final = append(final, clause)
		}
	}
	*clauses = final
	return numAdded, timedOut
}

// slicesContainsInt reports whether list contains v. The lists here
// (the literals chosen for one grid) are short, so a linear scan beats
// anything fancier.
func slicesContainsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
