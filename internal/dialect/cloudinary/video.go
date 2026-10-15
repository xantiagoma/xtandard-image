package cloudinary

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Video transformations (WP-125): /<cloud>/video/<type>/<transformations>/
// <public_id>.<ext> becomes a transform.KindVideo request when its output
// is a video or audio file, following
// https://cloudinary.com/documentation/video_transformation_reference.
// The output is the f_ format, else the public ID's extension, else MP4;
// an image format (f_jpg, dog.jpg) keeps the thumbnail path (one frame,
// so_ picks it).
//
// Components chain in URL order onto one VideoOp: trimming, codecs and
// audio parameters accumulate (a later value wins); geometry is at most a
// c_crop followed by one resize (a later resize replaces an earlier one).
// Parameters that cannot be applied are recorded in Request.Ignored with
// their reason (400 in strict mode); "approximated" ones are applied with
// a documented difference.

// Reasons of the video mapping.
const (
	reasonAudio    = "M5 (WP-123)"
	reasonRelative = "needs the source size or duration"
	reasonNotVideo = "not applicable to video"
)

// Expectations of the video parameters.
const (
	expSeconds  = "seconds (a non-negative number)"
	expBitrate  = "bits per second, optionally with k or m (500k, 2m)"
	expFPS      = "frames per second (1-240) or a range min-max"
	expVideoAng = "0, 90, 180, 270 (or negative), hflip or vflip"
	expVCodec   = "auto, h264, h265, vp9, av1 or none, holdable by the output format"
	expACodec   = "none, aac, mp3, opus or vorbis, holdable by the output format"
	expVQuality = "1-100 or auto[:best|good|eco|low]"
	expVFormat  = "auto, mp4, webm, m4a, mp3, ogg, opus or an image format"
	expVPixels  = "integer pixels > 0"
	expVolume   = "mute, a percentage -100..400 or <n>db"
)

// videoAutoQuality maps q_auto levels onto the 1–100 quality the encoders
// turn into a CRF (internal/video.CRF).
var videoAutoQuality = map[string]int{"best": 80, "good": 65, "eco": 50, "low": 35}

// imageFormat reports whether name is an image output format: such a
// video URL is a thumbnail.
func imageFormat(name string) bool {
	f, ok := formatByName(name)
	return ok && f != transform.FormatMP4 && f != transform.FormatWebM
}

// videoContainers maps Cloudinary video and audio formats to containers;
// approx marks a different but compatible output.
var videoContainers = map[string]struct {
	c      transform.VideoContainer
	approx bool
}{
	"mp4":  {c: transform.VideoContainerMP4},
	"webm": {c: transform.VideoContainerWebM},
	"m4a":  {c: transform.VideoContainerM4A},
	"mp3":  {c: transform.VideoContainerMP3},
	"opus": {c: transform.VideoContainerOpus},
	// Ogg Vorbis (audio) or Theora (video) in Cloudinary: Ogg Opus here.
	"ogg": {c: transform.VideoContainerOpus, approx: true},
}

// unsupportedVideoFormats are Cloudinary output formats xtandard-image does
// not write: recorded, and MP4 is delivered.
var unsupportedVideoFormats = map[string]string{
	"mov": reasonUnsupported, "mkv": reasonUnsupported, "flv": reasonUnsupported,
	"avi": reasonUnsupported, "wmv": reasonUnsupported, "ogv": reasonUnsupported,
	"3gp": reasonUnsupported, "ts": reasonUnsupported, "m2ts": reasonUnsupported,
	"mts": reasonUnsupported,
	"wav": reasonAudio, "flac": reasonAudio, "aac": reasonAudio, "aiff": reasonAudio,
	"amr": reasonUnsupported, "wma": reasonUnsupported,
}

// videoOutput decides whether a video asset URL is a transcode: its output
// format (the last f_, else the source's extension) is not an image format.
func videoOutput(u *parsedURL) bool {
	format := ""
	for _, seg := range u.comps {
		for part := range strings.SplitSeq(seg, ",") {
			if v, ok := strings.CutPrefix(part, "f_"); ok {
				format = v
			}
		}
	}
	if format != "" && format != "auto" && !strings.HasPrefix(format, "auto:") {
		return !imageFormat(format)
	}
	ext := sourceExt(u.source)
	return ext == "" || !imageFormat(ext)
}

// sourceExt is the extension of a public ID or of a fetch URL's path.
func sourceExt(s string) string {
	s, _, _ = strings.Cut(s, "?")
	i := strings.LastIndexByte(s, '.')
	if i < 0 || strings.ContainsAny(s[i:], "/%") {
		return ""
	}
	return strings.ToLower(s[i+1:])
}

