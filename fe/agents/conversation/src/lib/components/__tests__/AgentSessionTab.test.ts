import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { AgentSessionPolicy } from "../../api/team.js";

let current: AgentSessionPolicy;
const updateAgentSession = vi.fn((_b: string, _id: string, body: Partial<AgentSessionPolicy>) => {
  current = { ...current, ...body };
  return Promise.resolve(current);
});
const compactAgentMain = vi.fn(() => Promise.resolve({ status: "queued", session_id: "main1" }));
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  getAgentSession: () => Promise.resolve({ ...current }),
  updateAgentSession: (b: string, id: string, body: Partial<AgentSessionPolicy>) => updateAgentSession(b, id, body),
  compactAgentMain: () => compactAgentMain(),
  runApi: <T,>(p: Promise<T>) => p,
}));

import AgentSessionTab from "../AgentSessionTab.svelte";

const fresh = (o: Partial<AgentSessionPolicy> = {}): AgentSessionPolicy => ({
  compact: "auto", idle_hours: 8, summarise_threads: false, main_session_id: "main1", slack_dm_main_chat: true, slack_connected: true, ...o,
});

describe("AgentSessionTab", () => {
  beforeEach(() => { updateAgentSession.mockClear(); compactAgentMain.mockClear(); });

  test("switching to idle autosaves; hours are clamped and saved", async () => {
    current = fresh();
    render(AgentSessionTab, { props: { base: "/b", agentId: "a1" } });
    const hours = (await screen.findByLabelText("Idle hours")) as HTMLInputElement;
    expect(hours.disabled).toBe(true);
    await fireEvent.click(screen.getByLabelText(/Compact when idle for/));
    await waitFor(() => expect(updateAgentSession).toHaveBeenCalledWith("/b", "a1", { compact: "idle" }));
    await waitFor(() => expect(hours.disabled).toBe(false));
    await fireEvent.input(hours, { target: { value: "500" } });
    await waitFor(() => expect(updateAgentSession).toHaveBeenCalledWith("/b", "a1", { idle_hours: 168 }), { timeout: 2000 });
    expect(screen.getByTestId("session-save").textContent).toContain("Saved");
  });

  test("Compact now queues /compact; summarise flag saves; DM option is read-only", async () => {
    current = fresh({ slack_dm_main_chat: false });
    const onOpenConnections = vi.fn();
    render(AgentSessionTab, { props: { base: "/b", agentId: "a1", onOpenConnections } });
    await fireEvent.click(await screen.findByTestId("compact-now"));
    await waitFor(() => expect(compactAgentMain).toHaveBeenCalled());
    expect((await screen.findByTestId("compact-status")).textContent).toBe("Queued");
    await fireEvent.click(screen.getByRole("switch", { name: "Summarise threads" }));
    await waitFor(() => expect(updateAgentSession).toHaveBeenCalledWith("/b", "a1", { summarise_threads: true }));
    expect(screen.getByTestId("session-dm").textContent).toContain("a new chat per thread");
    await fireEvent.click(screen.getByRole("button", { name: "Open Connections" }));
    expect(onOpenConnections).toHaveBeenCalled();
  });

  test("no main chat yet: Compact now is disabled", async () => {
    current = fresh({ main_session_id: "" });
    render(AgentSessionTab, { props: { base: "/b", agentId: "a1" } });
    expect(((await screen.findByTestId("compact-now")) as HTMLButtonElement).disabled).toBe(true);
  });
});
