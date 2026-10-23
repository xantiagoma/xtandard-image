//! The async client against a stand-in server (feature `async`).
#![cfg(feature = "async")]

mod common;

use xtandard_image::{
    ByteRange, Client, ClientError, HttpErrorClass, InfoUrlParams, MediaOptions, Method, Options, QueryUrlBuilder,
    QueryUrlParams, ReadyState, StreamParams, StreamProtocol, UrlParams, VideoUrlParams,
};

use common::standin::{self, body, key};

fn client(base: &str) -> Client {
    Client::builder(base).signing(key()).playground_path("/_admin/play/").header("X-Test", "1").build().expect("client")
}

fn clip() -> Options {
    Options::new().set("vd", 2)
}

fn webm() -> VideoUrlParams {
    VideoUrlParams::new().extension("webm")
}

#[tokio::test]
async fn health() {
    let srv = standin::start();
    let xi = client(&srv.base);
    xi.healthz().await.expect("healthz");
    assert_eq!(srv.last().headers["accept"], "text/plain");
    assert_eq!(srv.last().headers["x-test"], "1");
    let r = xi.readyz().await.expect("readyz");
    assert_eq!((r.ready, r.state), (false, ReadyState::Draining));
}

#[tokio::test]
async fn info() {
    let srv = standin::start();
    let xi = client(&srv.base);
    let info = xi
        .info("s3://b/cat.jpg", Options::new().set("size", true).set("format", true), InfoUrlParams::new())
        .await
        .expect("info");
    assert_eq!(info.width, Some(640));
    let seen = srv.last();
    assert_eq!(seen.headers["accept"], "application/json");
    assert!(seen.target.starts_with("/info/"));
    assert_eq!(xi.info_at(&seen.target).await.expect("info_at").format.as_deref(), Some("jpeg"));
}

#[tokio::test]
async fn video_whole_range_and_head() {
    let srv = standin::start();
    let xi = client(&srv.base);
    let res = xi.video("s3://m/clip.mov", clip(), webm(), &MediaOptions::new()).await.expect("video");
    assert_eq!(res.bytes().await.expect("body").to_vec(), body());

    let res = xi
        .video("s3://m/clip.mov", clip(), webm(), &MediaOptions::new().range(ByteRange::new(10, 19)))
        .await
        .expect("range");
    assert_eq!(res.status().as_u16(), 206);
    assert_eq!(res.headers()["content-range"], "bytes 10-19/1000");
    assert_eq!(res.bytes().await.expect("body").to_vec(), body()[10..20].to_vec());
    assert_eq!(srv.last().headers["range"], "bytes=10-19");

    let res = xi.video("s3://m/clip.mov", clip(), webm(), &MediaOptions::new().head()).await.expect("head");
    assert_eq!(srv.last().method, "HEAD");
    assert_eq!(res.headers()["content-type"], "video/webm");
}

#[tokio::test]
async fn streamed_body() {
    let srv = standin::start();
    let xi = client(&srv.base);
    let mut res = xi.video("s3://m/clip.mov", clip(), webm(), &MediaOptions::new()).await.expect("video");
    let mut got = Vec::new();
    while let Some(chunk) = res.chunk().await.expect("chunk") {
        got.extend_from_slice(&chunk);
    }
    assert_eq!(got, body());
}

#[tokio::test]
async fn bad_signature_is_forbidden() {
    let srv = standin::start();
    let xi = client(&srv.base);
    let err =
        xi.video("s3://m/clip.mov", Options::new().set("vd", 3), webm(), &MediaOptions::new()).await.expect_err("403");
    let http = err.as_http().expect("an HTTP error");
    assert_eq!((http.status, http.kind.as_str(), http.message.as_str()), (403, "unauthorized", "invalid signature"));
    assert_eq!(http.class(), HttpErrorClass::Forbidden);
}

#[tokio::test]
async fn stream_and_ticket_child() {
    let srv = standin::start();
    let xi = client(&srv.base);
    let m = xi
        .stream("s3://m/clip.mov", &StreamParams::new(StreamProtocol::Hls).renditions([360, 720]))
        .await
        .expect("stream");
    assert!(m.url.contains("/st:hls:360:720/"), "{}", m.url);
    assert_eq!(m.content_type, "application/vnd.apple.mpegurl");
    assert!(srv.last().headers["accept"].starts_with("application/vnd.apple.mpegurl"));
    let child = m.body.lines().nth(1).expect("a child");
    let res = xi.media(child, &MediaOptions::new().range(ByteRange::suffix(4))).await.expect("child");
    assert_eq!(srv.last().target, "/_stream/T/v0.m3u8");
    assert_eq!(srv.last().headers["range"], "bytes=-4");
    assert_eq!(res.status().as_u16(), 200); // the stand-in only answers start-end ranges
}

