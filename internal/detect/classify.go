package detect

import (
	"bufio"
	"context"
	_ "embed" // the ImageNet class names
	"errors"
	"fmt"
	"image"
	"io"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/detect/ort"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Image classification (WP-110, the imgproxy info option classify): one
// ImageNet-style classifier, by default timm's MobileNetV3-Large
// (mobilenetv3_large_100, ONNX Model Zoo export, Apache-2.0, 1000 classes).
//
// Preprocessing follows timm's evaluation transform for the model: the
// image (upright, first page) is resized so that its short side is the
// model input divided by the crop fraction 0.875 (224 → 256) with a cubic
// kernel, centre-cropped to the input size, rendered to 8-bit sRGB and
// normalised (ImageNet mean and standard deviation by default) into an
// RGB float32 tensor, NCHW or NHWC as the model declares. The output is
// turned into probabilities with a softmax unless it already is one.
//
// The session is created on the first Classify call, not in New: a
// deployment that never classifies pays neither the memory of the model
// nor its load time. New only resolves (and optionally checksums) the
// model file and reads the class names.

// imagenetLabels holds the 1000 ImageNet-1k class names in class-id
// order, one per line: imagenet-simple-labels (Anish Athalye, public
// domain), unique short names derived from the WordNet synsets (NOTICE).
// It is the package's only variable because go:embed needs one.
//
//go:embed imagenet_labels.txt
var imagenetLabels string

// imagenetClassCount is the number of ImageNet-1k classes.
const imagenetClassCount = 1000

// classifyCropFraction is timm's default crop_pct: the model input is the
// centre 87.5% of the resized image.
const classifyCropFraction = 0.875

// defaultClassifySize is the input side of a classifier whose input size is
// dynamic.
const defaultClassifySize = 224

// modelClassify is the model label of the classifier (metrics, logs).
const modelClassify = "classify"

// Normalization is how 8-bit channel values v are scaled into the
// classifier's input (imgproxy's IMGPROXY_CLASSIFICATION_NORMALIZATION
// names).
type Normalization int

// Normalizations.
const (
	// NormalizeImageNet is (v/255 − mean) / std with ImageNet's per-channel
	// mean and standard deviation (timm, torchvision).
	NormalizeImageNet Normalization = iota
	// NormalizeFull maps to [-1, 1]: v/127.5 − 1 (TensorFlow Inception
	// style, e.g. EfficientNet-Lite).
	NormalizeFull
	// NormalizeHalf maps to [0, 1]: v/255.
	NormalizeHalf
	// NormalizeNone feeds the raw 0–255 values.
	NormalizeNone
)

// ParseNormalization parses imagenet, full, half or none.
func ParseNormalization(s string) (Normalization, bool) {
	switch s {
	case "imagenet":
		return NormalizeImageNet, true
	case "full":
		return NormalizeFull, true
	case "half":
		return NormalizeHalf, true
	case "none":
		return NormalizeNone, true
	default:
		return 0, false
	}
}

// Classification is a classifier's output for one image: a probability
// per class, in class-id order, with the class names.
type Classification struct {
	// Labels are the class names, shared by every result of the
	// classifier: read-only.
	Labels []string
	// Scores are the class probabilities (they sum to 1 for a softmax
	// classifier), len(Labels) long.
	Scores []float32
}

// classifier is the lazily loaded classification model.
type classifier struct {
	info   ModelInfo
	labels []string // nil: ImageNet's, when the model has 1000 outputs
	norm   Normalization

	mu     sync.Mutex
	done   bool // load ran (m or err is set)
	m      *classModel
	err    error
	closed bool
}

// classModel is the loaded session and the facts its preprocessing needs.
type classModel struct {
	session       *ort.Session
	width, height int
	nhwc          bool
	labels        []string
}

// HasClassifier reports whether Classify can run (the model file exists;
// the session itself is created on the first call).
func (d *Detector) HasClassifier() bool { return d != nil && d.classifier != nil }

// Classify runs the classifier on img (its first page, upright as given).
// It must run inside vips.Run. The first call creates the session; a model
// that cannot be loaded or does not look like a classifier fails every
// call with an Internal error (logged once).
func (d *Detector) Classify(ctx context.Context, img *vips.Image) (_ *Classification, err error) {
	if !d.HasClassifier() {
		return nil, fmt.Errorf("%w: no classification model", ErrUnavailable)
	}
	if img == nil {
		return nil, errs.New(errs.KindInternal, "detect: nil image")
	}
	m, err := d.loadClassifier(ctx)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		if d.opts.Observer != nil && err == nil {
			d.opts.Observer.ObserveDetect(modelClassify, elapsed)
		}
		if d.opts.Logger != nil {
			d.opts.Logger.LogAttrs(ctx, slog.LevelDebug, "classification finished",
				slog.String("model", modelClassify), slog.Duration("duration", elapsed), slog.Bool("ok", err == nil))
		}
	}()
	buf, err := d.prepareClassify(ctx, img, m)
	if err != nil {
		return nil, err
	}
	defer buf.Release()

	shape := []int64{1, 3, int64(m.height), int64(m.width)}
	if m.nhwc {
		shape = []int64{1, int64(m.height), int64(m.width), 3}
	}
	if err := d.sem.Acquire(ctx, 1); err != nil {
		return nil, ctxError(ctx, err)
	}
	outs, err := m.session.Run(ctx, buf.Bytes(), shape)
	d.sem.Release(1)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctxError(ctx, err)
		}
		return nil, errs.Wrapf(errs.KindInternal, err, "detect: run %s model", modelClassify)
	}
	defer func() {
		for i := range outs {
			outs[i].Release()
		}
	}()
	if len(outs) == 0 || len(outs[0].Data) != len(m.labels) {
		got := 0
		if len(outs) > 0 {
			got = len(outs[0].Data)
		}
		return nil, errs.Newf(errs.KindInternal, "detect: classifier returned %d scores for %d classes", got, len(m.labels))
	}
	return &Classification{Labels: m.labels, Scores: probabilities(outs[0].Data)}, nil
}

