package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/yogasw/wick/internal/agents/gate"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/team"
	teamagents "github.com/yogasw/wick/internal/connectors/team-agents"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// accessChangeType marks an approval_request card that carries a
// Captain's access proposal rather than a gate prompt.
const accessChangeType = "access_change"

// CaptainOps implements the agents.* connector (teamagents.Ops). Every op
// resolves the calling agent from the SESSION, requires its "Manage other
// agents" permission and acts on its owner's agents only.
type CaptainOps struct{}

var _ teamagents.Ops = CaptainOps{}

// TeamAgentOps is the late-bound accessor the connector registers with;
// nil until the Team service is wired.
func TeamAgentOps() teamagents.Ops {
	if globalTeam == nil {
		return nil
	}
	return CaptainOps{}
}

// captainListItem is one agent as agents.list reports it.
type captainListItem struct {
	ID          string `json:"id"`
	Handle      string `json:"handle"`
	Name        string `json:"name"`
	Tagline     string `json:"tagline,omitempty"`
	Description string `json:"description,omitempty"`
	Remote      bool   `json:"remote,omitempty"`
	// AgentDescription is a remote agent's longer profile (its system
	// prompt slot, which nothing sends to the remote): what it does and
	// when to call it.
	AgentDescription string `json:"agent_description,omitempty"`
	Captain          bool   `json:"captain"`
	Disabled         bool   `json:"disabled"`
	Status           string `json:"status"`
	CurrentAction    string `json:"current_action,omitempty"`
	NeedsAttention   bool   `json:"needs_attention"`
	Attention        string `json:"attention,omitempty"`
	Unread           bool   `json:"unread"`
	LastError        string `json:"last_error,omitempty"`
}

func (CaptainOps) manager(ctx context.Context, sessionID string) (entity.AgentPersona, error) {
	if globalTeam == nil {
		return entity.AgentPersona{}, errors.New("agents app is not enabled")
	}
	return globalTeam.ManagerFor(ctx, sessionID)
}

// target resolves ref (a @handle or an id) among the manager's owner's
// agents, never the manager itself.
func (o CaptainOps) target(ctx context.Context, mgr entity.AgentPersona, ref string) (entity.AgentPersona, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return entity.AgentPersona{}, errors.New("agent is required")
	}
	p, err := globalTeam.GetByHandle(ctx, mgr.OwnerUserID, team.NormalizeHandle(ref))
	if err != nil {
		p, err = globalTeam.Get(ctx, ref)
	}
	if err != nil || p.OwnerUserID != mgr.OwnerUserID {
		return entity.AgentPersona{}, fmt.Errorf("no agent %q on your Team", ref)
	}
	if p.ID == mgr.ID {
		return entity.AgentPersona{}, errors.New("this op manages OTHER agents; your own settings are the owner's to change")
	}
	return p, nil
}

