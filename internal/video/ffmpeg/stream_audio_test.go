//go:build ffmpeg && cgo

package ffmpeg_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/media/fmp4"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// chunkFixture is the stream fixture's video with the same two tones at
// 44.1 kHz in AAC (MP4: sample-exact audio timestamps).
const chunkFixture = "stream-vp9-aac44k.mp4"

// audioLayout lays a stream out with audio chunks of chunk.
func audioLayout(t *testing.T, src *vips.Buffer, op *transform.VideoOp, chunk time.Duration) (*video.StreamLayout, video.TranscodeInput) {
	t.Helper()
	caps := ffmpeg.TranscodeCaps()
	out, err := caps.ResolveStream(op)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := ffmpeg.OpenTranscode(t.Context(), src, video.Limits{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	in := tx.Input()
	l, err := video.PlanStream(&in, op, out, caps, txSettings(), &video.StreamSettings{AudioChunk: chunk})
	if err != nil {
		t.Fatal(err)
	}
	return l, in
}

// audioChunk transcodes chunk c of l's audio.
func audioChunk(t *testing.T, src *vips.Buffer, op *transform.VideoOp, l *video.StreamLayout, c int) *fmp4.File {
	t.Helper()
	tx, err := ffmpeg.OpenTranscode(t.Context(), src, video.Limits{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	in := tx.Input()
	p, err := video.PlanAudioChunk(&in, op, l, c, txSettings())
	if err != nil {
		t.Fatal(err)
	}
	o, err := tx.Run(t.Context(), p)
	if err != nil {
		t.Fatalf("chunk %d: %v", c, err)
	}
	return mustParse(t, o)
}

// decodePCM decodes the AAC track data into interleaved 48 kHz stereo
// samples with the ffmpeg program (no edit list: the encoder's priming
// is the first 1024 samples).
func decodePCM(t *testing.T, data []byte) []int16 {
	t.Helper()
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	f := filepath.Join(t.TempDir(), "audio.mp4")
	if err := os.WriteFile(f, data, 0o600); err != nil {
		t.Fatal(err)
	}
	wav := filepath.Join(t.TempDir(), "audio.wav")
	if out, err := exec.CommandContext(t.Context(), bin, "-nostdin", "-v", "error", "-i", f, "-c:a", "pcm_s16le", wav).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	b, err := os.ReadFile(wav)
	if err != nil {
		t.Fatal(err)
	}
	// The data chunk of the RIFF file.
	for off := 12; off+8 <= len(b); {
		id, size := string(b[off:off+4]), int(binary.LittleEndian.Uint32(b[off+4:]))
		if id == "data" {
			data := b[off+8 : min(off+8+size, len(b))]
			pcm := make([]int16, len(data)/2)
			if err := binary.Read(bytes.NewReader(data), binary.LittleEndian, pcm); err != nil {
				t.Fatal(err)
			}
			return pcm
		}
		off += 8 + size + size&1
	}
	t.Fatal("no data chunk in the decoded WAV")
	return nil
}

// TestStreamAudioChunks: a stream's audio encoded in chunks of two 1 s
// segments (WP-131) is the track encoded at once, but for the codec's
// noise: every fragment starts and lasts exactly as the single encode's
// (the same packet grid), the init segments are identical, the joined
// chunks decode to the same number of samples, and at the joins (2, 4
// and 6 s) neither the difference from the single encode nor the tones'
// second difference (a click detector) is larger than elsewhere.
func TestStreamAudioChunks(t *testing.T) {
	t.Parallel()
	src := source(t, chunkFixture)
	op := &transform.VideoOp{Stream: &transform.VideoStream{SegmentDuration: time.Second}}
	l, in := audioLayout(t, src, op, 2*time.Second)
	whole, _ := audioLayout(t, src, op, 0)
	if !in.AudioExact() || in.SampleRate != 44100 || l.Segments != 7 || l.Audio.ChunkSegments != 2 || l.AudioChunks() != 4 ||
		whole.AudioChunks() != 1 {
		t.Fatalf("input %+v, layout %+v", in, l.Audio)
	}
	ref := audioChunk(t, src, op, whole, 0)
	if len(ref.Fragments) != l.Segments {
		t.Fatalf("single encode: %d fragments", len(ref.Fragments))
	}
	var joined bytes.Buffer
	joined.Write(ref.Init)
	n := 0
	for c := range l.AudioChunks() {
		f := audioChunk(t, src, op, l, c)
		first, end := l.AudioChunkSegments(c)
		if !bytes.Equal(f.Init, ref.Init) || len(f.Fragments) != end-first {
			t.Fatalf("chunk %d: %d fragments, init equal %v", c, len(f.Fragments), bytes.Equal(f.Init, ref.Init))
		}
		for i, fr := range f.Fragments {
			want := ref.Fragments[first+i]
			if fr.DecodeTime != want.DecodeTime || fr.Duration != want.Duration || fr.Samples != want.Samples {
				t.Fatalf("segment %d: fragment at %d lasting %d (%d packets), single encode %d lasting %d (%d)",
					first+i, fr.DecodeTime, fr.Duration, fr.Samples, want.DecodeTime, want.Duration, want.Samples)
			}
			joined.Write(fr.Data)
			n++
		}
	}
	if n != l.Segments {
		t.Fatalf("%d segments from the chunks", n)
	}
	var single bytes.Buffer
	single.Write(ref.Init)
	for _, fr := range ref.Fragments {
		single.Write(fr.Data)
	}
	got, want := decodePCM(t, joined.Bytes()), decodePCM(t, single.Bytes())
	if len(got) != len(want) || len(got) < 2*48000*6 {
		t.Fatalf("%d samples, the single encode %d", len(got), len(want))
	}
	// Frames (sample pairs) around a join at stream time s, and away from
	// every join.
	const priming, win = 1024, 1024
	near := func(i int) bool {
		for _, s := range []int{2, 4, 6} {
			if j := priming + s*48000; i >= j-win && i < j+win {
				return true
			}
		}
		return false
	}
	var errJoin, errElse, sigElse float64
	var nJoin, nElse int
	var d2Join, d2Else float64
	frames := len(got) / 2
	for i := priming + 4800; i < frames-priming; i++ { // past the tones' onset
		for ch := range 2 {
			g, w := float64(got[2*i+ch]), float64(want[2*i+ch])
			d := (g - w) * (g - w)
			d2 := math.Abs(g - 2*float64(got[2*(i-1)+ch]) + float64(got[2*(i+1)+ch]))
			if near(i) {
				errJoin += d
				nJoin++
				d2Join = max(d2Join, d2)
			} else {
				errElse += d
				sigElse += w * w
				nElse++
				d2Else = max(d2Else, d2)
			}
		}
	}
	rmsJoin, rmsElse := math.Sqrt(errJoin/float64(nJoin)), math.Sqrt(errElse/float64(nElse))
	snr := 10 * math.Log10(sigElse/errElse)
	t.Logf("difference from the single encode: rms %.2f at the joins, %.2f elsewhere (%.1f dB below the signal); "+
		"largest second difference %.0f at the joins, %.0f elsewhere", rmsJoin, rmsElse, snr, d2Join, d2Else)
	if rmsJoin > 2*rmsElse+2 {
		t.Errorf("the joins differ from the single encode more than the rest: rms %.2f vs %.2f", rmsJoin, rmsElse)
	}
	if d2Join > 1.5*d2Else {
		t.Errorf("a click at a join: second difference %.0f, %.0f elsewhere", d2Join, d2Else)
	}
	if snr < 20 {
		t.Errorf("the chunks are %.1f dB from the single encode", snr)
	}
}
