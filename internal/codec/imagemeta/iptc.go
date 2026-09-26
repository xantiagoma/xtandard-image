// IPTC-IIM and Photoshop resource parsing derived from imgproxy's
// imagemeta/iptc/iptc.go, imagemeta/iptc/tags.go and
// imagemeta/photoshop/photoshop.go, https://github.com/imgproxy/imgproxy,
// Copyright (c) 2017 Sergey Alexandrovich, licensed under the Apache License,
// Version 2.0 (see /NOTICE). Modifications: rewritten over byte slices with
// bounds checks (truncated and oversized records end the parse instead of
// panicking), Pascal-string resource names, raw IIM input (TIFF) besides
// Photoshop resource blocks, and the Photoshop resolution block decoded for
// the /info response.

package imagemeta

import (
	"bytes"
	"encoding/binary"
)

// iptcFormat is the value type of an IPTC dataset.
type iptcFormat uint8

const (
	iptcByte iptcFormat = iota
	iptcShort
	iptcLong
	iptcString
	iptcBinary
	iptcDate
	iptcTime
)

// iptcKey is a record:dataset number pair ("2:120" is the caption).
type iptcKey struct {
	Record, Dataset byte
}

// iptcTag describes one dataset.
type iptcTag struct {
	Name       string
	Title      string
	Format     iptcFormat
	Required   bool
	Repeatable bool
	MinSize    int
	MaxSize    int
}

// Photoshop image resource block framing.
var (
	ps3Header      = []byte("Photoshop 3.0\x00")
	ps3BlockHeader = []byte("8BIM")
)

// Photoshop image resource IDs.
const (
	psResourceIPTC       = 0x0404
	psResourceResolution = 0x03ED
)

// iptcMarker starts every IPTC-IIM dataset.
const iptcMarker = 0x1c

// IPTC is the IPTC and Photoshop metadata of an image in the shape of
// imgproxy's /info response.
type IPTC struct {
	// Tags maps dataset titles ("Copyright Notice", "Keywords") to values:
	// a string, an int or bytes, or a list of them for repeatable datasets.
	// "Name" and "Caption" repeat Object Name (2:5) and Caption/Abstract
	// (2:120) like imgproxy does for backward compatibility. Empty when the
	// input has no IPTC datasets.
	Tags map[string]any
	// Photoshop is {"resolution": {...}} when the input carries a Photoshop
	// ResolutionInfo resource, else nil.
	Photoshop map[string]any
}

// ParseIPTC parses what libvips stores as "iptc-data": a Photoshop image
// resource section ("Photoshop 3.0\0" followed by 8BIM blocks, from a JPEG
// APP13 segment) or raw IPTC-IIM datasets (TIFF). Malformed input yields
// whatever was parsed before the error; it never fails.
func ParseIPTC(data []byte) IPTC {
	out := IPTC{Tags: map[string]any{}}
	iim := data
	if rest, ok := bytes.CutPrefix(data, ps3Header); ok {
		blocks := photoshopBlocks(rest)
		iim = blocks[psResourceIPTC]
		if res, ok := photoshopResolution(blocks[psResourceResolution]); ok {
			out.Photoshop = map[string]any{"resolution": res}
		}
	}
	parseIIM(iim, out.Tags)
	return out
}

// photoshopBlocks splits a resource section into its blocks by resource ID;
// a later block with the same ID wins.
func photoshopBlocks(data []byte) map[uint16][]byte {
	blocks := map[uint16][]byte{}
	for len(data) >= 12 { // "8BIM" + id + empty name (2) + size (4)
		if !bytes.HasPrefix(data, ps3BlockHeader) {
			data = data[1:] // resynchronise, like imgproxy
			continue
		}
		id := binary.BigEndian.Uint16(data[4:6])
		data = data[6:]
		// Pascal-string name padded to an even length (length byte included).
		nameLen := 1 + int(data[0])
		nameLen += nameLen & 1
		if len(data) < nameLen+4 {
			break
		}
		data = data[nameLen:]
		size := binary.BigEndian.Uint32(data[:4])
		data = data[4:]
		if uint64(size) > uint64(len(data)) {
			break
		}
		blocks[id] = data[:size]
		data = data[size:]
		if size&1 == 1 && len(data) > 0 { // resource data is padded to even
			data = data[1:]
		}
	}
	return blocks
}

