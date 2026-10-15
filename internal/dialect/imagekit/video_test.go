package imagekit_test

import (
	"strings"
	"testing"
)

const vid = "/sample-video.mp4"

// vq builds a query-placement video case.
func vq(name, tr string) testCase { return testCase{name: name, path: vid, query: "tr=" + tr} }

// TestVideo covers video transformations (WP-125) with the examples of
// https://imagekit.io/docs/video-transformation, video-resize-and-crop,
// trim-videos and video-optimization: every parameter with valid and
// invalid values, the output selection and signatures.
func TestVideo(t *testing.T) {
	t.Parallel()
	signed := func(path, query string) string {
		out, err := getFixture(t).signer.SignImageKitQuery(path, query, 0)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	runGolden(t, "video", []testCase{
		// Output selection.
		{name: "plain", path: vid},
		{name: "path_tr", path: "/tr:w-300,h-300/sample-video.mp4"},
		{name: "ext_webm", path: "/clips/a.webm"},
		{name: "ext_mov", path: "/clips/a.MOV"},
		{name: "ik_video_mp4", path: "/clips/a.mov/ik-video.mp4"},
		{name: "ik_video_mp4_f_webm", path: "/clips/a.mov/ik-video.mp4", query: "tr=f-webm"},
		{name: "ik_master", path: "/clips/a.mp4/ik-master.m3u8", query: "tr=sr-240_360_480"},
		// Adaptive bitrate streaming (WP-126).
		{name: "ik_master_mpd_all", path: "/clips/a.mp4/ik-master.mpd", query: "tr=sr-240_360_480_720_1080_1440_2160"},
		{name: "ik_master_no_sr", path: "/clips/a.mov/ik-master.m3u8"},
		{name: "ik_master_path_tr", path: "/tr:so-5,du-30,sr-360_720/clips/a.mp4/ik-master.m3u8"},
		{name: "ik_master_trim_crop_codec", path: "/clips/a.mp4/ik-master.m3u8", query: "tr=so-2,du-10,cm-extract,w-640,h-360,vc-av1,q-60,fps-25,sr-240_480"},
		{name: "ik_master_resize_dropped", path: "/clips/a.mp4/ik-master.m3u8", query: "tr=w-640,sr-240"},
		{name: "ik_master_resize_strict", cfg: "strict", path: "/clips/a.mp4/ik-master.m3u8", query: "tr=w-640,sr-240"},
		{name: "ik_master_vc_none", path: "/clips/a.mp4/ik-master.m3u8", query: "tr=vc-none"},
		{name: "ik_master_layer", path: "/clips/a.mp4/ik-master.m3u8", query: "tr=l-image,i-logo.png,lx-10,ly-N10,w-80,lso-1,leo-3,l-end,sr-240_360"},
		{name: "ik_master_bad_ext", path: "/clips/a.mp4/ik-master.ts"},
		{name: "ik_master_sr_bad", path: "/clips/a.mp4/ik-master.m3u8", query: "tr=sr-240_abc"},
		{name: "ik_master_sr_tiny", path: "/clips/a.mp4/ik-master.m3u8", query: "tr=sr-32"},
		{name: "sr_without_master", path: "/clips/a.mp4", query: "tr=sr-240_360"},
		{name: "sr_without_master_strict", cfg: "strict", path: "/clips/a.mp4", query: "tr=sr-240_360"},
		{name: "thumbnail_stays_image", path: "/clips/a.mp4/ik-thumbnail.jpg", query: "tr=so-2"},
		vq("f_mp4", "f-mp4"),
		vq("f_webm", "f-webm"),
		vq("f_auto", "f-auto"),
		vq("f_orig", "f-orig"),
		vq("f_jpg_frame", "f-jpg,w-100"),
		vq("f_png_last_wins", "f-mp4:f-png"),
		// Trimming.
		vq("trim_so_du", "so-5,du-5"),
		vq("trim_so_eo", "so-10.55,eo-20"),
		vq("trim_eo_du", "so-1,eo-5,du-3"),
		vq("trim_expression", "so-idu_mul_0.1,du-idu_mul_0.4"),
		vq("trim_expression_eo", "eo-idu_sub_20"),
		vq("trim_bad", "so-soon"),
		vq("trim_eo_before_so", "so-5,eo-2"),
		// Resize and crop.
		vq("width", "w-300"),
		vq("width_height", "w-300,h-300"),
		vq("maintain_ratio_focus", "w-400,h-300,c-maintain_ratio,fo-top_left"),
		vq("focus_auto", "w-400,h-300,fo-auto"),
		vq("force", "w-400,h-300,c-force"),
		vq("at_max", "w-400,h-300,c-at_max"),
		vq("at_max_enlarge", "w-400,h-300,c-at_max_enlarge"),
		vq("at_least", "w-400,h-300,c-at_least"),
		vq("pad_resize", "w-400,h-300,cm-pad_resize,bg-F3F3F3"),
		vq("pad_resize_focus", "w-400,h-300,cm-pad_resize,fo-bottom"),
		vq("pad_blurred", "w-400,h-300,cm-pad_resize,bg-blurred"),
		vq("extract", "w-200,h-200,cm-extract"),
		vq("extract_focus", "w-200,h-200,cm-extract,fo-right"),
		vq("extract_xy", "w-200,h-200,cm-extract,x-10,y-20"),
		vq("extract_then_resize", "w-400,h-400,cm-extract:w-200"),
		vq("resize_then_extract", "w-400:w-200,h-200,cm-extract"),
		vq("pad_extract", "w-400,h-400,cm-pad_extract"),
		vq("aspect_ratio", "ar-4-1,w-400"),
		vq("aspect_ratio_underscore", "ar-16_9,h-360"),
		vq("aspect_ratio_alone", "ar-1-1"),
		vq("relative_width", "w-0.5"),
		vq("expression_width", "w-iw_div_2"),
		vq("width_bad", "w-0"),
		vq("dpr", "w-300,dpr-2"),
		vq("dpr_auto", "w-300,dpr-auto"),
		vq("xc", "w-100,h-100,cm-extract,xc-50"),
		// Codecs, quality, frame rate, rotation.
		vq("vc_h264", "vc-h264"),
		vq("vc_vp9", "vc-vp9"),
		vq("vc_av1", "vc-av1"),
		vq("vc_vp9_mp4", "vc-vp9,f-mp4"),
		vq("vc_none", "vc-none"),
		vq("vc_none_opus", "vc-none,ac-opus"),
		vq("vc_bad", "vc-mpeg2"),
		vq("ac_none", "ac-none"),
		vq("ac_aac", "ac-aac"),
		vq("ac_opus", "ac-opus"),
		vq("ac_none_vc_none", "vc-none,ac-none"),
		vq("ac_bad", "ac-mp3"),
		vq("quality", "q-90"),
		vq("quality_bad", "q-0"),
		vq("fps", "fps-24"),
		vq("fps_bad", "fps-1000"),
		vq("rotate", "rt-90"),
		vq("rotate_negative", "rt-N90"),
		vq("rotate_45", "rt-45"),
		vq("rotate_auto", "rt-auto"),
		// Not applied.
		vq("bitrate", "br-500"),
		vq("streaming", "sr-240_360"),
		vq("layer_image", "l-image,i-logo.png,w-100,l-end,w-640"),
		vq("layer_text", "l-text,i-Hello,fs-32,l-end"),
		vq("layer_video", "l-video,i-intro.mp4,l-end"),
		vq("layer_window", "l-image,i-logo.png,lx-10,ly-N10,w-80,o-60,lso-1,leo-3,l-end"),
		vq("layer_duration", "l-image,i-logos@@brand.png,lfo-top_right,lso-2,ldu-1.5,l-end"),
		vq("layer_text_window", "l-text,i-Hello%20World,fs-32,co-FFFF00,tg-b,lfo-bottom,ldu-2,l-end"),
		vq("layer_leo_ldu", "l-image,i-logo.png,lso-1,leo-2,ldu-5,l-end"),
		vq("layer_leo_before_lso", "l-image,i-logo.png,lso-3,leo-2,l-end"),
		vq("layer_expression", "l-image,i-logo.png,lso-idu_mul_0.5,l-end"),
		vq("layer_audio_only", "l-image,i-logo.png,l-end,vc-none"),
		vq("layer_nested", "l-image,i-a.png,l-text,i-x,l-end,lso-1,l-end"),
		vq("layer_two_steps", "w-640:l-image,i-a.png,lfo-top_left,l-end:l-text,i-hi,lfo-bottom_right,l-end"),
		vq("layer_too_many", strings.Repeat("l-image,i-a.png,l-end,", 9)),
		vq("image_effect", "e-grayscale"),
		vq("border", "b-5_FF0000"),
		vq("named", "n-thumb"),
		vq("unknown", "zz-1"),
		{name: "unknown_strict", cfg: "strict", path: vid, query: "tr=zz-1"},
		{name: "layer_strict", cfg: "strict", path: vid, query: "tr=l-image,i-logo.png,l-end"},
		{name: "approximated_strict", cfg: "strict", path: vid, query: "tr=w-400,h-300,fo-face"},
		// A combined URL, the query extras and signatures.
		vq("combined", "so-2,du-5,w-640,h-360,fo-top,q-60,vc-h264,ac-aac,fps-30:rt-180"),
		{name: "attachment", path: vid, query: "tr=w-100&ik-attachment=true&v=2"},
		{name: "signed", cfg: "signed", path: vid, query: signed(vid, "tr=w-300,so-1")},
		{name: "signed_path", cfg: "signed", path: "/tr:w-300/sample-video.mp4", query: signed("/tr:w-300/sample-video.mp4", "")},
		{name: "signed_tampered", cfg: "signed", path: vid, query: "tr=w-301&" + sigParam(signed(vid, "tr=w-300,so-1"))},
		{name: "unsigned_required", cfg: "required", path: vid, query: "tr=w-300"},
		{name: "signed_layer", cfg: "signed", path: vid, query: signed(vid, "tr=l-image,i-logo.png,lfo-top_left,lso-1,l-end")},
	})
}

// sigParam is the ik-s parameter of a signed query.
func sigParam(query string) string {
	i := strings.Index(query, "ik-s=")
	if i < 0 {
		return ""
	}
	s, _, _ := strings.Cut(query[i:], "&")
	return s
}
