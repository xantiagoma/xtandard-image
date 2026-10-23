# XtandardImage (Swift)

The official Swift client of [xtandard-image](../../README.md): typed URL builders for the imgproxy and query dialects, signing, `enc/` encryption, PDF passwords, and an async `URLSession` client with Range and HEAD. It follows the shared spec and conformance fixture of [`clients/`](../README.md) ([ADR 0013](../../docs/adr/0013-official-clients.md)).

- Swift 6 language mode with strict concurrency: builders, keys and results are `Sendable` values.
- iOS 15+, macOS 12+, tvOS 15+, watchOS 8+, visionOS 1+ and Linux (server-side Swift: Vapor, Hummingbird).
- No dependencies on Apple platforms. On Linux, swift-crypto (Apache-2.0).

## Install

SwiftPM reads the package from **this repository's root** (`Package.swift` there points at `clients/swift/`); there is no mirror repository. Versions are the repository's own `vX.Y.Z` tags, in lockstep with the server (ADR 0013 §6).

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

In Xcode: File › Add Package Dependencies…, then the repository URL. A clone of the repository is about 24 MB with history; SwiftPM caches it. Nothing is tagged for Swift yet: until the first release, depend on a branch or revision (`.package(url: …, branch: "main")`), or on a local checkout (`.package(path: "../xtandard-image")`).

Needs Swift 6.2 or later (Xcode 26): the manifest uses tools version 6.2, and the code compiles in the Swift 6 language mode.

## Use

```swift
import XtandardImage

let img = imgproxy(
    baseURL: "https://img.example.com",
    signing: try SigningKey(key: "943b421c…c6da0881", salt: "520f986b…fea309c5"))  // hex, as in XI_SIGNING_KEYS

// Mapping form: names or aliases, in URL order; a bare value or a list each; nil skips the option.
let url = try img.url(
    "https://example.com/images/cat.jpg",
    options: ["rs": [ProcessingOption.ResizeType.fit, 300, 300], "q": 80, "bl": nil],
    extension: "png")
// https://img.example.com/<signature>/rs:fit:300:300/q:80/aHR0cHM6Ly9leGFtcGxlLmNvbS9pbWFnZXMvY2F0LmpwZw.png

// Fluent form: one method per option name and alias (generated), the same URL.
let same = try img.image("https://example.com/images/cat.jpg")
    .resize(ProcessingOption.ResizeType.fit, 300, 300)
    .quality(80)
    .extension("png")
    .build()

// Chained pipelines, nulls (empty arguments) and booleans (1/0).
let chained = try img.image("s3://photos/cat.jpg")
    .rs("fill", 600, 400, nil, true)       // rs:fill:600:400::1
    .g(ProcessingOption.GravityType.fp, 0.5, 0.25)
    .pipeline()
    .bl(2)
    .build()

// /info, /video and streams.
let info = try img.infoURL("s3://photos/cat.jpg", options: ["size": true, "format": true])
let clip = try img.videoURL("s3://media/talk.mov", options: ["vs": 12, "vd": 8], extension: "webm")
let hls = try img.streamURL("s3://media/talk.mov", protocol: .hls, renditions: [360, 720])

// Query dialect (/q/, /q/video, imaginary endpoints), signed with xi_sig.
let q = queryURLs(baseURL: "https://img.example.com", signing: img.signing)
let native = try q.url("s3://photos/cat.jpg", params: ["w": 300, "f": "webp"], options: ["bl": 2])
let resized = try q.imaginaryURL(.resize, "https://example.com/a.jpg", params: ["width": 300])
```

Option names are written short by default (`rs`, `q`); `optionNames: .canonical` writes `resize`, `.given` writes what you called. Numbers are written as ECMAScript writes them (`300`, `0.5`, `1e-7`), so the URL and its signature match every other client.

### Encryption and PDF passwords

