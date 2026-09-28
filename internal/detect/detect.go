package detect

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math"
	"runtime"
	"slices"
	"strconv"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/xantiagoma/xtandard-image/internal/detect/ort"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// ErrUnavailable means detection cannot run in this process: no ONNX
// Runtime library (the slim image, a build without cgo) or no model file.
// Its kind is Unsupported, so a request that needs detection on such a
// deployment fails with 422, never 500.
var ErrUnavailable = errs.New(errs.KindUnsupported, "detect: object detection is unavailable")

// Defaults of the Options thresholds.
const (
	// DefaultFaceThreshold is the minimum YuNet score. OpenCV's sample uses
	// 0.9 for display; 0.6 keeps small and profile faces for cropping.
	DefaultFaceThreshold = 0.6
	// DefaultFaceNMS is OpenCV FaceDetectorYN's default NMS threshold.
	DefaultFaceNMS = 0.3
	// DefaultObjectThreshold is the YOLOX demo's visualisation threshold.
	DefaultObjectThreshold = 0.3
	// DefaultObjectNMS is the YOLOX demo's NMS threshold.
	DefaultObjectNMS = 0.45
	// DefaultMaxDetections caps the detections one call returns.
	DefaultMaxDetections = 100
	// DefaultMaxInput is the longest side fed to a model with a dynamic
	// input size (fixed-size models use their own shape).
	DefaultMaxInput = 640
)

// Pad values of the letterbox around the resized image (YOLOX preproc fills
// with 114; OpenCV pads YuNet's input with zeros).
const (
	yoloxPad = 114
	yunetPad = 0
)

// Options configures New. Zero thresholds and limits take the defaults
// above.
type Options struct {
	// Library is the ONNX Runtime shared library: a path, or a file name the
	// dynamic linker resolves (e.g. "libonnxruntime.so.1").
	Library string
	// ModelsDir holds the model files named by FaceModel and ObjectModel
	// (absolute names are used as they are).
	ModelsDir string
	// FaceModel is a YuNet ONNX file; empty disables Faces.
	FaceModel string
	// ObjectModel is a YOLOX (COCO, 80 classes) ONNX file; empty disables
	// Objects.
	ObjectModel string
	// ClassifyModel is an image classifier ONNX file (ImageNet-1k by
	// default); empty disables Classify. Its session is created on the
	// first Classify call. A missing file disables Classify only (see
	// ClassifyUnavailable) unless it is the only model configured.
	ClassifyModel string
	// ClassifyLabels is the class names file of ClassifyModel, one per
	// line in output order; empty uses the embedded ImageNet-1k names.
	ClassifyLabels string
	// ClassifyNormalization scales the classifier's input (default
	// NormalizeImageNet).
	ClassifyNormalization Normalization
	// VerifyChecksums checks each model against the SHA256SUMS file of its
	// directory when it lists the model.
	VerifyChecksums bool
	// Threads is ONNX Runtime's intra-op thread count per inference.
	Threads int
	// Workers bounds concurrent inferences; 0 is GOMAXPROCS / Threads.
	Workers int
	// MaxInput is the longest side fed to a dynamic-size model.
	MaxInput int

	FaceThreshold, FaceNMS     float32
	ObjectThreshold, ObjectNMS float32
	MaxDetections              int

	// Observer receives one duration per detection call; nil disables it.
	Observer Observer
	// Logger receives a debug record per detection call; nil discards.
	Logger *slog.Logger
}

// Observer receives detection timings (the app forwards them to
// xi_detect_duration_seconds).
type Observer interface {
	ObserveDetect(model string, d time.Duration)
}

// Detection is one detected face or object in the pixel space of the image
// passed to Faces or Objects (its first page for multi-page images).
type Detection struct {
	Box   image.Rectangle
	Score float32
	Class string
	// Landmarks of a face: right eye, left eye, nose tip, right and left
	// mouth corners. Nil for objects.
	Landmarks []image.Point
}

// ModelInfo describes a loaded model, for startup logs.
type ModelInfo struct {
	Kind     string // "face", "object" or "classify"
	Path     string
	Input    string // e.g. "640x640", "dynamic", or "lazy" (a classifier not loaded yet)
	Checksum string // "verified", "unlisted" (not in SHA256SUMS) or "skipped"
}

// Model kinds, also the model label of xi_detect_duration_seconds.
const (
	modelFace   = "face"
	modelObject = "object"
)

// model is one loaded ONNX session with the facts its pre/post-processing
// needs.
type model struct {
	info          ModelInfo
	session       *ort.Session
	width, height int // fixed input size; 0 when dynamic
	pad           float32
}

