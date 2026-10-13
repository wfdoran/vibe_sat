package params

import (
	"math"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadKeepsDefaultsForOmittedFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partial.json")
	if err := writeFile(path, `{"cdcl": {"glucoseK": 0.9}}`); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	p, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.CDCL.GlucoseK != 0.9 {
		t.Errorf("CDCL.GlucoseK = %v, want 0.9 (explicitly set)", p.CDCL.GlucoseK)
	}
	want := Default()
	if p.CDCL.LubyBaseConflicts != want.CDCL.LubyBaseConflicts {
		t.Errorf("CDCL.LubyBaseConflicts = %v, want default %v (omitted from the file)", p.CDCL.LubyBaseConflicts, want.CDCL.LubyBaseConflicts)
	}
	if p.Preprocess.BVEWorkBudgetFactor != want.Preprocess.BVEWorkBudgetFactor {
		t.Errorf("Preprocess.BVEWorkBudgetFactor = %v, want default %v (whole object omitted)", p.Preprocess.BVEWorkBudgetFactor, want.Preprocess.BVEWorkBudgetFactor)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "does-not-exist.json")); err == nil {
		t.Fatal("Load on a missing file succeeded, want an error")
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := writeFile(path, `{not valid json`); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load on invalid JSON succeeded, want an error")
	}
}

func TestSaveRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roundtrip.json")
	original := Default()
	original.CDCL.GlucoseK = 0.42
	original.Preprocess.BVEWorkBudgetFactor = 12345

	if err := Save(path, original); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if got != original {
		t.Errorf("round-tripped Params = %+v, want %+v", got, original)
	}
}

func TestResolvePrefersExplicitPath(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "explicit.json")
	if err := writeFile(explicit, `{"cdcl": {"glucoseK": 0.11}}`); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	// An implicit .vibe_sat.json in the current directory must be
	// ignored once an explicit path is given.
	restore := chdirTemp(t, dir)
	defer restore()
	if err := writeFile(DefaultConfigFileName, `{"cdcl": {"glucoseK": 0.22}}`); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	p, source, err := Resolve(explicit)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if source != explicit {
		t.Errorf("source = %q, want %q", source, explicit)
	}
	if p.CDCL.GlucoseK != 0.11 {
		t.Errorf("CDCL.GlucoseK = %v, want 0.11 (from the explicit path, not the implicit file)", p.CDCL.GlucoseK)
	}
}

func TestResolveFallsBackToImplicitFile(t *testing.T) {
	restore := chdirTemp(t, t.TempDir())
	defer restore()
	if err := writeFile(DefaultConfigFileName, `{"preprocess": {"bveWorkBudgetFactor": 500}}`); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	p, source, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if source != DefaultConfigFileName {
		t.Errorf("source = %q, want %q", source, DefaultConfigFileName)
	}
	if p.Preprocess.BVEWorkBudgetFactor != 500 {
		t.Errorf("Preprocess.BVEWorkBudgetFactor = %v, want 500", p.Preprocess.BVEWorkBudgetFactor)
	}
}

func TestResolveFallsBackToBuiltInDefaults(t *testing.T) {
	restore := chdirTemp(t, t.TempDir())
	defer restore()

	p, source, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if source != "built-in defaults" {
		t.Errorf("source = %q, want %q", source, "built-in defaults")
	}
	if p != Default() {
		t.Errorf("Resolve with no config file present = %+v, want Default() = %+v", p, Default())
	}
}

func TestResolveExplicitPathMissingIsAnError(t *testing.T) {
	if _, _, err := Resolve(filepath.Join(t.TempDir(), "does-not-exist.json")); err == nil {
		t.Fatal("Resolve with a missing explicit path succeeded, want an error")
	}
}

// paramCase is one file body for the range-validation tests, with
// the "section.key" an error must name (rejected cases only).
type paramCase struct {
	body string
	key  string
}

