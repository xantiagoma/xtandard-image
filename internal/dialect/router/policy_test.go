package router_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

var restrictions = router.Restrictions{
	Widths:    []int{16, 640, 1080},
	DPRs:      []float64{2, 3},
	Qualities: []int{75, 80},
}

// req builds a one-pipeline request and lets mod edit it.
func req(mod func(r *transform.Request, p *transform.Pipeline)) func() *transform.Request {
	return func() *transform.Request {
		r := &transform.Request{Pipelines: make([]transform.Pipeline, 1)}
		if mod != nil {
			mod(r, &r.Pipelines[0])
		}
		return r
	}
}

func width(w int) func(*transform.Request, *transform.Pipeline) {
	return func(_ *transform.Request, p *transform.Pipeline) { p.Resize.Width.Px = w }
}

func TestUnsignedPolicy(t *testing.T) {
	t.Parallel()
	type edit = func(*transform.Request, *transform.Pipeline)
	tests := []struct {
		name   string
		policy router.Policy // mount policy; "" inherits restricted
		sig    sign.Result
		mod    edit
		wantOK bool
	}{
		{"allow: anything unsigned", router.PolicyAllow, sign.Unsigned, func(r *transform.Request, p *transform.Pipeline) {
			r.Kind = transform.KindRaw
			p.Resize.Width.Px = 641
			p.Watermarks = []transform.Watermark{{Opacity: 1}}
		}, true},
		{"deny: unsigned", router.PolicyDeny, sign.Unsigned, nil, false},
		{"deny: verified", router.PolicyDeny, sign.Verified, nil, true},
		{"deny: trusted", router.PolicyDeny, sign.Trusted, nil, true},
		{"restricted: signed skips checks", "", sign.Verified, width(641), true},
		{"restricted: empty request", "", sign.Unsigned, nil, true},
		{"restricted: allowlisted width", "", sign.Unsigned, width(640), true},
		{"restricted: non-allowlisted width", "", sign.Unsigned, width(641), false},
		{"restricted: height from width list", "", sign.Unsigned, func(_ *transform.Request, p *transform.Pipeline) {
			p.Resize.Width.Px, p.Resize.Height.Px = 640, 1080
		}, true},
		{"restricted: non-allowlisted height", "", sign.Unsigned, func(_ *transform.Request, p *transform.Pipeline) {
			p.Resize.Width.Px, p.Resize.Height.Px = 640, 427
		}, false},
		{"restricted: aspect ratio derives height", "", sign.Unsigned, func(_ *transform.Request, p *transform.Pipeline) {
			p.Resize.Width.Px, p.Resize.AspectRatio = 640, 1.5
			p.Resize.Type, p.Resize.Enlarge = transform.ResizeFill, true
		}, true},
		{"restricted: relative width", "", sign.Unsigned, func(_ *transform.Request, p *transform.Pipeline) { p.Resize.Width.Rel = 0.5 }, false},
		{"restricted: allowlisted dpr", "", sign.Unsigned, func(_ *transform.Request, p *transform.Pipeline) { p.Resize.DPR = 2 }, true},
		{"restricted: dpr 1 always", "", sign.Unsigned, func(_ *transform.Request, p *transform.Pipeline) { p.Resize.DPR = 1 }, true},
		{"restricted: non-allowlisted dpr", "", sign.Unsigned, func(_ *transform.Request, p *transform.Pipeline) { p.Resize.DPR = 2.5 }, false},
		{"restricted: min width", "", sign.Unsigned, func(_ *transform.Request, p *transform.Pipeline) { p.Resize.MinWidth = 10 }, false},
		{"restricted: zoom", "", sign.Unsigned, func(_ *transform.Request, p *transform.Pipeline) { p.Resize.ZoomX = 2 }, false},
		{"restricted: allowlisted quality and format", "", sign.Unsigned, func(r *transform.Request, _ *transform.Pipeline) {
			r.Output.Quality, r.Output.Format = 80, transform.FormatWebP
			r.Output.StripMetadata = new(true)
		}, true},
		{"restricted: non-allowlisted quality", "", sign.Unsigned, func(r *transform.Request, _ *transform.Pipeline) { r.Output.Quality = 81 }, false},
		{"restricted: non-allowlisted format quality", "", sign.Unsigned, func(r *transform.Request, _ *transform.Pipeline) {
			r.Output.FormatQuality = map[transform.Format]int{transform.FormatAVIF: 50}
		}, false},
		{"restricted: max bytes", "", sign.Unsigned, func(r *transform.Request, _ *transform.Pipeline) { r.Output.MaxBytes = 1000 }, false},
		{"restricted: smart gravity and cheap ops", "", sign.Unsigned, func(_ *transform.Request, p *transform.Pipeline) {
			p.Gravity.Type = transform.GravitySmart
			p.Rotate, p.Flip.H = 90, true
			p.Background = &transform.Color{R: 255, G: 255, B: 255, A: 255}
			p.Extend.Enabled = true
		}, true},
		{"restricted: face gravity", "", sign.Unsigned, func(_ *transform.Request, p *transform.Pipeline) { p.Gravity.Type = transform.GravityFace }, false},
		{"restricted: blur", "", sign.Unsigned, func(_ *transform.Request, p *transform.Pipeline) { p.Blur = 5 }, false},
		{"restricted: watermark", "", sign.Unsigned, func(_ *transform.Request, p *transform.Pipeline) {
			p.Watermarks = []transform.Watermark{{Source: &transform.SourceSpec{Raw: "https://x/y.png"}}}
		}, false},
		{"restricted: crop", "", sign.Unsigned, func(_ *transform.Request, p *transform.Pipeline) { p.Crop = &transform.Crop{} }, false},
		{"restricted: raw", "", sign.Unsigned, func(r *transform.Request, _ *transform.Pipeline) { r.Kind = transform.KindRaw }, false},
		{"restricted: info", "", sign.Unsigned, func(r *transform.Request, _ *transform.Pipeline) { r.Kind = transform.KindInfo }, false},
		{"restricted: security options", "", sign.Unsigned, func(r *transform.Request, _ *transform.Pipeline) { r.Limits.MaxSrcFileSize = 1 }, false},
		{"restricted: width client hint", "", sign.Unsigned, func(r *transform.Request, _ *transform.Pipeline) { r.ClientHints.Width = true }, false},
		{"restricted: save-data hint", "", sign.Unsigned, func(r *transform.Request, _ *transform.Pipeline) { r.ClientHints.SaveData = true }, true},
		{"restricted: chained pipelines", "", sign.Unsigned, func(r *transform.Request, _ *transform.Pipeline) {
			r.Pipelines = append(r.Pipelines, transform.Pipeline{})
		}, false},
		{"restricted: fallback url", "", sign.Unsigned, func(r *transform.Request, _ *transform.Pipeline) {
			r.Delivery.Fallback = &transform.SourceSpec{Raw: "https://x/404.png"}
		}, false},
		{"restricted: cache buster", "", sign.Unsigned, func(r *transform.Request, _ *transform.Pipeline) { r.Delivery.CacheBuster = "1" }, false},
		{"restricted: delivery basics", "", sign.Unsigned, func(r *transform.Request, _ *transform.Pipeline) {
			r.Delivery.Filename, r.Delivery.TTL = "a.jpg", time.Hour
			r.Delivery.Expires = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
		}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs, ds := fakes("d")
			fs["d"].sig, fs["d"].req = tc.sig, req(tc.mod)
			rt := newRouter(t, &router.Options{
				Mounts:       []router.Mount{{Name: "m", Dialect: "d", Prefix: "/", Policy: tc.policy}},
				Restrictions: restrictions,
			}, ds)
			res, m, err := rt.Route(request(t, "h", "/x"))
			if m == nil || m.Name != "m" {
				t.Fatalf("mount = %+v", m)
			}
			checkPolicyResult(t, res != nil, err, tc.wantOK)
		})
	}
}

