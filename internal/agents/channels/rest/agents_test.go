package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/entity"
)

// fakeAgentDir is an AgentDirectory over a fixed list.
type fakeAgentDir struct {
	mu       sync.Mutex
	agents   []Agent
	sessions map[string]string // session id → agent id
}

func (d *fakeAgentDir) AgentByHandle(_ context.Context, owner, handle string) (Agent, bool) {
	for _, a := range d.agents {
		if a.OwnerUserID == owner && a.Handle == handle {
			return a, true
		}
	}
	return Agent{}, false
}

func (d *fakeAgentDir) Agents(_ context.Context, owner string) []Agent {
	var out []Agent
	for _, a := range d.agents {
		if a.OwnerUserID == owner {
			out = append(out, a)
		}
	}
	return out
}

func (d *fakeAgentDir) EnsureSession(_ context.Context, a Agent, sessionID string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.sessions[sessionID]; ok {
		return false, nil
	}
	d.sessions[sessionID] = a.ID
	return true, nil
}

func withAgentDir(t *testing.T, agents ...Agent) *fakeAgentDir {
	t.Helper()
	d := &fakeAgentDir{agents: agents, sessions: map[string]string{}}
	prev := agentDir
	agentDir = d
	t.Cleanup(func() { agentDir = prev })
	return d
}

type agentSend struct {
	SessionID, Role, Text, Project, Caller string
}

// newAgentTestChannel is newTestChannel that also records the project
// override and caller every dispatch carries. enabled is the plain
// channel's switch.
func newAgentTestChannel(t *testing.T, enabled bool) (*Channel, func() []agentSend) {
	t.Helper()
	on := "false"
	if enabled {
		on = "true"
	}
	ch := New(agentconfig.RestChannelConfig{Enabled: on, ProjectID: "main"}, &fakeAuth{wantToken: "good", userID: "user-1"})
	ch.SetSessionChecker(fakeSessions{exists: true})
	var mu sync.Mutex
	var sent []agentSend
	ch.SetSendFunc(func(ctx context.Context, sessionID, _, _, role, text string) error {
		mu.Lock()
		sent = append(sent, agentSend{sessionID, role, text, agentchannels.ProjectOverride(ctx), agentchannels.CallerUserID(ctx)})
		mu.Unlock()
		if role == "user" {
			go func() {
				ch.OnAgentEvent(sessionID, event.AgentEvent{Type: event.TextDelta, Text: "from agent"})
				ch.OnAgentEvent(sessionID, event.AgentEvent{Type: event.Done})
			}()
		}
		return nil
	})
	return ch, func() []agentSend {
		mu.Lock()
		defer mu.Unlock()
		return append([]agentSend(nil), sent...)
	}
}

var (
	rekapAgent  = Agent{ID: "ag-1", OwnerUserID: "user-1", Handle: "rekap", ProjectID: "proj-rekap", RESTEnabled: true}
	offAgent    = Agent{ID: "ag-2", OwnerUserID: "user-1", Handle: "quiet", ProjectID: "proj-quiet"}
	pausedAgent = Agent{ID: "ag-3", OwnerUserID: "user-1", Handle: "paused", ProjectID: "p", RESTEnabled: true, Disabled: true}
	otherAgent  = Agent{ID: "ag-4", OwnerUserID: "user-2", Handle: "theirs", ProjectID: "p", RESTEnabled: true}
)

func agentChat(model string, extra map[string]any) map[string]any {
	body := map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "hi"}}}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error.Code
}

func TestAgentModel_RoutesToAgent(t *testing.T) {
	stubModels(t) // no providers: an agent model must not need one
	dir := withAgentDir(t, rekapAgent)
	// The plain channel is off: the agent's own connection is the gate.
	ch, sent := newAgentTestChannel(t, false)
	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", agentChat("agent:rekap", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp chatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Model != "agent:rekap" || resp.Choices[0].Message.Content != "from agent" {
		t.Fatalf("response: %+v", resp)
	}
	calls := sent()
	if len(calls) != 2 || calls[0].Role != "system" || !strings.Contains(calls[0].Text, "Agent: @rekap") {
		t.Fatalf("want context note then user turn, got %+v", calls)
	}
	for _, c := range calls {
		if c.Project != "proj-rekap" || c.Caller != "user-1" {
			t.Errorf("dispatch not in agent project as owner: %+v", c)
		}
	}
	if dir.sessions[calls[1].SessionID] != "ag-1" {
		t.Errorf("session %q not created as the agent's: %v", calls[1].SessionID, dir.sessions)
	}
}

func TestAgentModel_ConversationKeyedPerAgent(t *testing.T) {
	stubModels(t)
	second := Agent{ID: "ag-9", OwnerUserID: "user-1", Handle: "second", ProjectID: "p2", RESTEnabled: true}
	withAgentDir(t, rekapAgent, second)
	ch, sent := newAgentTestChannel(t, true)
	h := http.HandlerFunc(ch.handleChatCompletions)
	for _, m := range []string{"agent:rekap", "agent:rekap", "agent:second"} {
		if rec := postJSON(t, h, "/", "good", agentChat(m, map[string]any{"conversation": "c1"})); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", m, rec.Code, rec.Body.String())
		}
	}
	var users []string
	notes := 0
	for _, c := range sent() {
		if c.Role == "user" {
			users = append(users, c.SessionID)
		} else {
			notes++
		}
	}
	if len(users) != 3 || users[0] != users[1] || users[0] == users[2] {
		t.Fatalf("one conversation should be one session per agent: %v", users)
	}
	if notes != 2 {
		t.Errorf("context note once per new session, got %d", notes)
	}
}