// videoApplier maps components onto a VideoOp.
type videoApplier struct {
	a  *applier
	op transform.VideoOp

	format    string // f_ as written; "" = none
	extension string // the source's extension
	auto      bool   // f_auto
	vcNone    bool
	vcSet     bool // an explicit video codec
	acSet     bool // an explicit audio codec
	resized   bool
	dpr       float64
	eoGiven   bool
	duGiven   bool
	// stream is set by an m3u8 or mpd output, profile by sp_ (WP-126,
	// videostream.go).
	stream  *transform.VideoStream
	profile string
	hlsv3   bool // fl_hlsv3: MPEG-TS segments (WP-131)

	// Video presets (WP-132): t_<name> names video presets
	// (XI_VIDEO_TRANSFORM_PRESETS), which are the op's starting point
	// wherever they appear (a named transformation is the defaults of its
	// component, and video components accumulate onto one op), after the
	// "default" video preset. presetContainer reports a preset that chose
	// the container: it wins over the public ID's extension, f_ over it.
	// presetsOnly applies the presets alone (no other parameter, layer,
	// f_ or extension) for the restricted unsigned video policy.
	presetContainer bool
	presetsOnly     bool
}

// video parses the components of a video transcode into r.
func (a *applier) video(u *parsedURL) error { return a.videoOp(u, false) }

// presetVideo parses u again with its video presets alone: the VideoOp
// the restricted unsigned video policy compares the URL's with, or nil.
func (p *Parser) presetVideo(u *parsedURL, mount string) *transform.VideoOp {
	a := newApplier(p, p.opt.NewBuilder(Name), mount)
	if err := a.videoOp(u, true); err != nil {
		return nil
	}
	return a.r.Video
}

// videoPresets starts the op from the URL's t_ video presets (and the
// default video preset). It records whether the URL named one.
func (va *videoApplier) videoPresets(u *parsedURL) error {
	if va.a.p.o.VideoPresets == nil {
		return nil
	}
	var names []string
	err := walkVideoComponents(u, func(c *component) error {
		for _, p := range c.params {
			if p.key != "t" {
				continue
			}
			ns := strings.Split(p.value, ".")
			if slices.Contains(ns, "") {
				return dialect.InvalidArgument("t", p.value, "named transformation")
			}
			names = append(names, ns...)
		}
		return nil
	}, nil)
	if err != nil {
		return err
	}
	op, containerSet, err := va.a.p.opt.VideoPresetOp(names)
	if err != nil || op == nil {
		return err
	}
	va.a.videoReferenced = len(names) > 0
	va.op, va.stream, va.presetContainer = *op, op.Stream, containerSet
	return nil
}

// walkVideoComponents calls base for every component that is not part of
// a layer group and layer for every layer group (its first component and
// the ones up to its fl_layer_apply), in URL order; a nil layer skips
// layer groups.
func walkVideoComponents(u *parsedURL, base func(*component) error, layer func(key, value string, group []*component) error) error {
	for i := 0; i < len(u.comps); i++ {
		c, err := parseComponent(u.comps[i])
		if err != nil {
			return err
		}
		key := "l"
		lv, ok := c.get(key)
		if !ok {
			key = "u"
			lv, ok = c.get(key)
		}
		if ok {
			// An overlay (or underlay) and its layer group, as the image
			// path delimits it: up to the next fl_layer_apply, if any.
			start := i
			if !c.hasFlag("layer_apply") {
				for j := i + 1; j < len(u.comps); j++ {
					if strings.Contains(u.comps[j], "layer_apply") {
						i = j
						break
					}
				}
			}
			if layer == nil {
				continue
			}
			group := []*component{c}
			for _, seg := range u.comps[start+1 : i+1] {
				gc, err := parseComponent(seg)
				if err != nil {
					return err
				}
				group = append(group, gc)
			}
			if err := layer(key, lv, group); err != nil {
				return err
			}
			continue
		}
		if err := base(c); err != nil {
			return err
		}
	}
	return nil
}

