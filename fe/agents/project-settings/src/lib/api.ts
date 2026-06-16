import type { ProjectSettingsData, UpdateProjectRequest } from "./types.js";

class ApiError extends Error {
  constructor(public readonly status: number, message: string) {
    super(message);
  }
}

function getBase(): string {
  return document.getElementById("app")?.dataset.base ?? "";
}

async function get<T>(path: string): Promise<T> {
  const resp = await fetch(path, {
    credentials: "same-origin",
    headers: { "Accept": "application/json" },
  });
  if (!resp.ok) {
    const body = await resp.text().catch(() => "");
    throw new ApiError(resp.status, body || `HTTP ${resp.status}`);
  }
  return resp.json() as Promise<T>;
}

export async function getProjectSettings(id: string): Promise<ProjectSettingsData> {
  const base = getBase();
  return get<ProjectSettingsData>(`${base}/api/projects/${encodeURIComponent(id)}`);
}

export async function updateProject(id: string, req: UpdateProjectRequest): Promise<void> {
  const base = getBase();
  const resp = await fetch(`${base}/api/projects/${encodeURIComponent(id)}`, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", "Accept": "application/json" },
    body: JSON.stringify(req),
  });
  if (!resp.ok) {
    const body = await resp.text().catch(() => "");
    throw new ApiError(resp.status, body || `HTTP ${resp.status}`);
  }
}

export async function createProject(req: UpdateProjectRequest): Promise<string> {
  const base = getBase();
  const form = new URLSearchParams();
  form.set("name", req.name);
  form.set("icon", req.icon);
  form.set("description", req.description);
  form.set("folder_mode", req.folder_mode);
  form.set("custom_path", req.custom_path);
  form.set("preset", req.preset);
  form.set("provider", req.provider);
  form.set("system_addon", req.system_addon);
  const resp = await fetch(`${base}/projects`, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: form.toString(),
    redirect: "manual",
  });
  if (resp.type === "opaqueredirect" || resp.status === 303) {
    const location = resp.headers.get("location") ?? `${base}/sessions`;
    return location;
  }
  if (!resp.ok) {
    const body = await resp.text().catch(() => "");
    throw new ApiError(resp.status, body || `HTTP ${resp.status}`);
  }
  return `${base}/sessions`;
}

export async function deleteProject(id: string): Promise<void> {
  const base = getBase();
  const resp = await fetch(`${base}/projects/${encodeURIComponent(id)}`, {
    method: "DELETE",
    credentials: "same-origin",
  });
  if (!resp.ok) {
    const body = await resp.text().catch(() => "");
    throw new ApiError(resp.status, body || `HTTP ${resp.status}`);
  }
}

export async function unpinSession(projectID: string, sessionID: string): Promise<void> {
  const base = getBase();
  const form = new URLSearchParams();
  form.set("unpin", sessionID);
  const resp = await fetch(`${base}/projects/${encodeURIComponent(projectID)}`, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: form.toString(),
  });
  if (!resp.ok) {
    const body = await resp.text().catch(() => "");
    throw new ApiError(resp.status, body || `HTTP ${resp.status}`);
  }
}

export { ApiError };
