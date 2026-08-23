import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/svelte";
import NotesRail from "../NotesRail.svelte";
import type { Note, NotesResponse } from "../../types/agents.js";

/* The rail seeds NotesPanel with the notes it already has, so nothing here
   should fetch — but the panel reaches for the shared client on mount, and an
   unstubbed fetch would make that a noisy failure rather than a no-op. */
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
  body: "Root cause is the retry loop",
  audience: "both",
  created_at: "2026-08-22T00:00:00Z",
  updated_at: "2026-08-22T00:00:00Z",
  ...over,
});

function renderRail(notes: Note[], ticket: NotesResponse["ticket"] = undefined) {
  return render(NotesRail, {
    props: {
      base: "/tools/agents",
      sessionId: "s1",
      ticket: ticket ?? null,
      info: { notes, users: {} } as NotesResponse,
    },
  });
}

describe("NotesRail", () => {
  test("the heading counts the notes", () => {
    renderRail([note({ id: "n1" }), note({ id: "n2" })]);
    expect(screen.getByTestId("notes-count").textContent?.trim()).toBe("2");
  });

  // Nothing to count is nothing to badge: a "0" next to the heading reads as
  // a problem rather than as an empty list.
  test("no notes means no badge", () => {
    renderRail([]);
    expect(screen.queryByTestId("notes-count")).toBeNull();
  });

  // Hidden notes are the ones the agent will not read, so folding them into
  // the main count would say the opposite of what the badge means. They get
  // their own label instead.
  test("hidden notes are counted apart, not in the total", () => {
    renderRail([note({ id: "n1" }), note({ id: "n2", hidden: true })]);
    expect(screen.getByTestId("notes-count").textContent?.trim()).toBe("1");
    expect(screen.getByTestId("notes-hidden-count").textContent).toContain("1 hidden");
  });

  test("no hidden notes means no hidden label", () => {
    renderRail([note()]);
    expect(screen.queryByTestId("notes-hidden-count")).toBeNull();
  });

  // A ticket's scope is shared, and the rail has to say so — the notes below
  // are read by every session on that ticket, not just this chat.
  test("names the ticket when the scope is one", () => {
    renderRail([note()], { id: "T-4F2A", title: "Fix the webhook", status: "open" });
    expect(screen.getByText("T-4F2A")).toBeTruthy();
  });

  test("says the notes are private when there is no ticket", () => {
    renderRail([note()]);
    expect(screen.getByText(/Private to this chat/)).toBeTruthy();
  });
});
