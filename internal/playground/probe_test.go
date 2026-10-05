package playground

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// fakeProber answers from fixed values and records what it was asked.
type fakeProber struct {
	block   chan struct{} // when set, probes wait for it
	started chan struct{}
}

func (f *fakeProber) wait(ctx context.Context) error {
	if f.block == nil {
		return nil
	}
	f.started <- struct{}{}
	select {
	case <-f.block:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakeProber) ProbeSource(ctx context.Context, src string) (*MediaInfo, error) {
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	switch src {
	case "xi://local/clip.mp4":
		if !source.IsPlaygroundRequest(ctx) {
			return nil, errors.New("not a playground request")
		}
		return &MediaInfo{
			Container: "mov,mp4,m4a,3gp,3g2,mj2", Duration: 2, Size: 1000, BitRate: 4000,
			Video: &MediaVideo{Codec: "av1", Width: 1280, Height: 720, FPS: 25},
		}, nil
	case "xi://local/text.txt":
		return nil, errs.WithPublic(errs.New(errs.KindUnsupported, "ffmpeg: secret detail"), "unsupported video format")
	default:
		return nil, errors.New("internal failure with a secret")
	}
}

func (f *fakeProber) ProbeBytes(ctx context.Context, b []byte) (*MediaInfo, error) {
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	return &MediaInfo{Container: "mp3", Duration: 4, Size: int64(len(b)), Audio: &MediaAudio{Codec: "mp3", SampleRate: 44100, Channels: 2}}, nil
}

func (*fakeProber) MaxBytes() int64 { return 64 }

func newProbeFixture(t *testing.T, p MediaProber) http.Handler {
	t.Helper()
	h, err := New(Config{Path: DefaultPath}, &Deps{Meta: testMeta(), Image: &fakeImage{}, Logger: slog.New(slog.DiscardHandler), Prober: p})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func probeReq(t *testing.T, path, contentType, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/_playground/api/"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	return req
}

// TestProbeEndpoints: api/probe describes a source (as a playground
// request, so playground:// resolves), api/probe/result a body; errors
// keep their public text only.
func TestProbeEndpoints(t *testing.T) {
	t.Parallel()
	h := newProbeFixture(t, &fakeProber{})
	tests := []struct {
		name       string
		req        *http.Request
		code       int
		want       *MediaInfo
		errContain string
	}{
		{
			"source", probeReq(t, "probe", "application/json", `{"source":"xi://local/clip.mp4"}`), http.StatusOK,
			&MediaInfo{
				Container: "mov,mp4,m4a,3gp,3g2,mj2", Duration: 2, Size: 1000, BitRate: 4000,
				Video: &MediaVideo{Codec: "av1", Width: 1280, Height: 720, FPS: 25},
			}, "",
		},
		{
			"source error is public", probeReq(t, "probe", "application/json", `{"source":"xi://local/text.txt"}`),
			http.StatusUnprocessableEntity, nil, "unsupported video format",
		},
		{
			"internal error is hidden", probeReq(t, "probe", "application/json", `{"source":"xi://local/x"}`),
			http.StatusInternalServerError, nil, "probe failed",
		},
		{"empty source", probeReq(t, "probe", "application/json", `{"source":""}`), http.StatusBadRequest, nil, "source"},
		{"unescaped source", probeReq(t, "probe", "application/json", `{"source":"a b"}`), http.StatusBadRequest, nil, "source"},
		{"not json", probeReq(t, "probe", "text/plain", `x`), http.StatusUnsupportedMediaType, nil, ""},
		{
			"result", probeReq(t, "probe/result", "audio/mpeg", "ID3-some-bytes"), http.StatusOK,
			&MediaInfo{Container: "mp3", Duration: 4, Size: 14, Audio: &MediaAudio{Codec: "mp3", SampleRate: 44100, Channels: 2}}, "",
		},
		{"result too large", probeReq(t, "probe/result", "video/mp4", strings.Repeat("x", 65)), http.StatusRequestEntityTooLarge, nil, ""},
		{"empty result", probeReq(t, "probe/result", "video/mp4", ""), http.StatusBadRequest, nil, "empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, body := serve(t, h, tc.req)
			if code != tc.code {
				t.Fatalf("status %d, want %d: %s", code, tc.code, body)
			}
			if strings.Contains(string(body), "secret") {
				t.Errorf("internal text leaked: %s", body)
			}
			if tc.errContain != "" && !strings.Contains(string(body), tc.errContain) {
				t.Errorf("body %s, want %q", body, tc.errContain)
			}
			if tc.want == nil {
				return
			}
			var got MediaInfo
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(*tc.want, got); diff != "" {
				t.Errorf("info (-want +got):\n%s", diff)
			}
		})
	}
	cross := probeReq(t, "probe", "application/json", `{"source":"xi://local/clip.mp4"}`)
	cross.Header.Set("Sec-Fetch-Site", "cross-site")
	if code, _ := serve(t, h, cross); code != http.StatusForbidden {
		t.Errorf("cross-site probe = %d, want 403", code)
	}
}

// TestProbeBusy: every slot taken answers 429 with Retry-After, without
// waiting.
func TestProbeBusy(t *testing.T) {
	t.Parallel()
	f := &fakeProber{block: make(chan struct{}), started: make(chan struct{}, probeSlots)}
	h := newProbeFixture(t, f)
	done := make(chan int, probeSlots)
	for range probeSlots {
		go func() {
			code, _ := serve(t, h, probeReq(t, "probe/result", "video/mp4", "abc"))
			done <- code
		}()
	}
	for range probeSlots {
		<-f.started
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, probeReq(t, "probe/result", "video/mp4", "abc"))
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("busy probe = %d (Retry-After %q), want 429", rec.Code, rec.Header().Get("Retry-After"))
	}
	close(f.block)
	for range probeSlots {
		if code := <-done; code != http.StatusOK {
			t.Errorf("blocked probe = %d, want 200", code)
		}
	}
}

