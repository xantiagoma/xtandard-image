package imgproxy_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/preset"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

const videoSrc = "/plain/https://example.com/clip.mov"

// vid is a /video case with options (no leading slash) on videoSrc.
func vid(name, options string) testCase {
	return vidPath(name, "", "/unsafe/"+options+videoSrc)
}

func vidPath(name, cfg, path string) testCase {
	return testCase{name: name, cfg: cfg, path: path, video: true}
}

// TestVideo parses /video URLs (docs/URL-DIALECTS.md §3.8): every option
// and alias, the container from the extension, the geometry mapping and
// the errors, against testdata/golden/video.json.
func TestVideo(t *testing.T) {
	t.Parallel()
	runGolden(t, "video", []testCase{
		vidPath("defaults", "", "/unsafe"+videoSrc),
		vidPath("extension_webm", "", "/unsafe"+videoSrc+"@webm"),
		vidPath("extension_upper", "", "/unsafe"+videoSrc+"@MP3"),
		vidPath("extension_unknown", "", "/unsafe"+videoSrc+"@avi"),
		vidPath("base64_source_ext", "", "/unsafe/w:320/aHR0cHM6Ly9leGFtcGxlLmNvbS9jbGlwLm1vdg.webm"),
		vidPath("enc_source", "enc", "/unsafe/w:320/enc/ENC[https://example.com/clip.mov].m4a"),

		// Time.
		vid("start_end", "start:1.5/end:4"),
		vid("aliases_vs_vd", "vs:2/vd:3"),
		vid("ve", "ve:0.25"),
		vid("start_microseconds", "vs:0.0000015"),
		vid("start_negative", "vs:-1"),
		vid("start_not_a_number", "vs:abc"),
		vid("end_and_duration", "ve:3/vd:2"),
		vid("end_before_start", "vs:3/ve:2"),
		vid("duration_too_many_args", "vd:1:2"),

		// Geometry.
		vid("resize_fit", "rs:fit:640:360"),
		vid("resize_fit_enlarge", "rs:fit:640:360:1"),
		vid("resize_fit_extend", "rs:fit:640:360:0:1/bg:ffffff"),
		vid("resize_fill", "rs:fill:640:360"),
		vid("resize_fill_down", "rs:fill-down:640:360"),
		vid("resize_force", "rs:force:640:360"),
		vid("resize_auto", "rs:auto:640:360"),
		vid("resize_type_only", "rs:fill"),
		vid("resize_fill_one_side", "rs:fill:640"),
		vid("resize_too_many_args", "rs:fit:1:2:0:0:ce"),
		vid("size", "s:320:180:1"),
		vid("size_extend", "size:320:180::1"),
		vid("resizing_type_rt", "rt:fill/w:200/h:200"),
		vid("resizing_type_bad", "rt:crop"),
		vid("width_height", "width:640/height:360"),
		vid("w_h", "w:640/h:0"),
		vid("width_fraction", "w:0.5"),
		vid("width_huge", "w:9000"),
		vid("enlarge_el", "el:1/w:4000"),
		vid("extend_ex", "ex:1/w:100/h:100"),
		vid("gravity_compass", "g:soea:10:5/rs:fill:200:200"),
		vid("gravity_alias", "gravity:no/rt:fill/s:200:100"),
		vid("gravity_fp_refused", "g:fp:0.25:0.75/rs:fill:200:200"),
		vid("gravity_offset_fraction", "g:no:0.5"),
		vid("pad_gravity", "rs:fit:300:300:0:1/g:so"),
		vid("gravity_fp_missing_y", "g:fp:0.25"),
		vid("gravity_smart", "g:sm"),
		vid("gravity_offsets_kept", "g:no:3:4/g:ea"),
		vid("crop_pixels", "c:640:360"),
		vid("crop_gravity", "crop:640:360:nowe:10:20"),
		vid("crop_takes_gravity", "g:soea/c:100:100"),
		vid("crop_takes_gravity_offsets", "g:soea:10:5/c:100:100"),
		vid("crop_fp_refused", "c:100:0:fp:0.1:0.9"),
		vid("crop_width_only", "c:100"),
		vid("crop_relative", "c:0.5:0.5"),
		vid("rotate", "rot:90"),
		vid("rotate_negative", "rotate:-90"),
		vid("rotate_450", "rot:450"),
		vid("rotate_45", "rot:45"),
		vid("flip", "fl:1:1"),
		vid("flip_vertical_only", "flip::1"),
		vid("background", "background:ff0000/rs:fit:300:300:0:1"),
		vid("background_short", "bg:0f0"),
		vid("background_empty", "bg:fff/bg:"),
		vid("background_bad", "bg:zzz"),
		vid("fps", "fps:24"),
		vid("fps_fraction", "fps:29.97"),
		vid("fps_huge", "fps:500"),
		vid("fps_zero_keeps_source", "fps:0"),

		// Encoding.
		vid("format", "format:webm"),
		vid("format_f", "f:m4a"),
		vid("format_ext", "ext:opus"),
		vid("format_bad", "f:gif"),
		vidPath("format_extension_wins", "", "/unsafe/f:webm"+videoSrc+"@mp3"),
		vid("video_codec", "vc:av1"),
		vid("video_codec_h265", "video_codec:h265"),
		vid("video_codec_vp9_mp4", "vc:vp9"),
		vid("video_codec_vp9_webm", "vc:vp9/f:webm"),
		vid("video_codec_bad", "vc:mpeg2"),
		vid("audio_codec", "ac:aac"),
		vid("audio_codec_flac", "audio_codec:flac/f:flac"),
		vid("audio_codec_mismatch", "ac:opus"),
		vid("audio_codec_bad", "ac:vorbis"),
		vid("quality", "q:70"),
		vid("quality_long", "quality:100"),
		vid("quality_bad", "q:101"),
		vid("crf", "crf:30"),
		vid("crf_bad", "crf:64"),
		vid("video_bitrate", "vb:2M"),
		vid("video_bitrate_k", "video_bitrate:800k"),
		vid("video_bitrate_plain", "vb:1500000"),
		vid("video_bitrate_decimal", "vb:1.5M"),
		vid("video_bitrate_low", "vb:10k"),
		vid("video_bitrate_bad", "vb:fast"),
		vid("keyframe_interval", "ki:1"),
		vid("keyframe_interval_long", "keyframe_interval:0.5"),
		vid("keyframe_interval_tiny", "ki:0.01"),

		// Audio.
		vid("mute", "mute:1"),
		vid("mu", "mu:t"),
		vid("mute_audio_only", "mu:1/f:mp3"),
		vid("audio_bitrate", "ab:128k"),
		vid("audio_bitrate_long", "audio_bitrate:96000/f:m4a"),
		vid("audio_bitrate_high", "ab:1M"),
		vid("sample_rate", "sr:44100/f:mp3"),
		vid("sample_rate_long", "sample_rate:48000"),
		vid("sample_rate_odd", "sr:44000"),
		vid("audio_channels", "ach:1/f:m4a"),
		vid("audio_channels_long", "audio_channels:2"),
		vid("audio_channels_6", "ach:6"),
		vid("volume_factor", "vol:0.5"),
		vid("volume_db", "volume:-6dB"),
		vid("volume_db_positive", "vol:+3db"),
		vid("volume_db_low", "vol:-80dB"),
		vid("volume_bad", "vol:loud"),
		vid("volume_muted", "vol:2/mu:1"),
		vid("loudnorm", "ln:1"),
		vid("loudnorm_target", "loudnorm:1:-16:-1.5:11"),
		vid("loudnorm_partial", "ln:1:-14"),
		vid("loudnorm_off", "ln:1/ln:0"),
		vid("loudnorm_out_of_range", "ln:1:-80"),
		vid("loudnorm_bad", "ln:1:loud"),
		vid("fade", "fade:1:2"),
		vid("fade_in_only", "fd:0.5"),
		vid("fade_out_only", "fd::1"),
		vid("fade_bad", "fd:-1"),

		// Waveforms.
		vid("waveform", "wf:1"),
		vid("waveform_colours", "waveform:1:2266cc:ffffff:1/w:800/h:200"),
		vid("waveform_webp", "wf:1/f:webp"),
		vid("waveform_mp4", "wf:1/f:mp4"),
		vid("waveform_off", "wf:1/wf:0"),
		vid("waveform_bad_color", "wf:1:blue"),
		vid("png_implies_waveform", "f:png"),
		vidPath("png_extension", "", "/unsafe/w:600"+videoSrc+"@png"),

		// Overlays (WP-124).
		vid("overlay_image", "wm:0.8:soea:8:8/wmu:eGk6Ly9maXh0dXJlcy93YXRlcm1hcmsvYmFkZ2UucG5n/wms:48:0/ow:0.3:0.7/of:0.1:0.1"),
		vid("overlay_text", "wm:1:nowe:6:6/wmt:PGI-aGk8L2I-/wmr:15/wmsh:2"),
		vid("overlay_default_image", "watermark:0.5/overlay_window:1"),
		vid("overlay_zero_opacity", "wm:0/wmu:eGk6Ly9maXh0dXJlcy93YXRlcm1hcmsvYmFkZ2UucG5n"),
		vid("overlay_window_without_watermark", "ow:1:2"),
		vid("overlay_fades_too_long", "wm:1/ow:0:1/of:0.6:0.6"),
		vid("overlay_on_audio", "wm:1/f:m4a"),
		vid("timecode", "tc:hms"),
		vid("timecode_full", "timecode:seconds:noea:4:4:24:ffff00:0.8/wm:1/wmt:PGI-aGk8L2I-"),
		vid("timecode_none", "tc:hms/tc:none"),
		vid("timecode_bad_format", "tc:frames"),
		vid("timecode_bad_position", "tc:hms:fp"),
		vid("timecode_size_huge", "tc:hms:sowe:0:0:9999"),

		// Shared delivery options.
		vid("cachebuster", "cb:v2"),
		vid("expires_future", "exp:1893456000"),
		vid("expires_past", "exp:1000"),
		vid("filename", "fn:clip/att:1"),
		vid("hashsum", "hs:sha256:"+sha256Hex),
		vid("cache_tags", "ct:a:b"),

		// Raw source (WP-128): stays a video request; other options are kept
		// but ignored by the server.
		vid("raw", "raw:1"),
		vid("raw_off", "raw:0/w:320"),
		vidPath("raw_audio", "", "/unsafe/raw:true/vs:1"+videoSrc+"@mp3"),
		vid("raw_bad", "raw:maybe"),

		// Adaptive streaming (WP-126).
		vidPath("stream_ext_m3u8", "", "/unsafe"+videoSrc+"@m3u8"),
		vidPath("stream_ext_mpd", "", "/unsafe/vs:5/vd:60"+videoSrc+"@mpd"),
		vid("stream_option", "st:dash:240:720:480"),
		vid("stream_alias_segment", "stream:hls/sgd:4"),
		vidPath("stream_ext_after_option", "", "/unsafe/st:dash:360"+videoSrc+"@m3u8"),
		vidPath("stream_crop_codec_ki", "", "/unsafe/c:640:360:ce/rot:90/fps:25/vc:vp9/q:60/ki:2/ab:96k/ach:1"+videoSrc+"@m3u8"),
		vidPath("stream_size_rejected", "", "/unsafe/w:640"+videoSrc+"@m3u8"),
		vidPath("stream_bitrate_rejected", "", "/unsafe/vb:1m"+videoSrc+"@m3u8"),
		vidPath("stream_webm_rejected", "", "/unsafe/st:hls/f:webm"+videoSrc),
		vid("stream_bad_protocol", "st:smooth"),
		vid("stream_bad_rung", "st:hls:32"),
		vid("stream_too_many_rungs", "st:hls:1:2:3:4:5:6:7:8:9"),
		vid("segment_duration_bad", "sgd:1.5"),
		vid("segment_duration_long", "sgd:60"),
		vidPath("stream_signed", "signed", "/VSIG/st:hls:360"+videoSrc+"@m3u8"),

		// Rejected and unknown.
		vid("chained", "w:100/-/w:50"),
		vid("image_option_unknown", "bl:5"),
		vidPath("image_option_strict", "strict", "/unsafe/bl:5"+videoSrc),
		vid("preset_unknown", "pr:thumb"),

		// Signing: /video signs "/video" + the path.
		vidPath("signed", "signed", "/VSIG/rs:fit:320:0/vd:2"+videoSrc+"@webm"),
		vidPath("image_signature_rejected", "signed", "/SIG/rs:fit:320:0/vd:2"+videoSrc+"@webm"),
		vidPath("unsigned_on_signed", "signed", "/unsafe/vd:2"+videoSrc),
		vidPath("trusted", "signed", "/"+trustedSig+"/vd:2"+videoSrc),
	})
}

