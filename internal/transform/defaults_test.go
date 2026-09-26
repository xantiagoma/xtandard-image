package transform

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestApplyDefaultsEmptyRequest(t *testing.T) {
	t.Parallel()
	r := Request{Source: SourceSpec{Raw: "https://example.com/a.jpg"}}
	r.ApplyDefaults(StandardDefaults())

	want := Request{
		Source: SourceSpec{Raw: "https://example.com/a.jpg"},
		Pipelines: []Pipeline{{
			Resize: Resize{
				Type:      ResizeFit,
				ZoomX:     1,
				ZoomY:     1,
				DPR:       1,
				Algorithm: KernelLanczos3,
			},
			Gravity:    Gravity{Type: GravityCenter},
			Extend:     Extend{Gravity: Gravity{Type: GravityCenter}},
			ExtendAR:   Extend{Gravity: Gravity{Type: GravityCenter}},
			AutoRotate: new(true),
		}},
		Output: Output{
			StripMetadata:     new(true),
			KeepCopyright:     new(true),
			StripColorProfile: new(true),
			PreserveHDR:       new(false),
			EnforceThumbnail:  new(false),
		},
		Delivery: Delivery{Attachment: new(false)},
	}
	if diff := cmp.Diff(want, r); diff != "" {
		t.Fatalf("ApplyDefaults mismatch (-want +got):\n%s", diff)
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("defaulted empty request does not validate: %v", err)
	}
}

func TestStandardDefaultsMatchConfigDocs(t *testing.T) {
	t.Parallel()
	// docs/CONFIG.md: XI_PROCESSING_AUTO_ROTATE=true, STRIP_METADATA=true,
	// KEEP_COPYRIGHT=true, STRIP_COLOR_PROFILE=true, PRESERVE_HDR=false.
	want := Defaults{AutoRotate: true, StripMetadata: true, KeepCopyright: true, StripColorProfile: true}
	if diff := cmp.Diff(want, StandardDefaults()); diff != "" {
		t.Fatalf("StandardDefaults (-want +got):\n%s", diff)
	}
}

func TestApplyDefaultsHonoursConfig(t *testing.T) {
	t.Parallel()
	d := Defaults{PreserveHDR: true, EnforceThumbnail: true, ReturnAttachment: true}
	var r Request
	r.ApplyDefaults(d)
	got := []bool{
		*r.Pipelines[0].AutoRotate, *r.Output.StripMetadata, *r.Output.KeepCopyright,
		*r.Output.StripColorProfile, *r.Output.PreserveHDR, *r.Output.EnforceThumbnail,
		*r.Delivery.Attachment,
	}
	want := []bool{false, false, false, false, true, true, true}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("config-backed booleans (-want +got):\n%s", diff)
	}
}

func TestApplyDefaultsKeepsExplicitValues(t *testing.T) {
	t.Parallel()
	r := Request{
		Pipelines: []Pipeline{{
			Resize:     Resize{Type: ResizeFill, ZoomX: 2, ZoomY: 3, DPR: 2, Algorithm: KernelNearest},
			Gravity:    Gravity{Type: GravitySmart},
			AutoRotate: new(false),
		}},
		Output:   Output{StripMetadata: new(false)},
		Delivery: Delivery{Attachment: new(true)},
	}
	r.ApplyDefaults(StandardDefaults())
	p := r.Pipelines[0]
	if p.Resize.Type != ResizeFill || p.Resize.ZoomX != 2 || p.Resize.ZoomY != 3 || p.Resize.DPR != 2 ||
		p.Resize.Algorithm != KernelNearest || p.Gravity.Type != GravitySmart || *p.AutoRotate {
		t.Errorf("pipeline explicit values overwritten: %+v", p)
	}
	if *r.Output.StripMetadata || !*r.Delivery.Attachment {
		t.Error("explicit output/delivery booleans overwritten")
	}
}

func TestApplyDefaultsRotation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		rotate           int
		rotateAny        float64
		wantRotate       int
		wantRotateAnyNon float64
	}{
		{"zero", 0, 0, 0, 0},
		{"negative quarter", -90, 0, 270, 0},
		{"full turn", 360, 0, 0, 0},
		{"many turns", 90 + 360*5, 0, 90, 0},
		{"any multiple of 90 folds", 0, 90, 90, 0},
		{"any negative multiple folds", 90, -180, 270, 0},
		{"any arbitrary kept", 0, 45, 0, 45},
		{"any reduced above 180", 0, 270.5, 0, -89.5},
		{"any reduced below -180", 0, -190, 0, 170},
		{"any 540.5", 0, 540.5, 0, 180.5 - 360},
		{"both", 180, 30, 180, 30},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := Request{Pipelines: []Pipeline{{Rotate: tc.rotate, RotateAny: tc.rotateAny}}}
			r.ApplyDefaults(StandardDefaults())
			p := r.Pipelines[0]
			if p.Rotate != tc.wantRotate || p.RotateAny != tc.wantRotateAnyNon {
				t.Fatalf("Rotate, RotateAny = %d, %v; want %d, %v", p.Rotate, p.RotateAny, tc.wantRotate, tc.wantRotateAnyNon)
			}
		})
	}
}

