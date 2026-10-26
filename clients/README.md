# Official clients

Libraries that build and sign URLs for an [xtandard-image](../README.md) server, encrypt `enc/` sources and PDF passwords, and call `/info`, the media endpoints and the health endpoints. They are clients only: you still run the server ([Getting started](../docs/GETTING-STARTED.md)). Each client's README is the page its package registry shows.

Build and sign URLs **on a server**: the signing key signs everything, and the encryption key decrypts every `enc/` source and PDF password. The Swift, Kotlin Multiplatform and Dart clients also run in apps: signing and encryption stay optional there, because keys shipped in an app can be extracted; use the server's `restricted` unsigned policy (presets, allowed sizes) or URLs signed by your backend.

| Language | README | Package | Install (once published) |
|---|---|---|---|
| TypeScript | [`clients/typescript`](typescript/README.md) | npm `@xtandard/image` | `npm i @xtandard/image` |
| Python | [`clients/python`](python/README.md) | PyPI `xtandard-image` | `pip install xtandard-image` |
| Go | [`clients/go`](go/README.md) | module `github.com/xantiagoma/xtandard-image/clients/go` (tags `clients/go/vX.Y.Z`) | `go get github.com/xantiagoma/xtandard-image/clients/go` |
| PHP | [`clients/php`](php/README.md) | Packagist `xtandard/image` (from the root `composer.json`) | `composer require xtandard/image` |
| Ruby | [`clients/ruby`](ruby/README.md) | RubyGems `xtandard-image` | `gem install xtandard-image` |
| Rust | [`clients/rust`](rust/README.md) | crates.io `xtandard-image` | `cargo add xtandard-image` |
| Java | [`clients/java`](java/README.md) | Maven Central `io.github.xantiagoma:xtandard-image` | Maven or Gradle coordinates |
| Kotlin Multiplatform | [`clients/kotlin`](kotlin/README.md) | Maven Central `io.github.xantiagoma:xtandard-image-kmp` (JVM, Android, iOS, macOS) | Gradle coordinates |
| .NET | [`clients/dotnet`](dotnet/README.md) | NuGet `Xtandard.Image` (+ `Xtandard.Image.Extensions` for DI) | `dotnet add package Xtandard.Image` |
| Elixir | [`clients/elixir`](elixir/README.md) | Hex `xtandard_image` | `{:xtandard_image, "~> 0.1.0"}` |
| Swift | [`clients/swift`](swift/README.md) | SwiftPM product `XtandardImage`, from the root `Package.swift` (tags `vX.Y.Z`) | `.package(url: "https://github.com/xantiagoma/xtandard-image", from: "0.1.0")` |
| Dart and Flutter | [`clients/dart`](dart/README.md) | pub.dev `xtandard_image` | `dart pub add xtandard_image` |

Nothing is published yet; publishing to each registry is being set up ([RELEASE](../docs/RELEASE.md)). Every client is versioned in lockstep with the server's release tags (`0.x`, [ZeroVer](https://0ver.org)). The design is in [ADR 0013](../docs/adr/0013-official-clients.md).

## Client READMEs

Every client README follows one template, adapted to the language: the logo, `# xtandard-image for <Language>`, a one-line tagline and a badge row; two or three sentences saying it is a client for the server, with the `docker run` line from the root README (full `ghcr.io/xantiagoma/xtandard-image:<tag>` reference, the tag the root README uses, and the `XI_SOURCES…` allowlist); Install (the registry command first); a Quick start that signs a URL with keys from the environment and fetches `/info`; short Usage sections (building URLs, chained pipelines, sources, `/info`, video and streams, the query dialect, signing and encryption, the HTTP client, errors); Compatibility; Links. The option reference stays in [URL-DIALECTS](../docs/URL-DIALECTS.md) and the language's API docs, not in the README.

