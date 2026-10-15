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

func mark(p *transform.VideoOverlay) transform.VideoOverlay {
	o := *p
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
			op:   transform.VideoOp{Overlays: []transform.VideoOverlay{mark(&transform.VideoOverlay{})}},
			want: []OverlayPlan{{}},
		},
		{
			name: "window and fades",
			op: transform.VideoOp{Overlays: []transform.VideoOverlay{mark(&transform.VideoOverlay{
				Start: sec(0.5), End: sec(1.5), FadeIn: sec(0.25), FadeOut: sec(0.5),
			})}},
			want: []OverlayPlan{{
				Start: 0.5, End: 1.5, FadeIn: 0.25, FadeOut: 0.5, FadeEnd: 1.5, Enable: "between(t,0.5,1.5)",
			}},
		},
		{
			name: "end past the output runs to the end, fading out at it",
			op: transform.VideoOp{Overlays: []transform.VideoOverlay{mark(&transform.VideoOverlay{
				Start: sec(1), End: sec(10), FadeOut: sec(0.5),
			})}},
			want: []OverlayPlan{{Start: 1, FadeOut: 0.5, FadeEnd: 2, Enable: "gte(t,1)"}},
		},
		{
			name: "output timeline after the trim",
			op: transform.VideoOp{Start: sec(1), Overlays: []transform.VideoOverlay{
				mark(&transform.VideoOverlay{End: sec(0.5)}),
				mark(&transform.VideoOverlay{Start: sec(1.5)}), // the output is 1 s long
			}},
			want: []OverlayPlan{{End: 0.5, Enable: "lte(t,0.5)"}},
		},
		{
			name: "unknown length: no fade out without an end",
			op: transform.VideoOp{Overlays: []transform.VideoOverlay{mark(&transform.VideoOverlay{
				FadeIn: sec(0.2), FadeOut: sec(0.5),
			})}},
			in:   func(in *TranscodeInput) { in.Duration = 0 },
			want: []OverlayPlan{{FadeIn: 0.2}},
		},
		{
			name: "opacity 0 is dropped, indexes kept",
			op: transform.VideoOp{Overlays: []transform.VideoOverlay{
				{Text: "x"},
				mark(&transform.VideoOverlay{Text: "y"}),
			}},
			want: []OverlayPlan{{Index: 1}},
		},
		{
			name: "timecode",
			op: transform.VideoOp{Width: 640, Overlays: []transform.VideoOverlay{
				mark(&transform.VideoOverlay{
					Opacity: 0.8, Color: &transform.Color{R: 255, G: 128, A: 255}, Shadow: 1.6,
					Position: transform.Gravity{Type: transform.GravitySouthEast, XOffset: 10, YOffset: 0.1},
					Timecode: transform.VideoTimecodeHMS, TimecodeSize: 32, Start: sec(0.5), FadeIn: sec(0.5), FadeOut: sec(0.25),
				}),
				mark(&transform.VideoOverlay{
					Position: transform.Gravity{Type: transform.GravityNorthWest, XOffset: -4},
					Timecode: transform.VideoTimecodeSeconds,
				}),
				mark(&transform.VideoOverlay{Timecode: transform.VideoTimecodeHMS}),
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
		mark(&transform.VideoOverlay{
			Text: hostile, Font: "sans 20",
			Start: 123456 * time.Microsecond, End: 1987654 * time.Microsecond, FadeIn: 100 * time.Millisecond,
		}),
		mark(&transform.VideoOverlay{Source: &transform.SourceSpec{Raw: hostile}, FadeOut: time.Second}),
		mark(&transform.VideoOverlay{
			Opacity: 0.333, Color: &transform.Color{R: 1, G: 2, B: 3, A: 200},
			Position: transform.Gravity{Type: transform.GravityEast, XOffset: -0.25, YOffset: 7},
			Timecode: transform.VideoTimecodeSeconds, Start: time.Second, End: 1500 * time.Millisecond,
			FadeIn: 100 * time.Millisecond, FadeOut: 200 * time.Millisecond,
		}),
	}}
	ovs := planOverlayOp(t, &op, hd())
	if len(ovs) != 3 {
		t.Fatalf("%d overlays", len(ovs))
	}
	// The same overlays on every segment of every rendition of a stream
	// (WP-131): laid out on the largest rendition, scaled to each.
	sop := op
	sop.Width = 0
	sop.Stream = &transform.VideoStream{SegmentDuration: time.Second, Renditions: []int{720, 360, 144}}
	l := streamOf(t, hd(), &sop, gplCaps(), &StreamSettings{})
	for i := range l.Video {
		for n := range l.Segments {
			p, err := PlanSegment(hd(), &sop, l, &l.Video[i], n, settings())
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Video.Overlays) != 3 {
				t.Fatalf("%s/%d: %d overlays", l.Video[i].Name, n, len(p.Video.Overlays))
			}
			ovs = append(ovs, p.Video.Overlays...)
		}
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
			for _, w := range strings.FieldsFunc(f, func(r rune) bool { return r < 'a' || r > 'z' }) {
				if !slices.Contains(words, w) {
					t.Errorf("expression %q has the name %q", f, w)
				}
			}
		}
	}
}

