import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import NotesPanel from "../NotesPanel.svelte";
import type { Note } from "../../types/agents.js";

/* The panel talks to the API through the shared Effect client, which runs on
   fetch — so stubbing fetch is enough to drive it without a mock layer. */
const calls: { method: string; url: string; body?: unknown }[] = [];

beforeEach(() => {
  calls.length = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(typeof input === "string" ? input : (input as Request).url ?? input);
      const method = (init?.method ?? "GET").toUpperCase();
      // Effect's client sends the body as bytes, and jsdom's Uint8Array is
      // a different realm's class — so decode by shape, not by instanceof.
      let body: unknown;
      if (init?.body) {
        const b = init.body as ArrayBufferView | string;
        const raw =
          typeof b === "string"
            ? b
            : new TextDecoder().decode(
                ArrayBuffer.isView(b) ? new Uint8Array(b.buffer, b.byteOffset, b.byteLength) : b,
              );
        try {
          body = JSON.parse(raw);
        } catch {
          body = raw;
        }
      }
      calls.push({ method, url, body });
      return new Response(JSON.stringify({ id: "n-new", body: "x", audience: "both" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
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

function renderPanel(notes: Note[]) {
  return render(NotesPanel, {
    props: {
      base: "/tools/agents",
      scope: { ticketId: "T-4F2A" },
      notes,
      users: { "u-1": "Yoga" },
    },
  });
}

describe("NotesPanel", () => {
  test("renders seeded notes without fetching", () => {
    renderPanel([note()]);
    expect(screen.getByText("Root cause is the retry loop")).toBeTruthy();
    expect(calls.filter((c) => c.method === "GET")).toHaveLength(0);
  });

  // Audience is a label the reader has to be able to see, since it changes
  // how a note should be read — not a permission.
  test("shows who each note was written for", () => {
    renderPanel([
      note({ id: "a", body: "hint", audience: "ai" }),
      note({ id: "b", body: "handover", audience: "human" }),
      note({ id: "c", body: "general", audience: "both" }),
    ]);
    expect(screen.getByText("for the agent")).toBeTruthy();
    expect(screen.getByText("for people")).toBeTruthy();
    expect(screen.getByText("for anyone")).toBeTruthy();
  });

  // Hiding is the permission: the note stays visible to people (blurred)
  // and is explicitly labelled as out of the agent's reach.
  test("a hidden note is kept, blurred, and labelled", () => {
    const { container } = renderPanel([note({ hidden: true })]);
    expect(screen.getByText("Root cause is the retry loop")).toBeTruthy();
    expect(screen.getByText("hidden from agent")).toBeTruthy();
    expect(container.querySelector(".blur-\\[3px\\]")).not.toBeNull();
  });

  test("the eye toggle PATCHes hidden", async () => {
    renderPanel([note()]);
    await fireEvent.click(screen.getByLabelText("Hide from agent"));
    const patch = calls.find((c) => c.method === "PATCH");
    expect(patch?.url).toContain("/api/notes/n1?ticket_id=T-4F2A");
    expect(patch?.body).toEqual({ hidden: true });
  });

  test("checking a checkable note PATCHes done", async () => {
    renderPanel([note({ checkable: true })]);
    await fireEvent.click(screen.getByLabelText("Mark note done"));
    const patch = calls.find((c) => c.method === "PATCH");
    expect(patch?.body).toEqual({ done: true });
  });

  test("a plain note has no checkbox", () => {
    renderPanel([note()]);
    expect(screen.queryByLabelText("Mark note done")).toBeNull();
  });

  test("adding a note POSTs body, checkable, and audience", async () => {
    renderPanel([]);
    await fireEvent.input(screen.getByLabelText("New note"), {
      target: { value: "found it in the queue config" },
    });
    await fireEvent.click(screen.getByLabelText("Note audience").parentElement!.querySelector("input[type=checkbox]")!);
    await fireEvent.click(screen.getByText("Add note"));
    const post = calls.find((c) => c.method === "POST");
    expect(post?.url).toContain("/api/notes?ticket_id=T-4F2A");
    expect(post?.body).toMatchObject({ body: "found it in the queue config", audience: "both" });
  });

  test("the add button stays disabled for an empty draft", () => {
    renderPanel([]);
    expect((screen.getByText("Add note") as HTMLButtonElement).disabled).toBe(true);
  });

  test("deleting a note DELETEs it", async () => {
    renderPanel([note()]);
    await fireEvent.click(screen.getByLabelText("Delete note"));
    const del = calls.find((c) => c.method === "DELETE");
    expect(del?.url).toContain("/api/notes/n1?ticket_id=T-4F2A");
  });
});
