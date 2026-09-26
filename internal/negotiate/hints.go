package negotiate

import (
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// headerECT is the Network Information "effective connection type" hint.
const headerECT = "ECT" //nolint:misspell // header name, not a typo of "etc"

// MaxHintDPR is the largest DPR accepted from a client hint (imgproxy's
// maxClientHintDPR). Larger or non-positive values are ignored.
const MaxHintDPR = 8

// ClientHints are the request's client-hint headers after validation. Zero
// fields mean "absent or invalid".
type ClientHints struct {
	DPR           float64 // Sec-CH-DPR, else DPR; (0, MaxHintDPR]
	Width         int     // Sec-CH-Width, else Width; physical px, 1..transform.MaxDimension
	ViewportWidth int     // Sec-CH-Viewport-Width, else Viewport-Width; CSS px
	SaveData      bool    // Save-Data: on
	SlowNetwork   bool    // effective connection type slow-2g | 2g
}

// ParseClientHints reads the client-hint headers. The Sec-CH- name wins over
// the legacy name; an invalid value is skipped in favour of the next name.
func ParseClientHints(h http.Header) ClientHints {
	var c ClientHints
	for _, name := range [...]string{"Sec-Ch-Dpr", "Dpr"} {
		if d, ok := parseDPR(h.Get(name)); ok {
			c.DPR = d
			break
		}
	}
	for _, name := range [...]string{"Sec-Ch-Width", "Width"} {
		if w, ok := parseWidth(h.Get(name)); ok {
			c.Width = w
			break
		}
	}
	for _, name := range [...]string{"Sec-Ch-Viewport-Width", "Viewport-Width"} {
		if w, ok := parseWidth(h.Get(name)); ok {
			c.ViewportWidth = w
			break
		}
	}
	c.SaveData = strings.EqualFold(sfItem(h.Get("Save-Data")), "on")
	switch strings.ToLower(sfItem(h.Get("Ect"))) { //nolint:misspell // canonical form of the ECT header

	case "slow-2g", "2g":
		c.SlowNetwork = true
	}
	return c
}

// sfItem returns the bare item of a structured-field value: the text before
// any parameter, trimmed, with surrounding quotes removed.
func sfItem(v string) string {
	v, _, _ = strings.Cut(v, ";")
	v = trimOWS(v)
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	return v
}

func parseDPR(v string) (float64, bool) {
	v = sfItem(v)
	if v == "" {
		return 0, false
	}
	d, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(d) || d <= 0 || d > MaxHintDPR {
		return 0, false
	}
	return d, true
}

func parseWidth(v string) (int, bool) {
	v = sfItem(v)
	if v == "" {
		return 0, false
	}
	w, err := strconv.Atoi(v)
	if err != nil || w <= 0 || w > transform.MaxDimension {
		return 0, false
	}
	return w, true
}

// HintResult is what ApplyClientHints did.
type HintResult struct {
	// Vary lists the hint headers that were consulted. OR it with
	// Decision.Vary. A consulted hint varies even when it was absent, because
	// its presence would have changed the result.
	Vary VarySet
	// ContentDPR is the DPR taken from a hint, 0 when none was applied. The
	// server sets Content-DPR when it is non-zero and not 1.
	ContentDPR float64
	// SlowConnection is true when Save-Data or the connection-type hint asked for lighter output
	// and the URL did not fix the quality. When the URL gave
	// Output.SlowConnectionQuality it has already been copied into
	// Output.Quality; otherwise QualityDelta must be applied by the encoder
	// and made part of the cache key.
	SlowConnection bool
	QualityDelta   int
}

// ApplyClientHints resolves hints into req (ARCHITECTURE §4: the server
// replaces client hints with concrete Resize values before CanonicalKey). Call
// it after dialect parsing and before ApplyDefaults, so that a zero DPR still
// means "not set by the URL".
//
// A hint is consulted when the URL opts into it (req.ClientHints) or when
// cfg.EnableClientHints is on and the URL left the value unset (imgproxy
// IMGPROXY_ENABLE_CLIENT_HINTS semantics: URL options win). Only the first
// pipeline is resized; it is created when the request has none.
//
// Width: the physical-pixel Width hint is divided by the effective DPR to get
// CSS pixels, because the planner multiplies by DPR once; without a Width
// hint the Viewport-Width hint (already CSS pixels) is used. The result is
// rounded up to req.ClientHints.WidthStep and clamped to MaxDimension.
func ApplyClientHints(req *transform.Request, h ClientHints, cfg *Config) HintResult {
	var res HintResult
	want := req.ClientHints
	if !want.DPR && !want.Width && !want.SaveData && !cfg.EnableClientHints {
		return res
	}
	if len(req.Pipelines) == 0 {
		req.Pipelines = append(req.Pipelines, transform.Pipeline{})
	}
	rs := &req.Pipelines[0].Resize

	if want.DPR || (cfg.EnableClientHints && rs.DPR == 0) {
		res.Vary |= varyDPRGroup
		if h.DPR > 0 {
			rs.DPR = h.DPR
			res.ContentDPR = h.DPR
		}
	}

	if want.Width || (cfg.EnableClientHints && rs.Width.IsZero()) {
		res.Vary |= varyWidthGroup
		if css := hintedCSSWidth(h, rs.DPR, want.WidthStep); css > 0 {
			rs.Width = transform.Dim{Px: css}
		}
	}

	if (want.SaveData || cfg.EnableClientHints) && req.Output.Quality == 0 {
		res.Vary |= varySaveDataGroup
		if h.SaveData || h.SlowNetwork {
			res.SlowConnection = true
			if q := req.Output.SlowConnectionQuality; q > 0 {
				req.Output.Quality = q
			} else {
				res.QualityDelta = cfg.SaveDataQualityDelta
			}
		}
	}
	return res
}

func hintedCSSWidth(h ClientHints, dpr float64, step int) int {
	var css int
	switch {
	case h.Width > 0:
		if dpr <= 0 {
			dpr = 1
		}
		css = max(1, int(math.Round(float64(h.Width)/dpr)))
	case h.ViewportWidth > 0:
		css = h.ViewportWidth
	default:
		return 0
	}
	if step > 1 {
		css = (css + step - 1) / step * step
	}
	return min(css, transform.MaxDimension)
}
