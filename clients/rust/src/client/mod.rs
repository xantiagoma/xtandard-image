//! What the async ([`Client`](crate::Client)) and blocking ([`blocking::Client`](crate::blocking::Client))
//! HTTP clients share: the builder, request options, results and errors.

use std::marker::PhantomData;
use std::sync::Arc;
use std::time::Duration;

use reqwest::header::{ACCEPT, CONTENT_TYPE, HeaderMap, HeaderName, HeaderValue, RANGE};

use crate::encryption::EncryptionKey;
use crate::error::Error;
use crate::generated::constants::{QUERY_MOUNT, SourceEncoding};
use crate::generated::responses::ErrorResponse;
use crate::imgproxy::ImgproxyUrlBuilder;
use crate::options::OptionNames;
use crate::query::query_method_request;
use crate::range::{ByteRange, range_header};
use crate::signing::SigningKey;

#[cfg(feature = "async")]
pub(crate) mod async_impl;
#[cfg(feature = "blocking")]
pub mod blocking;

/// A request that did not succeed.
#[derive(Debug, thiserror::Error)]
#[non_exhaustive]
pub enum ClientError {
    /// The URL could not be built.
    #[error(transparent)]
    Build(#[from] Error),
    /// The server answered with a non-2xx status.
    #[error(transparent)]
    Http(Box<HttpError>),
    /// The request got no response (connection, TLS, timeout), or the body could not be read.
    #[error("{method} {url}: {source}")]
    Transport {
        /// The request method.
        method: &'static str,
        /// The request URL.
        url: String,
        /// The reqwest error.
        #[source]
        source: reqwest::Error,
    },
    /// A 2xx body that is not what the endpoint returns (JSON that does not parse, an unknown readiness state).
    #[error("{url}: {message}")]
    Decode {
        /// The request URL.
        url: String,
        /// What was wrong.
        message: String,
    },
    /// The client could not be set up (TLS, an invalid default header).
    #[error("client setup: {0}")]
    Setup(String),
}

impl From<HttpError> for ClientError {
    fn from(e: HttpError) -> Self {
        Self::Http(Box::new(e))
    }
}

impl ClientError {
    /// The HTTP error, when the server answered with a non-2xx status.
    pub fn as_http(&self) -> Option<&HttpError> {
        match self {
            Self::Http(e) => Some(e),
            _ => None,
        }
    }
}

/// A non-2xx response.
///
/// `kind`, `message` and `request_id` come from the server's JSON error
/// envelope; a body that is not the envelope (a proxy's error page, say)
/// yields kind `"unknown"` and the body text.
#[derive(Debug, Clone, PartialEq, thiserror::Error)]
#[error("{status} {kind}: {message}")]
#[non_exhaustive]
pub struct HttpError {
    /// The HTTP status.
    pub status: u16,
    /// The error kind (`invalid_url`, `unauthorized`, `not_found`, …) or `"unknown"`.
    pub kind: String,
    /// The client-safe message.
    pub message: String,
    /// The request ID from the envelope, also in the server logs.
    pub request_id: Option<String>,
    /// The request URL.
    pub url: String,
    /// The parsed envelope, when the body was one.
    pub body: Option<ErrorResponse>,
}

/// What an [`HttpError`] status means, as the Python client's exception classes have it.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
#[non_exhaustive]
pub enum HttpErrorClass {
    /// 400: the URL is invalid (kind `invalid_url`).
    BadRequest,
    /// 403: a bad or missing signature (`unauthorized`) or a refused source or option (`forbidden`).
    Forbidden,
    /// 404: the source does not exist (`not_found`) or the URL expired (`expired`).
    NotFound,
    /// 406: no acceptable output format.
    NotAcceptable,
    /// 422: the source is too large, unsupported, corrupt or over a processing limit.
    Unprocessable,
    /// 429: the server is overloaded; retry later.
    TooManyRequests,
    /// 502 and 504: an upstream error or timeout.
    Gateway,
    /// 503: the server is unavailable or draining.
    ServiceUnavailable,
    /// Another 5xx.
    Server,
    /// Another status.
    Other,
}

impl HttpError {
    /// The class of the status.
    pub fn class(&self) -> HttpErrorClass {
        match self.status {
            400 => HttpErrorClass::BadRequest,
            403 => HttpErrorClass::Forbidden,
            404 => HttpErrorClass::NotFound,
            406 => HttpErrorClass::NotAcceptable,
            422 => HttpErrorClass::Unprocessable,
            429 => HttpErrorClass::TooManyRequests,
            502 | 504 => HttpErrorClass::Gateway,
            503 => HttpErrorClass::ServiceUnavailable,
            s if s >= 500 => HttpErrorClass::Server,
            _ => HttpErrorClass::Other,
        }
    }

