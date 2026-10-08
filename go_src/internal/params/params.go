// Package params implements STAGE39.md's runtime-configurable
// "internal parameters": the subset of vibe_sat's tuning constants
// that have either already been benchmark-tuned (RestartGlucose's K,
// STAGE35.md) or explicitly flagged as tuning candidates
// (REPORT22.md's item 7/12, REPORT31.md's bveWorkBudgetFactor) --
// previously compile-time-only Go/Rust constants, now loadable from a
// JSON file so a benchmark sweep (see util/paramtune) can try
// different values without a rebuild.
//
// Deliberately not every internal constant this project has (see
// docs/internal-parameters.md for the full inventory): purely
// structural/definitional constants (a byte-size estimate, a numeric
// safety cap meant to never bind) and cadence/bookkeeping constants
// whose value doesn't change solving *quality* (how often a
// background check runs, a ring buffer's slot count) are left as
// compile-time constants, since exposing them would add real surface
// area -- a config field, a JSON key, a doc entry -- for something
// nobody has ever had a reason to want different. hc/ws's own tunables
// (restart count, max flips, noise percent) are excluded for a
// different reason: they're already runtime-configurable per run, via
// --alg-params, so a second, overlapping configuration mechanism would
// only raise the question of which one wins.
package params

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// DefaultConfigFileName is the config file Resolve looks for in the
// current working directory when --internal-params isn't given
// (STAGE39.md's "implicit" loading half) and the file
// --reset-internal-params writes to when --internal-params also isn't
// given.
const DefaultConfigFileName = ".vibe_sat.json"

// CDCL holds --algorithm=cdcl's runtime-configurable tuning constants.
// Field names and JSON keys match the Go constant names they replace
// (see docs/internal-parameters.md) exactly, so a user reading
// cdcl.go's own doc comments alongside a .vibe_sat.json file needs no
// translation.
type CDCL struct {
	LubyBaseConflicts        int     `json:"lubyBaseConflicts"`
	PolynomialBaseConflicts  int     `json:"polynomialBaseConflicts"`
	GeometricBaseConflicts   int     `json:"geometricBaseConflicts"`
	GeometricGrowthFactor    float64 `json:"geometricGrowthFactor"`
	LRBAlpha                 float64 `json:"lrbAlpha"`
	ClauseActivityDecay      float64 `json:"clauseActivityDecay"`
	VarActivityDecay         float64 `json:"varActivityDecay"`
	GlueClauseLBDThreshold   int     `json:"glueClauseLBDThreshold"`
	GlucoseWindowSize        int     `json:"glucoseWindowSize"`
	GlucoseK                 float64 `json:"glucoseK"`
	MinimizeWorkBudgetFactor int     `json:"minimizeWorkBudgetFactor"`

	// RephaseIntervalRestarts and RephaseMaxFlips are STAGE43.md's
	// periodic-WalkSAT-rephasing parameters (cdcl.PhaseRephaseWalkSAT;
	// REPORT33.md item 6): a WalkSAT burst runs every
	// RephaseIntervalRestarts restarts, bounded to RephaseMaxFlips
	// flips, and its resulting assignment overwrites cdcl's saved-phase
	// array (see cdcl.go's rephaseFromWalkSAT). Meaningless for every
	// other PhaseStrategy, which never read either field.
	RephaseIntervalRestarts int `json:"rephaseIntervalRestarts"`
	RephaseMaxFlips         int `json:"rephaseMaxFlips"`
}

// Preprocess holds Stage-8 preprocessing's runtime-configurable
// tuning constants.
type Preprocess struct {
	SubsumptionWorkBudgetFactor int `json:"subsumptionWorkBudgetFactor"`
	BVEWorkBudgetFactor         int `json:"bveWorkBudgetFactor"`
}

// Params is the top-level shape of a .vibe_sat.json file.
type Params struct {
	CDCL       CDCL       `json:"cdcl"`
	Preprocess Preprocess `json:"preprocess"`
}

