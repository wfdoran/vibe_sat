// Command paramtune is STAGE39.md's auto-tuning harness (item 9,
// second half -- item 15/REPORT22.md's item 7 parameter inventory
// comes first, see docs/internal-parameters.md): it sweeps candidate
// values for vibe_sat's runtime-configurable internal parameters
// (.vibe_sat.json, see go_src/internal/params) against a chosen slice
// of benchmark/, using util/benchcompare as the actual measurement
// engine, and reports which candidate value(s) perform best.
//
// It defaults to coordinate descent -- sweeping one parameter (or one
// small, explicitly-requested joint group) at a time, carrying the
// winner forward before sweeping the next -- per
// docs/internal-parameters.md's design notes: this project's own
// tuning history (REPORT35.md's glucoseK sweep) has never found
// evidence these parameters interact strongly enough to need a full
// joint search, and a full joint grid over even a handful of
// continuous-valued parameters is combinatorially expensive against
// real benchmark instances.
//
// It lives under util/, alongside benchcompare, per PROMPT.md's
// Stage 18 carve-out for permanent side tooling that go_src/rust_src
// must not depend on.
//
// Usage (run from anywhere inside the repository):
//
//	go run . --dirs=uf20-91 --algorithm=cdcl --time-limit-secs=10 \
//	  --sweep="cdcl.glucoseK=0.5|0.6|0.7|0.9"
//
// See README.md in this directory for the full flag reference and
// --sweep's mini-language.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	if err := runMain(); err != nil {
		fmt.Fprintln(os.Stderr, "paramtune:", err)
		os.Exit(1)
	}
}

func runMain() error {
	var (
		projectRootFlag     = flag.String("project-root", "", "path to the vibe_sat repository root (default: auto-detected)")
		goBinFlag           = flag.String("go-bin", "", "path to a pre-built go vibe_sat binary (default: build one from go_src, once, before sweeping)")
		rustBinFlag         = flag.String("rust-bin", "", "path to a pre-built rust vibe_sat binary (default: build one, in release mode, from rust_src, once, before sweeping)")
		benchcompareBinFlag = flag.String("benchcompare-bin", "", "path to a pre-built benchcompare binary (default: build one from util/benchcompare, once, before sweeping)")
		sweepFlag           = flag.String("sweep", "", "the sweep to run, e.g. \"cdcl.glucoseK=0.5|0.6|0.7\"; see README.md for the full mini-language (required)")
		listParamsFlag      = flag.Bool("list-params", false, "print every known parameter path (as accepted by --sweep) and exit")
		writeResultFlag     = flag.String("write-result-dir", "", "after sweeping, write the winning configuration as .vibe_sat.json into this directory")
		dirsFlag            = flag.String("dirs", "", "comma-separated benchmark/ subdirectory names, passed through to benchcompare (at least one of --dirs/--paths is required)")
		pathsFlag           = flag.String("paths", "", "comma-separated literal directory paths, passed through to benchcompare (at least one of --dirs/--paths is required)")
		sampleFlag          = flag.Int("sample", 0, "passed through to benchcompare's --sample")
		seedFlag            = flag.Int64("seed", 1, "passed through to benchcompare's --seed")
		maxSizeMBFlag       = flag.Int("max-size-mb", 0, "passed through to benchcompare's --max-size-mb")
		algorithmFlag       = flag.String("algorithm", "", "vibe_sat --algorithm value (required)")
		algParamsFlag       = flag.String("alg-params", "", "passed through to benchcompare's --alg-params")
		timeLimitFlag       = flag.Int("time-limit-secs", 0, "passed through to benchcompare's --time-limit-secs")
		numThreadsFlag      = flag.Int("num-threads", 0, "passed through to benchcompare's --num-threads")
		noPreFlag           = flag.Bool("no-preprocessing", false, "passed through to benchcompare's --no-preprocessing")
		hardTimeoutFlag     = flag.Int("hard-timeout-secs", 0, "passed through to benchcompare's --hard-timeout-secs")
	)
	flag.Parse()

	if *listParamsFlag {
		for _, p := range allParamPaths() {
			fmt.Println(p)
		}
		return nil
	}

	if *sweepFlag == "" {
		return fmt.Errorf("--sweep is required (see README.md, or --list-params for known parameter names)")
	}
	if *dirsFlag == "" && *pathsFlag == "" {
		return fmt.Errorf("at least one of --dirs or --paths is required")
	}
	if *algorithmFlag == "" {
		return fmt.Errorf("--algorithm is required")
	}

	stages, err := parseSweepSpec(*sweepFlag)
	if err != nil {
		return err
	}

	projectRoot := *projectRootFlag
	if projectRoot == "" {
		projectRoot, err = findProjectRoot(".")
		if err != nil {
			return err
		}
	}

	tmpDir, err := os.MkdirTemp("", "paramtune-")
	if err != nil {
		return fmt.Errorf("creating scratch directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	goBin := *goBinFlag
	if goBin == "" {
		fmt.Fprintln(os.Stderr, "paramtune: building go binary...")
		if goBin, err = buildGoBinary(projectRoot, tmpDir); err != nil {
			return err
		}
	}
	rustBin := *rustBinFlag
	if rustBin == "" {
		fmt.Fprintln(os.Stderr, "paramtune: building rust binary (release)...")
		if rustBin, err = buildRustBinary(projectRoot); err != nil {
			return err
		}
	}
	benchcompareBin := *benchcompareBinFlag
	if benchcompareBin == "" {
		fmt.Fprintln(os.Stderr, "paramtune: building benchcompare...")
		if benchcompareBin, err = buildBenchcompareBinary(projectRoot, tmpDir); err != nil {
			return err
		}
	}

	cfg := sweepConfig{
		projectRoot:     projectRoot,
		benchcompareBin: benchcompareBin,
		goBin:           goBin,
		rustBin:         rustBin,
		dirs:            *dirsFlag,
		paths:           *pathsFlag,
		sample:          *sampleFlag,
		seed:            *seedFlag,
		maxSizeMB:       *maxSizeMBFlag,
		algorithm:       *algorithmFlag,
		algParams:       *algParamsFlag,
		timeLimitSecs:   *timeLimitFlag,
		numThreads:      *numThreadsFlag,
		noPreprocessing: *noPreFlag,
		hardTimeoutSecs: *hardTimeoutFlag,
	}

	totalCombos := 0
	for _, st := range stages {
		totalCombos += len(st.combinations)
	}
	fmt.Fprintf(os.Stderr, "paramtune: %d stage(s), %d candidate(s) total\n", len(stages), totalCombos)

	results, overrides, err := runSweep(cfg, stages, runCandidate, func(stageIndex, comboIndex, comboTotal int, c combination) {
		fmt.Fprintf(os.Stderr, "  stage %d, candidate %d/%d: %s\n", stageIndex+1, comboIndex+1, comboTotal, c.describe())
	})
	if err != nil {
		return err
	}

	printSweepReport(os.Stdout, results, overrides)

	if *writeResultFlag != "" {
		p := defaultParams()
		for path, value := range overrides {
			if err := setByPath(&p, path, value); err != nil {
				return fmt.Errorf("applying winning override %s=%s: %w", path, value, err)
			}
		}
		if err := writeParamsFile(*writeResultFlag, p); err != nil {
			return fmt.Errorf("writing --write-result-dir: %w", err)
		}
	}

	return nil
}
