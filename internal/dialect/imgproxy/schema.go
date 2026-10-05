package imgproxy

import (
	"slices"
	"strconv"
	"strings"
)

// JSON Schema (draft 2020-12) of the option catalogue, embedded in the
// OpenAPI document as the ImgproxyProcessingOptions and
// ImgproxyInfoOptions components (ADR 0008 decision 4). The options object
// has one property per canonical option name; its value is the argument
// list as a tuple (prefixItems), typed and ranged per argument:
//
//	{"resize": ["fill", 300, 400, false], "gravity": ["sm"], "quality": [80]}
//
// is /rs:fill:300:400:0/g:sm/q:80/. Booleans are JSON booleans (1/0 in
// URLs); null is an empty argument, where the parser accepts one. Chained
// pipelines are one options object each. The x-xi-* annotations carry
// aliases, milestone, the imgproxy Pro flag, scope and the URL syntax.

const processingDescription = "imgproxy processing options (docs/URL-DIALECTS.md §3.2), one property per canonical option name. " +
	"Each value is the option's positional arguments: /name:a:b/ is {\"name\": [a, b]}. " +
	"Booleans are JSON booleans (1/0 in URLs); null is an empty argument (keeps the previous or default value). " +
	"Chained pipelines (\"/-/\") are one object each; request-scoped options apply to the whole request."

const infoDescription = "imgproxy /info options (docs/URL-DIALECTS.md §3.3), encoded like ImgproxyProcessingOptions. " +
	"Info options have their own namespace: s, f, a, c, p and ph mean something else in processing URLs."

const videoDescription = "imgproxy-style /video options (docs/URL-DIALECTS.md §3.8, video transformations), encoded like ImgproxyProcessingOptions. " +
	"Video options have their own namespace: geometry names (rs, s, w, h, g, c, rot, fl, bg, q, f) follow imgproxy's argument rules with video semantics."

// VideoOptionsSchema returns the ImgproxyVideoOptions schema.
func VideoOptionsSchema() map[string]any {
	return optionsSchema(VideoOptions(), videoDescription)
}

// ProcessingOptionsSchema returns the ImgproxyProcessingOptions schema.
func ProcessingOptionsSchema() map[string]any {
	return optionsSchema(ProcessingOptions(), processingDescription)
}

// InfoOptionsSchema returns the ImgproxyInfoOptions schema.
func InfoOptionsSchema() map[string]any {
	return optionsSchema(InfoOptions(), infoDescription)
}

func optionsSchema(opts []Option, desc string) map[string]any {
	props := make(map[string]any, len(opts))
	for i := range opts {
		props[opts[i].Name] = optionSchema(&opts[i])
	}
	return map[string]any{
		"type":                 "object",
		"description":          desc,
		"additionalProperties": false,
		"properties":           props,
	}
}

func optionSchema(o *Option) map[string]any {
	var s map[string]any
	if len(o.Forms) == 1 {
		s = formSchema(&o.Forms[0])
	} else {
		forms := make([]any, len(o.Forms))
		for i := range o.Forms {
			forms[i] = formSchema(&o.Forms[i])
		}
		s = map[string]any{"type": "array", "anyOf": forms}
	}
	s["description"] = o.description()
	s["x-xi-aliases"] = append([]string{}, o.Aliases...)
	s["x-xi-milestone"] = o.Milestone
	s["x-xi-pro"] = o.Pro
	s["x-xi-syntax"] = o.Syntax
	s["x-xi-default"] = o.Default
	s["x-xi-scope"] = o.Scope.String()
	if o.Security {
		s["x-xi-security"] = true
	}
	if o.Removed {
		s["deprecated"] = true
	}
	if o.Example != "" {
		s["examples"] = []any{o.Decode(strings.Split(o.Example, ":"))}
	}
	return s
}

func (o *Option) description() string {
	var b strings.Builder
	b.WriteString(o.Doc)
	b.WriteString(" URL: `")
	b.WriteString(strings.Join(o.Names(), "`, `"))
	b.WriteString("`; arguments ")
	b.WriteString(o.Syntax)
	b.WriteString("; default ")
	b.WriteString(o.Default)
	b.WriteString(".")
	if o.Security {
		b.WriteString(" Needs XI_SECURITY_ALLOW_SECURITY_OPTIONS (403 otherwise).")
	}
	return b.String()
}

