<p align="center"><img src="https://raw.githubusercontent.com/xantiagoma/xtandard-image/main/docs/assets/logo-128.png" alt="xtandard-image" width="128" height="128"></p>

# xtandard-image for Ruby

**URL builder, signing and HTTP client for the xtandard-image server, with RBS types and no runtime dependencies.**

[![RubyGems: pending](https://img.shields.io/badge/RubyGems-pending-lightgrey)](https://github.com/xantiagoma/xtandard-image/blob/main/docs/RELEASE.md) <!-- registry-status: pending -->
[![license: MIT](https://img.shields.io/badge/license-MIT-blue)](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

`xtandard-image` is the Ruby client for [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme), an open-source media server that covers your application's image and video needs, built on libvips and FFmpeg. The gem builds and signs the URLs your code sends to the server, and wraps its `/info`, media and health endpoints on `Net::HTTP`; it does not process images itself. To run a server, see [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md), or start one with signing keys:

```sh
export XI_SIGNING_KEY="$(openssl rand -hex 32)" XI_SIGNING_SALT="$(openssl rand -hex 32)"
docker run -d --name xtandard-image -p 8080:8080 \
  -e XI_SOURCES=web -e XI_SOURCES_WEB_TYPE=http -e XI_SOURCES_WEB_ALLOW=images.unsplash.com \
  -e XI_SIGNING_KEYS="$XI_SIGNING_KEY" -e XI_SIGNING_SALTS="$XI_SIGNING_SALT" \
  ghcr.io/xantiagoma/xtandard-image:0.1.1
```

## Install

```sh
bundle add xtandard-image   # or: gem install xtandard-image
```

Not published yet: publishing to RubyGems is being set up. Until then, install from the repository: <!-- registry-status: pending -->

```ruby
# Gemfile
gem "xtandard-image", git: "https://github.com/xantiagoma/xtandard-image", glob: "clients/ruby/*.gemspec"
```

## Quick start

Build a signed URL with the keys the server was started with:

```ruby
require "xtandard_image"

signing = XtandardImage::SigningKey.new(key: ENV.fetch("XI_SIGNING_KEY"), salt: ENV.fetch("XI_SIGNING_SALT"))
img = XtandardImage.imgproxy(base_url: "http://localhost:8080", signing: signing)

url = img.image("https://images.unsplash.com/photo-1506744038136-46273834b3fb")
         .resize(:fill, 1200, 630).gravity(:sm).extension("jpg").build
# http://localhost:8080/<signature>/rs:fill:1200:630/g:sm/aHR0cHM6….jpg
```

Ask the server about the source with the HTTP client:

```ruby
xi = XtandardImage::Client.new("http://localhost:8080", signing: signing)
info = xi.info("https://images.unsplash.com/photo-1506744038136-46273834b3fb")
puts info.values_at("format", "width", "height").join(" ") # jpeg 7360 4912
```

Sign URLs on a server only: the signing key signs any URL, and the encryption key decrypts every `enc/` source.

## Usage

### Building URLs

There are two equivalent forms, an options Hash and a fluent chain with one snake_case method per option name and alias:

```ruby
img = XtandardImage.imgproxy(base_url: "https://img.example.com")

img.url("s3://photos/cat.jpg", { resize: [:fill, 300, 400], gravity: :sm, quality: 80 }, extension: "webp")
img.image("s3://photos/cat.jpg").resize(:fill, 300, 400).g(:sm).quality(80).extension("webp").build
# both: https://img.example.com/insecure/rs:fill:300:400/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp
```

Keys are option names or aliases (symbols or strings), values are argument arrays or a bare first argument; `nil` inside an array is an empty argument. `to_s` builds too, so chains interpolate. The RBS signatures in `sig/` type every option, so Steep flags an unknown option or a wrong argument. The options themselves are documented in [URL dialects §3.2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#32-processing-options--tr).

Builder settings: `separator:` (default `:`), `unsigned_signature:` (`insecure`, `unsafe` or `_`), and `option_names:` (`:short` by default, `:canonical` or `:given`).

### Chained pipelines

An Array of Hashes, or `pipeline` in the fluent form, writes `/-/` between pipelines:

```ruby
img.url("s3://photos/cat.jpg", [{ rs: ["fit", 500, 500] }, { bl: 2 }])
img.image("s3://photos/cat.jpg").rs("fit", 500, 500).pipeline.bl(2).build
```

### Sources

| `source_encoding:` | URL |
|---|---|
| `:base64` (default) | `/…/czM6Ly9waG90b3MvY2F0LmpwZw.webp`; `split_source: 16` splits it into segments |
| `:plain` | `/…/plain/https://e.com/a%20b.jpg@webp`, escaped for you |
| `:encrypted` | `/…/enc/<token>.webp`; needs `encryption:` (below) |

Set it on the builder, per call or per chain (`.encoding(:plain)`). `XtandardImage.xi_source("photos", "2026/cat.jpg")` writes a named source (`xi://photos/2026/cat.jpg`) and `XtandardImage.source_url("s3", "bucket", "cat.jpg")` a storage URL.

### `/info`

```ruby
img.info_url("s3://photos/cat.jpg", { size: true, format: true })
img.info("s3://photos/cat.jpg").size(true).format(true).build
```

### Video and streams

`/video` URLs use the video option catalogue; the extension is the container (`mp4`, `webm`, `mp3`, `png` for a waveform, …). `stream_url` builds HLS and DASH manifests, whose segments a player fetches without any key:

```ruby
img.video("s3://media/talk.mov").rs(:fill, 640, 360).vs(12).vd(5).extension("webm").build
img.stream_url("s3://media/talk.mov", protocol: :hls, renditions: [360, 720], segment_duration: 4)
```

The server needs the `full` or `gpl` image for video. Options and containers: [URL dialects §3.8](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#38-video-transformations-wp-122-wp-123) and [§3.10](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#310-hls-and-dash-streams-wp-126-every-dialect).

### Query dialect

`XtandardImage.query_urls` builds `/q/` URLs (native keys plus imgproxy options in `o=`), `/q/video` and imaginary endpoints, signed with `xi_sig`. The server needs `XI_DIALECTS_QUERY_ENABLED=true`.

```ruby
q = XtandardImage.query_urls(base_url: "https://img.example.com", signing: signing)
q.url("s3://photos/cat.jpg", params: { w: 300, fit: "cover" }, options: { bl: 2 }, extension: "webp")
# https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&fit=cover&o=bl:2&format=webp&xi_sig=…
q.imaginary_url(:smartcrop, "s3://photos/cat.jpg", { width: 300, height: 300 })
```

`XtandardImage.query_method_request(url)` turns such a URL into an HTTP QUERY request, and `Client.new(…, query_method: true)` sends them that way. See [URL dialects §4](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#4-query-dialect-native--imaginary).

### Signing and encryption

`SigningKey` takes the server's hex key and salt (`XI_SIGNING_KEYS` / `XI_SIGNING_SALTS`, or imgproxy's `IMGPROXY_KEY` / `IMGPROXY_SALT`) and an optional `size:`. `EncryptionKey` takes the first `XI_ENCRYPTION_KEYS` entry, for `enc/` sources and password-protected PDFs:

```ruby
img = XtandardImage.imgproxy(
  base_url: "http://localhost:8080",
  signing: signing,
  encryption: XtandardImage::EncryptionKey.new(key: ENV.fetch("XI_ENCRYPTION_KEY")),
)
img.url("s3://private/cat.jpg", { w: 300 }, source_encoding: :encrypted)
img.url("s3://docs/contract.pdf", { rs: ["fit", 800, 0] }, pdf_password: "s3cret")
```

Each `enc/` token gets a random IV unless the key has a fixed 16-byte `iv:`. The helpers `sign`, `sign_imgproxy_path`, `sign_xi_url`, `sign_xi_query`, `encrypt_source` and `encrypt_pdf_password` are module functions of `XtandardImage`; `sign_xi_url` adds an `xi_sig` to a URL of any other dialect. Schemes: [URL dialects §2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#2-signing-schemes-at-a-glance).

The [`imgproxy`](https://github.com/imgproxy/imgproxy.rb) gem works against the server too, with the same key and salt; this gem adds `/video`, streams, the query dialect, `xi://` sources and the typed options.

### HTTP client

`XtandardImage::Client` has `healthz`, `readyz`, `info`, `image`, `video`, `stream`, `media` (with `range:` and `method: :head`) and `playground_meta`. A block streams a large body chunk by chunk, and each request opens its own connection, so a client can be shared between threads:

```ruby
res = xi.image("s3://photos/cat.jpg", { w: 300 }, accept: "image/avif,image/webp")
res.status; res.content_type; res.body
File.open("talk.webm", "wb") do |f|
  xi.video("s3://media/talk.mov", { vd: 30 }, extension: "webm") { |chunk| f.write(chunk) }
end
```

### Rails

Build URLs in a helper, with the keys in credentials:

```ruby
# config/initializers/xtandard_image.rb
XI = XtandardImage.imgproxy(
  base_url: ENV.fetch("XI_URL", "https://img.example.com"),
  signing: XtandardImage::SigningKey.new(
    key: Rails.application.credentials.dig(:xtandard_image, :key),
    salt: Rails.application.credentials.dig(:xtandard_image, :salt),
  ),
)

# app/helpers/xtandard_image_helper.rb: <%= image_tag xi_url(user.avatar, rs: [:fill, 256, 256]) %>
module XtandardImageHelper
  def xi_url(attachment, extension: "webp", **options)
    blob = attachment.blob
    source = blob.service.is_a?(ActiveStorage::Service::S3Service) ? "s3://#{blob.service.bucket.name}/#{blob.key}" : blob.url(expires_in: 1.hour)
    XI.url(source, options, extension: extension)
  end
end
```

### Errors

Every error derives from `XtandardImage::Error`. The builders raise `URLBuildError` with a `code` symbol such as `:unknown_option`, `:invalid_argument` or `:invalid_source`. A non-2xx answer raises an `HTTPError` subclass chosen by status (`BadRequestError`, `ForbiddenError`, `NotFoundError`, `TooManyRequestsError`, `ServiceUnavailableError`, …) with the server's `status`, `kind`, `message` and `request_id`; a connection failure raises `TransportError`.

```ruby
begin
  xi.info("https://example.com/a.jpg")
rescue XtandardImage::HTTPError => e
  puts "#{e.status} #{e.kind} #{e.request_id}" # 403 forbidden …
end
```

## Compatibility

- Ruby 3.2, 3.3, 3.4 and 4.0. No runtime dependencies: `openssl`, `net/http` and `json` come with Ruby. `json` declares only the Ruby licence, the one exception to the project's MIT/BSD/Apache/ISC licence policy (the Ruby licence offers BSD-2-Clause as an alternative).
- RBS signatures in `sig/`, clean under Steep in strict mode.
- The gem version is the server version: `xtandard-image 0.2.1` matches `ghcr.io/xantiagoma/xtandard-image:0.2.1`. Versions follow [ZeroVer](https://0ver.org): a minor bump may break the API, a patch does not.

## Links

- [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme) and [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md)
- [URL dialects](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md): every option and signing scheme
- [Playground](https://github.com/xantiagoma/xtandard-image/blob/main/docs/PLAYGROUND.md): build URLs in a browser and copy the Ruby code
- [Changelog](https://github.com/xantiagoma/xtandard-image/blob/main/CHANGELOG.md)
- [Contributing](https://github.com/xantiagoma/xtandard-image/blob/main/CONTRIBUTING.md) and [the official clients](https://github.com/xantiagoma/xtandard-image/blob/main/clients/README.md)
- [Security policy](https://github.com/xantiagoma/xtandard-image/blob/main/SECURITY.md)
- Licence: [MIT](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

A personal project, provided as is under the MIT licence: see the [disclaimer](https://github.com/xantiagoma/xtandard-image#disclaimer).

Design: every official client is generated from one spec and passes one conformance fixture ([ADR 0013](https://github.com/xantiagoma/xtandard-image/blob/main/docs/adr/0013-official-clients.md)).
