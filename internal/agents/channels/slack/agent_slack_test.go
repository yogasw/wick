package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	slackgo "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/yogasw/wick/internal/agents/event"
)

// stubSlack answers the few Web API methods these tests touch and records
// every form it was sent, keyed by method.
type stubSlack struct {
	srv    *httptest.Server
	mu     sync.Mutex
	calls  map[string][]url.Values
	scopes string
}

func newStubSlack(t *testing.T) *stubSlack {
	f := &stubSlack{calls: map[string][]url.Values{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		method := r.URL.Path[1:]
		f.mu.Lock()
		f.calls[method] = append(f.calls[method], r.Form)
		scopes := f.scopes
		f.mu.Unlock()
		if scopes != "" {
			w.Header().Set("X-OAuth-Scopes", scopes)
		}
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "auth.test":
			_, _ = w.Write([]byte(`{"ok":true,"user_id":"UB1","bot_id":"B1","user":"rekap","team":"T"}`))
		case "users.info":
			_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"U1","name":"yoga","profile":{"email":"y@example.com"}}}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *stubSlack) client() *slackgo.Client {
	return slackgo.New("xoxb-test", slackgo.OptionAPIURL(f.srv.URL+"/"))
}

func (f *stubSlack) sent(method string) []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

// Opening the agent view answers with the agent's prompts, capped at four.
func TestAssistantThreadStartedSetsSuggestedPrompts(t *testing.T) {
	f := newStubSlack(t)
	c := &Channel{api: f.client()}
	c.SetPromptsFn(func() []SuggestedPrompt {
		return []SuggestedPrompt{{"a", "1"}, {"b", "2"}, {"c", "3"}, {"d", "4"}, {"e", "5"}}
	})
	c.handleEventsAPI(context.Background(), slackevents.EventsAPIEvent{
		Type: slackevents.CallbackEvent,
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Type: "assistant_thread_started",
			Data: &slackevents.AssistantThreadStartedEvent{AssistantThread: slackevents.AssistantThread{ChannelID: "D1", ThreadTimeStamp: "1.2"}},
		},
	})
	calls := f.sent("assistant.threads.setSuggestedPrompts")
	if len(calls) != 1 {
		t.Fatalf("setSuggestedPrompts calls = %d, want 1", len(calls))
	}
	if calls[0].Get("channel_id") != "D1" || calls[0].Get("thread_ts") != "1.2" {
		t.Errorf("wrong thread: %v", calls[0])
	}
	var prompts []map[string]string
	if err := json.Unmarshal([]byte(calls[0].Get("prompts")), &prompts); err != nil || len(prompts) != MaxSuggestedPrompts {
		t.Fatalf("prompts = %q (%v)", calls[0].Get("prompts"), err)
	}
	if _, ok := c.SeenEvents()["assistant_thread_started"]; !ok {
		t.Error("the event must be recorded as seen")
	}
}

// No prompts configured = no call at all.
func TestAssistantThreadStartedWithoutPromptsIsQuiet(t *testing.T) {
	f := newStubSlack(t)
	c := &Channel{api: f.client()}
	c.SetPromptsFn(func() []SuggestedPrompt { return nil })
	c.handleAssistantThreadStarted(context.Background(), &slackevents.AssistantThreadStartedEvent{AssistantThread: slackevents.AssistantThread{ChannelID: "D1", ThreadTimeStamp: "1.2"}})
	if n := len(f.sent("assistant.threads.setSuggestedPrompts")); n != 0 {
		t.Fatalf("calls = %d, want 0", n)
	}
}

func TestParseOAuthScopesAndTokenScopes(t *testing.T) {
	if got := ParseOAuthScopes(" chat:write, im:history ,,"); !slices.Equal(got, []string{"chat:write", "im:history"}) {
		t.Fatalf("parse = %v", got)
	}
	f := newStubSlack(t)
	f.scopes = "chat:write,app_mentions:read"
	got, err := TokenScopes("xoxb-test", f.srv.URL+"/")
	if err != nil || !slices.Equal(got, []string{"chat:write", "app_mentions:read"}) {
		t.Fatalf("TokenScopes = %v, %v", got, err)
	}
}

func rowOf(rows []MatrixRow, key string) MatrixRow {
	for _, r := range rows {
		if r.Key == key {
			return r
		}
	}
	return MatrixRow{}
}

func itemOf(items []MatrixItem, name string) MatrixItem {
	for _, it := range items {
		if it.Name == name {
			return it
		}
	}
	return MatrixItem{}
}

