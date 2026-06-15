<script lang="ts">
  import { ToastHost, ConfirmDialog } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { fetchOverview, killSession, dequeueSession } from "$lib/api.js";
  import type { QueuedEntry, ActiveEntry, OverviewStats } from "$lib/types.js";

  const base: string = (document.getElementById("app")?.dataset.base ?? "").replace(/\/$/, "");

  let queued = $state<QueuedEntry[]>([]);
  let activeSessions = $state<ActiveEntry[]>([]);
  let stats = $state<OverviewStats>({ active: 0, pool_max: 0, queue_len: 0 });
  let queueSearch = $state("");
  let selected = $state<Set<string>>(new Set());

  let confirmOpen = $state(false);
  let confirmTitle = $state("");
  let confirmBody = $state("");
  let confirmAction = $state<(() => Promise<void>) | null>(null);

  function shortID(id: string): string {
    return id.length > 11 ? `${id.slice(0, 4)}…${id.slice(-4)}` : id;
  }

  function fmtWait(ms: number): string {
    return `waiting ${Math.floor(ms / 1000)}s`;
  }

  const filteredQueue = $derived(
    queueSearch.trim() === ""
      ? queued
      : queued.filter((q) => {
          const needle = queueSearch.toLowerCase();
          return (
            q.session_id.toLowerCase().includes(needle) ||
            q.label.toLowerCase().includes(needle) ||
            q.project.toLowerCase().includes(needle)
          );
        }),
  );

  const allChecked = $derived(
    filteredQueue.length > 0 && filteredQueue.every((q) => selected.has(q.session_id)),
  );

  function toggleAll(): void {
    const next = new Set(selected);
    if (allChecked) {
      filteredQueue.forEach((q) => next.delete(q.session_id));
    } else {
      filteredQueue.forEach((q) => next.add(q.session_id));
    }
    selected = next;
  }

  function toggleOne(id: string): void {
    const next = new Set(selected);
    if (next.has(id)) {
      next.delete(id);
    } else {
      next.add(id);
    }
    selected = next;
  }

  function openConfirm(title: string, body: string, action: () => Promise<void>): void {
    confirmTitle = title;
    confirmBody = body;
    confirmAction = action;
    confirmOpen = true;
  }

  async function runConfirm(): Promise<void> {
    confirmOpen = false;
    if (!confirmAction) return;
    try {
      await confirmAction();
    } catch (e) {
      toastError("Error", String(e));
    }
    confirmAction = null;
  }

  function killSelected(): void {
    const ids = [...selected].filter((id) => queued.some((q) => q.session_id === id));
    if (ids.length === 0) return;
    openConfirm(
      `Kill ${ids.length} queued session${ids.length > 1 ? "s" : ""}?`,
      "Killed queue entries will not execute.",
      async () => {
        await Promise.all(ids.map((id) => dequeueSession(base, id)));
        selected = new Set();
        toastOk("Done", `Killed ${ids.length} queue entr${ids.length > 1 ? "ies" : "y"}.`);
      },
    );
  }

  function confirmDequeue(id: string): void {
    openConfirm(
      "Kill queue entry?",
      "This entry will be removed and will not execute.",
      () => dequeueSession(base, id).then(() => toastOk("Killed", `Queue entry ${shortID(id)} removed.`)),
    );
  }

  function confirmKill(id: string): void {
    openConfirm(
      "Kill session?",
      "The running session process will be terminated.",
      () => killSession(base, id).then(() => toastOk("Killed", `Session ${shortID(id)} killed.`)),
    );
  }

  function lifecycleBadgeClass(lc: string): string {
    switch (lc) {
      case "working":
        return "bg-blue-100 dark:bg-blue-900/40 text-blue-700 dark:text-blue-300";
      case "spawning":
        return "bg-amber-100 dark:bg-amber-900/40 text-amber-700 dark:text-amber-300";
      default:
        return "bg-slate-100 dark:bg-navy-600 text-slate-600 dark:text-slate-300";
    }
  }

  $effect(() => {
    let alive = true;

    async function poll(): Promise<void> {
      try {
        const r = await fetchOverview(base);
        if (!alive) return;
        queued = r.queued;
        activeSessions = r.active;
        stats = r.stats;
      } catch {
        /* silent — stale data is acceptable during polling */
      }
    }

    void poll();
    const timer = setInterval(() => void poll(), 3000);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  });
</script>

