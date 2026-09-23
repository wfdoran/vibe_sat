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
/// these fields held as a compile-time constant before STAGE39.md.
pub fn default() -> Params {
    Params {
        cdcl: Cdcl {
            luby_base_conflicts: 100,
            polynomial_base_conflicts: 18000,
            geometric_base_conflicts: 100,
            geometric_growth_factor: 1.5,
            lrb_alpha: 0.4,
            clause_activity_decay: 0.999,
            var_activity_decay: 0.95,
            glue_clause_lbd_threshold: 2,
            glucose_window_size: 50,
            glucose_k: 0.6,
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
/// exist") or its contents are not valid JSON.
pub fn load(path: &str) -> Result<Params, String> {
    let contents = fs::read_to_string(path).map_err(|e| format!("open {path}: {e}"))?;
    let value = json::parse(&contents).map_err(|e| format!("parse {path}: {e}"))?;
    let mut params = default();
    apply(&mut params, &value);
    Ok(params)
}

/// Overlays whatever fields `value` (a parsed JSON object, expected to
/// have "cdcl"/"preprocess" sub-objects) specifies onto `params` in
/// place, leaving every field it doesn't mention untouched.
fn apply(params: &mut Params, value: &json::Value) {
    if let Some(cdcl) = value.get("cdcl") {
        if let Some(v) = cdcl
            .get("lubyBaseConflicts")
            .and_then(json::Value::as_usize)
        {
            params.cdcl.luby_base_conflicts = v;
        }
        if let Some(v) = cdcl
            .get("polynomialBaseConflicts")
            .and_then(json::Value::as_usize)
        {
            params.cdcl.polynomial_base_conflicts = v;
        }
        if let Some(v) = cdcl
            .get("geometricBaseConflicts")
            .and_then(json::Value::as_usize)
        {
            params.cdcl.geometric_base_conflicts = v;
        }
        if let Some(v) = cdcl
            .get("geometricGrowthFactor")
            .and_then(json::Value::as_f64)
        {
            params.cdcl.geometric_growth_factor = v;
        }
        if let Some(v) = cdcl.get("lrbAlpha").and_then(json::Value::as_f64) {
            params.cdcl.lrb_alpha = v;
        }
        if let Some(v) = cdcl
            .get("clauseActivityDecay")
            .and_then(json::Value::as_f64)
        {
            params.cdcl.clause_activity_decay = v;
        }
        if let Some(v) = cdcl.get("varActivityDecay").and_then(json::Value::as_f64) {
            params.cdcl.var_activity_decay = v;
        }
        if let Some(v) = cdcl
            .get("glueClauseLBDThreshold")
            .and_then(json::Value::as_usize)
        {
            params.cdcl.glue_clause_lbd_threshold = v;
        }
        if let Some(v) = cdcl
            .get("glucoseWindowSize")
            .and_then(json::Value::as_usize)
        {
            params.cdcl.glucose_window_size = v;
        }
        if let Some(v) = cdcl.get("glucoseK").and_then(json::Value::as_f64) {
            params.cdcl.glucose_k = v;
        }
        if let Some(v) = cdcl
            .get("minimizeWorkBudgetFactor")
            .and_then(json::Value::as_usize)
        {
            params.cdcl.minimize_work_budget_factor = v;
        }
        if let Some(v) = cdcl
            .get("rephaseIntervalRestarts")
            .and_then(json::Value::as_usize)
        {
            params.cdcl.rephase_interval_restarts = v;
        }
        if let Some(v) = cdcl.get("rephaseMaxFlips").and_then(json::Value::as_usize) {
            params.cdcl.rephase_max_flips = v;
        }
    }
    if let Some(preprocess) = value.get("preprocess") {
        if let Some(v) = preprocess
            .get("subsumptionWorkBudgetFactor")
            .and_then(json::Value::as_usize)
        {
            params.preprocess.subsumption_work_budget_factor = v;
        }
        if let Some(v) = preprocess
            .get("bveWorkBudgetFactor")
            .and_then(json::Value::as_usize)
        {
            params.preprocess.bve_work_budget_factor = v;
        }
    }
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

        pub fn as_f64(&self) -> Option<f64> {
            match self {
                Value::Number(n) => Some(*n),
                _ => None,
            }
        }

        pub fn as_i64(&self) -> Option<i64> {
            self.as_f64().map(|n| n as i64)
        }

        pub fn as_usize(&self) -> Option<usize> {
            self.as_i64().map(|n| n.max(0) as usize)
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
            assert_eq!(v.get("a").unwrap().as_i64(), Some(1));
            assert_eq!(v.get("b").unwrap().as_f64(), Some(2.5));
        }

        #[test]
        fn parses_nested_object() {
            let v = parse(r#"{"outer": {"inner": 42}}"#).unwrap();
            assert_eq!(
                v.get("outer").unwrap().get("inner").unwrap().as_i64(),
                Some(42)
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
        assert_eq!(d.cdcl.luby_base_conflicts, 100);
        assert_eq!(d.cdcl.polynomial_base_conflicts, 18000);
        assert_eq!(d.cdcl.geometric_base_conflicts, 100);
        assert_eq!(d.cdcl.geometric_growth_factor, 1.5);
        assert_eq!(d.cdcl.lrb_alpha, 0.4);
        assert_eq!(d.cdcl.clause_activity_decay, 0.999);
        assert_eq!(d.cdcl.var_activity_decay, 0.95);
        assert_eq!(d.cdcl.glue_clause_lbd_threshold, 2);
        assert_eq!(d.cdcl.glucose_window_size, 50);
        assert_eq!(d.cdcl.glucose_k, 0.6);
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
