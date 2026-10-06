// Package disk is the on-disk cache.Store (docs/ARCHITECTURE.md §9.2,
// XI_CACHE_RESULT_DISK_PATH / XI_CACHE_RESULT_DISK_BYTES).
//
// Layout: one file per entry at <dir>/<h[0:2]>/<h[2:4]>/<h>, where h is the
// hex SHA-256 of namespace+key, so a directory never holds more than a few
// thousand files and keys never reach the file system. A file is a
// cache.AppendFrame frame (magic, metadata length, JSON cache.Meta) followed
// by the body; the metadata carries the full key, the TTL and a CRC-32C of
// the body.
//
// Writes go to a temporary file in the shard directory and are renamed
// over the final name, so readers only ever see complete files. There is
// no fsync: the cache is disposable, and a file torn by a power loss fails
// its length or checksum check on read, is removed and counted
// (xi_cache_skipped_total{reason="corrupt"}), exactly like any other
// corrupt entry. Leftover temporary files are removed at start.
//
// Size bound: an in-memory index (name, size, expiry, recency) is rebuilt
// by scanning the directory in New (file modification time orders the
// LRU, so recency across restarts is write time) and kept current by Get,
// Set and Delete. A janitor goroutine evicts least recently used entries
// down to 90 % of MaxBytes whenever a write pushes usage over MaxBytes,
// and on every JanitorInterval tick also removes entries whose TTL has
// passed. The janitor uses time.Ticker and time.Now only, so it is
// deterministic under testing/synctest.
//
// The directory must be writable and dedicated to one process: with the
// read-only root file system of the production image it has to be a
// mounted volume (docs/CONFIG.md).
package disk

import (
	"cmp"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/cache"
)

// Name is the default store label in metrics.
const Name = "disk"

// DefaultJanitorInterval is how often the janitor removes expired entries
// when Options.JanitorInterval is zero.
const DefaultJanitorInterval = time.Minute

// DefaultFileBodyBytes is Options.FileBodyBytes when zero.
const DefaultFileBodyBytes = 8 << 20

const (
	nameLen = 2 * sha256.Size // hex file name
	tmpExt  = ".tmp"
	dirPerm = 0o750
)

// Options configures New.
type Options struct {
	// Dir is the cache directory; it is created if missing and must be
	// writable.
	Dir string
	// MaxBytes bounds the total size of the entry files; it must be > 0.
	MaxBytes int64
	// Name labels the metrics; "" uses Name.
	Name string
	// Metrics receives lookups, evictions, skips and size; nil discards.
	Metrics cache.Metrics
	// JanitorInterval is the period of the expiry sweep; ≤ 0 uses
	// DefaultJanitorInterval.
	JanitorInterval time.Duration
	// Now is the clock for TTLs; nil uses time.Now.
	Now func() time.Time
	// FileBodyBytes: a hit whose body has at least this many bytes is
	// returned as a cache.FileBuffer over the entry's file (read on
	// demand, so a Range request on a large video reads only its range)
	// instead of being read into memory; its length is checked, its
	// CRC-32C is not (a torn file is caught by the length). 0 uses
	// DefaultFileBodyBytes; negative never.
	FileBodyBytes int64
}

// Store is the disk cache. It is safe for concurrent use.
type Store struct {
	dir     string
	name    string
	metrics cache.Metrics
	max     int64
	low     int64 // eviction target: 90 % of max
	fileMin int64 // Options.FileBodyBytes; < 0 never
	now     func() time.Time

	mu     sync.Mutex
	used   int64
	ll     list.List // front = most recently used; values are *item
	items  map[string]*list.Element
	closed bool

	kick chan struct{} // capacity 1: asks the janitor to evict
	done chan struct{}
	wg   sync.WaitGroup
	once sync.Once
}

type item struct {
	name    string
	size    int64
	expires int64 // unix nanoseconds; 0 = never or not yet known
}

