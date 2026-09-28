package gainmap_test

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/codec/gainmap"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
)

// seineTmap is the tmap item of libavif's seine_sdr_gainmap_srgb.avif
// (BSD-2-Clause), which libavif wrote from the Adobe XMP of
// seine_sdr_gainmap_srgb.jpg (seineXMP below): the two must agree.
const seineTmap = "0000000000c000000000000000010000000d0000000afffc1475000f424000137cf9000f42400001d1b70001e848" +
	"00000001000000400000000100000040ffff33cf00030d4000137d13000f42400002df3b00030d400000000100000040" +
	"0000000100000040fffeee490003d09000138011000f4240000703bf0007a1200000000100000040000000010000004" + "0"

var seine = gainmap.Metadata{
	Channels:           3,
	Min:                [3]float64{-0.256907, -0.261365, -0.280284},
	Max:                [3]float64{1.277177, 1.277203, 1.277969},
	Gamma:              [3]float64{0.953784, 0.941095, 0.919422},
	BaseOffset:         [3]float64{0.015625, 0.015625, 0.015625},
	AlternateOffset:    [3]float64{0.015625, 0.015625, 0.015625},
	BaseHeadroom:       0,
	AlternateHeadroom:  1.3,
	UseBaseColourSpace: true,
}

const seineXMP = `<x:xmpmeta xmlns:x="adobe:ns:meta/" x:xmptk="Adobe XMP Core 7.0-c000 1.000000, 0000/00/00-00:00:00        ">
 <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
  <rdf:Description rdf:about=""
    xmlns:hdrgm="http://ns.adobe.com/hdr-gain-map/1.0/"
   hdrgm:Version="1.0"
   hdrgm:BaseRenditionIsHDR="False"
   hdrgm:OffsetSDR="0.015625"
   hdrgm:OffsetHDR="0.015625"
   hdrgm:HDRCapacityMin="0"
   hdrgm:HDRCapacityMax="1.3">
   <hdrgm:GainMapMin>
    <rdf:Seq>
     <rdf:li>-0.256907</rdf:li>
     <rdf:li>-0.261365</rdf:li>
     <rdf:li>-0.280284</rdf:li>
    </rdf:Seq>
   </hdrgm:GainMapMin>
   <hdrgm:GainMapMax>
    <rdf:Seq>
     <rdf:li>1.277177</rdf:li>
     <rdf:li>1.277203</rdf:li>
     <rdf:li>1.277969</rdf:li>
    </rdf:Seq>
   </hdrgm:GainMapMax>
   <hdrgm:Gamma>
    <rdf:Seq>
     <rdf:li>0.953784</rdf:li>
     <rdf:li>0.941095</rdf:li>
     <rdf:li>0.919422</rdf:li>
    </rdf:Seq>
   </hdrgm:Gamma>
  </rdf:Description>
 </rdf:RDF>
</x:xmpmeta>`

var approx = cmpopts.EquateApprox(0, 1e-6)

