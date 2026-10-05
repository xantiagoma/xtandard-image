package cloudflare

import (
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// MediaName is the identifier of the /cdn-cgi/media/ dialect (Cloudflare
// Media Transformations, WP-125).
const MediaName = "cloudflare_media"

// MediaPrefix is the native path prefix of Media Transformations. The
// /cdn-cgi/image/ parser hands paths that start with it to the media
// parser, so a host-bound Cloudflare mount serves both.
const MediaPrefix = "/cdn-cgi/media"

// Media Transformations limits (developers.cloudflare.com/stream/transform-videos/).
const (
	mediaMaxTime     = 10 * time.Minute
	mediaMinDuration = time.Second
	mediaMaxDuration = time.Minute
	mediaMinSide     = 10
	mediaMaxSide     = 2000
	mediaMaxFilename = 120
	// mediaSpriteFrames is the number of frames of a sprite sheet without
	// imageCount; mediaMaxSpriteFrames bounds imageCount.
	mediaSpriteFrames    = 10
	mediaMaxSpriteFrames = 100
)

// Media modes.
const (
	modeVideo       = "video"
	modeFrame       = "frame"
	modeSpritesheet = "spritesheet"
	modeAudio       = "audio"
)

// Reasons and expectations of the media dialect.
const (
	reasonNotInMode   = "not applicable in this mode"
	reasonSpriteStart = "sprite sheets start at the beginning of the video"

	expMode      = "video, frame, spritesheet or audio"
	expTime      = "time such as 5s, 1.5s or 2m, in 0..10m"
	expDuration  = "time such as 5s or 1m, in 1s..60s"
	expMediaSide = "integer 10..2000"
	expMediaFit  = "contain, scale-down or cover"
	expFrameFmt  = "jpg or png"
	expAudioFmt  = "m4a"
	expFilename  = "up to 120 letters, digits, - or _"
	expCount     = "integer 1..100"
)

// MediaParser is the Media Transformations dialect:
//
//	/cdn-cgi/media/<options>/<source>
//
// mounted at /cdn-cgi/media/ (next to the default /cdn-cgi/image/ mount)
// or reached through a Cloudflare mount whose path continues with
// /cdn-cgi/media/. The options are a comma-separated key=value list:
//
//   - mode (required): video → a KindVideo request (MP4), audio → an
//     audio-only M4A (AAC), frame → a still image of the frame at time
//     (the video thumbnail path, JPEG or PNG), spritesheet → a JPEG sprite
//     of frames (the video_thumbnail_tile path);
//   - time (0..10m) is where the output starts (the frame for frame);
//     duration (1s..60s, default 60s clamped to the source) its length,
//     or the range the sprite's frames are taken from;
//   - width, height (10..2000) and fit (contain, scale-down, cover; crop
//     as in Cloudflare's own example is cover) size the output;
//   - audio=false mutes video mode; format is jpg/png (frame) or m4a
//     (audio); filename names the Content-Disposition file;
//   - imageCount (an xtandard extension, 1..100, default 10) is the number
//     of sprite frames.
//
// Sources and xi_sig signing are those of /cdn-cgi/image/. Options that do
// not apply to the mode, and unknown ones, are recorded in Request.Ignored
// (400 in strict mode); values out of Cloudflare's ranges are 400.
type MediaParser struct {
	o   Options
	opt *imgproxy.Parser
}

var _ dialect.Dialect = (*MediaParser)(nil)

// NewMedia returns a MediaParser; nil means the zero Options.
func NewMedia(opts *Options) (*MediaParser, error) {
	var o Options
	if opts != nil {
		o = *opts
	}
	ip, err := imgproxy.New(&imgproxy.Options{Strict: o.Strict, Now: o.Now})
	if err != nil {
		return nil, err
	}
	return &MediaParser{o: o, opt: ip}, nil
}

// Name implements dialect.Dialect.
func (*MediaParser) Name() string { return MediaName }

// Verifier implements dialect.Dialect: the xi_sig verifier, or nil.
func (p *MediaParser) Verifier() sign.Verifier { return p.o.XIVerifier }

// Parse implements dialect.Dialect. It uses in.Path and in.RawQuery.
func (p *MediaParser) Parse(in *dialect.Input) (*dialect.Result, error) {
	return p.ParseURL(in.Path, in.RawQuery)
}

// ParseURL parses the escaped path after the mount prefix, for example
// "/mode=video,width=480/videos/a.mp4", and the raw query.
func (p *MediaParser) ParseURL(path, rawQuery string) (*dialect.Result, error) {
	if rest, ok := strings.CutPrefix(path, MediaPrefix); ok && (rest == "" || rest[0] == '/') {
		path = rest
	}
	sig, msg, err := sign.XIMessage(path, rawQuery)
	if err != nil {
		return nil, err
	}
	sigResult := sign.Unsigned
	if p.o.XIVerifier != nil {
		if sigResult, err = p.o.XIVerifier.Verify(sig, msg); err != nil {
			return nil, err
		}
	}
	opts, src, ok := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	switch {
	case opts == "":
		return nil, dialect.InvalidURL("missing options")
	case !ok:
		return nil, dialect.InvalidURL("missing source")
	}
	source, err := sourceOf(src, msg[len(path):])
	if err != nil {
		return nil, err
	}

	b := p.opt.NewBuilder(MediaName)
	m := &mediaState{st: newState(b, p.o.Strict, false)}
	if err := m.read(opts); err != nil {
		return nil, err
	}
	if err := m.build(); err != nil {
		return nil, err
	}
	r := b.Request()
	r.Source = transform.SourceSpec{Raw: source, Encoding: transform.SourcePlain}
	return &dialect.Result{Request: r, SourceString: source, Signature: sigResult}, nil
}

// mediaOption is one option as written (key lower-cased, value decoded).
type mediaOption struct{ key, value string }

// mediaState is one parse of a media option list.
type mediaState struct {
	st   *state
	opts []mediaOption // last value of each key, in first-seen order
}

// read splits the option list; a later value of a key replaces the earlier
// one.
func (m *mediaState) read(escaped string) error {
	for item := range strings.SplitSeq(escaped, ",") {
		if item == "" {
			continue
		}
		k, v, ok := strings.Cut(item, "=")
		key, err := unescape(k)
		if err != nil {
			return err
		}
		key = strings.ToLower(strings.TrimSpace(key))
		switch {
		case key == "":
			return dialect.InvalidURL("empty option name")
		case !ok:
			return dialect.InvalidArgument(key, "", "key=value")
		}
		value, err := unescape(v)
		if err != nil {
			return err
		}
		value = strings.TrimSpace(value)
		if i := m.index(key); i >= 0 {
			m.opts[i].value = value
			continue
		}
		m.opts = append(m.opts, mediaOption{key, value})
	}
	return nil
}

func (m *mediaState) index(key string) int {
	for i := range m.opts {
		if m.opts[i].key == key {
			return i
		}
	}
	return -1
}

// get returns the value of key and whether it was given.
func (m *mediaState) get(key string) (string, bool) {
	if i := m.index(key); i >= 0 {
		return m.opts[i].value, true
	}
	return "", false
}

// skip records an option that does not apply (400 in strict mode).
func (m *mediaState) skip(key, reason string) error {
	if m.st.strict {
		return &errs.Error{
			Kind:   errs.KindInvalidURL,
			Msg:    "option " + strconv.Quote(key) + ": " + reason,
			Public: "invalid url: unsupported option",
			Err:    dialect.ErrUnknownOption,
		}
	}
	m.st.b.Ignore(key, reason)
	return nil
}

// mediaKeys are the options each mode reads; mediaKnown every option.
var mediaKeys = map[string][]string{
	modeVideo:       {"time", "duration", "width", "height", "fit", "audio", "filename"},
	modeAudio:       {"time", "duration", "audio", "format", "filename"},
	modeFrame:       {"time", "width", "height", "fit", "format", "filename"},
	modeSpritesheet: {"time", "duration", "width", "height", "fit", "imagecount", "filename"},
}

var mediaKnown = []string{"mode", "time", "duration", "width", "height", "fit", "audio", "format", "filename", "imagecount"}

// build turns the options into the request of their mode.
func (m *mediaState) build() error {
	mode, ok := m.get("mode")
	if !ok {
		return dialect.InvalidURL("missing mode")
	}
	keys, ok := mediaKeys[mode]
	if !ok {
		return dialect.InvalidArgument("mode", mode, expMode)
	}
	for _, o := range m.opts {
		switch {
		case o.key == "mode" || slices.Contains(keys, o.key):
		case slices.Contains(mediaKnown, o.key):
			if err := m.skip(o.key, reasonNotInMode); err != nil {
				return err
			}
		case m.st.strict:
			return dialect.UnknownOption(o.key)
		default:
			m.st.b.Ignore(o.key, reasonUnknown)
		}
	}
	start, err := m.time("time", expTime, 0, mediaMaxTime)
	if err != nil {
		return err
	}
	if v, ok := m.get("filename"); ok {
		if err := m.filename(v); err != nil {
			return err
		}
	}
	switch mode {
	case modeFrame:
		return m.frame(start)
	case modeSpritesheet:
		return m.sprite(start)
	default:
		return m.video(mode, start)
	}
}

// video builds video and audio mode.
func (m *mediaState) video(mode string, start time.Duration) error {
	op := &transform.VideoOp{Start: start, Duration: mediaMaxDuration}
	d, err := m.time("duration", expDuration, mediaMinDuration, mediaMaxDuration)
	if err != nil {
		return err
	}
	if d > 0 {
		op.Duration = d
	}
	audio := true
	if v, ok := m.get("audio"); ok {
		if audio, err = boolOption("audio", v); err != nil {
			return err
		}
	}
	if mode == modeAudio {
		if !audio {
			return dialect.InvalidArgument("audio", "false", "true in audio mode")
		}
		if v, ok := m.get("format"); ok && v != "m4a" {
			return dialect.InvalidArgument("format", v, expAudioFmt)
		}
		op.Container, op.AudioCodec = transform.VideoContainerM4A, transform.AudioCodecAAC
	} else {
		op.Mute = !audio
		if err := m.box(&op.Width, &op.Height); err != nil {
			return err
		}
		if op.Fit, err = m.videoFit(op.Width, op.Height); err != nil {
			return err
		}
	}
	if err := m.st.b.Finish(); err != nil {
		return err
	}
	r := m.st.req()
	r.Kind, r.Video, r.Output = transform.KindVideo, op, transform.Output{}
	return nil
}

// frame builds frame mode: the frame at start, as a still image.
func (m *mediaState) frame(start time.Duration) error {
	pl := m.st.pl()
	sec := start.Seconds()
	pl.Video = &transform.VideoOptions{Second: &sec}
	if err := m.imageBox(); err != nil {
		return err
	}
	r := m.st.req()
	r.Output.Format = transform.FormatJPEG
	if v, ok := m.get("format"); ok {
		switch v {
		case "jpg", "jpeg":
		case "png":
			r.Output.Format = transform.FormatPNG
		default:
			return dialect.InvalidArgument("format", v, expFrameFmt)
		}
	}
	return m.st.b.Finish()
}

// sprite builds spritesheet mode: imageCount frames spread over duration
// (or the whole video) in a near-square JPEG grid.
func (m *mediaState) sprite(start time.Duration) error {
	if start > 0 {
		if err := m.skip("time", reasonSpriteStart); err != nil {
			return err
		}
	}
	d, err := m.time("duration", expDuration, mediaMinDuration, mediaMaxDuration)
	if err != nil {
		return err
	}
	n := mediaSpriteFrames
	if v, ok := m.get("imagecount"); ok {
		if n, err = strconv.Atoi(v); err != nil || n < 1 || n > mediaMaxSpriteFrames {
			return dialect.InvalidArgument("imageCount", v, expCount)
		}
	}
	cols := int(math.Ceil(math.Sqrt(float64(n))))
	tile := &transform.VideoTile{
		Columns: cols, Rows: (n + cols - 1) / cols, Trim: true, FocusX: 0.5, FocusY: 0.5,
	}
	if d > 0 {
		tile.Step = d.Seconds() / float64(n)
	}
	if err := m.box(&tile.TileWidth, &tile.TileHeight); err != nil {
		return err
	}
	fit, err := m.videoFit(tile.TileWidth, tile.TileHeight)
	if err != nil {
		return err
	}
	tile.Fill = fit == transform.VideoFitCover
	m.st.pl().Video = &transform.VideoOptions{Tile: tile}
	m.st.req().Output.Format = transform.FormatJPEG
	return m.st.b.Finish()
}

// time parses a time option: a number with the unit s or m ("5s", "1.5s",
// "2m"), bounded by lo..hi; 0 when absent.
func (m *mediaState) time(key, exp string, lo, hi time.Duration) (time.Duration, error) {
	v, ok := m.get(key)
	if !ok {
		return 0, nil
	}
	unit := time.Second
	num, found := strings.CutSuffix(v, "s")
	if !found {
		if num, found = strings.CutSuffix(v, "m"); !found {
			return 0, dialect.InvalidArgument(key, v, exp)
		}
		unit = time.Minute
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil || num == "" || num[0] == '+' || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0, dialect.InvalidArgument(key, v, exp)
	}
	t := time.Duration(math.Round(f * float64(unit)))
	if f*float64(unit) > float64(hi) || t < lo || t > hi {
		return 0, dialect.InvalidArgument(key, v, exp)
	}
	return t, nil
}

// box parses width and height (10..2000 pixels).
func (m *mediaState) box(w, h *int) error {
	for _, d := range []struct {
		key string
		dst *int
	}{{"width", w}, {"height", h}} {
		v, ok := m.get(d.key)
		if !ok {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < mediaMinSide || n > mediaMaxSide {
			return dialect.InvalidArgument(d.key, v, expMediaSide)
		}
		*d.dst = n
	}
	return nil
}

// videoFit maps fit for a w × h box. Without both sides every mode is a
// proportional resize (contain or scale-down).
func (m *mediaState) videoFit(w, h int) (transform.VideoFit, error) {
	v, ok := m.get("fit")
	fit := transform.VideoFitContain
	switch {
	case !ok, v == "contain":
	case v == "scale-down":
		fit = transform.VideoFitScaleDown
	case v == "cover", v == "crop":
		fit = transform.VideoFitCover
	default:
		return 0, dialect.InvalidArgument("fit", v, expMediaFit)
	}
	if fit == transform.VideoFitCover && (w == 0 || h == 0) {
		fit = transform.VideoFitContain
	}
	return fit, nil
}

// imageBox applies width, height and fit to the image pipeline (frame
// mode) with the /cdn-cgi/image/ semantics of the same values.
func (m *mediaState) imageBox() error {
	var w, h int
	if err := m.box(&w, &h); err != nil {
		return err
	}
	rs := &m.st.pl().Resize
	if w > 0 {
		rs.Width = transform.Dim{Px: w}
	}
	if h > 0 {
		rs.Height = transform.Dim{Px: h}
	}
	v, ok := m.get("fit")
	switch {
	case !ok:
		v = "contain"
	case v == "crop":
		v = "cover"
	case v != "contain" && v != "scale-down" && v != "cover":
		return dialect.InvalidArgument("fit", v, expMediaFit)
	}
	return m.st.fit(v)
}

// filename sets the Content-Disposition file name.
func (m *mediaState) filename(v string) error {
	if v == "" || len(v) > mediaMaxFilename || strings.IndexFunc(v, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_'
	}) >= 0 {
		return dialect.InvalidArgument("filename", v, expFilename)
	}
	m.st.req().Delivery.Filename = v
	return nil
}

func boolOption(key, v string) (bool, error) {
	switch v {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, dialect.InvalidArgument(key, v, expBool)
}

func unescape(s string) (string, error) {
	v, err := url.PathUnescape(s)
	if err != nil {
		return "", dialect.InvalidURL("invalid option escaping")
	}
	return v, nil
}