// rejectedParamCases has, for every field, values just outside its
// accepted range (and a few wild ones), so Go and Rust (which mirrors
// this table) are checked on exactly the same boundaries.
var rejectedParamCases = []paramCase{
	{`{"cdcl":{"lubyBaseConflicts":0}}`, "cdcl.lubyBaseConflicts"},
	{`{"cdcl":{"lubyBaseConflicts":-5}}`, "cdcl.lubyBaseConflicts"},
	{`{"cdcl":{"polynomialBaseConflicts":0}}`, "cdcl.polynomialBaseConflicts"},
	{`{"cdcl":{"geometricBaseConflicts":-1}}`, "cdcl.geometricBaseConflicts"},
	{`{"cdcl":{"geometricGrowthFactor":0.99}}`, "cdcl.geometricGrowthFactor"},
	{`{"cdcl":{"geometricGrowthFactor":0}}`, "cdcl.geometricGrowthFactor"},
	{`{"cdcl":{"geometricGrowthFactor":-2}}`, "cdcl.geometricGrowthFactor"},
	{`{"cdcl":{"lrbAlpha":0}}`, "cdcl.lrbAlpha"},
	{`{"cdcl":{"lrbAlpha":1.01}}`, "cdcl.lrbAlpha"},
	{`{"cdcl":{"clauseActivityDecay":0}}`, "cdcl.clauseActivityDecay"},
	{`{"cdcl":{"clauseActivityDecay":-0.5}}`, "cdcl.clauseActivityDecay"},
	{`{"cdcl":{"clauseActivityDecay":2}}`, "cdcl.clauseActivityDecay"},
	{`{"cdcl":{"varActivityDecay":0}}`, "cdcl.varActivityDecay"},
	{`{"cdcl":{"varActivityDecay":2.0}}`, "cdcl.varActivityDecay"},
	{`{"cdcl":{"glueClauseLBDThreshold":-1}}`, "cdcl.glueClauseLBDThreshold"},
	{`{"cdcl":{"glucoseWindowSize":0}}`, "cdcl.glucoseWindowSize"},
	{`{"cdcl":{"glucoseWindowSize":-5}}`, "cdcl.glucoseWindowSize"},
	{`{"cdcl":{"glucoseWindowSize":10000001}}`, "cdcl.glucoseWindowSize"},
	{`{"cdcl":{"glucoseWindowSize":100000000000}}`, "cdcl.glucoseWindowSize"},
	{`{"cdcl":{"lubyBaseConflicts":1000000001}}`, "cdcl.lubyBaseConflicts"},
	{`{"cdcl":{"lubyBaseConflicts":9223372036854775807}}`, "cdcl.lubyBaseConflicts"},
	{`{"cdcl":{"polynomialBaseConflicts":1000000001}}`, "cdcl.polynomialBaseConflicts"},
	{`{"cdcl":{"polynomialBaseConflicts":9223372036854775807}}`, "cdcl.polynomialBaseConflicts"},
	{`{"cdcl":{"minimizeWorkBudgetFactor":1000000001}}`, "cdcl.minimizeWorkBudgetFactor"},
	{`{"cdcl":{"minimizeWorkBudgetFactor":9223372036854775807}}`, "cdcl.minimizeWorkBudgetFactor"},
	{`{"preprocess":{"subsumptionWorkBudgetFactor":1000000001}}`, "preprocess.subsumptionWorkBudgetFactor"},
	{`{"preprocess":{"subsumptionWorkBudgetFactor":9223372036854775807}}`, "preprocess.subsumptionWorkBudgetFactor"},
	{`{"preprocess":{"bveWorkBudgetFactor":1000000001}}`, "preprocess.bveWorkBudgetFactor"},
	{`{"preprocess":{"bveWorkBudgetFactor":9223372036854775807}}`, "preprocess.bveWorkBudgetFactor"},
	{`{"cdcl":{"glucoseK":0}}`, "cdcl.glucoseK"},
	{`{"cdcl":{"glucoseK":-0.6}}`, "cdcl.glucoseK"},
	{`{"cdcl":{"minimizeWorkBudgetFactor":-1}}`, "cdcl.minimizeWorkBudgetFactor"},
	{`{"cdcl":{"rephaseIntervalRestarts":0}}`, "cdcl.rephaseIntervalRestarts"},
	{`{"cdcl":{"rephaseIntervalRestarts":-3}}`, "cdcl.rephaseIntervalRestarts"},
	{`{"cdcl":{"rephaseMaxFlips":0}}`, "cdcl.rephaseMaxFlips"},
	{`{"cdcl":{"vmtfThreadFraction":-0.1}}`, "cdcl.vmtfThreadFraction"},
	{`{"cdcl":{"vmtfThreadFraction":1.5}}`, "cdcl.vmtfThreadFraction"},
	{`{"preprocess":{"subsumptionWorkBudgetFactor":-1}}`, "preprocess.subsumptionWorkBudgetFactor"},
	{`{"preprocess":{"bveWorkBudgetFactor":-1}}`, "preprocess.bveWorkBudgetFactor"},
}

// acceptedParamCases has the boundary values that must still load.
var acceptedParamCases = []string{
	`{"cdcl":{"lubyBaseConflicts":1,"polynomialBaseConflicts":1,"geometricBaseConflicts":1}}`,
	`{"cdcl":{"geometricGrowthFactor":1}}`,
	`{"cdcl":{"geometricGrowthFactor":1000}}`,
	`{"cdcl":{"lrbAlpha":1,"clauseActivityDecay":1,"varActivityDecay":1}}`,
	`{"cdcl":{"lrbAlpha":0.0001,"clauseActivityDecay":0.0001,"varActivityDecay":0.0001}}`,
	`{"cdcl":{"glueClauseLBDThreshold":0}}`,
	`{"cdcl":{"glucoseWindowSize":1}}`,
	`{"cdcl":{"glucoseWindowSize":10000000}}`,
	`{"cdcl":{"lubyBaseConflicts":1000000000,"polynomialBaseConflicts":1000000000,"minimizeWorkBudgetFactor":1000000000}}`,
	`{"preprocess":{"subsumptionWorkBudgetFactor":1000000000,"bveWorkBudgetFactor":1000000000}}`,
	`{"cdcl":{"glucoseK":0.0001}}`,
	`{"cdcl":{"glucoseK":1000}}`,
	`{"cdcl":{"minimizeWorkBudgetFactor":0}}`,
	`{"cdcl":{"rephaseIntervalRestarts":1,"rephaseMaxFlips":1}}`,
	`{"cdcl":{"vmtfThreadFraction":0}}`,
	`{"cdcl":{"vmtfThreadFraction":1}}`,
	`{"preprocess":{"subsumptionWorkBudgetFactor":0,"bveWorkBudgetFactor":0}}`,
	`{}`,
}

