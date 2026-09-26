package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/awssih"
	"github.com/xantiagoma/xtandard-image/internal/dialect/bunny"
	"github.com/xantiagoma/xtandard-image/internal/dialect/cloudflare"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imagekit"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/dialect/query"
	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/dialect/thumbor"
	"github.com/xantiagoma/xtandard-image/internal/dialect/wsrv"
	"github.com/xantiagoma/xtandard-image/internal/fetch/azuresrc"
	"github.com/xantiagoma/xtandard-image/internal/fetch/fssrc"
	"github.com/xantiagoma/xtandard-image/internal/fetch/ftpsrc"
	"github.com/xantiagoma/xtandard-image/internal/fetch/gcssrc"
	"github.com/xantiagoma/xtandard-image/internal/fetch/httpsrc"
	"github.com/xantiagoma/xtandard-image/internal/fetch/s3src"
	"github.com/xantiagoma/xtandard-image/internal/fetch/sftpsrc"
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/preset"
	"github.com/xantiagoma/xtandard-image/internal/security/crypt"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/security/ssrf"
	"github.com/xantiagoma/xtandard-image/internal/security/svg"
	"github.com/xantiagoma/xtandard-image/internal/server"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// infoDialect key: the imgproxy /info grammar mounted under the imgproxy
// prefix (URL-DIALECTS §1).
const infoDialect = "imgproxy-info"

// infoParser adapts imgproxy's ParseInfo to dialect.Dialect.
type infoParser struct{ *imgproxy.Parser }

func (p infoParser) Name() string { return infoDialect }

func (p infoParser) Parse(in *dialect.Input) (*dialect.Result, error) { return p.ParseInfo(in) }