// Default returns the built-in defaults: exactly the values every one
// of these fields' replaced compile-time constant held before
// STAGE39.md, so a run with no config file present behaves
// byte-for-byte identically to every prior stage -- except
// LubyBaseConflicts, recalibrated by STAGE49.md's paramtune sweep
// (see reports/REPORT49.md): the literature-standard 100 (MiniSat's
// own default, kept as-is by STAGE15.md's original preference for a
// literature value) measurably underperforms on this project's own
// benchmark set, exactly as REPORT15.md's own conflict-rate reasoning
// predicted.
func Default() Params {
	return Params{
		CDCL: CDCL{
			LubyBaseConflicts:        4000,
			PolynomialBaseConflicts:  18000,
			GeometricBaseConflicts:   100,
			GeometricGrowthFactor:    1.5,
			LRBAlpha:                 0.4,
			ClauseActivityDecay:      0.999,
			VarActivityDecay:         0.95,
			GlueClauseLBDThreshold:   2,
			GlucoseWindowSize:        50,
			GlucoseK:                 0.6,
			MinimizeWorkBudgetFactor: 20,
			RephaseIntervalRestarts:  50,
			RephaseMaxFlips:          1000,
		},
		Preprocess: Preprocess{
			SubsumptionWorkBudgetFactor: 64,
			BVEWorkBudgetFactor:         2000,
		},
	}
}

// Load reads path as JSON and returns the resulting Params, starting
// from Default() and overwriting only the fields path's JSON actually
// sets -- an omitted key (or an entirely missing "cdcl"/"preprocess"
// object) keeps its default rather than zeroing out, so a user's
// .vibe_sat.json only needs to mention the handful of values they
// actually want to override.
//
// The merged result is then range-checked by validate (STAGE56.md): an
// out-of-range value, or a recognized field of the wrong JSON type, is
// an error naming the field, never silently accepted.
func Load(path string) (Params, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Params{}, err
	}
	p := Default()
	if err := json.Unmarshal(data, &p); err != nil {
		return Params{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := validate(p); err != nil {
		return Params{}, fmt.Errorf("invalid %s: %w", path, err)
	}
	return p, nil
}

// MaxGlucoseWindowSize is the largest accepted cdcl.glucoseWindowSize.
// The window is a ring buffer allocated up front for every cdcl run, so
// an absurd size is a crash (out of memory), not just a bad tuning; ten
// million is already about five orders of magnitude past the default of
// 50 and costs at most 80 MB.
const MaxGlucoseWindowSize = 10_000_000

// MaxScaleFactor is the largest accepted value of the five integer
// parameters that get multiplied by a problem- or run-sized quantity:
// the three work-budget factors (times a clause count, or a learned
// clause's length times its log) and the Luby and polynomial restart
// bases (times a Luby term or a squared restart count). One billion is
// over five orders of magnitude past the largest default (18000) and
// already means "never binds" in practice, but keeps every such product
// far below 2^63 for any formula or run that fits in memory. Without a
// cap, a value like INT64_MAX (a natural way to spell "unlimited")
// silently wraps: in Go it disabled BVE on formulas with an even clause
// count (STAGE58.md / reports/REPORT58.md).
const MaxScaleFactor = 1_000_000_000

// validate rejects parameter values that either crash the solver
// (glucoseWindowSize outside [1, MaxGlucoseWindowSize]: it sizes the LBD
// ring buffer allocated for every cdcl run, so 0 or a negative size
// panics and an enormous one runs out of memory) or silently wreck
// search quality instead of tuning it (STAGE56.md: e.g. a
// varActivityDecay of 2.0 makes activities shrink, and uf250-01 stops
// reaching a verdict in 3 s). The accepted range of every field is
// listed in --help and docs/internal-parameters.md; the checks below
// are in struct field order and report only the first violation, as
// "<section>.<key> must be <range> (got <value>)". NaN and infinities
// are rejected wherever a float field has no upper bound.
func validate(p Params) error {
	c := p.CDCL
	checks := []error{
		rangeInt("cdcl.lubyBaseConflicts", c.LubyBaseConflicts, 1, MaxScaleFactor),
		rangeInt("cdcl.polynomialBaseConflicts", c.PolynomialBaseConflicts, 1, MaxScaleFactor),
		minInt("cdcl.geometricBaseConflicts", c.GeometricBaseConflicts, 1),
		minFloat("cdcl.geometricGrowthFactor", c.GeometricGrowthFactor, 1),
		unitFloat("cdcl.lrbAlpha", c.LRBAlpha),
		unitFloat("cdcl.clauseActivityDecay", c.ClauseActivityDecay),
		unitFloat("cdcl.varActivityDecay", c.VarActivityDecay),
		minInt("cdcl.glueClauseLBDThreshold", c.GlueClauseLBDThreshold, 0),
		rangeInt("cdcl.glucoseWindowSize", c.GlucoseWindowSize, 1, MaxGlucoseWindowSize),
		positiveFloat("cdcl.glucoseK", c.GlucoseK),
		rangeInt("cdcl.minimizeWorkBudgetFactor", c.MinimizeWorkBudgetFactor, 0, MaxScaleFactor),
		minInt("cdcl.rephaseIntervalRestarts", c.RephaseIntervalRestarts, 1),
		minInt("cdcl.rephaseMaxFlips", c.RephaseMaxFlips, 1),
		rangeInt("preprocess.subsumptionWorkBudgetFactor", p.Preprocess.SubsumptionWorkBudgetFactor, 0, MaxScaleFactor),
		rangeInt("preprocess.bveWorkBudgetFactor", p.Preprocess.BVEWorkBudgetFactor, 0, MaxScaleFactor),
	}
	for _, err := range checks {
		if err != nil {
			return err
		}
	}
	return nil
}

// minInt returns an error naming key unless v >= min.
func minInt(key string, v, min int) error {
	if v < min {
		return fmt.Errorf("%s must be at least %d (got %d)", key, min, v)
	}
	return nil
}

// rangeInt returns an error naming key unless min <= v <= max.
func rangeInt(key string, v, min, max int) error {
	if v < min || v > max {
		return fmt.Errorf("%s must be between %d and %d (got %d)", key, min, max, v)
	}
	return nil
}

// minFloat returns an error naming key unless v is a finite number
// that is at least min.
func minFloat(key string, v, min float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < min {
		return fmt.Errorf("%s must be a finite number at least %v (got %v)", key, min, v)
	}
	return nil
}

// positiveFloat returns an error naming key unless v is a finite
// number greater than 0.
func positiveFloat(key string, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return fmt.Errorf("%s must be a finite number greater than 0 (got %v)", key, v)
	}
	return nil
}

