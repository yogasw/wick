package agents

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentslack "github.com/yogasw/wick/internal/agents/channels/slack"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/entity"
)

// withInstantWorld is withAgentSlackWorld plus a running shared Slack
// instance under the App Owner key, whose token has scopes.
func withInstantWorld(t *testing.T, scopes ...string) *agentslack.Channel {
	t.Helper()
	withAgentSlackWorld(t)
	reg := agentchannels.NewRegistry()
	ch := agentslack.NewWithOwnerCached(agentconfig.SlackChannelConfig{Mode: "socket", BotToken: "xoxb-x-SH", AppToken: "xapp-x"}, "", "USH", "shared-bot", "T")
	reg.AddKeyed(instantOwnerInstance, ch, nil)
	prevCh, prevScopes := globalChannels, instantScopesOf
	globalChannels = reg
	instantScopesOf = func(*agentslack.Channel) ([]string, error) { return scopes, nil }
	instantCache.invalidate()
	t.Cleanup(func() { globalChannels, instantScopesOf = prevCh, prevScopes; instantCache.invalidate() })
	return ch
}

func putInstant(t *testing.T, u *entity.User, agentID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	w, c := teamReq(t, u, http.MethodPut, "/api/team/agents/"+agentID+"/slack/instant", body, map[string]string{"id": agentID})
	apiTeamAgentSlackInstantPut(c)
	return w
}

func instantToken(t *testing.T, agentID string) string {
	t.Helper()
	_, cfg, found, err := instantRow(globalDB, agentID)
	if err != nil || !found {
		t.Fatalf("no instant row for %s: %v", agentID, err)
	}
	return cfg.AvatarToken
}

func TestInstantPutStoresAndNeverReturnsToken(t *testing.T) {
	withInstantWorld(t, "chat:write", "chat:write.customize")
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "nanda", "p1")
	u := &entity.User{ID: "u1"}

	w := putInstant(t, u, a.ID, map[string]any{
		"shared_channel": instantOwnerInstance, "prefix_enabled": true,
		"bound_channels": []string{"C0123ABCD", "https://x.slack.com/archives/C0999ZZZZ/p1", "c0123abcd", ""},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	var st AgentSlackInstantStatus
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if !st.Enabled || strings.Join(st.BoundChannels, ",") != "C0123ABCD,C0999ZZZZ" || !st.PrefixEnabled || st.CustomizeScope != "ok" {
		t.Fatalf("status = %+v", st)
	}
	tok := instantToken(t, a.ID)
	if len(tok) < 32 || strings.Contains(w.Body.String(), tok) {
		t.Fatalf("token weak or leaked: len %d, body %s", len(tok), w.Body)
	}
	// Omitted fields keep their value.
	if w := putInstant(t, u, a.ID, map[string]any{"prefix_enabled": false}); w.Code != http.StatusOK {
		t.Fatalf("partial put: %d %s", w.Code, w.Body)
	}
	_, cfg, _, _ := instantRow(globalDB, a.ID)
	if len(cfg.BoundChannels) != 2 || cfg.PrefixEnabled || cfg.AvatarToken != tok {
		t.Fatalf("partial put changed more than asked: %+v", cfg)
	}
}

func TestInstantChannelConflictIs409WithOwner(t *testing.T) {
	withInstantWorld(t)
	seedTeamProject(t, "p1", "u1")
	seedTeamProject(t, "p2", "u2")
	a := seedTeamAgent(t, "u1", "nanda", "p1")
	b := seedTeamAgent(t, "u2", "critic", "p2")
	if w := putInstant(t, &entity.User{ID: "u1"}, a.ID, map[string]any{"shared_channel": instantOwnerInstance, "bound_channels": []string{"C0123ABCD"}}); w.Code != http.StatusOK {
		t.Fatalf("first bind: %d %s", w.Code, w.Body)
	}
	w := putInstant(t, &entity.User{ID: "u2"}, b.ID, map[string]any{"shared_channel": instantOwnerInstance, "bound_channels": []string{"C0123ABCD"}})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "@nanda") {
		t.Fatalf("conflict = %d %s", w.Code, w.Body)
	}
	// Re-saving the owner's own binding is no conflict.
	if w := putInstant(t, &entity.User{ID: "u1"}, a.ID, map[string]any{"bound_channels": []string{"C0123ABCD"}}); w.Code != http.StatusOK {
		t.Fatalf("self re-save: %d %s", w.Code, w.Body)
	}
}

func TestInstantOnlyOwnerAndValidInput(t *testing.T) {
	withInstantWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "nanda", "p1")
	if w := putInstant(t, &entity.User{ID: "u2"}, a.ID, map[string]any{"shared_channel": instantOwnerInstance}); w.Code != http.StatusNotFound {
		t.Fatalf("stranger put = %d, want 404", w.Code)
	}
	u := &entity.User{ID: "u1"}
	for name, body := range map[string]map[string]any{
		"no app":      {},
		"foreign app": {"shared_channel": "slack:u9"},
		"custom bot":  {"shared_channel": "slack-agent:x"},
		"bad channel": {"shared_channel": instantOwnerInstance, "bound_channels": []string{"general"}},
		"missing app": {"shared_channel": "slack:u1"},
	} {
		if w := putInstant(t, u, a.ID, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", name, w.Code, w.Body)
		}
	}
}

