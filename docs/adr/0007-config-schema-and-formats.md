# ADR 0007: Config JSON Schema, multi-format config files, `config schema` / `config validate`

- Status: accepted; decisions 1–5, 7 and the self-serving part of 6 implemented by WP-81 (see "Implementation notes"); publishing (rest of 6) is WP-81b
- Date: 2026-09-26
- Amended: 2026-09-26, with the owner's answers to ROADMAP open questions 9 (schema URL and self-serving) and 11 (secret file references)

## Context

The config file (`--config` / `XI_CONFIG_FILE`, CONFIG.md §1) is YAML only today, decoded strictly by goccy/go-yaml into `config.Config` (go-standards §7). The owner wants editor validation and autocomplete: `# yaml-language-server: $schema=<url>` in YAML, `"$schema"` in JSON, and the same schema for YAML, JSON and TOML files. Constraints:

- `Config` has only `yaml` and `env` tags. About 15 fields use `encoding.TextUnmarshaler` types that a generic reflector models wrongly. `time.Duration` is an `int64` but is written `5s`. `HexKey` is a `[]byte`, so a reflector sees an array or base64. `ByteSize` takes `10MiB` or a plain integer, and `IntList` takes `640-1080/220` or a list. `Tristate`, `Secret` and the enum types (`UnsignedPolicy`, `SourceType`, `Format`, `DialectName`, …) have their own rules too.
- There are two dynamic maps. `sources.<name>` has keys `[a-z0-9-]` and fields that depend on `type`. `presets.<name>` is a string. Some maps take free keys, such as `sources.<name>.headers`.
- `_FILE` secrets exist only in the environment (`XI_SIGNING_KEYS_FILE`). The file has no `_file` keys. Before this ADR, a secret written in a file could only be a plain value; decision 7 adds a `{file: …}` reference.
- `internal/config` already has a tested field walker (`fields.go`, `fieldsOf`/`staticFields`/`sourceFields`), and `TestFieldWalkerMatchesLibrary` checks it against caarlos0/env. `docs_test.go` already parses every CONFIG.md §2 row (name, default, meaning) and checks it against the code.
- Env precedence must not change: defaults → file → `IMGPROXY_*` compat → `XI_*`.

## Options considered

### Schema generation

| Option | Evidence | Verdict |
|---|---|---|
| `github.com/invopop/jsonschema` | v0.14.0, 2026-04-23, MIT, 957★ (proxy.golang.org, GitHub API 2026-09-26). Emits draft 2020-12. `Reflector.FieldNameTag` can read `yaml` tags, `Mapper func(reflect.Type) *Schema` handles custom types, and `AddGoComments` adds descriptions. It has no `TextMarshaler` handling. It maps `map[string]T` to `additionalProperties` and never to `patternProperties`. Deps: `pb33f/ordered-map/v2`, testify (test). | Viable. But most of our leaves would need a `Mapper` override, and it still needs post-processing for `patternProperties` and for per-type sources. |
| `github.com/swaggest/jsonschema-go` | v0.3.79, 2025-11-10, MIT, 191★, one maintainer. `PropertyNameTag("yaml")`, `InterceptSchema`/`InterceptProp`. Its model is draft-07-centric (the reflect docs cite draft-04/07 keywords). Deps: `swaggest/refl`. | Rejected. Slower release cadence, bus factor of one, and draft-07 model. |
| huma's schema registry (`huma.SchemaFromType`) | Reads `json` tags only (`schema.go`). | Rejected for config, which has no `json` tags. We use huma for OpenAPI (ADR 0008). |
| **Hand-written generator over `fieldsOf`** | Stdlib only. It reuses the walker that already computes YAML paths and env names. Descriptions come from the CONFIG.md §2 "Meaning" cells that `docs_test.go` already parses. Defaults come from the default `Config`. It needs a small per-type table for the TextUnmarshaler types. | **Chosen.** |

### TOML decoder

| Module | Evidence | Verdict |
|---|---|---|
| **`github.com/pelletier/go-toml/v2`** | v2.4.3, 2026-07-05, MIT (LICENSE file; GitHub reports NOASSERTION only because the file has extra copyright lines). No dependencies. Supports TOML 1.1.0. Four releases in June and July 2026, including a nesting-depth DoS fix and position-carrying errors (v2.4.3 notes). | **Chosen**: most active maintenance, security fixes ship fast, zero deps. |
| `github.com/BurntSushi/toml` | v1.6.0, 2025-12-18, MIT, 5.0k★, no dependencies, TOML 1.1 on by default since v1.6.0. About one release a year. | Good fallback. It lost only on release cadence. |

