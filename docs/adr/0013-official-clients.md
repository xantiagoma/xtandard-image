# ADR 0013: Official clients share one spec, one conformance fixture and one generator

- Status: accepted (WP-134; owner decision 2026-10-15)
- Date: 2026-10-15

## Context

The owner (2026-10-15) asked for official URL builders and clients in the main backend languages: **Python, Go, PHP, Ruby, Rust, Java, .NET and Elixir**, next to the TypeScript package `@xtandard/image` (ADR 0009, WP-84, WP-133). The constraints he set:

- No Bazel. Keep mise and Taskfile, use each language's native tooling, and run CI only for what changed.
- Signing belongs on servers: a key in an app signs and decrypts everything. *Amended 2026-10-22 (owner):* Swift (WP-142), Kotlin Multiplatform (WP-143) and Dart (WP-144) clients exist for apps too; signing and encryption stay optional there, and their docs recommend the server's `restricted` unsigned policy or backend-signed URLs because keys shipped in an app can be extracted.
- WP-134 builds the shared foundation and the Python client. The other languages follow in parallel work packages (WP-135 onwards) that reuse the foundation unchanged, so it has to be easy to plug into and documented for an agent who has never seen the TypeScript package.

Each client has to know the same things the TypeScript package knows: three option catalogues (processing, `/info`, `/video`: about 90, 35 and 50 options, with aliases, positional arguments, enums and ranges), the query dialect's keys, the stream protocols and segment formats, the source schemes, two signature schemes (imgproxy, with `/video` signing `"/video" + path`, and `xi_sig`), the `enc/` token format and PDF passwords. They must also make the same URLs: a signature covers the bytes of the path, so `300` against `300.0` or a different percent-encoding is a different URL and a 403.

The TypeScript package reads that knowledge from `internal/apidoc/openapi.json` (its `x-xi-*` extensions) and proves its URLs with `testdata/urls/imgproxy-builder.json`. The fixture's cases come from the Go golden tables, TypeScript writes the URLs, and the Go parsers read them back (`TestBuilderFixture`). Eight more languages copying that per-language machinery, or reverse-engineering `openapi.json`, would drift.

## Decision

### 1. Layout

```
clients/
  README.md            how a language plugs in (steps, API surface, naming, fixtures)
  spec/spec.json       language-neutral spec, generated from the Go catalogues
  spec/conformance.json  cases every client runs
  codegen/             the one generator (Bun): generate.ts, lib.ts, spec.ts, emitters/<lang>.ts
  python/              PyPI xtandard-image (WP-134)
  go/  php/  ruby/  rust/  java/  dotnet/  elixir/   (WP-135 …)
  typescript/          npm @xtandard/image, the reference client (moved from packages/image-url, 2026-10-22)
```

- **TypeScript lives in `clients/typescript`** (npm `@xtandard/image`). It was first `packages/image-url` (`@xtandard/image-url`, ADR 0009) and stayed there when this ADR was written; on 2026-10-22 the owner asked to move it next to the other clients and drop the `-url` suffix, before anything was published, so no npm name was ever released. It is still a Bun workspace package with a lockfile, a licence check and a release job, and the reference implementation of the conformance fixture (below). `packages/*` stays a Bun workspace for future TypeScript integrations (React components, framework middlewares) that depend on `@xtandard/image`.
- **One directory per language**: `clients/<lang>/`, where `<lang>` is `python`, `go`, `php`, `ruby`, `rust`, `java`, `dotnet` or `elixir`. Each directory is a self-contained project in its ecosystem's layout (`pyproject.toml`, `go.mod`, `composer.json`, `*.gemspec`, `Cargo.toml`, `pom.xml` or Gradle, `*.csproj`, `mix.exs`). It has a copy of the root `LICENSE` (checked by `task clients:check`), a `README.md` with a coverage table, a `mise.toml` and a `Taskfile.yml` whose `ci` task runs everything.
- **The Go client is a separate Go module**, `clients/go` with module path `github.com/xantiagoma/xtandard-image/clients/go`, released with tags `clients/go/vX.Y.Z`. It imports nothing from `internal/` (the Go toolchain forbids that across modules anyway) and has no cgo. It is **the sanctioned exception to CLAUDE.md non-negotiable 2** ("all code lives under `internal/`, no `pkg/` without an ADR"). That rule is about the server's code. The client is a public library for other people's programs, so it is public by definition. The root `go.mod` gains `ignore ./clients`, so `./...` in the server module never walks the clients (a nested `go.mod` already excludes `clients/go`). The Go client follows `docs/go-standards.md` where it applies to a library: Go 1.27, stdlib first, `go-cmp` in tests, golangci-lint. It has no dependencies except possibly `go-cmp` for tests.
- `.dockerignore` drops `clients`. The server image never sees them.

