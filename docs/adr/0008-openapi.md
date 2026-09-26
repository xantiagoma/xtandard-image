# ADR 0008: OpenAPI 3.1 document generated from Go types

- Status: accepted (implementation: WP-82, WP-83; router spike: WP-82b; clients: WP-84; publishing: WP-81b)
- Date: 2026-09-26
- Amended: 2026-09-26, with the owner's answers to ROADMAP open questions 10 (TypeScript layout, see [ADR 0009](0009-typescript-monorepo.md)) and 12 (huma as router, decision 5)

## Context

The owner wants what ElysiaJS gives: an API description generated from code, used for client codegen (TypeScript via `openapi-typescript`) and a docs UI. The constraints:

- **stdlib `net/http.ServeMux` only, no globals** (go-standards §2, CLAUDE.md 4 and 11). Every dependency needs an ADR line and an MIT/BSD/Apache/ISC licence (§10). Banned modules include `gopkg.in/yaml.v2/v3`, `google/uuid`, testify, chi/gin/echo/gorilla, and `go-playground/validator` (§7).
- **The surface is unusual.** The image endpoint is a path grammar with many dialects, carried by the catch-all `GET /{path...}` and `dialect/router`. `/info` goes through the same catch-all (`<mount>/info/...`, ARCHITECTURE §2). Neither can be modelled as ordinary OpenAPI path parameters: a path parameter cannot contain `/`, and `allowReserved` applies only to query parameters.
  - Ordinary operations: `/healthz`, `/readyz`, and on the admin listener `/metrics`, pprof, the playground (WP-80), the future `/openapi.json` and `/config.schema.json` (ADR 0007 decision 6). The POST upload endpoint is deferred until after M3 (AGENT-WORKFLOW "Decisions already taken").
- **Typed payloads exist but are unexported today.** `server.infoJSON` (the imgproxy-compatible `/info` body) and the error envelope `{"error": {"kind", "message", "request_id"}}` in `server/errors.go` are both unexported.

## Options considered

Versions from proxy.golang.org, licence, stars and last push from the GitHub API, all checked 2026-09-26.

