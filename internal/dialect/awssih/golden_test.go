package awssih_test

import (
	"testing"
)

// Request fixtures ported from SIH's own test suite
// (.refs/aws-image-handler/source/image-handler/test/image-request/*.spec.ts,
// Apache-2.0, see NOTICE). Paths are copied verbatim; test names cite the
// spec file.
func TestGoldenRequestFixtures(t *testing.T) {
	t.Parallel()
	baseRequest := `{"bucket":"validBucket","requestType":"Default","key":"validKey"}`
	runGolden(t, "request", []testCase{
		// decode-request.spec.ts
		{name: "decode_valid", path: "/eyJidWNrZXQiOiJidWNrZXQtbmFtZS1oZXJlIiwia2V5Ijoia2V5LW5hbWUtaGVyZSJ9"},
		{name: "decode_not_base64", path: "/someNonBase64EncodedContentHere"},
		{name: "decode_no_path", path: ""},
		{name: "expires_past", path: b64raw(baseRequest), query: "expires=19700101T000000Z"},
		{name: "expires_bad_month", path: b64raw(baseRequest), query: "expires=19700001T000000Z"},
		{name: "expires_bad_separator", path: b64raw(baseRequest), query: "expires=19700101S000000Z"},
		{name: "expires_no_zone", path: b64raw(baseRequest), query: "expires=19700101T000000"},
		{name: "expires_absent", path: b64raw(baseRequest)},
		{name: "expires_future", path: b64raw(baseRequest), query: "expires=20270926T120000Z"},
		// parse-request-type.spec.ts
		{name: "type_default", path: "/eyJidWNrZXQiOiJteS1zYW1wbGUtYnVja2V0Iiwia2V5IjoibXktc2FtcGxlLWtleSIsImVkaXRzIjp7ImdyYXlzY2FsZSI6dHJ1ZX19"},
		{name: "type_thumbor_without_forwarding", path: "/unsafe/filters:brightness(10):contrast(30)/image"},
		{name: "type_bmp_without_forwarding", path: "12x12e24d234r2ewxsad123d34r.bmp"},
		// parse-image-edits.spec.ts: edits without a key (SIH fails later
		// fetching an undefined key).
		{name: "edits_without_key", path: "/eyJlZGl0cyI6eyJncmF5c2NhbGUiOiJ0cnVlIiwicm90YXRlIjo5MCwiZmxpcCI6InRydWUifX0="},
		// parse-image-headers.spec.ts
		{name: "headers_cache_control", path: "/eyJidWNrZXQiOiJ2YWxpZEJ1Y2tldCIsImtleSI6InZhbGlkS2V5IiwiaGVhZGVycyI6eyJDYWNoZS1Db250cm9sIjoibWF4LWFnZT0zMTUzNjAwMCxwdWJsaWMifSwib3V0cHV0Rm9ybWF0IjoianBlZyJ9"},
		{name: "headers_restricted", path: "/eyJidWNrZXQiOiJ2YWxpZEJ1Y2tldCIsImtleSI6InZhbGlkS2V5IiwiaGVhZGVycyI6eyJDYWNoZS1Db250cm9sIjoibWF4LWFnZT0zMTUzNjAwMCxwdWJsaWMiLCAiVHJhbnNmZXItZW5jb2RpbmciOiAidmFsdWUiLCAieC1hcGkta2V5IjogInZhbHVlIiwgIngtYW16LWhlYWRlciI6ICJ2YWx1ZSIsICJjb250ZW50LXR5cGUiOiAiaHRtbCIsICJBY2Nlc3MtQ29udHJvbC1BbGxvdy1PcmlnaW4iOiAiKiJ9LCJvdXRwdXRGb3JtYXQiOiJqcGVnIn0="},
		{name: "headers_absent", path: "/eyJidWNrZXQiOiJ2YWxpZEJ1Y2tldCIsImtleSI6InZhbGlkS2V5In0="},
		// index.spec.ts
		{name: "index_custom_header", path: "/eyJidWNrZXQiOiJzb3VyY2UtYnVja2V0Iiwia2V5IjoidGVzdC5qcGciLCJoZWFkZXJzIjp7IkN1c3RvbS1IZWFkZXIiOiJDdXN0b21WYWx1ZSJ9fQ=="},
		{name: "index_wrong_filter", path: "eyJidWNrZXQiOiJzb3VyY2UtYnVja2V0Iiwia2V5IjoidGVzdC5qcGciLCJlZGl0cyI6eyJ3cm9uZ0ZpbHRlciI6dHJ1ZX19"},
		{name: "index_wrong_filter_strict", cfg: "strict", path: "eyJidWNrZXQiOiJzb3VyY2UtYnVja2V0Iiwia2V5IjoidGVzdC5qcGciLCJlZGl0cyI6eyJ3cm9uZ0ZpbHRlciI6dHJ1ZX19"},
		{name: "index_arabic_key", path: "/eyJidWNrZXQiOiJlbGFzdGljYmVhbnN0YWxrLXVzLWVhc3QtMi0wNjY3ODQ4ODU1MTgiLCJrZXkiOiJlbnYtcHJvZC9nY2MvbGFuZGluZ3BhZ2UvMV81N19TbGltTl9MaWZ0LUNvcnNldC1Gb3ItTWVuLVNOQVAvYXR0YWNobWVudHMvZmZjMWYxNjAtYmQzOC00MWU4LThiYWQtZTNhMTljYzYxZGQzX1/Ys9mE2YrZhSDZhNmK2YHYqiAoMikuanBnIiwiZWRpdHMiOnsicmVzaXplIjp7IndpZHRoIjo0ODAsImZpdCI6ImNvdmVyIn19fQ=="},
		// setup.spec.ts
		{name: "setup_grayscale_jpeg", path: "/eyJidWNrZXQiOiJ2YWxpZEJ1Y2tldCIsImtleSI6InZhbGlkS2V5IiwiZWRpdHMiOnsiZ3JheXNjYWxlIjp0cnVlfSwib3V0cHV0Rm9ybWF0IjoianBlZyJ9"},
		{name: "setup_chinese_key_no_slash", path: "eyJidWNrZXQiOiJ2YWxpZEJ1Y2tldCIsImtleSI6IuS4reaWhyIsImVkaXRzIjp7ImdyYXlzY2FsZSI6dHJ1ZX0sIm91dHB1dEZvcm1hdCI6ImpwZWcifQ=="},
		{name: "setup_to_format_png", path: "/eyJidWNrZXQiOiJ2YWxpZEJ1Y2tldCIsImtleSI6InZhbGlkS2V5IiwiZWRpdHMiOnsidG9Gb3JtYXQiOiJwbmcifX0="},
		{name: "setup_grayscale_string", path: "/eyJidWNrZXQiOiJhbGxvd2VkQnVja2V0MDAxIiwia2V5Ijoic2FtcGxlSW1hZ2VLZXkwMDEuanBnIiwiZWRpdHMiOnsiZ3JheXNjYWxlIjoidHJ1ZSJ9fQ=="},
		{name: "setup_no_bucket_extra_padding", path: "/eyJrZXkiOiJzYW1wbGVJbWFnZUtleTAwMS5qcGciLCJlZGl0cyI6eyJncmF5c2NhbGUiOiJ0cnVlIn19=="},
		{name: "setup_no_edits", path: "/eyJidWNrZXQiOiJteS1zYW1wbGUtYnVja2V0Iiwia2V5Ijoic2FtcGxlLWltYWdlLTAwMS5qcGcifQ=="},
		{name: "setup_grayscale", path: "/eyJidWNrZXQiOiJ2YWxpZEJ1Y2tldCIsImtleSI6InZhbGlkS2V5IiwiZWRpdHMiOnsiZ3JheXNjYWxlIjp0cnVlfX0="},
		{name: "setup_reduction_effort_3", path: "/eyJidWNrZXQiOiJ0ZXN0Iiwia2V5IjoidGVzdC5wbmciLCJvdXRwdXRGb3JtYXQiOiJ3ZWJwIiwicmVkdWN0aW9uRWZmb3J0IjozfQ=="},
		{name: "setup_reduction_effort_string", path: "/eyJidWNrZXQiOiJ0ZXN0Iiwia2V5IjoidGVzdC5wbmciLCJvdXRwdXRGb3JtYXQiOiJ3ZWJwIiwicmVkdWN0aW9uRWZmb3J0IjoidGVzdCJ9"},
		{name: "setup_reduction_effort_10", path: "/eyJidWNrZXQiOiJ0ZXN0Iiwia2V5IjoidGVzdC5wbmciLCJvdXRwdXRGb3JtYXQiOiJ3ZWJwIiwicmVkdWN0aW9uRWZmb3J0IjoxMH0="},
		{name: "setup_webp", path: "/eyJidWNrZXQiOiJ0ZXN0Iiwia2V5IjoidGVzdC5wbmciLCJvdXRwdXRGb3JtYXQiOiJ3ZWJwIn0="},
		{name: "setup_webp_with_query_format", cfg: "query", path: "/eyJidWNrZXQiOiJ0ZXN0Iiwia2V5IjoidGVzdC5wbmciLCJvdXRwdXRGb3JtYXQiOiJ3ZWJwIn0=", query: "format=png"},
		{name: "setup_chinese_key_test_bucket", path: "eyJidWNrZXQiOiJ0ZXN0Iiwia2V5Ijoi5Lit5paHIiwiZWRpdHMiOnsiZ3JheXNjYWxlIjp0cnVlfSwib3V0cHV0Rm9ybWF0IjoianBlZyJ9"},
		{name: "setup_bucket_not_allowed", path: b64(map[string]any{"bucket": "notAllowedBucket", "key": "sampleImageKey001.jpg"})},
		// effort is the current field name (DefaultImageRequest.effort).
		{name: "effort_3", path: b64raw(`{"bucket":"test","key":"test.png","outputFormat":"webp","effort":3}`)},
		{name: "effort_invalid_falls_back_to_4", path: b64raw(`{"bucket":"test","key":"test.png","outputFormat":"webp","effort":10}`)},
		{name: "effort_ignored_for_jpeg", path: b64raw(`{"bucket":"test","key":"test.png","outputFormat":"jpeg","effort":1}`)},
	})
}

