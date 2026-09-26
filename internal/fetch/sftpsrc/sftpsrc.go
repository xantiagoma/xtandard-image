// Package sftpsrc is the sftp source backend (docs/ARCHITECTURE.md §6): a
// small bounded pool of authenticated SSH connections, each carrying one
// SFTP session (github.com/pkg/sftp over golang.org/x/crypto/ssh).
//
// The server's host key must be pinned in the configuration: there is no
// trust-on-first-use and no known_hosts lookup, and New refuses a Config
// without a host key. Cancelling the fetch context closes the connection,
// so a stuck server can never pin a goroutine past the request.
package sftpsrc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch/internal/connpool"
	"github.com/xantiagoma/xtandard-image/internal/fetch/internal/filemeta"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// Defaults applied by New to zero Config fields.
const (
	DefaultPort        = 22
	DefaultPoolSize    = 4
	DefaultDialTimeout = 10 * time.Second
	DefaultIdleTimeout = time.Minute
)

var (
	// ErrInvalidConfig reports a Config New rejects.
	ErrInvalidConfig = errors.New("sftpsrc: invalid config")
	// ErrHostKeyMismatch is wrapped by fetch errors when the server presents
	// a host key other than the pinned ones.
	ErrHostKeyMismatch = errors.New("sftpsrc: host key mismatch")

	errClosedEarly = errors.New("sftpsrc: body closed before EOF")
)

// Config is one sftp source (docs/CONFIG.md XI_SOURCES_<NAME>_*).
type Config struct {
	Name string // source name
	Host string // host name or IP literal, without port
	Port int    // 0 = 22
	User string
	// Password and/or PrivateKey authenticate; at least one is required.
	// PrivateKey is a PEM or OpenSSH private key, optionally encrypted with
	// PrivateKeyPassphrase.
	Password             string
	PrivateKey           string
	PrivateKeyPassphrase string
	// HostKey pins the server's public host key(s), one per line, in
	// authorized_keys ("ssh-ed25519 AAAA... comment") or known_hosts
	// ("host ssh-ed25519 AAAA...") form. Required.
	HostKey string
	// Root is the server directory keys are relative to; "" means "/".
	Root string
	// PoolSize bounds concurrent connections to this server (default 4).
	PoolSize int
	// DialTimeout bounds connect, SSH handshake and SFTP session start
	// (default 10s); the fetch context's deadline applies too.
	DialTimeout time.Duration
	// IdleTimeout closes pooled connections idle for longer (default 1m).
	IdleTimeout time.Duration
}

// Source serves one sftp server. It is safe for concurrent use.
type Source struct {
	name   string
	addr   string
	root   string
	ssh    *ssh.ClientConfig
	dialer net.Dialer
	pool   *connpool.Pool[*conn]
}

var _ source.Source = (*Source)(nil)

// New validates cfg and returns a Source. No connection is opened until the
// first fetch.
func New(cfg *Config) (*Source, error) {
	var problems []error
	bad := func(format string, args ...any) {
		problems = append(problems, fmt.Errorf("%w: source %q: %s", ErrInvalidConfig, cfg.Name, fmt.Sprintf(format, args...)))
	}
	if cfg.Name == "" {
		bad("empty name")
	}
	if cfg.Host == "" || strings.ContainsAny(cfg.Host, "/ \t\r\n") {
		bad("bad host %q", cfg.Host)
	}
	if cfg.Port < 0 || cfg.Port > 65535 {
		bad("bad port %d", cfg.Port)
	}
	if cfg.User == "" {
		bad("empty user")
	}
	if cfg.PoolSize < 0 || cfg.DialTimeout < 0 || cfg.IdleTimeout < 0 {
		bad("negative pool size or timeout")
	}
	pinned, err := parseHostKeys(cfg.HostKey)
	if err != nil {
		bad("%v", err)
	}
	var auth []ssh.AuthMethod
	if cfg.PrivateKey != "" {
		signer, err := parsePrivateKey(cfg.PrivateKey, cfg.PrivateKeyPassphrase)
		if err != nil {
			bad("private key: %v", err)
		} else {
			auth = append(auth, ssh.PublicKeys(signer))
		}
	}
	if cfg.Password != "" {
		auth = append(auth, ssh.Password(cfg.Password))
	}
	if cfg.Password == "" && cfg.PrivateKey == "" {
		bad("a password or private key is required")
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}

	port := cmpOr(cfg.Port, DefaultPort)
	dialTimeout := cmpOr(cfg.DialTimeout, DefaultDialTimeout)
	s := &Source{
		name:   cfg.Name,
		addr:   net.JoinHostPort(cfg.Host, strconv.Itoa(port)),
		root:   cfg.Root,
		dialer: net.Dialer{Timeout: dialTimeout},
		ssh: &ssh.ClientConfig{
			User:              cfg.User,
			Auth:              auth,
			HostKeyCallback:   pinnedCallback(pinned),
			HostKeyAlgorithms: hostKeyAlgorithms(pinned),
			Timeout:           dialTimeout,
		},
	}
	s.pool = connpool.New(connpool.Config[*conn]{
		Size:        cmpOr(cfg.PoolSize, DefaultPoolSize),
		IdleTimeout: cmpOr(cfg.IdleTimeout, DefaultIdleTimeout),
		Dial:        s.dial,
		Close:       closeConn,
	})
	return s, nil
}

