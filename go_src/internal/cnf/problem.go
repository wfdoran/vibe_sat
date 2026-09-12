// Package cnf defines the in-memory representation of a SAT problem in
// conjunctive normal form (CNF), along with routines to read such a
// problem from a DIMACS formatted file and to print summary statistics
// about it.
package cnf

// Literal represents a single DIMACS literal. A positive value asserts
// that the referenced variable is true; a negative value asserts that
// the referenced variable is false (negated). The magnitude of the
// value is the 1-based index of the variable being referenced. The
// value 0 is never stored as a Literal; in DIMACS files it is only
// used as a clause terminator and is consumed during parsing.
type Literal int32

// Var returns the 1-based variable index referenced by this literal,
// independent of its polarity (sign).
func (l Literal) Var() int {
	if l < 0 {
		return int(-l)
	}
	return int(l)
}

// IsNegative reports whether this literal is the negation of its
// underlying variable (i.e. whether the original DIMACS value was
// negative).
func (l Literal) IsNegative() bool {
	return l < 0
}

// Clause is a disjunction of literals. An empty Clause represents the
// empty clause, which is always false and therefore makes the whole
// formula unsatisfiable.
type Clause []Literal

// Problem holds an entire SAT instance in conjunctive normal form: the
// number of variables declared by the problem, and the list of clauses
// that must all be satisfied simultaneously. Literals within a clause
// refer to variables by number, from 1 to NumVars.
//
// This representation intentionally keeps clauses as simple slices of
// literals, with no shared mutable state, so that it can later be
// wrapped in whatever concurrency-safe structures (e.g. read-only
// sharing across goroutines, or per-worker copies) are needed once the
// solver runs on multiple cores.
type Problem struct {
	NumVars int      // number of variables declared on the "p cnf" line
	Clauses []Clause // the clauses making up the formula
}

// NumClauses returns the number of clauses currently stored in the
// problem.
func (p *Problem) NumClauses() int {
	return len(p.Clauses)
}

// NumLiterals returns the total number of literal occurrences across
// every clause in the problem (i.e. the sum of each clause's length,
// not the number of distinct literals).
func (p *Problem) NumLiterals() int {
	total := 0
	for _, clause := range p.Clauses {
		total += len(clause)
	}
	return total
}
