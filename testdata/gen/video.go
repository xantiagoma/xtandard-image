package main

import (
	"bufio"
	"math"
	"os"
)

// videoFrames writes the raw RGB24 frames (rows top to bottom, frames one
// after another) of the procedural video clip that testdata/gen/video.sh
// encodes into the video fixtures:
//
//   - the landscape of scene(), panned from left to right across a wider
//     panorama during the clip;
//   - a red disc moving along the lake and a colour bar at the top whose hue
//     encodes the time, so frames a fraction of a second apart differ;
//   - the first 0.4 s fade in from black (frame 0 is black) and the last
//     0.3 s fade out to white, the frames a best-frame selection must avoid.
//
// Every frame is a pure function of (w, h, frame, fps, frames).
func videoFrames(out string, w, h, fps, frames int) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(f, 1<<20)
	// The panorama is rendered once, 1.5 × the frame width.
	pw := w * 3 / 2
	pano := make([]float64, pw*h*3)
	for y := range h {
		for x := range pw {
			r, g, b := scene(float64(x)/float64(pw)*1.5, float64(y)/float64(h))
			i := (y*pw + x) * 3
			pano[i], pano[i+1], pano[i+2] = r, g, b
		}
	}
	dur := float64(frames) / float64(fps)
	row := make([]byte, w*3)
	for n := range frames {
		t := float64(n) / float64(fps)
		off := int(math.Round(float64(pw-w) * t / dur))
		fade, white := 1.0, 0.0
		if t < 0.4 {
			fade = t / 0.4
		}
		if t > dur-0.3 {
			white = clamp01((t - (dur - 0.3)) / 0.25)
		}
		discX, discY, discR := (0.1+0.8*t/dur)*float64(w), 0.82*float64(h), 0.06*float64(h)
		hr, hg, hb := hue(t / dur)
		for y := range h {
			for x := range w {
				i := (y*pw + x + off) * 3
				r, g, b := pano[i], pano[i+1], pano[i+2]
				if math.Hypot(float64(x)-discX, float64(y)-discY) < discR {
					r, g, b = 0.9, 0.12, 0.1
				}
				if y < h/12 && x < w/3 {
					r, g, b = hr, hg, hb
				}
				r, g, b = r*fade, g*fade, b*fade
				r, g, b = lerp(r, 1, white), lerp(g, 1, white), lerp(b, 1, white)
				row[x*3], row[x*3+1], row[x*3+2] = q8(r*255), q8(g*255), q8(b*255)
			}
			if _, err := bw.Write(row); err != nil {
				_ = f.Close()
				return err
			}
		}
	}
	if err := bw.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// hue returns a saturated colour for h in [0, 1] (HSV with S = V = 1).
func hue(h float64) (r, g, b float64) {
	h = math.Mod(h, 1) * 6
	x := 1 - math.Abs(math.Mod(h, 2)-1)
	switch int(h) {
	case 0:
		return 1, x, 0
	case 1:
		return x, 1, 0
	case 2:
		return 0, 1, x
	case 3:
		return 0, x, 1
	case 4:
		return x, 0, 1
	default:
		return 1, 0, x
	}
}
