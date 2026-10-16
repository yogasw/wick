import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

const createAgent = vi.fn((_b: string, body: unknown) => Promise.resolve({ id: "a1", ...(body as object) }));
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  createAgent: (b: string, body: unknown) => createAgent(b, body),
  listAgentConnectors: () =>
    Promise.resolve([{ id: "c1", key: "slack", label: "Slack", description: "", accounts: null, ops: [{ key: "send", name: "Send", destructive: true }] }]),
  listAgents: () => Promise.resolve({ agents: [], captain_id: "" }),
  getProjectPersona: () => Promise.resolve({ name: "Ops", description: "", system_prompt: "" }),
  runApi: <T,>(p: Promise<T>) => p,
}));
vi.mock("../../api/options.js", () => ({
  getProjectOptions: () => Promise.resolve([{ id: "p1", name: "Ops" }]),
  getProviderOptions: () => Promise.resolve([{ type: "claude", name: "claude", models: [] }]),
  getProviderOptionModels: () => Promise.resolve([{ id: "sonnet", label: "Sonnet", default: true }]),
}));
vi.stubGlobal("fetch", vi.fn(() => Promise.resolve({ ok: false, json: () => Promise.resolve({}) })));

import AgentWizard from "../AgentWizard.svelte";

const props = (extra: Record<string, unknown> = {}) => ({
  base: "/tools/agents", taken: [] as string[], onClose: vi.fn(), onCreated: vi.fn(), ...extra,
});
const lastBody = () => createAgent.mock.calls.at(-1)?.[1] as Record<string, unknown> | undefined;
const next = () => fireEvent.click(screen.getByRole("button", { name: "Next →" }));

async function toAccess(name = "Research") {
  await fireEvent.input(screen.getByLabelText("Name"), { target: { value: name } });
  await next();
}
async function pickModelAndCreate() {
  await fireEvent.click(screen.getByRole("button", { name: "Provider & model" }));
  await fireEvent.click(screen.getByRole("button", { name: /^Claude/ }));
  await fireEvent.click(await screen.findByText("Sonnet"));
  await fireEvent.click(screen.getByRole("button", { name: "Create agent" }));
}

describe("AgentWizard › the Captain runs as its owner", () => {
  beforeEach(() => createAgent.mockClear());

  test("Same as me, the summary says it runs as you, and run_as owner is sent", async () => {
    render(AgentWizard, { props: props({ firstAgent: true }) });
    await toAccess("Lead");
    const summary = screen.getByTestId("aw-access-summary").textContent ?? "";
    expect(summary).toContain("Same as you");
    expect(summary).toContain("runs as you");
    expect(summary).toContain("a Team lead works for you personally");
    await next();
    // Same as me never warns about connectors.
    expect(screen.queryByTestId("aw-no-connectors")).toBeNull();
    await pickModelAndCreate();
    await waitFor(() => expect(lastBody()).toMatchObject({ run_as: "owner", access_mode: "owner" }));
  });

  test("any other agent still runs as the caller", async () => {
    render(AgentWizard, { props: props() });
    await toAccess();
    expect(screen.getByTestId("aw-access-summary").textContent).not.toContain("runs as you");
    await next();
    await fireEvent.click(screen.getByRole("button", { name: "Continue without connectors →" }));
    await pickModelAndCreate();
    await waitFor(() => expect(lastBody()?.run_as).toBe("caller"));
  });
});

describe("AgentWizard › no connectors warns once", () => {
  beforeEach(() => createAgent.mockClear());

  test("the first Next with 0 connectors shows the alert and stays on Access", async () => {
    render(AgentWizard, { props: props() });
    await toAccess();
    await next();
    const alert = screen.getByTestId("aw-no-connectors");
    expect(alert.getAttribute("role")).toBe("alert");
    expect(alert.textContent).toContain("⚠ No connectors.");
    expect(alert.textContent).toContain("@research can't use any connector (Slack, Loki, Notion…) — only Platform tools.");
    expect(screen.queryByTestId("aw-model-step")).toBeNull();
    expect(screen.queryByRole("button", { name: "Next →" })).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Continue without connectors →" }));
    expect(screen.getByTestId("aw-model-step")).toBeDefined();
  });

  test("+ Add connectors opens the picker; adding one clears the alert", async () => {
    render(AgentWizard, { props: props() });
    await toAccess();
    await screen.findByTestId("access-empty");
    await next();
    const footer = screen.getAllByRole("button", { name: "+ Add connectors" }).at(-1)!;
    await fireEvent.click(footer);
    await fireEvent.click(screen.getByLabelText("Pick Slack"));
    await fireEvent.click(screen.getByRole("button", { name: "Add 1 as Read" }));
    expect(screen.queryByTestId("aw-no-connectors")).toBeNull();
    await next();
    expect(screen.getByTestId("aw-model-step")).toBeDefined();
  });

  test("not shown when converting a project", async () => {
    render(AgentWizard, { props: props({ convertProject: "p1" }) });
    await waitFor(() => expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("Ops"));
    await next();
    await next();
    expect(screen.queryByTestId("aw-no-connectors")).toBeNull();
    expect(screen.getByTestId("aw-model-step")).toBeDefined();
  });
});
