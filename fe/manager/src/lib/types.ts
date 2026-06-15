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

export interface McpHeaderRow {
  key: string;
  value: string;
  secret?: boolean;
}

export interface McpSSOExtra {
  audience?: string;
  ttl_seconds?: number;
}

export interface McpOAuthExtra {
  client_id?: string;
  client_secret?: string;
  scopes?: string;
}

export interface McpServerForm {
  label: string;
  icon: string;
  description: string;
  url: string;
  auth_scheme: string;
  auth_secret: string;
  auth_headers: McpHeaderRow[];
  headers: McpHeaderRow[];
  sso: McpSSOExtra;
  oauth: McpOAuthExtra;
  excluded: string[];
  oauth_login_id: string;
}

export interface McpTool {
  name: string;
  description: string;
}

export interface McpTestResult {
  ok: boolean;
  tools?: McpTool[];
  latency_ms?: number;
  error?: string;
  needs_login?: boolean;
  server_name?: string;
}

export interface McpServerInfo {
  def_id: string;
  disabled: boolean;
}

export interface McpServerFormResult {
  id: string;
  form: McpServerForm;
  tools: McpTool[];
  info: McpServerInfo | null;
}

export interface McpOAuthStartResult {
  auth_url: string;
  login_id: string;
}

export interface McpOAuthStatusResult {
  status: string;
}

export interface JobDetail {
  key: string;
  name: string;
  description: string;
  icon: string;
  schedule: string;
  enabled: boolean;
  max_runs: number;
  max_timeout_min: number;
  total_runs: number;
  last_status: string;
  can_configure: boolean;
  fields: ConfigField[] | null;
}

export interface JobSettings {
  schedule: string;
  enabled: boolean;
  max_runs: number;
  max_timeout_min: number;
}

export interface JobRunResult {
  id: string;
  job_id: string;
  status: string;
  result: string;
  triggered_by: string;
  started_at: string;
  ended_at: string | null;
}

export interface ToolDetail {
  key: string;
  name: string;
  description: string;
  icon: string;
  can_configure: boolean;
  fields: ConfigField[] | null;
}

export interface AuditRun {
  id: string;
  connector_id: string;
  connector_key: string;
  connector_name: string;
  operation_key: string;
  source: string;
  status: string;
  user_id: string;
  user_name: string;
  latency_ms: number;
  started_at: string;
}

export interface AuditSummary {
  total: number;
  succeeded: number;
  errored: number;
  avg_latency_ms: number;
}

export interface AuditResult {
  runs: AuditRun[] | null;
  source: string;
  status: string;
  from: string;
  to: string;
  page: number;
  total_pages: number;
  total: number;
  page_size: number;
  summary: AuditSummary;
}

export interface AuditFilter {
  source: string;
  status: string;
  from: string;
  to: string;
  page: number;
}
