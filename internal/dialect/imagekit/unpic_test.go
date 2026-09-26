package imagekit_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imagekit"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// unpicImg is the image of unpic's imagekit tests
// (.refs/unpic/src/providers/imagekit.test.ts, MIT).
const unpicImg = "https://ik.imagekit.io/ikmedia/docs_images/examples/example_food_3.jpg"

// unpicWant is what a URL means in unpic's operation vocabulary, mapped
// to the fields the request must carry.
type unpicWant struct {
	W, H      int
	Type      transform.ResizeType
	Enlarge   bool
	Gravity   transform.GravityType
	Quality   int
	Format    transform.Format
	Blur      float64
	Extend    bool
	BG        *transform.Color
	RadiusAll int
}

// TestUnpic parses every URL in unpic's imagekit tests: the inputs of its
// extract tests and the outputs of its transform and generate tests,
// host-bound on ik.imagekit.io with endpoint path /ikmedia. Each must
// carry unpic's operations (and unpic's defaults c-maintain_ratio,fo-auto
// where it adds them) and keep the source path.
func TestUnpic(t *testing.T) {
	t.Parallel()
	p, err := imagekit.New(&imagekit.Options{EndpointPath: "/ikmedia", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	fillDefaults := func(w unpicWant) unpicWant {
		if w.Type == transform.ResizeUnset {
			w.Type, w.Enlarge = transform.ResizeFill, true
		}
		if w.Gravity == transform.GravityUnset {
			w.Gravity = transform.GravitySmart
		}
		return w
	}
	white := transform.Color{R: 255, G: 255, B: 255, A: 255}
	tests := []struct {
		name, url string
		want      unpicWant
	}{
		// extract: operations {width: 300, height: 300}.
		{
			"extract_query", unpicImg + "?tr=w-300,h-300",
			unpicWant{W: 300, H: 300, Type: transform.ResizeFill, Enlarge: true},
		},
		{
			"extract_path", "https://ik.imagekit.io/ikmedia/tr:w-300,h-300/docs_images/examples/example_food_3.jpg",
			unpicWant{W: 300, H: 300, Type: transform.ResizeFill, Enlarge: true},
		},
		// transform outputs.
		{
			"transform_defaults", unpicImg + "?tr=w-300,h-300,c-maintain_ratio,fo-auto",
			fillDefaults(unpicWant{W: 300, H: 300}),
		},
		{
			"transform_override_defaults", unpicImg + "?tr=c-force%2Cfo-center%2Cq-80%2Cw-300%2Ch-200",
			unpicWant{W: 300, H: 200, Type: transform.ResizeForce, Enlarge: true, Gravity: transform.GravityCenter, Quality: 80},
		},
		{
			"transform_retransform_query", unpicImg + "?tr=blur-5%2Cw-300%2Ch-200%2Cq-75%2Cc-maintain_ratio%2Cfo-auto",
			fillDefaults(unpicWant{W: 300, H: 200, Quality: 75, Blur: 2.5}),
		},
		{
			"transform_retransform_path", unpicImg + "?tr=q-75%2Cblur-5%2Cw-300%2Ch-200%2Cc-maintain_ratio%2Cfo-auto",
			fillDefaults(unpicWant{W: 300, H: 200, Quality: 75, Blur: 2.5}),
		},
		// generate outputs.
		{
			"generate_defaults", unpicImg + "?tr=w-200,h-100,c-maintain_ratio,fo-auto",
			fillDefaults(unpicWant{W: 200, H: 100}),
		},
		{
			"generate_format", unpicImg + "?tr=w-300,f-webp,c-maintain_ratio,fo-auto",
			fillDefaults(unpicWant{W: 300, Format: transform.FormatWebP}),
		},
		{
			"generate_complex", unpicImg + "?tr=w-400,h-300,cm-pad_resize,c-at_max_enlarge,fo-bottom,bg-FFFFFF,r-90,q-85",
			unpicWant{W: 400, H: 300, Type: transform.ResizeFit, Enlarge: true, Quality: 85, Extend: true, BG: &white, RadiusAll: 90},
		},
		{
			"generate_no_defaults", unpicImg + "?tr=w-300,h-200,c-force,fo-center",
			unpicWant{W: 300, H: 200, Type: transform.ResizeForce, Enlarge: true, Gravity: transform.GravityCenter},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			u, err := url.Parse(tt.url)
			if err != nil {
				t.Fatal(err)
			}
			res, err := p.Parse(&dialect.Input{Path: u.EscapedPath(), RawQuery: u.RawQuery, Host: u.Host})
			if err != nil {
				t.Fatal(err)
			}
			checkResult(t, res)
			if res.SourceString != "docs_images/examples/example_food_3.jpg" {
				t.Errorf("source %q", res.SourceString)
			}
			r := res.Request
			if len(r.Pipelines) != 1 {
				t.Fatalf("%d pipelines", len(r.Pipelines))
			}
			pl := &r.Pipelines[0]
			got := unpicWant{
				W: pl.Resize.Width.Px, H: pl.Resize.Height.Px, Type: pl.Resize.Type, Enlarge: pl.Resize.Enlarge,
				Gravity: pl.Gravity.Type, Quality: r.Output.Quality, Format: r.Output.Format, Blur: pl.Blur,
				Extend: pl.Extend.Enabled, BG: pl.Background,
			}
			if pl.Extend.Enabled && tt.want.Gravity == transform.GravityUnset {
				// fo-bottom places the image inside the padded box.
				if pl.Extend.Gravity.Type != transform.GravitySouth {
					t.Errorf("extend gravity %v", pl.Extend.Gravity.Type)
				}
			}
			if rad := pl.Radius; rad != nil && rad.TopLeft == rad.BottomRight {
				got.RadiusAll = rad.TopLeft
			}
			if d := cmp.Diff(tt.want, got); d != "" {
				t.Errorf("mismatch (-unpic +got):\n%s", d)
			}
		})
	}
}
