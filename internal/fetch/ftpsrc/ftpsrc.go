// Package ftpsrc is the ftp source backend (docs/ARCHITECTURE.md §6): a
// small bounded pool of logged-in control connections (github.com/jlaffaye/ftp),
// optional explicit TLS (AUTH TLS, RFC 4217) for control and data channels,
// passive mode only (EPSV, falling back to PASV; the data connection always
// goes to the control connection's address, never to the address a PASV
// reply names), SIZE and MDTM before a streaming RETR.
//
// Cancelling the fetch context closes the control and data connections, so
// a stuck server can never pin a goroutine past the request.
package ftpsrc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jlaffaye/ftp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch/internal/connpool"
	"github.com/xantiagoma/xtandard-image/internal/fetch/internal/filemeta"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// Defaults applied by New to zero Config fields.
const (
	DefaultPort        = 21
	DefaultPoolSize    = 4
	DefaultDialTimeout = 10 * time.Second
	DefaultIdleTimeout = time.Minute
)

// quitTimeout bounds the QUIT sent when a pooled connection is closed.
const quitTimeout = time.Second

// ErrInvalidConfig reports a Config New rejects.
var ErrInvalidConfig = errors.New("ftpsrc: invalid config")

// errLoginRefused marks a login the server refused with a reply the ftp
// library reports as bare text (e.g. "530 Non-anonymous sessions must use
// encryption." in answer to USER).
var errLoginRefused = errors.New("ftpsrc: login refused")

// errClosedEarly is the abort cause when the body is closed before EOF.
var errClosedEarly = errors.New("ftpsrc: body closed before EOF")

// Config is one ftp source (docs/CONFIG.md XI_SOURCES_<NAME>_*).
type Config struct {
	Name string // source name
	Host string // host name or IP literal, without port
	Port int    // 0 = 21
	// User and Password log in; an empty User logs in as "anonymous".
	User     string
	Password string
	// TLS upgrades the control connection with AUTH TLS and protects data
	// connections (PBSZ 0, PROT P). Implicit FTPS (port 990) is not
	// supported.
	TLS bool
	// TLSConfig overrides the TLS client settings (for example a private CA
	// pool). It is cloned; ServerName defaults to Host and MinVersion to TLS
	// 1.2. Nil means the system roots.
	TLSConfig *tls.Config
	// Root is the server directory keys are relative to; "" means "/".
	Root string
	// PoolSize bounds concurrent connections to this server (default 4).
	PoolSize int
	// DialTimeout bounds connect, TLS and login (default 10s); the fetch
	// context's deadline applies too.
	DialTimeout time.Duration
	// IdleTimeout closes pooled connections idle for longer (default 1m).
	IdleTimeout time.Duration
}

// Source serves one ftp server. It is safe for concurrent use.
type Source struct {
	name     string
	addr     string
	root     string
	user     string
	password string
	tls      *tls.Config
	dialer   net.Dialer
	pool     *connpool.Pool[*conn]
}

var _ source.Source = (*Source)(nil)

