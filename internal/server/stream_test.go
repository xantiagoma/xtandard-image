package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/cache/memory"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/media/fmp4"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/security/ticket"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
)

// box builds an MP4 box.
func box(typ string, payload ...[]byte) []byte {
	body := bytes.Join(payload, nil)
	b := binary.BigEndian.AppendUint32(nil, uint32(8+len(body))) //nolint:gosec // G115: test boxes are small
	return append(append(b, typ...), body...)
}

func be32(v ...uint32) []byte {
	var b []byte
	for _, x := range v {
		b = binary.BigEndian.AppendUint32(b, x)
	}
	return b
}

// fakeFMP4 is a fragmented MP4 of one track named by tag: an init segment
// and one fragment per decode time in starts.
func fakeFMP4(tag string, timescale uint32, starts ...uint64) []byte {
	moov := box("moov",
		box("trak", box("mdia", box("mdhd", be32(0, 0, 0, timescale, 0, 0)), box("hdlr", []byte(tag)))),
		box("mvex", box("trex", be32(0, 1, 1, 1000, 0, 0))))
	out := append(box("ftyp", []byte("iso5"), be32(0)), moov...)
	for i, t := range starts {
		traf := box("traf", box("tfhd", be32(0x020000, 1)), box("tfdt", be32(1<<24), binary.BigEndian.AppendUint64(nil, t)),
			box("trun", be32(0x000001, 1, 0)))
		out = append(out, box("moof", box("mfhd", be32(0, uint32(i+1))), traf)...)
		out = append(out, box("mdat", []byte(tag+"/"+strconv.Itoa(i)))...)
	}
	return out
}

// fakeStream is a StreamService over the real planner with a fixed probe,
// whose transcodes are fake fragmented MP4 files.
type fakeStream struct {
	in         video.TranscodeInput
	caps       video.TranscodeCaps
	prefetchOK bool // Prefetch starts (and finishes at once)

	probes   atomic.Int32
	mu       sync.Mutex
	parts    []StreamPart
	keys     []string
	prefetch []StreamPart
	bodies   []*refBuf
}

func newFakeStream() *fakeStream {
	return &fakeStream{
		in: video.TranscodeInput{
			Container: "matroska", Duration: 6.6, Video: true, Width: 640, Height: 360, FPS: 25,
			Audio: true, AudioDecodable: true, SampleRate: 48000, Channels: 2,
		},
		caps: video.TranscodeCaps{
			Video: video.EncoderSet{VP9: true, AV1: true}, AAC: true, MuxMP4: true, VideoFilters: true, AudioFilters: true,
		},
	}
}

func (f *fakeStream) ResolveStream(op *transform.VideoOp) (video.TranscodeOutput, error) {
	return f.caps.ResolveStream(op)
}

func (f *fakeStream) Probe(_ context.Context, src fetch.Buffer) (video.TranscodeInput, error) {
	f.probes.Add(1)
	if len(src.Bytes()) == 0 {
		return video.TranscodeInput{}, errs.New(errs.KindCorrupt, "empty")
	}
	return f.in, nil
}

func (f *fakeStream) Layout(in *video.TranscodeInput, op *transform.VideoOp) (*video.StreamLayout, error) {
	out, err := f.caps.ResolveStream(op)
	if err != nil {
		return nil, err
	}
	return video.PlanStream(in, op, out, f.caps, &video.TranscodeSettings{MaxFPS: 60},
		&video.StreamSettings{SegmentDuration: 2 * time.Second})
}

func (f *fakeStream) result(op *transform.VideoOp, part StreamPart) *VideoResult {
	l, err := f.Layout(&f.in, op)
	if err != nil {
		panic(err)
	}
	var data []byte
	if part.Track == "" {
		starts := make([]uint64, 0, l.Segments)
		for n := range l.Segments {
			starts = append(starts, uint64(n*96000))
		}
		data = fakeFMP4("audio", 48000, starts...)
	} else {
		data = fakeFMP4(part.Track+":"+strconv.Itoa(part.Segment), 90000, uint64(part.Segment)*180000) //nolint:gosec // G115: a segment number is not negative
	}
	b := &refBuf{b: data}
	b.refs.Store(1)
	f.mu.Lock()
	f.bodies = append(f.bodies, b)
	f.mu.Unlock()
	return &VideoResult{Body: b}
}

