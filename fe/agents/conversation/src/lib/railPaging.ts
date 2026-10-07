/* Paging for the ticket board's untracked rail.

   The poll re-reads only the FIRST page; older pages are fetched once, on
   scroll, and kept. Each page is asked for by the server's cursor — "the
   rows after this one" — never by how many rows are drawn: a chat that
   left the untracked set some other way (attached by someone else, made a
   ticket off-board, deleted) can still be drawn from a kept page, and a
   count that included it would start the next page one row too far and
   skip a chat for the rest of the visit. Such a row may stay DRAWN until
   the request changes — the server does not list what left — but it can
   no longer move the paging.

   Two things keep the kept pages honest while the order moves under them
   (a chat used right now jumps to the top):

   - a chat held on a kept page AND in the fresh first page is drawn once,
     where the first page puts it.
   - a chat pushed off the first page by newer activity sits between the
     fresh first page and the kept pages' cursor — before where the next
     page starts, so no request will bring it back. It is kept at the head
     of the kept pages rather than lost from the rail for the visit.

   `tracked` is the chats this page just put on a ticket: gone from the rail
   even though a kept page still carries them. */
import type { TicketSessionRow } from "./types/agents.js";

export function mergeRail(
  first: TicketSessionRow[],
  more: TicketSessionRow[],
  tracked: ReadonlySet<string>,
): TicketSessionRow[] {
  if (more.length === 0) return first;
  const seen = new Set(first.map((r) => r.id));
  return [...first, ...more.filter((r) => !seen.has(r.id) && !tracked.has(r.id))];
}

/* `pageSize` is what the first page was asked for. A first page that came
   back SHORT of it is the whole set: nothing was pushed off it, and every
   kept row it lacks has left — so the kept pages are dropped outright, the
   one case where a ghost is cheap to tell from a row that moved. */
export function keepPushedOff(
  prevFirst: TicketSessionRow[],
  nextFirst: TicketSessionRow[],
  more: TicketSessionRow[],
  tracked: ReadonlySet<string>,
  pageSize: number,
): TicketSessionRow[] {
  if (more.length === 0) return more;
  if (nextFirst.length < pageSize) return [];
  const now = new Set(nextFirst.map((r) => r.id));
  const held = new Set(more.map((r) => r.id));
  const pushedOff = prevFirst.filter((r) => !now.has(r.id) && !held.has(r.id) && !tracked.has(r.id));
  return pushedOff.length === 0 ? more : [...pushedOff, ...more];
}