### Publishing location

| Option | Notes |
|---|---|
| Raw GitHub URL on `main` | Needs no infrastructure, but `main` can describe keys an older release rejects, and the URL is tied to the repo layout. |
| Raw GitHub URL on a release tag | Versioned and immutable, but tied to the repo layout. |
| **GitHub Pages** `https://xantiagoma.github.io/xtandard-image/schema/…` | Layout-independent and versioned per release. Needs one workflow. |
| **SchemaStore catalog entry** | Self-hosted schemas are allowed: the catalog `url` points at our URL. Generic `fileMatch` values such as `config.yaml` are refused ("avoid adding generic patterns", SchemaStore CONTRIBUTING.md). Once listed, VS Code, JetBrains, yaml-language-server, Taplo and Tombi match files without a modeline. |

### Secrets inside the file (open question 11)

| Option | Precedent | Verdict |
|---|---|---|
| Status quo: plain values in the file, `_FILE` only in the env | Mounting the whole config file as a Docker/Kubernetes secret already works. | Rejected as the only answer. A config file usually lives in version control or a ConfigMap, so a single secret forces the whole file into a secret store. |
| String reference `${file:/path}` inside any string | Grafana's `$__file{/path}` in `grafana.ini`, an INI file with no structured values. | Rejected. It is a magic string that collides with interpolation: Docker Compose interpolates `${…}` in `compose.yaml` and in inline `configs.content`, and rejects `${file:/x}` as invalid unless the user writes `$${…}`. It also makes a literal secret that starts with `${file:` impossible to write, and the expansion would hide inside `UnmarshalText`, which the env path uses too. |
| **Object reference `{file: /path}` on secret-typed values** | Docker Compose's own `secrets: <name>: file: <path>` shape, and Kubernetes-style `valueFrom` indirection. Unlike INI, all three of our formats have objects. | **Chosen.** It is explicit and unambiguous and needs no escaping. The schema can model it exactly (`oneOf` a string or a closed `{file}` object), so editors complete the `file` key. |

### Editor directives (verified)

- YAML: `# yaml-language-server: $schema=<url>`. yaml-language-server supports drafts 04, 07, 2019-09 and 2020-12 (README).
- JSON: a top-level `"$schema": "<url>"` key.
- TOML: `#:schema <url>` as the first line. This is a Taplo header directive (taplo.tamasfe.dev/configuration/directives), and Tombi supports it too.

## Decision

