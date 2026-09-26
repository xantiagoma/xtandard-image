# ADR 0009: TypeScript packages live in `packages/*`, a Bun workspace at the repo root

- Status: accepted (first package: WP-84)
- Date: 2026-09-26

## Context

WP-84 needs a TypeScript URL builder and client in this repo. ROADMAP open question 10 proposed `clients/ts`. The owner doubts that `clients/ts` is the convention, and expects more TypeScript packages later (React components, web middlewares), so the layout has to hold several packages. Constraints:

- The Go rules do not change. All Go code stays under `internal/` (CLAUDE.md 2), and the Docker build context stays `go.mod`, `cmd/` and `internal/`.
- The TS builder shares fixtures with the Go tests: the WP-21a URL table cases and the WP-19 signature vectors. Its types are generated from `internal/apidoc/openapi.json` (ADR 0008). A change on either side must fail CI in the same PR.
- The repo already uses Bun for tooling (`bunx openapi-typescript` in ADR 0008), and the project default is Bun, not npm, yarn or pnpm.

## Evidence

Repo layouts were checked on 2026-09-26 through the GitHub contents API and each repo's root `package.json` or `pnpm-workspace.yaml`.

| Repo | Layout | Takeaway |
|---|---|---|
| `grafana/grafana` (Go backend in `pkg/`) | Root `package.json` with `"workspaces": ["packages/*", …]` (yarn + nx/lerna). `packages/grafana-ui` → `@grafana/ui`, and `packages/grafana-api-clients` → `@grafana/api-clients` and `packages/grafana-openapi` → `@grafana/openapi` sit next to the Go server. | Go-first repo, TS packages in root `packages/*`, directory = npm name without the scope. |
| `hashicorp/terraform-cdk` (has `go.work`) | Yarn workspaces `packages/*` and `packages/@cdktf/*`. | `packages/*` at the root. |
| `gravitational/teleport` (Go-first) | `pnpm-workspace.yaml` → `web/packages/*`. | Still `packages/*`, nested under `web/` because the web UI is a large app of its own. |
| `connectrpc/connect-go` / `connect-es` | Separate repos. `connect-es` is npm workspaces over `packages/*`. | Split repos, but the TS repo uses `packages/*`. |
| `temporalio/sdk-typescript` | Its own repo, pnpm workspace `packages/*`. | Same. |
| `pocketbase/pocketbase` / `pocketbase/js-sdk` (2.9k★) | The SDK is in a separate repo. `ui/` in the main repo is the private, embedded admin app. | Separate SDK repo; an in-repo `ui/` app is not a library. |
| `centrifugal/centrifugo` / `centrifuge-js` | Separate repo. | Same. |
| `imgproxy/imgproxy` / `imgproxy-js-core` | Separate single-package repo (`src/`, `tests/`). | The closest domain precedent is split. |
| `encoredev/encore` | TS runtime in `runtimes/js`, a Rust N-API crate. | Not a workspace pattern, so not relevant. |
| `ascorbic/unpic` | Deno-first single package. | Not relevant. |
| Bun docs (`docs/pm/workspaces.mdx`) | "`workspaces` … By convention, they live in a directory called `packages`", with `bun.lock` at the root. | The toolchain's own convention. |

No repo uses `clients/`. Every in-repo precedent uses `packages/*` behind a root workspace manifest. The projects that split into separate repos (connect, temporal, pocketbase, centrifugo, imgproxy) have SDKs with their own release cadence and maintainers. Our package is generated from, and tested against, fixtures in this repo, so a split would only add a cross-repo drift check.

**npm name.** The scope `@xtandard` already exists and belongs to the owner: `https://registry.npmjs.org/-/org/xtandard/package` lists `@xtandard/lib`, `@xtandard/flags` and `@xtandard/webhooks`, published by `xantiagoma` from `xantiagoma/xtandard`. Because the scope is shared with another project, every package from this repo takes an `image-` prefix. `@xtandard/image-url`, `@xtandard/image-react` and `@xtandard/image-client` all returned 404 from the registry on 2026-09-26, so they are free. The unscoped `xtandard-image` is free too, and stays unused.

## Decision

