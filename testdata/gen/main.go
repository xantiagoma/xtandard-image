// Command gen writes the procedurally generated source images of the fixture
// corpus. It is driven by testdata/gen/gen.sh; see testdata/README.md.
//
// Every image is deterministic: the same subcommand always writes the same
// pixels, so the corpus can be regenerated and audited. Nothing here is copied
// from a third-party image, which is why all fixtures are CC0 (testdata/LICENSES.md).
//
// Usage:
//
//	go run ./testdata/gen <subcommand> <args...>
//
// This directory is under testdata/, so `go build ./...`, `go test ./...` and
// the linters never see it.
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: gen <photo|photo-alpha|photo16|graphic|palette|watermark|watermark-badge|gif|gif-anim|ico-png|ico-bmp|icns|eps-preview|exif6|metadata|bordered|ssim-refs|pdf|psd|psd-nocomposite|psb|exr-rgba-half|exr-rgb-float|radiance|rgbe|pfm|ppm|hdr-stats|dng|video-frames|fits|procreate|sketch|pdn|svgz|dds-bc1|dds-bc4|dds-bc7|dds-bc6h|ktx2-bc1|ktx2-bc4|ktx2-bc7|ktx2-bc6h|ktx2-rgba8|ktx2-rgb8|ktx2-rgba8-zlib|ktx2-rgba8-level|ktx2-rgba8-zstd|lab-tiff8|lab-tiff16|lab-tiff8-alpha|lab-tiff8-icc|cmyk-raw|mng> args...")
	}
	sizeArgs := func() (int, int, string, error) {
		if len(args) != 4 {
			return 0, 0, "", fmt.Errorf("%s: want W H out", args[0])
		}
		w, err1 := strconv.Atoi(args[1])
		h, err2 := strconv.Atoi(args[2])
		return w, h, args[3], errors.Join(err1, err2)
	}
	switch args[0] {
	case "photo", "photo-alpha", "photo16", "graphic", "palette":
		w, h, out, err := sizeArgs()
		if err != nil {
			return err
		}
		var img image.Image
		switch args[0] {
		case "photo":
			img = photo(w, h)
		case "photo-alpha":
			img = withAlpha(photo(w, h))
		case "photo16":
			img = photo16(w, h)
		case "graphic":
			img = graphic(w, h)
		default:
			img = paletted(graphic(w, h))
		}
		return writePNG(out, img)
	case "watermark":
		return writePNG(args[1], watermark())
	case "watermark-badge":
		return writePNG(args[1], badge())
	case "gif":
		return writeGIF(args[1], false)
	case "gif-anim":
		return writeGIF(args[1], true)
	case "ico-png":
		return writeICO(args[1], true)
	case "ico-bmp":
		return writeICO(args[1], false)
	case "icns":
		return writeICNS(args[1])
	case "eps-preview":
		return writeEPSPreview(args[1])
	case "exif6":
		if len(args) != 3 {
			return errors.New("exif6: want in.jpg out.jpg")
		}
		return injectOrientation(args[1], args[2], 6)
	case "metadata":
		if len(args) != 3 {
			return errors.New("metadata: want in.jpg out.jpg")
		}
		return injectMetadata(args[1], args[2])
	case "bordered":
		if len(args) != 8 {
			return errors.New("bordered: want W H top right bottom left out")
		}
		var v [6]int
		for i := range v {
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				return err
			}
			v[i] = n
		}
		return writePNG(args[7], bordered(v[0], v[1], v[2], v[3], v[4], v[5]))
	case "ssim-refs":
		return ssimRefs(args[1])
	case "pdf":
		return writePDF(args[1])
	case "psd":
		return writePSD(args[1], false, true)
	case "psd-nocomposite":
		return writePSD(args[1], false, false)
	case "psb":
		return writePSD(args[1], true, true)
	case "dng":
		if len(args) != 7 && len(args) != 8 {
			return errors.New("dng: want W H previewW previewH orientation out.dng [compression]")
		}
		v := [6]int{5: 1} // compression 1: uncompressed
		for i := range len(args) - 2 {
			a := args[i+1]
			if i == 5 {
				a = args[7]
			}
			n, err := strconv.Atoi(a)
			if err != nil {
				return err
			}
			v[i] = n
		}
		return writeDNG(args[6], v[0], v[1], v[2], v[3], uint16(v[4]), uint16(v[5]))
	case "cmyk-raw":
		if len(args) != 6 || (args[3] != "alpha" && args[3] != "opaque") {
			return errors.New("cmyk-raw: want W H alpha|opaque out.cmyk out.icc")
		}
		w, err1 := strconv.Atoi(args[1])
		h, err2 := strconv.Atoi(args[2])
		if err := errors.Join(err1, err2); err != nil {
			return err
		}
		return writeCMYKRaw(w, h, args[3] == "alpha", args[4], args[5])
	case "ktx2-bc1", "ktx2-bc4", "ktx2-bc7", "ktx2-bc6h", "ktx2-rgba8", "ktx2-rgb8", "ktx2-rgba8-zlib",
		"ktx2-rgba8-level", "ktx2-rgba8-zstd":
		in := ""
		if args[0] == "ktx2-rgba8-zstd" && len(args) == 5 {
			in, args = args[4], args[:4]
		}
		w, h, out, err := sizeArgs()
		if err != nil {
			return err
		}
		return ktx2Fixture(args[0], w, h, out, in)
	case "dds-bc1", "dds-bc4", "dds-bc7", "dds-bc6h", "lab-tiff8", "lab-tiff16", "lab-tiff8-alpha", "lab-tiff8-icc":
		w, h, out, err := sizeArgs()
		if err != nil {
			return err
		}
		if args[0][:4] == "dds-" {
			return ddsFixture(args[0], w, h, out)
		}
		return labTIFFFixture(args[0], w, h, out)
	case "exr-rgba-half", "exr-rgb-float", "radiance", "rgbe", "pfm", "ppm":
		w, h, out, err := sizeArgs()
		if err != nil {
			return err
		}
		switch args[0] {
		case "exr-rgba-half":
			return writeEXR(out, w, h, true, false)
		case "exr-rgb-float":
			return writeEXR(out, w, h, false, true)
		case "radiance":
			return writeRadiance(out, "#?RADIANCE", w, h)
		case "rgbe":
			return writeRadiance(out, "#?RGBE", w, h)
		case "pfm":
			return writePFM(out, w, h)
		default:
			return writePPM(out, w, h)
		}
	case "fits":
		w, h, out, err := sizeArgs()
		if err != nil {
			return err
		}
		return writeFITSFixtures(out, w, h)
	case "procreate":
		return writeProcreate(args[1])
	case "sketch":
		return writeSketch(args[1])
	case "pdn":
		return writePDN(args[1])
	case "mng":
		if len(args) != 3 {
			return errors.New("mng: want KIND out")
		}
		return writeMNG(args[1], args[2])
	case "svgz":
		if len(args) != 3 {
			return errors.New("svgz: want in.svg out.svgz")
		}
		return writeSVGZ(args[1], args[2])
	case "video-frames":
		if len(args) != 6 {
			return errors.New("video-frames: want W H FPS FRAMES out.rgb")
		}
		var v [4]int
		for i := range v {
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				return err
			}
			v[i] = n
		}
		return videoFrames(args[5], v[0], v[1], v[2], v[3])
	case "hdr-stats":
		w, h, _, err := sizeArgs()
		if err != nil {
			return err
		}
		hdrStats(w, h)
		return nil
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

