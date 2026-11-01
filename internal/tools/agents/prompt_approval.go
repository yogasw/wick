package agents

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/yogasw/wick/internal/agents/gate"
	"github.com/yogasw/wick/internal/agents/promptguard"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// globalPrompts settles prompt_change cards; nil until SetPromptGuard.
var globalPrompts *promptguard.Service

// SetPromptGuard wires the operator-prompt history: its approval cards
// are posted into the proposing session like a Captain's access card.
func SetPromptGuard(s *promptguard.Service) {
	globalPrompts = s
	if s == nil {
		return
	}
	s.Notify = func(card promptguard.Card) {
		extras := card.Extras
		agent := ""
		if globalTeam != nil {
			if p := globalTeam.AgentFor(context.Background(), card.SessionID); p != nil {
				agent = p.Handle
			}
		}
		extras["agent"] = agent
		who := "An agent"
		if agent != "" {
			who = "@" + agent
		}
		emitSystemEvent(card.SessionID, store.KindApprovalRequest, who+" "+card.Text, extras)
	}
}

// browserAdmin is the decider of a prompt change: an admin signed in
// with the browser cookie. A bearer token, a CLI token, a plugin's
// in-process call or a narrowed sub-agent principal is refused even
// when it maps to an admin, because those are what an agent holds.
func browserAdmin(c *tool.Ctx) error {
	u := login.GetUser(c.Context())
	if u == nil || !u.IsAdmin() {
		return promptguard.ErrNotAdmin
	}
	if strings.TrimSpace(c.R.Header.Get("Authorization")) != "" || login.IsScopedPrincipal(c.Context()) {
		return errors.New("a prompt change is accepted by an admin in the browser, not with a token")
	}
	if _, machine := login.MachineCaller(c.Context()); machine {
		return errors.New("a prompt change is accepted by an admin in the browser, not by a plugin")
	}
	return nil
}

// resolvePromptApproval settles a prompt_change card. false = approvalID
// is no prompt proposal of sessionID, and the caller goes on.
func resolvePromptApproval(c *tool.Ctx, sessionID, approvalID, decision string) bool {
	if globalPrompts == nil {
		return false
	}
	ch, err := globalPrompts.Proposal(c.Context(), approvalID)
	if err != nil || ch.SessionID != sessionID {
		return false
	}
	if err := browserAdmin(c); err != nil {
		c.JSON(http.StatusForbidden, map[string]string{"error": err.Error()})
		return true
	}
	who := actorName(c)
	accept := decision == gate.DecisionApproveOnce || decision == gate.DecisionApproveSession
	res, err := globalPrompts.Decide(c.Context(), approvalID, login.GetUser(c.Context()), who, accept)
	switch {
	case errors.Is(err, promptguard.ErrNotPending):
		c.JSON(http.StatusGone, map[string]string{"error": err.Error()})
		return true
	case errors.Is(err, promptguard.ErrStaleBase):
		c.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
		return true
	case err != nil:
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return true
	}
	state, outcome := gate.DecisionBlock, "declined"
	if accept {
		state, outcome = gate.DecisionApproveOnce, fmt.Sprintf("applied as version %d", res.Version)
	}
	emitSystemEvent(sessionID, store.KindApprovalRequest, outcome+" · by "+who, map[string]string{
		"approval_id": approvalID, "state": state, "type": promptguard.CardType, "decided_by": who,
	})
	c.JSON(http.StatusOK, map[string]string{"status": "resolved", "decision": state})
	return true
}
