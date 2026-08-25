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

  // Hiding says what it does in words now. As a bare eye beside a pencil it
  // was guesswork — keeping a note away from the agent while leaving it in the
  // list is not something an icon conveys.
  test("hiding from the agent PATCHes hidden", async () => {
    renderPanel([note()]);
    await fireEvent.click(screen.getByTestId("note-more-n1"));
    await fireEvent.click(screen.getByTestId("note-hide-n1"));
    const patch = calls.find((c) => c.method === "PATCH");
    expect(patch?.url).toContain("/api/notes/n1?ticket_id=T-4F2A");
    expect(patch?.body).toEqual({ hidden: true });
  });

  test("a hidden note offers to be shown again", async () => {
    renderPanel([note({ hidden: true })]);
    await fireEvent.click(screen.getByTestId("note-more-n1"));
    expect(screen.getByText("Show to agent")).toBeTruthy();
    await fireEvent.click(screen.getByTestId("note-hide-n1"));
    expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ hidden: false });
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

  // Deleting takes three deliberate steps — open the menu, pick Delete,
  // confirm — because the old single bare "×" beside the edit pencil read as
  // "close" and took the note with it.
  test("deleting a note DELETEs it, after the menu and a confirmation", async () => {
    renderPanel([note()]);
    await fireEvent.click(screen.getByTestId("note-more-n1"));
    await fireEvent.click(screen.getByTestId("note-delete-n1"));
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);

    await fireEvent.click(screen.getByTestId("note-delete-confirm-n1"));
    const del = calls.find((c) => c.method === "DELETE");
    expect(del?.url).toContain("/api/notes/n1?ticket_id=T-4F2A");
  });

  // Nothing destructive is one click away on the row itself: only the
  // hide/show toggle, whose effect is reversible.
  // The card carries ONE control, in its corner. Two icons in the row took
  // width from the first line of every note and truncated it.
  test("the card exposes one actions button and nothing else", () => {
    renderPanel([note()]);
    expect(screen.getByTestId("note-more-n1")).toBeTruthy();
    expect(screen.queryByLabelText("Delete note")).toBeNull();
    expect(screen.queryByLabelText("Hide from agent")).toBeNull();
    expect(screen.queryByLabelText("Edit note")).toBeNull();
  });

  test("backing out of the confirmation leaves the note alone", async () => {
    renderPanel([note()]);
    await fireEvent.click(screen.getByTestId("note-more-n1"));
    await fireEvent.click(screen.getByTestId("note-delete-n1"));
    await fireEvent.click(screen.getByText("Keep"));
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    expect(screen.getByTestId("note-n1")).toBeTruthy();
  });

  test("editing is reached through the menu", async () => {
    renderPanel([note()]);
    await fireEvent.click(screen.getByTestId("note-more-n1"));
    await fireEvent.click(screen.getByText("Edit note"));
    const box = screen.getByLabelText("Edit note") as HTMLTextAreaElement;
    expect(box.value).toBe("Root cause is the retry loop");
  });

  /* ── markdown ── */

  // A note body is Markdown, so the list shows it rendered — the source is
  // what the edit box is for.
  test("a note body renders its markdown", () => {
    renderPanel([note({ body: "**bold** and `code`\n\n- one\n- two" })]);
    const el = screen.getByTestId("note-body-n1");
    expect(el.querySelector("strong")?.textContent).toBe("bold");
    expect(el.querySelector("code")?.textContent).toBe("code");
    expect(el.querySelectorAll("li")).toHaveLength(2);
  });

  // Rendering markdown must not turn a note into an injection point: the
  // body is data, and a note can be written by an agent.
  test("markdown rendering escapes embedded html", () => {
    renderPanel([note({ body: "<img src=x onerror=alert(1)> <script>bad()</script>" })]);
    const el = screen.getByTestId("note-body-n1");
    expect(el.querySelector("script")).toBeNull();
    expect(el.querySelector("img")).toBeNull();
  });

  test("the composer says notes are markdown", () => {
    renderPanel([]);
    expect(screen.getByText(/Markdown/)).toBeTruthy();
  });

  // Ctrl+Enter is the save, because a plain Enter has to stay a newline in
  // something whose useful entries are multi-line.
  test("ctrl+enter adds a note and plain enter does not", async () => {
    renderPanel([]);
    const box = screen.getByLabelText("New note");
    await fireEvent.input(box, { target: { value: "found it" } });
    await fireEvent.keyDown(box, { key: "Enter" });
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    await fireEvent.keyDown(box, { key: "Enter", ctrlKey: true });
    await vi.waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
  });

  /* ── edit mode gives the box the whole row ── */

  // A checkbox and a "more" menu beside a textarea are two clicks that throw
  // the edit away. While editing, neither is there.
  test("editing hides the row's own controls", async () => {
    renderPanel([note({ checkable: true })]);
    expect(screen.getByLabelText("Mark note done")).toBeTruthy();

    await fireEvent.click(screen.getByTestId("note-more-n1"));
    await fireEvent.click(screen.getByText("Edit note"));

    expect(screen.queryByTestId("note-more-n1")).toBeNull();
    expect(screen.queryByLabelText("Mark note done")).toBeNull();
    expect(screen.queryByLabelText("Hide from agent")).toBeNull();
    expect(screen.getByLabelText("Edit note")).toBeTruthy();
  });

  test("the controls come back once the edit ends", async () => {
    renderPanel([note({ checkable: true })]);
    await fireEvent.click(screen.getByTestId("note-more-n1"));
    await fireEvent.click(screen.getByText("Edit note"));
    await fireEvent.click(screen.getByText("Cancel"));
    expect(screen.getByTestId("note-more-n1")).toBeTruthy();
    expect(screen.getByLabelText("Mark note done")).toBeTruthy();
  });

  test("escape abandons an edit without writing", async () => {
    renderPanel([note()]);
    await fireEvent.click(screen.getByTestId("note-more-n1"));
    await fireEvent.click(screen.getByText("Edit note"));
    const box = screen.getByLabelText("Edit note");
    await fireEvent.input(box, { target: { value: "changed" } });
    await fireEvent.keyDown(box, { key: "Escape" });
    expect(calls.some((c) => c.method === "PATCH")).toBe(false);
    expect(screen.queryByLabelText("Edit note")).toBeNull();
  });
});