// New opens (creating it if needed) the cache directory, checks that it
// is writable, rebuilds the index from the files already there and starts
// the janitor, which Close stops.
func New(opts Options) (*Store, error) {
	if opts.MaxBytes <= 0 {
		return nil, fmt.Errorf("disk cache: max bytes must be > 0, got %d", opts.MaxBytes)
	}
	if !filepath.IsAbs(opts.Dir) {
		return nil, fmt.Errorf("disk cache: directory %q must be absolute", opts.Dir)
	}
	s := &Store{
		dir:     filepath.Clean(opts.Dir),
		name:    cmp.Or(opts.Name, Name),
		metrics: opts.Metrics,
		max:     opts.MaxBytes,
		low:     opts.MaxBytes - opts.MaxBytes/10,
		fileMin: opts.FileBodyBytes,
		now:     opts.Now,
		items:   make(map[string]*list.Element),
		kick:    make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	if s.metrics == nil {
		s.metrics = cache.NopMetrics{}
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.fileMin == 0 {
		s.fileMin = DefaultFileBodyBytes
	}
	if err := os.MkdirAll(s.dir, dirPerm); err != nil {
		return nil, fmt.Errorf("disk cache: %w (the directory must be a writable volume)", err)
	}
	probe, err := os.CreateTemp(s.dir, "probe-*"+tmpExt)
	if err != nil {
		return nil, fmt.Errorf("disk cache: directory not writable: %w (mount a volume there)", err)
	}
	_ = probe.Close()           // empty probe file: nothing to flush
	_ = os.Remove(probe.Name()) // best effort; a leftover is removed by the next scan
	if err := s.scan(); err != nil {
		return nil, err
	}
	interval := opts.JanitorInterval
	if interval <= 0 {
		interval = DefaultJanitorInterval
	}
	s.mu.Lock()
	over := s.used > s.max
	s.reportSizeLocked()
	s.mu.Unlock()
	if over {
		s.kick <- struct{}{}
	}
	// Owner: the Store; exits when Close closes done.
	s.wg.Go(func() { s.janitor(interval) })
	return s, nil
}

// scan rebuilds the index from the directory tree: entry files ordered by
// modification time (oldest at the LRU end), temporary files removed,
// anything else ignored.
func (s *Store) scan() error {
	type found struct {
		name  string
		size  int64
		mtime time.Time
	}
	var files []found
	err := filepath.WalkDir(s.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		base := d.Name()
		if strings.HasSuffix(base, tmpExt) {
			_ = os.Remove(p) //nolint:gosec // G122: the directory is ours alone; best effort, a failure leaves the file for the next start
			return nil
		}
		if !validName(base) || p != s.path(base) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		files = append(files, found{name: base, size: info.Size(), mtime: info.ModTime()})
		return nil
	})
	if err != nil {
		return fmt.Errorf("disk cache: scan %s: %w", s.dir, err)
	}
	slices.SortFunc(files, func(a, b found) int { return a.mtime.Compare(b.mtime) })
	for _, f := range files {
		s.items[f.name] = s.ll.PushFront(&item{name: f.name, size: f.size})
		s.used += f.size
	}
	return nil
}

// Get implements cache.Store. A missing, expired or corrupt entry is a
// miss; corrupt and expired files are removed. A hit's body is a fresh Go
// buffer owned by the caller.
func (s *Store) Get(_ context.Context, ns cache.Namespace, key string) (cache.Entry, bool, error) {
	if s.isClosed() {
		return cache.Entry{}, false, cache.ErrClosed
	}
	full := string(ns) + key
	name := fileName(full)
	p := s.path(name)
	if s.fileMin > 0 {
		if e, ok, done := s.getFile(name, p, full); done {
			return e, ok, nil
		}
	}
	b, err := os.ReadFile(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		s.forget(name)
		s.metrics.ObserveCache(s.name, false)
		return cache.Entry{}, false, nil
	case err != nil:
		s.metrics.ObserveCache(s.name, false)
		return cache.Entry{}, false, fmt.Errorf("disk cache: read: %w", err)
	}
	m, body, ok := cache.ParseEntry(b, full)
	if !ok {
		s.remove(name)
		s.metrics.ObserveCacheSkip(s.name, cache.SkipCorrupt)
		s.metrics.ObserveCache(s.name, false)
		return cache.Entry{}, false, nil
	}
	if m.Expired(s.now()) {
		s.remove(name)
		s.metrics.ObserveCache(s.name, false)
		return cache.Entry{}, false, nil
	}
	s.touch(name, int64(len(b)), m.ExpiresAt)
	s.metrics.ObserveCache(s.name, true)
	return m.Entry(cache.NewBuffer(body, nil)), true, nil
}

// getFile answers a hit whose body has at least fileMin bytes with a
// cache.FileBuffer over the entry's file, after reading only its frame:
// done is false when the file is missing, small or unreadable, which Get
// then handles as before.
func (s *Store) getFile(name, p, full string) (e cache.Entry, ok, done bool) {
	f, err := os.Open(p) //nolint:gosec // G304: p is <dir>/<hex>/<hex>/<sha256 hex>, never request text
	if err != nil {
		return cache.Entry{}, false, false
	}
	keep := false
	defer func() {
		if !keep {
			_ = f.Close() // read-only
		}
	}()
	info, err := f.Stat()
	if err != nil || info.Size() < s.fileMin+int64(cache.FramePrefixLen) {
		return cache.Entry{}, false, false
	}
	m, off, err := cache.ReadFrame(f)
	if err != nil || m.Key != full || off+m.Size != info.Size() {
		s.remove(name)
		s.metrics.ObserveCacheSkip(s.name, cache.SkipCorrupt)
		s.metrics.ObserveCache(s.name, false)
		return cache.Entry{}, false, true
	}
	if m.Expired(s.now()) {
		s.remove(name)
		s.metrics.ObserveCache(s.name, false)
		return cache.Entry{}, false, true
	}
	s.touch(name, info.Size(), m.ExpiresAt)
	s.metrics.ObserveCache(s.name, true)
	keep = true
	return m.Entry(cache.NewFileBuffer(f, off, m.Size, nil)), true, true
}

// Set implements cache.Store: the entry is written to a temporary file
// and renamed into place. An entry larger than MaxBytes is skipped (not an
// error). The caller keeps its reference to e.Body.
func (s *Store) Set(_ context.Context, ns cache.Namespace, key string, e *cache.Entry, ttl time.Duration) error {
	if e.Body == nil {
		return errors.New("disk cache: entry without body")
	}
	if s.isClosed() {
		return cache.ErrClosed
	}
	full := string(ns) + key
	if cache.BodyLen(e.Body) > s.max {
		s.metrics.ObserveCacheSkip(s.name, cache.SkipTooLarge)
		return nil
	}
	m, err := cache.NewMeta(full, e, s.now(), ttl)
	if err != nil {
		return err
	}
	head, err := cache.AppendFrame(nil, &m)
	if err != nil {
		return err
	}
	size := int64(len(head)) + m.Size
	if size > s.max {
		s.metrics.ObserveCacheSkip(s.name, cache.SkipTooLarge)
		return nil
	}
	name := fileName(full)
	p := s.path(name)
	tmp, err := s.writeTemp(filepath.Dir(p), name, head, e.Body)
	if err != nil {
		return err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = os.Remove(tmp) // closed meanwhile: the write is abandoned
		return cache.ErrClosed
	}
	if err := os.Rename(tmp, p); err != nil {
		s.mu.Unlock()
		_ = os.Remove(tmp) // the rename error is what matters
		return fmt.Errorf("disk cache: %w", err)
	}
	if el, ok := s.items[name]; ok {
		s.unlinkLocked(el)
	}
	s.items[name] = s.ll.PushFront(&item{name: name, size: size, expires: m.ExpiresAt})
	s.used += size
	over := s.used > s.max
	s.reportSizeLocked()
	s.mu.Unlock()
	if over {
		select {
		case s.kick <- struct{}{}:
		default: // a sweep is already pending
		}
	}
	return nil
}

// writeTemp writes head and body to a new temporary file in dir and
// returns its path. A file body is streamed from its file.
func (s *Store) writeTemp(dir, name string, head []byte, body cache.Buffer) (string, error) {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return "", fmt.Errorf("disk cache: %w", err)
	}
	f, err := os.CreateTemp(dir, name+".*"+tmpExt)
	if err != nil {
		return "", fmt.Errorf("disk cache: %w", err)
	}
	_, err = f.Write(head)
	if err == nil {
		err = cache.WriteBody(f, body)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name()) // the write error is what matters
		return "", fmt.Errorf("disk cache: write: %w", err)
	}
	return f.Name(), nil
}

