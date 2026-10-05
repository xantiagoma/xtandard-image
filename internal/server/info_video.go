package server

import (
	"math"

	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
)

// InfoVideoResponse holds the /info fields of a video source (imgproxy
// Pro's video_meta option, on by default): the container metadata in
// video_meta and one entry per stream in video_streams. Both are absent
// for images and when the option is off. It is embedded in InfoResponse
// and, like it, public API (ADR 0008).
type InfoVideoResponse struct {
	VideoMeta    map[string]any    `json:"video_meta,omitzero"    doc:"Container metadata of a video source (info option video_meta): the container tags as strings (major_brand, creation_time, encoder, ...), like imgproxy, plus container (mov, matroska, avi), codec, duration (s), fps, bit_rate (bit/s), frames, width and height (coded size) and rotation (clockwise degrees) of the video stream thumbnails are taken from."`
	VideoStreams []InfoVideoStream `json:"video_streams,omitzero" doc:"Every stream of a video source in file order (info option video_meta), in imgproxy's shape."`
}

// InfoVideoStream is one entry of video_streams: imgproxy's type, codec,
// bps, fps, duration, language, frequency and layout, plus width, height
// and rotation for video streams.
type InfoVideoStream struct {
	Type      string  `json:"type"                doc:"video, audio, subtitle, data or attachment."`
	Codec     string  `json:"codec"               doc:"FFmpeg codec name: h264, hevc, vp9, av1, aac, ..."`
	BPS       int64   `json:"bps,omitempty"       doc:"Bit rate in bits per second, when known."`
	FPS       float64 `json:"fps,omitempty"       doc:"Average frame rate of a video stream."`
	Duration  float64 `json:"duration,omitempty"  doc:"Duration in seconds, when known."`
	Language  string  `json:"language,omitempty"  doc:"ISO 639-2 language tag, when set."`
	Width     int     `json:"width,omitempty"     doc:"xtandard-image extension: coded width of a video stream."`
	Height    int     `json:"height,omitempty"    doc:"xtandard-image extension: coded height of a video stream."`
	Rotation  int     `json:"rotation,omitempty"  doc:"xtandard-image extension: clockwise display rotation of a video stream (0, 90, 180, 270)."`
	Frequency int     `json:"frequency,omitempty" doc:"Sample rate of an audio stream in Hz."`
	Layout    string  `json:"layout,omitempty"    doc:"Channel layout of an audio stream: mono, stereo, 5.1(side), ..."`
	Channels  int     `json:"channels,omitempty"  doc:"xtandard-image extension: channel count of an audio stream."`
}

// infoVideo renders the video fields for o; zero for images or when
// video_meta is off.
func infoVideo(o *transform.InfoOptions, m *video.Meta) InfoVideoResponse {
	if m == nil || !o.VideoMeta {
		return InfoVideoResponse{}
	}
	meta := make(map[string]any, len(m.Tags)+9)
	for k, v := range m.Tags {
		meta[k] = v
	}
	meta["container"] = m.Container
	meta["codec"] = m.Codec
	meta["width"] = m.Width
	meta["height"] = m.Height
	meta["rotation"] = m.Rotation
	if m.Duration > 0 {
		meta["duration"] = round3(m.Duration)
	}
	if m.FPS > 0 {
		meta["fps"] = round3(m.FPS)
	}
	if m.BitRate > 0 {
		meta["bit_rate"] = m.BitRate
	}
	if m.Frames > 0 {
		meta["frames"] = m.Frames
	}
	streams := make([]InfoVideoStream, len(m.Streams))
	for i, s := range m.Streams {
		streams[i] = InfoVideoStream{
			Type: s.Type, Codec: s.Codec, BPS: s.BitRate, FPS: round3(s.FPS), Duration: round3(s.Duration),
			Language: s.Language, Width: s.Width, Height: s.Height, Rotation: s.Rotation,
			Frequency: s.SampleRate, Layout: s.Layout, Channels: s.Channels,
		}
	}
	return InfoVideoResponse{VideoMeta: meta, VideoStreams: streams}
}

// round3 keeps three decimals, so container rounding noise (29.97002997…)
// does not leak into the JSON.
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
