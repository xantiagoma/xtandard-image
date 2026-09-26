package config

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// TestRedaction loads a config with a distinct value in every secret field
// and proves that none of those bytes, raw or hex, appear in any printed
// form of it.
func TestRedaction(t *testing.T) {
	t.Parallel()
	const (
		signKey = "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"
		salt    = "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2"
		encKey  = "c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3"
	)
	secrets := []string{
		"trusted-sig-secret", "bearer-secret-value", "s3-secret-key-value", "gcs-json-secret",
		"azure-account-key-secret", "azure-conn-string-secret", "sftp-password-secret",
		"sftp-private-key-secret", "imgix-token-secret", "redis-pw-secret", "etag-buster-secret",
		"header-secret-value",
	}
	env := []string{
		"XI_SIGNING_KEYS=" + signKey, "XI_SIGNING_SALTS=" + salt, "XI_ENCRYPTION_KEYS=" + encKey,
		"XI_SIGNING_TRUSTED=trusted-sig-secret",
		"XI_SECURITY_SECRET=bearer-secret-value",
		"XI_SOURCES=s3,gcs,az,ftp,web",
		"XI_SOURCES_S3_TYPE=s3", "XI_SOURCES_S3_BUCKETS=b", "XI_SOURCES_S3_SECRET_ACCESS_KEY=s3-secret-key-value",
		"XI_SOURCES_GCS_TYPE=gcs", "XI_SOURCES_GCS_BUCKETS=b", "XI_SOURCES_GCS_CREDENTIALS_JSON=gcs-json-secret",
		"XI_SOURCES_AZ_TYPE=azure", "XI_SOURCES_AZ_BUCKETS=b", "XI_SOURCES_AZ_ACCOUNT_KEY=azure-account-key-secret",
		"XI_SOURCES_AZ_CONNECTION_STRING=azure-conn-string-secret",
		"XI_SOURCES_FTP_TYPE=sftp", "XI_SOURCES_FTP_HOST=h", "XI_SOURCES_FTP_HOST_KEY=k",
		"XI_SOURCES_FTP_PASSWORD=sftp-password-secret", "XI_SOURCES_FTP_PRIVATE_KEY=sftp-private-key-secret",
		"XI_SOURCES_WEB_TYPE=http", "XI_SOURCES_WEB_ALLOW=a.example", "XI_SOURCES_WEB_HEADERS=Authorization=header-secret-value",
		"XI_DIALECTS_IMGIX_KEYS=imgix-token-secret",
		"XI_CACHE_RESULT_REDIS_URL=redis://:redis-pw-secret@r:6379",
		"XI_CACHE_ETAG_BUSTER=etag-buster-secret",
	}
	cfg, _, err := Load(opts(nil, env...))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// The secrets really are loaded.
	if cfg.Sources["web"].Headers["Authorization"].Reveal() != "header-secret-value" || cfg.Signing.Keys[0].Hex() != signKey {
		t.Fatal("secrets not loaded")
	}

	var logBuf bytes.Buffer
	slog.New(slog.NewJSONHandler(&logBuf, nil)).Info("cfg",
		slog.Any("keys", cfg.Signing.Keys), slog.Any("secret", cfg.Security.Secret), slog.Any("enc", cfg.Encryption.Keys[0]))
	jsonOut, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	outputs := map[string]string{
		"Redacted": cfg.Redacted(),
		"String":   cfg.String(),
		"%v":       fmt.Sprintf("%v", &cfg),
		"%+v":      fmt.Sprintf("%+v", cfg.Sources),
		"%#v":      fmt.Sprintf("%#v", cfg.Signing),
		"%x":       fmt.Sprintf("%x %x %x", cfg.Signing.Keys, cfg.Security.Secret, cfg.Encryption.Keys[0]),
		"%d":       fmt.Sprintf("%d", cfg.Signing.Salts[0]),
		"%s":       fmt.Sprintf("%s %q", cfg.Cache.Result.Redis.URL, cfg.Sources["s3"].SecretAccessKey),
		"slog":     logBuf.String(),
		"json":     string(jsonOut),
	}
	needles := append(make([]string, 0, len(secrets)+9), secrets...)
	for _, h := range []string{signKey, salt, encKey} {
		b, _ := hex.DecodeString(h)
		needles = append(needles, h, string(b), fmt.Sprintf("%d", b))
	}
	for name, out := range outputs {
		for _, n := range needles {
			if strings.Contains(out, n) {
				t.Errorf("%s output contains secret %q:\n%s", name, n, out)
			}
		}
		if !strings.Contains(out, redacted) {
			t.Errorf("%s output has no %s marker", name, redacted)
		}
	}
	// Non-secret settings stay visible.
	out := cfg.Redacted()
	for _, want := range []string{"addr: :8080", "allowed_qualities: 5-100/5", "cors_origins: []", "max_src_bytes: 50MiB", "request_timeout: 20s", "host_key: k"} {
		if !strings.Contains(out, want) {
			t.Errorf("Redacted output lacks %q:\n%s", want, out)
		}
	}
}
