package crypt

import "testing"

func BenchmarkEncrypt(b *testing.B) {
	c := mustNew(b, imgproxyKey)
	p := []byte(vectors[0].plaintext)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.Encrypt(p); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecrypt(b *testing.B) {
	for _, bc := range []struct {
		name string
		keys []string
	}{
		{"one_key", []string{imgproxyKey}},
		{"third_of_three_keys", []string{otherKey, key128, imgproxyKey}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			c := mustNew(b, bc.keys...)
			tok := vectors[0].encoded
			b.ReportAllocs()
			for b.Loop() {
				if _, err := c.Decrypt(tok); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkDecryptInvalid(b *testing.B) {
	c := mustNew(b, otherKey, key128)
	tok := vectors[0].encoded
	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.Decrypt(tok); err == nil {
			b.Fatal("want error")
		}
	}
}
