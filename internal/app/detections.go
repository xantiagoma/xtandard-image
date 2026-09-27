package app

import (
	"container/list"
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"math"
	"sync"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/detect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// detectSampleSide is the longest side of the image detection runs on:
// the source is thumbnailed upright to it (shrink-on-load), and the
// detector letterboxes that to the model's 640×640. A little over the
// model input keeps the final resize (the detector's) a real downscale.
const detectSampleSide = 1024

// Detection model names (cache keys, logs).
const (
	modelFace   = "face"
	modelObject = "object"
)

// faceObjectDetector is what the pipeline needs from internal/detect; a
// nil *detect.Detector implements it and reports nothing available. Tests
// inject fakes.
type faceObjectDetector interface {
	HasFaces() bool
	HasObjects() bool
	Faces(ctx context.Context, img *vips.Image) ([]detect.Detection, error)
	Objects(ctx context.Context, img *vips.Image, classes []string) ([]detect.Detection, error)
}

// detections runs face and object detection for processing and /info
// (WP-72/73) and caches the results per source.
type detections struct {
	det   faceObjectDetector
	cache *detectCache // nil: no cache
	// smartFaces lets smart gravity crop on faces (XI_DETECT_SMART_FACES).
	smartFaces bool
	limits     codec.Limits
	logger     *slog.Logger
}

// newDetections wraps det (nil when detection is off or unavailable).
func newDetections(det faceObjectDetector, cacheSize int, smartFaces bool, limits codec.Limits, logger *slog.Logger) *detections {
	if det == nil {
		det = (*detect.Detector)(nil)
	}
	d := &detections{det: det, smartFaces: smartFaces, limits: limits, logger: logger}
	if cacheSize > 0 && (det.HasFaces() || det.HasObjects()) {
		d.cache = newDetectCache(cacheSize)
	}
	return d
}

// unavailable is the error of a request that needs a model this
// deployment does not have (the slim image, or a model not configured).
func unavailable(model string) error {
	return errs.WithPublic(fmt.Errorf("app: %s detection: %w", model, detect.ErrUnavailable),
		model+" detection is not available")
}

// detectSource is what detection runs on: the first page of an image
// source, or the frame of a video source the request uses (WP-91).
type detectSource struct {
	// buf holds the source bytes (the cache key) and format is their
	// probed format.
	buf    *vips.Buffer
	format codec.Format
	// frame, set for a video source, returns its decoded frame (in the
	// stored orientation) and the orientation that shows it upright. It
	// runs inside vips.Run; the caller owns and releases the frame, so the
	// same decode can be processed afterwards.
	frame func(ctx context.Context) (*codec.DecodedFrame, int, error)
}

// imageSource is the detectSource of an image (not video) source.
func imageSource(buf *vips.Buffer, f codec.Format) *detectSource {
	return &detectSource{buf: buf, format: f}
}

// forRequest returns the detections needs asks for on src, or nil when it
// asks for none. A required model the deployment lacks is Unsupported
// (422); faces for smart gravity are optional: without the face model,
// with XI_DETECT_SMART_FACES=false, or when detection fails, the planner
// uses the attention smart crop.
func (d *detections) forRequest(ctx context.Context, src *detectSource, needs plan.DetectionNeeds) (*plan.Detections, error) {
	if !needs.Any() {
		return nil, nil
	}
	if d == nil {
		d = &detections{det: (*detect.Detector)(nil)}
	}
	faces, objects := needs.Faces, needs.Objects
	if faces && !d.det.HasFaces() {
		return nil, unavailable(modelFace)
	}
	if objects && !d.det.HasObjects() {
		return nil, unavailable(modelObject)
	}
	optional := !faces && !objects && needs.SmartFaces
	if needs.SmartFaces && d.smartFaces && d.det.HasFaces() {
		faces = true
	}
	if !faces && !objects {
		return nil, nil
	}
	dets, err := d.detect(ctx, src, faces, objects)
	if err != nil && optional && ctx.Err() == nil {
		d.logger.LogAttrs(ctx, slog.LevelDebug, "smart face detection failed", slog.String("error", err.Error()))
		return nil, nil
	}
	return dets, err
}

// noFrame is the frame of a detectKey for an image source.
const noFrame = -1

// frameKey is the detectKey frame of a video frame at t seconds:
// microseconds.
func frameKey(t float64) int64 { return int64(math.Round(t * 1e6)) }

// detect runs the face and/or object model on src, from the cache when it
// has them. A video frame is decoded first (its timestamp is part of the
// key), also on a cache hit: the caller processes the same decode.
func (d *detections) detect(ctx context.Context, src *detectSource, faces, objects bool) (*plan.Detections, error) {
	sum := sha256.Sum256(src.buf.Bytes())
	out := &plan.Detections{}
	var needFaces, needObjects bool
	lookup := func(frame int64) bool {
		if faces {
			out.Faces, needFaces = d.cache.get(detectKey{sum: sum, model: modelFace, frame: frame})
			needFaces = !needFaces
		}
		if objects {
			out.Objects, needObjects = d.cache.get(detectKey{sum: sum, model: modelObject, frame: frame})
			needObjects = !needObjects
		}
		return needFaces || needObjects
	}
	if src.frame == nil && !lookup(noFrame) {
		return out, nil
	}
	err := vips.Run(ctx, func() error {
		var a vips.Arena
		defer a.Close()
		frame := int64(noFrame)
		var img *vips.Image
		var err error
		if src.frame != nil {
			var f *codec.DecodedFrame
			var orientation int
			if f, orientation, err = src.frame(ctx); err != nil {
				return err
			}
			if frame = frameKey(f.Info.Time); !lookup(frame) {
				return nil
			}
			if img, err = sampleFrame(ctx, &a, f, orientation); err != nil {
				return err
			}
		} else if img, err = codec.SampleImage(ctx, &a, src.buf, src.format, detectSampleSide, d.limits); err != nil {
			return err
		}
		// The thumbnail is read sequentially; each model reads it once, so
		// it is computed into memory first (at most detectSampleSide²).
		if img, err = a.Add(img.CopyMemory(ctx)); err != nil {
			return err
		}
		w, h := img.Width(), img.Height()
		if ph := img.PageHeight(); ph > 0 && ph < h {
			h = ph // the detector reads the first page
		}
		if needFaces {
			found, err := d.det.Faces(ctx, img)
			if err != nil {
				return err
			}
			out.Faces = planDetections(found, w, h)
			d.cache.put(detectKey{sum: sum, model: modelFace, frame: frame}, out.Faces)
		}
		if err := ctx.Err(); err != nil {
			return errs.Wrap(errs.KindOf(err), context.Cause(ctx))
		}
		if needObjects {
			found, err := d.det.Objects(ctx, img, nil)
			if err != nil {
				return err
			}
			out.Objects = planDetections(found, w, h)
			d.cache.put(detectKey{sum: sum, model: modelObject, frame: frame}, out.Objects)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// sampleFrame is the detection image of a decoded video frame: upright
// (orientation applied) and scaled down to fit detectSampleSide, like
// codec.SampleImage for images. It runs inside vips.Run.
func sampleFrame(ctx context.Context, a *vips.Arena, f *codec.DecodedFrame, orientation int) (*vips.Image, error) {
	img, err := f.UprightImage(ctx, a, orientation)
	if err != nil {
		return nil, err
	}
	if s := float64(detectSampleSide) / float64(max(img.Width(), img.Height())); s < 1 {
		return a.Add(img.Resize(ctx, s, s, vips.KernelLanczos3))
	}
	return img, nil
}

// planDetections converts detections in a w×h image to the planner's
// fractions (boxes and landmarks).
func planDetections(found []detect.Detection, w, h int) []plan.Detected {
	out := make([]plan.Detected, 0, len(found))
	if w <= 0 || h <= 0 {
		return out
	}
	fw, fh := float64(w), float64(h)
	for _, x := range found {
		p := plan.Detected{
			Class: x.Class, Score: float64(x.Score),
			Box: transform.Area{
				Left: float64(x.Box.Min.X) / fw, Top: float64(x.Box.Min.Y) / fh,
				Width: float64(x.Box.Dx()) / fw, Height: float64(x.Box.Dy()) / fh,
			},
		}
		for _, l := range x.Landmarks {
			p.Landmarks = append(p.Landmarks, [2]float64{float64(l.X) / fw, float64(l.Y) / fh})
		}
		out = append(out, p)
	}
	return out
}

// detectKey identifies the result of one model on one source: the
// SHA-256 of the source bytes (the fetched or sanitised bytes, so an
// origin that changes behind the same URL is detected again), the model
// and, for a video, the timestamp of the frame detected (frameKey; noFrame
// for images). The detection image is always derived from the bytes (or
// the frame) the same way (detectSampleSide), so the key needs nothing
// else.
type detectKey struct {
	sum   [sha256.Size]byte
	model string
	frame int64
}

// detectCache is a bounded LRU of detection results shared by processing
// and /info. Entries are immutable once stored.
type detectCache struct {
	mu    sync.Mutex
	max   int
	order *list.List // front = most recently used; values are *detectEntry
	byKey map[detectKey]*list.Element
}

type detectEntry struct {
	key  detectKey
	dets []plan.Detected
}

func newDetectCache(size int) *detectCache {
	return &detectCache{max: size, order: list.New(), byKey: make(map[detectKey]*list.Element, size)}
}

// get returns the cached result of k; a nil cache has nothing.
func (c *detectCache) get(k detectKey) ([]plan.Detected, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byKey[k]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(e)
	entry, _ := e.Value.(*detectEntry)
	return entry.dets, true
}

// put stores the result of k, evicting the least recently used entries
// beyond the bound; a nil cache stores nothing.
func (c *detectCache) put(k detectKey, dets []plan.Detected) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.byKey[k]; ok {
		c.order.MoveToFront(e)
		entry, _ := e.Value.(*detectEntry)
		entry.dets = dets
		return
	}
	c.byKey[k] = c.order.PushFront(&detectEntry{key: k, dets: dets})
	for c.order.Len() > c.max {
		last := c.order.Back()
		entry, _ := last.Value.(*detectEntry)
		delete(c.byKey, entry.key)
		c.order.Remove(last)
	}
}

// len is the number of cached results.
func (c *detectCache) len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
