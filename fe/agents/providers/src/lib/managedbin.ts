/* managedbin.ts — client for wick-managed provider binaries
   (/api/managed-binaries). A download runs as a server job; the UI polls
   the list for progress, so a reload still shows it. Release facts come
   from the server's background cache — reading the list never hits
   GitHub. Every write is admin-only server-side. */

import { ApiError, get, post } from "$lib/api.js";

export type ManagedJob = {
  id: string;
  type: string;
  tag: string;
  version: string;
  /* activate: the job also switches `current` (download-only = false). */
  activate: boolean;
  phase: string; // resolve | download | verify | probe | save | switching | done | error
  done: number;
  total: number;
  bytesPerSec: number;
  message: string;
  error: string;
  startedAt: string; // RFC3339, "" when unknown
  finishedAt: string; // RFC3339, "" while running
};

export type InstalledVersion = {
  version: string;
  tag: string;
  asset: string;
  sha256: string;
  installedAt: string;
  versionOutput: string;
  current: boolean;
  inUse: number;
  removable: boolean;
};

export type ManagedBinary = {
  type: string;
  binary: string;
  repo: string;
  hostLabel: string;
  enabled: boolean;
  current: string;
  currentPath: string;
  installed: InstalledVersion[];
  latest: string; // tag, "" when unknown
  latestVersion: string;
  latestCheckedAt: string;
  latestErr: string;
  releases: ManagedRelease[];
  updateAvailable: boolean;
  job: ManagedJob | null;
  lastJob: ManagedJob | null;
  sessionsOnOld: Record<string, number>;
};

export type ManagedRelease = { tag: string; version: string; prerelease: boolean; published: string };

type WireJob = Partial<{
  id: string; type: string; tag: string; version: string; activate: boolean; phase: string;
  done: number; total: number; bytes_per_sec: number; message: string; error: string;
  started_at: string; finished_at: string;
}>;

type WireRelease = Partial<{ tag: string; version: string; prerelease: boolean; published_at: string }>;

type WireInstalled = Partial<{
  version: string; tag: string; asset: string; sha256: string; installed_at: string;
  version_output: string; current: boolean; in_use: number; removable: boolean;
}>;

type WireManaged = Partial<{
  type: string; binary: string; repo: string; host_label: string; enabled: boolean;
  current: string; current_path: string; installed: WireInstalled[] | null;
  latest: WireRelease | null; latest_checked_at: string; latest_err: string; update_available: boolean;
  releases: WireRelease[] | null;
  job: WireJob | null; last_job: WireJob | null; sessions_on_old: Record<string, number> | null;
}>;

function mapJob(w: WireJob | null | undefined): ManagedJob | null {
  if (!w || !w.phase) return null;
  return {
    id: w.id ?? "", type: w.type ?? "", tag: w.tag ?? "", version: w.version ?? "",
    activate: w.activate ?? false, phase: w.phase ?? "", done: w.done ?? 0, total: w.total ?? 0,
    bytesPerSec: w.bytes_per_sec ?? 0, message: w.message ?? "", error: w.error ?? "",
    startedAt: w.started_at ?? "", finishedAt: w.finished_at ?? "",
  };
}

/* jobSummary is the line left on screen after a job ends successfully:
   "Installed v1.18.33 · 58 MB in 3.4s". */
export function jobSummary(j: ManagedJob): string {
  const v = j.version ? `v${j.version}` : j.tag || "latest";
  const verb = j.message ? j.message : j.activate ? `Installed ${v}` : `Downloaded ${v}`;
  const parts = [verb];
  if (j.total > 0 && !j.message) parts.push(mb(j.total));
  const t0 = Date.parse(j.startedAt), t1 = Date.parse(j.finishedAt);
  let out = parts.join(" · ");
  if (!Number.isNaN(t0) && !Number.isNaN(t1) && t1 >= t0) out += ` in ${((t1 - t0) / 1000).toFixed(1)}s`;
  return out;
}

export function normalizeManaged(w: WireManaged): ManagedBinary {
  return {
    type: w.type ?? "",
    binary: w.binary ?? "",
    repo: w.repo ?? "",
    hostLabel: w.host_label ?? "",
    enabled: w.enabled ?? false,
    current: w.current ?? "",
    currentPath: w.current_path ?? "",
    installed: (w.installed ?? []).map((i) => ({
      version: i.version ?? "", tag: i.tag ?? "", asset: i.asset ?? "", sha256: i.sha256 ?? "",
      installedAt: i.installed_at ?? "", versionOutput: i.version_output ?? "",
      current: i.current ?? false, inUse: i.in_use ?? 0, removable: i.removable ?? false,
    })),
    latest: w.latest?.tag ?? "",
    latestVersion: w.latest?.version ?? tagVersion(w.latest?.tag ?? ""),
    latestCheckedAt: w.latest_checked_at ?? "",
    latestErr: w.latest_err ?? "",
    releases: (w.releases ?? [])
      .map((r) => ({ tag: r.tag ?? "", version: r.version ?? tagVersion(r.tag ?? ""), prerelease: r.prerelease ?? false, published: r.published_at ?? "" }))
      .filter((r) => r.tag),
    updateAvailable: w.update_available ?? false,
    job: mapJob(w.job),
    lastJob: mapJob(w.last_job),
    sessionsOnOld: w.sessions_on_old ?? {},
  };
}

