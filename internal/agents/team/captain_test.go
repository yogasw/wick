package team

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
)

func TestDecodeCaptainCanDefaults(t *testing.T) {
	for _, raw := range []string{"", "{}", "not json"} {
		if got := DecodeCaptainCan(raw); got != DefaultCaptainCan() {
			t.Errorf("%q = %+v, want defaults", raw, got)
		}
	}
	if d := DefaultCaptainCan(); !d.Persona || d.Access || !d.Routines {
		t.Errorf("defaults = %+v, want persona+routines on, access off", d)
	}
	// A key the row lacks keeps its default.
	if got := DecodeCaptainCan(`{"access":true}`); !got.Access || !got.Persona || !got.Routines {
		t.Errorf("partial = %+v", got)
	}
	if got := DecodeCaptainCan(EncodeCaptainCan(CaptainCan{})); got != (CaptainCan{}) {
		t.Errorf("all-off round trip = %+v", got)
	}
}

func TestManagesAgents(t *testing.T) {
	on, off := true, false
	cases := []struct {
		p    entity.AgentPersona
		want bool
	}{
		{entity.AgentPersona{IsCaptain: true}, true},
		{entity.AgentPersona{}, false},
		{entity.AgentPersona{IsCaptain: true, ManageAgents: &off}, false},
		{entity.AgentPersona{ManageAgents: &on}, true},
	}
	for i, c := range cases {
		if got := ManagesAgents(c.p); got != c.want {
			t.Errorf("case %d = %v, want %v", i, got, c.want)
		}
	}
}

func TestScopeGatesAgentsConnector(t *testing.T) {
	if ScopeOf(entity.AgentPersona{ID: "a"}, nil).AllowKey(ManageAgentsKey) {
		t.Error("agent without Manage other agents must not see the agents connector")
	}
	sc := ScopeOf(entity.AgentPersona{ID: "c", IsCaptain: true}, nil)
	if !sc.AllowKey(ManageAgentsKey) {
		t.Error("Captain must see the agents connector by default")
	}
	if !sc.AllowKey("notes") {
		t.Error("gate must only touch the agents key")
	}
	if got := scopeFor(sc, false); got.(connectors.AgentFeatureScope).AllowKey(ManageAgentsKey) {
		t.Error("a sub-agent must not inherit Manage other agents")
	}
	if !sc.AllowKey(ManageAgentsKey) {
		t.Error("scopeFor must not mutate the cached scope")
	}
	if DenyAll().AllowKey(ManageAgentsKey) {
		t.Error("deny-all must not see the agents connector")
	}
}

func TestScopeForSessionSubAgentLosesManage(t *testing.T) {
	ctx := context.Background()
	layout := config.NewLayout(t.TempDir())
	svc := NewService(testDB(t), layout)
	p := &entity.AgentPersona{OwnerUserID: "u1", Handle: "captain", IsCaptain: true}
	if err := svc.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	for _, s := range []session.CreateOptions{
		{ID: "s-main", AgentID: p.ID},
		{ID: "s-sub", ParentSessionID: "s-main"},
	} {
		if _, err := session.Create(ctx, layout, s); err != nil {
			t.Fatal(err)
		}
	}
	keyOn := func(sid string) bool {
		return svc.ScopeForSession(ctx, sid).(connectors.AgentFeatureScope).AllowKey(ManageAgentsKey)
	}
	if !keyOn("s-main") {
		t.Error("Captain's own chat must see the agents connector")
	}
	// Twice: the second read comes from the cache.
	for i := 0; i < 2; i++ {
		if keyOn("s-sub") {
			t.Errorf("read %d: sub-agent must not see the agents connector", i)
		}
	}
	if _, err := svc.ManagerFor(ctx, "s-main"); err != nil {
		t.Errorf("ManagerFor main: %v", err)
	}
	if _, err := svc.ManagerFor(ctx, "s-sub"); !errors.Is(err, ErrNotManager) {
		t.Errorf("ManagerFor sub = %v, want ErrNotManager", err)
	}
	off := false
	p.ManageAgents = &off
	if err := svc.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	if keyOn("s-main") {
		t.Error("switching Manage other agents off must hide the connector")
	}
	if _, err := svc.ManagerFor(ctx, "s-main"); !errors.Is(err, ErrNotManager) {
		t.Errorf("ManagerFor with permission off = %v", err)
	}
}

func TestAccessHistoryPendingSettle(t *testing.T) {
	ctx := context.Background()
	st := NewStore(testDB(t))
	h := &entity.AgentAccessHistory{AgentID: "a1", OwnerUserID: "u1", Actor: "@captain", Status: AccessPending, ApprovalID: "ap-1"}
	ch := AccessChange{Diff: []string{"+Notion (read)"}, Grants: []ConnectorGrant{{ConnectorID: "n1", Level: LevelRead}}}
	if err := st.RecordAccess(ctx, h, ch); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordAccess(ctx, &entity.AgentAccessHistory{AgentID: "a1", OwnerUserID: "u1", Actor: "Yoga"}, AccessChange{Diff: []string{"-Slack"}}); err != nil {
		t.Fatal(err)
	}
	got, gotCh, err := st.PendingAccess(ctx, "ap-1")
	if err != nil || got.ID != h.ID || len(gotCh.Grants) != 1 || gotCh.Diff[0] != "+Notion (read)" {
		t.Fatalf("pending = %+v %+v %v", got, gotCh, err)
	}
	if err := st.SettleAccess(ctx, h.ID, AccessApplied, "Yoga"); err != nil {
		t.Fatal(err)
	}
	if err := st.SettleAccess(ctx, h.ID, AccessDeclined, "Yoga"); !errors.Is(err, ErrNotPending) {
		t.Errorf("second settle = %v, want ErrNotPending", err)
	}
	if _, _, err := st.PendingAccess(ctx, "ap-1"); !errors.Is(err, ErrNotPending) {
		t.Errorf("settled proposal still pending: %v", err)
	}
	rows, err := st.AccessHistory(ctx, "a1", HistoryLimit)
	if err != nil || len(rows) != 2 {
		t.Fatalf("history = %d rows, %v", len(rows), err)
	}
	if rows[1].Status != AccessApplied || rows[1].DecidedBy != "Yoga" {
		t.Errorf("settled row = %+v", rows[1])
	}
}

func TestManageAgentsBlock(t *testing.T) {
	if manageAgentsBlock(entity.AgentPersona{}) != "" {
		t.Error("an agent without the permission must not hear of agents.*")
	}
	b := manageAgentsBlock(entity.AgentPersona{IsCaptain: true})
	if !strings.Contains(b, "set_access") || !strings.Contains(b, "NO access") {
		t.Errorf("block misses the rules: %q", b)
	}
	// Three to five lines, short enough not to crowd the Team block.
	lines := strings.Count(strings.TrimSpace(ManageAgentsBlock), "\n") + 1
	if lines < 3 || lines > 5 || len(ManageAgentsBlock) > 800 {
		t.Errorf("block is %d lines / %d bytes", lines, len(ManageAgentsBlock))
	}
}
