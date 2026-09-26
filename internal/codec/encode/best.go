package encode

import (
	"context"
	"image"
	"strconv"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// BestSkippedPixels is BestResult.Skipped when the image is larger than
// BestConfig.MaxPixels: the negotiated format was encoded instead.
const BestSkippedPixels = "best_max_pixels"

// BestResult reports one EncodeBest.
type BestResult struct {
	// Skipped names why no selection ran (BestSkippedPixels); empty when
	// it did.
	Skipped string
	// Analysis is the content analysis of the processed image (first
	// frame); zero when skipped.
	Analysis negotiate.Analysis
	// Tries lists every candidate in the order it was encoded.
	Tries []BestTry
	// Chosen is the index in Tries of the returned output; -1 when
	// skipped.
	Chosen int
}

// BestTry is one encoded best-format candidate.
type BestTry struct {
	Candidate negotiate.Candidate
	// Quality is the quality of the save (0 without one: lossless, PNG,
	// GIF).
	Quality int
	// Bytes is the output size; 0 when Unsupported.
	Bytes int
	// Met reports that the output meets the quality bar: the auto-quality
	// target when a search ran, else always (the configured quality or a
	// lossless save), and the max_bytes budget when one is set.
	Met bool
	// Unsupported is set when this build cannot encode the candidate
	// (errs.KindUnsupported, e.g. HEIC without XI_FORMATS_HEIC_ENCODE); it
	// is skipped.
	Unsupported bool
}

// EncodeBest encodes img as the best format of d (a negotiate.ReasonBest
// decision; FORMATS §6.3): it classifies the processed image
// (negotiate.Analyze on a nearest-neighbour copy of the first frame of at
// most negotiate.AnalysisMaxDimension px), encodes every candidate of
// d.BestCandidates for that class with t's quality settings (and its
// auto-quality search when one applies), and returns the smallest output
// that meets the quality bar (BestTry.Met), else the smallest one. Ties go
// to the earlier (preferred) candidate. Above Config.Best.MaxPixels output
// pixels (all frames) nothing is analysed and d.Format is encoded as Encode
// would. Result.Best reports the analysis and every try; Result.Attempts
// counts the saves of all candidates.
//
// The image is rendered to memory once, so every candidate only encodes.
func (e *Encoder) EncodeBest(ctx context.Context, img *vips.Image, t *Target, d *negotiate.Decision) (*Result, error) {
	frames := 1
	if t.Animated {
		frames = img.Pages()
	}
	pixels := int64(img.Width()) * int64(img.PageHeight()) * int64(frames)
	if limit := e.cfg.Best.MaxPixels; limit > 0 && pixels > limit {
		res, err := e.Encode(ctx, img, t)
		if err != nil {
			return nil, err
		}
		res.Best = &BestResult{Skipped: BestSkippedPixels, Chosen: -1}
		return res, nil
	}

	var a vips.Arena
	defer a.Close()
	mem, err := a.Add(img.CopyMemory(ctx))
	if err != nil {
		return nil, err
	}
	an, err := analyze(ctx, mem, e.cfg.Best.ComplexityThreshold)
	if err != nil {
		return nil, err
	}
	cands := d.BestCandidates(an.Class, an.Colors)
	if len(cands) == 0 {
		cands = []negotiate.Candidate{{Format: d.Format, Lossless: d.Lossless, Quantize: d.Quantize}}
	}

	br := &BestResult{Analysis: an, Tries: make([]BestTry, 0, len(cands)), Chosen: -1}
	var best *Result
	attempts := 0
	for _, c := range cands {
		if err := ctx.Err(); err != nil {
			releaseResult(best)
			return nil, errs.Wrap(errs.KindOf(err), context.Cause(ctx))
		}
		tc := *t
		tc.Format = c.Format
		tc.Options.PreferLossless = c.Lossless
		tc.Options.Quantize = c.Quantize
		res, err := e.Encode(ctx, mem, &tc)
		if err != nil {
			if errs.KindOf(err) == errs.KindUnsupported {
				br.Tries = append(br.Tries, BestTry{Candidate: c, Unsupported: true})
				continue
			}
			releaseResult(best)
			return nil, err
		}
		attempts += res.Attempts
		try := BestTry{
			Candidate: c,
			Quality:   res.Quality,
			Bytes:     res.Blob.Len(),
			Met:       (res.AutoQuality.Method == transform.AutoQualityUnset || res.AutoQuality.Met) && !res.OverBudget,
		}
		br.Tries = append(br.Tries, try)
		if best == nil || better(&try, &br.Tries[br.Chosen]) {
			releaseResult(best)
			best, br.Chosen = res, len(br.Tries)-1
			continue
		}
		releaseResult(res)
	}
	if best == nil {
		return nil, errs.New(errs.KindUnsupported, "encode: no best-format candidate is encodable")
	}
	best.Attempts = attempts
	best.Best = br
	return best, nil
}

// String summarises the selection for the X-Xi-Best debug header, e.g.
// "graphic c=1.83 colors=5; webp~=246*, png~=386": the class, complexity
// and colour count, then every try as format (~ lossless, q<n> its
// quality), "=" its size, "!" a try that missed the quality bar, "*" the
// chosen one, "=unsupported" a candidate this build cannot encode. A
// skipped selection reads "skipped=<reason>".
func (b *BestResult) String() string {
	if b == nil {
		return ""
	}
	if b.Skipped != "" {
		return "skipped=" + b.Skipped
	}
	out := make([]byte, 0, 96)
	out = append(out, b.Analysis.Class.String()...)
	out = append(out, " c="...)
	out = strconv.AppendFloat(out, b.Analysis.Complexity, 'f', 2, 64)
	out = append(out, " colors="...)
	out = strconv.AppendInt(out, int64(b.Analysis.Colors), 10)
	out = append(out, ';')
	for i, t := range b.Tries {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, ' ')
		out = append(out, t.Candidate.Format.String()...)
		if t.Candidate.Lossless {
			out = append(out, '~')
		}
		if t.Quality > 0 {
			out = append(out, " q"...)
			out = strconv.AppendInt(out, int64(t.Quality), 10)
		}
		if t.Unsupported {
			out = append(out, "=unsupported"...)
			continue
		}
		out = append(out, '=')
		out = strconv.AppendInt(out, int64(t.Bytes), 10)
		if !t.Met {
			out = append(out, '!')
		}
		if i == b.Chosen {
			out = append(out, '*')
		}
	}
	return string(out)
}