| Option | Version / licence / activity | Fit |
|---|---|---|
| **`github.com/danielgtaylor/huma/v2`** | v2.39.1 (2026-07-29), MIT, 4.4k★, pushed 2026-09-24 | **Router.** The `humago` adapter (`adapters/humago/humago.go`) accepts any `Mux` with `HandleFunc` + `ServeHTTP` and registers `METHOD prefix+path`, so `*http.ServeMux` works as is; `NewWithPrefix` exists.<br>**Imports.** The core package imports only stdlib plus its own `casing`, `negotiation`, `validation` and `yaml` subpackages (checked file by file). The gin/echo/fiber/chi/gorilla/testify/uuid lines in its `go.mod` belong to other adapters and tests and are pruned from our build; on Go 1.27 it uses stdlib `uuid` (`schema_uuid_go127.go`).<br>**OpenAPI.** OpenAPI **3.1.0** by default, `OpenAPI.Downgrade()` to 3.0.3, JSON Schema 2020-12 registry (`NewMapRegistry`, `SchemaFromType`). Tags `doc`, `enum`, `format`, `example`, `pattern`. `TextUnmarshaler` types become strings, and `SchemaProvider`/`SchemaTransformer` give per-type control. `Extensions` maps carry `x-` fields, and `OpenAPI.AddOperation` adds operations **without handlers**.<br>**Docs UI.** Stoplight Elements, Scalar or Swagger UI, loaded from unpkg with SRI.<br>**Caveat.** It has mutable package-level variables (`NewError`, `DefaultArrayNullable`, `ErrorFormatter`, `ValidateStrictCasing`, `DefaultFormats`). We never assign them. |
| `github.com/go-fuego/fuego` | v0.20.0 (2026-07-21), MIT, 1.8k★ | Built on Go 1.22 `net/http`, but it requires `go-playground/validator/v10` (rejected in §7), `gorilla/schema` and `gopkg.in/yaml.v3` (banned), and uses `kin-openapi`. Its automatic features need fuego's handler signatures. **Rejected.** |
| `github.com/jkaninda/okapi` | v1.0.0 released 2026-09-26 (today), MIT, 99★, one main author | Has its own router, not ServeMux. Its `go.mod` requires `gopkg.in/yaml.v3` and `google/uuid` (both banned), plus kin-openapi. Too new to judge. **Rejected.** |
| `swaggest/rest` + `swaggest/openapi-go` | rest v0.2.76 (2026-09-17), openapi-go v0.2.61 (2026-04-14), MIT, one maintainer | `rest` requires `go-chi/chi/v5` and `gorilla/mux`. `openapi-go` has an `openapi31` reflector but requires `gopkg.in/yaml.v2` (banned). Its schema model is draft-07-centric. **Rejected.** |
| `github.com/getkin/kin-openapi` (hand-built spec + `openapi3gen`) | v0.149.0 (2026-08-28), MIT, 3.3k★, still v0 | It is a document model plus validator, but OAS 3.1 support is recent (v0.148 "carry format validators over to OAS3.1"), and v0 releases break often. `openapi3gen` reflection is weaker than huma's. Deps: `gorilla/mux` (routers package), `oasdiff/yaml`. **Rejected as builder.** |
| `oapi-codegen/oapi-codegen/v2` (spec-first) | v2.8.0 (2026-07-17), Apache-2.0, 8.6k★ | Excellent std-http server generation, and OAS 3.1 support landed (#2336). But it is **spec-first**, the opposite of "generated from Go types": `/info`, errors and config would be hand-maintained YAML, drifting from Go. **Rejected** (revisit only if we ever go spec-first). |
| `ogen-go/ogen` (spec-first) | v1.24.0 (2026-08-07), Apache-2.0, 2.1k★ | Spec-first, and it generates its own static radix router instead of using ServeMux. **Rejected.** |
| Hand-written OpenAPI structs + own reflector | none | Zero deps, but it reimplements what huma's registry already does well (refs, 2020-12, nullability, enums). Kept as the fallback if huma becomes unmaintained. |

## Decision

1. **Use huma v2 as the OpenAPI *builder*, not as the router, in WP-82.** Decision 5 sets how the WP-82b spike can change that for JSON endpoints. Until then, handlers stay plain `http.HandlerFunc`s on our two `ServeMux`es.
   - A new package `internal/apidoc` builds a `*huma.OpenAPI` (version `3.1.0`) from a `huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)`.
   - It declares every public and admin operation with `OpenAPI.AddOperation`. Response schemas are reflected from exported Go types: `server.InfoResponse`, renamed from `infoJSON`, and `server.ErrorResponse` for the error envelope, with a `Kind` enum taken from `errs`.
   - Nothing in huma is mutated. No huma adapter runs at runtime, so no huma error format or validation layer is in the request path.
   - `internal/apidoc` may import `server` and `transform` types. `server` must not import `apidoc`: `app` wires the handler.
2. **Drift is prevented by tests, since the handlers are not generated.**
   1. **Golden:** `internal/apidoc/openapi.json` is committed, embedded and regenerated with `-update`. A diff fails CI.
   2. **Route coverage:** `server` exposes its registered patterns (public and admin), and a test asserts that they map one-to-one to spec operations. `GET /{path...}` maps to the documented dialect operations, see item 4.
   3. **Contract:** handler tests validate real response bodies (`/info` fixtures of every M1 format, every `errs.Kind` error, `/healthz`, `/readyz`) against the operation's schema with `santhosh-tekuri/jsonschema/v6`, which is test-only and shared with ADR 0007.
   4. **Consumability:** CI runs `openapi-typescript` and `@redocly/cli lint` on the committed file. Until the Bun workspace exists, WP-82 runs both with `bunx` at pinned versions. Once WP-84 lands, `openapi-typescript` is a pinned devDependency of `packages/image-url`, and its `generate` script writes the committed TypeScript types there ([ADR 0009](0009-typescript-monorepo.md)).
3. **Serving:**
   - `GET /openapi.json` on the **admin** listener is always on and cheap: the embedded bytes with a strong ETag.
   - `GET /docs` is on the admin listener too, behind `XI_ADMIN_DOCS=false` (default off). It serves **Scalar** (MIT, the most active renderer, huma supports it) from a pinned jsdelivr URL with SRI and a strict CSP. Nothing is added to the binary.
   - When `XI_ADMIN_DOCS_CDN=false`, `/docs` returns 404 and says to point any OpenAPI viewer at `/openapi.json`. This covers air-gapped installs.
   - The public listener never serves either route.
4. **URL dialects in OpenAPI.** One operation per mounted dialect *grammar*, not per option.
   - imgproxy is documented as `GET /{signature}/{processingOptions}/{source}`. `processingOptions` and `source` are documented as `style: simple` strings containing `/`, and the operation carries:
     - `x-xi-dialect: imgproxy`;
     - `x-xi-path-grammar`, the EBNF from URL-DIALECTS §3.1;
     - `x-xi-processing-options: {"$ref": "#/components/schemas/ImgproxyProcessingOptions"}`;
     - `x-xi-signing`, the scheme and its link.
   - Generated HTTP clients are not expected to call it. Its purpose is the type contract for URL builders.
   - `/info` gets the same treatment with `ImgproxyInfoOptions` and the real `InfoResponse` body.
   - `ImgproxyProcessingOptions` is a JSON Schema object with one property per canonical option name (URL-DIALECTS §3.2). Each value is a positional-argument tuple (`prefixItems` with typed, ranged and enumerated items), with `x-xi-aliases`, `x-xi-milestone` and `x-xi-pro` annotations.
   - The schema is generated from a **declarative option catalogue** in `internal/dialect/imgproxy`, and a test checks the catalogue against the parser's `apply` switch in both directions and against URL-DIALECTS §3.2. `transform.Request` is **not** exposed. It is the normalised internal model: it holds `Dialect` and `Ignored`, carries unit-converted values, and changing it must not become an API break.
5. **huma as router: decided by a spike, WP-82b** (open question 12; the owner is indifferent and wants the better option). Typed handlers would make the spec equal the handler (the Elysia model) and replace the route-coverage and contract tests for those operations with compile-time types. So they are worth adopting if they cost nothing on errors or on the server model. Reading huma v2.39.1 shows where that cost could come from:
   - **Handler errors are ours.** A handler error that implements `huma.StatusError` is written as it is, with its own status (`huma.go`, handler path: `errors.As(err, &se)`, then `writeResponseWithPanic`). Our `errs`-backed `ErrorResponse` can implement it.
   - **huma's own errors use a package global.** Request validation ("validation failed", 422 by default), a 406 when a response cannot be marshalled, and the 500 for a non-`StatusError` error are all built by `WriteErr`, which calls the package variable `NewErrorWithContext`, which defaults to `NewError` (`error.go`). The documented way to customise them is to assign that variable, and we never assign huma globals.
   - **Per-API alternatives exist, but are unproven.** `Config.Transformers` (per API, `func(ctx, status, v any) (any, error)`) can rewrite a `*huma.ErrorModel` body into `ErrorResponse`. A `Resolver` that returns our `StatusError` sets the status, because the last `StatusError` wins. Per-operation `SkipValidateParams`/`SkipValidateBody` let a handler validate for itself. Separately, `WriteErr` prints to `os.Stderr`, not slog, if writing an error fails.
   - **The server model is safe.** `humago` registers `METHOD prefix+path` on our `*http.ServeMux` and reads `r.PathValue`. `http.Server`, its timeouts, the recover middleware and `MaxBytesReader` all remain ours.

   **WP-82b acceptance criteria** (M2, small, after WP-82). It passes only if all of these hold on a branch that moves `/healthz`, `/readyz` and one admin JSON route to `humago.NewWithPrefix(mux, …)`:
   1. Every error body on those routes, including huma-generated ones (malformed input, failed validation, an unacceptable `Accept`, a handler panic, an unexpected error), is our `ErrorResponse` with the status that `errs` maps its kind to. For example, invalid input is 400, not huma's 422. The WP-82 contract tests pass unchanged.
   2. No huma package-level variable is assigned. A forbidigo rule rejects `huma\.(NewError|NewErrorWithContext|ErrorFormatter|DefaultArrayNullable|ValidateStrictCasing|DefaultFormats)\s*=` across the repo, and the rule is part of the change.
   3. No regression against go-standards §2: routes stay on our stdlib `ServeMux`, `http.Server` keeps every timeout, and shutdown, recover and `MaxBytesReader` are unchanged. `go list -deps ./...` shows no new third-party module in the build. huma writes nothing to stderr in any tested path.
   4. The generated OpenAPI for the moved routes equals the WP-82 golden, or differs only by an explained, reviewed diff.
   5. `/healthz` allocations and latency do not regress (`for b.Loop()` benchmark, `benchstat -count=10`, go-standards §8).

   If it passes, an amendment moves the **JSON endpoints** to huma handlers: health, readiness, admin JSON routes, and the post-M3 upload and admin API. Depguard then lets huma into `server` and `obs` as well. If it fails, huma stays builder-only, and the spike's findings are recorded here. Either way, the **image endpoints and `/info` stay hand-written**. Both are reached through the dialect catch-all (`GET /{path...}`, `<mount>/info/...`), and their path grammar cannot be expressed as OpenAPI path parameters (Context). Non-JSON admin routes (`/metrics`, pprof, `/openapi.json`, `/config.schema.json`, `/docs`) stay plain handlers too.

## Consequences

- There is one new runtime dependency, `github.com/danielgtaylor/huma/v2` (MIT), confined to `internal/apidoc` by depguard. It pulls no third-party packages into the build. WP-82 adds it to the ADR 0006 register and go-standards §10.
  - Maintenance risk is moderate: one lead maintainer, but 4.4k★, many contributors and monthly releases.
  - The escape hatch is cheap. `apidoc` owns only declarations, so a swap to hand-written structs touches one package.
- The spec is only as true as the tests in item 2. Handlers are free-form, so the contract tests are mandatory, not optional.
- TS clients get real types for `/info`, errors, health and readiness, plus `ImgproxyProcessingOptions` for URL building. The first package is `@xtandard/image-url` in `packages/image-url` (WP-84, [ADR 0009](0009-typescript-monorepo.md)). Signing a URL still needs the key, so the builder signs server-side or in trusted code, never in the browser.
- 3.0-only tools can consume `OpenAPI.Downgrade()` output. If demand shows up, WP-82 can publish `openapi-3.0.json` too.
- The config JSON Schema (ADR 0007) stays a separate artefact. OpenAPI can `$ref` its published URL under `components.schemas.Config` for documentation, but the two are not generated by the same tool: config has `yaml` tags, and huma reads `json`.
