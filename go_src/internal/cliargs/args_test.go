package cliargs

import (
	"strconv"
	"strings"
	"testing"
)

// baseHCArgs returns a minimal valid argument list for algorithm
// "hc" that tests can append additional flags to.
func baseHCArgs(extra ...string) []string {
	args := []string{"--input=problem.cnf", "--algorithm=hc", "--alg-params=10"}
	return append(args, extra...)
}

// TestParseHelpLongForm verifies that --help alone sets Args.Help,
// without requiring any other argument.
func TestParseHelpLongForm(t *testing.T) {
	args, err := Parse([]string{"--help"})
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if !args.Help {
		t.Errorf("Help = false, want true")
	}
}

// TestParseHelpShortForm verifies that -h alone sets Args.Help.
func TestParseHelpShortForm(t *testing.T) {
	args, err := Parse([]string{"-h"})
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if !args.Help {
		t.Errorf("Help = false, want true")
	}
}

// TestParseHelpOverridesOtherErrors verifies that --help/-h takes
// precedence even when the rest of the command line is incomplete or
// invalid.
func TestParseHelpOverridesOtherErrors(t *testing.T) {
	args, err := Parse([]string{"--bogus=1", "--help"})
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if !args.Help {
		t.Errorf("Help = false, want true")
	}
}

// TestHelpTextMentionsEveryFlag verifies that HelpText documents
// every flag vibe_sat currently understands.
func TestHelpTextMentionsEveryFlag(t *testing.T) {
	text := HelpText()
	for _, flag := range []string{
		"--input", "-i", "--verbose", "-v", "--algorithm", "-a",
		"--output", "-o", "--time-limit-secs", "-t", "--alg-params", "-p",
		"--no-preprocessing", "-x", "--help", "-h",
	} {
		if !strings.Contains(text, flag) {
			t.Errorf("HelpText() does not mention %q", flag)
		}
	}
}

// TestParseLongForm verifies that the long form flags are parsed
// correctly.
func TestParseLongForm(t *testing.T) {
	args, err := Parse([]string{"--input=problem.cnf", "--verbose=2", "--algorithm=hc", "--alg-params=10"})
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if args.InputFile != "problem.cnf" {
		t.Errorf("InputFile = %q, want %q", args.InputFile, "problem.cnf")
	}
	if args.Verbose != 2 {
		t.Errorf("Verbose = %d, want 2", args.Verbose)
	}
	if args.Algorithm != "hc" {
		t.Errorf("Algorithm = %q, want %q", args.Algorithm, "hc")
	}
}

// TestParseShortForm verifies that the short form flags are parsed
// correctly.
func TestParseShortForm(t *testing.T) {
	args, err := Parse([]string{"-i", "problem.cnf", "-v", "3", "-a", "hc", "-p", "10"})
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if args.InputFile != "problem.cnf" {
		t.Errorf("InputFile = %q, want %q", args.InputFile, "problem.cnf")
	}
	if args.Verbose != 3 {
		t.Errorf("Verbose = %d, want 3", args.Verbose)
	}
	if len(args.AlgParams) != 1 || args.AlgParams[0] != 10 {
		t.Errorf("AlgParams = %v, want [10]", args.AlgParams)
	}
}

// TestParseDefaultVerbose verifies that verbose defaults to 0 when not
// specified on the command line.
func TestParseDefaultVerbose(t *testing.T) {
	args, err := Parse(baseHCArgs())
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if args.Verbose != 0 {
		t.Errorf("Verbose = %d, want 0", args.Verbose)
	}
}

// TestParseMissingInput verifies that omitting the required --input/-i
// argument produces an error.
func TestParseMissingInput(t *testing.T) {
	_, err := Parse([]string{"--algorithm=hc", "--alg-params=10"})
	if err == nil {
		t.Fatalf("expected error for missing --input, got nil")
	}
}

// TestParseMissingAlgorithm verifies that omitting the required
// --algorithm/-a argument produces an error.
func TestParseMissingAlgorithm(t *testing.T) {
	_, err := Parse([]string{"--input=problem.cnf", "--alg-params=10"})
	if err == nil {
		t.Fatalf("expected error for missing --algorithm, got nil")
	}
}

