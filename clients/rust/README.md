<p align="center"><img src="https://raw.githubusercontent.com/xantiagoma/xtandard-image/main/docs/assets/logo-128.png" alt="xtandard-image" width="128" height="128"></p>

# xtandard-image for Rust

**Typed URL builder, signing and HTTP client for the xtandard-image server.**

[![crates.io: pending](https://img.shields.io/badge/crates.io-pending-lightgrey)](https://github.com/xantiagoma/xtandard-image/blob/main/docs/RELEASE.md) <!-- registry-status: pending -->
[![license: MIT](https://img.shields.io/badge/license-MIT-blue)](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

`xtandard-image` is the Rust client for [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme), an open-source media server that covers your application's image and video needs, built on libvips and FFmpeg. The crate builds and signs the URLs your code sends to the server, and wraps its `/info`, media and health endpoints; it does not process images itself. To run a server, see [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md), or start one with signing keys:

```sh
export XI_SIGNING_KEY="$(openssl rand -hex 32)" XI_SIGNING_SALT="$(openssl rand -hex 32)"
docker run -d --name xtandard-image -p 8080:8080 \
  -e XI_SOURCES=web -e XI_SOURCES_WEB_TYPE=http -e XI_SOURCES_WEB_ALLOW=images.unsplash.com \
  -e XI_SIGNING_KEYS="$XI_SIGNING_KEY" -e XI_SIGNING_SALTS="$XI_SIGNING_SALT" \
  ghcr.io/xantiagoma/xtandard-image:0.1.0-rc.2
```

## Install

```sh
cargo add xtandard-image
```

Not published yet: publishing to crates.io is being set up. Until then, install from the repository: <!-- registry-status: pending -->

```toml
[dependencies]
xtandard-image = { git = "https://github.com/xantiagoma/xtandard-image" }
```

Cargo finds the crate in `clients/rust` by its name. The library is `xtandard_image`.

| Feature | Default | What |
|---|---|---|
| `async` | yes | `Client` (reqwest, tokio) |
| `blocking` | no | `blocking::Client` (`reqwest::blocking`) |
| `serde` | with a client | `InfoResponse`, `ErrorResponse` and the other response types |

`default-features = false` keeps only the URL builders, signing and encryption: no HTTP stack and no I/O.

## Quick start

Build a signed URL with the keys the server was started with:

```rust
use xtandard_image::{ImgproxyUrlBuilder, SigningKey};

let key = SigningKey::new(std::env::var("XI_SIGNING_KEY")?, std::env::var("XI_SIGNING_SALT")?)?;
let img = ImgproxyUrlBuilder::new().base_url("http://localhost:8080").signing(key.clone());

let url = img
    .image("https://images.unsplash.com/photo-1506744038136-46273834b3fb")
    .resize(("fill", 1200, 630))
    .gravity("sm")
    .extension("jpg")
    .build()?;
// http://localhost:8080/<signature>/rs:fill:1200:630/g:sm/aHR0cHM6….jpg
```

Ask the server about the source with the HTTP client:

```rust
use xtandard_image::{Client, InfoUrlParams, Options};

let xi = Client::builder("http://localhost:8080").signing(key).build()?;
let info = xi
    .info("https://images.unsplash.com/photo-1506744038136-46273834b3fb", Options::new(), InfoUrlParams::new())
    .await?;
println!("{:?} {:?} {:?}", info.format, info.width, info.height); // Some("jpeg") Some(7360) Some(4912)
```

Sign URLs on a server only: the signing key signs any URL, and the encryption key decrypts every `enc/` source.

## Usage

### Building URLs

There are two equivalent forms, an `Options` value and a fluent chain with one method per option name and alias:

```rust
use xtandard_image::{Options, UrlParams, processing::ResizeType};

let opts = Options::new().set("rs", (ResizeType::Fill, 300, 400)).set("g", "sm").set("q", 80);
img.url("s3://photos/cat.jpg", opts, UrlParams::new().extension("webp"))?;
img.image("s3://photos/cat.jpg").resize(("fill", 300, 400)).g("sm").quality(80).extension("webp").build()?;
// both: http://localhost:8080/<signature>/rs:fill:300:400/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp
```

An argument is anything that converts into `Arg`: integers, floats, booleans, strings, `None` (an empty argument) and the generated enums (`processing::ResizeType`, …); a tuple, array or `Vec` is an argument list. The options themselves are documented in [URL dialects §3.2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#32-processing-options--tr).

The builder also takes `separator` (default `:`), `unsigned_signature` (`insecure`, `unsafe` or `_`) and `option_names` (short names by default, canonical or as given).

### Chained pipelines

An array or `Vec` of `Options`, or `pipeline()` in the fluent form, writes `/-/` between pipelines:

```rust
img.url(src, [Options::new().set("rs", ("fit", 500, 500)), Options::new().set("bl", 2)], UrlParams::new())?;
img.image(src).rs(("fit", 500, 500)).pipeline().bl(2).build()?;
```

### Sources

| `SourceEncoding` | URL |
|---|---|
| `Base64` (default) | `/…/czM6Ly9waG90b3MvY2F0LmpwZw.webp`; `split_source(16)` splits it into segments |
| `Plain` | `/…/plain/https://example.com/a%20b.jpg@webp`, escaped for you |
| `Encrypted` | `/…/enc/<token>.webp`; needs `encryption` (below) |

Set it on the builder, per URL (`UrlParams::new().source_encoding(…)`) or per chain. `xi_source("photos", "2026/cat.jpg")?` writes a named source (`xi://photos/2026/cat.jpg`) and `source_url(SourceScheme::S3, "bucket", "cat.jpg")?` a storage URL.

### `/info`

```rust
img.info_url(src, Options::new().set("size", true).set("format", true), InfoUrlParams::new())?;
img.info(src).size(true).format(true).build()?;
```

### Video and streams

`/video` URLs use the video option catalogue; the extension is the container (`mp4`, `webm`, `mp3`, `png` for a waveform, …). `stream_url` builds HLS and DASH manifests, whose segments a player fetches without any key:

```rust
use xtandard_image::{StreamParams, StreamProtocol};

img.video("s3://media/talk.mov").resize(("fill", 640, 360)).start(12).duration(5).extension("webm").build()?;
img.stream_url("s3://media/talk.mov", &StreamParams::new(StreamProtocol::Hls).renditions([360, 720]).segment_duration(4))?;
```

The server needs the `full` or `gpl` image for video. Options and containers: [URL dialects §3.8](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#38-video-transformations-wp-122-wp-123) and [§3.10](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#310-hls-and-dash-streams-wp-126-every-dialect).

### Query dialect

`QueryUrlBuilder` builds `/q/` URLs (native keys plus imgproxy options in `o=`), `/q/video` and imaginary endpoints, signed with `xi_sig`. The server needs `XI_DIALECTS_QUERY_ENABLED=true`.

```rust
use xtandard_image::{ImaginaryEndpoint, QueryUrlBuilder, QueryUrlParams};

let q = QueryUrlBuilder::new().base_url("https://img.example.com").signing(key);
q.url("s3://photos/cat.jpg", Options::new().set("bl", 2), QueryUrlParams::new().param("w", 300).param("fit", "cover").extension("webp"))?;
// https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&fit=cover&o=bl:2&format=webp&xi_sig=…
q.imaginary_url(ImaginaryEndpoint::Smartcrop, "s3://photos/cat.jpg", [("width", 300), ("height", 300)])?;
```

`query_method_request(&url)` turns a query URL into an HTTP QUERY request, and `ClientBuilder::query_method(true)` sends one. See [URL dialects §4](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#4-query-dialect-native--imaginary).

### Signing and encryption

`SigningKey::new` takes the server's hex key and salt (`XI_SIGNING_KEYS` / `XI_SIGNING_SALTS`, or imgproxy's `IMGPROXY_KEY` / `IMGPROXY_SALT`). `EncryptionKey::new` takes the first `XI_ENCRYPTION_KEYS` entry, for `enc/` sources and password-protected PDFs:

```rust
use xtandard_image::{EncryptionKey, SourceEncoding};

let img = img.encryption(EncryptionKey::new(std::env::var("XI_ENCRYPTION_KEY")?)?);
img.url("s3://private/cat.jpg", Options::new().set("w", 300), UrlParams::new().source_encoding(SourceEncoding::Encrypted))?;
img.url("s3://docs/contract.pdf", Options::new().set("rs", ("fit", 800, 0)), UrlParams::new().pdf_password("s3cret"))?;
```

Each `enc/` token gets a random IV unless the key has a fixed one. `sign`, `sign_imgproxy_path`, `sign_xi_url`, `sign_xi_query`, `encrypt_source` and `encrypt_pdf_password` are exported too; `sign_xi_url` adds an `xi_sig` to a URL of any other dialect. Schemes: [URL dialects §2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#2-signing-schemes-at-a-glance).

### HTTP client

`Client` (async, feature `async`) and `blocking::Client` (feature `blocking`) run on reqwest with rustls: `healthz`, `readyz`, `info`, `image`, `video`, `stream`, `media` and `playground_meta`. Media calls return the `reqwest::Response` with the body unread:

```rust
use xtandard_image::{ByteRange, MediaOptions, VideoUrlParams};

let res = xi.image(src, Options::new().set("w", 300), UrlParams::new(), &MediaOptions::new().accept("image/avif,image/webp")).await?;
let part = xi.video(src, Options::new().set("vd", 5), VideoUrlParams::new().extension("webm"), &MediaOptions::new().range(ByteRange::new(0, 65535))).await?;
```

`build_with(reqwest::Client)` uses a reqwest client of your own, and `xtandard_image::reqwest` re-exports the reqwest version the crate uses.

### Errors

A URL that cannot be built is an `Error` with one variant per error code (`Error::UnknownOption`, `InvalidArgument`, `InvalidSource`, …); `err.code()` returns the code. The client returns a `ClientError`: `Http` for a non-2xx answer, with the server's `status`, `kind` and `request_id`; `Transport` when no answer came; `Build` and `Decode`.

```rust
use xtandard_image::ClientError;

let err = img.image(src).option("nope", 1).build().unwrap_err();
assert_eq!(err.code(), "unknown_option");

match xi.info("https://example.com/a.jpg", Options::new(), InfoUrlParams::new()).await {
    Err(ClientError::Http(e)) => eprintln!("{} {} {:?}", e.status, e.kind, e.request_id), // 403 forbidden Some("…")
    other => { /* … */ }
}
```

## Compatibility

- Rust 1.89 or later (edition 2024). The crate is `#![forbid(unsafe_code)]`. TLS is rustls with the ring provider and the platform's certificate verifier: no OpenSSL.
- The crate version is the server version: `xtandard-image 0.2.1` matches `ghcr.io/xantiagoma/xtandard-image:0.2.1`. Versions follow [ZeroVer](https://0ver.org): a minor bump may break the API, and a patch does not.

## Licences

The runtime dependencies are MIT, BSD, Apache-2.0 or ISC, with one exception: `Unicode-3.0`, the permissive licence of the Unicode data tables in the ICU4X crates that `url` uses through reqwest (and of `unicode-ident`). `deny.toml` lists it crate by crate.

## Links

- [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme) and [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md)
- [URL dialects](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md): every option and signing scheme
- [Playground](https://github.com/xantiagoma/xtandard-image/blob/main/docs/PLAYGROUND.md): build URLs in a browser and copy the Rust code
- [Changelog](https://github.com/xantiagoma/xtandard-image/blob/main/CHANGELOG.md)
- [Contributing](https://github.com/xantiagoma/xtandard-image/blob/main/CONTRIBUTING.md) and [the official clients](https://github.com/xantiagoma/xtandard-image/blob/main/clients/README.md)
- [Security policy](https://github.com/xantiagoma/xtandard-image/blob/main/SECURITY.md)
- Licence: [MIT](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

A personal project, provided as is under the MIT licence: see the [disclaimer](https://github.com/xantiagoma/xtandard-image#disclaimer).

Design: every official client is generated from one spec and passes one conformance fixture ([ADR 0013](https://github.com/xantiagoma/xtandard-image/blob/main/docs/adr/0013-official-clients.md)).
