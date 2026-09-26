package server

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// MaxDimension bounds requested widths and heights.
const MaxDimension = 16384

// imageRequest is what the WP-00 parser understands: fit the source inside
// Width×Height (0 = unbounded) and encode JPEG.
type imageRequest struct {
	Width, Height int
	Source        string // absolute http(s) URL
}

// parseImagePath is the temporary hard-coded parser of the walking skeleton
// (ROADMAP WP-00); internal/dialect/imgproxy replaces it. It accepts
//
//	/{insecure|unsafe}/rs:fit:{w}:{h}/plain/{url}
//	/{insecure|unsafe}/w:{w}/plain/{url}
//
// where url is an absolute http or https URL. Any other signature segment is
// KindUnauthorized (signatures are not verified yet); everything else that
// does not match is KindInvalidURL.
func parseImagePath(p string) (imageRequest, error) {
	var req imageRequest
	rest, ok := strings.CutPrefix(p, "/")
	if !ok {
		return req, errs.New(errs.KindInvalidURL, "path must start with /")
	}
	sig, rest, ok := strings.Cut(rest, "/")
	if !ok {
		return req, errs.New(errs.KindInvalidURL, "missing processing options")
	}
	if sig != "insecure" && sig != "unsafe" {
		return req, errs.New(errs.KindUnauthorized, "signed urls are not supported yet")
	}
	opts, rest, ok := strings.Cut(rest, "/")
	if !ok {
		return req, errs.New(errs.KindInvalidURL, "missing source")
	}
	var err error
	if req.Width, req.Height, err = parseSize(opts); err != nil {
		return req, err
	}
	src, ok := strings.CutPrefix(rest, "plain/")
	if !ok {
		return req, errs.New(errs.KindInvalidURL, "only plain sources are supported")
	}
	u, err := url.Parse(src)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return req, errs.New(errs.KindInvalidURL, "source must be an absolute http(s) url")
	}
	req.Source = u.String()
	return req, nil
}

func parseSize(opts string) (w, h int, err error) {
	if v, ok := strings.CutPrefix(opts, "rs:fit:"); ok {
		ws, hs, ok := strings.Cut(v, ":")
		if !ok {
			return 0, 0, errs.New(errs.KindInvalidURL, "rs:fit needs width and height")
		}
		if w, err = parseDim(ws); err != nil {
			return 0, 0, err
		}
		if h, err = parseDim(hs); err != nil {
			return 0, 0, err
		}
		return w, h, nil
	}
	if v, ok := strings.CutPrefix(opts, "w:"); ok {
		w, err = parseDim(v)
		return w, 0, err
	}
	return 0, 0, errs.New(errs.KindInvalidURL, "unsupported processing options")
}

func parseDim(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > MaxDimension {
		return 0, errs.New(errs.KindInvalidURL, "dimension must be an integer in [0, 16384]")
	}
	return n, nil
}
