//go:build acceptance

package acceptance

import (
	"encoding/json"
	"testing"
)

// TestDetection: face and object detection (WP-72/73) against the image
// under test. The full image (ONNX Runtime and the YuNet and YOLOX-s
// models) detects: /info detect_objects lists the crew's three faces with
// landmarks and three persons, and face gravity, crop_objects and the
// detection filters answer with the requested sizes. The slim image has no
// detector: every detection option answers 422, and smart gravity is the
// attention crop. Which image this is comes from /info detect_objects
// (200 or 422), so the suite runs unchanged against both.
func TestDetection(t *testing.T) {
	t.Parallel()
	app := base(t, "XI_ACC_APP")
	const (
		crew = "/plain/local:///detect/apollo11-crew.jpg" // 640x503
		cat  = "/plain/local:///detect/cat-laptop.jpg"    // 640x419
	)
	info := get(t, app+"/info/unsafe/s:0/f:0/d:0/exif:0/iptc:0/xmp:0/vm:0/do:1"+crew, nil)
	full := info.Status == 200
	if !full {
		info.wantStatus(t, 422)
		t.Log("no detector (slim image): detection options must answer 422")
	} else {
		var body struct {
			Objects []struct {
				ClassName string       `json:"class_name"`
				Landmarks [][2]float64 `json:"landmarks"`
			} `json:"objects"`
		}
		if err := json.Unmarshal(info.Body, &body); err != nil {
			t.Fatalf("/info: %v: %s", err, truncate(info.Body))
		}
		faces, persons := 0, 0
		for _, o := range body.Objects {
			switch o.ClassName {
			case "face":
				faces++
				if len(o.Landmarks) != 5 {
					t.Errorf("face without its 5 landmarks: %+v", o)
				}
			case "person":
				persons++
			}
		}
		if faces != 3 || persons < 3 {
			t.Errorf("/info detect_objects: %d faces and %d persons, want 3 and ≥ 3: %s", faces, persons, info.Body)
		}
	}
	detected := func(p props) (int, string, props) {
		if full {
			return 200, "image/png", p
		}
		return 422, "", props{}
	}
	cases := make([]imageCase, 0, 8)
	for _, c := range []struct {
		name, path string
		props      props
	}{
		{"face gravity", "/unsafe/rs:fill:240:120/g:obj:face" + crew + "@png", props{Format: "png", Width: 240, Height: 120}},
		{"object gravity with position", "/unsafe/rs:fill:120:240/g:obj:person/op:we:4" + crew + "@png", props{Format: "png", Width: 120, Height: 240}},
		{"weighted objects", "/unsafe/rs:fill:120:240/g:objw:face:2:person:1" + crew + "@png", props{Format: "png", Width: 120, Height: 240}},
		{"blur detections", "/unsafe/rs:fit:320:320/bd:10:face" + crew + "@png", props{Format: "png", Width: 320, Height: 252}},
		{"draw detections", "/unsafe/rs:fit:320:320/dd:1" + crew + "@png", props{Format: "png", Width: 320, Height: 252}},
	} {
		status, ctype, p := detected(c.props)
		cases = append(cases, imageCase{name: c.name, path: c.path, status: status, ctype: ctype, props: p})
	}
	// crop_objects: the cat's box (about 414x328 in the reference) fitted
	// into 200x200; its height depends on the detected box.
	status, ctype, _ := detected(props{})
	cases = append(cases,
		imageCase{
			name: "crop objects", path: "/unsafe/c_obj:1:cat/rs:fit:200:200" + cat + "@png", status: status, ctype: ctype,
			check: func(t *testing.T, r *response) {
				t.Helper()
				if !full {
					return
				}
				p, err := decodeProps(r.Body)
				if err != nil || p.Format != "png" || p.Width != 200 || p.Height < 145 || p.Height > 170 {
					t.Errorf("crop objects: %+v, %v; want a png 200 wide and about 158 high", p, err)
				}
			},
		},
		// Smart gravity: faces on the full image, attention on slim.
		imageCase{name: "smart", path: "/unsafe/rs:fill:120:240/g:sm" + crew + "@png", status: 200, ctype: "image/png", props: props{Format: "png", Width: 120, Height: 240}},
		// An unknown class is a client error on every image.
		imageCase{name: "unknown class", path: "/unsafe/rs:fill:120:240/g:obj:unicorn" + crew, status: 400},
	)
	runImageCases(t, app, cases)
}