// TestStreamOverlayPlans: a stream's overlays keep the output's timeline
// in every segment (windows and fades from the stream's start, not the
// segment's) and scale with the rendition: a timecode laid out on the
// largest one has its size, offsets and shadow scaled on the others.
func TestStreamOverlayPlans(t *testing.T) {
	t.Parallel()
	op := &transform.VideoOp{
		Stream: &transform.VideoStream{SegmentDuration: time.Second, Renditions: []int{720, 360}},
		Overlays: []transform.VideoOverlay{
			mark(&transform.VideoOverlay{Source: &transform.SourceSpec{Raw: "logo.png"}, Start: 500 * time.Millisecond, End: 1500 * time.Millisecond, FadeIn: 250 * time.Millisecond}),
			mark(&transform.VideoOverlay{
				Position: transform.Gravity{Type: transform.GravitySouthEast, XOffset: 20, YOffset: 0.05},
				Timecode: transform.VideoTimecodeHMS, TimecodeSize: 40,
			}),
		},
	}
	op.Overlays[1].Shadow = 4
	l := streamOf(t, hd(), op, gplCaps(), &StreamSettings{})
	for i, want := range []struct {
		size, shadow int
		x, y         string
	}{{40, 4, "w-tw-20", "h-th-36"}, {20, 2, "w-tw-10", "h-th-18"}} {
		r := &l.Video[i]
		for n := range l.Segments {
			p, err := PlanSegment(hd(), op, l, r, n, settings())
			if err != nil {
				t.Fatal(err)
			}
			v := p.Video
			if w, h := v.OverlayRef(); w != 1280 || h != 720 {
				t.Fatalf("%s: overlays laid out on %dx%d", r.Name, w, h)
			}
			img, tc := v.Overlays[0], v.Overlays[1].Timecode
			if img.Enable != "between(t,0.5,1.5)" || img.Start != 0.5 || img.FadeIn != 0.25 {
				t.Errorf("%s/%d: image overlay %+v", r.Name, n, img)
			}
			if tc == nil || tc.Size != want.size || tc.Shadow != want.shadow || tc.X != want.x || tc.Y != want.y {
				t.Errorf("%s/%d: timecode %+v, want %+v", r.Name, n, tc, want)
			}
		}
	}
	// A file's overlays are laid out on its own frame.
	file := &transform.VideoOp{Width: 640, Overlays: op.Overlays}
	if p, err := PlanTranscode(hd(), file, TranscodeOutput{Video: CodecVP9, Muxer: "webm"}, gplCaps(), settings()); err != nil ||
		p.Video.OverlayRefWidth != 0 || p.Video.Overlays[1].Timecode.Size != 40 {
		t.Fatalf("file plan %+v %v", p, err)
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
