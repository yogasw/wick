package handlers

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func bc(id string) batchCall { return batchCall{ToolID: id} }

// runBatch must return per-call outcomes in input order: success carries a
// result, failure carries an error, and neither stops the others.
func TestRunBatch_PartialSuccess(t *testing.T) {
	calls := []batchCall{bc("conn:a/op"), bc("conn:b/op"), bc("conn:c/op")}
	exec := func(_ context.Context, c batchCall) (string, error) {
		if c.ToolID == "conn:b/op" {
			return "", errors.New("boom")
		}
		return `{"ok":1}`, nil
	}
	res := runBatch(context.Background(), calls, 0, exec)

	if len(res) != 3 {
		t.Fatalf("want 3 results, got %d", len(res))
	}
	for i, r := range res {
		if r.Index != i {
			t.Errorf("result %d has Index %d (order not preserved)", i, r.Index)
		}
	}
	if !res[0].OK || string(res[0].Result) != `{"ok":1}` {
		t.Errorf("call 0 should succeed: %+v", res[0])
	}
	if res[1].OK || res[1].Error != "boom" || res[1].TimedOut {
		t.Errorf("call 1 should be a plain failure: %+v", res[1])
	}
	if !res[2].OK {
		t.Errorf("call 2 should succeed despite call 1 failing: %+v", res[2])
	}
}

// A call that outlives its per-call timeout is marked timed_out, others still
// complete (partial response).
func TestRunBatch_Timeout(t *testing.T) {
	calls := []batchCall{bc("fast"), bc("slow")}
	exec := func(ctx context.Context, c batchCall) (string, error) {
		if c.ToolID == "slow" {
			select {
			case <-time.After(2 * time.Second):
				return `{"late":true}`, nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return `{"fast":true}`, nil
	}
	res := runBatch(context.Background(), calls, 50*time.Millisecond, exec)

	if !res[0].OK {
		t.Errorf("fast call should succeed: %+v", res[0])
	}
	if res[1].OK || !res[1].TimedOut {
		t.Errorf("slow call should be timed_out: %+v", res[1])
	}
}

// A panicking call is contained — it becomes an error result, siblings run.
func TestRunBatch_PanicContained(t *testing.T) {
	calls := []batchCall{bc("ok"), bc("panic")}
	exec := func(_ context.Context, c batchCall) (string, error) {
		if c.ToolID == "panic" {
			panic("kaboom")
		}
		return `{}`, nil
	}
	res := runBatch(context.Background(), calls, 0, exec)
	if !res[0].OK {
		t.Errorf("ok call should succeed: %+v", res[0])
	}
	if res[1].OK || res[1].Error == "" {
		t.Errorf("panic call should be an error result: %+v", res[1])
	}
}

// Concurrency is fixed server-side (batchConcurrency) — never more than that
// many calls in flight at once, regardless of batch size.
func TestRunBatch_RespectsFixedConcurrency(t *testing.T) {
	const n = 20
	calls := make([]batchCall, n)
	for i := range calls {
		calls[i] = bc("c")
	}
	var inflight, peak int32
	exec := func(_ context.Context, _ batchCall) (string, error) {
		cur := atomic.AddInt32(&inflight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if cur <= p || atomic.CompareAndSwapInt32(&peak, p, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&inflight, -1)
		return `{}`, nil
	}
	runBatch(context.Background(), calls, 0, exec)
	if peak > batchConcurrency {
		t.Errorf("peak concurrency %d exceeded fixed limit %d", peak, batchConcurrency)
	}
	if peak < 2 {
		t.Errorf("expected some parallelism, peak was %d", peak)
	}
}
