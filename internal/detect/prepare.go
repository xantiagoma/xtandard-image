package detect

import (
	"context"
	"fmt"
	"image"
	"unsafe"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// input is one model input: a float32 NCHW BGR tensor of tw×th in C memory
// (a vips.Buffer, so ONNX Runtime never points into Go memory), holding
// the source resized to resW×resH at the top-left and padding elsewhere
// (the YOLOX letterbox; OpenCV pads YuNet the same way).
type input struct {
	buf        *vips.Buffer
	tw, th     int // tensor width and height
	resW, resH int // the resized image inside the tensor
	srcW, srcH int // the detection space: the image's first page
}

// prepare resizes img to fit the model input and packs its pixels.
func (d *Detector) prepare(ctx context.Context, img *vips.Image, m *model) (*input, error) {
	var a vips.Arena
	defer a.Close()
	cur := img
	srcW, srcH := img.Width(), img.Height()
	if ph := img.PageHeight(); ph > 0 && ph < srcH {
		// Animated and multi-page images: detect on the first frame.
		var err error
		if cur, err = a.Add(img.ExtractArea(ctx, image.Rect(0, 0, srcW, ph))); err != nil {
			return nil, err
		}
		srcH = ph
	}
	if srcW <= 0 || srcH <= 0 {
		return nil, errs.Newf(errs.KindInternal, "detect: image is %dx%d", srcW, srcH)
	}
	g := fitInput(srcW, srcH, m.width, m.height, d.maxInput)
	if g.resW != srcW || g.resH != srcH {
		var err error
		cur, err = a.Add(cur.Resize(ctx, float64(g.resW)/float64(srcW), float64(g.resH)/float64(srcH), vips.KernelLinear))
		if err != nil {
			return nil, err
		}
	}
	// libvips rounds the output size itself; never exceed the tensor.
	g.resW, g.resH = min(cur.Width(), g.tw), min(cur.Height(), g.th)
	pix, err := cur.ToRGB8(ctx)
	if err != nil {
		return nil, err
	}
	defer pix.Release()

	buf, err := vips.NewBuffer(3 * g.tw * g.th * 4)
	if err != nil {
		return nil, err
	}
	dst, err := float32View(buf.Bytes())
	if err != nil {
		buf.Release()
		return nil, err
	}
	if err := packBGR(dst, pix.Bytes(), cur.Width(), g.resW, g.resH, g.tw, g.th, m.pad); err != nil {
		buf.Release()
		return nil, errs.Wrap(errs.KindInternal, err)
	}
	return &input{buf: buf, tw: g.tw, th: g.th, resW: g.resW, resH: g.resH, srcW: srcW, srcH: srcH}, nil
}

// geometry is the letterbox of a source in a model input.
type geometry struct {
	tw, th     int // tensor size
	resW, resH int // resized source
}

// fitInput computes the letterbox. A fixed-size model (width, height > 0)
// gets the source scaled to fit inside it, up or down, as YOLOX's preproc
// does (r = min(W/w, H/h), sizes truncated). A dynamic model gets the
// source scaled down (never up) so its longest side is at most maxInput,
// and the tensor padded up to a multiple of 32.
func fitInput(srcW, srcH, width, height, maxInput int) geometry {
	var scale float64
	if width > 0 && height > 0 {
		scale = min(float64(width)/float64(srcW), float64(height)/float64(srcH))
	} else {
		scale = min(1, float64(maxInput)/float64(max(srcW, srcH)))
	}
	g := geometry{
		tw: width, th: height,
		resW: max(int(float64(srcW)*scale), 1),
		resH: max(int(float64(srcH)*scale), 1),
	}
	if width <= 0 || height <= 0 {
		g.tw, g.th = roundUp(g.resW, maxStride), roundUp(g.resH, maxStride)
	}
	return g
}

func roundUp(v, m int) int { return (v + m - 1) / m * m }

// float32View reinterprets C memory as float32s. The buffer comes from
// vips_tracked_malloc, which aligns far beyond 4 bytes; the check guards
// the assumption.
func float32View(b []byte) ([]float32, error) {
	p := unsafe.SliceData(b)
	if len(b)%4 != 0 || uintptr(unsafe.Pointer(p))%unsafe.Alignof(float32(0)) != 0 {
		return nil, errs.Newf(errs.KindInternal, "detect: tensor buffer of %d bytes is not float32-aligned", len(b))
	}
	return unsafe.Slice((*float32)(unsafe.Pointer(p)), len(b)/4), nil
}

// packBGR writes the w×h top-left region of pix (8-bit RGB, rows of
// stride pixels) into dst as a planar B, G, R float32 tensor of tw×th with
// raw 0–255 values, and fills the rest with pad. Neither model normalises
// its input (YOLOX demo preproc; OpenCV blobFromImage with scale 1, no
// mean, swapRB false).
func packBGR(dst []float32, pix []byte, stride, w, h, tw, th int, pad float32) error {
	plane := tw * th
	switch {
	case w <= 0 || h <= 0 || w > tw || h > th || w > stride:
		return fmt.Errorf("detect: cannot pack %dx%d (stride %d) into %dx%d", w, h, stride, tw, th)
	case len(dst) != 3*plane:
		return fmt.Errorf("detect: tensor has %d values, want %d", len(dst), 3*plane)
	case len(pix) < 3*(stride*(h-1)+w):
		return fmt.Errorf("detect: %d pixel bytes for %dx%d (stride %d)", len(pix), w, h, stride)
	}
	for i := range dst {
		dst[i] = pad
	}
	bPlane, gPlane, rPlane := dst[:plane], dst[plane:2*plane], dst[2*plane:]
	for y := range h {
		row := pix[3*stride*y : 3*(stride*y+w)]
		o := y * tw
		for x := range w {
			bPlane[o+x] = float32(row[3*x+2])
			gPlane[o+x] = float32(row[3*x+1])
			rPlane[o+x] = float32(row[3*x])
		}
	}
	return nil
}
