import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

// The server echoes avatar keys in its own order, like the Go struct does.
const echo = (body: Record<string, unknown>) => {
  const out: Record<string, unknown> = { ...agent, ...body };
  const a = body.avatar as Record<string, unknown> | undefined;
  if (a) out.avatar = Object.fromEntries(Object.keys(a).sort().reverse().map((k) => [k, a[k]]));
  return out;
};
let delay = 0;
let fail = false;
const updateAgent = vi.fn(async (_b: string, _id: string, body: unknown) => {
  await new Promise((r) => setTimeout(r, delay));
  if (fail) throw new Error("boom");
  return echo(body as Record<string, unknown>);
});
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  updateAgent: (b: string, id: string, body: unknown) => updateAgent(b, id, body),
  listAgentConnectors: () => Promise.resolve([]),
  getProjectPersona: () => Promise.resolve(null),
  runApi: <T,>(p: Promise<T>) => p,
}));
vi.mock("../../api/options.js", () => ({
  getProviderOptions: () => Promise.resolve([]),
  getProjectOptions: () => Promise.resolve([]),
}));

import AgentSettings from "../AgentSettings.svelte";
import type { AgentItem } from "../../api/team.js";

const agent = {
  id: "a1", handle: "worker", is_captain: false, project_id: "p1", name: "Worker", icon: "",
  description: "", system_prompt: "", provider: "", model: "", preset: "",
  features: {}, avatar: { kind: "blob", shape: "cloud", color: "#ef4444", expression: "sad" }, allowed_connectors: [],
  include_new_connectors: false, disabled: false, main_session_id: "", last_active: null,
  last_preview: "", status: "idle",
} as unknown as AgentItem;

const props = () => ({ base: "/tools/agents", agent, agents: [agent], tab: "avatar" as const, onTab: vi.fn(), onClose: vi.fn(), onSaved: vi.fn(), onDeleted: vi.fn() });
const footer = () => screen.getByTestId("autosave-status").textContent ?? "";

describe("AgentSettings autosave", () => {
  beforeEach(() => {
    updateAgent.mockClear();
    delay = 0;
    fail = false;
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
  });

  test("a reply with avatar keys in another order settles on Saved", async () => {
    render(AgentSettings, { props: props() });
    await fireEvent.click(screen.getByRole("button", { name: "happy" }));
    await waitFor(() => expect(footer()).toContain("Saved ✓"));
    expect(updateAgent).toHaveBeenCalledTimes(1);
    await new Promise((r) => setTimeout(r, 600));
    expect(updateAgent).toHaveBeenCalledTimes(1);
    expect(footer()).toContain("Saved ✓");
  });

  test("five quick clicks send at most two PATCHes, the last one with the last state", async () => {
    delay = 200;
    render(AgentSettings, { props: props() });
    for (const name of ["happy", "sad", "happy", "sad", "happy"]) await fireEvent.click(screen.getByRole("button", { name }));
    await fireEvent.click(screen.getByRole("button", { name: "cloud" }));
    await waitFor(() => expect(footer()).toContain("Saved ✓"), { timeout: 3000 });
    expect(updateAgent.mock.calls.length).toBeLessThanOrEqual(2);
    expect((updateAgent.mock.calls.at(-1)?.[2] as { avatar: { expression: string } }).avatar.expression).toBe("happy");
  });

  test("a refused save offers Retry", async () => {
    fail = true;
    render(AgentSettings, { props: props() });
    await fireEvent.click(screen.getByRole("button", { name: "happy" }));
    await waitFor(() => expect(footer()).toContain("Couldn't save"));
    expect(screen.getByRole("button", { name: "Retry" })).toBeDefined();
    fail = false;
    await fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(footer()).toContain("Saved ✓"));
  });
});

describe("AgentSettings tagline", () => {
  test("takes up to 50 characters", async () => {
    render(AgentSettings, { props: { ...props(), tab: "persona" as never } });
    const input = document.getElementById("as-tagline") as HTMLInputElement;
    expect(input.maxLength).toBe(50);
  });
});
