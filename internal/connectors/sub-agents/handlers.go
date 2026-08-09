package subagents

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

// delegateMaxTaskRunes caps a delegated task so a runaway prompt cannot
// bloat the row or the child's first turn.
const delegateMaxTaskRunes = 8000

type handlers struct{ deps Deps }

func newHandlers(deps Deps) *handlers { return &handlers{deps: deps} }

/* ── delegation ──────────────────────────────────────────────────────── */

type roleItem struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Provider    string `json:"provider"`
	MaxTurns    int    `json:"default_max_turns"`
	// Scope is "project" for a role this project owns and "global"
	// otherwise. Surfaced so the agent can tell a project's own role from
	// one every project inherits.
	Scope string `json:"scope"`
	// MemoryMode is what this role is told by default beyond its task. A
	// caller that cannot see it cannot tell whether overriding is worth
	// the argument.
	MemoryMode string `json:"memory_mode"`
}

func (h *handlers) listAgents(c *connector.Ctx) (any, error) {
	caller, err := h.deps.resolveCaller(c.Context(), c.SessionID(), false)
	if err != nil {
		return nil, err
	}
	profiles, err := h.deps.visibleRoles(c.Context(), caller)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	out := make([]roleItem, 0, len(profiles))
	for _, p := range profiles {
		scope := "global"
		if p.ProjectID != "" {
			scope = "project"
		}
		out = append(out, roleItem{
			Key: p.Key, Name: p.Name, Description: p.Description,
			Provider: p.Provider, MaxTurns: p.DefaultMaxTurns, Scope: scope,
			MemoryMode: delegation.NormalizeMemoryMode("", p.DefaultMemoryMode),
		})
	}

	// Roles are what you can START. Instances are who is already HERE —
	// including the ones that have finished, which stay addressable and
	// continuable. Without this list a leader cannot tell "delegate a
	// reviewer" from "the reviewer that already read this is one message
	// away", and it will reliably pick the expensive one.
	return map[string]any{
		"agents":    out,
		"instances": h.instancesFor(c, caller),
	}, nil
}

// instanceItem is one sub-agent that exists in this conversation.
type instanceItem struct {
	Handle       string `json:"handle"`
	Role         string `json:"role"`
	DelegationID string `json:"delegation_id"`
	Status       string `json:"status"`
	// Live separates "working right now" from "stopped". Both are
	// reachable; what differs is what to do with them, and the note says
	// which is which rather than leaving the model to infer it from the
	// status string.
	Live bool   `json:"live"`
	Note string `json:"note,omitempty"`
}

// instancesFor lists the sub-agents already present in the caller's tree.
//
// Best-effort: a leader that cannot be told who exists is worse off, not
// broken, so a lookup failure costs the list rather than the whole call.
func (h *handlers) instancesFor(c *connector.Ctx, caller caller) []instanceItem {
	if caller.sessionID == "" {
		return nil
	}
	svc := h.deps.svc()

	// A LEADER's sub-agents are its direct children, and each top-level
	// delegate call starts its own tree (RootID == its own id). Listing by
	// root therefore showed only the most recent one and silently dropped
	// every earlier delegation — six dispatches, one visible row, and no
	// way to find the others once their ids scrolled out of the
	// conversation. Listing by parent is the query that matches the
	// question "who have I delegated to".
	//
	// A SUB-AGENT asking the same question means something different: it
	// wants its colleagues, which is its tree. That path keeps ListByRoot.
	var rows []entity.AgentDelegation
	if self, err := svc.Repo.FindByChildSession(c.Context(), caller.sessionID); err == nil && self != nil {
		rows, err = svc.Repo.ListByRoot(c.Context(), self.RootID)
		if err != nil {
			return nil
		}
	} else {
		var err error
		rows, err = svc.Repo.ListByParent(c.Context(), caller.sessionID)
		if err != nil {
			return nil
		}
	}
	out := make([]instanceItem, 0, len(rows))
	for _, d := range rows {
		if d.Handle == "" {
			continue
		}
		live := !entity.IsTerminalDelegationStatus(d.Status)
		note := "Finished. Use continue to carry its work further in the same session, or message it a question."
		if live {
			note = "Still working. Use message to steer it; do not continue or collect it yet."
		}
		out = append(out, instanceItem{
			Handle: d.Handle, Role: d.ProfileKey, DelegationID: d.ID,
			Status: d.Status, Live: live, Note: note,
		})
	}
	return out
}

