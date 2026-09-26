//go:build acceptance

package acceptance

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// jpegSrc is 320x240; rs:fit:64:64 makes it 64x48.
const (
	jpegSrc = "http://fixtures/jpeg/baseline.jpg"
	fit64   = "/rs:fit:64:64"
)

// imageCase is one request against the main app and what its response
// must be.
type imageCase struct {
	name   string
	path   string      // request path (already signed where needed)
	header http.Header // request headers
	status int
	ctype  string // exact Content-Type
	props  props  // decoded image; zero Format skips the decode
	check  func(t *testing.T, r *response)
}

func runImageCases(t *testing.T, app string, cases []imageCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := get(t, app+tc.path, tc.header)
			r.wantStatus(t, tc.status)
			if got := r.Header.Get("Content-Type"); tc.ctype != "" && got != tc.ctype {
				t.Errorf("Content-Type %q, want %q", got, tc.ctype)
			}
			if tc.props.Format != "" {
				got, err := decodeProps(r.Body)
				if err != nil {
					t.Fatalf("%d-byte body: %v", len(r.Body), err)
				}
				if diff := cmp.Diff(tc.props, got); diff != "" {
					t.Errorf("image (-want +got):\n%s", diff)
				}
			}
			if tc.check != nil {
				tc.check(t, r)
			}
		})
	}
}

// TestOutputFormats encodes the same source into every M1 output format
// (HEIC encode is enabled in compose.acceptance.yml).
func TestOutputFormats(t *testing.T) {
	t.Parallel()
	app := base(t, "XI_ACC_APP")
	formats := []struct{ ext, ctype, format string }{
		{"jpeg", "image/jpeg", "jpeg"},
		{"png", "image/png", "png"},
		{"webp", "image/webp", "webp"},
		{"avif", "image/avif", "avif"},
		{"heic", "image/heic", "heic"},
		{"jxl", "image/jxl", "jxl"},
		{"gif", "image/gif", "gif"},
		{"tiff", "image/tiff", "tiff"},
		{"bmp", "image/bmp", "bmp"},
		{"ico", "image/x-icon", "ico"},
	}
	cases := make([]imageCase, 0, len(formats))
	for _, f := range formats {
		cases = append(cases, imageCase{
			name:   f.ext,
			path:   "/unsafe" + fit64 + "/plain/" + jpegSrc + "@" + f.ext,
			status: http.StatusOK,
			ctype:  f.ctype,
			props:  props{Format: f.format, Width: 64, Height: 48},
			check:  noVaryAccept,
		})
	}
	runImageCases(t, app, cases)
}

func noVaryAccept(t *testing.T, r *response) {
	t.Helper()
	if v := r.Header.Values("Vary"); slices.ContainsFunc(v, func(s string) bool { return strings.Contains(s, "Accept") }) {
		t.Errorf("Vary %q on an explicit format, want no Accept", v)
	}
}

// TestSources fetches the same fixture through every configured source type.
func TestSources(t *testing.T) {
	t.Parallel()
	app := base(t, "XI_ACC_APP")
	k := loadKeys(t)
	jpeg64 := props{Format: "jpeg", Width: 64, Height: 48}
	encPath := fit64 + "/enc/" + k.encrypt(t, jpegSrc) + ".png"
	runImageCases(t, app, []imageCase{
		{name: "http", path: "/unsafe" + fit64 + "/plain/" + jpegSrc, status: 200, ctype: "image/jpeg", props: jpeg64},
		{name: "http base64", path: "/unsafe" + fit64 + "/" + b64(jpegSrc) + ".jpg", status: 200, ctype: "image/jpeg", props: jpeg64},
		{name: "s3", path: "/unsafe" + fit64 + "/plain/s3://fixtures/jpeg/baseline.jpg", status: 200, ctype: "image/jpeg", props: jpeg64},
		{name: "s3 named xi", path: "/unsafe" + fit64 + "/plain/xi://assets/jpeg/baseline.jpg", status: 200, ctype: "image/jpeg", props: jpeg64},
		{name: "fs", path: "/unsafe" + fit64 + "/plain/local:///jpeg/baseline.jpg", status: 200, ctype: "image/jpeg", props: jpeg64},
		{name: "fs named xi", path: "/unsafe" + fit64 + "/plain/xi://local/webp/lossy.webp@png", status: 200, ctype: "image/png"},
		{name: "enc signed", path: k.sign(encPath), status: 200, ctype: "image/png", props: props{Format: "png", Width: 64, Height: 48}},
		{name: "enc unsigned", path: "/unsafe" + encPath, status: 200, ctype: "image/png", props: props{Format: "png", Width: 64, Height: 48}},
		{name: "enc garbage", path: "/unsafe" + fit64 + "/enc/" + b64("not a ciphertext at all"), status: 403},
		{name: "http host not allowlisted", path: "/unsafe/plain/http://example.com/a.jpg", status: 403},
		{name: "s3 bucket not allowlisted", path: "/unsafe/plain/s3://other/a.jpg", status: 403},
		{name: "fs traversal", path: "/unsafe/plain/local:///../etc/passwd", status: 403},
	})
}

