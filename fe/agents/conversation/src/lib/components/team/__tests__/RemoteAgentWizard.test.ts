import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { RemoteResolved } from "../../../api/team.js";

const resolved: RemoteResolved = {
  card_url: "https://research.example.com/.well-known/agent-card.json",
  host: "research.example.com",
  suggested_handle: "research-bot-2",
  card: {
    name: "Research Bot", description: "Finds papers", version: "1.4.0", streaming: true,
    skills: [{ id: "s1", name: "Search papers" }, { id: "s2", name: "Summarise" }],
    endpoint: "https://research.example.com/a2a", transport: "JSONRPC",
  },
};
const resolve = vi.fn((_b: string, _url: string, _auth?: unknown) => Promise.resolve(resolved));
const testRemote = vi.fn((_b: string, _body: unknown) =>
  Promise.resolve({ ok: true, state: "TASK_STATE_COMPLETED", card_ms: 4, latency_ms: 87, reply: "pong", error: "" }));
const create = vi.fn((_b: string, body: Record<string, unknown>) => Promise.resolve({ id: "r1", handle: body.handle ?? "research-bot-2", kind: "a2a-remote" }));
vi.mock("../../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../../api/team.js")>()),
  resolveRemoteCard: (b: string, url: string, auth?: unknown) => resolve(b, url, auth),
  testRemoteAgent: (b: string, body: unknown) => testRemote(b, body),
  createRemoteAgent: (b: string, body: Record<string, unknown>) => create(b, body),
  runApi: <T,>(p: Promise<T>) => p,
}));

import RemoteAgentWizard from "../RemoteAgentWizard.svelte";

const props = () => ({ base: "/tools/agents", taken: ["captain", "research-bot"], onClose: vi.fn(), onCreated: vi.fn(), onType: vi.fn() });
const next = () => fireEvent.click(screen.getByRole("button", { name: "Next →" }));

async function fetchCard(url = "https://research.example.com") {
  await fireEvent.input(screen.getByLabelText("Agent card URL"), { target: { value: url } });
  await fireEvent.click(screen.getByTestId("rw-fetch"));
  await screen.findByTestId("rw-card");
}

describe("RemoteAgentWizard", () => {
  beforeEach(() => { resolve.mockClear(); testRemote.mockClear(); create.mockClear(); });

  test("Fetch card previews name, host, version, streaming and skills; the handle comes from the card", async () => {
    render(RemoteAgentWizard, props());
    expect((screen.getByRole("button", { name: "Next →" }) as HTMLButtonElement).disabled).toBe(true);
    await fetchCard();
    const card = screen.getByTestId("rw-card").textContent ?? "";
    for (const s of ["Research Bot", "research.example.com", "v1.4.0", "streaming", "Search papers", "Summarise"]) expect(card).toContain(s);
    expect((screen.getByLabelText("Handle") as HTMLInputElement).value).toBe("research-bot-2");
    expect(resolve).toHaveBeenCalledWith("/tools/agents", "https://research.example.com", { type: "none" });
  });

  test("a bearer token goes out with fetch and test, and Test shows the latency", async () => {
    render(RemoteAgentWizard, props());
    await fireEvent.input(screen.getByLabelText("Agent card URL"), { target: { value: "https://research.example.com" } });
    await fireEvent.click(screen.getByRole("button", { name: "Bearer token" }));
    const secret = screen.getByLabelText("Token") as HTMLInputElement;
    expect(secret.type).toBe("password");
    expect((screen.getByTestId("rw-fetch") as HTMLButtonElement).disabled).toBe(true);
    await fireEvent.input(secret, { target: { value: "tok-123" } });
    await fireEvent.click(screen.getByTestId("rw-test"));
    await waitFor(() => expect(screen.getByTestId("rw-test-result").textContent).toContain("Replied in 87 ms"));
    expect(testRemote.mock.calls[0][1]).toEqual({ url: "https://research.example.com", auth: { type: "bearer", secret: "tok-123" } });
  });

  test("editing the URL after a fetch drops the preview", async () => {
    render(RemoteAgentWizard, props());
    await fetchCard();
    await fireEvent.input(screen.getByLabelText("Agent card URL"), { target: { value: "https://other.example.com" } });
    expect(screen.queryByTestId("rw-card")).toBeNull();
    expect((screen.getByRole("button", { name: "Next →" }) as HTMLButtonElement).disabled).toBe(true);
  });

  test("Usage warns that messages leave for the host; Connect only skips; create sends url, usage and no secret", async () => {
    const p = props();
    render(RemoteAgentWizard, p);
    await fetchCard();
    await next();
    expect(screen.getByTestId("rw-warning").textContent).toBe(
      "Messages you send, including other agents' output that mentions this agent, leave wick for research.example.com.");
    expect(screen.queryByText("Who may use it")).toBeNull();
    await next();
    expect(screen.getByTestId("rw-connect").textContent).toContain("coming soon");
    await fireEvent.click(screen.getByRole("button", { name: "Create agent" }));
    await waitFor(() => expect(p.onCreated).toHaveBeenCalled());
    expect(create.mock.calls[0][1]).toEqual({ url: resolved.card_url });
  });

  test("a typed handle is sent; a 409 lands on the handle field", async () => {
    create.mockImplementationOnce(() => Promise.reject(new Error("handle @rb is already taken")));
    render(RemoteAgentWizard, props());
    await fetchCard();
    await fireEvent.input(screen.getByLabelText("Handle"), { target: { value: "rb" } });
    await next();
    await next();
    await fireEvent.click(screen.getByRole("button", { name: "Create agent" }));
    await screen.findByText("handle @rb is already taken");
    expect(create.mock.calls[0][1]).toMatchObject({ handle: "rb" });
    expect(screen.getByLabelText("Handle")).toBeDefined();
  });

  test("Wick agent switches back", async () => {
    const p = props();
    render(RemoteAgentWizard, p);
    await fireEvent.click(screen.getByRole("button", { name: "Wick agent" }));
    expect(p.onType).toHaveBeenCalledWith("local");
  });

  test("Remote agent shows the source picker; Slack opens its wizard", async () => {
    const p = props();
    render(RemoteAgentWizard, p);
    expect(screen.getByRole("button", { name: "Remote agent" }).getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByTestId("remote-source-a2a").getAttribute("aria-checked")).toBe("true");
    await fireEvent.click(screen.getByTestId("remote-source-slack"));
    expect(p.onType).toHaveBeenCalledWith("slack");
  });
});
