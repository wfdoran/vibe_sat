//! STAGE39.md: runtime-configurable "internal parameters" -- tuning
//! constants that used to be compile-time `const`s scattered across
//! `cdcl` and `preprocess`, now collected here and readable from an
//! optional `.vibe_sat.json` file (or an explicitly named file via
//! `--internal-params`), so a value can be experimented with (see
//! `util/paramtune`, once it exists) without a rebuild. Mirrors
//! `go_src/internal/params` field-for-field, including JSON key names,
//! so a config file written by one language's binary is a valid input
//! to the other's.
//!
//! No JSON crate is used: `Cargo.toml`'s existing dependency list
//! (clap, rand, crossbeam-deque, arc-swap) has never included a
//! general-purpose serialization crate, and this module's actual need
//! -- read/write one small, fixed, two-level object of named int/float
//! fields -- is well within what a few dozen lines of hand-written
//! parsing/serialization can do correctly, so pulling in `serde`/
//! `serde_json` for it would be a dependency far larger than the
//! problem.

use std::fmt;
use std::fs;

/// The implicit config file name `Resolve` looks for in the current
/// directory when no `--internal-params` path is given explicitly.
pub const DEFAULT_CONFIG_FILE_NAME: &str = ".vibe_sat.json";

/// Runtime-configurable tuning constants for `cdcl` (STAGE39.md). Field
/// order here is also the order fields are written in by [`save`],
/// matching `go_src/internal/params.CDCL`'s field order so a
/// freshly-written default file is byte-for-byte comparable between
/// the two languages.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Cdcl {
    pub luby_base_conflicts: usize,
    pub polynomial_base_conflicts: usize,
    pub geometric_base_conflicts: usize,
    pub geometric_growth_factor: f64,
    pub lrb_alpha: f64,
    pub clause_activity_decay: f64,
    pub var_activity_decay: f64,
    pub glue_clause_lbd_threshold: usize,
    pub glucose_window_size: usize,
    pub glucose_k: f64,
    pub minimize_work_budget_factor: usize,
    /// STAGE43.md's periodic-WalkSAT-rephasing parameters
    /// (`cdcl::PhaseStrategy::RephaseWalkSAT`; `reports/REPORT33.md`
    /// item 6): a WalkSAT burst runs every `rephase_interval_restarts`
    /// restarts, bounded to `rephase_max_flips` flips, and its
    /// resulting assignment overwrites `cdcl`'s saved-phase array.
    /// Meaningless for every other `PhaseStrategy`.
    pub rephase_interval_restarts: usize,
    pub rephase_max_flips: usize,
}

/// Runtime-configurable tuning constants for `preprocess` (STAGE39.md).
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Preprocess {
    pub subsumption_work_budget_factor: usize,
    pub bve_work_budget_factor: usize,
}

/// The full set of runtime-configurable internal parameters, as read
/// from or written to a `.vibe_sat.json`-shaped file.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Params {
    pub cdcl: Cdcl,
    pub preprocess: Preprocess,
}

/// Returns the built-in defaults: exactly the values every one of
/// these fields held as a compile-time constant before STAGE39.md --
/// except `luby_base_conflicts`, recalibrated by STAGE49.md's
/// paramtune sweep (see `reports/REPORT49.md`): the literature
/// standard 100 measurably underperforms on this project's own
/// benchmark set, exactly as `REPORT15.md`'s own conflict-rate
/// reasoning predicted.
pub fn default() -> Params {
    Params {
        cdcl: Cdcl {
            luby_base_conflicts: 4000,
            polynomial_base_conflicts: 18000,
            geometric_base_conflicts: 100,
            geometric_growth_factor: 1.5,
            lrb_alpha: 0.1,
            clause_activity_decay: 0.999,
            var_activity_decay: 0.95,
            glue_clause_lbd_threshold: 2,
            glucose_window_size: 50,
            glucose_k: 0.8,
            minimize_work_budget_factor: 20,
            rephase_interval_restarts: 50,
            rephase_max_flips: 1000,
        },
        preprocess: Preprocess {
            subsumption_work_budget_factor: 64,
            bve_work_budget_factor: 2000,
        },
    }
}

/// Reads and parses `path` as a `.vibe_sat.json`-shaped file, starting
/// from [`default`] and overlaying whatever fields the file actually
/// specifies -- a partial file (only some fields, or only one of
/// "cdcl"/"preprocess") is valid, and every field it omits keeps its
/// default value. Fields present in the file but not recognized by
/// this schema are ignored, not an error (matching Go's
/// `encoding/json` unmarshal-into-a-known-struct behavior), so a
/// config file written by a newer version of either binary still
/// loads under an older one.
///
/// Returns an error if `path` cannot be read (including "does not
/// exist"), its contents are not valid JSON, a recognized field has the
/// wrong JSON type (a string, or a fraction for an integer field), or a
/// value is outside its accepted range (see [`validate`]). `null` leaves
/// a field at its default, as in Go.
pub fn load(path: &str) -> Result<Params, String> {
    let contents = fs::read_to_string(path).map_err(|e| format!("open {path}: {e}"))?;
    let value = json::parse(&contents).map_err(|e| format!("parse {path}: {e}"))?;
    let mut fields = Fields::from_params(&default());
    apply(&mut fields, &value).map_err(|e| format!("parse {path}: {e}"))?;
    validate(&fields).map_err(|e| format!("invalid {path}: {e}"))?;
    Ok(fields.into_params())
}

/// Largest accepted `cdcl.glucoseWindowSize`. The window is a vector
/// allocated up front for every cdcl run, so an absurd size aborts the
/// process with an allocation failure rather than merely tuning badly;
/// ten million is about five orders of magnitude past the default of 50
/// and costs at most 80 MB.
pub const MAX_GLUCOSE_WINDOW_SIZE: i64 = 10_000_000;

