// Package negotiate chooses the output format of an image response from the
// request, the Accept header, client hints, config and source metadata
// (docs/FORMATS.md §6, docs/ARCHITECTURE.md §2 step 7 and §9.1).
//
// It is pure Go and imports only transform and errs. The server calls, in
// order:
//
//	hints := negotiate.ApplyClientHints(req, negotiate.ParseClientHints(r.Header), &cfg, negotiate.HintsAll)
//	req.ApplyDefaults(...)                    // then Validate
//	accept := negotiate.ParseAcceptHeader(r.Header)
//	d, err := negotiate.Decide(req, accept, &cfg, meta) // after probe
//	w.Header().Set("Vary", (d.Vary | hints.Vary).Header()) // when non-empty
//
// Decision order (first match wins):
//
//  1. Info requests answer JSON.
//  2. Raw requests and skip-processing formats stream the source unmodified
//     (before the explicit format, like imgproxy).
//  3. An explicit output format in the URL (not auto/best) wins; no Vary.
//  4. SVG passthrough (FORMATS §6.2 SVG row).
//  5. The §6.2 matrix row for the content: modern formats (AVIF, WebP, JPEG
//     XL) the client explicitly accepts and config enables, ordered by
//     enforce flag, then Accept q-value, then the matrix column order; then
//     the first compatible entry of Config.Preferred.
//
// The M2 best-format analysis plugs in through two typed hooks: the probe's
// content analysis fills SourceMeta.Class (ContentUnknown is treated as a
// photo until then), and a FormatBest request gets Decision.Candidates (at
// most three, matrix order) with ReasonBest; the M2 encoder tries them and
// keeps the smallest, the M1 encoder uses Decision.Format (the first one).
package negotiate

