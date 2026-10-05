//go:build acceptance

package acceptance

import (
	"bytes"
	"net/http"
	"os"
	"strings"
	"testing"
)

// TestVideoStream: HLS and DASH (WP-126) against the image under test, on
// images with FFmpeg (XI_ACC_VIDEO=true): a signed master playlist, its
// media playlists and segments under the ticketed child URLs (no
// signature), a DASH MPD, and a tampered ticket refused. On slim the
// manifest URL answers 404 like every video URL.
func TestVideoStream(t *testing.T) {
	t.Parallel()
	app := base(t, "XI_ACC_APP")
	k := loadKeys(t)
	video := func(rest string) string {
		signed := k.sign("/video" + rest)
		sig, _, _ := strings.Cut(strings.TrimPrefix(signed, "/"), "/")
		return app + "/video/" + sig + rest
	}
	const src = "/plain/local:///video/stream-vp9-opus.webm"
	if os.Getenv("XI_ACC_VIDEO") != "true" {
		get(t, video("/st:hls"+src+"@m3u8"), nil).wantStatus(t, http.StatusNotFound)
		return
	}
	lines := func(b []byte) []string {
		var out []string
		for l := range strings.SplitSeq(string(b), "\n") {
			if _, after, ok := strings.Cut(l, `URI="`); ok {
				out = append(out, strings.TrimSuffix(after, `"`))
			} else if l != "" && l[0] != '#' {
				out = append(out, l)
			}
		}
		return out
	}

	master := get(t, video("/st:hls:360:240/sgd:2"+src+"@m3u8"), nil)
	master.wantStatus(t, http.StatusOK)
	if ct := master.Header.Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Fatalf("master Content-Type %q", ct)
	}
	pls := lines(master.Body)
	if len(pls) != 3 {
		t.Fatalf("master playlist:\n%s", master.Body)
	}
	for _, pl := range pls {
		media := get(t, app+pl, nil)
		media.wantStatus(t, http.StatusOK)
		dir := pl[:strings.LastIndexByte(pl, '/')+1]
		parts := lines(media.Body)
		if len(parts) != 5 { // the init segment and four 2 s segments of 6.6 s
			t.Fatalf("%s:\n%s", pl, media.Body)
		}
		for _, p := range []string{parts[0], parts[2]} {
			r := get(t, app+dir+p, nil)
			r.wantStatus(t, http.StatusOK)
			if !bytes.Contains(r.Body[:min(len(r.Body), 64)], []byte("ftyp")) && !bytes.Contains(r.Body[:min(len(r.Body), 64)], []byte("moof")) {
				t.Errorf("%s starts % x", p, r.Body[:min(len(r.Body), 16)])
			}
		}
	}

	mpd := get(t, video("/st:dash:240"+src+"@mpd"), nil)
	mpd.wantStatus(t, http.StatusOK)
	if ct := mpd.Header.Get("Content-Type"); ct != "application/dash+xml" || !bytes.Contains(mpd.Body, []byte("$Number$")) {
		t.Fatalf("mpd %q:\n%s", ct, mpd.Body)
	}

	seg := pls[1][:strings.LastIndexByte(pls[1], '/')+1] + "v0/1.m4s"
	bad := []byte(seg)
	bad[len("/_stream/")+3] ^= 1
	get(t, app+string(bad), nil).wantStatus(t, http.StatusForbidden)
	get(t, app+"/video/unsafe/st:hls"+src+"@m3u8", nil).wantStatus(t, http.StatusForbidden)
}
