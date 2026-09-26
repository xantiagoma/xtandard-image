package ssrf

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

func TestLimitBody(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		body    string
		length  int64 // Content-Length; -1 unknown
		limit   int64
		early   bool // LimitBody itself fails
		readErr bool
		want    string
	}{
		{"under", "abc", -1, 5, false, false, "abc"},
		{"exactly", "abcde", -1, 5, false, false, "abcde"},
		{"over by one", "abcdef", -1, 5, false, true, "abcde"},
		{"far over", strings.Repeat("x", 1<<16), -1, 10, false, true, strings.Repeat("x", 10)},
		{"declared over", "abcdef", 6, 5, true, false, ""},
		{"declared ok", "abc", 3, 5, false, false, "abc"},
		{"lying content-length", "abcdef", 3, 5, false, true, "abcde"},
		{"no limit", "abcdef", -1, 0, false, false, "abcdef"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(tc.body)), ContentLength: tc.length}
			err := LimitBody(resp, tc.limit)
			if tc.early {
				wantKind(t, err, errs.KindSourceTooLarge, ErrBodyTooLarge)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(resp.Body)
			if tc.readErr {
				wantKind(t, err, errs.KindSourceTooLarge, ErrBodyTooLarge)
				// Sticky: further reads keep failing.
				if n, err2 := resp.Body.Read(make([]byte, 8)); n != 0 || err2 == nil {
					t.Errorf("read after limit = %d, %v", n, err2)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("read %d bytes, want %d", len(got), len(tc.want))
			}
			if err := resp.Body.Close(); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestLimitBodyOverHTTP streams a chunked body larger than the cap through
// the SSRF client.
func TestLimitBodyOverHTTP(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		for range n {
			_, _ = io.WriteString(w, strings.Repeat("z", 1024))
			_ = http.NewResponseController(w).Flush() // forces chunked encoding
		}
	}))
	t.Cleanup(srv.Close)
	c := New(&Options{Policy: loopbackPolicy(t, srv)})

	for _, tc := range []struct {
		n       int
		wantErr bool
	}{{4, false}, {5, true}} {
		resp, err := get(t.Context(), t, c, srv.URL+"/?n="+strconv.Itoa(tc.n), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := LimitBody(resp, 4096); err != nil {
			_ = resp.Body.Close()
			t.Fatal(err)
		}
		_, err = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if tc.wantErr {
			wantKind(t, err, errs.KindSourceTooLarge, ErrBodyTooLarge)
		} else if err != nil {
			t.Errorf("n=%d: %v", tc.n, err)
		}
	}
}
