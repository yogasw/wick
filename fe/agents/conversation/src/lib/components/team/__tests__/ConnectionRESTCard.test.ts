import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { AgentRESTStatus } from "../../../restConnection.js";

const off: AgentRESTStatus = {
  enabled: false,
  base_url: "https://wick.example/integrations/rest/api/v1/openai",
  model: "agent:rekap",
  tokens_url: "https://wick.example/profile/tokens",
};
let current: AgentRESTStatus = off;
let failSave = false;
const update = vi.fn((_b: string, _id: string, enabled: boolean) =>
  failSave ? Promise.reject(new Error("boom")) : Promise.resolve({ ...current, enabled }));
const test_ = vi.fn(() => Promise.resolve({ ok: true, model: "agent:rekap", detail: "Calls with this model id reach the agent." }));
vi.mock("../../../restConnection.js", async (orig) => ({
  ...(await orig<typeof import("../../../restConnection.js")>()),
  getAgentREST: () => Promise.resolve(current),
  updateAgentREST: (b: string, id: string, enabled: boolean) => update(b, id, enabled),
  testAgentREST: () => test_(),
}));
vi.mock("../../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../../api/team.js")>()),
  runApi: <T,>(p: Promise<T>) => p,
}));

import ConnectionRESTCard from "../ConnectionRESTCard.svelte";
import { restCurlExample, restSDKExample } from "../../../restConnection.js";
import type { AgentItem } from "../../../api/team.js";

const agent = { id: "a1", handle: "rekap" } as unknown as AgentItem;

describe("ConnectionRESTCard", () => {
  beforeEach(() => { update.mockClear(); test_.mockClear(); current = off; failSave = false; });

  test("shows base URL, model id and the tokens page link", async () => {
    render(ConnectionRESTCard, { base: "/tools/agents", agent });
    expect((await screen.findByTestId("rest-base")).textContent).toBe(off.base_url);
    expect(screen.getByTestId("rest-model").textContent).toBe("agent:rekap");
    expect(screen.getByTestId("rest-tokens-link").getAttribute("href")).toBe(off.tokens_url);
    expect(screen.getByText("Off")).toBeTruthy();
  });

  test("toggle autosaves and flips the badge", async () => {
    render(ConnectionRESTCard, { base: "/tools/agents", agent });
    await screen.findByTestId("rest-base");
    await fireEvent.click(screen.getByRole("switch", { name: "Enable REST" }));
    await waitFor(() => expect(update).toHaveBeenCalledWith("/tools/agents", "a1", true));
    expect(await screen.findByText("Enabled")).toBeTruthy();
    expect(screen.getByText("Saved")).toBeTruthy();
  });

  test("a failed save rolls the toggle back", async () => {
    failSave = true;
    render(ConnectionRESTCard, { base: "/tools/agents", agent });
    await screen.findByTestId("rest-base");
    await fireEvent.click(screen.getByRole("switch", { name: "Enable REST" }));
    expect(await screen.findByText("Couldn't save")).toBeTruthy();
    expect(screen.getByText("Off")).toBeTruthy();
  });

  test("snippets use the placeholder, never a token", async () => {
    current = { ...off, enabled: true };
    render(ConnectionRESTCard, { base: "/tools/agents", agent });
    const curl = (await screen.findByTestId("rest-curl")).textContent ?? "";
    expect(curl).toContain("Bearer $WICK_TOKEN");
    expect(curl).toContain(`${off.base_url}/chat/completions`);
    expect(curl).toContain('"model":"agent:rekap"');
    const sdk = screen.getByTestId("rest-sdk").textContent ?? "";
    expect(sdk).toContain('os.environ["WICK_TOKEN"]');
    expect(sdk).toContain('model="agent:rekap"');
    expect(curl + sdk).not.toMatch(/wick_pat_/);
  });

  test("Test reports the result", async () => {
    current = { ...off, enabled: true };
    render(ConnectionRESTCard, { base: "/tools/agents", agent });
    await screen.findByTestId("rest-base");
    await fireEvent.click(screen.getByText("Test"));
    const line = await screen.findByTestId("rest-test");
    expect(line.textContent).toContain("OK");
    expect(test_).toHaveBeenCalled();
  });
});

describe("restConnection snippets", () => {
  test("curl targets chat/completions with the placeholder", () => {
    const s = restCurlExample("https://w/x", "agent:a");
    expect(s.startsWith("curl -X POST 'https://w/x/chat/completions'")).toBe(true);
    expect(s).toContain("$WICK_TOKEN");
  });
  test("SDK snippet points the client at the base URL", () => {
    expect(restSDKExample("https://w/x", "agent:a")).toContain('OpenAI(base_url="https://w/x", api_key=os.environ["WICK_TOKEN"])');
  });
});
