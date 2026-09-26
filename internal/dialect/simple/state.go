package simple

import (
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Fit is a provider-neutral fit mode. Tables map their vocabulary onto it
// ("cover", "crop", "fill", "clip", "bounds", ...) and the engine turns it
// into Resize once every parameter is read.
type Fit uint8

// Fit modes, named after CSS object-fit / sharp.
const (
	// FitUnset uses the Spec's default.
	FitUnset Fit = iota
	// FitCover fills the box and crops the overflow at the gravity
	// (ResizeFill).
	FitCover
	// FitInside fits inside the box keeping the aspect ratio (ResizeFit).
	FitInside
	// FitScaleDown is FitInside that never upscales.
	FitScaleDown
	// FitFill stretches to exactly the box (ResizeForce).
	FitFill
	// FitOutside covers the box without cropping (ResizeOutside).
	FitOutside
	// FitPad fits inside the box and pads to its size with the background,
	// placed at the gravity (sharp contain, Contentful pad).
	FitPad
)

// State is the request being built for one URL. Params write sizing
// through its fields and everything else straight into Request and
// Pipeline.
type State struct {
	Request  *transform.Request
	Pipeline *transform.Pipeline
	// Width and Height are CSS pixels; 0 = auto.
	Width, Height int
	// Fit is the fit mode; FitUnset = Spec.Fit.
	Fit Fit
	// Enlarge overrides Spec.Enlarge when set.
	Enlarge *bool

	b *imgproxy.Builder
}

// Ignore records a parameter that is understood but not applied, with a
// static reason.
func (st *State) Ignore(option, reason string) { st.b.Ignore(option, reason) }

// SetEnlarge overrides the provider's default enlargement.
func (st *State) SetEnlarge(v bool) { st.Enlarge = &v }

// applySize maps the collected sizing onto Resize (and Extend for FitPad).
func (st *State) applySize(spec *Spec) {
	rs := &st.Pipeline.Resize
	rs.Width.Px, rs.Height.Px = st.Width, st.Height
	enlarge := spec.Enlarge
	if st.Enlarge != nil {
		enlarge = *st.Enlarge
	}
	if st.Width == 0 && st.Height == 0 {
		return
	}
	fit := st.Fit
	if fit == FitUnset {
		fit = spec.Fit
	}
	switch fit {
	case FitInside:
		rs.Type = transform.ResizeFit
	case FitScaleDown:
		rs.Type, enlarge = transform.ResizeFit, false
	case FitFill:
		rs.Type = transform.ResizeForce
	case FitOutside:
		rs.Type = transform.ResizeOutside
	case FitPad:
		rs.Type = transform.ResizeFit
		if st.Width > 0 && st.Height > 0 {
			st.Pipeline.Extend = transform.Extend{Enabled: true, Gravity: st.Pipeline.Gravity}
		}
	default: // FitUnset, FitCover
		rs.Type = transform.ResizeFill
	}
	rs.Enlarge = enlarge
}
