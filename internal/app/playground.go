package app

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/apidoc"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/dialect/router"
	"github.com/xantiagoma/xtandard-image/internal/playground"
	"github.com/xantiagoma/xtandard-image/internal/playground/translate"
	"github.com/xantiagoma/xtandard-image/internal/preset"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// playgroundHandlers are the playground handlers of both listeners; nil
// where it is not served.
type playgroundHandlers struct {
	admin  http.Handler
	public http.Handler
	// publicPath is the playground path on the public listener
	// (XI_SERVER_PATH_PREFIX + XI_PLAYGROUND_PATH), reserved even when
	// the playground is not served there.
	publicPath string
}

// buildPlayground builds the playground (WP-80) when XI_PLAYGROUND_ENABLED:
// its metadata from the router's mounts and the apidoc catalogues, one
// signer per signable mount, and image, the public handler api/render
// runs.
func buildPlayground(ctx context.Context, cfg *config.Config, rt *router.Router, image http.Handler, version string, logger *slog.Logger, env *playgroundEnv) (playgroundHandlers, error) {
	pc := &cfg.Playground
	var publicPath string
	if pc.Path != "" {
		publicPath = strings.TrimSuffix(cfg.Server.PathPrefix, "/") + pc.Path
	}
	if !pc.Enabled {
		return playgroundHandlers{publicPath: publicPath}, nil
	}
	meta, signers, err := playgroundMeta(cfg, rt, version)
	if err != nil {
		return playgroundHandlers{}, fmt.Errorf("playground: %w", err)
	}
	if err := env.extend(meta, cfg); err != nil {
		return playgroundHandlers{}, fmt.Errorf("playground: %w", err)
	}
	presets, err := presetStore(cfg)
	if err != nil {
		return playgroundHandlers{}, fmt.Errorf("playground: %w", err)
	}
	tr, err := translate.New(translate.Options{Mounts: meta.Mounts, Separator: cfg.Dialects.Imgproxy.ArgumentsSeparator, Presets: presets})
	if err != nil {
		return playgroundHandlers{}, fmt.Errorf("playground: %w", err)
	}
	deps := &playground.Deps{
		Meta: meta, Signers: signers, Image: image, Logger: logger,
		Store: env.store, FSRoots: fsRoots(cfg), Translator: tr,
	}
	out := playgroundHandlers{publicPath: publicPath}
	if cfg.Admin.Addr != "" {
		if out.admin, err = playground.New(playground.Config{Path: pc.Path}, deps); err != nil {
			return playgroundHandlers{}, err
		}
	}
	if pc.Public {
		if out.public, err = playground.New(playground.Config{Path: out.publicPath}, deps); err != nil {
			return playgroundHandlers{}, err
		}
		if len(signers) > 0 {
			logger.LogAttrs(ctx, slog.LevelWarn, "playground is public and signs urls for anyone",
				slog.String("path", out.publicPath))
		}
	}
	return out, nil
}

// playgroundEnv is what the playground learns from the rest of the app.
type playgroundEnv struct {
	registry  *source.Registry
	store     *playground.Store
	detection bool // the detector is loaded
	video     bool // video sources decode
}

// playgroundStore is the playground:// source (WP-94): the built-in
// samples and, with XI_PLAYGROUND_UPLOADS, uploads. nil without the
// playground.
func playgroundStore(cfg *config.Config) *playground.Store {
	if !cfg.Playground.Enabled {
		return nil
	}
	return playground.NewStore(playground.StoreOptions{
		Uploads: cfg.Playground.Uploads.Bool(), MaxBytes: int64(cfg.Limits.MaxSrcBytes),
	})
}

// extend adds the samples, the gallery, the upload settings, the
// detection and video capabilities and the listable sources to meta.
func (e *playgroundEnv) extend(meta *playground.Meta, cfg *config.Config) error {
	allowed := func(u string) bool {
		if e.registry == nil {
			return false
		}
		_, err := e.registry.Resolve(transform.SourceSpec{Raw: u})
		return err == nil
	}
	gallery, err := playground.Gallery(allowed)
	if err != nil {
		return err
	}
	meta.Gallery = gallery
	meta.Samples = []playground.Sample{}
	if e.store != nil {
		meta.Samples = playground.Samples()
		meta.Uploads = e.store.UploadsEnabled()
		meta.MaxUploadBytes = e.store.MaxBytes()
	}
	meta.Detection, meta.Video = e.detection, e.video
	meta.Classes = append([]string{"face"}, transform.ObjectClasses()...)
	for i := range meta.Sources {
		meta.Sources[i].Listable = cfg.Sources[meta.Sources[i].Name].Type == config.SourceFS
	}
	return nil
}

