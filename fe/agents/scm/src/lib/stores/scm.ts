import { writable, derived, get } from "svelte/store";
import * as api from "$lib/api/scm";
import type { RepoSummary, FileChange, BranchInfo, StatusResult, GitStatusSnapshot } from "$lib/api/scm";

// Session id from ?session=<id>. The whole SPA is scoped to one session.
export function readSessionID(): string {
  const u = new URL(window.location.href);
  return u.searchParams.get("session") ?? "";
}

export const sessionID = writable<string>(readSessionID());

// The full snapshot is the single source of truth. It arrives once via
// HTTP on first load, then is replaced wholesale by each git_status SSE
// event — so there is no per-change fetch (zero polling).
export const snapshot = writable<GitStatusSnapshot>({ repos: [], statuses: {}, total_changed: 0 });
export const activeRepo = writable<string>(""); // RepoSummary.rel
export const loading = writable<boolean>(false);

// Derived views the components bind to.
export const repos = derived(snapshot, ($s) => $s.repos);
export const activeStatus = derived(
  [snapshot, activeRepo],
  ([$s, $r]): StatusResult | null => $s.statuses[$r] ?? null,
);
export const changes = derived(activeStatus, ($st): FileChange[] => $st?.changes ?? []);
export const branch = derived(activeStatus, ($st): BranchInfo | null => $st?.branch ?? null);

// Selected file for the compare view.
export type Selection = { path: string; staged: boolean; untracked: boolean };
export const selection = writable<Selection | null>(null);

// applySnapshot replaces the snapshot and keeps activeRepo valid.
export function applySnapshot(s: GitStatusSnapshot): void {
  snapshot.set(s);
  const cur = get(activeRepo);
  if (!cur || !s.repos.some((r) => r.rel === cur)) {
    activeRepo.set(s.repos[0]?.rel ?? "");
  }
}

// loadRepos is the one HTTP fetch (initial load + post-mutation nudge).
export async function loadRepos(): Promise<void> {
  const id = get(sessionID);
  if (!id) return;
  loading.set(true);
  try {
    applySnapshot(await api.getRepos(id));
  } finally {
    loading.set(false);
  }
}

// loadStatus is kept as an alias so action code reads naturally; it just
// refreshes the whole snapshot (cheap — one request, and the SSE event
// will confirm shortly after anyway).
export const loadStatus = loadRepos;

export type { RepoSummary };
