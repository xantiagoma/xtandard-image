package config

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// imgproxy compatibility (XI_COMPAT_IMGPROXY_ENV, docs/CONFIG.md §2
// "imgproxy variables"): IMGPROXY_* variables are translated into XI_*
// values that sit between the YAML file and the XI_* variables, so an XI_*
// variable always wins. Only variables that are set are translated;
// imgproxy's own defaults are not imported.

const (
	envCompatImgproxy = "XI_COMPAT_IMGPROXY_ENV"
	imgproxyPrefix    = "IMGPROXY_"
)

// Named sources created from imgproxy variables.
const (
	compatSourceHTTP  = "http"
	compatSourceS3    = "s3"
	compatSourceGCS   = "gcs"
	compatSourceABS   = "abs"
	compatSourceLocal = "local"
)

// imgproxyVar is one translated IMGPROXY_* variable. The list drives the
// translation, the CONFIG.md table (checked by docs_test.go) and the tests.
type imgproxyVar struct {
	Name string // IMGPROXY_…
	// Targets are every XI_* variable the translation may set. Empty means
	// the variable is rejected (it would loosen security if ignored).
	Targets []string
	Note    string // semantics, as documented in CONFIG.md
	apply   func(t *imgproxyTranslator, v string) error
}

// blobCompat describes one imgproxy object-storage backend.
type blobCompat struct {
	allowed string // IMGPROXY_*_ALLOWED_BUCKETS
	scheme  string // scheme in IMGPROXY_ALLOWED_SOURCES
	source  string // compat source name
	typ     SourceType
}

