export type AskOption = { label: string; value: string; description?: string };

export type AskField = {
  key: string;
  label?: string;
  help?: string;
  type: "rank" | "choice" | "multi" | "dropdown" | "text" | "secret" | "number" | string;
  required?: boolean;
  options?: AskOption[];
  allow_freeform?: boolean;
  placeholder?: string;
  value?: string;
};

export type AskRequest = {
  id: string;
  question?: string;
  options?: AskOption[];
  fields?: AskField[];
  allow_freeform?: boolean;
};

export type AskAnswer =
  | { id: string; value: string }
  | { id: string; text: string }
  | { id: string; values: Record<string, string> };

/** Tri-state for one configurable widget CSP directive. Mirrors
    config.Mode* in Go: block = 'none', list = the allowlist, all = any
    HTTPS host. An unknown value is treated as block. */
export type WidgetMode = "block" | "list" | "all";

/** The preset that decides the whole posture. Mirrors config.Preset* in
    Go: secure seals everything, unsecure opens everything, and only custom
    reads the per-directive fields. */
export type WidgetPreset = "secure" | "unsecure" | "custom";

/** HTML-artifact CSP policy as resolved by the backend. Directive fields
    are plain strings rather than WidgetMode because the wire value is
    operator-supplied and may be anything; the renderer narrows it.

    On a RESOLVED policy the preset has already been expanded, so every
    directive field states its real mode and `mode` is informational. */
export type WidgetPolicy = {
  mode?: string;
  frame_src?: string;
  img_src?: string;
  media_src?: string;
  connect_src?: string;
  /** External scripts only — the widget's own inline scripts always run. */
  script_src?: string;
  allow_popups?: boolean;
  /** Drops the sandbox flags an opened tab would inherit, so it gets a real
      origin instead of an opaque one. Implies allow_popups when rendered. */
  allow_popup_escape?: boolean;
  allowlist?: string[];
};

export type SessionListItem = {
  id: string;
  label: string;
  status: string;
  project_id: string;
  active_agent: string;
  created_at: string;
  last_active: string;
  lifecycle: string;
  pid?: number;
};

export type SessionMeta = {
  id: string;
  label: string;
  status: string;
  project_id: string;
  active_agent: string;
  title_custom: boolean;
  created_at: string;
  last_active: string;
  /** "type/name" provider key of the active agent — distinct from
      active_agent (the agent's own name, e.g. "main"). */
  provider?: string;
  /** Pinned model id on the active agent (wick only, currently). */
  model_id?: string;
  /** Resolved HTML-artifact CSP policy (global + this session's project
      override, already merged server-side). Absent on an older backend, in
      which case the SPA falls back to the fully-blocked policy. */
  widget?: WidgetPolicy;
};

export type TurnEvent = {
  type: string;
  tool_name?: string;
  tool_input?: string;
  tool_use_id?: string;
  is_error?: boolean;
  text?: string;
  /** RFC3339 — when this event arrived (tool_use) / when the tool
      finished (tool_result's end_at). Absent on older recorded traces. */
  at?: string;
  end_at?: string;
};

export type Attachment = {
  name: string;
  stored_name: string;
  url: string;
  mime: string;
  size: number;
};

export type Artifact = {
  name: string;
  path: string;
  url: string;
  download_url: string;
  kind: "image" | "pdf" | "html" | "markdown" | "text" | "file";
  mime?: string;
  size?: number;
};

// Sender is who a user turn came from, resolved by the channel from its
// transport envelope. Mirrors agentstore.Sender.
export type Sender = {
  id: string;
  name?: string;
  handle?: string;
  // "slack" | "telegram" | "rest" | "ui"
  channel: string;
  wick_user_id?: string;
};

export type ConversationTurn = {
  turn_id: string;
  role: string;
  agent: string;
  provider: string;
  text: string;
  // Origin of a user turn: "ui" (web composer), "slack", "telegram",
  // "schedule", … Absent/"ui" → no source badge. Persisted server-side
  // (agentstore.ConversationTurn.Source) and carried on live user_message
  // events. Used to badge messages that didn't come from this web session.
  source?: string;
  // Who sent a user turn, as the originating channel resolved it from its
  // own transport envelope — never parsed out of `text`, so a message body
  // claiming to be someone else cannot change it. Absent on turns with no
  // human behind them (scheduled runs, system messages) and on turns written
  // before this field existed.
  sender?: Sender;
  // RFC3339 string from history payload (Go struct `json:"ts"`). Live turns
  // built client-side only set `timestamp` (epoch ms) — read either.
  ts?: string;
  timestamp: number;
  truncated: boolean;
  interrupted: boolean;
  has_trace: boolean;
  events: TurnEvent[];
  attachments: Attachment[];
  has_artifact?: boolean;
  artifacts?: Artifact[];
  // system turn only — a provider/runtime error, rendered as a failure.
  is_error?: boolean;
};

