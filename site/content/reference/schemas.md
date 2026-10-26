---
title: Config schema and OpenAPI files
description: The JSON Schema of the xtandard-image config file and the OpenAPI 3.1 document of its HTTP API, at stable URLs, with editor modelines.
---

<!-- Written for the site (it was the Pages index, docs/pages/index.html, before WP-156).
     scripts/sync.ts replaces the MAJOR token with the schema major that scripts/pages-build.sh read
     from the config schema's $id, and BASE with the site base. -->

Machine-readable contracts of the xtandard-image server: the JSON Schema of its config file and the OpenAPI 3.1 document of its HTTP API. They are published at stable URLs on this site.

## Files

| Document | Versioned (`@MAJOR@`) | Latest |
|---|---|---|
| Config file JSON Schema (draft 2020-12) | [schema/@MAJOR@/config.schema.json](@BASE@/schema/@MAJOR@/config.schema.json) | [schema/latest/config.schema.json](@BASE@/schema/latest/config.schema.json) |
| OpenAPI 3.1 document | [schema/@MAJOR@/openapi.json](@BASE@/schema/@MAJOR@/openapi.json) | [schema/latest/openapi.json](@BASE@/schema/latest/openapi.json) |

`@MAJOR@` is the schema major version. It is the config schema's `$id` and changes only when a key is removed or renamed; `latest` follows the newest major. A running instance serves the exact copy for its own version on the admin listener: `GET /config.schema.json` and `GET /openapi.json`.

## API reference

[Browse the OpenAPI document](@BASE@/api/) with Scalar (loaded from cdn.jsdelivr.net at a pinned version). The [OpenAPI document](@BASE@/reference/openapi/) page explains how it is generated and served.

## Editor support

YAML (yaml-language-server: VS Code, JetBrains, Neovim, Helix), first line:

```yaml
# yaml-language-server: $schema=https://xantiagoma.github.io/xtandard-image/schema/@MAJOR@/config.schema.json
```

JSON, a top-level key the loader ignores:

```json
{ "$schema": "https://xantiagoma.github.io/xtandard-image/schema/@MAJOR@/config.schema.json" }
```

TOML (Taplo, Tombi), first line:

```toml
#:schema https://xantiagoma.github.io/xtandard-image/schema/@MAJOR@/config.schema.json
```

The schema URLs use `xantiagoma.github.io`, the config schema's `$id`, which GitHub redirects to this site. Every setting is described in [Configuration](@BASE@/guides/configuration/).
