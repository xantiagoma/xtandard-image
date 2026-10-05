package cloudinary_test

import (
	"errors"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// FuzzParse feeds arbitrary paths to every parser configuration. Parse
// must never panic, must return either a well-formed result or an errs
// error of a client kind, and argument errors must name a field.
func FuzzParse(f *testing.F) {
	for _, e := range loadCorpus(f) {
		f.Add(e.Path)
	}
	for _, s := range []string{
		up + "c_fill,g_auto,w_400,h_300/e_blur:200/a_90/q_auto/f_auto/v1/folder/sample.jpg",
		up + "l_logos:brand/c_scale,w_80/fl_layer_apply,g_north_west,x_10,y_10/sample.jpg",
		up + "if_w_gt_1000/c_scale,w_1000/if_end/e_grayscale/sample.jpg",
		up + "s--AAAAAAAA--/w_100/sample.jpg",
		up + "t_thumb.chained/e_blur/sample.jpg",
		"/demo/image/fetch/w_100/https:/example.com/a.jpg",
		"/image/upload/w_auto:100:400,dpr_auto/%zz",
		"/demo/image/upload/b_rgb:,bo_px_solid_,r_:,e_trim:1:rgb:/x",
		"/demo/image/upload/w_,h_,c_,g_,x_,y_,ar_,z_/x",
		"/demo/video/upload/so_2,du_5/c_fill,g_north,w_640,h_360/q_auto:good,vc_h264,ac_aac,br_1m,fps_20-30/v1/dog.mp4",
		"/demo/video/upload/c_crop,w_100,h_100,x_1,y_2/c_pad,w_10,h_10/b_red/e_volume:-5dB/a_hflip.90/dog.webm",
		"/demo/video/upload/vc_none,f_auto/l_logo/fl_layer_apply/dpr_3/dog",
		"/demo/video/upload/l_logo,e_fade:500/e_fade:-750,fl_layer_apply,g_south_east,x_8,y_8,so_1,du_3,o_50/l_text:Arial_20:hi/fl_layer_apply/dog.mp4",
		"/demo/video/fetch/so_auto,eo_1p,ar_16:9,w_10/https://e.com/a.mov",
		"//", "", "/demo/image/upload/v1/",
	} {
		f.Add(s)
	}
	fx := getFixture(f)
	f.Fuzz(func(t *testing.T, path string) {
		for name, p := range fx.parsers {
			res, err := p.Parse(&dialect.Input{Path: path, Mount: dialect.Mount{Source: mountSource}})
			if err != nil {
				if res != nil {
					t.Fatalf("%s: result with error", name)
				}
				if _, ok := errors.AsType[*errs.Error](err); !ok {
					t.Fatalf("%s: %q: error %T is not an errs.Error", name, path, err)
				}
				switch errs.KindOf(err) {
				case errs.KindInvalidURL, errs.KindUnauthorized, errs.KindForbidden:
				default:
					t.Fatalf("%s: %q: unexpected kind %v: %v", name, path, errs.KindOf(err), err)
				}
				if ae, ok := errors.AsType[*dialect.ArgumentError](err); ok && ae.Field == "" {
					t.Fatalf("%s: argument error without field: %v", name, err)
				}
				continue
			}
			r := res.Request
			if res.SourceString == "" || res.SourceString != r.Source.Raw {
				t.Fatalf("%s: %q: bad source %q", name, path, res.SourceString)
			}
			if n := len(r.Pipelines); n < 1 || n > transform.MaxPipelines {
				t.Fatalf("%s: %q: %d pipelines", name, path, n)
			}
			r.ApplyDefaults(transform.StandardDefaults())
			if err := r.Validate(); err != nil {
				t.Fatalf("%s: %q: Validate: %v", name, path, err)
			}
			_ = r.CanonicalKey()
		}
	})
}

// BenchmarkParse measures typical URLs: an unpic-generated one, a
// chained SDK URL with a layer, a signed URL and a fetch URL.
func BenchmarkParse(b *testing.B) {
	fx := getFixture(b)
	sig := signPath(b, "sha1", "c_fill,h_200,w_300/sample.jpg")
	cases := []struct{ name, cfg, path string }{
		{"unpic", "default", "/demo/image/upload/w_640,h_480,f_auto,c_lfill/v1/samples/animals/three-dogs.jpg"},
		{"chained_layer", "default", up + "c_fill,g_auto,h_300,w_400/e_blur:200/l_logos:brand/c_scale,w_80/fl_layer_apply,g_north_west,x_10,y_10/f_auto/q_auto/v1/folder/sample.jpg"},
		{"signed", "signed", up + sig + "/c_fill,h_200,w_300/sample.jpg"},
		{"fetch", "default", "/demo/image/fetch/w_300,f_auto/https://example.com/images/cat.jpg"},
	}
	for _, c := range cases {
		p := fx.parsers[c.cfg]
		in := &dialect.Input{Path: c.path, Mount: dialect.Mount{Source: mountSource}}
		if _, err := p.Parse(in); err != nil {
			b.Fatalf("%s: %v", c.name, err)
		}
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _ = p.Parse(in)
			}
		})
	}
}
