package cloudflare_test

import "testing"

// cfDemo is the source of Cloudflare's own example
// (developers.cloudflare.com/stream/transform-videos/).
const cfDemo = "/https://pub-8613b7f94d6146408add8fefb52c52e8.r2.dev/aus-mobile-demo.mp4"

func media(o string) string { return "/" + o + cfDemo }

// TestMedia covers /cdn-cgi/media/ (Media Transformations): every mode and
// option with valid values, the documented ranges, and invalid values.
func TestMedia(t *testing.T) {
	t.Parallel()
	signed := func(path, query string) string {
		q, err := getFixture(t).xi.SignXIQuery(path, query)
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	runGolden(t, "media", []testCase{
		// Cloudflare's documented examples.
		{name: "doc_video", cfg: "media", path: "/cdn-cgi/media" + media("mode=video,time=5s,duration=5s,width=500,height=500,fit=crop,audio=false")},
		{name: "doc_frame", cfg: "media", path: media("mode=frame,time=3s,width=500,height=500,fit=cover,format=jpg")},
		{name: "doc_spritesheet", cfg: "media", path: media("mode=spritesheet,duration=10s,width=200,height=100,fit=cover")},
		{name: "doc_audio", cfg: "media", path: media("mode=audio,time=5s,duration=10s,format=m4a")},
		// video mode.
		{name: "video_defaults", cfg: "media", path: media("mode=video")},
		{name: "video_minutes", cfg: "media", path: media("mode=video,time=1.5m,duration=1m")},
		{name: "video_decimal_seconds", cfg: "media", path: media("mode=video,time=0.25s,duration=1.5s")},
		{name: "video_width_only", cfg: "media", path: media("mode=video,width=480")},
		{name: "video_scale_down", cfg: "media", path: media("mode=video,width=480,height=480,fit=scale-down")},
		{name: "video_cover_one_side", cfg: "media", path: media("mode=video,width=480,fit=cover")},
		{name: "video_contain", cfg: "media", path: media("mode=video,width=640,height=360,fit=contain,audio=true")},
		{name: "video_filename", cfg: "media", path: media("mode=video,filename=my-clip_01")},
		{name: "video_later_wins", cfg: "media", path: media("mode=frame,width=100,mode=video,width=200")},
		{name: "video_format_ignored", cfg: "media", path: media("mode=video,format=jpg")},
		{name: "video_format_strict", cfg: "media_strict", path: media("mode=video,format=jpg")},
		{name: "video_unknown", cfg: "media", path: media("mode=video,quality=80")},
		{name: "video_unknown_strict", cfg: "media_strict", path: media("mode=video,quality=80")},
		{name: "video_relative_source", cfg: "media", path: "/mode=video/videos/a.mp4"},
		{name: "video_encoded_source", cfg: "media", path: "/mode=video/https%3A%2F%2Fexample.com%2Fv.mp4%3Fx%3D1"},
		// audio mode.
		{name: "audio_default", cfg: "media", path: media("mode=audio")},
		{name: "audio_false", cfg: "media", path: media("mode=audio,audio=false")},
		{name: "audio_format_mp3", cfg: "media", path: media("mode=audio,format=mp3")},
		{name: "audio_width_ignored", cfg: "media", path: media("mode=audio,width=100")},
		// frame mode.
		{name: "frame_default", cfg: "media", path: media("mode=frame")},
		{name: "frame_png", cfg: "media", path: media("mode=frame,time=2m,format=png,height=240")},
		{name: "frame_scale_down", cfg: "media", path: media("mode=frame,width=320,height=320,fit=scale-down")},
		{name: "frame_format_bad", cfg: "media", path: media("mode=frame,format=webp")},
		{name: "frame_audio_ignored", cfg: "media", path: media("mode=frame,audio=false,duration=5s")},
		// spritesheet mode.
		{name: "sprite_default", cfg: "media", path: media("mode=spritesheet")},
		{name: "sprite_count", cfg: "media", path: media("mode=spritesheet,imageCount=12,duration=6s,width=160")},
		{name: "sprite_count_bad", cfg: "media", path: media("mode=spritesheet,imageCount=0")},
		{name: "sprite_count_huge", cfg: "media", path: media("mode=spritesheet,imageCount=101")},
		{name: "sprite_time", cfg: "media", path: media("mode=spritesheet,time=5s")},
		{name: "sprite_time_strict", cfg: "media_strict", path: media("mode=spritesheet,time=5s")},
		// Ranges and invalid values.
		{name: "mode_missing", cfg: "media", path: media("width=100")},
		{name: "mode_bad", cfg: "media", path: media("mode=gif")},
		{name: "time_over", cfg: "media", path: media("mode=video,time=11m")},
		{name: "time_max", cfg: "media", path: media("mode=video,time=10m")},
		{name: "time_no_unit", cfg: "media", path: media("mode=video,time=5")},
		{name: "time_negative", cfg: "media", path: media("mode=video,time=-1s")},
		{name: "time_ms", cfg: "media", path: media("mode=video,time=500ms")},
		{name: "duration_short", cfg: "media", path: media("mode=video,duration=0.5s")},
		{name: "duration_long", cfg: "media", path: media("mode=video,duration=61s")},
		{name: "width_small", cfg: "media", path: media("mode=video,width=9")},
		{name: "width_large", cfg: "media", path: media("mode=video,width=2001")},
		{name: "height_text", cfg: "media", path: media("mode=video,height=tall")},
		{name: "fit_pad", cfg: "media", path: media("mode=video,width=100,height=100,fit=pad")},
		{name: "audio_bad", cfg: "media", path: media("mode=video,audio=no")},
		{name: "filename_bad", cfg: "media", path: media("mode=video,filename=a.mp4")},
		{name: "item_no_value", cfg: "media", path: media("mode=video,width")},
		{name: "missing_source", cfg: "media", path: "/mode=video"},
		{name: "missing_options", cfg: "media", path: "//v.mp4"},
		// Through the /cdn-cgi/image/ parser (host-bound or custom prefix).
		{name: "via_image_parser", path: "/cdn-cgi/media" + media("mode=video,width=320")},
		{name: "via_image_parser_strict", cfg: "strict", path: "/cdn-cgi/media" + media("mode=frame,time=1s")},
		// xi_sig.
		{name: "xi_signed", cfg: "media_xi", path: "/mode=video/v.mp4", query: signed("/mode=video/v.mp4", "")},
		{name: "xi_signed_native_prefix", cfg: "media_xi", path: "/cdn-cgi/media/mode=video/v.mp4", query: signed("/mode=video/v.mp4", "")},
		{name: "xi_tampered", cfg: "media_xi", path: "/mode=audio/v.mp4", query: signed("/mode=video/v.mp4", "")},
		{name: "xi_unsigned", cfg: "media_xi", path: "/mode=video/v.mp4"},
	})
}