// TestParseUnsupportedAlgorithm verifies that an --algorithm value
// other than "hc" produces an error.
func TestParseUnsupportedAlgorithm(t *testing.T) {
	_, err := Parse([]string{"--input=problem.cnf", "--algorithm=bogus", "--alg-params=10"})
	if err == nil {
		t.Fatalf("expected error for unsupported algorithm, got nil")
	}
}

// TestParseUnknownFlag verifies that an unrecognized flag produces an
// error.
func TestParseUnknownFlag(t *testing.T) {
	_, err := Parse(baseHCArgs("--bogus=1"))
	if err == nil {
		t.Fatalf("expected error for unknown flag, got nil")
	}
}

// TestParseHCRequiresStartsOrTimeLimit verifies that --algorithm=hc
// requires at least one of --alg-params or --time-limit-secs.
func TestParseHCRequiresStartsOrTimeLimit(t *testing.T) {
	_, err := Parse([]string{"--input=problem.cnf", "--algorithm=hc"})
	if err == nil {
		t.Fatalf("expected error when neither --alg-params nor --time-limit-secs is given")
	}
}

// TestParseHCWithTimeLimitOnly verifies that --algorithm=hc accepts
// --time-limit-secs alone, without --alg-params.
func TestParseHCWithTimeLimitOnly(t *testing.T) {
	args, err := Parse([]string{"--input=problem.cnf", "--algorithm=hc", "--time-limit-secs=30"})
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if args.TimeLimitSecs == nil || *args.TimeLimitSecs != 30 {
		t.Errorf("TimeLimitSecs = %v, want 30", args.TimeLimitSecs)
	}
	if len(args.AlgParams) != 0 {
		t.Errorf("AlgParams = %v, want empty", args.AlgParams)
	}
}

// TestParseHCWithBothStartsAndTimeLimit verifies that --algorithm=hc
// accepts both --alg-params and --time-limit-secs together.
func TestParseHCWithBothStartsAndTimeLimit(t *testing.T) {
	args, err := Parse([]string{"--input=problem.cnf", "--algorithm=hc", "--alg-params=5", "--time-limit-secs=30"})
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if len(args.AlgParams) != 1 || args.AlgParams[0] != 5 {
		t.Errorf("AlgParams = %v, want [5]", args.AlgParams)
	}
	if args.TimeLimitSecs == nil || *args.TimeLimitSecs != 30 {
		t.Errorf("TimeLimitSecs = %v, want 30", args.TimeLimitSecs)
	}
}

// TestParseHCRejectsMultipleAlgParams verifies that --algorithm=hc
// rejects more than one --alg-params value.
func TestParseHCRejectsMultipleAlgParams(t *testing.T) {
	_, err := Parse([]string{"--input=problem.cnf", "--algorithm=hc", "--alg-params", "5", "10"})
	if err == nil {
		t.Fatalf("expected error for more than one alg-param with algorithm=hc")
	}
}

// TestParseWSRequiresTriesOrTimeLimit verifies that --algorithm=ws
// requires at least one of --alg-params or --time-limit-secs, just
// like --algorithm=hc.
func TestParseWSRequiresTriesOrTimeLimit(t *testing.T) {
	_, err := Parse([]string{"--input=problem.cnf", "--algorithm=ws"})
	if err == nil {
		t.Fatalf("expected error when neither --alg-params nor --time-limit-secs is given")
	}
}

// TestParseWSWithTimeLimitOnly verifies that --algorithm=ws accepts
// --time-limit-secs alone, without --alg-params, and that
// MaxFlips/NoisePercent defaults are left for the caller to fill in
// (AlgParams stays empty).
func TestParseWSWithTimeLimitOnly(t *testing.T) {
	args, err := Parse([]string{"--input=problem.cnf", "--algorithm=ws", "--time-limit-secs=30"})
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if len(args.AlgParams) != 0 {
		t.Errorf("AlgParams = %v, want empty", args.AlgParams)
	}
}

