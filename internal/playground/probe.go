package playground

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/obs/logging"
)

// Probes (WP-127): the video mode's Source vs result table describes media
// files the browser cannot inspect itself (the codecs, frame rate, sample
// rate and channels of a source, of an audio file /info refuses, and of a
// transcoded result).

// maxProbeJSON bounds a POST api/probe body.
const maxProbeJSON = 16 << 10

// probeTimeout bounds one probe (the source fetch included).
const probeTimeout = 30 * time.Second

// probeSlots is how many probes run at once; more answer 429.
const probeSlots = 2

// MediaInfo describes a media file.
type MediaInfo struct {
	// Container is FFmpeg's demuxer ("mov,mp4,m4a,3gp,3g2,mj2", "matroska,webm", "mp3", ...).
	Container string  `json:"container" doc:"FFmpeg demuxer that read the file (mov,mp4,m4a,3gp,3g2,mj2; matroska,webm; mp3; ogg; flac; wav)."`
	Duration  float64 `json:"duration,omitempty" doc:"Duration in seconds; absent when unknown."`
	Size      int64   `json:"size" doc:"File size in bytes."`
	// BitRate is Size × 8 / Duration.
	BitRate int64       `json:"bit_rate,omitempty" doc:"Average bit rate in bits per second (size × 8 / duration)."`
	Video   *MediaVideo `json:"video,omitempty" doc:"The video stream; absent for audio files."`
	Audio   *MediaAudio `json:"audio,omitempty" doc:"The audio stream; absent for silent files."`
}

// MediaVideo is the video stream of a MediaInfo.
type MediaVideo struct {
	Codec    string  `json:"codec" doc:"FFmpeg codec name: h264, hevc, av1, vp9, ..."`
	Width    int     `json:"width" doc:"Coded width in pixels."`
	Height   int     `json:"height" doc:"Coded height in pixels."`
	FPS      float64 `json:"fps,omitempty" doc:"Average frame rate."`
	Rotation int     `json:"rotation,omitempty" doc:"Display rotation, clockwise degrees."`
}

// MediaAudio is the audio stream of a MediaInfo.
type MediaAudio struct {
	Codec      string `json:"codec" doc:"FFmpeg codec name: aac, opus, mp3, flac, pcm_s16le, ..."`
	SampleRate int    `json:"sample_rate,omitempty" doc:"Sample rate in Hz."`
	Channels   int    `json:"channels,omitempty" doc:"Channel count."`
}

// ProbeRequest is the body of POST api/probe.
type ProbeRequest struct {
	Source string `json:"source" minLength:"1" maxLength:"8192" doc:"Source string (as in a URL: https://…, xi://<source>/<key>, playground://…), resolved and fetched like a request's source."`
}

// MediaProber describes media files with FFmpeg (internal/app).
type MediaProber interface {
	// ProbeSource resolves and fetches src through the server's sources
	// (allowlists, size limits) and describes it.
	ProbeSource(ctx context.Context, src string) (*MediaInfo, error)
	// ProbeBytes describes b.
	ProbeBytes(ctx context.Context, b []byte) (*MediaInfo, error)
	// MaxBytes bounds a body for ProbeBytes.
	MaxBytes() int64
}

// prober is the handler state of the probe endpoints.
type prober struct {
	p     MediaProber
	slots *semaphore.Weighted
	log   *slog.Logger
}

func newProber(p MediaProber, log *slog.Logger) *prober {
	return &prober{p: p, slots: semaphore.NewWeighted(probeSlots), log: log}
}

// source answers POST api/probe.
func (pr *prober) source(w http.ResponseWriter, r *http.Request) {
	var req ProbeRequest
	if !decodeJSON(w, r, maxProbeJSON, &req) {
		return
	}
	if req.Source == "" || checkPart("source", req.Source) != nil {
		writeJSONError(w, http.StatusBadRequest, "source must be a non-empty escaped source string")
		return
	}
	pr.run(w, r, func(ctx context.Context) (*MediaInfo, error) { return pr.p.ProbeSource(withRender(ctx), req.Source) })
}

// result answers POST api/probe/result: the body is a media file.
func (pr *prober) result(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, pr.p.MaxBytes()))
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "body larger than XI_VIDEO_TRANSFORM_MAX_OUTPUT_BYTES")
			return
		}
		writeJSONError(w, http.StatusBadRequest, "cannot read the body")
		return
	}
	if len(body) == 0 {
		writeJSONError(w, http.StatusBadRequest, "empty body")
		return
	}
	pr.run(w, r, func(ctx context.Context) (*MediaInfo, error) { return pr.p.ProbeBytes(ctx, body) })
}

// run takes a slot (429 when every slot is busy) and answers the probe.
func (pr *prober) run(w http.ResponseWriter, r *http.Request, probe func(context.Context) (*MediaInfo, error)) {
	if !pr.slots.TryAcquire(1) {
		w.Header().Set("Retry-After", "1")
		writeJSONError(w, http.StatusTooManyRequests, "too many probes at once")
		return
	}
	defer pr.slots.Release(1)
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	info, err := probe(ctx)
	if err != nil {
		e, ok := errors.AsType[*errs.Error](err)
		if !ok {
			pr.log.LogAttrs(r.Context(), slog.LevelError, "playground probe failed", logging.Err(err))
			writeJSONError(w, http.StatusInternalServerError, "probe failed")
			return
		}
		// A source or media error: its public message is client-safe.
		writeJSONError(w, e.HTTPStatus(), e.PublicMessage())
		return
	}
	writeJSON(w, http.StatusOK, info)
}
