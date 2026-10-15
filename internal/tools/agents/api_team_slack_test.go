package agents

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
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

// Rotating tokens: both prefixes pass, an xoxe.xapp- token needs its
// refresh token + client id + secret, and a blank one keeps the stored value.
func TestAgentSlackTokenErrorPrefixes(t *testing.T) {
	for _, c := range []struct {
		name, mode, bot, app, sig, ref, cid, csec string
		wantErr                                   string
	}{
		{"legacy socket", "socket", "xoxb-1", "xapp-1", "", "", "", "", ""},
		{"rotating bot", "socket", "xoxe.xoxb-1", "xapp-1", "", "", "", "", ""},
		{"rotating app complete", "socket", "xoxb-1", "xoxe.xapp-1", "", "xoxe-1-R", "1.2", "s", ""},
		{"rotating app no creds", "socket", "xoxb-1", "xoxe.xapp-1", "", "", "", "", "refresh token"},
		{"rotating app bad refresh", "socket", "xoxb-1", "xoxe.xapp-1", "", "xapp-1", "1.2", "s", "refresh token"},
		{"rotating app no secret", "socket", "xoxb-1", "xoxe.xapp-1", "", "xoxe-1-R", "1.2", "", "Client Secret"},
		{"bad bot", "socket", "xoxp-1", "xapp-1", "", "", "", "", "xoxb-"},
		{"bad app", "socket", "xoxb-1", "xoxb-2", "", "", "", "", "xoxe.xapp-"},
		{"http ignores app", "http", "xoxb-1", "", "sig", "", "", "", ""},
	} {
		got := agentSlackTokenError(c.mode, c.bot, c.app, c.sig, c.ref, c.cid, c.csec)
		if (c.wantErr == "") != (got == "") || !strings.Contains(got, c.wantErr) {
			t.Errorf("%s: got %q, want error containing %q", c.name, got, c.wantErr)
		}
	}
}

