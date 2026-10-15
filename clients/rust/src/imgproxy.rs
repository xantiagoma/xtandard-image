//! imgproxy-dialect URL builder (docs/URL-DIALECTS.md §3.1).
//!
//! ```text
//! {base_url}[/info]/{signature}/{option}/…[/-/{option}/…]/{source}
//! {base_url}/video/{signature}/{video option}/…/{source}   (signs "/video" + path)
//! ```
//!
//! Sources are base64url (default), `plain/` (percent-escaped, `@ext`) or
//! `enc/` (AES-CBC); base64 and enc sources take `.ext`.

use crate::args::{Arg, Entry, IntoArgs, Options, Pipelines};
use crate::encryption::{EncryptionKey, encrypt_pdf_password, encrypt_source};
use crate::error::{Error, Result};
use crate::generated::constants::{
    DEFAULT_SEPARATOR, DEFAULT_UNSIGNED, FORBIDDEN_SEPARATORS, PDF_PASSWORD_NAME, PDF_PASSWORD_SHORT, SegmentFormat,
    SourceEncoding, StreamProtocol,
};
use crate::generated::methods::{info_methods, processing_methods, video_methods};
use crate::generated::options::{info, processing, video};
use crate::options::{Catalogue, OptionNames};
use crate::signing::{SigningKey, sign, sign_imgproxy_path};
use crate::text::{b64url, check_extension, check_text, escape_plain_source};

/// Builds imgproxy-dialect URLs for an xtandard-image (or imgproxy) server.
///
/// ```
/// use xtandard_image::{
///     ImgproxyUrlBuilder, Options, SigningKey, StreamParams, StreamProtocol, UrlParams, VideoUrlParams,
///     processing::ResizeType,
/// };
///
/// let img = ImgproxyUrlBuilder::new()
///     .base_url("https://img.example.com")
///     .signing(SigningKey::new("736563726574", "68656C6C6F")?);
///
/// // Object form: options keyed by name or alias, in URL order.
/// let opts = Options::new().set("rs", (ResizeType::Fill, 300, 400)).set("q", 80);
/// let a = img.url("s3://bucket/cat.jpg", &opts, UrlParams::new().extension("webp"))?;
///
/// // Fluent form: one method per option name and alias.
/// let b = img.image("s3://bucket/cat.jpg").resize((ResizeType::Fill, 300, 400)).quality(80).extension("webp").build()?;
/// assert_eq!(a, b);
///
/// let v = img.video_url("s3://bucket/talk.mov", Options::new().set("vs", 12).set("vd", 8), VideoUrlParams::new().extension("webm"))?;
/// assert!(v.starts_with("https://img.example.com/video/"));
/// let s = img.stream_url("s3://bucket/talk.mov", &StreamParams::new(StreamProtocol::Hls).renditions([360, 720]))?;
/// assert!(s.ends_with(".m3u8"));
/// # Ok::<(), xtandard_image::Error>(())
/// ```
#[derive(Debug, Clone)]
pub struct ImgproxyUrlBuilder {
    base_url: String,
    signing: Option<SigningKey>,
    unsigned_signature: String,
    encryption: Option<EncryptionKey>,
    source_encoding: SourceEncoding,
    split_source: usize,
    separator: char,
    option_names: OptionNames,
}

impl Default for ImgproxyUrlBuilder {
    fn default() -> Self {
        Self::new()
    }
}

/// Shorthand for [`ImgproxyUrlBuilder::new`], named like the TypeScript client's factory.
pub fn imgproxy() -> ImgproxyUrlBuilder {
    ImgproxyUrlBuilder::new()
}

/// Extra inputs of a processing URL ([`ImgproxyUrlBuilder::url`]).
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct UrlParams {
    extension: Option<String>,
    source_encoding: Option<SourceEncoding>,
    pdf_password: Option<String>,
}

impl UrlParams {
    /// No extension, the builder's source encoding, no PDF password.
    pub fn new() -> Self {
        Self::default()
    }

    /// The output format extension (`@webp` for plain sources, `.webp` otherwise); a
    /// string or an [`ImageFormat`](crate::ImageFormat).
    pub fn extension(mut self, ext: impl AsRef<str>) -> Self {
        self.extension = Some(ext.as_ref().to_owned());
        self
    }

    /// Overrides the builder's source encoding.
    pub fn source_encoding(mut self, encoding: SourceEncoding) -> Self {
        self.source_encoding = Some(encoding);
        self
    }