// TestLoadRejectsOutOfRangeParameters verifies Load refuses every
// out-of-range value with an error that names the offending
// "section.key" and says what is accepted, instead of letting it
// through to wreck (or crash) the solver (STAGE56.md).
func TestLoadRejectsOutOfRangeParameters(t *testing.T) {
	for _, tc := range rejectedParamCases {
		path := filepath.Join(t.TempDir(), "bad.json")
		if err := writeFile(path, tc.body); err != nil {
			t.Fatalf("writeFile: %v", err)
		}
		_, err := Load(path)
		if err == nil {
			t.Errorf("Load(%s) succeeded, want an error naming %s", tc.body, tc.key)
			continue
		}
		if !strings.Contains(err.Error(), tc.key+" must be ") {
			t.Errorf("Load(%s) error = %q, want it to contain %q", tc.body, err, tc.key+" must be ")
		}
	}
}

// TestLoadAcceptsBoundaryParameters verifies the inclusive ends of
// every range still load, so the validation is not stricter than the
// documented ranges.
func TestLoadAcceptsBoundaryParameters(t *testing.T) {
	for _, body := range acceptedParamCases {
		path := filepath.Join(t.TempDir(), "ok.json")
		if err := writeFile(path, body); err != nil {
			t.Fatalf("writeFile: %v", err)
		}
		if _, err := Load(path); err != nil {
			t.Errorf("Load(%s) = %v, want success", body, err)
		}
	}
}

// TestValidateAcceptsDefaults verifies the built-in defaults satisfy
// every range, so a run with no config file can never be rejected.
func TestValidateAcceptsDefaults(t *testing.T) {
	if err := validate(Default()); err != nil {
		t.Errorf("validate(Default()) = %v, want nil", err)
	}
}

// TestValidateRejectsNaNAndInfinity verifies the float checks cannot be
// bypassed by NaN or +/-Inf (which no JSON file can spell, but a
// programmatic caller or a future loader could produce).
func TestValidateRejectsNaNAndInfinity(t *testing.T) {
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		for _, set := range []func(*Params, float64){
			func(p *Params, v float64) { p.CDCL.GeometricGrowthFactor = v },
			func(p *Params, v float64) { p.CDCL.LRBAlpha = v },
			func(p *Params, v float64) { p.CDCL.ClauseActivityDecay = v },
			func(p *Params, v float64) { p.CDCL.VarActivityDecay = v },
			func(p *Params, v float64) { p.CDCL.GlucoseK = v },
		} {
			p := Default()
			set(&p, bad)
			if err := validate(p); err == nil {
				t.Errorf("validate accepted %v", bad)
			}
		}
	}
}

// TestLoadRejectsOnlyTheFirstViolationInFieldOrder pins that Load
// reports one error, for the first field (in struct order) out of range.
func TestLoadRejectsOnlyTheFirstViolationInFieldOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "two.json")
	if err := writeFile(path, `{"cdcl":{"varActivityDecay":2,"lubyBaseConflicts":0}}`); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "cdcl.lubyBaseConflicts") || strings.Contains(err.Error(), "varActivityDecay") {
		t.Errorf("Load error = %v, want only cdcl.lubyBaseConflicts reported", err)
	}
}

// TestLoadRejectsWrongJSONTypesAndAcceptsNull verifies a value of the
// wrong JSON type (string for a number, fraction for an integer field,
// non-object section) is an error rather than being ignored or
// truncated, and that null leaves the default in place. Mirrored in
// Rust, which parses JSON by hand and so must check these explicitly.
func TestLoadRejectsWrongJSONTypesAndAcceptsNull(t *testing.T) {
	for _, body := range []string{
		`{"cdcl":{"lubyBaseConflicts":"5"}}`,
		`{"cdcl":{"lubyBaseConflicts":1.5}}`,
		`{"cdcl":{"glucoseK":"0.6"}}`,
		`{"cdcl":{"glucoseK":true}}`,
		`{"cdcl":5}`,
		`{"preprocess":[1]}`,
	} {
		path := filepath.Join(t.TempDir(), "type.json")
		if err := writeFile(path, body); err != nil {
			t.Fatalf("writeFile: %v", err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("Load(%s) succeeded, want an error", body)
		}
	}
	path := filepath.Join(t.TempDir(), "null.json")
	if err := writeFile(path, `{"cdcl":{"lubyBaseConflicts":null,"glucoseK":null},"preprocess":null}`); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	p, err := Load(path)
	if err != nil || p != Default() {
		t.Errorf("Load with nulls = (%+v, %v), want (Default(), nil)", p, err)
	}
}