// One Slack mode at a time: Custom refuses while Instant is on and the
// other way round.
func TestInstantAndCustomAreExclusive(t *testing.T) {
	withInstantWorld(t)
	seedTeamProject(t, "p1", "u1")
	seedTeamProject(t, "p2", "u1")
	u := &entity.User{ID: "u1"}
	a := seedTeamAgent(t, "u1", "nanda", "p1")
	if w := putInstant(t, u, a.ID, map[string]any{"shared_channel": instantOwnerInstance}); w.Code != http.StatusOK {
		t.Fatalf("instant: %d %s", w.Code, w.Body)
	}
	// No registry from here on: a Custom connect would start a real bot.
	globalChannels = nil
	if w := connectSlack(t, u, a.ID, map[string]any{"bot_token": "xoxb-1-B1", "app_token": "xapp-1"}); w.Code != http.StatusConflict {
		t.Fatalf("custom over instant = %d %s", w.Code, w.Body)
	}
	b := seedTeamAgent(t, "u1", "critic", "p2")
	if w := connectSlack(t, u, b.ID, map[string]any{"bot_token": "xoxb-1-B2", "app_token": "xapp-1"}); w.Code != http.StatusOK {
		t.Fatalf("custom: %d %s", w.Code, w.Body)
	}
	if w := putInstant(t, u, b.ID, map[string]any{"shared_channel": instantOwnerInstance}); w.Code != http.StatusConflict {
		t.Fatalf("instant over custom = %d %s", w.Code, w.Body)
	}
}

func TestInstantStatusWarnsWithoutCustomizeScope(t *testing.T) {
	withInstantWorld(t, "chat:write")
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "nanda", "p1")
	w := putInstant(t, &entity.User{ID: "u1"}, a.ID, map[string]any{"shared_channel": instantOwnerInstance, "bound_channels": []string{"C0123ABCD"}})
	var st AgentSlackInstantStatus
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if st.CustomizeScope != "missing" || !strings.Contains(strings.Join(st.Warnings, "|"), "chat:write.customize") {
		t.Fatalf("status = %+v", st)
	}
}

// The router sees the agent on the shared instance only, serves its avatar
// by token, and a rotation kills the old URL.
func TestInstantRouterAndAvatarRotation(t *testing.T) {
	shared := withInstantWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "nanda", "p1")
	u := &entity.User{ID: "u1"}
	if w := putInstant(t, u, a.ID, map[string]any{"shared_channel": instantOwnerInstance, "bound_channels": []string{"C0123ABCD"}, "prefix_enabled": true}); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}

	r := instantRouter{}
	agents := r.Agents(shared)
	if len(agents) != 1 || agents[0].AgentID != a.ID || agents[0].ProjectID != "p1" || agents[0].Handle != "nanda" {
		t.Fatalf("agents = %+v", agents)
	}
	if got := r.Agents(agentslack.New(agentconfig.SlackChannelConfig{})); len(got) != 0 {
		t.Fatalf("unregistered instance sees %d agents", len(got))
	}
	old := instantToken(t, a.ID)
	if png, etag, ok := r.AvatarPNG(old); !ok || len(png) == 0 || etag == "" {
		t.Fatal("avatar not served for a valid token")
	}
	if _, _, ok := r.AvatarPNG("guess"); ok {
		t.Fatal("avatar served for a wrong token")
	}

	w, c := teamReq(t, u, http.MethodPost, "/x", nil, map[string]string{"id": a.ID})
	apiTeamAgentSlackInstantRotate(c)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), old) {
		t.Fatalf("rotate: %d %s", w.Code, w.Body)
	}
	fresh := instantToken(t, a.ID)
	if fresh == old {
		t.Fatal("token not rotated")
	}
	if _, _, ok := r.AvatarPNG(old); ok {
		t.Fatal("old token still serves")
	}
	if _, _, ok := r.AvatarPNG(fresh); !ok {
		t.Fatal("new token does not serve")
	}

	w, c = teamReq(t, u, http.MethodDelete, "/x", nil, map[string]string{"id": a.ID})
	apiTeamAgentSlackInstantDelete(c)
	if w.Code != http.StatusOK || len(r.Agents(shared)) != 0 {
		t.Fatalf("delete: %d, agents left %d", w.Code, len(r.Agents(shared)))
	}
}

func TestNormalizeSlackChannel(t *testing.T) {
	for in, want := range map[string]string{
		"C0123ABCD": "C0123ABCD", "#c0123abcd": "C0123ABCD",
		"https://acme.slack.com/archives/G0123ABCD":        "G0123ABCD",
		"https://app.slack.com/client/T0001AAAA/C0123ABCD": "C0123ABCD",
	} {
		if got, ok := normalizeSlackChannel(in); !ok || got != want {
			t.Errorf("%q = %q %v, want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"general", "U0123ABCD", "C1"} {
		if _, ok := normalizeSlackChannel(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}
