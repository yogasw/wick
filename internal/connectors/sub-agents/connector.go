// Package subagents exposes sub-agent delegation as a fixed,
// single-instance connector.
//
// Delegation used to live as four top-level MCP tools (wick_delegate,
// wick_agents, wick_delegate_collect, wick_tasks). Moving it behind the
// connector contract buys discovery (wick_list / wick_search / wick_get),
// an admin page, tag visibility, and connector_runs audit — the same
// deal wickmanager takes — at the cost of an extra hop: the leader must
// resolve the instance id before it can execute an op.
//
// Unlike wickmanager this connector is NOT tagged System: every user's
// agent needs to be able to delegate, and a System tag would hide the
// whole feature from non-admins. Write access is governed per-op
// instead — see the scope rules on create_agent.
//
// File layout follows the standard wick connector split:
//
//   - connector.go — Meta, Configs, per-op Input structs, Operations.
//   - service.go   — Deps + the glue calling delegation.Service.
//   - handlers.go  — one handler per op.
package subagents

import (
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/tool"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// Key is the connector definition slug. Fixed=true, so wick auto-seeds
// exactly one row on first boot and the manager UI hides "+ New row",
// Duplicate and Delete.
const Key = "sub-agents"

// Configs is intentionally empty — this connector drives in-process wick
// services, not an external API. Kept as an explicit struct so the admin
// form renders "no config required" rather than nothing.
type Configs struct{}

// Meta returns the static metadata block for the registry.
func Meta() connector.Meta {
	return connector.Meta{
		Key:  Key,
		Name: "Sub-agents",
		Description: "Hand a self-contained task to another agent and get its answer back. " +
			"Use it when work wants a different role (research, review, migration) or when the " +
			"intermediate steps would flood this conversation. Also creates new roles and drives shared task boards.",
		Icon:  "🧩",
		Fixed: true,
	}
}

// Module returns the fully-wired connector.Module.
//
// DefaultTags carry tags.Connector only — deliberately NOT tags.System.
// System is IsFilter+IsSystem, which no user can carry, so it hides the
// row from every non-admin; delegation has to be reachable by ordinary
// users or the feature does not exist for them.
func Module(deps Deps) connector.Module {
	m := Meta()
	m.DefaultTags = []tool.DefaultTag{tags.Connector}
	return connector.Module{
		Meta:       m,
		Operations: Operations(deps),
	}
}

/* ── op inputs ───────────────────────────────────────────────────────── */

type emptyInput struct{}

type delegateInput struct {
	Profile string `wick:"required;desc=Role key from list_agents (e.g. researcher)."`
	Task    string `wick:"required;textarea;desc=The complete self-contained instruction. The sub-agent sees none of this conversation."`
	Context string `wick:"textarea;desc=Optional background the sub-agent needs. Not this conversation's transcript."`
	// Turn and token caps are clamped to the system ceiling, never raised.
	MaxTurns     int    `wick:"desc=Optional cap on the sub-agent's turns. Clamped to the system ceiling."`
	MaxTokens    int    `wick:"desc=Optional cap on tokens this sub-agent may spend. Clamped to the system ceiling."`
	Mode         string `wick:"desc=sync (default) blocks and returns the answer. async returns a delegation_id and delivers later."`
	DeliverySink string `wick:"desc=Where an async result goes: channel (default), session (wake the caller), none (record only)."`
	Workspace    string `wick:"desc=shared (default) or worktree for a private git worktree. Falls back to shared with a note on a non-git project."`
}

type messageInput struct {
	To   string `wick:"required;desc=Handle of the agent to message, without the @ (see list_agents)."`
	Body string `wick:"required;textarea;desc=What you want to say or ask."`
	Kind string `wick:"desc=tell (default) sends and returns immediately. ask waits for that agent's answer."`
}

type replyInput struct {
	MessageID string `wick:"required;desc=The id of the question you are answering, shown with the question."`
	Body      string `wick:"required;textarea;desc=Your answer."`
}

type stopInput struct {
	Handle string `wick:"required;desc=Handle of the agent to stop, without the @."`
}

type collectInput struct {
	DelegationID string `wick:"desc=The delegation to collect. Omit to list every async result waiting for this conversation."`
}

type createAgentInput struct {
	Key         string `wick:"required;desc=Stable handle other calls use, lowercase-kebab (e.g. code-reviewer)."`
	Description string `wick:"required;textarea;desc=What this role is for. Read by the delegating agent to decide when to pick it — a vague description makes the role unusable."`
	SystemPrompt string `wick:"required;textarea;desc=The role's instructions. This becomes the sub-agent's system prompt."`
	Name         string `wick:"desc=Display name. Defaults to the key."`
	Provider     string `wick:"desc=Agent runtime: claude (default), codex, wick, gemini."`
	Model        string `wick:"desc=Provider-specific model id. Empty uses the provider default."`
	MaxTurns     int    `wick:"desc=Default turn budget for this role. Clamped to the system ceiling."`
	// Tool access. Narrowed against your own tags server-side, so this can
	// only ever restrict a role — it can never grant it something you do
	// not already have.
	AllowedTags   string `wick:"desc=Comma-separated tag ids limiting which tools/connectors this role may use. See list_access for what you can grant. Empty = the role inherits everything you can reach."`
	CanDelegate   bool   `wick:"desc=Let this role delegate and define roles of its own. Off by default: most roles should do their own work."`
	AllowTakeOver bool   `wick:"desc=Let a human send messages into this role mid-run. Its answers are then flagged as human-steered."`
	Mode          string `wick:"desc=sync (default) returns the answer to the caller. async returns immediately and delivers later."`
}

type tasksInput struct {
	Action   string `wick:"required;desc=list | add | claim | start | complete | fail"`
	Board    string `wick:"required;desc=Board key."`
	TaskID   string `wick:"desc=Target task, for start/complete/fail."`
	Title    string `wick:"desc=Task title, for add."`
	Body     string `wick:"textarea;desc=Task detail, for add."`
	Profile  string `wick:"desc=Role the task is meant for."`
	Result   string `wick:"textarea;desc=Outcome text, for complete/fail."`
	Evidence string `wick:"textarea;desc=Proof of completion (test output, report, link). Some boards refuse a completion without it."`
	Priority int    `wick:"desc=Higher is claimed first. Default 0."`
}

/* ── operations ──────────────────────────────────────────────────────── */

// Operations builds the closure-bound op list. Handlers capture deps so
// each op reaches wick services without a global accessor.
func Operations(deps Deps) []connector.Category {
	h := newHandlers(deps)
	return []connector.Category{
		connector.Cat("Delegation", "Hand work to another agent and collect the answer.",
			connector.Op("list_agents", "List Roles",
				"List the sub-agent roles you may delegate to. Returns [{key, name, description, provider, default_max_turns}]. "+
					"Roles are scoped: you see the global ones plus any this project defines, with a project role shadowing a global one of the same key. "+
					"Call this before delegate so you use a key that exists.",
				emptyInput{}, h.listAgents, wickdocs.Docs{}),

			connector.Op("delegate", "Delegate a Task",
				"Delegate one self-contained sub-task to another agent and WAIT for its result. "+
					"The sub-agent starts with a CLEAN context — it cannot see this conversation, so `task` must state everything it needs. "+
					"It returns its final answer as this call's result, carrying a `status`: "+
					"'done' is a complete answer; 'interrupted' means a HUMAN stopped it, so read the note and do NOT silently retry; "+
					"'stopped_max_turns' and 'stopped_budget' mean the result is PARTIAL — use what is there or ask the user. "+
					"Delegate when a sub-task wants a different role or would otherwise flood this conversation; do simple work yourself rather than paying a spawn.",
				delegateInput{}, h.delegate, wickdocs.Docs{}),

			connector.Op("collect", "Collect an Async Result",
				"Pick up the result of an async delegation started earlier. Pass delegation_id, or omit it to list everything waiting for this conversation. "+
					"A delegation still running comes back pending=true — carry on with other work rather than looping on it. "+
					"A result is handed over ONCE: if the reply says it was already collected, you have seen it before and must not act on it twice.",
				collectInput{}, h.collect, wickdocs.Docs{}),
		),

		connector.Cat("Messaging", "Talk to the other agents working in this conversation.",
			connector.Op("message", "Message an Agent",
				"Send a message to another agent already working in this conversation, addressed by handle (list_agents shows them). "+
					"kind=tell delivers it and returns immediately — use it to report progress or hand over information. "+
					"kind=ask waits for that agent's answer and returns it, for something you cannot continue without. "+
					"The recipient keeps the context of its own work, so you do not need to re-explain what it is doing. "+
					"Every message counts against this conversation's shared budget and its hop limit; when the hop limit runs out, "+
					"summarise and report to the user instead of messaging again.",
				messageInput{}, h.message, wickdocs.Docs{}),

			connector.Op("reply", "Answer a Question",
				"Answer a question another agent asked you. Pass the message_id shown with the question. "+
					"If you finish your turn without replying, your closing message is sent as the answer automatically — "+
					"so reply explicitly whenever the answer matters.",
				replyInput{}, h.reply, wickdocs.Docs{}),

			// Not destructive: stopping returns the sub-agent's partial work
			// as a normal result rather than discarding it, and the human
			// interrupt path is ungated for the same reason. Marking it
			// destructive would default it off on every row, leaving a leader
			// able to start work it cannot stop.
			connector.Op("stop", "Stop an Agent",
				"Stop another agent in this conversation. Its partial work is kept and returned, not discarded. "+
					"Use it when an agent is stuck, redundant, or working on something no longer needed.",
				stopInput{}, h.stop, wickdocs.Docs{}),
		),

		connector.Cat("Roles", "Define the roles you can delegate to.",
			connector.Op("list_access", "List Grantable Tool Access",
				"List the tool-access tags you can give a role, as {id, name}. Call this before create_agent when you want to "+
					"restrict what a role may reach: pass a subset of these ids as allowed_tags. Omitting allowed_tags gives the "+
					"role everything you can reach, which is the right default for a role doing work on your behalf. "+
					"A role can never exceed this set, so narrowing is the only thing allowed_tags can do.",
				emptyInput{}, h.listAccess, wickdocs.Docs{}),

			connector.Op("create_agent", "Create or Update a Role",
				"Create a sub-agent role, or update one you already own. "+
					"The role is created in THIS conversation's project, so it is visible only inside that project; "+
					"a role with the same key as a global one shadows it here without touching the global role. "+
					"Creating a GLOBAL role is admin-only and is not done through this op. "+
					"Create a role when you will delegate the same kind of work repeatedly — for one-off work, just pass a good `task` to delegate.",
				createAgentInput{}, h.createAgent, wickdocs.Docs{}),
		),

		connector.Cat("Task boards", "Shared work queues that outlive one conversation.",
			connector.Op("tasks", "Work a Task Board",
				"Work a shared task board: enqueue work, claim the next task, report progress, and complete it. "+
					"Actions: list, add, claim, start, complete, fail. "+
					"Claim before you start, and only complete a task you claimed. "+
					"Some boards require `evidence` — concrete proof such as test output or a link — before a task may be completed; "+
					"a claim of success without it is rejected or flagged.",
				tasksInput{}, h.tasks, wickdocs.Docs{}),
		),
	}
}
