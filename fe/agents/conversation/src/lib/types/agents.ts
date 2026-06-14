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