    /// Whether the status is 5xx.
    pub fn is_server_error(&self) -> bool {
        self.status >= 500
    }

    pub(crate) fn from_body(status: u16, reason: Option<&str>, url: String, body: &[u8]) -> Self {
        if let Ok(env) = serde_json::from_slice::<ErrorResponse>(body) {
            return Self {
                status,
                kind: env.error.kind.clone(),
                message: env.error.message.clone(),
                request_id: env.error.request_id.clone(),
                url,
                body: Some(env),
            };
        }
        let text = String::from_utf8_lossy(body).trim().to_owned();
        let message = if !text.is_empty() {
            text
        } else if let Some(r) = reason {
            r.to_owned()
        } else {
            format!("HTTP {status}")
        };
        Self { status, kind: "unknown".to_owned(), message, request_id: None, url, body: None }
    }
}

/// The readiness states of `/readyz`.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
#[non_exhaustive]
pub enum ReadyState {
    /// Serving.
    Ready,
    /// Starting up (503).
    Starting,
    /// Shutting down (503).
    Draining,
}

/// The `/readyz` answer: `ready` is true only for 200 `ready`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[non_exhaustive]
pub struct Readiness {
    /// Whether the server takes traffic.
    pub ready: bool,
    /// The state the server reported.
    pub state: ReadyState,
}

/// A fetched HLS playlist or DASH MPD.
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct StreamManifest {
    /// The (signed) manifest URL; hand it to a player.
    pub url: String,
    /// `application/vnd.apple.mpegurl` or `application/dash+xml`.
    pub content_type: String,
    /// The manifest; its child URLs (`/_stream/<ticket>/…`) carry a ticket and need no signing.
    pub body: String,
}

/// The method of a media request.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Default)]
pub enum Method {
    /// `GET` (default).
    #[default]
    Get,
    /// `HEAD`: the headers only.
    Head,
    /// `QUERY` (RFC 10008): a query dialect URL's query goes out as the request
    /// content ([`query_method_request`](crate::query_method_request)). The
    /// server needs `XI_SERVER_QUERY_METHOD` (on by default); many CDNs and
    /// proxies neither cache nor forward QUERY.
    Query,
}

impl Method {
    pub(crate) fn as_str(self) -> &'static str {
        match self {
            Self::Get => "GET",
            Self::Head => "HEAD",
            Self::Query => "QUERY",
        }
    }

    pub(crate) fn reqwest(self) -> reqwest::Method {
        match self {
            Self::Get => reqwest::Method::GET,
            Self::Head => reqwest::Method::HEAD,
            // "QUERY" is a valid token, so this never falls back.
            Self::Query => reqwest::Method::from_bytes(b"QUERY").unwrap_or(reqwest::Method::GET),
        }
    }
}

/// How a media request (image, video, stream part) is sent.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct MediaOptions {
    method: Option<Method>,
    range: Option<ByteRange>,
    accept: Option<String>,
    headers: Vec<(String, String)>,
}

impl MediaOptions {
    /// `GET` (or `QUERY` under the client's [`query_method`](ClientBuilder::query_method)),
    /// the whole body, `Accept: */*`.
    pub fn new() -> Self {
        Self::default()
    }