// imgproxyVars is the translation table, in processing order: when two
// variables set the same XI_* value the earlier one wins.
func imgproxyVars() []imgproxyVar {
	src := func(name, field string) string { return sourceEnvPrefix(name) + field }
	blobS3 := blobCompat{"IMGPROXY_S3_ALLOWED_BUCKETS", "s3", compatSourceS3, SourceS3}
	blobGCS := blobCompat{"IMGPROXY_GCS_ALLOWED_BUCKETS", "gs", compatSourceGCS, SourceGCS}
	blobABS := blobCompat{"IMGPROXY_ABS_ALLOWED_BUCKETS", "abs", compatSourceABS, SourceAzure}
	return []imgproxyVar{
		// Signing and security.
		{"IMGPROXY_KEY", ts("XI_SIGNING_KEYS"), "Same hex list", direct},
		{"IMGPROXY_SALT", ts("XI_SIGNING_SALTS"), "Same hex list", direct},
		{"IMGPROXY_SIGNATURE_SIZE", ts("XI_SIGNING_SIZE"), "Same", direct},
		{"IMGPROXY_TRUSTED_SIGNATURES", ts("XI_SIGNING_TRUSTED"), "Same", direct},
		{"IMGPROXY_SOURCE_URL_ENCRYPTION_KEY", ts("XI_ENCRYPTION_KEYS"), "imgproxy Pro; the hex key becomes the only encryption key", direct},
		{"IMGPROXY_SECRET", ts("XI_SECURITY_SECRET"), "Same `Authorization: Bearer` check", direct},
		{"IMGPROXY_ALLOWED_PROCESSING_OPTIONS", ts("XI_SECURITY_ALLOWED_PROCESSING_OPTIONS"), "Same", direct},
		{"IMGPROXY_ALLOW_SECURITY_OPTIONS", ts("XI_SECURITY_ALLOW_SECURITY_OPTIONS"), "Same", direct},

		// Sources. ALLOWED_SOURCES comes first: the USE_* switches read it.
		{
			"IMGPROXY_ALLOWED_SOURCES", ts("XI_SOURCES", src(compatSourceHTTP, "TYPE"), src(compatSourceHTTP, "ALLOW"), src(compatSourceLocal, "PREFIX")),
			"`http(s)://host[:port][/path/]` patterns become the allow list of an http source named `http` (scheme dropped, `*` in the host kept as a host glob, the path kept as a directory prefix, `*` in the path rejected). `s3://`, `gs://`, `abs://` patterns restrict the buckets of the matching source (bucket names only). `local:///dir/` becomes the `local` source prefix. Unset: no http source is created (imgproxy would fetch any URL), a startup warning says so",
			(*imgproxyTranslator).allowedSources,
		},
		{"IMGPROXY_USE_S3", ts("XI_SOURCES", src(compatSourceS3, "TYPE"), src(compatSourceS3, "BUCKETS")), "Creates an s3 source named `s3`. xtandard-image needs a bucket allowlist: `IMGPROXY_S3_ALLOWED_BUCKETS` or `s3://` patterns in `IMGPROXY_ALLOWED_SOURCES`. Credentials come from the standard `AWS_*` chain as in imgproxy", blobSwitch(blobS3)},
		{"IMGPROXY_S3_ALLOWED_BUCKETS", ts(src(compatSourceS3, "BUCKETS")), "Intersected (exact names) with `s3://` patterns of `IMGPROXY_ALLOWED_SOURCES` when both are set", blobBuckets(blobS3)},
		{"IMGPROXY_S3_DENIED_BUCKETS", nil, "Rejected at startup: there is no deny list; list the allowed buckets instead", rejectDenied},
		{"IMGPROXY_S3_REGION", ts(src(compatSourceS3, "REGION")), "Same", sourceField(compatSourceS3, "REGION")},
		{"IMGPROXY_S3_ENDPOINT_USE_PATH_STYLE", ts(src(compatSourceS3, "FORCE_PATH_STYLE")), "Same", sourceField(compatSourceS3, "FORCE_PATH_STYLE")},
		{"IMGPROXY_S3_ENDPOINT", ts(src(compatSourceS3, "ENDPOINT"), src(compatSourceS3, "FORCE_PATH_STYLE")), "Same; path-style addressing is turned on unless `IMGPROXY_S3_ENDPOINT_USE_PATH_STYLE` says otherwise (imgproxy's default)", s3Endpoint},
		{"IMGPROXY_S3_ASSUME_ROLE_ARN", ts(src(compatSourceS3, "ROLE_ARN")), "Same", sourceField(compatSourceS3, "ROLE_ARN")},
		{"IMGPROXY_USE_GCS", ts("XI_SOURCES", src(compatSourceGCS, "TYPE"), src(compatSourceGCS, "BUCKETS")), "Creates a gcs source named `gcs`; bucket allowlist as for `IMGPROXY_USE_S3`", blobSwitch(blobGCS)},
		{"IMGPROXY_GCS_ALLOWED_BUCKETS", ts(src(compatSourceGCS, "BUCKETS")), "As `IMGPROXY_S3_ALLOWED_BUCKETS`", blobBuckets(blobGCS)},
		{"IMGPROXY_GCS_DENIED_BUCKETS", nil, "Rejected at startup, as `IMGPROXY_S3_DENIED_BUCKETS`", rejectDenied},
		{"IMGPROXY_GCS_KEY", ts(src(compatSourceGCS, "CREDENTIALS_JSON")), "Same service-account JSON", sourceField(compatSourceGCS, "CREDENTIALS_JSON")},
		{"IMGPROXY_GCS_ENDPOINT", ts(src(compatSourceGCS, "ENDPOINT")), "Same", sourceField(compatSourceGCS, "ENDPOINT")},
		{"IMGPROXY_USE_ABS", ts("XI_SOURCES", src(compatSourceABS, "TYPE"), src(compatSourceABS, "BUCKETS")), "Creates an azure source named `abs`; container allowlist as for `IMGPROXY_USE_S3`", blobSwitch(blobABS)},
		{"IMGPROXY_ABS_ALLOWED_BUCKETS", ts(src(compatSourceABS, "BUCKETS")), "As `IMGPROXY_S3_ALLOWED_BUCKETS`", blobBuckets(blobABS)},
		{"IMGPROXY_ABS_DENIED_BUCKETS", nil, "Rejected at startup, as `IMGPROXY_S3_DENIED_BUCKETS`", rejectDenied},
		{"IMGPROXY_ABS_NAME", ts(src(compatSourceABS, "ACCOUNT")), "Storage account name", sourceField(compatSourceABS, "ACCOUNT")},
		{"IMGPROXY_ABS_KEY", ts(src(compatSourceABS, "ACCOUNT_KEY")), "Same", sourceField(compatSourceABS, "ACCOUNT_KEY")},
		{"IMGPROXY_ABS_ENDPOINT", ts(src(compatSourceABS, "ENDPOINT")), "Same service URL", sourceField(compatSourceABS, "ENDPOINT")},
		{"IMGPROXY_LOCAL_FILESYSTEM_ROOT", ts("XI_SOURCES", src(compatSourceLocal, "TYPE"), src(compatSourceLocal, "ROOT"), src(compatSourceLocal, "PREFIX")), "Creates an fs source named `local` (jailed with `os.Root`); its prefix comes from `local://` patterns of `IMGPROXY_ALLOWED_SOURCES`", (*imgproxyTranslator).localRoot},

		// Fetching and SSRF.
		{"IMGPROXY_ALLOW_LOOPBACK_SOURCE_ADDRESSES", ts("XI_FETCH_ALLOWED_NETWORKS"), "`true` adds `127.0.0.0/8`, `::1/128`", networks("127.0.0.0/8", "::1/128")},
		{"IMGPROXY_ALLOW_LINK_LOCAL_SOURCE_ADDRESSES", ts("XI_FETCH_ALLOWED_NETWORKS"), "`true` adds `169.254.0.0/16`, `fe80::/10`", networks("169.254.0.0/16", "fe80::/10")},
		{"IMGPROXY_ALLOW_PRIVATE_SOURCE_ADDRESSES", ts("XI_FETCH_ALLOWED_NETWORKS"), "`true` adds RFC 1918, CGNAT `100.64.0.0/10` and `fc00::/7`. imgproxy defaults to `true`; xtandard-image blocks private addresses unless this is set", networks("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "fc00::/7")},
		{"IMGPROXY_DOWNLOAD_TIMEOUT", ts("XI_FETCH_TIMEOUT"), "Integer seconds → duration", seconds},
		{"IMGPROXY_MAX_REDIRECTS", ts("XI_FETCH_MAX_REDIRECTS"), "Same", direct},
		{"IMGPROXY_USER_AGENT", ts("XI_FETCH_USER_AGENT"), "Same; `%current_version` is not expanded", direct},

		// Server.
		{"IMGPROXY_BIND", ts("XI_SERVER_ADDR"), "Same `host:port` (unix sockets are not supported)", direct},
		{"IMGPROXY_PATH_PREFIX", ts("XI_SERVER_PATH_PREFIX"), "Same", direct},
		{"IMGPROXY_TIMEOUT", ts("XI_SERVER_REQUEST_TIMEOUT"), "Integer seconds → duration", seconds},
		{"IMGPROXY_READ_REQUEST_TIMEOUT", ts("XI_SERVER_READ_TIMEOUT"), "Integer seconds → duration", seconds},
		{"IMGPROXY_WRITE_RESPONSE_TIMEOUT", ts("XI_SERVER_WRITE_TIMEOUT"), "Integer seconds → duration; must be ≥ the request timeout", seconds},
		{"IMGPROXY_KEEP_ALIVE_TIMEOUT", ts("XI_SERVER_IDLE_TIMEOUT"), "Integer seconds → duration; `0` (keep-alive off) is rejected", seconds},
		{"IMGPROXY_GRACEFUL_STOP_TIMEOUT", ts("XI_SERVER_SHUTDOWN_TIMEOUT"), "Integer seconds → duration", seconds},
		{"IMGPROXY_MAX_CLIENTS", ts("XI_SERVER_MAX_CONNECTIONS"), "`0` (unlimited) keeps the xtandard-image default", positiveOrDefault},
		{"IMGPROXY_ALLOW_ORIGIN", ts("XI_SERVER_CORS_ORIGINS"), "Same single origin", direct},
		{"IMGPROXY_WORKERS", ts("XI_PROCESSING_WORKERS"), "Same", direct},
		{"IMGPROXY_REQUESTS_QUEUE_SIZE", ts("XI_PROCESSING_QUEUE_SIZE"), "`0` (unlimited in imgproxy) keeps the default of workers×16; the queue is always bounded", positiveOrDefault},

		// Limits.
		{"IMGPROXY_MAX_SRC_RESOLUTION", ts("XI_LIMITS_MAX_SRC_PIXELS"), "Megapixels (float) → pixels", megapixels},
		{"IMGPROXY_MAX_SRC_FILE_SIZE", ts("XI_LIMITS_MAX_SRC_BYTES"), "Bytes; `0` (unlimited) keeps the default", positiveOrDefault},
		{"IMGPROXY_MAX_ANIMATION_FRAMES", ts("XI_LIMITS_MAX_ANIMATION_FRAMES"), "Same", direct},
		{"IMGPROXY_MAX_ANIMATION_FRAME_RESOLUTION", ts("XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS"), "Megapixels (float) → pixels; `0` (off) keeps the default", megapixels},
		{"IMGPROXY_MAX_RESULT_DIMENSION", ts("XI_LIMITS_MAX_RESULT_DIMENSION"), "`0` (unlimited) keeps the default", positiveOrDefault},

		// Cache headers.
		{"IMGPROXY_TTL", ts("XI_CACHE_HEADERS_TTL"), "Integer seconds → duration", seconds},
		{"IMGPROXY_CACHE_CONTROL_PASSTHROUGH", ts("XI_CACHE_HEADERS_PASSTHROUGH"), "Same", direct},
		{"IMGPROXY_ETAG_BUSTER", ts("XI_CACHE_ETAG_BUSTER"), "Same", direct},

		// URL dialect and presets.
		{"IMGPROXY_ARGUMENTS_SEPARATOR", ts("XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR"), "Same", direct},
		{"IMGPROXY_BASE_URL", ts("XI_DIALECTS_IMGPROXY_BASE_URL"), "Same", direct},
		{"IMGPROXY_URL_REPLACEMENTS", ts("XI_DIALECTS_IMGPROXY_URL_REPLACEMENTS"), "Wildcard patterns are converted to the equivalent anchored regexes", urlReplacements},
		{"IMGPROXY_PRESETS_SEPARATOR", ts("XI_PRESETS"), "Only used to split `IMGPROXY_PRESETS`", func(*imgproxyTranslator, string) error { return nil }},
		{"IMGPROXY_PRESETS", ts("XI_PRESETS"), "Same `name=options` list, split by `IMGPROXY_PRESETS_SEPARATOR` (default `,`)", presets},
		{"IMGPROXY_PRESETS_PATH", ts("XI_PRESETS_FILE"), "Same one-preset-per-line file", direct},
		{"IMGPROXY_ONLY_PRESETS", ts("XI_PRESETS_ONLY"), "Same", direct},

		// Processing.
		{"IMGPROXY_AUTO_ROTATE", ts("XI_PROCESSING_AUTO_ROTATE"), "Same", direct},
		{"IMGPROXY_STRIP_METADATA", ts("XI_PROCESSING_STRIP_METADATA"), "Same", direct},
		{"IMGPROXY_KEEP_COPYRIGHT", ts("XI_PROCESSING_KEEP_COPYRIGHT"), "Same", direct},
		{"IMGPROXY_STRIP_COLOR_PROFILE", ts("XI_PROCESSING_STRIP_COLOR_PROFILE"), "Same", direct},
		{"IMGPROXY_PRESERVE_HDR", ts("XI_PROCESSING_PRESERVE_HDR"), "Same", direct},
		{"IMGPROXY_USE_LINEAR_COLORSPACE", ts("XI_PROCESSING_LINEAR_RESIZE"), "Same", direct},
		{"IMGPROXY_WATERMARK_OPACITY", ts("XI_PROCESSING_WATERMARK_OPACITY"), "Same", direct},
		{"IMGPROXY_WATERMARK_PATH", ts("XI_PROCESSING_WATERMARK_SOURCE"), "Translated to `local:///<path>` when the path lies under `IMGPROXY_LOCAL_FILESYSTEM_ROOT`; otherwise a startup warning (set a source string yourself). Wins over `IMGPROXY_WATERMARK_URL`", localPath},
		{"IMGPROXY_WATERMARK_URL", ts("XI_PROCESSING_WATERMARK_SOURCE"), "Same URL, resolved against the sources like any source string", direct},
		{"IMGPROXY_FALLBACK_IMAGE_PATH", ts("XI_FALLBACK_SOURCE"), "As `IMGPROXY_WATERMARK_PATH`", localPath},
		{"IMGPROXY_FALLBACK_IMAGE_URL", ts("XI_FALLBACK_SOURCE"), "As `IMGPROXY_WATERMARK_URL`", direct},
		{"IMGPROXY_FALLBACK_IMAGE_HTTP_CODE", ts("XI_FALLBACK_STATUS"), "`0` (keep the error status) keeps the default", positiveOrDefault},

		// Formats.
		{"IMGPROXY_FORMAT_QUALITY", ts("XI_FORMATS_JPEG_QUALITY", "XI_FORMATS_WEBP_QUALITY", "XI_FORMATS_AVIF_QUALITY", "XI_FORMATS_HEIC_QUALITY", "XI_FORMATS_JXL_QUALITY"), "`fmt=q` pairs → per-format quality; formats without a per-format setting are ignored with a warning", formatQuality},
		{"IMGPROXY_QUALITY", ts("XI_FORMATS_QUALITY", "XI_FORMATS_JPEG_QUALITY", "XI_FORMATS_HEIC_QUALITY"), "Also sets jpeg and heic quality, which imgproxy's default `IMGPROXY_FORMAT_QUALITY` leaves on the global value", quality},
		{"IMGPROXY_JPEG_PROGRESSIVE", ts("XI_FORMATS_JPEG_PROGRESSIVE"), "Same (xtandard-image defaults to `true`, imgproxy to `false`)", direct},
		{"IMGPROXY_PNG_QUANTIZE", ts("XI_FORMATS_PNG_QUANTIZE"), "Same", direct},
		{"IMGPROXY_WEBP_EFFORT", ts("XI_FORMATS_WEBP_EFFORT"), "Same", direct},
		{"IMGPROXY_AVIF_SPEED", ts("XI_FORMATS_AVIF_EFFORT"), "Speed 0–9 → effort `9 − speed`", avifSpeed},
		{"IMGPROXY_JXL_EFFORT", ts("XI_FORMATS_JXL_EFFORT"), "Same", direct},
		{"IMGPROXY_AUTO_WEBP", ts("XI_FORMATS_AUTO_WEBP"), "Same", direct},
		{"IMGPROXY_AUTO_AVIF", ts("XI_FORMATS_AUTO_AVIF"), "Same", direct},
		{"IMGPROXY_AUTO_JXL", ts("XI_FORMATS_AUTO_JXL"), "Same", direct},
		{"IMGPROXY_ENFORCE_WEBP", ts("XI_FORMATS_ENFORCE_WEBP"), "Same", direct},
		{"IMGPROXY_ENFORCE_AVIF", ts("XI_FORMATS_ENFORCE_AVIF"), "Same", direct},
		{"IMGPROXY_ENFORCE_JXL", ts("XI_FORMATS_ENFORCE_JXL"), "Same", direct},
		{"IMGPROXY_PREFERRED_FORMATS", ts("XI_FORMATS_PREFERRED"), "Same format names", direct},
		{"IMGPROXY_SKIP_PROCESSING_FORMATS", ts("XI_FORMATS_SKIP_PROCESSING"), "Same format names", direct},
		{"IMGPROXY_BEST_FORMAT_MAX_RESOLUTION", ts("XI_FORMATS_BEST_MAX_PIXELS"), "Megapixels (float) → pixels; `0` (no cap in imgproxy) keeps the default", megapixels},
		{"IMGPROXY_BEST_FORMAT_COMPLEXITY_THRESHOLD", ts("XI_FORMATS_BEST_COMPLEXITY_THRESHOLD"), "Same meaning (below: lossless; `0` skips the analysis), but the score is our own (FORMATS.md §6.3), so tune it on your images", direct},
		{"IMGPROXY_ALWAYS_RASTERIZE_SVG", ts("XI_FORMATS_SVG_PASSTHROUGH"), "Inverted", inverted},
		{"IMGPROXY_ENABLE_CLIENT_HINTS", ts("XI_CLIENT_HINTS_ENABLED"), "Same", direct},

		// Observability.
		{"IMGPROXY_LOG_LEVEL", ts("XI_OBSERVABILITY_LOG_LEVEL"), "Same levels", direct},
		{"IMGPROXY_ENABLE_DEBUG_HEADERS", ts("XI_OBSERVABILITY_DEBUG_HEADERS"), "Same switch; the header names differ (`X-Xi-*`)", direct},
		{"IMGPROXY_PROMETHEUS_BIND", ts("XI_ADMIN_ADDR"), "Metrics are served on the admin listener", direct},
		{"IMGPROXY_OPEN_TELEMETRY_ENABLE", ts("XI_OTEL_ENABLED"), "Same; configure the exporter with the standard `OTEL_*` variables", direct},
	}
}

func ts(s ...string) []string { return s }

// imgproxyTranslator accumulates the XI_* values translated from the
// IMGPROXY_* variables in.
type imgproxyTranslator struct {
	in       map[string]string   // set IMGPROXY_* variables (non-empty)
	out      map[string]string   // translated XI_* values
	from     map[string][]string // XI_* → the IMGPROXY_* variables behind it
	cur      string              // variable being translated
	targets  []string            // its Targets
	warnings []string

	allowedSet     bool                // IMGPROXY_ALLOWED_SOURCES is set
	patternBuckets map[string][]string // scheme → buckets from ALLOWED_SOURCES
	localPrefixes  []string            // local:// directory prefixes ("" = root)
}

// imgproxyCompat is the translation result.
type imgproxyCompat struct {
	out      map[string]string
	from     map[string][]string
	unmapped []string
	warnings []string
	err      error
}

// translateImgproxyEnv translates the IMGPROXY_* variables of environ.
func translateImgproxyEnv(environ []string) imgproxyCompat {
	t := &imgproxyTranslator{
		in:             imgproxyEnviron(environ),
		out:            map[string]string{},
		from:           map[string][]string{},
		patternBuckets: map[string][]string{},
	}
	var errList []error
	known := map[string]bool{}
	for _, iv := range imgproxyVars() {
		known[iv.Name] = true
		v, ok := t.in[iv.Name]
		if !ok {
			continue
		}
		t.cur, t.targets = iv.Name, iv.Targets
		if err := iv.apply(t, v); err != nil {
			errList = append(errList, fmt.Errorf("%w: %s: %w", ErrEnv, iv.Name, err))
		}
	}
	if !t.allowedSet {
		t.warnings = append(t.warnings, "IMGPROXY_ALLOWED_SOURCES is unset: imgproxy would fetch any http(s) URL, xtandard-image creates no http source (set IMGPROXY_ALLOWED_SOURCES or XI_SOURCES_* explicitly)")
	}
	var unmapped []string
	for k := range t.in {
		if !known[k] {
			unmapped = append(unmapped, k)
		}
	}
	slices.Sort(unmapped)
	return imgproxyCompat{out: t.out, from: t.from, unmapped: unmapped, warnings: t.warnings, err: errors.Join(errList...)}
}

// imgproxyEnviron returns the non-empty IMGPROXY_* variables.
func imgproxyEnviron(environ []string) map[string]string {
	in := map[string]string{}
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, imgproxyPrefix) && strings.TrimSpace(v) != "" {
			in[k] = strings.TrimSpace(v)
		}
	}
	return in
}