// TestParseWSAcceptsThreeAlgParams verifies that --algorithm=ws
// accepts all three positional values: tries, max-flips-per-try, and
// noise-percent.
func TestParseWSAcceptsThreeAlgParams(t *testing.T) {
	args, err := Parse([]string{"--input=problem.cnf", "--algorithm=ws", "--alg-params", "5", "2000", "40"})
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if len(args.AlgParams) != 3 || args.AlgParams[0] != 5 || args.AlgParams[1] != 2000 || args.AlgParams[2] != 40 {
		t.Errorf("AlgParams = %v, want [5 2000 40]", args.AlgParams)
	}
}

// TestParseWSRejectsNonPositiveMaxFlips verifies that a non-positive
// second --alg-params value (max flips per try) is rejected for
// --algorithm=ws.
func TestParseWSRejectsNonPositiveMaxFlips(t *testing.T) {
	_, err := Parse([]string{"--input=problem.cnf", "--algorithm=ws", "--alg-params", "5", "0"})
	if err == nil {
		t.Fatalf("expected error for non-positive max-flips-per-try value")
	}
}

// TestParseWSRejectsOutOfRangeNoisePercent verifies that a third
// --alg-params value (noise percent) outside [0, 100] is rejected for
// --algorithm=ws.
func TestParseWSRejectsOutOfRangeNoisePercent(t *testing.T) {
	_, err := Parse([]string{"--input=problem.cnf", "--algorithm=ws", "--alg-params", "5", "1000", "101"})
	if err == nil {
		t.Fatalf("expected error for out-of-range noise-percent value")
	}
}

// TestParseDFSNeedsNoStoppingCriterion verifies that --algorithm=dfs
// is accepted with neither --alg-params nor --time-limit-secs, unlike
// --algorithm=hc/ws.
func TestParseDFSNeedsNoStoppingCriterion(t *testing.T) {
	args, err := Parse([]string{"--input=problem.cnf", "--algorithm=dfs"})
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if args.TimeLimitSecs != nil {
		t.Errorf("TimeLimitSecs = %v, want nil", args.TimeLimitSecs)
	}
}

// TestParseDFSAcceptsTimeLimit verifies that --algorithm=dfs accepts
// an optional --time-limit-secs.
func TestParseDFSAcceptsTimeLimit(t *testing.T) {
	args, err := Parse([]string{"--input=problem.cnf", "--algorithm=dfs", "--time-limit-secs=10"})
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if args.TimeLimitSecs == nil || *args.TimeLimitSecs != 10 {
		t.Errorf("TimeLimitSecs = %v, want 10", args.TimeLimitSecs)
	}
}

// TestParseDFSRejectsAlgParams verifies that --algorithm=dfs rejects
// --alg-params, since dfs takes no parameters at this stage.
func TestParseDFSRejectsAlgParams(t *testing.T) {
	_, err := Parse([]string{"--input=problem.cnf", "--algorithm=dfs", "--alg-params=5"})
	if err == nil {
		t.Fatalf("expected error for an out-of-range --alg-params value with --algorithm=dfs")
	}
}

// TestParseDFSAcceptsSelectVarVariant verifies that --algorithm=dfs
// accepts a single --alg-params value of 0 or 1, selecting which
// SelectVar heuristic to use (STAGE6.md).
func TestParseDFSAcceptsSelectVarVariant(t *testing.T) {
	for _, variant := range []int64{0, 1} {
		args, err := Parse([]string{"--input=problem.cnf", "--algorithm=dfs", "--alg-params", strconv.FormatInt(variant, 10)})
		if err != nil {
			t.Fatalf("Parse returned unexpected error for --alg-params=%d: %v", variant, err)
		}
		if len(args.AlgParams) != 1 || args.AlgParams[0] != variant {
			t.Errorf("AlgParams = %v, want [%d]", args.AlgParams, variant)
		}
	}
}

