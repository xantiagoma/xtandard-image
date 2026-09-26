//go:build cgo

package app

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
)

// allInfo turns on every info option this build answers.
const allInfo = "/s:1/f:1/d:1/exif:1/iptc:1/xmp:1/cs:1/b:1/sf:1/pn:1/a:1"

// Signing key and salt of imgproxy's "Signing the URL" documentation.
const (
	infoKey  = "736563726574"
	infoSalt = "68656C6C6F"
)

// startInfo runs the service with the fixtures bound to the imgproxy mount,
// a second fs source "synth" at synthDir, signing keys (not required),
// unsigned requests allowed and debug headers on.
func startInfo(t *testing.T, synthDir string) string {
	t.Helper()
	fixtures := filepath.Join(golden.ModuleRoot(t), "testdata", "fixtures")
	yml := `server:
  addr: "127.0.0.1:0"
  shutdown_delay: 0s
admin:
  addr: "127.0.0.2:0"
signing:
  keys: [` + infoKey + `]
  salts: [` + infoSalt + `]
  required: "false"
security:
  unsigned_policy: allow
observability:
  debug_headers: true
sources:
  fixtures:
    type: fs
    root: ` + strconv.Quote(fixtures) + `
  synth:
    type: fs
    root: ` + strconv.Quote(synthDir) + `
dialects:
  imgproxy:
    source: fixtures
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(config.LoadOptions{ConfigFile: path})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return "http://" + start(t, &cfg, hooks{}).public.String()
}

// TestInfo runs the /info checks against one service, serially and not in
// parallel with the other tests: every service start calls vips.Init, and
// concurrent starts crashed inside xi_init. The app tests of the base branch
// also crash intermittently in libvips during processing, even with
// -parallel 1; running this test alone keeps it out of both until app
// initialises libvips once per process.
func TestInfo(t *testing.T) {
	root := golden.ModuleRoot(t)
	synth := t.TempDir()
	if err := os.WriteFile(filepath.Join(synth, "metadata.jpg"), metadataJPEG(t, root), 0o600); err != nil {
		t.Fatal(err)
	}
	base := startInfo(t, synth)
	t.Run("golden", func(t *testing.T) { infoGolden(t, base, root) })
	t.Run("exif names", func(t *testing.T) { infoEXIFNames(t, base) })
	t.Run("signature", func(t *testing.T) { infoSignature(t, base) })
}

// infoGolden answers /info for every fixture of every M1 format plus a
// JPEG carrying EXIF, IPTC (with Photoshop resolution) and XMP, and
// compares the JSON with testdata/info/<name>.json (GOLDEN_UPDATE=1
// rewrites them; review the diff).
func infoGolden(t *testing.T, base, root string) {
	t.Helper()
	cases := map[string]string{"synth-metadata": "xi://synth/metadata.jpg"}
	fixtures, err := filepath.Glob(filepath.Join(root, "testdata", "fixtures", "*", "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures {
		rel, err := filepath.Rel(filepath.Join(root, "testdata", "fixtures"), f)
		if err != nil {
			t.Fatal(err)
		}
		rel = filepath.ToSlash(rel)
		name := strings.ReplaceAll(strings.TrimSuffix(rel, filepath.Ext(rel)), "/", "-")
		cases[name] = rel
	}
	if len(cases) < 20 {
		t.Fatalf("found %d fixtures", len(cases))
	}

	update := os.Getenv("GOLDEN_UPDATE") != ""
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			r := get(t, base+"/info/unsafe"+allInfo+"/plain/"+src)
			if r.status != http.StatusOK || r.ctype != "application/json" {
				t.Fatalf("GET: %d %q %s", r.status, r.ctype, r.body)
			}
			got := indentJSON(t, r.body)
			path := filepath.Join(root, "testdata", "info", name+".json")
			if update {
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got, 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with GOLDEN_UPDATE=1): %v", err)
			}
			if diff := cmp.Diff(string(want), string(got)); diff != "" {
				t.Errorf("%s (-golden +got):\n%s", path, diff)
			}
		})
	}
}

// infoEXIFNames checks the EXIF keys and values of the orientation 6 +
// ICC fixture and of the synthetic metadata JPEG: libexif titles as keys
// (imgproxy's human-readable names) and oriented dimensions.
func infoEXIFNames(t *testing.T, base string) {
	t.Helper()
	type info struct {
		Width       int               `json:"width"`
		Height      int               `json:"height"`
		Orientation int               `json:"orientation"`
		EXIF        map[string]string `json:"exif"`
		IPTC        map[string]any    `json:"iptc"`
		XMP         map[string]any    `json:"xmp"`
	}
	fetchInfo := func(src string) info {
		r := get(t, base+"/info/unsafe/plain/"+src)
		if r.status != http.StatusOK {
			t.Fatalf("GET %s: %d %s", src, r.status, r.body)
		}
		var v info
		if err := json.Unmarshal(r.body, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	o := fetchInfo("jpeg/exif-orientation-6-icc.jpg")
	if o.Width != 320 || o.Height != 240 || o.Orientation != 6 {
		t.Errorf("orientation 6: %dx%d orientation %d, want 320x240 6", o.Width, o.Height, o.Orientation)
	}
	if o.EXIF["Orientation"] != "Right-top" {
		t.Errorf("EXIF Orientation = %q, want Right-top (exif %v)", o.EXIF["Orientation"], o.EXIF)
	}

	m := fetchInfo("xi://synth/metadata.jpg")
	for k, want := range map[string]string{
		"Model":                    "NIKON D810",
		"Date and Time (Original)": "2016:09:11 22:15:03",
		"Orientation":              "Top-left",
	} {
		if got := m.EXIF[k]; got != want {
			t.Errorf("exif[%q] = %q, want %q (exif %v)", k, got, want, m.EXIF)
		}
	}
	for _, canonical := range []string{"DateTimeOriginal", "exif-ifd2-DateTimeOriginal"} {
		if _, ok := m.EXIF[canonical]; ok {
			t.Errorf("exif has canonical key %q; imgproxy reports titles", canonical)
		}
	}
	if m.IPTC["Copyright Notice"] != "Daily Bugle" || m.IPTC["Name"] != "Spider-Man" {
		t.Errorf("iptc = %v", m.IPTC)
	}
	if _, ok := m.XMP["dc"]; !ok {
		t.Errorf("xmp = %v", m.XMP)
	}
}

// infoSignature: /info URLs are signed like processing URLs; a
// processing signature does not verify an info path with other options.
func infoSignature(t *testing.T, base string) {
	t.Helper()
	keys, err := sign.ParseHexKeyring([]string{infoKey}, []string{infoSalt})
	if err != nil {
		t.Fatal(err)
	}
	scheme, err := sign.NewImgproxyScheme(32)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := sign.NewSigner(scheme, keys)
	if err != nil {
		t.Fatal(err)
	}
	const path = "/s:1/d:1/plain/jpeg/baseline.jpg"
	signed, err := signer.SignImgproxyPath(path)
	if err != nil {
		t.Fatal(err)
	}
	sig, _, _ := strings.Cut(strings.TrimPrefix(signed, "/"), "/")
	for _, tc := range []struct {
		name, path string
		want       int
	}{
		{"signed", "/info" + signed, http.StatusOK},
		{"tampered options", "/info/" + sig + "/s:0/d:1/plain/jpeg/baseline.jpg", http.StatusForbidden},
		{"bad signature", "/info/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" + path, http.StatusForbidden},
		{"unsigned allowed", "/info/unsafe" + path, http.StatusOK},
		{"missing source", "/info/unsafe/s:1", http.StatusBadRequest},
		{"missing file", "/info/unsafe/plain/jpeg/missing.jpg", http.StatusNotFound},
		{"outside the source", "/info/unsafe/plain/../../etc/passwd", http.StatusForbidden},
	} {
		if got := get(t, base+tc.path); got.status != tc.want {
			t.Errorf("%s: GET %s = %d %q, want %d", tc.name, tc.path, got.status, got.body, tc.want)
		}
	}
}

func indentJSON(t *testing.T, b []byte) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("invalid JSON %s: %v", b, err)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}

// metadataJPEG is testdata/fixtures/jpeg/baseline.jpg with an EXIF APP1
// (Model, Orientation, DateTimeOriginal), an XMP APP1 and a Photoshop APP13
// (IPTC datasets and a 240 dpi ResolutionInfo) inserted after SOI: the
// imgproxy /info documentation example.
func metadataJPEG(t *testing.T, root string) []byte {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(root, "testdata", "fixtures", "jpeg", "baseline.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	segment := func(marker byte, payload []byte) []byte {
		b := make([]byte, 0, 4+len(payload))
		b = append(b, 0xFF, marker)
		b = binary.BigEndian.AppendUint16(b, uint16(len(payload)+2)) //nolint:gosec // G115: segments are far below 64 KiB
		return append(b, payload...)
	}
	var out bytes.Buffer
	out.Write(src[:2]) // SOI
	out.Write(segment(0xE1, exifPayload()))
	out.Write(segment(0xE1, append([]byte("http://ns.adobe.com/xap/1.0/\x00"), xmpPacket...)))
	out.Write(segment(0xED, photoshopPayload()))
	out.Write(src[2:])
	return out.Bytes()
}

// exifPayload is "Exif\0\0" + a big-endian TIFF with IFD0 {Model,
// Orientation, ExifIFDPointer} and an EXIF IFD {DateTimeOriginal}.
func exifPayload() []byte {
	const (
		ifd0Off    = 8
		ifd0Size   = 2 + 3*12 + 4
		exifOff    = ifd0Off + ifd0Size
		exifSize   = 2 + 1*12 + 4
		modelOff   = exifOff + exifSize
		model      = "NIKON D810\x00"
		dateOff    = modelOff + 12 // len(model) + 1: keep offsets even
		dateTime   = "2016:09:11 22:15:03\x00"
		typeASCII  = 2
		typeShort  = 3
		typeLong   = 4
		tagModel   = 0x0110
		tagOrient  = 0x0112
		tagExifPtr = 0x8769
		tagDTO     = 0x9003
	)
	be := binary.BigEndian
	b := []byte("MM\x00\x2a\x00\x00\x00\x08")
	entry := func(tag, typ uint16, count, value uint32) {
		b = be.AppendUint16(b, tag)
		b = be.AppendUint16(b, typ)
		b = be.AppendUint32(b, count)
		b = be.AppendUint32(b, value)
	}
	b = be.AppendUint16(b, 3)
	entry(tagModel, typeASCII, uint32(len(model)), modelOff)
	entry(tagOrient, typeShort, 1, 1<<16) // SHORT 1, left-justified
	entry(tagExifPtr, typeLong, 1, exifOff)
	b = be.AppendUint32(b, 0)
	b = be.AppendUint16(b, 1)
	entry(tagDTO, typeASCII, uint32(len(dateTime)), dateOff)
	b = be.AppendUint32(b, 0)
	b = append(b, model...)
	b = append(b, 0)
	b = append(b, dateTime...)
	return append([]byte("Exif\x00\x00"), b...)
}

const xmpPacket = `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
 <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
  <rdf:Description rdf:about="" xmlns:aux="http://ns.adobe.com/exif/1.0/aux/" xmlns:dc="http://purl.org/dc/elements/1.1/"
    xmlns:photoshop="http://ns.adobe.com/photoshop/1.0/" aux:Lens="16.0-35.0 mm f/4.0" aux:SerialNumber="12345678"
    dc:format="image/jpeg" photoshop:DateCreated="2016-09-11T18:44:50.003">
   <dc:creator><rdf:Seq><rdf:li>Peter B. Parker</rdf:li></rdf:Seq></dc:creator>
   <dc:subject><rdf:Bag><rdf:li>spider-man</rdf:li><rdf:li>menance</rdf:li><rdf:li>offender</rdf:li></rdf:Bag></dc:subject>
  </rdf:Description>
 </rdf:RDF>
</x:xmpmeta>
<?xpacket end="w"?>`

// photoshopPayload is an APP13 "Photoshop 3.0" section with IPTC datasets
// and a 240 dpi ResolutionInfo resource.
func photoshopPayload() []byte {
	be := binary.BigEndian
	dataset := func(num byte, v string) []byte {
		b := []byte{0x1c, 2, num}
		b = be.AppendUint16(b, uint16(len(v))) //nolint:gosec // G115: test values are short
		return append(b, v...)
	}
	var iim []byte
	for _, d := range [][]byte{
		dataset(5, "Spider-Man"),
		dataset(120, "Spider-Man swings on the web"),
		dataset(116, "Daily Bugle"),
		dataset(25, "spider-man"),
		dataset(25, "menance"),
		dataset(25, "offender"),
	} {
		iim = append(iim, d...)
	}
	res := make([]byte, 16)
	be.PutUint32(res[0:], 240<<16)
	be.PutUint16(res[4:], 1)
	be.PutUint16(res[6:], 1)
	be.PutUint32(res[8:], 240<<16)
	be.PutUint16(res[12:], 1)
	be.PutUint16(res[14:], 1)
	resource := func(id uint16, data []byte) []byte {
		b := []byte("8BIM")
		b = be.AppendUint16(b, id)
		b = append(b, 0, 0)
		b = be.AppendUint32(b, uint32(len(data))) //nolint:gosec // G115: test values are short
		b = append(b, data...)
		if len(data)%2 == 1 {
			b = append(b, 0)
		}
		return b
	}
	out := []byte("Photoshop 3.0\x00")
	out = append(out, resource(0x03ED, res)...)
	return append(out, resource(0x0404, iim)...)
}
