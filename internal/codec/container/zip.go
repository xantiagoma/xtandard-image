package container

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// The ZIP reader below reads designer containers (OpenRaster, Krita,
// Procreate, Sketch) from memory. It is deliberately small instead of
// archive/zip: it parses only the classic end-of-central-directory record
// and central directory (no ZIP64, no multi-disk archives, no encryption),
// validates every name before anything is read, and inflates only the one
// entry a caller asks for, straight into the caller's buffer, with the
// size, ratio and CRC checks below. Nothing is ever written to disk, so
// path traversal cannot write files; names that would traverse are still
// refused, because an archive that carries them is hostile or broken.

// ZIP record signatures and fixed sizes (APPNOTE.TXT 6.3.10, §4.3).
const (
	sigLocal     = 0x04034b50
	sigCentral   = 0x02014b50
	sigEOCD      = 0x06054b50
	sigEOCD64Loc = 0x07064b50
	localLen     = 30
	centralLen   = 46
	eocdLen      = 22
	maxComment   = 0xFFFF

	methodStored  = 0
	methodDeflate = 8

	flagEncrypted = 1 << 0
	flagStrong    = 1 << 6 // strong encryption
	flagMasked    = 1 << 13
)

// Default ZIP limits (ZipLimits zero values).
const (
	// DefaultMaxEntries bounds the central directory. A Procreate document
	// stores each layer as tiles (one entry per 256 px chunk), so large
	// canvases with many layers reach a few thousand entries.
	DefaultMaxEntries = 20_000
	// DefaultMaxEntrySize bounds the uncompressed size of the entry that
	// is read.
	DefaultMaxEntrySize = 64 << 20
	// DefaultMaxTotalSize bounds the sum of the uncompressed sizes the
	// central directory declares: a sanity limit against forged
	// directories, well above any real document.
	DefaultMaxTotalSize = 4 << 30
	// DefaultMaxRatio bounds uncompressed/compressed size of an entry that
	// is inflated. PNG inside a ZIP barely compresses (about 1:1) and XML
	// about 1:10; deflate can reach about 1:1032, which only a bomb needs.
	DefaultMaxRatio = 200
	// maxNameLen bounds one entry name.
	maxNameLen = 1024
)

// ZipLimits bound what an archive may declare and what a read inflates.
// Zero fields take the defaults above.
type ZipLimits struct {
	// MaxEntries bounds the number of central directory records
	// (errs.KindLimitExceeded).
	MaxEntries int
	// MaxEntrySize bounds the uncompressed size of an entry that is read
	// (errs.KindLimitExceeded).
	MaxEntrySize int64
	// MaxTotalSize bounds the sum of every entry's declared uncompressed
	// size (errs.KindLimitExceeded).
	MaxTotalSize int64
	// MaxRatio bounds an inflated entry's uncompressed/compressed ratio
	// (errs.KindLimitExceeded).
	MaxRatio int64
}

func (l ZipLimits) withDefaults() ZipLimits {
	if l.MaxEntries <= 0 {
		l.MaxEntries = DefaultMaxEntries
	}
	if l.MaxEntrySize <= 0 {
		l.MaxEntrySize = DefaultMaxEntrySize
	}
	if l.MaxTotalSize <= 0 {
		l.MaxTotalSize = DefaultMaxTotalSize
	}
	if l.MaxRatio <= 0 {
		l.MaxRatio = DefaultMaxRatio
	}
	return l
}

// ZipEntry is one central directory record.
type ZipEntry struct {
	// Name is the entry's path inside the archive ("mergedimage.png").
	Name string
	// Size is the declared uncompressed size, CompressedSize the stored
	// size.
	Size, CompressedSize int64
	method               uint16
	flags                uint16
	crc                  uint32
	offset               int64 // local header offset
}

// Zip is a parsed archive over the caller's bytes (not copied).
type Zip struct {
	data    []byte
	entries []ZipEntry
	cdStart int64 // local data must end before the central directory
	lim     ZipLimits
}

// IsZip reports whether data starts with a ZIP local file header.
func IsZip(data []byte) bool {
	return len(data) >= 4 && binary.LittleEndian.Uint32(data) == sigLocal
}

