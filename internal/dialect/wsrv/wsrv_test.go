package wsrv_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
)

const src = "url=example.com/a.jpg"

// TestParams covers every wsrv parameter (docs/URL-DIALECTS.md §11) with
// valid values and, where the parameter has a grammar, invalid ones.
func TestParams(t *testing.T) {
	t.Parallel()
	runGolden(t, "params", []testCase{
		// Source.
		{name: "url_bare_host", query: src},
		{name: "url_https", query: "url=https://example.com/a.jpg"},
		{name: "url_http", query: "url=http://example.com/a.jpg"},
		{name: "url_ssl_prefix", query: "url=ssl:example.com/a.jpg"},
		{name: "url_ssl_prefix_slashes", query: "url=ssl://example.com/a.jpg"},
		{name: "url_scheme_relative", query: "url=//example.com/a.jpg"},
		{name: "url_encoded_query", query: "url=example.com/a.jpg%3Fv%3D1%26x%3D2&w=10"},
		{name: "url_s3", query: "url=s3://bucket/key.png"},
		{name: "url_leading_slash_relative", query: "url=/photos/a.jpg"},
		{name: "url_mount_relative", query: "url=photos/a.jpg&w=10", mount: "assets"},
		{name: "url_mount_absolute", query: "url=https://example.com/a.jpg", mount: "assets"},
		{name: "url_mount_scheme_relative", query: "url=//example.com/a.jpg", mount: "assets"},
		{name: "url_missing", query: "w=10"},
		{name: "url_empty", query: "url=&w=10"},
		{name: "url_first_wins", query: src + "&url=example.com/b.jpg"},
		{name: "url_control_char", query: "url=example.com/%00"},
		{name: "url_space", query: "url=example.com/a%20b.jpg"},
		{name: "path_not_root", path: "/other", query: src},
		{name: "path_empty", path: "", query: src},
		// Size.
		{name: "w", query: src + "&w=300"},
		{name: "width_synonym", query: src + "&width=300"},
		{name: "h", query: src + "&h=200"},
		{name: "height_synonym", query: src + "&height=200"},
		{name: "w_percent", query: src + "&w=50%25"},
		{name: "w_percent_literal", query: src + "&w=50%"},
		{name: "w_percent_out_of_range", query: src + "&w=150%25"},
		{name: "w_negative", query: src + "&w=-1"},
		{name: "w_invalid", query: src + "&w=abc"},
		{name: "w_empty", query: src + "&w="},
		{name: "w_zero", query: src + "&w=0"},
		{name: "w_first_wins", query: src + "&w=100&w=200"},
		{name: "dpr", query: src + "&w=100&dpr=2"},
		{name: "dpr_fraction", query: src + "&w=100&dpr=1.5"},
		{name: "dpr_zero", query: src + "&w=100&dpr=0"},
		{name: "dpr_too_large", query: src + "&dpr=9"},
		{name: "dpr_invalid", query: src + "&dpr=x"},
		// Fit.
		{name: "fit_inside", query: src + "&w=300&h=300&fit=inside"},
		{name: "fit_outside", query: src + "&w=300&h=300&fit=outside"},
		{name: "fit_cover", query: src + "&w=300&h=300&fit=cover"},
		{name: "fit_fill", query: src + "&w=300&h=300&fit=fill"},
		{name: "fit_contain", query: src + "&w=300&h=300&fit=contain"},
		{name: "fit_contain_cbg", query: src + "&w=300&h=300&fit=contain&cbg=black"},
		{name: "fit_contain_cbg_and_bg", query: src + "&w=300&h=300&fit=contain&cbg=black&bg=white"},
		{name: "fit_contain_align", query: src + "&w=300&h=300&fit=contain&a=top-left"},
		{name: "fit_contain_attention", query: src + "&w=300&h=300&fit=contain&a=attention"},
		{name: "cbg_without_contain", query: src + "&w=300&cbg=red"},
		{name: "fit_t_synonym", query: src + "&w=300&t=cover"},
		{name: "fit_deprecated_fit", query: src + "&w=300&fit=fit"},
		{name: "fit_deprecated_squaredown", query: src + "&w=300&fit=squaredown"},
		{name: "fit_deprecated_letterbox", query: src + "&w=300&fit=letterbox"},
		{name: "fit_deprecated_absolute", query: src + "&w=300&fit=absolute"},
		{name: "fit_deprecated_crop_prefix", query: src + "&w=300&fit=crop-top"},
		{name: "fit_invalid", query: src + "&fit=stretch"},
		{name: "we", query: src + "&w=300&we"},
		{name: "we_true", query: src + "&w=300&we=true"},
		{name: "we_false", query: src + "&w=300&we=false"},
		{name: "we_zero", query: src + "&w=300&we=0"},
		// Alignment.
		{name: "a_center", query: src + "&w=300&h=300&fit=cover&a=center"},
		{name: "a_top", query: src + "&w=300&h=300&fit=cover&a=top"},
		{name: "a_right", query: src + "&fit=cover&a=right"},
		{name: "a_bottom", query: src + "&fit=cover&a=bottom"},
		{name: "a_left", query: src + "&fit=cover&a=left"},
		{name: "a_top_left", query: src + "&fit=cover&a=top-left"},
		{name: "a_top_right", query: src + "&fit=cover&a=top-right"},
		{name: "a_bottom_left", query: src + "&fit=cover&a=bottom-left"},
		{name: "a_bottom_right", query: src + "&fit=cover&a=bottom-right"},
		{name: "a_deprecated_t", query: src + "&fit=cover&a=t"},
		{name: "a_entropy", query: src + "&w=300&h=300&fit=cover&a=entropy"},
		{name: "a_attention", query: src + "&w=300&h=300&fit=cover&a=attention"},
		{name: "a_focal_default", query: src + "&fit=cover&a=focal"},
		{name: "a_focal_fpx_fpy", query: src + "&fit=cover&a=focal&fpx=0.3&fpy=0.7"},
		{name: "a_focal_percentages", query: src + "&fit=cover&a=focal-20-80"},
		{name: "a_focal_percentages_fpx_first", query: src + "&fpx=0.9&fit=cover&a=focal-20-80"},
		{name: "a_crop_deprecated", query: src + "&fit=cover&a=crop-22-0"},
		{name: "align_synonym", query: src + "&fit=cover&align=bottom"},
		{name: "fpx_out_of_range", query: src + "&a=focal&fpx=2"},
		{name: "fpy_invalid", query: src + "&a=focal&fpy=x"},
		{name: "a_invalid", query: src + "&a=middle"},
		// Crop.
		{name: "crop_rect", query: src + "&cx=680&cy=500&cw=300&ch=300"},
		{name: "crop_rect_after_resize", query: src + "&w=500&cx=10&cy=20&cw=300&ch=200"},
		{name: "crop_rect_percent", query: src + "&cx=10%25&cy=100%25&cw=50%25&ch=50%25"},
		{name: "crop_only_offsets", query: src + "&cx=10&cy=10"},
		{name: "crop_precrop", query: src + "&w=300&cx=10&cy=20&cw=300&ch=200&precrop"},
		{name: "crop_precrop_false", query: src + "&w=300&cw=300&precrop=false"},
		{name: "crop_contain_after", query: src + "&w=300&h=300&fit=contain&cbg=red&cw=100&ch=100"},
		{name: "crop_effects_after", query: src + "&w=300&cw=100&blur=5&ro=45&filt=sepia"},
		{name: "crop_deprecated", query: src + "&crop=100,100,720,530"},
		{name: "crop_deprecated_short", query: src + "&crop=100,100"},
		{name: "crop_deprecated_cw_first", query: src + "&cw=50&crop=100,100,720,530"},
		{name: "crop_invalid", query: src + "&cw=abc"},
		// Trim.
		{name: "trim", query: src + "&trim=10"},
		{name: "trim_default", query: src + "&trim"},
		{name: "trim_zero", query: src + "&trim=0"},
		{name: "trim_tbg", query: src + "&trim=20&tbg=ff0000"},
		{name: "trim_out_of_range", query: src + "&trim=255"},
		{name: "trim_invalid", query: src + "&trim=x"},
		// Mask.
		{name: "mask_circle", query: src + "&w=300&h=300&fit=cover&mask=circle"},
		{name: "mask_ellipse", query: src + "&mask=ellipse"},
		{name: "mask_triangle", query: src + "&mask=triangle"},
		{name: "mask_triangle_180", query: src + "&mask=triangle-180"},
		{name: "mask_pentagon", query: src + "&mask=pentagon"},
		{name: "mask_pentagon_180", query: src + "&mask=pentagon-180"},
		{name: "mask_hexagon", query: src + "&mask=hexagon"},
		{name: "mask_square", query: src + "&mask=square"},
		{name: "mask_star", query: src + "&mask=star"},
		{name: "mask_heart_mbg", query: src + "&mask=heart&mbg=red"},
		{name: "mask_mtrim", query: src + "&mask=circle&mtrim"},
		{name: "mask_shape_synonym", query: src + "&shape=star&strim"},
		{name: "mask_none", query: src + "&mask=none&mtrim"},
		{name: "mask_invalid", query: src + "&mask=blob"},
		// Orientation.
		{name: "flip", query: src + "&flip"},
		{name: "flop", query: src + "&flop"},
		{name: "flip_flop", query: src + "&flip=1&flop=true"},
		{name: "flip_false", query: src + "&flip=false"},
		{name: "ro_90", query: src + "&ro=90"},
		{name: "ro_negative_90", query: src + "&ro=-90"},
		{name: "ro_450", query: src + "&ro=450"},
		{name: "ro_arbitrary", query: src + "&ro=45"},
		{name: "ro_arbitrary_rbg", query: src + "&h=300&ro=45&rbg=red"},
		{name: "ro_or_synonym", query: src + "&or=180"},
		{name: "ro_invalid", query: src + "&ro=x"},
		{name: "rbg_without_rotation", query: src + "&rbg=red"},
		// Adjustments.
		{name: "bg_name", query: src + "&bg=black"},
		{name: "bg_hex3", query: src + "&bg=f00"},
		{name: "bg_hex4_argb", query: src + "&bg=8f00"},
		{name: "bg_hex6", query: src + "&bg=ff8800"},
		{name: "bg_hex8_argb", query: src + "&bg=80ff8800"},
		{name: "bg_hash_encoded", query: src + "&bg=%23ff8800"},
		{name: "bg_invalid", query: src + "&bg=nocolour"},
		{name: "bg_bad_length", query: src + "&bg=12345"},
		{name: "blur", query: src + "&blur=5"},
		{name: "blur_mild", query: src + "&blur"},
		{name: "blur_out_of_range", query: src + "&blur=0.1"},
		{name: "blur_zero", query: src + "&blur=0"},
		{name: "blur_invalid", query: src + "&blur=x"},
		{name: "con", query: src + "&con=25"},
		{name: "con_negative", query: src + "&con=-100"},
		{name: "con_zero", query: src + "&con=0"},
		{name: "con_out_of_range", query: src + "&con=101"},
		{name: "gam", query: src + "&gam=3"},
		{name: "gam_default", query: src + "&gam"},
		{name: "gam_out_of_range", query: src + "&gam=5"},
		{name: "gam_invalid", query: src + "&gam=x"},
		{name: "mod_brightness", query: src + "&mod=2"},
		{name: "mod_all", query: src + "&mod=1.5,0.5,90"},
		{name: "mod_sat_first", query: src + "&sat=2&mod=1.5,0.5"},
		{name: "mod_bri_synonym", query: src + "&bri=1.2"},
		{name: "mod_out_of_range", query: src + "&mod=5000"},
		{name: "sat", query: src + "&sat=0.5"},
		{name: "sat_invalid", query: src + "&sat=x"},
		{name: "hue", query: src + "&hue=90"},
		{name: "hue_negative", query: src + "&hue=-90"},
		{name: "hue_wrap", query: src + "&hue=450"},
		{name: "sharp", query: src + "&sharp=3"},
		{name: "sharp_mild", query: src + "&sharp"},
		{name: "sharp_out_of_range", query: src + "&sharp=20"},
		{name: "sharp_flat_jagged", query: src + "&sharp=1,2,3"},
		{name: "sharp_keys", query: src + "&sharp=2&sharpf=1&sharpj=3"},
		{name: "sharp_invalid", query: src + "&sharp=x"},
		{name: "tint", query: src + "&tint=red"},
		{name: "tint_invalid", query: src + "&tint=zz"},
		{name: "filt_greyscale", query: src + "&filt=greyscale"},
		{name: "filt_sepia", query: src + "&filt=sepia"},
		{name: "filt_negate", query: src + "&filt=negate"},
		{name: "filt_duotone", query: src + "&filt=duotone"},
		{name: "filt_duotone_colours", query: src + "&filt=duotone&start=000&stop=fff"},
		{name: "filt_none", query: src + "&filt=none"},
		{name: "filt_invalid", query: src + "&filt=vintage"},
		// Format.
		{name: "output_jpg", query: src + "&output=jpg"},
		{name: "output_jpeg", query: src + "&output=jpeg"},
		{name: "output_png", query: src + "&output=png"},
		{name: "output_gif", query: src + "&output=gif"},
		{name: "output_tiff", query: src + "&output=tiff"},
		{name: "output_tif", query: src + "&output=tif"},
		{name: "output_webp", query: src + "&output=webp"},
		{name: "output_avif", query: src + "&output=avif"},
		{name: "output_jxl", query: src + "&output=jxl"},
		{name: "output_origin", query: src + "&output=origin"},
		{name: "output_json", query: src + "&w=300&output=json"},
		{name: "output_invalid", query: src + "&output=bmp"},
		{name: "q", query: src + "&q=20"},
		{name: "quality_synonym", query: src + "&quality=85"},
		{name: "q_out_of_range", query: src + "&q=0"},
		{name: "q_invalid", query: src + "&q=x"},
		{name: "l", query: src + "&output=png&l=9"},
		{name: "level_synonym", query: src + "&level=0"},
		{name: "l_out_of_range", query: src + "&l=10"},
		{name: "af", query: src + "&output=png&af"},
		{name: "il", query: src + "&w=300&il"},
		{name: "il_false", query: src + "&il=0"},
		{name: "ll", query: src + "&output=webp&ll"},
		{name: "lossless_synonym", query: src + "&lossless=true"},
		{name: "n_all", query: src + "&h=300&output=gif&n=-1"},
		{name: "n", query: src + "&n=3"},
		{name: "pages_synonym", query: src + "&pages=2"},
		{name: "n_invalid", query: src + "&n=0"},
		{name: "page", query: src + "&page=2"},
		{name: "page_largest", query: src + "&page=-1"},
		{name: "page_invalid", query: src + "&page=-3"},
		{name: "loop_delay_fsol", query: src + "&loop=0&delay=100,200&fsol=0"},
		// Delivery.
		{name: "filename", query: src + "&filename=photo"},
		{name: "maxage_days", query: src + "&w=100&maxage=31d"},
		{name: "maxage_week", query: src + "&maxage=1w"},
		{name: "maxage_months", query: src + "&maxage=2M"},
		{name: "maxage_year", query: src + "&maxage=1y"},
		{name: "maxage_combined", query: src + "&maxage=1w%203d"},
		{name: "maxage_seconds", query: src + "&maxage=86400"},
		{name: "maxage_too_short", query: src + "&maxage=1h"},
		{name: "maxage_too_long", query: src + "&maxage=2y"},
		{name: "maxage_bad_order", query: src + "&maxage=1d1w"},
		{name: "maxage_invalid", query: src + "&maxage=soon"},
		{name: "encoding_base64", query: src + "&crop=100,100,720,530&encoding=base64"},
		{name: "encoding_base64_upper", query: src + "&encoding=BASE64"},
		{name: "encoding_other", query: src + "&encoding=hex"},
		{name: "default_url", query: "url=example.org/noimage.jpg&default=wsrv.nl/placeholder.svg"},
		{name: "default_https", query: src + "&default=https://example.com/404.png"},
		{name: "default_one", query: src + "&default=1"},
		{name: "default_empty", query: src + "&default="},
		{name: "default_mount_relative", query: "url=a.jpg&default=missing.png", mount: "assets"},
		{name: "errorredirect_deprecated", query: src + "&errorredirect=example.com/404.png"},
		{name: "default_before_errorredirect", query: src + "&default=1&errorredirect=example.com/404.png"},
		{name: "default_control_char", query: src + "&default=example.com/%01"},
		// Unknown keys.
		{name: "unknown_ignored", query: src + "&zz=1&w=10"},
		{name: "unknown_strict", cfg: "strict", query: src + "&zz=1"},
		{name: "known_strict", cfg: "strict", query: src + "&w=10&fit=cover&maxage=1d&default=1"},
		// Combined.
		{name: "readme_root", query: "url=example.com/a.jpg&w=300&h=300&fit=cover&a=attention&output=webp&q=80"},
		{name: "readme_wsrv", query: "url=ssl:example.com/a.jpg&w=50%25&filt=greyscale&default=example.com/404.png"},
	})
}

