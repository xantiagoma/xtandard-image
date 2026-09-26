package negotiate

import (
	"math"
	"net/http"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

func header(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Add(kv[i], kv[i+1])
	}
	return h
}

func TestParseClientHints(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		h    http.Header
		want ClientHints
	}{
		{"none", header(), ClientHints{}},
		{
			"sec_ch", header("Sec-CH-DPR", "2", "Sec-CH-Width", "800", "Sec-CH-Viewport-Width", "400"),
			ClientHints{DPR: 2, Width: 800, ViewportWidth: 400, From: VarySecCHDPR | VarySecCHWidth | VarySecCHViewportWidth},
		},
		{
			"legacy", header("DPR", "1.5", "Width", "640", "Viewport-Width", "320"),
			ClientHints{DPR: 1.5, Width: 640, ViewportWidth: 320, From: VaryDPR | VaryWidth | VaryViewportWidth},
		},
		{
			"sec_ch_wins", header("Sec-CH-DPR", "3", "DPR", "2", "Sec-CH-Width", "900", "Width", "100", "Sec-CH-Viewport-Width", "5", "Viewport-Width", "6"),
			ClientHints{DPR: 3, Width: 900, ViewportWidth: 5, From: VarySecCHDPR | VarySecCHWidth | VarySecCHViewportWidth},
		},
		{
			"invalid_sec_ch_falls_back", header("Sec-CH-DPR", "abc", "DPR", "2", "Sec-CH-Width", "-5", "Width", "300"),
			ClientHints{DPR: 2, Width: 300, From: VaryDPR | VaryWidth},
		},
		{"dpr_range", header("DPR", "9"), ClientHints{}},
		{"dpr_zero", header("DPR", "0"), ClientHints{}},
		{"dpr_nan", header("DPR", "NaN"), ClientHints{}},
		{"dpr_inf", header("DPR", "+Inf"), ClientHints{}},
		{"dpr_max", header("DPR", "8"), ClientHints{DPR: 8, From: VaryDPR}},
		{"width_too_large", header("Width", "65537"), ClientHints{}},
		{"width_float", header("Width", "100.5"), ClientHints{}},
		{"sf_params_and_ows", header("Sec-CH-DPR", " 2.0;x=1 ", "Sec-CH-Width", "500 "), ClientHints{DPR: 2, Width: 500, From: VarySecCHDPR | VarySecCHWidth}},
		{"save_data", header("Save-Data", "on"), ClientHints{SaveData: true, From: VarySaveData}},
		{"save_data_case_params", header("Save-Data", "On; foo"), ClientHints{SaveData: true, From: VarySaveData}},
		{"save_data_off", header("Save-Data", "off"), ClientHints{}},
		{"conn_type_2g", header(headerECT, "2g"), ClientHints{SlowNetwork: true, From: VaryECT}},
		{"conn_type_slow_2g_quoted", header(headerECT, `"slow-2g"`), ClientHints{SlowNetwork: true, From: VaryECT}},
		{"conn_type_4g", header(headerECT, "4g"), ClientHints{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, ParseClientHints(tt.h)); diff != "" {
				t.Errorf("ParseClientHints (-want +got):\n%s", diff)
			}
		})
	}
}

// Vary lists used by the header table below.
const (
	vDPR    = "Sec-CH-DPR, DPR"
	vWidth  = "Sec-CH-Width, Width, Sec-CH-Viewport-Width, Viewport-Width"
	vSave   = "Save-Data, " + headerECT
	vAllHnt = vDPR + ", " + vWidth + ", " + vSave
)

