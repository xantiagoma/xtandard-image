package server

import (
	"encoding/hex"
	"math"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/imageinfo"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// InfoProResponse holds the /info fields of imgproxy's Pro options (WP-63),
// with imgproxy's names and shapes (docs.imgproxy.net
// usage/getting_info): relative crop coordinates, an RGBA palette and
// average, the six dominant colour classes (RGB), the BlurHash, the
// ThumbHash as upper-case hex, the perceptual hash as 16 lower-case hex
// digits, hex hashsums of the source bytes, and the detected objects
// (WP-73). It is embedded in InfoResponse and, like it, public API (ADR
// 0008).
type InfoProResponse struct {
	Crop           *infoCrop         `json:"crop,omitempty"            doc:"Crop rectangle as fractions of the oriented image (info option crop)."`
	Palette        []infoRGBA        `json:"palette,omitempty"         doc:"Up to the requested number of RGBA colours (info option palette)."`
	Average        *infoRGBA         `json:"average,omitempty"         doc:"Average RGBA colour (info option average)."`
	DominantColors *infoDominant     `json:"dominant_colors,omitempty" doc:"Dominant RGB colours by class; missing classes are left out unless built (info option dominant_colors)."`
	Blurhash       string            `json:"blurhash,omitempty"        doc:"BlurHash string (info option blurhash)."`
	ThumbHash      string            `json:"thumb_hash,omitempty"      doc:"ThumbHash as upper-case hex (info option thumbhash)."`
	PerceptualHash string            `json:"perceptual_hash,omitempty" doc:"Perceptual hash as 16 lower-case hex digits (info option phash)."`
	Hashsums       map[string]string `json:"hashsums,omitempty"        doc:"Hex digests of the source bytes by algorithm: md5, sha1, sha256, sha512 (info option calc_hashsums)."`
	Objects        []InfoObject      `json:"objects,omitzero"          doc:"Detected faces and objects, best first within each model (faces first), with boxes as fractions of the oriented image; [] when none were found (info option detect_objects)."`
	Classification []InfoClass       `json:"classification,omitzero"   doc:"The top_k classes of the image classifier (ImageNet-1k by default), best first; absent when no classifier is available (the option is then listed in X-Xi-Ignored) (info option classify)."`
}

// InfoClass is one class of the image classification, imgproxy Pro's
// classify shape.
type InfoClass struct {
	ClassID    int     `json:"class_id"   doc:"Class index in the classifier's output (the ImageNet-1k class id by default)."`
	Name       string  `json:"name"       doc:"Class name (ImageNet-1k: tabby cat, golden retriever, pizza, ...)."`
	Confidence float64 `json:"confidence" doc:"Class probability, 0-1."                                        maximum:"1" minimum:"0"`
}

// InfoObject is one detected face or object, imgproxy Pro's
// detect_objects shape (internal/detect.NewInfo) plus face landmarks.
type InfoObject struct {
	ClassID    int     `json:"class_id"    doc:"0 for faces (the face model's only class), the COCO class index for objects."`
	ClassName  string  `json:"class_name"  doc:"face, or a COCO class name (person, car, dog, traffic light, ...)."`
	Confidence float64 `json:"confidence"  doc:"Detection score, 0-1."                                                     maximum:"1" minimum:"0"`
	Left       float64 `json:"left"        doc:"Left edge of the box, as a fraction of the oriented image width."`
	Top        float64 `json:"top"         doc:"Top edge of the box, as a fraction of the oriented image height."`
	Width      float64 `json:"width"       doc:"Box width, as a fraction of the image width."`
	Height     float64 `json:"height"      doc:"Box height, as a fraction of the image height."`
	// Landmarks is an extension over imgproxy's shape.
	Landmarks [][2]float64 `json:"landmarks,omitempty" doc:"Face landmarks [x, y] as fractions of the image: right eye, left eye, nose tip, right and left mouth corners (faces only; an extension over imgproxy)."`
}

// infoCrop is the crop rectangle as fractions of the (oriented) image.
type infoCrop struct {
	Left   float64 `json:"left"`
	Top    float64 `json:"top"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type infoRGBA struct {
	R uint8 `json:"R"`
	G uint8 `json:"G"`
	B uint8 `json:"B"`
	A uint8 `json:"A"`
}

type infoRGB struct {
	R uint8 `json:"R"`
	G uint8 `json:"G"`
	B uint8 `json:"B"`
}

// infoDominant is imgproxy's dominant_colors object; classes the image
// lacks (and build_missed did not build) are left out.
type infoDominant struct {
	DarkMuted    *infoRGB `json:"dark_muted,omitempty"`
	DarkVibrant  *infoRGB `json:"dark_vibrant,omitempty"`
	LightMuted   *infoRGB `json:"light_muted,omitempty"`
	LightVibrant *infoRGB `json:"light_vibrant,omitempty"`
	Muted        *infoRGB `json:"muted,omitempty"`
	Vibrant      *infoRGB `json:"vibrant,omitempty"`
}

// pixelOptions is what the inspector must compute for o: nil when no
// pixel-derived field is requested (and always for the thumbor schema).
func pixelOptions(o *transform.InfoOptions) *imageinfo.Options {
	if o == nil || o.Schema == transform.InfoSchemaThumbor {
		return nil
	}
	px := &imageinfo.Options{Palette: o.Palette, Thumbhash: o.Thumbhash, PerceptualHash: o.PHash}
	if o.Average != nil {
		px.Average, px.AverageIgnoreTransparent = true, o.Average.IgnoreTransparent
	}
	if o.DominantColors != nil {
		px.Dominant, px.DominantBuildMissed = true, o.DominantColors.BuildMissed
	}
	if o.Blurhash != nil {
		px.BlurhashX, px.BlurhashY = o.Blurhash.XComponents, o.Blurhash.YComponents
	}
	if !px.Any() {
		return nil
	}
	return px
}

// infoPro renders the WP-63 and WP-73 fields for o. si.Pixels holds what
// the inspector computed for pixelOptions(o) and si.Detections what it
// detected for infoDetection; op places a crop on detections; src is the
// source as fetched.
func infoPro(o *transform.InfoOptions, op *transform.ObjectsPosition, si *SourceInfo, src []byte) (InfoProResponse, error) {
	var out InfoProResponse
	if c := o.Crop; c != nil && (!c.Width.IsZero() || !c.Height.IsZero()) {
		out.Crop = cropCoordinates(c, op, si)
	}
	if o.DetectObjects {
		out.Objects = infoObjects(si.Detections.Select(o.DetectClasses))
	}
	if cls := classificationOf(si); o.ClassifyTopK > 0 && cls != nil {
		top, unknown := cls.Top(o.ClassifyTopK, o.ClassifyClasses)
		if len(unknown) > 0 {
			return out, errs.WithPublic(errs.Newf(errs.KindInvalidURL, "server: unknown classification class %q", unknown[0]),
				"unknown classification class "+strconv.Quote(unknown[0]))
		}
		out.Classification = make([]InfoClass, 0, len(top))
		for _, c := range top {
			out.Classification = append(out.Classification, InfoClass{ClassID: c.ID, Name: c.Name, Confidence: round6(c.Score)})
		}
	}
	if len(o.CalcHashsums) > 0 {
		out.Hashsums = make(map[string]string, len(o.CalcHashsums))
		for _, h := range o.CalcHashsums {
			if sum, ok := imageinfo.Hashsum(h.String(), src); ok {
				out.Hashsums[h.String()] = sum
			}
		}
	}
	px := si.Pixels
	if px == nil {
		return out, nil
	}
	for _, c := range px.Palette {
		out.Palette = append(out.Palette, infoRGBA(c))
	}
	if px.Average != nil {
		a := infoRGBA(*px.Average)
		out.Average = &a
	}
	if d := px.Dominant; d != nil {
		out.DominantColors = &infoDominant{
			DarkMuted: rgb(d.DarkMuted), DarkVibrant: rgb(d.DarkVibrant),
			LightMuted: rgb(d.LightMuted), LightVibrant: rgb(d.LightVibrant),
			Muted: rgb(d.Muted), Vibrant: rgb(d.Vibrant),
		}
	}
	out.Blurhash = px.Blurhash
	if len(px.Thumbhash) > 0 {
		out.ThumbHash = strings.ToUpper(hex.EncodeToString(px.Thumbhash))
	}
	if px.PerceptualHash != nil {
		out.PerceptualHash = imageinfo.FormatPerceptualHash(*px.PerceptualHash)
	}
	return out, nil
}

func rgb(c *imageinfo.RGB) *infoRGB {
	if c == nil {
		return nil
	}
	v := infoRGB(*c)
	return &v
}

// cropCoordinates is imgproxy's relative crop: where crop c lands on the
// image as displayed (EXIF orientation applied), as fractions of its
// width and height; face and object gravities are placed on the
// detections with objects position op.
func cropCoordinates(c *transform.Crop, op *transform.ObjectsPosition, si *SourceInfo) *infoCrop {
	w, h := orientedSize(&si.Meta)
	if w <= 0 || h <= 0 {
		return &infoCrop{Width: 1, Height: 1}
	}
	x, y, cw, ch, _ := plan.InfoCrop(c, op, w, h, si.Detections)
	fw, fh := float64(w), float64(h)
	return &infoCrop{Left: float64(x) / fw, Top: float64(y) / fh, Width: float64(cw) / fw, Height: float64(ch) / fh}
}

// infoDetection is what an info request needs from detection. The thumbor
// schema plans the request's pipeline: its smart gravity crops on faces
// when they can be found (optional, like processing).
func infoDetection(req *transform.Request) plan.DetectionNeeds {
	if req.Info != nil && req.Info.Schema == transform.InfoSchemaThumbor {
		n := plan.NeedsDetection(&transform.Request{Pipelines: req.Pipelines})
		return plan.DetectionNeeds{SmartFaces: n.Any()}
	}
	return plan.NeedsDetection(req)
}

// objectsPositionOf is the objects position of req's first pipeline (the
// imgproxy info option objects_position sets it there).
func objectsPositionOf(req *transform.Request) *transform.ObjectsPosition {
	if len(req.Pipelines) == 0 {
		return nil
	}
	return req.Pipelines[0].Gravity.ObjectsPosition
}

// infoObjects converts detections to the /info shape (detect.NewInfo's),
// rounded to 6 decimals; never nil, so an empty result is [].
func infoObjects(dets []plan.Detected) []InfoObject {
	out := make([]InfoObject, 0, len(dets))
	for _, d := range dets {
		id := transform.ObjectClassID(d.Class)
		if d.Class == transform.FaceClass {
			id = 0
		}
		o := InfoObject{
			ClassID: id, ClassName: d.Class, Confidence: round6(d.Score),
			Left: round6(d.Box.Left), Top: round6(d.Box.Top), Width: round6(d.Box.Width), Height: round6(d.Box.Height),
		}
		for _, p := range d.Landmarks {
			o.Landmarks = append(o.Landmarks, [2]float64{round6(p[0]), round6(p[1])})
		}
		out = append(out, o)
	}
	return out
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }
