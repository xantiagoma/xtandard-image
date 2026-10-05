package apidoc

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/xantiagoma/xtandard-image/internal/playground"
)

// Playground schema component names (schemaNamer prefixes the playground
// package's types).
const (
	schemaPlaygroundMeta         = "PlaygroundMeta"
	schemaPlaygroundSignRequest  = "PlaygroundSignRequest"
	schemaPlaygroundSignResponse = "PlaygroundSignResponse"
	schemaPlaygroundError        = "PlaygroundErrorResponse"
	schemaPlaygroundTranslateReq = "PlaygroundTranslateRequest"
	schemaPlaygroundTranslateRes = "PlaygroundTranslateResponse"
	schemaPlaygroundUpload       = "PlaygroundUpload"
	schemaPlaygroundList         = "PlaygroundListResponse"
	schemaPlaygroundProbeReq     = "PlaygroundProbeRequest"
	schemaPlaygroundMediaInfo    = "PlaygroundMediaInfo"
)

// schemaNamer is huma's default namer, with "Playground" in front of the
// playground package's types (Mount, Option, Arg, ... are generic names).
func schemaNamer(t reflect.Type, hint string) string {
	name := huma.DefaultSchemaNamer(t, hint)
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if strings.HasSuffix(t.PkgPath(), "/internal/playground") {
		return "Playground" + name
	}
	return name
}

// playgroundExtensions: the admin listener, and the public one only with
// XI_PLAYGROUND_PUBLIC (the route-coverage test checks the admin mux).
func playgroundExtensions() map[string]any {
	return map[string]any{
		"x-xi-listener": ListenerAdmin,
		"x-xi-config":   "XI_PLAYGROUND_ENABLED; also on the public listener with XI_PLAYGROUND_PUBLIC; path XI_PLAYGROUND_PATH",
	}
}

func playgroundErrors(statuses ...int) map[string]*huma.Response {
	rs := map[string]*huma.Response{}
	for _, s := range statuses {
		rs[strconv.Itoa(s)] = &huma.Response{
			Description: http.StatusText(s) + ".",
			Content:     map[string]*huma.MediaType{"application/json": {Schema: ref(schemaPlaygroundError)}},
		}
	}
	return rs
}

const playgroundPath = playground.DefaultPath

func playgroundPageOperation() *huma.Operation {
	return &huma.Operation{
		OperationID: "getPlayground", Method: http.MethodGet, Path: playgroundPath + "/", Tags: []string{"playground"}, Servers: adminServers(),
		Summary: "Playground page",
		Description: "The playground UI: plain HTML, CSS and JavaScript embedded in the binary, served with " +
			"Content-Security-Policy default-src 'self', X-Frame-Options DENY and Cache-Control no-store. 404 unless XI_PLAYGROUND_ENABLED.",
		Responses: map[string]*huma.Response{
			"200": {Description: "The page.", Content: map[string]*huma.MediaType{"text/html": {Schema: str("HTML page.")}}},
			"404": {Description: "The playground is disabled."},
		},
		Extensions: playgroundExtensions(),
	}
}

func playgroundAssetOperation() *huma.Operation {
	return &huma.Operation{
		OperationID: "getPlaygroundAsset", Method: http.MethodGet, Path: playgroundPath + "/assets/{version}/{name}",
		Tags: []string{"playground"}, Servers: adminServers(),
		Summary:     "Playground scripts and stylesheet",
		Description: "The ES modules (app.js imports the others with relative URLs) and style.css under a content-hash version, with Cache-Control immutable; another version is a 404.",
		Parameters: []*huma.Param{
			pathParam("version", "Content hash of the assets (from the page).", "0123456789ab", nil),
			{
				Name: "name", In: "path", Required: true, Description: "Asset file name: an ES module or the stylesheet (app.js, style.css, ...).",
				Schema: &huma.Schema{Type: "string", Pattern: `^[a-z0-9-]+\.(js|css)$`},
			},
		},
		Responses: map[string]*huma.Response{
			"200": {Description: "The asset.", Content: map[string]*huma.MediaType{
				"text/javascript": {Schema: str("ES module.")},
				"text/css":        {Schema: str("Stylesheet.")},
			}},
			"404": {Description: "Unknown asset or stale version, or the playground is disabled."},
		},
		Extensions: playgroundExtensions(),
	}
}

func playgroundMetaOperation() *huma.Operation {
	return &huma.Operation{
		OperationID: "getPlaygroundMeta", Method: http.MethodGet, Path: playgroundPath + "/api/meta", Tags: []string{"playground"}, Servers: adminServers(),
		Summary: "What the playground can build",
		Description: "Enabled dialect mounts with their signing scheme and grammar, the option catalogues (the imgproxy catalogue " +
			"behind ImgproxyProcessingOptions and the query keys), preset names, named sources (name and type only), the signing mode " +
			"and the output formats. Never contains key material. Cache-Control no-store.",
		Responses: map[string]*huma.Response{
			"200": {Description: "Playground metadata.", Content: map[string]*huma.MediaType{"application/json": {Schema: ref(schemaPlaygroundMeta)}}},
			"404": {Description: "The playground is disabled."},
		},
		Extensions: playgroundExtensions(),
	}
}

