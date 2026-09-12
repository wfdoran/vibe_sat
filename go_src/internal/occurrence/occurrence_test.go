package occurrence

import (
	"reflect"
	"testing"

	"vibe_sat/internal/cnf"
)

// TestBuildPositiveAndNegativeOccurrences verifies that Build records
// each clause under the correct variable and polarity.
func TestBuildPositiveAndNegativeOccurrences(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 3,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(-2)}, // clause 0
			{cnf.Literal(-1), cnf.Literal(3)}, // clause 1
			{cnf.Literal(2), cnf.Literal(3)},  // clause 2
		},
	}

	lists := Build(problem)

	if got := lists.Positive[1]; !reflect.DeepEqual(got, []int{0}) {
		t.Errorf("Positive[1] = %v, want [0]", got)
	}
	if got := lists.Negative[1]; !reflect.DeepEqual(got, []int{1}) {
		t.Errorf("Negative[1] = %v, want [1]", got)
	}
	if got := lists.Positive[2]; !reflect.DeepEqual(got, []int{2}) {
		t.Errorf("Positive[2] = %v, want [2]", got)
	}
	if got := lists.Negative[2]; !reflect.DeepEqual(got, []int{0}) {
		t.Errorf("Negative[2] = %v, want [0]", got)
	}
	if got := lists.Positive[3]; !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("Positive[3] = %v, want [1 2]", got)
	}
	if got := lists.Negative[3]; len(got) != 0 {
		t.Errorf("Negative[3] = %v, want empty", got)
	}
}

// TestBuildTautologicalClauseAppearsInBothLists verifies that a
// clause containing both polarities of the same variable is recorded
// in that variable's positive list and its negative list.
func TestBuildTautologicalClauseAppearsInBothLists(t *testing.T) {
	problem := &cnf.Problem{
		NumVars: 1,
		Clauses: []cnf.Clause{
			{cnf.Literal(1), cnf.Literal(-1)},
		},
	}

	lists := Build(problem)

	if got := lists.Positive[1]; !reflect.DeepEqual(got, []int{0}) {
		t.Errorf("Positive[1] = %v, want [0]", got)
	}
	if got := lists.Negative[1]; !reflect.DeepEqual(got, []int{0}) {
		t.Errorf("Negative[1] = %v, want [0]", got)
	}
}

// TestBuildEmptyProblem verifies that Build handles a problem with no
// clauses without error.
func TestBuildEmptyProblem(t *testing.T) {
	problem := &cnf.Problem{NumVars: 2, Clauses: nil}
	lists := Build(problem)
	if len(lists.Positive) != 3 || len(lists.Negative) != 3 {
		t.Fatalf("expected lists sized for 2 variables (+1), got Positive len %d, Negative len %d", len(lists.Positive), len(lists.Negative))
	}
}
