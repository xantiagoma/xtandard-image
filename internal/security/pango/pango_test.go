package pango_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/security/pango"
)

func TestSanitize(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, in string
		want     pango.Markup
	}{
		{name: "plain", in: "Hello", want: pango.Markup{Text: "Hello", Runes: 5, Visible: 5, MaxSize: 16}},
		{name: "entities", in: "a &amp; b &lt;c&gt; &#233;", want: pango.Markup{Text: "a &amp; b &lt;c&gt; é", Runes: 11, Visible: 7, MaxSize: 16}},
		{name: "newline", in: "a\nb", want: pango.Markup{Text: "a&#xA;b", Runes: 3, Visible: 2, MaxSize: 16}},
		{name: "styles", in: "<b>B</b><i>I</i><u>U</u><s>S</s><tt>T</tt>", want: pango.Markup{Text: "<b>B</b><i>I</i><u>U</u><s>S</s><tt>T</tt>", Runes: 5, Visible: 5, MaxSize: 16}},
		{name: "big", in: "<big><big>x</big></big>", want: pango.Markup{Text: "<big><big>x</big></big>", Runes: 1, Visible: 1, MaxSize: 16 * 1.2 * 1.2}},
		{name: "small", in: "<small>x</small>", want: pango.Markup{Text: "<small>x</small>", Runes: 1, Visible: 1, MaxSize: 16}},
		{
			name: "span attributes",
			in:   `<span foreground="#ff0000" weight='bold' size="20pt" font_family="DejaVu Sans" alpha="50%">x</span>`,
			want: pango.Markup{Text: `<span foreground="#ff0000" weight="bold" size="20pt" font_family="DejaVu Sans" alpha="50%">x</span>`, Runes: 1, Visible: 1, MaxSize: 20},
		},
		{name: "size 1024ths", in: `<span size="40960">x</span>`, want: pango.Markup{Text: `<span size="40960">x</span>`, Runes: 1, Visible: 1, MaxSize: 40}},
		{name: "size named", in: `<span size="xx-large">x</span>`, want: pango.Markup{Text: `<span size="xx-large">x</span>`, Runes: 1, Visible: 1, MaxSize: 16 * 1.2 * 1.2 * 1.2}},
		{name: "font desc", in: `<span font="Sans Bold 30px">x</span>`, want: pango.Markup{Text: `<span font="Sans Bold 30">x</span>`, Runes: 1, Visible: 1, MaxSize: 30}},
		{name: "attribute escaped", in: `<span foreground="red">&quot;</span>`, want: pango.Markup{Text: `<span foreground="red">&#34;</span>`, Runes: 1, Visible: 1, MaxSize: 16}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := pango.Sanitize(tc.in, 16, pango.Options{})
			if err != nil {
				t.Fatalf("Sanitize: %v", err)
			}
			if got != tc.want {
				t.Errorf("Sanitize(%q)\n got %+v\nwant %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitizeRejects(t *testing.T) {
	t.Parallel()
	deep := strings.Repeat("<b>", 9) + "x" + strings.Repeat("</b>", 9)
	for _, tc := range []struct {
		name, in string
		want     error
	}{
		{name: "unknown element", in: "<img src='x'/>", want: pango.ErrInvalid},
		{name: "unknown attribute", in: `<span letter_spacing="100000">x</span>`, want: pango.ErrInvalid},
		{name: "attribute on b", in: `<b foreground="red">x</b>`, want: pango.ErrInvalid},
		{name: "bad colour", in: `<span color="url(x)">x</span>`, want: pango.ErrInvalid},
		{name: "unclosed", in: "<b>x", want: pango.ErrInvalid},
		{name: "stray end", in: "x</b>", want: pango.ErrInvalid},
		{name: "close root", in: "x</xi-markup><b>y</b>", want: pango.ErrInvalid},
		{name: "root element", in: "<xi-markup>x</xi-markup>", want: pango.ErrInvalid},
		{name: "doctype", in: `<!DOCTYPE x [<!ENTITY e "x">]>&e;`, want: pango.ErrInvalid},
		{name: "unknown entity", in: "&nbsp;", want: pango.ErrInvalid},
		{name: "comment", in: "<!-- x -->", want: pango.ErrInvalid},
		{name: "pi", in: "<?php x ?>", want: pango.ErrInvalid},
		{name: "namespace", in: `<x:b>y</x:b>`, want: pango.ErrInvalid},
		{name: "not utf8", in: "\xff", want: pango.ErrInvalid},
		{name: "variation", in: `<span font="Sans @wght=900">x</span>`, want: pango.ErrInvalid},
		{name: "too deep", in: deep, want: pango.ErrTooLarge},
		{name: "huge size", in: `<span size="999999999">x</span>`, want: pango.ErrTooLarge},
		{name: "huge pt", in: `<span size="5000pt">x</span>`, want: pango.ErrTooLarge},
		{name: "huge font", in: `<span font="Sans 5000">x</span>`, want: pango.ErrTooLarge},
		{name: "big nesting", in: strings.Repeat("<big>", 8) + "x" + strings.Repeat("</big>", 8), want: pango.ErrTooLarge},
		{name: "too long", in: strings.Repeat("x", pango.DefaultMaxBytes+1), want: pango.ErrTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := pango.Sanitize(tc.in, 100, pango.Options{MaxSize: 400})
			if !errors.Is(err, tc.want) {
				t.Errorf("Sanitize(%q) = %v, want %v", tc.in, err, tc.want)
			}
		})
	}
}

func TestParseFont(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in     string
		want   string
		size   float64
		family string
	}{
		{in: "sans 16", want: "sans 16", size: 16, family: "sans"},
		{in: "  DejaVu Sans,  Bold 12.5 ", want: "DejaVu Sans, Bold 12.5", size: 12.5, family: "DejaVu Sans, Bold"},
		{in: "Roboto", want: "Roboto", family: "Roboto"},
		{in: "24", want: "24", size: 24},
		{in: "Sans 20px", want: "Sans 20", size: 20, family: "Sans"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			f, err := pango.ParseFont(tc.in, 0)
			if err != nil {
				t.Fatal(err)
			}
			if f.String() != tc.want || f.Size != tc.size || f.Family() != tc.family {
				t.Errorf("ParseFont(%q) = %q size %g family %q", tc.in, f.String(), f.Size, f.Family())
			}
		})
	}
	for _, bad := range []string{"Sans <b>", "Sans\x00", "Sans 0", "Sans -3", "Sans 2000", strings.Repeat("a", 129)} {
		if _, err := pango.ParseFont(bad, 0); err == nil {
			t.Errorf("ParseFont(%q) succeeded", bad)
		}
	}
}
