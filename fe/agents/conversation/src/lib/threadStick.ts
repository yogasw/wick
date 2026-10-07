/* Bottom-pin scroll behaviour for the conversation thread (DetailView.svelte),
   pulled out of the component so it can be unit-tested without a browser.

   While the reader is at the bottom (stuck), every content growth — a new
   turn, streaming text, an HTML-artifact iframe sizing itself — is followed
   down. Only a GESTURE releases that: wheel up, an upward touch drag, an
   up-key, or grabbing the scrollbar. A scroll that is not a gesture (the
   browser clamping scrollTop when content shrinks, a scrollIntoView coming
   out of an artifact iframe) never releases it, so a preview that collapses
   on remount and grows back can no longer strand a stuck reader near the top
   of the widget: the next resize pins them down again.

   Thresholds are deliberately different. 80px is the Jump-button threshold —
   far enough up that an overlay is worth showing. Re-pinning on scroll is
   stricter, only when actually parked at the bottom (≤4px): sharing 80px made
   every short scroll (one wheel notch ≈ 40px) re-pin and yank the thread back
   down — the panel blinked on small scrolls. */

export const JUMP_THRESHOLD = 80;
export const REPIN_THRESHOLD = 4;
// How long a smooth scroll-to-bottom is given to run. Inside it, growth
// RETARGETS the smooth scroll instead of snapping (no smooth + instant
// collision), and the intermediate scroll events do not toggle the Jump
// button (no blink while it glides).
const SMOOTH_MS = 450;
const UP_KEYS = new Set(["PageUp", "ArrowUp", "Home"]);

export type ThreadScroll = {
  /** Pin to the bottom and stick. `smooth` for send / Jump / Ctrl+↓; open
      and refresh call it without, so the landing is instant. */
  scrollToBottom(o?: { smooth?: boolean }): void;
  /** Follow content growth if stuck — call when turns / live text change.
      Cheap: coalesced to at most one pin per frame however often it is
      called (it runs on every streamed token). */
  followIfStuck(): void;
  /** Set scrollTop without the scroll event counting (history prepend). */
  setTop(top: number): void;
  isStuck(): boolean;
  destroy(): void;
};

export type ThreadScrollOpts = {
  onJump?: (show: boolean) => void;
  onNearTop?: () => void;
};

const raf: (cb: () => void) => void =
  typeof requestAnimationFrame === "function" ? (cb) => requestAnimationFrame(cb) : (cb) => setTimeout(cb, 16);

const now = () => (typeof performance !== "undefined" ? performance.now() : Date.now());

function reducedMotion(): boolean {
  try {
    return typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches;
  } catch {
    return false;
  }
}