// videoOp maps the components onto the request's VideoOp; presetsOnly
// keeps the video presets alone (presetVideo).
func (a *applier) videoOp(u *parsedURL, presetsOnly bool) error {
	va := &videoApplier{a: a, extension: sourceExt(u.source), presetsOnly: presetsOnly}
	if presetsOnly {
		va.extension = ""
	}
	if err := va.videoPresets(u); err != nil {
		return err
	}
	if !presetsOnly {
		err := walkVideoComponents(u, func(c *component) error {
			if err := a.checkOnlyPresets(c); err != nil {
				return err
			}
			return va.component(c)
		}, func(key, value string, group []*component) error {
			if err := a.checkOnlyPresets(group[0]); err != nil {
				return err
			}
			return va.overlay(key, value, group)
		})
		if err != nil {
			return err
		}
	}
	if err := va.finish(); err != nil {
		return err
	}
	if err := a.b.Finish(); err != nil {
		return err
	}
	r := a.b.Request()
	op := va.op
	r.Kind, r.Video, r.Output = transform.KindVideo, &op, transform.Output{}
	return nil
}

// defaultFade is the length of e_fade without a value.
const defaultFade = 2 * time.Second

// overlay maps an l_ layer group onto a VideoOverlay (WP-124): the layer
// and its placement are parsed by the image path (applier.layer: image,
// l_fetch: and l_text: layers with g_, x_, y_, w_, h_, o_, a_, co_,
// fl_relative and fl_tiled), and the group's so_/eo_/du_ set the window on
// the output's timeline and e_fade[:±ms] the fades (positive fades in,
// negative out). Video, subtitle, LUT and audio layers, fl_splice and
// underlays are not supported.
func (va *videoApplier) overlay(key, v string, comps []*component) error {
	if comps[0].hasFlag("splice") {
		if err := va.skip("fl_splice", reasonUnsupported); err != nil {
			return err
		}
	}
	kind, _, _ := strings.Cut(v, ":")
	switch {
	case key == "u":
		return va.skip("u", reasonUnsupported)
	case kind == "video" || kind == "subtitles" || kind == "lut" || kind == "audio":
		return va.skip("l_"+kind, reasonUnsupported)
	}
	var ov transform.VideoOverlay
	var du time.Duration
	var eoSet bool
	rest := make([]*component, len(comps))
	for i, c := range comps {
		kept := &component{params: make([]param, 0, len(c.params))}
		for _, p := range c.params {
			var err error
			switch {
			case p.key == "so":
				err = va.offset("so", p.value, &ov.Start)
			case p.key == "eo":
				eoSet = true
				err = va.offset("eo", p.value, &ov.End)
			case p.key == "du":
				err = va.offset("du", p.value, &du)
			case p.key == "e" && (p.value == "fade" || strings.HasPrefix(p.value, "fade:")):
				err = va.fade(p.value, &ov)
			default:
				kept.params = append(kept.params, p)
			}
			if err != nil {
				return err
			}
		}
		rest[i] = kept
	}
	if du > 0 && !eoSet {
		ov.End = ov.Start + du
	} else if du > 0 {
		if err := va.skip("l_du", reasonApproximated); err != nil { // eo_ wins
			return err
		}
	}
	if ov.End > 0 && ov.End <= ov.Start {
		return dialect.InvalidArgument("eo", strconv.FormatFloat(ov.End.Seconds(), 'f', -1, 64), "a layer end after its so_")
	}
	pl := va.a.b.Pipeline()
	before := len(pl.Watermarks)
	if err := va.a.layer("l", v, rest); err != nil {
		return err
	}
	if len(pl.Watermarks) == before {
		return nil // recorded by layer
	}
	ov.Watermark = pl.Watermarks[before]
	pl.Watermarks = pl.Watermarks[:before]
	if len(va.op.Overlays) >= transform.MaxVideoOverlays {
		return dialect.InvalidURL("too many layers")
	}
	va.op.Overlays = append(va.op.Overlays, ov)
	return nil
}

// fade parses a layer's e_fade[:<ms>]: positive fades in, negative out.
func (va *videoApplier) fade(v string, ov *transform.VideoOverlay) error {
	d := defaultFade
	out := false
	if _, arg, ok := strings.Cut(v, ":"); ok {
		ms, err := strconv.Atoi(arg)
		if err != nil || ms == 0 || ms < -int(transform.MaxVideoTime.Milliseconds()) ||
			ms > int(transform.MaxVideoTime.Milliseconds()) {
			return dialect.InvalidArgument("e_fade", arg, "milliseconds, negative to fade out")
		}
		out = ms < 0
		d = time.Duration(max(ms, -ms)) * time.Millisecond
	}
	if out {
		ov.FadeOut = d
	} else {
		ov.FadeIn = d
	}
	return nil
}

