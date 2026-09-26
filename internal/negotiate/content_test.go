package negotiate

import (
	"errors"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// flat returns a w×h RGB image of vertical bands in n colours.
func flat(w, h, n int) []byte {
	pix := make([]byte, 0, w*h*3)
	for range h {
		for x := range w {
			c := byte(x * n / w * 40) //nolint:gosec // G115: n ≤ 6 bands, at most 200
			pix = append(pix, c, 255-c, c/2)
		}
	}
	return pix
}

// noisy returns a w×h image of bands smooth texture plus sensor noise.
func noisy(w, h, bands int) []byte {
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // G404: reproducible test noise, not a secret
	pix := make([]byte, 0, w*h*bands)
	for y := range h {
		for x := range w {
			base := float64(x+y) * 255 / float64(w+h)
			for b := range bands {
				v := base + r.NormFloat64()*6 + float64(b*10)
				if bands == 4 && b == 3 {
					v = 200
				}
				pix = append(pix, byte(min(max(v, 0), 255)))
			}
		}
	}
	return pix
}

func TestAnalyze(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		pix       []byte
		w, h, b   int
		threshold float64
		class     ContentClass
		colors    int
		alpha     bool
	}{
		{"flat bands", flat(64, 48, 6), 64, 48, 3, 5.5, ContentGraphic, 6, false},
		{"noise rgb", noisy(128, 96, 3), 128, 96, 3, 5.5, ContentPhoto, 0, false},
		{"noise rgba", noisy(128, 96, 4), 128, 96, 4, 5.5, ContentPhoto, 0, true},
		// A grey photo has at most 256 colours: its colour term is small
		// and it lands in the mixed band.
		{"noise grey", noisy(128, 96, 1), 128, 96, 1, 5.5, ContentMixed, 0, false},
		{"grey alpha single colour", []byte{10, 0, 10, 0, 10, 0, 10, 0}, 2, 2, 2, 5.5, ContentGraphic, 1, true},
		{"threshold zero", flat(64, 48, 6), 64, 48, 3, 0, ContentPhoto, 0, false},
		{"photo scored against a high threshold", noisy(128, 96, 3), 128, 96, 3, 20, ContentGraphic, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, err := Analyze(tt.pix, tt.w, tt.h, tt.b, tt.threshold)
			if err != nil {
				t.Fatal(err)
			}
			if a.Class != tt.class || a.AlphaUsed != tt.alpha || (tt.colors > 0 && a.Colors != tt.colors) {
				t.Errorf("Analyze = %+v, want class %s, colours %d, alpha %v", a, tt.class, tt.colors, tt.alpha)
			}
			if a.Complexity < 0 || a.EdgeDensity < 0 || a.EdgeDensity > 1 || a.GradientEntropy > 8 {
				t.Errorf("Analyze = %+v: score out of range", a)
			}
		})
	}
}

func TestAnalyzeMixedBand(t *testing.T) {
	t.Parallel()
	a, err := Analyze(noisy(128, 96, 3), 128, 96, 3, 5.5)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		threshold float64
		want      ContentClass
	}{
		{a.Complexity - mixedBand - 0.01, ContentPhoto},
		{a.Complexity - mixedBand/2, ContentMixed},
		{a.Complexity + mixedBand/2, ContentMixed},
		{a.Complexity + mixedBand + 0.01, ContentGraphic},
	} {
		got, err := Analyze(noisy(128, 96, 3), 128, 96, 3, tc.threshold)
		if err != nil {
			t.Fatal(err)
		}
		if got.Class != tc.want {
			t.Errorf("complexity %.3f, threshold %.3f: class %s, want %s", a.Complexity, tc.threshold, got.Class, tc.want)
		}
	}
}

func TestAnalyzeInput(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		pix     []byte
		w, h, b int
	}{
		{"short", make([]byte, 5), 2, 1, 3},
		{"zero width", nil, 0, 1, 3},
		{"five bands", make([]byte, 10), 2, 1, 5},
		{"zero bands", nil, 2, 1, 0},
	} {
		if _, err := Analyze(tc.pix, tc.w, tc.h, tc.b, 5.5); !errors.Is(err, ErrAnalysisInput) {
			t.Errorf("%s: err %v, want ErrAnalysisInput", tc.name, err)
		}
	}
}

func FuzzAnalyze(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, uint8(2), uint8(3))
	f.Add(make([]byte, 64), uint8(4), uint8(4))
	f.Fuzz(func(t *testing.T, pix []byte, w, bands uint8) {
		if w == 0 || bands == 0 || bands > 4 || len(pix) == 0 {
			return
		}
		row := int(w) * int(bands)
		h := len(pix) / row
		if h == 0 {
			return
		}
		a, err := Analyze(pix[:h*row], int(w), h, int(bands), 5.5)
		if err != nil {
			t.Fatal(err)
		}
		if a.Colors < 1 || a.Colors > int(w)*h || a.Class == ContentUnknown {
			t.Fatalf("Analyze = %+v", a)
		}
	})
}

func TestUniqueColors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   []uint32
		want int
	}{
		{nil, 0},
		{[]uint32{0}, 1},
		{[]uint32{0xffffffff, 0xffffffff}, 1},
		{[]uint32{0, 0xffffffff, 0, 5, 0xffffffff, 7, 7, 5}, 4},
	} {
		if got := uniqueColors(slices.Clone(tc.in)); got != tc.want {
			t.Errorf("uniqueColors(%x) = %d, want %d", tc.in, got, tc.want)
		}
	}
	// Many colours: collisions and probing.
	many := make([]uint32, 0, 70_000)
	for i := range uint32(70_000) {
		many = append(many, i*2654435761)
	}
	if got := uniqueColors(many); got != 70_000 {
		t.Errorf("uniqueColors(70000 distinct) = %d", got)
	}
}

