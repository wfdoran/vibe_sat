package assign

import (
	"math/rand/v2"
	"testing"

	"vibe_sat/internal/cnf"
)

// TestNewAllUnassigned verifies that New returns an Assignment of the
// correct length with every variable marked Unassigned.
func TestNewAllUnassigned(t *testing.T) {
	a := New(3)
	if len(a) != 4 {
		t.Fatalf("len(a) = %d, want 4", len(a))
	}
	for v := 1; v <= 3; v++ {
		if a[v] != Unassigned {
			t.Errorf("a[%d] = %v, want Unassigned", v, a[v])
		}
	}
}

// TestNewRandomProducesCompleteAssignment verifies that NewRandom
// assigns either True or False (never Unassigned) to every variable.
func TestNewRandomProducesCompleteAssignment(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	a := NewRandom(5, rng)
	if len(a) != 6 {
		t.Fatalf("len(a) = %d, want 6", len(a))
	}
	for v := 1; v <= 5; v++ {
		if a[v] != True && a[v] != False {
			t.Errorf("a[%d] = %v, want True or False", v, a[v])
		}
	}
}

// TestNewRandomIsDeterministicForASeededSource verifies that two
// separate NewRandom calls, each using a fresh rng seeded identically,
// produce the same assignment.
func TestNewRandomIsDeterministicForASeededSource(t *testing.T) {
	a := NewRandom(20, rand.New(rand.NewPCG(42, 7)))
	b := NewRandom(20, rand.New(rand.NewPCG(42, 7)))
	for v := 1; v <= 20; v++ {
		if a[v] != b[v] {
			t.Fatalf("assignments differ at variable %d: %v vs %v", v, a[v], b[v])
		}
	}
}

// TestLiteralIsTruePositiveLiteral verifies that a positive literal is
// true exactly when its variable is True.
func TestLiteralIsTruePositiveLiteral(t *testing.T) {
	a := New(1)
	a[1] = True
	if !a.LiteralIsTrue(cnf.Literal(1)) {
		t.Errorf("expected literal 1 to be true when variable 1 is True")
	}
	a[1] = False
	if a.LiteralIsTrue(cnf.Literal(1)) {
		t.Errorf("expected literal 1 to be false when variable 1 is False")
	}
}

// TestLiteralIsTrueNegativeLiteral verifies that a negative literal is
// true exactly when its variable is False.
func TestLiteralIsTrueNegativeLiteral(t *testing.T) {
	a := New(1)
	a[1] = False
	if !a.LiteralIsTrue(cnf.Literal(-1)) {
		t.Errorf("expected literal -1 to be true when variable 1 is False")
	}
	a[1] = True
	if a.LiteralIsTrue(cnf.Literal(-1)) {
		t.Errorf("expected literal -1 to be false when variable 1 is True")
	}
}
