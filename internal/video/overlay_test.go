package video

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// planOverlayOp plans op (with Width 640 on the 2 s hd source) and returns
// the video stream's overlays.
func planOverlayOp(t *testing.T, op *transform.VideoOp, in *TranscodeInput) []OverlayPlan {
	t.Helper()
	r := transform.Request{Kind: transform.KindVideo, Source: transform.SourceSpec{Raw: "a.mp4"}, Video: op}
	r.ApplyDefaults(transform.StandardDefaults())
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	out, err := fullCaps().Resolve(r.Video)
	if err != nil {
		t.Fatal(err)
	}
	p, err := PlanTranscode(in, r.Video, out, fullCaps(), settings())
	if err != nil {
		t.Fatal(err)
	}
	return p.Video.Overlays
}

func mark(o transform.VideoOverlay) transform.VideoOverlay {
	if o.Opacity == 0 {
		o.Opacity = 1
	}
	if o.Timecode == transform.VideoTimecodeNone && o.Text == "" {
		o.Source = &transform.SourceSpec{Raw: "logo.png"}
	}
	return o
}

func TestPlanOverlays(t *testing.T) {
	t.Parallel()
	sec := func(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }
	for _, tc := range []struct {
		name string
		op   transform.VideoOp
		in   func(*TranscodeInput)
		want []OverlayPlan
	}{
		{
			name: "always",
			op:   transform.VideoOp{Overlays: []transform.VideoOverlay{mark(transform.VideoOverlay{})}},
			want: []OverlayPlan{{}},
		},
		{
			name: "window and fades",
			op: transform.VideoOp{Overlays: []transform.VideoOverlay{mark(transform.VideoOverlay{
				Start: sec(0.5), End: sec(1.5), FadeIn: sec(0.25), FadeOut: sec(0.5),
			})}},
			want: []OverlayPlan{{
				Start: 0.5, End: 1.5, FadeIn: 0.25, FadeOut: 0.5, FadeEnd: 1.5, Enable: "between(t,0.5,1.5)",
			}},
		},
		{
			name: "end past the output runs to the end, fading out at it",
			op: transform.VideoOp{Overlays: []transform.VideoOverlay{mark(transform.VideoOverlay{
				Start: sec(1), End: sec(10), FadeOut: sec(0.5),
			})}},
			want: []OverlayPlan{{Start: 1, FadeOut: 0.5, FadeEnd: 2, Enable: "gte(t,1)"}},
		},
		{
			name: "output timeline after the trim",
			op: transform.VideoOp{Start: sec(1), Overlays: []transform.VideoOverlay{
				mark(transform.VideoOverlay{End: sec(0.5)}),
				mark(transform.VideoOverlay{Start: sec(1.5)}), // the output is 1 s long
			}},
			want: []OverlayPlan{{End: 0.5, Enable: "lte(t,0.5)"}},
		},
		{
			name: "unknown length: no fade out without an end",
			op: transform.VideoOp{Overlays: []transform.VideoOverlay{mark(transform.VideoOverlay{
				FadeIn: sec(0.2), FadeOut: sec(0.5),
			})}},
			in:   func(in *TranscodeInput) { in.Duration = 0 },
			want: []OverlayPlan{{FadeIn: 0.2}},
		},
		{
			name: "opacity 0 is dropped, indexes kept",
			op: transform.VideoOp{Overlays: []transform.VideoOverlay{
				{Watermark: transform.Watermark{Text: "x"}},
				mark(transform.VideoOverlay{Watermark: transform.Watermark{Text: "y"}}),
			}},
			want: []OverlayPlan{{Index: 1}},
		},
		{
			name: "timecode",
			op: transform.VideoOp{Width: 640, Overlays: []transform.VideoOverlay{
				mark(transform.VideoOverlay{
					Watermark: transform.Watermark{
						Opacity: 0.8, Color: &transform.Color{R: 255, G: 128, A: 255}, Shadow: 1.6,
						Position: transform.Gravity{Type: transform.GravitySouthEast, XOffset: 10, YOffset: 0.1},
					},
					Timecode: transform.VideoTimecodeHMS, TimecodeSize: 32, Start: sec(0.5), FadeIn: sec(0.5), FadeOut: sec(0.25),
				}),
				mark(transform.VideoOverlay{
					Watermark: transform.Watermark{Position: transform.Gravity{Type: transform.GravityNorthWest, XOffset: -4}},
					Timecode:  transform.VideoTimecodeSeconds,
				}),
				mark(transform.VideoOverlay{Timecode: transform.VideoTimecodeHMS}),
			}},
			want: []OverlayPlan{
				{
					Start: 0.5, FadeIn: 0.5, FadeOut: 0.25, FadeEnd: 2, Enable: "gte(t,0.5)",
					Timecode: &TimecodePlan{
						Text: "%{pts:hms}", Size: 32, Color: "0xff8000", X: "w-tw-10", Y: "h-th-36",
						Alpha: "0.8*clip(min((t-0.5)/0.5,(2-t)/0.25),0,1)", Shadow: 2,
					},
				},
				{Index: 1, Timecode: &TimecodePlan{Text: "%{pts:flt}", Size: 24, Color: "0xffffff", X: "-4", Y: "0", Alpha: "1"}},
				{Index: 2, Timecode: &TimecodePlan{Text: "%{pts:hms}", Size: 24, Color: "0xffffff", X: "(w-tw)/2+0", Y: "(h-th)/2+0", Alpha: "1"}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := hd()
			if tc.in != nil {
				tc.in(in)
			}
			got := planOverlayOp(t, &tc.op, in)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("overlays (-want +got):\n%s", diff)
			}
		})
	}
}

