//go:build cgo

package exec_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec/encode"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/testutil/rawfixtures"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// admitRecorder is an exec.Config.Admit fake: it records every charge and
// its release, and fails when err is set.
type admitRecorder struct {
	mu       sync.Mutex
	charged  []int64
	released int
	err      error
}

func (a *admitRecorder) admit(_ context.Context, bytes int64) (func(), error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.charged = append(a.charged, bytes)
	if a.err != nil {
		return nil, a.err
	}
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.released++
	}, nil
}

func newAdmitExecutor(tb testing.TB, lim plan.Limits, a *admitRecorder) *exec.Executor {
	tb.Helper()
	ec := encode.DefaultConfig()
	enc, err := encode.New(&ec)
	if err != nil {
		tb.Fatal(err)
	}
	e, err := exec.New(&exec.Config{Limits: lim, Admit: a.admit}, enc)
	if err != nil {
		tb.Fatal(err)
	}
	return e
}

// TestAdmitChargesPlanCost checks that Run charges exactly the planner's
// estimate once per job (RAW sources after their in-Run plan) and releases
// it when Run returns.
func TestAdmitChargesPlanCost(t *testing.T) {
	t.Parallel()
	f := rawfixtures.Files()[4] // pentax-k-50.dng
	for _, tc := range []struct {
		name, options string
		data          []byte
		out           transform.Format
		raw           bool
	}{
		{name: "jpeg resize", options: "rs:fit:120:120", data: readFixture(t, jpegPhoto), out: transform.FormatJPEG},
		{name: "random access doubles", options: "rs:fit:120:120/rot:90/t:10", data: readFixture(t, jpegPhoto), out: transform.FormatPNG},
		{name: "chained", options: "rs:fit:200:200/-/rs:fit:50:50", data: readFixture(t, jpegPhoto), out: transform.FormatWebP},
		{name: "raw", options: "rs:fit:320:320", data: rawfixtures.Read(t, &f), out: transform.FormatJPEG, raw: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var a admitRecorder
			e := newAdmitExecutor(t, testLimits, &a)
			j, buf := job(t, tc.data, parse(t, tc.options), tc.out)
			defer buf.Release()
			res, err := e.Run(t.Context(), j)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Release()

			a.mu.Lock()
			defer a.mu.Unlock()
			if len(a.charged) != 1 || a.released != 1 {
				t.Fatalf("charged %v, released %d times; want one charge, released once", a.charged, a.released)
			}
			if a.charged[0] <= 0 {
				t.Fatalf("charged %d bytes, want the plan's positive estimate", a.charged[0])
			}
			if tc.raw {
				return // planned inside Run from the LibRaw header
			}
			p, err := plan.Build(j.Request, j.Meta, testLimits)
			if err != nil {
				t.Fatal(err)
			}
			if a.charged[0] != p.Cost.EstimatedBytes {
				t.Errorf("charged %d bytes, plan estimate %d", a.charged[0], p.Cost.EstimatedBytes)
			}
		})
	}
}

// TestAdmitRejection checks that an Admit error ends Run before any decode
// and is returned unchanged.
func TestAdmitRejection(t *testing.T) {
	t.Parallel()
	rejected := errs.New(errs.KindUnavailable, "memory budget busy")
	a := admitRecorder{err: rejected}
	e := newAdmitExecutor(t, testLimits, &a)
	j, buf := job(t, readFixture(t, jpegPhoto), parse(t, "rs:fit:120:120"), transform.FormatJPEG)
	defer buf.Release()
	_, err := e.Run(t.Context(), j)
	if !errors.Is(err, rejected) {
		t.Fatalf("err = %v, want the Admit error", err)
	}
	if len(a.charged) != 1 || a.released != 0 {
		t.Errorf("charged %v, released %d; want one attempt, nothing to release", a.charged, a.released)
	}
}
