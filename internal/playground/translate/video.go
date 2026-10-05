package translate

import (
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Video transformations (WP-127): a transform.KindVideo request written in
// the dialects that have video URLs — imgproxy /video (URL-DIALECTS §3.8),
// the query form /q/video (§4), Cloudinary /video/{upload,fetch}/ (§6.1),
// ImageKit video files (§7.1) and Cloudflare /cdn-cgi/media/ (§8.2) — and
// parsed back like images, so the "unsupported here" notes are measured.
// The image-only dialects say why they have no card.

// videoSegment is the first segment of an imgproxy mount's video paths.
const videoSegment = "/video"

// videoParser is a dialect with a /video endpoint (router.VideoParser).
type videoParser interface {
	ParseVideo(in *dialect.Input) (*dialect.Result, error)
}

// videoTargets lists the output dialects of a video request, in response
// order: the five with video URLs, then the image-only ones.
func videoTargets() []target {
	none := func(label, why string) func(*emitter) (string, string, error) {
		return func(*emitter) (string, string, error) {
			return "", "", fmt.Errorf("%s has no video transformations: %s", label, why)
		}
	}
	return []target{
		{"imgproxy", "imgproxy", "/", emitVideoImgproxy},
		{"query", "imgproxy options as a query (query dialect)", "/q/", emitVideoQuery},
		{"cloudinary", "Cloudinary", "/cloudinary/", emitVideoCloudinary},
		{"imagekit", "ImageKit", "/imagekit/", emitVideoImagekit},
		{"cloudflare_media", "Cloudflare Media Transformations", "/cdn-cgi/media/", emitVideoCloudflareMedia},
		{"cloudflare", "Cloudflare Images", "/cdn-cgi/image/", none("/cdn-cgi/image/", "video is /cdn-cgi/media/ (Media Transformations)")},
		{"imgix", "imgix", "/imgix/", none("imgix", "its video API is a separate product; here a video source gives frames (frame=, fm=mp4 for animations)")},
		{"wsrv", "wsrv.nl (weserv)", "/wsrv/", none("wsrv.nl", "it serves images only")},
		{"thumbor", "Thumbor", "/thumbor/", none("Thumbor", "it serves images only")},
		{"bunny", "Bunny Optimizer", "/bunny/", none("Bunny Optimizer", "video is Bunny Stream, another product")},
		{"awssih", "AWS Serverless Image Handler", "/sih/", none("AWS Serverless Image Handler", "it serves images only")},
	}
}

// parseVideoPath parses an imgproxy mount's path "/video/<rest>" (without
// a signature: "/insecure" is added) with d's video endpoint; ok is false
// when path is not a video path.
func parseVideoPath(d dialect.Dialect, path, rawQuery, mountSource string) (*transform.Request, bool, error) {
	rest, ok := strings.CutPrefix(path, videoSegment+"/")
	if !ok {
		return nil, false, nil
	}
	vp, ok := d.(videoParser)
	if !ok {
		return nil, true, badRequest("this dialect has no /video endpoint")
	}
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, true, badRequest("invalid query")
	}
	res, err := vp.ParseVideo(&dialect.Input{Path: "/insecure/" + rest, Query: q, RawQuery: rawQuery, Mount: dialect.Mount{Source: mountSource}})
	if err != nil {
		return nil, true, err
	}
	r := res.Request
	if mountSource != "" && r.Source.Mount == "" {
		r.Source.Mount = mountSource
	}
	return r, true, nil
}