func playgroundSignOperation() *huma.Operation {
	rs := playgroundErrors(http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusInternalServerError)
	rs["200"] = &huma.Response{
		Description: "The URL, signed with the server's key for the mount when it holds one.",
		Content:     map[string]*huma.MediaType{"application/json": {Schema: ref(schemaPlaygroundSignResponse)}},
	}
	rs["403"] = &huma.Response{Description: "Cross-origin request rejected (http.CrossOriginProtection)."}
	rs["404"] = &huma.Response{Description: "The playground is disabled."}
	return &huma.Operation{
		OperationID: "postPlaygroundSign", Method: http.MethodPost, Path: playgroundPath + "/api/sign", Tags: []string{"playground"}, Servers: adminServers(),
		Summary: "Sign a URL with the server's keys",
		Description: "Builds the URL of a path (and query) on a mount and signs it with the server's key for that mount's scheme " +
			"(imgproxy path signature, xi_sig or thumbor), so keys never reach the browser. Without a key the URL is returned unsigned " +
			"(imgproxy insecure/, thumbor unsafe/). Anyone who can call this endpoint can sign URLs: keep the playground private. " +
			"Cross-origin requests are rejected; the body is JSON of at most 32 KiB.",
		RequestBody: &huma.RequestBody{
			Required: true,
			Content:  map[string]*huma.MediaType{"application/json": {Schema: ref(schemaPlaygroundSignRequest)}},
		},
		Responses:  rs,
		Extensions: playgroundExtensions(),
	}
}

func playgroundRenderOperation() *huma.Operation {
	return &huma.Operation{
		OperationID: "getPlaygroundRender", Method: http.MethodGet, Path: playgroundPath + "/api/render", Tags: []string{"playground"}, Servers: adminServers(),
		Summary: "Render a public URL in-process",
		Description: "Runs the public listener's handler on u (a path and query on the public listener) and returns its response " +
			"unchanged plus Server-Timing, so the page reads the result and its headers from its own origin. Accept and client hints " +
			"are forwarded; cookies and credentials are not. Every public-listener check applies.",
		Parameters: []*huma.Param{{
			Name: "u", In: "query", Required: true, Description: "Path and query on the public listener, starting with a single /.",
			Schema: &huma.Schema{Type: "string", MaxLength: new(16 << 10)}, Example: "/insecure/rs:fit:300:0/plain/https://example.com/a.jpg",
		}},
		Responses: withErrors(map[string]*huma.Response{
			"200": {
				Description: imageContentDescription,
				Headers: map[string]*huma.Param{
					"Server-Timing": {Description: "Time the public handler took to the first header write.", Schema: &huma.Schema{Type: "string"}},
				},
				Content: map[string]*huma.MediaType{"image/*": {Schema: &huma.Schema{Type: "string", ContentEncoding: "binary"}}},
			},
		}),
		Extensions: playgroundExtensions(),
	}
}

func playgroundTranslateOperation() *huma.Operation {
	rs := playgroundErrors(http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusInternalServerError)
	rs["200"] = &huma.Response{
		Description: "The canonical request and the same transform in every dialect.",
		Content:     map[string]*huma.MediaType{"application/json": {Schema: ref(schemaPlaygroundTranslateRes)}},
	}
	rs["403"] = &huma.Response{Description: "Cross-origin request rejected (http.CrossOriginProtection)."}
	rs["404"] = &huma.Response{Description: "The playground is disabled."}
	return &huma.Operation{
		OperationID: "postPlaygroundTranslate", Method: http.MethodPost, Path: playgroundPath + "/api/translate", Tags: []string{"playground"}, Servers: adminServers(),
		Summary: "Translate a URL into every dialect",
		Description: "Parses a URL of a mount (as for api/sign, without a signature) with that dialect's parser, or takes a canonical " +
			"transform.Request, and writes the transform in imgproxy (path and query form), imgix, Cloudinary, ImageKit, Cloudflare, wsrv, " +
			"thumbor, Bunny and AWS SIH. Each URL is parsed back with its dialect's parser and compared with the canonical request: " +
			"unsupported lists the fields the dialect cannot express, approximated those it writes with a different value. URLs of " +
			"mounted dialects are signed like api/sign; the others use the dialect's default prefix. Cross-origin requests are rejected.",
		RequestBody: &huma.RequestBody{
			Required: true,
			Content:  map[string]*huma.MediaType{"application/json": {Schema: ref(schemaPlaygroundTranslateReq)}},
		},
		Responses:  rs,
		Extensions: playgroundExtensions(),
	}
}