func (h *handlers) delegate(c *connector.Ctx) (any, error) {
	caller, err := h.deps.resolveCaller(c.Context(), c.SessionID(), true)
	if err != nil {
		return nil, err
	}

	if ok, role, derr := h.deps.callerMayDelegate(c.Context(), c.SessionID()); derr != nil {
		return nil, derr
	} else if !ok {
		return nil, fmt.Errorf(
			"the %q role is not allowed to delegate. Do this part of the work yourself, "+
				"or report back to whoever delegated to you", role)
	}

	// continue_id turns this into a continuation. Routed AFTER the
	// may-delegate check, deliberately: a role barred from delegating is
	// barred from driving another agent by any spelling. Everything past
	// this point is about spawning something new, which is exactly what a
	// continuation must not do.
	if id := strings.TrimSpace(c.Input("continue_id")); id != "" {
		return h.continueDelegation(c)
	}

	profileKey := strings.TrimSpace(c.Input("profile"))
	if profileKey == "" {
		return nil, errors.New("profile is required — call list_agents to see the available keys")
	}
	task := strings.TrimSpace(c.Input("task"))
	if task == "" {
		return nil, errors.New("task is required")
	}
	if runes := []rune(task); len(runes) > delegateMaxTaskRunes {
		return nil, fmt.Errorf("task too long (max %d characters)", delegateMaxTaskRunes)
	}

	// Resolved in the caller's project scope, so a role this project
	// defines shadows the global one of the same key.
	profile, err := h.deps.svc().Repo.GetProfileScoped(c.Context(), caller.projectID, profileKey)
	if err != nil {
		if errors.Is(err, delegation.ErrProfileNotFound) {
			return nil, fmt.Errorf("no such role: %s — call list_agents for the list", profileKey)
		}
		return nil, fmt.Errorf("resolve role: %w", err)
	}
	// Same wording as "not found": do not confirm the existence of a role
	// this caller may not use.
	if len(delegation.VisibleProfiles([]entity.AgentProfile{*profile}, caller.tagIDs, caller.user.IsAdmin())) == 0 {
		return nil, fmt.Errorf("no such role: %s — call list_agents for the list", profileKey)
	}

	mode, modeOK := delegation.ParseMode(c.Input("mode"))
	if !modeOK {
		return nil, errors.New("mode must be 'background' or 'foreground'")
	}
	sink := strings.TrimSpace(c.Input("delivery_sink"))
	if !delegation.ValidSink(sink) {
		return nil, errors.New("delivery_sink must be 'channel', 'session', or 'none'")
	}
	workspace := strings.TrimSpace(c.Input("workspace"))
	if !delegation.ValidWorkspace(workspace) {
		return nil, errors.New("workspace must be 'shared' or 'worktree'")
	}
	memory := strings.TrimSpace(c.Input("memory_mode"))
	if !delegation.ValidMemoryMode(memory) {
		// Rejected at the boundary rather than quietly defaulted: a model
		// that asked for no_history and silently got state_summary would
		// reason about a context it did not want and never learn why.
		return nil, fmt.Errorf("memory_mode must be one of %s",
			strings.Join(delegation.MemoryModes(), ", "))
	}

	req := delegation.Request{
		ProfileKey:      profileKey,
		Task:            task,
		Context:         strings.TrimSpace(c.Input("context")),
		MaxTurns:        c.InputInt("max_turns"),
		MaxTokens:       c.InputInt("max_tokens"),
		Mode:            mode,
		DeliverySink:    sink,
		Workspace:       workspace,
		MemoryMode:      memory,
		Supervised:      c.InputBool("supervised"),
		ParentSessionID: caller.sessionID,
		ProjectID:       caller.projectID,
		TriggeredBy:     caller.user.ID,
	}
	// A leader that is itself a sub-agent inherits its parent's root,
	// depth and ancestor chain — that inheritance is what makes the depth
	// limit and the cycle guard work at all.
	if parent, perr := h.deps.svc().Repo.FindByChildSession(c.Context(), caller.sessionID); perr == nil && parent != nil {
		req.RootID = parent.RootID
		req.Depth = parent.Depth + 1
		req.AncestorKeys = delegation.Ancestors(parent)
		if parent.TriggeredBy != "" {
			req.TriggeredBy = parent.TriggeredBy
		}
	}

	res, err := h.deps.svc().Run(c.Context(), req)
	if err != nil {
		// A governor refusal is actionable guidance rather than a crash:
		// its message tells the model what to do instead.
		var refusal *delegation.Refusal
		if errors.As(err, &refusal) {
			return nil, errors.New(refusal.Message)
		}
		return nil, fmt.Errorf("delegate: %w", err)
	}
	return res, nil
}

