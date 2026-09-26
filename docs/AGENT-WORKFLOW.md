# Agent workflow

How coding agents deliver a work package (WP) from `docs/ROADMAP.md`. The orchestrator assigns
WPs, assembles stacked PRs with `gh stack`, and merges. Agents never run `gh stack`, never merge,
and never push to `main`.

## Per-WP protocol

1. **Read first**: `CLAUDE.md`, `docs/go-standards.md`, the WP row in `docs/ROADMAP.md`, and the
   sections of `docs/ARCHITECTURE.md` / `docs/URL-DIALECTS.md` / `docs/FORMATS.md` /
   `docs/CONFIG.md` the row cites. Reference source lives in
   `/Users/santi/Projects/xtandard-image/.refs/` (absolute path; it is not in worktrees). `rg` it
   before implementing anything that mirrors imgproxy, imagor, unpic or libvips.
2. **Branch**: work in your assigned worktree on branch `m1/wp-XX-<slug>` (or `m2/`, `m3/`),
   created from the base branch the orchestrator names (usually `main` or the WP you depend on).
   Never rename the branch.
3. **Implement exactly the WP scope**. Interfaces come from `ARCHITECTURE.md`; if you must deviate,
   write the reason in the PR body section "Deviations" and update the doc in the same branch.
   Dependencies on other WPs that are not merged yet: code against the interface file on the base
   branch; do not stub other packages in your branch.
4. **Verify** before claiming done (all must pass; paste the output summary in your report):

   ```sh
   golangci-lint fmt --diff && golangci-lint run ./...
   go vet ./... && go fix -diff ./... && go mod tidy -diff
   go test -race -shuffle=on ./...
   ```

   cgo packages need libvips: `brew install vips` on macOS
   (`export CGO_CFLAGS_ALLOW=-Xpreprocessor`), or the base image container in CI.
5. **Commit** in small, focused commits with imperative subjects, and end each message with the
   attribution trailer `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`.
   Set the author with `git -c user.name="Santiago Montoya" -c user.email=xantiagoma@gmail.com`.
6. **Push** `git push -u origin <branch>`. Do not open a PR; the orchestrator does.
7. **Report** (final message, under 300 words): branch name, files added, how the done criteria in
   the ROADMAP row were verified, benchmarks if the WP asked for them, deviations, and anything the
   next WP needs to know.

## Rules that get PRs rejected

- Any `import "C"` outside `internal/vips`.
- `os.Getenv` outside `internal/config`; globals; `init()`.
- Tests that `time.Sleep`, use testify, or compare images byte-for-byte.
- A new third-party module without a line in the relevant ADR and a licence in MIT/BSD/Apache/ISC.
- GPL/AGPL code or binaries anywhere, including test fixtures.
- Touching files owned by another WP without saying so in "Deviations".

## Decisions already taken (do not reopen)

- GIF→video: WebM/VP9 and AV1-in-MP4; no H.264.
- HEIC: decode on, encode off by default (`XI_HEIC_ENCODE=false`).
- Invalid URL → 400; `XI_COMPAT_IMGPROXY_STATUS=true` switches to 404 like imgproxy.
- Disallowed source → 403 (imgproxy uses 404; same compat switch).
- POST upload processing: deferred until after M3.
- OpenStack Swift: not a source; use its S3 middleware.
- Unsigned client-generated URLs default to `restricted` with a startup warning listing allowed widths.
- Detection models: YuNet (faces) + YOLOX-s (objects), user-mountable ONNX.
- Playground: inside the binary, off by default, admin listener only.
