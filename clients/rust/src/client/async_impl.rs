//! The async HTTP client (feature `async`, on reqwest and tokio).

use crate::args::Pipelines;
use crate::client::{
    ClientBuilder, ClientError, Core, HttpError, MediaOptions, Method, Readiness, StreamManifest, decode, readiness,
    stream_accept, tls_config,
};
use crate::generated::responses::InfoResponse;
use crate::imgproxy::{ImgproxyUrlBuilder, InfoUrlParams, StreamParams, UrlParams, VideoUrlParams};

/// An async client of an xtandard-image server.
///
/// It covers the JSON and health endpoints (`/info`, `/healthz`, `/readyz`,
/// the playground's `api/meta`) and media fetches (images, `/video` results
/// with Range and HEAD, HLS and DASH manifests). URLs are built (and signed)
/// with the client's [`ImgproxyUrlBuilder`]. A non-2xx status is
/// [`ClientError::Http`]; a connection failure is [`ClientError::Transport`].
///
/// ```no_run
/// use xtandard_image::{Client, InfoUrlParams, MediaOptions, Options, SigningKey, VideoUrlParams};
///
/// # async fn run() -> Result<(), xtandard_image::ClientError> {
/// let xi = Client::builder("http://localhost:8080").signing(SigningKey::new("736563726574", "68656C6C6F")?).build()?;
/// xi.healthz().await?;
/// let info = xi.info("s3://bucket/cat.jpg", Options::new().set("size", true), InfoUrlParams::new()).await?;
/// println!("{:?} {:?}", info.width, info.format);
/// let head = xi
///     .video("s3://bucket/talk.mov", Options::new().set("vd", 5), VideoUrlParams::new().extension("webm"), &MediaOptions::new().head())
///     .await?;
/// println!("{:?}", head.headers().get("content-length"));
/// # Ok(())
/// # }
/// ```
#[derive(Debug, Clone)]
pub struct Client {
    core: Core,
    http: reqwest::Client,
}

impl ClientBuilder<Client> {
    /// Creates the client and its reqwest client (rustls with the ring provider, the platform's roots).
    ///
    /// # Errors
    ///
    /// [`ClientError::Setup`] when TLS or a header is invalid.
    pub fn build(self) -> Result<Client, ClientError> {
        let core = self.core()?;
        let mut b = reqwest::Client::builder().tls_backend_preconfigured(tls_config()?);
        if let Some(t) = core.timeout {
            b = b.timeout(t);
        }
        let http = b.build().map_err(|e| ClientError::Setup(e.to_string()))?;
        Ok(Client { core, http })
    }

