//! Query dialect URLs (docs/URL-DIALECTS.md §4), mounted at `/q/`.
//!
//! ```text
//! {base_url}{mount}/?url=<source>&<native key>=…&o=<imgproxy option path>[&xi_sig=…]
//! {base_url}{mount}/video?url=<source>&<video option>=<arg,arg>…[&xi_sig=…]
//! {base_url}{mount}/<imaginary endpoint>?url=<source>&<param>=…[&xi_sig=…]
//! ```
//!
//! `xi_sig` covers the escaped path after the mount plus the query, so it is appended last.

use crate::args::{Arg, Entry, IntoArgs, Options, Params, Pipelines};
use crate::encryption::EncryptionKey;
use crate::error::{Error, Result};
use crate::generated::constants::{
    DEFAULT_SEPARATOR, FORBIDDEN_QUERY_SEPARATORS, NATIVE_VIDEO_KEYS, PDF_PASSWORD_NAME, PDF_PASSWORD_SHORT,
    QUERY_MOUNT,
};
use crate::generated::methods::{processing_methods, video_methods};
use crate::generated::options::{processing, video};
use crate::imgproxy::{ChainState, StreamParams, chain_core, check_separator, password_token};
use crate::options::{OptionNames, format_arg};
use crate::signing::{SigningKey, sign_xi_query};
use crate::text::{check_extension, check_text, escape_query_value, format_float, format_int};

/// Builds query dialect URLs (the server needs `XI_DIALECTS_QUERY_ENABLED=true`).
///
/// With a signing key, every URL ends in an `xi_sig` over the path after the mount and the query.
///
/// ```
/// use xtandard_image::{Options, QueryUrlBuilder, QueryUrlParams};
///
/// let q = QueryUrlBuilder::new().base_url("https://img.example.com");
/// let url = q.url(
///     "s3://bucket/cat.jpg",
///     Options::new().set("bl", 2),
///     QueryUrlParams::new().param("w", 300).param("f", "webp"),
/// )?;
/// assert_eq!(url, "https://img.example.com/q/?url=s3://bucket/cat.jpg&w=300&f=webp&o=bl:2");
/// assert_eq!(q.image("s3://bucket/cat.jpg").param("w", 300).param("f", "webp").bl(2).build()?, url);
/// # Ok::<(), xtandard_image::Error>(())
/// ```
#[derive(Debug, Clone)]
pub struct QueryUrlBuilder {
    base_url: String,
    mount: String,
    signing: Option<SigningKey>,
    encryption: Option<EncryptionKey>,
    separator: char,
    option_names: OptionNames,
}

impl Default for QueryUrlBuilder {
    fn default() -> Self {
        Self::new()
    }
}

/// Shorthand for [`QueryUrlBuilder::new`], named like the TypeScript client's factory.
pub fn query_urls() -> QueryUrlBuilder {
    QueryUrlBuilder::new()
}

/// Extra inputs of a native `/q/` URL ([`QueryUrlBuilder::url`]).
#[derive(Debug, Clone, Default, PartialEq)]
pub struct QueryUrlParams {
    params: Params,
    extension: Option<String>,
    pdf_password: Option<String>,
}

impl QueryUrlParams {
    /// No native keys, no extension, no PDF password.
    pub fn new() -> Self {
        Self::default()
    }

    /// Appends a native key (`w`, `fit`, `g`, `f`, `q`, …), written before `o=`; `None` skips it.
    pub fn param(mut self, key: impl Into<String>, value: impl Into<Arg>) -> Self {
        self.params.push(key, value);
        self
    }

    /// Sets every native key at once.
    pub fn params(mut self, params: impl Into<Params>) -> Self {
        self.params = params.into();
        self
    }

    /// The output format (`format=`).
    pub fn extension(mut self, ext: impl AsRef<str>) -> Self {
        self.extension = Some(ext.as_ref().to_owned());
        self
    }

    /// The plain-text password of a protected PDF, encrypted into `pdp=` (needs both keys).
    pub fn pdf_password(mut self, password: impl Into<String>) -> Self {
        self.pdf_password = Some(password.into());
        self
    }
}

