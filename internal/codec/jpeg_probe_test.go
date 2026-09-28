//go:build cgo

package codec_test

import (
	"errors"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// TestProbeJPEGFrames: the frames no loader decodes are refused at probe
// with a clear 422 (WP-101); 12-bit DCT and lossless frames reach the
// shim's jpeghpload (WP-106).
func TestProbeJPEGFrames(t *testing.T) {
	t.Parallel()
	base := readFixture(t, "jpeg/baseline.jpg")
	for _, tc := range []struct {
		name   string
		data   []byte
		public string // "" = probes
		bits   int
	}{
		{"8-bit", base, "", 8},
		{"12-bit header", withPrecision(t, base, 0xC1, 12), "", 12},
		{"16-bit DCT", withPrecision(t, base, 0xC1, 16), "16-bit JPEG is not supported", 0},
		{"hierarchical", withPrecision(t, base, 0xC5, 8), "hierarchical JPEG is not supported", 0},
		{"arithmetic lossless", withPrecision(t, base, 0xCB, 8), "arithmetic-coded lossless JPEG is not supported", 0},
		{"1-bit lossless", withPrecision(t, base, 0xC3, 1), "1-bit lossless JPEG is not supported", 0},
		{"JPEG-LS", withPrecision(t, base, 0xF7, 8), "JPEG-LS is not supported", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, err := probe(t.Context(), t, tc.data, codec.Limits{}, "image/jpeg")
			if tc.public == "" {
				if err != nil || m.BitDepth != tc.bits || m.Format != codec.FormatJPEG {
					t.Errorf("probe: %v, bit depth %d; want JPEG, %d bits", err, m.BitDepth, tc.bits)
				}
				return
			}
			e, ok := errors.AsType[*errs.Error](err)
			if errs.KindOf(err) != errs.KindUnsupported || !ok || e.PublicMessage() != tc.public {
				t.Errorf("probe: %v, want unsupported %q", err, tc.public)
			}
		})
	}
}
