# SchemaStore catalog entry

Once xtandard-image is listed in the [SchemaStore catalog](https://github.com/SchemaStore/schemastore), editors that read it (yaml-language-server in VS Code and Neovim, JetBrains IDEs, Taplo, Tombi) apply the config schema to `xtandard.yaml` and similar files without a modeline ([ADR 0007](../adr/0007-config-schema-and-formats.md) decision 6, WP-81b).

| File | Purpose |
|---|---|
| [`catalog-entry.json`](catalog-entry.json) | The object to add to SchemaStore's `src/api/json/catalog.json`. |
| [`tests/positive/xtandard.yaml`](tests/positive/xtandard.yaml) | A sample the schema must accept. |
| [`tests/negative/xtandard.yaml`](tests/negative/xtandard.yaml) | A sample the schema must reject. |

`internal/config` `TestSchemaStoreEntry` checks all three on every test run: `url` is the schema's `$id`, `name` and `description` pass SchemaStore's catalog lint (`cli.js` `assertCatalogJsonHasNoBadFields`: no word "schema", no trailing `,.<space><tab>-`, no newline), every `fileMatch` is project-specific and simple, and the samples are accepted and rejected by both the schema and the real loader. CI also runs them through Ajv, the validator SchemaStore uses.

## Status: waiting for GitHub Pages

The entry is **self-hosted** ("How to add a JSON Schema that's self-hosted/remote/external" in SchemaStore's CONTRIBUTING.md): SchemaStore stores only the catalog entry, and its `url` must answer (its `node ./cli.js maintenance` task fetches every external `url` and reports the broken ones; `check-remote` is still a stub). It is `https://xantiagoma.github.io/xtandard-image/schema/v1/config.schema.json`, which exists only after the `pages` workflow has deployed. Do not open the PR before that URL returns the schema.

## Decisions and assumptions

- **Draft 2020-12.** SchemaStore's CONTRIBUTING recommends draft-07 for schemas *hosted in its repository*, and its `schema-validation.jsonc` requires hosted 2019-09/2020-12 schemas to be listed under `highSchemaVersion`. A self-hosted schema is not compiled by SchemaStore's CI (only the catalog is linted), and 2020-12 is in its accepted `$schema` list. Every editor we target supports 2020-12 (yaml-language-server supports drafts 04, 07, 2019-09 and 2020-12), and the schema uses only the keywords listed in ADR 0007 decision 1. If a reviewer asks for draft-07, the generator (`internal/config/schemagen_test.go`) can emit it; the only 2020-12-specific keyword in use is `$defs`, which draft-07 tools read as `definitions`.
- **No `src/test` files in the PR.** SchemaStore's CLI requires a `src/schemas/json/<name>.json` for every `src/test/<name>` directory, so tests belong only to hosted schemas. Our samples live here and run in our tests instead.
- **`fileMatch`.** Only project-specific names: `xtandard.{json,yaml,yml,toml}`, `xtandard-image.{json,yaml,yml,toml}` and the dot-prefixed forms, each written out because SchemaStore asks for simple patterns without `{…}` alternation. Generic names such as `config.yaml` are refused by SchemaStore. `**/xtandard/config.yaml` (the loader's `/etc/xtandard/config.*` discovery path) is left out; add it only if a reviewer accepts a directory pattern.
- **No `versions` map yet.** There is one schema major (`v1`). When `v2` exists, `url` moves to `schema/v2/…` and `versions` lists `"1"` and `"2"`.

## Opening the PR (owner)

1. Enable Pages with the workflow as source (Settings → Pages → Source: *GitHub Actions*), re-enable the `pages` workflow, and run it (Actions → `pages` → *Run workflow*). Its `verify` job fetches every published file and compares it with the committed one.
2. Confirm the URL: `curl -fsS https://xantiagoma.github.io/xtandard-image/schema/v1/config.schema.json | head -c 200`.
3. Fork and clone SchemaStore, then create a branch:

   ```sh
   gh repo fork SchemaStore/schemastore --clone && cd schemastore
   git switch -c xtandard-image
   ```

4. Insert the object from `catalog-entry.json` into `src/api/json/catalog.json` in alphabetical order by `name` (the catalog is sorted; look for the neighbours of "xtandard-image").
5. Run SchemaStore's checks (Node version from its `package.json` `engines`):

   ```sh
   npm clean-install
   node ./cli.js check
   npx prettier --check src/api/json/catalog.json
   ```

6. Open the PR titled "Add xtandard-image config" and link this repository, `docs/CONFIG.md` and the Pages URL in the description. Record the PR link in the ROADMAP WP-81b row.

After it merges, editors pick the schema up on their next catalog refresh; a modeline still wins over the catalog, so existing files keep working.
