//go:build cgo

package vips_test

import (
	"bytes"
	"context"
	"errors"
	"image/jpeg"
	"io"
	"os"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// These tests need libvips from the base image: run them with
// `task docker:test` (go-standards §8: vips is initialised once per package).
func TestMain(m *testing.M) {
	if err := vips.Init(new(vips.DefaultConfig())); err != nil {
		_, _ = os.Stderr.WriteString("vips init: " + err.Error() + "\n")
		os.Exit(1)
	}
	code := m.Run()
	vips.Shutdown()
	os.Exit(code)
}

func readFixture(tb testing.TB, name string) []byte {
	tb.Helper()
	b, err := os.ReadFile(golden.Fixture(tb, name))
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

func newBuffer(tb testing.TB, data []byte) *vips.Buffer {
	tb.Helper()
	buf, err := vips.NewBufferFromReader(bytes.NewReader(data), 1<<30)
	if err != nil {
		tb.Fatalf("NewBufferFromReader: %v", err)
	}
	return buf
}

// thumbnailJPEG runs one full job and returns the encoded bytes (copied, so
// the blob can be released inside the job).
func thumbnailJPEG(ctx context.Context, buf *vips.Buffer, w, h int) ([]byte, error) {
	var out []byte
	err := vips.Run(ctx, func() error {
		img, err := vips.LoadThumbnail(ctx, buf, w, h)
		if err != nil {
			return err
		}
		defer img.Close()
		blob, err := img.SaveJPEG(ctx, 80)
		if err != nil {
			return err
		}
		defer blob.Release()
		out = bytes.Clone(blob.Bytes())
		return nil
	})
	return out, err
}

func TestThumbnailFixtures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fixture      string
		w, h         int
		wantW, wantH int
	}{
		{fixture: "jpeg/baseline.jpg", w: 64, h: 64, wantW: 64, wantH: 48},
		{fixture: "jpeg/progressive.jpg", w: 64, h: 64, wantW: 64, wantH: 48},
		{fixture: "jpeg/exif-orientation-6-icc.jpg", w: 64, h: 64, wantW: 64, wantH: 48}, // autorotated upright
		{fixture: "uhdr/photo-ultrahdr.jpg", w: 64, h: 64, wantW: 64, wantH: 48},
		{fixture: "png/rgba8.png", w: 64, h: 64, wantW: 64, wantH: 48},
		{fixture: "png/rgb16.png", w: 64, h: 64, wantW: 64, wantH: 48},
		{fixture: "png/palette.png", w: 64, h: 64, wantW: 64, wantH: 48},
		{fixture: "gif/static.gif", w: 64, h: 64, wantW: 64, wantH: 48},
		{fixture: "gif/animated.gif", w: 64, h: 64, wantW: 64, wantH: 48},
		{fixture: "webp/lossy.webp", w: 64, h: 64, wantW: 64, wantH: 48},
		{fixture: "webp/lossless.webp", w: 64, h: 64, wantW: 64, wantH: 48},
		{fixture: "webp/animated.webp", w: 64, h: 64, wantW: 64, wantH: 48},
		{fixture: "avif/photo.avif", w: 64, h: 64, wantW: 64, wantH: 48},
		{fixture: "heic/photo.heic", w: 64, h: 64, wantW: 64, wantH: 48},
		{fixture: "jxl/photo.jxl", w: 64, h: 64, wantW: 64, wantH: 48},
		{fixture: "jp2/photo.jp2", w: 64, h: 64, wantW: 64, wantH: 48},
		{fixture: "tiff/multipage.tif", w: 64, h: 64, wantW: 64, wantH: 48},
		{fixture: "svg/logo.svg", w: 64, h: 64, wantW: 64, wantH: 48},
		{fixture: "jpeg/baseline.jpg", w: 32, wantW: 32, wantH: 24},          // width only
		{fixture: "jpeg/baseline.jpg", h: 60, wantW: 80, wantH: 60},          // height only
		{fixture: "jpeg/baseline.jpg", wantW: 320, wantH: 240},               // no bounds
		{fixture: "png/palette.png", w: 1000, h: 1000, wantW: 96, wantH: 72}, // never enlarges
	}
	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, readFixture(t, tc.fixture))
			defer buf.Release()

			out, err := thumbnailJPEG(t.Context(), buf, tc.w, tc.h)
			if err != nil {
				t.Fatalf("thumbnail %dx%d: %v", tc.w, tc.h, err)
			}
			cfg, err := jpeg.DecodeConfig(bytes.NewReader(out))
			if err != nil {
				t.Fatalf("output is not a JPEG: %v", err)
			}
			if cfg.Width != tc.wantW || cfg.Height != tc.wantH {
				t.Errorf("output %dx%d, want %dx%d", cfg.Width, cfg.Height, tc.wantW, tc.wantH)
			}
		})
	}
}

