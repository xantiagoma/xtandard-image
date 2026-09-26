package simple

import (
	"net/url"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
)

// maxParams bounds the number of parameters one URL may carry.
const maxParams = 256

// ParseQuery decodes a raw query into parameters in URL order. Empty
// segments are skipped; a malformed escape is a 400.
func ParseQuery(rawQuery string) ([]KV, error) {
	return splitKV(rawQuery, "&", "=", url.QueryUnescape)
}

// splitKV splits s into key/value pairs with the given separators and
// decoder, skipping empty pairs.
func splitKV(s, pairSep, kvSep string, decode func(string) (string, error)) ([]KV, error) {
	if s == "" {
		return nil, nil
	}
	n := strings.Count(s, pairSep) + 1
	if n > maxParams {
		return nil, dialect.InvalidURL("too many parameters")
	}
	out := make([]KV, 0, n)
	for part := range strings.SplitSeq(s, pairSep) {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, kvSep)
		var err error
		if k, err = decode(k); err != nil {
			return nil, dialect.InvalidURL("invalid parameter escaping")
		}
		if v, err = decode(v); err != nil {
			return nil, dialect.InvalidURL("invalid parameter escaping")
		}
		out = append(out, KV{Key: k, Value: v})
	}
	return out, nil
}

// QuerySource extracts the source from a query parameter (Next.js url=,
// Astro href=); every other query parameter is an option. The path is not
// used: the endpoint is whatever the mount matched.
func QuerySource(param string) Extractor {
	return func(_, rawQuery string) (*Extracted, error) {
		kvs, err := ParseQuery(rawQuery)
		if err != nil {
			return nil, err
		}
		ex := &Extracted{Params: kvs[:0]}
		found := false
		for _, kv := range kvs {
			if kv.Key == param {
				if found {
					return nil, dialect.InvalidURL("duplicate source parameter")
				}
				ex.Source, found = kv.Value, true
				continue
			}
			ex.Params = append(ex.Params, kv)
		}
		if !found || ex.Source == "" {
			return nil, dialect.InvalidURL("missing source parameter")
		}
		return ex, nil
	}
}

// PathSource extracts a CDN-style URL: the path is the object key on the
// provider origin and the query holds the options.
func PathSource(path, rawQuery string) (*Extracted, error) {
	kvs, err := ParseQuery(rawQuery)
	if err != nil {
		return nil, err
	}
	return &Extracted{Source: path, Escaped: true, Params: kvs}, nil
}
