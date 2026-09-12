//! Writes a satisfying assignment in the DIMACS solution format used
//! by SAT solvers and competitions: a status line followed by a value
//! line listing one signed literal per variable.

use std::io::{self, Write};

use crate::assignment::{Assignment, Value};

/// Writes `assignment`, a complete satisfying assignment over
/// variables 1 through `num_vars`, to `writer` in DIMACS solution
/// format:
///
/// ```text
/// s SATISFIABLE
/// v 1 -2 3 ... 0
/// ```
///
/// The value line contains, for every variable from 1 to `num_vars`,
/// the variable number if it is `True` or its negation if it is
/// `False`, terminated by a trailing 0.
pub fn write<W: Write>(writer: &mut W, assignment: &Assignment, num_vars: usize) -> io::Result<()> {
    writeln!(writer, "s SATISFIABLE")?;

    let mut line = String::from("v");
    for (v, value) in assignment.iter().enumerate().skip(1).take(num_vars) {
        if *value == Value::True {
            line.push_str(&format!(" {v}"));
        } else {
            line.push_str(&format!(" -{v}"));
        }
    }
    line.push_str(" 0");

    writeln!(writer, "{line}")
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::assignment;

    #[test]
    fn test_write_produces_expected_format() {
        let mut a = assignment::new(3);
        a[1] = Value::True;
        a[2] = Value::False;
        a[3] = Value::True;

        let mut buffer = Vec::new();
        write(&mut buffer, &a, 3).expect("write should not fail");
        let output = String::from_utf8(buffer).expect("valid utf-8");

        let lines: Vec<&str> = output.trim_end().split('\n').collect();
        assert_eq!(lines.len(), 2);
        assert_eq!(lines[0], "s SATISFIABLE");
        assert_eq!(lines[1], "v 1 -2 3 0");
    }
}
