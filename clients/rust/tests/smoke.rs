//! Smoke test against a running server, a no-op unless `XI_SMOKE_URL` is set.
//!
//! The playground demo (compose.playground.yml, unsigned URLs allowed) works:
//!
//! ```sh
//! XI_SMOKE_URL=http://127.0.0.1:8080 cargo test --all-features --test smoke -- --nocapture
//! ```
//!
//! It needs the demo's local fixtures (`xi://local/…`) and, for the `/video`
//! cases, a full or gpl image with `XI_VIDEO_TRANSFORM_ENABLED`.
#![cfg(any(feature = "async", feature = "blocking"))]

use xtandard_image::{InfoUrlParams, MediaOptions, Options, UrlParams, xi_source};

fn base() -> Option<String> {
    let base = std::env::var("XI_SMOKE_URL").ok().filter(|s| !s.is_empty());
    if base.is_none() {
        println!("skipped: set XI_SMOKE_URL to run against a server");
    }
    base
}

fn image() -> String {
    xi_source("local", "jpeg/baseline.jpg").expect("source")
}

#[cfg(feature = "async")]
fn video() -> String {
    xi_source("local", "video/bbb-h264-aac.mp4").expect("source")
}

#[cfg(feature = "async")]
fn short_clip() -> Options {
    Options::new().set("vd", 1).set("w", 160)
}

#[cfg(feature = "async")]
#[tokio::test]
async fn health_image_info_and_errors() {
    use xtandard_image::{HttpError, HttpErrorClass, SourceEncoding};

    let Some(base) = base() else { return };
    let xi = xtandard_image::Client::builder(&base).source_encoding(SourceEncoding::Plain).build().expect("client");
    xi.healthz().await.expect("healthz");
    assert!(xi.readyz().await.expect("readyz").ready);
    let res = xi
        .image(
            &image(),
            Options::new().set("rs", ("fit", 64, 64)),
            UrlParams::new().extension("webp"),
            &MediaOptions::new(),
        )
        .await
        .expect("image");
    assert_eq!(res.headers()["content-type"], "image/webp");
    assert!(!res.bytes().await.expect("body").is_empty());
    let info = xi
        .info(
            &image(),
            Options::new().set("size", true).set("format", true).set("dimensions", true),
            InfoUrlParams::new(),
        )
        .await
        .expect("info");
    assert_eq!(info.format.as_deref(), Some("jpeg"));
    assert!(info.size.unwrap_or_default() > 0);
    let missing = xi
        .image(&xi_source("local", "jpeg/missing.jpg").expect("source"), (), UrlParams::new(), &MediaOptions::new())
        .await
        .expect_err("404");
    assert_eq!(missing.as_http().map(HttpError::class), Some(HttpErrorClass::NotFound));
}

#[cfg(feature = "async")]
#[tokio::test]
async fn query_dialect() {
    use xtandard_image::{QueryUrlBuilder, QueryUrlParams};

    let Some(base) = base() else { return };
    let url = QueryUrlBuilder::new()
        .base_url(&base)
        .url(&image(), (), QueryUrlParams::new().param("w", 32).param("f", "png"))
        .expect("url");
    let xi = xtandard_image::Client::builder(&base).build().expect("client");
    assert_eq!(xi.media(&url, &MediaOptions::new()).await.expect("query").headers()["content-type"], "image/png");
}

#[cfg(feature = "async")]
#[tokio::test]
async fn video_range_and_stream() {
    use xtandard_image::{ByteRange, StreamParams, StreamProtocol, VideoUrlParams};

    let Some(base) = base() else { return };
    let xi = xtandard_image::Client::builder(&base).build().expect("client");
    let webm = || VideoUrlParams::new().extension("webm");
    let res = xi
        .video(&video(), short_clip(), webm(), &MediaOptions::new().range(ByteRange::new(0, 99)))
        .await
        .expect("video");
    assert!(matches!(res.status().as_u16(), 200 | 206), "{}", res.status());
    let head = xi.video(&video(), short_clip(), webm(), &MediaOptions::new().head()).await.expect("head");
    assert!(head.headers()["content-type"].to_str().expect("ascii").starts_with("video/webm"));
    let m = xi.stream(&video(), &StreamParams::new(StreamProtocol::Hls).renditions([240])).await.expect("stream");
    assert!(m.body.starts_with("#EXTM3U"), "{}", m.body);
    let child = m.body.lines().find(|l| !l.is_empty() && !l.starts_with('#')).expect("a child URL");
    assert_eq!(xi.media(child, &MediaOptions::new()).await.expect("child").status().as_u16(), 200);
}

#[cfg(feature = "blocking")]
#[test]
fn blocking_client() {
    let Some(base) = base() else { return };
    let xi = xtandard_image::blocking::Client::builder(&base).build().expect("client");
    xi.healthz().expect("healthz");
    let info = xi.info(&image(), Options::new().set("size", true), InfoUrlParams::new()).expect("info");
    assert!(info.size.unwrap_or_default() > 0);
    let res = xi
        .image(&image(), Options::new().set("w", 48), UrlParams::new().extension("png"), &MediaOptions::new())
        .expect("image");
    assert_eq!(res.headers()["content-type"], "image/png");
}
