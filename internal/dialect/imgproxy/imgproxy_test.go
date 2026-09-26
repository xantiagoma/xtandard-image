package imgproxy_test

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

const (
	src    = "/plain/https://example.com/a.jpg"
	srcB64 = "/aHR0cHM6Ly9leGFtcGxlLmNvbS9hLmpwZw" // https://example.com/a.jpg
)

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// TestGrammar covers the path grammar: signature segment, sources,
// extensions, redenormalisation, chained pipelines and separators.
func TestGrammar(t *testing.T) {
	t.Parallel()
	long := "/unsafe" + strings.Repeat("/w:1/-", transform.MaxPipelines-1) + "/w:1" + src
	tooLong := "/unsafe" + strings.Repeat("/w:1/-", transform.MaxPipelines) + "/w:1" + src
	runGolden(t, "grammar", []testCase{
		{name: "plain_basic", path: "/unsafe/rs:fit:300:300" + src},
		{name: "plain_no_options", path: "/unsafe" + src},
		{name: "plain_extension", path: "/unsafe/w:10" + src + "@webp"},
		{name: "plain_extension_jpg_alias", path: "/unsafe" + src + "@jpg"},
		{name: "plain_extension_empty", path: "/unsafe" + src + "@"},
		{name: "plain_extension_invalid", path: "/unsafe" + src + "@xyz"},
		{name: "plain_multiple_extensions", path: "/unsafe" + src + "@png@webp"},
		{name: "plain_percent_decoded", path: "/unsafe/w:10/plain/https://example.com/a%20b.jpg%3Fv%3D1%40x@png"},
		{name: "plain_bad_escape", path: "/unsafe/plain/https://example.com/%zz.jpg"},
		{name: "plain_control_char", path: "/unsafe/plain/https://example.com/%00.jpg"},
		{name: "plain_empty", path: "/unsafe/plain/@webp"},
		{name: "plain_collapsed_scheme", path: "/unsafe/w:10/plain/https:/example.com/a.jpg"},
		{name: "plain_collapsed_local", path: "/unsafe/plain/local:/images/a.jpg"},
		{name: "plain_s3", path: "/unsafe/plain/s3://bucket/path/to/key.png@avif"},
		{name: "plain_query_dropped", path: "/unsafe/w:10" + src + "?utm=1"},
		{name: "plain_word_is_base64_without_url", path: "/unsafe/w:1/plain"},
		{name: "base64_single", path: "/unsafe/w:10" + srcB64},
		{name: "base64_extension", path: "/unsafe" + srcB64 + ".png"},
		{name: "base64_split_docs_example", path: "/unsafe" + docsPath},
		{name: "base64_padding", path: "/unsafe/" + base64.URLEncoding.EncodeToString([]byte("https://e.com/ab")) + ".webp"},
		{name: "base64_invalid", path: "/unsafe/w:1/!!notbase64"},
		{name: "base64_multiple_extensions", path: "/unsafe" + srcB64 + ".png.webp"},
		{name: "base64_empty_with_extension", path: "/unsafe/w:1/.png"},
		{name: "base64_control_chars", path: "/unsafe/" + b64("https://e.com/\x00")},
		{name: "base64_includes_filename", cfg: "filename", path: "/unsafe/w:10" + srcB64 + "/cute-cat.jpg"},
		{name: "base64_includes_filename_ext", cfg: "filename", path: "/unsafe" + srcB64 + ".png/cute.cat.jpg"},
		{name: "base64_includes_filename_single_segment", cfg: "filename", path: "/unsafe" + srcB64},
		{name: "enc_vector", cfg: "enc", path: "/unsafe/enc/" + encToken + ".png"},
		{name: "enc_vector_split", cfg: "enc", path: "/unsafe/rs:fill:10:10/enc/" + encToken[:20] + "/" + encToken[20:50] + "/" + encToken[50:]},
		{name: "enc_roundtrip_extension", cfg: "enc", path: "/unsafe/w:300/enc/ENC[s3://bucket/key with spaces.png].webp"},
		{name: "enc_with_filename", cfg: "filename", path: "/unsafe/enc/" + encToken + "/pretty.jpg"},
		{name: "enc_without_cipher", path: "/unsafe/enc/" + encToken},
		{name: "enc_bad_token", cfg: "enc", path: "/unsafe/enc/AAAAAAAA"},
		{name: "enc_word_alone_is_base64", cfg: "enc", path: "/unsafe/w:1/enc"},
		{name: "options_percent_encoded_colon", path: "/unsafe/rs%3Afill%3A10%3A20/q%3A50" + src},
		{name: "options_percent_encoded_colon_base64", path: "/unsafe/w%3A10" + srcB64},
		{name: "double_slash_after_signature", path: "/unsafe//w:10" + src},
		{name: "no_leading_slash", path: "unsafe/w:10" + src},
		{name: "missing_source", path: "/unsafe/w:10"},
		{name: "missing_source_trailing_slash", path: "/unsafe/w:10/"},
		{name: "only_signature", path: "/unsafe"},
		{name: "only_signature_slash", path: "/unsafe/"},
		{name: "empty_path", path: ""},
		{name: "root_path", path: "/"},
		{name: "deprecated_basic_format", path: "/unsafe/fill/300/400/sm/0" + srcB64},
		{name: "option_in_signature_position", path: "/rs:fit:300:300" + src},
		{name: "chained_docs_example", path: "/unsafe/rs:fit:500:500/wm:0.5:nowe/-/wm:0.7:soea/plain/s3://bucket/key.jpg"},
		{name: "chained_request_options_anywhere", path: "/unsafe/w:100/q:80/-/bl:2/f:webp/sm:0/-/rot:90/cb:x" + src},
		{name: "chained_max", path: long},
		{name: "chained_too_many", path: tooLong},
		{name: "chained_empty_first", path: "/unsafe/-/w:1" + src},
		{name: "chained_empty_last", path: "/unsafe/w:1/-" + src},
		{name: "chained_empty_middle", path: "/unsafe/w:1/-/-/w:2" + src},
		{name: "chained_background_alpha_per_pipeline", path: "/unsafe/bg:ff0000/bga:0.5/-/bg:00ff00" + src},
		{name: "separator_comma", cfg: "sep", path: "/unsafe/rs,fill,300,300/q,80/g,fp,0.5,0.5" + src},
		{name: "separator_comma_colon_is_source", cfg: "sep", path: "/unsafe/rs:fill:300:300" + src},
		{name: "separator_comma_chained", cfg: "sep", path: "/unsafe/w,10/-/h,20" + srcB64 + ".png"},
		{name: "base_url_relative_plain", cfg: "baseurl", path: "/unsafe/plain/images/a.jpg"},
		{name: "base_url_already_prefixed", cfg: "baseurl", path: "/unsafe/plain/https://cdn.example.com/images/a.jpg"},
		{name: "base_url_base64", cfg: "baseurl", path: "/unsafe/" + b64("images/b.png")},
		{name: "base_url_not_prepended_to_absolute", cfg: "baseurl", path: "/unsafe/plain/local:///k.png"},
		{name: "url_replacement_absolute", cfg: "baseurl", path: "/unsafe/plain/s3://legacy-bucket/k.png"},
		{name: "url_replacement_then_base_url", cfg: "baseurl", path: "/unsafe/plain/old/k.png"},
		{name: "extension_overrides_format", path: "/unsafe/f:png" + src + "@webp"},
		{name: "raw_ignores_extension", path: "/unsafe/raw:1" + src + "@webp"},
		{name: "raw_false_keeps_extension", path: "/unsafe/raw:0" + src + "@webp"},
		{name: "unknown_option_ignored", path: "/unsafe/foo:bar/w:10" + src},
		{name: "unknown_option_empty_name", path: "/unsafe/:x" + src},
		{name: "unknown_option_strict", cfg: "strict", path: "/unsafe/foo:bar" + src},
		{name: "later_option_strict_not_rejected", cfg: "strict", path: "/unsafe/a:10:1:1/jpgo:1" + src},
		{name: "ignored_deduplicated", path: "/unsafe/foo:1/foo:2/-/foo:3" + src},
	})
}