// photoshopResolution decodes a ResolutionInfo resource: 16.16 fixed-point
// horizontal and vertical resolutions with their display units.
func photoshopResolution(b []byte) (map[string]any, bool) {
	if len(b) < 16 {
		return nil, false
	}
	fixed := func(p []byte) float64 { return float64(binary.BigEndian.Uint32(p)) / 65536 }
	unit := func(p []byte) uint16 { return binary.BigEndian.Uint16(p) }
	return map[string]any{
		"XResolution":     fixed(b[0:4]),
		"XResolutionUnit": resolutionUnit(unit(b[4:6])),
		"WidthUnit":       lengthUnit(unit(b[6:8])),
		"YResolution":     fixed(b[8:12]),
		"YResolutionUnit": resolutionUnit(unit(b[12:14])),
		"HeightUnit":      lengthUnit(unit(b[14:16])),
	}, true
}

func resolutionUnit(u uint16) string {
	switch u {
	case 1:
		return "inches"
	case 2:
		return "cm"
	default:
		return "unknown"
	}
}

func lengthUnit(u uint16) string {
	switch u {
	case 1:
		return "inches"
	case 2:
		return "cm"
	case 3:
		return "points"
	case 4:
		return "picas"
	case 5:
		return "columns"
	default:
		return "unknown"
	}
}

// parseIIM adds every known dataset of an IPTC-IIM stream to tags.
// Datasets with an unknown number or an out-of-range size are skipped;
// bytes that do not start a dataset are skipped one at a time.
func parseIIM(data []byte, tags map[string]any) {
	values := map[iptcKey][]any{}
	var order []iptcKey
	for len(data) >= 5 {
		if data[0] != iptcMarker {
			data = data[1:]
			continue
		}
		key := iptcKey{data[1], data[2]}
		size := uint64(binary.BigEndian.Uint16(data[3:5]))
		data = data[5:]
		if size >= 1<<15 { // extended dataset: the low bits give the length of the size field
			n := int(size & (1<<15 - 1))
			if (n != 4 && n != 8) || len(data) < n {
				break
			}
			size = 0
			for _, b := range data[:n] {
				size = size<<8 | uint64(b)
			}
			data = data[n:]
		}
		if size > uint64(len(data)) {
			break
		}
		raw := data[:size]
		data = data[size:]

		tag, ok := iptcTags[key]
		if !ok || len(raw) < tag.MinSize || len(raw) > tag.MaxSize {
			continue
		}
		if _, seen := values[key]; !seen {
			order = append(order, key)
		}
		v := iptcValue(tag.Format, raw)
		if tag.Repeatable {
			values[key] = append(values[key], v)
		} else {
			values[key] = []any{v}
		}
	}
	for _, key := range order {
		tag := iptcTags[key]
		vs := values[key]
		if tag.Repeatable {
			tags[tag.Title] = vs
		} else {
			tags[tag.Title] = vs[0]
		}
		if key.Record == 2 {
			switch key.Dataset {
			case 5:
				tags["Name"] = vs[0]
			case 120:
				tags["Caption"] = vs[0]
			}
		}
	}
}

// iptcValue converts a dataset to its JSON value: numbers for the integer
// formats, bytes for binary data, strings otherwise.
func iptcValue(f iptcFormat, raw []byte) any {
	switch f {
	case iptcByte, iptcShort, iptcLong:
		switch len(raw) {
		case 0:
			return 0
		case 1:
			return int(raw[0])
		case 2:
			return int(binary.BigEndian.Uint16(raw))
		case 3:
			return int(binary.BigEndian.Uint16(raw[:2]))<<8 | int(raw[2])
		default:
			return int(binary.BigEndian.Uint32(raw[:4]))
		}
	case iptcBinary:
		return bytes.Clone(raw)
	default:
		return string(raw)
	}
}
