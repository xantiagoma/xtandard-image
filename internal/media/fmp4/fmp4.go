// Package fmp4 reads the structure of the fragmented MP4 (CMAF) files the
// transcoder writes for HLS and DASH (WP-126, ADR 0012 §6): the init
// segment (ftyp and an empty moov) and the fragments (moof and mdat) that
// follow it, with each fragment's decode time (tfdt) and the duration of
// its samples (trun). The server cuts a transcode's output into the init
// segment and media segments with it, and the tests check the timeline of
// independently encoded segments and the codec strings of the manifests.
//
// It reads; it never writes or rewrites boxes. Only the boxes on the paths
// it needs are parsed, with every size checked against its parent.
package fmp4

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrMalformed is the error of a file this package cannot read.
var ErrMalformed = errors.New("fmp4: malformed file")

// File is a fragmented MP4 with one track.
type File struct {
	// Init is the init segment: every box before the first moof (ftyp,
	// moov). It aliases the parsed bytes.
	Init []byte
	// Timescale is the track's media timescale (mdhd), in ticks per
	// second.
	Timescale uint32
	// Fragments are the moof+mdat pairs in order.
	Fragments []Fragment
}

// Fragment is one moof and the mdat after it.
type Fragment struct {
	// Data is the fragment's bytes (moof and mdat, and any box between
	// them); it aliases the parsed bytes.
	Data []byte
	// DecodeTime is the first sample's decode time (tfdt), in Timescale
	// ticks.
	DecodeTime uint64
	// Duration is the sum of the samples' durations, in Timescale ticks;
	// Samples is their number.
	Duration uint64
	Samples  int
}

// box is one box: its type and where its payload is.
type box struct {
	typ        string
	start, end int // the whole box
	body       int // first payload byte
}

// boxes splits b[off:end] into boxes.
func boxes(b []byte, off, end int) ([]box, error) {
	var out []box
	for off < end {
		if end-off < 8 {
			return nil, fmt.Errorf("%w: truncated box header at %d", ErrMalformed, off)
		}
		size := int64(binary.BigEndian.Uint32(b[off:]))
		hdr := 8
		switch size {
		case 1:
			if end-off < 16 {
				return nil, fmt.Errorf("%w: truncated large box at %d", ErrMalformed, off)
			}
			size = int64(binary.BigEndian.Uint64(b[off+8:]))
			hdr = 16
		case 0:
			size = int64(end - off)
		}
		if size < int64(hdr) || size > int64(end-off) {
			return nil, fmt.Errorf("%w: box size %d at %d", ErrMalformed, size, off)
		}
		out = append(out, box{typ: string(b[off+4 : off+8]), start: off, end: off + int(size), body: off + hdr})
		off += int(size)
	}
	return out, nil
}

// child returns the first box of type typ inside parent, whose payload
// starts skip bytes into its body (sample entries have fields before
// their children).
func child(b []byte, parent box, skip int, typ string) (box, bool, error) {
	if parent.body+skip > parent.end {
		return box{}, false, fmt.Errorf("%w: short %s", ErrMalformed, parent.typ)
	}
	list, err := boxes(b, parent.body+skip, parent.end)
	if err != nil {
		return box{}, false, err
	}
	for _, c := range list {
		if c.typ == typ {
			return c, true, nil
		}
	}
	return box{}, false, nil
}

// path walks from parent through the box types of path.
func path(b []byte, parent box, types ...string) (box, error) {
	cur := parent
	for _, t := range types {
		c, ok, err := child(b, cur, 0, t)
		if err != nil {
			return box{}, err
		}
		if !ok {
			return box{}, fmt.Errorf("%w: no %s in %s", ErrMalformed, t, cur.typ)
		}
		cur = c
	}
	return cur, nil
}

// Parse reads b, a fragmented MP4 with one track. The File aliases b.
func Parse(b []byte) (*File, error) {
	top, err := boxes(b, 0, len(b))
	if err != nil {
		return nil, err
	}
	f := &File{}
	var moov *box
	var trexDuration uint32
	initEnd := -1
	for i := 0; i < len(top); i++ {
		bx := top[i]
		switch bx.typ {
		case "moov":
			moov = &top[i]
		case "moof":
			if initEnd < 0 {
				initEnd = bx.start
			}
			if moov == nil {
				return nil, fmt.Errorf("%w: moof before moov", ErrMalformed)
			}
			j := i + 1
			for j < len(top) && top[j].typ != "mdat" && top[j].typ != "moof" {
				j++
			}
			if j == len(top) || top[j].typ != "mdat" {
				return nil, fmt.Errorf("%w: moof without mdat", ErrMalformed)
			}
			fr, err := fragment(b, bx, trexDuration)
			if err != nil {
				return nil, err
			}
			fr.Data = b[bx.start:top[j].end]
			f.Fragments = append(f.Fragments, fr)
			i = j
		}
		if bx.typ == "moov" {
			ts, def, err := track(b, bx)
			if err != nil {
				return nil, err
			}
			f.Timescale, trexDuration = ts, def
		}
	}
	if moov == nil {
		return nil, fmt.Errorf("%w: no moov", ErrMalformed)
	}
	if initEnd < 0 {
		initEnd = moov.end
	}
	f.Init = b[:initEnd]
	return f, nil
}

