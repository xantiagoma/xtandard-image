package transform

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"
)

// canonicalKeyVersion is hashed first. Bump it only for a deliberate change of
// the encoding below; the golden test in canonical_test.go pins the result.
const canonicalKeyVersion = "xtandard-image/transform.CanonicalKey/v1\x00"

// CanonicalKey returns a stable identifier of everything in r that can change
// the response: 64 lowercase hex digits of a SHA-256. It is the input of the
// result-cache key and the ETag. Call it after ApplyDefaults (and after the
// server resolves ClientHints and a negotiated format), so that requests that
// mean the same thing produce the same key.
//
// # Algorithm
//
// The hash input is canonicalKeyVersion followed by the encoding of r, where
// encode(v) walks v by reflection:
//
//   - struct: '{', then every field that is not tagged `key:"-"` and is not
//     zero (see below), sorted by Go field name, each as
//     str(name) encode(value), then '}'. Sorting by name makes the key
//     independent of field declaration order; omitting zero fields means
//     adding a new field does not change existing keys.
//   - time.Time: 'T' varint(Unix seconds) varint(nanoseconds); zero = omitted.
//   - bool: 'b' 0x01 (false is zero, so only true is ever written as a field).
//   - signed integers (incl. time.Duration): 'i' zigzag varint.
//   - enum (a uint8 type with a String method): 'e' str(String()); the
//     canonical name, so reordering constants never changes a key.
//   - other unsigned integers: 'u' uvarint.
//   - float: 'f' 8 bytes big-endian IEEE-754 binary64, after mapping −0 to +0
//     and every NaN to 0x7ff8000000000001. Exact, and independent of any
//     decimal formatting. ±0 is zero and therefore omitted as a field.
//   - string: 's' str(value), where str(x) = varint(len(x)) x.
//   - pointer: 'p' encode(*ptr); a nil pointer is zero (omitted as a field,
//     'n' inside a slice). A non-nil pointer is always written, even to a
//     zero value, because nil means "config default" and &false does not.
//   - slice/array: 'l' varint(len) then encode(elem) for each element in
//     order (order is meaningful for pipelines and watermarks). An empty
//     slice is zero.
//   - map: 'm' varint(len) then pairs encode(k) encode(v) sorted by the
//     bytes of encode(k), so Go's random map order never matters.
//
// Zero means reflect.Value.IsZero, plus empty slices/maps and −0. Excluded
// fields: Request.Dialect, Request.Ignored, Delivery.Presets (ARCHITECTURE §4)
// and Delivery.BypassCache, none of which change the response body or headers
// cached with it. Only the standard library's reflect, encoding/binary,
// math.Float64bits and crypto/sha256 are involved, so the key does not depend
// on the Go version.
func (r *Request) CanonicalKey() string {
	e := keyEncoder{buf: make([]byte, 0, 512)}
	e.buf = append(e.buf, canonicalKeyVersion...)
	e.value(reflect.ValueOf(r).Elem())
	sum := sha256.Sum256(e.buf)
	return hex.EncodeToString(sum[:])
}

type keyEncoder struct {
	buf []byte
}

func (e *keyEncoder) str(s string) {
	e.length(len(s))
	e.buf = append(e.buf, s...)
}

// length appends a non-negative length (lengths from len() are never negative).
func (e *keyEncoder) length(n int) { e.buf = binary.AppendVarint(e.buf, int64(n)) }

func (e *keyEncoder) tag(c byte) { e.buf = append(e.buf, c) }

// isZeroKey reports whether v is omitted when it is a struct field.
func isZeroKey(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Slice, reflect.Map:
		return v.Len() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Struct:
		if v.Type() == reflect.TypeFor[time.Time]() {
			t, _ := reflect.TypeAssert[time.Time](v)
			return t.IsZero()
		}
		for i := range v.NumField() {
			if f := v.Type().Field(i); !skipField(&f) && !isZeroKey(v.Field(i)) {
				return false
			}
		}
		return true
	default:
		return v.IsZero()
	}
}