// ---- noise ------------------------------------------------------------------

// hash2 is a small integer hash for lattice value noise.
func hash2(x, y, seed int) float64 {
	h := uint32(x)*374761393 + uint32(y)*668265263 + uint32(seed)*2246822519
	h = (h ^ (h >> 13)) * 1274126177
	h ^= h >> 16
	return float64(h&0xffffff) / float64(0xffffff)
}

func smooth(t float64) float64 { return t * t * (3 - 2*t) }

func valueNoise(x, y float64, seed int) float64 {
	x0, y0 := math.Floor(x), math.Floor(y)
	ix, iy := int(x0), int(y0)
	fx, fy := smooth(x-x0), smooth(y-y0)
	a := hash2(ix, iy, seed)
	b := hash2(ix+1, iy, seed)
	c := hash2(ix, iy+1, seed)
	d := hash2(ix+1, iy+1, seed)
	return (a*(1-fx)+b*fx)*(1-fy) + (c*(1-fx)+d*fx)*fy
}

// fbm is fractal Brownian motion: octaves of value noise, result in [0, 1].
func fbm(x, y float64, octaves, seed int) float64 {
	var sum, amp, norm float64 = 0, 1, 0
	for o := range octaves {
		sum += amp * valueNoise(x, y, seed+o*101)
		norm += amp
		amp *= 0.5
		x *= 2
		y *= 2
	}
	return sum / norm
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

// ---- images -----------------------------------------------------------------

// scene returns linear-ish [0,1] RGB for a landscape: sky with clouds and a
// sun, a fractal mountain ridge, a lake with reflections, and a textured shore.
func scene(u, v float64) (r, g, b float64) {
	ridge := 0.42 + 0.18*(fbm(u*3, 0.5, 6, 7)-0.5)*2
	lake := 0.72
	switch {
	case v < ridge: // sky
		t := v / ridge
		r, g, b = lerp(0.25, 0.85, t), lerp(0.45, 0.88, t), lerp(0.85, 0.95, t)
		c := clamp01((fbm(u*4, v*8, 5, 11) - 0.45) * 2.5)
		r, g, b = lerp(r, 0.97, c), lerp(g, 0.97, c), lerp(b, 0.98, c)
		d := math.Hypot(u-0.78, (v-0.18)*0.75)
		glow := clamp01(1 - d/0.25)
		sun := 0.0
		if d < 0.05 {
			sun = 1
		}
		r, g, b = clamp01(r+0.5*glow*glow+sun), clamp01(g+0.4*glow*glow+sun), clamp01(b+0.1*glow*glow+sun*0.9)
	case v < lake: // mountains
		t := (v - ridge) / (lake - ridge)
		rock := fbm(u*16, v*16, 6, 23)
		shade := 0.55 + 0.6*(fbm(u*6+v*3, v*6, 4, 31)-0.5)
		r = clamp01(lerp(0.45, 0.2, t) * shade * (0.7 + 0.6*rock))
		g = clamp01(lerp(0.42, 0.35, t) * shade * (0.7 + 0.6*rock))
		b = clamp01(lerp(0.40, 0.18, t) * shade * (0.7 + 0.6*rock))
		if v-ridge < 0.03 && rock > 0.5 { // snow on the ridge
			r, g, b = 0.95, 0.95, 0.97
		}
	default: // lake reflecting the sky, ripples, shore
		mv := 2*lake - v
		if mv < 0 {
			mv = 0
		}
		rr, rg, rb := scene(u+0.004*math.Sin(v*300), mv)
		ripple := 0.85 + 0.15*fbm(u*40, v*6, 3, 41)
		t := clamp01((v - lake) / (1 - lake))
		r, g, b = rr*0.6*ripple, rg*0.65*ripple, rb*0.75*ripple
		if v > 0.9 { // pebbly shore
			p := fbm(u*60, v*60, 4, 53)
			r, g, b = lerp(r, 0.55*p+0.2, t), lerp(g, 0.45*p+0.2, t), lerp(b, 0.35*p+0.15, t)
		}
	}
	return r, g, b
}

// photo renders the scene with sensor-like noise and a caption, so it has the
// statistics of a real photograph: smooth gradients, fine texture and sharp
// edges.
func photo(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewPCG(1, 2))
	for y := range h {
		for x := range w {
			r, g, b := scene(float64(x)/float64(w), float64(y)/float64(h))
			n := rng.NormFloat64() * 2.5
			img.SetRGBA(x, y, color.RGBA{q8(r*255 + n), q8(g*255 + n), q8(b*255 + n), 255})
		}
	}
	d := font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(color.RGBA{250, 250, 245, 255}),
		Face: basicfont.Face7x13,
		Dot:  fixed.P(6, h-6),
	}
	d.DrawString("xtandard-image")
	return img
}

