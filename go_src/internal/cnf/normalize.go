package cnf

// Normalize rewrites p in place so that no clause is a tautology and no
// clause repeats a literal: every clause containing both x and -x is
// dropped (it is satisfied by every assignment, so removing it changes
// no verdict), and every repeated occurrence of the same literal within
// a clause is removed, keeping the first. An empty clause is left
// alone -- it is what makes a formula unsatisfiable.
//
// The solvers downstream assume this shape: a tautological clause
// makes variable elimination self-resolve incorrectly, and a clause
// that repeats a literal (e.g. "x x") is really a unit clause but looks
// like it has two distinct watchable literals to the watch-selection
// code. Reads each literal once, using a per-variable mark array, so it
// is O(total literals). Returns how many clauses were dropped as
// tautologies and how many duplicate literal occurrences were removed
// from the clauses that were kept.
func Normalize(p *Problem) (tautologies, duplicateLiterals int) {
	mark := make([]int8, p.NumVars+1) // 0 = unseen in this clause, 1 = seen positive, -1 = seen negative
	original := p.Clauses
	kept := original[:0]
	for _, clause := range original {
		out := clause[:0]
		tautology := false
		dups := 0
		for _, lit := range clause {
			v := lit.Var()
			sign := int8(1)
			if lit.IsNegative() {
				sign = -1
			}
			if mark[v] == sign {
				dups++
				continue
			}
			if mark[v] == -sign {
				tautology = true
				break
			}
			mark[v] = sign
			out = append(out, lit)
		}
		for _, lit := range out {
			mark[lit.Var()] = 0
		}
		if tautology {
			tautologies++
			continue
		}
		duplicateLiterals += dups
		kept = append(kept, out)
	}
	for i := len(kept); i < len(original); i++ {
		original[i] = nil
	}
	p.Clauses = kept
	return tautologies, duplicateLiterals
}