// continueDelegation carries an existing sub-agent further in its own
// session.
//
// Authority mirrors collect and stop rather than delegate: this does not
// create work in the caller's tree, it drives a delegation that already
// exists, so the question is "may this caller touch that row" — which
// CanInterrupt already answers inside the service.
func (h *handlers) continueDelegation(c *connector.Ctx) (any, error) {
	caller, err := h.deps.resolveCaller(c.Context(), c.SessionID(), true)
	if err != nil {
		return nil, err
	}

	// Two spellings reach here: the continue op's own delegation_id, and
	// delegate's continue_id shortcut. Read both rather than duplicating
	// the handler, so the two surfaces cannot drift apart in behaviour.
	id := strings.TrimSpace(c.Input("delegation_id"))
	if id == "" {
		id = strings.TrimSpace(c.Input("continue_id"))
	}
	if id == "" {
		return nil, errors.New("delegation_id is required — it is in the reply from delegate, or from collect")
	}
	task := strings.TrimSpace(c.Input("task"))
	if task == "" {
		return nil, errors.New("task is required — say what the sub-agent should do next")
	}
	if runes := []rune(task); len(runes) > delegateMaxTaskRunes {
		return nil, fmt.Errorf("task too long (max %d characters)", delegateMaxTaskRunes)
	}
	mode, modeOK := delegation.ParseMode(c.Input("mode"))
	if !modeOK {
		return nil, errors.New("mode must be 'background' or 'foreground'")
	}

	res, err := h.deps.svc().Continue(c.Context(), delegation.ContinueRequest{
		DelegationID: id,
		Task:         task,
		ExtraTurns:   c.InputInt("max_turns"),
		ExtraTokens:  c.InputInt("max_tokens"),
		Mode:         mode,
		ActorID:      caller.user.ID,
		IsAdmin:      caller.user.IsAdmin(),
	})
	if err != nil {
		switch {
		case errors.Is(err, delegation.ErrForbidden):
			return nil, errors.New("that delegation belongs to someone else")
		case errors.Is(err, delegation.ErrDelegationNotFound):
			return nil, fmt.Errorf("no such delegation: %s", id)
		case errors.Is(err, delegation.ErrNotContinuable):
			// Passed through verbatim: the service message already names
			// the way out, and rewrapping it would bury that.
			return nil, err
		}
		var refusal *delegation.Refusal
		if errors.As(err, &refusal) {
			return nil, errors.New(refusal.Message)
		}
		return nil, fmt.Errorf("continue: %w", err)
	}
	return res, nil
}

func (h *handlers) collect(c *connector.Ctx) (any, error) {
	caller, err := h.deps.resolveCaller(c.Context(), c.SessionID(), true)
	if err != nil {
		return nil, err
	}
	id := strings.TrimSpace(c.Input("delegation_id"))
	if id == "" {
		items, cerr := h.deps.svc().CollectPending(c.Context(), caller.sessionID)
		if cerr != nil {
			return nil, fmt.Errorf("collect: %w", cerr)
		}
		return map[string]any{"results": items}, nil
	}

	res, err := h.deps.svc().Collect(c.Context(), id, caller.user.ID, caller.user.IsAdmin())
	if err != nil {
		switch {
		case errors.Is(err, delegation.ErrForbidden):
			return nil, errors.New("that delegation belongs to someone else")
		case errors.Is(err, delegation.ErrDelegationNotFound):
			return nil, fmt.Errorf("no such delegation: %s", id)
		default:
			return nil, fmt.Errorf("collect: %w", err)
		}
	}
	return res, nil
}

// progress records a sub-agent's mid-flight position and wakes whoever
// is supervising it.
//
// Scoped by construction, like report_result: the row is resolved from
// the CALLING session, so a sub-agent can only ever report on its own
// work. There is no delegation_id input, and there must not be — one
// would let an agent file progress against somebody else's run.
func (h *handlers) progress(c *connector.Ctx) (any, error) {
	if err := h.deps.ready(); err != nil {
		return nil, err
	}
	res, err := h.deps.svc().ReportProgress(c.Context(), c.SessionID(), delegation.ProgressReport{
		Note:    c.Input("note"),
		Done:    c.Input("done"),
		Next:    c.Input("next"),
		Blocked: c.Input("blocked"),
	})
	if err != nil {
		if errors.Is(err, delegation.ErrNotASubAgent) {
			return nil, errors.New(
				"only a sub-agent can report progress — this conversation is not a delegation, so there is nobody supervising it")
		}
		return nil, fmt.Errorf("progress: %w", err)
	}
	return res, nil
}

