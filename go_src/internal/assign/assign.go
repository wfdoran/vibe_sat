// Package assign defines the in-memory representation of an
// assignment of truth values to the variables of a SAT problem.
package assign

import (
	"math/rand/v2"

	"vibe_sat/internal/cnf"
)

// Value represents the truth value of a single variable: True, False,
// or Unassigned. This stage of vibe_sat only ever works with complete
// assignments (every variable True or False), but the three-valued
// representation is adopted from the start so that later stages can
// represent partial assignments without changing this data structure.
type Value int8

const (
	False      Value = 0
	True       Value = 1
	Unassigned Value = 2
)

// Assignment holds one Value per variable. It is indexed directly by
// variable number, 1 through len(a)-1; index 0 is unused so that a
// variable number can be used as the slice index without adjustment.
type Assignment []Value

// New returns an Assignment large enough to hold numVars variables,
// with every variable initially Unassigned.
func New(numVars int) Assignment {
	assignment := make(Assignment, numVars+1)
	for v := range assignment {
		assignment[v] = Unassigned
	}
	return assignment
}

// NewRandom returns a new complete Assignment for numVars variables,
// where each variable is independently set to True or False with
// equal probability, drawn from rng.
func NewRandom(numVars int, rng *rand.Rand) Assignment {
	assignment := make(Assignment, numVars+1)
	for v := 1; v <= numVars; v++ {
		if rng.IntN(2) == 0 {
			assignment[v] = False
		} else {
			assignment[v] = True
		}
	}
	return assignment
}

// LiteralIsTrue reports whether lit evaluates to true under this
// assignment: a positive literal is true when its variable is True, a
// negative literal is true when its variable is False. An Unassigned
// variable makes any literal referencing it evaluate to false; this
// is only meaningful for complete assignments, which is all this
// stage produces.
func (a Assignment) LiteralIsTrue(lit cnf.Literal) bool {
	value := a[lit.Var()]
	if lit.IsNegative() {
		return value == False
	}
	return value == True
}