// buildRouter builds the signing verifiers, presets, the available dialects
// (imgproxy, query, imagekit, cloudflare, cloudflare_images, wsrv, thumbor,
// bunny and awssih) and the router. warnings lists enabled dialects that are not implemented yet; their
// mounts are skipped.
func buildRouter(cfg *config.Config) (rt *router.Router, warnings []string, err error) {
	keys := make([]sign.Key, 0, len(cfg.Signing.Keys))
	for i, k := range cfg.Signing.Keys {
		var salt []byte
		if i < len(cfg.Signing.Salts) {
			salt = cfg.Signing.Salts[i]
		}
		keys = append(keys, sign.NewKey(k, salt))
	}
	trusted := make([]string, 0, len(cfg.Signing.Trusted))
	for _, t := range cfg.Signing.Trusted {
		trusted = append(trusted, t.Reveal())
	}
	required := cfg.Signing.Required.Bool()
	var ring *sign.Keyring
	if len(keys) > 0 {
		ring = sign.NewKeyring(keys...)
	}

	ipOpts := imgproxy.Options{
		ArgumentsSeparator:   cfg.Dialects.Imgproxy.ArgumentsSeparator,
		Strict:               cfg.Dialects.Strict,
		AllowSecurityOptions: cfg.Security.AllowSecurityOptions,
		BaseURL:              cfg.Dialects.Imgproxy.BaseURL,
	}
	if ring != nil || len(trusted) > 0 {
		v, err := sign.NewImgproxyVerifier(sign.ImgproxyOptions{Keys: ring, Size: cfg.Signing.Size, Trusted: trusted, Required: required})
		if err != nil {
			return nil, nil, fmt.Errorf("imgproxy verifier: %w", err)
		}
		ipOpts.Verifier = v
	}
	if len(cfg.Encryption.Keys) > 0 {
		hexKeys := make([]string, 0, len(cfg.Encryption.Keys))
		for _, k := range cfg.Encryption.Keys {
			hexKeys = append(hexKeys, k.Hex())
		}
		if ipOpts.Cipher, err = crypt.New(hexKeys...); err != nil {
			return nil, nil, fmt.Errorf("encryption keys: %w", err)
		}
	}
	for i, pair := range cfg.Dialects.Imgproxy.URLReplacements {
		pat, repl, _ := strings.Cut(pair, "=")
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, nil, fmt.Errorf("url replacement %d: %w", i, err)
		}
		ipOpts.URLReplacements = append(ipOpts.URLReplacements, imgproxy.URLReplacement{Pattern: re, Replacement: repl})
	}
	if len(cfg.Presets) > 0 || cfg.PresetsFile != "" || cfg.PresetsOnly {
		store, err := preset.New(preset.Options{
			Definitions: cfg.Presets,
			File:        cfg.PresetsFile,
			Separator:   cfg.Dialects.Imgproxy.ArgumentsSeparator,
			Only:        cfg.PresetsOnly,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("presets: %w", err)
		}
		ipOpts.Presets = store
	}
	ip, err := imgproxy.New(&ipOpts)
	if err != nil {
		return nil, nil, fmt.Errorf("imgproxy dialect: %w", err)
	}

	qOpts := query.Options{
		ArgumentsSeparator:   cfg.Dialects.Imgproxy.ArgumentsSeparator,
		Strict:               cfg.Dialects.Strict,
		AllowSecurityOptions: cfg.Security.AllowSecurityOptions,
	}
	if ring != nil || len(trusted) > 0 {
		if qOpts.ImaginaryVerifier, err = sign.NewImaginaryVerifier(sign.ImaginaryOptions{Keys: ring, Trusted: trusted, Required: required}); err != nil {
			return nil, nil, fmt.Errorf("imaginary verifier: %w", err)
		}
		if qOpts.XIVerifier, err = sign.NewXIVerifier(sign.XIOptions{Keys: ring, Size: cfg.Signing.Size, Trusted: trusted, Required: required}); err != nil {
			return nil, nil, fmt.Errorf("xi_sig verifier: %w", err)
		}
	}
	q, err := query.New(&qOpts)
	if err != nil {
		return nil, nil, fmt.Errorf("query dialect: %w", err)
	}

	cfOpts := cloudflare.Options{Strict: cfg.Dialects.Strict, XIVerifier: qOpts.XIVerifier}
	cf, err := cloudflare.New(&cfOpts)
	if err != nil {
		return nil, nil, fmt.Errorf("cloudflare dialect: %w", err)
	}

	wsOpts := wsrv.Options{Strict: cfg.Dialects.Strict}
	if ring != nil || len(trusted) > 0 {
		if wsOpts.XIVerifier, err = sign.NewXIVerifier(sign.XIOptions{Keys: ring, Size: cfg.Signing.Size, Trusted: trusted, Required: required}); err != nil {
			return nil, nil, fmt.Errorf("wsrv xi_sig verifier: %w", err)
		}
	}
	ws, err := wsrv.New(&wsOpts)
	if err != nil {
		return nil, nil, fmt.Errorf("wsrv dialect: %w", err)
	}

	th, err := newThumbor(cfg.Dialects.Thumbor.Keys, cfg.Dialects.Strict, required)
	if err != nil {
		return nil, nil, err
	}

	dialects := map[string]dialect.Dialect{
		imgproxy.Name: ip, infoDialect: infoParser{ip}, query.Name: q, cloudflare.Name: cf, wsrv.Name: ws, thumbor.Name: th,
	}
	if sih := &cfg.Dialects.AWSSIH; sih.Enabled {
		p, warn, err := buildAWSSIH(cfg, dialects)
		if err != nil {
			return nil, nil, err
		}
		dialects[awssih.Name] = p
		warnings = append(warnings, warn...)
	}
	opts := &router.Options{
		Policy: router.Policy(cfg.Security.UnsignedPolicy),
		Restrictions: router.Restrictions{
			Widths:    cfg.Security.AllowedWidths,
			Heights:   cfg.Security.AllowedHeights,
			DPRs:      cfg.Security.AllowedDPRs,
			Qualities: cfg.Security.AllowedQualities,
		},
	}
	addMounts := func(name string, m *config.Mount) {
		if !m.Enabled {
			return
		}
		for i, mo := range append([]config.MountOptions{m.MountOptions}, m.Mounts...) {
			label := name
			if i > 0 {
				label = fmt.Sprintf("%s[%d]", name, i)
			}
			opts.Mounts = append(opts.Mounts, router.Mount{
				Name: label, Dialect: name, Prefix: mo.Prefix, Hosts: mo.Hosts,
				Source: mo.Source, Policy: router.Policy(mo.UnsignedPolicy),
			})
			if name == imgproxy.Name && mo.Prefix != "" {
				opts.Mounts = append(opts.Mounts, router.Mount{
					Name: label + "/info", Dialect: infoDialect, Prefix: strings.TrimSuffix(mo.Prefix, "/") + "/info/",
					Source: mo.Source, Policy: router.Policy(mo.UnsignedPolicy),
				})
			}
		}
	}
	addMounts(imgproxy.Name, &cfg.Dialects.Imgproxy.Mount)
	addMounts(query.Name, &cfg.Dialects.Query)
	if err := addImageKit(cfg, ipOpts.Presets, required, dialects, opts); err != nil {
		return nil, nil, err
	}
	addMounts(cloudflare.Name, &cfg.Dialects.Cloudflare)
	if err := addCloudflareImages(cfg, opts, dialects, ipOpts.Presets); err != nil {
		return nil, nil, err
	}
	addMounts(wsrv.Name, &cfg.Dialects.Wsrv)
	if err := addThumborMounts(cfg, required, dialects, opts); err != nil {
		return nil, nil, err
	}
	if err := addBunnyMounts(cfg, ipOpts.Presets, dialects, opts); err != nil {
		return nil, nil, err
	}
	addMounts(awssih.Name, &cfg.Dialects.AWSSIH.Mount)
	// Root "/?url=" (URL-DIALECTS §1): wsrv when enabled, else query.
	switch {
	case cfg.Dialects.Wsrv.Enabled:
		opts.RootURL = &router.Mount{
			Name: "wsrv/?url", Dialect: wsrv.Name, Source: cfg.Dialects.Wsrv.Source,
			Policy: router.Policy(cfg.Dialects.Wsrv.UnsignedPolicy),
		}
	case cfg.Dialects.Query.Enabled:
		opts.RootURL = &router.Mount{
			Name: "query/?url", Dialect: query.Name, Source: cfg.Dialects.Query.Source,
			Policy: router.Policy(cfg.Dialects.Query.UnsignedPolicy),
		}
	}
	if _, ok := dialects[string(cfg.Dialects.Default)]; ok && cfg.Dialects.Default != "" {
		opts.Default = router.Mount{Name: "default", Dialect: string(cfg.Dialects.Default)}
	} else if cfg.Dialects.Default != "" {
		warnings = append(warnings, fmt.Sprintf("default dialect %q is not implemented yet; unmatched requests answer 404", cfg.Dialects.Default))
	}

	for name, enabled := range map[string]bool{
		"imgix": cfg.Dialects.Imgix.Enabled, "cloudinary": cfg.Dialects.Cloudinary.Enabled,
		"simple": len(cfg.Dialects.Simple.Enabled) > 0,
	} {
		if enabled {
			warnings = append(warnings, "dialect "+name+" is enabled but not implemented yet; its mounts are skipped")
		}
	}
	slices.Sort(warnings)

	rt, err = router.New(opts, dialects)
	if err != nil {
		return nil, nil, err
	}
	return rt, warnings, nil
}

