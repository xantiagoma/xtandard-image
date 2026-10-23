# xtandard-image for Kotlin Multiplatform

The official Kotlin Multiplatform client of [xtandard-image](https://github.com/xantiagoma/xtandard-image) ([ADR 0013](../../docs/adr/0013-official-clients.md)). It has:

- typed URL builders for the imgproxy dialect (processing, `/info`, `/video`, HLS and DASH) and the query dialect (`/q/`, `/q/video`, imaginary endpoints), in an object form, a DSL and a fluent form;
- imgproxy and `xi_sig` signing;
- `enc/` source and PDF password encryption;
- an HTTP client on [Ktor](https://ktor.io) with suspend functions, Range and HEAD requests, and streaming bodies (`ByteReadChannel`).

| Target | Use | HTTP engine |
|---|---|---|
| `jvm` | servers, desktop, **Android apps** (they consume the JVM variant) | OkHttp |
| `iosArm64`, `iosSimulatorArm64` | iOS apps and shared KMP code | Darwin (URLSession) |
| `macosArm64` | macOS apps and tools | Darwin (URLSession) |

The common code is pure Kotlin. Only the HMAC-SHA256, AES-CBC and random-byte primitives and the default engine are per platform.

It is not on Maven Central yet. The coordinates are `io.github.xantiagoma:xtandard-image-kmp` (with the usual per-target artifacts such as `xtandard-image-kmp-jvm` and `xtandard-image-kmp-iosarm64`, which Gradle picks for you). They will follow the server's release tags. No artifact with that name existed on Maven Central when this client was written (checked 2026-10-22).

## Kotlin or Java client?

`io.github.xantiagoma:xtandard-image` is the [Java client](../java). It has no dependencies, and its `@NullMarked` JSpecify annotations make it pleasant from Kotlin. Its HTTP part uses `java.net.http`, which Android does not have, and Kotlin/Native (iOS, macOS) cannot use Java libraries at all. So there are two artifacts, with different names and packages (`io.github.xantiagoma.xtandard.image` and `io.github.xantiagoma.xtandard.image.kmp`), and both can sit on one classpath.

| You are writing | Use |
|---|---|
| a JVM server or tool in Java | the Java client |
| a JVM server or tool in Kotlin | either. The Java client has no dependencies and blocking or `CompletableFuture` calls. This one has suspend functions, a Kotlin DSL and Ktor, which you may already have. |
| an Android app | this one (the Java client's HTTP client cannot run on Android) |
| an iOS or macOS app, or shared KMP code | this one. Swift-only apps can use the Swift client instead. |

## Keys in apps

Signing and encryption work on every target, and both are optional. A key that ships inside a mobile or desktop app can be extracted from the binary. A signing key extracted that way signs **any** URL: any size, any source the server can reach, any amount of work. An encryption key extracted that way decrypts every `enc/` source and PDF password made with it. Choose one of these instead:

1. **Unsigned URLs with the server's `restricted` policy** (`XI_SECURITY_UNSIGNED_POLICY`, the default): unsigned images may use only the widths, heights, DPRs and qualities in the server's allowlists (`XI_SECURITY_ALLOWED_WIDTHS` and the like), and unsigned video transformations only what your video presets allow (`XI_VIDEO_TRANSFORM_UNSIGNED_POLICY=restricted`). See [`docs/CONFIG.md`](../../docs/CONFIG.md). A URL built in the app then cannot ask for anything else. Build these URLs with `ImgproxyUrlBuilder()` (no key), using the allowed sizes and the `preset` option.
2. **URLs signed by your backend**: the app asks your API for the URLs it needs, and the backend signs them with this library (or any other official client). The app only fetches them, with `XtandardClient.media(url)`.

Put keys in an app only when the app runs where you trust it, for example an internal tool on managed devices, and rotate them (`XI_SIGNING_KEYS` takes several) when a build leaks.

## Install (after the first release)

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

Until then, `gradle publishToMavenLocal` in this directory puts `0.1.0-SNAPSHOT` in `~/.m2` (add `mavenLocal()` to your repositories).

## URL builder

```kotlin
import io.github.xantiagoma.xtandard.image.kmp.*

val img = ImgproxyUrlBuilder(
    baseUrl = "https://img.example.com",
    signing = SigningKey.of(keyHex, saltHex),
    encryption = EncryptionKey.of(encryptionKeyHex), // only for enc/ sources and PDF passwords
)

// Object form: an options list with one typed method per option name and alias.
img.url("s3://photos/cat.jpg", processingOptions { rs("fill", 1200, 630); g("sm"); q(80) }, extension = "webp")
// https://img.example.com/<signature>/rs:fill:1200:630/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp

// DSL over the fluent chain: options and settings in one block.
img.image(Sources.xiSource("photos", "2026/cat.jpg")) {
    resize("fit", 300, 300)
    blur(2)
    extension(ImageFormat.PNG)
}

// Fluent form.
img.image("s3://photos/cat.jpg").resize("fit", 300, 300).blur(2).extension("png").build()

// Chained pipelines (/-/): a list of options lists, or pipeline() in the chain.
img.url("s3://photos/cat.jpg", listOf(processingOptions { rs("fit", 500, 500) }, processingOptions { bl(2) }))

// Plain or encrypted sources, a password-protected PDF.
img.url("https://example.com/a b.jpg", processingOptions { w(300) }, sourceEncoding = SourceEncoding.PLAIN)
img.url("s3://private/cat.jpg", processingOptions { w(300) }, sourceEncoding = SourceEncoding.ENCRYPTED)
img.url("s3://docs/report.pdf", processingOptions { rs("fit", 800, 0) }, extension = "png", pdfPassword = "s3cret")

// /info, /video, HLS and DASH.
img.infoUrl("s3://photos/cat.jpg", infoOptions { size(true); format(true); dimensions(true) })
img.videoUrl("s3://media/talk.mov", videoOptions { rs("fill", 640, 360); vs(12); vd(8) }, VideoContainer.WEBM)
img.video("s3://media/talk.mov") { start(12); duration(8); mute(true); extension("mp4") }
img.streamUrl("s3://media/talk.mov", StreamParams.hls(renditions = listOf(360, 720), segmentDuration = 4))
```

The option methods (`ProcessingMethods`, `InfoMethods`, `VideoMethods`) are generated from the server's catalogues. There is one overload per argument shape. Numeric arguments are `Number`, so `Int`, `Long`, `Float` and `Double` all fit. Arguments that may be empty are nullable: `null` writes `rs:fill:300::1`. Each method's KDoc lists the URL names, the arguments with their enum values and ranges, and the default. `option(name, args...)` adds any option by name. Numbers are written as JavaScript writes them (`300`, not `300.0`; `1.1f` is `1.1`), so URLs and signatures match the other clients.

Bad input throws `UrlBuildException`, whose `code` is the language-neutral `ErrorCode` of the conformance fixture: `UNKNOWN_OPTION`, `INVALID_ARGUMENT`, `INVALID_SOURCE`, `INVALID_EXTENSION`, `MISSING_SIGNING`, `MISSING_ENCRYPTION` and so on.

Builders and keys are immutable and thread-safe. Options lists and chains are mutable builders, like `StringBuilder`.

## Query dialect

```kotlin
val q = QueryUrlBuilder(baseUrl = "https://img.example.com", signing = SigningKey.of(KEY, SALT))
q.url("s3://photos/cat.jpg", queryParams { w(300); f("webp") }, processingOptions { bl(2) })
// https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&f=webp&o=bl:2&xi_sig=…
q.videoUrl("s3://media/talk.mov", videoOptions { rs("fill", 640, 360); vs(1) }, "webm")
q.streamUrl("s3://media/talk.mov", StreamParams.hls(listOf(360)))
q.imaginaryUrl(ImaginaryEndpoint.RESIZE, "s3://photos/cat.jpg", imaginaryParams { width(300) })
q.image("s3://photos/cat.jpg") { param("w", 300); bl(2) }
```

`QueryParams` and `ImaginaryParams` have a typed method per native key and alias, and `param(key, value)` for any key. `Signing.signXiUrl(url, key, mount = "/q")` signs a URL built elsewhere.

## HTTP client

```kotlin
XtandardClient("http://localhost:8080", signing = SigningKey.of(KEY, SALT), timeout = 30.seconds).use { xi ->
    xi.healthz()
    val info = xi.info("s3://photos/cat.jpg", infoOptions { size(true); format(true) })
    val width: Long? = info.width                       // kotlinx.serialization data class, null when absent
    val img = xi.image("s3://photos/cat.jpg", processingOptions { w(300) },
        request = MediaRequest(accept = "image/avif,image/webp"))
    val part = xi.video("s3://media/talk.mov", videoOptions { vd(5) }, "webm",
        request = MediaRequest(range = ByteRange.Between(0, 1023)))   // 206
    val head = xi.video("s3://media/talk.mov", videoOptions { vd(5) }, "webm",
        request = MediaRequest(MediaMethod.HEAD))
    val m = xi.stream("s3://media/talk.mov", StreamParams.hls(listOf(360, 720)))
    xi.media(m.body.lines().first { it.isNotEmpty() && !it.startsWith("#") })   // ticket URLs need no signing

    // A large body as it arrives:
    xi.mediaStream(url) { res -> res.body.copyAndClose(sink) }
}
```

Every call is a `suspend` function. Non-2xx answers throw an `HttpException` subclass chosen by status:

- `HttpException.BadRequest` (400)
- `HttpException.Forbidden` (403)
- `HttpException.NotFound` (404)
- `HttpException.NotAcceptable` (406)
- `HttpException.Unprocessable` (422)
- `HttpException.TooManyRequests` (429)
- `HttpException.ServiceUnavailable` (503)
- `HttpException.Gateway` (502, 504)
- `HttpException.ServerError` (other 5xx; the parent of the two above)

Each carries `status`, `kind`, `message` and `requestId` from the server's JSON error envelope. Connection failures and malformed answers throw `TransportException`. `UrlBuildException`, `HttpException` and `TransportException` are the subclasses of the sealed `XtandardException`.

Pass your own Ktor client with `httpClient = HttpClient(Darwin) { … }` (engine, proxy, TLS, logging). Its own timeouts apply, and `close()` leaves it open. `MediaRequest` also takes extra headers. `playgroundMeta()` reads the playground's `api/meta`; `playgroundBaseUrl` and `playgroundPath` point it at the admin listener. `XtandardJson.decodeFromString<InfoResponse>(text)` decodes an answer fetched another way.

## Coverage

| Server feature | Kotlin |
|---|---|
| imgproxy processing options (90 with aliases), typed per option | `ProcessingOptions`, `processingOptions {}` and the chains (`ProcessingMethods`), every name in the shared builder fixture |
| `/info` options (35) | `InfoOptions`, `infoUrl()`, `info()`, `XtandardClient.info()` (typed `InfoResponse`) |
| `/video` options (52) and all 12 containers; `/video` signing | `VideoOptions`, `VideoContainer`, `videoUrl()`, `video()`, `XtandardClient.video()` with Range and HEAD |
| HLS and DASH (`st`, `sgd`, `sgf:ts`, `@m3u8`, `@mpd`) | `StreamParams`, `streamUrl()` on both builders, `XtandardClient.stream()`; ticket child URLs through `media()` |
| Password-protected PDFs (`pdp`) | `pdfPassword =` on `url()` and `infoUrl()`, `withPdfPassword()` on the chains, `Encryption.encryptPdfPassword()` |
| Sources: base64, `plain/`, `enc/`, split; `xi://`, `s3://`, `gs://`, `abs://`, `local:///`, `ftp://`, `sftp://` | `SourceEncoding`, `splitSource`, `Sources.xiSource()`, `Sources.sourceUrl()` |
| Query dialect `/q/`, `/q/video`, imaginary; `xi_sig` | `QueryUrlBuilder` (native keys typed in `QueryParams`), `Signing.signXiUrl()` |
| `/healthz`, `/readyz`, playground `api/meta` | `healthz()`, `readyz()`, `playgroundMeta()` |

Not covered, on purpose (as in the TypeScript client):

- other dialects' URL shapes, which their own SDKs build;
- imaginary's `sign` scheme;
- presets-only URLs;
- the playground's POST API.

## Design decisions

- **Platform crypto, no crypto library.** HMAC-SHA256, AES-256-CBC with PKCS#7 and secure random bytes come from the platform: `javax.crypto` and `SecureRandom` on the JVM and Android, CommonCrypto (`CCHmac`, `CCCrypt`) and `SecRandomCopyBytes` on Apple platforms. Each is about 20 lines behind an `expect fun`. Everything around them (salting, truncation, base64url, the token layout, the IV) is common code and is checked against the shared vectors on every target. A multiplatform library, such as [cryptography-kotlin](https://github.com/whyoleg/cryptography-kotlin) (Apache-2.0), would add a dependency tree and provider modules to every app for three calls that the operating system already provides and maintains. Its WebCrypto backend is asynchronous, so a JS target would still need a different design (below).
- **Ktor, with one engine per platform.** OkHttp on the JVM works on servers and on Android alike. Darwin uses `URLSession`, so iOS apps get the system proxy, ATS and certificate settings. The engines are `implementation` dependencies. Pass `httpClient` to use another one, for example CIO.
- **kotlinx.serialization** decodes `/info` and error bodies into data classes generated from the server's JSON Schemas. It ignores unknown keys, so a newer server does not break an older client.
- **Android through the JVM variant.** Kotlin's Gradle plugin matches Android consumers (`androidJvm`) to a library's `jvm` variant, and the jar is Java 11 bytecode, which D8 accepts on every supported API level. The Ktor OkHttp engine is the standard Android choice. A separate `androidTarget` would need the Android SDK (and its licence acceptance) on every build machine, and it would add nothing yet: no part of the client uses an Android API. No Android app build in this repository checks it yet: the build machines have no Android SDK.
- **No JS or Wasm target.** URL building is synchronous, and WebCrypto, the only standard crypto in browsers, is asynchronous. JavaScript and TypeScript users have the [TypeScript client](../typescript).
- **One package, explicit API.** Everything is in `io.github.xantiagoma.xtandard.image.kmp`, with `explicitApi()` on. Compiler warnings are errors. ktlint (`ktlint_official`) checks the hand-written sources.
- **Typed methods and DSLs, not maps.** Kotlin has named arguments but no literal types, so the TypeScript and Python option objects become generated overloads (`rs(type: String, width: Number, height: Number)`) on generic interfaces that the options lists and chains share. The `processingOptions { }` and `img.image(src) { }` blocks are ordinary lambdas with receivers over the same methods (`@DslMarker`).
- **Gradle pinned by mise, no wrapper jar.** `mise.toml` pins JDK 21, Gradle 9.8.2 and ktlint, as the Java client pins Maven. CI installs the same Gradle version with `gradle/actions/setup-gradle`. `gradle wrapper` creates a wrapper if you want one locally.
- **Publishing** uses [vanniktech/gradle-maven-publish-plugin](https://github.com/vanniktech/gradle-maven-publish-plugin) (Apache-2.0) for the Central Portal, signing (only when `signingInMemoryKey` is set), sources jars and a Dokka javadoc jar. No task or workflow publishes yet. `task publish-dry-run` writes every publication into `build/repo`.
- **Three conformance cases are skipped:** `/info` and `/video` URLs with chained pipelines, and a `/video` URL with a PDF password. `infoUrl()` and `videoUrl()` take one options list, and `videoUrl()` has no PDF password parameter, so the API cannot express them (`clients/README.md`: skip what the type system rules out).

## Development

```sh
cd clients/kotlin
mise install        # JDK 21, Gradle 9.8.2 and ktlint, pinned in mise.toml
task ci             # = task -d clients/kotlin ci from the root: ktlint, build, tests, licences, docs, publish dry run
task fmt            # ktlint --format
task test           # JVM, macOS and iOS simulator tests (the Apple ones on macOS with Xcode)
task test-jvm       # the JVM tests only
XI_SMOKE_URL=http://127.0.0.1:8080 task test-jvm   # adds the smoke test against the playground demo
```

- Kotlin 2.4 with Gradle 9.8. Kotlin/Native downloads its toolchain into `~/.konan` on first use, and the Apple targets need Xcode. `-Pxi.simulator="iPhone 17 Pro"` (the default in `gradle.properties`) picks the simulator. `-Pxi.testJdk=11` runs the JVM tests on another JDK.
- `src/generated/kotlin/` is generated from `clients/spec/spec.json` by `task clients:generate` (`clients/codegen/emitters/kotlin.ts`). Do not edit it. ktlint skips it, and the compiler checks it like the rest.
- The common tests run on every target and read the shared fixtures from the repository by absolute path: a generated `REPO_ROOT` constant, so the iOS simulator finds them too. The HTTP tests use Ktor's `MockEngine` as the stand-in server on every target, and on the JVM the default OkHttp client also runs against the JDK's `HttpServer`.

Runtime dependencies (all Apache-2.0 except SLF4J, MIT): Ktor client core with OkHttp (JVM) or Darwin (Apple), kotlinx.serialization JSON, and what they pull in: kotlinx.coroutines, kotlinx-io, atomicfu, OkHttp, Okio, SLF4J API and JetBrains annotations. `gradle checkLicenses` (`task licenses`) resolves the runtime closure of every target, reads each module's POM licence (following parent POMs) and fails on anything outside MIT, BSD, Apache-2.0 and ISC. There are no exceptions. Test-only: kotlin-test, kotlinx-coroutines-test, ktor-client-mock and kotlinx-io (Apache-2.0), and JUnit 4 (EPL-1.0) on the JVM, never shipped. Build plugins (Kotlin, Dokka, maven-publish) are Apache-2.0 and are not dependencies of the artifact.
