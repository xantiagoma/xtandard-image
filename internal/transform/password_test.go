package transform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// TestPasswordNeverPrinted: every way a Password (or a request holding
// one) is formatted, logged or marshalled prints [REDACTED], never the
// bytes, and Clear wipes them.
func TestPasswordNeverPrinted(t *testing.T) {
	t.Parallel()
	const secret = "hunter2-secret"
	p := Password(secret)
	r := &Request{Source: SourceSpec{Raw: "s3://b/a.pdf", Password: &DocumentPassword{Secret: p, Digest: strings.Repeat("a", 64)}}}
	js, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("x", slog.Any("password", p), slog.Any("request", r))
	hexSecret := fmt.Sprintf("%x", secret)
	for _, s := range []string{
		fmt.Sprint(p), fmt.Sprintf("%v %+v %#v %s %q %x %X %d", p, p, p, p, p, p, p, p),
		fmt.Sprintf("%v %+v %#v", r, r, *r), string(js), logs.String(),
	} {
		if strings.Contains(s, secret) || strings.Contains(s, hexSecret) || strings.Contains(s, "104 117 110") {
			t.Errorf("printed: %s", s)
		}
	}
	if fmt.Sprint(p) != "[REDACTED]" || fmt.Sprint(Password(nil)) != "" {
		t.Errorf("String = %q / %q", fmt.Sprint(p), fmt.Sprint(Password(nil)))
	}
	r.Source.Password.Clear()
	if !bytes.Equal(r.Source.Password.Bytes(), make([]byte, len(secret))) {
		t.Errorf("Clear left %q", r.Source.Password.Bytes())
	}
	var none *DocumentPassword
	none.Clear()
	if none.Bytes() != nil {
		t.Error("nil password has bytes")
	}
}

func TestPasswordValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		src  SourceSpec
		ok   bool
	}{
		{"none", SourceSpec{}, true},
		{"with digest", SourceSpec{Password: &DocumentPassword{Secret: Password("pw"), Digest: strings.Repeat("0", 64)}}, true},
		{"no digest", SourceSpec{Password: &DocumentPassword{Secret: Password("pw")}}, false},
		{"digest alone", SourceSpec{Password: &DocumentPassword{Digest: strings.Repeat("0", 64)}}, false},
		{"too long", SourceSpec{Password: &DocumentPassword{Secret: Password(strings.Repeat("x", MaxPasswordLen+1)), Digest: strings.Repeat("0", 64)}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &Request{Source: tc.src}
			r.Source.Raw = "s3://b/a.pdf"
			r.ApplyDefaults(StandardDefaults())
			if err := r.Validate(); (err == nil) != tc.ok {
				t.Errorf("Validate = %v, want ok %t", err, tc.ok)
			}
		})
	}
}