// Edits from SIH's image handler specs
// (test/image-handler/*.spec.ts, Apache-2.0), wrapped in a request.
func TestGoldenHandlerFixtures(t *testing.T) {
	t.Parallel()
	req := func(edits string) string {
		return b64raw(`{"bucket":"validBucket","key":"validKey","edits":` + edits + `}`)
	}
	runGolden(t, "handler", []testCase{
		// allowlist.spec.ts: toFile is not allowlisted.
		{name: "allowlist_to_file", path: req(`{"rotate":null,"toFile":"test"}`)},
		{name: "allowlist_to_file_strict", cfg: "strict", path: req(`{"rotate":null,"toFile":"test"}`)},
		// animated.spec.ts
		{name: "animated_true", path: req(`{"grayscale":true,"animated":true}`)},
		{name: "animated_false", path: req(`{"grayscale":true,"animated":false}`)},
		// standard.spec.ts / format.spec.ts
		{name: "standard_grayscale_flip", path: req(`{"grayscale":true,"flip":true}`)},
		{name: "format_jpeg", path: b64raw(`{"bucket":"validBucket","key":"validKey","edits":{"grayscale":true,"flip":true},"outputFormat":"jpeg"}`)},
		{name: "format_webp_effort_3", path: b64raw(`{"bucket":"validBucket","key":"validKey","edits":{"grayscale":true,"flip":true},"outputFormat":"webp","effort":3}`)},
		// rotate.spec.ts
		{name: "rotate_null_auto_orients", path: req(`{"rotate":null}`)},
		{name: "rotate_empty_edits", path: req(`{}`)},
		// crop.spec.ts
		{name: "crop_100", path: req(`{"crop":{"left":0,"top":0,"width":100,"height":100}}`)},
		{name: "crop_1", path: req(`{"crop":{"left":0,"top":0,"width":1,"height":1}}`)},
		{name: "crop_missing_top", path: req(`{"crop":{"left":0,"right":0,"width":1,"height":1}}`)},
		// resize.spec.ts
		{name: "resize_string_dims_round", path: req(`{"resize":{"width":"99.1","height":"99.9"}}`)},
		{name: "resize_zero", path: req(`{"resize":{"width":0,"height":0}}`)},
		{name: "resize_null_height", path: req(`{"resize":{"width":100,"height":null}}`)},
		// overlay.spec.ts
		{name: "overlay_plain", path: req(`{"overlayWith":{"bucket":"bucket","key":"key"}}`)},
		{name: "overlay_minus_1", path: req(`{"overlayWith":{"bucket":"bucket","key":"key","options":{"left":"-1","top":"-1"}}}`)},
		{name: "overlay_plus_1", path: req(`{"overlayWith":{"bucket":"bucket","key":"key","options":{"left":"1","top":"1"}}}`)},
		{name: "overlay_50p", path: req(`{"overlayWith":{"bucket":"bucket","key":"key","options":{"left":"50p","top":"50p"}}}`)},
		{name: "overlay_25_5p", path: req(`{"overlayWith":{"bucket":"bucket","key":"key","options":{"left":"25.5p","top":"25.5p"}}}`)},
		{name: "overlay_minus_50p", path: req(`{"overlayWith":{"bucket":"bucket","key":"key","options":{"left":"-50p","top":"-50p"}}}`)},
		{name: "overlay_ratios_alpha", path: req(`{"overlayWith":{"bucket":"sample-bucket","key":"test.jpg","wRatio":"10","hRatio":"10","alpha":"30"}}`)},
		{name: "overlay_bucket_not_allowed", path: req(`{"overlayWith":{"bucket":"invalidBucket","key":"test.jpg"}}`)},
		{name: "overlay_with_resize", path: req(`{"resize":{"width":400,"height":400},"overlayWith":{"bucket":"sample-bucket","key":"test.jpg","options":{"gravity":"southeast"}}}`)},
		// smart-crop.spec.ts
		{name: "smart_crop_face_0", path: req(`{"smartCrop":{"faceIndex":0,"padding":0}}`)},
		{name: "smart_crop_webp_padding", path: req(`{"toFormat":"webp","smartCrop":{"padding":60}}`)},
		{name: "smart_crop_face_10", path: req(`{"smartCrop":{"faceIndex":10,"padding":0}}`)},
		{name: "smart_crop_true", path: req(`{"smartCrop":true}`)},
		// round-crop.spec.ts
		{name: "round_crop_true", path: req(`{"roundCrop":true}`)},
		{name: "round_crop_params", path: req(`{"roundCrop":{"top":100,"left":100,"rx":100,"ry":100}}`)},
		{name: "round_crop_ellipse", path: req(`{"roundCrop":{"rx":100,"ry":50}}`)},
		// content-moderation.spec.ts (Rekognition; not supported)
		{name: "content_moderation_min_confidence", path: req(`{"contentModeration":{"minConfidence":70}}`)},
		{name: "content_moderation_true_strict", cfg: "strict", path: req(`{"contentModeration":true}`)},
		// limit-input-pixels.spec.ts / standard.spec.ts metadata edits
		{name: "strip_exif", path: req(`{"stripExif":true}`)},
		{name: "strip_icc", path: req(`{"stripIcc":true}`)},
	})
}

