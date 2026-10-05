// Package ticket seals the stream tickets of HLS and DASH child URLs
// (WP-126, ADR 0012 §6 "Child URLs").
//
// A stream's manifest is reached by a signed URL of a dialect; the
// playlists and segments it lists must be fetchable by a player that has
// no signing key. Each child URL therefore carries a ticket: the stream's
// request (its source and video options, opaque bytes to this package)
// and an expiry, encrypted and authenticated with a server key. A ticket
// cannot be forged or altered without the key (AES-256-GCM's tag), does
// not reveal the source (an encrypted imgproxy source stays secret), and
// stops working at its expiry. What a child URL adds after the ticket (a
// rendition, a segment number) only selects a part of the stream the
// ticket already authorises, and the server checks it against the
// stream's layout.
//
// Format: base64url_nopad( 0x01 ‖ nonce[12] ‖ AES-256-GCM(k_enc, nonce,
// expiry[8, Unix seconds, big endian] ‖ payload, aad) ), where k_enc and
// k_mac are HKDF-SHA256 expansions of the configured secret and the nonce
// is HMAC-SHA256(k_mac, expiry ‖ payload)[:12]: a synthetic IV, so the
// same stream and expiry always give the same ticket (cacheable manifests)
// and two different plaintexts never share a nonce. Several secrets may be
// configured for rotation: the first seals, every one opens.
package ticket

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

const (
	version   = 1
	nonceSize = 12
	// MinSecret is the shortest secret accepted, in bytes.
	MinSecret = 16
	// MaxToken bounds a ticket's length, so a hostile URL costs nothing
	// to refuse.
	MaxToken = 8 << 10
	aad      = "xtandard-image stream ticket v1"
)

// key is one secret's derived keys.
type key struct {
	aead cipher.AEAD
	mac  []byte
}

// Sealer seals and opens tickets. It is safe for concurrent use.
type Sealer struct {
	keys []key
}

// New derives the keys of secrets (each at least MinSecret bytes); the
// first seals.
func New(secrets ...[]byte) (*Sealer, error) {
	if len(secrets) == 0 {
		return nil, errors.New("ticket: no secret")
	}
	s := &Sealer{}
	for i, sec := range secrets {
		if len(sec) < MinSecret {
			return nil, fmt.Errorf("ticket: secret %d is %d bytes, want at least %d", i, len(sec), MinSecret)
		}
		enc, err := hkdf.Key(sha256.New, sec, nil, "xtandard-image stream ticket encryption", 32)
		if err != nil {
			return nil, err
		}
		mac, err := hkdf.Key(sha256.New, sec, nil, "xtandard-image stream ticket nonce", 32)
		if err != nil {
			return nil, err
		}
		block, err := aes.NewCipher(enc)
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		s.keys = append(s.keys, key{aead: aead, mac: mac})
	}
	return s, nil
}

// Seal returns the ticket of payload, valid until exp (whole seconds).
func (s *Sealer) Seal(payload []byte, exp time.Time) string {
	k := s.keys[0]
	plain := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint64(plain, uint64(max(exp.Unix(), 0)))
	plain = append(plain, payload...)
	m := hmac.New(sha256.New, k.mac)
	m.Write(plain)
	nonce := m.Sum(nil)[:nonceSize]
	out := make([]byte, 0, 1+nonceSize+len(plain)+k.aead.Overhead())
	out = append(out, version)
	out = append(out, nonce...)
	out = k.aead.Seal(out, nonce, plain, []byte(aad))
	return base64.RawURLEncoding.EncodeToString(out)
}

// Open checks tok and returns its payload and expiry. A ticket that is
// malformed, altered or sealed with an unknown key is errs.KindForbidden
// (403); one past its expiry at now is errs.KindExpired.
func (s *Sealer) Open(tok string, now time.Time) ([]byte, time.Time, error) {
	if len(tok) > MaxToken {
		return nil, time.Time{}, invalid("ticket too long")
	}
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil || len(raw) < 1+nonceSize || raw[0] != version {
		return nil, time.Time{}, invalid("malformed ticket")
	}
	nonce, sealed := raw[1:1+nonceSize], raw[1+nonceSize:]
	for _, k := range s.keys {
		plain, err := k.aead.Open(nil, nonce, sealed, []byte(aad))
		if err != nil {
			continue
		}
		if len(plain) < 8 {
			return nil, time.Time{}, invalid("short ticket")
		}
		exp := time.Unix(int64(min(binary.BigEndian.Uint64(plain), 1<<62)), 0)
		if !now.Before(exp) {
			return nil, exp, &errs.Error{Kind: errs.KindExpired, Public: "url expired", Msg: "ticket: expired"}
		}
		return plain[8:], exp, nil
	}
	return nil, time.Time{}, invalid("ticket does not authenticate")
}

// invalid is the 403 of a ticket that does not open.
func invalid(msg string) error {
	return &errs.Error{Kind: errs.KindForbidden, Public: "invalid stream url", Msg: "ticket: " + msg}
}
