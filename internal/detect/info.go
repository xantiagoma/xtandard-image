package detect

import "math"

// Info is the JSON shape of detections in /info responses (`detect_objects`,
// WP-73). It follows imgproxy Pro's documented `objects` member (class_id,
// class_name, confidence and a box relative to the image size, so it does
// not depend on the requested output size), plus face landmarks.
type Info struct {
	Objects []InfoObject `json:"objects"`
}

// InfoObject is one detection with coordinates relative to the image
// (0–1): left/top of the box and its width/height.
type InfoObject struct {
	ClassID    int     `json:"class_id"`
	ClassName  string  `json:"class_name"`
	Confidence float64 `json:"confidence"`
	Left       float64 `json:"left"`
	Top        float64 `json:"top"`
	Width      float64 `json:"width"`
	Height     float64 `json:"height"`
	// Landmarks are a face's [x, y] points, relative like the box, in the
	// Detection.Landmarks order. An extension over imgproxy's shape.
	Landmarks [][2]float64 `json:"landmarks,omitempty"`
}

// NewInfo converts detections in a width×height image to the /info shape.
// Values are rounded to 6 decimals so the JSON is stable and compact.
func NewInfo(dets []Detection, width, height int) Info {
	info := Info{Objects: make([]InfoObject, 0, len(dets))}
	if width <= 0 || height <= 0 {
		return info
	}
	w, h := float64(width), float64(height)
	for _, d := range dets {
		o := InfoObject{
			ClassID:    ClassID(d.Class),
			ClassName:  d.Class,
			Confidence: round6(float64(d.Score)),
			Left:       round6(float64(d.Box.Min.X) / w),
			Top:        round6(float64(d.Box.Min.Y) / h),
			Width:      round6(float64(d.Box.Dx()) / w),
			Height:     round6(float64(d.Box.Dy()) / h),
		}
		for _, p := range d.Landmarks {
			o.Landmarks = append(o.Landmarks, [2]float64{round6(float64(p.X) / w), round6(float64(p.Y) / h)})
		}
		info.Objects = append(info.Objects, o)
	}
	return info
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }
