import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
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

/* The shared client sends JSON bodies as bytes; read them back as JSON. */
function jsonBody(init: RequestInit | undefined): unknown {
  const b = init?.body;
  if (b == null) return undefined;
  const text = typeof b === "string" ? b : new TextDecoder().decode(b as ArrayBufferView as Uint8Array);
  return JSON.parse(text);
}

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

/* The ticket's DESCRIPTION is on the panel too. With ticket mode on there is
   no separate Notes tab, so this is the one place both halves of the story
   are readable: what was asked, and what was found. */
describe("TicketPanel — the ticket's description", () => {
  test("a description renders above the notes", () => {
    renderPanel({ ticket: { id: "T-1", title: "Fix retries", status: "open", body: "Webhook 401s on retry" } });
    expect(screen.getByText("Webhook 401s on retry")).toBeTruthy();
    expect(screen.getByTestId("ticket-body")).toBeTruthy();
  });

  // No empty section — but an invitation to write one, like "Write a note".
  test("no description shows a slot to write it, not an empty section", () => {
    renderPanel();
    expect(screen.queryByTestId("ticket-body")).toBeNull();
    expect(screen.getByTestId("ticket-body-add").textContent).toContain("Describe what the ticket asks");
  });

  test("the slot opens an editor, and Save PATCHes the body", async () => {
    const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
    renderPanel();
    await fireEvent.click(screen.getByTestId("ticket-body-add"));
    const ta = screen.getByTestId("ticket-body-input") as HTMLTextAreaElement;
    await fireEvent.input(ta, { target: { value: "Kalender dokter terlepas setelah pilih tanggal" } });
    await fireEvent.click(screen.getByTestId("ticket-body-save"));
    await vi.waitFor(() => {
      const call = fetchMock.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === "PATCH");
      expect(call).toBeTruthy();
      expect(String(call![0])).toContain("/api/tickets/T-1");
      expect(jsonBody(call![1] as RequestInit)).toEqual({ body: "Kalender dokter terlepas setelah pilih tanggal" });
    });
  });

  test("an existing description can be edited", async () => {
    renderPanel({ ticket: { id: "T-1", title: "Fix retries", status: "open", body: "Webhook 401s on retry" } });
    await fireEvent.click(screen.getByTestId("ticket-body-edit"));
    expect((screen.getByTestId("ticket-body-input") as HTMLTextAreaElement).value).toBe("Webhook 401s on retry");
  });

  // Folded past a few lines: a long description would push the notes off the
  // panel, which is the opposite of helping.
  test("a long description is folded behind Show more", async () => {
    renderPanel({
      ticket: { id: "T-1", title: "Fix retries", status: "open", body: "line\n".repeat(20) },
    });
    const toggle = screen.getByTestId("ticket-body-toggle") as HTMLButtonElement;
    expect(toggle.textContent).toContain("Show more");
    toggle.click();
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.getByTestId("ticket-body-toggle").textContent).toContain("Show less");
  });

  test("a short description gets no toggle", () => {
    renderPanel({ ticket: { id: "T-1", title: "Fix retries", status: "open", body: "one line" } });
    expect(screen.queryByTestId("ticket-body-toggle")).toBeNull();
  });

  // The link to the Notes tab is only offered when that tab exists; with the
  // two merged it would be a dead end.
  test("the Notes-tab link is absent unless a handler is given", () => {
    renderPanel();
    expect(screen.queryByText("Open tab →")).toBeNull();
    renderPanel({ onOpenNotes: () => {} });
    expect(screen.getAllByText("Open tab →").length).toBe(1);
  });
});

describe("TicketPanel — additional info", () => {
  test("the project's fields show under Custom fields", () => {
    renderPanel({
      ticket: { id: "T-1", title: "Fix retries", status: "open", fields: { app_code: "app-abc123" } },
      fields: [{ key: "app_code", label: "App Code", type: "text" }],
    });
    expect(screen.getByTestId("ticket-fields").textContent).toContain("Custom fields");
    expect(screen.getByText("app-abc123")).toBeTruthy();
  });

  test("no field definitions means no section", () => {
    renderPanel({ ticket: { id: "T-1", title: "Fix retries", status: "open", fields: { stray: "x" } } });
    expect(screen.queryByTestId("ticket-fields")).toBeNull();
  });

  test("saving a field PATCHes only that key", async () => {
    const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
    renderPanel({
      ticket: { id: "T-1", title: "Fix retries", status: "open", fields: { app_code: "old", other: "keep" } },
      fields: [{ key: "app_code", label: "App Code", type: "text" }],
    });
    await fireEvent.click(screen.getByTestId("ticket-field-edit-app_code"));
    const input = screen.getByTestId("ticket-field-input-app_code") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "new" } });
    await fireEvent.keyDown(input, { key: "Enter" });
    await vi.waitFor(() => {
      const call = fetchMock.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === "PATCH");
      expect(call).toBeTruthy();
      expect(jsonBody(call![1] as RequestInit)).toEqual({ fields: { app_code: "new" } });
    });
  });
});