### 2. The language-neutral spec: `clients/spec/spec.json`

`internal/apidoc` exports the spec (`BuildClientSpec`, `MarshalClientSpec`) from the same catalogues as the OpenAPI document, and commits it. `TestClientSpec` fails when the committed file differs (`task clients:spec` = `go test ./internal/apidoc -run TestClientSpec -update`). `TestClientSpecCoversCatalogues` checks the content against the catalogues directly, so a bug in the exporter cannot be hidden by `-update`. The spec lives in `apidoc` because ARCHITECTURE §3 lets nothing but `app` import `apidoc`, and `apidoc` already owns the catalogue export, `queryKeys` and the imaginary tables.

The spec holds:

- `options.processing|info|video`: every option with its canonical name, its aliases, `short` (the name builders write by default), doc, syntax, default, milestone, scope, the pro, security, removed and shared flags, a typed example, and `forms`. Each form has positional `args` with name, type (string, integer, number, boolean, enum, color, base64, direction), enum, min, max, exclusive_min, multiple_of, pattern, nullable and doc, plus an optional repeated `rest` group and `min_items`.
- `query`: the mount, the native image keys with their aliases, the keys builders write themselves, the native `/q/video` keys (`crop` → `c`, `flip` → `fl`), and the imaginary endpoints and parameters.
- `media`: image formats, video containers, stream protocols with their manifest container and media type, segment formats with aliases, protocols and default, and the rendition and segment-duration bounds.
- `sources`: schemes with their authority and an example, the `xi://` name pattern, and the encodings.
- `signing`, `encryption`, `url`: constants and the rules as prose (the MAC, the imgproxy, `/info`, `/video` and `xi_sig` messages, the token format, the PDF password segment, separators, percent-encoding sets, number formatting, pipeline and source rules).
- `error_kinds`: the server's error kinds with status and public message, and `response_schemas`: the JSON Schemas of `InfoResponse`, `ErrorResponse` and what they reference, copied from `openapi.json`.

`spec_version` (1) changes only when a field is renamed or removed. New options never change it.

The TypeScript generator keeps reading `openapi.json`. That is the same catalogue data, and `TestTypeScriptCatalogue` already pins it. Switching it to `spec.json` is possible later, but it would rewrite `src/generated/options.ts` for no gain, so WP-134 does not.

### 3. The conformance fixture: `clients/spec/conformance.json`

There are about 150 cases, each with `op`, `config`, `input` and either `expect.value` or `expect.error`. They cover:

- imgproxy URLs: signed and unsigned, placeholders, base URL, base64, plain and encrypted sources with a fixed IV, `split_source`, the name styles, booleans and nulls, numbers, chained pipelines, separators, rest arguments, PDF passwords;
- `/info`, `/video`, and streams with renditions, segment duration, `ts` and `fmp4`;
- the query dialect: native keys, `o=`, mounts, `xi_sig`, `pdp=`, `/q/video` with native keys and repeated keys, streams, imaginary;
- the helpers: `sign`, `sign_xi_url`, the two encrypt functions, `xi_source`, `source_url`, `range_header`, the two escapes;
- negative cases with a language-neutral error code: `unknown_option`, `invalid_argument`, `invalid_source`, `invalid_extension`, `invalid_separator`, `missing_signing`, `missing_encryption`, `invalid_key`, `invalid_password`, `invalid_request`, `invalid_name`, `invalid_endpoint` and `invalid_range`.