// better reports whether try a beats the current choice b: meeting the
// quality bar first, then fewer bytes.
func better(a, b *BestTry) bool {
	if a.Met != b.Met {
		return a.Met
	}
	return a.Bytes < b.Bytes
}

func releaseResult(r *Result) {
	if r != nil && r.Blob != nil {
		r.Blob.Release()
	}
}

// analyze classifies the first frame of img: 8-bit sRGB (alpha kept),
// reduced by nearest neighbour to at most negotiate.AnalysisMaxDimension so
// no colour is invented.
func analyze(ctx context.Context, img *vips.Image, threshold float64) (negotiate.Analysis, error) {
	if threshold <= 0 {
		return negotiate.Analysis{Class: negotiate.ContentPhoto}, nil
	}
	var a vips.Arena
	defer a.Close()
	var err error
	if img.Pages() > 1 {
		if img, err = a.Add(img.ExtractArea(ctx, image.Rect(0, 0, img.Width(), img.PageHeight()))); err != nil {
			return negotiate.Analysis{}, err
		}
	}
	if longest := max(img.Width(), img.Height()); longest > negotiate.AnalysisMaxDimension {
		scale := float64(negotiate.AnalysisMaxDimension) / float64(longest)
		if img, err = a.Add(img.Resize(ctx, scale, 0, vips.KernelNearest)); err != nil {
			return negotiate.Analysis{}, err
		}
	}
	if img.Interpretation() != vips.InterpretationSRGB {
		if img, err = a.Add(img.Colourspace(ctx, vips.InterpretationSRGB)); err != nil {
			return negotiate.Analysis{}, err
		}
	}
	if img.BandFormat() != vips.BandFormatUchar {
		if img, err = a.Add(img.Cast(ctx, vips.BandFormatUchar)); err != nil {
			return negotiate.Analysis{}, err
		}
	}
	if img.Bands() > 4 {
		if img, err = a.Add(img.ExtractBand(ctx, 0, 4)); err != nil {
			return negotiate.Analysis{}, err
		}
	}
	pix, err := img.WriteToMemory(ctx)
	if err != nil {
		return negotiate.Analysis{}, err
	}
	an, err := negotiate.Analyze(pix, img.Width(), img.Height(), img.Bands(), threshold)
	if err != nil {
		return negotiate.Analysis{}, errs.Wrap(errs.KindInternal, err)
	}
	return an, nil
}
