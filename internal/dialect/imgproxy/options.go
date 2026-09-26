// Copyright 2017 Sergey Alexandrovich (imgproxy), Apache License 2.0.
// Option semantics reimplemented from imgproxy options/parser/apply.go and
// processing_options.go. Modifications copyright xtandard-image
// contributors, MIT. See NOTICE.

package imgproxy

import (
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Reasons recorded in Request.Ignored.
const (
	reasonM2             = "takes effect in M2"
	reasonM3             = "takes effect in M3"
	reasonNotImplemented = "not implemented"
	reasonUnknown        = "unknown option"
	reasonRemoved        = "removed upstream"
)

// apply applies one option. args has at least one element.
//
//nolint:gocyclo // one flat switch over the option table is the clearest form
func (st *state) apply(name string, args []string) error {
	pl := st.pipeline()
	switch name {
	// Resizing.
	case "resize", "rs":
		return st.resize(pl, args)
	case "size", "s":
		return st.size(pl, "size", args)
	case "resizing_type", "rt":
		return resizingType(pl, "resizing_type", args)
	case "resizing_algorithm", "ra":
		if err := maxArgs("resizing_algorithm", args, 1); err != nil {
			return err
		}
		k, ok := kernels[args[0]]
		if !ok {
			return dialect.InvalidArgument("resizing_algorithm", args[0], expAlgorithm)
		}
		pl.Resize.Algorithm = k
	case "width", "w":
		return width(pl, "width", args)
	case "height", "h":
		return height(pl, "height", args)
	case "min-width", "mw":
		return oneNonNegInt("min-width", args, &pl.Resize.MinWidth)
	case "min-height", "mh":
		return oneNonNegInt("min-height", args, &pl.Resize.MinHeight)
	case "zoom", "z":
		return zoom(pl, args)
	case "dpr":
		return onePositiveFloat("dpr", args, &pl.Resize.DPR)
	case "enlarge", "el":
		return oneBool("enlarge", args, &pl.Resize.Enlarge)
	case "extend", "ex":
		return st.extend(&pl.Extend, "extend", args)
	case "extend_aspect_ratio", "extend_ar", "exar":
		return st.extend(&pl.ExtendAR, "extend_aspect_ratio", args)

	// Cropping and geometry.
	case "gravity", "g":
		return st.gravity(&pl.Gravity, "gravity", gravityCrop, name, args)
	case "crop", "c":
		return st.crop(pl, name, args)
	case "trim", "t":
		return trim(pl, args)
	case "padding", "pd":
		return padding(pl, args)
	case "auto_rotate", "ar":
		return oneBoolPtr("auto_rotate", args, &pl.AutoRotate)
	case "rotate", "rot":
		if err := maxArgs("rotate", args, 1); err != nil {
			return err
		}
		r, err := parseInt("rotate", args[0])
		if err != nil {
			return err
		}
		if r%90 != 0 {
			return dialect.InvalidArgument("rotate", args[0], expRotate)
		}
		pl.Rotate = r
	case "flip", "fl":
		return flip(pl, args)
	case "background", "bg":
		return background(pl, args)
	case "background_alpha", "bga":
		if err := maxArgs("background_alpha", args, 1); err != nil {
			return err
		}
		a, err := parseUnitFloat("background_alpha", args[0])
		if err != nil {
			return err
		}
		st.bgAlpha[st.cur] = a

	// Effects.
	case "blur", "bl":
		return oneNonNegFloat("blur", args, &pl.Blur)
	case "sharpen", "sh":
		return oneNonNegFloat("sharpen", args, &pl.Sharpen)
	case "pixelate", "pix":
		return oneNonNegInt("pixelate", args, &pl.Pixelate)
	case "watermark", "wm":
		return st.watermark(pl, name, args)

	// Animation and pages.
	case "disable_animation", "da":
		return oneBool("disable_animation", args, &pl.DisableAnimation)
	case "page", "pg":
		if err := maxArgs("page", args, 1); err != nil {
			return err
		}
		v, err := parseNonNegInt("page", args[0])
		if err != nil {
			return err
		}
		pl.Page = &v
	case "pages", "pgs":
		if err := maxArgs("pages", args, 1); err != nil {
			return err
		}
		v, err := strconv.Atoi(args[0])
		if err != nil || (v < 1 && v != -1) || v > 1<<31-1 {
			return dialect.InvalidArgument("pages", args[0], expPages)
		}
		pl.Pages = &v

	// Output (request-wide).
	case "strip_metadata", "sm":
		return oneBoolPtr("strip_metadata", args, &st.r.Output.StripMetadata)
	case "keep_copyright", "kcr":
		return oneBoolPtr("keep_copyright", args, &st.r.Output.KeepCopyright)
	case "strip_color_profile", "scp":
		return oneBoolPtr("strip_color_profile", args, &st.r.Output.StripColorProfile)
	case "preserve_hdr", "ph":
		st.ignore(name, reasonM2)
		return oneBoolPtr("preserve_hdr", args, &st.r.Output.PreserveHDR)
	case "enforce_thumbnail", "eth":
		st.ignore(name, reasonM2)
		return oneBoolPtr("enforce_thumbnail", args, &st.r.Output.EnforceThumbnail)
	case "quality", "q":
		if err := maxArgs("quality", args, 1); err != nil {
			return err
		}
		q, err := parseQuality("quality", args[0])
		if err != nil {
			return err
		}
		st.r.Output.Quality = q
	case "format_quality", "fq":
		return st.formatQuality(args)
	case "max_bytes", "mb":
		return oneNonNegInt("max_bytes", args, &st.r.Output.MaxBytes)
	case "format", "f", "ext":
		if err := maxArgs("format", args, 1); err != nil {
			return err
		}
		return st.format("format", args[0])

	// Delivery (request-wide).
	case "skip_processing", "skp", "sp":
		for _, a := range args {
			f, err := parseFormat("skip_processing", a, false)
			if err != nil {
				return err
			}
			st.r.Delivery.SkipProcessing = append(st.r.Delivery.SkipProcessing, f)
		}
	case "raw":
		if err := maxArgs("raw", args, 1); err != nil {
			return err
		}
		raw, err := parseBool("raw", args[0])
		if err != nil {
			return err
		}
		st.r.Kind = transform.KindProcess
		if raw {
			st.r.Kind = transform.KindRaw
		}
	case "cachebuster", "cb":
		if err := maxArgs("cachebuster", args, 1); err != nil {
			return err
		}
		st.r.Delivery.CacheBuster = args[0]
	case "expires", "exp":
		return st.expires(args)
	case "filename", "fn":
		return st.filename(args)
	case "return_attachment", "att":
		return oneBoolPtr("return_attachment", args, &st.r.Delivery.Attachment)
	case "preset", "pr":
		return st.applyPresets(args)

	// Security (request-wide, only when allowed).
	case "max_src_resolution", "msr":
		if err := st.security("max_src_resolution"); err != nil {
			return err
		}
		return oneNonNegFloat("max_src_resolution", args, &st.r.Limits.MaxSrcResolution)
	case "max_src_file_size", "msfs":
		if err := st.security("max_src_file_size"); err != nil {
			return err
		}
		var v int
		if err := oneNonNegInt("max_src_file_size", args, &v); err != nil {
			return err
		}
		st.r.Limits.MaxSrcFileSize = int64(v)
	case "max_animation_frames", "maf":
		if err := st.security("max_animation_frames"); err != nil {
			return err
		}
		if err := maxArgs("max_animation_frames", args, 1); err != nil {
			return err
		}
		v, err := parsePositiveInt("max_animation_frames", args[0])
		if err != nil {
			return err
		}
		st.r.Limits.MaxAnimationFrames = v
	case "max_animation_frame_resolution", "mafr":
		if err := st.security("max_animation_frame_resolution"); err != nil {
			return err
		}
		return oneNonNegFloat("max_animation_frame_resolution", args, &st.r.Limits.MaxAnimationFrameResolution)
	case "max_result_dimension", "mrd":
		if err := st.security("max_result_dimension"); err != nil {
			return err
		}
		return oneNonNegInt("max_result_dimension", args, &st.r.Limits.MaxResultDimension)

	case "gif_options", "gifo":
		st.ignore(name, reasonRemoved)
	default:
		if ok, err := st.applyLater(pl, name, args); ok {
			return err
		}
		if st.p.o.Strict {
			return dialect.UnknownOption(name)
		}
		st.ignore(name, reasonUnknown)
	}
	return nil
}

func oneBool(field string, args []string, dst *bool) error {
	if err := maxArgs(field, args, 1); err != nil {
		return err
	}
	b, err := parseBool(field, args[0])
	if err != nil {
		return err
	}
	*dst = b
	return nil
}

func oneBoolPtr(field string, args []string, dst **bool) error {
	var b bool
	if err := oneBool(field, args, &b); err != nil {
		return err
	}
	*dst = &b
	return nil
}

func oneNonNegInt(field string, args []string, dst *int) error {
	if err := maxArgs(field, args, 1); err != nil {
		return err
	}
	v, err := parseNonNegInt(field, args[0])
	if err != nil {
		return err
	}
	*dst = v
	return nil
}

func oneNonNegFloat(field string, args []string, dst *float64) error {
	if err := maxArgs(field, args, 1); err != nil {
		return err
	}
	v, err := parseNonNegFloat(field, args[0])
	if err != nil {
		return err
	}
	*dst = v
	return nil
}

func onePositiveFloat(field string, args []string, dst *float64) error {
	if err := maxArgs(field, args, 1); err != nil {
		return err
	}
	v, err := parsePositiveFloat(field, args[0])
	if err != nil {
		return err
	}
	*dst = v
	return nil
}

func width(pl *transform.Pipeline, field string, args []string) error {
	var v int
	if err := oneNonNegInt(field, args, &v); err != nil {
		return err
	}
	pl.Resize.Width = transform.Dim{Px: v}
	return nil
}

func height(pl *transform.Pipeline, field string, args []string) error {
	var v int
	if err := oneNonNegInt(field, args, &v); err != nil {
		return err
	}
	pl.Resize.Height = transform.Dim{Px: v}
	return nil
}

func resizingType(pl *transform.Pipeline, field string, args []string) error {
	if err := maxArgs(field, args, 1); err != nil {
		return err
	}
	t, ok := resizingTypes[args[0]]
	if !ok {
		return dialect.InvalidArgument(field, args[0], expResizeType)
	}
	pl.Resize.Type = t
	return nil
}

// resize is rs:type:width:height:enlarge:extend[:gravity:x:y].
func (st *state) resize(pl *transform.Pipeline, args []string) error {
	if err := maxArgs("resize", args, 8); err != nil {
		return err
	}
	if args[0] != "" {
		if err := resizingType(pl, "resize.type", args[:1]); err != nil {
			return err
		}
	}
	if len(args) > 1 {
		return st.size(pl, "resize", args[1:])
	}
	return nil
}

// size is s:width:height:enlarge:extend[:gravity:x:y].
func (st *state) size(pl *transform.Pipeline, field string, args []string) error {
	if err := maxArgs(field, args, 7); err != nil {
		return err
	}
	if args[0] != "" {
		if err := width(pl, field+".width", args[:1]); err != nil {
			return err
		}
	}
	if len(args) > 1 && args[1] != "" {
		if err := height(pl, field+".height", args[1:2]); err != nil {
			return err
		}
	}
	if len(args) > 2 && args[2] != "" {
		if err := oneBool(field+".enlarge", args[2:3], &pl.Resize.Enlarge); err != nil {
			return err
		}
	}
	if len(args) > 3 && args[3] != "" {
		return st.extend(&pl.Extend, field+".extend", args[3:])
	}
	return nil
}

func zoom(pl *transform.Pipeline, args []string) error {
	if err := maxArgs("zoom", args, 2); err != nil {
		return err
	}
	x, err := parsePositiveFloat("zoom.x", args[0])
	if err != nil {
		return err
	}
	y := x
	if len(args) > 1 {
		if y, err = parsePositiveFloat("zoom.y", args[1]); err != nil {
			return err
		}
	}
	pl.Resize.ZoomX, pl.Resize.ZoomY = x, y
	return nil
}

// extend is ex:enabled[:gravity:x:y] (also extend_aspect_ratio).
func (st *state) extend(e *transform.Extend, field string, args []string) error {
	if err := maxArgs(field, args, 4); err != nil {
		return err
	}
	on, err := parseBool(field, args[0])
	if err != nil {
		return err
	}
	e.Enabled = on
	if len(args) > 1 {
		if _, err := parseGravity(&e.Gravity, field+".gravity", gravityExtend, args[1:]); err != nil {
			return err
		}
	}
	return nil
}

func (st *state) gravity(g *transform.Gravity, field string, kind gravityKind, name string, args []string) error {
	ms, err := parseGravity(g, field, kind, args)
	if err != nil {
		return err
	}
	if ms == "M3" {
		st.ignore(name+":"+args[0], reasonM3)
	}
	return nil
}

// crop is c:width[:height[:gravity...]].
func (st *state) crop(pl *transform.Pipeline, name string, args []string) error {
	c := transform.Crop{}
	if pl.Crop != nil {
		c = *pl.Crop
	}
	w, err := parseDim("crop.width", args[0])
	if err != nil {
		return err
	}
	c.Width = w
	if len(args) > 1 {
		h, err := parseDim("crop.height", args[1])
		if err != nil {
			return err
		}
		c.Height = h
	}
	if len(args) > 2 {
		if err := st.gravity(&c.Gravity, "crop.gravity", gravityCrop, name, args[2:]); err != nil {
			return err
		}
	}
	pl.Crop = &c
	return nil
}

// trim is t:threshold[:color:equal_hor:equal_ver]; an empty threshold
// disables trimming.
func trim(pl *transform.Pipeline, args []string) error {
	if err := maxArgs("trim", args, 4); err != nil {
		return err
	}
	t := transform.Trim{}
	if pl.Trim != nil {
		t = *pl.Trim
	}
	if args[0] == "" {
		pl.Trim = nil
		return nil
	}
	th, err := parseFloat("trim.threshold", args[0])
	if err != nil {
		return err
	}
	t.Threshold = th
	t.Color = nil
	if len(args) > 1 && args[1] != "" {
		c, err := parseHexColor("trim.color", args[1])
		if err != nil {
			return err
		}
		t.Color = &c
	}
	if len(args) > 2 && args[2] != "" {
		if err := oneBool("trim.equal_hor", args[2:3], &t.EqualHor); err != nil {
			return err
		}
	}
	if len(args) > 3 && args[3] != "" {
		if err := oneBool("trim.equal_ver", args[3:4], &t.EqualVer); err != nil {
			return err
		}
	}
	pl.Trim = &t
	return nil
}

// padding is pd:top[:right[:bottom[:left]]] with CSS shorthand rules.
func padding(pl *transform.Pipeline, args []string) error {
	if err := maxArgs("padding", args, 4); err != nil {
		return err
	}
	var v [4]int
	var set [4]bool
	fields := [4]string{"padding.top", "padding.right", "padding.bottom", "padding.left"}
	for i := range min(len(args), 4) {
		if args[i] == "" {
			continue
		}
		n, err := parseNonNegInt(fields[i], args[i])
		if err != nil {
			return err
		}
		v[i], set[i] = n, true
	}
	if !set[1] {
		v[1] = v[0]
	}
	if !set[2] {
		v[2] = v[0]
	}
	if !set[3] {
		v[3] = v[1]
	}
	if v == [4]int{} {
		pl.Padding = nil
		return nil
	}
	pl.Padding = &transform.Padding{Top: v[0], Right: v[1], Bottom: v[2], Left: v[3]}
	return nil
}

func flip(pl *transform.Pipeline, args []string) error {
	if err := maxArgs("flip", args, 2); err != nil {
		return err
	}
	if args[0] != "" {
		if err := oneBool("flip.horizontal", args[:1], &pl.Flip.H); err != nil {
			return err
		}
	}
	if len(args) > 1 && args[1] != "" {
		return oneBool("flip.vertical", args[1:2], &pl.Flip.V)
	}
	return nil
}

// background is bg:hex, bg:r:g:b, or bg: (no background).
func background(pl *transform.Pipeline, args []string) error {
	switch len(args) {
	case 1:
		if args[0] == "" {
			pl.Background = nil
			return nil
		}
		c, err := parseHexColor("background", args[0])
		if err != nil {
			return err
		}
		pl.Background = &c
	case 3:
		var c transform.Color
		var err error
		if c.R, err = parseByte("background.r", args[0]); err != nil {
			return err
		}
		if c.G, err = parseByte("background.g", args[1]); err != nil {
			return err
		}
		if c.B, err = parseByte("background.b", args[2]); err != nil {
			return err
		}
		c.A = 255
		pl.Background = &c
	default:
		return dialect.InvalidArgument("background", joinArgs(args), "hex color or r:g:b")
	}
	return nil
}

// watermark is wm:opacity[:position:x:y:scale]. One watermark per
// pipeline; later wm options update it argument by argument.
func (st *state) watermark(pl *transform.Pipeline, name string, args []string) error {
	if err := maxArgs("watermark", args, 7); err != nil {
		return err
	}
	if len(pl.Watermarks) == 0 {
		pl.Watermarks = make([]transform.Watermark, 1)
	}
	w := &pl.Watermarks[0]
	op, err := parseUnitFloat("watermark.opacity", args[0])
	if err != nil {
		return err
	}
	w.Opacity = op
	if len(args) > 1 && args[1] != "" {
		switch pos := args[1]; pos {
		case "re":
			w.Tile = transform.TileRepeat
			w.Position.Type = transform.GravityUnset
		case "ch":
			w.Tile = transform.TileChessboard
			w.Position.Type = transform.GravityUnset
			st.ignore(name+":ch", reasonM3)
		default:
			gt, ok := compass[pos]
			if !ok {
				return dialect.InvalidArgument("watermark.position", pos, expWatermarkPos)
			}
			w.Tile = transform.TileNone
			w.Position.Type = gt
		}
	}
	if len(args) > 2 && args[2] != "" {
		if w.Position.XOffset, err = parseFloat("watermark.x_offset", args[2]); err != nil {
			return err
		}
	}
	if len(args) > 3 && args[3] != "" {
		if w.Position.YOffset, err = parseFloat("watermark.y_offset", args[3]); err != nil {
			return err
		}
	}
	if len(args) > 4 && args[4] != "" {
		if w.Scale, err = parsePositiveFloat("watermark.scale", args[4]); err != nil {
			return err
		}
	}
	return nil
}

func (st *state) format(field, s string) error {
	f, err := parseFormat(field, s, true)
	if err != nil {
		return err
	}
	st.r.Output.Format = f
	return nil
}

// formatQuality is fq:format:quality[:format:quality...]; quality 0 means
// the format default and removes the entry.
func (st *state) formatQuality(args []string) error {
	if len(args)%2 != 0 {
		return dialect.InvalidArgument("format_quality", joinArgs(args), expEvenPairs)
	}
	for i := 0; i < len(args); i += 2 {
		f, err := parseFormat("format_quality.format", args[i], false)
		if err != nil {
			return err
		}
		q, err := parseQuality("format_quality.quality", args[i+1])
		if err != nil {
			return err
		}
		if q == 0 {
			delete(st.r.Output.FormatQuality, f)
			continue
		}
		if st.r.Output.FormatQuality == nil {
			st.r.Output.FormatQuality = make(map[transform.Format]int, len(args)/2)
		}
		st.r.Output.FormatQuality[f] = q
	}
	return nil
}

// expires is exp:unix_seconds; 0 or negative means never.
func (st *state) expires(args []string) error {
	if err := maxArgs("expires", args, 1); err != nil {
		return err
	}
	ts, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return dialect.InvalidArgument("expires", args[0], "unix timestamp")
	}
	st.r.Delivery.Expires = time.Time{}
	if ts > 0 {
		st.r.Delivery.Expires = time.Unix(ts, 0).UTC()
	}
	return nil
}

// filename is fn:name[:encoded]; encoded names are url-safe base64.
func (st *state) filename(args []string) error {
	if err := maxArgs("filename", args, 2); err != nil {
		return err
	}
	name := args[0]
	if len(args) > 1 {
		encoded, err := parseBool("filename.encoded", args[1])
		if err != nil {
			return err
		}
		if encoded {
			b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(name, "="))
			if err != nil {
				return dialect.InvalidArgument("filename", name, expBase64)
			}
			name = string(b)
		}
	}
	st.r.Delivery.Filename = name
	return nil
}

func (st *state) security(name string) error {
	if !st.p.o.AllowSecurityOptions {
		return dialect.OptionNotAllowed(name)
	}
	return nil
}
