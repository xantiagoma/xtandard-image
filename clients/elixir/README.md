![xtandard-image](https://raw.githubusercontent.com/xantiagoma/xtandard-image/main/docs/assets/logo-128.png)

# xtandard-image for Elixir

**Typed URL builders, signing and a Req-based HTTP client for the xtandard-image server.**

[//]: # (registry-status: pending)
[![Hex: pending](https://img.shields.io/badge/Hex-pending-lightgrey)](https://github.com/xantiagoma/xtandard-image/blob/main/docs/RELEASE.md)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue)](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

`xtandard_image` is the Elixir client for [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme), an open-source media server that covers your application's image and video needs, built on libvips and FFmpeg. The package builds and signs the URLs your code sends to the server, and wraps its `/info`, media and health endpoints; it does not process images itself. To run a server, see [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md), or start one with signing keys:

```sh
export XI_SIGNING_KEY="$(openssl rand -hex 32)" XI_SIGNING_SALT="$(openssl rand -hex 32)"
docker run -d --name xtandard-image -p 8080:8080 \
  -e XI_SOURCES=web -e XI_SOURCES_WEB_TYPE=http -e XI_SOURCES_WEB_ALLOW=images.unsplash.com \
  -e XI_SIGNING_KEYS="$XI_SIGNING_KEY" -e XI_SIGNING_SALTS="$XI_SIGNING_SALT" \
  ghcr.io/xantiagoma/xtandard-image:0.1.1
```

## Install

```elixir
def deps do
  [
    {:xtandard_image, "~> 0.1.0"},
    # Only for XtandardImage.Client:
    {:req, "~> 0.7"}
  ]
end
```

[//]: # (registry-status: pending)
Not published yet: publishing to Hex is being set up. Until then, install from the repository:

```elixir
{:xtandard_image, github: "xantiagoma/xtandard-image", sparse: "clients/elixir"}
```

## Quick start

Build a signed URL with the keys the server was started with:

```elixir
alias XtandardImage.Imgproxy
alias XtandardImage.Processing, as: P

signing = XtandardImage.SigningKey.new!(System.fetch_env!("XI_SIGNING_KEY"), System.fetch_env!("XI_SIGNING_SALT"))
img = XtandardImage.imgproxy(base_url: "http://localhost:8080", signing: signing)

url =
  img
  |> Imgproxy.image("https://images.unsplash.com/photo-1506744038136-46273834b3fb")
  |> P.resize({:fill, 1200, 630})
  |> P.gravity(:sm)
  |> P.extension(:jpg)
  |> P.build!()
# http://localhost:8080/<signature>/rs:fill:1200:630/g:sm/aHR0cHM6….jpg
```

Ask the server about the source with the HTTP client (it needs `:req` in your deps):

```elixir
xi = XtandardImage.Client.new("http://localhost:8080", signing: signing)
{:ok, info} = XtandardImage.Client.info(xi, "https://images.unsplash.com/photo-1506744038136-46273834b3fb")
IO.puts("#{info.format} #{info.width} #{info.height}") # jpeg 7360 4912
```

Sign URLs on a server only: the signing key signs any URL, and the encryption key decrypts every `enc/` source.

## Usage

### Building URLs

There are two equivalent forms, an options keyword list and a pipe of functions, one per option name and alias:

```elixir
img = XtandardImage.imgproxy(base_url: "https://img.example.com")

{:ok, url} = Imgproxy.url(img, "s3://photos/cat.jpg", [resize: {:fill, 300, 400}, gravity: :sm, quality: 80], extension: :webp)

img
|> Imgproxy.image("s3://photos/cat.jpg")
|> P.resize({:fill, 300, 400})
|> P.g(:sm)
|> P.quality(80)
|> P.extension(:webp)
|> P.build!()
# both: https://img.example.com/insecure/rs:fill:300:400/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp
```

Values are an argument tuple or list, or the first argument alone; a `nil` value skips the option (`|> P.quality(opts[:quality])`). `XtandardImage.Processing`, `XtandardImage.Info` and `XtandardImage.Video` are generated from the server's catalogue with a typespec per option, so Dialyzer checks both forms (reserved words get a trailing `_`: `fn_/2`, `do_/2`). The options themselves are documented in [URL dialects §3.2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#32-processing-options--tr).

Builder settings: `separator:` (default `":"`), `unsigned_signature:` (`"insecure"`, `"unsafe"` or `"_"`), and `option_names:` (`:short` by default, `:canonical` or `:given`).

### Chained pipelines

A list of keyword lists, or `P.pipeline/1` in the fluent form, writes `/-/` between pipelines:

```elixir
Imgproxy.url!(img, "s3://photos/cat.jpg", [[rs: {:fit, 500, 500}], [bl: 2]])
img |> Imgproxy.image("s3://photos/cat.jpg") |> P.rs({:fit, 500, 500}) |> P.pipeline() |> P.bl(2) |> P.build!()
```

### Sources

| `source_encoding:` | URL |
|---|---|
| `:base64` (default) | `/…/czM6Ly9waG90b3MvY2F0LmpwZw.webp`; `split_source: 16` splits it into segments |
| `:plain` | `/…/plain/https://e.com/a%20b.jpg@webp`, escaped for you |
| `:encrypted` | `/…/enc/<token>.webp`; needs `encryption:` (below) |

Set it on the builder, per call or per chain (`P.encoding(:plain)`). `XtandardImage.xi_source!("photos", "2026/cat.jpg")` writes a named source (`xi://photos/2026/cat.jpg`) and `XtandardImage.source_url("s3", "bucket", "cat.jpg")` a storage URL (`{:ok, "s3://bucket/cat.jpg"}`).

### `/info`

```elixir
Imgproxy.info_url!(img, "s3://photos/cat.jpg", size: true, format: true)
img |> Imgproxy.info("s3://photos/cat.jpg") |> XtandardImage.Info.size(true) |> XtandardImage.Info.format(true) |> XtandardImage.Info.build!()
```

### Video and streams

`/video` URLs use the video option catalogue (`XtandardImage.Video`); the extension is the container (`:mp4`, `:webm`, `:mp3`, `:png` for a waveform, …). `stream_url!/3` builds HLS and DASH manifests, whose segments a player fetches without any key:

```elixir
Imgproxy.video_url!(img, "s3://media/talk.mov", [rs: {:fill, 640, 360}, vs: 12, vd: 5], extension: :webm)
Imgproxy.stream_url!(img, "s3://media/talk.mov", protocol: :hls, renditions: [360, 720], segment_duration: 4)
```

The server needs the `full` or `gpl` image for video. Options and containers: [URL dialects §3.8](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#38-video-transformations-wp-122-wp-123) and [§3.10](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#310-hls-and-dash-streams-wp-126-every-dialect).

### Query dialect

`XtandardImage.query_urls/1` builds `/q/` URLs (native keys plus imgproxy options in `o=`), `/q/video` and imaginary endpoints, signed with `xi_sig`. The server needs `XI_DIALECTS_QUERY_ENABLED=true`.

```elixir
alias XtandardImage.Query

q = XtandardImage.query_urls(base_url: "https://img.example.com", signing: signing)
Query.url!(q, "s3://photos/cat.jpg", params: [w: 300, fit: :cover], options: [bl: 2], extension: :webp)
# https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&fit=cover&o=bl:2&format=webp&xi_sig=…
Query.imaginary_url!(q, :smartcrop, "s3://photos/cat.jpg", width: 300, height: 300)
```

`XtandardImage.query_method_request/1` turns such a URL into an HTTP QUERY request, and `Client.new(url, query_method: true)` sends them that way. See [URL dialects §4](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#4-query-dialect-native--imaginary).

### Signing and encryption

`SigningKey.new!/2` takes the server's hex key and salt (`XI_SIGNING_KEYS` / `XI_SIGNING_SALTS`, or imgproxy's `IMGPROXY_KEY` / `IMGPROXY_SALT`). `EncryptionKey.new!/1` takes the first `XI_ENCRYPTION_KEYS` entry, for `enc/` sources and password-protected PDFs:

```elixir
img =
  XtandardImage.imgproxy(
    base_url: "http://localhost:8080",
    signing: signing,
    encryption: XtandardImage.EncryptionKey.new!(System.fetch_env!("XI_ENCRYPTION_KEY"))
  )

Imgproxy.url!(img, "s3://private/cat.jpg", [w: 300], source_encoding: :encrypted)
Imgproxy.url!(img, "s3://docs/contract.pdf", [rs: {:fit, 800, 0}], pdf_password: "s3cret")
```

Each `enc/` token gets a random IV unless the key has a fixed 16-byte IV. `XtandardImage` also has `sign/2`, `sign_imgproxy_path/2`, `sign_xi_url/3`, `sign_xi_query/3`, `encrypt_source/2` and `encrypt_pdf_password/2`; `sign_xi_url/3` adds an `xi_sig` to a URL of any other dialect. Schemes: [URL dialects §2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#2-signing-schemes-at-a-glance).

### HTTP client

`XtandardImage.Client` (on [Req](https://hex.pm/packages/req), compiled when your project depends on it) has `healthz/1`, `readyz/1`, `info/4`, `image/4`, `video/4`, `stream/3`, `media/3` (with `range:` and `method: :head`) and `playground_meta/1`. `into:` streams a large body, and `req_options:` passes options to Req:

```elixir
{:ok, resp} = XtandardImage.Client.image(xi, "s3://photos/cat.jpg", [w: 300], accept: "image/avif,image/webp")
XtandardImage.Client.video(xi, "s3://media/talk.mov", [vd: 60], extension: :mp4, into: File.stream!("talk.mp4"))
```

### Errors

Functions that can fail return `{:ok, value}` or `{:error, error}`, and their bang variants raise it:

- `%XtandardImage.Error{code: code}`: the URL could not be built; `code` is `:unknown_option`, `:invalid_argument`, `:invalid_source`, …;
- `%XtandardImage.HTTPError{}`: a non-2xx answer, with the server's `status`, `kind`, `message` and `request_id`, and a `reason` per status class (`:forbidden`, `:not_found`, `:too_many_requests`, …);
- `%XtandardImage.TransportError{}`: no response (connection, TLS, timeout).

```elixir
case XtandardImage.Client.info(xi, "https://example.com/a.jpg") do
  {:ok, info} -> info
  {:error, %XtandardImage.HTTPError{} = e} -> IO.puts("#{e.status} #{e.kind} #{e.request_id}") # 403 forbidden …
end
```

## Compatibility

- Elixir 1.17 or later on Erlang/OTP 27 or later (tested on Elixir 1.17 / OTP 27 and Elixir 1.20 / OTP 29).
- URL building, signing and encryption need only OTP's `:crypto`. The HTTP client needs the optional `:req` (`~> 0.7`).
- The package version is the server version: `xtandard_image 0.2.1` matches `ghcr.io/xantiagoma/xtandard-image:0.2.1`. Versions follow [ZeroVer](https://0ver.org): a minor bump may break the API, a patch does not.

## Links

- [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme) and [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md)
- [URL dialects](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md): every option and signing scheme
- [Playground](https://github.com/xantiagoma/xtandard-image/blob/main/docs/PLAYGROUND.md): build URLs in a browser and copy the Elixir code
- [Changelog](https://github.com/xantiagoma/xtandard-image/blob/main/CHANGELOG.md)
- [Contributing](https://github.com/xantiagoma/xtandard-image/blob/main/CONTRIBUTING.md) and [the official clients](https://github.com/xantiagoma/xtandard-image/blob/main/clients/README.md)
- [Security policy](https://github.com/xantiagoma/xtandard-image/blob/main/SECURITY.md)
- Licence: [MIT](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

A personal project, provided as is under the MIT licence: see the [disclaimer](https://github.com/xantiagoma/xtandard-image#disclaimer).

Design: every official client is generated from one spec and passes one conformance fixture ([ADR 0013](https://github.com/xantiagoma/xtandard-image/blob/main/docs/adr/0013-official-clients.md)).
