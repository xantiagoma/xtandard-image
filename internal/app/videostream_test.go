//go:build cgo

package app

import (
	"bytes"
	"cmp"
	"encoding/json"
	"encoding/xml"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
)

// probePacket is one packet ffprobe reads.
type probePacket struct {
	pts, dur float64
	key      bool
}

// ffprobePackets lists the packets of data's first stream, in
// presentation order.
func ffprobePackets(t *testing.T, data []byte) []probePacket {
	t.Helper()
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("no ffprobe")
	}
	f := filepath.Join(t.TempDir(), "joined.mp4")
	if err := os.WriteFile(f, data, 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := exec.CommandContext(t.Context(), bin, "-v", "error", "-show_entries", "packet=pts_time,duration_time,flags",
		"-of", "json", f).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	var out struct {
		Packets []struct {
			PTS   string `json:"pts_time"`
			Dur   string `json:"duration_time"`
			Flags string `json:"flags"`
		} `json:"packets"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	ps := make([]probePacket, 0, len(out.Packets))
	for _, p := range out.Packets {
		pts, err1 := strconv.ParseFloat(p.PTS, 64)
		dur, err2 := strconv.ParseFloat(p.Dur, 64)
		if err1 != nil || err2 != nil {
			t.Fatalf("packet %+v", p)
		}
		ps = append(ps, probePacket{pts: pts, dur: dur, key: strings.HasPrefix(p.Flags, "K")})
	}
	slices.SortFunc(ps, func(a, b probePacket) int { return cmp.Compare(a.pts, b.pts) })
	return ps
}

// playlistURIs lists a playlist's URIs (lines that are not tags, and the
// URI attributes of tags).
func playlistURIs(pl []byte) []string {
	var out []string
	for line := range strings.SplitSeq(string(pl), "\n") {
		if _, after, ok := strings.Cut(line, `URI="`); ok {
			out = append(out, strings.TrimSuffix(after, `"`))
		} else if line != "" && line[0] != '#' {
			out = append(out, line)
		}
	}
	return out
}

