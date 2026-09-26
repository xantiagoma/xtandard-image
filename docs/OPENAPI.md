# OpenAPI document

xtandard-image describes its HTTP surface in an OpenAPI 3.1 document ([ADR 0008](adr/0008-openapi.md)). It is generated from Go types in `internal/apidoc`, committed as [`internal/apidoc/openapi.json`](../internal/apidoc/openapi.json), embedded in the binary and served byte for byte.

## Where to get it

| Location | Served by | Notes |
|---|---|---|
| `GET /openapi.json` | admin (`XI_ADMIN_ADDR`) | Always on. Strong `ETag`, `304` on `If-None-Match`. |
| `GET /docs` | admin | Scalar API reference, only with `XI_ADMIN_DOCS=true`; otherwise `404` with a pointer to `/openapi.json`. |
| `xtandard-image openapi` | CLI | Prints the same bytes, for codegen without a running instance. |
| `https://xantiagoma.github.io/xtandard-image/schema/v1/openapi.json` | GitHub Pages | Published by `.github/workflows/pages.yml` (WP-81b) next to the config schema; `schema/latest/openapi.json` is the same file. |
| `https://xantiagoma.github.io/xtandard-image/api/` | GitHub Pages | Scalar API reference over the published document (same pinned Scalar build and SRI as `/docs`). |

| `GET /_playground/api/meta`, `POST /_playground/api/sign`, `GET /_playground/api/render` | admin (public too with `XI_PLAYGROUND_PUBLIC=true`) | Playground JSON API, tagged `playground`, only with `XI_PLAYGROUND_ENABLED=true` ([CONFIG.md](CONFIG.md#playground)); the page and its assets are documented too. |

Neither admin route exists on the public listener. The Pages copy follows `main` (the site is rebuilt whenever the committed file changes, see [RELEASE.md](RELEASE.md#schema-and-api-site-github-pages)); `v1` is the config schema's major version, shared by both documents so they are published side by side. `task pages:build` assembles the same site locally in `dist/pages/`. `/docs` is a small HTML page: the browser loads Scalar 1.72.1 from `cdn.jsdelivr.net` (pinned version, Subresource Integrity, a CSP that allows only that script), so the viewer needs internet access. Nothing of Scalar is in the binary. Without a CDN, point any OpenAPI viewer (Redocly, Swagger UI, an IDE plugin) at `/openapi.json`.

## What it covers

- `/healthz`, `/readyz` (both listeners), and admin `/metrics`, `/openapi.json`, `/config.schema.json`, `/docs`, pprof.
- **Image URLs, one operation per dialect grammar:** imgproxy `/{signature}/{processingOptions}/{source}`, the query dialect `/q` with its query parameters and imaginary's `/q/{endpoint}`, ImageKit `/imagekit/{path}`, and imgix `/imgix/{path}` (its parameters are also the playground's imgix catalogue). The root `x-xi-dialects` extension lists every dialect with its default prefix, whether this build serves it, and its [URL-DIALECTS](URL-DIALECTS.md) section.
- **`/info/{signature}/{options}/{source}`** with the `InfoResponse` body.
- **Playground** (tag `playground`): the page, its assets, `api/meta` (`PlaygroundMeta`: mounts, option catalogues, preset and source names, signing mode, output formats; never key material), `api/sign` (`PlaygroundSignRequest` → `PlaygroundSignResponse`) and `api/render`. Paths are documented at the default `XI_PLAYGROUND_PATH` (`/_playground`). The catalogues come from `apidoc.Catalogues()`: the same imgproxy option catalogue that generates `ImgproxyProcessingOptions`, plus the query dialect keys.
- **Errors:** `ErrorResponse` (`{"error":{"kind","message","request_id"}}`, sent when `Accept` lists `application/json`) with the `kind` enum and the status table of [ARCHITECTURE §12](ARCHITECTURE.md#12-failure-modes-and-status-codes).

The imgproxy options and source contain `/`, which OpenAPI path parameters cannot express, so generated HTTP clients are not expected to call the image and `/info` operations. Their contract is in `x-xi-*` extensions:

| Extension | Meaning |
|---|---|
| `x-xi-dialect` | Dialect name. |
| `x-xi-path-grammar` | The URL grammar (URL-DIALECTS §3.1). |
| `x-xi-processing-options`, `x-xi-info-options`, `x-xi-options-schema` | `$ref` to `ImgproxyProcessingOptions` / `ImgproxyInfoOptions`. |
| `x-xi-signing` | Signature scheme and link. |
| `x-xi-listener` | `public`, `admin` or `public+admin`. |

`ImgproxyProcessingOptions` has one property per canonical option name; the value is the argument list as a typed tuple: `/rs:fill:300:400:0/g:sm/q:80/` is `{"resize": ["fill", 300, 400, false], "gravity": ["sm"], "quality": [80]}`. Booleans are JSON booleans, `null` is an empty argument. Each option carries `x-xi-aliases`, `x-xi-milestone`, `x-xi-pro`, `x-xi-scope`, `x-xi-syntax`, `x-xi-default` (and `x-xi-security`). It is generated from the option catalogue in `internal/dialect/imgproxy/catalog.go`.

## Keeping it true

Handlers are plain functions on our `ServeMux`es, so tests keep the document honest:

- `TestGolden` fails on a stale file. Regenerate with `go test ./internal/apidoc -run TestGolden -update` and commit the result.
- `TestRouteCoverage` maps every registered public and admin pattern to an operation and back.
- Contract tests validate real `/info` bodies (`testdata/info`), every error kind's body, `/healthz` and `/readyz` against the schemas, and real playground `api/meta`, `api/sign` and error bodies against the `Playground*` schemas.
- The catalogue tests check the imgproxy option catalogue against the parser's `apply` switches, the URL-DIALECTS §3.2/§3.3 tables and every golden-table URL.
- CI runs `@redocly/cli lint` (a pinned `bunx` version) on the committed file. The `ts` workflow regenerates the TypeScript types from it with the pinned `openapi-typescript` devDependency of `packages/image-url` and fails on drift (below).

To reproduce the CI steps locally:

```sh
bunx @redocly/cli@2.54.3 lint --skip-rule=no-path-trailing-slash internal/apidoc/openapi.json
bun install && bun run generate:check   # or: task ts:check
```

## TypeScript

[`@xtandard/image-url`](../packages/image-url/README.md) (WP-84, [ADR 0009](adr/0009-typescript-monorepo.md)) is generated from this document by `bun run generate`:

- `packages/image-url/src/generated/openapi.ts` is the `openapi-typescript` output (`paths`, `components`, `operations`). The package's client (`@xtandard/image-url/client`) takes `InfoResponse`, `ErrorResponse` and the `ErrorBody.kind` enum from it.
- `packages/image-url/src/generated/options.ts` holds the imgproxy option catalogue, taken from `ImgproxyProcessingOptions` and `ImgproxyInfoOptions`. It has one argument-tuple type per option and form, with trailing arguments optional after `minItems`, `items` as a rest element and `title` as the element label, and the `x-xi-aliases` tables. openapi-typescript alone renders `prefixItems` as fixed-length tuples and drops the extensions. The URL builder is typed by these, so a changed catalogue changes the TypeScript types in the same PR.

Both files are committed. `bun run generate:check` fails when they are stale, so a Go change to the catalogue or the document has to regenerate them.
