package config

import (
	"cmp"
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
	c.validateCache(&v)
	return errors.Join(v.errs...)
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
				v.check(a != "" && !strings.Contains(a, "://"), fmt.Sprintf("%s.allow[%d]", p, i), "want a host glob with an optional path prefix, got %q", a)
			}
			v.check(s.BaseURL == "" || validHTTPURL(s.BaseURL), p+".base_url", "must be an absolute http(s) URL")
		case SourceS3, SourceGCS, SourceAzure:
			v.check(len(s.Buckets) > 0 || s.Bucket != "", p+".buckets", "must list at least one bucket")
			v.check(s.Bucket == "" || len(s.Buckets) == 0 || slices.Contains(s.Buckets, s.Bucket), p+".bucket", "%q is not in buckets", s.Bucket)
			v.check(s.Endpoint == "" || validHTTPURL(s.Endpoint), p+".endpoint", "must be an absolute http(s) URL")
		case SourceFS:
			v.check(filepath.IsAbs(s.Root), p+".root", "must be an absolute path")
		case SourceFTP, SourceSFTP:
			v.check(s.Host != "", p+".host", "must be set")
			v.between(s.Port, 0, 65535, p+".port")
			v.check(s.Type != SourceSFTP || s.HostKey != "", p+".host_key", "is mandatory for sftp (no trust on first use)")
			v.check(s.PoolSize >= 0, p+".pool_size", "must not be negative")
			v.check(s.DialTimeout >= 0, p+".dial_timeout", "must not be negative")
			v.check(s.IdleTimeout >= 0, p+".idle_timeout", "must not be negative")
			v.check(s.PrivateKeyPassphrase == "" || s.PrivateKey != "", p+".private_key_passphrase", "needs private_key")
		}
	}
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
		"imgix": d.Imgix, "cloudinary": d.Cloudinary.Mount, "imagekit": d.Imagekit,
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
		{"limits.max_animation_frame_pixels", l.MaxAnimationFramePixels},
		{"limits.max_raw_demosaic_pixels", l.MaxRawDemosaicPixels},
		{"limits.max_svg_bytes", int64(l.MaxSVGBytes)},
		{"limits.max_svg_depth", int64(l.MaxSVGDepth)},
		{"limits.max_svg_elements", int64(l.MaxSVGElements)},
		{"limits.max_pdf_pages", int64(l.MaxPDFPages)},
		{"limits.max_watermark_text_pixels", l.MaxWatermarkTextPixels},
	} {
		v.positive(n.v, n.path)
	}
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
	if c.Security.UnsignedPolicy == UnsignedAllow && len(c.Signing.Keys) == 0 {
		w = append(w, "no signing keys and security.unsigned_policy=allow: any client can request any transformation")
	}
	if c.Fetch.AllowPrivateNetworks {
		w = append(w, "fetch.allow_private_networks=true disables the private-address SSRF block")
	}
	if c.DevMode {
		w = append(w, "dev mode is on: any http(s) source is allowed; do not use in production")
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
