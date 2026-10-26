<p align="center"><img src="https://raw.githubusercontent.com/xantiagoma/xtandard-image/main/docs/assets/logo-128.png" alt="xtandard-image" width="128" height="128"></p>

# xtandard-image for Ruby

The official Ruby client of [xtandard-image](https://github.com/xantiagoma/xtandard-image) ([ADR 0013](../../docs/adr/0013-official-clients.md)). It has:

- URL builders for the imgproxy dialect (processing, `/info`, `/video`, HLS and DASH) and the query dialect (`/q/`, `/q/video`, imaginary endpoints), as option Hashes or fluent chains with one snake_case method per option name and alias;
- imgproxy and `xi_sig` signing;
- `enc/` source and PDF password encryption;
- an HTTP client on `Net::HTTP` with Range, HEAD and streaming to a block.

It needs Ruby 3.2 or later and has **no runtime dependencies** (`openssl`, `net/http` and `json` from the standard library). It ships RBS signatures (`sig/`), generated from the server's catalogues for every option, and is clean under Steep in strict mode.

It is not on RubyGems yet. The gem `xtandard-image` will follow the server's release tags.

**Build and sign URLs on a server.** The signing key signs every URL, and the encryption key decrypts every `enc/` source and PDF password.

## Install (from the repository, until the first release)

```ruby
# Gemfile
gem "xtandard-image", git: "https://github.com/xantiagoma/xtandard-image", glob: "clients/ruby/*.gemspec"
```

```ruby
require "xtandard_image"
```

## URL builder

```ruby
key = XtandardImage::SigningKey.new(key: ENV.fetch("XI_SIGNING_KEY"), salt: ENV.fetch("XI_SIGNING_SALT"))
img = XtandardImage.imgproxy(
  base_url: "https://img.example.com",
  signing: key,
  encryption: XtandardImage::EncryptionKey.new(key: ENV.fetch("XI_ENCRYPTION_KEY")), # only for enc/ sources and PDF passwords
)

# Hash form: keys are option names or aliases (symbols or strings), values are argument
# arrays or a bare first argument. nil inside an array is an empty argument; a nil value skips the option.
img.url("s3://photos/cat.jpg", { rs: [:fill, 1200, 630], g: :sm, q: 80 }, extension: "webp")
# https://img.example.com/<signature>/rs:fill:1200:630/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp

# Fluent form: one method per option name and alias (min-width is min_width).
img.image(XtandardImage.xi_source("photos", "2026/cat.jpg")).resize("fit", 300, 300).blur(2).extension("png").build
img.image("s3://photos/cat.jpg").w(300).to_s # to_s builds too, so chains interpolate

# Chained pipelines (/-/): an Array of Hashes, or pipeline in the fluent form.
img.url("s3://photos/cat.jpg", [{ rs: ["fit", 500, 500] }, { bl: 2 }])
img.image("s3://photos/cat.jpg").rs("fit", 500, 500).pipeline.bl(2).build

# Plain or encrypted sources, a password-protected PDF.
img.url("https://example.com/a b.jpg", { w: 300 }, source_encoding: :plain)
img.url("s3://private/cat.jpg", { w: 300 }, source_encoding: :encrypted)
img.url("s3://docs/report.pdf", { rs: ["fit", 800, 0] }, pdf_password: "s3cret", extension: "png")

# /info, /video, HLS and DASH.
img.info_url("s3://photos/cat.jpg", { size: true, format: true, dimensions: true })
img.video_url("s3://media/talk.mov", { rs: ["fill", 640, 360], vs: 12, vd: 8 }, extension: "webm")
img.video("s3://media/talk.mov").start(12).duration(8).mute(true).extension("mp4").build
img.stream_url("s3://media/talk.mov", protocol: :hls, renditions: [360, 720], segment_duration: 4)
```

Enumerations (source encodings, protocols, enum arguments) take symbols or strings. Numbers are written as every official client writes them (`300`, `1.5`, `1e-7`), booleans as `1`/`0`.

A URL that cannot be built raises `XtandardImage::URLBuildError` with a `code` symbol from the shared conformance fixture: `:unknown_option`, `:invalid_argument`, `:invalid_source`, `:invalid_extension`, `:invalid_separator`, `:missing_signing`, `:missing_encryption`, `:invalid_key`, `:invalid_password`, `:invalid_request`, `:invalid_name`, `:invalid_endpoint` or `:invalid_range`.

### Types

`sig/` has RBS signatures for the whole API. The generated part (`sig/generated/`) types every option: the options Hash is a record (`processing_options`, `info_options`, `video_options`) whose keys are the option names and aliases, and each fluent method has one overload per argument shape. Under Steep, an unknown option, a wrong enum value or a wrong argument type is a type error:

```ruby
img.url("x", { q: "high" })         # Cannot pass a value of type Hash[Symbol, String] as processing_input
img.image("x").resize(:wrong, "a")  # Cannot find compatible overloading of method `resize`
```

`Client#info` returns an `info_response` record (string keys, as `JSON.parse` gives them).

## Query dialect

```ruby
q = XtandardImage.query_urls(base_url: "https://img.example.com", signing: key)
q.url("s3://photos/cat.jpg", params: { w: 300, f: "webp" }, options: { bl: 2 })
# https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&f=webp&o=bl:2&xi_sig=…
q.image("s3://photos/cat.jpg").param(:w, 300).bl(2).build
q.video_url("s3://media/talk.mov", { rs: ["fill", 640, 360], vs: 1 }, extension: "webm")
q.stream_url("s3://media/talk.mov", protocol: :hls, renditions: [360])
q.imaginary_url(:resize, "s3://photos/cat.jpg", { width: 300 })
```

## HTTP client

```ruby
xi = XtandardImage::Client.new("http://localhost:8080", signing: key, read_timeout: 30)
xi.healthz
xi.readyz.ready?                                                  # 503 starting/draining is not an error
info = xi.info("s3://photos/cat.jpg", { size: true, format: true }) # info["width"], info["format"], …
res = xi.image("s3://photos/cat.jpg", { w: 300 }, accept: "image/avif,image/webp")
res.status; res.content_type; res.body                            # body is a binary String
part = xi.video("s3://media/talk.mov", { vd: 5 }, extension: "webm", range: 0..1023)  # 206
head = xi.video("s3://media/talk.mov", { vd: 5 }, extension: "webm", method: :head)

# Stream a large body to a block, chunk by chunk (the Response then has no body).
File.open("talk.webm", "wb") do |f|
  xi.video("s3://media/talk.mov", { vd: 30 }, extension: "webm") { |chunk| f.write(chunk) }
end

manifest = xi.stream("s3://media/talk.mov", protocol: :hls, renditions: [360, 720])
segment = xi.media(manifest.body.lines(chomp: true).last) # ticket child URLs need no signing
```

`range:` takes a `Range` (`0..1023`, `0...1024`, `500..`), `XtandardImage::ByteSuffix.new(length: 500)` (the last 500 bytes), a Hash (`{ start: 0, end: 1023 }`, `{ suffix: 500 }`) or a header string; `XtandardImage.range_header` formats one. Each request opens its own connection, so a client can be shared between threads.

Errors form one hierarchy under `XtandardImage::Error`:

- `URLBuildError` (with `code`), raised before any request;
- `TransportError` (connection, TLS, timeout; `cause` is the underlying exception);
- `HTTPError`, with `status`, `kind`, `message`, `request_id` and `body` from the server's JSON error envelope, and a subclass by status: `BadRequestError` (400), `ForbiddenError` (403), `NotFoundError` (404), `NotAcceptableError` (406), `UnprocessableError` (422), `TooManyRequestsError` (429), `ServerError` (5xx) with `ServiceUnavailableError` (503) and `GatewayError` (502, 504).

### HTTP QUERY (opt-in)

The query dialect also accepts the HTTP QUERY method (RFC 10008): the URL's query moves into the request content, so long option chains, watermark text or encrypted sources stay out of URLs. It is off by default, because many CDNs and proxies neither cache nor forward QUERY; the server needs `XI_SERVER_QUERY_METHOD` (on by default).

```ruby
r = XtandardImage.query_method_request(q.url("s3://photos/cat.jpg", params: { w: 300 }))
r.http_method  # "QUERY"
r.url          # "https://img.example.com/q/"
r.content_type # "application/x-www-form-urlencoded"
r.body         # "url=s3://photos/cat.jpg&w=300&xi_sig=…"

xi.media(signed_query_url, method: :query)                                  # one call
xi = XtandardImage::Client.new("http://localhost:8080", query_method: true) # every media URL under query_mount ("/q")
```

The content is the built URL's query as written, `xi_sig` included (the signature does not cover the method). A URL without a query, or with a fragment, raises `URLBuildError` with code `:invalid_request`. HEAD and an explicit `method: :get` are unchanged. The member is `http_method`, not `method`, so `Object#method` keeps working.

## Rails

Build URLs in a helper. This one mirrors Active Storage's `variant` and `url_for`, and reads the keys from credentials:

```ruby
# config/initializers/xtandard_image.rb
XI = XtandardImage.imgproxy(
  base_url: ENV.fetch("XI_URL", "https://img.example.com"),
  signing: XtandardImage::SigningKey.new(
    key: Rails.application.credentials.dig(:xtandard_image, :key),
    salt: Rails.application.credentials.dig(:xtandard_image, :salt),
  ),
)

# app/helpers/xtandard_image_helper.rb
module XtandardImageHelper
  # <%= image_tag xi_url(user.avatar, rs: [:fill, 256, 256], g: :sm), alt: user.name %>
  def xi_url(attachment, extension: "webp", **options)
    XI.url(xi_source_for(attachment), options, extension: extension)
  end

  # <%= image_tag xi_url(photo, w: 800), srcset: xi_srcset(photo, [400, 800, 1600]) %>
  def xi_srcset(attachment, widths, **options)
    widths.map { |w| "#{xi_url(attachment, **options, w: w)} #{w}w" }.join(", ")
  end

  private

  # S3 blobs are read straight from the bucket (the server needs read access; GCS
  # works the same with gs://<bucket>/<key>); anything else through its signed URL.
  def xi_source_for(attachment)
    blob = attachment.blob
    if blob.service.is_a?(ActiveStorage::Service::S3Service)
      "s3://#{blob.service.bucket.name}/#{blob.key}"
    else
      blob.url(expires_in: 1.hour)
    end
  end
end
```

A named source (`XI_SOURCES` on the server) keeps bucket names out of URLs: return `XtandardImage.xi_source("uploads", blob.key)` instead.

## The community `imgproxy` gem

xtandard-image serves imgproxy URLs, so the community [`imgproxy`](https://github.com/imgproxy/imgproxy.rb) gem (`Imgproxy.url_for`, `Imgproxy.extend_active_storage!`, `user.avatar.imgproxy_url(width: 500)`) works against it unchanged: point `IMGPROXY_ENDPOINT` at the server and set `IMGPROXY_KEY` / `IMGPROXY_SALT` (and `IMGPROXY_SIGNATURE_SIZE`, `IMGPROXY_SOURCE_URL_ENCRYPTION_KEY`) to the server's `XI_SIGNING_KEYS`, `XI_SIGNING_SALTS`, `XI_SIGNING_SIZE` and `XI_ENCRYPTION_KEYS`. URLs from both gems are interchangeable, and an app can move one call at a time.

Use this gem for what imgproxy does not have: `/video`, HLS and DASH, the query dialect and `xi_sig`, `xi://` named sources, PDF passwords as xtandard-image writes them, the typed option tables of every server option, and the HTTP client.

## Coverage

| Server feature | Ruby |
|---|---|
| imgproxy processing options (90 with aliases), typed per option | `processing_options` record, fluent methods, every name in the shared builder fixture |
| `/info` options (35) | `info_options`, `info_url`, `info`, `Client#info` (`info_response`) |
| `/video` options (52) and all 12 containers; `/video` signing | `video_options`, `video_url`, `video`, `Client#video` with Range, HEAD and block streaming |
| HLS and DASH (`st`, `sgd`, `sgf:ts`, `@m3u8`, `@mpd`) | `stream_url` on both builders, `Client#stream`; ticket child URLs through `Client#media` |
| Password-protected PDFs (`pdp`) | `pdf_password:` / `with_pdf_password`, `encrypt_pdf_password` |
| Sources: base64, `plain/`, `enc/`, split; `xi://`, `s3://`, `gs://`, `abs://`, `local:///`, `ftp://`, `sftp://` | `source_encoding:`, `split_source:`, `xi_source`, `source_url` |
| Query dialect `/q/`, `/q/video`, imaginary; `xi_sig` | `QueryURLBuilder` (`query_image_params`, `imaginary_params`), `sign_xi_url` |
| HTTP QUERY on the query dialect (opt-in) | `query_method_request` / `QueryMethodRequest.from_url`, `Client#media(url, method: :query)`, `Client.new(query_method: true, query_mount: …)` |
| `/healthz`, `/readyz`, playground `api/meta` | `healthz`, `readyz`, `playground_meta` |

Not covered, on purpose (as in the TypeScript client):

- other dialects' URL shapes, which their own SDKs build;
- imaginary's `sign` scheme;
- presets-only URLs;
- the playground's POST API;
- an async client: Ruby code that wants concurrency runs the thread-safe `Client` from threads or fibers.

## Development

```sh
cd clients/ruby
mise install                 # Ruby pinned in mise.toml (Bundler ships with it)
task ci                      # = task -d clients/ruby ci from the root: lint, types, tests, licences, build
bundle exec rake test        # conformance + builder fixture + vectors + unit + type usage + client tests
bundle exec steep check      # strict, on lib/ and test/typecheck/
XI_SMOKE_URL=http://127.0.0.1:8080 ruby -Ilib -Itest test/smoke_test.rb   # against the playground demo
```

The development tools (RuboCop, Steep, RBS) need Ruby 3.3 or later; `task test:bare` runs the tests without Bundler on any supported Ruby, as CI does on 3.2. Steep was chosen over Sorbet because RBS is Ruby's own signature format (bundled with Ruby, read by Steep, TypeProf and IDEs), and it needs nothing in the runtime code: Sorbet RBIs would need `sorbet-runtime` or `T::Sig` to be useful.

`lib/xtandard_image/generated/` and `sig/generated/` are generated from `clients/spec/spec.json` by `task clients:generate` (`clients/codegen/emitters/ruby.ts`). Do not edit them.

Runtime dependencies: none. `scripts/check_licenses.rb` checks the standard library gems the code loads (`json`, `net-http`, `net-protocol`, `openssl`, `timeout`, `uri`, `zlib`): all are `Ruby OR BSD-2-Clause` except `json`, which declares only `Ruby`, a documented exception (the Ruby licence offers the 2-clause BSD licence as an alternative).
