import { describe, test, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import DeleteProjectDialog from "../DeleteProjectDialog.svelte";
import { canConfirmDelete, deleteProjectBody, needsTypedName } from "../delete-project.js";

const managed = { id: "p1", name: "Alpha", chats: 3, protected: false };

describe("delete-project wording", () => {
  test("managed project names chats, folder and memory", () => {
    expect(deleteProjectBody(managed)).toBe(
      "This deletes 3 chats, its files folder and the agents' memory for this project. This cannot be undone.",
    );
  });
  test("custom path says the folder stays", () => {
    const body = deleteProjectBody({ ...managed, chats: 1, custom_path: "/srv/repo" });
    expect(body).toContain("1 chat and");
    expect(body).toContain("/srv/repo is yours and stays on disk");
  });
  test("typed name only when there are chats", () => {
    expect(needsTypedName({ ...managed, chats: 0 })).toBe(false);
    expect(canConfirmDelete({ ...managed, chats: 0 }, "")).toBe(true);
    expect(canConfirmDelete(managed, "alpha")).toBe(false);
    expect(canConfirmDelete(managed, " Alpha ")).toBe(true);
    expect(canConfirmDelete({ ...managed, chats: 0, protected: true }, "")).toBe(false);
  });
});

function mockFetch(preview: object, deleteStatus = 200) {
  const calls: { url: string; method: string }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ url, method: init?.method ?? "GET" });
      if ((init?.method ?? "GET") === "DELETE") {
        return new Response(deleteStatus === 200 ? '{"status":"deleted"}' : '{"error":"nope"}', { status: deleteStatus });
      }
      return new Response(JSON.stringify(preview), { status: 200 });
    }),
  );
  return calls;
}

describe("DeleteProjectDialog", () => {
  afterEach(() => vi.unstubAllGlobals());

  test("asks for the name, then deletes", async () => {
    const calls = mockFetch(managed);
    const onDeleted = vi.fn();
    render(DeleteProjectDialog, { props: { open: true, base: "/tools/agents", projectID: "p1", onDeleted, onCancel: vi.fn() } });
    await screen.findByText("Delete project Alpha?");
    const btn = screen.getByRole("button", { name: "Delete project" }) as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    await fireEvent.input(screen.getByLabelText("Project name"), { target: { value: "Alpha" } });
    expect(btn.disabled).toBe(false);
    await fireEvent.click(btn);
    await waitFor(() => expect(onDeleted).toHaveBeenCalled());
    expect(calls).toEqual([
      { url: "/tools/agents/projects/p1/delete-preview", method: "GET" },
      { url: "/tools/agents/projects/p1", method: "DELETE" },
    ]);
  });

  test("protected project cannot be confirmed", async () => {
    mockFetch({ ...managed, chats: 0, protected: true });
    render(DeleteProjectDialog, { props: { open: true, base: "", projectID: "p1", onDeleted: vi.fn(), onCancel: vi.fn() } });
    await screen.findByText("This project can't be deleted (default/personal).");
    expect((screen.getByRole("button", { name: "Delete project" }) as HTMLButtonElement).disabled).toBe(true);
  });

  test("server error is shown, not swallowed", async () => {
    mockFetch({ ...managed, chats: 0 }, 500);
    const onDeleted = vi.fn();
    render(DeleteProjectDialog, { props: { open: true, base: "", projectID: "p1", onDeleted, onCancel: vi.fn() } });
    await screen.findByText("Delete project Alpha?");
    await fireEvent.click(screen.getByRole("button", { name: "Delete project" }));
    await screen.findByText("nope");
    expect(onDeleted).not.toHaveBeenCalled();
  });
});
