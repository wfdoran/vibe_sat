package cdcl

import (
	"math/rand/v2"
	"testing"
	"time"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/params"
)

// newVmtfSolver builds a VMTF solver over numVars variables with a
// single dummy clause, failing the test if the bootstrap reports UNSAT.
func newVmtfSolver(t *testing.T, numVars int) *solver {
	t.Helper()
	problem := &cnf.Problem{NumVars: numVars, Clauses: []cnf.Clause{{1, 2}}}
	s, ok, _ := newSolver(problem, nil, SelectVarVmtf, RestartNone, params.Default().CDCL, PhaseSaving, nil, time.Time{})
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	return s
}

// vmtfOrder returns the variables from the front (newest) to the back
// (oldest) of the list, checking along the way that the next/prev links
// agree with each other and that stamps strictly decrease toward the back.
func vmtfOrder(t *testing.T, s *solver) []int {
	t.Helper()
	var order []int
	prevVar := 0
	for v := s.vmtfFront; v != 0; v = s.vmtfPrev[v] {
		if len(order) > s.numVars {
			t.Fatalf("list has a cycle: %v", order)
		}
		if s.vmtfNext[v] != prevVar {
			t.Fatalf("vmtfNext[%d] = %d, want %d", v, s.vmtfNext[v], prevVar)
		}
		if prevVar != 0 && s.vmtfStamp[v] >= s.vmtfStamp[prevVar] {
			t.Fatalf("stamp[%d]=%d is not older than stamp[%d]=%d", v, s.vmtfStamp[v], prevVar, s.vmtfStamp[prevVar])
		}
		order = append(order, v)
		prevVar = v
	}
	if len(order) != s.numVars {
		t.Fatalf("list holds %d variables, want %d: %v", len(order), s.numVars, order)
	}
	if order[len(order)-1] != s.vmtfBack {
		t.Fatalf("vmtfBack = %d, want %d", s.vmtfBack, order[len(order)-1])
	}
	return order
}

// checkVmtfCursor verifies the search cursor's invariant: every
// variable newer than the cursor is assigned.
func checkVmtfCursor(t *testing.T, s *solver) {
	t.Helper()
	for v := 1; v <= s.numVars; v++ {
		if s.vmtfStamp[v] > s.vmtfStamp[s.vmtfSearch] && s.x[v] == assign.Unassigned {
			t.Fatalf("variable %d is unassigned but newer than the cursor (%d)", v, s.vmtfSearch)
		}
	}
}

