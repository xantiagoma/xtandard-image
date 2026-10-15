package apidoc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// The language-neutral client specification (ADR 0013): everything an
// official URL builder or client needs that the server defines, exported
// from the same catalogues as the OpenAPI document. It is committed as
// clients/spec/spec.json; TestClientSpec fails when it drifts, and
// `go test ./internal/apidoc -run TestClientSpec -update` rewrites it. The
// per-language generators (clients/codegen) read only this file.

// ClientSpecVersion is the version of the spec.json format. It changes
// when a field is renamed or removed, not when the catalogues grow.
const ClientSpecVersion = 1

// ClientSpec is the document written to clients/spec/spec.json.
type ClientSpec struct {
	Comment     string            `json:"$comment"`
	SpecVersion int               `json:"spec_version"`
	Options     SpecCatalogues    `json:"options"`
	Query       SpecQuery         `json:"query"`
	Media       SpecMedia         `json:"media"`
	Sources     SpecSources       `json:"sources"`
	Signing     SpecSigning       `json:"signing"`
	Encryption  SpecEncryption    `json:"encryption"`
	URL         SpecURL           `json:"url"`
	ErrorKinds  []SpecErrorKind   `json:"error_kinds"`
	Responses   map[string]any    `json:"response_schemas"`
	Endpoints   map[string]string `json:"endpoints"`
}

// SpecCatalogues are the three imgproxy-grammar option catalogues.
type SpecCatalogues struct {
	Processing []SpecOption `json:"processing"`
	Info       []SpecOption `json:"info"`
	Video      []SpecOption `json:"video"`
}

// SpecOption is one option of a catalogue (imgproxy.Option).
type SpecOption struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
	// Short is the name builders write by default: the shortest of the
	// name and its aliases (ties: the canonical name, then the first alias
	// in byte order).
	Short     string     `json:"short"`
	Doc       string     `json:"doc"`
	Syntax    string     `json:"syntax"`
	Default   string     `json:"default"`
	Milestone string     `json:"milestone"`
	Scope     string     `json:"scope"`
	Pro       bool       `json:"pro,omitempty"`
	Security  bool       `json:"security,omitempty"`
	Removed   bool       `json:"removed,omitempty"`
	Shared    bool       `json:"shared,omitempty"`
	Example   []any      `json:"example,omitempty"`
	Forms     []SpecForm `json:"forms"`
}

// SpecForm is one accepted argument shape.
type SpecForm struct {
	Args     []SpecArg `json:"args"`
	Rest     []SpecArg `json:"rest,omitempty"`
	MinItems int       `json:"min_items"`
}

// SpecArg is one positional argument.
type SpecArg struct {
	Name string `json:"name"`
	// Type is string, integer, number, boolean, enum, color, base64 or
	// direction (down, up, left, right or a number in [-360, 360]).
	Type         string   `json:"type"`
	Enum         []string `json:"enum,omitempty"`
	Min          *float64 `json:"min,omitempty"`
	Max          *float64 `json:"max,omitempty"`
	ExclusiveMin bool     `json:"exclusive_min,omitempty"`
	MultipleOf   float64  `json:"multiple_of,omitempty"`
	Pattern      string   `json:"pattern,omitempty"`
	// Nullable arguments may be empty in a URL (null in an options object).
	Nullable bool   `json:"nullable"`
	Doc      string `json:"doc,omitempty"`
}

// SpecQuery describes the query dialect (URL-DIALECTS §4).
type SpecQuery struct {
	Mount string `json:"mount"`
	// ImageKeys are the native keys of /q/ that builders type, aliases
	// folded in; BuilderKeys are the ones builders write themselves.
	ImageKeys   []SpecQueryKey `json:"image_keys"`
	BuilderKeys []string       `json:"builder_keys"`
	// NativeVideoKeys are /video options whose query spelling is a native
	// key with another syntax, written by the alias instead.
	NativeVideoKeys map[string]string `json:"native_video_keys"`
	Imaginary       SpecImaginary     `json:"imaginary"`
}

// SpecQueryKey is one native query key.
type SpecQueryKey struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
	Doc     string   `json:"doc"`
}

// SpecImaginary lists the imaginary endpoints and parameters of /q/<endpoint>.
type SpecImaginary struct {
	Endpoints []string `json:"endpoints"`
	Params    []string `json:"params"`
}

