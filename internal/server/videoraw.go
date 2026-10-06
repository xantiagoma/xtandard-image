package server

import (
	"bytes"
	"context"
	"net/http"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Raw video sources (WP-128): /video/<sig>/raw:1/plain/<src> streams the
// media source as fetched, so a client (the playground's before/after
// comparison) can play the original next to a transformation of it. It is
// a video request in every check that precedes it in videoProcess (role,
// signature, unsigned policy, expiry) and in its fetch (allowlists, the
// SSRF-safe fetcher, XI_VIDEO_TRANSFORM_MAX_INPUT_BYTES, hashsum); it
// differs in that nothing is resolved or transcoded, the other video
// options are ignored, and the body (the fetched source) is never stored
// in the result cache. respondVideo answers Range with it.

// videoRaw fetches req's source and completes the result with its bytes.
func (s *Server) videoRaw(ctx context.Context, r *http.Request, info *requestInfo, req *transform.Request, signed bool) (*imageResult, error) {
	lim := s.limits(&req.Limits)
	t := s.stage(info, stageResolve)
	key, err := s.resolve(ctx, req.Source)
	t.done()
	if err != nil {
		return nil, err
	}
	info.source = key.Source

	t = s.stage(info, stageFetch)
	fr, err := s.fetch(ctx, key, source.FetchOptions{MaxBytes: s.videoMaxSrcBytes(lim), Header: r.Header}, req.Source.Hashsum)
	t.done()
	if err != nil {
		return nil, err
	}
	defer fr.Release()

	ct, ext, ok := sniffMedia(fr.Bytes())
	if !ok {
		return nil, errs.New(errs.KindUnsupported, "server: raw video source is not a recognised video or audio file")
	}
	j := &job{req: req, key: key, lim: lim, signed: signed, hashsum: req.Source.Hashsum, video: "raw"}
	res := newResult(j, fr)
	res.ext, res.mediaType = ext, ct
	res.etag = etag(req.CanonicalKey()+"\x00video:raw", transform.FormatUnset, 0, &fr.Object, fr.Bytes(), s.cfg.ETagBuster)
	info.format = ext
	if res.notModified = notModified(r, res.etag, fr.Object.LastModified); res.notModified {
		return res, nil
	}
	if r.Method == http.MethodHead {
		return res, nil
	}
	body := fr.Buffer
	body.Retain()
	res.out = &ProcessOutput{Body: body, ContentType: ct}
	return res, nil
}

// sniffMedia identifies a video or audio file from its first bytes: its
// Content-Type and extension. Only media types are recognised: a raw video
// request never serves an image, a document or markup.
func sniffMedia(b []byte) (contentType, ext string, ok bool) {
	at := func(off int, magic string) bool {
		return len(b) >= off+len(magic) && string(b[off:off+len(magic)]) == magic
	}
	switch {
	case at(4, "ftyp"):
		switch {
		case at(8, "M4A "), at(8, "M4B "):
			return "audio/mp4", "m4a", true
		case at(8, "qt  "):
			return "video/quicktime", "mov", true
		}
		return "video/mp4", "mp4", true
	case at(0, "\x1a\x45\xdf\xa3"):
		if bytes.Contains(b[:min(len(b), 64)], []byte("webm")) {
			return "video/webm", "webm", true
		}
		return "video/x-matroska", "mkv", true
	case at(0, "OggS"):
		return "audio/ogg", "ogg", true
	case at(0, "fLaC"):
		return "audio/flac", "flac", true
	case at(0, "RIFF") && at(8, "WAVE"):
		return "audio/wav", "wav", true
	case at(0, "RIFF") && at(8, "AVI "):
		return "video/x-msvideo", "avi", true
	case at(0, "ID3"):
		return "audio/mpeg", "mp3", true
	case len(b) >= 2 && b[0] == 0xff && b[1]&0xf6 == 0xf0:
		// ADTS: the sync word and layer 00.
		return "audio/aac", "aac", true
	case len(b) >= 2 && b[0] == 0xff && b[1]&0xe0 == 0xe0 && b[1]&0x06 != 0:
		// An MPEG audio frame: the sync word and a layer.
		return "audio/mpeg", "mp3", true
	case len(b) > 188 && b[0] == 0x47 && b[188] == 0x47:
		return "video/mp2t", "ts", true
	}
	return "", "", false
}
