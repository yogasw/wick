import { describe, test, expect, vi, beforeEach } from "vitest";

const stage = vi.fn();
const unstage = vi.fn();
const toastError = vi.fn();
const loadStatus = vi.fn(() => Promise.resolve());

vi.mock("$lib/api/scm", () => ({
  stage: (...a: unknown[]) => stage(...a),
  unstage: (...a: unknown[]) => unstage(...a),
}));
vi.mock("@wick-fe/common-stores", () => ({
  toastOk: vi.fn(),
  toastError: (...a: unknown[]) => toastError(...a),
}));
vi.mock("$lib/stores/scm", async () => {
  const { writable } = await import("svelte/store");
  return {
    sessionID: writable("s1"),
    activeRepo: writable("wick"),
    changes: writable([]),
    branch: writable(""),
    loadStatus: () => loadStatus(),
    loadRepos: vi.fn(),
    selection: writable([]),
  };
});

import { stagePaths, unstagePaths } from "$lib/git-actions";

describe("git-actions stage/unstage errors", () => {
  beforeEach(() => {
    stage.mockReset();
    unstage.mockReset();
    toastError.mockReset();
    loadStatus.mockClear();
  });

  test("a failed stage toasts the error cut before git's own explanation", async () => {
    stage.mockRejectedValueOnce(
      new Error("stage a.txt: git add: exit status 128: fatal: Unable to create '.git/index.lock': File exists.\n\nAnother git process seems to be running"),
    );
    await stagePaths(["a.txt"]);
    expect(toastError).toHaveBeenCalledWith("Stage failed", "stage a.txt");
    expect(loadStatus).toHaveBeenCalled();
  });

  test("a failed unstage without a git marker keeps only the first line", async () => {
    unstage.mockRejectedValueOnce(new Error("permission denied\nsecond line"));
    await unstagePaths(["a.txt"]);
    expect(toastError).toHaveBeenCalledWith("Unstage failed", "permission denied");
  });

  test("a successful stage does not toast", async () => {
    stage.mockResolvedValueOnce(undefined);
    await stagePaths(["a.txt"]);
    expect(toastError).not.toHaveBeenCalled();
    expect(stage).toHaveBeenCalledWith("s1", "wick", ["a.txt"]);
  });
});
