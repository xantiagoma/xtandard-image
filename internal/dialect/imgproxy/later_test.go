package imgproxy_test

import (
	"strings"
	"testing"
)

// strictOpt is opt with strict mode on.
func strictOpt(name, options string) testCase {
	return testCase{name: name, cfg: "strict", path: "/unsafe/" + options + src}
}

// TestLaterOptions covers every M2/M3 option of docs/URL-DIALECTS.md §3.2
// under each alias: parsed into the request and recorded in Ignored with
// its milestone, with argument edge cases and errors.
func TestLaterOptions(t *testing.T) {
	t.Parallel()
	wmURL := b64("https://example.com/logo.png")
	runGolden(t, "later", []testCase{
		// crop_aspect_ratio / crop_ar / car (M2)
		opt("crop_aspect_ratio", "crop_aspect_ratio:1.5"),
		opt("crop_ar_enlarge", "c:100:200/crop_ar:1.777:1"),
		opt("car_before_crop_kept", "car:2:t/c:0.5:0.5"),
		opt("car_empty_ratio_keeps", "car:2/car::1"),
		opt("car_negative", "car:-1"),
		opt("car_too_many_args", "car:1:1:1"),
		// objects_position / obj_pos / op (M3)
		opt("objects_position_compass", "objects_position:soea:10:0.2"),
		opt("obj_pos_fp", "obj_pos:fp:0.2:0.8"),
		opt("op_fp_defaults", "op:fp"),
		opt("op_prop", "op:prop"),
		opt("op_kept_across_gravity", "op:no/g:sm"),
		opt("op_prop_args", "op:prop:1"),
		opt("op_bad_type", "op:sm"),
		opt("op_fp_out_of_range", "op:fp:2:0"),
		// adjust / a, brightness / br, contrast / co, saturation / sa (M2)
		opt("adjust_full", "adjust:-20:1.2:0.8"),
		opt("a_partial_merge", "a:10/a::1.5"),
		opt("a_identity", "a:0:1:1"),
		opt("a_brightness_out_of_range", "a:300"),
		opt("a_too_many_args", "a:1:1:1:1"),
		opt("brightness", "brightness:-255"),
		opt("br", "br:12.5"),
		opt("br_out_of_range", "br:256"),
		opt("contrast", "contrast:2"),
		opt("co_negative", "co:-1"),
		opt("saturation", "saturation:0"),
		opt("sa", "sa:1.25"),
		opt("sa_empty", "sa:"),
		// monochrome / mc (M2)
		opt("monochrome", "monochrome:0.5"),
		opt("mc_color", "mc:1:ff0000"),
		opt("mc_zero_off", "mc:0.5/mc:0"),
		opt("mc_bad_intensity", "mc:1.5"),
		opt("mc_bad_color", "mc:1:zz"),
		// duotone / dt (M2)
		opt("duotone", "duotone:0.7"),
		opt("dt_colors", "dt:1:112233:ddeeff"),
		opt("dt_merge_color2", "dt:1:112233/dt:::abcdef"),
		opt("dt_too_many_args", "dt:1:0:0:0"),
		// colorize / col (M2)
		opt("colorize", "colorize:0.5"),
		opt("col_full", "col:1:00ff00:1"),
		opt("col_keep_alpha_only", "col:0.3::t"),
		opt("col_bad_keep_alpha", "col:0.3::x"),
		// gradient / gr (M2)
		opt("gradient", "gradient:0.5"),
		opt("gr_full", "gr:1:ff0000:up:0.2:0.9"),
		opt("gr_angle", "gr:0.8::45"),
		opt("gr_each_direction", "gr:1::left/gr:1::right/gr:1::down"),
		opt("gr_bad_direction", "gr:1::sideways"),
		opt("gr_angle_out_of_range", "gr:1::400"),
		opt("gr_bad_stop", "gr:1:::0:2"),
		// unsharp_masking / ush (M2)
		opt("unsharp_masking", "unsharp_masking:always:2:20"),
		opt("ush_mode_only", "ush:none"),
		opt("ush_merge", "ush:auto:1/ush::3"),
		opt("ush_empty", "ush:"),
		opt("ush_bad_mode", "ush:sometimes"),
		opt("ush_negative_weight", "ush:auto:-1"),
		// blur_areas / ba (M2)
		opt("blur_areas", "blur_areas:5:0:0:0.5:0.5"),
		opt("ba_two_areas", "ba:10:0.1:0.1:0.2:0.2:0.5:0.5:0.25:0.25"),
		opt("ba_zero_sigma_off", "ba:5:0:0:1:1/ba:0"),
		opt("ba_incomplete_area", "ba:5:0:0:1"),
		opt("ba_out_of_range", "ba:5:0:0:1.5:1"),
		// progressive_blur / pbl (M2)
		opt("progressive_blur", "progressive_blur:10"),
		opt("pbl_full", "pbl:5:right:0.1:0.6"),
		opt("pbl_angle", "pbl:5:-30"),
		opt("pbl_zero_off", "pbl:5/pbl:0"),
		opt("pbl_bad_sigma", "pbl:x"),
		// blur_detections / bd, draw_detections / dd, crop_objects / c_obj (M3)
		opt("blur_detections", "blur_detections:10"),
		opt("bd_classes", "bd:5:face:license_plate"),
		opt("bd_zero_off", "bd:5:face/bd:0"),
		opt("bd_empty_class", "bd:5::face"),
		opt("draw_detections", "draw_detections:1"),
		opt("dd_classes", "dd:t:cat:dog"),
		opt("dd_false_off", "dd:1/dd:0"),
		opt("dd_bad", "dd:maybe"),
		opt("crop_objects", "crop_objects:1.2"),
		opt("c_obj_classes", "c_obj:1:face"),
		opt("c_obj_zero_off", "c_obj:0"),
		opt("c_obj_negative", "c_obj:-1"),
		// watermark_url / wmu, watermark_text / wmt (M3)
		opt("watermark_url", "wm:1/watermark_url:"+wmURL),
		opt("wmu_cleared", "wm:1/wmu:"+wmURL+"/wmu:"),
		opt("wmu_without_wm_dropped", "wmu:"+wmURL),
		opt("wmu_bad_base64", "wmu:!!!"),
		opt("wmu_control_chars", "wmu:"+b64("https://e.com/\x01")),
		opt("watermark_text", "wm:0.5/watermark_text:"+b64("<b>Hello</b> ©")),
		opt("wmt_wins_over_wmu", "wm:1/wmt:"+b64("text")+"/wmu:"+wmURL),
		opt("wmt_invalid_utf8", "wmt:"+b64("\xff\xfe")),
		// watermark_size / wms, watermark_rotate / wm_rot / wmr, watermark_shadow / wmsh (M3)
		opt("watermark_size", "wm:1/watermark_size:100:50"),
		opt("wms_height_only", "wm:1/wms::40"),
		opt("wms_negative", "wms:-1"),
		opt("watermark_rotate", "wm:1/watermark_rotate:45"),
		opt("wm_rot_reduced", "wm:1/wm_rot:-450"),
		opt("wmr", "wm:1/wmr:90.5"),
		opt("wmr_bad", "wmr:x"),
		opt("watermark_shadow", "wm:1/watermark_shadow:3"),
		opt("wmsh_negative", "wmsh:-2"),
		opt("watermark_everything", "wm:0.8:soea:5:5:0.2/wmt:"+b64("© ACME")+"/wms:200:0/wmr:15/wmsh:2"),
		// style / stl / st (M2)
		opt("style", "style:"+b64("svg { fill: red }")),
		opt("stl", "stl:"+b64("path{stroke:#000}")),
		opt("st_docs_alias", "st:"+b64("*{}")),
		opt("stl_cleared", "stl:"+b64("a{}")+"/stl:"),
		opt("stl_bad", "stl:***"),
		// dpi (M2)
		opt("dpi", "dpi:300"),
		opt("dpi_zero_resets", "dpi:300/dpi:0"),
		opt("dpi_too_large", "dpi:100000"),
		opt("dpi_float", "dpi:72.5"),
		// color_profile / cp / icc (M2)
		opt("color_profile_srgb", "color_profile:srgb"),
		opt("cp_cmyk", "cp:cmyk"),
		opt("icc_file", "icc:Display%20P3.icc"),
		opt("cp_cleared", "cp:srgb/cp:"),
		opt("cp_path_rejected", "cp:..%2Fetc%2Fpasswd"),
		opt("cp_dotdot_rejected", "cp:.."),
		opt("cp_bad_escape", "cp:%zz"),
		// autoquality / aq (M2)
		opt("autoquality_full", "autoquality:dssim:0.02:60:90:0.001"),
		opt("aq_method_only", "aq:size"),
		opt("aq_each_method", "aq:none/aq:ml"),
		opt("aq_merge", "aq:dssim:0.02/aq:::50:80"),
		opt("aq_bad_method", "aq:fast"),
		opt("aq_min_over_max", "aq:size::90:60"),
		opt("aq_bad_allowed_error", "aq:dssim::::2"),
		opt("aq_size_bytes", "aq:size:10240:10:80"),
		opt("aq_size_too_large", "aq:size:2000000000"),
		// jpeg_options / jpgo (M2)
		opt("jpeg_options_full", "jpeg_options:1:0:1:1:1:3"),
		opt("jpgo_progressive", "jpgo:t"),
		opt("jpgo_merge", "jpgo:1/jpgo::1"),
		opt("jpgo_quant_table_only", "jpgo::::::8"),
		opt("jpgo_bad_quant_table", "jpgo::::::9"),
		opt("jpgo_bad_bool", "jpgo:x"),
		// png_options / pngo (M2)
		opt("png_options_full", "png_options:1:1:64"),
		opt("pngo_quantize", "pngo::t"),
		opt("pngo_one_color", "pngo:::1"),
		opt("pngo_too_many_colors", "pngo:::257"),
		// webp_options / webpo (M2)
		opt("webp_options_full", "webp_options:lossy:1:photo"),
		opt("webpo_near_lossless", "webpo:near_lossless"),
		opt("webpo_every_preset", "webpo:lossless::default/webpo:::picture/webpo:::drawing/webpo:::icon/webpo:::text"),
		opt("webpo_bad_compression", "webpo:lossyish"),
		opt("webpo_bad_preset", "webpo::1:cartoon"),
		// avif_options / avifo (M2)
		opt("avif_options_on", "avif_options:on"),
		opt("avifo_off", "avifo:off"),
		opt("avifo_auto", "avifo:auto"),
		opt("avifo_bad", "avifo:444"),
		// format best (M2)
		opt("format_best", "f:best/aq:dssim"),
		// preserve_hdr / ph, enforce_thumbnail / eth (M2), page / pg, pages / pgs
		opt("hdr_thumbnail_pages", "ph:1/eth:1/pg:1/pgs:2"),
		// video_thumbnail_second / vts, video_thumbnail_keyframes / vtk (M3)
		opt("video_thumbnail_second", "video_thumbnail_second:5"),
		opt("vts_fraction", "vts:2.5"),
		opt("vts_negative", "vts:-1"),
		opt("video_thumbnail_keyframes", "video_thumbnail_keyframes:1"),
		opt("vtk_false", "vtk:0"),
		// video_thumbnail_tile / vtt (M3)
		opt("video_thumbnail_tile", "video_thumbnail_tile:1:3:2:100:50"),
		opt("vtt_full", "vtt:2:4:4:0:0:1:1:1:0.25:0.75"),
		opt("vtt_negative_step_auto", "vtt:-1:2:2:0:0"),
		opt("vtt_zero_step_off", "vtt:1:2:2:0:0/vtt:0"),
		opt("vtt_missing_rows", "vtt:1:2"),
		opt("vtt_bad_focus", "vtt:1:2:2:0:0:0:0:1:2"),
		opt("vtt_too_many_args", "vtt:1:1:1:1:1:1:1:1:1:1:1"),
		// video_thumbnail_animation / vta (M3)
		opt("video_thumbnail_animation", "video_thumbnail_animation:1:100:10:320:240"),
		opt("vta_full", "vta:-1:40:25:0:0:1:0:1:0.5:0.1"),
		opt("vta_zero_step_off", "vta:0:100:10"),
		opt("vta_missing_frames", "vta:1:100"),
		opt("vta_bad_delay", "vta:1:-5:10"),
		// fallback_image_url / fiu (M2)
		opt("fallback_image_url", "fallback_image_url:"+b64("https://example.com/404.png")),
		opt("fiu_cleared", "fiu:"+b64("x")+"/fiu:"),
		opt("fiu_bad", "fiu:%%%"),
		// bypass_cache / bc, cache_tags / ct (M2)
		opt("bypass_cache", "bypass_cache:1"),
		opt("bc_false", "bc:0"),
		opt("cache_tags", "cache_tags:product-1:catalog"),
		opt("ct_replaces", "ct:a:b/ct:c"),
		opt("ct_cleared", "ct:a/ct:"),
		opt("ct_empty_tag", "ct:a::b"),
		opt("ct_comma", "ct:a,b"),
		opt("ct_too_many", "ct:"+strings.Repeat("t:", 64)+"t"),
		// hashsum / hs (M2)
		opt("hashsum_sha256", "hashsum:sha256:"+strings.Repeat("ab", 32)),
		opt("hs_md5_uppercase", "hs:md5:"+strings.Repeat("AB", 16)),
		opt("hs_sha1", "hs:sha1:"+strings.Repeat("0", 40)),
		opt("hs_sha512", "hs:sha512:"+strings.Repeat("f", 128)),
		opt("hs_none_clears", "hs:md5:"+strings.Repeat("0", 32)+"/hs:none"),
		opt("hs_wrong_length", "hs:md5:00"),
		opt("hs_not_hex", "hs:md5:"+strings.Repeat("g", 32)),
		opt("hs_bad_type", "hs:crc32:00000000"),
		{name: "hs_kept_with_base64_source", path: "/unsafe/hs:md5:" + strings.Repeat("1", 32) + srcB64 + ".png"},
		// max_result_dimension is a security option (options_test.go);
		// combined chains across pipelines.
		opt("later_chained_pipelines", "a:10/mc:0.5/-/dt:1/ush:auto/-/pbl:3:up/ct:x/dpi:72"),
		opt("every_later_pipeline_option", strings.Join([]string{
			"car:1", "op:ce", "a:1:1.1:0.9", "br:5", "co:1.1", "sa:0.9", "mc:0.1", "dt:0.2", "col:0.3",
			"gr:0.4", "ush:auto", "ba:1:0:0:1:1", "pbl:2", "bd:3", "dd:1", "c_obj:1", "wm:1",
			"wmu:" + wmURL, "wms:10:10", "wmr:10", "wmsh:1", "stl:" + b64("a{}"), "vts:1", "vtk:1",
			"vtt:1:1:1:1:1", "vta:1:1:1:1:1",
		}, "/")),
		opt("every_later_request_option", strings.Join([]string{
			"dpi:72", "cp:srgb", "aq:size", "jpgo:1", "pngo:1", "webpo:lossy", "avifo:on",
			"fiu:" + wmURL, "bc:1", "ct:x", "hs:none",
		}, "/")),
		// strict mode: every option above is known; unknown names are 400.
		strictOpt("strict_later_options_accepted", "a:1/mc:0.5/vts:1/ct:x/stl:"+b64("a{}")),
		strictOpt("strict_unknown_rejected", "a:1/nonsense:1"),
		strictOpt("strict_unknown_single_letter", "x:1"),
		strictOpt("strict_info_only_option", "palette:8"),
		strictOpt("strict_gif_options_still_ignored", "gifo:1"),
		opt("unknown_recorded", "a:1/nonsense:1/palette:8"),
	})
}
