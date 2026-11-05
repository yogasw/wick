package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/schedule"
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

// A Run-as-caller agent runs as the person chatting, who is not the
// session's owner: it may still schedule into, and manage the schedules
// of, its own session — but not another session of that owner, and a
// person outside an agent keeps the owner rule.
func TestScheduleOwnAgentSessionAsCaller(t *testing.T) {
	layout := agentconfig.NewLayout(t.TempDir())
	for _, o := range []session.CreateOptions{
		{ID: "ops-thread", AgentID: "ops", UserID: "u1"},
		{ID: "ops-bot", AgentID: "ops"},
		{ID: "ops-other", AgentID: "ops", UserID: "u1"},
	} {
		if _, err := session.Create(context.Background(), layout, o); err != nil {
			t.Fatal(err)
		}
	}
	caller := &entity.User{ID: "u2", Approved: true}
	ops := team.ScopeOf(entity.AgentPersona{ID: "ops", OwnerUserID: "u1"}, nil)
	req := func(scope connectors.AgentScope, sid string) *http.Request {
		r := httptest.NewRequest("POST", "/", nil)
		ctx := r.Context()
		if scope != nil {
			ctx = connectors.WithAgentScope(ctx, scope)
		}
		return r.WithContext(WithSessionID(ctx, sid))
	}
	target := func(sid string) schedule.TargetSpec {
		return schedule.TargetSpec{SessionID: sid, Mode: entity.ScheduledSessionExisting}
	}

	if owner, err := scheduleAuthorizeTarget(req(ops, "ops-thread"), layout, target("ops-thread"), caller); err != nil || owner != "u1" {
		t.Fatalf("own session: owner=%q err=%v", owner, err)
	}
	if owner, err := scheduleAuthorizeTarget(req(ops, "ops-bot"), layout, target("ops-bot"), caller); err != nil || owner != "u2" {
		t.Fatalf("ownerless own session: owner=%q err=%v", owner, err)
	}
	if _, err := scheduleAuthorizeTarget(req(ops, "ops-thread"), layout, target("ops-other"), caller); err == nil {
		t.Fatal("caller reached another session of the owner")
	}
	if _, err := scheduleAuthorizeTarget(req(nil, "ops-thread"), layout, target("ops-thread"), caller); err == nil {
		t.Fatal("person outside an agent bypassed the owner rule")
	}

	watch := entity.ScheduledMessage{OwnerUserID: "u2", SessionID: "ops-thread"}
	if !scheduleCanManage(req(ops, "ops-thread"), layout, watch, caller) {
		t.Fatal("agent cannot manage the watch on its own session")
	}
	if scheduleCanManage(req(ops, "ops-other"), layout, entity.ScheduledMessage{OwnerUserID: "u9", SessionID: "ops-thread"}, caller) {
		t.Fatal("agent managed a schedule of a session it is not running in")
	}
}
