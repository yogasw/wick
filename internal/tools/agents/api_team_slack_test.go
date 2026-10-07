package agents

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// withAgentSlackWorld adds an agent_channels table and a stub auth.test
// that answers with the bot id named by the token's suffix.
func withAgentSlackWorld(t *testing.T) {
	t.Helper()
	withTeamWorld(t)
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"_ch?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&entity.AgentChannel{}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		tok := r.Form.Get("token")
		bot := tok[strings.LastIndex(tok, "-")+1:]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"user_id":"U` + bot + `","bot_id":"` + bot + `","user":"bot","team":"T"}`))
	}))
	prevDB, prevURL := globalDB, slackAPIURL
	globalDB, slackAPIURL = db, srv.URL+"/"
	t.Cleanup(func() { globalDB, slackAPIURL = prevDB, prevURL; srv.Close() })
}

func connectSlack(t *testing.T, u *entity.User, agentID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	w, c := teamReq(t, u, http.MethodPut, "/api/team/agents/"+agentID+"/slack", body, map[string]string{"id": agentID})
	apiTeamAgentSlackConnect(c)
	return w
}

// Secrets go in, only "set" comes back — on connect and on every read.
func TestAgentSlackSecretsNeverReturned(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	u := &entity.User{ID: "u1"}
	w := connectSlack(t, u, a.ID, map[string]any{"bot_token": "xoxb-SEKRIT-B1", "app_token": "xapp-SEKRIT-1"})
	if w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "SEKRIT") {
		t.Fatalf("connect response leaks a secret: %s", w.Body.String())
	}
	w2, c := teamReq(t, u, http.MethodGet, "/api/team/agents/"+a.ID+"/slack", nil, map[string]string{"id": a.ID})
	apiTeamAgentSlackGet(c)
	if strings.Contains(w2.Body.String(), "SEKRIT") {
		t.Fatalf("status leaks a secret: %s", w2.Body.String())
	}
	var st AgentSlackStatus
	_ = json.Unmarshal(w2.Body.Bytes(), &st)
	if !st.Connected || !st.Secrets["bot_token"] || !st.Secrets["app_token"] || st.BotID != "B1" || !st.DMMainChat {
		t.Fatalf("status = %+v", st)
	}
	// Options save without re-pasting tokens.
	w3, c := teamReq(t, u, http.MethodPatch, "/api/team/agents/"+a.ID+"/slack", map[string]any{"dm_main_chat": false}, map[string]string{"id": a.ID})
	apiTeamAgentSlackOptions(c)
	_ = json.Unmarshal(w3.Body.Bytes(), &st)
	if w3.Code != http.StatusOK || st.DMMainChat || !st.Secrets["bot_token"] {
		t.Fatalf("options: %d %+v", w3.Code, st)
	}
}

// One Slack app = one agent: the second agent pasting the same bot is
// refused with a clear 409, and a socket connection without xapp is a 400.
func TestAgentSlackRefusesBotOfAnotherAgent(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	seedTeamProject(t, "p2", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	b := seedTeamAgent(t, "u1", "loki", "p2")
	u := &entity.User{ID: "u1"}
	if w := connectSlack(t, u, a.ID, map[string]any{"bot_token": "xoxb-x-B1", "app_token": "xapp-1"}); w.Code != http.StatusOK {
		t.Fatalf("first: %d %s", w.Code, w.Body.String())
	}
	w := connectSlack(t, u, b.ID, map[string]any{"bot_token": "xoxb-y-B1", "app_token": "xapp-2"})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "@rekap") {
		t.Fatalf("duplicate: %d %s", w.Code, w.Body.String())
	}
	// Reconnecting the same agent with its own bot is fine.
	if w := connectSlack(t, u, a.ID, map[string]any{"bot_token": "xoxb-z-B1"}); w.Code != http.StatusOK {
		t.Fatalf("reconnect: %d %s", w.Code, w.Body.String())
	}
	if w := connectSlack(t, u, b.ID, map[string]any{"bot_token": "xoxb-y-B2"}); w.Code != http.StatusBadRequest {
		t.Fatalf("missing xapp: %d", w.Code)
	}
	// Disconnect drops the row.
	wd, c := teamReq(t, u, http.MethodDelete, "/api/team/agents/"+a.ID+"/slack", nil, map[string]string{"id": a.ID})
	apiTeamAgentSlackDisconnect(c)
	if wd.Code != http.StatusOK {
		t.Fatal(wd.Code)
	}
	if w := connectSlack(t, u, b.ID, map[string]any{"bot_token": "xoxb-y-B1", "app_token": "xapp-2"}); w.Code != http.StatusOK {
		t.Fatalf("after disconnect: %d %s", w.Code, w.Body.String())
	}
}

// The manifest endpoint carries the agent's prompts and a create link.
func TestAgentSlackManifestAndPrompts(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	u := &entity.User{ID: "u1"}
	w, c := teamReq(t, u, http.MethodPatch, "/api/team/agents/"+a.ID, map[string]any{
		"suggested_prompts": []map[string]string{{"title": "Recap today", "message": "Recap today's issues"}},
	}, map[string]string{"id": a.ID})
	apiTeamAgentUpdate(c)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Recap today") {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	five := make([]map[string]string, 5)
	for i := range five {
		five[i] = map[string]string{"title": "t", "message": "m"}
	}
	w, c = teamReq(t, u, http.MethodPatch, "/api/team/agents/"+a.ID, map[string]any{"suggested_prompts": five}, map[string]string{"id": a.ID})
	apiTeamAgentUpdate(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("five prompts: %d", w.Code)
	}
	w, c = teamReq(t, u, http.MethodGet, "/api/team/agents/"+a.ID+"/slack/manifest", nil, map[string]string{"id": a.ID})
	apiTeamAgentSlackManifest(c)
	var out struct {
		Manifest  map[string]any `json:"manifest"`
		CreateURL string         `json:"create_url"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != http.StatusOK || !strings.HasPrefix(out.CreateURL, "https://api.slack.com/apps?new_app=1&manifest_json=") ||
		!strings.Contains(w.Body.String(), "Recap today") {
		t.Fatalf("manifest: %d %s", w.Code, w.Body.String())
	}
}
