package wickmanager

import (
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/promptguard"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

// ── prompt_* ─────────────────────────────────────────────────────────
//
// The operator prompts every spawn carries. Reading is open to every
// signed-in user; prompt_set and prompt_rollback are admin-only and only
// FILE a proposal: an admin applies it by clicking Accept on the approval
// card in a browser (promptguard.Service.Decide). Nothing here writes a
// prompt, so an agent running as an admin cannot change its own rules.

type promptKeyInput struct {
	Key string `wick:"required;dropdown=system_prompt_security|system_prompt|system_prompt_team;desc=Operator prompt: system_prompt_security (security rules every session carries), system_prompt (project sessions), system_prompt_team (Team agents)."`
}

type promptSetInput struct {
	Key    string `wick:"required;dropdown=system_prompt_security|system_prompt|system_prompt_team;desc=Operator prompt to change."`
	Value  string `wick:"required;textarea;desc=The full new prompt text (it replaces the whole prompt, it is not appended)."`
	Reason string `wick:"textarea;desc=Why, shown on the approval card."`
}

type promptHistoryInput struct {
	Key   string `wick:"required;dropdown=system_prompt_security|system_prompt|system_prompt_team|team_instructions;desc=Prompt whose history to list. team_instructions = your own Team instructions."`
	Limit int    `wick:"number;desc=Max rows, newest first. 0 = 20."`
}

type promptRollbackInput struct {
	Key     string `wick:"required;dropdown=system_prompt_security|system_prompt|system_prompt_team;desc=Operator prompt to roll back."`
	Version int    `wick:"required;number;desc=Version to restore (see prompt_history)."`
	Reason  string `wick:"textarea;desc=Why, shown on the approval card."`
}

func (h *handlers) promptGet(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	key := c.Input("key")
	user, err := requireUser(ctx)
	defer func() { logOp(user, "prompt_get", map[string]string{"key": key}, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	if h.deps.Prompts == nil {
		err = promptguard.ErrUnavailable
		return nil, err
	}
	value, version, err := h.deps.Prompts.Current(ctx, key)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"key": key, "value": value, "version": version, "bytes": len(value)}
	if hist, herr := h.deps.Prompts.History(ctx, promptguard.ConfigTarget(key), 50); herr == nil {
		var pending []string
		for _, e := range hist {
			if e.Status == promptguard.StatusPending {
				pending = append(pending, e.ID)
			}
		}
		if len(pending) > 0 {
			out["pending_proposals"] = pending
		}
	}
	return out, nil
}

func (h *handlers) promptSet(c *connector.Ctx) (any, error) {
	return h.proposePrompt(c, "prompt_set", promptguard.ProposeInput{
		Key: c.Input("key"), Value: c.Input("value"), Reason: c.Input("reason"),
	})
}

func (h *handlers) promptRollback(c *connector.Ctx) (any, error) {
	v := c.InputInt("version")
	if v <= 0 {
		return nil, promptguard.ErrNoVersion
	}
	return h.proposePrompt(c, "prompt_rollback", promptguard.ProposeInput{
		Key: c.Input("key"), Reason: c.Input("reason"), RollbackOf: v,
	})
}

// proposePrompt files the proposal in the calling session; the card it
// posts is the only way it gets applied.
func (h *handlers) proposePrompt(c *connector.Ctx, op string, in promptguard.ProposeInput) (any, error) {
	start := time.Now()
	ctx := c.Context()
	args := map[string]any{"key": in.Key, "bytes": len(in.Value), "rollback_of": in.RollbackOf, "session_id": c.SessionID()}
	user, err := requireAdmin(ctx)
	var ch *entity.PromptChange
	defer func() {
		var after any
		if ch != nil {
			after = map[string]any{"approval_id": ch.ID, "status": ch.Status}
		}
		logOpDiff(user, op, args, nil, after, err, time.Since(start))
	}()
	if err != nil {
		return nil, err
	}
	if h.deps.Prompts == nil {
		err = promptguard.ErrUnavailable
		return nil, err
	}
	in.SessionID = c.SessionID()
	in.Actor = promptguard.Actor{UserID: user.ID, Name: userLabel(user)}
	if h.deps.AgentOf != nil && in.SessionID != "" {
		in.Actor.Agent = h.deps.AgentOf(in.SessionID)
	}
	var diff []string
	ch, diff, err = h.deps.Prompts.Propose(ctx, in)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status": "pending_approval", "approval_id": ch.ID, "key": in.Key,
		"base_version": ch.BaseVersion, "changes": diff,
		"note": "Nothing changed yet. An admin accepts or declines on the approval card in this chat, from the browser; end your turn and do not resubmit.",
	}, nil
}

func (h *handlers) promptHistory(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	key := c.Input("key")
	user, err := requireUser(ctx)
	defer func() { logOp(user, "prompt_history", map[string]string{"key": key}, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	if h.deps.Prompts == nil {
		err = promptguard.ErrUnavailable
		return nil, err
	}
	var target string
	switch {
	case key == promptguard.KeyTeamPrompt:
		target = promptguard.TeamTarget(user.ID)
	case promptguard.Managed(key):
		target = promptguard.ConfigTarget(key)
	default:
		err = promptguard.ErrUnknownKey
		return nil, err
	}
	hist, err := h.deps.Prompts.History(ctx, target, c.InputInt("limit"))
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(hist))
	for _, e := range hist {
		r := map[string]any{
			"id": e.ID, "version": e.Version, "status": e.Status, "via": e.Via,
			"actor": e.Actor, "actor_agent": e.ActorAgent, "reason": e.Reason,
			"decided_by": e.DecidedBy, "at": e.At, "bytes": len(e.Value), "diff": e.Diff,
		}
		if e.RollbackOf > 0 {
			r["rollback_of"] = e.RollbackOf
		}
		if e.Status != promptguard.StatusApplied {
			r["base_version"] = e.BaseVersion
		}
		rows = append(rows, r)
	}
	return map[string]any{"key": key, "changes": rows}, nil
}

// userLabel is how a person is named in the history.
func userLabel(u *entity.User) string {
	if n := strings.TrimSpace(u.Name); n != "" {
		return n
	}
	return u.Email
}
