package playground

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/obs/logging"
)

// maxListEntries caps one api/sources/<name>/list answer.
const maxListEntries = 500

// maxTranslateBody bounds a translate request body.
const maxTranslateBody = 64 << 10

// FSRoot is an fs source api/sources/<name>/list may browse.
type FSRoot struct {
	// Root is the source's jail directory (XI_SOURCES_<NAME>_ROOT).
	Root string
	// Prefix is its key prefix jail (XI_SOURCES_<NAME>_PREFIX), "" or
	// "dir/.../".
	Prefix string
}

// ListEntry is one file or directory of a list answer.
type ListEntry struct {
	Name string `json:"name" doc:"File or directory name."`
	// Key is the key to use in xi://<source>/<key> (relative to the
	// source, without its prefix jail); directories end with "/".
	Key  string `json:"key" doc:"Key relative to the source (xi://<source>/<key>); directories end with /."`
	Dir  bool   `json:"dir,omitempty" doc:"A directory: list it with prefix=key."`
	Size int64  `json:"size,omitempty" doc:"File size in bytes."`
}

// ListResponse is the body of GET api/sources/<name>/list.
type ListResponse struct {
	Source    string      `json:"source" doc:"Source name."`
	Prefix    string      `json:"prefix" doc:"The directory listed, relative to the source; empty for its root."`
	Entries   []ListEntry `json:"entries" doc:"Directories first, then files, each sorted by name."`
	Truncated bool        `json:"truncated,omitempty" doc:"More than 500 entries: only the first 500 are listed."`
}

// TranslateRequest is the body of POST api/translate: a URL of a mount
// (as for api/sign, without a signature) or a canonical request.
type TranslateRequest struct {
	Mount string `json:"mount,omitempty" doc:"Mount ID from api/meta; with path and query, the URL to translate."`
	Path  string `json:"path,omitempty" maxLength:"8192" doc:"Escaped path after the mount prefix, without a signature segment."`
	Query string `json:"query,omitempty" maxLength:"8192" doc:"Raw query without ?."`
	// Request is a transform.Request as JSON, instead of a URL.
	Request map[string]any `json:"request,omitempty" doc:"A canonical transform.Request (as in the translate response), instead of a URL."`
}

// Translation is what a Translator returns: the canonical request and one
// unsigned URL per dialect.
type Translation struct {
	Canonical any
	Targets   []TranslateTarget
}

// TranslateResponse is the body of a successful translate request.
type TranslateResponse struct {
	Canonical any               `json:"canonical" doc:"The canonical transform.Request the URL parses into."`
	Targets   []TranslateTarget `json:"targets" doc:"The same transform in every dialect, in a fixed order."`
}

// TranslateTarget is the transform in one dialect.
type TranslateTarget struct {
	Dialect string `json:"dialect" doc:"Dialect name (query is the imgproxy options in query form)."`
	Label   string `json:"label" doc:"Human-readable dialect name."`
	// Mount is the mount serving the dialect here; empty when the
	// dialect is not mounted (URL uses its default prefix).
	Mount string `json:"mount,omitempty" doc:"Mount ID serving this dialect here; empty when it is not mounted (the URL uses the default prefix)."`
	// Path and Query are relative to the mount prefix, unsigned.
	Path  string `json:"path,omitempty" doc:"Escaped path after the prefix, unsigned."`
	Query string `json:"query,omitempty" doc:"Raw query, unsigned."`
	// URL is the path and query on the public listener, signed when the
	// mount has a key.
	URL    string `json:"url,omitempty" doc:"Path and query on the public listener (prepend the base URL), signed when this server holds the mount's key."`
	Signed bool   `json:"signed,omitempty" doc:"URL carries a signature."`
	// Unsupported lists fields of the canonical request the dialect
	// cannot express; Approximated those it expresses differently.
	Unsupported  []string `json:"unsupported,omitempty" doc:"Canonical fields this dialect cannot express (lost in translation)."`
	Approximated []string `json:"approximated,omitempty" doc:"Canonical fields this dialect expresses with a different value."`
	// Error is set when the transform cannot be written in the dialect
	// at all (for example its source form).
	Error string `json:"error,omitempty" doc:"Why the transform cannot be written in this dialect at all."`
	// Alternates are the URL on the mount's other prefixes.
	Alternates []TranslateAlternate `json:"alternates,omitempty" doc:"The same URL on the mount's other prefixes (mounts with several prefixes)."`
}

// TranslateAlternate is a target's URL on another prefix of its mount.
type TranslateAlternate struct {
	Prefix string `json:"prefix" doc:"The mount prefix."`
	URL    string `json:"url" doc:"Path and query on the public listener, signed like url."`
}

// Translator translates a URL or canonical request into every dialect
// (internal/playground/translate).
type Translator interface {
	Translate(req *TranslateRequest) (*Translation, error)
}

// decodeJSON reads a JSON body of at most limit bytes into v, answering
// the error itself; it reports whether v was filled.
func decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		writeJSONError(w, http.StatusUnsupportedMediaType, "content type must be application/json")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return false
		}
		writeJSONError(w, http.StatusBadRequest, "invalid json body")
		return false
	}
	return true
}

