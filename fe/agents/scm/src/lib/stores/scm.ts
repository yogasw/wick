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
export const snapshot = writable<GitStatusSnapshot>({
  repos: [],
  statuses: {},
  active: "",
  active_explicit: false,
  total_changed: 0,
});
// Which repo is selected is remembered PER SESSION. The id has to come
// from the sessionID store, not from the URL: mounted as an island in the
// conversation shell there is no ?session= to read, so every session fell
// back to the same empty key — one global selection that made a freshly
// opened session jump to whatever repo the last one had picked.
function repoKey(id: string): string { return `wick.scm.activeRepo.${id}`; }
function readStoredRepo(id: string): string {
  if (!id) return "";
  try { return localStorage.getItem(repoKey(id)) ?? ""; } catch { return ""; }
}

// The island mounts before it is told its session (the prop lands in
// onMount), so the first value is read under an id we may not have yet;
// re-hydrate from the right key the moment the session is known.
let currentSession = get(sessionID);
export const activeRepo = writable<string>(readStoredRepo(currentSession));
sessionID.subscribe((id) => {
  if (id === currentSession) return;
  currentSession = id;
  activeRepo.set(readStoredRepo(id));
});
// hydrating suppresses the server write while a value is being applied
// FROM the server or from a snapshot default — otherwise reading the
// selection would immediately write it back.
let hydrating = false;

activeRepo.subscribe((v) => {
  // No session yet = no key to write under. Persisting here is what
  // created the shared global selection in the first place.
  if (currentSession) {
    try { localStorage.setItem(repoKey(currentSession), v); } catch { /* ignore */ }
    // The selection also lives on the session server-side, because the
    // AGENT reads it: it is what its system prompt names as the repo
    // being worked on and what wick_scm reports. localStorage alone
    // would leave the two sides describing different repos.
    if (!hydrating && v) {
      void api.setActiveRepo(currentSession, v).catch(() => { /* panel still works */ });
    }
  }
  // The SCM panel is a separate bundle from the conversation shell that
  // draws the Source rail badge. localStorage writes don't fire `storage`
  // in the tab that made them, so announce the switch — otherwise the
  // badge keeps counting a repo the user is no longer looking at.
  try {
    window.dispatchEvent(new CustomEvent("wick:scm-active-repo", { detail: v }));
  } catch { /* ignore */ }
});
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

// applySnapshot replaces the snapshot and keeps activeRepo valid. A
// stored selection that no longer exists (repo removed) falls back to
// the first repo — the same rule the server applies.
export function applySnapshot(s: GitStatusSnapshot): void {
  snapshot.set(s);
  const cur = get(activeRepo);
  const known = s.repos.some((r) => r.rel === cur);
  if (!cur || !known) {
    // Adopt the session's selection; the server already applied the
    // same "first repo when nothing is picked" fallback.
    setActiveRepoQuietly(s.active || s.repos[0]?.rel || "");
    return;
  }
  // Somebody else moved the selection — the agent via the Source
  // connector, or this session in another tab. Follow it.
  if (s.active_explicit && s.active && s.active !== cur) {
    setActiveRepoQuietly(s.active);
  }
}

// setActiveRepoQuietly applies a value without echoing it back to the
// server — for values that CAME from the server, or from the fallback
// rule the server already applies itself.
function setActiveRepoQuietly(rel: string): void {
  hydrating = true;
  try {
    activeRepo.set(rel);
  } finally {
    hydrating = false;
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
