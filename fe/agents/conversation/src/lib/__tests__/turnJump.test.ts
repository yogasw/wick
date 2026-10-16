import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { createTurnJump, JUMP_FINDING, JUMP_NOT_FOUND, type TurnJumpDeps, type TurnWindow } from "../turnJump.js";
import type { ConversationTurn } from "../types/agents.js";

const turn = (id: string) => ({ turn_id: id, role: "user", text: id }) as ConversationTurn;
const win = (...ids: string[]): TurnWindow => ({ turns: ids.map(turn), hasMore: true, hasMoreAfter: true });

/* A fake thread: `dom` is the set of turn ids rendered; showWindow swaps it
   for the window, the way DetailView's detached mode does. */
function setup(over: Partial<TurnJumpDeps> = {}) {
  const dom = new Set<string>(["r1", "r2"]);
  const els = new Map<string, HTMLElement>();
  const el = (id: string) => {
    if (!els.has(id)) {
      const e = document.createElement("div");
      e.dataset.turnId = id;
      els.set(id, e);
    }
    return els.get(id)!;
  };
  const state = { detached: false, pending: [] as (string | null)[], back: null as { turnId: string } | null };
  const deps: TurnJumpDeps = {
    findEl: (id) => (dom.has(id) ? el(id) : null),
    fetchAround: vi.fn(async (id: string) => win("a", id, "b")),
    showWindow: vi.fn(async (w: TurnWindow) => {
      dom.clear();
      w.turns.forEach((t) => dom.add(t.turn_id));
      state.detached = true;
    }),
    reveal: vi.fn(),
    setPending: (id) => { state.pending.push(id); },
    setBack: (b) => { state.back = b; },
    toastPending: vi.fn(() => 7),
    dismissToast: vi.fn(),
    toastWarn: vi.fn(),
    toastError: vi.fn(),
    ...over,
  };
  return { jump: createTurnJump(deps), deps, dom, state, el };
}

