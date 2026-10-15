//! Source URL and PDF password encryption, byte-compatible with the server (`internal/security/crypt`).
//!
//! `token = base64url_unpadded(IV[16] ‖ AES-CBC(key, IV, PKCS#7(utf8(plaintext))))`.
//! Encrypt on a server: the key decrypts every token.

use cbc::cipher::block_padding::Pkcs7;
use cbc::cipher::{BlockModeEncrypt, KeyIvInit};

use crate::error::{Error, Result};
use crate::generated::constants::{ENCRYPTION_KEY_SIZES, IV_SIZE, MAX_PDF_PASSWORD_BYTES};
use crate::text::{KeyMaterial, b64url, check_text};

/// An encryption key: the first of `XI_ENCRYPTION_KEYS` (imgproxy `IMGPROXY_SOURCE_URL_ENCRYPTION_KEY`).
///
/// Without a fixed IV every token gets a fresh random one, as the server's own
/// encoder does. A fixed IV makes URLs deterministic and reveals when two URLs
/// share a source; use it only for tests or cache keys. `Debug` never shows
/// the key or the IV.
///
/// ```
/// use xtandard_image::{EncryptionKey, encrypt_source};
///
/// let key = EncryptionKey::new("1eb5b0e971ad7f45324c1bb15c947cb207c43152fa5c6c7f35c4f36e0c18e0f1")?
///     .with_iv("30313233343536373839616263646566")?;
/// assert_eq!(
///     encrypt_source("http://img.example.com/pretty/image.jpg", &key)?,
///     "MDEyMzQ1Njc4OWFiY2RlZj206ZwwVKiBydg5ip7OI2oVBFCdcMXXGze1K8CjwiQuFTiJkTDmRJeJccTs0wH06w",
/// );
/// # Ok::<(), xtandard_image::Error>(())
/// ```
#[derive(Clone, PartialEq, Eq)]
pub struct EncryptionKey {
    key: Vec<u8>,
    iv: Option<[u8; IV_SIZE]>,
}

impl std::fmt::Debug for EncryptionKey {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("EncryptionKey")
            .field("bits", &(self.key.len() * 8))
            .field("fixed_iv", &self.iv.is_some())
            .finish_non_exhaustive()
    }
}

impl EncryptionKey {
    /// An AES key, hex or raw bytes: 16, 24 or 32 bytes.
    ///
    /// # Errors
    ///
    /// [`Error::InvalidKey`] when the key is not hex or has the wrong size.
    pub fn new(key: impl Into<KeyMaterial>) -> Result<Self> {
        let key = key.into().bytes("encryption key")?;
        if !ENCRYPTION_KEY_SIZES.contains(&key.len()) {
            return Err(Error::InvalidKey(format!("encryption key is {} bytes, want 16, 24 or 32", key.len())));
        }
        Ok(Self { key, iv: None })
    }

    /// Fixes the IV (16 bytes, hex or raw): every token of the same plaintext is the same.
    ///
    /// # Errors
    ///
    /// [`Error::InvalidKey`] when the IV is not hex or not 16 bytes.
    pub fn with_iv(mut self, iv: impl Into<KeyMaterial>) -> Result<Self> {
        let iv = iv.into().bytes("iv")?;
        self.iv = Some(iv.try_into().map_err(|_| Error::InvalidKey(format!("iv is not {IV_SIZE} bytes")))?);
        Ok(self)
    }

    /// Seals a plaintext into a token.
    fn seal(&self, plaintext: &str) -> String {
        let iv = self.iv.unwrap_or_else(|| {
            let mut iv = [0u8; IV_SIZE];
            getrandom::fill(&mut iv).expect("the operating system random source failed");
            iv
        });
        let data = plaintext.as_bytes();
        let ct = match self.key.len() {
            16 => cbc::Encryptor::<aes::Aes128>::new_from_slices(&self.key, &iv)
                .map(|e| e.encrypt_padded_vec::<Pkcs7>(data)),
            24 => cbc::Encryptor::<aes::Aes192>::new_from_slices(&self.key, &iv)
                .map(|e| e.encrypt_padded_vec::<Pkcs7>(data)),
            _ => cbc::Encryptor::<aes::Aes256>::new_from_slices(&self.key, &iv)
                .map(|e| e.encrypt_padded_vec::<Pkcs7>(data)),
        }
        .expect("key and IV sizes are checked on construction");
        let mut token = Vec::with_capacity(IV_SIZE + ct.len());
        token.extend_from_slice(&iv);
        token.extend_from_slice(&ct);
        b64url(&token)
    }
}

/// Encrypts a source URL into an `enc/` token.
///
/// # Errors
///
/// [`Error::InvalidSource`] when the source is empty or has control characters.
///
/// # Panics
///
/// When the key has no fixed IV and the operating system's random source fails.
pub fn encrypt_source(source: &str, key: &EncryptionKey) -> Result<String> {
    check_text(source, Error::InvalidSource, "source")?;
    Ok(key.seal(source))
}

/// Encrypts the password of a password-protected PDF into a `pdf_password` (`pdp`) token.
///
/// The token is the `enc/` format over the password, with the same key. The
/// server accepts the option on signed URLs only.
///
/// # Errors
///
/// [`Error::InvalidPassword`] when the password is empty, has control
/// characters or is longer than 1024 UTF-8 bytes.
///
/// # Panics
///
/// When the key has no fixed IV and the operating system's random source fails.
pub fn encrypt_pdf_password(password: &str, key: &EncryptionKey) -> Result<String> {
    check_text(password, Error::InvalidPassword, "password")?;
    if password.len() > MAX_PDF_PASSWORD_BYTES {
        return Err(Error::InvalidPassword(format!("password is longer than {MAX_PDF_PASSWORD_BYTES} bytes")));
    }
    Ok(key.seal(password))
}
