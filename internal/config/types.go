package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// redacted replaces every secret value in printed configuration.
const redacted = "[REDACTED]"

// ---- Secret ---------------------------------------------------------------

// Secret is a sensitive string (a password, token or URL with credentials).
// fmt, log/slog, encoding/json and YAML render a non-empty Secret as
// "[REDACTED]"; call Reveal to use the value.
type Secret string

// Reveal returns the secret value.
func (s Secret) Reveal() string { return string(s) }

// String implements fmt.Stringer without revealing the value.
func (s Secret) String() string { return redact(len(s)) }

// GoString implements fmt.GoStringer without revealing the value.
func (s Secret) GoString() string { return strconv.Quote(s.String()) }

// Format implements fmt.Formatter so no verb (%x, %d, …) reveals the value.
func (s Secret) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(s.String())) }

// LogValue implements slog.LogValuer.
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }

// MarshalText implements encoding.TextMarshaler without revealing the value.
func (s Secret) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (s *Secret) UnmarshalText(b []byte) error {
	*s = Secret(b)
	return nil
}

func redact(n int) string {
	if n == 0 {
		return ""
	}
	return redacted
}

// ---- HexKey ---------------------------------------------------------------

// HexKey is binary key material configured as hex (signing keys and salts,
// encryption keys). It prints as "[REDACTED]" like Secret.
type HexKey []byte

// String implements fmt.Stringer without revealing the key.
func (k HexKey) String() string { return redact(len(k)) }

// GoString implements fmt.GoStringer without revealing the key.
func (k HexKey) GoString() string { return strconv.Quote(k.String()) }

// Format implements fmt.Formatter so no verb (%x, %d, …) reveals the key.
func (k HexKey) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(k.String())) }

// LogValue implements slog.LogValuer.
func (k HexKey) LogValue() slog.Value { return slog.StringValue(k.String()) }

// MarshalText implements encoding.TextMarshaler without revealing the key.
func (k HexKey) MarshalText() ([]byte, error) { return []byte(k.String()), nil }

// UnmarshalText decodes hex. Errors never quote the input.
func (k *HexKey) UnmarshalText(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return errors.New("empty hex key")
	}
	v, err := hex.DecodeString(s)
	if err != nil {
		// hex errors quote the offending byte; keep key material out of logs.
		return errors.New("key is not valid hex")
	}
	*k = v
	return nil
}

// Hex returns the key hex-encoded (for components that take hex strings).
func (k HexKey) Hex() string { return hex.EncodeToString(k) }

// ---- ByteSize ------------------------------------------------------------

// ByteSize is a size in bytes. It parses plain integers and the binary
// suffixes KiB, MiB, GiB and TiB ("10MiB", "512 KiB", "1048576", "64B").
// Decimal suffixes (KB, MB, …) are rejected as ambiguous.
type ByteSize int64

var byteUnits = []struct {
	suffix string
	mult   int64
}{
	{"tib", 1 << 40},
	{"gib", 1 << 30},
	{"mib", 1 << 20},
	{"kib", 1 << 10},
	{"b", 1},
}

// ParseByteSize parses s as a ByteSize.
func ParseByteSize(s string) (ByteSize, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, errors.New("empty size")
	}
	lower := strings.ToLower(t)
	mult := int64(1)
	for _, u := range byteUnits {
		if num, ok := strings.CutSuffix(lower, u.suffix); ok {
			lower, mult = strings.TrimRight(num, " "), u.mult
			break
		}
	}
	if lower == "" {
		return 0, fmt.Errorf("size %q: missing number", t)
	}
	for _, c := range lower {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("size %q: want an integer with an optional KiB, MiB, GiB or TiB suffix", t)
		}
	}
	n, err := strconv.ParseInt(lower, 10, 64)
	if err != nil || n > math.MaxInt64/mult {
		return 0, fmt.Errorf("size %q: out of range", t)
	}
	return ByteSize(n * mult), nil
}

