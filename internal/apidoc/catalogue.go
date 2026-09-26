package apidoc

import (
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/playground"
)

// Catalogues returns the option catalogues the playground's option builder
// uses (GET /_playground/api/meta): the imgproxy processing options from
// the declarative catalogue behind the ImgproxyProcessingOptions schema,
// and the query dialect's native keys documented on getQueryImage. Removed
// imgproxy options are left out; the query keys the page sets itself (url
// and the signatures) too.
func Catalogues() []playground.Catalogue {
	return []playground.Catalogue{imgproxyCatalogue(), queryCatalogue()}
}

func imgproxyCatalogue() playground.Catalogue {
	src := imgproxy.ProcessingOptions()
	opts := make([]playground.Option, 0, len(src))
	for i := range src {
		o := &src[i]
		if o.Removed {
			continue
		}
		forms := make([]playground.Form, 0, len(o.Forms))
		for j := range o.Forms {
			f := &o.Forms[j]
			forms = append(forms, playground.Form{Args: args(f.Args), Rest: args(f.Rest), MinItems: f.MinItems})
		}
		opts = append(opts, playground.Option{
			Name: o.Name, Aliases: o.Aliases, Doc: o.Doc, Forms: forms,
			Example: o.Example, Milestone: o.Milestone, Security: o.Security,
		})
	}
	return playground.Catalogue{Grammar: playground.GrammarImgproxy, Options: opts}
}

func args(in []imgproxy.Arg) []playground.Arg {
	if len(in) == 0 {
		return nil
	}
	out := make([]playground.Arg, 0, len(in))
	for i := range in {
		a := &in[i]
		pa := playground.Arg{
			Name: a.Name, Type: argType(a.Type), Enum: a.Enum,
			Optional: a.Optional || a.Type == imgproxy.TypeBoolean, Pattern: a.Pattern, Doc: a.Doc,
		}
		if a.HasMin {
			pa.Min = new(a.Min)
		}
		if a.HasMax {
			pa.Max = new(a.Max)
		}
		out = append(out, pa)
	}
	return out
}

func argType(t imgproxy.ArgType) string {
	switch t {
	case imgproxy.TypeInteger:
		return "integer"
	case imgproxy.TypeNumber:
		return "number"
	case imgproxy.TypeBoolean:
		return "boolean"
	case imgproxy.TypeEnum:
		return "enum"
	case imgproxy.TypeColor:
		return "color"
	case imgproxy.TypeBase64:
		return "base64"
	case imgproxy.TypeDirection:
		return "direction"
	default:
		return "string"
	}
}

// queryCatalogue turns queryKeys into options with one string argument;
// keys documented as "Alias of x." become aliases of x.
func queryCatalogue() playground.Catalogue {
	var opts []playground.Option
	index := map[string]int{}
	for _, kv := range queryKeys() {
		name, doc := kv[0], kv[1]
		switch name {
		case "url", "sign", "xi_sig":
			continue
		}
		if target, ok := strings.CutPrefix(doc, "Alias of "); ok {
			if i, ok := index[strings.TrimSuffix(target, ".")]; ok {
				opts[i].Aliases = append(opts[i].Aliases, name)
				continue
			}
		}
		index[name] = len(opts)
		opts = append(opts, playground.Option{
			Name: name, Doc: doc,
			Forms: []playground.Form{{Args: []playground.Arg{{Name: "value", Type: "string", Doc: doc}}}},
		})
	}
	return playground.Catalogue{Grammar: playground.GrammarQuery, Options: opts}
}
