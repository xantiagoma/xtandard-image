//go:build cgo

package codec_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// TestProbeEPS: with Ghostscript the page box at 72 DPI (never running
// gs); without, the TIFF preview or a 422 with the public message.
func TestProbeEPS(t *testing.T) {
	t.Parallel()
	page := func(w, h, pages int) codec.ImageMeta {
		return codec.ImageMeta{
			Format: codec.FormatEPS, Width: w, Height: h, Frames: 1, Pages: pages, PageHeight: h,
			HasAlpha: true, Orientation: 1, BitDepth: 8, Loop: -1, ColorSpace: codec.ColorSpaceSRGB,
		}
	}
	on := codec.Limits{EPSEnabled: true}
	tests := []struct {
		name   string
		data   []byte
		lim    codec.Limits
		want   codec.ImageMeta
		kind   errs.Kind
		public string
	}{
		{"eps", readFixture(t, "eps/simple.eps"), on, page(100, 50, 1), 0, ""},
		{"postscript pages", readFixture(t, "eps/pages.ps"), on, page(200, 100, 3), 0, ""},
		{"illustrator", readFixture(t, "eps/illustrator.ai"), on, page(160, 120, 1), 0, ""},
		{"dos eps renders its postscript", readFixture(t, "eps/preview.eps"), on, page(96, 72, 1), 0, ""},
		{"fractional box rounds up", []byte("%!PS\n%%HiResBoundingBox: 0 0 10.2 20.9\n"), on, page(11, 21, 1), 0, ""},
		{
			"disabled", readFixture(t, "eps/simple.eps"),
			codec.Limits{},
			codec.ImageMeta{},
			errs.KindUnsupported,
			codec.EPSDisabledMessage,
		},
		{
			"disabled postscript", readFixture(t, "eps/pages.ps"),
			codec.Limits{},
			codec.ImageMeta{},
			errs.KindUnsupported,
			codec.EPSDisabledMessage,
		},
		// 30000 × 30000 pt at 72 DPI is 900 MP.
		{"over the pixel limit", []byte("%!PS\n%%BoundingBox: 0 0 30000 30000\n"), on, codec.ImageMeta{}, errs.KindLimitExceeded, ""},
		{"absurd box", []byte("%!PS\n%%BoundingBox: 0 0 1e9 10\n"), on, codec.ImageMeta{}, errs.KindCorrupt, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := probe(t.Context(), t, tt.data, tt.lim, "")
			if tt.kind != 0 {
				if errs.KindOf(err) != tt.kind {
					t.Fatalf("Probe error %v, want kind %s", err, tt.kind)
				}
				if e, ok := errors.AsType[*errs.Error](err); tt.public != "" && (!ok || e.PublicMessage() != tt.public) {
					t.Errorf("public message of %v, want %q", err, tt.public)
				}
				return
			}
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Probe (-want +got):\n%s", diff)
			}
		})
	}
}
