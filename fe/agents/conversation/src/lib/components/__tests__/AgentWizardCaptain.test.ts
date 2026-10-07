import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";

vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  createAgent: () => Promise.resolve({ id: "a1" }),
  listAgentConnectors: () => Promise.resolve([]),
  getProjectPersona: () => Promise.resolve({ name: "", description: "", system_prompt: "" }),
  runApi: <T,>(p: Promise<T>) => p,
}));
vi.mock("../../api/options.js", () => ({
  getProjectOptions: () => Promise.resolve([]),
  getProviderOptions: () => Promise.resolve([]),
  getProviderOptionModels: () => Promise.resolve([]),
}));
vi.stubGlobal("fetch", vi.fn(() => Promise.resolve({ ok: false, json: () => Promise.resolve({}) })));

import AgentWizard from "../AgentWizard.svelte";

const props = (extra: Record<string, unknown> = {}) => ({
  base: "/tools/agents", taken: [] as string[], onClose: vi.fn(), onCreated: vi.fn(), ...extra,
});

describe("AgentWizard › first agent becomes the Captain", () => {
  test("the Persona step says so and explains the Captain", () => {
    render(AgentWizard, { props: props({ firstAgent: true }) });
    const note = screen.getByTestId("aw-captain-note");
    expect(note.textContent).toContain("This agent will be your Captain.");
    const what = screen.getByTestId("aw-captain-what");
    expect(what.querySelector("summary")?.textContent).toBe("What is a Captain?");
    expect(what.textContent).toContain("One per Team, always a Wick agent, never shared.");
    expect(what.textContent).toContain("Make Captain");
  });

  test("Use Captain starter persona fills the form, which stays editable", async () => {
    render(AgentWizard, { props: props({ firstAgent: true, taken: ["captain"] }) });
    await fireEvent.click(screen.getByTestId("aw-captain-starter"));
    expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("Captain");
    // A taken handle gets the next free one.
    expect((screen.getByLabelText("Handle") as HTMLInputElement).value).not.toBe("captain");
    expect((screen.getByLabelText("System prompt (persona)") as HTMLTextAreaElement).value).toContain("coordinates the Team");
    await fireEvent.input(screen.getByLabelText("Name"), { target: { value: "Ops Lead" } });
    expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("Ops Lead");
  });

  test("no note once the Team has a Captain", () => {
    render(AgentWizard, { props: props() });
    expect(screen.queryByTestId("aw-captain-note")).toBeNull();
    expect(screen.queryByTestId("aw-captain-starter")).toBeNull();
  });
});
