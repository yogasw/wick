import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { SlackTestResult } from "../../../api/team.js";

const connectors = vi.fn(() => Promise.resolve([
  { id: "slk1", key: "slack", label: "Acme Slack", description: "", accounts: [], ops: [] },
  { id: "gh1", key: "github", label: "GitHub", description: "", accounts: [], ops: [] },
]));
let accounts: { id: string; display_name: string }[] = [];
const identities = vi.fn((_b: string, _c: string) => Promise.resolve({ bot: true, accounts }));
const testSlack = vi.fn((_b: string, _body: unknown): Promise<SlackTestResult> => Promise.resolve({ ok: true, state: "replied", latency_ms: 640, reply: "pong" }));
const create = vi.fn((_b: string, body: Record<string, unknown>) => Promise.resolve({ id: "s1", handle: "ops", kind: "slack-remote", ...body }));
vi.mock("../../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../../api/team.js")>()),
  listAgentConnectors: () => connectors(),
  getSlackIdentities: (b: string, c: string) => identities(b, c),
  testSlackRemote: (b: string, body: unknown) => testSlack(b, body),
  createSlackRemote: (b: string, body: Record<string, unknown>) => create(b, body),
  runApi: <T,>(p: Promise<T>) => p,
}));

import SlackRemoteWizard from "../SlackRemoteWizard.svelte";

const props = () => ({ base: "/tools/agents", taken: ["captain"], onClose: vi.fn(), onCreated: vi.fn(), onType: vi.fn() });
const nextBtn = () => screen.getByRole("button", { name: "Next →" }) as HTMLButtonElement;
const tick = () => new Promise((r) => setTimeout(r, 0));

async function toTarget() {
  await waitFor(() => expect(screen.getByLabelText("Slack workspace")).toBeTruthy());
  // The only Slack connector is picked for the user; GitHub is not offered.
  expect((screen.getByLabelText("Slack workspace") as HTMLSelectElement).value).toBe("slk1");
  expect(screen.queryByRole("option", { name: "GitHub" })).toBeNull();
  await fireEvent.click(nextBtn());
}

describe("SlackRemoteWizard", () => {
  beforeEach(() => { accounts = []; connectors.mockClear(); identities.mockClear(); testSlack.mockClear(); create.mockClear(); });

  test("channel target → bot → listen → Test, warning, Create sends a clean config", async () => {
    const p = props();
    render(SlackRemoteWizard, p);
    await toTarget();
    await fireEvent.click(screen.getByRole("button", { name: "Channel" }));
    expect(nextBtn().disabled).toBe(true);
    await fireEvent.input(screen.getByLabelText("Channel ID"), { target: { value: "C0OPS" } });
    await fireEvent.input(screen.getByLabelText("Mention (optional)"), { target: { value: "U0BOT" } });
    await fireEvent.input(screen.getByLabelText("Display name"), { target: { value: "#ops" } });
    await fireEvent.click(nextBtn());
    // No own Slack account: As me is offered disabled.
    await waitFor(() => expect(identities).toHaveBeenCalledWith("/tools/agents", "slk1"));
    await waitFor(() => expect(screen.getByText(/Connect your own Slack account/)).toBeTruthy());
    const radios = screen.getByTestId("sw-identity").querySelectorAll("input[type=radio]");
    expect((radios[1] as HTMLInputElement).disabled).toBe(true);
    await fireEvent.click(nextBtn());
    await fireEvent.click(screen.getByLabelText(/Anyone in the thread/));
    await fireEvent.click(screen.getByTestId("sw-marker"));
    await fireEvent.input(screen.getByLabelText("Idle (seconds)"), { target: { value: "30" } });
    await fireEvent.click(nextBtn());

    expect(screen.getByTestId("sw-warning").textContent).toContain("will be posted to #ops in Acme Slack.");
    await fireEvent.click(screen.getByTestId("sw-test"));
    await waitFor(() => expect(screen.getByTestId("sw-test-result").textContent).toContain("Replied in 640 ms"));
    const want = {
      connector_id: "slk1", identity: "bot", target: "channel", channel: "C0OPS", mention_id: "U0BOT", target_name: "#ops",
      listen: "anyone", marker: false, idle_sec: 30, max_sec: 0,
    };
    expect(testSlack).toHaveBeenCalledWith("/tools/agents", want);
    await fireEvent.click(screen.getByRole("button", { name: "Create agent" }));
    await waitFor(() => expect(p.onCreated).toHaveBeenCalled());
    expect(create).toHaveBeenCalledWith("/tools/agents", want);
  });

  test("a thread link fills channel and timestamp; As me needs the user's own account", async () => {
    accounts = [{ id: "acc-me", display_name: "Me (Acme)" }];
    render(SlackRemoteWizard, props());
    await toTarget();
    await fireEvent.click(screen.getByRole("button", { name: "Thread" }));
    await fireEvent.input(screen.getByLabelText("Thread link"), { target: { value: "https://acme.slack.com/archives/C0OPS/p1700000000123456" } });
    expect((screen.getByLabelText("Channel ID") as HTMLInputElement).value).toBe("C0OPS");
    expect((screen.getByLabelText("Thread timestamp") as HTMLInputElement).value).toBe("1700000000.123456");
    await fireEvent.click(nextBtn());
    await waitFor(() => expect(screen.getByText(/appear as you/)).toBeTruthy());
    await fireEvent.click(screen.getByLabelText(/As me/));
    await fireEvent.click(nextBtn());
    await fireEvent.click(nextBtn());
    await fireEvent.input(screen.getByLabelText("Handle"), { target: { value: "captain" } });
    expect(screen.getByText("@captain is already taken by another agent.")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Create agent" }) as HTMLButtonElement).disabled).toBe(true);
    await fireEvent.input(screen.getByLabelText("Handle"), { target: { value: "ops-thread" } });
    await fireEvent.click(screen.getByRole("button", { name: "Create agent" }));
    await waitFor(() => expect(create).toHaveBeenCalled());
    const body = create.mock.calls[0][1];
    expect(body).toMatchObject({ identity: "user", account_id: "acc-me", target: "thread", channel: "C0OPS", thread_ts: "1700000000.123456", handle: "ops-thread" });
    expect(screen.queryByTestId("sw-warning")?.textContent).toContain("a thread in #C0OPS");
    await tick();
  });

  test("a failed ping says so; switching source back to A2A asks the host", async () => {
    testSlack.mockImplementationOnce(() => Promise.resolve({ ok: false, state: "send_failed", latency_ms: 0, error: "not_in_channel" }));
    const p = props();
    render(SlackRemoteWizard, p);
    await toTarget();
    await fireEvent.input(screen.getByLabelText("User or bot ID"), { target: { value: "U0HELP" } });
    for (let i = 0; i < 3; i++) await fireEvent.click(nextBtn());
    expect(screen.getByTestId("sw-warning").textContent).toContain("posted to @U0HELP in Acme Slack.");
    await fireEvent.click(screen.getByTestId("sw-test"));
    await waitFor(() => expect(screen.getByTestId("sw-test-result").textContent).toBe("Ping failed: not_in_channel"));
    await fireEvent.click(screen.getByTestId("remote-source-a2a"));
    expect(p.onType).toHaveBeenCalledWith("remote");
  });
});
