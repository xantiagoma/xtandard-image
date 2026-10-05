package apidoc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/playground"
	"github.com/xantiagoma/xtandard-image/internal/server"
)

// Listener names in the x-xi-listener operation extension.
const (
	ListenerPublic = "public"
	ListenerAdmin  = "admin"
	ListenerBoth   = "public+admin"
)

// Schema component names.
const (
	schemaInfoResponse      = "InfoResponse"
	schemaErrorResponse     = "ErrorResponse"
	schemaErrorBody         = "ErrorBody"
	schemaProcessingOptions = "ImgproxyProcessingOptions"
	schemaInfoOptions       = "ImgproxyInfoOptions"
	schemaVideoOptions      = "ImgproxyVideoOptions"
	schemaRefPrefix         = "#/components/schemas/"
	docsBase                = "https://github.com/xantiagoma/xtandard-image/blob/main/docs/"
	urlDialectsDoc          = docsBase + "URL-DIALECTS.md"
	publicServerURL         = "http://localhost:8080"
	adminServerURL          = "http://127.0.0.1:9090"
	imageContentDescription = "The processed image; the format follows `format`, the source extension or Accept negotiation."
)

// imgproxyGrammar is the path grammar of URL-DIALECTS §3.1.
const imgproxyGrammar = `/%signature/%options.../plain/%source_url[@%extension]
/%signature/%options.../%base64url_source_url[.%extension]      (may be split with "/")
/%signature/%options.../enc/%aes_cbc_source[.%extension]        (may be split with "/")
/info/%signature/%info_options.../{plain/…|%base64…|enc/…}
/video/%signature/%video_options.../{plain/…[@%container]|%base64…|enc/…}

option      = name ":" arg *(":" arg)        ; separator configurable (XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR)
pipeline    = option *("/" option)
pipelines   = pipeline *("/-/" pipeline)       ; chained pipelines
bool        = "1" | "t" | "true" | "0" | "f" | "false"`

func ref(name string) *huma.Schema { return &huma.Schema{Ref: schemaRefPrefix + name} }

func str(desc string) *huma.Schema { return &huma.Schema{Type: "string", Description: desc} }

func textBody(pattern, example, desc string) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{
		"text/plain": {Schema: &huma.Schema{Type: "string", Pattern: pattern, Description: desc}, Example: example},
	}
}

