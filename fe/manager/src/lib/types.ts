export interface ConnectorDef {
  key: string;
  name: string;
  description: string;
  category: string;
  category_desc: string;
  icon: string;
  op_count: number;
  active_count: number;
  needs_setup_count: number;
  disabled_count: number;
  system: boolean;
  custom: boolean;
  custom_source: string;
  needs_reload: boolean;
  disabled: boolean;
  /* Connector-TYPE off-switch (header kebab) — hidden from the LLM, still
     listed here with a Disabled badge. Distinct from `disabled` (every
     instance row is off). */
  disabled_type?: boolean;
}

export interface ConnectorRow {
  id: string;
  label: string;
  disabled: boolean;
  status: string;
  rate_limit_rpm: number;
  tags: string[] | null;
  /* Owner-only: row visible to its owner + admins until an admin adds a
     sharing tag. Drives the 🔒 Private chip vs the Everyone fallback. */
  private?: boolean;
  /* OAuth/SSO surface — present only for OAuth connector types. oauth.start_url
     is non-empty only when the caller may connect (SSO on + policy + client_id
     set), which drives whether the per-row Connect button renders. */
  oauth?: ConnectorOAuthMeta | null;
  enable_sso?: boolean;
  multi_account?: boolean;
  accounts?: ConnectorAccount[] | null;
}

export interface ConnectorList {
  key: string;
  name: string;
  description: string;
  icon: string;
  fixed: boolean;
  op_count: number;
  custom: boolean;
  custom_source?: string;
  def_id?: string;
  mcp?: boolean;
  mcp_status?: string;
  needs_reload?: boolean;
  /* Connector-TYPE off-switch (header kebab). True = hidden from the LLM. */
  disabled_type?: boolean;
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
  /** Optional "Title" or "Title|Description" — groups simple fields into a
      titled card so shared context lives once on the group, not per field. */
  group?: string;
}

export interface ConnectorOp {
  key: string;
  name: string;
  description: string;
  destructive: boolean;
  config_only?: boolean;
  enabled: boolean;
  system_disabled: boolean;
  system_disabled_reason: string;
  admin_only: boolean;
  category: string;
}

export interface ConnectorCategory {
  key: string;
  title: string;
  description: string;
}

export interface ConnectorAccount {
  id: string;
  display_name: string;
  wick_user_id: string;
  disabled_ops: string[] | null;
  can_manage: boolean;
}

export interface ConnectorOAuthMeta {
  display_name: string;
  start_url: string;
}

export interface ConnectorDetail {
  key: string;
  name: string;
  icon: string;
  id: string;
  label: string;
  description?: string;
  disabled: boolean;
  rate_limit_rpm: number;
  has_health_check: boolean;
  can_configure: boolean;
  is_admin: boolean;
  /* When true the per-instance AI description is mandatory: the section is
     forced on and marked required, and a blank one keeps the instance
     needs_setup (mirrors Meta.RequireAIDescription). */
  require_ai_description?: boolean;
  /* True for an admin OR the instance owner — gates the Access policy +
     per-session config sections. Broader than is_admin (owner included),
     narrower than can_configure (excludes AllowOthersConfigure users). */
  can_manage_policy: boolean;
  fields: ConfigField[] | null;
  operations: ConnectorOp[] | null;
  categories: ConnectorCategory[] | null;
  accounts: ConnectorAccount[] | null;
  oauth: ConnectorOAuthMeta | null;
  enable_sso: boolean;
  multi_account: boolean;
  allow_others_connect_sso: boolean;
  allow_others_configure: boolean;
  session_config_capable: boolean;
  session_config_allowed: boolean;
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

/* A titled section grouping a custom connector's operations. Mirrors the
   Go custom.DefCategory; the connector detail page renders one card per
   section. An empty title is the default/ungrouped section. */
export interface DraftCategory {
  title: string;
  description: string;
  ops: DraftOp[];
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
  ops: DraftCategory[];
}

export interface CustomMeta {
  ai_providers: string[];
  categories: string[];
}

export interface CustomDraftResult {
  def_id: string;
  disabled: boolean;
  mcp: boolean;
  server_id?: string;
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

/* Plugin marketplace — one installable/installed connector plugin. */
export interface PluginEntry {
  key: string;
  name: string;
  description: string;
  version: string;
  installed: boolean;
  enabled: boolean;
  arch_ok: boolean;
  host?: string; // this server's os/arch, for the "no build for X" notice
  os_arch?: string[]; // os/arch the plugin ships a build for
  category?: string; // derived from the plugin's DefaultTags, like built-in connectors
  signed: string;
  update_available?: boolean; // catalog carries a newer version than the one on disk
  latest_version?: string; // that newer catalog version
}

export interface PluginsList {
  installed: PluginEntry[];
  available: PluginEntry[];
  registry_error?: string;
  /* Whether the viewer may install / update / enable / disable / remove.
     Non-admins still receive the full list; the action buttons render
     disabled with a "requires admin" hint. */
  is_admin: boolean;
}
