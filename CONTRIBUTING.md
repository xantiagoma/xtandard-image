# Contributing to xtandard-image

Thank you for helping. This file covers how to set up a checkout, which checks must pass and what a pull request needs. Bug reports and feature requests go through the [issue templates](https://github.com/xantiagoma/xtandard-image/issues/new/choose). **Never report a security problem in a public issue.** [SECURITY.md](SECURITY.md) explains how to report it privately.

By taking part you agree to the [Code of Conduct](CODE_OF_CONDUCT.md). Contributions are accepted under the project's [MIT licence](LICENSE). There is no CLA, and no DCO sign-off is needed.

## What the project is

xtandard-image is a **server**: a Go 1.27 program on libvips (through cgo), shipped as Docker images in three flavours: `slim`, `full` (adds FFmpeg LGPL and ONNX Runtime) and `gpl` (opt-in, contains GPL and AGPL software). The packages in [`clients/`](clients/README.md), including `@xtandard/image` on npm, are clients of that server and are generated from its catalogues. Most changes touch the server. Client changes usually start with a server change too.

Read these before writing code:

- [`docs/go-standards.md`](docs/go-standards.md) is binding for Go code: toolchain, HTTP, concurrency, cgo, errors, configuration, tests and dependencies. Its §10 lists the banned modules, and `depguard` enforces them.
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) covers the request lifecycle, the packages and the interfaces between them.
- [`docs/adr/`](docs/adr/) holds the decisions. Do not reopen one in a pull request without first opening an issue.
- The user docs your change affects: [`CONFIG.md`](docs/CONFIG.md), [`URL-DIALECTS.md`](docs/URL-DIALECTS.md), [`FORMATS.md`](docs/FORMATS.md) and [`SOURCES.md`](docs/SOURCES.md).

## Development setup