// Sharp edits beyond the SIH fixtures, one or more per supported option.
func TestGoldenEdits(t *testing.T) {
	t.Parallel()
	req := func(edits string) string {
		return b64raw(`{"key":"photos/cat.jpg","edits":` + edits + `}`)
	}
	runGolden(t, "edits", []testCase{
		{name: "resize_cover_attention", path: req(`{"resize":{"width":300,"height":200,"fit":"cover","position":"attention"}}`)},
		{name: "resize_cover_entropy_numeric", path: req(`{"resize":{"width":300,"height":200,"position":16}}`)},
		{name: "resize_cover_right_top", path: req(`{"resize":{"width":300,"height":200,"position":"right top"}}`)},
		{name: "resize_contain_background", path: req(`{"resize":{"width":300,"height":200,"fit":"contain","background":{"r":255,"g":0,"b":0,"alpha":0.5},"position":"north"}}`)},
		{name: "resize_contain_default_black", path: req(`{"resize":{"width":300,"height":200,"fit":"contain"}}`)},
		{name: "resize_inside_without_enlargement", path: req(`{"resize":{"width":300,"fit":"inside","withoutEnlargement":true,"kernel":"nearest"}}`)},
		{name: "resize_fill", path: req(`{"resize":{"width":300,"height":200,"fit":"fill"}}`)},
		{name: "resize_outside", path: req(`{"resize":{"width":300,"height":200,"fit":"outside"}}`)},
		{name: "resize_ratio_both", path: req(`{"resize":{"width":300,"height":200,"ratio":0.5}}`)},
		{name: "resize_ratio_source", path: req(`{"resize":{"ratio":0.25}}`)},
		{name: "resize_bad_fit", path: req(`{"resize":{"width":300,"fit":"stretch"}}`)},
		{name: "resize_bad_position", path: req(`{"resize":{"width":300,"height":200,"position":"middle"}}`)},
		{name: "resize_negative", path: req(`{"resize":{"width":-5}}`)},
		{name: "resize_not_number", path: req(`{"resize":{"width":"abc"}}`)},
		{name: "resize_too_large", path: req(`{"resize":{"width":100000}}`)},
		{name: "resize_mks_kernel_unknown_field", path: req(`{"resize":{"width":10,"kernel":"mks2021","fastShrinkOnLoad":false,"foo":1}}`)},
		{name: "extract_before_resize", path: req(`{"extract":{"left":10,"top":20,"width":100,"height":50},"resize":{"width":40}}`)},
		{name: "extract_after_resize", path: req(`{"resize":{"width":400},"extract":{"left":10,"top":20,"width":100,"height":50},"extend":5}`)},
		{name: "extract_negative", path: req(`{"extract":{"left":-1,"top":0,"width":1,"height":1}}`)},
		{name: "extend_object", path: req(`{"extend":{"top":1,"right":2,"bottom":3,"left":4,"background":"#00ff0080","extendWith":"mirror"}}`)},
		{name: "extend_zero_number", path: req(`{"extend":0}`)},
		{name: "trim_true", path: req(`{"trim":true}`)},
		{name: "trim_number", path: req(`{"trim":25}`)},
		{name: "trim_object", path: req(`{"trim":{"background":"white","threshold":5,"lineArt":true}}`)},
		{name: "trim_false", path: req(`{"trim":false}`)},
		{name: "rotate_90", path: req(`{"rotate":90}`)},
		{name: "rotate_minus_90", path: req(`{"rotate":-90}`)},
		{name: "rotate_450", path: req(`{"rotate":450}`)},
		{name: "rotate_any", path: req(`{"rotate":200.5}`)},
		{name: "rotate_string", path: req(`{"rotate":"x"}`)},
		{name: "flip_flop_false", path: req(`{"flip":false,"flop":"no"}`)},
		{name: "greyscale_later_wins", path: req(`{"greyscale":true,"grayscale":false}`)},
		{name: "negate_normalise", path: req(`{"negate":{"alpha":false},"normalise":{"lower":1,"upper":99}}`)},
		{name: "flatten_default", path: req(`{"flatten":true}`)},
		{name: "flatten_background", path: req(`{"flatten":{"background":"rgb(10, 20, 30)"}}`)},
		{name: "tint_hex", path: req(`{"tint":"#abc"}`)},
		{name: "tint_object", path: req(`{"tint":{"r":1,"g":2,"b":3}}`)},
		{name: "tint_bad", path: req(`{"tint":"chartreuse-ish"}`)},
		{name: "sharpen_mild", path: req(`{"sharpen":true}`)},
		{name: "sharpen_sigma", path: req(`{"sharpen":{"sigma":2}}`)},
		{name: "sharpen_sigma_extras", path: req(`{"sharpen":{"sigma":1.5,"m1":1,"m2":2}}`)},
		{name: "sharpen_out_of_range", path: req(`{"sharpen":{"sigma":11}}`)},
		{name: "blur_sigma", path: req(`{"blur":5}`)},
		{name: "blur_mild", path: req(`{"blur":true}`)},
		{name: "blur_object", path: req(`{"blur":{"sigma":3,"precision":"float"}}`)},
		{name: "blur_too_small", path: req(`{"blur":0.1}`)},
		{name: "modulate", path: req(`{"modulate":{"brightness":1.2,"saturation":0.5,"hue":400,"lightness":10}}`)},
		{name: "unsupported_ignored", path: req(`{"median":3,"threshold":128,"gamma":2.2,"clahe":{"width":3,"height":3}}`)},
		{name: "unsupported_strict", cfg: "strict", path: req(`{"median":3}`)},
		{name: "to_format_jpg_alias", path: req(`{"toFormat":"jpg"}`)},
		{name: "to_format_raw", path: req(`{"toFormat":"raw"}`)},
		{name: "jpeg_options", path: req(`{"jpeg":{"quality":80,"progressive":true,"chromaSubsampling":"4:4:4","mozjpeg":true}}`)},
		{name: "jpeg_options_individual", path: req(`{"jpeg":{"trellisQuantisation":true,"overshootDeringing":false,"optimiseScans":true,"quantisationTable":2}}`)},
		{name: "png_options", path: req(`{"png":{"quality":60,"compressionLevel":9,"adaptiveFiltering":true,"palette":true,"colours":64,"progressive":true,"effort":7}}`)},
		{name: "webp_options", path: req(`{"webp":{"quality":70,"lossless":true,"smartSubsample":true,"preset":"photo","effort":6,"alphaQuality":80}}`)},
		{name: "webp_near_lossless", path: req(`{"webp":{"nearLossless":true}}`)},
		{name: "avif_options", path: req(`{"avif":{"quality":50,"lossless":false,"effort":9,"chromaSubsampling":"4:2:0"}}`)},
		{name: "heif_options", path: req(`{"heif":{"quality":50,"compression":"hevc"}}`)},
		{name: "gif_tiff", path: req(`{"gif":{"effort":10},"tiff":{"quality":90}}`)},
		{name: "format_edit_force_false", path: req(`{"webp":{"quality":70,"force":false}}`)},
		{name: "format_edit_then_to_format", path: req(`{"webp":{"quality":70,"lossless":true},"toFormat":"png"}`)},
		{name: "output_format_overrides_edit_format", path: b64raw(`{"key":"a.jpg","edits":{"png":{"quality":70}},"outputFormat":"avif"}`)},
		{name: "to_format_overrides_output_format", path: b64raw(`{"key":"a.jpg","edits":{"toFormat":"gif"},"outputFormat":"avif"}`)},
		{name: "output_format_heic_raw", path: b64raw(`{"key":"a.jpg","outputFormat":"raw"}`)},
		{name: "jpeg_quality_out_of_range", path: req(`{"jpeg":{"quality":0}}`)},
		{name: "headers_full", path: b64raw(`{"key":"a.jpg","headers":{"Cache-Control":"public, max-age=600, immutable","Cache-Tag":"a, b c","X-Custom":"v","Content-Disposition":"attachment","Expires":"x"}}`)},
		{name: "headers_cache_control_partial", path: b64raw(`{"key":"a.jpg","headers":{"cache-control":"private, max-age=60"}}`)},
		{name: "key_escaped", path: b64raw(`{"key":"https://evil.example/x.jpg?y=1#z%41"}`)},
		{name: "key_empty", path: b64raw(`{"key":""}`)},
		{name: "key_number", path: b64raw(`{"key":5}`)},
		{name: "key_control", path: b64raw(`{"key":"a\nb.jpg"}`)},
		{name: "bucket_number", path: b64raw(`{"bucket":5,"key":"a.jpg"}`)},
		{name: "json_array_is_not_default", path: b64raw(`[{"key":"a.jpg"}]`)},
		{name: "json_duplicate_key_last_wins", path: b64raw(`{"key":"a.jpg","edits":{"blur":5,"rotate":90,"blur":7}}`)},
		{name: "url_safe_unpadded", path: "/eyJrZXkiOiJhPz8_Pj4-LmpwZyJ9"},
		{name: "edits_not_object", path: b64raw(`{"key":"a.jpg","edits":[1]}`)},
		{name: "edits_null", path: b64raw(`{"key":"a.jpg","edits":null,"outputFormat":null}`)},
		{name: "too_deep", path: b64raw(`{"key":"a.jpg","edits":{"x":[[[[[[[[[[[[[[[[[[1]]]]]]]]]]]]]]]]]]}}`)},
		{name: "animated_not_bool", path: req(`{"animated":"yes"}`)},
	})
}