func cmpOr[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}

// parseHostKeys reads the pinned keys: one per non-empty, non-comment line,
// authorized_keys or known_hosts form.
func parseHostKeys(s string) ([]ssh.PublicKey, error) {
	var keys []ssh.PublicKey
	for line := range strings.Lines(s) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			_, _, key, _, _, err = ssh.ParseKnownHosts([]byte(line))
		}
		if err != nil {
			return nil, fmt.Errorf("bad host key line: %w", err)
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, errors.New("host key is required (no trust on first use)")
	}
	return keys, nil
}

func parsePrivateKey(pem, passphrase string) (ssh.Signer, error) {
	if passphrase != "" {
		return ssh.ParsePrivateKeyWithPassphrase([]byte(pem), []byte(passphrase))
	}
	return ssh.ParsePrivateKey([]byte(pem))
}

// pinnedCallback accepts exactly the pinned keys, whatever the host name.
func pinnedCallback(pinned []ssh.PublicKey) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		got := key.Marshal()
		for _, k := range pinned {
			if bytes.Equal(k.Marshal(), got) {
				return nil
			}
		}
		return fmt.Errorf("%w: server offered %s %s", ErrHostKeyMismatch, key.Type(), ssh.FingerprintSHA256(key))
	}
}

// hostKeyAlgorithms asks the server for the pinned key types only, so a
// server holding several host keys presents the pinned one.
func hostKeyAlgorithms(pinned []ssh.PublicKey) []string {
	var algos []string
	add := func(a string) {
		if !slices.Contains(algos, a) {
			algos = append(algos, a)
		}
	}
	for _, k := range pinned {
		if k.Type() == ssh.KeyAlgoRSA {
			add(ssh.KeyAlgoRSASHA512)
			add(ssh.KeyAlgoRSASHA256)
		}
		add(k.Type())
	}
	return algos
}

// Name implements source.Source.
func (s *Source) Name() string { return s.name }

// Type implements source.Source.
func (s *Source) Type() string { return source.TypeSFTP }

// Close closes the idle connections; leased ones close when their body does.
func (s *Source) Close() error { return s.pool.Close() }

// Fetch implements source.Source. Size and LastModified come from a stat
// before the file is opened; ETag is the weak size+mtime validator of
// filemeta.
func (s *Source) Fetch(ctx context.Context, key source.Key, opt source.FetchOptions) (*source.Object, error) {
	p := filemeta.Join(s.root, key.Path)
	obj, retry, err := s.fetchOnce(ctx, p, opt)
	if retry {
		// A pooled connection the server dropped while idle: one fresh try.
		obj, _, err = s.fetchOnce(ctx, p, opt)
	}
	return obj, err
}

func (s *Source) fetchOnce(ctx context.Context, p string, opt source.FetchOptions) (obj *source.Object, retry bool, err error) {
	lease, err := s.pool.Get(ctx)
	if err != nil {
		return nil, false, classify(ctx, err, "sftp connect")
	}
	c := lease.Conn
	stop := context.AfterFunc(ctx, func() { c.abort(context.Cause(ctx)) })
	dl, _ := ctx.Deadline()
	_ = c.raw.SetDeadline(dl)

	finish := func(opErr error) {
		aborted := !stop() || c.isAborted()
		if aborted || (opErr != nil && isTransport(opErr)) {
			lease.Discard()
			return
		}
		_ = c.raw.SetDeadline(time.Time{})
		lease.Release()
	}
	fail := func(opErr error, what string) (*source.Object, bool, error) {
		finish(opErr)
		retry := lease.Reused && isTransport(opErr) && ctx.Err() == nil
		return nil, retry, classify(ctx, opErr, what)
	}

	fi, err := c.sftp.Stat(p)
	if err != nil {
		return fail(err, "sftp stat")
	}
	if !fi.Mode().IsRegular() {
		finish(nil)
		return nil, false, errs.New(errs.KindNotFound, "sftp: not a regular file")
	}
	size, mtime := fi.Size(), fi.ModTime().UTC()
	meta := &source.Object{Size: size, ETag: filemeta.ETag(size, mtime), LastModified: mtime}
	if filemeta.NotModified(opt.IfNoneMatch, opt.IfModifiedSince, meta.ETag, mtime) {
		finish(nil)
		meta.NotModified = true
		return meta, false, nil
	}
	if opt.MaxBytes > 0 && size > opt.MaxBytes {
		finish(nil)
		return nil, false, errs.Newf(errs.KindSourceTooLarge, "sftp object is %d bytes, limit %d", size, opt.MaxBytes)
	}
	f, err := c.sftp.Open(p)
	if err != nil {
		return fail(err, "sftp open")
	}
	meta.Body = &body{f: f, c: c, finish: finish}
	return meta, false, nil
}

// body streams one open remote file.
type body struct {
	f      *sftp.File
	c      *conn
	finish func(error)

	once sync.Once
}