// addImageKit registers one ImageKit parser per enabled mount, each
// verifying ik-s with that mount's private keys (XI_DIALECTS_IMAGEKIT_KEYS,
// raw strings), and its router mount. Mount i > 0 is keyed "imagekit[i]".
func addImageKit(cfg *config.Config, presets *preset.Store, required bool, dialects map[string]dialect.Dialect, opts *router.Options) error {
	m := &cfg.Dialects.Imagekit
	if !m.Enabled {
		return nil
	}
	for i, mo := range append([]config.MountOptions{m.MountOptions}, m.Mounts...) {
		key := imagekit.Name
		if i > 0 {
			key = fmt.Sprintf("%s[%d]", imagekit.Name, i)
		}
		o := imagekit.Options{Strict: cfg.Dialects.Strict, Presets: presets}
		if len(mo.Keys) > 0 {
			keys := make([]sign.Key, 0, len(mo.Keys))
			for _, k := range mo.Keys {
				keys = append(keys, sign.NewKey([]byte(k.Reveal()), nil))
			}
			v, err := sign.NewImageKitVerifier(sign.ImageKitOptions{Keys: sign.NewKeyring(keys...), Required: required})
			if err != nil {
				return fmt.Errorf("%s verifier: %w", key, err)
			}
			o.Verifier = v
		}
		p, err := imagekit.New(&o)
		if err != nil {
			return fmt.Errorf("%s dialect: %w", key, err)
		}
		dialects[key] = p
		opts.Mounts = append(opts.Mounts, router.Mount{
			Name: key, Dialect: key, Prefix: mo.Prefix, Hosts: mo.Hosts,
			Source: mo.Source, Policy: router.Policy(mo.UnsignedPolicy),
		})
	}
	return nil
}