// New validates cfg and returns a Source. No connection is opened until the
// first fetch.
func New(cfg *Config) (*Source, error) {
	var problems []error
	if cfg.Name == "" {
		problems = append(problems, fmt.Errorf("%w: empty name", ErrInvalidConfig))
	}
	if cfg.Host == "" || strings.ContainsAny(cfg.Host, "/ \t\r\n") {
		problems = append(problems, fmt.Errorf("%w: source %q: bad host %q", ErrInvalidConfig, cfg.Name, cfg.Host))
	}
	if cfg.Port < 0 || cfg.Port > 65535 {
		problems = append(problems, fmt.Errorf("%w: source %q: bad port %d", ErrInvalidConfig, cfg.Name, cfg.Port))
	}
	if cfg.PoolSize < 0 || cfg.DialTimeout < 0 || cfg.IdleTimeout < 0 {
		problems = append(problems, fmt.Errorf("%w: source %q: negative pool size or timeout", ErrInvalidConfig, cfg.Name))
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}
	port := cfg.Port
	if port == 0 {
		port = DefaultPort
	}
	s := &Source{
		name:     cfg.Name,
		addr:     net.JoinHostPort(cfg.Host, strconv.Itoa(port)),
		root:     cfg.Root,
		user:     cfg.User,
		password: cfg.Password,
		dialer:   net.Dialer{Timeout: cmpOr(cfg.DialTimeout, DefaultDialTimeout)},
	}
	if s.user == "" {
		s.user, s.password = "anonymous", "anonymous"
	}
	if cfg.TLS {
		tc := &tls.Config{MinVersion: tls.VersionTLS12}
		if cfg.TLSConfig != nil {
			tc = cfg.TLSConfig.Clone()
			tc.MinVersion = max(tc.MinVersion, tls.VersionTLS12)
		}
		if tc.ServerName == "" {
			tc.ServerName = cfg.Host
		}
		if tc.ClientSessionCache == nil {
			// Data connections resume the control session; servers such as
			// vsftpd (require_ssl_reuse) refuse them otherwise.
			tc.ClientSessionCache = tls.NewLRUClientSessionCache(0)
		}
		s.tls = tc
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

// Name implements source.Source.
func (s *Source) Name() string { return s.name }

// Type implements source.Source.
func (s *Source) Type() string { return source.TypeFTP }

// Close closes the idle connections; leased ones close when their body does.
func (s *Source) Close() error { return s.pool.Close() }

// Fetch implements source.Source. The object's Size comes from SIZE and its
// LastModified from MDTM when the server supports them; ETag is the weak
// size+mtime validator of filemeta.
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
		return nil, false, classify(ctx, err, "ftp connect")
	}
	c := lease.Conn
	stop := context.AfterFunc(ctx, func() { c.abort(context.Cause(ctx)) })
	dl, _ := ctx.Deadline()
	c.setDeadline(dl)

	// finish returns the connection to the pool unless it is unusable.
	finish := func(opErr error) {
		aborted := !stop() || c.isAborted()
		if aborted || (opErr != nil && isTransport(opErr)) {
			lease.Discard()
			return
		}
		c.setDeadline(time.Time{})
		lease.Release()
	}
	fail := func(opErr error, what string) (*source.Object, bool, error) {
		finish(opErr)
		retry := lease.Reused && isTransport(opErr) && ctx.Err() == nil
		return nil, retry, classify(ctx, opErr, what)
	}

	size, err := c.sc.FileSize(p)
	switch {
	case err == nil:
	case unsupported(err):
		size = -1
	default:
		return fail(err, "ftp SIZE")
	}
	var mtime time.Time
	if c.sc.IsGetTimeSupported() {
		switch t, err := c.sc.GetTime(p); {
		case err == nil:
			mtime = t
		case isTransport(err):
			return fail(err, "ftp MDTM")
		default: // MDTM refused for this file: carry on without it.
		}
	}
	meta := &source.Object{Size: size, ETag: filemeta.ETag(size, mtime), LastModified: mtime}
	if filemeta.NotModified(opt.IfNoneMatch, opt.IfModifiedSince, meta.ETag, mtime) {
		finish(nil)
		meta.NotModified = true
		return meta, false, nil
	}
	if opt.MaxBytes > 0 && size > opt.MaxBytes {
		finish(nil)
		return nil, false, errs.Newf(errs.KindSourceTooLarge, "ftp object is %d bytes, limit %d", size, opt.MaxBytes)
	}
	resp, err := c.sc.Retr(p)
	if err != nil {
		return fail(err, "ftp RETR")
	}
	meta.Body = &body{resp: resp, c: c, size: size, finish: finish}
	return meta, false, nil
}

// body streams one RETR. Reaching EOF reads the transfer-complete reply, so
// a transfer the server reports as failed (or a short one) is an error, not
// a truncated image.
type body struct {
	resp   *ftp.Response
	c      *conn
	size   int64
	n      int64
	finish func(error)

	once sync.Once
	err  error // outcome of complete, sticky
}

