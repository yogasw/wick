/*
 * Purpose:    Reply-quote jump controller — scrolls to the quoted turn when it
 *             is on screen, otherwise fetches ONE window around it
 *             (`?around=`) and hands it to the view as a detached window.
 *             Owns the single in-flight lookup, the slow-lookup toast and the
 *             "Back to reply" target. No DOM or API of its own: DetailView
 *             injects both, so the flow is testable without mounting it.
 * Caller:     DetailView (jumpToTurn, Back to reply chip)
 * Main Functions: createTurnJump
 */

import type { ConversationTurn } from "./types/agents.js";

/** One page of history plus whether more lies on either side of it. */
export type TurnWindow = {
  turns: ConversationTurn[];
  hasMore: boolean;
  hasMoreAfter: boolean;
};

/** The bubble whose quote was clicked — where "Back to reply" returns. */
export type BackTarget = { turnId: string };

export const JUMP_NOT_FOUND = "Original message is no longer available";
export const JUMP_FINDING = "Finding message…";
/** A lookup slower than this also says so in a toast. */
export const JUMP_SLOW_MS = 400;

export type TurnJumpDeps = {
  findEl(turnId: string): HTMLElement | null;
  /** `around=<turnId>`; null when the server no longer has that turn. */
  fetchAround(turnId: string): Promise<TurnWindow | null>;
  /** Replace the thread with a detached window and wait for it to render. */
  showWindow(win: TurnWindow): Promise<void>;
  /** Scroll the turn into view and flash it. `instant` after a window swap:
      a smooth glide across a freshly replaced list is just noise. */
  reveal(el: HTMLElement, instant: boolean): void;
  /** The reply bubble whose quote is being looked up (spinner), or null. */
  setPending(fromTurnId: string | null): void;
  setBack(back: BackTarget | null): void;
  toastPending(title: string): number;
  dismissToast(id: number): void;
  toastWarn(title: string): void;
  toastError(title: string, body?: string): void;
  slowMs?: number;
};

export type TurnJump = {
  /** Jump to `turnId`; `fromTurnId` is the bubble whose quote was clicked.
      While a lookup is out, every call returns that same promise. */
  jump(turnId: string, fromTurnId?: string): Promise<void>;
  /** Return to the bubble a jump started from. */
  back(target: BackTarget): Promise<void>;
  busy(): boolean;
  /** Drop a running lookup: its answer, if it still comes, changes nothing
      (Jump to latest while one is out). */
  cancel(): void;
  /** cancel() for good — the view is gone. */
  dispose(): void;
};

export function createTurnJump(deps: TurnJumpDeps): TurnJump {
  let inFlight: Promise<void> | null = null;
  // Bumped by cancel(): a lookup whose generation moved on is stale.
  let gen = 0;
  let disposed = false;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let toastId: number | null = null;

  function settle() {
    if (timer !== null) { clearTimeout(timer); timer = null; }
    if (toastId !== null) { deps.dismissToast(toastId); toastId = null; }
    deps.setPending(null);
    inFlight = null;
  }

  function lookup(turnId: string, pendingId: string | null, after: () => void): Promise<void> {
    const my = ++gen;
    deps.setPending(pendingId);
    timer = setTimeout(() => { timer = null; toastId = deps.toastPending(JUMP_FINDING); }, deps.slowMs ?? JUMP_SLOW_MS);
    const p = (async () => {
      try {
        const win = await deps.fetchAround(turnId);
        if (my !== gen) return;
        if (!win) {
          deps.toastWarn(JUMP_NOT_FOUND);
          return;
        }
        await deps.showWindow(win);
        if (my !== gen) return;
        const el = deps.findEl(turnId);
        if (el) deps.reveal(el, true);
        after();
      } catch (e: unknown) {
        if (my === gen) deps.toastError("History", e instanceof Error ? e.message : String(e));
      } finally {
        if (my === gen) settle();
      }
    })();
    inFlight = p;
    return p;
  }

  function cancel() {
    if (!inFlight) return;
    gen++;
    settle();
  }

  return {
    jump(turnId, fromTurnId) {
      if (disposed) return Promise.resolve();
      // Clicks on any quote are ignored while one lookup runs: they get the
      // running promise, never a second request.
      if (inFlight) return inFlight;
      const markBack = () => { if (fromTurnId) deps.setBack({ turnId: fromTurnId }); };
      const el = deps.findEl(turnId);
      if (el) {
        deps.reveal(el, false);
        markBack();
        return Promise.resolve();
      }
      return lookup(turnId, fromTurnId ?? null, markBack);
    },
    back(target) {
      if (disposed) return Promise.resolve();
      if (inFlight) return inFlight;
      deps.setBack(null);
      const el = deps.findEl(target.turnId);
      if (el) {
        deps.reveal(el, true);
        return Promise.resolve();
      }
      // The bubble was dropped with the window it sat in: fetch its own.
      return lookup(target.turnId, null, () => {});
    },
    busy: () => inFlight !== null,
    cancel,
    dispose() {
      cancel();
      disposed = true;
    },
  };
}
