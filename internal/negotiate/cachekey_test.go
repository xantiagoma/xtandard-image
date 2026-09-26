package negotiate

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestAcceptKey: equivalent Accept headers share a key, headers that can
// change the decision do not, and equal keys always decide alike.
func TestAcceptKey(t *testing.T) {
	t.Parallel()
	same := [][]string{
		{"", "*/*", "*/*;q=1"},
		{"image/webp,*/*", "*/*, image/webp", "image/webp;q=1.0, */*;q=1"},
		{"image/avif,image/webp,image/*;q=0.8", "image/webp, image/avif, image/*;q=0.8, text/html"},
	}
	keys := make([]string, 0, len(same))
	for _, group := range same {
		k := ParseAccept(group[0]).Key()
		for _, h := range group[1:] {
			if got := ParseAccept(h).Key(); got != k {
				t.Errorf("Key(%q) = %q, want Key(%q) = %q", h, got, group[0], k)
			}
		}
		for _, prev := range keys {
			if prev == k {
				t.Errorf("group %q shares a key with another group", group)
			}
		}
		keys = append(keys, k)
	}
	if got, want := ParseAccept("image/png").Key(), "jpeg=0/0;png=1000/1000;webp=0/0;avif=0/0;jxl=0/0;heic=0/0;gif=0/0;tiff=0/0;bmp=0/0;ico=0/0;svg=0/0;mp4=0/0;webm=0/0"; got != want {
		t.Errorf("Key = %q, want %q", got, want)
	}

	// Equal keys decide alike across content rows.
	cfg := DefaultConfig()
	cfg.AutoAVIF, cfg.AutoWebP = true, true
	req := &transform.Request{Kind: transform.KindProcess}
	for _, meta := range []SourceMeta{
		{Format: transform.FormatJPEG, NeedsRaster: true},
		{Format: transform.FormatPNG, HasAlpha: true, NeedsRaster: true},
		{Format: transform.FormatGIF, IsAnimated: true, FrameCount: 3, NeedsRaster: true},
	} {
		for _, group := range same {
			first, err := Decide(req, ParseAccept(group[0]), &cfg, meta)
			for _, h := range group[1:] {
				d, err2 := Decide(req, ParseAccept(h), &cfg, meta)
				if d.Format != first.Format || d.Vary != first.Vary || (err == nil) != (err2 == nil) {
					t.Errorf("%v: Decide(%q) = %v, Decide(%q) = %v", meta.Format, group[0], first.Format, h, d.Format)
				}
			}
		}
	}
}
