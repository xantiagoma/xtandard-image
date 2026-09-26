package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/detect"
	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/awssih"
	"github.com/xantiagoma/xtandard-image/internal/dialect/bunny"
	"github.com/xantiagoma/xtandard-image/internal/dialect/cloudflare"
	"github.com/xantiagoma/xtandard-image/internal/dialect/cloudinary"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imagekit"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgix"
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

// buildRouter builds the signing verifiers, presets, the dialects
// (imgproxy, query, imgix, imagekit, cloudflare, cloudflare_images, wsrv,
// thumbor, bunny, awssih, cloudinary and the simple family) and the router.
// warnings are configuration notes logged at startup.
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
		p, warn, err := buildAWSSIH(cfg)
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
	if err := addImgix(cfg, required, dialects, opts); err != nil {
		return nil, nil, err
	}
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
	if err := addCloudinary(cfg, ipOpts.Presets, required, dialects, opts); err != nil {
		return nil, nil, err
	}
	if err := addSimpleDialects(cfg, qOpts.XIVerifier, dialects, opts); err != nil {
		return nil, nil, err
	}
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
		if mo := primaryMount(cfg, cfg.Dialects.Default); mo != nil {
			// Unmatched requests resolve like the dialect's primary mount:
			// its bound source (imgix object keys, ...) and unsigned policy.
			opts.Default.Source, opts.Default.Policy = mo.Source, router.Policy(mo.UnsignedPolicy)
		}
	} else if cfg.Dialects.Default != "" {
		warnings = append(warnings, fmt.Sprintf("default dialect %q is not implemented yet; unmatched requests answer 404", cfg.Dialects.Default))
	}
	slices.Sort(warnings)

	rt, err = router.New(opts, dialects)
	if err != nil {
		return nil, nil, err
	}
	return rt, warnings, nil
}

// primaryMount is the primary mount configuration of a dialect whose
// primary parser is registered under its plain name; nil for the others
// (imgproxy's own mount is "/", the simple family has no single mount).
func primaryMount(cfg *config.Config, name config.DialectName) *config.MountOptions {
	d := &cfg.Dialects
	switch name {
	case config.DialectImgix:
		return &d.Imgix.MountOptions
	case config.DialectImagekit:
		return &d.Imagekit.MountOptions
	case config.DialectCloudinary:
		return &d.Cloudinary.MountOptions
	case config.DialectBunny:
		return &d.Bunny.MountOptions
	case config.DialectThumbor:
		return &d.Thumbor.MountOptions
	case config.DialectAWSSIH:
		return &d.AWSSIH.MountOptions
	case config.DialectCloudflareImages:
		return &d.CloudflareImages.MountOptions
	default:
		return nil
	}
}

