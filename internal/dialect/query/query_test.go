package query_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/query"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

const src = "url=https://example.com/a.jpg"

// TestNativeKeys covers every native key (docs/URL-DIALECTS.md §4) with a
// valid and, where the key has an argument grammar, an invalid value.
func TestNativeKeys(t *testing.T) {
	t.Parallel()
	runGolden(t, "native", []testCase{
		// Source.
		{name: "url_only", path: "/", query: src},
		{name: "url_empty_path", path: "", query: src},
		{name: "url_encoded", path: "/", query: "url=https%3A%2F%2Fexample.com%2Fa%20b.jpg%3Fv%3D1"},
		{name: "url_s3", path: "/", query: "url=s3://bucket/key.png&w=10"},
		{name: "url_relative", path: "/", query: "url=photos/a.jpg"},
		{name: "url_missing", path: "/", query: "w=10"},
		{name: "url_empty", path: "/", query: "url=&w=10"},
		{name: "url_duplicate", path: "/", query: src + "&url=https://example.com/b.jpg"},
		{name: "url_control_char", path: "/", query: "url=https://e.com/%00"},
		{name: "query_bad_escape", path: "/", query: src + "&w=%zz"},
		{name: "file_rejected", path: "/", query: "file=/etc/passwd"},
		// Resize.
		{name: "w", path: "/", query: src + "&w=300"},
		{name: "width", path: "/", query: src + "&width=300"},
		{name: "w_invalid", path: "/", query: src + "&w=-1"},
		{name: "w_empty", path: "/", query: src + "&w="},
		{name: "h", path: "/", query: src + "&h=200"},
		{name: "height", path: "/", query: src + "&height=200"},
		{name: "h_invalid", path: "/", query: src + "&h=abc"},
		{name: "w_later_wins", path: "/", query: src + "&w=100&w=200"},
		{name: "fit_fit", path: "/", query: src + "&fit=fit"},
		{name: "fit_contain", path: "/", query: src + "&fit=contain"},
		{name: "fit_inside", path: "/", query: src + "&fit=inside"},
		{name: "fit_fill", path: "/", query: src + "&fit=fill"},
		{name: "fit_cover", path: "/", query: src + "&fit=cover"},
		{name: "fit_fill_down", path: "/", query: src + "&fit=fill-down"},
		{name: "fit_force", path: "/", query: src + "&fit=force"},
		{name: "fit_stretch", path: "/", query: src + "&fit=stretch"},
		{name: "fit_outside", path: "/", query: src + "&fit=outside"},
		{name: "fit_auto", path: "/", query: src + "&fit=auto"},
		{name: "fit_invalid", path: "/", query: src + "&fit=crop"},
		{name: "dpr", path: "/", query: src + "&w=100&dpr=2"},
		{name: "dpr_invalid", path: "/", query: src + "&dpr=0"},
		{name: "enlarge", path: "/", query: src + "&enlarge=1"},
		{name: "enlarge_invalid", path: "/", query: src + "&enlarge=yes"},
		// Gravity and crop.
		{name: "g_north", path: "/", query: src + "&g=north"},
		{name: "gravity_centre", path: "/", query: src + "&gravity=centre"},
		{name: "g_southeast_offsets", path: "/", query: src + "&g=southeast:10:20"},
		{name: "g_imgproxy_code", path: "/", query: src + "&g=nowe"},
		{name: "g_smart", path: "/", query: src + "&g=smart"},
		{name: "g_attention", path: "/", query: src + "&g=attention"},
		{name: "g_entropy", path: "/", query: src + "&g=entropy"},
		{name: "g_entropy_args", path: "/", query: src + "&g=entropy:1"},
		{name: "g_fp", path: "/", query: src + "&g=fp:0.25:0.75"},
		{name: "g_fp_commas", path: "/", query: src + "&g=fp,0.25,0.75"},
		{name: "g_fp_out_of_range", path: "/", query: src + "&g=fp:2:0"},
		{name: "g_invalid", path: "/", query: src + "&g=middle"},
		{name: "crop_wh", path: "/", query: src + "&crop=100,50"},
		{name: "crop_w", path: "/", query: src + "&crop=0.5"},
		{name: "crop_whxy", path: "/", query: src + "&crop=100,50,10,20"},
		{name: "crop_with_gravity", path: "/", query: src + "&g=sm&crop=100,50"},
		{name: "crop_three_args", path: "/", query: src + "&crop=1,2,3"},
		{name: "crop_invalid", path: "/", query: src + "&crop=a,b"},
		// Output.
		{name: "f", path: "/", query: src + "&f=webp"},
		{name: "format", path: "/", query: src + "&format=avif"},
		{name: "f_jpg", path: "/", query: src + "&f=jpg"},
		{name: "f_invalid", path: "/", query: src + "&f=pdf"},
		{name: "q", path: "/", query: src + "&q=80"},
		{name: "quality", path: "/", query: src + "&quality=55"},
		{name: "q_invalid", path: "/", query: src + "&q=101"},
		{name: "lossless", path: "/", query: src + "&lossless=true"},
		{name: "lossless_false", path: "/", query: src + "&lossless=0"},
		{name: "lossless_invalid", path: "/", query: src + "&lossless=maybe"},
		{name: "progressive", path: "/", query: src + "&progressive=1"},
		{name: "strip", path: "/", query: src + "&strip=1"},
		{name: "strip_false", path: "/", query: src + "&strip=false"},
		// Effects and geometry.
		{name: "blur", path: "/", query: src + "&blur=2.5"},
		{name: "blur_invalid", path: "/", query: src + "&blur=-1"},
		{name: "sharpen", path: "/", query: src + "&sharpen=0.7"},
		{name: "pixelate", path: "/", query: src + "&pixelate=8"},
		{name: "pixelate_invalid", path: "/", query: src + "&pixelate=1.5"},
		{name: "rotate", path: "/", query: src + "&rotate=90"},
		{name: "rot", path: "/", query: src + "&rot=270"},
		{name: "rotate_invalid", path: "/", query: src + "&rotate=45"},
		{name: "flip_h", path: "/", query: src + "&flip=h"},
		{name: "flip_v", path: "/", query: src + "&flip=v"},
		{name: "flip_hv", path: "/", query: src + "&flip=hv"},
		{name: "flip_invalid", path: "/", query: src + "&flip=x"},
		{name: "bg_hex", path: "/", query: src + "&bg=ff8800"},
		{name: "bg_short_hex", path: "/", query: src + "&bg=f80"},
		{name: "bg_rgb", path: "/", query: src + "&bg=255,136,0"},
		{name: "bg_invalid", path: "/", query: src + "&bg=zzzzzz"},
		{name: "trim", path: "/", query: src + "&trim=10"},
		{name: "trim_color", path: "/", query: src + "&trim=10,ffffff,1,0"},
		{name: "trim_invalid", path: "/", query: src + "&trim=x"},
		{name: "pad_one", path: "/", query: src + "&pad=10"},
		{name: "pad_four", path: "/", query: src + "&pad=1,2,3,4"},
		{name: "pad_invalid", path: "/", query: src + "&pad=-1"},
		{name: "page", path: "/", query: src + "&page=2"},
		{name: "page_invalid", path: "/", query: src + "&page=-2"},
		// Delivery.
		{name: "preset", path: "/", query: src + "&preset=thumb,sharp"},
		{name: "preset_empty", path: "/", query: src + "&preset=a,,b"},
		{name: "cb", path: "/", query: src + "&cb=v2"},
		{name: "filename", path: "/", query: src + "&filename=my:photo"},
		{name: "attachment", path: "/", query: src + "&attachment=1"},
		{name: "expires_future", path: "/", query: src + "&expires=4102444800"},
		{name: "expires_past", path: "/", query: src + "&expires=1000"},
		{name: "expires_invalid", path: "/", query: src + "&expires=soon"},
		// o= passthrough.
		{name: "o_resize", path: "/", query: src + "&o=rs:fill:300:200/g:sm"},
		{name: "o_encoded", path: "/", query: src + "&o=rs%3Afill%3A300%3A200%2Fg%3Asm"},
		{name: "o_chained", path: "/", query: src + "&o=rs:fit:500:500/-/bl:2&q=70&w=100"},
		{name: "o_then_native_override", path: "/", query: src + "&o=w:100/q:50&w=200"},
		{name: "native_then_o_override", path: "/", query: src + "&w=200&o=w:100"},
		{name: "o_source_rejected", path: "/", query: src + "&o=w:1/plain/https://evil.example/x.jpg"},
		{name: "o_empty_segment", path: "/", query: src + "&o=w:1//h:2"},
		{name: "o_invalid_arg", path: "/", query: src + "&o=w:abc"},
		{name: "o_unknown_option", path: "/", query: src + "&o=nope:1"},
		{name: "o_unknown_option_strict", cfg: "strict", path: "/", query: src + "&o=nope:1"},
		{name: "o_security_forbidden", path: "/", query: src + "&o=mrd:100"},
		{name: "o_security_allowed", cfg: "security", path: "/", query: src + "&o=mrd:100"},
		{name: "o_separator", cfg: "sep", path: "/", query: src + "&o=rs,fill,10,20"},
		{name: "o_later_milestone", path: "/", query: src + "&o=sa:1.2"},
		{name: "o_empty_pipeline", path: "/", query: src + "&o=-/w:1"},
		// Unknown keys.
		{name: "unknown_key", path: "/", query: src + "&utm_source=x&w=10"},
		{name: "unknown_key_strict", cfg: "strict", path: "/", query: src + "&utm_source=x"},
		// weserv basics.
		{name: "weserv_basics", path: "/", query: "url=example.com/a.jpg&w=300&h=300"},
	})
}

