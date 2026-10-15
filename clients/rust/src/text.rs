//! Byte and text helpers shared by signing, encryption and the URL builders.

use std::fmt::Write as _;

use base64::Engine as _;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;

use crate::error::{Error, Result};
use crate::generated::constants::{PLAIN_SAFE, QUERY_SAFE};

/// Key material: a hex string (as in `XI_SIGNING_KEYS`, either case, surrounding
/// whitespace ignored) or raw bytes.
#[derive(Clone, PartialEq, Eq)]
pub enum KeyMaterial {
    /// Hex-encoded bytes.
    Hex(String),
    /// Raw bytes.
    Bytes(Vec<u8>),
}

impl std::fmt::Debug for KeyMaterial {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("KeyMaterial(…)")
    }
}

impl From<&str> for KeyMaterial {
    fn from(v: &str) -> Self {
        Self::Hex(v.to_owned())
    }
}

impl From<String> for KeyMaterial {
    fn from(v: String) -> Self {
        Self::Hex(v)
    }
}

impl From<&String> for KeyMaterial {
    fn from(v: &String) -> Self {
        Self::Hex(v.clone())
    }
}

impl From<Vec<u8>> for KeyMaterial {
    fn from(v: Vec<u8>) -> Self {
        Self::Bytes(v)
    }
}

impl From<&[u8]> for KeyMaterial {
    fn from(v: &[u8]) -> Self {
        Self::Bytes(v.to_vec())
    }
}

impl<const N: usize> From<[u8; N]> for KeyMaterial {
    fn from(v: [u8; N]) -> Self {
        Self::Bytes(v.to_vec())
    }
}

impl<const N: usize> From<&[u8; N]> for KeyMaterial {
    fn from(v: &[u8; N]) -> Self {
        Self::Bytes(v.to_vec())
    }
}

impl KeyMaterial {
    /// The bytes: hex decoded, or raw bytes copied.
    pub(crate) fn bytes(&self, what: &str) -> Result<Vec<u8>> {
        match self {
            Self::Bytes(b) => Ok(b.clone()),
            Self::Hex(s) => {
                decode_hex(s.trim()).ok_or_else(|| Error::InvalidKey(format!("{what} is not a hex string")))
            }
        }
    }
}

fn decode_hex(s: &str) -> Option<Vec<u8>> {
    if !s.len().is_multiple_of(2) {
        return None;
    }
    let digit = |c: u8| match c {
        b'0'..=b'9' => Some(c - b'0'),
        b'a'..=b'f' => Some(c - b'a' + 10),
        b'A'..=b'F' => Some(c - b'A' + 10),
        _ => None,
    };
    s.as_bytes().as_chunks::<2>().0.iter().map(|[hi, lo]| Some(digit(*hi)? << 4 | digit(*lo)?)).collect()
}

/// Unpadded base64url (RFC 4648 §5).
pub(crate) fn b64url(b: &[u8]) -> String {
    URL_SAFE_NO_PAD.encode(b)
}

/// Whether `s` contains an ASCII control character (C0 or DEL).
pub(crate) fn has_control_char(s: &str) -> bool {
    s.bytes().any(|b| b < 0x20 || b == 0x7f)
}

/// Rejects an empty string or one with control characters.
pub(crate) fn check_text(s: &str, err: fn(String) -> Error, what: &str) -> Result<()> {
    if s.is_empty() || has_control_char(s) {
        return Err(err(format!("{what} must be a non-empty string without control characters")));
    }
    Ok(())
}

/// Rejects an extension that is not ASCII letters and digits (`url.extension_pattern`).
pub(crate) fn check_extension(ext: &str) -> Result<&str> {
    if ext.is_empty() || !ext.bytes().all(|b| b.is_ascii_alphanumeric()) {
        return Err(Error::InvalidExtension(format!("invalid extension {ext:?}")));
    }
    Ok(ext)
}

fn escape(s: &str, safe: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for ch in s.chars() {
        if ch.is_ascii_alphanumeric() || safe.contains(ch) {
            out.push(ch);
        } else {
            let mut buf = [0u8; 4];
            for b in ch.encode_utf8(&mut buf).bytes() {
                let _ = write!(out, "%{b:02X}");
            }
        }
    }
    out
}

/// Percent-escapes a source for a `plain/` URL; the server path-unescapes it.
///
/// ASCII letters, digits and `url.plain_safe` (RFC 3986 unreserved characters,
/// the sub-delims, `:` and `/`) are kept; everything else (`%`, `@`, `?`, `#`,
/// spaces, non-ASCII) is percent-encoded as UTF-8 in upper case.
///
/// ```
/// assert_eq!(
///     xtandard_image::escape_plain_source("https://e.com/a b@c%d?e#f;g=h"),
///     "https://e.com/a%20b%40c%25d%3Fe%23f;g=h",
/// );
/// ```
pub fn escape_plain_source(source: &str) -> String {
    escape(source, PLAIN_SAFE)
}

