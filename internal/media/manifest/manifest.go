// Package manifest writes the HLS playlists and the DASH MPD of a stream
// (WP-126, ADR 0012 §6) from its layout (internal/video.StreamLayout),
// which the probe alone decides, and names the parts of a stream under its
// child URL base:
//
//	<base>master.m3u8, <base>manifest.mpd    the HLS master playlist, the MPD
//	<base>v<i>.m3u8, <base>a.m3u8            media playlists (video i, audio)
//	<base>v<i>/init.mp4, <base>a/init.mp4    init segments
//	<base>v<i>/<n>.m4s, <base>a/<n>.m4s      media segments (n from 0)
//
// base is an absolute path ending in "/" ("/_stream/<ticket>/"); the
// master playlist and the MPD name everything from it, the media
// playlists relative to themselves.
//
// HLS: version 7, CMAF (fMP4) segments with EXT-X-MAP, VOD playlists,
// EXT-X-INDEPENDENT-SEGMENTS, one EXT-X-STREAM-INF per video rendition
// (BANDWIDTH, CODECS, RESOLUTION, FRAME-RATE) and the audio as an
// EXT-X-MEDIA rendition group. DASH: a static MPD of the live profile
// (isoff-live: one SegmentTemplate with $Number$ per adaptation set), its
// presentationTimeOffset the stream's offset, so media time maps onto the
// period's timeline exactly.
package manifest

import (
	"encoding/xml"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/video"
)

// Content types.
const (
	ContentTypeHLS  = "application/vnd.apple.mpegurl"
	ContentTypeDASH = "application/dash+xml"
	ContentTypeMP4  = "video/mp4"
	ContentTypeM4A  = "audio/mp4"
)

// Audio is the audio track's name in child paths.
const Audio = "a"

// PartKind is what a child path names.
type PartKind uint8

// PartKind values.
const (
	PartMaster   PartKind = iota // master.m3u8
	PartMPD                      // manifest.mpd
	PartPlaylist                 // <track>.m3u8
	PartInit                     // <track>/init.mp4
	PartSegment                  // <track>/<n>.m4s
)

// Part is one file of a stream.
type Part struct {
	Kind PartKind
	// Track is "v0"… or Audio; "" for the master playlist and the MPD.
	Track string
	// Segment is the media segment's number (PartSegment).
	Segment int
}

// ErrNotFound is the error of a path that names no part.
var ErrNotFound = errors.New("manifest: no such part")

// maxSegment bounds a segment number in a path (MaxVideoTime / 1 s).
const maxSegment = 86400

// ParsePart parses a path relative to a stream's base.
func ParsePart(p string) (Part, error) {
	switch p {
	case "master.m3u8":
		return Part{Kind: PartMaster}, nil
	case "manifest.mpd":
		return Part{Kind: PartMPD}, nil
	}
	if track, ok := strings.CutSuffix(p, ".m3u8"); ok && validTrack(track) {
		return Part{Kind: PartPlaylist, Track: track}, nil
	}
	track, file, ok := strings.Cut(p, "/")
	if !ok || !validTrack(track) {
		return Part{}, ErrNotFound
	}
	if file == "init.mp4" {
		return Part{Kind: PartInit, Track: track}, nil
	}
	num, ok := strings.CutSuffix(file, ".m4s")
	if !ok || !decimal(num, 5) {
		return Part{}, ErrNotFound
	}
	n, err := strconv.Atoi(num)
	if err != nil || n > maxSegment {
		return Part{}, ErrNotFound
	}
	return Part{Kind: PartSegment, Track: track, Segment: n}, nil
}

// validTrack reports "a" or "v" followed by a decimal index without
// leading zeros.
func validTrack(t string) bool {
	if t == Audio {
		return true
	}
	n, ok := strings.CutPrefix(t, "v")
	return ok && decimal(n, 2)
}

