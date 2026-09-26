//go:build cgo

package raw_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec/raw"
	"github.com/xantiagoma/xtandard-image/internal/testutil/rawfixtures"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// These tests need LibRaw and libvips from the base image: run them with
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

func probe(tb testing.TB, data []byte) (raw.Header, error) {
	tb.Helper()
	buf, err := vips.NewBufferFromReader(bytes.NewReader(data), 1<<30)
	if err != nil {
		tb.Fatal(err)
	}
	defer buf.Release()
	var h raw.Header
	err = vips.Run(tb.Context(), func() error {
		var err error
		h, err = raw.Probe(tb.Context(), buf)
		return err
	})
	return h, err
}

// TestProbe checks the kind, the size, the orientation and the preview
// picked for the synthetic DNGs and every pinned camera sample.
func TestProbe(t *testing.T) {
	t.Parallel()
	type want struct {
		kind        raw.Kind
		w, h        int
		orientation int
		pw, ph      int
	}
	for _, tc := range []struct {
		name string
		data func(testing.TB) []byte
		want want
	}{
		{
			"synthetic landscape", func(tb testing.TB) []byte { tb.Helper(); return readFixture(tb, "dng/landscape.dng") },
			want{raw.KindDNG, 192, 128, 1, 96, 64},
		},
		{
			"synthetic portrait", func(tb testing.TB) []byte { tb.Helper(); return readFixture(tb, "dng/portrait-orientation-6.dng") },
			want{raw.KindDNG, 192, 128, 6, 48, 32},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, err := probe(t, tc.data(t))
			if err != nil {
				t.Fatal(err)
			}
			got := want{h.Kind, h.Width, h.Height, h.Orientation, h.Preview.Width, h.Preview.Height}
			if got != tc.want || !h.HasPreview {
				t.Errorf("Probe = %+v (preview %t), want %+v", got, h.HasPreview, tc.want)
			}
		})
	}
	previews := map[string][2]int{
		"cr2": {1536, 1024}, "cr3": {3408, 2272}, "nef": {3008, 2000},
		"arw": {1616, 1080}, "dng": {4928, 3264}, "raf": {4416, 2944},
	}
	for _, f := range rawfixtures.Files() {
		t.Run(f.Kind, func(t *testing.T) {
			t.Parallel()
			h, err := probe(t, rawfixtures.Read(t, &f))
			if err != nil {
				t.Fatal(err)
			}
			if h.Kind.String() != f.Kind || h.Orientation != f.Orientation {
				t.Errorf("kind %v, orientation %d; want %s, %d", h.Kind, h.Orientation, f.Kind, f.Orientation)
			}
			if p := previews[f.Kind]; !h.HasPreview || h.Preview.Width != p[0] || h.Preview.Height != p[1] {
				t.Errorf("preview %+v (%t), want %dx%d", h.Preview, h.HasPreview, p[0], p[1])
			}
		})
	}
}
