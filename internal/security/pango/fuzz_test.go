package pango_test

import (
	"errors"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/security/pango"
)

// FuzzSanitize checks that sanitised markup is a fixed point (sanitising it
// again gives the same result) and that the limits hold.
func FuzzSanitize(f *testing.F) {
	for _, s := range []string{
		"Hello", "<b>x</b>", `<span size="20pt" foreground="#fff">x</span>`, "<big><small>y</small></big>",
		"&amp;&#10;", `<span font="Sans Bold 30">z</span>`, "</xi-markup>", "<!DOCTYPE x>", "<![CDATA[<b>]]>",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		o := pango.Options{MaxSize: 200}
		m, err := pango.Sanitize(in, 16, o)
		if err != nil {
			if !errors.Is(err, pango.ErrInvalid) && !errors.Is(err, pango.ErrTooLarge) {
				t.Fatalf("unexpected error kind: %v", err)
			}
			return
		}
		if m.MaxSize > 200 || m.MaxSize < 16 {
			t.Fatalf("MaxSize %g outside [16, 200]", m.MaxSize)
		}
		if len(m.Text) > 6*len(in)+64 {
			t.Fatalf("output grew from %d to %d bytes", len(in), len(m.Text))
		}
		again, err := pango.Sanitize(m.Text, 16, pango.Options{MaxSize: 200, MaxBytes: 1 << 20})
		if err != nil {
			t.Fatalf("sanitised markup rejected: %v\nin:  %q\nout: %q", err, in, m.Text)
		}
		if again != m {
			t.Fatalf("not a fixed point:\n%q\n%q", m.Text, again.Text)
		}
	})
}

// FuzzParseFont checks that a parsed description parses back to itself.
func FuzzParseFont(f *testing.F) {
	for _, s := range []string{"sans 16", "DejaVu Sans, Bold 12.5", "24", "Sans 20px", "a,b c"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		ft, err := pango.ParseFont(in, 0)
		if err != nil {
			return
		}
		again, err := pango.ParseFont(ft.String(), 0)
		if err != nil || again.String() != ft.String() {
			t.Fatalf("round trip %q → %q → %q (%v)", in, ft.String(), again.String(), err)
		}
	})
}
