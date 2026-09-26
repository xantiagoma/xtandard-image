# Release runbook

How the app image `ghcr.io/xantiagoma/xtandard-image` is built, published and verified, and how the
npm package `@xtandard/image-url` follows it ([npm package](#npm-package)).
Design context: `docs/ARCHITECTURE.md` §14, `docs/go-standards.md` §9, ADR 0005, ADR 0009.

**Status (2026-09-27):** no release has been tagged yet, nothing is on npm, and the GitHub Pages site is not deployed. All GitHub workflows (`ci`, `base-image`, `nightly`, `release`, `pages`, `ts`) are disabled by owner decision until further notice, so everything below runs only after they are re-enabled (`gh workflow enable <name>`); until then images are built and verified locally (`task docker:*`).

## What gets published

A tag `vX.Y.Z` runs `.github/workflows/release.yml`:

| Job | Runs on | Does |
|---|---|---|
| `meta` | ubuntu-24.04 | Validates the tag (`v<major>.<minor>.<patch>[-pre]`), reads the commit and `SOURCE_DATE_EPOCH`, checks `scripts/base-image-ref.sh --check`, and resolves the base images from `docker/base/versions.sh` to `@sha256:` digests. |
| `build` (×2) | `ubuntu-24.04` (amd64), `ubuntu-24.04-arm` (arm64), native, no QEMU | Builds the `runtime` target, pushes it **by digest** (no tag yet), pulls it back and runs `scripts/verify-app-image.sh` (version, image config, ≤ 110 MB compressed, read-only rootfs + `health`, smoke test) and the M1 acceptance suite `scripts/acceptance.sh` against the pushed digest (the release gate, below). Then it attests a syft SPDX SBOM with cosign and SLSA provenance with `actions/attest-build-provenance` on the per-arch manifest, and exports the unstripped binary (`debug` target). |
| `merge` | ubuntu-24.04 | `docker buildx imagetools create` the multi-arch index, signs it keyless with cosign (`--recursive`, so the per-arch manifests are signed too), attests SLSA provenance on the index, and creates the GitHub release with the unstripped binaries and `SHA256SUMS`. |

Tags (`X.Y.Z` has no leading `v`):

| Tag | Stable release | Prerelease (`vX.Y.Z-rc.1`) |
|---|---|---|
| `X.Y.Z-slim` | yes | yes |
| `X.Y.Z` | yes | yes |
| `slim` | yes | no |
| `latest` | yes | no |

`slim` is the only app flavor the release workflow builds today. The `full` (WP-70) and `gpl` (WP-70b) base images are on `main`, built and verified locally (`task docker:base:full`, `task docker:base:gpl`); their multi-arch publish waits for GitHub Actions to be re-enabled. Once the workflow builds them, `X.Y.Z`/`latest` keep pointing at slim and `full` gets `X.Y.Z-full`/`full`.

Build properties (enforced in the `Dockerfile`):

- `CGO_ENABLED=1 go build -trimpath -buildvcs=false -pgo=auto -ldflags "-s -w -X main.version -X main.commit -X main.date"`; the build fails if `go version -m` does not report `-pgo=default.pgo`.
- `GOAMD64=v2` on amd64; arm64 keeps `GOARM64=v8.0`.
- Reproducible: pinned toolchain (in the base builder), base images by digest, `go.sum`, `main.date` and layer timestamps from `SOURCE_DATE_EPOCH` (`rewrite-timestamp=true`).
- Runtime: the base `runtime` image plus the binary only; UID 65532; `HEALTHCHECK` via `xtandard-image health`; `EXPOSE 8080 9090`; ARCHITECTURE §14 `ENV`; OCI labels `source`, `version`, `revision`, `created`, `licenses=MIT`, `description`, `base.name`.

## Cutting a release

1. Make sure `main` is green (CI `smoke` builds and verifies the image and `acceptance` runs the M1 suite against it, on both architectures) and that the base image for the current `docker/base/versions.sh` pin is published (`base-image` workflow on `main`).
2. Refresh the PGO profile if the hot path changed since the last release (below) and merge it.
3. Check the user-facing docs: the README quick start must pass (CI `quickstart` runs it on every PR; locally `task quickstart`). For a minor or major release, rerun the benchmark against the release candidate (`task release:build && bench/run.sh`, [BENCHMARKS.md](BENCHMARKS.md)) and update `bench/RESULTS.md` when the numbers moved.
4. Tag and push:

   ```sh
   git switch main && git pull --ff-only
   git tag -a v0.1.0 -m "v0.1.0"
   git push origin v0.1.0
   ```

5. Watch `release` in Actions. Nothing is tagged until both architectures have passed `verify-app-image.sh` and `acceptance.sh`; a failure leaves only untagged digests behind (GHCR retention cleans them up).
6. Verify the published image (below) and edit the generated release notes if needed. After the first release, switch the README quick start from building to the published image: the `sh quickstart-build` block becomes `docker pull ghcr.io/xantiagoma/xtandard-image:slim && docker tag ghcr.io/xantiagoma/xtandard-image:slim xtandard-image`, or the later blocks use the GHCR name directly.

7. Check the published schema and API document (below, "Schema and API site"): the last `pages` run on `main` must be green, and `curl -fsS https://xantiagoma.github.io/xtandard-image/schema/v1/config.schema.json | cmp - <(docker run --rm ghcr.io/xantiagoma/xtandard-image:X.Y.Z config schema)` must print nothing. If it differs, run the `pages` workflow from the tag.

**Dry run**: Actions → `release` → *Run workflow* with `dry_run` checked (the default) builds and verifies both architectures from the selected branch without pushing. With `dry_run` unchecked, `version` must be an existing tag; it is checked out and published like a tag push, but no GitHub release is created.

**Re-running** a release for the same tag rebuilds and re-tags; the image content is identical (reproducible build), but the digest can differ if the base image digest moved in between.

## Schema and API site (GitHub Pages)

`.github/workflows/pages.yml` publishes the config JSON Schema and the OpenAPI document to `https://xantiagoma.github.io/xtandard-image/` (WP-81b, ADR 0007 decision 6). It runs on every push to `main` that changes `internal/config/config.schema.json`, `internal/apidoc/openapi.json`, `docs/pages/**`, `scripts/pages-build.sh` or the workflow, and on demand (*Run workflow*).

| Path | Content |
|---|---|
| `/` | `index.html`: links every file, editor modelines (`docs/pages/index.html`) |
| `/api/` | Scalar API reference over `schema/latest/openapi.json` (same pinned Scalar build and SRI as admin `/docs`) |
| `/schema/v1/config.schema.json` | The config schema; this URL is its `$id` |
| `/schema/v1/openapi.json` | The OpenAPI document |
| `/schema/latest/…` | The same two files for the newest schema major |

`v1` is the schema major version read from the `$id`, not the release version, so the URL stays stable across releases. Bump it (in `config.SchemaID`, `TestSchemaID` and `docs/schemastore/catalog-entry.json`) only when a config key is removed or renamed; `scripts/pages-build.sh` then needs to keep publishing the last `v1` files next to `v2` (check them out from the last `v1` tag), because a published `$id` is forever.

Jobs: `build` runs `scripts/pages-build.sh` (the same script as `task pages:build`) and uploads `dist/pages` with `actions/upload-pages-artifact`; `deploy` publishes it with `actions/deploy-pages` to the `github-pages` environment; `verify` fetches every published JSON file, compares it byte for byte with the committed one, and validates the SchemaStore samples (`docs/schemastore/tests`) with Ajv against the schema URL their modeline names.

Before the first run the owner must, once: set Settings → Pages → Source to *GitHub Actions* and enable the `pages` workflow. Then open the SchemaStore PR ([docs/schemastore/README.md](schemastore/README.md)).

Locally, `task pages:build` assembles the same site in `dist/pages/` and lists its files; the output depends only on the committed files, so two runs are byte-identical. Serve it with any static server to check it, e.g. `python3 -m http.server -d dist/pages`.

## Verifying a published image

Signature (keyless, GitHub OIDC):

```sh
cosign verify ghcr.io/xantiagoma/xtandard-image:0.1.0 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/xantiagoma/xtandard-image/\.github/workflows/release\.yml@'
```

SBOM (SPDX, attested per architecture; resolve the platform manifest first):

```sh
digest=$(docker buildx imagetools inspect ghcr.io/xantiagoma/xtandard-image:0.1.0 \
  --format '{{range .Manifest.Manifests}}{{if eq .Platform.Architecture "arm64"}}{{.Digest}}{{end}}{{end}}')
cosign verify-attestation --type spdxjson \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/xantiagoma/xtandard-image/\.github/workflows/release\.yml@' \
  "ghcr.io/xantiagoma/xtandard-image@$digest" | jq -r '.payload' | base64 -d | jq '.predicate.packages | length'
```

SLSA provenance (index and per-arch manifests):

```sh
gh attestation verify oci://ghcr.io/xantiagoma/xtandard-image:0.1.0 --owner xantiagoma
```

Runtime contract, locally on your architecture:

```sh
docker pull ghcr.io/xantiagoma/xtandard-image:0.1.0
XI_EXPECT_VERSION=0.1.0 scripts/verify-app-image.sh ghcr.io/xantiagoma/xtandard-image:0.1.0
```

## Building and verifying locally

```sh
task release:build                               # base from versions.sh, pinned by digest (needs GHCR access)
XI_APP_BASE=xtandard-base:dev task release:build # locally built base (docker/base/README.md)
task release:verify                              # scripts/verify-app-image.sh xtandard-image:dev
```

Equivalent `docker build`:

```sh
docker build -t xtandard-image:dev \
  --build-arg BASE_BUILDER=xtandard-base:dev-builder \
  --build-arg BASE_RUNTIME=xtandard-base:dev \
  --build-arg VERSION=dev --build-arg COMMIT="$(git rev-parse HEAD)" \
  --build-arg SOURCE_DATE_EPOCH="$(git log -1 --pretty=%ct)" .
docker build --target debug --output type=local,dest=dist \
  --build-arg BASE_BUILDER=xtandard-base:dev-builder --build-arg BASE_RUNTIME=xtandard-base:dev .
```

`verify-app-image.sh` checks, in order: `version`; user `65532:65532`, ports, `HEALTHCHECK`, OCI labels and environment; compressed size (`docker save | gzip`, limit `XI_MAX_COMPRESSED_MB`, default 110); a run with `--read-only --tmpfs /tmp --cap-drop ALL` that must turn healthy, answer `health` with exit 0, reject writes to the rootfs and accept them in `/tmp`; and `scripts/smoke.sh`.

## Acceptance suite (M1 release gate)

`scripts/acceptance.sh` (`task acceptance`, WP-39) is a black-box end-to-end run of any app image on the host architecture: `XI_IMAGE` (default `xtandard-image:dev`; a registry reference is pulled). It gates every release (`release.yml` `build`, against the pushed digest, before `merge` tags anything) and runs in CI (`ci.yml` job `acceptance`, amd64 + arm64 on native runners, against the image the `smoke` job built; set the repository variable `XI_ACCEPTANCE_IMAGE`, e.g. `ghcr.io/xantiagoma/xtandard-image:slim`, to test a published image instead).

```sh
task release:build && task acceptance                                  # this checkout
XI_IMAGE=ghcr.io/xantiagoma/xtandard-image:0.1.0 scripts/acceptance.sh  # a published image
XI_ACC_RUN='TestSources|TestInfo' task acceptance                       # go test -run filter
```

It needs Docker (compose) and Go on the host (the suite is pure Go, `CGO_ENABLED=0`). Steps:

1. `scripts/check-readonly.sh` (shared with `verify-app-image.sh`): `docker run --read-only --tmpfs /tmp --cap-drop ALL`, healthy `HEALTHCHECK`, `health` exit codes, rootfs read-only.
2. `compose.acceptance.yml`: an nginx origin serving `testdata/fixtures` (`/slow/` throttled to 4 KiB/s), RustFS seeded with the fixtures (bucket `fixtures`, `test/acceptance/testdata/seed-s3.sh`), `app` configured through environment variables only (fresh signing, salt and encryption keys per run; `http`, `s3` and `fs` sources; a preset; fallback image; HEIC encode on; imgproxy at `/` with unsigned policy `allow`, the query dialect at `/q/` with `restricted`), and `app-tiny` (one worker, no queue). Both run read-only with all capabilities dropped.
3. `go test -tags acceptance ./test/acceptance/` against the published ports. Signatures, `enc/` tokens and image checks are re-implemented in the test from the documented contracts; decoded images are checked with the stdlib and `x/image` decoders plus header sniffs for AVIF/HEIC (`ftyp` + `ispe`), JPEG XL (size header), ICO and JPEG-in-TIFF.
4. A plain summary, one line per case with its time, on stdout, in `$XI_ACC_SUMMARY` when set, and in the Actions step summary. A skipped case counts as a failure.

| Test | Cases |
|---|---|
| `TestOutputFormats` | jpeg, png, webp, avif, heic, jxl, gif, tiff, bmp, ico: `Content-Type`, decoded format and 64×48 dimensions, no `Vary: Accept` |
| `TestSources` | http (plain and base64), s3 (`s3://` and `xi://assets/`), fs (`local:///` and `xi://local/`), `enc/` signed and unsigned, garbage `enc/` → 403, host/bucket not allowlisted and fs traversal → 403 |
| `TestSigningAndPolicy` | signed URL, signed preset, signed `/info`, tampered path and bad signature → 403, unsigned on the `allow` mount, `restricted` mount: allowlisted width/quality → 200, other width/quality, effects and a bogus `xi_sig` → 403 |
| `TestNegotiation` | `Accept` → avif / webp / jpeg with `Vary: Accept`; an explicit extension or `f:` wins without `Vary` |
| `TestHTTPSemantics` | 304 on `If-None-Match` (exact, weak, list, `*`), HEAD, 405, `raw` byte passthrough, fallback image on a source 404 (`XI_FALLBACK_STATUS`, `XI_FALLBACK_TTL`), `expires` in the past → 404 `no-store`, 400 `no-store`, JSON error body |
| `TestInfo` | `/info` JSON fields for jpeg (http), png with `cs`/`b`/`a`/`pn` (s3), heic (fs), animated gif; 304 on its ETag |
| `TestHealthAndMetrics` | `/healthz`, `/readyz`; admin `/metrics` has `xi_requests_total`, `xi_request_duration_seconds`, `xi_stage_duration_seconds`, `xi_source_fetch_duration_seconds`, `xi_queue_depth`, `xi_workers_busy`, `xi_vips_mem_*`, `xi_build_info` and the Go/process collectors; no `/metrics` on the public listener |
| `TestOverloadAndDrain` | 32 concurrent AVIF jobs on one worker → 200s and 429s with `Retry-After` and `no-store`, `xi_overload_total{reason="queue_full"}`; then SIGTERM (`docker kill`) during a ~5 s request: `/readyz` 503 while draining, the request completes with a valid image, the container exits 0 |

## Running read-only

The image writes nothing outside `/tmp` (fontconfig cache via `XDG_CACHE_HOME=/tmp/.cache`, the optional disk cache if configured there). Run it with a read-only rootfs and a small tmpfs:

```sh
docker run --read-only --tmpfs /tmp:rw,noexec,nosuid,size=64m --cap-drop ALL \
  --security-opt no-new-privileges -p 8080:8080 ghcr.io/xantiagoma/xtandard-image:slim
```

Compose: `read_only: true` and `tmpfs: [/tmp]`. Kubernetes: `readOnlyRootFilesystem: true` and an `emptyDir` (`medium: Memory` is fine) mounted at `/tmp` (`docs/CONFIG.md` §6).

## Refreshing the PGO profile

`cmd/xtandard-image/default.pgo` is a CPU profile of the server under a mixed resize/convert load over `testdata/fixtures` (every M1 decoder and encoder). `-pgo=auto` picks it up for the main package. The Go side is a minority of CPU time (libvips, libaom, libjxl and libwebp dominate), so the expected gain is small; confirm with benchstat before claiming one (go-standards §9).

Refresh it each release, or when the request path changes materially:

```sh
XI_APP_BASE=xtandard-base:dev task release:build   # or the GHCR base
task release:pgo                                   # scripts/pgo-profile.sh → cmd/xtandard-image/default.pgo
task release:build && task release:verify          # rebuild with the new profile
git add cmd/xtandard-image/default.pgo
```

`scripts/pgo-profile.sh` starts `compose.smoke.yml` with `XI_ADMIN_PPROF=true`, drives `PGO_WORKERS` (8) concurrent curl loops over the paths that return 200, and takes a `PGO_SECONDS` (30) CPU profile from the admin listener inside the container. Profiling a PGO-built binary is fine. Production profiles are better than the synthetic mix: take `/debug/pprof/profile?seconds=30` from the admin listener of several instances (`XI_ADMIN_PPROF=true`) and merge them with `go tool pprof -proto a.pprof b.pprof > cmd/xtandard-image/default.pgo`.

To build without the profile (bootstrapping from scratch, or measuring the gain), pass `--build-arg PGO=off`.

## Bumping the base image

`docker/base/versions.sh` is the single source of truth for the base pin (`VIPS_VERSION`, `XI_BASE_REVISION`, `XI_BASE_FLAVOR` → tag `<vips>-r<N>-<flavor>`).

1. Change `versions.sh` (and `./update-checksums.sh` for new tarballs), or bump `XI_BASE_REVISION` for a rebuild without a libvips change.
2. Update the two `ARG BASE_BUILDER=` / `ARG BASE_RUNTIME=` defaults in the root `Dockerfile` to the new tag; `scripts/base-image-ref.sh --check` (CI `smoke`, release `meta`) fails until they match.
3. Merge: the `base-image` workflow publishes `…-base:<tag>` and `…-base:<tag>-builder`.
4. CI `smoke` and the next release resolve the new tags to digests (`scripts/base-image-ref.sh --digest runtime|builder`) and build against them; the release records the runtime base in the `org.opencontainers.image.base.name` label.

The weekly `base-image` rebuild republishes the same tag with Ubuntu security updates; the next release picks up the new digest automatically. To ship those updates without code changes, tag a patch release.

## npm package

`@xtandard/image-url` (`packages/image-url`, [ADR 0009](adr/0009-typescript-monorepo.md) decision 6) is released from the same tag as the image and at the same version: tag `v0.4.0` publishes `@xtandard/image-url@0.4.0`. Its generated types describe that server version. The committed `package.json` keeps `"version": "0.0.0"`, and the version comes only from the tag.

The `npm` job of `.github/workflows/release.yml` runs after `merge`, so no package is published unless the image was:

1. It checks out the tag and runs `bun install --frozen-lockfile`. Then it runs the same checks as `task ts:check`: `bun run generate:check` (the generated types match the tagged `openapi.json`), `bun run typecheck`, `bun run test` (the shared signing vectors and the cross-language builder fixture included) and `bun run licenses`.
2. `npm pkg set version=<X.Y.Z>` sets the version, and `bun pm pack` packs the tarball. `prepack` builds `dist/` with `tsc` (JavaScript and `.d.ts`) and copies the root `LICENSE`. The `files` whitelist keeps the tarball to `dist/`, `README.md` and `LICENSE`.
3. `npm publish <tarball> --provenance --access public` publishes through npm trusted publishing. The GitHub Actions OIDC token (`id-token: write`) authenticates, so there is no npm token secret, and npm attaches a provenance attestation that links the tarball to the workflow run and commit. A stable release takes the `latest` dist-tag; a prerelease (`v0.4.0-rc.1`) takes `next`. `publishConfig` in `package.json` also sets `access: public` and `provenance: true`, so a manual `npm publish` from CI does the same.

**Not enabled yet.** The job only runs when the repository variable `XI_NPM_PUBLISH` is `enabled`. Before setting it, configure `@xtandard/image-url` on npmjs.com with a trusted publisher: repository `xantiagoma/xtandard-image`, workflow `release.yml`. The scope `@xtandard` belongs to the owner. The first publish of a new package name may need one manual `npm publish --access public` from a maintainer account before trusted publishing can be attached. The `ts` workflow (`.github/workflows/ts.yml`) is gated the same way, on `XI_TS_WORKFLOW=enabled`, and runs `task ts:check`'s steps on every change to `packages/**`, the lockfile, `openapi.json` or the shared fixtures.

Verify a published version:

```sh
npm view @xtandard/image-url@0.4.0 dist.attestations   # provenance present
npm audit signatures                                     # in a project that installed it
```

`bun pm pack --dry-run` in `packages/image-url` lists what would be published.
