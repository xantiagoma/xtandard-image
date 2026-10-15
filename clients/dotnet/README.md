# xtandard-image for .NET

The official .NET client of [xtandard-image](https://github.com/xantiagoma/xtandard-image) ([ADR 0013](../../docs/adr/0013-official-clients.md)). It has:

- typed URL builders for the imgproxy dialect (processing, `/info`, `/video`, HLS and DASH) and the query dialect (`/q/`, `/q/video`, imaginary endpoints);
- imgproxy and `xi_sig` signing;
- `enc/` source and PDF password encryption;
- an async client on `HttpClient`, friendly to `IHttpClientFactory`, with a typed `InfoResponse`.

It targets .NET 8 and .NET 10 (`net8.0`, `net10.0`). Nullable annotations are on, the core package has **no package dependencies**, and it is marked `IsAotCompatible`: no reflection, a source-generated `System.Text.Json` context (`XtandardJsonContext`) for the response bodies, and the trimming and AOT analyzers run in its build.

| Package | What | Dependencies |
|---|---|---|
| `Xtandard.Image` | builders, signing, encryption, `XtandardClient` | none |
| `Xtandard.Image.Extensions` | `services.AddXtandardImage(…)` for ASP.NET Core and the Generic Host | `Microsoft.Extensions.Http` (MIT) |

The dependency injection integration is a separate package so that the core stays dependency-free: console apps, workers, AOT apps and libraries take `Xtandard.Image` without the `Microsoft.Extensions.*` closure.

Neither is on NuGet yet. Both names were free on nuget.org on 2026-10-15. The packages will follow the server's release tags.

**Build and sign URLs on a server.** The signing key signs every URL, and the encryption key decrypts every `enc/` source and PDF password.

## Install (from the repository, until the first release)

```sh
cd clients/dotnet && task pack       # artifacts/Xtandard.Image.0.0.0.nupkg
dotnet add package Xtandard.Image --source path/to/clients/dotnet/artifacts
```

## URL builder

```csharp
using Xtandard.Image;
using static Xtandard.Image.ProcessingOption;   // the typed option records: Resize, Quality, Blur, …

var img = new ImgproxyUrlBuilder(new ImgproxyConfig
{
    BaseUrl = "https://img.example.com",
    Signing = new SigningKey(Environment.GetEnvironmentVariable("XI_SIGNING_KEY")!, Environment.GetEnvironmentVariable("XI_SIGNING_SALT")!),
    Encryption = new EncryptionKey(Environment.GetEnvironmentVariable("XI_ENCRYPTION_KEY")!), // only for enc/ sources and PDF passwords
});

// Options collection: names or aliases with their arguments, or typed records, in URL order.
img.Url("s3://photos/cat.jpg", new() { { "rs", "fill", 1200, 630 }, { "g", "sm" }, { "q", 80 } }, extension: "webp");
img.Url("s3://photos/cat.jpg", new() { new Resize("fill", 1200, 630), new Gravity("sm"), new Quality(80) }, extension: "webp");
// https://img.example.com/<signature>/rs:fill:1200:630/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp

// Fluent form: one method per option name and alias, typed from the option's forms.
img.Image(Sources.XiSource("photos", "2026/cat.jpg")).Resize("fit", 300, 300).Blur(2).Extension("png").Build();

// Chained pipelines (/-/): ChainedUrl, or Pipeline() in the fluent form.
img.ChainedUrl("s3://photos/cat.jpg", [new() { { "rs", "fit", 500, 500 } }, new() { { "bl", 2 } }]);
img.Image("s3://photos/cat.jpg").Resize("fit", 500, 500).Pipeline().Blur(2).Build();

// Plain or encrypted sources, a password-protected PDF.
img.Url("https://example.com/a b.jpg", new() { { "w", 300 } }, sourceEncoding: SourceEncoding.Plain);
img.Url("s3://private/cat.jpg", new() { { "w", 300 } }, sourceEncoding: SourceEncoding.Encrypted);
img.Url("s3://docs/report.pdf", new() { { "rs", "fit", 800, 0 } }, extension: "png", pdfPassword: "s3cret");

// /info, /video, HLS and DASH.
img.InfoUrl("s3://photos/cat.jpg", new() { { "size", true }, { "format", true }, { "dimensions", true } });
img.VideoUrl("s3://media/talk.mov", new() { { "rs", "fill", 640, 360 }, { "vs", 12 }, { "vd", 8 } }, extension: "webm");
img.Video("s3://media/talk.mov").Start(12).Duration(8).Mute(true).Extension("mp4").Build();
img.StreamUrl("s3://media/talk.mov", StreamProtocol.Hls, renditions: [360, 720], segmentDuration: 4);
```

Arguments are `OptionArg` values: strings, integers, numbers and booleans convert implicitly, and `OptionArg.Null` is an empty argument (`rs:fill:300::1`). The per-option methods and records are generated from the server's catalogues: integers are `long`, numbers `double`, flags `bool`, enums, colours and base64 strings `string`, and optional trailing arguments may be left out. Repeated groups are `params` (`CropObjects(1.5, "face", "dog")`, `BlurAreas(2, (0, 0, 0.5, 0.5))`). Their XML docs carry the option's description, URL names, allowed values and ranges.

Builders are immutable and thread-safe. An unknown option, a bad argument, source, extension or key throws `UrlBuildException`, whose `Code` (`UrlBuildErrorCode`) and `CodeName` (`unknown_option`, `invalid_argument`, `invalid_source`, …) are the conformance codes.

## Query dialect

```csharp
var q = new QueryUrlBuilder(new QueryConfig { BaseUrl = "https://img.example.com", Signing = key });
q.Url("s3://photos/cat.jpg", new() { { QueryKeys.W, 300 }, { QueryKeys.F, "webp" } }, new() { new Blur(2) });
// https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&f=webp&o=bl:2&xi_sig=…
q.Image("s3://photos/cat.jpg").Param("w", 300).Blur(2).Build();
q.VideoUrl("s3://media/talk.mov", new() { { "rs", "fill", 640, 360 }, { "vs", 1 } }, extension: "webm");
q.StreamUrl("s3://media/talk.mov", StreamProtocol.Hls, renditions: [360]);
q.ImaginaryUrl(ImaginaryEndpoints.Resize, "s3://photos/cat.jpg", new() { { "width", 300 } });
Signing.SignXiUrl("https://img.example.com/q/?url=https://e.com/a.jpg&w=300", key, mount: "/q");
```

## HTTP client

```csharp
using var xi = new XtandardClient(new XtandardClientOptions { BaseUrl = "http://localhost:8080", Signing = key });
await xi.HealthzAsync(ct);
InfoResponse info = await xi.InfoAsync("s3://photos/cat.jpg", new() { { "size", true }, { "format", true } }, cancellationToken: ct);
using var image = await xi.ImageAsync("s3://photos/cat.jpg", new() { { "w", 300 } }, request: new() { Accept = "image/avif,image/webp" }, cancellationToken: ct);
using var part = await xi.VideoAsync("s3://media/talk.mov", new() { { "vd", 5 } }, "webm", request: new() { Range = ByteRange.Of(0, 1023) }, cancellationToken: ct);
using var head = await xi.VideoAsync("s3://media/talk.mov", new() { { "vd", 5 } }, "webm", request: new() { Method = HttpMethod.Head }, cancellationToken: ct);
StreamManifest manifest = await xi.StreamAsync("s3://media/talk.mov", StreamProtocol.Hls, [360, 720], cancellationToken: ct);
using var segment = await xi.MediaAsync(manifest.Body.Split('\n').Last(l => l.Length > 0 && l[0] != '#'), cancellationToken: ct); // ticket URLs need no signing
```

Every method is async and takes a `CancellationToken`. Media methods return the `HttpResponseMessage`, which the caller disposes; `request: new() { Stream = true }` returns once the headers arrive, so a large body can be read as a stream. URLs are sent byte for byte (no `Uri` canonicalization), because the signature covers them.

`new XtandardClient(options)` creates and owns its `HttpClient`. `new XtandardClient(httpClient, options)` uses one it does not own, as from `IHttpClientFactory`. With `Xtandard.Image.Extensions`:

```csharp
builder.Services.AddXtandardImage(o =>
{
    o.BaseUrl = "https://img.example.com";
    o.Signing = new SigningKey(builder.Configuration["Xi:SigningKey"]!, builder.Configuration["Xi:SigningSalt"]!);
}); // returns the IHttpClientBuilder: add handlers or resilience here

// Inject XtandardClient (a typed client), ImgproxyUrlBuilder or QueryUrlBuilder (singletons).
```

Non-2xx responses throw an `XtandardHttpException` subclass chosen by status:

- `BadRequestException` (400)
- `ForbiddenException` (403)
- `NotFoundException` (404)
- `NotAcceptableException` (406)
- `UnprocessableException` (422)
- `TooManyRequestsException` (429)
- `ServiceUnavailableException` (503)
- `GatewayException` (502, 504)
- `ServerErrorException` (other 5xx, and the base of the three above)

Each carries `Status`, `Kind`, `Message` and `RequestId` from the server's JSON error envelope. Connection failures and timeouts throw `XtandardTransportException`; a cancelled token throws `OperationCanceledException`. All exceptions derive from `XtandardException`.

## Coverage

| Server feature | .NET |
|---|---|
| imgproxy processing options (90 with aliases), typed per option | `ProcessingOptions`, `ProcessingOption.*` records, fluent methods, every name in the shared builder fixture |
| `/info` options (35) | `InfoOptions`, `InfoOption.*`, `InfoUrl()`, `Info()`, `XtandardClient.InfoAsync()` (typed `InfoResponse`) |
| `/video` options (52) and all 12 containers; `/video` signing | `VideoOptions`, `VideoOption.*`, `VideoUrl()`, `Video()`, `XtandardClient.VideoAsync()` with Range and HEAD |
| HLS and DASH (`st`, `sgd`, `sgf:ts`, `@m3u8`, `@mpd`) | `StreamUrl()` on both builders, `XtandardClient.StreamAsync()`; ticket child URLs through `MediaAsync()` |
| Password-protected PDFs (`pdp`) | `pdfPassword:` / `WithPdfPassword()`, `Encryption.EncryptPdfPassword()` |
| Sources: base64, `plain/`, `enc/`, split; `xi://`, `s3://`, `gs://`, `abs://`, `local:///`, `ftp://`, `sftp://` | `SourceEncoding`, `SplitSource`, `Sources.XiSource()`, `Sources.SourceUrl()` |
| Query dialect `/q/`, `/q/video`, imaginary; `xi_sig` | `QueryUrlBuilder` (`QueryParams`, `QueryKeys`, `ImaginaryEndpoints`), `Signing.SignXiUrl()` |
| `/healthz`, `/readyz`, playground `api/meta` | `HealthzAsync()`, `ReadyzAsync()`, `PlaygroundMetaAsync()` |

Not covered, on purpose (as in the TypeScript and Python clients):

- other dialects' URL shapes, which their own SDKs build;
- imaginary's `sign` scheme;
- presets-only URLs;
- the playground's POST API.

Of the shared conformance fixture, three cases cannot be expressed by the typed API and are skipped with that reason: a PDF password on `/video` (`VideoUrl()` has no such parameter) and chained `/info` and `/video` pipelines (`InfoUrl()` and `VideoUrl()` take one options collection, and their fluent builders have no `Pipeline()`).

## Development

```sh
cd clients/dotnet
mise install                 # the .NET 10 and .NET 8 SDKs, pinned in mise.toml (global.json pins the SDK)
task ci                      # = task -d clients/dotnet ci from the root: restore, format, build, tests, licences, pack
task test                    # conformance + builder fixture + vectors + unit + stand-in HTTP + DI tests, on net8.0 and net10.0
XI_SMOKE_URL=http://127.0.0.1:8080 task test    # adds the smoke tests against the playground demo
```

mise installs the .NET SDKs on macOS and Linux, so no Docker image is needed. Tests use xUnit v3 on Microsoft.Testing.Platform. Restores are locked (`packages.lock.json`, `--locked-mode`), and `dotnet format --verify-no-changes` checks style. The build treats warnings as errors with the `latest-recommended` analyzers, code style enforcement, nullable reference types and, for the packages, the trimming and AOT analyzers.

`src/Xtandard.Image/Generated/` is generated from `clients/spec/spec.json` by `task clients:generate` (`clients/codegen/emitters/dotnet.ts`). Do not edit it. The files are marked `// <auto-generated/>`, so the analyzers and `dotnet format` skip them.

Package dependencies:

- `Xtandard.Image`: none. The SDK adds `Microsoft.NET.ILLink.Tasks` (MIT) at build time for the trimming analysis; it ships nothing.
- `Xtandard.Image.Extensions`: `Microsoft.Extensions.Http` 8.0.1 and its `Microsoft.Extensions.*` closure, all MIT.

`scripts/check-licenses.cs` (`task licenses`, a .NET 10 file-based app) checks the full closure from the lock files. There are no exceptions.