// reportResult records a sub-agent's structured answer.
//
// Refused for a leader, and said plainly rather than silently accepted: a
// leader has no delegation row to write to, and a model that believes it
// reported something it did not will close its turn thinking the job is
// done.
func (h *handlers) reportResult(c *connector.Ctx) (any, error) {
	if err := h.deps.ready(); err != nil {
		return nil, err
	}
	row, err := h.deps.svc().Repo.FindByChildSession(c.Context(), c.SessionID())
	if err != nil {
		return nil, fmt.Errorf("report_result: %w", err)
	}
	if row == nil {
		return nil, errors.New(
			"only a sub-agent can report a result — this conversation is not a delegation, so there is nothing to report to")
	}

	env := &delegation.ResultEnvelope{
		Summary:       strings.TrimSpace(c.Input("summary")),
		Confidence:    c.Input("confidence"),
		NeedsFollowup: c.InputBool("needs_followup"),
		Structured:    true,
	}
	if env.Summary == "" {
		return nil, errors.New("summary is required — it is what the delegating agent acts on")
	}
	for _, line := range strings.Split(c.Input("findings"), "\n") {
		if f := strings.TrimSpace(line); f != "" {
			env.Findings = append(env.Findings, f)
		}
	}
	if raw := strings.TrimSpace(c.Input("evidence")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &env.Evidence); err != nil {
			return nil, errors.New(`evidence must be a JSON array of {kind, source, excerpt}, e.g. [{"kind":"log","source":"loki: app=abc","excerpt":"401 signature_invalid"}]`)
		}
	}
	if raw := strings.TrimSpace(c.Input("recommended_next_tasks")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &env.RecommendedNextTasks); err != nil {
			return nil, errors.New(`recommended_next_tasks must be a JSON array of {role, task, reason}`)
		}
	}

	// Last call wins. Overwriting rather than refusing a second call: a
	// model correcting itself is doing the right thing, and the failure
	// mode of refusing is a stale answer nobody can replace.
	if err := h.deps.svc().Repo.SaveResultJSON(c.Context(), row.ID, env); err != nil {
		return nil, fmt.Errorf("report_result: %w", err)
	}
	return map[string]any{
		"recorded": true,
		"note":     "Recorded. Close with a SHORT message; do not repeat the whole report as prose.",
	}, nil
}

