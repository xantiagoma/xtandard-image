//! HTTP byte ranges of media requests.

use crate::error::{Error, Result};

/// A byte range of a `Range` request header.
///
/// ```
/// use xtandard_image::{ByteRange, range_header};
///
/// assert_eq!(range_header(&ByteRange::from_start(0))?, "bytes=0-");
/// assert_eq!(range_header(&ByteRange::new(5, 9))?, "bytes=5-9");
/// assert_eq!(range_header(&ByteRange::suffix(500))?, "bytes=-500");
/// assert_eq!(range_header(&"bytes=1-2".into())?, "bytes=1-2");
/// assert_eq!(range_header(&ByteRange::new(5, 4)).unwrap_err().code(), "invalid_range");
/// # Ok::<(), xtandard_image::Error>(())
/// ```
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ByteRange {
    /// Bytes `start` to `end` (inclusive), or to the end when `end` is `None`.
    Range {
        /// The first byte.
        start: u64,
        /// The last byte, inclusive.
        end: Option<u64>,
    },
    /// The last bytes.
    Suffix(u64),
    /// A header value written verbatim.
    Raw(String),
}

impl ByteRange {
    /// Bytes `start` to `end`, inclusive.
    pub const fn new(start: u64, end: u64) -> Self {
        Self::Range { start, end: Some(end) }
    }

    /// Bytes from `start` to the end.
    pub const fn from_start(start: u64) -> Self {
        Self::Range { start, end: None }
    }

    /// The last `length` bytes.
    pub const fn suffix(length: u64) -> Self {
        Self::Suffix(length)
    }
}

impl From<&str> for ByteRange {
    fn from(v: &str) -> Self {
        Self::Raw(v.to_owned())
    }
}

impl From<String> for ByteRange {
    fn from(v: String) -> Self {
        Self::Raw(v)
    }
}

impl From<std::ops::Range<u64>> for ByteRange {
    /// A half-open range: `0..100` is `bytes=0-99`. An empty range is kept as is
    /// (`end` before `start`) and refused by [`range_header`].
    fn from(r: std::ops::Range<u64>) -> Self {
        match r.end.checked_sub(1) {
            Some(last) => Self::Range { start: r.start, end: Some(last) },
            None => Self::Range { start: 1, end: Some(0) },
        }
    }
}

impl From<std::ops::RangeInclusive<u64>> for ByteRange {
    fn from(r: std::ops::RangeInclusive<u64>) -> Self {
        Self::new(*r.start(), *r.end())
    }
}

impl From<std::ops::RangeFrom<u64>> for ByteRange {
    fn from(r: std::ops::RangeFrom<u64>) -> Self {
        Self::from_start(r.start)
    }
}

/// Formats a byte range as a `Range` header value (`bytes=0-1023`, `bytes=-500`).
///
/// # Errors
///
/// [`Error::InvalidRange`] when the end is before the start.
pub fn range_header(r: &ByteRange) -> Result<String> {
    match r {
        ByteRange::Raw(s) => Ok(s.clone()),
        ByteRange::Suffix(n) => Ok(format!("bytes=-{n}")),
        ByteRange::Range { start, end: None } => Ok(format!("bytes={start}-")),
        ByteRange::Range { start, end: Some(end) } => {
            if end < start {
                return Err(Error::InvalidRange(format!("range end {end} is before start {start}")));
            }
            Ok(format!("bytes={start}-{end}"))
        }
    }
}