// normalizeVideo prepares a video request for compare: only the VideoOp,
// the source and the delivery describe the output (the dialects leave
// different empty pipelines behind), and an automatic audio codec is the
// container's.
func normalizeVideo(c *transform.Request) {
	c.Pipelines = nil
	c.Output = transform.Output{}
	if c.Video == nil {
		return
	}
	v := *c.Video
	if v.AudioCodec == transform.AudioCodecAuto {
		v.AudioCodec = defaultAudioCodec(v.Container)
	}
	if v.Container.AudioOnly() || v.Container.Image() {
		v.VideoCodec = transform.VideoCodecAuto
	}
	if v.Container.Image() {
		v.AudioCodec = transform.AudioCodecAuto
	}
	if v.End > 0 {
		// end and start+duration are the same cut.
		v.Duration, v.End = v.End-v.Start, 0
	}
	if v.Width == 0 && v.Height == 0 {
		v.Fit = transform.VideoFitScaleDown // no box: nothing to fit
	}
	if len(v.Overlays) == 0 {
		v.Overlays = nil
	}
	c.Video = &v
}

// defaultAudioCodec is the audio codec container c writes by default.
func defaultAudioCodec(c transform.VideoContainer) transform.AudioCodec {
	switch c {
	case transform.VideoContainerMP4, transform.VideoContainerM4A:
		return transform.AudioCodecAAC
	case transform.VideoContainerWebM, transform.VideoContainerOpus, transform.VideoContainerOgg:
		return transform.AudioCodecOpus
	case transform.VideoContainerMP3:
		return transform.AudioCodecMP3
	case transform.VideoContainerFLAC:
		return transform.AudioCodecFLAC
	case transform.VideoContainerWAV:
		return transform.AudioCodecPCM
	default:
		return transform.AudioCodecAuto
	}
}

// volumeArg writes a gain as decibels when it is a whole hundredth of a
// decibel ("-6dB", as such a gain is usually written), else as a factor.
func volumeArg(f float64) string {
	db := 20 * math.Log10(f)
	if r := math.Round(db*100) / 100; r != 0 && math.Abs(math.Pow(10, r/20)-f) < 1e-12 {
		return num(r) + "dB"
	}
	return num(f)
}

// secs writes a duration in seconds ("1.5").
func secs(d time.Duration) string { return num(d.Seconds()) }

var videoCompass = map[transform.VideoGravity]string{
	transform.VideoGravityCenter: "ce", transform.VideoGravityNorth: "no", transform.VideoGravitySouth: "so",
	transform.VideoGravityEast: "ea", transform.VideoGravityWest: "we", transform.VideoGravityNorthEast: "noea",
	transform.VideoGravityNorthWest: "nowe", transform.VideoGravitySouthEast: "soea", transform.VideoGravitySouthWest: "sowe",
}

