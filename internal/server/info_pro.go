package server

import (
	"encoding/hex"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/imageinfo"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// infoProJSON holds the /info fields of imgproxy's Pro options (WP-63),
// with imgproxy's names and shapes (docs.imgproxy.net
// usage/getting_info): relative crop coordinates, an RGBA palette and
// average, the six dominant colour classes (RGB), the BlurHash, the
// ThumbHash as upper-case hex, the perceptual hash as 16 lower-case hex
// digits, and hex hashsums of the source bytes.
type infoProJSON struct {
	Crop           *infoCrop         `json:"crop,omitempty"`
	Palette        []infoRGBA        `json:"palette,omitempty"`
	Average        *infoRGBA         `json:"average,omitempty"`
	DominantColors *infoDominant     `json:"dominant_colors,omitempty"`
	Blurhash       string            `json:"blurhash,omitempty"`
	ThumbHash      string            `json:"thumb_hash,omitempty"`
	PerceptualHash string            `json:"perceptual_hash,omitempty"`
	Hashsums       map[string]string `json:"hashsums,omitempty"`
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

// infoPro renders the WP-63 fields for o. si.Pixels holds what the
// inspector computed for pixelOptions(o); src is the source as fetched.
func infoPro(o *transform.InfoOptions, si *SourceInfo, src []byte) infoProJSON {
	var out infoProJSON
	if c := o.Crop; c != nil && (!c.Width.IsZero() || !c.Height.IsZero()) {
		out.Crop = cropCoordinates(c, &si.Meta)
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
		return out
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
	return out
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
// width and height.
func cropCoordinates(c *transform.Crop, m *SourceMeta) *infoCrop {
	w, h := orientedSize(m)
	if w <= 0 || h <= 0 {
		return &infoCrop{Width: 1, Height: 1}
	}
	x, y, cw, ch, _ := plan.InfoCrop(c, w, h)
	fw, fh := float64(w), float64(h)
	return &infoCrop{Left: float64(x) / fw, Top: float64(y) / fh, Width: float64(cw) / fw, Height: float64(ch) / fh}
}
