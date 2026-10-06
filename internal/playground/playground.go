// Package playground serves the embedded playground UI (WP-80): a page
// that builds URLs for the enabled dialect mounts from the option
// catalogue, signs them on the server and shows the source next to the
// result with its response headers.
//
// The page, its script and its stylesheet are plain files embedded in the
// binary; there is no build step and nothing is loaded from another
// origin, so the page works air-gapped under Content-Security-Policy
// default-src 'self'. Routes, relative to Config.Path:
//
//	GET  /                          the page (Cache-Control: no-store)
//	GET  /assets/<version>/<f>      the ES modules and the stylesheet (immutable; version is their hash)
//	GET  /api/meta                  Meta as JSON (no key material)
//	POST /api/sign                  SignRequest → SignResponse, signed with the server's keys
//	GET  /api/render?u=<url>        runs Deps.Image on a public path and query in-process (ranges=1: with Range, WP-128)
//	POST /api/translate             TranslateRequest → the transform in every dialect (WP-94)
//	POST /api/upload                a file for playground://uploads/<id> (XI_PLAYGROUND_UPLOADS)
//	GET  /api/sources/<n>/list      one directory of an fs source (WP-94)
//	POST /api/probe                 ProbeRequest → MediaInfo of a source (WP-127)
//	POST /api/probe/result          a media body → its MediaInfo (WP-127)
//
// app mounts the handler on the admin listener, and on the public listener
// only with XI_PLAYGROUND_PUBLIC. The package never sees key bytes: it holds
// *sign.Signer values, which only produce signatures.
package playground

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

//go:embed assets
var assets embed.FS

// DefaultPath is the default mount path (XI_PLAYGROUND_PATH).
const DefaultPath = "/_playground"

// csp allows only same-origin scripts, styles and requests; result images
// and videos are shown from blob: URLs of same-origin responses.
const csp = "default-src 'self'; img-src 'self' blob: data:; media-src 'self' blob:; object-src 'none'; base-uri 'none'; " +
	"form-action 'none'; frame-ancestors 'none'"

// renderCSP applies to rendered images opened directly (an SVG result must
// not run script in the admin origin).
const renderCSP = "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; sandbox"

// Config configures the handler.
type Config struct {
	// Path is the mount path requests arrive with ("/_playground"; on the
	// public listener, XI_SERVER_PATH_PREFIX + XI_PLAYGROUND_PATH): starts
	// with "/", no trailing "/".
	Path string
}

// Deps are the handler's collaborators.
type Deps struct {
	// Meta is served at api/meta and names the mounts api/sign accepts.
	Meta *Meta
	// Signers signs URLs of a mount, keyed by Mount.ID; a mount without an
	// entry gets unsigned URLs.
	Signers map[string]*sign.Signer
	// Image is the public listener's handler; api/render runs it
	// in-process.
	Image http.Handler
	// Logger receives panics and unexpected failures.
	Logger *slog.Logger
	// Store is the playground:// source (samples and uploads); nil
	// disables api/upload.
	Store *Store
	// FSRoots are the fs sources api/sources/<name>/list browses.
	FSRoots map[string]FSRoot
	// Translator answers api/translate; nil answers 404.
	Translator Translator
	// EncryptPassword encrypts a document password into a pdf_password
	// token (crypt.Cipher.Encrypt, the XI_ENCRYPTION_KEYS key); nil makes
	// api/sign refuse SignRequest.PDFPassword.
	EncryptPassword func(password []byte) (string, error)
	// Prober describes media files for the video mode (api/probe); nil
	// answers 404 (no video transformations or no FFmpeg).
	Prober MediaProber
}

// handler is the playground's handler tree.
type handler struct {
	base       string
	pathPrefix string // XI_SERVER_PATH_PREFIX without a trailing /
	meta       []byte
	mounts     map[string]*Mount
	signers    map[string]*sign.Signer
	image      http.Handler
	logger     *slog.Logger
	store      *Store
	fsRoots    map[string]FSRoot
	translator Translator
	encrypt    func(password []byte) (string, error)
	prober     *prober
	page       []byte
	version    string
	files      map[string]asset
}

type asset struct {
	body        []byte
	contentType string
}