    /// The plain-text password of a protected PDF, encrypted into `pdp` (needs both keys).
    pub fn pdf_password(mut self, password: impl Into<String>) -> Self {
        self.pdf_password = Some(password.into());
        self
    }
}

/// Extra inputs of an `/info` URL ([`ImgproxyUrlBuilder::info_url`]): no extension.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct InfoUrlParams {
    source_encoding: Option<SourceEncoding>,
    pdf_password: Option<String>,
}

impl InfoUrlParams {
    /// The builder's source encoding, no PDF password.
    pub fn new() -> Self {
        Self::default()
    }

    /// Overrides the builder's source encoding.
    pub fn source_encoding(mut self, encoding: SourceEncoding) -> Self {
        self.source_encoding = Some(encoding);
        self
    }

    /// The plain-text password of a protected PDF (needs both keys).
    pub fn pdf_password(mut self, password: impl Into<String>) -> Self {
        self.pdf_password = Some(password.into());
        self
    }
}

/// Extra inputs of a `/video` URL ([`ImgproxyUrlBuilder::video_url`]): no PDF password.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct VideoUrlParams {
    extension: Option<String>,
    source_encoding: Option<SourceEncoding>,
}

impl VideoUrlParams {
    /// No extension, the builder's source encoding.
    pub fn new() -> Self {
        Self::default()
    }

    /// The output container: a string or a [`VideoContainer`](crate::VideoContainer).
    pub fn extension(mut self, ext: impl AsRef<str>) -> Self {
        self.extension = Some(ext.as_ref().to_owned());
        self
    }

    /// Overrides the builder's source encoding.
    pub fn source_encoding(mut self, encoding: SourceEncoding) -> Self {
        self.source_encoding = Some(encoding);
        self
    }
}

/// An HLS or DASH manifest request ([`ImgproxyUrlBuilder::stream_url`], [`QueryUrlBuilder::stream_url`](crate::QueryUrlBuilder::stream_url)).
///
/// `stream`, `segment_duration`, `segment_format` and `format` are set from
/// these fields and are refused as options.
#[derive(Debug, Clone, PartialEq)]
pub struct StreamParams {
    protocol: StreamProtocol,
    renditions: Vec<u32>,
    segment_duration: Option<u32>,
    segment_format: Option<SegmentFormat>,
    options: Options,
    source_encoding: Option<SourceEncoding>,
}

impl StreamParams {
    /// A manifest of the protocol with the server's rendition ladder, segment duration and format.
    pub fn new(protocol: StreamProtocol) -> Self {
        Self {
            protocol,
            renditions: Vec::new(),
            segment_duration: None,
            segment_format: None,
            options: Options::new(),
            source_encoding: None,
        }
    }

    /// The rungs, by the short side of the frame (at most 8).
    pub fn renditions(mut self, renditions: impl IntoIterator<Item = u32>) -> Self {
        self.renditions = renditions.into_iter().collect();
        self
    }

    /// The segment length in whole seconds (1 to 30).
    pub fn segment_duration(mut self, seconds: u32) -> Self {
        self.segment_duration = Some(seconds);
        self
    }

    /// `fmp4` (CMAF, the default, never written) or `ts` (MPEG-TS, HLS only).
    pub fn segment_format(mut self, format: SegmentFormat) -> Self {
        self.segment_format = Some(format);
        self
    }

    /// Other `/video` options (trim, crop, quality, audio, …) applied to every rendition.
    pub fn options(mut self, options: Options) -> Self {
        self.options = options;
        self
    }

    /// Overrides the builder's source encoding (imgproxy dialect only).
    pub fn source_encoding(mut self, encoding: SourceEncoding) -> Self {
        self.source_encoding = Some(encoding);
        self
    }