// TestParseDFSRejectsMultipleAlgParams verifies that --algorithm=dfs
// rejects more than one --alg-params value.
func TestParseDFSRejectsMultipleAlgParams(t *testing.T) {
	_, err := Parse([]string{"--input=problem.cnf", "--algorithm=dfs", "--alg-params", "0", "1"})
	if err == nil {
		t.Fatalf("expected error for more than one alg-param with algorithm=dfs")
	}
}

// TestParseCDCLNeedsNoStoppingCriterion verifies that --algorithm=cdcl
// is accepted with neither --alg-params nor --time-limit-secs, same
// as --algorithm=dfs (STAGE11.md).
func TestParseCDCLNeedsNoStoppingCriterion(t *testing.T) {
	args, err := Parse([]string{"--input=problem.cnf", "--algorithm=cdcl"})
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if args.TimeLimitSecs != nil {
		t.Errorf("TimeLimitSecs = %v, want nil", args.TimeLimitSecs)
	}
}

// TestParseCDCLAcceptsTimeLimit verifies that --algorithm=cdcl accepts
// an optional --time-limit-secs.
func TestParseCDCLAcceptsTimeLimit(t *testing.T) {
	args, err := Parse([]string{"--input=problem.cnf", "--algorithm=cdcl", "--time-limit-secs=10"})
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if args.TimeLimitSecs == nil || *args.TimeLimitSecs != 10 {
		t.Errorf("TimeLimitSecs = %v, want 10", args.TimeLimitSecs)
	}
}

// TestParseCDCLAcceptsSelectVarVariant verifies that --algorithm=cdcl
// accepts a single --alg-params value of 0 or 1, selecting which
// SelectVar heuristic to use, same as --algorithm=dfs (STAGE11.md).
func TestParseCDCLAcceptsSelectVarVariant(t *testing.T) {
	for _, variant := range []int64{0, 1} {
		args, err := Parse([]string{"--input=problem.cnf", "--algorithm=cdcl", "--alg-params", strconv.FormatInt(variant, 10)})
		if err != nil {
			t.Fatalf("Parse returned unexpected error for --alg-params=%d: %v", variant, err)
		}
		if len(args.AlgParams) != 1 || args.AlgParams[0] != variant {
			t.Errorf("AlgParams = %v, want [%d]", args.AlgParams, variant)
		}
	}
}

// TestParseCDCLRejectsOutOfRangeAlgParams verifies that
// --algorithm=cdcl rejects an --alg-params value other than 0 or 1.
func TestParseCDCLRejectsOutOfRangeAlgParams(t *testing.T) {
	_, err := Parse([]string{"--input=problem.cnf", "--algorithm=cdcl", "--alg-params=5"})
	if err == nil {
		t.Fatalf("expected error for an out-of-range --alg-params value with --algorithm=cdcl")
	}
}

// TestParseCDCLRejectsThreeAlgParams verifies that --algorithm=cdcl
// rejects more than two --alg-params values (STAGE12.md adds a second
// value, the memory limit, but no third).
func TestParseCDCLRejectsThreeAlgParams(t *testing.T) {
	_, err := Parse([]string{"--input=problem.cnf", "--algorithm=cdcl", "--alg-params", "0", "100", "5"})
	if err == nil {
		t.Fatalf("expected error for three alg-params with algorithm=cdcl")
	}
}

// TestParseCDCLAcceptsMemoryLimit verifies that --algorithm=cdcl's
// second --alg-params value is parsed as a memory limit in bytes,
// per STAGE12.md, for a plain integer and for each supported unit
// suffix, case-insensitively.
func TestParseCDCLAcceptsMemoryLimit(t *testing.T) {
	cases := []struct {
		token string
		want  int64
	}{
		{"100", 100},
		{"100k", 100 * 1024},
		{"100K", 100 * 1024},
		{"100kb", 100 * 1024},
		{"100KB", 100 * 1024},
		{"5m", 5 * 1024 * 1024},
		{"5MB", 5 * 1024 * 1024},
		{"2g", 2 * 1024 * 1024 * 1024},
		{"2Gb", 2 * 1024 * 1024 * 1024},
	}
	for _, c := range cases {
		args, err := Parse([]string{"--input=problem.cnf", "--algorithm=cdcl", "--alg-params", "0", c.token})
		if err != nil {
			t.Fatalf("Parse returned unexpected error for --alg-params 0 %s: %v", c.token, err)
		}
		if args.MemoryLimitBytes == nil || *args.MemoryLimitBytes != c.want {
			t.Errorf("--alg-params 0 %s: MemoryLimitBytes = %v, want %d", c.token, args.MemoryLimitBytes, c.want)
		}
	}
}