// addCloudflareImages registers one Cloudflare Images dialect per enabled
// mount, since each mount has its own signing keys, and the mounts. Variants
// are the presets.
func addCloudflareImages(cfg *config.Config, opts *router.Options, dialects map[string]dialect.Dialect, presets *preset.Store) error {
	ci := &cfg.Dialects.CloudflareImages
	if !ci.Enabled {
		return nil
	}
	for i, mo := range append([]config.MountOptions{ci.MountOptions}, ci.Mounts...) {
		name := cloudflare.ImagesName
		if i > 0 {
			name = fmt.Sprintf("%s[%d]", cloudflare.ImagesName, i)
		}
		o := cloudflare.ImagesOptions{Accounts: ci.Accounts, Presets: presets, Strict: cfg.Dialects.Strict}
		if len(mo.Keys) > 0 {
			keys := make([]sign.Key, 0, len(mo.Keys))
			for _, k := range mo.Keys {
				keys = append(keys, sign.NewKey([]byte(k.Reveal()), nil))
			}
			// Unsigned (public) images are governed by the mount's unsigned
			// policy, not XI_SIGNING_REQUIRED, which is about XI_SIGNING_KEYS.
			v, err := sign.NewCloudflareVerifier(sign.CloudflareOptions{Keys: sign.NewKeyring(keys...)})
			if err != nil {
				return fmt.Errorf("%s verifier: %w", name, err)
			}
			o.Verifier = v
		}
		d, err := cloudflare.NewImages(&o)
		if err != nil {
			return fmt.Errorf("%s dialect: %w", name, err)
		}
		dialects[name] = d
		opts.Mounts = append(opts.Mounts, router.Mount{
			Name: name, Dialect: name, Prefix: mo.Prefix, Hosts: mo.Hosts,
			Source: mo.Source, Policy: router.Policy(mo.UnsignedPolicy),
		})
	}
	return nil
}

// newThumbor builds a thumbor parser verifying with keys (thumbor
// SECURITY_KEY strings, not hex; XI_DIALECTS_THUMBOR_KEYS). Without keys
// signing is off and every URL is unsigned.
func newThumbor(keys []config.Secret, strict, required bool) (*thumbor.Parser, error) {
	o := thumbor.Options{Strict: strict}
	if len(keys) > 0 {
		ks := make([]sign.Key, 0, len(keys))
		for _, k := range keys {
			ks = append(ks, sign.NewKey([]byte(k.Reveal()), nil))
		}
		v, err := sign.NewThumborVerifier(sign.ThumborOptions{Keys: sign.NewKeyring(ks...), Required: required})
		if err != nil {
			return nil, fmt.Errorf("thumbor verifier: %w", err)
		}
		o.Verifier = v
	}
	return thumbor.New(&o), nil
}

