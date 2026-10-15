package xtandard_test

// clients/spec/conformance.json: the cases every official client runs (ADR
// 0013). Positive cases must produce expect.value byte for byte; error cases
// must fail with a *BuildError of code expect.error. Each case runs twice
// where both forms exist: through the params structs (with xtandard.Opt
// values) and through the fluent chains (Option), and both must agree.

import (
	"fmt"
	"testing"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
	"github.com/xantiagoma/xtandard-image/clients/go/imgproxy"
	"github.com/xantiagoma/xtandard-image/clients/go/query"
)

type confKeys struct {
	Signing    struct{ Key, Salt string } `json:"signing"`
	Encryption struct{ Key, IV string }   `json:"encryption"`
}

type confCase struct {
	Name   string         `json:"name"`
	Op     string         `json:"op"`
	Config map[string]any `json:"config"`
	Input  map[string]any `json:"input"`
	Expect struct {
		Value *string `json:"value"`
		Error *string `json:"error"`
	} `json:"expect"`
}

type conformance struct {
	Keys  confKeys   `json:"keys"`
	Cases []confCase `json:"cases"`
}

// skipError marks a case the typed Go API cannot express.
type skipError string

func (e skipError) Error() string { return string(e) }

// run is one case's environment: the fixture keys and the case.
type run struct {
	keys confKeys
	c    confCase
}

func (r run) str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func (r run) has(m map[string]any, k string) bool {
	_, ok := m[k]
	return ok
}

func (r run) signing() (*xtandard.SigningKey, error) {
	s, ok := r.c.Config["signing"]
	if !ok {
		return nil, nil
	}
	key, salt := r.keys.Signing.Key, r.keys.Signing.Salt
	if m, ok := s.(map[string]any); ok {
		key, salt = r.str(m, "key"), r.str(m, "salt")
	}
	size := xtandard.DefaultSignatureSize
	if v, ok := r.c.Config["signature_size"].(float64); ok {
		size = int(v)
	}
	k, err := xtandard.ParseSigningKey(key, salt, size)
	return &k, err
}

func (r run) encryption() (*xtandard.EncryptionKey, error) {
	e, ok := r.c.Config["encryption"]
	if !ok {
		return nil, nil
	}
	key, iv := r.keys.Encryption.Key, r.keys.Encryption.IV
	if m, ok := e.(map[string]any); ok {
		key, iv = r.str(m, "key"), r.str(m, "iv")
	}
	k, err := xtandard.ParseEncryptionKey(key, iv)
	return &k, err
}

func (r run) imgproxy() (*imgproxy.Builder, error) {
	sk, err := r.signing()
	if err != nil {
		return nil, err
	}
	ek, err := r.encryption()
	if err != nil {
		return nil, err
	}
	cfg := r.c.Config
	split, _ := cfg["split_source"].(float64)
	return imgproxy.New(imgproxy.Config{
		BaseURL:           r.str(cfg, "base_url"),
		Signing:           sk,
		UnsignedSignature: r.str(cfg, "unsigned_signature"),
		Encryption:        ek,
		SourceEncoding:    xtandard.SourceEncoding(r.str(cfg, "source_encoding")),
		SplitSource:       int(split),
		Separator:         r.str(cfg, "separator"),
		OptionNames:       xtandard.OptionNames(r.str(cfg, "option_names")),
	})
}

func (r run) query() (*query.Builder, error) {
	sk, err := r.signing()
	if err != nil {
		return nil, err
	}
	ek, err := r.encryption()
	if err != nil {
		return nil, err
	}
	cfg := r.c.Config
	return query.New(query.Config{
		BaseURL:     r.str(cfg, "base_url"),
		Mount:       r.str(cfg, "mount"),
		Signing:     sk,
		Encryption:  ek,
		Separator:   r.str(cfg, "separator"),
		OptionNames: xtandard.OptionNames(r.str(cfg, "option_names")),
	})
}

