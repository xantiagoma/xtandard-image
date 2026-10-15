//! The official Rust client of [xtandard-image](https://github.com/xantiagoma/xtandard-image).
//!
//! - Typed URL builders for the imgproxy dialect (processing, `/info`,
//!   `/video`, HLS and DASH: [`ImgproxyUrlBuilder`]) and the query dialect
//!   (`/q/`, `/q/video`, imaginary endpoints: [`QueryUrlBuilder`]), in an
//!   object form ([`Options`]) and a fluent form with one method per option
//!   name and alias.
//! - imgproxy and `xi_sig` signing ([`SigningKey`], [`sign`], [`sign_xi_url`]).
//! - `enc/` source and PDF password encryption ([`EncryptionKey`]).
//! - HTTP clients on reqwest: async [`Client`] (feature `async`, default) and
//!   [`blocking::Client`] (feature `blocking`).
//!
//! URL building has no I/O (only the OS random source for encryption IVs) and
//! works with `default-features = false`.
//!
//! **Build and sign URLs on a server.** The signing key signs every URL, and the
//! encryption key decrypts every `enc/` source and PDF password.
//!
//! ```
//! use xtandard_image::{ImgproxyUrlBuilder, Options, SigningKey, UrlParams, processing::{GravityType, ResizeType}};
//!
//! let img = ImgproxyUrlBuilder::new()
//!     .base_url("https://img.example.com")
//!     .signing(SigningKey::new("943b421c9eb07c830af81030552c86009268de4e532ba2ee2eab8247c6da0881",
//!                              "520f986b998545b4785e0defbc4f3c1203f22de2374a3d53cb7a7fe9fea309c5")?);
//!
//! // Object form: option names or aliases, a tuple or a bare value each.
//! let url = img.url(
//!     "https://example.com/images/cat.jpg",
//!     Options::new().set("rs", (ResizeType::Fit, 300, 300)),
//!     UrlParams::new().extension("png"),
//! )?;
//! assert_eq!(
//!     url,
//!     "https://img.example.com/3kUTYFkXjSOKZ0vB6jAnVMUscg3Qa5k63uXjN9ru1MQ/rs:fit:300:300/aHR0cHM6Ly9leGFtcGxlLmNvbS9pbWFnZXMvY2F0LmpwZw.png",
//! );
//!
//! // Fluent form: the same URL.
//! let fluent = img.image("https://example.com/images/cat.jpg").resize((ResizeType::Fit, 300, 300)).extension("png").build()?;
//! assert_eq!(fluent, url);
//!
//! // Chained pipelines and a gravity focus point.
//! let chained = img
//!     .image("s3://photos/cat.jpg")
//!     .rs((ResizeType::Fill, 600, 400))
//!     .g((GravityType::Fp, 0.5, 0.25))
//!     .pipeline()
//!     .bl(2)
//!     .build()?;
//! assert!(chained.contains("/rs:fill:600:400/g:fp:0.5:0.25/-/bl:2/"));
//! # Ok::<(), xtandard_image::Error>(())
//! ```
//!
//! Errors carry the language-neutral code of `clients/spec/conformance.json`
//! ([`Error::code`]).
#![forbid(unsafe_code)]
#![cfg_attr(docsrs, feature(doc_cfg))]

mod args;
mod encryption;
mod error;
#[rustfmt::skip]
#[allow(clippy::pedantic, clippy::doc_markdown, rustdoc::bare_urls)]
mod generated;
mod imgproxy;
mod macros;
mod options;
mod query;
mod range;
mod signing;
mod sources;
mod text;

#[cfg(any(feature = "async", feature = "blocking"))]
mod client;

pub(crate) use macros::{option_names, str_enum};

pub use args::{Arg, IntoArgs, Options, Params, Pipelines};
pub use encryption::{EncryptionKey, encrypt_pdf_password, encrypt_source};
pub use error::{Error, Result};
pub use generated::constants::*;
pub use generated::options::{info, processing, video};
pub use imgproxy::{
    ImgproxyInfoUrlChain, ImgproxyUrlBuilder, ImgproxyUrlChain, ImgproxyVideoUrlChain, InfoUrlParams, StreamParams,
    UrlParams, VideoUrlParams, imgproxy,
};
pub use options::OptionNames;
pub use query::{QueryUrlBuilder, QueryUrlChain, QueryUrlParams, QueryVideoUrlChain, query_urls};
pub use range::{ByteRange, range_header};
pub use signing::{SigningKey, sign, sign_imgproxy_path, sign_xi_query, sign_xi_url, xi_message};
pub use sources::{source_url, xi_source};
pub use text::{KeyMaterial, escape_plain_source, escape_query_value};

/// serde types of the JSON response bodies (feature `serde`, on with either client).
#[cfg(feature = "serde")]
#[cfg_attr(docsrs, doc(cfg(feature = "serde")))]
pub mod responses {
    pub use crate::generated::responses::*;
}

#[cfg(feature = "serde")]
pub use generated::responses::{ErrorResponse, InfoResponse};

#[cfg(any(feature = "async", feature = "blocking"))]
pub use client::{
    ClientBuilder, ClientError, HttpError, HttpErrorClass, MediaOptions, Method, Readiness, ReadyState, StreamManifest,
};

#[cfg(feature = "async")]
#[cfg_attr(docsrs, doc(cfg(feature = "async")))]
pub use client::async_impl::Client;

/// The reqwest version the clients use (for [`ClientBuilder::build_with`] and the responses).
#[cfg(any(feature = "async", feature = "blocking"))]
pub use reqwest;

/// The blocking HTTP client (feature `blocking`).
#[cfg(feature = "blocking")]
#[cfg_attr(docsrs, doc(cfg(feature = "blocking")))]
pub mod blocking {
    pub use crate::client::blocking::Client;
}
