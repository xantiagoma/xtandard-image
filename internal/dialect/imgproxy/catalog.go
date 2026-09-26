package imgproxy

import (
	"maps"
	"math"
	"slices"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// This file is the declarative option catalogue (ADR 0008 decision 4,
// WP-83): every processing option of docs/URL-DIALECTS.md §3.2 and every
// info option of §3.3, with aliases, positional arguments, milestone and
// the imgproxy Pro flag. The parser keeps its apply switches; the catalogue
// is the documentation and JSON Schema source (schema.go), and
// catalog_test.go keeps the two in sync in both directions and against the
// URL-DIALECTS tables. Enumerations come from the parser's own tables
// where it has one.

// ArgType is the JSON type of a positional argument in the options object.
type ArgType uint8

// Argument types.
const (
	TypeString    ArgType = iota + 1 // free text
	TypeInteger                      // decimal integer
	TypeNumber                       // decimal number
	TypeBoolean                      // 1, t, true, 0, f, false in URLs; JSON boolean in the options object
	TypeEnum                         // one of Enum
	TypeColor                        // hex colour, rgb or rrggbb
	TypeBase64                       // url-safe base64, padding optional
	TypeDirection                    // down, up, left, right or an angle in degrees (-360..360)
)

// Arg is one positional argument of an option.
type Arg struct {
	Name string
	Type ArgType
	// Enum lists the accepted values of a TypeEnum argument.
	Enum []string
	// Min and Max bound numeric arguments when HasMin / HasMax are set;
	// ExclusiveMin makes Min exclusive.
	Min, Max       float64
	HasMin, HasMax bool
	ExclusiveMin   bool
	// MultipleOf constrains numeric arguments when non-zero.
	MultipleOf float64
	// Pattern is a regular expression for string arguments.
	Pattern string
	// Optional marks arguments the parser accepts empty ("rs::300"): an
	// empty argument keeps the previous or default value. It is null in the
	// options object. Booleans are always optional (empty is false).
	Optional bool
	// Doc describes the argument.
	Doc string
}

// Form is one accepted argument shape of an option.
type Form struct {
	// Args are the leading positional arguments; trailing ones may be
	// omitted.
	Args []Arg
	// Rest, when set, is a group of arguments repeated after Args
	// (blur_areas areas, format_quality pairs, class lists).
	Rest []Arg
	// MinItems is the least number of arguments; 0 means 1.
	MinItems int
}

// Scope says where an option applies.
type Scope uint8

// Scopes.
const (
	ScopePipeline Scope = iota + 1 // the pipeline it appears in
	ScopeRequest                   // the whole request, wherever it appears
)

// Option is one catalogue entry.
type Option struct {
	// Name is the canonical option name; Aliases are the short forms.
	Name    string
	Aliases []string
	// Forms are the accepted argument shapes; most options have one. When
	// there are several, the first whose enum arguments accept the URL's
	// arguments is the one a URL uses.
	Forms []Form
	Scope Scope
	// Pro marks options that are paid in imgproxy (free here).
	Pro bool
	// Security options need XI_SECURITY_ALLOW_SECURITY_OPTIONS (else 403).
	Security bool
	// Removed options are accepted and ignored for old URLs.
	Removed bool
	// Shared marks info options that are the processing option of the same
	// name (§3.3 last row).
	Shared bool
	// Syntax, Default and Milestone are the Args, Default and Milestone
	// cells of the option's URL-DIALECTS row, verbatim.
	Syntax, Default, Milestone string
	// Doc is a one-line description.
	Doc string
	// Example is a valid argument string ("fill:300:400:0").
	Example string
}

// Names returns the canonical name followed by the aliases.
func (o *Option) Names() []string { return append([]string{o.Name}, o.Aliases...) }

// Argument constructors.

func intArg(name string, lo, hi float64, doc string) Arg {
	return Arg{Name: name, Type: TypeInteger, Min: lo, Max: hi, HasMin: true, HasMax: true, Doc: doc}
}

func numArg(name string, lo, hi float64, doc string) Arg {
	return Arg{Name: name, Type: TypeNumber, Min: lo, Max: hi, HasMin: true, HasMax: true, Doc: doc}
}

func posArg(name string, doc string) Arg {
	return Arg{Name: name, Type: TypeNumber, Min: 0, HasMin: true, ExclusiveMin: true, Doc: doc}
}

func anyNum(name, doc string) Arg { return Arg{Name: name, Type: TypeNumber, Doc: doc} }

// nonNeg is a number >= 0 without an upper bound.
func nonNeg(name, doc string) Arg {
	return Arg{Name: name, Type: TypeNumber, Min: 0, HasMin: true, Doc: doc}
}

func boolArg(name, doc string) Arg {
	return Arg{Name: name, Type: TypeBoolean, Optional: true, Doc: doc}
}

func enumArg(name string, values []string, doc string) Arg {
	return Arg{Name: name, Type: TypeEnum, Enum: values, Doc: doc}
}

func constArg(value string) Arg {
	return Arg{Name: "type", Type: TypeEnum, Enum: []string{value}}
}

func colorArg(name, doc string) Arg {
	return Arg{Name: name, Type: TypeColor, Pattern: `^([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`, Doc: doc}
}

func base64Arg(name, doc string) Arg {
	return Arg{Name: name, Type: TypeBase64, Pattern: `^[A-Za-z0-9_-]*=*$`, Doc: doc}
}

func strArg(name, doc string) Arg { return Arg{Name: name, Type: TypeString, Doc: doc} }

func classArg() Arg {
	return Arg{Name: "class", Type: TypeString, Pattern: `^.+$`, Doc: "face, a COCO object class (person, car, dog, traffic_light, ...) or supercategory (vehicle, animal, ...)"}
}

func unitArg(name, doc string) Arg { return numArg(name, 0, 1, doc) }

// opt returns a copy of a that accepts an empty argument.
//
//nolint:gocritic // hugeParam: the catalogue is built from values; it is not on a request path
func (a Arg) opt() Arg {
	a.Optional = true
	return a
}

func form(args ...Arg) Form { return Form{Args: args} }

//nolint:gocritic // hugeParam: as opt
func single(a Arg) []Form { return []Form{form(a)} }

func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }

const maxInt32 = math.MaxInt32

// compassNames are the compass gravity types in imgproxy's documented
// order; compassNames and the compass table are checked equal in tests.
func compassNames() []string {
	return []string{"ce", "no", "so", "ea", "we", "noea", "nowe", "soea", "sowe"}
}

// formatNames are the parseFormat names; best only where allowed.
func formatNames(best bool) []string {
	fs := []string{"jpeg", "jpg", "png", "webp", "avif", "jxl", "heic", "gif", "tiff", "bmp", "ico", "svg"}
	if best {
		fs = append(fs, "best")
	}
	return fs
}

