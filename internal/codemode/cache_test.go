package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestParseCacheOpt(t *testing.T) {
	cases := map[string]CacheOpt{
		``:                    {},
		`null`:                {},
		`false`:               {},
		`true`:                {OK: time.Minute},
		`300`:                 {OK: 300 * time.Second},
		`-5`:                  {},
		`99999`:               {OK: CacheMaxTTL},
		`{"ok":30,"error":5}`: {OK: 30 * time.Second, Error: 5 * time.Second},
		`{"ttl":30}`:          {OK: 30 * time.Second},
		`{"error":99999}`:     {Error: CacheMaxErrorTTL},
		`"soon"`:              {},
		`{"ok":"x"}`:          {},
	}
	for in, want := range cases {
		if got := ParseCacheOpt(json.RawMessage(in)); got != want {
			t.Errorf("ParseCacheOpt(%s) = %+v, want %+v", in, got, want)
		}
	}
}

func TestCacheableError(t *testing.T) {
	for _, msg := range []string{"not_found", "channel_not_found", "forbidden: no access", "param id is required"} {
		if !cacheableError(msg) {
			t.Errorf("%q should be cacheable", msg)
		}
	}
	for _, msg := range []string{"", "timed out after 60s", "context deadline exceeded", "ratelimited: rate limit hit", "HTTP 503 Service Unavailable", "status 502", "dial tcp: connection refused", "unexpected EOF", "waiting for approval", "the run ended while this call was running"} {
		if cacheableError(msg) {
			t.Errorf("%q should not be cacheable", msg)
		}
	}
}

func TestCallCacheExpiresAndEvicts(t *testing.T) {
	c := newCallCache()
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	c.put("a", true, "1", time.Minute)
	if e, ok := c.get("a"); !ok || e.result != "1" {
		t.Fatalf("get a = %+v %v", e, ok)
	}
	now = now.Add(time.Minute)
	if _, ok := c.get("a"); ok {
		t.Fatal("entry outlived its ttl")
	}
	for i := range cacheMaxEntries + 10 {
		c.put(uuid.NewString()+string(rune('a'+i%26)), true, "x", time.Hour)
	}
	if c.ll.Len() != cacheMaxEntries || len(c.m) != cacheMaxEntries {
		t.Fatalf("len = %d/%d, want %d", c.ll.Len(), len(c.m), cacheMaxEntries)
	}
	c.put("big", true, strings.Repeat("x", cacheMaxEntryBytes+1), time.Hour)
	if _, ok := c.get("big"); ok {
		t.Fatal("oversized answer was kept")
	}
}

// countingHost counts the connector calls that really ran.
func countingHost(t *testing.T, f *fakeRunner, n *atomic.Int32) Host {
	h := hostFor(t, f)
	exec := h.Exec
	h.Exec = func(ctx context.Context, toolID string, params map[string]any) (string, error) {
		n.Add(1)
		if strings.HasSuffix(toolID, "/missing") {
			return "", errors.New("not_found")
		}
		if strings.HasSuffix(toolID, "/flaky") {
			return "", errors.New("HTTP 503 from upstream")
		}
		return exec(ctx, toolID, params)
	}
	return h
}

func TestRunCachesReadCalls(t *testing.T) {
	var n atomic.Int32
	h := countingHost(t, &fakeRunner{}, &n)
	scope := "u:" + uuid.NewString()
	code := "CCALL conn:a/list 60\nCCALL conn:a/list 60\nCALL conn:a/get"
	res, err := h.Run(context.Background(), Request{Code: code, CacheScope: scope})
	if err != nil || !res.OK {
		t.Fatalf("run: %v %+v", err, res)
	}
	if n.Load() != 2 {
		t.Fatalf("connector ran %d times, want 2 (second list cached)", n.Load())
	}
	cached := 0
	for _, c := range res.Calls {
		if c.Cached {
			cached++
		}
	}
	if cached != 1 {
		t.Fatalf("cached calls = %d, want 1: %+v", cached, res.Calls)
	}
	if !strings.Contains(string(res.Result), `{\"op\":\"list\"},{\"op\":\"list\"}`) {
		t.Fatalf("cached answer differs: %s", res.Result)
	}

	// Another caller never sees this caller's entry.
	n.Store(0)
	if _, err := h.Run(context.Background(), Request{Code: "CCALL conn:a/list 60", CacheScope: scope + "-other"}); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 1 {
		t.Fatalf("other scope hit the cache (ran %d)", n.Load())
	}

	// No scope = no cache.
	n.Store(0)
	if _, err := h.Run(context.Background(), Request{Code: "CCALL conn:a/list 60\nCCALL conn:a/list 60"}); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 2 {
		t.Fatalf("unscoped run cached (ran %d)", n.Load())
	}
}

func TestRunNeverCachesWrites(t *testing.T) {
	var n atomic.Int32
	h := countingHost(t, &fakeRunner{}, &n)
	scope := "u:" + uuid.NewString()
	if _, err := h.Run(context.Background(), Request{Code: "CCALL conn:a/send 60\nCCALL conn:a/send 60", CacheScope: scope}); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 2 {
		t.Fatalf("destructive op cached (ran %d)", n.Load())
	}
}

func TestRunCachesOnlyAnswerFailures(t *testing.T) {
	var n atomic.Int32
	h := countingHost(t, &fakeRunner{}, &n)
	scope := "u:" + uuid.NewString()
	code := `CCALL conn:a/missing {"error":60}
CCALL conn:a/missing {"error":60}
CCALL conn:a/flaky {"error":60}
CCALL conn:a/flaky {"error":60}
CCALL conn:a/fail 60
CCALL conn:a/fail 60`
	res, err := h.Run(context.Background(), Request{Code: code, CacheScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	// missing: 1 (second cached); flaky: 2 (5xx never kept); fail: 2 (no error ttl).
	if n.Load() != 5 {
		t.Fatalf("connector ran %d times, want 5", n.Load())
	}
	if !strings.Contains(string(res.Result), "ERR not_found,ERR not_found") {
		t.Fatalf("cached failure not replayed: %s", res.Result)
	}
}

func TestCacheHitRechecksAccess(t *testing.T) {
	var n atomic.Int32
	h := countingHost(t, &fakeRunner{}, &n)
	scope := "u:" + uuid.NewString()
	if _, err := h.Run(context.Background(), Request{Code: "CCALL conn:a/list 60", CacheScope: scope}); err != nil {
		t.Fatal(err)
	}
	// The run's pre-flight passes; access is gone by the time the call
	// comes, so the hit's own check must send it to the connector.
	var checks atomic.Int32
	h.Check = func(context.Context, string) error {
		if checks.Add(1) > 1 {
			return errors.New("not found or not accessible")
		}
		return nil
	}
	n.Store(0)
	h.Exec = func(context.Context, string, map[string]any) (string, error) {
		n.Add(1)
		return "", errors.New("not found or not accessible")
	}
	res, err := h.Run(context.Background(), Request{Code: "CCALL conn:a/list 60", CacheScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if n.Load() != 1 || !strings.Contains(string(res.Result), "ERR not found") {
		t.Fatalf("cached answer served after access was revoked (ran %d): %s", n.Load(), res.Result)
	}
}
