# xtandard-image for Java

The official Java client of [xtandard-image](https://github.com/xantiagoma/xtandard-image) ([ADR 0013](../../docs/adr/0013-official-clients.md)). It has:

- typed URL builders for the imgproxy dialect (processing, `/info`, `/video`, HLS and DASH) and the query dialect (`/q/`, `/q/video`, imaginary endpoints), in an object form and a fluent form;
- imgproxy and `xi_sig` signing;
- `enc/` source and PDF password encryption;
- an HTTP client on `java.net.http.HttpClient`, synchronous and asynchronous (`CompletableFuture`).

It needs Java 17 or later. It is a JPMS module (`io.github.xantiagoma.xtandard.image`) with **no runtime dependencies**: `javax.crypto` for HMAC and AES, `java.net.http` for HTTP, and a small internal JSON parser for `/info` answers. Everything is `@NullMarked` with [JSpecify](https://jspecify.dev) annotations, so Kotlin sees `String` and `Long?` instead of platform types. The annotations are compile-only: Kotlin reads them from the class files, and you need no extra dependency.

It is not on Maven Central yet. The coordinates `io.github.xantiagoma:xtandard-image` will follow the server's release tags. The namespace `io.github.xantiagoma` had no artifacts on Maven Central when this client was written (checked 2026-10-15).

**Build and sign URLs on a server.** The signing key signs every URL, and the encryption key decrypts every `enc/` source and PDF password.

## Install (after the first release)

```xml
<dependency>
  <groupId>io.github.xantiagoma</groupId>
  <artifactId>xtandard-image</artifactId>
  <version>0.x.y</version>
</dependency>
```

```kotlin
implementation("io.github.xantiagoma:xtandard-image:0.x.y")
```

Until then, `mvn install` in this directory puts `0.1.0-SNAPSHOT` in your local repository. Module users add `requires io.github.xantiagoma.xtandard.image;`.

## URL builder

```java
import io.github.xantiagoma.xtandard.image.*;

ImgproxyUrlBuilder img = ImgproxyUrlBuilder.builder()
    .baseUrl("https://img.example.com")
    .signing(SigningKey.of(System.getenv("XI_SIGNING_KEY"), System.getenv("XI_SIGNING_SALT")))
    .encryption(EncryptionKey.of(System.getenv("XI_ENCRYPTION_KEY"))) // only for enc/ sources and PDF passwords
    .build();

// Object form: an options list with one typed method per option name and alias.
img.url("s3://photos/cat.jpg", ProcessingOptions.create().rs("fill", 1200, 630).g("sm").q(80), "webp");
// https://img.example.com/<signature>/rs:fill:1200:630/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp

// Fluent form: the same methods on a chain.
img.image(Sources.xiSource("photos", "2026/cat.jpg")).resize("fit", 300, 300).blur(2).extension("png").build();

// Chained pipelines (/-/): a list of options lists, or pipeline() in the fluent form.
img.url("s3://photos/cat.jpg",
    List.of(ProcessingOptions.create().rs("fit", 500, 500), ProcessingOptions.create().bl(2)),
    UrlParams.of());

// Plain or encrypted sources, a password-protected PDF.
img.url("https://example.com/a b.jpg", ProcessingOptions.create().w(300),
    UrlParams.of().withSourceEncoding(SourceEncoding.PLAIN));
img.url("s3://private/cat.jpg", ProcessingOptions.create().w(300),
    UrlParams.of().withSourceEncoding(SourceEncoding.ENCRYPTED));
img.url("s3://docs/report.pdf", ProcessingOptions.create().rs("fit", 800, 0),
    UrlParams.ofExtension("png").withPdfPassword("s3cret"));

// /info, /video, HLS and DASH.
img.infoUrl("s3://photos/cat.jpg", InfoOptions.create().size(true).format(true).dimensions(true));
img.videoUrl("s3://media/talk.mov", VideoOptions.create().rs("fill", 640, 360).vs(12).vd(8), VideoContainer.WEBM);
img.video("s3://media/talk.mov").start(12).duration(8).mute(true).extension("mp4").build();
img.streamUrl("s3://media/talk.mov", StreamParams.hls().withRenditions(360, 720).withSegmentDuration(4));
```

The option methods (`ProcessingMethods`, `InfoMethods`, `VideoMethods`) are generated from the server's catalogues: one overload per argument shape, with `long`, `double`, `boolean` and `String` parameters, and `@Nullable Number`/`String`/`Boolean` where an argument may be empty (`null` writes `rs:fill:300::1`). Each method's Javadoc lists the URL names, the arguments with their enum values and ranges, and the default. `option(name, args...)` adds any option by name. Numbers are written as JavaScript writes them (`300`, not `300.0`), so URLs and signatures match the other clients.

Bad input throws `UrlBuildException` (a `RuntimeException`), whose `code()` is the language-neutral `ErrorCode` of the conformance fixture: `UNKNOWN_OPTION`, `INVALID_ARGUMENT`, `INVALID_SOURCE`, `INVALID_EXTENSION`, `MISSING_SIGNING`, `MISSING_ENCRYPTION` and so on.

Builders and keys are immutable and thread-safe. Options lists and fluent chains are mutable builders, like `StringBuilder`.

`Xtandard` has static entry points named like the TypeScript client's factories: `imgproxy()`, `queryUrls()`, `client(baseUrl)`, `xiSource(name, key)`.

## Query dialect

```java
QueryUrlBuilder q = QueryUrlBuilder.builder()
    .baseUrl("https://img.example.com")
    .signing(SigningKey.of(KEY, SALT))
    .build();
q.url("s3://photos/cat.jpg", QueryParams.create().w(300).f("webp"), ProcessingOptions.create().bl(2));
// https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&f=webp&o=bl:2&xi_sig=…
q.videoUrl("s3://media/talk.mov", VideoOptions.create().rs("fill", 640, 360).vs(1), "webm");
q.streamUrl("s3://media/talk.mov", StreamParams.hls().withRenditions(360));
q.imaginaryUrl(ImaginaryEndpoint.RESIZE, "s3://photos/cat.jpg", ImaginaryParams.create().width(300));
q.image("s3://photos/cat.jpg").param("w", 300).bl(2).build();
```

`QueryParams` and `ImaginaryParams` have a typed method per native key and alias, and `param(key, value)` for any key.

## HTTP client

```java
XtandardClient xi = XtandardClient.builder("http://localhost:8080")
    .signing(SigningKey.of(KEY, SALT))
    .timeout(Duration.ofSeconds(30))
    .build();

xi.healthz();
InfoResponse info = xi.info("s3://photos/cat.jpg", InfoOptions.create().size(true).format(true));
Long width = info.width();                                // typed record, null when absent
MediaResponse<byte[]> img = xi.image("s3://photos/cat.jpg", ProcessingOptions.create().w(300),
    UrlParams.of(), MediaRequest.get().withAccept("image/avif,image/webp"));
MediaResponse<byte[]> part = xi.video("s3://media/talk.mov", VideoOptions.create().vd(5),
    UrlParams.ofExtension("webm"), MediaRequest.get().withRange(ByteRange.of(0, 1023)));   // 206
MediaResponse<byte[]> head = xi.video("s3://media/talk.mov", VideoOptions.create().vd(5),
    UrlParams.ofExtension("webm"), MediaRequest.head());
StreamManifest m = xi.stream("s3://media/talk.mov", StreamParams.hls().withRenditions(360, 720));
xi.media(m.body().lines().filter(l -> !l.startsWith("#")).findFirst().orElseThrow()); // ticket URLs need no signing

// A large body as a stream (close it):
MediaResponse<InputStream> big = xi.media(url, MediaRequest.get(), HttpResponse.BodyHandlers.ofInputStream());

// The same calls, asynchronously:
xi.async().info("s3://photos/cat.jpg", InfoOptions.create().size(true))
    .thenAccept(i -> System.out.println(i.size()));
```

Non-2xx responses throw an `HttpException` subclass chosen by status:

- `HttpException.BadRequest` (400)
- `HttpException.Forbidden` (403)
- `HttpException.NotFound` (404)
- `HttpException.NotAcceptable` (406)
- `HttpException.Unprocessable` (422)
- `HttpException.TooManyRequests` (429)
- `HttpException.ServiceUnavailable` (503)
- `HttpException.Gateway` (502, 504)
- `HttpException.ServerError` (other 5xx; the parent of the two above)

Each carries `status()`, `kind()`, `getMessage()` and `requestId()` from the server's JSON error envelope. Connection failures throw `TransportException`. `UrlBuildException`, `HttpException` and `TransportException` are the permitted subclasses of the sealed `XtandardException`. An async call's future fails with the same exceptions (`join()` wraps them in a `CompletionException`).

Pass your own `HttpClient` with `.httpClient(...)` (proxy, TLS, executor, HTTP/2 settings). `MediaRequest` also takes extra headers, and `playgroundMeta()` reads the playground's `api/meta` (`playgroundBaseUrl` and `playgroundPath` point it at the admin listener).

### HTTP QUERY (opt-in)

The query dialect endpoints also accept the HTTP QUERY method (RFC 10008, server toggle `XI_SERVER_QUERY_METHOD`, on by default): the URL's query goes out as `application/x-www-form-urlencoded` content, so long option chains, watermark text or encrypted sources stay out of URLs. `xi_sig` does not cover the method, so the content is the query of the signed GET URL. It is **off by default** in the client, because many CDNs and proxies neither cache nor forward QUERY.

```java
String url = q.url("s3://photos/cat.jpg", QueryParams.create().w(300));
QueryMethodRequest req = Xtandard.queryMethodRequest(url);   // or QueryMethodRequest.from(url)
// req.method() "QUERY", req.url() ".../q/", req.contentType(), req.body() "url=...&w=300&xi_sig=..."
xi.media(url, MediaRequest.query());                          // this call as QUERY

XtandardClient xq = XtandardClient.builder("http://localhost:8080").queryMethod(true).queryMount("/q").build();
xq.media(url);                        // URLs under the mount go out as QUERY (MediaRequest.of(): no method named)
xq.media(url, MediaRequest.get());    // an explicit GET (or HEAD) stays as it is
```

A URL without a query, or with a fragment, throws `UrlBuildException` with `ErrorCode.INVALID_REQUEST`. URL builders are unchanged.

## Coverage

| Server feature | Java |
|---|---|
| imgproxy processing options (90 with aliases), typed per option | `ProcessingOptions` and the fluent chains (`ProcessingMethods`), every name in the shared builder fixture |
| `/info` options (35) | `InfoOptions`, `infoUrl()`, `info()`, `XtandardClient.info()` (typed `InfoResponse` record) |
| `/video` options (52) and all 12 containers; `/video` signing | `VideoOptions`, `VideoContainer`, `videoUrl()`, `video()`, `XtandardClient.video()` with Range and HEAD |
| HLS and DASH (`st`, `sgd`, `sgf:ts`, `@m3u8`, `@mpd`) | `StreamParams`, `streamUrl()` on both builders, `XtandardClient.stream()`; ticket child URLs through `media()` |
| Password-protected PDFs (`pdp`) | `UrlParams.withPdfPassword()`, `withPdfPassword()` on the chains, `Encryption.encryptPdfPassword()` |
| Sources: base64, `plain/`, `enc/`, split; `xi://`, `s3://`, `gs://`, `abs://`, `local:///`, `ftp://`, `sftp://` | `SourceEncoding`, `splitSource()`, `Sources.xiSource()`, `Sources.sourceUrl()` |
| Query dialect `/q/`, `/q/video`, imaginary; `xi_sig` | `QueryUrlBuilder` (native keys typed in `QueryParams`), `Signing.signXiUrl()` |
| HTTP QUERY on the query dialect (opt-in, off by default) | `QueryMethodRequest.from()` / `Xtandard.queryMethodRequest()`, `MediaRequest.query()`, `XtandardClient.Builder.queryMethod()` and `queryMount()` |
| `/healthz`, `/readyz`, playground `api/meta` | `healthz()`, `readyz()`, `playgroundMeta()` |

Not covered, on purpose (as in the TypeScript client):

- other dialects' URL shapes, which their own SDKs build;
- imaginary's `sign` scheme;
- presets-only URLs;
- the playground's POST API.

## Design decisions

- **Maven, not Gradle.** The POM is what Maven Central publishes anyway, so the build is that one declarative file; mise pins Maven (no wrapper jar committed); the Central Portal plugin is Sonatype's own. The library needs no Gradle features (no variants, no Kotlin build logic).
- **No JSON dependency.** `/info` answers are decoded by a strict RFC 8259 parser of about 250 lines (`Json`) into generated records. A Jackson or Gson dependency would have been the only runtime dependency, with its own version conflicts for every user, for one small document type. `InfoResponse.fromJson(String)` is public, so a response fetched another way can be decoded too.
- **Typed methods, not a Map.** Java has no keyword arguments or literal types, so the TypeScript and Python option objects become generated method overloads (`rs(String, double, double)`), shared through generic interfaces by the options lists and the chains.
- **Two fixture cases are skipped:** `/info` and `/video` URLs with chained pipelines. `InfoOptions` and `VideoOptions` are one pipeline each, so the API cannot express them (`clients/README.md`: skip what the type system rules out). The `/video` PDF password case runs: `UrlParams` is shared, and `videoUrl()` refuses its password with `INVALID_REQUEST`.

## Development

```sh
cd clients/java
mise install        # JDK 21 (build), JDK 17 (oldest runtime) and Maven, pinned in mise.toml
task ci             # = task -d clients/java ci from the root: format, Error Prone, tests on 21 and 17, licences, jars
task fmt            # google-java-format through Spotless
task test           # conformance + builder fixture + vectors + unit + client tests
XI_SMOKE_URL=http://127.0.0.1:8080 task test -- -Dtest=SmokeTest   # against the playground demo
```

- JDK 21 runs the build (javac `--release 17`, [Error Prone](https://errorprone.info) with [NullAway](https://github.com/uber/NullAway) in JSpecify mode, warnings are errors) and the tests; `task test17` runs them again on JDK 17. CI tests 17, 21 and 25.
- `src/generated/java/` is generated from `clients/spec/spec.json` by `task clients:generate` (`clients/codegen/emitters/java.ts`). Do not edit it. Spotless leaves it alone; Error Prone and NullAway check it like the rest.
- Tests read the shared fixtures from the repository by relative path (`../../clients/spec`, `../../testdata`). The HTTP tests use the JDK's `com.sun.net.httpserver` as the stand-in server.
- `mvn -Prelease deploy` signs with GPG and uploads to the Central Portal (`central-publishing-maven-plugin`, `autoPublish=false`); no task or workflow runs it yet.

Runtime dependencies: none. Compile-only: `org.jspecify:jspecify` (Apache-2.0). `scripts/CheckLicenses.java` (`task licenses`) fails on any runtime dependency and checks the compile-only ones against MIT, BSD, Apache-2.0 and ISC. Test-only: JUnit (EPL-2.0, never shipped). Build plugins (Error Prone, NullAway, Spotless, google-java-format) are Apache-2.0 or MIT and are not dependencies of the artifact.