<div class="min-h-screen p-6 space-y-6">
  <ToastHost />

  <ConfirmDialog
    open={confirmOpen}
    title={confirmTitle}
    body={confirmBody}
    confirmLabel="Kill"
    destructive={true}
    onConfirm={runConfirm}
    onCancel={() => { confirmOpen = false; confirmAction = null; }}
  />

  <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">Overview</h1>

  <div class="grid grid-cols-2 gap-4 sm:grid-cols-3">
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5 shadow-sm">
      <p class="text-xs font-medium text-black-700 dark:text-black-600 uppercase tracking-wide">Active Slots</p>
      <p class="mt-1 text-3xl font-bold text-blue-600 dark:text-blue-400">{stats.active} / {stats.pool_max}</p>
    </div>
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5 shadow-sm">
      <p class="text-xs font-medium text-black-700 dark:text-black-600 uppercase tracking-wide">Queued</p>
      <p class="mt-1 text-3xl font-bold text-amber-600 dark:text-amber-400">{stats.queue_len}</p>
    </div>
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5 shadow-sm">
      <p class="text-xs font-medium text-black-700 dark:text-black-600 uppercase tracking-wide">Sessions</p>
      <p class="mt-1 text-3xl font-bold text-green-600 dark:text-green-400">{activeSessions.length}</p>
    </div>
  </div>

  {#if queued.length > 0}
    <div class="rounded-xl border border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-900/20 shadow-sm overflow-visible">
      <div class="border-b border-amber-300 dark:border-amber-700 px-5 py-3">
        <div class="flex items-center justify-between gap-3 flex-wrap">
          <div>
            <h2 class="text-sm font-semibold text-amber-800 dark:text-amber-300">Queue</h2>
            <p class="text-xs text-amber-700 dark:text-amber-400 mt-0.5">Sessions waiting for a slot. Kill to release — killed entries won't execute.</p>
          </div>
          <button
            type="button"
            disabled={selected.size === 0}
            onclick={killSelected}
            class="rounded-md bg-red-600 px-3 py-1.5 text-xs font-medium text-white-100 hover:bg-red-700 transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
          >Kill selected ({selected.size})</button>
        </div>
        <div class="mt-3 flex items-center gap-3">
          <label class="flex items-center gap-2 text-xs text-amber-800 dark:text-amber-300 cursor-pointer shrink-0">
            <input
              type="checkbox"
              checked={allChecked}
              onchange={toggleAll}
              class="h-3.5 w-3.5 accent-red-600 rounded cursor-pointer"
            />
            Select all
          </label>
          <div class="relative flex-1 min-w-0">
            <svg viewBox="0 0 16 16" class="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-amber-600 dark:text-amber-500 pointer-events-none" fill="none" stroke="currentColor" stroke-width="1.5">
              <circle cx="6.5" cy="6.5" r="4.5"></circle>
              <path d="M10.5 10.5l3 3" stroke-linecap="round"></path>
            </svg>
            <input
              type="text"
              bind:value={queueSearch}
              placeholder="Filter queue by chat, project, or id..."
              class="w-full rounded-md border border-amber-300 dark:border-amber-700 bg-white-100 dark:bg-navy-800 pl-8 pr-3 py-1.5 text-xs text-black-900 dark:text-white-100 placeholder-amber-600/70 dark:placeholder-amber-500/60 focus:border-amber-500 focus:outline-none"
            />
          </div>
        </div>
      </div>
      <ul class="divide-y divide-amber-200 dark:divide-amber-800 text-sm">
        {#each filteredQueue as q (q.session_id)}
          <li class="group flex items-center gap-3 px-5 py-2.5">
            <input
              type="checkbox"
              checked={selected.has(q.session_id)}
              onchange={() => toggleOne(q.session_id)}
              class="h-3.5 w-3.5 accent-red-600 rounded cursor-pointer shrink-0"
            />
            <a href={`${base}/sessions/${q.session_id}`} class="min-w-0 flex-1 hover:underline">
              {#if q.label}
                <span class="block truncate text-xs text-black-900 dark:text-white-100">{q.label}</span>
              {:else}
                <span class="block truncate font-mono text-xs text-black-900 dark:text-white-100">{shortID(q.session_id)}</span>
              {/if}
              <span class="block text-[11px] text-amber-700 dark:text-amber-400">
                {#if q.project}{q.project} · {/if}{fmtWait(q.waiting_ms)}
              </span>
            </a>
            <button
              type="button"
              onclick={() => confirmDequeue(q.session_id)}
              class="shrink-0 rounded-md border border-red-300 dark:border-red-800 px-2 py-0.5 text-xs text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20 transition-colors"
            >Kill</button>
          </li>
        {/each}
      </ul>
      {#if filteredQueue.length === 0}
        <p class="px-5 py-4 text-center text-xs text-amber-700 dark:text-amber-400">No queued sessions match your filter.</p>
      {/if}
    </div>
  {/if}

  <div class="space-y-2">
    <div class="flex items-center justify-between">
      <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Active Sessions</h2>
      <a href={`${base}/sessions`} class="text-xs text-green-600 dark:text-green-400 hover:underline">View all →</a>
    </div>
    {#if activeSessions.length === 0}
      <div class="flex flex-col items-center justify-center rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 py-16 text-center">
        <div class="mb-3 flex h-12 w-12 items-center justify-center rounded-full bg-white-200 dark:bg-navy-800">
          <svg viewBox="0 0 24 24" class="h-6 w-6 text-black-600 dark:text-black-700" fill="none" stroke="currentColor" stroke-width="1.5">
            <path d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z" stroke-linecap="round" stroke-linejoin="round"></path>
          </svg>
        </div>
        <p class="text-sm font-medium text-black-800 dark:text-black-600">No active sessions.</p>
      </div>
    {:else}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
        <ul class="divide-y divide-white-300 dark:divide-navy-600 text-sm">
          {#each activeSessions as entry (entry.session_id)}
            <li class="flex items-center gap-3 px-5 py-2.5">
              <a href={`${base}/sessions/${entry.session_id}`} class="min-w-0 flex-1 hover:underline">
                {#if entry.label}
                  <span class="block truncate text-xs text-black-900 dark:text-white-100">{entry.label}</span>
                {:else}
                  <span class="block truncate font-mono text-xs text-black-900 dark:text-white-100">{shortID(entry.session_id)}</span>
                {/if}
                {#if entry.pid}
                  <span class="block text-[11px] text-black-500 dark:text-black-600">pid {entry.pid}</span>
                {/if}
              </a>
              {#if entry.lifecycle}
                <span class={`shrink-0 rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide ${lifecycleBadgeClass(entry.lifecycle)}`}>
                  {entry.lifecycle}
                </span>
              {/if}
              <button
                type="button"
                onclick={() => confirmKill(entry.session_id)}
                class="shrink-0 rounded-md border border-red-300 dark:border-red-800 px-2 py-0.5 text-xs text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20 transition-colors"
              >Kill</button>
            </li>
          {/each}
        </ul>
      </div>
    {/if}
  </div>
</div>
