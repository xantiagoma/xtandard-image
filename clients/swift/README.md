<p align="center"><img src="https://raw.githubusercontent.com/xantiagoma/xtandard-image/main/docs/assets/logo-128.png" alt="xtandard-image" width="128" height="128"></p>

# xtandard-image for Swift

**Typed URL builder, signing and async client for the xtandard-image server, for apps and servers.**

[![Swift Package Index: pending](https://img.shields.io/badge/Swift%20Package%20Index-pending-lightgrey)](https://github.com/xantiagoma/xtandard-image/blob/main/docs/RELEASE.md) <!-- registry-status: pending -->
[![license: MIT](https://img.shields.io/badge/license-MIT-blue)](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

`XtandardImage` is the Swift client for [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme), an open-source media server that covers your application's image and video needs, built on libvips and FFmpeg. The package builds and signs the URLs your code sends to the server, and wraps its `/info`, media and health endpoints with `URLSession`; it does not process images itself. To run a server, see [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md), or start one with signing keys:

```sh
export XI_SIGNING_KEY="$(openssl rand -hex 32)" XI_SIGNING_SALT="$(openssl rand -hex 32)"
docker run -d --name xtandard-image -p 8080:8080 \
  -e XI_SOURCES=web -e XI_SOURCES_WEB_TYPE=http -e XI_SOURCES_WEB_ALLOW=images.unsplash.com \
  -e XI_SIGNING_KEYS="$XI_SIGNING_KEY" -e XI_SIGNING_SALTS="$XI_SIGNING_SALT" \
  ghcr.io/xantiagoma/xtandard-image:0.1.1
```

## Install

The package is this repository: its root `Package.swift` serves the `XtandardImage` product, and versions are the repository's `vX.Y.Z` tags.

```swift
// Package.swift
dependencies: [
    .package(url: "https://github.com/xantiagoma/xtandard-image", from: "0.1.0"),
],
targets: [
    .target(name: "App", dependencies: [
        .product(name: "XtandardImage", package: "xtandard-image"),
    ]),
]
```

In Xcode: File › Add Package Dependencies…, then the repository URL.

Not published yet: publishing to the Swift Package Index is being set up. Until then, install from the repository: <!-- registry-status: pending -->

```swift
.package(url: "https://github.com/xantiagoma/xtandard-image", branch: "main"),
```

## Quick start

Build a signed URL with the keys the server was started with:

```swift
import Foundation
import XtandardImage

let env = ProcessInfo.processInfo.environment
let key = try SigningKey(key: env["XI_SIGNING_KEY"]!, salt: env["XI_SIGNING_SALT"]!)
let img = imgproxy(baseURL: "http://localhost:8080", signing: key)

let url = try img.image("https://images.unsplash.com/photo-1506744038136-46273834b3fb")
    .resize("fill", 1200, 630)
    .gravity("sm")
    .extension("jpg")
    .build()
// http://localhost:8080/<signature>/rs:fill:1200:630/g:sm/aHR0cHM6….jpg
```

Ask the server about the source with the HTTP client:

```swift
let xi = XtandardClient(baseURL: "http://localhost:8080", signing: key)
let info = try await xi.info("https://images.unsplash.com/photo-1506744038136-46273834b3fb")
print(info.format ?? "", info.width ?? 0, info.height ?? 0)  // jpeg 7360 4912
```

Sign URLs on your server. In apps, read [Keys in apps](#keys-in-apps) first.

## Usage

### Building URLs

There are two equivalent forms, an `Options` dictionary literal and a fluent chain with one method per option name and alias:

```swift
try img.url("s3://photos/cat.jpg", options: ["rs": ["fill", 300, 400], "g": "sm", "q": 80], extension: "webp")
try img.image("s3://photos/cat.jpg").resize(ProcessingOption.ResizeType.fill, 300, 400).g("sm").quality(80).extension("webp").build()
// both: http://localhost:8080/<signature>/rs:fill:300:400/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp
```

A value is a bare argument or a list; `nil` as an argument leaves it empty, and `nil` as a value skips the option. Builders, keys and results are `Sendable` values. The options themselves are documented in [URL dialects §3.2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#32-processing-options--tr).

`imgproxy(…)` also takes `separator` (default `:`), `unsignedSignature` (`insecure`, `unsafe` or `_`) and `optionNames` (`.short` by default, `.canonical` or `.given`).

### Chained pipelines

`pipelines:`, or `pipeline()` in the fluent form, writes `/-/` between pipelines:

```swift
try img.url(src, pipelines: [["rs": ["fit", 500, 500]], ["bl": 2]])
try img.image(src).rs("fit", 500, 500).pipeline().bl(2).build()
```

### Sources

| `sourceEncoding` | URL |
|---|---|
| `.base64` (default) | `/…/czM6Ly9waG90b3MvY2F0LmpwZw.webp`; `splitSource: 16` splits it into segments |
| `.plain` | `/…/plain/https://example.com/a%20b.jpg@webp`, escaped for you |
| `.encrypted` | `/…/enc/<token>.webp`; needs `encryption` (below) |

Set it on the builder or per URL (`sourceEncoding:`). `try xiSource("photos", "2026/cat.jpg")` writes a named source (`xi://photos/2026/cat.jpg`) and `try sourceURL(.s3, "bucket", "cat.jpg")` a storage URL.

### `/info`

```swift
try img.infoURL(src, options: ["size": true, "format": true])
try img.info(src).size(true).format(true).build()
```

### Video and streams

`/video` URLs use the video option catalogue; the extension is the container (`mp4`, `webm`, `mp3`, `png` for a waveform, …). `streamURL` builds HLS and DASH manifests, whose segments a player fetches without any key:

```swift
try img.video("s3://media/talk.mov").resize("fill", 640, 360).start(12).duration(5).extension("webm").build()
try img.streamURL("s3://media/talk.mov", protocol: .hls, renditions: [360, 720], segmentDuration: 4)
```

The server needs the `full` or `gpl` image for video. Options and containers: [URL dialects §3.8](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#38-video-transformations-wp-122-wp-123) and [§3.10](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#310-hls-and-dash-streams-wp-126-every-dialect).

### Query dialect

`queryURLs(…)` builds `/q/` URLs (native keys plus imgproxy options in `o=`), `/q/video` and imaginary endpoints, signed with `xi_sig`. The server needs `XI_DIALECTS_QUERY_ENABLED=true`.

```swift
let q = queryURLs(baseURL: "https://img.example.com", signing: key)
try q.url("s3://photos/cat.jpg", params: ["w": 300, "fit": "cover"], options: ["bl": 2], extension: "webp")
// https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&fit=cover&o=bl:2&format=webp&xi_sig=…
try q.imaginaryURL(.smartcrop, "s3://photos/cat.jpg", params: ["width": 300, "height": 300])
```

`queryMethodRequest(url)` turns a query URL into an HTTP QUERY request, and `XtandardClient(…, queryMethod: true)` sends one. See [URL dialects §4](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#4-query-dialect-native--imaginary).

### Signing and encryption

`SigningKey(key:salt:)` takes the server's hex key and salt (`XI_SIGNING_KEYS` / `XI_SIGNING_SALTS`, or imgproxy's `IMGPROXY_KEY` / `IMGPROXY_SALT`). `EncryptionKey(key:)` takes the first `XI_ENCRYPTION_KEYS` entry, for `enc/` sources and password-protected PDFs:

```swift
let enc = try EncryptionKey(key: env["XI_ENCRYPTION_KEY"]!)  // a random IV per token unless you pass iv:
let secret = imgproxy(baseURL: "http://localhost:8080", signing: key, encryption: enc)
try secret.url("s3://private/cat.jpg", options: ["w": 300], sourceEncoding: .encrypted)
try secret.url("s3://docs/contract.pdf", options: ["rs": ["fit", 800, 0]], pdfPassword: "s3cret")
```

`sign`, `signImgproxyPath`, `signXiURL`, `signXiQuery`, `encryptSource` and `encryptPDFPassword` are public too; `signXiURL` adds an `xi_sig` to a URL of any other dialect. Schemes: [URL dialects §2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#2-signing-schemes-at-a-glance).

### Keys in apps

Signing and encryption work on every platform, but **a key embedded in an app can be extracted**: the signing key signs every URL and the encryption key decrypts every `enc/` source. In apps you ship to users, prefer unsigned URLs with the server's `restricted` unsigned policy (presets or allowed sizes; build with `imgproxy()` and no `signing:`), or URLs signed by your backend that the app only fetches. The DocC article *Keys in apps* has the details.

### HTTP client

`XtandardClient` uses `URLSession` with `async throws(ClientError)`: `healthz`, `readyz`, `info`, `image`, `video`, `stream`, `media` and `playgroundMeta`, plus `bytes(_:)` for streamed bodies on Apple platforms:

```swift
let res = try await xi.image(src, options: ["w": 300], media: MediaOptions(accept: "image/avif,image/webp"))
let part = try await xi.video(src, options: ["vd": 5], extension: "webm", media: MediaOptions(range: .bytes(0, 65535)))
```

### Errors

Builders throw `URLBuildError`, whose `code` is the error code (`.unknownOption`, `.invalidArgument`, `.invalidSource`, …). The client throws `ClientError`: `.http(HTTPError)` for a non-2xx answer, with the server's `status`, `kind` and `requestID`; `.transport` when no answer came; `.build` and `.decode`.

```swift
do {
    _ = try await xi.info("https://example.com/a.jpg")
} catch {
    if case .http(let e) = error { print(e.status, e.kind, e.requestID ?? "") }  // 403 forbidden …
}
```

## Compatibility

- Swift 6.2 or later (Xcode 26), in the Swift 6 language mode with strict concurrency.
- iOS 15, macOS 12, tvOS 15, watchOS 8, visionOS 1, and Linux for server-side Swift (Vapor, Hummingbird).
- No dependencies on Apple platforms (CryptoKit and CommonCrypto); swift-crypto (Apache-2.0) elsewhere. swift-crypto vendors the eXtended Keccak Code Package under CC0-1.0, a public-domain dedication, linked on non-Apple platforms only.
- The package version is the server version: tag `v0.2.1` matches `ghcr.io/xantiagoma/xtandard-image:0.2.1`. Versions follow [ZeroVer](https://0ver.org): a minor bump may break the API, and a patch does not.

## Links

- [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme) and [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md)
- [URL dialects](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md): every option and signing scheme
- [Playground](https://github.com/xantiagoma/xtandard-image/blob/main/docs/PLAYGROUND.md): build URLs in a browser and copy the Swift code
- [Changelog](https://github.com/xantiagoma/xtandard-image/blob/main/CHANGELOG.md)
- [Contributing](https://github.com/xantiagoma/xtandard-image/blob/main/CONTRIBUTING.md) and [the official clients](https://github.com/xantiagoma/xtandard-image/blob/main/clients/README.md)
- [Security policy](https://github.com/xantiagoma/xtandard-image/blob/main/SECURITY.md)
- Licence: [MIT](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

A personal project, provided as is under the MIT licence: see the [disclaimer](https://github.com/xantiagoma/xtandard-image#disclaimer).

Design: every official client is generated from one spec and passes one conformance fixture ([ADR 0013](https://github.com/xantiagoma/xtandard-image/blob/main/docs/adr/0013-official-clients.md)).
