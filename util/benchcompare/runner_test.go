package main

import "testing"

func TestParseVerdictLine(t *testing.T) {
	cases := []struct {
		name   string
		stdout string
		want   verdict
	}{
		{"sat", "dfs: some params\nSAT\n", verdictSAT},
		{"unsat", "cdcl: some params\nUNSAT\nwall clock time: 1s\n", verdictUNSAT},
		{"unknown", "hillclimb: params\nUNKNOWN\n", verdictUNKNOWN},
		{"none", "some unrelated crash output\n", verdictNone},
		// "SATISFIABLE" (from the solution file's own status line, if it
		// ever ended up on stdout) must not be mistaken for the bare
		// "SAT" verdict line -- parseVerdictLine matches whole lines only.
		{"near-miss substring", "s SATISFIABLE\nv 1 2 0\n", verdictNone},
		{"last one wins", "SAT\nUNSAT\n", verdictUNSAT},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseVerdictLine(c.stdout); got != c.want {
				t.Errorf("parseVerdictLine(%q) = %v, want %v", c.stdout, got, c.want)
			}
		})
	}
}

func TestVerdictString(t *testing.T) {
	cases := map[verdict]string{verdictSAT: "SAT", verdictUNSAT: "UNSAT", verdictUNKNOWN: "UNKNOWN", verdictNone: "NONE"}
	for v, want := range cases {
		if got := v.String(); got != want {
			t.Errorf("verdict(%d).String() = %q, want %q", v, got, want)
		}
	}
}

func TestRunConfigArgs(t *testing.T) {
	limit := 30
	threads := 4
	cfg := runConfig{
		inputFile:       "input.cnf",
		algorithm:       "cdcl",
		algParams:       []string{"2", "4"},
		timeLimitSecs:   &limit,
		numThreads:      &threads,
		noPreprocessing: true,
		outputFile:      "out.sol",
	}
	args := cfg.args()
	want := []string{
		"--input=input.cnf", "--algorithm=cdcl", "--verbose=1", "--output=out.sol",
		"--alg-params", "2", "4", "--time-limit-secs=30", "--num-threads=4", "--no-preprocessing",
	}
	if !stringSliceEqual(args, want) {
		t.Errorf("args() = %v, want %v", args, want)
	}
}

// TestRunConfigArgsOmitsUnsetFields confirms a bare-minimum config
// (the common case: hc/ws/dfs with no restart/thread tuning) doesn't
// grow any of the optional flags -- important since some of them
// (--no-preprocessing in particular) change vibe_sat's behavior if
// present at all, regardless of any value.
func TestRunConfigArgsOmitsUnsetFields(t *testing.T) {
	cfg := runConfig{inputFile: "i.cnf", algorithm: "hc", outputFile: "o.sol"}
	args := cfg.args()
	want := []string{"--input=i.cnf", "--algorithm=hc", "--verbose=1", "--output=o.sol"}
	if !stringSliceEqual(args, want) {
		t.Errorf("args() = %v, want %v", args, want)
	}
}

func stringSliceEqual(a, b []string) bool {
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
