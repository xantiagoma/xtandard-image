package imagekit_test

import (
	"errors"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// FuzzParse feeds arbitrary paths and queries to every parser
// configuration. Parse must never panic, must return either a well-formed
// result or an errs error of a client kind, and argument errors must name
// a field.
func FuzzParse(f *testing.F) {
	for _, s := range [][2]string{
		{"/tr:w-300,h-200,fo-auto/folder/a.jpg", ""},
		{"/folder/a.jpg", "tr=w-400,h-300:rt-90&ik-t=1767225600&ik-s=abc"},
		{"/demo/tr:w-300,h-200,cm-extract,xc-400,yc-300/folder/a.jpg", "v=1"},
		{"/a.jpg", "tr=l-image,i-a@@b.png,lx-N10,ly-20,w-100,l-end,l-text,ie-SGk%3D,co-red,fs-10,l-end"},
		{"/a.jpg", "tr=e-usm-2-2-0.8-0.024,e-sharpen-10,r-1_2_3_4,b-5_red,bg-AAFF0040,t-15,pg-2_4,n-thumb,di-x@@y.jpg"},
		{"/a.jpg", "tr=l-image,l-image,l-end"},
		{"/v.mp4", "tr=so-2,du-5,w-640,h-360,fo-top,q-60,vc-h264,ac-aac,fps-30:rt-N90:w-10,h-10,cm-extract,x-1,y-2"},
		{"/tr:vc-none,ac-opus,f-webm,ar-16_9,dpr-2/a.webm/ik-video.mp4", "ik-attachment=true"},
		{"/a.mov", "tr=l-video,i-x.mp4,l-end,cm-pad_resize,bg-blurred,w-1,h-1,eo-idu_sub_2"},
		{"/a.mp4/ik-master.m3u8", "tr=sr-240"},
		{"/v.webm", "tr=du-1,l-image,i-a@@b.png,lx-N8,ly-N8,w-48,lso-0.3,leo-0.7,l-end:l-text,i-hi,ldu-2,l-end"},
		{"/tr%3A/x", "tr=%zz"},
		{"", ""},
		{"//", "&&&"},
	} {
		f.Add(s[0], s[1])
	}
	fx := getFixture(f)
	f.Fuzz(func(t *testing.T, path, query string) {
		for name, p := range fx.parsers {
			res, err := p.Parse(&dialect.Input{Path: path, RawQuery: query, Mount: dialect.Mount{Source: mountSource}})
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
			c := *r
			c.ApplyDefaults(transform.StandardDefaults())
			if err := c.Validate(); err != nil {
				t.Fatalf("%s: %q?%q: invalid request: %v", name, path, query, err)
			}
			_ = c.CanonicalKey()
		}
	})
}