// Build returns the OpenAPI document. It is deterministic: Marshal of two
// Builds is byte-identical.
func Build() (*huma.OpenAPI, error) {
	reg := huma.NewMapRegistry(schemaRefPrefix, schemaNamer)
	reg.Schema(reflect.TypeFor[server.InfoResponse](), true, schemaInfoResponse)
	reg.Schema(reflect.TypeFor[server.ErrorResponse](), true, schemaErrorResponse)
	reg.Schema(reflect.TypeFor[playground.Meta](), true, "")
	reg.Schema(reflect.TypeFor[playground.SignRequest](), true, "")
	reg.Schema(reflect.TypeFor[playground.SignResponse](), true, "")
	reg.Schema(reflect.TypeFor[playground.ErrorResponse](), true, "")
	reg.Schema(reflect.TypeFor[playground.TranslateRequest](), true, "")
	reg.Schema(reflect.TypeFor[playground.TranslateResponse](), true, "")
	reg.Schema(reflect.TypeFor[playground.Upload](), true, "")
	reg.Schema(reflect.TypeFor[playground.ListResponse](), true, "")
	reg.Schema(reflect.TypeFor[playground.ProbeRequest](), true, "")
	reg.Schema(reflect.TypeFor[playground.MediaInfo](), true, "")
	schemas := reg.Map()
	body, ok := schemas[schemaErrorBody]
	if !ok || body.Properties["kind"] == nil {
		return nil, fmt.Errorf("apidoc: %s has no kind property", schemaErrorBody)
	}
	for _, k := range errs.Kinds() {
		body.Properties["kind"].Enum = append(body.Properties["kind"].Enum, k.String())
	}
	body.Properties["kind"].Description += " See the status table in the ErrorResponse description."
	schemas[schemaErrorResponse].Description = errorDescription()
	schemas[schemaInfoResponse].Description = "imgproxy-compatible /info body (docs/URL-DIALECTS.md §3.3). " +
		"Each section is present exactly when its info option is on; a requested exif, iptc or xmp section is {} when the source has none."
	// The option catalogue schemas are plain JSON Schema maps: a Schema
	// with only Extensions marshals as exactly that map.
	schemas[schemaProcessingOptions] = &huma.Schema{Extensions: imgproxy.ProcessingOptionsSchema()}
	schemas[schemaInfoOptions] = &huma.Schema{Extensions: imgproxy.InfoOptionsSchema()}
	schemas[schemaVideoOptions] = &huma.Schema{Extensions: imgproxy.VideoOptionsSchema()}

	doc := &huma.OpenAPI{
		OpenAPI: "3.1.0",
		Info: &huma.Info{
			Title:   "xtandard-image",
			Version: "dev",
			Description: "Self-hosted image processing proxy. Image URLs follow URL dialects " +
				"(imgproxy is the default, see " + urlDialectsDoc + "); /info reports source metadata. " +
				"Operations tagged admin are served only on the admin listener (XI_ADMIN_ADDR), never on the public one. " +
				"The image and /info operations document a path grammar: the processing options and the source contain \"/\", " +
				"so generated HTTP clients are not expected to call them; their x-xi-* extensions are the contract for URL builders.",
			License: &huma.License{Name: "MIT", Identifier: "MIT"},
		},
		Servers: []*huma.Server{
			{URL: publicServerURL, Description: "Public listener (XI_SERVER_ADDR)."},
			{URL: adminServerURL, Description: "Admin listener (XI_ADMIN_ADDR): admin-tagged operations only."},
		},
		Tags: []*huma.Tag{
			{Name: "image", Description: "Image processing, one operation per URL dialect grammar.", ExternalDocs: &huma.ExternalDocs{URL: urlDialectsDoc}},
			{Name: "info", Description: "Source metadata (imgproxy /info)."},
			{Name: "health", Description: "Liveness and readiness, on both listeners."},
			{Name: "admin", Description: "Admin listener only: metrics, documents, profiling."},
			{Name: "playground", Description: "Playground UI and its JSON API (XI_PLAYGROUND_ENABLED, off by default): admin listener, " +
				"and the public listener only with XI_PLAYGROUND_PUBLIC. Paths are under XI_PLAYGROUND_PATH (default /_playground)."},
		},
		ExternalDocs: &huma.ExternalDocs{Description: "Documentation", URL: docsBase + "OPENAPI.md"},
		// No HTTP authentication: image URLs carry their signature in the
		// path or query (x-xi-signing), and the admin listener is private.
		Security: []map[string][]string{{}},
		Components: &huma.Components{
			Schemas:   reg,
			Responses: errorResponses(),
		},
		Extensions: map[string]any{"x-xi-dialects": dialects()},
	}
	for _, op := range operations() {
		doc.AddOperation(op)
	}
	return doc, nil
}

