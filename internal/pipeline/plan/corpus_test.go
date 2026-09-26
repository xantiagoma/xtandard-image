package plan_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// corpusCase is one row of testdata/imgproxy_corpus.json, recorded by
// running imgproxy's own pipeline with every libvips geometry call traced
// (testdata/harness). The planner, driven like the executor drives it,
// must produce the same calls with the same arguments and sizes.
type corpusCase struct {
	Name string `json:"name"`
	Src  struct {
		Format      string `json:"format"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		Orientation int    `json:"orientation"`
		Alpha       bool   `json:"alpha"`
		Frames      int    `json:"frames"`
	} `json:"src"`
	URL       string   `json:"url"`
	Trace     []string `json:"trace"`
	Width     int      `json:"width"`
	Height    int      `json:"height"`
	Error     string   `json:"error"`
	Deviation *struct {
		From, To, Why string
	} `json:"deviation"`
}

// imgproxyLimits mirrors the harness: imgproxy's defaults with 256 frames.
var imgproxyLimits = plan.Limits{MaxSrcPixels: 50_000_000, MaxAnimationFrames: 256, AllowRaisingLimits: true}

func loadCorpus(tb testing.TB) []corpusCase {
	tb.Helper()
	b, err := os.ReadFile("testdata/imgproxy_corpus.json")
	if err != nil {
		tb.Fatal(err)
	}
	var cs []corpusCase
	if err := json.Unmarshal(b, &cs); err != nil {
		tb.Fatal(err)
	}
	return cs
}

func parseImgproxy(tb testing.TB, opts string) *transform.Request {
	tb.Helper()
	p, err := imgproxy.New(&imgproxy.Options{AllowSecurityOptions: true})
	if err != nil {
		tb.Fatal(err)
	}
	res, err := p.ParsePath("/unsafe/" + opts + "/plain/local:///x")
	if err != nil {
		tb.Fatalf("parse %q: %v", opts, err)
	}
	r := res.Request
	r.ApplyDefaults(transform.StandardDefaults())
	if err := r.Validate(); err != nil {
		tb.Fatalf("validate %q: %v", opts, err)
	}
	return r
}

func (c *corpusCase) meta() plan.SourceMeta {
	s := c.Src
	return plan.SourceMeta{
		Width: s.Width, Height: s.Height, Frames: max(s.Frames, 1), HasAlpha: s.Alpha,
		Orientation: s.Orientation, IsAnimated: s.Frames > 1, Format: s.Format, IsVector: s.Format == "svg",
	}
}

func TestImgproxyCorpus(t *testing.T) {
	t.Parallel()
	cs := loadCorpus(t)
	if len(cs) < 60 {
		t.Fatalf("corpus has %d cases, want ≥ 60", len(cs))
	}
	for _, c := range cs {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			req := parseImgproxy(t, c.URL)
			got, w, h, err := drive(req, new(c.meta()), c.Trace)
			if c.Error != "" {
				if errs.KindOf(err) != errs.KindLimitExceeded {
					t.Fatalf("imgproxy failed with %q; planner error = %v, want KindLimitExceeded", c.Error, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := expectedTrace(&c)
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("libvips calls (-imgproxy +plan):\n%s", diff)
			}
			if w != c.Width || h != c.Height {
				t.Errorf("result %dx%d, imgproxy %dx%d", w, h, c.Width, c.Height)
			}
		})
	}
}

// expectedTrace drops imgproxy's header-only first load and all but the
// last load (the vector guard reloads twice), then applies a documented
// deviation.
func expectedTrace(c *corpusCase) []string {
	var out []string
	lastLoad := -1
	for i, s := range c.Trace {
		if strings.HasPrefix(s, "load ") {
			lastLoad = i
		}
	}
	for i, s := range c.Trace {
		if strings.HasPrefix(s, "load ") && i != lastLoad {
			continue
		}
		if d := c.Deviation; d != nil && s == d.From {
			s = d.To
		}
		out = append(out, s)
	}
	return out
}

// drive plays the executor: it builds the plan, checks the predicted
// loaded size, feeds back the trimmed size imgproxy observed (it depends on
// pixels) through Continue, and renders the plan as the libvips calls
// imgproxy makes.
func drive(req *transform.Request, meta *plan.SourceMeta, trace []string) ([]string, int, int, error) {
	p, err := plan.Build(req, *meta, imgproxyLimits)
	if err != nil {
		return nil, 0, 0, err
	}
	if lw, lh, ok := lastLoad(trace); ok && p.Animation == nil && (lw != p.Load.Width || lh != p.Load.Height) {
		// Continue exists for loaders that round differently, but every
		// corpus prediction must be exact.
		return nil, 0, 0, fmt.Errorf("predicted load %dx%d, imgproxy loaded %dx%d", p.Load.Width, p.Load.Height, lw, lh)
	}
	if tw, th, ok := traceSize(trace, "trim"); ok {
		if p, err = plan.Continue(req, *meta, imgproxyLimits, plan.Observed{Width: tw, Height: th, Trimmed: true}); err != nil {
			return nil, 0, 0, err
		}
	}
	return render(p, trace)
}

func lastLoad(trace []string) (w, h int, ok bool) {
	for _, s := range trace {
		if strings.HasPrefix(s, "load ") {
			w, h, ok = sizeOf(s)
		}
	}
	return w, h, ok
}

func traceSize(trace []string, op string) (w, h int, ok bool) {
	for _, s := range trace {
		if strings.HasPrefix(s, op+" ") {
			return sizeOf(s)
		}
	}
	return 0, 0, false
}

func sizeOf(s string) (w, h int, ok bool) {
	_, dims, found := strings.Cut(s, " -> ")
	if !found {
		return 0, 0, false
	}
	ws, hs, _ := strings.Cut(dims, "x")
	w, _ = strconv.Atoi(ws)
	h, _ = strconv.Atoi(hs)
	return w, h, true
}

// render turns a plan into imgproxy's traced calls with the sizes each
// produces. Trim sizes come from the trace (they depend on pixels).
func render(p *plan.Plan, trace []string) ([]string, int, int, error) {
	frames := 1
	if p.Animation != nil {
		frames = p.Animation.Frames
	}
	ld := p.Load
	shrink := 1.0
	switch {
	case ld.ShrinkOnLoad > 1:
		shrink = float64(ld.ShrinkOnLoad)
	case ld.Scale > 0:
		shrink = 1 / ld.Scale
	}
	out := []string{fmt.Sprintf("load shrink=%.10g page=%d n=%d -> %dx%d", shrink, ld.Page, ld.NPages, ld.Width, ld.Height*frames)}
	var w, h int
	for f := range frames {
		w, h = ld.Width, ld.Height
		for _, s := range p.Steps {
			var line string
			switch s := s.(type) {
			case plan.ColourStep:
				continue
			case plan.TrimStep:
				if f > 0 {
					continue
				}
				w, h, _ = traceSize(trace, "trim")
				line = "trim"
			case plan.PreCropStep:
				line, w, h = cropLine(&s.CropGeometry, w, h)
			case plan.CropStep:
				line, w, h = cropLine(&s.CropGeometry, w, h)
			case plan.ResizeStep:
				w, h = s.W, s.H
				line = fmt.Sprintf("resize %.10g %.10g", s.ScaleX, s.ScaleY)
			case plan.RotateStep:
				if s.Angle%180 != 0 {
					w, h = h, w
				}
				line = fmt.Sprintf("rotate %d", s.Angle)
			case plan.FlipStep:
				line = fmt.Sprintf("flip %t %t", s.Horizontal, s.Vertical)
			case plan.FilterStep:
				line = fmt.Sprintf("filters %g %g %d", s.Blur, s.Sharpen, max(s.Pixelate, 1))
			case plan.ExtendStep:
				x, y, ew, eh, _ := s.Rect(w, h)
				w, h = ew, eh
				line = fmt.Sprintf("embed %d %d %d %d", ew, eh, x, y)
			case plan.PaddingStep:
				w, h = w+s.Left+s.Right, h+s.Top+s.Bottom
				line = fmt.Sprintf("embed %d %d %d %d", w, h, s.Left, s.Top)
			case plan.FlattenStep:
				line = "flatten"
			default:
				return nil, 0, 0, fmt.Errorf("unexpected step %T", s)
			}
			out = append(out, fmt.Sprintf("%s -> %dx%d", line, w, h))
		}
	}
	if w != p.Output.Width || h != p.Output.Height {
		return nil, 0, 0, fmt.Errorf("steps end at %dx%d, Output says %dx%d", w, h, p.Output.Width, p.Output.Height)
	}
	return out, w, h, nil
}

func cropLine(c *plan.CropGeometry, w, h int) (string, int, int) {
	x, y, cw, ch, _ := c.Rect(w, h)
	if c.Smart != plan.InterestingNone {
		return fmt.Sprintf("smartcrop %d %d", cw, ch), cw, ch
	}
	return fmt.Sprintf("crop %d %d %d %d", x, y, cw, ch), cw, ch
}
