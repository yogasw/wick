package slack

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	slackgo "github.com/slack-go/slack"
)

// A turn that ends while its first live post is still on the wire must not
// post the reply again: the Done reconcile waits for the flush to record the
// live message ts, then edits (or skips) that message instead.
func TestDoneWaitsForInFlightLivePost(t *testing.T) {
	var posts, updates int
	inPost := make(chan struct{})
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		posts++
		if posts == 1 {
			close(inPost)
			<-release
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"C1","ts":"1700000000.000300"}`))
	})
	mux.HandleFunc("/chat.update", func(w http.ResponseWriter, r *http.Request) {
		updates++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"C1","ts":"1700000000.000300"}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	const reply = "Yang dimaksud yang mana?"
	tr := &turn{channelID: "C1", threadTS: "1700000000.000100", running: true}
	tr.buf.WriteString(reply)
	c := &Channel{
		api:   slackgo.New("xoxb-test", slackgo.OptionAPIURL(srv.URL+"/")),
		turns: map[string]*turn{"slack-t1": tr},
	}

	flushed := make(chan struct{})
	go func() { c.flushLiveMessage("slack-t1"); close(flushed) }()
	<-inPost

	done := make(chan struct{})
	go func() {
		c.mu.Lock()
		tr.buf.Reset()
		c.mu.Unlock()
		c.NotifyState("slack-t1", "done", reply)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("Done reconciled while the live post was still in flight")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-flushed
	<-done

	if posts != 1 {
		t.Fatalf("reply posted %d times, want 1", posts)
	}
	if updates != 0 {
		t.Fatalf("unchanged reply still edited %d times, want 0", updates)
	}
}