// addThumborMounts mounts the thumbor dialect. Each extra mount verifies
// with its own keys (falling back to the primary mount's), so it gets its
// own parser under the dialect key "thumbor[i]".
func addThumborMounts(cfg *config.Config, required bool, dialects map[string]dialect.Dialect, opts *router.Options) error {
	m := &cfg.Dialects.Thumbor
	if !m.Enabled {
		return nil
	}
	for i, mo := range append([]config.MountOptions{m.MountOptions}, m.Mounts...) {
		key := thumbor.Name
		if i > 0 {
			key = fmt.Sprintf("%s[%d]", thumbor.Name, i)
			keys := mo.Keys
			if len(keys) == 0 {
				keys = m.Keys
			}
			p, err := newThumbor(keys, cfg.Dialects.Strict, required)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			dialects[key] = p
		}
		opts.Mounts = append(opts.Mounts, router.Mount{
			Name: key, Dialect: key, Prefix: mo.Prefix, Hosts: mo.Hosts,
			Source: mo.Source, Policy: router.Policy(mo.UnsignedPolicy),
		})
	}
	return nil
}

// addBunnyMounts registers one bunny parser per enabled mount, because
// each mount (pull zone) has its own token authentication keys
// (dialects.bunny[.mounts[]].keys, raw strings). The primary mount's
// parser is registered as "bunny", extra mounts as "bunny[i]".
func addBunnyMounts(cfg *config.Config, presets *preset.Store, dialects map[string]dialect.Dialect, opts *router.Options) error {
	m := &cfg.Dialects.Bunny
	if !m.Enabled {
		return nil
	}
	for i, mo := range append([]config.MountOptions{m.MountOptions}, m.Mounts...) {
		label := bunny.Name
		if i > 0 {
			label = fmt.Sprintf("%s[%d]", bunny.Name, i)
		}
		keys := make([]sign.Key, 0, len(mo.Keys))
		for _, k := range mo.Keys {
			keys = append(keys, sign.NewKey([]byte(k.Reveal()), nil))
		}
		v, err := sign.NewBunnyVerifier(sign.BunnyOptions{Keys: sign.NewKeyring(keys...), Required: cfg.Signing.Required.Bool() && len(keys) > 0})
		if err != nil {
			return fmt.Errorf("%s verifier: %w", label, err)
		}
		p, err := bunny.New(&bunny.Options{
			Verifier:           v,
			Presets:            presets,
			ArgumentsSeparator: cfg.Dialects.Imgproxy.ArgumentsSeparator,
			Strict:             cfg.Dialects.Strict,
		})
		if err != nil {
			return fmt.Errorf("%s dialect: %w", label, err)
		}
		dialects[label] = p
		opts.Mounts = append(opts.Mounts, router.Mount{
			Name: label, Dialect: label, Prefix: mo.Prefix, Hosts: mo.Hosts,
			Source: mo.Source, Policy: router.Policy(mo.UnsignedPolicy),
		})
	}
	return nil
}