// TestStreamEndToEnd plays a signed HLS URL through the real router,
// server, media service and FFmpeg the way a player does (master, media
// playlists, init and media segments), stores the files in a directory
// tree as a player's cache would, and checks that the segments of every
// rendition, transcoded on their own, join into one video with the
// source's 165 frames on the 25 fps grid, a keyframe at every segment,
// and that the audio track joins into the stream's length, in sync with
// the video. Then DASH, ranges, signing and tickets.
func TestStreamEndToEnd(t *testing.T) {
	if !ffmpeg.Available() {
		t.Skip("this image has no FFmpeg")
	}
	cfg := videoConfig(t, config.RoleAll, true)
	cfg.Video.Transform.UnsignedPolicy = config.Defaults().Video.Transform.UnsignedPolicy
	cfg.Video.Transform.SegmentDuration = 2 * time.Second
	a := startVideo(t, cfg)
	base := "http://" + a.public.String()
	s := newVideoSigner(t)
	const src = "/plain/video/stream-vp9-opus.webm"

	master := get(t, base+s.video(t, "/st:hls:360:240"+src+"@m3u8"))
	if master.status != http.StatusOK || master.ctype != "application/vnd.apple.mpegurl" {
		t.Fatalf("master: %d %q %s", master.status, master.ctype, master.body)
	}
	if again := get(t, base+s.video(t, "/st:hls:360:240"+src+"@m3u8")); !bytes.Equal(again.body, master.body) {
		t.Fatal("the master playlist changed between two requests")
	}
	dir := t.TempDir()
	tracks := map[string][]byte{} // track playlist URI → its init and segments joined
	for _, pl := range playlistURIs(master.body) {
		media := get(t, base+pl)
		if media.status != http.StatusOK {
			t.Fatalf("%s: %d %s", pl, media.status, media.body)
		}
		writeTree(t, dir, pl, media.body)
		prefix := pl[:strings.LastIndexByte(pl, '/')+1]
		var joined []byte
		for _, u := range playlistURIs(media.body) {
			r := get(t, base+prefix+u)
			if r.status != http.StatusOK {
				t.Fatalf("%s: %d %s", u, r.status, r.body)
			}
			writeTree(t, dir, prefix+u, r.body)
			joined = append(joined, r.body...)
		}
		tracks[pl] = joined
	}
	writeTree(t, dir, "/master.m3u8", master.body)
	t.Logf("stream written to %s", dir)
	if len(tracks) != 3 {
		t.Fatalf("%d tracks: %s", len(tracks), master.body)
	}

	var audioStart float64
	var videoStarts []float64
	for pl, joined := range tracks {
		pk := ffprobePackets(t, joined)
		if strings.HasSuffix(pl, "/a.m3u8") {
			last := pk[len(pk)-1]
			if got := last.pts + last.dur - pk[0].pts; math.Abs(got-(6.6+1024.0/48000)) > 0.03 {
				t.Errorf("audio of %.4f s, want 6.6 s and the priming", got)
			}
			for i := 1; i < len(pk); i++ {
				if math.Abs(pk[i].pts-pk[i-1].pts-pk[i-1].dur) > 1e-5 { // ffprobe rounds to microseconds
					t.Fatalf("audio gap or overlap at packet %d (%.6f after %.6f+%.6f)", i, pk[i].pts, pk[i-1].pts, pk[i-1].dur)
				}
			}
			audioStart = pk[0].pts
			continue
		}
		if len(pk) != 165 {
			t.Fatalf("%s: %d frames, want 165", pl, len(pk))
		}
		for i, p := range pk {
			if want := pk[0].pts + float64(i)/25; math.Abs(p.pts-want) > 1e-4 {
				t.Fatalf("%s: frame %d at %.5f, want %.5f", pl, i, p.pts, want)
			}
			if i%50 == 0 && !p.key {
				t.Fatalf("%s: frame %d starts a segment without a keyframe", pl, i)
			}
		}
		videoStarts = append(videoStarts, pk[0].pts)
	}
	// The priming packet comes 1024 samples before the source's first
	// sample, which plays with the first frame.
	for _, v := range videoStarts {
		if math.Abs(v-audioStart-1024.0/48000) > 1e-3 {
			t.Errorf("video starts at %.5f, audio at %.5f: out of sync", v, audioStart)
		}
	}

	// DASH: the MPD of the same source, its first rendition's segment 1.
	mpdResp := get(t, base+s.video(t, "/st:dash:240"+src+"@mpd"))
	var mpd struct {
		BaseURL string `xml:"BaseURL"`
	}
	if mpdResp.status != http.StatusOK || xml.Unmarshal(mpdResp.body, &mpd) != nil || mpd.BaseURL == "" {
		t.Fatalf("mpd: %d %s", mpdResp.status, mpdResp.body)
	}
	for _, p := range []string{"v0/init.mp4", "v0/1.m4s", "a/init.mp4", "a/2.m4s"} {
		if r := get(t, base+mpd.BaseURL+p); r.status != http.StatusOK {
			t.Fatalf("dash %s: %d %s", p, r.status, r.body)
		}
	}

	// Ranges, signing and tickets.
	media := playlistURIs(master.body)[1]
	seg := media[:strings.LastIndexByte(media, '/')+1] + "v0/2.m4s"
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+seg, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-15")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("range: %d", resp.StatusCode)
	}
	tampered := []byte(seg)
	tampered[len("/_stream/")+5] ^= 1
	for _, tc := range []struct {
		name, url string
		status    int
	}{
		{"unsigned master", "/video/unsafe/st:hls" + src + "@m3u8", http.StatusForbidden},
		{"tampered ticket", string(tampered), http.StatusForbidden},
		{"segment past the end", strings.Replace(seg, "/2.m4s", "/4.m4s", 1), http.StatusNotFound},
		{"rendition the stream lacks", strings.Replace(seg, "v0/2.m4s", "v5/0.m4s", 1), http.StatusNotFound},
		{"size on a stream", s.video(t, "/w:320"+src+"@m3u8"), http.StatusBadRequest},
	} {
		if r := get(t, base+tc.url); r.status != tc.status {
			t.Errorf("%s: %d, want %d: %s", tc.name, r.status, tc.status, r.body)
		}
	}
}

