package router_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
)

func TestMounts(t *testing.T) {
	t.Parallel()
	_, ds := fakes("imgproxy", "query")
	rt := newRouter(t, &router.Options{
		Policy: router.PolicyDeny,
		Mounts: []router.Mount{
			{Name: "imgproxy", Dialect: "imgproxy", Prefix: "/"},
			{Name: "query", Dialect: "query", Prefix: "/q/", Hosts: []string{"q.example.com"}, Policy: router.PolicyAllow},
		},
		Default: router.Mount{Dialect: "imgproxy"},
	}, ds)
	want := []router.Mount{
		{Name: "imgproxy", Dialect: "imgproxy", Prefix: "/", Policy: router.PolicyDeny},
		{Name: "query", Dialect: "query", Prefix: "/q/", Hosts: []string{"q.example.com"}, Policy: router.PolicyAllow},
	}
	got := rt.Mounts()
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Mounts (-want +got):\n%s", diff)
	}
	got[1].Hosts[0] = "changed"
	if rt.Mounts()[1].Hosts[0] != "q.example.com" {
		t.Error("Mounts shares the Hosts slice with the router")
	}
}

func TestMountFor(t *testing.T) {
	t.Parallel()
	_, ds := fakes("imgproxy", "query", "wsrv")
	rt := newRouter(t, &router.Options{
		Mounts: []router.Mount{
			{Name: "imgproxy", Dialect: "imgproxy", Prefix: "/"},
			{Name: "query", Dialect: "query", Prefix: "/q/", Hosts: []string{"q.example.com"}},
		},
		RootURL: &router.Mount{Name: "root", Dialect: "wsrv"},
	}, ds)
	tests := []struct {
		host, path string
		hasURL     bool
		want       string // dialect; "" for no mount
	}{
		{"x.com", "/q", true, "query"},
		{"x.com", "/q/resize", true, "query"},
		{"x.com", "/q/video", false, "query"},
		{"x.com", "/qx", true, "imgproxy"},
		{"x.com", "/", true, "wsrv"},
		{"x.com", "/", false, "imgproxy"},
		{"q.example.com", "/anything", true, "query"},
	}
	for _, tc := range tests {
		m := rt.MountFor(tc.host, tc.path, tc.hasURL)
		got := ""
		if m != nil {
			got = m.Dialect
		}
		if got != tc.want {
			t.Errorf("MountFor(%q, %q, %t) = %q, want %q", tc.host, tc.path, tc.hasURL, got, tc.want)
		}
	}
}
