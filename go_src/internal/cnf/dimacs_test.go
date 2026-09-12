package cnf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTempCNF writes the given contents to a new temporary file with
// a .cnf suffix and returns its path. The file is automatically
// removed when the test completes.
func writeTempCNF(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.cnf")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("failed to write temp cnf file: %v", err)
	}
	return path
}

// TestReadDIMACSValid verifies that a well-formed CNF file is parsed
// into a Problem with the expected variable count, clause count, and
// clause contents.
func TestReadDIMACSValid(t *testing.T) {
	contents := `c a simple example
p cnf 3 2
1 -2 3 0
-1 2 0
`
	path := writeTempCNF(t, contents)
	problem, err := ReadDIMACS(path, 0)
	if err != nil {
		t.Fatalf("ReadDIMACS returned unexpected error: %v", err)
	}
	if problem.NumVars != 3 {
		t.Errorf("NumVars = %d, want 3", problem.NumVars)
	}
	if problem.NumClauses() != 2 {
		t.Errorf("NumClauses() = %d, want 2", problem.NumClauses())
	}
	if problem.NumLiterals() != 5 {
		t.Errorf("NumLiterals() = %d, want 5", problem.NumLiterals())
	}
}

// TestReadDIMACSClauseSpanningMultipleLines verifies that a clause
// whose literals are split across more than one line before its
// terminating 0 is parsed as a single clause.
func TestReadDIMACSClauseSpanningMultipleLines(t *testing.T) {
	contents := "p cnf 3 1\n1 -2\n3 0\n"
	path := writeTempCNF(t, contents)
	problem, err := ReadDIMACS(path, 0)
	if err != nil {
		t.Fatalf("ReadDIMACS returned unexpected error: %v", err)
	}
	if problem.NumClauses() != 1 {
		t.Errorf("NumClauses() = %d, want 1", problem.NumClauses())
	}
	if problem.NumLiterals() != 3 {
		t.Errorf("NumLiterals() = %d, want 3", problem.NumLiterals())
	}
}

// TestReadDIMACSMissingFile verifies that attempting to read a file
// that does not exist returns an error.
func TestReadDIMACSMissingFile(t *testing.T) {
	_, err := ReadDIMACS("/nonexistent/path/does-not-exist.cnf", 0)
	if err == nil {
		t.Fatalf("ReadDIMACS on nonexistent file: expected error, got nil")
	}
}

// TestReadDIMACSMissingHeader verifies that a file with no "p cnf"
// problem line produces an error.
func TestReadDIMACSMissingHeader(t *testing.T) {
	path := writeTempCNF(t, "1 -2 0\n")
	_, err := ReadDIMACS(path, 0)
	if err == nil {
		t.Fatalf("expected error for missing problem line, got nil")
	}
}

// TestReadDIMACSDuplicateHeader verifies that a second "p cnf" line
// produces an error.
func TestReadDIMACSDuplicateHeader(t *testing.T) {
	path := writeTempCNF(t, "p cnf 2 1\np cnf 2 1\n1 2 0\n")
	_, err := ReadDIMACS(path, 0)
	if err == nil {
		t.Fatalf("expected error for duplicate problem line, got nil")
	}
}

// TestReadDIMACSInvalidLiteral verifies that a non-numeric token in a
// clause produces an error.
func TestReadDIMACSInvalidLiteral(t *testing.T) {
	path := writeTempCNF(t, "p cnf 2 1\n1 x 0\n")
	_, err := ReadDIMACS(path, 0)
	if err == nil {
		t.Fatalf("expected error for invalid literal, got nil")
	}
}

// TestReadDIMACSLiteralExceedsVarCount verifies that a literal whose
// magnitude is greater than the declared number of variables produces
// an error.
func TestReadDIMACSLiteralExceedsVarCount(t *testing.T) {
	path := writeTempCNF(t, "p cnf 2 1\n1 3 0\n")
	_, err := ReadDIMACS(path, 0)
	if err == nil {
		t.Fatalf("expected error for literal exceeding variable count, got nil")
	}
}

// TestReadDIMACSUnterminatedClause verifies that a trailing clause
// without a terminating 0 produces an error.
func TestReadDIMACSUnterminatedClause(t *testing.T) {
	path := writeTempCNF(t, "p cnf 2 1\n1 2\n")
	_, err := ReadDIMACS(path, 0)
	if err == nil {
		t.Fatalf("expected error for unterminated clause, got nil")
	}
}

// TestReadDIMACSClauseCountMismatch verifies that a mismatch between
// the declared clause count and the actual number of clauses parsed
// produces an error.
func TestReadDIMACSClauseCountMismatch(t *testing.T) {
	path := writeTempCNF(t, "p cnf 2 2\n1 2 0\n")
	_, err := ReadDIMACS(path, 0)
	if err == nil {
		t.Fatalf("expected error for clause count mismatch, got nil")
	}
}

// TestReadDIMACSClauseBeforeHeader verifies that clause data appearing
// before the problem line produces an error.
func TestReadDIMACSClauseBeforeHeader(t *testing.T) {
	path := writeTempCNF(t, "1 2 0\np cnf 2 1\n")
	_, err := ReadDIMACS(path, 0)
	if err == nil {
		t.Fatalf("expected error for clause before problem line, got nil")
	}
}

// TestReadDIMACSEmptyClause verifies that an empty clause (a lone 0)
// is accepted and recorded as a zero-length clause.
func TestReadDIMACSEmptyClause(t *testing.T) {
	path := writeTempCNF(t, "p cnf 2 1\n0\n")
	problem, err := ReadDIMACS(path, 0)
	if err != nil {
		t.Fatalf("ReadDIMACS returned unexpected error: %v", err)
	}
	if problem.NumClauses() != 1 {
		t.Errorf("NumClauses() = %d, want 1", problem.NumClauses())
	}
	if len(problem.Clauses[0]) != 0 {
		t.Errorf("len(Clauses[0]) = %d, want 0", len(problem.Clauses[0]))
	}
}

// TestReadDIMACSPercentTerminator verifies that a trailing "%" marker
// line (used by the SATLIB benchmark set to mark the end of the
// clause section) is accepted, and that anything after it, including
// a stray trailing "0", is ignored.
func TestReadDIMACSPercentTerminator(t *testing.T) {
	contents := "p cnf 2 1\n1 2 0\n%\n0\n\n"
	path := writeTempCNF(t, contents)
	problem, err := ReadDIMACS(path, 0)
	if err != nil {
		t.Fatalf("ReadDIMACS returned unexpected error: %v", err)
	}
	if problem.NumClauses() != 1 {
		t.Errorf("NumClauses() = %d, want 1", problem.NumClauses())
	}
}

// TestReadDIMACSVerboseAnnouncesFilename verifies that, at verbose
// level 1, the filename being read is printed to stdout.
func TestReadDIMACSVerboseAnnouncesFilename(t *testing.T) {
	path := writeTempCNF(t, "p cnf 1 1\n1 0\n")

	var readErr error
	output := captureStdout(t, func() {
		_, readErr = ReadDIMACS(path, 1)
	})

	if readErr != nil {
		t.Fatalf("ReadDIMACS returned unexpected error: %v", readErr)
	}
	if !strings.Contains(output, path) {
		t.Errorf("expected stdout to contain filename %q, got %q", path, output)
	}
}
