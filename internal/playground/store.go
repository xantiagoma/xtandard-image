package playground

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

//go:embed samples
var sampleFiles embed.FS

// galleryJSON is the curated list of public sample files (real-world files
// per input format and its common variants: colour types, bit depths,
// animation, camera RAW families; WP-100), fetched through the server's
// http sources like any other URL.
//
//go:embed samples.json
var galleryJSON []byte

// GallerySample is one public sample file of the gallery.
type GallerySample struct {
	Format  string `json:"format" doc:"Input format the file exercises."`
	Name    string `json:"name" doc:"Description."`
	URL     string `json:"url" doc:"Public URL, fetched through an http source."`
	Bytes   int64  `json:"bytes" doc:"File size when the list was made."`
	Licence string `json:"licence" doc:"SPDX licence of the file."`
	Source  string `json:"source" doc:"Where the file comes from; the attribution for CC-BY files."`
	Notes   string `json:"notes,omitempty" doc:"What the file shows or the expected outcome."`
	// Variant names the configuration the file exercises within its
	// format (colour type, bit depth, compression, camera family, ...).
	Variant string `json:"variant,omitempty" doc:"Configuration within the format: colour type, bit depth, compression, animation, camera model (\"cmyk 16-bit\", \"Canon CR3 C-RAW\")."`
	// Tags are lower-case keywords for filtering (colour type, depth,
	// alpha, animated, hdr, large, gap, ...).
	Tags []string `json:"tags,omitempty" doc:"Lower-case keywords: colour type, bit depth, alpha, animated, multi-page, hdr, icc, camera brand; \"large\" marks files over 20 MB and \"gap\" formats not decoded yet (expected 422)."`
	// Allowed reports whether a configured http source (or dev mode)
	// allows the URL; the page disables the others.
	Allowed bool `json:"allowed" doc:"A configured http source (or dev mode) allows the URL."`
}

// Gallery returns the public samples with Allowed set by allowed (nil
// allows none).
func Gallery(allowed func(url string) bool) ([]GallerySample, error) {
	var g []GallerySample
	if err := json.Unmarshal(galleryJSON, &g); err != nil {
		return nil, fmt.Errorf("playground: samples.json: %w", err)
	}
	for i := range g {
		g[i].Allowed = allowed != nil && allowed(g[i].URL)
	}
	return g, nil
}

// Store areas: the first path segment of a playground:// source.
const (
	areaSamples = "samples"
	areaUploads = "uploads"
)

// UploadTTL is how long an upload stays in the store.
const UploadTTL = 10 * time.Minute

// Store defaults.
const (
	// maxUploads bounds the number of uploads held at once.
	maxUploads = 64
	// minStoreBytes is the least total size the store keeps (several
	// uploads of the largest allowed size may exceed it).
	minStoreBytes = 256 << 20
)

// sampleTypeName is the source type the store reports.
const sampleTypeName = "playground"

// Sample is a built-in sample image, served as playground://samples/<file>.
type Sample struct {
	// Name is the file name under samples/.
	Name string `json:"name" doc:"File name (playground://samples/<name>)."`
	// Label is a human-readable description.
	Label string `json:"label" doc:"Description."`
	// Kind groups the samples in the gallery.
	Kind string `json:"kind" enum:"photo,alpha,animated,raw,document,video,vector,detection" doc:"Gallery group."`
	// Source is the source string to use in URLs.
	Source string `json:"source" doc:"Source string (playground://samples/<name>)."`
	// Full reports that the sample needs the full image (video decoding
	// or detection models).
	Full bool `json:"full,omitempty" doc:"Needs the full image (FFmpeg or the detection models)."`
}

// Samples lists the embedded samples (copies of testdata/fixtures, all CC0
// or public domain, see testdata/LICENSES.md) in gallery order.
func Samples() []Sample {
	s := func(name, label, kind string, full bool) Sample {
		return Sample{Name: name, Label: label, Kind: kind, Source: "playground://" + areaSamples + "/" + name, Full: full}
	}
	return []Sample{
		s("photo.jpg", "JPEG photo, 320×240", "photo", false),
		s("alpha.png", "PNG with graded alpha, 128×96", "alpha", false),
		s("animated.gif", "Animated GIF, 6 frames", "animated", false),
		s("logo.svg", "SVG logo", "vector", false),
		s("raw.dng", "Camera RAW (DNG), 192×128", "raw", false),
		s("document.pdf", "PDF, 3 pages", "document", false),
		s("video.mp4", "Video (AV1 in MP4), 2 s", "video", true),
		s("faces.jpg", "Apollo 11 crew: three faces (NASA, public domain)", "detection", true),
		s("people-dogs.jpg", "Astronaut with two dogs (NASA, public domain)", "detection", true),
		s("cat.jpg", "Cat on a laptop (CC0)", "detection", true),
	}
}

// withRender marks an api/render request: only those may read the store
// (and the server resolves playground:// only for them), so the store
// stays invisible on the public listener.
func withRender(ctx context.Context) context.Context { return source.WithPlaygroundRequest(ctx) }

func isRender(ctx context.Context) bool { return source.IsPlaygroundRequest(ctx) }