// fsRoots are the fs sources api/sources/<name>/list browses.
func fsRoots(cfg *config.Config) map[string]playground.FSRoot {
	out := map[string]playground.FSRoot{}
	for name := range cfg.Sources {
		if s := cfg.Sources[name]; s.Type == config.SourceFS {
			prefix := strings.Trim(s.Prefix, "/")
			if prefix != "" {
				prefix += "/"
			}
			out[name] = playground.FSRoot{Root: s.Root, Prefix: prefix}
		}
	}
	return out
}

// withPlayground reserves path on the public listener: requests under it
// go to pg (XI_PLAYGROUND_PUBLIC) or get a 404, so the playground path never
// reaches a dialect; everything else goes to next. It is not a ServeMux, so
// image paths are never cleaned or redirected.
func withPlayground(next, pg http.Handler, path string) http.Handler {
	if path == "" || path == "/" {
		return next
	}
	if pg == nil {
		pg = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := r.URL.Path; p == path || strings.HasPrefix(p, path+"/") {
			pg.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// playgroundMeta describes the enabled mounts without key material and
// returns a signer for each mount the server holds a key for.
func playgroundMeta(cfg *config.Config, rt *router.Router, version string) (*playground.Meta, map[string]*sign.Signer, error) {
	ring := signingRing(cfg)
	mode := playground.SigningOff
	switch {
	case ring != nil && cfg.Signing.Required.Bool():
		mode = playground.SigningRequired
	case ring != nil:
		mode = playground.SigningOptional
	}
	var imgproxySigner, xiSigner *sign.Signer
	if ring != nil {
		ip, err := sign.NewImgproxyScheme(cfg.Signing.Size)
		if err != nil {
			return nil, nil, err
		}
		xi, err := sign.NewXIScheme(cfg.Signing.Size)
		if err != nil {
			return nil, nil, err
		}
		if imgproxySigner, err = sign.NewSigner(ip, ring); err != nil {
			return nil, nil, err
		}
		if xiSigner, err = sign.NewSigner(xi, ring); err != nil {
			return nil, nil, err
		}
	}
	sep := cfg.Dialects.Imgproxy.ArgumentsSeparator
	if sep == "" {
		sep = ":"
	}

	meta := &playground.Meta{
		Version:    version,
		PathPrefix: cfg.Server.PathPrefix,
		PublicAddr: cfg.Server.Addr,
		Public:     cfg.Playground.Public,
		Signing:    playground.Signing{Mode: mode, UnsignedPolicy: string(cfg.Security.UnsignedPolicy)},
		Mounts:     []playground.Mount{},
		Catalogues: apidoc.Catalogues(),
		Sources:    []playground.Source{},
		Formats:    outputFormats(cfg),
	}
	signers := map[string]*sign.Signer{}
	mounts := rt.Mounts()
	for i := range mounts {
		rm := &mounts[i]
		if rm.Dialect == infoDialect {
			continue
		}
		m := playground.Mount{
			ID: rm.Name, Dialect: rm.Dialect, Prefix: rm.Prefix, Hosts: rm.Hosts, Source: rm.Source,
			UnsignedPolicy: string(rm.Policy), Scheme: playground.SchemeNone, Grammar: playground.GrammarPath,
			Example: "/{src}",
		}
		var s *sign.Signer
		switch base, _, _ := strings.Cut(rm.Dialect, "["); base {
		case "imgproxy":
			m.Scheme, m.Grammar, m.ArgumentsSeparator, m.Example, s = playground.SchemeImgproxy, playground.GrammarImgproxy, sep, "", imgproxySigner
		case "query":
			m.Scheme, m.Grammar, m.Example, s = playground.SchemeXI, playground.GrammarQuery, "", xiSigner
		case "wsrv":
			m.Scheme, m.Example, s = playground.SchemeXI, "/?url={src}&w=300", xiSigner
		case "cloudflare":
			m.Scheme, m.Example, s = playground.SchemeXI, "/width=300,fit=cover/{src}", xiSigner
		case "thumbor":
			m.Scheme, m.Example = playground.SchemeThumbor, "/300x200/smart/{src}"
			var err error
			if s, err = thumborSigner(cfg, rm.Dialect); err != nil {
				return nil, nil, err
			}
		case "imgix":
			m.Scheme, m.Grammar, m.Example = playground.SchemeImgix, playground.GrammarImgix, ""
			var err error
			if s, err = imgixSigner(cfg, rm.Dialect); err != nil {
				return nil, nil, err
			}
		case "imagekit":
			m.Grammar, m.Example = playground.GrammarImagekit, "/tr:w-300,h-200/{src}"
		case "cloudinary":
			m.Grammar, m.Example = playground.GrammarCloudinary, "/image/upload/w_300,c_fill/{src}"
		case "bunny":
			m.Grammar, m.Example = playground.GrammarBunny, "/{src}?width=300"
		case "awssih":
			m.Grammar = playground.GrammarAWSSIH
		}
		switch base, _, _ := strings.Cut(rm.Dialect, "["); base {
		case "wsrv":
			m.Grammar = playground.GrammarWsrv
		case "cloudflare":
			m.Grammar = playground.GrammarCloudflare
		case "thumbor":
			m.Grammar = playground.GrammarThumbor
		}
		if s != nil {
			m.Keyed = true
			signers[m.ID] = s
		}
		meta.Mounts = append(meta.Mounts, m)
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Sources)) {
		meta.Sources = append(meta.Sources, playground.Source{Name: name, Type: string(cfg.Sources[name].Type)})
	}
	names, err := presetNames(cfg)
	if err != nil {
		return nil, nil, err
	}
	meta.Presets = names
	return meta, signers, nil
}

// signingRing is the XI_SIGNING_KEYS keyring (nil without keys), built as
// buildRouter builds it.
func signingRing(cfg *config.Config) *sign.Keyring {
	if len(cfg.Signing.Keys) == 0 {
		return nil
	}
	keys := make([]sign.Key, 0, len(cfg.Signing.Keys))
	for i, k := range cfg.Signing.Keys {
		var salt []byte
		if i < len(cfg.Signing.Salts) {
			salt = cfg.Signing.Salts[i]
		}
		keys = append(keys, sign.NewKey(k, salt))
	}
	return sign.NewKeyring(keys...)
}

// thumborSigner signs for thumbor mount dialect ("thumbor" or
// "thumbor[i]") with that mount's keys, falling back to the primary
// mount's, as addThumborMounts verifies. nil without keys.
func thumborSigner(cfg *config.Config, dialect string) (*sign.Signer, error) {
	m := &cfg.Dialects.Thumbor
	keys := m.Keys
	if idx, ok := strings.CutPrefix(dialect, "thumbor["); ok {
		i, err := strconv.Atoi(strings.TrimSuffix(idx, "]"))
		if err != nil || i < 1 || i > len(m.Mounts) {
			return nil, fmt.Errorf("thumbor mount %q", dialect)
		}
		if len(m.Mounts[i-1].Keys) > 0 {
			keys = m.Mounts[i-1].Keys
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}
	ks := make([]sign.Key, 0, len(keys))
	for _, k := range keys {
		ks = append(ks, sign.NewKey([]byte(k.Reveal()), nil))
	}
	return sign.NewSigner(sign.NewThumborScheme(), sign.NewKeyring(ks...))
}

// imgixSigner signs for imgix mount dialect ("imgix" or "imgix[i]") with
// the first of that mount's tokens, as addImgix verifies. nil without
// tokens.
func imgixSigner(cfg *config.Config, dialect string) (*sign.Signer, error) {
	m := &cfg.Dialects.Imgix
	keys := m.Keys
	if idx, ok := strings.CutPrefix(dialect, "imgix["); ok {
		i, err := strconv.Atoi(strings.TrimSuffix(idx, "]"))
		if err != nil || i < 1 || i > len(m.Mounts) {
			return nil, fmt.Errorf("imgix mount %q", dialect)
		}
		keys = m.Mounts[i-1].Keys
	}
	if len(keys) == 0 {
		return nil, nil
	}
	ks := make([]sign.Key, 0, len(keys))
	for _, k := range keys {
		ks = append(ks, sign.NewKey([]byte(k.Reveal()), nil))
	}
	return sign.NewSigner(sign.NewImgixScheme(), sign.NewKeyring(ks...))
}

// presetNames lists the configured presets (XI_PRESETS and
// XI_PRESETS_FILE), never nil.
func presetNames(cfg *config.Config) ([]string, error) {
	store, err := presetStore(cfg)
	if err != nil || store == nil {
		return []string{}, err
	}
	if names := store.Names(); names != nil {
		return names, nil
	}
	return []string{}, nil
}

// presetStore loads the presets as buildRouter does; nil without presets.
func presetStore(cfg *config.Config) (*preset.Store, error) {
	if len(cfg.Presets) == 0 && cfg.PresetsFile == "" {
		return nil, nil
	}
	store, err := preset.New(preset.Options{
		Definitions: cfg.Presets,
		File:        cfg.PresetsFile,
		Separator:   cfg.Dialects.Imgproxy.ArgumentsSeparator,
		Only:        cfg.PresetsOnly,
	})
	if err != nil {
		return nil, fmt.Errorf("presets: %w", err)
	}
	return store, nil
}

// outputFormats are the formats the server encodes (FORMATS.md); HEIC only
// with XI_FORMATS_HEIC_ENCODE.
func outputFormats(cfg *config.Config) []string {
	fs := []string{"jpeg", "png", "webp", "avif", "jxl", "gif", "tiff", "bmp", "ico", "svg"}
	if cfg.Formats.HEIC.Encode {
		fs = append(fs, "heic")
	}
	return append(fs, "best")
}