    /// The media type of the manifest.
    pub fn content_type(&self) -> &'static str {
        self.protocol.content_type()
    }

    pub(crate) fn has_source_encoding(&self) -> bool {
        self.source_encoding.is_some()
    }

    /// The video options and the manifest container (the extension).
    pub(crate) fn parts(&self) -> Result<(Vec<Entry>, &'static str)> {
        let mut entries = self.options.entries().to_vec();
        for (name, _) in &entries {
            if matches!(
                video::CATALOGUE.canonical(name),
                Some("stream" | "segment_duration" | "segment_format" | "format")
            ) {
                return Err(Error::InvalidRequest(format!(
                    "stream requests set {name} through protocol, renditions and the segment settings"
                )));
            }
        }
        let protocol = self.protocol;
        if !self.renditions.is_empty() {
            let mut args = vec![Arg::from(protocol)];
            args.extend(self.renditions.iter().map(|&r| Arg::from(r)));
            entries.push(("stream".to_owned(), args));
        }
        if let Some(d) = self.segment_duration {
            entries.push(("segment_duration".to_owned(), d.into_args()));
        }
        if let Some(f) = self.segment_format {
            if !f.protocols().contains(&protocol) {
                return Err(Error::InvalidRequest(format!("segment format {f} is not available for {protocol}")));
            }
            if f != SegmentFormat::DEFAULT {
                entries.push(("segment_format".to_owned(), f.into_args()));
            }
        }
        Ok((entries, protocol.manifest_container()))
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Endpoint {
    Process,
    Info,
    Video,
}

impl Endpoint {
    fn catalogue(self) -> Catalogue {
        match self {
            Self::Process => processing::CATALOGUE,
            Self::Info => info::CATALOGUE,
            Self::Video => video::CATALOGUE,
        }
    }
}

/// Rejects a separator that the grammar uses.
pub(crate) fn check_separator(sep: char, forbidden: &[char]) -> Result<char> {
    if forbidden.contains(&sep) {
        return Err(Error::InvalidSeparator(format!("invalid argument separator {sep:?}")));
    }
    Ok(sep)
}

/// The `pdf_password` token, checking that both keys are present.
pub(crate) fn password_token(
    encryption: Option<&EncryptionKey>,
    signing: Option<&SigningKey>,
    password: &str,
) -> Result<String> {
    let encryption =
        encryption.ok_or_else(|| Error::MissingEncryption("pdf_password needs an encryption key".to_owned()))?;
    if signing.is_none() {
        return Err(Error::MissingSigning(
            "pdf_password needs signing: the server refuses it on unsigned URLs".to_owned(),
        ));
    }
    encrypt_pdf_password(password, encryption)
}

fn split(token: &str, n: usize) -> String {
    if n == 0 || token.len() <= n {
        return token.to_owned();
    }
    // Base64url is ASCII, so byte chunks are character chunks.
    token.as_bytes().chunks(n).map(|c| String::from_utf8_lossy(c)).collect::<Vec<_>>().join("/")
}

impl ImgproxyUrlBuilder {
    /// Root-relative unsigned URLs (`/insecure/…`), base64 sources, `:` separators and short option names.
    pub fn new() -> Self {
        Self {
            base_url: String::new(),
            signing: None,
            unsigned_signature: DEFAULT_UNSIGNED.to_owned(),
            encryption: None,
            source_encoding: SourceEncoding::Base64,
            split_source: 0,
            separator: DEFAULT_SEPARATOR,
            option_names: OptionNames::Short,
        }
    }

    /// Prepended to every path: the server origin and the dialect's mount prefix, if any
    /// (trailing slashes are dropped).
    pub fn base_url(mut self, base_url: impl Into<String>) -> Self {
        self.base_url = base_url.into();
        self
    }

    /// Signs URLs with the imgproxy scheme; without a key the signature segment is the unsigned placeholder.
    pub fn signing(mut self, key: SigningKey) -> Self {
        self.signing = Some(key);
        self
    }

    /// The signature segment of unsigned URLs: `insecure` (default), `unsafe` or `_`.
    pub fn unsigned_signature(mut self, placeholder: impl Into<String>) -> Self {
        self.unsigned_signature = placeholder.into();
        self
    }

    /// The key of `encrypted` sources and PDF passwords.
    pub fn encryption(mut self, key: EncryptionKey) -> Self {
        self.encryption = Some(key);
        self
    }

    /// The default source encoding (`base64`, `plain` or `encrypted`).
    pub fn source_encoding(mut self, encoding: SourceEncoding) -> Self {
        self.source_encoding = encoding;
        self
    }

    /// Splits base64 and encrypted sources into `/`-separated chunks of this many characters (0: no split).
    pub fn split_source(mut self, chunk: usize) -> Self {
        self.split_source = chunk;
        self
    }

    /// The argument separator (`XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR`). Default `:`.
    pub fn separator(mut self, separator: char) -> Self {
        self.separator = separator;
        self
    }

    /// How option names are written: short (default), canonical or as given.
    pub fn option_names(mut self, style: OptionNames) -> Self {
        self.option_names = style;
        self
    }

    /// Builds a processing URL; several pipelines (a `Vec` or array of [`Options`]) chain with `/-/`.
    ///
    /// # Errors
    ///
    /// An [`Error`] whose code says what is wrong (`unknown_option`, `invalid_argument`, `invalid_source`, …).
    pub fn url(&self, source: &str, options: impl Into<Pipelines>, params: UrlParams) -> Result<String> {
        let UrlParams { extension, source_encoding, pdf_password } = params;
        self.build(
            Endpoint::Process,
            &options.into().entries(),
            source,
            source_encoding,
            extension.as_deref(),
            pdf_password.as_deref(),
        )
    }

    /// Builds an `/info` URL (one pipeline).
    ///
    /// # Errors
    ///
    /// As [`url`](Self::url), and [`Error::InvalidRequest`] for chained pipelines.
    pub fn info_url(&self, source: &str, options: impl Into<Pipelines>, params: InfoUrlParams) -> Result<String> {
        let InfoUrlParams { source_encoding, pdf_password } = params;
        self.build(Endpoint::Info, &options.into().entries(), source, source_encoding, None, pdf_password.as_deref())
    }

    /// Builds a `/video` URL: a video transformation, audio or a waveform; the extension is the container.
    ///
    /// # Errors
    ///
    /// As [`url`](Self::url), and [`Error::InvalidRequest`] for chained pipelines.
    pub fn video_url(&self, source: &str, options: impl Into<Pipelines>, params: VideoUrlParams) -> Result<String> {
        let VideoUrlParams { extension, source_encoding } = params;
        self.build(Endpoint::Video, &options.into().entries(), source, source_encoding, extension.as_deref(), None)
    }

    /// Builds an HLS or DASH manifest URL (`/video/…@m3u8` or `.mpd`).
    ///
    /// # Errors
    ///
    /// As [`video_url`](Self::video_url), and [`Error::InvalidRequest`] for a
    /// stream setting given as an option or `ts` segments on DASH.
    pub fn stream_url(&self, source: &str, stream: &StreamParams) -> Result<String> {
        let (entries, ext) = stream.parts()?;
        self.build(Endpoint::Video, &[entries], source, stream.source_encoding, Some(ext), None)
    }

    /// Starts a fluent processing URL for a source.
    pub fn image(&self, source: impl Into<String>) -> ImgproxyUrlChain<'_> {
        ImgproxyUrlChain { b: self, s: ChainState::new(source.into()) }
    }

    /// Starts a fluent `/info` URL for a source.
    pub fn info(&self, source: impl Into<String>) -> ImgproxyInfoUrlChain<'_> {
        ImgproxyInfoUrlChain { b: self, s: ChainState::new(source.into()) }
    }

    /// Starts a fluent `/video` URL for a source.
    pub fn video(&self, source: impl Into<String>) -> ImgproxyVideoUrlChain<'_> {
        ImgproxyVideoUrlChain { b: self, s: ChainState::new(source.into()) }
    }

    fn source_segment(&self, source: &str, encoding: SourceEncoding, ext: Option<&str>) -> Result<String> {
        check_text(source, Error::InvalidSource, "source")?;
        let ext = match ext {
            None | Some("") => None,
            Some(e) => Some(check_extension(e)?),
        };
        Ok(match encoding {
            SourceEncoding::Plain => {
                let mut s = format!("plain/{}", escape_plain_source(source));
                if let Some(e) = ext {
                    s.push('@');
                    s.push_str(e);
                }
                s
            }
            SourceEncoding::Base64 | SourceEncoding::Encrypted => {
                let mut s = if encoding == SourceEncoding::Base64 {
                    split(&b64url(source.as_bytes()), self.split_source)
                } else {
                    let key = self.encryption.as_ref().ok_or_else(|| {
                        Error::MissingEncryption("encrypted sources need an encryption key".to_owned())
                    })?;
                    format!("enc/{}", split(&encrypt_source(source, key)?, self.split_source))
                };
                if let Some(e) = ext {
                    s.push('.');
                    s.push_str(e);
                }
                s
            }
        })
    }

    fn finish(&self, endpoint: Endpoint, path: &str) -> String {
        let base = self.base_url.trim_end_matches('/');
        if endpoint == Endpoint::Video {
            let sig = self
                .signing
                .as_ref()
                .map_or_else(|| self.unsigned_signature.clone(), |k| sign(&format!("/video{path}"), k));
            return format!("{base}/video/{sig}{path}");
        }
        let signed = self
            .signing
            .as_ref()
            .map_or_else(|| format!("/{}{path}", self.unsigned_signature), |k| sign_imgproxy_path(path, k));
        let prefix = if endpoint == Endpoint::Info { "/info" } else { "" };
        format!("{base}{prefix}{signed}")
    }

    /// Builds a URL from option pipelines and a source, checking in the order of clients/README.md.
    fn build(
        &self,
        endpoint: Endpoint,
        pipelines: &[Vec<Entry>],
        source: &str,
        encoding: Option<SourceEncoding>,
        ext: Option<&str>,
        pdf_password: Option<&str>,
    ) -> Result<String> {
        let sep = check_separator(self.separator, FORBIDDEN_SEPARATORS)?;
        let style = self.option_names;
        let cat = endpoint.catalogue();
        let non_empty: Vec<&Vec<Entry>> = pipelines.iter().filter(|p| !p.is_empty()).collect();
        if endpoint != Endpoint::Process && non_empty.len() > 1 {
            return Err(Error::InvalidRequest(format!("/{} URLs have no chained pipelines", cat.kind)));
        }
        if endpoint == Endpoint::Video && pdf_password.is_some() {
            return Err(Error::InvalidRequest("/video URLs take no pdf_password".to_owned()));
        }
        let mut segments: Vec<String> = Vec::new();
        if let Some(password) = pdf_password {
            let token = password_token(self.encryption.as_ref(), self.signing.as_ref(), password)?;
            let name = if style == OptionNames::Canonical { PDF_PASSWORD_NAME } else { PDF_PASSWORD_SHORT };
            segments.push(format!("{name}{sep}{token}"));
        }
        for (i, p) in non_empty.iter().enumerate() {
            if i > 0 {
                segments.push("-".to_owned());
            }
            for e in *p {
                segments.push(cat.format_entry(e, sep, style)?);
            }
        }
        segments.push(self.source_segment(source, encoding.unwrap_or(self.source_encoding), ext)?);
        Ok(self.finish(endpoint, &format!("/{}", segments.join("/"))))
    }
}

