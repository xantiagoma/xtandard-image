# Official clients

URL builders and HTTP clients for xtandard-image in the main backend languages ([ADR 0013](../docs/adr/0013-official-clients.md)). They all share one language-neutral spec, one conformance fixture and one code generator. Build and sign URLs **on a server**: the signing key signs everything, and the encryption key decrypts every `enc/` source and PDF password. There are no mobile SDKs.

| Language | Directory | Package | Status |
|---|---|---|---|
| TypeScript | [`packages/image-url`](../packages/image-url) | npm `@xtandard/image-url` | reference implementation (ADR 0009) |
| Python | [`clients/python`](python) | PyPI `xtandard-image` | WP-134 |
| Go | `clients/go` | `github.com/xantiagoma/xtandard-image/clients/go` | planned (WP-135) |
| PHP | `clients/php` | Packagist `xtandard/image` | planned (WP-136) |
| Ruby | `clients/ruby` | RubyGems `xtandard-image` | planned (WP-137) |
| Rust | `clients/rust` | crates.io `xtandard-image` | planned (WP-138) |
| Java | `clients/java` | Maven Central `io.github.xantiagoma:xtandard-image` | planned (WP-139) |
| .NET | `clients/dotnet` | NuGet `Xtandard.Image` | planned (WP-140) |
| Elixir | `clients/elixir` | Hex `xtandard_image` | planned (WP-141) |

Nothing is published yet. Every client is versioned in lockstep with the server's release tags (`0.x`).

## What is shared

| Path | What | Written by | Checked by |
|---|---|---|---|
| `clients/spec/spec.json` | Everything a client needs from the server: the processing, `/info` and `/video` option catalogues (names, aliases, short names, typed positional arguments with enums and ranges, docs), query keys, imaginary endpoints, containers, stream protocols and segment formats, source schemes, signing, encryption and URL rules, error kinds, response JSON Schemas | `internal/apidoc` `BuildClientSpec` (`task clients:spec`) | Go `TestClientSpec`, `TestClientSpecCoversCatalogues` |
| `clients/spec/conformance.json` | About 150 cases: inputs to expected URL or value, or to an error code | inputs by hand; values by the TypeScript client (`task clients:fixtures`) | Go `TestConformanceFixture` (server parsers), every client's tests |
| `testdata/urls/imgproxy-builder.json` | 400+ cases derived from the Go golden tables: `url` (names as given), `url_short` (short names), `query_url` | Go `TestBuilderFixture -update` + TypeScript (`task ts:fixtures`) | Go and every client |
| `testdata/security/vectors.json` | imgproxy, `xi_sig`, `enc/` and PDF password vectors | by hand, from imgproxy's docs and examples | Go and every client |
| `clients/codegen/` | The generator: `generate.ts` runs every `emitters/<lang>.ts` on `spec.json`; `lib.ts` has the shared shape rules | — | `task clients:check` |

## Commands

```sh
task clients:spec          # rewrite spec.json from the Go catalogues (after a server option change)
task clients:generate      # regenerate every client's generated files (ONLY=python for one)
task clients:fixtures      # fill conformance.json expect.value from TypeScript, then check every URL against the server
task clients:check         # no drift: spec.json, generated files, codegen typecheck, LICENSE copies
task clients:test          # `task ci` in every clients/*/ that has a Taskfile.yml
task clients:python:test   # one client
```

After changing a Go option: `task clients:spec clients:generate`, then `bun run generate` in `packages/image-url`, then commit everything.

## Adding a language

Follow these steps in order. Python (`clients/python`) is the worked example. Copy its structure, not its idioms.

1. **Directory.** Create `clients/<lang>/` as a normal project for its ecosystem. Add:
   - a copy of the root `LICENSE`;
   - a `README.md` with install instructions, examples and a coverage table like Python's;
   - a `mise.toml` pinning the toolchain (ADR 0013 §5; use the official language Docker image only if mise cannot install it, and never `xtandard-base:*`);
   - a `Taskfile.yml` whose `ci` task installs, lints, type-checks, tests, checks licences and builds. `task clients:test` finds it automatically.