You need Git, Docker (Docker Desktop, OrbStack or a Linux engine, with Compose v2) and [mise](https://mise.jdx.dev). mise installs the pinned toolchain from [`.mise.toml`](.mise.toml): Go 1.27.1, golangci-lint 2.14.0, Bun 1.4.2 and [Task](https://taskfile.dev).

```sh
git clone https://github.com/xantiagoma/xtandard-image.git
cd xtandard-image
mise trust && mise install
task --list            # every task, with a description
```

### Build and test inside Docker (canonical)

The product is the Docker image, so the reference environment is the image's **builder** stage. It carries libvips 8.18 and every C library the server links, plus Go and golangci-lint. The `docker:*` tasks mount your checkout into that image (`compose.dev.yml`):

```sh
task docker:lint       # golangci-lint fmt/run, go vet, go fix -diff, go mod tidy -diff, cgo placement
task docker:test       # go test -race -shuffle=on ./...
task docker:shell      # a shell in the builder, repository at /src
```

By default they use the published builder, `ghcr.io/xantiagoma/xtandard-image-base:slim-builder`. To test the video and detection code, point `XI_BASE_IMAGE` at a `full` or `gpl` builder. The tasks then add `-tags ffmpeg`. If you cannot pull the published base, or you are changing `docker/base`, build it locally for your architecture:

```sh
task docker:base:slim                                  # xtandard-base:dev and xtandard-base:dev-builder (slow: libvips and its codecs from source)
XI_BASE_IMAGE=xtandard-base:dev-builder task docker:test
task docker:base:full && task docker:base:gpl          # optional: the other flavours, built on top of slim
```

The app image and the end-to-end suites:

```sh
task release:build                                     # the app image, xtandard-image:dev
XI_APP_BASE=xtandard-base:dev task release:build       # the same image, on a base you built locally
task release:verify && task smoke && task acceptance   # image contract, smoke test, acceptance suite
task quickstart                                        # runs the README quick start as written
task test:integration                                  # -tags integration against compose.test.yml (S3, Azurite, FTP, SFTP, Redis…)
```

### On the host

A host toolchain is fine for fast iteration. Pure-Go packages run with `CGO_ENABLED=0` (`task test:nocgo`). For the cgo packages on macOS, run `brew install vips pkg-config` and `export CGO_CFLAGS_ALLOW=-Xpreprocessor`. Homebrew's libvips is a different build from the image's, so a host-only pass does not count as verification. Run the Docker tasks before you open a pull request.

### TypeScript, the playground and the clients

```sh
bun install
task ts:check          # @xtandard/image: generated-types drift, typecheck, tests, licences, pack
task playground:test   # the playground's JavaScript unit tests
task clients:check     # spec.json, every client's generated files and the code generator agree
task clients:test      # each client's own `task ci`; toolchains come from clients/<lang>/mise.toml
```

## Checks a pull request must pass

CI runs all of these, and they must pass before you ask for a review. Run them in the builder (`task docker:lint`, `task docker:test`) or with the same versions on the host:

```sh
golangci-lint fmt --diff && golangci-lint run ./...   # the v2.14.0 binary, not `go tool`
go vet ./...
go fix -diff ./...                                     # must print nothing
go mod tidy -diff
go test -race -shuffle=on ./...
go tool -modfile=tools/go.mod govulncheck ./...
```

Add `task ts:check` and `task clients:check` when you touch TypeScript, the OpenAPI document, the option catalogues or `clients/`. Add `task quickstart` when you touch the README quick start.

## Rules that come up in review

These are short versions of [`CLAUDE.md`](CLAUDE.md) and [`docs/go-standards.md`](docs/go-standards.md). Those two files are the full rules.

- **Layout.** All Go code lives under `internal/`, and `cmd/xtandard-image/main.go` only wires things together. Only `internal/vips` imports `"C"`. The one exception is `internal/video/ffmpeg`, behind the `ffmpeg` build tag (ADR 0010). No C type leaks out of those packages, and `scripts/check-cgo.sh` checks it.
- **Configuration.** Configuration comes only from `internal/config`: one `Config` struct and `XI_*` variables, with no `os.Getenv` anywhere else, no globals and no `init()`. A new setting goes in [`docs/CONFIG.md`](docs/CONFIG.md) and the JSON Schema in the same pull request.
- **Errors and logs.** Wrap errors with `%w`. Map them to HTTP status codes in one place, and never send internal error text to clients. Log with `log/slog` JSON: static messages and `snake_case` keys.
- **Tests.** Use the stdlib `testing` package and `go-cmp`, in table-driven tests with `t.Parallel()`. Use `synctest` instead of `time.Sleep`. Every parser and decoder gets a fuzz target. Compare golden images perceptually, never byte for byte.
- **Performance claims** need `for b.Loop()` benchmarks and `benchstat` output from `-count=10` runs.
- **Dependencies need an ADR.** A new Go module, C library, base image package or client runtime dependency needs a line in the relevant ADR, with the reason and its licence. Copied or derived code, as from imgproxy, keeps its upstream header and gets an entry in [`NOTICE`](NOTICE).
- **Licence policy ([ADR 0006](docs/adr/0006-license-policy.md)).** Go modules must be MIT, BSD, Apache-2.0 or ISC. The `slim` and `full` images contain no GPL or AGPL code; LGPL is allowed when dynamically linked. GPL and AGPL software goes only in the opt-in `gpl` flavour, and only the components its amendments list. Test fixtures need a licence entry in [`testdata/LICENSES.md`](testdata/LICENSES.md).
- **Clients are generated.** The option tables of every client come from `clients/spec/spec.json`. `clients/codegen` produces them through a code model and a pretty-printer, never by string concatenation or templates. Do not edit generated files by hand. After changing a Go option, run `task clients:spec clients:generate`, then `bun run generate` in `clients/typescript`, and commit the result ([`clients/README.md`](clients/README.md)). A new language follows "Adding a language" there.
- **Docs describe what is on `main`.** Do not document planned behaviour as if it worked. A behaviour change updates the user docs and, if users will notice it, the `Unreleased` section of [`CHANGELOG.md`](CHANGELOG.md).
- **Docs are the website.** The [documentation website](https://me.xantiagoma.com/xtandard-image/) is built from the Markdown in the repository ([ADR 0015](docs/adr/0015-documentation-site.md)). Keep links relative, as GitHub renders them; the build turns them into site routes and fails on a broken link or anchor. A new file under `docs/` needs a place in `site/manifest.ts`. Preview with `task pages:dev`, check with `task pages:build`.

## Commits and pull requests

- **Conventional Commits:** `type(scope): imperative subject`. Types: `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `build`, `ci`, `chore` and `deploy`. The scope is the package or area: `fix(server): …`, `feat(dialect/imgix): …`, `docs(config): …`, `feat(clients/python): …`. Mark a breaking change with `!` (`feat(config)!: rename …`) and explain it in the body. Older history often uses just the area (`server: …`). Use the full form for new commits.
- Keep commits small and focused. A pull request does one thing. Larger work is split into work packages in [`docs/ROADMAP.md`](docs/ROADMAP.md). Open an issue first for anything big, so we can agree on the approach before you write it.
- Fill in the pull request template: what changed, why, how you verified it (paste the summary of the checks above), and any deviations from the docs or ADRs.
- A change that breaks configuration, URL options or a client API ships in the next **minor** version ([versioning](docs/RELEASE.md#versioning)). Say so in the pull request, and add an entry to the changelog.

## Reference implementations

The project reimplements other services' URL dialects and options. Check the real implementation instead of guessing: `scripts/clone-refs.sh` makes shallow clones of imgproxy, imagor, thumbor, imaginary, weserv, unpic, the provider SDKs, libvips, sharp and LibRaw in `.refs/`, which git ignores. Search them with `rg` before you implement a dialect, an option or a vips call.
