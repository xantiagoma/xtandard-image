<p align="center"><img src="https://raw.githubusercontent.com/xantiagoma/xtandard-image/main/docs/assets/logo-128.png" alt="xtandard-image" width="128" height="128"></p>

# xtandard-image for Kotlin Multiplatform

**Typed URL builder, signing and Ktor client for the xtandard-image server, on the JVM, Android, iOS and macOS.**

[![Maven Central: pending](https://img.shields.io/badge/Maven%20Central-pending-lightgrey)](https://github.com/xantiagoma/xtandard-image/blob/main/docs/RELEASE.md) <!-- registry-status: pending -->
[![license: MIT](https://img.shields.io/badge/license-MIT-blue)](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

`io.github.xantiagoma:xtandard-image-kmp` is the Kotlin Multiplatform client for [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme), an open-source media server that covers your application's image and video needs, built on libvips and FFmpeg. The library builds the URLs your code or app sends to the server, signs them where that is safe, and wraps its `/info` and health endpoints; it does not process images itself. To run a server, see [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md), or start one with signing keys:

```sh
export XI_SIGNING_KEY="$(openssl rand -hex 32)" XI_SIGNING_SALT="$(openssl rand -hex 32)"
docker run -d --name xtandard-image -p 8080:8080 \
  -e XI_SOURCES=web -e XI_SOURCES_WEB_TYPE=http -e XI_SOURCES_WEB_ALLOW=images.unsplash.com \
  -e XI_SIGNING_KEYS="$XI_SIGNING_KEY" -e XI_SIGNING_SALTS="$XI_SIGNING_SALT" \
  ghcr.io/xantiagoma/xtandard-image:0.1.1
```

## Install

```kotlin
// build.gradle.kts of a KMP module
kotlin {
    sourceSets {
        commonMain.dependencies {
            implementation("io.github.xantiagoma:xtandard-image-kmp:0.x.y")
        }
    }
}

// or of a JVM or Android module
dependencies {
    implementation("io.github.xantiagoma:xtandard-image-kmp:0.x.y")
}
```

Not published yet: publishing to Maven Central is being set up. Until then, install from the repository: <!-- registry-status: pending -->

```sh
git clone --depth 1 https://github.com/xantiagoma/xtandard-image
cd xtandard-image/clients/kotlin && gradle publishToMavenLocal   # JDK 21 and Gradle 9.8 (or `mise install` there)
```

That puts `0.1.0-SNAPSHOT` in your local Maven repository; depend on that version, with `mavenLocal()` in your repositories. The Apple targets need macOS with Xcode.

## Quick start

Build a signed URL with the keys the server was started with (on a server; for apps, read [Keys in apps](#keys-in-apps) first):

```kotlin
import io.github.xantiagoma.xtandard.image.kmp.*

val signing = SigningKey.of(System.getenv("XI_SIGNING_KEY"), System.getenv("XI_SIGNING_SALT"))
val img = ImgproxyUrlBuilder(baseUrl = "http://localhost:8080", signing = signing)

val url = img.image("https://images.unsplash.com/photo-1506744038136-46273834b3fb")
    .resize("fill", 1200, 630)
    .gravity("sm")
    .extension("jpg")
    .build()
// http://localhost:8080/<signature>/rs:fill:1200:630/g:sm/aHR0cHM6….jpg
```

Ask the server about the source with the HTTP client (every call is a `suspend` function):

```kotlin
XtandardClient("http://localhost:8080", signing = signing).use { xi ->
    val info = xi.info("https://images.unsplash.com/photo-1506744038136-46273834b3fb")
    println("${info.format} ${info.width} ${info.height}") // jpeg 7360 4912
}
```

## Keys in apps

Signing and encryption work on every target, and both are optional. A key shipped inside a mobile or desktop app can be extracted from the binary: a signing key then signs **any** URL, and an encryption key decrypts every `enc/` source and PDF password made with it. In apps, use one of these instead:

1. **Unsigned URLs under the server's `restricted` policy** (the default): unsigned URLs may only use the sizes and qualities in the server's allowlists, and presets. Build them with `ImgproxyUrlBuilder()` and no key. See [Configuration](https://github.com/xantiagoma/xtandard-image/blob/main/docs/CONFIG.md).
2. **URLs signed by your backend**, with this library or any other official client. The app only fetches them, for example with `XtandardClient.media(url)`.

## Usage

### Building URLs

There are three equivalent forms: an options list, a DSL block and a fluent chain, each with one typed method per option name and alias:

```kotlin
val img = ImgproxyUrlBuilder(baseUrl = "https://img.example.com")

img.url("s3://photos/cat.jpg", processingOptions { resize("fill", 300, 400); gravity("sm"); quality(80) }, extension = "webp")
img.image("s3://photos/cat.jpg") { resize("fill", 300, 400); g("sm"); quality(80); extension("webp") }
img.image("s3://photos/cat.jpg").resize("fill", 300, 400).g("sm").quality(80).extension("webp").build()
// all three: https://img.example.com/insecure/rs:fill:300:400/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp
```

The option methods are generated from the server's catalogue, with one overload per argument shape; numbers are `Number`, `null` leaves an argument empty, and `option(name, args...)` adds any option by name. Each method's KDoc lists the URL names, the argument values and ranges. The options themselves are documented in [URL dialects §3.2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#32-processing-options--tr). Builders and keys are immutable and thread-safe.

Builder settings: `separator` (default `:`), `unsignedSignature` (`insecure`, `unsafe` or `_`), `optionNames` (`SHORT` by default, `CANONICAL` or `GIVEN`) and `splitSource`.

### Chained pipelines

A list of options lists, or `pipeline()` in the chain, writes `/-/` between pipelines:

```kotlin
img.url("s3://photos/cat.jpg", listOf(processingOptions { rs("fit", 500, 500) }, processingOptions { bl(2) }))
img.image("s3://photos/cat.jpg").rs("fit", 500, 500).pipeline().bl(2).build()
```

### Sources

| `SourceEncoding` | URL |
|---|---|
| `BASE64` (default) | `/…/czM6Ly9waG90b3MvY2F0LmpwZw.webp` |
| `PLAIN` | `/…/plain/https://example.com/a%20b.jpg`, escaped for you |
| `ENCRYPTED` | `/…/enc/<token>`; needs `encryption` (below) |

Set it on the builder or per call (`img.url(source, processingOptions { w(300) }, sourceEncoding = SourceEncoding.PLAIN)`). `Sources.xiSource("photos", "2026/cat.jpg")` writes a named source (`xi://photos/2026/cat.jpg`) and `Sources.sourceUrl("s3", "bucket", "cat.jpg")` a storage URL.

### `/info`

```kotlin
img.infoUrl("s3://photos/cat.jpg", infoOptions { size(true); format(true) })
img.info("s3://photos/cat.jpg").size(true).format(true).build()
```

### Video and streams

`/video` URLs use the video option catalogue; the extension is the container (`mp4`, `webm`, `mp3`, `png` for a waveform, …). `streamUrl` builds HLS and DASH manifests, whose segments a player fetches without any key:

```kotlin
img.video("s3://media/talk.mov") { rs("fill", 640, 360); vs(12); vd(5); extension("webm") }
img.streamUrl("s3://media/talk.mov", StreamParams.hls(renditions = listOf(360, 720), segmentDuration = 4))
```

The server needs the `full` or `gpl` image for video. Options and containers: [URL dialects §3.8](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#38-video-transformations-wp-122-wp-123) and [§3.10](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#310-hls-and-dash-streams-wp-126-every-dialect).

### Query dialect

`QueryUrlBuilder` builds `/q/` URLs (native keys plus imgproxy options in `o=`), `/q/video` and imaginary endpoints, signed with `xi_sig`. The server needs `XI_DIALECTS_QUERY_ENABLED=true`.

```kotlin
val q = QueryUrlBuilder(baseUrl = "https://img.example.com", signing = signing)
q.url("s3://photos/cat.jpg", queryParams { w(300); fit("cover") }, processingOptions { bl(2) }, extension = "webp")
// https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&fit=cover&o=bl:2&format=webp&xi_sig=…
q.imaginaryUrl(ImaginaryEndpoint.SMARTCROP, "s3://photos/cat.jpg", imaginaryParams { width(300); height(300) })
```

See [URL dialects §4](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#4-query-dialect-native--imaginary).

### Signing and encryption

`SigningKey.of(key, salt)` takes the server's hex key and salt (`XI_SIGNING_KEYS` / `XI_SIGNING_SALTS`, or imgproxy's `IMGPROXY_KEY` / `IMGPROXY_SALT`). `EncryptionKey.of(key)` takes the first `XI_ENCRYPTION_KEYS` entry, for `enc/` sources and password-protected PDFs:

```kotlin
val img = ImgproxyUrlBuilder(baseUrl = baseUrl, signing = signing, encryption = EncryptionKey.of(System.getenv("XI_ENCRYPTION_KEY")))
img.url("s3://private/cat.jpg", processingOptions { w(300) }, sourceEncoding = SourceEncoding.ENCRYPTED)
img.url("s3://docs/contract.pdf", processingOptions { rs("fit", 800, 0) }, pdfPassword = "s3cret")
```

Each `enc/` token gets a random IV. `Signing` (`sign`, `signImgproxyPath`, `signXiUrl`, `signXiQuery`) and `Encryption` (`encryptSource`, `encryptPdfPassword`) expose the primitives; `Signing.signXiUrl` adds an `xi_sig` to a URL of any other dialect. Schemes: [URL dialects §2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#2-signing-schemes-at-a-glance). The crypto comes from the platform: `javax.crypto` on the JVM and Android, CommonCrypto on Apple platforms.

### HTTP client

`XtandardClient` runs on [Ktor](https://ktor.io), with OkHttp on the JVM and Android and Darwin (`URLSession`) on Apple platforms; pass your own with `httpClient = HttpClient(…) { … }`. It has `healthz`, `readyz`, `info`, `image`, `video`, `stream`, `media` (with `Range` and `HEAD`), `mediaStream` (the body as a `ByteReadChannel`) and `playgroundMeta`:

```kotlin
val res = xi.image("s3://photos/cat.jpg", processingOptions { w(300) }, request = MediaRequest(accept = "image/avif,image/webp"))
val part = xi.video("s3://media/talk.mov", videoOptions { vd(5) }, "webm", request = MediaRequest(range = ByteRange.Between(0, 65535)))
```

### Errors

The builders throw `UrlBuildException`, whose `code` (`ErrorCode.UNKNOWN_OPTION`, `INVALID_ARGUMENT`, `INVALID_SOURCE`, …) names the fault. The client throws an `HttpException` subclass by status (`HttpException.Forbidden`, `NotFound`, `TooManyRequests`, …) with the server's `status`, `kind` and `requestId`, and `TransportException` when the connection fails. All three extend the sealed `XtandardException`.

```kotlin
try {
    xi.info("https://example.com/a.jpg")
} catch (e: HttpException) {
    println("${e.status} ${e.kind} ${e.requestId}") // 403 forbidden …
}
```

## Compatibility

| Target | Use | HTTP engine |
|---|---|---|
| `jvm` (Java 11 bytecode) | servers, desktop, and Android apps, which consume the JVM variant | OkHttp |
| `iosArm64`, `iosSimulatorArm64` | iOS apps and shared KMP code | Darwin |
| `macosArm64` | macOS apps and tools | Darwin |

- Built with Kotlin 2.4. Runtime dependencies: Ktor client, kotlinx.serialization and kotlinx.coroutines.
- A JVM server in Java can use the dependency-free [Java client](https://github.com/xantiagoma/xtandard-image/blob/main/clients/java/README.md) instead; Android and Kotlin/Native need this one. Swift-only apps can use the [Swift client](https://github.com/xantiagoma/xtandard-image/blob/main/clients/swift/README.md).
- The library version is the server version: `0.2.1` matches `ghcr.io/xantiagoma/xtandard-image:0.2.1`. Versions follow [ZeroVer](https://0ver.org): a minor bump may break the API, a patch does not.

## Links

- [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme) and [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md)
- [URL dialects](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md): every option and signing scheme
- [Playground](https://github.com/xantiagoma/xtandard-image/blob/main/docs/PLAYGROUND.md): build URLs in a browser and copy the Kotlin code
- [Changelog](https://github.com/xantiagoma/xtandard-image/blob/main/CHANGELOG.md)
- [Contributing](https://github.com/xantiagoma/xtandard-image/blob/main/CONTRIBUTING.md) and [the official clients](https://github.com/xantiagoma/xtandard-image/blob/main/clients/README.md)
- [Security policy](https://github.com/xantiagoma/xtandard-image/blob/main/SECURITY.md)
- Licence: [MIT](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

A personal project, provided as is under the MIT licence: see the [disclaimer](https://github.com/xantiagoma/xtandard-image#disclaimer).

Design: every official client is generated from one spec and passes one conformance fixture ([ADR 0013](https://github.com/xantiagoma/xtandard-image/blob/main/docs/adr/0013-official-clients.md)).
