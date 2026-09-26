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
		{"sec_ch", header("Sec-CH-DPR", "2", "Sec-CH-Width", "800", "Sec-CH-Viewport-Width", "400"), ClientHints{DPR: 2, Width: 800, ViewportWidth: 400}},
		{"legacy", header("DPR", "1.5", "Width", "640", "Viewport-Width", "320"), ClientHints{DPR: 1.5, Width: 640, ViewportWidth: 320}},
		{"sec_ch_wins", header("Sec-CH-DPR", "3", "DPR", "2", "Sec-CH-Width", "900", "Width", "100"), ClientHints{DPR: 3, Width: 900}},
		{"invalid_sec_ch_falls_back", header("Sec-CH-DPR", "abc", "DPR", "2", "Sec-CH-Width", "-5", "Width", "300"), ClientHints{DPR: 2, Width: 300}},
		{"dpr_range", header("DPR", "9"), ClientHints{}},
		{"dpr_zero", header("DPR", "0"), ClientHints{}},
		{"dpr_nan", header("DPR", "NaN"), ClientHints{}},
		{"dpr_inf", header("DPR", "+Inf"), ClientHints{}},
		{"dpr_max", header("DPR", "8"), ClientHints{DPR: 8}},
		{"width_too_large", header("Width", "65537"), ClientHints{}},
		{"width_float", header("Width", "100.5"), ClientHints{}},
		{"sf_params_and_ows", header("Sec-CH-DPR", " 2.0;x=1 ", "Sec-CH-Width", "500 "), ClientHints{DPR: 2, Width: 500}},
		{"save_data", header("Save-Data", "on"), ClientHints{SaveData: true}},
		{"save_data_case_params", header("Save-Data", "On; foo"), ClientHints{SaveData: true}},
		{"save_data_off", header("Save-Data", "off"), ClientHints{}},
		{"conn_type_2g", header(headerECT, "2g"), ClientHints{SlowNetwork: true}},
		{"conn_type_slow_2g_quoted", header(headerECT, `"slow-2g"`), ClientHints{SlowNetwork: true}},
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

func TestApplyClientHints(t *testing.T) {
	t.Parallel()
	type want struct {
		Width   transform.Dim
		DPR     float64
		Quality int
		Result  HintResult
	}
	optIn := func(ch transform.ClientHints) *transform.Request {
		return &transform.Request{ClientHints: ch, Pipelines: []transform.Pipeline{{}}}
	}
	def := DefaultConfig()
	global := DefaultConfig()
	global.EnableClientHints = true
	tests := []struct {
		name  string
		req   *transform.Request
		hints ClientHints
		cfg   Config
		want  want
	}{
		{"not_opted_in", optIn(transform.ClientHints{}), ClientHints{DPR: 2, Width: 800}, def, want{}},
		{
			"dpr_and_width", optIn(transform.ClientHints{DPR: true, Width: true}),
			ClientHints{DPR: 2, Width: 801},
			def,
			want{Width: transform.Dim{Px: 401}, DPR: 2, Result: HintResult{Vary: varyDPRGroup | varyWidthGroup, ContentDPR: 2}},
		},
		{
			"width_only_physical_px", optIn(transform.ClientHints{Width: true}),
			ClientHints{DPR: 2, Width: 800},
			def,
			want{Width: transform.Dim{Px: 800}, Result: HintResult{Vary: varyWidthGroup}},
		},
		{
			"width_step_rounds_up", optIn(transform.ClientHints{Width: true, WidthStep: 100}),
			ClientHints{Width: 801},
			def,
			want{Width: transform.Dim{Px: 900}, Result: HintResult{Vary: varyWidthGroup}},
		},
		{
			"viewport_fallback", optIn(transform.ClientHints{Width: true}),
			ClientHints{ViewportWidth: 375},
			def,
			want{Width: transform.Dim{Px: 375}, Result: HintResult{Vary: varyWidthGroup}},
		},
		{
			"consulted_but_absent_still_varies", optIn(transform.ClientHints{DPR: true, Width: true}),
			ClientHints{},
			def,
			want{Result: HintResult{Vary: varyDPRGroup | varyWidthGroup}},
		},
		{
			"global_fills_unset_only", &transform.Request{Pipelines: []transform.Pipeline{{Resize: transform.Resize{Width: transform.Dim{Px: 300}}}}},
			ClientHints{DPR: 3, Width: 900},
			global,
			want{Width: transform.Dim{Px: 300}, DPR: 3, Result: HintResult{Vary: varyDPRGroup | varySaveDataGroup, ContentDPR: 3}},
		},
		{
			"global_no_pipelines", &transform.Request{},
			ClientHints{Width: 640},
			global,
			want{Width: transform.Dim{Px: 640}, Result: HintResult{Vary: varyDPRGroup | varyWidthGroup | varySaveDataGroup}},
		},
		{
			"save_data_delta", optIn(transform.ClientHints{SaveData: true}),
			ClientHints{SaveData: true},
			def,
			want{Result: HintResult{Vary: varySaveDataGroup, SlowConnection: true, QualityDelta: -15}},
		},
		{
			"save_data_url_quality", &transform.Request{ClientHints: transform.ClientHints{SaveData: true}, Output: transform.Output{SlowConnectionQuality: 40}},
			ClientHints{SlowNetwork: true},
			def,
			want{Quality: 40, Result: HintResult{Vary: varySaveDataGroup, SlowConnection: true}},
		},
		{
			"save_data_ignored_when_quality_fixed", &transform.Request{ClientHints: transform.ClientHints{SaveData: true}, Output: transform.Output{Quality: 90}},
			ClientHints{SaveData: true},
			def,
			want{Quality: 90},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := ApplyClientHints(tt.req, tt.hints, &tt.cfg)
			got := want{Quality: tt.req.Output.Quality, Result: res}
			if len(tt.req.Pipelines) > 0 {
				got.Width = tt.req.Pipelines[0].Resize.Width
				got.DPR = tt.req.Pipelines[0].Resize.DPR
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ApplyClientHints (-want +got):\n%s", diff)
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
	hr := ApplyClientHints(req, ParseClientHints(h), &cfg)
	req.Output.Format = transform.FormatJPEG // explicit: Accept must not vary
	d, err := Decide(req, ParseAcceptHeader(h), &cfg, photoOpaque)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := (d.Vary | hr.Vary).Header(), "Sec-CH-DPR, DPR, Sec-CH-Width, Width, Sec-CH-Viewport-Width, Viewport-Width"; got != want {
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
		ApplyClientHints(req, c, &cfg)
		if px := req.Pipelines[0].Resize.Width.Px; px < 0 || px > transform.MaxDimension {
			t.Fatalf("resolved width %d out of range", px)
		}
	})
}
