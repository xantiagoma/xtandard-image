package imagemeta

import (
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestEXIFField(t *testing.T) {
	t.Parallel()
	tests := []struct {
		field  string
		ifd    int
		name   string
		wantOK bool
	}{
		{"exif-ifd0-Model", 0, "Model", true},
		{"exif-ifd2-DateTimeOriginal", 2, "DateTimeOriginal", true},
		{"exif-ifd3-GPSVersionID", 3, "GPSVersionID", true},
		{"exif-data", 0, "", false},
		{"exif-ifd-Model", 0, "", false},
		{"exif-ifd0-", 0, "", false},
		{"exif-ifdx-Model", 0, "", false},
		{"exif-ifd123-Model", 0, "", false},
		{"xmp-data", 0, "", false},
	}
	for _, tc := range tests {
		ifd, name, ok := EXIFField(tc.field)
		if ifd != tc.ifd || name != tc.name || ok != tc.wantOK {
			t.Errorf("EXIFField(%q) = %d, %q, %v; want %d, %q, %v", tc.field, ifd, name, ok, tc.ifd, tc.name, tc.wantOK)
		}
	}
}

func TestEXIFValue(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		// Strings as libvips' vips_exif_to_s renders them (libvips exif.c).
		{"NIKON D810 (NIKON D810, ASCII, 11 components, 11 bytes)", "NIKON D810"},
		{"2016:09:11 22:15:03 (2016:09:11 22:15:03, ASCII, 20 components, 20 bytes)", "2016:09:11 22:15:03"},
		{
			"Adobe Photoshop Lightroom 6.1 (Windows) (Adobe Photoshop Lightroom 6.1 (Windows), ASCII, 40 components, 40 bytes)",
			"Adobe Photoshop Lightroom 6.1 (Windows)",
		},
		{"6 (Right-top, Short, 1 components, 2 bytes)", "Right-top"},
		{"8/1 (8.00 EV (f/16.0), Rational, 1 components, 8 bytes)", "8.00 EV (f/16.0)"},
		{"0 (Normal, Short, 1 components, 2 bytes)", "Normal"},
		{"72/1 (72, Rational, 1 components, 8 bytes)", "72"},
		{"-1/3 (-0.33 EV, SRational, 1 components, 8 bytes)", "-0.33 EV"},
		{"160/10 350/10 40/10 40/10 (16.0-35.0 mm f/4.0, Rational, 4 components, 32 bytes)", "16.0-35.0 mm f/4.0"},
		{"Exif Version 2.31 (Exif Version 2.31, Undefined, 4 components, 4 bytes)", "Exif Version 2.31"},
		// Numeric with 10+ components: the raw part is the text.
		{"1, 2, 3 (1, 2, 3, Short, 10 components, 20 bytes)", "1, 2, 3"},
		{" (, ASCII, 0 components, 0 bytes)", ""},
		// Not libvips-shaped: returned trimmed.
		{"plain value ", "plain value"},
		{"x (y, ASCII, n components, 3 bytes)", "x (y, ASCII, n components, 3 bytes)"},
		{"12 (Short, 1 components, 2 bytes)", "12 (Short, 1 components, 2 bytes)"},
		{"a (b, ASCII, 1 components, 1 bytes)", "a (b, ASCII, 1 components, 1 bytes)"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := EXIFValue(tc.in); got != tc.want {
			t.Errorf("EXIFValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// iim encodes one IPTC-IIM dataset.
func iim(record, dataset byte, value string) []byte {
	b := make([]byte, 0, 5+len(value))
	b = append(b, iptcMarker, record, dataset)
	b = binary.BigEndian.AppendUint16(b, uint16(len(value))) //nolint:gosec // G115: test values are short
	return append(b, value...)
}

// ps3 wraps resources in a Photoshop 3.0 resource section.
func ps3(blocks ...[]byte) []byte {
	out := append([]byte{}, ps3Header...)
	for _, b := range blocks {
		out = append(out, b...)
	}
	return out
}

func block(id uint16, data []byte) []byte {
	b := append([]byte{}, ps3BlockHeader...)
	b = binary.BigEndian.AppendUint16(b, id)
	b = append(b, 0, 0)                                     // empty Pascal name, padded
	b = binary.BigEndian.AppendUint32(b, uint32(len(data))) //nolint:gosec // G115: test values are short
	b = append(b, data...)
	if len(data)%2 == 1 {
		b = append(b, 0)
	}
	return b
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestParseIPTCDocsExample rebuilds the /info documentation example: IPTC
// datasets inside a JPEG APP13 Photoshop section plus a 240 dpi
// ResolutionInfo resource.
func TestParseIPTCDocsExample(t *testing.T) {
	t.Parallel()
	datasets := concat(
		iim(2, 0, "\x00\x04"), // record version (numeric)
		iim(2, 5, "Spider-Man"),
		iim(2, 120, "Spider-Man swings on the web"),
		iim(2, 116, "Daily Bugle"),
		iim(2, 25, "spider-man"),
		iim(2, 25, "menance"),
		iim(2, 25, "offender"),
		iim(2, 99, "unknown dataset"), // skipped
		iim(2, 10, "too long"),        // Urgency is 1 byte: skipped
	)
	res := make([]byte, 16)
	binary.BigEndian.PutUint32(res[0:], 240<<16)
	binary.BigEndian.PutUint16(res[4:], 1)
	binary.BigEndian.PutUint16(res[6:], 1)
	binary.BigEndian.PutUint32(res[8:], 240<<16)
	binary.BigEndian.PutUint16(res[12:], 1)
	binary.BigEndian.PutUint16(res[14:], 1)
	got := ParseIPTC(ps3(block(0x03ED, res), block(0x0404, append(datasets, 0x00))))

	want := `{"Tags":{"Caption":"Spider-Man swings on the web","Caption/Abstract":"Spider-Man swings on the web",` +
		`"Copyright Notice":"Daily Bugle","Keywords":["spider-man","menance","offender"],"Name":"Spider-Man",` +
		`"Object Name":"Spider-Man","Record Version":4},` +
		`"Photoshop":{"resolution":{"HeightUnit":"inches","WidthUnit":"inches","XResolution":240,` +
		`"XResolutionUnit":"inches","YResolution":240,"YResolutionUnit":"inches"}}}`
	if diff := cmp.Diff(want, jsonOf(t, got)); diff != "" {
		t.Errorf("ParseIPTC (-want +got):\n%s", diff)
	}
}

func TestParseIPTCShapes(t *testing.T) {
	t.Parallel()
	ext := []byte{iptcMarker, 2, 116, 0x80, 0x04, 0, 0, 0, 3, 'a', 'b', 'c'}
	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{"nil", nil, `{"Tags":{},"Photoshop":null}`},
		{"binary dataset", iim(1, 90, "\x1b%G"), `{"Tags":{"Coded Character Set":"GyVH"},"Photoshop":null}`},
		{"raw IIM (TIFF)", iim(2, 116, "(c) me"), `{"Tags":{"Copyright Notice":"(c) me"},"Photoshop":null}`},
		{"extended size", ext, `{"Tags":{"Copyright Notice":"abc"},"Photoshop":null}`},
		{"bad extended size field", []byte{iptcMarker, 2, 116, 0x80, 0x03, 0, 0, 0}, `{"Tags":{},"Photoshop":null}`},
		{"truncated dataset", iim(2, 116, "abc")[:6], `{"Tags":{},"Photoshop":null}`},
		{"garbage before a dataset", append([]byte{1, 2, 3}, iim(2, 80, "Peter")...), `{"Tags":{"By-line":["Peter"]},"Photoshop":null}`},
		{"non-repeatable last wins", concat(iim(2, 105, "a"), iim(2, 105, "b")), `{"Tags":{"Headline":"b"},"Photoshop":null}`},
		{"photoshop without iptc", ps3(block(0x0409, []byte{1, 2, 3})), `{"Tags":{},"Photoshop":null}`},
		{"photoshop truncated block", ps3(block(0x0404, iim(2, 116, "x")))[:20], `{"Tags":{},"Photoshop":null}`},
		{"photoshop odd block padding", ps3(block(0x0409, []byte{1}), block(0x0404, iim(2, 116, "x"))), `{"Tags":{"Copyright Notice":"x"},"Photoshop":null}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tc.want, jsonOf(t, ParseIPTC(tc.in))); diff != "" {
				t.Errorf("ParseIPTC (-want +got):\n%s", diff)
			}
		})
	}
}

// docsXMP is an XMP packet that carries the imgproxy /info documentation
// example.
const docsXMP = `<?xpacket begin="` + "\ufeff" + `" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
 <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
  <rdf:Description rdf:about=""
    xmlns:aux="http://ns.adobe.com/exif/1.0/aux/"
    xmlns:dc="http://purl.org/dc/elements/1.1/"
    xmlns:photoshop="http://ns.adobe.com/photoshop/1.0/"
    aux:ApproximateFocusDistance="4294967295/1"
    aux:ImageNumber="16604"
    aux:Lens="16.0-35.0 mm f/4.0"
    aux:LensID="163"
    aux:LensInfo="160/10 350/10 40/10 40/10"
    aux:SerialNumber="12345678"
    dc:format="image/jpeg"
    photoshop:DateCreated="2016-09-11T18:44:50.003">
   <dc:creator><rdf:Seq><rdf:li>Peter B. Parker</rdf:li></rdf:Seq></dc:creator>
   <dc:publisher><rdf:Bag><rdf:li>Daily Bugle</rdf:li></rdf:Bag></dc:publisher>
   <dc:subject><rdf:Bag><rdf:li>spider-man</rdf:li><rdf:li>menance</rdf:li><rdf:li>offender</rdf:li></rdf:Bag></dc:subject>
  </rdf:Description>
 </rdf:RDF>
</x:xmpmeta>
<?xpacket end="w"?>`

func TestParseXMPDocsExample(t *testing.T) {
	t.Parallel()
	want := `{"aux":{"ApproximateFocusDistance":"4294967295/1","ImageNumber":"16604","Lens":"16.0-35.0 mm f/4.0",` +
		`"LensID":"163","LensInfo":"160/10 350/10 40/10 40/10","SerialNumber":"12345678"},` +
		`"dc":{"creator":["Peter B. Parker"],"format":"image/jpeg","publisher":["Daily Bugle"],` +
		`"subject":["spider-man","menance","offender"]},"photoshop":{"DateCreated":"2016-09-11T18:44:50.003"}}`
	if diff := cmp.Diff(want, jsonOf(t, ParseXMP([]byte(docsXMP)))); diff != "" {
		t.Errorf("ParseXMP (-want +got):\n%s", diff)
	}
}

func TestParseXMPShapes(t *testing.T) {
	t.Parallel()
	const pre = `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">`
	const post = `</rdf:RDF></x:xmpmeta>`
	tests := []struct {
		name, in, want string
	}{
		{"alt picks the first item", `<rdf:Description xmlns:dc="d"><dc:title><rdf:Alt><rdf:li xml:lang="x-default">Hello</rdf:li>` +
			`<rdf:li xml:lang="de">Hallo</rdf:li></rdf:Alt></dc:title></rdf:Description>`, `{"dc":{"title":"Hello"}}`},
		{"simple element", `<rdf:Description xmlns:xmp="x"><xmp:Rating>5</xmp:Rating></rdf:Description>`, `{"xmp":{"Rating":"5"}}`},
		{
			"resource", `<rdf:Description xmlns:xmpRights="r"><xmpRights:WebStatement rdf:resource="https://e.com"/></rdf:Description>`,
			`{"xmpRights":{"WebStatement":"https://e.com"}}`,
		},
		{
			"parseType resource struct", `<rdf:Description xmlns:Iptc4xmpCore="c"><Iptc4xmpCore:CreatorContactInfo rdf:parseType="Resource">` +
				`<Iptc4xmpCore:CiEmailWork>a@b.c</Iptc4xmpCore:CiEmailWork></Iptc4xmpCore:CreatorContactInfo></rdf:Description>`,
			`{"Iptc4xmpCore":{"CreatorContactInfo":{"CiEmailWork":"a@b.c"}}}`,
		},
		{
			"nested description struct", `<rdf:Description xmlns:s="s"><s:dim><rdf:Description s:w="1" s:h="2"/></s:dim></rdf:Description>`,
			`{"s":{"dim":{"h":"2","w":"1"}}}`,
		},
		{"attribute struct", `<rdf:Description xmlns:s="s"><s:dim s:w="1"/></rdf:Description>`, `{"s":{"dim":{"w":"1"}}}`},
		{"descriptions merge", `<rdf:Description xmlns:a="a" a:x="1"/><rdf:Description xmlns:a="a" a:y="2"/>`, `{"a":{"x":"1","y":"2"}}`},
		{"empty", ``, `{}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tc.want, jsonOf(t, ParseXMP([]byte(pre+tc.in+post)))); diff != "" {
				t.Errorf("ParseXMP (-want +got):\n%s", diff)
			}
		})
	}
	if got := ParseXMP([]byte("<rdf:Description xmlns:a=\"a\" a:x=")); len(got) != 0 {
		t.Errorf("malformed packet = %v, want none", got)
	}
	deep := strings.Repeat("<a>", maxXMPDepth+2)
	if got := ParseXMP([]byte(deep)); got != nil {
		t.Errorf("too deep packet = %v, want nil", got)
	}
}

func FuzzEXIFValue(f *testing.F) {
	for _, s := range []string{"6 (Right-top, Short, 1 components, 2 bytes)", "a (a, ASCII, 2 components, 2 bytes)", " (, ASCII, 0 components, 0 bytes)", "1 2 (x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if v := EXIFValue(s); len(v) > len(s) {
			t.Fatalf("EXIFValue grew %q to %q", s, v)
		}
		_, _, _ = EXIFField(s)
	})
}

func FuzzParseIPTC(f *testing.F) {
	f.Add(ps3(block(0x0404, iim(2, 25, "k"))))
	f.Add(iim(2, 116, "c"))
	f.Add([]byte{iptcMarker, 2, 116, 0x80, 0x08, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, b []byte) {
		if _, err := json.Marshal(ParseIPTC(b)); err != nil {
			t.Fatal(err)
		}
	})
}

func FuzzParseXMP(f *testing.F) {
	f.Add([]byte(docsXMP))
	f.Add([]byte(`<rdf:Description xmlns:a="a" a:x="1"><a:y><rdf:Seq><rdf:li>z</rdf:li></rdf:Seq></a:y></rdf:Description>`))
	f.Fuzz(func(t *testing.T, b []byte) {
		if _, err := json.Marshal(ParseXMP(b)); err != nil {
			t.Fatal(err)
		}
	})
}
