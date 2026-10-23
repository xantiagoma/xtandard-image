package imgproxy_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/dialect/query"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// The official clients' conformance fixture (ADR 0013,
// clients/spec/conformance.json): the TypeScript client writes the expected
// values (XI_UPDATE_FIXTURES=1 bun test src/conformance.test.ts); this test
// holds them to the server. Every URL must parse with the dialect's own
// parser to the case's source, with a verified signature when the case is
// signed (an imgproxy, /info, /video or xi_sig signature over exactly what
// the server signs), and with the stream settings and PDF password it
// asked for; tokens must decrypt to their plaintext; signatures must be
// the HMAC the server computes.
const conformanceFile = "../../../clients/spec/conformance.json"

type conformanceKeys struct {
	Signing    fixtureKey    `json:"signing"`
	Encryption fixtureCipher `json:"encryption"`
}

type conformanceCase struct {
	Name   string          `json:"name"`
	Op     string          `json:"op"`
	Config json.RawMessage `json:"config"`
	Input  map[string]any  `json:"input"`
	Expect struct {
		Value *string `json:"value"`
		Error string  `json:"error"`
	} `json:"expect"`
}

type conformanceConfig struct {
	BaseURL       string          `json:"base_url"`
	Signing       json.RawMessage `json:"signing"`
	SignatureSize int             `json:"signature_size"`
	Separator     string          `json:"separator"`
	Mount         string          `json:"mount"`
}

// conformanceErrors are the error codes a client may raise (clients/README.md).
func conformanceErrors() []string {
	return []string{
		"unknown_option", "invalid_argument", "invalid_source", "invalid_extension", "invalid_separator",
		"missing_signing", "missing_encryption", "invalid_key", "invalid_password", "invalid_request",
		"invalid_name", "invalid_endpoint", "invalid_range",
	}
}

func TestConformanceFixture(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(conformanceFile)
	if err != nil {
		t.Fatal(err)
	}
	var fix struct {
		Keys  conformanceKeys   `json:"keys"`
		Cases []conformanceCase `json:"cases"`
	}
	if err := json.Unmarshal(b, &fix); err != nil {
		t.Fatal(err)
	}
	if fix.Keys.Signing != (fixtureKey{Key: exampleKey, Salt: exampleSalt}) || fix.Keys.Encryption.Key != encKey ||
		fix.Keys.Encryption.IV != hex.EncodeToString([]byte(fixtureIV)) {
		t.Fatal("conformance keys differ from the dialect test keys")
	}
	if len(fix.Cases) < 100 {
		t.Fatalf("only %d conformance cases", len(fix.Cases))
	}
	f := getFixture(t)
	seen := map[string]bool{}
	for _, c := range fix.Cases {
		if seen[c.Name] {
			t.Fatalf("duplicate case %s", c.Name)
		}
		seen[c.Name] = true
		t.Run(c.Op+"/"+c.Name, func(t *testing.T) {
			t.Parallel()
			if c.Expect.Error != "" {
				if c.Expect.Value != nil || !slices.Contains(conformanceErrors(), c.Expect.Error) {
					t.Fatalf("error case with a value or an unknown code %q", c.Expect.Error)
				}
				return
			}
			if c.Expect.Value == nil {
				t.Fatal("no expect.value; run task clients:fixtures")
			}
			var cfg conformanceConfig
			if len(c.Config) > 0 {
				if err := json.Unmarshal(c.Config, &cfg); err != nil {
					t.Fatal(err)
				}
			}
			checkConformance(t, f, &c, &cfg, *c.Expect.Value)
		})
	}
}

func inputString(c *conformanceCase, key string) string {
	s, _ := c.Input[key].(string)
	return s
}

// signedWithFixtureKey reports whether a case signs with the fixture key.
func signedWithFixtureKey(t *testing.T, cfg *conformanceConfig) bool {
	t.Helper()
	switch string(cfg.Signing) {
	case "", "null", "false":
		return false
	case "true":
		return true
	default:
		t.Fatalf("positive case with a custom signing key %s", cfg.Signing)
		return false
	}
}

func fixtureMAC(t *testing.T, msg string, size int) string {
	t.Helper()
	key, err := hex.DecodeString(exampleKey)
	if err != nil {
		t.Fatal(err)
	}
	salt, err := hex.DecodeString(exampleSalt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(msg, "/") {
		msg = "/" + msg
	}
	m := hmac.New(sha256.New, key)
	m.Write(salt)
	m.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:size])
}