Inputs are written by hand. The reference implementation, the TypeScript client, writes `expect.value` (`XI_UPDATE_FIXTURES=1 bun test src/conformance.test.ts`, which is `task clients:fixtures`). `internal/dialect/imgproxy` `TestConformanceFixture` then holds every value to the server:

- each URL parses with the dialect's own parser to the case's source;
- a signed URL has a verified signature;
- the stream settings and the PDF password come out as requested;
- tokens decrypt to their plaintext;
- signatures equal the server's HMAC;
- error cases carry a known code.

TypeScript checks that every error case throws. Every other client checks the code. A case that a statically typed API cannot express is skipped with that reason. Python skips one: `video_url()` has no PDF password parameter.

Every client also runs the two existing shared fixtures, which stay where they are because Go and TypeScript already read them there:

- `testdata/urls/imgproxy-builder.json`: more than 400 cases from the golden tables, with `url`, `url_short` and `query_url`;
- `testdata/security/vectors.json`: the imgproxy, `xi_sig`, `enc/` and PDF password vectors.

### 4. One generator, per-language emitters (Bun)

`clients/codegen/generate.ts` reads `spec.json` and runs every `emitters/<lang>.ts`, discovered by file name. Each emitter returns `{path: content}` for files under its own `clients/<lang>/`, which are typically the generated option types, the name tables, the fluent per-option methods, constants and response types. The commands are `task clients:generate` (or `ONLY=python`) and `task clients:check`, which fails on drift, typechecks the generator and compares the LICENSE copies. `lib.ts` holds the shared expansion rules, so all languages agree on what an options value may be: forms into fixed shapes plus a rest group, and which options take a bare first argument.

Why Bun rather than Go:

- The generator is development tooling, never shipped, and writes source for eight languages. ~~Template strings in TypeScript are shorter to write and review than `text/template`.~~ *Amended 2026-10-23 (owner, WP-146):* the emitters no longer write template strings (below); TypeScript is still the better host, because the code model, the printers and the playground's snippet generator share one Doc algebra.
- A Go generator under `internal/` would put eight languages' emitters under golangci-lint and the Go standards, and the Go module would have to change every time a language is added.
- Bun is already pinned in the root `.mise.toml` and runs on the host with no Docker. The Go half (the spec export) stays in Go because only Go can read the catalogues.
- Discovery by file name means the parallel language work packages add one file each and never edit a shared registry, so they never conflict.

**Amendment 2026-10-23 (owner, WP-146): a code model, refiners and Doc printers instead of template strings.** The owner ruled out source built by concatenating strings or template literals: it does not scale to later changes (a new modifier, a wrapped signature, a different doc comment style means editing hundreds of interpolations across eleven emitters). The generator now has the same design as the playground's snippet generator (WP-145, Kiota's CodeDOM → refiner → writer):