// decimal reports 1 to maxLen ASCII digits without a leading zero.
func decimal(s string, maxLen int) bool {
	if s == "" || len(s) > maxLen || (len(s) > 1 && s[0] == '0') {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Path is p's path relative to the stream's base.
func (p Part) Path() string {
	switch p.Kind {
	case PartMaster:
		return "master.m3u8"
	case PartMPD:
		return "manifest.mpd"
	case PartPlaylist:
		return p.Track + ".m3u8"
	case PartInit:
		return p.Track + "/init.mp4"
	default:
		return p.Track + "/" + strconv.Itoa(p.Segment) + ".m4s"
	}
}

// HLSMaster writes the master playlist of l, its media playlists under
// base.
func HLSMaster(l *video.StreamLayout, base string) []byte {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	codecs := func(r *video.Rendition) string { return r.Codecs }
	if a := l.Audio; a != nil {
		b.WriteString(`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="audio",DEFAULT=YES,AUTOSELECT=YES,CHANNELS="` +
			strconv.Itoa(a.Channels) + `",URI="` + base + Audio + `.m3u8"` + "\n")
		codecs = func(r *video.Rendition) string { return r.Codecs + "," + a.Codecs }
	}
	for i := range l.Video {
		r := &l.Video[i]
		b.WriteString("#EXT-X-STREAM-INF:BANDWIDTH=" + strconv.FormatInt(l.Bandwidth(r), 10) +
			`,CODECS="` + codecs(r) + `",RESOLUTION=` + strconv.Itoa(r.Width) + "x" + strconv.Itoa(r.Height) +
			",FRAME-RATE=" + strconv.FormatFloat(r.FPS(), 'f', 3, 64))
		if l.Audio != nil {
			b.WriteString(`,AUDIO="audio"`)
		}
		b.WriteString("\n" + base + r.Name + ".m3u8\n")
	}
	return []byte(b.String())
}

// HLSMedia writes the media playlist of track (a rendition's name or
// Audio), its init and media segments relative to it. ok is false for a
// track l does not have.
func HLSMedia(l *video.StreamLayout, track string) ([]byte, bool) {
	durations, ok := segmentDurations(l, track)
	if !ok {
		return nil, false
	}
	target := 1
	for _, d := range durations {
		target = max(target, int(math.Round(d)))
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:" + strconv.Itoa(target) +
		"\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	b.WriteString(`#EXT-X-MAP:URI="` + track + `/init.mp4"` + "\n")
	for n, d := range durations {
		b.WriteString("#EXTINF:" + strconv.FormatFloat(d, 'f', 6, 64) + ",\n" + track + "/" + strconv.Itoa(n) + ".m4s\n")
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return []byte(b.String()), true
}

// segmentDurations lists the segments' durations of track: a video
// rendition's are its frames on the grid (whole frames from the first
// grid point at or after the segment's start), the audio's the grid's.
func segmentDurations(l *video.StreamLayout, track string) ([]float64, bool) {
	out := make([]float64, l.Segments)
	if track == Audio {
		if l.Audio == nil {
			return nil, false
		}
		for n := range out {
			out[n] = l.SegmentDuration(n)
		}
		return out, true
	}
	r, ok := l.Rendition(track)
	if !ok {
		return nil, false
	}
	frame := func(t float64) int64 { return int64(math.Ceil(t*float64(r.FPSNum)/float64(r.FPSDen) - 1e-9)) }
	for n := range out {
		k0, k1 := frame(l.SegmentStart(n)), frame(l.SegmentStart(n)+l.SegmentDuration(n))
		out[n] = float64(k1-k0) * float64(r.FPSDen) / float64(r.FPSNum)
	}
	return out, true
}

// DASH writes the MPD of l, its segments under base.
func DASH(l *video.StreamLayout, base string) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" profiles="urn:mpeg:dash:profile:isoff-live:2011" type="static"` +
		` mediaPresentationDuration="` + isoDuration(l.Duration) + `" minBufferTime="PT` + strconv.Itoa(l.SegmentSeconds) + `S">` + "\n")
	b.WriteString("  <BaseURL>" + escape(base) + "</BaseURL>\n")
	b.WriteString(`  <Period id="0" start="PT0S">` + "\n")
	if len(l.Video) > 0 {
		r0 := &l.Video[0]
		b.WriteString(`    <AdaptationSet id="0" contentType="video" mimeType="video/mp4" segmentAlignment="true" startWithSAP="1"` +
			` maxWidth="` + strconv.Itoa(r0.Width) + `" maxHeight="` + strconv.Itoa(r0.Height) + `" frameRate="` + frameRate(r0) + `">` + "\n")
		b.WriteString(segmentTemplate(90_000, l.SegmentSeconds, l.Offset90k))
		for i := range l.Video {
			r := &l.Video[i]
			b.WriteString(`      <Representation id="` + r.Name + `" codecs="` + r.Codecs + `" bandwidth="` +
				strconv.FormatInt(l.Bandwidth(r)-l.AudioBandwidth(), 10) + `" width="` + strconv.Itoa(r.Width) +
				`" height="` + strconv.Itoa(r.Height) + `" sar="1:1"/>` + "\n")
		}
		b.WriteString("    </AdaptationSet>\n")
	}
	if a := l.Audio; a != nil {
		b.WriteString(`    <AdaptationSet id="1" contentType="audio" mimeType="audio/mp4" segmentAlignment="true" startWithSAP="1">` + "\n")
		b.WriteString(segmentTemplate(int64(a.SampleRate), l.SegmentSeconds, l.Offset90k*int64(a.SampleRate)/90_000))
		b.WriteString(`      <Representation id="` + Audio + `" codecs="` + a.Codecs + `" bandwidth="` +
			strconv.FormatInt(l.AudioBandwidth(), 10) + `" audioSamplingRate="` + strconv.Itoa(a.SampleRate) + `">` + "\n")
		b.WriteString(`        <AudioChannelConfiguration schemeIdUri="urn:mpeg:dash:23003:3:audio_channel_configuration:2011" value="` +
			strconv.Itoa(a.Channels) + `"/>` + "\n")
		b.WriteString("      </Representation>\n    </AdaptationSet>\n")
	}
	b.WriteString("  </Period>\n</MPD>\n")
	return []byte(b.String())
}

// segmentTemplate is an adaptation set's SegmentTemplate: segments of
// seconds each, numbered from 0, whose media time starts at offset ticks.
func segmentTemplate(timescale int64, seconds int, offset int64) string {
	return `      <SegmentTemplate timescale="` + strconv.FormatInt(timescale, 10) + `" duration="` +
		strconv.FormatInt(timescale*int64(seconds), 10) + `" startNumber="0" presentationTimeOffset="` +
		strconv.FormatInt(offset, 10) + `" initialization="$RepresentationID$/init.mp4" media="$RepresentationID$/$Number$.m4s"/>` + "\n"
}

// frameRate is a DASH frame rate: "25" or "30000/1001".
func frameRate(r *video.Rendition) string {
	if r.FPSDen == 1 {
		return strconv.Itoa(r.FPSNum)
	}
	return strconv.Itoa(r.FPSNum) + "/" + strconv.Itoa(r.FPSDen)
}

// isoDuration writes seconds as an xs:duration ("PT6.600S").
func isoDuration(s float64) string {
	return "PT" + strconv.FormatFloat(math.Round(s*1000)/1000, 'f', 3, 64) + "S"
}

func escape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s)) // a strings.Builder never fails
	return b.String()
}
