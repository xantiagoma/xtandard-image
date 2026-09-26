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
- **Stack `m3` (bottom → top):** #80 WP-81b/82b Pages+SchemaStore → #81 WP-70 full base → #83 WP-84
  TypeScript → #84 WP-80 playground → #85 WP-77/78 watermarks → #86 WP-87 stabilisation → #87 WP-88
  imgix mount → #88 WP-71 detection runtime. All verified green individually in the container.
- **Integration verified:** the stack top through #86 passed lint, slim+full test suites, ts:check,
  pages:build and acceptance (63/63) in the container; fixes landed on `m3/wp-87-stabilise`. Then
  #87 and #88 were rebased on it; the whole chain is synced on main (`gh stack view` shows no
  needsRebase). Lesson: an agent that pushes to a stack branch must `git fetch` right before
  checking it out, otherwise it force-pushes a tip based on a stale chain (happened once; repaired
  with `git rebase --onto <parent tip> <branch>~<own commits> <branch>` per branch).
- **In flight (2026-09-27 night):** `m3/wp-89-docs-status` (ROADMAP/README status audit: M2 rows
  still said planned; `x-xi-dialects` said planned for served dialects); `m3/wp-70b-gpl-base` (third image flavour
  `gpl`); `m3/wp-74-video` (video thumbnails, from `origin/m3/wp-70-full-base`);
  `m3/wp-72-73-detect-options` (face/object gravity + filters, from `origin/m3/wp-71-detect`). After
  WP-74: launch WP-75 (tiles/animation) and WP-76 (GIF→video; H.264 via libx264 only in `gpl`).
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

- **imgix dialect is parsed but not mounted**: `internal/dialect/imgix` exists and is tested, but
  `internal/app/build.go` only warns "enabled but not implemented yet". WP-88 (agent launched
  2026-09-27) mounts it like the other dialects and adds acceptance cases.

- PNG-alpha → WebP is ~14% slower than imgproxy and peak RSS is 2–5× (see `bench/RESULTS.md`).
- `internal/server` limiter admission cost is still 0 (planner `Cost` not wired).
- Cloudinary `w_auto`/`dpr_auto` and Cloudflare `width=auto` do not set `transform.ClientHints`.
- `XI_DIALECTS_IMAGEKIT_ENDPOINT_PATH` config key missing.
- First release tag pending (`docs/RELEASE.md`); Pages publishing of the config schema (WP-81b).
