package agents

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/team"
	teamagents "github.com/yogasw/wick/internal/connectors/team-agents"
	"github.com/yogasw/wick/internal/entity"
)

func setCaptainPolicy(t *testing.T, w captainWorld, mode string) {
	t.Helper()
	ctx := context.Background()
	c, _ := globalTeam.Get(ctx, w.captain.ID)
	c.CaptainPolicy = team.EncodeCaptainPolicy(team.CaptainPolicy{AutoApprove: mode})
	if err := globalTeam.Update(ctx, &c); err != nil {
		t.Fatal(err)
	}
}

func allowCaptainAccess(t *testing.T, id string) {
	t.Helper()
	ctx := context.Background()
	p, _ := globalTeam.Get(ctx, id)
	p.CaptainCan = team.EncodeCaptainCan(team.CaptainCan{Access: true})
	if err := globalTeam.Update(ctx, &p); err != nil {
		t.Fatal(err)
	}
}

func toolsOf(t *testing.T, id string) []string {
	t.Helper()
	p, err := globalTeam.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return team.DecodeNativeTools(p.AllowedNativeTools)
}

// A tools-only proposal keeps the connectors, waits for the card and
// applies the native tools on accept.
func TestCaptainProposesNativeTools(t *testing.T) {
	w := withCaptainWorld(t)
	ctx := context.Background()
	allowCaptainAccess(t, w.worker.ID)
	p, _ := globalTeam.Get(ctx, w.worker.ID)
	p.AllowedConnectors = team.EncodeGrants([]team.ConnectorGrant{{ConnectorID: "n1", Level: team.LevelRead}})
	p.AllowedNativeTools = team.EncodeNativeTools(team.DefaultNativeTools)
	if err := globalTeam.Update(ctx, &p); err != nil {
		t.Fatal(err)
	}
	want := append(slices.Clone(team.DefaultNativeTools), "Bash", "Edit", "Write")
	if _, err := (CaptainOps{}).SetAccess(ctx, "s-captain", teamagents.AccessInput{Agent: "@worker", NativeTools: []string{"Rm"}}); err == nil {
		t.Fatal("an unknown tool must be refused")
	}
	out, err := CaptainOps{}.SetAccess(ctx, "s-captain", teamagents.AccessInput{Agent: "@worker", NativeTools: want})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)
	if res["status"] != "pending_approval" {
		t.Fatalf("set_access = %v", res)
	}
	diff := strings.Join(res["changes"].([]string), ",")
	if diff != "+Bash (tool),+Edit (tool),+Write (tool)" {
		t.Errorf("diff = %q", diff)
	}
	if slices.Contains(toolsOf(t, w.worker.ID), "Bash") {
		t.Fatal("nothing may change before the owner accepts")
	}
	owner := &entity.User{ID: "u1", Name: "Yoga", Role: entity.RoleUser}
	id := res["approval_id"].(string)
	rw, c := postCtx(t, owner, "/api/sessions/s-captain/approvals/"+id, `{"decision":"accept"}`, map[string]string{"id": "s-captain", "approvalID": id})
	sessionApprovalDecision(c)
	if rw.Code != http.StatusOK {
		t.Fatalf("accept: %d", rw.Code)
	}
	if got := toolsOf(t, w.worker.ID); !slices.Equal(got, team.NormalizeNativeTools(want)) {
		t.Errorf("tools after accept = %v", got)
	}
	got, _ := globalTeam.Get(ctx, w.worker.ID)
	if gs := team.DecodeGrants(got.AllowedConnectors); len(gs) != 1 || gs[0].ConnectorID != "n1" {
		t.Errorf("a tools-only proposal must keep the connectors, got %v", gs)
	}
	if !strings.Contains(convText(t, "s-worker"), "+Bash (tool)") {
		t.Error("access_changed must list the tools")
	}
}