// SIH's query-param mapper (query-param-mapper.ts and its spec).
func TestGoldenQueryParams(t *testing.T) {
	t.Parallel()
	plain := b64raw(`{"key":"a.jpg"}`)
	withResize := b64raw(`{"key":"a.jpg","edits":{"resize":{"width":10,"height":20,"fit":"contain"},"blur":3}}`)
	runGolden(t, "query", []testCase{
		{name: "mapper_disabled", path: plain, query: "width=100&format=webp"},
		{name: "width_height_fit", cfg: "query", path: plain, query: "width=100&height=200&fit=cover"},
		{name: "zero_width_is_null", cfg: "query", path: plain, query: "width=0&height=200&fit=cover"},
		{name: "booleans_grayscale_to_greyscale", cfg: "query", path: plain, query: "flip=true&flop=0&grayscale=TRUE&rotate=90"},
		{name: "rotate_empty_auto_orient", cfg: "query", path: plain, query: "rotate="},
		{name: "format", cfg: "query", path: plain, query: "format=avif"},
		{name: "unknown_ignored", cfg: "query", path: plain, query: "unknown=value&width=100"},
		{name: "parse_int_prefix", cfg: "query", path: plain, query: "width=12abc"},
		{name: "not_a_number", cfg: "query", path: plain, query: "width=abc"},
		{name: "replaces_whole_resize", cfg: "query", path: withResize, query: "width=100"},
		{name: "last_value_wins", cfg: "query", path: plain, query: "width=1&width=2"},
		{name: "bad_escape", cfg: "query", path: plain, query: "width=%zz"},
	})
}

