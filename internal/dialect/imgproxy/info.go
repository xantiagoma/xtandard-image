// Info option grammar from the imgproxy documentation (docs.imgproxy.net
// usage/getting_info) and @imgproxy/imgproxy-js-core (MIT). Copyright
// xtandard-image contributors, MIT.

package imgproxy

import (
	"strconv"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// reasonNotApplicable marks URL parts an /info request does not use.
const reasonNotApplicable = "not applicable to info"

// reasonCentred marks an info crop whose content-aware gravity is placed at
// the centre.
const reasonCentred = "approximated by the centre"

// ParseInfo parses an imgproxy /info request (docs/URL-DIALECTS.md §3.3).
// in.Path is the escaped path after the "/info" prefix:
//
//	/%signature/%info_options.../{plain/%url|%base64|enc/%token}
//
// The signature covers the same bytes as for processing URLs (the path
// after the signature segment). The result has Kind == KindInfo, Info set
// to imgproxy's defaults overridden by the URL, and exactly one pipeline
// holding the processing-like options (page, video thumbnail,
// objects_position). A source extension is ignored.
//
// Info options the model cannot express yet are validated and recorded in
// Request.Ignored as "not implemented": exif canonical names and alpha
// check_transparency.
func (p *Parser) ParseInfo(in *dialect.Input) (*dialect.Result, error) {
	return p.ParseInfoPath(in.Path)
}

// ParseInfoPath is ParseInfo for a bare path such as
// "/unsafe/s/f/d/bh:4:3/plain/https://example.com/a.jpg".
func (p *Parser) ParseInfoPath(path string) (*dialect.Result, error) {
	return p.parsePath(path, true)
}

// defaultInfo returns imgproxy's defaults: size, format, dimensions, EXIF,
// IPTC, XMP and video metadata on, everything else off.
func defaultInfo() *transform.InfoOptions {
	return &transform.InfoOptions{
		Size: true, Format: true, Dimensions: true,
		EXIF: true, IPTC: true, XMP: true, VideoMeta: true,
	}
}

// applyInfo applies one /info option. args has at least one element.
//
//nolint:gocyclo // one flat switch over the option table is the clearest form
func (st *state) applyInfo(name string, args []string) error {
	in := st.r.Info
	var err error
	switch name {
	// Enabled by default.
	case "size", "s":
		return oneBool("size", args, &in.Size)
	case "format", "f":
		return oneBool("format", args, &in.Format)
	case "dimensions", "d":
		return oneBool("dimensions", args, &in.Dimensions)
	case "exif":
		return st.infoExif(args)
	case "iptc":
		return oneBool("iptc", args, &in.IPTC)
	case "xmp":
		return oneBool("xmp", args, &in.XMP)
	case "video_meta", "vm":
		return oneBool("video_meta", args, &in.VideoMeta)

	// Disabled by default.
	case "detect_objects", "do":
		return oneBool("detect_objects", args, &in.DetectObjects) // WP-73
	case "classify", "cl":
		return st.infoClassify(args) // WP-110
	// Header-only values, reported in M1 (WP-35).
	case "colorspace", "cs":
		return oneBool("colorspace", args, &in.Colorspace)
	case "bands", "b":
		return oneBool("bands", args, &in.Bands)
	case "sample_format", "sf":
		return oneBool("sample_format", args, &in.SampleFormat)
	case "pages_number", "pn":
		return oneBool("pages_number", args, &in.PagesNumber)
	case "alpha", "a":
		_, err = st.infoAlpha(args)
	// Pixel-derived and hash options (WP-63).
	case "crop", "c":
		tmp := transform.Pipeline{Crop: in.Crop}
		if err := st.crop(&tmp, args); err != nil {
			return err
		}
		in.Crop = tmp.Crop
		if c := in.Crop; c != nil && (c.Gravity.Type == transform.GravitySmart || c.Gravity.Type == transform.GravityEntropy) {
			// The info crop needs no pixels but the detections: entropy
			// (and smart without faces) is placed at the centre. Face and
			// object gravities are placed on the detections (WP-73).
			st.ignore("crop:gravity", reasonCentred)
		}
		return nil
	case "objects_position", "obj_pos", "op":
		// Places a crop with face or object gravity (WP-73).
		err = objectsPosition(&st.pipeline().Gravity, args)
		in.ObjectsPos = true
	case "palette", "p":
		return infoPalette(in, args)
	case "average", "avg":
		return infoAverage(in, args)
	case "dominant_colors", "dc":
		return infoDominantColors(in, args)
	case "blurhash", "bh":
		return infoBlurhash(in, args)
	case "thumbhash", "thumb_hash", "th":
		return oneBool("thumbhash", args, &in.Thumbhash)
	case "perceptual_hash", "phash", "ph":
		return oneBool("perceptual_hash", args, &in.PHash)
	case "calc_hashsums", "chs":
		return infoCalcHashsums(in, args)

	// /info describes the first page whatever `page` says (codec.Inspect
	// reads one header).
	case "page", "pg":
		if err := st.apply(name, args); err != nil {
			return err
		}
		st.ignore(name, reasonNotImplemented)
		return nil

	// Processing-like and request options shared with processing URLs.
	case "video_thumbnail_second", "vts",
		"video_thumbnail_keyframes", "vtk",
		"cachebuster", "cb",
		"expires", "exp",
		"preset", "pr",
		"hashsum", "hs",
		"max_src_resolution", "msr",
		"max_src_file_size", "msfs",
		"bypass_cache", "bc",
		"cache_tags", "ct":
		return st.apply(name, args)
	default:
		if st.p.o.Strict {
			return dialect.UnknownOption(name)
		}
		st.ignore(name, reasonUnknown)
		return nil
	}
	return err
}

// infoExif is exif:enabled[:canonical_names].
func (st *state) infoExif(args []string) error {
	if err := maxArgs("exif", args, 2); err != nil {
		return err
	}
	on, err := parseBool("exif", args[0])
	if err != nil {
		return err
	}
	st.r.Info.EXIF = on
	if len(args) > 1 {
		canonical, err := parseBool("exif.canonical_names", args[1])
		if err != nil {
			return err
		}
		if canonical {
			st.ignore("exif:canonical_names", reasonNotImplemented)
		}
	}
	return nil
}

// infoAlpha is alpha:enabled[:check_transparency].
func (st *state) infoAlpha(args []string) (bool, error) {
	if err := maxArgs("alpha", args, 2); err != nil {
		return false, err
	}
	on, err := parseBool("alpha", args[0])
	if err != nil {
		return false, err
	}
	st.r.Info.Alpha = on
	if len(args) > 1 {
		check, err := parseBool("alpha.check_transparency", args[1])
		if err != nil {
			return false, err
		}
		if check {
			st.ignore("alpha:check_transparency", reasonNotImplemented)
		}
	}
	return on, nil
}

// infoClassify is classify:top_k[:class_name...]; top_k 0 turns it off.
// Class names are the classifier's (ImageNet's by default, matched without
// regard to case, '_' for a space), checked when the result is rendered.
func (st *state) infoClassify(args []string) error {
	k, err := parseIntRange("classify.top_k", args[0], 0, transform.MaxListLen)
	if err != nil {
		return err
	}
	classes, err := parseClasses("classify.classes", args[1:])
	if err != nil {
		return err
	}
	st.r.Info.ClassifyTopK, st.r.Info.ClassifyClasses = k, nil
	if k > 0 {
		st.r.Info.ClassifyClasses = classes
	}
	return nil
}

// infoPalette is palette:colors, 0 (off) or 2..256.
func infoPalette(in *transform.InfoOptions, args []string) error {
	if err := maxArgs("palette", args, 1); err != nil {
		return err
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n == 1 || n < 0 || n > transform.MaxPaletteSize {
		return dialect.InvalidArgument("palette", args[0], expPalette)
	}
	in.Palette = n
	return nil
}

// infoAverage is average:enabled[:ignore_transparent] (default true).
func infoAverage(in *transform.InfoOptions, args []string) error {
	if err := maxArgs("average", args, 2); err != nil {
		return err
	}
	on, err := parseBool("average", args[0])
	if err != nil {
		return err
	}
	a := transform.InfoAverage{IgnoreTransparent: true}
	if in.Average != nil {
		a = *in.Average
	}
	if s := arg(args, 1); s != "" {
		if err := oneBool("average.ignore_transparent", args[1:2], &a.IgnoreTransparent); err != nil {
			return err
		}
	}
	in.Average = nil
	if on {
		in.Average = &a
	}
	return nil
}

// infoDominantColors is dominant_colors:enabled[:build_missed].
func infoDominantColors(in *transform.InfoOptions, args []string) error {
	if err := maxArgs("dominant_colors", args, 2); err != nil {
		return err
	}
	on, err := parseBool("dominant_colors", args[0])
	if err != nil {
		return err
	}
	dc := transform.InfoDominantColors{}
	if in.DominantColors != nil {
		dc = *in.DominantColors
	}
	if s := arg(args, 1); s != "" {
		if err := oneBool("dominant_colors.build_missed", args[1:2], &dc.BuildMissed); err != nil {
			return err
		}
	}
	in.DominantColors = nil
	if on {
		in.DominantColors = &dc
	}
	return nil
}

// infoBlurhash is blurhash:x_components:y_components, each 1..9; 0:0 is
// off.
func infoBlurhash(in *transform.InfoOptions, args []string) error {
	if err := maxArgs("blurhash", args, 2); err != nil {
		return err
	}
	if len(args) != 2 {
		return dialect.InvalidArgument("blurhash", joinArgs(args), "x_components:y_components")
	}
	x, err := parseIntRange("blurhash.x_components", args[0], 0, transform.MaxBlurhashDim)
	if err != nil {
		return err
	}
	y, err := parseIntRange("blurhash.y_components", args[1], 0, transform.MaxBlurhashDim)
	if err != nil {
		return err
	}
	switch {
	case x == 0 && y == 0:
		in.Blurhash = nil
	case x == 0:
		return dialect.InvalidArgument("blurhash.x_components", args[0], "integer in range 1 to 9")
	case y == 0:
		return dialect.InvalidArgument("blurhash.y_components", args[1], "integer in range 1 to 9")
	default:
		in.Blurhash = &transform.InfoBlurhash{XComponents: x, YComponents: y}
	}
	return nil
}

// infoCalcHashsums is calc_hashsums:type[:type...]; "chs:" clears it.
// Duplicates are dropped.
func infoCalcHashsums(in *transform.InfoOptions, args []string) error {
	in.CalcHashsums = nil
	if len(args) == 1 && args[0] == "" {
		return nil
	}
	for _, a := range args {
		h, ok := hashAlgorithms[a]
		if !ok {
			return dialect.InvalidArgument("calc_hashsums", a, "md5, sha1, sha256 or sha512")
		}
		dup := false
		for _, e := range in.CalcHashsums {
			dup = dup || e == h
		}
		if !dup {
			in.CalcHashsums = append(in.CalcHashsums, h)
		}
	}
	return nil
}