fn native_value(key: &str, v: &Arg) -> Result<Option<String>> {
    let where_ = format!("query key {key}");
    Ok(Some(match v {
        Arg::Null => return Ok(None),
        Arg::Bool(b) => (if *b { "true" } else { "false" }).to_owned(),
        Arg::Int(i) => format_int(*i, &where_)?,
        Arg::Float(f) => format_float(*f, &where_)?,
        Arg::Str(s) => s.clone(),
    }))
}

fn param_pairs<'a>(params: impl IntoIterator<Item = (&'a str, &'a Arg)>) -> Result<Vec<(String, String)>> {
    let mut out = Vec::new();
    for (k, v) in params {
        if let Some(s) = native_value(k, v)? {
            out.push((k.to_owned(), s));
        }
    }
    Ok(out)
}

fn is_endpoint(e: &str) -> bool {
    !e.is_empty() && e.bytes().all(|b| b.is_ascii_lowercase()) && e != "video"
}

impl QueryUrlBuilder {
    /// Root-relative unsigned URLs under `/q`, `:` separators in `o=` and short option names.
    pub fn new() -> Self {
        Self {
            base_url: String::new(),
            mount: QUERY_MOUNT.to_owned(),
            signing: None,
            encryption: None,
            separator: DEFAULT_SEPARATOR,
            option_names: OptionNames::Short,
        }
    }

    /// The server origin (and any path before the mount); trailing slashes are dropped.
    pub fn base_url(mut self, base_url: impl Into<String>) -> Self {
        self.base_url = base_url.into();
        self
    }

    /// The query dialect's mount prefix (`XI_DIALECTS_QUERY_PREFIX`). Default `/q`.
    pub fn mount(mut self, mount: impl Into<String>) -> Self {
        self.mount = mount.into();
        self
    }

    /// Signs with `xi_sig` (the imgproxy key and salt); without a key URLs are unsigned.
    pub fn signing(mut self, key: SigningKey) -> Self {
        self.signing = Some(key);
        self
    }

    /// The key of PDF passwords (`XI_ENCRYPTION_KEYS`).
    pub fn encryption(mut self, key: EncryptionKey) -> Self {
        self.encryption = Some(key);
        self
    }

    /// The argument separator inside `o=`. Default `:`.
    pub fn separator(mut self, separator: char) -> Self {
        self.separator = separator;
        self
    }

    /// How option names are written: short (default), canonical or as given.
    pub fn option_names(mut self, style: OptionNames) -> Self {
        self.option_names = style;
        self
    }

    /// Builds a native `/q/?url=…` URL: native keys, then the processing options as `o=`.
    ///
    /// # Errors
    ///
    /// An [`Error`] whose code says what is wrong.
    pub fn url(&self, source: &str, options: impl Into<Pipelines>, params: QueryUrlParams) -> Result<String> {
        let QueryUrlParams { params, extension, pdf_password } = params;
        self.build_image(&options.into().entries(), source, &params, extension.as_deref(), pdf_password.as_deref())
    }

    /// Builds a `/q/video` URL; `crop` and `flip` are written `c` and `fl`.
    ///
    /// Several pipelines are written in order, so a key may repeat (later wins argument by argument).
    ///
    /// # Errors
    ///
    /// An [`Error`] whose code says what is wrong; an argument with `:` is [`Error::InvalidArgument`].
    pub fn video_url(&self, source: &str, options: impl Into<Pipelines>, extension: Option<&str>) -> Result<String> {
        let entries: Vec<Entry> = options.into().entries().into_iter().flatten().collect();
        self.build_video(&entries, source, extension)
    }

    /// Builds a `/q/video` HLS or DASH manifest URL (`format=m3u8|mpd`).
    ///
    /// # Errors
    ///
    /// As [`video_url`](Self::video_url); a source encoding is [`Error::InvalidRequest`]
    /// (query URLs carry the source as is).
    pub fn stream_url(&self, source: &str, stream: &StreamParams) -> Result<String> {
        if stream.has_source_encoding() {
            return Err(Error::InvalidRequest("query URLs have no source encoding".to_owned()));
        }
        let (entries, ext) = stream.parts()?;
        self.build_video(&entries, source, Some(ext))
    }

