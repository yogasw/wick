import { formatCount, relativeTime } from "./format.js";
import type { BackfillReport, BackfillRequestEcho, Handoff, ProjectRow, RecentPage } from "./types.js";

// Presentation logic for the Projects tab. It lives here rather than in the
// component so the rules that decide what a row says — which column is
// genuinely unanswerable, what a backfill preview means, when "0" is a fact
// and when it is an absence — can be tested without mounting anything.

// ── the table ────────────────────────────────────────────────────────

export const projectKey = (r: ProjectRow): string => `${r.workspace}/${r.project}`;

// sortProjects puts the most recently touched project first, then falls back
// to the name. A project the store has never dated sorts last rather than
// first: an unknown date is not "the oldest", it is no date at all.
export function sortProjects(rows: ProjectRow[]): ProjectRow[] {
  return [...rows].sort((a, b) => {
    const ta = Date.parse(a.last_updated ?? "");
    const tb = Date.parse(b.last_updated ?? "");
    const va = Number.isFinite(ta) ? ta : -Infinity;
    const vb = Number.isFinite(tb) ? tb : -Infinity;
    if (va !== vb) return vb - va;
    return projectKey(a).localeCompare(projectKey(b));
  });
}

// ── per-project metrics: one source, swappable ───────────────────────

// MetricCell is one per-project number, or the stated absence of one. An
// unavailable cell carries its reason so the table never shows a bare dash
// that the reader has to guess at (PLAN §13.5 point 4).
export type MetricCell = { available: true; value: string; note?: string } | { available: false; reason: string };

export type ProjectMetrics = {
  pages: MetricCell;
  lastActive: MetricCell;
  sessions: MetricCell;
  observations: MetricCell;
  activity: MetricCell;
};

// BRIEFING_UNAVAILABLE is what a cell says when this project's own numbers
// could not be read.
//
// Sessions, observations and 7d/30d activity have exactly one source: the
// backend's `memory_briefing`, the single MCP call this dashboard makes
// (Yoga, 2026-09-25; PLAN §13.2.1). /api/v1/projects returns a page count and
// a date, and `status` has no --project flag at all, so when that one call
// fails there is nothing to fall back to.
//
// The store-wide total sitting one tab away is NOT a fallback: putting it
// here would break the rule §13.5 opens with, and inventing a number would be
// worse. So the cell states the absence and names the cause.
export const BRIEFING_UNAVAILABLE =
  "This project's own counters come from the backend's briefing call, and it did not answer for this project. " +
  "The Analytics tab's totals are store-wide and are NOT this project's, so nothing is borrowed from them.";

// briefingReason is that sentence plus the server's own message when there is
// one. The message is what distinguishes a daemon that went down mid-list
// from a project that was renamed out from under the listing.
export function briefingReason(row: ProjectRow): string {
  const err = row.briefing_error?.trim();
  return err ? `${BRIEFING_UNAVAILABLE} The backend said: ${err}` : BRIEFING_UNAVAILABLE;
}

// projectMetrics is THE single place a project's own numbers are resolved.
//
// Every component reads the table through this function — the table cells,
// the detail panel and the tests all come through here — which is why the
// briefing arriving was a change to this one function and not to the markup.
// `extra` stays for the same reason it was added: a caller (or a test) can
// override any single cell without re-deriving the rest.
export function projectMetrics(row: ProjectRow, extra?: Partial<ProjectMetrics>, now: number = Date.now()): ProjectMetrics {
  const b = row.briefing;
  const reason = briefingReason(row);
  return {
    pages: { available: true, value: formatCount(row.page_count), note: "latest version of each" },
    lastActive: { available: true, value: relativeTime(row.last_updated, now), note: row.last_updated ?? "never written to" },
    sessions: b
      ? { available: true, value: formatCount(b.counts.sessions), note: "sessions captured in this project" }
      : { available: false, reason },
    observations: b
      ? { available: true, value: formatCount(b.counts.observations), note: "observations stored for this project" }
      : { available: false, reason },
    activity: b
      ? { available: true, value: activityValue(b.activity_7d.sessions, b.activity_30d.sessions), note: activityNote(b) }
      : { available: false, reason },
    ...(extra ?? {}),
  };
}

// activityValue is the "7d / 30d" cell. Both windows are shown because
// either alone is misleading: 0 in 7d reads as a dead project until the 30d
// number says it was busy last month.
export function activityValue(sevenDay: number, thirtyDay: number): string {
  return `${formatCount(sevenDay)} / ${formatCount(thirtyDay)}`;
}