// Detector runs the face and object models. It is safe for concurrent use;
// Faces and Objects must be called inside vips.Run (they resize and read the
// image through libvips). A nil *Detector is valid and reports
// ErrUnavailable, so callers need no special case for slim deployments.
type Detector struct {
	rt         *ort.Runtime
	face       *model
	object     *model
	classifier *classifier // nil: no classification
	// classifyErr is why the configured classifier is unavailable (its
	// file is missing while the detection models are there); nil otherwise.
	classifyErr error
	sem         *semaphore.Weighted
	maxInput    int
	opts        Options
}

// New loads ONNX Runtime and the configured models. Nothing is loaded
// before this call (no init). It returns an error wrapping ErrUnavailable
// when the library or a model file is missing; any other error (a corrupt
// or unexpected model, a checksum mismatch) is a configuration error.
func New(options *Options) (*Detector, error) {
	opts := withDefaults(options)
	if opts.FaceModel == "" && opts.ObjectModel == "" && opts.ClassifyModel == "" {
		return nil, errs.New(errs.KindInternal, "detect: no model configured")
	}
	if opts.Library == "" {
		return nil, errs.New(errs.KindInternal, "detect: no onnx runtime library configured")
	}
	type pending struct {
		kind, name string
		path       string
		checksum   string
	}
	var (
		models      []pending
		classify    *classifier
		classifyErr error
	)
	for _, m := range []pending{
		{kind: modelFace, name: opts.FaceModel},
		{kind: modelObject, name: opts.ObjectModel},
		{kind: modelClassify, name: opts.ClassifyModel},
	} {
		if m.name == "" {
			continue
		}
		path, err := resolveModel(opts.ModelsDir, m.name)
		if m.kind == modelClassify && errors.Is(err, ErrUnavailable) && (opts.FaceModel != "" || opts.ObjectModel != "") {
			// A models directory with the detection models but not the
			// classifier keeps detecting.
			classifyErr = err
			continue
		}
		if err != nil {
			return nil, err
		}
		m.path, m.checksum = path, "skipped"
		if opts.VerifyChecksums {
			listed, err := verifyChecksum(path)
			if err != nil {
				return nil, err
			}
			m.checksum = "unlisted"
			if listed {
				m.checksum = "verified"
			}
		}
		if m.kind == modelClassify {
			classify = &classifier{
				info: ModelInfo{Kind: modelClassify, Path: m.path, Input: "lazy", Checksum: m.checksum},
				norm: opts.ClassifyNormalization,
			}
			if opts.ClassifyLabels != "" {
				if classify.labels, err = readClassNames(opts.ClassifyLabels); err != nil {
					return nil, err
				}
			}
			continue
		}
		models = append(models, m)
	}

	rt, err := ort.Load(opts.Library)
	if errors.Is(err, ort.ErrUnavailable) {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, err)
	}
	d := &Detector{
		rt:          rt,
		classifier:  classify,
		classifyErr: classifyErr,
		sem:         semaphore.NewWeighted(int64(opts.Workers)),
		maxInput:    opts.MaxInput,
		opts:        opts,
	}
	for _, p := range models {
		m, err := loadModel(rt, p.kind, p.path, opts.Threads)
		if err != nil {
			d.Close()
			return nil, err
		}
		m.info.Checksum = p.checksum
		if p.kind == modelFace {
			d.face = m
		} else {
			d.object = m
		}
	}
	return d, nil
}

func withDefaults(options *Options) Options {
	o := *options
	o.Threads = max(o.Threads, 1)
	if o.Workers <= 0 {
		o.Workers = max(runtime.GOMAXPROCS(0)/o.Threads, 1)
	}
	if o.MaxInput <= 0 {
		o.MaxInput = DefaultMaxInput
	}
	o.MaxInput = max(o.MaxInput, maxStride)
	if o.FaceThreshold <= 0 {
		o.FaceThreshold = DefaultFaceThreshold
	}
	if o.FaceNMS <= 0 {
		o.FaceNMS = DefaultFaceNMS
	}
	if o.ObjectThreshold <= 0 {
		o.ObjectThreshold = DefaultObjectThreshold
	}
	if o.ObjectNMS <= 0 {
		o.ObjectNMS = DefaultObjectNMS
	}
	if o.MaxDetections <= 0 {
		o.MaxDetections = DefaultMaxDetections
	}
	return o
}

