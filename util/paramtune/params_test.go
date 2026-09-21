package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSetByPathInt(t *testing.T) {
	p := defaultParams()
	if err := setByPath(&p, "preprocess.bveWorkBudgetFactor", "4000"); err != nil {
		t.Fatalf("setByPath: %v", err)
	}
	if p.Preprocess.BVEWorkBudgetFactor != 4000 {
		t.Errorf("BVEWorkBudgetFactor = %d, want 4000", p.Preprocess.BVEWorkBudgetFactor)
	}
}

func TestSetByPathFloat(t *testing.T) {
	p := defaultParams()
	if err := setByPath(&p, "cdcl.glucoseK", "0.9"); err != nil {
		t.Fatalf("setByPath: %v", err)
	}
	if p.CDCL.GlucoseK != 0.9 {
		t.Errorf("GlucoseK = %v, want 0.9", p.CDCL.GlucoseK)
	}
}

func TestSetByPathUnknownSection(t *testing.T) {
	p := defaultParams()
	if err := setByPath(&p, "bogus.glucoseK", "0.9"); err == nil {
		t.Fatal("expected an error for an unknown section, got nil")
	}
}

func TestSetByPathUnknownField(t *testing.T) {
	p := defaultParams()
	if err := setByPath(&p, "cdcl.bogusField", "0.9"); err == nil {
		t.Fatal("expected an error for an unknown field, got nil")
	}
}

func TestSetByPathInvalidPath(t *testing.T) {
	p := defaultParams()
	if err := setByPath(&p, "cdcl", "0.9"); err == nil {
		t.Fatal("expected an error for a path with no \".\", got nil")
	}
}

func TestSetByPathNotANumber(t *testing.T) {
	p := defaultParams()
	if err := setByPath(&p, "cdcl.glucoseK", "not-a-number"); err == nil {
		t.Fatal("expected an error for a non-numeric value, got nil")
	}
	if err := setByPath(&p, "preprocess.bveWorkBudgetFactor", "3.5"); err == nil {
		t.Fatal("expected an error assigning a float string to an int field, got nil")
	}
}

func TestFormatByPathRoundTrips(t *testing.T) {
	p := defaultParams()
	got, err := formatByPath(p, "cdcl.glucoseK")
	if err != nil {
		t.Fatalf("formatByPath: %v", err)
	}
	if got != "0.6" {
		t.Errorf("formatByPath(cdcl.glucoseK) = %q, want %q", got, "0.6")
	}
}

func TestAllParamPathsCoversKnownFields(t *testing.T) {
	paths := allParamPaths()
	want := []string{
		"cdcl.lubyBaseConflicts", "cdcl.glucoseK", "preprocess.bveWorkBudgetFactor",
		"preprocess.subsumptionWorkBudgetFactor",
	}
	set := map[string]bool{}
	for _, p := range paths {
		set[p] = true
	}
	for _, w := range want {
		if !set[w] {
			t.Errorf("allParamPaths() missing %q", w)
		}
	}
	if len(paths) != 13 {
		t.Errorf("allParamPaths() returned %d paths, want 13 (11 cdcl + 2 preprocess)", len(paths))
	}
}

func TestWriteParamsFileRoundTrips(t *testing.T) {
	dir := t.TempDir()
	p := defaultParams()
	p.CDCL.GlucoseK = 0.42
	if err := writeParamsFile(dir, p); err != nil {
		t.Fatalf("writeParamsFile: %v", err)
	}
	data, err := os.ReadFile(dir + "/.vibe_sat.json")
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	var loaded vibeSatParams
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("parsing back: %v", err)
	}
	if loaded.CDCL.GlucoseK != 0.42 {
		t.Errorf("GlucoseK after round trip = %v, want 0.42", loaded.CDCL.GlucoseK)
	}
}
