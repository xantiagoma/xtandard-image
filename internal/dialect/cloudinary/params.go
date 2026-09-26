package cloudinary

import (
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Parameter mapping (docs/URL-DIALECTS.md §6). Semantics, following
// https://cloudinary.com/documentation/transformation_reference:
//
//   - w_/h_: integer pixels, or a decimal (w_0.5, w_1.0) relative to the
//     input; w_auto[:step[:fallback]] opts into the width client hint.
//   - c_: scale (the default when w_/h_/ar_ are given) → force with both
//     dimensions or an aspect ratio, else fit + enlarge; fit → fit +
//     enlarge; limit → fit; mfit → outside + enlarge; fill → fill +
//     enlarge; lfill → fill-down; fill_pad → fill + extend (approximated);
//     pad → fit + enlarge + extend; lpad → fit + extend; mpad → fit + extend
//     (approximated: never scales in Cloudinary); crop → pre-resize crop
//     (x_/y_ = top-left corner, or with g_xy_center the centre); thumb →
//     fill with z_ as focus zoom; auto → fill + smart; auto_pad → fit +
//     enlarge + extend; imagga_* → recorded unsupported, applied as scale.
//   - g_: compass, center, auto[:…] → smart, face(s)[:…] → face (M3),
//     xy_center → focus point (x_/y_ relative), a single object name → object
//     (M3); x_/y_ offset the gravity.
//   - ar_: w:h or a decimal; z_: zoom for crop and thumb.
//   - e_: blur[:1..2000] σ = n/20 (like imgix blur), sharpen σ = n/100,
//     unsharp_mask amount n/100 (M2), pixelate[:n] (default 10),
//     grayscale/blackwhite/sepia/negate (M2), brightness n × 2.55,
//     contrast/saturation/gamma/vibrance/red/green/blue 1 + n/100, hue
//     n × 1.8° (all M2), colorize[:n] with co_ (M2), trim[:tolerance[:color]],
//     blur_faces/pixelate_faces (M3), blur_region with relative x_/y_/w_/h_
//     (M2); other known effects are unsupported.
//   - a_: degrees clockwise (multiples of 90 → Rotate, others → RotateAny,
//     M2), hflip, vflip, exif, ignore; auto_right/auto_left unsupported.
//   - b_ background, bo_<n>px_solid_<color> border (M2), r_ radius incl.
//     max (M2), o_ opacity (M2), co_ colour for colorize.
//   - q_<1..100>[:420|422|444], q_auto[:best|good|eco|low] (DSSIM targets
//     0.005/0.02/0.035/0.05, M2); f_auto (negotiate) or f_<format>;
//     dpr_<n>|auto; fl_ output/delivery flags; cs_ colour space (M2);
//     d_ default image (M2); pg_ page (1-based, M2); t_ named
//     transformations (presets); l_ image overlays → watermarks (M3).
//   - Unsupported (recorded, or 400 in strict mode): video and audio
//     parameters, conditions (if_ skips its component or block), variables,
//     custom functions, text/video/LUT layers, underlays, density.

// Reasons recorded in Request.Ignored.
const (
	reasonUnknown      = "unknown option"
	reasonUnsupported  = "unsupported"
	reasonApproximated = "approximated"
	reasonM2           = "takes effect in M2"
	reasonM3           = "takes effect in M3"
)

// ErrUnsupportedOption is the cause of the strict-mode 400 for a known
// Cloudinary parameter xtandard-image does not implement.
var ErrUnsupportedOption = errors.New("cloudinary: unsupported option")

// Expectation phrases (client-safe, static).
const (
	expDim     = "integer pixels > 0, a relative decimal (0.5) or auto[:step[:fallback]]"
	expCrop    = "crop mode (scale, fit, limit, mfit, fill, lfill, fill_pad, pad, lpad, mpad, crop, thumb, auto, auto_pad)"
	expGravity = "gravity (compass, center, auto, face, faces, xy_center or an object name)"
	expNumber  = "number"
	expRatio   = "aspect ratio (w:h or a positive decimal)"
	expPos     = "positive number"
	expQuality = "1-100[:420|422|444] or auto[:best|good|eco|low]"
	expFormat  = "auto, jpg, png, webp, avif, jxl, gif, heic, tiff, bmp, ico, svg, mp4 or webm"
	expDPR     = "positive number or auto"
	expColor   = "colour name or rgb:hex"
	expBorder  = "<width>px_solid_<color>"
	expRadius  = "1-4 ':'-separated pixel values or max"
	expPercent = "0-100"
	expEffect  = "effect strength in range"
	expPage    = "page number >= 1"
	expLayer   = "layer public id or fetch:<base64 url>"
	expAngle   = "degrees or hflip, vflip, exif, ignore"
)

// knownKeys are the Cloudinary transformation parameter keys
// (cloudinary-npm generate_transformation_string, SIMPLE_PARAMS).
var knownKeys = map[string]bool{
	"a": true, "ac": true, "af": true, "ar": true, "b": true, "bo": true, "br": true,
	"c": true, "co": true, "cs": true, "d": true, "dl": true, "dn": true, "dpr": true,
	"du": true, "e": true, "eo": true, "f": true, "fl": true, "fn": true, "fps": true,
	"g": true, "h": true, "if": true, "ki": true, "l": true, "o": true, "p": true,
	"pg": true, "q": true, "r": true, "so": true, "sp": true, "t": true, "u": true,
	"vc": true, "vs": true, "w": true, "x": true, "y": true, "z": true,
}

// isComponent reports whether seg has the shape of a transformation
// component: key_value parameters separated by ",", with lower-case keys
// (or $variables) and at least one known key.
func isComponent(seg string) bool {
	if seg == "" {
		return false
	}
	known := false
	for part := range strings.SplitSeq(seg, ",") {
		key, _, ok := strings.Cut(part, "_")
		if !ok || !isKey(key) {
			return false
		}
		known = known || knownKeys[key]
	}
	return known
}

func isKey(k string) bool {
	if k == "" {
		return false
	}
	for i := range len(k) {
		c := k[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c == '$' && i == 0:
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// param is one decoded key_value parameter.
type param struct {
	key, value string
}

// component is one parsed transformation component.
type component struct {
	params []param
}

// get returns the last value of key in the component.
func (c *component) get(key string) (string, bool) {
	for _, p := range slices.Backward(c.params) {
		if p.key == key {
			return p.value, true
		}
	}
	return "", false
}

// hasFlag reports whether the component carries fl_<name> (flags may be
// "."-joined).
func (c *component) hasFlag(name string) bool {
	for _, p := range c.params {
		if p.key != "fl" {
			continue
		}
		for f := range strings.SplitSeq(p.value, ".") {
			if flagName(f) == name {
				return true
			}
		}
	}
	return false
}

func flagName(f string) string {
	name, _, _ := strings.Cut(f, ":")
	return name
}

func parseComponent(seg string) (*component, error) {
	c := &component{params: make([]param, 0, 4)}
	for part := range strings.SplitSeq(seg, ",") {
		key, raw, _ := strings.Cut(part, "_")
		v, err := url.PathUnescape(raw)
		if err != nil {
			return nil, dialect.InvalidURL("invalid path escaping")
		}
		c.params = append(c.params, param{key: key, value: v})
	}
	return c, nil
}

// applier applies components to a request through an imgproxy.Builder.
type applier struct {
	p     *Parser
	b     *imgproxy.Builder
	r     *transform.Request
	mount string

	used           bool // the current pipeline holds pipeline parameters
	explicitFormat bool // f_ was given
	extFormat      transform.Format
}

func newApplier(p *Parser, b *imgproxy.Builder, mount string) *applier {
	return &applier{p: p, b: b, r: b.Request(), mount: mount}
}

// presetPrologue applies the "default" preset before the URL's
// components, as the imgproxy dialect does.
func (a *applier) presetPrologue() error {
	if _, ok := a.p.o.Presets.Default(); ok {
		return a.b.Apply("preset", "default")
	}
	return nil
}

// optionName is the name recorded in Request.Ignored: the key, plus the
// feature name for parameters whose value selects one (e_blur, fl_png8).
func optionName(key, value string) string {
	switch key {
	case "e", "fl", "c", "g", "a", "l", "q", "b", "cs", "f":
		name, _, _ := strings.Cut(value, ":")
		return truncate(key + "_" + name)
	}
	return truncate(key)
}

// skip handles a parameter that is not applied: unknown ones are 400 in
// strict mode, unsupported ones too; otherwise both are recorded.
func (a *applier) skip(name, reason string) error {
	if a.p.o.Strict {
		switch reason {
		case reasonUnknown:
			return dialect.UnknownOption(name)
		case reasonUnsupported:
			return &errs.Error{
				Kind:   errs.KindInvalidURL,
				Msg:    "option " + strconv.Quote(truncate(name)),
				Public: "invalid url: unsupported option",
				Err:    ErrUnsupportedOption,
			}
		}
	}
	a.b.Ignore(name, reason)
	return nil
}

// components applies the components in URL order.
func (a *applier) components(segs []string) error {
	comps := make([]*component, len(segs))
	for i, s := range segs {
		c, err := parseComponent(s)
		if err != nil {
			return err
		}
		if err := a.checkOnlyPresets(c); err != nil {
			return err
		}
		comps[i] = c
	}
	for i := 0; i < len(comps); i++ {
		c := comps[i]
		if cond, ok := c.get("if"); ok {
			if err := a.skip("if", reasonUnsupported); err != nil {
				return err
			}
			if cond != "end" && len(c.params) == 1 {
				// A block: skip everything up to its if_end.
				for i+1 < len(comps) && !isIfEnd(comps[i+1]) {
					i++
				}
				i++
			}
			continue
		}
		if l, ok := c.get("l"); ok {
			end := i
			if !c.hasFlag("layer_apply") {
				for j := i + 1; j < len(comps); j++ {
					if comps[j].hasFlag("layer_apply") {
						end = j
						break
					}
				}
			}
			if err := a.layer("l", l, comps[i:end+1]); err != nil {
				return err
			}
			i = end
			continue
		}
		if u, ok := c.get("u"); ok {
			end := i
			if !c.hasFlag("layer_apply") {
				for j := i + 1; j < len(comps); j++ {
					if comps[j].hasFlag("layer_apply") {
						end = j
						break
					}
				}
			}
			if err := a.skip(optionName("u", u), reasonUnsupported); err != nil {
				return err
			}
			i = end
			continue
		}
		if err := a.base(c); err != nil {
			return err
		}
	}
	return nil
}

// isVariableRef reports whether a value uses a user variable ($name).
func isVariableRef(v string) bool {
	return strings.HasPrefix(v, "$") || strings.Contains(v, "_$")
}

func isIfEnd(c *component) bool {
	v, ok := c.get("if")
	return ok && v == "end"
}

// checkOnlyPresets enforces only-presets mode: t_ is the only parameter.
func (a *applier) checkOnlyPresets(c *component) error {
	if !a.p.o.Presets.Only() {
		return nil
	}
	for _, p := range c.params {
		if p.key != "t" {
			return &errs.Error{
				Kind:   errs.KindForbidden,
				Public: "only presets are allowed",
				Err:    imgproxy.ErrOnlyPresets,
			}
		}
	}
	return nil
}

// startsPipeline reports whether c starts a chained pipeline when the
// current one is in use. b_ and dpr_ alone modify the current pipeline
// (the SDKs emit backgroundColor and dpr as their own components after
// the resize they belong to).
func startsPipeline(c *component) bool {
	for _, p := range c.params {
		switch p.key {
		case "w", "h", "c", "g", "x", "y", "ar", "z", "a", "bo", "e", "o", "r", "t":
			return true
		}
	}
	return false
}

// base applies a component that is not a layer.
func (a *applier) base(c *component) error {
	if startsPipeline(c) {
		if a.used {
			if err := a.b.NewPipeline(); err != nil {
				return err
			}
		}
		a.used = true
	}
	// Named transformations first: they are the component's defaults.
	for _, p := range c.params {
		if p.key == "t" {
			if err := a.named(p.value); err != nil {
				return err
			}
		}
	}
	pl := a.b.Pipeline()
	geometryDone := false
	for _, p := range c.params {
		if strings.HasPrefix(p.key, "$") {
			if err := a.skip("$variable", reasonUnsupported); err != nil {
				return err
			}
			continue
		}
		if isVariableRef(p.value) {
			if err := a.skip(optionName(p.key, p.value), reasonUnsupported); err != nil {
				return err
			}
			continue
		}
		var err error
		switch p.key {
		case "w", "h", "c", "g", "x", "y", "ar", "z":
			if !geometryDone {
				geometryDone = true
				err = a.geometry(c, pl)
			}
		case "t":
		case "e":
			err = a.effect(c, pl, p.value)
		case "co":
			if _, ok := c.get("e"); !ok {
				err = a.skip("co", reasonApproximated)
			}
		case "a":
			err = a.angle(pl, p.value)
		case "b":
			err = a.background(pl, p.value)
		case "bo":
			err = a.border(pl, p.value)
		case "r":
			err = a.radius(pl, p.value)
		case "o":
			if err = opacity(&pl.Adjust.Opacity, p.value); err == nil {
				a.b.Ignore("o", reasonM2)
			}
		case "dpr":
			err = a.dpr(pl, p.value)
		case "f":
			err = a.format(p.value)
		case "q":
			err = a.quality(p.value)
		case "fl":
			err = a.flags(p.value)
		case "cs":
			err = a.colorSpace(p.value)
		case "d":
			err = a.defaultImage(p.value)
		case "pg":
			err = a.page(p.value)
		case "ac", "af", "br", "dl", "dn", "du", "eo", "fn", "fps", "ki", "p", "so", "sp", "vc", "vs":
			err = a.skip(p.key, reasonUnsupported)
		default:
			err = a.skip(optionName(p.key, p.value), reasonUnknown)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// named expands t_<name>[.<name>…] as presets.
func (a *applier) named(v string) error {
	names := strings.Split(v, ".")
	if slices.Contains(names, "") {
		return dialect.InvalidArgument("t", v, "named transformation")
	}
	return a.b.Apply("preset", names...)
}

// ---------------------------------------------------------------------------
// Geometry

// dimArg is a parsed w_/h_ value.
type dimArg struct {
	set      bool
	dim      transform.Dim
	auto     bool // w_auto
	step     int
	fallback int
}

func (a *applier) parseDim(field, v string) (dimArg, error) {
	if rest, ok := strings.CutPrefix(v, "auto"); ok && field == "w" {
		d := dimArg{auto: true}
		if rest == "" {
			return d, nil
		}
		parts := strings.Split(strings.TrimPrefix(rest, ":"), ":")
		if rest[0] != ':' || len(parts) > 2 {
			return dimArg{}, a.skip("w_auto", reasonApproximated)
		}
		n, err := strconv.Atoi(parts[0])
		if err != nil {
			// w_auto:breakpoints…: the hint applies, the breakpoints do not.
			return d, a.skip("w_auto", reasonApproximated)
		}
		if n < 0 || n > transform.MaxDimension {
			return dimArg{}, dialect.InvalidArgument(field, v, expDim)
		}
		d.step = n
		if len(parts) == 2 {
			f, err := strconv.Atoi(parts[1])
			if err != nil || f < 0 || f > transform.MaxDimension {
				return dimArg{}, dialect.InvalidArgument(field, v, expDim)
			}
			d.fallback = f
		}
		return d, nil
	}
	if strings.Contains(v, ".") {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || !(f > 0 && f <= transform.MaxRelative) {
			return dimArg{}, dialect.InvalidArgument(field, v, expDim)
		}
		return dimArg{set: true, dim: transform.Dim{Rel: f}}, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		if isExpression(v) {
			return dimArg{}, a.skip(field, reasonUnsupported)
		}
		return dimArg{}, dialect.InvalidArgument(field, v, expDim)
	}
	if n <= 0 || n > transform.MaxDimension {
		return dimArg{}, dialect.InvalidArgument(field, v, expDim)
	}
	return dimArg{set: true, dim: transform.Dim{Px: n}}, nil
}

// isExpression reports whether v is a Cloudinary arithmetic expression
// (iw_div_2, ih_mul_0.5, …).
func isExpression(v string) bool {
	return v != "" && v[0] >= 'a' && v[0] <= 'z'
}

func parseFloat(field, v string) (float64, error) {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f != f || f > transform.MaxDimension || f < -transform.MaxDimension {
		return 0, dialect.InvalidArgument(field, v, expNumber)
	}
	return f, nil
}

func parsePositive(field, v string, maxValue float64) (float64, error) {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || !(f > 0 && f <= maxValue) {
		return 0, dialect.InvalidArgument(field, v, expPos)
	}
	return f, nil
}

func parseAspectRatio(v string) (float64, error) {
	if w, h, ok := strings.Cut(v, ":"); ok {
		fw, err1 := strconv.ParseFloat(w, 64)
		fh, err2 := strconv.ParseFloat(h, 64)
		if err1 != nil || err2 != nil || !(fw > 0) || !(fh > 0) {
			return 0, dialect.InvalidArgument("ar", v, expRatio)
		}
		r := fw / fh
		if !(r > 0 && r <= transform.MaxFactor) {
			return 0, dialect.InvalidArgument("ar", v, expRatio)
		}
		return r, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || !(f > 0 && f <= transform.MaxFactor) {
		return 0, dialect.InvalidArgument("ar", v, expRatio)
	}
	return f, nil
}

// geo is the geometry of one component.
type geo struct {
	w, h       dimArg
	crop       string
	gravity    string
	x, y       float64
	hasX, hasY bool
	ar, zoom   float64
}

func (a *applier) parseGeo(c *component) (*geo, error) {
	g := &geo{}
	var err error
	for _, p := range c.params {
		if isVariableRef(p.value) {
			continue // recorded by base
		}
		switch p.key {
		case "w":
			g.w, err = a.parseDim("w", p.value)
		case "h":
			g.h, err = a.parseDim("h", p.value)
			if g.h.auto {
				g.h = dimArg{}
			}
		case "c":
			g.crop = p.value
		case "g":
			g.gravity = p.value
		case "x":
			g.x, err = parseFloat("x", p.value)
			g.hasX = err == nil
		case "y":
			g.y, err = parseFloat("y", p.value)
			g.hasY = err == nil
		case "ar":
			g.ar, err = parseAspectRatio(p.value)
		case "z":
			g.zoom, err = parsePositive("z", p.value, transform.MaxFactor)
		}
		if err != nil {
			return nil, err
		}
	}
	return g, nil
}

// gravity parses g_ with the component's offsets. Unsupported
// gravities are recorded (or 400 in strict mode) and fall back to centre.
func (a *applier) gravity(g *geo) (gr transform.Gravity, err error) {
	v := g.gravity
	name, _, _ := strings.Cut(v, ":")
	switch name {
	case "":
		if g.hasX || g.hasY {
			gr.Type = transform.GravityNorthWest
		}
	case "center", "centre":
		gr.Type = transform.GravityCenter
	case "north":
		gr.Type = transform.GravityNorth
	case "south":
		gr.Type = transform.GravitySouth
	case "east":
		gr.Type = transform.GravityEast
	case "west":
		gr.Type = transform.GravityWest
	case "north_east":
		gr.Type = transform.GravityNorthEast
	case "north_west":
		gr.Type = transform.GravityNorthWest
	case "south_east":
		gr.Type = transform.GravitySouthEast
	case "south_west":
		gr.Type = transform.GravitySouthWest
	case "auto":
		gr.Type = transform.GravitySmart
	case "face", "faces", "adv_face", "adv_faces", "adv_eyes":
		gr.Type = transform.GravityFace
		a.b.Ignore("g_"+name, reasonM3)
	case "xy_center":
		if !g.hasX && !g.hasY {
			gr.Type = transform.GravityCenter
			break
		}
		if g.x < 0 || g.x > 1 || g.y < 0 || g.y > 1 || (g.x == 1 && g.y == 1) {
			// Absolute centre coordinates need the source size.
			gr.Type = transform.GravityCenter
			return gr, a.skip("g_xy_center", reasonApproximated)
		}
		gr.Type = transform.GravityFocusPoint
		gr.FocusX, gr.FocusY = g.x, g.y
		return gr, nil
	case "custom", "body", "ocr_text", "liquid", "clip_region", "region", "track_person":
		gr.Type = transform.GravityCenter
		return gr, a.skip("g_"+name, reasonUnsupported)
	default:
		if !isObjectName(v) {
			return gr, dialect.InvalidArgument("g", v, expGravity)
		}
		gr.Type = transform.GravityObject
		gr.Classes = []string{v}
		a.b.Ignore("g_"+truncate(v), reasonM3)
	}
	gr.XOffset, gr.YOffset = g.x, g.y
	return gr, nil
}

func isObjectName(v string) bool {
	if v == "" || len(v) > maxQuoted {
		return false
	}
	for i := range len(v) {
		c := v[i]
		if (c < 'a' || c > 'z') && c != '_' {
			return false
		}
	}
	return true
}

// geometry applies w_, h_, c_, g_, x_, y_, ar_ and z_ of a component.
func (a *applier) geometry(c *component, pl *transform.Pipeline) error {
	if e, ok := c.get("e"); ok && strings.HasSuffix(effectName(e), "_region") {
		return nil // the effect consumes x/y/w/h
	}
	g, err := a.parseGeo(c)
	if err != nil {
		return err
	}
	if g.w.auto {
		a.r.ClientHints.Width = true
		a.r.ClientHints.WidthStep = g.w.step
		if g.w.fallback > 0 {
			g.w = dimArg{set: true, dim: transform.Dim{Px: g.w.fallback}, auto: true}
		}
	}
	mode := g.crop
	if strings.HasPrefix(mode, "imagga_") {
		if err := a.skip("c_"+mode, reasonUnsupported); err != nil {
			return err
		}
		mode = "scale"
	}
	hasDims := g.w.set || g.h.set || g.ar > 0
	gr, err := a.gravity(g)
	if err != nil {
		return err
	}
	if mode == "" {
		if !hasDims {
			// Gravity alone only anchors later operations.
			if gr.Type != transform.GravityUnset {
				pl.Gravity = gr
			}
			if g.zoom > 0 {
				return a.skip("z", reasonApproximated)
			}
			return nil
		}
		if pl.Resize.Type != transform.ResizeUnset {
			// A named transformation already chose the mode; the
			// component only overrides its dimensions.
			setDims(&pl.Resize, g)
			return nil
		}
		mode = "scale"
	}
	if mode == "crop" {
		return a.crop(pl, g, &gr)
	}

	rs := &pl.Resize
	setDims(rs, g)
	extend := func(enlarge bool) {
		rs.Type, rs.Enlarge = transform.ResizeFit, enlarge
		pl.Extend = transform.Extend{Enabled: true, Gravity: padGravity(&gr)}
	}
	switch mode {
	case "scale":
		rs.Type, rs.Enlarge = transform.ResizeFit, true
		if (g.w.set && g.h.set) || g.ar > 0 {
			rs.Type = transform.ResizeForce
		}
	case "fit":
		rs.Type, rs.Enlarge = transform.ResizeFit, true
	case "limit":
		rs.Type = transform.ResizeFit
	case "mfit":
		rs.Type, rs.Enlarge = transform.ResizeOutside, true
	case "fill", "thumb", "auto":
		rs.Type, rs.Enlarge = transform.ResizeFill, true
		if mode == "auto" && gr.Type == transform.GravityUnset {
			gr.Type = transform.GravitySmart
		}
		if g.zoom > 0 {
			if mode == "thumb" {
				gr.FocusZoom = g.zoom
			} else if err := a.skip("z", reasonApproximated); err != nil {
				return err
			}
		}
		pl.Gravity = gr
	case "lfill":
		rs.Type = transform.ResizeFillDown
		pl.Gravity = gr
	case "fill_pad":
		rs.Type, rs.Enlarge = transform.ResizeFill, true
		pl.Gravity = gr
		pl.Extend = transform.Extend{Enabled: true}
		return a.skip("c_fill_pad", reasonApproximated)
	case "pad", "auto_pad":
		extend(true)
	case "lpad":
		extend(false)
	case "mpad":
		extend(false)
		return a.skip("c_mpad", reasonApproximated)
	default:
		return dialect.InvalidArgument("c", mode, expCrop)
	}
	return nil
}

func setDims(rs *transform.Resize, g *geo) {
	if g.w.set {
		rs.Width = g.w.dim
	}
	if g.h.set {
		rs.Height = g.h.dim
	}
	if g.ar > 0 {
		rs.AspectRatio = g.ar
	}
}

// padGravity is the extend anchor for pad modes: content-aware gravity
// has no meaning there.
func padGravity(g *transform.Gravity) transform.Gravity {
	if g.Type.IsCompass() {
		return transform.Gravity{Type: g.Type}
	}
	return transform.Gravity{}
}

// crop applies c_crop: a pre-resize crop without scaling.
func (a *applier) crop(pl *transform.Pipeline, g *geo, gr *transform.Gravity) error {
	if !g.w.set && !g.h.set && g.ar == 0 {
		return a.skip("c_crop", reasonApproximated)
	}
	cr := &transform.Crop{Gravity: *gr}
	if g.w.set {
		cr.Width = g.w.dim
	}
	if g.h.set {
		cr.Height = g.h.dim
	}
	if g.ar > 0 {
		cr.AspectRatio = g.ar
		a.b.Ignore("ar", reasonM2)
	}
	if g.zoom > 0 {
		cr.Gravity.FocusZoom = g.zoom
	}
	pl.Crop = cr
	return nil
}

// ---------------------------------------------------------------------------
// Effects

func effectName(v string) string {
	name, _, _ := strings.Cut(v, ":")
	return name
}

// effectArg parses the first ":" argument of an effect as an integer in
// [lo, hi], or returns def when absent.
func effectArg(field, v string, def, lo, hi int) (int, error) {
	_, rest, ok := strings.Cut(v, ":")
	if !ok || rest == "" {
		return def, nil
	}
	arg, _, _ := strings.Cut(rest, ":")
	n, err := strconv.Atoi(arg)
	if err != nil || n < lo || n > hi {
		return 0, dialect.InvalidArgument(field, v, expEffect)
	}
	return n, nil
}

// unsupportedEffects are known Cloudinary effects without an equivalent.
var unsupportedEffects = map[string]bool{
	"accelerate": true, "adv_redeye": true, "anti_removal": true, "art": true,
	"assist_colorblind": true, "auto_brightness": true, "auto_color": true,
	"auto_contrast": true, "background_removal": true, "bgremoval": true,
	"blue_to_purple": true, "boomerang": true, "brightness_hsb": true,
	"cartoonify": true, "deshake": true, "displace": true, "distort": true,
	"dropshadow": true, "enhance": true, "extract": true, "fade": true,
	"fill_light": true, "gen_background_replace": true, "gen_recolor": true,
	"gen_remove": true, "gen_replace": true, "gen_restore": true, "gradient_fade": true,
	"green_to_blue": true, "improve": true, "loop": true, "make_transparent": true,
	"noise": true, "oil_paint": true, "opacity_threshold": true, "ordered_dither": true,
	"outline": true, "preview": true, "recolor": true, "red_to_blue": true,
	"redeye": true, "replace_color": true, "reverse": true, "shadow": true,
	"shear": true, "simulate_colorblind": true, "style_transfer": true, "theme": true,
	"tint": true, "transition": true, "upscale": true, "vectorize": true,
	"viesus_correct": true, "vignette": true, "volume": true, "zoompan": true,
	"auto_enhance": true, "lut": true, "pixelate_region": true,
}

//nolint:gocyclo // one flat switch over Cloudinary's effect table is the clearest form
func (a *applier) effect(c *component, pl *transform.Pipeline, v string) error {
	name := effectName(v)
	field := "e_" + name
	adj := &pl.Adjust
	var err error
	m2 := func() { a.b.Ignore(field, reasonM2) }
	switch name {
	case "blur":
		var n int
		if n, err = effectArg(field, v, 100, 1, 2000); err == nil {
			pl.Blur = float64(n) / 20
		}
	case "sharpen":
		var n int
		if n, err = effectArg(field, v, 100, 1, 2000); err == nil {
			pl.Sharpen = float64(n) / 100
		}
	case "unsharp_mask":
		var n int
		if n, err = effectArg(field, v, 100, 1, 2000); err == nil {
			pl.Unsharp = &transform.Unsharp{Sigma: 1, Amount: float64(n) / 100}
			m2()
		}
	case "pixelate":
		var n int
		if n, err = effectArg(field, v, 10, 1, 200); err == nil {
			pl.Pixelate = n
		}
	case "grayscale":
		pl.Grayscale = true
		m2()
	case "blackwhite":
		pl.Grayscale = true
		m2()
		err = a.skip(field, reasonApproximated)
	case "sepia":
		pl.Sepia = true
		m2()
		if strings.Contains(v, ":") {
			err = a.skip(field, reasonApproximated)
		}
	case "negate":
		pl.Negate = true
		m2()
	case "brightness":
		var n int
		if n, err = effectArg(field, v, 80, -99, 100); err == nil {
			adj.Brightness = new(float64(n) * 2.55)
			m2()
		}
	case "contrast":
		err = a.multiplier(&adj.Contrast, field, v, 0, -100, 100)
	case "saturation":
		err = a.multiplier(&adj.Saturation, field, v, 80, -100, 100)
	case "gamma":
		err = a.multiplier(&adj.Gamma, field, v, 0, -50, 150)
	case "vibrance":
		err = a.multiplier(&adj.Vibrance, field, v, 20, -100, 100)
	case "red":
		err = a.multiplier(&adj.Red, field, v, 0, -100, 100)
	case "green":
		err = a.multiplier(&adj.Green, field, v, 0, -100, 100)
	case "blue":
		err = a.multiplier(&adj.Blue, field, v, 0, -100, 100)
	case "hue":
		var n int
		if n, err = effectArg(field, v, 80, -100, 100); err == nil {
			adj.Hue = new(float64(n) * 1.8)
			m2()
		}
	case "colorize":
		var n int
		if n, err = effectArg(field, v, 100, 0, 100); err != nil {
			break
		}
		col := transform.Color{R: 0x80, G: 0x80, B: 0x80, A: 255}
		if co, ok := c.get("co"); ok {
			if col, err = parseColor("co", co); err != nil {
				break
			}
		}
		pl.Colorize = &transform.Colorize{Opacity: float64(n) / 100, Color: col}
		m2()
	case "trim":
		err = a.trim(pl, v)
	case "blur_faces":
		var n int
		if n, err = effectArg(field, v, 500, 1, 2000); err == nil {
			pl.BlurDetections = &transform.DetectionFilter{Sigma: float64(n) / 20, Classes: []string{"face"}}
			a.b.Ignore(field, reasonM3)
		}
	case "pixelate_faces":
		var n int
		if n, err = effectArg(field, v, 10, 1, 200); err == nil {
			pl.BlurDetections = &transform.DetectionFilter{Pixelate: n, Classes: []string{"face"}}
			a.b.Ignore(field, reasonM3)
		}
	case "blur_region":
		err = a.blurRegion(c, pl, v)
	default:
		reason := reasonUnknown
		if unsupportedEffects[name] {
			reason = reasonUnsupported
		}
		err = a.skip(truncate(field), reason)
	}
	return err
}

// multiplier sets an adjustment 1 + n/100 from e_<name>[:n].
func (a *applier) multiplier(dst **float64, field, v string, def, lo, hi int) error {
	n, err := effectArg(field, v, def, lo, hi)
	if err != nil {
		return err
	}
	*dst = new(1 + float64(n)/100)
	a.b.Ignore(field, reasonM2)
	return nil
}

// trim is e_trim[:tolerance[:color]].
func (a *applier) trim(pl *transform.Pipeline, v string) error {
	parts := strings.Split(v, ":")
	t := &transform.Trim{Threshold: 10}
	if len(parts) > 1 && parts[1] != "" {
		n, err := strconv.Atoi(parts[1])
		if err != nil || n < 0 || n > 100 {
			return dialect.InvalidArgument("e_trim", v, expPercent)
		}
		t.Threshold = float64(n)
	}
	if len(parts) > 2 {
		c, err := parseColor("e_trim", strings.Join(parts[2:], ":"))
		if err != nil {
			return err
		}
		t.Color = &c
	}
	pl.Trim = t
	return nil
}

// blurRegion is e_blur_region[:n] with an optional region in the
// component's relative x_, y_, w_ and h_.
func (a *applier) blurRegion(c *component, pl *transform.Pipeline, v string) error {
	n, err := effectArg("e_blur_region", v, 100, 1, 2000)
	if err != nil {
		return err
	}
	sigma := float64(n) / 20
	vals := [4]float64{0, 0, 1, 1}
	hasRegion := false
	for i, k := range [4]string{"x", "y", "w", "h"} {
		s, ok := c.get(k)
		if !ok {
			continue
		}
		hasRegion = true
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || f < 0 || f > 1 || !strings.Contains(s, ".") && f != 0 {
			// Pixel regions need the source size.
			return a.skip("e_blur_region", reasonApproximated)
		}
		vals[i] = f
	}
	if !hasRegion {
		pl.Blur = sigma
		return nil
	}
	area := transform.Area{Left: vals[0], Top: vals[1], Width: min(vals[2], 1-vals[0]), Height: min(vals[3], 1-vals[1])}
	pl.BlurAreas = &transform.BlurAreas{Sigma: sigma, Areas: []transform.Area{area}}
	a.b.Ignore("e_blur_region", reasonM2)
	return nil
}

// ---------------------------------------------------------------------------
// Rotation, background, border, radius, opacity

func (a *applier) angle(pl *transform.Pipeline, v string) error {
	for part := range strings.SplitSeq(v, ".") {
		switch part {
		case "hflip":
			pl.Flip.H = !pl.Flip.H
		case "vflip":
			pl.Flip.V = !pl.Flip.V
		case "exif":
			on := true
			pl.AutoRotate = &on
		case "ignore":
			off := false
			pl.AutoRotate = &off
		case "auto_right", "auto_left", "auto":
			if err := a.skip("a_"+part, reasonUnsupported); err != nil {
				return err
			}
		default:
			deg, err := strconv.ParseFloat(part, 64)
			if err != nil || deg != deg || deg < -360*100 || deg > 360*100 {
				return dialect.InvalidArgument("a", v, expAngle)
			}
			if q := deg / 90; q == float64(int(q)) {
				pl.Rotate = ((pl.Rotate+int(q)*90)%360 + 360) % 360
				continue
			}
			pl.RotateAny += deg
			a.b.Ignore("a", reasonM2)
		}
	}
	return nil
}

func (a *applier) background(pl *transform.Pipeline, v string) error {
	name, _, _ := strings.Cut(v, ":")
	switch name {
	case "auto", "blurred", "gen_fill":
		return a.skip("b_"+name, reasonUnsupported)
	}
	c, err := parseColor("b", v)
	if err != nil {
		return err
	}
	pl.Background = &c
	return nil
}

func (a *applier) border(pl *transform.Pipeline, v string) error {
	w, color, ok := strings.Cut(v, "px_solid_")
	if !ok {
		return dialect.InvalidArgument("bo", v, expBorder)
	}
	n, err := strconv.Atoi(w)
	if err != nil || n < 1 || n > transform.MaxDimension {
		return dialect.InvalidArgument("bo", v, expBorder)
	}
	c, err := parseColor("bo", color)
	if err != nil {
		return err
	}
	pl.Border = &transform.Border{Top: n, Right: n, Bottom: n, Left: n, Color: c}
	a.b.Ignore("bo", reasonM2)
	return nil
}

// radius is r_<n>, r_<tl/br>:<tr/bl>, r_<tl>:<tr/bl>:<br>, r_<tl>:<tr>:<br>:<bl>
// or r_max.
func (a *applier) radius(pl *transform.Pipeline, v string) error {
	a.b.Ignore("r", reasonM2)
	if v == "max" {
		pl.Radius = &transform.Radius{Max: true}
		return nil
	}
	parts := strings.Split(v, ":")
	if len(parts) > 4 {
		return dialect.InvalidArgument("r", v, expRadius)
	}
	n := make([]int, len(parts))
	for i, s := range parts {
		x, err := strconv.Atoi(s)
		if err != nil || x < 0 || x > transform.MaxDimension {
			return dialect.InvalidArgument("r", v, expRadius)
		}
		n[i] = x
	}
	r := &transform.Radius{}
	switch len(n) {
	case 1:
		r.TopLeft, r.TopRight, r.BottomRight, r.BottomLeft = n[0], n[0], n[0], n[0]
	case 2:
		r.TopLeft, r.TopRight, r.BottomRight, r.BottomLeft = n[0], n[1], n[0], n[1]
	case 3:
		r.TopLeft, r.TopRight, r.BottomRight, r.BottomLeft = n[0], n[1], n[2], n[1]
	case 4:
		r.TopLeft, r.TopRight, r.BottomRight, r.BottomLeft = n[0], n[1], n[2], n[3]
	}
	pl.Radius = r
	return nil
}

// opacity parses o_<0-100> as a 0..1 fraction.
func opacity(dst **float64, v string) error {
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 100 {
		return dialect.InvalidArgument("o", v, expPercent)
	}
	*dst = new(float64(n) / 100)
	return nil
}

// ---------------------------------------------------------------------------
// Output and delivery

func (a *applier) dpr(pl *transform.Pipeline, v string) error {
	if v == "auto" {
		a.r.ClientHints.DPR = true
		return nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || !(f > 0 && f <= transform.MaxFactor) {
		return dialect.InvalidArgument("dpr", v, expDPR)
	}
	pl.Resize.DPR = f
	return nil
}

// formatByName maps Cloudinary format names and extensions.
func formatByName(s string) (transform.Format, bool) {
	switch strings.ToLower(s) {
	case "jpg", "jpeg", "jpe":
		return transform.FormatJPEG, true
	case "png":
		return transform.FormatPNG, true
	case "webp":
		return transform.FormatWebP, true
	case "avif":
		return transform.FormatAVIF, true
	case "jxl":
		return transform.FormatJXL, true
	case "gif":
		return transform.FormatGIF, true
	case "heic", "heif":
		return transform.FormatHEIC, true
	case "tif", "tiff":
		return transform.FormatTIFF, true
	case "bmp":
		return transform.FormatBMP, true
	case "ico":
		return transform.FormatICO, true
	case "svg":
		return transform.FormatSVG, true
	case "mp4":
		return transform.FormatMP4, true
	case "webm":
		return transform.FormatWebM, true
	}
	return transform.FormatUnset, false
}

func (a *applier) format(v string) error {
	a.explicitFormat = true
	if v == "auto" || strings.HasPrefix(v, "auto:") {
		a.r.Output.Format = transform.FormatUnset
		return nil
	}
	f, ok := formatByName(v)
	if !ok {
		return dialect.InvalidArgument("f", v, expFormat)
	}
	if f == transform.FormatMP4 || f == transform.FormatWebM {
		a.b.Ignore("f_"+v, reasonM3)
	}
	a.r.Output.Format = f
	return nil
}

// extension records the public ID's extension as the fallback format.
func (a *applier) extension(key string) {
	i := strings.LastIndexByte(key, '.')
	if i < 0 || strings.Contains(key[i:], "/") {
		return
	}
	if f, ok := formatByName(key[i+1:]); ok {
		a.extFormat = f
	}
}

// finishFormat applies the extension unless f_ was given.
func (a *applier) finishFormat() {
	if !a.explicitFormat && a.extFormat != transform.FormatUnset {
		a.r.Output.Format = a.extFormat
		if a.extFormat == transform.FormatMP4 || a.extFormat == transform.FormatWebM {
			a.b.Ignore("extension", reasonM3)
		}
	}
}

// autoQualityTargets are the DSSIM targets of q_auto levels.
var autoQualityTargets = map[string]float64{
	"best": 0.005,
	"good": 0.02,
	"eco":  0.035,
	"low":  0.05,
}

func (a *applier) quality(v string) error {
	o := &a.r.Output
	if rest, ok := strings.CutPrefix(v, "auto"); ok && (rest == "" || rest[0] == ':') {
		level := "good"
		if rest != "" {
			level, _, _ = strings.Cut(rest[1:], ":")
		}
		t, ok := autoQualityTargets[level]
		if !ok {
			return dialect.InvalidArgument("q", v, expQuality)
		}
		o.Quality = 0
		o.AutoQuality = &transform.AutoQuality{Method: transform.AutoQualityDSSIM, Target: t}
		a.b.Ignore("q_auto", reasonM2)
		return nil
	}
	if strings.HasPrefix(v, "jpegmini") {
		return a.skip("q_jpegmini", reasonUnsupported)
	}
	q, sub, hasSub := strings.Cut(v, ":")
	n, err := strconv.Atoi(q)
	if err != nil || n < 1 || n > transform.MaxQuality {
		return dialect.InvalidArgument("q", v, expQuality)
	}
	o.Quality, o.AutoQuality = n, nil
	if hasSub {
		switch sub {
		case "420":
			o.ChromaSubsampling = transform.Subsampling420
		case "422":
			o.ChromaSubsampling = transform.Subsampling422
		case "444":
			o.ChromaSubsampling = transform.Subsampling444
		default:
			return dialect.InvalidArgument("q", v, expQuality)
		}
	}
	return nil
}

// flags applies fl_<flag>[.<flag>…].
//
//nolint:gocyclo // one flat switch over Cloudinary's flag table is the clearest form
func (a *applier) flags(v string) error {
	o, d := &a.r.Output, &a.r.Delivery
	for f := range strings.SplitSeq(v, ".") {
		name, arg, _ := strings.Cut(f, ":")
		var err error
		switch name {
		case "lossy":
			o.Lossless = new(false)
		case "progressive":
			o.Progressive = new(arg != "none")
		case "strip_profile":
			o.StripColorProfile = new(true)
		case "keep_iptc":
			o.StripMetadata = new(false)
		case "keep_attribution":
			o.KeepCopyright = new(true)
		case "force_strip":
			o.StripMetadata = new(true)
		case "attachment", "streaming_attachment":
			d.Attachment = new(true)
			if arg != "" {
				d.Filename = arg
			}
		case "immutable_cache":
			d.Immutable = true
		case "png8":
			o.PNG = &transform.PNGOptions{Quantize: new(true), QuantizationColors: 256}
			a.b.Ignore("fl_png8", reasonM2)
		case "getinfo":
			a.r.Kind = transform.KindInfo
			a.r.Info = &transform.InfoOptions{Size: true, Format: true, Dimensions: true}
		case "animated", "awebp", "apng", "preserve_transparency", "any_format",
			"png24", "png32", "ignore_aspect_ratio", "layer_apply", "relative",
			"region_relative", "no_overflow", "tiled", "keep_dar":
			// Default behaviour here, or a layer flag handled by layer.
		default:
			reason := reasonUnknown
			switch name {
			case "clip", "clip_evenodd", "cutter", "rasterize", "sanitize", "text_no_trim",
				"text_disallow_overflow", "force_icc", "hlsv3", "mono", "no_stream", "splice",
				"truncate_ts", "waveform", "tiff8_lzw", "ignore_mask_channels", "custom_function",
				"draco", "getinfo_page", "c2pa":
				reason = reasonUnsupported
			}
			err = a.skip(truncate("fl_"+name), reason)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *applier) colorSpace(v string) error {
	o := &a.r.Output
	switch v {
	case "srgb", "no_cmyk", "true_color":
		o.ColorProfile = "srgb"
	case "tinysrgb":
		o.ColorProfile = "srgb"
		if err := a.skip("cs_tinysrgb", reasonApproximated); err != nil {
			return err
		}
	case "cmyk":
		o.ColorProfile = "cmyk"
	default:
		return a.skip(optionName("cs", v), reasonUnsupported)
	}
	a.b.Ignore("cs", reasonM2)
	return nil
}

// defaultImage is d_<public id> (":" separates folders), resolved against
// the same source as the image.
func (a *applier) defaultImage(v string) error {
	key := strings.ReplaceAll(v, ":", "/")
	if err := checkKey(key); err != nil {
		return err
	}
	if !imgproxy.PlausibleSource(key) {
		return dialect.InvalidArgument("d", v, "public id")
	}
	a.r.Delivery.Fallback = &transform.SourceSpec{Raw: key, Encoding: transform.SourceRelative, Mount: a.mount}
	a.b.Ignore("d", reasonM2)
	return nil
}

// page is pg_<n> (1-based); ranges and names are unsupported.
func (a *applier) page(v string) error {
	n, err := strconv.Atoi(v)
	if err != nil {
		if strings.ContainsAny(v, "-;") || strings.HasPrefix(v, "name:") || strings.HasPrefix(v, "embedded:") {
			return a.skip("pg", reasonUnsupported)
		}
		return dialect.InvalidArgument("pg", v, expPage)
	}
	if n < 1 {
		return dialect.InvalidArgument("pg", v, expPage)
	}
	page := n - 1
	a.r.Pipelines[0].Page = &page
	a.b.Ignore("pg", reasonM2)
	return nil
}
