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
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// TestProcessNoLeak sends 500 full requests (probe, plan, execute, encode)
// through the production Prober and Processor, cycling through JPEG, PNG
// with alpha, BMP, an EXIF-rotated JPEG and animated GIF/WebP, and checks
// that libvips' tracked memory, allocations, open files and live objects
// return to their baseline.
//
// Tolerance as in internal/vips TestNoLeak: libvips' worker threads keep
// per-thread buffers that wobble by a few hundred KiB; one leaked source
// buffer or image per request would show up as ≥ 500 objects or tens of MB.
//
// Not parallel: the counters are process-wide (see TestRunEndToEnd for the
// libvips lifecycle).
func TestProcessNoLeak(t *testing.T) {
	const (
		requests    = 500
		slackBytes  = 1 << 20
		slackAllocs = 64
		slackObjs   = 16
	)
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
	proc, err := newProcessor(&cfg)
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
	}
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
		out, err := proc.Process(t.Context(), &server.ProcessInput{
			Request: c.req, Source: src, Meta: meta, Decision: negotiate.Decision{Format: c.f},
		})
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if len(out.Body.Bytes()) == 0 {
			t.Fatalf("request %d: empty body", i)
		}
		out.Body.Release()
	}
	for i := range 2 * len(in) {
		request(i) // warm up lazily initialised libvips state
	}

	before, objects := vips.ReadMemStats(), vips.LiveObjects()
	for i := range requests {
		request(i)
	}
	after, objectsAfter := vips.ReadMemStats(), vips.LiveObjects()
	if after.Mem-before.Mem > slackBytes || after.Allocs-before.Allocs > slackAllocs ||
		after.Files != before.Files || objectsAfter-objects > slackObjs {
		t.Errorf("after %d requests: mem %d → %d B, allocs %d → %d, files %d → %d, objects %d → %d",
			requests, before.Mem, after.Mem, before.Allocs, after.Allocs, before.Files, after.Files,
			objects, objectsAfter)
	}
	t.Logf("after %d requests: mem %+d B, allocs %+d, objects %+d, highwater %d B",
		requests, after.Mem-before.Mem, after.Allocs-before.Allocs, objectsAfter-objects, after.MemHighwater)
}