/// What a fluent chain collected.
#[derive(Debug, Clone)]
pub(crate) struct ChainState {
    pub(crate) source: String,
    pub(crate) pipelines: Vec<Vec<Entry>>,
    pub(crate) params: Vec<(String, Arg)>,
    pub(crate) encoding: Option<SourceEncoding>,
    pub(crate) ext: Option<String>,
    pub(crate) pdf_password: Option<String>,
}

impl ChainState {
    pub(crate) fn new(source: String) -> Self {
        Self { source, pipelines: vec![Vec::new()], params: Vec::new(), encoding: None, ext: None, pdf_password: None }
    }

    pub(crate) fn push(&mut self, name: &str, args: Vec<Arg>) {
        self.pipelines.last_mut().expect("there is always a pipeline").push((name.to_owned(), args));
    }

    pub(crate) fn extend(&mut self, options: &Options) {
        self.pipelines.last_mut().expect("there is always a pipeline").extend_from_slice(options.entries());
    }
}

/// The control methods every chain has: `option` and `options`.
macro_rules! chain_core {
    () => {
        /// Appends one option occurrence by name or alias (as the per-option methods do).
        pub fn option(mut self, name: impl AsRef<str>, args: impl IntoArgs) -> Self {
            self.s.push(name.as_ref(), args.into_args());
            self
        }

        /// Appends every option of an [`Options`], in order.
        pub fn options(mut self, options: &Options) -> Self {
            self.s.extend(options);
            self
        }
    };
}
pub(crate) use chain_core;

