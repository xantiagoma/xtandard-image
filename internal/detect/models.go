package detect

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// checksumsFile is the sha256sum(1) listing shipped next to the models in
// the full image (docker/base, WP-70).
const checksumsFile = "SHA256SUMS"

// resolveModel returns the path of a model file: name itself when it is
// absolute, else name inside dir. A missing file wraps ErrUnavailable (the
// slim image has no models directory).
func resolveModel(dir, name string) (string, error) {
	path := name
	if !filepath.IsAbs(name) {
		path = filepath.Join(dir, name)
	}
	st, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("%w: model %s not found", ErrUnavailable, path)
	case err != nil:
		return "", errs.Wrapf(errs.KindInternal, err, "detect: model %s", path)
	case !st.Mode().IsRegular():
		return "", errs.Newf(errs.KindInternal, "detect: model %s is not a regular file", path)
	}
	return path, nil
}

// verifyChecksum checks path against the SHA256SUMS file of its directory.
// It reports whether the file was listed; a listed file whose digest
// differs is an error. A directory without SHA256SUMS lists nothing.
func verifyChecksum(path string) (listed bool, err error) {
	want, err := listedDigest(filepath.Join(filepath.Dir(path), checksumsFile), filepath.Base(path))
	if err != nil || want == "" {
		return false, err
	}
	f, err := os.Open(path)
	if err != nil {
		return true, errs.Wrapf(errs.KindInternal, err, "detect: open model %s", path)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return true, errs.Wrapf(errs.KindInternal, err, "detect: read model %s", path)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return true, errs.Newf(errs.KindInternal, "detect: model %s has sha256 %s, %s lists %s", path, got, checksumsFile, want)
	}
	return true, nil
}

// listedDigest returns the lower-case hex digest SHA256SUMS lists for name,
// or "" when the file or the entry does not exist. Lines are
// "<hex>  <name>" or "<hex> *<name>" (binary mode).
func listedDigest(sums, name string) (string, error) {
	f, err := os.Open(sums)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", errs.Wrapf(errs.KindInternal, err, "detect: open %s", sums)
	}
	defer f.Close()
	return parseChecksums(f, name)
}

// parseChecksums scans a sha256sum listing for name.
func parseChecksums(r io.Reader, name string) (string, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		digest, file, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		if !ok {
			continue
		}
		file = strings.TrimPrefix(strings.TrimLeft(file, " "), "*")
		if file == name && len(digest) == 2*sha256.Size {
			if _, err := hex.DecodeString(digest); err == nil {
				return strings.ToLower(digest), nil
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", errs.Wrapf(errs.KindInternal, err, "detect: read %s", checksumsFile)
	}
	return "", nil
}