// Marshal returns the indented, newline-terminated document: the bytes of
// the committed openapi.json.
func Marshal() ([]byte, error) {
	doc, err := Build()
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// errorStatuses returns every status an image or /info request can fail
// with: the errs table (minus 499, which is never written) plus the
// ingress 414 and the 501 of features that land later.
func errorStatuses() []int {
	var ss []int
	for _, k := range errs.Kinds() {
		if s := errs.Status(k); s != errs.StatusClientClosedRequest && !slices.Contains(ss, s) {
			ss = append(ss, s)
		}
	}
	ss = append(ss, http.StatusRequestURITooLong, http.StatusNotImplemented)
	slices.Sort(ss)
	return ss
}

// kindsFor lists the kinds that map to status.
func kindsFor(status int) []string {
	var ks []string
	for _, k := range errs.Kinds() {
		if errs.Status(k) == status {
			ks = append(ks, k.String())
		}
	}
	return ks
}

func errorDescription() string {
	var b strings.Builder
	b.WriteString("JSON error body, sent when Accept lists application/json (text/plain otherwise). ")
	b.WriteString("Status per kind (docs/ARCHITECTURE.md §12; XI_COMPAT_IMGPROXY_STATUS=true answers 400 and forbidden 403 with 404, like imgproxy):\n\n")
	b.WriteString("| kind | status | message |\n|---|---|---|\n")
	for _, k := range errs.Kinds() {
		fmt.Fprintf(&b, "| %s | %d | %s |\n", k, errs.Status(k), errs.PublicMessage(k))
	}
	b.WriteString("\n499 (canceled) is logged, never written.")
	return b.String()
}

func errorResponseName(status int) string { return "Error" + strconv.Itoa(status) }

func errorResponses() map[string]*huma.Response {
	rs := map[string]*huma.Response{}
	for _, s := range errorStatuses() {
		desc := http.StatusText(s)
		switch ks := kindsFor(s); {
		case len(ks) > 0:
			desc += ": " + strings.Join(ks, ", ") + "."
		case s == http.StatusRequestURITooLong:
			desc += ": the URL is longer than the server accepts."
		case s == http.StatusNotImplemented:
			desc += ": a request kind this build does not implement yet (for example /info without an inspector)."
		}
		if s == http.StatusTooManyRequests || s == http.StatusServiceUnavailable {
			desc += " Retry-After is set when the limiter rejected the request."
		}
		rs[errorResponseName(s)] = &huma.Response{
			Description: desc,
			Content: map[string]*huma.MediaType{
				"application/json": {Schema: ref(schemaErrorResponse)},
				"text/plain":       {Schema: str("The client-safe message.")},
			},
		}
	}
	return rs
}

// withErrors adds a $ref to every error response.
func withErrors(rs map[string]*huma.Response) map[string]*huma.Response {
	for _, s := range errorStatuses() {
		rs[strconv.Itoa(s)] = &huma.Response{Ref: "#/components/responses/" + errorResponseName(s)}
	}
	return rs
}

func adminServers() []*huma.Server {
	return []*huma.Server{{URL: adminServerURL, Description: "Admin listener (XI_ADMIN_ADDR)."}}
}

func pathParam(name, desc, example string, ext map[string]any) *huma.Param {
	return &huma.Param{
		Name: name, In: "path", Required: true, Description: desc,
		Schema: &huma.Schema{Type: "string"}, Example: example, Extensions: ext,
	}
}

func queryParam(name, desc string) *huma.Param {
	return &huma.Param{Name: name, In: "query", Description: desc, Schema: &huma.Schema{Type: "string"}}
}

func imageResponses() map[string]*huma.Response {
	return withErrors(map[string]*huma.Response{
		"200": {
			Description: imageContentDescription,
			Headers: map[string]*huma.Param{
				"ETag":          {Description: "Strong entity tag of the result.", Schema: &huma.Schema{Type: "string"}},
				"Cache-Control": {Description: "XI_CACHE_HEADERS_* policy, or the URL's expires.", Schema: &huma.Schema{Type: "string"}},
				"Vary":          {Description: "Accept and the client hints that decided the result.", Schema: &huma.Schema{Type: "string"}},
			},
			Content: map[string]*huma.MediaType{"image/*": {Schema: &huma.Schema{Type: "string", ContentEncoding: "binary"}}},
		},
		"304": {Description: "Not modified: If-None-Match matched the ETag."},
	})
}

func signingExtension() map[string]any {
	return map[string]any{
		"scheme": "imgproxy",
		"description": "HMAC-SHA256(key, salt ‖ path after the signature, starting with /), url-safe base64 without padding, " +
			"optionally truncated to XI_SIGNING_SIZE bytes; any value (conventionally unsafe) when signing is off.",
		"docs": "https://docs.imgproxy.net/usage/signing_url",
	}
}

//nolint:funlen // one flat list of operations is the clearest form
func operations() []*huma.Operation {
	return []*huma.Operation{
		{
			OperationID: "getHealthz", Method: http.MethodGet, Path: "/healthz", Tags: []string{"health"},
			Summary:     "Liveness",
			Description: "200 while the process can serve HTTP; makes no libvips calls. Served on both listeners.",
			Servers: []*huma.Server{
				{URL: publicServerURL, Description: "Public listener."}, {URL: adminServerURL, Description: "Admin listener."},
			},
			Responses:  map[string]*huma.Response{"200": {Description: "Alive.", Content: textBody(`^ok\n$`, "ok\n", "Always ok.")}},
			Extensions: map[string]any{"x-xi-listener": ListenerBoth},
		},
		{
			OperationID: "getReadyz", Method: http.MethodGet, Path: "/readyz", Tags: []string{"health"},
			Summary:     "Readiness",
			Description: "200 when ready for traffic; 503 while starting or draining (load balancers deregister). Served on both listeners.",
			Servers: []*huma.Server{
				{URL: publicServerURL, Description: "Public listener."}, {URL: adminServerURL, Description: "Admin listener."},
			},
			Responses: map[string]*huma.Response{
				"200": {Description: "Ready.", Content: textBody(`^ready\n$`, "ready\n", "The state name.")},
				"503": {Description: "Starting or draining.", Content: textBody(`^(starting|draining)\n$`, "draining\n", "The state name.")},
			},
			Extensions: map[string]any{"x-xi-listener": ListenerBoth},
		},
		{
			OperationID: "getMetrics", Method: http.MethodGet, Path: "/metrics", Tags: []string{"admin"}, Servers: adminServers(),
			Summary:     "Prometheus metrics",
			Description: "client_golang exposition of the private registry (docs/ARCHITECTURE.md §13); OpenMetrics when negotiated.",
			Responses: map[string]*huma.Response{"200": {
				Description: "Metrics.",
				Content: map[string]*huma.MediaType{
					"text/plain":                   {Schema: str("Prometheus text format.")},
					"application/openmetrics-text": {Schema: str("OpenMetrics text format.")},
				},
			}},
			Extensions: map[string]any{"x-xi-listener": ListenerAdmin},
		},
		{
			OperationID: "getOpenAPI", Method: http.MethodGet, Path: "/openapi.json", Tags: []string{"admin"}, Servers: adminServers(),
			Summary:     "This OpenAPI document",
			Description: "The embedded document with a strong ETag; always on, admin listener only.",
			Responses: map[string]*huma.Response{
				"200": {
					Description: "The OpenAPI 3.1 document.",
					Headers:     map[string]*huma.Param{"ETag": {Description: "Strong entity tag.", Schema: &huma.Schema{Type: "string"}}},
					Content:     map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{Type: "object", Description: "OpenAPI 3.1 document."}}},
				},
				"304": {Description: "Not modified: If-None-Match matched the ETag."},
			},
			Extensions: map[string]any{"x-xi-listener": ListenerAdmin},
		},
		{
			OperationID: "getConfigSchema", Method: http.MethodGet, Path: "/config.schema.json", Tags: []string{"admin"}, Servers: adminServers(),
			Summary: "Config file JSON Schema",
			Description: "JSON Schema (draft 2020-12) of the YAML/JSON/TOML config file, with a strong ETag; always on, admin listener only (ADR 0007 decision 6). " +
				"Its $id is the published GitHub Pages copy (externalDocs); this route serves the running binary's version.",
			ExternalDocs: &huma.ExternalDocs{Description: "Published copy (the schema's $id)", URL: config.SchemaID},
			Responses: map[string]*huma.Response{
				"200": {
					Description: "The config schema.",
					Headers:     map[string]*huma.Param{"ETag": {Description: "Strong entity tag.", Schema: &huma.Schema{Type: "string"}}},
					Content:     map[string]*huma.MediaType{"application/schema+json": {Schema: &huma.Schema{Type: "object", Description: "JSON Schema document."}}},
				},
				"304": {Description: "Not modified: If-None-Match matched the ETag."},
			},
			Extensions: map[string]any{"x-xi-listener": ListenerAdmin},
		},
		{
			OperationID: "getDocs", Method: http.MethodGet, Path: "/docs", Tags: []string{"admin"}, Servers: adminServers(),
			Summary: "API reference UI",
			Description: "Scalar API reference over /openapi.json when XI_ADMIN_DOCS=true. The page loads a pinned Scalar build " +
				"from cdn.jsdelivr.net with Subresource Integrity under a strict Content-Security-Policy; nothing is bundled in the binary.",
			Responses: map[string]*huma.Response{
				"200": {Description: "The Scalar page.", Content: map[string]*huma.MediaType{"text/html": {Schema: str("HTML page.")}}},
				"404": {Description: "XI_ADMIN_DOCS is off; the body points at /openapi.json.", Content: textBody("", "", "Plain-text hint.")},
			},
			Extensions: map[string]any{"x-xi-listener": ListenerAdmin},
		},
		{
			OperationID: "getPprofIndex", Method: http.MethodGet, Path: "/debug/pprof/", Tags: []string{"admin"}, Servers: adminServers(),
			Summary:     "pprof index",
			Description: "net/http/pprof index, only with XI_ADMIN_PPROF=true (404 otherwise).",
			Responses: map[string]*huma.Response{
				"200": {Description: "Index page.", Content: map[string]*huma.MediaType{"text/html": {Schema: str("HTML page.")}}},
				"404": {Description: "pprof is disabled."},
			},
			Extensions: map[string]any{"x-xi-listener": ListenerAdmin},
		},
		{
			OperationID: "getPprofProfile", Method: http.MethodGet, Path: "/debug/pprof/{profile}", Tags: []string{"admin"}, Servers: adminServers(),
			Summary: "pprof profiles",
			Description: "net/http/pprof endpoints, only with XI_ADMIN_PPROF=true (404 otherwise): cmdline, profile (CPU, ?seconds=), " +
				"symbol, trace (?seconds=) and every named profile (goroutine, goroutineleak, heap, allocs, block, mutex, threadcreate).",
			Parameters: []*huma.Param{{
				Name: "profile", In: "path", Required: true, Description: "Endpoint or profile name.",
				Schema: &huma.Schema{Type: "string", Examples: []any{"heap", "goroutine", "goroutineleak", "profile", "trace", "cmdline", "symbol"}},
			}},
			Responses: map[string]*huma.Response{
				"200": {Description: "Profile data.", Content: map[string]*huma.MediaType{
					"application/octet-stream": {Schema: &huma.Schema{Type: "string", ContentEncoding: "binary"}},
					"text/plain":               {Schema: str("Text form (?debug=1).")},
				}},
				"404": {Description: "pprof is disabled or the profile does not exist."},
			},
			Extensions: map[string]any{"x-xi-listener": ListenerAdmin},
		},
		{
			OperationID: "postPprofSymbol", Method: http.MethodPost, Path: "/debug/pprof/symbol", Tags: []string{"admin"}, Servers: adminServers(),
			Summary:     "pprof symbol lookup",
			Description: "Resolves program counters to function names, only with XI_ADMIN_PPROF=true.",
			RequestBody: &huma.RequestBody{Content: map[string]*huma.MediaType{"text/plain": {Schema: str("Program counters joined by +.")}}},
			Responses: map[string]*huma.Response{
				"200": {Description: "Symbols.", Content: map[string]*huma.MediaType{"text/plain": {Schema: str("One symbol per line.")}}},
				"404": {Description: "pprof is disabled."},
			},
			Extensions: map[string]any{"x-xi-listener": ListenerAdmin},
		},
		infoOperation(),
		videoOperation(),
		imgproxyOperation(),
		playgroundPageOperation(),
		playgroundAssetOperation(),
		playgroundMetaOperation(),
		playgroundSignOperation(),
		playgroundRenderOperation(),
		playgroundTranslateOperation(),
		playgroundUploadOperation(),
		playgroundListOperation(),
		playgroundProbeOperation(),
		playgroundProbeResultOperation(),
		queryOperation(),
		queryVideoOperation(),
		imaginaryOperation(),
		imagekitOperation(),
		imgixOperation(),
	}
}