    /// `HEAD` instead of `GET`.
    pub fn head(mut self) -> Self {
        self.method = Some(Method::Head);
        self
    }

    /// `QUERY` instead of `GET`: the URL must be a query dialect URL with a query.
    pub fn query(mut self) -> Self {
        self.method = Some(Method::Query);
        self
    }

    /// The method; an explicit [`Method::Get`] is never turned into `QUERY`.
    pub fn method(mut self, method: Method) -> Self {
        self.method = Some(method);
        self
    }

    /// Asks for part of the body (206): `ByteRange::new(0, 1023)`, `0..1024`, `ByteRange::suffix(500)`.
    pub fn range(mut self, range: impl Into<ByteRange>) -> Self {
        self.range = Some(range.into());
        self
    }

    /// The `Accept` header, which drives format negotiation (`image/avif,image/webp`).
    pub fn accept(mut self, accept: impl Into<String>) -> Self {
        self.accept = Some(accept.into());
        self
    }

    /// An extra request header.
    pub fn header(mut self, name: impl Into<String>, value: impl Into<String>) -> Self {
        self.headers.push((name.into(), value.into()));
        self
    }
}

/// Builds an HTTP client: [`Client::builder`](crate::Client::builder) or
/// [`blocking::Client::builder`](crate::blocking::Client::builder).
#[derive(Debug, Clone)]
pub struct ClientBuilder<C> {
    base_url: String,
    urls: ImgproxyUrlBuilder,
    headers: Vec<(String, String)>,
    timeout: Option<Duration>,
    playground_base_url: Option<String>,
    playground_path: String,
    query_method: bool,
    query_mount: String,
    client: PhantomData<fn() -> C>,
}

impl<C> ClientBuilder<C> {
    pub(crate) fn new(base_url: impl Into<String>) -> Self {
        Self {
            base_url: base_url.into().trim_end_matches('/').to_owned(),
            urls: ImgproxyUrlBuilder::new(),
            headers: Vec::new(),
            timeout: Some(Duration::from_secs(60)),
            playground_base_url: None,
            playground_path: "/_playground/".to_owned(),
            query_method: false,
            query_mount: QUERY_MOUNT.to_owned(),
            client: PhantomData,
        }
    }

    /// Signs the URLs the client builds.
    pub fn signing(mut self, key: SigningKey) -> Self {
        self.urls = self.urls.signing(key);
        self
    }

    /// The key of encrypted sources and PDF passwords.
    pub fn encryption(mut self, key: EncryptionKey) -> Self {
        self.urls = self.urls.encryption(key);
        self
    }

    /// The signature segment of unsigned URLs.
    pub fn unsigned_signature(mut self, placeholder: impl Into<String>) -> Self {
        self.urls = self.urls.unsigned_signature(placeholder);
        self
    }

    /// The default source encoding.
    pub fn source_encoding(mut self, encoding: SourceEncoding) -> Self {
        self.urls = self.urls.source_encoding(encoding);
        self
    }

    /// Splits base64 and encrypted sources into chunks (0: no split).
    pub fn split_source(mut self, chunk: usize) -> Self {
        self.urls = self.urls.split_source(chunk);
        self
    }

    /// The argument separator.
    pub fn separator(mut self, separator: char) -> Self {
        self.urls = self.urls.separator(separator);
        self
    }

    /// How option names are written.
    pub fn option_names(mut self, style: OptionNames) -> Self {
        self.urls = self.urls.option_names(style);
        self
    }

    /// A header sent with every request.
    pub fn header(mut self, name: impl Into<String>, value: impl Into<String>) -> Self {
        self.headers.push((name.into(), value.into()));
        self
    }

    /// The request timeout of the HTTP client this builder creates (default 60 s; `None`: none).
    pub fn timeout(mut self, timeout: Option<Duration>) -> Self {
        self.timeout = timeout;
        self
    }

