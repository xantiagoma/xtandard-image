package cache

import (
	"bytes"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"testing"
	"time"
)

// fileBuffer writes data after a prefix of off bytes into a temporary file
// and returns a FileBuffer over data, and a counter of its frees.
func fileBuffer(t *testing.T, data []byte, off int64) (*FileBuffer, *int) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "body-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(bytes.Repeat([]byte{'x'}, int(off)), data...)); err != nil {
		t.Fatal(err)
	}
	freed := new(int)
	return NewFileBuffer(f, off, int64(len(data)), func() { *freed++ }), freed
}

func TestFileBuffer(t *testing.T) {
	t.Parallel()
	data := bytes.Repeat([]byte("0123456789abcdef"), 1000)
	for _, off := range []int64{0, 5, 4097} { // page-aligned and not
		b, freed := fileBuffer(t, data, off)
		if b.Len() != int64(len(data)) || BodyLen(b) != int64(len(data)) || !IsFileBody(b) {
			t.Fatalf("off %d: Len %d", off, b.Len())
		}
		got, err := io.ReadAll(BodyReader(b))
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("off %d: reader: %v, %d bytes", off, err, len(got))
		}
		p := make([]byte, 10)
		if n, err := b.ReadAt(p, int64(len(data))-4); n != 4 || !errors.Is(err, io.EOF) || string(p[:4]) != "cdef" {
			t.Fatalf("off %d: ReadAt at the end = %d, %v", off, n, err)
		}
		if _, err := b.ReadAt(p, int64(len(data))); !errors.Is(err, io.EOF) {
			t.Fatalf("off %d: ReadAt past the end: %v", off, err)
		}
		if b.Mapped() {
			t.Fatal("reading mapped the file")
		}
		var w bytes.Buffer
		if err := WriteBody(&w, b); err != nil || !bytes.Equal(w.Bytes(), data) {
			t.Fatalf("off %d: WriteBody: %v", off, err)
		}
		if !bytes.Equal(b.Bytes(), data) || !b.Mapped() {
			t.Fatalf("off %d: Bytes differ", off)
		}
		b.Retain()
		b.Release()
		if *freed != 0 {
			t.Fatal("freed with a reference left")
		}
		b.Release()
		if *freed != 1 {
			t.Fatalf("off %d: freed %d times", off, *freed)
		}
		if f, _ := b.File(); f.Close() == nil {
			t.Fatal("the last Release left the file open")
		}
	}
}

func TestFileBufferReleasePanics(t *testing.T) {
	t.Parallel()
	b, _ := fileBuffer(t, []byte("abc"), 0)
	b.Release()
	defer func() {
		if recover() == nil {
			t.Fatal("Retain on a released buffer did not panic")
		}
	}()
	b.Retain()
}

func TestNewMetaFileBody(t *testing.T) {
	t.Parallel()
	data := bytes.Repeat([]byte("spooled video "), 50_000)
	b, _ := fileBuffer(t, data, 0)
	defer b.Release()
	m, err := NewMeta("res/k", &Entry{Body: b, Status: 200}, time.Unix(0, 0), 0)
	if err != nil {
		t.Fatal(err)
	}
	if m.Size != int64(len(data)) || m.CRC != crc32.Checksum(data, castagnoli) {
		t.Errorf("meta of a file body: size %d crc %x", m.Size, m.CRC)
	}
	if b.Mapped() {
		t.Error("NewMeta mapped the file")
	}
	e := Entry{Body: b}
	if e.Size() < int64(len(data)) {
		t.Errorf("entry size %d", e.Size())
	}
}

func TestReadFrame(t *testing.T) {
	t.Parallel()
	body := NewBuffer([]byte("body"), nil)
	defer body.Release()
	m := mustMeta(t, "res/k", &Entry{Body: body, Status: 200}, time.Unix(1, 0), time.Minute)
	frame, err := AppendFrame(nil, &m)
	if err != nil {
		t.Fatal(err)
	}
	got, off, err := ReadFrame(bytes.NewReader(append(frame, "body"...)))
	if err != nil || off != int64(len(frame)) || got.Key != "res/k" || got.Size != 4 {
		t.Fatalf("ReadFrame = %+v, %d, %v", got, off, err)
	}
	for _, bad := range [][]byte{nil, []byte("XIC1"), frame[:len(frame)-1], append([]byte("XXXX"), frame[4:]...)} {
		if _, _, err := ReadFrame(bytes.NewReader(bad)); !errors.Is(err, ErrCorrupt) {
			t.Errorf("ReadFrame(%q) = %v, want ErrCorrupt", bad, err)
		}
	}
}