func imgproxyParams(optionsName, optionsDesc, optionsExample, schema string) []*huma.Param {
	return []*huma.Param{
		pathParam("signature", "URL signature, or any value (conventionally unsafe) when signing is off.", "unsafe", nil),
		pathParam(optionsName, optionsDesc, optionsExample, map[string]any{
			"x-xi-options-schema": map[string]any{"$ref": schemaRefPrefix + schema},
		}),
		pathParam("source", "The source: plain/<percent-encoded URL>[@ext], <url-safe base64 URL>[.ext] (may be split with /) "+
			"or enc/<AES-CBC token>[.ext]. Schemes: http(s), s3, gs, abs, local, ftp, sftp, xi://<source>/.",
			"plain/https://example.com/a.jpg@webp", nil),
	}
}

func infoOperation() *huma.Operation {
	return &huma.Operation{
		OperationID: "getImgproxyInfo", Method: http.MethodGet, Path: "/info/{signature}/{options}/{source}", Tags: []string{"info"},
		Summary: "Source metadata (imgproxy /info)",
		Description: "Fetches the source and reports its metadata without processing it. {options} is zero or more info options " +
			"separated by / (s:1/f:1/d:1/bh:4:3), typed by the ImgproxyInfoOptions schema; the signature covers the path after it, " +
			"as for image URLs. Grammar: x-xi-path-grammar.",
		Parameters: imgproxyParams("options", "Info options joined by /, each name:arg:arg (ImgproxyInfoOptions).", "s:1/f:1/d:1", schemaInfoOptions),
		Responses: withErrors(map[string]*huma.Response{
			"200": {
				Description: "Source metadata.",
				Headers:     map[string]*huma.Param{"ETag": {Description: "Strong entity tag.", Schema: &huma.Schema{Type: "string"}}},
				Content:     map[string]*huma.MediaType{"application/json": {Schema: ref(schemaInfoResponse)}},
			},
			"304": {Description: "Not modified: If-None-Match matched the ETag."},
		}),
		ExternalDocs: &huma.ExternalDocs{URL: urlDialectsDoc + "#33-info-options"},
		Extensions: map[string]any{
			"x-xi-listener":     ListenerPublic,
			"x-xi-dialect":      imgproxy.Name,
			"x-xi-path-grammar": imgproxyGrammar,
			"x-xi-info-options": map[string]any{"$ref": schemaRefPrefix + schemaInfoOptions},
			"x-xi-signing":      signingExtension(),
		},
	}
}