#[tokio::test]
async fn errors_by_status() {
    let srv = standin::start();
    let xi = client(&srv.base);
    let nf = xi.image("s3://b/missing.jpg", (), UrlParams::new(), &MediaOptions::new()).await.expect_err("404");
    assert_eq!(nf.as_http().map(|h| (h.class(), h.kind.as_str())), Some((HttpErrorClass::NotFound, "not_found")));

    let gw = xi.media("/proxy-error", &MediaOptions::new()).await.expect_err("502");
    let gw = gw.as_http().expect("http");
    assert_eq!(
        (gw.class(), gw.kind.as_str(), gw.message.as_str()),
        (HttpErrorClass::Gateway, "unknown", "<html>bad gateway</html>")
    );
    assert!(gw.body.is_none() && gw.is_server_error());

    let busy = xi.media("/busy", &MediaOptions::new()).await.expect_err("429");
    let busy = busy.as_http().expect("http");
    assert_eq!((busy.class(), busy.request_id.as_deref()), (HttpErrorClass::TooManyRequests, Some("r1")));

    let down = client(&common::closed_base()).healthz().await.expect_err("down");
    assert!(matches!(down, ClientError::Transport { .. }), "{down:?}");

    let bad = xi.info_at("/not-json").await.expect_err("decode");
    assert!(matches!(bad, ClientError::Decode { .. }), "{bad:?}");

    let build = xi.image("", (), UrlParams::new(), &MediaOptions::new()).await.expect_err("build");
    assert!(matches!(&build, ClientError::Build(e) if e.code() == "invalid_source"), "{build:?}");

    let range = xi.media("/x", &MediaOptions::new().range(ByteRange::new(5, 4))).await.expect_err("range");
    assert!(matches!(&range, ClientError::Build(e) if e.code() == "invalid_range"), "{range:?}");
}

#[tokio::test]
async fn playground_meta_and_accept() {
    let srv = standin::start();
    let xi = client(&srv.base);
    assert_eq!(xi.playground_meta().await.expect("meta")["version"], "test");
    let _ = xi
        .image("s3://b/missing.jpg", (), UrlParams::new(), &MediaOptions::new().accept("image/avif,image/webp"))
        .await;
    assert_eq!(srv.last().headers["accept"], "image/avif,image/webp");
}

#[tokio::test]
async fn own_reqwest_client() {
    let srv = standin::start();
    // reqwest runs without a default TLS provider here; a client of your own needs one.
    let _ = rustls::crypto::ring::default_provider().install_default();
    let xi =
        Client::builder(format!("{}/", srv.base)).build_with(xtandard_image::reqwest::Client::new()).expect("client");
    assert_eq!(
        xi.urls().info_url("s3://b/a.jpg", (), InfoUrlParams::new()).expect("url"),
        format!("{}/info/insecure/czM6Ly9iL2EuanBn", srv.base)
    );
    xi.healthz().await.expect("healthz");
}

/// A signed `/q/` URL path and its query.
fn query_url() -> (String, String) {
    let url = QueryUrlBuilder::new()
        .signing(key())
        .url("s3://b/cat.jpg", Options::new().set("bl", 2), QueryUrlParams::new().param("w", 300))
        .expect("url");
    let (path, query) = url.split_once('?').expect("a query");
    assert_eq!(path, "/q/");
    (url.clone(), query.to_owned())
}

#[tokio::test]
async fn query_method_opt_in() {
    let srv = standin::start();
    let (url, query) = query_url();

    // Off by default: a query URL goes out as GET.
    let xi = client(&srv.base);
    xi.media(&url, &MediaOptions::new()).await.expect("get");
    let seen = srv.last();
    assert_eq!((seen.method.as_str(), seen.target.as_str(), seen.body.as_str()), ("GET", url.as_str(), ""));
    assert!(!seen.headers.contains_key("content-type"));

    // One call as QUERY.
    let res = xi.media(&url, &MediaOptions::new().query().accept("image/webp")).await.expect("query");
    assert_eq!(res.bytes().await.expect("body").as_ref(), b"q");
    let seen = srv.last();
    assert_eq!((seen.method.as_str(), seen.target.as_str()), ("QUERY", "/q/"));
    assert_eq!(seen.headers["content-type"], "application/x-www-form-urlencoded");
    assert_eq!((seen.headers["accept"].as_str(), seen.headers["x-test"].as_str()), ("image/webp", "1"));
    assert_eq!(seen.body, query);

    // query_method: URLs under the mount go out as QUERY unless the call names a method.
    let on = Client::builder(&srv.base).signing(key()).query_method(true).build().expect("client");
    on.media(&url, &MediaOptions::new()).await.expect("query");
    assert_eq!((srv.last().method.as_str(), srv.last().body), ("QUERY", query.clone()));
    on.media(&url, &MediaOptions::new().head()).await.expect("head");
    assert_eq!((srv.last().method.as_str(), srv.last().target), ("HEAD", url.clone()));
    on.media(&url, &MediaOptions::new().method(Method::Get)).await.expect("get");
    assert_eq!(srv.last().method, "GET");
    on.media("/busy", &MediaOptions::new()).await.expect_err("429");
    assert_eq!((srv.last().method.as_str(), srv.last().target.as_str()), ("GET", "/busy"));
    on.video("s3://m/clip.mov", clip(), webm(), &MediaOptions::new()).await.expect("video");
    assert_eq!(srv.last().method, "GET");

    // A custom mount.
    let mounted = Client::builder(&srv.base).query_method(true).query_mount("/img/q/").build().expect("client");
    mounted.media("/img/q/watermark?url=s3://b/a.jpg&text=hi", &MediaOptions::new()).await.expect_err("404");
    let seen = srv.last();
    assert_eq!(
        (seen.method.as_str(), seen.target.as_str(), seen.body.as_str()),
        ("QUERY", "/img/q/watermark", "url=s3://b/a.jpg&text=hi")
    );
    mounted.media(&url, &MediaOptions::new()).await.expect("get");
    assert_eq!(srv.last().method, "GET");

    // QUERY needs a URL with a query.
    let err = xi.media("/q/", &MediaOptions::new().query()).await.expect_err("no query");
    assert!(matches!(&err, ClientError::Build(e) if e.code() == "invalid_request"), "{err:?}");
}
