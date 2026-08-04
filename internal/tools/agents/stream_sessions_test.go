package agents

import (
	"encoding/json"
	"testing"
)

// The sidebar stream drops sub-agent sessions, because a child is not a
// row. That left a conversation looking dormant while its sub-agent
// worked: the leader's last event was "idle", and nothing after it ever
// mentioned the child. projectSidebarEvent re-attributes a child's
// transition to the conversation that owns it instead.
func TestProjectSidebarEvent(t *testing.T) {
	// root ── child
	parentOf := func(id string) (string, bool) {
		switch id {
		case "root":
			return "", true
		case "child":
			return "root", true
		default:
			return "", false
		}
	}

	cases := []struct {
		name         string
		sessionID    string
		lifecycle    string
		wantOK       bool
		wantSession  string
		wantLifecyle string
		wantSubAgent string
	}{
		{
			name: "a leader's own transition passes through untouched",
			// Lifecycle stays on Lifecycle: this is the conversation's own
			// process, and SubAgent must stay empty or the row would claim
			// delegated work that is not happening.
			sessionID: "root", lifecycle: "working",
			wantOK: true, wantSession: "root", wantLifecyle: "working", wantSubAgent: "",
		},
		{
			name:      "a child's work is attributed to its parent",
			sessionID: "child", lifecycle: "working",
			wantOK: true, wantSession: "root", wantLifecyle: "", wantSubAgent: "working",
		},
		{
			name:      "a child spawning counts too",
			sessionID: "child", lifecycle: "spawning",
			wantOK: true, wantSession: "root", wantLifecyle: "", wantSubAgent: "spawning",
		},
		{
			// The clearing edge: when a child stops, the parent row has to
			// be told, or its teal spinner spins forever. An empty SubAgent
			// on a known root IS the clear signal.
			name:      "a child going idle clears the parent's marker",
			sessionID: "child", lifecycle: "idle",
			wantOK: true, wantSession: "root", wantLifecyle: "", wantSubAgent: "",
		},
		{
			name:      "a child being killed clears it too",
			sessionID: "child", lifecycle: "killed",
			wantOK: true, wantSession: "root", wantLifecyle: "", wantSubAgent: "",
		},
		{
			name:      "an unknown session is dropped",
			sessionID: "ghost", lifecycle: "working",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := projectSidebarEvent(tc.sessionID, tc.lifecycle, parentOf)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if got.SessionID != tc.wantSession {
				t.Errorf("session = %q, want %q", got.SessionID, tc.wantSession)
			}
			if got.Lifecycle != tc.wantLifecyle {
				t.Errorf("lifecycle = %q, want %q", got.Lifecycle, tc.wantLifecyle)
			}
			if got.SubAgent != tc.wantSubAgent {
				t.Errorf("sub_agent = %q, want %q", got.SubAgent, tc.wantSubAgent)
			}
			if got.Type != "lifecycle" {
				t.Errorf("type = %q, want lifecycle", got.Type)
			}
		})
	}
}

// The projected event must not leak the fields the sidebar has no use
// for — the same reason the original stream re-projected instead of
// forwarding. A child's own session id in particular stays out: the
// sidebar cannot address it, and shipping it widens what a subscriber
// can observe for no gain.
func TestProjectSidebarEventShipsNoExtraFields(t *testing.T) {
	parentOf := func(id string) (string, bool) {
		if id == "child" {
			return "root", true
		}
		return "", true
	}
	ev, ok := projectSidebarEvent("child", "working", parentOf)
	if !ok {
		t.Fatal("expected a projected event")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(ev.JSON()), &out); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	allowed := map[string]bool{
		"session_id": true, "agent_name": true, "type": true,
		"data": true, "sub_agent": true, "lifecycle": true,
	}
	for k := range out {
		if !allowed[k] {
			t.Errorf("unexpected field %q in sidebar event", k)
		}
	}
	if out["session_id"] != "root" {
		t.Errorf("session_id = %v, want root", out["session_id"])
	}
}
