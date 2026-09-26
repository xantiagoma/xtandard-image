//go:build ffmpeg && cgo

package ffmpeg_test

import (
	"bytes"
	"context"
	"image"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/testutil/ssim"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// rgbImage copies a frame into an image for SSIM.
func rgbImage(f *ffmpeg.Frame) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, f.Width, f.Height))
	src := f.Pixels.Bytes()
	for i := range f.Width * f.Height {
		copy(img.Pix[i*4:i*4+3], src[i*f.Bands:i*f.Bands+3])
		img.Pix[i*4+3] = 255
	}
	return img
}

// TestSessionFrames: the frames a session decodes at several timestamps,
// forward without seeking where it can, are the frames Thumbnail decodes
// one at a time (seeking each), for every codec.
func TestSessionFrames(t *testing.T) {
	t.Parallel()
	for _, file := range []string{"clip-vp9.webm", "clip-av1.mp4", "bbb-h264-aac.mp4", "clip-vp8.webm", "clip-raw.avi"} {
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			src := source(t, file)
			s, err := ffmpeg.Open(t.Context(), src, video.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if !s.Reads(src) {
				t.Fatal("the session does not read its source")
			}
			times := []float64{0, 0.1, 0.25, 0.3, 0.7, 0.9, 5}
			frames, err := s.Frames(t.Context(), &video.FramesRequest{Times: times, Width: 160, Height: 90})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				for _, f := range frames {
					f.Release()
				}
			}()
			if len(frames) != len(times) {
				t.Fatalf("%d frames for %d timestamps", len(frames), len(times))
			}
			for i, at := range times {
				one, err := ffmpeg.Thumbnail(t.Context(), src, &video.FrameRequest{Second: &at, Width: 160, Height: 90})
				if err != nil {
					t.Fatal(err)
				}
				if one.Time != frames[i].Time {
					t.Errorf("t=%.2f: forward decode at %.3f s, seek at %.3f s", at, frames[i].Time, one.Time)
				}
				r, err := ssim.Compare(rgbImage(one), rgbImage(frames[i]))
				one.Release()
				if err != nil {
					t.Fatal(err)
				}
				if r.DSSIM > 0.001 {
					t.Errorf("t=%.2f: forward and seeking decodes differ, DSSIM %.4f", at, r.DSSIM)
				}
			}
		})
	}
}