- **Links are absolute** (`https://github.com/xantiagoma/xtandard-image/blob/main/...`): registries ship the README without `docs/`.
- **HTML or Markdown.** npm, PyPI, crates.io, Packagist and GitHub (Ruby, Java, Kotlin, Swift) render HTML: the logo is `<p align="center"><img …></p>`. pkg.go.dev, NuGet, pub.dev and Hex/ex_doc drop or escape raw HTML: the logo is Markdown `![xtandard-image](…)` ([BRAND](../docs/BRAND.md#where-each-file-goes)).
- **Registry status marker.** Until a client is published, its badge row has a static `https://img.shields.io/badge/<Registry>-pending-lightgrey` badge linking to [RELEASE](../docs/RELEASE.md), and its Install section has one line, "Not published yet: publishing to <Registry> is being set up. Until then, install from the repository:", before the install from the repository. Both carry the marker `registry-status: pending`: as `<!-- registry-status: pending -->` at the end of the line in the HTML READMEs, and as a `[//]: # (registry-status: pending)` line directly above them in the Markdown-only ones (Go, .NET, Elixir, Dart), where an HTML comment could be shown as text. When a client is published, `rg 'registry-status: pending' clients/<lang>/README.md` finds both: swap the badge for the registry's version badge, and delete the note line (with its marker) and the install from the repository below it.
- **Packagist and Swift Package Index** read the repository root (`composer.json` and `Package.swift` live there), so they show the root README, whose Clients table links to `clients/php` and `clients/swift`.

## What is shared

| Path | What | Written by | Checked by |
|---|---|---|---|
| `clients/spec/spec.json` | Everything a client needs from the server: the processing, `/info` and `/video` option catalogues (names, aliases, short names, typed positional arguments with enums and ranges, docs), query keys, imaginary endpoints, containers, stream protocols and segment formats, source schemes, signing, encryption and URL rules, error kinds, response JSON Schemas | `internal/apidoc` `BuildClientSpec` (`task clients:spec`) | Go `TestClientSpec`, `TestClientSpecCoversCatalogues` |
| `clients/spec/conformance.json` | About 150 cases: inputs to expected URL or value, or to an error code | inputs by hand; values by the TypeScript client (`task clients:fixtures`) | Go `TestConformanceFixture` (server parsers), every client's tests |
| `testdata/urls/imgproxy-builder.json` | 400+ cases derived from the Go golden tables: `url` (names as given), `url_short` (short names), `query_url` | Go `TestBuilderFixture -update` + TypeScript (`task ts:fixtures`) | Go and every client |
| `testdata/security/vectors.json` | imgproxy, `xi_sig`, `enc/` and PDF password vectors | by hand, from imgproxy's docs and examples | Go and every client |
| `clients/codegen/` | The generator: `generate.ts` runs every `emitters/<lang>.ts` (a refiner) on `spec.json`; `build.ts` is the language-neutral model, `model.ts` the code model, `printer.ts` and `lang/<lang>.ts` the printers on the playground's Doc algebra (`doc.ts`); `lib.ts` has the shared shape rules | — | `task clients:check` (drift, `bun test clients/codegen/test`, typecheck) |

## Commands

```sh
task clients:spec          # rewrite spec.json from the Go catalogues (after a server option change)
task clients:generate      # regenerate every client's generated files (ONLY=python for one)
task clients:fixtures      # fill conformance.json expect.value from TypeScript, then check every URL against the server
task clients:check         # no drift: spec.json, generated files, codegen tests and typecheck, LICENSE copies
task clients:test          # `task ci` in every clients/*/ that has a Taskfile.yml
task clients:python:test   # one client
```

After changing a Go option: `task clients:spec clients:generate`, then `bun run generate` in `clients/typescript`, then commit everything.

## Adding a language

Follow these steps in order. Python (`clients/python`) is the worked example. Copy its structure, not its idioms.

1. **Directory.** Create `clients/<lang>/` as a normal project for its ecosystem. Add:
   - a copy of the root `LICENSE`;
   - a `README.md` that follows the template in [Client READMEs](#client-readmes) (copy the TypeScript or Python one), and is wired into the package manifest so the registry shows it;
   - a `mise.toml` pinning the toolchain (ADR 0013 §5; use the official language Docker image only if mise cannot install it, and never `xtandard-base:*`);
   - a `Taskfile.yml` whose `ci` task installs, lints, type-checks, tests, checks licences and builds. `task clients:test` finds it automatically.
2. **Generator: refiner, printer, leaf tokens.** Generated files are never built by concatenating strings or template literals (WP-146): a language is a **refiner** that builds typed nodes, a **printer** that turns them into a layout document, and its **leaf tokens**.

   ```
   spec.json ──build.ts──▶ neutral model ──emitters/<lang>.ts (refiner)──▶ nodes (model.ts)
             ──lang/<lang>.ts (printer)──▶ Doc (doc.ts) ──layout──▶ text ──official formatter (optional)──▶ file
   ```

   - **Neutral model** (`build.ts`, shared, do not fork it): `catalogues(spec)` gives each catalogue's options with `names` (canonical, then aliases), `short`, `shapes` (argument lists: fixed elements plus an optional repeated tail, from `lib.ts`), `bare` (the argument types that may stand alone, `q: 80`), `args` and `tags` (milestone, scope, flags as doc phrases), plus the sorted `names` table and `byName`. Also `enums(spec)`, `imaginaryParams`, `queryKeyNames`, `nativeVideoKeys`, `defaultSegmentFormat` and `responseSchemas` (sorted, with sorted properties and `required`).
   - **Refiner** (`clients/codegen/emitters/<lang>.ts`): exports `emitter: Emitter` (`spec.ts`), `{ language, emit(spec) }`, which returns `{path: text}` for files under `clients/<lang>/` only. It lowers the neutral model into the nodes of `model.ts`, built with `N`: files, comments (`N.doc`, `N.line`), imports, namespaces, type aliases, constants, enums and members, classes/structs/records/interfaces/traits/protocols/mixins (`N.type`), fields, functions and parameters, annotations; statements (`ret`, `stmt`, `let`, `if`, `switch`/`case`, `forEach`, `throw`); expressions (literals, `id`, `get`, `call`/`invoke`, `new`, `index`, `named`, `list`/`tuple`, `map`/`entry`, `binary`, `unary`, `lambda`, `spread`, `typeExpr`); type references (`tref` with type arguments and nullability, `tunion`, `tlit`, `ttuple`). Constructs only one language has are its own kinds, `N.ext("<lang>.<kind>", fields)`. Spacing between declarations is `N.blank()`; a collection, call or type-argument list takes `layout: "expanded"` to be one item per line (tables), else the layout breaks it only past the printer's width. Then `printer.print(file)` per file.
   - **Printer** (`clients/codegen/lang/<lang>.ts`): a subclass of `Printer` (`printer.ts`) that maps nodes to a Doc with the algebra of `doc.ts` (`concat`, `group`, `indent`, `line`, `softline`, `hardline`, `ifBreak`, `join`, plus `lines`, `block`, `bracket`). The base prints expressions and types the C-like way and throws for declarations a language must define; override what your language writes differently, and print your `<lang>.*` kinds in `ext()`. It sets `unit` (indentation) and `width` (`Infinity` keeps every group flat, which is what the committed clients were written with). Never write `"\n"`: lines come from `hardline`, groups and `block`.
   - **Leaf tokens** (in the same file): string escaping for the language's literals (quotes, backslashes, and its interpolation markers: `$` in Kotlin and Dart, `#{` in Ruby and Elixir, `\(` in Swift), number formatting (JavaScript's, the spec's rule), identifier casing and reserved words, comment and docstring escaping. These and the text of one comment line are the only strings a refiner or printer composes.
   - **Official formatter** (optional): the printed text may go through the language's formatter when it is on `PATH` with the client's toolchain. Go pipes through `gofmt`; the others print the formatted shape directly. A formatter step must not change committed output without regenerating it.
   - **Tests** in `clients/codegen/test/` (`bun test clients/codegen/test`, part of `task clients:check`): `<lang>.test.ts` for the leaf tokens (escaping, casing, keywords, numbers) and a narrow-width layout; `golden.test.ts` prints every emitter's files for the small spec of `fixture.ts` into `test/golden/<lang>/` (`XI_UPDATE_GOLDEN=1` rewrites them).

   Generate at least the per-catalogue name tables (name or alias to canonical, canonical to short). Where the language can express them, also generate:
   - the typed option values (one type per option);
   - the fluent per-option methods;
   - the constants (formats, containers, stream manifests, segment formats, signature sizes, key sizes, separators, safe sets);
   - the response types (`InfoResponse`, `ErrorResponse`).

   Mark generated files `DO NOT EDIT` and keep them out of formatters and linters that would fight the generator. `task clients:generate` writes them and `task clients:check` fails on drift. Never import the TypeScript package or read `openapi.json` directly. `emitters/python.ts` with `lang/python.ts` is the smallest complete example.
3. **Runtime.** Implement the API surface below by hand on the generated tables. Use the language's standard HMAC-SHA256, AES-CBC (or its most-used crypto library) and HTTP client (one that is idiomatic, and async where the ecosystem expects it).
4. **Tests.** Run all three shared fixtures from the repository by relative path, plus unit tests and HTTP client tests against a stand-in server. Add a smoke test against a real server, skipped unless `XI_SMOKE_URL` is set; the playground demo at `http://127.0.0.1:8080` has unsigned URLs allowed, `xi://local/jpeg/baseline.jpg` and `xi://local/video/bbb-h264-aac.mp4`.
5. **Licences.** Add a script that checks the runtime dependency closure against MIT, BSD, Apache-2.0 and ISC (ADR 0006). Document any exception with its reason, in the script and in the README.
6. **CI.** Add `.github/workflows/client-<lang>.yml`, copied from `client-python.yml`, with path filters `clients/<lang>/**`, `clients/spec/**`, `testdata/urls/**`, `testdata/security/**` and the workflow itself. It is gated by `vars.XI_CLIENTS_WORKFLOW == 'enabled'` and has a matrix over the supported runtimes. Add `'clients/<lang>/**'` to `ci.yml`'s `paths-ignore`.
7. **Docs.** Fill in your row in the table above and in `docs/ROADMAP.md` (M6), and add a line in the root README's clients section. Note anything you could not do in the WP's "Deviations".

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
| HTTP QUERY (opt-in) | `queryMethodRequest(url)` → `{ method: "QUERY", url, contentType, body }`; `createClient({ queryMethod: true, queryMount })`, `media(url, { method: "QUERY" })` | `query_method_request(url)` → `QueryMethodRequest(method, url, content_type, body)`; `Client(query_method=True, query_mount=…)`, `media(url, method="QUERY")` | Off by default (WP-147, [URL-DIALECTS §4.4](../docs/URL-DIALECTS.md#44-http-query)). The request is the built query dialect URL split at the first `?`: the URL without its query, `Content-Type: application/x-www-form-urlencoded`, and the query as written (`xi_sig` included) as the content. A URL without a query (or with a fragment) is an `invalid_request` error. URL builders are unchanged |
| errors | `TypeError`/`RangeError`/`Error`; `XtandardError` (HTTP) | `URLBuildError(code)`; `HTTPError` subclasses by status; `TransportError` | builder errors carry the conformance error code |

Naming conventions:

- **Go:** `imgproxy.New(cfg)`, `URL`, `InfoURL`, `VideoURL`, `StreamURL`, typed option structs or `Options` maps, `errors.Is` sentinels per error code.
- **PHP:** PSR-12, namespace `Xtandard\Image`.
- **Ruby:** snake_case, module `XtandardImage`.
- **Rust:** crate `xtandard_image`, builder structs with `Result<_, Error>` where the `Error` enum has one variant per code.
- **Java:** package `io.github.xantiagoma.xtandard.image`.
- **.NET:** namespace `Xtandard.Image`, async with `Task`.
- **Elixir:** `XtandardImage`, `{:ok, url} | {:error, %XtandardImage.Error{code: :invalid_source}}`.
- **Swift:** module `XtandardImage`, `ImgproxyURLBuilder`/`QueryURLBuilder` (factories `imgproxy(…)`, `queryURLs(…)`), lowerCamelCase option methods on `Sendable` value chains, `Options` dictionary literals, typed throws `URLBuildError` with `code == .invalidSource`, `XtandardClient` with `async throws(ClientError)`.
- **Dart:** library `package:xtandard_image/xtandard_image.dart`, `ImgproxyUrlBuilder`/`QueryUrlBuilder` with named parameters, lowerCamelCase option methods (reserved words get `_`: `do_`), sealed `OptionValue`, `UrlBuildException(ErrorCode.invalidSource)`, `XtandardClient` returning `Future`s.

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
| `query.query_request` | `build` (`query.url`, `query.video_url`, `query.stream_url` or `query.imaginary_url`) plus that op's inputs; the case's `config` applies | the URL that `build` produces, as an HTTP QUERY request: `"QUERY " + url without its query + "\nContent-Type: application/x-www-form-urlencoded\n\n" + query` |
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