// buildAWSSIH builds the AWS Serverless Image Handler dialect
// (XI_DIALECTS_AWSSIH_*). Its keys are the SIH secrets, used verbatim; the
// primary mount's keys apply to every SIH mount. Thumbor forwarding uses
// the "thumbor" dialect when one is available (WP-45 registers it; it must
// accept "/unsafe/" paths, since SIH signs with its own parameter).
func buildAWSSIH(cfg *config.Config, dialects map[string]dialect.Dialect) (*awssih.Parser, []string, error) {
	sih := &cfg.Dialects.AWSSIH
	opts := awssih.Options{
		Buckets:     sih.Buckets,
		Strict:      cfg.Dialects.Strict,
		QueryParams: sih.QueryParams,
	}
	var warnings []string
	if len(sih.Keys) > 0 {
		keys := make([]sign.Key, 0, len(sih.Keys))
		for _, k := range sih.Keys {
			keys = append(keys, sign.NewKey([]byte(k.Reveal()), nil))
		}
		trusted := make([]string, 0, len(cfg.Signing.Trusted))
		for _, t := range cfg.Signing.Trusted {
			trusted = append(trusted, t.Reveal())
		}
		v, err := sign.NewAWSSIHVerifier(sign.AWSSIHOptions{
			Keys: sign.NewKeyring(keys...), Trusted: trusted, Required: cfg.Signing.Required.Bool(),
		})
		if err != nil {
			return nil, nil, fmt.Errorf("awssih verifier: %w", err)
		}
		opts.Verifier = v
	} else if cfg.Signing.Required.Bool() {
		warnings = append(warnings, "dialect awssih has no keys: signing.required does not apply to it; unsigned requests follow its unsigned policy")
	}
	if sih.RewriteMatchPattern != "" {
		rw, err := awssih.ParseRewrite(sih.RewriteMatchPattern, sih.RewriteSubstitution)
		if err != nil {
			return nil, nil, err
		}
		opts.Rewrite = rw
	}
	if sih.Thumbor {
		if th, ok := dialects[string(config.DialectThumbor)]; ok {
			opts.Thumbor = th
		} else {
			warnings = append(warnings, "dialect awssih: thumbor forwarding needs the thumbor dialect; non-JSON paths answer 400")
		}
	}
	p, err := awssih.New(&opts)
	if err != nil {
		return nil, nil, fmt.Errorf("awssih dialect: %w", err)
	}
	return p, warnings, nil
}

// ssrfPolicy maps XI_FETCH_* onto the SSRF policy.
func ssrfPolicy(cfg *config.Config) ssrf.Policy {
	p := ssrf.Policy{}
	if cfg.Fetch.AllowPrivateNetworks {
		p.Allow = ssrf.ClassPrivateNetworks
	}
	for _, c := range cfg.Fetch.AllowedNetworks {
		p.AllowCIDRs = append(p.AllowCIDRs, c.Prefix())
	}
	for _, port := range cfg.Fetch.AllowedPorts {
		if port > 0 && port <= 65535 {
			p.Ports = append(p.Ports, uint16(port))
		}
	}
	return p
}

// sources builds every configured backend (and the dev-mode http source)
// and the registry over them. closers are the backends holding connections
// or file handles, closed on shutdown after the listeners drained.
func buildSources(ctx context.Context, cfg *config.Config, client *http.Client, userAgent string) (*source.Registry, []io.Closer, error) {
	var (
		bindings []source.Binding
		closers  []io.Closer
		problems []error
	)
	names := make([]string, 0, len(cfg.Sources))
	for name := range cfg.Sources {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		sc := cfg.Sources[name]
		src, err := newSource(ctx, name, &sc, cfg, client, userAgent)
		if err != nil {
			problems = append(problems, fmt.Errorf("source %s: %w", name, err))
			continue
		}
		if c, ok := src.(io.Closer); ok {
			closers = append(closers, c)
		}
		bindings = append(bindings, source.Binding{Spec: sourceSpec(name, &sc), Source: src})
	}
	var dev source.Source
	if cfg.DevMode {
		d, err := httpsrc.New(httpsrc.Config{Name: source.DevSourceName, Client: client, Timeout: cfg.Fetch.Timeout})
		if err != nil {
			problems = append(problems, fmt.Errorf("dev source: %w", err))
		}
		dev = d
	}
	if err := errors.Join(problems...); err != nil {
		return nil, closers, err
	}
	reg, err := source.NewRegistry(source.Options{Sources: bindings, DevMode: cfg.DevMode, Dev: dev})
	if err != nil {
		return nil, closers, err
	}
	return reg, closers, nil
}