// loadModel creates the session and checks that the model has the shape
// its decoder expects, so a wrong file fails at startup, not per request.
func loadModel(rt *ort.Runtime, kind, path string, threads int) (*model, error) {
	s, err := rt.NewSession(path, ort.SessionOptions{IntraOpThreads: threads})
	if err != nil {
		return nil, errs.Wrapf(errs.KindInternal, err, "detect: load %s model %s", kind, path)
	}
	m := &model{session: s, info: ModelInfo{Kind: kind, Path: path, Input: "dynamic"}}
	if err := m.check(kind); err != nil {
		s.Close()
		return nil, errs.Wrapf(errs.KindInternal, err, "detect: %s model %s", kind, path)
	}
	if m.width > 0 {
		m.info.Input = strconv.Itoa(m.width) + "x" + strconv.Itoa(m.height)
	}
	return m, nil
}

// check validates the session's input (one float32 NCHW tensor with 3
// channels) and outputs against the model family.
func (m *model) check(kind string) error {
	in := m.session.Inputs()
	if len(in) != 1 || !in[0].Float || len(in[0].Shape) != 4 || in[0].Shape[1] != 3 {
		return fmt.Errorf("want one float32 [1,3,H,W] input, have %+v", in)
	}
	h, w := in[0].Shape[2], in[0].Shape[3]
	switch {
	case h > 0 && w > 0 && h%maxStride == 0 && w%maxStride == 0 && h <= 4096 && w <= 4096:
		m.width, m.height = int(w), int(h)
	case h > 0 || w > 0:
		return fmt.Errorf("input size %dx%d is not a multiple of %d up to 4096", w, h, maxStride)
	}
	outs := m.session.Outputs()
	switch kind {
	case modelFace:
		m.pad = yunetPad
		names := make([]string, 0, len(outs))
		for _, o := range outs {
			names = append(names, o.Name)
		}
		for stride := minStride; stride <= maxStride; stride *= 2 {
			cls, obj, bbox, kps := yunetOutputs(stride)
			for _, n := range []string{cls, obj, bbox, kps} {
				if !slices.Contains(names, n) {
					return fmt.Errorf("not a YuNet model: no output %q (have %v)", n, names)
				}
			}
		}
	default:
		m.pad = yoloxPad
		if len(outs) != 1 || !outs[0].Float || len(outs[0].Shape) != 3 || outs[0].Shape[2] != 5+cocoClassCount {
			return fmt.Errorf("not a YOLOX COCO model: want one float32 [1,N,%d] output, have %+v", 5+cocoClassCount, outs)
		}
	}
	return nil
}

// Close releases the sessions and the runtime. Call it once no detection is
// in flight. A nil Detector's Close does nothing.
func (d *Detector) Close() {
	if d == nil {
		return
	}
	for _, m := range []*model{d.face, d.object} {
		if m != nil {
			m.session.Close()
		}
	}
	if d.classifier != nil {
		d.classifier.close()
	}
	d.rt.Close()
}

// Version returns the ONNX Runtime version ("" for a nil Detector).
func (d *Detector) Version() string {
	if d == nil {
		return ""
	}
	return d.rt.Version()
}

// Models describes the loaded models.
func (d *Detector) Models() []ModelInfo {
	if d == nil {
		return nil
	}
	var out []ModelInfo
	for _, m := range []*model{d.face, d.object} {
		if m != nil {
			out = append(out, m.info)
		}
	}
	if c := d.classifier; c != nil {
		c.mu.Lock()
		out = append(out, c.info)
		c.mu.Unlock()
	}
	return out
}

// ClassifyUnavailable reports why the configured classifier is missing
// while the detection models are loaded, or nil.
func (d *Detector) ClassifyUnavailable() error {
	if d == nil {
		return nil
	}
	return d.classifyErr
}

// HasFaces reports whether Faces can run.
func (d *Detector) HasFaces() bool { return d != nil && d.face != nil }

// HasObjects reports whether Objects can run.
func (d *Detector) HasObjects() bool { return d != nil && d.object != nil }

// Faces detects faces in img, best first. It must run inside vips.Run.
func (d *Detector) Faces(ctx context.Context, img *vips.Image) ([]Detection, error) {
	if !d.HasFaces() {
		return nil, fmt.Errorf("%w: no face model", ErrUnavailable)
	}
	return d.detect(ctx, img, d.face, nil)
}