// SpecMedia are the output formats, containers and stream settings.
type SpecMedia struct {
	ImageFormats    []string    `json:"image_formats"`
	VideoContainers []string    `json:"video_containers"`
	Streams         SpecStreams `json:"streams"`
}

// SpecStreams describes HLS and DASH stream URLs (URL-DIALECTS §3.10).
type SpecStreams struct {
	Protocols          []SpecStreamProtocol `json:"protocols"`
	SegmentFormats     []SpecSegmentFormat  `json:"segment_formats"`
	MaxRenditions      int                  `json:"max_renditions"`
	RenditionMin       int                  `json:"rendition_min"`
	RenditionMax       int                  `json:"rendition_max"`
	SegmentDurationMin int                  `json:"segment_duration_min"`
	SegmentDurationMax int                  `json:"segment_duration_max"`
}

// SpecStreamProtocol is a stream protocol and its manifest container.
type SpecStreamProtocol struct {
	Name        string `json:"name"`
	Container   string `json:"container"`
	ContentType string `json:"content_type"`
}

// SpecSegmentFormat is a stream segment container.
type SpecSegmentFormat struct {
	Name      string   `json:"name"`
	Aliases   []string `json:"aliases"`
	Protocols []string `json:"protocols"`
	Default   bool     `json:"default,omitempty"`
}

// SpecSources describes source references and encodings.
type SpecSources struct {
	Schemes []SpecScheme `json:"schemes"`
	// XINamePattern is what builders accept as a named source (xi://).
	XINamePattern string   `json:"xi_name_pattern"`
	Encodings     []string `json:"encodings"`
}

// SpecScheme is one source scheme and what its authority is.
type SpecScheme struct {
	Scheme    string `json:"scheme"`
	Authority string `json:"authority"`
	Example   string `json:"example"`
}

// SpecSigning are the signature rules.
type SpecSigning struct {
	Algorithm            string            `json:"algorithm"`
	Encoding             string            `json:"encoding"`
	MinSize              int               `json:"min_size"`
	MaxSize              int               `json:"max_size"`
	DefaultSize          int               `json:"default_size"`
	UnsignedPlaceholders []string          `json:"unsigned_placeholders"`
	DefaultUnsigned      string            `json:"default_unsigned"`
	QueryParam           string            `json:"query_param"`
	Rules                map[string]string `json:"rules"`
}

// SpecEncryption are the enc/ source and pdf_password token rules.
type SpecEncryption struct {
	Cipher               string   `json:"cipher"`
	KeySizes             []int    `json:"key_sizes"`
	IVSize               int      `json:"iv_size"`
	Token                string   `json:"token"`
	SourcePrefix         string   `json:"source_prefix"`
	MaxPDFPasswordBytes  int      `json:"max_pdf_password_bytes"`
	PDFPasswordNames     []string `json:"pdf_password_names"`
	PDFPasswordShortName string   `json:"pdf_password_short_name"`
	Rules                []string `json:"rules"`
}

// SpecURL are the grammar rules builders follow when writing URLs.
type SpecURL struct {
	DefaultSeparator         string   `json:"default_separator"`
	ForbiddenSeparators      []string `json:"forbidden_separators"`
	ForbiddenQuerySeparators []string `json:"forbidden_query_separators"`
	PipelineSeparator        string   `json:"pipeline_separator"`
	// PlainSafe and QuerySafe are the characters, besides ASCII letters
	// and digits, that a plain source and a query key or value keep
	// verbatim; every other character is percent-encoded as UTF-8 bytes
	// with upper-case hex.
	PlainSafe        string   `json:"plain_safe"`
	QuerySafe        string   `json:"query_safe"`
	ExtensionPattern string   `json:"extension_pattern"`
	Rules            []string `json:"rules"`
}

// SpecErrorKind is one row of the error status table.
type SpecErrorKind struct {
	Kind    string `json:"kind"`
	Status  int    `json:"status"`
	Message string `json:"message"`
}

