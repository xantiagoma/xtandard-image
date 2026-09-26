//go:build cgo

package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// pipeline is the production Prober and Processor, used the way the server
// uses them.
type pipeline struct {
	prober *codecProber
	proc   *processor
	parser *imgproxy.Parser
}

func newPipeline(t *testing.T) *pipeline {
	t.Helper()
	cfg, _, err := config.Load(config.LoadOptions{Environ: []string{"XI_DEV_MODE=true"}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	vcfg := vips.DefaultConfig()
	if err := vips.Init(&vcfg); err != nil {
		t.Fatal(err)
	}
	proc, err := newProcessor(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	parser, err := imgproxy.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &pipeline{prober: &codecProber{}, proc: proc, parser: parser}
}

// run probes and processes a fixture with imgproxy options, returning the
// encoded output. lim.MaxFrames is also set as the request's
// max_animation_frames, which the server turns into the probe limit.
func (p *pipeline) run(t *testing.T, data []byte, options string, lim server.ProbeLimits, f transform.Format) []byte {
	t.Helper()
	res, err := p.parser.Parse(&dialect.Input{Path: "/insecure/" + options + "/plain/local:///x"})
	if err != nil {
		t.Fatalf("parse %q: %v", options, err)
	}
	req := res.Request
	req.Limits.MaxAnimationFrames = lim.MaxFrames
	req.ApplyDefaults(transform.StandardDefaults())
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	src, err := vips.NewBuffer(len(data))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Release()
	copy(src.Bytes(), data)
	meta, err := p.prober.Probe(t.Context(), src, lim)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	out, err := p.proc.Process(t.Context(), &server.ProcessInput{
		Request: req, Source: src, Meta: meta, Decision: negotiate.Decision{Format: f},
	})
	if err != nil {
		t.Fatalf("process %q: %v", options, err)
	}
	defer out.Body.Release()
	return bytes.Clone(out.Body.Bytes())
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(golden.ModuleRoot(t), "testdata", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// outHeader is what libvips reads back from an encoded output.
type outHeader struct {
	width, height, orientation, pages int
	fields                            map[vips.MetaField]bool
	text                              map[vips.MetaField]string
	blobs                             map[vips.MetaField][]byte
}

func readOutput(t *testing.T, data []byte) outHeader {
	t.Helper()
	buf, err := vips.NewBuffer(len(data))
	if err != nil {
		t.Fatal(err)
	}
	defer buf.Release()
	copy(buf.Bytes(), data)
	h := outHeader{fields: map[vips.MetaField]bool{}, text: map[vips.MetaField]string{}, blobs: map[vips.MetaField][]byte{}}
	err = vips.Run(t.Context(), func() error {
		img, err := vips.Load(t.Context(), buf, &vips.LoadOptions{NPages: -1})
		if err != nil {
			return err
		}
		defer img.Close()
		h.width, h.height, h.orientation, h.pages = img.Width(), img.PageHeight(), img.Orientation(), img.Pages()
		for _, f := range img.Fields() {
			h.fields[f] = true
			if s, ok := img.Text(f); ok {
				h.text[f] = s
			}
			if b, ok := img.Blob(f); ok {
				h.blobs[f] = b
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	return h
}

func (h *outHeader) hasPrefix(prefix string) []vips.MetaField {
	var out []vips.MetaField
	for f := range h.fields {
		if strings.HasPrefix(string(f), prefix) {
			out = append(out, f)
		}
	}
	return out
}

// TestProcessMetadata runs every combination of strip_metadata,
// keep_copyright and strip_color_profile through the production Prober and
// Processor on a Display P3 JPEG carrying EXIF (orientation 6, copyright,
// GPS), XMP (with GPS) and IPTC, and inspects the metadata of the JPEG, WebP
// and PNG outputs (FORMATS §4):
//
//   - GPS never leaves the server (EXIF GPS IFD, XMP exif:GPS*);
//   - strip_metadata removes EXIF, XMP and IPTC, except the EXIF Copyright
//     and Artist with keep_copyright; without it everything but GPS is kept
//     (IPTC only where the format holds it: JPEG);
//   - the P3 pixels are converted to sRGB; strip_color_profile drops the
//     profile, otherwise the output is tagged with the sRGB profile;
//   - the image is rotated upright and its orientation reset to 1.
//
// Not parallel (subtests included): see TestRunEndToEnd.
func TestProcessMetadata(t *testing.T) {
	p := newPipeline(t)
	data := fixture(t, "jpeg/metadata-p3.jpg")
	var srgb []byte
	if err := vips.Run(t.Context(), func() error {
		var err error
		srgb, err = vips.ProfileLoad(t.Context(), "srgb")
		return err
	}); err != nil || len(srgb) == 0 {
		t.Fatalf("srgb profile: %v", err)
	}
	src := readOutput(t, data)
	for _, f := range []vips.MetaField{"exif-ifd0-Make", "exif-ifd0-Copyright", "exif-ifd0-Artist", "exif-ifd2-DateTimeOriginal", "exif-ifd3-GPSLatitude", vips.FieldXMP, vips.FieldIPTC, vips.FieldICC} {
		if !src.fields[f] {
			t.Fatalf("fixture lacks %s", f)
		}
	}
	if src.orientation != 6 {
		t.Fatalf("fixture orientation %d, want 6", src.orientation)
	}

	for _, format := range []transform.Format{transform.FormatJPEG, transform.FormatWebP, transform.FormatPNG} {
		for _, strip := range []bool{true, false} {
			for _, keepCopyright := range []bool{true, false} {
				for _, stripProfile := range []bool{true, false} {
					options := fmt.Sprintf("sm:%d/kcr:%d/scp:%d", b2i(strip), b2i(keepCopyright), b2i(stripProfile))
					t.Run(format.String()+"/"+options, func(t *testing.T) {
						h := readOutput(t, p.run(t, data, options, server.ProbeLimits{}, format))
						checkMetadata(t, &h, format, strip, keepCopyright, stripProfile, srgb)
					})
				}
			}
		}
	}
}

func checkMetadata(t *testing.T, h *outHeader, format transform.Format, strip, keepCopyright, stripProfile bool, srgb []byte) {
	t.Helper()
	if h.width != 160 || h.height != 120 || h.orientation != 1 {
		t.Errorf("%dx%d orientation %d, want 160x120 upright with orientation 1", h.width, h.height, h.orientation)
	}
	if s, ok := h.text["exif-ifd0-Orientation"]; ok && !strings.HasPrefix(s, "1 ") {
		t.Errorf("EXIF Orientation = %q", s)
	}
	// GPS: never.
	if gps := h.hasPrefix("exif-ifd3-"); len(gps) > 0 {
		t.Errorf("GPS survived: %v", gps)
	}
	if x := h.blobs[vips.FieldXMP]; bytes.Contains(x, []byte("GPS")) {
		t.Errorf("GPS survived in XMP: %s", x)
	}
	// ICC.
	icc, hasICC := h.blobs[vips.FieldICC]
	switch {
	case stripProfile && hasICC:
		t.Errorf("strip_color_profile: ICC profile kept (%d bytes)", len(icc))
	case !stripProfile && !bytes.Equal(icc, srgb):
		t.Errorf("strip_color_profile=false: ICC is %d bytes, want the %d-byte sRGB profile", len(icc), len(srgb))
	}
	// EXIF, XMP, IPTC.
	var want map[vips.MetaField]bool
	switch {
	case strip && keepCopyright:
		want = map[vips.MetaField]bool{
			"exif-ifd0-Copyright": true, "exif-ifd0-Artist": true,
			"exif-ifd0-Make": false, "exif-ifd0-Model": false, "exif-ifd2-DateTimeOriginal": false,
			vips.FieldXMP: false, vips.FieldIPTC: false,
		}
	case strip:
		want = map[vips.MetaField]bool{vips.FieldEXIF: false, vips.FieldXMP: false, vips.FieldIPTC: false}
		if f := h.hasPrefix("exif-ifd"); len(f) > 0 {
			t.Errorf("EXIF fields survived: %v", f)
		}
	default:
		want = map[vips.MetaField]bool{
			"exif-ifd0-Make": true, "exif-ifd0-Model": true, "exif-ifd0-Copyright": true, "exif-ifd0-Artist": true,
			"exif-ifd2-DateTimeOriginal": true, vips.FieldXMP: true,
			vips.FieldIPTC: format == transform.FormatJPEG,
		}
	}
	for f, present := range want {
		if h.fields[f] != present {
			t.Errorf("%s present = %v, want %v", f, h.fields[f], present)
		}
	}
	if c, ok := h.text["exif-ifd0-Copyright"]; ok && !strings.HasPrefix(c, "(c) Jane") {
		t.Errorf("copyright = %q", c)
	}
	if x, ok := h.blobs[vips.FieldXMP]; ok && !bytes.Contains(x, []byte("(c) Jane")) {
		t.Errorf("XMP lost its rights: %s", x)
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestProcessPagesAndFrames: the prober reports the page count, so the
// `page` option reaches the second page of a multi-page TIFF (whose size
// differs from the first); max_animation_frames=1 processes the first frame
// of an animation instead of rejecting it.
//
// Not parallel (subtests included): see TestRunEndToEnd.
func TestProcessPagesAndFrames(t *testing.T) {
	p := newPipeline(t)
	tiff := fixture(t, "tiff/multipage.tif")
	gif := fixture(t, "gif/animated.gif")
	for _, tc := range []struct {
		name    string
		data    []byte
		options string
		lim     server.ProbeLimits
		format  transform.Format
		w, h    int
		pages   int
	}{
		{name: "tiff page 0", data: tiff, options: "rs:fit:1000:1000", format: transform.FormatPNG, w: 160, h: 120, pages: 1},
		{name: "tiff page 1", data: tiff, options: "pg:1", format: transform.FormatPNG, w: 96, h: 72, pages: 1},
		{name: "tiff page past the end", data: tiff, options: "pg:5", format: transform.FormatPNG, w: 96, h: 72, pages: 1},
		{name: "gif all frames", data: gif, options: "rs:fit:48:48", format: transform.FormatGIF, w: 48, h: 36, pages: 6},
		{name: "gif max frames 1", data: gif, options: "rs:fit:48:48", lim: server.ProbeLimits{MaxFrames: 1}, format: transform.FormatGIF, w: 48, h: 36, pages: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := readOutput(t, p.run(t, tc.data, tc.options, tc.lim, tc.format))
			if h.width != tc.w || h.height != tc.h || h.pages != tc.pages {
				t.Errorf("%dx%d × %d, want %dx%d × %d", h.width, h.height, h.pages, tc.w, tc.h, tc.pages)
			}
		})
	}
}