// A scope the manifest declares but the token lacks means the app was
// changed and not reinstalled; one missing from both is a plain add.
func TestMatrixReinstallVerdict(t *testing.T) {
	all := RequiredBotScopes()
	token := slices.DeleteFunc(slices.Clone(all), func(s string) bool { return s == "im:write" || s == "im:read" })
	manifest := slices.DeleteFunc(slices.Clone(all), func(s string) bool { return s == "im:read" })
	rows := BuildMatrix(MatrixInput{TokenScopes: token, ManifestScopes: manifest, ManifestEvents: RequiredBotEvents()})
	dm := rowOf(rows, FeatureDM)
	if dm.EventsFrom != EventsFromManifest {
		t.Errorf("events_from = %q, want manifest", dm.EventsFrom)
	}
	if dm.Status != StatusError {
		t.Fatalf("dm status = %q", dm.Status)
	}
	if it := itemOf(dm.Scopes, "im:write"); it.Status != StatusError || !contains(it.Hint, "reinstall the app to your workspace") {
		t.Errorf("im:write = %+v, want reinstall verdict", it)
	}
	if it := itemOf(dm.Scopes, "im:read"); !contains(it.Hint, "add the bot scope") {
		t.Errorf("im:read = %+v, want add-scope hint", it)
	}
	if rowOf(rows, FeatureCore).Status != StatusOK {
		t.Error("core is fully granted")
	}
}

// Without a manifest an event is judged by whether it ever arrived; a
// switched-off feature is "off", never a failure.
func TestMatrixEventObservationAndOff(t *testing.T) {
	c := &Channel{}
	c.observeEvent(observedEventName("message", "im"))
	c.observeEvent(observedEventName("app_mention", ""))
	rows := BuildMatrix(MatrixInput{
		TokenScopes: RequiredBotScopes(),
		Seen:        c.SeenEvents(),
		Active:      func(k string) bool { return k != FeatureReactionReply },
	})
	if it := itemOf(rowOf(rows, FeatureDM).Events, "message.im"); it.Status != StatusOK {
		t.Errorf("message.im = %+v", it)
	}
	core := rowOf(rows, FeatureCore)
	// Not seen since boot is pending, not a warning: the row stays ok.
	if it := itemOf(core.Events, "message.channels"); it.Status != StatusPending || !contains(it.Hint, "not seen yet") {
		t.Errorf("message.channels = %+v", it)
	}
	if core.Status != StatusOK || core.EventsFrom != EventsFromReceived {
		t.Errorf("core = %q from %q, want ok from received", core.Status, core.EventsFrom)
	}
	if r := rowOf(rows, FeatureReactionReply); r.Status != StatusOff || r.Scopes[0].Status != StatusOff || r.Scopes[0].Hint != "off — not checked" {
		t.Errorf("reaction row = %+v", r)
	}
	if observedEventName("message", "group") != "message.groups" || observedEventName("message", "channel") != "message.channels" {
		t.Error("message event names")
	}
}

type fakeUsers struct{ id string }

func (f fakeUsers) FindByChannelIdentity(context.Context, string, string, string) (string, bool) {
	return f.id, f.id != ""
}
func (f fakeUsers) FindByEmail(context.Context, string) (string, bool) { return f.id, f.id != "" }
func (f fakeUsers) RegisterFromChannel(context.Context, string, string, string) (string, error) {
	return "", ErrNoAccount
}
func (f fakeUsers) IsApproved(context.Context, string) bool                        { return true }
func (f fakeUsers) AutoRegisterEnabled(context.Context) bool                       { return false }
func (f fakeUsers) RecordIdentity(context.Context, string, string, string, string) {}

// A DM from a known person continues the main chat the resolver names; a
// channel thread never does, and nor does a DM when the option is off.
func TestDMMainSessionRouting(t *testing.T) {
	f := newStubSlack(t)
	c := &Channel{api: f.client(), sessionPrefix: "slackagent-a1-", users: fakeUsers{id: "u1"}}
	im := &slackevents.MessageEvent{ChannelType: "im", User: "U1", Channel: "D1", TimeStamp: "1.1"}
	if got := c.dmMainSession(im); got != "" {
		t.Fatalf("option off: got %q", got)
	}
	var asked string
	c.SetDMMainFn(func(uid string) string { asked = uid; return "main-1" })
	if got := c.dmMainSession(im); got != "main-1" || asked != "u1" {
		t.Fatalf("dm: got %q for %q", got, asked)
	}
	ch := &slackevents.MessageEvent{ChannelType: "channel", User: "U1", Channel: "C1", TimeStamp: "1.1"}
	if got := c.dmMainSession(ch); got != "" {
		t.Fatalf("channel thread must keep its own session, got %q", got)
	}
	// The main chat's turn is let go once answered, so a later web turn is
	// not delivered into the DM.
	c.turns = map[string]*turn{"main-1": {channelID: "D1", threadTS: "1.1", running: true}}
	c.OnAgentEvent("main-1", event.AgentEvent{Type: event.Done})
	c.mu.Lock()
	_, still := c.turns["main-1"]
	c.mu.Unlock()
	if still {
		t.Error("main chat turn must be released after Done")
	}
	// Unknown sender: no main chat, the usual per-thread session.
	c.users = fakeUsers{}
	if got := c.dmMainSession(im); got != "" {
		t.Fatalf("unknown sender: got %q", got)
	}
	_ = time.Now
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
func (f fakeUsers) CanUseAgents(context.Context, string) bool { return true }
