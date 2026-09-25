import type { Checkpoint, ProjectBriefing, ProjectScope, RecentPage, SearchHit } from "./types.js";
import { relativeTime } from "./format.js";
import { BRIEFING_UNAVAILABLE } from "./projects.js";

// Presentation logic for the project-scoped memory view (PLAN §22).
//
// It lives here rather than in the component for the same reason the other
// tabs' logic does: the rules that decide what a card says — which page is
// which, what a snippet is, whether a path is writable, whether an edit has
// unsaved work in it — are the part worth testing, and mounting a component
// to check them would be testing Svelte instead.

// PageCard is one page as the project view shows it.
//
// `snippet` is only ever present when something actually read the page: a
// search hit carries one, and an opened page has a body to derive one from.
// The listing itself has no snippet, and inventing one from the title would
// be filling a card with a restatement of its own heading.
export type PageCard = {
  path: string;
  title: string;
  kind?: string;
  updated_at?: string;
  snippet?: string;
};

// cardsFrom builds the card list: the project's recent pages, with any search
// hits' snippets folded in by path so a search decorates the list rather than
// replacing it.
export function cardsFrom(pages: RecentPage[] | null | undefined, hits?: SearchHit[] | null): PageCard[] {
  const snippets = new Map<string, string>();
  for (const h of hits ?? []) {
    if (h.snippet) snippets.set(h.path, h.snippet);
  }
  return (pages ?? []).map((p) => ({
    path: p.path,
    title: p.title?.trim() || p.path,
    kind: p.kind,
    updated_at: p.updated_at,
    snippet: snippets.get(p.path),
  }));
}

// cardsFromHits is the search result rendered as cards, for the hits that are
// NOT in the recent list — a project's older pages are reachable no other way.
export function cardsFromHits(hits: SearchHit[] | null | undefined, known: PageCard[]): PageCard[] {
  const have = new Set(known.map((c) => c.path));
  return (hits ?? [])
    .filter((h) => !have.has(h.path))
    .map((h) => ({
      path: h.path,
      title: h.title?.trim() || h.path,
      kind: h.kind,
      snippet: h.snippet,
    }));
}

// snippetOf is the first meaningful line of a body, for a page that has been
// opened. Headings are skipped: "# kasir_prod_db" under a card already titled
// "kasir_prod_db" tells the reader nothing.
export function snippetOf(body: string | undefined, max = 160): string {
  if (!body) return "";
  const lines = body.split("\n");
  let i = 0;
  // A page read through the API carries its frontmatter in its own field,
  // but a body pasted into the editor can still open with a fence — and its
  // keys are metadata, not the sentence the page is about.
  if (lines[0]?.trim() === "---") {
    i = 1;
    while (i < lines.length && lines[i].trim() !== "---") i++;
    i++;
  }
  for (; i < lines.length; i++) {
    const line = lines[i].trim();
    if (!line || line.startsWith("#")) continue;
    return line.length > max ? line.slice(0, max - 1).trimEnd() + "…" : line;
  }
  return "";
}

// cardAge is the "last written" line. An undated page says so rather than
// rendering an empty space that reads as "never".
export function cardAge(c: PageCard, now: number = Date.now()): string {
  if (!c.updated_at) return "date unknown";
  return relativeTime(c.updated_at, now);
}

// ── writing ──────────────────────────────────────────────────────────

// pathError validates a new page's path the way the store does, and says what
// is wrong rather than letting the backend answer about a path it normalised
// into something else. Null = usable.
export function pathError(path: string): string | null {
  const p = path.trim();
  if (!p) return "A path is required — for example notes/deploy.md";
  if (p.startsWith("/")) return "Use a path relative to the wiki, without a leading slash.";
  if (p.includes("..")) return "A path cannot climb out of the wiki with “..”.";
  if (/\s/.test(p)) return "A path cannot contain spaces.";
  if (!p.toLowerCase().endsWith(".md")) return "Pages are markdown — the path has to end in .md";
  return null;
}

// PAGE_KINDS are the semantic kinds the backend stores in frontmatter. The
// empty option is not "none": it leaves whatever the page already carries,
// which is what an edit to a page someone else wrote should default to.
export const PAGE_KINDS = [
  { label: "Leave unchanged", value: "" },
  { label: "fact", value: "fact" },
  { label: "rule", value: "rule" },
  { label: "decision", value: "decision" },
  { label: "gotcha", value: "gotcha" },
];

// hasUnsavedWork reports whether the editor holds something the store does
// not. It is what makes "Discard" mean something and what a close-confirm
// hangs off; comparing trimmed text keeps a trailing newline from reading as
// an edit.
export function hasUnsavedWork(original: string, draft: string): boolean {
  return original.trim() !== draft.trim();
}

