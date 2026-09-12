package cliargs

import "testing"

// TestParseLongForm verifies that the long form flags (--input,
// --verbose) are parsed correctly.
func TestParseLongForm(t *testing.T) {
	args, err := Parse([]string{"--input=problem.cnf", "--verbose=2"})
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if args.InputFile != "problem.cnf" {
		t.Errorf("InputFile = %q, want %q", args.InputFile, "problem.cnf")
	}
	if args.Verbose != 2 {
		t.Errorf("Verbose = %d, want 2", args.Verbose)
	}
}

// TestParseShortForm verifies that the short form flags (-i, -v) are
// parsed correctly.
func TestParseShortForm(t *testing.T) {
	args, err := Parse([]string{"-i", "problem.cnf", "-v", "3"})
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if args.InputFile != "problem.cnf" {
		t.Errorf("InputFile = %q, want %q", args.InputFile, "problem.cnf")
	}
	if args.Verbose != 3 {
		t.Errorf("Verbose = %d, want 3", args.Verbose)
	}
}

// TestParseDefaultVerbose verifies that verbose defaults to 0 when not
// specified on the command line.
func TestParseDefaultVerbose(t *testing.T) {
	args, err := Parse([]string{"--input=problem.cnf"})
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
	_, err := Parse([]string{"--verbose=1"})
	if err == nil {
		t.Fatalf("expected error for missing --input, got nil")
	}
}

// TestParseUnknownFlag verifies that an unrecognized flag produces an
// error.
func TestParseUnknownFlag(t *testing.T) {
	_, err := Parse([]string{"--input=problem.cnf", "--bogus=1"})
	if err == nil {
		t.Fatalf("expected error for unknown flag, got nil")
	}
}
