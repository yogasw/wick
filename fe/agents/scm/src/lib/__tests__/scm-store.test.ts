import { describe, test, expect, vi } from "vitest";
import { get } from "svelte/store";
import type { GitStatusSnapshot } from "$lib/api/scm";

const getRepos = vi.fn();
vi.mock("$lib/api/scm", () => ({
  getRepos: (...a: unknown[]) => getRepos(...a),
  setActiveRepo: vi.fn(() => Promise.resolve()),
}));

import { sessionID, snapshot, activeRepo, loadRepos } from "$lib/stores/scm";

function snap(rel: string): GitStatusSnapshot {
  return {
    repos: [{ rel } as GitStatusSnapshot["repos"][number]],
    statuses: {},
    active: rel,
    active_explicit: true,
    total_changed: 0,
  };
}

// The SCM bundle's stores outlive the island: switching to a session in
// another project remounts the panel against the SAME module state.
describe("scm store across a session switch", () => {
  test("a fetch for the previous session landing after the switch is dropped", async () => {
    sessionID.set("old");
    let resolveOld!: (s: GitStatusSnapshot) => void;
    getRepos.mockImplementationOnce(() => new Promise((r) => { resolveOld = r; }));
    const pending = loadRepos();

    sessionID.set("new");
    resolveOld(snap("old-project-repo"));
    await pending;

    expect(get(snapshot).repos).toEqual([]);
    expect(get(activeRepo)).not.toBe("old-project-repo");
    expect(localStorage.getItem("wick.scm.activeRepo.new")).not.toBe("old-project-repo");
  });

  test("switching session clears the previous session's repo list", async () => {
    sessionID.set("a");
    getRepos.mockResolvedValueOnce(snap("repo-a"));
    await loadRepos();
    expect(get(snapshot).repos.map((r) => r.rel)).toEqual(["repo-a"]);

    sessionID.set("b");
    expect(get(snapshot).repos).toEqual([]);
  });
});
