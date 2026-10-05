//go:build ffmpeg && cgo

package ffmpeg_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// tones is the 4 s stereo 44.1 kHz MP3 of testdata/fixtures/audio.
const tones = "../audio/tones-stereo.mp3"

// audioProbe is ffprobe's view of an audio file, decoded to the end.
type audioProbe struct {
	Streams []struct {
		CodecType  string `json:"codec_type"`
		CodecName  string `json:"codec_name"`
		SampleRate string `json:"sample_rate"`
		Channels   int    `json:"channels"`
		NbFrames   string `json:"nb_read_frames"`
	} `json:"streams"`
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
	} `json:"format"`
}

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(f, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func probeAudio(t *testing.T, data []byte) audioProbe {
	t.Helper()
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("no ffprobe")
	}
	cmd := exec.CommandContext(t.Context(), bin, "-v", "error", "-count_frames", "-show_entries",
		"stream=codec_type,codec_name,sample_rate,channels,nb_read_frames:format=format_name,duration", "-of", "json", writeTemp(t, data))
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	var p audioProbe
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// loudness is the integrated loudness (LUFS) of data, measured by the
// image's ffmpeg with the loudnorm filter's EBU R128 meter.
func loudness(t *testing.T, data []byte) float64 {
	t.Helper()
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	cmd := exec.CommandContext(t.Context(), bin, "-hide_banner", "-nostats", "-i", writeTemp(t, data),
		"-af", "loudnorm=print_format=json", "-f", "null", "-")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg loudnorm: %v\n%s", err, b)
	}
	i := bytes.LastIndexByte(b, '{')
	if i < 0 {
		t.Fatalf("no loudnorm report in %s", b)
	}
	var r struct {
		InputI string `json:"input_i"`
	}
	if err := json.Unmarshal(b[i:bytes.LastIndexByte(b, '}')+1], &r); err != nil {
		t.Fatal(err)
	}
	v, err := strconv.ParseFloat(r.InputI, 64)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestTranscodeAudioOutputs: every audio-only output plays to the end (the
// probe decodes every frame) with the requested codec, rate and channels,
// from an audio source and from a video's audio.
func TestTranscodeAudioOutputs(t *testing.T) {
	t.Parallel()
	caps := ffmpeg.TranscodeCaps()
	if !caps.AudioFilters || !caps.FLAC || !caps.PCM || !caps.MuxFLAC || !caps.MuxWAV {
		t.Fatalf("full image audio caps %+v", caps)
	}
	for _, tc := range []struct {
		name, file string
		op         transform.VideoOp
		format     string
		codec      string
		rate, ch   int
		duration   float64
	}{
		{
			"mp3 to m4a mono 22.05 kHz", tones,
			transform.VideoOp{Container: transform.VideoContainerM4A, SampleRate: 22050, Channels: 1},
			"mov,mp4,m4a,3gp,3g2,mj2", "aac", 22050, 1, 4.05,
		},
		{
			"mp3 to opus, cut", tones,
			transform.VideoOp{Container: transform.VideoContainerOpus, Start: time.Second, Duration: 2 * time.Second},
			"ogg", "opus", 48000, 2, 2,
		},
		{
			"mp3 to ogg 24 kHz mono", tones,
			transform.VideoOp{Container: transform.VideoContainerOgg, SampleRate: 24000, Channels: 1},
			"ogg", "opus", 48000, 1, 4.05,
		}, // Opus streams always declare 48 kHz
		{
			"mp3 to flac 48 kHz", tones,
			transform.VideoOp{Container: transform.VideoContainerFLAC, SampleRate: 48000},
			"flac", "flac", 48000, 2, 4.05,
		},
		{
			"mp3 to wav 8 kHz", tones,
			transform.VideoOp{Container: transform.VideoContainerWAV, SampleRate: 8000, End: 3 * time.Second},
			"wav", "pcm_s16le", 8000, 2, 3,
		},
		{
			"mp3 to mp3 32 kHz", tones,
			transform.VideoOp{Container: transform.VideoContainerMP3, SampleRate: 32000, AudioBitrate: 96_000},
			"mp3", "mp3", 32000, 2, 4.05,
		},
		{
			"video audio to stereo m4a", "bbb-h264-aac.mp4",
			transform.VideoOp{Container: transform.VideoContainerM4A, Channels: 2, SampleRate: 44100},
			"mov,mp4,m4a,3gp,3g2,mj2", "aac", 44100, 2, 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := transcode(t.Context(), source(t, tc.file), &tc.op, txSettings(), video.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			defer out.Release()
			p := probeAudio(t, out.Bytes())
			if p.Format.FormatName != tc.format || len(p.Streams) != 1 {
				t.Fatalf("%s with %+v, want %s with one audio stream", p.Format.FormatName, p.Streams, tc.format)
			}
			s := p.Streams[0]
			if s.CodecType != "audio" || s.CodecName != tc.codec || s.SampleRate != strconv.Itoa(tc.rate) || s.Channels != tc.ch {
				t.Errorf("stream %+v, want %s %d Hz %d channels", s, tc.codec, tc.rate, tc.ch)
			}
			if n, _ := strconv.Atoi(s.NbFrames); n == 0 {
				t.Errorf("no frame decoded")
			}
			if d, _ := strconv.ParseFloat(p.Format.Duration, 64); math.Abs(d-tc.duration) > 0.12 {
				t.Errorf("duration %.3f s, want %.3f s", d, tc.duration)
			}
		})
	}
}

// TestTranscodeAudioSources: the outputs of the lossless and Ogg muxers
// are themselves accepted as sources (the audio demuxer whitelist).
func TestTranscodeAudioSources(t *testing.T) {
	t.Parallel()
	for _, c := range []transform.VideoContainer{transform.VideoContainerWAV, transform.VideoContainerFLAC, transform.VideoContainerOgg} {
		t.Run(c.String(), func(t *testing.T) {
			t.Parallel()
			first, err := transcode(t.Context(), source(t, tones), &transform.VideoOp{Container: c, Duration: time.Second}, txSettings(), video.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			defer first.Release()
			buf, err := vips.NewBuffer(len(first.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			defer buf.Release()
			copy(buf.Bytes(), first.Bytes())
			out, err := transcode(t.Context(), buf, &transform.VideoOp{Container: transform.VideoContainerMP3}, txSettings(), video.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			defer out.Release()
			if p := probeAudio(t, out.Bytes()); len(p.Streams) != 1 || p.Streams[0].CodecName != "mp3" {
				t.Errorf("re-encoded %+v", p.Streams)
			}
		})
	}
}

// TestTranscodeLoudness: loudnorm brings the integrated loudness within
// ±1 LU of the target; volume scales it by its gain.
func TestTranscodeLoudness(t *testing.T) {
	t.Parallel()
	in := loudness(t, mustRead(t, tones))
	for _, tc := range []struct {
		name string
		op   transform.VideoOp
		want float64
	}{
		{"EBU R128 -23", transform.VideoOp{Loudnorm: new(transform.DefaultLoudnorm())}, -23},
		{"streaming -16", transform.VideoOp{Loudnorm: &transform.VideoLoudnorm{Integrated: -16, TruePeak: -1.5, Range: 11}}, -16},
		{"volume 0.5", transform.VideoOp{Volume: 0.5}, in - 6.02},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			op := tc.op
			op.Container = transform.VideoContainerFLAC
			out, err := transcode(t.Context(), source(t, tones), &op, txSettings(), video.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			defer out.Release()
			if got := loudness(t, out.Bytes()); math.Abs(got-tc.want) > 1 {
				t.Errorf("integrated loudness %.2f LUFS, want %.2f ±1 (source %.2f)", got, tc.want, in)
			}
		})
	}
}

// TestTranscodeFades: the audio fades in from silence and out to it.
func TestTranscodeFades(t *testing.T) {
	t.Parallel()
	op := &transform.VideoOp{
		Container: transform.VideoContainerWAV, Channels: 1, SampleRate: 8000,
		FadeIn: time.Second, FadeOut: time.Second,
	}
	out, err := transcode(t.Context(), source(t, tones), op, txSettings(), video.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer out.Release()
	pcm := wavSamples(t, out.Bytes())
	rms := func(from, to float64) float64 {
		a, b := int(from*8000), min(int(to*8000), len(pcm))
		var sum float64
		for _, s := range pcm[a:b] {
			sum += float64(s) * float64(s)
		}
		return math.Sqrt(sum / float64(b-a))
	}
	start, middle, end := rms(0, 0.1), rms(1.2, 1.8), rms(3.95, 4.05)
	if start > middle/5 || end > middle/5 {
		t.Errorf("RMS start %.0f, middle %.0f, end %.0f: not faded", start, middle, end)
	}
}

// wavSamples returns the 16-bit samples of a WAV file's data chunk.
func wavSamples(t *testing.T, b []byte) []int16 {
	t.Helper()
	for i := 12; i+8 <= len(b); {
		id, n := string(b[i:i+4]), int(binary.LittleEndian.Uint32(b[i+4:i+8]))
		if id == "data" {
			data := b[i+8 : min(i+8+n, len(b))]
			out := make([]int16, len(data)/2)
			if _, err := binary.Decode(data[:2*len(out)], binary.LittleEndian, out); err != nil {
				t.Fatal(err)
			}
			return out
		}
		i += 8 + n + n&1
	}
	t.Fatal("no data chunk")
	return nil
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtures, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestTranscodeWaveform: a waveform is width × height RGBA pixels whose
// wave is three times taller between 2 s and 3 s, compared perceptually
// with its golden; a source without audio is refused.
func TestTranscodeWaveform(t *testing.T) {
	t.Parallel()
	op := &transform.VideoOp{
		Container: transform.VideoContainerPNG, Width: 400, Height: 100,
		Waveform: &transform.VideoWaveform{Color: transform.Color{R: 0x22, G: 0x66, B: 0xcc}},
	}
	caps := ffmpeg.TranscodeCaps()
	out, err := caps.Resolve(op)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := ffmpeg.OpenTranscode(t.Context(), source(t, tones), video.Limits{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	in := tr.Input()
	p, err := video.PlanTranscode(&in, op, out, caps, txSettings())
	if err != nil {
		t.Fatal(err)
	}
	res, err := tr.Run(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Release()
	if len(res.Bytes()) != 400*100*4 {
		t.Fatalf("%d bytes, want 400x100 RGBA", len(res.Bytes()))
	}
	img := &image.NRGBA{Pix: append([]byte(nil), res.Bytes()...), Stride: 400 * 4, Rect: image.Rect(0, 0, 400, 100)}
	height := func(x int) int {
		n := 0
		for y := range 100 {
			if img.Pix[y*img.Stride+x*4+3] != 0 {
				n++
			}
		}
		return n
	}
	quiet, loud := height(50), height(250) // 0.5 s and 2.5 s
	if quiet == 0 || loud < 2*quiet {
		t.Errorf("wave height %d px at 0.5 s and %d px at 2.5 s, want the second about three times the first", quiet, loud)
	}
	golden.Compare(t, "video/waveform-tones", img, golden.Options{})

	noAudio := &transform.VideoOp{Container: transform.VideoContainerPNG, Waveform: &transform.VideoWaveform{}}
	if _, err := transcode(t.Context(), source(t, "clip-vp9.webm"), noAudio, txSettings(), video.Limits{}); errs.KindOf(err) != errs.KindUnsupported {
		t.Errorf("waveform of a silent video: %v, want unsupported", err)
	}
}

// TestTranscodeOptionsVideo: gravity crops, the keyframe interval and
// video fades on a real encode.
func TestTranscodeOptionsVideo(t *testing.T) {
	t.Parallel()
	op := &transform.VideoOp{
		Container: transform.VideoContainerWebM, Width: 160, Height: 160, Fit: transform.VideoFitCover,
		Gravity: transform.VideoGravityEast, KeyframeInterval: 200 * time.Millisecond,
		FadeIn: 500 * time.Millisecond, FadeOut: 500 * time.Millisecond, Mute: true,
	}
	out, err := transcode(t.Context(), source(t, "clip-vp8.webm"), op, txSettings(), video.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer out.Release()
	p := ffprobe(t, out.Bytes())
	if len(p.Streams) != 1 || p.Streams[0].Width != 160 || p.Streams[0].Height != 160 {
		t.Fatalf("streams %+v", p.Streams)
	}
	if n := keyframes(t, out.Bytes()); n < 4 {
		t.Errorf("%d keyframes in 1 s at a 0.2 s interval", n)
	}
}

// keyframes counts the video keyframes of data with ffprobe.
func keyframes(t *testing.T, data []byte) int {
	t.Helper()
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("no ffprobe")
	}
	b, err := exec.CommandContext(t.Context(), bin, "-v", "error", "-select_streams", "v", "-show_entries",
		"packet=flags", "-of", "csv=p=0", writeTemp(t, data)).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	return bytes.Count(b, []byte("K"))
}
