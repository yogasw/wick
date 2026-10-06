package slack

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	slackgo "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// callLog records every Slack Web API call by method, answering ok to all.
type callLog struct {
	mu    sync.Mutex
	calls map[string][]url.Values
}

func newCallLogSlack(t *testing.T) (*callLog, *slackgo.Client) {
	t.Helper()
	l := &callLog{calls: map[string][]url.Values{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		vals, _ := url.ParseQuery(string(body))
		l.mu.Lock()
		l.calls[r.URL.Path] = append(l.calls[r.URL.Path], vals)
		l.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"D1","ts":"1700000000.000200"}`))
	}))
	t.Cleanup(srv.Close)
	return l, slackgo.New("xoxb-test", slackgo.OptionAPIURL(srv.URL+"/"))
}

func (l *callLog) get(path string) []url.Values {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]url.Values(nil), l.calls[path]...)
}

// An instance that was never handed the pool dispatch must refuse the
// message in the thread, not dereference a nil func and kill the daemon.
func TestHandleMessageWithoutSendFnRepliesInsteadOfPanicking(t *testing.T) {
	l, api := newCallLogSlack(t)
	c := &Channel{api: api, turns: map[string]*turn{}}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("handleMessage panicked with nil sendFn: %v", r)
		}
	}()
	c.handleMessage(context.Background(), &slackevents.MessageEvent{
		Channel:     "D1",
		ChannelType: "im",
		User:        "U1",
		Text:        "halo",
		TimeStamp:   "1700000000.000100",
	}, nil)

	posts := l.get("/chat.postMessage")
	if len(posts) != 1 || posts[0].Get("text") != notReadyReply {
		t.Fatalf("want one %q reply, got %v", notReadyReply, posts)
	}
	if posts[0].Get("thread_ts") != "1700000000.000100" {
		t.Fatalf("reply not threaded: %v", posts[0])
	}
	reacts := l.get("/reactions.add")
	if len(reacts) == 0 || reacts[len(reacts)-1].Get("name") != reactionError {
		t.Fatalf("want %q reaction, got %v", reactionError, reacts)
	}
}

// A panic while handling one socket event (here: acking with no socket
// client) must be recovered inside the handler, so the Start loop and
// the daemon keep running.
func TestHandleSocketEventRecoversPanic(t *testing.T) {
	c := &Channel{turns: map[string]*turn{}}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic escaped handleSocketEvent: %v", r)
		}
	}()
	c.handleSocketEvent(context.Background(), socketmode.Event{
		Type:    socketmode.EventTypeEventsAPI,
		Request: &socketmode.Request{},
	})
}
