//go:build cgo

package exec_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	xexec "github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Video sprites and animations (WP-75) and video output (WP-76).

const animatedGIF = "gif/animated.gif" // 96×72, 6 frames of 100 ms

// outExecutor is videoExecutor with video output enabled on this build's
// encoders; edit adjusts the encoder settings.
func outExecutor(tb testing.TB, s video.Settings, edit func(*encode.VideoConfig)) *xexec.Executor {
	tb.Helper()
	ec := encode.DefaultConfig()
	ec.Video = encode.VideoConfig{Enabled: s.Enabled, Encoders: codec.VideoEncoders()}
	if edit != nil {
		edit(&ec.Video)
	}
	enc, err := encode.New(&ec)
	if err != nil {
		tb.Fatal(err)
	}
	e, err := xexec.New(&xexec.Config{Limits: testLimits, Video: s}, enc)
	if err != nil {
		tb.Fatal(err)
	}
	return e
}

// compositeJob builds the job of a video source the way the server and
// internal/app do: probe keeping the FFmpeg handle (codec.ProbeSession),
// resize the source meta to the sprite or animation the request asks for
// (video.RequestLayout), and hand the handle to the executor. The test
// cleanup closes the handle and releases the source.
func compositeJob(tb testing.TB, fixture string, req *transform.Request, out transform.Format) *xexec.Job {
	tb.Helper()
	j, release := compositeJobFunc(tb, fixture, req, out)
	tb.Cleanup(release)
	return j
}

// compositeJobFunc is compositeJob returning the release function.
func compositeJobFunc(tb testing.TB, fixture string, req *transform.Request, out transform.Format) (*xexec.Job, func()) {
	tb.Helper()
	buf := newBuffer(tb, readFixture(tb, fixture))
	var m codec.ImageMeta
	err := vips.Run(tb.Context(), func() error {
		var err error
		m, err = codec.ProbeSession(tb.Context(), buf, codec.Limits{}, new(videoSettings()), "")
		return err
	})
	if err != nil {
		buf.Release()
		tb.Fatalf("probe %s: %v", fixture, err)
	}
	release := func() {
		m.Session.Close()
		buf.Release()
	}
	pm := plan.SourceMeta{
		Width: m.Width, Height: m.Height, Frames: 1, HasAlpha: m.HasAlpha,
		Orientation: m.Orientation, Format: m.Format.String(), IsVideo: true,
	}
	animated := false
	if l, ok := video.RequestLayout(req, m.Video, 0); ok {
		pm.VideoComposite, pm.Orientation = true, 1
		pm.StreamWidth, pm.StreamHeight = m.Video.DisplayWidth, m.Video.DisplayHeight
		pm.Width, pm.Height = l.Width, l.Height
		if !l.Tile {
			pm.Height, pm.Frames = l.CellHeight, l.Frames
			pm.IsAnimated = l.Frames > 1
			animated = pm.IsAnimated
		}
	}
	d := negotiate.Decision{Format: out}
	return &xexec.Job{
		Request: req, Source: buf, Meta: pm, Video: m.Session,
		Target: encode.NewTarget(&req.Output, &d, animated),
	}, release
}

// runJob runs j and returns the encoded bytes (a copy) and the result.
func runJob(tb testing.TB, e *xexec.Executor, j *xexec.Job) ([]byte, *xexec.Result, error) {
	tb.Helper()
	res, err := e.Run(tb.Context(), j)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Release()
	return bytes.Clone(res.Body.Bytes()), res, nil
}

// frameOf cuts frame i out of a vertical strip of h-high frames (decode
// returns an *image.NRGBA).
func frameOf(tb testing.TB, img image.Image, i, h int) image.Image {
	tb.Helper()
	n, ok := img.(*image.NRGBA)
	if !ok {
		tb.Fatalf("frameOf: %T", img)
	}
	b := n.Bounds()
	return n.SubImage(image.Rect(b.Min.X, b.Min.Y+i*h, b.Max.X, b.Min.Y+(i+1)*h))
}