// TestImaginaryEndpoints covers every imaginary endpoint and param.
func TestImaginaryEndpoints(t *testing.T) {
	t.Parallel()
	runGolden(t, "imaginary", []testCase{
		{name: "unknown_endpoint", path: "/nope", query: src},
		{name: "nested_endpoint", path: "/resize/x", query: src},
		{name: "trailing_slash", path: "/resize/", query: src + "&width=300"},
		// resize
		{name: "resize_width", path: "/resize", query: src + "&width=300"},
		{name: "resize_both_embed", path: "/resize", query: src + "&width=300&height=200"},
		{name: "resize_nocrop_false", path: "/resize", query: src + "&width=300&height=200&nocrop=false"},
		{name: "resize_force", path: "/resize", query: src + "&width=300&height=200&force=true"},
		{name: "resize_extend_white", path: "/resize", query: src + "&width=300&height=200&extend=white"},
		{name: "resize_extend_black", path: "/resize", query: src + "&width=300&height=200&extend=black"},
		{name: "resize_extend_background", path: "/resize", query: src + "&width=300&height=200&extend=background&background=250,20,10"},
		{name: "resize_extend_mirror", path: "/resize", query: src + "&width=300&height=200&extend=mirror"},
		{name: "resize_extend_invalid", path: "/resize", query: src + "&width=300&height=200&extend=wrap"},
		{name: "resize_missing_dims", path: "/resize", query: src},
		{name: "resize_float_width", path: "/resize", query: src + "&width=299.5"},
		{name: "resize_negative", path: "/resize", query: src + "&width=-300"},
		{name: "resize_aspectratio", path: "/resize", query: src + "&width=300&aspectratio=16:9"},
		{name: "resize_aspectratio_invalid", path: "/resize", query: src + "&width=300&aspectratio=wide"},
		{name: "resize_aspectratio_both_dims", path: "/resize", query: src + "&width=300&height=100&aspectratio=16:9"},
		// fit / enlarge
		{name: "fit", path: "/fit", query: src + "&width=300&height=200"},
		{name: "fit_missing_height", path: "/fit", query: src + "&width=300"},
		{name: "enlarge", path: "/enlarge", query: src + "&width=3000&height=2000"},
		{name: "enlarge_nocrop", path: "/enlarge", query: src + "&width=3000&height=2000&nocrop=true"},
		{name: "enlarge_missing", path: "/enlarge", query: src + "&height=2000"},
		// extract
		{name: "extract", path: "/extract", query: src + "&top=10&left=20&areawidth=300&areaheight=200"},
		{name: "extract_resize", path: "/extract", query: src + "&top=10&left=20&areawidth=300&areaheight=200&width=100"},
		{name: "extract_missing_area", path: "/extract", query: src + "&top=10&areawidth=300"},
		// crop / smartcrop
		{name: "crop_both", path: "/crop", query: src + "&width=300&height=200"},
		{name: "crop_gravity", path: "/crop", query: src + "&width=300&height=200&gravity=north"},
		{name: "crop_width_only", path: "/crop", query: src + "&width=300&gravity=east"},
		{name: "crop_gravity_invalid", path: "/crop", query: src + "&width=300&gravity=up"},
		{name: "crop_missing", path: "/crop", query: src + "&gravity=north"},
		{name: "smartcrop", path: "/smartcrop", query: src + "&width=300&height=300"},
		{name: "smartcrop_height_only", path: "/smartcrop", query: src + "&height=300"},
		// rotate / autorotate / flip / flop
		{name: "rotate", path: "/rotate", query: src + "&rotate=90"},
		{name: "rotate_rounded", path: "/rotate", query: src + "&rotate=200"},
		{name: "rotate_full_turn", path: "/rotate", query: src + "&rotate=450"},
		{name: "rotate_missing", path: "/rotate", query: src},
		{name: "rotate_with_width", path: "/rotate", query: src + "&rotate=180&width=100"},
		{name: "autorotate", path: "/autorotate", query: src + "&width=100&type=png"},
		{name: "flip", path: "/flip", query: src},
		{name: "flop", path: "/flop", query: src},
		{name: "flip_and_flop_param", path: "/flip", query: src + "&flop=true"},
		// thumbnail / zoom
		{name: "thumbnail", path: "/thumbnail", query: src + "&width=100"},
		{name: "thumbnail_both", path: "/thumbnail", query: src + "&width=100&height=100"},
		{name: "thumbnail_embed", path: "/thumbnail", query: src + "&width=100&height=100&embed=true"},
		{name: "thumbnail_missing", path: "/thumbnail", query: src},
		{name: "zoom", path: "/zoom", query: src + "&factor=2"},
		{name: "zoom_area", path: "/zoom", query: src + "&factor=1&top=100&left=50&areawidth=300&areaheight=200"},
		{name: "zoom_area_missing", path: "/zoom", query: src + "&factor=1&top=100"},
		{name: "zoom_missing", path: "/zoom", query: src},
		// convert
		{name: "convert", path: "/convert", query: src + "&type=webp&quality=70"},
		{name: "convert_auto", path: "/convert", query: src + "&type=auto"},
		{name: "convert_missing", path: "/convert", query: src},
		{name: "convert_invalid", path: "/convert", query: src + "&type=bmpx"},
		// watermark
		{name: "watermark", path: "/watermark", query: src + "&text=hello&font=sans%2012&opacity=0.5&textwidth=200&color=255,200,150&margin=10&dpi=150"},
		{name: "watermark_defaults", path: "/watermark", query: src + "&text=hello&noreplicate=true"},
		{name: "watermark_missing", path: "/watermark", query: src},
		{name: "watermarkimage", path: "/watermarkimage", query: src + "&image=https://example.com/logo.png&top=10&left=20&opacity=2"},
		{name: "watermarkimage_missing", path: "/watermarkimage", query: src},
		// blur
		{name: "blur", path: "/blur", query: src + "&sigma=5"},
		{name: "blur_minampl", path: "/blur", query: src + "&sigma=5&minampl=0.5"},
		{name: "blur_missing", path: "/blur", query: src},
		// info
		{name: "info", path: "/info", query: src},
		// common params
		{name: "common_output", path: "/resize", query: src + "&width=100&type=png&quality=90&compression=9&palette=true&interlace=true&stripmeta=true&noprofile=true&norotation=true"},
		{name: "common_colorspace_bw", path: "/resize", query: src + "&width=100&colorspace=bw"},
		{name: "common_colorspace_invalid", path: "/resize", query: src + "&width=100&colorspace=cmyk"},
		{name: "common_compression_invalid", path: "/resize", query: src + "&width=100&compression=10"},
		{name: "common_quality_invalid", path: "/resize", query: src + "&width=100&quality=150"},
		{name: "common_bool_invalid", path: "/resize", query: src + "&width=100&force=sure"},
		{name: "common_background_invalid", path: "/resize", query: src + "&width=100&background=1,2"},
		{name: "common_background_range", path: "/resize", query: src + "&width=100&background=1,2,300"},
		{name: "common_blur", path: "/resize", query: src + "&width=100&sigma=1.5"},
		{name: "common_speed_ignored", path: "/resize", query: src + "&width=100&speed=4"},
		{name: "common_field_ignored", path: "/resize", query: src + "&width=100&field=file"},
		{name: "unknown_param", path: "/resize", query: src + "&width=100&w=5"},
		{name: "unknown_param_strict", cfg: "strict", path: "/resize", query: src + "&width=100&w=5"},
		{name: "file_rejected", path: "/resize", query: "file=image.jpg&width=100"},
	})
}