// TestSigning covers signature handling delegated to security/sign.
func TestSigning(t *testing.T) {
	t.Parallel()
	runGolden(t, "signing", []testCase{
		{name: "docs_vector", cfg: "signed", path: "/" + docsSig + docsPath},
		{name: "example_vector", cfg: "signed", path: "/" + exampleSig + examplePath},
		{name: "example_vector_truncated_8", cfg: "signed8", path: "/" + exampleSig8 + examplePath},
		{name: "example_vector_collapsed_scheme", cfg: "signed", path: "/" + exampleSig + "/rs:fit:300:300/plain/http:/img.example.com/pretty/image.jpg"},
		{name: "example_vector_percent_colon", cfg: "signed", path: "/" + exampleSig + "/rs%3Afit%3A300%3A300/plain/http://img.example.com/pretty/image.jpg"},
		{name: "signed_chained_enc", cfg: "signed", path: "/SIG/rs:fit:100:100/-/bl:2/enc/" + encToken + ".png"},
		{name: "signed_query_ignored", cfg: "signed", path: "/" + exampleSig + examplePath + "?x=1"},
		{name: "insecure_placeholder", cfg: "signed", path: "/insecure" + examplePath},
		{name: "unsafe_placeholder", cfg: "signed", path: "/unsafe" + examplePath},
		{name: "underscore_placeholder", cfg: "signed", path: "/_" + examplePath},
		{name: "trusted_signature", cfg: "signed", path: "/" + trustedSig + examplePath},
		{name: "wrong_signature", cfg: "signed", path: "/" + docsSig + examplePath},
		{name: "malformed_signature", cfg: "signed", path: "/not*base64" + examplePath},
		{name: "tampered_path", cfg: "signed", path: "/" + exampleSig + "/rs:fit:301:300/plain/http://img.example.com/pretty/image.jpg"},
		{name: "signature_too_long_for_size_8", cfg: "signed8", path: "/" + exampleSig + examplePath},
		{name: "required_rejects_placeholder", cfg: "required", path: "/insecure" + examplePath},
		{name: "required_accepts_signature", cfg: "required", path: "/" + exampleSig + examplePath},
		{name: "signing_off_any_segment", path: "/whatever-signature" + examplePath},
		{name: "signing_off_option_as_signature", path: "/w:10" + examplePath},
		{name: "signed_option_as_signature", cfg: "signed", path: "/rs:fit:300:300/plain/http://img.example.com/pretty/image.jpg"},
	})
}

