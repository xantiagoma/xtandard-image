package imgix_test

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgix"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

const img = "/photos/a.jpg"

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// TestParams covers every imgix parameter of docs/URL-DIALECTS.md §5 with
// valid and invalid values; the expected requests are in
// testdata/golden/params.json.
func TestParams(t *testing.T) {
	t.Parallel()
	q := func(name, query string) testCase { return testCase{name: name, path: img, query: query} }
	runGolden(t, "params", []testCase{
		// Source.
		{name: "source_key", path: img},
		{name: "source_key_mount", path: img, query: "w=10", mount: "assets"},
		{name: "source_nested_escaped", path: "/a/b%20c/d%2Be.png"},
		{name: "source_colon_first_segment", path: "/a:b/c.jpg", mount: "assets"},
		{name: "source_web_proxy", path: "/https%3A%2F%2Forigin.com%2Fa.jpg%3Fv%3D1", query: "w=400", mount: "assets"},
		{name: "source_web_proxy_unencoded", path: "/http://origin.com/a.jpg"},
		{name: "source_empty", path: "/", query: "w=10"},
		{name: "source_empty_path", path: "", query: "w=10"},
		{name: "source_bad_escape", path: "/a%zz.jpg"},
		{name: "source_control_char", path: "/a%00.jpg"},
		{name: "query_bad_escape", path: img, query: "w=%zz"},
		{name: "query_empty_segments", path: img, query: "&&w=10&&"},

		// Size.
		q("w", "w=400"),
		q("w_float_rounded", "w=200.6"),
		q("w_relative", "w=0.5"),
		q("w_zero", "w=0"),
		q("w_negative", "w=-1"),
		q("w_nan", "w=NaN"),
		q("w_huge", "w=100000"),
		q("h", "h=300"),
		q("h_relative", "h=0.25"),
		q("h_invalid", "h=abc"),
		q("wh_last_wins", "w=100&w=200"),
		q("dpr", "w=100&dpr=2"),
		q("dpr_zero", "dpr=0"),
		q("dpr_too_big", "dpr=11"),
		q("max_w_max_h", "w=2000&max-w=800&max-h=600"),
		q("min_w_min_h", "min-w=100&min-h=50"),
		q("max_w_invalid", "max-w=-5"),
		q("rect_px", "rect=10,20,300,200"),
		q("rect_relative", "rect=0.1,0.2,0.5,0.5"),
		q("rect_three", "rect=1,2,3"),
		q("rect_zero_width", "rect=0,0,0,10"),

		// fit.
		q("fit_default_clip", "w=400&h=300"),
		q("fit_clip", "w=400&h=300&fit=clip"),
		q("fit_max", "w=400&fit=max"),
		q("fit_min", "w=400&h=300&fit=min"),
		q("fit_crop", "w=400&h=300&fit=crop"),
		q("fit_smart_crop", "w=400&h=300&fit=smart-crop"),
		q("fit_scale", "w=400&h=300&fit=scale"),
		q("fit_fill", "w=400&h=300&fit=fill"),
		q("fit_fill_color", "w=400&h=300&fit=fill&fill=solid&fill-color=ff0000"),
		q("fit_fill_color_over_bg", "w=400&h=300&fit=fill&fill-color=ff0000&bg=00ff00"),
		q("fit_fill_blur", "w=400&h=300&fit=fill&fill=blur"),
		q("fit_fill_gen", "w=400&h=300&fit=fill&fill=gen"),
		q("fit_fill_invalid", "w=400&h=300&fit=fill&fill=pattern"),
		q("fit_fillmax", "w=400&h=300&fit=fillmax&fill-color=white"),
		q("fit_clamp", "w=400&h=300&fit=clamp"),
		q("fit_facearea", "w=200&h=200&fit=facearea&facepad=2&faceindex=1"),
		q("fit_facearea_bad_index", "w=200&h=200&fit=facearea&faceindex=0"),
		q("fit_invalid", "w=400&fit=cover"),
		q("fit_without_size", "fit=crop"),
		q("fill_without_fill_fit", "w=10&fill=blur&fill-color=red"),
		q("face_params_without_facearea", "w=10&facepad=2"),

		// crop.
		q("crop_top", "w=1&h=1&fit=crop&crop=top"),
		q("crop_bottom_right", "w=1&h=1&fit=crop&crop=bottom,right"),
		q("crop_top_left", "w=1&h=1&fit=crop&crop=top,left"),
		q("crop_left", "w=1&h=1&fit=crop&crop=left"),
		q("crop_faces", "w=1&h=1&fit=crop&crop=faces"),
		q("crop_faces_entropy_fallback", "w=1&h=1&fit=crop&crop=faces,entropy"),
		q("crop_entropy", "w=1&h=1&fit=crop&crop=entropy"),
		q("crop_edges", "w=1&h=1&fit=crop&crop=edges"),
		q("crop_auto", "w=1&h=1&fit=crop&crop=auto"),
		q("crop_objects", "w=1&h=1&fit=crop&crop=car,vehicle,auto"),
		q("crop_invalid", "w=1&h=1&fit=crop&crop=Top!"),
		q("crop_with_fit_min", "w=1&h=1&fit=min&crop=bottom"),
		q("crop_with_fit_clip_ignored", "w=1&h=1&crop=top"),
		q("crop_focalpoint", "w=1&h=1&fit=crop&crop=focalpoint&fp-x=0.2&fp-y=0.8&fp-z=3"),
		q("crop_focalpoint_defaults", "w=1&h=1&fit=crop&crop=focalpoint"),
		q("crop_focalpoint_out_of_range", "w=1&h=1&fit=crop&crop=focalpoint&fp-x=1.5"),
		q("fp_without_focalpoint", "w=1&h=1&fit=crop&fp-x=0.2"),
		q("fp_z_too_big", "fp-z=101"),
		q("ar_crop", "w=400&fit=crop&ar=16:9"),
		q("ar_decimal", "w=400&fit=crop&ar=1.5:1"),
		q("ar_single_number", "w=400&fit=crop&ar=2"),
		q("ar_without_crop", "w=400&ar=16:9"),
		q("ar_invalid", "w=400&fit=crop&ar=16:0"),

		// Format and quality.
		q("fm_jpg", "fm=jpg"),
		q("fm_pjpg", "fm=pjpg"),
		q("fm_png", "fm=png"),
		q("fm_png8", "fm=png8"),
		q("fm_png32", "fm=png32"),
		q("fm_webp", "fm=webp"),
		q("fm_avif", "fm=avif"),
		q("fm_jxl", "fm=jxl"),
		q("fm_gif", "fm=gif"),
		q("fm_json", "fm=json"),
		q("fm_blurhash", "fm=blurhash"),
		q("fm_mp4", "fm=mp4"),
		q("fm_webm", "fm=webm&auto=format"),
		q("fm_jp2_unsupported", "fm=jp2"),
		q("fm_jxr_unsupported", "fm=jxr"),
		q("fm_invalid", "fm=bmp"),
		q("q", "q=60"),
		q("q_zero", "q=0"),
		q("q_too_big", "q=101"),
		q("auto_format", "auto=format"),
		q("auto_format_with_fm", "fm=png&auto=format"),
		q("auto_compress", "auto=compress"),
		q("auto_compress_with_q", "auto=compress&q=80"),
		q("auto_format_compress", "auto=format,compress"),
		q("auto_compress_with_fm", "auto=compress&fm=webp"),
		q("auto_enhance_redeye", "auto=enhance,redeye"),
		q("auto_true", "auto=true"),
		q("auto_invalid", "auto=faster"),
		q("lossless", "fm=webp&lossless=1"),
		q("lossless_false", "lossless=false"),
		q("lossless_invalid", "lossless=yes"),
		q("chromasub_420", "chromasub=420"),
		q("chromasub_422", "chromasub=422"),
		q("chromasub_444", "chromasub=444"),
		q("chromasub_invalid", "chromasub=411"),
		q("cs_srgb", "cs=srgb"),
		q("cs_tinysrgb", "cs=tinysrgb"),
		q("cs_strip", "cs=strip"),
		q("cs_origin", "cs=origin"),
		q("cs_adobergb", "cs=adobergb1998"),
		q("cs_invalid", "cs=p3"),
		q("dpi", "dpi=300"),
		q("dpi_invalid", "dpi=-1"),
		q("dl", "dl=my%20photo.jpg"),
		q("dl_empty", "dl="),
		q("ch", "ch=Width,DPR,Save-Data"),
		q("ch_lowercase", "ch=width,dpr"),
		q("ch_invalid", "ch=Viewport-Width"),

		// Rotation.
		q("orient_1", "orient=1"),
		q("orient_2", "orient=2"),
		q("orient_3", "orient=3"),
		q("orient_4", "orient=4"),
		q("orient_5", "orient=5"),
		q("orient_6", "orient=6"),
		q("orient_7", "orient=7"),
		q("orient_8", "orient=8"),
		q("orient_90", "orient=90"),
		q("orient_180", "orient=180"),
		q("orient_270", "orient=270"),
		q("orient_invalid", "orient=9"),
		q("orient_flip", "orient=2&flip=hv"),
		q("rot", "rot=45"),
		q("rot_90", "rot=90"),
		q("rot_zero", "rot=0"),
		q("rot_invalid", "rot=360"),
		q("flip_h", "flip=h"),
		q("flip_v", "flip=v"),
		q("flip_hv", "flip=hv"),
		q("flip_invalid", "flip=x"),

		// Adjustments and stylize.
		q("bri", "bri=50"),
		q("con", "con=-50"),
		q("exp", "exp=100"),
		q("gam", "gam=-100"),
		q("sat_grayscale", "sat=-100"),
		q("hue", "hue=-90"),
		q("vib", "vib=25"),
		q("adjust_zero_identity", "bri=0&con=0"),
		q("bri_out_of_range", "bri=101"),
		q("high_shad_unsupported", "high=-50&shad=50"),
		q("blur", "blur=100"),
		q("blur_max", "blur=2000"),
		q("blur_invalid", "blur=2001"),
		q("px", "px=20"),
		q("px_invalid", "px=101"),
		q("sharp", "sharp=50"),
		q("sharp_zero", "sharp=0"),
		q("sharp_invalid", "sharp=-1"),
		q("usm", "usm=50&usmrad=10"),
		q("usm_default_radius", "usm=-20"),
		q("usmrad_without_usm", "usmrad=5"),
		q("usm_invalid", "usm=200"),
		q("sepia", "sepia=100"),
		q("sepia_partial", "sepia=40"),
		q("sepia_zero", "sepia=0"),
		q("monochrome", "monochrome=ff0000"),
		q("monochrome_argb", "monochrome=80ff0000"),
		q("monochrome_invalid", "monochrome=zzz"),
		q("duotone", "duotone=000080,ffff00"),
		q("duotone_alpha", "duotone=000080,ffff00&duotone-alpha=40"),
		q("duotone_invalid", "duotone=000080"),
		q("duotone_alpha_alone", "duotone-alpha=40"),
		q("invert", "invert=true"),
		q("invert_false", "invert=0"),
		q("invert_invalid", "invert=maybe"),

		// Canvas.
		q("bg_rgb", "bg=f00"),
		q("bg_argb", "bg=8f00"),
		q("bg_rrggbb", "bg=ff8800"),
		q("bg_aarrggbb", "bg=80ff8800"),
		q("bg_hash", "bg=%23ff8800"),
		q("bg_named", "bg=RebeccaPurple"),
		q("bg_transparent", "bg=transparent"),
		q("bg_invalid", "bg=ff88"+"0"),
		q("pad", "pad=10"),
		q("pad_sides", "pad=10&pad-top=1&pad-left=4"),
		q("pad_side_only", "pad-bottom=7"),
		q("pad_invalid", "pad=-1"),
		q("border", "border=4,ff0000"),
		q("border_sides", "border=4,red&border-top=1&border-bottom=0"),
		q("border_named_color", "border=2,black"),
		q("border_invalid", "border=4"),
		q("border_side_without_border", "border-left=3"),
		q("border_radius", "border-radius=10"),
		q("border_radius_corners", "border-radius=1,2,3,4"),
		q("border_radius_invalid", "border-radius=1,2"),
		q("border_radius_inner", "border=4,000&border-radius-inner=5"),
		q("trim_auto", "trim=auto"),
		q("trim_auto_md", "trim=auto&trim-md=30&trim-sd=5"),
		q("trim_color", "trim=color&trim-color=ffffff&trim-tol=20"),
		q("trim_color_default", "trim=color"),
		q("trim_pad", "trim=auto&trim-pad=10"),
		q("trim_invalid", "trim=all"),
		q("trim_params_alone", "trim-md=10"),
		q("trim_alias", "trim=color&trimcolor=000"),
		q("mask_ellipse", "mask=ellipse"),
		q("mask_ellipse_bg", "mask=ellipse&mask-bg=ff0000"),
		q("mask_corners", "mask=corners&corner-radius=20"),
		q("mask_corners_four", "mask=corners&corner-radius=1,2,3,4"),
		q("mask_corners_no_radius", "mask=corners"),
		q("mask_image_unsupported", "mask=https://example.com/m.png"),
		q("mask_bg_invalid", "mask=ellipse&mask-bg=nope"),

		// Pages.
		q("page", "page=2"),
		q("page_invalid", "page=0"),
		q("frame", "frame=3"),
		q("frame_invalid", "frame=-1"),

		// Watermarks.
		q("mark_url", "mark=https://example.com/logo.png"),
		q("mark64", "mark64="+b64("https://example.com/logo.png?x=1&y=2")),
		q("mark_path", "mark=/logos/logo.png"),
		q("mark_full", "mark=https://example.com/l.png&mark-alpha=50&mark-align=top,left&mark-pad=4&mark-scale=25&mark-w=100&mark-h=0.5&mark-tile=grid&mark-rot=30&mark-fit=clip"),
		q("mark_xy", "mark=https://example.com/l.png&mark-x=10&mark-y=20"),
		q("mark_aliases", "mark=https://example.com/l.png&markalpha=10&markalign=middle,center"),
		q("mark_align_invalid", "mark=https://example.com/l.png&mark-align=up"),
		q("mark_tile_invalid", "mark=https://example.com/l.png&mark-tile=brick"),
		q("mark_empty", "mark="),
		q("mark_params_alone", "mark-alpha=50"),
		q("txt", "txt=Hello+%3Cworld%3E"),
		q("txt64", "txt64="+b64("Hello, World! 😁")),
		q("txt_full", "txt=Hi&txt-font=Avenir%20Next&txt-size=40&txt-color=80ffffff&txt-align=middle,center&txt-pad=20&txt-shad=3&txt-width=200&txt-fit=max"),
		q("txt_size_only", "txt=Hi&txt-size=12"),
		q("txt_color_invalid", "txt=Hi&txt-color=nope"),
		q("txt_empty", "txt="),
		q("blend_unsupported", "blend=ff0000&blend-mode=multiply"),

		// Base64 variants.
		q("b64_w", "w64="+b64("300")),
		q("b64_padded", "bg64="+base64.URLEncoding.EncodeToString([]byte("ff0000"))),
		q("b64_std_alphabet", "txt64="+base64.StdEncoding.EncodeToString([]byte("a?b>c"))),
		q("b64_invalid", "txt64=***"),
		q("b64_unknown_name", "foo64="+b64("x")),
		q("b64_s64_not_signature", "s64="+b64("x")),

		// Unknown, unsupported, silent and strict.
		q("unknown", "w=10&foo=bar"),
		q("unsupported", "w=10&bg-remove=true&upscale=true&htn=1&fp-debug=1"),
		q("silent_tags", "w=10&ixlib=js-3.8.0&ixid=abc"),
		{name: "strict_unknown", cfg: "strict", path: img, query: "w=10&foo=bar"},
		{name: "strict_unsupported", cfg: "strict", path: img, query: "w=10&bg-remove=true"},
		{name: "strict_silent_tags", cfg: "strict", path: img, query: "w=10&ixlib=rb-4.0.3&ixid=abc"},
		{name: "strict_auto_enhance", cfg: "strict", path: img, query: "auto=format,enhance"},
		{name: "strict_known", cfg: "strict", path: img, query: "w=10&h=20&fit=crop&crop=faces"},
	})
}