// TestParseCDCLRejectsMemoryLimitAsFirstValue verifies that
// "--alg-params 100MB" (a memory-limit-shaped string given as the
// *first* value) is rejected, since positionally that slot is the
// SelectVar selector (0 or 1), not the memory limit.
func TestParseCDCLRejectsMemoryLimitAsFirstValue(t *testing.T) {
	_, err := Parse([]string{"--input=problem.cnf", "--algorithm=cdcl", "--alg-params", "100MB"})
	if err == nil {
		t.Fatalf("expected error for a non-numeric first --alg-params value with algorithm=cdcl")
	}
}

// TestParseCDCLRejectsMalformedMemoryLimit verifies that a
// second --alg-params value that isn't a valid byte size (no leading
// digits, or an unrecognized unit suffix) is rejected.
func TestParseCDCLRejectsMalformedMemoryLimit(t *testing.T) {
	for _, bad := range []string{"MB", "100XB", "100.5MB", ""} {
		_, err := Parse([]string{"--input=problem.cnf", "--algorithm=cdcl", "--alg-params", "0", bad})
		if err == nil {
			t.Errorf("expected error for malformed memory limit %q", bad)
		}
	}
}

// TestParseCDCLRejectsNonPositiveMemoryLimit verifies that a memory
// limit of zero bytes is rejected as nonsensical.
func TestParseCDCLRejectsNonPositiveMemoryLimit(t *testing.T) {
	_, err := Parse([]string{"--input=problem.cnf", "--algorithm=cdcl", "--alg-params", "0", "0"})
	if err == nil {
		t.Fatalf("expected error for a zero-byte memory limit")
	}
}

// TestParseCDCLDefaultsToUnboundedMemory verifies that
// MemoryLimitBytes is nil (unbounded) when no second --alg-params
// value is given, preserving Stage 11's original behavior.
func TestParseCDCLDefaultsToUnboundedMemory(t *testing.T) {
	args, err := Parse([]string{"--input=problem.cnf", "--algorithm=cdcl"})
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if args.MemoryLimitBytes != nil {
		t.Errorf("MemoryLimitBytes = %v, want nil", args.MemoryLimitBytes)
	}
}

// TestParseNoPreprocessingDefaultsFalse verifies that preprocessing is
// enabled by default (NoPreprocessing is false when the flag is not
// given).
func TestParseNoPreprocessingDefaultsFalse(t *testing.T) {
	args, err := Parse(baseHCArgs())
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if args.NoPreprocessing {
		t.Errorf("NoPreprocessing = true, want false by default")
	}
}

// TestParseNoPreprocessingLongForm verifies that --no-preprocessing
// sets NoPreprocessing, with no value expected.
func TestParseNoPreprocessingLongForm(t *testing.T) {
	args, err := Parse(baseHCArgs("--no-preprocessing"))
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if !args.NoPreprocessing {
		t.Errorf("NoPreprocessing = false, want true")
	}
}

// TestParseNoPreprocessingShortForm verifies that -x is equivalent to
// --no-preprocessing.
func TestParseNoPreprocessingShortForm(t *testing.T) {
	args, err := Parse(baseHCArgs("-x"))
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if !args.NoPreprocessing {
		t.Errorf("NoPreprocessing = false, want true")
	}
}

// TestParseNoPreprocessingRejectsValue verifies that
// --no-preprocessing does not accept an inline value.
func TestParseNoPreprocessingRejectsValue(t *testing.T) {
	_, err := Parse(baseHCArgs("--no-preprocessing=true"))
	if err == nil {
		t.Fatalf("expected error for --no-preprocessing=true (it takes no value)")
	}
}