func (f *fakeStream) Stream(_ context.Context, key string, _ fetch.Buffer, op *transform.VideoOp, part StreamPart) (*VideoResult, error) {
	f.mu.Lock()
	f.parts = append(f.parts, part)
	f.keys = append(f.keys, key)
	f.mu.Unlock()
	return f.result(op, part), nil
}

func (f *fakeStream) Prefetch(_ string, _ fetch.Buffer, op *transform.VideoOp, part StreamPart, done func(*VideoResult)) bool {
	f.mu.Lock()
	f.prefetch = append(f.prefetch, part)
	f.mu.Unlock()
	if !f.prefetchOK {
		return false
	}
	done(f.result(op, part))
	return true
}

func (f *fakeStream) streamed() []StreamPart {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]StreamPart(nil), f.parts...)
}

func (f *fakeStream) checkBodies(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, b := range f.bodies {
		if n := b.refs.Load(); n != 0 {
			t.Errorf("stream body %d holds %d references", i, n)
		}
	}
}

func newStreamHarness(tb testing.TB, op *transform.VideoOp, mutate func(*harness)) (*harness, *fakeStream) {
	tb.Helper()
	fs := newFakeStream()
	sealer, err := ticket.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		tb.Fatal(err)
	}
	mem, err := memory.New(memory.Options{MaxBytes: 1 << 20})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = mem.Close() })
	h := newHarness(tb, func(h *harness) {
		h.video = &fakeVideo{out: mp4Output, body: videoFile}
		h.stream, h.tickets, h.streamCache = fs, sealer, mem
		h.router.sig = sign.Verified
		h.router.mutate = videoRequest(op)
		h.f.data = []byte("source video")
		h.cfg.Video = VideoConfig{Budget: time.Minute}
		h.cfg.Stream = StreamConfig{TTL: 4 * time.Hour, Prefetch: 2}
		h.cfg.DebugHeaders = true
		if mutate != nil {
			mutate(h)
		}
	})
	return h, fs
}

func hlsOp() *transform.VideoOp {
	return &transform.VideoOp{Stream: &transform.VideoStream{Renditions: []int{360, 240}}}
}

// uris lists a playlist's URIs (the lines that are not tags, and the URI
// attributes of tags).
func uris(pl string) []string {
	var out []string
	for line := range strings.SplitSeq(pl, "\n") {
		if _, after, ok := strings.Cut(line, `URI="`); ok {
			out = append(out, strings.TrimSuffix(after, `"`))
		} else if line != "" && line[0] != '#' {
			out = append(out, line)
		}
	}
	return out
}

func mustGet(t *testing.T, h *harness, target string, status int) *httpResult {
	t.Helper()
	rec := h.do(t, http.MethodGet, target, nil)
	if rec.Code != status {
		t.Fatalf("GET %s: %d, want %d: %s", target, rec.Code, status, rec.Body.String())
	}
	return &httpResult{header: rec.Header(), body: rec.Body.Bytes()}
}

type httpResult struct {
	header http.Header
	body   []byte
}

