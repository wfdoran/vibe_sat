// Command benchcompare is a standing (not thrown-away) harness that
// runs the Go and Rust vibe_sat binaries side by side across a
// chosen slice of benchmark/ and reports whether they agree, whether
// every reported SAT solution independently checks out, and how their
// wall-clock time compares.
//
// This replaces the one-off Python/shell comparison scripts every
// stage from REPORT8.md through REPORT23.md built, used once, and
// discarded (never committed, per PROMPT.md's rule that only go_src,
// rust_src, and benchmark may be written to) -- REPORT22.md's item 18
// and REPORT23.md's own closing note both flagged that gap explicitly.
// It lives under util/, per PROMPT.md's Stage 18 carve-out for
// permanent side tooling that go_src/rust_src must not depend on (and,
// being a separate Go module in a different directory, structurally
// cannot import their "internal" packages even by accident).
//
// It is written once, in Go, rather than mirrored in both languages
// the way util/termination and util/clausesharing are: those existed
// to validate an algorithm/data-structure choice that mattered
// separately to each language's own implementation. This is a driver
// that shells out to two already-built binaries; there is no
// per-language design question for a second implementation to
// answer, only maintenance cost for no benefit.
//
// Usage (run from anywhere inside the repository; project root is
// auto-detected):
//
//	go run ./util/benchcompare \
//	  --dirs=uf250-1065,uuf250-1065 --algorithm=cdcl \
//	  --sample=20 --time-limit-secs=30
//
// See README.md in this directory for the full flag reference.
package main