func checkPolicyResult(t *testing.T, gotRes bool, err error, wantOK bool) {
	t.Helper()
	if wantOK {
		if err != nil || !gotRes {
			t.Fatalf("Route error = %v, want success", err)
		}
		return
	}
	if gotRes {
		t.Fatal("Route returned a result with an error")
	}
	e, ok := errors.AsType[*errs.Error](err)
	if !ok || e.HTTPStatus() != http.StatusForbidden || errs.KindOf(err) != errs.KindForbidden {
		t.Fatalf("Route error = %v, want 403 forbidden", err)
	}
	if !errors.Is(err, router.ErrUnsignedNotAllowed) {
		t.Errorf("error %v does not wrap ErrUnsignedNotAllowed", err)
	}
	if got := e.PublicMessage(); got != "unsigned request not allowed" {
		t.Errorf("public message = %q", got)
	}
}

func TestUnsignedPolicyInheritance(t *testing.T) {
	t.Parallel()
	fs, ds := fakes("d")
	fs["d"].req = req(width(641))
	rt := newRouter(t, &router.Options{
		Policy: router.PolicyDeny,
		Mounts: []router.Mount{
			{Name: "open", Dialect: "d", Prefix: "/open/", Policy: router.PolicyAllow},
			{Name: "global", Dialect: "d", Prefix: "/"},
		},
	}, ds)
	if _, m, err := rt.Route(request(t, "h", "/open/x")); err != nil || m.Policy != router.PolicyAllow {
		t.Errorf("override: mount %+v err %v, want allow", m, err)
	}
	_, m, err := rt.Route(request(t, "h", "/x"))
	if m.Policy != router.PolicyDeny {
		t.Errorf("inherited policy = %q, want deny", m.Policy)
	}
	checkPolicyResult(t, false, err, false)

	// The zero global policy is restricted.
	rt = newRouter(t, &router.Options{Mounts: []router.Mount{{Dialect: "d", Prefix: "/"}}}, ds)
	if _, m, _ := rt.Route(request(t, "h", "/x")); m.Policy != router.PolicyRestricted {
		t.Errorf("zero global policy = %q, want restricted", m.Policy)
	}
}