/// A fluent processing URL ([`ImgproxyUrlBuilder::image`]): one method per option
/// name and alias, `pipeline()` for `/-/`, then [`build`](Self::build).
///
/// ```
/// use xtandard_image::{ImgproxyUrlBuilder, OptionNames, SourceEncoding, processing::ResizeType};
///
/// let b = ImgproxyUrlBuilder::new().option_names(OptionNames::Given).source_encoding(SourceEncoding::Plain);
/// let url = b.image("https://example.com/a.jpg").resize((ResizeType::Fit, 1, 2)).q(90).min_width(10).build()?;
/// assert_eq!(url, "/insecure/resize:fit:1:2/q:90/min-width:10/plain/https://example.com/a.jpg");
/// # Ok::<(), xtandard_image::Error>(())
/// ```
#[derive(Debug, Clone)]
#[must_use = "a chain does nothing until build() is called"]
pub struct ImgproxyUrlChain<'a> {
    b: &'a ImgproxyUrlBuilder,
    s: ChainState,
}

#[allow(clippy::doc_markdown)] // generated from the spec's prose
impl ImgproxyUrlChain<'_> {
    processing_methods!();
}

impl ImgproxyUrlChain<'_> {
    chain_core!();

    /// Starts the next chained pipeline (`/-/`).
    pub fn pipeline(mut self) -> Self {
        self.s.pipelines.push(Vec::new());
        self
    }

    /// Overrides the source encoding.
    pub fn encoding(mut self, encoding: SourceEncoding) -> Self {
        self.s.encoding = Some(encoding);
        self
    }

    /// Sets the output extension (`@webp` / `.webp`).
    pub fn extension(mut self, ext: impl AsRef<str>) -> Self {
        self.s.ext = Some(ext.as_ref().to_owned());
        self
    }

    /// Opens a password-protected PDF: the plain-text password is encrypted into `pdp` (needs both keys).
    pub fn with_pdf_password(mut self, password: impl Into<String>) -> Self {
        self.s.pdf_password = Some(password.into());
        self
    }

    /// Builds the URL.
    ///
    /// # Errors
    ///
    /// As [`ImgproxyUrlBuilder::url`].
    pub fn build(&self) -> Result<String> {
        let s = &self.s;
        self.b.build(
            Endpoint::Process,
            &s.pipelines,
            &s.source,
            s.encoding,
            s.ext.as_deref(),
            s.pdf_password.as_deref(),
        )
    }
}