// Signature and expiry (setup.spec.ts "enableSignature").
func TestGoldenSignature(t *testing.T) {
	t.Parallel()
	const fixturePath = "/eyJidWNrZXQiOiJ2YWxpZEJ1Y2tldCIsImtleSI6InZhbGlkS2V5IiwiZWRpdHMiOnsidG9Gb3JtYXQiOiJwbmcifX0="
	const valid = "4d41311006641a56de7bca8abdbda91af254506107a2c7b338a13ca2fa95eac3"
	f := getFixture(t)
	signed, err := f.signer.SignAWSSIHQuery(fixturePath, "width=300&expires=20270101T000000Z")
	if err != nil {
		t.Fatal(err)
	}
	runGolden(t, "signature", []testCase{
		{name: "valid", cfg: "signed", path: fixturePath, query: "signature=" + valid},
		{name: "missing", cfg: "signed", path: fixturePath},
		{name: "mismatch", cfg: "signed", path: fixturePath, query: "signature=invalid"},
		{name: "last_char_differs", cfg: "signed", path: fixturePath, query: "signature=4d41311006641a56de7bca8abdbda91af254506107a2c7b338a13ca2fa95eac4"},
		{name: "query_covered", cfg: "signed", path: fixturePath, query: signed},
		{name: "query_tampered", cfg: "signed", path: fixturePath, query: signed + "&height=10"},
		{name: "optional_missing", cfg: "optional", path: fixturePath},
		{name: "optional_wrong", cfg: "optional", path: fixturePath, query: "signature=" + valid[:63] + "0"},
		{name: "checked_before_decoding", cfg: "signed", path: "/not-base64!"},
	})
}

