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
		Summary:     "Playground script and stylesheet",
		Description: "app.js and style.css under a content-hash version, with Cache-Control immutable; another version is a 404.",
		Parameters: []*huma.Param{
			pathParam("version", "Content hash of the assets (from the page).", "0123456789ab", nil),
			{
				Name: "name", In: "path", Required: true, Description: "Asset file name.",
				Schema: &huma.Schema{Type: "string", Enum: []any{"app.js", "style.css"}},
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
