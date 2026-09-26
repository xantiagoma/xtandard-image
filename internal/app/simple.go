package app

import (
	"fmt"
	"maps"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/dialect/simple"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// addSimpleDialects registers the table-driven unpic providers listed in
// XI_DIALECTS_SIMPLE_ENABLED (docs/URL-DIALECTS.md §12): one parser per
// provider at its native prefixes and /simple/<name>/, plus the host-bound
// dispatcher on XI_DIALECTS_SIMPLE_HOSTS. xi is the shared xi_sig verifier
// (nil when signing is off).
func addSimpleDialects(cfg *config.Config, xi sign.Verifier, dialects map[string]dialect.Dialect, opts *router.Options) error {
	s := &cfg.Dialects.Simple
	if len(s.Enabled) == 0 {
		return nil
	}
	ds, mounts, err := simple.Mounts(&simple.MountOptions{
		Enabled:  s.Enabled,
		Hosts:    s.Hosts,
		Source:   s.Source,
		Policy:   router.Policy(s.UnsignedPolicy),
		Verifier: xi,
		Strict:   cfg.Dialects.Strict,
	})
	if err != nil {
		return fmt.Errorf("simple dialects: %w", err)
	}
	maps.Copy(dialects, ds)
	opts.Mounts = append(opts.Mounts, mounts...)
	return nil
}