func mustHex(tb testing.TB, s string) []byte {
	tb.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

func TestParseTmap(t *testing.T) {
	t.Parallel()
	got, err := gainmap.ParseTmap(mustHex(t, seineTmap))
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(seine, got, approx); diff != "" {
		t.Errorf("ParseTmap (-want +got):\n%s", diff)
	}
	if got.BaseIsHDR() {
		t.Error("SDR base reported as HDR")
	}
}

// isoCommon builds a one-channel ISO payload with a common denominator.
func isoCommon(den uint32, vals ...uint32) []byte {
	b := []byte{0, 0, 0, 5, 0x08}
	b = binary.BigEndian.AppendUint32(b, den)
	for _, v := range vals {
		b = binary.BigEndian.AppendUint32(b, v)
	}
	return b
}

func TestParseISO(t *testing.T) {
	t.Parallel()
	neg := func(v int32) uint32 { return uint32(v) } //nolint:gosec // two's complement on purpose
	mono := gainmap.Metadata{
		Channels: 1, Min: [3]float64{-0.5, -0.5, -0.5}, Max: [3]float64{3, 3, 3}, Gamma: [3]float64{1, 1, 1},
		BaseOffset: [3]float64{0.25, 0.25, 0.25}, AlternateOffset: [3]float64{0.125, 0.125, 0.125},
		BaseHeadroom: 0, AlternateHeadroom: 3,
	}
	for _, tc := range []struct {
		name    string
		in      []byte
		want    gainmap.Metadata
		wantErr error
	}{
		{"common denominator", isoCommon(8, 0, 24, neg(-4), 24, 8, 2, 1), mono, nil},
		{"trailing bytes", append(isoCommon(8, 0, 24, neg(-4), 24, 8, 2, 1), 1, 2, 3), mono, nil},
		{"zero denominator", isoCommon(0, 0, 24, 0, 24, 8, 2, 1), gainmap.Metadata{}, gainmap.ErrMalformed},
		{"truncated", isoCommon(8, 0, 24, 0, 24), gainmap.Metadata{}, gainmap.ErrMalformed},
		{"minimum version", []byte{0, 1, 0, 0, 0}, gainmap.Metadata{}, gainmap.ErrUnsupported},
		{"max below min", isoCommon(8, 0, 24, 24, 0, 8, 2, 1), gainmap.Metadata{}, gainmap.ErrMalformed},
		{"zero gamma", isoCommon(8, 0, 24, 0, 24, 0, 2, 1), gainmap.Metadata{}, gainmap.ErrMalformed},
		{"empty", nil, gainmap.Metadata{}, gainmap.ErrMalformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := gainmap.ParseISO(tc.in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.want, got, approx); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
	if _, err := gainmap.ParseTmap([]byte{1, 0, 0}); !errors.Is(err, gainmap.ErrUnsupported) {
		t.Errorf("tmap version 1: %v, want unsupported", err)
	}
}

func TestParseXMP(t *testing.T) {
	t.Parallel()
	attrs := `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
<rdf:Description xmlns:hdrgm="http://ns.adobe.com/hdr-gain-map/1.0/" hdrgm:Version="1.0"
 hdrgm:GainMapMax="2.5" hdrgm:HDRCapacityMax="2.5"/></rdf:RDF></x:xmpmeta>`
	defaults := gainmap.Metadata{
		Channels: 1, Max: [3]float64{2.5, 2.5, 2.5}, Gamma: [3]float64{1, 1, 1},
		BaseOffset: [3]float64{1.0 / 64, 1.0 / 64, 1.0 / 64}, AlternateOffset: [3]float64{1.0 / 64, 1.0 / 64, 1.0 / 64},
		AlternateHeadroom: 2.5,
	}
	hdrBase := `<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"><rdf:Description
 xmlns:hdrgm="http://ns.adobe.com/hdr-gain-map/1.0/" hdrgm:Version="1.0" hdrgm:BaseRenditionIsHDR="True"
 hdrgm:GainMapMin="-2" hdrgm:GainMapMax="0" hdrgm:HDRCapacityMax="2"/></rdf:RDF>`
	for _, tc := range []struct {
		name    string
		in      string
		want    gainmap.Metadata
		ok      bool
		wantErr error
	}{
		{"seine (Seq elements)", seineXMP, func() gainmap.Metadata { m := seine; m.UseBaseColourSpace = false; return m }(), true, nil},
		{"attributes and defaults", attrs, defaults, true, nil},
		{"no version", `<x:xmpmeta xmlns:hdrgm="http://ns.adobe.com/hdr-gain-map/1.0/"/>`, gainmap.Metadata{}, false, nil},
		{"no hdrgm", `<x:xmpmeta xmlns:x="adobe:ns:meta/"/>`, gainmap.Metadata{}, false, nil},
		{
			"missing max", `<a xmlns:hdrgm="http://ns.adobe.com/hdr-gain-map/1.0/" hdrgm:Version="1.0" hdrgm:HDRCapacityMax="1"/>`,
			gainmap.Metadata{},
			true, gainmap.ErrMalformed,
		},
		{
			"bad number", `<a xmlns:hdrgm="http://ns.adobe.com/hdr-gain-map/1.0/" hdrgm:Version="1.0" hdrgm:GainMapMax="x" hdrgm:HDRCapacityMax="1"/>`,
			gainmap.Metadata{},
			true, gainmap.ErrMalformed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok, err := gainmap.ParseXMP([]byte(tc.in))
			if !errors.Is(err, tc.wantErr) || ok != tc.ok {
				t.Fatalf("ok %v err %v, want %v %v", ok, err, tc.ok, tc.wantErr)
			}
			if diff := cmp.Diff(tc.want, got, approx); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
	m, ok, err := gainmap.ParseXMP([]byte(hdrBase))
	if err != nil || !ok || !m.BaseIsHDR() {
		t.Errorf("BaseRenditionIsHDR: %+v %v %v, want an HDR base", m, ok, err)
	}
}

func TestFromUHDR(t *testing.T) {
	t.Parallel()
	one := [3]float64{1, 1, 1}
	got, err := gainmap.FromUHDR(one, [3]float64{8, 8, 8}, one, [3]float64{}, [3]float64{}, 1, 8)
	if err != nil {
		t.Fatal(err)
	}
	want := gainmap.Metadata{Channels: 3, Max: [3]float64{3, 3, 3}, Gamma: one, AlternateHeadroom: 3}
	if diff := cmp.Diff(want, got, approx); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if _, err := gainmap.FromUHDR(one, [3]float64{0, 1, 1}, one, [3]float64{}, [3]float64{}, 1, 8); err == nil {
		t.Error("zero boost accepted")
	}
	inv, scale, off := got.Coefficients(0.5)
	if diff := cmp.Diff([3]float64{1, 1, 1}, inv, approx); diff != "" {
		t.Errorf("inverse gamma: %s", diff)
	}
	if diff := cmp.Diff([3]float64{1.5, 1.5, 1.5}, scale, approx); diff != "" {
		t.Errorf("scale: %s", diff)
	}
	if off != [3]float64{} {
		t.Errorf("offset %v", off)
	}
}

func readFixture(tb testing.TB, name string) []byte {
	tb.Helper()
	b, err := os.ReadFile(golden.Fixture(tb, name))
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

func TestFindJPEG(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture  string
		ok       bool
		channels int
	}{
		// Adobe's layout: MPF, hdrgm XMP in the gain map image (jpegload).
		{"gainmap/libavif-paris_exif_xmp_gainmap_bigendian.jpg", true, 3},
		// libultrahdr's: ISO 21496-1 and XMP in the gain map image.
		{"uhdr/photo-ultrahdr.jpg", true, 1},
		{"jpeg/baseline.jpg", false, 0},
		{"jpeg/metadata-p3.jpg", false, 0},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			src := readFixture(t, tc.fixture)
			g, ok, err := gainmap.FindJPEG(src)
			if err != nil || ok != tc.ok {
				t.Fatalf("FindJPEG: ok %v, %v; want %v", ok, err, tc.ok)
			}
			if !ok {
				return
			}
			if g.Offset <= 0 || g.Offset+g.Length > len(src) || src[g.Offset] != 0xFF || src[g.Offset+1] != 0xD8 {
				t.Errorf("gain map range %d+%d of %d bytes is not a JPEG", g.Offset, g.Length, len(src))
			}
			if g.Meta.Channels != tc.channels || g.Meta.AlternateHeadroom <= 0 || g.Meta.Max[0] <= g.Meta.Min[0] {
				t.Errorf("metadata %+v", g.Meta)
			}
			if math.IsNaN(g.Meta.Gamma[0]) {
				t.Error("NaN gamma")
			}
		})
	}
}

func TestHasTmap(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture string
		want    bool
	}{
		{"gainmap/libavif-color_grid_gainmap_different_grid.avif", true},
		{"gainmap/libavif-color_nogrid_alpha_nogrid_gainmap_grid.avif", true},
		{"gainmap/libavif-unsupported_gainmap_version.avif", true},
		{"avif/photo.avif", false},
		{"heic/photo.heic", false},
		{"avif/sequence.avif", false},
		{"jpeg/baseline.jpg", false},
	} {
		if got := gainmap.HasTmap(readFixture(t, tc.fixture)); got != tc.want {
			t.Errorf("%s: HasTmap = %v, want %v", tc.fixture, got, tc.want)
		}
	}
}

func FuzzParseISO(f *testing.F) {
	f.Add(mustHex(f, seineTmap)[1:])
	f.Add(isoCommon(8, 0, 24, 0, 24, 8, 2, 1))
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := gainmap.ParseISO(b)
		if err == nil {
			if verr := m.Validate(); verr != nil {
				t.Fatalf("accepted invalid metadata: %v", verr)
			}
		}
	})
}