// TestApplyClientHints is the WP-65 header table: for each hint and
// precedence combination, the resolved width, DPR and quality and the exact
// Vary list (only the hint names that decided the result).
func TestApplyClientHints(t *testing.T) {
	t.Parallel()
	type want struct {
		Pipelines  int
		Width      int
		DPR        float64
		Quality    int
		Delta      int
		ContentDPR float64
		Vary       string
	}
	type reqOpt func(*transform.Request)
	urlWidth := func(px int) reqOpt {
		return func(r *transform.Request) { r.Pipelines[0].Resize.Width = transform.Dim{Px: px} }
	}
	urlDPR := func(d float64) reqOpt { return func(r *transform.Request) { r.Pipelines[0].Resize.DPR = d } }
	urlQuality := func(q int) reqOpt { return func(r *transform.Request) { r.Output.Quality = q } }
	optIn := func(ch transform.ClientHints) reqOpt { return func(r *transform.Request) { r.ClientHints = ch } }
	noPipelines := func(r *transform.Request) { r.Pipelines = nil }
	scq := func(q int) reqOpt { return func(r *transform.Request) { r.Output.SlowConnectionQuality = q } }

	off := DefaultConfig()
	on := DefaultConfig()
	on.EnableClientHints = true
	onNoDelta := on
	onNoDelta.SaveDataQualityDelta = 0

	tests := []struct {
		name  string
		cfg   Config
		scope HintScope
		opts  []reqOpt
		h     http.Header
		want  want
	}{
		// Server-wide switch (imgproxy IMGPROXY_ENABLE_CLIENT_HINTS).
		{
			"disabled_ignores_hints", off, HintsAll, nil,
			header("Sec-CH-DPR", "2", "Sec-CH-Width", "800", "Save-Data", "on"),
			want{Pipelines: 1},
		},
		{
			"enabled_absent_varies_on_every_name", on, HintsAll, nil,
			header(),
			want{Pipelines: 1, Vary: vAllHnt},
		},

		// DPR precedence.
		{
			"dpr_sec_ch", on, HintsAll, nil, header("Sec-CH-DPR", "2"),
			want{Pipelines: 1, DPR: 2, ContentDPR: 2, Vary: "Sec-CH-DPR, " + vWidth + ", " + vSave},
		},
		{
			"dpr_legacy", on, HintsAll, nil, header("DPR", "2"),
			want{Pipelines: 1, DPR: 2, ContentDPR: 2, Vary: vAllHnt},
		},
		{
			"dpr_sec_ch_beats_legacy", on, HintsAll, nil, header("Sec-CH-DPR", "3", "DPR", "2"),
			want{Pipelines: 1, DPR: 3, ContentDPR: 3, Vary: "Sec-CH-DPR, " + vWidth + ", " + vSave},
		},
		{
			"dpr_invalid_sec_ch_uses_legacy", on, HintsAll, nil, header("Sec-CH-DPR", "x", "DPR", "2"),
			want{Pipelines: 1, DPR: 2, ContentDPR: 2, Vary: vAllHnt},
		},
		{
			"dpr_url_wins_no_vary", on, HintsAll,
			[]reqOpt{urlDPR(1.5)},
			header("Sec-CH-DPR", "3"),
			want{Pipelines: 1, DPR: 1.5, Vary: vWidth + ", " + vSave},
		},

		// Width precedence: Width (physical px / DPR) over Viewport-Width.
		{
			"width_divided_by_hint_dpr", on, HintsAll, nil, header("Sec-CH-DPR", "2", "Sec-CH-Width", "800"),
			want{Pipelines: 1, Width: 400, DPR: 2, ContentDPR: 2, Vary: "Sec-CH-DPR, Sec-CH-Width, " + vSave},
		},
		{
			"width_legacy_no_dpr", on, HintsAll, nil, header("Width", "800"),
			want{Pipelines: 1, Width: 800, Vary: vDPR + ", Sec-CH-Width, Width, " + vSave},
		},
		{
			"width_sec_ch_beats_legacy", on, HintsAll, nil, header("Sec-CH-Width", "700", "Width", "100"),
			want{Pipelines: 1, Width: 700, Vary: vDPR + ", Sec-CH-Width, " + vSave},
		},
		{
			"width_beats_viewport", on, HintsAll, nil, header("Sec-CH-Width", "800", "Sec-CH-Viewport-Width", "400"),
			want{Pipelines: 1, Width: 800, Vary: vDPR + ", Sec-CH-Width, " + vSave},
		},
		{
			"viewport_fallback_sec_ch", on, HintsAll, nil, header("Sec-CH-Viewport-Width", "375"),
			want{Pipelines: 1, Width: 375, Vary: vDPR + ", Sec-CH-Width, Width, Sec-CH-Viewport-Width, " + vSave},
		},
		{
			"viewport_fallback_legacy", on, HintsAll, nil, header("Viewport-Width", "375"),
			want{Pipelines: 1, Width: 375, Vary: vAllHnt},
		},
		{
			"width_url_wins_no_vary", on, HintsAll,
			[]reqOpt{urlWidth(300)},
			header("Sec-CH-Width", "900", "Sec-CH-DPR", "3"),
			want{Pipelines: 1, Width: 300, DPR: 3, ContentDPR: 3, Vary: "Sec-CH-DPR, " + vSave},
		},
		{
			"width_divided_by_url_dpr", on, HintsAll,
			[]reqOpt{urlDPR(2)},
			header("Sec-CH-Width", "900", "Sec-CH-DPR", "3"),
			want{Pipelines: 1, Width: 450, DPR: 2, Vary: "Sec-CH-Width, " + vSave},
		},
		{
			"url_fixes_everything", on, HintsAll,
			[]reqOpt{urlWidth(300), urlDPR(2), urlQuality(80)},
			header("Sec-CH-Width", "900", "Sec-CH-DPR", "3", "Save-Data", "on"),
			want{Pipelines: 1, Width: 300, DPR: 2, Quality: 80},
		},

		// Save-Data and the connection-type hint.
		{
			"save_data_delta", on, HintsAll,
			[]reqOpt{urlWidth(300), urlDPR(1)},
			header("Save-Data", "on", headerECT, "2g"),
			want{Pipelines: 1, Width: 300, DPR: 1, Delta: -15, Vary: "Save-Data"},
		},
		{
			"slow_connection_type_delta", on, HintsAll,
			[]reqOpt{urlWidth(300), urlDPR(1)},
			header(headerECT, "slow-2g"),
			want{Pipelines: 1, Width: 300, DPR: 1, Delta: -15, Vary: vSave},
		},
		{
			"save_data_url_quality_wins", on, HintsAll,
			[]reqOpt{urlWidth(300), urlDPR(1), urlQuality(90)},
			header("Save-Data", "on"),
			want{Pipelines: 1, Width: 300, DPR: 1, Quality: 90},
		},
		{
			"save_data_zero_delta_not_consulted", onNoDelta, HintsAll,
			[]reqOpt{urlWidth(300), urlDPR(1)},
			header("Save-Data", "on"),
			want{Pipelines: 1, Width: 300, DPR: 1},
		},
		{
			"save_data_slow_connection_quality", onNoDelta, HintsAll,
			[]reqOpt{urlWidth(300), urlDPR(1), scq(40)},
			header("Save-Data", "on"),
			want{Pipelines: 1, Width: 300, DPR: 1, Quality: 40, Vary: "Save-Data"},
		},

		// Restricted unsigned requests: server-wide width/DPR hints off.
		{
			"restricted_scope_save_data_only", on, HintsSaveDataOnly, nil,
			header("Sec-CH-DPR", "2", "Sec-CH-Width", "800", "Save-Data", "on"),
			want{Pipelines: 1, Delta: -15, Vary: "Save-Data"},
		},

		// URL opt-ins (imgix ch=, cloudinary w_auto, imagekit dpr_auto) work
		// without the server-wide switch.
		{
			"opt_in_dpr_and_width", off, HintsAll,
			[]reqOpt{optIn(transform.ClientHints{DPR: true, Width: true})},
			header("Sec-CH-DPR", "2", "Sec-CH-Width", "801"),
			want{Pipelines: 1, Width: 401, DPR: 2, ContentDPR: 2, Vary: "Sec-CH-DPR, Sec-CH-Width"},
		},
		{
			"opt_in_width_only_physical_px", off, HintsAll,
			[]reqOpt{optIn(transform.ClientHints{Width: true})},
			header("Sec-CH-DPR", "2", "Sec-CH-Width", "800"),
			want{Pipelines: 1, Width: 800, Vary: "Sec-CH-Width"},
		},
		{
			"opt_in_width_step_rounds_up", off, HintsAll,
			[]reqOpt{optIn(transform.ClientHints{Width: true, WidthStep: 100})},
			header("Sec-CH-Width", "801"),
			want{Pipelines: 1, Width: 900, Vary: "Sec-CH-Width"},
		},
		{
			"opt_in_absent_still_varies", off, HintsAll,
			[]reqOpt{optIn(transform.ClientHints{DPR: true, Width: true})},
			header(),
			want{Pipelines: 1, Vary: vDPR + ", " + vWidth},
		},
		{
			"opt_in_save_data", off, HintsAll,
			[]reqOpt{optIn(transform.ClientHints{SaveData: true})},
			header("Save-Data", "on"),
			want{Pipelines: 1, Delta: -15, Vary: "Save-Data"},
		},

		// Pipelines are created only for a value that changes the result.
		{
			"no_pipelines_width_creates_one", on, HintsAll,
			[]reqOpt{noPipelines},
			header("Sec-CH-Width", "640"),
			want{Pipelines: 1, Width: 640, Vary: vDPR + ", Sec-CH-Width, " + vSave},
		},
		{
			"no_pipelines_dpr_1_adds_none", on, HintsAll,
			[]reqOpt{noPipelines},
			header("Sec-CH-DPR", "1"),
			want{ContentDPR: 1, Vary: "Sec-CH-DPR, " + vWidth + ", " + vSave},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := &transform.Request{Pipelines: []transform.Pipeline{{}}}
			for _, o := range tt.opts {
				o(req)
			}
			res := ApplyClientHints(req, ParseClientHints(tt.h), &tt.cfg, tt.scope)
			got := want{
				Pipelines: len(req.Pipelines), Quality: req.Output.Quality,
				Delta: res.QualityDelta, ContentDPR: res.ContentDPR, Vary: res.Vary.Header(),
			}
			if len(req.Pipelines) > 0 {
				got.Width = req.Pipelines[0].Resize.Width.Px
				got.DPR = req.Pipelines[0].Resize.DPR
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ApplyClientHints (-want +got):\n%s", diff)
			}
			if res.SlowConnection != (res.QualityDelta != 0 || (req.Output.SlowConnectionQuality > 0 && req.Output.Quality == req.Output.SlowConnectionQuality)) {
				t.Errorf("SlowConnection = %v with delta %d", res.SlowConnection, res.QualityDelta)
			}
		})
	}
}

