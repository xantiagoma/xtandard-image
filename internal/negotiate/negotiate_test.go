package negotiate

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

const (
	acceptChrome = "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"
	acceptWebP   = "image/webp,image/apng,image/*,*/*;q=0.8"
	acceptJXL    = "image/jxl,image/*,*/*;q=0.8"
	acceptLegacy = "image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5"
	acceptNoSVG  = "image/avif,image/webp,image/png,image/jpeg"
)

// allAuto enables every auto flag so each §6.2 column is reachable.
func allAuto() Config {
	c := DefaultConfig()
	c.AutoJXL = true
	return c
}

// result is the comparable projection of a Decision.
type result struct {
	Format      transform.Format
	Lossless    bool
	Quantize    bool
	Passthrough bool
	Vary        VarySet
	Reason      Reason
	Candidates  []transform.Format
}

func project(d *Decision) result {
	return result{
		Format: d.Format, Lossless: d.Lossless, Quantize: d.Quantize, Passthrough: d.Passthrough,
		Vary: d.Vary, Reason: d.Reason, Candidates: d.Candidates(),
	}
}

var (
	photoOpaque   = SourceMeta{Format: transform.FormatJPEG, Class: ContentPhoto}
	photoAlpha    = SourceMeta{Format: transform.FormatPNG, Class: ContentPhoto, HasAlpha: true}
	graphicOpaque = SourceMeta{Format: transform.FormatPNG, Class: ContentGraphic, Colors: 12}
	graphicAlpha  = SourceMeta{Format: transform.FormatPNG, Class: ContentGraphic, HasAlpha: true, Colors: 4000}
	animated      = SourceMeta{Format: transform.FormatGIF, IsAnimated: true, FrameCount: 10}
	svgSource     = SourceMeta{Format: transform.FormatSVG, Class: ContentGraphic}
)

func auto() *transform.Request { return &transform.Request{Pipelines: []transform.Pipeline{{}}} }

func explicit(f transform.Format) *transform.Request {
	r := auto()
	r.Output.Format = f
	return r
}

func withCfg(f func(*Config)) Config {
	c := allAuto()
	f(&c)
	return c
}

func accepted(f transform.Format, lossless bool) result {
	return result{Format: f, Lossless: lossless, Vary: VaryAccept, Reason: ReasonAccept}
}

func fallback(f transform.Format, lossless bool) result {
	return result{Format: f, Lossless: lossless, Vary: VaryAccept, Reason: ReasonFallback}
}