// loadClassifier creates the session on first use.
func (d *Detector) loadClassifier(ctx context.Context) (*classModel, error) {
	c := d.classifier
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errs.New(errs.KindInternal, "detect: classifier closed")
	}
	if !c.done {
		c.done = true
		start := time.Now()
		c.m, c.err = newClassModel(d.rt, c, d.opts.Threads)
		if d.opts.Logger != nil {
			if c.err != nil {
				d.opts.Logger.LogAttrs(ctx, slog.LevelError, "classification model failed to load",
					slog.String("path", c.info.Path), slog.String("error", c.err.Error()))
			} else {
				d.opts.Logger.LogAttrs(ctx, slog.LevelInfo, "classification model loaded",
					slog.String("path", c.info.Path), slog.String("input", c.info.Input),
					slog.Int("classes", len(c.m.labels)), slog.Duration("duration", time.Since(start)))
			}
		}
	}
	return c.m, c.err
}

// newClassModel creates the session and checks that the model is a
// classifier this package can feed: one float32 image input (NCHW or NHWC
// with 3 channels) and a first output with one score per class.
func newClassModel(rt *ort.Runtime, c *classifier, threads int) (*classModel, error) {
	s, err := rt.NewSession(c.info.Path, ort.SessionOptions{IntraOpThreads: threads})
	if err != nil {
		return nil, errs.Wrapf(errs.KindInternal, err, "detect: load %s model %s", modelClassify, c.info.Path)
	}
	m := &classModel{session: s}
	if err := m.check(c.labels); err != nil {
		s.Close()
		return nil, errs.Wrapf(errs.KindInternal, err, "detect: %s model %s", modelClassify, c.info.Path)
	}
	c.info.Input = strconv.Itoa(m.width) + "x" + strconv.Itoa(m.height)
	return m, nil
}

