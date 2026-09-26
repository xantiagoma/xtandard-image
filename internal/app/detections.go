package app

import (
	"container/list"
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
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

// forRequest returns the detections needs asks for on src (a source of
// format f, as probed), or nil when it asks for none. A required model
// the deployment lacks is Unsupported (422); faces for smart gravity are
// optional: without the face model, with XI_DETECT_SMART_FACES=false, or
// when detection fails, the planner uses the attention smart crop.
func (d *detections) forRequest(ctx context.Context, src *vips.Buffer, f codec.Format, needs plan.DetectionNeeds) (*plan.Detections, error) {
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
	dets, err := d.detect(ctx, src, f, faces, objects)
	if err != nil && optional && ctx.Err() == nil {
		d.logger.LogAttrs(ctx, slog.LevelDebug, "smart face detection failed", slog.String("error", err.Error()))
		return nil, nil
	}
	return dets, err
}

// detect runs the face and/or object model on src, from the cache when it
// has them.
func (d *detections) detect(ctx context.Context, src *vips.Buffer, f codec.Format, faces, objects bool) (*plan.Detections, error) {
	sum := sha256.Sum256(src.Bytes())
	out := &plan.Detections{}
	var needFaces, needObjects bool
	if faces {
		out.Faces, needFaces = d.cache.get(detectKey{sum: sum, model: modelFace})
		needFaces = !needFaces
	}
	if objects {
		out.Objects, needObjects = d.cache.get(detectKey{sum: sum, model: modelObject})
		needObjects = !needObjects
	}
	if !needFaces && !needObjects {
		return out, nil
	}
	err := vips.Run(ctx, func() error {
		var a vips.Arena
		defer a.Close()
		img, err := codec.SampleImage(ctx, &a, src, f, detectSampleSide, d.limits)
		if err != nil {
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
			d.cache.put(detectKey{sum: sum, model: modelFace}, out.Faces)
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
			d.cache.put(detectKey{sum: sum, model: modelObject}, out.Objects)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
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
// origin that changes behind the same URL is detected again) and the
// model. The detection image is always derived from the bytes the same
// way (detectSampleSide), so the key needs nothing else.
type detectKey struct {
	sum   [sha256.Size]byte
	model string
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
