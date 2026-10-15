//! URL signing, byte-compatible with the server (`internal/security/sign`).
//!
//! - imgproxy: `base64url(HMAC-SHA256(key, salt ‖ path)[..size])`, the signature
//!   segment of `/{signature}/{options}/{source}`; `/video` URLs sign
//!   `"/video" + path`.
//! - `xi_sig`: the same construction over `path ‖ "?" ‖ query-without-xi_sig`,
//!   appended as the last query parameter (docs/URL-DIALECTS.md §2).

use hmac::{Hmac, KeyInit, Mac};
use sha2::Sha256;

use crate::error::{Error, Result};
use crate::generated::constants::{DEFAULT_SIGNATURE_SIZE, MAX_SIGNATURE_SIZE, MIN_SIGNATURE_SIZE, XI_SIG_PARAM};
use crate::text::{KeyMaterial, b64url};

/// A signing key: `XI_SIGNING_KEYS` / `XI_SIGNING_SALTS` (imgproxy `IMGPROXY_KEY` / `IMGPROXY_SALT`).
///
/// `Debug` never shows the key or the salt.
///
/// ```
/// use xtandard_image::{SigningKey, sign};
///
/// let key = SigningKey::new("736563726574", "68656C6C6F")?; // hex, as in the server config
/// assert_eq!(key.size(), 32);
/// let short = key.clone().with_size(8)?;
/// assert_eq!(sign("/rs:fit:300:300/plain/a.jpg", &short).len(), 11);
/// # Ok::<(), xtandard_image::Error>(())
/// ```
#[derive(Clone, PartialEq, Eq)]
pub struct SigningKey {
    key: Vec<u8>,
    salt: Vec<u8>,
    size: u8,
}

impl std::fmt::Debug for SigningKey {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("SigningKey").field("size", &self.size).finish_non_exhaustive()
    }
}

impl SigningKey {
    /// A key and salt, each hex (as in the server config) or raw bytes; the signature size is 32.
    ///
    /// # Errors
    ///
    /// [`Error::InvalidKey`] when a hex string is not hex.
    pub fn new(key: impl Into<KeyMaterial>, salt: impl Into<KeyMaterial>) -> Result<Self> {
        Ok(Self {
            key: key.into().bytes("signing key")?,
            salt: salt.into().bytes("signing salt")?,
            size: DEFAULT_SIGNATURE_SIZE,
        })
    }

    /// Sets the signature size in bytes before encoding, 1 to 32 (`XI_SIGNING_SIZE`).
    ///
    /// # Errors
    ///
    /// [`Error::InvalidKey`] when the size is outside 1–32.
    pub fn with_size(mut self, size: u8) -> Result<Self> {
        if !(MIN_SIGNATURE_SIZE..=MAX_SIGNATURE_SIZE).contains(&size) {
            return Err(Error::InvalidKey(format!(
                "signature size {size} out of range [{MIN_SIGNATURE_SIZE}, {MAX_SIGNATURE_SIZE}]"
            )));
        }
        self.size = size;
        Ok(self)
    }

    /// The signature size in bytes before encoding.
    pub fn size(&self) -> u8 {
        self.size
    }
}

/// Signs a message with the salted HMAC-SHA256 construction of the imgproxy and `xi_sig` schemes.
///
/// Like the server, a message without a leading `/` is signed as if it had one.
///
/// ```
/// use xtandard_image::{SigningKey, sign};
///
/// let key = SigningKey::new("736563726574", "68656C6C6F")?;
/// assert_eq!(
///     sign("/rs:fill:300:400:0/g:sm/aHR0cDovL2V4YW1w/bGUuY29tL2ltYWdl/cy9jdXJpb3NpdHku/anBn.png", &key),
///     "oKfUtW34Dvo2BGQehJFR4Nr0_rIjOtdtzJ3QFsUcXH8",
/// );
/// # Ok::<(), xtandard_image::Error>(())
/// ```
///
/// # Panics
///
/// Never: HMAC takes keys of any length.
pub fn sign(message: &str, key: &SigningKey) -> String {
    let mut mac = <Hmac<Sha256> as KeyInit>::new_from_slice(&key.key).expect("HMAC takes keys of any size");
    mac.update(&key.salt);
    if !message.starts_with('/') {
        mac.update(b"/");
    }
    mac.update(message.as_bytes());
    let digest = mac.finalize().into_bytes();
    b64url(&digest[..usize::from(key.size)])
}

/// Returns `"/" + signature + path` for an imgproxy path (everything after the signature segment).
pub fn sign_imgproxy_path(path: &str, key: &SigningKey) -> String {
    let p = if path.starts_with('/') { path.to_owned() } else { format!("/{path}") };
    format!("/{}{p}", sign(&p, key))
}

