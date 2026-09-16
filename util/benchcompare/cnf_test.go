package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTempCNF writes contents to a fresh .cnf file under t's temp
// directory and returns its path.
func writeTempCNF(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.cnf")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing temp cnf: %v", err)
	}
	return path
}

func TestParseDIMACSValidFormula(t *testing.T) {
	path := writeTempCNF(t, "c a comment\np cnf 3 2\n1 -2 0\n2 3 0\n")
	p, err := parseDIMACS(path)
	if err != nil {
		t.Fatalf("parseDIMACS: %v", err)
	}
	if p.numVars != 3 {
		t.Errorf("numVars = %d, want 3", p.numVars)
	}
	want := [][]int{{1, -2}, {2, 3}}
	if len(p.clauses) != len(want) {
		t.Fatalf("clauses = %v, want %v", p.clauses, want)
	}
	for i := range want {
		if !intSliceEqual(p.clauses[i], want[i]) {
			t.Errorf("clause %d = %v, want %v", i, p.clauses[i], want[i])
		}
	}
}

func intSliceEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestParseDIMACSPercentTerminator confirms the SATLIB "%" end-of-clauses
// marker (REPORT1.md) is honored: everything after it, including a
// trailing bare "0" line some SATLIB files include, must be ignored.
func TestParseDIMACSPercentTerminator(t *testing.T) {
	path := writeTempCNF(t, "p cnf 2 1\n1 2 0\n%\n0\n\n")
	p, err := parseDIMACS(path)
	if err != nil {
		t.Fatalf("parseDIMACS: %v", err)
	}
	if len(p.clauses) != 1 {
		t.Fatalf("clauses = %v, want exactly 1", p.clauses)
	}
}

func TestParseDIMACSMultiLineClause(t *testing.T) {
	path := writeTempCNF(t, "p cnf 3 1\n1 -2\n3 0\n")
	p, err := parseDIMACS(path)
	if err != nil {
		t.Fatalf("parseDIMACS: %v", err)
	}
	if !intSliceEqual(p.clauses[0], []int{1, -2, 3}) {
		t.Errorf("clauses[0] = %v, want [1 -2 3]", p.clauses[0])
	}
}

func TestParseDIMACSErrors(t *testing.T) {
	cases := map[string]string{
		"missing header":        "1 2 0\n",
		"clause count mismatch": "p cnf 2 2\n1 2 0\n",
		"literal out of range":  "p cnf 1 1\n2 0\n",
		"bad literal token":     "p cnf 1 1\nfoo 0\n",
		"unterminated clause":   "p cnf 2 1\n1 2\n",
	}
	for name, contents := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeTempCNF(t, contents)
			if _, err := parseDIMACS(path); err == nil {
				t.Fatalf("parseDIMACS(%q) succeeded, want an error", contents)
			}
		})
	}
}

func TestParseDIMACSMissingFile(t *testing.T) {
	if _, err := parseDIMACS(filepath.Join(t.TempDir(), "does-not-exist.cnf")); err == nil {
		t.Fatal("parseDIMACS on a missing file succeeded, want an error")
	} else if !strings.Contains(err.Error(), "opening") {
		t.Errorf("error = %q, want it to mention opening the file", err)
	}
}
