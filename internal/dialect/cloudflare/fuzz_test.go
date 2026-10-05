package cloudflare_test

import (
	"errors"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// FuzzParse feeds arbitrary paths and queries to every parser
// configuration of both grammars. Parse must never panic, must return
// either a well-formed result or an errs error of a client kind, and
// argument errors must name a field.
func FuzzParse(f *testing.F) {
	for _, s := range [][2]string{
		{opt("width=400,height=300,fit=cover,gravity=0.5x0.2,quality=80,format=webp,dpr=2"), ""},
		{opt("background=rgb(1,2,3),trim=0.1;0.2;0.1;0.2,border=red;1;2;3;4,metadata=none"), ""},
		{opt("trim=border,trim.border.color=%23fff,trim.border.tolerance=9,trim.left=5,trim.width=0.5"), ""},
		{opt("onerror=redirect,compression=fast,scq=low,anim=false,rotate=90,flip=hv,blur=250,sharpen=10"), ""},
		{"/w=10/https%3A%2F%2Fexample.com%2Fa.jpg%3Fx%3D1", "y=2&xi_sig=AAAA"},
		{"/cdn-cgi/image/w=10/https:/e.com/a.jpg", ""},
		{delivery + "/thumbnail", "exp=" + expFuture + "&sig=00"},
		{delivery + "/w=400,sharpen=3", "exp=" + expPast},
		{"/cdn-cgi/imagedelivery/h/a/b/c/public", "sig=a&sig=b"},
		{"/cdn-cgi/media/mode=video,time=5s,duration=5s,width=500,height=500,fit=crop,audio=false/https://e.com/v.mp4", ""},
		{"/mode=frame,time=1.5s,width=320,format=png/v.mp4", ""},
		{"/mode=spritesheet,duration=10s,imageCount=12,width=160,fit=cover/v.mp4", ""},
		{"/mode=audio,time=2m,duration=1m,format=m4a,filename=clip_1/v.mp4", "xi_sig=AAAA"},
		{"/((((,)))/x", "exp=-1"},
		{"//", "&&&"},
		{"", ""},
	} {
		f.Add(s[0], s[1])
	}
	fx := getFixture(f)
	f.Fuzz(func(t *testing.T, path, query string) {
		for name, p := range fx.parsers {
			res, err := p.Parse(&dialect.Input{Path: path, RawQuery: query})
			if err != nil {
				if res != nil {
					t.Fatalf("%s: result with error", name)
				}
				if _, ok := errors.AsType[*errs.Error](err); !ok {
					t.Fatalf("%s: %q?%q: error %T is not an errs.Error", name, path, query, err)
				}
				switch errs.KindOf(err) {
				case errs.KindInvalidURL, errs.KindUnauthorized, errs.KindForbidden, errs.KindExpired:
				default:
					t.Fatalf("%s: %q?%q: unexpected kind %v: %v", name, path, query, errs.KindOf(err), err)
				}
				if ae, ok := errors.AsType[*dialect.ArgumentError](err); ok && ae.Field == "" {
					t.Fatalf("%s: argument error without field: %v", name, err)
				}
				continue
			}
			r := res.Request
			if res.SourceString == "" || res.SourceString != r.Source.Raw {
				t.Fatalf("%s: %q?%q: bad source %q", name, path, query, res.SourceString)
			}
			if n := len(r.Pipelines); n < 1 || n > transform.MaxPipelines {
				t.Fatalf("%s: %q?%q: %d pipelines", name, path, query, n)
			}
			if (r.Kind == transform.KindVideo) != (r.Video != nil) {
				t.Fatalf("%s: %q?%q: kind %v with video %v", name, path, query, r.Kind, r.Video)
			}
			r.ApplyDefaults(transform.StandardDefaults())
			if err := r.Validate(); err != nil {
				t.Fatalf("%s: %q?%q: parsed request fails validation: %v", name, path, query, err)
			}
			_ = r.CanonicalKey()
		}
	})
}
