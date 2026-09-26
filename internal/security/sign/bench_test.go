package sign_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

func BenchmarkImgproxyVerify(b *testing.B) {
	one := hexKeyring(b, exampleKey, exampleSalt)
	// Rotation worst case: the matching key is the last of three.
	three := hexKeyring(b, docsKey, docsSalt, docsKey+"00", docsSalt, exampleKey, exampleSalt)
	cases := []struct {
		name string
		keys *sign.Keyring
		sig  string
	}{
		{"1key", one, exampleSig},
		{"3keys_last", three, exampleSig},
		{"mismatch_1key", one, docsSig},
		{"trusted", one, "trusted-signature"},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			v := imgproxyVerifier(b, sign.ImgproxyOptions{Keys: c.keys, Trusted: []string{"trusted-signature"}})
			b.ReportAllocs()
			for b.Loop() {
				_, _ = v.Verify(c.sig, examplePath)
			}
		})
	}
}

func BenchmarkXIMessage(b *testing.B) {
	const q = "url=https%3A%2F%2Fexample.com%2Fa.jpg&w=640&q=75&xi_sig=" + exampleXISig
	b.ReportAllocs()
	for b.Loop() {
		_, _, _ = sign.XIMessage("/_next/image", q)
	}
}