func TestApplyDefaultsDropsIdentityAdjustments(t *testing.T) {
	t.Parallel()
	r := Request{Pipelines: []Pipeline{{Adjust: Adjust{
		Brightness: new(0.0), Hue: new(0.0), Contrast: new(1.0), Saturation: new(1.0),
		Gamma: new(1.0), Exposure: new(1.0), Vibrance: new(1.0), Red: new(1.0),
		Green: new(1.0), Blue: new(1.0), Opacity: new(1.0),
	}}}}
	r.ApplyDefaults(StandardDefaults())
	if diff := cmp.Diff(Adjust{}, r.Pipelines[0].Adjust); diff != "" {
		t.Fatalf("identity adjustments not dropped (-want +got):\n%s", diff)
	}

	r = Request{Pipelines: []Pipeline{{Adjust: Adjust{Brightness: new(10.0), Contrast: new(1.5)}}}}
	r.ApplyDefaults(StandardDefaults())
	if a := r.Pipelines[0].Adjust; a.Brightness == nil || a.Contrast == nil {
		t.Fatal("non-identity adjustments dropped")
	}
}

func TestApplyDefaultsGravityInheritance(t *testing.T) {
	t.Parallel()
	r := Request{Pipelines: []Pipeline{{
		Gravity:    Gravity{Type: GravityFocusPoint, FocusX: 0.2, FocusY: 0.8},
		Crop:       &Crop{Width: Dim{Px: 100}, Height: Dim{Px: 100}},
		Watermarks: []Watermark{{Opacity: 0.5}, {Opacity: 1, Position: Gravity{Type: GravitySouthEast}}},
	}}}
	r.ApplyDefaults(StandardDefaults())
	p := r.Pipelines[0]
	if diff := cmp.Diff(p.Gravity, p.Crop.Gravity); diff != "" {
		t.Errorf("crop gravity does not inherit pipeline gravity (-want +got):\n%s", diff)
	}
	if p.Watermarks[0].Position.Type != GravityCenter || p.Watermarks[1].Position.Type != GravitySouthEast {
		t.Errorf("watermark positions = %v, %v", p.Watermarks[0].Position.Type, p.Watermarks[1].Position.Type)
	}

	r = Request{Pipelines: []Pipeline{{Crop: &Crop{Gravity: Gravity{Type: GravityNorth}}}}}
	r.ApplyDefaults(StandardDefaults())
	if r.Pipelines[0].Crop.Gravity.Type != GravityNorth {
		t.Error("explicit crop gravity overwritten")
	}
}

func TestApplyDefaultsIsIdempotent(t *testing.T) {
	t.Parallel()
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			once := tc.build()
			once.ApplyDefaults(StandardDefaults())
			twice := tc.build()
			twice.ApplyDefaults(StandardDefaults())
			twice.ApplyDefaults(StandardDefaults())
			if diff := cmp.Diff(once, twice); diff != "" {
				t.Fatalf("second ApplyDefaults changed the request (-once +twice):\n%s", diff)
			}
		})
	}
}

func TestApplyDefaultsDoesNotMutateSharedData(t *testing.T) {
	t.Parallel()
	// Preset data is shared between requests; ApplyDefaults must copy before
	// writing.
	crop := &Crop{Width: Dim{Px: 10}}
	marks := []Watermark{{Opacity: 1}}
	pipes := []Pipeline{{Gravity: Gravity{Type: GravitySmart}, Crop: crop, Watermarks: marks, Rotate: -90}}

	r := Request{Pipelines: pipes}
	r.ApplyDefaults(StandardDefaults())

	if crop.Gravity.Type != GravityUnset {
		t.Error("shared *Crop was modified")
	}
	if marks[0].Position.Type != GravityUnset {
		t.Error("shared watermark slice was modified")
	}
	if pipes[0].Rotate != -90 || pipes[0].Resize.Type != ResizeUnset {
		t.Error("shared pipeline slice was modified")
	}
}
