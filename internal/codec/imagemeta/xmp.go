package imagemeta

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// XMP parse bounds: packets are small, and anything past these is hostile.
const (
	maxXMPDepth = 64
	maxXMPNodes = 100_000
)

var errXMPTooComplex = errors.New("imagemeta: xmp too deep or too large")

// xmpNode is one element of the packet, with namespace prefixes kept as
// written (xml.Decoder.RawToken does not resolve them).
type xmpNode struct {
	name     xml.Name // Space is the prefix
	attrs    []xml.Attr
	children []*xmpNode
	text     strings.Builder
}

// ParseXMP turns an XMP packet into imgproxy's /info shape: one object per
// namespace prefix ("dc", "aux", "photoshop", ...) mapping property names to
// values. Simple properties are strings, rdf:Seq and rdf:Bag arrays are
// lists, rdf:Alt yields its first (x-default) item, and structures are
// nested objects. Properties written as attributes of rdf:Description are
// included. Unparsable packets yield nil.
func ParseXMP(data []byte) map[string]map[string]any {
	root, err := parseXMPTree(data)
	if err != nil {
		return nil
	}
	out := map[string]map[string]any{}
	var walk func(n *xmpNode)
	walk = func(n *xmpNode) {
		if n.name.Space == "rdf" && n.name.Local == "Description" {
			addDescription(out, n)
			return
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(root)
	return out
}

func parseXMPTree(data []byte) (*xmpNode, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	d.Entity = xml.HTMLEntity
	root := &xmpNode{}
	stack := []*xmpNode{root}
	nodes := 0
	for {
		tok, err := d.RawToken()
		if errors.Is(err, io.EOF) {
			return root, nil
		}
		if err != nil {
			return nil, err
		}
		top := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			nodes++
			if nodes > maxXMPNodes || len(stack) > maxXMPDepth {
				return nil, errXMPTooComplex
			}
			n := &xmpNode{name: t.Name, attrs: t.Attr}
			top.children = append(top.children, n)
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			top.text.Write(t)
		default: // comments, processing instructions (xpacket), directives
		}
	}
}

// skipAttr reports attributes that are RDF syntax, not properties.
func skipAttr(a xml.Attr) bool {
	switch a.Name.Space {
	case "", "rdf", "xmlns", "xml":
		return true
	}
	return false
}

func addDescription(out map[string]map[string]any, n *xmpNode) {
	set := func(prefix, name string, v any) {
		m := out[prefix]
		if m == nil {
			m = map[string]any{}
			out[prefix] = m
		}
		m[name] = v
	}
	for _, a := range n.attrs {
		if !skipAttr(a) {
			set(a.Name.Space, a.Name.Local, a.Value)
		}
	}
	for _, c := range n.children {
		if c.name.Space == "" || c.name.Space == "rdf" {
			continue
		}
		set(c.name.Space, c.name.Local, xmpValue(c))
	}
}

// xmpValue is the value of a property element.
func xmpValue(n *xmpNode) any {
	for _, a := range n.attrs {
		if a.Name.Space == "rdf" && a.Name.Local == "resource" {
			return a.Value
		}
		if a.Name.Space == "rdf" && a.Name.Local == "parseType" && a.Value == "Resource" {
			return xmpStruct(n)
		}
	}
	for _, c := range n.children {
		if c.name.Space != "rdf" {
			continue
		}
		switch c.name.Local {
		case "Seq", "Bag":
			items := []any{}
			for _, li := range c.children {
				if li.name.Space == "rdf" && li.name.Local == "li" {
					items = append(items, xmpValue(li))
				}
			}
			return items
		case "Alt":
			for _, li := range c.children {
				if li.name.Space == "rdf" && li.name.Local == "li" {
					return xmpValue(li)
				}
			}
			return ""
		case "Description":
			return xmpStruct(c)
		}
	}
	// A struct written with property children but no rdf wrapper.
	for _, c := range n.children {
		if c.name.Space != "" && c.name.Space != "rdf" {
			return xmpStruct(n)
		}
	}
	// Qualified or struct values written as attributes of the element.
	if s := xmpStruct(n); len(s) > 0 && strings.TrimSpace(n.text.String()) == "" {
		return s
	}
	return strings.TrimSpace(n.text.String())
}

// xmpStruct collects the fields of a structure: property attributes and
// property children, keyed by local name.
func xmpStruct(n *xmpNode) map[string]any {
	m := map[string]any{}
	for _, a := range n.attrs {
		if !skipAttr(a) {
			m[a.Name.Local] = a.Value
		}
	}
	for _, c := range n.children {
		if c.name.Space == "" || c.name.Space == "rdf" {
			continue
		}
		m[c.name.Local] = xmpValue(c)
	}
	return m
}
