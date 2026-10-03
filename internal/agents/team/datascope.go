package team

import (
	"context"
	"errors"
	"fmt"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/connectors"
)

// Data scope: which sessions and projects wick's own per-session data
// (notes, tickets, schedules) may be read or written in. It sits on top of
// the connector level a Scope resolves:
//
//   - never across users: a session or project of another person is
//     refused for everyone, agent or not;
//   - an ordinary agent reaches only its own sessions (and the sub-agent
//     sessions under them) and its own project;
//   - the Captain reaches every session and project of its owner.
//
// Errors read "not found" so a caller cannot probe what exists.

// AgentID is the agent the scope was built for ("" for DenyAll).
func (s *Scope) AgentID() string { return s.agentID }

// Captain reports whether the scope belongs to the owner's Captain.
func (s *Scope) Captain() bool { return s.captain }

// agentScopeFrom returns the Team scope on ctx, nil outside an agent.
func agentScopeFrom(ctx context.Context) *Scope {
	s, _ := connectors.AgentScopeFrom(ctx).(*Scope)
	return s
}

// CheckSessionTarget allows callingSID to act on targetSID's data.
// callerUID is the person the call runs for ("" = unknown, no user check).
func CheckSessionTarget(ctx context.Context, layout config.Layout, callerUID, callingSID, targetSID string) error {
	if targetSID == "" || targetSID == callingSID {
		return nil
	}
	notFound := fmt.Errorf("session not found: %s", targetSID)
	sess, err := session.Load(layout, targetSID)
	if err != nil {
		return notFound
	}
	if callerUID != "" && sess.Meta.UserID != "" && sess.Meta.UserID != callerUID {
		return notFound
	}
	return checkAgentSession(ctx, layout, targetSID, notFound)
}

// CheckAgentSession is the agent half of CheckSessionTarget, for callers
// that already enforce the per-user rule themselves (schedules).
func CheckAgentSession(ctx context.Context, layout config.Layout, targetSID string) error {
	return checkAgentSession(ctx, layout, targetSID, fmt.Errorf("session not found: %s", targetSID))
}

func checkAgentSession(ctx context.Context, layout config.Layout, targetSID string, notFound error) error {
	s := agentScopeFrom(ctx)
	if s == nil || s.captain {
		return nil
	}
	if s.denyAll || s.agentID == "" {
		return notFound
	}
	agent, err := AgentOfSession(layout, targetSID)
	if err != nil || agent != s.agentID {
		return notFound
	}
	return nil
}

// CheckProjectTarget allows callingSID to act on projectID's data. The
// calling session's own project always passes.
func CheckProjectTarget(ctx context.Context, layout config.Layout, callerUID, callingSID, projectID string) error {
	if projectID == "" {
		return nil
	}
	if callingSID != "" {
		if sess, err := session.Load(layout, callingSID); err == nil && sess.Meta.ProjectID == projectID {
			return nil
		}
	}
	notFound := fmt.Errorf("project not found: %s", projectID)
	p, err := project.Load(layout, projectID)
	if err != nil {
		return notFound
	}
	if callerUID != "" && p.Meta.OwnerUserID != "" && p.Meta.OwnerUserID != callerUID {
		return notFound
	}
	return CheckAgentProject(ctx, projectID)
}

// CheckAgentProject refuses an ordinary agent any project but its own
// (the calling session's, which CheckProjectTarget already let through).
func CheckAgentProject(ctx context.Context, projectID string) error {
	s := agentScopeFrom(ctx)
	if s == nil || s.captain {
		return nil
	}
	return fmt.Errorf("project not found: %s", projectID)
}

// CheckOwnerRecipient refuses an agent session any notification recipient
// but the person the call runs for.
func CheckOwnerRecipient(ctx context.Context, callerUID, recipientUID string) error {
	if agentScopeFrom(ctx) == nil {
		return nil
	}
	if callerUID == "" || recipientUID != callerUID {
		return errors.New("an agent may only notify its owner — use send_to_me")
	}
	return nil
}