2. **Emitter.** Add `clients/codegen/emitters/<lang>.ts`, which exports `emitter: Emitter` (`spec.ts`):

   ```ts
   export const emitter: Emitter = { language: "<lang>", emit: (spec) => ({ "clients/<lang>/…/generated/options.x": "…" }) };
   ```

   It must write only under `clients/<lang>/`. Use `lib.ts`:
   - `shapes(option)` gives the argument-list shapes (fixed elements plus an optional repeated tail);
   - `bareArgs(option)` gives the argument types that may stand alone (`q: 80`);
   - `allArgs`, `nameTable`, `pascal`, `snake`.

   Generate at least the per-catalogue name tables (name or alias to canonical, canonical to short). Where the language can express them, also generate:
   - the typed option values (one type per option);
   - the fluent per-option methods;
   - the constants (formats, containers, stream manifests, segment formats, signature sizes, key sizes, separators, safe sets);
   - the response types (`InfoResponse`, `ErrorResponse`).

   Mark generated files `DO NOT EDIT` and keep them out of formatters and linters that would fight the generator. `task clients:generate` writes them and `task clients:check` fails on drift. Never import the TypeScript package or read `openapi.json` directly.
3. **Runtime.** Implement the API surface below by hand on the generated tables. Use the language's standard HMAC-SHA256, AES-CBC (or its most-used crypto library) and HTTP client (one that is idiomatic, and async where the ecosystem expects it).
4. **Tests.** Run all three shared fixtures from the repository by relative path, plus unit tests and HTTP client tests against a stand-in server. Add a smoke test against a real server, skipped unless `XI_SMOKE_URL` is set; the playground demo at `http://127.0.0.1:8080` has unsigned URLs allowed, `xi://local/jpeg/baseline.jpg` and `xi://local/video/bbb-h264-aac.mp4`.
5. **Licences.** Add a script that checks the runtime dependency closure against MIT, BSD, Apache-2.0 and ISC (ADR 0006). Document any exception with its reason, in the script and in the README.
6. **CI.** Add `.github/workflows/client-<lang>.yml`, copied from `client-python.yml`, with path filters `clients/<lang>/**`, `clients/spec/**`, `testdata/urls/**`, `testdata/security/**` and the workflow itself. It is gated by `vars.XI_CLIENTS_WORKFLOW == 'enabled'` and has a matrix over the supported runtimes. Add `'clients/<lang>/**'` to `ci.yml`'s `paths-ignore`.
7. **Docs.** Fill in your row in the table above and in `docs/ROADMAP.md` (M6), and add a line in the README's clients section. Note anything you could not do in the WP's "Deviations".

Do not edit `spec.json` by hand, change another language's emitter, or change `conformance.json` values by hand. If a case seems wrong, fix the reference (TypeScript) or the server and regenerate with `task clients:fixtures`. Add new **cases** freely: inputs by hand, then `task clients:fixtures`.

## Required API surface

Names follow each language's conventions. The table uses the TypeScript and Python names; snake_case or camelCase and the error style follow the language.

