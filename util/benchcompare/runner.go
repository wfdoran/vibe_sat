package main

// Running a built vibe_sat binary (Go or Rust -- from this tool's
// point of view they are just an executable path and a shared set of
// command line flags, per PROMPT.md's requirement that both languages
// understand identical arguments) and capturing what this harness
// needs from it: the SAT/UNSAT/UNKNOWN verdict, wall-clock time, and
// (when satisfiable) the path to a written solution file.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// verdict is the outcome vibe_sat reports for one run, read from its
// own stdout (see cliargs/help.go and every algorithm package's
// verbose >= 1 output: a bare "SAT", "UNSAT", or "UNKNOWN" line).
type verdict int

const (
	verdictNone verdict = iota // never printed a recognizable verdict line at all
	verdictSAT
	verdictUNSAT
	verdictUNKNOWN
)

func (v verdict) String() string {
	switch v {
	case verdictSAT:
		return "SAT"
	case verdictUNSAT:
		return "UNSAT"
	case verdictUNKNOWN:
		return "UNKNOWN"
	default:
		return "NONE"
	}
}

// MarshalJSON renders a verdict as its name ("SAT"/"UNSAT"/"UNKNOWN"/
// "NONE") rather than its underlying int, so a JSON report is readable
// without cross-referencing this source file.
func (v verdict) MarshalJSON() ([]byte, error) {
	return []byte(`"` + v.String() + `"`), nil
}

// runConfig describes one invocation of a vibe_sat binary: which
// binary, which input file, and which of the flags STAGE1.md through
// STAGE21.md introduced to pass along. Fields left at their zero value
// are simply omitted from the command line, matching vibe_sat's own
// defaults.
type runConfig struct {
	binary          string
	inputFile       string
	algorithm       string
	algParams       []string // raw tokens, passed through verbatim to --alg-params
	timeLimitSecs   *int
	numThreads      *int
	noPreprocessing bool
	outputFile      string        // always set by the caller so a SAT run can be independently verified
	hardTimeout     time.Duration // kills the process if exceeded; a safety net independent of --time-limit-secs
}

// args builds the actual argv (excluding argv[0]) for cfg, using only
// long flag forms for readability in logs -- both vibe_sat binaries
// accept these interchangeably with their short forms per STAGE1.md's
// "two versions" requirement, so there is no behavioral difference.
func (cfg runConfig) args() []string {
	out := []string{
		"--input=" + cfg.inputFile,
		"--algorithm=" + cfg.algorithm,
		"--verbose=1",
		"--output=" + cfg.outputFile,
	}
	if len(cfg.algParams) > 0 {
		out = append(out, "--alg-params")
		out = append(out, cfg.algParams...)
	}
	if cfg.timeLimitSecs != nil {
		out = append(out, fmt.Sprintf("--time-limit-secs=%d", *cfg.timeLimitSecs))
	}
	if cfg.numThreads != nil {
		out = append(out, fmt.Sprintf("--num-threads=%d", *cfg.numThreads))
	}
	if cfg.noPreprocessing {
		out = append(out, "--no-preprocessing")
	}
	return out
}

// runOutcome is everything this harness records about one run. Fields
// are exported (and error values kept as plain strings, not the error
// interface) purely so this struct can be handed straight to
// encoding/json in writeJSON -- an unexported field, or a field of
// interface type like "error", either gets silently dropped or
// marshals to little more than "{}".
type runOutcome struct {
	Verdict        verdict       `json:"verdict"`
	ElapsedSeconds float64       `json:"elapsed_seconds"`
	Stdout         string        `json:"stdout"`
	Stderr         string        `json:"stderr"`
	ExitErr        string        `json:"exit_err,omitempty"`     // non-empty if the process exited non-zero or was killed
	TimedOutOS     bool          `json:"timed_out_os"`           // true if the *harness's* hardTimeout killed it, not vibe_sat's own --time-limit-secs
	SolutionErr    string        `json:"solution_err,omitempty"` // set if Verdict == verdictSAT but the solution file couldn't be read/parsed/verified
	Solved         bool          `json:"solved"`                 // Verdict == verdictSAT and the reported assignment independently checked out
	elapsed        time.Duration // kept for convenient time.Duration-typed access; ElapsedSeconds is the JSON view of the same value
}

