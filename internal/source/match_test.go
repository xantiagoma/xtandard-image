package source

import (
	"strings"
	"testing"
)

func TestMatchHost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		glob, host string
		want       bool
	}{
		{"*", "anything.example", true},
		{"cdn.example.com", "cdn.example.com", true},
		{"cdn.example.com", "cdn.example.com.evil.com", false},
		{"cdn.example.com", "xcdn.example.com", false},
		{"*.example.com", "a.example.com", true},
		{"*.example.com", "a.b.example.com", true},
		{"*.example.com", "example.com", false},
		{"*.example.com", "evilexample.com", false},
		{"*.example.com", "a.example.com.evil.com", false},
		{"img-*.example.com", "img-1.example.com", true},
		{"img-*.example.com", "img-.example.com", true},
		{"img-*.example.com", "img-1.a.example.com", false},
		{"img-*.example.com", "cdn.example.com", false},
		{"*.cdn-*.example.com", "a.cdn-eu.example.com", true},
		{"*.cdn-*.example.com", "a.cdn-eu.x.example.com", false},
	}
	for _, tc := range tests {
		if got := matchHost(tc.glob, tc.host); got != tc.want {
			t.Errorf("matchHost(%q, %q) = %v, want %v", tc.glob, tc.host, got, tc.want)
		}
	}
}

func TestCompileAllow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		pattern string
		want    allowRule
	}{
		{"cdn.example.com", allowRule{host: "cdn.example.com", prefix: "/"}},
		{"CDN.Example.com.", allowRule{host: "cdn.example.com", prefix: "/"}},
		{"cdn.example.com/img", allowRule{host: "cdn.example.com", prefix: "/img/"}},
		{"cdn.example.com/Img%20x/", allowRule{host: "cdn.example.com", prefix: "/Img x/"}},
		{"HTTPS://cdn.example.com:0443/", allowRule{scheme: SchemeHTTPS, host: "cdn.example.com", port: "443", prefix: "/"}},
		{"http://*.example.com:8080", allowRule{scheme: SchemeHTTP, host: "*.example.com", port: "8080", prefix: "/"}},
		{"[2001:db8::1]", allowRule{host: "2001:db8::1", prefix: "/"}},
		{"*", allowRule{host: "*", prefix: "/"}},
	}
	for _, tc := range tests {
		got, err := compileAllow(tc.pattern)
		if err != nil {
			t.Errorf("compileAllow(%q): %v", tc.pattern, err)
			continue
		}
		if got != tc.want {
			t.Errorf("compileAllow(%q) = %+v, want %+v", tc.pattern, got, tc.want)
		}
	}
}

func TestAllowRuleIPv6(t *testing.T) {
	t.Parallel()
	a, err := compileAllow("[2001:DB8::1]:8080/x/")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := ParseRef("http://[2001:db8:0::1]:8080/x/a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if !a.matchURL(&ref) {
		t.Errorf("%+v does not match %+v", a, ref)
	}
}

func TestRedactKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		key  Key
		want string
	}{
		{Key{Source: "origin", Path: "https://cdn.example.com/a.jpg?token=secret"}, "origin:https://cdn.example.com/a.jpg?REDACTED"},
		{Key{Source: "origin", Path: "https://cdn.example.com/a.jpg"}, "origin:https://cdn.example.com/a.jpg"},
		{Key{Source: "assets", Bucket: "b", Path: "k?x.jpg", Version: "3"}, "assets:b/k?x.jpg#v=3"},
		{Key{Source: DevSourceName, Path: "http://x.test/?a=b", Dev: true}, "_dev:http://x.test/?REDACTED"},
	}
	for _, tc := range tests {
		if got := RedactKey(tc.key); got != tc.want {
			t.Errorf("RedactKey(%+v) = %q, want %q", tc.key, got, tc.want)
		}
	}
	long := RedactKey(Key{Source: "s", Path: strings.Repeat("a", 2000)})
	if len(long) != maxRedactedLen+3 {
		t.Errorf("long key not capped: len %d", len(long))
	}
}
