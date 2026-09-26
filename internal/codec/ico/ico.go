// Portions derived from imgproxy's vips/ico.h and vips/icoload.c,
// https://github.com/imgproxy/imgproxy, Copyright (c) 2017 Sergey
// Alexandrovich, licensed under the Apache License, Version 2.0 (see /NOTICE).
// Modifications: rewritten in pure Go; entries are measured from their
// payload headers instead of the directory; explicit page selection and
// "smallest entry at least W×H" selection; CUR files; the AND mask of DIB
// entries becomes alpha; out-of-bounds entries are skipped instead of read.

// Package ico decodes and encodes Windows icon (ICO) and cursor (CUR) files
// in pure Go. An entry is either a PNG stream, returned as
// codec.Decoded.Embedded for libvips to load, or a DIB decoded by codec/bmp.
package ico

import (
	"bytes"
	"encoding/binary"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/codec/bmp"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

const (
	dirHeaderLen = 6  // ICONDIR
	dirEntryLen  = 16 // ICONDIRENTRY
	typeIcon     = 1
	typeCursor   = 2
	maxDimension = 256 // largest size an ICO directory can describe
)

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// Entry is one usable image of an icon file.
type Entry struct {
	// Index is the position in the icon directory, the value of
	// Options.Page.
	Index int
	// Width and Height come from the payload header (the directory byte
	// cannot express sizes above 256 and is often wrong).
	Width  int
	Height int
	// BitCount is the payload's bits per pixel.
	BitCount int
	// PNG reports a PNG payload; otherwise it is a DIB.
	PNG bool

	data []byte
}

// Options control Decode.
type Options struct {
	// MaxPixels bounds the chosen entry's Width*Height (errs.KindLimitExceeded).
	// Zero means codec.DefaultMaxPixels.
	MaxPixels int64
	// UsePage selects the entry whose directory index is Page. Otherwise
	// Decode picks with Pick(entries, MinWidth, MinHeight).
	UsePage bool
	Page    int
	// MinWidth and MinHeight ask for the smallest entry that is at least this
	// large (FORMATS.md: thumbnails pick the smallest entry ≥ target). Zero
	// picks the largest entry.
	MinWidth  int
	MinHeight int
}

// Sniff reports whether head starts like an ICO or CUR file: a zero reserved
// word, type 1 or 2 and at least one entry. The format has no magic number,
// so when the first directory entry is present its reserved byte must be
// zero and its data offset must lie past the directory.
func Sniff(head []byte) bool {
	if len(head) < dirHeaderLen || head[0] != 0 || head[1] != 0 ||
		(head[2] != typeIcon && head[2] != typeCursor) || head[3] != 0 {
		return false
	}
	count := int(binary.LittleEndian.Uint16(head[4:]))
	if count == 0 {
		return false
	}
	if len(head) < dirHeaderLen+dirEntryLen {
		return true
	}
	e := head[dirHeaderLen:]
	off := int64(binary.LittleEndian.Uint32(e[12:]))
	return e[3] == 0 && off >= int64(dirHeaderLen+dirEntryLen*count)
}

// Entries lists the usable entries of an icon file in directory order.
// Entries whose payload lies outside the file or whose header does not parse
// are left out; a file with none fails with errs.KindCorrupt.
func Entries(data []byte) ([]Entry, error) {
	if len(data) < dirHeaderLen || binary.LittleEndian.Uint16(data) != 0 {
		return nil, corrupt("missing icon directory")
	}
	if t := binary.LittleEndian.Uint16(data[2:]); t != typeIcon && t != typeCursor {
		return nil, errs.Newf(errs.KindCorrupt, "ico: unknown resource type %d", t)
	}
	count := int(binary.LittleEndian.Uint16(data[4:]))
	if count == 0 || len(data) < dirHeaderLen+dirEntryLen*count {
		return nil, corrupt("truncated icon directory")
	}
	entries := make([]Entry, 0, count)
	for i := range count {
		d := data[dirHeaderLen+dirEntryLen*i:]
		size := int64(binary.LittleEndian.Uint32(d[8:]))
		off := int64(binary.LittleEndian.Uint32(d[12:]))
		if off < int64(dirHeaderLen+dirEntryLen*count) || off+size > int64(len(data)) {
			continue
		}
		e, ok := parseEntry(data[off : off+size])
		if !ok {
			continue
		}
		e.Index = i
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		return nil, corrupt("no usable icon entry")
	}
	return entries, nil
}

func parseEntry(p []byte) (Entry, bool) {
	if bytes.HasPrefix(p, pngSignature) {
		// IHDR is always the first chunk: length(4) "IHDR" width height depth colour.
		if len(p) < 26 || string(p[12:16]) != "IHDR" {
			return Entry{}, false
		}
		w := binary.BigEndian.Uint32(p[16:])
		h := binary.BigEndian.Uint32(p[20:])
		if w == 0 || h == 0 || w > 1<<24 || h > 1<<24 {
			return Entry{}, false
		}
		return Entry{Width: int(w), Height: int(h), BitCount: pngBitCount(p[24], p[25]), PNG: true, data: p}, true
	}
	c, err := bmp.IconConfig(p)
	if err != nil || c.Embedded {
		return Entry{}, false
	}
	return Entry{Width: c.Width, Height: c.Height, BitCount: c.BitCount, data: p}, true
}

func pngBitCount(depth, colourType byte) int {
	channels := 0
	switch colourType {
	case 0, 3: // greyscale, indexed
		channels = 1
	case 4: // greyscale + alpha
		channels = 2
	case 2: // truecolour
		channels = 3
	case 6: // truecolour + alpha
		channels = 4
	}
	return int(depth) * channels
}

// Pick chooses an entry: with minW and minH zero the largest (by area, then
// bit depth); otherwise the smallest one covering minW×minH, falling back to
// the largest when none does. entries must not be empty.
func Pick(entries []Entry, minW, minH int) Entry {
	best := entries[0]
	for _, e := range entries[1:] {
		if better(e, best) {
			best = e
		}
	}
	if minW <= 0 && minH <= 0 {
		return best
	}
	found := false
	var pick Entry
	for _, e := range entries {
		if e.Width < minW || e.Height < minH {
			continue
		}
		a, pa := area(e), area(pick)
		if !found || a < pa || (a == pa && e.BitCount > pick.BitCount) {
			pick, found = e, true
		}
	}
	if !found {
		return best
	}
	return pick
}

func better(a, b Entry) bool {
	if area(a) != area(b) {
		return area(a) > area(b)
	}
	return a.BitCount > b.BitCount
}

func area(e Entry) int64 { return int64(e.Width) * int64(e.Height) }

// Decode selects one entry (see Options) and decodes it: a PNG entry comes
// back as Embedded, a DIB entry as Raw with the AND mask applied as alpha.
// Malformed input fails with errs.KindCorrupt, an entry over MaxPixels with
// errs.KindLimitExceeded; Decode never panics.
func Decode(data []byte, o Options) (codec.Decoded, error) {
	entries, err := Entries(data)
	if err != nil {
		return codec.Decoded{}, err
	}
	var e Entry
	if o.UsePage {
		i := indexOf(entries, o.Page)
		if i < 0 {
			return codec.Decoded{}, errs.Newf(errs.KindCorrupt, "ico: page %d not found or unreadable", o.Page)
		}
		e = entries[i]
	} else {
		e = Pick(entries, o.MinWidth, o.MinHeight)
	}
	limit := o.MaxPixels
	if limit <= 0 {
		limit = codec.DefaultMaxPixels
	}
	if area(e) > limit {
		return codec.Decoded{}, errs.Newf(errs.KindLimitExceeded,
			"ico: %dx%d exceeds %d pixels", e.Width, e.Height, limit)
	}
	if e.PNG {
		return codec.Decoded{Embedded: e.data}, nil
	}
	return bmp.DecodeIcon(e.data, bmp.Options{MaxPixels: limit})
}

func indexOf(entries []Entry, page int) int {
	for i, e := range entries {
		if e.Index == page {
			return i
		}
	}
	return -1
}

func corrupt(msg string) error { return errs.New(errs.KindCorrupt, "ico: "+msg) }