// applyImgproxyCompat puts the translated values under the XI_* variables:
// an XI_* variable that is set (directly or via _FILE) wins, except
// XI_SOURCES, whose names are merged. It returns, per XI_* variable taken
// from imgproxy, the IMGPROXY_* names for error messages.
func applyImgproxyCompat(vars, origin map[string]string, c *imgproxyCompat) map[string]string {
	alias := map[string]string{}
	for _, k := range slices.Sorted(maps.Keys(c.out)) {
		v := c.out[k]
		if k == envSources {
			vars[k] = joinUnique(vars[k], v)
			continue
		}
		if strings.TrimSpace(vars[k]) != "" || origin[k] != "" {
			continue
		}
		vars[k] = v
		alias[k] = strings.Join(c.from[k], ", ")
	}
	return alias
}

// set stores key=val unless an earlier variable already set key.
func (t *imgproxyTranslator) set(key, val string) {
	if _, ok := t.out[key]; ok {
		return
	}
	t.out[key] = val
	t.from[key] = append(t.from[key], t.cur)
}

// add appends list items to key, skipping duplicates.
func (t *imgproxyTranslator) add(key string, items ...string) {
	t.out[key] = joinUnique(t.out[key], strings.Join(items, ","))
	if !slices.Contains(t.from[key], t.cur) {
		t.from[key] = append(t.from[key], t.cur)
	}
}