// ── checkpoints ──────────────────────────────────────────────────────

// checkpointLabel is one restore option: when it was made, and what made it.
// The summary is the part that makes a list of hashes choosable.
export function checkpointLabel(c: Checkpoint, now: number = Date.now()): string {
  const when = c.time ? relativeTime(new Date(c.time * 1000).toISOString(), now) : "unknown time";
  return `${c.short_oid || c.oid.slice(0, 12)} · ${when} · ${c.summary || "no summary"}`;
}

// checkpointsForPath narrows the store-wide git history to the commits that
// name this page.
//
// The list is store-wide — one git tree holds every project — so showing it
// unfiltered next to one page would offer to "restore" this page from a
// commit that never touched it. The unfiltered list stays available because a
// page's own commits are not always named after it (a consolidation commit
// can carry several), which is why this returns both halves rather than
// hiding one.
export function checkpointsForPath(all: Checkpoint[] | null | undefined, path: string): {
  matching: Checkpoint[];
  others: Checkpoint[];
} {
  const rows = all ?? [];
  const p = path.trim();
  if (!p) return { matching: [], others: rows };
  const matching = rows.filter((c) => (c.summary ?? "").includes(p));
  return { matching, others: rows.filter((c) => !matching.includes(c)) };
}

// ── the header ───────────────────────────────────────────────────────

// scopeHeading is what the view calls itself: the wick project's name, with
// the memory bucket underneath. Both, always — the project name is what the
// person clicked, and the bucket is what the agents actually write to.
export function scopeHeading(scope: ProjectScope | null): { title: string; bucket: string } {
  if (!scope) return { title: "This project's memory", bucket: "" };
  return { title: scope.name || scope.project, bucket: `${scope.workspace}/${scope.project}` };
}

// counterRow is the project's own numbers, in display order. It reads from
// the briefing and nowhere else: the store-wide totals with the same names
// live on the Analytics tab and must never be mixed in here (PLAN §13.5).
export function counterRow(b: ProjectBriefing | undefined | null): { label: string; value: string }[] {
  if (!b) return [];
  const c = b.counts;
  return [
    { label: "Pages", value: String(c.pages_latest) },
    { label: "Sessions", value: String(c.sessions) },
    { label: "Observations", value: String(c.observations) },
    { label: "Last 7 days", value: `${b.activity_7d.sessions} sessions · ${b.activity_7d.observations} obs` },
    { label: "Last 30 days", value: `${b.activity_30d.sessions} sessions · ${b.activity_30d.observations} obs` },
  ];
}

// ── this project's analytics (Yoga, 2026-09-25) ──────────────────────
//
// Every figure below comes from a PER-PROJECT source — the backend's
// briefing, or the project's own pages. Nothing store-wide is borrowed and
// relabelled: the store-wide totals with the same names live on the global
// panel's Analytics tab, and mixing the two scopes is the confusion PLAN
// §13.5 opens with. Where a per-project figure genuinely has no source, the
// cell says so instead of showing a zero.

// WriteDay is one day of the project's write history.
export type WriteDay = { date: string; count: number };

// writeTimeline counts the project's page writes per day over the last
// `days` days, from the pages' own updated_at.
//
// It is built from the RECENT-PAGES listing, which is capped — so the oldest
// days in a long window can undercount, and the caller says so rather than
// drawing a decline that is really the edge of the list (see timelineCaveat).
export function writeTimeline(
  pages: RecentPage[] | null | undefined,
  days = 30,
  now: number = Date.now(),
): WriteDay[] {
  const out: WriteDay[] = [];
  const byDay = new Map<string, number>();
  for (const p of pages ?? []) {
    const t = Date.parse(p.updated_at ?? "");
    if (!Number.isFinite(t)) continue;
    const key = new Date(t).toISOString().slice(0, 10);
    byDay.set(key, (byDay.get(key) ?? 0) + 1);
  }
  for (let i = days - 1; i >= 0; i--) {
    const key = new Date(now - i * 86_400_000).toISOString().slice(0, 10);
    out.push({ date: key, count: byDay.get(key) ?? 0 });
  }
  return out;
}

// timelineCaveat warns when the listing itself is the limit — the only
// honest reading of a chart whose input is capped.
export function timelineCaveat(pages: RecentPage[] | null | undefined, limit = 50): string | null {
  const n = (pages ?? []).length;
  if (n < limit) return null;
  return `This chart counts the ${n} most recent pages, which is the listing's limit — days before the oldest of them are undercounted, not quiet.`;
}

