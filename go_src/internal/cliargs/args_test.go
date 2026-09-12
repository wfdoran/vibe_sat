package cliargs

import (
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
		"--help", "-h",
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