/// A fluent `/info` URL ([`ImgproxyUrlBuilder::info`]).
#[derive(Debug, Clone)]
#[must_use = "a chain does nothing until build() is called"]
pub struct ImgproxyInfoUrlChain<'a> {
    b: &'a ImgproxyUrlBuilder,
    s: ChainState,
}

#[allow(clippy::doc_markdown)] // generated from the spec's prose
impl ImgproxyInfoUrlChain<'_> {
    info_methods!();
}

impl ImgproxyInfoUrlChain<'_> {
    chain_core!();

    /// Overrides the source encoding.
    pub fn encoding(mut self, encoding: SourceEncoding) -> Self {
        self.s.encoding = Some(encoding);
        self
    }

    /// Opens a password-protected PDF (needs both keys).
    pub fn with_pdf_password(mut self, password: impl Into<String>) -> Self {
        self.s.pdf_password = Some(password.into());
        self
    }

    /// Builds the URL.
    ///
    /// # Errors
    ///
    /// As [`ImgproxyUrlBuilder::info_url`].
    pub fn build(&self) -> Result<String> {
        let s = &self.s;
        self.b.build(Endpoint::Info, &s.pipelines, &s.source, s.encoding, None, s.pdf_password.as_deref())
    }
}

/// A fluent `/video` URL ([`ImgproxyUrlBuilder::video`]): video transformations, audio and waveforms.
#[derive(Debug, Clone)]
#[must_use = "a chain does nothing until build() is called"]
pub struct ImgproxyVideoUrlChain<'a> {
    b: &'a ImgproxyUrlBuilder,
    s: ChainState,
}

#[allow(clippy::doc_markdown)] // generated from the spec's prose
impl ImgproxyVideoUrlChain<'_> {
    video_methods!();
}

impl ImgproxyVideoUrlChain<'_> {
    chain_core!();

    /// Overrides the source encoding.
    pub fn encoding(mut self, encoding: SourceEncoding) -> Self {
        self.s.encoding = Some(encoding);
        self
    }

    /// Sets the output container (`webm`, `mp4`, `mp3`, `png`, …).
    pub fn extension(mut self, ext: impl AsRef<str>) -> Self {
        self.s.ext = Some(ext.as_ref().to_owned());
        self
    }

    /// Builds the URL.
    ///
    /// # Errors
    ///
    /// As [`ImgproxyUrlBuilder::video_url`].
    pub fn build(&self) -> Result<String> {
        let s = &self.s;
        self.b.build(Endpoint::Video, &s.pipelines, &s.source, s.encoding, s.ext.as_deref(), None)
    }
}
