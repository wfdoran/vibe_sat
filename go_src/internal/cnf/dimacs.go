package cnf

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ReadDIMACS opens the file named by filename and parses it as a
// DIMACS formatted CNF file, returning the resulting Problem.
//
// The DIMACS CNF format consists of:
//   - zero or more comment lines beginning with 'c'
//   - exactly one problem line of the form "p cnf <numVars> <numClauses>"
//   - a sequence of clauses, each a whitespace separated list of
//     non-zero signed integers (literals) terminated by a 0; a clause
//     may span multiple lines
//
// If verbose is >= 1, the name of the file being read is printed to
// stdout before it is opened. If any error is encountered (the file
// cannot be opened, or its contents are not well-formed DIMACS CNF),
// a non-nil error is returned describing the problem; the caller is
// expected to report this to the user and exit with a non-zero status.
func ReadDIMACS(filename string, verbose int) (*Problem, error) {
	if verbose >= 1 {
		fmt.Println("Reading CNF file:", filename)
	}

	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("could not open input file %q: %w", filename, err)
	}
	defer file.Close()

	var (
		numVars          int
		numClausesStated int
		headerSeen       bool
		clauses          []Clause
		currentClause    Clause
	)

	scanner := bufio.NewScanner(file)
	lineNum := 0
readLines:
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		switch line[0] {
		case 'c':
			// Comment line; ignore.
			continue

		case '%':
			// Some DIMACS files (notably the SATLIB benchmark set) end
			// the clause section with a line containing only "%",
			// optionally followed by trailing "0" and blank lines.
			// Treat this marker as the end of the clause data.
			break readLines

		case 'p':
			if headerSeen {
				return nil, fmt.Errorf("%s:%d: duplicate problem line", filename, lineNum)
			}
			fields := strings.Fields(line)
			if len(fields) != 4 || fields[0] != "p" || fields[1] != "cnf" {
				return nil, fmt.Errorf("%s:%d: malformed problem line %q, expected \"p cnf <vars> <clauses>\"", filename, lineNum, line)
			}
			numVars, err = strconv.Atoi(fields[2])
			if err != nil || numVars < 0 {
				return nil, fmt.Errorf("%s:%d: invalid number of variables %q", filename, lineNum, fields[2])
			}
			numClausesStated, err = strconv.Atoi(fields[3])
			if err != nil || numClausesStated < 0 {
				return nil, fmt.Errorf("%s:%d: invalid number of clauses %q", filename, lineNum, fields[3])
			}
			headerSeen = true

		default:
			if !headerSeen {
				return nil, fmt.Errorf("%s:%d: clause data found before problem line", filename, lineNum)
			}
			for _, token := range strings.Fields(line) {
				value, err := strconv.Atoi(token)
				if err != nil {
					return nil, fmt.Errorf("%s:%d: invalid literal %q", filename, lineNum, token)
				}
				if value == 0 {
					clauses = append(clauses, currentClause)
					currentClause = nil
					continue
				}
				variable := value
				if variable < 0 {
					variable = -variable
				}
				if variable > numVars {
					return nil, fmt.Errorf("%s:%d: literal %d refers to variable beyond declared count %d", filename, lineNum, value, numVars)
				}
				currentClause = append(currentClause, Literal(value))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading %q: %w", filename, err)
	}

	if !headerSeen {
		return nil, fmt.Errorf("%s: missing problem line (\"p cnf <vars> <clauses>\")", filename)
	}
	if len(currentClause) > 0 {
		return nil, fmt.Errorf("%s: final clause is not terminated with a 0", filename)
	}
	if len(clauses) != numClausesStated {
		return nil, fmt.Errorf("%s: problem line declares %d clauses but %d were found", filename, numClausesStated, len(clauses))
	}

	return &Problem{NumVars: numVars, Clauses: clauses}, nil
}