// skip records a parameter that is not applied; in strict mode everything
// but an approximation is a 400.
func (va *videoApplier) skip(name, reason string) error {
	if va.a.p.o.Strict && reason != reasonApproximated {
		if reason == reasonUnknown {
			return dialect.UnknownOption(name)
		}
		return &errs.Error{
			Kind:   errs.KindInvalidURL,
			Msg:    "option " + strconv.Quote(truncate(name)) + ": " + reason,
			Public: "invalid url: unsupported option",
			Err:    ErrUnsupportedOption,
		}
	}
	va.a.b.Ignore(name, reason)
	return nil
}

// component applies one component.
//
//nolint:gocyclo,cyclop // one flat switch over the parameter table is the clearest form
func (va *videoApplier) component(c *component) error {
	geometry := false
	for _, p := range c.params {
		if strings.HasPrefix(p.key, "$") || isVariableRef(p.value) {
			if err := va.skip(optionName(p.key, p.value), reasonUnsupported); err != nil {
				return err
			}
			continue
		}
		var err error
		switch p.key {
		case "w", "h", "c", "g", "x", "y", "ar", "b":
			if !geometry {
				geometry = true
				err = va.geometry(c)
			}
		case "so":
			err = va.offset("so", p.value, &va.op.Start)
		case "eo":
			va.eoGiven = true
			err = va.offset("eo", p.value, &va.op.End)
		case "du":
			va.duGiven = true
			err = va.offset("du", p.value, &va.op.Duration)
		case "f":
			va.format = p.value
		case "q":
			err = va.quality(p.value)
		case "br":
			err = va.bitrate(p.value)
		case "fps":
			err = va.fps(p.value)
		case "a":
			err = va.angle(p.value)
		case "e":
			err = va.effect(p.value)
		case "ac":
			err = va.audioCodec(p.value)
		case "vc":
			err = va.videoCodec(p.value)
		case "fl":
			err = va.flags(p.value)
		case "dpr":
			err = va.dprValue(p.value)
		case "af":
			err = va.skip("af", reasonAudio)
		case "sp":
			err = va.streamingProfile(p.value)
		case "if":
			err = va.skip("if", reasonUnsupported)
		case "t":
			if va.a.p.o.VideoPresets == nil {
				err = va.skip("t", reasonUnsupported)
			} // else applied first (videoPresets)
		case "d", "pg", "z", "o", "r", "bo", "co", "cs", "dl", "dn", "fn", "ki", "p", "vs":
			err = va.skip(optionName(p.key, p.value), reasonUnsupported)
		default:
			err = va.skip(optionName(p.key, p.value), reasonUnknown)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// offset parses so_/eo_/du_ in seconds; percentages of the duration are
// recorded (they need the source's duration).
func (va *videoApplier) offset(key, v string, dst *time.Duration) error {
	switch {
	case v == "auto" && key == "so":
		return va.skip("so_auto", reasonApproximated) // a thumbnail selection: the start here
	case strings.HasSuffix(v, "p") || strings.HasSuffix(v, "%"):
		return va.skip(key, reasonRelative)
	}
	sec, err := strconv.ParseFloat(v, 64)
	if err != nil || sec < 0 || math.IsInf(sec, 0) || math.IsNaN(sec) || sec > transform.MaxVideoTime.Seconds() {
		return dialect.InvalidArgument(key, v, expSeconds)
	}
	*dst = time.Duration(math.Round(sec * float64(time.Second)))
	return nil
}

// geometry applies one component's w_, h_, c_, g_, x_, y_, ar_ and b_.
func (va *videoApplier) geometry(c *component) error {
	var w, h, x, y int
	var hasX, hasY bool
	var ar float64
	mode, gravity := "", ""
	var bg *transform.Color
	for _, p := range c.params {
		var err error
		switch p.key {
		case "w":
			w, err = va.pixels("w", p.value)
		case "h":
			h, err = va.pixels("h", p.value)
		case "x":
			x, err = va.offsetPx("x", p.value)
			hasX = err == nil
		case "y":
			y, err = va.offsetPx("y", p.value)
			hasY = err == nil
		case "c":
			mode = p.value
		case "g":
			gravity = p.value
		case "ar":
			ar, err = parseAspectRatio(p.value)
		case "b":
			bg, err = va.background(p.value)
		}
		if err != nil {
			return err
		}
	}
	if w < 0 || h < 0 { // relative or expression, recorded
		return nil
	}
	switch {
	case ar > 0 && w > 0 && h == 0:
		h = max(int(math.Round(float64(w)/ar)), 1)
	case ar > 0 && h > 0 && w == 0:
		w = max(int(math.Round(float64(h)*ar)), 1)
	case ar > 0 && w == 0 && h == 0:
		return va.skip("ar", reasonRelative)
	}
	g, err := va.gravity(gravity)
	if err != nil {
		return err
	}
	if mode == "crop" {
		return va.crop(w, h, x, y, hasX || hasY, gravity != "", g)
	}
	if w == 0 && h == 0 && mode == "" && gravity == "" && bg != nil {
		va.op.Background = *bg // b_ alone: the background of the resize before it
		return nil
	}
	if w == 0 && h == 0 {
		if mode != "" || bg != nil {
			return va.skip("c_"+truncate(mode), reasonRelative)
		}
		return nil
	}
	fit, err := va.fit(mode, w > 0 && h > 0)
	if err != nil {
		return err
	}
	if va.resized {
		if err := va.skip("c_"+truncate(mode), reasonApproximated); err != nil { // a second resize replaces the first
			return err
		}
	}
	if hasX || hasY {
		if err := va.skip("x", reasonApproximated); err != nil { // offsets only place crops
			return err
		}
	}
	va.resized = true
	va.op.Width, va.op.Height, va.op.Fit, va.op.Gravity = w, h, fit, g
	if bg != nil {
		va.op.Background = *bg
	}
	return nil
}

// fit maps a crop mode with or without both sides.
func (va *videoApplier) fit(mode string, box bool) (transform.VideoFit, error) {
	fit := transform.VideoFitContain
	approx := false
	switch mode {
	case "", "scale":
		if box {
			fit = transform.VideoFitFill
		}
	case "fit":
	case "limit":
		fit = transform.VideoFitScaleDown
	case "mfit":
		approx = true
	case "fill":
		fit = transform.VideoFitCover
	case "lfill", "thumb", "auto":
		fit, approx = transform.VideoFitCover, true
	case "pad":
		fit = transform.VideoFitPad
	case "lpad", "mpad", "fill_pad", "auto_pad":
		fit, approx = transform.VideoFitPad, true
	default:
		if strings.HasPrefix(mode, "imagga_") {
			return fit, va.skip("c_"+truncate(mode), reasonUnsupported)
		}
		return fit, dialect.InvalidArgument("c", mode, expCrop)
	}
	if fit.NeedsBox() && !box {
		fit = transform.VideoFitContain
	}
	if approx {
		return fit, va.skip("c_"+mode, reasonApproximated)
	}
	return fit, nil
}

// crop applies c_crop: a w × h rectangle at x, y from the gravity's
// anchor (the centre by default; the top-left corner with x_/y_ alone).
func (va *videoApplier) crop(w, h, x, y int, offsets, hasGravity bool, g transform.VideoGravity) error {
	switch {
	case w == 0 || h == 0:
		return va.skip("c_crop", reasonRelative)
	case w < 2 || h < 2:
		return dialect.InvalidArgument("c_crop", strconv.Itoa(min(w, h)), "a crop of at least 2 × 2 pixels")
	case va.resized || va.op.Crop != nil:
		return va.skip("c_crop", reasonUnsupported) // a crop after a resize, or a second crop
	}
	crop := &transform.VideoCrop{X: x, Y: y, Width: w, Height: h}
	if hasGravity || !offsets {
		crop.Gravity = &g
	}
	va.op.Crop = crop
	return nil
}

// pixels parses a w_/h_ value; relative sizes and expressions are recorded
// and return -1.
func (va *videoApplier) pixels(key, v string) (int, error) {
	n, err := strconv.Atoi(v)
	if err == nil {
		if n <= 0 || n > transform.MaxVideoDimension {
			return 0, dialect.InvalidArgument(key, v, expVPixels)
		}
		return n, nil
	}
	if f, ferr := strconv.ParseFloat(v, 64); ferr == nil && f > 0 || isExpression(v) {
		return -1, va.skip(key, reasonRelative)
	}
	return 0, dialect.InvalidArgument(key, v, expVPixels)
}

// offsetPx parses x_/y_ as non-negative pixels.
func (va *videoApplier) offsetPx(key, v string) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > transform.MaxVideoDimension {
		if f, ferr := strconv.ParseFloat(v, 64); ferr == nil && f > -1 && f < 1 {
			return 0, va.skip(key, reasonRelative)
		}
		return 0, dialect.InvalidArgument(key, v, "non-negative integer pixels")
	}
	return n, nil
}

// gravity maps g_; detection-based gravities are approximated by the
// centre.
func (va *videoApplier) gravity(v string) (transform.VideoGravity, error) {
	name, _, _ := strings.Cut(v, ":")
	switch name {
	case "", "center", "centre":
		return transform.VideoGravityCenter, nil
	case "north":
		return transform.VideoGravityNorth, nil
	case "south":
		return transform.VideoGravitySouth, nil
	case "east":
		return transform.VideoGravityEast, nil
	case "west":
		return transform.VideoGravityWest, nil
	case "north_east":
		return transform.VideoGravityNorthEast, nil
	case "north_west":
		return transform.VideoGravityNorthWest, nil
	case "south_east":
		return transform.VideoGravitySouthEast, nil
	case "south_west":
		return transform.VideoGravitySouthWest, nil
	case "auto", "face", "faces", "adv_face", "adv_faces", "adv_eyes", "xy_center", "custom", "body", "track_person":
		return transform.VideoGravityCenter, va.skip("g_"+name, reasonApproximated)
	}
	if isObjectName(v) {
		return transform.VideoGravityCenter, va.skip("g_"+truncate(v), reasonApproximated)
	}
	return 0, dialect.InvalidArgument("g", v, expGravity)
}

func (va *videoApplier) background(v string) (*transform.Color, error) {
	name, _, _ := strings.Cut(v, ":")
	switch name {
	case "auto", "blurred", "gen_fill":
		return nil, va.skip("b_"+name, reasonUnsupported)
	}
	c, err := parseColor("b", v)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// quality maps q_<n> and q_auto[:level].
func (va *videoApplier) quality(v string) error {
	q, rest, _ := strings.Cut(v, ":")
	if q == "auto" {
		level := "good"
		if rest != "" {
			level, _, _ = strings.Cut(rest, ":")
		}
		n, ok := videoAutoQuality[level]
		if !ok {
			return dialect.InvalidArgument("q", v, expVQuality)
		}
		va.op.Quality, va.op.CRF = n, 0
		return nil
	}
	n, err := strconv.Atoi(q)
	if err != nil || n < 1 || n > 100 {
		return dialect.InvalidArgument("q", v, expVQuality)
	}
	va.op.Quality, va.op.CRF = n, 0
	if rest != "" {
		return va.skip("q", reasonApproximated) // q_<n>:qmax_<m>, chroma subsampling
	}
	return nil
}

// bitrate maps br_<n>[k|m][:constant] to the maximum bit rate.
func (va *videoApplier) bitrate(v string) error {
	s, mode, _ := strings.Cut(v, ":")
	mul := 1.0
	switch {
	case strings.HasSuffix(s, "k"):
		s, mul = s[:len(s)-1], 1e3
	case strings.HasSuffix(s, "m"):
		s, mul = s[:len(s)-1], 1e6
	}
	f, err := strconv.ParseFloat(s, 64)
	bps := f * mul
	if err != nil || math.IsNaN(bps) || bps < transform.MinVideoBitrate || bps > transform.MaxVideoBitrate {
		return dialect.InvalidArgument("br", v, expBitrate)
	}
	va.op.MaxBitrate = int64(bps)
	if mode != "" {
		return va.skip("br", reasonApproximated) // constant bit rate: capped instead
	}
	return nil
}

// fps maps fps_<n>; a range keeps its upper bound.
func (va *videoApplier) fps(v string) error {
	lo, hi, isRange := strings.Cut(v, "-")
	s := lo
	if isRange {
		if hi == "" {
			return va.skip("fps", reasonApproximated) // a minimum only: the source's rate
		}
		s = hi
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || !(f >= 1 && f <= transform.MaxVideoFPS) {
		return dialect.InvalidArgument("fps", v, expFPS)
	}
	va.op.FPS = f
	if isRange {
		return va.skip("fps", reasonApproximated)
	}
	return nil
}

// angle maps a_: multiples of 90 and the flips.
func (va *videoApplier) angle(v string) error {
	for part := range strings.SplitSeq(v, ".") {
		switch part {
		case "hflip":
			va.op.FlipH = !va.op.FlipH
			continue
		case "vflip":
			va.op.FlipV = !va.op.FlipV
			continue
		case "auto_right", "auto_left", "exif", "ignore":
			if err := va.skip("a_"+part, reasonUnsupported); err != nil {
				return err
			}
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return dialect.InvalidArgument("a", v, expVideoAng)
		}
		if n%90 != 0 {
			if err := va.skip("a", reasonUnsupported); err != nil {
				return err
			}
			continue
		}
		va.op.Rotate = ((va.op.Rotate+n)%360 + 360) % 360
	}
	return nil
}

// effect maps e_volume:mute; the other audio effects come with WP-123 and
// video effects are not supported.
func (va *videoApplier) effect(v string) error {
	name, arg, _ := strings.Cut(v, ":")
	switch name {
	case "volume":
		switch {
		case arg == "mute" || arg == "-100":
			va.op.Mute = true
			return nil
		case validVolume(arg):
			return va.skip("e_volume", reasonAudio)
		}
		return dialect.InvalidArgument("e_volume", arg, expVolume)
	case "fade", "noise_reduction", "loudness", "dehaze_audio":
		return va.skip("e_"+name, reasonAudio)
	}
	return va.skip(truncate("e_"+name), reasonUnsupported)
}

func validVolume(s string) bool {
	num, _ := strings.CutSuffix(strings.ToLower(s), "db")
	_, err := strconv.ParseFloat(num, 64)
	return s != "" && err == nil
}

// audioCodec maps ac_.
func (va *videoApplier) audioCodec(v string) error {
	switch v {
	case "none":
		va.op.Mute = true
		return nil
	case "aac":
		va.op.AudioCodec = transform.AudioCodecAAC
	case "mp3":
		va.op.AudioCodec = transform.AudioCodecMP3
	case "opus":
		va.op.AudioCodec = transform.AudioCodecOpus
	case "vorbis":
		va.op.AudioCodec = transform.AudioCodecOpus
		va.acSet = true
		return va.skip("ac_vorbis", reasonApproximated) // Opus instead
	default:
		return dialect.InvalidArgument("ac", v, expACodec)
	}
	va.op.Mute, va.acSet = false, true
	return nil
}

// videoCodec maps vc_<codec>[:profile[:level]].
func (va *videoApplier) videoCodec(v string) error {
	name, rest, _ := strings.Cut(v, ":")
	va.vcNone, va.vcSet = false, true
	switch name {
	case "auto":
		va.op.VideoCodec, va.vcSet = transform.VideoCodecAuto, false
	case "h264":
		va.op.VideoCodec = transform.VideoCodecH264
	case "h265", "hevc":
		va.op.VideoCodec = transform.VideoCodecHEVC
	case "vp9":
		va.op.VideoCodec = transform.VideoCodecVP9
	case "av1":
		va.op.VideoCodec = transform.VideoCodecAV1
	case "none":
		va.op.VideoCodec, va.vcNone, va.vcSet = transform.VideoCodecAuto, true, false
	case "vp8", "prores", "theora":
		va.op.VideoCodec, va.vcSet = transform.VideoCodecAuto, false
		return va.skip("vc_"+name, reasonUnsupported)
	default:
		return dialect.InvalidArgument("vc", v, expVCodec)
	}
	if rest != "" {
		return va.skip("vc", reasonApproximated) // profile and level: the encoder's defaults
	}
	return nil
}

// flags maps the fl_ flags that apply to video.
func (va *videoApplier) flags(v string) error {
	d := &va.a.r.Delivery
	for f := range strings.SplitSeq(v, ".") {
		name, arg, _ := strings.Cut(f, ":")
		var err error
		switch name {
		case "attachment", "streaming_attachment":
			d.Attachment = new(true)
			if arg != "" {
				d.Filename = arg
			}
		case "immutable_cache":
			d.Immutable = true
		case "keep_dar", "animated", "layer_apply", "no_stream", "truncate_ts":
			// The default here: the display aspect is kept, outputs are
			// complete files, timestamps start at zero.
		case "lossy", "awebp", "apng", "png8", "progressive", "preserve_transparency":
			err = va.skip("fl_"+name, reasonNotVideo)
		case "waveform", "mono":
			err = va.skip("fl_"+name, reasonAudio)
		case "hlsv3":
			va.hlsv3 = true
		case "splice", "getinfo", "relative", "replace_image":
			err = va.skip("fl_"+name, reasonUnsupported)
		default:
			err = va.skip(truncate("fl_"+name), reasonUnknown)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (va *videoApplier) dprValue(v string) error {
	if v == "auto" {
		return va.skip("dpr_auto", reasonApproximated) // 1
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || !(f > 0 && f <= transform.MaxFactor) {
		return dialect.InvalidArgument("dpr", v, expDPR)
	}
	va.dpr = f
	return nil
}

// finish resolves the output: container from f_ or the extension, codecs
// against it, the cut and the DPR.
func (va *videoApplier) finish() error {
	op := &va.op
	if va.eoGiven && va.duGiven {
		if err := va.skip("du", reasonApproximated); err != nil { // eo_ wins
			return err
		}
		op.Duration = 0
	}
	if op.End > 0 && op.End <= op.Start {
		return dialect.InvalidArgument("eo", strconv.FormatFloat(op.End.Seconds(), 'f', -1, 64), "a time after so_")
	}
	if va.dpr > 0 && va.dpr != 1 {
		scale := func(n int) int {
			if n == 0 {
				return 0
			}
			return min(max(int(math.Round(float64(n)*va.dpr)), 2), transform.MaxVideoDimension)
		}
		op.Width, op.Height = scale(op.Width), scale(op.Height)
		if c := op.Crop; c != nil {
			c.X, c.Y, c.Width, c.Height = scale(c.X), scale(c.Y), scale(c.Width), scale(c.Height)
		}
	}
	if err := va.container(); err != nil {
		return err
	}
	if va.auto {
		va.autoContainer()
	}
	if va.stream != nil {
		return va.finishStream()
	}
	if va.profile != "" {
		if err := va.skip("sp_"+va.profile, reasonNoStream); err != nil {
			return err
		}
	}
	if va.hlsv3 {
		if err := va.skip("fl_hlsv3", reasonNoHLS); err != nil {
			return err
		}
	}

	if va.vcNone && !op.Container.AudioOnly() {
		// Audio only: the container's audio counterpart, or the one the
		// audio codec needs.
		switch {
		case op.AudioCodec == transform.AudioCodecMP3:
			op.Container = transform.VideoContainerMP3
		case op.AudioCodec == transform.AudioCodecOpus || op.Container == transform.VideoContainerWebM:
			op.Container = transform.VideoContainerOpus
		default:
			op.Container = transform.VideoContainerM4A
		}
		op.AutoContainer = false
	}
	if op.Container.AudioOnly() {
		if len(op.Overlays) > 0 {
			return dialect.InvalidArgument("l", op.Container.String(), "a video output for layers")
		}
		if va.vcSet {
			if err := va.skip("vc", reasonNotVideo); err != nil {
				return err
			}
			op.VideoCodec = transform.VideoCodecAuto
		}
		if op.Mute {
			return dialect.InvalidArgument("ac", "none", "an audio codec for audio-only output")
		}
		op.Width, op.Height, op.Fit, op.Crop, op.FPS, op.MaxBitrate = 0, 0, 0, nil, 0, 0
	}
	if !op.Container.AllowsVideo(op.VideoCodec) {
		return dialect.InvalidArgument("vc", op.VideoCodec.String(), expVCodec)
	}
	if !op.Container.AllowsAudio(op.AudioCodec) {
		return dialect.InvalidArgument("ac", op.AudioCodec.String(), expACodec)
	}
	return nil
}

// container sets the container from f_ (else the extension, else MP4).
func (va *videoApplier) container() error {
	if va.format == "" && va.presetContainer {
		return nil // the preset's container (or stream) wins over the extension
	}
	name, key := va.format, "f"
	if name == "" {
		name, key = va.extension, "extension"
	}
	if name == "auto" || strings.HasPrefix(name, "auto:") {
		va.auto = true
		return nil
	}
	if name == "" {
		return nil
	}
	if p, ok := streamProtocols[strings.ToLower(name)]; ok {
		va.stream = &transform.VideoStream{Protocol: p}
		return nil
	}
	if c, ok := videoContainers[strings.ToLower(name)]; ok {
		va.op.Container = c.c
		if c.approx {
			return va.skip("f_"+name, reasonApproximated)
		}
		return nil
	}
	if reason, ok := unsupportedVideoFormats[strings.ToLower(name)]; ok {
		return va.skip(truncate("f_"+name), reason)
	}
	if key == "extension" {
		return nil // an extension that is no output format (dog.mov.bak)
	}
	return dialect.InvalidArgument("f", name, expVFormat)
}

// autoContainer applies f_auto: the server picks WebM or MP4 from Accept
// unless a codec or audio-only output fixes the container.
func (va *videoApplier) autoContainer() {
	op := &va.op
	switch {
	case va.vcNone:
		// Audio only: finish picks the container.
	case op.VideoCodec == transform.VideoCodecVP9 || op.AudioCodec == transform.AudioCodecOpus:
		op.Container = transform.VideoContainerWebM
	case op.VideoCodec == transform.VideoCodecH264 || op.VideoCodec == transform.VideoCodecHEVC ||
		op.AudioCodec == transform.AudioCodecAAC:
		op.Container = transform.VideoContainerMP4
	default:
		op.Container, op.AutoContainer = transform.VideoContainerMP4, true
	}
}