// String returns "pipeline" or "request".
func (s Scope) String() string {
	if s == ScopePipeline {
		return "pipeline"
	}
	return "request"
}

func formSchema(f *Form) map[string]any {
	s := map[string]any{"type": "array", "minItems": max(f.MinItems, 1)}
	if len(f.Args) > 0 {
		items := make([]any, len(f.Args))
		for i := range f.Args {
			items[i] = argSchema(&f.Args[i])
		}
		s["prefixItems"] = items
	}
	switch len(f.Rest) {
	case 0:
		s["maxItems"] = len(f.Args)
	case 1:
		s["items"] = argSchema(&f.Rest[0])
	default:
		group := make([]any, len(f.Rest))
		for i := range f.Rest {
			group[i] = argSchema(&f.Rest[i])
		}
		s["items"] = map[string]any{"anyOf": group}
	}
	return s
}

func jsonType(t ArgType) string {
	switch t {
	case TypeInteger:
		return "integer"
	case TypeNumber:
		return "number"
	case TypeBoolean:
		return "boolean"
	default:
		return "string"
	}
}

func argSchema(a *Arg) map[string]any {
	s := map[string]any{"title": a.Name}
	if a.Doc != "" {
		s["description"] = a.Doc
	}
	if a.Type == TypeDirection {
		alts := []any{
			map[string]any{"type": "string", "enum": []any{"down", "up", "left", "right"}},
			map[string]any{"type": "number", "minimum": -360, "maximum": 360},
		}
		if a.Optional {
			alts = append(alts, map[string]any{"type": "null"})
		}
		s["anyOf"] = alts
		return s
	}
	t := jsonType(a.Type)
	if a.Optional {
		s["type"] = []string{t, "null"}
	} else {
		s["type"] = t
	}
	if a.Type == TypeEnum {
		enum := make([]any, 0, len(a.Enum)+1)
		for _, v := range a.Enum {
			enum = append(enum, v)
		}
		if a.Optional {
			enum = append(enum, nil)
		}
		s["enum"] = enum
	}
	if a.HasMin {
		if a.ExclusiveMin {
			s["exclusiveMinimum"] = a.Min
		} else {
			s["minimum"] = a.Min
		}
	}
	if a.HasMax {
		s["maximum"] = a.Max
	}
	if a.MultipleOf != 0 {
		s["multipleOf"] = a.MultipleOf
	}
	if a.Pattern != "" {
		s["pattern"] = a.Pattern
	}
	return s
}

// Decode converts URL arguments ("fill", "300", "", "1") into the options
// object's typed tuple (["fill", 300, null, true]) using the form the
// arguments select. Values that do not parse as their type stay strings,
// so a schema check rejects them.
func (o *Option) Decode(args []string) []any {
	f := o.form(args)
	out := make([]any, len(args))
	for i, s := range args {
		a, ok := f.argAt(i)
		if !ok {
			out[i] = s
			continue
		}
		out[i] = decodeArg(&a, s)
	}
	return out
}

// form returns the first form that accepts args (arity and enum values),
// or the first form.
func (o *Option) form(args []string) *Form {
	for i := range o.Forms {
		if o.Forms[i].accepts(args) {
			return &o.Forms[i]
		}
	}
	return &o.Forms[0]
}

func (f *Form) argAt(i int) (Arg, bool) {
	if i < len(f.Args) {
		return f.Args[i], true
	}
	if len(f.Rest) == 0 {
		return Arg{}, false
	}
	return f.Rest[(i-len(f.Args))%len(f.Rest)], true
}

func (f *Form) accepts(args []string) bool {
	if len(args) < max(f.MinItems, 1) || (len(f.Rest) == 0 && len(args) > len(f.Args)) {
		return false
	}
	for i, s := range args {
		a, ok := f.argAt(i)
		if !ok || (a.Type == TypeEnum && s != "" && !slices.Contains(a.Enum, s)) {
			return false
		}
	}
	return true
}

func decodeArg(a *Arg, s string) any {
	if s == "" && a.Optional {
		return nil
	}
	switch a.Type {
	case TypeInteger:
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
	case TypeNumber, TypeDirection:
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			return v
		}
	case TypeBoolean:
		if v, err := strconv.ParseBool(s); err == nil {
			return v
		}
	default:
	}
	return s
}
