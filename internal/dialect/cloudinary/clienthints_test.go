package cloudinary_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestClientHints checks that w_auto and dpr_auto opt into the width and
// DPR client hints exactly like imgix ch=Width,DPR and ImageKit
// w-auto/dpr-auto do, and that negotiate.ApplyClientHints resolves them
// into the first pipeline's Resize.
func TestClientHints(t *testing.T) {
	t.Parallel()
	// A 2x client whose layout slot is 575 CSS px wide.
	hints := negotiate.ClientHints{DPR: 2, Width: 1150}
	tests := []struct {
		name     string
		tx       string
		want     transform.ClientHints
		resolved transform.Resize // Pipelines[0].Resize after ApplyClientHints
	}{
		{
			name:     "w_auto",
			tx:       "w_auto",
			want:     transform.ClientHints{Width: true},
			resolved: transform.Resize{Width: transform.Dim{Px: 1150}},
		},
		{
			name:     "w_auto_step",
			tx:       "w_auto:100",
			want:     transform.ClientHints{Width: true, WidthStep: 100},
			resolved: transform.Resize{Width: transform.Dim{Px: 1200}},
		},
		{
			name: "w_auto_fallback",
			tx:   "c_limit,w_auto:100:400",
			want: transform.ClientHints{Width: true, WidthStep: 100},
			// The hint replaces the fallback width.
			resolved: transform.Resize{Type: transform.ResizeFit, Width: transform.Dim{Px: 1200}},
		},
		{
			name:     "dpr_auto",
			tx:       "c_scale,w_300,dpr_auto",
			want:     transform.ClientHints{DPR: true},
			resolved: transform.Resize{Type: transform.ResizeFit, Enlarge: true, Width: transform.Dim{Px: 300}, DPR: 2},
		},
		{
			name: "w_auto_dpr_auto",
			tx:   "c_limit,w_auto/dpr_auto",
			want: transform.ClientHints{Width: true, DPR: true},
			// Physical width hint ÷ DPR hint = CSS px.
			resolved: transform.Resize{Type: transform.ResizeFit, Width: transform.Dim{Px: 575}, DPR: 2},
		},
		{
			name:     "explicit_values",
			tx:       "c_scale,w_300,dpr_2.0",
			want:     transform.ClientHints{},
			resolved: transform.Resize{Type: transform.ResizeFit, Enlarge: true, Width: transform.Dim{Px: 300}, DPR: 2},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := parse(t, "", up+tc.tx+"/sample.jpg")
			if err != nil {
				t.Fatal(err)
			}
			req := res.Request
			if d := cmp.Diff(tc.want, req.ClientHints); d != "" {
				t.Errorf("ClientHints (-want +got):\n%s", d)
			}
			cfg := negotiate.DefaultConfig()
			negotiate.ApplyClientHints(req, hints, &cfg, negotiate.HintsAll)
			if len(req.Pipelines) == 0 {
				t.Fatal("no pipeline after ApplyClientHints")
			}
			if d := cmp.Diff(tc.resolved, req.Pipelines[0].Resize); d != "" {
				t.Errorf("resolved Resize (-want +got):\n%s", d)
			}
		})
	}
}