// TestPipelineEndpoint covers /pipeline operations JSON.
func TestPipelineEndpoint(t *testing.T) {
	t.Parallel()
	ops := func(s string) string { return src + "&operations=" + url.QueryEscape(s) }
	eleven := "[" + strings.TrimSuffix(strings.Repeat(`{"operation":"flip"},`, 11), ",") + "]"
	runGolden(t, "pipeline", []testCase{
		{name: "readme_example", path: "/pipeline", query: ops(`[{"operation":"crop","params":{"width":300,"height":260}},{"operation":"convert","params":{"type":"webp"}}]`)},
		{name: "three_ops", path: "/pipeline", query: ops(`[{"operation":"resize","params":{"width":800}},{"operation":"blur","params":{"sigma":2.5}},{"operation":"watermarkImage","params":{"image":"https://example.com/w.png","opacity":0.5}}]`)},
		{name: "string_and_bool_params", path: "/pipeline", query: ops(`[{"operation":"enlarge","params":{"width":"600","height":"400","nocrop":true}}]`)},
		{name: "float_truncated", path: "/pipeline", query: ops(`[{"operation":"thumbnail","params":{"width":99.9}}]`)},
		{name: "ignore_failure", path: "/pipeline", query: ops(`[{"operation":"flop","ignore_failure":true}]`)},
		{name: "unknown_params_ignored", path: "/pipeline", query: ops(`[{"operation":"flip","params":{"url":"x","bogus":1}}]`)},
		{name: "output_last_wins", path: "/pipeline", query: ops(`[{"operation":"convert","params":{"type":"png"}},{"operation":"convert","params":{"type":"webp","quality":60}}]`)},
		{name: "missing", path: "/pipeline", query: src},
		{name: "empty_array", path: "/pipeline", query: ops(`[]`)},
		{name: "invalid_json", path: "/pipeline", query: ops(`[{"operation":`)},
		{name: "trailing_data", path: "/pipeline", query: ops(`[] []`)},
		{name: "unknown_field", path: "/pipeline", query: ops(`[{"operation":"flip","extra":1}]`)},
		{name: "unknown_operation", path: "/pipeline", query: ops(`[{"operation":"info"}]`)},
		{name: "http_only_name", path: "/pipeline", query: ops(`[{"operation":"watermarkimage","params":{"image":"https://e.com/w.png"}}]`)},
		{name: "too_many", path: "/pipeline", query: ops(eleven)},
		{name: "param_wrong_type", path: "/pipeline", query: ops(`[{"operation":"resize","params":{"width":true}}]`)},
		{name: "param_negative", path: "/pipeline", query: ops(`[{"operation":"resize","params":{"width":-1}}]`)},
		{name: "op_missing_required", path: "/pipeline", query: ops(`[{"operation":"rotate","params":{}}]`)},
		{name: "top_level_params_not_applied", path: "/pipeline", query: ops(`[{"operation":"flip"}]`) + "&width=100&type=png"},
	})
}

