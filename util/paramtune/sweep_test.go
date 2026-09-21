package main

import (
	"errors"
	"testing"
)

// fakeRunner scores a candidate purely from the vibeSatParams it was
// given, with no subprocess involved -- this is what lets
// TestRunSweepCarriesWinnerForward assert on runSweep's own sequencing
// logic (does it fix stage 1's winner before stage 2 runs?) in
// isolation from benchcompare/vibe_sat entirely.
func fakeRunner(score func(p vibeSatParams) float64) candidateRunner {
	return func(cfg sweepConfig, p vibeSatParams) ([]bcFileResult, error) {
		elapsed := score(p)
		return []bcFileResult{{
			File: "fake.cnf",
			Go:   bcRunOutcome{Verdict: "SAT", ElapsedSeconds: elapsed, Solved: true},
			Rust: bcRunOutcome{Verdict: "SAT", ElapsedSeconds: elapsed, Solved: true},
		}}, nil
	}
}

func TestRunSweepPicksLowestElapsedWithinAStage(t *testing.T) {
	stages, err := parseSweepSpec("cdcl.glucoseK=0.5|0.6|0.7")
	if err != nil {
		t.Fatalf("parseSweepSpec: %v", err)
	}
	// Make 0.7 look fastest.
	runner := fakeRunner(func(p vibeSatParams) float64 {
		if p.CDCL.GlucoseK == 0.7 {
			return 1.0
		}
		return 10.0
	})

	_, overrides, err := runSweep(sweepConfig{}, stages, runner, nil)
	if err != nil {
		t.Fatalf("runSweep: %v", err)
	}
	if overrides["cdcl.glucoseK"] != "0.7" {
		t.Errorf("winning cdcl.glucoseK = %q, want %q", overrides["cdcl.glucoseK"], "0.7")
	}
}

func TestRunSweepCarriesWinnerForwardToNextStage(t *testing.T) {
	stages, err := parseSweepSpec("cdcl.glucoseK=0.5|0.9;cdcl.lrbAlpha=0.2|0.4")
	if err != nil {
		t.Fatalf("parseSweepSpec: %v", err)
	}
	// Stage 1 has 2 candidates (glucoseK=0.5|0.9); 0.9 should win.
	// Stage 2 has 2 more (lrbAlpha=0.2|0.4); by then glucoseK must
	// already be fixed at 0.9, not its 0.6 default -- this is the
	// actual coordinate-descent behavior under test.
	var callCount int
	var sawGlucoseKDuringStage2 []float64
	runner := func(cfg sweepConfig, p vibeSatParams) ([]bcFileResult, error) {
		callCount++
		var elapsed float64
		switch {
		case callCount <= 2: // stage 1: glucoseK=0.5 then glucoseK=0.9
			if p.CDCL.GlucoseK == 0.9 {
				elapsed = 1.0
			} else {
				elapsed = 10.0
			}
		default: // stage 2: lrbAlpha=0.2 then lrbAlpha=0.4
			sawGlucoseKDuringStage2 = append(sawGlucoseKDuringStage2, p.CDCL.GlucoseK)
			if p.CDCL.LRBAlpha == 0.4 {
				elapsed = 1.0
			} else {
				elapsed = 5.0
			}
		}
		return []bcFileResult{{
			File: "fake.cnf",
			Go:   bcRunOutcome{Verdict: "SAT", ElapsedSeconds: elapsed, Solved: true},
			Rust: bcRunOutcome{Verdict: "SAT", ElapsedSeconds: elapsed, Solved: true},
		}}, nil
	}

	_, overrides, err := runSweep(sweepConfig{}, stages, runner, nil)
	if err != nil {
		t.Fatalf("runSweep: %v", err)
	}
	if overrides["cdcl.glucoseK"] != "0.9" {
		t.Errorf("winning cdcl.glucoseK = %q, want %q", overrides["cdcl.glucoseK"], "0.9")
	}
	if overrides["cdcl.lrbAlpha"] != "0.4" {
		t.Errorf("winning cdcl.lrbAlpha = %q, want %q", overrides["cdcl.lrbAlpha"], "0.4")
	}
	for _, gk := range sawGlucoseKDuringStage2 {
		if gk != 0.9 {
			t.Errorf("stage 2 saw cdcl.glucoseK = %v, want it fixed at 0.9 (stage 1's winner)", gk)
		}
	}
	if len(sawGlucoseKDuringStage2) == 0 {
		t.Fatal("test bug: never detected stage 2 running at all")
	}
}

func TestRunSweepReturnsErrorWhenEveryCandidateFails(t *testing.T) {
	stages, err := parseSweepSpec("cdcl.glucoseK=0.5|0.6")
	if err != nil {
		t.Fatalf("parseSweepSpec: %v", err)
	}
	runner := func(cfg sweepConfig, p vibeSatParams) ([]bcFileResult, error) {
		return nil, errors.New("simulated failure")
	}
	if _, _, err := runSweep(sweepConfig{}, stages, runner, nil); err == nil {
		t.Fatal("expected an error when every candidate fails, got nil")
	}
}
