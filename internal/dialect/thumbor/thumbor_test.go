package thumbor_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

const gh = "https%3A%2F%2Fgithub.com%2Fthumbor%2Fthumbor%2Fraw%2Fmaster%2Fexample.jpg"

// TestCorpus parses every thumbor URL found in thumbor's documentation
// (docs/*.rst, host removed) and test suite (tests/**/*.py), MIT.
func TestCorpus(t *testing.T) {
	t.Parallel()
	runGolden(t, "corpus", []testCase{
		// docs/usage.rst, docs/imaging.rst.
		{name: "docs_usage_flip_align_smart", path: "/unsafe/-300x-200/left/top/smart/path/to/my/nice/image.jpg"},
		{name: "docs_usage_meta", path: "/unsafe/meta/-300x-200/left/top/smart/path/to/my/nice/image.jpg"},
		{name: "docs_usage_http_image", path: "/unsafe/300x200/http://s.glbimg.com/jo/g1/f/original/2012/03/16/supersonic-skydiver_fran.jpg"},
		{name: "docs_usage_encoded_image", path: "/unsafe/300x200/smart/" + gh},
		{name: "docs_smart_key", path: "/unsafe/300x200/smart/path/to/image.jpg"},
		{name: "docs_square", path: "/unsafe/300x300/smart/path/to/image.jpg"},
		{name: "docs_huge", path: "/unsafe/9999x9999/smart/path/to/image.jpg"},
		{name: "docs_flip_auto", path: "/unsafe/-0x-0/" + gh},
		{name: "docs_stretch", path: "/unsafe/200x100/filters:stretch()/" + gh},
		{name: "docs_smart_200x400", path: "/unsafe/200x400/smart/" + gh},
		{name: "docs_manual_crop", path: "/unsafe/240x220:480x260/" + gh},
		{name: "docs_width_only", path: "/unsafe/300x0/" + gh},
		{name: "docs_extract_focal", path: "/unsafe/300x100/filters:extract_focal()/localhost:8888/unsafe/240x220:480x260/" + gh},
		{name: "docs_nested_thumbor_image", path: "/unsafe/300x100/localhost:8888/unsafe/240x220:480x260/" + gh},
		{name: "docs_autojpg", path: "/unsafe/300x300/filters:autojpg()/" + gh},
		{name: "docs_vertical_align_filter", path: "/unsafe/300x300/filters:vertical_align(middle)/" + gh},
		{name: "docs_focal", path: "/unsafe/400x100/filters:focal(146x206:279x360)/" + gh},
		{name: "docs_horizontal_align_filter", path: "/unsafe/400x600/filters:horizontal_align(center)/" + gh},
		{name: "docs_debug", path: "/unsafe/debug/200x400/smart/" + gh},
		{name: "docs_blur", path: "/unsafe/filters:blur(7)/" + gh},
		{name: "docs_brightness_contrast", path: "/unsafe/filters:brightness(10):contrast(30)/" + gh},
		{name: "docs_brightness", path: "/unsafe/filters:brightness(40)/" + gh},
		{name: "docs_contrast_negative", path: "/unsafe/filters:contrast(-40)/" + gh},
		{name: "docs_contrast", path: "/unsafe/filters:contrast(40)/" + gh},
		{name: "docs_convolution_edge", path: "/unsafe/filters:convolution(-1;-1;-1;-1;8;-1;-1;-1;-1,3,false)/http://upload.wikimedia.org/wikipedia/commons/5/50/Vd-Orig.png"},
		{name: "docs_convolution_blur", path: "/unsafe/filters:convolution(1;2;1;2;4;2;1;2;1,3,true)/http://upload.wikimedia.org/wikipedia/commons/5/50/Vd-Orig.png"},
		{name: "docs_cover", path: "/unsafe/filters:cover()/http://server.my/animated_static.gif"},
		{name: "docs_equalize", path: "/unsafe/filters:equalize()/" + gh},
		{name: "docs_format_webp", path: "/unsafe/filters:format(webp)/" + gh},
		{name: "docs_gifv", path: "/unsafe/filters:gifv()/http://localhost/livingroom.gif"},
		{name: "docs_gifv_background_short", path: "/unsafe/filters:gifv():background_color(f0f)/http://localhost/livingroom.gif"},
		{name: "docs_gifv_background_hex", path: "/unsafe/filters:gifv():background_color(ff00ff)/http://localhost/livingroom.gif"},
		{name: "docs_gifv_background_name", path: "/unsafe/filters:gifv():background_color(magenta)/http://localhost/livingroom.gif"},
		{name: "docs_gifv_mp4", path: "/unsafe/filters:gifv(mp4)/http://localhost/livingroom.gif"},
		{name: "docs_gifv_webm", path: "/unsafe/filters:gifv(webm)/http://localhost/livingroom.gif"},
		{name: "docs_grayscale", path: "/unsafe/filters:grayscale()/" + gh},
		{name: "docs_max_bytes", path: "/unsafe/filters:max_bytes(7500)/" + gh},
		{name: "docs_no_upscale", path: "/unsafe/filters:no_upscale()/" + gh},
		{name: "docs_noise", path: "/unsafe/filters:noise(40)/" + gh},
		{name: "docs_proportion", path: "/unsafe/filters:proportion(0.5)/" + gh},
		{name: "docs_quality", path: "/unsafe/filters:quality(40)/" + gh},
		{name: "docs_rgb", path: "/unsafe/filters:rgb(20,-20,40)/" + gh},
		{name: "docs_rotate", path: "/unsafe/filters:rotate(90)/" + gh},
		{name: "docs_round_corner", path: "/unsafe/filters:round_corner(20,255,255,255)/" + gh},
		{name: "docs_round_corner_elliptic", path: "/unsafe/filters:round_corner(20|40,0,0,0)/" + gh},
		{name: "docs_round_corner_transparent", path: "/unsafe/filters:round_corner(30,0,0,0,1)/" + gh},
		{name: "docs_saturation_negative", path: "/unsafe/filters:saturation(-40)/" + gh},
		{name: "docs_saturation", path: "/unsafe/filters:saturation(40)/" + gh},
		{name: "docs_sharpen", path: "/unsafe/filters:sharpen(2,1.0,true)/" + gh},
		{name: "docs_strip_exif", path: "/unsafe/filters:strip_exif()/http://www.arte.tv/static-epgapi/057460-011-A.jpg"},
		{name: "docs_strip_icc", path: "/unsafe/filters:strip_icc()/http://videoprocessing.ucsd.edu/~stanleychan/research/pix/Blurred_foreman_0005.png"},
		{name: "docs_fill_transparent_hex", path: "/unsafe/fit-in/300x225/filters:fill(add8e6,1)/https://github.com/thumbor/thumbor/wiki/dice_transparent_background.png"},
		{name: "docs_fill_auto_true", path: "/unsafe/fit-in/300x225/filters:fill(auto,true)/https://github.com/thumbor/thumbor/wiki/dice_transparent_background.png"},
		{name: "docs_fill_name_1", path: "/unsafe/fit-in/300x225/filters:fill(blue,1)/https://github.com/thumbor/thumbor/wiki/dice_transparent_background.png"},
		{name: "docs_fill_blur_true", path: "/unsafe/fit-in/300x225/filters:fill(blur,true)/https://github.com/thumbor/thumbor/wiki/dice_transparent_background.png"},
		{name: "docs_fill_short_true", path: "/unsafe/fit-in/300x225/filters:fill(f00,true)/https://github.com/thumbor/thumbor/wiki/dice_transparent_background.png"},
		{name: "docs_background_color_hex", path: "/unsafe/fit-in/300x300/filters:background_color(add8e6)/" + gh},
		{name: "docs_background_color_name", path: "/unsafe/fit-in/300x300/filters:background_color(blue)/" + gh},
		{name: "docs_background_color_short", path: "/unsafe/fit-in/300x300/filters:background_color(f00)/" + gh},
		{name: "docs_fill_hex", path: "/unsafe/fit-in/300x300/filters:fill(add8e6)/" + gh},
		{name: "docs_fill_auto", path: "/unsafe/fit-in/300x300/filters:fill(auto)/" + gh},
		{name: "docs_fill_blur", path: "/unsafe/fit-in/300x300/filters:fill(blur)/" + gh},
		{name: "docs_upscale", path: "/unsafe/fit-in/600x500/filters:upscale()/https://raw.githubusercontent.com/thumbor/thumbor/e86324e49d7e53acc2a8057e43f3fdd2ca5cea75/docs/images/dice_transparent_background.png"},
		{name: "docs_watermark", path: "/unsafe/filters:watermark(http://example.com/watermark.png,-10,-10,50)/" + gh},
		{name: "docs_watermark_repeat_ratio", path: "/unsafe/filters:watermark(http://example.com/watermark.png,repeat,repeat,50,20,none)/" + gh},
		{name: "docs_watermark_percent", path: "/unsafe/filters:watermark(http://example.com/watermark.png,-10p,center,50,none,30)/" + gh},
		// tests/handlers/*.py.
		{name: "tests_crop_1x", path: "/unsafe/0x0:1681x596/1x/image.jpg"},
		{name: "tests_plain", path: "/unsafe/image.jpg"},
		{name: "tests_space", path: "/unsafe/image%20space.jpg"},
		{name: "tests_comma", path: "/unsafe/Giunchedi%2C_Filippo_January_2015_01.png"},
		{name: "tests_utf8_http", path: "/unsafe/http://test.com/maracujá.jpg"},
		{name: "tests_200x200", path: "/unsafe/200x200/image.jpg"},
		{name: "tests_smart", path: "/unsafe/smart/image.jpg"},
		{name: "tests_autojpg_false", path: "/unsafe/filters:autojpg(false)/image.png"},
		{name: "tests_autojpg_true", path: "/unsafe/filters:autojpg(true)/image.png"},
		{name: "tests_format_invalid", path: "/unsafe/filters:format(asdf)/image.jpg"},
		{name: "tests_format_avif", path: "/unsafe/filters:format(avif)/1x1.png"},
		{name: "tests_format_gif", path: "/unsafe/filters:format(gif)/image.jpg"},
		{name: "tests_format_heic", path: "/unsafe/filters:format(heic)/image.jpg"},
		{name: "tests_format_heif", path: "/unsafe/filters:format(heif)/1x1.png"},
		{name: "tests_format_jpeg", path: "/unsafe/filters:format(jpeg)/20x20.jpg"},
		{name: "tests_format_png", path: "/unsafe/filters:format(png)/cmyk.jpg"},
		{name: "tests_max_age", path: "/unsafe/filters:max_age(30)/image.jpg"},
		{name: "tests_max_bytes", path: "/unsafe/filters:max_bytes(35000)/Giunchedi%2C_Filippo_January_2015_01.jpg"},
		{name: "tests_watermark_zero", path: "/unsafe/filters:watermark(boom.jpg,0,0,0)/image.jpg"},
		{name: "tests_fit_in", path: "/unsafe/fit-in/134x134/940x2.png"},
		{name: "tests_fit_in_equalize", path: "/unsafe/fit-in/134x134/filters:equalize()/940x2.png"},
		{name: "tests_fit_in_width", path: "/unsafe/fit-in/100x/Commons-logo.svg"},
		{name: "tests_meta_size", path: "/unsafe/meta/200x300/alabama1_ap620%C3%A9.jpg"},
		{name: "tests_meta_zero", path: "/unsafe/meta/0x0/Giunchedi%2C_Filippo_January_2015_01-cmyk.jpg"},
		{name: "tests_meta_gif", path: "/unsafe/meta/animated.gif"},
		{name: "tests_background_gifv", path: "/unsafe/filters:gifv(mp4):background_color(ff00ff)/animated.gif"},
		{name: "tests_image_that_looks_like_size", path: "/unsafe/20x20.jpg"},
		{name: "tests_query_in_key", path: "/unsafe/smart/image.jpg%3Fts%3D1"},
		{name: "tests_fragment_in_key", path: "/unsafe/smart/image.jpg%23something"},
		{name: "tests_empty", path: "/unsafe/"},
	})
}

