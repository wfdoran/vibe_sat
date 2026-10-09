package preprocess

import (
	"math/rand/v2"
	"testing"
	"time"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/params"
)

// bvaHugeBudget returns a work budget large enough never to bind.
func bvaHugeBudget() *int {
	b := 1 << 40
	return &b
}

// assignmentFromMask builds a complete assignment of variables 1..n
// where bit v-1 of mask gives variable v's value.
func assignmentFromMask(n, mask int) assign.Assignment {
	a := assign.New(n)
	for v := 1; v <= n; v++ {
		if mask&(1<<(v-1)) != 0 {
			a[v] = assign.True
		} else {
			a[v] = assign.False
		}
	}
	return a
}

// satisfiableOverOriginal reports, for each assignment of the original
// variables 1..oldVars, whether the clause set (which may use up to
// totalVars variables, the extras being existentially quantified) has a
// satisfying extension. It returns a slice indexed by the old-variable
// mask.
func satisfiableOverOriginal(clauses []cnf.Clause, oldVars, totalVars int) []bool {
	result := make([]bool, 1<<oldVars)
	for mask := 0; mask < 1<<totalVars; mask++ {
		if allSatisfied(clauses, assignmentFromMask(totalVars, mask)) {
			result[mask&(1<<oldVars-1)] = true
		}
	}
	return result
}

// checkBVAEquivalent verifies the defining property of Bounded Variable
// Addition: an assignment of the original variables satisfies the
// original formula exactly when it extends to a model of the new one.
func checkBVAEquivalent(t *testing.T, original, rewritten []cnf.Clause, oldVars, newVars int) {
	t.Helper()
	before := satisfiableOverOriginal(original, oldVars, oldVars)
	after := satisfiableOverOriginal(rewritten, oldVars, newVars)
	for mask := range before {
		if before[mask] != after[mask] {
			t.Fatalf("assignment %b: original satisfied=%v, rewritten extendable=%v\noriginal: %v\nrewritten: %v",
				mask, before[mask], after[mask], original, rewritten)
		}
	}
}

// pairwiseAtMostOne returns the clauses (NOT a OR NOT b) for every pair
// of variables 1..n, the classic target of Bounded Variable Addition.
func pairwiseAtMostOne(n int) []cnf.Clause {
	var clauses []cnf.Clause
	for i := 1; i <= n; i++ {
		for j := i + 1; j <= n; j++ {
			clauses = append(clauses, cnf.Clause{cnf.Literal(-i), cnf.Literal(-j)})
		}
	}
	return clauses
}

// TestBVAReencodesPairwiseAtMostOne checks that a pairwise at-most-one
// constraint over 8 variables (28 clauses) is reencoded with fewer
// clauses and one or more added variables, and stays equivalent.
func TestBVAReencodesPairwiseAtMostOne(t *testing.T) {
	original := pairwiseAtMostOne(8)
	clauses := cloneClauses(original)

	added, timedOut := addBoundedVariables(&clauses, 8, bvaHugeBudget(), time.Time{})
	if timedOut {
		t.Fatalf("timedOut with no deadline")
	}
	if added == 0 {
		t.Fatalf("expected BVA to add at least one variable")
	}
	if len(clauses) >= len(original) {
		t.Errorf("clause count %d, want fewer than %d", len(clauses), len(original))
	}
	checkBVAEquivalent(t, original, clauses, 8, 8+added)
}

// TestBVAGridIsReplacedByTwoSets checks the textbook case exactly: the
// 3x3 grid (a OR C_j) for a in {1,2,3} and C_j in {4,5,6} (9 clauses)
// becomes 3+3 clauses over one new variable.
func TestBVAGridIsReplacedByTwoSets(t *testing.T) {
	var original []cnf.Clause
	for a := 1; a <= 3; a++ {
		for c := 4; c <= 6; c++ {
			original = append(original, cnf.Clause{cnf.Literal(a), cnf.Literal(c)})
		}
	}
	clauses := cloneClauses(original)
	added, _ := addBoundedVariables(&clauses, 6, bvaHugeBudget(), time.Time{})
	if added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	if len(clauses) != 6 {
		t.Errorf("clauses = %d (%v), want 6", len(clauses), clauses)
	}
	checkBVAEquivalent(t, original, clauses, 6, 7)
}

// TestBVALeavesFormulaWithoutPatternAlone checks that a formula with no
// shared structure is returned untouched.
func TestBVALeavesFormulaWithoutPatternAlone(t *testing.T) {
	original := []cnf.Clause{{1, 2, 3}, {-1, 4, 5}, {-2, -4, 6}, {3, -5, -6}}
	clauses := cloneClauses(original)
	added, _ := addBoundedVariables(&clauses, 6, bvaHugeBudget(), time.Time{})
	if added != 0 || len(clauses) != len(original) {
		t.Errorf("added=%d clauses=%d, want 0 and %d", added, len(clauses), len(original))
	}
}

// TestBVAZeroBudgetDoesNothing checks that an exhausted budget leaves
// the formula alone and does not report a timeout.
func TestBVAZeroBudgetDoesNothing(t *testing.T) {
	original := pairwiseAtMostOne(8)
	clauses := cloneClauses(original)
	zero := 0
	added, timedOut := addBoundedVariables(&clauses, 8, &zero, time.Time{})
	if added != 0 || timedOut || len(clauses) != len(original) {
		t.Errorf("added=%d timedOut=%v clauses=%d, want no change", added, timedOut, len(clauses))
	}
}