// TestVideoTile: sprites of Columns × Rows frames spread over the video
// (or every step seconds), fitted, extended or filled into the tile size,
// trimmed to the frames a short video has; rotated video comes out upright.
func TestVideoTile(t *testing.T) {
	t.Parallel()
	requireVideo(t)
	e := outExecutor(t, videoSettings(), nil)
	for _, tc := range []struct {
		name, fixture, options string
		w, h                   int
		info                   string
	}{
		{"video-tile-vp9-3x2", videoVP9, "vtt:-1:3:2:160:90", 480, 180, "tile 3x2 6 frames 0.000-1.680s"},
		{"video-tile-vp9-trim", videoVP9, "vtt:0.5:3:3:160:90:0:1", 480, 180, "tile 3x2 4 frames 0.000-1.520s"},
		{"video-tile-vp9-untrimmed", videoVP9, "vtt:0.5:3:2:160:90", 480, 180, "tile 3x2 4 frames"},
		{"video-tile-av1-extend", videoAV1, "vtt:-1:2:2:100:100:1", 200, 200, "tile 2x2 4 frames"},
		{"video-tile-av1-fill", videoAV1, "vtt:-1:2:2:100:100:0:0:1:0.5:0.5", 200, 200, "tile 2x2 4 frames"},
		{"video-tile-rotate90", videoRot90, "vtt:-1:2:1:0:160", 180, 160, "tile 2x1 2 frames"},
		{"video-tile-h264-resized", videoH264, "vtt:-1:4:1:320:180/rs:fit:640:0", 640, 90, "tile 4x1 4 frames"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data, res, err := runJob(t, e, compositeJob(t, tc.fixture, parse(t, tc.options), transform.FormatPNG))
			if err != nil {
				t.Fatal(err)
			}
			img, frames := decode(t, data)
			if b := img.Bounds(); b.Dx() != tc.w || b.Dy() != tc.h || frames != 1 {
				t.Fatalf("sprite %dx%d (%d frames), want %dx%d", b.Dx(), b.Dy(), frames, tc.w, tc.h)
			}
			if got := res.VideoFrame.String(); !strings.HasPrefix(got, tc.info) {
				t.Errorf("X-Xi-Video-Frame %q, want prefix %q", got, tc.info)
			}
			golden.Compare(t, "exec/"+tc.name, img, golden.Options{Tolerance: videoTol})
		})
	}
}

// TestVideoAnimation: an animation of frames sampled every step seconds,
// with the requested delay (or the step's pace), as GIF or WebP; trimmed
// to the video's length or padded with black frames; a still output keeps
// the first frame. The first and last frames are compared perceptually.
func TestVideoAnimation(t *testing.T) {
	t.Parallel()
	requireVideo(t)
	e := outExecutor(t, videoSettings(), nil)
	for _, tc := range []struct {
		name, fixture, options string
		out                    transform.Format
		w, h, frames           int
		delay                  int
		merged                 bool
	}{
		{"video-anim-vp9-gif", videoVP9, "vta:0.25:0:8:160:90", transform.FormatGIF, 160, 90, 8, 250, false},
		{"video-anim-av1-webp", videoAV1, "vta:-1:100:5:0:120", transform.FormatWebP, 213, 120, 5, 100, false},
		{"video-anim-vp9-trim", videoVP9, "vta:0.5:80:10:96:0:0:1", transform.FormatWebP, 96, 54, 4, 80, false},
		{"video-anim-vp9-padded", videoVP9, "vta:0.5:80:6:96:0", transform.FormatWebP, 96, 54, 6, 80, true},
		{"video-anim-rotate90-fill", videoRot90, "vta:-1:50:4:100:100:0:0:1", transform.FormatWebP, 100, 100, 4, 50, false},
		{"video-anim-still", videoVP9, "vta:-1:100:5:160:90", transform.FormatJPEG, 160, 90, 1, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data, res, err := runJob(t, e, compositeJob(t, tc.fixture, parse(t, tc.options), tc.out))
			if err != nil {
				t.Fatal(err)
			}
			img, frames := decode(t, data)
			// The WebP encoder merges identical frames (the black ones
			// padding a short video) into one longer frame.
			if b := img.Bounds(); frames > tc.frames || (frames < tc.frames && !tc.merged) ||
				b.Dx() != tc.w || b.Dy() != tc.h*frames {
				t.Fatalf("%dx%d with %d frames, want %d frames of %dx%d", b.Dx(), b.Dy(), frames, tc.frames, tc.w, tc.h)
			}
			if tc.delay > 0 {
				d := delays(t, data)
				sum := 0
				for _, v := range d {
					sum += v
				}
				if len(d) != frames || d[0] != tc.delay || sum != tc.frames*tc.delay {
					t.Errorf("delays %v, want %d × %d ms", d, tc.frames, tc.delay)
				}
			}
			tc.frames = frames
			if !strings.HasPrefix(res.VideoFrame.String(), "animation ") {
				t.Errorf("X-Xi-Video-Frame %q", res.VideoFrame.String())
			}
			golden.Compare(t, "exec/"+tc.name+"-first", frameOf(t, img, 0, tc.h), golden.Options{Tolerance: videoTol})
			if tc.frames > 1 {
				golden.Compare(t, "exec/"+tc.name+"-last", frameOf(t, img, tc.frames-1, tc.h), golden.Options{Tolerance: videoTol})
			}
		})
	}
}

