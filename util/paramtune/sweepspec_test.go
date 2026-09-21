package main

import "testing"

func TestParseSweepSpecSingleParam(t *testing.T) {
	stages, err := parseSweepSpec("cdcl.glucoseK=0.5|0.6|0.7")
	if err != nil {
		t.Fatalf("parseSweepSpec: %v", err)
	}
	if len(stages) != 1 {
		t.Fatalf("got %d stages, want 1", len(stages))
	}
	if len(stages[0].combinations) != 3 {
		t.Fatalf("got %d combinations, want 3", len(stages[0].combinations))
	}
	for i, want := range []string{"0.5", "0.6", "0.7"} {
		got := stages[0].combinations[i]
		if len(got) != 1 || got[0].path != "cdcl.glucoseK" || got[0].value != want {
			t.Errorf("combination %d = %v, want [{cdcl.glucoseK %s}]", i, got, want)
		}
	}
}

func TestParseSweepSpecMultipleStages(t *testing.T) {
	stages, err := parseSweepSpec("cdcl.glucoseK=0.5|0.6;preprocess.bveWorkBudgetFactor=1000|2000|4000")
	if err != nil {
		t.Fatalf("parseSweepSpec: %v", err)
	}
	if len(stages) != 2 {
		t.Fatalf("got %d stages, want 2", len(stages))
	}
	if len(stages[0].combinations) != 2 {
		t.Errorf("stage 1: got %d combinations, want 2", len(stages[0].combinations))
	}
	if len(stages[1].combinations) != 3 {
		t.Errorf("stage 2: got %d combinations, want 3", len(stages[1].combinations))
	}
}

func TestParseSweepSpecJointGrid(t *testing.T) {
	stages, err := parseSweepSpec("cdcl.glucoseK=0.5|0.6,cdcl.lrbAlpha=0.3|0.4")
	if err != nil {
		t.Fatalf("parseSweepSpec: %v", err)
	}
	if len(stages) != 1 {
		t.Fatalf("got %d stages, want 1", len(stages))
	}
	if len(stages[0].combinations) != 4 {
		t.Fatalf("got %d combinations, want 4 (2x2 cartesian product)", len(stages[0].combinations))
	}
	seen := map[string]bool{}
	for _, c := range stages[0].combinations {
		if len(c) != 2 {
			t.Fatalf("combination %v has %d assignments, want 2", c, len(c))
		}
		seen[c.describe()] = true
	}
	for _, want := range []string{
		"cdcl.glucoseK=0.5, cdcl.lrbAlpha=0.3",
		"cdcl.glucoseK=0.5, cdcl.lrbAlpha=0.4",
		"cdcl.glucoseK=0.6, cdcl.lrbAlpha=0.3",
		"cdcl.glucoseK=0.6, cdcl.lrbAlpha=0.4",
	} {
		if !seen[want] {
			t.Errorf("missing expected combination %q among %v", want, seen)
		}
	}
}

func TestParseSweepSpecErrors(t *testing.T) {
	cases := []string{
		"",
		"cdcl.glucoseK",  // no "="
		"=0.5|0.6",       // no path
		"cdcl.glucoseK=", // no values
		";",              // stage with nothing in it
	}
	for _, spec := range cases {
		if _, err := parseSweepSpec(spec); err == nil {
			t.Errorf("parseSweepSpec(%q): expected an error, got nil", spec)
		}
	}
}
