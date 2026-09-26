package imagekit_test

import (
	"errors"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imagekit"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

const img = "/folder/a.jpg"

// q builds a query-placement case.
func q(name, tr string) testCase { return testCase{name: name, path: img, query: "tr=" + tr} }

// TestGrammar covers placements, chains, sources and the query.
func TestGrammar(t *testing.T) {
	t.Parallel()
	runGolden(t, "grammar", []testCase{
		{name: "no_transform", path: img},
		{name: "path_first_segment", path: "/tr:w-300,h-200/folder/a.jpg"},
		{name: "path_middle_segment", path: "/folder/tr:w-300/a.jpg"},
		{name: "path_escaped_colon", path: "/tr%3Aw-300/folder/a.jpg"},
		{name: "path_escaped_value", path: "/tr:l-text,i-Hello%20World,l-end/a.jpg"},
		{name: "path_last_segment_is_file", path: "/tr:w-300"},
		{name: "path_two_tr_segments", path: "/tr:w-1/tr:w-2/a.jpg"},
		{name: "path_bad_escape", path: "/tr:w-%zz/a.jpg"},
		{name: "query", path: img, query: "tr=w-300,h-200"},
		{name: "query_encoded_commas", path: img, query: "tr=w-300%2Ch-200%2Cc-force"},
		{name: "query_twice", path: img, query: "tr=w-1&tr=w-2"},
		{name: "query_bad_escape", path: img, query: "tr=w-%zz"},
		{name: "both_path_wins", path: "/tr:w-300/a.jpg", query: "tr=w-100"},
		{name: "both_strict", cfg: "strict", path: "/tr:w-300/a.jpg", query: "tr=w-100"},
		{name: "chain_three", path: img, query: "tr=w-400,h-300:rt-90:bl-4"},
		{name: "chain_request_scoped_last_wins", path: img, query: "tr=q-50:q-70"},
		{name: "chain_empty_steps_skipped", path: img, query: "tr=:w-300::,:"},
		{name: "chain_too_long", path: img, query: "tr=w-1:w-2:w-3:w-4:w-5:w-6:w-7:w-8:w-9:w-10:w-11:w-12:w-13:w-14:w-15:w-16:w-17"},
		{name: "empty_params_skipped", path: img, query: "tr=w-300,,h-200,"},
		{name: "missing_source", path: "/tr:w-300/", query: ""},
		{name: "empty_path", path: ""},
		{name: "source_unicode", path: "/sdk-testing-files/%E0%A4%B9%E0%A4%BF.png"},
		{name: "source_scheme_like", path: "/https:/evil.example/a.jpg"},
		{name: "source_control_char", path: "/a%00.jpg"},
		{name: "source_bad_escape", path: "/a%zz.jpg"},
		{name: "cache_buster", path: img, query: "v=123&tr=w-300&updatedAt=1"},
		{name: "attachment", path: img, query: "ik-attachment=true"},
		{name: "attachment_invalid", path: img, query: "ik-attachment=maybe"},
		{name: "endpoint_path", cfg: "endpoint", path: "/demo/tr:w-300/folder/a.jpg"},
		{name: "endpoint_path_outside", cfg: "endpoint", path: "/other/folder/a.jpg"},
		{name: "endpoint_path_prefix_only", cfg: "endpoint", path: "/demox/a.jpg"},
		{name: "unknown_param", path: img, query: "tr=w-300,foo-1"},
		{name: "unknown_param_strict", cfg: "strict", path: img, query: "tr=w-300,foo-1"},
		{name: "unsupported_param_strict", cfg: "strict", path: img, query: "tr=e-bgremove"},
	})
}

// TestParams covers every parameter with valid and invalid values.
func TestParams(t *testing.T) {
	t.Parallel()
	runGolden(t, "params", []testCase{
		// Size.
		q("w", "w-300"),
		q("w_relative", "w-0.5"),
		q("w_auto", "w-auto"),
		q("w_expression", "w-iw_div_2"),
		q("w_zero", "w-0"),
		q("w_one_float", "w-1.0"),
		q("w_negative", "w--3"),
		q("w_huge", "w-99999999"),
		q("h", "h-200"),
		q("h_auto", "h-auto"),
		q("h_invalid", "h-abc"),
		q("ar", "w-400,ar-16-9"),
		q("ar_underscore", "h-300,ar-4_3"),
		q("ar_invalid", "ar-4"),
		q("ar_zero", "ar-0-3"),
		q("ar_expression", "ar-iar_div_2"),
		// Crop strategies.
		q("c_default", "w-300,h-300"),
		q("c_maintain_ratio", "w-300,h-300,c-maintain_ratio"),
		q("c_maintain_ratio_no_enlarge", "w-300,h-300,c-maintain_ratio_no_enlarge"),
		q("c_force", "w-300,h-300,c-force"),
		q("c_at_max", "w-300,h-300,c-at_max"),
		q("c_at_max_enlarge", "w-300,h-300,c-at_max_enlarge"),
		q("c_at_least", "w-300,h-300,c-at_least"),
		q("c_without_size", "c-at_max"),
		q("c_invalid", "c-crop"),
		// Crop modes.
		q("cm_pad_resize", "w-300,h-200,cm-pad_resize"),
		q("cm_pad_resize_bg_focus", "w-300,h-200,cm-pad_resize,bg-F3F3F3,fo-left"),
		q("cm_pad_resize_focus_auto", "w-300,h-200,cm-pad_resize,fo-auto"),
		q("cm_pad_resize_no_enlarge", "w-300,h-200,cm-pad_resize_no_enlarge"),
		q("cm_extract", "w-300,h-200,cm-extract"),
		q("cm_extract_focus", "w-300,h-200,cm-extract,fo-bottom_right"),
		q("cm_extract_xy", "w-300,h-200,cm-extract,x-10,y-20"),
		q("cm_extract_xcyc", "w-300,h-200,cm-extract,xc-400,yc-300"),
		q("cm_extract_xcyc_near_edge", "w-300,h-200,cm-extract,xc-10,yc-10"),
		q("cm_extract_xcyc_relative", "w-0.5,h-200,cm-extract,xc-400,yc-300"),
		q("cm_extract_ar", "w-300,ar-4-3,cm-extract"),
		q("cm_pad_extract", "w-800,h-800,cm-pad_extract,bg-red"),
		q("cm_invalid", "cm-stretch"),
		q("xy_without_extract", "w-300,x-10"),
		q("x_invalid", "x-N10"),
		q("x_expression", "cm-extract,w-10,h-10,x-iw_mul_0.4"),
		// Focus.
		q("fo_compass", "w-300,h-300,fo-top"),
		q("fo_auto", "w-300,h-300,fo-auto"),
		q("fo_auto_custom_override", "w-300,h-300,fo-auto-custom_override"),
		q("fo_face", "w-300,h-300,fo-face"),
		q("fo_faces_zoom", "w-300,h-300,fo-faces,z-0.8"),
		q("fo_object", "w-300,h-300,fo-car"),
		q("fo_object_list", "w-300,h-300,fo-truck_car_train"),
		q("fo_extract_face_zoom", "w-300,h-300,cm-extract,fo-face,z-2"),
		q("fo_custom", "w-300,h-300,fo-custom"),
		q("fo_invalid", "fo-Top!"),
		q("z_without_detection", "w-300,z-2"),
		q("z_invalid", "z-0"),
		// Output.
		q("q", "q-80"),
		q("q_invalid", "q-0"),
		q("q_too_high", "q-101"),
		q("f_webp", "f-webp"),
		q("f_jpeg", "f-jpeg"),
		q("f_auto", "f-auto"),
		q("f_orig", "f-orig"),
		q("f_mp4", "f-mp4"),
		q("f_svg", "f-svg"),
		q("f_invalid", "f-bmp"),
		q("pr_lo", "pr-true,lo-true"),
		q("pr_false", "pr-false"),
		q("md_cp_true", "md-true,cp-true"),
		q("md_cp_false", "md-false,cp-false"),
		q("md_invalid", "md-yes"),
		q("orig_true", "orig-true,w-300"),
		q("orig_false", "orig-false"),
		q("dpr", "w-300,dpr-2"),
		q("dpr_auto", "dpr-auto"),
		q("dpr_too_high", "dpr-6"),
		// Effects.
		q("bl", "bl-10"),
		q("blur_alias", "blur-5"),
		q("bl_invalid", "bl-0"),
		q("bl_too_high", "bl-101"),
		q("e_sharpen", "e-sharpen"),
		q("e_sharpen_value", "e-sharpen-10"),
		q("e_sharpen_invalid", "e-sharpen-x"),
		q("e_usm", "e-usm-2-2-0.8-0.024"),
		q("e_usm_short", "e-usm-2-2-0.8"),
		q("e_usm_long", "e-usm-2-2-0.8-0.024-1"),
		q("e_usm_bare", "e-usm"),
		q("e_grayscale", "e-grayscale"),
		q("e_contrast", "e-contrast"),
		q("e_unsupported", "e-bgremove,e-upscale,e-gradient-ld-45"),
		q("r", "r-20"),
		q("r_max", "r-max"),
		q("r_corners", "r-20_40_80_160"),
		q("r_three", "r-1_2_3"),
		q("r_invalid", "r-x"),
		q("b", "b-5_FFF000"),
		q("b_dash", "b-5-A94D34"),
		q("b_named", "b-2_red"),
		q("b_invalid", "b-5"),
		q("bg_hex", "bg-272B38"),
		q("bg_rgba", "bg-AAFF0040"),
		q("bg_short", "bg-fff"),
		q("bg_named", "bg-LightGreen"),
		q("bg_blurred", "w-300,h-300,cm-pad_resize,bg-blurred_25_N30"),
		q("bg_dominant", "bg-dominant"),
		q("bg_invalid", "bg-notacolor"),
		q("rt_90", "rt-90"),
		q("rt_negative", "rt-N90"),
		q("rt_450", "rt-450"),
		q("rt_any", "rt-45"),
		q("rt_any_negative", "rt-N40"),
		q("rt_any_wrap", "rt-270.5"),
		q("rt_auto", "rt-auto"),
		q("rt_invalid", "rt-left"),
		q("fl_h", "fl-h"),
		q("fl_v", "fl-v"),
		q("fl_hv", "fl-h_v"),
		q("fl_invalid", "fl-x"),
		q("t_true", "t-true"),
		q("t_value", "t-15"),
		q("t_false", "t-false"),
		q("t_invalid", "t-100"),
		q("o", "o-50"),
		q("o_invalid", "o-150"),
		// Delivery and presets.
		q("n_unconfigured", "n-thumb"),
		{name: "n_preset", cfg: "presets", path: img, query: "tr=n-thumb,q-90"},
		{name: "n_preset_chained", cfg: "presets", path: img, query: "tr=n-chained:rt-90"},
		{name: "n_undefined", cfg: "presets", path: img, query: "tr=n-nope"},
		q("n_empty", "n-"),
		q("di", "di-folder@@default.jpg"),
		q("di_empty", "di-"),
		q("pg", "pg-2"),
		q("pg_range", "pg-2_4"),
		q("pg_invalid", "pg-0"),
		q("pg_bad_range", "pg-4_2"),
		q("video_params", "vc-h264,so-5,sr-1440_1080"),
	})
}

// TestLayers covers overlays.
func TestLayers(t *testing.T) {
	t.Parallel()
	runGolden(t, "layers", []testCase{
		q("image", "l-image,i-logo.png,l-end"),
		q("image_nested_path", "l-image,i-brand@@logos@@logo.png,lfo-bottom_right,l-end"),
		q("image_slash_path", "l-image,i-/path/to/overlay.jpg,w-100,b-10_CDDC39,l-end"),
		q("image_base64", "l-image,ie-YnJhbmQvbG9nbyAoMSkucG5n,l-end"),
		q("image_base64_invalid", "l-image,ie-!!!,l-end"),
		q("image_offsets", "w-800,l-image,i-logo.png,lx-10,ly-20,w-100,h-50,o-50,l-end"),
		q("image_negative_offsets", "l-image,i-logo.png,lx-N10,ly-N20,l-end"),
		q("image_x_only", "l-image,i-logo.png,lx-10,l-end"),
		q("image_offset_expression", "l-image,i-logo.png,lx-bw_mul_0.5,l-end"),
		q("image_offset_invalid", "l-image,i-logo.png,lx-1.5,l-end"),
		q("image_lfo_invalid", "l-image,i-logo.png,lfo-middle,l-end"),
		q("image_size_invalid", "l-image,i-logo.png,w-x,l-end"),
		q("image_size_relative", "l-image,i-logo.png,w-0.5,l-end"),
		q("image_missing_input", "l-image,w-100,l-end"),
		q("image_no_l_end", "w-300,l-image,i-logo.png"),
		q("image_nested_layer", "l-image,i-logo.png,l-text,i-hi,l-end,l-end"),
		q("image_in_chain", "w-300:l-image,i-logo.png,l-end"),
		q("image_font_param", "l-image,i-logo.png,fs-10,l-end"),
		q("canvas", "l-image,i-ik_canvas,bg-FF0000,l-end"),
		q("text", "l-text,i-Minimal%20Text,fs-20,co-FF0000,ff-Roboto,l-end"),
		q("text_markup_escaped", "l-text,i-%3Cb%3Ex%3C%2Fb%3E%20%26,l-end"),
		q("text_base64", "l-text,ie-SGVsbG8sIHdvcmxkOiDDvG7Dr2NvZGU%3D,l-end"),
		q("text_font_file", "l-text,i-hi,ff-fonts@@Poppins.ttf,l-end"),
		q("text_fs_only", "l-text,i-hi,fs-12,l-end"),
		q("text_color_invalid", "l-text,i-hi,co-nope,l-end"),
		q("text_fs_invalid", "l-text,i-hi,fs-0,l-end"),
		q("text_missing", "l-text,fs-20,l-end"),
		q("video", "l-video,i-clip.mp4,l-end"),
		q("l_end_alone", "w-300,l-end"),
		q("too_many", "l-image,i-a,l-end,l-image,i-a,l-end,l-image,i-a,l-end,l-image,i-a,l-end,l-image,i-a,l-end,l-image,i-a,l-end,l-image,i-a,l-end,l-image,i-a,l-end,l-image,i-a,l-end,l-image,i-a,l-end,l-image,i-a,l-end,l-image,i-a,l-end,l-image,i-a,l-end,l-image,i-a,l-end,l-image,i-a,l-end,l-image,i-a,l-end,l-image,i-a,l-end"),
		{name: "strict_layer_extra", cfg: "strict", path: img, query: "tr=l-image,i-logo.png,lm-multiply,l-end"},
	})
}

// TestNew covers constructor validation.
func TestNew(t *testing.T) {
	t.Parallel()
	for _, ep := range []string{"/a?b", "/a//b", "/a#b"} {
		if _, err := imagekit.New(&imagekit.Options{EndpointPath: ep}); err == nil {
			t.Errorf("EndpointPath %q accepted", ep)
		}
	}
	p, err := imagekit.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != imagekit.Name || p.Verifier() != nil {
		t.Errorf("Name %q, Verifier %v", p.Name(), p.Verifier())
	}
	res, err := p.ParseURL("/tr:w-10/a.jpg", "")
	if err != nil || res.Request.Pipelines[0].Resize.Width.Px != 10 {
		t.Fatalf("ParseURL = %+v, %v", res, err)
	}
}

// TestErrorKinds checks the status classes the server maps.
func TestErrorKinds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		tc   testCase
		kind errs.Kind
		is   error
	}{
		{testCase{path: "/tr:w-1/tr:w-2/a.jpg"}, errs.KindInvalidURL, dialect.ErrMalformedURL},
		{testCase{path: img, query: "tr=w-x"}, errs.KindInvalidURL, nil},
		{testCase{cfg: "strict", path: img, query: "tr=zz-1"}, errs.KindInvalidURL, dialect.ErrUnknownOption},
		{testCase{cfg: "required", path: img}, errs.KindUnauthorized, nil},
		{testCase{path: img, query: "ik-t=1000"}, errs.KindExpired, dialect.ErrExpired},
	}
	for _, tt := range tests {
		_, err := parseCase(t, tt.tc)
		if errs.KindOf(err) != tt.kind {
			t.Errorf("%+v: kind %v, want %v (%v)", tt.tc, errs.KindOf(err), tt.kind, err)
		}
		if tt.is != nil && !errors.Is(err, tt.is) {
			t.Errorf("%+v: %v is not %v", tt.tc, err, tt.is)
		}
	}
}
