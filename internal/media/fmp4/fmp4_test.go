package fmp4

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// mk builds a box of typ around the concatenated payloads.
func mk(typ string, payload ...[]byte) []byte {
	body := bytes.Join(payload, nil)
	b := binary.BigEndian.AppendUint32(nil, uint32(8+len(body))) //nolint:gosec // G115: test boxes are small
	return append(append(b, typ...), body...)
}

func u32(v ...uint32) []byte {
	var b []byte
	for _, x := range v {
		b = binary.BigEndian.AppendUint32(b, x)
	}
	return b
}

func u64(v uint64) []byte { return binary.BigEndian.AppendUint64(nil, v) }

// sampleEntry is a sample entry of typ with fixed fields of size n and
// one child.
func sampleEntry(typ string, n int, child []byte) []byte {
	return mk(typ, make([]byte, n), child)
}

// file builds an init segment with entry and the given fragments
// (decode time, sample durations).
func file(entry []byte, timescale uint32, frags ...[]uint32) []byte {
	stsd := mk("stsd", u32(0, 1), entry)
	mdhd := mk("mdhd", u32(0, 0, 0, timescale, 0, 0))
	moov := mk("moov",
		mk("mvhd", make([]byte, 100)),
		mk("trak", mk("tkhd", make([]byte, 84)), mk("mdia", mdhd, mk("hdlr", make([]byte, 25)),
			mk("minf", mk("stbl", stsd, mk("stts", u32(0, 0)))))),
		mk("mvex", mk("trex", u32(0, 1, 1, 512, 0, 0))))
	out := append(mk("ftyp", []byte("iso5"), u32(512), []byte("iso6mp41")), moov...)
	var t uint64
	for i, durs := range frags {
		// trun: data offset and sample durations (flags 0x101) for even
		// fragments, the trex default (flags 0x001) for odd ones.
		var trun []byte
		if i%2 == 0 {
			trun = mk("trun", u32(0x000101, uint32(len(durs)), 0), u32(durs...)) //nolint:gosec // G115: a few samples
		} else {
			trun = mk("trun", u32(0x000001, uint32(len(durs)), 0)) //nolint:gosec // G115: a few samples
		}
		traf := mk("traf", mk("tfhd", u32(0x020000, 1)), mk("tfdt", u32(1<<24), u64(t)), trun)
		out = append(out, mk("moof", mk("mfhd", u32(0, uint32(i+1))), traf)...)
		out = append(out, mk("mdat", []byte("data"))...)
		for _, d := range durs {
			if i%2 == 0 {
				t += uint64(d)
			} else {
				t += 512
			}
		}
	}
	return out
}

func TestParse(t *testing.T) {
	t.Parallel()
	avcC := mk("avcC", []byte{1, 0x64, 0x00, 0x1f, 0xff})
	b := file(sampleEntry("avc1", visualEntry, avcC), 90000, []uint32{3600, 3600, 3600}, []uint32{0, 0}, []uint32{1800})
	f, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if f.Timescale != 90000 || len(f.Fragments) != 3 {
		t.Fatalf("timescale %d, %d fragments", f.Timescale, len(f.Fragments))
	}
	want := []Fragment{{DecodeTime: 0, Duration: 10800, Samples: 3}, {DecodeTime: 10800, Duration: 1024, Samples: 2}, {DecodeTime: 11824, Duration: 1800, Samples: 1}}
	end := len(f.Init)
	for i, fr := range f.Fragments {
		if fr.DecodeTime != want[i].DecodeTime || fr.Duration != want[i].Duration || fr.Samples != want[i].Samples {
			t.Errorf("fragment %d = %+v, want %+v", i, fr, want[i])
		}
		if !bytes.HasPrefix(fr.Data[4:], []byte("moof")) || !bytes.Equal(fr.Data, b[end:end+len(fr.Data)]) {
			t.Errorf("fragment %d bytes are not the file's moof+mdat", i)
		}
		end += len(fr.Data)
	}
	if end != len(b) || !bytes.HasPrefix(f.Init[4:], []byte("ftyp")) {
		t.Fatalf("init + fragments cover %d of %d bytes", end, len(b))
	}
	if c, err := CodecString(f.Init); err != nil || c != "avc1.64001f" {
		t.Fatalf("codec %q %v", c, err)
	}
}

