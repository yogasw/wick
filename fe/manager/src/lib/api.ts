import { apiGet, apiPost } from "@wick-fe/common-api";
import type {
  ConnectorDef,
  ConnectorList,
  ConnectorDetail,
  HealthCheckResult,
  TestMeta,
  TestRunResult,
  HistoryResult,
  HistoryFilter,
} from "./types.js";

/* Connector definitions live at the server-absolute /manager surface,
   distinct from the SPA mount base (/modules/manager/app). */
export async function listConnectors(): Promise<ConnectorDef[]> {
  const r = await apiGet<ConnectorDef[] | null>("/manager/api/connectors");
  return (r ?? []).map((c) => ({
    key: c.key,
    name: c.name,
    category: c.category ?? "",
    icon: c.icon ?? "",
    custom: c.custom ?? false,
    disabled: c.disabled ?? false,
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

export async function getConnectorRow(key: string, id: string): Promise<ConnectorDetail> {
  const r = await apiGet<ConnectorDetail>(rowBase(key, id));
  return { ...r, fields: r.fields ?? [], operations: r.operations ?? [] };
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