// incident reads or patches this conversation's investigation record.
//
// Scoped by construction: the caller's session resolves to a tree, and
// the tree resolves to an incident. There is no way to name somebody
// else's.
func (h *handlers) incident(c *connector.Ctx) (any, error) {
	caller, err := h.deps.resolveCaller(c.Context(), c.SessionID(), true)
	if err != nil {
		return nil, err
	}
	svc := h.deps.svc()
	root := svc.RootForSession(c.Context(), caller.sessionID)
	if root == "" {
		return nil, errors.New("this conversation has no sub-agents yet, so there is no investigation to record")
	}

	switch strings.TrimSpace(c.Input("action")) {
	case "get":
		return h.incidentGet(c, root)
	case "update":
		return h.incidentUpdate(c, root)
	case "close":
		inc, err := svc.CloseIncident(c.Context(), root, c.Input("final_summary"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"incident": inc, "note": "Closed. Only a human can reopen it."}, nil
	default:
		return nil, errors.New("action must be get, update, or close")
	}
}

func (h *handlers) incidentGet(c *connector.Ctx, root string) (any, error) {
	svc := h.deps.svc()
	inc, err := svc.IncidentForRoot(c.Context(), root)
	if err != nil {
		return nil, fmt.Errorf("incident: %w", err)
	}
	if inc == nil {
		// Not an error: "nothing recorded yet" is the normal state for
		// most conversations, and an error here would teach the model to
		// avoid the op.
		return map[string]any{
			"exists": false,
			"note":   "No incident recorded for this conversation yet. It appears by itself once a sub-agent quotes evidence, or when you call update.",
		}, nil
	}
	ev, err := svc.ListEvidence(c.Context(), inc.ID)
	if err != nil {
		return nil, fmt.Errorf("incident evidence: %w", err)
	}
	// Grouped by kind because that is how it is read: a supervisor asks
	// "what do the logs say" before it asks "what did agent 3 find".
	byKind := map[string][]entity.AgentEvidence{}
	for _, e := range ev {
		byKind[e.Kind] = append(byKind[e.Kind], e)
	}
	return map[string]any{
		"exists":         true,
		"incident":       inc,
		"evidence":       byKind,
		"evidence_count": len(ev),
	}, nil
}

func (h *handlers) incidentUpdate(c *connector.Ctx, root string) (any, error) {
	p := delegation.IncidentPatch{}
	// Only fields the caller actually sent are patched; an absent field
	// must not be read as "set this to empty".
	str := func(key string) *string {
		raw, ok := c.Inputs()[key]
		if !ok || strings.TrimSpace(raw) == "" {
			return nil
		}
		v := strings.TrimSpace(raw)
		return &v
	}
	jsonList := func(key string) (*string, error) {
		v := str(key)
		if v == nil {
			return nil, nil
		}
		if !json.Valid([]byte(*v)) {
			return nil, fmt.Errorf("%s must be valid JSON", key)
		}
		return v, nil
	}

	p.Title = str("title")
	p.UserIssue = str("user_issue")
	p.Summary = str("summary")
	p.Status = str("status")
	p.StopReason = str("stop_reason")

	var err error
	if p.Hypotheses, err = jsonList("hypotheses"); err != nil {
		return nil, err
	}
	if p.MissingEvidence, err = jsonList("missing_evidence"); err != nil {
		return nil, err
	}
	if p.NextActions, err = jsonList("next_actions"); err != nil {
		return nil, err
	}
	if p.ClientContext, err = jsonList("client_context"); err != nil {
		return nil, err
	}

	inc, err := h.deps.svc().UpdateIncident(c.Context(), root, p)
	if err != nil {
		return nil, err
	}
	return map[string]any{"incident": inc}, nil
}

/* ── roles ───────────────────────────────────────────────────────────── */

// createAgent creates or updates a role in the CALLER'S PROJECT.
//
// Project scope is not a limitation bolted on — it is what makes this op
// safe to expose to every user. A project role is reachable only from
// sessions in that project, so the blast radius of an agent inventing a
// role is the project it is already working in. Global roles are
// reachable from everywhere and stay admin-only, created through the
// admin API instead.
func (h *handlers) createAgent(c *connector.Ctx) (any, error) {
	caller, err := h.deps.resolveCaller(c.Context(), c.SessionID(), true)
	if err != nil {
		return nil, err
	}
	if caller.projectID == "" {
		return nil, errors.New(
			"this conversation is not in a project, so there is no scope to create a role in. " +
				"Move the session into a project first, or ask an admin to add a global role.")
	}
	// A role that may not delegate has no use for roles it could define.
	if ok, role, derr := h.deps.callerMayDelegate(c.Context(), c.SessionID()); derr != nil {
		return nil, derr
	} else if !ok {
		return nil, fmt.Errorf("the %q role is not allowed to define or change roles", role)
	}

	key := strings.TrimSpace(c.Input("key"))
	if key == "" {
		return nil, errors.New("key is required")
	}
	// Scope-exact, never resolved: a resolved lookup would find a GLOBAL
	// role of the same key and overwrite it, silently editing the role
	// every other project sees.
	existing, err := h.deps.svc().Repo.GetProfileExact(c.Context(), caller.projectID, key)
	if err != nil && !errors.Is(err, delegation.ErrProfileNotFound) {
		return nil, fmt.Errorf("look up role: %w", err)
	}
	// Before a single field is read: a locked role is frozen for MCP
	// entirely, and unlocking is a human action in the web UI. An agent
	// that could unlock what it locked would be guarding nothing.
	if err := delegation.CheckMutable(existing); err != nil {
		return nil, err
	}

	// Editing an existing role is a PATCH: omitted fields keep their
	// current value. Requiring the full role on every tweak would make
	// "raise its turn budget" a chance to silently drop its prompt.
	description := strings.TrimSpace(c.Input("description"))
	if description == "" {
		if existing == nil {
			return nil, errors.New(
				"description is required — it is what a delegating agent reads to decide when to pick this role")
		}
		description = existing.Description
	}
	systemPrompt := strings.TrimSpace(c.Input("system_prompt"))
	if systemPrompt == "" {
		if existing == nil {
			return nil, errors.New(
				"system_prompt is required — without it the role has no instructions and behaves like a generic assistant")
		}
		systemPrompt = existing.SystemPrompt
	}

	// Provider + model resolve as ONE unit, from a single level: an explicit
	// pair from the call, else the existing role's pair, else the calling
	// conversation's. Mixing levels would pair a provider with a model id
	// from another instance's registry, which resolves to the wrong model or
	// to none.
	//
	// Inheriting from the caller (rather than defaulting to "claude", as this
	// did before) is the point: an agent asked to create a sub-agent has no
	// way to know which instance the conversation runs on, so a hardcoded
	// default silently moved new roles onto a different provider — and onto a
	// different model — than the one the user was working with.
	providerKey := strings.TrimSpace(c.Input("provider"))
	model := strings.TrimSpace(c.Input("model"))
	if providerKey == "" {
		// Model alone cannot select a level: without a provider there is no
		// registry to resolve it against, so it is dropped with the level.
		model = ""
		switch {
		case existing != nil && strings.TrimSpace(existing.Provider) != "":
			providerKey, model = existing.Provider, existing.Model
		default:
			providerKey, model = caller.providerKey, caller.modelID
		}
	}
	name := strings.TrimSpace(c.Input("name"))
	if name == "" {
		if existing != nil {
			name = existing.Name
		} else {
			name = key
		}
	}

	p := &entity.AgentProfile{
		ProjectID:    caller.projectID,
		Key:          key,
		Name:         name,
		Description:  description,
		Provider:     providerKey,
		Model:        model,
		SystemPrompt: systemPrompt,
		// Narrowed against the caller's own tags, so an agent choosing a
		// role's tool access can only ever pick a SUBSET of what the human
		// who triggered it already has. Empty = inherit that set in full.
		AllowedTagIDs:      encodeTagIDs(delegation.NarrowTags(splitList(c.Input("allowed_tags")), caller.tagIDs)),
		Icon:               strings.TrimSpace(c.Input("icon")),
		AllowedNativeTools: encodeTagIDs(splitList(c.Input("allowed_native_tools"))),
		StrictMCP:          c.InputBool("strict_mcp"),
		DefaultMaxTokens:   c.InputInt("max_tokens"),
		Disabled:           c.InputBool("disabled"),
		DefaultMaxTurns:    c.InputInt("max_turns"),
		DefaultMode:        delegation.ModeAsync,
		DefaultWorkspace:   delegation.WorkspaceShared,
		CanDelegate:        c.InputBool("can_delegate"),
		AllowTakeOver:      c.InputBool("allow_take_over"),
		Locked:             c.InputBool("locked"),
		CreatedBy:          caller.user.ID,
	}
	if raw := strings.TrimSpace(c.Input("mode")); raw != "" {
		mode, ok := delegation.ParseMode(raw)
		if !ok {
			return nil, fmt.Errorf("mode must be %q or %q, got %q",
				delegation.ModeBackground, delegation.ModeForeground, raw)
		}
		p.DefaultMode = mode
	}
	if ws := strings.TrimSpace(c.Input("workspace")); ws != "" {
		if ws != delegation.WorkspaceShared && ws != delegation.WorkspaceWorktree {
			return nil, fmt.Errorf("workspace must be %q or %q, got %q",
				delegation.WorkspaceShared, delegation.WorkspaceWorktree, ws)
		}
		p.DefaultWorkspace = ws
	}
	if mm := strings.TrimSpace(c.Input("memory_mode")); mm != "" {
		if !delegation.ValidMemoryMode(mm) {
			return nil, fmt.Errorf("memory_mode must be one of %s, got %q",
				strings.Join(delegation.MemoryModes(), ", "), mm)
		}
		p.DefaultMemoryMode = mm
	}
	// A new role keeps the historical default when the caller says nothing.
	// Reading the raw input distinguishes "false" from "not mentioned".
	if existing == nil && c.Input("strict_mcp") == "" {
		p.StrictMCP = true
	}
	// Carry forward anything this call did not mention.
	if existing != nil {
		if strings.TrimSpace(c.Input("allowed_tags")) == "" {
			p.AllowedTagIDs = existing.AllowedTagIDs
		}
		if strings.TrimSpace(c.Input("mode")) == "" {
			p.DefaultMode = existing.DefaultMode
		}
		if c.Input("can_delegate") == "" {
			p.CanDelegate = existing.CanDelegate
		}
		if c.Input("allow_take_over") == "" {
			p.AllowTakeOver = existing.AllowTakeOver
		}
		if c.Input("locked") == "" {
			p.Locked = existing.Locked
		}
		if c.InputInt("max_turns") <= 0 {
			p.DefaultMaxTurns = existing.DefaultMaxTurns
		}
		p.Model = strings.TrimSpace(c.Input("model"))
		if p.Model == "" {
			p.Model = existing.Model
		}
		if strings.TrimSpace(c.Input("icon")) == "" {
			p.Icon = existing.Icon
		}
		if strings.TrimSpace(c.Input("allowed_native_tools")) == "" {
			p.AllowedNativeTools = existing.AllowedNativeTools
		}
		if c.Input("strict_mcp") == "" {
			p.StrictMCP = existing.StrictMCP
		}
		if c.InputInt("max_tokens") <= 0 {
			p.DefaultMaxTokens = existing.DefaultMaxTokens
		}
		if c.Input("disabled") == "" {
			p.Disabled = existing.Disabled
		}
		if strings.TrimSpace(c.Input("workspace")) == "" {
			p.DefaultWorkspace = existing.DefaultWorkspace
		}
		if strings.TrimSpace(c.Input("memory_mode")) == "" {
			p.DefaultMemoryMode = existing.DefaultMemoryMode
		}
	}
	if p.DefaultMaxTurns <= 0 {
		p.DefaultMaxTurns = delegation.DefaultMaxTurns
	}
	created := true
	if existing != nil {
		created = false
		p.ID = existing.ID
		p.CreatedBy = existing.CreatedBy
		p.CreatedAt = existing.CreatedAt
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if err := h.deps.svc().Repo.SaveProfile(c.Context(), p); err != nil {
		return nil, fmt.Errorf("save role: %w", err)
	}

	status := "updated"
	if created {
		status = "created"
	}
	return map[string]any{
		"status": status, "key": p.Key, "scope": "project", "project_id": p.ProjectID,
		"note": "Visible only inside this project. Delegate to it with the `delegate` op.",
	}, nil
}

/* ── task boards ─────────────────────────────────────────────────────── */

type taskItem struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Body       string `json:"body,omitempty"`
	Stage      string `json:"stage"`
	ClaimState string `json:"claim_state"`
	ProfileKey string `json:"profile_key,omitempty"`
	Priority   int    `json:"priority,omitempty"`
	Result     string `json:"result,omitempty"`
}

func tasksToItems(tasks []entity.AgentTask) []taskItem {
	out := make([]taskItem, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, taskItem{
			ID: t.ID, Title: t.Title, Body: t.Body, Stage: t.Stage,
			ClaimState: t.ClaimState, ProfileKey: t.ProfileKey,
			Priority: t.Priority, Result: t.Result,
		})
	}
	return out
}