// delays reads the frame delays of an animated GIF or WebP.
func delays(tb testing.TB, data []byte) []int {
	tb.Helper()
	buf := newBuffer(tb, data)
	defer buf.Release()
	var d []int
	err := vips.Run(tb.Context(), func() error {
		img, err := vips.Load(tb.Context(), buf, &vips.LoadOptions{NPages: -1})
		if err != nil {
			return err
		}
		defer img.Close()
		d, _ = img.Ints(vips.FieldDelay)
		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}
	return d
}

// probed is what ffprobe reports about an encoded video.
type probed struct {
	Container string
	Codec     string
	PixFmt    string
	Width     int
	Height    int
	Frames    int
	Duration  float64
	AlphaMode string
	Tag       string
}

// ffprobe runs the ffprobe CLI of the builder image on data; the test is
// skipped when it is not installed.
func ffprobe(tb testing.TB, data []byte) probed {
	tb.Helper()
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		tb.Skip("ffprobe is not installed (the full and gpl builder images have it)")
	}
	f := filepath.Join(tb.TempDir(), "out")
	if err := os.WriteFile(f, data, 0o600); err != nil {
		tb.Fatal(err)
	}
	out, err := exec.CommandContext(tb.Context(), bin, "-v", "error", "-count_frames", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name,codec_tag_string,pix_fmt,width,height,nb_read_frames:stream_tags=alpha_mode:format=format_name,duration",
		"-of", "json", f).Output()
	if err != nil {
		tb.Fatalf("ffprobe: %v (%s)", err, out)
	}
	var r struct {
		Streams []struct {
			Codec  string            `json:"codec_name"`
			Tag    string            `json:"codec_tag_string"`
			PixFmt string            `json:"pix_fmt"`
			Width  int               `json:"width"`
			Height int               `json:"height"`
			Frames string            `json:"nb_read_frames"`
			Tags   map[string]string `json:"tags"`
		} `json:"streams"`
		Format struct {
			Name     string `json:"format_name"`
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &r); err != nil || len(r.Streams) != 1 {
		tb.Fatalf("ffprobe output %s: %v", out, err)
	}
	s := r.Streams[0]
	p := probed{
		Container: r.Format.Name, Codec: s.Codec, PixFmt: s.PixFmt, Width: s.Width, Height: s.Height,
		Tag: s.Tag,
	}
	for k, v := range s.Tags { // Matroska tags are upper case
		if strings.EqualFold(k, "alpha_mode") {
			p.AlphaMode = v
		}
	}
	p.Frames, _ = strconv.Atoi(s.Frames)
	p.Duration, _ = strconv.ParseFloat(r.Format.Duration, 64)
	return p
}

// gif100 makes a 100-frame 320×180 animated GIF of 40 ms frames: the 50
// frames of the VP9 clip, then the same 50 frames decoded from the AV1
// clip (their compression differs, so the GIF encoder merges none).
func gif100(tb testing.TB, e *xexec.Executor) []byte {
	tb.Helper()
	var halves [2][]byte
	for i, fx := range []string{videoVP9, videoAV1} {
		j, release := compositeJobFunc(tb, fx, parse(tb, "vta:0.04:40:50:320:180"), transform.FormatWebP)
		data, _, err := runJob(tb, e, j)
		release()
		if err != nil {
			tb.Fatal(err)
		}
		halves[i] = data
	}
	var out []byte
	err := vips.Run(tb.Context(), func() error {
		var a vips.Arena
		defer a.Close()
		ctx := tb.Context()
		var strips []*vips.Image
		for _, h := range halves {
			buf := newBuffer(tb, h)
			img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{NPages: -1, Access: vips.AccessRandom}))
			buf.Release()
			if err != nil {
				return err
			}
			strips = append(strips, img)
		}
		img, err := a.Add(vips.Arrayjoin(ctx, strips, 1))
		if err != nil {
			return err
		}
		n := img.Height() / 180
		if img, err = a.Add(img.SetPageHeight(ctx, 180)); err != nil {
			return err
		}
		if img, err = a.Add(img.SetInt(ctx, vips.FieldNPages, n)); err != nil {
			return err
		}
		d := make([]int, n)
		for i := range d {
			d[i] = 40
		}
		if img, err = a.Add(img.SetInts(ctx, vips.FieldDelay, d)); err != nil {
			return err
		}
		o := vips.DefaultSaveOptions()
		blob, err := img.Save(ctx, vips.FormatGIF, &o)
		if err != nil {
			return err
		}
		defer blob.Release()
		out = bytes.Clone(blob.Bytes())
		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}
	if _, frames := decode(tb, out); frames != 100 {
		tb.Fatalf("the source GIF has %d frames, want 100", frames)
	}
	return out
}