// auto_approve=created applies create's suggestions at once, but a later
// set_access still waits for the card.
func TestCaptainAutoApproveCreated(t *testing.T) {
	w := withCaptainWorld(t)
	ctx := context.Background()
	setCaptainPolicy(t, w, team.AutoApproveCreated)
	out, err := CaptainOps{}.Create(ctx, "s-captain", teamagents.CreateInput{
		Name:              "Client X",
		AccessSuggestions: []team.ConnectorGrant{{ConnectorID: "n1", Level: team.LevelRead}},
		NativeTools:       append(slices.Clone(team.DefaultNativeTools), "Bash", "Write"),
	})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)
	req, _ := res["access_request"].(map[string]any)
	if req == nil || req["status"] != "applied" {
		t.Fatalf("create under auto-approve = %v", res)
	}
	p, _ := globalTeam.GetByHandle(ctx, "u1", "client-x")
	if gs := team.DecodeGrants(p.AllowedConnectors); len(gs) != 1 {
		t.Errorf("grants = %v", gs)
	}
	if tools := team.DecodeNativeTools(p.AllowedNativeTools); !slices.Contains(tools, "Bash") || !slices.Contains(tools, "Write") {
		t.Errorf("tools = %v", tools)
	}
	rows, _ := globalTeam.AccessHistory(ctx, p.ID, team.HistoryLimit)
	if len(rows) != 1 || rows[0].Status != team.AccessApplied || rows[0].DecidedBy != autoApprovedBy {
		t.Errorf("history = %+v", rows)
	}
	if strings.Contains(convText(t, "s-captain"), `"type":"access_change"`) {
		t.Error("an auto-approved proposal must not post a card")
	}

	allowCaptainAccess(t, p.ID)
	out, err = CaptainOps{}.SetAccess(ctx, "s-captain", teamagents.AccessInput{Agent: "@client-x", NativeTools: team.DefaultNativeTools})
	if err != nil {
		t.Fatal(err)
	}
	if out.(map[string]any)["status"] != "pending_approval" {
		t.Errorf("a later proposal must still wait under created: %v", out)
	}
}

// auto_approve=all applies set_access at once, still inside the owner's
// catalog and Settings › Captain.
func TestCaptainAutoApproveAll(t *testing.T) {
	w := withCaptainWorld(t)
	ctx := context.Background()
	setCaptainPolicy(t, w, team.AutoApproveAll)
	in := teamagents.AccessInput{Agent: "@worker", Grants: []team.ConnectorGrant{{ConnectorID: "s1", Level: team.LevelAll}}, NativeTools: []string{"Read", "Bash"}}
	if _, err := (CaptainOps{}).SetAccess(ctx, "s-captain", in); err == nil {
		t.Fatal("auto-approve must not skip the agent's Settings › Captain")
	}
	allowCaptainAccess(t, w.worker.ID)
	beyond := teamagents.AccessInput{Agent: "@worker", Grants: []team.ConnectorGrant{{ConnectorID: "jira", Level: team.LevelAll}}}
	if _, err := (CaptainOps{}).SetAccess(ctx, "s-captain", beyond); err == nil {
		t.Fatal("auto-approve must not exceed the owner's catalog")
	}
	out, err := CaptainOps{}.SetAccess(ctx, "s-captain", in)
	if err != nil {
		t.Fatal(err)
	}
	if out.(map[string]any)["status"] != "applied" {
		t.Fatalf("set_access under all = %v", out)
	}
	if got := toolsOf(t, w.worker.ID); !slices.Equal(got, []string{"Read", "Bash"}) {
		t.Errorf("tools = %v", got)
	}
}

func TestCaptainPolicyCodec(t *testing.T) {
	if got := team.DecodeCaptainPolicy(""); got.AutoApprove != team.AutoApproveOff {
		t.Errorf("empty = %v", got)
	}
	if got := team.DecodeCaptainPolicy(`{"auto_approve":"bogus"}`); got.AutoApprove != team.AutoApproveOff {
		t.Errorf("unknown mode = %v", got)
	}
	c := team.CaptainPolicy{AutoApprove: team.AutoApproveCreated}
	if !c.AutoApproves(true) || c.AutoApproves(false) {
		t.Error("created approves create-time suggestions only")
	}
}
