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

  // A room runs one sub-agent at a time. Reading the rail top-down should
  // answer "what is happening, what is next, what is done" without
  // cross-referencing status chips row by row.
  test("rows are grouped Working, Queued, Finished", () => {
    render(SubAgentPanel, {
      props: props({
        subAgents: [
          subAgent({ delegation_id: "d-done", status: "done" }),
          subAgent({
            delegation_id: "d-queued",
            status: "queued",
            lifecycle: "",
            queue_position: 2,
          }),
          subAgent({ delegation_id: "d-run", status: "running", lifecycle: "working" }),
        ],
      }),
    });

    const headings = screen
      .getAllByTestId("subagent-group")
      .map((el) => el.textContent?.trim());
    expect(headings).toEqual(["Working", "Queued", "Finished"]);
  });

  // The position is the whole point of showing a queue: a leader that
  // fanned out four investigators needs to see three of them have not
  // started yet.
  test("a queued row shows its place in line", () => {
    render(SubAgentPanel, {
      props: props({
        subAgents: [
          subAgent({ status: "queued", lifecycle: "", queue_position: 3, result: undefined }),
        ],
      }),
    });
    expect(screen.getByText("#3")).toBeTruthy();
  });

  // No time estimate anywhere: an honest one is not available, and a
  // dishonest one is worse than nothing.
  test("a queued row promises no start time", () => {
    render(SubAgentPanel, {
      props: props({
        subAgents: [
          subAgent({ status: "queued", lifecycle: "", queue_position: 1, result: undefined }),
        ],
      }),
    });
    expect(screen.queryByText(/starts in|eta|estimated/i)).toBeNull();
  });

  // Empty groups must not render their heading — three headings over one
  // row reads as two things having gone missing.
  test("a group with no rows shows no heading", () => {
    render(SubAgentPanel, { props: props() });
    const headings = screen
      .getAllByTestId("subagent-group")
      .map((el) => el.textContent?.trim());
    expect(headings).toEqual(["Finished"]);
  });

  // Confidence is the first thing a reader checks before acting on a
  // finding, so it has to be visible without opening the transcript.
  test("a finished sub-agent shows its reported confidence", () => {
    render(SubAgentPanel, {
      props: props({
        subAgents: [
          subAgent({
            envelope: {
              summary: "401 spike traced to the retry path",
              confidence: "high",
              structured: true,
              evidence: [{ kind: "log", source: "loki: app=abc", excerpt: "401 signature_invalid" }],
            },
          }),
        ],
      }),
    });
    expect(screen.getByText("High confidence")).toBeTruthy();
  });

  // An agent that never called report_result asserted nothing. That is a
  // different state from "asserted, but unsure", and conflating them lets
  // a supervisor act on findings nobody stood behind.
  test("an unreported result is labelled Unreported, not Unknown", () => {
    render(SubAgentPanel, {
      props: props({
        subAgents: [
          subAgent({
            envelope: { summary: "some prose", confidence: "unknown", structured: false },
          }),
        ],
      }),
    });
    expect(screen.getByText("Unreported")).toBeTruthy();
    expect(screen.queryByText(/confidence/i)).toBeNull();
  });

  test("a sub-agent with no envelope shows no confidence chip", () => {
    render(SubAgentPanel, { props: props() });
    expect(screen.queryByText(/confidence|unreported/i)).toBeNull();
  });

  // An investigation's state is the frame every row below it sits in, so
  // it belongs above the list rather than buried in one card.
  test("an investigation shows its status, round and evidence count", () => {
    render(SubAgentPanel, {
      props: props({
        incident: {
          status: "investigating",
          iteration: 2,
          summary: "401s on the abc.com webhook",
          evidence_count: 5,
        },
      }),
    });
    expect(screen.getByText("Investigating")).toBeTruthy();
    expect(screen.getByText(/round 2/i)).toBeTruthy();
    expect(screen.getByText(/5 evidence/i)).toBeTruthy();
    expect(screen.getByText(/401s on the abc.com webhook/)).toBeTruthy();
  });

  // Most conversations are not investigations. A header on all of them
  // would be noise that teaches people to ignore it.
  test("a conversation with no investigation shows no incident header", () => {
    render(SubAgentPanel, { props: props() });
    expect(screen.queryByTestId("incident-header")).toBeNull();
  });

  // A stopped investigation that does not say why is indistinguishable
  // from one still running.
  test("an escalated investigation shows why it stopped", () => {
    render(SubAgentPanel, {
      props: props({
        incident: {
          status: "escalated",
          iteration: 5,
          summary: "",
          stop_reason: "iteration cap",
          evidence_count: 3,
        },
      }),
    });
    expect(screen.getByText("Escalated")).toBeTruthy();
    expect(screen.getByText(/iteration cap/)).toBeTruthy();
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