// mp4Codec is the codec this build writes into MP4: H.264 with libx264
// (gpl), else AV1.
func mp4Codec() string {
	if codec.VideoEncoders().H264 {
		return "h264"
	}
	return "av1"
}

// moovFirst reports an MP4 whose moov box precedes mdat (faststart).
func moovFirst(data []byte) bool {
	m, d := bytes.Index(data, []byte("moov")), bytes.Index(data, []byte("mdat"))
	return m > 0 && d > 0 && m < d
}

// TestVideoOutput: animated GIF and WebP sources and a video animation
// encoded as WebM (VP9) and MP4 (H.264 in the gpl image, AV1 in full),
// checked with ffprobe: container, codec, frame count, duration ≈ the
// source's, smaller than the source GIF, faststart MP4.
func TestVideoOutput(t *testing.T) {
	t.Parallel()
	requireVideo(t)
	e := outExecutor(t, videoSettings(), nil)
	gif := readFixture(t, animatedGIF)
	for _, tc := range []struct {
		name      string
		out       transform.Format
		container string
		codec     string
	}{
		{"webm", transform.FormatWebM, "matroska,webm", "vp9"},
		{"mp4", transform.FormatMP4, "mov,mp4,m4a,3gp,3g2,mj2", mp4Codec()},
	} {
		t.Run("gif-"+tc.name, func(t *testing.T) {
			t.Parallel()
			j, buf := job(t, gif, parse(t, ""), tc.out)
			defer buf.Release()
			data, res, err := runJob(t, e, j)
			if err != nil {
				t.Fatal(err)
			}
			p := ffprobe(t, data)
			t.Logf("%s: %d bytes (GIF %d), %s %dx%d %s, %d frames, %.3f s",
				tc.name, len(data), len(gif), p.Codec, p.Width, p.Height, p.PixFmt, p.Frames, p.Duration)
			if p.Container != tc.container || p.Codec != tc.codec || p.Width != 96 || p.Height != 72 || p.Frames != 6 {
				t.Errorf("ffprobe %+v, want %s/%s 96x72 with 6 frames", p, tc.container, tc.codec)
			}
			if p.Duration < 0.55 || p.Duration > 0.65 {
				t.Errorf("duration %.3f s, want 0.6 s", p.Duration)
			}
			if len(data) >= len(gif) {
				t.Errorf("%d bytes, not smaller than the %d-byte GIF", len(data), len(gif))
			}
			if res.VideoCodec != tc.codec || res.Frames != 6 || res.ContentType != "video/"+tc.name {
				t.Errorf("result codec %q, %d frames, %s", res.VideoCodec, res.Frames, res.ContentType)
			}
			if tc.out == transform.FormatMP4 && !moovFirst(data) {
				t.Error("moov box after mdat: faststart did not run")
			}
		})
	}
	t.Run("gif-100-frames", func(t *testing.T) {
		t.Parallel()
		big := gif100(t, e)
		for _, f := range []transform.Format{transform.FormatWebM, transform.FormatMP4} {
			j, buf := job(t, big, parse(t, ""), f)
			data, _, err := runJob(t, e, j)
			buf.Release()
			if err != nil {
				t.Fatal(err)
			}
			p := ffprobe(t, data)
			t.Logf("100-frame GIF → %s: %d bytes (GIF %d, %.1f%%), %s %dx%d, %d frames, %.3f s",
				f, len(data), len(big), 100*float64(len(data))/float64(len(big)), p.Codec, p.Width, p.Height, p.Frames, p.Duration)
			if p.Frames != 100 || p.Width != 320 || p.Height != 180 || p.Duration < 3.95 || p.Duration > 4.05 || len(data) >= len(big) {
				t.Errorf("%s: ffprobe %+v, %d bytes; want 100 frames of 320x180, 4 s, smaller than the GIF", f, p, len(data))
			}
		}
	})
	t.Run("webp-alpha-webm", func(t *testing.T) {
		t.Parallel()
		src := readFixture(t, "webp/animated.webp")
		j, buf := job(t, src, parse(t, ""), transform.FormatWebM)
		defer buf.Release()
		data, _, err := runJob(t, e, j)
		if err != nil {
			t.Fatal(err)
		}
		p := ffprobe(t, data)
		if p.Codec != "vp9" || p.Frames < 2 {
			t.Errorf("ffprobe %+v", p)
		}
		if j.Meta.HasAlpha && p.AlphaMode != "1" {
			t.Errorf("alpha source without alpha_mode: %+v", p)
		}
	})
	t.Run("video-animation-mp4", func(t *testing.T) {
		t.Parallel()
		data, _, err := runJob(t, e, compositeJob(t, videoVP9, parse(t, "vta:0.2:200:10:320:180"), transform.FormatMP4))
		if err != nil {
			t.Fatal(err)
		}
		p := ffprobe(t, data)
		if p.Codec != mp4Codec() || p.Frames != 10 || p.Width != 320 || p.Height != 180 || p.Duration < 1.9 || p.Duration > 2.1 {
			t.Errorf("ffprobe %+v, want %s 320x180, 10 frames, 2 s", p, mp4Codec())
		}
	})
	t.Run("odd-size-padded", func(t *testing.T) {
		t.Parallel()
		j, buf := job(t, gif, parse(t, "rs:force:45:31"), transform.FormatWebM)
		defer buf.Release()
		data, res, err := runJob(t, e, j)
		if err != nil {
			t.Fatal(err)
		}
		if p := ffprobe(t, data); p.Width != 46 || p.Height != 32 || res.Width != 46 {
			t.Errorf("ffprobe %+v, result %dx%d: want 46x32", p, res.Width, res.Height)
		}
	})
	t.Run("hevc", func(t *testing.T) {
		t.Parallel()
		j, buf := job(t, gif, parse(t, ""), transform.FormatHEVC)
		defer buf.Release()
		data, res, err := runJob(t, e, j)
		if !codec.VideoEncoders().HEVC {
			if errs.KindOf(err) != errs.KindUnsupported {
				t.Fatalf("hevc without libx265: err = %v, want unsupported", err)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		p := ffprobe(t, data)
		t.Logf("hevc: %d bytes (GIF %d), %s %dx%d, %d frames, %.3f s", len(data), len(gif), p.Codec, p.Width, p.Height, p.Frames, p.Duration)
		if p.Codec != "hevc" || p.Tag != "hvc1" || p.Frames != 6 || res.ContentType != "video/mp4" {
			t.Errorf("ffprobe %+v, content type %s", p, res.ContentType)
		}
	})
}

// TestVideoOutputQuality: the request quality moves the CRF: a higher
// quality gives a larger file.
func TestVideoOutputQuality(t *testing.T) {
	t.Parallel()
	requireVideo(t)
	e := outExecutor(t, videoSettings(), nil)
	size := func(q string) int {
		j := compositeJob(t, videoAV1, parse(t, "vta:0.2:100:10:320:180/q:"+q), transform.FormatWebM)
		data, _, err := runJob(t, e, j)
		if err != nil {
			t.Fatal(err)
		}
		return len(data)
	}
	if lo, hi := size("30"), size("95"); lo >= hi {
		t.Errorf("q30 %d bytes, q95 %d bytes: want q95 larger", lo, hi)
	}
}

// TestVideoOutputLimits: frames, duration and frame pixels over the
// XI_VIDEO_ENCODE_* limits are refused; video off refuses the output.
func TestVideoOutputLimits(t *testing.T) {
	t.Parallel()
	requireVideo(t)
	gif := readFixture(t, animatedGIF)
	for name, edit := range map[string]func(*encode.VideoConfig){
		"frames":   func(c *encode.VideoConfig) { c.Settings.MaxFrames = 5 },
		"duration": func(c *encode.VideoConfig) { c.Settings.MaxDuration = 500 * time.Millisecond },
		"pixels":   func(c *encode.VideoConfig) { c.Settings.MaxPixels = 96 * 70 },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			j, buf := job(t, gif, parse(t, ""), transform.FormatWebM)
			defer buf.Release()
			if _, _, err := runJob(t, outExecutor(t, videoSettings(), edit), j); errs.KindOf(err) != errs.KindLimitExceeded {
				t.Fatalf("err = %v, want limit exceeded", err)
			}
		})
	}
	t.Run("disabled", func(t *testing.T) {
		t.Parallel()
		j, buf := job(t, gif, parse(t, ""), transform.FormatMP4)
		defer buf.Release()
		if _, _, err := runJob(t, outExecutor(t, video.Settings{}, nil), j); errs.KindOf(err) != errs.KindUnsupported {
			t.Fatalf("err = %v, want unsupported", err)
		}
	})
	t.Run("composite-frames-capped", func(t *testing.T) {
		t.Parallel()
		s := videoSettings()
		s.MaxCompositeFrames = 3
		req := parse(t, "vtt:-1:3:3:64:36")
		j := compositeJob(t, videoVP9, req, transform.FormatPNG)
		// The server sized the sprite with the same cap.
		m := ffmeta(t, videoVP9)
		l, _ := video.RequestLayout(req, &m, 3)
		j.Meta.Width, j.Meta.Height = l.Width, l.Height
		data, _, err := runJob(t, outExecutor(t, s, nil), j)
		if err != nil {
			t.Fatal(err)
		}
		if img, _ := decode(t, data); img.Bounds().Dx() != 192 || img.Bounds().Dy() != 36 {
			t.Errorf("capped sprite %v, want 192x36 (3 columns, 1 row)", img.Bounds())
		}
	})
}

