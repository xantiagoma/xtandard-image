package sign_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// Key material from the imgproxy docs and examples (Apache-2.0).
const (
	// docs.imgproxy.net "Signing the URL": key "secret", salt "hello".
	docsKey  = "736563726574"
	docsSalt = "68656C6C6F"
	docsPath = "/rs:fill:300:400:0/g:sm/aHR0cDovL2V4YW1w/bGUuY29tL2ltYWdl/cy9jdXJpb3NpdHku/anBn.png"
	docsSig  = "oKfUtW34Dvo2BGQehJFR4Nr0_rIjOtdtzJ3QFsUcXH8"

	// imgproxy examples/signature.* and signature-truncated.mjs.
	exampleKey    = "943b421c9eb07c830af81030552c86009268de4e532ba2ee2eab8247c6da0881"
	exampleSalt   = "520f986b998545b4785e0defbc4f3c1203f22de2374a3d53cb7a7fe9fea309c5"
	examplePath   = "/rs:fit:300:300/plain/http://img.example.com/pretty/image.jpg"
	exampleSig    = "m3k5QADfcKPDj-SDI2AIogZbC3FlAXszuwhtWXYqavc"
	exampleSig8   = "m3k5QADfcKM"
	exampleXIPath = "/imgix/photo.jpg"
	exampleXIQ    = "w=400&h=300"
	exampleXISig  = "hfEpzdduLUoMu7i8byFbffPk8lDHlZheutCf3sDNr4E"
)

func hexKeyring(tb testing.TB, pairs ...string) *sign.Keyring {
	tb.Helper()
	var keys, salts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		keys = append(keys, pairs[i])
		salts = append(salts, pairs[i+1])
	}
	r, err := sign.ParseHexKeyring(keys, salts)
	if err != nil {
		tb.Fatalf("ParseHexKeyring: %v", err)
	}
	return r
}

// rawKeyring builds a keyring from raw (not hex) secret/salt pairs, as
// imgproxy's own test suite does.
func rawKeyring(pairs ...string) *sign.Keyring {
	keys := make([]sign.Key, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		keys = append(keys, sign.NewKey([]byte(pairs[i]), []byte(pairs[i+1])))
	}
	return sign.NewKeyring(keys...)
}

func imgproxyVerifier(tb testing.TB, o sign.ImgproxyOptions) *sign.ImgproxyVerifier {
	tb.Helper()
	if o.Size == 0 {
		o.Size = sign.DefaultSignatureSize
	}
	v, err := sign.NewImgproxyVerifier(o)
	if err != nil {
		tb.Fatalf("NewImgproxyVerifier: %v", err)
	}
	return v
}

func signer(tb testing.TB, size int, keys *sign.Keyring) *sign.Signer {
	tb.Helper()
	scheme, err := sign.NewImgproxyScheme(size)
	if err != nil {
		tb.Fatalf("NewImgproxyScheme(%d): %v", size, err)
	}
	s, err := sign.NewSigner(scheme, keys)
	if err != nil {
		tb.Fatalf("NewSigner: %v", err)
	}
	return s
}

func mustSign(tb testing.TB, s *sign.Signer, msg string) string {
	tb.Helper()
	sig, err := s.Sign(msg)
	if err != nil {
		tb.Fatalf("Sign: %v", err)
	}
	return sig
}
