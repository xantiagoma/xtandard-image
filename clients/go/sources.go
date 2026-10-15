package xtandard

import (
	"regexp"
	"strings"
	"unicode"
)

var xiName = regexp.MustCompile(XiNamePattern)

// XiSource returns a named source reference, xi://<name>/<key> (XI_SOURCES).
// The key may be relative or, for an http source, an absolute URL; leading
// slashes are dropped:
//
//	XiSource("photos", "2026/cat.jpg")                // xi://photos/2026/cat.jpg
//	XiSource("web", "https://cdn.example.com/a.jpg")  // xi://web/https://cdn.example.com/a.jpg
//
// A name that does not match [XiNamePattern] is an [ErrInvalidName] error.
// The helpers only join strings; the URL builders escape or encode the result.
func XiSource(name, key string) (string, error) {
	if !xiName.MatchString(name) {
		return "", Errorf(ErrInvalidName, "invalid source name %q", name)
	}
	return "xi://" + name + "/" + strings.TrimLeft(key, "/"), nil
}

// SourceURL returns a source reference for a scheme: the bucket, container,
// host or source name, then the key. local takes only a path:
//
//	SourceURL(SourceSchemeS3, "photos", "cat.jpg")     // s3://photos/cat.jpg
//	SourceURL(SourceSchemeLocal, "", "img/cat.jpg")    // local:///img/cat.jpg
//
// A missing authority, one with "/", "?", "#" or whitespace, and an authority
// for local are [ErrInvalidName] errors.
func SourceURL(scheme SourceScheme, authority, key string) (string, error) {
	key = strings.TrimLeft(key, "/")
	switch scheme {
	case SourceSchemeXi:
		return XiSource(authority, key)
	case SourceSchemeLocal:
		if authority != "" {
			return "", Errorf(ErrInvalidName, "local sources have no authority: local:///<path>")
		}
		return "local:///" + key, nil
	default:
	}
	if authority == "" || strings.ContainsFunc(authority, func(r rune) bool {
		return r == '/' || r == '?' || r == '#' || unicode.IsSpace(r)
	}) {
		return "", Errorf(ErrInvalidName, "invalid %s authority %q", scheme, authority)
	}
	return string(scheme) + "://" + authority + "/" + key, nil
}