```swift
let enc = try EncryptionKey(key: "1eb5b0e9…0c18e0f1")             // XI_ENCRYPTION_KEYS; a random IV per token
let secret = imgproxy(signing: key, encryption: enc, sourceEncoding: .encrypted)
let hidden = try secret.url("https://private.example.com/a.jpg", options: ["w": 640])  // …/enc/<token>
let pdf = try secret.url("s3://docs/report.pdf", options: ["w": 800], pdfPassword: "s3cret")  // …/pdp:<token>/…
```

### HTTP client

```swift
let xi = XtandardClient(baseURL: "https://img.example.com", signing: key)
try await xi.healthz()
let ready = try await xi.readyz()                                     // .ready / .starting / .draining
let meta = try await xi.info("s3://photos/cat.jpg", options: ["size": true, "format": true])
print(meta.width ?? 0, meta.format ?? "")                             // typed InfoResponse (Codable)

let webp = try await xi.image("s3://photos/cat.jpg", options: ["w": 320], media: MediaOptions(accept: "image/avif,image/webp"))
let part = try await xi.video("s3://media/talk.mov", options: ["vd": 5], extension: "webm",
                              media: MediaOptions(range: .bytes(0, 1023)))   // 206
let head = try await xi.video("s3://media/talk.mov", options: ["vd": 5], extension: "webm",
                              media: MediaOptions(method: .head))
let manifest = try await xi.stream("s3://media/talk.mov", protocol: .hls, renditions: [360])

#if canImport(Darwin)
let (bytes, response) = try await xi.bytes(clip)                      // URLSession.AsyncBytes
for try await byte in bytes { /* … */ }
#endif
```

Errors are typed: builders throw `URLBuildError` (`error.code` is the shared code: `.unknownOption`, `.invalidArgument`, …); the client throws `ClientError`: `.build`, `.http(HTTPError)` (status, `kind`, `message`, `requestID`, `category`), `.transport` or `.decode`.

### Vapor

```swift
import Vapor
import XtandardImage

extension Application {
    var images: ImgproxyURLBuilder {
        imgproxy(baseURL: Environment.get("XI_BASE_URL")!,
                 signing: try! SigningKey(key: Environment.get("XI_KEY")!, salt: Environment.get("XI_SALT")!))
    }
}

app.get("avatar", ":id") { req -> Response in
    let url = try req.application.images.url("s3://avatars/\(req.parameters.get("id")!).jpg",
                                             options: ["rs": ["fill", 128, 128]], extension: "webp")
    return req.redirect(to: url)
}
```

## Keys in apps

Signing and `enc/` encryption are fully supported on iOS, macOS, tvOS, watchOS and visionOS, but **a key embedded in an app binary can be extracted**: the signing key signs every URL and the encryption key decrypts every `enc/` source and PDF password. In apps you ship to users, prefer:

- **unsigned URLs** with the server's `restricted` unsigned policy and a list of presets or allowed sizes (build URLs with `imgproxy()` and no `signing:`), or
- **backend-signed URLs**: your server signs, the app only fetches (`XtandardClient.media(_:)` or any image loader).

The DocC article *Keys in apps* says the same.

## Crypto per platform