// TestDocsExamples parses the examples of the wsrv.nl documentation
// (https://wsrv.nl/docs/, September 2026).
func TestDocsExamples(t *testing.T) {
	t.Parallel()
	examples := []struct{ name, query string }{
		{"intro", "url=wsrv.nl/lichtenstein.jpg&w=300&h=300"},
		{"size_w", "url=wsrv.nl/lichtenstein.jpg&w=300"},
		{"size_h", "url=wsrv.nl/lichtenstein.jpg&h=300"},
		{"size_dpr", "url=wsrv.nl/lichtenstein.jpg&h=144&dpr=2"},
		{"fit_inside", "url=wsrv.nl/lichtenstein.jpg&w=300&h=300&fit=inside"},
		{"fit_outside", "url=wsrv.nl/lichtenstein.jpg&w=300&h=300&fit=outside"},
		{"fit_cover", "url=wsrv.nl/lichtenstein.jpg&w=300&h=300&fit=cover"},
		{"fit_fill", "url=wsrv.nl/lichtenstein.jpg&w=300&h=300&fit=fill"},
		{"fit_contain", "url=wsrv.nl/lichtenstein.jpg&w=300&h=300&fit=contain&cbg=black"},
		{"fit_we", "url=wsrv.nl/lichtenstein.jpg&w=300&h=300&fit=inside&we"},
		{"crop_a_top", "url=wsrv.nl/lichtenstein.jpg&w=300&h=300&fit=cover&a=top"},
		{"crop_focal", "url=wsrv.nl/lichtenstein.jpg&w=300&h=300&fit=cover&a=focal&fpy=0.45"},
		{"crop_attention", "url=wsrv.nl/puppy.jpg&w=300&h=300&fit=cover&a=attention"},
		{"crop_rect", "url=wsrv.nl/lichtenstein.jpg&cx=680&cy=500&cw=300&ch=300"},
		{"trim", "url=wsrv.nl/transparency_demo.png&w=300&trim=10"},
		{"mask_circle", "url=wsrv.nl/lichtenstein.jpg&w=300&h=300&fit=cover&mask=circle"},
		{"mask_mtrim", "url=wsrv.nl/lichtenstein.jpg&w=300&h=900&fit=cover&a=crop-22-0&mask=circle&mtrim"},
		{"mask_heart", "url=wsrv.nl/transparency_demo.png&w=400&bg=black&mask=heart&mbg=red"},
		{"flip", "url=wsrv.nl/lichtenstein.jpg&h=300&flip"},
		{"flop", "url=wsrv.nl/lichtenstein.jpg&h=300&flop"},
		{"ro_rbg", "url=wsrv.nl/lichtenstein.jpg&h=300&ro=45&rbg=red"},
		{"bg", "url=wsrv.nl/transparency_demo.png&w=400&bg=black"},
		{"blur", "url=wsrv.nl/lichtenstein.jpg&w=300&blur=5"},
		{"con", "url=wsrv.nl/lichtenstein.jpg&w=300&con=25"},
		{"duotone", "url=wsrv.nl/zebra.jpg&w=300&h=300&fit=cover&a=focal&fpx=0.6&filt=duotone"},
		{"gam", "url=wsrv.nl/lichtenstein.jpg&w=300&gam=3"},
		{"mod", "url=wsrv.nl/lichtenstein.jpg&w=300&mod=2"},
		{"sat", "url=wsrv.nl/lichtenstein.jpg&w=300&sat=0.5"},
		{"hue", "url=wsrv.nl/lichtenstein.jpg&w=300&hue=90"},
		{"sharp", "url=wsrv.nl/lichtenstein.jpg&w=300&sharp=3"},
		{"tint", "url=wsrv.nl/lichtenstein.jpg&w=300&tint=red"},
		{"encoding", "url=wsrv.nl/lichtenstein.jpg&crop=100,100,720,530&encoding=base64"},
		{"maxage", "url=wsrv.nl/lichtenstein.jpg&w=100&maxage=31d"},
		{"il", "url=wsrv.nl/lichtenstein.jpg&w=300&il"},
		{"n", "url=wsrv.nl/banana.webp&h=300&output=gif&n=-1"},
		{"output", "url=wsrv.nl/lichtenstein.jpg&w=300&output=webp"},
		{"q", "url=wsrv.nl/lichtenstein.jpg&w=300&q=20"},
		{"default", "url=example.org/noimage.jpg&default=wsrv.nl/placeholder.svg"},
	}
	cases := make([]testCase, 0, len(examples))
	for _, e := range examples {
		cases = append(cases, testCase{name: e.name, query: e.query})
	}
	runGolden(t, "docs", cases)
}