// ipVideoOp writes the /video options of v (aliases, as the image emitter
// writes them). The container goes into the source extension.
//
//nolint:gocyclo,cyclop // one flat walk over the VideoOp model
func ipVideoOp(v *transform.VideoOp) ipOpts {
	var o ipOpts
	if v.Start > 0 {
		o.add("vs", secs(v.Start))
	}
	if v.End > 0 {
		o.add("ve", secs(v.End))
	}
	if v.Duration > 0 {
		o.add("vd", secs(v.Duration))
	}
	if c := v.Crop; c != nil {
		args := []string{itoa(c.Width), itoa(c.Height)}
		g := transform.VideoGravityNorthWest // offsets from the top-left corner
		if c.Gravity != nil {
			g = *c.Gravity
		}
		if c.Gravity != nil || c.X != 0 || c.Y != 0 {
			args = append(args, videoCompass[g], itoa0(c.X), itoa0(c.Y))
		}
		o.add("c", args...)
	}
	if v.Width > 0 || v.Height > 0 {
		typ, enlarge, extend := "fit", "", ""
		switch v.Fit {
		case transform.VideoFitCover:
			typ = "fill"
		case transform.VideoFitFill:
			typ = "force"
		case transform.VideoFitPad:
			extend = "1"
		case transform.VideoFitContain:
			enlarge = "1"
		case transform.VideoFitScaleDown:
		}
		o.add("rs", typ, itoa0(v.Width), itoa0(v.Height), enlarge, extend)
	}
	if v.Gravity != transform.VideoGravityCenter {
		o.add("g", videoCompass[v.Gravity])
	}
	if v.Background != (transform.Color{}) {
		o.add("bg", hexRGB(v.Background))
	}
	if v.Rotate != 0 {
		o.add("rot", itoa(v.Rotate))
	}
	if v.FlipH || v.FlipV {
		o.add("fl", boolIf(v.FlipH), boolIf(v.FlipV))
	}
	if v.FPS > 0 {
		o.add("fps", num(v.FPS))
	}
	if v.VideoCodec != transform.VideoCodecAuto {
		o.add("vc", v.VideoCodec.String())
	}
	if v.AudioCodec != transform.AudioCodecAuto {
		o.add("ac", v.AudioCodec.String())
	}
	if v.Quality > 0 {
		o.add("q", itoa(v.Quality))
	}
	if v.CRF > 0 {
		o.add("crf", itoa(v.CRF))
	}
	if v.MaxBitrate > 0 {
		o.add("vb", itoa64(v.MaxBitrate))
	}
	if v.KeyframeInterval > 0 {
		o.add("ki", secs(v.KeyframeInterval))
	}
	if v.Mute {
		o.add("mu", "1")
	}
	if v.AudioBitrate > 0 {
		o.add("ab", itoa64(v.AudioBitrate))
	}
	if v.SampleRate > 0 {
		o.add("sr", itoa(v.SampleRate))
	}
	if v.Channels > 0 {
		o.add("ach", itoa(v.Channels))
	}
	if v.Volume > 0 {
		o.add("vol", volumeArg(v.Volume))
	}
	if l := v.Loudnorm; l != nil {
		o.add("ln", "1", num(l.Integrated), num(l.TruePeak), num(l.Range))
	}
	if v.FadeIn > 0 || v.FadeOut > 0 {
		o.add("fd", secs(v.FadeIn), secs(v.FadeOut))
	}
	if w := v.Waveform; w != nil {
		color, bg := "", ""
		if w.Color != (transform.Color{}) {
			color = hexRGB(w.Color)
		}
		if w.Background != (transform.Color{}) {
			bg = hexRGB(w.Background)
		}
		o.add("wf", "1", color, bg, boolIf(w.SplitChannels))
	}
	ipVideoOverlays(&o, v.Overlays)
	return o
}

// ipVideoOverlays writes the first image or text overlay (imgproxy's wm
// options timed by ow and of) and the first timecode; others are lost.
func ipVideoOverlays(o *ipOpts, ovs []transform.VideoOverlay) {
	mark, tc := false, false
	for i := range ovs {
		ov := &ovs[i]
		switch {
		case ov.Timecode != transform.VideoTimecodeNone && !tc:
			tc = true
			pos, x, y, size, color, opacity := "", "", "", "", "", ""
			if c, ok := ipCompass[ov.Position.Type]; ok && ov.Position.Type != transform.GravitySouthWest {
				pos = c
			}
			if ov.Position.XOffset != 0 {
				x = num(ov.Position.XOffset)
			}
			if ov.Position.YOffset != 0 {
				y = num(ov.Position.YOffset)
			}
			if ov.TimecodeSize > 0 {
				size = itoa(ov.TimecodeSize)
			}
			if ov.Color != nil && *ov.Color != (transform.Color{R: 255, G: 255, B: 255, A: 255}) {
				color = hexRGB(*ov.Color)
			}
			if ov.Opacity != 1 {
				opacity = num(ov.Opacity)
			}
			o.add("tc", ov.Timecode.String(), pos, x, y, size, color, opacity)
		case ov.Timecode == transform.VideoTimecodeNone && !mark:
			mark = true
			ipWatermark(o, &ov.Watermark)
			if ov.Start > 0 || ov.End > 0 {
				o.add("ow", secs(ov.Start), secs(ov.End))
			}
			if ov.FadeIn > 0 || ov.FadeOut > 0 {
				o.add("of", secs(ov.FadeIn), secs(ov.FadeOut))
			}
		}
	}
}

