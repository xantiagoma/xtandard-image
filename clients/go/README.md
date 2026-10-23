# xtandard-image for Go

The official Go client of [xtandard-image](https://github.com/xantiagoma/xtandard-image) ([ADR 0013](../../docs/adr/0013-official-clients.md)). It has:

- URL builders for the imgproxy dialect (processing, `/info`, `/video`, HLS and DASH) and the query dialect (`/q/`, `/q/video`, imaginary endpoints), each as a call with a params struct or as a fluent chain;
- one typed struct per option of every catalogue (`processing.Resize`, `info.Size`, `video.Start`, …), generated from the server's catalogues;
- imgproxy and `xi_sig` signing;
- `enc/` source and PDF password encryption;
- an HTTP client on `net/http` with `context.Context` on every call, Range and HEAD, and streamed bodies.

It uses only the standard library (`crypto/hmac`, `crypto/aes`, `net/http`): no third-party module, no cgo, nothing from the server's `internal/`.

**Build and sign URLs on a server.** The signing key signs every URL, and the encryption key decrypts every `enc/` source and PDF password.

## Install

```sh
go get github.com/xantiagoma/xtandard-image/clients/go@latest
```

The module path is `github.com/xantiagoma/xtandard-image/clients/go` and the root package is named `xtandard`. Releases are tagged `clients/go/vX.Y.Z` (the Go module proxy's convention for a module in a subdirectory) and follow the server's release line in lockstep (`0.x` before 1.0). No tag exists yet; until the first release, `go get …@main` takes the current commit. Go modules need no registry, so the name cannot be taken: pkg.go.dev lists the module once a tag is fetched through the proxy.

### Go versions

`go.mod` says `go 1.26.0`: the oldest Go release still supported upstream (Go supports the two newest majors, 1.26 and 1.27). Users on either can depend on the client, and 1.26 brings what the API is designed around (`new(expr)` for the optional fields of the typed options, `errors.AsType`). Development uses go1.27.1, the server's toolchain (`mise.toml`), and CI tests 1.26 and 1.27. The minimum moves up when a Go release falls out of support.

## Packages

| Package | What |
|---|---|
| `xtandard` (the module root) | signing (`Sign`, `SignImgproxyPath`, `XiMessage`, `SignXiQuery`, `SignXiURL`, `SigningKey`), encryption (`EncryptSource`, `EncryptPDFPassword`, `EncryptionKey`), sources (`XiSource`, `SourceURL`, `EscapePlainSource`, `EscapeQueryValue`), byte ranges (`RangeHeader`, `ByteRange`, `ByteRangeFrom`, `ByteSuffix`, `RawRange`), the option model (`Option`, `Opt`, `ProcessingOption`, `InfoOption`, `VideoOption`, `Catalogue`), constants, `InfoResponse`/`ErrorResponse`, errors |
| `imgproxy` | `New(Config)`, `URL`, `InfoURL`, `VideoURL`, `StreamURL`; fluent `Image`, `Info`, `Video` |
| `query` | `New(Config)`, `URL`, `VideoURL`, `StreamURL`, `ImaginaryURL`; fluent `Image`, `Video`; `MethodRequest` (HTTP QUERY) |
| `processing`, `info`, `video` | one struct per option form (generated) |
| `client` | `New(Config)`: `Healthz`, `Readyz`, `Info`, `InfoAt`, `Image`, `Video`, `Stream`, `Media`, `PlaygroundMeta` |

## URL builder

```go
import (
	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
	"github.com/xantiagoma/xtandard-image/clients/go/imgproxy"
	"github.com/xantiagoma/xtandard-image/clients/go/processing"
)

sk, err := xtandard.ParseSigningKey(os.Getenv("XI_SIGNING_KEY"), os.Getenv("XI_SIGNING_SALT"), xtandard.DefaultSignatureSize)
ek, err := xtandard.ParseEncryptionKey(os.Getenv("XI_ENCRYPTION_KEY"), "") // only for enc/ sources and PDF passwords; "" = random IV
img, err := imgproxy.New(imgproxy.Config{BaseURL: "https://img.example.com", Signing: &sk, Encryption: &ek})

// Params form: typed options (package processing) and untyped xtandard.Opt values mix freely.
url, err := img.URL("s3://photos/cat.jpg", imgproxy.Params{
	Options: []xtandard.ProcessingOption{
		processing.Resize{Type: processing.ResizeTypeFill, Width: new(1200), Height: new(630)},
		processing.GravitySm{},
		xtandard.Opt("q", 80),
	},
	Extension: "webp",
})
// https://img.example.com/<signature>/rs:fill:1200:630/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp

// Fluent form: one method per option name and alias.
url, err = img.Image("xi://photos/2026/cat.jpg").Resize("fit", 300, 300).Blur(2).Extension("png").Build()

// Chained pipelines (/-/): Pipelines in the params form, Pipeline() in the fluent form.
url, err = img.URL("s3://photos/cat.jpg", imgproxy.Params{
	Options:   []xtandard.ProcessingOption{xtandard.Opt("rs", "fit", 500, 500)},
	Pipelines: [][]xtandard.ProcessingOption{{xtandard.Opt("bl", 2)}},
})

// Plain or encrypted sources, a password-protected PDF.
url, err = img.URL("https://example.com/a b.jpg", imgproxy.Params{SourceEncoding: xtandard.SourceEncodingPlain})
url, err = img.URL("s3://private/cat.jpg", imgproxy.Params{SourceEncoding: xtandard.SourceEncodingEncrypted})
url, err = img.URL("s3://docs/report.pdf", imgproxy.Params{PDFPassword: new("s3cret"), Extension: "png"})

// /info, /video, HLS and DASH.
url, err = img.InfoURL("s3://photos/cat.jpg", imgproxy.InfoParams{Options: []xtandard.InfoOption{info.Size{Size: new(true)}}})
url, err = img.VideoURL("s3://media/talk.mov", imgproxy.VideoParams{
	Options:   []xtandard.VideoOption{video.Start{Seconds: 12}, video.Duration{Seconds: 8}},
	Extension: "webm",
})
url, err = img.Video("s3://media/talk.mov").Start(12).Duration(8).Mute(true).Extension("mp4").Build()
url, err = img.StreamURL("s3://media/talk.mov", imgproxy.StreamParams{
	Protocol: xtandard.StreamProtocolHLS, Renditions: []int{360, 720}, SegmentDuration: 4,
})
```

### Typed options

Each option of each catalogue has a struct per argument form in `processing`, `info` or `video` (generated from `clients/spec/spec.json`). Fields follow the positional arguments:

- a required argument is a value (`processing.Quality{Quality: 80}`);
- an optional or nullable number or boolean is a pointer (`Width: new(300)`), and nil writes an empty argument (`rs:fill::300`);
- a string, colour or enum argument is a string type, and `""` writes an empty argument;
- an enum argument has its own type and constants (`processing.ResizeTypeFill`, `processing.GravityTypeNowe`);
- an option with several forms has one struct per form, named after the form's fixed value (`processing.Gravity`, `GravitySm`, `GravityFp`, `GravityObj`, `GravityObjw`);
- repeated arguments are a slice (`processing.Preset{Names: …}`, `processing.FormatQuality{Items: …}`).

Unset trailing arguments are left out. Every struct implements its catalogue's interface (`xtandard.ProcessingOption`, `InfoOption`, `VideoOption`), so a processing option cannot be passed where a `/video` option is expected. `xtandard.Opt(name, args...)` is the untyped escape hatch, and it is checked at run time like every option.

### Errors

Builders return a `*xtandard.BuildError` whose `Code` is one of the language-neutral codes of the conformance fixture. Each code is an `xtandard.ErrorCode` constant, which is an error, so match them with `errors.Is`:

```go
_, err := img.Image(src).Option("nope", 1).Build()
errors.Is(err, xtandard.ErrUnknownOption) // true
if be, ok := errors.AsType[*xtandard.BuildError](err); ok { log.Print(be.Code, be.Message) }
```

The codes are `ErrUnknownOption`, `ErrInvalidArgument`, `ErrInvalidSource`, `ErrInvalidExtension`, `ErrInvalidSeparator`, `ErrMissingSigning`, `ErrMissingEncryption`, `ErrInvalidKey`, `ErrInvalidPassword`, `ErrInvalidRequest`, `ErrInvalidName`, `ErrInvalidEndpoint` and `ErrInvalidRange`.

## Query dialect

```go
q, err := query.New(query.Config{BaseURL: "https://img.example.com", Signing: &sk})
url, err := q.URL("s3://photos/cat.jpg", query.Params{
	Params:  []query.Param{query.P(xtandard.QueryKeyW, 300), query.P(xtandard.QueryKeyF, "webp")},
	Options: []xtandard.ProcessingOption{xtandard.Opt("bl", 2)},
})
// https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&f=webp&o=bl:2&xi_sig=…
url, err = q.Video("s3://media/talk.mov").Resize("fill", 640, 360).Start(1).Extension("webm").Build()
url, err = q.StreamURL("s3://media/talk.mov", query.StreamParams{Protocol: xtandard.StreamProtocolHLS, Renditions: []int{360}})
url, err = q.ImaginaryURL(xtandard.ImaginaryEndpointResize, "s3://photos/cat.jpg", query.P(xtandard.ImaginaryParamWidth, 300))
```

## HTTP client

```go
c, err := client.New(client.Config{
	URLs:       imgproxy.Config{BaseURL: "http://localhost:8080", Signing: &sk},
	HTTPClient: &http.Client{}, // optional; http.DefaultClient otherwise
})
err = c.Healthz(ctx)
in, err := c.Info(ctx, "s3://photos/cat.jpg", imgproxy.InfoParams{Options: []xtandard.InfoOption{info.Size{Size: new(true)}}}) // *xtandard.InfoResponse
res, err := c.Image(ctx, "s3://photos/cat.jpg", imgproxy.Params{Options: opts}, client.Accept("image/avif,image/webp"))
defer res.Body.Close() // a streamed io.ReadCloser
part, err := c.Video(ctx, "s3://media/talk.mov", vp, client.WithRange(xtandard.ByteRange{Start: 0, End: 1023})) // 206
head, err := c.Video(ctx, "s3://media/talk.mov", vp, client.Head())
m, err := c.Stream(ctx, "s3://media/talk.mov", imgproxy.StreamParams{Protocol: xtandard.StreamProtocolHLS, Renditions: []int{360, 720}})
seg, err := c.Media(ctx, "/_stream/<ticket>/…") // ticket child URLs need no signing
```

Media calls return the `*http.Response` with the body unread; close it. A non-2xx response is a `*client.HTTPError` with `Status`, `Kind` (the server's error kind), `Message` and `RequestID`, read from the JSON envelope or, for a plain-text body, matched by status and message. It unwraps to its `xtandard.ErrorKind`, so `errors.Is(err, xtandard.ErrorKindNotFound)` matches a missing source. A request that got no response is a `*client.TransportError`, which unwraps to the cause (`context.DeadlineExceeded`, a dial error, …). A `Client` and a `Builder` are safe for concurrent use; a fluent chain is for one URL.

### HTTP QUERY (opt-in)

The query dialect also accepts the HTTP QUERY method ([RFC 10008](https://www.rfc-editor.org/rfc/rfc10008), [URL-DIALECTS §4.4](../../docs/URL-DIALECTS.md#44-http-query)): the URL's query goes in the request content, so long option chains, watermark text or encrypted sources stay out of URLs. `xi_sig` does not cover the method, so the content of a signed QUERY is the query of the signed GET URL. URL builders are unchanged.

```go
r, err := query.MethodRequest(url) // url from q.URL, q.VideoURL, q.StreamURL or q.ImaginaryURL
// query.Request{Method: "QUERY", URL: "https://img.example.com/q/",
//               ContentType: "application/x-www-form-urlencoded", Body: "url=s3://photos/cat.jpg&w=300&xi_sig=…"}

c, err := client.New(client.Config{URLs: imgproxy.Config{BaseURL: "http://localhost:8080"}, QueryMethod: true}) // QueryMount "/q" by default
res, err := c.Media(ctx, url)                 // QUERY for URLs under /q/, GET for everything else
res, err = c.Media(ctx, url, client.Query())  // one call, whatever QueryMethod says
res, err = c.Media(ctx, url, client.Get())    // GET even with QueryMethod
```

It is off by default. The server needs `XI_SERVER_QUERY_METHOD` (on by default), and many CDNs and proxies neither cache nor forward QUERY. `client.Head()` is sent unchanged. A URL without a query, or with a fragment, is an `xtandard.ErrInvalidRequest` error.

## Coverage

| Server feature | Go |
|---|---|
| imgproxy processing options (90 with aliases) | 101 typed structs in `processing` (one per form), 185 fluent methods (every name and alias), `xtandard.Opt`; every name in the shared builder fixture |
| `/info` options (35) | 41 structs in `info`, `InfoURL()`, `Info()`, `client.Info()` (typed `InfoResponse`) |
| `/video` options (52) and all 12 containers; `/video` signing | 52 structs in `video`, `VideoURL()`, `Video()`, `client.Video()` with Range and HEAD |
| HLS and DASH (`st`, `sgd`, `sgf:ts`, `@m3u8`, `@mpd`) | `StreamURL()` on both builders, `client.Stream()`; ticket child URLs through `client.Media()` |
| Password-protected PDFs (`pdp`) | `PDFPassword` / `WithPDFPassword()`, `EncryptPDFPassword()` |
| Sources: base64, `plain/`, `enc/`, split; `xi://`, `s3://`, `gs://`, `abs://`, `local:///`, `ftp://`, `sftp://` | `SourceEncoding`, `SplitSource`, `XiSource()`, `SourceURL()` |
| Query dialect `/q/`, `/q/video`, imaginary; `xi_sig` | package `query` (`QueryKey*` and `ImaginaryParam*` constants), `SignXiURL()` |
| HTTP QUERY on the query dialect (RFC 10008), off by default | `query.MethodRequest()`, `client.Config{QueryMethod, QueryMount}`, `client.Query()`, `client.Get()` |
| `/healthz`, `/readyz`, playground `api/meta` | `Healthz()`, `Readyz()`, `PlaygroundMeta()` |

Not covered, on purpose (as in the TypeScript and Python clients):

- other dialects' URL shapes, which their own SDKs build;
- imaginary's `sign` scheme;
- presets-only URLs;
- the playground's POST API.

Three conformance cases are skipped because the typed API cannot express them: chained `/info` and `/video` pipelines (`InfoParams` and `VideoParams` take one pipeline, and their chains have no `Pipeline()`), and a PDF password on `/video` (`VideoParams` has no such field). The runtime still refuses them when it is reached through other code.

## Development

```sh
cd clients/go
mise install          # Go 1.27.1 and golangci-lint 2.14.0, pinned in mise.toml
task ci               # = task -d clients/go ci from the root: lint (golangci-lint fmt and run, go vet, go fix -diff, go mod tidy -diff), go test -race, licences, build
go test ./...         # conformance + builder fixture + vectors + unit + stand-in server + Example tests
XI_SMOKE_URL=http://127.0.0.1:8080 go test -run Smoke -v ./client   # against the playground demo (task smoke)
```

The tests read the shared fixtures from the repository by relative path: `clients/spec/conformance.json`, `testdata/urls/imgproxy-builder.json` and `testdata/security/vectors.json`.

`zz_generated*.go` (in the root, `processing`, `info`, `video`, `imgproxy` and `query`) is generated from `clients/spec/spec.json` by `task clients:generate` (`clients/codegen/emitters/go.ts`, through `gofmt`). Do not edit it; golangci-lint skips it.

Runtime dependencies: none (standard library only). `scripts/check-licenses.sh` checks the closure against the ADR 0006 allowlist and fails on anything else; there are no exceptions.
