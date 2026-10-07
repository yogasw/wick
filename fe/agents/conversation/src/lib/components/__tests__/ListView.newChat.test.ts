import { describe, test, expect, vi } from "vitest";
import { render, screen } from "@testing-library/svelte";
import { Effect } from "effect";

vi.mock("@wick-fe/common-api", async () => ({ WickClientLayer: (await import("effect")).Layer.empty }));
vi.mock("@wick-fe/common-stores", () => ({ toastError: vi.fn() }));
vi.mock("../../api/sessions.js", () => ({
  listSessions: vi.fn(() => Effect.succeed({ sessions: [], total: 0, hasMore: false })),
}));
vi.mock("../../api/options.js", () => ({
  getProjectOptions: vi.fn(() => Effect.succeed([])),
  getProviderOptions: vi.fn(() => Effect.succeed([])),
  pinProject: vi.fn(() => Effect.succeed({ pinned: false })),
}));

import ListView from "../ListView.svelte";

// "New chat" on the Agents session list must stay in Agents: the bare
// landing (base or base + "/") is what "Open Team when I open Agents"
// redirects to Team, so the link carries ?view=classic.
describe("ListView New chat", () => {
  test("links to the Agents landing marked classic, never the bare landing", async () => {
    render(ListView, { props: { base: "/tools/agents" } });
    const link = await screen.findByRole("link", { name: "New chat" });
    expect(link.getAttribute("href")).toBe("/tools/agents/?view=classic");
  });
});
