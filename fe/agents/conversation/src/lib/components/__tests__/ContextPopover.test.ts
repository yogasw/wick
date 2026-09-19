import { describe, it, expect, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/svelte";
import ContextPopover from "../ContextPopover.svelte";
import type { SessionContext } from "../../api/context.js";

const TOTALS = {
  input: 10,
  cache_read: 190_000,
  cache_write: 16_000,
  output: 500,
  total: 206_510,
  cost_usd: 0.27,
  cache_hit_pct: 92,
};

function ctx(over: Partial<SessionContext> = {}): SessionContext {
  return {
    session_id: "S1",
    provider: "claude/opus",
    model: "claude-opus-5",
    used: 210_886,
    window: 1_000_000,
    pct: 21,
    turns: 12,
    totals: TOTALS,
    providers: [
      {
        provider: "claude/opus",
        used: 210_886,
        window: 1_000_000,
        pct: 21,
        turns: 12,
        totals: TOTALS,
      },
    ],
    trend: [100, 5_000, 210_886],
    ...over,
  };
}

const base = {
  open: true,
  loading: false,
  error: "",
  onRefresh: () => {},
  onCompact: () => {},
  compacting: false,
  onClose: () => {},
};

describe("ContextPopover", () => {
  it("shows the fill and says there is room", () => {
    render(ContextPopover, { props: { ...base, data: ctx() } });
    expect(screen.getByText("21%")).toBeTruthy();
    expect(screen.getByText(/Plenty of room/i)).toBeTruthy();
  });

  it("shows what the whole session sent, next to the fill level", () => {
    // 206,510 total against a 210,886 fill: the two numbers answer
    // different questions and the panel has to carry both. The tile is
    // the one people mean by "how much has this session used".
    render(ContextPopover, { props: { ...base, data: ctx() } });
    const tile = screen.getByTestId("session-tokens");
    expect(tile.textContent?.trim()).toBe("207k");
    expect(tile.getAttribute("title")).toMatch(/206,510 tokens this session/);
    expect(screen.getByText(/92% hit/)).toBeTruthy();
  });

  it("offers no Compact button on a provider that cannot compact", () => {
    // codex: `codex exec` has no slash commands, so the button would
    // only make the model claim it compacted. Better to say so.
    render(ContextPopover, {
      props: {
        ...base,
        data: ctx({
          provider: "codex/default",
          can_compact: false,
          compact_note: "codex has no /compact: it compacts on its own once it reaches its limit.",
        }),
      },
    });
    expect(screen.getByTestId("compact-unavailable")).toBeTruthy();
    expect(screen.queryByText(/Compact conversation/i)).toBeNull();
    expect(screen.getByText(/compacts on its own/i)).toBeTruthy();
  });

  it("still offers Compact when the server says nothing about it", () => {
    // An older server sends no can_compact; silence must not remove a
    // button that used to work.
    render(ContextPopover, { props: { ...base, data: ctx() } });
    expect(screen.getByText(/Compact conversation/i)).toBeTruthy();
  });

  it("warns when the window is nearly full", () => {
    render(ContextPopover, { props: { ...base, data: ctx({ pct: 93 }) } });
    expect(screen.getByText(/Nearly full/i)).toBeTruthy();
  });

  it("shows tokens instead of a percentage when no window was reported", () => {
    render(ContextPopover, { props: { ...base, data: ctx({ window: 0, pct: 0, used: 20_250 }) } });
    // codex reports no window; inventing a denominator would be a lie.
    expect(screen.getByText(/doesn't report a window size/i)).toBeTruthy();
    expect(screen.queryByText("0%")).toBeNull();
  });

  it("never compacts on one click — it asks first", async () => {
    const onCompact = vi.fn();
    render(ContextPopover, { props: { ...base, data: ctx(), onCompact } });

    screen.getByText("Compact conversation").click();
    await waitFor(() => expect(screen.getByText(/Older turns are replaced/i)).toBeTruthy());
    expect(onCompact).not.toHaveBeenCalled();

    screen.getByText("Yes, compact").click();
    await waitFor(() => expect(onCompact).toHaveBeenCalledTimes(1));
  });

  it("lets the confirm be cancelled without sending anything", async () => {
    const onCompact = vi.fn();
    render(ContextPopover, { props: { ...base, data: ctx(), onCompact } });
    screen.getByText("Compact conversation").click();
    await waitFor(() => expect(screen.getByText("Cancel")).toBeTruthy());
    screen.getByText("Cancel").click();
    await waitFor(() => expect(screen.getByText("Compact conversation")).toBeTruthy());
    expect(onCompact).not.toHaveBeenCalled();
  });

  it("is honest about who compacts: the button never fires itself, the provider does", () => {
    render(ContextPopover, { props: { ...base, data: ctx() } });
    expect(screen.getByText(/never presses this for you/i)).toBeTruthy();
    expect(screen.getByText(/provider compacts on its own/i)).toBeTruthy();
  });

  it("says nothing was measured yet instead of showing 0%", () => {
    render(ContextPopover, {
      props: {
        ...base,
        data: ctx({ provider: "", turns: 0, used: 0, window: 0, pct: 0, providers: [] }),
      },
    });
    expect(screen.getByText(/No reading yet/i)).toBeTruthy();
  });

  it("lists other providers the session used", () => {
    const data = ctx({
      providers: [
        {
          provider: "claude/opus",
          used: 210_886,
          window: 1_000_000,
          pct: 21,
          turns: 12,
          totals: TOTALS,
        },
        { provider: "codex/gpt", used: 20_250, window: 0, pct: 0, turns: 3, totals: TOTALS },
      ],
    });
    render(ContextPopover, { props: { ...base, data } });
    expect(screen.getByText("codex/gpt")).toBeTruthy();
  });
});
