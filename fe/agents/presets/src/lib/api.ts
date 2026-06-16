import type { PresetListResponse, PresetDetailResponse } from "./types.js";

class ApiError extends Error {
  constructor(public readonly status: number, message: string) {
    super(message);
  }
}

function getBase(): string {
  return document.getElementById("app")?.dataset.base ?? "";
}

async function get<T>(path: string): Promise<T> {
  const resp = await fetch(path, { credentials: "same-origin", headers: { "Accept": "application/json" } });
  if (!resp.ok) {
    const body = await resp.text().catch(() => "");
    throw new ApiError(resp.status, body || `HTTP ${resp.status}`);
  }
  return resp.json() as Promise<T>;
}

export async function listPresets(): Promise<PresetListResponse> {
  const base = getBase();
  const r = await get<PresetListResponse>(`${base}/api/presets`);
  return { presets: r.presets ?? [] };
}

export async function getPreset(name: string): Promise<PresetDetailResponse> {
  const base = getBase();
  const r = await get<PresetDetailResponse>(`${base}/api/presets/${encodeURIComponent(name)}`);
  return { name: r.name ?? name, body: r.body ?? "" };
}

export async function createPreset(name: string, body: string): Promise<void> {
  const base = getBase();
  const form = new URLSearchParams();
  form.set("name", name);
  form.set("body", body);
  const resp = await fetch(`${base}/presets`, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: form.toString(),
    redirect: "manual",
  });
  if (resp.type === "opaqueredirect" || resp.status === 303 || resp.ok) return;
  throw new ApiError(resp.status, await resp.text().catch(() => ""));
}

export async function updatePreset(name: string, body: string): Promise<void> {
  const base = getBase();
  const form = new URLSearchParams();
  form.set("body", body);
  const resp = await fetch(`${base}/presets/${encodeURIComponent(name)}`, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: form.toString(),
  });
  if (!resp.ok) {
    throw new ApiError(resp.status, await resp.text().catch(() => ""));
  }
}

export async function deletePreset(name: string): Promise<void> {
  const base = getBase();
  const resp = await fetch(`${base}/presets/${encodeURIComponent(name)}`, {
    method: "DELETE",
    credentials: "same-origin",
  });
  if (!resp.ok) {
    throw new ApiError(resp.status, await resp.text().catch(() => ""));
  }
}

export { ApiError };