// videoResponses are the responses of a video transformation: the file
// (byte ranges supported), 304, 206 and 416, and the errors.
func videoResponses() map[string]*huma.Response {
	binary := &huma.Schema{Type: "string", ContentEncoding: "binary"}
	return withErrors(map[string]*huma.Response{
		"200": {
			Description: "The transcoded file: video/mp4, video/webm, audio/mp4, audio/mpeg, audio/ogg, audio/flac, audio/wav, " +
				"or image/png and image/webp for a waveform picture.",
			Headers: map[string]*huma.Param{
				"ETag":          {Description: "Strong entity tag (also the If-Range validator).", Schema: &huma.Schema{Type: "string"}},
				"Accept-Ranges": {Description: "bytes", Schema: &huma.Schema{Type: "string"}},
				"Cache-Control": {Description: "XI_CACHE_HEADERS_* policy, or the URL's expires.", Schema: &huma.Schema{Type: "string"}},
			},
			Content: map[string]*huma.MediaType{
				"video/*": {Schema: binary}, "audio/*": {Schema: binary}, "image/png": {Schema: binary}, "image/webp": {Schema: binary},
			},
		},
		"206": {Description: "Partial content: the Range of the file (multipart/byteranges for several ranges)."},
		"304": {Description: "Not modified: If-None-Match matched the ETag."},
		"416": {Description: "Range not satisfiable."},
	})
}

