<script lang="ts">
  import { onMount } from "svelte";
  import {
    sessionID, repos, activeRepo, changes, branch, loading, loadRepos, applySnapshot,
  } from "$lib/stores/scm";
  import { subscribeGitStatus } from "$lib/sse";
  import { toastError } from "$lib/stores/toast";
  import { stagePaths, unstagePaths, discardPaths, commit, type FileChange } from "$lib/git-actions";
  import ToastHost from "$lib/components/shared/ToastHost.svelte";
  import ConfirmDialog from "$lib/components/shared/ConfirmDialog.svelte";
  import RepoSection from "$lib/components/RepoSection.svelte";
  import ChangesSection from "$lib/components/ChangesSection.svelte";
  import CommitBox from "$lib/components/CommitBox.svelte";
  import BranchBar from "$lib/components/BranchBar.svelte";
  import DiffModal from "$lib/components/DiffModal.svelte";
  import HistoryView from "$lib/components/HistoryView.svelte";

  type Props = {
    sessionID?: string;
    mode?: "sidebar" | "full";
    pinned?: boolean;
    onPinToggle?: () => void;
    onClose?: () => void;
  };
  let {
    sessionID: sessionIDProp,
    mode = "full",
    pinned = false,
    onPinToggle,
    onClose,
  }: Props = $props();

  const VIEW_MODE_KEY = "wick.scm.viewMode";

  let busy = $state(false);
  // compare holds the file to diff. commitSha set → diff a past commit;
  // otherwise staged picks the HEAD↔index vs index↔working sides.
  let compare = $state<{ file: FileChange; staged: boolean; commitSha?: string } | null>(null);
  let view = $state<"changes" | "history">("changes");
  let viewMode = $state<"tree" | "list">(
    (typeof localStorage !== "undefined" && (localStorage.getItem(VIEW_MODE_KEY) as "tree" | "list")) || "tree",
  );
  // Folder expand/collapse state, keyed by folder path. Default expanded.
  let expanded = $state<Record<string, boolean>>({});
  // Pending discard confirmation.
  let discardAsk = $state<{ paths: string[]; untracked: string[]; label: string } | null>(null);

  const staged = $derived($changes.filter((c) => c.staged));
  const unstaged = $derived($changes.filter((c) => c.unstaged || c.untracked));

  function setViewMode(m: "tree" | "list") {
    viewMode = m;
    try { localStorage.setItem(VIEW_MODE_KEY, m); } catch { /* ignore */ }
  }
  function toggleDir(path: string) {
    expanded[path] = expanded[path] === false ? true : false;
  }

  onMount(() => {
    if (sessionIDProp) sessionID.set(sessionIDProp);
    if (!$sessionID) {
      toastError("No session", "Open this panel from a session page.");
      return;
    }
    // One HTTP fetch for the initial snapshot; afterwards every update is
    // a pushed git_status event carrying the full snapshot — no polling.
    void loadRepos();
    return subscribeGitStatus($sessionID, (snap) => applySnapshot(snap));
  });

  function selectRepo(rel: string) {
    // Switching repos is local — the snapshot already holds every repo's
    // status, so no fetch is needed.
    activeRepo.set(rel);
    compare = null;
  }
  // openCompare resolves a path to its FileChange in the right group.
  function openCompare(path: string, isStaged: boolean) {
    const list = isStaged ? staged : unstaged;
    const c = list.find((x) => x.path === path);
    if (c) compare = { file: c, staged: isStaged };
  }
  function openCommitFile(sha: string, file: FileChange) {
    compare = { file, staged: false, commitSha: sha };
  }
  async function withBusy(fn: () => Promise<unknown>) {
    busy = true;
    try { await fn(); } finally { busy = false; }
  }
  // Discard is destructive — stash the request and confirm first.
  function askDiscard(paths: string[], untracked: string[]) {
    if (paths.length === 0) return;
    const label = paths.length === 1 ? paths[0] : `${paths.length} files`;
    discardAsk = { paths, untracked, label };
  }
  async function confirmDiscard() {
    const d = discardAsk;
    discardAsk = null;
    if (d) await withBusy(() => discardPaths(d.paths, d.untracked));
  }
