<script lang="ts">
  import { get } from "svelte/store";
  import * as api from "$lib/api/scm";
  import type { LogEntry, CommitDetail, FileChange } from "$lib/api/scm";
  import { sessionID, activeRepo } from "$lib/stores/scm";
  import { toastError } from "$lib/stores/toast";

  type Props = {
    onOpenCommitFile: (sha: string, file: FileChange) => void;
  };
  let { onOpenCommitFile }: Props = $props();

  let commits = $state<LogEntry[]>([]);
  let loading = $state(true);
  let expanded = $state<string | null>(null);
  let detail = $state<CommitDetail | null>(null);

  async function load() {
    loading = true;
    try {
      const r = await api.getLog(get(sessionID), get(activeRepo), 80);
      commits = r.commits;
    } catch (e) {
      toastError("History", String(e));
    } finally {
      loading = false;
    }
  }

  async function toggle(sha: string) {
    if (expanded === sha) {
      expanded = null;
      detail = null;
      return;
    }
    expanded = sha;
    detail = null;
    try {
      detail = await api.getCommit(get(sessionID), get(activeRepo), sha);
    } catch (e) {
      toastError("Commit", String(e));
    }
  }

  function statusColor(s: string): string {
    if (s === "A") return "text-green-600 dark:text-green-400";
    if (s === "D") return "text-cau-600 dark:text-cau-400";
    return "text-amber-600 dark:text-amber-400";
  }

  $effect(() => {
    // Reload when the active repo changes.
    void $activeRepo;
    load();
  });
</script>

<div class="flex-1 overflow-y-auto">
  {#if loading}
    <p class="p-4 text-xs text-black-700 dark:text-black-600">Loading history…</p>
  {:else if commits.length === 0}
    <p class="p-4 text-xs text-black-700 dark:text-black-600">No commits.</p>
  {:else}
    {#each commits as c (c.sha)}
      <div class="border-b border-white-300 dark:border-navy-600 last:border-0">
        <button
          type="button"
          onclick={() => toggle(c.sha)}
          class="w-full px-3 py-2 text-left hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
        >
          <div class="flex items-center gap-2">
            <span class="font-mono text-[10px] text-green-600 dark:text-green-400 shrink-0">{c.sha}</span>
            <span class="min-w-0 flex-1 truncate text-xs text-black-900 dark:text-white-100">{c.subject}</span>
          </div>
          <div class="mt-0.5 flex items-center gap-2 text-[10px] text-black-700 dark:text-black-600">
            <span class="truncate">{c.author}</span>
            <span>·</span>
            <span class="shrink-0">{c.rel_date}</span>
          </div>
        </button>
        {#if expanded === c.sha}
          <div class="bg-white-200 dark:bg-navy-800 px-3 py-2">
            {#if !detail}
              <p class="text-[11px] text-black-700 dark:text-black-600">Loading files…</p>
            {:else if detail.files.length === 0}
              <p class="text-[11px] text-black-700 dark:text-black-600">No file changes.</p>
            {:else}
              {#each detail.files as f (f.path)}
                <button
                  type="button"
                  onclick={() => onOpenCommitFile(c.sha, { path: f.path } as FileChange)}
                  class="flex w-full items-center gap-2 rounded px-1.5 py-1 text-left hover:bg-white-300 dark:hover:bg-navy-700"
                >
                  <span class={"shrink-0 font-mono text-[10px] " + statusColor(f.status)}>{f.status}</span>
                  <span class="min-w-0 flex-1 truncate text-[11px] text-black-800 dark:text-black-600">{f.path}</span>
                </button>
              {/each}
            {/if}
          </div>
        {/if}
      </div>
    {/each}
  {/if}
</div>