/// Percent-escapes a query key or value, keeping `:`, `/`, `,` and the rest of
/// `url.query_safe` readable.
///
/// ```
/// assert_eq!(xtandard_image::escape_query_value("a&b=c+d e/f:g"), "a%26b%3Dc%2Bd%20e/f:g");
/// ```
pub fn escape_query_value(value: &str) -> String {
    escape(value, QUERY_SAFE)
}

/// Formats a number like ECMAScript `Number::toString` (`300`, `1.5`, `0.1`,
/// `1e+21`, `1e-7`), so URLs and their signatures match across languages.
pub(crate) fn format_float(v: f64, where_: &str) -> Result<String> {
    if !v.is_finite() {
        return Err(Error::InvalidArgument(format!("{where_}: {v} is not a finite number")));
    }
    if v == 0.0 {
        return Ok("0".to_owned());
    }
    let sign = if v < 0.0 { "-" } else { "" };
    // Shortest round-trip digits: "d.ddde-x".
    let sci = format!("{:e}", v.abs());
    let (mantissa, exp) = sci.split_once('e').unwrap_or((sci.as_str(), "0"));
    let digits: String = mantissa.chars().filter(char::is_ascii_digit).collect();
    let exp: i32 = exp.parse().unwrap_or(0);
    // |v| = 0.<digits> × 10^point (ECMAScript's k digits and exponent n).
    let point = exp + 1;
    let count = i32::try_from(digits.len()).unwrap_or(i32::MAX);
    let zeros = |len: i32| "0".repeat(usize::try_from(len).unwrap_or(0));
    let out = if count <= point && point <= 21 {
        format!("{digits}{}", zeros(point - count))
    } else if 0 < point && point <= 21 {
        let (int, frac) = digits.split_at(usize::try_from(point).unwrap_or(0));
        format!("{int}.{frac}")
    } else if -6 < point && point <= 0 {
        format!("0.{}{digits}", zeros(-point))
    } else {
        let e10 = point - 1;
        let suffix = format!("e{}{}", if e10 >= 0 { "+" } else { "-" }, e10.abs());
        if count == 1 { format!("{digits}{suffix}") } else { format!("{}.{}{suffix}", &digits[..1], &digits[1..]) }
    };
    Ok(format!("{sign}{out}"))
}

/// Formats an integer: exactly below 10^21, like a float above.
pub(crate) fn format_int(v: i128, where_: &str) -> Result<String> {
    const LIMIT: i128 = 1_000_000_000_000_000_000_000;
    if v.abs() < LIMIT {
        return Ok(v.to_string());
    }
    #[allow(clippy::cast_precision_loss)] // as JavaScript does above 2^53
    format_float(v as f64, where_)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn numbers_match_ecmascript() {
        let cases: &[(f64, &str)] = &[
            (0.0, "0"),
            (-0.0, "0"),
            (300.0, "300"),
            (1.5, "1.5"),
            (-1.5, "-1.5"),
            (0.1, "0.1"),
            (0.000_001, "0.000001"),
            (1e-7, "1e-7"),
            (1.5e-9, "1.5e-9"),
            (123_456_789.125, "123456789.125"),
            (1e20, "100000000000000000000"),
            (1e21, "1e+21"),
            (1.5e22, "1.5e+22"),
            (0.300_000_000_000_000_04, "0.30000000000000004"),
            (-90.0, "-90"),
        ];
        for &(v, want) in cases {
            assert_eq!(format_float(v, "x").unwrap(), want, "{v}");
        }
        assert_eq!(format_int(10_i128.pow(21), "x").unwrap(), "1e+21");
        assert_eq!(format_int((1 << 53) + 1, "x").unwrap(), "9007199254740993");
        assert_eq!(format_int(-12, "x").unwrap(), "-12");
        assert_eq!(Arg::from(0.1_f32), Arg::Float(0.1));
    }

    use crate::Arg;

    #[test]
    fn non_finite_numbers_are_errors() {
        for v in [f64::INFINITY, f64::NEG_INFINITY, f64::NAN] {
            assert_eq!(format_float(v, "x").unwrap_err().code(), "invalid_argument");
        }
    }

    #[test]
    fn hex() {
        assert_eq!(decode_hex("0aFf"), Some(vec![0x0a, 0xff]));
        assert_eq!(decode_hex("0"), None);
        assert_eq!(decode_hex("zz"), None);
        assert_eq!(KeyMaterial::from(" 00 ").bytes("k").unwrap(), vec![0]);
        assert_eq!(format!("{:?}", KeyMaterial::from("00")), "KeyMaterial(…)");
    }
}