func videoOperation() *huma.Operation {
	return &huma.Operation{
		OperationID: "getImgproxyVideo", Method: http.MethodGet, Path: "/video/{signature}/{options}/{source}", Tags: []string{"video"},
		Summary: "Transform a video or its audio (imgproxy-style /video, WP-122, WP-123)",
		Description: "Trims, resizes, crops, rotates and transcodes a video into MP4 or WebM, its audio into M4A, MP3, Opus, Ogg, " +
			"FLAC or WAV, or draws its audio as a PNG or WebP waveform. {options} is zero or more video options separated by / " +
			"(rs:fill:640:360/vs:1/vd:5), typed by the ImgproxyVideoOptions schema; the source extension (@webm) picks the container. " +
			"The signature covers \"/video\" + the path after it, so image signatures are not valid here. Needs " +
			"XI_VIDEO_TRANSFORM_ENABLED (404 otherwise, and on XI_ROLE=image); unsigned requests are 403 unless " +
			"XI_VIDEO_TRANSFORM_UNSIGNED_POLICY=allow. Supports Range.",
		Parameters:   imgproxyParams("options", "Video options joined by /, each name:arg:arg (ImgproxyVideoOptions).", "rs:fit:640:0/vd:5", schemaVideoOptions),
		Responses:    videoResponses(),
		ExternalDocs: &huma.ExternalDocs{URL: urlDialectsDoc + "#38-video-transformations-wp-122-wp-123"},
		Extensions: map[string]any{
			"x-xi-listener":      ListenerPublic,
			"x-xi-dialect":       imgproxy.Name,
			"x-xi-path-grammar":  imgproxyGrammar,
			"x-xi-video-options": map[string]any{"$ref": schemaRefPrefix + schemaVideoOptions},
			"x-xi-signing":       videoSigningExtension(),
		},
	}
}

func videoSigningExtension() map[string]any {
	return map[string]any{
		"scheme": "imgproxy",
		"description": "HMAC-SHA256(key, salt ‖ \"/video\" ‖ path after the signature), url-safe base64 without padding, " +
			"optionally truncated to XI_SIGNING_SIZE bytes; any value (conventionally unsafe) when signing is off.",
		"docs": urlDialectsDoc + "#38-video-transformations-wp-122-wp-123",
	}
}

func queryVideoOperation() *huma.Operation {
	opts := imgproxy.VideoOptions()
	ps := []*huma.Param{{
		Name: "url", In: "query", Required: true, Example: "https://example.com/clip.mp4",
		Description: "Source URL or xi://<source>/<key>.", Schema: &huma.Schema{Type: "string"},
	}}
	for i := range opts {
		if opts[i].Shared {
			continue
		}
		ps = append(ps, queryParam(opts[i].Name, opts[i].Doc+" Arguments joined by , or : ("+opts[i].Syntax+")."))
	}
	o := queryParam("o", "A /video option path (rs:fill:640:360/vd:5).")
	o.Extensions = map[string]any{"x-xi-options-schema": map[string]any{"$ref": schemaRefPrefix + schemaVideoOptions}}
	ps = append(ps, o, queryParam("xi_sig", "Signature over \"/video\" + the query (sign.XIMessage)."))
	return &huma.Operation{
		OperationID: "getQueryVideo", Method: http.MethodGet, Path: "/q/video", Tags: []string{"video"},
		Summary: "Transform a video (query dialect)",
		Description: "The /video options as query keys (aliases too), in query order, mounted at /q/ when " +
			"XI_DIALECTS_QUERY_ENABLED=true; fit, gravity, crop and flip also take the native spellings.",
		Parameters:   ps,
		Responses:    videoResponses(),
		ExternalDocs: &huma.ExternalDocs{URL: urlDialectsDoc + "#4-query-dialect-native--imaginary"},
		Extensions:   map[string]any{"x-xi-listener": ListenerPublic, "x-xi-dialect": "query"},
	}
}

