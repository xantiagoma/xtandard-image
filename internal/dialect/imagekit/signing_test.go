package imagekit_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// TestSigning covers ik-s / ik-t verification against a mount prefix
// (no endpoint path): the signed message is the path after the mount.
func TestSigning(t *testing.T) {
	t.Parallel()
	f := getFixture(t)
	signed := func(path, query string, expires int64) string {
		t.Helper()
		out, err := f.signer.SignImageKitQuery(path, query, expires)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	const future, past = 1893456000, 1700000000
	tests := []struct {
		name, cfg, path, query string
		want                   sign.Result
		kind                   errs.Kind // expected error kind; the zero kind means success
	}{
		{"unsigned_off", "default", img, "tr=w-1", sign.Unsigned, errs.KindUnknown},
		{"ignored_when_off", "default", img, "ik-s=deadbeef", sign.Unsigned, errs.KindUnknown},
		{"unsigned_optional", "signed", img, "tr=w-1", sign.Unsigned, errs.KindUnknown},
		{"unsigned_required", "required", img, "tr=w-1", sign.Unsigned, errs.KindUnauthorized},
		{"verified", "signed", img, signed(img, "tr=w-300", 0), sign.Verified, errs.KindUnknown},
		{"verified_path_tr", "signed", "/tr:w-300/a.jpg", signed("/tr:w-300/a.jpg", "", 0), sign.Verified, errs.KindUnknown},
		{"verified_expiring", "required", img, signed(img, "tr=w-300", future), sign.Verified, errs.KindUnknown},
		{"expired", "required", img, signed(img, "tr=w-300", past), sign.Verified, errs.KindExpired},
		{"expired_unsigned_off", "default", img, "ik-t=1700000000", sign.Unsigned, errs.KindExpired},
		{"wrong", "signed", img, "tr=w-300&ik-s=" + "00000000000000000000000000000000000000ff", sign.Unsigned, errs.KindUnauthorized},
		{"malformed", "signed", img, "ik-s=xyz", sign.Unsigned, errs.KindUnauthorized},
		{"duplicate", "signed", img, "ik-s=a&ik-s=b", sign.Unsigned, errs.KindUnauthorized},
		{"expires_invalid_off", "default", img, "ik-t=soon", sign.Unsigned, errs.KindInvalidURL},
		{"no_expiry_constant", "signed", img, signed(img, "", 0) + "&ik-t=9999999999", sign.Verified, errs.KindUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := parseCase(t, testCase{cfg: tt.cfg, path: tt.path, query: tt.query})
			if tt.kind != errs.KindUnknown {
				if errs.KindOf(err) != tt.kind {
					t.Fatalf("err %v (kind %v), want kind %v", err, errs.KindOf(err), tt.kind)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if res.Signature != tt.want {
				t.Errorf("signature %v, want %v", res.Signature, tt.want)
			}
		})
	}
}

// TestSigningEndpointPath checks that the endpoint path is excluded from
// the signed message, like the SDK strips the URL endpoint.
func TestSigningEndpointPath(t *testing.T) {
	t.Parallel()
	f := getFixture(t)
	q, err := f.signer.SignImageKitQuery("/tr:w-300/a.jpg", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.parsers["endpoint"].Parse(&dialect.Input{Path: "/demo/tr:w-300/a.jpg", RawQuery: q})
	if err != nil || res.Signature != sign.Verified {
		t.Fatalf("Parse = %v, %v", res, err)
	}
	if _, ok := f.parsers["endpoint"].Verifier().(*sign.KeyedVerifier); !ok {
		t.Errorf("Verifier is %T", f.parsers["endpoint"].Verifier())
	}
}