// ffprobeTypedPackets lists the packets of data by codec type ("video",
// "audio"), each in presentation order.
func ffprobeTypedPackets(t *testing.T, data []byte, ext string) map[string][]probePacket {
	t.Helper()
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("no ffprobe")
	}
	f := filepath.Join(t.TempDir(), "joined."+ext)
	if err := os.WriteFile(f, data, 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := exec.CommandContext(t.Context(), bin, "-v", "error", "-show_entries",
		"packet=codec_type,pts_time,duration_time,flags", "-of", "json", f).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	var out struct {
		Packets []struct {
			Type  string `json:"codec_type"`
			PTS   string `json:"pts_time"`
			Dur   string `json:"duration_time"`
			Flags string `json:"flags"`
		} `json:"packets"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	ps := map[string][]probePacket{}
	for _, p := range out.Packets {
		pts, err1 := strconv.ParseFloat(p.PTS, 64)
		dur, err2 := strconv.ParseFloat(p.Dur, 64)
		if err1 != nil || err2 != nil {
			t.Fatalf("packet %+v", p)
		}
		ps[p.Type] = append(ps[p.Type], probePacket{pts: pts, dur: dur, key: strings.HasPrefix(p.Flags, "K")})
	}
	for _, list := range ps {
		slices.SortFunc(list, func(a, b probePacket) int { return cmp.Compare(a.pts, b.pts) })
	}
	return ps
}

// TestStreamTSEndToEnd plays a signed HLS URL with MPEG-TS segments
// (WP-131) as a legacy player does: version 3 playlists without an audio
// group, .ts segments that each carry their audio. The segments of every
// rendition join into one transport stream with the source's 165 frames
// on the 25 fps grid, a keyframe at every segment, continuous audio over
// the stream's length and in sync with the video. Images without libx264
// answer 422.
func TestStreamTSEndToEnd(t *testing.T) {
	if !ffmpeg.Available() {
		t.Skip("this image has no FFmpeg")
	}
	cfg := videoConfig(t, config.RoleAll, true)
	cfg.Video.Transform.SegmentDuration = 2 * time.Second
	a := startVideo(t, cfg)
	base := "http://" + a.public.String()
	s := newVideoSigner(t)
	const src = "/plain/video/stream-vp9-opus.webm"
	master := get(t, base+s.video(t, "/st:hls:360:240/sgf:ts"+src+"@m3u8"))
	if !ffmpeg.TranscodeCaps().Video.H264 {
		if master.status != http.StatusUnprocessableEntity {
			t.Fatalf("mpeg-ts without libx264: %d %s", master.status, master.body)
		}
		return
	}
	if master.status != http.StatusOK || !bytes.Contains(master.body, []byte("#EXT-X-VERSION:3")) ||
		bytes.Contains(master.body, []byte("EXT-X-MEDIA")) {
		t.Fatalf("master: %d %s", master.status, master.body)
	}
	pls := playlistURIs(master.body)
	if len(pls) != 2 {
		t.Fatalf("playlists %q", pls)
	}
	for _, pl := range pls {
		media := get(t, base+pl)
		if media.status != http.StatusOK || bytes.Contains(media.body, []byte("EXT-X-MAP")) {
			t.Fatalf("%s: %d %s", pl, media.status, media.body)
		}
		prefix := pl[:strings.LastIndexByte(pl, '/')+1]
		var joined []byte
		for _, u := range playlistURIs(media.body) {
			r := get(t, base+prefix+u)
			if r.status != http.StatusOK || r.ctype != "video/mp2t" || !strings.HasSuffix(u, ".ts") {
				t.Fatalf("%s: %d %q %s", u, r.status, r.ctype, r.body)
			}
			joined = append(joined, r.body...)
		}
		pk := ffprobeTypedPackets(t, joined, "ts")
		v, au := pk["video"], pk["audio"]
		if len(v) != 165 || len(au) < 300 {
			t.Fatalf("%s: %d frames, %d audio packets", pl, len(v), len(au))
		}
		for i, p := range v {
			if want := v[0].pts + float64(i)/25; math.Abs(p.pts-want) > 1e-4 {
				t.Fatalf("%s: frame %d at %.5f, want %.5f", pl, i, p.pts, want)
			}
			if i%50 == 0 && !p.key {
				t.Fatalf("%s: frame %d starts a segment without a keyframe", pl, i)
			}
		}
		for i := 1; i < len(au); i++ {
			if math.Abs(au[i].pts-au[i-1].pts-au[i-1].dur) > 2e-5 {
				t.Fatalf("%s: audio gap or overlap at packet %d (%.6f after %.6f+%.6f)", pl, i, au[i].pts, au[i-1].pts, au[i-1].dur)
			}
		}
		if got := au[len(au)-1].pts + au[len(au)-1].dur - au[0].pts; math.Abs(got-(6.6+1024.0/48000)) > 0.03 {
			t.Errorf("%s: audio of %.4f s, want 6.6 s and the priming", pl, got)
		}
		if math.Abs(v[0].pts-au[0].pts-1024.0/48000) > 1e-3 {
			t.Errorf("%s: video starts at %.5f, audio at %.5f: out of sync", pl, v[0].pts, au[0].pts)
		}
	}
	// A DASH stream cannot have MPEG-TS segments.
	if r := get(t, base+s.video(t, "/sgf:ts"+src+"@mpd")); r.status != http.StatusBadRequest {
		t.Fatalf("dash with ts: %d %s", r.status, r.body)
	}
}

// TestStreamAudioChunksEndToEnd: with XI_VIDEO_TRANSFORM_AUDIO_CHUNK, a
// long track's audio segments are transcoded chunk by chunk (WP-131); the
// segments a player fetches join into gapless audio of the stream's
// length, as with one encode, for HLS and DASH alike.
func TestStreamAudioChunksEndToEnd(t *testing.T) {
	if !ffmpeg.Available() {
		t.Skip("this image has no FFmpeg")
	}
	cfg := videoConfig(t, config.RoleAll, true)
	cfg.Video.Transform.SegmentDuration = time.Second
	cfg.Video.Transform.AudioChunk = 2 * time.Second
	a := startVideo(t, cfg)
	base := "http://" + a.public.String()
	s := newVideoSigner(t)
	const src = "/plain/video/stream-vp9-aac44k.mp4"
	master := get(t, base+s.video(t, "/st:hls:240"+src+"@m3u8"))
	if master.status != http.StatusOK {
		t.Fatalf("master: %d %s", master.status, master.body)
	}
	var audio string
	for _, pl := range playlistURIs(master.body) {
		if strings.HasSuffix(pl, "/a.m3u8") {
			audio = pl
		}
	}
	media := get(t, base+audio)
	prefix := audio[:strings.LastIndexByte(audio, '/')+1]
	uris := playlistURIs(media.body)
	if len(uris) != 8 { // init and 7 segments
		t.Fatalf("audio playlist %s", media.body)
	}
	joined := make([]byte, 0, 64<<10)
	// Fetched out of order first, as a player seeking would: the last
	// chunk before the others.
	for _, i := range []int{0, 7, 1, 2, 3, 4, 5, 6} {
		if r := get(t, base+prefix+uris[i]); r.status != http.StatusOK {
			t.Fatalf("%s: %d %s", uris[i], r.status, r.body)
		}
	}
	for _, u := range uris {
		joined = append(joined, get(t, base+prefix+u).body...)
	}
	pk := ffprobePackets(t, joined)
	for i := 1; i < len(pk); i++ {
		if math.Abs(pk[i].pts-pk[i-1].pts-pk[i-1].dur) > 1e-5 {
			t.Fatalf("audio gap or overlap at packet %d (%.6f after %.6f+%.6f)", i, pk[i].pts, pk[i-1].pts, pk[i-1].dur)
		}
	}
	if got := pk[len(pk)-1].pts + pk[len(pk)-1].dur - pk[0].pts; math.Abs(got-(6.6+1024.0/48000)) > 0.03 {
		t.Errorf("audio of %.4f s, want 6.6 s and the priming", got)
	}

	// MPEG-TS segments copy the chunks' audio: still gapless.
	if !ffmpeg.TranscodeCaps().Video.H264 {
		return
	}
	ts := get(t, base+s.video(t, "/st:hls:240/sgf:ts"+src+"@m3u8"))
	pl := playlistURIs(ts.body)[0]
	tsMedia := get(t, base+pl)
	var tsJoined []byte
	for _, u := range playlistURIs(tsMedia.body) {
		r := get(t, base+pl[:strings.LastIndexByte(pl, '/')+1]+u)
		if r.status != http.StatusOK {
			t.Fatalf("%s: %d %s", u, r.status, r.body)
		}
		tsJoined = append(tsJoined, r.body...)
	}
	au := ffprobeTypedPackets(t, tsJoined, "ts")["audio"]
	for i := 1; i < len(au); i++ {
		if math.Abs(au[i].pts-au[i-1].pts-au[i-1].dur) > 2e-5 {
			t.Fatalf("ts audio gap or overlap at packet %d (%.6f after %.6f+%.6f)", i, au[i].pts, au[i-1].pts, au[i-1].dur)
		}
	}
	if len(au) != len(pk) {
		t.Errorf("ts segments carry %d audio packets, the audio playlist %d", len(au), len(pk))
	}
}

// writeTree stores body at its URL path under dir.

func writeTree(t *testing.T, dir, urlPath string, body []byte) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(urlPath, "/")))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
}
