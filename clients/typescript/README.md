# @xtandard/image

Typed URL builder and small fetch client for [xtandard-image](https://github.com/xantiagoma/xtandard-image), the self-hosted image proxy. It builds imgproxy-dialect URLs (images, `/info`, `/video` with audio and HLS/DASH streams) and query dialect URLs (`/q/`, `/q/video`, imaginary endpoints). The image and `/info` URLs work with imgproxy too.

- The option types are generated from the server's OpenAPI document. Every option name and alias is typed, with its argument tuple, so an unknown option or a wrong argument does not compile.
- Sources can be base64url, `plain/` (escaped for you) or `enc/` (AES-CBC source encryption). An extension suffix is optional.
- URLs can be signed with the imgproxy scheme (key, salt, signature size), and any URL can take an `xi_sig` query signature.
- Crypto runs on WebCrypto (`crypto.subtle`), so the package works in Bun, Node 22+, Deno, browsers and workers. It has no runtime dependencies and ships as ESM only.
- Package versions follow the server's release tags, so `@xtandard/image@X.Y.Z` describes `ghcr.io/xantiagoma/xtandard-image:X.Y.Z`.

```sh
bun add @xtandard/image   # or npm i / pnpm add / deno add npm:@xtandard/image
```

## Building URLs

There are two equivalent forms. A plain object:

```ts
import { imgproxy } from "@xtandard/image";

const img = imgproxy({ baseUrl: "https://img.example.com" });

await img.url({
  source: "s3://photos/cat.jpg",
  options: { resize: ["fill", 300, 400], gravity: "sm", quality: 80 },
  extension: "webp",
});
// https://img.example.com/insecure/rs:fill:300:400/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp
```

And a fluent chain, with one method per option name and alias:

```ts
await img.from("s3://photos/cat.jpg").resize("fill", 300, 400).g("sm").quality(80).extension("webp").build();
```

`build()` and `url()` are async because signing and encryption use WebCrypto.

**Options.** Each key is a canonical option name or an alias (`resize` or `rs`), and each value is the option's positional arguments ([URL-DIALECTS §3.2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md)). A tuple can stop early, so `["fit"]` is fine. `null` leaves an argument empty (`rs::300`), and booleans are written `1`/`0`. When an option takes a single argument, you can pass the value itself: `quality: 80` is `quality: [80]`.

```ts
// @ts-expect-error unknown option
img.url({ source, options: { nope: [1] } });
// @ts-expect-error quality takes a number
img.from(source).quality("80");
```

**Chained pipelines.** Pass an array of option objects, or call `.chain()` in the fluent form. Either one writes `/-/`:

```ts
await img.url({ source, options: [{ rs: ["fit", 500, 500], wm: [0.5, "nowe"] }, { wm: [0.7, "soea"] }] });
await img.from(source).rs("fit", 500, 500).chain().bl(2).build();
```

An options object cannot repeat a key. To repeat an option in one pipeline, which imgproxy merges argument by argument, call the method twice or use `.option(name, ...args)`.

**Sources.**

| `sourceEncoding` | URL | Notes |
|---|---|---|
| `"base64"` (default) | `/…/aHR0cHM6Ly9….webp` | unpadded base64url. `splitSource: 16` splits it into segments. |
| `"plain"` | `/…/plain/https://e.com/a%20b.jpg@webp` | `%`, `@`, `?`, `#`, spaces and non-ASCII are percent-escaped |
| `"encrypted"` | `/…/enc/<token>.webp` | needs `encryption` (below) |

Set the encoding on the builder (`imgproxy({ sourceEncoding: "plain" })`), on a request (`sourceEncoding`), or on a chain (`.encoding("plain")`).

**Other settings.** `separator` (`XI_IMGPROXY_ARGUMENTS_SEPARATOR`, default `:`), `unsignedSignature` (`insecure` by default, or `unsafe` or `_`), and `optionNames`: `"short"` (default: the shortest alias), `"canonical"` or `"given"`.

**`/info`.** `img.infoUrl({ source, options: { size: true, format: true } })` and `img.info(source).size(true).build()` use the info option catalogue. There, `a` means `alpha`, not `adjust`.

## Video, audio and streams

`/video` URLs ([URL-DIALECTS §3.8](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#38-video-transformations-wp-122-wp-123)) use the video option catalogue (`ImgproxyVideoOptions`, generated like the image one). The extension is the container (`VideoContainer`: `mp4`, `webm`, `m4a`, `mp3`, `opus`, `ogg`, `flac`, `wav`, `png`/`webp` for a waveform, `m3u8`/`mpd` for a stream). The signature covers `/video` + the path, so the builder signs it differently from an image URL. Servers refuse unsigned video URLs by default.

```ts
// A 5-second 640×360 WebM cut with a fade and normalised loudness
await img.videoUrl({ source: "s3://media/talk.mov", options: { rs: ["fill", 640, 360], vs: 12, vd: 5, fd: [0.5, 1], ln: true }, extension: "webm" });
await img.video("s3://media/talk.mov").rs("fill", 640, 360).vs(12).vd(5).extension("webm").build();

// Audio: an MP3 extract, mono, 96 kbit/s; a waveform picture
await img.videoUrl({ source: "s3://media/talk.mov", options: { vs: 30, vd: 60, ach: 1, ab: "96k" }, extension: "mp3" });
await img.videoUrl({ source: "s3://media/podcast.mp3", options: { wf: [true, "2266cc", "ffffff"], w: 800, h: 160 }, extension: "png" });

// Overlays: an image watermark (wmu is the base64url of "xi://assets/logo.png") shown
// from 1 s to 4 s with fades, and a timecode
await img.video(src).wm(0.8, "soea", 8, 8).wmu("eGk6Ly9hc3NldHMvbG9nby5wbmc").ow(1, 4).of(0.3, 0.3).tc("hms", "sowe", 8, 8, 20).build();

// The source unmodified (Range-capable), for a "before" player
await img.videoUrl({ source: src, options: { raw: true } });
```

**HLS and DASH** ([§3.10](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#310-hls-and-dash-streams-wp-126-every-dialect)). `streamUrl` writes the `st`/`sgd` options and the `m3u8`/`mpd` container:

```ts
await img.streamUrl({ source: "s3://media/talk.mov", protocol: "hls", renditions: [360, 720], segmentDuration: 4, options: { vs: 60, vd: 300 } });
// https://img.example.com/video/<sig>/vs:60/vd:300/st:hls:360:720/sgd:4/czM6Ly9tZWRpYS90YWxrLm1vdg.m3u8
```

`segmentFormat: "ts"` asks for MPEG-TS segments (HLS only, H.264 + AAC, for older players; `sgf:ts`); the default is CMAF/fMP4.

The manifest lists its playlists and segments under `/_stream/<ticket>/…`. The ticket seals the stream, so a player (hls.js, Safari, dash.js, ffmpeg) fetches them without any key. Only the manifest URL is signed.

## Query dialect

`queryUrls()` builds `/q/` URLs ([URL-DIALECTS §4](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#4-query-dialect-native--imaginary)). The server needs `XI_DIALECTS_QUERY_ENABLED=true`. With `signing`, each URL ends in an `xi_sig` over the path after the mount and the query.

```ts
import { queryUrls } from "@xtandard/image";

const q = queryUrls({ baseUrl: "https://img.example.com", signing });  // mount: "/q" by default

// Native keys (typed from the OpenAPI getQueryImage parameters) and any imgproxy option in o=
await q.url({ source: "s3://photos/cat.jpg", params: { w: 300, fit: "cover", g: "smart" }, options: { bl: 2 }, extension: "webp" });
// …/q/?url=s3://photos/cat.jpg&w=300&fit=cover&g=smart&o=bl:2&format=webp&xi_sig=…
await q.from("s3://photos/cat.jpg").param("w", 300).blur(2).chain().quality(80).build();

// /q/video: every video option is a key, arguments joined by ","
await q.video("s3://media/talk.mov").vs(1).vd(5).rs("fill", 640, 360).extension("webm").build();
// …/q/video?url=s3://media/talk.mov&vs=1&vd=5&rs=fill,640,360&format=webm&xi_sig=…
await q.streamUrl({ source: "s3://media/talk.mov", protocol: "dash" });

// imaginary endpoints (typed endpoint and parameters)
await q.imaginaryUrl({ endpoint: "smartcrop", source: "s3://photos/cat.jpg", params: { width: 300, height: 300 } });
```

`crop` and `flip` have native spellings in `/q/video` (`w,h[,x,y]`, `h|v|hv`), so the builder writes the imgproxy forms under their aliases `c` and `fl`. `pdfPassword` works here too (`pdp=`). The imaginary `sign` scheme is not implemented; use `xi_sig`.

## Sources

Any source string works. `xi://<name>/<key>` selects a named source (`XI_SOURCES`). The helpers only join strings:

```ts
import { sourceUrl, xiSource } from "@xtandard/image";

xiSource("photos", "2026/cat.jpg");                // xi://photos/2026/cat.jpg
xiSource("web", "https://cdn.example.com/a.jpg");  // xi://web/https://cdn.example.com/a.jpg (an http source without base_url)
sourceUrl("s3", "bucket", "cat.jpg");              // s3://bucket/cat.jpg (also gs, abs, ftp, sftp, http, https)
sourceUrl("local", "", "img/cat.jpg");             // local:///img/cat.jpg
```

## Signing

Use the same hex key and salt as the server's `XI_SIGNING_KEYS` / `XI_SIGNING_SALTS` (imgproxy `IMGPROXY_KEY` / `IMGPROXY_SALT`). Sign only on a server. A key in browser code can sign any URL.

```ts
const img = imgproxy({
  baseUrl: "https://img.example.com",
  signing: { key: process.env.XI_SIGNING_KEY!, salt: process.env.XI_SIGNING_SALT!, size: 32 },
});
await img.url({ source: "https://images.unsplash.com/photo-1506744038136-46273834b3fb", options: { rs: ["fill", 1200, 630] } });
// https://img.example.com/<signature>/rs:fill:1200:630/aHR0cHM6…
```

`size` is `XI_SIGNING_SIZE` (1–32 bytes before encoding, default 32). The lower-level helpers are exported too:

```ts
import { sign, signImgproxyPath, signXiQuery, signXiUrl } from "@xtandard/image";

await signImgproxyPath("/rs:fit:300:300/plain/http://img.example.com/pretty/image.jpg", key);
// "/<signature>/rs:fit:300:300/plain/…"

// xi_sig: for dialects without their own scheme (query dialect, imgix/Cloudflare-style
// URLs, unpic-generated URLs). It signs the path after the mount plus the query,
// minus any existing xi_sig, and appends xi_sig as the last parameter.
await signXiUrl("https://img.example.com/q/resize?width=300&url=https%3A%2F%2Fe.com%2Fa.jpg", key, { mount: "/q" });
await signXiQuery("/imgix/photo.jpg", "w=400&h=300", key); // "w=400&h=300&xi_sig=…"
```

## Source encryption

`enc/` sources hide the source URL. The key is the server's first `XI_ENCRYPTION_KEYS` entry (imgproxy `IMGPROXY_SOURCE_URL_ENCRYPTION_KEY`), a hex AES key of 16, 24 or 32 bytes:

```ts
const img = imgproxy({ sourceEncoding: "encrypted", encryption: { key: process.env.XI_ENCRYPTION_KEY! }, signing });
await img.url({ source: "s3://private/cat.jpg", options: { w: 300 } });
```

Each URL gets a fresh random IV, like the server's own encoder. To make URLs stable, for example as cache keys, pass a fixed 16-byte `iv`. A fixed IV reveals when two URLs have the same source. Encrypted URLs should always be signed, because CBC is malleable ([ARCHITECTURE §10](https://github.com/xantiagoma/xtandard-image/blob/main/docs/ARCHITECTURE.md)). Chromium's WebCrypto rejects 192-bit AES keys. Bun, Node, Deno, Firefox and Safari accept them.

## Password-protected PDFs

A PDF with a user (open) password needs its password in the URL, encrypted with the same key as `enc/` sources, and the server accepts it on signed URLs only ([URL-DIALECTS §3.7](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#37-password-protected-pdfs-wp-115)). Pass the plain-text password as `pdfPassword` (or `.pdfPassword()` in the fluent form); the builder encrypts it into a `pdp:<token>` segment and needs both `encryption` and `signing`:

```ts
const img = imgproxy({ baseUrl, signing, encryption: { key: process.env.XI_ENCRYPTION_KEY! } });
await img.url({ source: "s3://docs/contract.pdf", options: { rs: ["fit", 800, 0], f: "webp" }, pdfPassword: "s3cret" });
await img.from("s3://docs/contract.pdf").pdfPassword("s3cret").rs("fit", 800, 0).build();
```

`encryptPdfPassword(password, { key })` returns the token alone, for the query dialect (`pdf_password=<token>` with `xi_sig`) or the `pdp` option. Like the signing key, the encryption key belongs on a server: build these URLs there.

## Client

`@xtandard/image/client` wraps the JSON and health endpoints. Its types come from the OpenAPI document:

```ts
import { createClient, XtandardError } from "@xtandard/image/client";

const xi = createClient({ baseUrl: "http://localhost:8080", imgproxy: { signing: { key, salt } } });

await xi.healthz(); // throws XtandardError unless 200
const { ready, state } = await xi.readyz(); // 503 "starting"/"draining" is a state, not an error

try {
  const info = await xi.info({ source: "s3://photos/cat.jpg", options: { size: true, format: true } });
  info.width; // typed InfoResponse
} catch (e) {
  if (e instanceof XtandardError) {
    e.status; // 403
    e.kind; // "unauthorized" (typed ErrorKind)
    e.requestId; // matches the server logs
  }
}
```

`info()` builds and signs the `/info` URL with the builder settings and asks for `application/json`. Errors carry the server's `{"error":{"kind","message","request_id"}}` envelope. If the body is not that envelope (a proxy error page, say), `kind` is `"unknown"`. `fetch` and `headers` can be overridden, and every call takes an `AbortSignal`. The generated `paths`, `components` and `operations` types are re-exported for other OpenAPI tooling.

Media calls build and sign the URL, send `Range` and `HEAD` when asked, and resolve with the 2xx `Response`, body unread, or throw `XtandardError`:

```ts
const part = await xi.video({ source: "s3://media/talk.mov", options: { vd: 5 }, extension: "webm" }, { range: { start: 0, end: 65535 } });
part.status; // 206, Content-Range: bytes 0-65535/…
const head = await xi.video(request, { method: "HEAD" }); // type, ETag, no body
const img = await xi.image({ source, options: { w: 300 } }, { accept: "image/avif,image/webp" });

const { url, body } = await xi.stream({ source: "s3://media/talk.mov", protocol: "hls" }); // the master playlist
const segment = await xi.media("/_stream/<ticket>/v0/1.m4s", { range: { suffix: 1024 } }); // a ticket URL: no signing
const meta = await xi.playgroundMeta(); // GET <playground>/api/meta, typed PlaygroundMeta
```

`range` takes `{ start, end? }`, `{ suffix }` or a header string (`rangeHeader()` formats it). `playgroundMeta()` uses `playground: { baseUrl, path }` from the client options. The playground is on the admin listener unless `XI_PLAYGROUND_PUBLIC=true`.

## Coverage

What the server accepts (`main`, M5 through WP-129) compared with what the package exposed before and after WP-133. The option catalogues are generated, so a new Go option reaches the TypeScript types and fluent methods with `bun run generate`. The Go test `internal/apidoc` `TestTypeScriptCatalogue` fails when the committed tables and the catalogues differ. The `testdata/urls/imgproxy-builder.json` fixture checks the generated URLs in both directions: Go derives the cases from its golden tables, this package builds and signs the URLs, and Go parses them back.

| Server feature | Before WP-133 | Now |
|---|---|---|
| imgproxy processing options, 90 names with aliases (watermarks `wm`/`wmu`/`wmt`/`wms`/`wmr`/`wmsh`, smart/object/face gravity, `crop_objects`, `blur_detections`, `draw_detections`, `page`/`pages`, animation limits, `format_quality`, `pr`, `raw`, `cb`, `exp`, `fn`, `att`, `hs`, `ct`, `bc`, `gain_map`, …) | typed and generated; 88 covered by the Go↔TS fixture | typed and generated; 89 in the fixture (`gain_map` added). `pdf_password` goes through `pdfPassword` and has its own vectors |
| `/info` options, 35 (`classify`, `blurhash`, `thumbhash`, `perceptual_hash`, `calc_hashsums`, `video_meta`, `dominant_colors`, `detect_objects`, …) | typed; 34 in the fixture | unchanged (`pdf_password` via `pdfPassword`) |
| Password-protected PDFs (`pdp:`) | `pdfPassword`, `encryptPdfPassword` | also on query URLs (`pdp=`) |
| `/video` options, 50 (cut, geometry, fps, codecs, `q`/`crf`/`vb`/`ki`, audio, waveform, `wm*` overlays with `ow`/`of`, `tc`, `st`/`sgd`, `raw`, delivery options) | typed (`videoUrl`, `video`); 49 in the fixture, 6 of 12 containers | 50 in the fixture and all 12 containers; `VideoContainer` type |
| `/video` signing (`"/video"` + path) | yes | yes |
| HLS/DASH manifests | only by hand (`st`, `@m3u8`) | `streamUrl()` on both builders, `client.stream()`; `StreamProtocol` comes from the catalogue |
| Stream child URLs (`/_stream/<ticket>/…`) | — | `client.media()` (no signing; `Range`, `HEAD`) |
| Query dialect `/q/?url=…` (native keys, `o=`, `xi_sig`) | `signXiUrl` on a hand-written URL | `queryUrls().url()`/`.from()`: native keys typed from OpenAPI, options typed in `o=`; 495 fixture cases parsed by the Go query dialect to the same request as their imgproxy URL |
| `/q/video` | — | `queryUrls().videoUrl()`/`.video()`, typed by the video catalogue (`crop`/`flip` written `c`/`fl`) |
| imaginary endpoints `/q/<endpoint>` | — | `queryUrls().imaginaryUrl()`, endpoint and parameters typed from OpenAPI |
| Source references (`xi://<name>/…`, `s3://`, `gs://`, `abs://`, `local:///`, `ftp://`, `sftp://`) | plain strings | strings, plus `xiSource()` / `sourceUrl()` |
| JSON endpoints: `/info`, `/healthz`, `/readyz` | typed client | unchanged |
| Playground `api/meta` | — | `client.playgroundMeta()` (typed `PlaygroundMeta`) |
| Image and video fetches with `Range` / `HEAD` | — | `client.image()`, `client.video()`, `client.media()` |
| Native query keys in OpenAPI | `vts`, `vtk`, `pages` and `pdf_password` were missing | all listed; `TestQueryKeysMatchParser` keeps them in sync with the parser |

Not covered, on purpose:

- **Other dialects' URL shapes** (imgix, Cloudinary, ImageKit, Cloudflare, thumbor, Bunny, wsrv, AWS SIH, the unpic micro-dialects). Their own SDKs and unpic build those URLs. `signXiUrl` adds `xi_sig` to any of them.
- **imaginary's `sign` scheme.** Query URLs are signed with `xi_sig`, which the server accepts on every query URL.
- **Only-presets URLs** (`/<preset>:<preset>/plain/…` with `XI_PRESETS_ONLY`). Use `pr:` options instead.
- **The playground's POST API** (`api/sign`, `api/render`, `api/translate`, `api/probe`, `api/upload`). It is development tooling on the admin listener. Its types are still in the generated `paths`.
- **Native query values** are typed as `string | number | boolean`. The OpenAPI document declares them as strings without enums.

## Development

This package lives in the xtandard-image repository's Bun workspace ([ADR 0009](https://github.com/xantiagoma/xtandard-image/blob/main/docs/adr/0009-typescript-monorepo.md)):

```sh
bun install
bun run generate        # src/generated/ from internal/apidoc/openapi.json (committed)
bun run generate:check  # fails when the committed files are stale
bun run typecheck       # tsc, including the type tests in test-d/
bun test
```

`src/generated/openapi.ts` is `openapi-typescript` output. `src/generated/options.ts` holds the option catalogue: argument tuples with optional trailing elements and rest groups, plus the alias tables. It is emitted by `scripts/generate.ts` from the same `ImgproxyProcessingOptions` / `ImgproxyInfoOptions` schemas, because openapi-typescript renders `prefixItems` as fixed-length tuples and drops the `x-xi-aliases` extension.

The tests share fixtures with the Go server. `testdata/security/vectors.json` holds the signing and encryption vectors. `testdata/urls/imgproxy-builder.json` has one case per successful dialect table case. The Go tests derive those cases, this package builds their URLs, and the Go parser checks that each URL parses to the table case's request (`task ts:fixtures` regenerates the file). Cases marked `query` are also built as query dialect URLs (`query_url`, signed with `xi_sig` when the case is signed), and the Go query parser must turn them into the same request, by canonical key, as the imgproxy URL.

A new option needs no TypeScript code. Add it to the Go catalogue (`internal/dialect/imgproxy/catalog*.go`) and its golden cases, then run:

```sh
go test ./internal/apidoc -run TestGolden -update                        # openapi.json
go test ./internal/dialect/imgproxy -run 'TestVideo|TestBuilderFixture' -update
bun run generate && XI_UPDATE_FIXTURES=1 bun test src/fixture.test.ts    # types, then the fixture URLs
```

## License

MIT
