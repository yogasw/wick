package slack

import (
	"context"
	"testing"

	slackgo "github.com/slack-go/slack"
)

// fakeSlackAPI records the calls workflowSend makes so tests can assert
// dispatch + arg parsing without touching the live Slack API.
type fakeSlackAPI struct {
	calls       []string
	postChannel string
	postOpts    int
	ephemUser   string
	updTS       string
	emoji       string
	item        slackgo.ItemRef
	openUsers   []string
	openTrigger string
	pushTrigger string
	openView    slackgo.ModalViewRequest
	pushView    slackgo.ModalViewRequest
	updView     slackgo.ModalViewRequest
	updViewID   string
	updHash     string
	pubReq      slackgo.PublishViewContextRequest
}

func (f *fakeSlackAPI) PostMessageContext(_ context.Context, ch string, opts ...slackgo.MsgOption) (string, string, error) {
	f.calls = append(f.calls, "PostMessage")
	f.postChannel = ch
	f.postOpts = len(opts)
	return ch, "1700.0001", nil
}

func (f *fakeSlackAPI) PostEphemeralContext(_ context.Context, ch, user string, _ ...slackgo.MsgOption) (string, error) {
	f.calls = append(f.calls, "PostEphemeral")
	f.postChannel = ch
	f.ephemUser = user
	return "1700.0002", nil
}

func (f *fakeSlackAPI) UpdateMessageContext(_ context.Context, ch, ts string, _ ...slackgo.MsgOption) (string, string, string, error) {
	f.calls = append(f.calls, "UpdateMessage")
	f.postChannel = ch
	f.updTS = ts
	return ch, ts, "", nil
}

func (f *fakeSlackAPI) AddReactionContext(_ context.Context, name string, item slackgo.ItemRef) error {
	f.calls = append(f.calls, "AddReaction")
	f.emoji = name
	f.item = item
	return nil
}

func (f *fakeSlackAPI) OpenConversationContext(_ context.Context, p *slackgo.OpenConversationParameters) (*slackgo.Channel, bool, bool, error) {
	f.calls = append(f.calls, "OpenConversation")
	f.openUsers = p.Users
	ch := &slackgo.Channel{}
	ch.ID = "D999"
	return ch, false, false, nil
}

func (f *fakeSlackAPI) OpenViewContext(_ context.Context, triggerID string, view slackgo.ModalViewRequest) (*slackgo.ViewResponse, error) {
	f.calls = append(f.calls, "OpenView")
	f.openTrigger = triggerID
	f.openView = view
	return &slackgo.ViewResponse{View: slackgo.View{ID: "V1", Hash: "h1"}}, nil
}

func (f *fakeSlackAPI) PushViewContext(_ context.Context, triggerID string, view slackgo.ModalViewRequest) (*slackgo.ViewResponse, error) {
	f.calls = append(f.calls, "PushView")
	f.pushTrigger = triggerID
	f.pushView = view
	return &slackgo.ViewResponse{View: slackgo.View{ID: "V2"}}, nil
}

func (f *fakeSlackAPI) UpdateViewContext(_ context.Context, view slackgo.ModalViewRequest, _, hash, viewID string) (*slackgo.ViewResponse, error) {
	f.calls = append(f.calls, "UpdateView")
	f.updView = view
	f.updViewID = viewID
	f.updHash = hash
	return &slackgo.ViewResponse{View: slackgo.View{ID: viewID}}, nil
}

func (f *fakeSlackAPI) PublishViewContext(_ context.Context, req slackgo.PublishViewContextRequest) (*slackgo.ViewResponse, error) {
	f.calls = append(f.calls, "PublishView")
	f.pubReq = req
	return &slackgo.ViewResponse{View: slackgo.View{ID: "H1"}}, nil
}

func dispatch(t *testing.T, f *fakeSlackAPI, op string, args map[string]any) (any, error) {
	t.Helper()
	c := &Channel{}
	return c.workflowSend(context.Background(), f, op, args)
}