func sourceSpec(name string, s *config.Source) source.Spec {
	return source.Spec{
		Name: name, Type: string(s.Type),
		Allow: s.Allow, BaseURL: s.BaseURL,
		Buckets: s.Buckets, Bucket: s.Bucket, Prefix: s.Prefix,
		Host: s.Host, Port: s.Port,
	}
}

// s3Config maps an s3 source definition; the s3 cache store reuses it
// (XI_CACHE_RESULT_S3_SOURCE).
func s3Config(name string, s *config.Source) *s3src.Config {
	return &s3src.Config{
		Name: name, Endpoint: s.Endpoint, Region: s.Region, ForcePathStyle: s.ForcePathStyle,
		AccessKeyID: s.AccessKeyID, SecretAccessKey: s.SecretAccessKey.Reveal(), RoleARN: s.RoleARN,
	}
}

func newSource(ctx context.Context, name string, s *config.Source, cfg *config.Config, client *http.Client, userAgent string) (source.Source, error) {
	switch s.Type {
	case config.SourceHTTP:
		h := http.Header{}
		for k, v := range s.Headers {
			h.Set(k, v.Reveal())
		}
		return httpsrc.New(httpsrc.Config{Name: name, Client: client, Timeout: cfg.Fetch.Timeout, Headers: h})
	case config.SourceS3:
		return s3src.New(ctx, s3Config(name, s))
	case config.SourceGCS:
		return gcssrc.New(ctx, &gcssrc.Config{
			Name: name, CredentialsJSON: []byte(s.CredentialsJSON.Reveal()), Endpoint: s.Endpoint, UserAgent: userAgent,
		})
	case config.SourceAzure:
		return azuresrc.New(ctx, azuresrc.Config{
			Name: name, Account: s.Account, AccountKey: s.AccountKey.Reveal(),
			ConnectionString: s.ConnectionString.Reveal(), SASToken: s.SASToken.Reveal(),
			Endpoint: s.Endpoint, Prefix: s.Prefix,
		})
	case config.SourceFS:
		return fssrc.New(fssrc.Config{Name: name, Root: s.Root, FollowSymlinks: s.FollowSymlinks})
	case config.SourceFTP:
		return ftpsrc.New(&ftpsrc.Config{
			Name: name, Host: s.Host, Port: s.Port, User: s.User, Password: s.Password.Reveal(), TLS: s.TLS,
			Root: s.Root, PoolSize: s.PoolSize, DialTimeout: s.DialTimeout, IdleTimeout: s.IdleTimeout,
		})
	case config.SourceSFTP:
		return sftpsrc.New(&sftpsrc.Config{
			Name: name, Host: s.Host, Port: s.Port, User: s.User, Password: s.Password.Reveal(),
			PrivateKey: s.PrivateKey.Reveal(), PrivateKeyPassphrase: s.PrivateKeyPassphrase.Reveal(),
			HostKey: s.HostKey, Root: s.Root, PoolSize: s.PoolSize, DialTimeout: s.DialTimeout, IdleTimeout: s.IdleTimeout,
		})
	default:
		return nil, fmt.Errorf("unknown source type %q", s.Type)
	}
}

