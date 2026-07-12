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
};

export type TurnEvent = {
  type: string;
  tool_name?: string;
  tool_input?: string;
  tool_use_id?: string;
  is_error?: boolean;
  text?: string;
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
  | { kind: "tool"; toolUseId: string; toolName: string; toolInput: string; result?: string; isError?: boolean; startedAt?: number; endedAt?: number };

export type LiveTurn = { text: string; blocks: ThreadBlock[] };

export type TypingState = { active: boolean; substate?: string };

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

export type ProviderOption = {
  type: string;
  name: string;
  version: string;
  usesAIRouter?: boolean;
};

export type ProjectOption = {
  id: string;
  name: string;
  path: string;
  managed: boolean;
  pinned: boolean;
  defaultProvider?: string;
};