// videoPathOpts is the option path of a video request (video options,
// then the delivery options).
func videoPathOpts(r *transform.Request, sep string) []string {
	o := ipVideoOp(r.Video)
	segs := o.segments(sep)
	ro := ipRequest(&transform.Request{Delivery: r.Delivery, Source: r.Source})
	return append(segs, ro.segments(sep)...)
}

// emitVideoImgproxy writes /video/<options>/plain/<source>@<container>.
func emitVideoImgproxy(e *emitter) (string, string, error) {
	var b strings.Builder
	b.WriteString(videoSegment)
	for _, s := range videoPathOpts(e.r, e.sep) {
		b.WriteByte('/')
		b.WriteString(s)
	}
	b.WriteString("/plain/")
	b.WriteString(escapePlain(e.src.abs))
	if c := e.r.Video.Container; c != transform.VideoContainerMP4 {
		b.WriteString("@" + c.String())
	}
	return b.String(), "", nil
}

// queryVideoOption reports options written under o= in the query form:
// those whose query key has another (native) meaning there.
var queryVideoPathOnly = map[string]bool{"g": true, "c": true, "fl": true}

// emitVideoQuery writes /video?url=<source>&<option>=<args>; the options
// whose query key is a native spelling (gravity, crop, flip) go into o=.
func emitVideoQuery(e *emitter) (string, string, error) {
	parts := []string{"url=" + url.QueryEscape(e.src.abs)}
	var rest []string
	for _, seg := range videoPathOpts(e.r, e.sep) {
		name, arg, _ := strings.Cut(seg, e.sep)
		if queryVideoPathOnly[name] {
			rest = append(rest, seg)
			continue
		}
		parts = append(parts, name+"="+url.QueryEscape(strings.ReplaceAll(arg, e.sep, ":")))
	}
	if c := e.r.Video.Container; c != transform.VideoContainerMP4 {
		parts = append(parts, "format="+c.String())
	}
	if len(rest) > 0 {
		parts = append(parts, "o="+url.QueryEscape(strings.Join(rest, "/")))
	}
	return videoSegment, strings.Join(parts, "&"), nil
}

var cloudinaryVideoGravity = map[transform.VideoGravity]string{
	transform.VideoGravityNorth: "north", transform.VideoGravitySouth: "south", transform.VideoGravityEast: "east",
	transform.VideoGravityWest: "west", transform.VideoGravityNorthEast: "north_east", transform.VideoGravityNorthWest: "north_west",
	transform.VideoGravitySouthEast: "south_east", transform.VideoGravitySouthWest: "south_west",
}

// cloudinaryVideoFormats are the containers Cloudinary's f_ names.
var cloudinaryVideoFormats = map[transform.VideoContainer]string{
	transform.VideoContainerMP4: "mp4", transform.VideoContainerWebM: "webm", transform.VideoContainerM4A: "m4a",
	transform.VideoContainerMP3: "mp3", transform.VideoContainerOpus: "opus",
}

