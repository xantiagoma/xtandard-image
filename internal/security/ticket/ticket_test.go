package ticket

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

var (
	secretA = []byte("0123456789abcdef0123456789abcdef")
	secretB = []byte("fedcba9876543210fedcba9876543210")
	now     = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
)

func mustNew(t testing.TB, secrets ...[]byte) *Sealer {
	t.Helper()
	s, err := New(secrets...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	s := mustNew(t, secretA)
	payload := []byte(`{"source":"https://example.com/secret.mp4","video":{"stream":{}}}`)
	exp := now.Add(time.Hour)
	tok := s.Seal(payload, exp)
	got, gotExp, err := s.Open(tok, now)
	if err != nil || !bytes.Equal(got, payload) || !gotExp.Equal(exp) {
		t.Fatalf("Open = %q, %v, %v", got, gotExp, err)
	}
	if strings.Contains(tok, "example") {
		t.Fatal("the ticket shows its payload")
	}
	if strings.ContainsAny(tok, "+/=") {
		t.Fatalf("ticket %q is not base64url without padding", tok)
	}
	// Deterministic: the same stream and expiry give the same ticket
	// (cacheable manifests); another expiry or payload another one.
	if s.Seal(payload, exp) != tok || s.Seal(payload, exp.Add(time.Second)) == tok || s.Seal(append(payload, ' '), exp) == tok {
		t.Fatal("tickets are not a function of payload and expiry")
	}
}

func TestOpenRejects(t *testing.T) {
	t.Parallel()
	s := mustNew(t, secretA)
	tok := s.Seal([]byte("payload"), now.Add(time.Hour))
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		t.Fatal(err)
	}
	flip := func(i int) string {
		b := bytes.Clone(raw)
		b[i] ^= 1
		return base64.RawURLEncoding.EncodeToString(b)
	}
	tests := []struct {
		name string
		tok  string
		at   time.Time
		kind errs.Kind
	}{
		{"version", flip(0), now, errs.KindForbidden},
		{"nonce", flip(3), now, errs.KindForbidden},
		{"ciphertext", flip(len(raw) / 2), now, errs.KindForbidden},
		{"tag", flip(len(raw) - 1), now, errs.KindForbidden},
		{"truncated", tok[:len(tok)-4], now, errs.KindForbidden},
		{"empty", "", now, errs.KindForbidden},
		{"not base64", "!!!!", now, errs.KindForbidden},
		{"too long", strings.Repeat("A", MaxToken+1), now, errs.KindForbidden},
		{"other key", mustNew(t, secretB).Seal([]byte("payload"), now.Add(time.Hour)), now, errs.KindForbidden},
		{"expired", tok, now.Add(time.Hour), errs.KindExpired},
		{"long expired", tok, now.Add(24 * time.Hour), errs.KindExpired},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := s.Open(tc.tok, tc.at); errs.KindOf(err) != tc.kind {
				t.Fatalf("err %v, want %s", err, tc.kind)
			}
		})
	}
}

func TestRotation(t *testing.T) {
	t.Parallel()
	old := mustNew(t, secretA)
	tok := old.Seal([]byte("p"), now.Add(time.Hour))
	rotated := mustNew(t, secretB, secretA)
	if p, _, err := rotated.Open(tok, now); err != nil || string(p) != "p" {
		t.Fatalf("a ticket of the previous key: %q %v", p, err)
	}
	if _, _, err := old.Open(rotated.Seal([]byte("p"), now.Add(time.Hour)), now); errs.KindOf(err) != errs.KindForbidden {
		t.Fatalf("the old key opens a new ticket: %v", err)
	}
}

func TestNewRejects(t *testing.T) {
	t.Parallel()
	if _, err := New(); err == nil {
		t.Fatal("no secret accepted")
	}
	if _, err := New([]byte("short")); err == nil {
		t.Fatal("short secret accepted")
	}
}

func FuzzOpen(f *testing.F) {
	s := mustNew(f, secretA)
	f.Add(s.Seal([]byte("payload"), now.Add(time.Hour)))
	f.Add("AQ")
	f.Add("")
	f.Fuzz(func(t *testing.T, tok string) {
		p, _, err := s.Open(tok, now)
		if err == nil && s.Seal(p, now.Add(time.Hour)) == "" {
			t.Fatal("unreachable")
		}
		if err != nil && errs.KindOf(err) != errs.KindForbidden && errs.KindOf(err) != errs.KindExpired {
			t.Fatalf("kind %s", errs.KindOf(err))
		}
	})
}

func BenchmarkOpen(b *testing.B) {
	s := mustNew(b, secretA)
	tok := s.Seal(bytes.Repeat([]byte("x"), 300), now.Add(time.Hour))
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := s.Open(tok, now); err != nil {
			b.Fatal(err)
		}
	}
}

// TestOneSpelling: a ticket has one spelling; base64's unused trailing
// bits set differently do not open it (a tampered last character is
// always refused).
func TestOneSpelling(t *testing.T) {
	t.Parallel()
	s := mustNew(t, secretA)
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	tried := 0
	for n := range 3 {
		tok := s.Seal([]byte(strings.Repeat("p", n+1)), now.Add(time.Hour))
		if len(tok)%4 == 0 {
			continue // no unused bits
		}
		last := strings.IndexByte(alphabet, tok[len(tok)-1])
		alt := tok[:len(tok)-1] + string(alphabet[last^1]) // the lowest bit is never used
		if _, _, err := s.Open(alt, now); errs.KindOf(err) != errs.KindForbidden {
			t.Fatalf("%q opened as %q: %v", alt, tok, err)
		}
		tried++
	}
	if tried == 0 {
		t.Fatal("no ticket with unused bits")
	}
}
