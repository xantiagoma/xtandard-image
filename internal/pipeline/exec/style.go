package exec

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/svg"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// skippedStyle is reported in Result.Skipped when `style` meets a source
// that is not an SVG document.
const skippedStyle = "style"

// styledSource returns the source of j with the request's `style` CSS
// (imgproxy Pro: "prepend a <style> node with the provided CSS styles to the
// <svg> node of a source SVG image") injected as the first child of the root
// element, or nil when there is nothing to inject. The document is then
// sanitised again as a whole, so the injected CSS goes through exactly the
// policy an SVG's own <style> does (@import, url() and friends neutralised;
// escaped CSS that would spell them dropped). The caller releases the
// returned buffer.
//
// The source buffer already holds the sanitised SVG (the server sanitises
// before the probe); a source that is not SVG leaves the option unapplied
// (skipped), like imgproxy.
func (e *Executor) styledSource(j *Job, css string) (*vips.Buffer, error) {
	if css == "" || j.Meta.Format != "svg" {
		return nil, nil
	}
	doc, err := injectStyle(j.Source.Bytes(), css)
	if err != nil {
		return nil, err
	}
	var clean bytes.Buffer
	clean.Grow(len(doc))
	if err := svg.Sanitize(&clean, bytes.NewReader(doc), e.cfg.SVG); err != nil {
		if errors.Is(err, svg.ErrTooLarge) || errors.Is(err, svg.ErrTooDeep) || errors.Is(err, svg.ErrTooManyElements) {
			return nil, errs.Wrapf(errs.KindLimitExceeded, err, "exec: styled svg")
		}
		return nil, errs.Wrapf(errs.KindCorrupt, err, "exec: styled svg")
	}
	buf, err := vips.NewBuffer(clean.Len())
	if err != nil {
		return nil, err
	}
	copy(buf.Bytes(), clean.Bytes())
	return buf, nil
}

// svgNS is the namespace of the injected element, declared on it so that it
// is an SVG <style> whatever prefix the document binds (<svg:svg>).
const svgNS = "http://www.w3.org/2000/svg"

// injectStyle inserts <style>css</style> right after the start tag of the
// root element of doc. The CSS is XML-escaped character data, so it can
// never close the element or open markup. A self-closing root (<svg/>) is
// expanded.
func injectStyle(doc []byte, css string) ([]byte, error) {
	d := xml.NewDecoder(bytes.NewReader(doc))
	d.Strict = true
	for {
		start := int(d.InputOffset())
		tok, err := d.RawToken()
		if errors.Is(err, io.EOF) {
			return nil, errs.New(errs.KindCorrupt, "exec: svg without a root element")
		}
		if err != nil {
			return nil, errs.Wrapf(errs.KindCorrupt, err, "exec: parse svg for style")
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		end := int(d.InputOffset())
		tag := doc[start:end]
		var style bytes.Buffer
		style.WriteString(`<style xmlns="` + svgNS + `">`)
		if err := xml.EscapeText(&style, []byte(css)); err != nil {
			return nil, errs.Wrapf(errs.KindInvalidURL, err, "exec: style css")
		}
		style.WriteString(`</style>`)

		out := make([]byte, 0, len(doc)+style.Len()+16)
		if closed, ok := bytes.CutSuffix(bytes.TrimRight(tag, " \t\r\n"), []byte("/>")); ok {
			qname := se.Name.Local
			if se.Name.Space != "" { // RawToken keeps the prefix as written
				qname = se.Name.Space + ":" + se.Name.Local
			}
			out = append(out, doc[:start]...)
			out = append(out, closed...)
			out = append(out, '>')
			out = append(out, style.Bytes()...)
			out = append(out, "</"+qname+">"...)
			return append(out, doc[end:]...), nil
		}
		out = append(out, doc[:end]...)
		out = append(out, style.Bytes()...)
		return append(out, doc[end:]...), nil
	}
}

// firstStyle is the `style` of the first pipeline: the one that loads the
// source.
func firstStyle(req *transform.Request) string {
	if len(req.Pipelines) == 0 {
		return ""
	}
	return req.Pipelines[0].SVGStyle
}