/// Largest accepted value of the five integer parameters that get
/// multiplied by a problem- or run-sized quantity (the three work-budget
/// factors and the Luby and polynomial restart bases); the same value
/// as Go's `MaxScaleFactor`, and the reason is the same: it is over five
/// orders of magnitude past the largest default and never binds in
/// practice, yet keeps every such product far below 2^63 (without a cap
/// a huge value silently wraps in a release build).
pub const MAX_SCALE_FACTOR: i64 = 1_000_000_000;

/// Every parameter as a signed 64-bit integer or an f64, mirroring Go's
/// `int`/`float64` struct fields. [`Params`] stores counts as `usize`,
/// which cannot represent a negative value, so a file's `-5` would be
/// silently clamped to 0 if it were stored there directly; [`load`]
/// instead reads into this struct, validates it, and only then converts
/// to [`Params`], so Go and Rust reject exactly the same files.
#[derive(Debug, Clone, Copy)]
struct Fields {
    luby_base_conflicts: i64,
    polynomial_base_conflicts: i64,
    geometric_base_conflicts: i64,
    geometric_growth_factor: f64,
    lrb_alpha: f64,
    clause_activity_decay: f64,
    var_activity_decay: f64,
    glue_clause_lbd_threshold: i64,
    glucose_window_size: i64,
    glucose_k: f64,
    minimize_work_budget_factor: i64,
    rephase_interval_restarts: i64,
    rephase_max_flips: i64,
    subsumption_work_budget_factor: i64,
    bve_work_budget_factor: i64,
}

impl Fields {
    /// Widens `p` into the signed representation.
    fn from_params(p: &Params) -> Fields {
        Fields {
            luby_base_conflicts: p.cdcl.luby_base_conflicts as i64,
            polynomial_base_conflicts: p.cdcl.polynomial_base_conflicts as i64,
            geometric_base_conflicts: p.cdcl.geometric_base_conflicts as i64,
            geometric_growth_factor: p.cdcl.geometric_growth_factor,
            lrb_alpha: p.cdcl.lrb_alpha,
            clause_activity_decay: p.cdcl.clause_activity_decay,
            var_activity_decay: p.cdcl.var_activity_decay,
            glue_clause_lbd_threshold: p.cdcl.glue_clause_lbd_threshold as i64,
            glucose_window_size: p.cdcl.glucose_window_size as i64,
            glucose_k: p.cdcl.glucose_k,
            minimize_work_budget_factor: p.cdcl.minimize_work_budget_factor as i64,
            rephase_interval_restarts: p.cdcl.rephase_interval_restarts as i64,
            rephase_max_flips: p.cdcl.rephase_max_flips as i64,
            subsumption_work_budget_factor: p.preprocess.subsumption_work_budget_factor as i64,
            bve_work_budget_factor: p.preprocess.bve_work_budget_factor as i64,
        }
    }

    /// Narrows back to [`Params`]. Only called on a [`Fields`] that
    /// [`validate`] accepted, so every integer is non-negative.
    fn into_params(self) -> Params {
        Params {
            cdcl: Cdcl {
                luby_base_conflicts: self.luby_base_conflicts as usize,
                polynomial_base_conflicts: self.polynomial_base_conflicts as usize,
                geometric_base_conflicts: self.geometric_base_conflicts as usize,
                geometric_growth_factor: self.geometric_growth_factor,
                lrb_alpha: self.lrb_alpha,
                clause_activity_decay: self.clause_activity_decay,
                var_activity_decay: self.var_activity_decay,
                glue_clause_lbd_threshold: self.glue_clause_lbd_threshold as usize,
                glucose_window_size: self.glucose_window_size as usize,
                glucose_k: self.glucose_k,
                minimize_work_budget_factor: self.minimize_work_budget_factor as usize,
                rephase_interval_restarts: self.rephase_interval_restarts as usize,
                rephase_max_flips: self.rephase_max_flips as usize,
            },
            preprocess: Preprocess {
                subsumption_work_budget_factor: self.subsumption_work_budget_factor as usize,
                bve_work_budget_factor: self.bve_work_budget_factor as usize,
            },
        }
    }
}

/// Rejects parameter values that either crash the solver
/// (`glucoseWindowSize` outside `[1, MAX_GLUCOSE_WINDOW_SIZE]`: it sizes
/// the LBD vector allocated for every cdcl run, so 0 panics and an
/// enormous size aborts on allocation failure) or silently wreck search
/// quality instead of tuning it (STAGE56.md: e.g. a `varActivityDecay`
/// of 2.0 makes activities shrink, and uf250-01 stops reaching a verdict
/// in 3 s). The accepted range of every field is listed in `--help` and
/// `docs/internal-parameters.md`; checks run in field order (the same
/// order as Go's `validate`) and only the first violation is reported,
/// as "<section>.<key> must be <range> (got <value>)". NaN and
/// infinities are rejected wherever a float field has no upper bound.
fn validate(f: &Fields) -> Result<(), String> {
    range_int(
        "cdcl.lubyBaseConflicts",
        f.luby_base_conflicts,
        1,
        MAX_SCALE_FACTOR,
    )?;
    range_int(
        "cdcl.polynomialBaseConflicts",
        f.polynomial_base_conflicts,
        1,
        MAX_SCALE_FACTOR,
    )?;
    min_int("cdcl.geometricBaseConflicts", f.geometric_base_conflicts, 1)?;
    min_float("cdcl.geometricGrowthFactor", f.geometric_growth_factor, 1.0)?;
    unit_float("cdcl.lrbAlpha", f.lrb_alpha)?;
    unit_float("cdcl.clauseActivityDecay", f.clause_activity_decay)?;
    unit_float("cdcl.varActivityDecay", f.var_activity_decay)?;
    min_int(
        "cdcl.glueClauseLBDThreshold",
        f.glue_clause_lbd_threshold,
        0,
    )?;
    range_int(
        "cdcl.glucoseWindowSize",
        f.glucose_window_size,
        1,
        MAX_GLUCOSE_WINDOW_SIZE,
    )?;
    positive_float("cdcl.glucoseK", f.glucose_k)?;
    range_int(
        "cdcl.minimizeWorkBudgetFactor",
        f.minimize_work_budget_factor,
        0,
        MAX_SCALE_FACTOR,
    )?;
    min_int(
        "cdcl.rephaseIntervalRestarts",
        f.rephase_interval_restarts,
        1,
    )?;
    min_int("cdcl.rephaseMaxFlips", f.rephase_max_flips, 1)?;
    range_int(
        "preprocess.subsumptionWorkBudgetFactor",
        f.subsumption_work_budget_factor,
        0,
        MAX_SCALE_FACTOR,
    )?;
    range_int(
        "preprocess.bveWorkBudgetFactor",
        f.bve_work_budget_factor,
        0,
        MAX_SCALE_FACTOR,
    )?;
    Ok(())
}

