package slack

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	slackgo "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

func TestDMOnlyMessagePredicate(t *testing.T) {
	owner, channel := "U0OWNER1234", "D0123456789"
	for _, tc := range []struct {
		name, user, channel string
		ev                  *slackevents.MessageEvent
		want                bool
	}{
		{"owner DM", owner, channel, &slackevents.MessageEvent{User: owner, Channel: channel, ChannelType: "im"}, true},
		{"owner public channel", owner, channel, &slackevents.MessageEvent{User: owner, Channel: "C0123456789", ChannelType: "channel"}, false},
		{"other sender in DM", owner, channel, &slackevents.MessageEvent{User: "U0123456789", Channel: channel, ChannelType: "im"}, false},
		{"other DM", owner, channel, &slackevents.MessageEvent{User: owner, Channel: "D9876543210", ChannelType: "im"}, false},
		{"group DM", owner, channel, &slackevents.MessageEvent{User: owner, Channel: channel, ChannelType: "mpim"}, false},
		{"bot", owner, channel, &slackevents.MessageEvent{User: owner, Channel: channel, ChannelType: "im", BotID: "B0123456789"}, false},
		{"bot subtype", owner, channel, &slackevents.MessageEvent{User: owner, Channel: channel, ChannelType: "im", SubType: "bot_message"}, false},
		{"blank user env", "", channel, &slackevents.MessageEvent{User: owner, Channel: channel, ChannelType: "im"}, false},
		{"blank channel env", owner, "", &slackevents.MessageEvent{User: owner, Channel: channel, ChannelType: "im"}, false},
		{"malformed user env", " U0OWNER1234", channel, &slackevents.MessageEvent{User: owner, Channel: channel, ChannelType: "im"}, false},
		{"malformed channel env", owner, "C0123456789", &slackevents.MessageEvent{User: owner, Channel: channel, ChannelType: "im"}, false},
		{"nil message", owner, channel, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(dmOnlyUserEnv, tc.user)
			t.Setenv(dmOnlyChannelEnv, tc.channel)
			if got := dmOnlyMessageAllowed(tc.ev); got != tc.want {
				t.Fatalf("allowed = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDMOnlyGuardDropsBeforeWorkflowAndDispatch(t *testing.T) {
	t.Setenv(dmOnlyUserEnv, "U0OWNER1234")
	t.Setenv(dmOnlyChannelEnv, "D0123456789")
	s := &Channel{}
	emitted := 0
	s.SetWorkflowEventSink(func(context.Context, string, map[string]any) { emitted++ })
	// These must return without constructing a Slack client, dispatching, or emitting workflows.
	s.handleEventsAPI(context.Background(), slackevents.EventsAPIEvent{Type: slackevents.CallbackEvent, InnerEvent: slackevents.EventsAPIInnerEvent{Data: &slackevents.ReactionAddedEvent{}}})
	s.handleEventsAPI(context.Background(), slackevents.EventsAPIEvent{Type: slackevents.CallbackEvent, InnerEvent: slackevents.EventsAPIInnerEvent{Data: &slackevents.MessageEvent{User: "U0123456789", Channel: "D0123456789", ChannelType: "im"}}})
	s.handleMessage(context.Background(), &slackevents.MessageEvent{User: "U0123456789", Channel: "D0123456789", ChannelType: "im"}, nil)
	s.handleSlashCommand(context.Background(), slackgo.SlashCommand{})
	s.handleInteraction(context.Background(), slackgo.InteractionCallback{})
	if emitted != 0 {
		t.Fatalf("emitted %d workflows", emitted)
	}
}

func TestDMOnlyGuardAbsentIsUnrestricted(t *testing.T) {
	t.Setenv(dmOnlyUserEnv, "")
	t.Setenv(dmOnlyChannelEnv, "")
	os.Unsetenv(dmOnlyUserEnv)
	os.Unsetenv(dmOnlyChannelEnv)
	if dmOnlyRestricted() || !dmOnlyMessageAllowed(nil) {
		t.Fatal("absent restriction settings changed normal behavior")
	}
}

type fakeRoundTripper struct{ calls *int }

func (f fakeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	*f.calls++
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header), Request: req}, nil
}

func TestSlackConnectOnlyTransport(t *testing.T) {
	for _, tc := range []struct {
		method  string
		allowed bool
	}{
		{"auth.test", true}, {"apps.connections.open", true}, {"conversations.info", true},
		{"chat.postMessage", false}, {"chat.update", false}, {"reactions.add", false},
		{"conversations.open", false}, {"conversations.history", false}, {"conversations.replies", false}, {"oauth.v2.access", false}, {"files.getUploadURLExternal", false}, {"new.method", false},
	} {
		t.Run(tc.method, func(t *testing.T) {
			calls := 0
			transport := restrictedSlackTransport{next: fakeRoundTripper{calls: &calls}}
			req, _ := http.NewRequest(http.MethodPost, "https://slack.com/api/"+tc.method, nil)
			resp, err := transport.RoundTrip(req)
			if resp != nil {
				resp.Body.Close()
			}
			if tc.allowed && (err != nil || calls != 1) {
				t.Fatalf("read blocked: calls=%d err=%v", calls, err)
			}
			if !tc.allowed && (err == nil || calls != 0) {
				t.Fatalf("write reached transport: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestSlackConnectOnlyPreservesClientSettings(t *testing.T) {
	t.Setenv(connectOnlyEnv, "true")
	got := restrictedSlackHTTPClient()
	if got == http.DefaultClient || got.Timeout != http.DefaultClient.Timeout || got.CheckRedirect != nil {
		t.Fatal("default client settings changed")
	}
	if _, ok := got.Transport.(restrictedSlackTransport); !ok {
		t.Fatal("guard missing")
	}
}

func TestDMOnlyDirectoryConversation(t *testing.T) {
	t.Setenv(dmOnlyUserEnv, "U0OWNER1234")
	t.Setenv(dmOnlyChannelEnv, "D0OWNER1234")
	ch := &slackgo.Channel{}
	ch.ID, ch.User, ch.IsIM = "D0OWNER1234", "U0OWNER1234", true
	if !dmOnlyLookupQueryAllowed("d0owner1234") || !dmOnlyConversationAllowed(ch) {
		t.Fatal("verified owner DM not offered")
	}
	ch.User = "U0123456789"
	if dmOnlyConversationAllowed(ch) {
		t.Fatal("other user's DM offered")
	}
	ch.User, ch.IsIM = "U0OWNER1234", false
	if dmOnlyConversationAllowed(ch) {
		t.Fatal("non-DM offered")
	}
	ch.IsIM, ch.ID = true, "D0123456789"
	if dmOnlyConversationAllowed(ch) || dmOnlyLookupQueryAllowed(ch.ID) {
		t.Fatal("other DM offered")
	}
}

func TestSlackConnectOnlyBlocksNonSDKWrites(t *testing.T) {
	t.Setenv(connectOnlyEnv, "true")
	s := &Channel{}
	if _, err := s.WorkflowSend(context.Background(), "respond_url", nil); err == nil {
		t.Fatal("workflow write accepted")
	}
	if _, err := exchangeRefreshToken(context.Background(), "https://invalid.example/api", "", "", ""); err == nil {
		t.Fatal("token refresh accepted")
	}
}

func TestSlackConnectOnlyDropsOwnerMessageBeforeDispatch(t *testing.T) {
	t.Setenv(connectOnlyEnv, "true")
	t.Setenv(dmOnlyUserEnv, "U0OWNER1234")
	t.Setenv(dmOnlyChannelEnv, "D0OWNER1234")
	s := &Channel{}
	emitted, dispatched := 0, 0
	s.SetWorkflowEventSink(func(context.Context, string, map[string]any) { emitted++ })
	s.SetSendFunc(func(context.Context, string, string, string, string, string) error { dispatched++; return nil })
	ev := &slackevents.MessageEvent{User: "U0OWNER1234", Channel: "D0OWNER1234", ChannelType: "im", TimeStamp: "123.456", Text: "queued message"}
	if !dmOnlyMessageAllowed(ev) {
		t.Fatal("test message is not an allowed owner DM")
	}
	s.handleEventsAPI(context.Background(), slackevents.EventsAPIEvent{Type: slackevents.CallbackEvent, InnerEvent: slackevents.EventsAPIInnerEvent{Data: ev}})
	s.handleMessage(context.Background(), ev, nil)
	if emitted != 0 || dispatched != 0 {
		t.Fatalf("connection-only dispatched=%d workflows=%d", dispatched, emitted)
	}
}