// b64 is the imgproxy base64 source form (URL-safe, unpadded).
func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// TestSigningAndPolicy covers signed imgproxy URLs and the unsigned policy
// of the two mounts: "/" (allow) and "/q/" (restricted).
func TestSigningAndPolicy(t *testing.T) {
	t.Parallel()
	app := base(t, "XI_ACC_APP")
	k := loadKeys(t)
	signed := k.sign(fit64 + "/plain/" + jpegSrc)
	tampered := strings.Replace(signed, "64:64", "65:64", 1)
	badSig := "/" + strings.Repeat("A", 43) + fit64 + "/plain/" + jpegSrc
	jpeg64 := props{Format: "jpeg", Width: 64, Height: 48}
	q := func(v url.Values) string { return "/q/?" + v.Encode() }
	runImageCases(t, app, []imageCase{
		{name: "signed", path: signed, status: 200, ctype: "image/jpeg", props: jpeg64, check: immutableAbsent},
		{name: "signed preset", path: k.sign("/pr:thumb/plain/" + jpegSrc + "@webp"), status: 200, ctype: "image/webp", props: props{Format: "webp", Width: 100, Height: 100}},
		{name: "signed info", path: "/info" + k.sign("/plain/"+jpegSrc), status: 200, ctype: "application/json"},
		{name: "tampered path", path: tampered, status: 403},
		{name: "bad signature", path: badSig, status: 403},
		{name: "unsigned allow mount", path: "/unsafe" + fit64 + "/plain/" + jpegSrc, status: 200, ctype: "image/jpeg", props: jpeg64},
		{name: "unsigned allow mount insecure", path: "/insecure/pr:thumb/plain/" + jpegSrc, status: 200, props: props{Format: "jpeg", Width: 100, Height: 100}},
		{name: "restricted allowed width", path: q(url.Values{"url": {jpegSrc}, "w": {"64"}}), status: 200, ctype: "image/jpeg", props: jpeg64},
		{name: "restricted allowed width+quality", path: q(url.Values{"url": {jpegSrc}, "w": {"128"}, "q": {"75"}, "f": {"webp"}}), status: 200, ctype: "image/webp", props: props{Format: "webp", Width: 128, Height: 96}},
		{name: "restricted width not allowlisted", path: q(url.Values{"url": {jpegSrc}, "w": {"123"}}), status: 403},
		{name: "restricted quality not allowlisted", path: q(url.Values{"url": {jpegSrc}, "w": {"64"}, "q": {"33"}}), status: 403},
		{name: "restricted blur rejected", path: q(url.Values{"url": {jpegSrc}, "w": {"64"}, "blur": {"5"}}), status: 403},
		{name: "restricted bogus xi_sig", path: q(url.Values{"url": {jpegSrc}, "w": {"123"}, "xi_sig": {"bogus"}}), status: 403},
	})
}

// immutableAbsent: a signed URL without cache buster or hashsum is not
// immutable (ARCHITECTURE §9.1).
func immutableAbsent(t *testing.T, r *response) {
	t.Helper()
	if cc := r.Header.Get("Cache-Control"); strings.Contains(cc, "immutable") || !strings.Contains(cc, "max-age=") {
		t.Errorf("Cache-Control %q", cc)
	}
}

// TestNegotiation checks Accept-driven output formats and Vary.
func TestNegotiation(t *testing.T) {
	t.Parallel()
	app := base(t, "XI_ACC_APP")
	path := "/unsafe" + fit64 + "/plain/" + jpegSrc
	accept := func(v string) http.Header { return http.Header{"Accept": {v}} }
	runImageCases(t, app, []imageCase{
		{name: "avif preferred", path: path, header: accept("image/avif,image/webp,*/*"), status: 200, ctype: "image/avif", props: props{Format: "avif", Width: 64, Height: 48}, check: varyAccept},
		{name: "webp", path: path, header: accept("image/webp,*/*"), status: 200, ctype: "image/webp", props: props{Format: "webp", Width: 64, Height: 48}, check: varyAccept},
		{name: "no modern format", path: path, header: accept("image/*"), status: 200, ctype: "image/jpeg", props: props{Format: "jpeg", Width: 64, Height: 48}, check: varyAccept},
		{name: "explicit extension wins", path: path + "@png", header: accept("image/avif,image/webp,*/*"), status: 200, ctype: "image/png", props: props{Format: "png", Width: 64, Height: 48}, check: noVaryAccept},
		{name: "explicit option wins", path: "/unsafe" + fit64 + "/f:gif/plain/" + jpegSrc, header: accept("image/avif,*/*"), status: 200, ctype: "image/gif", props: props{Format: "gif", Width: 64, Height: 48}, check: noVaryAccept},
	})
}

