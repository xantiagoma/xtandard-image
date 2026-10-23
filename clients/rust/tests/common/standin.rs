//! The stand-in xtandard-image server of the client tests (as the Python client's `MockTransport`).

use serde_json::json;
use xtandard_image::{ImgproxyUrlBuilder, Options, SigningKey, VideoUrlParams};

use super::{Reply, Seen, Server};

pub const KEY: &str = "736563726574";
pub const SALT: &str = "68656C6C6F";

pub fn key() -> SigningKey {
    SigningKey::new(KEY, SALT).expect("key")
}

/// 1000 bytes: 0, 1, …, 255, 0, 1, …
pub fn body() -> Vec<u8> {
    (0..1000_u32).map(|i| u8::try_from(i % 256).expect("byte")).collect()
}

/// The path of the one signed `/video` URL the stand-in accepts.
pub fn video_path() -> String {
    ImgproxyUrlBuilder::new()
        .signing(key())
        .video_url("s3://m/clip.mov", Options::new().set("vd", 2), VideoUrlParams::new().extension("webm"))
        .expect("url")
}

fn range(req: &Seen, len: usize) -> Option<(usize, usize)> {
    let spec = req.headers.get("range")?.strip_prefix("bytes=")?;
    let (a, b) = spec.split_once('-')?;
    let start = a.parse().ok()?;
    let end = if b.is_empty() { len - 1 } else { b.parse().ok()? };
    Some((start, end))
}

pub fn start() -> Server {
    let video = video_path();
    Server::start(move |req| {
        let path = req.target.split('?').next().unwrap_or_default();
        if path == "/healthz" {
            return Reply::new(200, "text/plain", "ok\n");
        }
        if path == "/readyz" {
            return Reply::new(503, "text/plain", "draining\n");
        }
        if path.starts_with("/info/") {
            return Reply::json(200, &json!({"width": 640, "height": 480, "format": "jpeg"}));
        }
        if path.starts_with("/video/") && path.rsplit('.').next() == Some("m3u8") {
            return Reply::new(200, "application/vnd.apple.mpegurl", "#EXTM3U\n/_stream/T/v0.m3u8\n");
        }
        if path.starts_with("/video/") || path.starts_with("/_stream/") {
            if path.starts_with("/video/") && req.target != video {
                return Reply::json(403, &json!({"error": {"kind": "unauthorized", "message": "invalid signature"}}));
            }
            let all = body();
            if let Some((start, end)) = range(req, all.len()) {
                return Reply::new(206, "video/webm", all[start..=end].to_vec())
                    .header("Content-Range", &format!("bytes {start}-{end}/{}", all.len()));
            }
            return Reply::new(200, "video/webm", all);
        }
        if path == "/q" || path.starts_with("/q/") {
            // The query dialect answers GET and QUERY alike (RFC 10008).
            return Reply::new(200, "image/webp", "q");
        }
        if path == "/_admin/play/api/meta" {
            return Reply::json(200, &json!({"version": "test"}));
        }
        if path == "/proxy-error" {
            return Reply::new(502, "text/html", "<html>bad gateway</html>");
        }
        if path == "/busy" {
            return Reply::json(
                429,
                &json!({"error": {"kind": "overloaded", "message": "too many requests", "request_id": "r1"}}),
            );
        }
        if path == "/not-json" {
            return Reply::new(200, "application/json", "{");
        }
        Reply::json(404, &json!({"error": {"kind": "not_found", "message": "source image not found"}}))
    })
}