// emitVideoCloudinary writes /<cloud>/video/{upload,fetch}/<components>/<source>:
// a c_crop component, a resize component, then the trim, codecs, audio and
// delivery.
//
//nolint:gocyclo,cyclop // one flat walk over the VideoOp model
func emitVideoCloudinary(e *emitter) (string, string, error) {
	var kind, id string
	if k, ok := e.src.keyOn(e.mountSource); ok {
		kind, id = "upload", k
	} else if e.src.isHTTP() {
		kind, id = "fetch", escapePlain(e.src.abs)
	} else {
		return "", "", errSource("Cloudinary takes an http(s) URL (fetch) or a public ID on the mount's source (upload)")
	}
	v := e.r.Video
	var comps []string
	if c := v.Crop; c != nil && c.Width > 0 && c.Height > 0 {
		comp := []string{"c_crop", "w_" + itoa(c.Width), "h_" + itoa(c.Height)}
		if c.Gravity != nil && *c.Gravity != transform.VideoGravityCenter {
			comp = append(comp, "g_"+cloudinaryVideoGravity[*c.Gravity])
		}
		if c.X > 0 || c.Gravity == nil {
			comp = append(comp, "x_"+itoa(c.X))
		}
		if c.Y > 0 || c.Gravity == nil {
			comp = append(comp, "y_"+itoa(c.Y))
		}
		comps = append(comps, strings.Join(comp, ","))
	}
	if v.Width > 0 || v.Height > 0 {
		var comp []string
		if v.Width > 0 {
			comp = append(comp, "w_"+itoa(v.Width))
		}
		if v.Height > 0 {
			comp = append(comp, "h_"+itoa(v.Height))
		}
		box := v.Width > 0 && v.Height > 0
		switch v.Fit {
		case transform.VideoFitCover:
			comp = append(comp, "c_fill")
		case transform.VideoFitPad:
			comp = append(comp, "c_pad")
		case transform.VideoFitFill:
			comp = append(comp, "c_scale")
		case transform.VideoFitScaleDown:
			comp = append(comp, "c_limit")
		case transform.VideoFitContain:
			if box {
				comp = append(comp, "c_fit")
			}
		}
		if g, ok := cloudinaryVideoGravity[v.Gravity]; ok {
			comp = append(comp, "g_"+g)
		}
		if v.Fit == transform.VideoFitPad && v.Background != (transform.Color{}) {
			comp = append(comp, "b_"+cloudinaryColor(v.Background))
		}
		comps = append(comps, strings.Join(comp, ","))
	}
	var req []string
	if v.Start > 0 {
		req = append(req, "so_"+secs(v.Start))
	}
	if v.End > 0 {
		req = append(req, "eo_"+secs(v.End))
	}
	if v.Duration > 0 {
		req = append(req, "du_"+secs(v.Duration))
	}
	var angle []string
	if v.Rotate != 0 {
		angle = append(angle, itoa(v.Rotate))
	}
	if v.FlipH {
		angle = append(angle, "hflip")
	}
	if v.FlipV {
		angle = append(angle, "vflip")
	}
	if len(angle) > 0 {
		req = append(req, "a_"+strings.Join(angle, "."))
	}
	if v.FPS > 0 {
		req = append(req, "fps_"+num(v.FPS))
	}
	if v.Quality > 0 {
		req = append(req, "q_"+itoa(v.Quality))
	}
	if v.MaxBitrate > 0 {
		req = append(req, "br_"+itoa64(v.MaxBitrate))
	}
	switch v.VideoCodec {
	case transform.VideoCodecH264:
		req = append(req, "vc_h264")
	case transform.VideoCodecHEVC:
		req = append(req, "vc_h265")
	case transform.VideoCodecVP9:
		req = append(req, "vc_vp9")
	case transform.VideoCodecAV1:
		req = append(req, "vc_av1")
	case transform.VideoCodecAuto:
	}
	switch {
	case v.Mute:
		req = append(req, "ac_none")
	case v.AudioCodec == transform.AudioCodecAAC, v.AudioCodec == transform.AudioCodecMP3, v.AudioCodec == transform.AudioCodecOpus:
		req = append(req, "ac_"+v.AudioCodec.String())
	}
	switch f, ok := cloudinaryVideoFormats[v.Container]; {
	case v.AutoContainer:
		req = append(req, "f_auto")
	case ok:
		req = append(req, "f_"+f)
	default:
		return "", "", fmt.Errorf("Cloudinary video URLs write MP4, WebM, M4A, MP3 and Opus here, not %s "+ //nolint:staticcheck // a provider name
			"(f_wav, f_flac and fl_waveform are not mapped)", v.Container)
	}
	d := &e.r.Delivery
	if d.Attachment != nil && *d.Attachment {
		a := "fl_attachment"
		if d.Filename != "" {
			a += ":" + strings.TrimSuffix(d.Filename, fileExt(d.Filename))
		}
		req = append(req, a)
	}
	comps = append(comps, strings.Join(req, ","))
	return "/xi/video/" + kind + "/" + strings.Join(append(comps, id), "/"), "", nil
}

