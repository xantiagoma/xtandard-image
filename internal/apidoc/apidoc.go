// Package apidoc builds the OpenAPI 3.1 document of xtandard-image (ADR
// 0008, docs/OPENAPI.md) and embeds the committed copy that the admin
// listener serves at /openapi.json.
//
// huma v2 is used only as a document builder: its OpenAPI model, its JSON
// Schema registry and AddOperation, with no adapter, no router and no huma
// package-level variable ever assigned. Handlers stay plain functions on
// our ServeMuxes, so tests keep the document honest: a golden test
// (openapi.json, regenerated with -update), a route-coverage test against
// server.Patterns and admin.Patterns, contract tests validating real
// response bodies against the schemas, and the imgproxy option catalogue
// tests in internal/dialect/imgproxy.
//
// server and admin never import apidoc; app passes Spec to the admin mux.
package apidoc

import _ "embed" // the committed document

//go:embed openapi.json
var spec string

// Spec returns the committed OpenAPI document, byte for byte the file
// Marshal writes with -update.
func Spec() []byte { return []byte(spec) }
