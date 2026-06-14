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

export type ConversationTurn = {
  turn_id: string;
  role: string;
  agent: string;
  provider: string;
  text: string;
  timestamp: number;
  truncated: boolean;
  interrupted: boolean;
  has_trace: boolean;
  events: TurnEvent[];
  attachments: Attachment[];
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

export type ProcessInfo = {
  session_id: string;
  agent_name: string;
  provider: string;
  pid: number;
  queued: number;
  lifecycle: string;
  substate?: string;
  alive: boolean;
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
  | { kind: "tool"; toolUseId: string; toolName: string; toolInput: string; result?: string; isError?: boolean; startedAt?: number; endedAt?: number };

export type LiveTurn = { text: string; blocks: ThreadBlock[] };

export type TypingState = { active: boolean; substate?: string };
