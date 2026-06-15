<script lang="ts">
  import { onMount } from "svelte";
  import {
    sessionID, repos, activeRepo, changes, branch, loading, loadRepos, applySnapshot,
  } from "$lib/stores/scm";
  import { subscribeGitStatus } from "$lib/sse";
  import { stagePaths, unstagePaths, discardPaths, commit, loadCompare, loadCommitCompare, saveFile, langFor, type FileChange, type CompareData } from "$lib/git-actions";
  import { ToastHost, ConfirmDialog } from "@wick-fe/common-ui";
  import RepoSection from "$lib/components/RepoSection.svelte";
  import ChangesSection from "$lib/components/ChangesSection.svelte";
  import CommitBox from "$lib/components/CommitBox.svelte";
  import BranchBar from "$lib/components/BranchBar.svelte";
  import DiffModal from "$lib/components/DiffModal.svelte";
  import HistoryView from "$lib/components/HistoryView.svelte";
  import MonacoView from "$lib/components/MonacoView.svelte";
  import * as api from "$lib/api/scm";
  import { get } from "svelte/store";

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
  let noSession = $state(false);
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

  // Inline diff state (full mode only — sidebar mode uses DiffModal).
  let inlineData = $state<CompareData | null>(null);
  let inlineDirtyBuffer = $state<string | null>(null); // non-null = user edited
  let diffSideBySide = $state(false);
  const inlineLang = $derived(compare ? langFor(compare.file.path) : "plaintext");
  const inlineCompareKey = $derived(compare ? `${compare.commitSha ?? ""}|${compare.staged}|${compare.file.path}` : "");
  let inlineLoadedKey = "";

  $effect(() => {
    const key = inlineCompareKey;
    if (mode !== "full" || !compare) return;
    if (key === inlineLoadedKey) return;
    inlineLoadedKey = key;
    inlineData = null;
    inlineDirtyBuffer = null;
    const p = compare.commitSha
      ? loadCommitCompare(compare.commitSha, compare.file.path)
      : loadCompare(compare.file, compare.staged);
    p.then((d) => (inlineData = d)).catch(() => (inlineData = null));
  });

  async function saveInline() {
    if (!compare || inlineDirtyBuffer === null) return;
    busy = true;
    try {
      await saveFile(compare.file.path, inlineDirtyBuffer);
      const fresh = await loadCompare(compare.file, compare.staged);
      inlineData = { original: fresh.original, modified: inlineDirtyBuffer };
      inlineDirtyBuffer = null;
    } finally {
      busy = false;
    }
  }

  const staged = $derived($changes.filter((c) => c.staged));
  const unstaged = $derived($changes.filter((c) => c.unstaged || c.untracked));
  const allFiles = $derived([...staged, ...unstaged]);

  // Auto-select first file when list loads and nothing is selected.
  $effect(() => {
    if (mode !== "full") return;
    if (compare) return;
    const first = allFiles[0];
    if (first) compare = { file: first, staged: first.staged ?? false };
  });

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
      noSession = true;
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
          class="inline-flex lg:hidden h-7 w-7 items-center justify-center rounded-lg text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
        >
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"/></svg>
        </button>
      </div>
    </div>

    {#if noSession}
      <div class="flex flex-1 items-center justify-center px-6">
        <p class="text-center text-xs text-black-700 dark:text-black-600">Open this panel from a session page.</p>
      </div>
    {:else}
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
    {/if}
  </div>
{:else if noSession}
  <div class="flex h-full w-full items-center justify-center bg-white-100 dark:bg-navy-700 px-6">
    <p class="text-center text-xs text-black-700 dark:text-black-600">Open this panel from a session page.</p>
  </div>
{:else}
  <!-- Full mode: slim file list + diff editor as primary surface -->
  <div class="flex h-full w-full overflow-hidden">

    <!-- Left: file list (220px) -->
    <aside class="flex w-[220px] shrink-0 flex-col border-r border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700">

      <!-- Header -->
      <div class="flex items-center justify-between gap-1 px-3 py-2 border-b border-white-300 dark:border-navy-600">
        <div class="flex items-center gap-1.5 min-w-0">
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 text-green-500" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="4" cy="4" r="1.5"/><circle cx="4" cy="12" r="1.5"/><circle cx="12" cy="5" r="1.5"/><path d="M4 5.5v5M5.5 4H9a2 2 0 012 2v0" stroke-linecap="round"/></svg>
          {#if $repos.length > 1}
            <select
              value={$activeRepo}
              onchange={(e) => selectRepo((e.target as HTMLSelectElement).value)}
              class="min-w-0 flex-1 truncate bg-transparent text-[11px] font-medium text-black-900 dark:text-white-100 focus:outline-none cursor-pointer"
            >
              {#each $repos as r}<option value={r.rel}>{r.rel}</option>{/each}
            </select>
          {:else}
            <span class="truncate text-[11px] font-semibold text-black-900 dark:text-white-100">
              {$activeRepo || "Source Control"}
            </span>
          {/if}
        </div>
        <button type="button" onclick={() => loadRepos()} title="Refresh" class="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded text-black-600 hover:bg-white-200 dark:hover:bg-navy-800">
          <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 8a6 6 0 0110.5-4M14 8a6 6 0 01-10.5 4M11 2v3h3M5 14v-3H2" stroke-linecap="round" stroke-linejoin="round"/></svg>
        </button>
      </div>

      <!-- File list -->
      <div class="flex-1 overflow-y-auto py-1">
        {#if $loading && allFiles.length === 0}
          <p class="px-3 py-3 text-[11px] text-black-600">Loading…</p>
        {:else if allFiles.length === 0}
          <p class="px-3 py-3 text-[11px] text-black-600">No changes.</p>
        {:else}
          {#if staged.length > 0}
            <div class="px-2 pb-0.5 pt-2">
              <span class="text-[9px] font-semibold uppercase tracking-wider text-black-600">Staged ({staged.length})</span>
            </div>
            {#each staged as c (c.path)}
              {@const active = compare?.file.path === c.path && compare?.staged === true}
              <button
                type="button"
                onclick={() => openCompare(c.path, true)}
                class={"group flex w-full items-center gap-1.5 px-2 py-1 text-left transition-colors " + (active ? "bg-white-300 dark:bg-navy-600" : "hover:bg-white-200 dark:hover:bg-navy-800")}
              >
                <span class="min-w-0 flex-1 truncate font-mono text-[11px] text-black-800 dark:text-black-500">{c.path.split("/").pop()}</span>
                <span class="shrink-0 font-mono text-[9px] text-amber-600 dark:text-amber-400">{c.index}</span>
              </button>
            {/each}
          {/if}
          {#if unstaged.length > 0}
            <div class="px-2 pb-0.5 pt-2">
              <span class="text-[9px] font-semibold uppercase tracking-wider text-black-600">Changes ({unstaged.length})</span>
            </div>
            {#each unstaged as c (c.path)}
              {@const active = compare?.file.path === c.path && compare?.staged === false}
              <button
                type="button"
                onclick={() => openCompare(c.path, false)}
                class={"group flex w-full items-center gap-1.5 px-2 py-1 text-left transition-colors " + (active ? "bg-white-300 dark:bg-navy-600" : "hover:bg-white-200 dark:hover:bg-navy-800")}
              >
                <span class="min-w-0 flex-1 truncate font-mono text-[11px] text-black-800 dark:text-black-500">{c.path.split("/").pop()}</span>
                <span class="shrink-0 font-mono text-[9px] text-green-600 dark:text-green-400">{c.work_tree}</span>
              </button>
            {/each}
          {/if}
        {/if}
      </div>

      <!-- Commit box + branch -->
      {#if $activeRepo}
        <div class="border-t border-white-300 dark:border-navy-600">
          <CommitBox stagedCount={staged.length} {busy} onCommit={(m) => withBusy(() => commit(m)).then(() => true)} />
        </div>
      {/if}
      {#if $branch}<BranchBar branch={$branch} {busy} />{/if}
    </aside>

    <!-- Right: diff editor -->
    <main class="flex min-w-0 flex-1 flex-col overflow-hidden bg-white-200 dark:bg-navy-800">
      {#if compare && !compare.commitSha}
        <!-- Working-tree diff with action buttons -->
        <div class="flex h-full flex-col">
          <div class="flex items-center gap-2 border-b border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5">
            <span class="min-w-0 flex-1 truncate font-mono text-[11px] text-black-900 dark:text-white-100">{compare.file.path}</span>
            <!-- Save — appears only when user edits in the diff editor -->
            {#if inlineDirtyBuffer !== null}
              <button type="button" onclick={saveInline} disabled={busy} class="rounded bg-green-500 px-2 py-0.5 text-[11px] font-medium text-white-100 hover:bg-green-600 disabled:opacity-50">Save</button>
              <button type="button" onclick={() => (inlineDirtyBuffer = null)} class="rounded border border-white-300 dark:border-navy-600 px-2 py-0.5 text-[11px] text-black-700 dark:text-black-500 hover:bg-white-200 dark:hover:bg-navy-800">Revert edit</button>
            {/if}
            <!-- Discard (rollback) -->
            <button
              type="button"
              title="Discard changes"
              onclick={() => askDiscard([compare!.file.path], compare!.file.untracked ? [compare!.file.path] : [])}
              class="inline-flex items-center gap-1 rounded border border-white-300 dark:border-navy-600 px-2 py-0.5 text-[11px] text-black-700 dark:text-black-500 hover:border-red-400 hover:text-red-600 dark:hover:text-red-400 transition-colors"
            >
              <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M2 8a6 6 0 0110.5-4M11 2v3H8M14 8a6 6 0 01-10.5 4M5 14v-3h3" stroke-linecap="round" stroke-linejoin="round"/></svg>
              Discard
            </button>
            <!-- Stage / Unstage -->
            {#if compare.staged}
              <button
                type="button"
                onclick={() => withBusy(() => unstagePaths([compare!.file.path]))}
                disabled={busy}
                class="inline-flex items-center gap-1 rounded border border-white-300 dark:border-navy-600 px-2 py-0.5 text-[11px] text-black-700 dark:text-black-500 hover:border-amber-400 hover:text-amber-600 dark:hover:text-amber-400 transition-colors disabled:opacity-50"
              >
                <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.75"><path d="M3 8h10" stroke-linecap="round"/></svg>
                Unstage
              </button>
            {:else}
              <button
                type="button"
                onclick={() => withBusy(() => stagePaths([compare!.file.path]))}
                disabled={busy}
                class="inline-flex items-center gap-1 rounded border border-white-300 dark:border-navy-600 px-2 py-0.5 text-[11px] text-black-700 dark:text-black-500 hover:border-green-500 hover:text-green-600 dark:hover:text-green-400 transition-colors disabled:opacity-50"
              >
                <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.75"><path d="M8 3v10M3 8h10" stroke-linecap="round"/></svg>
                Stage
              </button>
            {/if}
            <!-- Side-by-side toggle -->
            <button
              type="button"
              title={diffSideBySide ? "Inline diff" : "Side-by-side diff"}
              onclick={() => (diffSideBySide = !diffSideBySide)}
              class={"inline-flex items-center gap-1 rounded border px-2 py-0.5 text-[11px] transition-colors " + (diffSideBySide ? "border-green-400 text-green-600 dark:text-green-400" : "border-white-300 dark:border-navy-600 text-black-600 hover:bg-white-200 dark:hover:bg-navy-800")}
            >
              <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M8 2v12M2 4h4M2 8h4M2 12h4M10 4h4M10 8h4M10 12h4" stroke-linecap="round"/></svg>
            </button>
          </div>
          <div class="min-h-0 flex-1">
            {#if inlineData}
              <MonacoView
                mode="diff"
                original={inlineData.original}
                modified={inlineDirtyBuffer ?? inlineData.modified}
                language={inlineLang}
                sideBySide={diffSideBySide}
                onDirty={(v) => (inlineDirtyBuffer = v)}
              />
            {:else}
              <div class="flex h-full items-center justify-center text-xs text-black-600">Loading…</div>
            {/if}
          </div>
        </div>
      {:else if compare && compare.commitSha}
        <!-- Commit history diff — read-only, no action buttons -->
        <div class="flex h-full flex-col">
          <div class="flex items-center gap-2 border-b border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5">
            <span class="min-w-0 flex-1 truncate font-mono text-[11px] text-black-900 dark:text-white-100">{compare.file.path}</span>
            <span class="shrink-0 font-mono text-[10px] text-black-600">at {compare.commitSha}</span>
            <button type="button" onclick={() => (compare = null)} class="inline-flex h-6 w-6 items-center justify-center rounded text-black-600 hover:bg-white-200 dark:hover:bg-navy-800">
              <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"/></svg>
            </button>
          </div>
          <div class="min-h-0 flex-1">
            <MonacoView mode="diff" original={inlineData?.original ?? ""} modified={inlineData?.modified ?? ""} language={inlineLang} />
          </div>
        </div>
      {:else}
        <div class="flex h-full items-center justify-center text-xs text-black-600">No changes.</div>
      {/if}
    </main>
  </div>
{/if}

{#if mode === "sidebar" && compare}
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
