# xtandard-image for Python

The official Python client of [xtandard-image](https://github.com/xantiagoma/xtandard-image) ([ADR 0013](../../docs/adr/0013-official-clients.md)). It has:

- typed URL builders for the imgproxy dialect (processing, `/info`, `/video`, HLS and DASH) and the query dialect (`/q/`, `/q/video`, imaginary endpoints);
- imgproxy and `xi_sig` signing;
- `enc/` source and PDF password encryption;
- synchronous and asynchronous HTTP clients on [httpx](https://www.python-httpx.org/).

It needs Python 3.10 or later. It is fully typed (`py.typed`), and clean under pyright and mypy in strict mode.

It is not on PyPI yet. The package `xtandard-image` will follow the server's release tags.

**Build and sign URLs on a server.** The signing key signs every URL, and the encryption key decrypts every `enc/` source and PDF password.

## Install (from the repository, until the first release)

```sh
uv add "xtandard-image @ git+https://github.com/xantiagoma/xtandard-image#subdirectory=clients/python"
```

## URL builder

```python
import os
from xtandard_image import EncryptionKey, SigningKey, imgproxy, xi_source

img = imgproxy(
    base_url="https://img.example.com",
    signing=SigningKey(key=os.environ["XI_SIGNING_KEY"], salt=os.environ["XI_SIGNING_SALT"]),
    encryption=EncryptionKey(key=os.environ["XI_ENCRYPTION_KEY"]),  # only for enc/ sources and PDF passwords
)

# Object form: keys are option names or aliases, values are argument tuples or a bare first argument.
img.url("s3://photos/cat.jpg", {"rs": ("fill", 1200, 630), "g": "sm", "q": 80}, extension="webp")
# https://img.example.com/<signature>/rs:fill:1200:630/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp

# Fluent form: one method per option name and alias.
img.image(xi_source("photos", "2026/cat.jpg")).resize("fit", 300, 300).blur(2).extension("png").build()

# Chained pipelines (/-/): a list of option mappings, or pipeline() in the fluent form.
img.url("s3://photos/cat.jpg", [{"rs": ("fit", 500, 500)}, {"bl": 2}])

# Plain or encrypted sources, a password-protected PDF.
img.url("https://example.com/a b.jpg", {"w": 300}, source_encoding="plain")
img.url("s3://private/cat.jpg", {"w": 300}, source_encoding="encrypted")
img.url("s3://docs/report.pdf", {"rs": ("fit", 800, 0)}, pdf_password="s3cret", extension="png")

# /info, /video, HLS and DASH.
img.info_url("s3://photos/cat.jpg", {"size": True, "format": True, "dimensions": True})
img.video_url("s3://media/talk.mov", {"rs": ("fill", 640, 360), "vs": 12, "vd": 8}, extension="webm")
img.video("s3://media/talk.mov").start(12).duration(8).mute(True).extension("mp4").build()
img.stream_url("s3://media/talk.mov", protocol="hls", renditions=[360, 720], segment_duration=4)
```

The option types (`ProcessingOptions`, `InfoOptions`, `VideoOptions`) are `TypedDict`s generated from the server's catalogues. An unknown option, a wrong enum value or a wrong argument type fails type-checking. At run time, it raises `URLBuildError` with a `code` such as `unknown_option`, `invalid_argument` or `invalid_source`.

## Query dialect

```python
from xtandard_image import SigningKey, query_urls

q = query_urls(base_url="https://img.example.com", signing=SigningKey(key=KEY, salt=SALT))
q.url("s3://photos/cat.jpg", params={"w": 300, "f": "webp"}, options={"bl": 2})
# https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&f=webp&o=bl:2&xi_sig=…
q.video_url("s3://media/talk.mov", {"rs": ("fill", 640, 360), "vs": 1}, extension="webm")
q.stream_url("s3://media/talk.mov", protocol="hls", renditions=[360])
q.imaginary_url("resize", "s3://photos/cat.jpg", {"width": 300})
```

## HTTP client

```python
from xtandard_image import AsyncClient, ByteRange, Client, ForbiddenError, SigningKey

with Client("http://localhost:8080", signing=SigningKey(key=KEY, salt=SALT)) as xi:
    xi.healthz()
    info = xi.info("s3://photos/cat.jpg", {"size": True, "format": True})  # typed InfoResponse
    res = xi.image("s3://photos/cat.jpg", {"w": 300}, accept="image/avif,image/webp")
    part = xi.video("s3://media/talk.mov", {"vd": 5}, extension="webm", range=ByteRange(0, 1023))
    head = xi.video("s3://media/talk.mov", {"vd": 5}, extension="webm", method="HEAD")
    manifest = xi.stream("s3://media/talk.mov", protocol="hls", renditions=[360, 720])
    segment = xi.media(manifest.body.splitlines()[-1])  # ticket child URLs need no signing

async with AsyncClient("http://localhost:8080") as xi:
    info = await xi.info("s3://photos/cat.jpg", {"size": True})
```

Non-2xx responses raise an `HTTPError` subclass chosen by status:

- `BadRequestError` (400)
- `ForbiddenError` (403)
- `NotFoundError` (404)
- `NotAcceptableError` (406)
- `UnprocessableError` (422)
- `TooManyRequestsError` (429)
- `ServiceUnavailableError` (503)
- `GatewayError` (502, 504)
- `ServerError` (other 5xx)

Each carries `status`, `kind`, `message` and `request_id` from the server's JSON error envelope. Connection failures raise `TransportError`. Pass `stream=True` to iterate a large body.

## Coverage

| Server feature | Python |
|---|---|
| imgproxy processing options (90 with aliases), typed per option | `ProcessingOptions`, fluent methods, every name in the shared builder fixture |
| `/info` options (35) | `InfoOptions`, `info_url()`, `info()`, `Client.info()` (typed `InfoResponse`) |
| `/video` options (50) and all 12 containers; `/video` signing | `VideoOptions`, `video_url()`, `video()`, `Client.video()` with Range and HEAD |
| HLS and DASH (`st`, `sgd`, `sgf:ts`, `@m3u8`, `@mpd`) | `stream_url()` on both builders, `Client.stream()`; ticket child URLs through `Client.media()` |
| Password-protected PDFs (`pdp`) | `pdf_password=` / `with_pdf_password()`, `encrypt_pdf_password()` |
| Sources: base64, `plain/`, `enc/`, split; `xi://`, `s3://`, `gs://`, `abs://`, `local:///`, `ftp://`, `sftp://` | `source_encoding`, `split_source`, `xi_source()`, `source_url()` |
| Query dialect `/q/`, `/q/video`, imaginary; `xi_sig` | `QueryURLBuilder` (native keys typed as `QueryImageParams`), `sign_xi_url()` |
| `/healthz`, `/readyz`, playground `api/meta` | `healthz()`, `readyz()`, `playground_meta()` |

Not covered, on purpose (as in the TypeScript client):

- other dialects' URL shapes, which their own SDKs build;
- imaginary's `sign` scheme;
- presets-only URLs;
- the playground's POST API.

## Development

```sh
cd clients/python
mise install                 # Python and uv pinned in mise.toml
task ci                      # = task -d clients/python ci from the root: lint, types, tests, licences, build
uv run pytest                # conformance + builder fixture + vectors + unit + client tests
XI_SMOKE_URL=http://127.0.0.1:8080 uv run pytest tests/test_smoke.py   # against the playground demo
```

`src/xtandard_image/_generated/` is generated from `clients/spec/spec.json` by `task clients:generate` (`clients/codegen/emitters/python.ts`). Do not edit it.

Runtime dependencies:

- `httpx` (BSD-3-Clause), which brings `certifi` (MPL-2.0, an unmodified CA bundle; a documented exception);
- `cryptography` (Apache-2.0 OR BSD-3-Clause);
- `typing-extensions` (PSF-2.0), on Python 3.10 only.

`scripts/check_licenses.py` checks the full closure.