func skipField(f *reflect.StructField) bool {
	return !f.IsExported() || f.Tag.Get("key") == "-"
}

func (e *keyEncoder) value(v reflect.Value) {
	switch v.Kind() {
	case reflect.Struct:
		if v.Type() == reflect.TypeFor[time.Time]() {
			t, _ := reflect.TypeAssert[time.Time](v)
			e.tag('T')
			e.buf = binary.AppendVarint(e.buf, t.Unix())
			e.buf = binary.AppendVarint(e.buf, int64(t.Nanosecond()))
			return
		}
		e.structFields(v)
	case reflect.Bool:
		e.tag('b')
		if v.Bool() {
			e.buf = append(e.buf, 1)
		} else {
			e.buf = append(e.buf, 0)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		e.tag('i')
		e.buf = binary.AppendVarint(e.buf, v.Int())
	case reflect.Uint8:
		if s, ok := reflect.TypeAssert[fmt.Stringer](v); ok {
			e.tag('e')
			e.str(s.String())
			return
		}
		e.tag('u')
		e.buf = binary.AppendUvarint(e.buf, v.Uint())
	case reflect.Uint, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		e.tag('u')
		e.buf = binary.AppendUvarint(e.buf, v.Uint())
	case reflect.Float32, reflect.Float64:
		e.tag('f')
		e.buf = binary.BigEndian.AppendUint64(e.buf, canonicalFloatBits(v.Float()))
	case reflect.String:
		e.tag('s')
		e.str(v.String())
	case reflect.Pointer:
		if v.IsNil() {
			e.tag('n')
			return
		}
		e.tag('p')
		e.value(v.Elem())
	case reflect.Slice, reflect.Array:
		e.tag('l')
		e.length(v.Len())
		for i := range v.Len() {
			e.value(v.Index(i))
		}
	case reflect.Map:
		e.mapEntries(v)
	default:
		// Interfaces, channels, funcs and complex numbers never appear in the
		// model; TestCanonicalKeyCoversAllKinds keeps it that way. Encode the
		// kind so a mistake stays deterministic instead of panicking.
		e.tag('?')
		e.str(v.Kind().String())
	}
}

func (e *keyEncoder) structFields(v reflect.Value) {
	t := v.Type()
	idx := make([]int, 0, t.NumField())
	for i := range t.NumField() {
		if f := t.Field(i); !skipField(&f) && !isZeroKey(v.Field(i)) {
			idx = append(idx, i)
		}
	}
	slices.SortFunc(idx, func(a, b int) int { return strings.Compare(t.Field(a).Name, t.Field(b).Name) })
	e.tag('{')
	for _, i := range idx {
		e.str(t.Field(i).Name)
		e.value(v.Field(i))
	}
	e.tag('}')
}

func (e *keyEncoder) mapEntries(v reflect.Value) {
	type entry struct{ k, v []byte }
	entries := make([]entry, 0, v.Len())
	it := v.MapRange()
	for it.Next() {
		ke := keyEncoder{}
		ke.value(it.Key())
		ve := keyEncoder{}
		ve.value(it.Value())
		entries = append(entries, entry{ke.buf, ve.buf})
	}
	slices.SortFunc(entries, func(a, b entry) int { return bytes.Compare(a.k, b.k) })
	e.tag('m')
	e.length(len(entries))
	for _, en := range entries {
		e.buf = append(e.buf, en.k...)
		e.buf = append(e.buf, en.v...)
	}
}

// canonicalNaN is the single NaN bit pattern used in keys.
const canonicalNaN = 0x7ff8000000000001

func canonicalFloatBits(f float64) uint64 {
	switch {
	case math.IsNaN(f):
		return canonicalNaN
	case f == 0:
		return 0 // folds −0 into +0
	}
	return math.Float64bits(f)
}
