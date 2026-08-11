package handlers

import (
	"context"
	"errors"
	"strings"
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

// TestRunBatchPropagatesASelfReportedFailure covers a batch summary that called a
// refused operation a success.
//
// br.OK used to mean only "the call did not error", so an operation refused by its own
// connector — a git push blocked by policy — came back as
// {ok: true, result: {ok: false, …}}. The reader's eye lands on the outer flag and on
// ok_count, both of which said the batch was clean, while the refusal sat nested one
// level down. A caller scanning a 20-call batch had no reason to open each result.
func TestRunBatchPropagatesASelfReportedFailure(t *testing.T) {
	const denied = `{"ok":false,"exit_code":-1,"policy":{"verdict":"deny",` +
		`"reason":"branch \"main\" is protected; direct push is blocked"}}`

	res := runBatch(context.Background(),
		[]batchCall{{ToolID: "git.push"}}, 0,
		func(context.Context, batchCall) (string, error) { return denied, nil })

	if res[0].OK {
		t.Error("an operation that reported ok=false must not be counted as a successful call")
	}
	// The summary has to say WHY, or the caller is back to opening every result.
	if !strings.Contains(res[0].Error, "refused by policy") {
		t.Errorf("error = %q, want it to name the policy refusal", res[0].Error)
	}
	if !strings.Contains(res[0].Error, "is protected") {
		t.Errorf("error = %q, want the connector's own reason", res[0].Error)
	}
	// The full payload stays available: this reclassifies, it does not discard.
	if !strings.Contains(string(res[0].Result), "exit_code") {
		t.Errorf("the original result must still be returned, got %s", res[0].Result)
	}
}

// TestRunBatchLeavesOrdinaryResponsesAlone is the other half. Inferring failure too
// eagerly would turn ordinary payloads into reported errors, which is worse than the
// under-reporting it replaces.
func TestRunBatchLeavesOrdinaryResponsesAlone(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		// The success case, with a policy block that ALLOWED it.
		{"ok true", `{"ok":true,"policy":{"verdict":"allow"}}`},
		// No "ok" field at all: most connectors return a plain payload, and absence is
		// not failure.
		{"no ok field", `{"branches":["main","dev"]}`},
		// A false nested somewhere unrelated must not be read as the operation's verdict.
		{"false deeper in the payload", `{"items":[{"ok":false}]}`},
		// Not an object, and not JSON: nothing can be concluded either way.
		{"a JSON array", `[1,2,3]`},
		{"plain text", `some output`},
		{"empty", ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := runBatch(context.Background(),
				[]batchCall{{ToolID: "x.y"}}, 0,
				func(context.Context, batchCall) (string, error) { return tc.body, nil })
			if !res[0].OK {
				t.Errorf("must stay OK, got error %q for body %s", res[0].Error, tc.body)
			}
		})
	}
}

// TestSelfReportedFailurePicksTheMostUsefulReason checks the fallback order. A caller
// reading only the summary should get the most specific explanation available.
func TestSelfReportedFailurePicksTheMostUsefulReason(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{
			"policy denial wins",
			`{"ok":false,"policy":{"verdict":"deny","reason":"blocked"},"stderr":"noise"}`,
			"refused by policy: blocked",
		},
		{
			"then an explicit error",
			`{"ok":false,"error":"bad input","stderr":"noise"}`,
			"bad input",
		},
		{
			// git's own message, trimmed to one line: the rest is still in Result.
			"then the first line of stderr",
			`{"ok":false,"stderr":"fatal: not a repository\nmore detail"}`,
			"fatal: not a repository",
		},
		{
			// An allow verdict is not a reason, so it must not be used as one.
			"an allowed verdict is not a reason",
			`{"ok":false,"policy":{"verdict":"allow"}}`,
			"the operation reported ok=false",
		},
		{
			"nothing to go on",
			`{"ok":false}`,
			"the operation reported ok=false",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := selfReportedFailure(tc.body); got != tc.want {
				t.Errorf("selfReportedFailure() = %q, want %q", got, tc.want)
			}
		})
	}
}
