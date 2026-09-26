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

   **Run these inside the base image container**, not on the host: `task docker:lint`,
   `task docker:test` (they mount the worktree into the `builder` stage of
   `docker/base`, image `xtandard-base:dev` locally or `ghcr.io/xantiagoma/xtandard-image-base:slim`).
   The Docker image is the product; a host-only pass (Homebrew libvips) proves nothing and is not
   accepted as verification. Pure-Go packages may additionally run on the host for speed.
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
- GPL/AGPL code or binaries anywhere, including test fixtures. The only exception is the opt-in `gpl` base image flavor (x264, x265, GPL FFmpeg; `docker/base/versions-gpl.sh`, ADR 0006 amendment 2026-09-27); Go code and slim/full stay GPL-free.
- Touching files owned by another WP without saying so in "Deviations".

## Decisions already taken (do not reopen)

- GIF→video (2026-09-27, FORMATS 5.1): `webm` = VP9 (libvpx) everywhere; `mp4` = H.264 (libx264) in `gpl` images and AV1-in-MP4 (libaom) in `full`; `hevc` (libx265) only in `gpl`. The Go side detects encoders at startup with `avcodec_find_encoder_by_name`; no openh264.
- No GPL code in `slim` or `full`: their FFmpeg is LGPL-only (`--disable-gpl --disable-nonfree`), no poppler, exiv2, x264/x265 or GPL `dssim`. The opt-in `gpl` flavor (WP-70b) adds exactly x264, x265 and FFmpeg with `--enable-gpl`, labelled `GPL-2.0-or-later AND MIT`; patent review is the deployer's.
- HEVC: decode stays (libde265, FFmpeg `hevc`); HEIC encode (kvazaar) off by default; HEVC video encode only in `gpl` (libx265).
- Detection model files: YuNet `face_detection_yunet_2023mar.onnx` (MIT) and YOLOX-s `yolox_s.onnx` (Apache-2.0), shipped in the `full` image under `/usr/share/xtandard/models/` with their licences (WP-70).
- HEIC: decode on, encode off by default (`XI_HEIC_ENCODE=false`).
- Invalid URL → 400; `XI_COMPAT_IMGPROXY_STATUS=true` switches to 404 like imgproxy.
- Disallowed source → 403 (imgproxy uses 404; same compat switch).
- POST upload processing: deferred until after M3.
- OpenStack Swift: not a source; use its S3 middleware.
- Unsigned client-generated URLs default to `restricted` with a startup warning listing allowed widths.
- Detection models: YuNet (faces) + YOLOX-s (objects), user-mountable ONNX.
- Playground (WP-80): inside the binary, off by default (`XI_PLAYGROUND_ENABLED=false`), served on the admin listener at `/_playground/` (`XI_PLAYGROUND_PATH`), and on the public listener only with `XI_PLAYGROUND_PUBLIC=true` (default false). No build step: plain HTML/CSS/JS embedded with `go:embed`, no bundler, no npm, no CDN scripts (works air-gapped; CSP `default-src 'self'`). URLs are signed server-side; keys never reach the browser.