func TestAgentSlackRotatingConnect(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	u := &entity.User{ID: "u1"}
	if w := connectSlack(t, u, a.ID, map[string]any{"bot_token": "xoxb-x-B1", "app_token": "xoxe.xapp-1-SEKRIT"}); w.Code != http.StatusBadRequest {
		t.Fatalf("rotating without creds: %d %s", w.Code, w.Body.String())
	}
	w := connectSlack(t, u, a.ID, map[string]any{
		"bot_token": "xoxb-x-B1", "app_token": "xoxe.xapp-1-SEKRIT",
		"app_refresh_token": "xoxe-1-SEKRIT", "client_id": "111.222", "client_secret": "SEKRIT",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("rotating connect: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "SEKRIT") {
		t.Fatalf("connect response leaks a secret: %s", w.Body.String())
	}
	var st AgentSlackStatus
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if !st.Rotating || st.ClientID != "111.222" || !st.Secrets["app_refresh_token"] || !st.Secrets["client_secret"] {
		t.Fatalf("status = %+v", st)
	}
	// Re-saving with blanks keeps the stored rotation credentials.
	if w := connectSlack(t, u, a.ID, map[string]any{"bot_token": "xoxb-x-B1"}); w.Code != http.StatusOK {
		t.Fatalf("resave: %d %s", w.Code, w.Body.String())
	}
	// A NEW rotating app token without its own refresh token is refused:
	// the stored one belongs to the previous token.
	if w := connectSlack(t, u, a.ID, map[string]any{"bot_token": "xoxb-x-B1", "app_token": "xoxe.xapp-1-OTHER"}); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "together with its refresh token") {
		t.Fatalf("new rotating token, blank refresh: %d %s", w.Code, w.Body.String())
	}
	// Switching back to a legacy token drops the rotation settings.
	w = connectSlack(t, u, a.ID, map[string]any{"bot_token": "xoxb-x-B1", "app_token": "xapp-1-LEGACY"})
	if w.Code != http.StatusOK {
		t.Fatalf("legacy switch: %d %s", w.Code, w.Body.String())
	}
	st = AgentSlackStatus{}
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if st.Rotating || st.ClientID != "" || st.Secrets["app_refresh_token"] || st.Secrets["client_secret"] || st.AppTokenExpiresAt != "" {
		t.Fatalf("legacy status = %+v", st)
	}
}

// An options save merges only its own keys: a token rotation stored after
// the request was read is not rolled back.
func TestAgentSlackOptionsKeepRotatedToken(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	u := &entity.User{ID: "u1"}
	if w := connectSlack(t, u, a.ID, map[string]any{
		"bot_token": "xoxb-x-B1", "app_token": "xoxe.xapp-1-A",
		"app_refresh_token": "xoxe-1-R", "client_id": "1.2", "client_secret": "s",
	}); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	if err := persistAgentSlackAppToken(a.ID)("xoxe.xapp-1-B", "xoxe-1-R2", time.Now().Add(12*time.Hour)); err != nil {
		t.Fatal(err)
	}
	w, c := teamReq(t, u, http.MethodPatch, "/api/team/agents/"+a.ID+"/slack", map[string]any{"dm_main_chat": false}, map[string]string{"id": a.ID})
	apiTeamAgentSlackOptions(c)
	if w.Code != http.StatusOK {
		t.Fatalf("options: %d %s", w.Code, w.Body.String())
	}
	cfg, _, _ := agentSlackStore().LoadSlackForAgent(a.ID)
	if cfg.AppToken != "xoxe.xapp-1-B" || cfg.AppRefreshToken != "xoxe-1-R2" {
		t.Fatalf("options save rolled the token back: %s / %s", tokenHead(cfg.AppToken), tokenHead(cfg.AppRefreshToken))
	}
	m, _ := agentchannels.AgentSlackConfig(globalDB, a.ID)
	if m[agentSlackKeyDMMain] != "false" {
		t.Fatalf("option not saved: %v", m[agentSlackKeyDMMain])
	}
	got, _, _, _ := loadAgentSlackAppToken(a.ID)()
	if got != "xoxe.xapp-1-B" {
		t.Fatalf("loader = %q", got)
	}
}

func tokenHead(t string) string { return strings.SplitN(t, "-", 3)[0] + "-…" + t[max(len(t)-2, 0):] }

// The rotated pair lands in the agent's row and the row stays enabled.
func TestPersistAgentSlackAppToken(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	u := &entity.User{ID: "u1"}
	if w := connectSlack(t, u, a.ID, map[string]any{
		"bot_token": "xoxb-x-B1", "app_token": "xoxe.xapp-1-A",
		"app_refresh_token": "xoxe-1-R", "client_id": "1.2", "client_secret": "s",
	}); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	exp := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	if err := persistAgentSlackAppToken(a.ID)("xoxe.xapp-1-B", "xoxe-1-R2", exp); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := agentSlackStore().LoadSlackForAgent(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppToken != "xoxe.xapp-1-B" || cfg.AppRefreshToken != "xoxe-1-R2" || cfg.AppTokenExpiresAt != "2026-10-15T00:00:00Z" || cfg.ClientID != "1.2" {
		t.Fatalf("cfg = %+v", cfg)
	}
	row, _, _ := agentchannels.AgentSlackRow(globalDB, a.ID)
	if !row.Enabled {
		t.Fatal("persist turned the connection off")
	}
}

// Global save: a different rotating app token clears the old lineage's
// refresh token and expiry; re-saving the same token changes nothing else.
func TestSlackAppTokenPasteKeys(t *testing.T) {
	if kv := slackAppTokenPasteKeys("xoxe.xapp-1-A", "xoxe.xapp-1-A"); len(kv) != 0 {
		t.Fatalf("same token: %v", kv)
	}
	kv := slackAppTokenPasteKeys("xoxe.xapp-1-B", "xoxe.xapp-1-A")
	if v, ok := kv["app_refresh_token"]; !ok || v != "" {
		t.Fatalf("new rotating token keeps the old refresh token: %v", kv)
	}
	if v, ok := kv["app_token_expires_at"]; !ok || v != "" {
		t.Fatalf("expiry not cleared: %v", kv)
	}
	kv = slackAppTokenPasteKeys("xapp-1-LEGACY", "xoxe.xapp-1-A")
	if _, ok := kv["app_refresh_token"]; ok {
		t.Fatalf("legacy token touched the refresh token: %v", kv)
	}
}