// gravityForms are the crop gravity shapes (gravityCrop) after the
// leading arguments lead.
func gravityForms(lead ...Arg) []Form {
	with := func(f Form) Form {
		f.Args = append(slices.Clone(lead), f.Args...)
		if f.MinItems > 0 {
			f.MinItems += len(lead)
		}
		return f
	}
	offset := func(n string) Arg { return anyNum(n, "offset in pixels (relative when < 1)").opt() }
	return []Form{
		with(form(enumArg("type", compassNames(), "compass gravity").opt(), offset("x_offset"), offset("y_offset"))),
		with(form(constArg("sm"))),
		with(Form{Args: []Arg{constArg("fp"), unitArg("x", "focus point x (0..1)"), unitArg("y", "focus point y (0..1)")}, MinItems: 3}),
		with(Form{Args: []Arg{constArg("obj")}, Rest: []Arg{classArg()}}),
		with(Form{Args: []Arg{constArg("objw")}, Rest: []Arg{classArg(), nonNeg("weight", "class weight")}, MinItems: 3}),
	}
}

// extendArgs are extend:gravity:x:y (gravityExtend: compass or fp).
func extendArgs(prefix string) []Arg {
	return []Arg{
		boolArg(prefix+"extend", "extend the image to the requested size"),
		enumArg(prefix+"gravity", append(compassNames(), "fp"), "extend gravity (compass or fp)").opt(),
		anyNum(prefix+"x_offset", "gravity x offset (fp: 0..1)").opt(),
		anyNum(prefix+"y_offset", "gravity y offset (fp: 0..1)").opt(),
	}
}

func sizeArgs() []Arg {
	return append([]Arg{
		intArg("width", 0, maxInt32, "width in pixels; 0 keeps the aspect ratio").opt(),
		intArg("height", 0, maxInt32, "height in pixels; 0 keeps the aspect ratio").opt(),
		boolArg("enlarge", "allow upscaling"),
	}, extendArgs("")...)
}

func objectsPositionForms() []Form {
	off := func(n string) Arg {
		return numArg(n, -transform.MaxDimension, transform.MaxDimension, "offset in pixels").opt()
	}
	return []Form{
		form(enumArg("type", compassNames(), "compass position"), off("x_offset"), off("y_offset")),
		form(constArg("fp"), unitArg("x", "x (0..1)").opt(), unitArg("y", "y (0..1)").opt()),
		form(constArg("prop")),
	}
}

func cropForms() []Form {
	dim := func(n string) Arg {
		return numArg(n, 0, maxInt32-1, "0 = full size, < 1 relative, >= 1 pixels")
	}
	return gravityForms(dim("width"), dim("height"))
}

func videoTileArgs(what string) []Arg {
	return []Arg{
		boolArg("extend_"+what, "extend "+what+"s to the requested size"),
		boolArg("trim", "trim the black borders"),
		boolArg("fill", "fill the "+what+" (crop)"),
		unitArg("focus_x", "focus x (0..1)").opt(),
		unitArg("focus_y", "focus y (0..1)").opt(),
	}
}

//nolint:gocritic // hugeParam: as opt
func securityOption(name, alias string, a Arg, doc string) Option {
	return Option{
		Name: name, Aliases: []string{alias}, Forms: single(a), Scope: ScopeRequest, Security: true,
		Syntax: "int", Default: "config", Milestone: "M1", Doc: doc, Example: "10",
	}
}