// StoreOptions configures NewStore.
type StoreOptions struct {
	// Uploads enables POST api/upload (XI_PLAYGROUND_UPLOADS).
	Uploads bool
	// MaxBytes bounds one upload (XI_LIMITS_MAX_SRC_BYTES).
	MaxBytes int64
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Store is the playground's in-memory source (playground://): the
// embedded samples and, when enabled, uploads that expire after
// UploadTTL. It implements source.Source under
// source.PlaygroundSourceName and is safe for concurrent use.
type Store struct {
	uploads  bool
	maxBytes int64
	maxTotal int64
	now      func() time.Time

	mu    sync.Mutex
	items map[string]*upload // id → upload
	order []string           // ids, oldest first
	total int64
}

type upload struct {
	body        []byte
	contentType string
	etag        string
	created     time.Time
}

// Upload describes a stored upload.
type Upload struct {
	Source      string    `json:"source" doc:"Source string for URLs (playground://uploads/<id>)."`
	Size        int64     `json:"size" doc:"Bytes stored."`
	ContentType string    `json:"content_type" doc:"Content type sniffed from the bytes."`
	Expires     time.Time `json:"expires" doc:"When the upload is dropped."`
}

var _ source.Source = (*Store)(nil)

// NewStore returns a store.
func NewStore(o StoreOptions) *Store {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &Store{
		uploads: o.Uploads, maxBytes: o.MaxBytes, maxTotal: max(minStoreBytes, o.MaxBytes),
		now: now, items: map[string]*upload{},
	}
}

// Name implements source.Source.
func (*Store) Name() string { return source.PlaygroundSourceName }

// Type implements source.Source.
func (*Store) Type() string { return sampleTypeName }

// UploadsEnabled reports whether uploads are accepted.
func (s *Store) UploadsEnabled() bool { return s.uploads }

// MaxBytes is the largest upload accepted.
func (s *Store) MaxBytes() int64 { return s.maxBytes }

// errNotStored is the store's not-found error: also what a request from
// outside api/render sees, so the store's content is not probed.
var errNotStored = errors.New("playground: no such file")

// Fetch implements source.Source: key.Path is "<area>/<key>".
func (s *Store) Fetch(ctx context.Context, key source.Key, opt source.FetchOptions) (*source.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	notFound := errs.Wrap(errs.KindNotFound, errNotStored)
	if !isRender(ctx) {
		return nil, notFound
	}
	area, name, _ := strings.Cut(key.Path, "/")
	var (
		body  []byte
		ctype string
		etag  string
	)
	switch area {
	case areaSamples:
		b, err := fs.ReadFile(sampleFiles, path.Join(areaSamples, name))
		if err != nil || strings.Contains(name, "/") {
			return nil, notFound
		}
		body, ctype, etag = b, contentType(b, name), `"sample-`+name+`"`
	case areaUploads:
		u := s.get(name)
		if u == nil {
			return nil, notFound
		}
		body, ctype, etag = u.body, u.contentType, u.etag
	default:
		return nil, notFound
	}
	if opt.MaxBytes > 0 && int64(len(body)) > opt.MaxBytes {
		return nil, errs.Newf(errs.KindSourceTooLarge, "playground file of %d bytes", len(body))
	}
	obj := &source.Object{Size: int64(len(body)), ContentType: ctype, ETag: etag}
	if opt.IfNoneMatch != "" && opt.IfNoneMatch == etag {
		obj.NotModified = true
		return obj, nil
	}
	obj.Body = io.NopCloser(bytes.NewReader(body))
	return obj, nil
}

// contentType sniffs b; SVG and the formats http.DetectContentType does not
// know are left to the server's own sniffing.
func contentType(b []byte, name string) string {
	if strings.HasSuffix(name, ".svg") {
		return "image/svg+xml"
	}
	ct := http.DetectContentType(b)
	if strings.HasPrefix(ct, "text/") {
		return "application/octet-stream"
	}
	return ct
}

// get returns the live upload id, dropping expired ones.
func (s *Store) get(id string) *upload {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	return s.items[id]
}

// expire drops expired uploads; s.mu is held.
func (s *Store) expire() {
	now := s.now()
	for len(s.order) > 0 {
		u := s.items[s.order[0]]
		if u != nil && now.Sub(u.created) < UploadTTL {
			return
		}
		s.drop(s.order[0])
	}
}

// drop removes id; s.mu is held.
func (s *Store) drop(id string) {
	if u := s.items[id]; u != nil {
		s.total -= int64(len(u.body))
		delete(s.items, id)
	}
	if i := slices.Index(s.order, id); i >= 0 {
		s.order = slices.Delete(s.order, i, i+1)
	}
}

// Put stores body and returns its source string. It evicts the oldest
// uploads to stay within the store's bounds.
func (s *Store) Put(body []byte) (Upload, error) {
	if !s.uploads {
		return Upload{}, errors.New("playground: uploads disabled")
	}
	if len(body) == 0 {
		return Upload{}, badRequest("empty upload")
	}
	if int64(len(body)) > s.maxBytes {
		return Upload{}, errs.Newf(errs.KindSourceTooLarge, "upload of %d bytes", len(body))
	}
	var raw [16]byte
	_, _ = rand.Read(raw[:]) // crypto/rand.Read never fails (Go 1.24)
	id := hex.EncodeToString(raw[:])
	sum := sha256.Sum256(body)
	u := &upload{
		body: bytes.Clone(body), contentType: contentType(body, ""),
		etag: `"` + hex.EncodeToString(sum[:8]) + `"`, created: s.now(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	for len(s.order) > 0 && (len(s.order) >= maxUploads || s.total+int64(len(body)) > s.maxTotal) {
		s.drop(s.order[0])
	}
	s.items[id] = u
	s.order = append(s.order, id)
	s.total += int64(len(body))
	return Upload{
		Source: "playground://" + areaUploads + "/" + id, Size: int64(len(body)),
		ContentType: u.contentType, Expires: u.created.Add(UploadTTL).UTC(),
	}, nil
}