// Delete implements cache.Store.
func (s *Store) Delete(_ context.Context, ns cache.Namespace, key string) error {
	name := fileName(string(ns) + key)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return cache.ErrClosed
	}
	if el, ok := s.items[name]; ok {
		s.unlinkLocked(el)
		s.reportSizeLocked()
	}
	if err := os.Remove(s.path(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("disk cache: %w", err)
	}
	return nil
}

// Close implements cache.Store: it stops the janitor. The files stay for
// the next process.
func (s *Store) Close() error {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.done)
		s.wg.Wait()
	})
	return nil
}

// Usage reports the bytes and entries the index holds.
func (s *Store) Usage() (bytes int64, entries int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.used, s.ll.Len()
}

// janitor evicts on a kick (usage over the bound) and sweeps expired
// entries on every tick, until Close.
func (s *Store) janitor(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-s.kick:
			s.sweep(false)
		case <-t.C:
			s.sweep(true)
		}
	}
}

// sweep removes expired entries (when expired is set) and then least
// recently used ones until usage is at most the low watermark, if it is
// over the bound. Files are unlinked under the lock so a concurrent Set of
// the same key cannot lose its new file.
func (s *Store) sweep(expired bool) {
	now := s.now().UnixNano()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	n := 0
	if expired {
		for el := s.ll.Back(); el != nil; {
			prev := el.Prev()
			if it := itemOf(el); it.expires != 0 && now >= it.expires {
				s.deleteLocked(el)
				n++
			}
			el = prev
		}
	}
	if s.used > s.max {
		for s.used > s.low && s.ll.Len() > 0 {
			s.deleteLocked(s.ll.Back())
			n++
		}
	}
	if n > 0 {
		s.metrics.ObserveCacheEvictions(s.name, n)
		s.reportSizeLocked()
	}
}

