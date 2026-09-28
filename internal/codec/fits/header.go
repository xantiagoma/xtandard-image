package fits

import (
	"math"
	"math/bits"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// FITS structure (FITS standard 4.0, §3-4): a file is a sequence of HDUs,
// each a header of 80-character cards in 2880-byte blocks ended by an END
// card, followed by its data padded to a multiple of 2880 bytes.
const (
	blockSize = 2880
	cardSize  = 80
	// maxHeaderBlocks bounds one header (36 000 cards).
	maxHeaderBlocks = 1000
	// maxHDUs bounds the HDUs scanned for an image.
	maxHDUs = 1000
	// maxAxes is the largest NAXIS the standard allows.
	maxAxes = 999
	// maxSide bounds each image dimension; larger values are corrupt or
	// hostile (the pixel limit refuses anything near it anyway).
	maxSide = 1 << 20
	// maxBytes bounds any size computed from a header before it is
	// compared with the file length.
	maxBytes = 1 << 62
)

// header is the keyword values of one HDU header. Only the first
// occurrence of a keyword counts. Values are kept as the raw value field
// text; the accessors parse them.
type header struct {
	cards map[string]string
	// size is the header length in bytes (a multiple of blockSize).
	size int64
}

func corrupt(what string) error { return errs.New(errs.KindCorrupt, "fits: "+what) }

func corruptf(format string, args ...any) error {
	return errs.Newf(errs.KindCorrupt, "fits: "+format, args...)
}

// parseHeader reads the header that starts at off.
func parseHeader(b []byte, off int64) (header, error) {
	h := header{cards: make(map[string]string, 64)}
	for blk := range maxHeaderBlocks {
		start := off + int64(blk)*blockSize
		if start+blockSize > int64(len(b)) {
			return header{}, corrupt("truncated header")
		}
		block := b[start : start+blockSize]
		for c := 0; c < blockSize; c += cardSize {
			card := block[c : c+cardSize]
			key := strings.TrimRight(string(card[:8]), " ")
			if key == "END" {
				h.size = int64(blk+1) * blockSize
				return h, nil
			}
			if string(card[8:10]) != "= " || key == "" {
				continue // commentary card
			}
			if _, ok := h.cards[key]; !ok {
				h.cards[key] = string(card[10:])
			}
		}
	}
	return header{}, corrupt("header without END")
}

// value returns the value field of key without its comment: a string's
// contents (quotes removed, doubled quotes unescaped, trailing spaces trimmed) or the
// trimmed token of any other type.
func (h *header) value(key string) (string, bool) {
	raw, ok := h.cards[key]
	if !ok {
		return "", false
	}
	raw = strings.TrimLeft(raw, " ")
	if strings.HasPrefix(raw, "'") {
		var sb strings.Builder
		for i := 1; i < len(raw); i++ {
			if raw[i] == '\'' {
				if i+1 < len(raw) && raw[i+1] == '\'' {
					sb.WriteByte('\'')
					i++
					continue
				}
				return strings.TrimRight(sb.String(), " "), true
			}
			sb.WriteByte(raw[i])
		}
		return "", false // unterminated string
	}
	if i := strings.IndexByte(raw, '/'); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimSpace(raw), true
}

// str returns a string value ("" when absent or not a string).
func (h *header) str(key string) string {
	raw, ok := h.cards[key]
	if !ok || !strings.HasPrefix(strings.TrimLeft(raw, " "), "'") {
		return ""
	}
	v, _ := h.value(key)
	return v
}

// logical reports a logical T value.
func (h *header) logical(key string) bool {
	v, ok := h.value(key)
	return ok && v == "T"
}

// integer returns an integer value; ok is false when absent. A present but
// malformed value is an error.
func (h *header) integer(key string) (int64, bool, error) {
	v, ok := h.value(key)
	if !ok {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(v, "+"), 10, 64)
	if err != nil {
		return 0, false, corruptf("bad integer %s", key)
	}
	return n, true, nil
}

// integerOr returns an integer value or def when absent.
func (h *header) integerOr(key string, def int64) (int64, error) {
	n, ok, err := h.integer(key)
	if err != nil || !ok {
		return def, err
	}
	return n, nil
}

// real returns a floating-point value (D exponents allowed); ok is false
// when absent. Non-finite values are errors.
func (h *header) real(key string) (float64, bool, error) {
	v, ok := h.value(key)
	if !ok {
		return 0, false, nil
	}
	f, err := strconv.ParseFloat(strings.NewReplacer("D", "E", "d", "e").Replace(v), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false, corruptf("bad number %s", key)
	}
	return f, true, nil
}

// realOr returns a floating-point value or def when absent.
func (h *header) realOr(key string, def float64) (float64, error) {
	f, ok, err := h.real(key)
	if err != nil || !ok {
		return def, err
	}
	return f, nil
}

// mul multiplies non-negative sizes, failing past maxBytes.
func mul(a, b int64) (int64, error) {
	if a < 0 || b < 0 {
		return 0, corrupt("negative size")
	}
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	if hi != 0 || lo > maxBytes {
		return 0, corrupt("size overflow")
	}
	return int64(lo), nil
}

// padded rounds n up to whole blocks.
func padded(n int64) int64 { return (n + blockSize - 1) / blockSize * blockSize }

// axes reads NAXIS and NAXISn under prefix ("NAXIS" or "ZNAXIS").
func (h *header) axes(prefix string) ([]int64, error) {
	n, ok, err := h.integer(prefix)
	if err != nil {
		return nil, err
	}
	if !ok || n < 0 || n > maxAxes {
		return nil, corruptf("bad %s", prefix)
	}
	ax := make([]int64, n)
	for i := range ax {
		v, ok, err := h.integer(prefix + strconv.Itoa(i+1))
		if err != nil {
			return nil, err
		}
		if !ok || v < 0 {
			return nil, corruptf("bad %s%d", prefix, i+1)
		}
		ax[i] = v
	}
	return ax, nil
}

// Two's complement reinterpretations of big-endian integers (FITS integers
// are signed, except 8-bit ones).

func s16(v uint16) int64 { return int64(int16(v)) } //nolint:gosec // G115: two's complement is the encoding
func s32(v uint32) int64 { return int64(int32(v)) } //nolint:gosec // G115: two's complement is the encoding
func s64(v uint64) int64 { return int64(v) }        //nolint:gosec // G115: two's complement is the encoding