// translate answers POST api/translate.
func (h *handler) translate(w http.ResponseWriter, r *http.Request) {
	var req TranslateRequest
	if !decodeJSON(w, r, maxTranslateBody, &req) {
		return
	}
	if (req.Request == nil) == (req.Mount == "") {
		writeJSONError(w, http.StatusBadRequest, "give either mount and path, or request")
		return
	}
	if req.Mount != "" {
		if _, ok := h.mounts[req.Mount]; !ok {
			writeJSONError(w, http.StatusBadRequest, "unknown mount")
			return
		}
		if err := checkPart("path", req.Path); err != nil {
			writeJSONError(w, http.StatusBadRequest, "path must be escaped (no spaces, control characters or #)")
			return
		}
		if err := checkPart("query", req.Query); err != nil {
			writeJSONError(w, http.StatusBadRequest, "query must be escaped (no spaces, control characters or #)")
			return
		}
	}
	tr, err := h.translator.Translate(&req)
	if err != nil {
		if e, ok := errors.AsType[*badRequestError](err); ok {
			writeJSONError(w, http.StatusBadRequest, e.msg)
			return
		}
		if e, ok := errors.AsType[*errs.Error](err); ok {
			// A dialect error: its public message is client-safe.
			msg := e.Public
			if msg == "" {
				msg = errs.PublicMessage(errs.KindOf(err))
			}
			writeJSONError(w, http.StatusBadRequest, "cannot translate the url: "+msg)
			return
		}
		h.logger.LogAttrs(r.Context(), slog.LevelError, "playground translate failed", logging.Err(err))
		writeJSONError(w, http.StatusInternalServerError, "translation failed")
		return
	}
	res := TranslateResponse{Canonical: tr.Canonical, Targets: tr.Targets}
	for i := range res.Targets {
		t := &res.Targets[i]
		if t.Error != "" || t.Path == "" {
			continue
		}
		m, ok := h.mounts[t.Mount]
		if !ok {
			t.URL = h.pathPrefix + withQuery(t.Path, t.Query)
			continue
		}
		s, err := SignURL(m, h.signers[m.ID], t.Path, t.Query)
		if err != nil {
			t.Error = "cannot sign this url"
			continue
		}
		t.URL, t.Signed = h.pathPrefix+s.URL, s.Signed
		for _, p := range m.Prefixes {
			alt, ok := m.at(p)
			if !ok || p == m.Prefix {
				continue
			}
			if s, err := SignURL(alt, h.signers[m.ID], t.Path, t.Query); err == nil {
				t.Alternates = append(t.Alternates, TranslateAlternate{Prefix: p, URL: h.pathPrefix + s.URL})
			}
		}
	}
	writeJSON(w, http.StatusOK, res)
}

func withQuery(p, q string) string {
	if q == "" {
		return p
	}
	return p + "?" + q
}

// upload answers POST api/upload: the raw body is stored for UploadTTL and
// its playground:// source returned.
func (h *handler) upload(w http.ResponseWriter, r *http.Request) {
	if h.store == nil || !h.store.UploadsEnabled() {
		writeJSONError(w, http.StatusNotFound, "uploads are disabled (XI_PLAYGROUND_UPLOADS)")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.store.MaxBytes()))
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "file larger than XI_LIMITS_MAX_SRC_BYTES")
			return
		}
		writeJSONError(w, http.StatusBadRequest, "cannot read the upload")
		return
	}
	u, err := h.store.Put(body)
	if err != nil {
		if e, ok := errors.AsType[*badRequestError](err); ok {
			writeJSONError(w, http.StatusBadRequest, e.msg)
			return
		}
		writeJSONError(w, http.StatusRequestEntityTooLarge, "file larger than XI_LIMITS_MAX_SRC_BYTES")
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

// list answers GET api/sources/{name}/list?prefix=: one directory of an fs
// source.
func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	fr, ok := h.fsRoots[name]
	if !ok {
		writeJSONError(w, http.StatusNotFound, "no such fs source")
		return
	}
	prefix := strings.Trim(r.URL.Query().Get("prefix"), "/")
	if len(prefix) > maxURLPart || !validListPrefix(prefix) {
		writeJSONError(w, http.StatusBadRequest, "prefix must be a relative directory without . or .. segments")
		return
	}
	root, err := os.OpenRoot(fr.Root)
	if err != nil {
		h.logger.LogAttrs(r.Context(), slog.LevelWarn, "playground list cannot open root",
			slog.String("source", name), logging.Err(err))
		writeJSONError(w, http.StatusInternalServerError, "cannot open the source")
		return
	}
	defer func() { _ = root.Close() }()
	dir := strings.TrimSuffix(fr.Prefix, "/")
	if prefix != "" {
		dir = path.Join(dir, prefix)
	}
	if dir == "" {
		dir = "."
	}
	entries, err := fs.ReadDir(root.FS(), dir)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "no such directory")
		return
	}
	res := ListResponse{Source: name, Prefix: prefix, Entries: []ListEntry{}}
	keyPrefix := ""
	if prefix != "" {
		keyPrefix = prefix + "/"
	}
	for _, e := range entries {
		if len(res.Entries) == maxListEntries {
			res.Truncated = true
			break
		}
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		le := ListEntry{Name: e.Name(), Key: keyPrefix + e.Name()}
		switch {
		case e.IsDir():
			le.Dir, le.Key = true, le.Key+"/"
		case e.Type().IsRegular():
			if info, err := e.Info(); err == nil {
				le.Size = info.Size()
			}
		default:
			continue // symlinks, devices, sockets
		}
		res.Entries = append(res.Entries, le)
	}
	slices.SortStableFunc(res.Entries, func(a, b ListEntry) int {
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	writeJSON(w, http.StatusOK, res)
}

// validListPrefix accepts "" and slash-separated names without "." or ".."
// segments, backslashes or control characters.
func validListPrefix(p string) bool {
	if p == "" {
		return true
	}
	for seg := range strings.SplitSeq(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	for i := range len(p) {
		if c := p[i]; c < 0x20 || c == 0x7f || c == '\\' {
			return false
		}
	}
	return true
}
