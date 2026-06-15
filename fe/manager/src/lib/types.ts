export interface ConnectorDef {
  key: string;
  name: string;
  category: string;
  icon: string;
  custom?: boolean;
  disabled?: boolean;
}

export interface ConnectorRow {
  id: string;
  label: string;
  disabled: boolean;
  status: string;
  rate_limit_rpm: number;
  tags: string[] | null;
}

export interface ConnectorList {
  key: string;
  name: string;
  description: string;
  icon: string;
  fixed: boolean;
  op_count: number;
  custom: boolean;
  rows: ConnectorRow[] | null;
}

export interface ConfigField {
  key: string;
  type: string;
  value: string;
  options: string;
  required: boolean;
  is_secret: boolean;
  has_value: boolean;
  description: string;
  visible_when: string;
  col_options?: Record<string, string>;
  env_override: string;
}

export interface ConnectorOp {
  key: string;
  name: string;
  description: string;
  destructive: boolean;
  enabled: boolean;
  system_disabled: boolean;
  system_disabled_reason: string;
}

export interface ConnectorDetail {
  key: string;
  name: string;
  icon: string;
  id: string;
  label: string;
  disabled: boolean;
  rate_limit_rpm: number;
  has_health_check: boolean;
  can_configure: boolean;
  fields: ConfigField[] | null;
  operations: ConnectorOp[] | null;
}

export interface HealthCheckResult {
  ok: boolean;
  error?: string;
  newly_locked?: string[] | null;
  newly_cleared?: string[] | null;
  ops?: Record<string, { enabled: boolean; system_disabled: boolean; reason: string }>;
}

export interface TestInputField {
  key: string;
  type: string;
  required: boolean;
  description: string;
}

export interface TestOp {
  key: string;
  name: string;
  description: string;
  destructive: boolean;
  input: TestInputField[] | null;
}

export interface TestAccount {
  id: string;
  display_name: string;
}

export interface TestMeta {
  key: string;
  name: string;
  icon: string;
  id: string;
  label: string;
  ops: TestOp[] | null;
  accounts: TestAccount[] | null;
}

export interface TestRunResult {
  operation: string;
  run_id?: string;
  status?: string;
  latency_ms?: number;
  response?: unknown;
  error?: string;
}

export interface HistoryRun {
  id: string;
  operation_key: string;
  source: string;
  status: string;
  user_id: string;
  user_name: string;
  error_msg: string;
  latency_ms: number;
  http_status: number;
  ip_address: string;
  user_agent: string;
  request_json: string;
  response_json: string;
  started_at: string;
}

export interface HistoryOpOption {
  key: string;
  name: string;
}

export interface HistoryUserOption {
  id: string;
  name: string;
}

export interface HistoryResult {
  key: string;
  name: string;
  id: string;
  label: string;
  runs: HistoryRun[] | null;
  ops: HistoryOpOption[] | null;
  users: HistoryUserOption[] | null;
  page: number;
  total_pages: number;
  total: number;
  page_size: number;
}

export interface HistoryFilter {
  op: string;
  source: string;
  status: string;
  user: string;
  page: number;
}

export interface DraftField {
  key: string;
  widget: string;
  options: string;
  secret: boolean;
  required: boolean;
  default: string;
  desc: string;
}

export interface DraftOpRequest {
  method: string;
  url_template: string;
  headers: Record<string, string>;
  body_template: string;
  content_type: string;
}

export interface DraftMCPSource {
  server_id: string;
  tool_name: string;
}

export interface DraftOp {
  key: string;
  name: string;
  description: string;
  destructive: boolean;
  inputs: DraftField[];
  request?: DraftOpRequest;
  mcp_source?: DraftMCPSource;
}

export interface Draft {
  key: string;
  name: string;
  description: string;
  icon: string;
  source: string;
  category: string;
  single: boolean;
  allow_session_config: boolean;
  health_op: string;
  health_expect: string;
  configs: DraftField[];
  ops: DraftOp[];
}

export interface CustomMeta {
  ai_providers: string[];
  categories: string[];
}

export interface CustomDraftResult {
  def_id: string;
  disabled: boolean;
  mcp: boolean;
  draft: Draft | null;
}

export interface SaveDraftResult {
  redirect?: string;
  ok?: boolean;
  reload_error?: string;
}