| Capability | TypeScript | Python | Notes |
|---|---|---|---|
| imgproxy builder with config | `imgproxy({ baseUrl, signing, unsignedSignature, encryption, sourceEncoding, splitSource, separator, optionNames })` | `ImgproxyURLBuilder(base_url=…, signing=…, …)` (alias `imgproxy`) | defaults: `""`, unsigned, `"insecure"`, none, `base64`, no split, `":"`, `short` |
| processing URL | `url({ source, options, extension, sourceEncoding, pdfPassword })` | `url(source, options, *, extension, source_encoding, pdf_password)` | options: one mapping or a list of mappings (a list chains `/-/`) |
| `/info` URL | `infoUrl` | `info_url` | one pipeline; no extension |
| `/video` URL | `videoUrl` | `video_url` | signs `"/video" + path`; no PDF password |
| HLS/DASH URL | `streamUrl({ protocol, renditions, segmentDuration, segmentFormat, options })` | `stream_url(source, *, protocol, renditions, segment_duration, segment_format, options)` | `stream`, `segment_duration`, `segment_format` and `format` are refused as options |
| fluent builders | `from(src)`, `info(src)`, `video(src)`, one method per option name and alias, `option`, `options`, `chain`, `extension`, `encoding`, `pdfPassword`, `build` | `image(src)`, `info(src)`, `video(src)`, methods per name and alias, `option`, `options`, `pipeline`, `extension`, `encoding`, `with_pdf_password`, `build` | control-method names must not collide with an option name; the generator checks |
| query builder | `queryUrls({ baseUrl, mount, signing, encryption, separator, optionNames })`: `url`, `videoUrl`, `streamUrl`, `imaginaryUrl`, `from`, `video`, `param` | `QueryURLBuilder(…)` (alias `query_urls`): `url`, `video_url`, `stream_url`, `imaginary_url`, `image`, `video`, `param` | mount default `/q` |
| signing | `sign`, `signImgproxyPath`, `xiMessage`, `signXiQuery`, `signXiUrl`, `SigningKey` | `sign`, `sign_imgproxy_path`, `xi_message`, `sign_xi_query`, `sign_xi_url`, `SigningKey` | keys are hex strings or raw bytes |
| encryption | `encryptSource`, `encryptPdfPassword`, `EncryptionKey { key, iv? }` | `encrypt_source`, `encrypt_pdf_password`, `EncryptionKey` | random IV unless one is fixed |
| sources | `xiSource`, `sourceUrl`, `escapePlainSource`, `escapeQueryValue` | `xi_source`, `source_url`, `escape_plain_source`, `escape_query_value` | |
| HTTP client | `createClient(…)`: `healthz`, `readyz`, `info`, `infoUrl`, `image`, `video`, `stream`, `media`, `playgroundMeta`, `rangeHeader` | `Client` and `AsyncClient`: `healthz`, `readyz`, `info`, `info_at`, `image`, `video`, `stream`, `media`, `playground_meta`; `range_header` | Range and HEAD on media; non-2xx raises a typed error with `status`, `kind`, `message`, `request_id` |
| errors | `TypeError`/`RangeError`/`Error`; `XtandardError` (HTTP) | `URLBuildError(code)`; `HTTPError` subclasses by status; `TransportError` | builder errors carry the conformance error code |

Naming conventions:

- **Go:** `imgproxy.New(cfg)`, `URL`, `InfoURL`, `VideoURL`, `StreamURL`, typed option structs or `Options` maps, `errors.Is` sentinels per error code.
- **PHP:** PSR-12, namespace `Xtandard\Image`.
- **Ruby:** snake_case, module `XtandardImage`.
- **Rust:** crate `xtandard_image`, builder structs with `Result<_, Error>` where the `Error` enum has one variant per code.
- **Java:** package `io.github.xantiagoma.xtandard.image`.
- **.NET:** namespace `Xtandard.Image`, async with `Task`.
- **Elixir:** `XtandardImage`, `{:ok, url} | {:error, %XtandardImage.Error{code: :invalid_source}}`.

## URL rules that bite

`spec.json` (`url.rules`, `signing.rules`, `encryption.rules`) is normative. These are the details clients most often get wrong:

- **Numbers** are written as ECMAScript `Number::toString` writes them: `300` not `300.0`, `0.1`, `1.5`, `1e-7`, `1e+21`. Whole numbers below 1e21 have no fraction. Booleans are `1`/`0` in options and `true`/`false` in native query keys.
- **Short names** are written by default. Use `option.short` from the spec; do not compute it yourself. `canonical` writes `name`, and `given` writes whatever the caller used.
- **Option arguments** are never escaped. An argument containing `/`, the separator, `?`, `#`, whitespace or a control character is an error. `null` or `None` is an empty argument (`rs:fill:300::1`). In an options mapping, a key whose value is `null` or `None` is skipped.
- **Plain sources** keep ASCII letters, digits and `url.plain_safe`. Every other character is written as `%XX` per UTF-8 byte, in upper case. Query keys and values keep letters, digits and `url.query_safe`.
- **Order** of a query URL: `url`, native keys, `o`, `format`, `pdp`, `xi_sig`. `xi_sig` signs the path after the mount, plus `?` and the query without `xi_sig`.
- **PDF password**: `pdp:<token>` is the first option segment (`pdf_password` with canonical names) and is written verbatim, even when the token contains the separator. It needs both keys. Check order: encryption key first (`missing_encryption`), then signing (`missing_signing`).
- **Checks run in this order**, so a case with several faults raises the expected code:
  1. separator;
  2. chained pipelines on `/info` or `/video`;
  3. a PDF password on `/video`;
  4. the password keys;
  5. each option (unknown name, then the arguments);
  6. the source;
  7. the extension;
  8. the encoding key.

  Key validation (`invalid_key`) may happen when the key object is created.

