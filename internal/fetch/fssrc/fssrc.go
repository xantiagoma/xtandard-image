// Package fssrc is the local filesystem source (XI_SOURCES_<NAME>_TYPE=fs).
//
// Every access goes through an [os.Root] opened on the configured root
// directory, so "..", absolute paths and symbolic links that point outside the
// root cannot escape it (docs/ARCHITECTURE.md §6). Symbolic links that stay
// inside the root are refused too unless Config.FollowSymlinks is set.
package fssrc

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// Config configures one fs source.
type Config struct {
	// Name is the registered source name.
	Name string
	// Root is the directory the source is jailed to (XI_SOURCES_<NAME>_ROOT).
	Root string
	// FollowSymlinks allows symbolic links whose target stays inside Root.
	// Links that leave Root, and absolute links, are refused regardless
	// because os.Root refuses them. Default false: a key with any symlink
	// component is refused.
	FollowSymlinks bool
}

// Source serves regular files beneath a root directory. It is safe for
// concurrent use. Close releases the root directory handle.
type Source struct {
	name   string
	root   *os.Root
	follow bool
	// escapeErr is os.Root's unexported "path escapes from parent" error,
	// captured once in New so escapes are matched with errors.Is rather than
	// by message text.
	escapeErr error
}

var _ source.Source = (*Source)(nil)

// New opens cfg.Root as an os.Root. The root must exist and be a directory.
func New(cfg Config) (*Source, error) {
	if cfg.Root == "" {
		return nil, fmt.Errorf("fs source %q: root is empty", cfg.Name)
	}
	root, err := os.OpenRoot(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("fs source %q: open root: %w", cfg.Name, err)
	}
	s := &Source{name: cfg.Name, root: root, follow: cfg.FollowSymlinks}
	// Stat("..") on a root always fails with the escape error.
	if _, err := root.Stat(".."); err != nil {
		if pe, ok := errors.AsType[*fs.PathError](err); ok {
			s.escapeErr = pe.Err
		}
	}
	return s, nil
}

// Name returns the configured source name.
func (s *Source) Name() string { return s.name }

// Type returns source.TypeFS.
func (*Source) Type() string { return source.TypeFS }

// Close releases the root directory handle.
func (s *Source) Close() error { return s.root.Close() }

// Fetch opens key.Path beneath the root. Only regular files are served;
// directories and special files are reported as not found. When
// opt.MaxBytes > 0 a larger file fails with KindSourceTooLarge before any
// byte is read. Conditional options (IfNoneMatch, else IfModifiedSince) yield
// an Object with NotModified set and a nil Body.
func (s *Source) Fetch(ctx context.Context, key source.Key, opt source.FetchOptions) (*source.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("fs source %q: %w", s.name, err)
	}
	name, err := localName(key.Path)
	if err != nil {
		return nil, errs.Wrapf(errs.KindForbidden, err, "fs source %q", s.name)
	}
	if !s.follow {
		if err := s.checkNoSymlinks(name); err != nil {
			return nil, err
		}
	}
	info, err := s.root.Stat(name)
	if err != nil {
		return nil, s.classify(err)
	}
	if !info.Mode().IsRegular() {
		return nil, errs.Newf(errs.KindNotFound, "fs source %q: %s is not a regular file", s.name, name)
	}
	f, err := s.root.OpenFile(name, openFlags, 0)
	if err != nil {
		return nil, s.classify(err)
	}
	obj, err := s.object(f, info, opt)
	if err != nil || obj.NotModified {
		_ = f.Close()
	}
	return obj, err
}

// object builds the Object for the opened file f whose pre-open Stat was
// before. The caller closes f unless the returned Object carries it as Body.
func (s *Source) object(f *os.File, before fs.FileInfo, opt source.FetchOptions) (*source.Object, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, s.classify(err)
	}
	// The path may have been swapped between Stat and Open; os.Root still
	// jails it, but refuse anything that is no longer the same regular file.
	if !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return nil, errs.Newf(errs.KindForbidden, "fs source %q: file changed while opening", s.name)
	}
	size := info.Size()
	if opt.MaxBytes > 0 && size > opt.MaxBytes {
		return nil, errs.Newf(errs.KindSourceTooLarge, "fs source %q: %d bytes exceeds limit %d", s.name, size, opt.MaxBytes)
	}
	obj := &source.Object{
		Size:         size,
		ContentType:  contentType(f.Name()),
		ETag:         etag(info),
		LastModified: info.ModTime().UTC().Truncate(time.Second),
	}
	if notModified(obj, opt) {
		obj.NotModified = true
		return obj, nil
	}
	obj.Body = f
	return obj, nil
}

