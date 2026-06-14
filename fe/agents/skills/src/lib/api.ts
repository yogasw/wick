import type { SkillListResponse, SkillDetailResponse, SkillFileDetailResponse } from "./types.js";

class ApiError extends Error {
  constructor(public readonly status: number, message: string) {
    super(message);
  }
}

function getBase(): string {
  return document.getElementById("app")?.dataset.base ?? "";
}

async function get<T>(path: string): Promise<T> {
  const resp = await fetch(getBase() + path, { credentials: "same-origin", headers: { "Accept": "application/json" } });
  if (!resp.ok) {
    const body = await resp.text().catch(() => "");
    throw new ApiError(resp.status, body || `HTTP ${resp.status}`);
  }
  return resp.json() as Promise<T>;
}

function normalizeList(r: SkillListResponse): SkillListResponse {
  return {
    dirs: r.dirs ?? [],
    skills: (r.skills ?? []).map((s) => ({
      ...s,
      in_dirs: s.in_dirs ?? [],
      missing_dirs: s.missing_dirs ?? [],
    })),
  };
}

export async function listSkills(): Promise<SkillListResponse> {
  const r = await get<SkillListResponse>("/api/skills");
  return normalizeList(r);
}

export async function getSkill(name: string): Promise<SkillDetailResponse> {
  const r = await get<SkillDetailResponse>(`/api/skills/${encodeURIComponent(name)}`);
  return {
    ...r,
    in_dirs: r.in_dirs ?? [],
    missing_dirs: r.missing_dirs ?? [],
    entries: (r.entries ?? []).map((e) => ({
      ...e,
      in_dirs: e.in_dirs ?? [],
      missing_dirs: e.missing_dirs ?? [],
    })),
  };
}

export async function getSkillFile(folder: string, file: string): Promise<SkillFileDetailResponse> {
  const r = await get<SkillFileDetailResponse>(
    `/api/skills/${encodeURIComponent(folder)}/files/${encodeURIComponent(file)}`
  );
  return { ...r, in_dirs: r.in_dirs ?? [] };
}

export async function postMutation(url: string): Promise<void> {
  const resp = await fetch(getBase() + url, { method: "POST", redirect: "manual" });
  if (resp.type === "opaqueredirect" || resp.status === 303 || resp.ok) return;
  throw new ApiError(resp.status, await resp.text().catch(() => ""));
}

export { ApiError };
