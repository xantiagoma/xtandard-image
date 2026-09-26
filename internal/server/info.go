package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/imageinfo"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
)

// InfoConfig configures the /info endpoint (Kind == transform.KindInfo).
type InfoConfig struct {
	// CacheTTL is the max-age of successful info responses
	// (XI_CACHE_HEADERS_TTL); ≤ 0 sends "no-cache".
	CacheTTL time.Duration
	// DebugHeaders adds X-Xi-Ignored, the options that were parsed but not
	// applied (XI_OBSERVABILITY_DEBUG_HEADERS).
	DebugHeaders bool
	// PlanLimits are the executor's planner limits: the thumbor meta
	// response (InfoSchemaThumbor) plans the request to report its target
	// size and operations.
	PlanLimits plan.Limits
}

// EXIFEntry is one EXIF tag of a source.
type EXIFEntry struct {
	// Name is the canonical tag name ("DateTimeOriginal").
	Name string
	// Title is the human-readable name imgproxy reports ("Date and Time
	// (Original)").
	Title string
	// Value is the human-readable value.
	Value string
}

// SourceInfo is what the info endpoint reports about a source: the probe
// result plus header-only details and metadata. It is the server's view of
// codec.Info; app adapts one to the other and parses IPTC and XMP.
type SourceInfo struct {
	Meta SourceMeta
	// Format and MIMEType are imgproxy's names ("jpeg", "image/jpeg").
	Format   string
	MIMEType string
	// Pages is the number of pages or animation frames (≥ 1).
	Pages int
	// Bands, SampleFormat ("uchar") and Colorspace ("srgb") use libvips
	// names, like imgproxy.
	Bands        int
	SampleFormat string
	Colorspace   string
	// HDR reports a scene-linear source (OpenEXR, Radiance, PFM); /info
	// then answers "hdr": true.
	HDR bool
	// EXIF is in file order.
	EXIF []EXIFEntry
	// IPTC maps dataset titles to values, Photoshop holds
	// {"resolution": {...}} when present, XMP maps namespace prefixes to
	// properties (imgproxy's shapes). nil means the source has none.
	IPTC      map[string]any
	Photoshop map[string]any
	XMP       map[string]map[string]any
	// Pixels holds the pixel-derived fields the request asked for
	// (Inspect's px); nil when it asked for none.
	Pixels *imageinfo.Result
	// Video describes a video source (container, tags, streams); nil for
	// images.
	Video *video.Meta
}

// Inspector reads a source's header and metadata for /info (stage 11 of an
// info request). Like Prober it returns errs.KindUnsupported for unknown or
// disabled formats, KindCorrupt for unreadable headers and
// KindLimitExceeded when a limit is exceeded, and must not retain src.
// When px asks for pixel-derived fields (px.Any()), it also decodes the
// first page and fills SourceInfo.Pixels (WP-63). app implements it with
// codec.Inspect, codec.Sample and imageinfo.Analyze in one vips.Run.
type Inspector interface {
	Inspect(ctx context.Context, src fetch.Buffer, lim ProbeLimits, px *imageinfo.Options) (*SourceInfo, error)
}

// infoContentType is the media type of info responses.
const infoContentType = "application/json"

// maxIgnoredHeader bounds X-Xi-Ignored (option names come from the URL).
const maxIgnoredHeader = 1024

