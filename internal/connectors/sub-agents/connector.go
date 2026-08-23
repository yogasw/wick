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
// That hop is paid back at the MCP layer: every enabled op here ALSO
// surfaces as a top-level wick_agent_<op> tool (see
// internal/mcp/handlers/wickmanager.go), routed through the same
// wick_execute path so visibility, per-op access and audit are
// identical. Delegation is called every turn of a multi-agent flow, and
// making the hot path one call keeps models from drifting to a
// provider's native agent tools.
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
// DefaultTags carry tags.Connector + tags.Platform — deliberately NOT
// tags.System. System is IsFilter+IsSystem, which no user can carry, so
// it hides the row from every non-admin; delegation has to be reachable
// by ordinary users or the feature does not exist for them. Platform is
// a plain group tag, so it categorises without restricting access.
func Module(deps Deps) connector.Module {
	m := Meta()
	m.DefaultTags = []tool.DefaultTag{tags.Connector, tags.Platform}
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
	// A shortcut onto the continue op, not a second implementation. Kept
	// to one sentence on purpose: this description already steers the
	// model's choice between spawning and following up, and spelling out
	// a second mode here is what makes it pick the wrong one.
	ContinueID string `wick:"desc=Set this to a delegation id to CONTINUE that sub-agent in its existing session instead of spawning a new one. See the continue op, which is the same thing said plainly."`
	Context    string `wick:"textarea;desc=Optional background the sub-agent needs. Not this conversation's transcript."`
	// Turn and token caps are clamped to the system ceiling, never raised.
	MaxTurns     int    `wick:"desc=Optional cap on the sub-agent's turns. Clamped to the system ceiling."`
	MaxTokens    int    `wick:"desc=Optional cap on tokens this sub-agent may spend. Clamped to the system ceiling."`
	Mode         string `wick:"desc=background (default) returns a delegation_id immediately and the result is delivered later. foreground blocks this call until the sub-agent answers — only for a short lookup you cannot continue without."`
	DeliverySink string `wick:"desc=Where a background result goes: session (default) wakes you with it, channel posts it to the chat thread, none records it only."`
	Workspace    string `wick:"desc=shared (default) or worktree for a private git worktree. Falls back to shared with a note on a non-git project."`
	MemoryMode   string `wick:"desc=What this sub-agent is told beyond its task. no_history = nothing. state_summary (default) = one line per finished sibling. relevant_chunks = your context field only, curated by you. full_history = every sibling's full result, for audit and debugging only — expensive, noisy, and it biases the agent toward earlier conclusions."`
	Supervised   bool   `wick:"desc=Ask this sub-agent to report progress as it works, waking you when it does. Turn it on for long work you intend to watch, so you can correct a wrong direction while it is still cheap. Off by default: a short task finishes before a report would be useful."`
}

type progressInput struct {
	Note    string `wick:"required;textarea;desc=Where you are now, in a sentence or two: what you just finished and what you are moving to."`
	Done    string `wick:"textarea;desc=Optional. What is finished so far, one item per line."`
	Next    string `wick:"textarea;desc=Optional. What you are about to do next."`
	Blocked string `wick:"textarea;desc=Optional. What is stopping you, if anything. Say it here rather than pushing on — the agent supervising you can unblock it."`
}

type continueInput struct {
	DelegationID string `wick:"required;desc=The delegation to carry further, from delegate or collect."`
	Task         string `wick:"required;textarea;desc=What to do NEXT. The sub-agent still has its earlier work in context, so give it the next step — restating the original brief invites it to start over."`
	MaxTurns     int    `wick:"desc=Extra turns to grant for this leg, ADDED to what it already spent. Omit for another full allowance."`
	MaxTokens    int    `wick:"desc=Extra tokens to grant for this leg, added the same way."`
	Mode         string `wick:"desc=background (default, keeps whatever it ran as before) or foreground."`
}

// Deliberately absent from continueInput: profile, workspace, memory_mode.
// All three were settled when the delegation was created, and changing
// one mid-life would continue a DIFFERENT agent than the one whose
// transcript is being resumed.

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

type reportResultInput struct {
	Summary              string `wick:"required;textarea;desc=Your finished answer in a few sentences. This is what the agent that delegated to you acts on."`
	Findings             string `wick:"textarea;desc=One finding per line. A finding is a conclusion you are prepared to defend."`
	Evidence             string `wick:"textarea;desc=JSON array of {kind, source, excerpt}. kind is log | code | doc | data | observation. Quote real material: a claim with no excerpt is a guess."`
	Confidence           string `wick:"desc=low, medium, or high — how sure you are of the summary overall. Anything else is recorded as unknown."`
	NeedsFollowup        bool   `wick:"desc=True when the task is not fully answered and someone should continue it."`
	RecommendedNextTasks string `wick:"textarea;desc=JSON array of {role, task, reason} for work you recommend dispatching next."`
}

