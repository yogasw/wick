/* Split plain text into runs of text and URLs, so a field value like
   "Slack discussion: https://…/p1790…" can render the URL as a link with an
   open-in-new-tab button beside it and leave the rest as text.

   Only http(s) — a value is typed by a person or mirrored from another
   system, and anything else (javascript:, data:, file:) must never become
   something clickable. Trailing sentence punctuation is not part of the URL:
   "see https://x.io/a." links to https://x.io/a. */
export type LinkSegment = { kind: "text"; text: string } | { kind: "url"; url: string };

const URL_RE = /\bhttps?:\/\/[^\s<>"'`]+/gi;
const TRAILING = /[.,;:!?'")\]}>]+$/;

export function linkify(text: string | null | undefined): LinkSegment[] {
  if (!text) return [];
  const out: LinkSegment[] = [];
  let last = 0;
  for (const m of text.matchAll(URL_RE)) {
    const start = m.index ?? 0;
    let url = m[0];
    const trail = url.match(TRAILING)?.[0] ?? "";
    // Keep a closing paren that belongs to the URL itself, e.g. a wiki path
    // "…/Foo_(bar)": only strip it when the URL has no matching opener.
    let keep = "";
    if (trail.startsWith(")") && url.slice(0, -trail.length).includes("(")) {
      keep = ")";
    }
    url = url.slice(0, url.length - trail.length) + keep;
    if (start > last) out.push({ kind: "text", text: text.slice(last, start) });
    out.push({ kind: "url", url });
    last = start + url.length;
  }
  if (last < text.length) out.push({ kind: "text", text: text.slice(last) });
  return out;
}

/** The first URL in the text, when there is one — what the row's single
    open-in-new-tab button opens. */
export function firstUrl(text: string | null | undefined): string | null {
  const seg = linkify(text).find((s) => s.kind === "url");
  return seg && seg.kind === "url" ? seg.url : null;
}
