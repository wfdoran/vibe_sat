package main

// A deliberately independent, minimal copy of go_src/internal/params's
// JSON shape and defaults (STAGE39.md). paramtune is its own Go
// module under util/, per PROMPT.md's Stage 18 carve-out -- like
// benchcompare's cnf.go/verify.go, it is structurally unable to import
// an "internal" package from a different module, so the shape is
// reproduced here rather than shared. Keep this in sync with
// go_src/internal/params/params.go and docs/internal-parameters.md by
// hand if either changes.

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type cdclParams struct {
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

type preprocessParams struct {
	SubsumptionWorkBudgetFactor int `json:"subsumptionWorkBudgetFactor"`
	BVEWorkBudgetFactor         int `json:"bveWorkBudgetFactor"`
}

// vibeSatParams is the top-level shape of a .vibe_sat.json file --
// must match go_src/internal/params.Params field-for-field.
type vibeSatParams struct {
	CDCL       cdclParams       `json:"cdcl"`
	Preprocess preprocessParams `json:"preprocess"`
}

// defaultParams returns the same built-in defaults as
// go_src/internal/params.Default() and rust_src's equivalent.
func defaultParams() vibeSatParams {
	return vibeSatParams{
		CDCL: cdclParams{
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
		Preprocess: preprocessParams{
			SubsumptionWorkBudgetFactor: 64,
			BVEWorkBudgetFactor:         2000,
		},
	}
}

// writeParamsFile writes p as indented JSON to the file
// ".vibe_sat.json" inside dir -- exactly the file name and shape
// vibe_sat's own implicit config discovery looks for (STAGE39.md's
// params.Resolve), so a vibe_sat binary run with dir as its current
// working directory picks p up automatically, with no
// --internal-params flag needed.
func writeParamsFile(dir string, p vibeSatParams) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(dir+"/.vibe_sat.json", data, 0o644)
}

// setByPath sets the field named by a dotted "section.field" path
// (e.g. "cdcl.glucoseK", "preprocess.bveWorkBudgetFactor" -- matching
// a .vibe_sat.json file's own JSON keys exactly, so a --sweep
// argument reads the same way a config file would) on p to value,
// parsed as an int or float64 to match the field's own type.
func setByPath(p *vibeSatParams, path string, value string) error {
	field, err := fieldByPath(p, path)
	if err != nil {
		return err
	}
	switch field.Kind() {
	case reflect.Int:
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("parameter %s: %q is not an integer", path, value)
		}
		field.SetInt(int64(n))
	case reflect.Float64:
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("parameter %s: %q is not a number", path, value)
		}
		field.SetFloat(f)
	default:
		return fmt.Errorf("parameter %s has unsupported type %s", path, field.Kind())
	}
	return nil
}

// formatByPath reads the field named by path back out as a string,
// for reporting (e.g. "the winning value of cdcl.glucoseK was 0.7").
func formatByPath(p vibeSatParams, path string) (string, error) {
	field, err := fieldByPath(&p, path)
	if err != nil {
		return "", err
	}
	switch field.Kind() {
	case reflect.Int:
		return strconv.FormatInt(field.Int(), 10), nil
	case reflect.Float64:
		return strconv.FormatFloat(field.Float(), 'g', -1, 64), nil
	default:
		return "", fmt.Errorf("parameter %s has unsupported type %s", path, field.Kind())
	}
}

// fieldByPath resolves "section.field" (e.g. "cdcl.glucoseK") against
// p's JSON tags -- not its Go field names -- so a --sweep argument
// can be written exactly as it would appear in a .vibe_sat.json file.
func fieldByPath(p *vibeSatParams, path string) (reflect.Value, error) {
	parts := strings.SplitN(path, ".", 2)
	if len(parts) != 2 {
		return reflect.Value{}, fmt.Errorf("invalid parameter path %q: expected \"section.field\", e.g. \"cdcl.glucoseK\" (see docs/internal-parameters.md)", path)
	}
	top := reflect.ValueOf(p).Elem()
	section, ok := fieldByJSONTag(top, parts[0])
	if !ok {
		return reflect.Value{}, fmt.Errorf("unknown parameter section %q (known sections: cdcl, preprocess)", parts[0])
	}
	field, ok := fieldByJSONTag(section, parts[1])
	if !ok {
		return reflect.Value{}, fmt.Errorf("unknown parameter %q in section %q (see docs/internal-parameters.md or --list-params for the full set)", parts[1], parts[0])
	}
	return field, nil
}

// fieldByJSONTag finds the field of struct value v whose `json:"..."`
// tag equals tag.
func fieldByJSONTag(v reflect.Value, tag string) (reflect.Value, bool) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		jsonTag := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if jsonTag == tag {
			return v.Field(i), true
		}
	}
	return reflect.Value{}, false
}

// allParamPaths lists every known "section.field" path, sorted, for
// --list-params.
func allParamPaths() []string {
	var paths []string
	def := defaultParams()
	top := reflect.ValueOf(def)
	topType := top.Type()
	for i := 0; i < topType.NumField(); i++ {
		section := strings.Split(topType.Field(i).Tag.Get("json"), ",")[0]
		sv := top.Field(i)
		st := sv.Type()
		for j := 0; j < st.NumField(); j++ {
			field := strings.Split(st.Field(j).Tag.Get("json"), ",")[0]
			paths = append(paths, section+"."+field)
		}
	}
	sort.Strings(paths)
	return paths
}
