package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/server"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/testutil/leakcheck"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// TestProcessNoLeak sends 500 full requests (probe, plan, execute, encode)
// through the production Prober and Processor, cycling through JPEG, PNG
// with alpha, BMP, an EXIF-rotated JPEG, animated GIF/WebP, PDF pages
// (one, and all stacked), a PSD without a composite and the WP-105 DDS,
// L*a*b* TIFF and CMYK JPEG XL inputs, and checks
// that libvips' tracked memory, allocations, open files and live objects
// return to their baseline.
//
// Tolerance: leakcheck.Default once the counters settle (libvips' worker
// threads free their reserve buffers just after a sink returns); one leaked
// source buffer or image per request would show up as ≥ 500 objects or
// tens of MB.
//
// Not parallel: the counters are process-wide (see TestRunEndToEnd for the
// libvips lifecycle).
func TestProcessNoLeak(t *testing.T) {
	const requests = 500
	cfg, _, err := config.Load(config.LoadOptions{Environ: []string{"XI_DEV_MODE=true"}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	vcfg := vips.DefaultConfig()
	if err := vips.Init(&vcfg); err != nil {
		if errors.Is(err, vips.ErrUnavailable) {
			t.Skip("libvips not available (CGO_ENABLED=0)")
		}
		t.Fatal(err)
	}
	proc, err := newProcessor(&cfg, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	prober := &codecProber{}
	parser, err := imgproxy.New(nil)
	if err != nil {
		t.Fatal(err)
	}

	type reqCase struct {
		fixture, options string
		format           transform.Format
	}
	cases := []reqCase{
		{"jpeg/baseline.jpg", "rs:fill:100:100/g:sm", transform.FormatJPEG},
		{"png/rgba8.png", "rs:fit:64:64/pd:4", transform.FormatWebP},
		{"bmp/rgb24.bmp", "rs:fit:40:40", transform.FormatPNG},
		{"jpeg/exif-orientation-6-icc.jpg", "rs:fit:80:80/ex:1/bg:255:0:0", transform.FormatJPEG},
		{"gif/animated.gif", "rs:fill:32:32", transform.FormatGIF},
		{"webp/animated.webp", "c:48:48/rs:fit:24:24", transform.FormatWebP},
		// Auto-quality: several saves and decodes per request (WP-56).
		{"jpeg/baseline.jpg", "rs:fit:120:120/aq:dssim", transform.FormatJPEG},
		{"png/rgba8.png", "aq:dssim:0.01", transform.FormatWebP},
		{"jpeg/baseline.jpg", "rs:fit:120:120/aq:size:2000", transform.FormatWebP},
		{"gif/animated.gif", "rs:fill:32:32/aq:dssim", transform.FormatWebP},
		{"pdf/pages.pdf", "pg:1/rs:fit:100:0", transform.FormatJPEG},
		{"pdf/pages.pdf", "pgs:-1/rs:fit:0:200", transform.FormatPNG},
		{"psd/no-composite.psd", "rs:fit:48:48", transform.FormatPNG},
		// Best format: analysis plus up to three candidate encodes (WP-57).
		{"jpeg/baseline.jpg", "rs:fit:120:120/f:best", transform.FormatBest},
		{"png/palette.png", "f:best/aq:dssim", transform.FormatBest},
		{"gif/animated.gif", "rs:fill:32:32/f:best", transform.FormatBest},
		// WP-105: DDS decoded in Go (BC7, BC6H tone-mapped), CIE L*a*b* TIFF
		// through its relabelled copy, CMYK JPEG XL through the shim's
		// libjxl loader.
		{"dds/bc7-alpha.dds", "rs:fit:32:32", transform.FormatPNG},
		{"dds/bc6h-scene.dds", "rs:fit:32:32", transform.FormatJPEG},
		{"tiff/lab8-alpha.tif", "rs:fit:32:32", transform.FormatPNG},
		{"tiff/lab16.tif", "rs:fit:32:32", transform.FormatJPEG},
		{"jxl/cmyk-alpha.jxl", "rs:fit:32:32", transform.FormatWebP},
	}
	negCfg := negotiate.DefaultConfig()
	accept := negotiate.ParseAccept("image/avif,image/webp,*/*")
	type prepared struct {
		data []byte
		req  *transform.Request
		f    transform.Format
	}
	in := make([]prepared, 0, len(cases))
	for _, c := range cases {
		data, err := os.ReadFile(filepath.Join(golden.ModuleRoot(t), "testdata", "fixtures", c.fixture))
		if err != nil {
			t.Fatal(err)
		}
		res, err := parser.Parse(&dialect.Input{Path: "/insecure/" + c.options + "/plain/local:///x"})
		if err != nil {
			t.Fatal(err)
		}
		res.Request.ApplyDefaults(transform.StandardDefaults())
		if err := res.Request.Validate(); err != nil {
			t.Fatal(err)
		}
		in = append(in, prepared{data: data, req: res.Request, f: c.format})
	}

	request := func(i int) {
		c := &in[i%len(in)]
		src, err := vips.NewBuffer(len(c.data))
		if err != nil {
			t.Fatal(err)
		}
		defer src.Release()
		copy(src.Bytes(), c.data)
		meta, err := prober.Probe(t.Context(), src, server.ProbeLimits{})
		if err != nil {
			t.Fatal(err)
		}
		d := negotiate.Decision{Format: c.f}
		if c.f == transform.FormatBest {
			if d, err = negotiate.Decide(c.req, accept, &negCfg, negotiate.SourceMeta{
				Format: meta.Format, HasAlpha: meta.HasAlpha, IsAnimated: meta.IsAnimated, FrameCount: meta.Frames,
			}); err != nil {
				t.Fatal(err)
			}
		}
		out, err := proc.Process(t.Context(), &server.ProcessInput{
			Request: c.req, Source: src, Meta: meta, Decision: d,
		})
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if (c.f == transform.FormatBest) != (out.Best != "") {
			t.Fatalf("request %d: best summary %q for %s", i, out.Best, c.f)
		}
		if len(out.Body.Bytes()) == 0 {
			t.Fatalf("request %d: empty body", i)
		}
		out.Body.Release()
	}
	for i := range 2 * len(in) {
		request(i) // warm up lazily initialised libvips state
	}

	before := leakcheck.Take()
	for i := range requests {
		request(i)
	}
	after := leakcheck.Check(t, before, leakcheck.Default, "requests")
	t.Logf("after %d requests: mem %+d B, allocs %+d, objects %+d, highwater %d B",
		requests, after.Mem-before.Mem, after.Allocs-before.Allocs, after.Objects-before.Objects, after.MemHighwater)
}