- `build.ts` turns the spec into a language-neutral model: the catalogues with each option's names, aliases, short name, argument shapes (`lib.ts`), bare arguments and documentation tags; the enumerations and constants; the response schemas with sorted properties.
- `model.ts` is a declaration-level code model of typed nodes: files, comments, imports, namespaces, type aliases, constants, enums, class-like declarations (class, struct, record, interface, trait, protocol, mixin, extension), fields, functions and parameters, annotations; the statements and expressions their bodies need; type references (generic, nullable, union, literal, tuple). A language adds kinds of its own as `<lang>.<kind>` nodes.
- Each `emitters/<lang>.ts` is the language's **refiner**: it lowers the neutral model into nodes in the shape its client wants. Each `lang/<lang>.ts` is the **printer** (a subclass of `printer.ts`) with the language's **leaf tokens**: string escaping against its interpolation markers, numbers as JavaScript writes them (the spec's rule), identifier casing and reserved words, comment escaping. Only leaf tokens, and the text of one comment line, are strings.
- Printers produce a Doc in the Wadler/Leijen algebra (`text`, `concat`, `group`, `indent`, `line`, `softline`, `hardline`, `ifBreak`, `join`), and the layout decides the line breaks from the printer's width. There is one implementation: `clients/codegen/doc.ts` re-exports `internal/playground/assets/snippets/doc.js`, which cannot move because `go:embed` reads only below `internal/playground`; it carries JSDoc types for the generator (compiled with `allowJs`), and a test checks that the generator and the playground's printers use that same module.
- An official formatter may follow the printer when the client's toolchain has it: Go keeps `gofmt`. The migration kept every committed file byte-identical (`generate --check`), so printers set an unbounded width where the committed files have long lines; a client that adopts its formatter later changes the width and regenerates.
- `clients/codegen/test/` (part of `task clients:check`) tests the Doc helpers, the model, the printer base, each language's leaf tokens and narrow-width layout, and one golden per language for a small spec (`test/fixture.ts`).
- The TypeScript client's `scripts/generate.ts` writes `options.ts` with the same model and a TypeScript printer; `openapi.ts` stays openapi-typescript's output (it prints from an AST already).

### 5. Toolchains: a `mise.toml` per client directory, Docker where mise has none

Each `clients/<lang>/mise.toml` pins that language's toolchain: for example Python 3.14.8 and uv 0.13.1, Rust, a JDK with Gradle or Maven, .NET, Erlang with Elixir, PHP with Composer, Ruby with Bundler. mise picks it up in that directory, the client's `Taskfile.yml` runs its commands through `mise exec --`, and CI uses the official setup actions with the same versions.

A language whose toolchain mise cannot install reliably on macOS and Linux may use the **official** language image instead, through a `docker run` in its Taskfile (`php:<ver>-cli`, `elixir:<ver>`, `mcr.microsoft.com/dotnet/sdk:<ver>`). It must never use or retag the `xtandard-base:*` images. The language's work package records the choice in its README.

The minimum supported runtime is each ecosystem's oldest non-EOL version (Python 3.10). CI tests the range.

### 6. Versioning and publishing

All clients follow the server's release line in lockstep, ~~`0.x` before 1.0~~ *(amended 2026-10-25, owner, WP-152: `0.x` with no planned 1.0, ZeroVer; docs/RELEASE.md "Versioning")*. The types describe that server version, as ADR 0009 says for npm. Nothing is published by WP-134. The targets, and the release job each language adds when the release pipeline publishes it (behind a repository variable like `XI_NPM_PUBLISH`):

| Language | Registry | Name |
|---|---|---|
| TypeScript | npm | `@xtandard/image` |
| Python | PyPI (trusted publishing, OIDC) | `xtandard-image` (import `xtandard_image`) |
| Go | pkg.go.dev (module proxy, tag `clients/go/vX.Y.Z`) | `github.com/xantiagoma/xtandard-image/clients/go` |
| PHP | Packagist | `xtandard/image` |
| Ruby | RubyGems | `xtandard-image` |
| Rust | crates.io | `xtandard-image` |
| Java | Maven Central | `io.github.xantiagoma:xtandard-image` |
| .NET | NuGet | `Xtandard.Image` |
| Elixir | Hex | `xtandard_image` |
| Swift | SwiftPM, the repository URL itself (root `Package.swift`, tag `vX.Y.Z`); Swift Package Index by URL | product `XtandardImage`, package identity `xtandard-image` |

**No mirror repositories (owner, 2026-10-22).** Registries that read a manifest at the repository root are served from this repository's root instead of a split repo: PHP through a root `composer.json` that autoloads `clients/php/src` and `clients/php/generated` (Packagist), and Swift through a root `Package.swift` whose targets use `path: "clients/swift/…"` (SwiftPM). Both take their versions from the repository's own `vX.Y.Z` tags, which is the lockstep versioning above. Composer downloads release zips, so `.gitattributes` marks `/testdata` `export-ignore` (about 20 MB down to 6 MB); SwiftPM clones the repository (about 24 MB with history). Development manifests stay in each client's directory (`clients/php/composer.json` with tests and tools); a release checklist item keeps the root `require`/`autoload` in sync. The root `Package.swift` (WP-142) declares only the Swift library and its test target, both under `clients/swift/`, plus one dependency, swift-crypto, built only off Apple platforms; tests read the shared fixtures by relative path from the clone. It needs nothing else at the root: Go ignores it, Bun and the other clients never read it, and the server's Dockerfile copies only `go.mod`, `go.sum`, `cmd/` and `internal/` (a few hundred bytes of manifest in the build context are never used). Consumers write `.package(url: "https://github.com/xantiagoma/xtandard-image", from: "0.x.y")` and the product `XtandardImage`; there is no Swift-specific tag prefix, so a Swift release is a server release.

Each language's work package checks that its name is free on the registry and records the result in its README.

### 7. CI: one workflow per client, path-filtered

- `clients.yml`: the generated files and the generator (paths `clients/**`).
- `client-<lang>.yml`: one per language, for its toolchain and its supported-version matrix. It runs when `clients/<lang>/**`, `clients/spec/**`, the shared `testdata/` fixtures or its own workflow change.
- `ci.yml` ignores `clients/codegen/**` and `clients/<lang>/**` but not `clients/spec/**`, because the Go tests read the spec and the fixture.
- `ts.yml` also watches `clients/spec/conformance.json`.

Separate files keep the parallel work packages from editing the same workflow. While Actions are off, the jobs run only when the repository variable `XI_CLIENTS_WORKFLOW` is `enabled`. `task clients:test` runs `task ci` in every `clients/*/` that has a `Taskfile.yml`.

### 8. Why not Bazel (or Pants, Buck2)

- The server is cgo over libvips and FFmpeg, built in Docker base images (ADR 0005). Bazel would need a hermetic C toolchain and rules for about 20 C libraries that the base images already build, or it would wrap `docker build` and add nothing.
- Every ecosystem here has a native tool that its users and contributors know: `go`, `uv`, `cargo`, `mvn`/`gradle`, `dotnet`, `mix`, `composer`, `bundle`, `bun`. Bazel's rules for PHP, Elixir and Ruby are community-maintained and lag behind the languages.
- The team is one owner plus agents. Bazel's cost is the BUILD files, the remote cache and debugging sandboxing. That pays off with hundreds of engineers, not here.
- Path-filtered workflows and the per-language Taskfiles already give "test only what changed" at this repo's size.
- Revisit when CI time for unaffected targets becomes a problem, when there are cross-language build dependencies beyond the JSON spec (generated protobufs, shared native code), or when clients need hermetic, reproducible builds that mise and the lockfiles cannot give. The lighter options to try first are **moon** (moonrepo: per-project tasks, affected-project detection, a toolchain manager, not hermetic) and **Nx** (affected graph, caching, plugins for most of these languages, heavier and Node-centred).

## Consequences

- A language work package is mostly the client itself: `clients/<lang>/` with its emitter, its runtime (builders, signing, encryption, an HTTP client), its tests over the three fixtures, its Taskfile, `mise.toml`, workflow, README and the ROADMAP row. `clients/README.md` is the checklist.
- A new server option reaches every client through `task clients:spec clients:generate`. A grammar change (a new rule) reaches them through a new conformance case, which fails every client until it is implemented.
- `spec.json` (about 200 KB) and every client's generated files are committed and reviewed like `openapi.json`.
- The licence policy (ADR 0006) extends to each client's runtime dependencies, checked by a script per client. Python's exceptions: `certifi` (MPL-2.0, Mozilla's CA bundle, unmodified, through httpx) and `typing-extensions` (PSF-2.0, Python 3.10 only).

## Implementation notes (WP-134)

- Go: `internal/apidoc/clientspec.go` (+ test), with `imaginaryEndpoints()`/`imaginaryParams()` split out of `imaginaryOperation()` (`openapi.json` unchanged); `internal/dialect/imgproxy/conformance_test.go`. `go.mod` gains `ignore ./clients`.
- TypeScript: `src/conformance.test.ts`. One runtime guard matching the types: `buildImgproxyVideoUrl` now throws for a `pdfPassword` from untyped callers instead of dropping it.
- Python (`clients/python`): runtime dependencies `httpx` (BSD-3-Clause, 0.28.1) and `cryptography` (Apache-2.0 OR BSD-3-Clause, 50.0.2), plus `typing-extensions` on Python 3.10. Development dependencies (`pytest`, `ruff`, `pyright`, `mypy`, `packaging`) are locked in `uv.lock`, and `hatchling` builds the package.