// startErrText turns a claim-guard failure into guidance rather than a
// bare error, so the model corrects its sequence instead of retrying.
func startErrText(err error) error {
	if errors.Is(err, delegation.ErrTaskClaimed) {
		return errors.New("you do not hold that task — claim it first, and note another worker may have taken it")
	}
	return err
}

// tasks drives a shared board. Every action routes through the same repo
// methods the board UI uses, so the evidence gate and the claim guard
// apply identically to an agent and to a human — a gate enforced only on
// the web path would be bypassed by exactly the caller it constrains.
func (h *handlers) tasks(c *connector.Ctx) (any, error) {
	caller, err := h.deps.resolveCaller(c.Context(), c.SessionID(), true)
	if err != nil {
		return nil, err
	}
	repo := h.deps.svc().Repo

	boardKey := strings.TrimSpace(c.Input("board"))
	if boardKey == "" {
		return nil, errors.New("board is required")
	}
	board, err := repo.GetBoard(c.Context(), boardKey)
	if err != nil {
		if errors.Is(err, delegation.ErrBoardNotFound) {
			return nil, fmt.Errorf("no such board: %s", boardKey)
		}
		return nil, fmt.Errorf("resolve board: %w", err)
	}
	if board.Disabled {
		return nil, fmt.Errorf("board %s is disabled", boardKey)
	}

	// The worker identity is the calling session: a claim must be held by
	// something that can be found again and released if it dies.
	workerID := caller.sessionID

	action := strings.TrimSpace(c.Input("action"))
	switch action {
	case "list":
		tasks, lerr := repo.ListTasks(c.Context(), board.ID)
		if lerr != nil {
			return nil, fmt.Errorf("list tasks: %w", lerr)
		}
		return map[string]any{"tasks": tasksToItems(tasks)}, nil

	case "add":
		title := strings.TrimSpace(c.Input("title"))
		if title == "" {
			return nil, errors.New("title is required to add a task")
		}
		t := &entity.AgentTask{
			BoardID:    board.ID,
			Title:      title,
			Body:       strings.TrimSpace(c.Input("body")),
			ProfileKey: strings.TrimSpace(c.Input("profile")),
			Priority:   c.InputInt("priority"),
			// New work lands in `ready`, not `backlog`: a task an agent
			// enqueued is meant to be picked up, and parking it in backlog
			// would leave it invisible to every worker.
			Stage:     entity.StageReady,
			ColumnID:  "ready",
			CreatedBy: caller.user.ID,
		}
		if cerr := repo.CreateTask(c.Context(), t); cerr != nil {
			return nil, fmt.Errorf("add task: %w", cerr)
		}
		return map[string]any{"task_id": t.ID, "status": "added"}, nil

	case "claim":
		t, cerr := repo.ClaimTask(c.Context(), board.ID, workerID, strings.TrimSpace(c.Input("profile")))
		if cerr != nil {
			return nil, fmt.Errorf("claim: %w", cerr)
		}
		if t == nil {
			return map[string]any{
				"claimed": false,
				"note":    "No ready tasks on this board. Do not poll in a loop — do other work and check again later.",
			}, nil
		}
		return map[string]any{
			"claimed": true, "task_id": t.ID, "title": t.Title, "body": t.Body,
			"note": "You hold this task. Call action='start' when you begin, then 'complete' or 'fail' when done.",
		}, nil

	case "start":
		id := strings.TrimSpace(c.Input("task_id"))
		if id == "" {
			return nil, errors.New("task_id is required")
		}
		if serr := repo.StartTask(c.Context(), id, workerID, ""); serr != nil {
			return nil, startErrText(serr)
		}
		return map[string]any{"status": "started", "task_id": id}, nil

	case "complete", "fail":
		id := strings.TrimSpace(c.Input("task_id"))
		if id == "" {
			return nil, errors.New("task_id is required")
		}
		failed := action == "fail"
		evidence := strings.TrimSpace(c.Input("evidence"))
		result := strings.TrimSpace(c.Input("result"))

		if cerr := repo.CompleteTask(c.Context(), board, id, workerID, result, evidence, failed); cerr != nil {
			if errors.Is(cerr, delegation.ErrEvidenceRequired) {
				return nil, errors.New(
					"this board requires evidence before a task can be completed — supply `evidence`: " +
						"test output, a report, or a link that shows the work actually landed")
			}
			return nil, startErrText(cerr)
		}
		out := map[string]any{"status": "completed", "task_id": id}
		if failed {
			out["status"] = "failed"
		}
		if warn := delegation.GateWarning(board, evidence); warn != "" {
			out["warning"] = warn
		}
		return out, nil

	default:
		return nil, errors.New("action must be one of: list, add, claim, start, complete, fail")
	}
}