// Thumbor and custom request types, forwarded to a fake thumbor dialect.
func TestGoldenForwarding(t *testing.T) {
	t.Parallel()
	runGolden(t, "forward", []testCase{
		// parse-request-type.spec.ts
		{name: "thumbor_unsafe", cfg: "thumbor", path: "/unsafe/filters:brightness(10):contrast(30)/https://upload.wikimedia.org/wikipedia/commons/thumb/7/79/Coffee_berries_1.jpg/1200px-Coffee_berries_1.jpg"},
		{name: "thumbor_no_extension", cfg: "thumbor", path: "/unsafe/filters:brightness(10):contrast(30)/image"},
		{name: "thumbor_image_no_slash", cfg: "thumbor", path: "image.jpg"},
		// parse-image-edits.spec.ts / setup.spec.ts
		{name: "thumbor_filters", cfg: "thumbor", path: "/filters:rotate(90)/filters:grayscale()/thumbor-image.jpg"},
		{name: "thumbor_fit_in", cfg: "thumbor", path: "/fit-in/400x400/filters:format(png)/filters:quality(50)/test-image-001.jpg"},
		{name: "custom_rewrite", cfg: "custom", path: "/filters-rotate(90)/filters-grayscale()/custom-image.jpg"},
		{name: "custom_rewrite_no_extension", cfg: "custom", path: "/filters-rotate(90)/filters-grayscale()/custom-image"},
		// parse-image-bucket.spec.ts: s3:<bucket> override.
		{name: "s3_bucket_override", cfg: "thumbor", path: "/filters:grayscale()/s3:sample-bucket/test-image-001.jpg"},
		{name: "s3_bucket_not_listed", cfg: "thumbor", path: "/filters:grayscale()/s3:other/test-image-001.jpg"},
		{name: "query_edits_applied", cfg: "thumbor", path: "/200x200/image.jpg", query: "format=webp&width=50&flip=true"},
		{name: "expires_applied", cfg: "thumbor", path: "/image.jpg", query: "expires=20270101T000000Z"},
		{name: "thumbor_error_propagates", cfg: "thumbor", path: "/fail/image.jpg"},
		{name: "base64_json_not_forwarded", cfg: "thumbor", path: b64raw(`{"key":"a.jpg"}`)},
	})
}
