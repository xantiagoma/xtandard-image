![xtandard-image](https://raw.githubusercontent.com/xantiagoma/xtandard-image/main/docs/assets/logo-128.png)

# xtandard-image for Go

**Typed URL builder, signing and HTTP client for the xtandard-image server.**

[//]: # (registry-status: pending)
[![pkg.go.dev: pending](https://img.shields.io/badge/pkg.go.dev-pending-lightgrey)](https://github.com/xantiagoma/xtandard-image/blob/main/docs/RELEASE.md)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue)](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

`github.com/xantiagoma/xtandard-image/clients/go` is the Go client for [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme), an open-source media server that covers your application's image and video needs, built on libvips and FFmpeg. The module builds and signs the URLs your code sends to the server, and wraps its `/info`, media and health endpoints; it does not process images itself. To run a server, see [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md), or start one with signing keys:

```sh
export XI_SIGNING_KEY="$(openssl rand -hex 32)" XI_SIGNING_SALT="$(openssl rand -hex 32)"
docker run -d --name xtandard-image -p 8080:8080 \
  -e XI_SOURCES=web -e XI_SOURCES_WEB_TYPE=http -e XI_SOURCES_WEB_ALLOW=images.unsplash.com \
  -e XI_SIGNING_KEYS="$XI_SIGNING_KEY" -e XI_SIGNING_SALTS="$XI_SIGNING_SALT" \
  ghcr.io/xantiagoma/xtandard-image:0.1.0-rc.2
```

## Install

```sh
go get github.com/xantiagoma/xtandard-image/clients/go
```

[//]: # (registry-status: pending)
Not published yet: publishing to pkg.go.dev is being set up. Until then, install from the repository:

```sh
git clone --depth 1 https://github.com/xantiagoma/xtandard-image
go mod edit -replace=github.com/xantiagoma/xtandard-image/clients/go=./xtandard-image/clients/go
go mod tidy
```

The root package is named `xtandard`. Releases will be tagged `clients/go/vX.Y.Z`, at the server's version.

## Quick start

Build a signed URL with the keys the server was started with:

```go
package main

import (
	"fmt"
	"log"
	"os"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
	"github.com/xantiagoma/xtandard-image/clients/go/imgproxy"
)

func main() {
	key, err := xtandard.ParseSigningKey(os.Getenv("XI_SIGNING_KEY"), os.Getenv("XI_SIGNING_SALT"), xtandard.DefaultSignatureSize)
	if err != nil {
		log.Fatal(err)
	}
	img, err := imgproxy.New(imgproxy.Config{BaseURL: "http://localhost:8080", Signing: &key})
	if err != nil {
		log.Fatal(err)
	}
	url, err := img.Image("https://images.unsplash.com/photo-1506744038136-46273834b3fb").
		Resize("fill", 1200, 630).Gravity("sm").Extension("jpg").Build()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(url) // http://localhost:8080/<signature>/rs:fill:1200:630/g:sm/aHR0cHM6….jpg
}
```

Ask the server about the source with the HTTP client:

```go
xi, err := client.New(client.Config{URLs: imgproxy.Config{BaseURL: "http://localhost:8080", Signing: &key}})
if err != nil {
	log.Fatal(err)
}
in, err := xi.Info(context.Background(), "https://images.unsplash.com/photo-1506744038136-46273834b3fb", imgproxy.InfoParams{})
if err != nil {
	log.Fatal(err)
}
fmt.Println(*in.Format, *in.Width, *in.Height) // jpeg 7360 4912
```

Sign URLs on a server only: the signing key signs any URL, and the encryption key decrypts every `enc/` source.

## Usage

The module has the root package `xtandard` (keys, signing, encryption, sources, errors, response types), the builders `imgproxy` and `query`, the typed options `processing`, `info` and `video`, and `client`. Builders and clients are safe for concurrent use; a fluent chain builds one URL.

### Building URLs

There are two equivalent forms, a params struct and a fluent chain with one method per option name and alias:

```go
url, err := img.URL("s3://photos/cat.jpg", imgproxy.Params{
	Options: []xtandard.ProcessingOption{
		processing.Resize{Type: processing.ResizeTypeFill, Width: new(300), Height: new(400)},
		processing.GravitySm{},
		xtandard.Opt("q", 80),
	},
	Extension: "webp",
})
url, err = img.Image("s3://photos/cat.jpg").Resize("fill", 300, 400).G("sm").Quality(80).Extension("webp").Build()
// both: http://localhost:8080/<signature>/rs:fill:300:400/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp
```

The packages `processing`, `info` and `video` have one generated struct per option form, so a processing option cannot be passed where a `/video` option is expected; `xtandard.Opt(name, args...)` is the untyped form, checked at run time. A nil pointer or `""` writes an empty argument. The options themselves are documented in [URL dialects §3.2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#32-processing-options--tr).

`imgproxy.Config` also takes `Separator` (default `:`), `UnsignedSignature` (`insecure`, `unsafe` or `_`) and `OptionNames` (short names by default, canonical or as given).

### Chained pipelines

`Pipelines` in the params form, or `Pipeline()` in the fluent form, writes `/-/` between pipelines:

```go
url, err := img.URL(src, imgproxy.Params{
	Options:   []xtandard.ProcessingOption{xtandard.Opt("rs", "fit", 500, 500)},
	Pipelines: [][]xtandard.ProcessingOption{{xtandard.Opt("bl", 2)}},
})
url, err = img.Image(src).Rs("fit", 500, 500).Pipeline().Bl(2).Build()
```

### Sources

| `SourceEncoding` | URL |
|---|---|
| `xtandard.SourceEncodingBase64` (default) | `/…/czM6Ly9waG90b3MvY2F0LmpwZw.webp`; `SplitSource` splits it into segments |
| `xtandard.SourceEncodingPlain` | `/…/plain/https://example.com/a%20b.jpg@webp`, escaped for you |
| `xtandard.SourceEncodingEncrypted` | `/…/enc/<token>.webp`; needs `Encryption` (below) |

Set it on the `Config`, per URL (`Params.SourceEncoding`) or per chain (`.Encoding(…)`). `xtandard.XiSource("photos", "2026/cat.jpg")` writes a named source (`xi://photos/2026/cat.jpg`) and `xtandard.SourceURL("s3", "bucket", "cat.jpg")` a storage URL; both return an error for an invalid name.

### `/info`

```go
url, err := img.InfoURL(src, imgproxy.InfoParams{Options: []xtandard.InfoOption{info.Size{Size: new(true)}}})
url, err = img.Info(src).Size(true).Format(true).Build()
```

### Video and streams

`/video` URLs use the video option catalogue; the extension is the container (`mp4`, `webm`, `mp3`, `png` for a waveform, …). `StreamURL` builds HLS and DASH manifests, whose segments a player fetches without any key:

```go
url, err := img.Video("s3://media/talk.mov").Resize("fill", 640, 360).Start(12).Duration(5).Extension("webm").Build()
url, err = img.StreamURL("s3://media/talk.mov", imgproxy.StreamParams{
	Protocol: xtandard.StreamProtocolHLS, Renditions: []int{360, 720}, SegmentDuration: 4,
})
```

The server needs the `full` or `gpl` image for video. Options and containers: [URL dialects §3.8](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#38-video-transformations-wp-122-wp-123) and [§3.10](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#310-hls-and-dash-streams-wp-126-every-dialect).

### Query dialect

Package `query` builds `/q/` URLs (native keys plus imgproxy options in `o=`), `/q/video` and imaginary endpoints, signed with `xi_sig`. The server needs `XI_DIALECTS_QUERY_ENABLED=true`.

```go
q, err := query.New(query.Config{BaseURL: "https://img.example.com", Signing: &key})
url, err := q.URL("s3://photos/cat.jpg", query.Params{
	Params:    []query.Param{query.P(xtandard.QueryKeyW, 300), query.P(xtandard.QueryKeyFit, "cover")},
	Options:   []xtandard.ProcessingOption{xtandard.Opt("bl", 2)},
	Extension: "webp",
})
// https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&fit=cover&o=bl:2&format=webp&xi_sig=…
url, err = q.ImaginaryURL(xtandard.ImaginaryEndpointSmartcrop, "s3://photos/cat.jpg",
	query.P(xtandard.ImaginaryParamWidth, 300), query.P(xtandard.ImaginaryParamHeight, 300))
```

`query.MethodRequest(url)` turns a query URL into an HTTP QUERY request, and `client.Config{QueryMethod: true}` sends one. See [URL dialects §4](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#4-query-dialect-native--imaginary).

### Signing and encryption

`xtandard.ParseSigningKey` takes the server's hex key and salt (`XI_SIGNING_KEYS` / `XI_SIGNING_SALTS`, or imgproxy's `IMGPROXY_KEY` / `IMGPROXY_SALT`) and the signature size. `xtandard.ParseEncryptionKey` takes the first `XI_ENCRYPTION_KEYS` entry, for `enc/` sources and password-protected PDFs:

```go
ek, err := xtandard.ParseEncryptionKey(os.Getenv("XI_ENCRYPTION_KEY"), "") // "": a random IV per token
img, err := imgproxy.New(imgproxy.Config{BaseURL: base, Signing: &key, Encryption: &ek})
url, err := img.URL("s3://private/cat.jpg", imgproxy.Params{SourceEncoding: xtandard.SourceEncodingEncrypted})
url, err = img.URL("s3://docs/contract.pdf", imgproxy.Params{
	Options: []xtandard.ProcessingOption{xtandard.Opt("rs", "fit", 800, 0)}, PDFPassword: new("s3cret"),
})
```

`Sign`, `SignImgproxyPath`, `SignXiURL`, `SignXiQuery`, `EncryptSource` and `EncryptPDFPassword` are exported too; `SignXiURL` adds an `xi_sig` to a URL of any other dialect. Schemes: [URL dialects §2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#2-signing-schemes-at-a-glance).

### HTTP client

`client.New` wraps `net/http` (`Config.HTTPClient`, `http.DefaultClient` by default) with a `context.Context` on every call: `Healthz`, `Readyz`, `Info`, `Image`, `Video`, `Stream`, `Media` and `PlaygroundMeta`. Media calls return the `*http.Response` with the body unread; close it:

```go
res, err := xi.Image(ctx, src, imgproxy.Params{Options: opts}, client.Accept("image/avif,image/webp"))
defer res.Body.Close()
part, err := xi.Video(ctx, "s3://media/talk.mov", vp, client.WithRange(xtandard.ByteRange{Start: 0, End: 65535})) // 206
```

### Errors

Builders return a `*xtandard.BuildError` whose code (`xtandard.ErrUnknownOption`, `ErrInvalidArgument`, `ErrInvalidSource`, …) matches with `errors.Is`. The client returns a `*client.HTTPError` for a non-2xx answer, with the server's `Status`, `Kind` and `RequestID`, and a `*client.TransportError` when no answer came:

```go
_, err := img.Image(src).Option("nope", 1).Build()
errors.Is(err, xtandard.ErrUnknownOption) // true

_, err = xi.Info(ctx, "https://example.com/a.jpg", imgproxy.InfoParams{})
if he, ok := errors.AsType[*client.HTTPError](err); ok {
	log.Print(he.Status, he.Kind, he.RequestID) // 403 forbidden …
}
```

An `HTTPError` also unwraps to its kind: `errors.Is(err, xtandard.ErrorKindNotFound)`.

## Compatibility

- Go 1.26 or later (the two Go releases supported upstream). Standard library only: no third-party modules and no cgo.
- The module version is the server version: `clients/go/v0.2.1` matches `ghcr.io/xantiagoma/xtandard-image:0.2.1`. Versions follow [ZeroVer](https://0ver.org): a minor bump may break the API, and a patch does not.

## Links

- [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme) and [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md)
- [URL dialects](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md): every option and signing scheme
- [Playground](https://github.com/xantiagoma/xtandard-image/blob/main/docs/PLAYGROUND.md): build URLs in a browser and copy the Go code
- [Changelog](https://github.com/xantiagoma/xtandard-image/blob/main/CHANGELOG.md)
- [Contributing](https://github.com/xantiagoma/xtandard-image/blob/main/CONTRIBUTING.md) and [the official clients](https://github.com/xantiagoma/xtandard-image/blob/main/clients/README.md)
- [Security policy](https://github.com/xantiagoma/xtandard-image/blob/main/SECURITY.md)
- Licence: [MIT](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

A personal project, provided as is under the MIT licence: see the [disclaimer](https://github.com/xantiagoma/xtandard-image#disclaimer).

Design: every official client is generated from one spec and passes one conformance fixture ([ADR 0013](https://github.com/xantiagoma/xtandard-image/blob/main/docs/adr/0013-official-clients.md)).
