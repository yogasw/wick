import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

const createAgent = vi.fn((_b: string, body: unknown) => Promise.resolve({ id: "a1", ...(body as object) }));
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  createAgent: (b: string, body: unknown) => createAgent(b, body),
  listAgentConnectors: () => Promise.resolve([]),
  // The Mentions step lists the owner's other agents.
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
  base: "/tools/agents", taken: [], onClose: vi.fn(), onCreated: vi.fn(), ...extra,
});
const lastBody = () => createAgent.mock.calls.at(-1)?.[1] as Record<string, unknown> | undefined;

/** Step 3: open the picker, drill into Claude, pick a model. */
async function pickModel() {
  await fireEvent.click(screen.getByRole("button", { name: "Provider & model" }));
  await fireEvent.click(screen.getByRole("button", { name: /^Claude/ }));
  await fireEvent.click(await screen.findByText("Sonnet"));
}

async function toModelStep() {
  await fireEvent.click(screen.getByRole("button", { name: "Next →" }));
  await fireEvent.click(screen.getByRole("button", { name: "Next →" }));
}

async function fillAndCreate() {
  await fireEvent.input(screen.getByLabelText("Name"), { target: { value: "Log Hunter" } });
  await toModelStep();
  await pickModel();
  await fireEvent.click(screen.getByRole("button", { name: "Create agent" }));
}

describe("AgentWizard › Keep the project in Agents → Projects", () => {
  beforeEach(() => createAgent.mockClear());

  test("is ticked by default and sent on create", async () => {
    render(AgentWizard, { props: props() });
    const box = screen.getByTestId("aw-show-in-projects").querySelector("input") as HTMLInputElement;
    expect(box.checked).toBe(true);
    await fillAndCreate();
    await waitFor(() => expect(lastBody()?.show_in_projects).toBe(true));
  });

  test("unticked sends false", async () => {
    render(AgentWizard, { props: props() });
    await fireEvent.click(screen.getByTestId("aw-show-in-projects").querySelector("input")!);
    await fillAndCreate();
    await waitFor(() => expect(lastBody()?.show_in_projects).toBe(false));
  });

  test("converting a project shows it ticked and sends it with convert", async () => {
    render(AgentWizard, { props: props({ convertProject: "p1" }) });
    const box = screen.getByTestId("aw-show-in-projects").querySelector("input") as HTMLInputElement;
    expect(box.checked).toBe(true);
    await waitFor(() => expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("Ops"));
    await toModelStep();
    await pickModel();
    await fireEvent.click(screen.getByRole("button", { name: "Create agent" }));
    await waitFor(() => expect(lastBody()).toMatchObject({ convert: true, project_id: "p1", show_in_projects: true }));
  });
});

describe("AgentWizard › step 3 Model", () => {
  beforeEach(() => createAgent.mockClear());

  test("three steps; the picker starts empty and Create stays off until a model is picked", async () => {
    render(AgentWizard, { props: props() });
    for (const s of ["Persona", "Access & mentions", "Model"]) expect(screen.getByText(s)).toBeDefined();
    await fireEvent.input(screen.getByLabelText("Name"), { target: { value: "Log Hunter" } });
    await fireEvent.click(screen.getByRole("button", { name: "Next →" }));
    // Step 2 no longer creates.
    expect(screen.queryByRole("button", { name: "Create agent" })).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Next →" }));
    expect(screen.getByTestId("aw-model-step")).toBeDefined();
    expect(screen.getByRole("button", { name: "Provider & model" }).textContent).toContain("Choose provider & model…");
    const create = screen.getByRole("button", { name: "Create agent" }) as HTMLButtonElement;
    expect(create.disabled).toBe(true);
    await fireEvent.click(create);
    expect(createAgent).not.toHaveBeenCalled();
    await pickModel();
    expect((screen.getByRole("button", { name: "Create agent" }) as HTMLButtonElement).disabled).toBe(false);
    await fireEvent.click(screen.getByRole("button", { name: "Create agent" }));
    await waitFor(() => expect(lastBody()).toMatchObject({ provider: "claude/claude", model: "sonnet" }));
  });

  test("Back walks one step at a time", async () => {
    render(AgentWizard, { props: props() });
    await fireEvent.input(screen.getByLabelText("Name"), { target: { value: "Log Hunter" } });
    await toModelStep();
    await fireEvent.click(screen.getByRole("button", { name: "← Back" }));
    expect(screen.queryByTestId("aw-model-step")).toBeNull();
    expect(screen.getByRole("button", { name: "Next →" })).toBeDefined();
  });
});
