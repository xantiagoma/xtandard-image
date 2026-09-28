package server

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// passwordRequest makes the fake router return a request carrying a
// decrypted pdf_password (as the imgproxy dialect does) and keeps it.
func passwordRequest(pw string, got **transform.Request) func(*transform.Request) {
	return func(r *transform.Request) {
		r.Source.Password = &transform.DocumentPassword{Secret: transform.Password(pw), Digest: strings.Repeat("a", 64)}
		*got = r
	}
}

// TestPDFPasswordPlumbing: a verified request's password reaches the
// probe and the processor and is wiped once served; a source default
// applies when the request has none; the request's own wins (WP-115).
func TestPDFPasswordPlumbing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, url, dflt, want string
	}{
		{"url password", "open sesame", "", "open sesame"},
		{"source default", "", "vault", "vault"},
		{"url wins", "open sesame", "vault", "open sesame"},
		{"none", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var req *transform.Request
			h := newHarness(t, func(h *harness) {
				h.router.sig = sign.Verified
				if tc.url != "" {
					h.router.mutate = passwordRequest(tc.url, &req)
				}
				if tc.dflt != "" {
					h.cfg.SourcePasswords = map[string]transform.Password{"origin": transform.Password(tc.dflt)}
				}
			})
			rec := h.do(t, http.MethodGet, okPath, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d %q", rec.Code, rec.Body.String())
			}
			if got := string(h.pr.password); got != tc.want {
				t.Errorf("probe password %q, want %q", got, tc.want)
			}
			if got := string(h.p.password); got != tc.want {
				t.Errorf("processor password %q, want %q", got, tc.want)
			}
			if req != nil && !bytes.Equal(req.Source.Password.Bytes(), make([]byte, len(tc.url))) {
				t.Errorf("request password not wiped: %q", req.Source.Password.Bytes())
			}
			if tc.dflt != "" && string(h.cfg.SourcePasswords["origin"]) == tc.dflt && string(h.srv.cfg.SourcePasswords["origin"]) != tc.dflt {
				t.Error("the source default was wiped")
			}
		})
	}
}

// TestPDFPasswordNeedsVerifiedSignature is the server's own check behind
// the dialects': a password on an unsigned or trusted-signature URL is 403
// before anything is fetched.
func TestPDFPasswordNeedsVerifiedSignature(t *testing.T) {
	t.Parallel()
	for _, sig := range []sign.Result{sign.Unsigned, sign.Trusted} {
		t.Run(sig.String(), func(t *testing.T) {
			t.Parallel()
			var req *transform.Request
			h := newHarness(t, func(h *harness) {
				h.router.sig = sig
				h.router.mutate = passwordRequest("secret", &req)
			})
			rec := h.do(t, http.MethodGet, okPath, nil)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "pdf_password requires a signed url") {
				t.Fatalf("status %d %q, want 403", rec.Code, rec.Body.String())
			}
			if h.f.calls.Load() != 0 || h.pr.calls.Load() != 0 {
				t.Error("fetched or probed")
			}
			if string(req.Source.Password.Bytes()) == "secret" {
				t.Error("password not wiped")
			}
		})
	}
}

func TestRedactPasswords(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{"/sig/pdp:TOKEN/rs:fit:1:1/plain/a.pdf", "/sig/pdp:REDACTED/rs:fit:1:1/plain/a.pdf"},
		{"/info/sig/pdf_password:TOK_EN/plain/a.pdf", "/info/sig/pdf_password:REDACTED/plain/a.pdf"},
		{"/sig/pdp_TO_KEN/plain/a.pdf", "/sig/pdp_REDACTED/plain/a.pdf"},
		{"/sig/pdf_password,T/-/pdp:U/plain/a.pdf", "/sig/pdf_password,REDACTED/-/pdp:REDACTED/plain/a.pdf"},
		{"/sig/pdp:/plain/a.pdf", "/sig/pdp:REDACTED/plain/a.pdf"},
		{"/sig/pdpx:1/plain/pdp.pdf", "/sig/pdpx:1/plain/pdp.pdf"},
		{"/sig/rs:fit:1:1/plain/a.jpg", "/sig/rs:fit:1:1/plain/a.jpg"},
		{"/q/", "/q/"},
	} {
		if got := redactPasswords(tc.in); got != tc.want {
			t.Errorf("redactPasswords(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
