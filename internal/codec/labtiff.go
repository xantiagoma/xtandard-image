package codec

import (
	"context"
	"encoding/binary"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// CIE L*a*b* TIFF (PhotometricInterpretation 8, WP-105).
//
// libvips 8.18's tiffload reads 8-bit CIELab as LABQ-coded pixels that its
// own sanity check rejects ("bad labq format"; fixed after 8.18), and turns
// 16-bit CIELab into LabS relative to D65, while TIFF CIELab is relative to
// D50 unless a WhitePoint tag says otherwise (libtiff's default, after
// Adobe's TIFF technical note). So CIELab pages are read as what they
// physically are: a copy of the file with PhotometricInterpretation
// rewritten to RGB makes libvips load the raw samples (3 bands plus any
// extra samples, 8 or 16 bits), which UnpackLab turns into float L*a*b*
// and LabToSRGB converts to sRGB colorimetrically.

// TIFF tags and values read here.
const (
	tiffTagBitsPerSample   = 258
	tiffTagCompression     = 259
	tiffTagPhotometric     = 262
	tiffTagSamplesPerPixel = 277
	tiffPhotometricRGB     = 2
	tiffPhotometricCIELab  = 8
	// labMaxIFDs bounds the IFD chain walk (pages).
	labMaxIFDs = 1024
)

// LabTIFF describes the CIELab pages of a TIFF (ParseLabTIFF).
type LabTIFF struct {
	// Bits is the sample depth of the first page: 8 or 16.
	Bits int
	// Pages reports, per page (IFD chain order, as libvips numbers them),
	// whether it is CIELab; page 0 always is.
	Pages []bool
	// photometric holds the offsets of the PhotometricInterpretation values
	// to rewrite.
	photometric []int
	order       binary.ByteOrder
}

// Page reports whether page i is CIELab.
func (l *LabTIFF) Page(i int) bool { return i >= 0 && i < len(l.Pages) && l.Pages[i] }

// ParseLabTIFF reports whether b is a classic TIFF whose first page is CIE
// L*a*b* that libvips needs help with: 8 or 16 bits per sample, at least 3
// samples per pixel, not JPEG-compressed. It reads only the IFD chain
// (bounded by labMaxIFDs, loops stop the walk).
func ParseLabTIFF(b []byte) (LabTIFF, bool) {
	if len(b) < 8 {
		return LabTIFF{}, false
	}
	var bo binary.ByteOrder
	switch string(b[:4]) {
	case "II*\x00":
		bo = binary.LittleEndian
	case "MM\x00*":
		bo = binary.BigEndian
	default:
		return LabTIFF{}, false
	}
	l := LabTIFF{order: bo}
	off := int64(bo.Uint32(b[4:]))
	seen := map[int64]bool{}
	for range labMaxIFDs {
		if off == 0 || seen[off] {
			break
		}
		seen[off] = true
		n, ok := ifdCount(b, off, bo)
		if !ok {
			break
		}
		bits, spp, compression, photometric, pos := 0, 1, 1, -1, 0
		for i := range n {
			e := b[off+2+12*int64(i):]
			tag, typ, count := bo.Uint16(e), bo.Uint16(e[2:]), bo.Uint32(e[4:])
			if typ != 3 || count == 0 { // SHORT values only
				continue
			}
			v := int(bo.Uint16(e[8:])) // the first value, inline for count ≤ 2
			switch tag {
			case tiffTagBitsPerSample:
				if count > 2 {
					if o := int64(bo.Uint32(e[8:])); o >= 0 && o+2 <= int64(len(b)) {
						v = int(bo.Uint16(b[o:]))
					} else {
						v = 0
					}
				}
				bits = v
			case tiffTagSamplesPerPixel:
				spp = v
			case tiffTagCompression:
				compression = v
			case tiffTagPhotometric:
				photometric, pos = v, int(off)+2+12*i+8
			}
		}
		lab := photometric == tiffPhotometricCIELab && (bits == 8 || bits == 16) && spp >= 3 &&
			compression != 6 && compression != 7
		if len(l.Pages) == 0 {
			if !lab {
				return LabTIFF{}, false
			}
			l.Bits = bits
		}
		l.Pages = append(l.Pages, lab)
		if lab {
			l.photometric = append(l.photometric, pos)
		}
		off = int64(bo.Uint32(b[off+2+12*int64(n):]))
	}
	return l, len(l.Pages) > 0
}

// Patch returns a C-memory copy of src, the TIFF l was parsed from, with the
// CIELab pages relabelled RGB, for libvips to load their raw samples. The
// caller releases it.
func (l *LabTIFF) Patch(src *vips.Buffer) (*vips.Buffer, error) {
	data := src.Bytes()
	buf, err := vips.NewBuffer(len(data))
	if err != nil {
		return nil, err
	}
	out := buf.Bytes()
	copy(out, data)
	for _, p := range l.photometric {
		l.order.PutUint16(out[p:], tiffPhotometricRGB)
	}
	return buf, nil
}

// UnpackLab turns a page loaded from a Patch copy (raw L, a, b samples as
// uchar or ushort, plus extra bands) into float L*a*b* (L 0-100, a and b
// signed) with the extra bands after it, unchanged. The embedded ICC profile
// stays attached.
func UnpackLab(ctx context.Context, a *vips.Arena, img *vips.Image) (*vips.Image, error) {
	if img.Bands() < 3 {
		return nil, errs.Newf(errs.KindCorrupt, "codec: lab tiff with %d bands", img.Bands())
	}
	var n int
	switch img.BandFormat() {
	case vips.BandFormatUchar:
		n = 256
	case vips.BandFormatUshort:
		n = 65536
	default:
		return nil, errs.New(errs.KindCorrupt, "codec: lab tiff with unexpected samples")
	}
	// One LUT column per channel: L scaled to 0-100, a and b two's
	// complement (× 1/256 for 16 bits).
	lut := make([]float32, 3*n)
	for v := range n {
		lut[3*v] = float32(float64(v) * 100 / float64(n-1))
		var s float64
		if n == 256 {
			s = float64(int8(uint8(v)))
		} else {
			s = float64(int16(uint16(v))) / 256
		}
		lut[3*v+1], lut[3*v+2] = float32(s), float32(s)
	}
	lutImg, err := a.Add(vips.NewFromFloats(ctx, lut, n, 1, 3))
	if err != nil {
		return nil, err
	}
	colour := img
	var extra *vips.Image
	if img.Bands() > 3 {
		if colour, err = a.Add(img.ExtractBand(ctx, 0, 3)); err != nil {
			return nil, err
		}
		if extra, err = a.Add(img.ExtractBand(ctx, 3, img.Bands()-3)); err != nil {
			return nil, err
		}
	}
	if colour, err = a.Add(colour.MapLUT(ctx, lutImg)); err != nil {
		return nil, err
	}
	if extra != nil {
		if colour, err = a.Add(vips.Bandjoin(ctx, []*vips.Image{colour, extra})); err != nil {
			return nil, err
		}
	}
	return a.Add(colour.Copy(ctx, vips.CopyOptions{Interpretation: vips.InterpretationLab}))
}

// LabToSRGB converts float L*a*b* (UnpackLab) with optional extra bands to
// 8-bit sRGB, the first extra band as alpha (8-bit alpha kept, 16-bit
// scaled). An embedded ICC profile whose data colour space is L*a*b* is
// honoured; otherwise the values are relative to D50 (TIFF's CIELab
// default). Either way lcms converts them to sRGB (relative
// colorimetric). The embedded profile is dropped from the result, whose pixels
// are plain sRGB. alpha16 says the extra bands held 16-bit samples.
func LabToSRGB(ctx context.Context, a *vips.Arena, img *vips.Image, alpha16 bool) (*vips.Image, error) {
	colour := img
	var alpha *vips.Image
	var err error
	if img.Bands() > 3 {
		if colour, err = a.Add(img.ExtractBand(ctx, 0, 3)); err != nil {
			return nil, err
		}
		if alpha, err = a.Add(img.ExtractBand(ctx, 3, 1)); err != nil {
			return nil, err
		}
		if alpha16 {
			if alpha, err = a.Add(alpha.Linear(ctx, []float64{1.0 / 257}, []float64{0})); err != nil {
				return nil, err
			}
		}
		if alpha, err = a.Add(alpha.CastRound(ctx, vips.BandFormatUchar)); err != nil {
			return nil, err
		}
	}
	if colour, err = a.Add(colour.Copy(ctx, vips.CopyOptions{Interpretation: vips.InterpretationLab})); err != nil {
		return nil, err
	}
	icc, _ := img.Blob(vips.FieldICC)
	if colour, err = labToSRGB8(ctx, a, colour, icc); err != nil {
		return nil, err
	}
	if colour.BandFormat() != vips.BandFormatUchar {
		if colour, err = a.Add(colour.CastRound(ctx, vips.BandFormatUchar)); err != nil {
			return nil, err
		}
	}
	if alpha != nil {
		if colour, err = a.Add(vips.Bandjoin(ctx, []*vips.Image{colour, alpha})); err != nil {
			return nil, err
		}
	}
	if _, ok := colour.Blob(vips.FieldICC); ok {
		return a.Add(colour.RemoveMetadata(ctx, vips.FieldICC))
	}
	return colour, nil
}

// labToSRGB8 converts float L*a*b* to 8-bit sRGB with lcms
// (vips_icc_transform to libvips' built-in sRGB profile, relative
// colorimetric): the input profile is the embedded one when it is an
// L*a*b* profile lcms accepts, else labD50Profile, TIFF's D50 default.
// libvips refuses to import L*a*b* to its own L*a*b* (both are PCS), hence
// the transform straight to a device profile.
func labToSRGB8(ctx context.Context, a *vips.Arena, colour *vips.Image, icc []byte) (*vips.Image, error) {
	if len(icc) >= 128 && string(icc[16:20]) == "Lab " {
		out, err := a.Add(colour.ICCTransform(ctx, "srgb", vips.IntentRelative, true, 8))
		if err == nil {
			return out, nil
		}
		if k := errs.KindOf(err); k == errs.KindCanceled || k == errs.KindTimeout {
			return nil, err
		}
		// An unusable profile: fall back to TIFF's default.
	}
	withD50, err := a.Add(colour.SetBlob(ctx, vips.FieldICC, labD50Profile()))
	if err != nil {
		return nil, err
	}
	return a.Add(withD50.ICCTransform(ctx, "srgb", vips.IntentRelative, true, 8))
}

// LoadLabTIFF loads a page of a CIELab TIFF (ParseLabTIFF) as 8-bit sRGB:
// the page is read from a Patch copy, unpacked and converted when it is
// CIELab (LabToSRGB); the pages of other types are returned as loaded.
// opts.Page selects the page; NPages is forced to 1.
func LoadLabTIFF(ctx context.Context, a *vips.Arena, l *LabTIFF, src *vips.Buffer, opts *vips.LoadOptions) (*vips.Image, error) {
	buf, err := l.Patch(src)
	if err != nil {
		return nil, err
	}
	defer buf.Release() // the image keeps its own reference
	o := *opts
	o.NPages = 1
	img, err := a.Add(vips.Load(ctx, buf, &o))
	if err != nil || !l.Page(o.Page) {
		return img, err
	}
	sixteen := img.BandFormat() == vips.BandFormatUshort
	if img, err = UnpackLab(ctx, a, img); err != nil {
		return nil, err
	}
	return LabToSRGB(ctx, a, img, sixteen)
}
