import { apiGet, apiPost, apiPostSSE } from "@wick-fe/common-api";
import type {
  ConnectorDef,
  ConnectorList,
  ConnectorDetail,
  HealthCheckResult,
  TestMeta,
  TestRunResult,
  HistoryResult,
  HistoryFilter,
  CustomMeta,
  CustomDraftResult,
  Draft,
  SaveDraftResult,
  McpServerForm,
  McpServerFormResult,
  McpTestResult,
  McpOAuthStartResult,
  McpOAuthStatusResult,
  JobDetail,
  JobSettings,
  JobRunResult,
  ToolDetail,
  AuditResult,
  AuditFilter,
} from "./types.js";

/* Connector definitions live at the server-absolute /manager/api surface
   (the SPA itself is mounted at /manager). */
export async function listConnectors(): Promise<ConnectorDef[]> {
  const r = await apiGet<Partial<ConnectorDef>[] | null>("/manager/api/connectors");
  return (r ?? []).map((c) => ({
    key: c.key ?? "",
    name: c.name ?? "",
    description: c.description ?? "",
    category: c.category ?? "",
    category_desc: c.category_desc ?? "",
    icon: c.icon ?? "",
    op_count: c.op_count ?? 0,
    active_count: c.active_count ?? 0,
    needs_setup_count: c.needs_setup_count ?? 0,
    disabled_count: c.disabled_count ?? 0,
    system: c.system ?? false,
    custom: c.custom ?? false,
    custom_source: c.custom_source ?? "",
    needs_reload: c.needs_reload ?? false,
    disabled: c.disabled ?? false,
    disabled_type: c.disabled_type ?? false,
  }));
}

function connBase(key: string): string {
  return `/manager/api/connectors/${encodeURIComponent(key)}`;
}

function rowBase(key: string, id: string): string {
  return `${connBase(key)}/${encodeURIComponent(id)}`;
}

export async function getConnector(key: string): Promise<ConnectorList> {
  const r = await apiGet<ConnectorList>(connBase(key));
  return { ...r, rows: r.rows ?? [] };
}

export async function reloadConnector(key: string): Promise<{ ok: boolean }> {
  return apiPost<{ ok: boolean }>(`${connBase(key)}/reload`);
}

/* Connector-TYPE off-switch (admin-only): hide/show the whole connector type
   from the LLM. Distinct from the per-row toggleConnectorDisabled. */
export async function setConnectorTypeDisabled(
  key: string,
  disabled: boolean,
): Promise<{ disabled_type: boolean }> {
  const verb = disabled ? "type-disable" : "type-enable";
  return apiPost<{ disabled_type: boolean }>(`${connBase(key)}/${verb}`);
}

export async function getConnectorRow(key: string, id: string): Promise<ConnectorDetail> {
  const r = await apiGet<ConnectorDetail>(rowBase(key, id));
  return {
    ...r,
    fields: r.fields ?? [],
    operations: r.operations ?? [],
    accounts: r.accounts ?? [],
  };
}

export async function createConnectorRow(key: string): Promise<string> {
  const r = await apiPost<{ id: string }>(`${connBase(key)}/new`);
  return r.id;
}

export async function setConnectorConfig(
  key: string,
  id: string,
  configKey: string,
  value: string,
): Promise<void> {
  await apiPost(`${rowBase(key, id)}/configs/${encodeURIComponent(configKey)}`, { value });
}

export async function setConnectorLabel(key: string, id: string, label: string): Promise<void> {
  await apiPost(`${rowBase(key, id)}/label`, { label });
}

/* Per-instance AI-facing description (appended to the module description in
   wick_list/wick_get). Empty string clears it. */
export async function setConnectorDescription(key: string, id: string, description: string): Promise<void> {
  await apiPost(`${rowBase(key, id)}/description`, { description });
}

export async function toggleConnectorDisabled(key: string, id: string): Promise<boolean> {
  const r = await apiPost<{ disabled: boolean }>(`${rowBase(key, id)}/disable`);
  return r.disabled;
}

