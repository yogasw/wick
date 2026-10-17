package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
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
	// A Captain in a ticket room reaches its ticket only: creating,
	// scheduling or reconfiguring agents would act outside it.
	if InTicketRoom(sessionID) {
		return entity.AgentPersona{}, team.ErrInTicketRoom
	}
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
		av = team.AvatarForCreate(av)
	}
	sys := in.SystemPrompt
	if strings.TrimSpace(sys) == "" {
		sys = defaultAgentSystemAddon
	}
	prov, err := captainProviderKey(ctx, mgr.OwnerUserID, in.Provider)
	if err != nil {
		return nil, err
	}
	pid, err := createAgentProjectFor(ctx, mgr.OwnerUserID, name, "", in.Description, sys, prov, in.Model, "")
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
		// An agent the Captain makes lets the Captain PROPOSE access to it
		// from the start: every proposal still waits for the owner's card
		// (or their auto-approve rule), so this only spares the owner a
		// trip to Settings › Captain per agent before the first request.
		CaptainCan: team.EncodeCaptainCan(team.CaptainCan{Persona: true, Access: true, Routines: true}),
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
	if len(in.AccessSuggestions) > 0 || in.NativeTools != nil {
		prop := accessProposal{Grants: in.AccessSuggestions, NativeTools: in.NativeTools,
			Reason: "suggested access for the new agent", Created: true}
		if prop.Grants == nil {
			prop.Grants = []team.ConnectorGrant{}
		}
		res, err := o.propose(ctx, sessionID, mgr, *p, prop)
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
	if in.Avatar != nil || in.AvatarImage != nil {
		av, err := o.nextAvatar(ctx, sessionID, p, in)
		if err != nil {
			return nil, err
		}
		if enc := team.EncodeAvatar(av); enc != p.Avatar {
			p.Avatar = enc
			changed = append(changed, "avatar")
			if err := globalTeam.Update(ctx, &p); err != nil {
				return nil, err
			}
		}
	}
	if (in.Provider != nil || in.Model != nil) && IsRemoteAgent(p) {
		return nil, fmt.Errorf("@%s is a remote agent; it has no provider here", p.Handle)
	}
	var prov *string
	if in.Provider != nil {
		k := ""
		if v := strings.TrimSpace(*in.Provider); !strings.EqualFold(v, "default") {
			var err error
			if k, err = captainProviderKey(ctx, mgr.OwnerUserID, v); err != nil {
				return nil, err
			}
		}
		prov = &k
	}
	if p.ProjectID != "" && (in.Name != nil || in.Description != nil || in.SystemPrompt != nil || prov != nil || in.Model != nil) {
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
		if prov != nil && *prov != meta.Defaults.Provider {
			meta.Defaults.Provider = *prov
			changed = append(changed, "provider")
			// A model id is scoped to one instance: a new provider drops
			// the old pin unless a model comes with it.
			if in.Model == nil && meta.Defaults.Model != "" {
				meta.Defaults.Model = ""
			}
		}
		if in.Model != nil {
			if meta.Defaults.Provider == "" && strings.TrimSpace(*in.Model) != "" {
				return nil, errors.New("set a provider before pinning a model (the server default has no fixed model list)")
			}
			if nv := modelWithProvider(meta.Defaults.Provider, *in.Model); nv != meta.Defaults.Model {
				meta.Defaults.Model = nv
				changed = append(changed, "model")
			}
		}
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

// captainProviderKey checks a provider the Captain names for an agent:
// "type/name" (a bare type means its default instance), an instance
// that exists, and one the OWNER may use — the same gate as picking it
// in Settings. "" stays "" (the server default).
func captainProviderKey(ctx context.Context, owner, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	key := normalizeProviderKey(raw)
	t, name, ok := splitProviderKey(key)
	if !ok {
		return "", fmt.Errorf("provider must be type/name, e.g. claude/claude_waba")
	}
	found := false
	var names []string
	for _, ps := range providerChoicesCached(ctx) {
		names = append(names, ps.Type+"/"+ps.Name)
		if ps.Type == string(t) && ps.Name == name {
			found = true
		}
	}
	if !found {
		return "", fmt.Errorf("no provider %s; available: %s", key, strings.Join(names, ", "))
	}
	if err := workflowProviderAccess(ctx, owner, string(t), name); err != nil {
		return "", err
	}
	return key, nil
}

// nextAvatar is p's avatar after in: the JSON look replaces shape, colour
// and kind (keeping a stored picture), a picture file is re-encoded and
// stored the way the Settings upload is, and switches the avatar to it.
func (CaptainOps) nextAvatar(ctx context.Context, sessionID string, p entity.AgentPersona, in teamagents.PersonaInput) (team.Avatar, error) {
	av := team.DecodeAvatar(p.Avatar)
	if in.Avatar != nil {
		var next team.Avatar
		if err := json.Unmarshal([]byte(*in.Avatar), &next); err != nil {
			return av, errors.New("avatar must be a JSON object {kind, shape, color, expression}")
		}
		next.Image, next.Events = av.Image, av.Events
		if next.Kind == team.AvatarKindImage && next.Image == nil {
			return av, errors.New("@" + p.Handle + " has no picture yet; pass avatar_image")
		}
		av = next
	}
	if in.AvatarImage != nil {
		sess, ok := globalMgr.Registry().Session(sessionID)
		if !ok {
			return av, errors.New("your session is gone; cannot read avatar_image")
		}
		cwd, err := resolveSessionCwd(sess)
		if err != nil {
			return av, err
		}
		path, err := safeJoin(cwd, strings.TrimSpace(*in.AvatarImage))
		if err != nil {
			return av, fmt.Errorf("avatar_image must be a file inside your working folder: %w", err)
		}
		st, err := os.Stat(path)
		if err != nil || !st.Mode().IsRegular() {
			return av, fmt.Errorf("avatar_image %q is not a readable file", *in.AvatarImage)
		}
		if st.Size() > team.AvatarImageMaxInput {
			return av, team.ErrAvatarImageTooLarge
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return av, err
		}
		img, err := team.ProcessAvatarImageLimited(ctx, raw)
		if err != nil {
			return av, err
		}
		if err := globalTeam.SaveAvatarImage(ctx, p.ID, img); err != nil {
			return av, errors.New("could not save the picture")
		}
		av.Kind = team.AvatarKindImage
		av.Image = &team.AvatarImage{V: img.Version, Aspect: float64(img.Width) / float64(max(1, img.Height))}
	}
	return team.NormalizeAvatar(av), nil
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
	return o.propose(ctx, sessionID, mgr, p, accessProposal{Grants: in.Grants, NativeTools: in.NativeTools, Reason: in.Reason})
}

// accessProposal is what a Captain asks for. Grants nil = keep the
// connectors, NativeTools nil = keep the tools; Created marks the
// suggestions agents.create files for the agent it just made.
type accessProposal struct {
	Grants      []team.ConnectorGrant
	NativeTools []string
	Reason      string
	Created     bool
}

// accessChangeOf is the history payload of prop on p.
func accessChangeOf(p entity.AgentPersona, prop accessProposal) team.AccessChange {
	ch := team.AccessChange{Grants: prop.Grants, NativeTools: prop.NativeTools}
	if prop.Grants == nil {
		ch.KeepConnectors = true
		ch.Grants = team.DecodeGrants(p.AllowedConnectors)
	}
	return ch
}

// withAccessChange is p with ch applied: the connector checklist unless
// ch keeps it, the native tools when ch carries them.
func withAccessChange(p entity.AgentPersona, ch team.AccessChange) entity.AgentPersona {
	if !ch.KeepConnectors {
		grants := ch.Grants
		if grants == nil {
			grants = []team.ConnectorGrant{}
		}
		p.AllowedConnectors = team.EncodeGrants(grants)
	}
	if ch.NativeTools != nil {
		p.AllowedNativeTools = team.EncodeNativeTools(ch.NativeTools)
	}
	return p
}

// fullAccessDiff is accessChangeList plus the native tools that move.
func fullAccessDiff(before, after entity.AgentPersona, label func(string) string) []string {
	diff := accessChangeList(before, after, label)
	return append(diff, team.NativeToolsDiff(team.DecodeNativeTools(before.AllowedNativeTools), team.DecodeNativeTools(after.AllowedNativeTools))...)
}

// applyAccessChange writes ch onto the agent behind p and announces it in
// that agent's chat; approvedBy names who let it through.
func applyAccessChange(ctx context.Context, p entity.AgentPersona, ch team.AccessChange, actor, approvedBy string, label func(string) string) error {
	before := p
	p = withAccessChange(p, ch)
	if err := globalTeam.Update(ctx, &p); err != nil {
		return err
	}
	if diff := fullAccessDiff(before, p, label); len(diff) > 0 {
		emitAccessChanged(p, diff, actor, approvedBy, label)
	}
	return nil
}

// autoApprovedBy is the decided_by of a proposal the Captain policy
// applied without a card.
const autoApprovedBy = "auto-approve (Captain policy)"

// propose checks the proposal against the OWNER's own catalog (the same
// check as Settings › Access) and records it. It then posts the approval
// card in the manager's chat, or applies it at once when the owner's
// Captain policy auto-approves it.
func (o CaptainOps) propose(ctx context.Context, sessionID string, mgr, p entity.AgentPersona, prop accessProposal) (map[string]any, error) {
	if err := team.ValidateNativeTools(prop.NativeTools); err != nil {
		return nil, err
	}
	cat, err := globalTeam.OwnerCatalog(ctx, mgr.OwnerUserID)
	if err != nil {
		return nil, err
	}
	ch := accessChangeOf(p, prop)
	if !ch.KeepConnectors {
		if err := team.CheckGrants(ch.Grants, team.CatalogOf(cat)); err != nil {
			return nil, err
		}
	}
	label := labelerOf(cat)
	diff := fullAccessDiff(p, withAccessChange(p, ch), label)
	if len(diff) == 0 {
		return nil, fmt.Errorf("@%s already has exactly this access", p.Handle)
	}
	ch.Diff = diff
	approvalID := uuid.New().String()
	by := "@" + mgr.Handle
	h := &entity.AgentAccessHistory{
		AgentID: p.ID, OwnerUserID: p.OwnerUserID, Actor: by, ActorAgentID: mgr.ID,
		Status: team.AccessPending, ApprovalID: approvalID, SessionID: sessionID,
	}
	if err := globalTeam.RecordAccess(ctx, h, ch); err != nil {
		return nil, err
	}
	reason := prop.Reason
	if team.DecodeCaptainPolicy(mgr.CaptainPolicy).AutoApproves(prop.Created) {
		if err := globalTeam.SettleAccess(ctx, h.ID, team.AccessApplied, autoApprovedBy); err != nil {
			return nil, err
		}
		if err := applyAccessChange(ctx, p, ch, by, autoApprovedBy, label); err != nil {
			return nil, err
		}
		emitSystemEvent(sessionID, store.KindAccessChanged,
			fmt.Sprintf("Access of @%s changed: %s · by %s · %s", p.Handle, strings.Join(diff, ", "), by, autoApprovedBy),
			map[string]string{"agent_id": p.ID, "handle": p.Handle, "changed_by": by, "approved_by": autoApprovedBy,
				"changes": strings.Join(diff, ", "), "approval_id": approvalID})
		return map[string]any{
			"status": "applied", "approval_id": approvalID, "changes": diff,
			"note": "Applied at once by the owner's Captain auto-approve rule; it is in the agent's Access history.",
		}, nil
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
		if err := team.CheckGrants(ch.Grants, team.CatalogOf(cat)); err != nil && !ch.KeepConnectors {
			c.JSON(http.StatusConflict, map[string]string{"error": err.Error() + " — decline this request"})
			return true
		}
		if err := globalTeam.SettleAccess(c.Context(), h.ID, team.AccessApplied, who); err != nil {
			c.JSON(http.StatusGone, map[string]string{"error": "request id no longer pending (timed out or already resolved)"})
			return true
		}
		if err := team.ValidateNativeTools(ch.NativeTools); err != nil {
			c.JSON(http.StatusConflict, map[string]string{"error": err.Error() + " — decline this request"})
			return true
		}
		if err := applyAccessChange(c.Context(), p, ch, h.Actor, who, label); err != nil {
			c.JSON(teamAgentSaveStatus(err), map[string]string{"error": err.Error()})
			return true
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