// inspect answers an info request (ARCHITECTURE §2: /info stops after stage
// 11): normalise, resolve, fetch, admission, then the header and metadata
// read instead of the probe. The JSON body is returned as the result's
// output, so image() handles it like an encoded image; respondInfo writes
// it.
func (s *Server) inspect(ctx context.Context, r *http.Request, req *transform.Request, info *requestInfo) (*imageResult, error) {
	if s.deps.Inspector == nil {
		return nil, &notImplementedError{what: "info requests"}
	}
	if req.Info == nil {
		return nil, errs.New(errs.KindInternal, "server: info request without info options")
	}

	// 6. Normalise: defaults and validation, so the canonical key (and the
	// ETag) only holds effective values.
	t := s.stage(info, stageNormalize)
	req.ApplyDefaults(s.cfg.Defaults)
	err := req.Validate()
	lim := s.limits(&req.Limits)
	t.done()
	if err != nil {
		return nil, err
	}

	// 5. Resolve the source against the allowlist.
	t = s.stage(info, stageResolve)
	key, err := s.deps.Resolver.Resolve(req.Source)
	t.done()
	if err != nil {
		return nil, err
	}
	info.source = key.Source

	// 9. Fetch (coalesced with concurrent processing requests for the same
	// source).
	t = s.stage(info, stageFetch)
	fr, err := s.fetch(ctx, key, source.FetchOptions{MaxBytes: lim.maxSrcBytes, Header: r.Header}, req.Source.Hashsum)
	t.done()
	if err != nil {
		return nil, err
	}
	defer fr.Release()

	// 10. Admission: header reads run libvips, so they share the workers.
	t = s.stage(info, stageAdmission)
	release, err := s.deps.Admitter.Acquire(ctx, 0)
	t.done()
	if err != nil {
		return nil, err
	}
	defer release()

	// 11. Header and metadata, plus the pixel-derived fields when asked
	// for (decoded in the same vips.Run).
	t = s.stage(info, stageProbe)
	si, err := s.inspectSource(ctx, fr.Buffer, &lim.probe, pixelOptions(req.Info))
	t.done()
	if err != nil {
		return nil, err
	}

	body, err := s.infoResponse(req, si, fr.Bytes())
	if err != nil {
		return nil, err
	}
	info.format = "json"
	info.width, info.height = orientedSize(&si.Meta)
	return &imageResult{
		out: &ProcessOutput{
			Body:        bytesBlob(body),
			ContentType: infoContentType,
			Format:      transform.FormatUnset,
			Width:       info.width,
			Height:      info.height,
		},
		etag:   etag(req.CanonicalKey(), transform.FormatUnset, 0, &fr.Object, fr.Bytes(), s.cfg.ETagBuster),
		object: fr.Object,
		req:    req,
	}, nil
}

// infoResponse renders the info JSON in the schema the request asks for.
// src is the source as fetched: size and hashsums cover these bytes, not
// the sanitised SVG.
func (s *Server) infoResponse(req *transform.Request, si *SourceInfo, src []byte) ([]byte, error) {
	if req.Info.Schema == transform.InfoSchemaThumbor {
		return s.thumborMetaBody(req, si)
	}
	body, err := infoBody(req.Info, si, src)
	if err != nil {
		return nil, errs.Wrapf(errs.KindInternal, err, "server: encode info")
	}
	return body, nil
}

// inspectSource is probe for info requests: SVG is sanitised before
// anything parses it, and the input limits are re-checked on the result.
func (s *Server) inspectSource(ctx context.Context, buf fetch.Buffer, lim *ProbeLimits, px *imageinfo.Options) (*SourceInfo, error) {
	ctx, span := s.deps.Tracer.Start(ctx, "probe")
	defer span.End()

	src, sanitised, err := s.sanitize(buf)
	if err != nil {
		span.SetStatus(codes.Error, errs.KindOf(err).String())
		return nil, err
	}
	defer src.Release()
	si, err := s.deps.Inspector.Inspect(ctx, src, *lim, px)
	switch {
	case err != nil:
	case si == nil:
		err = errs.New(errs.KindInternal, "server: inspector returned no info")
	case px.Any() && si.Pixels == nil:
		err = errs.New(errs.KindInternal, "server: inspector returned no pixel info")
	case si.Meta.Format == transform.FormatSVG && !sanitised:
		err = errs.New(errs.KindUnsupported, "server: inspector found svg the sniffer missed")
	default:
		err = checkMeta(&si.Meta, lim)
	}
	if err != nil {
		span.SetStatus(codes.Error, errs.KindOf(err).String())
		return nil, err
	}
	span.SetAttributes(attribute.String("xi.source_format", si.Format))
	return si, nil
}

