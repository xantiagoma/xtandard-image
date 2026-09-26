package server

import (
	"net/http"
	"strconv"

	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Client-hint response headers (WP-65). Static values: go-standards §2.
const (
	// acceptCH advertises the hints we read. Only the Sec-CH- names are
	// requested: Chromium sends the legacy DPR/Width/Viewport-Width names
	// only for pages that still ask for them, and we read both anyway.
	// Save-Data is a low-entropy hint browsers send without opt-in.
	acceptCH = "Sec-CH-DPR, Sec-CH-Width, Sec-CH-Viewport-Width, ECT" //nolint:misspell // ECT is the connection-type hint header
	// criticalCH asks a navigating browser to retry with the resize hints
	// (only honoured on navigation responses, e.g. an image opened
	// directly).
	criticalCH = "Sec-CH-DPR, Sec-CH-Width, Sec-CH-Viewport-Width"
)

// ClientHintHeaders configures the client-hint response headers.
type ClientHintHeaders struct {
	// CriticalCH adds Critical-CH next to Accept-CH
	// (XI_CLIENT_HINTS_CRITICAL_CH).
	CriticalCH bool
}

// resolveHints is stage 6's client-hint step: it turns the request's hint
// headers into concrete resize and quality values (negotiate.ApplyClientHints)
// before defaults and the canonical key, so hinted results get their own
// cache key and ETag. An unsigned request on a restricted mount gets only the
// Save-Data hint from the server-wide switch: a hinted width or DPR would
// bypass the policy's allowlists (the router already rejects URLs that opt
// into those hints themselves).
func (s *Server) resolveHints(r *http.Request, req *transform.Request, mount *router.Mount, signed bool) negotiate.HintResult {
	scope := negotiate.HintsAll
	// Fail closed: a missing mount or an unresolved ("") policy counts as
	// restricted, the router's default.
	if !signed && (mount == nil || mount.Policy == router.PolicyRestricted || mount.Policy == "") {
		scope = negotiate.HintsSaveDataOnly
	}
	return negotiate.ApplyClientHints(req, negotiate.ParseClientHints(r.Header), &s.cfg.Negotiate, scope)
}

// setHintHeaders writes Content-DPR, Accept-CH and Critical-CH on an image
// response (200, HEAD and 304 alike). Accept-CH is sent whenever hints are
// enabled server-wide or the URL opted into one, including on unprocessed
// responses: browsers only act on it for navigations, so it mainly documents
// the contract; pages embedding cross-origin images must send Accept-CH and
// a Permissions-Policy delegation (ch-dpr, ch-width, ch-viewport-width) for
// this origin themselves (docs/CONFIG.md).
func (s *Server) setHintHeaders(h http.Header, res *imageResult) {
	if dpr := res.hints.ContentDPR; dpr != 0 && dpr != 1 {
		h.Set("Content-Dpr", strconv.FormatFloat(dpr, 'f', -1, 64))
	}
	ch := res.req.ClientHints
	if !s.cfg.Negotiate.EnableClientHints && !ch.DPR && !ch.Width && !ch.SaveData {
		return
	}
	h.Set("Accept-CH", acceptCH)
	if s.cfg.ClientHints.CriticalCH {
		h.Set("Critical-Ch", criticalCH)
	}
}