export async function deleteConnectorRow(key: string, id: string): Promise<void> {
  await apiPost(`${rowBase(key, id)}/delete`);
}

export async function runHealthCheck(key: string, id: string): Promise<HealthCheckResult> {
  return apiPost<HealthCheckResult>(`${rowBase(key, id)}/health-check`);
}

export async function resyncMcpTools(key: string): Promise<{ ok: boolean; operations: number }> {
  return apiPost<{ ok: boolean; operations: number }>(`${connBase(key)}/resync-tools`);
}

/* Per-row admin controls (Phase 7a). Each POSTs JSON to a /manager/api
   twin of the legacy templ form-post route, reusing the same services +
   permission gates server-side. */

export async function setConnectorRateLimit(key: string, id: string, rpm: number): Promise<number> {
  const r = await apiPost<{ rate_limit_rpm: number }>(`${rowBase(key, id)}/rate-limit`, { rpm });
  return r.rate_limit_rpm;
}

export async function duplicateConnectorRow(key: string, id: string): Promise<string> {
  const r = await apiPost<{ id: string }>(`${rowBase(key, id)}/duplicate`);
  return r.id;
}

export interface AccessPolicy {
  allow_others_configure: boolean;
  allow_others_connect_sso: boolean;
  enable_sso: boolean;
  multi_account: boolean;
}

export async function setConnectorAccessPolicy(
  key: string,
  id: string,
  policy: AccessPolicy,
): Promise<void> {
  await apiPost(`${rowBase(key, id)}/access-policy`, policy);
}

export async function setConnectorSessionConfig(
  key: string,
  id: string,
  allow: boolean,
): Promise<boolean> {
  const r = await apiPost<{ allow_session_config: boolean }>(`${rowBase(key, id)}/session-config`, {
    allow_session_config: allow,
  });
  return r.allow_session_config;
}

export async function toggleConnectorOperation(
  key: string,
  id: string,
  opKey: string,
  enabled: boolean,
): Promise<boolean> {
  const r = await apiPost<{ enabled: boolean }>(
    `${rowBase(key, id)}/operations/${encodeURIComponent(opKey)}`,
    { enabled },
  );
  return r.enabled;
}

export async function bulkToggleOperations(
  key: string,
  id: string,
  enabled: boolean,
  ops: string[] = [],
): Promise<void> {
  await apiPost(`${rowBase(key, id)}/operations/bulk`, { enabled, ops });
}

export async function disconnectConnectorAccount(
  key: string,
  id: string,
  accountId: string,
): Promise<void> {
  await apiPost(`${rowBase(key, id)}/accounts/${encodeURIComponent(accountId)}/disconnect`);
}

export async function setAccountDisabledOps(
  key: string,
  id: string,
  accountId: string,
  disabledOps: string[],
): Promise<void> {
  await apiPost(`${rowBase(key, id)}/accounts/${encodeURIComponent(accountId)}/ops`, {
    disabled_ops: disabledOps,
  });
}

export async function getTestMeta(key: string, id: string): Promise<TestMeta> {
  const r = await apiGet<TestMeta>(`${rowBase(key, id)}/test-meta`);
  return { ...r, ops: r.ops ?? [], accounts: r.accounts ?? [] };
}

export async function runConnectorTest(
  key: string,
  id: string,
  operation: string,
  input: Record<string, string>,
  accountId: string,
): Promise<TestRunResult> {
  return apiPost<TestRunResult>(`${rowBase(key, id)}/test`, {
    operation,
    input,
    account_id: accountId,
  });
}

const customBase = "/manager/api/connectors/custom";

export async function getCustomMeta(): Promise<CustomMeta> {
  const r = await apiGet<CustomMeta>(`${customBase}/meta`);
  return { ai_providers: r.ai_providers ?? [], categories: r.categories ?? [] };
}

export async function getCustomDraft(defID: string): Promise<CustomDraftResult> {
  return apiGet<CustomDraftResult>(`${customBase}/${encodeURIComponent(defID)}/draft`);
}

