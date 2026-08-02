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