func TestNewRejectsBadConfig(t *testing.T) {
	t.Parallel()
	for _, o := range []imgproxy.Options{
		{ArgumentsSeparator: "/"},
		{ArgumentsSeparator: "-"},
		{ArgumentsSeparator: "%"},
		{URLReplacements: []imgproxy.URLReplacement{{}}},
	} {
		if _, err := imgproxy.New(&o); err == nil {
			t.Errorf("New(%+v) succeeded", o)
		}
	}
}

func TestInterface(t *testing.T) {
	t.Parallel()
	f := getFixture(t)
	var d dialect.Dialect = f.parsers["signed"]
	if d.Name() != "imgproxy" {
		t.Errorf("Name = %q", d.Name())
	}
	if d.Verifier() == nil {
		t.Error("nil verifier")
	}
	if res, err := d.Verifier().Verify(exampleSig, examplePath); err != nil || res != sign.Verified {
		t.Errorf("Verifier().Verify = %v, %v", res, err)
	}
	p, err := imgproxy.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Verifier() == nil {
		t.Error("unsigned parser has nil verifier")
	}
}

// TestEncRoundTrip encrypts sources with crypt and parses them back in
// every enc/ form (split, extension, filename).
func TestEncRoundTrip(t *testing.T) {
	t.Parallel()
	f := getFixture(t)
	for _, plain := range []string{
		"https://example.com/a.jpg",
		"s3://bucket/some/key with spaces.png",
		"local:///images/ü.webp",
		"https://example.com/" + strings.Repeat("x", 500),
	} {
		tok, err := f.cipher.Encrypt([]byte(plain))
		if err != nil {
			t.Fatal(err)
		}
		mid := len(tok) / 2
		for _, tc := range []struct{ cfg, path, ext string }{
			{"enc", "/unsafe/enc/" + tok, ""},
			{"enc", "/unsafe/w:10/enc/" + tok[:mid] + "/" + tok[mid:] + ".webp", "webp"},
			{"filename", "/unsafe/enc/" + tok + ".png/name.jpg", "png"},
		} {
			res, err := f.parsers[tc.cfg].ParsePath(tc.path)
			if err != nil {
				t.Fatalf("%s: %v", tc.path, err)
			}
			if res.SourceString != plain || res.Request.Source.Encoding != transform.SourceEncrypted {
				t.Errorf("%s: source %q (%v), want %q", tc.path, res.SourceString, res.Request.Source.Encoding, plain)
			}
			if got := res.Request.Output.Format.String(); got != tc.ext {
				t.Errorf("%s: format %q, want %q", tc.path, got, tc.ext)
			}
		}
	}
}

