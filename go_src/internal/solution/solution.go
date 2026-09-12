// Package solution writes a satisfying assignment in the DIMACS
// solution format used by SAT solvers and competitions: a status line
// followed by a value line listing one signed literal per variable.
package solution

import (
	"fmt"
	"io"
	"strings"

	"vibe_sat/internal/assign"
)

// Write writes assignment, a complete satisfying assignment over
// variables 1 through numVars, to w in DIMACS solution format:
//
//	s SATISFIABLE
//	v 1 -2 3 ... 0
//
// The value line contains, for every variable from 1 to numVars, the
// variable number if it is True or its negation if it is False,
// terminated by a trailing 0.
func Write(w io.Writer, assignment assign.Assignment, numVars int) error {
	if _, err := fmt.Fprintln(w, "s SATISFIABLE"); err != nil {
		return err
	}

	var line strings.Builder
	line.WriteString("v")
	for v := 1; v <= numVars; v++ {
		if assignment[v] == assign.True {
			fmt.Fprintf(&line, " %d", v)
		} else {
			fmt.Fprintf(&line, " %d", -v)
		}
	}
	line.WriteString(" 0")

	_, err := fmt.Fprintln(w, line.String())
	return err
}