// addSource declares the named source with its type.
func (t *imgproxyTranslator) addSource(name string, typ SourceType) {
	t.add(envSources, name)
	t.set(sourceEnvPrefix(name)+"TYPE", string(typ))
}

// hasSource reports whether the named source was created.
func (t *imgproxyTranslator) hasSource(name string) bool {
	_, ok := t.out[sourceEnvPrefix(name)+"TYPE"]
	return ok
}

func (t *imgproxyTranslator) warnf(format string, args ...any) {
	t.warnings = append(t.warnings, t.cur+": "+fmt.Sprintf(format, args...))
}

// target returns the first target of the variable being translated.
func (t *imgproxyTranslator) target() string {
	return t.targets[0]
}

func direct(t *imgproxyTranslator, v string) error {
	t.set(t.target(), v)
	return nil
}

// seconds converts imgproxy's integer seconds into a duration.
func seconds(t *imgproxyTranslator, v string) error {
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return fmt.Errorf("want a non-negative integer number of seconds, got %q", v)
	}
	if n == 0 && t.cur == "IMGPROXY_KEEP_ALIVE_TIMEOUT" {
		return errors.New("0 disables keep-alive in imgproxy; xtandard-image needs a positive idle timeout")
	}
	t.set(t.target(), strconv.Itoa(n)+"s")
	return nil
}

