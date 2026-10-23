package config

import (
	"bytes"
	"cmp"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// maxDPR is the largest device pixel ratio accepted anywhere (client hints
// use the same bound).
const maxDPR = 8

// validator collects violations; every one names its YAML path and variable.
type validator struct{ errs []error }

func (v *validator) add(path, format string, args ...any) {
	v.errs = append(v.errs, fmt.Errorf("%w: %s (%s): %s", ErrInvalid, path, envNameOf(path), fmt.Sprintf(format, args...)))
}

func (v *validator) check(ok bool, path, format string, args ...any) {
	if !ok {
		v.add(path, format, args...)
	}
}

func (v *validator) positive(n int64, path string) {
	v.check(n > 0, path, "must be > 0, got %d", n)
}

func (v *validator) nonNegative(n int64, path string) {
	v.check(n >= 0, path, "must be >= 0, got %d", n)
}

func (v *validator) between(n, lo, hi int, path string) {
	v.check(n >= lo && n <= hi, path, "must be between %d and %d, got %d", lo, hi, n)
}

// Validate reports every violation, joined with errors.Join. Each error
// wraps ErrInvalid.
func (c *Config) Validate() error {
	var v validator
	c.validateServer(&v)
	c.validateSecurity(&v)
	c.validateSources(&v)
	c.validateFetch(&v)
	c.validateDialects(&v)
	c.validateProcessing(&v)
	c.validateFormats(&v)
	c.validateLimits(&v)
	c.validateVideo(&v)
	c.validateCache(&v)
	c.validateDetect(&v)
	return errors.Join(v.errs...)
}

func (c *Config) validateVideoTransform(v *validator) {
	t := c.Video.Transform
	v.check(slices.Contains(roles(), c.Role), "role", "must be all, image or video, got %q", c.Role)
	v.check(c.Role != RoleVideo || t.Enabled, "role", "video needs video.transform.enabled")
	v.check(t.Workers >= 1 && t.Workers <= 1024, "video.transform.workers", "must be between 1 and 1024, got %d", t.Workers)
	v.between(t.Queue, 0, 1_000_000, "video.transform.queue")
	v.nonNegative(t.Capacity, "video.transform.capacity")
	v.between(t.MaxJobs, 0, 4096, "video.transform.max_jobs")
	v.nonNegative(t.QueueCost, "video.transform.queue_cost")
	v.nonNegative(int64(t.QueueTimeout), "video.transform.queue_timeout")
	v.positive(int64(t.Timeout), "video.transform.timeout")
	v.between(t.Threads, 1, 64, "video.transform.threads")
	v.positive(int64(t.MaxInputBytes), "video.transform.max_input_bytes")
	v.positive(int64(t.MaxInputDuration), "video.transform.max_input_duration")
	v.positive(t.MaxInputPixels, "video.transform.max_input_pixels")
	v.positive(int64(t.MaxOutputBytes), "video.transform.max_output_bytes")
	v.positive(int64(t.MaxOutputDuration), "video.transform.max_output_duration")
	v.positive(t.MaxOutputPixels, "video.transform.max_output_pixels")
	v.check(t.MaxFPS >= 1 && t.MaxFPS <= 240, "video.transform.max_fps", "must be between 1 and 240, got %g", t.MaxFPS)
	v.check(slices.Contains(unsignedPolicies(), t.UnsignedPolicy),
		"video.transform.unsigned_policy", "must be deny, allow or restricted, got %q", t.UnsignedPolicy)
	c.validateVideoPresets(v)
	v.check(len(t.ABRLadder) >= 1 && len(t.ABRLadder) <= 8, "video.transform.abr_ladder", "must list 1 to 8 rungs, got %d", len(t.ABRLadder))
	seen := map[int]bool{}
	for _, item := range t.ABRLadder {
		h, _, err := ParseABRRung(item)
		v.check(err == nil, "video.transform.abr_ladder", "%v", err)
		v.check(err != nil || !seen[h], "video.transform.abr_ladder", "rung %d listed twice", h)
		seen[h] = true
	}
	v.check(t.SegmentDuration >= time.Second && t.SegmentDuration <= 30*time.Second && t.SegmentDuration%time.Second == 0,
		"video.transform.segment_duration", "must be whole seconds from 1s to 30s, got %s", t.SegmentDuration)
	v.check(t.AudioChunk == 0 || (t.AudioChunk >= time.Second && t.AudioChunk <= time.Hour && t.AudioChunk%time.Second == 0),
		"video.transform.audio_chunk", "must be 0 or whole seconds from 1s to 1h, got %s", t.AudioChunk)
	v.between(t.PrefetchSegments, 0, 16, "video.transform.prefetch_segments")
	v.check(t.StreamTTL >= time.Minute, "video.transform.stream_ttl", "must be at least 1m, got %s", t.StreamTTL)
	for i, k := range t.StreamKeys {
		v.check(len(k) >= 16, "video.transform.stream_keys", "key %d is %d bytes, want at least 16", i, len(k))
	}
	v.nonNegative(int64(t.StreamCacheBytes), "video.transform.stream_cache_bytes")
	v.check(t.SpoolDir == "" || filepath.IsAbs(t.SpoolDir), "video.transform.spool_dir", "must be an absolute path, got %q", t.SpoolDir)
	v.nonNegative(int64(t.SpoolThreshold), "video.transform.spool_threshold")
	v.nonNegative(int64(t.SpoolMaxBytes), "video.transform.spool_max_bytes")
	v.check(t.SpoolMaxBytes == 0 || t.SpoolThreshold > 0, "video.transform.spool_max_bytes", "needs video.transform.spool_threshold > 0")
	v.nonNegative(int64(t.SpoolTTL), "video.transform.spool_ttl")
}

// VideoUnsignedOptions are the values of
// video.transform.unsigned_allowed_options.
func VideoUnsignedOptions() []string { return []string{"start", "end", "duration", "mute"} }

// videoContainers are the container names of video transformations
// (transform.VideoContainer).
func videoContainers() []string {
	return []string{"mp4", "webm", "m4a", "mp3", "opus", "ogg", "flac", "wav", "png", "webp"}
}

// validateVideoPresets checks the video presets' syntax and the restricted
// policy's settings. What each option means is checked when the dialects
// are built (internal/app), which fails startup too.
func (c *Config) validateVideoPresets(v *validator) {
	t := c.Video.Transform
	const path = "video.transform.presets"
	sep := cmp.Or(c.Dialects.Imgproxy.ArgumentsSeparator, ":")
	for _, name := range sortedKeys(t.Presets) {
		v.check(name != "" && !strings.ContainsAny(name, "/,= ") && !strings.Contains(name, sep), path, "bad video preset name %q", name)
		value := strings.TrimSpace(t.Presets[name])
		if value == "" {
			v.add(path, "video preset %q is empty", name)
			continue
		}
		for seg := range strings.SplitSeq(value, "/") {
			if seg == "-" {
				v.add(path, "video preset %q: chained pipelines are not video options", name)
				break
			}
			if opt, _, ok := strings.Cut(seg, sep); !ok || opt == "" {
				v.add(path, "video preset %q: %q is not an option (name%sargs)", name, truncateValue(seg), sep)
				break
			}
		}
	}
	for i, o := range t.UnsignedAllowedOptions {
		v.check(slices.Contains(VideoUnsignedOptions(), o), fmt.Sprintf("video.transform.unsigned_allowed_options[%d]", i),
			"%q is not one of %s", o, strings.Join(VideoUnsignedOptions(), ", "))
	}
	for i, f := range t.UnsignedFormats {
		v.check(slices.Contains(videoContainers(), f), fmt.Sprintf("video.transform.unsigned_formats[%d]", i),
			"%q is not one of %s", f, strings.Join(videoContainers(), ", "))
	}
	v.nonNegative(int64(t.UnsignedMaxDuration), "video.transform.unsigned_max_duration")
	v.check(t.UnsignedPolicy != UnsignedRestricted || len(t.Presets) > 0, "video.transform.unsigned_policy",
		"restricted needs video.transform.presets")
}

// truncateValue bounds a configuration value quoted in an error.
func truncateValue(s string) string {
	const maxQuoted = 64
	if len(s) > maxQuoted {
		return s[:maxQuoted]
	}
	return s
}

func (c *Config) validateServer(v *validator) {
	s := c.Server
	v.check(s.Addr != "", "server.addr", "must not be empty")
	v.check(s.PathPrefix == "" || strings.HasPrefix(s.PathPrefix, "/"), "server.path_prefix", "must start with /")
	for _, d := range []named[time.Duration]{
		{"server.request_timeout", s.RequestTimeout},
		{"server.read_header_timeout", s.ReadHeaderTimeout},
		{"server.read_timeout", s.ReadTimeout},
		{"server.write_timeout", s.WriteTimeout},
		{"server.idle_timeout", s.IdleTimeout},
		{"server.shutdown_timeout", s.ShutdownTimeout},
	} {
		v.check(d.v > 0, d.path, "must be > 0, got %s", d.v)
	}
	v.check(s.WriteTimeout >= s.RequestTimeout, "server.write_timeout", "must be >= server.request_timeout (%s)", s.RequestTimeout)
	v.nonNegative(int64(s.ShutdownDelay), "server.shutdown_delay")
	v.positive(int64(s.MaxHeaderBytes), "server.max_header_bytes")
	v.positive(int64(s.MaxConnections), "server.max_connections")
	v.between(s.ReadyQueueThreshold, 0, 100, "server.ready_queue_threshold")
	v.positive(int64(s.QueryMaxBodyBytes), "server.query_max_body_bytes")
	v.between(s.QueryLocationMaxLength, 0, 8192, "server.query_location_max_length")
	for i, o := range s.CORSOrigins {
		v.check(o == "*" || validOrigin(o), fmt.Sprintf("server.cors_origins[%d]", i), "%q is not * or scheme://host[:port]", o)
	}
	v.check(!c.Admin.Pprof || c.Admin.Addr != "", "admin.pprof", "needs admin.addr")
	v.check(!c.Admin.Docs || c.Admin.Addr != "", "admin.docs", "needs admin.addr")
	v.check(c.Admin.Addr == "" || c.Admin.Addr != s.Addr, "admin.addr", "must differ from server.addr")
	c.validatePlayground(v)
	v.check(c.Observability.LogLevel != "", "observability.log_level", "must be set")
}

func (c *Config) validatePlayground(v *validator) {
	p := c.Playground
	v.check(!p.Public || p.Enabled, "playground.public", "needs playground.enabled")
	v.check(!p.Uploads.Bool() || p.Enabled, "playground.uploads", "needs playground.enabled")
	v.check(!p.Enabled || p.Public || c.Admin.Addr != "", "playground.enabled", "needs admin.addr (or playground.public)")
	ok := len(p.Path) > 1 && p.Path[0] == '/' && !strings.HasSuffix(p.Path, "/") && !strings.Contains(p.Path, "//")
	for _, r := range p.Path {
		ok = ok && (r == '/' || r == '-' || r == '_' || r == '.' || r == '~' ||
			('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9'))
	}
	v.check(ok, "playground.path", "%q must start with /, not end with /, and use only [A-Za-z0-9._~/-]", p.Path)
	// Admin routes the playground path must not shadow.
	for _, r := range []string{"/healthz", "/readyz", "/metrics", "/openapi.json", "/docs", "/debug", "/config.schema.json"} {
		v.check(p.Path != r && !strings.HasPrefix(p.Path, r+"/"), "playground.path", "%q shadows the admin route %s", p.Path, r)
	}
}

func validOrigin(o string) bool {
	u, err := url.Parse(o)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && (u.Path == "" || u.Path == "/")
}

func (c *Config) validateSecurity(v *validator) {
	s := c.Signing
	v.check(len(s.Keys) == len(s.Salts), "signing.salts", "want one salt per key: %d keys, %d salts", len(s.Keys), len(s.Salts))
	v.between(s.Size, 1, 32, "signing.size")
	v.check(!s.Required.Bool() || len(s.Keys) > 0, "signing.required", "is true but no signing.keys are set")
	for i, k := range s.Trusted {
		v.check(k != "", fmt.Sprintf("signing.trusted[%d]", i), "must not be empty")
	}
	for i, k := range c.Encryption.Keys {
		switch len(k) {
		case 16, 24, 32:
		default:
			v.add(fmt.Sprintf("encryption.keys[%d]", i), "AES key must be 16, 24 or 32 bytes, got %d", len(k))
		}
	}
	v.check(c.Encryption.Key == nil || len(c.Encryption.Keys) == 0, "encryption.key", "is an alias of encryption.keys; set only one")

	sec := c.Security
	v.check(sec.UnsignedPolicy != "", "security.unsigned_policy", "must be set")
	if sec.UnsignedPolicy == UnsignedRestricted {
		v.check(len(sec.AllowedWidths) > 0, "security.allowed_widths", "must not be empty with the restricted policy")
		v.check(len(sec.AllowedQualities) > 0, "security.allowed_qualities", "must not be empty with the restricted policy")
	}
	for _, w := range sec.AllowedWidths {
		if w < 1 || w > c.Limits.MaxResultDimension {
			v.add("security.allowed_widths", "%d is outside 1..limits.max_result_dimension (%d)", w, c.Limits.MaxResultDimension)
			break
		}
	}
	for _, h := range sec.AllowedHeights {
		if h < 1 || h > c.Limits.MaxResultDimension {
			v.add("security.allowed_heights", "%d is outside 1..limits.max_result_dimension (%d)", h, c.Limits.MaxResultDimension)
			break
		}
	}
	for _, d := range sec.AllowedDPRs {
		if !(d > 0 && d <= maxDPR) {
			v.add("security.allowed_dprs", "%g is outside (0, %d]", d, maxDPR)
			break
		}
	}
	for _, q := range sec.AllowedQualities {
		if q < 1 || q > 100 {
			v.add("security.allowed_qualities", "%d is outside 1..100", q)
			break
		}
	}
	v.check(!sec.AllowRaisingLimits || sec.AllowSecurityOptions, "security.allow_raising_limits", "needs security.allow_security_options")
}

// validName reports whether s is a map key name: [a-z0-9][a-z0-9-]*.
func validName(s string) bool {
	if s == "" || s[0] == '-' {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// sourceFieldTypes lists, per YAML field of Source, the types it applies to.
func sourceFieldTypes() map[string][]SourceType {
	blob := []SourceType{SourceS3, SourceGCS, SourceAzure}
	remote := []SourceType{SourceFTP, SourceSFTP}
	return map[string][]SourceType{
		"allow": {SourceHTTP}, "base_url": {SourceHTTP}, "headers": {SourceHTTP},
		"buckets": blob, "bucket": blob,
		"prefix":   {SourceS3, SourceGCS, SourceAzure, SourceFS, SourceFTP, SourceSFTP},
		"endpoint": blob, "region": {SourceS3}, "force_path_style": {SourceS3},
		"access_key_id": {SourceS3}, "secret_access_key": {SourceS3}, "role_arn": {SourceS3},
		"credentials_json": {SourceGCS},
		"account":          {SourceAzure}, "account_key": {SourceAzure}, "connection_string": {SourceAzure}, "sas_token": {SourceAzure},
		"root": {SourceFS, SourceFTP, SourceSFTP}, "follow_symlinks": {SourceFS},
		"host": remote, "port": remote, "user": remote, "password": remote, "tls": {SourceFTP},
		"private_key": {SourceSFTP}, "private_key_passphrase": {SourceSFTP}, "host_key": {SourceSFTP},
		"pool_size": remote, "dial_timeout": remote, "idle_timeout": remote,
		"pdf_password": sourceTypes(),
	}
}

func (c *Config) validateSources(v *validator) {
	v.check(len(c.Sources) > 0 || c.DevMode, "sources", "no sources configured: define sources (XI_SOURCES=…) or set XI_DEV_MODE=true")
	applies := sourceFieldTypes()
	for _, name := range sortedKeys(c.Sources) {
		s, p := c.Sources[name], "sources."+name
		v.check(validName(name), p, "name must match [a-z0-9-]")
		if s.Type == "" {
			v.add(p+".type", "must be set")
			continue
		}
		v.check(validPDFPassword(s.PDFPassword.Reveal()), p+".pdf_password", "must be at most 1024 bytes without control characters")
		rv, rt := reflect.ValueOf(s), reflect.TypeFor[Source]()
		for i := range rt.NumField() {
			tag, _, _ := strings.Cut(rt.Field(i).Tag.Get("yaml"), ",")
			types, ok := applies[tag]
			if ok && !rv.Field(i).IsZero() && !slices.Contains(types, s.Type) {
				v.add(p+"."+tag, "does not apply to type %s", s.Type)
			}
		}
		switch s.Type {
		case SourceHTTP:
			v.check(len(s.Allow) > 0 || s.BaseURL != "", p+".allow", "an http source needs allow patterns or a base_url")
			for i, a := range s.Allow {
				v.check(validAllowPattern(a), fmt.Sprintf("%s.allow[%d]", p, i), "want [http:// or https://]host-glob[:port][/path/prefix/], got %q", a)
			}
			v.check(s.BaseURL == "" || validHTTPURL(s.BaseURL), p+".base_url", "must be an absolute http(s) URL")
		case SourceS3, SourceGCS, SourceAzure:
			v.check(len(s.Buckets) > 0 || s.Bucket != "", p+".buckets", "must list at least one bucket")
			v.check(s.Bucket == "" || len(s.Buckets) == 0 || slices.Contains(s.Buckets, s.Bucket), p+".bucket", "%q is not in buckets", s.Bucket)
			v.check(s.Endpoint == "" || validHTTPURL(s.Endpoint), p+".endpoint", "must be an absolute http(s) URL")
			if s.Type == SourceAzure {
				validateAzure(v, p, &s)
			}
		case SourceFS:
			v.check(filepath.IsAbs(s.Root), p+".root", "must be an absolute path")
		case SourceFTP, SourceSFTP:
			v.check(s.Host != "", p+".host", "must be set")
			v.check(!strings.ContainsAny(s.Host, "/ \t\r\n"), p+".host", "must be a host name or IP address without scheme, path or spaces, got %q", s.Host)
			v.between(s.Port, 0, 65535, p+".port")
			if s.Type == SourceSFTP {
				validateSFTP(v, p, &s)
			}
			v.check(s.PoolSize >= 0, p+".pool_size", "must not be negative")
			v.check(s.DialTimeout >= 0, p+".dial_timeout", "must not be negative")
			v.check(s.IdleTimeout >= 0, p+".idle_timeout", "must not be negative")
			v.check(s.PrivateKeyPassphrase == "" || s.PrivateKey != "", p+".private_key_passphrase", "needs private_key")
		}
	}
}

// validateAzure checks what azuresrc.New and the Azure SDK would refuse at
// startup, plus the account and container naming rules the service
// enforces (Azure: "Naming and referencing containers, blobs, and
// metadata").
func validateAzure(v *validator, p string, s *Source) {
	cs := s.ConnectionString.Reveal()
	v.check(s.Account != "" || s.Endpoint != "" || cs != "", p+".account",
		"an azure source needs account, endpoint or connection_string")
	v.check(s.Account == "" || validAzureAccount(s.Account), p+".account",
		"must be a storage account name: 3-24 lowercase letters and digits, got %q", s.Account)
	const containerRule = "must be a container name: 3-63 lowercase letters, digits and single hyphens, starting and ending with a letter or digit, or $root / $web, got %q"
	for i, b := range s.Buckets {
		v.check(validAzureContainer(b), fmt.Sprintf("%s.buckets[%d]", p, i), containerRule, b)
	}
	v.check(len(s.Buckets) > 0 || s.Bucket == "" || validAzureContainer(s.Bucket), p+".bucket", containerRule, s.Bucket)
	if cs != "" {
		v.check(s.Account == "" && s.AccountKey == "" && s.SASToken == "" && s.Endpoint == "", p+".connection_string",
			"carries the endpoint and credentials itself: do not combine it with account, account_key, sas_token or endpoint")
		if err := checkAzureConnectionString(cs); err != nil {
			v.add(p+".connection_string", "%v", err)
		}
		return
	}
	v.check(s.AccountKey == "" || s.SASToken == "", p+".sas_token", "account_key and sas_token are exclusive: set one")
	if s.AccountKey != "" {
		v.check(s.Account != "", p+".account_key", "needs account (the shared key signs for a named account)")
		_, err := base64.StdEncoding.DecodeString(s.AccountKey.Reveal())
		v.check(err == nil, p+".account_key", "must be the base64 key from the storage account's access keys")
	}
	if s.SASToken != "" {
		v.check(!strings.Contains(s.Endpoint, "?"), p+".endpoint", "must not carry a query when sas_token is set")
		v.check(!strings.Contains(s.SASToken.Reveal(), "://"), p+".sas_token", "must be the token (sv=…&sig=…), not a SAS URL")
	}
}

// validAzureAccount is the storage account naming rule: 3-24 lowercase
// letters and digits.
func validAzureAccount(s string) bool {
	if len(s) < 3 || len(s) > 24 {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// validAzureContainer is the container naming rule: 3-63 characters of
// lowercase letters, digits and hyphens, starting and ending with a letter
// or digit, no consecutive hyphens; $root and $web are the special
// containers.
func validAzureContainer(s string) bool {
	if s == "$root" || s == "$web" {
		return true
	}
	if len(s) < 3 || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' || strings.Contains(s, "--") {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// checkAzureConnectionString applies the Azure SDK's parser rules
// (azblob ParseConnectionString): Key=Value pairs separated by ';', with
// BlobEndpoint or AccountName, and AccountName+AccountKey or
// SharedAccessSignature. UseDevelopmentStorage=true is not understood by
// the Go SDK: Azurite needs the explicit form.
func checkAzureConnectionString(cs string) error {
	kv := map[string]string{}
	for part := range strings.SplitSeq(strings.TrimRight(cs, ";"), ";") {
		k, val, ok := strings.Cut(part, "=")
		if !ok {
			return errors.New("must be Key=Value pairs separated by ';' (copy it from the storage account's access keys)")
		}
		kv[strings.TrimSpace(k)] = val
	}
	_, hasName := kv["AccountName"]
	_, hasKey := kv["AccountKey"]
	_, hasSAS := kv["SharedAccessSignature"]
	blob, hasBlob := kv["BlobEndpoint"]
	switch {
	case !hasName && !hasBlob:
		if _, dev := kv["UseDevelopmentStorage"]; dev {
			return errors.New("UseDevelopmentStorage=true is not supported: give Azurite's AccountName, AccountKey and BlobEndpoint")
		}
		return errors.New("needs AccountName or BlobEndpoint")
	case (!hasName || !hasKey) && !hasSAS:
		return errors.New("needs AccountName with AccountKey, or SharedAccessSignature")
	case hasBlob && !validHTTPURL(blob):
		return errors.New("BlobEndpoint must be an absolute http(s) URL")
	case hasKey:
		if _, err := base64.StdEncoding.DecodeString(kv["AccountKey"]); err != nil {
			return errors.New("AccountKey must be base64")
		}
	}
	return nil
}

// validateSFTP checks the credentials and pinned host keys sftpsrc.New
// parses at startup.
func validateSFTP(v *validator, p string, s *Source) {
	v.check(s.User != "", p+".user", "must be set for sftp")
	v.check(s.Password != "" || s.PrivateKey != "", p+".password", "an sftp source needs a password or a private_key")
	if s.HostKey == "" {
		v.add(p+".host_key", "is mandatory for sftp (no trust on first use): paste the server's key, e.g. the output of ssh-keyscan -p PORT HOST")
	} else if err := checkHostKeys(s.HostKey); err != nil {
		v.add(p+".host_key", "%v", err)
	}
	if s.PrivateKey != "" {
		if err := checkPrivateKey(s.PrivateKey.Reveal(), s.PrivateKeyPassphrase.Reveal()); err != nil {
			v.add(p+".private_key", "%v", err)
		}
	}
}

// checkHostKeys checks pinned host keys as sftpsrc reads them: one per
// non-empty, non-comment line, in authorized_keys ("ssh-ed25519 AAAA…") or
// known_hosts ("host ssh-ed25519 AAAA…") form. config imports no SSH
// library (ARCHITECTURE §3), so this is the structural part of that parse:
// a known key type followed by base64 wire data that names the same type;
// sftpsrc still parses the keys themselves at startup.
func checkHostKeys(keys string) error {
	n, i := 0, 0
	for line := range strings.Lines(keys) {
		i++
		if line = strings.TrimSpace(line); line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !hostKeyLine(line) {
			return fmt.Errorf("line %d is not an SSH public key in authorized_keys or known_hosts form (ssh-keyscan output)", i)
		}
		n++
	}
	if n == 0 {
		return errors.New("has no key: paste the server's key, e.g. the output of ssh-keyscan -p PORT HOST")
	}
	return nil
}

// hostKeyLine reports whether line holds "<type> <base64>" whose wire
// encoding starts with the same type string (RFC 4253 §6.6).
func hostKeyLine(line string) bool {
	fields := strings.Fields(line)
	for i := 0; i+1 < len(fields); i++ {
		if !sshKeyType(fields[i]) {
			continue
		}
		blob, err := base64.StdEncoding.DecodeString(fields[i+1])
		if err != nil {
			return false
		}
		typ, rest, ok := sshWireString(blob)
		return ok && string(typ) == fields[i] && len(rest) > 0
	}
	return false
}

// sshWireString splits an SSH wire-format string (uint32 length, bytes)
// off the front of b.
func sshWireString(b []byte) (s, rest []byte, ok bool) {
	if len(b) < 4 {
		return nil, nil, false
	}
	n := int64(binary.BigEndian.Uint32(b))
	if n > int64(len(b)-4) {
		return nil, nil, false
	}
	return b[4 : 4+n], b[4+n:], true
}

// sshKeyType reports whether t is a public key algorithm x/crypto/ssh
// parses, certificates included.
func sshKeyType(t string) bool {
	switch strings.TrimSuffix(t, "-cert-v01@openssh.com") {
	case "ssh-ed25519", "ssh-rsa", "ssh-dss", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521",
		"sk-ssh-ed25519@openssh.com", "sk-ecdsa-sha2-nistp256@openssh.com":
		return true
	}
	return false
}

// checkPrivateKey checks an SSH private key as x/crypto/ssh, which sftpsrc
// uses, would read it: a PEM block of a type it supports, decoded with the
// standard library where it can be (PKCS #1, PKCS #8, SEC 1), and a
// passphrase exactly when the key is encrypted (an OpenSSH key whose
// cipher is not "none", or a legacy PEM block with Proc-Type ENCRYPTED).
// A wrong passphrase is only found at startup.
func checkPrivateKey(key, passphrase string) error {
	const notKey = "is not an SSH private key (PEM or OpenSSH format; with a _FILE variable or {file: …}, the file's content)"
	block, _ := pem.Decode([]byte(key))
	if block == nil {
		return errors.New(notKey)
	}
	encrypted := strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED")
	var err error
	switch block.Type {
	case "OPENSSH PRIVATE KEY":
		encrypted, err = opensshEncrypted(block.Bytes)
	case "RSA PRIVATE KEY":
		if !encrypted {
			_, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		}
	case "EC PRIVATE KEY":
		if !encrypted {
			_, err = x509.ParseECPrivateKey(block.Bytes)
		}
	case "PRIVATE KEY":
		_, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "DSA PRIVATE KEY":
	case "ENCRYPTED PRIVATE KEY":
		return errors.New("is an encrypted PKCS #8 key, which the SSH client cannot read: convert it with ssh-keygen -p -f KEY (OpenSSH format)")
	default:
		return errors.New(notKey)
	}
	switch {
	case err != nil:
		return errors.New(notKey)
	case encrypted && passphrase == "":
		return errors.New("is encrypted: set private_key_passphrase")
	case !encrypted && passphrase != "":
		return errors.New("is not encrypted: remove private_key_passphrase")
	}
	return nil
}

// opensshEncrypted reads the header of an "openssh-key-v1" private key
// (OpenSSH PROTOCOL.key): the magic, then the cipher name, which is "none"
// for an unencrypted key.
func opensshEncrypted(b []byte) (bool, error) {
	const magic = "openssh-key-v1\x00"
	rest, ok := bytes.CutPrefix(b, []byte(magic))
	if !ok {
		return false, errors.New("not an openssh-key-v1 key")
	}
	cipher, _, ok := sshWireString(rest)
	if !ok || len(cipher) == 0 {
		return false, errors.New("bad cipher name")
	}
	return string(cipher) != "none", nil
}

// validPDFPassword checks a source's default document password: what a
// pdf_password token may carry (crypt refuses control characters; the
// transform package caps the length at 1024 bytes).
func validPDFPassword(pw string) bool {
	if len(pw) > 1024 || !utf8.ValidString(pw) {
		return false
	}
	for i := range len(pw) {
		if c := pw[i]; c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// validAllowPattern is the syntactic check of an http allow pattern: an
// optional http:// or https:// scheme (any case) followed by a non-empty
// host glob; any other scheme is rejected. internal/source compiles the
// pattern (host glob, port, path prefix) at startup.
func validAllowPattern(a string) bool {
	rest := a
	for _, scheme := range []string{"https://", "http://"} {
		if len(a) >= len(scheme) && strings.EqualFold(a[:len(scheme)], scheme) {
			rest = a[len(scheme):]
			break
		}
	}
	return rest != "" && rest[0] != '/' && !strings.Contains(rest, "://")
}

func validHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func (c *Config) validateFetch(v *validator) {
	f := c.Fetch
	v.positive(int64(f.Timeout), "fetch.timeout")
	v.between(f.MaxRedirects, 0, 20, "fetch.max_redirects")
	v.positive(int64(f.MaxConcurrent), "fetch.max_concurrent")
	v.check(len(f.AllowedPorts) > 0, "fetch.allowed_ports", "must not be empty")
	for _, p := range f.AllowedPorts {
		if p < 1 || p > 65535 {
			v.add("fetch.allowed_ports", "%d is not a port", p)
		}
	}
}

// mounts returns every mount of the router by YAML path.
func (d *Dialects) mounts() map[string]Mount {
	return map[string]Mount{
		"imgproxy": d.Imgproxy.Mount, "query": d.Query, "wsrv": d.Wsrv,
		"cloudflare": d.Cloudflare, "cloudflare_images": d.CloudflareImages.Mount,
		"imgix": d.Imgix, "cloudinary": d.Cloudinary.Mount, "imagekit": d.Imagekit.Mount,
		"bunny": d.Bunny, "thumbor": d.Thumbor, "awssih": d.AWSSIH.Mount,
	}
}

func (c *Config) validateDialects(v *validator) {
	d := c.Dialects
	ms := d.mounts()
	if def, ok := ms[string(d.Default)]; !ok {
		v.add("dialects.default", "must be a dialect name")
	} else {
		v.check(def.Enabled, "dialects.default", "dialect %s is not enabled", d.Default)
	}
	checkMount := func(p string, o MountOptions) {
		v.check(o.Prefix == "" || strings.HasPrefix(o.Prefix, "/"), p+".prefix", "must start with /")
		v.check(o.Source == "" || c.hasSource(o.Source), p+".source", "source %q is not configured", o.Source)
		for i, h := range o.Hosts {
			v.check(validHost(h), fmt.Sprintf("%s.hosts[%d]", p, i), "%q is not a host name", h)
		}
	}
	for _, name := range sortedKeys(ms) {
		m, p := ms[name], "dialects."+name
		checkMount(p, m.MountOptions)
		for i, extra := range m.Mounts {
			checkMount(fmt.Sprintf("%s.mounts[%d]", p, i), extra)
			v.check(extra.Prefix != "" || len(extra.Hosts) > 0, fmt.Sprintf("%s.mounts[%d]", p, i), "needs a prefix or hosts")
		}
	}
	v.check(d.Imgproxy.ArgumentsSeparator != "", "dialects.imgproxy.arguments_separator", "must not be empty")
	v.check(d.Imgproxy.BaseURL == "" || validHTTPURL(d.Imgproxy.BaseURL), "dialects.imgproxy.base_url", "must be an absolute http(s) URL")
	for i, r := range d.Imgproxy.URLReplacements {
		p := fmt.Sprintf("dialects.imgproxy.url_replacements[%d]", i)
		re, _, ok := strings.Cut(r, "=")
		if !ok {
			v.add(p, "want regex=replacement")
			continue
		}
		if _, err := regexp.Compile(re); err != nil {
			v.add(p, "bad regex: %v", err)
		}
	}
	ep := d.Imagekit.EndpointPath
	v.check(!strings.ContainsAny(ep, "?#") && !strings.Contains(strings.Trim(ep, "/"), "//"),
		"dialects.imagekit.endpoint_path", "must be a plain path")
	for _, cloud := range sortedKeys(d.Cloudinary.Clouds) {
		src := d.Cloudinary.Clouds[cloud]
		v.check(c.hasSource(src), "dialects.cloudinary.clouds", "cloud %q maps to unknown source %q", cloud, src)
	}
	for _, hash := range sortedKeys(d.CloudflareImages.Accounts) {
		src := d.CloudflareImages.Accounts[hash]
		v.check(hash != "" && !strings.Contains(hash, "/"), "dialects.cloudflare_images.accounts", "bad account hash %q", hash)
		v.check(c.hasSource(src), "dialects.cloudflare_images.accounts", "account %q maps to unknown source %q", hash, src)
	}
	for _, bucket := range sortedKeys(d.AWSSIH.Buckets) {
		src := d.AWSSIH.Buckets[bucket]
		v.check(bucket != "", "dialects.awssih.buckets", "bucket name must not be empty")
		v.check(c.hasSource(src), "dialects.awssih.buckets", "bucket %q maps to unknown source %q", bucket, src)
	}
	v.check((d.AWSSIH.RewriteMatchPattern == "") == (d.AWSSIH.RewriteSubstitution == ""),
		"dialects.awssih.rewrite_match_pattern", "must be set together with rewrite_substitution")
	v.check(d.AWSSIH.RewriteMatchPattern == "" || strings.HasPrefix(d.AWSSIH.RewriteMatchPattern, "/"),
		"dialects.awssih.rewrite_match_pattern", "must be a /pattern/flags literal")
	for i, name := range d.Simple.Enabled {
		v.check(validName(name), fmt.Sprintf("dialects.simple.enabled[%d]", i), "%q is not a provider name", name)
	}
	v.check(d.Simple.Source == "" || c.hasSource(d.Simple.Source), "dialects.simple.source", "source %q is not configured", d.Simple.Source)
	for i, h := range d.Simple.Hosts {
		v.check(validHost(h), fmt.Sprintf("dialects.simple.hosts[%d]", i), "%q is not a host name", h)
	}

	for _, name := range sortedKeys(c.Presets) {
		v.check(name != "" && !strings.ContainsAny(name, ":/"), "presets", "bad preset name %q", name)
		v.check(c.Presets[name] != "", "presets", "preset %q is empty", name)
	}
	v.check(!c.PresetsOnly || len(c.Presets) > 0 || c.PresetsFile != "", "presets_only", "is true but no presets are defined")
}

func validHost(h string) bool {
	if h == "" || strings.ContainsAny(h, "/:@ ") {
		return net.ParseIP(strings.Trim(h, "[]")) != nil
	}
	return true
}

// hasSource reports whether name is a configured source.
func (c *Config) hasSource(name string) bool {
	_, ok := c.Sources[name]
	return ok
}

func (c *Config) validateProcessing(v *validator) {
	p := c.Processing
	v.positive(int64(p.Workers), "processing.workers")
	v.nonNegative(int64(p.QueueSize), "processing.queue_size")
	v.nonNegative(int64(p.MemoryBudget), "processing.memory_budget")
	v.check(p.GoMemoryFraction > 0 && p.GoMemoryFraction <= 1, "processing.go_memory_fraction", "must be in (0, 1], got %g", p.GoMemoryFraction)
	v.check(p.WatermarkOpacity >= 0 && p.WatermarkOpacity <= 1, "processing.watermark_opacity", "must be in [0, 1], got %g", p.WatermarkOpacity)
	v.check(p.ColorProfilesDir == "" || filepath.IsAbs(p.ColorProfilesDir), "processing.color_profiles_dir", "must be an absolute path")
	v.check(validFont(p.WatermarkFont), "processing.watermark_font", "must be a Pango font description (letters, digits, spaces and \",-_.\", at most 128 bytes)")
	v.check(p.WatermarkFontFile == "" || filepath.IsAbs(p.WatermarkFontFile), "processing.watermark_font_file", "must be an absolute path")
	v.between(c.Fallback.Status, 200, 599, "fallback.status")
	v.nonNegative(int64(c.Fallback.TTL), "fallback.ttl")
	v.positive(int64(c.Vips.Concurrency), "vips.concurrency")
	v.check(c.Vips.FailOn != "", "vips.fail_on", "must be set")
}

func (c *Config) validateDetect(v *validator) {
	d := c.Detect
	if d.Enabled == False {
		return
	}
	v.check(d.Library != "", "detect.library", "must not be empty")
	v.between(d.Threads, 1, 64, "detect.threads")
	v.nonNegative(int64(d.Workers), "detect.workers")
	v.between(d.MaxInput, 32, 4096, "detect.max_input")
	v.between(d.CacheSize, 0, 1_000_000, "detect.cache_size")
	v.check(d.ModelsDir == "" || filepath.IsAbs(d.ModelsDir), "detect.models_dir", "must be an absolute path")
	for _, m := range []named[string]{
		{"detect.face_model", d.FaceModel},
		{"detect.object_model", d.ObjectModel},
		{"detect.classify.model", d.Classify.Model},
	} {
		switch {
		case m.v == "" || filepath.IsAbs(m.v):
		case strings.ContainsRune(m.v, '/') || m.v == "." || m.v == "..":
			v.add(m.path, "must be a file name in detect.models_dir or an absolute path, got %q", m.v)
		default:
			v.check(d.ModelsDir != "", m.path, "needs detect.models_dir for the relative name %q", m.v)
		}
	}
	v.check(d.Enabled != True || d.FaceModel != "" || d.ObjectModel != "" || d.Classify.Model != "", "detect.enabled",
		"true needs detect.face_model, detect.object_model or detect.classify.model")
	cl := d.Classify
	v.check(cl.Labels == "" || filepath.IsAbs(cl.Labels), "detect.classify.labels", "must be an absolute path")
	v.check(slices.Contains(normalizations(), cl.Normalization), "detect.classify.normalization",
		"must be imagenet, full, half or none, got %q", cl.Normalization)
	v.between(cl.TopKMax, 1, 64, "detect.classify.top_k_max")
	v.check(cl.Threshold >= 0 && cl.Threshold <= 1, "detect.classify.threshold", "must be between 0 and 1, got %v", cl.Threshold)
}

func (c *Config) validateFormats(v *validator) {
	f := c.Formats
	for _, q := range []named[int]{
		{"formats.quality", f.Quality},
		{"formats.jpeg.quality", f.JPEG.Quality},
		{"formats.webp.quality", f.WebP.Quality},
		{"formats.avif.quality", f.AVIF.Quality},
		{"formats.heic.quality", f.HEIC.Quality},
		{"formats.jxl.quality", f.JXL.Quality},
	} {
		v.between(q.v, 1, 100, q.path)
	}
	v.between(f.WebP.Effort, 0, 6, "formats.webp.effort")
	v.between(f.AVIF.Effort, 0, 9, "formats.avif.effort")
	v.between(f.JXL.Effort, 1, 10, "formats.jxl.effort")
	v.between(f.GIF.Effort, 1, 10, "formats.gif.effort")
	v.check(f.PDF.DPI >= 1 && f.PDF.DPI <= 1200, "formats.pdf.dpi", "must be in [1, 1200], got %g", f.PDF.DPI)
	v.between(f.EPS.MaxProcs, 1, 256, "formats.eps.max_procs")
	v.check(f.EPS.Timeout >= 100*time.Millisecond && f.EPS.Timeout <= 10*time.Minute, "formats.eps.timeout",
		"must be in [100ms, 10m], got %s", f.EPS.Timeout)
	v.check(f.EPS.MaxDPI >= 1 && f.EPS.MaxDPI <= 2400, "formats.eps.max_dpi", "must be in [1, 2400], got %g", f.EPS.MaxDPI)
	v.check(strings.TrimSpace(f.EPS.GSPath) != "", "formats.eps.gs_path", "must not be empty")
	v.between(f.Metafile.MaxProcs, 1, 256, "formats.metafile.max_procs")
	v.check(f.Metafile.Timeout >= 100*time.Millisecond && f.Metafile.Timeout <= 10*time.Minute, "formats.metafile.timeout",
		"must be in [100ms, 10m], got %s", f.Metafile.Timeout)
	v.check(strings.TrimSpace(f.Metafile.Path) != "", "formats.metafile.path", "must not be empty")
	as := c.Sandbox.MaxAS
	v.check(as == 0 || (as >= 64<<20 && as <= 1<<40), "sandbox.max_as",
		"must be 0 (no limit) or in [64MiB, 1TiB], got %s", as)
	v.check(f.HDR.ToneMap == ToneMapReinhard || f.HDR.ToneMap == ToneMapACES || f.HDR.ToneMap == ToneMapClip,
		"formats.hdr.tonemap", "must be reinhard, aces or clip, got %q", f.HDR.ToneMap)
	v.positive(f.AVIF.MaxPixels, "formats.avif.max_pixels")
	v.positive(f.Best.MaxPixels, "formats.best.max_pixels")
	v.check(f.RAW.PreviewMinRatio > 0 && f.RAW.PreviewMinRatio <= 100, "formats.raw.preview_min_ratio", "must be > 0 and <= 100")
	v.check(f.Best.ComplexityThreshold >= 0, "formats.best.complexity_threshold", "must be >= 0")
	v.check(len(f.Preferred) > 0, "formats.preferred", "must not be empty")
	v.check(!slices.Contains(f.Preferred, FormatSVG), "formats.preferred", "svg cannot be an output format")
	v.between(f.SaveDataQualityDelta, -100, 0, "formats.save_data_quality_delta")
	aq := f.AutoQuality
	v.check(aq.Method != "", "formats.autoquality.method", "must be set")
	v.between(aq.Min, 1, 100, "formats.autoquality.min")
	v.between(aq.Max, 1, 100, "formats.autoquality.max")
	v.check(aq.Min <= aq.Max, "formats.autoquality.min", "must be <= formats.autoquality.max")
	v.check(aq.Target > 0, "formats.autoquality.target", "must be > 0")
	v.check(aq.AllowedError >= 0 && aq.AllowedError <= 1, "formats.autoquality.allowed_error", "must be in 0..1")
	v.check(aq.Method != AutoQualitySize || aq.Target >= 1, "formats.autoquality.target",
		"must be a byte count >= 1 with method size")
	lossy := []Format{FormatJPEG, FormatWebP, FormatAVIF, FormatHEIC, FormatJXL}
	for _, b := range []struct {
		path string
		m    map[Format]int
	}{{"formats.autoquality.format_min", aq.FormatMin}, {"formats.autoquality.format_max", aq.FormatMax}} {
		for _, f := range sortedKeys(b.m) {
			v.check(slices.Contains(lossy, f), b.path, "%q is not a lossy output format", f)
			v.between(b.m[f], 1, 100, b.path)
		}
	}
	for _, f := range sortedKeys(aq.FormatTarget) {
		v.check(slices.Contains(lossy, f), "formats.autoquality.format_target", "%q is not a lossy output format", f)
		v.check(aq.FormatTarget[f] > 0, "formats.autoquality.format_target", "must be > 0")
	}
	for _, f := range lossy {
		lo, hi := aq.Min, aq.Max
		if q := aq.FormatMin[f]; q > 0 {
			lo = q
		}
		if q := aq.FormatMax[f]; q > 0 {
			hi = q
		}
		v.check(lo <= hi, "formats.autoquality.format_min", "%s minimum %d is above its maximum %d", f, lo, hi)
	}
}

func (c *Config) validateLimits(v *validator) {
	l := c.Limits
	for _, n := range []named[int64]{
		{"limits.max_src_bytes", int64(l.MaxSrcBytes)},
		{"limits.max_src_pixels", l.MaxSrcPixels},
		{"limits.max_result_dimension", int64(l.MaxResultDimension)},
		{"limits.max_animation_frames", int64(l.MaxAnimationFrames)},
		{"limits.max_animation_pixels", l.MaxAnimationPixels},
		{"limits.max_raw_demosaic_pixels", l.MaxRawDemosaicPixels},
		{"limits.max_svg_bytes", int64(l.MaxSVGBytes)},
		{"limits.max_svg_depth", int64(l.MaxSVGDepth)},
		{"limits.max_svg_elements", int64(l.MaxSVGElements)},
		{"limits.max_pdf_pages", int64(l.MaxPDFPages)},
		{"limits.max_watermark_text_pixels", l.MaxWatermarkTextPixels},
	} {
		v.positive(n.v, n.path)
	}
	// 0 turns the per-frame limit off: animations count all their frames
	// against max_src_pixels, as imgproxy does.
	v.nonNegative(l.MaxAnimationFramePixels, "limits.max_animation_frame_pixels")
}

// x26xPresets are the presets x264 and x265 share, fastest first.
func x26xPresets() []string {
	return []string{"ultrafast", "superfast", "veryfast", "faster", "fast", "medium", "slow", "slower", "veryslow", "placebo"}
}

func (c *Config) validateVideo(v *validator) {
	vd := c.Video
	v.positive(int64(vd.MaxBytes), "video.max_bytes")
	v.positive(int64(vd.MaxDuration), "video.max_duration")
	v.positive(vd.MaxPixels, "video.max_pixels")
	v.between(vd.Thumbnail.Samples, 1, 32, "video.thumbnail.samples")
	v.positive(int64(vd.Thumbnail.Span), "video.thumbnail.span")
	v.between(vd.MaxAnimationFrames, 1, 10_000, "video.max_animation_frames")
	e := vd.Encode
	v.between(e.VP9CRF, 0, 63, "video.encode.vp9_crf")
	v.between(e.AV1CRF, 0, 63, "video.encode.av1_crf")
	v.between(e.H264CRF, 0, 51, "video.encode.h264_crf")
	v.between(e.HEVCCRF, 0, 51, "video.encode.hevc_crf")
	v.between(e.VP9Speed, 0, 9, "video.encode.vp9_speed")
	v.between(e.AV1Speed, 0, 10, "video.encode.av1_speed")
	for _, p := range []struct{ name, v string }{
		{"video.encode.h264_preset", e.H264Preset}, {"video.encode.hevc_preset", e.HEVCPreset},
	} {
		v.check(slices.Contains(x26xPresets(), p.v), p.name, "%q is not an x264/x265 preset (%s)", p.v, strings.Join(x26xPresets(), ", "))
	}
	v.positive(int64(e.MaxFrames), "video.encode.max_frames")
	v.positive(int64(e.MaxDuration), "video.encode.max_duration")
	v.positive(e.MaxPixels, "video.encode.max_pixels")
	_, isNumber := vd.Thumbnail.Second.Seconds()
	v.check(isNumber || vd.Thumbnail.Second == VideoSecondAuto || vd.Thumbnail.Second == "",
		"video.thumbnail.second", "%q is not a number of seconds or auto", vd.Thumbnail.Second)
	c.validateVideoTransform(v)
}

func (c *Config) validateCache(v *validator) {
	ca := c.Cache
	v.nonNegative(int64(ca.Headers.TTL), "cache.headers.ttl")
	v.check(ca.TagsHeader != "", "cache.tags_header", "must not be empty")
	r := ca.Result
	seen := map[StoreKind]bool{}
	for _, s := range r.Stores {
		v.check(!seen[s], "cache.result.stores", "%s listed twice", s)
		seen[s] = true
	}
	if r.Enabled() {
		v.positive(int64(r.TTL), "cache.result.ttl")
		v.positive(int64(r.WriteQueue), "cache.result.write_queue")
	}
	if seen[StoreMemory] {
		v.positive(int64(r.MemoryBytes), "cache.result.memory_bytes")
	}
	o := ca.Origin
	seenOrigin := map[StoreKind]bool{}
	for _, s := range o.Stores {
		v.check(!seenOrigin[s], "cache.origin.stores", "%s listed twice", s)
		v.check(s != StoreMemory, "cache.origin.stores", "memory is the built-in origin LRU (cache.origin.memory_bytes); list disk, redis or s3")
		seenOrigin[s] = true
	}
	if len(o.Stores) > 0 {
		v.positive(int64(o.StoreTTL), "cache.origin.store_ttl")
	}
	// Backends are shared by the result and the origin cache.
	for s := range seenOrigin {
		seen[s] = true
	}
	if seen[StoreDisk] {
		v.check(filepath.IsAbs(r.Disk.Path), "cache.result.disk.path", "must be an absolute path with the disk store")
		v.positive(int64(r.Disk.Bytes), "cache.result.disk.bytes")
	}
	if seen[StoreRedis] {
		v.check(r.Redis.URL != "", "cache.result.redis.url", "must be set with the redis store")
		v.positive(int64(r.Redis.MaxValueBytes), "cache.result.redis.max_value_bytes")
	}
	if seen[StoreS3] {
		src, ok := c.Sources[r.S3.Source]
		v.check(ok && src.Type == SourceS3, "cache.result.s3.source", "must name an s3 source, got %q", r.S3.Source)
		v.check(r.S3.Bucket != "", "cache.result.s3.bucket", "must be set with the s3 store")
	}
	v.nonNegative(int64(ca.Origin.MemoryBytes), "cache.origin.memory_bytes")
	v.nonNegative(int64(ca.Origin.NegativeTTL), "cache.origin.negative_ttl")
}

// warnings are startup findings that are not errors.
func (c *Config) warnings() []string {
	var w []string
	if c.Security.UnsignedPolicy == UnsignedRestricted {
		w = append(w, fmt.Sprintf("unsigned requests are restricted to widths %v and qualities %v (security.unsigned_policy=restricted)",
			[]int(c.Security.AllowedWidths), []int(c.Security.AllowedQualities)))
	}
	if t := &c.Video.Transform; t.Enabled && t.UnsignedPolicy == UnsignedRestricted {
		w = append(w, fmt.Sprintf("unsigned video transformations are restricted to video presets %v with options %v and formats %v (video.transform.unsigned_policy=restricted)",
			sortedKeys(t.Presets), t.UnsignedAllowedOptions, t.UnsignedFormats))
	}
	if c.Security.UnsignedPolicy == UnsignedAllow && len(c.Signing.Keys) == 0 {
		w = append(w, "no signing keys and security.unsigned_policy=allow: any client can request any transformation")
	}
	if c.Fetch.AllowPrivateNetworks {
		w = append(w, "fetch.allow_private_networks=true disables the private-address SSRF block")
	}
	if c.DevMode {
		w = append(w, "dev mode is on: any http(s) source is allowed; do not use in production")
	}
	if t := &c.Video.Transform; t.Enabled && c.Role.Videos() && len(t.StreamKeys) == 0 && len(c.Signing.Keys) == 0 {
		w = append(w, "no video.transform.stream_keys and no signing keys: the child URLs of HLS and DASH manifests "+
			"work only on the instance that wrote them, until it restarts")
	}
	return w
}

func sortedKeys[K cmp.Ordered, V any](m map[K]V) []K {
	return slices.Sorted(maps.Keys(m))
}

// named is a value with its YAML path, for table-driven checks in a fixed
// order.
type named[T any] struct {
	path string
	v    T
}

// validFont reports whether s is a plausible Pango font description: the
// executor parses it again with the same character set.
func validFont(s string) bool {
	if strings.TrimSpace(s) == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(" ,-_.", r) {
			return false
		}
	}
	return true
}
