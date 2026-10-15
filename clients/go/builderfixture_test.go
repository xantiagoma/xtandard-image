package xtandard_test

// testdata/urls/imgproxy-builder.json: 600 cases derived from the server's
// golden tables. The URLs were written by the TypeScript client and parsed
// back by the Go dialect tests (TestBuilderFixture); this client must write
// the same ones: url with option names as given, url_short with the default
// short names, and query_url (query dialect) where the case has one.

import (
	"testing"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
	"github.com/xantiagoma/xtandard-image/clients/go/imgproxy"
	"github.com/xantiagoma/xtandard-image/clients/go/query"
)

type builderCase struct {
	Name          string               `json:"name"`
	Endpoint      string               `json:"endpoint"`
	Encoding      string               `json:"encoding"`
	Source        string               `json:"source"`
	Extension     string               `json:"extension"`
	Separator     string               `json:"separator"`
	SignatureSize int                  `json:"signature_size"`
	Pipelines     [][]map[string][]any `json:"pipelines"`
	Query         bool                 `json:"query"`
	URL           string               `json:"url"`
	URLShort      string               `json:"url_short"`
	QueryURL      string               `json:"query_url"`
}

type builderFixture struct {
	Signing    struct{ Key, Salt string } `json:"signing"`
	Encryption struct{ Key, IV string }   `json:"encryption"`
	Cases      []builderCase              `json:"cases"`
}

// option returns the one name and arguments of a pipeline entry ({"rs": [..]}).
func option(t *testing.T, entry map[string][]any) xtandard.Option {
	t.Helper()
	if len(entry) != 1 {
		t.Fatalf("pipeline entry with %d keys", len(entry))
	}
	for name, args := range entry {
		return xtandard.Opt(name, args...)
	}
	return xtandard.Option{}
}

func TestBuilderFixture(t *testing.T) {
	t.Parallel()
	fx := loadJSON[builderFixture](t, "testdata/urls/imgproxy-builder.json")
	if len(fx.Cases) < 400 {
		t.Fatalf("only %d cases", len(fx.Cases))
	}
	enc, err := xtandard.ParseEncryptionKey(fx.Encryption.Key, fx.Encryption.IV)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range fx.Cases {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			var sk *xtandard.SigningKey
			if c.SignatureSize > 0 {
				k, err := xtandard.ParseSigningKey(fx.Signing.Key, fx.Signing.Salt, c.SignatureSize)
				if err != nil {
					t.Fatal(err)
				}
				sk = &k
			}
			fluent := func(names xtandard.OptionNames) string {
				t.Helper()
				b, err := imgproxy.New(imgproxy.Config{
					UnsignedSignature: "unsafe",
					Encryption:        &enc,
					Signing:           sk,
					Separator:         c.Separator,
					OptionNames:       names,
				})
				if err != nil {
					t.Fatal(err)
				}
				enc := xtandard.SourceEncoding(c.Encoding)
				var url string
				switch c.Endpoint {
				case "info":
					ch := b.Info(c.Source).Encoding(enc)
					for _, pl := range c.Pipelines {
						for _, e := range pl {
							o := option(t, e)
							ch.Option(o.Name, o.Args...)
						}
					}
					url, err = ch.Build()
				case "video":
					ch := b.Video(c.Source).Encoding(enc).Extension(c.Extension)
					for _, pl := range c.Pipelines {
						for _, e := range pl {
							o := option(t, e)
							ch.Option(o.Name, o.Args...)
						}
					}
					url, err = ch.Build()
				default:
					ch := b.Image(c.Source).Encoding(enc).Extension(c.Extension)
					for i, pl := range c.Pipelines {
						if i > 0 {
							ch.Pipeline()
						}
						for _, e := range pl {
							o := option(t, e)
							ch.Option(o.Name, o.Args...)
						}
					}
					url, err = ch.Build()
				}
				if err != nil {
					t.Fatal(err)
				}
				return url
			}
			if got := fluent(xtandard.GivenNames); got != c.URL {
				t.Errorf("given names:\n got %s\nwant %s", got, c.URL)
			}
			if got := fluent(xtandard.ShortNames); got != c.URLShort {
				t.Errorf("short names:\n got %s\nwant %s", got, c.URLShort)
			}
			if !c.Query {
				return
			}
			q, err := query.New(query.Config{Signing: sk})
			if err != nil {
				t.Fatal(err)
			}
			var got string
			if c.Endpoint == "video" {
				ch := q.Video(c.Source).Extension(c.Extension)
				for _, pl := range c.Pipelines {
					for _, e := range pl {
						o := option(t, e)
						ch.Option(o.Name, o.Args...)
					}
				}
				got, err = ch.Build()
			} else {
				ch := q.Image(c.Source).Extension(c.Extension)
				for i, pl := range c.Pipelines {
					if i > 0 {
						ch.Pipeline()
					}
					for _, e := range pl {
						o := option(t, e)
						ch.Option(o.Name, o.Args...)
					}
				}
				got, err = ch.Build()
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != c.QueryURL {
				t.Errorf("query:\n got %s\nwant %s", got, c.QueryURL)
			}
		})
	}
}