// sha256Hex is a well-formed hashsum digest.
const sha256Hex = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

// TestVideoOnlyPresets: only-presets mode admits no /video URL.
func TestVideoOnlyPresets(t *testing.T) {
	t.Parallel()
	s, err := preset.New(preset.Options{Definitions: map[string]string{"thumb": "rs:fit:100:100"}, Only: true})
	if err != nil {
		t.Fatal(err)
	}
	p, err := imgproxy.New(&imgproxy.Options{Presets: s})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.ParseVideo(&dialect.Input{Path: "/unsafe/thumb" + videoSrc})
	if errs.KindOf(err) != errs.KindForbidden {
		t.Fatalf("err %v, want forbidden", err)
	}
}

// TestVideoSignatureDomain: the same options signed as an image URL and as
// a video URL give different signatures, and each verifies only on its
// own endpoint.
func TestVideoSignatureDomain(t *testing.T) {
	t.Parallel()
	f := getFixture(t)
	p := f.parsers["signed"]
	rest := "/w:320" + videoSrc
	img, vidSig := f.expand(t, "/SIG"+rest), f.expand(t, "/VSIG"+rest)
	if img == vidSig {
		t.Fatal("image and video signatures are equal")
	}
	if res, err := p.ParseVideo(&dialect.Input{Path: vidSig}); err != nil || res.Signature != sign.Verified {
		t.Errorf("video signature on /video: %v %v", res, err)
	}
	if _, err := p.ParseVideo(&dialect.Input{Path: img}); errs.KindOf(err) != errs.KindForbidden && errs.KindOf(err) != errs.KindUnauthorized {
		t.Errorf("image signature on /video: %v", err)
	}
}