// TestGrammar covers every segment of the URL grammar.
func TestGrammar(t *testing.T) {
	t.Parallel()
	runGolden(t, "grammar", []testCase{
		{name: "missing_signature", path: "/300x200/a.jpg"},
		{name: "empty", path: ""},
		{name: "only_unsafe", path: "/unsafe"},
		{name: "unsafe_trailing_slash", path: "/unsafe/300x200/"},
		{name: "no_leading_slash", path: "unsafe/a.jpg"},
		{name: "size_orig", path: "/unsafe/origx300/a.jpg"},
		{name: "size_orig_both", path: "/unsafe/origxorig/a.jpg"},
		{name: "size_flip_h", path: "/unsafe/-300x200/a.jpg"},
		{name: "size_flip_v_only", path: "/unsafe/x-/a.jpg"},
		{name: "size_bare_x", path: "/unsafe/x/a.jpg"},
		{name: "size_too_large", path: "/unsafe/70000x10/a.jpg"},
		{name: "size_overflow", path: "/unsafe/99999999999999999999x10/a.jpg"},
		{name: "size_not_a_size", path: "/unsafe/300xabc/a.jpg"},
		{name: "fit_in", path: "/unsafe/fit-in/300x200/a.jpg"},
		{name: "full_fit_in", path: "/unsafe/full-fit-in/300x200/a.jpg"},
		{name: "adaptive_fit_in", path: "/unsafe/adaptive-fit-in/300x200/a.jpg"},
		{name: "adaptive_full_fit_in", path: "/unsafe/adaptive-full-fit-in/300x200/a.jpg"},
		{name: "fit_in_without_size", path: "/unsafe/fit-in/a.jpg"},
		{name: "trim", path: "/unsafe/trim/a.jpg"},
		{name: "trim_bottom_right", path: "/unsafe/trim:bottom-right/a.jpg"},
		{name: "trim_top_left_tolerance", path: "/unsafe/trim:top-left:50/a.jpg"},
		{name: "trim_tolerance_only", path: "/unsafe/trim:10/a.jpg"},
		{name: "trim_tolerance_clamped", path: "/unsafe/trim:bottom-right:400/a.jpg"},
		{name: "trim_bad_corner_is_image", path: "/unsafe/trim:middle/a.jpg"},
		{name: "crop", path: "/unsafe/10x20:310x220/a.jpg"},
		{name: "crop_zero", path: "/unsafe/0x0:0x0/a.jpg"},
		{name: "crop_inverted", path: "/unsafe/300x20:10x220/a.jpg"},
		{name: "crop_too_large", path: "/unsafe/0x0:99999x10/a.jpg"},
		{name: "align_all", path: "/unsafe/300x200/right/bottom/a.jpg"},
		{name: "align_h_only", path: "/unsafe/300x200/left/a.jpg"},
		{name: "align_v_only", path: "/unsafe/300x200/top/a.jpg"},
		{name: "align_center_middle", path: "/unsafe/300x200/center/middle/a.jpg"},
		{name: "smart_overrides_align", path: "/unsafe/300x200/left/top/smart/a.jpg"},
		{name: "all_segments", path: "/unsafe/debug/meta/trim:bottom-right:5/10x20:310x220/adaptive-full-fit-in/-300x-0/right/bottom/smart/filters:quality(50)/s3-key/a.jpg"},
		{name: "doc_example_trim_crop", path: "/unsafe/trim/10x20:310x220/fit-in/-300x0/left/top/filters:blur(3)/s3-key/a.jpg"},
		{name: "wrong_order_is_image", path: "/unsafe/smart/300x200/a.jpg"},
		{name: "segment_as_last_is_image", path: "/unsafe/smart"},
		{name: "filters_as_image", path: "/unsafe/filters:quality(80)"},
		{name: "filters_malformed", path: "/unsafe/filters:quality(80):/a.jpg"},
		{name: "filters_no_parens_is_image", path: "/unsafe/filters:grayscale/a.jpg"},
		{name: "image_bad_escape", path: "/unsafe/300x200/a%zz.jpg"},
		{name: "image_control_char", path: "/unsafe/a%00.jpg"},
		{name: "image_absolute_s3", path: "/unsafe/s3://bucket/key.jpg"},
		{name: "image_encoded_url", path: "/unsafe/" + gh},
		{name: "query_ignored", path: "/unsafe/a.jpg"},
		{name: "strict_unknown_filter", cfg: "strict", path: "/unsafe/filters:sepia()/a.jpg"},
		{name: "strict_known_ignored_filter", cfg: "strict", path: "/unsafe/filters:noise(10)/a.jpg"},
	})
}