## Conformance cases

`conformance.json` has `keys.signing` (`key`, `salt`, hex) and `keys.encryption` (`key`, and `iv` for deterministic tokens), and `cases`. Each case has:

- `name`;
- `op`;
- `config` (optional):
  - `base_url`;
  - `signing`: `true` for the fixture key, or an object `{key, salt}`;
  - `signature_size`;
  - `unsigned_signature`;
  - `encryption`: `true` for the fixture key and IV, or an object `{key, iv?}`;
  - `source_encoding`, `split_source`, `separator`, `option_names`, `mount`;
- `input`;
- `expect`: `{"value": "…"}` or `{"error": "<code>"}`.

| `op` | `input` | Output |
|---|---|---|
| `imgproxy.url` | `source`, `options` (list of pipelines, each a list of `[name, [args…]]`), `extension`, `source_encoding`, `pdf_password` | URL |
| `imgproxy.info_url` | `source`, `options`, `source_encoding`, `pdf_password` | URL |
| `imgproxy.video_url` | `source`, `options`, `extension`, `source_encoding` (`pdf_password` means error) | URL |
| `imgproxy.stream_url` | `source`, `protocol`, `renditions`, `segment_duration`, `segment_format`, `options`, `source_encoding` | URL |
| `query.url` | `source`, `params` (list of `[key, value]`; `null` skipped), `options`, `extension`, `pdf_password` | URL |
| `query.video_url` | `source`, `options` (several pipelines are written in order: keys may repeat), `extension` | URL |
| `query.stream_url` | as `imgproxy.stream_url` without `source_encoding` | URL |
| `query.imaginary_url` | `endpoint`, `source`, `params` | URL |
| `sign` | `message` | signature |
| `sign_xi_url` | `url`, `mount` | URL |
| `encrypt_source` | `source` | token |
| `encrypt_pdf_password` | `password` | token |
| `xi_source` | `name`, `key` | source |
| `source_url` | `scheme`, `authority`, `key` | source |
| `range_header` | `range`: `{start, end?}`, `{suffix}` or a string | header value |
| `escape_plain_source`, `escape_query_value` | `value` | escaped string |

Build every positive case through both the options-mapping form and the fluent form where both exist (the Python runner does). They must agree. Each error code maps to one class of fault:

| Code | Raised for |
|---|---|
| `unknown_option` | an option name not in the catalogue |
| `invalid_argument` | no arguments, a forbidden character, a non-finite number, an unsupported type, `:` in a `/q/video` argument |
| `invalid_source` | an empty source, or a source with control characters |
| `invalid_extension` | an extension that does not match `url.extension_pattern` |
| `invalid_separator` | a separator that is not one character, or is forbidden |
| `missing_signing` | a PDF password without a signing key |
| `missing_encryption` | an encrypted source or a PDF password without an encryption key |
| `invalid_key` | key material that is not hex, a key of the wrong size, an IV that is not 16 bytes, a signature size outside 1–32 |
| `invalid_password` | an empty password, a password with control characters, a password over 1024 UTF-8 bytes |
| `invalid_request` | chained `/info` or `/video` pipelines, a PDF password on `/video`, a stream option given as an option, an unknown protocol, `ts` on DASH, a URL outside the mount, two `xi_sig` |
| `invalid_name` | a bad `xi://` source name or authority |
| `invalid_endpoint` | an imaginary endpoint that is not lower-case letters, or is `video` |
| `invalid_range` | a negative range value, or an end before the start |

When a statically typed API cannot express a case (Python's `video_url` has no PDF password parameter), skip it with that reason. Do not add a parameter just to raise the error.
