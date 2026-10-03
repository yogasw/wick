package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/channels/rest"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

func restCall(t *testing.T, u *entity.User, method, path string, body map[string]any, h func(*tool.Ctx)) (int, string) {
	t.Helper()
	id := strings.Split(strings.TrimPrefix(path, "/api/team/agents/"), "/")[0]
	w, c := teamReq(t, u, method, path, body, map[string]string{"id": id})
	h(c)
	return w.Code, w.Body.String()
}

func TestAgentRESTToggle(t *testing.T) {
	withAgentA2AWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	u := &entity.User{ID: "u1"}
	base := "/api/team/agents/" + a.ID + "/rest"

	code, body := restCall(t, u, http.MethodGet, base, nil, apiTeamAgentRESTGet)
	var st AgentRESTStatus
	_ = json.Unmarshal([]byte(body), &st)
	if code != http.StatusOK || st.Enabled || st.Model != "agent:rekap" ||
		!strings.HasSuffix(st.BaseURL, restBasePath) || !strings.HasSuffix(st.TokensURL, tokensPagePath) {
		t.Fatalf("initial: %d %s", code, body)
	}
	if strings.Contains(body, "wick_pat_") || strings.Contains(body, "token\"") {
		t.Fatalf("status must carry no token: %s", body)
	}

	if a2, ok := RESTDirectory().AgentByHandle(context.Background(), "u1", "rekap"); !ok || a2.RESTEnabled {
		t.Fatalf("default must be off: %+v %v", a2, ok)
	}
	if code, body = restCall(t, u, http.MethodPut, base, map[string]any{"enabled": true}, apiTeamAgentRESTPut); code != http.StatusOK {
		t.Fatalf("enable: %d %s", code, body)
	}
	a2, ok := RESTDirectory().AgentByHandle(context.Background(), "u1", "rekap")
	if !ok || !a2.RESTEnabled || a2.ID != a.ID || a2.ProjectID != "p1" {
		t.Fatalf("after enable: %+v %v", a2, ok)
	}
	if list := RESTDirectory().Agents(context.Background(), "u1"); len(list) != 1 || !list[0].RESTEnabled {
		t.Fatalf("agents: %+v", list)
	}
	if _, ok := RESTDirectory().AgentByHandle(context.Background(), "u2", "rekap"); ok {
		t.Fatal("another owner must not resolve the handle")
	}

	if code, _ = restCall(t, u, http.MethodPut, base, map[string]any{}, apiTeamAgentRESTPut); code != http.StatusBadRequest {
		t.Fatalf("missing enabled: %d", code)
	}
	if code, _ = restCall(t, &entity.User{ID: "u2"}, http.MethodPut, base, map[string]any{"enabled": false}, apiTeamAgentRESTPut); code != http.StatusNotFound {
		t.Fatalf("foreign owner: %d", code)
	}
	if on, _ := restConnStore().Enabled(a.ID); !on {
		t.Fatal("a foreign request must not change the toggle")
	}

	restCall(t, u, http.MethodPut, base, map[string]any{"enabled": false}, apiTeamAgentRESTPut)
	if a2, _ := RESTDirectory().AgentByHandle(context.Background(), "u1", "rekap"); a2.RESTEnabled {
		t.Fatal("disable did not stick")
	}

	removeAgentREST(a.ID)
	if on, _ := restConnStore().Enabled(a.ID); on {
		t.Fatal("removed connection still on")
	}
}

func TestAgentRESTTest(t *testing.T) {
	withAgentA2AWorld(t)
	prev := rest.AgentsWired()
	rest.SetAgentDirectory(RESTDirectory())
	t.Cleanup(func() {
		if !prev {
			rest.SetAgentDirectory(nil)
		}
	})
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	u := &entity.User{ID: "u1"}
	base := "/api/team/agents/" + a.ID + "/rest"

	var res RESTTestResult
	_, body := restCall(t, u, http.MethodPost, base+"/test", nil, apiTeamAgentRESTTest)
	_ = json.Unmarshal([]byte(body), &res)
	if res.OK || res.Model != "agent:rekap" || !strings.Contains(res.Detail, "off") {
		t.Fatalf("off: %s", body)
	}
	restCall(t, u, http.MethodPut, base, map[string]any{"enabled": true}, apiTeamAgentRESTPut)
	_, body = restCall(t, u, http.MethodPost, base+"/test", nil, apiTeamAgentRESTTest)
	res = RESTTestResult{}
	_ = json.Unmarshal([]byte(body), &res)
	if !res.OK {
		t.Fatalf("on: %s", body)
	}
}
