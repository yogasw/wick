import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { AgentA2AStatus } from "../../../a2aConnection.js";

const off: AgentA2AStatus = {
  enabled: false, public_card: false, allowed_callers: [], key_set: false,
  endpoint_url: "https://wick.example/integrations/a2a/a1", card_url: "https://wick.example/integrations/a2a/a1/.well-known/agent-card.json",
};
const on: AgentA2AStatus = { ...off, enabled: true, key_set: true, key_hint: "Wx9z" };
let current: AgentA2AStatus = off;
const update = vi.fn((_b: string, _id: string, body: Partial<AgentA2AStatus>) =>
  Promise.resolve(body.enabled ? { ...on, api_key: "wa2a_SECRETKEYWx9z" } : { ...current, ...body }));
const rotate = vi.fn(() => Promise.resolve({ ...on, api_key: "wa2a_ROTATEDabcd", key_hint: "abcd" }));
vi.mock("../../../a2aConnection.js", async (orig) => ({
  ...(await orig<typeof import("../../../a2aConnection.js")>()),
  getAgentA2A: () => Promise.resolve(current),
  updateAgentA2A: (b: string, id: string, body: Partial<AgentA2AStatus>) => update(b, id, body),
  rotateAgentA2A: () => rotate(),
  revokeAgentA2A: () => Promise.resolve({ ...on, key_set: false, key_hint: undefined }),
  testAgentA2A: () => Promise.resolve({ ok: true, status: "TASK_STATE_COMPLETED", card_ms: 3, send_ms: 12, reply: "pong" }),
}));
vi.mock("../../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../../api/team.js")>()),
  runApi: <T,>(p: Promise<T>) => p,
}));

import ConnectionA2ACard from "../ConnectionA2ACard.svelte";
import { curlExample, maskedKey, probeLine } from "../../../a2aConnection.js";
import type { AgentItem } from "../../../api/team.js";

const agent = { id: "a1", handle: "rekap" } as unknown as AgentItem;

describe("ConnectionA2ACard", () => {
  beforeEach(() => { update.mockClear(); rotate.mockClear(); current = off; });

  test("enable autosaves and shows the new key once", async () => {
    render(ConnectionA2ACard, { base: "/tools/agents", agent });
    await screen.findByTestId("a2a-endpoint-url");
    expect(screen.getByTestId("a2a-masked-key").textContent).toBe("No key");
    await fireEvent.click(screen.getByRole("switch", { name: "Enable A2A" }));
    await waitFor(() => expect(update).toHaveBeenCalledWith("/tools/agents", "a1", { enabled: true }));
    const fresh = await screen.findByTestId("a2a-fresh-key");
    expect(fresh.textContent).toContain("wa2a_SECRETKEYWx9z");
    const curl = screen.getByTestId("a2a-curl").textContent ?? "";
    expect(curl).toContain("Bearer $A2A_KEY");
    expect(curl).not.toContain("wa2a_SECRETKEYWx9z");
  });

  test("stored key stays masked; rotate reveals the new one", async () => {
    current = on;
    render(ConnectionA2ACard, { base: "/tools/agents", agent });
    const masked = await screen.findByTestId("a2a-masked-key");
    expect(masked.textContent).toContain("Wx9z");
    expect(screen.getByTestId("a2a-curl").textContent).toContain("$A2A_KEY");
    await fireEvent.click(screen.getByText("Rotate key"));
    expect((await screen.findByTestId("a2a-fresh-key")).textContent).toContain("wa2a_ROTATEDabcd");
    expect(screen.getByTestId("a2a-curl").textContent).not.toContain("wa2a_ROTATEDabcd");
  });

  test("revoke hides the fresh key and drops back to No key", async () => {
    current = on;
    render(ConnectionA2ACard, { base: "/tools/agents", agent });
    await screen.findByTestId("a2a-masked-key");
    await fireEvent.click(screen.getByText("Rotate key"));
    await screen.findByTestId("a2a-fresh-key");
    await fireEvent.click(screen.getByText("Revoke key"));
    await waitFor(() => expect(screen.getByTestId("a2a-masked-key").textContent).toBe("No key"));
    expect(screen.queryByTestId("a2a-fresh-key")).toBeNull();
    expect(screen.getByText("Create key")).toBeTruthy();
  });

  test("Test is disabled while A2A is off", async () => {
    render(ConnectionA2ACard, { base: "/tools/agents", agent });
    await screen.findByTestId("a2a-endpoint-url");
    expect((screen.getByText("Test").closest("button") as HTMLButtonElement).disabled).toBe(true);
  });

  test("public card toggle autosaves and Test reports latency", async () => {
    current = on;
    render(ConnectionA2ACard, { base: "/tools/agents", agent });
    await screen.findByTestId("a2a-masked-key");
    await fireEvent.click(screen.getByRole("switch", { name: "Public agent card" }));
    await waitFor(() => expect(update).toHaveBeenCalledWith("/tools/agents", "a1", { public_card: true }));
    await fireEvent.click(screen.getByText("Test"));
    expect((await screen.findByTestId("a2a-probe")).textContent).toBe("OK — card 3 ms, ping 12 ms");
  });

  test("helpers", () => {
    expect(maskedKey({ key_set: true, key_hint: "abcd" })).toBe("wa2a_••••••••abcd");
    expect(probeLine({ ok: false, status: "card_failed", card_ms: 1, send_ms: 0, error: "401" })).toBe("Failed (card_failed): 401");
    const curl = curlExample("https://x/integrations/a2a/a1");
    expect(curl).toContain("'https://x/integrations/a2a/a1'");
    expect(curl).toContain('"method":"SendMessage"');
    expect(curl).toContain("Bearer $A2A_KEY");
  });
});
