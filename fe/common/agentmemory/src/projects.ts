import { formatCount, relativeTime } from "./format.js";
import type { BackfillReport, BackfillRequestEcho, DoctorReport, Handoff, ProjectRow, RecentPage } from "./types.js";

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
// Three outcomes, and they are genuinely different questions. The one that
// used to be wrong is the DRY RUN: `ai-memory backfill --dry-run` reports
// `selected` and leaves `imported_sessions` at 0 every time, whatever the
// state — measured on this host at --max-sessions 1, 2 and 2000, and on a
// scope with skipped_non_empty false, where nothing was blocking an import at
// all. So "would import 0 sessions" was never a result; it was a field the
// preview does not fill. The user read it as the feature being broken
// ("kalau gini berarti bukan import per project dong"), which is a fair
// reading of a sentence that says an import would do nothing. A preview now
// reports what it selected and stops there.
export function backfillSummary(rep: BackfillReport | undefined): string {
  if (!rep) return "";
  if (rep.skipped_non_empty) {
    const found = rep.selected > 0
      ? `${rep.selected} local session${rep.selected === 1 ? "" : "s"} found for this project, none imported`
      : "Nothing imported";
    return (
      `${found}: this project's memory already has captured sessions, so an unforced import skips every one of them. ` +
      "Only a forced import would re-read them — and it would add their observations a second time."
    );
  }
  if (rep.selected === 0) return NO_LOCAL_SESSIONS;
  const n = `${rep.selected} session file${rep.selected === 1 ? "" : "s"}`;
  if (rep.dry_run) {
    // `selected` is capped by max-sessions — at --max-sessions 1 the backend
    // returned exactly 1 — so it is how many an import would READ, not how
    // many exist in the folder.
    return (
      `${n} would be read for this project. A preview stops there: the backend reports what it selected, ` +
      "not what importing them would write, so there is no import count to show yet."
    );
  }
  const parts = [`${n} read`, `imported ${rep.imported_sessions} session${rep.imported_sessions === 1 ? "" : "s"}`];
  if (rep.imported_events > 0) parts.push(`${rep.imported_events} events`);
  if (rep.skipped_for_cap > 0) parts.push(`${rep.skipped_for_cap} left out by the max-sessions cap`);
  if (rep.failed_sessions > 0) parts.push(`${rep.failed_sessions} failed`);
  return `${parts.join(", ")}.`;
}

// NO_LOCAL_SESSIONS says WHERE it looked, because "no sessions found" while
// the panel is showing a list of sessions reads as a bug.
//
// It is not one — they are different things. wick sessions are wick's own;
// these are the transcript files claude and codex leave on this host. A
// project can have plenty of the first and none of the second: verified for
// support-tools/rest-20c9202d on this host, which has wick sessions and no
// harness transcript directory at all.
export const NO_LOCAL_SESSIONS =
  "No harness session files were found in this project's folder. That is not the same as having no sessions: " +
  "this counts the transcript files claude and codex leave on this host for work done in that folder, and a project " +
  "can have wick sessions without any of them.";

// BACKFILL_EXPLAINER is the card's own answer to "ini pas import gimana cara
// kerja nya?" and, more pointedly, to "apakah buka claude codex atau gimana
// sih?" (Yoga, 2026-09-26).
//
// The answer to the second is NO, and it is said outright rather than left to
// be inferred from "reads the local history". claude keeps one .jsonl
// transcript per session under ~/.claude/projects/<folder>/ and codex keeps
// its own under ~/.codex/sessions/; backfill opens those files. Nothing is
// launched, nobody is signed in as, and no model is called — which is the
// difference between a file read and the thing people reasonably fear when a
// button offers to "import your sessions".
export const BACKFILL_EXPLAINER =
  "Claude and codex each leave a transcript file on this host for every session that ran in this project's folder. " +
  "Import reads those files and writes their sessions into this project's memory, so switching capture on part-way " +
  "through a project does not leave it amnesiac about everything before. It does not start claude or codex, does not " +
  "sign in as anyone, and calls no model and no network service — it is a read of files already on this disk. " +
  "It is a one-time bootstrap rather than a sync: once this project's memory holds sessions, a further import finds " +
  "the same files and imports none of them.";