// pipelines returns input.options as Options, one slice per pipeline.
func (r run) pipelines() [][]xtandard.Option {
	raw, _ := r.c.Input["options"].([]any)
	out := make([][]xtandard.Option, 0, len(raw))
	for _, pl := range raw {
		entries, _ := pl.([]any)
		p := make([]xtandard.Option, 0, len(entries))
		for _, e := range entries {
			pair, _ := e.([]any)
			name, _ := pair[0].(string)
			args, _ := pair[1].([]any)
			p = append(p, xtandard.Opt(name, args...))
		}
		out = append(out, p)
	}
	return out
}

func asProcessing(p []xtandard.Option) []xtandard.ProcessingOption {
	out := make([]xtandard.ProcessingOption, len(p))
	for i, o := range p {
		out[i] = o
	}
	return out
}

func asInfo(p []xtandard.Option) []xtandard.InfoOption {
	out := make([]xtandard.InfoOption, len(p))
	for i, o := range p {
		out[i] = o
	}
	return out
}

func asVideo(p []xtandard.Option) []xtandard.VideoOption {
	out := make([]xtandard.VideoOption, len(p))
	for i, o := range p {
		out[i] = o
	}
	return out
}

func (r run) params() []query.Param {
	raw, _ := r.c.Input["params"].([]any)
	out := make([]query.Param, 0, len(raw))
	for _, p := range raw {
		pair, _ := p.([]any)
		key, _ := pair[0].(string)
		out = append(out, query.P(key, pair[1]))
	}
	return out
}

func (r run) password() *string {
	if s, ok := r.c.Input["pdf_password"].(string); ok {
		return &s
	}
	return nil
}

func (r run) renditions() []int {
	raw, _ := r.c.Input["renditions"].([]any)
	out := make([]int, 0, len(raw))
	for _, v := range raw {
		f, _ := v.(float64)
		out = append(out, int(f))
	}
	return out
}

func (r run) segmentDuration() int {
	f, _ := r.c.Input["segment_duration"].(float64)
	return int(f)
}

// single returns the one pipeline of an /info or /video case, or a skip for
// chained ones: InfoParams and VideoParams take one pipeline, so Go's typed
// API cannot express the error case.
func (r run) single() ([]xtandard.Option, error) {
	pls := r.pipelines()
	switch len(pls) {
	case 0:
		return nil, nil
	case 1:
		return pls[0], nil
	}
	return nil, skipError("not expressible: InfoParams and VideoParams take one pipeline, so chained /info and /video URLs cannot be written")
}

func (r run) rangeValue() xtandard.Range {
	switch v := r.c.Input["range"].(type) {
	case string:
		return xtandard.RawRange(v)
	case map[string]any:
		if n, ok := v["suffix"].(float64); ok {
			return xtandard.ByteSuffix{Length: int64(n)}
		}
		start, _ := v["start"].(float64)
		if end, ok := v["end"].(float64); ok {
			return xtandard.ByteRange{Start: int64(start), End: int64(end)}
		}
		return xtandard.ByteRangeFrom{Start: int64(start)}
	}
	return nil
}