// positiveOrDefault translates n > 0 and keeps the default for imgproxy's
// "0 = unlimited/off".
func positiveOrDefault(t *imgproxyTranslator, v string) error {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return fmt.Errorf("want a non-negative integer, got %q", v)
	}
	if n > 0 {
		t.set(t.target(), strconv.FormatInt(n, 10))
	}
	return nil
}

// megapixels converts imgproxy's float megapixels into pixels; 0 keeps the
// default.
func megapixels(t *imgproxyTranslator, v string) error {
	mp, err := strconv.ParseFloat(v, 64)
	if err != nil || mp < 0 || math.IsInf(mp, 0) || math.IsNaN(mp) || mp > 1e9 {
		return fmt.Errorf("want non-negative megapixels, got %q", v)
	}
	if px := int64(math.Round(mp * 1e6)); px > 0 {
		t.set(t.target(), strconv.FormatInt(px, 10))
	}
	return nil
}

func inverted(t *imgproxyTranslator, v string) error {
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fmt.Errorf("want true or false, got %q", v)
	}
	t.set(t.target(), strconv.FormatBool(!b))
	return nil
}

func avifSpeed(t *imgproxyTranslator, v string) error {
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 9 {
		return fmt.Errorf("want a speed between 0 and 9, got %q", v)
	}
	t.set(t.target(), strconv.Itoa(9-n))
	return nil
}

