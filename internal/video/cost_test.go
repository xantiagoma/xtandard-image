package video

import (
	"math"
	"testing"
)

func costInput(codec string, w, h int, fps, dur float64) *TranscodeInput {
	return &TranscodeInput{
		Duration: dur, Video: true, VideoCodec: codec, Width: w, Height: h, FPS: fps,
		Audio: true, AudioDecodable: true, SampleRate: 48000, Channels: 2,
	}
}

func videoPlan(codec Codec, w, h int, fps, dur float64) *TranscodePlan {
	return &TranscodePlan{Duration: dur, Video: &VideoStreamPlan{Codec: codec, Width: w, Height: h, FPS: fps}}
}

// TestEstimateCost checks the model term by term against its constants.
func TestEstimateCost(t *testing.T) {
	t.Parallel()
	in720 := costInput("h264", 1280, 720, 30, 10)
	px720 := 1280.0 * 720
	audio := &AudioStreamPlan{Codec: AudioAAC, SampleRate: 48000, Channels: 2}
	for _, tc := range []struct {
		name string
		in   *TranscodeInput
		p    *TranscodePlan
		want float64
	}{
		{
			"10 s 720p30 H.264 to 720p VP9",
			in720, videoPlan(CodecVP9, 1280, 720, 30, 10),
			baseMs + 10*30*px720*decodeNsPerPixel["h264"]/1e6 + 10*30*px720*encodeNsPerPixel[CodecVP9]/1e6,
		},
		{
			"with AAC stereo 48 kHz",
			in720, func() *TranscodePlan { p := videoPlan(CodecH264, 640, 360, 30, 10); p.Audio = audio; return p }(),
			baseMs + 10*30*px720*decodeNsPerPixel["h264"]/1e6 + 10*30*640*360*encodeNsPerPixel[CodecH264]/1e6 +
				audioBaseMs[AudioAAC] + 10*audioMsPerSecond[AudioAAC],
		},
		{
			"a cut decodes the pre-roll and the tail; the output frame rate drives the encode",
			in720, &TranscodePlan{Start: 4, End: 6, Duration: 2, Video: &VideoStreamPlan{Codec: CodecAV1, Width: 1280, Height: 720, FPS: 15}},
			baseMs + (2+prerollSec+segmentTailSec)*30*px720*decodeNsPerPixel["h264"]/1e6 + 2*15*px720*encodeNsPerPixel[CodecAV1]/1e6,
		},
		{
			"a stream segment is its window, plus pre-roll and tail",
			in720, &TranscodePlan{
				Start: 12, End: 18, Duration: 60, Fragment: &FragmentPlan{Discont: true},
				Video: &VideoStreamPlan{Codec: CodecH264, Width: 1280, Height: 720, FPS: 30},
			},
			baseMs + (6+prerollSec+segmentTailSec)*30*px720*decodeNsPerPixel["h264"]/1e6 + 6*30*px720*encodeNsPerPixel[CodecH264]/1e6,
		},
		{
			"audio only: no video decode; mono 24 kHz is a quarter of stereo 48 kHz",
			in720, &TranscodePlan{Duration: 10, Audio: &AudioStreamPlan{Codec: AudioOpus, SampleRate: 24000, Channels: 1}},
			baseMs + audioBaseMs[AudioOpus] + 10*audioMsPerSecond[AudioOpus]/4,
		},
		{
			"waveform",
			in720, &TranscodePlan{Duration: 10, Waveform: &WaveformPlan{Width: 1000, Height: 200}},
			baseMs + 10*waveformMsPerSec + 0.2*waveformMsPerMpx,
		},
		{
			"unknown codec, frame rate and duration",
			&TranscodeInput{Video: true, VideoCodec: "prores", Width: 1920, Height: 1080},
			&TranscodePlan{Video: &VideoStreamPlan{Codec: CodecH264, Width: 1920, Height: 1080}},
			baseMs + assumedDurationSec*assumedFPS*1920*1080*decodeDefault/1e6 + assumedDurationSec*assumedFPS*1920*1080*encodeNsPerPixel[CodecH264]/1e6,
		},
	} {
		got := EstimateCost(tc.in, tc.p)
		if math.Abs(got-tc.want) > 1e-6*tc.want {
			t.Errorf("%s: cost %.3f, want %.3f", tc.name, got, tc.want)
		}
	}
}

// TestEstimateCostOrder: the relations admission relies on.
func TestEstimateCostOrder(t *testing.T) {
	t.Parallel()
	in4k := costInput("h264", 3840, 2160, 30, 300)
	in := costInput("h264", 1280, 720, 30, 300)
	cost := func(in *TranscodeInput, c Codec, w, h int, dur float64) float64 {
		return EstimateCost(in, videoPlan(c, w, h, 30, dur))
	}
	hevc, av1 := cost(in, CodecHEVC, 1280, 720, 10), cost(in, CodecAV1, 1280, 720, 10)
	vp9, h264 := cost(in, CodecVP9, 1280, 720, 10), cost(in, CodecH264, 1280, 720, 10)
	if hevc <= av1 || av1 <= vp9 || av1 <= h264 {
		t.Errorf("encoder order (default settings, measured): hevc %.0f > av1 %.0f > vp9 %.0f, h264 %.0f", hevc, av1, vp9, h264)
	}
	big, small := cost(in4k, CodecAV1, 3840, 2160, 300), cost(in, CodecVP9, 640, 360, 10)
	if big < 1000*small {
		t.Errorf("5 min 4K AV1 %.0f is not much heavier than 10 s 360p VP9 %.0f", big, small)
	}
	extract := EstimateCost(in4k, &TranscodePlan{Duration: 2, Audio: &AudioStreamPlan{Codec: AudioAAC, SampleRate: 48000, Channels: 2}})
	if extract > 200 {
		t.Errorf("a 2 s audio extraction of a 4K video costs %.0f", extract)
	}
	if c := EstimateCost(&TranscodeInput{}, &TranscodePlan{Duration: 0.001}); c < 1 {
		t.Errorf("cost %.3f below 1", c)
	}
	if c := cost(in, CodecVP9, 640, 360, 20); c <= cost(in, CodecVP9, 640, 360, 10) {
		t.Error("a longer cut does not cost more")
	}
}