1. **Layout.** A Bun workspace at the repo root:
   - `package.json`: `"private": true`, `"workspaces": ["packages/*"]`, `"packageManager": "bun@<pin>"`, and shared scripts (`generate`, `typecheck`, `test`, `lint`) that fan out with `bun run --filter './packages/*' <script>`;
   - `bun.lock`, committed, the text lockfile;
   - `tsconfig.base.json`: `strict`, `noUncheckedIndexedAccess`, `exactOptionalPropertyTypes`, `moduleResolution: "bundler"`, `verbatimModuleSyntax`. Each package extends it.
   - `packages/<name>/` holds one npm package, and `<name>` is its npm name without the scope: `packages/image-url` → `@xtandard/image-url`. Planned later: `packages/image-react` (`@xtandard/image-react`) and web middlewares as `packages/image-<framework>`. Packages depend on each other with `workspace:*`.
2. **First package: `@xtandard/image-url` in `packages/image-url`** (WP-84). It holds the URL builder, the OpenAPI types and a small typed fetch helper for `/info` and health (subpath export `@xtandard/image-url/client`). A separate `image-client` package is split out only if the client grows beyond that.
   - The OpenAPI output lands in `packages/image-url/src/generated/openapi.ts`, written by the package's `generate` script: `openapi-typescript ../../internal/apidoc/openapi.json -o src/generated/openapi.ts`. `openapi-typescript` is a pinned devDependency, not a floating `bunx`. The file is committed, so consumers and reviewers can see it.
   - Cross-language fixtures stay under the root `testdata/`, and TS tests read them by relative path, so there is one copy.
3. **Package rules.** ESM only (`"type": "module"`), an `exports` map, `.d.ts` emitted, no runtime dependencies unless an ADR allows one, `"sideEffects": false`, and Node ≥ 22 / modern browsers / Bun / Deno as targets. npm dependencies follow the ADR 0006 licence allowlist (MIT/BSD/Apache/ISC), checked in CI.
4. **Tooling.** Bun is pinned in `.mise.toml` next to Go, so `mise install` gives both. Tests use `bun test` (`*.test.ts` next to the source). Type-level tests (an unknown option or a wrong argument type must not compile) are `tsc --noEmit` over `*.test-d.ts` files with `// @ts-expect-error`.
5. **CI**: a `ts` workflow, triggered by changes to `packages/**`, `package.json`, `bun.lock`, `tsconfig.base.json`, `internal/apidoc/openapi.json` or the shared `testdata/` fixtures:
   1. install Bun at the `.mise.toml` pin, then `bun install --frozen-lockfile`;
   2. `bun run generate`, then fail on any diff: the generated types are stale if `openapi.json` changed without them;
   3. `bun run typecheck` (`tsc --noEmit` per package, including the type tests);
   4. `bun test`;
   5. the licence check over `bun.lock`.
   The Go workflows ignore these paths, and the TS workflow ignores Go-only paths.
6. **Publishing** (with the release pipeline, RELEASE.md). Package versions follow the server's release tag in lockstep, because the types describe that server version. The release job packs each non-private package with `bun pm pack` and publishes with `npm publish --provenance --access public` from GitHub Actions OIDC, so every tarball carries an npm provenance attestation. If `bun publish` gains provenance support, the job may switch to it.
7. **Isolation from Go and Docker.**
   - `go.mod` gains an `ignore` directive (Go 1.25+) for `node_modules` at the root and under `packages/*`, so `./...` never walks JS dependencies.
   - `.dockerignore` adds `packages`, `node_modules`, `package.json`, `bun.lock` and `tsconfig.base.json`.
   - CLAUDE.md non-negotiable 2 ("all code lives under `internal/`") applies to Go code. `packages/` holds TypeScript only and never Go.

## Consequences

- ROADMAP open question 10 is resolved: TypeScript stays in this repo, in `packages/*`, as the Bun workspace, and the first package is `@xtandard/image-url`.
- The repo root gains `package.json`, `bun.lock` and `tsconfig.base.json`. That is the usual cost of a mixed Go/TS repo (Grafana, terraform-cdk).
- A Go-side change to `openapi.json` or to the shared fixtures now triggers the TS workflow, so a Go PR can fail on TS drift. That is intended.
- Adding a package means adding a directory under `packages/`, with no workflow change. The `image-` prefix keeps names clear within the shared `@xtandard` scope.
- If a package ever needs its own cadence or maintainers (a large React component library, say), moving it to its own repo stays possible. The npm name does not change.