// TestFilters covers each filter with valid and invalid arguments.
func TestFilters(t *testing.T) {
	t.Parallel()
	const img = "/a.jpg"
	f := func(name, filter string) testCase {
		return testCase{name: name, path: "/unsafe/300x200/filters:" + filter + img}
	}
	runGolden(t, "filters", []testCase{
		f("quality", "quality(80)"),
		f("quality_zero", "quality(0)"),
		f("quality_too_high", "quality(101)"),
		f("quality_negative", "quality(-1)"),
		f("quality_missing", "quality()"),
		f("quality_two_args", "quality(1,2)"),
		f("format_upper", "format(WEBP)"),
		f("format_jpg", "format(jpg)"),
		f("format_quoted", "format('png')"),
		f("format_spaces", "format( avif )"),
		f("max_bytes_invalid", "max_bytes(-5)"),
		f("autojpg_bad", "autojpg(yes)"),
		f("strip_exif_args", "strip_exif(1)"),
		f("strip_both", "strip_exif():strip_icc()"),
		f("max_age_huge", "max_age(999999999999)"),
		f("no_upscale_then_upscale", "no_upscale():upscale()"),
		f("stretch", "stretch()"),
		{name: "stretch_with_fit_in", path: "/unsafe/fit-in/300x200/filters:stretch()" + img},
		f("proportion_out_of_range", "proportion(1.5)"),
		f("proportion_invalid", "proportion(abc)"),
		f("proportion_exponent", "proportion(1e-1)"),
		f("rotate_270", "rotate(270)"),
		f("rotate_negative", "rotate(-90)"),
		f("rotate_450", "rotate(450)"),
		f("rotate_45", "rotate(45)"),
		f("focal_fractional", "focal(0.1x0.2:0.3x0.6)"),
		f("focal_pixels", "focal(146x206:279x360)"),
		f("focal_inverted", "focal(0.5x0.5:0.1x0.1)"),
		f("focal_malformed", "focal(1x2)"),
		f("brightness_clamped", "brightness(500)"),
		f("brightness_float", "brightness(1.5)"),
		f("contrast_minus_100", "contrast(-100)"),
		f("saturation_minus_200", "saturation(-200)"),
		f("rgb_two_args", "rgb(1,2)"),
		f("grayscale_args", "grayscale(1)"),
		f("background_color_bad", "background_color(nothex)"),
		f("background_color_transparent", "background_color(transparent)"),
		f("fill_transparent_arg_bad", "fill(red,maybe)"),
		f("fill_bad_color", "fill(zzz)"),
		f("round_corner_bad_radius", "round_corner(a,0,0,0)"),
		f("round_corner_bad_channel", "round_corner(10,256,0,0)"),
		f("round_corner_few_args", "round_corner(10,0,0)"),
		f("blur_sigma", "blur(4,2)"),
		f("blur_zero", "blur(0)"),
		f("blur_sigma_zero", "blur(4,0)"),
		f("sharpen_zero", "sharpen(0,1,false)"),
		f("sharpen_bad_bool", "sharpen(1,1,yes)"),
		f("sharpen_few_args", "sharpen(1,1)"),
		f("watermark_center", "watermark(w.png,center,center,0)"),
		f("watermark_negative_zero", "watermark(w.png,-0,-0,100)"),
		f("watermark_percent_100", "watermark(w.png,100p,50p,20)"),
		f("watermark_repeat_x", "watermark(w.png,repeat,10,20)"),
		f("watermark_ratio_both", "watermark(w.png,10,10,20,30,40)"),
		f("watermark_quoted_url_comma", "watermark('https://e.com/w,1.png',10,10,20)"),
		f("watermark_encoded_url", "watermark(https%3A%2F%2Fe.com%2Fw.png,10,10,20)"),
		f("watermark_bad_alpha", "watermark(w.png,1,1,101)"),
		f("watermark_bad_pos", "watermark(w.png,left,1,1)"),
		f("watermark_bad_ratio", "watermark(w.png,1,1,1,x)"),
		f("watermark_empty_url", "watermark(,1,1,1)"),
		f("watermark_few_args", "watermark(w.png,1,1)"),
		f("unknown", "sepia()"),
		f("unsupported_all", "colorize(10,20,30,ff0000):curve([(0,0)],[(0,0)],[(0,0)],[(0,0)]):red_eye():frame(f.png)"),
		f("later_wins", "quality(10):quality(20)"),
		f("bad_escape_in_args", "quality(%zz)"),
		{name: "extract_focal_no_nested", path: "/unsafe/filters:extract_focal()/a.jpg"},
		{name: "extract_focal_nested_levels", path: "/unsafe/filters:extract_focal()/h.com/unsafe/300x200/i.com/unsafe/1x2:30x40/s3://b/k.jpg"},
		{name: "extract_focal_args", path: "/unsafe/filters:extract_focal(1)/a.jpg"},
	})
}

