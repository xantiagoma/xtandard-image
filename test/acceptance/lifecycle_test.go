//go:build acceptance

package acceptance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestOverloadAndDrain runs against app-tiny (one worker, no queue). Its
// steps are sequential: the drain step stops the container, so it runs last.
//
//nolint:tparallel // the subtests are ordered steps against one container
func TestOverloadAndDrain(t *testing.T) {
	t.Parallel()
	app := base(t, "XI_ACC_TINY")
	admin := base(t, "XI_ACC_TINY_ADMIN")
	container := env(t, "XI_ACC_TINY_CONTAINER")

	t.Run("429 under load", func(t *testing.T) { testOverload(t, app, admin) })
	t.Run("SIGTERM drains in-flight request", func(t *testing.T) { testDrain(t, app, admin, container) })
}

// testOverload fires concurrent expensive jobs at one worker without a
// queue: at least one must fail fast with 429 and Retry-After, at least one
// must succeed, and nothing else may happen (ARCHITECTURE §11).
func testOverload(t *testing.T, app, admin string) {
	t.Helper()
	const n = 32
	var (
		mu       sync.Mutex
		counts   = map[int]int{}
		retry    []string
		failures []error
		wg       sync.WaitGroup
	)
	for i := range n {
		wg.Go(func() {
			// Distinct cache busters: every request is its own job.
			url := fmt.Sprintf("%s/unsafe/rs:fit:1600:1600:1/q:90/cb:%d/plain/%s@avif", app, i, jpegSrc)
			r, err := fetch(t.Context(), http.MethodGet, url, nil)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures = append(failures, err)
				return
			}
			counts[r.Status]++
			if r.Status == http.StatusTooManyRequests {
				retry = append(retry, r.Header.Get("Retry-After"))
				if cc := r.Header.Get("Cache-Control"); cc != "no-store" {
					failures = append(failures, fmt.Errorf("429 Cache-Control %q, want no-store", cc))
				}
			}
		})
	}
	wg.Wait()
	if err := errors.Join(failures...); err != nil {
		t.Fatal(err)
	}
	t.Logf("statuses of %d concurrent requests: %v", n, counts)
	if counts[http.StatusTooManyRequests] == 0 || counts[http.StatusOK] == 0 {
		t.Fatalf("statuses %v, want both 200 and 429", counts)
	}
	if counts[http.StatusOK]+counts[http.StatusTooManyRequests] != n {
		t.Errorf("statuses %v, want only 200 and 429", counts)
	}
	for _, ra := range retry {
		if s, err := strconv.Atoi(ra); err != nil || s < 1 {
			t.Errorf("Retry-After %q, want seconds ≥ 1", ra)
		}
	}
	metrics := string(get(t, admin+"/metrics", nil).Body)
	if !hasMetric(metrics, `xi_overload_total{reason="queue_full"}`) {
		t.Error(`/metrics has no xi_overload_total{reason="queue_full"}`)
	}
}

// testDrain starts a request whose origin is throttled (~5 s), waits until
// the server counts it in flight, sends SIGTERM to the container and checks
// that readiness turns 503, the request still completes with a valid
// image, and the process exits 0 (ARCHITECTURE §11, go-standards §2).
func testDrain(t *testing.T, app, admin, container string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	// A fresh cache buster so the origin cache cannot answer quickly.
	slow := fmt.Sprintf("%s/unsafe/rs:fit:64:64/cb:%d/plain/http://fixtures/slow/jpeg/baseline.jpg@png", app, time.Now().UnixNano())
	type result struct {
		r   *response
		err error
	}
	done := make(chan result, 1)
	go func() {
		r, err := fetch(ctx, http.MethodGet, slow, nil)
		done <- result{r, err}
	}()

	if err := poll(ctx, 50*time.Millisecond, func() (bool, error) {
		r, err := fetch(ctx, http.MethodGet, admin+"/metrics", nil)
		if err != nil {
			return false, err
		}
		return metricValue(string(r.Body), "xi_requests_in_flight") >= 1, nil
	}); err != nil {
		t.Fatalf("slow request never in flight: %v", err)
	}
	select {
	case res := <-done:
		t.Fatalf("slow request finished before SIGTERM (%+v); is /slow/ throttled?", res)
	default:
	}

	start := time.Now()
	if out, err := docker(ctx, "kill", "--signal", "TERM", container); err != nil {
		t.Fatalf("docker kill: %v: %s", err, out)
	}

	// Readiness is 503 during XI_SERVER_SHUTDOWN_DELAY (2 s), before the
	// listener closes.
	sawDraining := false
	_ = poll(ctx, 50*time.Millisecond, func() (bool, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, app+"/readyz", http.NoBody)
		if err != nil {
			return false, err
		}
		res, err := client.Do(req)
		if err != nil {
			return true, nil //nolint:nilerr // listener closed: the delay is over
		}
		_ = res.Body.Close()
		sawDraining = res.StatusCode == http.StatusServiceUnavailable
		return sawDraining, nil
	})
	if !sawDraining {
		t.Error("/readyz never answered 503 while draining")
	}

	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		t.Fatal("in-flight request did not complete")
	}
	if res.err != nil {
		t.Fatalf("in-flight request: %v", res.err)
	}
	r := res.r
	r.wantStatus(t, http.StatusOK)
	if p, err := decodeProps(r.Body); err != nil || p != (props{Format: "png", Width: 64, Height: 48}) {
		t.Errorf("drained response %+v, %v", p, err)
	}

	out, err := docker(ctx, "wait", container)
	if err != nil {
		t.Fatalf("docker wait: %v: %s", err, out)
	}
	if code := strings.TrimSpace(out); code != "0" {
		t.Errorf("exit code %s after SIGTERM, want 0", code)
	}
	t.Logf("drained and exited %s after SIGTERM", time.Since(start).Round(100*time.Millisecond))
}

// docker runs the docker CLI (the suite drives the container lifecycle
// like an orchestrator would).
func docker(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	return string(out), err
}

// poll calls f every interval until it reports done, fails, or ctx ends.
func poll(ctx context.Context, interval time.Duration, f func() (bool, error)) error {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		ok, err := f()
		if err != nil || ok {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.New("timed out")
		case <-tick.C:
		}
	}
}

// metricValue returns the value of an unlabelled sample, or -1.
func metricValue(body, name string) float64 {
	for line := range strings.Lines(body) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), name+" "); ok {
			f, err := strconv.ParseFloat(v, 64)
			if err == nil {
				return f
			}
		}
	}
	return -1
}
