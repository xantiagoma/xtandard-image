package encode

import (
	"context"
	"math"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Skip reasons reported in AutoQualityResult.Ignored (and, through
// pipeline/exec, in the Skipped list of the response).
const (
	// IgnoredML: the ml method is not implemented; the quality resolves as
	// without auto-quality.
	IgnoredML = "autoquality_ml"
	// IgnoredSizeTarget: the size method has no byte target (the target is
	// below 1 and no max_bytes is set).
	IgnoredSizeTarget = "autoquality_size_target"
)

// AutoQualityResult reports the auto-quality of one Encode.
type AutoQualityResult struct {
	// Method is the method that ran: AutoQualityUnset when none did.
	Method transform.AutoQualityMethod
	// Target is the DSSIM target (dssim) or the byte budget (size).
	Target float64
	// Metric is the measured value of the returned image: its DSSIM on the
	// comparison copy (dssim) or its size in bytes (size).
	Metric float64
	// Met reports that Metric is at most Target (dssim: plus the allowed
	// error). A dssim search that ends unmet returned its best attempt,
	// a size search the smallest one (Result.OverBudget is then set too).
	Met bool
	// Ignored names why a requested auto-quality did not run (IgnoredML,
	// IgnoredSizeTarget); empty otherwise.
	Ignored string
}

// aqParams is the resolved auto-quality of one Target.
type aqParams struct {
	method       transform.AutoQualityMethod
	target       float64
	lo, hi       int // searched quality range
	allowedError float64
}

// bounds returns the searched quality range for format f: the request's
// bounds (reqMin, reqMax; 0 = unset), else the per-format config, else the
// global config. When a request bound crosses a configured one, the
// configured one collapses onto it.
func (c *AutoQualityConfig) bounds(f transform.Format, reqMin, reqMax int) (lo, hi int) {
	pick := func(req int, perFormat map[transform.Format]int, global int) int {
		if req > 0 {
			return req
		}
		if v := perFormat[f]; v > 0 {
			return v
		}
		return global
	}
	lo, hi = pick(reqMin, c.FormatMin, c.Min), pick(reqMax, c.FormatMax, c.Max)
	if lo > hi {
		if reqMax > 0 && reqMin <= 0 {
			lo = hi
		} else {
			hi = lo
		}
	}
	return min(max(lo, 1), 100), min(max(hi, 1), 100)
}

// autoQuality resolves the auto-quality of t for a save whose default
// quality is quality (0: the save has no quality knob, so nothing runs).
// The request's autoquality overrides the config field by field; a config
// method (none set by the request) yields to an explicit request quality.
// ignored is set when a requested method cannot run.
func (c *AutoQualityConfig) autoQuality(t *Target, quality int) (p aqParams, ignored string) {
	req := t.AutoQuality
	if req == nil {
		req = &transform.AutoQuality{}
	}
	p.method = req.Method
	if p.method == transform.AutoQualityUnset {
		if t.Quality > 0 || t.FormatQuality[t.Format] > 0 {
			return aqParams{}, ""
		}
		p.method = c.Method
	}
	if quality == 0 {
		return aqParams{}, ""
	}
	switch p.method {
	case transform.AutoQualitySize, transform.AutoQualityDSSIM:
	case transform.AutoQualityML:
		return aqParams{}, IgnoredML
	default:
		return aqParams{}, ""
	}

	p.target = req.Target
	if p.target <= 0 {
		p.target = c.FormatTarget[t.Format]
	}
	if p.target <= 0 {
		p.target = c.Target
	}
	p.allowedError = req.AllowedError
	if p.allowedError <= 0 {
		p.allowedError = c.AllowedError
	}
	p.lo, p.hi = c.bounds(t.Format, req.Min, req.Max)

	if p.method == transform.AutoQualitySize {
		// A DSSIM-like target (< 1 byte) is no budget: use max_bytes, or
		// give up.
		if p.target < 1 {
			if t.MaxBytes <= 0 {
				return aqParams{}, IgnoredSizeTarget
			}
			p.target = float64(t.MaxBytes)
		}
		if t.MaxBytes > 0 {
			p.target = min(p.target, float64(t.MaxBytes))
		}
		p.target = math.Floor(p.target)
	}
	return p, ""
}

// logSlope is the assumed fall of ln(DSSIM) per quality step, used to aim the
// second save before two measurements allow interpolation. Measured on the
// fixture corpus it lies between 0.03 and 0.08 for JPEG, WebP and AVIF.
const logSlope = 0.05

// candidate is one save of a search.
type candidate struct {
	body Body
	q    int
	d    float64
}

func (c *candidate) release() {
	if c.body != nil {
		c.body.Release()
		c.body = nil
	}
}

// searchResult is the outcome of searchDSSIM.
type searchResult struct {
	candidate
	attempts int
	met      bool
}

// measureFunc returns the DSSIM of an encoded candidate against the
// pre-encode image.
type measureFunc func(ctx context.Context, b Body) (float64, error)

// searchDSSIM finds the lowest quality in p.lo..p.hi whose DSSIM is at most
// p.target, in at most MaxSaves saves. It starts at start (the quality the
// request would get without auto-quality), then brackets the target: from one
// side it extrapolates ln(DSSIM) with the slope of its last two measurements
// (logSlope before there are two), from both it interpolates ln(DSSIM) linearly
// between the closest failing and passing qualities (safeguarded towards the
// middle of the bracket so a skewed curve still shrinks it). A save whose
// DSSIM is within p.allowedError of the target ends the search at once.
//
// When no save passes, the highest-quality attempt is returned unmet: the
// search stops early when p.hi itself fails. Bodies that are not returned
// are released.
func searchDSSIM(ctx context.Context, p aqParams, start int, save saveFunc, measure measureFunc) (searchResult, error) {
	var fail, pass candidate // highest failing and lowest passing quality
	cleanup := func() {
		fail.release()
		pass.release()
	}
	q := min(max(start, p.lo), p.hi)
	attempts := 0
	var seen []point
	for attempts < MaxSaves {
		if err := ctx.Err(); err != nil {
			cleanup()
			return searchResult{}, err
		}
		body, err := save(ctx, q)
		if err != nil {
			cleanup()
			return searchResult{}, err
		}
		attempts++
		d, err := measure(ctx, body)
		if err != nil {
			body.Release()
			cleanup()
			return searchResult{}, err
		}
		c := candidate{body: body, q: q, d: d}
		seen = append(seen, point{q: q, d: d})
		switch {
		case math.Abs(d-p.target) <= p.allowedError:
			cleanup()
			return searchResult{candidate: c, attempts: attempts, met: true}, nil
		case d > p.target:
			if fail.body == nil || q > fail.q {
				fail.release()
				fail = c
			} else {
				c.release()
			}
		default:
			if pass.body == nil || q < pass.q {
				pass.release()
				pass = c
			} else {
				c.release()
			}
		}
		next, ok := nextQuality(p, &fail, &pass, seen)
		if !ok {
			break
		}
		q = next
	}
	if pass.body != nil {
		fail.release()
		return searchResult{candidate: pass, attempts: attempts, met: true}, nil
	}
	return searchResult{candidate: fail, attempts: attempts}, nil
}

// point is one measurement of a search.
type point struct {
	q int
	d float64
}

// nextQuality picks the next quality to save, or reports that the bracket
// is closed (no untried quality can improve the result). seen holds every
// measurement so far.
func nextQuality(p aqParams, fail, pass *candidate, seen []point) (int, bool) {
	low, high := p.lo-1, p.hi+1 // exclusive bounds of the untried range
	if fail.body != nil {
		low = fail.q
	}
	if pass.body != nil {
		high = pass.q
	}
	span := high - low
	if span <= 1 {
		return 0, false
	}
	lt := lnD(p.target)
	var q float64
	margin := 1
	switch {
	case fail.body != nil && pass.body != nil:
		lf, lp := lnD(fail.d), lnD(pass.d)
		frac := 0.5
		if lf > lp {
			frac = (lf - lt) / (lf - lp)
		}
		q = float64(fail.q) + frac*float64(pass.q-fail.q)
		margin = max(1, span/6)
	case fail.body != nil:
		q = float64(fail.q) + (lnD(fail.d)-lt)/slope(seen, fail.q)
	default:
		q = float64(pass.q) - (lt-lnD(pass.d))/slope(seen, pass.q)
	}
	next := int(math.Round(q))
	lo, hi := low+margin, high-margin
	if lo > hi {
		lo, hi = low+1, high-1
	}
	return min(max(next, lo), hi), true
}

func lnD(d float64) float64 { return math.Log(max(d, 1e-9)) }

// slope estimates the fall of ln(DSSIM) per quality step near quality at:
// from at and the measurement closest to it when the two agree with a
// falling curve, else logSlope. The estimate is clamped to a plausible range
// so one noisy pair cannot send the search to the end of the range.
func slope(seen []point, at int) float64 {
	var a, b *point
	for i := range seen {
		switch {
		case seen[i].q == at:
			a = &seen[i]
		case b == nil || abs(seen[i].q-at) < abs(b.q-at):
			b = &seen[i]
		}
	}
	if a == nil || b == nil {
		return logSlope
	}
	s := (lnD(b.d) - lnD(a.d)) / float64(a.q-b.q)
	if !(s > 0) {
		return logSlope
	}
	return min(max(s, 0.01), 0.2)
}

func abs(x int) int { return max(x, -x) }
