<script lang="ts">
  import { untrack } from "svelte";
  import { get } from "svelte/store";
  import * as api from "$lib/api/scm";
  import type { LogEntry, CommitDetail, FileChange, HistoryRef } from "$lib/api/scm";
  import { sessionID, activeRepo } from "$lib/stores/scm";
  import { buildGraph, graphWidth } from "$lib/graph";
  import GraphRail from "$lib/components/GraphRail.svelte";
  import RefPicker from "$lib/components/RefPicker.svelte";
  import { toastError } from "@wick-fe/common-stores";

  type Props = {
    onOpenCommitFile: (sha: string, file: FileChange) => void;
  };
  let { onOpenCommitFile }: Props = $props();

  let commits = $state<LogEntry[]>([]);
  let refs = $state<HistoryRef[]>([]);
  let trunk = $state("");
  let loading = $state(true);
  let expanded = $state<string | null>(null);
  let detail = $state<CommitDetail | null>(null);

  // Which references to walk. [] = auto (checked-out branch + upstream),
  // ["all"] = everything, otherwise the named refs. Remembered per repo:
  // "show me all branches" is a property of the repo you are reading, not
  // of the panel, and re-picking it on every switch got old fast.
  let selectedRefs = $state<string[]>([]);
  const refsKey = (repo: string) => `wick.scm.graphRefs.${repo}`;
  function readRefs(repo: string): string[] {
    try {
      const raw = localStorage.getItem(refsKey(repo));
      return raw ? (JSON.parse(raw) as string[]) : [];
    } catch {
      return [];
    }
  }
  function writeRefs(repo: string, v: string[]): void {
    try {
      localStorage.setItem(refsKey(repo), JSON.stringify(v));
    } catch {
      /* selection just won't persist */
    }
  }

  async function load() {
    loading = true;
    try {
      const id = get(sessionID);
      const repo = get(activeRepo);
      const [log, refList] = await Promise.all([
        api.getLog(id, repo, 80, selectedRefs),
        // The picker's contents, not the history itself — a failure here
        // must not empty the graph.
        api.getHistoryRefs(id, repo).catch(() => ({ refs: [], trunk: "" })),
      ]);
      commits = log.commits;
      refs = refList.refs;
      trunk = refList.trunk;
    } catch (e) {
      toastError("History", String(e));
    } finally {
      loading = false;
    }
  }

  function pickRefs(next: string[]) {
    selectedRefs = next;
    writeRefs(get(activeRepo), next);
    void load();
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

  // The three states a commit can be in, spelled out. "local" is the one
  // that matters most — it is the work that exists nowhere else yet.
  const STATE_LABEL: Record<string, string> = {
    local: "local",
    pushed: "pushed",
    trunk: "merged",
  };
  function stateClass(state: string | undefined): string {
    if (state === "local") return "border-amber-500/60 text-amber-600 dark:text-amber-400";
    if (state === "pushed") return "border-link-400/60 text-link-400";
    return "border-green-500/50 text-green-600 dark:text-green-400";
  }
  function stateTitle(state: string | undefined): string {
    if (state === "local") return "Committed here only — not on any remote branch yet";
    if (state === "pushed") return `Pushed to its remote branch, not yet on ${trunk || "the trunk"}`;
    return `Merged into ${trunk || "the trunk"}`;
  }

  const rows = $derived(buildGraph(commits.map((c) => ({ sha: c.sha, parents: c.parents ?? [] }))));
  const lanes = $derived(graphWidth(rows));
  // How much of the local work is unpushed, for the header. A count is
  // what makes "you have not pushed" impossible to scroll past.
  // A label only where the state CHANGES. Thirteen rows each stamped
  // "local" does not say more than one does — it says it twelve times over
  // the subject line you were trying to read. Marking the newest commit of
  // each run reads as "from here up", which is the same thing an editor's
  // branch marker means.
  function showState(i: number): boolean {
    const st = commits[i]?.state;
    if (st !== "local" && st !== "pushed") return false;
    return commits[i - 1]?.state !== st;
  }

  const localCount = $derived(commits.filter((c) => c.state === "local").length);
  const pushedCount = $derived(commits.filter((c) => c.state === "pushed").length);

  $effect(() => {
    // Reload when the active repo changes, picking up that repo's saved
    // reference selection first.
    const repo = $activeRepo;
    // untrack, or the effect re-runs itself forever: it WRITES selectedRefs
    // and load() READS it synchronously (before its first await), so the
    // write invalidates the very effect that made it. readRefs hands back a
    // fresh array each time, so the identity never settles either — the
    // panel sat on "Loading history…" while re-firing /git/log + /git/refs
    // in a loop.
    untrack(() => {
      selectedRefs = readRefs(repo);
      load();
    });
  });
</script>

<div class="flex flex-1 flex-col overflow-hidden">
  <div class="flex items-center gap-2 border-b border-white-300 dark:border-navy-600 px-3 py-1.5">
    <span class="text-[10px] font-medium uppercase tracking-wide text-black-700 dark:text-black-600">Graph</span>
    <!-- The per-row badges collapsed to one line: what is not yet out of this
         clone, what is out but not landed, and where the trunk is. -->
    {#if localCount > 0}
      <span
        class="flex shrink-0 items-center gap-1 rounded border border-amber-500/60 px-1 text-[9px] text-amber-600 dark:text-amber-400"
        title="Commits that exist only in this clone"
      >
        <svg viewBox="0 0 16 16" class="h-2.5 w-2.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M8 11V3M5 6l3-3 3 3" stroke-linecap="round" stroke-linejoin="round"/><path d="M3 13h10" stroke-linecap="round"/></svg>
        {localCount} unpushed
      </span>
    {/if}
    {#if pushedCount > 0}
      <span
        class="flex shrink-0 items-center gap-1 rounded border border-link-400/60 px-1 text-[9px] text-link-400"
        title={`Pushed to their remote branch, not yet on ${trunk || "the trunk"}`}
      >
        <svg viewBox="0 0 16 16" class="h-2.5 w-2.5" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M4.5 12a3 3 0 01-.3-6A4 4 0 0112 6.5a2.75 2.75 0 01-.25 5.5z" stroke-linejoin="round"/></svg>
        {pushedCount} pushed
      </span>
    {/if}
    {#if trunk}
      <span class="flex shrink-0 items-center gap-1 text-[9px] text-black-600 dark:text-black-700" title="Commits below this are merged">
        <svg viewBox="0 0 16 16" class="h-2.5 w-2.5" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="4" cy="4" r="1.5"/><circle cx="4" cy="12" r="1.5"/><circle cx="12" cy="5" r="1.5"/><path d="M4 5.5v5M5.5 4H9a2 2 0 012 2v0" stroke-linecap="round"/></svg>
        {trunk}
      </span>
    {/if}
    <span class="flex-1"></span>
    <RefPicker {refs} {trunk} selected={selectedRefs} onchange={pickRefs} />
  </div>

  <div class="flex-1 overflow-y-auto">
    {#if loading}
      <p class="p-4 text-xs text-black-700 dark:text-black-600">Loading history…</p>
    {:else if commits.length === 0}
      <p class="p-4 text-xs text-black-700 dark:text-black-600">No commits.</p>
    {:else}
      {#each commits as c, i (c.sha)}
        <div class="border-b border-white-300 dark:border-navy-600 last:border-0">
          <div class="flex items-stretch">
            <!-- The rail sits outside the button so its lanes stay
                 continuous down the list rather than restarting per row. -->
            <GraphRail row={rows[i]} {lanes} />
            <button
              type="button"
              onclick={() => toggle(c.sha)}
              class="min-w-0 flex-1 px-2 py-1 text-left hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
              style="height:44px"
            >
              <div class="flex items-center gap-1.5">
                <span class="min-w-0 flex-1 truncate text-xs text-black-900 dark:text-white-100">{c.subject}</span>
                <!-- Only the states worth interrupting for. "merged" is what
                     almost every row is, so a pill saying so on every line is
                     noise that hides the two rows that differ; and the ref
                     names (HEAD, master, origin/master) repeated down the
                     column crowd out the subject, which is what you actually
                     read. Both now live once, in the header. -->
                {#if showState(i)}
                  <span
                    class={"shrink-0 rounded border px-1 text-[9px] " + stateClass(c.state)}
                    title={stateTitle(c.state)}
                  >{STATE_LABEL[c.state ?? ""] ?? ""}</span>
                {/if}
              </div>
              <div class="mt-0.5 flex items-center gap-2 text-[10px] text-black-700 dark:text-black-600">
                <span class="font-mono text-green-600 dark:text-green-400">{c.sha}</span>
                <span class="truncate">{c.author}</span>
                <span>·</span>
                <span class="shrink-0">{c.rel_date}</span>
              </div>
            </button>
          </div>
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
</div>
