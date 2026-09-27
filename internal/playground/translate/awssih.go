package translate

import (
	"encoding/base64"
	"encoding/json"
	"math"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

var sihPositions = map[transform.GravityType]string{
	transform.GravityNorth: "north", transform.GravitySouth: "south", transform.GravityEast: "east",
	transform.GravityWest: "west", transform.GravityNorthEast: "northeast", transform.GravityNorthWest: "northwest",
	transform.GravitySouthEast: "southeast", transform.GravitySouthWest: "southwest",
	transform.GravityEntropy: "entropy", transform.GravitySmart: "attention",
}

var sihFormats = map[transform.Format]string{
	transform.FormatJPEG: "jpeg", transform.FormatPNG: "png", transform.FormatWebP: "webp", transform.FormatAVIF: "avif",
	transform.FormatGIF: "gif", transform.FormatTIFF: "tiff", transform.FormatHEIC: "heif", transform.FormatJXL: "jxl",
}

// sihColor is sharp's {r, g, b, alpha}.
func sihColor(c transform.Color) map[string]any {
	return map[string]any{"r": c.R, "g": c.G, "b": c.B, "alpha": round4(float64(c.A) / 255)}
}

// emitAWSSIH writes the base64 JSON request {"key", "edits"}.
//
//nolint:gocyclo // one flat walk over the pipeline model
func emitAWSSIH(e *emitter) (string, string, error) {
	if _, ok := e.src.keyOn(e.mountSource); !ok {
		return "", "", errSource("AWS SIH takes an object key on the mount's source (its bucket)")
	}
	p := e.pl()
	edits := map[string]any{}
	rs := &p.Resize
	if !rs.Width.IsZero() || !rs.Height.IsZero() {
		r := map[string]any{}
		if rs.Width.Px > 0 {
			r["width"] = rs.Width.Px
		}
		if rs.Height.Px > 0 {
			r["height"] = rs.Height.Px
		}
		switch {
		case p.Extend.Enabled && rs.Type == transform.ResizeFit:
			r["fit"] = "contain"
			if p.Background != nil {
				r["background"] = sihColor(*p.Background)
			}
			if pos, ok := sihPositions[p.Extend.Gravity.Type]; ok {
				r["position"] = pos
			}
		case rs.Type == transform.ResizeFill || rs.Type == transform.ResizeFillDown:
			if pos, ok := sihPositions[p.Gravity.Type]; ok && p.Gravity.XOffset == 0 && p.Gravity.YOffset == 0 {
				r["position"] = pos
			}
		case rs.Type == transform.ResizeForce:
			r["fit"] = "fill"
		case rs.Type == transform.ResizeOutside:
			r["fit"] = "outside"
		default:
			r["fit"] = "inside"
		}
		if !rs.Enlarge {
			r["withoutEnlargement"] = true
		}
		if rs.Algorithm != transform.KernelUnset && rs.Algorithm != transform.KernelLanczos3 {
			r["kernel"] = rs.Algorithm.String()
		}
		edits["resize"] = r
	}
	if t := p.Trim; t != nil {
		tr := map[string]any{"threshold": t.Threshold}
		if t.Color != nil {
			tr["background"] = sihColor(*t.Color)
		}
		edits["trim"] = tr
	}
	if pd := p.Padding; pd != nil {
		ex := map[string]any{"top": pd.Top, "right": pd.Right, "bottom": pd.Bottom, "left": pd.Left}
		if p.Background != nil {
			ex["background"] = sihColor(*p.Background)
		}
		edits["extend"] = ex
	}
	// SIH's rotate: null is auto-rotation, the default here.
	if deg := float64(p.Rotate) + p.RotateAny; deg != 0 {
		edits["rotate"] = deg
	}
	if p.Flip.V {
		edits["flip"] = true
	}
	if p.Flip.H {
		edits["flop"] = true
	}
	if p.Grayscale {
		edits["grayscale"] = true
	}
	if p.Equalize {
		edits["normalize"] = true
	}
	if p.Sharpen > 0 {
		edits["sharpen"] = map[string]any{"sigma": p.Sharpen}
	}
	if p.Blur >= 0.3 {
		edits["blur"] = p.Blur
	}
	a := &p.Adjust
	mod := map[string]any{}
	if a.Exposure != nil {
		mod["brightness"] = *a.Exposure
	}
	if a.Saturation != nil {
		mod["saturation"] = *a.Saturation
	}
	if a.Hue != nil {
		mod["hue"] = math.Round(*a.Hue)
	}
	if len(mod) > 0 {
		edits["modulate"] = mod
	}
	o := &e.r.Output
	if f, ok := sihFormats[o.Format]; ok {
		edits["toFormat"] = f
		if o.Quality > 0 {
			edits[f] = map[string]any{"quality": o.Quality}
		}
	}
	if o.StripMetadata != nil && *o.StripMetadata {
		edits["stripExif"] = true
	}
	if o.StripColorProfile != nil && *o.StripColorProfile {
		edits["stripIcc"] = true
	}
	if p.DisableAnimation {
		edits["animated"] = false
	} else if p.Pages != nil && *p.Pages == -1 {
		edits["animated"] = true
	}
	body := map[string]any{"key": e.src.ref.Path}
	if len(edits) > 0 {
		body["edits"] = edits
	}
	b, err := json.Marshal(body)
	if err != nil {
		return "", "", err
	}
	return "/" + base64.StdEncoding.EncodeToString(b), "", nil
}
