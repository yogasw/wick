package agents

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	"github.com/yogasw/wick/internal/entity"
)

// withOwnerLookup seeds owner u1 with an email and swaps the Slack stub for
// one that also answers users.lookupByEmail. found=false makes Slack answer
// users_not_found.
func withOwnerLookup(t *testing.T, found bool) {
	t.Helper()
	if err := globalDB.AutoMigrate(&entity.User{}); err != nil {
		t.Fatal(err)
	}
	if err := globalDB.Create(&entity.User{ID: "u1", Email: "owner@example.test", Name: "Owner"}).Error; err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "users.lookupByEmail") {
			if !found || r.Form.Get("email") != "owner@example.test" {
				_, _ = w.Write([]byte(`{"ok":false,"error":"users_not_found"}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"UOWNER","name":"owner","real_name":"Owner Person"}}`))
			return
		}
		tok := r.Form.Get("token")
		bot := tok[strings.LastIndex(tok, "-")+1:]
		_, _ = w.Write([]byte(`{"ok":true,"user_id":"U` + bot + `","bot_id":"` + bot + `","user":"bot","team":"T"}`))
	}))
	prev := slackAPIURL
	slackAPIURL = srv.URL + "/"
	t.Cleanup(func() { slackAPIURL = prev; srv.Close() })
}

func getSlackSettings(t *testing.T, u *entity.User, agentID string) AgentSlackSettings {
	t.Helper()
	w, c := teamReq(t, u, http.MethodGet, "/api/team/agents/"+agentID+"/slack/settings", nil, map[string]string{"id": agentID})
	apiTeamAgentSlackSettingsGet(c)
	if w.Code != http.StatusOK {
		t.Fatalf("settings: %d %s", w.Code, w.Body.String())
	}
	var st AgentSlackSettings
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	return st
}

func settingValue(st AgentSlackSettings, key string) (string, bool) {
	for _, f := range st.Fields {
		if f.Key == key {
			return f.Value, true
		}
	}
	return "", false
}

// A brand-new app is "Only me": the owner, found by email in the app's
// workspace, is the only allowed user.
func TestAgentSlackNewAppDefaultsToOwnerOnly(t *testing.T) {
	withAgentSlackWorld(t)
	withOwnerLookup(t, true)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "captain", "p1")
	u := &entity.User{ID: "u1"}
	if w := connectSlack(t, u, a.ID, map[string]any{"bot_token": "xoxb-x-B1", "app_token": "xapp-1"}); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	st := getSlackSettings(t, u, a.ID)
	if mode, _ := settingValue(st, "users_mode"); mode != "whitelist" {
		t.Fatalf("users_mode = %q, want whitelist", mode)
	}
	if list, _ := settingValue(st, "allowed_users"); !strings.Contains(list, `"UOWNER"`) {
		t.Fatalf("allowed_users = %q, want the owner", list)
	}
	if st.OwnerID != "UOWNER" || st.OwnerName != "Owner Person" {
		t.Fatalf("owner = %q/%q", st.OwnerID, st.OwnerName)
	}
}

// When Slack cannot find the owner the app stays open (no guessing by
// name) and the settings carry no owner, so the card asks for a manual pick.
func TestAgentSlackOwnerNotFoundStaysOpen(t *testing.T) {
	withAgentSlackWorld(t)
	withOwnerLookup(t, false)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "captain", "p1")
	u := &entity.User{ID: "u1"}
	if w := connectSlack(t, u, a.ID, map[string]any{"bot_token": "xoxb-x-B1", "app_token": "xapp-1"}); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	st := getSlackSettings(t, u, a.ID)
	if mode, _ := settingValue(st, "users_mode"); mode != "all" {
		t.Fatalf("users_mode = %q, want all", mode)
	}
	if st.OwnerID != "" {
		t.Fatalf("owner resolved without Slack: %q", st.OwnerID)
	}
}

// A connection that already existed keeps its access on reconnect.
func TestAgentSlackExistingConnectionKeepsAccess(t *testing.T) {
	withAgentSlackWorld(t)
	withOwnerLookup(t, true)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "captain", "p1")
	if err := agentchannels.SaveAgentSlack(globalDB, a.ID, "u1", map[string]string{"bot_token": "xoxb-old-B1", "app_token": "xapp-1", "mode": "socket"}, true); err != nil {
		t.Fatal(err)
	}
	u := &entity.User{ID: "u1"}
	if w := connectSlack(t, u, a.ID, map[string]any{"bot_token": "xoxb-x-B1", "app_token": "xapp-1"}); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	if mode, _ := settingValue(getSlackSettings(t, u, a.ID), "users_mode"); mode != "all" {
		t.Fatalf("existing connection changed to %q", mode)
	}
}

// The card shows exactly the four setting groups, and only their keys save.
func TestAgentSlackSettingsGroupsAndPatch(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "captain", "p1")
	u := &entity.User{ID: "u1"}
	if w := connectSlack(t, u, a.ID, map[string]any{"bot_token": "xoxb-x-B1", "app_token": "xapp-1"}); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	st := getSlackSettings(t, u, a.ID)
	groups := map[string]bool{}
	for _, f := range st.Fields {
		title, _, _ := strings.Cut(f.Group, "|")
		groups[title] = true
		if f.IsSecret {
			t.Errorf("secret field %q reached the card", f.Key)
		}
	}
	for _, g := range []string{"Access Control", "Agent Behaviour", "Reaction Auto-Reply", "Approval Gates"} {
		if !groups[g] {
			t.Errorf("group %q missing", g)
		}
	}
	if groups["Connection"] || groups["Routing"] || len(groups) != 4 {
		t.Errorf("groups = %v, want exactly the four", groups)
	}
	if v, ok := settingValue(st, "bots_mode"); !ok || v != "none" {
		t.Errorf("bots_mode = %q (present %v), want none", v, ok)
	}

	patch := func(key, value string) *httptest.ResponseRecorder {
		w, c := teamReq(t, u, http.MethodPatch, "/api/team/agents/"+a.ID+"/slack/settings", map[string]any{"key": key, "value": value}, map[string]string{"id": a.ID})
		apiTeamAgentSlackSettingsPatch(c)
		return w
	}
	if w := patch("bot_token", "xoxb-evil"); w.Code != http.StatusBadRequest {
		t.Fatalf("patching a token: %d, want 400", w.Code)
	}
	if w := patch("bots_mode", "whitelist"); w.Code != http.StatusOK {
		t.Fatalf("patch bots_mode: %d %s", w.Code, w.Body.String())
	}
	if v, _ := settingValue(getSlackSettings(t, u, a.ID), "bots_mode"); v != "whitelist" {
		t.Fatalf("bots_mode after patch = %q", v)
	}
}
