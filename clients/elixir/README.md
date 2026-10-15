# xtandard_image for Elixir

The official Elixir client of [xtandard-image](https://github.com/xantiagoma/xtandard-image) ([ADR 0013](https://github.com/xantiagoma/xtandard-image/blob/main/docs/adr/0013-official-clients.md)). It has:

- URL builders for the imgproxy dialect (processing, `/info`, `/video`, HLS and DASH) and the query dialect (`/q/`, `/q/video`, imaginary endpoints), with an options form (keyword lists) and a pipe-friendly fluent form;
- imgproxy and `xi_sig` signing;
- `enc/` source and PDF password encryption;
- an HTTP client on [Req](https://hex.pm/packages/req), compiled when your project depends on Req.

It needs Elixir 1.17 or later on Erlang/OTP 27 or later. URL building, signing and encryption have no dependencies beyond OTP's `:crypto`. Every public function has a typespec (`@spec`), the option tables and their types are generated from the server's catalogues, and the package is clean under Dialyzer.

It is not on Hex yet. The package `xtandard_image` (free on hex.pm as of 2026-10-15) will follow the server's release tags.

**Build and sign URLs on a server.** The signing key signs every URL, and the encryption key decrypts every `enc/` source and PDF password.

## Install (from the repository, until the first release)

```elixir
def deps do
  [
    {:xtandard_image,
     github: "xantiagoma/xtandard-image", sparse: "clients/elixir"},
    # Only for XtandardImage.Client:
    {:req, "~> 0.7"}
  ]
end
```

## URL builder

```elixir
alias XtandardImage.{EncryptionKey, Imgproxy, SigningKey}
alias XtandardImage.Processing, as: P

img =
  XtandardImage.imgproxy(
    base_url: "https://img.example.com",
    signing: SigningKey.new!(System.fetch_env!("XI_SIGNING_KEY"), System.fetch_env!("XI_SIGNING_SALT")),
    # only for enc/ sources and PDF passwords
    encryption: EncryptionKey.new!(System.fetch_env!("XI_ENCRYPTION_KEY"))
  )

# Options form: option names or aliases (atoms or strings) to an argument tuple
# or list, or the first argument alone. Order is URL order.
{:ok, url} = Imgproxy.url(img, "s3://photos/cat.jpg", [rs: {:fill, 1200, 630}, g: :sm, q: 80], extension: :webp)
# https://img.example.com/<signature>/rs:fill:1200:630/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp

# Fluent form: one function per option name and alias; nothing is checked until build.
img
|> Imgproxy.image(XtandardImage.xi_source!("photos", "2026/cat.jpg"))
|> P.resize({:fit, 300, 300})
|> P.blur(2)
|> P.extension(:png)
|> P.build!()

# Chained pipelines (/-/): a list of keyword lists, or P.pipeline/1.
Imgproxy.url!(img, "s3://photos/cat.jpg", [[rs: {:fit, 500, 500}], [bl: 2]])

# Plain or encrypted sources, a password-protected PDF.
Imgproxy.url!(img, "https://example.com/a b.jpg", [w: 300], source_encoding: :plain)
Imgproxy.url!(img, "s3://private/cat.jpg", [w: 300], source_encoding: :encrypted)
Imgproxy.url!(img, "s3://docs/report.pdf", [rs: {:fit, 800, 0}], pdf_password: "s3cret", extension: :png)

# /info, /video, HLS and DASH.
Imgproxy.info_url!(img, "s3://photos/cat.jpg", size: true, format: true, dimensions: true)
Imgproxy.video_url!(img, "s3://media/talk.mov", [rs: {:fill, 640, 360}, vs: 12, vd: 8], extension: :webm)

img
|> Imgproxy.video("s3://media/talk.mov")
|> XtandardImage.Video.start(12)
|> XtandardImage.Video.duration(8)
|> XtandardImage.Video.mute(true)
|> XtandardImage.Video.extension(:mp4)
|> XtandardImage.Video.build!()

Imgproxy.stream_url!(img, "s3://media/talk.mov", protocol: :hls, renditions: [360, 720], segment_duration: 4)
```

Functions that can fail return `{:ok, value}` or `{:error, %XtandardImage.Error{code: code}}`, and their bang variants raise the error. The codes are the language-neutral ones of the shared conformance fixture: `:unknown_option`, `:invalid_argument`, `:invalid_source`, `:invalid_extension`, `:missing_signing` and so on. A `nil` option value skips the option, which suits optional settings (`|> P.quality(opts[:quality])`).

`XtandardImage.Processing`, `XtandardImage.Info` and `XtandardImage.Video` are generated from the server's catalogues. Each has one function per option name and alias (`resize/2` and `rs/2`; `fn_/2` for `fn`, `do_/2` and `end_/2` for the reserved words), a typespec per option (`resize_value`, the argument tuples, lists and bare values, with enum values as atoms), and the `options` keyword type that Dialyzer checks the options form against.

## Query dialect

```elixir
alias XtandardImage.Query

q = XtandardImage.query_urls(base_url: "https://img.example.com", signing: key)
Query.url!(q, "s3://photos/cat.jpg", params: [w: 300, f: :webp], options: [bl: 2])
# https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&f=webp&o=bl:2&xi_sig=…
Query.video_url!(q, "s3://media/talk.mov", [rs: {:fill, 640, 360}, vs: 1], extension: :webm)
Query.stream_url!(q, "s3://media/talk.mov", protocol: :hls, renditions: [360])
Query.imaginary_url!(q, :resize, "s3://photos/cat.jpg", width: 300)
```

## HTTP client

Add `{:req, "~> 0.7"}` to your deps for `XtandardImage.Client`.

```elixir
alias XtandardImage.Client

xi = Client.new("http://localhost:8080", signing: key)
:ok = Client.healthz(xi)
{:ok, info} = Client.info(xi, "s3://photos/cat.jpg", size: true, format: true)  # %XtandardImage.InfoResponse{}
{:ok, resp} = Client.image(xi, "s3://photos/cat.jpg", [w: 300], accept: "image/avif,image/webp")
{:ok, part} = Client.video(xi, "s3://media/talk.mov", [vd: 5], extension: :webm, range: 0..1023)
{:ok, head} = Client.video(xi, "s3://media/talk.mov", [vd: 5], extension: :webm, method: :head)
{:ok, manifest} = Client.stream(xi, "s3://media/talk.mov", protocol: :hls, renditions: [360, 720])
# Ticket child URLs need no signing.
{:ok, segment} = Client.media(xi, manifest.body |> String.split("\n", trim: true) |> List.last())
# A large body, streamed:
Client.video(xi, "s3://media/talk.mov", [vd: 60], extension: :mp4, into: File.stream!("talk.mp4"))
```

Errors come back as `{:error, error}` (or are raised by the bang variants):

- `%XtandardImage.Error{}`: the URL could not be built;
- `%XtandardImage.HTTPError{}`: a non-2xx response, with `status`, `reason`, `kind`, `message` and `request_id` from the server's JSON error envelope. `reason` names the status class: `:bad_request` (400), `:forbidden` (403), `:not_found` (404), `:not_acceptable` (406), `:unprocessable` (422), `:too_many_requests` (429), `:service_unavailable` (503), `:gateway` (502, 504), `:server_error` (other 5xx);
- `%XtandardImage.TransportError{}`: no response (connection, TLS, timeout).

`req_options:` passes options to Req (timeouts, `:retry`, `:plug` in tests). The client sets `retry: false` and `decode_body: false` unless you override them.

## Phoenix

Build URLs in a function component, so templates never see the keys:

```elixir
defmodule MyAppWeb.Images do
  use Phoenix.Component

  alias XtandardImage.Imgproxy

  # Build the builder once from config (runtime.exs), e.g. in a module function.
  defp builder do
    config = Application.fetch_env!(:my_app, :xtandard_image)

    XtandardImage.imgproxy(
      base_url: config[:base_url],
      signing: XtandardImage.SigningKey.new!(config[:key], config[:salt])
    )
  end

  attr :src, :string, required: true, doc: "the source: s3://…, xi://…, https://…"
  attr :width, :integer, required: true
  attr :height, :integer, default: nil
  attr :format, :atom, default: :webp
  attr :rest, :global, include: ~w(alt class loading)

  def xi_img(assigns) do
    img = builder()
    w = assigns.width
    options = [rs: {:fit, w, assigns.height || 0}]
    srcset = for dpr <- [1, 2], do: "#{Imgproxy.url!(img, assigns.src, options ++ [dpr: dpr], extension: assigns.format)} #{dpr}x"

    assigns =
      assigns
      |> assign(:url, Imgproxy.url!(img, assigns.src, options, extension: assigns.format))
      |> assign(:srcset, Enum.join(srcset, ", "))

    ~H"""
    <img src={@url} srcset={@srcset} width={@width} height={@height} {@rest} />
    """
  end
end
```

```heex
<.xi_img src="s3://photos/cat.jpg" width={320} alt="A cat" loading="lazy" />
```

## Coverage

| Server feature | Elixir |
|---|---|
| imgproxy processing options (90 with aliases), typed per option | `XtandardImage.Processing` (types and functions), the options form; every name in the shared builder fixture |
| `/info` options (35) | `XtandardImage.Info`, `Imgproxy.info_url/4`, `Imgproxy.info/2`, `Client.info/4` (`%XtandardImage.InfoResponse{}`) |
| `/video` options (50) and all 12 containers; `/video` signing | `XtandardImage.Video`, `Imgproxy.video_url/4`, `Imgproxy.video/2`, `Client.video/4` with Range and HEAD |
| HLS and DASH (`st`, `sgd`, `sgf:ts`, `@m3u8`, `@mpd`) | `stream_url/3` on both builders, `Client.stream/3`; ticket child URLs through `Client.media/3` |
| Password-protected PDFs (`pdp`) | `pdf_password:` / `with_pdf_password/2`, `XtandardImage.encrypt_pdf_password/2` |
| Sources: base64, `plain/`, `enc/`, split; `xi://`, `s3://`, `gs://`, `abs://`, `local:///`, `ftp://`, `sftp://` | `source_encoding:`, `split_source:`, `xi_source/2`, `source_url/3` |
| Query dialect `/q/`, `/q/video`, imaginary; `xi_sig` | `XtandardImage.Query`, `XtandardImage.sign_xi_url/3` |
| `/healthz`, `/readyz`, playground `api/meta` | `Client.healthz/1`, `readyz/1`, `playground_meta/1` |

Not covered, on purpose (as in the TypeScript and Python clients):

- other dialects' URL shapes, which their own SDKs build;
- imaginary's `sign` scheme;
- presets-only URLs;
- the playground's POST API.

## Development

```sh
cd clients/elixir
task install                 # mise install erlang elixir (pinned in mise.toml), Hex, rebar3, deps
task ci                      # format, Credo --strict, Dialyzer, tests, licences, docs, hex.build
mix test                     # conformance + builder fixture + vectors + unit + doctests + client tests
XI_SMOKE_URL=http://127.0.0.1:8080 mix test test/smoke_test.exs   # against the playground demo
```

`mise.toml` pins the newest supported pair (OTP 29, Elixir 1.20); CI also tests the oldest (OTP 27, Elixir 1.17). mise installs both on macOS and Linux, so no Docker image is needed.

`lib/xtandard_image/generated/` is generated from `clients/spec/spec.json` by `task clients:generate` (`clients/codegen/emitters/elixir.ts`). Do not edit it; it is left out of `mix format` and Credo, and Dialyzer checks it.

Runtime dependencies: none for URL building. With Req (optional): `req`, `finch`, `mint`, `hpax`, `mime`, `jason`, `nimble_options`, `nimble_pool`, `telemetry`, all MIT or Apache-2.0. `scripts/check_licenses.exs` checks the closure; there are no exceptions. `NimbleOptions` is not used directly: the options are plain keyword lists checked by `Keyword.validate!/2`.