func checkConformance(t *testing.T, f *fixture, c *conformanceCase, cfg *conformanceConfig, value string) {
	t.Helper()
	signed := signedWithFixtureKey(t, cfg)
	size := cfg.SignatureSize
	if size == 0 {
		size = sign.MaxSignatureSize
	}
	keys, err := sign.ParseHexKeyring([]string{exampleKey}, []string{exampleSalt})
	if err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return now }
	group, _, _ := strings.Cut(c.Op, ".")
	switch c.Op {
	case "imgproxy.url", "imgproxy.info_url", "imgproxy.video_url", "imgproxy.stream_url":
		o := imgproxy.Options{Cipher: f.cipher, ArgumentsSeparator: cfg.Separator, Now: clock}
		if signed {
			if o.Verifier, err = sign.NewImgproxyVerifier(sign.ImgproxyOptions{Keys: keys, Size: size, Required: true}); err != nil {
				t.Fatal(err)
			}
		}
		p, err := imgproxy.New(&o)
		if err != nil {
			t.Fatal(err)
		}
		path, ok := strings.CutPrefix(value, strings.TrimRight(cfg.BaseURL, "/"))
		if !ok {
			t.Fatalf("%s does not start with the base URL %q", value, cfg.BaseURL)
		}
		parse := p.Parse
		switch c.Op {
		case "imgproxy.info_url":
			path, ok = strings.CutPrefix(path, "/info")
			parse = p.ParseInfo
		case "imgproxy.video_url", "imgproxy.stream_url":
			path, ok = strings.CutPrefix(path, "/video")
			parse = p.ParseVideo
		}
		if !ok {
			t.Fatalf("%s lacks its endpoint prefix", value)
		}
		res, err := parse(&dialect.Input{Path: path})
		if err != nil {
			t.Fatalf("%s: %v", value, err)
		}
		checkConformanceResult(t, c, res, signed, value)
	case "query.url", "query.video_url", "query.stream_url", "query.imaginary_url":
		o := query.Options{Cipher: f.cipher, ArgumentsSeparator: cfg.Separator, Now: clock}
		if signed {
			if o.XIVerifier, err = sign.NewXIVerifier(sign.XIOptions{Keys: keys, Size: size, Required: true}); err != nil {
				t.Fatal(err)
			}
		}
		p, err := query.New(&o)
		if err != nil {
			t.Fatal(err)
		}
		mount := strings.TrimRight(cfg.Mount, "/")
		if cfg.Mount == "" {
			mount = "/q"
		}
		rest, ok := strings.CutPrefix(value, strings.TrimRight(cfg.BaseURL, "/")+mount)
		if !ok {
			t.Fatalf("%s is not under %s%s", value, cfg.BaseURL, mount)
		}
		path, raw, _ := strings.Cut(rest, "?")
		res, err := p.Parse(&dialect.Input{Path: path, RawQuery: raw})
		if err != nil {
			t.Fatalf("%s: %v", value, err)
		}
		checkConformanceResult(t, c, res, signed, value)
	case "query.query_request":
		checkQueryRequest(t, f, c, cfg, value)
	case "sign":
		if want := fixtureMAC(t, inputString(c, "message"), size); value != want {
			t.Errorf("signature %s, server %s", value, want)
		}
	case "sign_xi_url":
		u, err := url.Parse(value)
		if err != nil {
			t.Fatal(err)
		}
		path := u.EscapedPath()
		if m := strings.TrimRight(inputString(c, "mount"), "/"); m != "" {
			path = strings.TrimPrefix(path, m)
		}
		if path == "" {
			path = "/"
		}
		sig, msg, err := sign.XIMessage(path, u.RawQuery)
		if err != nil {
			t.Fatal(err)
		}
		if want := fixtureMAC(t, msg, size); sig != want {
			t.Errorf("xi_sig %s over %q, server %s", sig, msg, want)
		}
	case "encrypt_source":
		pt, err := f.cipher.Decrypt(value)
		if err != nil || string(pt) != inputString(c, "source") {
			t.Errorf("token decrypts to %q, %v", pt, err)
		}
	case "encrypt_pdf_password":
		pt, err := f.cipher.DecryptPassword(value)
		if err != nil || string(pt) != inputString(c, "password") {
			t.Errorf("token decrypts to %q, %v", pt, err)
		}
	case "xi_source", "source_url":
		// Dialects do not import source (ARCHITECTURE §3): the reference must
		// at least be a URL with a scheme, and is what the dialects accept.
		if u, err := url.Parse(value); err != nil || u.Scheme == "" || !imgproxy.PlausibleSource(value) {
			t.Errorf("%s is not a source reference: %v", value, err)
		}
	case "escape_plain_source":
		if got, err := url.PathUnescape(value); err != nil || got != inputString(c, "value") {
			t.Errorf("%s unescapes to %q, %v", value, got, err)
		}
	case "escape_query_value":
		if got, err := url.QueryUnescape(value); err != nil || got != inputString(c, "value") {
			t.Errorf("%s unescapes to %q, %v", value, got, err)
		}
	case "range_header":
		if !strings.HasPrefix(value, "bytes=") {
			t.Errorf("range %q", value)
		}
	default:
		t.Fatalf("unknown op %s (group %s)", c.Op, group)
	}
}

