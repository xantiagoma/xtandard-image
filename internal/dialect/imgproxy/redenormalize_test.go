package imgproxy

import (
	"regexp"
	"testing"
)

// imgproxyFixPathRe is imgproxy's handlers/path.go fixPathRe, the reference
// for collapsedScheme.
var imgproxyFixPathRe = regexp.MustCompile(`^(\S+)\:/([^/])`)

func referenceCollapsedScheme(s string) int {
	m := imgproxyFixPathRe.FindStringSubmatchIndex(s)
	if m == nil {
		return -1
	}
	return m[3]
}

func TestCollapsedSchemeMatchesImgproxyRegexp(t *testing.T) {
	t.Parallel()
	for _, s := range []string{
		"https:/example.com/a.jpg",
		"https://example.com/a.jpg",
		"local:/images/a.jpg",
		"s3:/bucket/x:/y",
		"a b:/c",
		":/x",
		"x:/",
		"x:/ ",
		"http:/e.com/p:/q r:/s",
		"",
	} {
		if got, want := collapsedScheme(s), referenceCollapsedScheme(s); got != want {
			t.Errorf("collapsedScheme(%q) = %d, regexp %d", s, got, want)
		}
	}
}

func FuzzCollapsedScheme(f *testing.F) {
	f.Add("https:/example.com/a.jpg")
	f.Add("s3:/b/x:/y z:/w")
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := collapsedScheme(s), referenceCollapsedScheme(s); got != want {
			t.Fatalf("collapsedScheme(%q) = %d, regexp %d", s, got, want)
		}
	})
}

func TestRedenormalizePath(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"rs:fit:1:1/plain/https:/e.com/a.jpg": "rs:fit:1:1/plain/https://e.com/a.jpg",
		"plain/local:/a.jpg":                  "plain/local:///a.jpg",
		"rs%3Afit/plain/x%3Ay":                "rs:fit/plain/x%3Ay",
		"rs%3Afit/aGk":                        "rs:fit/aGk",
		"/w:1/plain/https://e.com/a":          "w:1/plain/https://e.com/a",
	} {
		if got := redenormalizePath(in); got != want {
			t.Errorf("redenormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}