func equalInts(a, b []int) bool {
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

// TestInitVmtfStartsWithLowestVariableFirst checks the initial order:
// variable 1 at the front, so decisions before any conflict walk the
// variables in increasing index order.
func TestInitVmtfStartsWithLowestVariableFirst(t *testing.T) {
	s := newVmtfSolver(t, 5)
	if got, want := vmtfOrder(t, s), []int{1, 2, 3, 4, 5}; !equalInts(got, want) {
		t.Errorf("initial order = %v, want %v", got, want)
	}
	if v := s.selectVarVmtf(); v != 1 {
		t.Errorf("first decision variable = %d, want 1", v)
	}
}

// TestVmtfBumpMovesToFront checks a bump from the middle, from the
// back, and of the variable already at the front (a no-op).
func TestVmtfBumpMovesToFront(t *testing.T) {
	s := newVmtfSolver(t, 5)
	s.x[3] = assign.True // assigned, as it is during analyze
	s.vmtfBump(3)
	if got, want := vmtfOrder(t, s), []int{3, 1, 2, 4, 5}; !equalInts(got, want) {
		t.Errorf("after bumping 3: %v, want %v", got, want)
	}
	s.x[5] = assign.True
	s.vmtfBump(5)
	if got, want := vmtfOrder(t, s), []int{5, 3, 1, 2, 4}; !equalInts(got, want) {
		t.Errorf("after bumping 5 (the back): %v, want %v", got, want)
	}
	counter := s.vmtfCounter
	s.vmtfBump(5)
	if s.vmtfCounter != counter {
		t.Errorf("bumping the front variable changed the stamp counter")
	}
	if got, want := vmtfOrder(t, s), []int{5, 3, 1, 2, 4}; !equalInts(got, want) {
		t.Errorf("after bumping the front: %v, want %v", got, want)
	}
}

// TestVmtfBumpAnalyzedKeepsRelativeOrder checks that the variables from
// one conflict keep their old relative order after being bumped
// together, wherever they started.
func TestVmtfBumpAnalyzedKeepsRelativeOrder(t *testing.T) {
	s := newVmtfSolver(t, 6) // order: 1 2 3 4 5 6 (1 newest)
	for v := 1; v <= 6; v++ {
		s.x[v] = assign.True
	}
	s.vmtfAnalyzed = []int{5, 2, 6} // listed out of order on purpose
	s.vmtfBumpAnalyzed()
	// Variable 2 was newer than 5, which was newer than 6. The oldest is
	// bumped first, so the newest ends up in front and the three keep
	// their relative order.
	if got, want := vmtfOrder(t, s), []int{2, 5, 6, 1, 3, 4}; !equalInts(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	if len(s.vmtfAnalyzed) != 0 {
		t.Errorf("vmtfAnalyzed not emptied: %v", s.vmtfAnalyzed)
	}
}

// TestSelectVarVmtfSkipsAssignedAndMovesCursor checks that decisions
// skip assigned variables and that the cursor follows them.
func TestSelectVarVmtfSkipsAssignedAndMovesCursor(t *testing.T) {
	s := newVmtfSolver(t, 5)
	s.x[1] = assign.True
	s.x[2] = assign.False
	if v := s.selectVarVmtf(); v != 3 {
		t.Errorf("selectVarVmtf() = %d, want 3", v)
	}
	if s.vmtfSearch != 3 {
		t.Errorf("cursor = %d, want 3", s.vmtfSearch)
	}
	checkVmtfCursor(t, s)
}

// TestBacktrackToRestoresVmtfCursor checks the cursor invariant across
// a decision, a bump while assigned, and a backjump: afterwards the
// next decision is the newest unassigned variable.
func TestBacktrackToRestoresVmtfCursor(t *testing.T) {
	s := newVmtfSolver(t, 6)
	s.decide(rand.New(rand.NewPCG(1, 1))) // decides variable 1, level 1
	s.decide(rand.New(rand.NewPCG(1, 1))) // decides variable 2, level 2
	s.decide(rand.New(rand.NewPCG(1, 1))) // decides variable 3, level 3
	if s.vmtfSearch != 3 {
		t.Fatalf("cursor = %d after three decisions, want 3", s.vmtfSearch)
	}
	// Bump variable 2 (assigned) to the very front, as a conflict would.
	s.vmtfBump(2)
	checkVmtfCursor(t, s)

	s.backtrackTo(1) // unassigns 2 and 3
	checkVmtfCursor(t, s)
	if v := s.selectVarVmtf(); v != 2 {
		t.Errorf("after backjump, next decision = %d, want 2 (the bumped variable)", v)
	}
	s.backtrackTo(0)
	checkVmtfCursor(t, s)
	if v := s.selectVarVmtf(); v != 2 {
		t.Errorf("after restart, next decision = %d, want 2", v)
	}
}

// TestAnalyzeBumpsVmtfVariablesToFront uses the same hand-verified
// conflict as TestAnalyzeBumpsVsidsActivity: variables 2 and 4 take
// part in it, so both must end up ahead of the untouched variable 3.
func TestAnalyzeBumpsVmtfVariablesToFront(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 4,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(2)},
			{cnf.Literal(-1), cnf.Literal(3)},
			{cnf.Literal(-1), cnf.Literal(-3)},
			{cnf.Literal(-2), cnf.Literal(4)},
			{cnf.Literal(-2), cnf.Literal(-4)},
		},
	}
	s, ok, _ := newSolver(problem, nil, SelectVarVmtf, RestartNone, params.Default().CDCL, PhaseSaving, nil, time.Time{})
	if !ok {
		t.Fatal("newSolver reported UNSAT unexpectedly")
	}
	s.currentLevel = 1
	s.trailLim = append(s.trailLim, len(s.trail))
	s.assignLiteral(cnf.Literal(-1), s.currentLevel, noReason)
	confl := s.propagate()
	if confl == noReason {
		t.Fatal("propagate() found no conflict")
	}
	s.analyze(confl)

	order := vmtfOrder(t, s)
	position := map[int]int{}
	for i, v := range order {
		position[v] = i
	}
	if position[2] > position[3] || position[4] > position[3] {
		t.Errorf("order %v: variables 2 and 4 took part in the conflict and must precede variable 3", order)
	}
}

