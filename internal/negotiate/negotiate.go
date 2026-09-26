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
// Best format (FORMATS §6.3): a FormatBest request gets ReasonBest and the
// candidate lists of both still-image rows (photo and graphic) or of the
// animated row, because the content class is only known once the image is
// processed. The encoder analyses the processed image (Analyze), asks
// Decision.BestCandidates for its class, encodes each candidate and keeps
// the smallest; Decision.Format (the first candidate of the SourceMeta
// class) is what is encoded when the analysis is skipped.
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

// ContentClass values. ContentUnknown is what probes produce; Decide treats
// it as ContentPhoto. Analyze fills ContentPhoto, ContentGraphic or, close to
// the threshold, ContentMixed (Decide treats it as a photo; BestCandidates
// mixes lossy and lossless candidates).
const (
	ContentUnknown ContentClass = iota
	ContentPhoto
	ContentGraphic
	ContentMixed
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
	case ContentMixed:
		return "mixed"
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
	ReasonBest                         // FormatBest: candidate lists set, Format is the first of the SourceMeta class
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

	// Best-format lists (ReasonBest only): the photo and graphic rows of a
	// still image, or the animated row in lossy.
	lossy, lossless candList
	animated        bool
}

// Candidate is one best-format candidate: a format and how to encode it.
type Candidate struct {
	Format transform.Format
	// Lossless asks for a lossless save (graphics rows); it is the
	// encoder's PreferLossless hint.
	Lossless bool
	// Quantize asks for a palette PNG (≤ 256 colours).
	Quantize bool
}

// candList is a fixed-capacity candidate list.
type candList struct {
	c [maxCandidates]Candidate
	n uint8
}

func (l *candList) slice() []Candidate { return l.c[:l.n] }

func (l *candList) push(c Candidate) {
	if int(l.n) == len(l.c) {
		return
	}
	for _, have := range l.c[:l.n] {
		if have.Format == c.Format && have.Lossless == c.Lossless {
			return
		}
	}
	l.c[l.n] = c
	l.n++
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

// BestKey fingerprints the best-format candidate lists ("" unless Reason is
// ReasonBest), e.g. "avif,webp,jpeg|webp~,png~" (~ marks lossless, a
// leading "a:" an animation). Two decisions with equal keys pick the same
// output for the same image, so the key belongs in anything that identifies
// the response before it is encoded (the ETag).
func (d *Decision) BestKey() string {
	if d.Reason != ReasonBest {
		return ""
	}
	b := make([]byte, 0, 48)
	if d.animated {
		b = append(b, "a:"...)
	}
	for i, l := range [2]*candList{&d.lossy, &d.lossless} {
		if i > 0 {
			b = append(b, '|')
		}
		for j, c := range l.slice() {
			if j > 0 {
				b = append(b, ',')
			}
			b = append(b, c.Format.String()...)
			if c.Lossless {
				b = append(b, '~')
			}
		}
	}
	return string(b)
}

// BestCandidates returns the formats to try for a processed image of the
// given class (FORMATS §6.3), at most three, in preference order: the
// lossless graphic row for ContentGraphic, the lossy photo row for photos
// (and ContentUnknown), and both interleaved for ContentMixed. A PNG
// candidate for an image of 1..256 colours is quantised. For an animation
// the candidates are the animated row's (animated formats only), saved
// lossless for graphics. It is nil unless Reason is ReasonBest.
func (d *Decision) BestCandidates(class ContentClass, colors int) []Candidate {
	if d.Reason != ReasonBest {
		return nil
	}
	lossy, lossless := d.lossy, d.lossless
	if d.animated {
		lossless = candList{}
		for _, c := range lossy.slice() {
			c.Lossless = supportsLossless(c.Format)
			lossless.push(c)
		}
	}
	var out candList
	switch class {
	case ContentGraphic:
		out = lossless
		if out.n == 0 {
			out = lossy
		}
	case ContentMixed:
		for i := range maxCandidates {
			if i < int(lossy.n) {
				out.push(lossy.c[i])
			}
			if i < int(lossless.n) {
				out.push(lossless.c[i])
			}
		}
	default:
		out = lossy
		if out.n == 0 {
			out = lossless
		}
	}
	cands := make([]Candidate, out.n)
	copy(cands, out.slice())
	for i := range cands {
		if cands[i].Format == transform.FormatPNG && cands[i].Lossless && colors > 0 && colors <= 256 {
			cands[i].Quantize = true
		}
	}
	return cands
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

	content := classify(req, &meta)
	cands := n.row(content, &meta)
	if len(cands) == 0 {
		return Decision{Vary: n.vary}, errs.New(errs.KindNotAcceptable, "negotiate: no acceptable output format")
	}
	d := decision(cands)
	if d.Format == transform.FormatPNG && content == rowGraphic && meta.Colors > 0 && meta.Colors <= 256 {
		d.Quantize = true
	}
	if req.Output.Format == transform.FormatBest {
		d.Reason = ReasonBest
		for _, c := range cands[:min(len(cands), maxCandidates)] {
			d.candidates[d.nCand] = c.format
			d.nCand++
		}
		primary := bestList(cands, content == rowAnimated)
		switch content {
		case rowAnimated:
			d.lossy, d.animated = primary, true
		case rowGraphic:
			d.lossless = primary
			d.lossy = bestList(n.row(photoRow(&meta), &meta), false)
		default:
			d.lossy = primary
			d.lossless = bestList(n.row(rowGraphic, &meta), false)
		}
	}
	d.Vary = n.vary
	return d, nil
}

// bestList converts the first maxCandidates candidates. GIF is dropped from
// still rows (a 256-colour palette is no candidate for a still photo, and
// graphics have lossless formats); PNG is always a lossless save.
func bestList(cands []candidate, animated bool) candList {
	var l candList
	for _, c := range cands {
		if c.format == transform.FormatGIF && !animated {
			continue
		}
		l.push(Candidate{Format: c.format, Lossless: c.lossless || c.format == transform.FormatPNG})
	}
	return l
}

// photoRow is the photo row of a still image.
func photoRow(meta *SourceMeta) row {
	if meta.HasAlpha {
		return rowPhotoAlpha
	}
	return rowPhotoOpaque
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

// row builds the ordered, Accept-filtered candidate list of one matrix row.
// The result aliases n's buffer until the next call.
func (n *negotiation) row(content row, meta *SourceMeta) []candidate {
	n.n = 0
	n.content = content
	n.addModern(meta)
	n.addFallbacks()
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
	return cands
}

// decision is the Decision for the first of a non-empty candidate list.
func decision(cands []candidate) Decision {
	first := cands[0]
	d := Decision{Format: first.format, Lossless: first.lossless, Reason: ReasonFallback}
	switch {
	case first.enforced:
		d.Reason = ReasonEnforced
	case first.modern:
		d.Reason = ReasonAccept
	}
	return d
}