// orientedSize is the size of one frame after EXIF orientation: 5–8 swap
// width and height. imgproxy's width and height always account for it.
func orientedSize(m *SourceMeta) (w, h int) {
	if m.Orientation >= 5 && m.Orientation <= 8 {
		return m.Height, m.Width
	}
	return m.Width, m.Height
}

// InfoResponse is imgproxy's /info response body (docs.imgproxy.net
// usage/getting_info). Each section is present exactly when its option is
// on; requested metadata sections the source lacks are empty objects.
// Options imgproxy supports but this build does not compute yet (detection,
// classification) are left out; the dialect lists
// them in Request.Ignored (X-Xi-Ignored). The crop, colour and hash fields
// (WP-63) are InfoProResponse, rendered by info_pro.go.
//
// It is public API: internal/apidoc reflects it into the OpenAPI document
// (ADR 0008), so renaming a field or its JSON name is a breaking change.
// The doc, minimum and maximum tags become schema keywords.
type InfoResponse struct {
	Size         *int                      `json:"size,omitempty"          doc:"Source size in bytes (info option size)."`
	Format       string                    `json:"format,omitempty"        doc:"Source format in imgproxy's names: jpeg (UltraHDR too), png, webp, avif, heic, jxl, gif, tiff, bmp, ico, svg, pdf, psd, ... (info option format)."`
	MIMEType     string                    `json:"mime_type,omitempty"     doc:"Source media type, e.g. image/jpeg; HEIC is image/heif (info option format)."`
	Width        *int                      `json:"width,omitempty"         doc:"Width of one page or frame in pixels, EXIF orientation applied (info option dimensions)."`
	Height       *int                      `json:"height,omitempty"        doc:"Height of one page or frame in pixels, EXIF orientation applied (info option dimensions)."`
	Orientation  *int                      `json:"orientation,omitempty"   doc:"EXIF orientation 1-8, 1 when absent (info option dimensions)."                            maximum:"8" minimum:"1"`
	EXIF         map[string]string         `json:"exif,omitzero"           doc:"EXIF tags keyed by libexif title, with libexif's human-readable values; {} when the source has none (info option exif)."`
	IPTC         map[string]any            `json:"iptc,omitzero"           doc:"IPTC IIM datasets keyed by title, repeatable datasets as arrays; {} when the source has none (info option iptc)."`
	Photoshop    map[string]any            `json:"photoshop,omitzero"      doc:"Photoshop image resources, e.g. resolution, when present (info option iptc)."`
	XMP          map[string]map[string]any `json:"xmp,omitzero"            doc:"XMP properties grouped by namespace prefix; {} when the source has none (info option xmp)."`
	Colorspace   string                    `json:"colorspace,omitempty"    doc:"libvips interpretation name: srgb, b-w, rgb16, cmyk, ... (info option colorspace)."`
	Bands        *int                      `json:"bands,omitempty"         doc:"Number of bands (channels), alpha included (info option bands)."`
	SampleFormat string                    `json:"sample_format,omitempty" doc:"libvips band format name: uchar, ushort, float, ... (info option sample_format)."`
	PagesNumber  *int                      `json:"pages_number,omitempty"  doc:"Number of pages, or frames of an animation (info option pages_number)."               minimum:"1"`
	Alpha        *bool                     `json:"alpha,omitempty"         doc:"Whether the source has an alpha channel (info option alpha)."`
	InfoProResponse
	InfoVideoResponse
	HDR bool `json:"hdr,omitempty" doc:"xtandard-image extension: true for scene-linear sources (OpenEXR, Radiance, PFM), which imgproxy does not read; absent otherwise."`
}