func (b *body) Read(p []byte) (int, error) {
	n, err := b.f.Read(p)
	switch {
	case err == nil:
		return n, nil
	case errors.Is(err, io.EOF):
		b.done(nil)
		return n, io.EOF
	default:
		cerr := b.c.readErr(err)
		b.c.abort(err)
		b.done(err)
		return n, cerr
	}
}

// done closes the remote handle and returns the connection to the pool
// (unless it broke).
func (b *body) done(readErr error) {
	b.once.Do(func() {
		err := readErr
		if cerr := b.f.Close(); err == nil {
			err = cerr
		}
		b.finish(err)
	})
}

// Close ends the transfer. Closing before EOF drops the connection, since
// read-ahead requests may still be in flight on it.
func (b *body) Close() error {
	b.once.Do(func() {
		b.c.abort(errClosedEarly)
		_ = b.f.Close()
		b.finish(errClosedEarly)
	})
	return nil
}

// conn is one pooled SSH connection with its SFTP session.
type conn struct {
	raw  net.Conn
	ssh  *ssh.Client
	sftp *sftp.Client

	mu      sync.Mutex
	aborted error // why abort closed the connection; nil while usable
}

func (s *Source) dial(ctx context.Context) (*conn, error) {
	raw, err := s.dialer.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return nil, err
	}
	c := &conn{raw: raw}
	stop := context.AfterFunc(ctx, func() { c.abort(context.Cause(ctx)) })
	dl := time.Now().Add(s.dialer.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(dl) {
		dl = d
	}
	_ = raw.SetDeadline(dl)
	err = c.handshake(s.addr, s.ssh)
	aborted := !stop()
	if err == nil && aborted {
		err = context.Cause(ctx)
	}
	if err != nil {
		c.abort(err)
		return nil, err
	}
	_ = raw.SetDeadline(time.Time{})
	return c, nil
}

func (c *conn) handshake(addr string, cfg *ssh.ClientConfig) error {
	sc, chans, reqs, err := ssh.NewClientConn(c.raw, addr, cfg)
	if err != nil {
		return err
	}
	c.ssh = ssh.NewClient(sc, chans, reqs)
	c.sftp, err = sftp.NewClient(c.ssh)
	return err
}

// abort closes the connection, unblocking any I/O; cause is what readErr
// reports afterwards.
func (c *conn) abort(cause error) {
	c.mu.Lock()
	if c.aborted == nil {
		c.aborted = cmpOr(cause, net.ErrClosed)
	}
	c.mu.Unlock()
	_ = c.raw.Close()
}

func (c *conn) abortCause() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.aborted
}

func (c *conn) isAborted() bool { return c.abortCause() != nil }

// readErr classifies a failure while streaming the body.
func (c *conn) readErr(err error) error {
	switch cause := c.abortCause(); {
	case errors.Is(cause, context.DeadlineExceeded):
		return errs.Wrapf(errs.KindUpstreamTimeout, errors.Join(cause, err), "sftp read")
	case errors.Is(cause, context.Canceled):
		return errs.Wrapf(errs.KindCanceled, errors.Join(cause, err), "sftp read")
	default:
		return classifyErr(err, "sftp read")
	}
}

func closeConn(c *conn) error {
	var problems []error
	if c.sftp != nil {
		problems = append(problems, c.sftp.Close())
	}
	if c.ssh != nil {
		problems = append(problems, c.ssh.Close())
	}
	c.abort(net.ErrClosed)
	return errors.Join(problems...)
}

// isTransport reports whether err is a connection-level failure rather than
// an SFTP status reply; after one the connection's state is unknown.
func isTransport(err error) bool {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		return false
	}
	_, ok := errors.AsType[*sftp.StatusError](err)
	return !ok
}

// classify maps an sftp failure to an errs kind; an ended ctx wins, since
// its AfterFunc closing the connection is what made the operation fail.
func classify(ctx context.Context, err error, what string) error {
	if ctx.Err() != nil {
		cause := context.Cause(ctx)
		if errors.Is(cause, context.DeadlineExceeded) {
			return errs.Wrapf(errs.KindUpstreamTimeout, errors.Join(cause, err), "%s", what)
		}
		return errs.Wrapf(errs.KindCanceled, errors.Join(cause, err), "%s", what)
	}
	return classifyErr(err, what)
}

// classifyErr maps an ssh/sftp error to an errs kind.
func classifyErr(err error, what string) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return errs.Wrapf(errs.KindNotFound, err, "%s", what)
	case errors.Is(err, fs.ErrPermission):
		return errs.Wrapf(errs.KindForbidden, err, "%s", what)
	case errors.Is(err, ErrHostKeyMismatch):
		return errs.Wrapf(errs.KindUpstreamError, err, "%s", what)
	case strings.Contains(err.Error(), "ssh: unable to authenticate"):
		// x/crypto/ssh has no typed client-side authentication error.
		return errs.Wrapf(errs.KindForbidden, err, "%s", what)
	}
	if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
		return errs.Wrapf(errs.KindUpstreamTimeout, err, "%s", what)
	}
	return errs.Wrapf(errs.KindUpstreamError, err, "%s", what)
}
