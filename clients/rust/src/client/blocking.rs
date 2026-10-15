//! The blocking HTTP client (feature `blocking`, on `reqwest::blocking`).
//!
//! Do not call it from inside an async runtime; use [`Client`](crate::Client) there.

use crate::args::Pipelines;
use crate::client::{
    ClientBuilder, ClientError, Core, HttpError, MediaOptions, Method, Readiness, StreamManifest, decode, readiness,
    stream_accept, tls_config,
};
use crate::generated::responses::InfoResponse;
use crate::imgproxy::{ImgproxyUrlBuilder, InfoUrlParams, StreamParams, UrlParams, VideoUrlParams};

/// A blocking client of an xtandard-image server, with the same methods as the async
/// [`Client`](crate::Client).
///
/// ```no_run
/// use xtandard_image::blocking::Client;
/// use xtandard_image::{MediaOptions, Options, UrlParams};
///
/// let xi = Client::builder("http://localhost:8080").build()?;
/// xi.healthz()?;
/// let res = xi.image("s3://bucket/cat.jpg", Options::new().set("w", 300), UrlParams::new(), &MediaOptions::new().accept("image/avif,image/webp"))?;
/// let bytes = res.bytes().map_err(|e| e.to_string());
/// # Ok::<(), xtandard_image::ClientError>(())
/// ```
#[derive(Debug, Clone)]
pub struct Client {
    core: Core,
    http: reqwest::blocking::Client,
}

impl ClientBuilder<Client> {
    /// Creates the client and its reqwest client (rustls with the ring provider, the platform's roots).
    ///
    /// # Errors
    ///
    /// [`ClientError::Setup`] when TLS or a header is invalid.
    pub fn build(self) -> Result<Client, ClientError> {
        let core = self.core()?;
        let http = reqwest::blocking::Client::builder()
            .tls_backend_preconfigured(tls_config()?)
            .timeout(core.timeout)
            .build()
            .map_err(|e| ClientError::Setup(e.to_string()))?;
        Ok(Client { core, http })
    }

    /// Creates the client on a reqwest blocking client of your own.
    ///
    /// # Errors
    ///
    /// [`ClientError::Setup`] when a header is invalid.
    pub fn build_with(self, http: reqwest::blocking::Client) -> Result<Client, ClientError> {
        Ok(Client { core: self.core()?, http })
    }
}

impl Client {
    /// A builder for a server at `base_url` (origin plus the imgproxy mount prefix, if any).
    pub fn builder(base_url: impl Into<String>) -> ClientBuilder<Self> {
        ClientBuilder::new(base_url)
    }

    /// The URL builder of this client (base URL, keys and URL settings).
    pub fn urls(&self) -> &ImgproxyUrlBuilder {
        &self.core.urls
    }

    /// The reqwest client.
    pub fn http(&self) -> &reqwest::blocking::Client {
        &self.http
    }

    fn send(
        &self,
        method: Method,
        url: &str,
        headers: reqwest::header::HeaderMap,
    ) -> Result<reqwest::blocking::Response, ClientError> {
        self.http.request(method.reqwest(), url).headers(headers).send().map_err(|source| ClientError::Transport {
            method: method.as_str(),
            url: url.to_owned(),
            source,
        })
    }

    fn checked(res: reqwest::blocking::Response, method: Method) -> Result<reqwest::blocking::Response, ClientError> {
        if res.status().is_success() {
            return Ok(res);
        }
        let status = res.status();
        let url = res.url().to_string();
        let body = Self::body(res, &url, method)?;
        Err(HttpError::from_body(status.as_u16(), status.canonical_reason(), url, &body).into())
    }

    fn body(res: reqwest::blocking::Response, url: &str, method: Method) -> Result<Vec<u8>, ClientError> {
        res.bytes().map(|b| b.to_vec()).map_err(|source| ClientError::Transport {
            method: method.as_str(),
            url: url.to_owned(),
            source,
        })
    }

    fn json<T: serde::de::DeserializeOwned>(&self, url: &str) -> Result<T, ClientError> {
        let res = self.send(Method::Get, url, self.core.headers_for("application/json", None, &[])?)?;
        let body = Self::body(Self::checked(res, Method::Get)?, url, Method::Get)?;
        decode(url, &body)
    }

    /// `GET /healthz`: succeeds while the process can serve HTTP.
    ///
    /// # Errors
    ///
    /// [`ClientError::Http`] or [`ClientError::Transport`].
    pub fn healthz(&self) -> Result<(), ClientError> {
        let url = self.core.endpoint("/healthz");
        Self::checked(self.send(Method::Get, &url, self.core.headers_for("text/plain", None, &[])?)?, Method::Get)?;
        Ok(())
    }

