package cloudinary_test

import (
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect/cloudinary"
)

const up = "/demo/image/upload/"

// TestGrammar covers the URL shapes, delivery types, sources, versions,
// signatures and component splitting.
func TestGrammar(t *testing.T) {
	t.Parallel()
	sigSHA1 := signPath(t, "sha1", "c_fill,w_300/sample.jpg")
	cases := []testCase{
		{name: "public", path: up + "w_300/sample.jpg"},
		{name: "public_no_tx", path: up + "sample.jpg"},
		{name: "private_cdn", path: "/image/upload/w_300/sample.jpg"},
		{name: "private_cdn_nomap", cfg: "nomap", path: "/image/upload/w_300/sample.jpg"},
		{name: "cloud_other", path: "/other/image/upload/sample.jpg"},
		{name: "cloud_unknown", path: "/nope/image/upload/sample.jpg"},
		{name: "cloud_nomap", cfg: "nomap", path: "/anything/image/upload/sample.jpg"},
		{name: "cloud_named_image", path: "/image/image/upload/sample.jpg"},
		{name: "asset_video", path: "/demo/video/upload/dog.mp4"},
		{name: "asset_raw", path: "/demo/raw/upload/doc.txt"},
		{name: "asset_private_video", path: "/video/upload/dog.mp4"},
		{name: "delivery_sprite", path: "/demo/image/sprite/logo.png"},
		{name: "delivery_unknown", path: "/demo/image/nope/logo.png"},
		{name: "missing_delivery", path: "/demo/image"},
		{name: "missing_asset", path: "/demo"},
		{name: "empty", path: "/"},
		{name: "missing_public_id", path: up},
		{name: "missing_public_id_after_tx", path: up + "w_100/"},
		{name: "missing_public_id_after_version", path: up + "w_100/v12/"},
		{name: "version", path: up + "v1234/sample.jpg"},
		{name: "version_after_tx", path: up + "c_scale,w_100/v1712345678/folder/sample.png"},
		{name: "version_as_id", path: up + "w_100/v1234"},
		{name: "folder_id_without_version", path: up + "w_100/folder/sub/sample.jpg"},
		{name: "tx_like_id", path: up + "w_100/c_logo.png"},
		{name: "underscore_id", path: up + "my_photo.jpg"},
		{name: "underscore_folder", path: up + "w_100/my_folder/pic.jpg"},
		{name: "chained", path: up + "c_fill,w_400,h_300/e_blur:200/a_90/sample.jpg"},
		{name: "request_level_components_merge", path: up + "c_fill,w_400/q_auto/f_auto/dpr_2.0/sample.jpg"},
		{name: "unicode_id", path: up + "w_100/v1/caf%C3%A9%20%C3%BC.jpg"},
		{name: "bad_escape", path: up + "%zz.jpg"},
		{name: "control_char", path: up + "a%0Ab.jpg"},
		{name: "scheme_id", path: up + "https:/evil.example/a.jpg"},
		{name: "scheme_id_s3", path: up + "s3:bucket/key.jpg"},
		{name: "colon_later_segment", path: up + "folder/a:b.jpg"},
		{name: "ext_uppercase", path: up + "sample.JPG"},
		{name: "ext_webp", path: up + "w_100/sample.webp"},
		{name: "ext_unknown", path: up + "doc.pdf"},
		{name: "ext_none", path: up + "folder/sample"},
		{name: "ext_with_format", path: up + "f_png/sample.jpg"},
		{name: "ext_with_f_auto", path: up + "f_auto/sample.jpg"},
		{name: "ext_mp4", path: up + "sample.mp4"},

		// fetch
		{name: "fetch", path: "/demo/image/fetch/w_100/https://example.com/a.jpg"},
		{name: "fetch_no_tx", path: "/demo/image/fetch/https://example.com/a.jpg"},
		{name: "fetch_collapsed", path: "/demo/image/fetch/w_100/https:/example.com/a.jpg"},
		{name: "fetch_encoded", path: "/demo/image/fetch/f_auto/https%3A%2F%2Fexample.com%2Fa.jpg%3Fv%3D1"},
		{name: "fetch_escaped_query", path: "/demo/image/fetch/w_300/https://example.com/a%20b.jpg%3Fx%3D1"},
		{name: "fetch_tx_like_path", path: "/demo/image/fetch/w_100/https://example.com/w_200/a.jpg"},
		{name: "fetch_http", path: "/demo/image/fetch/http://example.com/a.jpg"},
		{name: "fetch_ftp", path: "/demo/image/fetch/ftp://example.com/a.jpg"},
		{name: "fetch_relative", path: "/demo/image/fetch/w_100/sample.jpg"},
		{name: "fetch_no_host", path: "/demo/image/fetch/https:///a.jpg"},
		{name: "fetch_private_cdn", path: "/image/fetch/w_100/https://example.com/a.jpg"},
		{name: "fetch_ignores_extension", path: "/demo/image/fetch/https://example.com/a.webp"},

		// signatures
		{name: "sig_unchecked_without_verifier", path: up + "s--AAAAAAAA--/w_100/sample.jpg"},
		{name: "sig_verified", cfg: "signed", path: up + sigSHA1 + "/c_fill,w_300/sample.jpg"},
		{name: "sig_verified_with_version", cfg: "signed", path: up + sigSHA1 + "/c_fill,w_300/v99/sample.jpg"},
		{name: "sig_mismatch", cfg: "signed", path: up + sigSHA1 + "/c_fill,w_301/sample.jpg"},
		{name: "sig_malformed_chars", cfg: "signed", path: up + "s--AAAA!AAA--/w_100/sample.jpg"},
		{name: "sig_malformed_length", cfg: "signed", path: up + "s--AAAAAAA--/w_100/sample.jpg"},
		{name: "sig_bad_segment", cfg: "signed", path: up + "s--AAAAAAAA/w_100/sample.jpg"},
		{name: "sig_empty_segment", path: up + "s----/w_100/sample.jpg"},
		{name: "sig_absent_signed_cfg", cfg: "signed", path: up + "w_100/sample.jpg"},
		{name: "sig_absent_required", cfg: "required", path: up + "w_100/sample.jpg"},
		{name: "sig_short_longonly", cfg: "longonly", path: up + sigSHA1 + "/c_fill,w_300/sample.jpg"},
		{name: "sig_long_longonly", cfg: "longonly", path: up + signPath(t, "long", "c_fill,w_300/sample.jpg") + "/c_fill,w_300/sample.jpg"},
		{name: "sig_sha256_short", cfg: "sha256", path: up + signPath(t, "sha256", "c_fill,w_300/sample.jpg") + "/c_fill,w_300/sample.jpg"},
		{name: "sig_private_cdn", cfg: "signed", path: "/image/upload/" + sigSHA1 + "/c_fill,w_300/sample.jpg"},
		{name: "sig_fetch", cfg: "signed", path: "/demo/image/fetch/" + signPath(t, "sha1", "w_100/https://example.com/a.jpg") + "/w_100/https://example.com/a.jpg"},
		{name: "private_unsigned", path: "/demo/image/private/w_100/sample.jpg"},
		{name: "private_signed", cfg: "signed", path: "/demo/image/private/" + signPath(t, "sha1", "w_100/sample.jpg") + "/w_100/sample.jpg"},
		{name: "authenticated_unsigned_signed_cfg", cfg: "signed", path: "/demo/image/authenticated/w_100/sample.jpg"},

		// presets
		{name: "named_without_store", path: up + "t_thumb/sample.jpg"},
		{name: "named_preset", cfg: "presets", path: up + "t_thumb/sample.jpg"},
		{name: "named_multi", cfg: "presets", path: up + "t_thumb.watermarked/sample.jpg"},
		{name: "named_with_params", cfg: "presets", path: up + "t_thumb,w_200/sample.jpg"},
		{name: "named_chained_then_component", cfg: "presets", path: up + "t_chained/e_grayscale/sample.jpg"},
		{name: "named_unknown", cfg: "presets", path: up + "t_nope/sample.jpg"},
		{name: "named_empty", cfg: "presets", path: up + "t_thumb./sample.jpg"},
		{name: "only_presets_ok", cfg: "only", path: up + "t_thumb/sample.jpg"},
		{name: "only_presets_rejects_params", cfg: "only", path: up + "t_thumb/w_100/sample.jpg"},
		{name: "default_preset", cfg: "defpre", path: up + "q_80,w_100/sample.jpg"},

		// strict mode and unsupported constructs
		{name: "unknown_key", path: up + "w_100,zz_1/sample.jpg"},
		{name: "unknown_key_strict", cfg: "strict", path: up + "w_100,zz_1/sample.jpg"},
		{name: "unsupported_strict", cfg: "strict", path: up + "e_cartoonify/sample.jpg"},
		{name: "video_param", path: up + "so_2,vc_auto/sample.jpg"},
		{name: "video_param_strict", cfg: "strict", path: up + "so_2/sample.jpg"},
		{name: "video_thumbnail", path: "/demo/video/upload/so_1.5,f_jpg/dog.mp4"},
		{name: "video_thumbnail_best", path: "/demo/video/upload/so_auto,w_300,f_webp/dog.mp4"},
		{name: "video_thumbnail_fetch", path: "/demo/video/fetch/so_3,f_auto/https://example.com/dog.webm"},
		{name: "video_thumbnail_percent", path: "/demo/video/upload/so_10p,f_jpg/dog.mp4"},
		{name: "video_thumbnail_bad_offset", path: "/demo/video/upload/so_soon,f_jpg/dog.mp4"},
		{name: "if_component", path: up + "if_w_gt_1000,c_scale,w_1000/e_grayscale/sample.jpg"},
		{name: "if_block", path: up + "if_w_gt_1000/c_scale,w_1000/e_blur/if_end/e_grayscale/sample.jpg"},
		{name: "if_strict", cfg: "strict", path: up + "if_w_gt_1000,w_10/sample.jpg"},
		{name: "variables", path: up + "$size_200,c_scale,w_$size/sample.jpg"},
		{name: "too_many_pipelines", path: up + strings.Repeat("e_blur/", 17) + "sample.jpg"},
	}
	runGolden(t, "grammar", cases)
}

