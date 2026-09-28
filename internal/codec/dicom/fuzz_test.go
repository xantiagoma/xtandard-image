package dicom_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec/dicom"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

func addSeeds(f *testing.F) {
	f.Helper()
	for _, name := range fixtures {
		f.Add(readFixture(f, name))
	}
	f.Add(build("1.2.840.10008.1.2.1", image(4, 16)))
}

// allowed reports the error kinds a hostile file may produce.
func allowed(t *testing.T, what string, err error) {
	t.Helper()
	switch errs.KindOf(err) {
	case errs.KindCorrupt, errs.KindUnsupported, errs.KindLimitExceeded:
	default:
		t.Fatalf("%s: unexpected kind %v: %v", what, errs.KindOf(err), err)
	}
}

func FuzzParseHeader(f *testing.F) {
	addSeeds(f)
	f.Fuzz(func(t *testing.T, b []byte) {
		h, err := dicom.ParseHeader(b)
		if err != nil {
			allowed(t, "ParseHeader", err)
			return
		}
		if h.Width < 1 || h.Height < 1 || h.Frames < 1 || (h.Samples != 1 && h.Samples != 3) {
			t.Fatalf("accepted header %+v", h)
		}
	})
}

func FuzzDecode(f *testing.F) {
	addSeeds(f)
	f.Fuzz(func(t *testing.T, b []byte) {
		r, err := dicom.Decode(b, dicom.Options{MaxPixels: 1 << 20})
		if err != nil {
			allowed(t, "Decode", err)
			return
		}
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
		h, err := dicom.ParseHeader(b)
		if err != nil {
			t.Fatalf("Decode succeeded where ParseHeader failed: %v", err)
		}
		if r.Width != h.Width || r.Height != h.Height || r.Bands != 3 {
			t.Fatalf("decoded %dx%dx%d for %dx%d", r.Width, r.Height, r.Bands, h.Width, h.Height)
		}
	})
}

func FuzzFirstFrame(f *testing.F) {
	addSeeds(f)
	f.Fuzz(func(t *testing.T, b []byte) {
		h, err := dicom.ParseHeader(b)
		if err != nil || !h.Encoding.Codec() {
			return
		}
		frame, err := dicom.FirstFrame(b, &h)
		if err != nil {
			allowed(t, "FirstFrame", err)
			return
		}
		if len(frame) == 0 || len(frame) > len(b) {
			t.Fatalf("first frame of %d bytes from a %d-byte file", len(frame), len(b))
		}
		// Rendering arbitrary samples of the declared shape never fails
		// with anything but the allowed kinds.
		s := dicom.Samples{Width: 2, Height: 2, Bands: h.Samples, Data: make([]int32, 4*h.Samples)}
		for i := range s.Data {
			s.Data[i] = int32(i*977) - 1000
		}
		if _, err := h.Render(s); err != nil {
			allowed(t, "Render", err)
		}
	})
}
