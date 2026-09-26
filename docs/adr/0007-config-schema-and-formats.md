# ADR 0007: Config JSON Schema, multi-format config files, `config schema` / `config validate`

- Status: accepted (implementation: WP-81, publishing: WP-81b)
- Date: 2026-09-26

## Context

The config file (`--config` / `XI_CONFIG_FILE`, CONFIG.md §1) is YAML only today, decoded strictly by goccy/go-yaml into `config.Config` (go-standards §7). The owner wants editor validation and autocomplete: `# yaml-language-server: $schema=<url>` in YAML, `"$schema"` in JSON, and the same schema for YAML, JSON and TOML files. Constraints:

- `Config` has only `yaml` and `env` tags. About 15 fields use `encoding.TextUnmarshaler` types that a generic reflector models wrongly. `time.Duration` is an `int64` but is written `5s`. `HexKey` is a `[]byte`, so a reflector sees an array or base64. `ByteSize` takes `10MiB` or a plain integer, and `IntList` takes `640-1080/220` or a list. `Tristate`, `Secret` and the enum types (`UnsignedPolicy`, `SourceType`, `Format`, `DialectName`, …) have their own rules too.
- There are two dynamic maps. `sources.<name>` has keys `[a-z0-9-]` and fields that depend on `type`. `presets.<name>` is a string. Some maps take free keys, such as `sources.<name>.headers`.
- `_FILE` secrets exist only in the environment (`XI_SIGNING_KEYS_FILE`). The file has no `_file` keys, so the file schema does not need them. It does document them, see below.
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

### Editor directives (verified)

- YAML: `# yaml-language-server: $schema=<url>`. yaml-language-server supports drafts 04, 07, 2019-09 and 2020-12 (README).
- JSON: a top-level `"$schema": "<url>"` key.
- TOML: `#:schema <url>` as the first line. This is a Taplo header directive (taplo.tamasfe.dev/configuration/directives), and Tombi supports it too.

## Decision

1. **The schema is JSON Schema draft 2020-12**, generated by a hand-written generator in `internal/config` that walks `staticFields`/`sourceFields` with no new dependency. It uses only keywords that every editor above handles: `properties`, `additionalProperties`, `patternProperties`, `oneOf`, `const`, `enum`, `pattern`, `minimum`, `default`, `description`, `markdownDescription`, `$defs`/`$ref`. It never relies on `unevaluatedProperties`.
   - Every object is closed with `additionalProperties: false`, like the strict loader.
   - The root also allows `"$schema": {"type": "string"}` so JSON (and TOML `$schema` keys) validate.
   - `sources` is `patternProperties: {"^[a-z0-9-]+$": {"$ref": "#/$defs/source"}}` with `additionalProperties: false`. `source` is a `oneOf` of one closed schema per `type` (`"type": {"const": "s3"}` plus only that type's fields), so completion offers only the fields that apply, which mirrors `Validate`. `presets` uses the same name pattern with string values. `headers` uses `additionalProperties: {"type": "string"}`.
   - Per-type mapping: durations and sizes are string patterns (sizes also accept a non-negative integer), with the same grammar as `ParseDuration`/`ByteSize`. `HexKey` is a hex pattern. `IntList` is a range-string pattern or an array of integers. `CIDR` is a string. `Tristate` is a boolean or `"auto"`. Enum types list their values from the same tables their `UnmarshalText` uses. `Secret`/`HexKey` get `"writeOnly": true`.
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
   - Precedence, `_FILE`, list replacement and unknown-key errors stay as they are.
5. **CLI** (`cmd/xtandard-image/run.go`):
   - `config schema` prints the embedded schema.
   - `config validate [--config PATH] [--no-env]` runs `Load` + `Validate` and prints every violation with its path and variable, or `ok`. It exits 1 on any error. `--no-env` ignores `XI_*`/`IMGPROXY_*`, so a config repo's CI can check a file on its own.
   - `config print` is unchanged.
6. **Publishing** (WP-81b, with the release pipeline):
   - Pages serves `https://xantiagoma.github.io/xtandard-image/schema/config.schema.json` (latest *release*, never `main`) and `…/schema/<version>/config.schema.json`, where `<version>` is the release tag without `v`.
   - `$id` is the versioned URL.
   - After the first release, open a SchemaStore PR with a catalog entry that has `url` (latest), `versions`, and `fileMatch` limited to project-specific names: `xtandard-image.{yaml,yml,json,toml}`, `.xtandard-image.{yaml,yml,json,toml}`, and `**/xtandard/config.{yaml,yml,json,toml}` if reviewers accept it.
   - The raw tag URL `https://raw.githubusercontent.com/xantiagoma/xtandard-image/<tag>/internal/config/config.schema.json` stays documented as a fallback.

## Consequences

- New direct deps: `github.com/pelletier/go-toml/v2` (runtime, `internal/config` only, depguard-confined) and `github.com/santhosh-tekuri/jsonschema/v6` (test only). WP-81 adds both to the ADR 0006 register and to go-standards §10. §7 changes from "if a YAML file is ever added" to "YAML, JSON or TOML file, one strict decode path".
- Adding a config field needs three things: the struct field, its CONFIG.md row (already required by `docs_test.go`), and `-update` for the schema. CI fails if any of the three is missing.
- TOML errors lose line numbers, because they are reported after the map pivot. go-toml's own syntax errors keep their position.
- Only `$schema` and CONFIG.md are user-visible. The file format does not change, so existing YAML files stay valid.
- A published `$id` is forever. Moving to a custom domain later means publishing at both URLs (owner decision, see ROADMAP open questions).