// MarshalClientSpec returns clients/spec/spec.json, byte for byte.
func MarshalClientSpec() ([]byte, error) {
	s, err := BuildClientSpec()
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// BuildClientSpec assembles the client specification from the catalogues.
func BuildClientSpec() (*ClientSpec, error) {
	processing, info, video := imgproxy.ProcessingOptions(), imgproxy.InfoOptions(), imgproxy.VideoOptions()
	media, err := specMedia(processing, video)
	if err != nil {
		return nil, err
	}
	responses, err := specResponses()
	if err != nil {
		return nil, err
	}
	pdp := findOption(processing, "pdf_password")
	if pdp == nil {
		return nil, errors.New("apidoc: no pdf_password option in the processing catalogue")
	}
	return &ClientSpec{
		Comment: "Generated by internal/apidoc (BuildClientSpec) from the server's catalogues; DO NOT EDIT. " +
			"Regenerate: go test ./internal/apidoc -run TestClientSpec -update (task clients:spec). " +
			"Format: docs/adr/0013-official-clients.md, clients/README.md.",
		SpecVersion: ClientSpecVersion,
		Options: SpecCatalogues{
			Processing: specOptions(processing), Info: specOptions(info), Video: specOptions(video),
		},
		Query:      specQuery(),
		Media:      media,
		Sources:    specSources(),
		Signing:    specSigning(),
		Encryption: specEncryption(pdp),
		URL:        specURL(),
		ErrorKinds: specErrorKinds(),
		Responses:  responses,
		Endpoints: map[string]string{
			"process": "{base}{prefix}/{signature}/{options}/{source}",
			"info":    "{base}{prefix}/info/{signature}/{options}/{source}",
			"video":   "{base}{prefix}/video/{signature}/{options}/{source}",
			"query":   "{base}{mount}/?url={source}&{keys}[&o={options}][&format={ext}][&pdp={token}][&xi_sig={signature}]",
			"healthz": "/healthz",
			"readyz":  "/readyz",
		},
	}, nil
}

func findOption(opts []imgproxy.Option, name string) *imgproxy.Option {
	for i := range opts {
		if opts[i].Name == name {
			return &opts[i]
		}
	}
	return nil
}

// shortName is the name a builder writes by default.
func shortName(o *imgproxy.Option) string {
	short := o.Name
	for _, a := range slices.Sorted(slices.Values(o.Aliases)) {
		if len(a) < len(short) {
			short = a
		}
	}
	return short
}

func specOptions(opts []imgproxy.Option) []SpecOption {
	out := make([]SpecOption, 0, len(opts))
	for i := range opts {
		o := &opts[i]
		so := SpecOption{
			Name: o.Name, Aliases: append([]string{}, o.Aliases...), Short: shortName(o), Doc: o.Doc,
			Syntax: o.Syntax, Default: o.Default, Milestone: o.Milestone, Scope: o.Scope.String(),
			Pro: o.Pro, Security: o.Security, Removed: o.Removed, Shared: o.Shared,
		}
		if o.Example != "" {
			so.Example = o.Decode(strings.Split(o.Example, ":"))
		}
		for j := range o.Forms {
			f := &o.Forms[j]
			so.Forms = append(so.Forms, SpecForm{Args: specArgs(f.Args), Rest: specArgs(f.Rest), MinItems: max(f.MinItems, 1)})
		}
		out = append(out, so)
	}
	slices.SortFunc(out, func(a, b SpecOption) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func specArgs(in []imgproxy.Arg) []SpecArg {
	out := make([]SpecArg, 0, len(in))
	for i := range in {
		a := &in[i]
		sa := SpecArg{
			Name: a.Name, Type: argType(a.Type), Enum: a.Enum, ExclusiveMin: a.ExclusiveMin,
			MultipleOf: a.MultipleOf, Pattern: a.Pattern, Nullable: a.Optional, Doc: a.Doc,
		}
		if a.HasMin {
			sa.Min = new(a.Min)
		}
		if a.HasMax {
			sa.Max = new(a.Max)
		}
		out = append(out, sa)
	}
	return out
}

// queryBuilderKeys are the /q/ keys a builder writes itself: the source,
// the option path, the PDF password and the signatures.
func queryBuilderKeys() []string {
	return []string{"url", "o", "pdf_password", "pdp", "sign", "xi_sig"}
}

func specQuery() SpecQuery {
	q := SpecQuery{
		Mount:           "/q",
		BuilderKeys:     queryBuilderKeys(),
		NativeVideoKeys: map[string]string{"crop": "c", "flip": "fl"},
		Imaginary:       SpecImaginary{Endpoints: imaginaryEndpoints(), Params: imaginaryParams()},
	}
	index := map[string]int{}
	for _, kv := range queryKeys() {
		name, doc := kv[0], kv[1]
		if slices.Contains(q.BuilderKeys, name) {
			continue
		}
		if target, ok := strings.CutPrefix(doc, "Alias of "); ok {
			if i, ok := index[strings.TrimSuffix(target, ".")]; ok {
				q.ImageKeys[i].Aliases = append(q.ImageKeys[i].Aliases, name)
				continue
			}
		}
		index[name] = len(q.ImageKeys)
		q.ImageKeys = append(q.ImageKeys, SpecQueryKey{Name: name, Aliases: []string{}, Doc: doc})
	}
	return q
}

// firstEnum returns the enum of the first argument of an option's first form.
func firstEnum(opts []imgproxy.Option, name string) ([]string, error) {
	o := findOption(opts, name)
	if o == nil || len(o.Forms) == 0 || len(o.Forms[0].Args) == 0 || o.Forms[0].Args[0].Type != imgproxy.TypeEnum {
		return nil, fmt.Errorf("apidoc: option %s has no enum first argument", name)
	}
	return slices.Clone(o.Forms[0].Args[0].Enum), nil
}

// streamManifests are the manifest container and media type of each
// stream protocol (internal/video, URL-DIALECTS §3.10).
func streamManifests() map[string][2]string {
	return map[string][2]string{
		"hls":  {"m3u8", "application/vnd.apple.mpegurl"},
		"dash": {"mpd", "application/dash+xml"},
	}
}

// segmentFormats are the canonical segment formats, their aliases and
// the protocols that take them; the first is the default.
func segmentFormats() []SpecSegmentFormat {
	return []SpecSegmentFormat{
		{Name: "fmp4", Aliases: []string{"cmaf"}, Protocols: []string{"dash", "hls"}, Default: true},
		{Name: "ts", Aliases: []string{"mpegts"}, Protocols: []string{"hls"}},
	}
}

func specMedia(processing, video []imgproxy.Option) (SpecMedia, error) {
	var m SpecMedia
	var err error
	if m.ImageFormats, err = firstEnum(processing, "format"); err != nil {
		return m, err
	}
	if m.VideoContainers, err = firstEnum(video, "format"); err != nil {
		return m, err
	}
	protocols, err := firstEnum(video, "stream")
	if err != nil {
		return m, err
	}
	manifests := streamManifests()
	for _, p := range protocols {
		mf, ok := manifests[p]
		if !ok {
			return m, fmt.Errorf("apidoc: stream protocol %s has no manifest container in streamManifests", p)
		}
		if !slices.Contains(m.VideoContainers, mf[0]) {
			return m, fmt.Errorf("apidoc: manifest container %s is not a /video container", mf[0])
		}
		m.Streams.Protocols = append(m.Streams.Protocols, SpecStreamProtocol{Name: p, Container: mf[0], ContentType: mf[1]})
	}
	if len(protocols) != len(manifests) {
		return m, errors.New("apidoc: streamManifests lists a protocol the stream option does not take")
	}
	m.Streams.SegmentFormats = segmentFormats()
	values, err := firstEnum(video, "segment_format")
	if err != nil {
		return m, err
	}
	var names []string
	for _, f := range m.Streams.SegmentFormats {
		names = append(names, f.Name)
		names = append(names, f.Aliases...)
	}
	slices.Sort(names)
	slices.Sort(values)
	if !slices.Equal(names, values) {
		return m, fmt.Errorf("apidoc: segment formats %v differ from the segment_format option %v", names, values)
	}
	m.Streams.MaxRenditions = transform.MaxRenditions
	m.Streams.RenditionMin, m.Streams.RenditionMax = transform.MinRendition, transform.MaxRendition
	m.Streams.SegmentDurationMin = int(transform.MinSegmentDuration.Seconds())
	m.Streams.SegmentDurationMax = int(transform.MaxSegmentDuration.Seconds())
	return m, nil
}

// schemeAuthority says what the authority of a source scheme is and gives
// an example; every scheme a client may write is listed.
func schemeAuthority(s source.Scheme) (authority, example string, ok bool) {
	switch s {
	case source.SchemeXI:
		return "source name (XI_SOURCES)", "xi://photos/2026/cat.jpg", true
	case source.SchemeHTTP:
		return "host[:port]", "http://img.example.com/cat.jpg", true
	case source.SchemeHTTPS:
		return "host[:port]", "https://img.example.com/cat.jpg", true
	case source.SchemeS3:
		return "bucket", "s3://bucket/cat.jpg", true
	case source.SchemeGS:
		return "bucket", "gs://bucket/cat.jpg", true
	case source.SchemeAzure:
		return "container", "abs://container/cat.jpg", true
	case source.SchemeLocal:
		return "none (local:///<path>)", "local:///images/cat.jpg", true
	case source.SchemeFTP:
		return "host[:port]", "ftp://files.example.com/cat.jpg", true
	case source.SchemeSFTP:
		return "host[:port]", "sftp://files.example.com/cat.jpg", true
	case source.SchemeRelative, source.SchemePlayground:
		return "", "", false // a mount-relative key; the playground's own store
	default:
		return "", "", false
	}
}

func specSources() SpecSources {
	var schemes []SpecScheme
	for s := source.SchemeRelative; s <= source.SchemePlayground; s++ {
		if auth, ex, ok := schemeAuthority(s); ok {
			schemes = append(schemes, SpecScheme{Scheme: s.String(), Authority: auth, Example: ex})
		}
	}
	return SpecSources{
		Schemes:       schemes,
		XINamePattern: "^[A-Za-z0-9][A-Za-z0-9_.-]*$",
		Encodings:     []string{"base64", "plain", "encrypted"},
	}
}

func specSigning() SpecSigning {
	return SpecSigning{
		Algorithm:            "HMAC-SHA256",
		Encoding:             "base64url without padding (RFC 4648 §5)",
		MinSize:              sign.MinSignatureSize,
		MaxSize:              sign.MaxSignatureSize,
		DefaultSize:          sign.MaxSignatureSize,
		UnsignedPlaceholders: sign.ImgproxyPlaceholders(),
		DefaultUnsigned:      sign.ImgproxyPlaceholders()[0],
		QueryParam:           sign.XIParam,
		Rules: map[string]string{
			"mac": "sig(msg) = base64url(HMAC-SHA256(key, salt ‖ msg)[:size]); key and salt are hex (XI_SIGNING_KEYS, " +
				"XI_SIGNING_SALTS) or raw bytes; a msg without a leading \"/\" is signed as \"/\" + msg.",
			"imgproxy": "path = \"/\" + options + \"/\" + source; URL = base + \"/\" + sig(path) + path.",
			"info":     "path as imgproxy; URL = base + \"/info/\" + sig(path) + path (the signature does not cover \"/info\").",
			"video": "path as imgproxy; URL = base + \"/video/\" + sig(\"/video\" + path) + path: the signature covers " +
				"\"/video\" + path, so no image signature is valid for a /video URL.",
			"xi_sig": "msg = path after the mount (\"/\", \"/video\", \"/resize\") + \"?\" + the query's non-empty " +
				"parameters except xi_sig, in order and as written (no \"?\" when none remain); the URL ends in " +
				"\"&xi_sig=\" + sig(msg). Re-signing drops any earlier xi_sig; two xi_sig parameters are an error.",
			"unsigned": "without a key the signature segment is a placeholder (default \"insecure\") and the query " +
				"dialect has no xi_sig.",
		},
	}
}

func specEncryption(pdp *imgproxy.Option) SpecEncryption {
	return SpecEncryption{
		Cipher:               "AES-CBC, PKCS#7 padding",
		KeySizes:             []int{16, 24, 32},
		IVSize:               16,
		Token:                "base64url_nopad(IV ‖ AES-CBC(key, IV, PKCS7(utf8(plaintext))))",
		SourcePrefix:         "enc/",
		MaxPDFPasswordBytes:  transform.MaxPasswordLen,
		PDFPasswordNames:     pdp.Names(),
		PDFPasswordShortName: shortName(pdp),
		Rules: []string{
			"The key is the first of XI_ENCRYPTION_KEYS, hex or raw bytes (16, 24 or 32 bytes).",
			"The IV is 16 random bytes per token; a fixed IV (deterministic mode) is allowed for tests and cache keys.",
			"An encrypted source is \"enc/\" + token, split like base64 sources, with \".\" + extension after it.",
			"A plaintext must be non-empty and free of ASCII control characters (C0 and DEL).",
			"pdf_password: the token over the UTF-8 password (at most max_pdf_password_bytes) is the first option " +
				"segment, written verbatim (a separator inside it is joined back by the server); in the query dialect " +
				"it is the pdp (or pdf_password) key after the other keys. It needs both an encryption key and " +
				"signing: the server refuses it on unsigned URLs. /video URLs take none.",
		},
	}
}

func specURL() SpecURL {
	return SpecURL{
		DefaultSeparator:         ":",
		ForbiddenSeparators:      []string{"/", "-", "%"},
		ForbiddenQuerySeparators: []string{"/", "-", "%", "&", "="},
		PipelineSeparator:        "-",
		PlainSafe:                "-._~!$&'()*+,;=:/",
		QuerySafe:                "-._~:/,@!$'()*",
		ExtensionPattern:         "^[A-Za-z0-9]+$",
		Rules: []string{
			"An option segment is name + sep + args joined by sep; an option needs at least one argument.",
			"Arguments: booleans are 1/0, null and absent are empty, numbers use the shortest round-trip decimal " +
				"(ECMAScript Number::toString: 300, 1.5, 0.1, 1e+21), strings are written verbatim and must not " +
				"contain \"/\", the separator, \"?\", \"#\", whitespace or control characters.",
			"Option names: \"short\" writes each option's short name (default), \"canonical\" its name, \"given\" the " +
				"name as the caller wrote it (a name or an alias). Unknown names are an error.",
			"Options objects are written in insertion order; a list of objects chains pipelines with \"/-/\"; empty " +
				"pipelines are dropped. /info and /video URLs have one pipeline.",
			"Sources: base64 (default) is base64url_nopad(utf8(source)) + \".\" + ext; plain is \"plain/\" + the source " +
				"percent-encoded except letters, digits and plain_safe (UTF-8, upper-case hex) + \"@\" + ext; encrypted is \"enc/\" + token + " +
				"\".\" + ext. split_source N splits base64 and encrypted tokens into \"/\"-joined chunks of N characters.",
			"A source is non-empty and has no ASCII control characters. Extensions match extension_pattern.",
			"The base URL loses trailing slashes; without one, URLs are root-relative.",
			"Query dialect: keys and values are percent-encoded except letters, digits and query_safe; native values: booleans true/false, " +
				"numbers as above; null/absent keys are skipped; order: url, native keys, o, format, pdp, xi_sig.",
			"/q/video: every /video option is a key (short, canonical or given name; crop and flip as native_video_keys) " +
				"whose arguments are joined by \",\" and may not contain \":\"; then format.",
			"Streams: a /video URL with the protocol's manifest container as extension; renditions add " +
				"stream:<protocol>:<h>…, segment_duration adds sgd, a non-default segment format adds sgf (ts is HLS only); " +
				"stream, segment_duration, segment_format and format may not also be given as options.",
			"Imaginary: /q/<endpoint>?url=…&<param>=…; endpoint matches ^[a-z]+$ and is not \"video\".",
		},
	}
}

func specErrorKinds() []SpecErrorKind {
	kinds := errs.Kinds()
	out := make([]SpecErrorKind, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, SpecErrorKind{Kind: k.String(), Status: errs.Status(k), Message: errs.PublicMessage(k)})
	}
	return out
}

// specResponses returns the JSON Schemas of the JSON bodies a client
// decodes, from the committed OpenAPI document: InfoResponse,
// ErrorResponse and every schema they reference.
func specResponses() (map[string]any, error) {
	var doc struct {
		Components struct {
			Schemas map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(Spec(), &doc); err != nil {
		return nil, err
	}
	out := map[string]any{}
	queue := []string{schemaInfoResponse, schemaErrorResponse}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if _, ok := out[name]; ok {
			continue
		}
		s, ok := doc.Components.Schemas[name]
		if !ok {
			return nil, fmt.Errorf("apidoc: schema %s is not in openapi.json", name)
		}
		out[name] = s
		queue = append(queue, schemaRefs(s)...)
	}
	return out, nil
}

// schemaRefs returns the component names a schema references.
func schemaRefs(v any) []string {
	var out []string
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if ref, ok := x.(string); ok && k == "$ref" {
				out = append(out, strings.TrimPrefix(ref, schemaRefPrefix))
				continue
			}
			out = append(out, schemaRefs(x)...)
		}
	case []any:
		for _, x := range t {
			out = append(out, schemaRefs(x)...)
		}
	}
	return out
}
