import type { OverviewResponse, TeamResponse } from "./types.js";

class ApiError extends Error {
  constructor(public readonly status: number, message: string) {
    super(message);
  }
}

async function get<T>(path: string): Promise<T> {
  const resp = await fetch(path, { credentials: "same-origin", headers: { "Accept": "application/json" } });
  if (!resp.ok) {
    const body = await resp.text().catch(() => "");
    throw new ApiError(resp.status, body || `HTTP ${resp.status}`);
  }
  return resp.json() as Promise<T>;
}

async function post(path: string): Promise<void> {
  const resp = await fetch(path, { method: "POST", credentials: "same-origin" });
  if (resp.type === "opaqueredirect" || resp.status === 303 || resp.ok) return;
  throw new ApiError(resp.status, await resp.text().catch(() => ""));
}

export async function fetchOverview(base: string): Promise<OverviewResponse> {
  const r = await get<OverviewResponse>(`${base}/api/overview`);
  return {
    queued: r.queued ?? [],
    active: r.active ?? [],
    stats: r.stats ?? { active: 0, pool_max: 0, queue_len: 0 },
  };
}

/** fetchTeam reads the caller's Team roster for the Overview card. Captain
    first, then oldest first — the server's order is kept. ensure=0 keeps
    it read-only: opening Overview must not create anyone's Captain. */
export async function fetchTeam(base: string): Promise<TeamResponse> {
  const r = await get<{ agents: TeamResponse["agents"] | null; captain_id?: string }>(`${base}/api/team/agents?ensure=0`);
  return { agents: r.agents ?? [], captain_id: r.captain_id ?? "" };
}

export async function killSession(base: string, id: string): Promise<void> {
  await post(`${base}/sessions/${id}/kill`);
}

export async function dequeueSession(base: string, id: string): Promise<void> {
  await post(`${base}/sessions/${id}/dequeue`);
}

export { ApiError };
