package apidoc

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/awssih"
	"github.com/xantiagoma/xtandard-image/internal/dialect/bunny"
	"github.com/xantiagoma/xtandard-image/internal/dialect/cloudflare"
	"github.com/xantiagoma/xtandard-image/internal/dialect/cloudinary"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imagekit"
	"github.com/xantiagoma/xtandard-image/internal/dialect/thumbor"
	"github.com/xantiagoma/xtandard-image/internal/dialect/wsrv"
	"github.com/xantiagoma/xtandard-image/internal/playground"
	"github.com/xantiagoma/xtandard-image/internal/preset"
)

// companions are parameters some entries need next to them to be applied
// rather than recorded as ignored (strict mode rejects ignored ones).
var companions = map[string]map[string]string{
	playground.GrammarCloudinary: {"co": "e_colorize:50", "x": "g_north_west", "y": "g_north_west", "z": "c_thumb,w_100,h_100"},
	playground.GrammarImagekit:   {"cm": "w-100,h-100", "x": "w-100,h-100,cm-extract", "y": "w-100,h-100,cm-extract", "z": "fo-face", "c": "w-100", "ar": "w-100"},
	playground.GrammarCloudflare: {"zoom": "gravity=face"},
	playground.GrammarWsrv:       {"fpx": "a=focal", "fpy": "a=focal", "cbg": "fit=contain", "mtrim": "mask=circle", "mbg": "mask=circle", "rbg": "ro=45", "start": "filt=duotone", "stop": "filt=duotone", "precrop": "cx=1"},
	playground.GrammarBunny:      {"crop_gravity": "crop=10,10"},
}

// catalogueURL writes one parameter the way the playground page does, and
// returns the dialect input.
func catalogueURL(grammar string, o *playground.Option) *dialect.Input {
	name, v := o.Name, o.Example
	flag := len(o.Forms) == 0 || len(o.Forms[0].Args) == 0
	extra := companions[grammar][name]
	in := &dialect.Input{Mount: dialect.Mount{Source: "local"}}
	switch grammar {
	case playground.GrammarCloudinary:
		c := name + "_" + v
		if strings.HasPrefix(name, "e_") || strings.HasPrefix(name, "fl_") {
			c = name
			if !flag {
				c += ":" + v
			}
		}
		if extra != "" {
			c += "," + extra
		}
		in.Path = "/demo/image/fetch/" + c + "/https://example.com/a.jpg"
	case playground.GrammarImagekit:
		c := name
		if !flag {
			c += "-" + v
		}
		if extra != "" {
			c += "," + extra
		}
		in.Path = "/tr:" + c + "/a.jpg"
	case playground.GrammarCloudflare:
		c := name + "=" + v
		if extra != "" {
			c += "," + extra
		}
		in.Path = "/" + c + "/https://example.com/a.jpg"
	case playground.GrammarWsrv:
		in.Path, in.RawQuery = "/", "url=https%3A%2F%2Fexample.com%2Fa.jpg&"+name
		if !flag {
			in.RawQuery += "=" + v
		}
		if extra != "" {
			in.RawQuery += "&" + extra
		}
	case playground.GrammarThumbor:
		var seg string
		switch name {
		case "size":
			seg = v
		case "fit":
			seg = v + "/300x200"
		case "crop", "trim", "halign", "valign":
			seg = v
		case "smart":
			seg = "300x200/smart"
		default:
			f := name + "()"
			if !flag {
				f = name + "(" + v + ")"
			}
			seg = "filters:" + f
		}
		in.Path = "/unsafe/" + seg + "/a.jpg"
	case playground.GrammarBunny:
		in.Path, in.RawQuery = "/a.jpg", name+"="+v
		if extra != "" {
			in.RawQuery += "&" + extra
		}
	case playground.GrammarAWSSIH:
		in.Path = "/" + base64.StdEncoding.EncodeToString([]byte(`{"key":"a.jpg"}`))
		in.RawQuery = name + "=" + v
	}
	in.Query, _ = url.ParseQuery(in.RawQuery)
	return in
}

// TestDialectCataloguesParse keeps the static catalogues honest: every
// parameter, written with its example value as the page writes it, parses
// in strict mode (where unknown and unsupported parameters are a 400) with
// nothing recorded as ignored.
func TestDialectCataloguesParse(t *testing.T) {
	t.Parallel()
	presets, err := preset.New(preset.Options{Definitions: map[string]string{"thumb": "rs:fill:100:100"}})
	if err != nil {
		t.Fatal(err)
	}
	must := func(d dialect.Dialect, err error) dialect.Dialect {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	parsers := map[string]dialect.Dialect{
		playground.GrammarCloudinary: must(cloudinary.New(&cloudinary.Options{Strict: true, Presets: presets})),
		playground.GrammarImagekit:   must(imagekit.New(&imagekit.Options{Strict: true, Presets: presets})),
		playground.GrammarCloudflare: must(cloudflare.New(&cloudflare.Options{Strict: true})),
		playground.GrammarWsrv:       must(wsrv.New(&wsrv.Options{Strict: true})),
		playground.GrammarThumbor:    thumbor.New(&thumbor.Options{Strict: true}),
		playground.GrammarBunny:      must(bunny.New(&bunny.Options{Strict: true, Presets: presets})),
		playground.GrammarAWSSIH:     must(awssih.New(&awssih.Options{Strict: true, QueryParams: true})),
	}
	for _, cat := range Catalogues() {
		p, ok := parsers[cat.Grammar]
		if !ok {
			continue // imgproxy, query and imgix have their own catalogue tests
		}
		for i := range cat.Options {
			o := &cat.Options[i]
			t.Run(cat.Grammar+"/"+o.Name, func(t *testing.T) {
				t.Parallel()
				if o.Group == "" {
					t.Error("no group")
				}
				in := catalogueURL(cat.Grammar, o)
				res, err := p.Parse(in)
				if err != nil {
					t.Fatalf("%s?%s: %v", in.Path, in.RawQuery, err)
				}
				for _, ig := range res.Request.Ignored {
					if ig.Reason != "approximated" { // approximated parameters are applied
						t.Errorf("%s?%s: ignored %+v", in.Path, in.RawQuery, ig)
					}
				}
			})
		}
	}
}

func TestCataloguesHaveGroups(t *testing.T) {
	t.Parallel()
	for _, cat := range Catalogues() {
		for _, o := range cat.Options {
			if o.Group == "" {
				t.Errorf("%s option %s has no group", cat.Grammar, o.Name)
			}
		}
	}
}