describe("TicketPanel — custom fields: filled at rest, every field one click away", () => {
  const defs = [
    { key: "app_code", label: "App Code", type: "text" as const },
    { key: "type", label: "Type", type: "select" as const, options: ["bug", "task"] },
  ];

  test("nothing filled: the section is there, with 'Add custom fields' instead of rows", () => {
    renderPanel({ ticket: { id: "T-1", title: "Fix retries", status: "open", fields: { app_code: "  " } }, fields: defs });
    expect(screen.getByTestId("ticket-fields")).toBeTruthy();
    expect(screen.queryByTestId("ticket-fields-list")).toBeNull();
    expect(screen.getByTestId("ticket-fields-toggle").textContent).toContain("Add custom fields (2)");
  });

  test("an empty field filled from the open list PATCHes that key", async () => {
    const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
    renderPanel({ ticket: { id: "T-1", title: "Fix retries", status: "open", fields: {} }, fields: defs });
    await fireEvent.click(screen.getByTestId("ticket-fields-toggle"));
    await fireEvent.click(screen.getByTestId("ticket-field-add-type"));
    await fireEvent.change(screen.getByTestId("ticket-field-input-type"), { target: { value: "bug" } });
    await vi.waitFor(() => {
      const call = fetchMock.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === "PATCH");
      expect(call).toBeTruthy();
      expect(jsonBody(call![1] as RequestInit)).toEqual({ fields: { type: "bug" } });
    });
  });

  test("no field definitions means no section at all", () => {
    renderPanel({ ticket: { id: "T-1", title: "Fix retries", status: "open", fields: { stray: "x" } } });
    expect(screen.queryByTestId("ticket-fields")).toBeNull();
  });
});

describe("TicketPanel — the description's fold", () => {
  test("Show more sits at the end, under the text it folds", () => {
    renderPanel({ ticket: { id: "T-1", title: "Fix retries", status: "open", body: "line\n".repeat(20) } });
    const section = screen.getByTestId("ticket-body");
    const toggle = screen.getByTestId("ticket-body-toggle");
    expect(section.lastElementChild!.contains(toggle)).toBe(true);
  });

  test("clicking the folded text opens it — no need to find the toggle", async () => {
    renderPanel({ ticket: { id: "T-1", title: "Fix retries", status: "open", body: "line\n".repeat(20) } });
    await fireEvent.click(screen.getByTestId("ticket-body-text"));
    expect(screen.getByTestId("ticket-body-toggle").textContent).toContain("Show less");
  });

  test("a link inside the folded text still goes where it points, without toggling", async () => {
    renderPanel({ ticket: { id: "T-1", title: "Fix retries", status: "open", body: "[doc](https://x.io/a)\n" + "line\n".repeat(20) } });
    const a = screen.getByText("doc");
    a.addEventListener("click", (e) => e.preventDefault());
    await fireEvent.click(a);
    expect(screen.getByTestId("ticket-body-toggle").textContent).toContain("Show more");
  });
});

describe("TicketPanel — custom buttons (Sync)", () => {
  test("no buttons, no row", () => {
    renderPanel();
    expect(screen.queryByTestId("ticket-buttons")).toBeNull();
  });

  test("a sync button runs the ticket action by id and re-reads the ticket", async () => {
    const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
    fetchMock.mockImplementation(async () =>
      new Response(JSON.stringify({ ok: true, status: 200, message: "synced from Notion" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const onChanged = vi.fn();
    renderPanel({ buttons: [{ id: "btn-1", label: "Sync from Notion" }], onChanged });
    const btn = screen.getByTestId("ticket-button-btn-1");
    // In the header it reads "Sync"; the full label is the tooltip.
    expect(btn.textContent).toContain("Sync");
    expect(btn.getAttribute("title")).toBe("Sync from Notion");
    await fireEvent.click(btn);
    await vi.waitFor(() => {
      const call = fetchMock.mock.calls.find(([u]) => String(u).includes("/api/tickets/T-1/actions/btn-1"));
      expect(call).toBeTruthy();
      expect((call![1] as RequestInit).method).toBe("POST");
      expect(onChanged).toHaveBeenCalled();
    });
  });

  test("while one runs, the buttons are disabled so a slow receiver is not double-fired", async () => {
    const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
    let release!: () => void;
    fetchMock.mockImplementation(() => new Promise<Response>((res) => {
      release = () => res(new Response(JSON.stringify({ ok: true, status: 200 }), { status: 200, headers: { "Content-Type": "application/json" } }));
    }));
    renderPanel({ buttons: [{ id: "btn-1", label: "Sync from Notion" }, { id: "btn-2", label: "Open in Notion" }] });
    await fireEvent.click(screen.getByTestId("ticket-button-btn-1"));
    await vi.waitFor(() => expect(screen.getByTestId("ticket-button-btn-1").textContent).toContain("Syncing"));
    expect((screen.getByTestId("ticket-button-btn-2") as HTMLButtonElement).disabled).toBe(true);
    release();
  });
});

/* The move/attach picker is searched: a project holds dozens of open
   tickets, and an id pasted in must land on its ticket, not on whatever
   title shares a few characters with it. */
describe("TicketPanel — ticket picker search", () => {
  test("typing an id puts that ticket first and hides non-matches", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        new Response(
          JSON.stringify({
            tickets: [
              { id: "3eb1f07f4ae081c38ec0edde28a0604e", title: "Paragon webhook", status: "open", updated_at: "2026-09-30T10:00:00Z" },
              { id: "3eb1f07f4ae08125bf4bff60a77bce2f", title: "MTI changes", status: "open", updated_at: "2026-09-30T09:00:00Z" },
              { id: "T-2", title: "Unrelated", status: "open", updated_at: "2026-09-30T11:00:00Z" },
            ],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );
    renderPanel({ ticket: null });
    await fireEvent.click(screen.getByText("Attach to existing…"));
    const search = (await screen.findByTestId("ticket-pick-search")) as HTMLInputElement;
    await fireEvent.input(search, { target: { value: "bce2f" } });
    const rows = screen.getByTestId("ticket-pick-list").querySelectorAll("button");
    expect(rows.length).toBe(1);
    expect(rows[0].textContent).toContain("MTI changes");
    // Long id shown shortened, full id kept on the tooltip.
    expect(rows[0].textContent).toContain("3eb1…7bce2f");
    expect(rows[0].getAttribute("title")).toContain("3eb1f07f4ae08125bf4bff60a77bce2f");
  });
});