export type ApprovalRequest = {
  id: string;
  agent_name: string;
  tool: string;
  work_dir: string;
  cmd: string;
  match_key: string;
  // Seconds left before the daemon blocks this command on its own.
  // Absent or 0 when the approval timeout is switched off: the prompt
  // then stays open while a tab is watching the session, so there is no
  // deadline to count down to.
  expires_in_sec?: number;
};

export type ApprovedItem = {
  match_key: string;
  scope: "session" | "always";
};

export type ApprovalDecision =
  | "approve_once"
  | "approve_session"
  | "approve_always"
  // Refuse and end the agent's turn.
  | "block"
  // Refuse this one command but keep the turn alive, handing the agent
  // a reason so it can take a different route. Requires a reason.
  | "guide";

export type ApprovalsResponse = {
  pending: ApprovalRequest[];
  session_approved: ApprovedItem[];
  always_approved: ApprovedItem[];
};

export type ContextFileEntry = {
  path: string;
  name: string;
  size: number;
  isDir: boolean;
  mtime: number;
};

/* Mirror of @wick-fe/common-ui's ComposerCommand (kept local to avoid a
   type-only import through the common-ui barrel). Structurally identical, so a
   ComposerCommand[] built here is assignable to the shared Composer's prop. */
export type ComposerCommand = {
  value: string;
  label: string;
  hint?: string;
  category?: string;
  run?: () => void;
};

export type ProcessInfo = {
  session_id: string;
  agent_name: string;
  provider: string;
  pid: number;
  queued: number;
  lifecycle: string;
  substate?: string;
  alive: boolean;
  // "process" = a real running/queued slot (counts, renders a card).
  // "idle" = no process at all; row carries only the provider/agent name
  // for the composer toolbar and must NOT be counted or shown as a card.
  // Optional for backward compat with older payloads (treated as process).
  kind?: "process" | "idle";
};

// SubAgentStatus mirrors entity.DelegationStatuses in Go exactly. The two
// lists are compared by a Go test, so adding a status on one side without
// the other fails the build rather than silently rendering an unknown
// status as blank.
export type SubAgentStatus =
  | "queued"
  | "running"
  | "done"
  | "failed"
  | "interrupted"
  | "stopped_max_turns"
  | "stopped_budget";

// SubAgentItem is one row in the Sub-agents rail panel — a single
// delegation from this conversation to another agent profile.
export type SubAgentItem = {
  delegation_id: string;
  child_session_id: string;
  profile_key: string;
  /** Address inside the delegation tree — what an @mention resolves to.
      Absent on rows written before handles existed. */
  handle?: string;
  // label is the delegated task, truncated server-side.
  label: string;
  status: SubAgentStatus;
  // lifecycle comes from the live pool snapshot; "" when the sub-agent
  // has no running process (queued, or already finished).
  lifecycle: string;
  depth: number;
  turns_used: number;
  max_turns: number;
  result?: string;
  started_at?: string;
  /** Set once the delegation reaches a terminal status; absent while it
      is queued or running. Which one is present decides whether the row
      reads "running 4m" or "4m ago". */
  ended_at?: string;
  /** 1-based place in this conversation's waiting line; absent or 0 once
      the sub-agent is running. Computed server-side so the panel never
      infers ordering from timestamps it may have received out of order. */
  queue_position?: number;
  /** The sub-agent's answer as typed fields. `structured: false` means it
      never called report_result and this was reconstructed from its
      closing message — the findings were never actually asserted. */
  envelope?: SubAgentEnvelope;
};

/** The compact incident header the rail shows above the agent list.
    Absent for a conversation with no investigation, which is most. */
export type IncidentSummary = {
  status: string;
  iteration: number;
  summary: string;
  stop_reason?: string;
  evidence_count: number;
};

export type SubAgentEvidence = {
  kind: string;
  source: string;
  excerpt: string;
};

export type SubAgentEnvelope = {
  summary: string;
  findings?: string[];
  evidence?: SubAgentEvidence[];
  confidence: string;
  needs_followup?: boolean;
  structured: boolean;
};

export type FileContent = {
  path: string;
  size: number;
  binary: boolean;
  content?: string;
  tooBig?: boolean;
  mtime?: number;
};