// check reads the input layout and size and matches the output to the
// class names.
func (m *classModel) check(labels []string) error {
	in := m.session.Inputs()
	if len(in) != 1 || !in[0].Float || len(in[0].Shape) != 4 {
		return fmt.Errorf("want one float32 4-d image input, have %+v", in)
	}
	sh := in[0].Shape
	var h, w int64
	switch {
	case sh[1] == 3:
		h, w = sh[2], sh[3]
	case sh[3] == 3:
		m.nhwc, h, w = true, sh[1], sh[2]
	default:
		return fmt.Errorf("input %v has no 3-channel axis (NCHW or NHWC)", sh)
	}
	if h <= 0 || w <= 0 {
		h, w = defaultClassifySize, defaultClassifySize
	}
	if h > 4096 || w > 4096 {
		return fmt.Errorf("input size %dx%d is over 4096", w, h)
	}
	m.width, m.height = int(w), int(h)

	outs := m.session.Outputs()
	if len(outs) == 0 || !outs[0].Float {
		return fmt.Errorf("want a float32 output, have %+v", outs)
	}
	n := int64(1)
	for _, d := range outs[0].Shape {
		if d > 0 {
			n *= d
		}
	}
	m.labels = labels
	if m.labels == nil {
		if n != imagenetClassCount {
			return fmt.Errorf("output %v has %d classes: set the class names file for a non-ImageNet model", outs[0].Shape, n)
		}
		m.labels = imagenetClassNames()
	}
	if n != int64(len(m.labels)) {
		return fmt.Errorf("output %v has %d classes, the class names file %d", outs[0].Shape, n, len(m.labels))
	}
	return nil
}

// close releases the session, if it was created.
func (c *classifier) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m != nil {
		c.m.session.Close()
		c.m = nil
	}
	c.closed = true
}

// imagenetClassNames parses the embedded ImageNet class names.
func imagenetClassNames() []string {
	return strings.Split(strings.TrimSuffix(imagenetLabels, "\n"), "\n")
}

// ImageNetClasses returns the ImageNet-1k class names of the shipped
// classifier, in class-id order.
func ImageNetClasses() []string { return imagenetClassNames() }

// maxClassNames bounds a class names file.
const maxClassNames = 100_000

// readClassNames reads a class names file: one name per line, in the
// model's output order (imgproxy's IMGPROXY_CLASSIFICATION_CLASSES
// format). Surrounding spaces are trimmed; empty names are an error, and a
// trailing empty line is ignored.
func readClassNames(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errs.Wrapf(errs.KindInternal, err, "detect: class names")
	}
	defer f.Close()
	names, err := parseClassNames(f)
	if err != nil {
		return nil, errs.Wrapf(errs.KindInternal, err, "detect: class names %s", path)
	}
	return names, nil
}

// parseClassNames is readClassNames' parser.
func parseClassNames(r io.Reader) ([]string, error) {
	sc := bufio.NewScanner(r)
	var names []string
	blank := 0
	for sc.Scan() {
		name := strings.TrimSpace(sc.Text())
		if name == "" {
			blank++
			continue
		}
		if blank > 0 {
			return nil, fmt.Errorf("empty class name at line %d", len(names)+1)
		}
		if len(names) == maxClassNames {
			return nil, fmt.Errorf("more than %d class names", maxClassNames)
		}
		names = append(names, name)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, errors.New("no class names")
	}
	return names, nil
}

// prepareClassify renders img's first page into the model input: resized
// so the input covers classifyCropFraction of it, centre-cropped, and
// packed normalised into C memory.
func (d *Detector) prepareClassify(ctx context.Context, img *vips.Image, m *classModel) (*vips.Buffer, error) {
	var a vips.Arena
	defer a.Close()
	cur := img
	srcW, srcH := img.Width(), img.Height()
	if ph := img.PageHeight(); ph > 0 && ph < srcH {
		var err error
		if cur, err = a.Add(img.ExtractArea(ctx, image.Rect(0, 0, srcW, ph))); err != nil {
			return nil, err
		}
		srcH = ph
	}
	if srcW <= 0 || srcH <= 0 {
		return nil, errs.Newf(errs.KindInternal, "detect: image is %dx%d", srcW, srcH)
	}
	scale := classifyScale(srcW, srcH, m.width, m.height)
	cur, err := a.Add(cur.Resize(ctx, scale, scale, vips.KernelCubic))
	if err != nil {
		return nil, err
	}
	// libvips rounds the resized size itself: centre on what it made.
	rw, rh := cur.Width(), cur.Height()
	if rw < m.width || rh < m.height {
		return nil, errs.Newf(errs.KindInternal, "detect: resized %dx%d is smaller than the %dx%d input", rw, rh, m.width, m.height)
	}
	crop := image.Rect(0, 0, m.width, m.height).Add(image.Pt((rw-m.width)/2, (rh-m.height)/2))
	if crop != image.Rect(0, 0, rw, rh) {
		if cur, err = a.Add(cur.ExtractArea(ctx, crop)); err != nil {
			return nil, err
		}
	}
	pix, err := cur.ToRGB8(ctx)
	if err != nil {
		return nil, err
	}
	defer pix.Release()

	buf, err := vips.NewBuffer(3 * m.width * m.height * 4)
	if err != nil {
		return nil, err
	}
	dst, err := float32View(buf.Bytes())
	if err != nil {
		buf.Release()
		return nil, err
	}
	if err := packRGB(dst, pix.Bytes(), m.width, m.height, m.nhwc, d.classifier.norm); err != nil {
		buf.Release()
		return nil, errs.Wrap(errs.KindInternal, err)
	}
	return buf, nil
}

