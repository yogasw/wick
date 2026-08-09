package spa

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestDevReloadSharesOneWatcher is the regression guard for the bug this
// endpoint used to have: one fsnotify watcher per SSE connection. On Windows
// each watcher pins a dedicated OS thread, so N open tabs pinned N threads and
// N sets of directory handles for as long as they stayed open.
//
// Asserted through the hub's own bookkeeping rather than a thread count: the
// invariant that matters is "many subscribers, one publisher", and thread
// counts are too noisy to assert on.
func TestDevReloadSharesOneWatcher(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}

	resetHub(t)

	const conns = 5
	srv := httptest.NewServer(globalSSEHandler([]string{dir}))
	defer srv.Close()

	var wg sync.WaitGroup
	readers := make([]*http.Response, 0, conns)
	var mu sync.Mutex
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(srv.URL)
			if err != nil {
				return
			}
			mu.Lock()
			readers = append(readers, resp)
			mu.Unlock()
			// Read the ": connected" preamble so the handler has reached its
			// select loop (and thus registered) before we count subscribers.
			br := bufio.NewReader(resp.Body)
			_, _ = br.ReadString('\n')
		}()
	}
	wg.Wait()
	defer func() {
		for _, r := range readers {
			_ = r.Body.Close()
		}
	}()

	if len(readers) != conns {
		t.Fatalf("connected %d of %d SSE clients", len(readers), conns)
	}

	// The whole point: conns subscribers, exactly one hub (one watcher).
	waitFor(t, "subscribers to register", func() bool {
		return subscriberCount() == conns
	})
	if hub == nil {
		t.Fatal("hub was never started")
	}
}

// TestDevReloadFansOutToEverySubscriber proves the shared watcher still
// delivers: a single rebuild must wake every connected tab, not just the one
// that happened to own the watcher before.
func TestDevReloadFansOutToEverySubscriber(t *testing.T) {
	dir := t.TempDir()
	resetHub(t)

	h := reloadHubFor([]string{dir})
	subs := make([]chan struct{}, 3)
	for i := range subs {
		subs[i] = h.subscribe()
	}
	defer func() {
		for _, ch := range subs {
			h.unsubscribe(ch)
		}
	}()

	h.publish()

	for i, ch := range subs {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatalf("subscriber %d never received the reload", i)
		}
	}
}

// TestDevReloadPublishNeverBlocks guards the non-blocking send: a subscriber
// that is not reading must not be able to stall the watcher loop for every
// other tab.
func TestDevReloadPublishNeverBlocks(t *testing.T) {
	dir := t.TempDir()
	resetHub(t)

	h := reloadHubFor([]string{dir})
	stalled := h.subscribe()
	defer h.unsubscribe(stalled)

	done := make(chan struct{})
	go func() {
		// Far more publishes than the buffer depth; a blocking send would
		// wedge on the second one.
		for i := 0; i < 100; i++ {
			h.publish()
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on a subscriber that was not reading")
	}
}

// TestDevReloadUnsubscribeReleases makes sure a closed tab stops being fanned
// to — otherwise the subscriber map grows for the life of the process.
func TestDevReloadUnsubscribeReleases(t *testing.T) {
	dir := t.TempDir()
	resetHub(t)

	h := reloadHubFor([]string{dir})
	ch := h.subscribe()
	if got := subscriberCount(); got != 1 {
		t.Fatalf("subscriberCount() = %d, want 1", got)
	}
	h.unsubscribe(ch)
	if got := subscriberCount(); got != 0 {
		t.Fatalf("after unsubscribe: subscriberCount() = %d, want 0", got)
	}
}

// TestDevReloadScriptUsesSharedWorker pins the transport choice: the page
// script must join the reload stream through the SharedWorker (one connection
// for ALL tabs) and keep the direct EventSource only as a fallback. A revert
// to unconditional `new EventSource` would silently reintroduce one held
// connection per tab — the pattern that exhausted the browser's ~6-per-origin
// quota and left later requests stuck at "pending".
func TestDevReloadScriptUsesSharedWorker(t *testing.T) {
	globalMu.Lock()
	globalWatchDirs = []string{t.TempDir()}
	globalMu.Unlock()
	t.Cleanup(func() {
		globalMu.Lock()
		globalWatchDirs = nil
		globalMu.Unlock()
	})

	script := DevReloadScript()
	if !strings.Contains(script, "SharedWorker") {
		t.Fatal("DevReloadScript must route through a SharedWorker so all tabs share one reload connection")
	}
	if !strings.Contains(script, devReloadWorkerPath) {
		t.Fatalf("DevReloadScript must load the worker from %s", devReloadWorkerPath)
	}
	if !strings.Contains(script, "new EventSource") {
		t.Fatal("DevReloadScript must keep the direct EventSource fallback for browsers without SharedWorker")
	}
}

// TestDevReloadWorkerEndpoint guards the worker script's availability: the
// inline page script hard-codes its URL, so a missing or mis-typed handler
// would silently break dev reload for every tab at once.
func TestDevReloadWorkerEndpoint(t *testing.T) {
	globalMu.Lock()
	globalWatchDirs = []string{t.TempDir()}
	globalMu.Unlock()
	t.Cleanup(func() {
		globalMu.Lock()
		globalWatchDirs = nil
		globalMu.Unlock()
	})

	mux := http.NewServeMux()
	RegisterGlobalHandler(mux)

	req := httptest.NewRequest(http.MethodGet, devReloadWorkerPath, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", devReloadWorkerPath, rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/javascript" {
		t.Fatalf("Content-Type = %q, want application/javascript", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, devReloadPath) {
		t.Fatalf("worker script must open the %s stream", devReloadPath)
	}
	if !strings.Contains(body, "onconnect") {
		t.Fatal("worker script must accept connecting tabs via onconnect")
	}
}

// resetHub clears the process-wide hub so each test starts from a known state.
// The production path deliberately builds the hub exactly once; tests need to
// rebuild it, so they reset the Once alongside it.
func resetHub(t *testing.T) {
	t.Helper()
	globalMu.Lock()
	globalWatchDirs = nil
	globalMu.Unlock()
	hubOnce = sync.Once{}
	hub = nil
	t.Cleanup(func() {
		hubOnce = sync.Once{}
		hub = nil
	})
}

func subscriberCount() int {
	if hub == nil {
		return 0
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return len(hub.subs)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", strings.TrimSpace(what))
}
