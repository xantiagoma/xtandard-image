# Orchestration handoff

Read this first when resuming the build with a fresh context. It says where the project stands,
how work is delivered, and which helper scripts exist. The roadmap with per-package status is
`docs/ROADMAP.md`; the delivery protocol for coding agents is `docs/AGENT-WORKFLOW.md`.

## State as of 2026-09-28

- **M1 and M2 are complete on `main`.** M2 landed as one merge commit (#79) of the 27-branch stack
  (per-package PRs #50–#78 closed with a reference; their descriptions document each package). Final
  verification of the merged tree in the container: 59 packages green.
- **M3 merged into `main`** via `gh stack merge` (merge commit 893b191, PRs #80–#92); 0 open PRs.
  Post-merge verification of the merged tree (`m3/wp-90-post-merge`): `docker:lint` green on the slim and full builders (one fix:
  the unused `infoLater` in `internal/dialect/imgproxy`), `docker:test` 65/65 packages on the slim,
  full (`-tags ffmpeg`) and gpl builders, `ts:check` 546 tests, `pages:build`, and acceptance 90/90
  against a release image built from the full base.
  Local base tags: `xtandard-base:dev[-builder]`, `dev-full[-builder]`, `dev-gpl[-builder]`.
- **Integration verified:** the stack top through #86 passed lint, slim+full test suites, ts:check,
  pages:build and acceptance (63/63) in the container; fixes landed on `m3/wp-87-stabilise`. Then
  #87 and #88 were rebased on it; the whole chain is synced on main (`gh stack view` shows no
  needsRebase). Lesson: an agent that pushes to a stack branch must `git fetch` right before
  checking it out, otherwise it force-pushes a tip based on a stale chain (happened once; repaired
  with `git rebase --onto <parent tip> <branch>~<own commits> <branch>` per branch).
- **Merging:** the session's permission classifier denied `gh stack merge` once; the owner then
  granted merge permission explicitly in chat and the orchestrator ran `gh stack merge 92 --yes
  --merge` (893b191) and `gh pr merge` for later PRs.
- **M3 is complete on `main`** (PR #93 merged, 4e8dc70; 0 open PRs). Every milestone package is
  merged and verified in the container (slim/full/gpl: 65 packages; acceptance 90/90).
- **Next: first prerelease tag.** `docs/RELEASE.md` needs the `release` and `base-image` workflows
  (multi-arch GHCR push, cosign, SBOM; the r3 `slim` and r4 `full`/`gpl` base tags (WP-93) are not on GHCR yet). Both are
  disabled by the owner and the Actions minutes were exhausted until 2026-10-01. Ask the owner to
  re-enable `base-image` (push to main publishes r3 slim, r4 full/gpl), then `release`, then tag `v0.1.0-rc.1`.
  A local-only tag without images on GHCR is not useful for a Docker-target project.
- **M4:** WP-94 playground v2 is **merged (PR #95, owner-run merge; the session classifier denies merges without review, so ask
  the owner to run `gh pr merge`)**. The demo container `xtandard-play` on the tailnet (http://100.102.217.2:8080/_playground/)
  runs the v2 build `xtandard-image:wp94-gpl` with the configuration in `compose.playground.yml`
  (every dialect + all 20 simple providers mounted, sources bound to `web`, unsigned policy `allow`).
  Known demo limits: ImageKit/Bunny/AWS SIH translation cards are empty for absolute URLs (their
  grammars need relative keys on a source with `base_url`); the playground lists a mount once per
  native prefix (`simple:*` duplicates) — dedupe in the UI later.
- **M4 in flight (2026-09-28):** WP-95 (named http sources take absolute keys; `xi://web/https://…`
  works; clearer 403s) is **merged (#96; owner granted merge permission in chat, `gh pr merge` worked)**; the demo runs it (`xtandard-image:wp95-gpl` via `compose.playground.yml`). WP-96 (playground polish: aspect-correct comparison,
  mount dedupe (a real `api/meta` bug), translate reasons, sortable matrix; also fixed video results
  not playing (CSP `media-src blob:`) and translate 500 on `simple:*`) is **merged (#97)**. The demo
  runs `xtandard-image:main-gpl` built from main a394eca. `scripts/playground-check.ts` drives the UI
  in headless Chromium (41 checks).
- **M4 backlog done (2026-09-27):** WP-91/92 (detection on video frames, detections mapped through
  chains) merged as #99, WP-93 (MALLOC_CONF in the base, slim r3 / full+gpl r4, local only until the
  `base-image` workflow runs) merged as #98. WP-97 long-tail inputs (JPEG XR via fuzz-hardened jxrlib, PNM P1–P7, TGA/PCX/DDS/QOI/SGI/
  Sun/XBM/XPM via ImageMagick, WBMP/ICNS in Go; EPS declined: AGPL Ghostscript) merged as #100;
  bases now slim r4 / full r5 / gpl r5 (local `xtandard-base:dev*` tags retagged from the WP-97
  build). WP-98 (animation pixel limits follow imgproxy's
  CheckDimensions; new `XI_LIMITS_MAX_ANIMATION_PIXELS` 200 MP; 422 bodies name the limit) merged as
  #101. WP-99 (EPS/PS/AI via out-of-process sandboxed Ghostscript 10.08,
  gpl only, off by default; gpl base r6; ADR 0011) merged as #102 with the owner's explicit
  authorisation after the classifier had blocked the first launch. Demo runs `xtandard-image:main-gpl`
  from main with `XI_FORMATS_EPS_ENABLED=true` (in `compose.playground.yml`); the network namespace
  falls back under Docker's seccomp (logged). Local `xtandard-base:dev-gpl*` retagged from wp99.
- **WP-100 (format gap research, 290-sample variant gallery) merged as #103**; proposed WP-101…108 in
  ROADMAP. DOC-1 merged (#104: SOURCES.md, config methods, OpenAPI discovery,
  examples loaded by tests; sources resolve in **alphabetical** order; follow-ups: imgproxy compat drops
  the scheme from allowed-source patterns, `config validate` misses Azure account / sftp host keys).
  WP-101/102 merged (#105): Nikon HE / unsupported RAW → embedded preview, lazy-decode 500→422,
  RAW-aware pixel limit (`XI_LIMITS_MAX_RAW_DEMOSAIC_PIXELS` 120 MP), libtiff zstd; bases slim r5 /
  full r6 / gpl r7 (local `dev*` tags retagged from wp101). Host `go build` no longer works (libjxr
  only in the base): verify in the container. WP-109 (playground Info panel, #106) and WP-107 (designer containers, #107; needs the next base
  revision for the ImageMagick XCF policy) merged. Pending base integration: one revision bump combining
  WP-107 policy + WP-103/105/110 base changes, then retag `dev*` and redeploy the demo. In flight (batch 1): WP-103 (LibRaw snapshot, X3F, GPR, DNG SDK),
  WP-105 (Lab TIFF, CMYK JXL, pure-Go DDS BCn), WP-110 (`classify` with a CPU ImageNet ONNX model in full/gpl, only when requested; CLIP and a
  CUDA image are future ROADMAP rows). Batch 2
  after these: WP-104 (AVIF sequences, gain maps), WP-106 (12/16-bit JPEG), WP-108 (FITS/DICOM/KTX2…).
  Rule for parallel base work: agents build under `xtandard-base:wpNNN*`, never bump revisions or
  retag `dev*`; the orchestrator bumps revisions once when integrating. Leaf MOS sniffing still open.
  Commit trailer is now `Co-Authored-By: Claude Opus 5.5`.
- **State 2026-09-28 (night):** merged WP-109 (#106), WP-107 (#107), WP-105 (#108), race fix in
  `internal/server` inspectSource (#109), WP-111 playground tools/labels/loading/selectors (#110).
  Demo runs `xtandard-image:main-gpl` from main de83223 (bases still r5/r6/r7: no XCF policy yet).
  **Pending:** branch `m4/wp-110-classify` (rebased, plus commit bumping bases to slim r6 / full r7 /
  gpl r8 for WP-107 XCF policy + WP-110 model; its ROADMAP row says `#109`: fix to the real PR
  number); building those bases failed with **Docker VM disk full (4.4 GB free of 88 GB)**; asked the
  owner to allow pruning 32 dangling volumes + 3 stale `xi-quickstart-*` containers, or to grow the
  Docker disk. WP-103 (LibRaw snapshot 2026-09-25, X3F, GPR, Adobe DNG SDK for JPEG XL DNG;
  branch `m4/wp-103-libraw-coverage`, verified on `xtandard-base:wp103*`) is done but NOT merged: owner
  must decide whether the Adobe DNG SDK (indemnity clause, `LicenseRef-Adobe-DNG-SDK`) ships in all
  flavours or only `gpl` (recommended). Integrate 103 + 107 policy + 110 model in one base revision. Batch 2 (WP-104, 106, 108) not launched.
  Follow-ups renumbered: WP-113 shim float rounding, WP-114 CMYK BPC.
- **Owner decisions 2026-09-28 (late): Adobe DNG SDK ONLY in `gpl`** (slim/full LibRaw = snapshot +
  X3F; gpl adds DNG SDK + GPR; slim/full keep WP-101 preview/422 for JPEG XL DNG and GPR). **Owner
  allowed deleting Docker images not used by any container** (keep `xtandard-base:dev*`,
  `xtandard-image:main-gpl`, and `wp103*`/`wp110*` until the integrated bases exist); volumes and
  containers are NOT authorised. **Next action (blocked only by transient permission-classifier
  errors):** one integration agent: prune images → branch `m4/base-integration` from main with WP-110
  then WP-103 (DNG SDK/GPR confined to gpl), one revision bump (slim r6 / full r7 / gpl r8), build
  bases (`dev*`), lint/test on slim/full/gpl, acceptance, by-hand checks, push; then orchestrator
  merges, rebuilds the demo, and launches batch 2 (WP-104, 106, 108).
- **Base integration done (2026-09-28), branch `m4/base-integration` (pushed, not merged):** main +
  WP-110 (5 commits) + WP-103 (3 commits) + DNG SDK/GPR confined to gpl (`build-libraw.sh
  --with-sdks` in gpl-deps only; pins in `versions-gpl.sh`; gate: `dng-sdk=LicenseRef-Adobe-DNG-SDK`
  only through `XI_GPL_COMPONENTS`, strict gate fails on the SDK's RTTI names) + one bump: slim r6 /
  full r7 / gpl r8, root Dockerfile `8.18.6-r6-slim`. Local `xtandard-base:dev*` are these r6/r7/r8
  builds (wp103*/wp110* tags removed). Verified: base checks slim/full/gpl, lint and tests on the three
  builders, acceptance 125/125 on full and gpl release images. Next: merge, rebuild the demo
  (`xtandard-image:main-gpl`), launch batch 2.
- **Backlog after M3 (WP-91+):** face/object gravity on a single-frame video answers 422 (detection
  hands the container bytes to libvips; fix = detect on the decoded frame; composites already skip
  detection); `classify` has no model; detections are not used in chained pipelines; `/info` capability listing (encoders).
- **Adoption recipe:** `scripts/stack/adopt2.sh - <branch> <n> "<title>" <body.md>`; on `MANUAL:` fix
  the file by hand (keep both sides for docs; README status matrix: take the most advanced status
  per row), `git add`, then loop `resolve.sh` + `GIT_EDITOR=true git rebase --continue`, then
  `go build`, `git push --force-with-lease`, `gh stack add`, `gh stack submit --auto --open`,
  `gh pr edit`. `rerere` is enabled; `scripts/stack/resolve-handoff.py` handles HANDOFF conflicts.
- **Merge plan when the stack is complete and the verifier is green:** try `gh stack merge <top>
  --yes --merge`; if GitHub refuses, the M2 fallback (one PR from the chain top, `--merge`, close
  the per-package PRs) works.
- **Owner decision 2026-09-27:** add a third image flavour **`gpl`** (FFmpeg `--enable-gpl` + libx264 +
  x265, anything else we excluded only for being GPL) with its own GPL notice; source stays MIT;
  users of that image review licences/patents themselves. WP-70b agent launched. WP-76 (GIF→video)
  must pick the H.264 encoder at runtime when present (gpl image) and fall back to VP9/AV1.
- **M3 decisions taken by the orchestrator (owner said "resuélvelo con tu mejor approach"):**
  GIF→video = WebM/VP9 + AV1-in-MP4, no H.264 encode (open question 1; **superseded by the owner
  2026-09-27**: H.264/HEVC encode ship in the opt-in `gpl` flavor, WP-70b `m3/wp-70b-gpl-base`,
  ADR 0006 amendment, FORMATS 5.1); detection models = YuNet
  (MIT) + YOLOX-s (Apache-2.0) (question 7); playground inside the binary, admin listener, off by
  default (question 8).
- Stacks start fresh per milestone: `m3/wp-XX-slug` branches, adopt with `scripts/stack/adopt2.sh`.
  Because GitHub's stack grouping blocked per-PR merges twice, the fallback that worked for M2 is:
  one PR from the verified chain top merged with `--merge`, then close the per-package PRs.

## Owner decisions taken (do not reopen)

`CLAUDE.md` rule 3 now allows exactly one more cgo package, `internal/video/ffmpeg` (build tag
`ffmpeg`, ADR 0010); the change is on `m3/wp-74-video` (#90).

See `docs/AGENT-WORKFLOW.md` "Decisions already taken" and ADRs 0007–0009. Additional ones:
RustFS replaces MinIO in test composes; the AWS Serverless Image Handler dialect is in scope
(WP-85); the auto-quality default DSSIM target stays 0.02 until WP-57 data says otherwise; the
owner has authorised merging PRs without review.

## How work is delivered

1. One Opus subagent per ROADMAP work package, in an isolated git worktree, on a branch named
   `m<milestone>/wp-XX-slug`, following `docs/AGENT-WORKFLOW.md`. Agents never run `gh stack`,
   never merge, never push to `main`.
2. The orchestrator adopts finished branches into the milestone stack with
   `scripts/stack/adopt2.sh <agent-worktree-id|-> <branch> <n-own-commits> "<PR title>" <body.md>`:
   it rebases the branch's last N commits onto the current stack top (`git rebase --onto`),
   resolves routine conflicts with `scripts/stack/resolve.sh`, builds, force-pushes with lease,
   `gh stack add`s it, syncs, opens the PR and sets its description.
3. `scripts/stack/resolve.sh` rules: `go.mod` union + `go mod tidy`; `go.sum` union; `.golangci.yml`
   keep ours; `NOTICE`, ADR 0006, `internal/security/sign/doc.go`, `docs/URL-DIALECTS.md` keep both
   sides of every hunk; `docs/ROADMAP.md` keep ours and append rows only the branch adds
   (`roadmap-merge.py`); `compose.test.yml` / `compose.acceptance.yml` take theirs. Everything else is
   reported as `MANUAL:` and must be merged by hand or by an integration agent.
4. When several finished branches touch the same files (typical: `internal/app/build.go`
   registrations, `internal/server` redirect/cache/hints, `internal/codec` format tables), spawn one
   integration agent that rebases them sequentially onto the stack top, resolves semantically,
   verifies in the container and force-pushes; then adopt each with `adopt2.sh … 0`-style bookkeeping
   (`gh stack add`, `gh stack sync`, `gh stack submit --auto --open`, `gh pr edit`).
5. Verification is the container: `XI_BASE_IMAGE=xtandard-base:dev-builder task docker:lint` and
   `task docker:test`. Local images `xtandard-base:dev*` (slim, full, gpl) are the r3/r4/r4 build (WP-93).
   Host cgo builds need `CGO_LDFLAGS_ALLOW='-Xpreprocessor|-fopenmp' CGO_CFLAGS_ALLOW=-Xpreprocessor`.
6. Merging: `gh stack merge <top PR> --yes --squash` merges the whole stack bottom-up; if GitHub
   refuses (`is part of a stack …`) or the remote grouping is stale, rebuild the stack
   (`gh stack unstack --local`, `gh stack init <branches in order>`, `gh stack submit --auto --open`)
   or merge PRs one by one after `git rebase --onto main <prev-old-tip> <branch>` +
   `gh pr edit --base main` + `gh pr merge --squash`. gh-stack's local state is `.git/gh-stack`
   (JSON; stale stacks can be pruned by hand). After a squash merge, unstacked local branches are
   deleted and the remaining ones are re-initialised from the first unmerged branch.
7. **All GitHub workflows are DISABLED since 2026-09-27 (owner decision: build and test locally only until further notice).** Re-enable with `gh workflow enable <name>` (ci, base-image, nightly, release). GitHub Actions minutes are billed (private repo). Since 2026-09-27 PR pushes run only lint, container
   unit tests and the `CGO_ENABLED=0` job; smoke/acceptance/quickstart run on pushes to `main` or when a
   PR carries the `ci-full` label; docs-only changes skip CI. The included 2,000 min/month were exhausted on
   2026-09-26 (resets 2026-10-01); runs may queue or fail until then unless a budget is set.
8. CI on stacked PRs is only green once the base image tag the branch pins exists on GHCR; the
   base image is published by `.github/workflows/base-image.yml` on pushes to `main` (never on PRs).

## Known gaps and follow-ups

- Peak RSS is still above imgproxy's after WP-87's `MALLOC_CONF=narenas:2` (png-alpha 400 px
  WebP at 16 concurrent: ~180–210 MiB vs ~165–180 MiB; it was 2–5×; see `bench/RESULTS.md`).
  Both the app image and, since WP-93, every base runtime (r3 slim, r4 full/gpl) set it.
- The PNG-alpha → WebP "14 % slower" gap did not reproduce under profiling (WP-87): the executor
  equals the bare libvips pipeline and libwebp is ~60 % of the CPU. Rerun the full bench on a quiet
  Linux host before quoting capacity.
- Client hints (`w_auto`, `dpr_auto`, `width=auto`, imgix/ImageKit auto) resolve into pipeline 0
  only: an auto width in a later chained component still resizes the first pipeline.
- Parsed but not applied (recorded as "not implemented"): Cloudinary `e_vibrance`, imgix `vib`,
  ImageKit `e-contrast` (the planner has no vibrance/equalize step); thumbor `equalize()` and AWS
  SIH `normalise` set `Equalize` without recording anything.
- The memory budget (`XI_PROCESSING_MEMORY_BUDGET`) is charged after the probe and plan
  (`limiter.Reserve` through `exec.Config.Admit`): a job waiting for memory holds its worker slot
  for up to `XI_PROCESSING_QUEUE_TIMEOUT`.
- First release tag pending (`docs/RELEASE.md`); the WP-81b Pages site is built (`task pages:build`)
  but not deployed until the owner enables GitHub Pages and the workflows.

- **2026-09-28 (latest):** base integration merged (#111): classify (WP-110) + LibRaw snapshot/X3F in
  all flavours, DNG SDK + GPR only in gpl (WP-103); bases slim r6 / full r7 / gpl r8 are the local
  `xtandard-base:dev*`. The integrator also freed Docker disk by removing unused third-party images
  (demi-*, mongo:7, clickhouse, moneat-backend…; owner informed; no volumes/containers touched).
  Demo redeployed from main 1956f2c (classify and XCF work). **In flight:** WP-104 (AVIF/HEIF
  sequences, gain maps), WP-106 (12/16-bit JPEG, JPEG-LS), WP-108 (FITS, DICOM, KTX2, MNG/PICT,
  EMF/WMF gpl-only, Leaf MOS sniffing), WP-115 (encrypted per-request PDF password on signed URLs).
  Parallel rule as before: agents build under `xtandard-base:wpNNN*`; one integration bump at the end.
- **2026-09-28 (later):** merged WP-106 (#112: 12-bit and lossless JPEG; JPEG-LS split out), WP-115
  (#113: encrypted per-request PDF password on signed URLs), WP-104 (#114: AVIF/HEIF sequences, gain
  maps, `gain_map` option, PQ AVIF / UltraHDR outputs). None needed a base change. Demo redeployed from
  main 85164d1. Still running: WP-108 (FITS, DICOM, KTX2, MNG/PICT, EMF/WMF gpl-only, Leaf MOS).
  Rebase lesson: unioning map literals in tests can duplicate keys; run `go vet` after unions.
- **WP-108 done** on `m4/wp-108-long-tail` (KTX2/Basis Universal, pure-Go FITS and DICOM with privacy
  test, MNG/JNG, PICT/Photo CD, Leaf MOS, WMF/EMF gpl-only via sandboxed converter; sandbox
  NPROC/execve race fixed; Ghostscript sandbox moved to `internal/sandbox`). Docker hung once and
  recovered by itself. Integration agent running on `m4/wp-108-integration`: rebase onto main, bases
  slim r7 / full r8 / gpl r9, finish gpl verify-image, fuzz and full suites. Then merge + redeploy demo.
- **2026-09-28 (WP-108 integration), branch `m4/wp-108-integration` (pushed, not merged):** WP-108
  rebased on main (WP-104/106/115), DICOM lossless and 12-bit JPEG frames now decode through
  WP-106's `jpeghpload` (no longer 422), one bump: slim r7 / full r8 / gpl r9, root Dockerfile
  `8.18.6-r7-slim`. Local `xtandard-base:dev*` are these builds (`wp108*` base tags removed).
  Verified: base checks slim/full/gpl, lint and `-race` tests on the three builders, acceptance
  133/133 on full and gpl release images. Next: merge, rebuild the demo.
- **WP-108 merged (#115)**, bases slim r7 / full r8 / gpl r9 (local `dev*`). Demo redeployed from main
  a69b514. In flight: WP-116 playground recipes (watermarks + showcase; adds a default watermark to
  `compose.playground.yml`).
