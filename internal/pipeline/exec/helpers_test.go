//go:build cgo

package exec_test

import (
	"bytes"
	"image"
	"os"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// These tests need libvips from the base image: run them with
// `task docker:test`.
func TestMain(m *testing.M) {
	cfg := vips.DefaultConfig()
	if err := vips.Init(&cfg); err != nil {
		_, _ = os.Stderr.WriteString("vips init: " + err.Error() + "\n")
		os.Exit(1)
	}
	code := m.Run()
	vips.Shutdown()
	os.Exit(code)
}

// testLimits are the documented XI_LIMITS_* defaults.
var testLimits = plan.Limits{
	MaxSrcPixels:       50_000_000,
	MaxResultDimension: 8192,
	MaxAnimationFrames: 256,
}

func newExecutor(tb testing.TB) *exec.Executor {
	tb.Helper()
	ec := encode.DefaultConfig()
	enc, err := encode.New(&ec)
	if err != nil {
		tb.Fatal(err)
	}
	e, err := exec.New(&exec.Config{Limits: testLimits}, enc)
	if err != nil {
		tb.Fatal(err)
	}
	return e
}

func readFixture(tb testing.TB, name string) []byte {
	tb.Helper()
	b, err := os.ReadFile(golden.Fixture(tb, name))
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

func newBuffer(tb testing.TB, data []byte) *vips.Buffer {
	tb.Helper()
	buf, err := vips.NewBufferFromReader(bytes.NewReader(data), 1<<30)
	if err != nil {
		tb.Fatalf("NewBufferFromReader: %v", err)
	}
	return buf
}

// parse turns imgproxy processing options ("rs:fill:100:100/g:sm") into a
// normalised request, the way the server does.
func parse(tb testing.TB, options string) *transform.Request {
	tb.Helper()
	p, err := imgproxy.New(nil)
	if err != nil {
		tb.Fatal(err)
	}
	path := "/insecure/" + strings.Trim(options, "/") + "/plain/local:///img"
	if options == "" {
		path = "/insecure/plain/local:///img"
	}
	res, err := p.Parse(&dialect.Input{Path: path})
	if err != nil {
		tb.Fatalf("parse %q: %v", options, err)
	}
	req := res.Request
	req.ApplyDefaults(transform.StandardDefaults())
	if err := req.Validate(); err != nil {
		tb.Fatalf("validate %q: %v", options, err)
	}
	return req
}

// job builds the executor input for data the way internal/app does: probe,
// sniff, target from the request and a negotiated format.
func job(tb testing.TB, data []byte, req *transform.Request, out transform.Format) (*exec.Job, *vips.Buffer) {
	tb.Helper()
	return jobMeta(tb, data, data, req, out)
}

// jobMeta is job with the header probed from header and the pixels loaded
// from data (a truncated file behind a valid header).
func jobMeta(tb testing.TB, header, data []byte, req *transform.Request, out transform.Format) (*exec.Job, *vips.Buffer) {
	tb.Helper()
	hb := newBuffer(tb, header)
	defer hb.Release()
	var m codec.ImageMeta
	err := vips.Run(tb.Context(), func() error {
		var err error
		m, err = codec.Probe(tb.Context(), hb, codec.Limits{}, "")
		return err
	})
	if err != nil {
		tb.Fatalf("probe: %v", err)
	}
	buf := newBuffer(tb, data)
	name := m.Format.String()
	if m.Format == codec.FormatUHDR {
		name = "jpeg"
	}
	if out == transform.FormatUnset {
		out, _ = m.Format.Output()
	}
	d := negotiate.Decision{Format: out}
	return &exec.Job{
		Request: req,
		Source:  buf,
		Meta: plan.SourceMeta{
			Width: m.Width, Height: m.Height, Frames: max(m.Frames, m.Pages), HasAlpha: m.HasAlpha,
			Orientation: m.Orientation, IsAnimated: m.IsAnimated, Format: name,
			IsVector: m.Format == codec.FormatSVG,
		},
		Target: encode.NewTarget(&req.Output, &d, m.IsAnimated),
	}, buf
}

// output is a processed image decoded back to pixels.
type output struct {
	res    *exec.Result
	img    image.Image // every frame, stacked vertically
	frames int
	bytes  []byte
}

// process runs options on a fixture and decodes the result.
func process(tb testing.TB, fixture, options string, out transform.Format) output {
	tb.Helper()
	return processData(tb, readFixture(tb, fixture), options, out)
}

func processData(tb testing.TB, data []byte, options string, out transform.Format) output {
	tb.Helper()
	j, buf := job(tb, data, parse(tb, options), out)
	defer buf.Release()
	res, err := newExecutor(tb).Run(tb.Context(), j)
	if err != nil {
		tb.Fatalf("run %q: %v", options, err)
	}
	defer res.Body.Release()
	enc := bytes.Clone(res.Body.Bytes())
	img, frames := decode(tb, enc)
	return output{res: res, img: img, frames: frames, bytes: enc}
}

// decode loads every frame of an encoded image with libvips as 8-bit sRGBA.
func decode(tb testing.TB, data []byte) (image.Image, int) {
	tb.Helper()
	buf := newBuffer(tb, data)
	defer buf.Release()
	var pix []byte
	var w, h, frames int
	err := vips.Run(tb.Context(), func() error {
		var a vips.Arena
		defer a.Close()
		ctx := tb.Context()
		img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{NPages: -1}))
		if err != nil {
			return err
		}
		frames = img.Pages()
		if img.Interpretation() != vips.InterpretationSRGB {
			if img, err = a.Add(img.Colourspace(ctx, vips.InterpretationSRGB)); err != nil {
				return err
			}
		}
		if !img.HasAlpha() {
			if img, err = a.Add(img.AddAlpha(ctx)); err != nil {
				return err
			}
		}
		if img, err = a.Add(img.Cast(ctx, vips.BandFormatUchar)); err != nil {
			return err
		}
		w, h = img.Width(), img.Height()
		pix, err = img.WriteToMemory(ctx)
		return err
	})
	if err != nil {
		tb.Fatalf("decode output: %v", err)
	}
	return &image.NRGBA{Pix: pix, Stride: 4 * w, Rect: image.Rect(0, 0, w, h)}, frames
}