// TestSignedRoundTrip signs generated paths and checks they verify, and
// that any single-byte change to the options breaks the signature.
func TestSignedRoundTrip(t *testing.T) {
	t.Parallel()
	f := getFixture(t)
	p := f.parsers["signed"]
	for i, rest := range []string{
		"/rs:fill:300:400:0/g:sm/plain/https://example.com/a.jpg@webp",
		"/w:100/-/bl:2" + srcB64 + ".png",
		"/pr:thumb/enc/" + encToken,
		"/q:80/plain/local:///x.png",
	} {
		sig, err := f.signer.Sign(rest)
		if err != nil {
			t.Fatal(err)
		}
		res, err := p.ParsePath("/" + sig + rest)
		if err != nil || res.Signature != sign.Verified {
			t.Errorf("%d: %v, %v", i, res, err)
		}
		tampered := strings.Replace(rest, ":", ":1", 1)
		if _, err := p.ParsePath("/" + sig + tampered); errs.KindOf(err) != errs.KindUnauthorized {
			t.Errorf("%d: tampered path: %v", i, err)
		}
	}
}

// TestArgumentErrorsNameField checks that invalid arguments are 400s whose
// public message names the field without echoing the value.
func TestArgumentErrorsNameField(t *testing.T) {
	t.Parallel()
	p := getFixture(t).parsers["default"]
	for opt, field := range map[string]string{
		"w:-5":           "width",
		"rs:fit:x":       "resize.width",
		"g:fp:2:0":       "gravity.x",
		"wm:1:xx":        "watermark.position",
		"bg:1:2:SECRET9": "background.b",
		"q:101":          "quality",
		"t:1:zzzzzz":     "trim.color",
	} {
		_, err := p.ParsePath("/unsafe/" + opt + src)
		ae, ok := errors.AsType[*dialect.ArgumentError](err)
		if !ok || ae.Field != field {
			t.Errorf("%s: err %v, want field %q", opt, err, field)
			continue
		}
		if errs.Status(errs.KindOf(err)) != 400 {
			t.Errorf("%s: status %d", opt, errs.Status(errs.KindOf(err)))
		}
		pub := err.(interface{ PublicMessage() string }).PublicMessage() //nolint:forcetypeassert // errs.Error
		if !strings.Contains(pub, field) || strings.Contains(pub, "SECRET9") {
			t.Errorf("%s: public message %q", opt, pub)
		}
	}
}

// TestExpiresClock checks expiry against the injected clock.
func TestExpiresClock(t *testing.T) {
	t.Parallel()
	p := getFixture(t).parsers["default"]
	for ts, wantErr := range map[int64]bool{
		now.Unix() - 1:  true,
		now.Unix():      false,
		now.Unix() + 60: false,
		0:               false,
		-5:              false,
	} {
		_, err := p.ParsePath("/unsafe/exp:" + strconv.FormatInt(ts, 10) + src)
		if (err != nil) != wantErr || (err != nil && !errors.Is(err, dialect.ErrExpired)) {
			t.Errorf("exp %d: %v", ts, err)
		}
	}
}
