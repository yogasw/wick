import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/svelte";

vi.mock("../../router.js", () => ({ push: vi.fn() }));

vi.mock("../../api/tickets.js", async (orig) => {
  const { Effect } = await import("effect");
  const real = (await orig()) as Record<string, unknown>;
  return {
    ...real,
    getTicketFilter: vi.fn(() => Effect.succeed({})),
    getTicketPrefs: vi.fn(() => Effect.succeed({})),
    getProjectTickets: vi.fn(() => Effect.succeed({ config: { enabled: true }, statuses: [], tickets: [], untracked: [], users: {} })),
    getTicket: vi.fn(() =>
      Effect.succeed({
        ticket: { id: "T-7", project_id: "proj-42", title: "Seven", status: "open", created_at: "2026-10-01T00:00:00Z", updated_at: "2026-10-01T00:00:00Z" },
        config: { enabled: true },
        sessions: [],
        notes: [],
        statuses: [{ key: "open", label: "Open" }],
        users: {},
      }),
    ),
  };
});
vi.mock("../../stores/sessionsStream.js", () => ({ connectSessionsStream: vi.fn(() => () => {}) }));

/* Whether the caller has a Team (the roster names a Captain). */
const team = vi.hoisted(() => ({ on: false }));
vi.mock("../../teamTicketLinks.js", async (orig) => ({
  ...((await orig()) as Record<string, unknown>),
  hasTeam: vi.fn(async () => team.on),
}));

import ProjectLanding from "../ProjectLanding.svelte";

const props = {
  base: "/tools/agents",
  project: { id: "proj-42", name: "Acme", path: "/p", managed: true, pinned: false },
  providers: [],
  sessions: [],
  ownerTab: "me" as const,
  onOwnerTab: vi.fn(),
  onPin: vi.fn(),
  onSelectSession: vi.fn(),
};

describe("ProjectLanding ticket page — Open in Team", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } })));
    window.history.replaceState({}, "", "/tools/agents/sessions?project=proj-42&ticket=T-7");
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    window.history.replaceState({}, "", "/");
  });

  test("with a Team, the ticket page links to Team › Tickets", async () => {
    team.on = true;
    render(ProjectLanding, { props });
    const box = await screen.findByTestId("open-in-team");
    const link = box.querySelector("a")!;
    expect(link.textContent).toContain("Open in Team › Tickets ↗");
    expect(link.getAttribute("href")).toBe("/tools/agents/team/t/proj-42/T-7");
  });

  test("without a Team, nothing changes", async () => {
    team.on = false;
    render(ProjectLanding, { props });
    await screen.findByText("Seven");
    await waitFor(() => expect(screen.queryByTestId("open-in-team")).toBeNull());
  });
});