// track reads the track's timescale (mdhd) and default sample duration
// (trex) from moov.
func track(b []byte, moov box) (timescale, defDuration uint32, err error) {
	mdhd, err := path(b, moov, "trak", "mdia", "mdhd")
	if err != nil {
		return 0, 0, err
	}
	off := mdhd.body + 12 // version/flags, creation and modification times (v0)
	if mdhd.body < mdhd.end && b[mdhd.body] == 1 {
		off = mdhd.body + 20
	}
	if off+4 > mdhd.end {
		return 0, 0, fmt.Errorf("%w: short mdhd", ErrMalformed)
	}
	timescale = binary.BigEndian.Uint32(b[off:])
	if timescale == 0 {
		return 0, 0, fmt.Errorf("%w: timescale 0", ErrMalformed)
	}
	if trex, err := path(b, moov, "mvex", "trex"); err == nil && trex.body+16 <= trex.end {
		defDuration = binary.BigEndian.Uint32(b[trex.body+12:])
	}
	return timescale, defDuration, nil
}

// trun and tfhd flags.
const (
	tfhdBaseDataOffset  = 0x01
	tfhdSampleDescIndex = 0x02
	tfhdDefaultDuration = 0x08
	trunDataOffset      = 0x001
	trunFirstFlags      = 0x004
	trunDuration        = 0x100
	trunSize            = 0x200
	trunFlags           = 0x400
	trunCTS             = 0x800
)

// fragment reads a moof's decode time and sample durations.
func fragment(b []byte, moof box, trexDuration uint32) (Fragment, error) {
	var fr Fragment
	traf, err := path(b, moof, "traf")
	if err != nil {
		return fr, err
	}
	tfdt, err := path(b, traf, "tfdt")
	if err != nil {
		return fr, err
	}
	switch {
	case tfdt.body+12 <= tfdt.end && b[tfdt.body] == 1:
		fr.DecodeTime = binary.BigEndian.Uint64(b[tfdt.body+4:])
	case tfdt.body+8 <= tfdt.end:
		fr.DecodeTime = uint64(binary.BigEndian.Uint32(b[tfdt.body+4:]))
	default:
		return fr, fmt.Errorf("%w: short tfdt", ErrMalformed)
	}
	def := trexDuration
	tfhd, err := path(b, traf, "tfhd")
	if err != nil {
		return fr, err
	}
	if tfhd.body+8 > tfhd.end {
		return fr, fmt.Errorf("%w: short tfhd", ErrMalformed)
	}
	flags := binary.BigEndian.Uint32(b[tfhd.body:]) & 0xffffff
	off := tfhd.body + 8 // flags, track_ID
	if flags&tfhdBaseDataOffset != 0 {
		off += 8
	}
	if flags&tfhdSampleDescIndex != 0 {
		off += 4
	}
	if flags&tfhdDefaultDuration != 0 {
		if off+4 > tfhd.end {
			return fr, fmt.Errorf("%w: short tfhd", ErrMalformed)
		}
		def = binary.BigEndian.Uint32(b[off:])
	}
	list, err := boxes(b, traf.body, traf.end)
	if err != nil {
		return fr, err
	}
	for _, t := range list {
		if t.typ != "trun" {
			continue
		}
		if err := trun(b, t, def, &fr); err != nil {
			return fr, err
		}
	}
	return fr, nil
}

// trun adds a trun's samples and durations to fr.
func trun(b []byte, t box, def uint32, fr *Fragment) error {
	if t.body+8 > t.end {
		return fmt.Errorf("%w: short trun", ErrMalformed)
	}
	flags := binary.BigEndian.Uint32(b[t.body:]) & 0xffffff
	n := int(binary.BigEndian.Uint32(b[t.body+4:]))
	off := t.body + 8
	if flags&trunDataOffset != 0 {
		off += 4
	}
	if flags&trunFirstFlags != 0 {
		off += 4
	}
	per := 0
	for _, f := range []uint32{trunDuration, trunSize, trunFlags, trunCTS} {
		if flags&f != 0 {
			per += 4
		}
	}
	if n < 0 || off > t.end || (per > 0 && n > (t.end-off)/per) {
		return fmt.Errorf("%w: trun of %d samples", ErrMalformed, n)
	}
	for i := range n {
		d := def
		if flags&trunDuration != 0 {
			d = binary.BigEndian.Uint32(b[off+i*per:])
		}
		fr.Duration += uint64(d)
	}
	fr.Samples += n
	return nil
}

