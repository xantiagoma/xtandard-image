package xtandard

import (
	"crypto/hmac"
	"crypto/sha256"
	"regexp"
	"strings"
)

// SigningKey is a signing key: one of XI_SIGNING_KEYS with its salt from
// XI_SIGNING_SALTS (imgproxy's IMGPROXY_KEY and IMGPROXY_SALT), and the
// signature size (XI_SIGNING_SIZE). Make one with [ParseSigningKey] or
// [NewSigningKey]; it is immutable and safe for concurrent use. Its String
// method never prints the key material.
type SigningKey struct {
	key, salt []byte
	size      int
}

// ParseSigningKey returns a signing key from hex key and salt (as in the
// server's config). size is the signature size in bytes before encoding, 1 to
// 32; [DefaultSignatureSize] is the full HMAC.
func ParseSigningKey(key, salt string, size int) (SigningKey, error) {
	k, err := hexKey(key, "signing key")
	if err != nil {
		return SigningKey{}, err
	}
	s, err := hexKey(salt, "signing salt")
	if err != nil {
		return SigningKey{}, err
	}
	return newSigningKey(k, s, size)
}

// NewSigningKey returns a signing key from raw key and salt bytes (copied).
// size is as for [ParseSigningKey].
func NewSigningKey(key, salt []byte, size int) (SigningKey, error) {
	return newSigningKey(append([]byte(nil), key...), append([]byte(nil), salt...), size)
}

func newSigningKey(key, salt []byte, size int) (SigningKey, error) {
	if size < MinSignatureSize || size > MaxSignatureSize {
		return SigningKey{}, Errorf(ErrInvalidKey, "signature size %d out of range [%d, %d]", size, MinSignatureSize, MaxSignatureSize)
	}
	return SigningKey{key: key, salt: salt, size: size}, nil
}

// Size returns the signature size in bytes before encoding.
func (k SigningKey) Size() int { return k.size }

// String implements fmt.Stringer without the key material.
func (k SigningKey) String() string { return "xtandard.SigningKey{redacted}" }

// GoString implements fmt.GoStringer without the key material.
func (k SigningKey) GoString() string { return k.String() }

// Sign signs a message with the salted HMAC-SHA256 construction of the
// imgproxy and xi_sig schemes: base64url(HMAC-SHA256(key, salt ‖ message)[:size]).
// Like the server, a message without a leading "/" is signed as if it had one.
// The zero SigningKey signs with an empty key and salt and the full size.
func Sign(message string, key SigningKey) string {
	if !strings.HasPrefix(message, "/") {
		message = "/" + message
	}
	mac := hmac.New(sha256.New, key.key)
	mac.Write(key.salt)
	mac.Write([]byte(message))
	sum := mac.Sum(nil)
	if key.size > 0 {
		sum = sum[:key.size]
	}
	return b64url(sum)
}

// SignImgproxyPath returns "/" + signature + path for an imgproxy path
// (everything after the signature segment: options and source).
func SignImgproxyPath(path string, key SigningKey) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return "/" + Sign(path, key) + path
}

// XiMessage splits a raw query into the message xi_sig signs and the xi_sig
// value it carries ("" when it has none). The message is path, then "?" and
// the remaining non-empty parameters in their original order and encoding
// (no "?" when none remain). Two xi_sig parameters are an [ErrInvalidRequest]
// error, as the server answers them with 403.
func XiMessage(path, rawQuery string) (message, signature string, err error) {
	var kept []string
	seen := false
	for part := range strings.SplitSeq(rawQuery, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		if k == XiSigParam {
			if seen {
				return "", "", Errorf(ErrInvalidRequest, "query has more than one %s parameter", XiSigParam)
			}
			seen, signature = true, v
			continue
		}
		kept = append(kept, part)
	}
	if len(kept) == 0 {
		return path, signature, nil
	}
	return path + "?" + strings.Join(kept, "&"), signature, nil
}

// SignXiQuery returns rawQuery (a leading "?" is ignored) without any xi_sig
// and with a fresh xi_sig for path appended last. path is the escaped path the
// dialect sees, after its mount prefix ("/", "/video", "/resize").
func SignXiQuery(path, rawQuery string, key SigningKey) (string, error) {
	msg, _, err := XiMessage(path, strings.TrimPrefix(rawQuery, "?"))
	if err != nil {
		return "", err
	}
	sig := XiSigParam + "=" + Sign(msg, key)
	if q := msg[len(path):]; q != "" {
		return q[1:] + "&" + sig, nil
	}
	return sig, nil
}

var absoluteURL = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)

// SignXiURL signs a URL (absolute, or a path with an optional query) with
// xi_sig. mount is the dialect's mount prefix, which the signed path excludes
// ([QueryMount] for the query dialect, "" for none). A fragment is dropped.
// A path outside the mount and two xi_sig parameters are [ErrInvalidRequest]
// errors.
func SignXiURL(url string, key SigningKey, mount string) (string, error) {
	noHash, _, _ := strings.Cut(url, "#")
	head, query, _ := strings.Cut(noHash, "?")
	origin, path := "", head
	if absoluteURL.MatchString(head) {
		hostStart := strings.Index(head, "//") + 2
		if i := strings.IndexByte(head[hostStart:], '/'); i >= 0 {
			origin, path = head[:hostStart+i], head[hostStart+i:]
		} else {
			origin, path = head, "/"
		}
	}
	m := strings.TrimRight(mount, "/")
	if m != "" {
		if path != m && !strings.HasPrefix(path, m+"/") {
			return "", Errorf(ErrInvalidRequest, "path %s is not under the mount %s", path, m)
		}
		path = path[len(m):]
		if path == "" {
			path = "/"
		}
	}
	q, err := SignXiQuery(path, query, key)
	if err != nil {
		return "", err
	}
	return origin + m + path + "?" + q, nil
}