func TestAgentModel_ConnectionOff403(t *testing.T) {
	stubModels(t)
	withAgentDir(t, offAgent)
	ch, sent := newAgentTestChannel(t, true)
	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", agentChat("agent:quiet", nil))
	if rec.Code != http.StatusForbidden || errCode(t, rec) != "rest_connection_disabled" {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if len(sent()) != 0 {
		t.Error("nothing may be dispatched")
	}
}

func TestAgentModel_NotFound404(t *testing.T) {
	stubModels(t)
	withAgentDir(t, rekapAgent, pausedAgent, otherAgent)
	ch, sent := newAgentTestChannel(t, true)
	for _, m := range []string{"agent:theirs", "agent:paused", "agent:nobody", "agent:"} {
		rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", agentChat(m, nil))
		if rec.Code != http.StatusNotFound || errCode(t, rec) != "model_not_found" {
			t.Errorf("%s: got %d %s", m, rec.Code, rec.Body.String())
		}
	}
	if len(sent()) != 0 {
		t.Error("nothing may be dispatched")
	}
}

func TestAgentModel_NoDirectory404(t *testing.T) {
	stubModels(t)
	prev := agentDir
	agentDir = nil
	t.Cleanup(func() { agentDir = prev })
	ch, _ := newAgentTestChannel(t, true)
	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", agentChat("agent:rekap", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestAgentModel_StreamRejectedLikePlain(t *testing.T) {
	stubModels(t)
	withAgentDir(t, rekapAgent)
	ch, sent := newAgentTestChannel(t, true)
	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", agentChat("agent:rekap", map[string]any{"stream": true}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d", rec.Code)
	}
	if len(sent()) != 0 {
		t.Error("nothing may be dispatched")
	}
}

func TestAgentModel_Background(t *testing.T) {
	stubModels(t)
	withAgentDir(t, rekapAgent)
	ch, sent := newAgentTestChannel(t, true)
	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", agentChat("agent:rekap", map[string]any{"background": true}))
	var resp chatResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if rec.Code != http.StatusOK || resp.Status != "queued" {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if n := len(sent()); n != 2 {
		t.Errorf("want note + user turn, got %d", n)
	}
}

func TestAgentModel_PlainModelStillNeedsChannel(t *testing.T) {
	stubModels(t, "claude")
	withAgentDir(t, rekapAgent)
	ch, _ := newAgentTestChannel(t, false)
	rec := postJSON(t, http.HandlerFunc(ch.handleChatCompletions), "/", "good", agentChat("claude", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", rec.Code)
	}
}

func getModels(t *testing.T, ch *Channel) (int, []string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer good")
	rec := httptest.NewRecorder()
	ch.handleModels(rec, req)
	var body modelsListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	var ids []string
	for _, m := range body.Data {
		ids = append(ids, m.ID)
	}
	return rec.Code, ids
}

func TestModels_ListsOwnRESTAgents(t *testing.T) {
	stubModels(t, "claude")
	withAgentDir(t, rekapAgent, offAgent, pausedAgent, otherAgent)

	ch, _ := newAgentTestChannel(t, true)
	code, ids := getModels(t, ch)
	if code != http.StatusOK || strings.Join(ids, ",") != "claude,agent:rekap" {
		t.Fatalf("enabled channel: %d %v", code, ids)
	}

	off, _ := newAgentTestChannel(t, false)
	code, ids = getModels(t, off)
	if code != http.StatusOK || strings.Join(ids, ",") != "agent:rekap" {
		t.Fatalf("disabled channel: %d %v", code, ids)
	}
}

func TestModels_DisabledChannelNoAgents503(t *testing.T) {
	stubModels(t, "claude")
	withAgentDir(t, offAgent)
	ch, _ := newAgentTestChannel(t, false)
	if code, _ := getModels(t, ch); code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", code)
	}
}

func TestAgentConnStore(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&entity.AgentChannel{}); err != nil {
		t.Fatal(err)
	}
	st := NewAgentConnStore(db)
	if on, err := st.Enabled("a1"); err != nil || on {
		t.Fatalf("no row must read off: %v %v", on, err)
	}
	for _, step := range []bool{true, false, true} {
		if err := st.SetEnabled("a1", "u1", step); err != nil {
			t.Fatal(err)
		}
		if on, _ := st.Enabled("a1"); on != step {
			t.Fatalf("after SetEnabled(%v) read %v", step, on)
		}
	}
	_ = st.SetEnabled("a2", "u1", false)
	_ = st.SetEnabled("a3", "u2", true)
	set, err := st.EnabledAgents("u1")
	if err != nil || len(set) != 1 || !set["a1"] {
		t.Fatalf("enabled set: %v %v", set, err)
	}
	if err := st.Delete("a1"); err != nil {
		t.Fatal(err)
	}
	if on, _ := st.Enabled("a1"); on {
		t.Fatal("deleted connection still on")
	}
}