// List implements agents.list.
func (o CaptainOps) List(ctx context.Context, sessionID string) (any, error) {
	mgr, err := o.manager(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	rows, err := globalTeam.List(ctx, mgr.OwnerUserID)
	if err != nil {
		return nil, err
	}
	users := teamProjectUsersFor(ctx, rows)
	live := teamLiveNow()
	out := make([]captainListItem, 0, len(rows))
	for _, p := range rows {
		it := teamAgentToItem(p, users, live, nil)
		row := captainListItem{
			ID: it.ID, Handle: it.Handle, Name: it.Name, Tagline: it.Tagline,
			Description: it.Description, Remote: IsRemoteAgent(p),
			Captain: it.IsCaptain, Disabled: it.Disabled, Status: it.Status,
			CurrentAction: it.CurrentAction, NeedsAttention: it.NeedsAttention,
			Attention: it.AttentionPreview, Unread: it.Unread,
		}
		if it.Status == "error" {
			row.LastError = it.LastPreview
		}
		if row.Remote {
			row.AgentDescription = it.SystemPrompt
		}
		out = append(out, row)
	}
	return map[string]any{"agents": out}, nil
}

// Create implements agents.create: a new agent with no access at all.
// Suggested access becomes one approval request on the manager's chat.
func (o CaptainOps) Create(ctx context.Context, sessionID string, in teamagents.CreateInput) (any, error) {
	mgr, err := o.manager(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	handle := team.NormalizeHandle(in.Handle)
	if handle == "" {
		handle = team.NormalizeHandle(strings.ReplaceAll(strings.ToLower(name), " ", "-"))
	}
	if err := validateTeamHandle(ctx, handle); err != nil {
		return nil, err
	}
	if name == "" {
		name = handle
	}
	tagline := strings.TrimSpace(in.Tagline)
	if len([]rune(tagline)) > team.MaxTagline {
		return nil, fmt.Errorf("tagline is at most %d characters", team.MaxTagline)
	}
	if _, err := globalTeam.GetByHandle(ctx, mgr.OwnerUserID, handle); err == nil {
		return nil, team.ErrHandleTaken
	}
	av := team.DefaultAvatarFor(handle)
	if in.Avatar != "" {
		if err := json.Unmarshal([]byte(in.Avatar), &av); err != nil {
			return nil, errors.New("avatar must be a JSON object")
		}
	}
	sys := in.SystemPrompt
	if strings.TrimSpace(sys) == "" {
		sys = defaultAgentSystemAddon
	}
	pid, err := createAgentProjectFor(ctx, mgr.OwnerUserID, name, "", in.Description, sys, "", "", "")
	if err != nil {
		return nil, err
	}
	p := &entity.AgentPersona{
		OwnerUserID: mgr.OwnerUserID, Handle: handle, ProjectID: pid, Tagline: tagline,
		// Deny by default: suggestions never land here, only an accepted
		// approval does.
		AllowedConnectors: "[]",
		RunAs:             team.RunAsCaller,
		Features:          team.EncodeFeatures(team.DefaultFeatures()),
		Avatar:            team.EncodeAvatar(av),
	}
	if err := globalTeam.Create(ctx, p); err != nil {
		_ = globalMgr.DeleteProject(ctx, pid)
		return nil, err
	}
	by := "@" + mgr.Handle
	label := o.labeler(ctx, mgr.OwnerUserID)
	extras := agentCreatedExtras(*p, name, by, "", label, "chat")
	text := agentCreatedText(name, by, "", "")
	if s, ok := mainSessionOf(mgr.OwnerUserID, mgr.ID); ok {
		emitSystemEvent(s.ID, store.KindAgentCreated, text, extras)
	}
	out := map[string]any{"id": p.ID, "handle": p.Handle, "name": name, "access": "none"}
	if len(in.AccessSuggestions) > 0 {
		res, err := o.propose(ctx, sessionID, mgr, *p, in.AccessSuggestions, "suggested access for the new agent")
		if err != nil {
			out["access_suggestions_error"] = err.Error()
		} else {
			out["access_request"] = res
		}
	}
	return out, nil
}

// UpdatePersona implements agents.update_persona.
func (o CaptainOps) UpdatePersona(ctx context.Context, sessionID string, in teamagents.PersonaInput) (any, error) {
	mgr, err := o.manager(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	p, err := o.target(ctx, mgr, in.Agent)
	if err != nil {
		return nil, err
	}
	if !team.DecodeCaptainCan(p.CaptainCan).Persona {
		return nil, fmt.Errorf("@%s does not let the Captain edit its persona (its Settings › Captain); ask the owner", p.Handle)
	}
	var changed []string
	if in.Tagline != nil {
		t := strings.TrimSpace(*in.Tagline)
		if len([]rune(t)) > team.MaxTagline {
			return nil, fmt.Errorf("tagline is at most %d characters", team.MaxTagline)
		}
		if t != p.Tagline {
			p.Tagline = t
			changed = append(changed, "tagline")
			if err := globalTeam.Update(ctx, &p); err != nil {
				return nil, err
			}
		}
	}
	if p.ProjectID != "" && (in.Name != nil || in.Description != nil || in.SystemPrompt != nil) {
		proj, ok := globalMgr.Registry().Project(p.ProjectID)
		if !ok {
			return nil, errors.New("the agent's project is gone; its persona cannot be edited")
		}
		meta := proj.Meta
		set := func(dst *string, v *string, what string) {
			if v != nil && *dst != *v {
				*dst = *v
				changed = append(changed, what)
			}
		}
		set(&meta.Name, in.Name, "name")
		set(&meta.Description, in.Description, "description")
		set(&meta.Defaults.SystemAddon, in.SystemPrompt, "system prompt")
		if _, err := globalMgr.UpdateProject(ctx, p.ProjectID, meta); err != nil {
			return nil, err
		}
	}
	if len(changed) == 0 {
		return map[string]any{"status": "unchanged"}, nil
	}
	by := "@" + mgr.Handle
	text := fmt.Sprintf("Persona of @%s changed: %s · by %s", p.Handle, strings.Join(changed, ", "), by)
	extras := map[string]string{"agent_id": p.ID, "handle": p.Handle, "changed_by": by, "fields": strings.Join(changed, ",")}
	if s, ok := mainSessionOf(p.OwnerUserID, p.ID); ok {
		emitSystemEvent(s.ID, store.KindPersonaChanged, text, extras)
	}
	return map[string]any{"status": "updated", "changed": changed}, nil
}

// SetAccess implements agents.set_access: it only ever files a proposal.
func (o CaptainOps) SetAccess(ctx context.Context, sessionID string, in teamagents.AccessInput) (any, error) {
	mgr, err := o.manager(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	p, err := o.target(ctx, mgr, in.Agent)
	if err != nil {
		return nil, err
	}
	if !team.DecodeCaptainCan(p.CaptainCan).Access {
		return nil, fmt.Errorf("@%s does not let the Captain propose access changes (its Settings › Captain); ask the owner to change it there", p.Handle)
	}
	return o.propose(ctx, sessionID, mgr, p, in.Grants, in.Reason)
}

// propose checks grants against the OWNER's own catalog (the same check
// as Settings › Access), records a pending history row and posts the
// approval card in the manager's chat.
func (o CaptainOps) propose(ctx context.Context, sessionID string, mgr, p entity.AgentPersona, grants []team.ConnectorGrant, reason string) (map[string]any, error) {
	if grants == nil {
		grants = []team.ConnectorGrant{}
	}
	cat, err := globalTeam.OwnerCatalog(ctx, mgr.OwnerUserID)
	if err != nil {
		return nil, err
	}
	if err := team.CheckGrants(grants, team.CatalogOf(cat)); err != nil {
		return nil, err
	}
	label := labelerOf(cat)
	after := p
	after.AllowedConnectors = team.EncodeGrants(grants)
	diff := accessChangeList(p, after, label)
	if len(diff) == 0 {
		return nil, fmt.Errorf("@%s already has exactly this access", p.Handle)
	}
	approvalID := uuid.New().String()
	by := "@" + mgr.Handle
	h := &entity.AgentAccessHistory{
		AgentID: p.ID, OwnerUserID: p.OwnerUserID, Actor: by, ActorAgentID: mgr.ID,
		Status: team.AccessPending, ApprovalID: approvalID, SessionID: sessionID,
	}
	if err := globalTeam.RecordAccess(ctx, h, team.AccessChange{Diff: diff, Grants: grants}); err != nil {
		return nil, err
	}
	extras := map[string]string{
		"approval_id": approvalID, "state": "pending", "type": accessChangeType,
		"agent": mgr.Handle, "target": p.Handle, "target_id": p.ID,
		"changes": strings.Join(diff, "\n"), "reason": strings.TrimSpace(reason),
	}
	emitSystemEvent(sessionID, store.KindApprovalRequest,
		fmt.Sprintf("@%s wants to change access of @%s", mgr.Handle, p.Handle), extras)
	return map[string]any{
		"status": "pending_approval", "approval_id": approvalID, "changes": diff,
		"note": "Nothing changed yet. The owner accepts or declines on the card; end your turn and do not resubmit.",
	}, nil
}

func (CaptainOps) labeler(ctx context.Context, owner string) func(string) string {
	cat, _ := globalTeam.OwnerCatalog(ctx, owner)
	return labelerOf(cat)
}

// resolveAccessApproval settles a Captain's access proposal from its
// card. false = approvalID is no access proposal of sessionID, and the
// caller goes on to the gate. Only the agent's owner decides: anyone
// else gets the same 410 as an unknown id.
//
// Accept re-checks the grants against the owner's catalog as it is NOW
// — access lost since the proposal is refused, never applied.
func resolveAccessApproval(c *tool.Ctx, sessionID, approvalID, decision string) bool {
	if globalTeam == nil {
		return false
	}
	h, ch, err := globalTeam.AccessProposal(c.Context(), approvalID)
	if err != nil || h.SessionID != sessionID {
		return false
	}
	if h.OwnerUserID != actorID(c) || h.Status != team.AccessPending {
		c.JSON(http.StatusGone, map[string]string{"error": "request id no longer pending (timed out or already resolved)"})
		return true
	}
	p, err := globalTeam.Get(c.Context(), h.AgentID)
	if err != nil || p.OwnerUserID != h.OwnerUserID {
		_ = globalTeam.SettleAccess(c.Context(), h.ID, team.AccessDeclined, actorName(c))
		c.JSON(http.StatusGone, map[string]string{"error": "the agent no longer exists"})
		return true
	}
	who := actorName(c)
	cat, _ := globalTeam.OwnerCatalog(c.Context(), h.OwnerUserID)
	label := labelerOf(cat)
	accept := decision == gate.DecisionApproveOnce || decision == gate.DecisionApproveSession
	if accept {
		cat, err := globalTeam.OwnerCatalog(c.Context(), h.OwnerUserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return true
		}
		if err := team.CheckGrants(ch.Grants, team.CatalogOf(cat)); err != nil {
			c.JSON(http.StatusConflict, map[string]string{"error": err.Error() + " — decline this request"})
			return true
		}
		if err := globalTeam.SettleAccess(c.Context(), h.ID, team.AccessApplied, who); err != nil {
			c.JSON(http.StatusGone, map[string]string{"error": "request id no longer pending (timed out or already resolved)"})
			return true
		}
		before := p
		p.AllowedConnectors = team.EncodeGrants(ch.Grants)
		if err := globalTeam.Update(c.Context(), &p); err != nil {
			c.JSON(teamAgentSaveStatus(err), map[string]string{"error": err.Error()})
			return true
		}
		if diff := accessChangeList(before, p, label); len(diff) > 0 {
			emitAccessChanged(p, diff, h.Actor, who, label)
		}
		decision = gate.DecisionApproveOnce
	} else {
		if err := globalTeam.SettleAccess(c.Context(), h.ID, team.AccessDeclined, who); err != nil {
			c.JSON(http.StatusGone, map[string]string{"error": "request id no longer pending (timed out or already resolved)"})
			return true
		}
		if s, ok := mainSessionOf(p.OwnerUserID, p.ID); ok {
			emitSystemEvent(s.ID, store.KindAccessChangeDeclined,
				fmt.Sprintf("Access change for @%s declined · proposed by %s · declined by %s", p.Handle, h.Actor, who),
				map[string]string{"agent_id": p.ID, "handle": p.Handle, "proposed_by": h.Actor, "declined_by": who,
					"changes": strings.Join(ch.Diff, ", "), "approval_id": approvalID})
		}
		decision = gate.DecisionBlock
	}
	outcome := "applied"
	if !accept {
		outcome = "declined"
	}
	emitSystemEvent(sessionID, store.KindApprovalRequest, outcome+" · by "+who, map[string]string{
		"approval_id": approvalID, "state": decision, "type": accessChangeType, "decided_by": who,
	})
	c.JSON(http.StatusOK, map[string]string{"status": "resolved", "decision": decision})
	return true
}