// TestSigned checks thumbor's HMAC vectors through the parser.
func TestSigned(t *testing.T) {
	t.Parallel()
	runGolden(t, "signed", []testCase{
		{name: "composer_vector", cfg: "signed", path: "/G_dykuWBGyEil5JnNh9cBke0Ajo=/200x300/myserver.com/myimg.jpg"},
		{name: "acme_smart", cfg: "signed", path: "/_wIUeSaeHw8dricKG2MGhqu5thk=/smart/image.jpg"},
		{name: "acme_filters", cfg: "signed", path: "/5YRxzS2yxZxj9SZ50SoZ11eIdDI=/filters:fill(blue)/image.jpg"},
		{name: "acme_meta", cfg: "signed", path: "/oGi9XL4nHJKGEKVHjHEp1Jg1KpM=/meta/animated.gif"},
		{name: "acme_utf8", cfg: "signed", path: "/lc6e3kkm_2Ww7NWho8HPOe-sqLU=/smart/alabama1_ap620%C3%A9.jpg"},
		{name: "acme_percent_encoded_sig", cfg: "signed", path: "/_wIUeSaeHw8dricKG2MGhqu5thk%3D/smart/image.jpg"},
		{name: "tampered", cfg: "signed", path: "/_wIUeSaeHw8dricKG2MGhqu5thk=/smart/image2.jpg"},
		{name: "bad_sig_escape", cfg: "signed", path: "/_wIUeSaeHw8dricKG2MGhqu5thk%zz/smart/image.jpg"},
		{name: "unsafe_allowed", cfg: "signed", path: "/unsafe/smart/image.jpg"},
		{name: "unsafe_required", cfg: "required", path: "/unsafe/smart/image.jpg"},
		{name: "signed_required", cfg: "required", path: "/_wIUeSaeHw8dricKG2MGhqu5thk=/smart/image.jpg"},
		{name: "signing_off_any_hash", path: "/_wIUeSaeHw8dricKG2MGhqu5thk=/smart/image2.jpg"},
		{name: "long_segment_is_signature", cfg: "signed", path: "/filters:format(webp):quality(80)/image.jpg"},
	})
}