    /// The origin of the admin listener for `playground_meta()`; default the base URL.
    pub fn playground_base_url(mut self, url: impl Into<String>) -> Self {
        self.playground_base_url = Some(url.into());
        self
    }

    /// `XI_PLAYGROUND_PATH`. Default `/_playground/`.
    pub fn playground_path(mut self, path: impl Into<String>) -> Self {
        self.playground_path = path.into();
        self
    }

    /// Sends query dialect URLs (those under [`query_mount`](Self::query_mount))
    /// passed to `media()` with the HTTP QUERY method (RFC 10008) when the call
    /// names no method: the query moves into the request content.
    ///
    /// Off by default: the server needs `XI_SERVER_QUERY_METHOD` (on by
    /// default), and many CDNs and proxies neither cache nor forward QUERY.
    /// [`MediaOptions::query`] does the same for one call.
    pub fn query_method(mut self, on: bool) -> Self {
        self.query_method = on;
        self
    }

    /// The query dialect's mount (`XI_DIALECTS_QUERY_PREFIX`), for [`query_method`](Self::query_method). Default `/q`.
    pub fn query_mount(mut self, mount: impl Into<String>) -> Self {
        self.query_mount = mount.into();
        self
    }

    pub(crate) fn core(self) -> Result<Core, ClientError> {
        let mut headers = HeaderMap::new();
        for (k, v) in &self.headers {
            let name = HeaderName::try_from(k.as_str()).map_err(|e| ClientError::Setup(format!("header {k}: {e}")))?;
            let value =
                HeaderValue::try_from(v.as_str()).map_err(|e| ClientError::Setup(format!("header {k}: {e}")))?;
            headers.append(name, value);
        }
        let origin = self.playground_base_url.as_deref().unwrap_or(&self.base_url).trim_end_matches('/').to_owned();
        let playground_meta = format!("{origin}/{}/api/meta", self.playground_path.trim_matches('/'));
        Ok(Core {
            urls: self.urls.base_url(self.base_url.clone()),
            base_url: self.base_url,
            headers,
            playground_meta,
            timeout: self.timeout,
            query_method: self.query_method,
            query_mount: self.query_mount.trim_end_matches('/').to_owned(),
        })
    }
}

/// What the async and blocking clients share: URLs, headers, response checks.
#[derive(Debug, Clone)]
pub(crate) struct Core {
    pub(crate) base_url: String,
    pub(crate) urls: ImgproxyUrlBuilder,
    pub(crate) headers: HeaderMap,
    pub(crate) playground_meta: String,
    pub(crate) timeout: Option<Duration>,
    pub(crate) query_method: bool,
    pub(crate) query_mount: String,
}

/// A media request as sent: method, URL, headers and, for QUERY, the content.
pub(crate) struct MediaRequest {
    pub(crate) method: Method,
    pub(crate) url: String,
    pub(crate) headers: HeaderMap,
    pub(crate) body: Option<String>,
}

impl Core {
    /// An absolute URL for a server path; absolute URLs are kept.
    pub(crate) fn abs(&self, url: &str) -> String {
        if has_scheme(url) {
            return url.to_owned();
        }
        let sep = if url.starts_with('/') { "" } else { "/" };
        format!("{}{sep}{url}", self.base_url)
    }

    pub(crate) fn endpoint(&self, path: &str) -> String {
        format!("{}{path}", self.base_url)
    }

    pub(crate) fn headers_for(
        &self,
        accept: &str,
        range: Option<&ByteRange>,
        extra: &[(String, String)],
    ) -> Result<HeaderMap, ClientError> {
        let mut h = self.headers.clone();
        for (k, v) in extra {
            let name = HeaderName::try_from(k.as_str()).map_err(|e| ClientError::Setup(format!("header {k}: {e}")))?;
            let value =
                HeaderValue::try_from(v.as_str()).map_err(|e| ClientError::Setup(format!("header {k}: {e}")))?;
            h.insert(name, value);
        }
        h.insert(ACCEPT, HeaderValue::try_from(accept).map_err(|e| ClientError::Setup(format!("accept: {e}")))?);
        if let Some(r) = range {
            let v = range_header(r)?;
            h.insert(RANGE, HeaderValue::try_from(v).map_err(|e| ClientError::Setup(format!("range: {e}")))?);
        }
        Ok(h)
    }