/// Errors naming `key` unless `v >= min`.
fn min_int(key: &str, v: i64, min: i64) -> Result<(), String> {
    if v < min {
        return Err(format!("{key} must be at least {min} (got {v})"));
    }
    Ok(())
}

/// Errors naming `key` unless `min <= v <= max`.
fn range_int(key: &str, v: i64, min: i64, max: i64) -> Result<(), String> {
    if v < min || v > max {
        return Err(format!("{key} must be between {min} and {max} (got {v})"));
    }
    Ok(())
}

/// Errors naming `key` unless `v` is a finite number at least `min`.
fn min_float(key: &str, v: f64, min: f64) -> Result<(), String> {
    if !v.is_finite() || v < min {
        return Err(format!(
            "{key} must be a finite number at least {min} (got {v})"
        ));
    }
    Ok(())
}

/// Errors naming `key` unless `v` is a finite number greater than 0.
fn positive_float(key: &str, v: f64) -> Result<(), String> {
    if !v.is_finite() || v <= 0.0 {
        return Err(format!(
            "{key} must be a finite number greater than 0 (got {v})"
        ));
    }
    Ok(())
}

/// Errors naming `key` unless `0 < v <= 1` (NaN fails both comparisons
/// and is rejected too).
fn unit_float(key: &str, v: f64) -> Result<(), String> {
    if !(v > 0.0 && v <= 1.0) {
        return Err(format!(
            "{key} must be greater than 0 and at most 1 (got {v})"
        ));
    }
    Ok(())
}

/// Reads `obj[key]` as an integer into `slot`. A missing key or `null`
/// leaves `slot` alone; any other non-number, a number with a
/// fractional part, or one beyond the i64 range is an error (Go's
/// `encoding/json` rejects the same inputs when unmarshaling into an
/// `int`).
fn read_int(obj: &json::Value, section: &str, key: &str, slot: &mut i64) -> Result<(), String> {
    match obj.get(key) {
        None | Some(json::Value::Null) => Ok(()),
        Some(json::Value::Number(n))
            if n.fract() == 0.0 && n.abs() < 9_223_372_036_854_775_808.0 =>
        {
            *slot = *n as i64;
            Ok(())
        }
        Some(_) => Err(format!("{section}.{key} must be an integer")),
    }
}

/// Reads `obj[key]` as a float into `slot`; a missing key or `null`
/// leaves `slot` alone and any other non-number is an error.
fn read_float(obj: &json::Value, section: &str, key: &str, slot: &mut f64) -> Result<(), String> {
    match obj.get(key) {
        None | Some(json::Value::Null) => Ok(()),
        Some(json::Value::Number(n)) => {
            *slot = *n;
            Ok(())
        }
        Some(_) => Err(format!("{section}.{key} must be a number")),
    }
}

/// Overlays whatever fields `value` (a parsed JSON object, expected to
/// have "cdcl"/"preprocess" sub-objects) specifies onto `f` in place,
/// leaving every field it doesn't mention untouched. A "cdcl" or
/// "preprocess" entry that is present but is neither an object nor
/// `null` is an error.
fn apply(f: &mut Fields, value: &json::Value) -> Result<(), String> {
    let section = |name: &str| -> Result<Option<&json::Value>, String> {
        match value.get(name) {
            None | Some(json::Value::Null) => Ok(None),
            Some(v @ json::Value::Object(_)) => Ok(Some(v)),
            Some(_) => Err(format!("{name} must be an object")),
        }
    };
    if let Some(c) = section("cdcl")? {
        read_int(c, "cdcl", "lubyBaseConflicts", &mut f.luby_base_conflicts)?;
        read_int(
            c,
            "cdcl",
            "polynomialBaseConflicts",
            &mut f.polynomial_base_conflicts,
        )?;
        read_int(
            c,
            "cdcl",
            "geometricBaseConflicts",
            &mut f.geometric_base_conflicts,
        )?;
        read_float(
            c,
            "cdcl",
            "geometricGrowthFactor",
            &mut f.geometric_growth_factor,
        )?;
        read_float(c, "cdcl", "lrbAlpha", &mut f.lrb_alpha)?;
        read_float(
            c,
            "cdcl",
            "clauseActivityDecay",
            &mut f.clause_activity_decay,
        )?;
        read_float(c, "cdcl", "varActivityDecay", &mut f.var_activity_decay)?;
        read_int(
            c,
            "cdcl",
            "glueClauseLBDThreshold",
            &mut f.glue_clause_lbd_threshold,
        )?;
        read_int(c, "cdcl", "glucoseWindowSize", &mut f.glucose_window_size)?;
        read_float(c, "cdcl", "glucoseK", &mut f.glucose_k)?;
        read_int(
            c,
            "cdcl",
            "minimizeWorkBudgetFactor",
            &mut f.minimize_work_budget_factor,
        )?;
        read_int(
            c,
            "cdcl",
            "rephaseIntervalRestarts",
            &mut f.rephase_interval_restarts,
        )?;
        read_int(c, "cdcl", "rephaseMaxFlips", &mut f.rephase_max_flips)?;
    }
    if let Some(pp) = section("preprocess")? {
        read_int(
            pp,
            "preprocess",
            "subsumptionWorkBudgetFactor",
            &mut f.subsumption_work_budget_factor,
        )?;
        read_int(
            pp,
            "preprocess",
            "bveWorkBudgetFactor",
            &mut f.bve_work_budget_factor,
        )?;
    }
    Ok(())
}