// touch marks name as just used, adding it to the index when another
// process (or a previous run racing the scan) wrote it.
func (s *Store) touch(name string, size, expires int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if el, ok := s.items[name]; ok {
		it := itemOf(el)
		s.used += size - it.size
		it.size, it.expires = size, expires
		s.ll.MoveToFront(el)
		return
	}
	s.items[name] = s.ll.PushFront(&item{name: name, size: size, expires: expires})
	s.used += size
	s.reportSizeLocked()
}

// forget drops name from the index after its file vanished.
func (s *Store) forget(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if el, ok := s.items[name]; ok {
		s.unlinkLocked(el)
		s.reportSizeLocked()
	}
}

// remove deletes the file of name (expired or corrupt) and its index
// entry.
func (s *Store) remove(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if el, ok := s.items[name]; ok {
		s.unlinkLocked(el)
		s.reportSizeLocked()
	}
	_ = os.Remove(s.path(name)) // best effort: a leftover is found again by Get or evicted
}

// deleteLocked removes el from the index and its file from disk.
func (s *Store) deleteLocked(el *list.Element) {
	name := s.unlinkLocked(el)
	_ = os.Remove(s.path(name)) // best effort: a leftover is re-indexed at the next start
}

// unlinkLocked removes el from the index and returns its name.
func (s *Store) unlinkLocked(el *list.Element) string {
	it := itemOf(el)
	s.ll.Remove(el)
	delete(s.items, it.name)
	s.used -= it.size
	return it.name
}

func (s *Store) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Store) reportSizeLocked() { s.metrics.SetCacheSize(s.name, s.used, s.ll.Len()) }

// path returns the file of name: <dir>/<name[0:2]>/<name[2:4]>/<name>.
func (s *Store) path(name string) string {
	return filepath.Join(s.dir, name[0:2], name[2:4], name)
}

// fileName is the hex SHA-256 of the full key.
func fileName(full string) string {
	sum := sha256.Sum256([]byte(full))
	return hex.EncodeToString(sum[:])
}

func validName(s string) bool {
	if len(s) != nameLen {
		return false
	}
	for i := range len(s) {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// itemOf returns the item stored in el; every element holds one.
func itemOf(el *list.Element) *item {
	it, _ := el.Value.(*item)
	return it
}
