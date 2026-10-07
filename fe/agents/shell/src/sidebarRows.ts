/* Live sidebar rows: ages, running accent, and re-sorting.

   Split from main.ts so the DOM side is testable under jsdom. The rows are
   the <a data-session-row> nodes layout.templ renders inside
   [data-sidebar-sessions]; this code only ever re-reads and re-orders
   them, it never builds one. */

import { relativeAge, isRunningStatus, sortRows, touchesActivity } from "./sidebarOrder.js";

/* MIRRORS the running classes in layout.templ sidebarSessionRow. */
const RUNNING_BORDER = "border-green-500";
const IDLE_BORDER = "border-transparent";
const RUNNING_WASH = ["bg-green-500/5", "dark:bg-green-500/10"];

function rowsIn(list: HTMLElement): HTMLElement[] {
  return Array.from(list.querySelectorAll<HTMLElement>(":scope > [data-session-row]"));
}

function lastActiveOf(row: HTMLElement): number {
  return Number(row.dataset["lastActive"] ?? "0") || 0;
}

/* refreshAges rewrites every row's age label from its data-last-active. */
export function refreshAges(list: HTMLElement, nowMs: number): void {
  for (const row of rowsIn(list)) {
    const age = row.querySelector<HTMLElement>("[data-session-age]");
    if (age) {
      age.textContent = relativeAge(lastActiveOf(row), nowMs);
    }
  }
}

/* applyRowStatus repaints one row for a new effective status: the accent
   border and the age label swap with the spinner, and a transition that
   counts as use (see touchesActivity) stamps the row "now". Returns true
   when the row's sort key changed, so the caller knows a re-sort is due. */
export function applyRowStatus(
  row: HTMLElement,
  prevStatus: string | undefined,
  status: string,
  nowMs: number,
): boolean {
  const wasRunning = row.dataset["running"] === "true";
  const running = isRunningStatus(status);
  let changed = wasRunning !== running;
  row.dataset["running"] = String(running);
  row.classList.toggle(RUNNING_BORDER, running);
  row.classList.toggle(IDLE_BORDER, !running);
  // The open row keeps its own highlight; a wash on top would muddy it.
  const wash = running && row.getAttribute("aria-current") !== "page";
  for (const c of RUNNING_WASH) {
    row.classList.toggle(c, wash);
  }
  if (touchesActivity(prevStatus, status)) {
    row.dataset["lastActive"] = String(nowMs);
    changed = true;
  }
  const age = row.querySelector<HTMLElement>("[data-session-age]");
  if (age) {
    age.classList.toggle("hidden", running);
    age.textContent = relativeAge(lastActiveOf(row), nowMs);
  }
  return changed;
}

/* resortRows puts the rows in running-first, newest-first order. It only
   touches the DOM when the order actually differs, so a no-op re-sort does
   not reset scroll or hover state. */
export function resortRows(list: HTMLElement): void {
  const rows = rowsIn(list);
  const keyed = rows.map((el) => ({
    el,
    id: el.dataset["sessionRow"] ?? "",
    running: el.dataset["running"] === "true",
    lastActive: lastActiveOf(el),
  }));
  const sorted = sortRows(keyed);
  if (sorted.every((r, i) => r.el === rows[i])) {
    return;
  }
  for (const r of sorted) {
    list.appendChild(r.el);
  }
}

/* createResorter returns request(): ask for a re-sort after a live event.

   Rows moving under the cursor is how a click lands on the wrong session,
   so a re-sort waits while the pointer is over the list, pressed, or
   dragging a row, and runs once it leaves. Bursts of events (a turn emits
   several transitions in a second) collapse into at most one re-sort per
   minIntervalMs. */
export function createResorter(
  list: HTMLElement,
  opts: { minIntervalMs?: number; now?: () => number } = {},
): { request: () => void } {
  const minInterval = opts.minIntervalMs ?? 3000;
  const now = opts.now ?? (() => Date.now());
  let hovering = false;
  let pressing = false;
  let dragging = false;
  let pending = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let lastRun = -Infinity;

  const busy = () => hovering || pressing || dragging;
  const run = () => {
    timer = undefined;
    if (!pending) {
      return;
    }
    if (busy()) {
      return; // flush() runs it when the pointer lets go
    }
    const wait = lastRun + minInterval - now();
    if (wait > 0) {
      timer = setTimeout(run, wait);
      return;
    }
    pending = false;
    lastRun = now();
    resortRows(list);
  };
  const flush = () => {
    if (pending && timer === undefined) {
      run();
    }
  };

  list.addEventListener("pointerenter", () => { hovering = true; });
  list.addEventListener("pointerleave", () => { hovering = false; pressing = false; flush(); });
  list.addEventListener("pointerdown", () => { pressing = true; });
  list.addEventListener("pointerup", () => { pressing = false; flush(); });
  list.addEventListener("pointercancel", () => { pressing = false; flush(); });
  list.addEventListener("dragstart", () => { dragging = true; });
  list.addEventListener("dragend", () => { dragging = false; flush(); });

  return {
    request: () => {
      pending = true;
      if (timer === undefined) {
        run();
      }
    },
  };
}
