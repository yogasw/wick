/* One requestAnimationFrame loop and one pointermove listener shared by
   every AgentAvatar on the page (PLAN 6.5), instead of a loop per avatar.
   The loop runs only while someone is subscribed and the tab is visible;
   a hidden tab stops it and showing the tab again resumes it. */

type Frame = (tSeconds: number) => void;

const subs = new Set<Frame>();
let raf = 0;
let wired = false;

/** Last pointer position (client px) and when it moved (performance.now ms). */
export const pointer = { x: 0, y: 0, at: -1e9 };

/** POINTER_IDLE_MS: after this long without movement the eyes drift back. */
export const POINTER_IDLE_MS = 1500;

export const pointerActive = (nowMs: number) => nowMs - pointer.at < POINTER_IDLE_MS;

export function prefersReducedMotion(): boolean {
  return typeof window === "undefined" || typeof window.matchMedia !== "function"
    ? true
    : window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

function frame(ms: number) {
  raf = 0;
  const t = ms / 1000;
  for (const fn of subs) fn(t);
  schedule();
}

function schedule() {
  if (raf || subs.size === 0 || typeof requestAnimationFrame !== "function") return;
  if (typeof document !== "undefined" && document.visibilityState === "hidden") return;
  raf = requestAnimationFrame(frame);
}

function stop() {
  if (raf && typeof cancelAnimationFrame === "function") cancelAnimationFrame(raf);
  raf = 0;
}

function wire() {
  if (wired || typeof document === "undefined") return;
  wired = true;
  document.addEventListener(
    "pointermove",
    (e) => {
      pointer.x = e.clientX;
      pointer.y = e.clientY;
      pointer.at = performance.now();
    },
    { passive: true },
  );
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") stop();
    else schedule();
  });
}

/** subscribe calls fn every frame with the time in seconds; the returned
    function unsubscribes, and the loop stops with the last subscriber. */
export function subscribe(fn: Frame): () => void {
  wire();
  subs.add(fn);
  schedule();
  return () => {
    subs.delete(fn);
    if (subs.size === 0) stop();
  };
}