// ProcessingOptions returns the processing option catalogue in
// URL-DIALECTS §3.2 order.
//
//nolint:maintidx // one flat table is the clearest form
func ProcessingOptions() []Option {
	dim := func(n string) Arg { return intArg(n, 0, maxInt32, n+" in pixels") }
	unitOpt := func(n, doc string) Arg { return unitArg(n, doc).opt() }
	return []Option{
		{
			Name: "resize", Aliases: []string{"rs"}, Scope: ScopePipeline,
			Forms:  []Form{form(append([]Arg{enumArg("type", sortedKeys(resizingTypes), "resizing type").opt()}, sizeArgs()...)...)},
			Syntax: "type:w:h:enlarge:extend(:gravity…)", Default: "—", Milestone: "M1",
			Doc: "Resizing type, size, enlarge and extend in one option.", Example: "fill:300:400:0",
		},
		{
			Name: "size", Aliases: []string{"s"}, Scope: ScopePipeline, Forms: []Form{form(sizeArgs()...)},
			Syntax: "w:h:enlarge:extend", Default: "—", Milestone: "M1",
			Doc: "Size, enlarge and extend in one option.", Example: "300:400:1",
		},
		{
			Name: "resizing_type", Aliases: []string{"rt"}, Scope: ScopePipeline,
			Forms:  single(enumArg("type", sortedKeys(resizingTypes), "resizing type")),
			Syntax: "fit, fill, fill-down, force, auto", Default: "fit", Milestone: "M1",
			Doc: "How the image fits the requested size.", Example: "fill",
		},
		{
			Name: "resizing_algorithm", Aliases: []string{"ra"}, Scope: ScopePipeline, Pro: true,
			Forms:  single(enumArg("algorithm", sortedKeys(kernels), "resampling kernel")),
			Syntax: "nearest, linear, cubic, lanczos2, lanczos3", Default: "lanczos3", Milestone: "M1",
			Doc: "Resampling kernel.", Example: "lanczos3",
		},
		{
			Name: "width", Aliases: []string{"w"}, Scope: ScopePipeline, Forms: single(dim("width")),
			Syntax: "int", Default: "0", Milestone: "M1", Doc: "Target width; 0 keeps the aspect ratio.", Example: "300",
		},
		{
			Name: "height", Aliases: []string{"h"}, Scope: ScopePipeline, Forms: single(dim("height")),
			Syntax: "int", Default: "0", Milestone: "M1", Doc: "Target height; 0 keeps the aspect ratio.", Example: "200",
		},
		{
			Name: "min-width", Aliases: []string{"mw"}, Scope: ScopePipeline, Forms: single(dim("min_width")),
			Syntax: "int", Default: "0", Milestone: "M1", Doc: "Minimum result width.", Example: "100",
		},
		{
			Name: "min-height", Aliases: []string{"mh"}, Scope: ScopePipeline, Forms: single(dim("min_height")),
			Syntax: "int", Default: "0", Milestone: "M1", Doc: "Minimum result height.", Example: "100",
		},
		{
			Name: "zoom", Aliases: []string{"z"}, Scope: ScopePipeline,
			Forms:  []Form{form(posArg("x", "horizontal zoom factor"), posArg("y", "vertical zoom factor (default: x)"))},
			Syntax: "x[:y]", Default: "1", Milestone: "M1", Doc: "Zoom factor applied after resizing.", Example: "1.5:2",
		},
		{
			Name: "dpr", Scope: ScopePipeline, Forms: single(posArg("dpr", "device pixel ratio")),
			Syntax: "float", Default: "1", Milestone: "M1", Doc: "Device pixel ratio multiplying the size.", Example: "2",
		},
		{
			Name: "enlarge", Aliases: []string{"el"}, Scope: ScopePipeline, Forms: single(boolArg("enlarge", "allow upscaling")),
			Syntax: "bool", Default: "false", Milestone: "M1", Doc: "Allow upscaling.", Example: "1",
		},
		{
			Name: "extend", Aliases: []string{"ex"}, Scope: ScopePipeline, Forms: []Form{form(extendArgs("")...)},
			Syntax: "bool:gravity:x:y", Default: "false:ce:0:0", Milestone: "M1",
			Doc: "Extend the image to the requested size with the background.", Example: "1:so",
		},
		{
			Name: "extend_aspect_ratio", Aliases: []string{"extend_ar", "exar"}, Scope: ScopePipeline, Forms: []Form{form(extendArgs("")...)},
			Syntax: "bool:gravity:x:y", Default: "false", Milestone: "M1",
			Doc: "Extend the image to the requested aspect ratio.", Example: "1:ce",
		},
		{
			Name: "gravity", Aliases: []string{"g"}, Scope: ScopePipeline, Forms: gravityForms(),
			Syntax:    "no,so,ea,we,noea,nowe,soea,sowe,ce[:x:y]; `sm`; `fp:x:y`; `obj:classes…` Pro; `objw:class:weight…` Pro",
			Default:   "ce:0:0",
			Milestone: "M1 (sm, fp), M3 (obj, objw: WP-72)",
			Doc:       "Where to crop from when the aspect ratio changes; `sm` crops on faces when there are some (XI_DETECT_SMART_FACES).", Example: "fp:0.5:0.3",
		},
		{
			Name: "objects_position", Aliases: []string{"obj_pos", "op"}, Scope: ScopePipeline, Pro: true,
			Forms: objectsPositionForms(), Syntax: "type:x:y, `fp:x:y`, `prop`", Default: "ce", Milestone: "M3 (WP-72)",
			Doc: "Position of the detected objects' box in the crop (face, obj and objw gravity).", Example: "prop",
		},
		{
			Name: "crop", Aliases: []string{"c"}, Scope: ScopePipeline, Forms: cropForms(),
			Syntax: "w:h[:gravity] (abs ≥1, rel <1)", Default: "—", Milestone: "M1",
			Doc: "Crop before resizing.", Example: "300:200:nowe",
		},
		{
			Name: "crop_aspect_ratio", Aliases: []string{"crop_ar", "car"}, Scope: ScopePipeline, Pro: true,
			Forms:  []Form{form(numArg("aspect_ratio", 0, transform.MaxFactor, "width / height; 0 = off").opt(), boolArg("enlarge", "enlarge the crop area"))},
			Syntax: "ratio:enlarge", Default: "0", Milestone: "M2",
			Doc: "Correct the crop area (the whole image without crop) to an aspect ratio.", Example: "1.5:1",
		},
		{
			Name: "trim", Aliases: []string{"t"}, Scope: ScopePipeline,
			Forms: []Form{form(
				anyNum("threshold", "colour distance threshold; empty disables trimming").opt(),
				colorArg("color", "colour to trim (default: top-left pixel)").opt(),
				boolArg("equal_hor", "trim left and right equally"),
				boolArg("equal_ver", "trim top and bottom equally"),
			)},
			Syntax: "threshold:color:equal_hor:equal_ver", Default: "—", Milestone: "M1",
			Doc: "Remove borders of a uniform colour.", Example: "10:ffffff:1:1",
		},
		{
			Name: "padding", Aliases: []string{"pd"}, Scope: ScopePipeline,
			Forms: []Form{form(
				intArg("top", 0, maxInt32, "top padding").opt(), intArg("right", 0, maxInt32, "right padding (default: top)").opt(),
				intArg("bottom", 0, maxInt32, "bottom padding (default: top)").opt(), intArg("left", 0, maxInt32, "left padding (default: right)").opt(),
			)},
			Syntax: "t:r:b:l (CSS shorthand)", Default: "—", Milestone: "M1",
			Doc: "Padding with the background colour, CSS shorthand.", Example: "10:20",
		},
		{
			Name: "auto_rotate", Aliases: []string{"ar"}, Scope: ScopePipeline, Forms: single(boolArg("auto_rotate", "apply the EXIF orientation")),
			Syntax: "bool", Default: "config (true)", Milestone: "M1", Doc: "Apply the EXIF orientation.", Example: "1",
		},
		{
			Name: "rotate", Aliases: []string{"rot"}, Scope: ScopePipeline,
			Forms:  single(Arg{Name: "angle", Type: TypeInteger, Min: math.MinInt32, Max: maxInt32, HasMin: true, HasMax: true, MultipleOf: 90, Doc: "degrees, a multiple of 90"}),
			Syntax: "multiple of 90", Default: "0", Milestone: "M1", Doc: "Rotate after the EXIF orientation.", Example: "90",
		},
		{
			Name: "flip", Aliases: []string{"fl"}, Scope: ScopePipeline,
			Forms:  []Form{form(boolArg("horizontal", "flip horizontally"), boolArg("vertical", "flip vertically"))},
			Syntax: "h:v bools", Default: "false:false", Milestone: "M1", Doc: "Mirror the image.", Example: "1:0",
		},
		{
			Name: "background", Aliases: []string{"bg"}, Scope: ScopePipeline,
			Forms: []Form{
				form(colorArg("color", "hex colour; empty removes the background").opt()),
				{Args: []Arg{intArg("r", 0, 255, "red"), intArg("g", 0, 255, "green"), intArg("b", 0, 255, "blue")}, MinItems: 3},
			},
			Syntax: "r:g:b or hex", Default: "none", Milestone: "M1",
			Doc: "Background colour for transparency, extend and padding.", Example: "ffffff",
		},
		{
			Name: "background_alpha", Aliases: []string{"bga"}, Scope: ScopePipeline, Pro: true,
			Forms:  single(unitArg("alpha", "background opacity (0..1)")),
			Syntax: "0..1", Default: "1", Milestone: "M2", Doc: "Background opacity.", Example: "0.5",
		},
		{
			Name: "adjust", Aliases: []string{"a"}, Scope: ScopePipeline, Pro: true,
			Forms: []Form{form(
				numArg("brightness", -transform.MaxBrightness, transform.MaxBrightness, "brightness").opt(),
				numArg("contrast", 0, transform.MaxFactor, "contrast factor").opt(),
				numArg("saturation", 0, transform.MaxFactor, "saturation factor").opt(),
			)},
			Syntax: "brightness:contrast:saturation", Default: "—", Milestone: "M2",
			Doc: "Brightness, contrast and saturation in one option.", Example: "10:1.1:0.9",
		},
		{
			Name: "brightness", Aliases: []string{"br"}, Scope: ScopePipeline, Pro: true,
			Forms:  single(numArg("brightness", -transform.MaxBrightness, transform.MaxBrightness, "brightness")),
			Syntax: "−255..255", Default: "0", Milestone: "M2", Doc: "Brightness.", Example: "10",
		},
		{
			Name: "contrast", Aliases: []string{"co"}, Scope: ScopePipeline, Pro: true,
			Forms:  single(numArg("contrast", 0, transform.MaxFactor, "contrast factor")),
			Syntax: "float", Default: "1", Milestone: "M2", Doc: "Contrast factor.", Example: "1.2",
		},
		{
			Name: "saturation", Aliases: []string{"sa"}, Scope: ScopePipeline, Pro: true,
			Forms:  single(numArg("saturation", 0, transform.MaxFactor, "saturation factor")),
			Syntax: "float", Default: "1", Milestone: "M2", Doc: "Saturation factor.", Example: "0.8",
		},
		{
			Name: "monochrome", Aliases: []string{"mc"}, Scope: ScopePipeline, Pro: true,
			Forms:  []Form{form(unitOpt("intensity", "effect intensity (0..1)"), colorArg("color", "tint colour").opt())},
			Syntax: "intensity:color", Default: "0:b3b3b3", Milestone: "M2", Doc: "Monochrome effect.", Example: "1:b3b3b3",
		},
		{
			Name: "duotone", Aliases: []string{"dt"}, Scope: ScopePipeline, Pro: true,
			Forms:  []Form{form(unitOpt("intensity", "effect intensity (0..1)"), colorArg("color1", "shadow colour").opt(), colorArg("color2", "highlight colour").opt())},
			Syntax: "intensity:c1:c2", Default: "0:000000:ffffff", Milestone: "M2", Doc: "Duotone effect.", Example: "1:000000:ffffff",
		},
		{
			Name: "blur", Aliases: []string{"bl"}, Scope: ScopePipeline, Forms: single(nonNeg("sigma", "Gaussian sigma")),
			Syntax: "sigma", Default: "off", Milestone: "M1", Doc: "Gaussian blur.", Example: "2",
		},
		{
			Name: "sharpen", Aliases: []string{"sh"}, Scope: ScopePipeline, Forms: single(nonNeg("sigma", "sharpening sigma")),
			Syntax: "sigma", Default: "off", Milestone: "M1", Doc: "Sharpen.", Example: "0.5",
		},
		{
			Name: "pixelate", Aliases: []string{"pix"}, Scope: ScopePipeline, Forms: single(intArg("size", 0, maxInt32, "pixel block size")),
			Syntax: "size", Default: "off", Milestone: "M1", Doc: "Pixelate.", Example: "8",
		},
		{
			Name: "unsharp_masking", Aliases: []string{"ush"}, Scope: ScopePipeline, Pro: true,
			Forms: []Form{form(
				enumArg("mode", sortedKeys(unsharpModes), "when to sharpen").opt(),
				numArg("weight", 0, transform.MaxFactor, "sharpening weight").opt(),
				numArg("divider", 0, transform.MaxFactor, "divider").opt(),
			)},
			Syntax: "mode:weight:divider (3×3 kernel, ARCHITECTURE §8.1 step 8)", Default: "auto:1:24 when given; off otherwise", Milestone: "M2 (WP-61)", Doc: "Unsharp masking after downscaling.", Example: "always:1:24",
		},
		{
			Name: "blur_areas", Aliases: []string{"ba"}, Scope: ScopePipeline, Pro: true,
			Forms: []Form{{
				Args: []Arg{numArg("sigma", 0, transform.MaxSigma, "blur sigma; 0 = off")},
				Rest: []Arg{unitArg("left", "left (0..1)"), unitArg("top", "top (0..1)"), unitArg("width", "width (0..1)"), unitArg("height", "height (0..1)")},
			}},
			Syntax: "sigma:l:t:w:h… (relative to the oriented source; follow rotate/flip/crops)", Default: "off", Milestone: "M2 (WP-61)", Doc: "Blur rectangular areas (relative coordinates).", Example: "10:0:0:0.5:0.5",
		},
		{
			Name: "progressive_blur", Aliases: []string{"pbl"}, Scope: ScopePipeline, Pro: true,
			Forms: []Form{form(
				numArg("sigma", 0, transform.MaxSigma, "blur sigma; 0 = off").opt(),
				Arg{Name: "direction", Type: TypeDirection, Optional: true, Doc: "down, up, left, right or degrees clockwise (0 = down, 90 = left)"},
				unitOpt("start", "start of the ramp (0..1)"), unitOpt("stop", "end of the ramp (0..1)"),
			)},
			Syntax: "sigma:direction:start:stop; direction as `gradient`", Default: "off (…:down:0:1)", Milestone: "M2 (WP-61)",
			Doc: "Blur that increases along a direction.", Example: "10:down:0.5:1",
		},
		{
			Name: "blur_detections", Aliases: []string{"bd"}, Scope: ScopePipeline, Pro: true,
			Forms:  []Form{{Args: []Arg{numArg("sigma", 0, transform.MaxSigma, "blur sigma; 0 = off")}, Rest: []Arg{classArg()}}},
			Syntax: "sigma:classes…", Default: "off", Milestone: "M3 (WP-73)", Doc: "Blur detected objects.", Example: "10:face",
		},
		{
			Name: "draw_detections", Aliases: []string{"dd"}, Scope: ScopePipeline, Pro: true,
			Forms:  []Form{{Args: []Arg{boolArg("draw", "draw boxes")}, Rest: []Arg{classArg()}}},
			Syntax: "bool:classes…", Default: "off", Milestone: "M3 (WP-73)", Doc: "Draw boxes and labels around detected objects.", Example: "1:face",
		},
		{
			Name: "crop_objects", Aliases: []string{"c_obj"}, Scope: ScopePipeline, Pro: true,
			Forms:  []Form{{Args: []Arg{numArg("scale", 0, transform.MaxFactor, "scale factor of the object box; 0 = off")}, Rest: []Arg{classArg()}}},
			Syntax: "scale:classes…", Default: "off", Milestone: "M3 (WP-72)", Doc: "Crop to detected objects before resizing.", Example: "1.2:face",
		},
		{
			Name: "colorize", Aliases: []string{"col"}, Scope: ScopePipeline, Pro: true,
			Forms:  []Form{form(unitOpt("opacity", "overlay opacity (0..1)"), colorArg("color", "overlay colour").opt(), boolArg("keep_alpha", "keep the alpha channel"))},
			Syntax: "opacity:color:keep_alpha", Default: "off", Milestone: "M2", Doc: "Colour overlay.", Example: "0.5:ff0000:1",
		},
		{
			Name: "gradient", Aliases: []string{"gr"}, Scope: ScopePipeline, Pro: true,
			Forms: []Form{form(
				unitOpt("opacity", "gradient opacity (0..1)"), colorArg("color", "gradient colour").opt(),
				Arg{Name: "direction", Type: TypeDirection, Optional: true, Doc: "down, up, left, right or degrees clockwise (0 = down, 90 = left)"},
				unitOpt("start", "start (0..1)"), unitOpt("stop", "stop (0..1)"),
			)},
			Syntax: "opacity:color:direction:start:stop; direction `down`/`up`/`left`/`right` or degrees clockwise like imgproxy (0 = top to bottom like `down`, 90 = right to left like `left`)", Default: "0:000:down:0:1", Milestone: "M2",
			Doc: "Gradient overlay.", Example: "0.5:000000:down:0.5:1",
		},
		{
			Name: "watermark", Aliases: []string{"wm"}, Scope: ScopePipeline,
			Forms: []Form{form(
				unitArg("opacity", "opacity (0..1); 0 disables"),
				enumArg("position", append(compassNames(), "re", "ch"), "compass position, re (tile) or ch (chessboard)").opt(),
				anyNum("x_offset", "x offset").opt(), anyNum("y_offset", "y offset").opt(),
				posArg("scale", "scale relative to the image").opt(),
			)},
			Syntax: "opacity:pos:x:y:scale; pos incl. `re` (tile), `ch` (chessboard, Pro)", Default: "off", Milestone: "M1 (`re` M1, `ch` M3 WP-77)",
			Doc: "Draw the watermark.", Example: "0.5:soea:10:10:0.2",
		},
		{
			Name: "watermark_url", Aliases: []string{"wmu"}, Scope: ScopePipeline, Pro: true,
			Forms:  single(base64Arg("url", "url-safe base64 of the watermark source URL; empty = configured image").opt()),
			Syntax: "base64url URL", Default: "config image", Milestone: "M3 (WP-77)", Doc: "Watermark image source, fetched like a source (allowlist, SSRF policy, byte limit, origin cache).", Example: "aHR0cHM6Ly9leGFtcGxlLmNvbS93LnBuZw",
		},
		{
			Name: "watermark_text", Aliases: []string{"wmt"}, Scope: ScopePipeline, Pro: true,
			Forms:  single(base64Arg("text", "url-safe base64 of Pango markup").opt()),
			Syntax: "base64url Pango markup", Default: "—", Milestone: "M3 (WP-78)", Doc: "Text watermark (replaces watermark_url); markup is sanitised to a whitelist, font `sans 16` by default.", Example: "SGVsbG8",
		},
		{
			Name: "watermark_size", Aliases: []string{"wms"}, Scope: ScopePipeline, Pro: true,
			Forms:  []Form{form(intArg("width", 0, transform.MaxDimension, "width; 0 = auto").opt(), intArg("height", 0, transform.MaxDimension, "height; 0 = auto").opt())},
			Syntax: "w:h", Default: "0:0", Milestone: "M3 (WP-77)", Doc: "Watermark size in pixels (fit, enlarging; used when the wm scale is 0).", Example: "100:0",
		},
		{
			Name: "watermark_rotate", Aliases: []string{"wm_rot", "wmr"}, Scope: ScopePipeline, Pro: true,
			Forms:  single(anyNum("angle", "degrees")),
			Syntax: "degrees", Default: "0", Milestone: "M3 (WP-77)", Doc: "Watermark rotation, clockwise.", Example: "45",
		},
		{
			Name: "watermark_shadow", Aliases: []string{"wmsh"}, Scope: ScopePipeline, Pro: true,
			Forms:  single(numArg("sigma", 0, transform.MaxSigma, "shadow blur sigma")),
			Syntax: "sigma", Default: "off", Milestone: "M3 (WP-77)", Doc: "Watermark shadow: its alpha blurred with sigma, in black.", Example: "2",
		},
		{
			Name: "style", Aliases: []string{"stl", "st"}, Scope: ScopePipeline, Pro: true,
			Forms:  single(base64Arg("css", "url-safe base64 of CSS")),
			Syntax: "base64url CSS", Default: "—", Milestone: "M2 (WP-64)", Doc: "CSS applied to SVG sources.", Example: "c3Zn",
		},
		{
			Name: "strip_metadata", Aliases: []string{"sm"}, Scope: ScopeRequest, Forms: single(boolArg("strip", "strip metadata")),
			Syntax: "bool", Default: "config (true)", Milestone: "M1", Doc: "Strip metadata from the result.", Example: "1",
		},
		{
			Name: "keep_copyright", Aliases: []string{"kcr"}, Scope: ScopeRequest, Forms: single(boolArg("keep", "keep copyright metadata")),
			Syntax: "bool", Default: "config (true)", Milestone: "M1", Doc: "Keep copyright when stripping metadata.", Example: "1",
		},
		{
			Name: "dpi", Scope: ScopeRequest, Pro: true, Forms: single(intArg("dpi", 0, transform.MaxDPI, "DPI; 0 = unchanged")),
			Syntax: "int", Default: "0", Milestone: "M2 (WP-64)", Doc: "DPI metadata of the result.", Example: "300",
		},
		{
			Name: "strip_color_profile", Aliases: []string{"scp"}, Scope: ScopeRequest, Forms: single(boolArg("strip", "convert to sRGB and drop the profile")),
			Syntax: "bool", Default: "config (true)", Milestone: "M1", Doc: "Convert to sRGB and drop the ICC profile.", Example: "1",
		},
		{
			Name: "preserve_hdr", Aliases: []string{"ph"}, Scope: ScopeRequest, Forms: single(boolArg("preserve", "keep HDR data")),
			Syntax: "bool", Default: "config (false)", Milestone: "M2", Doc: "Keep HDR data (gain maps, high bit depth).", Example: "1",
		},
		{
			Name: "color_profile", Aliases: []string{"cp", "icc"}, Scope: ScopeRequest, Pro: true,
			Forms:  single(strArg("profile", "srgb, cmyk or a profile file name (no path)").opt()),
			Syntax: "srgb, cmyk, file", Default: "—", Milestone: "M2", Doc: "Target colour profile.", Example: "srgb",
		},
		{
			Name: "enforce_thumbnail", Aliases: []string{"eth"}, Scope: ScopeRequest, Forms: single(boolArg("enforce", "use the embedded thumbnail")),
			Syntax: "bool", Default: "config", Milestone: "M2 (WP-64)", Doc: "Use the embedded thumbnail when present.", Example: "1",
		},
		{
			Name: "quality", Aliases: []string{"q"}, Scope: ScopeRequest, Forms: single(intArg("quality", 0, transform.MaxQuality, "0 = format default")),
			Syntax: "0..100", Default: "0 (config)", Milestone: "M1", Doc: "Encoder quality.", Example: "80",
		},
		{
			Name: "format_quality", Aliases: []string{"fq"}, Scope: ScopeRequest,
			Forms: []Form{{
				Rest:     []Arg{enumArg("format", formatNames(false), "output format"), intArg("quality", 0, transform.MaxQuality, "0 removes the entry")},
				MinItems: 2,
			}},
			Syntax: "fmt:q…", Default: "—", Milestone: "M1", Doc: "Quality per output format.", Example: "webp:70:avif:50",
		},
		{
			Name: "autoquality", Aliases: []string{"aq"}, Scope: ScopeRequest, Pro: true,
			Forms: []Form{form(
				enumArg("method", sortedKeys(autoQualityMethods), "search method").opt(),
				numArg("target", 0, transform.MaxAutoQualityTarget, "target (bytes for size, DSSIM otherwise)").opt(),
				intArg("min_quality", 0, transform.MaxQuality, "lowest quality").opt(),
				intArg("max_quality", 0, transform.MaxQuality, "highest quality").opt(),
				unitOpt("allowed_error", "allowed error (0..1)"),
			)},
			Syntax:  "method:target:min:max:allowed_error; method `none`/`size`/`dssim`/`ml` (`ml` parsed, run as no auto-quality and reported skipped); `size` target in bytes (≤ 2³⁰)",
			Default: "config", Milestone: "M2 (WP-56, FORMATS §6.4)", Doc: "Search the lowest quality that meets a target.", Example: "dssim:0.02:70:85:0.001",
		},
		{
			Name: "max_bytes", Aliases: []string{"mb"}, Scope: ScopeRequest, Forms: single(intArg("bytes", 0, maxInt32, "0 = no limit")),
			Syntax: "int", Default: "0", Milestone: "M1", Doc: "Lower the quality until the result fits.", Example: "100000",
		},
		{
			Name: "jpeg_options", Aliases: []string{"jpgo"}, Scope: ScopeRequest, Pro: true,
			Forms: []Form{form(
				boolArg("progressive", "progressive JPEG"), boolArg("no_subsample", "disable chroma subsampling"),
				boolArg("trellis_quant", "trellis quantisation"), boolArg("overshoot_deringing", "overshoot deringing"),
				boolArg("optimize_scans", "optimise progressive scans"),
				intArg("quant_table", 0, transform.MaxJPEGQuant, "quantisation table").opt(),
			)},
			Syntax: "progressive:no_subsample:trellis_quant:overshoot_deringing:optimize_scans:quant_table", Default: "config", Milestone: "M2 (WP-64)",
			Doc: "JPEG encoder settings.", Example: "1:0:1:1:1:3",
		},
		{
			Name: "png_options", Aliases: []string{"pngo"}, Scope: ScopeRequest, Pro: true,
			Forms: []Form{form(
				boolArg("interlaced", "Adam7 interlacing"), boolArg("quantize", "palette quantisation"),
				intArg("quantization_colors", 0, transform.MaxPaletteSize, "palette size, 2..256; 0 = default").opt(),
			)},
			Syntax: "interlaced:quantize:quantization_colors", Default: "config", Milestone: "M2 (WP-64)", Doc: "PNG encoder settings.", Example: "0:1:128",
		},
		{
			Name: "webp_options", Aliases: []string{"webpo"}, Scope: ScopeRequest, Pro: true,
			Forms: []Form{form(
				enumArg("compression", sortedKeys(webpCompressions), "compression").opt(),
				boolArg("smart_subsample", "smart chroma subsampling"),
				enumArg("preset", sortedKeys(webpPresets), "encoder preset").opt(),
			)},
			Syntax: "compression:smart_subsample:preset", Default: "config", Milestone: "M2 (WP-64)", Doc: "WebP encoder settings.", Example: "lossy:1:photo",
		},
		{
			Name: "avif_options", Aliases: []string{"avifo"}, Scope: ScopeRequest, Pro: true,
			Forms:  single(enumArg("subsample", sortedKeys(avifSubsamples), "auto, on (4:2:0) or off (4:4:4)")),
			Syntax: "subsample", Default: "config", Milestone: "M2 (WP-64)", Doc: "AVIF encoder settings.", Example: "off",
		},
		{
			Name: "format", Aliases: []string{"f", "ext"}, Scope: ScopeRequest,
			Forms:  single(enumArg("format", formatNames(true), "output format; best picks the smallest (Pro)")),
			Syntax: "extension or `best` Pro", Default: "source/negotiated", Milestone: "M1 (`best` M2)", Doc: "Output format.", Example: "webp",
		},
		{
			Name: "page", Aliases: []string{"pg"}, Scope: ScopePipeline, Pro: true, Forms: single(intArg("page", 0, maxInt32, "first page, from 0")),
			Syntax: "int", Default: "0", Milestone: "M2 (WP-50, WP-62)", Doc: "First page of a multi-page source.", Example: "1",
		},
		{
			Name: "pages", Aliases: []string{"pgs"}, Scope: ScopePipeline, Pro: true, Forms: single(intArg("pages", -1, maxInt32, "number of pages, ≥ 1, or -1 for all")),
			Syntax: "int", Default: "1", Milestone: "M2 (WP-50, WP-62)", Doc: "Number of pages stacked into the result.", Example: "2",
		},
		{
			Name: "disable_animation", Aliases: []string{"da"}, Scope: ScopePipeline, Pro: true, Forms: single(boolArg("disable", "use the first frame only")),
			Syntax: "bool", Default: "false", Milestone: "M1", Doc: "Process only the first frame.", Example: "1",
		},
		{
			Name: "video_thumbnail_second", Aliases: []string{"vts"}, Scope: ScopePipeline, Pro: true, Forms: single(numArg("second", -maxInt32, maxInt32, "timestamp in seconds; negative counts from the end")),
			Syntax: "float", Default: "config", Milestone: "M3 (WP-74)", Doc: "Video frame to use as the image (negative: from the end; unset: the configured second or the best frame).", Example: "5",
		},
		{
			Name: "video_thumbnail_keyframes", Aliases: []string{"vtk"}, Scope: ScopePipeline, Pro: true, Forms: single(boolArg("keyframes", "use keyframes only")),
			Syntax: "bool", Default: "config", Milestone: "M3 (WP-74)", Doc: "Seek to keyframes only.", Example: "1",
		},
		{
			Name: "video_thumbnail_tile", Aliases: []string{"vtt"}, Scope: ScopePipeline, Pro: true,
			Forms: []Form{form(append([]Arg{
				numArg("step", -maxInt32, maxInt32, "seconds between frames; 0 = off, < 0 = from the duration"),
				intArg("columns", 0, transform.MaxDimension, "columns").opt(), intArg("rows", 0, transform.MaxDimension, "rows").opt(),
				intArg("tile_width", 0, transform.MaxDimension, "tile width").opt(), intArg("tile_height", 0, transform.MaxDimension, "tile height").opt(),
			}, videoTileArgs("tile")...)...)},
			Syntax: "step:cols:rows:tw:th:extend:trim:fill:fx:fy; step 0 = off, < 0 = from duration (stored as 0)", Default: "off", Milestone: "M3",
			Doc: "Sprite sheet of video frames.", Example: "1:4:4:160:90",
		},
		{
			Name: "video_thumbnail_animation", Aliases: []string{"vta"}, Scope: ScopePipeline, Pro: true,
			Forms: []Form{form(append([]Arg{
				numArg("step", -maxInt32, maxInt32, "seconds between frames; 0 = off, < 0 = from the duration"),
				numArg("delay", 0, maxInt32, "frame delay in milliseconds").opt(),
				intArg("frames", 0, transform.MaxDimension, "number of frames").opt(),
				intArg("frame_width", 0, transform.MaxDimension, "frame width").opt(), intArg("frame_height", 0, transform.MaxDimension, "frame height").opt(),
			}, videoTileArgs("frame")...)...)},
			Syntax: "step:delay(ms):frames:fw:fh:extend:trim:fill:fx:fy; step as `vtt`", Default: "off", Milestone: "M3",
			Doc: "Animation of video frames.", Example: "1:100:10:320:180",
		},
		{
			Name: "fallback_image_url", Aliases: []string{"fiu"}, Scope: ScopeRequest, Pro: true,
			Forms:  single(base64Arg("url", "url-safe base64 of the fallback source URL; empty = configured").opt()),
			Syntax: "base64url URL", Default: "config", Milestone: "M1 (WP-34c)",
			Doc: "Image served when the source fails (XI_FALLBACK_ALLOW_REQUEST_URL).", Example: "aHR0cHM6Ly9leGFtcGxlLmNvbS9mLnBuZw",
		},
		{
			Name: "skip_processing", Aliases: []string{"skp", "sp"}, Scope: ScopeRequest,
			Forms:  []Form{{Rest: []Arg{enumArg("format", formatNames(false), "source format served as is")}}},
			Syntax: "ext…", Default: "—", Milestone: "M1", Doc: "Serve sources of these formats unprocessed.", Example: "svg:gif",
		},
		{
			Name: "raw", Scope: ScopeRequest, Forms: single(boolArg("raw", "stream the source unprocessed")),
			Syntax: "bool", Default: "false", Milestone: "M1", Doc: "Stream the source without processing.", Example: "1",
		},
		{
			Name: "cachebuster", Aliases: []string{"cb"}, Scope: ScopeRequest, Forms: single(strArg("value", "any string").opt()),
			Syntax: "string", Default: "—", Milestone: "M1", Doc: "Changes the URL, the cache key and the ETag.", Example: "v2",
		},
		{
			Name: "bypass_cache", Aliases: []string{"bc"}, Scope: ScopeRequest, Pro: true, Forms: single(boolArg("bypass", "skip the result cache")),
			Syntax: "bool", Default: "false", Milestone: "M2 (WP-52)", Doc: "Skip the result cache (XI_CACHE_ALLOW_BYPASS).", Example: "1",
		},
		{
			Name: "cache_tags", Aliases: []string{"ct"}, Scope: ScopeRequest, Pro: true,
			Forms:  []Form{{Rest: []Arg{{Name: "tag", Type: TypeString, Pattern: `^[!-+\--~]*$`, Optional: true, Doc: "visible ASCII without commas; a single empty tag clears"}}}},
			Syntax: "tags… (visible ASCII, no commas; replaces earlier tags)", Default: "—", Milestone: "M2 (WP-34b, WP-64 end-to-end test)",
			Doc: "Tags for the Cache-Tag response header.", Example: "product:hero",
		},
		{
			Name: "expires", Aliases: []string{"exp"}, Scope: ScopeRequest,
			Forms:  single(Arg{Name: "timestamp", Type: TypeInteger, Doc: "unix seconds; ≤ 0 never expires"}),
			Syntax: "unix ts", Default: "—", Milestone: "M1", Doc: "URL expiry (404 afterwards).", Example: "4102444800",
		},
		{
			Name: "filename", Aliases: []string{"fn"}, Scope: ScopeRequest,
			Forms:  []Form{form(strArg("name", "file name without extension").opt(), boolArg("encoded", "name is url-safe base64"))},
			Syntax: "name:encoded", Default: "source basename", Milestone: "M1", Doc: "Content-Disposition file name.", Example: "photo",
		},
		{
			Name: "return_attachment", Aliases: []string{"att"}, Scope: ScopeRequest, Forms: single(boolArg("attachment", "Content-Disposition: attachment")),
			Syntax: "bool", Default: "config", Milestone: "M1", Doc: "Download instead of inline display.", Example: "1",
		},
		{
			Name: "preset", Aliases: []string{"pr"}, Scope: ScopePipeline,
			Forms:  []Form{{Rest: []Arg{{Name: "name", Type: TypeString, Pattern: `^.+$`, Doc: "preset name"}}}},
			Syntax: "names…", Default: "—", Milestone: "M1", Doc: "Expand presets in place.", Example: "thumbnail",
		},
		{
			Name: "hashsum", Aliases: []string{"hs"}, Scope: ScopeRequest, Pro: true,
			Forms: []Form{form(
				enumArg("type", append([]string{"none"}, sortedKeys(hashAlgorithms)...), "hash algorithm; none or empty removes the check").opt(),
				Arg{Name: "hashsum", Type: TypeString, Pattern: `^[0-9a-fA-F]*$`, Optional: true, Doc: "hex digest"},
			)},
			Syntax: "none, or md5/sha1/sha256/sha512:hex", Default: "none", Milestone: "M2 (WP-64)", Doc: "Check the source bytes against a digest.", Example: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		securityOption("max_src_resolution", "msr", nonNeg("megapixels", "megapixels"), "Lower (or raise) the source resolution limit."),
		securityOption("max_src_file_size", "msfs", intArg("bytes", 0, maxInt32, "bytes"), "Lower (or raise) the source size limit."),
		securityOption("max_animation_frames", "maf", intArg("frames", 1, maxInt32, "frames"), "Lower (or raise) the animation frame limit."),
		securityOption("max_animation_frame_resolution", "mafr", nonNeg("megapixels", "megapixels"), "Lower (or raise) the frame resolution limit."),
		securityOption("max_result_dimension", "mrd", intArg("pixels", 0, maxInt32, "pixels"), "Lower (or raise) the result dimension limit."),
		{
			Name: "gif_options", Aliases: []string{"gifo"}, Scope: ScopeRequest, Removed: true,
			Forms:  []Form{{Rest: []Arg{strArg("value", "ignored").opt()}}},
			Syntax: "—", Default: "—", Milestone: "—", Doc: "Removed upstream; accepted and ignored for old URLs.", Example: "1",
		},
	}
}

// InfoOptions returns the /info option catalogue in URL-DIALECTS §3.3
// order. Shared entries are the processing options of the same name.
func InfoOptions() []Option {
	flag := func(name string, aliases []string, def, ms, doc string) Option {
		return Option{
			Name: name, Aliases: aliases, Scope: ScopeRequest, Forms: single(boolArg(name, doc)),
			Syntax: "bool", Default: def, Milestone: ms, Doc: doc, Example: "1",
		}
	}
	pro := func(o Option) Option {
		o.Pro = true
		return o
	}
	opts := []Option{
		flag("size", []string{"s"}, "true", "M1", "Report the source size in bytes."),
		flag("format", []string{"f"}, "true", "M1", "Report the format and media type."),
		flag("dimensions", []string{"d"}, "true", "M1", "Report width, height and orientation."),
		{
			Name: "exif", Scope: ScopeRequest,
			Forms:  []Form{form(boolArg("enabled", "report EXIF"), boolArg("canonical_names", "canonical tag names (not implemented)"))},
			Syntax: "enabled:canonical_names", Default: "true:false", Milestone: "M1", Doc: "Report EXIF tags.", Example: "1:0",
		},
		flag("iptc", nil, "true", "M1", "Report IPTC datasets (and Photoshop resolution)."),
		flag("xmp", nil, "true", "M1", "Report XMP properties."),
		flag("video_meta", []string{"vm"}, "true", "M3 (WP-74)", "Report video metadata."),
		pro(flag("detect_objects", []string{"do"}, "false", "M3 (WP-73)", "Report detected faces and objects.")),
		{
			Name: "classify", Aliases: []string{"cl"}, Scope: ScopeRequest, Pro: true,
			Forms:  []Form{{Args: []Arg{intArg("top_k", 0, transform.MaxListLen, "number of classes; 0 = off")}, Rest: []Arg{classArg()}}},
			Syntax: "top_k:classes…", Default: "0", Milestone: "not implemented", Doc: "Classify the image (no classification model is shipped).", Example: "3",
		},
		flag("colorspace", []string{"cs"}, "false", "M1", "Report the libvips interpretation."),
		flag("bands", []string{"b"}, "false", "M1", "Report the number of bands."),
		flag("sample_format", []string{"sf"}, "false", "M1", "Report the libvips band format."),
		flag("pages_number", []string{"pn"}, "false", "M1", "Report the number of pages or frames."),
		{
			Name: "alpha", Aliases: []string{"a"}, Scope: ScopeRequest,
			Forms:  []Form{form(boolArg("alpha", "report the alpha channel"), boolArg("check_transparency", "check for transparent pixels (not implemented)"))},
			Syntax: "alpha:check_transparency", Default: "false:false", Milestone: "M1", Doc: "Report whether the source has alpha.", Example: "1",
		},
		{
			Name: "crop", Aliases: []string{"c"}, Scope: ScopeRequest, Forms: cropForms(),
			Syntax: "w:h:gravity (as processing `crop`)", Default: "—", Milestone: "M2", Doc: "Report the crop area.", Example: "300:200:sm",
		},
		{
			Name: "objects_position", Aliases: []string{"obj_pos", "op"}, Scope: ScopeRequest, Pro: true, Forms: objectsPositionForms(),
			Syntax: "as processing", Default: "ce:0:0", Milestone: "M3 (WP-73)", Doc: "Objects position for the reported crop.", Example: "prop",
		},
		{
			Name: "palette", Aliases: []string{"p"}, Scope: ScopeRequest,
			Forms:  single(intArg("colors", 0, transform.MaxPaletteSize, "0 = off, else 2..256")),
			Syntax: "0 or 2..256", Default: "0", Milestone: "M2", Doc: "Report a palette.", Example: "16",
		},
		{
			Name: "average", Aliases: []string{"avg"}, Scope: ScopeRequest,
			Forms:  []Form{form(boolArg("enabled", "report the average colour"), boolArg("ignore_transparent", "ignore transparent pixels (default true)"))},
			Syntax: "enabled:ignore_transparent", Default: "false:true", Milestone: "M2", Doc: "Report the average colour.", Example: "1:1",
		},
		{
			Name: "dominant_colors", Aliases: []string{"dc"}, Scope: ScopeRequest,
			Forms:  []Form{form(boolArg("enabled", "report dominant colours"), boolArg("build_missed", "derive missing colours"))},
			Syntax: "enabled:build_missed", Default: "false:false", Milestone: "M2", Doc: "Report dominant colours.", Example: "1:0",
		},
		{
			Name: "blurhash", Aliases: []string{"bh"}, Scope: ScopeRequest,
			Forms: []Form{{
				Args:     []Arg{intArg("x_components", 0, transform.MaxBlurhashDim, "1..9; 0:0 = off"), intArg("y_components", 0, transform.MaxBlurhashDim, "1..9; 0:0 = off")},
				MinItems: 2,
			}},
			Syntax: "x:y (1..9 each, 0:0 off)", Default: "0:0", Milestone: "M2", Doc: "Report a BlurHash.", Example: "4:3",
		},
		flag("thumbhash", []string{"thumb_hash", "th"}, "false", "M2", "Report a ThumbHash."),
		flag("perceptual_hash", []string{"phash", "ph"}, "false", "M2", "Report a perceptual hash."),
		{
			Name: "calc_hashsums", Aliases: []string{"chs"}, Scope: ScopeRequest,
			Forms:  []Form{{Rest: []Arg{enumArg("type", sortedKeys(hashAlgorithms), "hash algorithm; a single empty argument clears").opt()}}},
			Syntax: "md5, sha1, sha256, sha512…", Default: "—", Milestone: "M2", Doc: "Report source digests.", Example: "md5:sha256",
		},
	}
	proc := ProcessingOptions()
	for i := range proc {
		o := proc[i]
		switch o.Name {
		case "page", "video_thumbnail_second", "video_thumbnail_keyframes", "cachebuster", "expires",
			"preset", "hashsum", "max_src_resolution", "max_src_file_size", "bypass_cache", "cache_tags":
			o.Shared = true
			opts = append(opts, o)
		}
	}
	return opts
}