// TestOverlayGraphIsNumeric: whatever the overlays say, the chain stays
// numeric, and every expression the shim sets is numbers, operators and a
// fixed function and variable vocabulary; the only text is a constant
// timecode expansion. Text and sources never reach the plan: they are
// rendered into pixels.
func TestOverlayGraphIsNumeric(t *testing.T) {
	t.Parallel()
	hostile := `[in]drawtext=text='%{pts\:hms}':fontfile=/etc/passwd,movie=x;%{e:system}`
	op := transform.VideoOp{Width: 333, Overlays: []transform.VideoOverlay{
		mark(transform.VideoOverlay{Watermark: transform.Watermark{Text: hostile, Font: "sans 20"},
			Start: 123456 * time.Microsecond, End: 1987654 * time.Microsecond, FadeIn: 100 * time.Millisecond}),
		mark(transform.VideoOverlay{Watermark: transform.Watermark{Source: &transform.SourceSpec{Raw: hostile}}, FadeOut: time.Second}),
		mark(transform.VideoOverlay{
			Watermark: transform.Watermark{Opacity: 0.333, Color: &transform.Color{R: 1, G: 2, B: 3, A: 200},
				Position: transform.Gravity{Type: transform.GravityEast, XOffset: -0.25, YOffset: 7}},
			Timecode: transform.VideoTimecodeSeconds, Start: time.Second, End: 1500 * time.Millisecond,
			FadeIn: 100 * time.Millisecond, FadeOut: 200 * time.Millisecond,
		}),
	}}
	ovs := planOverlayOp(t, &op, hd())
	if len(ovs) != 3 {
		t.Fatalf("%d overlays", len(ovs))
	}
	const expr = "abcdefghijklmnopqrstuvwxyz0123456789()+-*/.,"
	words := []string{"between", "gte", "lte", "clip", "min", "t", "w", "h", "tw", "th", "x"}
	for _, o := range ovs {
		fields := []string{o.Enable}
		if tc := o.Timecode; tc != nil {
			fields = append(fields, tc.X, tc.Y, tc.Alpha, tc.Color)
			if tc.Text != "%{pts:hms}" && tc.Text != "%{pts:flt}" {
				t.Errorf("timecode text %q is not a constant", tc.Text)
			}
		}
		for _, f := range fields {
			if i := strings.IndexFunc(f, func(r rune) bool { return !strings.ContainsRune(expr, r) }); i >= 0 {
				t.Errorf("expression %q has %q", f, f[i])
			}
			for _, w := range strings.FieldsFunc(f, func(r rune) bool { return !('a' <= r && r <= 'z') }) {
				if !slices.Contains(words, w) {
					t.Errorf("expression %q has the name %q", f, w)
				}
			}
		}
	}
}

func TestCheckOverlays(t *testing.T) {
	t.Parallel()
	v := &VideoStreamPlan{Width: 100, Height: 50, Overlays: []OverlayPlan{{}}}
	if err := v.CheckOverlays(); errs.KindOf(err) != errs.KindInternal {
		t.Errorf("unplaced overlay: %v", err)
	}
	v.Overlays[0].Place(90, 40, 10, 10)
	if err := v.CheckOverlays(); err != nil {
		t.Errorf("corner overlay: %v", err)
	}
	v.Overlays[0].Place(91, 40, 10, 10)
	if err := v.CheckOverlays(); errs.KindOf(err) != errs.KindInternal {
		t.Errorf("overflowing overlay: %v", err)
	}
	v.Overlays = []OverlayPlan{{Timecode: &TimecodePlan{}}}
	if err := v.CheckOverlays(); err != nil {
		t.Errorf("timecode: %v", err)
	}
	v.Overlays = make([]OverlayPlan, transform.MaxVideoOverlays+1)
	if err := v.CheckOverlays(); errs.KindOf(err) != errs.KindInternal {
		t.Errorf("too many: %v", err)
	}
}
