//! Source references (docs/URL-DIALECTS.md §3.1): the string a URL carries as its source.
//!
//! These helpers only join strings; the URL builders escape or encode the result:
//!
//! ```text
//! https://host/path         an http source
//! s3://bucket/key           gs://bucket/key   abs://container/key
//! local:///path             ftp://host/path   sftp://host/path
//! xi://<source>/<key>       a named source (XI_SOURCES)
//! ```

use crate::error::{Error, Result};
use crate::generated::constants::SourceScheme;

/// Whether `name` matches `sources.xi_name_pattern` (`^[A-Za-z0-9][A-Za-z0-9_.-]*$`).
fn is_source_name(name: &str) -> bool {
    let mut bytes = name.bytes();
    bytes.next().is_some_and(|b| b.is_ascii_alphanumeric())
        && bytes.all(|b| b.is_ascii_alphanumeric() || matches!(b, b'_' | b'.' | b'-'))
}

/// A named source reference, `xi://<name>/<key>`.
///
/// The key may be relative or, for an http source, an absolute URL; leading
/// slashes are dropped.
///
/// ```
/// use xtandard_image::xi_source;
///
/// assert_eq!(xi_source("photos", "2026/cat.jpg")?, "xi://photos/2026/cat.jpg");
/// assert_eq!(xi_source("web", "https://cdn.example.com/a.jpg")?, "xi://web/https://cdn.example.com/a.jpg");
/// # Ok::<(), xtandard_image::Error>(())
/// ```
///
/// # Errors
///
/// [`Error::InvalidName`] when the name is not a source name.
pub fn xi_source(name: &str, key: &str) -> Result<String> {
    if !is_source_name(name) {
        return Err(Error::InvalidName(format!("invalid source name {name:?}")));
    }
    Ok(format!("xi://{name}/{}", key.trim_start_matches('/')))
}

/// A source reference for a scheme: the bucket, container, host or source
/// name, then the key. `local` takes only a path (`local:///<path>`).
///
/// ```
/// use xtandard_image::{SourceScheme, source_url};
///
/// assert_eq!(source_url(SourceScheme::S3, "photos", "cat.jpg")?, "s3://photos/cat.jpg");
/// assert_eq!(source_url(SourceScheme::Local, "", "img/cat.jpg")?, "local:///img/cat.jpg");
/// # Ok::<(), xtandard_image::Error>(())
/// ```
///
/// # Errors
///
/// [`Error::InvalidName`] when the authority is missing, has `/`, `?`, `#` or
/// whitespace, or is given for `local`.
pub fn source_url(scheme: SourceScheme, authority: &str, key: &str) -> Result<String> {
    let key = key.trim_start_matches('/');
    match scheme {
        SourceScheme::Xi => xi_source(authority, key),
        SourceScheme::Local => {
            if !authority.is_empty() {
                return Err(Error::InvalidName("local sources have no authority: local:///<path>".to_owned()));
            }
            Ok(format!("local:///{key}"))
        }
        _ => {
            if authority.is_empty() || authority.chars().any(|c| matches!(c, '/' | '?' | '#') || c.is_whitespace()) {
                return Err(Error::InvalidName(format!("invalid {scheme} authority {authority:?}")));
            }
            Ok(format!("{scheme}://{authority}/{key}"))
        }
    }
}
