# OpenAPI document

xtandard-image describes its HTTP surface in an OpenAPI 3.1 document ([ADR 0008](adr/0008-openapi.md)). It is generated from Go types in `internal/apidoc`, committed as [`internal/apidoc/openapi.json`](../internal/apidoc/openapi.json), embedded in the binary and served byte for byte.

## Where to get it

| Route | Listener | Notes |
|---|---|---|
| `GET /openapi.json` | admin (`XI_ADMIN_ADDR`) | Always on. Strong `ETag`, `304` on `If-None-Match`. |
| `GET /docs` | admin | Scalar API reference, only with `XI_ADMIN_DOCS=true`; otherwise `404` with a pointer to `/openapi.json`. |

Neither route exists on the public listener. `/docs` is a small HTML page: the browser loads Scalar 1.72.1 from `cdn.jsdelivr.net` (pinned version, Subresource Integrity, a CSP that allows only that script), so the viewer needs internet access. Nothing of Scalar is in the binary. Without a CDN, point any OpenAPI viewer (Redocly, Swagger UI, an IDE plugin) at `/openapi.json`.

## What it covers

- `/healthz`, `/readyz` (both listeners), and admin `/metrics`, `/openapi.json`, `/config.schema.json`, `/docs`, pprof.
- **Image URLs, one operation per dialect grammar:** imgproxy `/{signature}/{processingOptions}/{source}`, the query dialect `/q` with its query parameters and imaginary's `/q/{endpoint}`, and ImageKit `/imagekit/{path}`. The root `x-xi-dialects` extension lists every dialect with its default prefix, whether this build serves it, and its [URL-DIALECTS](URL-DIALECTS.md) section.
- **`/info/{signature}/{options}/{source}`** with the `InfoResponse` body.
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
- Contract tests validate real `/info` bodies (`testdata/info`), every error kind's body, `/healthz` and `/readyz` against the schemas.
- The catalogue tests check the imgproxy option catalogue against the parser's `apply` switches, the URL-DIALECTS §3.2/§3.3 tables and every golden-table URL.
- CI runs `openapi-typescript` and `@redocly/cli lint` (pinned `bunx` versions) on the committed file.

To reproduce the CI step locally:

```sh
bunx openapi-typescript@7.13.0 internal/apidoc/openapi.json -o /tmp/openapi.ts
bunx @redocly/cli@2.54.3 lint --skip-rule=no-path-trailing-slash internal/apidoc/openapi.json
```