// TestSigning covers the optional xi scheme.
func TestSigning(t *testing.T) {
	t.Parallel()
	f := getFixture(t)
	signed, err := f.xi.SignXIQuery("/", src+"&w=300")
	if err != nil {
		t.Fatal(err)
	}
	runGolden(t, "signing", []testCase{
		{name: "unsigned_off", query: src},
		{name: "unsigned_optional", cfg: "signed", query: src},
		{name: "unsigned_required", cfg: "required", query: src},
		{name: "signed_valid", cfg: "signed", query: signed},
		{name: "signed_valid_required", cfg: "required", query: signed},
		{name: "signed_tampered", cfg: "signed", query: signed + "&h=10"},
		{name: "signed_bad", cfg: "signed", query: src + "&xi_sig=AAAA"},
	})
}

// TestNameAndVerifier checks the dialect metadata.
func TestNameAndVerifier(t *testing.T) {
	t.Parallel()
	f := getFixture(t)
	var d dialect.Dialect = f.parsers["default"]
	if d.Name() != "wsrv" {
		t.Errorf("Name = %q", d.Name())
	}
	if d.Verifier() != nil {
		t.Error("default parser has a verifier")
	}
	if f.parsers["signed"].Verifier() == nil {
		t.Error("signed parser has no verifier")
	}
}
