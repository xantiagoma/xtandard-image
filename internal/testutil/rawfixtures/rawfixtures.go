// Package rawfixtures provides the real camera RAW files the RAW tests run
// on (WP-49). They are CC0 samples from raw.pixls.us, 5–18 MB each, too
// large to commit: Path downloads each once from a pinned URL, checks its
// SHA-256 and caches it under testdata/cache/raw/ (gitignored). When the
// file is not cached and cannot be downloaded (offline CI, sandbox), or
// XI_TEST_OFFLINE=1 is set, the test is skipped rather than failed; the
// synthetic DNGs in testdata/fixtures/dng/ keep the RAW paths covered
// offline. Provenance and licences: testdata/LICENSES.md.
package rawfixtures

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
)

// EnvOffline skips every download ("1" or "true").
const EnvOffline = "XI_TEST_OFFLINE"

// File is one pinned sample.
type File struct {
	Name   string // cache file name, also the test case name
	Kind   string // codec/raw kind ("cr2", "nef", ...)
	URL    string
	SHA256 string
	// Camera is "Make Model" as LibRaw reports it.
	Camera string
	// Orientation is the EXIF orientation LibRaw reports.
	Orientation int
}

// Files returns the samples, one per format of the WP-49 done criteria.
// Every one is CC0 on raw.pixls.us ("co" licence column).
func Files() []File {
	return []File{
		{
			Name: "canon-eos-350d.cr2", Kind: "cr2", Camera: "Canon EOS 350D", Orientation: 8,
			URL:    "https://raw.pixls.us/getfile.php/1230/nice/Canon%20-%20EOS%20Digital%20Rebel%20XT%20-%20RAW%20(3:2).CR2",
			SHA256: "4b47ba27c0090efa091aaf99babc76d9a3e6f6f121b443d6c58c3ce7275cd770",
		},
		{
			Name: "canon-eos-r6.cr3", Kind: "cr3", Camera: "Canon EOS R6", Orientation: 1,
			URL:    "https://raw.pixls.us/getfile.php/4659/nice/Canon%20-%20EOS%20R6%20-%203:2.CR3",
			SHA256: "74abb0a113d075ad9887a058082f40dd2a938c4813a08474d82356f11a027778",
		},
		{
			Name: "nikon-d70s.nef", Kind: "nef", Camera: "Nikon D70s", Orientation: 1,
			URL:    "https://raw.pixls.us/getfile.php/1117/nice/Nikon%20-%20D70s%20-%2012bit%2012bit%20compressed%20(Lossy%20(type%201))%20(3:2).NEF",
			SHA256: "6c46f9854033e7623ebbb5de8c951df6762ca44f05d45035ccc5cb7e9f4e5692",
		},
		{
			Name: "sony-ilce-7s.arw", Kind: "arw", Camera: "Sony ILCE-7S", Orientation: 1,
			URL:    "https://raw.pixls.us/getfile.php/1582/nice/Sony%20-%20ILCE-7S%20-%2014bit%2014bit%20compressed%20(3:2).ARW",
			SHA256: "a35ebb2fbec929daa5beb20d1ce5c15a8aac7b1a7a231455387f3df8a7442e07",
		},
		{
			Name: "pentax-k-50.dng", Kind: "dng", Camera: "Pentax K-50", Orientation: 1,
			URL:    "https://raw.pixls.us/getfile.php/2235/nice/Pentax%20-%20K-50%20-%2012bit%20(3:2).DNG",
			SHA256: "9ea1dc1f92df19699aa8b620670c64a84d4ead0172a49cbbd47dea9257d14e67",
		},
		{
			Name: "fujifilm-x-s10.raf", Kind: "raf", Camera: "Fujifilm X-S10", Orientation: 1,
			URL:    "https://raw.pixls.us/getfile.php/4190/nice/Fujifilm%20-%20X-S10%20-%2014bit%2014bit%20compressed%20(3:2).RAF",
			SHA256: "9419d1408ebf850395e5dd563beb96309466546252caf541466034c9cb7e9724",
		},
	}
}

