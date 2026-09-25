// Wiki tab logic. Kept out of the component so the rules that decide what the
// reader is told — which page they are looking at, and whether a session's raw
// events exist at all — are testable without rendering.

import type { Page, SearchHit } from "./types.js";

// hitKey identifies one search hit. A path is only unique WITHIN a project, so
// the scope has to be part of the key: two projects can each hold
// "sessions/<id>.md", and keying on the path alone would make selecting one
// highlight the other.
export const hitKey = (h: SearchHit): string => `${h.workspace ?? ""}/${h.project ?? ""}/${h.path}`;

// hitTitle is the label for a hit. Falls back to the path rather than to
// "Untitled": the path is the one thing every page has, and it is also what
// the reader needs to recognise which page this is.
export function hitTitle(h: SearchHit): string {
  const t = (h.title ?? "").trim();
  return t !== "" ? t : h.path;
}

// hitScope is the "which store slot is this in" label. Shown on every hit
// because a store-wide search returns rows from several projects at once, and
// an unlabelled list of session titles from four projects is unreadable.
export function hitScope(h: SearchHit): string {
  const ws = (h.workspace ?? "").trim();
  const proj = (h.project ?? "").trim();
  if (ws && proj) return `${ws}/${proj}`;
  return proj || ws || "scope unknown";
}

// snippetHTML is the hit's excerpt. The backend wraps matched terms in <mark>,
// so it is rendered as markup — and therefore everything that is NOT that tag
// is escaped here first. A page body is agent-written text that can contain
// anything, so it cannot be trusted into innerHTML unfiltered.
export function snippetHTML(snippet: string | undefined): string {
  if (!snippet) return "";
  const escaped = snippet
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
  return escaped.replace(/&lt;mark&gt;/g, "<mark>").replace(/&lt;\/mark&gt;/g, "</mark>");
}

// SEARCH_EMPTY explains a zero-hit search, which is the single most misleading
// state in this tab: the backend matches WHOLE FTS tokens, so "kasir" finds
// nothing that "kasir_prod_db" finds (verified 2026-09-25). Without this, an
// operator concludes the store is empty when it was their query that missed.
export const SEARCH_EMPTY =
  "No page matched. Search matches whole words only — an identifier has to be typed in full, so \"kasir\" finds nothing that \"kasir_prod_db\" finds. Try a longer fragment, or the exact identifier.";

// ── raw observations ─────────────────────────────────────────────────

// RAW_HEADING is the section a session page carries its own event log under.
export const RAW_HEADING = "## Raw observations";

// RawObservations is what the tab can say about a session's raw events.
//
// Why this is a section of the page and not its own request: there is NO
// source for raw per-session observations outside the page. The backend's CLI
// has no subcommand for them (checked against the full command list, 2.4.0),
// and the MCP tool that does would be a third exception to this dashboard's
// no-MCP rule — which is authorised for exactly two calls, and this is not one
// of them (PLAN §13.2.1, §20.2). So the tab reads the section the consolidator
// already wrote into the page, and says plainly when a page has none rather
// than implying the session had no events.
export type RawObservations =
  | { present: true; lines: string[] }
  | { present: false; reason: string };

export function rawObservations(page: Page | null): RawObservations {
  if (!page) return { present: false, reason: "No page open." };
  const i = page.body.indexOf(RAW_HEADING);
  if (i < 0) {
    return {
      present: false,
      reason:
        "This page carries no raw-observation section. Only a session page has one, and only the backend writes it — wick has no other way to read a session's raw events.",
    };
  }
  const after = page.body.slice(i + RAW_HEADING.length);
  // Stop at the next heading: what follows the list is the consolidator's own
  // footer, not an observation.
  const end = after.search(/\n#{1,6} /);
  const block = end < 0 ? after : after.slice(0, end);
  const lines = block
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l.startsWith("- "))
    .map((l) => l.slice(2).trim());
  if (lines.length === 0) {
    return { present: false, reason: "The raw-observation section is present but empty." };
  }
  return { present: true, lines };
}

// pageScope labels which store slot a page came from — the same reason
// hitScope exists, applied to the open page.
export function pageScope(page: Page | null): string {
  if (!page) return "";
  const ws = (page.workspace ?? "").trim();
  const proj = (page.project ?? "").trim();
  return ws && proj ? `${ws}/${proj}` : proj || ws || "";
}

// frontmatterRows flattens a page's frontmatter for display. The keys differ
// per page kind, so they are shown as they arrive; nested values are rendered
// as JSON rather than dropped, because `sources` is where a session page
// records which agent wrote it.
export function frontmatterRows(page: Page | null): { key: string; value: string }[] {
  const fm = page?.frontmatter;
  if (!fm || typeof fm !== "object") return [];
  return Object.entries(fm).map(([key, v]) => ({
    key,
    value: typeof v === "string" ? v : JSON.stringify(v),
  }));
}
