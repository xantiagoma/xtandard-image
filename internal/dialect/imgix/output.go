package imgix

import (
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// --- output (https://docs.imgix.com/apis/rendering/format) ----------------

// applyOutput maps fm, q, auto, lossless, chromasub, cs, dpi, dl, ch and
// expires.
func (st *state) applyOutput() error {
	o := &st.r.Output
	autoFormat, compress, err := st.auto()
	if err != nil {
		return err
	}
	if err := st.format(autoFormat); err != nil {
		return err
	}
	if v, ok := st.get("q"); ok {
		q, err := intRange("q", v, 0, 100)
		if err != nil {
			return err
		}
		o.Quality = max(q, 1)
	}
	if compress {
		// auto=compress: quality 45 unless q is set, metadata stripped, and
		// without an explicit or negotiated format JPEG for opaque images
		// (PNG keeps transparency).
		if !st.has("q") {
			o.Quality = 45
		}
		on := true
		o.StripMetadata = &on
		if o.Format == transform.FormatUnset && !autoFormat && st.r.Kind == transform.KindProcess {
			o.PreferJPEGIfOpaque = true
		}
	}
	if v, ok := st.get("lossless"); ok {
		on, err := imgproxy.ParseBool("lossless", v)
		if err != nil {
			return err
		}
		o.Lossless = &on
	}
	if v, ok := st.get("chromasub"); ok {
		switch v {
		case "420":
			o.ChromaSubsampling = transform.Subsampling420
		case "422":
			o.ChromaSubsampling = transform.Subsampling422
		case "444":
			o.ChromaSubsampling = transform.Subsampling444
		default:
			return dialect.InvalidArgument("chromasub", v, expSub)
		}
	}
	if err := st.colorSpace(); err != nil {
		return err
	}
	if v, ok := st.get("dpi"); ok {
		n, err := intRange("dpi", v, 0, transform.MaxDPI)
		if err != nil {
			return err
		}
		o.DPI = n
	}
	if v, ok := st.get("dl"); ok {
		if v == "" || !imgproxy.PlausibleSource(v) {
			return dialect.InvalidArgument("dl", v, "file name")
		}
		on := true
		st.r.Delivery.Filename, st.r.Delivery.Attachment = v, &on
	}
	if err := st.clientHints(); err != nil {
		return err
	}
	if v, ok := st.get("expires"); ok {
		return st.b.Apply("expires", v)
	}
	return nil
}

// auto parses the auto list. enhance, redeye and true (enhance plus
// redeye) are recorded as unsupported in every mode: imgix treats them as
// best-effort hints.
func (st *state) auto() (format, compress bool, err error) {
	v, ok := st.get("auto")
	if !ok {
		return false, false, nil
	}
	for _, tok := range list(v) {
		switch strings.TrimSpace(tok) {
		case "format":
			format = true
		case "compress":
			compress = true
		case "enhance", "redeye", "true":
			st.b.Ignore("auto", reasonUnsupported)
		default:
			return false, false, dialect.InvalidArgument("auto", v, expAuto)
		}
	}
	return format, compress, nil
}

// format maps fm. With auto=format imgix negotiates AVIF/WebP and uses fm
// only as the fallback for clients without them; xi negotiates and records
// fm as ignored (the fallback is the negotiation default). json, blurhash,
// mp4 and webm are not image formats and win over auto=format.
func (st *state) format(autoFormat bool) error {
	v, ok := st.get("fm")
	if !ok {
		return nil
	}
	o := &st.r.Output
	on := true
	switch v {
	case "json":
		st.r.Kind = transform.KindInfo
		st.r.Info = &transform.InfoOptions{Size: true, Format: true, Dimensions: true, EXIF: true, IPTC: true, XMP: true}
		return nil
	case "blurhash":
		// imgix answers the bare blurhash string; xi answers info JSON with
		// a blurhash field.
		st.r.Kind = transform.KindInfo
		st.r.Info = &transform.InfoOptions{Blurhash: &transform.InfoBlurhash{XComponents: 4, YComponents: 3}}
		st.b.Ignore("fm", reasonApproximated)
		return nil
	case "mp4":
		o.Format = transform.FormatMP4
		st.b.Ignore("fm", reasonM3)
		return nil
	case "webm":
		o.Format = transform.FormatWebM
		st.b.Ignore("fm", reasonM3)
		return nil
	case "jp2", "jxr", "pdf":
		return &errs.Error{Kind: errs.KindUnsupported, Msg: "imgix fm " + v, Public: "unsupported output format"}
	}
	var (
		f     transform.Format
		apply func()
	)
	switch v {
	case "jpg", "jpeg":
		f = transform.FormatJPEG
	case "pjpg":
		f, apply = transform.FormatJPEG, func() { o.Progressive = &on }
	case "png", "png32":
		f = transform.FormatPNG
	case "png8":
		f, apply = transform.FormatPNG, func() { o.PNG = &transform.PNGOptions{Quantize: &on, QuantizationColors: 256} }
	case "webp":
		f = transform.FormatWebP
	case "avif":
		f = transform.FormatAVIF
	case "jxl":
		f = transform.FormatJXL
	case "gif":
		f = transform.FormatGIF
	default:
		return dialect.InvalidArgument("fm", v, expFormat)
	}
	if autoFormat {
		st.b.Ignore("fm", reasonAutoFormat)
		return nil
	}
	o.Format = f
	if apply != nil {
		apply()
	}
	return nil
}

// colorSpace maps cs: srgb and tinysrgb convert to sRGB (M2), strip drops
// the profile, origin keeps it; adobergb1998 is not implemented.
func (st *state) colorSpace() error {
	v, ok := st.get("cs")
	if !ok {
		return nil
	}
	o := &st.r.Output
	switch v {
	case "srgb", "tinysrgb":
		o.ColorProfile = "srgb"
		st.b.Ignore("cs", reasonM2)
	case "strip", "origin":
		on := v == "strip"
		o.StripColorProfile = &on
	case "adobergb1998":
		st.b.Ignore("cs", reasonNotImplemented)
	default:
		return dialect.InvalidArgument("cs", v, expCS)
	}
	return nil
}

// clientHints maps ch=Width,DPR,Save-Data.
func (st *state) clientHints() error {
	v, ok := st.get("ch")
	if !ok {
		return nil
	}
	ch := &st.r.ClientHints
	for _, tok := range list(v) {
		switch strings.ToLower(strings.TrimSpace(tok)) {
		case "width":
			ch.Width = true
		case "dpr":
			ch.DPR = true
		case "save-data":
			ch.SaveData = true
		default:
			return dialect.InvalidArgument("ch", v, expCH)
		}
	}
	return nil
}
