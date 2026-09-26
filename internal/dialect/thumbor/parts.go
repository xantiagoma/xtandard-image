// Grammar from libthumbor url.py (Url.regex) and thumbor context.py
// (RequestParameters), MIT. Copyright xtandard-image contributors, MIT.

package thumbor

import (
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/thumbor/filters"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// size is one requested dimension.
type size struct {
	px   int
	orig bool // "orig": the source dimension
}

// parts is a thumbor path split into its grammar segments. Everything is
// still escaped except the parsed numbers.
type parts struct {
	debug, meta bool

	trim            bool
	trimBottomRight bool
	trimTolerance   int

	crop    bool
	cropBox [4]int // left, top, right, bottom

	fitIn, full, adaptive bool

	dims          bool
	flipH, flipV  bool
	width, height size

	halign, valign string
	smart          bool

	filters []filters.Filter
	image   string
}

// parseParts splits the path after the signature segment. Each optional
// segment is consumed only when a non-empty remainder follows it, so the
// image is never empty unless the path is.
func parseParts(rest string) (*parts, error) {
	u := &parts{}
	// next returns the leading segment when something follows it.
	next := func() (string, string, bool) {
		seg, tail, ok := strings.Cut(rest, "/")
		return seg, tail, ok && tail != ""
	}
	if seg, tail, ok := next(); ok && seg == "debug" {
		u.debug, rest = true, tail
	}
	if seg, tail, ok := next(); ok && seg == "meta" {
		u.meta, rest = true, tail
	}
	if seg, tail, ok := next(); ok {
		if matched, err := u.parseTrim(seg); err != nil {
			return nil, err
		} else if matched {
			rest = tail
		}
	}
	if seg, tail, ok := next(); ok {
		if matched, err := u.parseCrop(seg); err != nil {
			return nil, err
		} else if matched {
			rest = tail
		}
	}
	if seg, tail, ok := next(); ok && u.parseFitIn(seg) {
		rest = tail
	}
	if seg, tail, ok := next(); ok {
		if matched, err := u.parseDims(seg); err != nil {
			return nil, err
		} else if matched {
			rest = tail
		}
	}
	if seg, tail, ok := next(); ok && (seg == "left" || seg == "right" || seg == "center") {
		u.halign, rest = seg, tail
	}
	if seg, tail, ok := next(); ok && (seg == "top" || seg == "bottom" || seg == "middle") {
		u.valign, rest = seg, tail
	}
	if seg, tail, ok := next(); ok && seg == "smart" {
		u.smart, rest = true, tail
	}
	if list, tail, ok := filters.Cut(rest); ok && tail != "" {
		fs, err := filters.Parse(list)
		if err != nil {
			return nil, dialect.InvalidURL("malformed filters")
		}
		u.filters, rest = fs, tail
	}
	if rest == "" {
		return nil, dialect.InvalidURL("missing image")
	}
	u.image = rest
	return u, nil
}

// parseTrim matches trim(:top-left|:bottom-right)?(:\d+)?.
func (u *parts) parseTrim(seg string) (bool, error) {
	body, ok := strings.CutPrefix(seg, "trim")
	if !ok || (body != "" && body[0] != ':') {
		return false, nil
	}
	var corner, tol string
	if body != "" {
		fields := strings.Split(body[1:], ":")
		switch {
		case len(fields) > 2:
			return false, nil
		case len(fields) == 2:
			corner, tol = fields[0], fields[1]
		case fields[0] == "top-left" || fields[0] == "bottom-right":
			corner = fields[0]
		default:
			tol = fields[0]
		}
		if corner != "" && corner != "top-left" && corner != "bottom-right" {
			return false, nil
		}
		if len(fields) == 2 && corner == "" {
			return false, nil
		}
	}
	if tol != "" {
		if !isDigits(tol) {
			return false, nil
		}
		n, err := parseUint("trim.tolerance", tol, 1<<16)
		if err != nil {
			return false, err
		}
		u.trimTolerance = n
	}
	u.trim, u.trimBottomRight = true, corner == "bottom-right"
	return true, nil
}

// parseCrop matches \d+x\d+:\d+x\d+.
func (u *parts) parseCrop(seg string) (bool, error) {
	a, b, ok := strings.Cut(seg, ":")
	if !ok {
		return false, nil
	}
	l, t, ok1 := cutPair(a)
	r, bt, ok2 := cutPair(b)
	if !ok1 || !ok2 {
		return false, nil
	}
	names := [4]string{"crop.left", "crop.top", "crop.right", "crop.bottom"}
	for i, s := range [4]string{l, t, r, bt} {
		n, err := parseUint(names[i], s, transform.MaxDimension)
		if err != nil {
			return false, err
		}
		u.cropBox[i] = n
	}
	u.crop = true
	return true, nil
}

// cutPair splits "AxB" into two digit strings.
func cutPair(s string) (string, string, bool) {
	a, b, ok := strings.Cut(s, "x")
	return a, b, ok && isDigits(a) && isDigits(b)
}

// parseFitIn matches (adaptive-)?(full-)?fit-in.
func (u *parts) parseFitIn(seg string) bool {
	s, adaptive := strings.CutPrefix(seg, "adaptive-")
	s, full := strings.CutPrefix(s, "full-")
	if s != "fit-in" {
		return false
	}
	u.fitIn, u.adaptive, u.full = true, adaptive, full
	return true
}

// parseDims matches (-)?(\d+|orig)?x(-)?(\d+|orig)?.
func (u *parts) parseDims(seg string) (bool, error) {
	w, h, ok := strings.Cut(seg, "x")
	if !ok {
		return false, nil
	}
	w, flipH := strings.CutPrefix(w, "-")
	h, flipV := strings.CutPrefix(h, "-")
	if !validSize(w) || !validSize(h) {
		return false, nil
	}
	width, err := parseSize("width", w)
	if err != nil {
		return false, err
	}
	height, err := parseSize("height", h)
	if err != nil {
		return false, err
	}
	u.dims, u.flipH, u.flipV, u.width, u.height = true, flipH, flipV, width, height
	return true, nil
}

func validSize(s string) bool { return s == "" || s == "orig" || isDigits(s) }

func parseSize(field, s string) (size, error) {
	switch s {
	case "":
		return size{}, nil
	case "orig":
		return size{orig: true}, nil
	}
	n, err := parseUint(field, s, transform.MaxDimension)
	return size{px: n}, err
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// parseUint parses a digit string no larger than maxV.
func parseUint(field, s string, maxV int) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > maxV {
		return 0, dialect.InvalidArgument(field, s, "an integer from 0 to "+strconv.Itoa(maxV))
	}
	return n, nil
}
