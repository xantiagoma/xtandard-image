//go:build cgo

package vips_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// pdfPasswordFixtures are the user-password PDFs of testdata/LICENSES.md
// (qpdf of pages.pdf: 200x150 first page, 3 pages).
var pdfPasswordFixtures = []struct{ name, password string }{
	{"pdf/password.pdf", "secret"},             // AES-256 (R6)
	{"pdf/password-aes128.pdf", "open sesame"}, // AES-128 (R4)
}

// publicMessage returns err's client-facing message.
func publicMessage(err error) string {
	if e, ok := errors.AsType[*errs.Error](err); ok {
		return e.PublicMessage()
	}
	return ""
}

// TestPDFPasswordRight opens each fixture with its password through every
// load path (probe, load and render, thumbnail): WP-115.
func TestPDFPasswordRight(t *testing.T) {
	t.Parallel()
	for _, fx := range pdfPasswordFixtures {
		t.Run(fx.name, func(t *testing.T) {
			t.Parallel()
			src := newBuffer(t, readFixture(t, fx.name))
			defer src.Release()
			pw := []byte(fx.password)
			buf, err := src.WithPassword(pw)
			if err != nil {
				t.Fatal(err)
			}
			clear(pw) // the Buffer holds its own copy
			defer buf.Release()
			if !buf.HasPassword() || src.HasPassword() {
				t.Fatalf("HasPassword: view %t, source %t; want true, false", buf.HasPassword(), src.HasPassword())
			}
			job(t, func(ctx context.Context, a *vips.Arena) error {
				h, err := vips.Probe(ctx, buf)
				if err != nil {
					return err
				}
				if h.Format != vips.FormatPDF || h.Width != 200 || h.Height != 150 || h.Pages != 3 {
					t.Errorf("probe = %s %dx%d, %d pages; want pdf 200x150, 3 pages", h.Format, h.Width, h.Height, h.Pages)
				}
				img, err := a.Add(vips.Load(ctx, buf, &vips.LoadOptions{Page: 1, Access: vips.AccessSequential}))
				if err != nil {
					return err
				}
				out, err := img.Save(ctx, vips.FormatPNG, nil)
				if err != nil {
					return err
				}
				out.Release()
				th, err := a.Add(vips.Thumbnail(ctx, buf, &vips.ThumbnailOptions{Width: 100, Height: 100, Size: vips.SizeDown}))
				if err != nil {
					return err
				}
				if th.Width() != 100 || th.Height() != 75 {
					t.Errorf("thumbnail = %dx%d, want 100x75", th.Width(), th.Height())
				}
				out, err = th.Save(ctx, vips.FormatPNG, nil)
				if err != nil {
					return err
				}
				out.Release()
				return nil
			})
		})
	}
}

// TestPDFPasswordWrong checks the 422 messages: a wrong password says so,
// a missing one keeps the existing message, and neither echoes anything.
func TestPDFPasswordWrong(t *testing.T) {
	t.Parallel()
	const wrong = "not-the-password"
	for _, fx := range pdfPasswordFixtures {
		for _, tc := range []struct {
			name, password, public string
		}{
			{"wrong", wrong, "wrong document password"},
			{"missing", "", "password-protected documents are not supported"},
		} {
			t.Run(fx.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				src := newBuffer(t, readFixture(t, fx.name))
				defer src.Release()
				buf, err := src.WithPassword([]byte(tc.password))
				if err != nil {
					t.Fatal(err)
				}
				defer buf.Release()
				for op, run := range map[string]func(ctx context.Context, a *vips.Arena) error{
					"probe": func(ctx context.Context, _ *vips.Arena) error { _, err := vips.Probe(ctx, buf); return err },
					"load": func(ctx context.Context, a *vips.Arena) error {
						_, err := a.Add(vips.Load(ctx, buf, nil))
						return err
					},
					"thumbnail": func(ctx context.Context, a *vips.Arena) error {
						_, err := a.Add(vips.Thumbnail(ctx, buf, &vips.ThumbnailOptions{Width: 50}))
						return err
					},
				} {
					err := tryJob(t.Context(), run)
					if errs.KindOf(err) != errs.KindUnsupported || publicMessage(err) != tc.public {
						t.Errorf("%s: %v (kind %s, public %q); want unsupported %q", op, err, errs.KindOf(err), publicMessage(err), tc.public)
					}
					if wrongPW := tc.password != ""; errors.Is(err, errs.ErrWrongPassword) != wrongPW {
						t.Errorf("%s: errors.Is(err, ErrWrongPassword) = %t, want %t", op, !wrongPW, wrongPW)
					}
					if err != nil && (strings.Contains(err.Error(), wrong) || strings.Contains(err.Error(), fx.password)) {
						t.Errorf("%s: error %q echoes a password", op, err)
					}
				}
			})
		}
	}
}

// TestPDFPasswordIgnoredElsewhere: a password on a non-encrypted PDF or on
// another format changes nothing.
func TestPDFPasswordIgnoredElsewhere(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"pdf/pages.pdf", "jpeg/baseline.jpg"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src := newBuffer(t, readFixture(t, name))
			defer src.Release()
			buf, err := src.WithPassword([]byte("irrelevant"))
			if err != nil {
				t.Fatal(err)
			}
			defer buf.Release()
			job(t, func(ctx context.Context, a *vips.Arena) error {
				if _, err := vips.Probe(ctx, buf); err != nil {
					return err
				}
				_, err := a.Add(vips.Thumbnail(ctx, buf, &vips.ThumbnailOptions{Width: 50}))
				return err
			})
		})
	}
}

func TestPDFWithPasswordRejectsNUL(t *testing.T) {
	t.Parallel()
	src := newBuffer(t, []byte("%PDF-1.4"))
	defer src.Release()
	if _, err := src.WithPassword([]byte("a\x00b")); !errors.Is(err, vips.ErrPasswordNUL) {
		t.Errorf("WithPassword(NUL) = %v, want ErrPasswordNUL", err)
	}
	same, err := src.WithPassword(nil)
	if err != nil || same != src {
		t.Fatalf("WithPassword(nil) = %p, %v; want the source itself", same, err)
	}
	same.Release() // the extra reference
}