func imgproxyOperation() *huma.Operation {
	return &huma.Operation{
		OperationID: "getImgproxyImage", Method: http.MethodGet, Path: "/{signature}/{processingOptions}/{source}", Tags: []string{"image"},
		Summary: "Process an image (imgproxy dialect, the default)",
		Description: "imgproxy URL: {processingOptions} is zero or more options separated by / (rs:fill:300:400:0/g:sm), " +
			"with /-/ chaining pipelines, typed by the ImgproxyProcessingOptions schema (x-xi-processing-options). " +
			"Grammar: x-xi-path-grammar; the mount prefix is configurable (XI_DIALECTS_IMGPROXY_PREFIX).",
		Parameters: imgproxyParams("processingOptions", "Processing options joined by /, each name:arg:arg (ImgproxyProcessingOptions).",
			"rs:fill:300:400:0/g:sm", schemaProcessingOptions),
		Responses:    imageResponses(),
		ExternalDocs: &huma.ExternalDocs{URL: urlDialectsDoc + "#3-imgproxy-canonical"},
		Extensions: map[string]any{
			"x-xi-listener":           ListenerPublic,
			"x-xi-dialect":            imgproxy.Name,
			"x-xi-path-grammar":       imgproxyGrammar,
			"x-xi-processing-options": map[string]any{"$ref": schemaRefPrefix + schemaProcessingOptions},
			"x-xi-signing":            signingExtension(),
		},
	}
}

// queryKeys are the native query dialect keys (URL-DIALECTS §4,
// internal/dialect/query native.go) with their descriptions.
func queryKeys() [][2]string {
	return [][2]string{
		{"url", "Source URL or xi://<source>/<key> (required)."},
		{"width", "Width in pixels."},
		{"w", "Alias of width."},
		{"height", "Height in pixels."},
		{"h", "Alias of height."},
		{"fit", "fit, contain, inside, fill, cover, fill-down, force, stretch, outside or auto."},
		{"gravity", "Compass word, smart, entropy or fp:x:y."},
		{"g", "Alias of gravity."},
		{"crop", "w,h or w,h,x,y."},
		{"dpr", "Device pixel ratio."},
		{"enlarge", "Allow upscaling (bool)."},
		{"format", "Output format."},
		{"f", "Alias of format."},
		{"quality", "0..100."},
		{"q", "Alias of quality."},
		{"lossless", "Lossless encoding (bool)."},
		{"progressive", "Progressive/interlaced encoding (bool)."},
		{"blur", "Gaussian sigma."},
		{"sharpen", "Sharpen sigma."},
		{"pixelate", "Block size."},
		{"rotate", "Multiple of 90."},
		{"rot", "Alias of rotate."},
		{"flip", "h, v or hv."},
		{"bg", "Background: hex or r,g,b."},
		{"strip", "Strip metadata (bool)."},
		{"trim", "Trim threshold (and colour, ...)."},
		{"pad", "Padding, CSS shorthand with , or :."},
		{"preset", "Preset names."},
		{"page", "First page."},
		{"cb", "Cache buster."},
		{"filename", "Content-Disposition file name."},
		{"attachment", "Download as attachment (bool)."},
		{"expires", "Unix expiry."},
		{"o", "An imgproxy option path (rs:fill:300:200/g:sm), typed by ImgproxyProcessingOptions; makes the dialect complete."},
		{"sign", "imaginary signature over the path and the sorted query."},
		{"xi_sig", "xi signature (imgproxy keys) over the path and the query."},
	}
}

func queryOperation() *huma.Operation {
	keys := queryKeys()
	ps := make([]*huma.Param, 0, len(keys))
	for _, kv := range keys {
		p := queryParam(kv[0], kv[1])
		if kv[0] == "url" {
			p.Required = true
			p.Example = "https://example.com/a.jpg"
		}
		if kv[0] == "o" {
			p.Extensions = map[string]any{"x-xi-options-schema": map[string]any{"$ref": schemaRefPrefix + schemaProcessingOptions}}
		}
		ps = append(ps, p)
	}
	return &huma.Operation{
		OperationID: "getQueryImage", Method: http.MethodGet, Path: "/q", Tags: []string{"image"},
		Summary: "Process an image (query dialect)",
		Description: "Native query dialect, mounted at /q/ when XI_DIALECTS_QUERY_ENABLED=true. Keys apply in query order " +
			"through the imgproxy option parser, so later keys override earlier ones.",
		Parameters:   ps,
		Responses:    imageResponses(),
		ExternalDocs: &huma.ExternalDocs{URL: urlDialectsDoc + "#4-query-dialect-native--imaginary"},
		Extensions:   map[string]any{"x-xi-listener": ListenerPublic, "x-xi-dialect": "query"},
	}
}

