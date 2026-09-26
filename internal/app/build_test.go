package app

import (
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/server"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestServerConfigSpecialPaths: XI_FALLBACK_* and the debug-header switch
// reach the server (WP-34c).
func TestServerConfigSpecialPaths(t *testing.T) {
	t.Parallel()
	cfg, _, err := config.Load(config.LoadOptions{Environ: []string{
		"XI_DEV_MODE=true",
		"XI_FALLBACK_SOURCE=local:///fallback.png",
		"XI_FALLBACK_STATUS=404",
		"XI_FALLBACK_TTL=30s",
		"XI_FALLBACK_ALLOW_REQUEST_URL=true",
		"XI_OBSERVABILITY_DEBUG_HEADERS=true",
	}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	sc := serverConfig(&cfg)
	want := server.Fallback{Source: "local:///fallback.png", Status: 404, TTL: 30 * time.Second, AllowRequestURL: true}
	if diff := cmp.Diff(want, sc.Fallback); diff != "" {
		t.Errorf("fallback (-want +got):\n%s", diff)
	}
	if !sc.DebugHeaders {
		t.Error("debug headers not mapped")
	}

	def, _, err := config.Load(config.LoadOptions{Environ: []string{"XI_DEV_MODE=true"}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	if got := serverConfig(&def).Fallback; got != (server.Fallback{Status: 200, TTL: time.Minute}) {
		t.Errorf("default fallback = %+v", got)
	}
}

// TestEncoderConfigAutoQuality: XI_FORMATS_AUTOQUALITY_* reach the encoder,
// and the documented defaults equal the encoder's own.
func TestEncoderConfigAutoQuality(t *testing.T) {
	t.Parallel()
	def, _, err := config.Load(config.LoadOptions{Environ: []string{"XI_DEV_MODE=true"}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(encode.DefaultAutoQuality(), encoderConfig(&def).AutoQuality); diff != "" {
		t.Errorf("defaults (-encode +config):\n%s", diff)
	}

	cfg, _, err := config.Load(config.LoadOptions{Environ: []string{
		"XI_DEV_MODE=true",
		"XI_FORMATS_AUTOQUALITY_METHOD=dssim",
		"XI_FORMATS_AUTOQUALITY_TARGET=0.015",
		"XI_FORMATS_AUTOQUALITY_MIN=50",
		"XI_FORMATS_AUTOQUALITY_MAX=88",
		"XI_FORMATS_AUTOQUALITY_ALLOWED_ERROR=0.002",
		"XI_FORMATS_AUTOQUALITY_FORMAT_MIN=jpeg=45,avif=35",
		"XI_FORMATS_AUTOQUALITY_FORMAT_MAX=avif=70",
		"XI_FORMATS_AUTOQUALITY_FORMAT_TARGET=webp=0.01",
	}, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	want := encode.AutoQualityConfig{
		Method: transform.AutoQualityDSSIM, Target: 0.015, Min: 50, Max: 88, AllowedError: 0.002,
		FormatTarget: map[transform.Format]float64{transform.FormatWebP: 0.01},
		FormatMin:    map[transform.Format]int{transform.FormatJPEG: 45, transform.FormatAVIF: 35},
		FormatMax:    map[transform.Format]int{transform.FormatAVIF: 70},
	}
	ec := encoderConfig(&cfg)
	if diff := cmp.Diff(want, ec.AutoQuality); diff != "" {
		t.Errorf("mapped (-want +got):\n%s", diff)
	}
	if err := ec.Validate(); err != nil {
		t.Errorf("mapped config invalid: %v", err)
	}
}
