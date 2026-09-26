package cloudflare_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestClientHints checks that width=auto opts into the width client hint
// exactly like imgix ch=Width does (and slow-connection-quality into
// Save-Data), and that negotiate.ApplyClientHints resolves it into the
// pipeline's Resize.
func TestClientHints(t *testing.T) {
	t.Parallel()
	// A 2x client whose layout slot is 575 CSS px wide.
	hints := negotiate.ClientHints{DPR: 2, Width: 1150}
	tests := []struct {
		name     string
		options  string
		want     transform.ClientHints
		resolved transform.Resize // Pipelines[0].Resize after ApplyClientHints
	}{
		{
			name:     "width_auto",
			options:  "width=auto",
			want:     transform.ClientHints{Width: true},
			resolved: transform.Resize{Width: transform.Dim{Px: 1150}},
		},
		{
			name:    "width_auto_dpr",
			options: "w=auto,dpr=2,fit=scale-down",
			want:    transform.ClientHints{Width: true},
			// Physical width hint ÷ the URL's DPR = CSS px.
			resolved: transform.Resize{Type: transform.ResizeFit, Width: transform.Dim{Px: 575}, DPR: 2},
		},
		{
			name:     "width_px_wins",
			options:  "width=auto,width=300",
			want:     transform.ClientHints{},
			resolved: transform.Resize{Width: transform.Dim{Px: 300}},
		},
		{
			name:     "height_only",
			options:  "height=200",
			want:     transform.ClientHints{},
			resolved: transform.Resize{Height: transform.Dim{Px: 200}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := parse(t, "", opt(tc.options), "")
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
