<script lang="ts">
  import { onMount } from "svelte";
  import { apiGetSessions } from "$lib/api.js";
  import type { SessionsList } from "$lib/types.js";

  type Props = {
    base: string;
    /** Scope to one provider instance. Both omitted = all providers. */
    type?: string;
    name?: string;
    /** Open the session detail page for the clicked session. */
    onOpenSession: (sessionID: string) => void;
  };
  let { base, type, name, onOpenSession }: Props = $props();

  let scoped = $derived(!!type || !!name);

  let data = $state<SessionsList | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  let query = $state("");
  let page = $state(1);

  function shortID(id: string): string {
    return id.length > 8 ? id.slice(0, 8) : id;
  }

  async function load(): Promise<void> {
    loading = true;
    error = null;
    try {
      data = await apiGetSessions(base, { type, name, q: query.trim(), page });
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load sessions";
    } finally {
      loading = false;
    }
  }

  let debounce: ReturnType<typeof setTimeout> | null = null;
  function onSearchInput(): void {
    if (debounce) clearTimeout(debounce);
    debounce = setTimeout(() => {
      page = 1;
      void load();
    }, 300);
  }

  function goPage(p: number): void {
    if (p < 1) return;
    page = p;
    void load();
  }

  onMount(load);
</script>

<div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
  <div class="px-5 py-3 flex items-center gap-2 border-b border-white-300 dark:border-navy-600">
    <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Recent Sessions</h2>
    {#if data}
      <span class="rounded bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs font-medium text-black-700 dark:text-black-600">{data.Total}</span>
    {/if}
  </div>

  <div class="flex flex-wrap items-center gap-2 px-5 py-3 border-b border-white-300 dark:border-navy-600">
    <input
      type="text"
      bind:value={query}
      oninput={onSearchInput}
      placeholder="Search session / first message / pid / status"
      class="flex-1 min-w-[12rem] rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-1.5 text-xs text-black-900 dark:text-white-100"
    />
  </div>

  {#if loading && !data}
    <div class="px-5 py-8 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
  {:else if error}
    <div class="px-5 py-6 text-center text-sm text-error-400">{error}</div>
  {:else if (data?.Total ?? 0) === 0}
    <div class="px-5 py-8 text-center text-sm text-black-700 dark:text-black-600">No sessions recorded yet.</div>
  {:else}
    <table class="w-full text-xs">
      <thead>
        <tr class="border-b border-white-300 dark:border-navy-600 text-black-700 dark:text-black-600">
          <th class="px-5 py-2.5 text-left">Last activity</th>
          {#if !scoped}<th class="px-5 py-2.5 text-left">Provider</th>{/if}
          <th class="px-5 py-2.5 text-left">Session</th>
          <th class="px-5 py-2.5 text-left">Spawns</th>
          <th class="px-5 py-2.5 text-left">Last status</th>
          <th class="px-5 py-2.5 text-left">First Message</th>
        </tr>
      </thead>
      <tbody>
        {#each data?.Sessions ?? [] as s (s.SessionID)}
          <tr
            class="border-b border-white-300 dark:border-navy-600 last:border-0 hover:bg-white-200 dark:hover:bg-navy-800 cursor-pointer"
            onclick={() => onOpenSession(s.SessionID)}
          >
            <td class="px-5 py-2 font-mono text-black-700 dark:text-black-600 whitespace-nowrap">{new Date(s.LastStarted).toLocaleString()}</td>
            {#if !scoped}<td class="px-5 py-2 font-mono text-black-700 dark:text-black-600">{s.ProviderType}/{s.ProviderName}</td>{/if}
            <td class="px-5 py-2 font-mono text-black-900 dark:text-white-100">{shortID(s.SessionID)}</td>
            <td class="px-5 py-2 font-mono text-black-700 dark:text-black-600">{s.SpawnCount}</td>
            <td class="px-5 py-2">
              {#if !s.LastStatus}
                <span class="rounded bg-green-100 dark:bg-green-900 px-1.5 py-0.5 text-xs text-green-700 dark:text-green-300">running</span>
              {:else if s.LastStatus === "unclean"}
                <span class="rounded bg-red-100 dark:bg-red-900 px-1.5 py-0.5 text-xs text-red-700 dark:text-red-300">unclean exit</span>
              {:else if s.LastStatus === "error"}
                <span class="rounded bg-red-100 dark:bg-red-900 px-1.5 py-0.5 text-xs text-red-700 dark:text-red-300">error</span>
              {:else}
                <span class="rounded bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 text-xs text-black-700 dark:text-black-600">{s.LastStatus}</span>
              {/if}
            </td>
            <td class="px-5 py-2 text-black-700 dark:text-black-600 max-w-xs truncate">{s.FirstMessage}</td>
          </tr>
        {/each}
      </tbody>
    </table>
    {#if data && (data.Page > 1 || data.HasNext)}
      <div class="flex items-center justify-between border-t border-white-300 dark:border-navy-600 px-5 py-3">
        {#if data.Page > 1}
          <button onclick={() => goPage(data.Page - 1)} class="text-sm text-green-600 dark:text-green-400 hover:underline">← Prev</button>
        {:else}
          <span></span>
        {/if}
        <span class="text-xs text-black-700 dark:text-black-600">Page {data.Page}</span>
        {#if data.HasNext}
          <button onclick={() => goPage(data.Page + 1)} class="text-sm text-green-600 dark:text-green-400 hover:underline">Next →</button>
        {:else}
          <span></span>
        {/if}
      </div>
    {/if}
  {/if}
</div>
