package gainmap

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Adobe's gain map XMP namespace (also written by Google UltraHDR).
const (
	hdrgmNS = "http://ns.adobe.com/hdr-gain-map/1.0/"
	rdfNS   = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
)

// XMP bounds: a gain map packet has a dozen properties.
const (
	maxXMPTokens = 20_000
	maxXMPBytes  = 1 << 20
)

// ParseXMP reads Adobe hdrgm gain-map metadata from an XMP packet: the
// properties as attributes of rdf:Description or as elements, single
// values or rdf:Seq lists of three. ok is false when the packet has no
// hdrgm:Version (no gain-map metadata); the error reports metadata that
// is present but unusable. Defaults follow the Adobe specification:
// GainMapMin 0, Gamma 1, offsets 1/64, HDRCapacityMin 0; GainMapMax and
// HDRCapacityMax are required.
func ParseXMP(packet []byte) (m Metadata, ok bool, err error) {
	if len(packet) > maxXMPBytes || !bytes.Contains(packet, []byte(hdrgmNS)) {
		return Metadata{}, false, nil
	}
	props, err := hdrgmProps(packet)
	if err != nil {
		return Metadata{}, false, err
	}
	if _, ok := props["Version"]; !ok {
		return Metadata{}, false, nil
	}
	m = Metadata{Channels: 1}
	get := func(name string, def float64, required bool) ([3]float64, error) {
		vs, ok := props[name]
		if !ok || len(vs) == 0 {
			if required {
				return [3]float64{}, fmt.Errorf("%w: hdrgm:%s missing", ErrMalformed, name)
			}
			return [3]float64{def, def, def}, nil
		}
		if len(vs) != 1 && len(vs) != 3 {
			return [3]float64{}, fmt.Errorf("%w: hdrgm:%s has %d values", ErrMalformed, name, len(vs))
		}
		var out [3]float64
		for i := range 3 {
			v, err := strconv.ParseFloat(strings.TrimSpace(vs[min(i, len(vs)-1)]), 64)
			if err != nil {
				return [3]float64{}, fmt.Errorf("%w: hdrgm:%s: %w", ErrMalformed, name, err)
			}
			out[i] = v
		}
		if len(vs) == 3 {
			m.Channels = 3
		}
		return out, nil
	}
	var capMin, capMax [3]float64
	if m.Min, err = get("GainMapMin", 0, false); err != nil {
		return Metadata{}, true, err
	}
	if m.Max, err = get("GainMapMax", 0, true); err != nil {
		return Metadata{}, true, err
	}
	if m.Gamma, err = get("Gamma", 1, false); err != nil {
		return Metadata{}, true, err
	}
	offSDR, err := get("OffsetSDR", 1.0/64, false)
	if err != nil {
		return Metadata{}, true, err
	}
	offHDR, err := get("OffsetHDR", 1.0/64, false)
	if err != nil {
		return Metadata{}, true, err
	}
	if capMin, err = get("HDRCapacityMin", 0, false); err != nil {
		return Metadata{}, true, err
	}
	if capMax, err = get("HDRCapacityMax", 0, true); err != nil {
		return Metadata{}, true, err
	}
	m.BaseOffset, m.AlternateOffset = offSDR, offHDR
	m.BaseHeadroom, m.AlternateHeadroom = max(capMin[0], 0), capMax[0]
	if vs := props["BaseRenditionIsHDR"]; len(vs) > 0 && strings.EqualFold(strings.TrimSpace(vs[0]), "true") {
		// The base is the HDR rendition: the map leads to SDR.
		m.BaseOffset, m.AlternateOffset = offHDR, offSDR
		m.BaseHeadroom, m.AlternateHeadroom = capMax[0], max(capMin[0], 0)
	}
	if err := m.Validate(); err != nil {
		return Metadata{}, true, err
	}
	return m, true, nil
}

// hdrgmProps collects the hdrgm properties of packet: name → values.
func hdrgmProps(packet []byte) (map[string][]string, error) {
	d := xml.NewDecoder(bytes.NewReader(packet))
	d.Strict = false
	props := map[string][]string{}
	var cur string // hdrgm element being read
	var text strings.Builder
	var items []string
	inItem := false
	for range maxXMPTokens {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			return props, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%w: xmp: %w", ErrMalformed, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			for _, a := range t.Attr {
				if a.Name.Space == hdrgmNS {
					props[a.Name.Local] = []string{a.Value}
				}
			}
			switch {
			case t.Name.Space == hdrgmNS && cur == "":
				cur = t.Name.Local
				text.Reset()
				items = items[:0]
			case cur != "" && t.Name.Space == rdfNS && t.Name.Local == "li":
				inItem = true
				text.Reset()
			}
		case xml.CharData:
			if cur != "" && text.Len() < 256 {
				text.Write(t)
			}
		case xml.EndElement:
			switch {
			case inItem && t.Name.Space == rdfNS && t.Name.Local == "li":
				items = append(items, text.String())
				text.Reset()
				inItem = false
			case cur != "" && t.Name.Space == hdrgmNS && t.Name.Local == cur:
				if len(items) > 0 {
					props[cur] = append([]string(nil), items...)
				} else {
					props[cur] = []string{text.String()}
				}
				cur = ""
			}
		}
	}
	return nil, fmt.Errorf("%w: xmp too large", ErrMalformed)
}
