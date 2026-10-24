# Orchestration handoff

Read this first when resuming the build with a fresh context. It says where the project stands,
how work is delivered, and which helper scripts exist. The roadmap with per-package status is
`docs/ROADMAP.md`; the delivery protocol for coding agents is `docs/AGENT-WORKFLOW.md`.

## State as of 2026-09-29

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
- **First prerelease `v0.1.0-rc.1` is out (2026-09-29), built locally** because Actions is
  unavailable (billing). Release: https://github.com/xantiagoma/xtandard-image/releases/tag/v0.1.0-rc.1
  (prerelease, tag on `8c9d16b`). GHCR has private packages `xtandard-image:0.1.0-rc.1[-slim|-full|-gpl]`
  (amd64 + arm64) and the bases `xtandard-image-base:8.18.6-r7-slim`, `-r8-full`, `-r9-gpl` (and `-builder`,
  plus the moving `slim`/`full`/`gpl`[-builder] base tags). The images are not signed and have no provenance.
  The SBOMs (syft SPDX/CycloneDX) and the unstripped binaries are release assets. The procedure and results are in
  `docs/RELEASE.md` "Local release (no Actions)". The `release` run that the tag started failed (billing,
  expected). Open items: once Actions is back, re-run `release` for the tag to get signed images
  (since WP-118 it builds, verifies, signs and attests slim, full and gpl). The amd64 gpl `TestEPS`
  failure under Rosetta is fixed by WP-118 (the sandbox drops only `RLIMIT_AS` under emulation;
  metafile `auto` no longer stops startup); still confirm gpl on native amd64 in that run.
  Then switch the README quick start to the GHCR image.
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
- The recurring load-only test flakes are fixed (WP-119): video leak tests (`leakcheck`), fake
  executables written without the ETXTBSY race (`internal/testutil/execscript`), Ghostscript and
  EPS timeouts that a loaded machine could reach, the sandbox helper's last limits and exec made
  unpreemptible (`pthread_create failed` / `cannot allocate memory`), gs/metafile deadlines passed
  before the fork reported as timeouts instead of 500s, and sftp bodies ending early without an
  error after a cancellation (a product bug). `TestShutdownDrains` never reproduced; its dial now
  uses a loopback address of its own against port reuse.
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
  a69b514. WP-116 playground Examples (37 recipes) merged (#116); demo redeployed from
  main a795fc1 with a default watermark. No agents in flight.
- **WP-117 merged:** playground watermarks are explicit (`wmu` with the fixture logo); the demo no longer
  sets `XI_PROCESSING_WATERMARK_SOURCE` (owner: the server default stays off; the playground must not
  rely on hidden settings). Demo redeployed. No agents in flight.
- **WP-118 done** on `m4/wp-118-release-followups` (pushed, not merged): sandbox drops only
  `RLIMIT_AS` under Rosetta/QEMU (`XI_SANDBOX_MAX_AS`, `XI_SANDBOX_DETECT_EMULATION`, ADR 0011
  amendment), metafile `auto` never stops startup; `release.yml` builds, verifies, signs and attests
  slim, full and gpl (flavour × arch matrix, per-flavour merge, a `release` job); app OCI labels per
  flavour (`XI_FLAVOR`, gpl `GPL-2.0-or-later AND AGPL-3.0-or-later AND MIT AND
  LicenseRef-Adobe-DNG-SDK`, `dev.xtandard.flavor`), checked by `verify-app-image.sh`. Acceptance
  133/133 on the three native arm64 images and on an amd64 gpl image under Rosetta. Note: the gpl
  **base** label is still `AGPL-3.0-or-later AND GPL-2.0-or-later AND MIT` (no DNG SDK LicenseRef);
  align it at the next base bump. No agents in flight.
- **M5 video started (2026-10-04, owner decision):** video transformations like Cloudinary video and
  Cloudflare Media Transformations (incl. audio-only), same binary/images (full/gpl), separate
  subsystem with its own pool, off by default, `XI_ROLE=all|image|video`. Phase A sync short
  transforms (trim/resize/crop/fps/rotate/quality, mp4/webm, mute, audio-only, overlays) cached +
  Range; Phase B HLS/DASH JIT packaging. In flight: WP-120 (ADR 0012, ROADMAP M5 table, FFmpeg base
  with audio/filters/libopus/LAME/HLS muxers under `xtandard-base:wp120*`), WP-121 (core subsystem,
  transcode shim, Range serving, singleflight, role) building against wp120 bases when ready.
  Next: WP-122 options, WP-123 audio, WP-124 overlays, WP-125 dialects, WP-126 HLS/DASH, WP-127 playground.
- **M5 foundation merged (#120):** WP-120 (ADR 0012, FFmpeg audio/filters/HLS muxers) + WP-121 (video
  subsystem `internal/media`, transcode shim, Range, coalescing, `XI_ROLE`, `XI_VIDEO_TRANSFORM_*`,
  unsigned video denied by default); bases slim r7 (unchanged), full r9, gpl r10 (local `dev-full*`,
  `dev-gpl*`). Planned: WP-121b spool to disk, WP-121c cost-based admission. **In flight:** WP-122/123
  (imgproxy/query video URL syntax + audio features, TS builder), WP-124 (image/text overlays on
  video), WP-125 (Cloudinary/Cloudflare/ImageKit video dialects). Next: integrate these three, then
  WP-126 (HLS/DASH), WP-127 (playground video tools), WP-121b/c.
- **M5 progress (2026-10-05):** merged WP-124 overlays (#121), WP-125 video dialects (#122: Cloudflare
  `/cdn-cgi/media`, Cloudinary `/video`, ImageKit video, with overlays), WP-122/123 (#123: imgproxy
  `/video/...` and `/q/video` URLs, audio features, waveform). **In flight:** WP-126 HLS/DASH JIT
  (told to rebase onto #123 and live under the `/video` family), WP-127 playground video tools (enables
  video in the demo compose), WP-119 test stabilisation (recurring leak/ghostscript/sftp flakes).
  Planned: WP-121b spool to disk, WP-121c cost-based admission.
- **M5 Phase A + B merged (2026-10-05):** WP-127 playground video mode (#124), WP-119 flaky tests +
  4 product bugs (#125), WP-126 HLS/DASH JIT with sealed child-URL tickets (#126; also fixed
  TestShutdownDrains: a parallel wildcard listener took the freed port). Demo redeployed with video
  enabled. Remaining M5: WP-121b spool to disk, WP-121c cost-based admission; open issues from WP-126:
  MPEG-TS segments, overlays on streams, chunked audio for long sources, segments refetch the source.
- **2026-10-06:** WP-128 playground media before/after with `raw:1` source passthrough (#127),
  fix: unsized video output scaled down to `XI_VIDEO_TRANSFORM_MAX_OUTPUT_PIXELS` instead of 422 (#128),
  WP-129 55 video/audio recipes with public samples (#129; demo shows 104 recipes). Open: Cloudinary
  `e_volume` levels other than mute not mapped; no HDR/10-bit or rotation-metadata sample yet.
- **In flight (2026-10-06):** WP-121b video spool to disk + shared stream source (`m5/wp-121b-video-spool`),
  WP-121c cost-based video admission (`m5/wp-121c-video-admission`), WP-130 polish batch: WP-113 shim
  float rounding, WP-114 CMYK BPC, `config validate` Azure/SFTP, gpl licence label (DNG SDK/GPR),
  imgproxy allowed-sources scheme (`m5/wp-130-polish`). 121b and 121c both touch `internal/media`:
  merge 121b first, then rebase 121c.
- **Merged 2026-10-06:** WP-121c cost-based video admission (#130), WP-130 polish (#131: WP-113, WP-114,
  Azure/SFTP validate, imgproxy allowed-source scheme, gpl licence labels; base labels need a base
  revision bump to take effect). Still in flight: WP-121b spool (told to rebase onto WP-121c).
- **Merged 2026-10-06:** WP-121b video spool + shared stream sources (#132). M5 robustness done.
  Remaining M5: MPEG-TS segments, overlays on streams, chunked audio for long streams, video preset
  whitelist for unsigned URLs, Cloudinary `e_volume` levels, `raw:1` still fetches into memory, S3 cache
  hits read whole. Nothing in flight.
- **In flight (2026-10-15):** WP-131 HLS extras: MPEG-TS, overlays on streams, chunked audio
  (`m5/wp-131-hls-extras`); WP-132 unsigned video presets / restricted policy (`m5/wp-132-video-presets`);
  WP-133 TypeScript client audit + video/info/query builders (`m5/wp-133-ts-client`). After 131/132 merge,
  add their new options to the TS client (WP-133 lists what is needed).
- **Merged 2026-10-15:** WP-133 TS client catch-up + drift tests (#133), WP-132 video presets and
  restricted unsigned video policy (#134). Demo redeployed (106 recipes, presets `web`/`preview`).
  Local bases: `xtandard-base:dev-gpl*`/`dev-full*` were found rebuilt (same IDs as `wp132-gpl*`, from
  main's `docker/base`); `dev-slim` is missing. Origin unconfirmed (WP-132 denies retagging). Still in
  flight: WP-131 HLS extras.
- **Merged 2026-10-15:** WP-131 HLS extras (#135: MPEG-TS `sgf:ts`, overlays on streams, chunked
  stream audio `XI_VIDEO_TRANSFORM_AUDIO_CHUNK`) + TS client `streamUrl({ segmentFormat })`. Demo
  redeployed; TS master/segment verified (video/mp2t, sync byte 0x47). Nothing in flight.
  - **Base images:** the WP-131 agent rebuilt `xtandard-base:dev-gpl*`/`dev-full*` with
    `task docker:base:*` from main's `docker/base` (rule broken; contents match main). `dev-slim` was
    already missing; rebuild it with `task docker:base:slim` when needed.
  - **Open (playground-check):** "compare audio: A/B at the same position" fails in headless Chrome
    (Δ 130–230 ms; WP-128 measured 20 ms). Probing shows the audible player's clock runs ~5-7% faster
    than the muted one and the source's 1.1× rate nudge doesn't close it; source and result MP3s are
    identical in duration/sample rate and raw Range requests take <10 ms, so it looks like a headless
    audio-clock artefact, not a server regression. Needs a check in a real browser; then either fix
    compare.js sync for audio or loosen/skip the check headless.
- **M6 clients (owner 2026-10-15):** official clients for Python, Go, PHP, Ruby, Rust, Java, .NET,
  Elixir (+ existing TS); no Bazel (mise + Taskfile, native tooling, path-filtered CI). **WP-134 done**
  (`m6/wp-134-clients-foundation`, pushed, PR not opened): ADR 0013, `clients/spec/spec.json` (from
  `internal/apidoc` `BuildClientSpec`, `TestClientSpec`), `clients/spec/conformance.json` (150 cases,
  values by the TS client, held to the server by `TestConformanceFixture`), `clients/codegen` (Bun,
  emitters discovered per file), `task clients:{spec,generate,fixtures,check,test,python:test}`,
  `clients/python` (PyPI `xtandard-image`), workflows `clients.yml` + `client-python.yml` (gated by
  `vars.XI_CLIENTS_WORKFLOW`). Next: fan out WP-135..141 in parallel; each agent follows
  `clients/README.md` "Adding a language" and touches only `clients/<lang>/`, its emitter, its
  workflow, its ROADMAP/README rows and `ci.yml` paths-ignore.
- **Merged 2026-10-15:** WP-134 clients foundation + Python (#136; ADR 0013, `clients/spec`,
  `clients/codegen`, `task clients:*`). **In flight:** WP-135 Go, WP-136 PHP, WP-137 Ruby, WP-138 Rust,
  WP-139 Java, WP-140 .NET, WP-141 Elixir (`m6/wp-13N-<lang>-client`). Each touches only its client dir,
  emitter and workflow plus one row in clients/README, ROADMAP, README and ci.yml paths-ignore: merge one
  at a time, unioning those shared rows. Note: ADR numbering has two 0010s.
- **M6 clients done (2026-10-15):** PHP #137, Go #138, Elixir #139, .NET #140, Rust #141, Java #142,
  Ruby #143 (plus Python #136). On main `task clients:test` and `task clients:check` pass for all eight.
  Not published to any registry (owner decision pending). Release notes: PHP needs a split repo for
  Packagist; Go client tags `clients/go/vX.Y.Z`; client workflows run only when repo variable
  `XI_CLIENTS_WORKFLOW=enabled`. Nothing in flight.
- **2026-10-22:** the TypeScript client moved from `packages/image-url` to `clients/typescript` and was
  renamed `@xtandard/image` (never published, so no npm name was released). Bun workspaces are
  `clients/typescript` + `packages/*` (kept for future React/middleware packages); ADR 0013 updated, ADRs
  0008/0009 carry an amendment note. Also fixed the scheduled base-image shellcheck failure (#144).
- **2026-10-22, no mirror repos:** PHP publishes from a root `composer.json` (#146; `/testdata`
  export-ignore; `scripts/check-root-manifests.ts` in `task clients:check`). **In flight:** WP-142 Swift
  (root `Package.swift`, `m6/wp-142-swift-client`), WP-143 Kotlin Multiplatform
  (`m6/wp-143-kotlin-client`), WP-144 Dart (`m6/wp-144-dart-client`). Merge with the client helpers
  (shared rows union); ci.yml paths-ignore needs `clients/swift|kotlin|dart/**` and `Package.swift`.
- **Merged 2026-10-22:** Dart #147, Swift #148 (root `Package.swift`; root `.gitignore` ignores SwiftPM
  state; `scripts/check-root-manifests.ts` checks its target paths), Kotlin Multiplatform #149
  (`xtandard-image-kmp`; JVM, macOS, iOS; no androidTarget/JS yet). Twelve clients now:
  TS, Python, Go, PHP, Ruby, Rust, Java, .NET, Elixir, Swift, Kotlin MP, Dart. Nothing in flight.
  Unverified: `client-kotlin.yml` uses simulator "iPhone 16" on the macOS runner.
- **Codegen design (owner, 2026-10-23): no code built by string concatenation.** Pattern: code model
  (CodeDOM) → per-language refiners → per-language printers on a Wadler/Leijen Doc algebra (group,
  indent, line, softline) → official formatter. In flight: WP-145 playground snippets for all 12 clients
  with highlighting, built this way + an execution check per language (`m6/wp-145-playground-snippets`).
  Next: WP-146 migrates `clients/codegen` emitters (~7k lines of template strings) to the same design,
  reusing WP-145's Doc module, with byte-identical generated output as the gate.
- **Merged 2026-10-23:** WP-145 playground snippets (#151): 13 targets, code model → refiner → Doc
  printer in `internal/playground/assets/snippets/`, own tokenizer for highlighting, execution check
  `task playground:snippets` 156/156; also `TestSniffFixtures` skips hidden files (.DS_Store). Demo
  redeployed (assets under `assets/<hash>/`). **In flight:** WP-146 migrates `clients/codegen` to the same
  design, byte-identical generated output as the gate (`m6/wp-146-codegen-model`).
- **Merged 2026-10-23:** WP-146 (#152): `clients/codegen` on code model → refiner → Doc printer, one
  Doc module (`internal/playground/assets/snippets/doc.js`, re-exported by `clients/codegen/doc.ts`),
  generated output byte-identical for all 12 clients. Also fixed `TestProbeEndpoints` flake (shared probe
  slots). Proposed model follow-ups in the WP-146 report (generic `N.ext` kinds, conditional/initializer
  nodes, Param docs, module inner docs, PHP docblock tags). Nothing in flight. Pending owner answer:
  HTTP QUERY (RFC 10008) as WP-147.
- **In flight (2026-10-23):** WP-147 HTTP QUERY method (RFC 10008) on `/q` and `/q/video`: form and
  JSON bodies, `Accept-Query`, `xi_sig` over method+path+canonical params, cache shared with GET, CORS,
  opt-in in the clients, playground toggle (`m6/wp-147-http-query`).
- **Merged 2026-10-23:** Rust `ctutils` 0.4.5 for RUSTSEC-2026-0385 (#153); WP-147 HTTP QUERY (#154, ADR
  0014): additive, verified on the demo (GET/HEAD of 9 URLs byte-identical before/after incl. ETags;
  QUERY form/JSON 200 with `Location`, `Accept-Query`, `Vary`). Follow-ups: QUERY snippet refiners for
  the 9 other languages; Swift/Dart method enums gained `.query`. Headless check: audio A/B (known) and an
  intermittent `audio-loudnorm-speech` recipe timeout (external archive.org sample). Nothing in flight.

- **In flight 2026-10-23: M7 deployment and operations.** WP-148 (`m7/wp-148-operations`: OPERATIONS.md,
  Kustomize, Helm chart, KEDA/HPA, Grafana dashboard, alert/SLO rules, OTel stack, local kind autoscale
  check) and WP-149 (`m7/wp-149-platforms`: DEPLOY.md, compose/Swarm/Dokploy/Coolify/Openship/Fly/
  Cloud Run/ECS/App Runner/Container Apps/Railway/Render/DO, Terraform/OpenTofu, Pulumi, SST, CDK),
  parallel; WP-149 consumes the chart at `deploy/helm/xtandard-image` (reconcile value names at merge).
  Merge WP-148 first. Backlog: standalone Linux tarballs (ROADMAP "Backlog after M4").

- **Merged 2026-10-23: M7** WP-148 (#155: OPERATIONS.md, Kustomize, Helm chart, KEDA/HPA, dashboard,
  rules, OTel stack; kind autoscale 1→5→1) and WP-149 (#156: DEPLOY.md, every platform file, Terraform/
  OpenTofu, Pulumi, SST, CDK; `task deploy:check`, `deploy:platforms:check`, `deploy:platforms:run`).
  Workflow `deploy.yml` gated by `XI_DEPLOY_WORKFLOW=enabled`. **In flight:** WP-150 (`m7/wp-150-config-apply`):
  GOMEMLIMIT fraction, ready queue threshold and max connections were validated but never applied.

- **Merged 2026-10-24: WP-150** (#157): GOMEMLIMIT from the cgroup limit, `/readyz` queue threshold,
  public-listener connection limit, plus `XI_SECURITY_SECRET`, `XI_SECURITY_ALLOWED_PROCESSING_OPTIONS`
  and `XI_FETCH_MAX_CONCURRENT` (found by the Config audit; guard `TestEveryFieldHasAConsumer`).
  `XI_PROCESSING_LINEAR_RESIZE` is a backlog gap. Demo redeployed: GET/HEAD snapshot identical.
  Nothing in flight.

- **2026-10-24: `v0.1.0-rc.2` released locally** (commit `e6ed827d`; GHCR `0.1.0-rc.2[-slim|-full|-gpl]`,
  amd64 + arm64, unsigned; acceptance 143/155/155 on both arches). GitHub Actions is **disabled** on the
  repository (owner decision after billing blocked it again); re-enable it in Settings → Actions or with
  `gh api -X PUT repos/xantiagoma/xtandard-image/actions/permissions -F enabled=true`. Deploy manifests and
  the Helm chart point at rc.2.