// classifyScale is the uniform factor that scales the source so that the tw×th input is the centre
// classifyCropFraction of it on the side that fills it first (timm:
// resize the short side to size/crop_pct, then centre-crop), up or down.
// The factor is nudged so libvips' rounding never makes the resized image
// smaller than the input.
func classifyScale(srcW, srcH, tw, th int) float64 {
	scale := max(float64(tw)/float64(srcW), float64(th)/float64(srcH)) / classifyCropFraction
	// Never below the input (rounding), with half a pixel of margin.
	scale = max(scale, (float64(tw)+0.5)/float64(srcW), (float64(th)+0.5)/float64(srcH))
	return scale
}

// packRGB writes w×h 8-bit RGB pixels as a normalised RGB float32 tensor:
// planar (NCHW) or interleaved (NHWC).
func packRGB(dst []float32, pix []byte, w, h int, nhwc bool, norm Normalization) error {
	n := w * h
	switch {
	case w <= 0 || h <= 0:
		return fmt.Errorf("detect: cannot pack %dx%d", w, h)
	case len(dst) != 3*n:
		return fmt.Errorf("detect: tensor has %d values, want %d", len(dst), 3*n)
	case len(pix) < 3*n:
		return fmt.Errorf("detect: %d pixel bytes for %dx%d", len(pix), w, h)
	}
	// ImageNet channel statistics (timm IMAGENET_DEFAULT_MEAN / _STD).
	mean := [3]float32{0.485, 0.456, 0.406}
	std := [3]float32{0.229, 0.224, 0.225}
	var scale, bias [3]float32
	for c := range 3 {
		switch norm {
		case NormalizeImageNet:
			scale[c] = 1 / (255 * std[c])
			bias[c] = -mean[c] / std[c]
		case NormalizeFull:
			scale[c], bias[c] = 1/127.5, -1
		case NormalizeHalf:
			scale[c] = 1.0 / 255
		default:
			scale[c] = 1
		}
	}
	if nhwc {
		for i := range 3 * n {
			c := i % 3
			dst[i] = float32(pix[i])*scale[c] + bias[c]
		}
		return nil
	}
	r, g, b := dst[:n], dst[n:2*n], dst[2*n:]
	for i := range n {
		p := pix[3*i : 3*i+3]
		r[i] = float32(p[0])*scale[0] + bias[0]
		g[i] = float32(p[1])*scale[1] + bias[1]
		b[i] = float32(p[2])*scale[2] + bias[2]
	}
	return nil
}

// probabilities copies the model output to Go memory as probabilities: the
// values themselves when they already are a distribution (a model ending
// in Softmax), else their softmax.
func probabilities(out []float32) []float32 {
	p := make([]float32, len(out))
	var sum float64
	isDist := true
	peak := float32(math.Inf(-1))
	for _, v := range out {
		if !(v >= 0 && v <= 1) {
			isDist = false
		}
		sum += float64(v)
		peak = max(peak, v)
	}
	if isDist && math.Abs(sum-1) < 1e-3 {
		copy(p, out)
		return p
	}
	sum = 0
	for i, v := range out {
		e := math.Exp(float64(v - peak))
		if math.IsNaN(e) {
			e = 0
		}
		p[i] = float32(e)
		sum += e
	}
	if sum > 0 {
		for i := range p {
			p[i] = float32(float64(p[i]) / sum)
		}
	}
	return p
}