// activityNote spells the column out on hover — which number is which window,
// and in what unit. "6 / 6" in a column headed "Activity 7d/30d" does not say
// whether it counts sessions, pages or observations.
export function activityNote(b: ProjectBriefingLike): string {
  return (
    `sessions in this project: ${formatCount(b.activity_7d.sessions)} in the last 7 days, ` +
    `${formatCount(b.activity_30d.sessions)} in the last 30 — ` +
    `${formatCount(b.activity_7d.observations)} / ${formatCount(b.activity_30d.observations)} observations, ` +
    `${formatCount(b.activity_7d.pages_updated)} / ${formatCount(b.activity_30d.pages_updated)} pages updated`
  );
}

// ProjectBriefingLike is the slice of a briefing activityNote needs, so the
// helper can be tested without building a whole briefing.
type ProjectBriefingLike = {
  activity_7d: { sessions: number; observations: number; pages_updated: number };
  activity_30d: { sessions: number; observations: number; pages_updated: number };
};

// metricText renders a cell for a table. "n/a" rather than "0" or "—": a
// number that has no source has not been measured as zero.
export function metricText(cell: MetricCell): string {
  return cell.available ? cell.value : "n/a";
}

// metricTitle is the cell's hover text — the value's own context when there
// is one, the reason for its absence when there is not.
export function metricTitle(cell: MetricCell): string {
  return cell.available ? (cell.note ?? cell.value) : cell.reason;
}

// ── latest pages ─────────────────────────────────────────────────────

// LatestPages is the project detail's page list, or the stated reason there
// is none. `empty` is deliberately its own state: a project that has been
// briefed and holds no pages is a fact, while a project that could not be
// briefed is an unknown, and one line cannot serve both.
export type LatestPages =
  | { state: "ok"; pages: RecentPage[] }
  | { state: "empty" }
  | { state: "unavailable"; reason: string };

export function latestPages(row: ProjectRow | null): LatestPages {
  if (!row?.briefing) return { state: "unavailable", reason: row ? briefingReason(row) : BRIEFING_UNAVAILABLE };
  const pages = row.briefing.recent_pages ?? [];
  return pages.length ? { state: "ok", pages } : { state: "empty" };
}

// pageLabel is what one row of that list reads as. The path is the fallback,
// not the decoration: a page whose title was never written still has to be
// identifiable, and "sessions/<uuid>.md" identifies it.
export function pageLabel(p: RecentPage): string {
  const title = p.title?.trim();
  return title ? title : p.path;
}

// ── pending handoffs per project ─────────────────────────────────────

// HandoffCell is one row's pending-handoff column. It is loaded per project,
// once, when the tab opens: the count is a scoped server call each time, so it
// is never part of the 5-second poll.
export type HandoffCell =
  | { state: "unknown" }
  | { state: "loading" }
  | { state: "ok"; count: number }
  | { state: "error"; message: string };

// handoffCellText renders that cell. An unread count is "—", never "0": a
// project whose handoffs could not be read has not been shown to have none.
export function handoffCellText(cell: HandoffCell | undefined): string {
  switch (cell?.state) {
    case "ok":
      return String(cell.count);
    case "loading":
      return "…";
    case "error":
      return "?";
    default:
      return "—";
  }
}

// handoffDetail is the same cell spelled out for the project detail panel.
// `error` is separated from `text` so the component colours a failed read
// differently without re-deriving why it failed.
export function handoffDetail(cell: HandoffCell | undefined): { text: string; error: boolean } {
  switch (cell?.state) {
    case "ok":
      return {
        text:
          cell.count === 0
            ? "None open — nothing is waiting to be picked up by another agent."
            : `${cell.count} open baton${cell.count === 1 ? "" : "s"} waiting to be picked up.`,
        error: false,
      };
    case "loading":
      return { text: "Reading…", error: false };
    case "error":
      return { text: `Could not be read: ${cell.message}`, error: true };
    default:
      return { text: "Not read yet.", error: false };
  }
}

// oldestHandoff returns the oldest open baton, which is the one worth acting
// on — a handoff that has been waiting is either forgotten or stuck.
export function oldestHandoff(rows: Handoff[]): Handoff | null {
  const dated = rows.filter((r) => (r.created_at_ms ?? 0) > 0);
  if (!dated.length) return rows[0] ?? null;
  return dated.reduce((a, b) => ((a.created_at_ms ?? 0) <= (b.created_at_ms ?? 0) ? a : b));
}

// ── backfill ─────────────────────────────────────────────────────────

