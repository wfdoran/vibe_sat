package hillclimb

import (
	"math/rand/v2"
	"path/filepath"
	"testing"

	"vibe_sat/internal/cnf"
	"vibe_sat/internal/occurrence"
)

// STAGE24.md: extending REPORT23.md's cdcl-only profiling-benchmark
// infrastructure to hc/ws as well. Ordinary Go benchmarks, skipped by
// a plain "go test", only run with an explicit "-bench" flag.
//
// benchmarkProblem loads a representative uf100-430 instance -- large
// enough for the incremental flip/score bookkeeping (Stage 2's
// "Tilt") to do real, repeated work per start, but small enough that
// a fixed, generous number of starts finishes in a benchmarking-
// friendly amount of time. Unlike dfs/cdcl's hard-UNSAT instances,
// hc/ws have no natural "how much work did this actually take" stopping
// point of their own (they are incomplete searches -- see STAGE2.md/
// STAGE4.md), so the workload here is fixed by NumStarts/NumTries
// instead of by the instance's inherent difficulty.
func benchmarkProblem(b *testing.B) (*cnf.Problem, *occurrence.Lists) {
	b.Helper()
	path := filepath.Join("..", "..", "..", "benchmark", "uf100-430", "uf100-01.cnf")
	problem, err := cnf.ReadDIMACS(path, 0)
	if err != nil {
		b.Fatalf("failed to read benchmark CNF file %s: %v", path, err)
	}
	return problem, occurrence.Build(problem)
}

// benchNumStarts/benchNumTries are deliberately generous: per
// REPORT4.md, plain hc rarely solves uf100-430 within a modest flip
// budget at all, so this benchmark's point is to exercise a fixed,
// reproducible amount of flip/score work, not to reach a solution.
const benchNumStarts = 200

// BenchmarkHillClimbSingleThreaded profiles the Stage 2 hill-climb's
// per-flip incremental scoring in isolation.
func BenchmarkHillClimbSingleThreaded(b *testing.B) {
	problem, lists := benchmarkProblem(b)
	numStarts := benchNumStarts
	for i := 0; i < b.N; i++ {
		rng := rand.New(rand.NewPCG(1, 2))
		Run(problem, lists, Params{NumStarts: &numStarts}, rng, 0)
	}
}

// BenchmarkHillClimbParallel8 profiles an 8-worker run: the Stage 17
// shared best-score CAS and per-worker RNG derivation are now live.
func BenchmarkHillClimbParallel8(b *testing.B) {
	problem, lists := benchmarkProblem(b)
	numStarts := benchNumStarts
	for i := 0; i < b.N; i++ {
		rng := rand.New(rand.NewPCG(1, 2))
		RunParallel(problem, lists, Params{NumStarts: &numStarts}, 8, rng, 0)
	}
}

// BenchmarkWalkSatSingleThreaded profiles WalkSAT's break-count
// computation and unsatisfied-clause-set bookkeeping (Stage 4) in
// isolation.
func BenchmarkWalkSatSingleThreaded(b *testing.B) {
	problem, lists := benchmarkProblem(b)
	numTries := benchNumStarts
	params := WalkSatParams{NumTries: &numTries, MaxFlipsPerTry: DefaultMaxFlipsPerTry, NoisePercent: DefaultNoisePercent}
	for i := 0; i < b.N; i++ {
		rng := rand.New(rand.NewPCG(1, 2))
		RunWalkSat(problem, lists, params, rng, 0)
	}
}

// BenchmarkWalkSatParallel8 profiles an 8-worker WalkSAT run.
func BenchmarkWalkSatParallel8(b *testing.B) {
	problem, lists := benchmarkProblem(b)
	numTries := benchNumStarts
	params := WalkSatParams{NumTries: &numTries, MaxFlipsPerTry: DefaultMaxFlipsPerTry, NoisePercent: DefaultNoisePercent}
	for i := 0; i < b.N; i++ {
		rng := rand.New(rand.NewPCG(1, 2))
		RunWalkSatParallel(problem, lists, params, 8, rng, 0)
	}
}
