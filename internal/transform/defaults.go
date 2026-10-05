package transform

import (
	"math"
	"slices"
)

// Defaults are the config-derived values ApplyDefaults fills into options a
// URL left unset. internal/app maps config.Config onto this struct.
type Defaults struct {
	AutoRotate        bool // XI_PROCESSING_AUTO_ROTATE
	StripMetadata     bool // XI_PROCESSING_STRIP_METADATA
	KeepCopyright     bool // XI_PROCESSING_KEEP_COPYRIGHT
	StripColorProfile bool // XI_PROCESSING_STRIP_COLOR_PROFILE
	PreserveHDR       bool // XI_PROCESSING_PRESERVE_HDR
	EnforceThumbnail  bool
	ReturnAttachment  bool
}

// StandardDefaults returns the documented config defaults (docs/CONFIG.md).
func StandardDefaults() Defaults {
	return Defaults{
		AutoRotate:        true,
		StripMetadata:     true,
		KeepCopyright:     true,
		StripColorProfile: true,
	}
}

// ApplyDefaults replaces unset options with their effective values so that
// two requests that mean the same thing compare (and hash) equal:
//
//   - at least one Pipeline exists;
//   - Resize.Type → fit, Resize.Algorithm → lanczos3, Zoom and DPR → 1;
//   - Gravity, Extend/ExtendAR gravity, watermark and video overlay
//     positions → center;
//     an unset Crop gravity inherits the pipeline gravity;
//   - Rotate is reduced to 0/90/180/270 and multiples of 90 in RotateAny are
//     folded into Rotate; RotateAny is reduced to (−180, 180];
//   - identity colour adjustments (brightness 0, hue 0, multipliers 1) → nil;
//   - config-backed booleans (auto-rotate, metadata, colour profile, HDR,
//     enforce-thumbnail, attachment) are set from d.
//
// It never writes through pointers or slices shared with the caller (for
// example preset data): anything it changes is copied first. It is idempotent.
func (r *Request) ApplyDefaults(d Defaults) {
	if len(r.Pipelines) == 0 {
		r.Pipelines = []Pipeline{{}}
	} else {
		r.Pipelines = slices.Clone(r.Pipelines)
	}
	for i := range r.Pipelines {
		r.Pipelines[i].applyDefaults(d)
	}

	o := &r.Output
	setBool(&o.StripMetadata, d.StripMetadata)
	setBool(&o.KeepCopyright, d.KeepCopyright)
	setBool(&o.StripColorProfile, d.StripColorProfile)
	setBool(&o.PreserveHDR, d.PreserveHDR)
	setBool(&o.EnforceThumbnail, d.EnforceThumbnail)
	setBool(&r.Delivery.Attachment, d.ReturnAttachment)

	if v := r.Video; v != nil && len(v.Overlays) > 0 {
		op := *v
		op.Overlays = slices.Clone(v.Overlays)
		for i := range op.Overlays {
			defaultGravity(&op.Overlays[i].Position)
		}
		r.Video = &op
	}
}

func (p *Pipeline) applyDefaults(d Defaults) {
	rs := &p.Resize
	if rs.Type == ResizeUnset {
		rs.Type = ResizeFit
	}
	if rs.Algorithm == KernelUnset {
		rs.Algorithm = KernelLanczos3
	}
	if rs.ZoomX == 0 {
		rs.ZoomX = 1
	}
	if rs.ZoomY == 0 {
		rs.ZoomY = 1
	}
	if rs.DPR == 0 {
		rs.DPR = 1
	}

	defaultGravity(&p.Gravity)
	defaultGravity(&p.Extend.Gravity)
	defaultGravity(&p.ExtendAR.Gravity)
	if p.Crop != nil && p.Crop.Gravity.Type == GravityUnset {
		c := *p.Crop
		c.Gravity = p.Gravity
		p.Crop = &c
	}
	if len(p.Watermarks) > 0 {
		p.Watermarks = slices.Clone(p.Watermarks)
		for i := range p.Watermarks {
			defaultGravity(&p.Watermarks[i].Position)
		}
	}

	p.normaliseRotation()
	p.Adjust.dropIdentity()
	setBool(&p.AutoRotate, d.AutoRotate)
}

func defaultGravity(g *Gravity) {
	if g.Type == GravityUnset {
		g.Type = GravityCenter
	}
}

func (p *Pipeline) normaliseRotation() {
	if a := p.RotateAny; a != 0 && !math.IsNaN(a) && !math.IsInf(a, 0) {
		if q := a / 90; q == math.Trunc(q) && math.Abs(q) < 1<<20 {
			p.Rotate += int(q) * 90
			p.RotateAny = 0
		} else {
			a = math.Mod(a, 360) // (−360, 360)
			switch {
			case a > 180:
				a -= 360
			case a <= -180:
				a += 360
			}
			p.RotateAny = a
		}
	}
	p.Rotate = ((p.Rotate % 360) + 360) % 360
}

// dropIdentity sets adjustments that do nothing to nil.
func (a *Adjust) dropIdentity() {
	dropIf(&a.Brightness, 0)
	dropIf(&a.Hue, 0)
	dropIf(&a.Contrast, 1)
	dropIf(&a.Saturation, 1)
	dropIf(&a.Gamma, 1)
	dropIf(&a.Exposure, 1)
	dropIf(&a.Vibrance, 1)
	dropIf(&a.Red, 1)
	dropIf(&a.Green, 1)
	dropIf(&a.Blue, 1)
	dropIf(&a.Opacity, 1)
}

func dropIf(p **float64, identity float64) {
	if *p != nil && **p == identity {
		*p = nil
	}
}

func setBool(p **bool, v bool) {
	if *p == nil {
		*p = &v
	}
}
