package preprocess

import (
	"path/filepath"
	"testing"

	"vibe_sat/internal/cnf"
)

// STAGE24.md: extending REPORT23.md's cdcl-only profiling-benchmark
// infrastructure to preprocessing as well. An ordinary Go benchmark,
// skipped by a plain "go test", only run with an explicit "-bench"
// flag.
//
// This loads benchmark/blocksworld/bw_large.c.cnf, the exact instance
// REPORT21.md's "Open questions" flagged as spending ~11 seconds in
// this package's Run alone (confirmed there via --no-preprocessing:
// 11s -> 34ms) -- REPORT22.md's item 2 ("parallelize preprocessing")
// named this as real, measured evidence that this package, not any
// solving algorithm, is the bottleneck on some real instances. This
// benchmark exists so a future stage that pursues that item has a
// profile to start from instead of re-deriving one from scratch.
//
// Typical invocation, from go_src:
//
//	go test ./internal/preprocess/ -bench=BenchmarkRunHard -benchtime=1x \
//	  -cpuprofile=/tmp/preprocess_cpu.prof
//	go tool pprof -top /tmp/preprocess_cpu.prof
func BenchmarkRunHard(b *testing.B) {
	path := filepath.Join("..", "..", "..", "benchmark", "blocksworld", "bw_large.c.cnf")
	problem, err := cnf.ReadDIMACS(path, 0)
	if err != nil {
		b.Fatalf("failed to read benchmark CNF file %s: %v", path, err)
	}
	for i := 0; i < b.N; i++ {
		Run(problem, 0)
	}
}
