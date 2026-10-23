import { describe, test, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import DeleteProjectDialog from "../DeleteProjectDialog.svelte";
import { canConfirmDelete, DELETE_CONSENT, deleteProjectBody, makeSums, sumAnswered } from "../delete-project.js";

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
  test("every gate must pass, even for an empty project", () => {
    const sums = makeSums(seq(0.125, 0.25, 0.375, 0.5));
    const ok = { typed: " Alpha ", consent: true, sums, answers: ["12", "30"] as [string, string] };
    expect(canConfirmDelete(managed, ok)).toBe(true);
    expect(canConfirmDelete({ ...managed, chats: 0 }, { ...ok, typed: "" })).toBe(false);
    expect(canConfirmDelete(managed, { ...ok, typed: "alpha" })).toBe(false);
    expect(canConfirmDelete(managed, { ...ok, consent: false })).toBe(false);
    expect(canConfirmDelete(managed, { ...ok, answers: ["12", "31"] })).toBe(false);
    expect(canConfirmDelete(managed, { ...ok, answers: ["", "30"] })).toBe(false);
    expect(canConfirmDelete({ ...managed, protected: true }, ok)).toBe(false);
  });
});

/* A fixed rng: each call returns the next value, cycling. */
function seq(...vals: number[]): () => number {
  let i = 0;
  return () => vals[i++ % vals.length];
}

describe("makeSums", () => {
  test("factors are 2..9 and the deterministic rng picks them", () => {
    expect(makeSums(seq(0.125, 0.25, 0.375, 0.5))).toEqual([{ a: 3, b: 4 }, { a: 5, b: 6 }]);
    expect(makeSums(seq(0, 0.999, 0.5, 0.5))).toEqual([{ a: 2, b: 9 }, { a: 6, b: 6 }]);
  });
  test("never the same pair twice, in either order", () => {
    // 3×4, then 3×4 again, then 4×3, then 5×6: the repeats are skipped.
    expect(makeSums(seq(0.125, 0.25, 0.125, 0.25, 0.25, 0.125, 0.375, 0.5))).toEqual([
      { a: 3, b: 4 },
      { a: 5, b: 6 },
    ]);
    // A stuck rng still yields two different pairs.
    const [x, y] = makeSums(() => 0);
    expect(x).not.toEqual(y);
  });
  test("random sums stay in range and differ", () => {
    for (let i = 0; i < 200; i++) {
      const [x, y] = makeSums();
      for (const v of [x.a, x.b, y.a, y.b]) expect(v >= 2 && v <= 9).toBe(true);
      expect(x.a === y.a && x.b === y.b).toBe(false);
      expect(x.a === y.b && x.b === y.a).toBe(false);
    }
  });
  test("answers are checked as numbers", () => {
    expect(sumAnswered({ a: 3, b: 4 }, " 12 ")).toBe(true);
    expect(sumAnswered({ a: 3, b: 4 }, "13")).toBe(false);
    expect(sumAnswered({ a: 3, b: 4 }, "")).toBe(false);
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

  test("asks for the name, consent and both sums, then deletes", async () => {
    const calls = mockFetch(managed);
    const onDeleted = vi.fn();
    render(DeleteProjectDialog, { props: { open: true, base: "/tools/agents", projectID: "p1", onDeleted, onCancel: vi.fn(), rng: seq(0.125, 0.25, 0.375, 0.5) } });
    await screen.findByText("Delete project Alpha?");
    expect(screen.getByText("3 × 4 = ?")).toBeTruthy();
    expect(screen.getByText("5 × 6 = ?")).toBeTruthy();
    expect(screen.getByText(DELETE_CONSENT)).toBeTruthy();
    const btn = screen.getByRole("button", { name: "Delete project" }) as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    await fireEvent.input(screen.getByLabelText("Project name"), { target: { value: "Alpha" } });
    await fireEvent.input(screen.getByLabelText("Answer 1"), { target: { value: "12" } });
    await fireEvent.input(screen.getByLabelText("Answer 2"), { target: { value: "30" } });
    expect(btn.disabled).toBe(true);
    await fireEvent.click(screen.getByTestId("delete-project-consent"));
    expect(btn.disabled).toBe(false);
    await fireEvent.input(screen.getByLabelText("Answer 2"), { target: { value: "31" } });
    expect(btn.disabled).toBe(true);
    await fireEvent.input(screen.getByLabelText("Answer 2"), { target: { value: "30" } });
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
    render(DeleteProjectDialog, { props: { open: true, base: "", projectID: "p1", onDeleted, onCancel: vi.fn(), rng: seq(0.125, 0.25, 0.375, 0.5) } });
    await screen.findByText("Delete project Alpha?");
    await fireEvent.input(screen.getByLabelText("Project name"), { target: { value: "Alpha" } });
    await fireEvent.input(screen.getByLabelText("Answer 1"), { target: { value: "12" } });
    await fireEvent.input(screen.getByLabelText("Answer 2"), { target: { value: "30" } });
    await fireEvent.click(screen.getByTestId("delete-project-consent"));
    await fireEvent.click(screen.getByRole("button", { name: "Delete project" }));
    await screen.findByText("nope");
    expect(onDeleted).not.toHaveBeenCalled();
  });
});