// TestParams covers every parameter with valid and invalid values.
func TestParams(t *testing.T) {
	t.Parallel()
	txs := map[string]string{ //nolint:gosec // transformation strings, not credentials
		// width, height, crop modes
		"w_px":                "w_300",
		"w_rel":               "w_0.5",
		"w_rel_one":           "w_1.0",
		"w_rel_over":          "w_2.5",
		"w_zero":              "w_0",
		"w_negative":          "w_-5",
		"w_huge":              "w_70000",
		"w_expr":              "w_iw_div_2",
		"w_bad":               "w_1x",
		"w_auto":              "w_auto",
		"w_auto_step":         "w_auto:100",
		"w_auto_fallback":     "c_scale,w_auto:100:400",
		"w_auto_breakpoint":   "w_auto:breakpoints",
		"w_auto_bad":          "w_auto:1:2:3",
		"h_px":                "h_200",
		"h_rel":               "h_0.25",
		"h_auto":              "h_auto",
		"c_scale_w":           "c_scale,w_300",
		"c_scale_wh":          "c_scale,w_300,h_200",
		"c_scale_ar":          "c_scale,w_400,ar_16:9",
		"c_scale_ar_float":    "ar_1.5,c_scale,w_400",
		"c_default_scale":     "w_300,h_200",
		"c_fit":               "c_fit,w_300,h_200",
		"c_limit":             "c_limit,w_300,h_200",
		"c_mfit":              "c_mfit,w_300,h_200",
		"c_fill":              "c_fill,w_300,h_200",
		"c_fill_g_compass":    "c_fill,g_north_east,w_300,h_200,x_10,y_0.1",
		"c_fill_g_auto":       "c_fill,g_auto,w_300,h_200",
		"c_fill_g_auto_sub":   "c_fill,g_auto:subject,w_300,h_200",
		"c_fill_g_face":       "c_fill,g_face,w_300,h_200",
		"c_fill_g_faces":      "c_fill,g_faces:center,w_300,h_200",
		"c_fill_g_object":     "c_fill,g_dog,w_300,h_200",
		"c_fill_g_custom":     "c_fill,g_custom,w_300,h_200",
		"c_fill_g_bad":        "c_fill,g_North,w_300",
		"c_fill_zoom":         "c_fill,w_300,z_2",
		"c_lfill":             "c_lfill,w_300,h_200",
		"c_lfill_no_dims":     "c_lfill",
		"c_fill_pad":          "c_fill_pad,g_auto,w_300,h_200",
		"c_pad":               "c_pad,w_300,h_200,b_black",
		"c_pad_gravity":       "c_pad,g_south,w_300,h_200",
		"c_pad_g_auto":        "c_pad,g_auto,w_300,h_200",
		"c_lpad":              "c_lpad,w_300,h_200",
		"c_mpad":              "c_mpad,w_300,h_200",
		"c_crop_xy":           "c_crop,w_150,h_100,x_20,y_30",
		"c_crop_gravity":      "c_crop,g_south_west,w_150,h_100",
		"c_crop_xy_center":    "c_crop,g_xy_center,w_0.5,h_0.5,x_0.25,y_0.75",
		"c_crop_xy_center_px": "c_crop,g_xy_center,w_100,h_100,x_250,y_300",
		"c_crop_rel":          "c_crop,w_0.5,h_0.25",
		"c_crop_ar":           "ar_4:3,c_crop,g_auto",
		"c_crop_no_dims":      "c_crop,g_north",
		"c_crop_zoom":         "c_crop,g_face,w_100,h_100,z_1.5",
		"c_thumb":             "c_thumb,g_face,w_150,h_150,z_0.7",
		"c_auto":              "c_auto,w_300,h_300",
		"c_auto_pad":          "c_auto_pad,g_auto,w_300,h_300",
		"c_imagga_scale":      "c_imagga_scale,w_300",
		"c_bad":               "c_stretch,w_300",
		"g_only":              "g_north",
		"g_xy_only":           "x_10,y_20",
		"g_zoom_only":         "z_2",
		"x_bad":               "c_fill,w_100,x_abc",
		"ar_bad":              "c_scale,w_100,ar_0:1",
		"ar_bad_float":        "c_scale,w_100,ar_-1",
		"z_bad":               "c_thumb,w_100,z_0",

		// effects
		"e_blur":               "e_blur",
		"e_blur_300":           "e_blur:300",
		"e_blur_bad":           "e_blur:0",
		"e_blur_nan":           "e_blur:x",
		"e_sharpen":            "e_sharpen:150",
		"e_unsharp":            "e_unsharp_mask:200",
		"e_pixelate":           "e_pixelate:20",
		"e_pixelate_default":   "e_pixelate",
		"e_grayscale":          "e_grayscale",
		"e_blackwhite":         "e_blackwhite",
		"e_sepia":              "e_sepia",
		"e_sepia_n":            "e_sepia:50",
		"e_negate":             "e_negate",
		"e_brightness":         "e_brightness:30",
		"e_brightness_default": "e_brightness",
		"e_brightness_bad":     "e_brightness:-100",
		"e_contrast":           "e_contrast:-20",
		"e_saturation":         "e_saturation:50",
		"e_gamma":              "e_gamma:40",
		"e_hue":                "e_hue:50",
		"e_vibrance":           "e_vibrance:70",
		"e_red":                "e_red:50",
		"e_green":              "e_green:-50",
		"e_blue":               "e_blue:10",
		"e_colorize":           "co_red,e_colorize:40",
		"e_colorize_default":   "e_colorize",
		"e_colorize_bad_color": "co_nocolor,e_colorize",
		"e_trim":               "e_trim",
		"e_trim_tol_color":     "e_trim:15:rgb:ffffff",
		"e_trim_bad":           "e_trim:200",
		"e_blur_faces":         "e_blur_faces:600",
		"e_pixelate_faces":     "e_pixelate_faces:12",
		"e_blur_region":        "e_blur_region:800,h_0.4,w_0.3,x_0.1,y_0.2",
		"e_blur_region_whole":  "e_blur_region",
		"e_blur_region_px":     "e_blur_region,h_40,w_30,x_10,y_20",
		"e_unsupported":        "e_vignette",
		"e_unknown":            "e_sparkle",
		"co_without_effect":    "co_red",

		// rotation
		"a_45":         "a_45",
		"a_90":         "a_90",
		"a_neg90":      "a_-90",
		"a_450":        "a_450",
		"a_hflip":      "a_hflip",
		"a_vflip":      "a_vflip",
		"a_list":       "a_hflip.90",
		"a_exif":       "a_exif",
		"a_ignore":     "a_ignore",
		"a_auto_right": "a_auto_right",
		"a_bad":        "a_left",

		// background, border, radius, opacity
		"b_named":       "b_lightblue",
		"b_named_uc":    "b_Red",
		"b_rgb3":        "b_rgb:f00",
		"b_rgb4":        "b_rgb:f008",
		"b_rgb6":        "b_rgb:00ff00",
		"b_rgb8":        "b_rgb:00ff0080",
		"b_hash":        "b_%23336699",
		"b_transparent": "b_transparent",
		"b_auto":        "b_auto:border",
		"b_blurred":     "b_blurred:400",
		"b_bad":         "b_rgb:zzz",
		"b_bad_name":    "b_notacolor",
		"bo":            "bo_4px_solid_black",
		"bo_rgb":        "bo_10px_solid_rgb:ff0080",
		"bo_bad":        "bo_4px_dashed_black",
		"bo_bad_w":      "bo_0px_solid_black",
		"r_one":         "r_20",
		"r_two":         "r_10:20",
		"r_three":       "r_10:20:30",
		"r_four":        "r_10:20:30:40",
		"r_max":         "r_max",
		"r_bad":         "r_1:2:3:4:5",
		"r_neg":         "r_-1",
		"o":             "o_60",
		"o_bad":         "o_150",

		// output and delivery
		"f_auto":            "f_auto",
		"f_auto_image":      "f_auto:image",
		"f_webp":            "f_webp",
		"f_jpeg":            "f_jpeg",
		"f_heif":            "f_heif",
		"f_tiff":            "f_tiff",
		"f_jxl":             "f_jxl",
		"f_mp4":             "f_mp4",
		"f_pdf":             "f_pdf",
		"q_80":              "q_80",
		"q_chroma":          "q_70:420",
		"q_chroma_444":      "q_90:444",
		"q_chroma_bad":      "q_70:411",
		"q_zero":            "q_0",
		"q_101":             "q_101",
		"q_auto":            "q_auto",
		"q_auto_best":       "q_auto:best",
		"q_auto_eco":        "q_auto:eco",
		"q_auto_low":        "q_auto:low",
		"q_auto_bad":        "q_auto:great",
		"q_jpegmini":        "q_jpegmini:1",
		"q_then_auto":       "q_80,q_auto",
		"dpr":               "c_scale,w_300,dpr_2.0",
		"dpr_auto":          "c_scale,w_300,dpr_auto",
		"dpr_bad":           "dpr_0",
		"dpr_own_component": "c_scale,w_300/dpr_1.5",
		"cs_srgb":           "cs_srgb",
		"cs_tinysrgb":       "cs_tinysrgb",
		"cs_cmyk":           "cs_cmyk",
		"cs_keep_cmyk":      "cs_keep_cmyk",
		"d":                 "d_avatar.png",
		"d_folder":          "d_fallbacks:avatar.png",
		"d_scheme":          "d_https:%2F%2Fevil.example%2Fa.png",
		"pg":                "pg_3",
		"pg_zero":           "pg_0",
		"pg_range":          "pg_2-4",
		"pg_bad":            "pg_x",
		"dn":                "dn_150",

		// flags
		"fl_lossy":            "fl_lossy",
		"fl_progressive":      "fl_progressive",
		"fl_progressive_none": "fl_progressive:none",
		"fl_multi":            "fl_progressive.strip_profile.immutable_cache",
		"fl_keep_iptc":        "fl_keep_iptc",
		"fl_keep_attribution": "fl_keep_attribution",
		"fl_force_strip":      "fl_force_strip",
		"fl_attachment":       "fl_attachment",
		"fl_attachment_name":  "fl_attachment:my_photo",
		"fl_awebp":            "fl_awebp",
		"fl_animated":         "fl_animated",
		"fl_preserve":         "fl_preserve_transparency",
		"fl_png8":             "fl_png8",
		"fl_getinfo":          "fl_getinfo",
		"fl_rasterize":        "fl_rasterize",
		"fl_unknown":          "fl_sparkle",

		// layers
		"l_legacy":           "fl_relative,g_south_east,l_logo,o_60,w_0.2,x_5,y_5",
		"l_layer_apply":      "l_logos:brand/c_scale,w_80/fl_layer_apply,g_north_west,x_10,y_10",
		"l_fetch_padded":     "l_fetch:aHR0cHM6Ly9leGFtcGxlLmNvbS9sb2dvLnBuZw%3D%3D/fl_layer_apply,g_south",
		"l_fetch_raw":        "g_center,l_fetch:aHR0cHM6Ly9leGFtcGxlLmNvbS9sb2dvLnBuZw",
		"l_fetch_bad":        "l_fetch:!!!",
		"l_fetch_not_http":   "l_fetch:ZmlsZTovLy9ldGMvcGFzc3dk",
		"l_tiled":            "l_logo/fl_layer_apply,fl_tiled",
		"l_text":             "l_text:Arial_40:Hello/fl_layer_apply",
		"l_text_styled":      "l_text:Open%20Sans_32_bold_italic_underline_center:Hi%20%3Cthere%3E%252C%20you,co_rgb:ff0000,g_south_east,x_10,y_5,o_60,a_-15,w_200/fl_layer_apply",
		"l_text_spacing":     "l_text:Arial_20_letter_spacing_4:Spaced",
		"l_text_no_size":     "l_text:Arial:Hello",
		"l_text_no_text":     "l_text:Arial_20",
		"l_image_co":         "l_logo,co_red,e_colorize",
		"l_px_size_rotate":   "l_logo,w_100,h_50,a_30,g_auto",
		"l_rel_without_flag": "l_logo,w_0.5",
		"l_extra_params":     "l_logo,e_grayscale,c_fill",
		"l_scheme_id":        "l_s3:bucket",
		"l_then_resize":      "l_logo/fl_layer_apply/c_scale,w_100",
		"u_underlay":         "u_paper",
		"u_underlay_apply":   "u_paper/c_scale,w_100/fl_layer_apply",
	}
	cases := make([]testCase, 0, len(txs))
	for name, tx := range txs {
		cases = append(cases, testCase{name: name, path: up + tx + "/sample.jpg"})
	}
	runGolden(t, "params", cases)
}

func signPath(tb testing.TB, signer, msg string) string {
	tb.Helper()
	s, err := getFixture(tb).signers[signer].SignCloudinary(msg)
	if err != nil {
		tb.Fatal(err)
	}
	return s
}

func TestNewRejectsEmptyCloud(t *testing.T) {
	t.Parallel()
	for _, clouds := range []map[string]string{{"": "a"}, {"a": ""}} {
		if _, err := cloudinary.New(&cloudinary.Options{Clouds: clouds}); err == nil {
			t.Errorf("New(%v) = nil error", clouds)
		}
	}
	p, err := cloudinary.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "cloudinary" || p.Verifier() != nil {
		t.Errorf("Name %q, Verifier %v", p.Name(), p.Verifier())
	}
}
