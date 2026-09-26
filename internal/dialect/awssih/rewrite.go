package awssih

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Rewrite is SIH's "custom" request type (REWRITE_MATCH_PATTERN and
// REWRITE_SUBSTITUTION): a regular expression replacement applied to paths
// that are not base64 JSON before they are handed to the thumbor dialect.
type Rewrite struct {
	re     *regexp.Regexp
	repl   string // Go template ($1 → ${1})
	global bool
}

// ParseRewrite compiles a JavaScript-style "/pattern/flags" match pattern
// and a String.prototype.replace substitution, as SIH reads them from its
// environment (thumbor-mapper.ts parseCustomPath). Supported flags: g
// (replace every match; otherwise the first), i, m, s; u and d are
// accepted and ignored. The pattern is compiled as RE2, so JavaScript-only
// constructs (lookaround, backreferences) are rejected here, at startup.
// Substitution tokens: $1–$99, $<name>, $& and $$.
func ParseRewrite(pattern, substitution string) (*Rewrite, error) {
	body, flags, ok := cutRegexLiteral(pattern)
	if !ok {
		return nil, errors.New("awssih: rewrite pattern must be a /pattern/flags literal")
	}
	rw := &Rewrite{}
	var inline strings.Builder
	for _, f := range flags {
		switch f {
		case 'g':
			rw.global = true
		case 'i', 'm', 's':
			if !strings.ContainsRune(inline.String(), f) {
				inline.WriteRune(f)
			}
		case 'u', 'd':
		default:
			return nil, fmt.Errorf("awssih: rewrite pattern flag %q is not supported", f)
		}
	}
	if inline.Len() > 0 {
		body = "(?" + inline.String() + ")" + body
	}
	re, err := regexp.Compile(body)
	if err != nil {
		return nil, fmt.Errorf("awssih: rewrite pattern: %w", err)
	}
	rw.re = re
	if rw.repl, err = goTemplate(substitution); err != nil {
		return nil, err
	}
	return rw, nil
}

// cutRegexLiteral splits "/body/flags" the way SIH does: the flags are the
// text after the last "/".
func cutRegexLiteral(s string) (body, flags string, ok bool) {
	if len(s) < 2 || s[0] != '/' {
		return "", "", false
	}
	i := strings.LastIndexByte(s, '/')
	if i == 0 {
		return "", "", false
	}
	return s[1:i], s[i+1:], true
}

// goTemplate converts a JavaScript replacement string into a
// regexp.Expand template.
func goTemplate(js string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(js); i++ {
		c := js[i]
		if c != '$' || i+1 == len(js) {
			b.WriteByte(c)
			continue
		}
		next := js[i+1]
		switch {
		case next == '$':
			b.WriteString("$$")
			i++
		case next == '&':
			b.WriteString("${0}")
			i++
		case '0' <= next && next <= '9':
			j := i + 2
			if j < len(js) && '0' <= js[j] && js[j] <= '9' {
				j++
			}
			b.WriteString("${" + js[i+1:j] + "}")
			i = j - 1
		case next == '<':
			end := strings.IndexByte(js[i:], '>')
			if end < 0 {
				b.WriteString("$$")
				continue
			}
			b.WriteString("${" + js[i+2:i+end] + "}")
			i += end
		case next == '`' || next == '\'':
			return "", errors.New("awssih: rewrite substitution tokens $` and $' are not supported")
		default:
			b.WriteString("$$")
		}
	}
	return b.String(), nil
}

// Apply rewrites path.
func (rw *Rewrite) Apply(path string) string {
	if rw.global {
		return rw.re.ReplaceAllString(path, rw.repl)
	}
	m := rw.re.FindStringSubmatchIndex(path)
	if m == nil {
		return path
	}
	out := rw.re.ExpandString(nil, rw.repl, path, m)
	return path[:m[0]] + string(out) + path[m[1]:]
}
