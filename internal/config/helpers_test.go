package config

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"io/fs"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// testSSHSeed derives the fixed Ed25519 test key (generated at test time,
// so no private key is committed).
const testSSHSeed = "xtandard-image config test key.."

// sshString is an SSH wire-format string (RFC 4251 §5).
func sshString(b []byte) []byte {
	return append(binary.BigEndian.AppendUint32(nil, uint32(len(b))), b...) //nolint:gosec // G115: test strings are a few bytes
}

// testSSHPublicWire is the test key's public half in SSH wire format.
func testSSHPublicWire() []byte {
	pub, _ := ed25519.NewKeyFromSeed([]byte(testSSHSeed)).Public().(ed25519.PublicKey)
	return append(sshString([]byte("ssh-ed25519")), sshString(pub)...)
}

// testSSHPrivateKey is the test key as PEM: PKCS #8 (deterministic, so
// loaded configs compare equal), or with encrypted an "openssh-key-v1"
// key whose header names aes256-ctr and bcrypt (the encrypted section is
// filler: Validate reads the header only).
func testSSHPrivateKey(encrypted bool) string {
	if !encrypted {
		der, err := x509.MarshalPKCS8PrivateKey(ed25519.NewKeyFromSeed([]byte(testSSHSeed)))
		if err != nil {
			return ""
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	}
	b := []byte("openssh-key-v1\x00")
	b = append(b, sshString([]byte("aes256-ctr"))...)
	b = append(b, sshString([]byte("bcrypt"))...)
	b = append(b, sshString(append(sshString(make([]byte, 16)), 0, 0, 0, 16))...)
	b = binary.BigEndian.AppendUint32(b, 1)
	b = append(b, sshString(testSSHPublicWire())...)
	b = append(b, sshString(make([]byte, 160))...)
	return string(pem.EncodeToMemory(&pem.Block{Type: "OPENSSH PRIVATE KEY", Bytes: b}))
}

// testSSHHostKey is the test key's public half in authorized_keys form
// ("ssh-ed25519 AAAA…"), as a pinned host key.
func testSSHHostKey() string {
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(testSSHPublicWire())
}

// testAzureConnectionString is an Azurite-style connection string the Azure
// SDK parses.
func testAzureConnectionString() string {
	return "DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;AccountKey=" +
		base64.StdEncoding.EncodeToString([]byte(testHexKey)) + ";BlobEndpoint=http://azurite:10000/devstoreaccount1;"
}

// files is an in-memory ReadFile.
type files map[string]string

func (f files) read(name string) ([]byte, error) {
	if v, ok := f[name]; ok {
		return []byte(v), nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// opts builds LoadOptions reading from f with a fixed GOMAXPROCS of 4.
func opts(f files, environ ...string) LoadOptions {
	return LoadOptions{Environ: environ, ReadFile: f.read, GOMAXPROCS: 4}
}

// mustParse runs load (no Validate) and fails the test on error.
func mustParse(t *testing.T, f files, environ ...string) Config {
	t.Helper()
	cfg, _, err := load(opts(f, environ...))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return cfg
}

// with returns the smallest valid environment plus extra.
func with(extra ...string) []string {
	return append([]string{"XI_SOURCES=origin", "XI_SOURCES_ORIGIN_TYPE=http", "XI_SOURCES_ORIGIN_ALLOW=cdn.example.com"}, extra...)
}

// diff is cmp.Diff with the options Config needs.
func diff(want, got any) string {
	return cmp.Diff(want, got, cmp.Comparer(func(a, b CIDR) bool { return a == b }))
}