// BACKFILL_SELECTED_NOTE explains the word the report uses. It counts files
// the backend would READ, capped by max-sessions — not everything in the
// folder, and not what would be written.
export const BACKFILL_SELECTED_NOTE =
  "“Found” counts the harness transcript files in this project's folder that an import would read, up to the max-sessions cap. What actually gets written is a separate number, and only a real import can report it: sessions already in this project's memory are skipped.";

// ── what is behind the number (Yoga, 2026-09-26) ─────────────────────
//
// "itu katanya found tapi aku kok ngak yakin itu ada beneran dan mana yg mau
// di import" — a bare count is not checkable, and he is right not to trust
// one. `ai-memory doctor --json`, run in a project's folder, decomposes it
// from the SAME discovery the backfill uses: on this host that folder's
// doctor reported claude-code 3 + codex 8, and its dry run selected 11.
//
// What doctor does NOT give is a list. There is no enumeration in the CLI —
// `backfill --session <id>` imports one by id, but nothing lists the ids — so
// the page states that limit instead of wick going to the harness stores
// itself. A second implementation of "which sessions belong here" would drift
// from ai-memory's, which is the class of bug this whole morning was.

// HarnessRow is one harness's coverage of this project's folder, as the
// import page shows it.
export type HarnessRow = {
  agent: string;
  // local is what this harness left in the folder; recent is the subset
  // doctor counts as recent. captured is what the store already holds.
  local: number;
  recent: number;
  captured: number;
  // uncaptured marks the half-wired state: sessions ran here and the hook
  // never reported them.
  uncaptured: boolean;
};

export function harnessRows(d: DoctorReport | null | undefined): HarnessRow[] {
  return (d?.rows ?? []).map((r) => ({
    agent: r.agent,
    local: r.local_total,
    recent: r.local_recent,
    captured: r.captured,
    uncaptured: r.uncaptured,
  }));
}

// localTotal sums what the harnesses left in the folder, or null when doctor
// could not answer. Null is not zero: a failed check has not shown there is
// nothing there.
export function localTotal(d: DoctorReport | null | undefined): number | null {
  if (!d || d.error || !d.rows) return null;
  return d.rows.reduce((n, r) => n + r.local_total, 0);
}

// countsNote reconciles the two numbers on the page — doctor's per-harness
// totals and the dry run's `selected` — or says plainly that they differ.
//
// They usually match (3 + 8 = 11 on this host). When they do not, the cap or
// doctor's recency window is the likely reason, and BOTH numbers are shown
// with the disagreement stated. Silently preferring one would hide exactly
// the kind of mismatch that makes a count untrustworthy.
export function countsNote(selected: number | null, local: number | null): string | null {
  if (selected === null || local === null) return null;
  if (selected === local) {
    return `The two agree: ${local} session file${local === 1 ? "" : "s"} in the folder, ${selected} that an import would read.`;
  }
  return (
    `These two do not match: the harness check counts ${local} session file${local === 1 ? "" : "s"} in the folder, ` +
    `while the preview would read ${selected}. The max-sessions cap and the check's own recency window are the usual ` +
    "reasons. Both numbers are the backend's own — neither is adjusted to agree with the other."
  );
}

// IMPORT_NO_LIST is the limit, stated rather than worked around.
export const IMPORT_NO_LIST =
  "ai-memory reports these as counts, not as a list of sessions: its backfill has no way to enumerate what it found, only to import one by id once you already know the id. wick does not go looking through the harness folders itself to produce a list — a second opinion about which sessions belong to a project would drift from the one that actually does the importing.";

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