// TestOPassthroughEquivalence checks that ?url=X&o=<options> parses to
// the same request as the imgproxy path /unsafe/<options>/plain/X, and
// that native keys equal their imgproxy options.
func TestOPassthroughEquivalence(t *testing.T) {
	t.Parallel()
	f := getFixture(t)
	const source = "https://example.com/a.jpg"
	tests := []struct {
		name, query, options string
	}{
		{"resize", "o=rs:fill:300:200/g:sm", "rs:fill:300:200/g:sm"},
		{"chained", "o=rs:fit:500:500/wm:0.5:nowe/-/wm:0.7:soea", "rs:fit:500:500/wm:0.5:nowe/-/wm:0.7:soea"},
		{"output", "o=q:80/f:webp/sm:1/fq:png:50", "q:80/f:webp/sm:1/fq:png:50"},
		{"geometry", "o=c:100:50:nowe:10:20/t:10:fff:1:0/pd:1:2:3:4/rot:90/fl:1:0", "c:100:50:nowe:10:20/t:10:fff:1:0/pd:1:2:3:4/rot:90/fl:1:0"},
		{"effects", "o=bl:2/sh:1/pix:4/bg:ff0000/bga:0.5", "bl:2/sh:1/pix:4/bg:ff0000/bga:0.5"},
		{"delivery", "o=cb:1/fn:photo/att:1/exp:4102444800/pr:a:b", "cb:1/fn:photo/att:1/exp:4102444800/pr:a:b"},
		{"later_milestone", "o=sa:1.2/pg:2/wmu:aHR0cA", "sa:1.2/pg:2/wmu:aHR0cA"},
		{"native_resize", "w=300&h=200&fit=cover&g=smart", "w:300/h:200/rt:fill/g:sm"},
		{"native_output", "f=webp&q=80&strip=1&attachment=1&cb=x&filename=a", "f:webp/q:80/sm:1/att:1/cb:x/fn:a"},
		{"native_geometry", "crop=100,50,10,20&rot=90&flip=hv&bg=fff&pad=1,2&trim=5", "c:100:50:nowe:10:20/rot:90/fl:1:1/bg:fff/pd:1:2/t:5"},
		{"native_effects", "blur=2&sharpen=1&pixelate=3&dpr=2&enlarge=1&page=1&preset=p", "bl:2/sh:1/pix:3/dpr:2/el:1/page:1/pr:p"},
		{"native_gravity_offsets", "g=southwest:5:6", "g:sowe:5:6"},
		{"native_fp", "g=fp:0.2:0.8", "g:fp:0.2:0.8"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q, err := f.parsers["default"].Parse(&dialect.Input{Path: "/", RawQuery: "url=" + url.QueryEscape(source) + "&" + tc.query})
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			ip, err := f.imgproxy.Parse(&dialect.Input{Path: "/unsafe/" + tc.options + "/plain/" + source})
			if err != nil {
				t.Fatalf("imgproxy: %v", err)
			}
			got, want := *q.Request, *ip.Request
			got.Dialect, want.Dialect = "", ""
			if d := cmp.Diff(want, got); d != "" {
				t.Errorf("query != imgproxy (-imgproxy +query):\n%s", d)
			}
			if q.SourceString != ip.SourceString {
				t.Errorf("source %q != %q", q.SourceString, ip.SourceString)
			}
		})
	}
}

func TestNew(t *testing.T) {
	t.Parallel()
	for _, sep := range []string{"/", "-", "%"} {
		if _, err := query.New(&query.Options{ArgumentsSeparator: sep}); err == nil {
			t.Errorf("separator %q accepted", sep)
		}
	}
	p, err := query.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != query.Name || p.Verifier() != nil {
		t.Fatalf("Name %q Verifier %v", p.Name(), p.Verifier())
	}
	res, err := p.Parse(&dialect.Input{Path: "/", RawQuery: src + "&w=1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Request.Pipelines[0].Resize.Width != (transform.Dim{Px: 1}) {
		t.Fatalf("width %+v", res.Request.Pipelines[0].Resize.Width)
	}
}