// unknownFormat marks inputs no enabled loader may take.
const unknownFormat = "\x00unknown"

func TestThumbnailErrors(t *testing.T) {
	t.Parallel()

	baseline := readFixture(t, "jpeg/baseline.jpg")
	tests := []struct {
		name    string
		data    []byte
		want    errs.Kind
		wantMsg string // substring of the logged vips text, when it matters
	}{
		{name: "bmp has no enabled loader", data: readFixture(t, "bmp/rgb24.bmp"), want: errs.KindUnsupported},
		{name: "ico has no enabled loader", data: readFixture(t, "ico/png-entry.ico"), want: errs.KindUnsupported},
		{name: "garbage", data: []byte("definitely not an image"), want: errs.KindUnsupported},
		// ppmload_buffer is enabled for binary PGM/PPM/PFM only (WP-67): the
		// shim refuses the ASCII and bitmap variants.
		{name: "ascii pgm is refused", data: []byte("P2\n2 2\n255\n0 0 0 0\n"), want: errs.KindUnsupported, wantMsg: "restricted to binary"},
		{name: "pbm bitmap is refused", data: []byte("P4\n8 1\n\x00"), want: errs.KindUnsupported, wantMsg: "restricted to binary"},
		// matrixload is compiled in, untrusted and not on the allowlist. Blocked
		// loaders are skipped by the libvips sniffer (foreign.c
		// file_add_class), so the input reads as an unknown format, or, where
		// ImageMagick is built in, matches its lowest-priority loader, which
		// the shim refuses for anything but PSD.
		{name: "matrix loader is blocked", data: []byte("2 2\n0 0\n0 0\n"), want: errs.KindUnsupported, wantMsg: unknownFormat},
		{name: "jpeg header only", data: baseline[:24], want: errs.KindCorrupt},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buf := newBuffer(t, tc.data)
			defer buf.Release()

			_, err := thumbnailJPEG(t.Context(), buf, 64, 64)
			if got := errs.KindOf(err); got != tc.want {
				t.Fatalf("error kind = %v (%v), want %v", got, err, tc.want)
			}
			opErr, ok := errors.AsType[*vips.OpError](err)
			if !ok {
				t.Fatalf("error %v does not carry *vips.OpError", err)
			}
			switch {
			case tc.wantMsg == unknownFormat:
				if !strings.Contains(opErr.Msg, "not in a known format") && !strings.Contains(opErr.Msg, "restricted to PSD/PSB") {
					t.Errorf("vips message %q names neither an unknown format nor the PSD-only ImageMagick", opErr.Msg)
				}
			case !strings.Contains(opErr.Msg, tc.wantMsg):
				t.Errorf("vips message %q does not mention %q", opErr.Msg, tc.wantMsg)
			}
		})
	}
}