// TestProbeDisabled: without a prober the routes do not exist.
func TestProbeDisabled(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, p := range []string{"probe", "probe/result"} {
		res, _ := f.do(t, probeReq(t, p, "application/json", `{"source":"x"}`))
		if res.StatusCode != http.StatusNotFound && res.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s without a prober = %d, want 404/405", p, res.StatusCode)
		}
	}
}

// TestSignVideo: a /video path on an imgproxy mount is signed over
// "/video" + the path (domain-separated from images) and placed after
// /video; unsigned mounts get /video/insecure/.
func TestSignVideo(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ipv, err := sign.NewImgproxyVerifier(sign.ImgproxyOptions{Keys: f.ring, Size: 32, Required: true})
	if err != nil {
		t.Fatal(err)
	}
	const opts = "/rs:fit:640:0/vs:1/vd:2/plain/xi://local/clip.mp4@webm"
	res, b := f.do(t, post(t, `{"mount":"imgproxy","path":"/video`+opts+`"}`))
	var out SignResponse
	if res.StatusCode != http.StatusOK || json.Unmarshal(b, &out) != nil {
		t.Fatalf("status %d: %s", res.StatusCode, b)
	}
	rest, ok := strings.CutPrefix(out.URL, "/img/video/")
	sig, path, _ := strings.Cut(rest, "/")
	if !ok || !out.Signed || "/"+path != opts {
		t.Fatalf("url %q", out.URL)
	}
	if r, err := ipv.Verify(sig, "/video"+opts); err != nil || r != sign.Verified {
		t.Errorf("video signature: Verify = %v, %v", r, err)
	}
	if r, err := ipv.Verify(sig, opts); err == nil && r == sign.Verified {
		t.Error("the video signature is also valid for the image path")
	}
	res, b = f.do(t, post(t, `{"mount":"imgproxy-open","path":"/video`+opts+`"}`))
	if res.StatusCode != http.StatusOK || !strings.Contains(string(b), `"/img/open/video/insecure`+opts+`"`) {
		t.Errorf("unsigned video url: %d %s", res.StatusCode, b)
	}
}