export function isRunning(j: ManagedJob | null): boolean {
  return !!j && j.phase !== "done" && j.phase !== "error";
}

/* tagVersion: "v18.4.3" → "18.4.3" (mirrors managedbin.TagVersion). */
export function tagVersion(tag: string): string {
  return tag.replace(/^v(?=\d)/, "");
}

const mb = (n: number) => `${Math.round(n / (1 << 20))} MB`;

/* Overall progress, weighted by phase, so the bar always carries a number:
   the download dominates (it is the only slow step), the checks after it
   are short. A download of unknown size parks at the start of its band. */
const PHASE_STEP: Record<string, number> = { resolve: 1, download: 2, verify: 3, probe: 4, save: 5, switching: 5 };
export const JOB_STEPS = 5;

export function jobPct(j: ManagedJob): number {
  switch (j.phase) {
    case "resolve":
      return 2;
    case "download":
      return j.total > 0 ? 5 + Math.floor(Math.min(1, j.done / j.total) * 80) : 5;
    case "verify":
      return 88;
    case "probe":
      return 93;
    case "save":
      return 97;
    case "switching":
      return 99;
    default:
      return 100;
  }
}

/* downloadPct is the byte-level percent of the download itself (the label
   shows it; the bar shows the overall jobPct). -1 when the size is unknown. */
export function downloadPct(j: ManagedJob): number {
  return j.total > 0 ? Math.min(100, Math.floor((j.done / j.total) * 100)) : -1;
}

function stepPrefix(j: ManagedJob): string {
  const n = PHASE_STEP[j.phase];
  return n ? `Step ${n}/${JOB_STEPS} · ` : "";
}

/* jobLabel is the one-line progress text:
   "Downloading v18.4.4… 42% (120 / 286 MB · 12.5 MB/s)". */
export function jobLabel(j: ManagedJob): string {
  const v = j.version ? `v${j.version}` : j.tag || "latest";
  switch (j.phase) {
    case "resolve":
      return `${stepPrefix(j)}Looking up ${v} on GitHub…`;
    case "download": {
      const speed = j.bytesPerSec > 0 ? ` · ${(j.bytesPerSec / (1 << 20)).toFixed(1)} MB/s` : "";
      if (j.total > 0) return `${stepPrefix(j)}Downloading ${v}… ${downloadPct(j)}% (${mb(j.done)} / ${mb(j.total)}${speed})`;
      return `${stepPrefix(j)}Downloading ${v}… ${mb(j.done)}${speed}`;
    }
    case "verify":
      return `${stepPrefix(j)}Verifying sha256 of ${v}…`;
    case "probe":
      return `${stepPrefix(j)}Checking ${v} --version…`;
    case "save":
      return `${stepPrefix(j)}Saving ${v}…`;
    case "switching":
      return `${stepPrefix(j)}Activating ${v}…`;
    case "done":
      return j.message || "Done";
    case "error":
      return j.error || "Failed";
  }
  return j.phase;
}

/* jobShort is the busy-button text: "Downloading v18.4.4… 45%". */
export function jobShort(j: ManagedJob): string {
  if (j.phase === "download") {
    const v = j.version ? `v${j.version}` : j.tag || "latest";
    const p = downloadPct(j);
    return p >= 0 ? `Downloading ${v}… ${p}%` : `Downloading ${v}…`;
  }
  return `${jobLabel(j).replace(/^Step \d\/\d · /, "")} (${jobPct(j)}%)`;
}

/* jobVersion: the version a job is about, for matching it to a row
   (the tag is all we know before the release resolves). */
export function jobVersion(j: ManagedJob): string {
  return j.version || tagVersion(j.tag);
}

export type VersionRowStatus = "active" | "downloaded" | "not_downloaded";

export type VersionRow = {
  version: string;
  tag: string;
  status: VersionRowStatus;
  prerelease: boolean;
  published: string;
  latest: boolean;
  installed: InstalledVersion | null;
};

function cmpVersion(a: string, b: string): number {
  const pa = a.split(/[.-]/), pb = b.split(/[.-]/);
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const x = pa[i] ?? "", y = pb[i] ?? "";
    const nx = Number(x), ny = Number(y);
    if (x !== "" && y !== "" && !Number.isNaN(nx) && !Number.isNaN(ny)) {
      if (nx !== ny) return nx - ny;
    } else if (x !== y) {
      // "1.2.3" (release) sorts above "1.2.3-rc.1".
      if (x === "") return 1;
      if (y === "") return -1;
      return x < y ? -1 : 1;
    }
  }
  return 0;
}

/* versionRows is the Detail panel's one version list: every cached GitHub
   release plus every downloaded version (a pinned or pruned-from-GitHub
   one included), newest first, each with its status. */
