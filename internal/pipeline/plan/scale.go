package plan

import (
	"math"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// geo is the size state of one pipeline, imgproxy's processing.Context
// minus the image.
type geo struct {
	srcW, srcH  int // current image, oriented (EXIF + rotate)
	angle       int // EXIF rotation
	flip        bool
	cropW       int
	cropH       int
	cropGravity Gravity

	reqW, reqH int // requested box after relative/aspect/adaptive resolution

	wScale, hScale, dprScale float64
	vectorBaseShrink         float64

	targetW, targetH         int
	scaledW, scaledH         int
	resultCropW, resultCropH int
	exarW, exarH             int
}

// opts is the pipeline's resize options with limits resolved.
type opts struct {
	p          *transform.Pipeline
	rt         transform.ResizeType
	enlarge    bool
	zoomX      float64
	zoomY      float64
	dpr        float64
	vector     bool // vector source with its data still available
	maxResult  int
	rotate     int
	flipX      bool
	flipY      bool
	autoRotate bool
}

func newOpts(p *transform.Pipeline, maxResult int) opts {
	rs := &p.Resize
	o := opts{
		p:          p,
		rt:         rs.Type,
		enlarge:    rs.Enlarge,
		zoomX:      sane(rs.ZoomX),
		zoomY:      sane(rs.ZoomY),
		dpr:        sane(rs.DPR),
		maxResult:  maxResult,
		rotate:     ((p.Rotate%360)/90*90 + 360) % 360,
		flipX:      p.Flip.H,
		flipY:      p.Flip.V,
		autoRotate: p.AutoRotate == nil || *p.AutoRotate,
	}
	if o.rt == transform.ResizeUnset {
		o.rt = transform.ResizeFit
	}
	return o
}

// sane maps unset and nonsensical multipliers to 1.
func sane(f float64) float64 {
	if !(f > 0) || math.IsInf(f, 0) {
		return 1
	}
	return f
}

// dim resolves a requested dimension against the source size.
func dim(d transform.Dim, src int) int {
	switch {
	case d.Px > 0:
		return d.Px
	case d.Rel > 0:
		return max(1, scaleDim(src, d.Rel))
	}
	return 0
}

// calcParams is imgproxy's CalcParams on the current (oriented) size.
func (g *geo) calcParams(o *opts) {
	p := o.p
	g.cropW, g.cropH = 0, 0
	if p.Crop != nil {
		g.cropW = cropSize(g.srcW, p.Crop.Width)
		g.cropH = cropSize(g.srcH, p.Crop.Height)
	}
	wts := minNonZero(g.cropW, g.srcW)
	hts := minNonZero(g.cropH, g.srcH)

	g.resolveBox(o)
	g.calcScale(wts, hts, o)
	g.calcSizes(wts, hts, o)
	g.limitScale(wts, hts, o, o.maxResult, o.maxResult, true)
	if rs := &p.Resize; rs.MaxWidth > 0 || rs.MaxHeight > 0 {
		g.limitScale(wts, hts, o, scaleDim(rs.MaxWidth, o.dpr), scaleDim(rs.MaxHeight, o.dpr), false)
	}
}

// resolveBox turns the requested width and height into pixels: relative
// sizes, aspect ratio and thumbor's adaptive swap are not imgproxy options.
func (g *geo) resolveBox(o *opts) {
	rs := &o.p.Resize
	w, h := dim(rs.Width, g.srcW), dim(rs.Height, g.srcH)
	if ar := rs.AspectRatio; ar > 0 && !math.IsInf(ar, 0) {
		switch {
		case w > 0 && h == 0:
			h = max(1, roundInt(float64(w)/ar))
		case h > 0 && w == 0:
			w = max(1, roundInt(float64(h)*ar))
		}
	}
	if rs.Adaptive && w > 0 && h > 0 && (g.srcW > g.srcH) != (w > h) && w != h {
		w, h = h, w
	}
	g.reqW, g.reqH = w, h
}

// calcScale is imgproxy's calcScale.
func (g *geo) calcScale(width, height int, o *opts) {
	wshrink, hshrink := 1.0, 1.0
	srcW, srcH := float64(width), float64(height)

	dstW := nonZero(float64(g.reqW), srcW)
	dstH := nonZero(float64(g.reqH), srcH)

	if dstW != srcW {
		wshrink = srcW / dstW
	}
	if dstH != srcH {
		hshrink = srcH / dstH
	}

	if wshrink != 1 || hshrink != 1 {
		rt := o.rt
		if rt == transform.ResizeAuto {
			srcD, dstD := srcW-srcH, dstW-dstH
			if (srcD >= 0 && dstD >= 0) || (srcD < 0 && dstD < 0) {
				rt = transform.ResizeFill
			} else {
				rt = transform.ResizeFit
			}
		}
		switch {
		case g.reqW == 0 && rt != transform.ResizeForce:
			wshrink = hshrink
		case g.reqH == 0 && rt != transform.ResizeForce:
			hshrink = wshrink
		case rt == transform.ResizeFit:
			wshrink = math.Max(wshrink, hshrink)
			hshrink = wshrink
		case rt == transform.ResizeFill || rt == transform.ResizeFillDown || rt == transform.ResizeOutside:
			wshrink = math.Min(wshrink, hshrink)
			hshrink = wshrink
		default:
		}
	}

	wshrink /= o.zoomX
	hshrink /= o.zoomY

	g.dprScale = o.dpr

	if !o.enlarge && !o.vector {
		minShrink := math.Min(wshrink, hshrink)
		if minShrink < 1 {
			wshrink /= minShrink
			hshrink /= minShrink
			// The target cannot be reached without enlarging; compensate a
			// DPR below 1 unless extend keeps the composition fixed.
			if !o.p.Extend.Enabled {
				g.dprScale /= minShrink
			}
		}
		g.dprScale = math.Min(g.dprScale, math.Min(wshrink, hshrink))
	}

	if mw := o.p.Resize.MinWidth; mw > 0 {
		if minShrink := srcW / float64(mw); minShrink < wshrink {
			hshrink /= wshrink / minShrink
			wshrink = minShrink
		}
	}
	if mh := o.p.Resize.MinHeight; mh > 0 {
		if minShrink := srcH / float64(mh); minShrink < hshrink {
			wshrink /= hshrink / minShrink
			hshrink = minShrink
		}
	}

	wshrink /= g.dprScale
	hshrink /= g.dprScale

	wshrink = min(wshrink, srcW)
	hshrink = min(hshrink, srcH)

	g.wScale = 1 / wshrink
	g.hScale = 1 / hshrink
}

// calcSizes is imgproxy's calcSizes.
func (g *geo) calcSizes(width, height int, o *opts) {
	g.targetW = scaleDim(g.reqW, g.dprScale*o.zoomX)
	g.targetH = scaleDim(g.reqH, g.dprScale*o.zoomY)

	g.scaledW = scaleDim(width, g.wScale)
	g.scaledH = scaleDim(height, g.hScale)

	switch {
	case o.rt == transform.ResizeOutside:
		// Cover the box without cropping (wsrv/imagekit "outside").
		g.resultCropW, g.resultCropH = 0, 0
	case o.rt == transform.ResizeFillDown && !o.enlarge:
		diffW := float64(g.targetW) / float64(g.scaledW)
		diffH := float64(g.targetH) / float64(g.scaledH)
		switch {
		case diffW > diffH && diffW > 1:
			g.resultCropH = scaleDim(g.scaledW, float64(g.targetH)/float64(g.targetW))
			g.resultCropW = g.scaledW
		case diffH > diffW && diffH > 1:
			g.resultCropW = scaleDim(g.scaledH, float64(g.targetW)/float64(g.targetH))
			g.resultCropH = g.scaledH
		default:
			g.resultCropW, g.resultCropH = g.targetW, g.targetH
		}
	default:
		g.resultCropW, g.resultCropH = g.targetW, g.targetH
	}

	g.exarW, g.exarH = 0, 0
	if o.p.ExtendAR.Enabled && g.targetW > 0 && g.targetH > 0 {
		outW := minNonZero(g.scaledW, g.resultCropW)
		outH := minNonZero(g.scaledH, g.resultCropH)
		diffW := float64(g.targetW) / float64(outW)
		diffH := float64(g.targetH) / float64(outH)
		switch {
		case diffH > diffW:
			g.exarH = scaleDim(outW, float64(g.targetH)/float64(g.targetW))
			g.exarW = outW
		case diffW > diffH:
			g.exarW = scaleDim(outH, float64(g.targetW)/float64(g.targetH))
			g.exarH = outH
		}
	}
}

// limitScale is imgproxy's limitScale generalised to per-axis limits:
// it downscales so the result (with extend and padding when withExtras)
// fits maxW×maxH. A limit ≤ 0 is off.
func (g *geo) limitScale(width, height int, o *opts, maxW, maxH int, withExtras bool) {
	if maxW <= 0 && maxH <= 0 {
		return
	}
	outW := minNonZero(g.scaledW, g.resultCropW)
	outH := minNonZero(g.scaledH, g.resultCropH)
	if withExtras {
		p := o.p
		switch {
		case p.Extend.Enabled:
			outW = max(outW, g.targetW)
			outH = max(outH, g.targetH)
		case p.ExtendAR.Enabled:
			outW = max(outW, g.exarW)
			outH = max(outH, g.exarH)
		}
		if pd := p.Padding; pd != nil {
			outW += scaleDimEven(pd.Left, g.dprScale) + scaleDimEven(pd.Right, g.dprScale)
			outH += scaleDimEven(pd.Top, g.dprScale) + scaleDimEven(pd.Bottom, g.dprScale)
		}
	}

	down := math.Inf(1)
	if maxW > 0 && outW > maxW {
		down = float64(maxW) / float64(outW)
	}
	if maxH > 0 && outH > maxH {
		down = math.Min(down, float64(maxH)/float64(outH))
	}
	if maxW == maxH && !math.IsInf(down, 1) {
		// imgproxy divides by the larger side; keep its exact float.
		down = float64(maxW) / float64(max(outW, outH))
	}
	if math.IsInf(down, 1) {
		return
	}

	g.wScale *= down
	g.hScale *= down
	g.wScale = math.Max(g.wScale, 1/float64(width))
	g.hScale = math.Max(g.hScale, 1/float64(height))
	g.dprScale *= down

	g.calcSizes(width, height, o)
}
