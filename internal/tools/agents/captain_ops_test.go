package agents

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/connectors"
	teamagents "github.com/yogasw/wick/internal/connectors/team-agents"
	"github.com/yogasw/wick/internal/entity"
)

// captainWorld is withTeamWorld plus an owner u1 with a Captain, a
// worker, a stranger's agent, their main chats and a sub-agent of the
// Captain. The owner's catalog is Notion (n1) and Slack (s1).
type captainWorld struct {
	captain, worker, stranger entity.AgentPersona
}

func withCaptainWorld(t *testing.T) captainWorld {
	t.Helper()
	withTeamWorld(t)
	ctx := context.Background()
	globalTeam.SetOwnerCatalog(func(_ context.Context, userID string) ([]connectors.CatalogEntry, error) {
		if userID != "u1" {
			return nil, nil
		}
		return []connectors.CatalogEntry{
			{Row: entity.Connector{ID: "n1", Label: "Notion"}, Ops: []connectors.CatalogOp{{Key: "read_page"}}},
			{Row: entity.Connector{ID: "s1", Label: "Slack"}, Ops: []connectors.CatalogOp{{Key: "send_message", Destructive: true}}},
		}, nil
	})
	var w captainWorld
	mk := func(owner, handle string, captain bool) entity.AgentPersona {
		seedTeamProject(t, "p-"+handle, owner)
		p := &entity.AgentPersona{OwnerUserID: owner, Handle: handle, ProjectID: "p-" + handle, IsCaptain: captain}
		if err := globalTeam.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
		return *p
	}
	w.captain, w.worker, w.stranger = mk("u1", "captain", true), mk("u1", "worker", false), mk("u2", "other", false)
	seed := func(id, owner, agent, parent string) {
		meta := session.Meta{ProjectID: "p1", Origin: session.OriginUI, Status: session.StatusIdle, UserID: owner,
			AgentID: agent, AgentMain: agent != "", ParentSessionID: parent, CreatedAt: time.Now(), LastActive: time.Now()}
		if err := os.MkdirAll(globalLayout.SessionDir(id), 0o755); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(meta)
		if err := os.WriteFile(globalLayout.SessionMeta(id), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seed("s-captain", "u1", w.captain.ID, "")
	seed("s-worker", "u1", w.worker.ID, "")
	seed("s-sub", "u1", "", "s-captain")
	if err := globalMgr.Registry().Reload(); err != nil {
		t.Fatal(err)
	}
	return w
}

func convText(t *testing.T, sid string) string {
	t.Helper()
	b, _ := os.ReadFile(globalLayout.SessionConversation(sid))
	return string(b)
}

func TestCaptainOpsNeedManagePermission(t *testing.T) {
	w := withCaptainWorld(t)
	ctx := context.Background()
	ops := CaptainOps{}
	if _, err := ops.List(ctx, "s-captain"); err != nil {
		t.Fatalf("Captain list: %v", err)
	}
	for _, sid := range []string{"s-worker", "s-sub", "missing"} {
		if _, err := ops.List(ctx, sid); !errors.Is(err, team.ErrNotManager) {
			t.Errorf("%s: err = %v, want ErrNotManager", sid, err)
		}
	}
	// Owner check: another owner's agent is not reachable by handle or id.
	if _, err := ops.UpdatePersona(ctx, "s-captain", teamagents.PersonaInput{Agent: w.stranger.ID}); err == nil {
		t.Error("stranger's agent must not be editable")
	}
	if _, err := ops.UpdatePersona(ctx, "s-captain", teamagents.PersonaInput{Agent: "@captain"}); err == nil {
		t.Error("the Captain must not manage itself through agents.*")
	}
}

func TestCaptainListReportsTeam(t *testing.T) {
	withCaptainWorld(t)
	out, err := CaptainOps{}.List(context.Background(), "s-captain")
	if err != nil {
		t.Fatal(err)
	}
	items := out.(map[string]any)["agents"].([]captainListItem)
	if len(items) != 2 || !items[0].Captain || items[1].Handle != "worker" || items[1].Status == "" {
		t.Fatalf("list = %+v", items)
	}
}

func TestCaptainCreateIsDenyByDefault(t *testing.T) {
	withCaptainWorld(t)
	ctx := context.Background()
	out, err := CaptainOps{}.Create(ctx, "s-captain", teamagents.CreateInput{
		Name: "Rekap Harian", Tagline: "Daily",
		AccessSuggestions: []team.ConnectorGrant{{ConnectorID: "n1", Level: team.LevelRead}},
	})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)
	p, err := globalTeam.GetByHandle(ctx, "u1", "rekap-harian")
	if err != nil {
		t.Fatalf("created agent: %v (%v)", err, res)
	}
	if len(team.DecodeGrants(p.AllowedConnectors)) != 0 {
		t.Fatalf("new agent must start with no access, got %s", p.AllowedConnectors)
	}
	if res["access_request"] == nil {
		t.Fatalf("suggestions must become an approval request: %v", res)
	}
	conv := convText(t, "s-captain")
	if !strings.Contains(conv, `"kind":"agent_created"`) || !strings.Contains(conv, "created by @captain") {
		t.Errorf("agent_created by @captain missing: %s", conv)
	}
	if !strings.Contains(conv, `"type":"access_change"`) {
		t.Errorf("approval card missing: %s", conv)
	}
}

func TestCaptainUpdatePersonaHonoursCaptainCan(t *testing.T) {
	w := withCaptainWorld(t)
	ctx := context.Background()
	sp := "Review every PR."
	if _, err := (CaptainOps{}).UpdatePersona(ctx, "s-captain", teamagents.PersonaInput{Agent: "@worker", SystemPrompt: &sp}); err != nil {
		t.Fatal(err)
	}
	if proj, _ := globalMgr.Registry().Project(w.worker.ProjectID); proj.Meta.Defaults.SystemAddon != sp {
		t.Errorf("system prompt = %q", proj.Meta.Defaults.SystemAddon)
	}
	if !strings.Contains(convText(t, "s-worker"), `"kind":"persona_changed"`) {
		t.Error("persona_changed chip missing in the agent's chat")
	}
	p, _ := globalTeam.Get(ctx, w.worker.ID)
	p.CaptainCan = team.EncodeCaptainCan(team.CaptainCan{Persona: false})
	if err := globalTeam.Update(ctx, &p); err != nil {
		t.Fatal(err)
	}
	if _, err := (CaptainOps{}).UpdatePersona(ctx, "s-captain", teamagents.PersonaInput{Agent: "worker", SystemPrompt: &sp}); err == nil ||
		!strings.Contains(err.Error(), "Settings › Captain") {
		t.Errorf("persona off must refuse with a clear message, got %v", err)
	}
}

func TestCaptainSetAccessApproval(t *testing.T) {
	w := withCaptainWorld(t)
	ctx := context.Background()
	ops := CaptainOps{}
	in := teamagents.AccessInput{Agent: "@worker", Grants: []team.ConnectorGrant{{ConnectorID: "n1", Level: team.LevelRead}}}

	// Default captain_can.access is off: refused outright, no card.
	if _, err := ops.SetAccess(ctx, "s-captain", in); err == nil || !strings.Contains(err.Error(), "propose access") {
		t.Fatalf("access off: %v", err)
	}
	if strings.Contains(convText(t, "s-captain"), "approval_request") {
		t.Fatal("a refused proposal must not post a card")
	}
	p, _ := globalTeam.Get(ctx, w.worker.ID)
	p.CaptainCan = team.EncodeCaptainCan(team.CaptainCan{Access: true})
	if err := globalTeam.Update(ctx, &p); err != nil {
		t.Fatal(err)
	}

	// Never beyond the owner's catalog.
	beyond := teamagents.AccessInput{Agent: "@worker", Grants: []team.ConnectorGrant{{ConnectorID: "jira", Level: team.LevelAll}}}
	if _, err := ops.SetAccess(ctx, "s-captain", beyond); err == nil {
		t.Fatal("a grant outside the owner's access must be refused")
	}
	pick := teamagents.AccessInput{Agent: "@worker", Grants: []team.ConnectorGrant{{ConnectorID: "n1", Level: team.LevelPick, Ops: []string{"delete_db"}}}}
	if _, err := ops.SetAccess(ctx, "s-captain", pick); err == nil {
		t.Fatal("an op the owner lacks must be refused")
	}

	propose := func() string {
		out, err := ops.SetAccess(ctx, "s-captain", in)
		if err != nil {
			t.Fatal(err)
		}
		res := out.(map[string]any)
		if res["status"] != "pending_approval" {
			t.Fatalf("set_access = %v", res)
		}
		if p, _ := globalTeam.Get(ctx, w.worker.ID); len(team.DecodeGrants(p.AllowedConnectors)) != 0 {
			t.Fatal("nothing may change before the owner accepts")
		}
		return res["approval_id"].(string)
	}
	decide := func(u *entity.User, sid, id, decision string) int {
		rw, c := postCtx(t, u, "/api/sessions/"+sid+"/approvals/"+id, `{"decision":"`+decision+`"}`, map[string]string{"id": sid, "approvalID": id})
		sessionApprovalDecision(c)
		return rw.Code
	}
	owner := &entity.User{ID: "u1", Name: "Yoga", Role: entity.RoleUser}

	// Decline: nothing applied, history declined, event recorded.
	id := propose()
	if code := decide(owner, "s-captain", id, "decline"); code != http.StatusOK {
		t.Fatalf("decline: %d", code)
	}
	if p, _ := globalTeam.Get(ctx, w.worker.ID); len(team.DecodeGrants(p.AllowedConnectors)) != 0 {
		t.Fatal("decline must not apply")
	}
	if !strings.Contains(convText(t, "s-worker"), `"kind":"access_change_declined"`) {
		t.Error("access_change_declined missing")
	}
	if code := decide(owner, "s-captain", id, "accept"); code != http.StatusGone {
		t.Errorf("deciding twice: %d", code)
	}

	// Accept: applied, history applied, access_changed with approved_by.
	id = propose()
	if code := decide(owner, "s-worker", id, "accept"); code == http.StatusOK {
		t.Error("a card can only be decided from the session it sits in")
	}
	if code := decide(owner, "s-captain", id, "accept"); code != http.StatusOK {
		t.Fatalf("accept: %d", code)
	}
	got, _ := globalTeam.Get(ctx, w.worker.ID)
	if gs := team.DecodeGrants(got.AllowedConnectors); len(gs) != 1 || gs[0].ConnectorID != "n1" {
		t.Fatalf("grants after accept = %v", gs)
	}
	conv := convText(t, "s-worker")
	if !strings.Contains(conv, `"kind":"access_changed"`) || !strings.Contains(conv, `"approved_by":"Yoga"`) {
		t.Errorf("access_changed with approved_by missing: %s", conv)
	}
	rows, _ := globalTeam.AccessHistory(ctx, w.worker.ID, team.HistoryLimit)
	statuses := map[string]int{}
	for _, r := range rows {
		statuses[r.Status]++
		if r.Actor != "@captain" {
			t.Errorf("actor = %q", r.Actor)
		}
	}
	if statuses[team.AccessApplied] != 1 || statuses[team.AccessDeclined] != 1 {
		t.Errorf("history = %v", statuses)
	}
}

// A human Settings save is audited too, and the History route returns it.
func TestAccessHistoryRecordsSettingsSave(t *testing.T) {
	w := withCaptainWorld(t)
	owner := &entity.User{ID: "u1", Name: "Yoga", Role: entity.RoleUser}
	before := w.worker
	after := before
	after.AllowedConnectors = team.EncodeGrants([]team.ConnectorGrant{{ConnectorID: "s1", Level: team.LevelAll}})
	_, c := teamReq(t, owner, http.MethodPatch, "/api/team/agents/x", nil, map[string]string{"id": w.worker.ID})
	announceAccessChanged(c, before, after)

	rw, c := teamReq(t, owner, http.MethodGet, "/api/team/agents/x/access-history", nil, map[string]string{"id": w.worker.ID})
	apiTeamAgentAccessHistory(c)
	var body struct {
		Items []teamAccessHistoryItem `json:"items"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil || rw.Code != http.StatusOK {
		t.Fatalf("%d %s", rw.Code, rw.Body)
	}
	if len(body.Items) != 1 || body.Items[0].Actor != "Yoga" || body.Items[0].Status != team.AccessApplied || len(body.Items[0].Diff) != 1 {
		t.Fatalf("history = %+v", body.Items)
	}
	// Someone else's agent: 404.
	rw, c = teamReq(t, owner, http.MethodGet, "/x", nil, map[string]string{"id": w.stranger.ID})
	apiTeamAgentAccessHistory(c)
	if rw.Code != http.StatusNotFound {
		t.Errorf("stranger history: %d", rw.Code)
	}
}

func TestTeamAgentItemCaptainFields(t *testing.T) {
	w := withCaptainWorld(t)
	it := teamAgentToItem(w.captain, teamProjectUsers{}, teamLive{}, nil)
	if !it.ManageAgents || it.CaptainCan != team.DefaultCaptainCan() {
		t.Errorf("captain item = %v %+v", it.ManageAgents, it.CaptainCan)
	}
	if teamAgentToItem(w.worker, teamProjectUsers{}, teamLive{}, nil).ManageAgents {
		t.Error("worker must not manage agents by default")
	}
}
