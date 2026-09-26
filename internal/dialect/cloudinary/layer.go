package cloudinary

import (
	"encoding/base64"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// layer turns an image overlay into a watermark on the current pipeline.
// comps is the layer group: the l_ component alone (legacy syntax, where
// placement and layer parameters share the component), or the l_
// component, the components transforming the layer and the one carrying
// fl_layer_apply. Watermarks take effect in M3.
//
// Mapped: g_ → position, x_/y_ → offsets, o_ → opacity, a_ → rotation,
// w_/h_ in pixels → size, a relative w_ with fl_relative → scale of the
// base image, fl_tiled → repeat. Other parameters of the group are
// recorded as approximated. Text, video, subtitle and LUT layers are
// unsupported.
func (a *applier) layer(key, v string, comps []*component) error {
	kind, rest, _ := strings.Cut(v, ":")
	var src *transform.SourceSpec
	switch kind {
	case "text", "video", "subtitles", "lut", "audio":
		return a.skip(key+"_"+kind, reasonUnsupported)
	case "fetch":
		s, err := layerFetch(rest)
		if err != nil {
			return err
		}
		src = &transform.SourceSpec{Raw: s, Encoding: transform.SourcePlain}
	default:
		id := v
		if strings.HasPrefix(v, "image:") {
			id = rest
		}
		key := strings.ReplaceAll(id, ":", "/")
		if checkKey(key) != nil || !imgproxy.PlausibleSource(key) {
			return dialect.InvalidArgument("l", v, expLayer)
		}
		src = &transform.SourceSpec{Raw: key, Encoding: transform.SourceRelative, Mount: a.mount}
	}

	wm := transform.Watermark{Opacity: 1, Source: src}
	var (
		w, h     dimArg
		relative bool
		gx, gy   float64
		hasXY    bool
		gravity  string
	)
	for _, c := range comps {
		relative = relative || c.hasFlag("relative")
		for _, p := range c.params {
			var err error
			switch p.key {
			case "l":
			case "g":
				gravity = p.value
			case "x":
				gx, err = parseFloat("x", p.value)
				hasXY = true
			case "y":
				gy, err = parseFloat("y", p.value)
				hasXY = true
			case "o":
				var o *float64
				if err = opacity(&o, p.value); err == nil {
					wm.Opacity = *o
				}
			case "a":
				var deg float64
				if deg, err = strconv.ParseFloat(p.value, 64); err != nil || deg != deg || deg < -360 || deg > 360 {
					err = a.skip("l_a", reasonApproximated)
				} else {
					wm.Rotate = deg
				}
			case "w":
				w, err = a.parseDim("w", p.value)
			case "h":
				h, err = a.parseDim("h", p.value)
			case "fl":
				for f := range strings.SplitSeq(p.value, ".") {
					switch flagName(f) {
					case "tiled":
						wm.Tile = transform.TileRepeat
					case "layer_apply", "relative", "no_overflow":
					default:
						err = a.skip("l_fl_"+truncate(flagName(f)), reasonApproximated)
					}
					if err != nil {
						break
					}
				}
			case "c":
				if p.value != "scale" && p.value != "fit" && p.value != "limit" {
					err = a.skip("l_c_"+truncate(p.value), reasonApproximated)
				}
			default:
				err = a.skip("l_"+optionName(p.key, p.value), reasonApproximated)
			}
			if err != nil {
				return err
			}
		}
	}
	if gravity != "" || hasXY {
		gr, err := a.gravity(&geo{gravity: gravity, x: gx, y: gy, hasX: hasXY, hasY: hasXY})
		if err != nil {
			return err
		}
		if gr.Type.IsDetection() || gr.Type == transform.GravitySmart || gr.Type == transform.GravityFocusPoint {
			gr = transform.Gravity{Type: transform.GravityCenter}
			if err := a.skip("l_g", reasonApproximated); err != nil {
				return err
			}
		}
		wm.Position = gr
	}
	switch {
	case w.set && w.dim.Rel > 0 && relative:
		wm.Scale = w.dim.Rel
	case w.set && w.dim.Px > 0:
		wm.Width = w.dim.Px
	case w.set:
		if err := a.skip("l_w", reasonApproximated); err != nil {
			return err
		}
	}
	if h.set && h.dim.Px > 0 {
		wm.Height = h.dim.Px
	} else if h.set {
		if err := a.skip("l_h", reasonApproximated); err != nil {
			return err
		}
	}
	a.used = true
	pl := a.b.Pipeline()
	if len(pl.Watermarks) >= transform.MaxWatermarks {
		return dialect.InvalidURL("too many layers")
	}
	pl.Watermarks = append(pl.Watermarks, wm)
	a.b.Ignore("l", reasonM3)
	return nil
}

// layerFetch decodes l_fetch:<base64 url> (standard or URL-safe alphabet,
// padding optional, as the SDKs emit it).
func layerFetch(s string) (string, error) {
	s = strings.TrimRight(s, "=")
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		b, err = base64.RawStdEncoding.DecodeString(s)
	}
	if err != nil {
		return "", dialect.InvalidArgument("l", s, expLayer)
	}
	u, ok := fetchURL(string(b))
	if !ok || !imgproxy.PlausibleSource(u) {
		return "", dialect.InvalidArgument("l", s, expLayer)
	}
	return u, nil
}