// OpenZip parses data's end-of-central-directory record and central
// directory and validates every entry name. Truncated or inconsistent
// structures and names that are absolute, contain "..", backslashes or NUL
// fail with errs.KindCorrupt; ZIP64 and multi-disk archives with
// errs.KindUnsupported; more entries or a larger declared total than lim
// allows with errs.KindLimitExceeded.
func OpenZip(data []byte, lim ZipLimits) (*Zip, error) {
	lim = lim.withDefaults()
	eocd, err := findEOCD(data)
	if err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	rec := data[eocd:]
	disk, cdDisk := le.Uint16(rec[4:]), le.Uint16(rec[6:])
	onDisk, total := le.Uint16(rec[8:]), le.Uint16(rec[10:])
	cdSize, cdOff := int64(le.Uint32(rec[12:])), int64(le.Uint32(rec[16:]))
	switch {
	case total == 0xFFFF || cdSize == 0xFFFFFFFF || cdOff == 0xFFFFFFFF ||
		(eocd >= 20 && le.Uint32(data[eocd-20:]) == sigEOCD64Loc):
		return nil, errs.New(errs.KindUnsupported, "container: zip64 archive")
	case disk != 0 || cdDisk != 0 || onDisk != total:
		return nil, errs.New(errs.KindUnsupported, "container: multi-disk zip archive")
	case int(total) > lim.MaxEntries:
		return nil, errs.Newf(errs.KindLimitExceeded, "container: zip of %d entries exceeds the limit of %d",
			total, lim.MaxEntries)
	case cdOff+cdSize > int64(eocd) || int64(total)*centralLen > cdSize:
		return nil, errs.New(errs.KindCorrupt, "container: zip central directory outside the archive")
	}
	z := &Zip{data: data, cdStart: cdOff, lim: lim, entries: make([]ZipEntry, 0, total)}
	cd := data[cdOff : cdOff+cdSize]
	var sum int64
	for range total {
		e, n, err := parseCentral(cd)
		if err != nil {
			return nil, err
		}
		cd = cd[n:]
		if e.offset+localLen > cdOff {
			return nil, errs.New(errs.KindCorrupt, "container: zip entry outside the archive")
		}
		sum += e.Size
		if sum > lim.MaxTotalSize {
			return nil, errs.Newf(errs.KindLimitExceeded, "container: zip declares more than %d uncompressed bytes",
				lim.MaxTotalSize)
		}
		z.entries = append(z.entries, e)
	}
	return z, nil
}

// findEOCD returns the offset of the end-of-central-directory record: the
// last signature whose comment ends exactly at the end of data, so bytes
// appended after an archive (or a signature inside a comment) cannot
// redirect the reader.
func findEOCD(data []byte) (int, error) {
	if len(data) < eocdLen {
		return 0, errs.New(errs.KindCorrupt, "container: zip too short")
	}
	lo := max(0, len(data)-eocdLen-maxComment)
	for i := len(data) - eocdLen; i >= lo; i-- {
		if binary.LittleEndian.Uint32(data[i:]) != sigEOCD {
			continue
		}
		if i+eocdLen+int(binary.LittleEndian.Uint16(data[i+20:])) == len(data) {
			return i, nil
		}
	}
	return 0, errs.New(errs.KindCorrupt, "container: zip end of central directory not found")
}

// parseCentral reads one central directory record from the start of cd and
// returns it with its length.
func parseCentral(cd []byte) (ZipEntry, int, error) {
	le := binary.LittleEndian
	if len(cd) < centralLen || le.Uint32(cd) != sigCentral {
		return ZipEntry{}, 0, errs.New(errs.KindCorrupt, "container: bad zip central directory record")
	}
	nameLen, extraLen, commentLen := int(le.Uint16(cd[28:])), int(le.Uint16(cd[30:])), int(le.Uint16(cd[32:]))
	n := centralLen + nameLen + extraLen + commentLen
	if n > len(cd) {
		return ZipEntry{}, 0, errs.New(errs.KindCorrupt, "container: truncated zip central directory")
	}
	e := ZipEntry{
		flags:          le.Uint16(cd[8:]),
		method:         le.Uint16(cd[10:]),
		crc:            le.Uint32(cd[16:]),
		CompressedSize: int64(le.Uint32(cd[20:])),
		Size:           int64(le.Uint32(cd[24:])),
		offset:         int64(le.Uint32(cd[42:])),
		Name:           string(cd[centralLen : centralLen+nameLen]),
	}
	if le.Uint16(cd[34:]) != 0 {
		return ZipEntry{}, 0, errs.New(errs.KindUnsupported, "container: multi-disk zip archive")
	}
	if err := checkName(e.Name); err != nil {
		return ZipEntry{}, 0, err
	}
	return e, n, nil
}

// checkName refuses entry names that could address anything outside the
// archive root, or that different tools would read differently.
func checkName(name string) error {
	switch {
	case name == "" || len(name) > maxNameLen:
		return errs.New(errs.KindCorrupt, "container: zip entry name empty or too long")
	case strings.ContainsAny(name, "\x00\\"):
		return errs.New(errs.KindCorrupt, "container: zip entry name with NUL or backslash")
	case name[0] == '/' || (len(name) >= 2 && name[1] == ':'):
		return errs.New(errs.KindCorrupt, "container: absolute zip entry name")
	}
	for seg := range strings.SplitSeq(name, "/") {
		if seg == ".." {
			return errs.New(errs.KindCorrupt, "container: zip entry name traverses the archive root")
		}
	}
	return nil
}

// Entries returns the central directory records in archive order.
func (z *Zip) Entries() []ZipEntry { return z.entries }

// Lookup returns the entry named name. An archive with two entries of that
// name is ambiguous (readers disagree on which one wins) and fails with
// errs.KindCorrupt; ok is false when there is none.
func (z *Zip) Lookup(name string) (*ZipEntry, bool, error) {
	var found *ZipEntry
	for i := range z.entries {
		if z.entries[i].Name != name {
			continue
		}
		if found != nil {
			return nil, false, errs.Newf(errs.KindCorrupt, "container: duplicate zip entry %q", name)
		}
		found = &z.entries[i]
	}
	return found, found != nil, nil
}