// serverConfig maps config.Config onto the server's own options.
func serverConfig(cfg *config.Config) server.Config {
	fmts := func(in []config.Format) []transform.Format {
		out := make([]transform.Format, 0, len(in))
		for _, f := range in {
			if tf, err := imgproxy.ParseFormat("format", string(f)); err == nil {
				out = append(out, tf)
			}
		}
		return out
	}
	return server.Config{
		Addr:                 cfg.Server.Addr,
		PathPrefix:           cfg.Server.PathPrefix,
		RequestTimeout:       cfg.Server.RequestTimeout,
		ReadHeaderTimeout:    cfg.Server.ReadHeaderTimeout,
		ReadTimeout:          cfg.Server.ReadTimeout,
		WriteTimeout:         cfg.Server.WriteTimeout,
		IdleTimeout:          cfg.Server.IdleTimeout,
		MaxHeaderBytes:       int(cfg.Server.MaxHeaderBytes),
		CompatImgproxyStatus: cfg.Compat.ImgproxyStatus,
		Defaults: transform.Defaults{
			AutoRotate:        cfg.Processing.AutoRotate,
			StripMetadata:     cfg.Processing.StripMetadata,
			KeepCopyright:     cfg.Processing.KeepCopyright,
			StripColorProfile: cfg.Processing.StripColorProfile,
			PreserveHDR:       cfg.Processing.PreserveHDR,
		},
		Negotiate: negotiate.Config{
			AutoAVIF: cfg.Formats.Auto.AVIF, AutoWebP: cfg.Formats.Auto.WebP, AutoJXL: cfg.Formats.Auto.JXL,
			EnforceAVIF: cfg.Formats.Enforce.AVIF, EnforceWebP: cfg.Formats.Enforce.WebP, EnforceJXL: cfg.Formats.Enforce.JXL,
			StrictAccept:         cfg.Formats.StrictAccept,
			Preferred:            fmts(cfg.Formats.Preferred),
			SkipProcessing:       fmts(cfg.Formats.SkipProcessing),
			SVGPassthrough:       cfg.Formats.SVGPassthrough,
			AVIFMaxPixels:        cfg.Formats.AVIF.MaxPixels,
			JXLMaxPixels:         negotiate.DefaultConfig().JXLMaxPixels,
			EnableClientHints:    cfg.ClientHints.Enabled,
			SaveDataQualityDelta: cfg.Formats.SaveDataQualityDelta,
		},
		ClientHints: server.ClientHintHeaders{CriticalCH: cfg.ClientHints.CriticalCH},
		Limits: server.Limits{
			MaxSrcBytes:             int64(cfg.Limits.MaxSrcBytes),
			MaxSrcPixels:            cfg.Limits.MaxSrcPixels,
			MaxAnimationFrames:      cfg.Limits.MaxAnimationFrames,
			MaxAnimationFramePixels: cfg.Limits.MaxAnimationFramePixels,
			MaxResultDimension:      cfg.Limits.MaxResultDimension,
			AllowRaising:            cfg.Security.AllowRaisingLimits,
		},
		SVG: svg.Options{
			MaxBytes:    int64(cfg.Limits.MaxSVGBytes),
			MaxDepth:    cfg.Limits.MaxSVGDepth,
			MaxElements: cfg.Limits.MaxSVGElements,
		},
		ETagBuster: cfg.Cache.ETagBuster.Reveal(),
		Cache: server.CacheHeaders{
			TTL:         cfg.Cache.Headers.TTL,
			ErrorTTL:    cfg.Cache.Headers.ErrorTTL,
			Passthrough: cfg.Cache.Headers.Passthrough,
		},
		CacheTagsHeader: cfg.Cache.TagsHeader,
		DebugHeaders:    cfg.Observability.DebugHeaders,
		Fallback: server.Fallback{
			Source:          cfg.Fallback.Source,
			Status:          cfg.Fallback.Status,
			TTL:             cfg.Fallback.TTL,
			AllowRequestURL: cfg.Fallback.AllowRequestURL,
		},
		Info: server.InfoConfig{
			CacheTTL:     cfg.Cache.Headers.TTL,
			DebugHeaders: cfg.Observability.DebugHeaders,
			PlanLimits:   planLimits(cfg),
		},
		ResultCache: server.ResultCacheConfig{
			TTL:         cfg.Cache.Result.TTL,
			AllowBypass: cfg.Cache.AllowBypass,
		},
	}
}
