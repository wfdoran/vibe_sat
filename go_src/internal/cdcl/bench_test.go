package cdcl

import (
	"math/rand/v2"
	"path/filepath"
	"testing"

	"vibe_sat/internal/cnf"
	"vibe_sat/internal/params"
)

// STAGE23.md: profiling harness for cdcl. These are ordinary Go
// benchmarks (skipped by a plain "go test", only run with
// "go test -bench=..."), kept as permanent, reusable infrastructure
// rather than a one-off throwaway script -- reports/REPORT22.md's
// item 18 (a standing benchmark harness) gets a first real instance
// here, even though that wasn't this stage's own goal.
//
// Each benchmark loads a real, genuinely hard uuf250-1065 instance
// (see reports/REPORT21.md's benchmark section: this file alone took
// 23.7s/21.3s single-threaded in Go/Rust) directly, skipping Stage
// 8 preprocessing deliberately -- STAGE23.md asks to profile "the
// CDCL algorithm" specifically, and preprocessing is negligible on
// this instance size anyway (confirmed manually: comfortably under a
// millisecond), so there is nothing to gain and something to lose
// (an extra, irrelevant frame in every profile) by routing through
// it here.
//
// Typical invocation, from go_src:
//
//	go test ./internal/cdcl/ -bench=BenchmarkRunHard -benchtime=1x \
//	  -cpuprofile=/tmp/cdcl_cpu.prof -memprofile=/tmp/cdcl_mem.prof
//	go tool pprof -top /tmp/cdcl_cpu.prof
//
// -benchtime=1x is important: Go's default benchmarking behavior
// re-runs a benchmark function enough times to get a stable timing
// measurement, which for a ~20+ second search would otherwise turn
// one profiling run into several minutes.
func hardBenchmarkProblem(b *testing.B) *cnf.Problem {
	b.Helper()
	path := filepath.Join("..", "..", "..", "benchmark", "uuf250-1065", "uuf250-01.cnf")
	problem, err := cnf.ReadDIMACS(path, 0)
	if err != nil {
		b.Fatalf("failed to read benchmark CNF file %s: %v", path, err)
	}
	return problem
}

// BenchmarkRunHardSingleThreaded profiles the single-threaded search
// loop in isolation -- no clause-sharing code is reachable at all
// here, so any hot spot found is in the base algorithm (propagate,
// analyze, decide, restart bookkeeping), not the Stage 20/21 additions.
func BenchmarkRunHardSingleThreaded(b *testing.B) {
	problem := hardBenchmarkProblem(b)
	for i := 0; i < b.N; i++ {
		rng := rand.New(rand.NewPCG(1, 2))
		result := Run(problem, nil, SelectVarVsids, RestartPolynomial, nil, rng, 0, params.Default().CDCL)
		if result.Satisfiable {
			b.Fatal("expected unsatisfiable")
		}
	}
}

// BenchmarkRunHardParallel8 profiles an 8-worker run (this machine's
// core count in prior stages' benchmarks; see REPORT21.md) -- the
// clause-sharing export/import path (clauseshare.go) is now live and
// under real, sustained pressure for the whole run, not just
// exercised briefly in a unit test.
func BenchmarkRunHardParallel8(b *testing.B) {
	problem := hardBenchmarkProblem(b)
	for i := 0; i < b.N; i++ {
		rng := rand.New(rand.NewPCG(1, 2))
		result := RunParallel(problem, nil, SelectVarVsids, RestartRoundRobin, nil, 8, rng, 0, params.Default().CDCL)
		if result.Satisfiable {
			b.Fatal("expected unsatisfiable")
		}
	}
}

// BenchmarkRunHardParallel32 deliberately oversubscribes this
// machine's 10 cores, per STAGE23.md's specific request to look
// closely at clause-database contention: if the export/import path
// has a real scaling problem, running well past the physical core
// count is where its relative cost (as a fraction of total CPU time)
// should show up most clearly, even though wall-clock time itself is
// expected to be worse than at 8 threads (REPORT21.md already found
// 16 threads regressing vs. 8 on this same instance).
func BenchmarkRunHardParallel32(b *testing.B) {
	problem := hardBenchmarkProblem(b)
	for i := 0; i < b.N; i++ {
		rng := rand.New(rand.NewPCG(1, 2))
		result := RunParallel(problem, nil, SelectVarVsids, RestartRoundRobin, nil, 32, rng, 0, params.Default().CDCL)
		if result.Satisfiable {
			b.Fatal("expected unsatisfiable")
		}
	}
}
