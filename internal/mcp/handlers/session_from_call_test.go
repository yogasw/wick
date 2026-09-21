package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
)

// Tools that MANAGE a session take an explicit target when one is given —
// that is how an admin acts on another conversation — and fall back to the
// session the call itself carries. The opposite of ResolveCallSession, and
// deliberately so; see the doc comments on both.
func TestResolveSessionPreferArg(t *testing.T) {
	tests := []struct {
		name   string
		header string
		arg    string
		want   string
	}{
		{"explicit target wins", "sess-own", "sess-other", "sess-other"},
		{"call's own session fills the gap", "sess-own", "", "sess-own"},
		{"argument alone", "", "sess-arg", "sess-arg"},
		{"neither", "", "", ""},
		{"whitespace argument is not a target", "sess-own", "   ", "sess-own"},
		{"values are trimmed", " sess-own ", "", "sess-own"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveSessionPreferArg(tt.header, tt.arg); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// fixtureSession writes a minimal session on disk so the meta handlers can
// load it.
func fixtureSession(t *testing.T, id string) agentconfig.Layout {
	t.Helper()
	layout := agentconfig.NewLayout(t.TempDir())
	if err := os.MkdirAll(layout.SessionDir(id), 0o755); err != nil {
		t.Fatalf("mkdir session: %v", err)
	}
	if err := session.SaveMeta(layout, id, session.Meta{Label: "auto label"}); err != nil {
		t.Fatalf("save meta: %v", err)
	}
	return layout
}

func TestWickSessionInfo_ResolvesSessionFromCall(t *testing.T) {
	const id = "sess-abc"
	layout := fixtureSession(t, id)

	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set(SessionHeader, id)

	var got ToolCallResult
	WickSessionInfo(httptest.NewRecorder(), r, RPCRequest{}, captureResponder(t, &got), layout, map[string]any{})
	if got.IsError {
		t.Fatalf("session_id should not be required when the call carries one: %+v", got)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(got.Content[0].Text), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["session_id"] != id {
		t.Fatalf("session_id = %v, want %s", out["session_id"], id)
	}
}

func TestWickSetTitle_ResolvesSessionFromCall(t *testing.T) {
	const id = "sess-abc"
	layout := fixtureSession(t, id)

	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set(SessionHeader, id)

	var got ToolCallResult
	WickSetTitle(httptest.NewRecorder(), r, RPCRequest{}, captureResponder(t, &got), layout, nil, map[string]any{"title": "Fix webhook 401"})
	if got.IsError {
		t.Fatalf("set_title without session_id: %+v", got)
	}
	sess, err := session.Load(layout, id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if sess.Meta.Label != "Fix webhook 401" || !sess.Meta.TitleCustom {
		t.Fatalf("meta = %q custom=%v", sess.Meta.Label, sess.Meta.TitleCustom)
	}
}

// No header and no argument is still an error — the tool must not guess.
func TestWickSessionInfo_NoSessionAnywhere(t *testing.T) {
	layout := fixtureSession(t, "sess-abc")
	var got ToolCallResult
	WickSessionInfo(httptest.NewRecorder(), httptest.NewRequest("POST", "/mcp", nil), RPCRequest{},
		captureResponder(t, &got), layout, map[string]any{})
	if !got.IsError {
		t.Fatal("expected an error when neither the call nor the args name a session")
	}
}
