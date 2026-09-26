package cloudflare_test

import (
	"testing"
)

const img = "/uploads/a.jpg"

func opt(o string) string { return "/" + o + img }

// TestImageOptions covers every /cdn-cgi/image/ option
// (developers.cloudflare.com/images/optimization/features/) with valid
// values, aliases and invalid values.
func TestImageOptions(t *testing.T) {
	t.Parallel()
	runGolden(t, "image", []testCase{
		// Size.
		{name: "width", path: opt("width=400")},
		{name: "w", path: opt("w=400")},
		{name: "width_auto", path: opt("width=auto")},
		{name: "width_auto_then_px", path: opt("width=auto,w=300")},
		{name: "width_zero", path: opt("width=0")},
		{name: "width_negative", path: opt("width=-5")},
		{name: "width_huge", path: opt("width=99999999")},
		{name: "width_float", path: opt("width=1.5")},
		{name: "height", path: opt("height=300")},
		{name: "h", path: opt("h=300")},
		{name: "height_auto", path: opt("height=auto")},
		{name: "wbreakpoints", path: opt("width=auto,wbreakpoints=320;768")},
		{name: "wmobile_wdesktop", path: opt("width=auto,wmobile=768,wdesktop=1200")},
		{name: "later_wins", path: opt("w=100,w=200")},
		// Fit.
		{name: "fit_scale_down", path: opt("w=400,h=300,fit=scale-down")},
		{name: "fit_contain", path: opt("w=400,h=300,fit=contain")},
		{name: "fit_cover", path: opt("w=400,h=300,fit=cover")},
		{name: "fit_crop", path: opt("w=400,h=300,fit=crop")},
		{name: "fit_pad", path: opt("w=400,h=300,fit=pad,background=white")},
		{name: "fit_squeeze", path: opt("w=400,h=300,fit=squeeze")},
		{name: "fit_scale_up", path: opt("w=400,fit=scale-up")},
		{name: "fit_aspect_crop", path: opt("w=400,h=300,fit=aspect-crop")},
		{name: "fit_pad_then_cover", path: opt("fit=pad,fit=cover")},
		{name: "fit_invalid", path: opt("fit=fill")},
		// Gravity.
		{name: "gravity_auto", path: opt("fit=cover,gravity=auto")},
		{name: "g_auto", path: opt("fit=cover,g=auto")},
		{name: "gravity_face", path: opt("gravity=face")},
		{name: "gravity_face_zoom", path: opt("gravity=face,zoom=0.5")},
		{name: "face_zoom_alias", path: opt("face-zoom=0.1,g=face")},
		{name: "zoom_invalid", path: opt("zoom=2")},
		{name: "gravity_left", path: opt("gravity=left")},
		{name: "gravity_right", path: opt("gravity=right")},
		{name: "gravity_top", path: opt("gravity=top")},
		{name: "gravity_bottom", path: opt("gravity=bottom")},
		{name: "gravity_point", path: opt("gravity=0.5x0.2")},
		{name: "gravity_point_edges", path: opt("g=0x1")},
		{name: "gravity_point_out_of_range", path: opt("g=1.5x0.5")},
		{name: "gravity_point_malformed", path: opt("g=0.5")},
		{name: "gravity_invalid", path: opt("gravity=center")},
		// Quality.
		{name: "quality", path: opt("quality=75")},
		{name: "q", path: opt("q=75")},
		{name: "quality_high", path: opt("quality=high")},
		{name: "quality_medium_high", path: opt("quality=medium-high")},
		{name: "quality_medium_low", path: opt("quality=medium-low")},
		{name: "quality_low", path: opt("q=low")},
		{name: "quality_zero", path: opt("q=0")},
		{name: "quality_101", path: opt("q=101")},
		{name: "scq", path: opt("scq=50")},
		{name: "slow_connection_quality", path: opt("slow-connection-quality=low")},
		{name: "scq_invalid", path: opt("scq=fast")},
		// Format.
		{name: "format_auto", path: opt("format=auto")},
		{name: "f_auto", path: opt("f=auto")},
		{name: "format_avif", path: opt("format=avif")},
		{name: "format_webp", path: opt("f=webp")},
		{name: "format_jpeg", path: opt("f=jpeg")},
		{name: "format_jpg", path: opt("f=jpg")},
		{name: "format_baseline_jpeg", path: opt("format=baseline-jpeg")},
		{name: "format_png", path: opt("format=png")},
		{name: "format_gif", path: opt("format=gif")},
		{name: "format_json", path: opt("format=json")},
		{name: "format_invalid", path: opt("format=tiff")},
		// DPR.
		{name: "dpr", path: opt("w=100,dpr=2")},
		{name: "dpr_fraction", path: opt("dpr=1.5")},
		{name: "dpr_zero", path: opt("dpr=0")},
		{name: "dpr_nan", path: opt("dpr=NaN")},
		// Animation.
		{name: "anim_false", path: opt("anim=false")},
		{name: "anim_true", path: opt("anim=true")},
		{name: "anim_invalid", path: opt("anim=no")},
		// Background.
		{name: "background_hex", path: opt("background=%23ff0000")},
		{name: "background_hex_short", path: opt("background=%23f00")},
		{name: "background_hex_alpha", path: opt("background=%23ff000080")},
		{name: "background_name", path: opt("background=red")},
		{name: "background_name_case", path: opt("background=CornflowerBlue")},
		{name: "background_grey", path: opt("background=lightgrey")},
		{name: "background_transparent", path: opt("background=transparent")},
		{name: "background_rgb_encoded", path: opt("background=rgb%28240%2C40%2C145%29")},
		{name: "background_rgb_raw_commas", path: opt("background=rgb(240,40,145),w=10")},
		{name: "background_rgba", path: opt("background=rgba(250,40,145,0.5)")},
		{name: "background_rgb_space", path: opt("background=rgb(10%2020%2030%20%2F%2050%25)")},
		{name: "background_rgb_percent", path: opt("background=rgb(100%25,0%25,50%25)")},
		{name: "background_invalid_name", path: opt("background=notacolor")},
		{name: "background_invalid_hex", path: opt("background=%23ggg")},
		{name: "background_invalid_rgb", path: opt("background=rgb(300,0,0)")},
		// Blur and sharpen.
		{name: "blur", path: opt("blur=50")},
		{name: "blur_max", path: opt("blur=250")},
		{name: "blur_over", path: opt("blur=251")},
		{name: "sharpen", path: opt("sharpen=2")},
		{name: "sharpen_fraction", path: opt("sharpen=0.5")},
		{name: "sharpen_over", path: opt("sharpen=11")},
		// Colour adjustments.
		{name: "brightness", path: opt("brightness=0.5")},
		{name: "brightness_one", path: opt("brightness=1")},
		{name: "contrast", path: opt("contrast=1.5")},
		{name: "gamma", path: opt("gamma=0.5")},
		{name: "gamma_zero", path: opt("gamma=0")},
		{name: "saturation", path: opt("saturation=0")},
		{name: "saturation_negative", path: opt("saturation=-1")},
		{name: "contrast_invalid", path: opt("contrast=abc")},
		// Rotate and flip.
		{name: "rotate_90", path: opt("rotate=90")},
		{name: "rotate_180", path: opt("rotate=180")},
		{name: "rotate_270", path: opt("rotate=270")},
		{name: "rotate_0", path: opt("rotate=0")},
		{name: "rotate_45", path: opt("rotate=45")},
		{name: "flip_h", path: opt("flip=h")},
		{name: "flip_v", path: opt("flip=v")},
		{name: "flip_hv", path: opt("flip=hv")},
		{name: "flip_invalid", path: opt("flip=x")},
		// Trim.
		{name: "trim_border", path: opt("trim=border")},
		{name: "trim_border_color", path: opt("trim=border,trim.border.color=%23ffffff")},
		{name: "trim_border_tolerance", path: opt("trim.border.tolerance=200")},
		{name: "trim_border_keep", path: opt("trim=border,trim.border.keep=5")},
		{name: "trim_border_false", path: opt("trim=border,trim.border=false")},
		{name: "trim_border_tolerance_invalid", path: opt("trim.border.tolerance=300")},
		{name: "trim_sides_px", path: opt("trim=10;20;30;40")},
		{name: "trim_sides_fraction", path: opt("trim=0.1;0.2;0.1;0.2")},
		{name: "trim_sides_mixed", path: opt("trim=0.1;20;0;0.2")},
		{name: "trim_sides_too_much", path: opt("trim=0.5;0.5;0.5;0.5")},
		{name: "trim_sides_count", path: opt("trim=1;2;3")},
		{name: "trim_top", path: opt("trim.top=0.25")},
		{name: "trim_left_px", path: opt("trim.left=800")},
		{name: "trim_height", path: opt("trim.height=800")},
		{name: "trim_width_fraction", path: opt("trim.width=0.5,trim.left=0.25")},
		{name: "trim_right_fraction", path: opt("trim.right=0.1")},
		{name: "trim_bottom_px", path: opt("trim.bottom=10")},
		{name: "trim_zero", path: opt("trim=0;0;0;0")},
		{name: "trim_width_zero", path: opt("trim.width=0")},
		{name: "trim_side_invalid", path: opt("trim.top=1.5")},
		{name: "trim_invalid", path: opt("trim=edges")},
		// Border.
		{name: "border", path: opt("border=red;5")},
		{name: "border_sides", path: opt("border=%23000;1;2;3;4")},
		{name: "border_dotted", path: opt("border.color=blue,border.width=2,border.left=8")},
		{name: "border_invalid", path: opt("border=red")},
		{name: "border_width_invalid", path: opt("border.top=-1")},
		// Metadata.
		{name: "metadata_keep", path: opt("metadata=keep")},
		{name: "metadata_copyright", path: opt("metadata=copyright")},
		{name: "metadata_none", path: opt("metadata=none")},
		{name: "metadata_invalid", path: opt("metadata=all")},
		// Delivery.
		{name: "onerror_redirect", path: opt("onerror=redirect")},
		{name: "onerror_invalid", path: opt("onerror=fallback")},
		{name: "compression_fast", path: opt("compression=fast")},
		{name: "compression_invalid", path: opt("compression=slow")},
		// Unsupported and unknown.
		{name: "segment", path: opt("segment=foreground")},
		{name: "segment_strict", cfg: "strict", path: opt("segment=foreground")},
		{name: "segment_invalid", path: opt("segment=background")},
		{name: "upscale_interpolate", path: opt("upscale=interpolate")},
		{name: "upscale_generate", path: opt("upscale=generate")},
		{name: "upscale_generate_strict", cfg: "strict", path: opt("upscale=generate")},
		{name: "upscale_invalid", path: opt("upscale=ai")},
		{name: "unknown", path: opt("w=10,origin-auth=share-publicly")},
		{name: "unknown_strict", cfg: "strict", path: opt("w=10,origin-auth=share-publicly")},
		{name: "known_strict", cfg: "strict", path: opt("w=10,fit=cover,f=auto")},
		// Option list syntax.
		{name: "case_insensitive_keys", path: opt("Width=10,FIT=cover")},
		{name: "empty_items", path: opt("w=10,,h=5,")},
		{name: "item_without_value", path: opt("w=10,cover")},
		{name: "item_bad_escape", path: opt("w=%zz")},
		{name: "all_options", path: opt("width=400,height=300,fit=cover,gravity=0.5x0.2,quality=80,format=webp,dpr=2," +
			"anim=false,background=%23ffffff,blur=10,sharpen=1,brightness=1.1,contrast=1.2,gamma=0.9,saturation=1.3," +
			"rotate=90,flip=h,trim=border,metadata=none,onerror=redirect,compression=fast,scq=40")},
	})
}

