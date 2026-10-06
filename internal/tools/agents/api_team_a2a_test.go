package agents

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/a2aserver"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

func withAgentA2AWorld(t *testing.T) {
	t.Helper()
	withTeamWorld(t)
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"_a2a?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&entity.AgentChannel{}); err != nil {
		t.Fatal(err)
	}
	prev := globalDB
	globalDB = db
	t.Cleanup(func() { globalDB = prev })
}

func a2aCall(t *testing.T, u *entity.User, method, path string, body map[string]any, h func(*tool.Ctx)) (int, AgentA2AStatus, string) {
	t.Helper()
	id := strings.Split(strings.TrimPrefix(path, "/api/team/agents/"), "/")[0]
	w, c := teamReq(t, u, method, path, body, map[string]string{"id": id})
	h(c)
	var st AgentA2AStatus
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	return w.Code, st, w.Body.String()
}

func TestAgentA2AKeyShownOnce(t *testing.T) {
	withAgentA2AWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	u := &entity.User{ID: "u1"}
	base := "/api/team/agents/" + a.ID + "/a2a"

	code, st, _ := a2aCall(t, u, http.MethodGet, base, nil, apiTeamAgentA2AGet)
	if code != http.StatusOK || st.Enabled || st.KeySet || !strings.HasSuffix(st.EndpointURL, a2aserver.EndpointPath(a.ID)) {
		t.Fatalf("initial: %d %+v", code, st)
	}

	code, st, _ = a2aCall(t, u, http.MethodPut, base, map[string]any{"enabled": true}, apiTeamAgentA2APut)
	if code != http.StatusOK || !st.Enabled || !st.KeySet || !strings.HasPrefix(st.APIKey, "wa2a_") {
		t.Fatalf("enable: %d %+v", code, st)
	}
	key := st.APIKey
	conn, _, _ := a2aStore().Load(a.ID)
	if !a2aserver.VerifyKey(key, conn.KeyHash) {
		t.Fatal("stored hash does not match the shown key")
	}

	// Later reads and saves never carry the key or its hash.
	for _, step := range []struct {
		method string
		body   map[string]any
		h      func(*tool.Ctx)
	}{
		{http.MethodGet, nil, apiTeamAgentA2AGet},
		{http.MethodPut, map[string]any{"public_card": true, "allowed_callers": []string{" u2 ", "u2", ""}}, apiTeamAgentA2APut},
	} {
		_, st, raw := a2aCall(t, u, step.method, base, step.body, step.h)
		if st.APIKey != "" || strings.Contains(raw, key) || strings.Contains(raw, conn.KeyHash) {
			t.Fatalf("%s leaks the key: %s", step.method, raw)
		}
	}
	_, st, _ = a2aCall(t, u, http.MethodGet, base, nil, apiTeamAgentA2AGet)
	if !st.PublicCard || len(st.AllowedCallers) != 1 || st.AllowedCallers[0] != "u2" {
		t.Fatalf("options: %+v", st)
	}

	_, st, _ = a2aCall(t, u, http.MethodPost, base+"/rotate", nil, apiTeamAgentA2ARotate)
	if st.APIKey == "" || st.APIKey == key {
		t.Fatalf("rotate: %+v", st)
	}
	conn, _, _ = a2aStore().Load(a.ID)
	if a2aserver.VerifyKey(key, conn.KeyHash) || !a2aserver.VerifyKey(st.APIKey, conn.KeyHash) {
		t.Fatal("rotate kept the old key alive")
	}

	_, st, _ = a2aCall(t, u, http.MethodPost, base+"/revoke", nil, apiTeamAgentA2ARevoke)
	if st.KeySet || st.APIKey != "" || !st.Enabled {
		t.Fatalf("revoke: %+v", st)
	}
}

func TestAgentA2AOwnerOnly(t *testing.T) {
	withAgentA2AWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	code, _, _ := a2aCall(t, &entity.User{ID: "u2"}, http.MethodPut, "/api/team/agents/"+a.ID+"/a2a", map[string]any{"enabled": true}, apiTeamAgentA2APut)
	if code == http.StatusOK {
		t.Fatal("another user enabled the agent's A2A connection")
	}
	if _, found, _ := a2aStore().Load(a.ID); found {
		t.Fatal("connection row written for a stranger")
	}
}

func TestA2AAgentFromPersona(t *testing.T) {
	withAgentA2AWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	got, ok := a2aDirectory{}.Agent(t.Context(), a.ID)
	if !ok || got.OwnerUserID != "u1" || got.ProjectID != "p1" || got.Name == "" {
		t.Fatalf("agent = %+v %v", got, ok)
	}
	if _, ok := (a2aDirectory{}).Agent(t.Context(), "missing"); ok {
		t.Fatal("missing agent resolved")
	}
}