import (
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func main() {
	if err := runMain(); err != nil {
		fmt.Fprintln(os.Stderr, "benchcompare:", err)
		os.Exit(1)
	}
}

// runMain does all of the real work; main only exists to give it a
// single place to report an error and set the process exit code from.
func runMain() error {
	var (
		projectRootFlag = flag.String("project-root", "", "path to the vibe_sat repository root (default: auto-detected by walking up from the current directory)")
		goBinFlag       = flag.String("go-bin", "", "path to a pre-built go vibe_sat binary (default: build one from go_src)")
		rustBinFlag     = flag.String("rust-bin", "", "path to a pre-built rust vibe_sat binary (default: build one, in release mode, from rust_src)")
		dirsFlag        = flag.String("dirs", "", "comma-separated benchmark/ subdirectory names to draw .cnf files from, e.g. uf250-1065,uuf250-1065 (at least one of --dirs/--paths is required)")
		pathsFlag       = flag.String("paths", "", "comma-separated literal directory paths (absolute, or relative to the current directory) to draw .cnf files from -- for a local-only benchmark set that isn't under benchmark/ at all, e.g. --paths=../../sat_comp/2018 (at least one of --dirs/--paths is required)")
		sampleFlag      = flag.Int("sample", 0, "sample at most this many files per --dirs/--paths entry (0 = use every file)")
		seedFlag        = flag.Int64("seed", 1, "seed for --sample's file selection, for a reproducible sweep across runs")
		maxSizeMBFlag   = flag.Int("max-size-mb", 0, "exclude any .cnf file larger than this many megabytes (0 = no limit) -- see README.md for why a large, unfamiliar benchmark set should usually set this")
		algorithmFlag   = flag.String("algorithm", "", "vibe_sat --algorithm value: hc, ws, dfs, or cdcl (required)")
		algParamsFlag   = flag.String("alg-params", "", "space-separated --alg-params values, passed through verbatim, e.g. \"2 4\"")
		timeLimitFlag   = flag.Int("time-limit-secs", 0, "vibe_sat --time-limit-secs value (0 = omit the flag entirely)")
		numThreadsFlag  = flag.Int("num-threads", 0, "vibe_sat --num-threads value (0 = omit the flag entirely, i.e. vibe_sat's own default of 1)")
		noPreFlag       = flag.Bool("no-preprocessing", false, "pass vibe_sat's --no-preprocessing flag")
		hardTimeoutFlag = flag.Int("hard-timeout-secs", 0, "kill a single run after this many seconds regardless of --time-limit-secs (0 = auto: time-limit+30s, or 120s if no time limit was given)")
		jsonFlag        = flag.String("json", "", "write full per-file results as JSON to this path, in addition to the summary printed to stdout")
		quietFlag       = flag.Bool("quiet", false, "suppress the one-line-per-file progress output")
		failOnIssues    = flag.Bool("fail-on-issues", false, "STAGE37.md: exit with a non-zero status if any file was flagged (a verification failure, a crash, a cross-language mismatch, or a definite verdict either binary failed to reach at all) -- off by default, since an interactive run's whole point is often to look at borderline results, not just pass/fail; a CI smoke test should set this")
	)
	flag.Parse()

	if *dirsFlag == "" && *pathsFlag == "" {
		return fmt.Errorf("at least one of --dirs or --paths is required")
	}
	if *algorithmFlag == "" {
		return fmt.Errorf("--algorithm is required")
	}

	projectRoot := *projectRootFlag
	if projectRoot == "" {
		root, err := findProjectRoot(".")
		if err != nil {
			return err
		}
		projectRoot = root
	}

	tmpDir, err := os.MkdirTemp("", "benchcompare-")
	if err != nil {
		return fmt.Errorf("creating scratch directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	goBin := *goBinFlag
	if goBin == "" {
		fmt.Fprintln(os.Stderr, "benchcompare: building go binary...")
		goBin, err = buildGoBinary(projectRoot, tmpDir)
		if err != nil {
			return err
		}
	}
	rustBin := *rustBinFlag
	if rustBin == "" {
		fmt.Fprintln(os.Stderr, "benchcompare: building rust binary (release)...")
		rustBin, err = buildRustBinary(projectRoot)
		if err != nil {
			return err
		}
	}

	maxSizeBytes := int64(*maxSizeMBFlag) * 1024 * 1024

	var files []string
	if *dirsFlag != "" {
		dirFiles, err := selectFiles(projectRoot, strings.Split(*dirsFlag, ","), *sampleFlag, *seedFlag, maxSizeBytes)
		if err != nil {
			return err
		}
		files = append(files, dirFiles...)
	}
	if *pathsFlag != "" {
		pathFiles, err := selectPaths(strings.Split(*pathsFlag, ","), *sampleFlag, *seedFlag, maxSizeBytes)
		if err != nil {
			return err
		}
		files = append(files, pathFiles...)
	}
	if len(files) == 0 {
		return fmt.Errorf("no .cnf files found under the requested --dirs/--paths (or all were excluded by --max-size-mb)")
	}

	var algParams []string
	if strings.TrimSpace(*algParamsFlag) != "" {
		algParams = strings.Fields(*algParamsFlag)
	}
	var timeLimit *int
	if *timeLimitFlag > 0 {
		timeLimit = timeLimitFlag
	}
	var numThreads *int
	if *numThreadsFlag > 0 {
		numThreads = numThreadsFlag
	}
	hardTimeout := time.Duration(*hardTimeoutFlag) * time.Second
	if hardTimeout <= 0 {
		if timeLimit != nil {
			hardTimeout = time.Duration(*timeLimit+30) * time.Second
		} else {
			hardTimeout = 120 * time.Second
		}
	}

	sweepStart := time.Now()
	results := make([]fileResult, 0, len(files))
	for i, f := range files {
		if !*quietFlag {
			fmt.Fprintf(os.Stderr, "[%d/%d] %s\n", i+1, len(files), filepath.Base(f))
		}
		goOutFile := filepath.Join(tmpDir, "go_solution.sol")
		rustOutFile := filepath.Join(tmpDir, "rust_solution.sol")

		// Run sequentially, one binary at a time: running Go and Rust
		// concurrently on the same machine would make both binaries'
		// wall-clock times meaningless (each would be competing with
		// the other for the same physical cores), which defeats the
		// entire point of a timing comparison -- especially once
		// --num-threads asks either binary for more than one worker.
		goResult := run(runConfig{
			binary: goBin, inputFile: f, algorithm: *algorithmFlag, algParams: algParams,
			timeLimitSecs: timeLimit, numThreads: numThreads, noPreprocessing: *noPreFlag,
			outputFile: goOutFile, hardTimeout: hardTimeout,
		})
		rustResult := run(runConfig{
			binary: rustBin, inputFile: f, algorithm: *algorithmFlag, algParams: algParams,
			timeLimitSecs: timeLimit, numThreads: numThreads, noPreprocessing: *noPreFlag,
			outputFile: rustOutFile, hardTimeout: hardTimeout,
		})

		results = append(results, fileResult{File: filepath.Base(f), Go: goResult, Rust: rustResult})
	}
	sweepElapsed := time.Since(sweepStart)

	s := summarize(results)
	printReport(os.Stdout, *algorithmFlag, sweepElapsed, results, s)

	if *jsonFlag != "" {
		if err := writeJSON(*jsonFlag, results); err != nil {
			return fmt.Errorf("writing --json output: %w", err)
		}
	}

	if *failOnIssues && hasIssues(s) {
		return fmt.Errorf("one or more files were flagged; see the report above")
	}

	return nil
}

// hasIssues reports whether s contains anything --fail-on-issues
// should treat as a failure: a cross-language mismatch, an
// independent-verification failure, or a run that never reached a
// definite (SAT/UNSAT) verdict at all -- whether that's an outright
// crash/process error/harness timeout (NoVerdict; every one of these
// cases printReport's own "flagged runs" section already surfaces to
// a human) or a well-formed "gave up" UNKNOWN (Unknown; not otherwise
// flagged, since it's a normal, unremarkable outcome for
// benchcompare's usual big-sweep-of-genuinely-hard-instances use --
// but a smoke test's whole point is files small enough that nothing
// should ever need to give up).
func hasIssues(s summary) bool {
	return len(s.Mismatches) > 0 ||
		s.Go.VerificationFails > 0 || s.Rust.VerificationFails > 0 ||
		s.Go.NoVerdict > 0 || s.Rust.NoVerdict > 0 ||
		s.Go.Unknown > 0 || s.Rust.Unknown > 0
}

// findProjectRoot walks upward from start looking for a directory that
// contains go_src/, rust_src/, and benchmark/ subdirectories -- the
// three fixed anchors PROMPT.md's project layout guarantees will
// always exist together at the repository root. This lets
// benchcompare be invoked from anywhere inside the repository (the
// repository root, util/benchcompare/ itself, or anywhere else)
// without the caller needing to pass --project-root by hand.
func findProjectRoot(start string) (string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	dir := abs
	for range 32 { // ample for any realistic directory depth; avoids an infinite loop at "/"
		if looksLikeProjectRoot(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("could not auto-detect the vibe_sat project root above %s; pass --project-root explicitly", abs)
}

// looksLikeProjectRoot reports whether dir contains the three
// directories every stage of this project has kept at its root.
func looksLikeProjectRoot(dir string) bool {
	for _, sub := range []string{"go_src", "rust_src", "benchmark"} {
		info, err := os.Stat(filepath.Join(dir, sub))
		if err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// selectFiles gathers every *.cnf file anywhere under benchmark/<dir>
// (recursively, not just directly inside it) for each dir in
// dirNames, then, if sample > 0, deterministically (given seed)
// samples at most that many files from each directory -- so a large
// directory like uf250-1065's 100 files can be swept at a manageable
// size without the selection changing from run to run. maxSizeBytes,
// if positive, silently excludes any file larger than that (see
// selectFromDir's doc comment for why this exists).
//
// The recursive walk matters: benchmark/'s subdirectories are not all
// laid out the same way -- most (e.g. uf250-1065) hold their .cnf
// files directly, but a few (e.g. uuf100-430, uf75-325, uuf50-218,
// uuf75-325) have an extra nested folder level, a leftover of how
// their original SATLIB tarball extracted. Walking recursively handles
// both without needing special-case knowledge of which is which.
func selectFiles(projectRoot string, dirNames []string, sample int, seed int64, maxSizeBytes int64) ([]string, error) {
	rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed)^0x9e3779b97f4a7c15))

	var all []string
	for _, name := range dirNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		dir := filepath.Join(projectRoot, "benchmark", name)
		found, err := selectFromDir(dir, sample, maxSizeBytes, rng)
		if err != nil {
			return nil, fmt.Errorf("benchmark directory %q: %w", dir, err)
		}
		all = append(all, found...)
	}
	return all, nil
}

// selectPaths is selectFiles' counterpart for literal filesystem
// paths rather than names relative to benchmark/ -- STAGE29.md's
// sat_comp/2018 (deliberately kept outside benchmark/ and out of the
// repository entirely; see reports/REPORT29.md) is exactly the case
// this exists for: a local-only benchmark set this tool still needs
// to be able to point at, now and in any future stage, without it
// ever being committed. Each entry in dirPaths is resolved relative to
// the current working directory (or used as-is if already absolute),
// not projectRoot, since a local-only benchmark set has no fixed
// location inside this repository to be relative to.
func selectPaths(dirPaths []string, sample int, seed int64, maxSizeBytes int64) ([]string, error) {
	rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed)^0x9e3779b97f4a7c15))

	var all []string
	for _, dir := range dirPaths {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		found, err := selectFromDir(dir, sample, maxSizeBytes, rng)
		if err != nil {
			return nil, fmt.Errorf("path %q: %w", dir, err)
		}
		all = append(all, found...)
	}
	return all, nil
}

