package askuser

import (
	"path/filepath"
	"testing"
	"time"
)

func testSocketPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "a.sock")
}

func TestSocketRoundTrip(t *testing.T) {
	mgr := NewManager(Options{})
	path := testSocketPath(t)
	srv, err := ServeSocket(path, mgr)
	if err != nil {
		t.Fatalf("ServeSocket: %v", err)
	}
	defer srv.Close()

	// Resolve from the "UI" side as soon as the ask registers.
	go func() {
		for i := 0; i < 100; i++ {
			pending := mgr.PendingFor("s1")
			if len(pending) > 0 {
				mgr.Resolve(pending[0].ID, Answer{Values: map[string]string{"base_url": "https://abc.net"}})
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	asker := &SocketAsker{Path: path}
	ans, err := asker.Ask(Question{
		SessionID: "s1",
		Question:  "override?",
		Fields:    []Field{{Key: "base_url", Label: "base_url"}},
		Timeout:   5 * time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("Ask over socket: %v", err)
	}
	if ans.Values["base_url"] != "https://abc.net" {
		t.Fatalf("answer = %+v", ans)
	}
}

func TestSocketAskerDialFailure(t *testing.T) {
	asker := &SocketAsker{Path: filepath.Join(t.TempDir(), "missing.sock")}
	_, err := asker.Ask(Question{SessionID: "s1", Question: "q", Timeout: time.Second}, nil)
	if err == nil {
		t.Fatal("want dial error, got nil")
	}
}

func TestSocketServerPropagatesAskError(t *testing.T) {
	mgr := NewManager(Options{})
	path := testSocketPath(t)
	srv, err := ServeSocket(path, mgr)
	if err != nil {
		t.Fatalf("ServeSocket: %v", err)
	}
	defer srv.Close()

	asker := &SocketAsker{Path: path}
	// Nobody resolves — the 100ms timeout should come back as an error
	// string over the wire.
	_, err = asker.Ask(Question{SessionID: "s1", Question: "q", Timeout: 100 * time.Millisecond}, nil)
	if err == nil {
		t.Fatal("want timeout error, got nil")
	}
}
