package router

import (
	"errors"
	"reflect"
	"slices"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// ErrUnsignedNotAllowed is wrapped by the 403 returned when the unsigned
// policy rejects a request.
var ErrUnsignedNotAllowed = errors.New("router: unsigned request not allowed")

// Restrictions are the allowlists of the restricted policy
// (XI_SECURITY_ALLOWED_WIDTHS, XI_SECURITY_ALLOWED_QUALITIES).
//
// A restricted unsigned request may use only:
//
//   - Request: kind process, one pipeline, Save-Data client hint, any source.
//   - Resize: type, width in Widths, height in Heights, dpr in DPRs, aspect
//     ratio, zoom 1, enlarge, adaptive, kernel. Relative (percent)
//     dimensions and min/max clamps are rejected.
//   - Gravity: compass, smart, entropy, focus point (no face or object
//     detection).
//   - Rotate by 90°, auto-rotate, flip, background, extend with a
//     background fill, page, disable animation.
//   - Output: format, quality / per-format quality / slow-connection
//     quality in Qualities, lossless, progressive, metadata and colour
//     profile stripping, prefer-JPEG-if-opaque.
//   - Delivery: filename, attachment, expires, TTL, immutable, on-error
//     redirect, skip-processing formats, cache tags, presets, response
//     encoding.
//
// Everything else is rejected: info and raw requests, chained pipelines,
// security limit overrides, width/DPR client hints (the hinted value would
// bypass the allowlist), crops, trims, effects, watermarks, fallback image
// URLs, cache busters and cache bypass, auto quality, max bytes and encoder
// tuning. The check is an allowlist: fields added to transform later are
// rejected until they are listed here.
type Restrictions struct {
	// Widths are the allowed pixel widths; 0 (auto) is always allowed.
	Widths []int
	// Heights are the allowed pixel heights; nil means Widths.
	Heights []int
	// DPRs are the allowed device pixel ratios; unset and 1 are always
	// allowed. nil allows only those.
	DPRs []float64
	// Qualities are the allowed qualities; 0 (default) is always allowed.
	Qualities []int
}

type restrictions struct {
	widths, heights, qualities []int
	dprs                       []float64
}

func newRestrictions(o *Restrictions) *restrictions {
	r := &restrictions{
		widths:    slices.Clone(o.Widths),
		heights:   slices.Clone(o.Heights),
		qualities: slices.Clone(o.Qualities),
		dprs:      slices.Clone(o.DPRs),
	}
	if o.Heights == nil {
		r.heights = r.widths
	}
	return r
}

func unsignedError(reason string) error {
	return &errs.Error{
		Kind:   errs.KindForbidden,
		Msg:    reason,
		Public: "unsigned request not allowed",
		Err:    ErrUnsignedNotAllowed,
	}
}

// enforce applies m's unsigned policy to res.
func (rt *Router) enforce(m *Mount, res *dialect.Result) error {
	if res.Signature.Signed() {
		return nil
	}
	switch m.Policy {
	case PolicyAllow:
		return nil
	case PolicyRestricted:
		if reason := rt.restrict.check(res.Request); reason != "" {
			return unsignedError("restricted policy: " + reason)
		}
		return nil
	default: // PolicyDeny; New rejects anything else.
		return unsignedError("unsigned policy deny")
	}
}

// check returns "" when req is within the restricted option set, else the
// first offending option (internal text).
func (r *restrictions) check(req *transform.Request) string {
	switch {
	case req.Kind != transform.KindProcess:
		return "kind " + req.Kind.String()
	case req.Info != nil:
		return "info"
	case req.Limits != (transform.LimitOverrides{}):
		return "security options"
	case req.ClientHints.Width || req.ClientHints.DPR:
		return "width or dpr client hint"
	case len(req.Pipelines) > 1:
		return "chained pipelines"
	}
	if len(req.Pipelines) == 1 {
		if reason := r.checkPipeline(&req.Pipelines[0]); reason != "" {
			return reason
		}
	}
	if reason := r.checkOutput(&req.Output); reason != "" {
		return reason
	}
	return checkDelivery(&req.Delivery)
}

// checkPipeline and the other checks clear the allowed fields of a copy and
// report whatever is left, so unknown fields fail closed.
func (r *restrictions) checkPipeline(orig *transform.Pipeline) string {
	p := *orig
	rs := p.Resize
	switch {
	case rs.Width.Rel != 0 || rs.Height.Rel != 0:
		return "relative dimension"
	case rs.Width.Px != 0 && !slices.Contains(r.widths, rs.Width.Px):
		return "width not allowlisted"
	case rs.Height.Px != 0 && !slices.Contains(r.heights, rs.Height.Px):
		return "height not allowlisted"
	case rs.DPR != 0 && rs.DPR != 1 && !slices.Contains(r.dprs, rs.DPR):
		return "dpr not allowlisted"
	case rs.ZoomX != 0 && rs.ZoomX != 1, rs.ZoomY != 0 && rs.ZoomY != 1:
		return "zoom"
	case p.Gravity.Type >= transform.GravityFace:
		return "detection gravity"
	case p.Extend.Enabled && p.Extend.Mode != transform.ExtendBackground,
		p.ExtendAR.Enabled && p.ExtendAR.Mode != transform.ExtendBackground:
		return "extend mode"
	}
	rest := rs
	rest.Type, rest.Width, rest.Height, rest.DPR = 0, transform.Dim{}, transform.Dim{}, 0
	rest.AspectRatio, rest.ZoomX, rest.ZoomY = 0, 0, 0
	rest.Enlarge, rest.Adaptive, rest.Algorithm = false, false, 0
	if f := firstSet(rest); f != "" {
		return "resize." + f
	}
	p.Resize = transform.Resize{}
	p.Gravity = transform.Gravity{}
	p.Rotate, p.AutoRotate, p.Flip, p.Background = 0, nil, transform.Flip{}, nil
	p.Extend, p.ExtendAR = transform.Extend{}, transform.Extend{}
	p.Page, p.DisableAnimation = nil, false
	if f := firstSet(p); f != "" {
		return "pipeline." + f
	}
	return ""
}

func (r *restrictions) quality(q int) bool {
	return q == 0 || slices.Contains(r.qualities, q)
}

func (r *restrictions) checkOutput(orig *transform.Output) string {
	o := *orig
	if !r.quality(o.Quality) || !r.quality(o.SlowConnectionQuality) {
		return "quality not allowlisted"
	}
	for _, q := range o.FormatQuality {
		if !r.quality(q) {
			return "quality not allowlisted"
		}
	}
	o.Format, o.Quality, o.SlowConnectionQuality, o.FormatQuality = 0, 0, 0, nil
	o.Lossless, o.Progressive, o.PreferJPEGIfOpaque = nil, nil, false
	o.StripMetadata, o.KeepCopyright, o.StripColorProfile = nil, nil, nil
	if f := firstSet(o); f != "" {
		return "output." + f
	}
	return ""
}

func checkDelivery(orig *transform.Delivery) string {
	d := *orig
	d.Filename, d.Attachment, d.Expires, d.TTL, d.Immutable = "", nil, time.Time{}, 0, false
	d.OnErrorRedirect, d.SkipProcessing, d.CacheTags, d.Presets = false, nil, nil, nil
	d.Encoding = 0
	if f := firstSet(d); f != "" {
		return "delivery." + f
	}
	return ""
}

// firstSet returns the name of the first non-zero field of the struct v.
func firstSet(v any) string {
	rv := reflect.ValueOf(v)
	for i := range rv.NumField() {
		if !rv.Field(i).IsZero() {
			return rv.Type().Field(i).Name
		}
	}
	return ""
}