// Objects detects COCO objects in img, best first. classes restricts the
// result to those class names (see ObjectClasses); nil or empty keeps every
// class. An unknown class name is an InvalidURL error, since class names
// come from request options. It must run inside vips.Run.
func (d *Detector) Objects(ctx context.Context, img *vips.Image, classes []string) ([]Detection, error) {
	var keep []bool
	if len(classes) > 0 {
		keep = make([]bool, cocoClassCount)
		for _, c := range classes {
			id := ClassID(c)
			if id < 0 || c == FaceClass {
				return nil, errs.WithPublic(errs.Newf(errs.KindInvalidURL, "detect: unknown object class %q", c),
					"unknown object class "+strconv.Quote(c))
			}
			keep[id] = true
		}
	}
	if !d.HasObjects() {
		return nil, fmt.Errorf("%w: no object model", ErrUnavailable)
	}
	return d.detect(ctx, img, d.object, keep)
}

// detect runs one model over img: letterbox and tensor in C memory,
// inference under the semaphore, decoding and NMS in Go, and mapping back
// to img's pixels.
func (d *Detector) detect(ctx context.Context, img *vips.Image, m *model, keep []bool) (_ []Detection, err error) {
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		if d.opts.Observer != nil && err == nil {
			d.opts.Observer.ObserveDetect(m.info.Kind, elapsed)
		}
		if d.opts.Logger != nil {
			d.opts.Logger.LogAttrs(ctx, slog.LevelDebug, "detection finished",
				slog.String("model", m.info.Kind), slog.Duration("duration", elapsed), slog.Bool("ok", err == nil))
		}
	}()
	if img == nil {
		return nil, errs.New(errs.KindInternal, "detect: nil image")
	}
	in, err := d.prepare(ctx, img, m)
	if err != nil {
		return nil, err
	}
	defer in.buf.Release()

	if err := d.sem.Acquire(ctx, 1); err != nil {
		return nil, ctxError(ctx, err)
	}
	outs, err := m.session.Run(ctx, in.buf.Bytes(), []int64{1, 3, int64(in.th), int64(in.tw)})
	d.sem.Release(1)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctxError(ctx, err)
		}
		return nil, errs.Wrapf(errs.KindInternal, err, "detect: run %s model", m.info.Kind)
	}
	defer func() {
		for i := range outs {
			outs[i].Release()
		}
	}()

	var (
		cands      []candidate
		threshold  float32
		classAware bool
	)
	if m.info.Kind == modelFace {
		byName := make(map[string][]float32, len(outs))
		for i := range outs {
			byName[outs[i].Name] = outs[i].Data
		}
		threshold = d.opts.FaceNMS
		cands, err = decodeYuNet(byName, in.tw, in.th, d.opts.FaceThreshold)
	} else {
		threshold, classAware = d.opts.ObjectNMS, true
		cands, err = decodeYOLOX(outs[0].Data, in.tw, in.th, cocoClassCount, d.opts.ObjectThreshold, keep)
	}
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, err)
	}
	return in.toSource(nms(cands, threshold, classAware, d.opts.MaxDetections), m.info.Kind == modelFace), nil
}

// ctxError classifies the end of ctx (canceled or deadline) like
// internal/vips does.
func ctxError(ctx context.Context, err error) error {
	if ctx.Err() == nil {
		return errs.Wrap(errs.KindInternal, err)
	}
	return errs.Wrapf(errs.KindOf(ctx.Err()), context.Cause(ctx), "detect: %v", err)
}

// toSource maps kept candidates from model-input pixels to source pixels,
// clamps them to the image and drops boxes that end up empty.
func (in *input) toSource(kept []candidate, face bool) []Detection {
	sx := float32(in.srcW) / float32(in.resW)
	sy := float32(in.srcH) / float32(in.resH)
	bounds := image.Rect(0, 0, in.srcW, in.srcH)
	classes := cocoClasses()
	out := make([]Detection, 0, len(kept))
	for _, c := range kept {
		r := image.Rect(round(c.box.x1*sx), round(c.box.y1*sy), round(c.box.x2*sx), round(c.box.y2*sy)).Intersect(bounds)
		if r.Empty() {
			continue
		}
		det := Detection{Box: r, Score: c.score}
		if face {
			det.Class = FaceClass
			det.Landmarks = make([]image.Point, faceLandmarks)
			for j := range faceLandmarks {
				det.Landmarks[j] = image.Pt(
					min(max(round(c.landmarks[2*j]*sx), 0), in.srcW-1),
					min(max(round(c.landmarks[2*j+1]*sy), 0), in.srcH-1))
			}
		} else {
			det.Class = classes[c.class]
		}
		out = append(out, det)
	}
	return out
}

// round converts a finite coordinate to the nearest int, saturating far
// outside the image (clamping follows).
func round(v float32) int {
	return int(math.Round(float64(min(max(v, -1<<24), 1<<24))))
}
