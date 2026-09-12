package cnf

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout runs fn with os.Stdout redirected to a pipe and
// returns everything written to stdout during that call.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = orig

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("failed to read pipe: %v", err)
	}
	return string(out)
}

// TestPrintSummary verifies that PrintSummary reports the number of
// variables, clauses, and literals of the given problem.
func TestPrintSummary(t *testing.T) {
	problem := &Problem{
		NumVars: 4,
		Clauses: []Clause{
			{Literal(1), Literal(-2)},
			{Literal(3), Literal(4), Literal(-1)},
		},
	}

	output := captureStdout(t, func() {
		PrintSummary(problem)
	})

	if !strings.Contains(output, "Number of variables: 4") {
		t.Errorf("output missing variable count, got %q", output)
	}
	if !strings.Contains(output, "Number of clauses: 2") {
		t.Errorf("output missing clause count, got %q", output)
	}
	if !strings.Contains(output, "Number of literals: 5") {
		t.Errorf("output missing literal count, got %q", output)
	}
}