    /// Builds an imaginary endpoint URL (`/q/resize?url=…&width=300`); the endpoint is a
    /// string or an [`ImaginaryEndpoint`](crate::ImaginaryEndpoint).
    ///
    /// ```
    /// use xtandard_image::{ImaginaryEndpoint, QueryUrlBuilder};
    ///
    /// let url = QueryUrlBuilder::new().imaginary_url(ImaginaryEndpoint::Resize, "https://e.com/a.jpg", [("width", 300)])?;
    /// assert_eq!(url, "/q/resize?url=https://e.com/a.jpg&width=300");
    /// # Ok::<(), xtandard_image::Error>(())
    /// ```
    ///
    /// # Errors
    ///
    /// [`Error::InvalidEndpoint`] when the endpoint is not lower-case letters or is `video`.
    pub fn imaginary_url(&self, endpoint: impl AsRef<str>, source: &str, params: impl Into<Params>) -> Result<String> {
        let endpoint = endpoint.as_ref();
        if !is_endpoint(endpoint) {
            return Err(Error::InvalidEndpoint(format!("invalid imaginary endpoint {endpoint:?}")));
        }
        check_text(source, Error::InvalidSource, "source")?;
        let params = params.into();
        let mut pairs = vec![("url".to_owned(), source.to_owned())];
        pairs.extend(param_pairs(params.iter())?);
        self.finish(&format!("/{endpoint}"), &pairs)
    }

    /// Starts a fluent `/q/?url=…` URL for a source.
    pub fn image(&self, source: impl Into<String>) -> QueryUrlChain<'_> {
        QueryUrlChain { b: self, s: ChainState::new(source.into()) }
    }

    /// Starts a fluent `/q/video` URL for a source.
    pub fn video(&self, source: impl Into<String>) -> QueryVideoUrlChain<'_> {
        QueryVideoUrlChain { b: self, s: ChainState::new(source.into()) }
    }

    fn finish(&self, path: &str, pairs: &[(String, String)]) -> Result<String> {
        let base = self.base_url.trim_end_matches('/');
        let mount = self.mount.trim_end_matches('/');
        let mut query = pairs
            .iter()
            .map(|(k, v)| format!("{}={}", escape_query_value(k), escape_query_value(v)))
            .collect::<Vec<_>>()
            .join("&");
        if let Some(key) = &self.signing {
            query = sign_xi_query(path, &query, key)?;
        }
        Ok(format!("{base}{mount}{path}?{query}"))
    }

    fn build_image(
        &self,
        pipelines: &[Vec<Entry>],
        source: &str,
        params: &Params,
        ext: Option<&str>,
        pdf_password: Option<&str>,
    ) -> Result<String> {
        let sep = check_separator(self.separator, FORBIDDEN_QUERY_SEPARATORS)?;
        let style = self.option_names;
        check_text(source, Error::InvalidSource, "source")?;
        let mut pairs = vec![("url".to_owned(), source.to_owned())];
        pairs.extend(param_pairs(params.iter())?);
        let mut piped = Vec::new();
        for p in pipelines.iter().filter(|p| !p.is_empty()) {
            let segs =
                p.iter().map(|e| processing::CATALOGUE.format_entry(e, sep, style)).collect::<Result<Vec<_>>>()?;
            piped.push(segs.join("/"));
        }
        if !piped.is_empty() {
            pairs.push(("o".to_owned(), piped.join("/-/")));
        }
        if let Some(e) = ext.filter(|e| !e.is_empty()) {
            pairs.push(("format".to_owned(), check_extension(e)?.to_owned()));
        }
        if let Some(password) = pdf_password {
            let token = password_token(self.encryption.as_ref(), self.signing.as_ref(), password)?;
            let name = if style == OptionNames::Canonical { PDF_PASSWORD_NAME } else { PDF_PASSWORD_SHORT };
            pairs.push((name.to_owned(), token));
        }
        self.finish("/", &pairs)
    }

    fn build_video(&self, entries: &[Entry], source: &str, ext: Option<&str>) -> Result<String> {
        check_text(source, Error::InvalidSource, "source")?;
        let mut pairs = vec![("url".to_owned(), source.to_owned())];
        for (name, args) in entries {
            let key = video::CATALOGUE.written_name(name, self.option_names)?;
            if args.is_empty() {
                return Err(Error::InvalidArgument(format!("option {name}: at least one argument is required")));
            }
            let key = NATIVE_VIDEO_KEYS.iter().find(|(k, _)| *k == key).map_or(key, |(_, v)| *v);
            let mut values = Vec::with_capacity(args.len());
            for (i, a) in args.iter().enumerate() {
                let where_ = format!("option {name} argument {}", i + 1);
                let s = format_arg(a, ',', &where_)?;
                if s.contains(':') {
                    return Err(Error::InvalidArgument(format!("{where_}: {s:?} contains ':'")));
                }
                values.push(s);
            }
            pairs.push((key.to_owned(), values.join(",")));
        }
        if let Some(e) = ext.filter(|e| !e.is_empty()) {
            pairs.push(("format".to_owned(), check_extension(e)?.to_owned()));
        }
        self.finish("/video", &pairs)
    }
}

