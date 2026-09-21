package main

// Running one candidate: writing a .vibe_sat.json into a fresh
// scratch directory, then invoking a pre-built benchcompare binary
// with that scratch directory as its own process's current working
// directory -- benchcompare's run() (see util/benchcompare/runner.go)
// execs the vibe_sat binaries via exec.CommandContext with no Dir of
// its own set, so they inherit whatever directory the benchcompare
// process itself was started in. That is precisely vibe_sat's own
// "implicit .vibe_sat.json in the current directory" discovery path
// (STAGE39.md's params.Resolve) -- no --internal-params flag needed,
// and no change to benchcompare required. Since benchcompare's own
// project-root auto-detection walks upward from its process's cwd
// looking for go_src/rust_src/benchmark, and the scratch directory
// here is deliberately outside the repository, --project-root is
// always passed explicitly.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// sweepConfig holds everything about the benchmark sweep itself
// (which files, which algorithm, timing/thread flags) that stays
// fixed across every candidate -- only the .vibe_sat.json contents
// change from one benchcompare invocation to the next.
type sweepConfig struct {
	projectRoot     string
	benchcompareBin string
	goBin           string
	rustBin         string
	dirs            string
	paths           string
	sample          int
	seed            int64
	maxSizeMB       int
	algorithm       string
	algParams       string
	timeLimitSecs   int
	numThreads      int
	noPreprocessing bool
	hardTimeoutSecs int
}

// runCandidate writes p's JSON into a fresh scratch directory, runs
// benchcompare with that directory as its cwd, and returns the parsed
// per-file results.
func runCandidate(cfg sweepConfig, p vibeSatParams) ([]bcFileResult, error) {
	scratchDir, err := os.MkdirTemp("", "paramtune-candidate-")
	if err != nil {
		return nil, fmt.Errorf("creating scratch directory: %w", err)
	}
	defer os.RemoveAll(scratchDir)

	if err := writeParamsFile(scratchDir, p); err != nil {
		return nil, fmt.Errorf("writing .vibe_sat.json: %w", err)
	}

	jsonOut := filepath.Join(scratchDir, "..", filepath.Base(scratchDir)+"-report.json")
	jsonOut, err = filepath.Abs(jsonOut)
	if err != nil {
		return nil, err
	}
	defer os.Remove(jsonOut)

	args := []string{
		"--project-root=" + cfg.projectRoot,
		"--go-bin=" + cfg.goBin,
		"--rust-bin=" + cfg.rustBin,
		"--algorithm=" + cfg.algorithm,
		"--json=" + jsonOut,
		"--quiet",
	}
	if cfg.dirs != "" {
		args = append(args, "--dirs="+cfg.dirs)
	}
	if cfg.paths != "" {
		args = append(args, "--paths="+cfg.paths)
	}
	if cfg.sample > 0 {
		args = append(args, fmt.Sprintf("--sample=%d", cfg.sample))
	}
	args = append(args, fmt.Sprintf("--seed=%d", cfg.seed))
	if cfg.maxSizeMB > 0 {
		args = append(args, fmt.Sprintf("--max-size-mb=%d", cfg.maxSizeMB))
	}
	if cfg.algParams != "" {
		args = append(args, "--alg-params="+cfg.algParams)
	}
	if cfg.timeLimitSecs > 0 {
		args = append(args, fmt.Sprintf("--time-limit-secs=%d", cfg.timeLimitSecs))
	}
	if cfg.numThreads > 0 {
		args = append(args, fmt.Sprintf("--num-threads=%d", cfg.numThreads))
	}
	if cfg.noPreprocessing {
		args = append(args, "--no-preprocessing")
	}
	if cfg.hardTimeoutSecs > 0 {
		args = append(args, fmt.Sprintf("--hard-timeout-secs=%d", cfg.hardTimeoutSecs))
	}

	cmd := exec.Command(cfg.benchcompareBin, args...)
	cmd.Dir = scratchDir // the load-bearing line: gives vibe_sat's implicit .vibe_sat.json discovery something to find
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("running benchcompare: %w\n%s", err, out)
	}

	return readBenchcompareJSON(jsonOut)
}