func TestDecide(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		req    *transform.Request
		accept string
		cfg    Config
		meta   SourceMeta
		want   result
	}{
		// FORMATS §6.2 row "Still photo, opaque".
		{"row=photo_opaque/col=avif", auto(), acceptChrome, allAuto(), photoOpaque, accepted(transform.FormatAVIF, false)},
		{"row=photo_opaque/col=webp", auto(), acceptWebP, allAuto(), photoOpaque, accepted(transform.FormatWebP, false)},
		{"row=photo_opaque/col=jxl", auto(), acceptJXL, allAuto(), photoOpaque, accepted(transform.FormatJXL, false)},
		{"row=photo_opaque/col=else", auto(), acceptLegacy, allAuto(), photoOpaque, fallback(transform.FormatJPEG, false)},
		{"row=photo_opaque/col=jxl_disabled", auto(), acceptJXL, DefaultConfig(), photoOpaque, fallback(transform.FormatJPEG, false)},
		{"row=photo_opaque/class_unknown_is_photo", auto(), acceptChrome, allAuto(), SourceMeta{Format: transform.FormatJPEG}, accepted(transform.FormatAVIF, false)},

		// Row "Still photo, alpha".
		{"row=photo_alpha/col=avif", auto(), acceptChrome, allAuto(), photoAlpha, accepted(transform.FormatAVIF, false)},
		{"row=photo_alpha/col=webp", auto(), acceptWebP, allAuto(), photoAlpha, accepted(transform.FormatWebP, false)},
		{"row=photo_alpha/col=jxl", auto(), acceptJXL, allAuto(), photoAlpha, accepted(transform.FormatJXL, false)},
		{"row=photo_alpha/col=else", auto(), acceptLegacy, allAuto(), photoAlpha, fallback(transform.FormatPNG, false)},

		// Row "Graphic / flat colours, opaque or alpha".
		{"row=graphic_opaque/col=avif", auto(), acceptChrome, allAuto(), graphicOpaque, accepted(transform.FormatWebP, true)},
		{"row=graphic_opaque/col=webp", auto(), acceptWebP, allAuto(), graphicOpaque, accepted(transform.FormatWebP, true)},
		{"row=graphic_opaque/col=jxl", auto(), acceptJXL, allAuto(), graphicOpaque, accepted(transform.FormatJXL, true)},
		{
			"row=graphic_opaque/col=else_quantised", auto(), acceptLegacy, allAuto(), graphicOpaque,
			result{Format: transform.FormatPNG, Lossless: true, Quantize: true, Vary: VaryAccept, Reason: ReasonFallback},
		},
		{"row=graphic_alpha/col=avif", auto(), acceptChrome, allAuto(), graphicAlpha, accepted(transform.FormatWebP, true)},
		{"row=graphic_alpha/col=webp", auto(), acceptWebP, allAuto(), graphicAlpha, accepted(transform.FormatWebP, true)},
		{"row=graphic_alpha/col=jxl", auto(), acceptJXL, allAuto(), graphicAlpha, accepted(transform.FormatJXL, true)},
		{"row=graphic_alpha/col=else_many_colours", auto(), acceptLegacy, allAuto(), graphicAlpha, fallback(transform.FormatPNG, true)},
		{"row=graphic/avif_only_accept_falls_back", auto(), "image/avif,*/*", allAuto(), graphicOpaque, result{Format: transform.FormatPNG, Lossless: true, Quantize: true, Vary: VaryAccept}},

		// Row "Animated".
		{"row=animated/col=avif", auto(), acceptChrome, allAuto(), animated, accepted(transform.FormatWebP, false)},
		{"row=animated/col=avif_sequences_supported", auto(), acceptChrome, withCfg(func(c *Config) { c.AVIFAnimation = true }), animated, accepted(transform.FormatAVIF, false)},
		{"row=animated/col=webp", auto(), acceptWebP, allAuto(), animated, accepted(transform.FormatWebP, false)},
		{"row=animated/col=jxl", auto(), acceptJXL, allAuto(), animated, fallback(transform.FormatGIF, false)},
		{"row=animated/col=else", auto(), acceptLegacy, allAuto(), animated, fallback(transform.FormatGIF, false)},
		{"row=animated/frame_count_only", auto(), acceptLegacy, allAuto(), SourceMeta{Format: transform.FormatWebP, FrameCount: 3}, fallback(transform.FormatGIF, false)},
		{
			"row=animated/disable_animation_is_still", &transform.Request{Pipelines: []transform.Pipeline{{DisableAnimation: true}}},
			acceptLegacy, allAuto(), animated, fallback(transform.FormatJPEG, false),
		},

		// Row "SVG source, no raster ops, SVG acceptable".
		{"row=svg/col=avif", auto(), acceptChrome, allAuto(), svgSource, result{Format: transform.FormatSVG, Passthrough: true, Vary: VaryAccept, Reason: ReasonSVGPassthrough}},
		{"row=svg/col=webp_via_wildcard", auto(), acceptWebP, allAuto(), svgSource, result{Format: transform.FormatSVG, Passthrough: true, Vary: VaryAccept, Reason: ReasonSVGPassthrough}},
		{"row=svg/col=jxl", auto(), acceptJXL, allAuto(), svgSource, result{Format: transform.FormatSVG, Passthrough: true, Vary: VaryAccept, Reason: ReasonSVGPassthrough}},
		{"row=svg/col=else", auto(), acceptLegacy, allAuto(), svgSource, result{Format: transform.FormatSVG, Passthrough: true, Vary: VaryAccept, Reason: ReasonSVGPassthrough}},
		{"row=svg/no_accept_header", auto(), "", allAuto(), svgSource, result{Format: transform.FormatSVG, Passthrough: true, Vary: VaryAccept, Reason: ReasonSVGPassthrough}},
		{"row=svg/not_acceptable_rasterises", auto(), acceptNoSVG, allAuto(), svgSource, accepted(transform.FormatWebP, true)},
		{"row=svg/raster_ops_rasterise", auto(), acceptChrome, allAuto(), SourceMeta{Format: transform.FormatSVG, NeedsRaster: true, Class: ContentGraphic}, accepted(transform.FormatWebP, true)},
		{"row=svg/passthrough_disabled", auto(), acceptLegacy, withCfg(func(c *Config) { c.SVGPassthrough = false }), svgSource, fallback(transform.FormatPNG, true)},

		// Row "Source format not encodable".
		{"row=unencodable/pdf/col=avif", auto(), acceptChrome, allAuto(), SourceMeta{Format: transform.FormatUnset}, accepted(transform.FormatAVIF, false)},
		{"row=unencodable/pdf/col=else_opaque", auto(), acceptLegacy, allAuto(), SourceMeta{Format: transform.FormatUnset}, fallback(transform.FormatJPEG, false)},
		{"row=unencodable/psd/col=else_alpha", auto(), acceptLegacy, allAuto(), SourceMeta{HasAlpha: true}, fallback(transform.FormatPNG, false)},
		{"row=unencodable/tiff/col=else", auto(), acceptLegacy, allAuto(), SourceMeta{Format: transform.FormatTIFF}, fallback(transform.FormatJPEG, false)},
		{"row=unencodable/bmp_alpha/col=else", auto(), acceptLegacy, allAuto(), SourceMeta{Format: transform.FormatBMP, HasAlpha: true}, fallback(transform.FormatPNG, false)},
		{"row=unencodable/heic/col=webp", auto(), acceptWebP, allAuto(), SourceMeta{Format: transform.FormatHEIC}, accepted(transform.FormatWebP, false)},

		// §6.1.1: explicit format wins, never varies.
		{"explicit/jpeg_ignores_accept", explicit(transform.FormatJPEG), acceptChrome, allAuto(), photoOpaque, result{Format: transform.FormatJPEG, Reason: ReasonExplicit}},
		{"explicit/png_for_animated", explicit(transform.FormatPNG), acceptChrome, allAuto(), animated, result{Format: transform.FormatPNG, Reason: ReasonExplicit}},
		{
			"explicit/beats_enforce", explicit(transform.FormatJPEG), acceptChrome,
			withCfg(func(c *Config) { c.EnforceAVIF, c.EnforceWebP = true, true }), photoOpaque,
			result{Format: transform.FormatJPEG, Reason: ReasonExplicit},
		},
		{"explicit/svg_raster_source", explicit(transform.FormatSVG), acceptChrome, allAuto(), svgSource, result{Format: transform.FormatSVG, Reason: ReasonExplicit}},
		{"explicit/info_is_json", &transform.Request{Kind: transform.KindInfo}, acceptChrome, allAuto(), photoOpaque, result{Format: transform.FormatJSON, Reason: ReasonExplicit}},

		// Skip processing and raw.
		{
			"skip_processing/config", explicit(transform.FormatWebP), acceptChrome,
			withCfg(func(c *Config) { c.SkipProcessing = []transform.Format{transform.FormatGIF} }), animated,
			result{Format: transform.FormatGIF, Passthrough: true, Reason: ReasonSkipProcessing},
		},
		{
			"skip_processing/request", &transform.Request{Delivery: transform.Delivery{SkipProcessing: []transform.Format{transform.FormatJPEG}}},
			acceptChrome, allAuto(), photoOpaque,
			result{Format: transform.FormatJPEG, Passthrough: true, Reason: ReasonSkipProcessing},
		},
		{
			"skip_processing/other_format_negotiates", auto(), acceptChrome,
			withCfg(func(c *Config) { c.SkipProcessing = []transform.Format{transform.FormatSVG} }), photoOpaque,
			accepted(transform.FormatAVIF, false),
		},
		{"skip_processing/raw_kind", &transform.Request{Kind: transform.KindRaw}, acceptChrome, allAuto(), photoOpaque, result{Format: transform.FormatJPEG, Passthrough: true, Reason: ReasonSkipProcessing}},

		// Accept q-values and enforce flags.
		{"accept/q_orders_modern", auto(), "image/avif;q=0.5,image/webp;q=0.9,*/*;q=0.1", allAuto(), photoOpaque, accepted(transform.FormatWebP, false)},
		{"accept/q_tie_uses_matrix_order", auto(), "image/webp,image/jxl,image/avif", allAuto(), photoOpaque, accepted(transform.FormatAVIF, false)},
		{"accept/q0_excludes", auto(), "image/avif;q=0,image/webp", allAuto(), photoOpaque, accepted(transform.FormatWebP, false)},
		{"accept/wildcard_does_not_imply_modern", auto(), "*/*", allAuto(), photoOpaque, fallback(transform.FormatJPEG, false)},
		{"accept/image_wildcard_does_not_imply_modern", auto(), "image/*", allAuto(), photoOpaque, fallback(transform.FormatJPEG, false)},
		{"accept/case_insensitive", auto(), "IMAGE/WEBP", allAuto(), photoOpaque, accepted(transform.FormatWebP, false)},
		{"accept/params_do_not_match", auto(), "image/webp;foo=bar", allAuto(), photoOpaque, fallback(transform.FormatJPEG, false)},
		{"auto/avif_disabled", auto(), acceptChrome, withCfg(func(c *Config) { c.AutoAVIF = false }), photoOpaque, accepted(transform.FormatWebP, false)},
		{
			"enforce/beats_q", auto(), "image/avif;q=0.1,image/webp",
			withCfg(func(c *Config) { c.EnforceAVIF = true }), photoOpaque,
			result{Format: transform.FormatAVIF, Vary: VaryAccept, Reason: ReasonEnforced},
		},
		{
			"enforce/implies_auto", auto(), acceptChrome,
			Config{EnforceWebP: true, Preferred: DefaultConfig().Preferred},
			photoOpaque,
			result{Format: transform.FormatWebP, Vary: VaryAccept, Reason: ReasonEnforced},
		},
		{
			"enforce/needs_accept", auto(), acceptLegacy,
			withCfg(func(c *Config) { c.EnforceAVIF = true }), photoOpaque, fallback(transform.FormatJPEG, false),
		},
		{
			"enforce/avif_for_graphic_lossless", auto(), acceptChrome,
			withCfg(func(c *Config) { c.EnforceAVIF = true }), graphicAlpha,
			result{Format: transform.FormatAVIF, Lossless: true, Vary: VaryAccept, Reason: ReasonEnforced},
		},
		{
			"enforce/avif_not_for_animation", auto(), acceptChrome,
			withCfg(func(c *Config) { c.EnforceAVIF = true }), animated, accepted(transform.FormatWebP, false),
		},

		// Cost guards (§6.1.4).
		{"cost_guard/avif_over_limit", auto(), acceptChrome, allAuto(), SourceMeta{Format: transform.FormatJPEG, Pixels: 20_000_000}, accepted(transform.FormatWebP, false)},
		{"cost_guard/avif_at_limit", auto(), acceptChrome, allAuto(), SourceMeta{Format: transform.FormatJPEG, Pixels: 16_000_000}, accepted(transform.FormatAVIF, false)},
		{"cost_guard/jxl_over_limit", auto(), acceptJXL, allAuto(), SourceMeta{Format: transform.FormatJPEG, Pixels: 40_000_000}, result{Format: transform.FormatJPEG, Vary: VaryAccept}},

		// Preferred formats ordering.
		{
			"preferred/png_first", auto(), acceptLegacy,
			withCfg(func(c *Config) { c.Preferred = []transform.Format{transform.FormatPNG, transform.FormatJPEG} }), photoOpaque,
			fallback(transform.FormatPNG, false),
		},
		{
			"preferred/skips_incompatible_alpha", auto(), acceptLegacy,
			withCfg(func(c *Config) {
				c.Preferred = []transform.Format{transform.FormatJPEG, transform.FormatWebP, transform.FormatPNG}
			}), photoAlpha,
			fallback(transform.FormatWebP, false),
		},
		{
			"preferred/none_compatible_uses_hard_default", auto(), acceptLegacy,
			withCfg(func(c *Config) { c.Preferred = []transform.Format{transform.FormatJPEG} }), animated,
			fallback(transform.FormatGIF, false),
		},

		// No negotiation at all: no Vary.
		{
			"vary/none_without_auto_flags", auto(), acceptChrome,
			Config{Preferred: DefaultConfig().Preferred},
			photoOpaque,
			result{Format: transform.FormatJPEG, Reason: ReasonFallback},
		},

		// Strict Accept (406 cases are in TestDecideNotAcceptable).
		{
			"strict/skips_unacceptable_fallback", auto(), "image/png",
			withCfg(func(c *Config) { c.StrictAccept = true }), photoOpaque,
			fallback(transform.FormatPNG, false),
		},
		{
			"strict/varies_even_without_auto", auto(), "image/png",
			Config{StrictAccept: true, Preferred: DefaultConfig().Preferred},
			photoOpaque,
			fallback(transform.FormatPNG, false),
		},

		// best (M2 hook).
		{
			"best/candidates_matrix_order", explicit(transform.FormatBest), acceptChrome, allAuto(), photoOpaque,
			result{
				Format: transform.FormatAVIF, Vary: VaryAccept, Reason: ReasonBest,
				Candidates: []transform.Format{transform.FormatAVIF, transform.FormatWebP, transform.FormatJPEG},
			},
		},
		{
			"best/graphic", explicit(transform.FormatBest), acceptChrome, allAuto(), graphicOpaque,
			result{
				Format: transform.FormatWebP, Lossless: true, Vary: VaryAccept, Reason: ReasonBest,
				Candidates: []transform.Format{transform.FormatWebP, transform.FormatPNG},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d, err := Decide(tt.req, ParseAccept(tt.accept), &tt.cfg, tt.meta)
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if diff := cmp.Diff(tt.want, project(&d)); diff != "" {
				t.Errorf("Decide mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDecideNotAcceptable(t *testing.T) {
	t.Parallel()
	cfg := withCfg(func(c *Config) { c.StrictAccept = true })
	for _, accept := range []string{"text/html", "image/webp;q=0,image/*;q=0", "application/json"} {
		t.Run(accept, func(t *testing.T) {
			t.Parallel()
			d, err := Decide(auto(), ParseAccept(accept), &cfg, photoOpaque)
			if errs.KindOf(err) != errs.KindNotAcceptable {
				t.Fatalf("Decide error = %v (kind %v), want not_acceptable", err, errs.KindOf(err))
			}
			if !d.Vary.Has(VaryAccept) {
				t.Errorf("406 must still vary on Accept, got %q", d.Vary)
			}
		})
	}
	t.Run("absent_accept_is_any", func(t *testing.T) {
		t.Parallel()
		d, err := Decide(auto(), nil, &cfg, photoOpaque)
		if err != nil || d.Format != transform.FormatJPEG {
			t.Fatalf("Decide = %v, %v; want jpeg", d.Format, err)
		}
	})
	t.Run("explicit_never_406", func(t *testing.T) {
		t.Parallel()
		d, err := Decide(explicit(transform.FormatJPEG), ParseAccept("text/html"), &cfg, photoOpaque)
		if err != nil || d.Format != transform.FormatJPEG || d.Vary != 0 {
			t.Fatalf("Decide = %+v, %v", d, err)
		}
	})
}

// TestDecideVary pins ARCHITECTURE §9.1: Vary: Accept only when the format
// was negotiated.
func TestDecideVary(t *testing.T) {
	t.Parallel()
	cfg := allAuto()
	tests := []struct {
		name string
		req  *transform.Request
		meta SourceMeta
		want VarySet
	}{
		{"explicit_format", explicit(transform.FormatWebP), photoOpaque, 0},
		{"explicit_extension_like_png", explicit(transform.FormatPNG), photoAlpha, 0},
		{"info", &transform.Request{Kind: transform.KindInfo}, photoOpaque, 0},
		{"skip_processing", &transform.Request{Kind: transform.KindRaw}, photoOpaque, 0},
		{"auto", auto(), photoOpaque, VaryAccept},
		{"auto_fallback_still_varies", auto(), SourceMeta{Format: transform.FormatJPEG, Pixels: 1 << 40}, VaryAccept},
		{"svg_passthrough", auto(), svgSource, VaryAccept},
		{"best", explicit(transform.FormatBest), photoOpaque, VaryAccept},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, accept := range []string{"", acceptLegacy, acceptChrome} {
				d, err := Decide(tt.req, ParseAccept(accept), &cfg, tt.meta)
				if err != nil {
					t.Fatal(err)
				}
				if d.Vary != tt.want {
					t.Errorf("accept %q: Vary = %q, want %q", accept, d.Vary, tt.want)
				}
			}
		})
	}
	t.Run("no_auto_flags_no_vary", func(t *testing.T) {
		t.Parallel()
		off := Config{Preferred: cfg.Preferred}
		d, err := Decide(auto(), ParseAccept(acceptChrome), &off, photoOpaque)
		if err != nil || d.Vary != 0 {
			t.Fatalf("Vary = %q, err %v; want none", d.Vary, err)
		}
	})
	t.Run("only_jxl_enabled_animated_no_vary", func(t *testing.T) {
		t.Parallel()
		jxl := Config{AutoJXL: true, Preferred: cfg.Preferred}
		d, err := Decide(auto(), ParseAccept(acceptChrome), &jxl, animated)
		if err != nil || d.Vary != 0 || d.Format != transform.FormatGIF {
			t.Fatalf("Decide = %v Vary %q, err %v; want gif without Vary", d.Format, d.Vary, err)
		}
	})
}

func TestDecisionContentType(t *testing.T) {
	t.Parallel()
	cfg := allAuto()
	d, err := Decide(auto(), ParseAccept(acceptChrome), &cfg, photoOpaque)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.ContentType(); got != "image/avif" {
		t.Errorf("ContentType = %q", got)
	}
	for f := range transform.Format(16) {
		mt, ok := mediaTypeOf(f)
		ct := ContentType(f)
		if ok != (ct != "") || (ok && ct != mt.typ+"/"+mt.sub) {
			t.Errorf("format %v: ContentType %q disagrees with mediaTypeOf %v", f, ct, mt)
		}
	}
	if ContentType(transform.FormatBest) != "" || ContentType(transform.FormatUnset) != "" {
		t.Error("pseudo formats must have no content type")
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()
	good := DefaultConfig()
	if err := good.Validate(); err != nil {
		t.Fatalf("DefaultConfig invalid: %v", err)
	}
	bad := []Config{
		{},
		{Preferred: []transform.Format{transform.FormatSVG}},
		{Preferred: []transform.Format{transform.FormatJPEG}, SkipProcessing: []transform.Format{transform.FormatBest}},
		{Preferred: []transform.Format{transform.FormatJPEG}, AVIFMaxPixels: -1},
		{Preferred: []transform.Format{transform.FormatJPEG}, SaveDataQualityDelta: 5},
	}
	for i, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("case %d: Validate accepted %+v", i, c)
		}
	}
}

func TestStrings(t *testing.T) {
	t.Parallel()
	for r := range ReasonBest + 2 {
		if r.String() == "" {
			t.Errorf("Reason(%d) has empty name", r)
		}
	}
	for c := range ContentGraphic + 2 {
		if c.String() == "" {
			t.Errorf("ContentClass(%d) has empty name", c)
		}
	}
}

func BenchmarkDecide(b *testing.B) {
	cfg := allAuto()
	accept := ParseAccept(acceptChrome)
	req := auto()
	b.ReportAllocs()
	for b.Loop() {
		d, err := Decide(req, accept, &cfg, photoAlpha)
		if err != nil || d.Format != transform.FormatAVIF {
			b.Fatal(d.Format, err)
		}
	}
}

func BenchmarkDecideExplicit(b *testing.B) {
	cfg := allAuto()
	accept := ParseAccept(acceptChrome)
	req := explicit(transform.FormatWebP)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Decide(req, accept, &cfg, photoOpaque); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseAcceptAndDecide(b *testing.B) {
	cfg := allAuto()
	req := auto()
	b.ReportAllocs()
	for b.Loop() {
		d, err := Decide(req, ParseAccept(acceptChrome), &cfg, photoOpaque)
		if err != nil || d.Format != transform.FormatAVIF {
			b.Fatal(d.Format, err)
		}
	}
}
