import { readable } from "svelte/store";

/* A clock, so "5m ago" does not quietly become a lie.

   Relative timestamps are derived from the current time, and Svelte only
   recomputes a derived value when something it reads changes — a label
   built from Date.now() would freeze at whatever it said on first paint.
   Reading this store makes the current time a dependency like any other.

   One shared store rather than a timer per component: readable's start
   function runs on the first subscriber and its teardown on the last, so
   a page with no relative timestamps on screen runs no interval at all.

   Thirty seconds because that is the resolution the labels have —
   anything finer redraws for no visible change. */
export const now = readable(Date.now(), (set) => {
  const id = setInterval(() => set(Date.now()), 30_000);
  return () => clearInterval(id);
});
