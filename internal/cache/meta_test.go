package cache

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestFrameRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 26, 12, 0, 0, 123, time.UTC)
	body := []byte("image bytes")
	b := NewBuffer(body, nil)
	defer b.Release()
	in := &Entry{Body: b, Status: http.StatusOK, Header: http.Header{"Content-Type": {"image/jpeg"}}}
	m := mustMeta(t, "res/v1/k", in, now, time.Hour)
	frame, err := AppendFrame([]byte("junk-before"), &m)
	if err != nil {
		t.Fatal(err)
	}
	frame = append(frame[len("junk-before"):], body...)

	got, off, err := ParseFrame(frame)
	if err != nil {
		t.Fatalf("ParseFrame: %v", err)
	}
	if diff := cmp.Diff(m, got); diff != "" {
		t.Errorf("meta (-want +got):\n%s", diff)
	}
	if err := got.Check(frame[off:]); err != nil {
		t.Errorf("Check: %v", err)
	}
	e := got.Entry(nil)
	if !e.StoredAt.Equal(now) || !e.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Errorf("times %s / %s", e.StoredAt, e.ExpiresAt)
	}
	if got.Expired(now.Add(time.Hour-1)) || !got.Expired(now.Add(time.Hour)) {
		t.Error("expiry boundary wrong")
	}
	never := mustMeta(t, "k", in, now, 0)
	if never.ExpiresAt != 0 || never.Expired(now.Add(1e6*time.Hour)) || !never.Entry(nil).ExpiresAt.IsZero() {
		t.Error("ttl 0 must never expire")
	}
}

func TestFrameCorrupt(t *testing.T) {
	t.Parallel()
	b := NewBuffer([]byte("abc"), nil)
	defer b.Release()
	m := mustMeta(t, "res/k", &Entry{Body: b, Status: 200}, time.Unix(0, 0), 0)
	good, err := AppendFrame(nil, &m)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"empty":           nil,
		"short":           []byte("XIC"),
		"bad magic":       append([]byte("XIC2"), good[4:]...),
		"length too long": append([]byte("XIC1\x00\x01\x00\x00"), good[8:]...),
		"truncated json":  good[:len(good)-2],
		"not json":        []byte("XIC1\x00\x00\x00\x03abc"),
		"no key":          []byte("XIC1\x00\x00\x00\x02{}"),
	}
	for name, frame := range tests {
		if _, _, err := ParseFrame(frame); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: %v, want ErrCorrupt", name, err)
		}
	}
	if err := m.Check([]byte("abd")); !errors.Is(err, ErrCorrupt) {
		t.Errorf("flipped byte: %v", err)
	}
	if err := m.Check([]byte("ab")); !errors.Is(err, ErrCorrupt) {
		t.Errorf("short body: %v", err)
	}
}

func FuzzParseFrame(f *testing.F) {
	b := NewBuffer([]byte("seed"), nil)
	m := mustMeta(f, "res/v1/seed", &Entry{Body: b, Status: 200, Header: http.Header{"Etag": {`"x"`}}}, time.Unix(1, 0), time.Minute)
	b.Release()
	seed, err := AppendFrame(nil, &m)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(append(seed, "seed"...))
	f.Add([]byte("XIC1\xff\xff\xff\xff"))
	f.Fuzz(func(t *testing.T, data []byte) {
		m, off, err := ParseFrame(data)
		if err != nil {
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("error without ErrCorrupt: %v", err)
			}
			return
		}
		if off < FramePrefixLen || off > len(data) {
			t.Fatalf("offset %d out of range for %d bytes", off, len(data))
		}
		_ = m.Check(data[off:])
	})
}

func mustMeta(tb testing.TB, full string, e *Entry, now time.Time, ttl time.Duration) Meta {
	tb.Helper()
	m, err := NewMeta(full, e, now, ttl)
	if err != nil {
		tb.Fatal(err)
	}
	return m
}