func (b *body) Read(p []byte) (int, error) {
	n, err := b.resp.Read(p)
	b.n += int64(n)
	switch {
	case err == nil:
		return n, nil
	case errors.Is(err, io.EOF):
		if cerr := b.complete(true); cerr != nil {
			return n, cerr
		}
		return n, io.EOF
	default:
		cerr := b.c.readErr(err)
		b.once.Do(func() {
			b.c.abort(err)
			_ = b.resp.Close()
			b.finish(err)
			b.err = cerr
		})
		return n, cerr
	}
}

// complete closes the data connection, reads the server's final reply and
// hands the connection back (or drops it when anything went wrong).
func (b *body) complete(eof bool) error {
	b.once.Do(func() {
		if !eof {
			// Early close: drop the connection rather than wait for the
			// server's abort replies.
			b.c.abort(errClosedEarly)
		}
		err := b.resp.Close()
		switch {
		case err != nil && eof:
			b.err = b.c.readErr(err)
		case err == nil && eof && b.size >= 0 && b.n != b.size:
			err = fmt.Errorf("short transfer: %d of %d bytes", b.n, b.size)
			b.err = errs.Wrap(errs.KindUpstreamError, err)
		default:
		}
		if err != nil {
			// A reply other than 226 (e.g. 426 after an early close) may be
			// followed by another; never reuse such a connection.
			b.c.abort(err)
		}
		b.finish(err)
	})
	return b.err
}

// Close ends the transfer; closing before EOF aborts it and drops the
// connection instead of pooling it.
func (b *body) Close() error {
	_ = b.complete(false)
	return nil
}

// conn is one pooled control connection.
type conn struct {
	sc   *ftp.ServerConn
	ctrl net.Conn // raw TCP control connection (TLS, if any, is layered by ftp)

	mu       sync.Mutex
	data     net.Conn  // latest data connection
	deadline time.Time // applied to control and data connections
	aborted  error     // why abort closed the connection; nil while usable
	dialed   bool      // the control connection was handed to ftp.Dial
}

// dial opens and logs in one control connection, honouring ctx throughout.
func (s *Source) dial(ctx context.Context) (*conn, error) {
	raw, err := s.dialer.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return nil, err
	}
	c := &conn{ctrl: raw}
	stop := context.AfterFunc(ctx, func() { c.abort(context.Cause(ctx)) })
	dl := time.Now().Add(s.dialer.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(dl) {
		dl = d
	}
	c.setDeadline(dl)
	opts := []ftp.DialOption{ftp.DialWithDialFunc(c.dialFunc(&s.dialer, s.tls))}
	if s.tls != nil {
		opts = append(opts, ftp.DialWithExplicitTLS(s.tls))
	}
	c.sc, err = ftp.Dial(s.addr, opts...)
	if err == nil {
		err = loginErr(c.sc.Login(s.user, s.password))
	}
	aborted := !stop()
	if err == nil && aborted {
		err = context.Cause(ctx)
	}
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	c.setDeadline(time.Time{})
	return c, nil
}

// dialFunc hands ftp.Dial the pre-dialled control connection first, then
// dials data connections to the address the library computed (the control
// connection's IP, never one from a PASV reply), layering TLS when the
// control channel uses it.
func (c *conn) dialFunc(d *net.Dialer, tc *tls.Config) func(network, address string) (net.Conn, error) {
	return func(network, address string) (net.Conn, error) {
		c.mu.Lock()
		if !c.dialed {
			c.dialed = true
			c.mu.Unlock()
			return c.ctrl, nil
		}
		aborted := c.aborted != nil
		c.mu.Unlock()
		if aborted {
			return nil, net.ErrClosed
		}
		// The ftp library's dial hook has no context: abort closes the
		// connection and the dialer has a timeout.
		nc, err := d.Dial(network, address)
		if err != nil {
			return nil, err
		}
		if tc != nil {
			nc = tls.Client(nc, tc)
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.aborted != nil {
			_ = nc.Close()
			return nil, net.ErrClosed
		}
		if !c.deadline.IsZero() {
			_ = nc.SetDeadline(c.deadline)
		}
		c.data = nc
		return nc, nil
	}
}

func (c *conn) setDeadline(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadline = t
	_ = c.ctrl.SetDeadline(t)
	if c.data != nil {
		_ = c.data.SetDeadline(t)
	}
}

// abort closes the control and data connections, unblocking any I/O; cause
// is what readErr reports afterwards.
func (c *conn) abort(cause error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.aborted == nil {
		c.aborted = cmpOr(cause, net.ErrClosed)
	}
	_ = c.ctrl.Close()
	if c.data != nil {
		_ = c.data.Close()
	}
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
		return errs.Wrapf(errs.KindUpstreamTimeout, errors.Join(cause, err), "ftp read")
	case errors.Is(cause, context.Canceled):
		return errs.Wrapf(errs.KindCanceled, errors.Join(cause, err), "ftp read")
	default:
		return classifyErr(err, "ftp read")
	}
}