func TestWorkflowSend_SendMessage(t *testing.T) {
	f := &fakeSlackAPI{}
	out, err := dispatch(t, f, "send_message", map[string]any{"channel": "C1", "text": "hi"})
	if err != nil {
		t.Fatalf("send_message: %v", err)
	}
	if f.postChannel != "C1" || f.calls[0] != "PostMessage" {
		t.Fatalf("wrong dispatch: %+v", f)
	}
	m := out.(map[string]any)
	if m["ts"] != "1700.0001" || m["channel"] != "C1" {
		t.Fatalf("bad output: %+v", m)
	}
}

func TestWorkflowSend_SendMessage_MissingText(t *testing.T) {
	f := &fakeSlackAPI{}
	if _, err := dispatch(t, f, "send_message", map[string]any{"channel": "C1"}); err == nil {
		t.Fatal("expected error for missing text")
	}
}

func TestWorkflowSend_ReplyThread(t *testing.T) {
	f := &fakeSlackAPI{}
	if _, err := dispatch(t, f, "reply_thread", map[string]any{"channel": "C1", "thread": "1700.0", "text": "re"}); err != nil {
		t.Fatalf("reply_thread: %v", err)
	}
	if f.postOpts < 2 {
		t.Fatalf("reply_thread should pass text + thread opts, got %d", f.postOpts)
	}
}

func TestWorkflowSend_React(t *testing.T) {
	f := &fakeSlackAPI{}
	if _, err := dispatch(t, f, "react", map[string]any{"channel": "C1", "message_ts": "1700.0", "emoji": "tada"}); err != nil {
		t.Fatalf("react: %v", err)
	}
	if f.emoji != "tada" || f.item.Channel != "C1" || f.item.Timestamp != "1700.0" {
		t.Fatalf("bad react args: %+v", f)
	}
}

func TestWorkflowSend_SendDM_OpensConversation(t *testing.T) {
	f := &fakeSlackAPI{}
	out, err := dispatch(t, f, "send_dm", map[string]any{"user": "U7", "text": "yo"})
	if err != nil {
		t.Fatalf("send_dm: %v", err)
	}
	if len(f.openUsers) != 1 || f.openUsers[0] != "U7" {
		t.Fatalf("send_dm should open conversation with user, got %+v", f.openUsers)
	}
	if f.postChannel != "D999" {
		t.Fatalf("send_dm should post to opened DM channel, got %q", f.postChannel)
	}
	if out.(map[string]any)["channel"] != "D999" {
		t.Fatalf("bad output: %+v", out)
	}
}

func TestWorkflowSend_OpenModal_ParsesView(t *testing.T) {
	f := &fakeSlackAPI{}
	view := `{"type":"modal","title":{"type":"plain_text","text":"Hi"}}`
	out, err := dispatch(t, f, "open_modal", map[string]any{"trigger_id": "T1", "view": view})
	if err != nil {
		t.Fatalf("open_modal: %v", err)
	}
	if f.openTrigger != "T1" || string(f.openView.Type) != "modal" {
		t.Fatalf("view not parsed/dispatched: trigger=%q type=%q", f.openTrigger, f.openView.Type)
	}
	m := out.(map[string]any)
	if m["view_id"] != "V1" || m["view_hash"] != "h1" {
		t.Fatalf("bad output: %+v", m)
	}
}

func TestWorkflowSend_OpenModal_InvalidViewJSON(t *testing.T) {
	f := &fakeSlackAPI{}
	if _, err := dispatch(t, f, "open_modal", map[string]any{"trigger_id": "T1", "view": "{not json}"}); err == nil {
		t.Fatal("expected error for invalid view JSON")
	}
}

func TestWorkflowSend_PublishHome(t *testing.T) {
	f := &fakeSlackAPI{}
	view := `{"type":"home","blocks":[]}`
	if _, err := dispatch(t, f, "publish_home", map[string]any{"user_id": "U7", "view": view}); err != nil {
		t.Fatalf("publish_home: %v", err)
	}
	if f.pubReq.UserID != "U7" || string(f.pubReq.View.Type) != "home" {
		t.Fatalf("bad publish req: %+v", f.pubReq)
	}
}

func TestWorkflowSend_UnknownOp(t *testing.T) {
	f := &fakeSlackAPI{}
	if _, err := dispatch(t, f, "no_such_op", map[string]any{}); err == nil {
		t.Fatal("expected error for unknown op")
	}
}