    /// The request `media()` sends for a URL or server path: QUERY for
    /// [`Method::Query`], or with `query_method` for a URL under the query mount
    /// when no method was given; otherwise GET (or the given method) unchanged.
    pub(crate) fn media_request(&self, url: &str, opts: &MediaOptions) -> Result<MediaRequest, ClientError> {
        let url = self.abs(url);
        let mut headers =
            self.headers_for(opts.accept.as_deref().unwrap_or("*/*"), opts.range.as_ref(), &opts.headers)?;
        let method = opts.method.unwrap_or(if self.query_method && self.under_query_mount(&url) {
            Method::Query
        } else {
            Method::Get
        });
        if method != Method::Query {
            return Ok(MediaRequest { method, url, headers, body: None });
        }
        let q = query_method_request(&url)?;
        headers.insert(CONTENT_TYPE, HeaderValue::from_static(q.content_type));
        Ok(MediaRequest { method, url: q.url, headers, body: Some(q.body) })
    }

    /// Whether an absolute URL's path is the query mount or under it.
    fn under_query_mount(&self, url: &str) -> bool {
        let Ok(u) = reqwest::Url::parse(url) else { return false };
        let path = u.path();
        let mount = self.query_mount.as_str();
        path == mount || path.strip_prefix(mount).is_some_and(|rest| rest.starts_with('/'))
    }
}

fn has_scheme(url: &str) -> bool {
    let Some((scheme, _)) = url.split_once(':') else { return false };
    let mut chars = scheme.chars();
    chars.next().is_some_and(|c| c.is_ascii_alphabetic())
        && chars.all(|c| c.is_ascii_alphanumeric() || matches!(c, '+' | '.' | '-'))
}

/// The readiness of a `/readyz` answer.
pub(crate) fn readiness(status: u16, url: &str, text: &str) -> Result<Readiness, ClientError> {
    let state = match text.trim() {
        "ready" => ReadyState::Ready,
        "starting" => ReadyState::Starting,
        "draining" => ReadyState::Draining,
        other => {
            return Err(ClientError::Decode {
                url: url.to_owned(),
                message: format!("unexpected readiness state {other:?}"),
            });
        }
    };
    Ok(Readiness { ready: status == 200 && state == ReadyState::Ready, state })
}

pub(crate) fn decode<T: serde::de::DeserializeOwned>(url: &str, body: &[u8]) -> Result<T, ClientError> {
    serde_json::from_slice(body).map_err(|e| ClientError::Decode { url: url.to_owned(), message: e.to_string() })
}

pub(crate) fn stream_accept(content_type: &str) -> String {
    format!("{content_type}, */*;q=0.5")
}

/// rustls with the ring provider and the platform verifier, ALPN h2 and HTTP/1.1.
pub(crate) fn tls_config() -> Result<rustls::ClientConfig, ClientError> {
    let provider = Arc::new(rustls::crypto::ring::default_provider());
    let verifier = rustls_platform_verifier::Verifier::new(provider.clone())
        .map_err(|e| ClientError::Setup(format!("TLS verifier: {e}")))?;
    let mut cfg = rustls::ClientConfig::builder_with_provider(provider)
        .with_safe_default_protocol_versions()
        .map_err(|e| ClientError::Setup(format!("TLS: {e}")))?
        .dangerous()
        .with_custom_certificate_verifier(Arc::new(verifier))
        .with_no_client_auth();
    cfg.alpn_protocols = vec![b"h2".to_vec(), b"http/1.1".to_vec()];
    Ok(cfg)
}