</script>

{#if mode === "sidebar"}
  <!-- Compact vertical dock: header → repo picker → commit → changes → branch -->
  <div class="flex h-full w-full flex-col bg-white-100 dark:bg-navy-700">
    <div class="flex items-center justify-between gap-2 border-b border-white-300 dark:border-navy-600 px-3 py-2.5">
      <div class="flex items-center gap-2 min-w-0">
        <svg viewBox="0 0 16 16" class="h-4 w-4 shrink-0 text-green-500" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="4" cy="4" r="1.5"/><circle cx="4" cy="12" r="1.5"/><circle cx="12" cy="5" r="1.5"/><path d="M4 5.5v5M5.5 4H9a2 2 0 012 2v0" stroke-linecap="round"/></svg>
        <h2 class="truncate text-sm font-semibold text-black-900 dark:text-white-100">Source Control</h2>
      </div>
      <div class="flex shrink-0 items-center gap-0.5">
        <!-- View mode: tree / list -->
        <button
          type="button"
          onclick={() => setViewMode(viewMode === "tree" ? "list" : "tree")}
          title={viewMode === "tree" ? "View as list" : "View as tree"}
          class="inline-flex h-7 w-7 items-center justify-center rounded-lg text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
        >
          {#if viewMode === "tree"}
            <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 4h12M2 8h12M2 12h12" stroke-linecap="round"/></svg>
          {:else}
            <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M3 4h3M3 8h6M6 8v4h3M3 4v8" stroke-linecap="round" stroke-linejoin="round"/></svg>
          {/if}
        </button>
        <!-- Pin only makes sense for the desktop push dock; on mobile the
             panel is a full-screen overlay, so hide the pin below lg. -->
        <button
          type="button"
          onclick={() => onPinToggle?.()}
          title={pinned ? "Unpin" : "Pin to sidebar"}
          class={"hidden lg:inline-flex h-7 w-7 items-center justify-center rounded-lg transition-colors " + (pinned ? "text-green-600 dark:text-green-400 bg-green-100 dark:bg-green-900/30" : "text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800")}
        >
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill={pinned ? "currentColor" : "none"} stroke="currentColor" stroke-width="1.3"><path d="M10 2l4 4-2 .5-2.5 2.5L9 12l-1 1-3-3 1-1 3-.5L11.5 6 12 4z" stroke-linejoin="round"/><path d="M5 11L2 14" stroke-linecap="round"/></svg>
        </button>
        <button
          type="button"
          onclick={() => onClose?.()}
          title="Close"
          class="inline-flex h-7 w-7 items-center justify-center rounded-lg text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
        >
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"/></svg>
        </button>
      </div>
    </div>

    <!-- Repositories section (collapsible). Hidden when only one repo. -->
    {#if $repos.length > 1}
      <RepoSection repos={$repos} activeRepo={$activeRepo} onSelect={selectRepo} />
    {:else if $repos.length === 0}
      <p class="px-3 py-3 text-xs text-black-700 dark:text-black-600">No git repos in this session's working directory.</p>
    {/if}

    {#if $activeRepo}
      <!-- Changes | History tabs -->
      <div class="flex border-b border-white-300 dark:border-navy-600 text-xs">
        <button
          type="button"
          onclick={() => (view = "changes")}
          class={"flex-1 px-3 py-2 font-medium transition-colors " + (view === "changes" ? "text-green-600 dark:text-green-400 border-b-2 border-green-500" : "text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800")}
        >Changes</button>
        <button
          type="button"
          onclick={() => (view = "history")}
          class={"flex-1 px-3 py-2 font-medium transition-colors " + (view === "history" ? "text-green-600 dark:text-green-400 border-b-2 border-green-500" : "text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800")}
        >History</button>
      </div>

      {#if view === "changes"}
        <CommitBox stagedCount={staged.length} {busy} onCommit={(m) => withBusy(() => commit(m)).then(() => true)} />
        <div class="flex-1 overflow-y-auto py-1">
          {#if $loading && staged.length === 0 && unstaged.length === 0}
            <p class="p-4 text-xs text-black-700 dark:text-black-600">Loading…</p>
          {:else if staged.length === 0 && unstaged.length === 0}
            <p class="p-4 text-xs text-black-700 dark:text-black-600">No changes.</p>
          {:else}
            <ChangesSection
              title="Staged Changes" items={staged} staged={true}
              {viewMode} {expanded} onToggleDir={toggleDir} onOpen={openCompare}
              onAction={(p) => withBusy(() => unstagePaths(p))}
              onDiscard={askDiscard} actionIcon="unstage"
            />
            <ChangesSection
              title="Changes" items={unstaged} staged={false}
              {viewMode} {expanded} onToggleDir={toggleDir} onOpen={openCompare}
              onAction={(p) => withBusy(() => stagePaths(p))}
              onDiscard={askDiscard} actionIcon="stage"
            />
          {/if}
        </div>
      {:else}
        <HistoryView onOpenCommitFile={openCommitFile} />
      {/if}

      {#if $branch}
        <BranchBar branch={$branch} {busy} />
      {/if}
    {/if}
  </div>
{:else}
  <!-- Full mode: repo rail + changes + (compare opens modal) -->
  <div class="flex h-full w-full overflow-hidden">
    <aside class="flex w-64 shrink-0 flex-col border-r border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
      <div class="flex items-center justify-between px-4 py-3 border-b border-white-300 dark:border-navy-600">
        <h1 class="text-sm font-semibold text-black-900 dark:text-white-100">Source Control</h1>
        <button type="button" onclick={() => loadRepos()} title="Refresh" aria-label="Refresh" class="inline-flex h-7 w-7 items-center justify-center rounded-lg text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 8a6 6 0 0110.5-4M14 8a6 6 0 01-10.5 4M11 2v3h3M5 14v-3H2" stroke-linecap="round" stroke-linejoin="round"/></svg>
        </button>
      </div>
      <div class="flex-1 overflow-y-auto">
        <RepoSection repos={$repos} activeRepo={$activeRepo} onSelect={selectRepo} />
      </div>
      {#if $branch}<BranchBar branch={$branch} {busy} />{/if}
    </aside>
    <section class="flex w-96 shrink-0 flex-col border-r border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
      {#if $activeRepo}
        <CommitBox stagedCount={staged.length} {busy} onCommit={(m) => withBusy(() => commit(m)).then(() => true)} />
        <div class="flex-1 overflow-y-auto py-1">
          {#if staged.length === 0 && unstaged.length === 0}
            <p class="p-4 text-xs text-black-700 dark:text-black-600">No changes.</p>
          {:else}
            <ChangesSection title="Staged Changes" items={staged} staged={true} {viewMode} {expanded} onToggleDir={toggleDir} onOpen={openCompare} onAction={(p) => withBusy(() => unstagePaths(p))} onDiscard={askDiscard} actionIcon="unstage" />
            <ChangesSection title="Changes" items={unstaged} staged={false} {viewMode} {expanded} onToggleDir={toggleDir} onOpen={openCompare} onAction={(p) => withBusy(() => stagePaths(p))} onDiscard={askDiscard} actionIcon="stage" />
          {/if}
        </div>
      {/if}
    </section>
    <main class="flex min-w-0 flex-1 items-center justify-center bg-white-200 dark:bg-navy-800 text-xs text-black-700 dark:text-black-600">
      Select a file to compare.
    </main>
  </div>
{/if}

{#if compare}
  <DiffModal file={compare.file} staged={compare.staged} commitSha={compare.commitSha} onClose={() => (compare = null)} />
{/if}

<ConfirmDialog
  open={!!discardAsk}
  title="Discard changes?"
  body={discardAsk ? `Discard changes to ${discardAsk.label}? This cannot be undone.` : ""}
  confirmLabel="Discard"
  cancelLabel="Cancel"
  destructive={true}
  onConfirm={confirmDiscard}
  onCancel={() => (discardAsk = null)}
/>

<ToastHost />
