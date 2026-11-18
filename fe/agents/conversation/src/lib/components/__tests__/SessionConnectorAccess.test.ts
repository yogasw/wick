import { describe, test, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import SessionConnectorAccess from "../SessionConnectorAccess.svelte";

const DATA = {
  enabled: true,
  connectors: [
    {
      connector_id: "c-1", key: "notes", label: "Notes", kind: "saved", calls: 3, errors: 1, overrides: 1, approved: 2,
      last_at: "2026-01-02T10:00:00Z", last_op: "update_page", last_status: "error", last_error: "401", last_note: "mode=write", mode: "ask",
    },
  ],
  requests: [],
  grants: [{ id: "g-1", connector_id: "c-1", op_key: "update_page", target: "page_id=p1", approver: "Owner A" }],
};

afterEach(() => vi.unstubAllGlobals());

describe("SessionConnectorAccess", () => {
  test("lists usage and posts a policy change", async () => {
    const calls: { url: string; body?: string }[] = [];
    vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ url, body: init?.body as string | undefined });
      return new Response(JSON.stringify(init?.method === "POST" ? { ok: true } : DATA), { status: 200 });
    }));
    render(SessionConnectorAccess, { base: "/tools/agents", sessionId: "sess-1" });
    await screen.findByText(/1 override/);
    expect(screen.getByText(/2 approved/)).toBeTruthy();
    expect(screen.getByRole("radio", { name: "Ask each time" }).getAttribute("aria-checked")).toBe("true");
    await fireEvent.click(screen.getByRole("radio", { name: "Blocked" }));
    await waitFor(() => expect(calls.some((c) => c.url.endsWith("/sessions/sess-1/access/policy"))).toBe(true));
    const post = calls.find((c) => c.url.endsWith("/access/policy"));
    expect(JSON.parse(post!.body!)).toEqual({ connector_id: "c-1", mode: "block" });
    expect(screen.getByText("Revoke")).toBeTruthy();
  });
});