/// Formats an `f64` the way `encoding/json` (and this module's own
/// writer) render a float: as an integer with no fractional part
/// (`1.0` -> `"1"`) is what Go's `json.Marshal` does NOT do for a
/// float64 field -- Go always writes at least one fractional digit's
/// worth of precision via its shortest-round-trip algorithm, e.g.
/// `1.5` stays `"1.5"` and `0.4` stays `"0.4"`. Rust's own shortest
/// round-tripping `f64::to_string` (via `ryu`-equivalent formatting in
/// std since 1.x) already produces exactly that same shortest
/// representation, so no special-casing is needed here beyond ensuring
/// a bare integer value still gets a trailing ".0" the way Go would
/// (Go's float64 JSON encoding always includes a decimal point).
fn format_f64(v: f64) -> String {
    let s = v.to_string();
    if s.contains('.') || s.contains('e') || s.contains('E') {
        s
    } else {
        format!("{s}.0")
    }
}

/// Serializes `p` as indented JSON (matching `go_src`'s
/// `json.MarshalIndent(p, "", "  ")`), with a trailing newline.
fn render(p: &Params) -> String {
    format!(
        "{{\n  \"cdcl\": {{\n    \"lubyBaseConflicts\": {},\n    \"polynomialBaseConflicts\": {},\n    \"geometricBaseConflicts\": {},\n    \"geometricGrowthFactor\": {},\n    \"lrbAlpha\": {},\n    \"clauseActivityDecay\": {},\n    \"varActivityDecay\": {},\n    \"glueClauseLBDThreshold\": {},\n    \"glucoseWindowSize\": {},\n    \"glucoseK\": {},\n    \"minimizeWorkBudgetFactor\": {},\n    \"rephaseIntervalRestarts\": {},\n    \"rephaseMaxFlips\": {}\n  }},\n  \"preprocess\": {{\n    \"subsumptionWorkBudgetFactor\": {},\n    \"bveWorkBudgetFactor\": {}\n  }}\n}}\n",
        p.cdcl.luby_base_conflicts,
        p.cdcl.polynomial_base_conflicts,
        p.cdcl.geometric_base_conflicts,
        format_f64(p.cdcl.geometric_growth_factor),
        format_f64(p.cdcl.lrb_alpha),
        format_f64(p.cdcl.clause_activity_decay),
        format_f64(p.cdcl.var_activity_decay),
        p.cdcl.glue_clause_lbd_threshold,
        p.cdcl.glucose_window_size,
        format_f64(p.cdcl.glucose_k),
        p.cdcl.minimize_work_budget_factor,
        p.cdcl.rephase_interval_restarts,
        p.cdcl.rephase_max_flips,
        p.preprocess.subsumption_work_budget_factor,
        p.preprocess.bve_work_budget_factor,
    )
}

/// Writes `p` to `path` as indented JSON with a trailing newline (see
/// [`render`]).
pub fn save(path: &str, p: &Params) -> Result<(), String> {
    fs::write(path, render(p)).map_err(|e| format!("write {path}: {e}"))
}

/// Resolves which parameters a run should use, and where they came
/// from (for an optional `--verbose` announcement):
///
/// - If `explicit_path` is `Some`, it is [`load`]-ed and any error
///   (missing file, invalid JSON) is returned as-is -- an explicitly
///   named config file that can't be read is always a hard error,
///   never a silent fallback to defaults.
/// - Otherwise, if [`DEFAULT_CONFIG_FILE_NAME`] exists in the current
///   directory, it is loaded the same way.
/// - Otherwise, [`default`] is returned, with source
///   `"built-in defaults"`.
pub fn resolve(explicit_path: Option<&str>) -> Result<(Params, String), String> {
    if let Some(path) = explicit_path {
        let params = load(path)?;
        return Ok((params, path.to_string()));
    }
    if fs::metadata(DEFAULT_CONFIG_FILE_NAME).is_ok() {
        let params = load(DEFAULT_CONFIG_FILE_NAME)?;
        return Ok((params, DEFAULT_CONFIG_FILE_NAME.to_string()));
    }
    Ok((default(), "built-in defaults".to_string()))
}

/// A minimal JSON reader, sufficient for this module's own schema:
/// objects, numbers, strings, `true`/`false`/`null`, and arrays,
/// parsed into a small [`Value`] tree. Not a general-purpose JSON
/// library (no streaming, no error-position reporting beyond a plain
/// message) -- see the module doc comment for why a hand-written
/// parser is preferred over adding a dependency here.
mod json {
    use std::collections::BTreeMap;
    use std::fmt;

    #[derive(Debug, Clone, PartialEq)]
    pub enum Value {
        Null,
        Bool(bool),
        Number(f64),
        String(String),
        Array(Vec<Value>),
        Object(BTreeMap<String, Value>),
    }

    impl Value {
        pub fn get(&self, key: &str) -> Option<&Value> {
            match self {
                Value::Object(map) => map.get(key),
                _ => None,
            }
        }
    }

    #[derive(Debug)]
    pub struct ParseError(String);

