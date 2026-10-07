import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, fireEvent, screen, waitFor } from "@testing-library/svelte";

const files = [
  { path: "internal/agents/scm/compare.go", status: "M", additions: 3, deletions: 1 },
  { path: "fe/agents/scm/src/lib/components/CompareModal.svelte", status: "M", additions: 10, deletions: 2 },
  { path: "README.md", status: "A", additions: 1, deletions: 0 },
];

const compareRefs = vi.fn((base: string, head: string) =>
  Promise.resolve({
    base,
    head,
    merge_base: "abc1234",
    ahead: 2,
    behind: 0,
    files,
    commits: [
      { sha: "bbb2222", subject: "second", author: "a", rel_date: "1 minute ago", iso_date: "" },
      { sha: "aaa1111", subject: "first", author: "a", rel_date: "2 minutes ago", iso_date: "" },
    ],
  }),
);
const loadRefCompare = vi.fn(() => Promise.resolve({ original: "a", modified: "b" }));

vi.mock("$lib/git-actions", () => ({
  compareRefs: (base: string, head: string) => compareRefs(base, head),
  loadRefCompare: (...a: unknown[]) => loadRefCompare(...(a as [])),
  restoreFromRef: vi.fn(),
  resetCurrentTo: vi.fn(),
  langFor: () => "plaintext",
}));
vi.mock("$lib/api/scm", () => ({
  setActiveRepo: () => Promise.resolve(),
  getHistoryRefs: () =>
    Promise.resolve({
      refs: [
        { name: "master", sha: "m1", remote: false, current: false, trunk: true },
        { name: "feature", sha: "f1", remote: false, current: true, trunk: false },
      ],
      trunk: "master",
    }),
  getBranches: () => Promise.resolve({ current: "feature", branches: ["master", "feature"], remotes: [] }),
  getLog: () =>
    Promise.resolve({ commits: [{ sha: "bbb2222", subject: "second", author: "a", rel_date: "1 minute ago", iso_date: "" }] }),
}));
vi.mock("$lib/components/MonacoView.svelte", async () => ({
  default: (await import("./stubs/Empty.svelte")).default,
}));

import { sessionID, activeRepo } from "$lib/stores/scm";
import CompareModal from "$lib/components/CompareModal.svelte";

// jsdom has no layout, so no scrollIntoView either.
Element.prototype.scrollIntoView = () => {};

beforeEach(() => {
  compareRefs.mockClear();
  loadRefCompare.mockClear();
  localStorage.clear();
  sessionID.set("S1");
  activeRepo.set("repo");
});

const lastCall = () => compareRefs.mock.calls.at(-1);

describe("CompareModal", () => {
  test("opens on the pair it was given, working tree included", async () => {
    render(CompareModal, {
      props: { onClose: vi.fn(), initial: { base: "master", head: ":worktree", threeDot: true } },
    });
    await waitFor(() => expect(lastCall()).toEqual(["master", ":worktree"]));
    expect(await screen.findByText("2 commits + uncommitted")).toBeTruthy();
    expect(screen.getAllByText("Working tree (uncommitted)").length).toBeGreaterThan(0);
  });

  test("quick presets: default branch → HEAD and last N commits", async () => {
    render(CompareModal, { props: { onClose: vi.fn() } });
    await waitFor(() => expect(compareRefs).toHaveBeenCalled());
    await fireEvent.click(screen.getByText("master → HEAD"));
    await waitFor(() => expect(lastCall()).toEqual(["master", "HEAD"]));
    await fireEvent.click(screen.getByText("Last"));
    await waitFor(() => expect(lastCall()).toEqual(["HEAD~3", "HEAD"]));
    await fireEvent.click(screen.getByText("master → Working tree"));
    await waitFor(() => expect(lastCall()).toEqual(["master", ":worktree"]));
  });

  test("file search filters the list, counts matches and Enter opens the first", async () => {
    render(CompareModal, { props: { onClose: vi.fn(), initial: { base: "master", head: "HEAD", threeDot: true } } });
    const box = (await screen.findByLabelText("Search changed files")) as HTMLInputElement;
    await fireEvent.input(box, { target: { value: "MODAL" } });
    expect(screen.getByText("1 of 3")).toBeTruthy();
    expect(screen.queryByText("compare.go")).toBeNull();
    loadRefCompare.mockClear();
    await fireEvent.keyDown(box, { key: "Enter" });
    await waitFor(() => expect(loadRefCompare).toHaveBeenCalled());
    const opened = (loadRefCompare.mock.calls.at(-1) as unknown[])[2] as { path: string };
    expect(opened.path).toBe("fe/agents/scm/src/lib/components/CompareModal.svelte");
    await fireEvent.input(box, { target: { value: "nothing-like-this" } });
    expect(screen.getByText(/No file matches/)).toBeTruthy();
  });

  test("a typed ref shaped like an option is refused before any request", async () => {
    render(CompareModal, {
      props: { onClose: vi.fn(), initial: { base: "master", head: "HEAD", threeDot: true, pickHead: true } },
    });
    const input = (await screen.findByLabelText("Filter head refs")) as HTMLInputElement;
    const before = compareRefs.mock.calls.length;
    await fireEvent.input(input, { target: { value: "--output=/x" } });
    await fireEvent.keyDown(input, { key: "Enter" });
    expect(screen.getByText("A ref may not start with '-'.")).toBeTruthy();
    expect(compareRefs.mock.calls.length).toBe(before);
    // A recent commit from the picker is a valid head.
    await fireEvent.input(input, { target: { value: "" } });
    await fireEvent.click(screen.getByText("second"));
    await waitFor(() => expect(lastCall()).toEqual(["master", "bbb2222"]));
  });
});
