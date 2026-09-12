package cnf

import "fmt"

// PrintSummary prints the basic size parameters of a SAT problem to
// stdout: the number of variables, the number of clauses, and the
// total number of literal occurrences across all clauses.
func PrintSummary(problem *Problem) {
	fmt.Printf("Number of variables: %d\n", problem.NumVars)
	fmt.Printf("Number of clauses: %d\n", problem.NumClauses())
	fmt.Printf("Number of literals: %d\n", problem.NumLiterals())
}