// TestBVAExpiredDeadlineDoesNothing checks that an already-passed
// deadline stops BVA before it changes anything and reports timedOut.
func TestBVAExpiredDeadlineDoesNothing(t *testing.T) {
	original := pairwiseAtMostOne(8)
	clauses := cloneClauses(original)
	added, timedOut := addBoundedVariables(&clauses, 8, bvaHugeBudget(), time.Now().Add(-time.Second))
	if added != 0 || !timedOut || len(clauses) != len(original) {
		t.Errorf("added=%d timedOut=%v clauses=%d, want no change and timedOut", added, timedOut, len(clauses))
	}
}

// randomGridFormula builds a random formula over 8 variables made of a
// product grid (literals over variables 1..4 each paired with several
// remainders over variables 5..8), plus a few unrelated random clauses.
func randomGridFormula(rng *rand.Rand) []cnf.Clause {
	randomLit := func(lo, hi int) cnf.Literal {
		v := lo + rng.IntN(hi-lo+1)
		if rng.IntN(2) == 0 {
			return cnf.Literal(-v)
		}
		return cnf.Literal(v)
	}
	var heads []cnf.Literal
	for len(heads) < 2+rng.IntN(3) {
		lit := randomLit(1, 4)
		dup := false
		for _, h := range heads {
			if h.Var() == lit.Var() {
				dup = true
			}
		}
		if !dup {
			heads = append(heads, lit)
		}
	}
	var clauses []cnf.Clause
	for r := 0; r < 3+rng.IntN(3); r++ {
		a, b := randomLit(5, 8), randomLit(5, 8)
		if a.Var() == b.Var() {
			continue
		}
		for _, h := range heads {
			clauses = append(clauses, cnf.Clause{h, a, b})
		}
	}
	for k := 0; k < rng.IntN(5); k++ {
		x, y, z := randomLit(1, 8), randomLit(1, 8), randomLit(1, 8)
		if x.Var() == y.Var() || y.Var() == z.Var() || x.Var() == z.Var() {
			continue
		}
		clauses = append(clauses, cnf.Clause{x, y, z})
	}
	return clauses
}

// TestBVAPreservesSatisfiabilityOnRandomGrids fuzzes
// addBoundedVariables against exhaustive enumeration: on every random
// formula, the rewritten clauses must be equivalent over the original
// variables.
func TestBVAPreservesSatisfiabilityOnRandomGrids(t *testing.T) {
	rng := rand.New(rand.NewPCG(61, 61))
	rewrittenAtLeastOnce := 0
	for trial := 0; trial < 300; trial++ {
		original := randomGridFormula(rng)
		clauses := cloneClauses(original)
		added, _ := addBoundedVariables(&clauses, 8, bvaHugeBudget(), time.Time{})
		if added > 0 {
			rewrittenAtLeastOnce++
		}
		if added > 6 {
			t.Fatalf("trial %d: %d added variables is more than this test can enumerate", trial, added)
		}
		checkBVAEquivalent(t, original, clauses, 8, 8+added)
	}
	if rewrittenAtLeastOnce < 50 {
		t.Errorf("only %d of 300 random formulas were rewritten; the generator is not exercising BVA", rewrittenAtLeastOnce)
	}
}

// TestRunWithBVAReconstructsCorrectly runs the whole pipeline on a
// satisfiable formula BVA rewrites, then reconstructs every model of
// the reduced problem and checks it satisfies the original.
func TestRunWithBVAReconstructsCorrectly(t *testing.T) {
	// "at most one of 1..7" plus (1 OR 2 OR ... OR 7) forces exactly one.
	original := pairwiseAtMostOne(7)
	original = append(original, cnf.Clause{1, 2, 3, 4, 5, 6, 7})
	problem := &cnf.Problem{NumVars: 7, Clauses: original}

	// Variable elimination would remove these low-degree variables before
	// BVA ever runs, so turn it off to hand BVA the formula intact.
	pp := params.Default().Preprocess
	pp.BVAWorkBudgetFactor = 100
	pp.BVEWorkBudgetFactor = 0
	result := Run(problem, 0, 1, pp, time.Time{})
	if result.Unsat {
		t.Fatalf("formula is satisfiable")
	}
	if result.Stats.VariablesAdded == 0 {
		t.Fatalf("expected BVA to add a variable")
	}
	n := result.Problem.NumVars
	if n > 18 {
		t.Fatalf("reduced problem has %d variables, too many to enumerate", n)
	}
	models := 0
	for mask := 0; mask < 1<<n; mask++ {
		solution := assignmentFromMask(n, mask)
		if !allSatisfied(result.Problem.Clauses, solution) {
			continue
		}
		models++
		allClausesSatisfied(t, problem, result.Reconstruct(solution))
	}
	if models == 0 {
		t.Fatalf("reduced problem has no models, but the original is satisfiable")
	}
}

// TestRunWithBVAZeroFactorAddsNothing checks that a factor of 0 turns
// BVA off.
func TestRunWithBVAZeroFactorAddsNothing(t *testing.T) {
	problem := &cnf.Problem{NumVars: 8, Clauses: pairwiseAtMostOne(8)}
	pp := params.Default().Preprocess
	pp.BVAWorkBudgetFactor = 0
	result := Run(problem, 0, 1, pp, time.Time{})
	if result.Stats.VariablesAdded != 0 {
		t.Errorf("VariablesAdded = %d, want 0", result.Stats.VariablesAdded)
	}
}
