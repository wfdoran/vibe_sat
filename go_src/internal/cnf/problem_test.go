package cnf

import "testing"

// TestLiteralVar verifies that Var returns the correct 1-based
// variable index for both positive and negative literals.
func TestLiteralVar(t *testing.T) {
	cases := []struct {
		lit  Literal
		want int
	}{
		{Literal(5), 5},
		{Literal(-5), 5},
		{Literal(1), 1},
		{Literal(-1), 1},
	}
	for _, c := range cases {
		if got := c.lit.Var(); got != c.want {
			t.Errorf("Literal(%d).Var() = %d, want %d", c.lit, got, c.want)
		}
	}
}

// TestLiteralIsNegative verifies that IsNegative correctly reports the
// polarity of a literal.
func TestLiteralIsNegative(t *testing.T) {
	if Literal(5).IsNegative() {
		t.Errorf("Literal(5).IsNegative() = true, want false")
	}
	if !Literal(-5).IsNegative() {
		t.Errorf("Literal(-5).IsNegative() = false, want true")
	}
}

// TestProblemNumClauses verifies that NumClauses returns the number of
// clauses stored in the problem.
func TestProblemNumClauses(t *testing.T) {
	p := &Problem{
		NumVars: 3,
		Clauses: []Clause{
			{Literal(1), Literal(-2)},
			{Literal(3)},
		},
	}
	if got := p.NumClauses(); got != 2 {
		t.Errorf("NumClauses() = %d, want 2", got)
	}
}

// TestProblemNumLiterals verifies that NumLiterals sums the lengths of
// every clause in the problem, including an empty clause.
func TestProblemNumLiterals(t *testing.T) {
	p := &Problem{
		NumVars: 3,
		Clauses: []Clause{
			{Literal(1), Literal(-2), Literal(3)},
			{Literal(-1)},
			{},
		},
	}
	if got := p.NumLiterals(); got != 4 {
		t.Errorf("NumLiterals() = %d, want 4", got)
	}
}
