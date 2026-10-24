package imgproxy_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/preset"
)

// TestAllowedOptions covers XI_SECURITY_ALLOWED_PROCESSING_OPTIONS, as
// imgproxy's IMGPROXY_ALLOWED_PROCESSING_OPTIONS: names are matched as
// written, every URL kind is checked, and a preset's own options are not.
func TestAllowedOptions(t *testing.T) {
	t.Parallel()
	presets, err := preset.New(preset.Options{Definitions: map[string]string{"thumb": "rs:fill:100:100/q:70"}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		allowed   []string
		path      string
		forbidden string // option refused; "" = parses
	}{
		{"empty list allows all", nil, "/unsafe/w:100/q:50" + src, ""},
		{"allowed", []string{"w", "h"}, "/unsafe/w:100/h:50" + src, ""},
		{"not allowed", []string{"w"}, "/unsafe/w:100/q:50" + src, "q"},
		{"alias is its own name", []string{"w"}, "/unsafe/width:100" + src, "width"},
		{"chained pipeline", []string{"w"}, "/unsafe/w:100/-/h:20" + src, "h"},
		{"preset expands freely", []string{"pr"}, "/unsafe/pr:thumb" + src, ""},
		{"preset itself checked", []string{"w"}, "/unsafe/pr:thumb" + src, "pr"},
		{"info url", []string{"w"}, "/info/unsafe/size:1" + src, "size"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := imgproxy.New(&imgproxy.Options{AllowedOptions: tc.allowed, Presets: presets})
			if err != nil {
				t.Fatal(err)
			}
			if path, info := strings.CutPrefix(tc.path, "/info"); info {
				_, err = p.ParseInfoPath(path)
			} else {
				_, err = p.ParsePath(tc.path)
			}
			if tc.forbidden == "" {
				if err != nil {
					t.Fatalf("parse: %v", err)
				}
				return
			}
			if !errors.Is(err, dialect.ErrOptionNotAllowed) || errs.KindOf(err) != errs.KindForbidden {
				t.Fatalf("err = %v, want option not allowed (403)", err)
			}
			if e, ok := errors.AsType[*errs.Error](err); !ok || e.Public != "option not allowed: "+tc.forbidden {
				t.Errorf("err = %v, want it to name %q", err, tc.forbidden)
			}
		})
	}
}
