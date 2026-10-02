package cnf

import (
	"reflect"
	"testing"
)

// TestNormalizeDropsTautologiesAndDuplicates checks the three cases
// together: a tautology is dropped, a repeated literal is collapsed
// (first occurrence kept, order otherwise preserved), and a clause that
// needs no change is untouched.
func TestNormalizeDropsTautologiesAndDuplicates(t *testing.T) {
	p := &Problem{NumVars: 4, Clauses: []Clause{
		{1, -1, 2},     // tautology: dropped
		{3, 2, 3, 2},   // duplicates: -> {3, 2}
		{-4, 1},        // unchanged
		{2, 3, -2},     // tautology (not adjacent): dropped
		{-1, -1, -1},   // -> {-1}
		{4, -3, 4, -3}, // -> {4, -3}
	}}
	tautologies, dups := Normalize(p)

	want := []Clause{{3, 2}, {-4, 1}, {-1}, {4, -3}}
	if !reflect.DeepEqual(p.Clauses, want) {
		t.Errorf("Clauses = %v, want %v", p.Clauses, want)
	}
	if tautologies != 2 {
		t.Errorf("tautologies = %d, want 2", tautologies)
	}
	if dups != 2+2+2 {
		t.Errorf("duplicateLiterals = %d, want 6", dups)
	}
	if p.NumVars != 4 {
		t.Errorf("NumVars = %d, want 4 (must never change)", p.NumVars)
	}
}

// TestNormalizeKeepsEmptyClause verifies an empty clause survives: it is
// what makes a formula unsatisfiable, so dropping it would be unsound.
func TestNormalizeKeepsEmptyClause(t *testing.T) {
	p := &Problem{NumVars: 2, Clauses: []Clause{{}, {1, 2}}}
	tautologies, dups := Normalize(p)
	if tautologies != 0 || dups != 0 {
		t.Errorf("(tautologies, dups) = (%d, %d), want (0, 0)", tautologies, dups)
	}
	if len(p.Clauses) != 2 || len(p.Clauses[0]) != 0 {
		t.Errorf("Clauses = %v, want the empty clause preserved first", p.Clauses)
	}
}

// TestNormalizeNoOpOnCleanInput verifies a formula that is already
// normalized is left exactly as it was, and that the per-clause marks
// are reset between clauses (clause 2 reuses variable 1 with the
// opposite sign, which must not look like a tautology).
func TestNormalizeNoOpOnCleanInput(t *testing.T) {
	p := &Problem{NumVars: 3, Clauses: []Clause{{1, 2}, {-1, 3}, {-2, -3}}}
	want := []Clause{{1, 2}, {-1, 3}, {-2, -3}}
	tautologies, dups := Normalize(p)
	if tautologies != 0 || dups != 0 || !reflect.DeepEqual(p.Clauses, want) {
		t.Errorf("got (%d, %d, %v), want (0, 0, %v)", tautologies, dups, p.Clauses, want)
	}
}

// TestNormalizeAllTautologies verifies a formula made only of
// tautologies normalizes to zero clauses.
func TestNormalizeAllTautologies(t *testing.T) {
	p := &Problem{NumVars: 2, Clauses: []Clause{{1, -1}, {2, -2}}}
	tautologies, _ := Normalize(p)
	if tautologies != 2 || len(p.Clauses) != 0 {
		t.Errorf("got %d tautologies and %d clauses left, want 2 and 0", tautologies, len(p.Clauses))
	}
}