// networks adds cidrs to the SSRF exemptions when the switch is true.
func networks(cidrs ...string) func(*imgproxyTranslator, string) error {
	return func(t *imgproxyTranslator, v string) error {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("want true or false, got %q", v)
		}
		if b {
			t.add("XI_FETCH_ALLOWED_NETWORKS", cidrs...)
		}
		return nil
	}
}

// formatQuality translates "jpeg=80,webp=70" into per-format qualities.
func formatQuality(t *imgproxyTranslator, v string) error {
	for pair := range strings.SplitSeq(v, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, q, ok := strings.Cut(pair, "=")
		name, q = strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(q)
		n, err := strconv.Atoi(q)
		if !ok || name == "" || err != nil || n < 1 || n > 100 {
			return fmt.Errorf("want format=quality pairs with quality 1..100, got %q", pair)
		}
		switch name {
		case "jpg":
			name = "jpeg"
		case "jpeg", "webp", "avif", "heic", "jxl":
		default:
			t.warnf("%s has no per-format quality in xtandard-image; ignored", name)
			continue
		}
		t.set("XI_FORMATS_"+strings.ToUpper(name)+"_QUALITY", strconv.Itoa(n))
	}
	return nil
}

func quality(t *imgproxyTranslator, v string) error {
	for _, k := range []string{"XI_FORMATS_QUALITY", "XI_FORMATS_JPEG_QUALITY", "XI_FORMATS_HEIC_QUALITY"} {
		t.set(k, v)
	}
	return nil
}

