import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import SubAgentPanel from "../SubAgentPanel.svelte";
import type { SubAgentItem } from "../../types/agents.js";

function subAgent(over: Partial<SubAgentItem> = {}): SubAgentItem {
  return {
    delegation_id: "d1",
    child_session_id: "root--sub-9f2c81ab40de",
    profile_key: "game-player-2",
    label: "Permainan 'Tebak Kata Rahasia'. Participant A sudah memilih …",
    status: "done",
    lifecycle: "",
    depth: 0,
    turns_used: 1,
    max_turns: 3,
    result: '{"participant":"B","secret_word":"kursi"}',
    started_at: new Date(Date.now() - 60_000).toISOString(),
    ...over,
  };
}

function props(over: Record<string, unknown> = {}) {
  return {
    subAgents: [subAgent()],
    selectedId: null,
    onSelect: vi.fn(),
    onInterrupt: vi.fn(),
    onInterruptAll: vi.fn(),
    messages: [],
    hopsLeft: 4,
    onBumpHops: vi.fn(),
    ...over,
  };
}

describe("SubAgentPanel", () => {
  // The result preview is the part a reader reaches for when they want more,
  // so it must not be the one dead zone on the card.
  test("clicking the result preview opens the sub-agent", async () => {
    const p = props();
    render(SubAgentPanel, { props: p });

    await fireEvent.click(screen.getByText(/secret_word/));
    expect(p.onSelect).toHaveBeenCalledWith("root--sub-9f2c81ab40de");
  });

  test("clicking the task label opens the sub-agent", async () => {
    const p = props();
    render(SubAgentPanel, { props: p });

    await fireEvent.click(screen.getByText(/Tebak Kata Rahasia/));
    expect(p.onSelect).toHaveBeenCalledWith("root--sub-9f2c81ab40de");
  });

  test("the card opens on Enter, so it is reachable by keyboard", async () => {
    const p = props();
    render(SubAgentPanel, { props: p });

    await fireEvent.keyDown(screen.getByRole("button", { name: /open sub-agent/i }), {
      key: "Enter",
    });
    expect(p.onSelect).toHaveBeenCalledWith("root--sub-9f2c81ab40de");
  });

  // A "Running" chip says what the row is; the spinner says it is happening
  // now, which is the only thing visible when scanning a list of done rows.
  test("a working sub-agent spins, a finished one does not", () => {
    const { unmount } = render(SubAgentPanel, { props: props() });
    expect(screen.queryByLabelText("Working")).toBeNull();
    unmount();

    render(SubAgentPanel, {
      props: props({ subAgents: [subAgent({ status: "running", lifecycle: "working" })] }),
    });
    expect(screen.getByLabelText("Working")).toBeTruthy();
  });

  // status "running" with nothing spawned means queued behind a slot.
  // Spinning there promises progress that is not happening.
  test("a running sub-agent with no live process does not spin", () => {
    render(SubAgentPanel, {
      props: props({ subAgents: [subAgent({ status: "running", lifecycle: "idle" })] }),
    });
    expect(screen.queryByLabelText("Working")).toBeNull();
  });

  // A finished row is dated by when it FINISHED — that is what you want
  // when scanning results — and the rounded label carries the exact time
  // in its tooltip for when the age actually matters.
  test("a finished sub-agent shows how long ago it ended", () => {
    const ended = new Date(Date.now() - 5 * 60_000).toISOString();
    render(SubAgentPanel, {
      props: props({
        subAgents: [
          subAgent({
            started_at: new Date(Date.now() - 20 * 60_000).toISOString(),
            ended_at: ended,
          }),
        ],
      }),
    });
    const stamp = screen.getByText(/5m ago/);
    expect(stamp).toBeTruthy();
    expect(stamp.getAttribute("title")).toContain(String(new Date(ended).getFullYear()));
  });

  // The only interesting question about a running sub-agent is how long
  // it has been at it, so a live row is dated by its start instead.
  test("a running sub-agent shows how long it has been running", () => {
    render(SubAgentPanel, {
      props: props({
        subAgents: [
          subAgent({
            status: "running",
            lifecycle: "working",
            started_at: new Date(Date.now() - 3 * 60_000).toISOString(),
            ended_at: undefined,
          }),
        ],
      }),
    });
    expect(screen.getByText(/running 3m/)).toBeTruthy();
  });

  test("a row with no timestamps shows no stamp at all", () => {
    render(SubAgentPanel, {
      props: props({ subAgents: [subAgent({ started_at: undefined, ended_at: undefined })] }),
    });
    expect(screen.queryByText(/ago|running \d/)).toBeNull();
  });

  // Stop sits on top of a card that opens the transcript. Letting the click
  // through would put a panel in front of the run you just ended.
  test("Stop interrupts without also opening the sub-agent", async () => {
    const p = props({ subAgents: [subAgent({ status: "running" })] });
    render(SubAgentPanel, { props: p });

    await fireEvent.click(screen.getByRole("button", { name: /^stop$/i }));
    expect(p.onInterrupt).toHaveBeenCalledWith("d1");
    expect(p.onSelect).not.toHaveBeenCalled();
  });
});
