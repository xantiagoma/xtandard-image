# @xtandard/image-url

Typed URL builder and small fetch client for [xtandard-image](https://github.com/xantiagoma/xtandard-image), the self-hosted image proxy. It builds imgproxy-dialect URLs and works with imgproxy too.

- The option types are generated from the server's OpenAPI document. Every option name and alias is typed, with its argument tuple, so an unknown option or a wrong argument does not compile.
- Sources can be base64url, `plain/` (escaped for you) or `enc/` (AES-CBC source encryption). An extension suffix is optional.
- URLs can be signed with the imgproxy scheme (key, salt, signature size), and any URL can take an `xi_sig` query signature.
- Crypto runs on WebCrypto (`crypto.subtle`), so the package works in Bun, Node 22+, Deno, browsers and workers. It has no runtime dependencies and ships as ESM only.
- Package versions follow the server's release tags, so `@xtandard/image-url@X.Y.Z` describes `ghcr.io/xantiagoma/xtandard-image:X.Y.Z`.

```sh
bun add @xtandard/image-url   # or npm i / pnpm add / deno add npm:@xtandard/image-url
```

## Building URLs

There are two equivalent forms. A plain object:

```ts
import { imgproxy } from "@xtandard/image-url";

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

**`/video`** (video transformations, audio and waveforms; [URL-DIALECTS §3.8](../../docs/URL-DIALECTS.md#38-video-transformations-wp-122-wp-123)). `img.videoUrl({ source, options: { resize: ["fill", 640, 360], start: 1.5, duration: 5 }, extension: "webm" })` and `img.video(source).rs("fill", 640, 360).vs(1.5).vd(5).extension("webm").build()` use the video option catalogue (`ImgproxyVideoOptions`); the extension is the container (`mp4`, `webm`, `m4a`, `mp3`, `opus`, `ogg`, `flac`, `wav`, or `png`/`webp` for a waveform). The signature covers `/video` + the path, so the builder signs it differently from an image URL; servers refuse unsigned video URLs by default.

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
import { sign, signImgproxyPath, signXiQuery, signXiUrl } from "@xtandard/image-url";

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

`@xtandard/image-url/client` wraps the JSON and health endpoints. Its types come from the OpenAPI document:

```ts
import { createClient, XtandardError } from "@xtandard/image-url/client";

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

The tests share fixtures with the Go server. `testdata/security/vectors.json` holds the signing and encryption vectors. `testdata/urls/imgproxy-builder.json` has one case per successful dialect table case. The Go tests derive those cases, this package builds their URLs, and the Go parser checks that each URL parses to the table case's request (`task ts:fixtures` regenerates the file).

## License

MIT