// viaParams runs the case through the params structs and the helper functions.
func (r run) viaParams() (string, error) {
	in := r.c.Input
	enc := xtandard.SourceEncoding(r.str(in, "source_encoding"))
	switch r.c.Op {
	case "imgproxy.url":
		b, err := r.imgproxy()
		if err != nil {
			return "", err
		}
		pls := r.pipelines()
		p := imgproxy.Params{Extension: r.str(in, "extension"), SourceEncoding: enc, PDFPassword: r.password()}
		for i, pl := range pls {
			if i == 0 {
				p.Options = asProcessing(pl)
			} else {
				p.Pipelines = append(p.Pipelines, asProcessing(pl))
			}
		}
		return b.URL(r.str(in, "source"), p)
	case "imgproxy.info_url":
		opts, err := r.single()
		if err != nil {
			return "", err
		}
		b, err := r.imgproxy()
		if err != nil {
			return "", err
		}
		return b.InfoURL(r.str(in, "source"), imgproxy.InfoParams{Options: asInfo(opts), SourceEncoding: enc, PDFPassword: r.password()})
	case "imgproxy.video_url":
		if r.has(in, "pdf_password") {
			return "", skipError("not expressible: VideoParams has no PDFPassword field (/video URLs take none)")
		}
		opts, err := r.single()
		if err != nil {
			return "", err
		}
		b, err := r.imgproxy()
		if err != nil {
			return "", err
		}
		return b.VideoURL(r.str(in, "source"), imgproxy.VideoParams{Options: asVideo(opts), Extension: r.str(in, "extension"), SourceEncoding: enc})
	case "imgproxy.stream_url":
		opts, err := r.single()
		if err != nil {
			return "", err
		}
		b, err := r.imgproxy()
		if err != nil {
			return "", err
		}
		return b.StreamURL(r.str(in, "source"), imgproxy.StreamParams{
			Protocol:        xtandard.StreamProtocol(r.str(in, "protocol")),
			Renditions:      r.renditions(),
			SegmentDuration: r.segmentDuration(),
			SegmentFormat:   xtandard.SegmentFormat(r.str(in, "segment_format")),
			Options:         asVideo(opts),
			SourceEncoding:  enc,
		})
	case "query.url":
		b, err := r.query()
		if err != nil {
			return "", err
		}
		p := query.Params{Params: r.params(), Extension: r.str(in, "extension"), PDFPassword: r.password()}
		for i, pl := range r.pipelines() {
			if i == 0 {
				p.Options = asProcessing(pl)
			} else {
				p.Pipelines = append(p.Pipelines, asProcessing(pl))
			}
		}
		return b.URL(r.str(in, "source"), p)
	case "query.video_url":
		b, err := r.query()
		if err != nil {
			return "", err
		}
		// Several pipelines are written in order: a []VideoOption may repeat keys.
		var opts []xtandard.Option
		for _, pl := range r.pipelines() {
			opts = append(opts, pl...)
		}
		return b.VideoURL(r.str(in, "source"), query.VideoParams{Options: asVideo(opts), Extension: r.str(in, "extension")})
	case "query.stream_url":
		opts, err := r.single()
		if err != nil {
			return "", err
		}
		b, err := r.query()
		if err != nil {
			return "", err
		}
		return b.StreamURL(r.str(in, "source"), query.StreamParams{
			Protocol:        xtandard.StreamProtocol(r.str(in, "protocol")),
			Renditions:      r.renditions(),
			SegmentDuration: r.segmentDuration(),
			SegmentFormat:   xtandard.SegmentFormat(r.str(in, "segment_format")),
			Options:         asVideo(opts),
		})
	case "query.imaginary_url":
		b, err := r.query()
		if err != nil {
			return "", err
		}
		return b.ImaginaryURL(xtandard.ImaginaryEndpoint(r.str(in, "endpoint")), r.str(in, "source"), r.params()...)
	case "sign":
		k, err := r.signing()
		if err != nil {
			return "", err
		}
		return xtandard.Sign(r.str(in, "message"), *k), nil
	case "sign_xi_url":
		k, err := r.signing()
		if err != nil {
			return "", err
		}
		return xtandard.SignXiURL(r.str(in, "url"), *k, r.str(in, "mount"))
	case "encrypt_source":
		k, err := r.encryption()
		if err != nil {
			return "", err
		}
		return xtandard.EncryptSource(r.str(in, "source"), *k)
	case "encrypt_pdf_password":
		k, err := r.encryption()
		if err != nil {
			return "", err
		}
		return xtandard.EncryptPDFPassword(r.str(in, "password"), *k)
	case "xi_source":
		return xtandard.XiSource(r.str(in, "name"), r.str(in, "key"))
	case "source_url":
		return xtandard.SourceURL(xtandard.SourceScheme(r.str(in, "scheme")), r.str(in, "authority"), r.str(in, "key"))
	case "range_header":
		return xtandard.RangeHeader(r.rangeValue())
	case "escape_plain_source":
		return xtandard.EscapePlainSource(r.str(in, "value")), nil
	case "escape_query_value":
		return xtandard.EscapeQueryValue(r.str(in, "value")), nil
	}
	return "", fmt.Errorf("unknown op %s", r.c.Op)
}

// chain is the common surface of the fluent chains the runner drives.
type chain interface {
	Build() (string, error)
}

