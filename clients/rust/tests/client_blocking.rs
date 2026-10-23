//! The blocking client against a stand-in server (feature `blocking`).
#![cfg(feature = "blocking")]

mod common;

use std::io::Read as _;

use xtandard_image::blocking::Client;
use xtandard_image::{
    ByteRange, ClientError, HttpErrorClass, InfoUrlParams, MediaOptions, Options, QueryUrlBuilder, QueryUrlParams,
    ReadyState, StreamParams, StreamProtocol, UrlParams, VideoUrlParams,
};

use common::standin::{self, body, key};

fn client(base: &str) -> Client {
    Client::builder(base).signing(key()).playground_path("/_admin/play/").build().expect("client")
}

fn clip() -> Options {
    Options::new().set("vd", 2)
}

fn webm() -> VideoUrlParams {
    VideoUrlParams::new().extension("webm")
}

#[test]
fn health_and_info() {
    let srv = standin::start();
    let xi = client(&srv.base);
    xi.healthz().expect("healthz");
    let r = xi.readyz().expect("readyz");
    assert_eq!((r.ready, r.state), (false, ReadyState::Draining));
    let info = xi.info("s3://b/cat.jpg", Options::new().set("size", true), InfoUrlParams::new()).expect("info");
    assert_eq!(info.height, Some(480));
    assert_eq!(xi.info_at(&srv.last().target).expect("info_at").format.as_deref(), Some("jpeg"));
    assert_eq!(xi.playground_meta().expect("meta")["version"], "test");
}

#[test]
fn video_whole_range_head_and_streamed() {
    let srv = standin::start();
    let xi = client(&srv.base);
    let res = xi.video("s3://m/clip.mov", clip(), webm(), &MediaOptions::new()).expect("video");
    assert_eq!(res.bytes().expect("body").to_vec(), body());

    let res = xi.video("s3://m/clip.mov", clip(), webm(), &MediaOptions::new().range(0..10)).expect("range");
    assert_eq!(res.status().as_u16(), 206);
    assert_eq!(res.bytes().expect("body").to_vec(), body()[..10].to_vec());
    assert_eq!(srv.last().headers["range"], "bytes=0-9");

    xi.video("s3://m/clip.mov", clip(), webm(), &MediaOptions::new().head()).expect("head");
    assert_eq!(srv.last().method, "HEAD");

    let mut res = xi.video("s3://m/clip.mov", clip(), webm(), &MediaOptions::new()).expect("video");
    let mut got = Vec::new();
    res.read_to_end(&mut got).expect("read");
    assert_eq!(got, body());
}

#[test]
fn stream_and_errors() {
    let srv = standin::start();
    let xi = client(&srv.base);
    let m = xi.stream("s3://m/clip.mov", &StreamParams::new(StreamProtocol::Hls)).expect("stream");
    assert!(m.body.starts_with("#EXTM3U"));
    let child = m.body.lines().nth(1).expect("a child");
    assert_eq!(
        xi.media(child, &MediaOptions::new().range(ByteRange::suffix(4))).expect("child").status().as_u16(),
        200
    );

    let err = xi.video("s3://m/clip.mov", Options::new().set("vd", 3), webm(), &MediaOptions::new()).expect_err("403");
    assert_eq!(err.as_http().map(|h| (h.class(), h.kind.as_str())), Some((HttpErrorClass::Forbidden, "unauthorized")));
    let nf = xi.image("s3://b/missing.jpg", (), UrlParams::new(), &MediaOptions::new()).expect_err("404");
    assert_eq!(nf.as_http().map(|h| h.status), Some(404));
    let busy = xi.media("/busy", &MediaOptions::new()).expect_err("429");
    assert_eq!(busy.as_http().and_then(|h| h.request_id.clone()).as_deref(), Some("r1"));
    let down = client(&common::closed_base()).healthz().expect_err("down");
    assert!(matches!(down, ClientError::Transport { .. }), "{down:?}");
}

#[test]
fn query_method_opt_in() {
    let srv = standin::start();
    let url = QueryUrlBuilder::new()
        .signing(key())
        .url("s3://b/cat.jpg", (), QueryUrlParams::new().param("w", 300))
        .expect("url");
    let query = url.split_once('?').expect("a query").1.to_owned();

    client(&srv.base).media(&url, &MediaOptions::new()).expect("get");
    assert_eq!((srv.last().method.as_str(), srv.last().target), ("GET", url.clone()));

    let mut res = client(&srv.base).media(&url, &MediaOptions::new().query()).expect("query");
    let mut got = String::new();
    res.read_to_string(&mut got).expect("body");
    assert_eq!(got, "q");
    let seen = srv.last();
    assert_eq!((seen.method.as_str(), seen.target.as_str(), seen.body.as_str()), ("QUERY", "/q/", query.as_str()));
    assert_eq!(seen.headers["content-type"], "application/x-www-form-urlencoded");

    let on = Client::builder(&srv.base).query_method(true).build().expect("client");
    on.media(&url, &MediaOptions::new()).expect("query");
    assert_eq!((srv.last().method.as_str(), srv.last().body), ("QUERY", query));
    on.media(&url, &MediaOptions::new().head()).expect("head");
    assert_eq!(srv.last().method, "HEAD");
    on.video("s3://m/clip.mov", clip(), webm(), &MediaOptions::new()).expect_err("403");
    assert_eq!(srv.last().method, "GET");
}