// checkConformanceResult compares a parsed URL with the case it was built from.
func checkConformanceResult(t *testing.T, c *conformanceCase, res *dialect.Result, signed bool, value string) {
	t.Helper()
	if want := inputString(c, "source"); res.SourceString != want {
		t.Errorf("%s: source %q, want %q", value, res.SourceString, want)
	}
	if signed && res.Signature != sign.Verified {
		t.Errorf("%s: signature %v, want verified", value, res.Signature)
	}
	if pw := inputString(c, "pdf_password"); pw != "" {
		got := res.Request.Source.Password
		if got == nil || string(got.Secret) != pw {
			t.Errorf("%s: pdf password not decrypted", value)
		}
	}
	proto := inputString(c, "protocol")
	if proto == "" {
		return
	}
	v := res.Request.Video
	if v == nil || v.Stream == nil {
		t.Fatalf("%s: not a stream", value)
	}
	if (v.Stream.Protocol == transform.StreamDASH) != (proto == "dash") {
		t.Errorf("%s: protocol %v, want %s", value, v.Stream.Protocol, proto)
	}
	var renditions []int
	if rs, ok := c.Input["renditions"].([]any); ok {
		for _, r := range rs {
			n, _ := r.(float64)
			renditions = append(renditions, int(n))
		}
	}
	if !slices.Equal(v.Stream.Renditions, renditions) {
		t.Errorf("%s: renditions %v, want %v", value, v.Stream.Renditions, renditions)
	}
	if d, ok := c.Input["segment_duration"].(float64); ok && v.Stream.SegmentDuration != time.Duration(d)*time.Second {
		t.Errorf("%s: segment duration %v, want %vs", value, v.Stream.SegmentDuration, d)
	}
	if (v.Stream.Format == transform.SegmentTS) != (inputString(c, "segment_format") == "ts") {
		t.Errorf("%s: segment format %v", value, v.Stream.Format)
	}
}

// checkQueryRequest checks a query.query_request value, an HTTP QUERY
// request ("QUERY <url>\nContent-Type: …\n\n<content>", WP-147): the
// server turns its form content into the query the GET URL would carry
// (query.ContentQuery), which must parse like the URL built by
// input.build, signature included; the same parameters as JSON content
// must give the same query, so they carry the same xi_sig.
func checkQueryRequest(t *testing.T, f *fixture, c *conformanceCase, cfg *conformanceConfig, value string) {
	t.Helper()
	head, content, ok := strings.Cut(value, "\n\n")
	line, header, _ := strings.Cut(head, "\n")
	target, isQuery := strings.CutPrefix(line, "QUERY ")
	if !ok || !isQuery || header != "Content-Type: "+query.MediaTypeForm || strings.Contains(target, "?") {
		t.Fatalf("%q is not a QUERY request with form content", value)
	}
	raw, err := query.ContentQuery(query.MediaTypeForm, []byte(content))
	if err != nil {
		t.Fatalf("%s: content: %v", value, err)
	}
	built := *c
	built.Op = inputString(c, "build")
	if !strings.HasPrefix(built.Op, "query.") || built.Op == c.Op {
		t.Fatalf("input.build %q is not a query URL op", built.Op)
	}
	checkConformance(t, f, &built, cfg, target+"?"+raw)

	var obj strings.Builder
	seen := map[string]bool{}
	obj.WriteByte('{')
	for i, part := range strings.Split(raw, "&") {
		k, v, _ := strings.Cut(part, "=")
		k, errK := url.QueryUnescape(k)
		v, errV := url.QueryUnescape(v)
		if errK != nil || errV != nil {
			t.Fatalf("%s: escaping", value)
		}
		if seen[k] {
			return // a repeated key has no JSON object form
		}
		seen[k] = true
		if i > 0 {
			obj.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, _ := json.Marshal(v)
		obj.Write(kb)
		obj.WriteByte(':')
		obj.Write(vb)
	}
	obj.WriteByte('}')
	fromJSON, err := query.ContentQuery(query.MediaTypeJSON, []byte(obj.String()))
	if err != nil {
		t.Fatalf("%s: json content %s: %v", value, obj.String(), err)
	}
	if fromJSON != raw {
		t.Errorf("json content gives %q, form %q", fromJSON, raw)
	}
}
