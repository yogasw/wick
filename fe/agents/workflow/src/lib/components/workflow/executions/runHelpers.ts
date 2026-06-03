import type { RunSummary } from "$lib/api/workflow";

// fmtTimestamp parses an RFC3339 timestamp and returns "YYYY-MM-DD HH:mm:ss"
// without trailing milliseconds / timezone. Falls back to the raw string
// when parsing fails so the user sees something useful.
export function fmtTimestamp(ts?: string): string {
  if (!ts) return "—";
  return ts.replace("T", " ").slice(0, 19);
}

// fmtTimeOnly returns just the HH:mm:ss portion — used in the events
// timeline where the date column would be redundant noise.
export function fmtTimeOnly(ts?: string): string {
  if (!ts) return "—";
  const s = ts.replace("T", " ");
  const m = s.match(/\d{2}:\d{2}:\d{2}/);
  return m ? m[0] : s.slice(0, 8);
}

// fmtDuration computes a human-readable wall time from a RunSummary's
// started_at vs ended/finished_at fields. Returns "running" when no
// end timestamp landed yet.
export function fmtDuration(r: { started_at?: string; ended_at?: string; finished_at?: string }): string {
  if (!r.started_at) return "—";
  const start = new Date(r.started_at).getTime();
  const endIso = r.ended_at ?? r.finished_at;
  if (!endIso) return "running";
  const end = new Date(endIso).getTime();
  const ms = Math.max(0, end - start);
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${(ms / 60_000).toFixed(1)} m`;
}

// statusKind maps the raw status string to a coarse bucket the UI uses
// to pick colour swatches. Keeps the colour mapping in one spot.
export type StatusKind = "success" | "failed" | "running" | "other";
export function statusKind(s: string | undefined): StatusKind {
  switch ((s ?? "").toLowerCase()) {
    case "success":
    case "succeeded":
    case "ok":
      return "success";
    case "failed":
    case "error":
      return "failed";
    case "running":
    case "queued":
      return "running";
    default:
      return "other";
  }
}

// statusBadgeClass returns Tailwind classes for the per-row status
// pill. Keeps colours consistent between RunListItem and RunDetail.
export function statusBadgeClass(s: string | undefined): string {
  switch (statusKind(s)) {
    case "success":
      return "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300";
    case "failed":
      return "bg-rose-500/15 text-rose-700 dark:text-rose-300";
    case "running":
      return "bg-amber-500/15 text-amber-700 dark:text-amber-300";
    default:
      return "bg-slate-500/15 text-slate-700 dark:text-slate-300";
  }
}

// statusLabel hands back the short label rendered inside the pill —
// "✓ success" / "✗ failed" / etc. Falls back to the raw status when
// nothing matches the known kinds.
export function statusLabel(s: string | undefined): string {
  switch (statusKind(s)) {
    case "success": return "✓ success";
    case "failed": return "✗ failed";
    case "running": return "● running";
    default: return s ?? "—";
  }
}

// shortID truncates a UUID-shaped id for compact rendering in lists.
// Keeps the first 8 chars which is enough to disambiguate at glance.
export function shortID(id: string): string {
  return id.length > 8 ? id.slice(0, 8) : id;
}

// runKind buckets a row into manual / automation / test using the
// same rules as the backend (see runKind in spa_workflows.go). Kept
// in sync so the FE pill matches the filter the API expects.
export type RunKind = "manual" | "automation" | "test";
export function runKind(r: { source?: string; trigger_type?: string }): RunKind {
  switch (r.source) {
    case "spa": return "manual";
    case "test":
    case "wftest":
      return "test";
  }
  if (r.trigger_type === "manual") return "manual";
  return "automation";
}

export function kindBadgeClass(kind: RunKind): string {
  switch (kind) {
    case "manual": return "bg-sky-500/15 text-sky-700 dark:text-sky-300";
    case "automation": return "bg-violet-500/15 text-violet-700 dark:text-violet-300";
    case "test": return "bg-amber-500/15 text-amber-700 dark:text-amber-300";
  }
}

export function kindLabel(kind: RunKind): string {
  switch (kind) {
    case "manual": return "manual";
    case "automation": return "auto";
    case "test": return "test";
  }
}

// triggerIDOf extracts the trigger id that fired a run, looking in
// the spots the backend writes it. Used by the replay action so the
// editor can re-pin that trigger on switch.
export function triggerIDOf(runDetail: any | null | undefined): string | null {
  if (!runDetail) return null;
  if (typeof runDetail.event?.payload?.trigger_id === "string") {
    return runDetail.event.payload.trigger_id;
  }
  if (typeof runDetail.trigger_id === "string") {
    return runDetail.trigger_id;
  }
  return null;
}

// downloadJSON writes the given JSON payload as a file named
// `run-<id>.json`. Used by the export action.
export function downloadJSON(filename: string, payload: unknown) {
  const blob = new Blob([JSON.stringify(payload, null, 2)], {
    type: "application/json",
  });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}

// runKey returns whichever of id / run_id is populated on a row. The
// backend normaliser writes both, but defensive readers shouldn't
// assume either was set.
export function runKey(r: RunSummary): string {
  return r.id || r.run_id || "";
}