export function attachThreadScroll(el: HTMLElement, opts: ThreadScrollOpts = {}): ThreadScroll {
  // Starts stuck: a fresh open lands at the latest turn and stays there
  // through the post-mount settle while artifact iframes size themselves.
  let stick = true;
  let suppress = false;
  let smoothUntil = 0;
  let pinQueued = false;
  let jumpShown = false;
  const dist = () => el.scrollHeight - el.scrollTop - el.clientHeight;

  // Mirrored onto the scroller so HtmlArtifact knows not to compensate
  // scrollTop for its own resize while the thread is pinned (the pin wins).
  function setStick(v: boolean) {
    stick = v;
    if (v) el.setAttribute("data-stick-bottom", "");
    else el.removeAttribute("data-stick-bottom");
  }
  setStick(true);

  function quiet(write: () => void) {
    suppress = true;
    write();
    raf(() => { suppress = false; });
  }
  // Only report CHANGES of the Jump state — no re-render per scroll event.
  function jump(show: boolean) {
    if (show === jumpShown) return;
    jumpShown = show;
    opts.onJump?.(show);
  }
  function pin() {
    const top = el.scrollHeight;
    if (now() < smoothUntil && typeof el.scrollTo === "function") el.scrollTo({ top, behavior: "smooth" });
    else el.scrollTop = top;
    jump(false);
  }
  function schedulePin() {
    if (pinQueued) return;
    pinQueued = true;
    raf(() => {
      pinQueued = false;
      if (stick) pin();
    });
  }
  // A gesture up is the reader's INTENT to leave the bottom and must win
  // instantly: the scroll event it causes lands a beat later, and a resize in
  // that gap would otherwise re-pin and swallow the gesture.
  function release() {
    smoothUntil = 0;
    setStick(false);
    jump(true);
  }

  function onScroll() {
    if (suppress || now() < smoothUntil) return;
    const d = dist();
    // Re-pin only. Releasing here would let a clamp or a scrollIntoView
    // un-stick the reader; releasing belongs to the gesture listeners.
    if (d <= REPIN_THRESHOLD) setStick(true);
    jump(!stick && d > JUMP_THRESHOLD);
    if (el.scrollTop < 80) opts.onNearTop?.();
  }

  // Content changed size with no scroll event (an iframe resized, a turn
  // arrived, content shrank and the browser clamped). Stuck → pin, even
  // inside a suppress window: pinning is idempotent, and skipping it was how
  // a shrink-then-grow left a stuck reader in the middle.
  function onResize() {
    if (stick) pin();
    else jump(dist() > JUMP_THRESHOLD);
  }

  function onWheel(e: WheelEvent) {
    if (e.deltaY < 0) release();
  }
  let touchY = 0;
  function onTouchStart(e: TouchEvent) {
    touchY = e.touches[0]?.clientY ?? 0;
  }
  function onTouchMove(e: TouchEvent) {
    const y = e.touches[0]?.clientY ?? touchY;
    if (y > touchY + 2) release();
    touchY = y;
  }
  function onKeydown(e: KeyboardEvent) {
    if (UP_KEYS.has(e.key) || (e.key === " " && e.shiftKey)) release();
  }
  // A press on the scroller's own scrollbar gutter (target is the scroller,
  // x past its content width). Dragging back to the bottom re-pins on scroll.
  function onPointerDown(e: PointerEvent) {
    if (e.target === el && e.offsetX >= el.clientWidth) release();
  }

  el.addEventListener("scroll", onScroll, { passive: true });
  el.addEventListener("wheel", onWheel, { passive: true });
  el.addEventListener("touchstart", onTouchStart, { passive: true });
  el.addEventListener("touchmove", onTouchMove, { passive: true });
  el.addEventListener("keydown", onKeydown);
  el.addEventListener("pointerdown", onPointerDown, { passive: true });

  // Observe the scroller AND its content column: the column is what grows
  // when a NEW turn is appended, so growth from turns that did not exist at
  // mount is still caught.
  let ro: ResizeObserver | null = null;
  if (typeof ResizeObserver !== "undefined") {
    ro = new ResizeObserver(() => onResize());
    ro.observe(el);
    for (const child of Array.from(el.children)) ro.observe(child);
  }

  return {
    scrollToBottom(o?: { smooth?: boolean }) {
      setStick(true);
      const smooth = !!o?.smooth && !reducedMotion() && typeof el.scrollTo === "function";
      smoothUntil = smooth ? now() + SMOOTH_MS : 0;
      quiet(pin);
    },
    followIfStuck() {
      if (stick) schedulePin();
    },
    setTop(top: number) {
      quiet(() => { el.scrollTop = top; });
    },
    isStuck: () => stick,
    destroy() {
      el.removeEventListener("scroll", onScroll);
      el.removeEventListener("wheel", onWheel);
      el.removeEventListener("touchstart", onTouchStart);
      el.removeEventListener("touchmove", onTouchMove);
      el.removeEventListener("keydown", onKeydown);
      el.removeEventListener("pointerdown", onPointerDown);
      ro?.disconnect();
    },
  };
}