export async function parseCustomPaste(
  parser: string,
  provider: string,
  paste: string,
): Promise<Draft> {
  return apiPost<Draft>(`${customBase}/parse`, { parser, provider, paste });
}

export async function saveCustomDraft(draft: Draft): Promise<SaveDraftResult> {
  return apiPost<SaveDraftResult>(`${customBase}/save`, draft);
}

export async function updateCustomDraft(defID: string, draft: Draft): Promise<SaveDraftResult> {
  return apiPost<SaveDraftResult>(`${customBase}/${encodeURIComponent(defID)}/save`, draft);
}

export async function deleteCustomDef(defID: string): Promise<void> {
  await apiPost(`${customBase}/${encodeURIComponent(defID)}/delete`);
}

export async function setCustomDefDisabled(defID: string, disabled: boolean): Promise<boolean> {
  const verb = disabled ? "disable" : "enable";
  const r = await apiPost<{ disabled: boolean }>(`${customBase}/${encodeURIComponent(defID)}/${verb}`);
  return r.disabled;
}

/* MCP server form. The test/save/oauth endpoints live on the legacy
   /manager/connectors/custom/mcp-servers/* surface (already JSON, reused
   verbatim); only the edit-mode prefill has a /manager/api/ twin. */
const mcpBase = "/manager/connectors/custom/mcp-servers";

export async function getMcpServerForm(id: string): Promise<McpServerFormResult> {
  const r = await apiGet<McpServerFormResult>(
    `/manager/api/connectors/custom/mcp-servers/edit?id=${encodeURIComponent(id)}`,
  );
  return { ...r, tools: r.tools ?? [] };
}

export async function testMcpServer(form: McpServerForm): Promise<McpTestResult> {
  return apiPost<McpTestResult>(`${mcpBase}/test`, form);
}

export async function saveMcpServer(
  form: McpServerForm,
  testedOk: boolean,
  id: string,
): Promise<{ redirect?: string }> {
  return apiPost<{ redirect?: string }>(`${mcpBase}/save`, { form, tested_ok: testedOk, id });
}

export async function startMcpOAuth(form: McpServerForm): Promise<McpOAuthStartResult> {
  return apiPost<McpOAuthStartResult>(`${mcpBase}/oauth/start`, { form });
}

export async function getMcpOAuthStatus(loginId: string): Promise<McpOAuthStatusResult> {
  return apiGet<McpOAuthStatusResult>(
    `${mcpBase}/oauth/status?login_id=${encodeURIComponent(loginId)}`,
  );
}

/* Jobs + tools live at the server-absolute /manager/api surface, like the
   connector endpoints — the SPA mount base is ignored by the api client. */
function jobBase(key: string): string {
  return `/manager/api/jobs/${encodeURIComponent(key)}`;
}

export async function getJob(key: string): Promise<JobDetail> {
  const r = await apiGet<JobDetail>(jobBase(key));
  return { ...r, fields: r.fields ?? [] };
}

export async function updateJobSettings(key: string, settings: JobSettings): Promise<void> {
  await apiPost(`${jobBase(key)}/settings`, settings);
}

export async function setJobConfig(key: string, configKey: string, value: string): Promise<void> {
  await apiPost(`${jobBase(key)}/configs/${encodeURIComponent(configKey)}`, { value });
}

export async function runJob(key: string): Promise<string> {
  const r = await apiPost<{ run_id: string }>(`${jobBase(key)}/run`);
  return r.run_id;
}

export async function getJobRun(key: string, runID: string): Promise<JobRunResult> {
  return apiGet<JobRunResult>(`${jobBase(key)}/runs/${encodeURIComponent(runID)}`);
}

export async function getTool(key: string): Promise<ToolDetail> {
  const r = await apiGet<ToolDetail>(`/manager/api/tools/${encodeURIComponent(key)}`);
  return { ...r, fields: r.fields ?? [] };
}

