# Release runbook

How the app image `ghcr.io/xantiagoma/xtandard-image` is built, published and verified, and how the
npm package `@xtandard/image` follows it ([npm package](#npm-package)).
Design context: `docs/ARCHITECTURE.md` §14, `docs/go-standards.md` §9, ADR 0005, ADR 0009.

**Status (2026-10-26):** two prereleases are out, `v0.1.0-rc.1` (2026-09-29) and `v0.1.0-rc.2` (2026-10-24), first built and pushed **locally** ([Local release (no Actions)](#local-release-no-actions)): three app flavours for both architectures, with the base images on GHCR. The repository went public on 2026-10-26 and GitHub Actions is enabled again, so `release.yml` re-runs `v0.1.0-rc.2` to publish signed, attested equivalents. Nothing is on npm or any other package registry yet. What changed in each version is in [CHANGELOG.md](../CHANGELOG.md).

## Versioning

xtandard-image follows [ZeroVer](https://0ver.org): the major version stays at `0`, and there is no planned `1.0`. The sibling xtandard projects use the same convention. It means that options can still change. It does not mean the software is unfinished.

**One version for the whole product.** The git tag `vX.Y.Z[-rc.N]` is the only source of the version. Every artefact released from that tag carries it, without the leading `v`:

- the three image flavours (`slim`, `full`, `gpl`);
- the `@xtandard/image` npm package;
- the other official clients, once their publish jobs exist ([ADR 0013](adr/0013-official-clients.md) §6).

A client at version `X.Y.Z` describes the server at version `X.Y.Z`. Its generated option types come from that version's catalogues and OpenAPI document. The committed manifests keep placeholder versions such as `0.0.0` or `0.1.0-SNAPSHOT`, and the release job sets the real version from the tag. Today `release.yml` publishes the images and (behind `XI_NPM_PUBLISH`) npm. The Go module follows its own tag, `clients/go/vX.Y.Z`. Swift uses the root `vX.Y.Z` tag directly. The other registries have no publish job yet.

**What a bump means** in `0.MINOR.PATCH`:

| Bump | Example | May contain |
|---|---|---|
| **MINOR** | `0.1.4` → `0.2.0` | Breaking changes: a configuration key or `XI_*` variable removed, renamed or with a new default; a URL option removed or with a new meaning; changes to response headers or status codes that clients see; client API changes. The [changelog](../CHANGELOG.md) lists each one under **Changed** or **Removed**, with what to do. |
| **PATCH** | `0.2.0` → `0.2.1` | Fixes, security fixes, new options and variables that keep the old behaviour by default, new formats and dialects, base image refreshes. Nothing that breaks a working configuration or URL. |
| **Prerelease** | `0.2.0-rc.1` | A release candidate of the next version, for testing. It can change before the final release. |

Pin images to an exact version (`:0.2.1-full`) or by digest in production, and read the changelog before you move to a new minor. For npm, `^0.2.1` takes the patches but not the next minor, which is how npm reads caret ranges below `1.0`.

**Where each version lands:**

| Release | GHCR tags | npm dist-tag | GitHub release |
|---|---|---|---|
| Stable `vX.Y.Z` | `X.Y.Z`, `X.Y.Z-slim`, `X.Y.Z-full`, `X.Y.Z-gpl`, and the moving tags `latest`, `slim`, `full`, `gpl` | `latest` | release |
| Prerelease `vX.Y.Z-rc.N` | `X.Y.Z-rc.N`, `X.Y.Z-rc.N-slim`, `X.Y.Z-rc.N-full`, `X.Y.Z-rc.N-gpl` only | `next` | prerelease |
| Edge: a push to `main` ([Edge images](#edge-images)) | the moving `main`, `main-slim`, `main-full`, `main-gpl`, and per commit `sha-<sha7>`, `sha-<sha7>-slim`, `sha-<sha7>-full`, `sha-<sha7>-gpl` | none | none |

`X.Y.Z` and `latest` without a suffix are the `slim` flavour, and so are `main` and `sha-<sha7>`. Edge images never move `latest`, `slim`, `full` or `gpl`: those always name the newest stable release. There are no `X.Y` (for example `0.1`) or `X` tags: `release.yml` does not create them. For "the newest patch of this minor", use the moving flavour tag or pin an exact version. The tag check in `release.yml` accepts any `v<major>.<minor>.<patch>[-pre]`. Keeping the major at `0` is a policy of this document, and the workflow does not enforce it.

## What gets published

A tag `vX.Y.Z` runs `.github/workflows/release.yml`. It builds three flavours of the app, each on the base image of the same flavour (ADR 0005, ADR 0006):

| Flavour | Base | Build args | Size limit (compressed) | `org.opencontainers.image.licenses` |
|---|---|---|---|---|
| `slim` | `…-base:<vips>-r<N>-slim` | `XI_FLAVOR=slim` | 110 MB | `MIT` |
| `full` | `…-base:<vips>-r<M>-full` | `XI_FLAVOR=full GO_TAGS=ffmpeg` | 170 MB | `MIT` |
| `gpl` | `…-base:<vips>-r<G>-gpl` | `XI_FLAVOR=gpl GO_TAGS=ffmpeg` | 170 MB | `GPL-2.0-or-later AND GPL-2.0-only AND AGPL-3.0-or-later AND MIT AND LicenseRef-Adobe-DNG-SDK AND (Apache-2.0 OR MIT)` |

`N`, `M` and `G` are `XI_BASE_REVISION` (`docker/base/versions.sh`), `XI_BASE_FULL_REVISION` (`versions-full.sh`) and `XI_BASE_GPL_REVISION` (`versions-gpl.sh`); `scripts/base-image-ref.sh --flavor <f> [--digest] runtime|builder` prints them.

| Job | Runs on | Does |
|---|---|---|
| `meta` | ubuntu-24.04 | Validates the tag (`v<major>.<minor>.<patch>[-pre]`; on a push to `main`, computes the [edge](#edge-images) version instead), reads the commit and `SOURCE_DATE_EPOCH`, checks `scripts/base-image-ref.sh --check`, and resolves the slim, full and gpl base images (runtime and builder) to `@sha256:` digests. |
| `build` (×6: 3 flavours × 2 architectures) | `ubuntu-24.04` (amd64), `ubuntu-24.04-arm` (arm64), native, no QEMU | Builds the `runtime` target of one flavour, pushes it **by digest** (no tag yet), pulls it back and runs `scripts/verify-app-image.sh` with `XI_EXPECT_FLAVOR` (version, image config, flavour labels, the flavour's size limit, read-only rootfs + `health`, smoke test) and the M1 acceptance suite `scripts/acceptance.sh` against the pushed digest (the release gate, below). Then it attests a syft SPDX SBOM with cosign and SLSA provenance with `actions/attest-build-provenance` on the per-arch manifest, and exports the unstripped binary (`debug` target, `xtandard-image-<ver>-<flavour>-linux-<arch>.debug`). |
| `merge` (×3, one per flavour) | ubuntu-24.04 | Waits for all six builds. `docker buildx imagetools create` the flavour's multi-arch index with its tags (below), signs it keyless with cosign (`--recursive`, so the per-arch manifests are signed too) and attests SLSA provenance on the index. |
| `release` | ubuntu-24.04 | Tag pushes only, after the three merges: creates the GitHub release with the six unstripped binaries, the six SPDX SBOMs, `SHA256SUMS`, and the three index digests in the notes. |

Tags (`X.Y.Z` has no leading `v`). The moving tags (`latest`, `slim`, `full`, `gpl`) are only for stable releases; `X.Y.Z` and `latest` are the slim flavour:

| Tag | Flavour | Stable release | Prerelease (`vX.Y.Z-rc.1`) |
|---|---|---|---|
| `X.Y.Z-slim` | slim | yes | yes |
| `X.Y.Z` | slim | yes | yes |
| `slim` | slim | yes | no |
| `latest` | slim | yes | no |
| `X.Y.Z-full` | full | yes | yes |
| `full` | full | yes | no |
| `X.Y.Z-gpl` | gpl | yes | yes |
| `gpl` | gpl | yes | no |

Build properties (enforced in the `Dockerfile`):

- `CGO_ENABLED=1 go build -trimpath -buildvcs=false -pgo=auto -ldflags "-s -w -X main.version -X main.commit -X main.date"`; the build fails if `go version -m` does not report `-pgo=default.pgo`.
- `GOAMD64=v2` on amd64; arm64 keeps `GOARM64=v8.0`.
- Reproducible: pinned toolchain (in the base builder), base images by digest, `go.sum`, `main.date` and layer timestamps from `SOURCE_DATE_EPOCH` (`rewrite-timestamp=true`).
- Runtime: the base `runtime` image plus the binary only; UID 65532; `HEALTHCHECK` via `xtandard-image health`; `EXPOSE 8080 9090`; ARCHITECTURE §14 `ENV`; OCI labels `source`, `version`, `revision`, `created`, `base.name`, and per flavour (build arg `XI_FLAVOR`, stages `runtime-slim`/`runtime-full`/`runtime-gpl`) `licenses` (table above), a `description` naming the flavour and `dev.xtandard.flavor`. The base's own `dev.xtandard.base.flavor` label is inherited, and `verify-app-image.sh` fails when the two differ.
- `XI_FLAVOR=full` or `gpl` without `GO_TAGS=ffmpeg` fails the build (the video decoders would silently be missing).

## Edge images

Every push to `main` runs the same `release.yml` in edge mode (WP-155), so you can follow the development head. The quality bar is the same as for a release: `meta`, the six native `build` legs (`verify-app-image.sh` and the acceptance suite against the pushed digest), and `merge` with keyless cosign signing, SLSA provenance and the SBOM attestation. Only the version and the tags differ, and the `release` and `npm` jobs are skipped. Pushes that only touch Markdown, `docs/`, `clients/`, `packages/`, `bench/` or the JS workspace files (`paths-ignore` in the workflow, none of them in the image) build nothing. A newer push cancels the edge run still in progress; tag releases are never cancelled.

| Tag | Flavour | Moves |
|---|---|---|
| `main`, `main-slim` | slim | every push to `main` |
| `main-full` | full | every push to `main` |
| `main-gpl` | gpl | every push to `main` |
| `sha-<sha7>`, `sha-<sha7>-slim` | slim | never (one per commit) |
| `sha-<sha7>-full` | full | never |
| `sha-<sha7>-gpl` | gpl | never |

`<sha7>` is the first 7 characters of the commit hash (`git rev-parse HEAD | cut -c1-7`). The version baked in (`xtandard-image version`, the `org.opencontainers.image.version` label) is a semver prerelease that sorts after the newest release tag on `main` and before the next release: `0.1.0-rc.2.main.45.g7083040` after `v0.1.0-rc.2` (45 commits since that tag), or `0.1.1-main.3.gabc1234` after a stable `v0.1.0`.

Edge images are for testing and for deploys that track `main`. They carry no compatibility promise and no changelog entry. To deploy one reproducibly, pin the per-commit tag or, better, its digest; never deploy the moving `main` tag where you need to know what runs:

```sh
docker buildx imagetools inspect ghcr.io/xantiagoma/xtandard-image:sha-7083040-full   # prints the index digest
docker pull ghcr.io/xantiagoma/xtandard-image@sha256:<digest>
```

Verify an edge image like a release ([Verifying a published image](#verifying-a-published-image)), but pin the signer to `main`:

```sh
cosign verify ghcr.io/xantiagoma/xtandard-image:sha-7083040 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity 'https://github.com/xantiagoma/xtandard-image/.github/workflows/release.yml@refs/heads/main'
```

The release identity regexp below (`release\.yml@`) also accepts edge signatures. To accept only release builds, use `--certificate-identity-regexp '^https://github\.com/xantiagoma/xtandard-image/\.github/workflows/release\.yml@refs/tags/v'`.

**Retention.** Each edge run adds six per-arch manifests, three indexes, and their signatures and attestations to the package, and the `sha-*` tags stay forever. Nothing deletes them yet. Backlog: a scheduled workflow with `actions/delete-package-versions` that keeps the newest N `sha-*` versions and deletes untagged versions older than a few weeks. It must never delete a version that carries a release tag (`X.Y.Z*`, `latest`, `slim`, `full`, `gpl`), nor the signature and attestation artefacts (`sha256-<digest>.sig`/`.att` tags, referrers) of a version it keeps, and the per-arch manifests of a kept index are untagged too, so "untagged" alone is not a safe filter. The unstripped edge binaries (`debug-*` workflow artefacts) are kept 14 days instead of 90.

## Cutting a release

1. Make sure `main` is green (CI `smoke` builds and verifies the image and `acceptance` runs the M1 suite against it, on both architectures) and that the slim, full and gpl base images for the current `docker/base/versions*.sh` pins are published (`base-image` workflow on `main`).
2. Refresh the PGO profile if the hot path changed since the last release (below) and merge it. Pick the version ([Versioning](#versioning)): a minor bump if anything breaks a configuration, a URL or a client API, and a patch otherwise. Then move the `Unreleased` section of [CHANGELOG.md](../CHANGELOG.md) under the new version with today's date, and merge it.
3. Check the user-facing docs: the quick start in the README and `docs/GETTING-STARTED.md` must pass (CI `quickstart` runs both; locally `task quickstart`). Point every pinned image reference at the version you are about to tag (`grep -rn 'xtandard-image:0\.' README.md docs deploy clients`: the README and docs commands, the deploy examples, the Modal `VERSION`, the Kustomize `newTag`s and the chart `appVersion`) (`scripts/quickstart.sh` swaps any `ghcr.io/xantiagoma/xtandard-image:<tag>` there for the local build). For a minor or major release, rerun the benchmark against the release candidate (`task release:build && bench/run.sh`, [BENCHMARKS.md](BENCHMARKS.md)) and update `bench/RESULTS.md` when the numbers moved.
4. Tag and push:

   ```sh
   git switch main && git pull --ff-only
   git tag -a v0.1.0 -m "v0.1.0"
   git push origin v0.1.0
   ```

5. Watch `release` in Actions. Nothing is tagged until all six builds (three flavours, two architectures) have passed `verify-app-image.sh` and `acceptance.sh`; a failure leaves only untagged digests behind (GHCR retention cleans them up).
6. Verify the published image (below) and edit the generated release notes if needed. After the first stable release, `docs/GETTING-STARTED.md` can lead with the published image instead of a local build: its `sh quickstart-build` block stays the CI build, and the prose names the moving `slim` tag.

7. Check the published schema and API document (below, "Documentation site and schemas"): the last `pages` run on `main` must be green, and `curl -fsS https://xantiagoma.github.io/xtandard-image/schema/v1/config.schema.json | cmp - <(docker run --rm ghcr.io/xantiagoma/xtandard-image:X.Y.Z config schema)` must print nothing. If it differs, run the `pages` workflow from the tag.

**Dry run**: Actions → `release` → *Run workflow* with `dry_run` checked (the default) builds and verifies the three flavours on both architectures from the selected branch without pushing. With `dry_run` unchecked, `version` must be an existing tag; it is checked out and published like a tag push, but no GitHub release is created.

**Re-running** a release for the same tag rebuilds and re-tags; the image content is identical (reproducible build), but the digest can differ if the base image digest moved in between.

## Local release (no Actions)

Used for `v0.1.0-rc.1` (2026-09-29) while GitHub Actions was unavailable (billing). It is the same procedure as `base-image.yml` and `release.yml`, run by hand from a macOS arm64 host (8 cores, OrbStack with a 7.8 GiB VM). linux/arm64 builds natively and linux/amd64 builds under Rosetta emulation. Docker was logged in to `ghcr.io` with a token that has `write:packages`.

1. **Clean tree:** `git worktree add ../xi-release origin/main` (commit `8c9d16b`), then `mise trust`. Every build used `SOURCE_DATE_EPOCH=$(git log -1 --pretty=%ct)`.
2. **Builder:** `docker buildx create --name xi-release --driver docker-container --bootstrap`, passed explicitly with `--builder xi-release` so the default builder was left alone. It was removed at the end, and its cache went with it.
3. **Licence gate:** `docker/base/test-check-licenses.sh` passed. shellcheck 0.11 (the `koalaman/shellcheck:stable` container) reports one SC2046 warning in `build-gpl-metafile.sh` and one SC2016 info in `build-libraw.sh` that the older CI shellcheck may not report. These are lint findings only.
4. **Bases**, one platform at a time (arm64, then amd64). For each flavour and each target (`runtime`/`builder`, `runtime-full`/`builder-full`, `runtime-gpl`/`builder-gpl`):
   `docker buildx build --platform linux/<arch> --target <t> --build-arg VIPS_VERSION=8.18.6 --label org.opencontainers.image.version=<tag>[-builder] --provenance=false --sbom=false --output type=image,name=ghcr.io/xantiagoma/xtandard-image-base,push-by-digest=true,name-canonical=true,push=true --metadata-file <f> docker/base`.
   Then `docker/base/verify-image.sh --flavor <f> <runtime@digest> <builder@digest>` ran on each pulled per-arch pair (amd64 under emulation), and `docker buildx imagetools create` tagged `<vips>-r<N>-<flavour>[-builder]` and `<flavour>[-builder]`. Build times, arm64 native: slim 12 min, full 3.5 min and gpl 4 min on top of it. amd64 emulated: slim runtime 2 h 55 min, full 6 min, gpl 12 min. Build cache peaked at 13.2 GB.
5. **App**, per flavour and platform: the `runtime` target on the new bases, resolved to index digests. The build args were `BASE_BUILDER`/`BASE_RUNTIME` (`<base>:<tag>[-builder]@sha256:…`), `VERSION=0.1.0-rc.1`, `COMMIT` and `GO_TAGS` (`ffmpeg` for full and gpl), with the label `org.opencontainers.image.created`, `--provenance=false --sbom=false` and `push-by-digest=true,rewrite-timestamp=true`. The `debug` target was exported to `xtandard-image-<ver>-<flavour>-linux-<arch>.debug`. PGO came from the committed `default.pgo`.
6. **Gate:** `scripts/verify-app-image.sh <image@digest>` ran with `XI_EXPECT_VERSION=0.1.0-rc.1`, `XI_MAX_COMPRESSED_MB` 110 for slim and 170 for full/gpl (ARCHITECTURE §14), and `XI_IMAGE=<image@digest> scripts/acceptance.sh`. Results: arm64 133/133 for all three flavours; amd64 133/133 for slim and full. amd64 gpl got 132/133: `TestEPS` fails, and some runs lose `app-tiny` at startup, because Rosetta cannot map memory under the sandbox's `RLIMIT_AS` for `gs` and `xi-metafile2svg` (`rosetta error: mmap_anonymous_rw mmap failed`). This is an emulation artefact. Native amd64 (CI) is the real gate for that image. WP-118 fixed it: the sandbox detects Rosetta/QEMU and drops only `RLIMIT_AS` there (`XI_SANDBOX_MAX_AS`, `XI_SANDBOX_DETECT_EMULATION`, ADR 0011 amendment), and `XI_FORMATS_METAFILE_ENABLED=auto` no longer stops startup when the converter's self-check fails.
7. **Tags:** `docker buildx imagetools create` created `0.1.0-rc.1` and `0.1.0-rc.1-slim` (slim), `0.1.0-rc.1-full` and `0.1.0-rc.1-gpl`. No moving tags (`latest`, `slim`, `full`, `gpl`) were created for the prerelease. Pass `--annotation "index:org.opencontainers.image.source=https://github.com/xantiagoma/xtandard-image"` to `imagetools create`: GHCR links a package pushed from outside Actions to the repository only through that index annotation (the per-arch config label is not enough). rc.1 and rc.2 were created without it, so the package was linked by hand (Package settings → Connect repository, 2026-10-24).
8. **SBOMs:** `anchore/syft` (1.52.0) ran as a container against the local daemon (`-v /var/run/docker.sock:/var/run/docker.sock … docker:<image@digest> -o spdx-json=… -o cyclonedx-json=…`) for each per-arch app image, and for the 12 base images, which went into a tarball. They were attached to the GitHub release, not attested.
9. **No signing or provenance:** cosign keyless signing and `attest-build-provenance` need the workflow's GitHub OIDC identity. The release notes say so, and the CI release re-publishes signed images when Actions is back.
10. **Release:** `git tag -a v0.1.0-rc.1` on `8c9d16b`, then `git push origin v0.1.0-rc.1`. The push started a `release` run, which failed (billing, expected). Then `gh release create v0.1.0-rc.1 --prerelease --verify-tag --notes-file …` with the debug binaries, the SBOMs and `SHA256SUMS`.

Total wall time was about 4 h 10 min, most of it the emulated amd64 slim base. To do it again, copy these steps. Building each platform separately and joining the per-arch digests with `imagetools create` is more robust than one two-platform invocation, and a failed step can be retried without redoing the other platform.

**`v0.1.0-rc.2` (2026-10-24)** repeated steps 1, 2 and 5–10 on commit `e6ed827d` without rebuilding the bases: slim r7, full r9 and gpl r10 were already on GHCR, built, verified and attested by `base-image.yml` on 2026-10-23, and the app builds used their index digests (`scripts/base-image-ref.sh --digest`). Acceptance: slim 143/143, full 155/155 and gpl 155/155 on arm64 and on amd64 under Rosetta (WP-118 fixed the gpl sandbox failure under emulation). Total wall time was about 25 minutes. Actions was disabled on the repository the same day (`enabled: false` in the repository's Actions permissions) after billing blocked it again.

## Documentation site and schemas (GitHub Pages)

`.github/workflows/pages.yml` publishes the documentation website, the config JSON Schema and the OpenAPI document to `https://me.xantiagoma.com/xtandard-image/` (the custom domain of `xantiagoma.github.io`, which redirects there; WP-156, [ADR 0015](adr/0015-documentation-site.md); WP-81b, ADR 0007 decision 6). It deploys on every push to `main` that changes `docs/**`, a root `*.md`, `clients/README.md` or `clients/*/README.md`, `bench/RESULTS.md`, `site/**`, `internal/config/config.schema.json`, `internal/apidoc/openapi.json`, `scripts/pages-build.sh`, `scripts/check-ts-licenses.ts` or the workflow, and on demand (*Run workflow*). Pull requests that touch the same paths build and check the site without deploying.

| Path | Content |
|---|---|
| `/` | The home page; the docs under `/getting-started/`, `/guides/…`, `/deploy/`, `/operations/`, `/clients/…`, `/reference/…` and `/project/…` |
| `/api/` | Scalar API reference over `schema/latest/openapi.json` (same pinned Scalar build and SRI as admin `/docs`) |
| `/reference/schemas/` | The schema files and the editor modelines (formerly the site's index page) |
| `/schema/v1/config.schema.json` | The config schema; this URL is its `$id` |
| `/schema/v1/openapi.json` | The OpenAPI document |
| `/schema/latest/…` | The same two files for the newest schema major |
| `/assets/…` | Every file of `docs/assets` (favicons, lockups, `social.png`, screenshots) |

The site is Astro Starlight in `site/`, a separate Bun project with its own lockfile. The Markdown in the repository stays the source: `site/scripts/sync.ts` copies the files that `site/manifest.ts` lists (the sidebar) into the site, takes each title from its H1, turns relative links into site routes or GitHub URLs, and fails on a link to a missing file. `scripts/pages-build.sh` still assembles the schema files, the Scalar page and the assets (into `dist/pages-static/`), which the site copies byte for byte. The build fails on a broken link or anchor (`starlight-links-validator`, then `site/scripts/check-dist.ts` over every built HTML file) or when a static file differs from what `pages-build.sh` produced.

`v1` is the schema major version read from the `$id`, not the release version, so the URL stays stable across releases. The `$id` keeps the `xantiagoma.github.io` host, which GitHub redirects to the custom domain. Bump the major (in `config.SchemaID`, `TestSchemaID` and `docs/schemastore/catalog-entry.json`) only when a config key is removed or renamed; `scripts/pages-build.sh` then needs to keep publishing the last `v1` files next to `v2` (check them out from the last `v1` tag), because a published `$id` is forever.

Jobs: `build` installs `site/bun.lock`, runs the licence check (`bun run licenses`) and `bun run build` with the origin and base path that `actions/configure-pages` reports, and uploads `dist/pages` with `actions/upload-pages-artifact`; `deploy` publishes it with `actions/deploy-pages` to the `github-pages` environment; `verify` fetches every published JSON file and the Scalar page, compares them byte for byte with the committed ones, and validates the SchemaStore samples (`docs/schemastore/tests`) with Ajv against the schema URL their modeline names.

Before the first run the owner must, once: set Settings → Pages → Source to *GitHub Actions* and enable the `pages` workflow. Then open the SchemaStore PR ([docs/schemastore/README.md](schemastore/README.md)).

Locally, `task pages:build` builds the same site into `dist/pages/` (the output depends only on the committed files), and `task pages:dev` serves a live preview at `http://localhost:4321/xtandard-image/`; rerun it after editing a doc, because the pages are synced copies. To check a build, serve `dist/pages` under the base path, for example by copying it to `<dir>/xtandard-image/` and running `python3 -m http.server -d <dir>`.

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

# full and gpl: the flavour (XI_FLAVOR, labels) and GO_TAGS=ffmpeg follow the base name
XI_APP_BASE=xtandard-base:dev-gpl XI_IMAGE=xtandard-image:gpl task release:build
XI_EXPECT_FLAVOR=gpl scripts/verify-app-image.sh xtandard-image:gpl
XI_IMAGE=xtandard-image:gpl task acceptance
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
# gpl (full likewise): --build-arg XI_FLAVOR=gpl --build-arg GO_TAGS=ffmpeg on the gpl base
```

`verify-app-image.sh` checks, in order: `version`; user `65532:65532`, ports, `HEALTHCHECK`, OCI labels (per flavour: `dev.xtandard.flavor` equals the base's `dev.xtandard.base.flavor` and `XI_EXPECT_FLAVOR` when set, `licenses` as in the flavour table, a `description` naming the flavour) and environment; compressed size (`docker save | gzip`, limit `XI_MAX_COMPRESSED_MB`, default 110 for slim and 170 for full and gpl); a run with `--read-only --tmpfs /tmp --cap-drop ALL` that must turn healthy, answer `health` with exit 0, reject writes to the rootfs and accept them in `/tmp`; and `scripts/smoke.sh`.

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

**Video transformations** (`XI_VIDEO_TRANSFORM_ENABLED`, full and gpl images) also write their spool files (WP-121b): video sources and outputs larger than `XI_VIDEO_TRANSFORM_SPOOL_THRESHOLD` (32 MiB) live in `XI_VIDEO_TRANSFORM_SPOOL_DIR` (default `/tmp/xtandard-video`) instead of memory. A 64 MiB tmpfs is too small for them, and a tmpfs (or `emptyDir` with `medium: Memory`) is RAM, which is what the spool saves: mount a disk-backed volume there instead (Compose: a named volume; Kubernetes: an `emptyDir` without `medium`, with a `sizeLimit`, or an ephemeral volume) and set `XI_VIDEO_TRANSFORM_SPOOL_MAX_BYTES` below its size, so a full volume is a 503 with `Retry-After` rather than a failed write:

```sh
docker run --read-only --tmpfs /tmp:rw,noexec,nosuid,size=64m -v xi-spool:/spool \
  -e XI_VIDEO_TRANSFORM_ENABLED=true -e XI_VIDEO_TRANSFORM_SPOOL_DIR=/spool \
  -e XI_VIDEO_TRANSFORM_SPOOL_MAX_BYTES=8GiB --cap-drop ALL ghcr.io/xantiagoma/xtandard-image:full
```

The directory must be writable by the image's user (65532; Kubernetes `fsGroup` does it for an `emptyDir`): an instance that serves video transformations fails to start otherwise. Its files are unlinked as soon as they are opened, so a crash leaves nothing behind; at start the server still removes `xi-spool-*` files an earlier process might have left. With `XI_VIDEO_TRANSFORM_SPOOL_THRESHOLD=0` everything stays in memory and nothing is written there.

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

`@xtandard/image` (`clients/typescript`, [ADR 0009](adr/0009-typescript-monorepo.md) decision 6) is released from the same tag as the image and at the same version: tag `v0.4.0` publishes `@xtandard/image@0.4.0`. Its generated types describe that server version. The committed `package.json` keeps `"version": "0.0.0"`, and the version comes only from the tag.

The `npm` job of `.github/workflows/release.yml` runs after `merge`, so no package is published unless the image was:

1. It checks out the tag and runs `bun install --frozen-lockfile`. Then it runs the same checks as `task ts:check`: `bun run generate:check` (the generated types match the tagged `openapi.json`), `bun run typecheck`, `bun run test` (the shared signing vectors and the cross-language builder fixture included) and `bun run licenses`.
2. `npm pkg set version=<X.Y.Z>` sets the version, and `bun pm pack` packs the tarball. `prepack` builds `dist/` with `tsc` (JavaScript and `.d.ts`) and copies the root `LICENSE`. The `files` whitelist keeps the tarball to `dist/`, `README.md` and `LICENSE`.
3. `npm publish <tarball> --provenance --access public` publishes through npm trusted publishing. The GitHub Actions OIDC token (`id-token: write`) authenticates, so there is no npm token secret, and npm attaches a provenance attestation that links the tarball to the workflow run and commit. A stable release takes the `latest` dist-tag; a prerelease (`v0.4.0-rc.1`) takes `next`. `publishConfig` in `package.json` also sets `access: public` and `provenance: true`, so a manual `npm publish` from CI does the same.

**Enabled (2026-10-26).** The job runs when the repository variable `XI_NPM_PUBLISH` is `enabled`, which it is. `@xtandard/image` has a trusted publisher on npmjs.com: repository `xantiagoma/xtandard-image`, workflow `release.yml`. Renaming the workflow file breaks publishing. `0.1.0` was published by hand with `npm publish --access public`, without provenance, because npm attaches a trusted publisher only to an existing package. Every later version comes from this job, through OIDC and with provenance. A version already on npm is skipped, so re-running a release is safe. The `ts` workflow (`.github/workflows/ts.yml`) is gated the same way, on `XI_TS_WORKFLOW=enabled`, and runs `task ts:check`'s steps on every change to the TypeScript client, the lockfile, `openapi.json` or the shared fixtures.

Verify a published version:

```sh
npm view @xtandard/image@0.4.0 dist.attestations   # provenance present
npm audit signatures                                     # in a project that installed it
```

`bun pm pack --dry-run` in `clients/typescript` lists what would be published.