func FuzzParseXMP(f *testing.F) {
	f.Add([]byte(seineXMP))
	f.Fuzz(func(t *testing.T, b []byte) {
		m, ok, err := gainmap.ParseXMP(b)
		if ok && err == nil {
			if verr := m.Validate(); verr != nil {
				t.Fatalf("accepted invalid metadata: %v", verr)
			}
		}
	})
}

func FuzzFindJPEG(f *testing.F) {
	f.Add(readFixture(f, "uhdr/photo-ultrahdr.jpg"))
	f.Add(readFixture(f, "gainmap/libavif-paris_exif_xmp_gainmap_bigendian.jpg"))
	f.Fuzz(func(t *testing.T, b []byte) {
		g, ok, err := gainmap.FindJPEG(b)
		if ok && err == nil {
			if g.Offset < 0 || g.Length < 4 || g.Offset+g.Length > len(b) {
				t.Fatalf("range %d+%d outside %d bytes", g.Offset, g.Length, len(b))
			}
		}
	})
}

func FuzzHasTmap(f *testing.F) {
	f.Add(readFixture(f, "gainmap/libavif-color_grid_gainmap_different_grid.avif"))
	f.Add(readFixture(f, "avif/photo.avif"))
	f.Fuzz(func(_ *testing.T, b []byte) {
		gainmap.HasTmap(b)
	})
}