export async function setToolConfig(key: string, configKey: string, value: string): Promise<void> {
  await apiPost(`/manager/api/tools/${encodeURIComponent(key)}/configs/${encodeURIComponent(configKey)}`, { value });
}

export async function getAuditRuns(filter: AuditFilter): Promise<AuditResult> {
  const params = new URLSearchParams();
  if (filter.source) params.set("source", filter.source);
  if (filter.status) params.set("status", filter.status);
  if (filter.from) params.set("from", filter.from);
  if (filter.to) params.set("to", filter.to);
  if (filter.page > 1) params.set("page", String(filter.page));
  const qs = params.toString();
  const path = qs ? `/manager/api/runs?${qs}` : "/manager/api/runs";
  const r = await apiGet<AuditResult>(path);
  return { ...r, runs: r.runs ?? [] };
}

export async function getConnectorHistory(
  key: string,
  id: string,
  filter: HistoryFilter,
): Promise<HistoryResult> {
  const params = new URLSearchParams();
  if (filter.op) params.set("op", filter.op);
  if (filter.source) params.set("source", filter.source);
  if (filter.status) params.set("status", filter.status);
  if (filter.user) params.set("user", filter.user);
  if (filter.page > 1) params.set("page", String(filter.page));
  const qs = params.toString();
  const path = qs ? `${rowBase(key, id)}/history?${qs}` : `${rowBase(key, id)}/history`;
  const r = await apiGet<HistoryResult>(path);
  return { ...r, runs: r.runs ?? [], ops: r.ops ?? [], users: r.users ?? [] };
}

/* Plugin marketplace. Listing is readable by any logged-in user; the
   actions below are admin-only server-side. Backed by internal/manager/plugins_api.go. */
export async function listPlugins(): Promise<import("./types.js").PluginsList> {
  return apiGet<import("./types.js").PluginsList>("/manager/api/plugins");
}
/* Install/update progress phases streamed over SSE by plugins_api.go.
   pct is the download percentage while phase==="downloading" (-1 = unknown
   size). "error" carries a message; "done" is the terminal success frame. */
export type PluginProgress =
  | { phase: "downloading" | "verifying" | "replacing" | "done"; pct: number }
  | { phase: "error"; error: string };

export async function installPlugin(name: string): Promise<{ ok: boolean }> {
  return apiPost<{ ok: boolean }>("/manager/api/plugins/install", { name });
}
export async function updatePlugin(key: string): Promise<{ ok: boolean; version?: string }> {
  return apiPost<{ ok: boolean; version?: string }>(
    `/manager/api/plugins/${encodeURIComponent(key)}/update`,
  );
}

/* Streaming variants: same endpoints, but with Accept: text/event-stream so the
   server streams staged progress. onProgress fires per phase / percent tick.
   Resolves when the stream ends; rejects (via APIError) on an "error" frame. */
export async function updatePluginStream(
  key: string,
  onProgress: (p: PluginProgress) => void,
  signal?: AbortSignal,
): Promise<void> {
  return streamInstall(`/manager/api/plugins/${encodeURIComponent(key)}/update`, onProgress, signal);
}

async function streamInstall(
  path: string,
  onProgress: (p: PluginProgress) => void,
  signal?: AbortSignal,
): Promise<void> {
  let failed: string | null = null;
  await apiPostSSE<PluginProgress & { error?: string }>(
    path,
    (p) => {
      onProgress(p);
      if (p.phase === "error") failed = p.error ?? "install failed";
    },
    signal,
  );
  if (failed) throw new Error(failed);
}
export async function setPluginEnabled(key: string, enabled: boolean): Promise<{ ok: boolean }> {
  const verb = enabled ? "enable" : "disable";
  return apiPost<{ ok: boolean }>(`/manager/api/plugins/${encodeURIComponent(key)}/${verb}`);
}
export async function removePlugin(key: string): Promise<{ ok: boolean }> {
  return apiPost<{ ok: boolean }>(`/manager/api/plugins/${encodeURIComponent(key)}/remove`);
}
