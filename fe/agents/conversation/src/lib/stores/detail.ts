import { writable } from "svelte/store";

// detail.ts backs the generic "detail modal" — a reusable overlay that
// shows the full body of a `detail` fence (or anything else) on demand, so
// long-but-occasionally-needed content (a compaction summary, a verbose
// explanation) stays out of the transcript flow and one click away.
//
// Any UI can open it: chips rendered by richRender dispatch a
// `wick-detail-open` CustomEvent that DetailView forwards here via showDetail.

export type DetailContent = { title: string; body: string };

export const currentDetail = writable<DetailContent | null>(null);

export function showDetail(d: DetailContent) {
  currentDetail.set(d);
}

export function hideDetail() {
  currentDetail.set(null);
}