func varyAccept(t *testing.T, r *response) {
	t.Helper()
	if v := strings.Join(r.Header.Values("Vary"), ","); !strings.Contains(v, "Accept") {
		t.Errorf("Vary %q, want Accept", v)
	}
}

// TestHTTPSemantics: conditional requests, HEAD, raw passthrough, the
// fallback image, expiry and error caching (ARCHITECTURE §9.1, §12).
func TestHTTPSemantics(t *testing.T) {
	t.Parallel()
	app := base(t, "XI_ACC_APP")
	path := app + "/unsafe" + fit64 + "/plain/" + jpegSrc + "@png"

	t.Run("304 on If-None-Match", func(t *testing.T) {
		t.Parallel()
		first := get(t, path, nil)
		first.wantStatus(t, 200)
		etag := first.Header.Get("ETag")
		if !strings.HasPrefix(etag, `"xi1-`) {
			t.Fatalf("ETag %q", etag)
		}
		for _, inm := range []string{etag, "W/" + etag, `"other", ` + etag, "*"} {
			r := get(t, path, http.Header{"If-None-Match": {inm}})
			r.wantStatus(t, http.StatusNotModified)
			if len(r.Body) != 0 || r.Header.Get("ETag") != etag || r.Header.Get("Content-Type") != "" {
				t.Errorf("If-None-Match %s: body %d bytes, ETag %q, Content-Type %q", inm, len(r.Body), r.Header.Get("ETag"), r.Header.Get("Content-Type"))
			}
		}
		get(t, path, http.Header{"If-None-Match": {`"xi1-nope"`}}).wantStatus(t, 200)
	})

	t.Run("HEAD", func(t *testing.T) {
		t.Parallel()
		r := do(t.Context(), t, http.MethodHead, path, nil)
		r.wantStatus(t, 200)
		if r.Header.Get("Content-Type") != "image/png" || r.Header.Get("ETag") == "" || len(r.Body) != 0 {
			t.Errorf("HEAD: Content-Type %q, ETag %q, %d body bytes", r.Header.Get("Content-Type"), r.Header.Get("ETag"), len(r.Body))
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		t.Parallel()
		do(t.Context(), t, http.MethodPost, path, nil).wantStatus(t, http.StatusMethodNotAllowed)
	})

	t.Run("raw passthrough", func(t *testing.T) {
		t.Parallel()
		r := get(t, app+"/unsafe/raw:1/rs:fit:8:8/plain/http://fixtures/png/palette.png", nil)
		r.wantStatus(t, 200)
		if r.Header.Get("Content-Type") != "image/png" {
			t.Errorf("Content-Type %q", r.Header.Get("Content-Type"))
		}
		// raw is a passthrough: the contract is the origin's exact bytes,
		// not a re-encoded image.
		if !bytes.Equal(r.Body, fixture(t, "png/palette.png")) {
			t.Errorf("raw body (%d bytes) differs from the origin file", len(r.Body))
		}
	})

	t.Run("fallback image on source 404", func(t *testing.T) {
		t.Parallel()
		r := get(t, app+"/unsafe"+fit64+"/plain/http://fixtures/missing.jpg@png", nil)
		r.wantStatus(t, 200) // XI_FALLBACK_STATUS
		if r.Header.Get("X-Xi-Fallback") != "1" || r.Header.Get("Cache-Control") != "public, max-age=30" {
			t.Errorf("X-Xi-Fallback %q, Cache-Control %q", r.Header.Get("X-Xi-Fallback"), r.Header.Get("Cache-Control"))
		}
		p, err := decodeProps(r.Body)
		if err != nil || p.Format != "png" || p.Width > 64 || p.Height > 64 {
			t.Errorf("fallback image %+v, %v", p, err)
		}
	})

	t.Run("expired", func(t *testing.T) {
		t.Parallel()
		r := get(t, app+"/unsafe/exp:1000/plain/"+jpegSrc, nil)
		r.wantStatus(t, http.StatusNotFound)
		if cc := r.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("Cache-Control %q, want no-store", cc)
		}
	})

	t.Run("invalid URL", func(t *testing.T) {
		t.Parallel()
		r := get(t, app+"/unsafe/rs:fit:abc/plain/"+jpegSrc, nil)
		r.wantStatus(t, http.StatusBadRequest)
		if cc := r.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("Cache-Control %q, want no-store", cc)
		}
	})

	t.Run("JSON error body", func(t *testing.T) {
		t.Parallel()
		r := get(t, app+"/unsafe/plain/http://example.com/a.jpg", http.Header{"Accept": {"application/json"}})
		r.wantStatus(t, http.StatusForbidden)
		var body struct {
			Error struct{ Kind, Message, RequestID string }
		}
		if err := json.Unmarshal(r.Body, &body); err != nil || body.Error.Kind == "" {
			t.Errorf("error body %q: %v", r.Body, err)
		}
	})
}

