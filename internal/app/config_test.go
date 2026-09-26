package app

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestConfigFromEnv(t *testing.T) {
	t.Parallel()

	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}

	t.Run("defaults", func(t *testing.T) {
		t.Parallel()
		c, err := ConfigFromEnv(env(nil))
		if err != nil {
			t.Fatal(err)
		}
		if c.DevMode || c.ServerAddr != ":8080" || c.AdminAddr != "127.0.0.1:9090" ||
			c.ServerRequestTimeout != 20*time.Second || c.ServerWriteTimeout != 25*time.Second ||
			c.VipsConcurrency != 1 || c.JPEGQuality != 80 || c.LimitsMaxSrcBytes != 50<<20 ||
			c.ProcessingQueueSize != c.ProcessingWorkers*16 || c.LogLevel != slog.LevelInfo {
			t.Errorf("unexpected defaults: %+v", c)
		}
	})

	t.Run("overrides", func(t *testing.T) {
		t.Parallel()
		c, err := ConfigFromEnv(env(map[string]string{
			"XI_DEV_MODE":                     "true",
			"XI_SERVER_ADDR":                  "127.0.0.1:0",
			"XI_SERVER_REQUEST_TIMEOUT":       "3s",
			"XI_ADMIN_ADDR":                   "off",
			"XI_FETCH_ALLOW_PRIVATE_NETWORKS": "1",
			"XI_PROCESSING_WORKERS":           "2",
			"XI_OBSERVABILITY_LOG_LEVEL":      "debug",
		}))
		if err != nil {
			t.Fatal(err)
		}
		if !c.DevMode || c.ServerAddr != "127.0.0.1:0" || c.AdminAddr != "" || !c.FetchAllowPrivateNetworks ||
			c.ServerWriteTimeout != 8*time.Second || c.ProcessingQueueSize != 32 || c.LogLevel != slog.LevelDebug {
			t.Errorf("unexpected config: %+v", c)
		}
	})

	t.Run("every malformed value is reported", func(t *testing.T) {
		t.Parallel()
		_, err := ConfigFromEnv(env(map[string]string{
			"XI_DEV_MODE":             "maybe",
			"XI_FETCH_TIMEOUT":        "ten",
			"XI_FORMATS_JPEG_QUALITY": "x",
		}))
		if err == nil {
			t.Fatal("want an error")
		}
		for _, key := range []string{"XI_DEV_MODE", "XI_FETCH_TIMEOUT", "XI_FORMATS_JPEG_QUALITY"} {
			if !strings.Contains(err.Error(), key) {
				t.Errorf("error %q does not mention %s", err, key)
			}
		}
	})

	t.Run("range violations", func(t *testing.T) {
		t.Parallel()
		_, err := ConfigFromEnv(env(map[string]string{"XI_FORMATS_JPEG_QUALITY": "101", "XI_LIMITS_MAX_SRC_BYTES": "0"}))
		if err == nil || !strings.Contains(err.Error(), "QUALITY") || !strings.Contains(err.Error(), "MAX_SRC_BYTES") {
			t.Errorf("error = %v, want both violations", err)
		}
	})
}