describe("turnJump", () => {
  beforeEach(() => { vi.useFakeTimers(); });
  afterEach(() => { vi.useRealTimers(); });

  test("target on screen: scrolls to it (smooth), no request, Back to reply set", async () => {
    const { jump, deps, state, el } = setup();
    await jump.jump("r1", "r2");
    expect(deps.fetchAround).not.toHaveBeenCalled();
    expect(deps.reveal).toHaveBeenCalledWith(el("r1"), false);
    expect(state.back).toEqual({ turnId: "r2" });
    expect(state.detached).toBe(false);
  });

  test("target not on screen: ONE around request, detached window, instant scroll", async () => {
    const { jump, deps, state, el } = setup();
    await jump.jump("old", "r2");
    expect(deps.fetchAround).toHaveBeenCalledTimes(1);
    expect(deps.fetchAround).toHaveBeenCalledWith("old");
    expect(deps.showWindow).toHaveBeenCalledTimes(1);
    expect(state.detached).toBe(true);
    expect(deps.reveal).toHaveBeenCalledWith(el("old"), true);
    expect(state.back).toEqual({ turnId: "r2" });
    // Spinner on the clicked quote's bubble, cleared at the end.
    expect(state.pending).toEqual(["r2", null]);
    expect(jump.busy()).toBe(false);
  });

  test("not found: toast, the thread is left alone, no Back to reply", async () => {
    const { jump, deps, state } = setup({ fetchAround: vi.fn(async () => null) });
    await jump.jump("gone", "r2");
    expect(deps.toastWarn).toHaveBeenCalledWith(JUMP_NOT_FOUND);
    expect(deps.showWindow).not.toHaveBeenCalled();
    expect(state.detached).toBe(false);
    expect(state.back).toBeNull();
  });

  test("a failed lookup surfaces an error toast and clears the spinner", async () => {
    const { jump, deps, state } = setup({ fetchAround: vi.fn(async () => { throw new Error("boom"); }) });
    await jump.jump("old", "r2");
    expect(deps.toastError).toHaveBeenCalledWith("History", "boom");
    expect(state.pending.at(-1)).toBeNull();
  });

  test("in flight: every other quote click gets the same promise, no second request", async () => {
    let resolve!: (w: TurnWindow) => void;
    const fetchAround = vi.fn(() => new Promise<TurnWindow>((r) => { resolve = r; }));
    const { jump, deps } = setup({ fetchAround });
    const p1 = jump.jump("old", "r2");
    const p2 = jump.jump("old", "r2");
    const p3 = jump.jump("other", "r1");
    expect(p2).toBe(p1);
    expect(p3).toBe(p1);
    expect(jump.busy()).toBe(true);
    resolve(win("old"));
    await p1;
    expect(fetchAround).toHaveBeenCalledTimes(1);
    expect(deps.reveal).toHaveBeenCalledTimes(1);
  });

  test("a slow lookup says Finding message… after 400 ms, replaced by the result", async () => {
    let resolve!: (w: TurnWindow) => void;
    const { jump, deps } = setup({ fetchAround: vi.fn(() => new Promise<TurnWindow>((r) => { resolve = r; })) });
    const p = jump.jump("old", "r2");
    vi.advanceTimersByTime(399);
    expect(deps.toastPending).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(deps.toastPending).toHaveBeenCalledWith(JUMP_FINDING);
    resolve(win("old"));
    await p;
    expect(deps.dismissToast).toHaveBeenCalledWith(7);
  });

  test("a fast lookup never shows the Finding toast", async () => {
    const { jump, deps } = setup();
    await jump.jump("old", "r2");
    vi.advanceTimersByTime(1000);
    expect(deps.toastPending).not.toHaveBeenCalled();
    expect(deps.dismissToast).not.toHaveBeenCalled();
  });

  test("Back to reply: bubble still on screen → scroll to it, chip cleared", async () => {
    const { jump, deps, state, el } = setup();
    await jump.jump("r1", "r2");
    await jump.back(state.back!);
    expect(state.back).toBeNull();
    expect(deps.reveal).toHaveBeenLastCalledWith(el("r2"), true);
    expect(deps.fetchAround).not.toHaveBeenCalled();
  });

  test("Back to reply: bubble dropped with its window → around=<reply id>", async () => {
    const { jump, deps, state, el } = setup();
    await jump.jump("old", "r2");
    expect(state.back).toEqual({ turnId: "r2" });
    await jump.back(state.back!);
    expect(deps.fetchAround).toHaveBeenLastCalledWith("r2");
    expect(deps.reveal).toHaveBeenLastCalledWith(el("r2"), true);
    // Used once: it does not re-arm itself.
    expect(state.back).toBeNull();
  });

  test("cancel: a late answer changes nothing, toast and spinner are cleared, the next click asks again", async () => {
    let resolve!: (w: TurnWindow) => void;
    const fetchAround = vi.fn(() => new Promise<TurnWindow>((r) => { resolve = r; }));
    const { jump, deps, state } = setup({ fetchAround });
    const p = jump.jump("old", "r2");
    vi.advanceTimersByTime(400);
    jump.cancel();
    expect(deps.dismissToast).toHaveBeenCalledWith(7);
    expect(state.pending.at(-1)).toBeNull();
    expect(jump.busy()).toBe(false);
    resolve(win("old"));
    await p;
    expect(deps.showWindow).not.toHaveBeenCalled();
    expect(state.back).toBeNull();
    void jump.jump("old", "r2");
    expect(fetchAround).toHaveBeenCalledTimes(2);
  });

  test("dispose: no slow toast after unmount, later clicks are ignored", async () => {
    const fetchAround = vi.fn(() => new Promise<TurnWindow>(() => {}));
    const { jump, deps } = setup({ fetchAround });
    void jump.jump("old", "r2");
    jump.dispose();
    vi.advanceTimersByTime(1000);
    expect(deps.toastPending).not.toHaveBeenCalled();
    await jump.jump("other", "r1");
    expect(fetchAround).toHaveBeenCalledTimes(1);
  });
});
