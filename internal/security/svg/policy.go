package svg

import (
	"encoding/xml"
	"strings"
)

// isDroppedElement reports elements that are removed together with their
// whole subtree, matched on the local name regardless of namespace prefix and
// ASCII case. Besides script and foreignObject this covers SVG Tiny's
// handler/listener and the XHTML embedding and navigation elements that are
// reachable through a namespace prefix in an SVG document.
func isDroppedElement(local string) bool {
	switch strings.ToLower(local) {
	case "script", "handler", "listener", "foreignobject",
		"iframe", "frame", "frameset", "embed", "object", "applet",
		"base", "link", "meta", "form", "audio", "video":
		return true
	}
	return false
}

// isAnimationElement reports elements that can change another attribute's
// value at run time.
func isAnimationElement(local string) bool {
	switch strings.ToLower(local) {
	case "animate", "set", "animatetransform", "animatemotion", "animatecolor":
		return true
	}
	return false
}

func dropElement(t xml.StartElement) bool {
	local := t.Name.Local
	if isDroppedElement(local) {
		return true
	}
	switch {
	case isAnimationElement(local):
		// <set attributeName="href" to="javascript:..."/> and friends.
		for _, a := range t.Attr {
			if !strings.EqualFold(a.Name.Local, "attributeName") {
				continue
			}
			if target := localPart(strings.TrimSpace(a.Value)); isEventAttr(target) ||
				strings.EqualFold(target, "href") || strings.EqualFold(target, "base") {
				return true
			}
		}
	case strings.EqualFold(local, "use"):
		// <use> can pull in a whole external document; only same-document
		// references are acceptable, never data: URIs.
		for _, a := range t.Attr {
			if isHrefAttr(a.Name) && !isFragment(a.Value) {
				return true
			}
		}
	}
	return false
}

// sanitizeAttr decides whether an attribute survives and returns its possibly
// rewritten value.
func sanitizeAttr(a xml.Attr, allowData bool) (string, bool) {
	if a.Name.Space == "xmlns" || (a.Name.Space == "" && a.Name.Local == "xmlns") {
		// Namespace declarations: their values are identifiers, not fetched.
		return a.Value, true
	}
	local := a.Name.Local
	switch {
	case isEventAttr(local):
		return "", false
	case strings.EqualFold(local, "base") && strings.EqualFold(a.Name.Space, "xml"):
		// xml:base would make "#frag" references resolve to another document.
		return "", false
	case isHrefAttr(a.Name):
		return a.Value, isFragment(a.Value) || (allowData && isDataImage(a.Value))
	}
	if !strings.ContainsAny(a.Value, `(@\`) {
		return a.Value, true
	}
	// Presentation attributes and style="" are CSS: fill="url(https://…)".
	out, verdict := sanitizeCSS(a.Value, allowData)
	switch verdict {
	case cssRewritten:
		return out, true
	case cssRejected:
		return "", false
	case cssUnchanged:
	}
	return a.Value, true
}

func isEventAttr(local string) bool {
	return len(local) > 2 && strings.EqualFold(local[:2], "on")
}

func isHrefAttr(n xml.Name) bool {
	return strings.EqualFold(n.Local, "href") && n.Space != "xmlns"
}

// isFragment reports whether a reference stays inside the document.
func isFragment(v string) bool {
	v = trimURL(v)
	return v != "" && v[0] == '#'
}

// isDataImage reports whether v is a data: URI with a raster image media type.
// SVG data URIs are refused because their content would bypass sanitisation.
func isDataImage(v string) bool {
	v = trimURL(v)
	const prefix = "data:image/"
	if len(v) < len(prefix) || !strings.EqualFold(v[:len(prefix)], prefix) {
		return false
	}
	mime := v[len(prefix):]
	if i := strings.IndexAny(mime, ";,"); i >= 0 {
		mime = mime[:i]
	}
	mime = strings.TrimSpace(mime)
	return mime != "" && !strings.HasPrefix(strings.ToLower(mime), "svg")
}

// trimURL prepares a URL for the checks above the way the WHATWG URL parser
// would: leading and trailing C0 controls and spaces are stripped and ASCII
// tab and newline characters are removed everywhere. Unicode spaces are kept,
// since the parser keeps them (" #x" is a relative URL, not a fragment).
func trimURL(v string) string {
	v = strings.TrimFunc(v, func(r rune) bool { return r <= ' ' })
	if strings.ContainsAny(v, "\t\n\r") {
		v = strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(v)
	}
	return v
}

func localPart(name string) string {
	if _, after, ok := strings.Cut(name, ":"); ok {
		return after
	}
	return name
}
