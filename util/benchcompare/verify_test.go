package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempSolution(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.sol")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing temp solution: %v", err)
	}
	return path
}

func TestParseSolutionFileValid(t *testing.T) {
	path := writeTempSolution(t, "s SATISFIABLE\nv 1 -2 3 0\n")
	assign, err := parseSolutionFile(path)
	if err != nil {
		t.Fatalf("parseSolutionFile: %v", err)
	}
	want := assignment{1: true, 2: false, 3: true}
	for v, val := range want {
		if got, ok := assign[v]; !ok || got != val {
			t.Errorf("assign[%d] = %v, %v; want %v, true", v, got, ok, val)
		}
	}
}

// TestParseSolutionFileSplitAcrossLines confirms a value line split
// across several "v" lines (legal DIMACS, though vibe_sat itself never
// writes one this way) still parses correctly.
func TestParseSolutionFileSplitAcrossLines(t *testing.T) {
	path := writeTempSolution(t, "s SATISFIABLE\nv 1 -2\nv 3 0\n")
	assign, err := parseSolutionFile(path)
	if err != nil {
		t.Fatalf("parseSolutionFile: %v", err)
	}
	if len(assign) != 3 || !assign[1] || assign[2] || !assign[3] {
		t.Errorf("assign = %v, want {1:true 2:false 3:true}", assign)
	}
}

func TestParseSolutionFileErrors(t *testing.T) {
	cases := map[string]string{
		"missing status line": "v 1 -2 0\n",
		"missing terminator":  "s SATISFIABLE\nv 1 -2\n",
		"duplicate variable":  "s SATISFIABLE\nv 1 -1 0\n",
		"bad status text":     "s UNSATISFIABLE\nv 1 0\n",
		"unrecognized line":   "s SATISFIABLE\nx 1 0\n",
	}
	for name, contents := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeTempSolution(t, contents)
			if _, err := parseSolutionFile(path); err == nil {
				t.Fatalf("parseSolutionFile(%q) succeeded, want an error", contents)
			}
		})
	}
}

func TestVerifySatisfiesAcceptsGoodAssignment(t *testing.T) {
	p := &problem{numVars: 3, clauses: [][]int{{1, -2}, {2, 3}, {-1, -3}}}
	// x1=true, x2=true, x3=false satisfies all three clauses.
	assign := assignment{1: true, 2: true, 3: false}
	if err := verifySatisfies(p, assign); err != nil {
		t.Errorf("verifySatisfies = %v, want nil", err)
	}
}

func TestVerifySatisfiesRejectsBadAssignment(t *testing.T) {
	p := &problem{numVars: 2, clauses: [][]int{{1, 2}}}
	// x1=false, x2=false violates the only clause.
	assign := assignment{1: false, 2: false}
	if err := verifySatisfies(p, assign); err == nil {
		t.Fatal("verifySatisfies succeeded on a violated clause, want an error")
	}
}

func TestVerifySatisfiesRejectsMissingVariable(t *testing.T) {
	p := &problem{numVars: 2, clauses: [][]int{{1, 2}}}
	assign := assignment{1: false} // variable 2 never assigned
	if err := verifySatisfies(p, assign); err == nil {
		t.Fatal("verifySatisfies succeeded despite a missing variable, want an error")
	}
}
