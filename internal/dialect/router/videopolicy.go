package router

import (
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// VideoPolicy is the unsigned policy of video transformations
// (XI_VIDEO_TRANSFORM_UNSIGNED_*, ADR 0012 §8, WP-132). A video
// transformation whose signature is not Verified or Trusted is checked
// against it after the mount's policy:
//
//   - the mount's policy deny: 403, whatever this says;
//   - deny (the default): 403;
//   - allow: the mount's policy decides (allow passes; restricted refuses,
//     since its allowlists describe images);
//   - restricted: the request must be what its video presets make of it,
//     on a mount whose policy is allow or restricted (see below).
//
// Restricted: the dialect parses the URL a second time keeping only its
// preset references (dialect.Result.PresetVideo: imgproxy and query pr,
// Cloudinary t_, ImageKit n-, and the default video preset). The request
// passes when it names at least one video preset and its VideoOp equals
// that presets-only VideoOp in every field but
//
//   - the AllowedOptions (start, end, duration, mute), which the URL may
//     set freely; when it changes the cut with them and MaxDuration is set,
//     the cut must be bounded (a duration, or an end) and at most
//     MaxDuration long;
//   - the container: the presets' one, or one of Formats (an automatic
//     WebM-or-MP4 choice needs both mp4 and webm), of the same kind (a
//     file with video, audio only, or a waveform picture).
//
// So a stream (HLS or DASH) and raw delivery are possible only when a
// preset asks for them: the extension, f_ or ik-master of the URL cannot
// add them. Besides the VideoOp, the request may carry only the delivery
// options the image restricted policy allows (no cache buster or cache
// bypass, no security options, no chained pipelines). The comparison is
// over the whole VideoOp, so a field added to it later is refused until
// it is listed here.
type VideoPolicy struct {
	// Policy is allow, restricted or deny; "" means deny.
	Policy Policy
	// AllowedOptions are the VideoOp fields an unsigned restricted
	// request may set besides its presets: start, end, duration, mute.
	AllowedOptions []string
	// Formats are the containers (transform.VideoContainer names) an
	// unsigned restricted request may pick instead of its presets' one.
	Formats []string
	// MaxDuration bounds the cut an unsigned restricted request sets with
	// the start, end and duration options; 0 is no bound beyond the
	// output limits.
	MaxDuration time.Duration
}

// VideoAllowedOptions lists the values of VideoPolicy.AllowedOptions.
func VideoAllowedOptions() []string { return []string{"start", "end", "duration", "mute"} }

type videoPolicy struct {
	policy      Policy
	free        map[string]bool
	formats     []transform.VideoContainer
	maxDuration time.Duration
}

func newVideoPolicy(o *VideoPolicy) (*videoPolicy, error) {
	vp := &videoPolicy{policy: o.Policy, free: map[string]bool{}, maxDuration: o.MaxDuration}
	if vp.policy == "" {
		vp.policy = PolicyDeny
	}
	if !validPolicy(vp.policy) {
		return nil, fmt.Errorf("router: unknown video unsigned policy %q", o.Policy)
	}
	for _, name := range o.AllowedOptions {
		if !slices.Contains(VideoAllowedOptions(), name) {
			return nil, fmt.Errorf("router: video unsigned allowed option %q is not one of %v", name, VideoAllowedOptions())
		}
		vp.free[name] = true
	}
	for _, f := range o.Formats {
		c, err := transform.ParseVideoContainer(f)
		if err != nil {
			return nil, fmt.Errorf("router: video unsigned format %q: %w", f, err)
		}
		vp.formats = append(vp.formats, c)
	}
	if o.MaxDuration < 0 {
		return nil, fmt.Errorf("router: negative video unsigned max duration %s", o.MaxDuration)
	}
	return vp, nil
}

// enforceVideo applies the mount's and the video unsigned policies to an
// unsigned video transformation.
func (rt *Router) enforceVideo(m *Mount, res *dialect.Result) error {
	switch {
	case m.Policy == PolicyDeny:
		return unsignedError("unsigned policy deny")
	case rt.video.policy == PolicyDeny:
		return unsignedError("video unsigned policy deny")
	case rt.video.policy == PolicyAllow && m.Policy == PolicyAllow:
		return nil
	case rt.video.policy == PolicyAllow:
		return unsignedError("restricted policy: kind video")
	}
	if reason := rt.video.check(res); reason != "" {
		return unsignedError("video restricted policy: " + reason)
	}
	return nil
}

// check returns "" when res is within the restricted video policy, else
// the reason (internal text).
func (vp *videoPolicy) check(res *dialect.Result) string {
	req := res.Request
	switch {
	case req.Video == nil:
		return "no video options"
	case res.PresetVideo == nil:
		return "no video preset"
	case req.Info != nil:
		return "info"
	case req.Limits != (transform.LimitOverrides{}):
		return "security options"
	case req.ClientHints != (transform.ClientHints{}):
		return "client hints"
	case len(req.Pipelines) > 1:
		return "chained pipelines"
	}
	if f := firstSet(req.Output); f != "" {
		return "output." + f
	}
	for i := range req.Pipelines {
		if f := firstSet(req.Pipelines[i]); f != "" {
			return "pipeline." + f
		}
	}
	if reason := checkDelivery(&req.Delivery); reason != "" {
		return reason
	}
	return vp.checkOp(req.Video, res.PresetVideo)
}

// checkOp compares the request's op with the presets' one.
func (vp *videoPolicy) checkOp(got, presets *transform.VideoOp) string {
	g, p := *got, *presets
	if reason := vp.checkContainer(&g, &p); reason != "" {
		return reason
	}
	g.Container, g.AutoContainer, p.Container, p.AutoContainer = 0, false, 0, false
	cut := g.Start != p.Start || g.End != p.End || g.Duration != p.Duration
	for _, f := range []struct {
		name     string
		got, pre any
		clear    func()
	}{
		{"start", g.Start, p.Start, func() { g.Start, p.Start = 0, 0 }},
		{"end", g.End, p.End, func() { g.End, p.End = 0, 0 }},
		{"duration", g.Duration, p.Duration, func() { g.Duration, p.Duration = 0, 0 }},
		{"mute", g.Mute, p.Mute, func() { g.Mute, p.Mute = false, false }},
	} {
		if f.got == f.pre {
			continue
		}
		if !vp.free[f.name] {
			return "option " + f.name + " not allowed"
		}
		f.clear()
	}
	if cut && vp.maxDuration > 0 {
		length := got.Duration
		if length == 0 && got.End > 0 {
			length = got.End - got.Start
		}
		switch {
		case length <= 0:
			return "unbounded cut"
		case length > vp.maxDuration:
			return "cut longer than the unsigned limit"
		}
	}
	if !reflect.DeepEqual(g, p) {
		return "video." + firstDiff(g, p) + " not from a preset"
	}
	return ""
}

// checkContainer allows the presets' container or one of the formats of
// the same kind.
func (vp *videoPolicy) checkContainer(g, p *transform.VideoOp) string {
	if g.Container == p.Container && g.AutoContainer == p.AutoContainer {
		return ""
	}
	if g.Container.AudioOnly() != p.Container.AudioOnly() || g.Container.Image() != p.Container.Image() {
		return "container kind not from a preset"
	}
	ok := slices.Contains(vp.formats, g.Container)
	if g.AutoContainer {
		ok = slices.Contains(vp.formats, transform.VideoContainerMP4) && slices.Contains(vp.formats, transform.VideoContainerWebM)
	}
	if !ok {
		return "container not allowlisted"
	}
	return ""
}

// firstDiff names the first field of two VideoOps that differs.
func firstDiff(a, b transform.VideoOp) string {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	for i := range va.NumField() {
		if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
			return va.Type().Field(i).Name
		}
	}
	return ""
}
