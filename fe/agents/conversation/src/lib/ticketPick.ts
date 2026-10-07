/* Ranking for the rail's "move / attach to a ticket" picker.

   Ticket ids are long and share a prefix (a Notion-adopted id is 32 hex
   characters, and tickets made the same day start alike), so people paste
   or type an id to find one — an id hit must beat a title hit, or the
   ticket they named sinks under every title that happens to contain the
   same few characters.

   Rank, best first: exact id, id prefix, id contains, title contains. Ties
   keep the most recently updated ticket on top, which is also the order
   with no query at all. */
import type { TicketCard } from "./types/agents.js";

function idRank(id: string, q: string): number {
  // A pasted Notion link carries the id inside it — that is an exact hit.
  if (id === q || (id.length >= 8 && q.includes(id))) return 0;
  if (id.startsWith(q)) return 1;
  if (id.includes(q)) return 2;
  return -1;
}

export function rankTickets(tickets: TicketCard[], query: string): TicketCard[] {
  const q = query.trim().toLowerCase();
  const byRecent = (a: TicketCard, b: TicketCard) =>
    (b.updated_at ?? "").localeCompare(a.updated_at ?? "");
  if (q === "") return [...tickets].sort(byRecent);
  const scored: { t: TicketCard; rank: number }[] = [];
  for (const t of tickets) {
    let rank = idRank(t.id.toLowerCase(), q);
    if (rank < 0 && (t.title ?? "").toLowerCase().includes(q)) rank = 3;
    if (rank >= 0) scored.push({ t, rank });
  }
  scored.sort((a, b) => a.rank - b.rank || byRecent(a.t, b.t));
  return scored.map((s) => s.t);
}

/* A long id shown in a narrow row. Cut in the MIDDLE, not the end: ids
   minted close together share their head (3eb1f07f4ae08…), so an
   end-truncated list reads as the same id over and over — the tail is
   what tells them apart. Short codes (T-4F2A) pass through untouched. */
export function shortTicketId(id: string): string {
  return id.length <= 14 ? id : id.slice(0, 4) + "…" + id.slice(-6);
}
