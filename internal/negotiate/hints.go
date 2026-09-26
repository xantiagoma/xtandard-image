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
	// From records which header name supplied each non-zero field, so that
	// ApplyClientHints varies on exactly the names that decided the result.
	// A legacy member (VaryDPR, VaryWidth, VaryViewportWidth) means the
	// Sec-CH- name was absent or invalid; a non-zero field without either
	// member is read as "from the Sec-CH- name".
	From VarySet
}

// hintName is one header name of a hint and its Vary member.
type hintName struct {
	name string
	m    VarySet
}

// ParseClientHints reads the client-hint headers. The Sec-CH- name wins over
// the legacy name; an invalid value is skipped in favour of the next name.
func ParseClientHints(h http.Header) ClientHints {
	var c ClientHints
	// Header names in precedence order: the Sec-CH- name wins.
	dprNames := [...]hintName{{"Sec-Ch-Dpr", VarySecCHDPR}, {"Dpr", VaryDPR}}
	widthNames := [...]hintName{{"Sec-Ch-Width", VarySecCHWidth}, {"Width", VaryWidth}}
	viewportNames := [...]hintName{{"Sec-Ch-Viewport-Width", VarySecCHViewportWidth}, {"Viewport-Width", VaryViewportWidth}}
	for _, n := range dprNames {
		if d, ok := parseDPR(h.Get(n.name)); ok {
			c.DPR, c.From = d, c.From|n.m
			break
		}
	}
	for _, n := range widthNames {
		if w, ok := parseWidth(h.Get(n.name)); ok {
			c.Width, c.From = w, c.From|n.m
			break
		}
	}
	for _, n := range viewportNames {
		if w, ok := parseWidth(h.Get(n.name)); ok {
			c.ViewportWidth, c.From = w, c.From|n.m
			break
		}
	}
	if strings.EqualFold(sfItem(h.Get("Save-Data")), "on") {
		c.SaveData, c.From = true, c.From|VarySaveData
	}
	switch strings.ToLower(sfItem(h.Get("Ect"))) { //nolint:misspell // canonical form of the ECT header
	case "slow-2g", "2g":
		c.SlowNetwork, c.From = true, c.From|VaryECT
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
	// Vary lists the hint headers the result depends on; OR it with
	// Decision.Vary. Only hints the URL left to the client are listed (a
	// URL dpr, width or quality is never overridden, so its hint never
	// varies), and within a hint only the names that decided it: a valid
	// Sec-CH- value makes the legacy name irrelevant, a Width hint makes
	// Viewport-Width irrelevant, and Save-Data: on makes the connection type irrelevant. A
	// hint that could apply but was absent still varies on its names,
	// because its presence would have changed the result (RFC 9110
	// §12.5.5: a shared cache must not serve the hint-less rendition to a
	// hinted client).
	Vary VarySet
	// ContentDPR is the DPR taken from a hint, 0 when none was applied. The
	// server sets Content-DPR when it is non-zero and not 1.
	ContentDPR float64
	// SlowConnection is true when Save-Data or the connection-type hint
	// asked for lighter output and the URL did not fix the quality. When the
	// URL gave Output.SlowConnectionQuality it has already been copied into
	// Output.Quality; otherwise QualityDelta must be applied by the encoder
	// and made part of the cache key (it is part of the ETag).
	SlowConnection bool
	QualityDelta   int
}

// HintScope selects which client hints ApplyClientHints may resolve.
type HintScope uint8

// Hint scopes.
const (
	// HintsAll resolves every hint the URL opts into or config enables.
	HintsAll HintScope = iota
	// HintsSaveDataOnly ignores the server-wide DPR and width hints. The
	// server passes it for unsigned requests under the restricted policy,
	// whose width and DPR allowlists a hinted value would bypass (the router
	// already rejects URLs that opt into width or DPR hints themselves).
	HintsSaveDataOnly
)

// ApplyClientHints resolves hints into req (ARCHITECTURE §4: the server
// replaces client hints with concrete Resize values before CanonicalKey). Call
// it after dialect parsing and before ApplyDefaults, so that a zero DPR still
// means "not set by the URL".
//
// A hint is consulted when the URL opts into it (req.ClientHints) or when
// cfg.EnableClientHints is on, scope is HintsAll and the URL left the value
// unset (imgproxy IMGPROXY_ENABLE_CLIENT_HINTS semantics: URL options win).
// Only the first pipeline is resized; it is created when the request has
// none and a hint sets a non-default value.
//
// Width: the physical-pixel Width hint is divided by the effective DPR (the
// URL's, else the DPR hint, else 1) to get CSS pixels, because the planner
// multiplies by DPR once; without a Width hint the Viewport-Width hint
// (already CSS pixels) is used. The result is rounded up to
// req.ClientHints.WidthStep and clamped to MaxDimension.
//
// Save-Data / connection type: when the URL did not fix the quality,
// Save-Data: on or a slow-2g/2g connection type selects the URL's slow-connection quality, else the
// config's quality delta. With neither (delta 0) the hint cannot change the
// result and is not consulted.
func ApplyClientHints(req *transform.Request, h ClientHints, cfg *Config, scope HintScope) HintResult {
	var res HintResult
	want := req.ClientHints
	global := cfg.EnableClientHints
	if !want.DPR && !want.Width && !want.SaveData && !global {
		return res
	}
	var rs transform.Resize
	if len(req.Pipelines) > 0 {
		rs = req.Pipelines[0].Resize
	}
	resizing := global && scope == HintsAll

	if want.DPR || (resizing && rs.DPR == 0) {
		res.Vary |= hintVary(h.From, VarySecCHDPR, VaryDPR, h.DPR > 0)
		if h.DPR > 0 {
			rs.DPR = h.DPR
			res.ContentDPR = h.DPR
		}
	}

	if want.Width || (resizing && rs.Width.IsZero()) {
		if h.Width > 0 {
			res.Vary |= hintVary(h.From, VarySecCHWidth, VaryWidth, true)
		} else {
			res.Vary |= VarySecCHWidth | VaryWidth |
				hintVary(h.From, VarySecCHViewportWidth, VaryViewportWidth, h.ViewportWidth > 0)
		}
		if css := hintedCSSWidth(h, rs.DPR, want.WidthStep); css > 0 {
			rs.Width = transform.Dim{Px: css}
		}
	}

	switch {
	case len(req.Pipelines) > 0:
		req.Pipelines[0].Resize = rs
	case !rs.Width.IsZero() || (rs.DPR != 0 && rs.DPR != 1):
		// A DPR hint of 1 alone equals the default: adding a pipeline for
		// it would change the canonical key but not the pixels.
		req.Pipelines = append(req.Pipelines, transform.Pipeline{Resize: rs})
	}

	scq := req.Output.SlowConnectionQuality
	if (want.SaveData || global) && req.Output.Quality == 0 && (scq > 0 || cfg.SaveDataQualityDelta != 0) {
		if h.SaveData {
			res.Vary |= VarySaveData
		} else {
			res.Vary |= VarySaveData | VaryECT
		}
		if h.SaveData || h.SlowNetwork {
			res.SlowConnection = true
			if scq > 0 {
				req.Output.Quality = scq
			} else {
				res.QualityDelta = cfg.SaveDataQualityDelta
			}
		}
	}
	return res
}

// hintVary returns the Vary members of one hint read as sec, else legacy:
// only sec when a valid Sec-CH- value decided it, else both names (the
// legacy name supplied it, or neither did and either could have).
func hintVary(from, sec, legacy VarySet, present bool) VarySet {
	if present && from&legacy == 0 {
		return sec
	}
	return sec | legacy
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
