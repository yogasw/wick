<script lang="ts">
  import MonacoView from "$lib/components/MonacoView.svelte";
  import { loadCompare, loadCommitCompare, saveFile, langFor, type CompareData, type FileChange } from "$lib/git-actions";
  import * as api from "$lib/api/scm";
  import { get } from "svelte/store";
  import { sessionID, activeRepo } from "$lib/stores/scm";

  type Props = {
    file: FileChange;
    // staged side selection for working-tree changes (ignored for commits).
    staged?: boolean;
    // When set, compare a file inside a past commit (read-only, no edit).
    commitSha?: string;
    onClose: () => void;
  };
  let { file, staged = false, commitSha, onClose }: Props = $props();

  let data = $state<CompareData | null>(null);
  let editMode = $state(false);
  let editBuffer = $state("");
  let busy = $state(false);

  const lang = $derived(langFor(file.path));
  const readOnlyHistory = $derived(!!commitSha);

  // Identity of the thing being compared. We reload ONLY when this key
  // changes — not on every prop-object churn. A new snapshot from SSE
  // hands ScmPanel a fresh `file` object with the same path/staged, which
  // must NOT reload (it would drop edit mode + steal focus mid-edit).
  const compareKey = $derived(`${commitSha ?? ""}|${staged}|${file.path}`);
  let loadedKey = "";

  $effect(() => {
    const key = compareKey;
    if (key === loadedKey) return; // same target — leave editor untouched
    loadedKey = key;
    data = null;
    editMode = false;
    const p = commitSha ? loadCommitCompare(commitSha, file.path) : loadCompare(file, staged);
    p.then((d) => (data = d)).catch(() => (data = null));
  });

  async function startEdit() {
    const r = await api.getFile(get(sessionID), get(activeRepo), file.path);
    editBuffer = r.content;
    editMode = true;
  }
  async function save() {
    busy = true;
    try {
      await saveFile(file.path, editBuffer);
      editMode = false;
      data = await loadCompare(file, staged);
    } finally {
      busy = false;
    }
  }
</script>

<svelte:window onkeydown={(e) => e.key === "Escape" && onClose()} />

<div
  class="fixed inset-0 z-[60] flex items-center justify-center bg-black/60 backdrop-blur-sm p-4"
  role="presentation"
  onclick={(e) => { if (e.target === e.currentTarget) onClose(); }}
>
  <div class="flex h-[90vh] w-full max-w-6xl flex-col overflow-hidden rounded-2xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-2xl">
    <div class="flex items-center justify-between gap-3 border-b border-white-300 dark:border-navy-600 px-4 py-2.5">
      <div class="min-w-0 flex-1">
        <p class="truncate font-mono text-xs text-black-900 dark:text-white-100">{file.path}</p>
        {#if commitSha}
          <p class="text-[10px] text-black-700 dark:text-black-600">at commit {commitSha}</p>
        {/if}
      </div>
      <div class="flex shrink-0 items-center gap-1">
        {#if !readOnlyHistory}
          {#if editMode}
            <button type="button" onclick={save} disabled={busy} class="rounded-lg bg-green-500 px-2.5 py-1 text-[11px] font-medium text-white-100 hover:bg-green-600 disabled:opacity-50">Save</button>
            <button type="button" onclick={() => (editMode = false)} class="rounded-lg border border-white-300 dark:border-navy-600 px-2.5 py-1 text-[11px] text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800">Cancel</button>
          {:else}
            <button type="button" onclick={startEdit} class="rounded-lg border border-white-300 dark:border-navy-600 px-2.5 py-1 text-[11px] text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800">Edit</button>
          {/if}
        {/if}
        <button type="button" onclick={onClose} title="Close" class="inline-flex h-7 w-7 items-center justify-center rounded-lg text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800">
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"/></svg>
        </button>
      </div>
    </div>
    <div class="min-h-0 flex-1">
      {#if editMode}
        <MonacoView mode="edit" modified={editBuffer} language={lang} onChange={(v) => (editBuffer = v)} />
      {:else if data}
        <MonacoView mode="diff" original={data.original} modified={data.modified} language={lang} />
      {:else}
        <p class="p-4 text-xs text-black-700 dark:text-black-600">Loading…</p>
      {/if}
    </div>
  </div>
</div>