// TestInfo checks /info against the fixture's known properties.
func TestInfo(t *testing.T) {
	t.Parallel()
	app := base(t, "XI_ACC_APP")
	cases := []struct {
		name, path string
		want       map[string]any
	}{
		{
			name: "jpeg defaults", path: "/info/unsafe/plain/" + jpegSrc,
			want: map[string]any{"size": 20859.0, "format": "jpeg", "mime_type": "image/jpeg", "width": 320.0, "height": 240.0, "orientation": 1.0},
		},
		{
			name: "png from s3 with extras", path: "/info/unsafe/cs:1/b:1/a:1/pn:1/plain/s3://fixtures/png/rgba8.png",
			want: map[string]any{"format": "png", "mime_type": "image/png", "alpha": true, "bands": 4.0, "colorspace": "srgb", "pages_number": 1.0},
		},
		{
			name: "heic from fs", path: "/info/unsafe/plain/local:///heic/photo.heic",
			want: map[string]any{"format": "heic", "mime_type": "image/heif"},
		},
		{
			name: "animated gif", path: "/info/unsafe/pn:1/plain/http://fixtures/gif/animated.gif",
			want: map[string]any{"format": "gif", "mime_type": "image/gif"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := get(t, app+tc.path, nil)
			r.wantStatus(t, 200)
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type %q", ct)
			}
			var got map[string]any
			if err := json.Unmarshal(r.Body, &got); err != nil {
				t.Fatalf("body %q: %v", r.Body, err)
			}
			for k, want := range tc.want {
				if diff := cmp.Diff(want, got[k]); diff != "" {
					t.Errorf("%s (-want +got):\n%s", k, diff)
				}
			}
			for _, k := range []string{"size", "width", "height"} {
				if v, ok := got[k].(float64); !ok || v <= 0 {
					t.Errorf("%s = %v, want > 0", k, got[k])
				}
			}
			etag := r.Header.Get("ETag")
			get(t, app+tc.path, http.Header{"If-None-Match": {etag}}).wantStatus(t, http.StatusNotModified)
		})
	}
}

// TestHealthAndMetrics: probes on the public listener, Prometheus metrics on
// the admin listener.
func TestHealthAndMetrics(t *testing.T) {
	t.Parallel()
	app := base(t, "XI_ACC_APP")
	admin := base(t, "XI_ACC_ADMIN")
	get(t, app+"/healthz", nil).wantStatus(t, 200)
	get(t, app+"/readyz", nil).wantStatus(t, 200)
	get(t, app+"/unsafe"+fit64+"/plain/"+jpegSrc, nil).wantStatus(t, 200)

	r := get(t, admin+"/metrics", nil)
	r.wantStatus(t, 200)
	body := string(r.Body)
	for _, m := range []string{
		"xi_requests_total{", "xi_request_duration_seconds_bucket{", "xi_requests_in_flight",
		"xi_stage_duration_seconds_bucket{", "xi_source_fetch_duration_seconds_bucket{",
		"xi_queue_depth", "xi_workers_busy", "xi_vips_mem_bytes", "xi_vips_mem_highwater_bytes",
		"xi_build_info{", "go_goroutines", "process_resident_memory_bytes",
	} {
		if !hasMetric(body, m) {
			t.Errorf("/metrics has no %s", m)
		}
	}
	if !strings.Contains(body, `status="200"`) {
		t.Error(`/metrics: no series with status="200"`)
	}
	// The admin listener only: /metrics is not on the public one.
	if s := get(t, app+"/metrics", nil).Status; s == 200 {
		t.Errorf("public /metrics status %d", s)
	}
}

func hasMetric(body, prefix string) bool {
	for line := range strings.Lines(body) {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}