// Has reports whether the archive has exactly one entry named name.
func (z *Zip) Has(name string) bool {
	_, ok, err := z.Lookup(name)
	return ok && err == nil
}

// Check validates e for reading without inflating it: stored or deflate,
// not encrypted, within the entry size and ratio limits, its local header
// consistent with the central directory and its data inside the archive.
// It returns where the data lies.
func (z *Zip) Check(e *ZipEntry) (start, end int64, err error) {
	switch {
	case e.flags&(flagEncrypted|flagStrong|flagMasked) != 0:
		return 0, 0, errs.Newf(errs.KindUnsupported, "container: zip entry %q is encrypted", e.Name)
	case e.method != methodStored && e.method != methodDeflate:
		return 0, 0, errs.Newf(errs.KindUnsupported, "container: zip entry %q uses compression method %d",
			e.Name, e.method)
	case e.Size > z.lim.MaxEntrySize:
		return 0, 0, errs.Newf(errs.KindLimitExceeded, "container: zip entry of %d bytes exceeds the limit of %d",
			e.Size, z.lim.MaxEntrySize)
	case e.method == methodStored && e.CompressedSize != e.Size:
		return 0, 0, errs.New(errs.KindCorrupt, "container: stored zip entry with different sizes")
	case e.method == methodDeflate && e.Size > 0 && e.CompressedSize == 0:
		return 0, 0, errs.New(errs.KindCorrupt, "container: empty deflate stream for a non-empty entry")
	case e.Size > e.CompressedSize*z.lim.MaxRatio && e.Size > 1<<10:
		// Tiny entries (a few repeated bytes) may compress beyond any ratio.
		return 0, 0, errs.Newf(errs.KindLimitExceeded, "container: zip entry compression ratio exceeds %d",
			z.lim.MaxRatio)
	}
	le := binary.LittleEndian
	loc := z.data[e.offset:]
	if len(loc) < localLen || le.Uint32(loc) != sigLocal {
		return 0, 0, errs.New(errs.KindCorrupt, "container: bad zip local header")
	}
	nameLen, extraLen := int64(le.Uint16(loc[26:])), int64(le.Uint16(loc[28:]))
	start = e.offset + localLen + nameLen + extraLen
	end = start + e.CompressedSize
	switch {
	case end > z.cdStart:
		return 0, 0, errs.New(errs.KindCorrupt, "container: zip entry data outside the archive")
	case string(loc[localLen:localLen+nameLen]) != e.Name:
		return 0, 0, errs.New(errs.KindCorrupt, "container: zip local header names another entry")
	case le.Uint16(loc[8:]) != e.method:
		return 0, 0, errs.New(errs.KindCorrupt, "container: zip local header disagrees on the method")
	}
	return start, end, nil
}

// ReadInto inflates e into dst, which must be exactly e.Size bytes. The
// stream must produce exactly that many bytes with the recorded CRC-32;
// anything else fails with errs.KindCorrupt, limits as Check describes.
func (z *Zip) ReadInto(e *ZipEntry, dst []byte) error {
	start, end, err := z.Check(e)
	if err != nil {
		return err
	}
	if int64(len(dst)) != e.Size {
		return errs.Newf(errs.KindInternal, "container: buffer of %d bytes for a %d-byte entry", len(dst), e.Size)
	}
	raw := z.data[start:end]
	if e.method == methodStored {
		copy(dst, raw)
	} else {
		fr := flate.NewReader(bytes.NewReader(raw))
		defer fr.Close()
		if _, err := io.ReadFull(fr, dst); err != nil {
			return errs.Wrapf(errs.KindCorrupt, err, "container: inflate %q", e.Name)
		}
		// The stream must end here: more output means the declared size
		// lied (a bomb in disguise), and a truncated stream is corrupt.
		var one [1]byte
		if n, err := fr.Read(one[:]); n != 0 || !errors.Is(err, io.EOF) {
			return errs.Newf(errs.KindCorrupt, "container: zip entry %q is longer than declared", e.Name)
		}
	}
	if crc32.ChecksumIEEE(dst) != e.crc {
		return errs.Newf(errs.KindCorrupt, "container: zip entry %q fails its CRC", e.Name)
	}
	return nil
}

// ReadSmall reads e into Go memory when it is at most maxLen bytes (a
// mimetype or a manifest); larger entries fail with errs.KindCorrupt.
func (z *Zip) ReadSmall(e *ZipEntry, maxLen int64) ([]byte, error) {
	if e.Size > maxLen {
		return nil, errs.Newf(errs.KindCorrupt, "container: zip entry %q of %d bytes, want at most %d",
			e.Name, e.Size, maxLen)
	}
	b := make([]byte, e.Size)
	if err := z.ReadInto(e, b); err != nil {
		return nil, err
	}
	return b, nil
}
