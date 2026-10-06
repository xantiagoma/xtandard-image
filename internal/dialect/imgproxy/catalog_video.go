package imgproxy

import "github.com/xantiagoma/xtandard-image/internal/transform"

const (
	msVideo   = "M5 (WP-122)"
	msAudio   = "M5 (WP-123)"
	msOverlay = "M5 (WP-124)"
	msStream  = "M5 (WP-126)"
)

// videoFormatNames are the containers of /video URLs, in videoContainers
// (format option and extension).
func videoFormatNames() []string {
	return []string{"mp4", "webm", "m4a", "mp3", "opus", "ogg", "flac", "wav", "png", "webp", "m3u8", "mpd"}
}

func secondsArg(name, doc string) Arg {
	return numArg(name, 0, transform.MaxVideoTime.Seconds(), doc)
}

func videoDimArg(name, doc string) Arg {
	return intArg(name, 0, transform.MaxVideoDimension, doc)
}

func bitrateArg(name, doc string) Arg {
	return Arg{Name: name, Type: TypeString, Pattern: `^([0-9]+(\.[0-9]+)?[kKmM]?)?$`, Doc: doc}
}

// videoGravityForms are the gravity shapes of video crops after the
// leading arguments lead; the compass type is optional after lead (a crop
// without gravity), required alone (the gravity option).
func videoGravityForms(lead ...Arg) []Form {
	typ := enumArg("type", compassNames(), "compass gravity")
	if len(lead) > 0 {
		typ = typ.opt()
	}
	off := func(n string) Arg {
		return intArg(n, 0, transform.MaxVideoDimension, "offset in pixels towards the inside of the frame (moves crops)").opt()
	}
	return []Form{form(append(append([]Arg{}, lead...), typ, off("x_offset"), off("y_offset"))...)}
}

