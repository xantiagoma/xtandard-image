<p align="center"><img src="https://raw.githubusercontent.com/xantiagoma/xtandard-image/main/docs/assets/logo-128.png" alt="xtandard-image" width="128" height="128"></p>

# xtandard-image for Java

**Typed URL builder, signing and `java.net.http` client for the xtandard-image server.**

[![Maven Central: pending](https://img.shields.io/badge/Maven%20Central-pending-lightgrey)](https://github.com/xantiagoma/xtandard-image/blob/main/docs/RELEASE.md) <!-- registry-status: pending -->
[![license: MIT](https://img.shields.io/badge/license-MIT-blue)](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

`io.github.xantiagoma:xtandard-image` is the Java client for [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme), an open-source media server that covers your application's image and video needs, built on libvips and FFmpeg. The library builds and signs the URLs your code sends to the server, and wraps its `/info` and health endpoints; it does not process images itself. To run a server, see [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md), or start one with signing keys:

```sh
export XI_SIGNING_KEY="$(openssl rand -hex 32)" XI_SIGNING_SALT="$(openssl rand -hex 32)"
docker run -d --name xtandard-image -p 8080:8080 \
  -e XI_SOURCES=web -e XI_SOURCES_WEB_TYPE=http -e XI_SOURCES_WEB_ALLOW=images.unsplash.com \
  -e XI_SIGNING_KEYS="$XI_SIGNING_KEY" -e XI_SIGNING_SALTS="$XI_SIGNING_SALT" \
  ghcr.io/xantiagoma/xtandard-image:0.1.1
```

## Install

Maven:

```xml
<dependency>
  <groupId>io.github.xantiagoma</groupId>
  <artifactId>xtandard-image</artifactId>
  <version>0.x.y</version>
</dependency>
```

Gradle:

```kotlin
implementation("io.github.xantiagoma:xtandard-image:0.x.y")
```

Not published yet: publishing to Maven Central is being set up. Until then, install from the repository: <!-- registry-status: pending -->

```sh
git clone --depth 1 https://github.com/xantiagoma/xtandard-image
cd xtandard-image/clients/java && mvn install -DskipTests   # JDK 21 and Maven 3.9 (or `mise install` there)
```

That puts `0.1.0-SNAPSHOT` in your local Maven repository; depend on that version, with `mavenLocal()` in Gradle. JPMS users add `requires io.github.xantiagoma.xtandard.image;`.

## Quick start

Build a signed URL with the keys the server was started with:

```java
import io.github.xantiagoma.xtandard.image.*;

SigningKey signing = SigningKey.of(System.getenv("XI_SIGNING_KEY"), System.getenv("XI_SIGNING_SALT"));
ImgproxyUrlBuilder img = ImgproxyUrlBuilder.builder().baseUrl("http://localhost:8080").signing(signing).build();

String url = img.image("https://images.unsplash.com/photo-1506744038136-46273834b3fb")
    .resize("fill", 1200, 630)
    .gravity("sm")
    .extension("jpg")
    .build();
// http://localhost:8080/<signature>/rs:fill:1200:630/g:sm/aHR0cHM6….jpg
```

Ask the server about the source with the HTTP client:

```java
XtandardClient xi = XtandardClient.builder("http://localhost:8080").signing(signing).build();

InfoResponse info = xi.info("https://images.unsplash.com/photo-1506744038136-46273834b3fb");
System.out.println(info.format() + " " + info.width() + " " + info.height()); // jpeg 7360 4912
```

Sign URLs on a server only: the signing key signs any URL.

## Usage

### Building URLs

There are two equivalent forms, an options list and a fluent chain. Both have one typed method per option name and alias:

```java
ImgproxyUrlBuilder img = ImgproxyUrlBuilder.builder().baseUrl("https://img.example.com").build();

img.url("s3://photos/cat.jpg", ProcessingOptions.create().resize("fill", 300, 400).gravity("sm").quality(80), "webp");
img.image("s3://photos/cat.jpg").resize("fill", 300, 400).g("sm").quality(80).extension("webp").build();
// both: https://img.example.com/insecure/rs:fill:300:400/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp
```

The option methods are generated from the server's catalogue, with one overload per argument shape; `null` leaves an argument empty, and `option(name, args...)` adds any option by name. Each method's Javadoc lists the URL names, the argument values and ranges. The options themselves are documented in [URL dialects §3.2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#32-processing-options--tr). Builders and keys are immutable and thread-safe; options lists and chains are mutable, like `StringBuilder`.

Builder settings: `separator` (default `:`), `unsignedSignature` (`insecure`, `unsafe` or `_`), `optionNames` (`SHORT` by default, `CANONICAL` or `GIVEN`) and `splitSource`.

### Chained pipelines

A list of options lists, or `pipeline()` in the fluent form, writes `/-/` between pipelines:

```java
img.url("s3://photos/cat.jpg",
    List.of(ProcessingOptions.create().rs("fit", 500, 500), ProcessingOptions.create().bl(2)), UrlParams.of());
img.image("s3://photos/cat.jpg").rs("fit", 500, 500).pipeline().bl(2).build();
```

### Sources

| `SourceEncoding` | URL |
|---|---|
| `BASE64` (default) | `/…/czM6Ly9waG90b3MvY2F0LmpwZw.webp` |
| `PLAIN` | `/…/plain/https://example.com/a%20b.jpg`, escaped for you |
| `ENCRYPTED` | `/…/enc/<token>`; needs `encryption` (below) |

Set it on the builder (`sourceEncoding(…)`) or per call (`UrlParams.of().withSourceEncoding(SourceEncoding.PLAIN)`). `Sources.xiSource("photos", "2026/cat.jpg")` writes a named source (`xi://photos/2026/cat.jpg`) and `Sources.sourceUrl("s3", "bucket", "cat.jpg")` a storage URL.

### `/info`

```java
img.infoUrl("s3://photos/cat.jpg", InfoOptions.create().size(true).format(true));
img.info("s3://photos/cat.jpg").size(true).format(true).build();
```

### Video and streams

`/video` URLs use the video option catalogue; the extension is the container (`mp4`, `webm`, `mp3`, `png` for a waveform, …). `streamUrl` builds HLS and DASH manifests, whose segments a player fetches without any key:

```java
img.video("s3://media/talk.mov").rs("fill", 640, 360).vs(12).vd(5).extension("webm").build();
img.streamUrl("s3://media/talk.mov", StreamParams.hls().withRenditions(360, 720).withSegmentDuration(4));
```

The server needs the `full` or `gpl` image for video. Options and containers: [URL dialects §3.8](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#38-video-transformations-wp-122-wp-123) and [§3.10](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#310-hls-and-dash-streams-wp-126-every-dialect).

### Query dialect

`QueryUrlBuilder` builds `/q/` URLs (native keys plus imgproxy options in `o=`), `/q/video` and imaginary endpoints, signed with `xi_sig`. The server needs `XI_DIALECTS_QUERY_ENABLED=true`.

```java
QueryUrlBuilder q = QueryUrlBuilder.builder().baseUrl("https://img.example.com").signing(signing).build();
q.url("s3://photos/cat.jpg", QueryParams.create().w(300).fit("cover"),
    List.of(ProcessingOptions.create().bl(2)), UrlParams.ofExtension("webp"));
// https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&fit=cover&o=bl:2&format=webp&xi_sig=…
q.imaginaryUrl(ImaginaryEndpoint.SMARTCROP, "s3://photos/cat.jpg", ImaginaryParams.create().width(300).height(300));
```

See [URL dialects §4](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#4-query-dialect-native--imaginary).

### Signing and encryption

`SigningKey.of(key, salt)` takes the server's hex key and salt (`XI_SIGNING_KEYS` / `XI_SIGNING_SALTS`, or imgproxy's `IMGPROXY_KEY` / `IMGPROXY_SALT`). `EncryptionKey.of(key)` takes the first `XI_ENCRYPTION_KEYS` entry, for `enc/` sources and password-protected PDFs:

```java
ImgproxyUrlBuilder img = ImgproxyUrlBuilder.builder().baseUrl(baseUrl).signing(signing)
    .encryption(EncryptionKey.of(System.getenv("XI_ENCRYPTION_KEY"))).build();
img.url("s3://private/cat.jpg", ProcessingOptions.create().w(300),
    UrlParams.of().withSourceEncoding(SourceEncoding.ENCRYPTED));
img.url("s3://docs/contract.pdf", ProcessingOptions.create().rs("fit", 800, 0), UrlParams.of().withPdfPassword("s3cret"));
```

Each `enc/` token gets a random IV. `Signing` (`sign`, `signImgproxyPath`, `signXiUrl`, `signXiQuery`) and `Encryption` (`encryptSource`, `encryptPdfPassword`) expose the primitives; `Signing.signXiUrl` adds an `xi_sig` to a URL of any other dialect. Schemes: [URL dialects §2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#2-signing-schemes-at-a-glance).

### HTTP client

`XtandardClient` runs on `java.net.http.HttpClient` (pass your own with `.httpClient(…)`). It has `healthz`, `readyz`, `info`, `image`, `video`, `stream`, `media` (with `Range` and `HEAD`) and `playgroundMeta`, and `xi.async()` has the same calls returning `CompletableFuture`s:

```java
MediaResponse<byte[]> res = xi.image("s3://photos/cat.jpg", ProcessingOptions.create().w(300),
    UrlParams.of(), MediaRequest.get().withAccept("image/avif,image/webp"));
xi.async().info("s3://photos/cat.jpg").thenAccept(i -> System.out.println(i.width()));
```

### Errors

The builders throw `UrlBuildException`, whose `code()` (`ErrorCode.UNKNOWN_OPTION`, `INVALID_ARGUMENT`, `INVALID_SOURCE`, …) names the fault. The client throws an `HttpException` subclass by status (`HttpException.Forbidden`, `NotFound`, `TooManyRequests`, …) with the server's `status()`, `kind()` and `requestId()`, and `TransportException` when the connection fails. All three extend the sealed, unchecked `XtandardException`.

```java
try {
  xi.info("https://example.com/a.jpg");
} catch (HttpException e) {
  System.out.println(e.status() + " " + e.kind() + " " + e.requestId()); // 403 forbidden …
}
```

## Compatibility

- Java 17 or later (tested on 17, 21 and 25). A JPMS module with no runtime dependencies: `javax.crypto`, `java.net.http` and a small internal JSON parser. JSpecify nullness annotations are compile-only, so Kotlin sees non-platform types.
- On Android and Kotlin/Native, use the [Kotlin Multiplatform client](https://github.com/xantiagoma/xtandard-image/blob/main/clients/kotlin/README.md): Android has no `java.net.http`.
- The library version is the server version: `0.2.1` matches `ghcr.io/xantiagoma/xtandard-image:0.2.1`. Versions follow [ZeroVer](https://0ver.org): a minor bump may break the API, a patch does not.

## Links

- [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme) and [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md)
- [URL dialects](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md): every option and signing scheme
- [Playground](https://github.com/xantiagoma/xtandard-image/blob/main/docs/PLAYGROUND.md): build URLs in a browser and copy the Java code
- [Changelog](https://github.com/xantiagoma/xtandard-image/blob/main/CHANGELOG.md)
- [Contributing](https://github.com/xantiagoma/xtandard-image/blob/main/CONTRIBUTING.md) and [the official clients](https://github.com/xantiagoma/xtandard-image/blob/main/clients/README.md)
- [Security policy](https://github.com/xantiagoma/xtandard-image/blob/main/SECURITY.md)
- Licence: [MIT](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

A personal project, provided as is under the MIT licence: see the [disclaimer](https://github.com/xantiagoma/xtandard-image#disclaimer).

Design: every official client is generated from one spec and passes one conformance fixture ([ADR 0013](https://github.com/xantiagoma/xtandard-image/blob/main/docs/adr/0013-official-clients.md)).
