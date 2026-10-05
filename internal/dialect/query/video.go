package query

import (
	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
)

// videoEndpoint is the path after the mount that selects video
// transformations (WP-122): /q/video?url=…&start=1&width=640&format=webm.
const videoEndpoint = "video"

// expVideoFit is the expectation of the fit key of video URLs.
const expVideoFit = "fit, contain, inside, fill, cover, fill-down, force or stretch"

// video applies the keys of a /video request in query order: every key is
// the imgproxy /video option of the same name or alias
// (docs/URL-DIALECTS.md §3.8) with "," or ":" between its arguments, plus
// the native spellings of fit, gravity, crop, flip, cb and attachment, and
// o= for a /video option path.
func (p *Parser) video(b *imgproxy.Builder, params []param) error {
	for _, kv := range params {
		if err := p.videoKey(b, kv.key, kv.value); err != nil {
			return err
		}
	}
	return nil
}

func (p *Parser) videoKey(b *imgproxy.Builder, key, v string) error {
	switch key {
	case paramURL:
		return nil
	case paramO:
		return b.ApplyPath(paramO, v)
	case "fit":
		switch v {
		case "fit", "contain", "inside":
			return b.Apply("resizing_type", "fit")
		case "fill", "cover":
			return b.Apply("resizing_type", "fill")
		case "fill-down":
			return b.Apply("resizing_type", "fill-down")
		case "force", "stretch":
			return b.Apply("resizing_type", "force")
		}
		return dialect.InvalidArgument("fit", v, expVideoFit)
	case "gravity", "g":
		args := split(v)
		args[0] = gravityCode(args[0])
		return b.Apply("gravity", args...)
	case "crop":
		return crop(b, v)
	case "flip":
		return flip(b, v)
	case "cb":
		return b.Apply("cachebuster", v)
	case "attachment":
		return b.Apply("return_attachment", v)
	case paramPDFPassword, paramPDFPasswordShort:
		return p.unknown(b, key)
	}
	if isSignatureParam(key) {
		return nil
	}
	return b.Apply(key, split(v)...)
}
