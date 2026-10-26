![xtandard-image](https://raw.githubusercontent.com/xantiagoma/xtandard-image/main/docs/assets/logo-128.png)

# xtandard-image for .NET

**Typed URL builders, signing and an async HttpClient-based client for the xtandard-image server.**

[//]: # (registry-status: pending)
[![NuGet: pending](https://img.shields.io/badge/NuGet-pending-lightgrey)](https://github.com/xantiagoma/xtandard-image/blob/main/docs/RELEASE.md) [![license: MIT](https://img.shields.io/badge/license-MIT-blue)](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

`Xtandard.Image` is the .NET client for [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme), an open-source media server that covers your application's image and video needs, built on libvips and FFmpeg. The package builds and signs the URLs your code sends to the server, and wraps its `/info`, media and health endpoints; it does not process images itself. To run a server, see [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md), or start one with signing keys:

```sh
export XI_SIGNING_KEY="$(openssl rand -hex 32)" XI_SIGNING_SALT="$(openssl rand -hex 32)"
docker run -d --name xtandard-image -p 8080:8080 \
  -e XI_SOURCES=web -e XI_SOURCES_WEB_TYPE=http -e XI_SOURCES_WEB_ALLOW=images.unsplash.com \
  -e XI_SIGNING_KEYS="$XI_SIGNING_KEY" -e XI_SIGNING_SALTS="$XI_SIGNING_SALT" \
  ghcr.io/xantiagoma/xtandard-image:0.1.0-rc.2
```

## Install

```sh
dotnet add package Xtandard.Image
dotnet add package Xtandard.Image.Extensions   # optional: AddXtandardImage() for ASP.NET Core and the Generic Host
```

[//]: # (registry-status: pending)
Not published yet: publishing to NuGet is being set up. Until then, install from the repository:

```sh
git clone --depth 1 https://github.com/xantiagoma/xtandard-image
dotnet pack xtandard-image/clients/dotnet/src/Xtandard.Image -c Release -o ./xi-packages
dotnet add package Xtandard.Image --version 0.0.0 --source ./xi-packages
```

Packing needs the .NET 10 SDK; the package itself runs on .NET 8 and .NET 10. `Xtandard.Image.Extensions` packs the same way from `src/Xtandard.Image.Extensions`.

## Quick start

Build a signed URL with the keys the server was started with:

```csharp
using Xtandard.Image;

var signing = new SigningKey(
    Environment.GetEnvironmentVariable("XI_SIGNING_KEY")!,
    Environment.GetEnvironmentVariable("XI_SIGNING_SALT")!);

var img = new ImgproxyUrlBuilder(new ImgproxyConfig { BaseUrl = "http://localhost:8080", Signing = signing });

string url = img.Image("https://images.unsplash.com/photo-1506744038136-46273834b3fb")
    .Resize("fill", 1200, 630)
    .Gravity("sm")
    .Extension("jpg")
    .Build();
// http://localhost:8080/<signature>/rs:fill:1200:630/g:sm/aHR0cHM6….jpg
```

Ask the server about the source with the client:

```csharp
using var xi = new XtandardClient(new XtandardClientOptions { BaseUrl = "http://localhost:8080", Signing = signing });

InfoResponse info = await xi.InfoAsync("https://images.unsplash.com/photo-1506744038136-46273834b3fb");
Console.WriteLine($"{info.Format} {info.Width} {info.Height}"); // jpeg 7360 4912
```

Sign URLs on a server only: the signing key signs every URL, and the encryption key decrypts every `enc/` source.

## Usage

### Building URLs

There are two equivalent forms, an options collection (names or aliases with their arguments, or typed records) and a fluent chain with one method per option name and alias:

```csharp
using static Xtandard.Image.ProcessingOption; // the typed option records: Resize, Gravity, Quality, …

var img = new ImgproxyUrlBuilder(new ImgproxyConfig { BaseUrl = "https://img.example.com", Signing = signing });

img.Url("s3://photos/cat.jpg", new() { { "rs", "fill", 1200, 630 }, { "g", "sm" }, { "q", 80 } }, extension: "webp");
img.Url("s3://photos/cat.jpg", new() { new Resize("fill", 1200, 630), new Gravity("sm"), new Quality(80) }, extension: "webp");
// both: https://img.example.com/<signature>/rs:fill:1200:630/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp
img.Image(Sources.XiSource("photos", "2026/cat.jpg")).Resize("fit", 300, 300).Blur(2).Extension("png").Build();
```

The methods and records are generated from the server's catalogues, with typed arguments and XML docs. `OptionArg.Null` writes an empty argument (`rs:fill:300::1`). Builders are immutable and thread-safe. The options themselves are documented in [URL dialects §3.2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#32-processing-options--tr).

### Chained pipelines

`ChainedUrl`, or `Pipeline()` in the fluent form, writes `/-/` between pipelines:

```csharp
img.ChainedUrl("s3://photos/cat.jpg", [new() { { "rs", "fit", 500, 500 } }, new() { { "bl", 2 } }]);
img.Image("s3://photos/cat.jpg").Resize("fit", 500, 500).Pipeline().Blur(2).Build();
```

### Sources

Sources are base64url by default (`SplitSource` splits them into segments). `SourceEncoding.Plain` writes `/plain/…`, escaped for you, and `SourceEncoding.Encrypted` writes `/enc/<token>` (it needs `Encryption`, below). Set it on `ImgproxyConfig` or per call:

```csharp
img.Url("https://example.com/a b.jpg", new() { { "w", 300 } }, sourceEncoding: SourceEncoding.Plain);
// …/plain/https://example.com/a%20b.jpg
```

`Sources.XiSource("photos", "2026/cat.jpg")` writes a named source (`xi://photos/2026/cat.jpg`) and `Sources.SourceUrl("s3", "bucket", "cat.jpg")` a storage URL.

### `/info`

```csharp
img.InfoUrl("s3://photos/cat.jpg", new() { { "size", true }, { "format", true }, { "dimensions", true } });
```

### Video and streams

`/video` URLs use the video option catalogue; the extension is the container (`mp4`, `webm`, `mp3`, `png` for a waveform, …). `StreamUrl` builds HLS and DASH manifests, whose segments a player fetches without any key:

```csharp
img.Video("s3://media/talk.mov").Start(12).Duration(8).Mute(true).Extension("mp4").Build();
img.StreamUrl("s3://media/talk.mov", StreamProtocol.Hls, renditions: [360, 720], segmentDuration: 4);
```

The server needs the `full` or `gpl` image for video. Options and containers: [URL dialects §3.8](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#38-video-transformations-wp-122-wp-123) and [§3.10](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#310-hls-and-dash-streams-wp-126-every-dialect).

### Query dialect

`QueryUrlBuilder` builds `/q/` URLs (native keys plus imgproxy options in `o=`), `/q/video` and imaginary endpoints, signed with `xi_sig`. The server needs `XI_DIALECTS_QUERY_ENABLED=true`.

```csharp
var q = new QueryUrlBuilder(new QueryConfig { BaseUrl = "https://img.example.com", Signing = signing });
q.Url("s3://photos/cat.jpg", new() { { QueryKeys.W, 300 }, { QueryKeys.F, "webp" } }, new() { new Blur(2) });
// https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&f=webp&o=bl:2&xi_sig=…
q.ImaginaryUrl(ImaginaryEndpoints.Resize, "s3://photos/cat.jpg", new() { { "width", 300 } });
```

See [URL dialects §4](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#4-query-dialect-native--imaginary). The client can also send query URLs with the HTTP QUERY method (`QueryMethodRequest`, `XtandardClientOptions.QueryMethod`), off by default.

### Signing and encryption

`SigningKey` takes the server's hex key and salt (`XI_SIGNING_KEYS` / `XI_SIGNING_SALTS`, or imgproxy's `IMGPROXY_KEY` / `IMGPROXY_SALT`). `EncryptionKey` takes the first `XI_ENCRYPTION_KEYS` entry, for `enc/` sources and password-protected PDFs:

```csharp
var img = new ImgproxyUrlBuilder(new ImgproxyConfig
{
    BaseUrl = "https://img.example.com",
    Signing = signing,
    Encryption = new EncryptionKey(Environment.GetEnvironmentVariable("XI_ENCRYPTION_KEY")!),
});
img.Url("s3://private/cat.jpg", new() { { "w", 300 } }, sourceEncoding: SourceEncoding.Encrypted);
img.Url("s3://docs/report.pdf", new() { { "rs", "fit", 800, 0 } }, extension: "png", pdfPassword: "s3cret");
```

`Signing.SignXiUrl` adds an `xi_sig` to a URL of any other dialect. Schemes: [URL dialects §2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#2-signing-schemes-at-a-glance).

### HTTP client

`XtandardClient` has `HealthzAsync`, `ReadyzAsync`, `InfoAsync`, `ImageAsync`, `VideoAsync`, `StreamAsync`, `MediaAsync` (with `Range` and `HEAD`) and `PlaygroundMetaAsync`. Every method takes a `CancellationToken`; media methods return the `HttpResponseMessage`, which the caller disposes:

```csharp
using var image = await xi.ImageAsync("s3://photos/cat.jpg", new() { { "w", 300 } }, request: new() { Accept = "image/avif,image/webp" });
using var part = await xi.VideoAsync("s3://media/talk.mov", new() { { "vd", 5 } }, "webm", request: new() { Range = ByteRange.Of(0, 1023) });
```

`new XtandardClient(options)` owns its `HttpClient`; `new XtandardClient(httpClient, options)` uses one it does not own. With `Xtandard.Image.Extensions`, register it through `IHttpClientFactory`:

```csharp
builder.Services.AddXtandardImage(o =>
{
    o.BaseUrl = "https://img.example.com";
    o.Signing = new SigningKey(builder.Configuration["Xi:SigningKey"]!, builder.Configuration["Xi:SigningSalt"]!);
}); // returns the IHttpClientBuilder: add handlers or resilience here
// Inject XtandardClient (a typed client), ImgproxyUrlBuilder or QueryUrlBuilder (singletons).
```

### Errors

An invalid option, argument, source, extension or key throws `UrlBuildException`, whose `Code` (`UrlBuildErrorCode.UnknownOption`, …) and `CodeName` (`unknown_option`, …) are the codes every official client shares. A non-2xx answer throws an `XtandardHttpException` subclass chosen by status (`ForbiddenException`, `NotFoundException`, `TooManyRequestsException`, `ServiceUnavailableException`, …), with the server's `Status`, `Kind` and `RequestId`. Connection failures and timeouts throw `XtandardTransportException`. All of them derive from `XtandardException`.

```csharp
try
{
    await xi.InfoAsync("https://example.com/a.jpg");
}
catch (XtandardHttpException e)
{
    Console.WriteLine($"{(int)e.Status} {e.Kind} {e.RequestId}"); // 403 forbidden …
}
```

## Compatibility

- .NET 8 and .NET 10 (`net8.0`, `net10.0`). Nullable annotations, trimming- and AOT-compatible (a source-generated `System.Text.Json` context, no reflection).
- `Xtandard.Image` has no package dependencies. `Xtandard.Image.Extensions` depends on `Microsoft.Extensions.Http` (MIT).
- The package version is the server version: `Xtandard.Image` 0.2.1 matches `ghcr.io/xantiagoma/xtandard-image:0.2.1`. Versions follow [ZeroVer](https://0ver.org): a minor bump may break the API, a patch does not.

## Links

- [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme) and [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md)
- [URL dialects](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md): every option and signing scheme
- [Playground](https://github.com/xantiagoma/xtandard-image/blob/main/docs/PLAYGROUND.md): build URLs in a browser and copy the C# code
- [Changelog](https://github.com/xantiagoma/xtandard-image/blob/main/CHANGELOG.md)
- [Contributing](https://github.com/xantiagoma/xtandard-image/blob/main/CONTRIBUTING.md) and [the official clients](https://github.com/xantiagoma/xtandard-image/blob/main/clients/README.md)
- [Security policy](https://github.com/xantiagoma/xtandard-image/blob/main/SECURITY.md)
- Licence: [MIT](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

A personal project, provided as is under the MIT licence: see the [disclaimer](https://github.com/xantiagoma/xtandard-image#disclaimer).

Design: every official client is generated from one spec and passes one conformance fixture ([ADR 0013](https://github.com/xantiagoma/xtandard-image/blob/main/docs/adr/0013-official-clients.md)).