// presets re-joins IMGPROXY_PRESETS with the XI_PRESETS separator.
func presets(t *imgproxyTranslator, v string) error {
	sep := cmp.Or(t.in["IMGPROXY_PRESETS_SEPARATOR"], ",")
	var out []string
	for p := range strings.SplitSeq(v, sep) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.Contains(p, "=") || strings.Contains(p, ",") {
			return fmt.Errorf("preset %q: want name=options without commas", p)
		}
		out = append(out, p)
	}
	t.set("XI_PRESETS", strings.Join(out, ","))
	return nil
}

// urlReplacements converts imgproxy wildcard patterns into regexes.
func urlReplacements(t *imgproxyTranslator, v string) error {
	var out []string
	for kv := range strings.SplitSeq(v, ";") {
		pattern, repl, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("want pattern=replacement pairs separated by ;, got %q", kv)
		}
		out = append(out, wildcardRegexp(pattern)+"="+repl)
	}
	t.set(t.target(), strings.Join(out, ";"))
	return nil
}

// wildcardRegexp mirrors imgproxy's RegexpFromPattern: an anchored prefix
// match where "*" matches within one path segment.
func wildcardRegexp(pattern string) string {
	parts := strings.Split(pattern, "*")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	return "^" + strings.Join(parts, "([^/]*)")
}

// allowedSources translates IMGPROXY_ALLOWED_SOURCES.
func (t *imgproxyTranslator) allowedSources(v string) error {
	t.allowedSet = true
	var allow []string
	for p := range strings.SplitSeq(v, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		scheme, rest, ok := strings.Cut(p, "://")
		if !ok {
			return fmt.Errorf("pattern %q: want scheme://…", p)
		}
		switch scheme = strings.ToLower(scheme); scheme {
		case "http", "https":
			host, path, _ := strings.Cut(rest, "/")
			if host == "" || strings.Contains(path, "*") {
				return fmt.Errorf("pattern %q: want http(s)://host-glob[:port][/path/] without * in the path", p)
			}
			allow = append(allow, strings.TrimSuffix(host+"/"+path, "/"))
		case "s3", "gs", "abs":
			bucket, path, _ := strings.Cut(rest, "/")
			if bucket == "" || path != "" {
				return fmt.Errorf("pattern %q: only whole buckets (%s://bucket/) can be translated; use XI_SOURCES_<NAME>_PREFIX for a key prefix", p, scheme)
			}
			t.patternBuckets[scheme] = append(t.patternBuckets[scheme], bucket)
		case "local":
			dir := strings.Trim(rest, "/")
			if strings.Contains(dir, "*") {
				return fmt.Errorf("pattern %q: * cannot be translated", p)
			}
			t.localPrefixes = append(t.localPrefixes, dir)
		default:
			return fmt.Errorf("pattern %q: scheme %s has no xtandard-image source", p, scheme)
		}
	}
	if len(allow) > 0 {
		t.addSource(compatSourceHTTP, SourceHTTP)
		t.add(sourceEnvPrefix(compatSourceHTTP)+"ALLOW", allow...)
	}
	return nil
}

// blobSwitch translates IMGPROXY_USE_S3/GCS/ABS.
func blobSwitch(b blobCompat) func(*imgproxyTranslator, string) error {
	return func(t *imgproxyTranslator, v string) error {
		return t.blobSwitch(b, v)
	}
}

