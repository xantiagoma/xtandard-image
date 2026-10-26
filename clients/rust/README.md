<p align="center"><img src="https://raw.githubusercontent.com/xantiagoma/xtandard-image/main/docs/assets/logo-128.png" alt="xtandard-image" width="128" height="128"></p>

# xtandard-image for Rust

The official Rust client of [xtandard-image](https://github.com/xantiagoma/xtandard-image) ([ADR 0013](../../docs/adr/0013-official-clients.md)). It has:

- typed URL builders for the imgproxy dialect (processing, `/info`, `/video`, HLS and DASH) and the query dialect (`/q/`, `/q/video`, imaginary endpoints), in an object form and a fluent form with one method per option name and alias;
- imgproxy and `xi_sig` signing;
- `enc/` source and PDF password encryption;
- async and blocking HTTP clients on [reqwest](https://docs.rs/reqwest) with rustls.

It needs Rust 1.89 or later (edition 2024; `rust-version` in `Cargo.toml`). The crate is `#![forbid(unsafe_code)]`.

It is not on crates.io yet. The name `xtandard-image` was free on crates.io on 2026-10-15; the crate will follow the server's release tags.

**Build and sign URLs on a server.** The signing key signs every URL, and the encryption key decrypts every `enc/` source and PDF password.

## Install (from the repository, until the first release)

```toml
[dependencies]
xtandard-image = { git = "https://github.com/xantiagoma/xtandard-image" }
```

Cargo finds the crate in `clients/rust` by its name.

| Feature | Default | What |
|---|---|---|
| `async` | yes | `Client` (reqwest, tokio), with `serde` |
| `blocking` | no | `blocking::Client` (`reqwest::blocking`), with `serde` |
| `serde` | with a client | `InfoResponse`, `ErrorResponse` and the other response types |

`default-features = false` leaves the URL builders, signing and encryption: no HTTP stack and no I/O besides the OS random source for encryption IVs. Their dependencies are RustCrypto's `hmac`, `sha2`, `aes` and `cbc`, plus `base64`, `getrandom` and `thiserror`.

## URL builder

```rust
use xtandard_image::{
    EncryptionKey, ImgproxyUrlBuilder, InfoUrlParams, Options, SigningKey, SourceEncoding, StreamParams,
    StreamProtocol, UrlParams, VideoUrlParams, processing::ResizeType, xi_source,
};

let img = ImgproxyUrlBuilder::new()
    .base_url("https://img.example.com")
    .signing(SigningKey::new(std::env::var("XI_SIGNING_KEY")?, std::env::var("XI_SIGNING_SALT")?)?)
    .encryption(EncryptionKey::new(std::env::var("XI_ENCRYPTION_KEY")?)?); // only for enc/ sources and PDF passwords

// Object form: option names or aliases; a tuple of arguments or a bare first argument.
let opts = Options::new().set("rs", (ResizeType::Fill, 1200, 630)).set("g", "sm").set("q", 80);
img.url("s3://photos/cat.jpg", &opts, UrlParams::new().extension("webp"))?;
// https://img.example.com/<signature>/rs:fill:1200:630/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp

// Fluent form: one method per option name and alias.
img.image(xi_source("photos", "2026/cat.jpg")?).resize((ResizeType::Fit, 300, 300)).blur(2).extension("png").build()?;

// Chained pipelines (/-/): a Vec or array of Options, or pipeline() in the fluent form.
img.url("s3://photos/cat.jpg", [Options::new().set("rs", ("fit", 500, 500)), Options::new().set("bl", 2)], UrlParams::new())?;

// Plain or encrypted sources, a password-protected PDF.
img.url("https://example.com/a b.jpg", Options::new().set("w", 300), UrlParams::new().source_encoding(SourceEncoding::Plain))?;
img.url("s3://private/cat.jpg", Options::new().set("w", 300), UrlParams::new().source_encoding(SourceEncoding::Encrypted))?;
img.url("s3://docs/report.pdf", Options::new().set("rs", ("fit", 800, 0)), UrlParams::new().pdf_password("s3cret").extension("png"))?;

// /info, /video, HLS and DASH.
img.info_url("s3://photos/cat.jpg", Options::new().set("size", true).set("format", true), InfoUrlParams::new())?;
img.video_url("s3://media/talk.mov", Options::new().set("rs", ("fill", 640, 360)).set("vs", 12).set("vd", 8), VideoUrlParams::new().extension("webm"))?;
img.video("s3://media/talk.mov").start(12).duration(8).mute(true).extension("mp4").build()?;
img.stream_url("s3://media/talk.mov", &StreamParams::new(StreamProtocol::Hls).renditions([360, 720]).segment_duration(4))?;
```

Arguments are anything that converts into `Arg`: integers, floats (written like JavaScript writes them: `300`, `1.5`, `1e-7`), booleans (`1`/`0`), strings, `None` (an empty argument) and the generated argument enums (`processing::ResizeType`, `processing::GravityType`, `video::AudioCodecCodec`, …). A tuple, array, slice or `Vec` is an argument list. Each catalogue has an `OptionName` enum (`processing::OptionName::Resize`, with `name()`, `short()` and `aliases()`), and the formats, containers, protocols, segment formats, source schemes and error kinds are enums too (`ImageFormat`, `VideoContainer`, `StreamProtocol`, …).

A URL that cannot be built is an `Error` with one variant per language-neutral code: `Error::UnknownOption`, `InvalidArgument`, `InvalidSource`, `InvalidExtension`, `InvalidSeparator`, `MissingSigning`, `MissingEncryption`, `InvalidKey`, `InvalidPassword`, `InvalidRequest`, `InvalidName`, `InvalidEndpoint` and `InvalidRange`. `err.code()` returns the code (`"unknown_option"`, …).

## Query dialect

```rust
use xtandard_image::{Options, QueryUrlBuilder, QueryUrlParams, StreamParams, StreamProtocol, ImaginaryEndpoint};

let q = QueryUrlBuilder::new().base_url("https://img.example.com").signing(key);
q.url("s3://photos/cat.jpg", Options::new().set("bl", 2), QueryUrlParams::new().param("w", 300).param("f", "webp"))?;
// https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&f=webp&o=bl:2&xi_sig=…
q.image("s3://photos/cat.jpg").param("w", 300).bl(2).build()?;
q.video_url("s3://media/talk.mov", Options::new().set("rs", ("fill", 640, 360)).set("vs", 1), Some("webm"))?;
q.stream_url("s3://media/talk.mov", &StreamParams::new(StreamProtocol::Hls).renditions([360]))?;
q.imaginary_url(ImaginaryEndpoint::Resize, "s3://photos/cat.jpg", [("width", 300)])?;
```

## HTTP client

```rust
use xtandard_image::{
    ByteRange, Client, HttpErrorClass, InfoUrlParams, MediaOptions, Options, StreamParams, StreamProtocol, UrlParams,
    VideoUrlParams,
};

let xi = Client::builder("http://localhost:8080").signing(key).build()?;
xi.healthz().await?;
let info = xi.info("s3://photos/cat.jpg", Options::new().set("size", true).set("format", true), InfoUrlParams::new()).await?; // typed InfoResponse
let res = xi.image("s3://photos/cat.jpg", Options::new().set("w", 300), UrlParams::new(), &MediaOptions::new().accept("image/avif,image/webp")).await?;
let part = xi.video("s3://media/talk.mov", Options::new().set("vd", 5), VideoUrlParams::new().extension("webm"), &MediaOptions::new().range(ByteRange::new(0, 1023))).await?;
let head = xi.video("s3://media/talk.mov", Options::new().set("vd", 5), VideoUrlParams::new().extension("webm"), &MediaOptions::new().head()).await?;
let manifest = xi.stream("s3://media/talk.mov", &StreamParams::new(StreamProtocol::Hls).renditions([360, 720])).await?;
let segment = xi.media(manifest.body.lines().last().unwrap(), &MediaOptions::new()).await?; // ticket child URLs need no signing

// Blocking (feature "blocking"): the same methods without .await.
let xi = xtandard_image::blocking::Client::builder("http://localhost:8080").build()?;
```

The media methods return the `reqwest::Response` with the body unread: stream it with `chunk()` (async) or `std::io::Read` (blocking). Errors are a `ClientError`:

- `ClientError::Http`: a non-2xx status. The `HttpError` carries `status`, `kind`, `message` and `request_id` from the server's JSON error envelope (kind `"unknown"` and the body text otherwise), and `class()` maps the status as the Python client's exception classes do: `BadRequest` (400), `Forbidden` (403), `NotFound` (404), `NotAcceptable` (406), `Unprocessable` (422), `TooManyRequests` (429), `ServiceUnavailable` (503), `Gateway` (502, 504), `Server` (other 5xx).
- `ClientError::Transport`: no response (connection, TLS, timeout).
- `ClientError::Build`: the URL could not be built.
- `ClientError::Decode`: a 2xx body that is not what the endpoint returns.

### HTTP QUERY (opt-in)

The query dialect endpoints also accept the HTTP QUERY method (RFC 10008, server toggle `XI_SERVER_QUERY_METHOD`, on by default): the URL's query goes out as `application/x-www-form-urlencoded` content, so long option chains, watermark text or encrypted sources stay out of URLs. `xi_sig` does not cover the method, so the content is the query of the signed GET URL. It is **off by default** in the client, because many CDNs and proxies neither cache nor forward QUERY.

```rust
use xtandard_image::{MediaOptions, query_method_request};

let url = q.url("s3://photos/cat.jpg", Options::new().set("bl", 2), QueryUrlParams::new().param("w", 300))?;
let req = query_method_request(&url)?; // QueryMethodRequest { method: "QUERY", url: ".../q/", content_type, body: "url=…&w=300&o=bl:2&xi_sig=…" }
xi.media(&url, &MediaOptions::new().query()).await?; // this call as QUERY
let xi = Client::builder("http://localhost:8080").query_method(true).query_mount("/q").build()?;
xi.media(&url, &MediaOptions::new()).await?; // URLs under the mount go out as QUERY unless the call names a method (HEAD, GET)
```

A URL without a query, or with a fragment, is `Error::InvalidRequest` (`invalid_request`). URL builders are unchanged.

TLS is rustls with the ring provider and the platform's certificate verifier (no OpenSSL, no aws-lc). `build_with(reqwest::Client)` uses a reqwest client of your own; install a rustls `CryptoProvider` (or configure its TLS) before creating it. `xtandard_image::reqwest` re-exports the reqwest version the crate uses.

## Coverage

| Server feature | Rust |
|---|---|
| imgproxy processing options (90 with aliases) | `Options`, fluent methods, `processing::OptionName`, argument enums, every name in the shared builder fixture |
| `/info` options (35) | `info_url()`, `info()`, `Client::info()` (typed `InfoResponse`) |
| `/video` options (52) and all 12 containers; `/video` signing | `video_url()`, `video()`, `Client::video()` with Range and HEAD |
| HLS and DASH (`st`, `sgd`, `sgf:ts`, `@m3u8`, `@mpd`) | `stream_url()` on both builders, `Client::stream()`; ticket child URLs through `Client::media()` |
| Password-protected PDFs (`pdp`) | `UrlParams::pdf_password`, `with_pdf_password()`, `encrypt_pdf_password()` |
| Sources: base64, `plain/`, `enc/`, split; `xi://`, `s3://`, `gs://`, `abs://`, `local:///`, `ftp://`, `sftp://` | `SourceEncoding`, `split_source()`, `xi_source()`, `source_url()` |
| Query dialect `/q/`, `/q/video`, imaginary; `xi_sig` | `QueryUrlBuilder`, `sign_xi_url()` |
| HTTP QUERY on the query dialect (opt-in, off by default) | `query_method_request()`, `MediaOptions::query()`, `ClientBuilder::query_method()` and `query_mount()` |
| `/healthz`, `/readyz`, playground `api/meta` | `healthz()`, `readyz()`, `playground_meta()` |

Not covered, on purpose (as in the TypeScript and Python clients):

- other dialects' URL shapes, which their own SDKs build;
- imaginary's `sign` scheme;
- presets-only URLs;
- the playground's POST API.

The typed API cannot express three conformance cases, which the test skips with the reason: a separator longer than one character (`separator` is a `char`), a PDF password on `/video` (`VideoUrlParams` has none) and a negative byte range (`ByteRange` holds `u64`).

## Development

```sh
cd clients/rust
mise install                 # Rust and cargo-deny pinned in mise.toml
task ci                      # = task -d clients/rust ci from the root: fmt, clippy, tests, docs, licences, package
cargo test --all-features    # conformance + builder fixture + vectors + unit + doc + stand-in HTTP tests
XI_SMOKE_URL=http://127.0.0.1:8080 cargo test --all-features --test smoke -- --nocapture   # against the playground demo
```

`src/generated/` is generated from `clients/spec/spec.json` by `task clients:generate` (`clients/codegen/emitters/rust.ts`). Do not edit it; `lib.rs` keeps it out of rustfmt and the pedantic lints.

## Licences

`deny.toml` (`task licenses`, `cargo deny check`) checks the runtime closure of every feature on the supported targets against the ADR 0006 allowlist (MIT, BSD, Apache-2.0, ISC), plus RustSec advisories and banned crates (OpenSSL, native-tls, aws-lc). The documented exception:

- `Unicode-3.0`: the Unicode data tables of the ICU4X crates (`icu_*`, `zerovec`, `yoke`, `tinystr`, …) that `url` uses for IDNA host names through reqwest, and `unicode-ident` (a build-time proc-macro dependency). The licence is permissive and only asks to keep the notice.

`ring` is Apache-2.0 AND ISC. wasm32 is not a supported target: there `rustls-platform-verifier` would pull `webpki-root-certs` (CDLA-Permissive-2.0).