func TestVarySet(t *testing.T) {
	t.Parallel()
	tests := []struct {
		v    VarySet
		want string
	}{
		{0, ""},
		{VaryAccept, "Accept"},
		{VaryAccept | varyDPRGroup, "Accept, Sec-CH-DPR, DPR"},
		{varyWidthGroup, "Sec-CH-Width, Width, Sec-CH-Viewport-Width, Viewport-Width"},
		{varySaveDataGroup | VaryAccept, "Accept, Save-Data, " + headerECT},
	}
	for _, tt := range tests {
		if got := tt.v.Header(); got != tt.want {
			t.Errorf("Header(%b) = %q, want %q", tt.v, got, tt.want)
		}
	}
	all := varyEnd - 1
	names := all.Names()
	if len(names) != 9 || slices.Contains(names, "") || slices.Contains(names, "*") {
		t.Errorf("Names() = %v", names)
	}
	if !all.Has(VaryAccept|VaryECT) || VaryAccept.Has(VaryECT) {
		t.Error("Has is wrong")
	}
}

// TestVaryEndToEnd combines hints and negotiation the way the server does.
func TestVaryEndToEnd(t *testing.T) {
	t.Parallel()
	cfg := allAuto()
	req := &transform.Request{ClientHints: transform.ClientHints{DPR: true, Width: true}}
	h := header("Accept", acceptChrome, "Sec-CH-DPR", "2", "Sec-CH-Width", "1000")
	hr := ApplyClientHints(req, ParseClientHints(h), &cfg, HintsAll)
	req.Output.Format = transform.FormatJPEG // explicit: Accept must not vary
	d, err := Decide(req, ParseAcceptHeader(h), &cfg, photoOpaque)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := (d.Vary | hr.Vary).Header(), "Sec-CH-DPR, Sec-CH-Width"; got != want {
		t.Errorf("Vary = %q, want %q", got, want)
	}
	if req.Pipelines[0].Resize.Width.Px != 500 || req.Pipelines[0].Resize.DPR != 2 {
		t.Errorf("resize = %+v", req.Pipelines[0].Resize)
	}
}

