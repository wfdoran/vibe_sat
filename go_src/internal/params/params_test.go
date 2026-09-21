package params

import (
	"path/filepath"
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