// TestSigning covers the s parameter and expires; signatures are the
// imgix-blueprint vectors or produced with the blueprint token.
func TestSigning(t *testing.T) {
	t.Parallel()
	signQ := func(path, query string) string {
		q, err := getFixture(t).signer.SignImgixQuery(path, query)
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	future := strconv.FormatInt(now.Add(1).Unix()+1, 10)
	past := strconv.FormatInt(now.Unix()-1, 10)
	const proxy = "/http%3A%2F%2Favatars.com%2Fjohn-smith.png"
	runGolden(t, "signing", []testCase{
		// imgix-blueprint "Securing URLs" examples.
		{name: "blueprint_simple", cfg: "required", path: "/users/1.png", query: "s=6797c24146142d5b40bde3141fd3600c"},
		{name: "blueprint_proxy", cfg: "required", path: proxy, query: "s=493a52f008c91416351f8b33d4883135"},
		{name: "blueprint_params", cfg: "required", path: "/users/1.png", query: "w=400&h=300&s=c7b86f666a832434dd38577e38cf86d1"},
		{name: "blueprint_params_sorted", cfg: "required", path: "/users/1.png", query: "h=300&w=400&s=1a4e48641614d1109c6a7af51be23d18"},
		{name: "blueprint_proxy_params", cfg: "required", path: proxy, query: "w=400&h=300&s=61ea1cc7add87653bb0695fe25f2b534"},
		{name: "blueprint_proxy_params_sorted", cfg: "required", path: proxy, query: "h=300&w=400&s=a201fe1a3caef4944dcb40f6ce99e746"},
		{name: "blueprint_txt64", cfg: "required", path: "/users/1.png", query: "txt64=dGhpcy9zZWVtc-KApiBwcmV0dHkgc2tldGNoeSEg8J-YgQ&s=90f614fddc8ae5b9d5eb2f69b25daed4"},

		{name: "tampered_query", cfg: "required", path: "/users/1.png", query: "w=401&h=300&s=c7b86f666a832434dd38577e38cf86d1"},
		{name: "reordered_query", cfg: "required", path: "/users/1.png", query: "h=300&w=400&s=c7b86f666a832434dd38577e38cf86d1"},
		{name: "tampered_path", cfg: "required", path: "/users/2.png", query: "s=6797c24146142d5b40bde3141fd3600c"},
		{name: "missing_required", cfg: "required", path: "/users/1.png", query: "w=400"},
		{name: "missing_optional", cfg: "signed", path: "/users/1.png", query: "w=400"},
		{name: "malformed", cfg: "signed", path: "/users/1.png", query: "s=xyz"},
		{name: "duplicate_s", cfg: "signed", path: "/users/1.png", query: "s=6797c24146142d5b40bde3141fd3600c&s=6797c24146142d5b40bde3141fd3600c"},
		{name: "unsigned_config_ignores_s", path: "/users/1.png", query: "w=1&s=bogus"},
		{name: "s_not_last", cfg: "required", path: "/users/1.png", query: "w=400&s=c7b86f666a832434dd38577e38cf86d1&h=300"},

		{name: "expires_future", cfg: "required", path: img, query: signQ(img, "w=100&expires="+future)},
		{name: "expires_past", cfg: "required", path: img, query: signQ(img, "w=100&expires="+past)},
		{name: "expires_past_bad_signature", cfg: "required", path: img, query: "w=100&expires=" + past + "&s=6797c24146142d5b40bde3141fd3600c"},
		{name: "expires_past_unsigned", path: img, query: "expires=" + past},
		{name: "expires_zero_never", path: img, query: "expires=0"},
		{name: "expires_invalid", path: img, query: "expires=tomorrow"},
		{name: "expires64", cfg: "required", path: img, query: signQ(img, "expires64="+b64(past))},
	})
}

// TestExpiredStatus pins the HTTP mapping of an expired URL (404) and of a
// bad signature (403).
func TestExpiredStatus(t *testing.T) {
	t.Parallel()
	p := getFixture(t).parsers["required"]
	past := strconv.FormatInt(now.Unix()-1, 10)
	q, err := getFixture(t).signer.SignImgixQuery(img, "expires="+past)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Parse(&dialect.Input{Path: img, RawQuery: q})
	if !errors.Is(err, dialect.ErrExpired) || errs.Status(errs.KindOf(err)) != http.StatusNotFound {
		t.Fatalf("expired: %v (status %d)", err, errs.Status(errs.KindOf(err)))
	}
	_, err = p.Parse(&dialect.Input{Path: img, RawQuery: "expires=" + past + "&s=00000000000000000000000000000000"})
	if !errors.Is(err, sign.ErrSignatureMismatch) || errs.Status(errs.KindOf(err)) != http.StatusForbidden {
		t.Fatalf("bad signature: %v", err)
	}
	// Exactly at the expiry instant the URL is still valid.
	q, err = getFixture(t).signer.SignImgixQuery(img, "expires="+strconv.FormatInt(now.Unix(), 10))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Parse(&dialect.Input{Path: img, RawQuery: q}); err != nil {
		t.Fatalf("at expiry: %v", err)
	}
}

func TestNewAndAccessors(t *testing.T) {
	t.Parallel()
	p, err := imgix.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != imgix.Name || p.Verifier() != nil {
		t.Fatalf("Name %q, Verifier %v", p.Name(), p.Verifier())
	}
	// Default clock: a far-future expiry parses.
	if _, err := p.ParseURL(img, "expires=4102444800"); err != nil {
		t.Fatal(err)
	}
	v := getFixture(t).parsers["signed"].Verifier()
	if v == nil {
		t.Fatal("nil verifier")
	}
	res, err := getFixture(t).parsers["signed"].Parse(&dialect.Input{
		Path: img, RawQuery: "mark=/logo.png", Mount: dialect.Mount{Source: "assets"},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := res.Request
	if r.Source.Mount != "assets" || r.Pipelines[0].Watermarks[0].Source.Mount != "assets" {
		t.Fatalf("mount not propagated: %+v", r.Source)
	}
	if r.Source.Encoding != transform.SourceRelative {
		t.Fatalf("encoding %v", r.Source.Encoding)
	}
}
