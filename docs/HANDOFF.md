# Orchestration handoff

Read this first when resuming the build with a fresh context. It says where the project stands,
how work is delivered, and which helper scripts exist. The roadmap with per-package status is
`docs/ROADMAP.md`; the delivery protocol for coding agents is `docs/AGENT-WORKFLOW.md`.

## State as of 2026-09-27 (evening)

- **M1 and M2 are complete on `main`.** M2 landed as one merge commit (#79) of the 27-branch stack
  (per-package PRs #50–#78 closed with a reference; their descriptions document each package). Final
  verification of the merged tree in the container: 59 packages green.
- **Next, in order:** WP-87 stabilisation pass (#86), WP-81b Pages + SchemaStore publishing (#80;
  the deploy needs GitHub Pages enabled by the owner and workflows re-enabled) and the WP-82b
  huma-handler spike (#80) are all in the `m3` stack now; the rest of **M3** per `docs/ROADMAP.md`
  (face/object detection options, video thumbnails, GIF→video, `gpl` flavour) is in progress or planned.
- **M3 merged into `main`** via `gh stack merge 92 --yes --merge` (merge commit 893b191, PRs #80–#92; the
  owner granted merge permission explicitly after the session classifier denied it once). 0 open
  stack PRs from that chain. ROADMAP/README still say "in PR" for the merged M3 rows: the post-merge
  agent (`m3/wp-90-post-merge`) fixes that and runs the full container verification of `main`.
- **Open:** PR for `m3/wp-75-76-video-out` (WP-75/76, last M3 package; rebased onto main by hand:
  planner unions of detections + video composite; `later.json` regenerated; host tests green;
  container run pending). Merge order: post-merge branch first, then rebase 75/76 on it and merge.
- **Then:** first prerelease tag (`docs/RELEASE.md`; workflows disabled by the owner: build locally,
  `gh release create` with local artefacts or ask the owner to re-enable `release.yml`).
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
   `task docker:test`. Local images `xtandard-base:dev` / `dev-builder` are the r2 build.
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

- **imgix dialect is parsed but not mounted on `main`**: `internal/dialect/imgix` exists and is
  tested, but `main`'s `internal/app/build.go` only warns "enabled but not implemented yet". WP-88
  (#87, in the `m3` stack) mounts it like the other dialects and adds acceptance cases.

- Peak RSS is still above imgproxy's after WP-87's `MALLOC_CONF=narenas:2` (png-alpha 400 px
  WebP at 16 concurrent: ~180–210 MiB vs ~165–180 MiB; it was 2–5×; see `bench/RESULTS.md`). The
  app image sets it; the base runtime image (`docker/base/Dockerfile`) does not yet, so images
  built directly on the base keep jemalloc's 4 arenas per CPU until the next base release.
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
