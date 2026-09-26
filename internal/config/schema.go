package config

import _ "embed" // the config JSON Schema ships in the binary (ADR 0007)

// SchemaID is the $id of the config JSON Schema: its GitHub Pages URL
// (ADR 0007 decision 6). The path segment v1 is the schema's major version,
// not the release: it changes only when a config key is removed or renamed,
// so editors and the SchemaStore catalog keep one stable URL per major
// (scripts/pages-build.sh publishes the file there). Every $ref inside the
// schema is local, so a copy fetched from anywhere resolves the same way.
const SchemaID = "https://xantiagoma.github.io/xtandard-image/schema/v1/config.schema.json"

// schemaJSON is config.schema.json, generated from Config and CONFIG.md by
// TestJSONSchema (go test ./internal/config -run TestJSONSchema -update).
// It is the package's only variable because go:embed needs one; Schema
// hands out copies so it is never modified.
//
//go:embed config.schema.json
var schemaJSON string

// Schema returns the config file JSON Schema (draft 2020-12): the bytes of
// `config schema` and of the admin route GET /config.schema.json. The
// loader and Validate, not the schema, are the authority.
func Schema() []byte { return []byte(schemaJSON) }