// TestSessionReuse: one session answers several calls with fresh contexts,
// a cancelled call does not poison the next, limits are checked per call,
// and a closed session refuses calls.
func TestSessionReuse(t *testing.T) {
	t.Parallel()
	src := source(t, "clip-vp9.webm")
	s, err := ffmpeg.Open(t.Context(), src, video.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if m := s.Meta(); m.Codec != "vp9" || m.DisplayWidth != 1280 {
		t.Fatalf("meta %+v", m)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Thumbnail(ctx, &video.FrameRequest{Second: new(1.0)}); errs.KindOf(err) != errs.KindCanceled {
		t.Fatalf("canceled call: %v", err)
	}
	f, err := s.Thumbnail(t.Context(), &video.FrameRequest{Second: new(1.0), Width: 64, Height: 36})
	if err != nil {
		t.Fatalf("after a canceled call: %v", err)
	}
	f.Release()
	if _, err := s.Frames(t.Context(), &video.FramesRequest{Times: []float64{0}, Limits: video.Limits{MaxPixels: 1000}}); errs.KindOf(err) != errs.KindLimitExceeded {
		t.Fatalf("limits: %v", err)
	}
	other := source(t, "clip-vp9.webm")
	if s.Reads(other) {
		t.Error("Reads is true for a copy of the source")
	}
	s.Close()
	s.Close()
	if _, err := s.Thumbnail(t.Context(), &video.FrameRequest{Second: new(1.0)}); errs.KindOf(err) != errs.KindInternal {
		t.Fatalf("closed session: %v", err)
	}
	if n := ffmpeg.ReadStats().Handles; n < 0 {
		t.Fatalf("handles %d", n)
	}
}

// encodeFrames encodes n solid-colour frames of w×h with p.
func encodeFrames(tb testing.TB, p *video.EncodeParams, n int) (*vips.Buffer, error) {
	tb.Helper()
	enc, err := ffmpeg.NewEncoder(tb.Context(), p)
	if err != nil {
		return nil, err
	}
	defer enc.Close()
	pix, err := vips.NewBuffer(p.Width * p.Height * p.Bands)
	if err != nil {
		tb.Fatal(err)
	}
	defer pix.Release()
	for i := range n {
		b := pix.Bytes()
		for j := range b {
			b[j] = byte((j*7 + i*40) & 0xff)
		}
		if err := enc.Frame(tb.Context(), b, int64(i*100), 100); err != nil {
			return nil, err
		}
	}
	return enc.Finish(tb.Context())
}

// TestEncoder: every encoder this build has writes a file with the right
// container signature; a missing one is Unsupported; bad parameters and
// out-of-order timestamps are refused.
func TestEncoder(t *testing.T) {
	t.Parallel()
	set := ffmpeg.Encoders()
	if !set.VP9 || !set.AV1 {
		t.Fatalf("encoders %+v: the full image has libvpx-vp9 and libaom-av1", set)
	}
	for _, c := range []video.Codec{video.CodecVP9, video.CodecAV1, video.CodecH264, video.CodecHEVC} {
		t.Run(c.String(), func(t *testing.T) {
			t.Parallel()
			container := video.ContainerMP4
			if c == video.CodecVP9 {
				container = video.ContainerWebM
			}
			p := &video.EncodeParams{
				Codec: c, Container: container, Width: 64, Height: 48, Bands: 3,
				CRF: video.DefaultCRF(c), Speed: video.DefaultSpeed, Preset: video.DefaultPreset, FPS: 10,
			}
			out, err := encodeFrames(t, p, 5)
			if !set.Has(c) {
				if errs.KindOf(err) != errs.KindUnsupported {
					t.Fatalf("missing encoder: %v, want unsupported", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer out.Release()
			data := out.Bytes()
			switch container {
			case video.ContainerWebM:
				if !bytes.HasPrefix(data, []byte{0x1a, 0x45, 0xdf, 0xa3}) {
					t.Errorf("not EBML: % x", data[:8])
				}
			default:
				if !bytes.Equal(data[4:8], []byte("ftyp")) {
					t.Errorf("not ISOBMFF: % x", data[:8])
				}
			}
		})
	}
	bad := &video.EncodeParams{Codec: video.CodecVP9, Container: video.ContainerWebM, Width: 63, Height: 48, Bands: 3}
	if _, err := ffmpeg.NewEncoder(t.Context(), bad); errs.KindOf(err) != errs.KindInternal {
		t.Errorf("odd width: %v", err)
	}
	enc, err := ffmpeg.NewEncoder(t.Context(), &video.EncodeParams{
		Codec: video.CodecVP9, Container: video.ContainerWebM,
		Width: 16, Height: 16, Bands: 4, Alpha: true, CRF: 30, Speed: 8, FPS: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(enc.Close)
	pix := make16(t)
	t.Cleanup(pix.Release)
	if err := enc.Frame(t.Context(), pix.Bytes(), 100, 100); err != nil {
		t.Fatal(err)
	}
	if err := enc.Frame(t.Context(), pix.Bytes(), 100, 100); errs.KindOf(err) != errs.KindInternal {
		t.Errorf("repeated timestamp: %v", err)
	}
	if err := enc.Frame(t.Context(), pix.Bytes()[:10], 200, 100); errs.KindOf(err) != errs.KindInternal {
		t.Errorf("short frame: %v", err)
	}
}

func make16(tb testing.TB) *vips.Buffer {
	tb.Helper()
	b, err := vips.NewBuffer(16 * 16 * 4)
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

// TestEncoderLimitAndCancel: the output byte limit stops the muxer, and a
// done context stops a frame.
func TestEncoderLimitAndCancel(t *testing.T) {
	t.Parallel()
	p := &video.EncodeParams{
		Codec: video.CodecVP9, Container: video.ContainerWebM, Width: 256, Height: 256,
		Bands: 3, CRF: 4, Speed: 8, FPS: 10, MaxBytes: 2048,
	}
	if _, err := encodeFrames(t, p, 10); errs.KindOf(err) != errs.KindLimitExceeded {
		t.Errorf("byte limit: %v, want limit exceeded", err)
	}
	p.MaxBytes = 0
	enc, err := ffmpeg.NewEncoder(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	pix, err := vips.NewBuffer(256 * 256 * 3)
	if err != nil {
		t.Fatal(err)
	}
	defer pix.Release()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := enc.Frame(ctx, pix.Bytes(), 0, 100); errs.KindOf(err) != errs.KindCanceled {
		t.Errorf("canceled frame: %v", err)
	}
	if _, err := enc.Finish(t.Context()); errs.KindOf(err) != errs.KindInternal {
		t.Errorf("finish without frames: %v", err)
	}
}

// TestEncoderNoLeak: 200 encodes (VP9 and AV1) leave no encoder open, no
// libvips-tracked byte behind, and glibc's heap does not grow.
func TestEncoderNoLeak(t *testing.T) {
	run := func(i int) {
		c, k := video.CodecVP9, video.ContainerWebM
		if i%2 == 1 {
			c, k = video.CodecAV1, video.ContainerMP4
		}
		out, err := encodeFrames(t, &video.EncodeParams{
			Codec: c, Container: k, Width: 96, Height: 72, Bands: 3,
			CRF: 36, Speed: 8, FPS: 10,
		}, 6)
		if err != nil {
			t.Fatal(err)
		}
		out.Release()
	}
	for i := range 20 {
		run(i)
	}
	mem, heap := vips.ReadMemStats().Mem, ffmpeg.ReadStats().HeapInUse
	defer func() { t.Logf("glibc heap in use %d → %d bytes", heap, ffmpeg.ReadStats().HeapInUse) }()
	for i := range 200 {
		run(i)
	}
	if s := ffmpeg.ReadStats(); s.Encoders != 0 {
		t.Errorf("%d encoders open after 200 encodes", s.Encoders)
	}
	if m := vips.ReadMemStats().Mem; m != mem {
		t.Errorf("libvips tracked memory %d → %d bytes after 200 encodes", mem, m)
	}
	if h := ffmpeg.ReadStats().HeapInUse; heap > 0 && h-heap > 1<<20 {
		t.Errorf("glibc heap in use grew by %d bytes over 200 encodes", h-heap)
	}
}

// BenchmarkFrames measures ten frames of a 720p stream spread over its
// duration at 320×180 on one handle (Session.Frames, forward decoding
// where it can) against ten Thumbnail calls (open, probe and seek each).
func BenchmarkFrames(b *testing.B) {
	times := []float64{0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9}
	for _, fx := range benchFixtures {
		src := source(b, fx.file)
		b.Run(fx.name+"/session", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				s, err := ffmpeg.Open(b.Context(), src, video.Limits{})
				if err != nil {
					b.Fatal(err)
				}
				frames, err := s.Frames(b.Context(), &video.FramesRequest{Times: times, Width: 320, Height: 180})
				if err != nil {
					b.Fatal(err)
				}
				for _, f := range frames {
					f.Release()
				}
				s.Close()
			}
		})
		b.Run(fx.name+"/thumbnails", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				for _, at := range times {
					f, err := ffmpeg.Thumbnail(b.Context(), src, &video.FrameRequest{Second: &at, Width: 320, Height: 180})
					if err != nil {
						b.Fatal(err)
					}
					f.Release()
				}
			}
		})
	}
}