    impl fmt::Display for ParseError {
        fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
            write!(f, "{}", self.0)
        }
    }

    impl std::error::Error for ParseError {}

    struct Parser<'a> {
        bytes: &'a [u8],
        pos: usize,
    }

    pub fn parse(input: &str) -> Result<Value, ParseError> {
        let mut p = Parser {
            bytes: input.as_bytes(),
            pos: 0,
        };
        p.skip_ws();
        let value = p.parse_value()?;
        p.skip_ws();
        if p.pos != p.bytes.len() {
            return Err(ParseError(format!(
                "unexpected trailing content at byte {}",
                p.pos
            )));
        }
        Ok(value)
    }

    impl<'a> Parser<'a> {
        fn skip_ws(&mut self) {
            while self.pos < self.bytes.len() && self.bytes[self.pos].is_ascii_whitespace() {
                self.pos += 1;
            }
        }

        fn peek(&self) -> Option<u8> {
            self.bytes.get(self.pos).copied()
        }

        fn expect(&mut self, b: u8) -> Result<(), ParseError> {
            if self.peek() == Some(b) {
                self.pos += 1;
                Ok(())
            } else {
                Err(ParseError(format!(
                    "expected '{}' at byte {}",
                    b as char, self.pos
                )))
            }
        }

        fn parse_value(&mut self) -> Result<Value, ParseError> {
            self.skip_ws();
            match self.peek() {
                Some(b'{') => self.parse_object(),
                Some(b'[') => self.parse_array(),
                Some(b'"') => self.parse_string().map(Value::String),
                Some(b't') => self.parse_literal("true", Value::Bool(true)),
                Some(b'f') => self.parse_literal("false", Value::Bool(false)),
                Some(b'n') => self.parse_literal("null", Value::Null),
                Some(c) if c == b'-' || c.is_ascii_digit() => self.parse_number(),
                Some(c) => Err(ParseError(format!(
                    "unexpected character '{}' at byte {}",
                    c as char, self.pos
                ))),
                None => Err(ParseError("unexpected end of input".to_string())),
            }
        }

        fn parse_literal(&mut self, text: &str, value: Value) -> Result<Value, ParseError> {
            if self.bytes[self.pos..].starts_with(text.as_bytes()) {
                self.pos += text.len();
                Ok(value)
            } else {
                Err(ParseError(format!(
                    "expected \"{text}\" at byte {}",
                    self.pos
                )))
            }
        }

        fn parse_object(&mut self) -> Result<Value, ParseError> {
            self.expect(b'{')?;
            let mut map = BTreeMap::new();
            self.skip_ws();
            if self.peek() == Some(b'}') {
                self.pos += 1;
                return Ok(Value::Object(map));
            }
            loop {
                self.skip_ws();
                let key = self.parse_string()?;
                self.skip_ws();
                self.expect(b':')?;
                self.skip_ws();
                let value = self.parse_value()?;
                map.insert(key, value);
                self.skip_ws();
                match self.peek() {
                    Some(b',') => {
                        self.pos += 1;
                    }
                    Some(b'}') => {
                        self.pos += 1;
                        break;
                    }
                    _ => {
                        return Err(ParseError(format!(
                            "expected ',' or '}}' at byte {}",
                            self.pos
                        )));
                    }
                }
            }
            Ok(Value::Object(map))
        }

        fn parse_array(&mut self) -> Result<Value, ParseError> {
            self.expect(b'[')?;
            let mut items = Vec::new();
            self.skip_ws();
            if self.peek() == Some(b']') {
                self.pos += 1;
                return Ok(Value::Array(items));
            }
            loop {
                self.skip_ws();
                items.push(self.parse_value()?);
                self.skip_ws();
                match self.peek() {
                    Some(b',') => {
                        self.pos += 1;
                    }
                    Some(b']') => {
                        self.pos += 1;
                        break;
                    }
                    _ => {
                        return Err(ParseError(format!(
                            "expected ',' or ']' at byte {}",
                            self.pos
                        )));
                    }
                }
            }
            Ok(Value::Array(items))
        }

        fn parse_string(&mut self) -> Result<String, ParseError> {
            self.expect(b'"')?;
            let mut out = String::new();
            loop {
                match self.peek() {
                    None => return Err(ParseError("unterminated string".to_string())),
                    Some(b'"') => {
                        self.pos += 1;
                        break;
                    }
                    Some(b'\\') => {
                        self.pos += 1;
                        match self.peek() {
                            Some(b'"') => {
                                out.push('"');
                                self.pos += 1;
                            }
                            Some(b'\\') => {
                                out.push('\\');
                                self.pos += 1;
                            }
                            Some(b'/') => {
                                out.push('/');
                                self.pos += 1;
                            }
                            Some(b'n') => {
                                out.push('\n');
                                self.pos += 1;
                            }
                            Some(b't') => {
                                out.push('\t');
                                self.pos += 1;
                            }
                            Some(b'r') => {
                                out.push('\r');
                                self.pos += 1;
                            }
                            Some(b'b') => {
                                out.push('\u{8}');
                                self.pos += 1;
                            }
                            Some(b'f') => {
                                out.push('\u{c}');
                                self.pos += 1;
                            }
                            Some(b'u') => {
                                self.pos += 1;
                                let hex =
                                    self.bytes.get(self.pos..self.pos + 4).ok_or_else(|| {
                                        ParseError("truncated \\u escape".to_string())
                                    })?;
                                let hex_str = std::str::from_utf8(hex)
                                    .map_err(|_| ParseError("invalid \\u escape".to_string()))?;
                                let code = u32::from_str_radix(hex_str, 16)
                                    .map_err(|_| ParseError("invalid \\u escape".to_string()))?;
                                out.push(char::from_u32(code).ok_or_else(|| {
                                    ParseError("invalid \\u codepoint".to_string())
                                })?);
                                self.pos += 4;
                            }
                            _ => return Err(ParseError("invalid escape sequence".to_string())),
                        }
                    }
                    Some(_) => {
                        // ASCII-only fast path is fine here (field
                        // names/values in this schema are all ASCII);
                        // fall back to decoding one UTF-8 char for
                        // anything else (e.g. inside an unrecognized
                        // string value we still need to skip past).
                        let rest = std::str::from_utf8(&self.bytes[self.pos..])
                            .map_err(|_| ParseError("invalid UTF-8".to_string()))?;
                        let ch = rest
                            .chars()
                            .next()
                            .expect("non-empty after None/'\"'/'\\\\' checks");
                        out.push(ch);
                        self.pos += ch.len_utf8();
                    }
                }
            }
            Ok(out)
        }

        fn parse_number(&mut self) -> Result<Value, ParseError> {
            let start = self.pos;
            if self.peek() == Some(b'-') {
                self.pos += 1;
            }
            while matches!(self.peek(), Some(c) if c.is_ascii_digit()) {
                self.pos += 1;
            }
            if self.peek() == Some(b'.') {
                self.pos += 1;
                while matches!(self.peek(), Some(c) if c.is_ascii_digit()) {
                    self.pos += 1;
                }
            }
            if matches!(self.peek(), Some(b'e') | Some(b'E')) {
                self.pos += 1;
                if matches!(self.peek(), Some(b'+') | Some(b'-')) {
                    self.pos += 1;
                }
                while matches!(self.peek(), Some(c) if c.is_ascii_digit()) {
                    self.pos += 1;
                }
            }
            let text = std::str::from_utf8(&self.bytes[start..self.pos]).expect("ASCII number");
            text.parse::<f64>()
                .map(Value::Number)
                .map_err(|_| ParseError(format!("invalid number {text:?}")))
        }
    }

    #[cfg(test)]
    mod tests {
        use super::*;

        #[test]
        fn parses_flat_object() {
            let v = parse(r#"{"a": 1, "b": 2.5, "c": "x"}"#).unwrap();
            assert_eq!(v.get("a"), Some(&Value::Number(1.0)));
            assert_eq!(v.get("b"), Some(&Value::Number(2.5)));
        }

        #[test]
        fn parses_nested_object() {
            let v = parse(r#"{"outer": {"inner": 42}}"#).unwrap();
            assert_eq!(
                v.get("outer").unwrap().get("inner"),
                Some(&Value::Number(42.0))
            );
        }

        #[test]
        fn rejects_invalid_json() {
            assert!(parse("{not valid json").is_err());
            assert!(parse("").is_err());
        }
    }
}

impl fmt::Display for Params {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", render(self))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicU64, Ordering};

    static COUNTER: AtomicU64 = AtomicU64::new(0);

    /// A scratch file path in the system temp directory, unique per
    /// call, so tests running in parallel (the default for `cargo
    /// test`) never collide on the same path.
    fn scratch_path(name: &str) -> String {
        let n = COUNTER.fetch_add(1, Ordering::SeqCst);
        std::env::temp_dir()
            .join(format!("vibe_sat_params_test_{n}_{name}"))
            .to_string_lossy()
            .into_owned()
    }

    #[test]
    fn default_matches_pre_stage39_constants() {
        let d = default();
        // STAGE49.md recalibrated luby_base_conflicts away from its
        // pre-STAGE39.md value (100) -- see reports/REPORT49.md.
        assert_eq!(d.cdcl.luby_base_conflicts, 4000);
        assert_eq!(d.cdcl.polynomial_base_conflicts, 18000);
        assert_eq!(d.cdcl.geometric_base_conflicts, 100);
        assert_eq!(d.cdcl.geometric_growth_factor, 1.5);
        assert_eq!(d.cdcl.lrb_alpha, 0.1);
        assert_eq!(d.cdcl.clause_activity_decay, 0.999);
        assert_eq!(d.cdcl.var_activity_decay, 0.95);
        assert_eq!(d.cdcl.glue_clause_lbd_threshold, 2);
        assert_eq!(d.cdcl.glucose_window_size, 50);
        assert_eq!(d.cdcl.glucose_k, 0.8);
        assert_eq!(d.cdcl.minimize_work_budget_factor, 20);
        assert_eq!(d.cdcl.rephase_interval_restarts, 50);
        assert_eq!(d.cdcl.rephase_max_flips, 1000);
        assert_eq!(d.preprocess.subsumption_work_budget_factor, 64);
        assert_eq!(d.preprocess.bve_work_budget_factor, 2000);
    }

    #[test]
    fn load_keeps_defaults_for_omitted_fields() {
        let path = scratch_path("partial.json");
        fs::write(&path, r#"{"cdcl": {"glucoseK": 0.9}}"#).unwrap();
        let p = load(&path).unwrap();
        assert_eq!(p.cdcl.glucose_k, 0.9);
        assert_eq!(p.cdcl.lrb_alpha, default().cdcl.lrb_alpha);
        assert_eq!(p.preprocess, default().preprocess);
        fs::remove_file(&path).ok();
    }

    /// Out-of-range values, one per row as (file body, "section.key"
    /// the error must name): just outside every field's accepted range
    /// plus a few wild ones. Mirrors `rejectedParamCases` in Go's
    /// params_test.go.
    const REJECTED: &[(&str, &str)] = &[
        (
            r#"{"cdcl":{"lubyBaseConflicts":9223372036854775807}}"#,
            "cdcl.lubyBaseConflicts",
        ),
        (
            r#"{"cdcl":{"polynomialBaseConflicts":9223372036854775807}}"#,
            "cdcl.polynomialBaseConflicts",
        ),
        (
            r#"{"cdcl":{"minimizeWorkBudgetFactor":9223372036854775807}}"#,
            "cdcl.minimizeWorkBudgetFactor",
        ),
        (
            r#"{"preprocess":{"subsumptionWorkBudgetFactor":9223372036854775807}}"#,
            "preprocess.subsumptionWorkBudgetFactor",
        ),
        (
            r#"{"preprocess":{"bveWorkBudgetFactor":9223372036854775807}}"#,
            "preprocess.bveWorkBudgetFactor",
        ),
        (
            r#"{"cdcl":{"lubyBaseConflicts":1000000001}}"#,
            "cdcl.lubyBaseConflicts",
        ),
        (
            r#"{"cdcl":{"polynomialBaseConflicts":1000000001}}"#,
            "cdcl.polynomialBaseConflicts",
        ),
        (
            r#"{"cdcl":{"minimizeWorkBudgetFactor":1000000001}}"#,
            "cdcl.minimizeWorkBudgetFactor",
        ),
        (
            r#"{"preprocess":{"subsumptionWorkBudgetFactor":1000000001}}"#,
            "preprocess.subsumptionWorkBudgetFactor",
        ),
        (
            r#"{"preprocess":{"bveWorkBudgetFactor":1000000001}}"#,
            "preprocess.bveWorkBudgetFactor",
        ),
        (
            r#"{"cdcl":{"lubyBaseConflicts":0}}"#,
            "cdcl.lubyBaseConflicts",
        ),
        (
            r#"{"cdcl":{"lubyBaseConflicts":-5}}"#,
            "cdcl.lubyBaseConflicts",
        ),
        (
            r#"{"cdcl":{"polynomialBaseConflicts":0}}"#,
            "cdcl.polynomialBaseConflicts",
        ),
        (
            r#"{"cdcl":{"geometricBaseConflicts":-1}}"#,
            "cdcl.geometricBaseConflicts",
        ),
        (
            r#"{"cdcl":{"geometricGrowthFactor":0.99}}"#,
            "cdcl.geometricGrowthFactor",
        ),
        (
            r#"{"cdcl":{"geometricGrowthFactor":0}}"#,
            "cdcl.geometricGrowthFactor",
        ),
        (
            r#"{"cdcl":{"geometricGrowthFactor":-2}}"#,
            "cdcl.geometricGrowthFactor",
        ),
        (r#"{"cdcl":{"lrbAlpha":0}}"#, "cdcl.lrbAlpha"),
        (r#"{"cdcl":{"lrbAlpha":1.01}}"#, "cdcl.lrbAlpha"),
        (
            r#"{"cdcl":{"clauseActivityDecay":0}}"#,
            "cdcl.clauseActivityDecay",
        ),
        (
            r#"{"cdcl":{"clauseActivityDecay":-0.5}}"#,
            "cdcl.clauseActivityDecay",
        ),
        (
            r#"{"cdcl":{"clauseActivityDecay":2}}"#,
            "cdcl.clauseActivityDecay",
        ),
        (
            r#"{"cdcl":{"varActivityDecay":0}}"#,
            "cdcl.varActivityDecay",
        ),
        (
            r#"{"cdcl":{"varActivityDecay":2.0}}"#,
            "cdcl.varActivityDecay",
        ),
        (
            r#"{"cdcl":{"glueClauseLBDThreshold":-1}}"#,
            "cdcl.glueClauseLBDThreshold",
        ),
        (
            r#"{"cdcl":{"glucoseWindowSize":0}}"#,
            "cdcl.glucoseWindowSize",
        ),
        (
            r#"{"cdcl":{"glucoseWindowSize":-5}}"#,
            "cdcl.glucoseWindowSize",
        ),
        (
            r#"{"cdcl":{"glucoseWindowSize":10000001}}"#,
            "cdcl.glucoseWindowSize",
        ),
        (
            r#"{"cdcl":{"glucoseWindowSize":100000000000}}"#,
            "cdcl.glucoseWindowSize",
        ),
        (r#"{"cdcl":{"glucoseK":0}}"#, "cdcl.glucoseK"),
        (r#"{"cdcl":{"glucoseK":-0.6}}"#, "cdcl.glucoseK"),
        (
            r#"{"cdcl":{"minimizeWorkBudgetFactor":-1}}"#,
            "cdcl.minimizeWorkBudgetFactor",
        ),
        (
            r#"{"cdcl":{"rephaseIntervalRestarts":0}}"#,
            "cdcl.rephaseIntervalRestarts",
        ),
        (
            r#"{"cdcl":{"rephaseIntervalRestarts":-3}}"#,
            "cdcl.rephaseIntervalRestarts",
        ),
        (r#"{"cdcl":{"rephaseMaxFlips":0}}"#, "cdcl.rephaseMaxFlips"),
        (
            r#"{"preprocess":{"subsumptionWorkBudgetFactor":-1}}"#,
            "preprocess.subsumptionWorkBudgetFactor",
        ),
        (
            r#"{"preprocess":{"bveWorkBudgetFactor":-1}}"#,
            "preprocess.bveWorkBudgetFactor",
        ),
    ];

    /// The inclusive ends of every range, which must still load.
    const ACCEPTED: &[&str] = &[
        r#"{"cdcl":{"lubyBaseConflicts":1,"polynomialBaseConflicts":1,"geometricBaseConflicts":1}}"#,
        r#"{"cdcl":{"geometricGrowthFactor":1}}"#,
        r#"{"cdcl":{"geometricGrowthFactor":1000}}"#,
        r#"{"cdcl":{"lrbAlpha":1,"clauseActivityDecay":1,"varActivityDecay":1}}"#,
        r#"{"cdcl":{"lrbAlpha":0.0001,"clauseActivityDecay":0.0001,"varActivityDecay":0.0001}}"#,
        r#"{"cdcl":{"glueClauseLBDThreshold":0}}"#,
        r#"{"cdcl":{"glucoseWindowSize":1}}"#,
        r#"{"cdcl":{"glucoseWindowSize":10000000}}"#,
        r#"{"cdcl":{"lubyBaseConflicts":1000000000,"polynomialBaseConflicts":1000000000,"minimizeWorkBudgetFactor":1000000000}}"#,
        r#"{"preprocess":{"subsumptionWorkBudgetFactor":1000000000,"bveWorkBudgetFactor":1000000000}}"#,
        r#"{"cdcl":{"glucoseK":0.0001}}"#,
        r#"{"cdcl":{"glucoseK":1000}}"#,
        r#"{"cdcl":{"minimizeWorkBudgetFactor":0}}"#,
        r#"{"cdcl":{"rephaseIntervalRestarts":1,"rephaseMaxFlips":1}}"#,
        r#"{"preprocess":{"subsumptionWorkBudgetFactor":0,"bveWorkBudgetFactor":0}}"#,
        "{}",
    ];

    /// Load refuses every out-of-range value with an error naming the
    /// offending "section.key" and saying what is accepted, instead of
    /// letting it through to wreck (or crash) the solver (STAGE56.md).
    #[test]
    fn load_rejects_out_of_range_parameters() {
        for (body, key) in REJECTED {
            let path = scratch_path("bad.json");
            fs::write(&path, body).unwrap();
            let err = load(&path).expect_err(body);
            assert!(err.contains(&format!("{key} must be ")), "{body}: {err}");
            fs::remove_file(&path).ok();
        }
    }

    /// The inclusive ends of every range still load, so validation is
    /// not stricter than the documented ranges.
    #[test]
    fn load_accepts_boundary_parameters() {
        for body in ACCEPTED {
            let path = scratch_path("ok.json");
            fs::write(&path, body).unwrap();
            load(&path).unwrap_or_else(|e| panic!("{body}: {e}"));
            fs::remove_file(&path).ok();
        }
    }

    /// The built-in defaults satisfy every range, so a run with no
    /// config file can never be rejected.
    #[test]
    fn validate_accepts_defaults() {
        assert!(validate(&Fields::from_params(&default())).is_ok());
    }

    /// The float checks cannot be bypassed by NaN or +/-Inf.
    #[test]
    fn validate_rejects_nan_and_infinity() {
        let setters: [fn(&mut Fields, f64); 5] = [
            |f, v| f.geometric_growth_factor = v,
            |f, v| f.lrb_alpha = v,
            |f, v| f.clause_activity_decay = v,
            |f, v| f.var_activity_decay = v,
            |f, v| f.glucose_k = v,
        ];
        for bad in [f64::NAN, f64::INFINITY, f64::NEG_INFINITY] {
            for set in setters {
                let mut f = Fields::from_params(&default());
                set(&mut f, bad);
                assert!(validate(&f).is_err(), "accepted {bad}");
            }
        }
    }

    /// Only the first violation, in field order, is reported.
    #[test]
    fn load_reports_only_the_first_violation_in_field_order() {
        let path = scratch_path("two.json");
        fs::write(
            &path,
            r#"{"cdcl":{"varActivityDecay":2,"lubyBaseConflicts":0}}"#,
        )
        .unwrap();
        let err = load(&path).unwrap_err();
        assert!(err.contains("cdcl.lubyBaseConflicts"), "{err}");
        assert!(!err.contains("varActivityDecay"), "{err}");
        fs::remove_file(&path).ok();
    }

    /// A value of the wrong JSON type (string for a number, fraction for
    /// an integer field, non-object section) is an error rather than
    /// being ignored or truncated, and null leaves the default in place.
    /// Mirrors Go's behavior under encoding/json.
    #[test]
    fn load_rejects_wrong_json_types_and_accepts_null() {
        for body in [
            r#"{"cdcl":{"lubyBaseConflicts":"5"}}"#,
            r#"{"cdcl":{"lubyBaseConflicts":1.5}}"#,
            r#"{"cdcl":{"glucoseK":"0.6"}}"#,
            r#"{"cdcl":{"glucoseK":true}}"#,
            r#"{"cdcl":5}"#,
            r#"{"preprocess":[1]}"#,
        ] {
            let path = scratch_path("type.json");
            fs::write(&path, body).unwrap();
            assert!(load(&path).is_err(), "{body} was accepted");
            fs::remove_file(&path).ok();
        }
        let path = scratch_path("null.json");
        fs::write(
            &path,
            r#"{"cdcl":{"lubyBaseConflicts":null,"glucoseK":null},"preprocess":null}"#,
        )
        .unwrap();
        assert_eq!(load(&path).unwrap(), default());
        fs::remove_file(&path).ok();
    }

    #[test]
    fn load_missing_file_is_an_error() {
        let result = load(&scratch_path("does-not-exist.json"));
        assert!(result.is_err());
    }

    #[test]
    fn load_invalid_json_is_an_error() {
        let path = scratch_path("invalid.json");
        fs::write(&path, "{not valid").unwrap();
        let result = load(&path);
        assert!(result.is_err());
        fs::remove_file(&path).ok();
    }

    #[test]
    fn save_round_trips() {
        let path = scratch_path("roundtrip.json");
        let mut p = default();
        p.cdcl.glucose_k = 0.75;
        p.preprocess.bve_work_budget_factor = 12345;
        save(&path, &p).unwrap();
        let loaded = load(&path).unwrap();
        assert_eq!(loaded, p);
        fs::remove_file(&path).ok();
    }

    #[test]
    fn save_writes_trailing_newline() {
        let path = scratch_path("newline.json");
        save(&path, &default()).unwrap();
        let contents = fs::read_to_string(&path).unwrap();
        assert!(contents.ends_with('\n'));
        fs::remove_file(&path).ok();
    }

    #[test]
    fn resolve_prefers_explicit_path() {
        let path = scratch_path("explicit.json");
        let mut p = default();
        p.cdcl.lrb_alpha = 0.1;
        save(&path, &p).unwrap();
        let (resolved, source) = resolve(Some(&path)).unwrap();
        assert_eq!(resolved, p);
        assert_eq!(source, path);
        fs::remove_file(&path).ok();
    }

    #[test]
    fn resolve_explicit_path_missing_is_an_error() {
        let result = resolve(Some("/nonexistent/path/to/.vibe_sat.json"));
        assert!(result.is_err());
    }

    #[test]
    fn resolve_falls_back_to_built_in_defaults() {
        // No explicit path, and this test doesn't create
        // DEFAULT_CONFIG_FILE_NAME in the current directory, so this
        // only asserts the behavior when that file genuinely isn't
        // present; if some other concurrently running test ever
        // created one in the shared cwd, this would be flaky -- no
        // test in this module does, so it's safe.
        if fs::metadata(DEFAULT_CONFIG_FILE_NAME).is_ok() {
            return;
        }
        let (resolved, source) = resolve(None).unwrap();
        assert_eq!(resolved, default());
        assert_eq!(source, "built-in defaults");
    }
}
