package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// pipeListener is an in-memory net.Listener: dial hands it the server end
// of a net.Pipe.
type pipeListener struct {
	conns     chan net.Conn
	done      chan struct{}
	closeOnce sync.Once
	failNext  chan error
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), done: make(chan struct{}), failNext: make(chan error, 1)}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case err := <-l.failNext:
		return nil, err
	default:
	}
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

// dial offers a connection to Accept without waiting for it to be taken;
// the client end is closed with the test.
func (l *pipeListener) dial(t *testing.T) {
	t.Helper()
	server, client := net.Pipe()
	go func() {
		select {
		case l.conns <- server:
		case <-l.done:
			_ = server.Close()
		}
	}()
	t.Cleanup(func() { _ = client.Close() })
}

// acceptor runs Accept in a goroutine and reports each result.
type acceptResult struct {
	c   net.Conn
	err error
}

func acceptAsync(ln net.Listener) <-chan acceptResult {
	ch := make(chan acceptResult, 1)
	go func() {
		c, err := ln.Accept()
		ch <- acceptResult{c, err}
	}()
	return ch
}

func TestLimitListener(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		run  func(t *testing.T, inner *pipeListener, ln net.Listener)
	}{
		{"waits for a slot", func(t *testing.T, inner *pipeListener, ln net.Listener) {
			t.Helper()
			open := make([]net.Conn, 0, 2)
			for range 2 {
				inner.dial(t)
				r := <-acceptAsync(ln)
				if r.err != nil {
					t.Fatalf("Accept: %v", r.err)
				}
				open = append(open, r.c)
			}
			inner.dial(t)
			third := acceptAsync(ln)
			synctest.Wait()
			select {
			case r := <-third:
				t.Fatalf("third Accept returned %v, %v with two connections open", r.c, r.err)
			default:
			}
			// A double Close releases one slot, not two.
			_ = open[0].Close()
			_ = open[0].Close()
			r := <-third
			if r.err != nil {
				t.Fatalf("third Accept: %v", r.err)
			}
			inner.dial(t)
			fourth := acceptAsync(ln)
			synctest.Wait()
			select {
			case r := <-fourth:
				t.Fatalf("fourth Accept returned %v, %v: a double Close released two slots", r.c, r.err)
			default:
			}
			_ = r.c.Close()
			if r := <-fourth; r.err != nil {
				t.Fatalf("fourth Accept: %v", r.err)
			}
		}},
		{"close unblocks a waiting accept", func(t *testing.T, inner *pipeListener, ln net.Listener) {
			t.Helper()
			for range 2 {
				inner.dial(t)
				if r := <-acceptAsync(ln); r.err != nil {
					t.Fatalf("Accept: %v", r.err)
				}
			}
			waiting := acceptAsync(ln)
			synctest.Wait()
			if err := ln.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if r := <-waiting; !errors.Is(r.err, net.ErrClosed) {
				t.Fatalf("waiting Accept = %v, %v, want net.ErrClosed", r.c, r.err)
			}
		}},
		{"accept error releases the slot", func(t *testing.T, inner *pipeListener, ln net.Listener) {
			t.Helper()
			for range 2 {
				inner.failNext <- errors.New("accept failed")
				if r := <-acceptAsync(ln); r.err == nil {
					t.Fatalf("Accept = %v, want the inner error", r.c)
				}
			}
			for range 2 {
				inner.dial(t)
				if r := <-acceptAsync(ln); r.err != nil {
					t.Fatalf("Accept after errors: %v (slots leaked)", r.err)
				}
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				inner := newPipeListener()
				ln := LimitListener(t.Context(), inner, 2)
				t.Cleanup(func() { _ = ln.Close() })
				tc.run(t, inner, ln)
			})
		})
	}
}

func TestLimitListenerUnlimited(t *testing.T) {
	t.Parallel()
	inner := newPipeListener()
	if ln := LimitListener(t.Context(), inner, 0); ln != net.Listener(inner) {
		t.Fatalf("LimitListener(0) = %T, want the listener unchanged", ln)
	}
}

// TestLimitListenerHTTP serves HTTP/1.1 and h2c through a limited listener
// over TCP and shuts the server down gracefully: the wrapper is transparent
// to net/http (protocol detection, keep-alive, Shutdown closing the
// listener and its idle connections).
func TestLimitListenerHTTP(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		protocols func(*http.Protocols)
		wantMajor int
	}{
		{"http1", func(p *http.Protocols) { p.SetHTTP1(true) }, 1},
		{"h2c", func(p *http.Protocols) { p.SetUnencryptedHTTP2(true) }, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var lc net.ListenConfig
			tcp, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			ln := LimitListener(t.Context(), tcp, 1)
			var serverProtos http.Protocols
			serverProtos.SetHTTP1(true)
			serverProtos.SetUnencryptedHTTP2(true)
			srv := &http.Server{
				Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/plain")
					_, _ = io.WriteString(w, "ok")
				}),
				Protocols:         &serverProtos,
				ReadHeaderTimeout: 5 * time.Second,
			}
			served := make(chan error, 1)
			go func() { served <- srv.Serve(ln) }()

			var clientProtos http.Protocols
			tc.protocols(&clientProtos)
			client := &http.Client{Transport: &http.Transport{Protocols: &clientProtos}}
			// Several requests over one slot: the connection is reused.
			for range 3 {
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+tcp.Addr().String()+"/", http.NoBody)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatalf("GET: %v", err)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.ProtoMajor != tc.wantMajor {
					t.Fatalf("ProtoMajor = %d, want %d", resp.ProtoMajor, tc.wantMajor)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if err := srv.Shutdown(ctx); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
			if err := <-served; !errors.Is(err, http.ErrServerClosed) {
				t.Fatalf("Serve = %v, want http.ErrServerClosed", err)
			}
			client.CloseIdleConnections()
		})
	}
}
