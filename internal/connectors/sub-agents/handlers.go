package subagents

import (
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
		})
	}
	return map[string]any{"agents": out}, nil
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

	mode := strings.TrimSpace(c.Input("mode"))
	if !delegation.ValidMode(mode) {
		return nil, errors.New("mode must be 'sync' or 'async'")
	}
	sink := strings.TrimSpace(c.Input("delivery_sink"))
	if !delegation.ValidSink(sink) {
		return nil, errors.New("delivery_sink must be 'channel', 'session', or 'none'")
	}
	workspace := strings.TrimSpace(c.Input("workspace"))
	if !delegation.ValidWorkspace(workspace) {
		return nil, errors.New("workspace must be 'shared' or 'worktree'")
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

	provider := strings.TrimSpace(c.Input("provider"))
	if provider == "" {
		if existing != nil {
			provider = existing.Provider
		} else {
			provider = "claude"
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
		Provider:     provider,
		Model:        strings.TrimSpace(c.Input("model")),
		SystemPrompt: systemPrompt,
		// Narrowed against the caller's own tags, so an agent choosing a
		// role's tool access can only ever pick a SUBSET of what the human
		// who triggered it already has. Empty = inherit that set in full.
		AllowedTagIDs:      encodeTagIDs(delegation.NarrowTags(splitList(c.Input("allowed_tags")), caller.tagIDs)),
		AllowedNativeTools: "[]",
		StrictMCP:          true,
		DefaultMaxTurns:    c.InputInt("max_turns"),
		DefaultMode:        delegation.ModeSync,
		DefaultWorkspace:   delegation.WorkspaceShared,
		CanDelegate:        c.InputBool("can_delegate"),
		AllowTakeOver:      c.InputBool("allow_take_over"),
		CreatedBy:          caller.user.ID,
	}
	if mode := strings.TrimSpace(c.Input("mode")); mode != "" {
		if mode != delegation.ModeSync && mode != delegation.ModeAsync {
			return nil, fmt.Errorf("mode must be %q or %q, got %q",
				delegation.ModeSync, delegation.ModeAsync, mode)
		}
		p.DefaultMode = mode
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
		if c.InputInt("max_turns") <= 0 {
			p.DefaultMaxTurns = existing.DefaultMaxTurns
		}
		p.Model = strings.TrimSpace(c.Input("model"))
		if p.Model == "" {
			p.Model = existing.Model
		}
		p.AllowedNativeTools = existing.AllowedNativeTools
		p.StrictMCP = existing.StrictMCP
		p.DefaultWorkspace = existing.DefaultWorkspace
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