var imagekitVideoGravity = map[transform.VideoGravity]string{
	transform.VideoGravityNorth: "top", transform.VideoGravitySouth: "bottom", transform.VideoGravityEast: "right",
	transform.VideoGravityWest: "left", transform.VideoGravityNorthEast: "top_right", transform.VideoGravityNorthWest: "top_left",
	transform.VideoGravitySouthEast: "bottom_right", transform.VideoGravitySouthWest: "bottom_left",
}

// emitVideoImagekit writes /tr:<steps>/<key>: an extract step (the crop),
// then the resize, trim, codecs and container; a key without a video
// extension gets /ik-video.mp4 (MP4 output only).
//
//nolint:gocyclo,cyclop // one flat walk over the VideoOp model
func emitVideoImagekit(e *emitter) (string, string, error) {
	key, ok := e.src.keyOn(e.mountSource)
	if !ok {
		return "", "", e.errNeedsKey("ImageKit")
	}
	v := e.r.Video
	var steps []string
	if c := v.Crop; c != nil && c.Width > 0 && c.Height > 0 {
		st := []string{"w-" + itoa(c.Width), "h-" + itoa(c.Height), "cm-extract"}
		if c.Gravity != nil {
			if g, ok := imagekitVideoGravity[*c.Gravity]; ok {
				st = append(st, "fo-"+g)
			}
		}
		if c.X > 0 || c.Gravity == nil {
			st = append(st, "x-"+itoa(c.X))
		}
		if c.Y > 0 || c.Gravity == nil {
			st = append(st, "y-"+itoa(c.Y))
		}
		steps = append(steps, strings.Join(st, ","))
	}
	var st []string
	if v.Width > 0 || v.Height > 0 {
		if v.Width > 0 {
			st = append(st, "w-"+itoa(v.Width))
		}
		if v.Height > 0 {
			st = append(st, "h-"+itoa(v.Height))
		}
		switch v.Fit {
		case transform.VideoFitFill:
			st = append(st, "c-force")
		case transform.VideoFitScaleDown:
			st = append(st, "c-at_max")
		case transform.VideoFitContain:
			st = append(st, "c-at_max_enlarge")
		case transform.VideoFitPad:
			st = append(st, "cm-pad_resize")
			if v.Background != (transform.Color{}) {
				st = append(st, "bg-"+strings.ToUpper(hexRGB(v.Background)))
			}
		case transform.VideoFitCover: // the default with both sides
		}
		if g, ok := imagekitVideoGravity[v.Gravity]; ok {
			st = append(st, "fo-"+g)
		}
	}
	if v.Start > 0 {
		st = append(st, "so-"+secs(v.Start))
	}
	if v.End > 0 {
		st = append(st, "eo-"+secs(v.End))
	}
	if v.Duration > 0 {
		st = append(st, "du-"+secs(v.Duration))
	}
	if v.Rotate != 0 {
		st = append(st, "rt-"+itoa(v.Rotate))
	}
	if v.FPS > 0 {
		st = append(st, "fps-"+num(v.FPS))
	}
	if v.Quality > 0 {
		st = append(st, "q-"+itoa(v.Quality))
	}
	switch v.VideoCodec {
	case transform.VideoCodecH264, transform.VideoCodecVP9, transform.VideoCodecAV1:
		st = append(st, "vc-"+v.VideoCodec.String())
	case transform.VideoCodecAuto, transform.VideoCodecHEVC:
	}
	switch v.Container {
	case transform.VideoContainerM4A:
		st = append(st, "vc-none")
	case transform.VideoContainerOpus:
		st = append(st, "vc-none", "ac-opus")
	case transform.VideoContainerMP4:
		if !v.AutoContainer {
			st = append(st, "f-mp4")
		}
	case transform.VideoContainerWebM:
		st = append(st, "f-webm")
	default:
		return "", "", fmt.Errorf("ImageKit has no %s output: its video API writes MP4, WebM, M4A and Ogg Opus", v.Container)
	}
	switch {
	case v.Mute:
		st = append(st, "ac-none")
	case v.AudioCodec == transform.AudioCodecAAC && v.Container != transform.VideoContainerM4A:
		st = append(st, "ac-aac")
	case v.AudioCodec == transform.AudioCodecOpus && v.Container != transform.VideoContainerOpus:
		st = append(st, "ac-opus")
	}
	if len(st) > 0 {
		steps = append(steps, strings.Join(st, ","))
	}
	path := "/" + key
	if !isVideoKey(key) {
		path += "/ik-video.mp4"
	}
	if len(steps) > 0 {
		path = "/tr:" + strings.Join(steps, ":") + path
	}
	return path, "", nil
}

