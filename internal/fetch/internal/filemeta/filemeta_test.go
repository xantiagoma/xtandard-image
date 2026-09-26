package filemeta

import (
	"strings"
	"testing"
	"time"
)

func TestETag(t *testing.T) {
	t.Parallel()
	mtime := time.Unix(0x5f5e100, 999)
	tests := []struct {
		name  string
		size  int64
		mtime time.Time
		want  string
	}{
		{"both", 255, mtime, `W/"ff-5f5e100"`},
		{"size only", 16, time.Time{}, `W/"10-"`},
		{"mtime only", -1, mtime, `W/"-5f5e100"`},
		{"neither", -1, time.Time{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ETag(tc.size, tc.mtime); got != tc.want {
				t.Errorf("ETag(%d, %v) = %q, want %q", tc.size, tc.mtime, got, tc.want)
			}
		})
	}
}

func TestNotModified(t *testing.T) {
	t.Parallel()
	mtime := time.Date(2026, 9, 1, 12, 0, 0, 500, time.UTC)
	etag := ETag(10, mtime)
	tests := []struct {
		name  string
		inm   string
		ims   time.Time
		etag  string
		mtime time.Time
		want  bool
	}{
		{"no conditions", "", time.Time{}, etag, mtime, false},
		{"etag match", etag, time.Time{}, etag, mtime, true},
		{"etag strong form matches weakly", strings.TrimPrefix(etag, "W/"), time.Time{}, etag, mtime, true},
		{"etag in list", `"x", ` + etag, time.Time{}, etag, mtime, true},
		{"star", "*", time.Time{}, etag, mtime, true},
		{"etag mismatch ignores ims", `"x"`, mtime.Add(time.Hour), etag, mtime, false},
		{"unknown etag", `"x"`, time.Time{}, "", mtime, false},
		{"ims equal (sub-second mtime)", "", mtime.Truncate(time.Second), etag, mtime, true},
		{"ims later", "", mtime.Add(time.Hour), etag, mtime, true},
		{"ims earlier", "", mtime.Add(-time.Second), etag, mtime, false},
		{"ims unknown mtime", "", mtime, etag, time.Time{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := NotModified(tc.inm, tc.ims, tc.etag, tc.mtime); got != tc.want {
				t.Errorf("NotModified(%q, %v, %q, %v) = %v, want %v", tc.inm, tc.ims, tc.etag, tc.mtime, got, tc.want)
			}
		})
	}
}

func TestJoin(t *testing.T) {
	t.Parallel()
	tests := []struct{ root, key, want string }{
		{"", "a.jpg", "/a.jpg"},
		{"/", "dir/a.jpg", "/dir/a.jpg"},
		{"/srv/img/", "dir/a.jpg", "/srv/img/dir/a.jpg"},
		{"upload", "a.jpg", "/upload/a.jpg"},
	}
	for _, tc := range tests {
		if got := Join(tc.root, tc.key); got != tc.want {
			t.Errorf("Join(%q, %q) = %q, want %q", tc.root, tc.key, got, tc.want)
		}
	}
}
