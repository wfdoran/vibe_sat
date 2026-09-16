package main

// This file is a small, from-scratch DIMACS CNF parser used only to
// independently re-check a satisfying assignment vibe_sat reports. It
// deliberately does not import (and cannot import, since they are
// "internal" packages in a different Go module) go_src's own
// internal/cnf parser -- the whole point of an independent verifier is
// that a bug shared between the solver's own parser and the checker's
// parser could hide a real mistake. Every stage since REPORT1.md has
// used a from-scratch checker script for exactly this reason; this is
// that checker, finally made permanent instead of thrown away after
// each stage.

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// problem is an in-memory DIMACS CNF formula: the declared variable
// count and the list of clauses, each a list of signed literals (a
// positive integer for the variable, negative for its negation; 0 is
// never stored, it is only the file's own clause terminator).
type problem struct {
	numVars int
	clauses [][]int
}

// parseDIMACS reads the CNF file at path and returns the formula it
// describes. It mirrors the handful of real-world conventions
// go_src/internal/cnf.ReadDIMACS and rust_src/src/cnf.rs's parser both
// had to learn to handle (REPORT1.md): "c"-prefixed comment lines are
// skipped, exactly one "p cnf <numVars> <numClauses>" header is
// required, and a line consisting solely of "%" marks the end of the
// clause section (a legacy SATLIB convention present in every
// benchmark file this project uses).
func parseDIMACS(path string) (*problem, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer file.Close()

	var p problem
	haveHeader := false
	declaredClauses := 0
	var current []int
	sawEndMarker := false

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		if sawEndMarker {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "c") {
			continue
		}
		if line == "%" {
			sawEndMarker = true
			continue
		}
		if strings.HasPrefix(line, "p") {
			fields := strings.Fields(line)
			if len(fields) != 4 || fields[0] != "p" || fields[1] != "cnf" {
				return nil, fmt.Errorf("%s: malformed header line %q", path, line)
			}
			numVars, err := strconv.Atoi(fields[2])
			if err != nil {
				return nil, fmt.Errorf("%s: bad variable count in header: %w", path, err)
			}
			numClauses, err := strconv.Atoi(fields[3])
			if err != nil {
				return nil, fmt.Errorf("%s: bad clause count in header: %w", path, err)
			}
			if haveHeader {
				return nil, fmt.Errorf("%s: duplicate p cnf header", path)
			}
			p.numVars = numVars
			declaredClauses = numClauses
			haveHeader = true
			continue
		}
		if !haveHeader {
			return nil, fmt.Errorf("%s: clause data before p cnf header", path)
		}
		for _, tok := range strings.Fields(line) {
			lit, err := strconv.Atoi(tok)
			if err != nil {
				return nil, fmt.Errorf("%s: invalid literal %q: %w", path, tok, err)
			}
			if lit == 0 {
				p.clauses = append(p.clauses, current)
				current = nil
				continue
			}
			if lit > p.numVars || lit < -p.numVars {
				return nil, fmt.Errorf("%s: literal %d out of range for %d variables", path, lit, p.numVars)
			}
			current = append(current, lit)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if !haveHeader {
		return nil, fmt.Errorf("%s: missing p cnf header", path)
	}
	if len(current) != 0 {
		return nil, fmt.Errorf("%s: final clause missing terminating 0", path)
	}
	if len(p.clauses) != declaredClauses {
		return nil, fmt.Errorf("%s: header declared %d clauses, found %d", path, declaredClauses, len(p.clauses))
	}
	return &p, nil
}