    /// Creates the client on a reqwest client of your own (its TLS, proxy and timeout settings).
    ///
    /// # Errors
    ///
    /// [`ClientError::Setup`] when a header is invalid.
    pub fn build_with(self, http: reqwest::Client) -> Result<Client, ClientError> {
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
    pub fn http(&self) -> &reqwest::Client {
        &self.http
    }

    async fn send(
        &self,
        method: Method,
        url: &str,
        headers: reqwest::header::HeaderMap,
    ) -> Result<reqwest::Response, ClientError> {
        self.http
            .request(method.reqwest(), url)
            .headers(headers)
            .send()
            .await
            .map_err(|source| ClientError::Transport { method: method.as_str(), url: url.to_owned(), source })
    }

    async fn checked(res: reqwest::Response, method: Method) -> Result<reqwest::Response, ClientError> {
        if res.status().is_success() {
            return Ok(res);
        }
        let status = res.status();
        let url = res.url().to_string();
        let body = res.bytes().await.map_err(|source| ClientError::Transport {
            method: method.as_str(),
            url: url.clone(),
            source,
        })?;
        Err(HttpError::from_body(status.as_u16(), status.canonical_reason(), url, &body).into())
    }

    async fn body(res: reqwest::Response, url: &str) -> Result<Vec<u8>, ClientError> {
        res.bytes().await.map(|b| b.to_vec()).map_err(|source| ClientError::Transport {
            method: "GET",
            url: url.to_owned(),
            source,
        })
    }

    async fn json<T: serde::de::DeserializeOwned>(&self, url: &str) -> Result<T, ClientError> {
        let res = self.send(Method::Get, url, self.core.headers_for("application/json", None, &[])?).await?;
        let body = Self::body(Self::checked(res, Method::Get).await?, url).await?;
        decode(url, &body)
    }

    /// `GET /healthz`: succeeds while the process can serve HTTP.
    ///
    /// # Errors
    ///
    /// [`ClientError::Http`] or [`ClientError::Transport`].
    pub async fn healthz(&self) -> Result<(), ClientError> {
        let url = self.core.endpoint("/healthz");
        let res = self.send(Method::Get, &url, self.core.headers_for("text/plain", None, &[])?).await?;
        Self::checked(res, Method::Get).await?;
        Ok(())
    }

    /// `GET /readyz`: the readiness state; 503 (starting, draining) is not an error.
    ///
    /// # Errors
    ///
    /// [`ClientError::Http`] for another status, [`ClientError::Decode`] for an unknown state.
    pub async fn readyz(&self) -> Result<Readiness, ClientError> {
        let url = self.core.endpoint("/readyz");
        let res = self.send(Method::Get, &url, self.core.headers_for("text/plain", None, &[])?).await?;
        let status = res.status().as_u16();
        if status != 200 && status != 503 {
            return Err(Self::checked(res, Method::Get).await.err().unwrap_or_else(|| ClientError::Decode {
                url: url.clone(),
                message: format!("unexpected status {status}"),
            }));
        }
        let body = Self::body(res, &url).await?;
        readiness(status, &url, &String::from_utf8_lossy(&body))
    }

    /// `GET /info` for a source, with the URL built (and signed) from the arguments.
    ///
    /// # Errors
    ///
    /// [`ClientError::Build`], [`ClientError::Http`], [`ClientError::Transport`] or [`ClientError::Decode`].
    pub async fn info(
        &self,
        source: &str,
        options: impl Into<Pipelines>,
        params: InfoUrlParams,
    ) -> Result<InfoResponse, ClientError> {
        let url = self.core.urls.info_url(source, options, params)?;
        self.json(&url).await
    }

    /// `GET` a prebuilt `/info/…` path or URL.
    ///
    /// # Errors
    ///
    /// As [`info`](Self::info).
    pub async fn info_at(&self, url: &str) -> Result<InfoResponse, ClientError> {
        self.json(&self.core.abs(url)).await
    }

    /// `GET` (or `HEAD`) any URL or server path: an image or video URL, a query URL, a stream part.
    ///
    /// The body is not read: use [`reqwest::Response::bytes`] or
    /// [`reqwest::Response::chunk`] to stream it.
    ///
    /// # Errors
    ///
    /// [`ClientError::Http`] (the body is read for the error) or [`ClientError::Transport`].
    pub async fn media(&self, url: &str, opts: &MediaOptions) -> Result<reqwest::Response, ClientError> {
        let url = self.core.abs(url);
        let res = self.send(opts.method, &url, self.core.media_headers(opts)?).await?;
        Self::checked(res, opts.method).await
    }

    /// `GET` (or `HEAD`) a processed image; the `Accept` header drives format negotiation.
    ///
    /// # Errors
    ///
    /// As [`media`](Self::media), and [`ClientError::Build`].
    pub async fn image(
        &self,
        source: &str,
        options: impl Into<Pipelines>,
        params: UrlParams,
        opts: &MediaOptions,
    ) -> Result<reqwest::Response, ClientError> {
        let url = self.core.urls.url(source, options, params)?;
        self.media(&url, opts).await
    }

    /// `GET` (or `HEAD`) a `/video` result: video, audio or a waveform; a range asks for part of it (206).
    ///
    /// # Errors
    ///
    /// As [`media`](Self::media), and [`ClientError::Build`].
    pub async fn video(
        &self,
        source: &str,
        options: impl Into<Pipelines>,
        params: VideoUrlParams,
        opts: &MediaOptions,
    ) -> Result<reqwest::Response, ClientError> {
        let url = self.core.urls.video_url(source, options, params)?;
        self.media(&url, opts).await
    }

    /// `GET` an HLS or DASH manifest (`/video/…@m3u8` or `.mpd`).
    ///
    /// # Errors
    ///
    /// As [`media`](Self::media), and [`ClientError::Build`].
    pub async fn stream(&self, source: &str, stream: &StreamParams) -> Result<StreamManifest, ClientError> {
        let url = self.core.urls.stream_url(source, stream)?;
        let ctype = stream.content_type();
        let res = self.send(Method::Get, &url, self.core.headers_for(&stream_accept(ctype), None, &[])?).await?;
        let res = Self::checked(res, Method::Get).await?;
        let content_type =
            res.headers().get(reqwest::header::CONTENT_TYPE).and_then(|v| v.to_str().ok()).unwrap_or(ctype).to_owned();
        let body = Self::body(res, &url).await?;
        Ok(StreamManifest { url, content_type, body: String::from_utf8_lossy(&body).into_owned() })
    }

    /// `GET` the playground's `api/meta` (needs `XI_PLAYGROUND_ENABLED`; admin listener by default).
    ///
    /// # Errors
    ///
    /// [`ClientError::Http`], [`ClientError::Transport`] or [`ClientError::Decode`].
    pub async fn playground_meta(&self) -> Result<serde_json::Value, ClientError> {
        self.json(&self.core.playground_meta).await
    }
}