type incidentInput struct {
	Action          string `wick:"required;desc=get | update | close."`
	Title           string `wick:"desc=Short incident title, for update."`
	UserIssue       string `wick:"textarea;desc=The problem as the user reported it, for update."`
	Summary         string `wick:"textarea;desc=Current best understanding, for update."`
	Status          string `wick:"desc=investigating | confirmed | escalated, for update. Use action=close to close."`
	Hypotheses      string `wick:"textarea;desc=JSON array of strings REPLACING the hypothesis list, for update. Omit to leave it unchanged."`
	MissingEvidence string `wick:"textarea;desc=JSON array of strings REPLACING the missing-evidence list, for update. Omit to leave it unchanged."`
	NextActions     string `wick:"textarea;desc=JSON array of strings REPLACING the next-actions list, for update. Omit to leave it unchanged."`
	ClientContext   string `wick:"textarea;desc=JSON object with the affected client (app id, name, environment), for update. Omit to leave it unchanged."`
	StopReason      string `wick:"desc=Why the investigation stopped, for update. An investigation that ends without saying why looks identical to one still running."`
	FinalSummary    string `wick:"textarea;desc=Closing summary. Required for close — it is what anyone reading this later gets."`
}

type createAgentInput struct {
	Key          string `wick:"required;desc=Stable handle other calls use, lowercase-kebab (e.g. code-reviewer)."`
	Description  string `wick:"required;textarea;desc=What this role is for. Read by the delegating agent to decide when to pick it — a vague description makes the role unusable."`
	SystemPrompt string `wick:"required;textarea;desc=The role's instructions. This becomes the sub-agent's system prompt."`
	Name         string `wick:"desc=Display name. Defaults to the key."`
	Icon         string `wick:"desc=A single emoji shown beside this role in lists. Optional."`
	Provider     string `wick:"desc=Agent runtime: claude, codex, wick, gemini. A specific instance may be named as type/name (e.g. codex/abc). Empty inherits whatever THIS conversation is running on — usually what you want, so omit it unless the role must run somewhere else."`
	Model        string `wick:"desc=Model id within the chosen provider. Only read when you also set provider, because a model id is scoped to one instance: omit both to inherit this conversation's provider AND model together. Empty with an explicit provider uses that provider's own default."`
	MaxTurns     int    `wick:"desc=Default turn budget for this role. Clamped to the system ceiling."`
	MaxTokens    int    `wick:"desc=Default token budget for one delegation of this role. 0 = the role adds no cap of its own, and the per-tree budget still applies."`
	// Tool access. Narrowed against your own tags server-side, so this can
	// only ever restrict a role — it can never grant it something you do
	// not already have.
	AllowedTags string `wick:"desc=Comma-separated tag ids limiting which tools/connectors this role may use. See list_access for what you can grant. Empty = the role inherits everything you can reach."`
	// Stored, returned, and read by nothing. Said plainly because an inert
	// field the model believes in is worse than an absent one.
	AllowedNativeTools string `wick:"desc=Comma-separated provider-native tool names (e.g. Read, Grep, WebSearch). NOT ENFORCED today: the value is stored but nothing forwards it to the spawn, so it does not restrict what this role can call."`
	StrictMCP          bool   `wick:"desc=Drop the host's own MCP servers from this role's spawn. NOT ENFORCED: no spawn passes --strict-mcp-config any more. wick injects its own MCP server per spawn with that caller's own token, so this would only affect the user's other servers. What a role may reach is enforced server-side by tags and per-op access instead."`
	CanDelegate        bool   `wick:"desc=Let this role delegate and define roles of its own. Off by default: most roles should do their own work."`
	AllowTakeOver      bool   `wick:"desc=Let a human send messages into this role mid-run. Its answers are then flagged as human-steered."`
	Mode               string `wick:"desc=background (default) runs this role detached and delivers its result later. foreground makes the caller block until it answers — pick it only for roles that answer in seconds."`
	Workspace          string `wick:"desc=Default working directory for this role: shared (default), or worktree for a private git worktree. Falls back to shared with a note on a non-git project."`
	MemoryMode         string `wick:"desc=Default for what this role is told beyond its task. One of no_history, state_summary (the default), relevant_chunks, full_history. A caller can override it per delegation."`
	Disabled           bool   `wick:"desc=Keep the role on record but hide it from every roster. A disabled role cannot be delegated to."`
	Locked             bool   `wick:"desc=Freeze this role. Once locked, no further edit or delete is accepted over MCP — only a human can unlock it in the web UI. One-way from here: you can lock, you cannot unlock."`
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
			connector.Op("list_agents", "List Roles and Live Sub-agents",
				"Returns two lists. `agents` is the ROLES you may delegate to — [{key, name, description, provider, default_max_turns}], "+
					"scoped so you see the global ones plus any this project defines, a project role shadowing a global one of the same key. "+
					"`instances` is the sub-agents that already EXIST in this conversation — [{handle, role, delegation_id, status, live}] — "+
					"including ones that have finished, which stay reachable. "+
					"Read `instances` FIRST. An agent that already did related work can be continued or messaged for a fraction of what a fresh "+
					"spawn costs, and it remembers what it did; delegating instead gets you a stranger who has to rediscover all of it. "+
					"Call this before delegate, continue, or message so you use a key or handle that exists.",
				emptyInput{}, h.listAgents, wickdocs.Docs{}),

			connector.Op("delegate", "Delegate a Task",
				"Hand one self-contained sub-task to another agent. Runs in the BACKGROUND by default: this call returns a "+
					"delegation_id and status 'running' or 'queued', NOT an answer. Say what you started, end your turn, and you are woken "+
					"when the result lands — do not sit in a loop calling collect, and never report a result you have not been given. "+
					"The sub-agent starts with a CLEAN context — it cannot see this conversation, so `task` must state everything it needs. "+
					"Dispatch several in one turn and they queue behind one another, one at a time per conversation. "+
					"Pass mode=foreground ONLY for a short lookup whose answer your very next sentence needs; it blocks this call and holds your process idle meanwhile. "+
					"Every finished result carries a `status`: 'done' is a complete answer; 'interrupted' means a HUMAN stopped it, so read the note and do NOT silently retry; "+
					"'stopped_max_turns' and 'stopped_budget' mean the result is PARTIAL — use what is there or ask the user. "+
					"Delegate when a sub-task wants a different role or would otherwise flood this conversation; do simple work yourself rather than paying a spawn.",
				delegateInput{}, h.delegate, wickdocs.Docs{}),

			// Not destructive: continuing ADDS work to a sub-agent that has
			// already stopped. Nothing is discarded and nothing is undone, so
			// defaulting the toggle off on every row would leave a leader able
			// to start work it cannot carry forward — the exact gap this op
			// exists to close.
			connector.Op("continue", "Continue a Sub-agent",
				"Carry an EXISTING delegation further in the SAME session, so the sub-agent keeps everything it "+
					"learned. This is how you follow up: a new delegate call spawns a stranger with a blank context, "+
					"while continue wakes the agent that did the work. "+
					"Use it when a sub-agent finished but the job is not done ('needs_followup'), when it stopped at "+
					"'stopped_max_turns' or 'stopped_budget' with partial work, or when you have reviewed its answer "+
					"and want the next step done. "+
					"`task` is the NEXT instruction, not the original brief — the sub-agent still has that. Restating it "+
					"makes it start over. "+
					"Turn and token grants are ADDED to what it already spent, so a turn-exhausted agent gets real room "+
					"rather than a budget it has already used. "+
					"Only for a delegation that has STOPPED: to steer one still working, use message. "+
					"Check `resumed` in the reply — false means the transcript could not be recovered and the agent is "+
					"working from nothing despite being in its old session, so your instruction must stand alone.",
				continueInput{}, h.continueDelegation, wickdocs.Docs{}),

			connector.Op("collect", "Collect a Background Result",
				"Pick up the result of a background delegation started earlier. Pass delegation_id, or omit it to list everything waiting for this conversation. "+
					"A delegation still running comes back pending=true — carry on with other work rather than looping on it. "+
					"A result is handed over ONCE: if the reply says it was already collected, you have seen it before and must not act on it twice.",
				collectInput{}, h.collect, wickdocs.Docs{}),

			connector.Op("progress", "Report Your Progress",
				"Tell the agent supervising you where you are, WHILE you are still working. It wakes them, so use it "+
					"when you reach something they would want to know: a milestone finished, a plan that changed, or "+
					"something blocking you. "+
					"Report meaning, not activity — 'auth handler works, writing tests now', never 'read three files'. "+
					"Do NOT wait for a reply; file it and keep working. If they want a change they will message you. "+
					"This is not your answer: finish with report_result, which is a different op and ends your work.",
				progressInput{}, h.progress, wickdocs.Docs{}),

			connector.Op("report_result", "Report Your Result",
				"Report your finished work as structured fields, so the agent that delegated to you can act on it without re-reading your prose. "+
					"Call this ONCE, as the last thing you do before your closing message. "+
					"Evidence must be QUOTED, not described: a source and an excerpt someone else could verify — a claim with no excerpt is a guess. "+
					"If you never call this, your closing message is recorded as the summary with confidence 'unknown', which tells the caller your findings were never actually asserted.",
				reportResultInput{}, h.reportResult, wickdocs.Docs{}),

			connector.Op("incident", "Work the Incident Record",
				"Read or update this conversation's incident record — the durable state of an investigation, so what you know survives a context that does not. "+
					"get returns status, iteration, summary, hypotheses, missing evidence, next actions, and the evidence collected so far grouped by kind. "+
					"update patches ONLY the fields you pass, so you can add a hypothesis without restating everything else. "+
					"close writes a terminal status with a final summary; a closed incident refuses further updates and only a human can reopen it. "+
					"There is no open action: the record appears by itself the first time there is something to store.",
				incidentInput{}, h.incident, wickdocs.Docs{}),
		),

		connector.Cat("Messaging", "Talk to the other agents working in this conversation.",
			connector.Op("message", "Message an Agent",
				"Send a message to an agent working under you, addressed by handle (list_agents shows who you can reach). "+
					"Scope: you can only message agents visible in YOUR list_agents. A sub-agent sees only itself — "+
					"it CANNOT message its siblings; to influence one, report to your supervisor (progress or ask) and let it re-steer. "+
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
