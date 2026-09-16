package dfs

import (
	"math/rand/v2"
	"path/filepath"
	"testing"

	"vibe_sat/internal/cnf"
	"vibe_sat/internal/occurrence"
)

// STAGE24.md: extending the profiling-benchmark infrastructure
// REPORT23.md introduced for internal/cdcl to the other three
// algorithm packages, closing the gap REPORT23.md's own closing note
// flagged ("it only covers cdcl so far"). These are ordinary Go
// benchmarks, skipped by a plain "go test" and only run with an
// explicit "-bench" flag.
//
// hardBenchmarkProblem loads benchmark/uuf175-753/uuf175-083.cnf, the
// instance REPORT19.md identified as the hardest of that directory's
// 100 files (a few seconds single-threaded) while still small enough
// to actually finish proving UNSAT within a reasonable benchmarking
// budget -- REPORT18.md's own uuf250-1065 attempts never finished at
// all, which would make -benchtime=1x still take unpredictably long.
//
// Typical invocation, from go_src:
//
//	go test ./internal/dfs/ -bench=BenchmarkRunHard -benchtime=1x \
//	  -cpuprofile=/tmp/dfs_cpu.prof
//	go tool pprof -top /tmp/dfs_cpu.prof
func hardBenchmarkProblem(b *testing.B) (*cnf.Problem, *occurrence.Lists) {
	b.Helper()
	path := filepath.Join("..", "..", "..", "benchmark", "uuf175-753", "uuf175-083.cnf")
	problem, err := cnf.ReadDIMACS(path, 0)
	if err != nil {
		b.Fatalf("failed to read benchmark CNF file %s: %v", path, err)
	}
	return problem, occurrence.Build(problem)
}

// BenchmarkRunHardSingleThreaded profiles the single-threaded search
// (watched-literal BCP, the STAGE5.md weighted SelectVar, and the
// Stage 9 watch-state cloning per branch) with no threading machinery
// reachable at all.
func BenchmarkRunHardSingleThreaded(b *testing.B) {
	problem, lists := hardBenchmarkProblem(b)
	for i := 0; i < b.N; i++ {
		rng := rand.New(rand.NewPCG(1, 2))
		result := Run(problem, lists, nil, SelectVarWeighted, rng, 0)
		if result.Satisfiable {
			b.Fatal("expected unsatisfiable")
		}
	}
}

// BenchmarkRunHardParallel8 profiles an 8-worker run -- the Stage 18
// BFS-seeding and work-stealing deque are now live for the whole run,
// not just briefly exercised in a unit test.
func BenchmarkRunHardParallel8(b *testing.B) {
	problem, lists := hardBenchmarkProblem(b)
	for i := 0; i < b.N; i++ {
		rng := rand.New(rand.NewPCG(1, 2))
		result := RunParallel(problem, lists, nil, SelectVarWeighted, 8, rng, 0)
		if result.Satisfiable {
			b.Fatal("expected unsatisfiable")
		}
	}
}
