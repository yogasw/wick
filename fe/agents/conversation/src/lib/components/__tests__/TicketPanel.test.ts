import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/svelte";
import TicketPanel from "../TicketPanel.svelte";
import type { Note } from "../../types/agents.js";

/* The panel seeds NotesPanel with the notes the rail already fetched, so
   nothing here should hit the network — but the panel reaches for the shared
   client on mount, and an unstubbed fetch would fail loudly instead of
   quietly doing nothing. */
beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      new Response(JSON.stringify({ notes: [], users: {} }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
});

const note = (over: Partial<Note> = {}): Note => ({
  id: "n1",
  body: "Checked the webhook, it 401s",
  audience: "both",
  created_at: "2026-08-22T00:00:00Z",
  updated_at: "2026-08-22T00:00:00Z",
  ...over,
});

function renderPanel(over: Record<string, unknown> = {}) {
  return render(TicketPanel, {
    props: {
      base: "/tools/agents",
      sessionId: "s1",
      projectId: "p1",
      ticket: { id: "T-1", title: "Fix retries", status: "open" },
      noteCount: 1,
      notes: [note()],
      users: {},
      ...over,
    },
  });
}

/* Notes ARE shown on the ticket. They used to be a link to another tab,
   which left the one place you look at a ticket unable to show what had
   been written about it. */
describe("TicketPanel — notes in place", () => {
  test("the notes themselves are on the panel", () => {
    renderPanel();
    expect(screen.getByText("Checked the webhook, it 401s")).toBeTruthy();
  });

  test("the section collapses, and says how many are folded away", async () => {
    renderPanel();
    (screen.getByTestId("ticket-notes-toggle") as HTMLButtonElement).click();
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByText("Checked the webhook, it 401s")).toBeNull();
    expect(screen.getByTestId("ticket-notes-toggle").textContent).toContain("1");
  });

  // The Notes tab is still where notes live for a chat on no ticket, so the
  // way through to it stays.
  test("the Notes tab is still one click away", () => {
    const onOpenNotes = vi.fn();
    renderPanel({ onOpenNotes });
    (screen.getByText("Open tab →") as HTMLButtonElement).click();
    expect(onOpenNotes).toHaveBeenCalled();
  });

  // Off a ticket the panel is an offer to create one — and the chat's own
  // notes still belong here.
  test("works on a chat with no ticket", () => {
    renderPanel({ ticket: null });
    expect(screen.getByTestId("ticket-notes-toggle").textContent).toContain("this chat");
    expect(screen.getByText("Checked the webhook, it 401s")).toBeTruthy();
  });
});
