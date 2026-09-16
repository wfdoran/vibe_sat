package main

// Independent parsing of vibe_sat's own DIMACS solution output
// ("s SATISFIABLE" / "v <lit> ... 0", per go_src/internal/solution and
// rust_src/src/solution.rs) and a from-scratch check that a returned
// assignment actually satisfies every clause of the original formula.

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// assignment maps a 1-based variable number to the value vibe_sat
// reported for it (true/false). It is built purely from the solution
// file's own "v" line(s), independent of anything the solver itself
// believes about the problem.
type assignment map[int]bool

// parseSolutionFile reads a DIMACS solution file written by
// --output=<file> and returns the assignment it encodes. It expects
// exactly the format solution.Write/solution::write produce: a
// "s SATISFIABLE" status line, followed by one or more "v ..." value
// lines (allowing the value line to be split across several lines,
// though vibe_sat itself always writes a single one) terminated by a
// literal 0.
func parseSolutionFile(path string) (assignment, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening solution file %s: %w", path, err)
	}
	defer file.Close()

	result := assignment{}
	sawStatus := false
	done := false

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		switch fields[0] {
		case "s":
			if len(fields) != 2 || fields[1] != "SATISFIABLE" {
				return nil, fmt.Errorf("%s: unexpected status line %q", path, line)
			}
			sawStatus = true
		case "v":
			if done {
				return nil, fmt.Errorf("%s: value data after terminating 0", path)
			}
			for _, tok := range fields[1:] {
				lit, err := strconv.Atoi(tok)
				if err != nil {
					return nil, fmt.Errorf("%s: invalid value token %q: %w", path, tok, err)
				}
				if lit == 0 {
					done = true
					break
				}
				v := lit
				if v < 0 {
					v = -v
				}
				if _, dup := result[v]; dup {
					return nil, fmt.Errorf("%s: variable %d assigned twice", path, v)
				}
				result[v] = lit > 0
			}
		default:
			return nil, fmt.Errorf("%s: unrecognized line %q", path, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if !sawStatus {
		return nil, fmt.Errorf("%s: missing 's SATISFIABLE' status line", path)
	}
	if !done {
		return nil, fmt.Errorf("%s: value line never reached a terminating 0", path)
	}
	return result, nil
}

// verifySatisfies checks, from scratch, that assign satisfies every
// clause of p: every clause must contain at least one literal whose
// variable is assigned the polarity that makes that literal true. It
// returns a nil error on success, or a descriptive error naming the
// first violated or under-specified clause found.
func verifySatisfies(p *problem, assign assignment) error {
	for i, clause := range p.clauses {
		satisfied := false
		for _, lit := range clause {
			v := lit
			if v < 0 {
				v = -v
			}
			val, ok := assign[v]
			if !ok {
				return fmt.Errorf("clause %d: variable %d has no assigned value", i, v)
			}
			if (lit > 0 && val) || (lit < 0 && !val) {
				satisfied = true
				break
			}
		}
		if !satisfied {
			return fmt.Errorf("clause %d (%v) is not satisfied by the reported assignment", i, clause)
		}
	}
	return nil
}
