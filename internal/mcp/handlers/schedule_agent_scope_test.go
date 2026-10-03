package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
)

// By-id actions (cancel/pause/resume/reschedule/run_now) all pass through
// scheduleCanManage: an ordinary agent manages only schedules tied to its
// own sessions or project; people and the Captain keep the owner rules.
func TestScheduleAgentMayManage(t *testing.T) {
	layout := agentconfig.NewLayout(t.TempDir())
	for _, id := range []string{"p-ops", "p-cap"} {
		if _, err := project.Create(layout, project.CreateOptions{ID: id, Name: id, OwnerUserID: "u1"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []session.CreateOptions{
		{ID: "ops-main", AgentID: "ops", UserID: "u1", ProjectID: "p-ops"},
		{ID: "cap-main", AgentID: "cap", UserID: "u1", ProjectID: "p-cap"},
	} {
		if _, err := session.Create(context.Background(), layout, o); err != nil {
			t.Fatal(err)
		}
	}
	req := func(scope connectors.AgentScope) *http.Request {
		r := httptest.NewRequest("POST", "/", nil)
		ctx := connectors.WithAgentScope(r.Context(), scope)
		return r.WithContext(WithSessionID(ctx, "ops-main"))
	}
	ops := team.ScopeOf(entity.AgentPersona{ID: "ops"}, nil)
	capt := team.ScopeOf(entity.AgentPersona{ID: "cap", IsCaptain: true}, nil)
	own := entity.ScheduledMessage{SessionID: "ops-main"}
	fromOwn := entity.ScheduledMessage{SourceSessionID: "ops-main", ProjectID: "p-cap"}
	others := entity.ScheduledMessage{SessionID: "cap-main"}
	otherProject := entity.ScheduledMessage{ProjectID: "p-cap"}
	cases := []struct {
		name  string
		scope connectors.AgentScope
		m     entity.ScheduledMessage
		want  bool
	}{
		{"agent own session", ops, own, true},
		{"agent created it", ops, fromOwn, true},
		{"agent other agent's session", ops, others, false},
		{"agent other project", ops, otherProject, false},
		{"captain any", capt, others, true},
		{"person", nil, others, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scheduleAgentMayManage(req(tc.scope), layout, tc.m); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// The Captain of an admin owner gets no admin bypass: another user's
// schedule stays out of reach, its owner's own stays manageable.
func TestScheduleCanManageCaptainOfAdmin(t *testing.T) {
	layout := agentconfig.NewLayout(t.TempDir())
	if _, err := project.Create(layout, project.CreateOptions{ID: "p-u2", Name: "u2", OwnerUserID: "u2", Tags: []string{"t-x"}}); err != nil {
		t.Fatal(err)
	}
	admin := &entity.User{ID: "u1", Role: entity.RoleAdmin, Approved: true}
	capt := team.ScopeOf(entity.AgentPersona{ID: "cap", OwnerUserID: "u1", IsCaptain: true}, nil)
	r := httptest.NewRequest("POST", "/", nil)
	agentReq := r.WithContext(connectors.WithAgentScope(r.Context(), capt))
	other := entity.ScheduledMessage{OwnerUserID: "u2", ProjectID: "p-u2"}
	if scheduleCanManage(agentReq, layout, other, admin) {
		t.Fatal("captain of an admin owner managed another user's schedule")
	}
	if !scheduleCanManage(agentReq, layout, entity.ScheduledMessage{OwnerUserID: "u1"}, admin) {
		t.Fatal("captain refused its owner's schedule")
	}
	if !scheduleCanManage(r, layout, other, admin) {
		t.Fatal("admin as a person lost the bypass")
	}
	if owner, all := scheduleScope(admin); owner != "" || !all {
		t.Fatal("person admin list scope changed")
	}
}
