import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { AgentSchedule, AgentScheduledList, AgentScheduleWrite } from "../../api/team.js";

const daily: AgentSchedule = {
  id: "s1", title: "Morning recap", message: "Morning recap", kind: "recurring", status: "active", cron: "0 9 * * *",
  next_run_at: "2026-10-04T02:00:00Z", run_count: 2, destination: "main", session_id: "main1",
};
let list: AgentScheduledList;
const createAgentSchedule = vi.fn((_b: string, _id: string, body: AgentScheduleWrite) =>
  Promise.resolve({ ...daily, id: "s2", message: body.message ?? "", title: body.message ?? "" }));
const updateAgentSchedule = vi.fn((_b: string, _id: string, _sid: string, body: AgentScheduleWrite) =>
  Promise.resolve({ ...daily, message: body.message ?? daily.message, cron: body.cron ?? daily.cron }));
const agentScheduleAction = vi.fn((_b: string, _id: string, _sid: string, action: string) =>
  Promise.resolve({ ...daily, paused: action === "pause" }));
const deleteAgentSchedule = vi.fn(() => Promise.resolve({}));
const getAgentScheduleRuns = vi.fn((_b: string, _id: string, _sid: string) => Promise.resolve({
  items: [
    { at: "2026-10-03T02:00:00Z", session_id: "main1", turn_id: "t2", status: "failed", error: "provider timed out" },
    { at: "2026-10-02T02:00:00Z", session_id: "main1", turn_id: "t1", status: "ok" },
  ],
}));
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  getAgentScheduled: () => Promise.resolve(structuredClone(list)),
  createAgentSchedule: (b: string, id: string, body: AgentScheduleWrite) => createAgentSchedule(b, id, body),
  updateAgentSchedule: (b: string, id: string, sid: string, body: AgentScheduleWrite) => updateAgentSchedule(b, id, sid, body),
  agentScheduleAction: (b: string, id: string, sid: string, a: string) => agentScheduleAction(b, id, sid, a),
  deleteAgentSchedule: () => deleteAgentSchedule(),
  getAgentScheduleRuns: (b: string, id: string, sid: string) => getAgentScheduleRuns(b, id, sid),
  runApi: <T,>(p: Promise<T>) => p,
}));

import AgentScheduled from "../AgentScheduled.svelte";
import type { AgentItem } from "../../api/team.js";

const agent = { id: "a1", handle: "rekap", name: "Rekap", avatar: { shape: "circle", color: "#6366f1" } } as unknown as AgentItem;
const base = (o: Partial<AgentScheduledList> = {}): AgentScheduledList => ({
  items: [daily], feature_on: true, agent_disabled: false, server_timezone: "Asia/Jakarta (UTC+07:00)", main_session_id: "main1", slack_online: false, ...o,
});