func TestCodecString(t *testing.T) {
	t.Parallel()
	esds := mk("esds", u32(0),
		[]byte{0x03, 0x19, 0x00, 0x01, 0x00, // ES_Descriptor, ES_ID 1, no flags
			0x04, 0x11, 0x40, 0x15, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // DecoderConfig: AAC (0x40)
			0x05, 0x02, 0x11, 0x90, // AudioSpecificConfig: AAC-LC (2), 48 kHz, stereo
			0x06, 0x01, 0x02})
	tests := map[string][]byte{
		"avc1.640028":   sampleEntry("avc1", visualEntry, mk("avcC", []byte{1, 0x64, 0x00, 0x28})),
		"av01.0.08M.08": sampleEntry("av01", visualEntry, mk("av1C", []byte{0x81, 0x08, 0x0c, 0x00})),
		"av01.0.05H.10": sampleEntry("av01", visualEntry, mk("av1C", []byte{0x81, 0x05, 0xcc, 0x00})),
		"vp09.00.31.08": sampleEntry("vp09", visualEntry, mk("vpcC", u32(1<<24), []byte{0, 31, 0x82, 2, 2, 2, 0, 0})),
		"mp4a.40.2":     sampleEntry("mp4a", audioEntry, esds),
	}
	for want, entry := range tests {
		got, err := CodecString(file(entry, 48000))
		if err != nil || got != want {
			t.Errorf("CodecString = %q, %v; want %q", got, err, want)
		}
	}
	for name, entry := range map[string][]byte{
		"unknown entry": sampleEntry("hvc1", visualEntry, mk("hvcC", []byte{1})),
		"empty av1C":    sampleEntry("av01", visualEntry, mk("av1C")),
		"no avcC":       sampleEntry("avc1", visualEntry, mk("colr", []byte{1})),
		"short esds":    sampleEntry("mp4a", audioEntry, mk("esds", []byte{0})),
	} {
		if got, err := CodecString(file(entry, 48000)); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: %q %v", name, got, err)
		}
	}
}

func TestParseMalformed(t *testing.T) {
	t.Parallel()
	good := file(sampleEntry("avc1", visualEntry, mk("avcC", []byte{1, 0x64, 0, 0x1f})), 90000, []uint32{1, 2})
	cases := map[string][]byte{
		"empty":           nil,
		"truncated":       good[:len(good)-3],
		"no moov":         mk("moof", mk("traf")),
		"moof before":     append(mk("moof", mk("traf")), good...),
		"moof no mdat":    append(append([]byte{}, good[:len(good)-12]...), mk("free")...),
		"huge box":        append(u32(0xffffff00), []byte("ftyp")...),
		"large box short": append(u32(1), []byte("ftypxx")...),
		"zero timescale":  file(sampleEntry("avc1", visualEntry, nil), 0),
		"huge trun": append(good[:len(good)-len(mk("mdat", []byte("data")))-
			len(mk("moof", mk("mfhd", u32(0, 1)), mk("traf", mk("tfhd", u32(0x020000, 1)), mk("tfdt", u32(1<<24), u64(0)),
				mk("trun", u32(0x000101, 2, 0), u32(1, 2)))))],
			append(mk("moof", mk("mfhd", u32(0, 1)), mk("traf", mk("tfhd", u32(0x020000, 1)), mk("tfdt", u32(1<<24), u64(0)),
				mk("trun", u32(0x000101, 1<<30, 0), u32(1, 2)))), mk("mdat")...)...),
	}
	for name, b := range cases {
		if _, err := Parse(b); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add(file(sampleEntry("avc1", visualEntry, mk("avcC", []byte{1, 0x64, 0, 0x1f})), 90000, []uint32{1, 2}, []uint32{3}))
	f.Add(file(sampleEntry("mp4a", audioEntry, mk("esds", u32(0))), 48000, []uint32{1024}))
	f.Fuzz(func(t *testing.T, b []byte) {
		fl, err := Parse(b)
		if err != nil {
			return
		}
		n := len(fl.Init)
		for _, fr := range fl.Fragments {
			n += len(fr.Data)
		}
		if n > len(b) {
			t.Fatalf("parts of %d bytes from %d", n, len(b))
		}
		_, _ = CodecString(fl.Init)
	})
}
