package manifest

import (
	"bytes"
	"encoding/xml"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
)

var update = flag.Bool("update", false, "rewrite testdata/*")

const base = "/_stream/TICKET/"

// layouts are planned from probes, as the server does.
func layouts(t *testing.T) map[string]*video.StreamLayout {
	t.Helper()
	caps := video.TranscodeCaps{
		Video: video.EncoderSet{VP9: true, AV1: true, H264: true}, AAC: true, MuxMP4: true,
		VideoFilters: true, AudioFilters: true,
	}
	hd := &video.TranscodeInput{
		Container: "mov", Duration: 61.5, StartTime: 0.1, Video: true, Width: 1280, Height: 720, FPS: 30000.0 / 1001,
		Audio: true, AudioDecodable: true, SampleRate: 44100, Channels: 2,
	}
	clip := &video.TranscodeInput{Container: "matroska", Duration: 6.6, Video: true, Width: 640, Height: 360, FPS: 25}
	s := &video.TranscodeSettings{MaxOutputPixels: 3840 * 2160, MaxFPS: 60}
	out := map[string]*video.StreamLayout{}
	for name, c := range map[string]struct {
		in *video.TranscodeInput
		op transform.VideoOp
	}{
		"hd-h264-aac": {hd, transform.VideoOp{Stream: &transform.VideoStream{}}},
		"hd-dash":     {hd, transform.VideoOp{Stream: &transform.VideoStream{Protocol: transform.StreamDASH, Renditions: []int{720, 360}}}},
		"clip-av1-muted": {clip, transform.VideoOp{
			VideoCodec: transform.VideoCodecAV1, Stream: &transform.VideoStream{SegmentDuration: 2 * time.Second},
		}},
	} {
		o, err := caps.ResolveStream(&c.op)
		if err != nil {
			t.Fatal(err)
		}
		l, err := video.PlanStream(c.in, &c.op, o, caps, s, &video.StreamSettings{})
		if err != nil {
			t.Fatal(err)
		}
		out[name] = l
	}
	return out
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Fatalf("%s (-want +got):\n%s", name, diff)
	}
}

// TestManifests: the playlists and MPDs of a few layouts (golden files).
func TestManifests(t *testing.T) {
	t.Parallel()
	for name, l := range layouts(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			golden(t, name+".m3u8", HLSMaster(l, base))
			for _, track := range append(tracks(l), Audio) {
				pl, ok := HLSMedia(l, track)
				if ok != (track != Audio || l.Audio != nil) {
					t.Fatalf("track %s: ok %v", track, ok)
				}
				if ok && track != "v2" && track != "v3" { // keep the goldens short
					golden(t, name+"."+track+".m3u8", pl)
				}
			}
			mpd := DASH(l, base)
			golden(t, name+".mpd", mpd)
			var doc struct {
				XMLName xml.Name `xml:"MPD"`
			}
			if err := xml.Unmarshal(mpd, &doc); err != nil {
				t.Fatalf("MPD is not XML: %v", err)
			}
		})
	}
}

func tracks(l *video.StreamLayout) []string {
	var out []string
	for _, r := range l.Video {
		out = append(out, r.Name)
	}
	return out
}

// TestHLSMediaRules checks the HLS rules every media playlist must keep:
// each EXTINF rounds to at most the target duration, they add up to the
// stream's duration, and every listed path parses back to its part.
func TestHLSMediaRules(t *testing.T) {
	t.Parallel()
	for name, l := range layouts(t) {
		for _, track := range append(tracks(l), Audio) {
			pl, ok := HLSMedia(l, track)
			if !ok {
				continue
			}
			var target, total float64
			n := 0
			for line := range strings.SplitSeq(string(pl), "\n") {
				switch {
				case strings.HasPrefix(line, "#EXT-X-TARGETDURATION:"):
					target = mustFloat(t, strings.TrimPrefix(line, "#EXT-X-TARGETDURATION:"))
				case strings.HasPrefix(line, "#EXTINF:"):
					d := mustFloat(t, strings.TrimSuffix(strings.TrimPrefix(line, "#EXTINF:"), ","))
					if d < 0.0005 || float64(int(d+0.5)) > target {
						t.Errorf("%s %s: EXTINF %v with target %v", name, track, d, target)
					}
					total += d
				case line != "" && line[0] != '#':
					p, err := ParsePart(line)
					if err != nil || p.Kind != PartSegment || p.Track != track || p.Segment != n {
						t.Errorf("%s %s: %q is %+v %v", name, track, line, p, err)
					}
					n++
				}
			}
			if n != l.Segments || total < l.Duration-0.05 || total > l.Duration+0.05 {
				t.Errorf("%s %s: %d segments of %.4f s, want %d of %.4f", name, track, n, total, l.Segments, l.Duration)
			}
		}
	}
}

func mustFloat(t *testing.T, s string) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("%q: %v", s, err)
	}
	return f
}

func TestParsePart(t *testing.T) {
	t.Parallel()
	good := map[string]Part{
		"master.m3u8":  {Kind: PartMaster},
		"manifest.mpd": {Kind: PartMPD},
		"v0.m3u8":      {Kind: PartPlaylist, Track: "v0"},
		"a.m3u8":       {Kind: PartPlaylist, Track: "a"},
		"v12/init.mp4": {Kind: PartInit, Track: "v12"},
		"a/init.mp4":   {Kind: PartInit, Track: "a"},
		"v3/0.m4s":     {Kind: PartSegment, Track: "v3"},
		"a/86400.m4s":  {Kind: PartSegment, Track: "a", Segment: 86400},
	}
	for p, want := range good {
		got, err := ParsePart(p)
		if err != nil || got != want {
			t.Errorf("ParsePart(%q) = %+v, %v", p, got, err)
		}
		if got.Path() != p {
			t.Errorf("%+v.Path() = %q, want %q", got, got.Path(), p)
		}
	}
	for _, p := range []string{
		"", "v.m3u8", "v01.m3u8", "v123.m3u8", "x0.m3u8", "v0/init.mp", "v0/01.m4s", "v0/-1.m4s", "v0/86401.m4s",
		"v0/1.m4s/x", "../v0.m3u8", "v0/.m4s", "a/b/1.m4s", "v0/1e3.m4s", "master.m3u", "v0/+1.m4s",
	} {
		if got, err := ParsePart(p); err == nil {
			t.Errorf("ParsePart(%q) = %+v", p, got)
		}
	}
}

func FuzzParsePart(f *testing.F) {
	for _, s := range []string{"master.m3u8", "v0.m3u8", "a/init.mp4", "v7/123.m4s", "v0/01.m4s"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		p, err := ParsePart(s)
		if err == nil && p.Path() != s {
			t.Fatalf("ParsePart(%q).Path() = %q", s, p.Path())
		}
	})
}

// TestMasterNamesEverything: the master playlist names each media
// playlist under base, with the codecs of its rendition and the audio.
func TestMasterNamesEverything(t *testing.T) {
	t.Parallel()
	l := layouts(t)["hd-h264-aac"]
	m := HLSMaster(l, base)
	for _, r := range l.Video {
		if !bytes.Contains(m, []byte(base+r.Name+".m3u8")) || !bytes.Contains(m, []byte(`CODECS="`+r.Codecs+`,mp4a.40.2"`)) {
			t.Fatalf("master playlist lacks %s:\n%s", r.Name, m)
		}
	}
}