// notModified evaluates the conditional options as RFC 9110 §13.2.2 orders
// them: If-None-Match (weak comparison) takes precedence over
// If-Modified-Since, which compares at one-second resolution.
func notModified(obj *source.Object, opt source.FetchOptions) bool {
	if opt.IfNoneMatch != "" {
		return etagMatches(opt.IfNoneMatch, obj.ETag)
	}
	if !opt.IfModifiedSince.IsZero() {
		return !obj.LastModified.After(opt.IfModifiedSince)
	}
	return false
}

// etagMatches reports whether the If-None-Match list is "*" or contains tag
// under weak comparison.
func etagMatches(list, tag string) bool {
	want := strings.TrimPrefix(tag, "W/")
	for c := range strings.SplitSeq(list, ",") {
		c = strings.TrimSpace(c)
		if c == "*" || strings.TrimPrefix(c, "W/") == want {
			return true
		}
	}
	return false
}

// localName converts a Key.Path into an os.Root name, refusing empty and
// absolute paths, ".." segments and NUL bytes before the kernel sees them.
// os.Root refuses escapes anyway; this keeps the error kind deterministic.
func localName(p string) (string, error) {
	if strings.IndexByte(p, 0) >= 0 {
		return "", errors.New("path contains NUL")
	}
	name := filepath.FromSlash(p)
	if !filepath.IsLocal(name) {
		return "", fmt.Errorf("path %q is not local to the root", p)
	}
	return filepath.Clean(name), nil
}

// checkNoSymlinks refuses name when any of its components is a symbolic
// link. A concurrent swap after this check can at worst make os.Root follow a
// link that stays inside the root, and Fetch refuses a final component that
// no longer is the file it stat'ed.
func (s *Source) checkNoSymlinks(name string) error {
	prefix := ""
	for part := range strings.SplitSeq(name, string(filepath.Separator)) {
		prefix = filepath.Join(prefix, part)
		fi, err := s.root.Lstat(prefix)
		if err != nil {
			return s.classify(err)
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return errs.Newf(errs.KindForbidden, "fs source %q: symlink %s not allowed", s.name, prefix)
		}
	}
	return nil
}

// classify maps filesystem errors to error kinds: missing → NotFound;
// permission, root escape or refused symlink → Forbidden; anything else
// (EIO, ...) → UpstreamError.
func (s *Source) classify(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return errs.Wrapf(errs.KindNotFound, err, "fs source %q", s.name)
	case errors.Is(err, fs.ErrPermission),
		s.escapeErr != nil && errors.Is(err, s.escapeErr),
		isSymlinkRefused(err):
		return errs.Wrapf(errs.KindForbidden, err, "fs source %q", s.name)
	default:
		return errs.Wrapf(errs.KindUpstreamError, err, "fs source %q", s.name)
	}
}

// etag derives a weak validator from size, modification time (ns) and inode.
// It changes when the file is rewritten or replaced (new inode) and is weak
// because equal metadata does not strictly guarantee equal bytes. The values
// are hashed (FNV-1a 64) so the inode number is not disclosed to clients.
func etag(info fs.FileInfo) string {
	var buf [64]byte
	b := strconv.AppendInt(buf[:0], info.Size(), 16)
	b = append(b, '-')
	b = strconv.AppendInt(b, info.ModTime().UnixNano(), 16)
	b = append(b, '-')
	b = strconv.AppendUint(b, inode(info), 16)
	h := fnv.New64a()
	_, _ = h.Write(b)
	out := append(buf[:0], `W/"`...)
	out = strconv.AppendUint(out, h.Sum64(), 16)
	out = append(out, '"')
	return string(out)
}

// contentType returns a media type guessed from the file extension. It is a
// hint only (the codec layer sniffs the bytes); a fixed table keeps the
// result independent of the host's mime.types.
func contentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".avif":
		return "image/avif"
	case ".bmp":
		return "image/bmp"
	case ".gif":
		return "image/gif"
	case ".heic":
		return "image/heic"
	case ".heif":
		return "image/heif"
	case ".ico":
		return "image/x-icon"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".jxl":
		return "image/jxl"
	case ".pdf":
		return "application/pdf"
	case ".png":
		return "image/png"
	case ".svg":
		return "image/svg+xml"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}