// TestStreamHLS walks an HLS stream as a player does: the master playlist
// from the dialect URL, its media playlists, init segments and media
// segments from the child URLs, each a part of the transcodes' output, the
// video init segment from segment 0's transcode, the audio from one
// transcode of the whole track, every part cached once produced.
func TestStreamHLS(t *testing.T) {
	t.Parallel()
	h, fs := newStreamHarness(t, hlsOp(), func(h *harness) { h.cfg.Stream.Prefetch = 0 })
	master := mustGet(t, h, videoPath, http.StatusOK)
	if ct := master.header.Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Fatalf("Content-Type %q", ct)
	}
	if cc := master.header.Get("Cache-Control"); cc != "public, max-age=3600" {
		t.Errorf("master Cache-Control %q, want a quarter of the TTL", cc)
	}
	list := uris(string(master.body))
	if len(list) != 3 || !strings.HasPrefix(list[0], StreamPath) || !strings.HasSuffix(list[0], "/a.m3u8") ||
		!strings.HasSuffix(list[1], "/v0.m3u8") || !strings.HasSuffix(list[2], "/v1.m3u8") {
		t.Fatalf("master playlist URIs %q:\n%s", list, master.body)
	}
	// The ticket carries only what the URL set (zero fields omitted).
	if tok := strings.Split(list[1], "/")[2]; len(tok) > 200 {
		t.Errorf("ticket of %d characters for a two-rendition stream: %s", len(tok), tok)
	}
	if fs.probes.Load() != 1 || len(fs.streamed()) != 0 {
		t.Fatalf("the master probed %d times and transcoded %v", fs.probes.Load(), fs.streamed())
	}
	for _, pl := range list {
		media := mustGet(t, h, pl, http.StatusOK)
		dir := pl[:strings.LastIndexByte(pl, '/')+1]
		var got []byte
		for i, u := range uris(string(media.body)) {
			part := mustGet(t, h, dir+u, http.StatusOK)
			got = append(got, part.body...)
			if i > 0 && part.header.Get("Content-Type") != "video/mp4" && part.header.Get("Content-Type") != "audio/mp4" {
				t.Errorf("%s: Content-Type %q", u, part.header.Get("Content-Type"))
			}
		}
		f, err := fmp4.Parse(got)
		if err != nil || len(f.Fragments) != 4 {
			t.Fatalf("%s: init and segments do not join into one file of 4 fragments: %v", pl, err)
		}
	}
	// One transcode per video segment (the init came with segment 0) and
	// one for the whole audio track; no probe after the master's.
	if parts := fs.streamed(); len(parts) != 9 || fs.probes.Load() != 1 {
		t.Fatalf("transcodes %v, probes %d", parts, fs.probes.Load())
	}
	// Everything again: from the stream cache.
	for _, pl := range list {
		dir := pl[:strings.LastIndexByte(pl, '/')+1]
		for i, u := range uris(string(mustGet(t, h, pl, http.StatusOK).body)) {
			if r := mustGet(t, h, dir+u, http.StatusOK); i > 0 && r.header.Get(hdrXiCache) != cacheHit {
				t.Errorf("%s: X-Xi-Cache %q", u, r.header.Get(hdrXiCache))
			}
		}
	}
	if parts := fs.streamed(); len(parts) != 9 {
		t.Fatalf("cached parts transcoded again: %v", parts)
	}
	h.checkReleased(t)
	_ = h.streamCache.Close() // drops the cached parts' references
	fs.checkBodies(t)
}

// TestStreamDASH: the MPD names its segments by template under the base.
func TestStreamDASH(t *testing.T) {
	t.Parallel()
	op := &transform.VideoOp{Mute: true, Stream: &transform.VideoStream{Protocol: transform.StreamDASH}}
	h, _ := newStreamHarness(t, op, nil)
	r := mustGet(t, h, videoPath, http.StatusOK)
	if ct := r.header.Get("Content-Type"); ct != "application/dash+xml" {
		t.Fatalf("Content-Type %q", ct)
	}
	var mpd struct {
		BaseURL string `xml:"BaseURL"`
		Sets    []struct {
			Template struct {
				Media string `xml:"media,attr"`
				Init  string `xml:"initialization,attr"`
			} `xml:"SegmentTemplate"`
			Reps []struct {
				ID string `xml:"id,attr"`
			} `xml:"Representation"`
		} `xml:"Period>AdaptationSet"`
	}
	if err := xml.Unmarshal(r.body, &mpd); err != nil {
		t.Fatal(err)
	}
	if len(mpd.Sets) != 1 || len(mpd.Sets[0].Reps) != 2 {
		t.Fatalf("MPD %+v", mpd)
	}
	tmpl := mpd.Sets[0].Template
	for _, rep := range mpd.Sets[0].Reps {
		mustGet(t, h, mpd.BaseURL+strings.ReplaceAll(tmpl.Init, "$RepresentationID$", rep.ID), http.StatusOK)
		seg := strings.ReplaceAll(strings.ReplaceAll(tmpl.Media, "$RepresentationID$", rep.ID), "$Number$", "3")
		mustGet(t, h, mpd.BaseURL+seg, http.StatusOK)
	}
	// The MPD under the ticket is the same document.
	if again := mustGet(t, h, mpd.BaseURL+"manifest.mpd", http.StatusOK); !bytes.Equal(again.body, r.body) {
		t.Fatal("manifest.mpd differs from the dialect URL's MPD")
	}
}

// streamBase returns the child URL base of the harness's stream.
func streamBase(t *testing.T, h *harness) string {
	t.Helper()
	u := uris(string(mustGet(t, h, videoPath, http.StatusOK).body))[1]
	return u[:strings.LastIndexByte(u, '/')+1]
}