// ffmeta probes a video fixture.
func ffmeta(tb testing.TB, fixture string) video.Meta {
	tb.Helper()
	buf := newBuffer(tb, readFixture(tb, fixture))
	defer buf.Release()
	m, err := ffmpeg.Probe(tb.Context(), buf, video.Limits{})
	if err != nil {
		tb.Fatal(err)
	}
	return m
}

// TestVideoOutputCanceled: a done context stops the encode with its kind.
func TestVideoOutputCanceled(t *testing.T) {
	t.Parallel()
	requireVideo(t)
	j, buf := job(t, readFixture(t, animatedGIF), parse(t, ""), transform.FormatWebM)
	defer buf.Release()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := outExecutor(t, videoSettings(), nil).Run(ctx, j); errs.KindOf(err) != errs.KindCanceled {
		t.Fatalf("err = %v, want canceled", err)
	}
}

// TestVideoOutputUnsupported: without FFmpeg (slim) MP4, WebM and HEVC
// output, and sprites, answer errs.KindUnsupported, never a crash.
func TestVideoOutputUnsupported(t *testing.T) {
	t.Parallel()
	if codec.VideoAvailable() {
		t.Skip("this build writes video")
	}
	e := outExecutor(t, video.Settings{Enabled: true}, nil)
	for _, f := range []transform.Format{transform.FormatMP4, transform.FormatWebM, transform.FormatHEVC} {
		j, buf := job(t, readFixture(t, animatedGIF), parse(t, ""), f)
		_, err := e.Run(t.Context(), j)
		buf.Release()
		if errs.KindOf(err) != errs.KindUnsupported {
			t.Errorf("%s: err = %v, want unsupported", f, err)
		}
	}
	buf := newBuffer(t, readFixture(t, videoVP9))
	defer buf.Release()
	err := vips.Run(t.Context(), func() error {
		_, err := codec.ProbeSession(t.Context(), buf, codec.Limits{}, new(videoSettings()), "")
		return err
	})
	if errs.KindOf(err) != errs.KindUnsupported {
		t.Fatalf("probe: err = %v, want unsupported", err)
	}
}