// String formats the size with the largest exact binary unit.
func (b ByteSize) String() string {
	if b <= 0 {
		return strconv.FormatInt(int64(b), 10)
	}
	for _, u := range []struct {
		name string
		mult int64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
		if int64(b)%u.mult == 0 {
			return strconv.FormatInt(int64(b)/u.mult, 10) + u.name
		}
	}
	return strconv.FormatInt(int64(b), 10)
}

// MarshalText implements encoding.TextMarshaler.
func (b ByteSize) MarshalText() ([]byte, error) { return []byte(b.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (b *ByteSize) UnmarshalText(t []byte) error {
	v, err := ParseByteSize(string(t))
	if err != nil {
		return err
	}
	*b = v
	return nil
}

// ---- Duration and CIDR parsers -------------------------------------------

// ParseDuration parses a Go duration ("5s", "1m30s"). Negative durations
// are rejected; "0" is allowed.
func ParseDuration(s string) (time.Duration, error) {
	t := strings.TrimSpace(s)
	d, err := time.ParseDuration(t)
	if err != nil {
		return 0, fmt.Errorf("duration %q: want Go syntax such as 5s or 1m30s", t)
	}
	if d < 0 {
		return 0, fmt.Errorf("duration %q: must not be negative", t)
	}
	return d, nil
}

// ParseCIDR parses a network in CIDR notation, or a single address (which
// becomes a /32 or /128). Host bits must be zero, zones and IPv4-mapped
// IPv6 forms are rejected, so every prefix has one canonical spelling.
func ParseCIDR(s string) (netip.Prefix, error) {
	t := strings.TrimSpace(s)
	var p netip.Prefix
	if strings.Contains(t, "/") {
		var err error
		if p, err = netip.ParsePrefix(t); err != nil {
			return netip.Prefix{}, fmt.Errorf("cidr %q: invalid", t)
		}
		if p != p.Masked() {
			return netip.Prefix{}, fmt.Errorf("cidr %q: host bits set, did you mean %s", t, p.Masked())
		}
	} else {
		a, err := netip.ParseAddr(t)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("cidr %q: invalid", t)
		}
		if a.Zone() != "" {
			return netip.Prefix{}, fmt.Errorf("cidr %q: zones are not allowed", t)
		}
		p = netip.PrefixFrom(a, a.BitLen())
	}
	if p.Addr().Is4In6() {
		return netip.Prefix{}, fmt.Errorf("cidr %q: use the plain IPv4 form", t)
	}
	return p, nil
}

// CIDR is a network parsed with ParseCIDR.
type CIDR netip.Prefix

// Prefix returns the network as a netip.Prefix.
func (c CIDR) Prefix() netip.Prefix { return netip.Prefix(c) }

// String implements fmt.Stringer.
func (c CIDR) String() string { return netip.Prefix(c).String() }

// MarshalText implements encoding.TextMarshaler.
func (c CIDR) MarshalText() ([]byte, error) { return []byte(c.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (c *CIDR) UnmarshalText(b []byte) error {
	p, err := ParseCIDR(string(b))
	if err != nil {
		return err
	}
	*c = CIDR(p)
	return nil
}

// ---- IntList -------------------------------------------------------------

// IntList is a list of non-negative integers written as comma-separated
// items, each a number ("16"), a range ("1-10") or a stepped range
// ("5-100/5" = 5, 10, …, 100). YAML also accepts a sequence of such items.
type IntList []int

// maxIntListLen bounds expansion so a typo like 1-1000000000 cannot
// allocate gigabytes.
const maxIntListLen = 10_000

// ParseIntList parses s as an IntList.
func ParseIntList(s string) (IntList, error) {
	var out IntList
	for item := range strings.SplitSeq(s, ",") {
		var err error
		if out, err = appendIntItem(out, item); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func appendIntItem(out IntList, item string) (IntList, error) {
	it := strings.TrimSpace(item)
	if it == "" {
		return nil, errors.New("empty list item")
	}
	rng, stepStr, hasStep := strings.Cut(it, "/")
	loStr, hiStr, isRange := strings.Cut(rng, "-")
	if hasStep && !isRange {
		return nil, fmt.Errorf("item %q: a step needs a range (a-b/step)", it)
	}
	lo, err := parseNonNeg(loStr)
	if err != nil {
		return nil, fmt.Errorf("item %q: %w", it, err)
	}
	hi, step := lo, 1
	if isRange {
		if hi, err = parseNonNeg(hiStr); err != nil {
			return nil, fmt.Errorf("item %q: %w", it, err)
		}
		if hi < lo {
			return nil, fmt.Errorf("item %q: range end below start", it)
		}
	}
	if hasStep {
		if step, err = parseNonNeg(stepStr); err != nil || step == 0 {
			return nil, fmt.Errorf("item %q: step must be a positive integer", it)
		}
	}
	if (hi-lo)/step+1 > maxIntListLen-len(out) {
		return nil, fmt.Errorf("item %q: expands to more than %d values", it, maxIntListLen)
	}
	for v := lo; v <= hi; v += step {
		out = append(out, v)
	}
	return out, nil
}

func parseNonNeg(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 || n > math.MaxInt32 {
		return 0, errors.New("want a non-negative integer")
	}
	return n, nil
}

// String formats the list compactly: runs of three or more values with a
// constant step become a-b/step (or a-b for step 1). ParseIntList reads it
// back to the same list.
func (l IntList) String() string {
	var sb strings.Builder
	for i := 0; i < len(l); {
		if sb.Len() > 0 {
			sb.WriteByte(',')
		}
		j := i + 1
		if j < len(l) && l[j] > l[i] {
			step := l[j] - l[i]
			for j+1 < len(l) && l[j+1]-l[j] == step {
				j++
			}
			if j-i >= 2 {
				sb.WriteString(strconv.Itoa(l[i]) + "-" + strconv.Itoa(l[j]))
				if step != 1 {
					sb.WriteString("/" + strconv.Itoa(step))
				}
				i = j + 1
				continue
			}
		}
		sb.WriteString(strconv.Itoa(l[i]))
		i++
	}
	return sb.String()
}

// MarshalText implements encoding.TextMarshaler with String.
func (l IntList) MarshalText() ([]byte, error) { return []byte(l.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler (env form).
func (l *IntList) UnmarshalText(b []byte) error {
	v, err := ParseIntList(string(b))
	if err != nil {
		return err
	}
	*l = v
	return nil
}

// UnmarshalYAML accepts a scalar ("5-100/5") or a sequence of items.
func (l *IntList) UnmarshalYAML(unmarshal func(any) error) error {
	var items []string
	if err := unmarshal(&items); err == nil {
		var out IntList
		for _, it := range items {
			if out, err = appendIntItem(out, it); err != nil {
				return err
			}
		}
		*l = out
		return nil
	}
	var s string
	if err := unmarshal(&s); err != nil {
		return errors.New("want a list of integers or a string such as 5-100/5")
	}
	return l.UnmarshalText([]byte(s))
}

// ---- Tristate ------------------------------------------------------------

// Tristate is a boolean whose default is derived from other settings.
// After Load it is always True or False.
type Tristate uint8

// Tristate values.
const (
	Auto Tristate = iota
	True
	False
)

// Bool reports whether t is True.
func (t Tristate) Bool() bool { return t == True }

// String implements fmt.Stringer.
func (t Tristate) String() string {
	switch t {
	case True:
		return "true"
	case False:
		return "false"
	default:
		return "auto"
	}
}

// MarshalText implements encoding.TextMarshaler.
func (t Tristate) MarshalText() ([]byte, error) { return []byte(t.String()), nil }

// UnmarshalText accepts auto and every strconv.ParseBool spelling.
func (t *Tristate) UnmarshalText(b []byte) error {
	s := strings.TrimSpace(string(b))
	if strings.EqualFold(s, "auto") {
		*t = Auto
		return nil
	}
	v, err := strconv.ParseBool(s)
	if err != nil {
		return fmt.Errorf("%q: want true, false or auto", s)
	}
	*t = False
	if v {
		*t = True
	}
	return nil
}

// ---- enums ---------------------------------------------------------------

// parseEnum lower-cases s and returns the matching allowed value.
func parseEnum[T ~string](s string, allowed ...T) (T, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	for _, a := range allowed {
		if v == string(a) {
			return a, nil
		}
	}
	names := make([]string, len(allowed))
	for i, a := range allowed {
		names[i] = string(a)
	}
	return "", fmt.Errorf("%q: want one of %s", v, strings.Join(names, ", "))
}

func unmarshalEnum[T ~string](dst *T, b []byte, allowed ...T) error {
	v, err := parseEnum(string(b), allowed...)
	if err != nil {
		return err
	}
	*dst = v
	return nil
}

// UnsignedPolicy governs requests without a verified signature.
type UnsignedPolicy string

// Unsigned policies. The empty value on a dialect means "inherit the global
// policy".
const (
	UnsignedAllow      UnsignedPolicy = "allow"
	UnsignedRestricted UnsignedPolicy = "restricted"
	UnsignedDeny       UnsignedPolicy = "deny"
)

// UnmarshalText implements encoding.TextUnmarshaler.
func (p *UnsignedPolicy) UnmarshalText(b []byte) error {
	return unmarshalEnum(p, b, unsignedPolicies()...)
}

// unsignedPolicies lists the UnsignedPolicy values (UnmarshalText and the
// config schema read it).
func unsignedPolicies() []UnsignedPolicy {
	return []UnsignedPolicy{UnsignedAllow, UnsignedRestricted, UnsignedDeny}
}

// SourceType is the backend of a named source.
type SourceType string

// Source types.
const (
	SourceHTTP  SourceType = "http"
	SourceS3    SourceType = "s3"
	SourceGCS   SourceType = "gcs"
	SourceAzure SourceType = "azure"
	SourceFS    SourceType = "fs"
	SourceFTP   SourceType = "ftp"
	SourceSFTP  SourceType = "sftp"
)

// UnmarshalText implements encoding.TextUnmarshaler.
func (t *SourceType) UnmarshalText(b []byte) error {
	return unmarshalEnum(t, b, sourceTypes()...)
}

// sourceTypes lists the SourceType values.
func sourceTypes() []SourceType {
	return []SourceType{SourceHTTP, SourceS3, SourceGCS, SourceAzure, SourceFS, SourceFTP, SourceSFTP}
}

// LogLevel is the minimum slog level.
type LogLevel string

// Log levels.
const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

// UnmarshalText implements encoding.TextUnmarshaler.
func (l *LogLevel) UnmarshalText(b []byte) error {
	return unmarshalEnum(l, b, logLevels()...)
}

// logLevels lists the LogLevel values.
func logLevels() []LogLevel { return []LogLevel{LogDebug, LogInfo, LogWarn, LogError} }

// Level returns the slog level.
func (l LogLevel) Level() slog.Level {
	switch l {
	case LogDebug:
		return slog.LevelDebug
	case LogWarn:
		return slog.LevelWarn
	case LogError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// FailOn is libvips' fail_on load option.
type FailOn string

// libvips fail_on levels.
const (
	FailOnNone      FailOn = "none"
	FailOnTruncated FailOn = "truncated"
	FailOnError     FailOn = "error"
	FailOnWarning   FailOn = "warning"
)

// UnmarshalText implements encoding.TextUnmarshaler.
func (f *FailOn) UnmarshalText(b []byte) error {
	return unmarshalEnum(f, b, failOns()...)
}

// failOns lists the FailOn values.
func failOns() []FailOn { return []FailOn{FailOnNone, FailOnTruncated, FailOnError, FailOnWarning} }

// AutoQualityMethod selects the auto-quality algorithm.
type AutoQualityMethod string

// Auto-quality methods.
const (
	AutoQualityNone  AutoQualityMethod = "none"
	AutoQualitySize  AutoQualityMethod = "size"
	AutoQualityDSSIM AutoQualityMethod = "dssim"
)

// UnmarshalText implements encoding.TextUnmarshaler.
func (m *AutoQualityMethod) UnmarshalText(b []byte) error {
	return unmarshalEnum(m, b, autoQualityMethods()...)
}

// autoQualityMethods lists the AutoQualityMethod values.
func autoQualityMethods() []AutoQualityMethod {
	return []AutoQualityMethod{AutoQualityNone, AutoQualitySize, AutoQualityDSSIM}
}

// StoreKind is a result cache tier.
type StoreKind string

// Result cache tiers.
const (
	StoreMemory StoreKind = "memory"
	StoreDisk   StoreKind = "disk"
	StoreRedis  StoreKind = "redis"
	StoreS3     StoreKind = "s3"
)

// UnmarshalText implements encoding.TextUnmarshaler.
func (s *StoreKind) UnmarshalText(b []byte) error {
	return unmarshalEnum(s, b, storeKinds()...)
}

// storeKinds lists the StoreKind values.
func storeKinds() []StoreKind { return []StoreKind{StoreMemory, StoreDisk, StoreRedis, StoreS3} }

// Format is an image format name.
type Format string

// Formats that may appear in configuration.
const (
	FormatJPEG Format = "jpeg"
	FormatPNG  Format = "png"
	FormatGIF  Format = "gif"
	FormatWebP Format = "webp"
	FormatAVIF Format = "avif"
	FormatHEIC Format = "heic"
	FormatJXL  Format = "jxl"
	FormatTIFF Format = "tiff"
	FormatBMP  Format = "bmp"
	FormatICO  Format = "ico"
	// FormatSVG is only meaningful as a source format (skip_processing).
	FormatSVG Format = "svg"
)

// UnmarshalText implements encoding.TextUnmarshaler; "jpg" means jpeg.
func (f *Format) UnmarshalText(b []byte) error {
	if strings.EqualFold(strings.TrimSpace(string(b)), formatJPGAlias) {
		*f = FormatJPEG
		return nil
	}
	return unmarshalEnum(f, b, formats()...)
}

// formatJPGAlias is accepted for FormatJPEG.
const formatJPGAlias = "jpg"

// formats lists the Format values (without the jpg alias).
func formats() []Format {
	return []Format{
		FormatJPEG, FormatPNG, FormatGIF, FormatWebP, FormatAVIF,
		FormatHEIC, FormatJXL, FormatTIFF, FormatBMP, FormatICO, FormatSVG,
	}
}

// DialectName names a URL dialect.
type DialectName string

// Dialect names (docs/URL-DIALECTS.md §1).
const (
	DialectImgproxy         DialectName = "imgproxy"
	DialectQuery            DialectName = "query"
	DialectWsrv             DialectName = "wsrv"
	DialectCloudflare       DialectName = "cloudflare"
	DialectCloudflareImages DialectName = "cloudflare_images"
	DialectImgix            DialectName = "imgix"
	DialectCloudinary       DialectName = "cloudinary"
	DialectImagekit         DialectName = "imagekit"
	DialectBunny            DialectName = "bunny"
	DialectThumbor          DialectName = "thumbor"
	DialectAWSSIH           DialectName = "awssih"
)

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *DialectName) UnmarshalText(b []byte) error {
	return unmarshalEnum(d, b, dialectNames()...)
}

// dialectNames lists the DialectName values.
func dialectNames() []DialectName {
	return []DialectName{
		DialectImgproxy, DialectQuery, DialectWsrv, DialectCloudflare,
		DialectCloudflareImages, DialectImgix, DialectCloudinary, DialectImagekit, DialectBunny, DialectThumbor, DialectAWSSIH,
	}
}