// TestStreamChildErrors: a tampered or expired ticket, and paths a stream
// does not have.
func TestStreamChildErrors(t *testing.T) {
	t.Parallel()
	h, fs := newStreamHarness(t, hlsOp(), nil)
	base := streamBase(t, h)
	tok := strings.TrimSuffix(strings.TrimPrefix(base, StreamPath), "/")
	flip := func(i int) string {
		b := []byte(tok)
		if b[i] == 'A' {
			b[i] = 'B'
		} else {
			b[i] = 'A'
		}
		return StreamPath + string(b) + "/v0.m3u8"
	}
	tests := []struct {
		name, path string
		status     int
	}{
		{"tampered start", flip(2), http.StatusForbidden},
		{"tampered middle", flip(len(tok) / 2), http.StatusForbidden},
		{"tampered end", flip(len(tok) - 1), http.StatusForbidden},
		{"no ticket", StreamPath + "v0.m3u8", http.StatusNotFound},
		{"garbage ticket", StreamPath + "x/v0.m3u8", http.StatusForbidden},
		{"unknown rendition", base + "v7.m3u8", http.StatusNotFound},
		{"unknown rendition segment", base + "v7/0.m4s", http.StatusNotFound},
		{"segment past the end", base + "v0/4.m4s", http.StatusNotFound},
		{"audio past the end", base + "a/4.m4s", http.StatusNotFound},
		{"bad part", base + "v0/x.m4s", http.StatusNotFound},
		{"leading zero", base + "v0/01.m4s", http.StatusNotFound},
		{"master", base + "master.m3u8", http.StatusOK},
	}
	t.Cleanup(func() {
		if parts := fs.streamed(); len(parts) != 0 {
			t.Errorf("errors transcoded %v", parts)
		}
	})
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mustGet(t, h, tc.path, tc.status)
		})
	}
}

// TestStreamTicketExpiry: a stream's ticket works until its expiry (a 4h
// TTL from a quarter-TTL step), then answers like an expired URL.
func TestStreamTicketExpiry(t *testing.T) {
	t.Parallel()
	h, _ := newStreamHarness(t, hlsOp(), nil)
	base := streamBase(t, h)
	mustGet(t, h, base+"v0.m3u8", http.StatusOK)
	h.srv.now = func() time.Time { return time.Now().Add(5 * time.Hour) }
	mustGet(t, h, base+"v0.m3u8", http.StatusNotFound)
	h.checkReleased(t)
}

// TestStreamMasterPolicy: the dialect URL is a video transformation for
// signing, roles and the unsigned policy, and refuses what a stream cannot
// be before fetching.
func TestStreamMasterPolicy(t *testing.T) {
	t.Parallel()
	t.Run("unsigned", func(t *testing.T) {
		t.Parallel()
		h, _ := newStreamHarness(t, hlsOp(), func(h *harness) { h.router.sig = sign.Unsigned })
		mustGet(t, h, videoPath, http.StatusForbidden)
		if h.f.calls.Load() != 0 {
			t.Fatal("fetched before refusing")
		}
	})
	t.Run("image role", func(t *testing.T) {
		t.Parallel()
		h, _ := newStreamHarness(t, hlsOp(), func(h *harness) { h.cfg.Role = RoleImage })
		mustGet(t, h, videoPath, http.StatusNotFound)
		mustGet(t, h, StreamPath+"x/v0.m3u8", http.StatusNotFound)
	})
	t.Run("streams off", func(t *testing.T) {
		t.Parallel()
		h, _ := newStreamHarness(t, hlsOp(), func(h *harness) { h.stream = nil })
		mustGet(t, h, videoPath, http.StatusNotFound)
	})
	t.Run("h264 on this build", func(t *testing.T) {
		t.Parallel()
		op := hlsOp()
		op.VideoCodec = transform.VideoCodecH264
		h, _ := newStreamHarness(t, op, nil)
		mustGet(t, h, videoPath, http.StatusUnprocessableEntity)
		if h.f.calls.Load() != 0 {
			t.Fatal("fetched before refusing")
		}
	})
	t.Run("path prefix", func(t *testing.T) {
		t.Parallel()
		h, _ := newStreamHarness(t, hlsOp(), func(h *harness) { h.cfg.PathPrefix = "/img" })
		u := uris(string(mustGet(t, h, "/img"+videoPath, http.StatusOK).body))[1]
		if !strings.HasPrefix(u, "/img"+StreamPath) {
			t.Fatalf("child URL %q outside the prefix", u)
		}
		mustGet(t, h, u, http.StatusOK)
		mustGet(t, h, strings.TrimPrefix(u, "/img"), http.StatusNotFound)
	})
}