// selectFromDir recursively finds every *.cnf file under dir, drops
// any larger than maxSizeBytes (if positive -- 0 means no limit), and,
// if sample > 0, deterministically (via rng) samples at most that many
// of what remains.
//
// The size cap exists because of a real hazard REPORT29.md hit
// directly: a single multi-hundred-megabyte, multi-million-clause CNF
// file can exhaust available memory or blow through an intended time
// limit by more than an order of magnitude (dfs's per-branch watch-
// state cloning in particular -- see REPORT9.md's own flagged
// scalability tradeoff), and unlike a slow-but-safe run,
// --hard-timeout-secs cannot save you from an OOM kill that happens
// before the timeout would even fire. A sweep across an unfamiliar,
// wildly-size-varying benchmark set should default to excluding the
// files most likely to cause that, not discover the hazard the hard
// way on every single run.
func selectFromDir(dir string, sample int, maxSizeBytes int64, rng *rand.Rand) ([]string, error) {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("directory does not exist")
	}
	var found []string
	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".cnf") {
			return nil
		}
		if maxSizeBytes > 0 {
			info, statErr := d.Info()
			if statErr != nil {
				return statErr
			}
			if info.Size() > maxSizeBytes {
				return nil
			}
		}
		found = append(found, path)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("reading directory: %w", walkErr)
	}
	sort.Strings(found) // deterministic order before sampling
	if sample > 0 && len(found) > sample {
		rng.Shuffle(len(found), func(i, j int) { found[i], found[j] = found[j], found[i] })
		found = found[:sample]
		sort.Strings(found) // deterministic printing order after sampling too
	}
	return found, nil
}