// backfillSummary turns one report into the sentence shown after a run.
//
// The no-op case is the one that needs explaining: selected 0 with
// skipped_non_empty true is not a failure and not an empty project — it means
// the store already holds sessions and an unforced import correctly did
// nothing (PLAN §11.1). Without that sentence the button looks broken.
export function backfillSummary(rep: BackfillReport | undefined): string {
  if (!rep) return "";
  const what = rep.dry_run ? "would import" : "imported";
  if (rep.skipped_non_empty && rep.selected === 0) {
    return (
      "Nothing to do: this project already has captured sessions, so an unforced import skipped every one of them. " +
      "Only a forced import would re-read them — and it would add their observations a second time."
    );
  }
  if (rep.selected === 0) {
    return "No local harness sessions were found for this project, so there is nothing to import.";
  }
  const parts = [
    `${rep.selected} session${rep.selected === 1 ? "" : "s"} selected`,
    `${what} ${rep.imported_sessions} session${rep.imported_sessions === 1 ? "" : "s"}`,
  ];
  if (rep.imported_events > 0) parts.push(`${rep.imported_events} events`);
  if (rep.skipped_for_cap > 0) {
    parts.push(`${rep.skipped_for_cap} left out by the max-sessions cap`);
  }
  if (rep.failed_sessions > 0) parts.push(`${rep.failed_sessions} failed`);
  return `${parts.join(", ")}.`;
}

// backfillCapWarning fires when the cap truncated the history. It matters
// because the backend's own default is 25, low enough to silently leave most
// of a long project behind (PLAN §10.6).
export function backfillCapWarning(
  rep: BackfillReport | undefined,
  req?: BackfillRequestEcho | null,
): string | null {
  if (!rep || rep.skipped_for_cap <= 0) return null;
  // Name the cap when the server said what it resolved to. "Raise it in
  // Settings" is advice you cannot act on without knowing what it is now:
  // the number is not in the report, only in the echoed request.
  const cap = req?.max_sessions && req.max_sessions > 0 ? ` of ${req.max_sessions}` : "";
  return `${rep.skipped_for_cap} session${
    rep.skipped_for_cap === 1 ? " was" : "s were"
  } left out because of the max-sessions cap${cap}. Raise it in Settings and run the import again to take the rest.`;
}

// backfillConfirmBody is the ConfirmDialog text for a REAL import. It states
// the consequence rather than asking "are you sure": observations do not
// dedupe, so a forced run counts the same facts twice and drags recall
// ranking with them (PLAN §11.1).
export function backfillConfirmBody(scope: string, force: boolean): string {
  const head = `Import this project's local harness history into ${scope}.`;
  if (!force) {
    return `${head} Sessions already in the store are skipped, so nothing is duplicated. This reads every local session file and can take a while.`;
  }
  return (
    `${head} FORCED: every selected session is re-imported, including the ones already captured. ` +
    "Sessions and latest pages dedupe, observations do NOT — their facts will be stored a second time and recall ranking will shift with them. " +
    "Importing one missing session at a time is the safe way to fill a gap."
  );
}

// ── the project this panel was opened for (PLAN §22) ─────────────────

// scopeKeyOf is the selection key for a server-resolved project scope, in the
// same "workspace/project" form the table's rows use — so opening the panel
// from a project menu selects the row that is already there.
export const scopeKeyOf = (sc: { workspace: string; project: string }): string => `${sc.workspace}/${sc.project}`;

// scopeCaveat is what has to be said about HOW the bucket was decided, or
// null when there is nothing to add.
//
// Only `basename` earns a caveat, and it earns a real one: that folder is a
// custom path wick deliberately does not mark, so the backend derives the
// project from the folder's NAME — and two projects whose folders share a
// name share one memory (PLAN §14.3). The Health tab is where that is
// actually detected, so the line points at it rather than at a fix that would
// mean wick writing a dotfile into someone's repo.
export function scopeCaveat(source: string): string | null {
  if (source !== "basename") return null;
  return (
    "This project's folder is a custom path, which wick does not mark, so its memory bucket comes from the folder NAME. " +
    "Another project whose folder has the same name would share this memory — the Health tab lists any such collision."
  );
}

// scopeOrigin is the plain-language half of the same answer, for every source.
export function scopeOrigin(source: string): string {
  switch (source) {
    case "marker":
      return "pinned by this folder's .ai-memory.toml — the file agents actually read";
    case "wick":
      return "wick's own mapping for this project; the marker is written the next time a session runs here";
    default:
      return "derived from the folder name, because this folder carries no marker";
  }
}