// NetworkFiles returns the WP-101/102 samples of the playground gallery
// (docs/research/format-gap.md): bodies LibRaw cannot demosaic, served from
// their embedded previews, and sensors over 50 MP. They are 11–78 MB each,
// so only the `-tags network` tests use them. All are CC0 on raw.pixls.us.
func NetworkFiles() []NetworkFile {
	const base = "https://raw.pixls.us/getfile.php/"
	return []NetworkFile{
		{File{
			Name: "nikon-z50ii-he.nef", Kind: "nef", Camera: "Nikon Z50_2", Orientation: 1,
			URL:    base + "7764/nice/Nikon%20-%20Z50_2%20-%208bit%20compressed%20%283%3A2%29.NEF",
			SHA256: "71c12d398e6f5a01e0e32c2211c40cafdb29e85ba1e17c4f65e7a4e958754c54",
		}, "nikon-he", 5568, 3712},
		{File{
			Name: "nikon-z8-he.nef", Kind: "nef", Camera: "Nikon Z 8", Orientation: 1,
			URL:    base + "6616/nice/Nikon%20-%20Z%208%20-%208bit%208bit%20compressed%20%283%3A2%29.NEF",
			SHA256: "82df041542b8d328738443bb5ff3373b396e406ecc8cbe7dc3b35252790c53e7",
		}, "nikon-he", 8256, 5504},
		{File{
			Name: "nikon-zf-he.nef", Kind: "nef", Camera: "Nikon Z f", Orientation: 1,
			URL:    base + "6887/nice/Nikon%20-%20Z%20f%20-%208bit%208bit%20lossy%20compressed%20%283%3A2%29.NEF",
			SHA256: "98d6ca8e6c98048ca7ffed68ccaeda7b2b9f03807f0d320d97d5678db21748c2",
		}, "nikon-he", 6048, 4032},
		{File{
			Name: "sony-ilce-7m5.arw", Kind: "arw", Camera: "Sony ILCE-7M5", Orientation: 1,
			URL:    base + "8849/nice/Sony%20-%20ILCE-7M5%20-%2014bit%20%283%3A2%29.ARW",
			SHA256: "c16cae99d26a2f8ad183d31a23823310a994aa058d55cfe56e4f82ba56865f57",
		}, "unknown-camera", 4608, 3072},
		{File{
			Name: "fujifilm-gfx100rf.raf", Kind: "raf", Camera: "Fujifilm GFX100RF", Orientation: 1,
			URL:    base + "8091/nice/Fujifilm%20-%20GFX100RF%20-%2016bit%20compressed%20%284%3A3%29.RAF",
			SHA256: "14bed5ac8191d6f2a7c82984e3bf6470ad4740df329293eac42b8018cc6a668a",
		}, "", 11662, 8746},
		{File{
			Name: "hasselblad-x1d-ii.fff", Kind: "3fr", Camera: "Hasselblad X1D-50c", Orientation: 1,
			URL:    base + "3874/nice/Hasselblad%20-%20X1D%20II%2050C%20-%2016bit%20%284%3A3%29.fff",
			SHA256: "8c2451b3478fb529b4e9bd51ed3267999ce26eb1fcb5c3b93c3ae7bc56267794",
		}, "", 8278, 6208},
		{File{
			Name: "google-pixel-8-pro.dng", Kind: "dng", Camera: "Google Pixel 8 Pro", Orientation: 1,
			URL:    base + "7751/nice/Google%20-%20Pixel%208%20Pro%20-%2016bit%20%284%3A3%29.dng",
			SHA256: "45420c595401547cca950ae58d552bc82b32d31ce1d58c082df33004016197f5",
		}, "", 8160, 6144},
	}
}

// NetworkFile is a WP-101/102 sample: its reason to be preview-only
// ("nikon-he", "unknown-camera") or "" and the size the probe reports (the
// preview's for a preview-only file, the demosaic's otherwise).
type NetworkFile struct {
	File
	Undecodable   string
	Width, Height int
}

// Path returns the cached path of f, downloading and verifying it first
// when needed. It skips tb when the file is unavailable.
func Path(tb testing.TB, f *File) string {
	tb.Helper()
	dir := filepath.Join(golden.ModuleRoot(tb), "testdata", "cache", "raw")
	p := filepath.Join(dir, f.Name)
	if ok, err := verify(p, f.SHA256); ok {
		return p
	} else if err != nil && !os.IsNotExist(err) {
		tb.Fatalf("raw fixture %s: %v", f.Name, err)
	}
	if v := os.Getenv(EnvOffline); v == "1" || v == "true" {
		tb.Skipf("raw fixture %s not cached and %s is set", f.Name, EnvOffline)
	}
	switch err := download(tb.Context(), dir, p, f); {
	case errors.Is(err, errDigest):
		tb.Fatalf("raw fixture %s: %v", f.Name, err) // the pinned file changed upstream
	case err != nil:
		tb.Skipf("raw fixture %s unavailable (offline?): %v", f.Name, err)
	}
	return p
}

// Read returns the bytes of f (Path, then os.ReadFile).
func Read(tb testing.TB, f *File) []byte {
	tb.Helper()
	b, err := os.ReadFile(Path(tb, f))
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

// errDigest reports a downloaded file whose SHA-256 is not the pinned one.
var errDigest = errors.New("digest mismatch")

// verify reports whether p exists with the expected digest.
func verify(p, want string) (bool, error) {
	fh, err := os.Open(p)
	if err != nil {
		return false, err
	}
	defer fh.Close()
	h := sha256.New()
	if _, err := io.Copy(h, fh); err != nil {
		return false, err
	}
	return hex.EncodeToString(h.Sum(nil)) == want, nil
}

// download fetches f into a temporary file next to p and renames it into
// place once its digest matches, so concurrent test binaries never see a
// partial file.
func download(ctx context.Context, dir, p string, f *File) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", f.URL, resp.Status)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, f.Name+".*.part")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, 128<<20)); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != f.SHA256 {
		return fmt.Errorf("%w: sha256 %s, want %s", errDigest, got, f.SHA256)
	}
	return os.Rename(tmp.Name(), p)
}
