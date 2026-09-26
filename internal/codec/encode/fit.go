package encode

import "context"

// MaxSaves bounds the saves of one max_bytes search (ARCHITECTURE §8.3).
const MaxSaves = 6

// saveFunc encodes the image at quality q.
type saveFunc func(ctx context.Context, q int) (Body, error)

// fitResult is the outcome of fitBytes.
type fitResult struct {
	body       Body
	quality    int
	attempts   int
	overBudget bool
}

// fitBytes finds the largest quality in 1..start whose output is at most
// limit bytes, with at most MaxSaves saves: start first (most requests fit),
// then a binary search below it. When no attempt fits it returns the smallest
// output seen, flagged overBudget. Bodies that are not returned are released.
func fitBytes(ctx context.Context, start, limit int, save saveFunc) (fitResult, error) {
	var best, smallest fitResult // best: largest fitting quality
	keep := func(r fitResult) {
		if best.body != nil && best.body != r.body {
			best.body.Release()
		}
		best = r
	}
	track := func(r fitResult) {
		if smallest.body == nil || r.body.Len() < smallest.body.Len() {
			if smallest.body != nil {
				smallest.body.Release()
			}
			smallest = r
			return
		}
		r.body.Release()
	}
	release := func() {
		if best.body != nil {
			best.body.Release()
		}
		if smallest.body != nil {
			smallest.body.Release()
		}
	}

	low, high := 1, start
	q := start
	attempts := 0
	for attempts < MaxSaves && low <= high {
		if err := ctx.Err(); err != nil {
			release()
			return fitResult{}, err
		}
		body, err := save(ctx, q)
		if err != nil {
			release()
			return fitResult{}, err
		}
		attempts++
		r := fitResult{body: body, quality: q}
		if body.Len() <= limit {
			keep(r)
			low = q + 1
		} else {
			track(r)
			high = q - 1
		}
		q = low + (high-low)/2
	}

	if best.body != nil {
		if smallest.body != nil {
			smallest.body.Release()
		}
		best.attempts = attempts
		return best, nil
	}
	smallest.attempts = attempts
	smallest.overBudget = true
	return smallest, nil
}