// TestVideoOutputNoLeak: 200 GIF → WebM encodes and 200 sprites leave no
// libvips object, tracked byte or FFmpeg encoder or handle behind.
func TestVideoOutputNoLeak(t *testing.T) {
	requireVideo(t)
	e := outExecutor(t, videoSettings(), nil)
	j, buf := job(t, readFixture(t, animatedGIF), parse(t, ""), transform.FormatWebM)
	defer buf.Release()
	tile := parse(t, "vtt:-1:2:2:64:36")
	run := func() {
		res, err := e.Run(t.Context(), j)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Release()
		// A sprite on a fresh probe handle, closed like the server does.
		tj, release := compositeJobFunc(t, videoVP9, tile, transform.FormatPNG)
		defer release()
		res, err = e.Run(t.Context(), tj)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Release()
	}
	run()
	objects, mem := vips.LiveObjects(), vips.ReadMemStats().Mem
	for range 200 {
		run()
	}
	if n := vips.LiveObjects(); n > objects {
		t.Errorf("libvips objects %d → %d", objects, n)
	}
	if m := vips.ReadMemStats().Mem; m > mem {
		t.Errorf("libvips tracked memory %d → %d bytes", mem, m)
	}
	if s := ffmpeg.ReadStats(); s.Encoders != 0 {
		t.Errorf("%d FFmpeg encoders open", s.Encoders)
	}
}