| Platform | HMAC-SHA256 | AES-CBC + PKCS#7 | Random IV |
|---|---|---|---|
| iOS, macOS, tvOS, watchOS, visionOS | CryptoKit (`HMAC<SHA256>`) | CommonCrypto (`CCCrypt`, `kCCOptionPKCS7Padding`) | `SystemRandomNumberGenerator` |
| Linux (and Android, Windows) | swift-crypto `Crypto` (same API as CryptoKit, on BoringSSL) | swift-crypto `_CryptoExtras` (`AES._CBC`, PKCS#7 by default; since 3.1.0) | `SystemRandomNumberGenerator` |

Both paths run the same shared vectors. The manifest depends on swift-crypto `3.1.0..<6.0.0` (wide, so it resolves next to Vapor's own requirement) only for non-Apple platforms; SwiftPM still downloads it on Apple platforms but never builds it.

## Coverage

| Server feature | Swift |
|---|---|
| imgproxy processing options (90 with aliases), typed per option | `ProcessingOption` (names, short names, aliases, one enum per enum argument), `Options`, generated fluent methods; every name in the shared builder fixture |
| `/info` options (35) | `InfoOption`, `infoURL()`, `info()`, `XtandardClient.info()` (Codable `InfoResponse`) |
| `/video` options (50) and all 12 containers; `/video` signing | `VideoOption`, `videoURL()`, `video()`, `XtandardClient.video()` with Range and HEAD |
| HLS and DASH (`st`, `sgd`, `sgf:ts`, `@m3u8`, `@mpd`) | `streamURL()` on both builders, `XtandardClient.stream()`; ticket child URLs through `media()` |
| Password-protected PDFs (`pdp`) | `pdfPassword:` / `withPDFPassword()`, `encryptPDFPassword()` |
| Sources: base64, `plain/`, `enc/`, split; `xi://`, `s3://`, `gs://`, `abs://`, `local:///`, `ftp://`, `sftp://` | `sourceEncoding`, `splitSource`, `xiSource()`, `sourceURL()` |
| Query dialect `/q/`, `/q/video`, imaginary; `xi_sig` | `QueryURLBuilder`, `ImaginaryEndpoint`, `signXiURL()` |
| `/healthz`, `/readyz`, playground `api/meta` | `healthz()`, `readyz()`, `playgroundMeta()` |
| Streaming bodies | `bytes(_:_:)` (`URLSession.AsyncBytes`) on Apple platforms; `media()` reads the whole body everywhere |

Not covered, on purpose (as in the TypeScript client): other dialects' URL shapes, imaginary's `sign` scheme, presets-only URLs, the playground's POST API.

Conformance: 147 of 150 cases pass on macOS and Linux; 3 are skipped as not expressible by the typed API (`infoURL` and `videoURL` take one `Options`, so they cannot chain pipelines; `videoURL` has no PDF password parameter).

## Development

The manifest is the repository root's `Package.swift`; the tasks here pass `--package-path ../..` and build into `clients/swift/.build`.

```sh
task ci        # swift-format lint, build (warnings as errors), tests, licence check, DocC
task linux     # build, tests, licences and DocC in the official swift image (mise.toml pins it)
XI_SMOKE_URL=http://127.0.0.1:8080 task test   # adds the smoke test against the playground demo
task format    # rewrite the hand-written sources with swift-format
```

- **Toolchain.** mise's swift backend is experimental and installs no Apple SDKs, so macOS uses the Xcode toolchain (Xcode 26: Swift 6.2+, swift-format and docc included) and Linux the official `swift:6.3.3` image (`mise.toml`). CI does the same (`.github/workflows/client-swift.yml`).
- **Generated code.** `Sources/XtandardImage/Generated/` is written by `clients/codegen/emitters/swift.ts` (`task clients:generate`) and is not formatted or linted by hand.
- **Tests** (Swift Testing) read the three shared fixtures by relative path from the repository (`clients/spec/conformance.json`, `testdata/urls/imgproxy-builder.json`, `testdata/security/vectors.json`) and run the HTTP client against a stand-in server on POSIX sockets.
- **Docs.** `scripts/docs.sh` runs the toolchain's `docc` on the module's symbol graph with warnings as errors, so the package has no swift-docc-plugin dependency.
- **Licences.** `scripts/check-licenses.sh` checks the resolved closure: swift-crypto and swift-asn1 (Apache-2.0). **Exception:** swift-crypto vendors the eXtended Keccak Code Package (`CXKCP`, SHA-3 and ML-KEM) under CC0-1.0, a public-domain dedication; it is linked on non-Apple platforms only. BoringSSL inside swift-crypto is Apache-2.0.
- **Registry.** SwiftPM has no central registry here: the package is its Git URL (identity `xtandard-image`, product `XtandardImage`). After the first release it can be submitted to the Swift Package Index by URL.