    /// `GET /readyz`: the readiness state; 503 (starting, draining) is not an error.
    ///
    /// # Errors
    ///
    /// [`ClientError::Http`] for another status, [`ClientError::Decode`] for an unknown state.
    pub fn readyz(&self) -> Result<Readiness, ClientError> {
        let url = self.core.endpoint("/readyz");
        let res = self.send(Method::Get, &url, self.core.headers_for("text/plain", None, &[])?)?;
        let status = res.status().as_u16();
        if status != 200 && status != 503 {
            return Err(Self::checked(res, Method::Get).err().unwrap_or_else(|| ClientError::Decode {
                url: url.clone(),
                message: format!("unexpected status {status}"),
            }));
        }
        let body = Self::body(res, &url, Method::Get)?;
        readiness(status, &url, &String::from_utf8_lossy(&body))
    }

    /// `GET /info` for a source, with the URL built (and signed) from the arguments.
    ///
    /// # Errors
    ///
    /// [`ClientError::Build`], [`ClientError::Http`], [`ClientError::Transport`] or [`ClientError::Decode`].
    pub fn info(
        &self,
        source: &str,
        options: impl Into<Pipelines>,
        params: InfoUrlParams,
    ) -> Result<InfoResponse, ClientError> {
        let url = self.core.urls.info_url(source, options, params)?;
        self.json(&url)
    }

    /// `GET` a prebuilt `/info/…` path or URL.
    ///
    /// # Errors
    ///
    /// As [`info`](Self::info).
    pub fn info_at(&self, url: &str) -> Result<InfoResponse, ClientError> {
        self.json(&self.core.abs(url))
    }

    /// `GET` (or `HEAD`) any URL or server path: an image or video URL, a query URL, a stream part.
    ///
    /// The body is not read: the response implements [`std::io::Read`].
    ///
    /// # Errors
    ///
    /// [`ClientError::Http`] (the body is read for the error) or [`ClientError::Transport`].
    pub fn media(&self, url: &str, opts: &MediaOptions) -> Result<reqwest::blocking::Response, ClientError> {
        let url = self.core.abs(url);
        Self::checked(self.send(opts.method, &url, self.core.media_headers(opts)?)?, opts.method)
    }

    /// `GET` (or `HEAD`) a processed image; the `Accept` header drives format negotiation.
    ///
    /// # Errors
    ///
    /// As [`media`](Self::media), and [`ClientError::Build`].
    pub fn image(
        &self,
        source: &str,
        options: impl Into<Pipelines>,
        params: UrlParams,
        opts: &MediaOptions,
    ) -> Result<reqwest::blocking::Response, ClientError> {
        let url = self.core.urls.url(source, options, params)?;
        self.media(&url, opts)
    }

    /// `GET` (or `HEAD`) a `/video` result; a range asks for part of it (206).
    ///
    /// # Errors
    ///
    /// As [`media`](Self::media), and [`ClientError::Build`].
    pub fn video(
        &self,
        source: &str,
        options: impl Into<Pipelines>,
        params: VideoUrlParams,
        opts: &MediaOptions,
    ) -> Result<reqwest::blocking::Response, ClientError> {
        let url = self.core.urls.video_url(source, options, params)?;
        self.media(&url, opts)
    }

    /// `GET` an HLS or DASH manifest.
    ///
    /// # Errors
    ///
    /// As [`media`](Self::media), and [`ClientError::Build`].
    pub fn stream(&self, source: &str, stream: &StreamParams) -> Result<StreamManifest, ClientError> {
        let url = self.core.urls.stream_url(source, stream)?;
        let ctype = stream.content_type();
        let res = Self::checked(
            self.send(Method::Get, &url, self.core.headers_for(&stream_accept(ctype), None, &[])?)?,
            Method::Get,
        )?;
        let content_type =
            res.headers().get(reqwest::header::CONTENT_TYPE).and_then(|v| v.to_str().ok()).unwrap_or(ctype).to_owned();
        let body = Self::body(res, &url, Method::Get)?;
        Ok(StreamManifest { url, content_type, body: String::from_utf8_lossy(&body).into_owned() })
    }

    /// `GET` the playground's `api/meta`.
    ///
    /// # Errors
    ///
    /// [`ClientError::Http`], [`ClientError::Transport`] or [`ClientError::Decode`].
    pub fn playground_meta(&self) -> Result<serde_json::Value, ClientError> {
        self.json(&self.core.playground_meta)
    }
}
