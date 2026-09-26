# Orchestration handoff

Read this first when resuming the build with a fresh context. It says where the project stands,
how work is delivered, and which helper scripts exist. The roadmap with per-package status is
`docs/ROADMAP.md`; the delivery protocol for coding agents is `docs/AGENT-WORKFLOW.md`.

## State as of 2026-09-27 (evening)

- **M1 and M2 are complete on `main`.** M2 landed as one merge commit (#79) of the 27-branch stack
  (per-package PRs #50–#78 closed with a reference; their descriptions document each package). Final
  verification of the merged tree in the container: 59 packages green.
- **Next, in order:** WP-87 stabilisation pass (agent running; see "Known gaps"), WP-81b Pages +
  SchemaStore publishing (needs GitHub Pages enabled by the owner and workflows re-enabled), WP-82b
  huma-handler spike, then **M3** per `docs/ROADMAP.md` (full image with FFmpeg + ONNX, face/object
  detection, video thumbnails, advanced watermarks, GIF→video, playground, TypeScript package WP-84).
- **In flight (launched 2026-09-27 night, one Opus agent each, isolated worktrees):**
  `m3/wp-87-stabilise` (vips error-buffer race + PDF /Encrypt pre-detection, wall-clock tests,
  leak slack, reasonM2 leftovers, client-hint flags, IMAGEKIT_ENDPOINT_PATH, limiter cost,
  progressive-blur angle, PNG-alpha→WebP perf), `m3/wp-70-full-base` (FFmpeg 8 LGPL + ONNX Runtime
  1.30 + YuNet/YOLOX-s models, local tags `xtandard-base:dev-full[-builder]`),
  `m3/wp-77-78-watermarks`, `m3/wp-84-ts-url-builder`, `m3/wp-80-playground`,
  `m3/wp-81b-82b-pages-huma`. Adopt them into a fresh `m3` stack in this order: 87, 81b, 84, 80,
  77-78, 70. After WP-70 lands, launch WP-71 (detect runtime) → WP-72/73, and WP-74 (video) →
  WP-75/76.
- **M3 decisions taken by the orchestrator (owner said "resuélvelo con tu mejor approach"):**
  GIF→video = WebM/VP9 + AV1-in-MP4, no H.264 encode (open question 1); detection models = YuNet
  (MIT) + YOLOX-s (Apache-2.0) (question 7); playground inside the binary, admin listener, off by
  default (question 8).
- Stacks start fresh per milestone: `m3/wp-XX-slug` branches, adopt with `scripts/stack/adopt2.sh`.
  Because GitHub's stack grouping blocked per-PR merges twice, the fallback that worked for M2 is:
  one PR from the verified chain top merged with `--merge`, then close the per-package PRs.

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