// VideoOptions returns the /video option catalogue (docs/URL-DIALECTS.md
// §3.8) in table order. Shared entries are the processing options of the
// same name.
func VideoOptions() []Option {
	video := func(o Option) Option {
		o.Scope = ScopeRequest
		if o.Milestone == "" {
			o.Milestone = msVideo
		}
		return o
	}
	dims := func() []Arg {
		return []Arg{
			videoDimArg("width", "output width in pixels; 0 keeps the aspect ratio").opt(),
			videoDimArg("height", "output height in pixels; 0 keeps the aspect ratio").opt(),
			boolArg("enlarge", "fit may enlarge"),
			boolArg("extend", "pad to the box with the background"),
		}
	}
	resizeTypes := []string{"fit", "fill", "fill-down", "force"}
	opts := []Option{
		video(Option{
			Name: "start", Aliases: []string{"vs"}, Forms: single(secondsArg("seconds", "start of the output in the source")),
			Syntax: "seconds", Default: "0", Doc: "Start the output at this time of the source (exact, from the keyframe before it).", Example: "1.5",
		}),
		video(Option{
			Name: "end", Aliases: []string{"ve"}, Forms: single(secondsArg("seconds", "end of the output in the source")),
			Syntax: "seconds", Default: "end of the source", Doc: "End the output at this time of the source (exclusive with duration).", Example: "4",
		}),
		video(Option{
			Name: "duration", Aliases: []string{"vd"}, Forms: single(secondsArg("seconds", "length of the output")),
			Syntax: "seconds", Default: "to the end", Doc: "Length of the output from start (exclusive with end).", Example: "3",
		}),
		video(Option{
			Name: "resize", Aliases: []string{"rs"},
			Forms:  []Form{form(append([]Arg{enumArg("type", resizeTypes, "resizing type").opt()}, dims()...)...)},
			Syntax: "type:w:h:enlarge:extend", Default: "fit:0:0:false:false",
			Doc:     "Resize like imgproxy: fit (scale-down, or contain with enlarge; extend pads), fill and fill-down (cover, cropped by gravity), force (stretch).",
			Example: "fill:640:360",
		}),
		video(Option{
			Name: "size", Aliases: []string{"s"}, Forms: []Form{form(dims()...)},
			Syntax: "w:h:enlarge:extend", Default: "0:0:false:false", Doc: "Resize without the type.", Example: "640:360:0:1",
		}),
		video(Option{
			Name: "resizing_type", Aliases: []string{"rt"}, Forms: single(enumArg("type", resizeTypes, "resizing type")),
			Syntax: "fit, fill, fill-down, force", Default: "fit", Doc: "Resizing type (auto is not available for video).", Example: "fill",
		}),
		video(Option{
			Name: "width", Aliases: []string{"w"}, Forms: single(videoDimArg("width", "pixels")),
			Syntax: "px", Default: "0", Doc: "Output width (rounded down to even).", Example: "640",
		}),
		video(Option{
			Name: "height", Aliases: []string{"h"}, Forms: single(videoDimArg("height", "pixels")),
			Syntax: "px", Default: "0", Doc: "Output height (rounded down to even).", Example: "360",
		}),
		video(Option{
			Name: "enlarge", Aliases: []string{"el"}, Forms: single(boolArg("enlarge", "fit may enlarge")),
			Syntax: "bool", Default: "false", Doc: "Let fit enlarge a smaller source.", Example: "1",
		}),
		video(Option{
			Name: "extend", Aliases: []string{"ex"}, Forms: single(boolArg("extend", "pad to the box")),
			Syntax: "bool", Default: "false", Doc: "Pad a fit to the whole box with the background colour.", Example: "1",
		}),
		video(Option{
			Name: "gravity", Aliases: []string{"g"}, Forms: videoGravityForms(),
			Syntax: "type:x:y", Default: "ce:0:0", Doc: "Anchor of fill crops, pads and crops (a compass point; the pixel offsets move crops).", Example: "soea:10:10",
		}),
		video(Option{
			Name: "crop", Aliases: []string{"c"},
			Forms:  videoGravityForms(videoDimArg("width", "pixels; 0 = full width"), videoDimArg("height", "pixels; 0 = full height").opt()),
			Syntax: "w:h:gravity", Default: "—", Doc: "Crop a rectangle (pixels) of the rotated frame before the resize.", Example: "640:360:nowe",
		}),
		video(Option{
			Name: "rotate", Aliases: []string{"rot"}, Forms: single(Arg{Name: "angle", Type: TypeInteger, MultipleOf: 90, Doc: "clockwise degrees, a multiple of 90"}),
			Syntax: "multiple of 90", Default: "0", Doc: "Rotate clockwise after the display matrix.", Example: "90",
		}),
		video(Option{
			Name: "flip", Aliases: []string{"fl"}, Forms: []Form{form(boolArg("horizontal", "mirror left-right"), boolArg("vertical", "mirror top-bottom"))},
			Syntax: "h:v", Default: "false:false", Doc: "Mirror after the rotation.", Example: "1:0",
		}),
		video(Option{
			Name: "background", Aliases: []string{"bg"}, Forms: single(colorArg("color", "hex colour").opt()),
			Syntax: "hex", Default: "000000", Doc: "Colour of the bars of extend.", Example: "ffffff",
		}),
		video(Option{
			Name: "fps", Forms: single(numArg("fps", 0, transform.MaxVideoFPS, "frames per second; 0 keeps the source's")),
			Syntax: "rate", Default: "source", Doc: "Output frame rate (capped by XI_VIDEO_TRANSFORM_MAX_FPS).", Example: "24",
		}),
		video(Option{
			Name: "format", Aliases: []string{"f", "ext"}, Forms: single(enumArg("format", videoFormatNames(), "output container")),
			Syntax: "mp4, webm, m4a, mp3, opus, ogg, flac, wav, png, webp, m3u8, mpd", Default: "mp4",
			Doc: "Output container (the source extension sets it too); png and webp are waveform pictures, m3u8 and mpd streams (WP-126).", Example: "webm",
		}),
		video(Option{
			Name: "video_codec", Aliases: []string{"vc"}, Forms: single(enumArg("codec", sortedKeys(videoCodecs), "video codec")),
			Syntax: "auto, h264, av1, vp9, hevc", Default: "auto", Doc: "Video codec (h264 and hevc need the gpl image).", Example: "av1",
		}),
		video(Option{
			Name: "audio_codec", Aliases: []string{"ac"}, Forms: single(enumArg("codec", sortedKeys(audioCodecs), "audio codec")),
			Syntax: "auto, aac, opus, mp3, flac, pcm", Default: "auto", Doc: "Audio codec (must suit the container).", Example: "aac",
		}),
		video(Option{
			Name: "quality", Aliases: []string{"q"}, Forms: single(intArg("quality", 0, 100, "1..100; 0 = default")),
			Syntax: "1..100", Default: "0", Doc: "Video quality, mapped onto the codec's CRF scale.", Example: "70",
		}),
		video(Option{
			Name: "crf", Forms: single(intArg("crf", 0, transform.MaxVideoCRF, "constant rate factor; 0 = default")),
			Syntax: "0..63", Default: "0", Doc: "Constant rate factor (wins over quality).", Example: "30",
		}),
		video(Option{
			Name: "video_bitrate", Aliases: []string{"vb"}, Forms: single(bitrateArg("bitrate", "bits per second, k or M suffix")),
			Syntax: "bit/s (k, M)", Default: "uncapped", Doc: "Cap the video bit rate (50k..200M).", Example: "2M",
		}),
		video(Option{
			Name: "keyframe_interval", Aliases: []string{"ki"}, Forms: single(numArg("seconds", 0, transform.MaxKeyframeInterval.Seconds(), "seconds between keyframes")),
			Syntax: "seconds", Default: "2", Doc: "Time between keyframes (GOP length, 0.1..60 s).", Example: "1",
		}),
		video(Option{
			Name: "mute", Aliases: []string{"mu"}, Forms: single(boolArg("mute", "drop the audio")), Milestone: msAudio,
			Syntax: "bool", Default: "false", Doc: "Drop the audio stream.", Example: "1",
		}),
		video(Option{
			Name: "audio_bitrate", Aliases: []string{"ab"}, Forms: single(bitrateArg("bitrate", "bits per second, k suffix")), Milestone: msAudio,
			Syntax: "bit/s (k)", Default: "codec default", Doc: "Audio bit rate (8k..512k; ignored by flac and wav).", Example: "128k",
		}),
		video(Option{
			Name: "sample_rate", Aliases: []string{"sr"}, Milestone: msAudio,
			Forms:  single(Arg{Name: "hz", Type: TypeInteger, Min: 0, Max: 96000, HasMin: true, HasMax: true, Doc: "8000, 11025, 12000, 16000, 22050, 24000, 32000, 44100, 48000, 88200 or 96000"}),
			Syntax: "Hz", Default: "source", Doc: "Output sample rate (the codec must support it: Opus 8/12/16/24/48 kHz).", Example: "44100",
		}),
		video(Option{
			Name: "audio_channels", Aliases: []string{"ach"}, Forms: single(intArg("channels", 0, 2, "1 mono, 2 stereo; 0 = source (at most 2)")), Milestone: msAudio,
			Syntax: "1 or 2", Default: "source", Doc: "Downmix to mono or stereo.", Example: "1",
		}),
		video(Option{
			Name: "volume", Aliases: []string{"vol"}, Milestone: msAudio,
			Forms:  single(Arg{Name: "gain", Type: TypeString, Pattern: `^([0-9]+(\.[0-9]+)?|[-+]?[0-9]+(\.[0-9]+)?[dD][bB])?$`, Doc: "a factor (0.5) or decibels (-6dB)"}),
			Syntax: "factor or NdB", Default: "1", Doc: "Change the audio level (-60 dB..+20 dB).", Example: "-6dB",
		}),
		video(Option{
			Name: "loudnorm", Aliases: []string{"ln"}, Milestone: msAudio,
			Forms: []Form{form(boolArg("enabled", "normalise the loudness"),
				numArg("integrated", transform.MinLoudnessIntegrated, transform.MaxLoudnessIntegrated, "integrated loudness target, LUFS").opt(),
				numArg("true_peak", transform.MinLoudnessTruePeak, transform.MaxLoudnessTruePeak, "true peak, dBTP").opt(),
				numArg("range", transform.MinLoudnessRange, transform.MaxLoudnessRange, "loudness range, LU").opt())},
			Syntax: "enabled:I:TP:LRA", Default: "false:-23:-1:7", Doc: "EBU R128 loudness normalisation (single-pass loudnorm).", Example: "1:-16:-1.5:11",
		}),
		video(Option{
			Name: "fade", Aliases: []string{"fd"}, Milestone: msAudio,
			Forms:  []Form{form(secondsArg("in", "fade-in seconds").opt(), secondsArg("out", "fade-out seconds").opt())},
			Syntax: "in:out", Default: "0:0", Doc: "Fade in from black and silence, and out to them (seconds).", Example: "1:2",
		}),
		video(Option{
			Name: "waveform", Aliases: []string{"wf"}, Milestone: msAudio,
			Forms: []Form{form(boolArg("enabled", "render a waveform picture"), colorArg("color", "wave colour").opt(),
				colorArg("background", "background colour; empty is transparent").opt(), boolArg("split_channels", "one band per channel"))},
			Syntax: "enabled:color:background:split", Default: "false:000000::false",
			Doc: "Render the audio as a width × height (default 1024×256) PNG or WebP picture.", Example: "1:2266cc:ffffff:0",
		}),
		video(Option{
			Name: "overlay_window", Aliases: []string{"ow"}, Milestone: msOverlay,
			Forms:  []Form{form(secondsArg("start", "first second of the overlay on the output").opt(), secondsArg("end", "end of the overlay; 0 = the end").opt())},
			Syntax: "start:end", Default: "0:0", Doc: "Show the watermark overlay (wm, wmu, wmt) from start to end seconds of the output.", Example: "1:4",
		}),
		video(Option{
			Name: "overlay_fade", Aliases: []string{"of"}, Milestone: msOverlay,
			Forms:  []Form{form(secondsArg("in", "fade-in seconds").opt(), secondsArg("out", "fade-out seconds").opt())},
			Syntax: "in:out", Default: "0:0", Doc: "Fade the watermark overlay in and out (seconds, within its window).", Example: "0.5:0.5",
		}),
		video(Option{
			Name: "stream", Aliases: []string{"st"}, Milestone: msStream,
			Forms: []Form{{
				Args: []Arg{enumArg("protocol", []string{"dash", "hls"}, "manifest: HLS (m3u8) or DASH (mpd)")},
				Rest: []Arg{intArg("height", transform.MinRendition, transform.MaxRendition, "a rendition by the short side of its frame").opt()},
			}},
			Syntax: "protocol:height…", Default: "none (a file)",
			Doc:     "Deliver an HLS or DASH stream (as the m3u8 or mpd extension does) with these renditions (at most 8; none: the configured ladder). Rungs above the source are dropped.",
			Example: "hls:360:720",
		}),
		video(Option{
			Name: "segment_duration", Aliases: []string{"sgd"}, Milestone: msStream,
			Forms:  single(intArg("seconds", 1, transform.MaxSegmentDuration.Seconds(), "whole seconds")),
			Syntax: "seconds", Default: "XI_VIDEO_TRANSFORM_SEGMENT_DURATION (6)", Doc: "Length of a stream's segments (1..30 s).", Example: "4",
		}),
		video(Option{
			Name: "timecode", Aliases: []string{"tc"}, Milestone: msOverlay,
			Forms: []Form{form(enumArg("format", []string{"hms", "seconds", "none"}, "HH:MM:SS.mmm, seconds, or none"),
				enumArg("position", compassNames(), "compass position").opt(),
				anyNum("x_offset", "offset in pixels").opt(), anyNum("y_offset", "offset in pixels").opt(),
				intArg("size", 0, transform.MaxTimecodeSize, "font size in pixels; 0 = default").opt(),
				colorArg("color", "text colour (white)").opt(), unitArg("opacity", "opacity (1)").opt())},
			Syntax: "format:position:x:y:size:color:opacity", Default: "none:sowe:0:0:0:ffffff:1",
			Doc: "Draw the output's running time (FFmpeg drawtext with a fixed expansion).", Example: "hms:sowe:8:8:20",
		}),
	}
	proc := ProcessingOptions()
	for i := range proc {
		o := proc[i]
		switch o.Name {
		case "watermark", "watermark_url", "watermark_text", "watermark_size", "watermark_rotate", "watermark_shadow",
			"cachebuster", "expires", "filename", "return_attachment", "hashsum", "bypass_cache", "cache_tags", "raw":
			o.Shared = true
			opts = append(opts, o)
		}
	}
	return opts
}