// viaFluent runs the case through the fluent chains; ok is false for the ops
// (and chained /info and /video cases) that have no fluent form.
func (r run) viaFluent() (url string, ok bool, err error) {
	in := r.c.Input
	pls := r.pipelines()
	enc := xtandard.SourceEncoding(r.str(in, "source_encoding"))
	ext := r.str(in, "extension")
	pw := r.password()
	var c chain
	switch r.c.Op {
	case "imgproxy.url":
		b, err := r.imgproxy()
		if err != nil {
			return "", true, err
		}
		ch := b.Image(r.str(in, "source"))
		for i, pl := range pls {
			if i > 0 {
				ch.Pipeline()
			}
			for _, o := range pl {
				ch.Option(o.Name, o.Args...)
			}
		}
		if enc != "" {
			ch.Encoding(enc)
		}
		ch.Extension(ext)
		if pw != nil {
			ch.WithPDFPassword(*pw)
		}
		c = ch
	case "imgproxy.info_url":
		if len(pls) > 1 {
			return "", false, nil
		}
		b, err := r.imgproxy()
		if err != nil {
			return "", true, err
		}
		ch := b.Info(r.str(in, "source"))
		for _, pl := range pls {
			for _, o := range pl {
				ch.Option(o.Name, o.Args...)
			}
		}
		if enc != "" {
			ch.Encoding(enc)
		}
		if pw != nil {
			ch.WithPDFPassword(*pw)
		}
		c = ch
	case "imgproxy.video_url":
		if len(pls) > 1 || pw != nil {
			return "", false, nil
		}
		b, err := r.imgproxy()
		if err != nil {
			return "", true, err
		}
		ch := b.Video(r.str(in, "source"))
		for _, pl := range pls {
			for _, o := range pl {
				ch.Option(o.Name, o.Args...)
			}
		}
		if enc != "" {
			ch.Encoding(enc)
		}
		ch.Extension(ext)
		c = ch
	case "query.url":
		b, err := r.query()
		if err != nil {
			return "", true, err
		}
		ch := b.Image(r.str(in, "source"))
		for _, p := range r.params() {
			ch.Param(p.Key, p.Value)
		}
		for i, pl := range pls {
			if i > 0 {
				ch.Pipeline()
			}
			for _, o := range pl {
				ch.Option(o.Name, o.Args...)
			}
		}
		ch.Extension(ext)
		if pw != nil {
			ch.WithPDFPassword(*pw)
		}
		c = ch
	case "query.video_url":
		b, err := r.query()
		if err != nil {
			return "", true, err
		}
		ch := b.Video(r.str(in, "source"))
		for _, pl := range pls {
			for _, o := range pl {
				ch.Option(o.Name, o.Args...)
			}
		}
		ch.Extension(ext)
		c = ch
	default:
		return "", false, nil
	}
	url, err = c.Build()
	return url, true, err
}

func TestConformance(t *testing.T) {
	t.Parallel()
	fx := loadJSON[conformance](t, "clients/spec/conformance.json")
	if len(fx.Cases) < 100 {
		t.Fatalf("only %d cases", len(fx.Cases))
	}
	ops := map[string]bool{}
	for _, c := range fx.Cases {
		ops[c.Op] = true
	}
	if len(ops) < 17 {
		t.Fatalf("only %d ops", len(ops))
	}
	for _, c := range fx.Cases {
		t.Run(c.Op+":"+c.Name, func(t *testing.T) {
			t.Parallel()
			r := run{keys: fx.Keys, c: c}
			got, err := r.viaParams()
			if skip, ok := err.(skipError); ok { //nolint:errorlint // skipError is returned unwrapped by the runner
				t.Skip(string(skip))
			}
			fluent, hasFluent, ferr := r.viaFluent()
			if c.Expect.Error != nil {
				wantCode(t, err, *c.Expect.Error)
				if hasFluent {
					wantCode(t, ferr, *c.Expect.Error)
				}
				return
			}
			if err != nil {
				t.Fatalf("params form: %v", err)
			}
			if got != *c.Expect.Value {
				t.Errorf("params form:\n got %s\nwant %s", got, *c.Expect.Value)
			}
			if hasFluent {
				if ferr != nil {
					t.Fatalf("fluent form: %v", ferr)
				}
				if fluent != *c.Expect.Value {
					t.Errorf("fluent form:\n got %s\nwant %s", fluent, *c.Expect.Value)
				}
			}
		})
	}
}