// TestStreamPrefetch: a segment transcoded on request prefetches the next
// ones of its rendition that are not cached, never past the end, and the
// prefetched ones are served from the cache.
func TestStreamPrefetch(t *testing.T) {
	t.Parallel()
	h, fs := newStreamHarness(t, hlsOp(), func(h *harness) { h.f.data = []byte("source video") })
	fs.prefetchOK = true
	base := streamBase(t, h)
	mustGet(t, h, base+"v1/1.m4s", http.StatusOK)
	fs.mu.Lock()
	got := append([]StreamPart(nil), fs.prefetch...)
	fs.mu.Unlock()
	if len(got) != 2 || got[0] != (StreamPart{Track: "v1", Segment: 2}) || got[1] != (StreamPart{Track: "v1", Segment: 3}) {
		t.Fatalf("prefetched %v", got)
	}
	for _, n := range []string{"2", "3"} {
		if r := mustGet(t, h, base+"v1/"+n+".m4s", http.StatusOK); r.header.Get(hdrXiCache) != cacheHit {
			t.Errorf("prefetched segment %s missed", n)
		}
	}
	// Segment 3 is the last: a miss there prefetches nothing.
	mustGet(t, h, base+"v0/3.m4s", http.StatusOK)
	fs.mu.Lock()
	n := len(fs.prefetch)
	fs.mu.Unlock()
	if n != 2 {
		t.Fatalf("prefetched past the end: %v", fs.prefetch)
	}
	h.checkReleased(t)
	_ = h.streamCache.Close() // drops the cached parts' references
	fs.checkBodies(t)
}

// TestStreamRange: segments answer byte ranges and conditional requests.
func TestStreamRange(t *testing.T) {
	t.Parallel()
	h, _ := newStreamHarness(t, hlsOp(), nil)
	base := streamBase(t, h)
	full := mustGet(t, h, base+"v0/0.m4s", http.StatusOK)
	rec := h.do(t, http.MethodGet, base+"v0/0.m4s", http.Header{"Range": {"bytes=0-7"}})
	if rec.Code != http.StatusPartialContent || !bytes.Equal(rec.Body.Bytes(), full.body[:8]) {
		t.Fatalf("range: %d %q", rec.Code, rec.Body.Bytes())
	}
	rec = h.do(t, http.MethodGet, base+"v0/0.m4s", http.Header{"If-None-Match": {full.header.Get("ETag")}})
	if rec.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match: %d", rec.Code)
	}
	rec = h.do(t, http.MethodHead, base+"v0/0.m4s", nil)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != strconv.Itoa(len(full.body)) {
		t.Fatalf("HEAD: %d, %d bytes, Content-Length %q", rec.Code, rec.Body.Len(), rec.Header().Get("Content-Length"))
	}
}

// TestStreamResultCache: parts survive in the result cache (another
// instance, or after the stream cache dropped them): no probe and no
// transcode.
func TestStreamResultCache(t *testing.T) {
	t.Parallel()
	mem, err := memory.New(memory.Options{MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	store, err := cache.NewAsync(mem, cache.AsyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	shared := func(h *harness) {
		h.cache = store
		h.cfg.ResultCache = ResultCacheConfig{TTL: time.Hour, Build: "test"}
	}
	h1, fs1 := newStreamHarness(t, hlsOp(), shared)
	base := streamBase(t, h1)
	first := mustGet(t, h1, base+"v0/2.m4s", http.StatusOK)
	store.Wait()

	h2, fs2 := newStreamHarness(t, hlsOp(), shared) // same ticket key, empty stream cache
	again := mustGet(t, h2, base+"v0/2.m4s", http.StatusOK)
	if !bytes.Equal(first.body, again.body) || fs2.probes.Load() != 0 || len(fs2.streamed()) != 0 {
		t.Fatalf("second instance probed %d times, transcoded %v", fs2.probes.Load(), fs2.streamed())
	}
	_ = fs1
}