describe("AgentScheduled", () => {
  beforeEach(() => {
    vi.useRealTimers();
    for (const f of [createAgentSchedule, updateAgentSchedule, agentScheduleAction, deleteAgentSchedule, getAgentScheduleRuns]) f.mockClear();
  });

  test("list shows the human schedule, destination and status; the toggle pauses", async () => {
    list = base();
    render(AgentScheduled, { props: { base: "/b", agent, onClose: vi.fn() } });
    const row = await screen.findByTestId("scheduled-row");
    expect(row.textContent).toContain("Morning recap");
    expect(row.textContent).toContain("Every day 09:00");
    expect(row.textContent).toContain("Main chat");
    expect(row.textContent).toContain("On");
    await fireEvent.click(screen.getByRole("switch", { name: "On" }));
    await waitFor(() => expect(agentScheduleAction).toHaveBeenCalledWith("/b", "a1", "s1", "pause"));
    await waitFor(() => expect(screen.getByTestId("scheduled-row").textContent).toContain("Paused"));
  });

  test("⋯ menu: run now and delete with confirmation", async () => {
    list = base();
    render(AgentScheduled, { props: { base: "/b", agent, onClose: vi.fn() } });
    await screen.findByTestId("scheduled-row");
    await fireEvent.click(screen.getByRole("button", { name: "More" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Run now" }));
    await waitFor(() => expect(agentScheduleAction).toHaveBeenCalledWith("/b", "a1", "s1", "run"));
    await fireEvent.click(screen.getByRole("button", { name: "More" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Delete" }));
    const confirm = await screen.findByTestId("confirm-delete");
    expect(deleteAgentSchedule).not.toHaveBeenCalled();
    await fireEvent.click(confirm.querySelector("button")!);
    await waitFor(() => expect(deleteAgentSchedule).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByTestId("scheduled-row")).toBeNull());
  });

  test("create: button stays disabled until the message is written, sends into the main chat", async () => {
    list = base({ items: [], slack_online: true });
    render(AgentScheduled, { props: { base: "/b", agent, onClose: vi.fn() } });
    await fireEvent.click(await screen.findByRole("button", { name: "New schedule" }));
    expect(screen.getByTestId("server-tz").textContent).toContain("Asia/Jakarta");
    expect(screen.queryByTestId("dest-slack")).toBeNull();
    const create = screen.getByRole("button", { name: "Create" }) as HTMLButtonElement;
    expect(create.disabled).toBe(true);
    await fireEvent.input(screen.getByLabelText("Message to the agent"), { target: { value: "Check the inbox" } });
    await fireEvent.click(screen.getByRole("button", { name: "Every" }));
    await fireEvent.input(screen.getByLabelText("Every"), { target: { value: "2" } });
    await fireEvent.click(create);
    await waitFor(() => expect(createAgentSchedule).toHaveBeenCalled());
    expect(createAgentSchedule.mock.calls[0][2]).toEqual({ message: "Check the inbox", every: "2h", destination: "main" });
  });

  test("edit autosaves and keeps to the schedule's kind", async () => {
    list = base();
    render(AgentScheduled, { props: { base: "/b", agent, onClose: vi.fn() } });
    await screen.findByTestId("scheduled-row");
    await fireEvent.click(screen.getByRole("button", { name: "More" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Edit" }));
    expect((screen.getByRole("button", { name: "Once at" }) as HTMLButtonElement).disabled).toBe(true);
    await fireEvent.input(screen.getByLabelText("Cron"), { target: { value: "0 10 * * *" } });
    await waitFor(() => expect(updateAgentSchedule).toHaveBeenCalled(), { timeout: 2000 });
    expect(updateAgentSchedule.mock.calls[0][3]).toEqual({ message: "Morning recap", cron: "0 10 * * *", destination: "main" });
    await waitFor(() => expect(screen.getByTestId("save-state").textContent).toBe("Saved"));
  });

  test("feature off: explains and links to Tools & features", async () => {
    list = base({ feature_on: false });
    const onOpenTools = vi.fn();
    render(AgentScheduled, { props: { base: "/b", agent, onClose: vi.fn(), onOpenTools } });
    const off = await screen.findByTestId("scheduled-off");
    expect(off.textContent).toContain("Scheduling is off for this agent");
    expect(screen.queryByTestId("scheduled-list")).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Open Tools & features" }));
    expect(onOpenTools).toHaveBeenCalled();
  });

  test("a disabled agent shows its schedules as held", async () => {
    list = base({ agent_disabled: true, items: [{ ...daily, paused: true, held_by_agent: true }] });
    render(AgentScheduled, { props: { base: "/b", agent, onClose: vi.fn() } });
    const row = await screen.findByTestId("scheduled-row");
    expect(row.textContent).toContain("Held — agent off");
    expect(screen.getByText(/schedules are on hold/)).toBeTruthy();
  });

  test("Telegram destination is hidden without a connection", async () => {
    list = base({ items: [], telegram_connected: false });
    render(AgentScheduled, { props: { base: "/b", agent, onClose: vi.fn() } });
    await fireEvent.click(await screen.findByRole("button", { name: "New schedule" }));
    expect(screen.queryByTestId("dest-telegram")).toBeNull();
  });

  test("create into a Telegram chat of the agent's bot", async () => {
    const chats = [{ session_id: "tg-a", title: "Ops group" }, { session_id: "tg-b", title: "Rina" }];
    list = base({ items: [], telegram_connected: true, telegram_chats: chats });
    render(AgentScheduled, { props: { base: "/b", agent, onClose: vi.fn() } });
    await fireEvent.click(await screen.findByRole("button", { name: "New schedule" }));
    await fireEvent.input(screen.getByLabelText("Message to the agent"), { target: { value: "Daily digest" } });
    await fireEvent.click(screen.getByLabelText("Telegram chat", { selector: "input" }));
    await fireEvent.change(screen.getByLabelText("Telegram chat", { selector: "select" }), { target: { value: "tg-b" } });
    await fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() => expect(createAgentSchedule).toHaveBeenCalled());
    expect(createAgentSchedule.mock.calls[0][2]).toEqual({ message: "Daily digest", cron: "0 9 * * *", destination: "telegram", telegram_session: "tg-b" });
  });

  test("a Telegram row names its chat; editing can move it to the main chat", async () => {
    const chats = [{ session_id: "tg-a", title: "Ops group" }];
    list = base({ items: [{ ...daily, destination: "telegram", telegram_session: "tg-a", session_id: "tg-a" }], telegram_connected: true, telegram_chats: chats });
    render(AgentScheduled, { props: { base: "/b", agent, onClose: vi.fn() } });
    expect((await screen.findByTestId("scheduled-row")).textContent).toContain("Telegram · Ops group");
    await fireEvent.click(screen.getByRole("button", { name: "More" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Edit" }));
    expect((screen.getByLabelText("Telegram chat", { selector: "select" }) as HTMLSelectElement).value).toBe("tg-a");
    await fireEvent.click(screen.getByLabelText("Main chat"));
    await waitFor(() => expect(updateAgentSchedule).toHaveBeenCalled(), { timeout: 2000 });
    expect(updateAgentSchedule.mock.calls[0][3]).toEqual({ message: "Morning recap", cron: "0 9 * * *", destination: "main" });
  });

  test("create into a Slack channel: known channels are offered, a pasted link works too", async () => {
    list = base({ items: [], slack_ready: true, slack_mode: "custom", slack_channels: [{ id: "C0123ABCD" }] });
    render(AgentScheduled, { props: { base: "/b", agent, onClose: vi.fn() } });
    await fireEvent.click(await screen.findByRole("button", { name: "New schedule" }));
    await fireEvent.input(screen.getByLabelText("Message to the agent"), { target: { value: "Daily digest" } });
    await fireEvent.click(screen.getByLabelText("Slack channel", { selector: "input[type=radio]" }));
    const field = screen.getByLabelText("Slack channel", { selector: "input[list]" }) as HTMLInputElement;
    expect(field.value).toBe("C0123ABCD");
    await fireEvent.input(field, { target: { value: "#general" } });
    expect(screen.getByTestId("form-problem").textContent).toContain("Slack channel");
    await fireEvent.input(field, { target: { value: "https://x.slack.com/archives/C0999ZZZZ" } });
    await fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() => expect(createAgentSchedule).toHaveBeenCalled());
    expect(createAgentSchedule.mock.calls[0][2]).toEqual({ message: "Daily digest", cron: "0 9 * * *", destination: "slack", slack_channel: "C0999ZZZZ" });
  });

  test("a Slack row names its channel; editing keeps the Slack destination", async () => {
    list = base({ items: [{ ...daily, destination: "slack", slack_channel: "C0123ABCD", session_id: "slackagent-a1-1.2" }], slack_ready: true, slack_channels: [] });
    render(AgentScheduled, { props: { base: "/b", agent, onClose: vi.fn() } });
    expect((await screen.findByTestId("scheduled-row")).textContent).toContain("Slack · #C0123ABCD");
    await fireEvent.click(screen.getByRole("button", { name: "More" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Edit" }));
    expect((screen.getByLabelText("Slack channel", { selector: "input[list]" }) as HTMLInputElement).value).toBe("C0123ABCD");
    await fireEvent.input(screen.getByLabelText("Message to the agent"), { target: { value: "Evening recap" } });
    await waitFor(() => expect(updateAgentSchedule).toHaveBeenCalled(), { timeout: 2000 });
    expect(updateAgentSchedule.mock.calls[0][3]).toEqual({ message: "Evening recap", cron: "0 9 * * *", destination: "slack", slack_channel: "C0123ABCD" });
  });

  test("History lists the last runs and opens the chat of one", async () => {
    list = base();
    const onOpenSession = vi.fn();
    render(AgentScheduled, { props: { base: "/b", agent, onClose: vi.fn(), onOpenSession } });
    await screen.findByTestId("scheduled-row");
    await fireEvent.click(screen.getByRole("button", { name: "More" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "History" }));
    await waitFor(() => expect(screen.getAllByTestId("run-row")).toHaveLength(2));
    expect(getAgentScheduleRuns).toHaveBeenCalledWith("/b", "a1", "s1");
    const [failed, ok] = screen.getAllByTestId("run-row");
    expect(failed.textContent).toContain("Failed");
    expect(failed.textContent).toContain("provider timed out");
    expect(ok.textContent).toContain("OK");
    await fireEvent.click(ok.querySelector("button")!);
    expect(onOpenSession).toHaveBeenCalledWith("main1");
  });
});