// FuzzParseClientHints checks that hint parsing never panics and only yields
// in-range values.
func FuzzParseClientHints(f *testing.F) {
	for _, s := range []string{"", "2", "1.5", "NaN", "-1", "1e3", `"on"`, "on;x", "slow-2g", "800", "65536"} {
		f.Add(s, s)
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		h := header("Sec-CH-DPR", a, "DPR", b, "Sec-CH-Width", a, "Width", b,
			"Viewport-Width", a, "Save-Data", b, headerECT, a)
		c := ParseClientHints(h)
		if math.IsNaN(c.DPR) || c.DPR < 0 || c.DPR > MaxHintDPR {
			t.Fatalf("DPR %v out of range", c.DPR)
		}
		for _, w := range []int{c.Width, c.ViewportWidth} {
			if w < 0 || w > transform.MaxDimension {
				t.Fatalf("width %d out of range", w)
			}
		}
		req := &transform.Request{ClientHints: transform.ClientHints{DPR: true, Width: true, WidthStep: 7}}
		cfg := DefaultConfig()
		ApplyClientHints(req, c, &cfg, HintsAll)
		if len(req.Pipelines) == 0 {
			return
		}
		if px := req.Pipelines[0].Resize.Width.Px; px < 0 || px > transform.MaxDimension {
			t.Fatalf("resolved width %d out of range", px)
		}
	})
}
