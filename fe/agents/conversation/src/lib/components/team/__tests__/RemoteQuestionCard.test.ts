import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/svelte";

let inputRequired = true;
const get = vi.fn((_b: string, _id: string, _sid?: string) =>
  Promise.resolve({ host: "h", session: { input_required: inputRequired, updated_at: "" } }));
vi.mock("../../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../../api/team.js")>()),
  getRemoteAgent: (b: string, id: string, sid?: string) => get(b, id, sid),
  runApi: <T,>(p: Promise<T>) => p,
}));

import RemoteQuestionCard from "../RemoteQuestionCard.svelte";

const props = (turn = "idle|1") => ({ base: "/tools/agents", agentId: "r1", sessionId: "s1", handle: "research", turn, question: "Which year?" });

describe("RemoteQuestionCard", () => {
  beforeEach(() => { get.mockClear(); inputRequired = true; });

  test("input-required shows the question card for this chat", async () => {
    render(RemoteQuestionCard, props());
    const card = await screen.findByTestId("remote-question");
    expect(card.textContent).toContain("@research is waiting for your answer");
    expect(card.textContent).toContain("Which year?");
    expect(card.textContent).toContain("same task");
    expect(get).toHaveBeenCalledWith("/tools/agents", "r1", "s1");
  });

  test("no card when the remote is not waiting; re-reads when the turn changes", async () => {
    inputRequired = false;
    const r = render(RemoteQuestionCard, props());
    await waitFor(() => expect(get).toHaveBeenCalledTimes(1));
    expect(screen.queryByTestId("remote-question")).toBeNull();
    inputRequired = true;
    await r.rerender(props("idle|2"));
    await screen.findByTestId("remote-question");
    expect(get).toHaveBeenCalledTimes(2);
  });
});
