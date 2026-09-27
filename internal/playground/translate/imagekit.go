package translate

import (
	"math"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

var imagekitCompass = compassNames{
	transform.GravityCenter: "center", transform.GravityNorth: "top", transform.GravitySouth: "bottom",
	transform.GravityEast: "right", transform.GravityWest: "left", transform.GravityNorthEast: "top_right",
	transform.GravityNorthWest: "top_left", transform.GravitySouthEast: "bottom_right", transform.GravitySouthWest: "bottom_left",
}

var imagekitFormats = map[transform.Format]string{
	transform.FormatJPEG: "jpg", transform.FormatPNG: "png", transform.FormatWebP: "webp", transform.FormatAVIF: "avif",
	transform.FormatJXL: "jxl", transform.FormatGIF: "gif", transform.FormatMP4: "mp4", transform.FormatWebM: "webm",
}

// imagekitFocus is fo-<value>, or "".
func imagekitFocus(g *transform.Gravity) string {
	switch g.Type {
	case transform.GravitySmart:
		return "auto"
	case transform.GravityFace:
		if g.FaceIndex == nil && g.FacePad == 0 {
			return "face"
		}
	case transform.GravityObject:
		return strings.Join(g.Classes, "_")
	default:
		if n, ok := imagekitCompass[g.Type]; ok && g.XOffset == 0 && g.YOffset == 0 && g.Type != transform.GravityCenter {
			return n
		}
	}
	return ""
}

// imagekitStep is the parameters of one pipeline.
//
//nolint:gocyclo,funlen // one flat walk over the pipeline model
func imagekitStep(p *transform.Pipeline) []string {
	var s []string
	rs := &p.Resize
	w, hasW := dim(rs.Width)
	h, hasH := dim(rs.Height)
	if hasW {
		s = append(s, "w-"+w)
	}
	if hasH {
		s = append(s, "h-"+h)
	}
	if rs.AspectRatio > 0 {
		s = append(s, "ar-"+num(round4(rs.AspectRatio))+"-1")
	}
	focus := p.Gravity
	if hasW || hasH {
		switch {
		case p.Extend.Enabled && rs.Type == transform.ResizeFit:
			if rs.Enlarge {
				s = append(s, "cm-pad_resize")
			} else {
				s = append(s, "cm-pad_resize_no_enlarge")
			}
			focus = p.Extend.Gravity
		case rs.Type == transform.ResizeFill && !rs.Enlarge, rs.Type == transform.ResizeFillDown:
			s = append(s, "c-maintain_ratio_no_enlarge")
		case rs.Type == transform.ResizeForce:
			s = append(s, "c-force")
		case rs.Type == transform.ResizeOutside:
			s = append(s, "c-at_least")
		case rs.Type == transform.ResizeFit && rs.Enlarge:
			s = append(s, "c-at_max_enlarge")
		case rs.Type == transform.ResizeFit || rs.Type == transform.ResizeUnset:
			s = append(s, "c-at_max")
		}
	}
	if f := imagekitFocus(&focus); f != "" {
		s = append(s, "fo-"+f)
	}
	if rs.DPR != 0 && rs.DPR != 1 {
		s = append(s, "dpr-"+num(rs.DPR))
	}
	if bg := p.Background; bg != nil {
		s = append(s, "bg-"+strings.ToUpper(hexRGB(*bg)))
	}
	if p.Blur > 0 {
		s = append(s, "bl-"+num(math.Round(p.Blur*2)))
	}
	switch {
	case p.Sharpen == 1:
		s = append(s, "e-sharpen")
	case p.Sharpen > 0.5:
		s = append(s, "e-sharpen-"+num(math.Round((p.Sharpen-0.5)*50)))
	}
	if p.Grayscale {
		s = append(s, "e-grayscale")
	}
	if r := p.Radius; r != nil {
		switch {
		case r.Max:
			s = append(s, "r-max")
		case sameSides(r.TopLeft, r.TopRight, r.BottomRight, r.BottomLeft):
			s = append(s, "r-"+itoa(r.TopLeft))
		default:
			s = append(s, "r-"+itoa(r.TopLeft)+"_"+itoa(r.TopRight)+"_"+itoa(r.BottomRight)+"_"+itoa(r.BottomLeft))
		}
	}
	if b := p.Border; b != nil && sameSides(b.Top, b.Right, b.Bottom, b.Left) {
		s = append(s, "b-"+itoa(b.Top)+"_"+strings.ToUpper(hexRGB(b.Color)))
	}
	if rot := p.Rotate; rot != 0 || p.RotateAny != 0 {
		deg := float64(rot) + p.RotateAny
		if deg < 0 {
			s = append(s, "rt-N"+num(-deg))
		} else {
			s = append(s, "rt-"+num(deg))
		}
	}
	switch {
	case p.Flip.H && p.Flip.V:
		s = append(s, "fl-h_v")
	case p.Flip.H:
		s = append(s, "fl-h")
	case p.Flip.V:
		s = append(s, "fl-v")
	}
	if t := p.Trim; t != nil && t.Color == nil && t.Threshold >= 1 && t.Threshold <= 99 {
		s = append(s, "t-"+num(math.Round(t.Threshold)))
	}
	if op := p.Adjust.Opacity; op != nil {
		s = append(s, "o-"+num(math.Round(*op*100)))
	}
	if p.Page != nil {
		s = append(s, "pg-"+itoa(*p.Page+1))
	}
	if v := p.Video; v != nil && v.Second != nil {
		s = append(s, "so-"+num(*v.Second))
	}
	return s
}

// emitImagekit writes /tr:<steps>/<key>.
func emitImagekit(e *emitter) (string, string, error) {
	key, ok := e.src.keyOn(e.mountSource)
	if !ok {
		return "", "", e.errNeedsKey("ImageKit")
	}
	steps := make([]string, 0, len(e.r.Pipelines))
	for i := range e.r.Pipelines {
		st := imagekitStep(&e.r.Pipelines[i])
		if i == 0 {
			o := &e.r.Output
			if f, ok := imagekitFormats[o.Format]; ok {
				st = append(st, "f-"+f)
			}
			if o.Quality > 0 {
				st = append(st, "q-"+itoa(o.Quality))
			}
			if o.Progressive != nil {
				st = append(st, "pr-"+boolWord(*o.Progressive))
			}
			if o.Lossless != nil {
				st = append(st, "lo-"+boolWord(*o.Lossless))
			}
			if o.StripMetadata != nil && !*o.StripMetadata {
				st = append(st, "md-true")
			}
			if o.StripColorProfile != nil && !*o.StripColorProfile {
				st = append(st, "cp-true")
			}
		}
		if len(st) > 0 {
			steps = append(steps, strings.Join(st, ","))
		}
	}
	if len(steps) == 0 {
		return "/" + key, "", nil
	}
	return "/tr:" + strings.Join(steps, ":") + "/" + key, "", nil
}

func boolWord(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