// BenchmarkVideoOutput measures a 100-frame 320×180 animated GIF (made
// from the VP9 clip) encoded as WebM (VP9) and MP4 (H.264 in the gpl
// image, AV1 in full), end to end through the executor, and a 3×3 sprite
// and a 10-frame animation of the 720p clips.
func BenchmarkVideoOutput(b *testing.B) {
	if !codec.VideoAvailable() {
		b.Skip("built without FFmpeg")
	}
	e := outExecutor(b, videoSettings(), nil)
	gif := gif100(b, e)
	b.Logf("source GIF: %d bytes, 100 frames of 320x180", len(gif))
	for _, f := range []transform.Format{transform.FormatWebM, transform.FormatMP4} {
		b.Run("gif100-320/"+f.String()+"-"+codecOf(f), func(b *testing.B) {
			j, buf := job(b, gif, parse(b, ""), f)
			defer buf.Release()
			var n int
			for b.Loop() {
				res, err := e.Run(b.Context(), j)
				if err != nil {
					b.Fatal(err)
				}
				n = res.Body.Len()
				res.Body.Release()
			}
			b.ReportMetric(float64(n), "bytes")
		})
	}
	for _, fx := range []struct{ name, file string }{{"h264", videoH264}, {"vp9", videoVP9}, {"av1", videoAV1}} {
		for _, bc := range []struct {
			name, options string
			out           transform.Format
		}{
			{"tile3x3-png", "vtt:-1:3:3:160:90", transform.FormatPNG},
			{"anim10-webp", "vta:-1:100:10:320:180", transform.FormatWebP},
		} {
			b.Run(fx.name+"/"+bc.name, func(b *testing.B) {
				req := parse(b, bc.options)
				for b.Loop() {
					j, release := compositeJobFunc(b, fx.file, req, bc.out)
					res, err := e.Run(b.Context(), j)
					if err != nil {
						b.Fatal(err)
					}
					res.Body.Release()
					release()
				}
			})
		}
	}
}

func codecOf(f transform.Format) string {
	if f == transform.FormatWebM {
		return "vp9"
	}
	return mp4Codec()
}