func q8(v float64) uint8 { return uint8(math.Round(math.Max(0, math.Min(255, v)))) }

// withAlpha fades the photo out towards the corners (8-bit straight alpha).
func withAlpha(src *image.RGBA) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(b)
	for y := range b.Dy() {
		for x := range b.Dx() {
			c := src.RGBAAt(x, y)
			d := math.Hypot(float64(x)/float64(b.Dx())-0.5, float64(y)/float64(b.Dy())-0.5)
			c.A = q8(255 * clamp01(1.4-2.2*d))
			dst.SetNRGBA(x, y, color.NRGBA{c.R, c.G, c.B, c.A})
		}
	}
	return dst
}

// photo16 is the noiseless scene at 16 bits per channel (no 8-bit quantisation).
func photo16(w, h int) *image.RGBA64 {
	img := image.NewRGBA64(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			r, g, b := scene(float64(x)/float64(w), float64(y)/float64(h))
			img.SetRGBA64(x, y, color.RGBA64{uint16(r * 65535), uint16(g * 65535), uint16(b * 65535), 65535})
		}
	}
	return img
}

// graphic is a flat-colour logo-like image (few colours, hard edges).
func graphic(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{245, 243, 236, 255}), image.Point{}, draw.Src)
	cols := []color.RGBA{{230, 57, 70, 255}, {29, 53, 87, 255}, {69, 123, 157, 255}, {241, 196, 15, 255}}
	fw, fh := float64(w), float64(h)
	for y := range h {
		for x := range w {
			u, v := float64(x)/fw, float64(y)/fh
			switch {
			case math.Hypot(u-0.32, v-0.45) < 0.25:
				img.SetRGBA(x, y, cols[0])
			case u > 0.55 && u < 0.9 && v > 0.2 && v < 0.75:
				img.SetRGBA(x, y, cols[1])
			case v > 0.82 && int(u*8)%2 == 0:
				img.SetRGBA(x, y, cols[2])
			case math.Abs(u-v) < 0.03:
				img.SetRGBA(x, y, cols[3])
			}
		}
	}
	return img
}