func TestBufferOutlivesRelease(t *testing.T) {
	t.Parallel()

	buf := newBuffer(t, readFixture(t, "jpeg/baseline.jpg"))
	ctx := t.Context()
	err := vips.Run(ctx, func() error {
		img, err := vips.LoadThumbnail(ctx, buf, 64, 64)
		// The image owns a C-side reference: the pixels are decoded lazily
		// on save, after the Go reference is gone.
		buf.Release()
		if err != nil {
			return err
		}
		defer img.Close()
		blob, err := img.SaveJPEG(ctx, 80)
		if err != nil {
			return err
		}
		defer blob.Release()
		if b := blob.Bytes(); len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 || b[len(b)-2] != 0xFF || b[len(b)-1] != 0xD9 {
			t.Errorf("output lacks JPEG SOI/EOI markers")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNewBufferFromReader(t *testing.T) {
	t.Parallel()

	data := bytes.Repeat([]byte("0123456789abcdef"), 20_000) // 320 000 bytes, several doublings
	readErr := errs.New(errs.KindUpstreamError, "boom")
	tests := []struct {
		name    string
		r       io.Reader
		limit   int64
		want    int
		wantErr errs.Kind
	}{
		{name: "grows", r: bytes.NewReader(data), limit: 1 << 20, want: len(data)},
		{name: "one byte reads", r: iotest.OneByteReader(bytes.NewReader(data[:1000])), limit: 1 << 20, want: 1000},
		{name: "exactly the limit", r: bytes.NewReader(data), limit: int64(len(data)), want: len(data)},
		{name: "over the limit", r: bytes.NewReader(data), limit: int64(len(data)) - 1, wantErr: errs.KindSourceTooLarge},
		{name: "empty", r: bytes.NewReader(nil), limit: 10, want: 0},
		{name: "reader error keeps kind", r: iotest.ErrReader(readErr), limit: 10, wantErr: errs.KindUpstreamError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			buf, err := vips.NewBufferFromReader(tc.r, tc.limit)
			if tc.wantErr != errs.KindUnknown {
				if got := errs.KindOf(err); got != tc.wantErr {
					t.Fatalf("error kind = %v (%v), want %v", got, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer buf.Release()
			if buf.Len() != tc.want || !bytes.Equal(buf.Bytes(), data[:tc.want]) {
				t.Errorf("buffer has %d bytes, want %d matching the input", buf.Len(), tc.want)
			}
		})
	}
}

// fetchBuffer is fetch.Buffer (ARCHITECTURE §7), restated so this package
// does not import the fetcher.
type fetchBuffer interface {
	Bytes() []byte
	Retain()
	Release()
}

var (
	_ fetchBuffer = (*vips.Buffer)(nil)
	_ fetchBuffer = (*vips.Blob)(nil)
)

func TestNewBufferSize(t *testing.T) {
	t.Parallel()

	for _, n := range []int{0, 1, 4096, 1 << 20} {
		buf, err := vips.NewBuffer(n)
		if err != nil {
			t.Fatal(err)
		}
		if len(buf.Bytes()) != n || buf.Len() != n {
			t.Errorf("NewBuffer(%d) has %d bytes", n, len(buf.Bytes()))
		}
		buf.Release()
	}
	if _, err := vips.NewBuffer(-1); errs.KindOf(err) != errs.KindInternal {
		t.Errorf("NewBuffer(-1) = %v, want KindInternal", err)
	}
}

func TestBufferRefcount(t *testing.T) {
	t.Parallel()

	buf, err := vips.NewBuffer(16)
	if err != nil {
		t.Fatal(err)
	}
	copy(buf.Bytes(), "0123456789abcdef")
	buf.Retain()
	buf.Release()
	if string(buf.Bytes()) != "0123456789abcdef" {
		t.Fatalf("bytes changed while a reference is held")
	}
	buf.Release()
	if buf.Bytes() != nil {
		t.Errorf("Bytes() after the final Release = non-nil")
	}
	buf.Release() // extra releases are no-ops
}

func TestRunHonoursContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	called := false
	err := vips.Run(ctx, func() error { called = true; return nil })
	if called || errs.KindOf(err) != errs.KindCanceled {
		t.Errorf("Run on a canceled ctx: called=%v err=%v, want not called and KindCanceled", called, err)
	}
}