// vmtfBruteForceSatisfiable reports whether clauses over numVars
// variables has a model, by trying every assignment.
func vmtfBruteForceSatisfiable(numVars int, clauses []cnf.Clause) bool {
	for mask := 0; mask < 1<<numVars; mask++ {
		a := assign.New(numVars)
		for v := 1; v <= numVars; v++ {
			if mask&(1<<(v-1)) != 0 {
				a[v] = assign.True
			} else {
				a[v] = assign.False
			}
		}
		ok := true
		for _, clause := range clauses {
			sat := false
			for _, lit := range clause {
				if a.LiteralIsTrue(lit) {
					sat = true
					break
				}
			}
			if !sat {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// TestRunWithVmtfAgreesWithBruteForce fuzzes VMTF (and the round-robin
// choice, which resolves to VSIDS with one thread) against exhaustive
// enumeration on small random 3-SAT formulas, including restarts, and
// checks every reported model.
func TestRunWithVmtfAgreesWithBruteForce(t *testing.T) {
	rng := rand.New(rand.NewPCG(64, 64))
	for _, variant := range []SelectVarVariant{SelectVarVmtf, SelectVarRoundRobin} {
		for trial := 0; trial < 150; trial++ {
			numVars := 8 + rng.IntN(5)
			numClauses := int(4.3 * float64(numVars))
			var clauses []cnf.Clause
			for len(clauses) < numClauses {
				var clause cnf.Clause
				used := map[int]bool{}
				for len(clause) < 3 {
					v := 1 + rng.IntN(numVars)
					if used[v] {
						continue
					}
					used[v] = true
					lit := cnf.Literal(v)
					if rng.IntN(2) == 0 {
						lit = -lit
					}
					clause = append(clause, lit)
				}
				clauses = append(clauses, clause)
			}
			problem := &cnf.Problem{NumVars: numVars, Clauses: clauses}
			want := vmtfBruteForceSatisfiable(numVars, clauses)
			result := Run(problem, nil, variant, RestartGeometric, nil, rand.New(rand.NewPCG(uint64(trial), 7)), 0, params.Default().CDCL, PhaseSaving)
			if result.TimedOut || result.Satisfiable != want {
				t.Fatalf("variant %d trial %d: Run satisfiable=%v timedOut=%v, brute force says %v\n%v", variant, trial, result.Satisfiable, result.TimedOut, want, clauses)
			}
			if result.Satisfiable {
				for ci, clause := range clauses {
					sat := false
					for _, lit := range clause {
						if result.Assignment.LiteralIsTrue(lit) {
							sat = true
						}
					}
					if !sat {
						t.Fatalf("variant %d trial %d: clause %d (%v) not satisfied", variant, trial, ci, clause)
					}
				}
			}
		}
	}
}

// TestRunWithVmtfProvesUnsatisfiablePigeonhole checks VMTF on a
// formula that needs many conflicts, with restarts.
func TestRunWithVmtfProvesUnsatisfiablePigeonhole(t *testing.T) {
	problem := pigeonholeProblem(6, 5)
	for _, strategy := range []RestartStrategy{RestartNone, RestartPolynomial} {
		rng := rand.New(rand.NewPCG(9, 9))
		result := Run(problem, nil, SelectVarVmtf, strategy, nil, rng, 0, params.Default().CDCL, PhaseSaving)
		if result.Satisfiable || result.TimedOut {
			t.Fatalf("restart %d: satisfiable=%v timedOut=%v, want a proven UNSAT", strategy, result.Satisfiable, result.TimedOut)
		}
	}
}

// TestResolveSelectVarVariant checks the round-robin mapping and that a
// concrete heuristic passes through unchanged.
func TestResolveSelectVarVariant(t *testing.T) {
	want := []SelectVarVariant{SelectVarVsids, SelectVarVmtf, SelectVarLrb, SelectVarVsids, SelectVarVmtf, SelectVarVsids, SelectVarVmtf}
	for i, w := range want {
		if got := resolveSelectVarVariant(SelectVarRoundRobin, i); got != w {
			t.Errorf("worker %d: got %d, want %d", i, got, w)
		}
	}
	for _, v := range []SelectVarVariant{SelectVarWeighted, SelectVarFast, SelectVarVsids, SelectVarLrb, SelectVarVmtf} {
		if got := resolveSelectVarVariant(v, 3); got != v {
			t.Errorf("resolveSelectVarVariant(%d, 3) = %d, want it unchanged", v, got)
		}
	}
	if len(roundRobinVariants)%4 == 0 || len(roundRobinVariants)%3 == 0 {
		t.Errorf("round-robin period %d must be coprime to the restart (4) and phase (3) periods", len(roundRobinVariants))
	}
}

// TestRunParallelWithRoundRobinHeuristics runs every worker kind (five
// threads cover VSIDS, VMTF and LRB) on an UNSAT and a SAT formula.
func TestRunParallelWithRoundRobinHeuristics(t *testing.T) {
	unsat := pigeonholeProblem(6, 5)
	result := RunParallel(unsat, nil, SelectVarRoundRobin, RestartRoundRobin, nil, 5, rand.New(rand.NewPCG(3, 3)), 0, params.Default().CDCL, PhaseRoundRobin)
	if result.Satisfiable || result.TimedOut {
		t.Fatalf("pigeonhole: satisfiable=%v timedOut=%v, want a proven UNSAT", result.Satisfiable, result.TimedOut)
	}

	sat := pigeonholeProblem(5, 5)
	result = RunParallel(sat, nil, SelectVarRoundRobin, RestartRoundRobin, nil, 5, rand.New(rand.NewPCG(4, 4)), 0, params.Default().CDCL, PhaseRoundRobin)
	if !result.Satisfiable {
		t.Fatalf("5 pigeons in 5 holes reported unsatisfiable")
	}
	for ci, clause := range sat.Clauses {
		ok := false
		for _, lit := range clause {
			if result.Assignment.LiteralIsTrue(lit) {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("clause %d (%v) not satisfied", ci, clause)
		}
	}
}

// benchmarkDecisionStep measures one variable-selection call, on a
// solver with numVars variables, as a descent assigns them one at a time
// (the pattern a search follows between conflicts). It compares VSIDS's
// linear scan, which looks at every variable, with VMTF's cursor walk.
func benchmarkDecisionStep(b *testing.B, numVars int, vmtf bool) {
	problem := &cnf.Problem{NumVars: numVars, Clauses: []cnf.Clause{{1, 2}}}
	variant := SelectVarVsids
	if vmtf {
		variant = SelectVarVmtf
	}
	s, ok, _ := newSolver(problem, nil, variant, RestartNone, params.Default().CDCL, PhaseSaving, nil, time.Time{})
	if !ok {
		b.Fatal("newSolver reported UNSAT unexpectedly")
	}
	b.ResetTimer()
	assigned := 0
	for i := 0; i < b.N; i++ {
		var v int
		if vmtf {
			v = s.selectVarVmtf()
		} else {
			v = s.selectVarByActivity(s.varActivity)
		}
		s.x[v] = assign.True
		assigned++
		if assigned == numVars {
			// Everything is assigned: undo it all, as a restart would.
			b.StopTimer()
			for u := 1; u <= numVars; u++ {
				s.x[u] = assign.Unassigned
			}
			s.vmtfSearch = s.vmtfFront
			assigned = 0
			b.StartTimer()
		}
	}
}

func BenchmarkDecisionVsids1k(b *testing.B)  { benchmarkDecisionStep(b, 1_000, false) }
func BenchmarkDecisionVmtf1k(b *testing.B)   { benchmarkDecisionStep(b, 1_000, true) }
func BenchmarkDecisionVsids10k(b *testing.B) { benchmarkDecisionStep(b, 10_000, false) }
func BenchmarkDecisionVmtf10k(b *testing.B)  { benchmarkDecisionStep(b, 10_000, true) }
func BenchmarkDecisionVsids50k(b *testing.B) { benchmarkDecisionStep(b, 50_000, false) }
func BenchmarkDecisionVmtf50k(b *testing.B)  { benchmarkDecisionStep(b, 50_000, true) }