func imaginaryOperation() *huma.Operation {
	params := []string{
		"url", "width", "height", "areawidth", "areaheight", "quality", "compression", "type", "force", "rotate", "embed",
		"norotation", "noprofile", "stripmeta", "flip", "flop", "extend", "background", "gravity", "top", "left", "factor",
		"dpi", "interlace", "aspectratio", "palette", "sigma", "minampl", "text", "font", "textwidth", "opacity", "margin",
		"color", "noreplicate", "image", "nocrop", "colorspace", "operations", "sign", "xi_sig",
	}
	ps := make([]*huma.Param, 0, 1+len(params))
	ps = append(ps, &huma.Param{
		Name: "endpoint", In: "path", Required: true, Description: "imaginary operation.",
		Schema: &huma.Schema{Type: "string", Enum: []any{
			"resize", "enlarge", "extract", "crop", "smartcrop", "rotate", "autorotate", "flip", "flop", "thumbnail",
			"zoom", "convert", "fit", "blur", "watermark", "watermarkimage", "info", "pipeline",
		}},
	})
	for _, k := range params {
		p := queryParam(k, "imaginary parameter "+k+" (URL-DIALECTS §4).")
		if k == "url" {
			p.Required, p.Description = true, "Source URL or xi://<source>/<key>."
		}
		ps = append(ps, p)
	}
	return &huma.Operation{
		OperationID: "getImaginaryImage", Method: http.MethodGet, Path: "/q/{endpoint}", Tags: []string{"image"},
		Summary:      "Process an image (imaginary endpoints, query dialect)",
		Description:  "imaginary's GET API under the query dialect mount. POST uploads and the file parameter are not supported.",
		Parameters:   ps,
		Responses:    imageResponses(),
		ExternalDocs: &huma.ExternalDocs{URL: urlDialectsDoc + "#4-query-dialect-native--imaginary"},
		Extensions:   map[string]any{"x-xi-listener": ListenerPublic, "x-xi-dialect": "query"},
	}
}

func imagekitOperation() *huma.Operation {
	return &huma.Operation{
		OperationID: "getImagekitImage", Method: http.MethodGet, Path: "/imagekit/{path}", Tags: []string{"image"},
		Summary: "Process an image (ImageKit dialect)",
		Description: "ImageKit URL under its mount (XI_DIALECTS_IMAGEKIT_ENABLED, prefix /imagekit/ or host-bound): " +
			"transformations in a tr:… path segment or the tr query parameter.",
		Parameters: []*huma.Param{
			pathParam("path", "[tr:<transformations>/]<source path>, relative to the mount's source.", "tr:w-300,h-200/photos/a.jpg", nil),
			queryParam("tr", "Transformations, e.g. w-300,h-200:rt-90 (chained with :)."),
			queryParam("ik-s", "Signature: hex HMAC-SHA1 of the URL relative to the endpoint and ik-t."),
			queryParam("ik-t", "Signature expiry, unix seconds."),
		},
		Responses:    imageResponses(),
		ExternalDocs: &huma.ExternalDocs{URL: urlDialectsDoc + "#7-imagekit"},
		Extensions:   map[string]any{"x-xi-listener": ListenerPublic, "x-xi-dialect": "imagekit"},
	}
}

// dialects lists every URL dialect with its default mount and its
// URL-DIALECTS section; the ones without an operation are documented only
// there. Every dialect is served by this build (status "served"); a dialect
// that is parsed but not mounted would say "planned".
func dialects() []map[string]any {
	d := func(name, prefix, anchor string) map[string]any {
		return map[string]any{"name": name, "prefix": prefix, "status": "served", "docs": urlDialectsDoc + anchor}
	}
	return []map[string]any{
		d("imgproxy", "/", "#3-imgproxy-canonical"),
		d("query", "/q/", "#4-query-dialect-native--imaginary"),
		d("imagekit", "/imagekit/", "#7-imagekit"),
		d("imgix", "/imgix/", "#5-imgix"),
		d("cloudinary", "/cloudinary/", "#6-cloudinary"),
		d("cloudflare", "/cdn-cgi/image/", "#8-cloudflare-images-cdn-cgiimage"),
		d("cloudflare_images", "/cdn-cgi/imagedelivery/", "#81-cloudflare-images-delivery-cdn-cgiimagedelivery-imagedeliverynet"),
		d("bunny", "/bunny/", "#9-bunny-optimizer"),
		d("thumbor", "/thumbor/", "#10-thumbor"),
		d("wsrv", "/wsrv/", "#11-weserv--wsrvnl"),
		d("awssih", "/sih/", "#13-aws-serverless-image-handler-dynamic-image-transformation-for-amazon-cloudfront"),
		d("simple", "provider-native prefixes", "#12-unpic-long-tail-simple-micro-dialects"),
	}
}
