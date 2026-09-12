package solution

import (
	"strings"
	"testing"

	"vibe_sat/internal/assign"
)

// TestWriteProducesExpectedFormat verifies that Write emits the status
// line followed by a value line listing one signed literal per
// variable, matching the assignment.
func TestWriteProducesExpectedFormat(t *testing.T) {
	a := assign.New(3)
	a[1] = assign.True
	a[2] = assign.False
	a[3] = assign.True

	var buf strings.Builder
	if err := Write(&buf, a, 3); err != nil {
		t.Fatalf("Write returned unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), buf.String())
	}
	if lines[0] != "s SATISFIABLE" {
		t.Errorf("line 0 = %q, want %q", lines[0], "s SATISFIABLE")
	}
	if lines[1] != "v 1 -2 3 0" {
		t.Errorf("line 1 = %q, want %q", lines[1], "v 1 -2 3 0")
	}
}
