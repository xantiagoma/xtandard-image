//! Errors of the URL builders, signing, encryption and the source helpers.

/// Why a URL, token or header could not be built.
///
/// There is one variant per language-neutral error code of
/// `clients/spec/conformance.json` ([`Error::code`]); each carries a message
/// for humans.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
#[non_exhaustive]
pub enum Error {
    /// An option name that is not in the catalogue.
    #[error("{0}")]
    UnknownOption(String),
    /// No arguments, a forbidden character, a non-finite number, or `:` in a `/q/video` argument.
    #[error("{0}")]
    InvalidArgument(String),
    /// An empty source, or a source with control characters.
    #[error("{0}")]
    InvalidSource(String),
    /// An extension that is not ASCII letters and digits.
    #[error("{0}")]
    InvalidExtension(String),
    /// A separator the grammar forbids.
    #[error("{0}")]
    InvalidSeparator(String),
    /// A PDF password without a signing key.
    #[error("{0}")]
    MissingSigning(String),
    /// An encrypted source or a PDF password without an encryption key.
    #[error("{0}")]
    MissingEncryption(String),
    /// Key material that is not hex, a key of the wrong size, an IV that is not 16 bytes, a signature size outside 1–32.
    #[error("{0}")]
    InvalidKey(String),
    /// An empty password, a password with control characters, or one over 1024 UTF-8 bytes.
    #[error("{0}")]
    InvalidPassword(String),
    /// Chained `/info` or `/video` pipelines, a stream option given as an option, an unknown protocol,
    /// `ts` on DASH, a URL outside the mount, two `xi_sig` parameters.
    #[error("{0}")]
    InvalidRequest(String),
    /// A bad `xi://` source name or source authority.
    #[error("{0}")]
    InvalidName(String),
    /// An imaginary endpoint that is not lower-case letters, or is `video`.
    #[error("{0}")]
    InvalidEndpoint(String),
    /// A range whose end is before its start.
    #[error("{0}")]
    InvalidRange(String),
}

impl Error {
    /// The language-neutral error code (`unknown_option`, `invalid_argument`, …), as in
    /// `clients/spec/conformance.json`.
    ///
    /// ```
    /// use xtandard_image::ImgproxyUrlBuilder;
    ///
    /// let err = ImgproxyUrlBuilder::new().image("s3://b/a.jpg").option("nope", 1).build().unwrap_err();
    /// assert_eq!(err.code(), "unknown_option");
    /// ```
    pub const fn code(&self) -> &'static str {
        match self {
            Self::UnknownOption(_) => "unknown_option",
            Self::InvalidArgument(_) => "invalid_argument",
            Self::InvalidSource(_) => "invalid_source",
            Self::InvalidExtension(_) => "invalid_extension",
            Self::InvalidSeparator(_) => "invalid_separator",
            Self::MissingSigning(_) => "missing_signing",
            Self::MissingEncryption(_) => "missing_encryption",
            Self::InvalidKey(_) => "invalid_key",
            Self::InvalidPassword(_) => "invalid_password",
            Self::InvalidRequest(_) => "invalid_request",
            Self::InvalidName(_) => "invalid_name",
            Self::InvalidEndpoint(_) => "invalid_endpoint",
            Self::InvalidRange(_) => "invalid_range",
        }
    }

    /// The message, without the code.
    pub fn message(&self) -> &str {
        match self {
            Self::UnknownOption(m)
            | Self::InvalidArgument(m)
            | Self::InvalidSource(m)
            | Self::InvalidExtension(m)
            | Self::InvalidSeparator(m)
            | Self::MissingSigning(m)
            | Self::MissingEncryption(m)
            | Self::InvalidKey(m)
            | Self::InvalidPassword(m)
            | Self::InvalidRequest(m)
            | Self::InvalidName(m)
            | Self::InvalidEndpoint(m)
            | Self::InvalidRange(m) => m,
        }
    }
}

/// `Result` with this crate's [`Error`].
pub type Result<T, E = Error> = std::result::Result<T, E>;