// New validates cfg and deps and returns the handler for every path under
// cfg.Path.
func New(cfg Config, deps *Deps) (http.Handler, error) {
	p := cfg.Path
	if len(p) < 2 || p[0] != '/' || strings.HasSuffix(p, "/") || strings.ContainsAny(p, "{}") {
		return nil, fmt.Errorf("playground: path %q must start with / and not end with /", p)
	}
	if deps == nil || deps.Meta == nil || deps.Image == nil || deps.Logger == nil {
		return nil, errors.New("playground: meta, image and logger are required")
	}
	meta, err := json.Marshal(deps.Meta)
	if err != nil {
		return nil, fmt.Errorf("playground: meta: %w", err)
	}
	h := &handler{
		base:       p,
		pathPrefix: strings.TrimSuffix(deps.Meta.PathPrefix, "/"),
		meta:       meta,
		mounts:     make(map[string]*Mount, len(deps.Meta.Mounts)),
		signers:    deps.Signers,
		image:      deps.Image,
		logger:     deps.Logger,
		store:      deps.Store,
		fsRoots:    deps.FSRoots,
		translator: deps.Translator,
		encrypt:    deps.EncryptPassword,
		files:      map[string]asset{},
	}
	if deps.Prober != nil {
		h.prober = newProber(deps.Prober, deps.Logger)
	}
	for i := range deps.Meta.Mounts {
		m := &deps.Meta.Mounts[i]
		h.mounts[m.ID] = m
	}
	if err := h.loadAssets(); err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+p+"/{$}", h.servePage)
	mux.HandleFunc("GET "+p+"/assets/{version}/{name}", h.serveAsset)
	mux.HandleFunc("GET "+p+"/api/meta", h.serveMeta)
	mux.Handle("POST "+p+"/api/sign", http.NewCrossOriginProtection().Handler(http.HandlerFunc(h.sign)))
	mux.HandleFunc("GET "+p+"/api/render", h.render)
	mux.HandleFunc("GET "+p+"/api/stream/{part...}", h.stream)
	if h.translator != nil {
		mux.Handle("POST "+p+"/api/translate", http.NewCrossOriginProtection().Handler(http.HandlerFunc(h.translate)))
	}
	if h.store != nil && h.store.UploadsEnabled() {
		mux.Handle("POST "+p+"/api/upload", http.NewCrossOriginProtection().Handler(http.HandlerFunc(h.upload)))
	}
	mux.HandleFunc("GET "+p+"/api/sources/{name}/list", h.list)
	if h.prober != nil {
		mux.Handle("POST "+p+"/api/probe", http.NewCrossOriginProtection().Handler(http.HandlerFunc(h.prober.source)))
		mux.Handle("POST "+p+"/api/probe/result", http.NewCrossOriginProtection().Handler(http.HandlerFunc(h.prober.result)))
	}
	// Anything else under p is the ServeMux 404 (or 405 for a known path
	// with another method).
	mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, p+"/", http.StatusTemporaryRedirect)
	})
	return h.recoverer(securityHeaders(mux)), nil
}

// loadAssets reads the embedded files (every .js module and .css
// stylesheet under assets/), derives the asset version from their hash and
// fills the page template. The modules import each other with relative
// URLs, so they all live under one versioned directory.
func (h *handler) loadAssets() error {
	sum := sha256.New()
	entries, err := fs.ReadDir(assets, "assets")
	if err != nil {
		return fmt.Errorf("playground: %w", err)
	}
	for _, e := range entries { // sorted by name: the hash is deterministic
		name := e.Name()
		var contentType string
		switch {
		case strings.HasSuffix(name, ".js"):
			contentType = "text/javascript; charset=utf-8"
		case strings.HasSuffix(name, ".css"):
			contentType = "text/css; charset=utf-8"
		default:
			continue
		}
		b, err := fs.ReadFile(assets, "assets/"+name)
		if err != nil {
			return fmt.Errorf("playground: %w", err)
		}
		sum.Write([]byte(name))
		sum.Write(b)
		h.files[name] = asset{body: b, contentType: contentType}
	}
	h.version = hex.EncodeToString(sum.Sum(nil))[:12]
	page, err := fs.ReadFile(assets, "assets/index.html")
	if err != nil {
		return fmt.Errorf("playground: %w", err)
	}
	h.page = []byte(strings.ReplaceAll(string(page), "__ASSETS__", "assets/"+h.version))
	return nil
}

// securityHeaders sets the headers every playground response carries.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// recoverer answers 500 on a panic, re-panicking http.ErrAbortHandler.
func (h *handler) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(v)
			}
			h.logger.LogAttrs(r.Context(), slog.LevelError, "playground panic",
				slog.String("panic", fmt.Sprint(v)), slog.String("path", r.URL.Path))
			writeJSONError(w, http.StatusInternalServerError, "internal error")
		}()
		next.ServeHTTP(w, r)
	})
}

func (h *handler) servePage(w http.ResponseWriter, r *http.Request) {
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(h.page)
	}
}

func (h *handler) serveAsset(w http.ResponseWriter, r *http.Request) {
	a, ok := h.files[r.PathValue("name")]
	if !ok || r.PathValue("version") != h.version {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", a.contentType)
	// The URL carries the content hash, so it never changes.
	hd.Set("Cache-Control", "public, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(a.body)
	}
}

func (h *handler) serveMeta(w http.ResponseWriter, r *http.Request) {
	hd := w.Header()
	hd.Set("Content-Type", "application/json")
	hd.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(h.meta)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status, b = http.StatusInternalServerError, []byte(`{"error":"internal error"}`)
	}
	hd := w.Header()
	hd.Set("Content-Type", "application/json")
	hd.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorResponse{Error: msg})
}