/// A fluent `/q/?url=…` URL ([`QueryUrlBuilder::image`]): the option methods
/// write `o=`, [`param`](Self::param) a native key.
#[derive(Debug, Clone)]
#[must_use = "a chain does nothing until build() is called"]
pub struct QueryUrlChain<'a> {
    b: &'a QueryUrlBuilder,
    s: ChainState,
}

#[allow(clippy::doc_markdown)] // generated from the spec's prose
impl QueryUrlChain<'_> {
    processing_methods!();
}

impl QueryUrlChain<'_> {
    chain_core!();

    /// Appends a native key (`w`, `fit`, `g`, …), written before `o=`; `None` skips it.
    pub fn param(mut self, key: impl Into<String>, value: impl Into<Arg>) -> Self {
        self.s.params.push((key.into(), value.into()));
        self
    }

    /// Starts the next chained pipeline (`/-/` inside `o=`).
    pub fn pipeline(mut self) -> Self {
        self.s.pipelines.push(Vec::new());
        self
    }

    /// Sets the output format (`format=`).
    pub fn extension(mut self, ext: impl AsRef<str>) -> Self {
        self.s.ext = Some(ext.as_ref().to_owned());
        self
    }

    /// Opens a password-protected PDF (`pdp=`; needs both keys).
    pub fn with_pdf_password(mut self, password: impl Into<String>) -> Self {
        self.s.pdf_password = Some(password.into());
        self
    }

    /// Builds the URL.
    ///
    /// # Errors
    ///
    /// As [`QueryUrlBuilder::url`].
    pub fn build(&self) -> Result<String> {
        let s = &self.s;
        let params: Params = s.params.iter().cloned().collect();
        self.b.build_image(&s.pipelines, &s.source, &params, s.ext.as_deref(), s.pdf_password.as_deref())
    }
}

/// A fluent `/q/video` URL ([`QueryUrlBuilder::video`]): one method per video option name and alias.
#[derive(Debug, Clone)]
#[must_use = "a chain does nothing until build() is called"]
pub struct QueryVideoUrlChain<'a> {
    b: &'a QueryUrlBuilder,
    s: ChainState,
}

#[allow(clippy::doc_markdown)] // generated from the spec's prose
impl QueryVideoUrlChain<'_> {
    video_methods!();
}

impl QueryVideoUrlChain<'_> {
    chain_core!();

    /// Sets the output container (`format=`).
    pub fn extension(mut self, ext: impl AsRef<str>) -> Self {
        self.s.ext = Some(ext.as_ref().to_owned());
        self
    }

    /// Builds the URL.
    ///
    /// # Errors
    ///
    /// As [`QueryUrlBuilder::video_url`].
    pub fn build(&self) -> Result<String> {
        let s = &self.s;
        let entries: Vec<Entry> = s.pipelines.iter().flatten().cloned().collect();
        self.b.build_video(&entries, &s.source, s.ext.as_deref())
    }
}
