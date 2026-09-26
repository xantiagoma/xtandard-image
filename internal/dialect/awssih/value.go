package awssih

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
)

// field is one member of a JSON object, in document order.
type field struct {
	key string
	raw json.RawMessage
}

// fields is an ordered object.
type fields []field

// lookup returns the member named key.
func (fs fields) lookup(key string) (json.RawMessage, bool) {
	for _, f := range fs {
		if f.key == key {
			return f.raw, true
		}
	}
	return nil, false
}

var errNotObject = errors.New("awssih: not a JSON object")

// maxDepth bounds nesting inside the request JSON (edits are at most three
// levels deep; anything deeper is rejected before it is walked).
const maxDepth = 16

// objectFields decodes a JSON object into its members in document order.
// A repeated name keeps the position of its first occurrence and the value
// of its last, like JavaScript's JSON.parse.
func objectFields(raw json.RawMessage) (fields, error) {
	if kind(raw) != kindObject {
		return nil, errNotObject
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil { // '{'
		return nil, err
	}
	var fs fields
	index := map[string]int{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, errNotObject
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		if i, ok := index[key]; ok {
			fs[i].raw = v
			continue
		}
		index[key] = len(fs)
		fs = append(fs, field{key, v})
	}
	return fs, nil
}

// objectMap decodes a JSON object into a map (last value wins).
func objectMap(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if kind(raw) != kindObject {
		return nil, errNotObject
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// jsonKind is the type of a JSON value, from its first byte.
type jsonKind uint8

const (
	kindInvalid jsonKind = iota
	kindNull
	kindBool
	kindNumber
	kindString
	kindArray
	kindObject
)

func kind(raw json.RawMessage) jsonKind {
	raw = bytes.TrimLeft(raw, " \t\r\n")
	if len(raw) == 0 {
		return kindInvalid
	}
	switch c := raw[0]; {
	case c == 'n':
		return kindNull
	case c == 't' || c == 'f':
		return kindBool
	case c == '"':
		return kindString
	case c == '[':
		return kindArray
	case c == '{':
		return kindObject
	case c == '-' || ('0' <= c && c <= '9'):
		return kindNumber
	default:
		return kindInvalid
	}
}

// depth reports the maximum nesting depth of raw (1 for a scalar).
func depth(raw []byte) int {
	d, maxD, inStr, esc := 0, 1, false, false
	for _, c := range raw {
		switch {
		case esc:
			esc = false
		case inStr:
			switch c {
			case '\\':
				esc = true
			case '"':
				inStr = false
			default:
			}
		case c == '"':
			inStr = true
		case c == '{' || c == '[':
			d++
			maxD = max(maxD, d+1)
		case c == '}' || c == ']':
			d--
		default:
		}
	}
	return maxD
}

// asBool decodes a JSON boolean.
func asBool(raw json.RawMessage) (b, ok bool) {
	if kind(raw) != kindBool {
		return false, false
	}
	return bytes.HasPrefix(bytes.TrimLeft(raw, " \t\r\n"), []byte("true")), true
}

// sharpFlag is sharp's rule for boolean toggles such as flip(x), flop(x),
// greyscale(x), negate(x) and flatten(x): a boolean is taken as is and any
// other value (including null, strings and objects) means true.
func sharpFlag(raw json.RawMessage) bool {
	if b, ok := asBool(raw); ok {
		return b
	}
	return true
}

// asString decodes a JSON string.
func asString(raw json.RawMessage) (string, bool) {
	if kind(raw) != kindString {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// asNumber decodes a finite JSON number, or a string holding one the way
// JavaScript's Number() reads it (surrounding whitespace allowed, "" is 0).
// SIH coerces resize dimensions with Number().
func asNumber(raw json.RawMessage) (float64, bool) {
	switch kind(raw) {
	case kindNumber:
		f, err := strconv.ParseFloat(string(bytes.TrimSpace(raw)), 64)
		return f, err == nil && !math.IsInf(f, 0)
	case kindString:
		s, _ := asString(raw)
		s = strings.TrimSpace(s)
		if s == "" {
			return 0, true
		}
		if strings.ContainsAny(s, "_xXoObBpP") { // Go-only literal forms
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil && !math.IsInf(f, 0) && !math.IsNaN(f)
	default:
		return 0, false
	}
}

// asInt decodes an integral JSON number (or numeric string) in [lo, hi].
func asInt(name string, raw json.RawMessage, lo, hi int) (int, error) {
	f, ok := asNumber(raw)
	if !ok || f != math.Trunc(f) || f < float64(lo) || f > float64(hi) {
		return 0, dialect.InvalidArgument(name, string(raw), "an integer between "+strconv.Itoa(lo)+" and "+strconv.Itoa(hi))
	}
	return int(f), nil
}

// asFloat decodes a JSON number (or numeric string) in [lo, hi].
func asFloat(name string, raw json.RawMessage, lo, hi float64) (float64, error) {
	f, ok := asNumber(raw)
	if !ok || f < lo || f > hi {
		return 0, dialect.InvalidArgument(name, string(raw),
			"a number between "+strconv.FormatFloat(lo, 'g', -1, 64)+" and "+strconv.FormatFloat(hi, 'g', -1, 64))
	}
	return f, nil
}

// jsRound is JavaScript's Math.round (halves round towards +∞).
func jsRound(f float64) float64 { return math.Floor(f + 0.5) }

// parseIntJS is JavaScript's parseInt(s, 10): optional whitespace and sign,
// then the longest run of decimal digits; no digits is NaN (ok = false).
func parseIntJS(s string) (int, bool) {
	s = strings.TrimLeft(s, " \t\n\r\v\f")
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	n, digits := 0, 0
	for digits < len(s) && '0' <= s[digits] && s[digits] <= '9' {
		if n < 1<<40 {
			n = n*10 + int(s[digits]-'0')
		}
		digits++
	}
	if digits == 0 {
		return 0, false
	}
	if neg {
		n = -n
	}
	return n, true
}