func playgroundUploadOperation() *huma.Operation {
	rs := playgroundErrors(http.StatusBadRequest, http.StatusRequestEntityTooLarge)
	rs["201"] = &huma.Response{
		Description: "The stored file and its playground:// source.",
		Content:     map[string]*huma.MediaType{"application/json": {Schema: ref(schemaPlaygroundUpload)}},
	}
	rs["403"] = &huma.Response{Description: "Cross-origin request rejected (http.CrossOriginProtection)."}
	rs["404"] = &huma.Response{Description: "The playground or its uploads are disabled (XI_PLAYGROUND_UPLOADS)."}
	return &huma.Operation{
		OperationID: "postPlaygroundUpload", Method: http.MethodPost, Path: playgroundPath + "/api/upload", Tags: []string{"playground"}, Servers: adminServers(),
		Summary: "Upload a file for the playground",
		Description: "Stores the raw request body (at most XI_LIMITS_MAX_SRC_BYTES) in memory for 10 minutes and returns its source, " +
			"playground://uploads/<id>. The source resolves only for requests made through api/render; the store keeps at most 64 " +
			"files or 256 MiB and drops the oldest first. Needs XI_PLAYGROUND_UPLOADS (on by default with the playground).",
		RequestBody: &huma.RequestBody{
			Required: true,
			Content:  map[string]*huma.MediaType{"application/octet-stream": {Schema: &huma.Schema{Type: "string", ContentEncoding: "binary"}}},
		},
		Responses:  rs,
		Extensions: playgroundExtensions(),
	}
}

func playgroundListOperation() *huma.Operation {
	rs := playgroundErrors(http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError)
	rs["200"] = &huma.Response{
		Description: "One directory of the source.",
		Content:     map[string]*huma.MediaType{"application/json": {Schema: ref(schemaPlaygroundList)}},
	}
	return &huma.Operation{
		OperationID: "getPlaygroundSourceList", Method: http.MethodGet, Path: playgroundPath + "/api/sources/{name}/list", Tags: []string{"playground"}, Servers: adminServers(),
		Summary: "Browse an fs source",
		Description: "Lists one directory of a named fs source (inside its root and prefix jail, through os.Root): directories first, " +
			"then files, at most 500 entries, dot files skipped. Other source types and unknown names are 404.",
		Parameters: []*huma.Param{
			pathParam("name", "Named source (type fs).", "local", nil),
			{
				Name: "prefix", In: "query", Description: "Directory relative to the source; empty for its root. No . or .. segments.",
				Schema: &huma.Schema{Type: "string", MaxLength: new(8 << 10)}, Example: "photos/2026",
			},
		},
		Responses:  rs,
		Extensions: playgroundExtensions(),
	}
}

func playgroundProbeResponses() map[string]*huma.Response {
	rs := playgroundErrors(http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity,
		http.StatusTooManyRequests, http.StatusInternalServerError)
	rs["200"] = &huma.Response{
		Description: "The media file's container, duration, average bit rate and streams.",
		Content:     map[string]*huma.MediaType{"application/json": {Schema: ref(schemaPlaygroundMediaInfo)}},
	}
	rs["403"] = &huma.Response{Description: "Cross-origin request rejected (http.CrossOriginProtection), or a source the allowlists refuse."}
	rs["404"] = &huma.Response{Description: "The playground is disabled, or video transformations are off (no prober), or the source does not exist."}
	return rs
}

func playgroundProbeOperation() *huma.Operation {
	return &huma.Operation{
		OperationID: "postPlaygroundProbe", Method: http.MethodPost, Path: playgroundPath + "/api/probe", Tags: []string{"playground"}, Servers: adminServers(),
		Summary: "Describe a media source (video mode)",
		Description: "Resolves and fetches a source like a request's (allowlists, the SSRF-safe fetcher, " +
			"XI_VIDEO_TRANSFORM_MAX_INPUT_BYTES; playground:// samples and uploads resolve) and reads its container and stream " +
			"headers with FFmpeg: container, duration, codecs, size, frame rate, sample rate and channels. Unlike /info it reads " +
			"audio files. Only with video transformations on (XI_VIDEO_TRANSFORM_ENABLED); two probes at a time, more answer 429. " +
			"Cross-origin requests are rejected.",
		RequestBody: &huma.RequestBody{
			Required: true,
			Content:  map[string]*huma.MediaType{"application/json": {Schema: ref(schemaPlaygroundProbeReq)}},
		},
		Responses:  playgroundProbeResponses(),
		Extensions: playgroundExtensions(),
	}
}

func playgroundProbeResultOperation() *huma.Operation {
	return &huma.Operation{
		OperationID: "postPlaygroundProbeResult", Method: http.MethodPost, Path: playgroundPath + "/api/probe/result", Tags: []string{"playground"}, Servers: adminServers(),
		Summary: "Describe a media body (video mode)",
		Description: "Reads the container and stream headers of the request body (a result the page fetched, at most " +
			"XI_VIDEO_TRANSFORM_MAX_OUTPUT_BYTES) with FFmpeg, for the Source vs result table. Only with video transformations on; " +
			"two probes at a time, more answer 429. Cross-origin requests are rejected.",
		RequestBody: &huma.RequestBody{
			Required: true,
			Content:  map[string]*huma.MediaType{"application/octet-stream": {Schema: &huma.Schema{Type: "string", ContentEncoding: "binary"}}},
		},
		Responses:  playgroundProbeResponses(),
		Extensions: playgroundExtensions(),
	}
}