// run executes cfg.binary with cfg.args(), enforcing cfg.hardTimeout
// as an absolute ceiling (independent of, and strictly larger than,
// whatever --time-limit-secs was itself given -- this exists purely so
// a bug or an unexpectedly hard instance can never hang this harness
// itself indefinitely). It always removes any stale file at
// cfg.outputFile first, since vibe_sat only ever creates that file
// when a solution is actually found (see writeSolution/write_solution
// in both main.go/main.rs) -- a leftover file from a previous run at
// the same path would otherwise look like a fresh solution.
func run(cfg runConfig) runOutcome {
	_ = os.Remove(cfg.outputFile)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.hardTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, cfg.binary, cfg.args()...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	outcome := runOutcome{
		elapsed:        elapsed,
		ElapsedSeconds: elapsed.Seconds(),
		Stdout:         stdout.String(),
		Stderr:         stderr.String(),
	}
	if err != nil {
		outcome.ExitErr = err.Error()
	}
	if ctx.Err() == context.DeadlineExceeded {
		outcome.TimedOutOS = true
	}
	outcome.Verdict = parseVerdictLine(outcome.Stdout)

	if outcome.Verdict == verdictSAT {
		assign, parseErr := parseSolutionFile(cfg.outputFile)
		if parseErr != nil {
			outcome.SolutionErr = parseErr.Error()
			return outcome
		}
		p, parseErr := parseDIMACS(cfg.inputFile)
		if parseErr != nil {
			outcome.SolutionErr = parseErr.Error()
			return outcome
		}
		if verErr := verifySatisfies(p, assign); verErr != nil {
			outcome.SolutionErr = verErr.Error()
			return outcome
		}
		outcome.Solved = true
	}

	return outcome
}

// parseVerdictLine scans stdout for the last line that is exactly
// "SAT", "UNSAT", or "UNKNOWN" (every algorithm package prints exactly
// one such line at --verbose=1; scanning for the *last* one rather
// than the first is a harmless robustness margin, not a requirement of
// the current output format). A run that never printed one of these
// three (a crash, a parse error, an unexpected early exit) reports
// verdictNone.
func parseVerdictLine(stdout string) verdict {
	found := verdictNone
	for _, line := range strings.Split(stdout, "\n") {
		switch strings.TrimSpace(line) {
		case "SAT":
			found = verdictSAT
		case "UNSAT":
			found = verdictUNSAT
		case "UNKNOWN":
			found = verdictUNKNOWN
		}
	}
	return found
}

// buildGoBinary compiles go_src's vibe_sat command into a fresh binary
// under destDir and returns its path. It shells out to the same "go
// build" a user would run by hand; nothing here re-implements or
// bypasses the Go toolchain.
func buildGoBinary(projectRoot, destDir string) (string, error) {
	dest := filepath.Join(destDir, "vibe_sat_go")
	cmd := exec.Command("go", "build", "-o", dest, "./cmd/vibe_sat")
	cmd.Dir = filepath.Join(projectRoot, "go_src")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go build failed: %w\n%s", err, stderr.String())
	}
	return dest, nil
}

// buildRustBinary compiles rust_src's vibe_sat binary in release mode
// (an unoptimized debug build would make every timing comparison in
// this harness meaningless) and returns the path cargo itself places
// it at.
func buildRustBinary(projectRoot string) (string, error) {
	manifest := filepath.Join(projectRoot, "rust_src", "Cargo.toml")
	cmd := exec.Command("cargo", "build", "--release", "--quiet", "--manifest-path", manifest)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("cargo build failed: %w\n%s", err, stderr.String())
	}
	return filepath.Join(projectRoot, "rust_src", "target", "release", "vibe_sat"), nil
}
