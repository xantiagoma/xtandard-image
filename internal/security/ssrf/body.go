package ssrf

import (
	"io"
	"net/http"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// LimitBody caps resp.Body at limit bytes, like http.MaxBytesReader for
// responses. A declared Content-Length above limit fails immediately; the
// caller still owns and must close resp.Body. Otherwise resp.Body is
// replaced by a reader that returns a KindSourceTooLarge error wrapping
// ErrBodyTooLarge once more than limit bytes arrive. A non-positive limit
// is a no-op.
func LimitBody(resp *http.Response, limit int64) error {
	if limit <= 0 || resp.Body == nil {
		return nil
	}
	if resp.ContentLength > limit {
		return tooLarge(limit)
	}
	resp.Body = &limitedBody{rc: resp.Body, n: limit, limit: limit}
	return nil
}

func tooLarge(limit int64) error {
	return errs.Wrapf(errs.KindSourceTooLarge, ErrBodyTooLarge, "body over %d bytes", limit)
}

type limitedBody struct {
	rc    io.ReadCloser
	n     int64 // bytes still allowed
	limit int64
	err   error // sticky once the limit is exceeded
}

func (l *limitedBody) Read(p []byte) (int, error) {
	if l.err != nil {
		return 0, l.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	// Read one byte past the budget to tell "exactly limit" from "over".
	if int64(len(p)) > l.n+1 {
		p = p[:l.n+1]
	}
	n, err := l.rc.Read(p)
	if int64(n) <= l.n {
		l.n -= int64(n)
		return n, err
	}
	n = int(l.n)
	l.n = 0
	l.err = tooLarge(l.limit)
	return n, l.err
}

func (l *limitedBody) Close() error { return l.rc.Close() }
