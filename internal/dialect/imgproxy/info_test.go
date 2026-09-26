package imgproxy_test

import (
	"strings"
	"testing"
)

// info builds an /info case "/unsafe/<options>/plain/https://example.com/a.jpg".
func info(name, options string) testCase {
	p := "/unsafe" + src
	if options != "" {
		p = "/unsafe/" + options + src
	}
	return testCase{name: name, path: p, info: true}
}

// infoCfg is info with a named parser configuration and a full path.
func infoCfg(name, cfg, path string) testCase {
	return testCase{name: name, cfg: cfg, path: path, info: true}
}

// TestInfo covers the /info option grammar of docs/URL-DIALECTS.md §3.3:
// defaults, every option under each alias, option combinations, shared
// processing options, sources and signatures.
func TestInfo(t *testing.T) {
	t.Parallel()
	runGolden(t, "info", []testCase{
		// Defaults and the docs example.
		info("defaults", ""),
		info("docs_example", "s:1/f:1/d:1/bh:4:3"),
		info("bare_names_are_not_options", "s/f/d/bh:4:3"),
		// Default-on options, turned off.
		info("size_off", "size:0"),
		info("s_off", "s:0"),
		info("format_off", "format:false"),
		info("f_off", "f:0"),
		info("dimensions_off", "dimensions:0"),
		info("d_off", "d:f"),
		info("exif_off", "exif:0"),
		info("exif_canonical", "exif:1:1"),
		info("exif_bad", "exif:yes"),
		info("exif_too_many_args", "exif:1:1:1"),
		info("iptc_off", "iptc:0"),
		info("xmp_off", "xmp:0"),
		info("video_meta_off", "video_meta:0"),
		info("vm_on", "vm:1"),
		info("all_default_off", "s:0/f:0/d:0/exif:0/iptc:0/xmp:0/vm:0"),
		info("empty_bool_is_false", "s:"),
		// Default-off options.
		info("detect_objects", "detect_objects:1"),
		info("do", "do:t"),
		info("classify", "classify:3:cat:dog"),
		info("cl_top1", "cl:1"),
		info("cl_off", "cl:0"),
		info("cl_empty_class", "cl:2::dog"),
		info("colorspace", "colorspace:1"),
		info("cs", "cs:1"),
		info("bands", "bands:1"),
		info("b", "b:1"),
		info("sample_format", "sample_format:1"),
		info("sf", "sf:1"),
		info("pages_number", "pages_number:1"),
		info("pn", "pn:1"),
		info("alpha", "alpha:1"),
		info("a_check_transparency", "a:1:1"),
		info("a_off", "a:0"),
		info("crop", "crop:100:200"),
		info("c_gravity", "c:0.5:0.5:sm"),
		info("c_fp", "c:100:100:fp:0.2:0.3"),
		info("c_obj_gravity", "c:100:100:obj:face"),
		info("c_bad", "c:-5"),
		info("objects_position", "objects_position:no:5:5"),
		info("obj_pos_prop", "obj_pos:prop"),
		info("op_fp", "op:fp:0.1:0.9"),
		info("op_bad", "op:xx"),
		info("palette", "palette:16"),
		info("p_off", "p:0"),
		info("p_max", "p:256"),
		info("p_one", "p:1"),
		info("p_too_many", "p:257"),
		info("average", "average:1"),
		info("avg_keep_transparent", "avg:1:0"),
		info("avg_off", "avg:1/avg:0"),
		info("avg_bad", "avg:x"),
		info("dominant_colors", "dominant_colors:1"),
		info("dc_build_missed", "dc:1:1"),
		info("dc_off", "dc:0:1"),
		info("blurhash", "blurhash:4:3"),
		info("bh_max", "bh:9:9"),
		info("bh_off", "bh:4:3/bh:0:0"),
		info("bh_one_zero", "bh:0:3"),
		info("bh_too_large", "bh:10:1"),
		info("bh_single_arg", "bh:4"),
		info("thumbhash", "thumbhash:1"),
		info("thumb_hash", "thumb_hash:1"),
		info("th", "th:1"),
		info("perceptual_hash", "perceptual_hash:1"),
		info("phash", "phash:1"),
		info("ph", "ph:1"),
		info("calc_hashsums", "calc_hashsums:md5:sha256"),
		info("chs_all_dedup", "chs:md5:sha1:sha256:sha512:md5"),
		info("chs_cleared", "chs:md5/chs:"),
		info("chs_bad", "chs:crc32"),
		// Shared processing-like options.
		info("page", "page:3"),
		info("pg_negative", "pg:-1"),
		info("video_thumbnail_second", "video_thumbnail_second:10"),
		info("vts_vtk", "vts:2.5/vtk:1"),
		info("cachebuster", "cachebuster:v1"),
		info("cb", "cb:abc"),
		info("expires_future", "expires:4102444800"),
		info("exp_past", "exp:1000"),
		info("preset", "preset:meta"),
		info("pr_many", "pr:a:b"),
		info("hashsum", "hashsum:sha1:"+strings.Repeat("a", 40)),
		info("hs_bad", "hs:sha1:00"),
		info("bypass_cache", "bc:1"),
		info("cache_tags", "ct:a:b"),
		info("msr_not_allowed", "msr:10"),
		info("msfs_not_allowed", "msfs:10"),
		infoCfg("max_src_resolution", "security", "/unsafe/max_src_resolution:50/msfs:1024"+src),
		// Option combinations.
		info("combo_everything", strings.Join([]string{
			"s:1", "f:1", "d:1", "exif:1", "iptc:1", "xmp:1", "vm:1", "do:1", "cl:1", "cs:1", "b:1",
			"sf:1", "pn:1", "a:1", "c:10:10", "op:ce", "p:8", "avg:1", "dc:1", "bh:3:3", "th:1",
			"ph:1", "chs:sha256", "pg:1", "vts:1", "vtk:0", "cb:x", "pr:p", "bc:1", "ct:t",
		}, "/")),
		info("combo_only_hashes", "s:0/f:0/d:0/exif:0/iptc:0/xmp:0/vm:0/th:1/ph:1/bh:4:4"),
		info("combo_later_wins", "p:8/p:16/c:10:10/c:20"),
		// Processing-only options and grammar in info URLs.
		info("processing_option_unknown", "rs:fit:100:100"),
		info("resizing_type_first_segment", "fit"),
		info("chained_rejected", "s:1/-/f:1"),
		infoCfg("strict_unknown", "strict", "/unsafe/rs:fit:1:1"+src),
		infoCfg("strict_known", "strict", "/unsafe/s:1/p:4/chs:md5"+src),
		infoCfg("strict_processing_alias_rejected", "strict", "/unsafe/q:80"+src),
		// Sources.
		{name: "plain_extension_ignored", path: "/unsafe/s:1" + src + "@png", info: true},
		{name: "base64_source", path: "/unsafe/s:1" + srcB64, info: true},
		{name: "base64_extension_ignored", path: "/unsafe" + srcB64 + ".webp", info: true},
		infoCfg("enc_source", "enc", "/unsafe/d:1/enc/"+encToken),
		infoCfg("base64_filename", "filename", "/unsafe/s:1"+srcB64+"/pic.jpg"),
		info("missing_source", "s:1/p:4"),
		{name: "empty_path", path: "", info: true},
		{name: "percent_encoded_colon", path: "/unsafe/bh%3A4%3A3" + src, info: true},
		// Signatures.
		infoCfg("signed_valid", "signed", "/SIG/s:1/bh:4:3"+src),
		infoCfg("signed_required_valid", "required", "/SIG/p:8/enc/"+encToken),
		infoCfg("signed_processing_vector", "signed8", "/"+exampleSig8+examplePath),
		infoCfg("signed_tampered", "signed8", "/"+exampleSig8+"/s:1/plain/http://img.example.com/pretty/image.jpg"),
		infoCfg("signed_unsafe_rejected", "required", "/unsafe/s:1"+src),
		infoCfg("signed_trusted", "signed", "/"+trustedSig+"/p:4"+src),
	})
}