// TestImageSource covers the source after the options.
func TestImageSource(t *testing.T) {
	t.Parallel()
	signed := func(path, query string) string {
		q, err := getFixture(t).xi.SignXIQuery(path, query)
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	runGolden(t, "source", []testCase{
		{name: "relative", path: "/w=10/uploads/a.jpg"},
		{name: "relative_escaped", path: "/w=10/uploads/a%20b.jpg"},
		{name: "relative_colon", path: "/w=10/s3:/bucket/key.jpg"},
		{name: "relative_query", path: "/w=10/a.jpg", query: "v=2"},
		{name: "absolute_https", path: "/w=10/https://other.example.com/a.jpg"},
		{name: "absolute_http", path: "/w=10/http://other.example.com/a.jpg"},
		{name: "absolute_collapsed", path: "/w=10/https:/other.example.com/a.jpg"},
		{name: "absolute_upper_scheme", path: "/w=10/HTTPS://other.example.com/a.jpg"},
		{name: "absolute_encoded", path: "/w=10/https%3A%2F%2Fexample.com%2Fa.jpg%3Fx%3D1"},
		{name: "absolute_encoded_plus_query", path: "/w=10/https%3A%2F%2Fexample.com%2Fa.jpg%3Fx%3D1", query: "y=2"},
		{name: "absolute_query", path: "/w=10/https://example.com/a.jpg", query: "x=1&y=2"},
		{name: "absolute_bad_escape", path: "/w=10/https%3A%2F%2Fexample.com%zz"},
		{name: "native_prefix", path: "/cdn-cgi/image/w=10/uploads/a.jpg"},
		{name: "missing_source", path: "/w=10"},
		{name: "empty_source", path: "/w=10/"},
		{name: "missing_options", path: "//uploads/a.jpg"},
		{name: "empty_path", path: ""},
		{name: "control_char", path: "/w=10/a%00.jpg"},
		// xi_sig.
		{name: "xi_signed", cfg: "xi", path: "/w=10/a.jpg", query: signed("/w=10/a.jpg", "")},
		{name: "xi_signed_query", cfg: "xi", path: "/w=10/https://e.com/a.jpg", query: signed("/w=10/https://e.com/a.jpg", "v=1")},
		{name: "xi_bad", cfg: "xi", path: "/w=10/a.jpg", query: "xi_sig=AAAA"},
		{name: "xi_tampered", cfg: "xi", path: "/w=20/a.jpg", query: signed("/w=10/a.jpg", "")},
		{name: "xi_unsigned", cfg: "xi", path: "/w=10/a.jpg"},
		{name: "xi_ignored_without_verifier", path: "/w=10/a.jpg", query: "xi_sig=AAAA&v=1"},
	})
}