// infoBody renders the response for the options in o; src is the source
// as fetched.
func infoBody(o *transform.InfoOptions, si *SourceInfo, src []byte) ([]byte, error) {
	var out InfoResponse
	if o.Size {
		size := len(src)
		out.Size = &size
	}
	if o.Format {
		out.Format, out.MIMEType = si.Format, si.MIMEType
	}
	if o.Dimensions {
		w, h := orientedSize(&si.Meta)
		orientation := si.Meta.Orientation
		if orientation < 1 || orientation > 8 {
			orientation = 1
		}
		out.Width, out.Height, out.Orientation = &w, &h, &orientation
	}
	if o.EXIF {
		out.EXIF = make(map[string]string, len(si.EXIF))
		for _, e := range si.EXIF {
			k := e.Title
			if k == "" {
				k = e.Name
			}
			if _, dup := out.EXIF[k]; !dup { // IFD0 wins over the thumbnail IFD
				out.EXIF[k] = e.Value
			}
		}
	}
	if o.IPTC {
		out.IPTC = orEmpty(si.IPTC)
		if len(si.Photoshop) > 0 {
			out.Photoshop = si.Photoshop
		}
	}
	if o.XMP {
		out.XMP = si.XMP
		if out.XMP == nil {
			out.XMP = map[string]map[string]any{}
		}
	}
	if o.Colorspace {
		out.Colorspace = si.Colorspace
	}
	if o.Bands {
		out.Bands = &si.Bands
	}
	if o.SampleFormat {
		out.SampleFormat = si.SampleFormat
	}
	if o.PagesNumber {
		pages := max(si.Pages, 1)
		out.PagesNumber = &pages
	}
	if o.Alpha {
		out.Alpha = &si.Meta.HasAlpha
	}
	out.InfoProResponse = infoPro(o, si, src)
	out.InfoVideoResponse = infoVideo(o, si.Video)
	out.HDR = si.HDR
	return json.Marshal(&out)
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// respondInfo is stage 14 for info requests: JSON with the processing
// ETag scheme, Cache-Control from XI_CACHE_HEADERS_TTL, 304 on a matching
// If-None-Match, and X-Xi-Ignored when debug headers are on.
//
// WP-34b hook: once processing responses get their full header set
// (Cache-Control per request, expires, Last-Modified), move the shared
// parts here onto the same helper.
func (s *Server) respondInfo(w http.ResponseWriter, r *http.Request, res *imageResult) {
	h := w.Header()
	h.Set("ETag", res.etag)
	h.Set("Cache-Control", s.infoCacheControl())
	h.Set("X-Content-Type-Options", "nosniff")
	if s.cfg.Info.DebugHeaders && len(res.req.Ignored) > 0 {
		h.Set("X-Xi-Ignored", ignoredHeader(res.req.Ignored))
	}
	if etagMatches(r.Header.Get("If-None-Match"), res.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body := res.out.Body.Bytes()
	h.Set("Content-Type", res.out.ContentType)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body) //nolint:gosec // G705: JSON built by encoding/json with an explicit Content-Type and nosniff
	}
}

// infoCacheControl is imgproxy's "max-age=<ttl>, public".
func (s *Server) infoCacheControl() string {
	ttl := int64(s.cfg.Info.CacheTTL / time.Second)
	if ttl <= 0 {
		return "no-cache"
	}
	return "max-age=" + strconv.FormatInt(ttl, 10) + ", public"
}

// ignoredHeader renders Request.Ignored as "option (reason), ...", cut at
// maxIgnoredHeader bytes.
func ignoredHeader(list []transform.Ignored) string {
	var b strings.Builder
	for i, ig := range list {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(ig.Option)
		b.WriteString(" (")
		b.WriteString(ig.Reason)
		b.WriteString(")")
		if b.Len() > maxIgnoredHeader {
			break
		}
	}
	out := b.String()
	if len(out) > maxIgnoredHeader {
		out = out[:maxIgnoredHeader]
	}
	return out
}

// etagMatches implements the weak comparison If-None-Match uses (RFC 9110
// §13.1.2).
func etagMatches(header, etag string) bool {
	if header == "" {
		return false
	}
	want := strings.TrimPrefix(etag, "W/")
	for part := range strings.SplitSeq(header, ",") {
		part = strings.TrimSpace(part)
		if part == "*" || strings.TrimPrefix(part, "W/") == want {
			return true
		}
	}
	return false
}

// bytesBlob is a Blob over Go memory (the JSON body).
type bytesBlob []byte

func (b bytesBlob) Bytes() []byte { return b }
func (bytesBlob) Release()        {}