// unitFloat returns an error naming key unless 0 < v <= 1 (NaN fails
// both comparisons and is rejected too).
func unitFloat(key string, v float64) error {
	if !(v > 0 && v <= 1) {
		return fmt.Errorf("%s must be greater than 0 and at most 1 (got %v)", key, v)
	}
	return nil
}

// Save writes p to path as indented JSON, creating or truncating the
// file. Used by --reset-internal-params (called with Default()) so a
// user has a complete, correctly-shaped starting point to edit rather
// than needing to know every field name and its default value ahead
// of time.
func Save(path string, p Params) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// Resolve implements STAGE39.md's load precedence: explicitPath (from
// --internal-params), if given, must exist and parse -- an explicit
// request for a specific file is never silently ignored. Otherwise,
// DefaultConfigFileName is loaded if present in the current working
// directory (the "implicit" half: a .vibe_sat.json left lying around
// is picked up with no flag needed, the same convention tools like
// git/eslint/cargo already use); if that file doesn't exist either,
// Default() is returned unchanged. source describes which of the
// three happened, for callers that want to report it (main prints it
// at --verbose >= 1, so loading a config file -- implicit or explicit
// -- is never silently surprising).
func Resolve(explicitPath string) (p Params, source string, err error) {
	if explicitPath != "" {
		p, err = Load(explicitPath)
		if err != nil {
			return Params{}, "", err
		}
		return p, explicitPath, nil
	}
	if _, statErr := os.Stat(DefaultConfigFileName); statErr == nil {
		p, err = Load(DefaultConfigFileName)
		if err != nil {
			return Params{}, "", err
		}
		return p, DefaultConfigFileName, nil
	}
	return Default(), "built-in defaults", nil
}
