# Release runbook

How the app image `ghcr.io/xantiagoma/xtandard-image` is built, published and verified.
Design context: `docs/ARCHITECTURE.md` §14, `docs/go-standards.md` §9, ADR 0005.

## What gets published

A tag `vX.Y.Z` runs `.github/workflows/release.yml`:

| Job | Runs on | Does |
|---|---|---|
| `meta` | ubuntu-24.04 | Validates the tag (`v<major>.<minor>.<patch>[-pre]`), reads the commit and `SOURCE_DATE_EPOCH`, checks `scripts/base-image-ref.sh --check`, and resolves the base images from `docker/base/versions.sh` to `@sha256:` digests. |
| `build` (×2) | `ubuntu-24.04` (amd64), `ubuntu-24.04-arm` (arm64), native, no QEMU | Builds the `runtime` target, pushes it **by digest** (no tag yet), pulls it back and runs `scripts/verify-app-image.sh` (version, image config, ≤ 110 MB compressed, read-only rootfs + `health`, smoke test). Then it attests a syft SPDX SBOM with cosign and SLSA provenance with `actions/attest-build-provenance` on the per-arch manifest, and exports the unstripped binary (`debug` target). |
| `merge` | ubuntu-24.04 | `docker buildx imagetools create` the multi-arch index, signs it keyless with cosign (`--recursive`, so the per-arch manifests are signed too), attests SLSA provenance on the index, and creates the GitHub release with the unstripped binaries and `SHA256SUMS`. |

Tags (`X.Y.Z` has no leading `v`):

| Tag | Stable release | Prerelease (`vX.Y.Z-rc.1`) |
|---|---|---|
| `X.Y.Z-slim` | yes | yes |
| `X.Y.Z` | yes | yes |
| `slim` | yes | no |
| `latest` | yes | no |

`slim` is the default flavor until `full` exists (M3); then `X.Y.Z`/`latest` keep pointing at slim and `full` gets `X.Y.Z-full`/`full`.

Build properties (enforced in the `Dockerfile`):

- `CGO_ENABLED=1 go build -trimpath -buildvcs=false -pgo=auto -ldflags "-s -w -X main.version -X main.commit -X main.date"`; the build fails if `go version -m` does not report `-pgo=default.pgo`.
- `GOAMD64=v2` on amd64; arm64 keeps `GOARM64=v8.0`.
- Reproducible: pinned toolchain (in the base builder), base images by digest, `go.sum`, `main.date` and layer timestamps from `SOURCE_DATE_EPOCH` (`rewrite-timestamp=true`).
- Runtime: the base `runtime` image plus the binary only; UID 65532; `HEALTHCHECK` via `xtandard-image health`; `EXPOSE 8080 9090`; ARCHITECTURE §14 `ENV`; OCI labels `source`, `version`, `revision`, `created`, `licenses=MIT`, `description`, `base.name`.

## Cutting a release

1. Make sure `main` is green (CI `smoke` builds and verifies the image on both architectures) and that the base image for the current `docker/base/versions.sh` pin is published (`base-image` workflow on `main`).
2. Refresh the PGO profile if the hot path changed since the last release (below) and merge it.
3. Tag and push:

   ```sh
   git switch main && git pull --ff-only
   git tag -a v0.1.0 -m "v0.1.0"
   git push origin v0.1.0
   ```

4. Watch `release` in Actions. Nothing is tagged until both architectures have passed `verify-app-image.sh`; a failure leaves only untagged digests behind (GHCR retention cleans them up).
5. Verify the published image (below) and edit the generated release notes if needed.

**Dry run**: Actions → `release` → *Run workflow* with `dry_run` checked (the default) builds and verifies both architectures from the selected branch without pushing. With `dry_run` unchecked, `version` must be an existing tag; it is checked out and published like a tag push, but no GitHub release is created.

**Re-running** a release for the same tag rebuilds and re-tags; the image content is identical (reproducible build), but the digest can differ if the base image digest moved in between.

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
