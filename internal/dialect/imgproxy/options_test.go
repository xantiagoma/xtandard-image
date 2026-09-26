package imgproxy_test

import (
	"strings"
	"testing"
)

// opt builds a case "/unsafe/<options>/plain/https://example.com/a.jpg".
func opt(name, options string) testCase {
	return testCase{name: name, path: "/unsafe/" + options + src}
}

// secOpt is opt with security options allowed.
func secOpt(name, options string) testCase {
	return testCase{name: name, cfg: "security", path: "/unsafe/" + options + src}
}

// TestOptions covers every M1 option of docs/URL-DIALECTS.md §3.2 under
// each alias, argument edge cases and the error for each invalid form.
func TestOptions(t *testing.T) {
	t.Parallel()
	runGolden(t, "options", []testCase{
		// resize / rs
		opt("resize_full", "resize:fill:300:400:1:1:so:5:6"),
		opt("rs_docs", "rs:fill:300:400:0"),
		opt("rs_width_only", "rs::100"),
		opt("rs_type_only", "rs:fill-down"),
		opt("rs_zero_size", "rs:fit:0:0"),
		opt("rs_extend_no_gravity", "rs:fit:100:100:0:1"),
		opt("rs_merges_argwise", "rs:fill:100:100/rs::200"),
		opt("rs_too_many_args", "rs:fit:1:1:1:1:ce:0:0:9"),
		opt("rs_bad_type", "rs:cover:100:100"),
		opt("rs_bad_width", "rs:fit:-1:100"),
		opt("rs_bad_enlarge", "rs:fit:1:1:yes"),
		opt("rs_bad_extend_gravity", "rs:fit:1:1:0:1:sm"),
		// size / s
		opt("size_full", "size:100:200:1:1:ce"),
		opt("s_width", "s:100"),
		opt("s_height_only", "s::200"),
		opt("s_too_many_args", "s:1:1:1:1:ce:0:0:0"),
		// resizing_type / rt
		opt("resizing_type_force", "resizing_type:force"),
		opt("rt_auto", "rt:auto"),
		opt("rt_bad", "rt:crop"),
		opt("rt_too_many_args", "rt:fit:fill"),
		// resizing_algorithm / ra
		opt("resizing_algorithm_cubic", "resizing_algorithm:cubic"),
		opt("ra_nearest", "ra:nearest"),
		opt("ra_linear", "ra:linear"),
		opt("ra_lanczos2", "ra:lanczos2"),
		opt("ra_bad", "ra:mitchell"),
		// width / w, height / h
		opt("width", "width:640"),
		opt("w", "w:320"),
		opt("w_zero", "w:0"),
		opt("w_negative", "w:-5"),
		opt("w_float", "w:1.5"),
		opt("w_overflow", "w:99999999999"),
		opt("height", "height:480"),
		opt("h", "h:240"),
		opt("h_bad", "h:abc"),
		// min-width / mw, min-height / mh
		opt("min_width", "min-width:100"),
		opt("mw", "mw:50"),
		opt("min_height", "min-height:80"),
		opt("mh", "mh:40"),
		opt("mw_bad", "mw:-1"),
		// zoom / z
		opt("zoom_one_arg", "zoom:2"),
		opt("z_two_args", "z:1.5:0.5"),
		opt("z_zero", "z:0"),
		opt("z_too_many_args", "z:1:2:3"),
		opt("z_bad_y", "z:1:-2"),
		// dpr
		opt("dpr", "dpr:2"),
		opt("dpr_fraction", "dpr:1.5"),
		opt("dpr_zero", "dpr:0"),
		opt("dpr_nan", "dpr:NaN"),
		opt("dpr_inf", "dpr:Inf"),
		// enlarge / el
		opt("enlarge_true", "enlarge:true"),
		opt("el_1", "el:1"),
		opt("el_t", "el:t"),
		opt("el_empty_false", "el:"),
		opt("el_bad", "el:yes"),
		// extend / ex
		opt("extend_full", "extend:t:so:10:20"),
		opt("ex_1", "ex:1"),
		opt("ex_focus_point", "ex:1:fp:0.5:0.25"),
		opt("ex_smart_rejected", "ex:1:sm"),
		opt("ex_empty_gravity", "ex:1:"),
		opt("ex_too_many_args", "ex:1:ce:0:0:0"),
		opt("ex_merges_offsets", "ex:1:no:5:6/ex:0:so"),
		// extend_aspect_ratio / extend_ar / exar
		opt("extend_aspect_ratio", "extend_aspect_ratio:1:ce"),
		opt("extend_ar", "extend_ar:t"),
		opt("exar", "exar:1:nowe:1:2"),
		opt("exar_bad", "exar:1:xx"),
		// gravity / g
		opt("gravity_compass_offsets", "gravity:no:10:20"),
		opt("g_every_compass", "g:ce/g:so/g:ea/g:we/g:noea/g:soea/g:sowe/g:nowe"),
		opt("g_smart", "g:sm"),
		opt("g_smart_args", "g:sm:1"),
		opt("g_focus_point", "g:fp:0.1:0.9"),
		opt("g_focus_point_out_of_range", "g:fp:2:0"),
		opt("g_focus_point_missing_y", "g:fp:0.5"),
		opt("g_relative_offsets", "g:ea:0.25:0.5"),
		opt("g_negative_offsets", "g:we:-10:-20"),
		opt("g_offsets_kept_on_type_change", "g:no:10:20/g:so"),
		opt("g_bad_type", "g:middle"),
		opt("g_bad_offset", "g:no:a"),
		opt("g_too_many_args", "g:no:1:2:3"),
		opt("g_obj", "g:obj:face:cat"),
		opt("g_obj_all", "g:obj"),
		opt("g_obj_empty_class", "g:obj::cat"),
		opt("g_objw", "g:objw:face:2:cat:0.5"),
		opt("g_objw_odd", "g:objw:face"),
		opt("g_objw_bad_weight", "g:objw:face:-1"),
		// crop / c
		opt("crop_absolute", "crop:100:200"),
		opt("c_relative_smart", "c:0.5:0.5:sm"),
		opt("c_focus_point", "c:100:200:fp:0.3:0.7"),
		opt("c_width_only", "c:100"),
		opt("c_fractional_px_truncated", "c:150.9:10.2"),
		opt("c_compass_offsets", "c:100:100:noea:5:6"),
		opt("c_merges", "c:100:200:we/c:50"),
		opt("c_negative", "c:-1"),
		opt("c_bad_gravity", "c:100:200:xx"),
		opt("c_huge", "c:1e300"),
		// trim / t
		opt("trim_threshold", "trim:10"),
		opt("t_full", "t:10:ff00ff:1:0"),
		opt("t_short_color", "t:5.5:abc:t"),
		opt("t_disabled_by_empty_threshold", "t:10/t:"),
		opt("t_bad_color", "t:10:ggg"),
		opt("t_too_many_args", "t:10:fff:t:t:x"),
		opt("t_bad_threshold", "t:x"),
		// padding / pd
		opt("padding_one", "padding:10"),
		opt("pd_two", "pd:1:2"),
		opt("pd_three", "pd:1:2:3"),
		opt("pd_four", "pd:1:2:3:4"),
		opt("pd_empty_top", "pd::5"),
		opt("pd_zero_is_none", "pd:10/pd:0"),
		opt("pd_negative", "pd:-1"),
		opt("pd_too_many_args", "pd:1:2:3:4:5"),
		// auto_rotate / ar
		opt("auto_rotate_true", "auto_rotate:1"),
		opt("ar_false", "ar:0"),
		// rotate / rot
		opt("rotate_90", "rotate:90"),
		opt("rot_negative", "rot:-90"),
		opt("rot_450", "rot:450"),
		opt("rot_45", "rot:45"),
		// flip / fl
		opt("flip_h", "flip:1:0"),
		opt("fl_v", "fl:0:t"),
		opt("fl_empty_h", "fl::1"),
		opt("fl_too_many_args", "fl:1:1:1"),
		// background / bg
		opt("background_hex", "background:ff0000"),
		opt("bg_short_hex", "bg:f0a"),
		opt("bg_rgb", "bg:0:128:255"),
		opt("bg_cleared", "bg:fff/bg:"),
		opt("bg_two_args", "bg:1:2"),
		opt("bg_rgb_out_of_range", "bg:300:0:0"),
		opt("bg_bad_hex", "bg:gg0000"),
		opt("bg_hex_with_alpha_rejected", "bg:ff000080"),
		// background_alpha / bga (M2)
		opt("background_alpha", "bg:ffffff/background_alpha:0.5"),
		opt("bga_before_bg", "bga:0.25/bg:000"),
		opt("bga_without_bg", "bga:0.5"),
		opt("bga_out_of_range", "bga:2"),
		// blur / bl, sharpen / sh, pixelate / pix
		opt("blur", "blur:3.5"),
		opt("bl_zero", "bl:0"),
		opt("bl_negative", "bl:-1"),
		opt("sharpen", "sharpen:1"),
		opt("sh", "sh:0.5"),
		opt("pixelate", "pixelate:5"),
		opt("pix", "pix:10"),
		opt("pix_float", "pix:1.5"),
		// watermark / wm
		opt("watermark_full", "watermark:1:soea:10:20:0.3"),
		opt("wm_opacity_only", "wm:0.5"),
		opt("wm_replicate", "wm:1:re"),
		opt("wm_replicate_spacing", "wm:1:re:10:10"),
		opt("wm_chessboard", "wm:0.5:ch"),
		opt("wm_zero_opacity_dropped", "wm:0"),
		opt("wm_merges_argwise", "wm:1:nowe:5:5/wm:0.5"),
		opt("wm_empty_position_kept", "wm:1:we/wm:0.3::7"),
		opt("wm_opacity_out_of_range", "wm:2"),
		opt("wm_bad_position", "wm:1:xx"),
		opt("wm_smart_position", "wm:1:sm"),
		opt("wm_zero_scale", "wm:1:ce:0:0:0"),
		opt("wm_too_many_args", "wm:1:ce:0:0:1:1:1:1"),
		// strip_metadata / sm, keep_copyright / kcr, strip_color_profile / scp
		opt("strip_metadata", "strip_metadata:1"),
		opt("sm_false", "sm:0"),
		opt("keep_copyright", "keep_copyright:t"),
		opt("kcr_false", "kcr:f"),
		opt("strip_color_profile", "strip_color_profile:1"),
		opt("scp_false", "scp:false"),
		// preserve_hdr / ph, enforce_thumbnail / eth (M2)
		opt("preserve_hdr", "preserve_hdr:1"),
		opt("ph", "ph:0"),
		opt("enforce_thumbnail", "enforce_thumbnail:1"),
		opt("eth", "eth:0"),
		opt("eth_bad", "eth:2"),
		// quality / q, format_quality / fq, max_bytes / mb
		opt("quality", "quality:80"),
		opt("q_zero", "q:0"),
		opt("q_100", "q:100"),
		opt("q_out_of_range", "q:101"),
		opt("format_quality", "format_quality:avif:50"),
		opt("fq_pairs", "fq:jpeg:80:webp:70:jpg:85"),
		opt("fq_zero_drops_entry", "fq:png:60:webp:70/fq:png:0"),
		opt("fq_odd", "fq:jpeg"),
		opt("fq_bad_format", "fq:xyz:1"),
		opt("fq_bad_quality", "fq:png:x"),
		opt("max_bytes", "max_bytes:100000"),
		opt("mb", "mb:0"),
		opt("mb_negative", "mb:-1"),
		// format / f / ext
		opt("format_webp", "format:webp"),
		opt("f_jpg", "f:jpg"),
		opt("ext_png", "ext:png"),
		opt("f_every_format", "f:jpeg/f:avif/f:jxl/f:heic/f:gif/f:tiff/f:bmp/f:ico/f:svg"),
		opt("f_best", "f:best"),
		opt("f_bad", "f:xyz"),
		opt("f_empty", "f:"),
		opt("f_too_many_args", "f:png:jpg"),
		// skip_processing / skp / sp
		opt("skip_processing", "skip_processing:svg:gif"),
		opt("skp_appends", "skp:jpg/skp:png"),
		opt("sp_js_core_alias", "sp:webp"),
		opt("skp_bad", "skp:pdf"),
		// raw
		opt("raw_true", "raw:1"),
		opt("raw_false", "raw:0"),
		opt("raw_bad", "raw:maybe"),
		// cachebuster / cb
		opt("cachebuster", "cachebuster:v2"),
		opt("cb", "cb:abc123"),
		opt("cb_cleared", "cb:x/cb:"),
		opt("cb_too_many_args", "cb:a:b"),
		// expires / exp
		opt("expires_future", "expires:4102444800"),
		opt("exp_zero_never", "exp:0"),
		opt("exp_past", "exp:1000"),
		opt("exp_bad", "exp:tomorrow"),
		// filename / fn
		opt("filename_plain", "filename:cat.jpg"),
		opt("fn_encoded", "fn:"+b64("chat résumé.png")+":1"),
		opt("fn_not_encoded_flag", "fn:plain-name:0"),
		opt("fn_bad_base64", "fn:!!!:1"),
		opt("fn_too_many_args", "fn:a:0:x"),
		// return_attachment / att
		opt("return_attachment", "return_attachment:1"),
		opt("att_false", "att:0"),
		// preset / pr
		opt("preset", "preset:thumb"),
		opt("pr_many", "pr:a:b/pr:c"),
		opt("pr_empty", "pr:"),
		// disable_animation / da, page / pg, pages / pgs
		opt("disable_animation", "disable_animation:1"),
		opt("da_false", "da:0"),
		opt("page", "page:0"),
		opt("pg", "pg:2"),
		opt("pg_negative", "pg:-1"),
		opt("pages", "pages:3"),
		opt("pgs_all", "pgs:-1"),
		opt("pgs_zero", "pgs:0"),
		// security options
		secOpt("max_src_resolution", "max_src_resolution:12.5"),
		secOpt("msr", "msr:1"),
		secOpt("max_src_file_size", "max_src_file_size:1048576"),
		secOpt("msfs", "msfs:5"),
		secOpt("max_animation_frames", "max_animation_frames:10"),
		secOpt("maf", "maf:1"),
		secOpt("maf_zero", "maf:0"),
		secOpt("max_animation_frame_resolution", "max_animation_frame_resolution:3.5"),
		secOpt("mafr", "mafr:2"),
		secOpt("max_result_dimension", "max_result_dimension:4000"),
		secOpt("mrd", "mrd:100"),
		opt("msr_not_allowed", "msr:1"),
		opt("mrd_not_allowed", "mrd:100"),
		// removed, later-milestone and unknown options
		opt("gif_options_removed", "gif_options:1:1"),
		opt("gifo_removed", "gifo:1"),
		opt("later_options_recorded", "a:10:1.2:0.8/br:10/co:1/sa:1/mc:0.5/dt:0.5/ush:auto/ba:1:0:0:1:1/pbl:5/bd:5/dd:1/c_obj:1/col:0.5/gr:0.5"),
		opt("later_watermark_options", "wmu:"+b64("https://e.com/w.png")+"/wmt:dGV4dA/wms:10:10/wm_rot:45/wmr:45/wmsh:2/stl:Y3Nz"),
		opt("later_output_options", "dpi:300/cp:srgb/icc:srgb/aq:dssim/jpgo:1/pngo:1/webpo:lossy/avifo:on"),
		opt("later_misc_options", "vts:5/vtk:1/vtt:1:2:2/vta:1:1:10/fiu:eA/bc:1/ct:a:b/hs:md5:00/op:ce/obj_pos:ce/car:1.5/crop_ar:1/crop_aspect_ratio:1/objects_position:ce"),
		opt("later_long_names", "adjust:1/brightness:1/contrast:1/saturation:1/monochrome:1/duotone:1/unsharp_masking:auto/blur_areas:1/progressive_blur:1/blur_detections:1/draw_detections:1/crop_objects:1/colorize:1/gradient:1"),
		opt("later_long_names_2", "watermark_url:x/watermark_text:x/watermark_size:1/watermark_rotate:1/watermark_shadow:1/style:x/color_profile:x/autoquality:x/jpeg_options:x/png_options:x/webp_options:x/avif_options:x"),
		opt("later_long_names_3", "video_thumbnail_second:1/video_thumbnail_keyframes:1/video_thumbnail_tile:1/video_thumbnail_animation:1/fallback_image_url:x/bypass_cache:1/cache_tags:x/hashsum:x"),
		opt("unknown_option", "nonsense:1:2"),
		// long option chains in one pipeline
		opt("combined_typical", "rs:fill:800:600:1/g:sm/q:82/f:webp/sm:1/dpr:2/sh:0.3"),
		opt("combined_every_m1_pipeline_option", strings.Join([]string{
			"rs:fill:100:100", "ra:linear", "mw:10", "mh:10", "z:1.1", "dpr:1.5", "el:1", "ex:1:so",
			"exar:1:no", "g:fp:0.5:0.5", "c:0.9:0.9", "t:5", "pd:1:2", "ar:0", "rot:180", "fl:1:1",
			"bg:112233", "bl:1", "sh:1", "pix:2", "wm:0.8:ea:1:2:0.5", "da:1",
		}, "/")),
	})
}