// addImgix registers one imgix parser per enabled mount (an imgix
// "source"), each verifying s with that mount's secure URL tokens
// (XI_DIALECTS_IMGIX_KEYS, raw strings; several allow rotation), and its
// router mount. Mount i > 0 is keyed "imgix[i]". A mount without tokens
// does not verify s: every URL is unsigned and follows the mount's
// unsigned policy.
func addImgix(cfg *config.Config, required bool, dialects map[string]dialect.Dialect, opts *router.Options) error {
	m := &cfg.Dialects.Imgix
	if !m.Enabled {
		return nil
	}
	for i, mo := range append([]config.MountOptions{m.MountOptions}, m.Mounts...) {
		key := imgix.Name
		if i > 0 {
			key = fmt.Sprintf("%s[%d]", imgix.Name, i)
		}
		o := imgix.Options{Strict: cfg.Dialects.Strict}
		if len(mo.Keys) > 0 {
			keys := make([]sign.Key, 0, len(mo.Keys))
			for _, k := range mo.Keys {
				keys = append(keys, sign.NewKey([]byte(k.Reveal()), nil))
			}
			v, err := sign.NewImgixVerifier(sign.ImgixOptions{Keys: sign.NewKeyring(keys...), Required: required})
			if err != nil {
				return fmt.Errorf("%s verifier: %w", key, err)
			}
			o.Verifier = v
		}
		p, err := imgix.New(&o)
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

// addImageKit registers one ImageKit parser per enabled mount, each
// verifying ik-s with that mount's private keys (XI_DIALECTS_IMAGEKIT_KEYS,
// raw strings) and stripping XI_DIALECTS_IMAGEKIT_ENDPOINT_PATH, and its
// router mount. Mount i > 0 is keyed "imagekit[i]".
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
		o := imagekit.Options{Strict: cfg.Dialects.Strict, Presets: presets, EndpointPath: m.EndpointPath}
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
// primary mount's keys apply to every SIH mount. Thumbor forwarding
// (XI_DIALECTS_AWSSIH_THUMBOR) parses with a keyless thumbor dialect (WP-45)
// that accepts the "/unsafe/" paths SIH forwards.
func buildAWSSIH(cfg *config.Config) (*awssih.Parser, []string, error) {
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
		// A keyless thumbor parser of its own: the registered "thumbor"
		// dialect verifies with XI_DIALECTS_THUMBOR_KEYS and, when signing
		// is required, rejects the "/unsafe/" paths SIH forwards (SIH
		// signs with its own parameter, checked before forwarding).
		th, err := newThumbor(nil, cfg.Dialects.Strict, false)
		if err != nil {
			return nil, nil, err
		}
		opts.Thumbor = th
	}
	p, err := awssih.New(&opts)
	if err != nil {
		return nil, nil, fmt.Errorf("awssih dialect: %w", err)
	}
	return p, warnings, nil
}

// addCloudinary registers one Cloudinary parser per enabled mount: each
// mount's keys are its api secrets (dialects.cloudinary.keys), so mounts
// cannot share a parser. The dialect key is the mount label.
func addCloudinary(cfg *config.Config, presets *preset.Store, required bool, dialects map[string]dialect.Dialect, opts *router.Options) error {
	c := &cfg.Dialects.Cloudinary
	if !c.Enabled {
		return nil
	}
	for i, mo := range append([]config.MountOptions{c.MountOptions}, c.Mounts...) {
		label := cloudinary.Name
		if i > 0 {
			label = fmt.Sprintf("%s[%d]", cloudinary.Name, i)
		}
		o := cloudinary.Options{
			Clouds:             c.Clouds,
			Strict:             cfg.Dialects.Strict,
			Presets:            presets,
			ArgumentsSeparator: cfg.Dialects.Imgproxy.ArgumentsSeparator,
		}
		if len(mo.Keys) > 0 {
			keys := make([]sign.Key, 0, len(mo.Keys))
			for _, k := range mo.Keys {
				keys = append(keys, sign.NewKey([]byte(k.Reveal()), nil))
			}
			v, err := sign.NewCloudinaryVerifier(sign.CloudinaryOptions{Keys: sign.NewKeyring(keys...), Required: required})
			if err != nil {
				return fmt.Errorf("%s verifier: %w", label, err)
			}
			o.Verifier = v
		}
		p, err := cloudinary.New(&o)
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
			MaxVideoCompositeFrames: cfg.Video.MaxAnimationFrames,
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

// buildDetector loads face and object detection (internal/detect) as
// XI_DETECT_* configure it. Nothing is loaded before this call: ONNX
// Runtime is dlopen'ed here, never at init. With enabled=auto a missing
// library or model (the slim image) is logged and detection stays off
// (nil, which internal/detect treats as unavailable); with true it fails
// startup; with false nothing is loaded.
func buildDetector(ctx context.Context, cfg *config.Config, obs detect.Observer, logger *slog.Logger) (*detect.Detector, error) {
	dc := cfg.Detect
	if dc.Enabled == config.False {
		logger.LogAttrs(ctx, slog.LevelInfo, "detection disabled")
		return nil, nil
	}
	d, err := detect.New(&detect.Options{
		Library:         dc.Library,
		ModelsDir:       dc.ModelsDir,
		FaceModel:       dc.FaceModel,
		ObjectModel:     dc.ObjectModel,
		VerifyChecksums: dc.VerifyChecksums,
		Threads:         dc.Threads,
		Workers:         dc.Workers,
		MaxInput:        dc.MaxInput,
		Observer:        obs,
		Logger:          logger,
	})
	switch {
	case errors.Is(err, detect.ErrUnavailable) && dc.Enabled == config.Auto:
		logger.LogAttrs(ctx, slog.LevelInfo, "detection unavailable", slog.String("reason", err.Error()))
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("detection (XI_DETECT_*): %w", err)
	}
	attrs := []slog.Attr{slog.String("onnxruntime", d.Version())}
	for _, m := range d.Models() {
		attrs = append(attrs, slog.Group(m.Kind+"_model",
			slog.String("path", m.Path), slog.String("input", m.Input), slog.String("checksum", m.Checksum)))
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "detection enabled", attrs...)
	return d, nil
}
