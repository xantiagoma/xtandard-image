package cache

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"time"
)

// ErrCorrupt reports a persisted entry that cannot be decoded: a truncated
// or foreign file, an unknown frame version, a body whose length or
// checksum does not match its metadata. Backends serve it as a miss, drop
// the entry when they own it and count it (SkipCorrupt).
var ErrCorrupt = errors.New("cache: corrupt entry")

// Framing of a persisted entry (disk file, redis value):
//
//	"XIC1" | uint32 big-endian length of the JSON metadata | JSON Meta | body
//
// The s3 store keeps the JSON in object metadata and the body as the object.
const (
	frameMagic = "XIC1"
	// FramePrefixLen is the fixed part of a frame before the JSON.
	FramePrefixLen = len(frameMagic) + 4
	// maxMetaLen bounds the JSON a reader accepts, so a corrupt length
	// cannot make it allocate.
	maxMetaLen = 64 << 10
)

// castagnoli is CRC-32C, hardware-accelerated on amd64 and arm64.
var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// Meta is the persisted form of an Entry without its body, shared by the
// out-of-process backends (disk, redis, s3cache) so an entry written by
// one replica decodes the same everywhere.
type Meta struct {
	// Key is the full ns+key: backends that hash keys into names detect a
	// collision or a foreign file by comparing it.
	Key       string      `json:"k"`
	Status    int         `json:"s"`
	Header    http.Header `json:"h,omitempty"`
	StoredAt  int64       `json:"t"`           // unix nanoseconds
	ExpiresAt int64       `json:"e,omitempty"` // unix nanoseconds; 0 = never
	Size      int64       `json:"n"`           // body length
	CRC       uint32      `json:"c"`           // CRC-32C of the body
}

// NewMeta describes e stored under fullKey (ns + key) at now for ttl: it
// keeps e.StoredAt (now when zero), derives ExpiresAt with Expiry and
// checksums the body (streaming a file body, which can fail to read).
func NewMeta(fullKey string, e *Entry, now time.Time, ttl time.Duration) (Meta, error) {
	m := Meta{Key: fullKey, Status: e.Status, Header: e.Header}
	stored := e.StoredAt
	if stored.IsZero() {
		stored = now
	}
	m.StoredAt = stored.UnixNano()
	if exp := Expiry(now, ttl); !exp.IsZero() {
		m.ExpiresAt = exp.UnixNano()
	}
	if e.Body == nil {
		return m, nil
	}
	if !IsFileBody(e.Body) {
		b := e.Body.Bytes()
		m.Size = int64(len(b))
		m.CRC = crc32.Checksum(b, castagnoli)
		return m, nil
	}
	h := crc32.New(castagnoli)
	if err := WriteBody(h, e.Body); err != nil {
		return Meta{}, err
	}
	m.Size, m.CRC = BodyLen(e.Body), h.Sum32()
	return m, nil
}

// Expired reports whether the entry is past its ExpiresAt at now.
func (m *Meta) Expired(now time.Time) bool {
	return m.ExpiresAt != 0 && now.UnixNano() >= m.ExpiresAt
}

// Check verifies body against Size and CRC.
func (m *Meta) Check(body []byte) error {
	if int64(len(body)) != m.Size {
		return fmt.Errorf("%w: body has %d bytes, metadata says %d", ErrCorrupt, len(body), m.Size)
	}
	if crc32.Checksum(body, castagnoli) != m.CRC {
		return fmt.Errorf("%w: body checksum mismatch", ErrCorrupt)
	}
	return nil
}

// Entry returns the Entry described by m around body (the Entry takes
// over the caller's reference).
func (m *Meta) Entry(body Buffer) Entry {
	e := Entry{Body: body, Status: m.Status, Header: m.Header, StoredAt: time.Unix(0, m.StoredAt)}
	if m.ExpiresAt != 0 {
		e.ExpiresAt = time.Unix(0, m.ExpiresAt)
	}
	return e
}

// EncodeMeta returns m as JSON (the form s3cache keeps in object
// metadata).
func EncodeMeta(m *Meta) ([]byte, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("cache: encode metadata: %w", err)
	}
	return b, nil
}

// DecodeMeta parses the JSON of EncodeMeta; any failure is ErrCorrupt.
func DecodeMeta(b []byte) (Meta, error) {
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return Meta{}, fmt.Errorf("%w: metadata: %w", ErrCorrupt, err)
	}
	if m.Size < 0 || m.Key == "" {
		return Meta{}, fmt.Errorf("%w: metadata without key or with a negative size", ErrCorrupt)
	}
	return m, nil
}

// AppendFrame appends the frame prefix and m's JSON to dst; the body
// follows.
func AppendFrame(dst []byte, m *Meta) ([]byte, error) {
	j, err := EncodeMeta(m)
	if err != nil {
		return nil, err
	}
	if len(j) > maxMetaLen {
		return nil, fmt.Errorf("cache: metadata of %d bytes exceeds %d", len(j), maxMetaLen)
	}
	dst = append(dst, frameMagic...)
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(j))) //nolint:gosec // G115: len(j) ≤ maxMetaLen, checked above
	return append(dst, j...), nil
}

// ParseEntry decodes a whole frame-plus-body value stored under full
// (ns + key): ok is false when the frame is corrupt, the body fails its
// length or checksum, or the value belongs to another key.
func ParseEntry(b []byte, full string) (m Meta, body []byte, ok bool) {
	m, off, err := ParseFrame(b)
	if err != nil || m.Key != full || m.Check(b[off:]) != nil {
		return Meta{}, nil, false
	}
	return m, b[off:], true
}

// ReadFrame reads the frame at the start of r and returns its metadata
// and the offset of the body, reading nothing of the body (the disk
// store's file bodies).
func ReadFrame(r io.Reader) (Meta, int64, error) {
	var pre [FramePrefixLen]byte
	if _, err := io.ReadFull(r, pre[:]); err != nil || string(pre[:len(frameMagic)]) != frameMagic {
		return Meta{}, 0, fmt.Errorf("%w: bad frame header", ErrCorrupt)
	}
	n := binary.BigEndian.Uint32(pre[len(frameMagic):])
	if n > maxMetaLen {
		return Meta{}, 0, fmt.Errorf("%w: metadata length %d out of range", ErrCorrupt, n)
	}
	j := make([]byte, n)
	if _, err := io.ReadFull(r, j); err != nil {
		return Meta{}, 0, fmt.Errorf("%w: truncated metadata", ErrCorrupt)
	}
	m, err := DecodeMeta(j)
	if err != nil {
		return Meta{}, 0, err
	}
	return m, int64(FramePrefixLen) + int64(n), nil
}

// ParseFrame decodes the frame at the start of b and returns its metadata
// and the offset of the body. It does not check the body (Meta.Check).
func ParseFrame(b []byte) (Meta, int, error) {
	if len(b) < FramePrefixLen || string(b[:len(frameMagic)]) != frameMagic {
		return Meta{}, 0, fmt.Errorf("%w: bad frame header", ErrCorrupt)
	}
	n := binary.BigEndian.Uint32(b[len(frameMagic):FramePrefixLen])
	if n > maxMetaLen || int(n) > len(b)-FramePrefixLen {
		return Meta{}, 0, fmt.Errorf("%w: metadata length %d out of range", ErrCorrupt, n)
	}
	end := FramePrefixLen + int(n)
	m, err := DecodeMeta(b[FramePrefixLen:end])
	if err != nil {
		return Meta{}, 0, err
	}
	return m, end, nil
}
