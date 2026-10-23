package query_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect/query"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

func TestContentQuery(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, mediaType, content, want string
		unsupported, invalid           bool
	}{
		{name: "form verbatim", mediaType: query.MediaTypeForm, content: "url=http://e.com/a.jpg&w=300&o=rs:fill:1:2/bl:2", want: "url=http://e.com/a.jpg&w=300&o=rs:fill:1:2/bl:2"},
		{name: "form keeps escapes and plus", mediaType: query.MediaTypeForm, content: "url=a%20b&text=hello+world", want: "url=a%20b&text=hello+world"},
		{name: "form trailing newline", mediaType: query.MediaTypeForm, content: "url=a&w=1\r\n", want: "url=a&w=1"},
		{name: "form empty", mediaType: query.MediaTypeForm, content: "", want: ""},
		{name: "form raw space", mediaType: query.MediaTypeForm, content: "url=a b", invalid: true},
		{name: "form hash", mediaType: query.MediaTypeForm, content: "url=a#b", invalid: true},
		{name: "form non-ascii", mediaType: query.MediaTypeForm, content: "text=é", invalid: true},
		{
			name: "json typed", mediaType: query.MediaTypeJSON,
			content: `{"url":"http://e.com/a b.jpg","w":300,"dpr":1.50,"strip":true,"crop":[10,20,"30",40],"skip":null,"text":"a&b=c ü"}`,
			want:    "url=http://e.com/a%20b.jpg&w=300&dpr=1.50&strip=true&crop=10,20,30,40&text=a%26b%3Dc%20%C3%BC",
		},
		{name: "json order kept", mediaType: query.MediaTypeJSON, content: `{"w":1,"url":"x","w2":2}`, want: "w=1&url=x&w2=2"},
		{name: "json escaped key", mediaType: query.MediaTypeJSON, content: `{"a b":"1"}`, want: "a%20b=1"},
		{name: "json empty object", mediaType: query.MediaTypeJSON, content: ` {} `, want: ""},
		{name: "json duplicate", mediaType: query.MediaTypeJSON, content: `{"w":1,"w":2}`, invalid: true},
		{name: "json nested object", mediaType: query.MediaTypeJSON, content: `{"w":{"a":1}}`, invalid: true},
		{name: "json nested array", mediaType: query.MediaTypeJSON, content: `{"w":[[1]]}`, invalid: true},
		{name: "json array null", mediaType: query.MediaTypeJSON, content: `{"w":[null]}`, invalid: true},
		{name: "json not object", mediaType: query.MediaTypeJSON, content: `["url"]`, invalid: true},
		{name: "json trailing", mediaType: query.MediaTypeJSON, content: `{"w":1}{}`, invalid: true},
		{name: "json malformed", mediaType: query.MediaTypeJSON, content: `{"w":`, invalid: true},
		{name: "json invalid utf8", mediaType: query.MediaTypeJSON, content: "{\"w\":\"\xff\"}", invalid: true},
		{name: "json empty", mediaType: query.MediaTypeJSON, content: ``, invalid: true},
		{name: "unsupported", mediaType: "text/plain", content: "url=x", unsupported: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := query.ContentQuery(tc.mediaType, []byte(tc.content))
			switch {
			case tc.unsupported:
				if !errors.Is(err, query.ErrUnsupportedMediaType) {
					t.Fatalf("err = %v, want ErrUnsupportedMediaType", err)
				}
			case tc.invalid:
				if !errors.Is(err, query.ErrInvalidContent) || errs.KindOf(err) != errs.KindInvalidURL {
					t.Fatalf("err = %v, want a 400 wrapping ErrInvalidContent", err)
				}
			case err != nil:
				t.Fatalf("err = %v", err)
			default:
				if diff := cmp.Diff(tc.want, got); diff != "" {
					t.Errorf("query (-want +got):\n%s", diff)
				}
			}
		})
	}
}

// TestContentQuerySignsLikeForm: the JSON content of a parameter list and
// its form encoding (as the clients write it) give the same query, so
// they carry the same xi_sig.
func TestContentQuerySignsLikeForm(t *testing.T) {
	t.Parallel()
	params := [][2]string{{"url", "https://e.com/ä b.jpg?x=1&y=2"}, {"w", "300"}, {"o", "rs:fill:300:200/wm:0.5:ce"}, {"text", "50% off"}}
	var form, json strings.Builder
	json.WriteString("{")
	for i, kv := range params {
		if i > 0 {
			form.WriteString("&")
			json.WriteString(",")
		}
		form.WriteString(query.EscapeQuery(kv[0]) + "=" + query.EscapeQuery(kv[1]))
		json.WriteString(`"` + kv[0] + `":"` + kv[1] + `"`)
	}
	json.WriteString("}")
	fq, err := query.ContentQuery(query.MediaTypeForm, []byte(form.String()))
	if err != nil {
		t.Fatal(err)
	}
	jq, err := query.ContentQuery(query.MediaTypeJSON, []byte(json.String()))
	if err != nil {
		t.Fatal(err)
	}
	if fq != jq {
		t.Errorf("form %q != json %q", fq, jq)
	}
}

// FuzzContentQuery: ContentQuery never panics, returns only its two error
// kinds, and every query it returns is made of URL query characters and
// parses (the server feeds it to ParseURL).
func FuzzContentQuery(f *testing.F) {
	for _, s := range []string{
		"url=http://e.com/a.jpg&w=300",
		`{"url":"http://e.com/a.jpg","w":300,"crop":[1,2,3,4],"strip":true,"x":null}`,
		`{"w":1,"w":2}`, `{"a":{"b":1}}`, `[]`, `{"a":"é😀"}`, "", "{", "%zz",
	} {
		f.Add(true, []byte(s))
		f.Add(false, []byte(s))
	}
	f.Fuzz(func(t *testing.T, form bool, content []byte) {
		mt := query.MediaTypeJSON
		if form {
			mt = query.MediaTypeForm
		}
		q, err := query.ContentQuery(mt, content)
		if err != nil {
			if !errors.Is(err, query.ErrInvalidContent) {
				t.Fatalf("unexpected error %v", err)
			}
			return
		}
		for i := range len(q) {
			c := q[i]
			if c <= ' ' || c >= 0x7f || c == '#' || c == '"' || c == '<' || c == '>' || c == '\\' || c == '^' || c == '`' || c == '{' || c == '|' || c == '}' || c == '[' || c == ']' {
				t.Fatalf("query %q holds %q", q, c)
			}
		}
		p, err := query.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = p.ParseURL("/", q) // must not panic
	})
}