export function versionRows(m: ManagedBinary): VersionRow[] {
  const byVer = new Map<string, VersionRow>();
  for (const r of m.releases) {
    byVer.set(r.version, {
      version: r.version, tag: r.tag, status: "not_downloaded", prerelease: r.prerelease,
      published: r.published, latest: r.tag === m.latest, installed: null,
    });
  }
  for (const i of m.installed) {
    const row = byVer.get(i.version);
    const status: VersionRowStatus = i.current ? "active" : "downloaded";
    if (row) {
      row.status = status;
      row.installed = i;
    } else {
      byVer.set(i.version, {
        version: i.version, tag: i.tag || `v${i.version}`, status, prerelease: false,
        published: "", latest: (i.tag || `v${i.version}`) === m.latest, installed: i,
      });
    }
  }
  return [...byVer.values()].sort((a, b) => cmpVersion(b.version, a.version));
}

/* latestState: what the summary offers for the newest release —
   "download" (not on disk yet), "activate" (downloaded, not active),
   or "" (active already / unknown). */
export function latestState(m: ManagedBinary): "download" | "activate" | "" {
  if (!m.latest || !m.latestVersion) return "";
  const i = m.installed.find((x) => x.version === m.latestVersion);
  if (!i) return "download";
  return i.current ? "" : "activate";
}

/* sessionsNote: "2 sessions still on v18.4.2" for every old version in use. */
export function sessionsNote(m: ManagedBinary): string {
  const parts = Object.entries(m.sessionsOnOld)
    .filter(([, n]) => n > 0)
    .map(([v, n]) => `${n} ${n === 1 ? "session" : "sessions"} still on v${v}`);
  return parts.join(" · ");
}

function base(b: string): string {
  return `${b}/api/managed-binaries`;
}

export async function apiManagedList(b: string): Promise<{ types: ManagedBinary[]; isAdmin: boolean }> {
  const r = await get<{ types?: WireManaged[]; is_admin?: boolean }>(base(b));
  return { types: (r.types ?? []).map(normalizeManaged), isAdmin: r.is_admin ?? false };
}

/* StartResult: "started" — a new job; "following" — one was already
   running for the type (HTTP 409 already_running), so the UI shows that
   job's progress instead of an error. */
export type StartResult = { state: "started" | "following"; job: ManagedJob | null };

async function startJob(b: string, type: string, action: "download" | "install", tag: string): Promise<StartResult> {
  const q = tag ? `?tag=${encodeURIComponent(tag)}` : "";
  try {
    const r = await post<{ job?: WireJob }>(`${base(b)}/${encodeURIComponent(type)}/${action}${q}`);
    return { state: "started", job: mapJob(r?.job) };
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) {
      try {
        const body = JSON.parse(e.message) as { code?: string; job?: WireJob };
        if (body.code === "already_running") return { state: "following", job: mapJob(body.job) };
      } catch {
        /* not JSON — a real conflict */
      }
    }
    throw e;
  }
}

/* apiManagedDownload: download + verify + store only; `current` moves
   only on a first install (nothing to switch away from). */
export function apiManagedDownload(b: string, type: string, tag = ""): Promise<StartResult> {
  return startJob(b, type, "download", tag);
}

/* apiManagedInstall: download AND activate (kept for callers that want
   the one-step path). */
export function apiManagedInstall(b: string, type: string, tag = ""): Promise<StartResult> {
  return startJob(b, type, "install", tag);
}

export class CheckTooSoonError extends Error {
  constructor(public readonly retryAfterS: number) {
    super(`Checked less than a minute ago — try again in ${retryAfterS}s`);
  }
}

/* apiManagedCheck forces a release-cache refresh (server-limited to
   once a minute; inside the gap it throws CheckTooSoonError). */
export async function apiManagedCheck(b: string, type: string): Promise<ManagedBinary> {
  try {
    return normalizeManaged(await post<WireManaged>(`${base(b)}/${encodeURIComponent(type)}/check`));
  } catch (e) {
    if (e instanceof ApiError && e.status === 429) {
      let wait = 60;
      try {
        wait = (JSON.parse(e.message) as { retry_after_s?: number }).retry_after_s ?? 60;
      } catch {
        /* keep 60 */
      }
      throw new CheckTooSoonError(wait);
    }
    throw e;
  }
}

export async function apiManagedActivate(b: string, type: string, version: string): Promise<ManagedBinary> {
  return normalizeManaged(await post<WireManaged>(`${base(b)}/${encodeURIComponent(type)}/activate?version=${encodeURIComponent(version)}`));
}

export async function apiManagedRemove(b: string, type: string, version: string): Promise<ManagedBinary> {
  return normalizeManaged(await post<WireManaged>(`${base(b)}/${encodeURIComponent(type)}/remove?version=${encodeURIComponent(version)}`));
}

export async function apiManagedVerify(b: string, type: string): Promise<{ output: string; error: string }> {
  const r = await post<{ output?: string; error?: string }>(`${base(b)}/${encodeURIComponent(type)}/verify`);
  return { output: r?.output ?? "", error: r?.error ?? "" };
}
