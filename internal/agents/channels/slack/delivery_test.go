package slack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/agents/store"
)

// deliveryFakeSlack answers chat.postMessage / chat.update with postReply and
// chat.getPermalink with a link built from the ts it was asked about.
type deliveryFakeSlack struct {
	srv       *httptest.Server
	postReply string
	mu        sync.Mutex
	linkTS    []string
}

func newDeliveryFakeSlack(t *testing.T, postReply string) *deliveryFakeSlack {
	t.Helper()
	f := &deliveryFakeSlack{postReply: postReply}
	reply := func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(f.postReply))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/chat.postMessage", reply)
	mux.HandleFunc("/chat.update", reply)
	mux.HandleFunc("/chat.getPermalink", func(w http.ResponseWriter, r *http.Request) {
		// GET with a query string or a form POST, depending on slack-go.
		ts := r.FormValue("message_ts")
		f.mu.Lock()
		f.linkTS = append(f.linkTS, ts)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"ok":true,"channel":"C1","permalink":"https://example.slack.com/archives/C1/p%s"}`, ts)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

type recordedDelivery struct {
	turnID string
	d      store.Delivery
}

// deliveryChannel is a Channel whose recorder keeps every report, and
// resolves "" to the turn ID "turn-1" the way the real recorder resolves it
// to the newest assistant turn.
func deliveryChannel(f *deliveryFakeSlack) (*Channel, *[]recordedDelivery) {
	var got []recordedDelivery
	c := &Channel{
		api:   slackgo.New("xoxb-test", slackgo.OptionAPIURL(f.srv.URL+"/")),
		turns: map[string]*turn{"slack-t1": {}},
	}
	c.SetDeliveryFn(func(sessionID, turnID string, d store.Delivery) string {
		if sessionID != "slack-t1" {
			panic("unexpected session " + sessionID)
		}
		if turnID == "" {
			turnID = "turn-1"
		}
		got = append(got, recordedDelivery{turnID, d})
		return turnID
	})
	return c, &got
}

const okPost = `{"ok":true,"channel":"C1","ts":"1700000000.000300"}`

func TestFinalizeReplyRecordsSent(t *testing.T) {
	f := newDeliveryFakeSlack(t, okPost)
	c, got := deliveryChannel(f)
	c.finalizeReply("slack-t1", "C1", "1700000000.000100", "Selesai.", "", "")

	if len(*got) != 2 {
		t.Fatalf("want sending + sent, got %+v", *got)
	}
	if s := (*got)[0].d.Status; s != store.DeliverySending {
		t.Fatalf("first report = %q, want sending", s)
	}
	last := (*got)[1]
	if last.turnID != "turn-1" || last.d.Status != store.DeliverySent || last.d.Channel != "slack" {
		t.Fatalf("settled report = %+v", last)
	}
	if want := "https://example.slack.com/archives/C1/p1700000000.000300"; last.d.Permalink != want {
		t.Fatalf("permalink = %q, want %q (the posted reply)", last.d.Permalink, want)
	}
	if last.d.Error != "" || last.d.At.IsZero() {
		t.Fatalf("sent report error=%q at=%v", last.d.Error, last.d.At)
	}
}

func TestFinalizeReplyRecordsFailure(t *testing.T) {
	f := newDeliveryFakeSlack(t, `{"ok":false,"error":"channel_not_found"}`)
	c, got := deliveryChannel(f)
	c.finalizeReply("slack-t1", "C1", "1700000000.000100", "Selesai.", "", "")

	if len(*got) != 2 {
		t.Fatalf("want sending + failed, got %+v", *got)
	}
	last := (*got)[1].d
	if last.Status != store.DeliveryFailed || last.Error != "channel_not_found" {
		t.Fatalf("settled report = %+v, want failed channel_not_found", last)
	}
	if last.Permalink != "" {
		t.Fatalf("nothing was posted, yet permalink = %q", last.Permalink)
	}
}

// A reply already streaming into a live message is linked to that message.
func TestFinalizeReplyLiveMessageLinksLiveTS(t *testing.T) {
	f := newDeliveryFakeSlack(t, okPost)
	c, got := deliveryChannel(f)
	c.finalizeReply("slack-t1", "C1", "1700000000.000100", "Final text", "1700000000.000200", "Partial")

	last := (*got)[len(*got)-1].d
	if last.Status != store.DeliverySent {
		t.Fatalf("status = %q, want sent", last.Status)
	}
	if want := "https://example.slack.com/archives/C1/p1700000000.000200"; last.Permalink != want {
		t.Fatalf("permalink = %q, want the live message %q", last.Permalink, want)
	}
}

func TestFinalizeReplyEmptyTextRecordsNothing(t *testing.T) {
	f := newDeliveryFakeSlack(t, okPost)
	c, got := deliveryChannel(f)
	c.finalizeReply("slack-t1", "C1", "1700000000.000100", "", "", "")
	if len(*got) != 0 {
		t.Fatalf("nothing posted, yet recorded %+v", *got)
	}
}

// No recorder wired: posting works exactly as before.
func TestFinalizeReplyWithoutRecorder(t *testing.T) {
	f := newDeliveryFakeSlack(t, okPost)
	c := &Channel{
		api:   slackgo.New("xoxb-test", slackgo.OptionAPIURL(f.srv.URL+"/")),
		turns: map[string]*turn{"slack-t1": {}},
	}
	c.finalizeReply("slack-t1", "C1", "1700000000.000100", "Selesai.", "", "")
	if len(f.linkTS) != 0 {
		t.Fatalf("no recorder, yet asked Slack for permalinks %v", f.linkTS)
	}
}

func TestMessagePermalink(t *testing.T) {
	f := newDeliveryFakeSlack(t, okPost)
	c, _ := deliveryChannel(f)
	if got, want := c.messagePermalink("C1", "1700000000.000100"), "https://example.slack.com/archives/C1/p1700000000.000100"; got != want {
		t.Fatalf("permalink = %q, want %q", got, want)
	}
	if got := c.messagePermalink("", "1"); got != "" {
		t.Fatalf("no channel, yet permalink %q", got)
	}
	if got := (&Channel{}).messagePermalink("C1", "1"); got != "" {
		t.Fatalf("no client, yet permalink %q", got)
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestDeliveryErrorCode(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{errors.New("channel_not_found"), "channel_not_found"},
		{errors.New("not_in_channel"), "not_in_channel"},
		{errors.New("rate_limited"), "rate_limited"},
		{errors.New("slack rate limit exceeded, retry after 3s"), "rate_limited"},
		{context.DeadlineExceeded, "timeout"},
		{fmt.Errorf("post: %w", timeoutErr{}), "timeout"},
		{errors.New("slack server error: 503 Service Unavailable"), "http_503"},
		// Anything unrecognised never reaches the page verbatim.
		{errors.New(`Post "https://slack.com/api/chat.postMessage?token=xoxb-secret": EOF`), "request_failed"},
		{errSlackNotConnected, "not_connected"},
	}
	for _, tc := range cases {
		if got := deliveryErrorCode(tc.err); got != tc.want {
			t.Errorf("deliveryErrorCode(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}
