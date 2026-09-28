//go:build cgo

package app

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/testutil/leakcheck"
)

// designerConfig serves testdata/fixtures on the imgproxy mount and dir as
// source "synth", with debug headers.
func designerConfig(t *testing.T, dir string) *config.Config {
	t.Helper()
	root := golden.ModuleRoot(t)
	yml := `server:
  addr: "127.0.0.1:0"
  shutdown_delay: 0s
admin:
  addr: "127.0.0.2:0"
security:
  unsigned_policy: allow
observability:
  debug_headers: true
sources:
  fixtures:
    type: fs
    root: ` + strconv.Quote(filepath.Join(root, "testdata", "fixtures")) + `
  synth:
    type: fs
    root: ` + strconv.Quote(dir) + `
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
	return &cfg
}

// writeZip writes a ZIP of name→data (deflated) into dir.
func writeZip(t *testing.T, dir, name string, entries ...[2]string) {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, e := range entries {
		fw, err := w.Create(e[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(e[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// designerFixtures are the designer documents of testdata/fixtures, with
// the size of the image they carry and the X-Xi-Container-Source value
// ("" for XCF, which is decoded, not unwrapped).
var designerFixtures = []struct {
	path, container string
	w, h            int
}{
	{"ora/layers.ora", "ora; entry=mergedimage.png; content=composite", 96, 72},
	{"kra/layers.kra", "kra; entry=mergedimage.png; content=composite", 96, 72},
	{"procreate/thumbnail.procreate", "procreate; entry=QuickLook/Thumbnail.png; content=thumbnail", 160, 120},
	{"sketch/preview.sketch", "sketch; entry=previews/preview.png; content=preview", 192, 144},
	{"svgz/logo.svgz", "svgz; entry=gzip; content=document", 0, 0},
	{"pdn/thumbnail.pdn", "pdn; entry=header thumbnail; content=thumbnail", 128, 96},
	{"xcf/layers.xcf", "", 160, 120},
	{"xcf/alpha.xcf", "", 64, 48},
}

// TestDesignerContainers requests every designer fixture through the
// service: the pixels come from the embedded image (or the flattened XCF
// layers), X-Xi-Container-Source names it, /info reports the container,
// and hostile archives fail with 422 while the service keeps working.
func TestDesignerContainers(t *testing.T) {
	synth := t.TempDir()
	png := string(mustRead(t, filepath.Join(golden.ModuleRoot(t), "testdata", "fixtures", "png", "rgba8.png")))
	writeZip(t, synth, "traversal.ora", [2]string{"mimetype", "image/openraster"}, [2]string{"../../etc/passwd", "x"},
		[2]string{"mergedimage.png", png})
	writeZip(t, synth, "bomb.ora", [2]string{"mimetype", "image/openraster"},
		[2]string{"mergedimage.png", string(make([]byte, 32<<20))})
	writeZip(t, synth, "notpng.kra", [2]string{"mimetype", "application/x-krita"}, [2]string{"mergedimage.png", "GIF89a"})
	writeZip(t, synth, "empty.ora", [2]string{"mimetype", "image/openraster"}, [2]string{"stack.xml", "<image/>"})
	writeZip(t, synth, "report.docx", [2]string{"[Content_Types].xml", "<Types/>"})
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="20" height="10" onload="alert(1)"><script>alert(1)</script><rect width="20" height="10" fill="red"/></svg>`))
	_ = zw.Close()
	if err := os.WriteFile(filepath.Join(synth, "script.svgz"), gz.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	a := start(t, designerConfig(t, synth), hooks{})
	base := "http://" + a.public.String()

	for _, f := range designerFixtures {
		t.Run(f.path, func(t *testing.T) {
			r := get(t, base+"/insecure/plain/"+f.path+"@png")
			img := decodePNG(t, r)
			if got := r.header.Get("X-Xi-Container-Source"); got != f.container {
				t.Errorf("X-Xi-Container-Source %q, want %q", got, f.container)
			}
			if b := img.Bounds(); f.w > 0 && (b.Dx() != f.w || b.Dy() != f.h) {
				t.Errorf("%dx%d, want %dx%d", b.Dx(), b.Dy(), f.w, f.h)
			}
			// A second request is a result-cache hit when the cache is on;
			// either way the header is the same.
			if r2 := get(t, base+"/insecure/plain/"+f.path+"@png"); r2.header.Get("X-Xi-Container-Source") != f.container {
				t.Errorf("repeat: X-Xi-Container-Source %q", r2.header.Get("X-Xi-Container-Source"))
			}
			r = get(t, base+"/info/unsafe/plain/"+f.path)
			if r.status != http.StatusOK {
				t.Fatalf("info: %d %s", r.status, r.body)
			}
			var info struct {
				Format    string `json:"format"`
				Width     int    `json:"width"`
				Container *struct {
					Format, Entry, Content string
				} `json:"container"`
			}
			if err := json.Unmarshal(r.body, &info); err != nil {
				t.Fatal(err)
			}
			switch {
			case f.container == "" && (info.Container != nil || info.Format != "xcf"):
				t.Errorf("info: %s", r.body)
			case f.container != "" && info.Container == nil:
				t.Errorf("info without container: %s", r.body)
			case f.container != "" && info.Container.Format+"; entry="+info.Container.Entry+"; content="+info.Container.Content != f.container:
				t.Errorf("info container %+v, want %q", *info.Container, f.container)
			}
		})
	}

	t.Run("svgz is sanitised", func(t *testing.T) {
		img := decodePNG(t, get(t, base+"/insecure/rs:fit:40:40/plain/xi://synth/script.svgz@png"))
		if b := img.Bounds(); b.Dx() != 40 || b.Dy() != 20 {
			t.Errorf("%dx%d, want 40x20", b.Dx(), b.Dy())
		}
	})
	for _, name := range []string{"traversal.ora", "bomb.ora", "notpng.kra", "empty.ora", "report.docx"} {
		t.Run("hostile "+name, func(t *testing.T) {
			r := get(t, base+"/insecure/plain/xi://synth/"+name+"@png")
			if r.status != http.StatusUnprocessableEntity {
				t.Errorf("%d %s, want 422", r.status, r.body)
			}
		})
	}
	// Still serving.
	decodePNG(t, get(t, base+"/insecure/plain/ora/layers.ora@png"))
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestDesignerNoLeak repeats every designer request: unwrapped buffers,
// flattened XCF layers and their scratch copies are all released.
func TestDesignerNoLeak(t *testing.T) {
	a := start(t, designerConfig(t, t.TempDir()), hooks{})
	base := "http://" + a.public.String()
	request := func(i int) {
		f := designerFixtures[i%len(designerFixtures)]
		if r := get(t, base+"/insecure/rs:fit:48:48/plain/"+f.path+"@webp"); r.status != http.StatusOK {
			t.Fatalf("%s: %d %s", f.path, r.status, r.body)
		}
	}
	for i := range 2 * len(designerFixtures) {
		request(i)
	}
	before := leakcheck.Take()
	for i := range 20 * len(designerFixtures) {
		request(i)
	}
	leakcheck.Check(t, before, leakcheck.Default, "requests")
}