/* ── messaging ───────────────────────────────────────────────────────── */

// messageMaxRunes bounds one message the way delegate bounds a task.
const messageMaxRunes = 8000

func (h *handlers) message(c *connector.Ctx) (any, error) {
	if _, err := h.deps.resolveCaller(c.Context(), c.SessionID(), true); err != nil {
		return nil, err
	}
	root, from, err := h.deps.treePosition(c.Context(), c.SessionID())
	if err != nil {
		return nil, err
	}
	to := strings.TrimPrefix(strings.TrimSpace(c.Input("to")), "@")
	if to == "" {
		return nil, errors.New("to is required — call list_agents for the handles")
	}
	body := strings.TrimSpace(c.Input("body"))
	if body == "" {
		return nil, errors.New("body is required")
	}
	if len([]rune(body)) > messageMaxRunes {
		return nil, fmt.Errorf("message too long (max %d characters)", messageMaxRunes)
	}
	kind := strings.TrimSpace(c.Input("kind"))
	if kind == "" {
		kind = entity.MessageTell
	}

	res, err := h.deps.svc().SendMessage(c.Context(), delegation.SendInput{
		RootID: root, FromHandle: from, ToHandle: to, Body: body, Kind: kind,
	})
	if err != nil {
		// A governor refusal is guidance, not a crash: its text tells the
		// model what to do instead of messaging again.
		var refusal *delegation.Refusal
		if errors.As(err, &refusal) {
			return nil, errors.New(refusal.Message)
		}
		return nil, err
	}
	return res, nil
}