// CodecString returns the RFC 6381 codec string of the init segment's
// sample entry: "avc1.PPCCLL", "av01.P.LLT.DD", "vp09.PP.LL.DD" or
// "mp4a.40.<object type>", from its configuration record (avcC, av1C,
// vpcC, esds).
func CodecString(init []byte) (string, error) {
	top, err := boxes(init, 0, len(init))
	if err != nil {
		return "", err
	}
	for _, bx := range top {
		if bx.typ != "moov" {
			continue
		}
		stsd, err := path(init, bx, "trak", "mdia", "minf", "stbl", "stsd")
		if err != nil {
			return "", err
		}
		entries, err := boxes(init, min(stsd.body+8, stsd.end), stsd.end) // version/flags, entry count
		if err != nil || len(entries) == 0 {
			return "", fmt.Errorf("%w: empty stsd", ErrMalformed)
		}
		return entryCodec(init, entries[0])
	}
	return "", fmt.Errorf("%w: no moov", ErrMalformed)
}

// Sample entry sizes before their child boxes.
const (
	visualEntry = 78
	audioEntry  = 28
)

func entryCodec(b []byte, e box) (string, error) {
	switch e.typ {
	case "avc1", "avc3":
		c, ok, err := child(b, e, visualEntry, "avcC")
		if err != nil || !ok || c.body+4 > c.end {
			return "", fmt.Errorf("%w: no avcC", ErrMalformed)
		}
		return fmt.Sprintf("%s.%02x%02x%02x", e.typ, b[c.body+1], b[c.body+2], b[c.body+3]), nil
	case "av01":
		c, ok, err := child(b, e, visualEntry, "av1C")
		if err != nil || !ok || c.body+3 > c.end {
			return "", fmt.Errorf("%w: no av1C", ErrMalformed)
		}
		profile, level := b[c.body+1]>>5, b[c.body+1]&0x1f
		tier := "M"
		if b[c.body+2]&0x80 != 0 {
			tier = "H"
		}
		depth := 8
		switch {
		case b[c.body+2]&0x40 != 0 && b[c.body+2]&0x20 != 0:
			depth = 12
		case b[c.body+2]&0x40 != 0:
			depth = 10
		}
		return fmt.Sprintf("av01.%d.%02d%s.%02d", profile, level, tier, depth), nil
	case "vp09":
		c, ok, err := child(b, e, visualEntry, "vpcC")
		if err != nil || !ok || c.body+7 > c.end {
			return "", fmt.Errorf("%w: no vpcC", ErrMalformed)
		}
		return fmt.Sprintf("vp09.%02d.%02d.%02d", b[c.body+4], b[c.body+5], b[c.body+6]>>4), nil
	case "mp4a":
		c, ok, err := child(b, e, audioEntry, "esds")
		if err != nil || !ok {
			return "", fmt.Errorf("%w: no esds", ErrMalformed)
		}
		return esdsCodec(b[c.body:c.end])
	default:
		return "", fmt.Errorf("%w: sample entry %q", ErrMalformed, e.typ)
	}
}

// esdsCodec reads "mp4a.<object type indication>.<audio object type>"
// from an esds payload (version/flags, then the ES descriptor).
func esdsCodec(p []byte) (string, error) {
	if len(p) < 4 {
		return "", fmt.Errorf("%w: short esds", ErrMalformed)
	}
	p = p[4:]
	var oti, aot byte
	for len(p) > 0 {
		tag, body, rest, ok := descriptor(p)
		if !ok {
			break
		}
		switch tag {
		case 0x03: // ES_Descriptor: ES_ID, flags, then sub-descriptors
			if len(body) < 3 {
				return "", fmt.Errorf("%w: short ES descriptor", ErrMalformed)
			}
			flags := body[2]
			skip := 3
			if flags&0x80 != 0 {
				skip += 2
			}
			if flags&0x40 != 0 && len(body) > skip {
				skip += 1 + int(body[skip])
			}
			if flags&0x20 != 0 {
				skip += 2
			}
			if skip > len(body) {
				return "", fmt.Errorf("%w: short ES descriptor", ErrMalformed)
			}
			p = body[skip:]
			continue
		case 0x04: // DecoderConfigDescriptor
			if len(body) < 13 {
				return "", fmt.Errorf("%w: short decoder config", ErrMalformed)
			}
			oti = body[0]
			p = body[13:]
			continue
		case 0x05: // DecoderSpecificInfo: AudioSpecificConfig
			if len(body) > 0 {
				aot = body[0] >> 3
			}
		}
		p = rest
	}
	if oti == 0 || aot == 0 {
		return "", fmt.Errorf("%w: no audio object type", ErrMalformed)
	}
	return fmt.Sprintf("mp4a.%x.%d", oti, aot), nil
}

// descriptor splits an MPEG-4 descriptor (tag, expandable size).
func descriptor(p []byte) (tag byte, body, rest []byte, ok bool) {
	if len(p) < 2 {
		return 0, nil, nil, false
	}
	tag = p[0]
	size, i := 0, 1
	for ; i < len(p) && i <= 4; i++ {
		size = size<<7 | int(p[i]&0x7f)
		if p[i]&0x80 == 0 {
			i++
			break
		}
	}
	if size > len(p)-i {
		return 0, nil, nil, false
	}
	return tag, p[i : i+size], p[i+size:], true
}
