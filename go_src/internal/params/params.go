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
// byte-for-byte identically to every prior stage.
func Default() Params {
	return Params{
		CDCL: CDCL{
			LubyBaseConflicts:        100,
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
func Load(path string) (Params, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Params{}, err
	}
	p := Default()
	if err := json.Unmarshal(data, &p); err != nil {
		return Params{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return p, nil
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