// isVideoKey reports a key with one of ImageKit's video extensions.
func isVideoKey(key string) bool {
	switch strings.ToLower(strings.TrimPrefix(fileExt(key), ".")) {
	case "mp4", "webm", "mov", "m4v", "mkv", "avi", "ogv", "3gp", "mpeg", "mpg", "ts", "flv", "wmv", "qt":
		return true
	}
	return false
}

// emitVideoCloudflareMedia writes /<mode=video|audio,…>/<source>: a cut of
// at most 60 s (Cloudflare's bound; the whole source when the request has
// no cut) resized to a contain, scale-down or cover box.
func emitVideoCloudflareMedia(e *emitter) (string, string, error) {
	var srcPath string
	switch k, ok := e.src.keyOn(e.mountSource); {
	case ok:
		srcPath = k
	case e.src.isHTTP():
		srcPath = e.src.abs
		if strings.ContainsAny(srcPath, "?#%") {
			srcPath = url.PathEscape(srcPath)
		}
	default:
		return "", "", errSource("Cloudflare takes an http(s) URL or a path on the mount's source")
	}
	v := e.r.Video
	var opts []string
	switch {
	case v.Container == transform.VideoContainerM4A:
		opts = append(opts, "mode=audio")
	case v.Container.HasVideoStream():
		opts = append(opts, "mode=video")
	default:
		return "", "", fmt.Errorf("Cloudflare Media Transformations write MP4 video or M4A audio, not %s", v.Container) //nolint:staticcheck // a provider name
	}
	if v.Start > 0 {
		opts = append(opts, "time="+cloudflareSeconds(v.Start))
	}
	d := v.Duration
	if v.End > 0 {
		d = v.End - v.Start
	}
	if d > 0 {
		opts = append(opts, "duration="+cloudflareSeconds(d))
	}
	if v.Container.HasVideoStream() {
		if v.Width > 0 {
			opts = append(opts, "width="+itoa(v.Width))
		}
		if v.Height > 0 {
			opts = append(opts, "height="+itoa(v.Height))
		}
		switch v.Fit {
		case transform.VideoFitScaleDown:
			if v.Width > 0 || v.Height > 0 {
				opts = append(opts, "fit=scale-down")
			}
		case transform.VideoFitCover:
			opts = append(opts, "fit=cover")
		case transform.VideoFitContain, transform.VideoFitPad, transform.VideoFitFill:
		}
		if v.Mute {
			opts = append(opts, "audio=false")
		}
	}
	if f := e.r.Delivery.Filename; f != "" {
		opts = append(opts, "filename="+strings.TrimSuffix(f, fileExt(f)))
	}
	return "/" + strings.Join(opts, ",") + "/" + srcPath, "", nil
}

// cloudflareSeconds is "5s" or "1.25s".
func cloudflareSeconds(d time.Duration) string {
	return num(math.Round(d.Seconds()*1000)/1000) + "s"
}
