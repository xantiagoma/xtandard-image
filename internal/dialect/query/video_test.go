package query_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

const vsrc = "url=https://example.com/clip.mov"

// TestVideoKeys covers the /video endpoint (docs/URL-DIALECTS.md §4, the
// options of §3.8 as query keys) against testdata/golden/video.json.
func TestVideoKeys(t *testing.T) {
	t.Parallel()
	v := func(name, q string) testCase { return testCase{name: name, path: "/video", query: vsrc + q} }
	runGolden(t, "video", []testCase{
		v("defaults", ""),
		{name: "trailing_slash", path: "/video/", query: vsrc + "&w=320"},
		v("example", "&start=1&duration=5&width=640&format=webm&mute=1"),
		v("short_names", "&vs=1&ve=3&w=320&h=180&f=m4a&q=70"),
		v("fit_cover_gravity_words", "&w=200&h=200&fit=cover&g=southeast:10:5"),
		v("fit_stretch", "&w=200&h=200&fit=stretch"),
		v("fit_outside", "&fit=outside"),
		v("gravity_fp_commas", "&w=200&h=200&fit=fill&g=fp,0.2,0.8"),
		v("crop_whxy", "&crop=100,50,10,20"),
		v("crop_wh_gravity", "&crop=100,50&g=north"),
		v("flip_hv", "&flip=hv&rot=90"),
		v("resize_option", "&rs=fit,640,360,0,1&bg=ffffff"),
		v("codecs_bitrates", "&f=webm&vc=av1&ac=opus&vb=2M&ab=96k&crf=30&ki=1&fps=24"),
		v("audio", "&f=mp3&sr=44100&ach=1&vol=-6dB&ln=1,-16,-1.5,11&fade=1,2"),
		v("waveform", "&waveform=1,2266cc,ffffff,1&w=800&h=200&f=webp"),
		v("o_path", "&o=rs:fill:320:180/vd:2"),
		v("overlay", "&wm=0.8,soea,8,8&wmu=eGk6Ly9maXh0dXJlcy93YXRlcm1hcmsvYmFkZ2UucG5n&wms=48&ow=0.3,0.7&of=0.1,0.1&tc=hms,sowe,8,8,20"),
		v("overlay_o_path", "&o=wm:1:nowe:6:6/wmt:PGI-aGk8L2I-/ow:0.2"),
		v("cb_attachment_filename", "&cb=2&attachment=1&filename=clip"),
		v("unknown_key", "&blur=5"),
		{name: "unknown_key_strict", cfg: "strict", path: "/video", query: vsrc + "&blur=5"},
		v("fit_invalid", "&fit=auto"),
		v("bad_volume", "&vol=loud"),
		v("bad_sample_rate", "&sr=44000"),
		v("waveform_in_mp4", "&wf=1&f=mp4"),
		v("pdf_password_refused", "&pdp=abc"),
		{name: "url_missing", path: "/video", query: "w=10"},
	})
}

// TestVideoSigning: xi_sig over "/video" + the query verifies, and an
// image signature for the same query does not.
func TestVideoSigning(t *testing.T) {
	t.Parallel()
	f := getFixture(t)
	p := f.parsers["signed"]
	const q = vsrc + "&w=320&vd=2"
	signed, err := f.xi.SignXIQuery("/video", q)
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.ParseURL("/video", signed)
	if err != nil || res.Signature != sign.Verified || res.Request.Kind != transform.KindVideo {
		t.Fatalf("signed video: %+v %v", res, err)
	}
	image, err := f.xi.SignXIQuery("/", q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ParseURL("/video", image); errs.KindOf(err) != errs.KindUnauthorized {
		t.Fatalf("image signature on /video: %v", err)
	}
}
