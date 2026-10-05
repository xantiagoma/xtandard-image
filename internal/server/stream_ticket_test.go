package server

import (
	jsonv2 "encoding/json/v2"
	"strings"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestTicketPayloadRoundTrip: what a ticket carries rebuilds the same
// stream request (the same canonical key, so the same cache entries).
func TestTicketPayloadRoundTrip(t *testing.T) {
	t.Parallel()
	g := transform.VideoGravityNorthEast
	op := transform.VideoOp{
		Start: 1500 * time.Millisecond, Duration: 30 * time.Second, Crop: &transform.VideoCrop{Width: 640, X: 3, Gravity: &g},
		Rotate: 90, FlipH: true, FPS: 29.97, VideoCodec: transform.VideoCodecVP9, Quality: 62, KeyframeInterval: time.Second,
		FadeIn: 250 * time.Millisecond, AudioBitrate: 96_000, Channels: 1, Volume: 0.75,
		Loudnorm: &transform.VideoLoudnorm{Integrated: -16, TruePeak: -1.5, Range: 11},
		Stream:   &transform.VideoStream{Protocol: transform.StreamDASH, Renditions: []int{720, 360}, SegmentDuration: 4 * time.Second},
	}
	spec := transform.SourceSpec{
		Raw: "https://example.com/a.mov", Encoding: transform.SourceBase64, Mount: "m",
		Hashsum:  &transform.Hashsum{Algorithm: transform.HashSHA256, Digest: strings.Repeat("ab", 32)},
		Password: &transform.DocumentPassword{Secret: []byte("never in a ticket"), Digest: "x"},
	}
	want := streamRequest(spec, &op)
	if want.Source.Password != nil {
		t.Fatal("a stream request keeps a document password")
	}
	b, err := jsonv2.Marshal(ticketPayload{Source: want.Source, Video: *want.Video}, ticketJSON())
	if err != nil {
		t.Fatal(err)
	}
	var tp ticketPayload
	if err := jsonv2.Unmarshal(b, &tp, ticketJSON()); err != nil {
		t.Fatal(err)
	}
	if got := streamRequest(tp.Source, &tp.Video); got.CanonicalKey() != want.CanonicalKey() {
		t.Fatalf("round trip changed the request:\n%s", b)
	}
	if strings.Contains(string(b), "never") {
		t.Fatalf("payload holds the password: %s", b)
	}
}