/// Splits a raw query into its `xi_sig` value and the message `xi_sig` signs.
///
/// The message is the path, then `?` and the remaining non-empty parameters
/// in their original order and encoding.
///
/// ```
/// use xtandard_image::xi_message;
///
/// assert_eq!(xi_message("/", "url=x&&xi_sig=s&w=1")?, (Some("s".to_owned()), "/?url=x&w=1".to_owned()));
/// assert_eq!(xi_message("/", "")?, (None, "/".to_owned()));
/// # Ok::<(), xtandard_image::Error>(())
/// ```
///
/// # Errors
///
/// [`Error::InvalidRequest`] when the query has more than one `xi_sig` (the server answers 403).
pub fn xi_message(path: &str, raw_query: &str) -> Result<(Option<String>, String)> {
    let mut sig: Option<String> = None;
    let mut kept: Vec<&str> = Vec::new();
    for part in raw_query.split('&') {
        if part.is_empty() {
            continue;
        }
        let (k, v) = part.split_once('=').unwrap_or((part, ""));
        if k == XI_SIG_PARAM {
            if sig.is_some() {
                return Err(Error::InvalidRequest(format!("query has more than one {XI_SIG_PARAM} parameter")));
            }
            sig = Some(v.to_owned());
            continue;
        }
        kept.push(part);
    }
    let msg = if kept.is_empty() { path.to_owned() } else { format!("{path}?{}", kept.join("&")) };
    Ok((sig, msg))
}

/// Returns `raw_query` without any `xi_sig` and with a fresh `xi_sig` for `path` appended last.
///
/// `path` is the escaped path the dialect sees, after its mount prefix.
///
/// # Errors
///
/// [`Error::InvalidRequest`] when the query has more than one `xi_sig`.
pub fn sign_xi_query(path: &str, raw_query: &str, key: &SigningKey) -> Result<String> {
    let (_, msg) = xi_message(path, raw_query.strip_prefix('?').unwrap_or(raw_query))?;
    let sig = sign(&msg, key);
    let q = &msg[path.len()..];
    Ok(if q.is_empty() { format!("{XI_SIG_PARAM}={sig}") } else { format!("{}&{XI_SIG_PARAM}={sig}", &q[1..]) })
}

/// The length of a leading `scheme://`, if `s` has one.
fn absolute_prefix(s: &str) -> Option<usize> {
    let (scheme, _) = s.split_once("://")?;
    let mut chars = scheme.chars();
    let first = chars.next()?;
    (first.is_ascii_alphabetic() && chars.all(|c| c.is_ascii_alphanumeric() || matches!(c, '+' | '.' | '-')))
        .then_some(scheme.len() + 3)
}

/// Signs a URL (absolute, or a path with an optional query) with `xi_sig`.
///
/// `mount` is the dialect's mount prefix, which the signed path excludes
/// (`"/q"` for the query dialect; `""` for none). A fragment is dropped.
///
/// ```
/// use xtandard_image::{SigningKey, sign_xi_url};
///
/// let key = SigningKey::new("736563726574", "68656C6C6F")?;
/// let url = sign_xi_url("https://img.example.com/q/?url=https://e.com/a.jpg&w=300", &key, "/q")?;
/// assert!(url.starts_with("https://img.example.com/q/?url=https://e.com/a.jpg&w=300&xi_sig="));
/// # Ok::<(), xtandard_image::Error>(())
/// ```
///
/// # Errors
///
/// [`Error::InvalidRequest`] when the path is not under the mount or the query has two `xi_sig`.
pub fn sign_xi_url(url: &str, key: &SigningKey, mount: &str) -> Result<String> {
    let no_hash = url.split_once('#').map_or(url, |(a, _)| a);
    let (head, query) = no_hash.split_once('?').unwrap_or((no_hash, ""));
    let (origin, path) = match absolute_prefix(head) {
        Some(start) => match head[start..].find('/') {
            Some(i) => head.split_at(start + i),
            None => (head, "/"),
        },
        None => ("", head),
    };
    let m = mount.trim_end_matches('/');
    let mut path = path;
    if !m.is_empty() {
        let under = path == m || path.strip_prefix(m).is_some_and(|rest| rest.starts_with('/'));
        if !under {
            return Err(Error::InvalidRequest(format!("path {path} is not under the mount {m}")));
        }
        path = &path[m.len()..];
        if path.is_empty() {
            path = "/";
        }
    }
    Ok(format!("{origin}{m}{path}?{}", sign_xi_query(path, query, key)?))
}
