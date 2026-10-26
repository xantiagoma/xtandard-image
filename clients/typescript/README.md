<p align="center"><img src="https://raw.githubusercontent.com/xantiagoma/xtandard-image/main/docs/assets/logo-128.png" alt="xtandard-image" width="128" height="128"></p>

# xtandard-image for TypeScript

**Typed URL builder, signing and fetch client for the xtandard-image server.**

[![npm](https://img.shields.io/npm/v/@xtandard/image)](https://www.npmjs.com/package/@xtandard/image)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue)](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

`@xtandard/image` is the TypeScript client for [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme), an open-source media server that covers your application's image and video needs, built on libvips and FFmpeg. The package builds and signs the URLs your code sends to the server, and wraps its `/info` and health endpoints; it does not process images itself. To run a server, see [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md), or start one with signing keys:

```sh
export XI_SIGNING_KEY="$(openssl rand -hex 32)" XI_SIGNING_SALT="$(openssl rand -hex 32)"
docker run -d --name xtandard-image -p 8080:8080 \
  -e XI_SOURCES=web -e XI_SOURCES_WEB_TYPE=http -e XI_SOURCES_WEB_ALLOW=images.unsplash.com \
  -e XI_SIGNING_KEYS="$XI_SIGNING_KEY" -e XI_SIGNING_SALTS="$XI_SIGNING_SALT" \
  ghcr.io/xantiagoma/xtandard-image:0.1.1
```

## Install

```sh
npm i @xtandard/image   # or: bun add / pnpm add / deno add npm:@xtandard/image
```

## Quick start

Build a signed URL with the keys the server was started with:

```ts
import { imgproxy } from "@xtandard/image";

const img = imgproxy({
  baseUrl: "http://localhost:8080",
  signing: { key: process.env.XI_SIGNING_KEY!, salt: process.env.XI_SIGNING_SALT! },
});

const url = await img
  .from("https://images.unsplash.com/photo-1506744038136-46273834b3fb")
  .resize("fill", 1200, 630)
  .gravity("sm")
  .extension("jpg")
  .build();
// http://localhost:8080/<signature>/rs:fill:1200:630/g:sm/aHR0cHM6….jpg
```

Ask the server about the source with the fetch client:

```ts
import { createClient } from "@xtandard/image/client";

const xi = createClient({
  baseUrl: "http://localhost:8080",
  imgproxy: { signing: { key: process.env.XI_SIGNING_KEY!, salt: process.env.XI_SIGNING_SALT! } },
});

const info = await xi.info({ source: "https://images.unsplash.com/photo-1506744038136-46273834b3fb" });
console.log(info.format, info.width, info.height); // jpeg 7360 4912
```

Sign URLs on a server only: a key shipped to a browser signs any URL.

## Usage

### Building URLs

There are two equivalent forms, an options object and a fluent chain with one method per option name and alias:

```ts
const img = imgproxy({ baseUrl: "https://img.example.com" });

await img.url({
  source: "s3://photos/cat.jpg",
  options: { resize: ["fill", 300, 400], gravity: "sm", quality: 80 },
  extension: "webp",
});
await img.from("s3://photos/cat.jpg").resize("fill", 300, 400).g("sm").quality(80).extension("webp").build();
// both: https://img.example.com/insecure/rs:fill:300:400/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp
```

Option names, aliases and argument tuples are typed from the server's catalogue, so an unknown option or a wrong argument does not compile. A tuple can stop early (`["fit"]`), `null` leaves an argument empty, and a single argument can be passed bare (`quality: 80`). `build()` and `url()` are async because signing and encryption use WebCrypto. The options themselves are documented in [URL dialects §3.2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#32-processing-options--tr).

Builder settings: `separator` (default `:`), `unsignedSignature` (`insecure`, `unsafe` or `_`), and `optionNames` (`"short"` by default, `"canonical"` or `"given"`).

### Chained pipelines

An array of option objects, or `.chain()` in the fluent form, writes `/-/` between pipelines:

```ts
await img.url({ source, options: [{ rs: ["fit", 500, 500] }, { bl: 2 }] });
await img.from(source).rs("fit", 500, 500).chain().bl(2).build();
```

### Sources

| `sourceEncoding` | URL |
|---|---|
| `"base64"` (default) | `/…/czM6Ly9waG90b3MvY2F0LmpwZw.webp`; `splitSource: 16` splits it into segments |
| `"plain"` | `/…/plain/https://e.com/a%20b.jpg@webp`, escaped for you |
| `"encrypted"` | `/…/enc/<token>.webp`; needs `encryption` (below) |

Set it on the builder, per request (`sourceEncoding`) or per chain (`.encoding("plain")`). `xiSource("photos", "2026/cat.jpg")` writes a named source (`xi://photos/2026/cat.jpg`) and `sourceUrl("s3", "bucket", "cat.jpg")` a storage URL.

### `/info`

```ts
await img.infoUrl({ source, options: { size: true, format: true } });
await img.info(source).size(true).format(true).build();
```

### Video and streams

`/video` URLs use the video option catalogue; the extension is the container (`mp4`, `webm`, `mp3`, `png` for a waveform, …). `streamUrl` builds HLS and DASH manifests, whose segments a player fetches without any key:

```ts
await img.video("s3://media/talk.mov").rs("fill", 640, 360).vs(12).vd(5).extension("webm").build();
await img.streamUrl({ source: "s3://media/talk.mov", protocol: "hls", renditions: [360, 720], segmentDuration: 4 });
```

The server needs the `full` or `gpl` image for video. Options and containers: [URL dialects §3.8](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#38-video-transformations-wp-122-wp-123) and [§3.10](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#310-hls-and-dash-streams-wp-126-every-dialect).

### Query dialect

`queryUrls()` builds `/q/` URLs (native keys plus imgproxy options in `o=`), `/q/video` and imaginary endpoints, signed with `xi_sig`. The server needs `XI_DIALECTS_QUERY_ENABLED=true`.

```ts
import { queryUrls } from "@xtandard/image";

const q = queryUrls({ baseUrl: "https://img.example.com", signing });
await q.url({ source: "s3://photos/cat.jpg", params: { w: 300, fit: "cover" }, options: { bl: 2 }, extension: "webp" });
// https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&fit=cover&o=bl:2&format=webp&xi_sig=…
await q.imaginaryUrl({ endpoint: "smartcrop", source: "s3://photos/cat.jpg", params: { width: 300, height: 300 } });
```

See [URL dialects §4](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#4-query-dialect-native--imaginary).

### Signing and encryption

`signing` takes the server's hex key and salt (`XI_SIGNING_KEYS` / `XI_SIGNING_SALTS`, or imgproxy's `IMGPROXY_KEY` / `IMGPROXY_SALT`) and an optional `size`. `encryption` takes the first `XI_ENCRYPTION_KEYS` entry, for `enc/` sources and password-protected PDFs:

```ts
const img = imgproxy({ baseUrl, signing, encryption: { key: process.env.XI_ENCRYPTION_KEY! } });
await img.url({ source: "s3://private/cat.jpg", options: { w: 300 }, sourceEncoding: "encrypted" });
await img.url({ source: "s3://docs/contract.pdf", options: { rs: ["fit", 800, 0] }, pdfPassword: "s3cret" });
```

Each `enc/` token gets a random IV unless you pass a fixed 16-byte `iv`. The helpers `sign`, `signImgproxyPath`, `signXiUrl`, `signXiQuery`, `encryptSource` and `encryptPdfPassword` are exported too; `signXiUrl` adds an `xi_sig` to a URL of any other dialect. Schemes: [URL dialects §2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#2-signing-schemes-at-a-glance).

### HTTP client

`@xtandard/image/client` has `healthz`, `readyz`, `info`, `image`, `video`, `stream`, `media` (with `Range` and `HEAD`) and `playgroundMeta`. Its response types come from the server's OpenAPI document, and every call takes an `AbortSignal`:

```ts
const res = await xi.image({ source, options: { w: 300 } }, { accept: "image/avif,image/webp" }); // a Response
const part = await xi.video({ source, options: { vd: 5 }, extension: "webm" }, { range: { start: 0, end: 65535 } });
```

### Errors

The builders throw `TypeError` or `RangeError` for an invalid option, source or key. The client throws `XtandardError` for a non-2xx answer, with the server's `status`, `kind` and `requestId`:

```ts
import { XtandardError } from "@xtandard/image/client";

try {
  await xi.info({ source: "https://example.com/a.jpg" });
} catch (e) {
  if (e instanceof XtandardError) console.log(e.status, e.kind, e.requestId); // 403 "forbidden" …
}
```

## Compatibility

- Node.js 22 or later, Bun, Deno, browsers and workers: crypto runs on WebCrypto. ESM only, no runtime dependencies.
- The package version is the server version: `@xtandard/image@0.2.1` matches `ghcr.io/xantiagoma/xtandard-image:0.2.1`. Versions follow [ZeroVer](https://0ver.org): a minor bump may break the API, a patch does not, and prereleases are on the `next` dist-tag.

## Links

- [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme) and [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md)
- [URL dialects](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md): every option and signing scheme
- [Playground](https://github.com/xantiagoma/xtandard-image/blob/main/docs/PLAYGROUND.md): build URLs in a browser and copy the TypeScript code
- [Changelog](https://github.com/xantiagoma/xtandard-image/blob/main/CHANGELOG.md)
- [Contributing](https://github.com/xantiagoma/xtandard-image/blob/main/CONTRIBUTING.md) and [the official clients](https://github.com/xantiagoma/xtandard-image/blob/main/clients/README.md)
- [Security policy](https://github.com/xantiagoma/xtandard-image/blob/main/SECURITY.md)
- Licence: [MIT](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

A personal project, provided as is under the MIT licence: see the [disclaimer](https://github.com/xantiagoma/xtandard-image#disclaimer).

Design: every official client is generated from one spec and passes one conformance fixture ([ADR 0013](https://github.com/xantiagoma/xtandard-image/blob/main/docs/adr/0013-official-clients.md)).