import (
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Config is the negotiation part of the config tree; app maps config.Config
// into it (CONFIG.md "Processing, formats, limits").
type Config struct {
	AutoAVIF, AutoWebP, AutoJXL          bool // XI_FORMATS_AUTO_*: negotiate the format from Accept
	EnforceAVIF, EnforceWebP, EnforceJXL bool // XI_FORMATS_ENFORCE_*: implies auto; beats q-values and the content preference
	StrictAccept                         bool // XI_FORMATS_STRICT_ACCEPT: 406 when no candidate is acceptable

	// Preferred is the fallback order when no modern format is negotiated
	// (XI_FORMATS_PREFERRED). The first entry compatible with the content
	// (alpha, animation, lossless for graphics) is used.
	Preferred []transform.Format
	// SkipProcessing lists source formats streamed unmodified
	// (XI_FORMATS_SKIP_PROCESSING, imgproxy IMGPROXY_SKIP_PROCESSING_FORMATS).
	SkipProcessing []transform.Format

	SVGPassthrough bool  // XI_FORMATS_SVG_PASSTHROUGH
	AVIFAnimation  bool  // the encoder can write AVIF sequences (not in M1)
	AVIFMaxPixels  int64 // XI_FORMATS_AVIF_MAX_PIXELS; 0 = no guard
	JXLMaxPixels   int64 // JPEG XL cost guard (FORMATS §6.1: 32 MP); 0 = no guard

	EnableClientHints    bool // XI_CLIENT_HINTS_ENABLED: honour hints on every request (imgproxy compat)
	SaveDataQualityDelta int  // XI_FORMATS_SAVE_DATA_QUALITY_DELTA
}

// DefaultConfig returns the documented defaults (CONFIG.md, FORMATS §6).
func DefaultConfig() Config {
	return Config{
		AutoAVIF:             true,
		AutoWebP:             true,
		Preferred:            []transform.Format{transform.FormatJPEG, transform.FormatPNG, transform.FormatGIF},
		SVGPassthrough:       true,
		AVIFMaxPixels:        16_000_000,
		JXLMaxPixels:         32_000_000,
		SaveDataQualityDelta: -15,
	}
}

// Validate checks the config. Preferred must be non-empty and hold only
// raster formats; SkipProcessing must hold concrete formats.
func (c *Config) Validate() error {
	var errList []error
	if len(c.Preferred) == 0 {
		errList = append(errList, errors.New("negotiate: preferred formats must not be empty"))
	}
	for _, f := range c.Preferred {
		if !rasterEncodable(f) {
			errList = append(errList, fmt.Errorf("negotiate: preferred format %q is not an encodable raster format", f))
		}
	}
	for _, f := range c.SkipProcessing {
		if _, ok := mediaTypeOf(f); !ok {
			errList = append(errList, fmt.Errorf("negotiate: skip-processing format %q is not a concrete format", f))
		}
	}
	if c.AVIFMaxPixels < 0 || c.JXLMaxPixels < 0 {
		errList = append(errList, errors.New("negotiate: max pixels must not be negative"))
	}
	if c.SaveDataQualityDelta < -100 || c.SaveDataQualityDelta > 0 {
		errList = append(errList, errors.New("negotiate: save-data quality delta must be in -100..0"))
	}
	return errors.Join(errList...)
}

// ContentClass is the content-analysis verdict (FORMATS §6.1 point 3).
type ContentClass uint8

// ContentClass values. ContentUnknown is what M1 probes produce; Decide treats
// it as ContentPhoto. The M2 analysis (Sobel complexity + unique colours)
// fills ContentPhoto or ContentGraphic.
const (
	ContentUnknown ContentClass = iota
	ContentPhoto
	ContentGraphic
)

// String returns the class name for logs.
func (c ContentClass) String() string {
	switch c {
	case ContentUnknown:
		return "unknown"
	case ContentPhoto:
		return "photo"
	case ContentGraphic:
		return "graphic"
	}
	return "ContentClass(" + strconv.Itoa(int(c)) + ")"
}

// SourceMeta is what Decide needs from the probe and planner.
type SourceMeta struct {
	// Format is the sniffed source format; FormatUnset for sources that have
	// no transform.Format (PDF, PSD, RAW, JPEG 2000, …), which are never
	// kept or passed through.
	Format transform.Format
	// HasAlpha reports whether the output carries alpha: source alpha or an
	// operation that adds it, minus flattening onto a background. The
	// planner computes it.
	HasAlpha   bool
	IsAnimated bool
	FrameCount int   // > 1 also means animated
	Pixels     int64 // output pixels (all frames) for the cost guards; 0 = unknown
	// NeedsRaster is true when the request applies raster operations, which
	// rules out SVG passthrough.
	NeedsRaster bool
	// Class is the M2 content-analysis hook; see ContentClass.
	Class ContentClass
	// Colors is the unique-colour count when known (M2); 1..256 makes a PNG
	// fallback for graphics quantised. 0 = unknown.
	Colors int
}

// Reason says which rule produced a Decision, for logs and debug headers.
type Reason uint8

// Reason values.
const (
	ReasonFallback       Reason = iota // no modern format negotiated: Config.Preferred
	ReasonExplicit                     // format fixed by the URL, or info JSON
	ReasonSkipProcessing               // source streamed unmodified
	ReasonSVGPassthrough               // SVG source served as SVG
	ReasonAccept                       // modern format chosen from Accept and auto flags
	ReasonEnforced                     // modern format chosen by an enforce flag
	ReasonBest                         // FormatBest: Candidates set, Format is the first (M2 hook)
)

// String returns the reason name for logs.
func (r Reason) String() string {
	switch r {
	case ReasonFallback:
		return "fallback"
	case ReasonExplicit:
		return "explicit"
	case ReasonSkipProcessing:
		return "skip_processing"
	case ReasonSVGPassthrough:
		return "svg_passthrough"
	case ReasonAccept:
		return "accept"
	case ReasonEnforced:
		return "enforced"
	case ReasonBest:
		return "best"
	}
	return "Reason(" + strconv.Itoa(int(r)) + ")"
}

// maxCandidates is the size of the best-format candidate set (FORMATS §6.3).
const maxCandidates = 3

// Decision is the negotiated output.
type Decision struct {
	Format transform.Format
	// Lossless is the matrix's encoding hint (graphics rows). The encoder
	// uses it only when Output.Lossless is nil.
	Lossless bool
	// Quantize asks for a palette PNG (graphics with ≤ 256 colours).
	Quantize bool
	// Passthrough means the source bytes are sent unmodified.
	Passthrough bool
	// Vary lists the request headers this decision depended on.
	Vary   VarySet
	Reason Reason

	candidates [maxCandidates]transform.Format
	nCand      uint8
}

// ContentType returns the Content-Type of the decided format.
func (d *Decision) ContentType() string { return ContentType(d.Format) }

// Candidates returns the best-format candidate set in preference order. It is
// empty unless Reason is ReasonBest.
func (d *Decision) Candidates() []transform.Format {
	if d.nCand == 0 {
		return nil
	}
	return d.candidates[:d.nCand]
}

// Decide negotiates the output format. It returns an error of kind
// errs.KindNotAcceptable only when cfg.StrictAccept is on and no candidate is
// acceptable. req must have passed ApplyDefaults (it is not modified).
func Decide(req *transform.Request, accept AcceptList, cfg *Config, meta SourceMeta) (Decision, error) {
	if req.Kind == transform.KindInfo {
		return Decision{Format: transform.FormatJSON, Reason: ReasonExplicit}, nil
	}
	if req.Kind == transform.KindRaw || (meta.Format != transform.FormatUnset &&
		(slices.Contains(cfg.SkipProcessing, meta.Format) || slices.Contains(req.Delivery.SkipProcessing, meta.Format))) {
		return Decision{Format: meta.Format, Passthrough: true, Reason: ReasonSkipProcessing}, nil
	}
	if f := req.Output.Format; f != transform.FormatUnset && f != transform.FormatBest {
		return Decision{Format: f, Reason: ReasonExplicit}, nil
	}

	n := negotiation{accept: accept, cfg: cfg}
	if meta.Format == transform.FormatSVG && cfg.SVGPassthrough && !meta.NeedsRaster &&
		n.acceptable(transform.FormatSVG) {
		return Decision{Format: transform.FormatSVG, Passthrough: true, Vary: n.vary, Reason: ReasonSVGPassthrough}, nil
	}

	n.content = classify(req, &meta)
	n.addModern(&meta)
	n.addFallbacks()

	d, ok := n.pick(req.Output.Format == transform.FormatBest)
	if !ok {
		return Decision{Vary: n.vary}, errs.New(errs.KindNotAcceptable, "negotiate: no acceptable output format")
	}
	if d.Format == transform.FormatPNG && n.content == rowGraphic && meta.Colors > 0 && meta.Colors <= 256 {
		d.Quantize = true
	}
	return d, nil
}

// row is the FORMATS §6.2 matrix row.
type row uint8

const (
	rowPhotoOpaque row = iota
	rowPhotoAlpha
	rowGraphic
	rowAnimated
)

func classify(req *transform.Request, meta *SourceMeta) row {
	animated := meta.IsAnimated || meta.FrameCount > 1
	for i := range req.Pipelines {
		if req.Pipelines[i].DisableAnimation {
			animated = false
		}
	}
	switch {
	case animated:
		return rowAnimated
	case meta.Class == ContentGraphic:
		return rowGraphic
	case meta.HasAlpha:
		return rowPhotoAlpha
	default:
		return rowPhotoOpaque
	}
}

// candidate is one entry of the ordered candidate list.
type candidate struct {
	format   transform.Format
	lossless bool
	enforced bool
	q        int // Accept weight; modern candidates only
	modern   bool
}

// negotiation is the per-call scratch state. It lives on the stack: the
// candidate list has a fixed capacity (3 modern + preferred list capped).
type negotiation struct {
	accept  AcceptList
	cfg     *Config
	content row
	vary    VarySet
	cands   [maxCandidates + 8]candidate
	n       int
}

func (n *negotiation) push(c candidate) {
	if n.n == len(n.cands) {
		return
	}
	for i := range n.n {
		if n.cands[i].format == c.format {
			return
		}
	}
	n.cands[n.n] = c
	n.n++
}

// acceptable reports whether the client accepts f (any range, wildcards
// included). It marks Accept as consulted.
func (n *negotiation) acceptable(f transform.Format) bool {
	n.vary |= VaryAccept
	mt, _ := mediaTypeOf(f)
	return n.accept.Quality(mt.typ, mt.sub) > 0
}

// matrix returns the modern-format columns of a row in column order
// (FORMATS §6.2: AVIF, else WebP, else JPEG XL) with the lossless flag.
func (n *negotiation) matrix() (fs [3]transform.Format, lossless bool, count int) {
	switch n.content {
	case rowAnimated:
		// WebP animated; AVIF sequences when the encoder supports them
		// (then preferred, like the AVIF column); JPEG XL → GIF column.
		if n.cfg.AVIFAnimation {
			return [3]transform.Format{transform.FormatAVIF, transform.FormatWebP}, false, 2
		}
		return [3]transform.Format{transform.FormatWebP}, false, 1
	case rowGraphic:
		// AVIF lossless is larger than WebP lossless: AVIF column → WebP.
		// AVIF appears only when enforced (see addModern).
		return [3]transform.Format{transform.FormatWebP, transform.FormatJXL, transform.FormatAVIF}, true, 3
	default:
		return [3]transform.Format{transform.FormatAVIF, transform.FormatWebP, transform.FormatJXL}, false, 3
	}
}

func (c *Config) flags(f transform.Format) (auto, enforce bool) {
	switch f {
	case transform.FormatAVIF:
		return c.AutoAVIF || c.EnforceAVIF, c.EnforceAVIF
	case transform.FormatWebP:
		return c.AutoWebP || c.EnforceWebP, c.EnforceWebP
	case transform.FormatJXL:
		return c.AutoJXL || c.EnforceJXL, c.EnforceJXL
	default:
		return false, false
	}
}

func (n *negotiation) addModern(meta *SourceMeta) {
	fs, lossless, count := n.matrix()
	start := n.n
	for _, f := range fs[:count] {
		auto, enforce := n.cfg.flags(f)
		if !auto {
			continue
		}
		if n.content == rowGraphic && f == transform.FormatAVIF && !enforce {
			continue
		}
		if meta.Pixels > 0 {
			if f == transform.FormatAVIF && n.cfg.AVIFMaxPixels > 0 && meta.Pixels > n.cfg.AVIFMaxPixels {
				continue
			}
			if f == transform.FormatJXL && n.cfg.JXLMaxPixels > 0 && meta.Pixels > n.cfg.JXLMaxPixels {
				continue
			}
		}
		n.vary |= VaryAccept
		mt, _ := mediaTypeOf(f)
		q := n.accept.Explicit(mt.typ, mt.sub)
		if q == 0 {
			continue
		}
		n.push(candidate{format: f, lossless: lossless, enforced: enforce, q: q, modern: true})
	}
	// Stable sort keeps the matrix column order among equals.
	slices.SortStableFunc(n.cands[start:n.n], func(a, b candidate) int {
		if a.enforced != b.enforced {
			if a.enforced {
				return -1
			}
			return 1
		}
		return b.q - a.q
	})
}

func (n *negotiation) addFallbacks() {
	needAlpha := n.content == rowPhotoAlpha
	needAnim := n.content == rowAnimated
	needLossless := n.content == rowGraphic
	for _, f := range n.cfg.Preferred {
		if (needAlpha && !supportsAlpha(f)) || (needAnim && !n.cfg.supportsAnimation(f)) ||
			(needLossless && !supportsLossless(f)) {
			continue
		}
		n.push(candidate{format: f, lossless: needLossless})
	}
	// Hard defaults when Preferred has nothing compatible (FORMATS §6.2
	// "else" column).
	switch n.content {
	case rowAnimated:
		n.push(candidate{format: transform.FormatGIF})
	case rowPhotoOpaque:
		n.push(candidate{format: transform.FormatJPEG})
	case rowPhotoAlpha, rowGraphic:
		n.push(candidate{format: transform.FormatPNG, lossless: needLossless})
	}
}

func (n *negotiation) pick(best bool) (Decision, bool) {
	cands := n.cands[:n.n]
	if n.cfg.StrictAccept {
		kept := cands[:0]
		for _, c := range cands {
			// Modern candidates were accepted explicitly already.
			if c.modern || n.acceptable(c.format) {
				kept = append(kept, c)
			}
		}
		cands = kept
	}
	if len(cands) == 0 {
		return Decision{}, false
	}
	first := cands[0]
	d := Decision{Format: first.format, Lossless: first.lossless, Vary: n.vary, Reason: ReasonFallback}
	switch {
	case first.enforced:
		d.Reason = ReasonEnforced
	case first.modern:
		d.Reason = ReasonAccept
	}
	if best {
		d.Reason = ReasonBest
		for _, c := range cands[:min(len(cands), maxCandidates)] {
			d.candidates[d.nCand] = c.format
			d.nCand++
		}
	}
	return d, true
}
