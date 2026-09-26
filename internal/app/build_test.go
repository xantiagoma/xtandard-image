package app

import (
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/server"
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