export type AgentEvent = {
  session_id?: string;
  agent_name?: string;
  type: string;
  data?: string;
  tool_name?: string;
  tool_input?: string;
  tool_use_id?: string;
  is_error?: boolean;
  pid?: number;
  lifecycle?: string;
  at?: number;
  end_at?: number;
};

export type SSEStatus = "connecting" | "connected" | "error";

export type ThreadBlock =
  | { kind: "thinking"; text: string }
  | { kind: "raw"; text: string }
  | {
      kind: "tool";
      toolUseId: string;
      toolName: string;
      toolInput: string;
      result?: string;
      isError?: boolean;
      startedAt?: number;
      endedAt?: number;
      // Set from a `connector_run` SSE event while the underlying connector run
      // is in flight, so a running wick_execute tool call can show a Cancel
      // button (needs connectorId + runId for the cancel route). Cleared when the
      // run finishes.
      runId?: string;
      connectorId?: string;
    };

export type LiveTurn = { text: string; blocks: ThreadBlock[] };

export type TypingState = { active: boolean; substate?: string; toolName?: string };

export type WsField = {
  key: string;
  label?: string;
  type: "text" | "password" | "dropdown" | string;
  required?: boolean;
  secret?: boolean;
  set?: boolean;
  placeholder?: string;
  options?: string[];
  value?: string;
  help?: string;
};

export type WsInstance = {
  id: string;
  label?: string;
  status: string;
  fields?: WsField[];
};

// WsTombstone is a connector auto-deleted when the session went idle — shown as
// a "deleted, re-create" notice. Carries no config (that's gone with it).
export type WsTombstone = {
  label: string;
  base_key: string;
  deleted_at: string;
  reason?: string;
};

export type WsBase = {
  base_key: string;
  label?: string;
};

export type Schedule = {
  id: string;
  session_id: string;
  created_by: string;
  kind: string; // once | recurring
  run_at: string; // RFC3339 — next fire
  status: string; // pending | active | done | cancelled | failed
  message: string;
  run_count: number;
  paused?: boolean;
  interval_ms?: number;
  cron?: string;
  max_runs?: number;
  ends_at?: string;
  last_run_at?: string;
  last_error?: string;

  /* Scope. "existing" delivers into session_id; "new" / "template" run in
     project_id and resolve the target session at each fire. */
  session_mode: "existing" | "new" | "template";
  project_id?: string;
  project_name?: string;
  session_template?: string;
  last_session_id?: string;
  last_session_label?: string;
  source_session_id?: string;
  /* Fires triggered by Run now — separate from run_count, which is what
     max_runs caps. */
  manual_runs?: number;
  /* Zone a cron expression is matched in (the server's); cron rows only. */
  cron_timezone?: string;
};

export type ProviderModelOption = {
  id: string;
  label: string;
  default: boolean;
  desc?: string;
  /** True for a live model set: an expandable picker row (4th level) resolved
      by `id`; the vendor filter stays server-side. */
  live?: boolean;
  /** Vendor-declared capabilities (raw); present on live-discovery rows. */
  caps?: import("@wick-fe/common-ui").ModelCaps;
};

export type ProviderOption = {
  type: string;
  name: string;
  version: string;
  usesAIRouter?: boolean;
  /** Only populated for wick instances with >1 enabled model — the composer
      picker adds a 3rd "model" level only when there's a real choice. */
  models?: ProviderModelOption[];
  /** Capability-chip prefs (wick-scoped, ride on the wick option row). */
  show_capabilities?: boolean;
  capability_display_mode?: string;
};

export type ProjectOption = {
  id: string;
  name: string;
  path: string;
  managed: boolean;
  pinned: boolean;
  defaultProvider?: string;
  /** Model pinned on defaultProvider, in that instance's own id space (for
      wick, "<entryID>@<vendorModelID>"). Only meaningful together with
      defaultProvider — a model id does not resolve on its own. */
  defaultModel?: string;
  /** Whether this project has a ticket board. Ticket affordances outside
      the board itself (a chat's jump-to-ticket entry) key off this, so a
      project without one shows none of them. */
  ticketEnabled?: boolean;
};

/** One column on a project's board. Statuses are per project: a team names
    its own stages. `key` is what a ticket stores and what MCP accepts;
    `label` is display only. Exactly one status is `terminal` — the stage
    auto-resolve moves finished work to. */
export type TicketStatus = {
  key: string;
  label?: string;
  terminal?: boolean;
};

/** One custom field definition in a project's ticket schema. */
export type TicketField = {
  key: string;
  label: string;
  type: "text" | "select";
  options?: string[];
  required?: boolean;
};