func closeConn(c *conn) error {
	c.setDeadline(time.Now().Add(quitTimeout))
	err := c.sc.Quit()
	c.abort(net.ErrClosed)
	return err
}

// loginErr tags a Login failure that is neither an FTP reply with a code
// nor a transport or TLS failure: jlaffaye/ftp turns an unexpected USER
// reply into errors.New(text), losing the code.
func loginErr(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*textproto.Error](err); ok {
		return err
	}
	if _, ok := errors.AsType[net.Error](err); ok {
		return err
	}
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return err
	}
	if _, ok := errors.AsType[tls.AlertError](err); ok {
		return err
	}
	if _, ok := errors.AsType[tls.RecordHeaderError](err); ok {
		return err
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return err
	}
	return fmt.Errorf("%w: %w", errLoginRefused, err)
}

// isTransport reports whether err is a connection-level failure rather than
// an FTP reply; after one the connection's state is unknown.
func isTransport(err error) bool {
	if _, ok := errors.AsType[*textproto.Error](err); ok {
		return false
	}
	if _, ok := errors.AsType[*strconv.NumError](err); ok {
		return false
	}
	return !errors.Is(err, ftp.ErrInvalidCommand)
}

// unsupported reports a reply meaning "command not implemented".
func unsupported(err error) bool {
	if pe, ok := errors.AsType[*textproto.Error](err); ok {
		switch pe.Code {
		case ftp.StatusBadCommand, ftp.StatusNotImplemented, ftp.StatusNotImplementedParameter:
			return true
		default:
		}
	}
	_, ok := errors.AsType[*strconv.NumError](err)
	return ok
}

// classify maps an ftp failure to an errs kind; an ended ctx wins, since
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

// classifyErr maps an ftp reply or transport error to an errs kind.
func classifyErr(err error, what string) error {
	if pe, ok := errors.AsType[*textproto.Error](err); ok {
		switch pe.Code {
		case ftp.StatusFileUnavailable, ftp.StatusBadFileName:
			if strings.Contains(strings.ToLower(pe.Msg), "permission") {
				return errs.Wrapf(errs.KindForbidden, err, "%s", what)
			}
			return errs.Wrapf(errs.KindNotFound, err, "%s", what)
		case ftp.StatusNotLoggedIn, ftp.StatusInvalidCredentials, ftp.StatusLoginNeedAccount, ftp.StatusStorNeedAccount:
			return errs.Wrapf(errs.KindForbidden, err, "%s", what)
		default:
			return errs.Wrapf(errs.KindUpstreamError, err, "%s", what)
		}
	}
	if errors.Is(err, errLoginRefused) {
		return errs.Wrapf(errs.KindForbidden, err, "%s", what)
	}
	if errors.Is(err, ftp.ErrInvalidCommand) {
		return errs.Wrapf(errs.KindInvalidURL, err, "%s", what)
	}
	if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
		return errs.Wrapf(errs.KindUpstreamTimeout, err, "%s", what)
	}
	return errs.Wrapf(errs.KindUpstreamError, err, "%s", what)
}
