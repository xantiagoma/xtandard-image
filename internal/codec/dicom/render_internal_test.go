package dicom

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// TestRenderMatchesDecode renders the stored values of the RLE phantom
// through Header.Render with the JPEG 2000 file's header (the same phantom,
// window and rescale), as the caller does after a codec decoded the
// codestream, and expects Decode's output.
func TestRenderMatchesDecode(t *testing.T) {
	t.Parallel()
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", "dicom", name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	rle := read("ct-rle.dcm")
	want, err := Decode(rle, Options{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := parse(rle)
	if err != nil {
		t.Fatal(err)
	}
	frag, err := p.firstFrame()
	if err != nil {
		t.Fatal(err)
	}
	frame, err := decodeRLE(&p.h, frag)
	if err != nil {
		t.Fatal(err)
	}
	s := p.h.nativeSampler(frame, binary.BigEndian)
	samples := Samples{Width: s.w, Height: s.h, Bands: 1, Data: make([]int32, s.w*s.h)}
	for i := range samples.Data {
		samples.Data[i] = s.at(i, 0)
	}
	h, err := ParseHeader(read("ct-j2k.dcm"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.Render(samples)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Data, want.Data) {
		t.Error("Render of the decoded frame differs from Decode")
	}
}
