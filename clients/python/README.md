<p align="center"><img src="https://raw.githubusercontent.com/xantiagoma/xtandard-image/main/docs/assets/logo-128.png" alt="xtandard-image" width="128" height="128"></p>

# xtandard-image for Python

**Typed URL builder, signing and HTTP client (sync and async) for the xtandard-image server.**

[![PyPI: pending](https://img.shields.io/badge/PyPI-pending-lightgrey)](https://github.com/xantiagoma/xtandard-image/blob/main/docs/RELEASE.md) <!-- registry-status: pending -->
[![license: MIT](https://img.shields.io/badge/license-MIT-blue)](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

`xtandard-image` is the Python client for [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme), an open-source media server that covers your application's image and video needs, built on libvips and FFmpeg. The package builds and signs the URLs your code sends to the server, and wraps its `/info`, media and health endpoints on [httpx](https://www.python-httpx.org/); it does not process images itself. To run a server, see [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md), or start one with signing keys:

```sh
export XI_SIGNING_KEY="$(openssl rand -hex 32)" XI_SIGNING_SALT="$(openssl rand -hex 32)"
docker run -d --name xtandard-image -p 8080:8080 \
  -e XI_SOURCES=web -e XI_SOURCES_WEB_TYPE=http -e XI_SOURCES_WEB_ALLOW=images.unsplash.com \
  -e XI_SIGNING_KEYS="$XI_SIGNING_KEY" -e XI_SIGNING_SALTS="$XI_SIGNING_SALT" \
  ghcr.io/xantiagoma/xtandard-image:0.1.0
```

## Install

```sh
pip install xtandard-image   # or: uv add xtandard-image
```

Not published yet: publishing to PyPI is being set up. Until then, install from the repository: <!-- registry-status: pending -->

```sh
uv add "xtandard-image @ git+https://github.com/xantiagoma/xtandard-image#subdirectory=clients/python"
# or: pip install "xtandard-image @ git+https://github.com/xantiagoma/xtandard-image#subdirectory=clients/python"
```

## Quick start

Build a signed URL with the keys the server was started with:

```python
import os

from xtandard_image import SigningKey, imgproxy

signing = SigningKey(key=os.environ["XI_SIGNING_KEY"], salt=os.environ["XI_SIGNING_SALT"])
img = imgproxy(base_url="http://localhost:8080", signing=signing)

url = (
    img.image("https://images.unsplash.com/photo-1506744038136-46273834b3fb")
    .resize("fill", 1200, 630)
    .gravity("sm")
    .extension("jpg")
    .build()
)
# http://localhost:8080/<signature>/rs:fill:1200:630/g:sm/aHR0cHM6….jpg
```

Ask the server about the source with the HTTP client:

```python
from xtandard_image import Client

with Client("http://localhost:8080", signing=signing) as xi:
    info = xi.info("https://images.unsplash.com/photo-1506744038136-46273834b3fb")
    print(info["format"], info["width"], info["height"])  # jpeg 7360 4912
```

Sign URLs on a server only: the signing key signs any URL, and the encryption key decrypts every `enc/` source.

## Usage

### Building URLs

There are two equivalent forms, an options mapping and a fluent chain with one method per option name and alias:

```python
img = imgproxy(base_url="https://img.example.com")

img.url("s3://photos/cat.jpg", {"resize": ("fill", 300, 400), "gravity": "sm", "quality": 80}, extension="webp")
img.image("s3://photos/cat.jpg").resize("fill", 300, 400).g("sm").quality(80).extension("webp").build()
# both: https://img.example.com/insecure/rs:fill:300:400/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp
```

The option types (`ProcessingOptions`, `InfoOptions`, `VideoOptions`) are `TypedDict`s generated from the server's catalogue, so pyright and mypy catch an unknown option or a wrong argument. A single argument can be passed bare (`"q": 80`), and `None` leaves an argument empty. The options themselves are documented in [URL dialects §3.2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#32-processing-options--tr).

Builder settings: `separator` (default `:`), `unsigned_signature` (`insecure`, `unsafe` or `_`), and `option_names` (`"short"` by default, `"canonical"` or `"given"`).

### Chained pipelines

A list of option mappings, or `.pipeline()` in the fluent form, writes `/-/` between pipelines:

```python
img.url("s3://photos/cat.jpg", [{"rs": ("fit", 500, 500)}, {"bl": 2}])
img.image("s3://photos/cat.jpg").rs("fit", 500, 500).pipeline().bl(2).build()
```

### Sources

| `source_encoding` | URL |
|---|---|
| `"base64"` (default) | `/…/czM6Ly9waG90b3MvY2F0LmpwZw.webp`; `split_source=16` splits it into segments |
| `"plain"` | `/…/plain/https://e.com/a%20b.jpg@webp`, escaped for you |
| `"encrypted"` | `/…/enc/<token>.webp`; needs `encryption` (below) |

Set it on the builder, per call (`source_encoding=`) or per chain (`.encoding("plain")`). `xi_source("photos", "2026/cat.jpg")` writes a named source (`xi://photos/2026/cat.jpg`) and `source_url("s3", "bucket", "cat.jpg")` a storage URL.

### `/info`

```python
img.info_url("s3://photos/cat.jpg", {"size": True, "format": True})
img.info("s3://photos/cat.jpg").size(True).format(True).build()
```

### Video and streams

`/video` URLs use the video option catalogue; the extension is the container (`mp4`, `webm`, `mp3`, `png` for a waveform, …). `stream_url` builds HLS and DASH manifests, whose segments a player fetches without any key:

```python
img.video("s3://media/talk.mov").rs("fill", 640, 360).vs(12).vd(5).extension("webm").build()
img.stream_url("s3://media/talk.mov", protocol="hls", renditions=[360, 720], segment_duration=4)
```

The server needs the `full` or `gpl` image for video. Options and containers: [URL dialects §3.8](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#38-video-transformations-wp-122-wp-123) and [§3.10](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#310-hls-and-dash-streams-wp-126-every-dialect).

### Query dialect

`query_urls()` builds `/q/` URLs (native keys plus imgproxy options in `o=`), `/q/video` and imaginary endpoints, signed with `xi_sig`. The server needs `XI_DIALECTS_QUERY_ENABLED=true`.

```python
from xtandard_image import query_urls

q = query_urls(base_url="https://img.example.com", signing=signing)
q.url("s3://photos/cat.jpg", params={"w": 300, "fit": "cover"}, options={"bl": 2}, extension="webp")
# https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&fit=cover&o=bl:2&format=webp&xi_sig=…
q.imaginary_url("smartcrop", "s3://photos/cat.jpg", {"width": 300, "height": 300})
```

`query_method_request(url)` turns such a URL into an HTTP QUERY request, and `Client(query_method=True)` sends them that way. See [URL dialects §4](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#4-query-dialect-native--imaginary).

### Signing and encryption

`SigningKey` takes the server's hex key and salt (`XI_SIGNING_KEYS` / `XI_SIGNING_SALTS`, or imgproxy's `IMGPROXY_KEY` / `IMGPROXY_SALT`) and an optional `size`. `EncryptionKey` takes the first `XI_ENCRYPTION_KEYS` entry, for `enc/` sources and password-protected PDFs:

```python
from xtandard_image import EncryptionKey

img = imgproxy(
    base_url="http://localhost:8080",
    signing=signing,
    encryption=EncryptionKey(key=os.environ["XI_ENCRYPTION_KEY"]),
)
img.url("s3://private/cat.jpg", {"w": 300}, source_encoding="encrypted")
img.url("s3://docs/contract.pdf", {"rs": ("fit", 800, 0)}, pdf_password="s3cret")
```

Each `enc/` token gets a random IV unless the key has a fixed 16-byte `iv`. The helpers `sign`, `sign_imgproxy_path`, `sign_xi_url`, `sign_xi_query`, `encrypt_source` and `encrypt_pdf_password` are exported too; `sign_xi_url` adds an `xi_sig` to a URL of any other dialect. Schemes: [URL dialects §2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#2-signing-schemes-at-a-glance).

### HTTP client

`Client` and `AsyncClient` have `healthz`, `readyz`, `info`, `info_at`, `image`, `video`, `stream`, `media` (with `range=` and `method="HEAD"`) and `playground_meta`. They take the builder settings, and an `httpx` client of your own if you want one:

```python
from xtandard_image import AsyncClient, ByteRange

async with AsyncClient("http://localhost:8080", signing=signing) as xi:
    res = await xi.image("s3://photos/cat.jpg", {"w": 300}, accept="image/avif,image/webp")
    part = await xi.video("s3://media/talk.mov", {"vd": 5}, extension="webm", range=ByteRange(0, 65535))
```

### Errors

The builders raise `URLBuildError` with a `code` such as `unknown_option`, `invalid_argument` or `invalid_source`. A non-2xx answer raises an `HTTPError` subclass chosen by status (`BadRequestError`, `ForbiddenError`, `NotFoundError`, `TooManyRequestsError`, `ServiceUnavailableError`, …) with the server's `status`, `kind`, `message` and `request_id`; a connection failure raises `TransportError`. All of them derive from `XtandardError`.

```python
from xtandard_image import HTTPError

try:
    xi.info("https://example.com/a.jpg")
except HTTPError as e:
    print(e.status, e.kind, e.request_id)  # 403 forbidden …
```

## Compatibility

- Python 3.10 to 3.14. Fully typed (`py.typed`), clean under pyright and mypy in strict mode.
- Runtime dependencies: `httpx`, `cryptography`, and `typing-extensions` on Python 3.10. Two are exceptions to the project's MIT/BSD/Apache/ISC licence policy: `certifi` (MPL-2.0, an unmodified CA bundle that `httpx` brings) and `typing-extensions` (PSF-2.0).
- The package version is the server version: `xtandard-image==0.2.1` matches `ghcr.io/xantiagoma/xtandard-image:0.2.1`. Versions follow [ZeroVer](https://0ver.org): a minor bump may break the API, a patch does not.

## Links

- [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme) and [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md)
- [URL dialects](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md): every option and signing scheme
- [Playground](https://github.com/xantiagoma/xtandard-image/blob/main/docs/PLAYGROUND.md): build URLs in a browser and copy the Python code
- [Changelog](https://github.com/xantiagoma/xtandard-image/blob/main/CHANGELOG.md)
- [Contributing](https://github.com/xantiagoma/xtandard-image/blob/main/CONTRIBUTING.md) and [the official clients](https://github.com/xantiagoma/xtandard-image/blob/main/clients/README.md)
- [Security policy](https://github.com/xantiagoma/xtandard-image/blob/main/SECURITY.md)
- Licence: [MIT](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

A personal project, provided as is under the MIT licence: see the [disclaimer](https://github.com/xantiagoma/xtandard-image#disclaimer).

Design: every official client is generated from one spec and passes one conformance fixture ([ADR 0013](https://github.com/xantiagoma/xtandard-image/blob/main/docs/adr/0013-official-clients.md)).