func (t *imgproxyTranslator) blobSwitch(b blobCompat, v string) error {
	on, err := strconv.ParseBool(v)
	if err != nil {
		return fmt.Errorf("want true or false, got %q", v)
	}
	if !on {
		return nil
	}
	if t.allowedSet && len(t.patternBuckets[b.scheme]) == 0 {
		t.warnf("IMGPROXY_ALLOWED_SOURCES allows no %s:// URL; source %q not created", b.scheme, b.source)
		return nil
	}
	t.addSource(b.source, b.typ)
	if _, ok := t.in[b.allowed]; !ok && t.allowedSet {
		t.set(sourceEnvPrefix(b.source)+"BUCKETS", strings.Join(t.patternBuckets[b.scheme], ","))
	}
	return nil
}

// blobBuckets translates IMGPROXY_*_ALLOWED_BUCKETS.
func blobBuckets(b blobCompat) func(*imgproxyTranslator, string) error {
	return func(t *imgproxyTranslator, v string) error {
		return t.blobBuckets(b, v)
	}
}

func (t *imgproxyTranslator) blobBuckets(b blobCompat, v string) error {
	if !t.hasSource(b.source) {
		return nil
	}
	var buckets []string
	for s := range strings.SplitSeq(v, ",") {
		if s = strings.TrimSpace(s); s != "" && (!t.allowedSet || slices.Contains(t.patternBuckets[b.scheme], s)) {
			buckets = append(buckets, s)
		}
	}
	if len(buckets) == 0 {
		return fmt.Errorf("no bucket is allowed by both %s and the %s:// patterns of IMGPROXY_ALLOWED_SOURCES", t.cur, b.scheme)
	}
	t.set(sourceEnvPrefix(b.source)+"BUCKETS", strings.Join(buckets, ","))
	return nil
}

func rejectDenied(*imgproxyTranslator, string) error {
	return errors.New("xtandard-image has no bucket deny list; list the allowed buckets in IMGPROXY_*_ALLOWED_BUCKETS or XI_SOURCES_<NAME>_BUCKETS")
}

// sourceField copies the value onto a field of a compat source, if that
// source was created.
func sourceField(name, field string) func(*imgproxyTranslator, string) error {
	return func(t *imgproxyTranslator, v string) error {
		if t.hasSource(name) {
			t.set(sourceEnvPrefix(name)+field, v)
		}
		return nil
	}
}

func s3Endpoint(t *imgproxyTranslator, v string) error {
	if !t.hasSource(compatSourceS3) {
		return nil
	}
	p := sourceEnvPrefix(compatSourceS3)
	t.set(p+"ENDPOINT", v)
	t.set(p+"FORCE_PATH_STYLE", "true") // no-op when USE_PATH_STYLE came first
	return nil
}

// localRoot translates IMGPROXY_LOCAL_FILESYSTEM_ROOT.
func (t *imgproxyTranslator) localRoot(v string) error {
	if t.allowedSet && len(t.localPrefixes) == 0 {
		t.warnf("IMGPROXY_ALLOWED_SOURCES allows no local:// URL; source %q not created", compatSourceLocal)
		return nil
	}
	t.addSource(compatSourceLocal, SourceFS)
	t.set(sourceEnvPrefix(compatSourceLocal)+"ROOT", v)
	if slices.Contains(t.localPrefixes, "") {
		return nil // the whole root is allowed
	}
	switch prefixes := slices.Compact(slices.Sorted(slices.Values(t.localPrefixes))); len(prefixes) {
	case 0:
	case 1:
		t.set(sourceEnvPrefix(compatSourceLocal)+"PREFIX", prefixes[0]+"/")
	default:
		return fmt.Errorf("IMGPROXY_ALLOWED_SOURCES lists several local:// directories %q; one source prefix cannot express that", prefixes)
	}
	return nil
}

// localPath translates a file path under IMGPROXY_LOCAL_FILESYSTEM_ROOT into
// a local:/// source string.
func localPath(t *imgproxyTranslator, v string) error {
	root := strings.TrimSuffix(t.in["IMGPROXY_LOCAL_FILESYSTEM_ROOT"], "/") + "/"
	rel, ok := strings.CutPrefix(v, root)
	if !t.hasSource(compatSourceLocal) || !ok || rel == "" {
		t.warnf("%q is not under the local source root; not translated (set %s to a source string)", v, t.target())
		return nil
	}
	t.set(t.target(), "local:///"+rel)
	return nil
}

// joinUnique joins two comma-separated lists, trimming items and dropping
// empty and duplicate ones.
func joinUnique(a, b string) string {
	var out []string
	for item := range strings.SplitSeq(a+","+b, ",") {
		if item = strings.TrimSpace(item); item != "" && !slices.Contains(out, item) {
			out = append(out, item)
		}
	}
	return strings.Join(out, ",")
}
