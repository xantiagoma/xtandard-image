package awssih

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
)

// isMapperParam reports whether key is one of SIH's query-param mapper
// parameters (query-param-mapper.ts QUERY_PARAM_MAPPING).
func isMapperParam(key string) bool {
	switch key {
	case "format", "fit", "width", "height", "rotate", "flip", "flop", "grayscale", "greyscale":
		return true
	}
	return false
}

// setField stores key in fs, keeping the position of an existing member (the
// semantics of a JavaScript property assignment).
func setField(fs *fields, key string, raw json.RawMessage) {
	for i := range *fs {
		if (*fs)[i].key == key {
			(*fs)[i].raw = raw
			return
		}
	}
	*fs = append(*fs, field{key, raw})
}

func jsonString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// queryBool is SIH's stringToBoolean: "0", "false" and "" (any case) are
// false, anything else is true.
func queryBool(s string) json.RawMessage {
	switch strings.ToLower(s) {
	case "0", "false", "":
		return json.RawMessage("false")
	}
	return json.RawMessage("true")
}

// queryInt is SIH's stringToNullInt ("" → null, else parseInt) and, with
// zeroIsNull, zeroStringToNullInt ("0" → null). parseInt yielding NaN makes
// sharp throw (a 500 in SIH); here it is a 400.
func queryInt(name, s string, zeroIsNull bool) (json.RawMessage, error) {
	if s == "" || (zeroIsNull && s == "0") {
		return json.RawMessage("null"), nil
	}
	n, ok := parseIntJS(s)
	if !ok {
		return nil, dialect.InvalidArgument(name, s, "an integer")
	}
	return json.RawMessage(strconv.Itoa(n)), nil
}

// queryEdits maps the mapper parameters to edits, in parameter order.
// With the mapper disabled they are recorded in Ignored.
func (p *Parser) queryEdits(b *builder, params []param) (fields, error) {
	var (
		out    fields
		resize fields
	)
	for _, kv := range params {
		if !isMapperParam(kv.key) {
			continue
		}
		if !p.o.QueryParams {
			b.ignore(kv.key, reasonQueryOff)
			continue
		}
		switch kv.key {
		case "format":
			setField(&out, "toFormat", jsonString(kv.value))
		case "fit":
			setField(&out, "resize", nil)
			setField(&resize, "fit", jsonString(kv.value))
		case "width", "height":
			v, err := queryInt(kv.key, kv.value, true)
			if err != nil {
				return nil, err
			}
			setField(&out, "resize", nil)
			setField(&resize, kv.key, v)
		case "rotate":
			v, err := queryInt(kv.key, kv.value, false)
			if err != nil {
				return nil, err
			}
			setField(&out, "rotate", v)
		case "flip", "flop":
			setField(&out, kv.key, queryBool(kv.value))
		default: // grayscale, greyscale
			setField(&out, "greyscale", queryBool(kv.value))
		}
	}
	if len(resize) > 0 {
		setField(&out, "resize", marshalFields(resize))
	}
	return out, nil
}

func marshalFields(fs fields) json.RawMessage {
	var b strings.Builder
	b.WriteByte('{')
	for i, f := range fs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(jsonString(f.key))
		b.WriteByte(':')
		b.Write(f.raw)
	}
	b.WriteByte('}')
	return json.RawMessage(b.String())
}

// mergeQueryEdits merges the query edits into edits as SIH does
// ({...edits, ...queryEdits}).
func (p *Parser) mergeQueryEdits(b *builder, edits fields, params []param) (fields, error) {
	qe, err := p.queryEdits(b, params)
	if err != nil {
		return nil, err
	}
	for _, f := range qe {
		setField(&edits, f.key, f.raw)
	}
	return edits, nil
}