/** One rule deciding when a new session gets a ticket on its own. Rules
    are tried in order and the FIRST match wins, so a disabled narrow rule
    above a broad one carves an exception out of it. */
export type AutoCreateRule = {
  /** "ui" | "slack" | "telegram" | "rest" | "*" */
  origin: string;
  /** "" (any) | "dm" | "channel" | "thread" — channel origins only. */
  channel_kind?: string;
  /** "" | "contains:<text>" | "regex:<expr>", tested against the first message. */
  match?: string;
  /** Title template; "{message}" and "{origin}" are substituted. */
  title?: string;
  enabled: boolean;
};

/** A project's ticket-mode configuration (zero value = feature off). */
export type TicketConfig = {
  enabled?: boolean;
  fields?: TicketField[];
  followup_after_sec?: number;
  followup_prompt?: string;
  auto_resolve_after_sec?: number;
  auto_create?: AutoCreateRule[];
  /** Board columns, in order. Empty means the built-in set. */
  statuses?: TicketStatus[];
};

/** One card on the project ticket board. A card is a TICKET, not a
    session: a ticket holds the work, and several sessions can hang off it. */
export type TicketCard = {
  id: string; // short quotable code, e.g. "T-4F2A"
  title: string;
  status: string;
  assignee?: string;
  fields?: Record<string, string>;
  /** The ticket's sessions, listed on the card so one can be dragged to
      another ticket without opening anything. */
  session_rows?: TicketSessionRow[];
  sessions: number;
  notes: number;
  open_tasks: number;
  updated_at: string;
  created_at: string;
  stale: boolean;
};

/** GET /api/projects/{id}/tickets response. */
export type TicketBoard = {
  config: TicketConfig;
  tickets: TicketCard[];
  /** Chats in this project that belong to no ticket — the board's left
      rail, and the drag source for attaching one. One page at a time, or
      empty when the rail is collapsed: these are payload caps, not display
      filters, so a big project stays cheap to poll. */
  untracked: TicketSessionRow[];
  /** How many untracked chats exist, however few were sent. */
  untracked_total?: number;
  statuses: TicketStatus[];
  users?: Record<string, string>;
  me?: string;
};

/** One session listed inside a ticket's detail view. */
export type TicketSessionRow = {
  id: string;
  label: string;
  status?: string;
  lifecycle?: string;
  last_active?: string;
};

/** The full ticket, as stored. */
export type Ticket = {
  id: string;
  project_id: string;
  title: string;
  status: string;
  assignee?: string;
  fields?: Record<string, string>;
  sessions?: string[];
  created_at: string;
  updated_at: string;
};

/** GET /api/tickets/{ticketID} response. */
export type TicketDetail = {
  ticket: Ticket;
  config: TicketConfig;
  sessions: TicketSessionRow[];
  notes: Note[];
  statuses: TicketStatus[];
  users?: Record<string, string>;
  me?: string;
};

/** One markdown note on a ticket or a session. */
export type Note = {
  id: string;
  body: string;
  /** Who the note was written for — a label, not an access rule. The agent
      reads every audience; `hidden` is what keeps one away from it. */
  audience: "ai" | "human" | "both";
  checkable?: boolean;
  done?: boolean;
  /** Hidden notes never reach the agent. Still listed here, blurred. */
  hidden?: boolean;
  author?: string;
  created_at: string;
  updated_at: string;
};

/** Per-user saved board filter for one project. */
export type TicketFilter = {
  statuses?: string[];
  assignee?: string; // "" = all, "me", or a user id
  view_mode?: string; // "list" | "card"
  /** Adds the untracked chats to the board — and only then does the server
      send that list, which is what keeps a project with hundreds of loose
      chats cheap to poll. Off by default; its count arrives regardless, so
      the switch can name what turning it on would cost. */
  show_untracked?: boolean;
};

/** GET /api/notes response. `ticket` is present when the scope resolved to
    a ticket, so the caller can name it without a second fetch. */
export type NotesResponse = {
  notes: Note[];
  users?: Record<string, string>;
  me?: string;
  ticket?: { id: string; title: string; status: string };
  /** The ticket's project board columns, so the rail's status select offers
      the same choices as the board. */
  statuses?: TicketStatus[];
};

/** One message between two agents inside a delegation tree. */
export type AgentMessageItem = {
  id: string;
  from_handle: string;
  to_handle: string;
  body: string;
  kind: "ask" | "tell" | "reply";
  /** Set when wick promoted a closing turn into an answer nobody wrote. */
  auto_reply?: boolean;
  created_at: string;
};
