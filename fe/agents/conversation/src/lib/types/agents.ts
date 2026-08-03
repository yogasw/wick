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
};

export type ApprovedItem = {
  match_key: string;
  scope: "session" | "always";
};

export type ApprovalDecision =
  | "approve_once"
  | "approve_session"
  | "approve_always"
  | "block";

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