// watermark is a 48x32 RGBA logo for the watermark cases: a translucent
// orange plate with a white corner triangle top-left and a navy bar at the
// bottom (asymmetric on both axes, so a flip or a misplacement shows), inside
// a half-transparent white rim.
func watermark() *image.NRGBA {
	const w, h = 48, 32
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			var c color.NRGBA
			switch {
			case x == 0 || y == 0 || x == w-1 || y == h-1:
				c = color.NRGBA{R: 255, G: 255, B: 255, A: 128}
			case x < 2 || y < 2 || x >= w-2 || y >= h-2:
				// transparent gap between the rim and the plate
			case y >= h-8:
				c = color.NRGBA{R: 20, G: 40, B: 120, A: 255}
			case (x-2)+(y-2) < 14:
				c = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
			default:
				c = color.NRGBA{R: 230, G: 90, B: 20, A: 220}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

// badge is a 40x40 RGBA per-request watermark (imgproxy watermark_url in
// tests), unlike the configured logo: an opaque teal disc with a
// transparent surround and a white "L" whose foot points right, so a
// rotation or a mirror shows.
func badge() *image.NRGBA {
	const n = 40
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	for y := range n {
		for x := range n {
			dx, dy := float64(x)-19.5, float64(y)-19.5
			d := math.Sqrt(dx*dx + dy*dy)
			if d > 18 {
				continue
			}
			c := color.NRGBA{R: 0, G: 140, B: 150, A: 255}
			if d > 17 {
				c.A = uint8(255 * (18 - d))
			}
			if (x >= 12 && x < 18 && y >= 9 && y < 31) || (x >= 12 && x < 29 && y >= 25 && y < 31) {
				c = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func paletted(src image.Image) *image.Paletted {
	dst := image.NewPaletted(src.Bounds(), palette.Plan9)
	draw.Draw(dst, dst.Bounds(), src, image.Point{}, draw.Src)
	return dst
}

// ---- encoders ---------------------------------------------------------------

func writePNG(path string, img image.Image) error {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return err
	}
	return write(path, buf.Bytes())
}

func write(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func writeGIF(path string, animated bool) error {
	const w, h = 96, 72
	if !animated {
		var buf bytes.Buffer
		if err := gif.Encode(&buf, paletted(graphic(w, h)), nil); err != nil {
			return err
		}
		return write(path, buf.Bytes())
	}
	anim := &gif.GIF{LoopCount: 0}
	for f := range 6 {
		frame := paletted(graphic(w, h))
		// A ball moving across the frame.
		cx := 10 + f*15
		for y := range h {
			for x := range w {
				if (x-cx)*(x-cx)+(y-20)*(y-20) < 64 {
					frame.Set(x, y, color.RGBA{0, 160, 80, 255})
				}
			}
		}
		anim.Image = append(anim.Image, frame)
		anim.Delay = append(anim.Delay, 10)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, anim); err != nil {
		return err
	}
	return write(path, buf.Bytes())
}

// writeICO writes a single-entry ICO: a 64x64 PNG entry, or a 32x32 32-bit
// BMP (DIB) entry with its AND mask, per the ICO layout documented at
// https://learn.microsoft.com/en-us/previous-versions/ms997538(v=msdn.10).
func writeICO(path string, pngEntry bool) error {
	size := 32
	if pngEntry {
		size = 64
	}
	src := withAlpha(photo(size, size))
	var data []byte
	if pngEntry {
		var buf bytes.Buffer
		if err := png.Encode(&buf, src); err != nil {
			return err
		}
		data = buf.Bytes()
	} else {
		data = dib(src)
	}
	var out bytes.Buffer
	le := binary.LittleEndian
	hdr := make([]byte, 6+16)
	le.PutUint16(hdr[2:], 1) // type: icon
	le.PutUint16(hdr[4:], 1) // one entry
	e := hdr[6:]
	e[0], e[1] = byte(size), byte(size)
	le.PutUint16(e[4:], 1)  // planes
	le.PutUint16(e[6:], 32) // bit count
	le.PutUint32(e[8:], uint32(len(data)))
	le.PutUint32(e[12:], uint32(len(hdr)))
	out.Write(hdr)
	out.Write(data)
	return write(path, out.Bytes())
}

// dib encodes a 32-bit bottom-up BITMAPINFOHEADER image with a 1-bit AND mask,
// as stored inside ICO files (height field is doubled).
func dib(src *image.NRGBA) []byte {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	le := binary.LittleEndian
	maskStride := ((w + 31) / 32) * 4
	out := make([]byte, 40, 40+w*h*4+maskStride*h)
	le.PutUint32(out[0:], 40)
	le.PutUint32(out[4:], uint32(w))
	le.PutUint32(out[8:], uint32(2*h))
	le.PutUint16(out[12:], 1)
	le.PutUint16(out[14:], 32)
	le.PutUint32(out[20:], uint32(w*h*4+maskStride*h))
	for y := h - 1; y >= 0; y-- {
		for x := range w {
			c := src.NRGBAAt(x, y)
			out = append(out, c.B, c.G, c.R, c.A)
		}
	}
	for y := h - 1; y >= 0; y-- {
		row := make([]byte, maskStride)
		for x := range w {
			if src.NRGBAAt(x, y).A == 0 {
				row[x/8] |= 0x80 >> (x % 8)
			}
		}
		out = append(out, row...)
	}
	return out
}

// injectOrientation inserts an APP1 Exif segment holding only the Orientation
// tag (0x0112) right after the JFIF APP0 segment (or SOI when there is none).
func injectOrientation(in, out string, orientation uint16) error {
	src, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	if len(src) < 4 || src[0] != 0xFF || src[1] != 0xD8 {
		return errors.New("exif6: not a JPEG")
	}
	// TIFF header (big endian) + IFD0 with one entry + next-IFD offset 0.
	tiff := []byte{'M', 'M', 0, 42, 0, 0, 0, 8, 0, 1, 0x01, 0x12, 0, 3, 0, 0, 0, 1, byte(orientation >> 8), byte(orientation), 0, 0, 0, 0, 0, 0}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	seg = append(seg, payload...)

	pos := 2
	if src[2] == 0xFF && src[3] == 0xE0 {
		pos = 4 + int(binary.BigEndian.Uint16(src[4:6]))
	}
	dst := make([]byte, 0, len(src)+len(seg))
	dst = append(dst, src[:pos]...)
	dst = append(dst, seg...)
	dst = append(dst, src[pos:]...)
	return write(out, dst)
}

// ---- SSIM reference set ---------------------------------------------------------

// ssimRefs writes an 8-bit greyscale reference and distorted versions of it in
// the spirit of Fig. 2 of Wang et al. 2004 (mean shift, contrast stretch,
// impulse noise, Gaussian blur, JPEG). internal/testutil/ssim compares its
// scores on these files with scikit-image's.
func ssimRefs(dir string) error {
	const w, h = 160, 120
	ref := image.NewGray(image.Rect(0, 0, w, h))
	draw.Draw(ref, ref.Bounds(), photo(w, h), image.Point{}, draw.Src)

	mapPix := func(f func(x, y int, v float64) float64) *image.Gray {
		dst := image.NewGray(ref.Bounds())
		for y := range h {
			for x := range w {
				dst.SetGray(x, y, color.Gray{q8(f(x, y, float64(ref.GrayAt(x, y).Y)))})
			}
		}
		return dst
	}
	rng := rand.New(rand.NewPCG(3, 4))
	impulse := mapPix(func(_, _ int, v float64) float64 {
		switch p := rng.Float64(); {
		case p < 0.02:
			return 0
		case p < 0.04:
			return 255
		}
		return v
	})

	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, ref, &jpeg.Options{Quality: 15}); err != nil {
		return err
	}
	jimg, err := jpeg.Decode(&jb)
	if err != nil {
		return err
	}
	jgray := image.NewGray(ref.Bounds())
	draw.Draw(jgray, jgray.Bounds(), jimg, image.Point{}, draw.Src)

	files := map[string]image.Image{
		"ref.png":       ref,
		"meanshift.png": mapPix(func(_, _ int, v float64) float64 { return v + 20 }),
		"contrast.png":  mapPix(func(_, _ int, v float64) float64 { return 128 + (v-128)*1.3 }),
		"impulse.png":   impulse,
		"blur.png":      gaussBlur(ref, 1.5),
		"jpeg.png":      jgray,
	}
	for name, img := range files {
		if err := writePNG(filepath.Join(dir, name), img); err != nil {
			return err
		}
	}
	return nil
}

// gaussBlur is a separable Gaussian blur with clamped edges.
func gaussBlur(src *image.Gray, sigma float64) *image.Gray {
	r := int(math.Ceil(3 * sigma))
	k := make([]float64, 2*r+1)
	var sum float64
	for i := range k {
		d := float64(i - r)
		k[i] = math.Exp(-d * d / (2 * sigma * sigma))
		sum += k[i]
	}
	for i := range k {
		k[i] /= sum
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	tmp := make([]float64, w*h)
	at := func(v, n int) int { return max(0, min(n-1, v)) }
	for y := range h {
		for x := range w {
			var s float64
			for i, kv := range k {
				s += kv * float64(src.GrayAt(at(x+i-r, w), y).Y)
			}
			tmp[y*w+x] = s
		}
	}
	dst := image.NewGray(b)
	for y := range h {
		for x := range w {
			var s float64
			for i, kv := range k {
				s += kv * tmp[at(y+i-r, h)*w+x]
			}
			dst.SetGray(x, y, color.Gray{q8(s)})
		}
	}
	return dst
}