func bestReq() *transform.Request { return explicit(transform.FormatBest) }

func TestBestCandidates(t *testing.T) {
	t.Parallel()
	c := func(f transform.Format, lossless bool) Candidate { return Candidate{Format: f, Lossless: lossless} }
	photoStill := SourceMeta{Format: transform.FormatJPEG}
	tests := []struct {
		name   string
		req    *transform.Request
		accept string
		cfg    Config
		meta   SourceMeta
		class  ContentClass
		colors int
		want   []Candidate
	}{
		{
			"photo", bestReq(), acceptChrome, allAuto(), photoStill, ContentPhoto, 0,
			[]Candidate{c(transform.FormatAVIF, false), c(transform.FormatWebP, false), c(transform.FormatJPEG, false)},
		},
		{
			"unknown is photo", bestReq(), acceptChrome, allAuto(), photoStill, ContentUnknown, 0,
			[]Candidate{c(transform.FormatAVIF, false), c(transform.FormatWebP, false), c(transform.FormatJPEG, false)},
		},
		{
			"graphic", bestReq(), acceptChrome, allAuto(), photoStill, ContentGraphic, 4000,
			[]Candidate{c(transform.FormatWebP, true), c(transform.FormatPNG, true)},
		},
		{
			"graphic quantised png", bestReq(), acceptChrome, allAuto(), photoStill, ContentGraphic, 12,
			[]Candidate{c(transform.FormatWebP, true), {Format: transform.FormatPNG, Lossless: true, Quantize: true}},
		},
		{
			"graphic with jxl", bestReq(), "image/jxl,image/webp,*/*", allAuto(), photoStill, ContentGraphic, 0,
			[]Candidate{c(transform.FormatWebP, true), c(transform.FormatJXL, true), c(transform.FormatPNG, true)},
		},
		{
			"graphic enforced avif", bestReq(), acceptChrome,
			withCfg(func(c *Config) { c.EnforceAVIF = true }), photoStill, ContentGraphic, 0,
			[]Candidate{c(transform.FormatAVIF, true), c(transform.FormatWebP, true), c(transform.FormatPNG, true)},
		},
		{
			"mixed interleaves", bestReq(), acceptChrome, allAuto(), photoStill, ContentMixed, 0,
			[]Candidate{c(transform.FormatAVIF, false), c(transform.FormatWebP, true), c(transform.FormatWebP, false)},
		},
		{
			"mixed legacy", bestReq(), acceptLegacy, allAuto(), photoStill, ContentMixed, 0,
			[]Candidate{c(transform.FormatJPEG, false), c(transform.FormatPNG, true)},
		},
		{
			"photo with alpha", bestReq(), acceptWebP, allAuto(), photoAlpha, ContentPhoto, 0,
			[]Candidate{c(transform.FormatWebP, false), c(transform.FormatPNG, true)},
		},
		{
			"source meta graphic, analysed photo", bestReq(), acceptChrome, allAuto(), graphicOpaque, ContentPhoto, 0,
			[]Candidate{c(transform.FormatAVIF, false), c(transform.FormatWebP, false), c(transform.FormatJPEG, false)},
		},
		{
			"animated photo", bestReq(), acceptChrome, allAuto(), animated, ContentPhoto, 0,
			[]Candidate{c(transform.FormatWebP, false), c(transform.FormatGIF, false)},
		},
		{
			"animated graphic", bestReq(), acceptChrome, allAuto(), animated, ContentGraphic, 0,
			[]Candidate{c(transform.FormatWebP, true), c(transform.FormatGIF, false)},
		},
		{
			"animated mixed", bestReq(), acceptChrome, allAuto(), animated, ContentMixed, 0,
			[]Candidate{c(transform.FormatWebP, false), c(transform.FormatWebP, true), c(transform.FormatGIF, false)},
		},
		{
			"animated legacy", bestReq(), acceptLegacy, allAuto(), animated, ContentGraphic, 0,
			[]Candidate{c(transform.FormatGIF, false)},
		},
		{
			"strict accept drops fallbacks", bestReq(), "image/webp",
			withCfg(func(c *Config) { c.StrictAccept = true }), photoStill, ContentMixed, 0,
			[]Candidate{c(transform.FormatWebP, false), c(transform.FormatWebP, true)},
		},
		{"not best", auto(), acceptChrome, allAuto(), photoStill, ContentPhoto, 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d, err := Decide(tt.req, ParseAccept(tt.accept), &tt.cfg, tt.meta)
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			got := d.BestCandidates(tt.class, tt.colors)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("BestCandidates (-want +got):\n%s", diff)
			}
			if tt.req.Output.Format == transform.FormatBest && !d.Vary.Has(VaryAccept) {
				t.Errorf("Vary %q: Accept decided the candidates", d.Vary)
			}
		})
	}
}

func BenchmarkAnalyze(b *testing.B) {
	pix := noisy(256, 192, 3)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Analyze(pix, 256, 192, 3, 5.5); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecideBest(b *testing.B) {
	cfg := allAuto()
	accept := ParseAccept(acceptChrome)
	req := bestReq()
	b.ReportAllocs()
	for b.Loop() {
		d, err := Decide(req, accept, &cfg, photoAlpha)
		if err != nil || d.Reason != ReasonBest {
			b.Fatal(d.Reason, err)
		}
	}
}
