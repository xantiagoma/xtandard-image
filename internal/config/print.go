package config

import (
	"maps"
	"time"

	"github.com/goccy/go-yaml"
)

// Redacted returns the configuration as YAML for `config print`. Every
// Secret and HexKey renders as "[REDACTED]" and http source header values
// are redacted too, so the output is safe to paste into an issue.
func (c *Config) Redacted() string {
	cp := *c
	cp.Sources = maps.Clone(c.Sources)
	for name := range cp.Sources {
		s := cp.Sources[name]
		if len(s.Headers) > 0 {
			h := make(map[string]Secret, len(s.Headers))
			for k := range s.Headers {
				h[k] = redacted
			}
			s.Headers = h
			cp.Sources[name] = s
		}
	}
	b, err := yaml.MarshalWithOptions(&cp,
		yaml.CustomMarshaler(func(d time.Duration) ([]byte, error) { return []byte(d.String()), nil }),
	)
	if err != nil {
		return "# error: " + err.Error() + "\n"
	}
	return string(b)
}

// String implements fmt.Stringer with the redacted YAML.
func (c *Config) String() string { return c.Redacted() }