// TestSignerRoundTrip signs arbitrary paths with the thumbor scheme and
// parses them back.
func TestSignerRoundTrip(t *testing.T) {
	t.Parallel()
	f := getFixture(t)
	p := f.parsers["required"]
	for _, path := range []string{
		"a.jpg",
		"meta/trim/10x20:310x220/fit-in/-300x0/left/top/smart/filters:blur(3):watermark(w.png,1,1,1)/" + gh,
	} {
		signed, err := f.signer.SignThumborPath(path)
		if err != nil {
			t.Fatal(err)
		}
		res, err := p.Parse(&dialect.Input{Path: signed})
		if err != nil || res.Signature != sign.Verified {
			t.Fatalf("%s: %v, %v", signed, res, err)
		}
		tampered := strings.Replace(signed, "a.jpg", "b.jpg", 1)
		if tampered != signed {
			_, err := p.Parse(&dialect.Input{Path: tampered})
			if !errors.Is(err, sign.ErrSignatureMismatch) || errs.KindOf(err) != errs.KindUnauthorized {
				t.Fatalf("tampered: %v", err)
			}
		}
	}
}

func TestParserBasics(t *testing.T) {
	t.Parallel()
	f := getFixture(t)
	if f.parsers["default"].Name() != "thumbor" || f.parsers["default"].Verifier() != nil {
		t.Fatal("default parser")
	}
	if f.parsers["signed"].Verifier() == nil {
		t.Fatal("signed parser has no verifier")
	}
	res, err := f.parsers["default"].Parse(&dialect.Input{Path: "/unsafe/meta/a.jpg", RawQuery: "w=1"})
	if err != nil || res.Request.Kind != transform.KindInfo || res.Request.Info.Schema != transform.InfoSchemaThumbor {
		t.Fatalf("meta: %+v, %v", res, err)
	}
}
