package agents

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	"github.com/yogasw/wick/internal/entity"
)

// withAgentTelegramWorld adds an agent_channels table and a stub getMe that
// answers with the bot id in front of the token's colon; "0:…" is refused.
func withAgentTelegramWorld(t *testing.T) {
	t.Helper()
	withTeamWorld(t)
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"_tg?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&entity.AgentChannel{}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /bot<token>/getMe
		tok := strings.TrimPrefix(strings.Split(r.URL.Path, "/")[1], "bot")
		id := tok[:strings.Index(tok, ":")]
		w.Header().Set("Content-Type", "application/json")
		if id == "0" {
			_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"id":` + id + `,"is_bot":true,"first_name":"B","username":"bot` + id + `_bot"}}`))
	}))
	prevDB, prevEP := globalDB, telegramAPIEndpoint
	globalDB, telegramAPIEndpoint = db, srv.URL+"/bot%s/%s"
	t.Cleanup(func() { globalDB, telegramAPIEndpoint = prevDB, prevEP; srv.Close() })
}

func tgToken(id string) string { return id + ":AAH" + strings.Repeat("x", 30) }

func connectTelegram(t *testing.T, u *entity.User, agentID, token string) *httptest.ResponseRecorder {
	t.Helper()
	w, c := teamReq(t, u, http.MethodPut, "/api/team/agents/"+agentID+"/telegram", map[string]any{"bot_token": token}, map[string]string{"id": agentID})
	apiTeamAgentTelegramConnect(c)
	return w
}

// The token goes in once and never comes back, on connect or on read.
func TestAgentTelegramTokenNeverReturned(t *testing.T) {
	withAgentTelegramWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	u := &entity.User{ID: "u1"}
	tok := tgToken("111")
	w := connectTelegram(t, u, a.ID, tok)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), tok) || strings.Contains(w.Body.String(), "AAH") {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	var st AgentTelegramStatus
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if !st.Connected || st.BotID != "111" || st.BotUsername != "bot111_bot" || st.Link != "https://t.me/bot111_bot" {
		t.Fatalf("status = %+v", st)
	}
	wg, c := teamReq(t, u, http.MethodGet, "/api/team/agents/"+a.ID+"/telegram", nil, map[string]string{"id": a.ID})
	apiTeamAgentTelegramGet(c)
	if wg.Code != http.StatusOK || strings.Contains(wg.Body.String(), "AAH") {
		t.Fatalf("get: %d %s", wg.Code, wg.Body.String())
	}
	row, ok, _ := agentchannels.AgentTelegramRow(globalDB, a.ID)
	if !ok || !row.Enabled || !strings.Contains(row.Config, `"project_id":"p1"`) {
		t.Fatalf("row = %+v", row)
	}
	// A foreign owner sees nothing.
	wf, c := teamReq(t, &entity.User{ID: "u2"}, http.MethodGet, "/api/team/agents/"+a.ID+"/telegram", nil, map[string]string{"id": a.ID})
	apiTeamAgentTelegramGet(c)
	if wf.Code != http.StatusNotFound {
		t.Fatalf("foreign: %d", wf.Code)
	}
}

// One bot answers one agent: a second agent gets 409 until the first lets go.
func TestAgentTelegramRefusesBotOfAnotherAgent(t *testing.T) {
	withAgentTelegramWorld(t)
	seedTeamProject(t, "p1", "u1")
	seedTeamProject(t, "p2", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	b := seedTeamAgent(t, "u1", "loki", "p2")
	u := &entity.User{ID: "u1"}
	if w := connectTelegram(t, u, a.ID, tgToken("111")); w.Code != http.StatusOK {
		t.Fatalf("first: %d %s", w.Code, w.Body.String())
	}
	w := connectTelegram(t, u, b.ID, tgToken("111"))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "@rekap") {
		t.Fatalf("duplicate: %d %s", w.Code, w.Body.String())
	}
	if w := connectTelegram(t, u, a.ID, tgToken("111")); w.Code != http.StatusOK {
		t.Fatalf("reconnect own bot: %d %s", w.Code, w.Body.String())
	}
	wd, c := teamReq(t, u, http.MethodDelete, "/api/team/agents/"+a.ID+"/telegram", nil, map[string]string{"id": a.ID})
	apiTeamAgentTelegramDisconnect(c)
	if wd.Code != http.StatusOK {
		t.Fatal(wd.Code)
	}
	if _, ok, _ := agentchannels.AgentTelegramRow(globalDB, a.ID); ok {
		t.Fatal("disconnect kept the row")
	}
	if w := connectTelegram(t, u, b.ID, tgToken("111")); w.Code != http.StatusOK {
		t.Fatalf("after disconnect: %d %s", w.Code, w.Body.String())
	}
}

// Malformed and rejected tokens are 400, and the error never echoes them.
func TestAgentTelegramBadToken(t *testing.T) {
	withAgentTelegramWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	u := &entity.User{ID: "u1"}
	if w := connectTelegram(t, u, a.ID, "not-a-token"); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed: %d", w.Code)
	}
	tok := tgToken("0")
	w := connectTelegram(t, u, a.ID, tok)
	if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), tok) {
		t.Fatalf("rejected: %d %s", w.Code, w.Body.String())
	}
	if _, ok, _ := agentchannels.AgentTelegramRow(globalDB, a.ID); ok {
		t.Fatal("a rejected token was stored")
	}
}

// Test checks the stored token with getMe; without a connection it is 404.
func TestAgentTelegramTest(t *testing.T) {
	withAgentTelegramWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	u := &entity.User{ID: "u1"}
	call := func() (int, map[string]any) {
		w, c := teamReq(t, u, http.MethodPost, "/api/team/agents/"+a.ID+"/telegram/test", nil, map[string]string{"id": a.ID})
		apiTeamAgentTelegramTest(c)
		out := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	if code, _ := call(); code != http.StatusNotFound {
		t.Fatalf("no connection: %d", code)
	}
	connectTelegram(t, u, a.ID, tgToken("222"))
	code, out := call()
	if code != http.StatusOK || out["bot_username"] != "bot222_bot" || !strings.Contains(out["detail"].(string), "@bot222_bot") {
		t.Fatalf("test: %d %v", code, out)
	}
	removeAgentTelegram(a.ID)
	if code, _ := call(); code != http.StatusNotFound {
		t.Fatalf("after remove: %d", code)
	}
}