// PaceReading compares the last 7 days against the last 30. It answers the
// question four static numbers cannot: is this project's memory still being
// written to, or did it go quiet?
export type PaceReading = {
  available: boolean;
  label: string;
  detail: string;
  // ratio > 1 = the recent week is busier than the month's average week.
  ratio: number;
};

export function paceOf(b: ProjectBriefing | null | undefined): PaceReading {
  if (!b) {
    return { available: false, label: "n/a", detail: BRIEFING_UNAVAILABLE, ratio: 0 };
  }
  const week = b.activity_7d.observations;
  const month = b.activity_30d.observations;
  if (month === 0) {
    return {
      available: true,
      label: "No activity in 30 days",
      detail: "Nothing has been captured in this project for a month. If agents have been working here, the hook is the first thing to check.",
      ratio: 0,
    };
  }
  // The month's average week, compared with the week just gone.
  const avgWeek = (month * 7) / 30;
  const ratio = avgWeek === 0 ? 0 : week / avgWeek;
  if (week === 0) {
    return {
      available: true,
      label: "Quiet this week",
      detail: `${month} observations in the last 30 days, none in the last 7.`,
      ratio: 0,
    };
  }
  if (ratio >= 1.25) {
    return {
      available: true,
      label: "Busier than usual",
      detail: `${week} observations this week against an average week of ${avgWeek.toFixed(1)}.`,
      ratio,
    };
  }
  if (ratio <= 0.75) {
    return {
      available: true,
      label: "Slowing down",
      detail: `${week} observations this week against an average week of ${avgWeek.toFixed(1)}.`,
      ratio,
    };
  }
  return {
    available: true,
    label: "Steady",
    detail: `${week} observations this week, in line with the 30-day average of ${avgWeek.toFixed(1)} a week.`,
    ratio,
  };
}

// KindSlice is one semantic kind's share of this project's pages.
export type KindSlice = { kind: string; count: number; share: number };

// kindMix is what this project's memory is MADE of — facts, rules,
// decisions, gotchas, session notes. Two projects with the same page count
// can be completely different stores, and this is the only view that says so.
export function kindMix(pages: RecentPage[] | null | undefined): KindSlice[] {
  const rows = pages ?? [];
  if (rows.length === 0) return [];
  const by = new Map<string, number>();
  for (const p of rows) {
    const k = (p.kind ?? "").trim() || "unlabelled";
    by.set(k, (by.get(k) ?? 0) + 1);
  }
  return [...by.entries()]
    .map(([kind, count]) => ({ kind, count, share: count / rows.length }))
    .sort((a, b) => b.count - a.count || a.kind.localeCompare(b.kind));
}

// freshness is how long it has been since anything was written here, and
// whether that is worth noticing. A project whose agents are working while
// its memory stands still is the silent failure this whole feature exists
// for — so the reading is stated, not left to be inferred from a date.
export type Freshness = { available: boolean; label: string; detail: string; stale: boolean };

// STALE_DAYS is when "quiet" becomes "worth checking". A fortnight is long
// enough to cover a holiday and short enough that a broken hook is caught in
// the same month it broke.
export const STALE_DAYS = 14;

export function freshness(b: ProjectBriefing | null | undefined, now: number = Date.now()): Freshness {
  const last = b?.last_observation_at;
  const t = Date.parse(last ?? "");
  if (!b || !Number.isFinite(t)) {
    return {
      available: false,
      label: "n/a",
      detail: b
        ? "This project has no captured observation to date from — nothing has been recorded in it yet."
        : BRIEFING_UNAVAILABLE,
      stale: false,
    };
  }
  const days = Math.floor((now - t) / 86_400_000);
  const stale = days >= STALE_DAYS;
  return {
    available: true,
    label: days <= 0 ? "Today" : days === 1 ? "Yesterday" : `${days} days ago`,
    detail: stale
      ? `Nothing has been captured here for ${days} days. If agents have been working in this project since, the capture hook is the first thing to check.`
      : "The most recent thing an agent recorded in this project.",
    stale,
  };
}

// linkRow is the project's cross-project position: what it leans on, what
// leans on it, and what is waiting in its mailbox. All four are the
// briefing's own per-project counters.
export function linkRow(b: ProjectBriefing | null | undefined): { label: string; value: string; note?: string }[] {
  if (!b) return [];
  return [
    { label: "Depends on", value: String(b.cross_project_dependencies), note: "other projects this one's memory refers to" },
    { label: "Depended on by", value: String(b.cross_project_dependents), note: "projects whose memory refers to this one" },
    { label: "Handoffs pending", value: String(b.pending_handoff_count), note: "batons another agent has not picked up" },
    { label: "Messages pending", value: String(b.pending_message_count), note: "mail addressed to this project" },
  ];
}