1. **The schema is JSON Schema draft 2020-12**, generated by a hand-written generator in `internal/config` that walks `staticFields`/`sourceFields` with no new dependency. It uses only keywords that every editor above handles: `properties`, `additionalProperties`, `patternProperties`, `oneOf`, `const`, `enum`, `pattern`, `minimum`, `default`, `description`, `markdownDescription`, `$defs`/`$ref`. It never relies on `unevaluatedProperties`.
   - Every object is closed with `additionalProperties: false`, like the strict loader.
   - The root also allows `"$schema": {"type": "string"}` so JSON (and TOML `$schema` keys) validate.
   - `sources` is `patternProperties: {"^[a-z0-9-]+$": {"$ref": "#/$defs/source"}}` with `additionalProperties: false`. `source` is a `oneOf` of one closed schema per `type` (`"type": {"const": "s3"}` plus only that type's fields), so completion offers only the fields that apply, which mirrors `Validate`. `presets` uses the same name pattern with string values. `headers` uses `additionalProperties: {"type": "string"}`.
   - Per-type mapping: durations and sizes are string patterns (sizes also accept a non-negative integer), with the same grammar as `ParseDuration`/`ByteSize`. `HexKey` is a hex pattern. `IntList` is a range-string pattern or an array of integers. `CIDR` is a string. `Tristate` is a boolean or `"auto"`. Enum types list their values from the same tables their `UnmarshalText` uses. `Secret`/`HexKey` get `"writeOnly": true`. Each secret-typed value is also a `oneOf` of its string schema and `#/$defs/fileRef` (a closed `{"file": <absolute path>}` object), see decision 7.
   - Each property's `description` is the CONFIG.md meaning, followed by ``Env: `XI_…` `` and, for secrets, ``(or `XI_…_FILE`)``. This way `_FILE` shows up in the editor without inventing file-side `_file` keys. `default` comes from the default `Config`.
2. **One committed artefact, embedded.** `internal/config/config.schema.json` is generated by `TestJSONSchema` (`go test ./internal/config -run TestJSONSchema -update`, the same `-update` flow as the golden harness). The test fails when the committed file differs from the generated one. It also compiles the schema and checks it:
   - every CONFIG.md §3 example and every file in `internal/config/testdata` validates;
   - a corpus of invalid files (unknown key, wrong enum, s3-only field on an http source) is rejected.
   The file is embedded with `//go:embed`. It lives next to the package because `go:embed` cannot reach a parent directory and the project allows no Go code outside `internal/`. The owner's suggested `schema/config.schema.json` exists only as the published path. Because the generator and its CONFIG.md parsing live in `_test.go` files, the binary gains no dependency and no generator code.
3. **Validator for those tests only:** `github.com/santhosh-tekuri/jsonschema/v6` v6.0.3 (Apache-2.0, 2026-06-28, 1.3k★, full 2020-12). It is test-only and shared with ADR 0008's contract tests. The runtime never validates against the schema: the Go loader and `Validate()` remain the authority, and the schema is an editor aid that the tests keep from being stricter or looser than the loader on the covered corpus.
4. **Formats**, chosen by extension: `.yaml`/`.yml` → YAML, `.json` → JSON, `.toml` → TOML. Any other extension is a startup error that lists the three.
   - JSON goes through the existing goccy/go-yaml strict decoder unchanged, since JSON is YAML 1.2 flow syntax. A test corpus covers escapes, big integers and nested arrays.
   - TOML is decoded by go-toml/v2 into `map[string]any`, re-marshalled with goccy/go-yaml, and then goes through the same strict decoder. There is one set of struct tags (`yaml`) and one strictness and error path. TOML date/time values are rejected, because no field takes one. Errors name the dotted key path (`sources.assets.bucket`), which is the same in every format.
   - A top-level `$schema` key is removed before the strict decode in every format. YAML and TOML modelines are comments and need nothing.
   - Default discovery becomes `/etc/xtandard/config.{yaml,yml,json,toml}`. Finding more than one is an error.
   - Precedence, `_FILE`, list replacement and unknown-key errors stay as they are. The only addition is the secret file reference in decision 7.
5. **CLI** (`cmd/xtandard-image/run.go`):
   - `config schema` prints the embedded schema. Its output is byte-for-byte the same file the admin listener serves at `GET /config.schema.json` (decision 6).
   - `config validate [--config PATH] [--no-env]` runs `Load` + `Validate` and prints every violation with its path and variable, or `ok`. It exits 1 on any error. `--no-env` ignores `XI_*`/`IMGPROXY_*`, so a config repo's CI can check a file on its own.
   - `config print` is unchanged.
6. **Publishing and self-serving** (open question 9, resolved: GitHub Pages, no custom domain):
   - **The server serves its own schema** (WP-81). The admin listener (`XI_ADMIN_ADDR`, default `127.0.0.1:9090`) answers `GET /config.schema.json` with the embedded bytes, `Content-Type: application/schema+json` and a strong ETag. Like `/openapi.json` (ADR 0008 decision 3), the route is always on and never exists on the public listener. What it serves always matches the running binary's loader, which a Pages URL cannot promise for an older deployment.
   - **Pages** (WP-81b, with the release pipeline) serves `https://xantiagoma.github.io/xtandard-image/schema/config.schema.json` (latest *release*, never `main`) and `…/schema/<version>/config.schema.json`, where `<version>` is the release tag without `v`.
   - **`$id` is the versioned Pages URL** in every copy: embedded, served, printed and published. `$id` identifies the schema and is not where it is fetched from. Every `$ref` in the schema is a local `#/$defs/…` fragment, so a copy fetched from the admin listener or from a local file resolves exactly like the Pages copy.
   - **A self-hosted instance can point editors at its own admin URL** instead of Pages: `# yaml-language-server: $schema=http://127.0.0.1:9090/config.schema.json`, or the same URL in `"$schema"` or `#:schema`, reached directly or through `kubectl port-forward`. The editor then validates against exactly the version that will load the file. Offline editors use `config schema > config.schema.json` and a relative path.
   - After the first release, open a SchemaStore PR with a catalog entry that has `url` (latest), `versions`, and `fileMatch` limited to project-specific names: `xtandard-image.{yaml,yml,json,toml}`, `.xtandard-image.{yaml,yml,json,toml}`, and `**/xtandard/config.{yaml,yml,json,toml}` if reviewers accept it.
   - The raw tag URL `https://raw.githubusercontent.com/xantiagoma/xtandard-image/<tag>/internal/config/config.schema.json` stays documented as a fallback.
7. **Secret file references** (open question 11, WP-81). In every format, a secret-typed value may be written as `{file: /absolute/path}` instead of a literal.
   - It is allowed only where the value is a `Secret` or a `HexKey`: the field itself, one element of a list of them (`signing: {keys: [{file: /run/secrets/key_a}, {file: /run/secrets/key_b}]}` during a rotation), or one value of a map of them (`sources.<name>.headers.Authorization`). Anywhere else it is a type error, as today.
   - It means the same as `XI_…_FILE` for one value. After the strict decode, the loader reads the file, trims one trailing newline and parses the content with the same `UnmarshalText`. Both paths share one function.
   - The path must be absolute, so it never depends on the working directory. A missing or unreadable file, a file over 64 KiB, or content that does not parse is a startup error that names the key path and never the content.
   - Precedence does not change. The reference is a file-level value, so `XI_…` or `XI_…_FILE` in the environment still wins. `config print` shows the resolved value as `[REDACTED]`.
   - Decoding: `Secret` and `HexKey` get a goccy `BytesUnmarshaler` that accepts either a scalar or the closed `{file}` mapping. JSON and TOML reach it through the same decoder (decision 4).
   - Nothing in the file is interpolated. There is no `${…}` syntax.

## Consequences

- New direct deps: `github.com/pelletier/go-toml/v2` (runtime, `internal/config` only, depguard-confined) and `github.com/santhosh-tekuri/jsonschema/v6` (test only). WP-81 adds both to the ADR 0006 register and to go-standards §10. §7 changes from "if a YAML file is ever added" to "YAML, JSON or TOML file, one strict decode path".
- Adding a config field needs three things: the struct field, its CONFIG.md row (already required by `docs_test.go`), and `-update` for the schema. CI fails if any of the three is missing.
- TOML errors lose line numbers, because they are reported after the map pivot. go-toml's own syntax errors keep their position.
- Only `$schema` and CONFIG.md are user-visible. The file format does not change, so existing YAML files stay valid.
- A published `$id` is forever. The owner accepted the GitHub Pages URL (open question 9). Moving to a custom domain later means publishing at both URLs and keeping the old `$id` resolvable.
- The admin listener gains one always-on route, `/config.schema.json`. ADR 0008's route-coverage test must include it.
- With `{file: …}` references, a config file can live in version control or a ConfigMap while its secrets stay in Docker or Kubernetes secret mounts.

## Implementation notes (WP-81)

- The generator is `internal/config/schemagen_test.go`; `schema_test.go` holds `TestJSONSchema` (staleness, compile, valid and invalid corpora, each checked against both the schema and `Load`). `schema.go` only embeds the file and exposes `config.Schema()` and `config.SchemaID`.
- `$id` is `https://xantiagoma.github.io/xtandard-image/schema/config.schema.json` (the latest-release URL). WP-81b may rewrite it to the versioned URL when it publishes a release copy.
- `{file: …}` decoding uses goccy `CustomUnmarshaler` options for `Secret` and `HexKey` inside the decode call instead of `BytesUnmarshaler` methods, so the types' public API does not change. The unmarshaler never fails the decode: a reference or a malformed secret becomes an internal marker that is resolved (or reported with its key path and variable) after the environment is applied, so an environment value still wins and a reference it overrides is never read. `XI_…_FILE` and `{file: …}` share `readSecretFile` (64 KiB cap, trailing newlines trimmed as before).
- `presets` names use the loader's own rule, `^[^:/]+$`, instead of the source-name pattern, so the schema never rejects a preset name the loader accepts.
- `--config /dev/null` keeps meaning "no file" (it skips default discovery) although it has no known extension.
- The schema is intentionally a little stricter than the loader where editors benefit: enum values are lower case and a source must set `type` in the file.