// TestParseNoPreprocessingDoesNotConsumeFollowingFlag verifies that,
// being a zero-value flag, --no-preprocessing does not swallow the
// next token as if it were a value.
func TestParseNoPreprocessingDoesNotConsumeFollowingFlag(t *testing.T) {
	args, err := Parse([]string{"--input=problem.cnf", "--algorithm=hc", "--no-preprocessing", "--alg-params=10"})
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if !args.NoPreprocessing {
		t.Errorf("NoPreprocessing = false, want true")
	}
	if len(args.AlgParams) != 1 || args.AlgParams[0] != 10 {
		t.Errorf("AlgParams = %v, want [10]", args.AlgParams)
	}
}

// TestTokenizeAlgParamsSpaceSeparatedMultipleValues verifies that the
// space-separated form of --alg-params collects multiple following
// tokens, up to the flag's maximum of three. This is tested against
// the tokenizer directly (rather than the full Parse pipeline) since
// the only currently supported algorithm, "hc", restricts
// --alg-params to a single value; the tokenizing behavior itself is
// algorithm-independent.
func TestTokenizeAlgParamsSpaceSeparatedMultipleValues(t *testing.T) {
	rawValues, err := tokenize([]string{"-p", "1", "2", "3", "--input=problem.cnf"})
	if err != nil {
		t.Fatalf("tokenize returned unexpected error: %v", err)
	}
	values := rawValues["alg-params"]
	if len(values) != 3 || values[0] != "1" || values[1] != "2" || values[2] != "3" {
		t.Errorf("alg-params = %v, want [1 2 3]", values)
	}
}

// TestTokenizeAlgParamsStopsAtNextFlag verifies that space-separated
// --alg-params values stop being collected once a token that looks
// like a flag is encountered, even if fewer than three values have
// been collected.
func TestTokenizeAlgParamsStopsAtNextFlag(t *testing.T) {
	rawValues, err := tokenize([]string{"-p", "5", "--verbose", "2"})
	if err != nil {
		t.Fatalf("tokenize returned unexpected error: %v", err)
	}
	if values := rawValues["alg-params"]; len(values) != 1 || values[0] != "5" {
		t.Errorf("alg-params = %v, want [5]", values)
	}
	if values := rawValues["verbose"]; len(values) != 1 || values[0] != "2" {
		t.Errorf("verbose = %v, want [2]", values)
	}
}

// TestTokenizeAlgParamsAllowsNegativeNumberValue verifies that a
// negative numeric value after --alg-params is treated as a value,
// not mistaken for a new flag.
func TestTokenizeAlgParamsAllowsNegativeNumberValue(t *testing.T) {
	rawValues, err := tokenize([]string{"-p", "-5"})
	if err != nil {
		t.Fatalf("tokenize returned unexpected error: %v", err)
	}
	if values := rawValues["alg-params"]; len(values) != 1 || values[0] != "-5" {
		t.Errorf("alg-params = %v, want [-5]", values)
	}
}

// TestParseOutputFile verifies that --output/-o is parsed correctly.
func TestParseOutputFile(t *testing.T) {
	args, err := Parse(baseHCArgs("--output=solution.txt"))
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if args.OutputFile != "solution.txt" {
		t.Errorf("OutputFile = %q, want %q", args.OutputFile, "solution.txt")
	}
}

// TestParseNegativeTimeLimitRejected verifies that a non-positive
// --time-limit-secs value is rejected.
func TestParseNegativeTimeLimitRejected(t *testing.T) {
	_, err := Parse([]string{"--input=problem.cnf", "--algorithm=hc", "--time-limit-secs=0"})
	if err == nil {
		t.Fatalf("expected error for non-positive --time-limit-secs")
	}
}

// TestParseUnexpectedPositionalArgument verifies that a bare token
// that is not associated with any flag produces an error.
func TestParseUnexpectedPositionalArgument(t *testing.T) {
	_, err := Parse(append(baseHCArgs(), "stray"))
	if err == nil {
		t.Fatalf("expected error for unexpected positional argument")
	}
}
