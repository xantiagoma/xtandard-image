package codec

import (
	"bytes"
	"encoding/binary"
	"slices"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// EXRHeader is what the header of a single-part OpenEXR image declares. It
// is read in Go so that the pixel limits are enforced before OpenEXR, which
// sizes its line-offset tables from the data window, ever sees the file.
type EXRHeader struct {
	// Width and Height are the size of the data window.
	Width, Height int
	// Tiled is set for tiled (not scanline) files.
	Tiled bool
	// Channels are the channel names in file order (alphabetical).
	Channels []string
	// Float reports a 32-bit (float or uint) channel among R, G, B, A and Y;
	// otherwise they are all half.
	Float bool
}

// OpenEXR version-field flags (OpenEXR file layout, "Version Field").
const (
	exrFlagTiled     = 0x200
	exrFlagLongNames = 0x400
	exrFlagDeep      = 0x800
	exrFlagMultipart = 0x1000
	exrMaxDimension  = 1 << 24 // well past any limit, below int32 overflow
	exrMaxAttributes = 1024
)

// ParseEXRHeader reads the magic number, version field and header
// attributes of an OpenEXR file (dataWindow and channels are required) and
// touches no pixel data. Multi-part and deep files, and files with none of
// the R, G, B or Y channels libvips reads, fail with errs.KindUnsupported;
// truncated or inconsistent headers with errs.KindCorrupt.
func ParseEXRHeader(b []byte) (EXRHeader, error) {
	le := binary.LittleEndian
	if len(b) < 8 || !has(b, magicEXR) {
		return EXRHeader{}, errs.New(errs.KindCorrupt, "codec: exr: bad magic number")
	}
	version := le.Uint32(b[4:])
	if version&0xff != 2 {
		return EXRHeader{}, errs.Newf(errs.KindCorrupt, "codec: exr: version %d", version&0xff)
	}
	if version&(exrFlagDeep|exrFlagMultipart) != 0 {
		return EXRHeader{}, errs.WithPublic(
			errs.New(errs.KindUnsupported, "codec: exr: multi-part or deep file"),
			"multi-part and deep OpenEXR files are not supported")
	}
	maxName := 31
	if version&exrFlagLongNames != 0 {
		maxName = 255
	}
	h := EXRHeader{Tiled: version&exrFlagTiled != 0}
	var window, channels bool
	off := 8
	for range exrMaxAttributes {
		name, n, ok := exrString(b[off:], maxName)
		if !ok {
			return EXRHeader{}, errs.New(errs.KindCorrupt, "codec: exr: truncated header")
		}
		off += n
		if name == "" {
			if !window || !channels {
				return EXRHeader{}, errs.New(errs.KindCorrupt, "codec: exr: no dataWindow or channels")
			}
			if err := h.check(); err != nil {
				return EXRHeader{}, err
			}
			return h, nil
		}
		typ, n, ok := exrString(b[off:], maxName)
		if !ok || typ == "" || len(b)-off-n < 4 {
			return EXRHeader{}, errs.New(errs.KindCorrupt, "codec: exr: truncated attribute")
		}
		off += n
		size := int64(le.Uint32(b[off:]))
		off += 4
		if size > int64(len(b)-off) {
			return EXRHeader{}, errs.Newf(errs.KindCorrupt, "codec: exr: attribute %q past the end", name)
		}
		value := b[off : off+int(size)]
		off += int(size)
		switch {
		case name == "dataWindow" && typ == "box2i":
			if len(value) != 16 {
				return EXRHeader{}, errs.New(errs.KindCorrupt, "codec: exr: bad dataWindow")
			}
			x0, y0 := int64(int32(le.Uint32(value))), int64(int32(le.Uint32(value[4:])))      //nolint:gosec // sign intended
			x1, y1 := int64(int32(le.Uint32(value[8:]))), int64(int32(le.Uint32(value[12:]))) //nolint:gosec // sign intended
			w, ht := x1-x0+1, y1-y0+1
			if w < 1 || ht < 1 || w > exrMaxDimension || ht > exrMaxDimension {
				return EXRHeader{}, errs.Newf(errs.KindCorrupt, "codec: exr: data window %dx%d", w, ht)
			}
			h.Width, h.Height, window = int(w), int(ht), true
		case name == "channels" && typ == "chlist":
			if err := h.parseChannels(value, maxName); err != nil {
				return EXRHeader{}, err
			}
			channels = true
		}
	}
	return EXRHeader{}, errs.New(errs.KindCorrupt, "codec: exr: too many attributes")
}

// parseChannels reads a chlist: per channel a name, int32 pixel type
// (0 uint, 1 half, 2 float), a pLinear byte, 3 reserved bytes and the x/y
// sampling, then an empty name.
func (h *EXRHeader) parseChannels(v []byte, maxName int) error {
	h.Channels = h.Channels[:0]
	for len(v) > 0 {
		name, n, ok := exrString(v, maxName)
		if !ok {
			return errs.New(errs.KindCorrupt, "codec: exr: bad channel list")
		}
		v = v[n:]
		if name == "" {
			return nil
		}
		if len(v) < 16 || len(h.Channels) >= 1024 {
			return errs.New(errs.KindCorrupt, "codec: exr: bad channel list")
		}
		pt := binary.LittleEndian.Uint32(v)
		if pt > 2 {
			return errs.Newf(errs.KindCorrupt, "codec: exr: channel %q pixel type %d", name, pt)
		}
		if pt != 1 && exrRGBAY(name) {
			h.Float = true
		}
		h.Channels = append(h.Channels, name)
		v = v[16:]
	}
	return errs.New(errs.KindCorrupt, "codec: exr: unterminated channel list")
}

// check refuses files libvips' RGBA reader would decode to black: no R, G,
// B or luminance (Y) channel.
func (h *EXRHeader) check() error {
	if !slices.ContainsFunc(h.Channels, func(c string) bool { return c != "A" && exrRGBAY(c) }) {
		return errs.WithPublic(
			errs.New(errs.KindUnsupported, "codec: exr: no R, G, B or Y channel"),
			"OpenEXR files without RGB or luminance channels are not supported")
	}
	return nil
}

// HasAlpha reports an A channel.
func (h *EXRHeader) HasAlpha() bool { return slices.Contains(h.Channels, "A") }

// exrRGBAY reports the channels OpenEXR's RGBA interface reads.
func exrRGBAY(c string) bool {
	switch c {
	case "R", "G", "B", "A", "Y", "RY", "BY":
		return true
	}
	return false
}

// exrString reads a NUL-terminated string of at most maxLen bytes and
// returns it with the bytes consumed (terminator included).
func exrString(b []byte, maxLen int) (string, int, bool) {
	i := bytes.IndexByte(b[:min(len(b), maxLen+1)], 0)
	if i < 0 {
		return "", 0, false
	}
	return string(b[:i]), i + 1, true
}