func (h *handlers) reply(c *connector.Ctx) (any, error) {
	if _, err := h.deps.resolveCaller(c.Context(), c.SessionID(), true); err != nil {
		return nil, err
	}
	_, from, err := h.deps.treePosition(c.Context(), c.SessionID())
	if err != nil {
		return nil, err
	}
	id := strings.TrimSpace(c.Input("message_id"))
	if id == "" {
		return nil, errors.New("message_id is required — it is shown with the question")
	}
	body := strings.TrimSpace(c.Input("body"))
	if body == "" {
		return nil, errors.New("body is required")
	}
	if err := h.deps.svc().Reply(c.Context(), id, from, body); err != nil {
		return nil, err
	}
	return map[string]any{"status": "sent"}, nil
}

func (h *handlers) stop(c *connector.Ctx) (any, error) {
	caller, err := h.deps.resolveCaller(c.Context(), c.SessionID(), true)
	if err != nil {
		return nil, err
	}
	root, _, err := h.deps.treePosition(c.Context(), c.SessionID())
	if err != nil {
		return nil, err
	}
	handle := strings.TrimPrefix(strings.TrimSpace(c.Input("handle")), "@")
	if handle == "" {
		return nil, errors.New("handle is required")
	}
	target, err := h.deps.svc().Repo.FindByHandle(c.Context(), root, handle)
	if err != nil {
		return nil, err
	}
	// Same wording for "absent" and "not yours": confirming that a handle
	// exists elsewhere would leak the shape of other conversations.
	if target == nil || !delegation.CanAgentStop(target, root) {
		return nil, fmt.Errorf("no such agent in this conversation: @%s", handle)
	}
	out, err := h.deps.svc().Interrupt(c.Context(), target.ID, caller.user.ID, caller.user.IsAdmin())
	if err != nil {
		return nil, err
	}
	return map[string]any{"handle": handle, "outcome": out}, nil
}

// listAccess reports what the caller can hand to a role.
//
// Discovery is the missing half of letting an agent choose tool access:
// without it the model can only guess tag ids, and a guessed id is
// silently dropped by the narrowing rule — which looks identical to a
// role that simply has no tools.
func (h *handlers) listAccess(c *connector.Ctx) (any, error) {
	caller, err := h.deps.resolveCaller(c.Context(), c.SessionID(), false)
	if err != nil {
		return nil, err
	}
	type tagItem struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	out := make([]tagItem, 0, len(caller.tagIDs))
	if namer, ok := any(h.deps.svc().Tags).(delegation.TagNamer); ok && len(caller.tagIDs) > 0 {
		tags, terr := namer.TagsByIDs(c.Context(), caller.tagIDs)
		if terr != nil {
			return nil, fmt.Errorf("resolve tags: %w", terr)
		}
		for _, t := range tags {
			out = append(out, tagItem{ID: t.ID, Name: t.Name})
		}
	} else {
		// No namer wired: ids alone are still usable, just harder to read.
		for _, id := range caller.tagIDs {
			out = append(out, tagItem{ID: id, Name: id})
		}
	}
	return map[string]any{
		"tags": out,
		"note": "Pass a subset of these ids as allowed_tags on create_agent to restrict a role. " +
			"Omitting allowed_tags gives the role everything listed here. A role can never exceed this set.",
	}, nil
}
