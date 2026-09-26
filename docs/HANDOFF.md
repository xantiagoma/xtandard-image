# Orchestration handoff

Read this first when resuming the build with a fresh context. It says where the project stands,
how work is delivered, and which helper scripts exist. The roadmap with per-package status is
`docs/ROADMAP.md`; the delivery protocol for coding agents is `docs/AGENT-WORKFLOW.md`.

## State as of 2026-09-27

- **M1 is complete** on `main` (PRs #1–#48, #61): server, every source type, imgproxy + query
  dialects, signing/encryption, all still formats, executor verified against imgproxy, encoders,
  `/info`, response semantics, raw/fallback, presets, limiter, observability, slim image, release
  pipeline, acceptance suite (62 cases on amd64 + arm64), README/quick start/benchmarks.
  No release tag has been cut yet (`docs/RELEASE.md`).
- **M2 in progress.** Merged: WP-66 base image r2. Open, stacked on `m2/*` (PRs #50–#67, top
  `m2/wp-63-info-pro`): imgix, ImageKit, RustFS, chained pipelines, colour adjustments, cache core,
  pixel focus + thumbor meta, client hints, auto-quality, PDF/PSD, filter extras, geometry extras,
  disk/S3/Redis cache tiers, config JSON Schema, best format, /info Pro options.
- **FROZEN top while integrator C rebases onto it** (2026-09-27): the chain
  `wp-43-79-cloudflare → wp-46-wsrv → wp-45-thumbor → wp-44-bunny → wp-85-aws-sih → wp-41-cloudinary →
  wp-49-raw → wp-47-48-simple-dialects`, then `wp-64-62-request-extras`. When it reports, adopt each with
  `gh stack add` + `gh stack sync` + `gh stack submit --auto --open` + `gh pr edit` (they will already be
  on top; no `--onto` needed).
- **Still being implemented:** WP-67 HDR (`m2/wp-67-hdr`), WP-82/83 OpenAPI (`m2/wp-82-83-openapi`).
  Adopt them after the chain with `scripts/stack/adopt2.sh`.
- **Remaining M2 after that:** WP-81b (Pages + SchemaStore publishing), WP-82b huma-handler spike,
  and a stabilisation pass **WP-87** (not yet in ROADMAP): (1) libvips process-wide error buffer race —
  `xi_load_error` in `internal/vips/load.c` reads the buffer to classify password-protected PDFs and
  can read another thread's message; fix by detecting `/Encrypt` in the PDF trailer in pure Go before
  calling pdfload and by never classifying on error text; (2) wall-clock tests (`TestRawPreviewFaster`,
  `httpsrc TestFetchTimeout/body`) → synctest or ratios with slack; (3) leak-test slack under load
  (`TestProcessNoLeak`, `TestWatermarkNoLeak`); (4) PNG-alpha→WebP throughput and peak RSS profiling.
  spike. Then M3 (`docs/ROADMAP.md`).

## Owner decisions taken (do not reopen)

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

- PNG-alpha → WebP is ~14% slower than imgproxy and peak RSS is 2–5× (see `bench/RESULTS.md`).
- `internal/server` limiter admission cost is still 0 (planner `Cost` not wired).
- Cloudinary `w_auto`/`dpr_auto` and Cloudflare `width=auto` do not set `transform.ClientHints`.
- `XI_DIALECTS_IMAGEKIT_ENDPOINT_PATH` config key missing.
- First release tag pending (`docs/RELEASE.md`); Pages publishing of the config schema (WP-81b).