// TestImgproxyRestricted runs the real imgproxy dialect behind the router.
func TestImgproxyRestricted(t *testing.T) {
	t.Parallel()
	p, err := imgproxy.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := router.New(&router.Options{
		Mounts:       []router.Mount{{Name: "imgproxy", Dialect: imgproxy.Name, Prefix: "/", Source: "origin"}},
		Restrictions: restrictions,
	}, map[string]dialect.Dialect{imgproxy.Name: p})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, uri string
		wantOK    bool
	}{
		{"allowlisted width", "/unsafe/rs:fit:640:0/q:80/plain/https://example.com/a.jpg@webp", true},
		{"non-allowlisted width", "/unsafe/rs:fit:641:0/plain/https://example.com/a.jpg", false},
		{"watermark", "/unsafe/rs:fit:640:0/wm:0.5:ce/plain/https://example.com/a.jpg", false},
		{"raw", "/unsafe/raw:1/plain/https://example.com/a.jpg", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, _, err := rt.Route(request(t, "h", tc.uri))
			checkPolicyResult(t, res != nil, err, tc.wantOK)
			if res != nil {
				if res.Request.Pipelines[0].Resize.Width.Px != 640 || res.Request.Source.Mount != "origin" {
					t.Errorf("request = %+v", res.Request)
				}
			}
		})
	}
	// A parse error is the dialect's, not the policy's.
	if _, _, err := rt.Route(request(t, "h", "/unsafe")); errs.KindOf(err) != errs.KindInvalidURL {
		t.Errorf("malformed imgproxy url = %v, want 400", err)
	}
}
