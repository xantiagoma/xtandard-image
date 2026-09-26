package translate

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// maxPathDepth is how many segments a reported field keeps below the
// pipeline ("resize.width", "watermarks[0].opacity").
const maxPathDepth = 2

// compare reports the fields of want that got does not reproduce:
// unsupported when got lost a set value, approximated when got carries a
// different (or an extra) value. Both requests are normalized.
func compare(want, got *transform.Request) (unsupported, approximated []string) {
	d := &differ{pipelines: len(want.Pipelines)}
	d.value("", reflect.ValueOf(want).Elem(), reflect.ValueOf(got).Elem())
	return d.lost, d.changed
}

type differ struct {
	pipelines     int
	lost, changed []string
	seenL, seenC  map[string]bool
}

func (d *differ) report(path string, lost bool) { d.addRaw(d.shorten(path), lost) }

// addRaw records path as is.
func (d *differ) addRaw(path string, lost bool) {
	if lost {
		if d.seenL == nil {
			d.seenL = map[string]bool{}
		}
		if !d.seenL[path] {
			d.seenL[path] = true
			d.lost = append(d.lost, path)
		}
		return
	}
	if d.seenC == nil {
		d.seenC = map[string]bool{}
	}
	if !d.seenC[path] {
		d.seenC[path] = true
		d.changed = append(d.changed, path)
	}
}

// shorten drops "pipelines[0]." for single-pipeline requests and keeps
// maxPathDepth segments of pipeline and request fields.
func (d *differ) shorten(p string) string {
	prefix := ""
	if rest, ok := strings.CutPrefix(p, "pipelines["); ok {
		i := strings.Index(rest, "].")
		if i < 0 {
			return p
		}
		if d.pipelines > 1 {
			prefix = p[:len("pipelines[")+i+2]
		}
		p = rest[i+2:]
	}
	segs := strings.Split(p, ".")
	if len(segs) > maxPathDepth {
		segs = segs[:maxPathDepth]
	}
	return prefix + strings.Join(segs, ".")
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// snake converts a Go field name to snake_case ("MinWidth" → "min_width",
// "DPR" → "dpr").
func snake(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i, r := range rs {
		upper := r >= 'A' && r <= 'Z'
		if upper && i > 0 {
			prevLower := rs[i-1] >= 'a' && rs[i-1] <= 'z'
			nextLower := i+1 < len(rs) && rs[i+1] >= 'a' && rs[i+1] <= 'z'
			if prevLower || (nextLower && rs[i-1] >= 'A' && rs[i-1] <= 'Z') {
				b.WriteByte('_')
			}
		}
		if upper {
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

var timeType = reflect.TypeFor[time.Time]()

// leafTypes compare as a whole: a colour or a padding is one field for a
// person, not four.
var leafTypes = map[reflect.Type]bool{
	reflect.TypeFor[transform.Color]():     true,
	reflect.TypeFor[transform.Dim]():       true,
	reflect.TypeFor[transform.Padding]():   true,
	reflect.TypeFor[transform.Flip]():      true,
	reflect.TypeFor[transform.Radius]():    true,
	reflect.TypeFor[transform.FocusRect](): true,
}

var pipelineType = reflect.TypeFor[transform.Pipeline]()

func (d *differ) value(path string, w, g reflect.Value) {
	if leafTypes[w.Type()] {
		if !reflect.DeepEqual(w.Interface(), g.Interface()) {
			d.report(path, g.IsZero())
		}
		return
	}
	switch w.Kind() {
	case reflect.Pointer:
		switch {
		case w.IsNil() && g.IsNil():
		case w.IsNil():
			d.value(path, reflect.New(w.Type().Elem()).Elem(), g.Elem())
		case g.IsNil():
			d.value(path, w.Elem(), reflect.New(w.Type().Elem()).Elem())
		default:
			d.value(path, w.Elem(), g.Elem())
		}
	case reflect.Struct:
		if w.Type() == timeType {
			wt, _ := reflect.TypeAssert[time.Time](w)
			gt, _ := reflect.TypeAssert[time.Time](g)
			if !wt.Equal(gt) {
				d.report(path, gt.IsZero())
			}
			return
		}
		t := w.Type()
		for i := range t.NumField() {
			f := t.Field(i)
			if !f.IsExported() || f.Tag.Get("key") == "-" {
				continue
			}
			d.value(join(path, snake(f.Name)), w.Field(i), g.Field(i))
		}
	case reflect.Slice:
		n := max(w.Len(), g.Len())
		// Lists of names or numbers compare as a whole.
		if k := w.Type().Elem().Kind(); k != reflect.Struct && k != reflect.Pointer {
			if !reflect.DeepEqual(w.Interface(), g.Interface()) && !(w.Len() == 0 && g.Len() == 0) {
				d.report(path, g.Len() == 0)
			}
			return
		}
		if w.Type().Elem() == pipelineType {
			for i := range n {
				p := fmt.Sprintf("%s[%d]", path, i)
				switch {
				case i >= g.Len():
					d.addRaw(p, true) // a chained pipeline the dialect cannot write
				case i >= w.Len():
					d.addRaw(p, false)
				default:
					d.value(p, w.Index(i), g.Index(i))
				}
			}
			return
		}
		zero := reflect.New(w.Type().Elem()).Elem()
		for i := range n {
			wi, gi := zero, zero
			if i < w.Len() {
				wi = w.Index(i)
			}
			if i < g.Len() {
				gi = g.Index(i)
			}
			d.value(fmt.Sprintf("%s[%d]", path, i), wi, gi)
		}
	case reflect.Map:
		keys := map[string]bool{}
		for _, k := range w.MapKeys() {
			keys[fmt.Sprint(k.Interface())] = true
		}
		for _, k := range g.MapKeys() {
			keys[fmt.Sprint(k.Interface())] = true
		}
		if !reflect.DeepEqual(w.Interface(), g.Interface()) && len(keys) > 0 {
			d.report(path, g.Len() == 0)
		}
	case reflect.Float32, reflect.Float64:
		a, b := w.Float(), g.Float()
		if math.Abs(a-b) > 1e-6*max(1, math.Abs(a), math.Abs(b)) {
			d.report(path, b == 0)
		}
	default:
		if !reflect.DeepEqual(w.Interface(), g.Interface()) {
			d.report(path, g.IsZero())
		}
	}
}
