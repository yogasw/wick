<script lang="ts">
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { fetchProcessesE } from "$lib/api.js";
  import { humanBytes, humanBps, humanPct } from "$lib/format.js";
  import type { ProcessListResponse } from "$lib/types.js";

  interface Props {
    base: string;
  }
  let { base }: Props = $props();

  // Grouped by executable name by default: a browser is a dozen processes,
  // and "chrome.exe × 12 — 2.1 GB" answers a question that twelve separate
  // 180 MB rows do not.
  let data = $state<ProcessListResponse | null>(null);
  let query = $state("");
  let sort = $state<"mem" | "cpu" | "io">("mem");
  let page = $state(1);
  let loading = $state(false);
  let error = $state("");
  let expanded = $state<Set<string>>(new Set());

  async function load(): Promise<void> {
    loading = true;
    try {
      data = await Effect.runPromise(
        fetchProcessesE(base, { q: query, sort, page }).pipe(Effect.provide(WickClientLayer)),
      );
      error = "";
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  // Debounce typing: one request per pause, not one per keystroke.
  let debounce: ReturnType<typeof setTimeout> | null = null;
  function onSearch(v: string): void {
    query = v;
    page = 1; // a new search starts at the first page, not wherever we were
    if (debounce) clearTimeout(debounce);
    debounce = setTimeout(() => void load(), 250);
  }

  function setSort(s: "mem" | "cpu" | "io"): void {
    sort = s;
    page = 1;
    void load();
  }

  function goPage(n: number): void {
    page = n;
    void load();
  }

  function toggle(name: string): void {
    const next = new Set(expanded);
    if (next.has(name)) {
      next.delete(name);
    } else {
      next.add(name);
    }
    expanded = next;
  }

  // Load once when the section is first shown.
  let started = false;
  $effect(() => {
    if (!started) {
      started = true;
      void load();
    }
  });

  const sortLabel: Record<string, string> = { mem: "Memory", cpu: "CPU", io: "Disk" };

  // Share for an individual member, against the same denominator the group
  // row uses — so an expanded row can be compared with its parent without
  // mental arithmetic. Empty when the machine's memory is unknown rather
  // than showing a fabricated 0%.
  function report_pct(bytes: number): string {
    const total = data?.machine_mem_bytes ?? 0;
    if (total <= 0) return "—";
    return `${((bytes / total) * 100).toFixed(1)}%`;
  }
</script>

<div class="overflow-hidden rounded-xl border border-white-300 bg-white-100 shadow-sm dark:border-navy-600 dark:bg-navy-700">
  <div class="flex flex-wrap items-center justify-between gap-3 border-b border-white-300 px-5 py-4 dark:border-navy-600">
    <div>
      <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">All processes</h2>
      {#if data?.available}
        <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">
          {#if query}
            {data.matched} matching group{data.matched === 1 ? "" : "s"} of {data.total} processes
          {:else}
            {data.total} processes, grouped by name
          {/if}
        </p>
      {/if}
    </div>

    <div class="flex flex-wrap items-center gap-2">
      <input
        type="search"
        placeholder="Search by name…"
        value={query}
        oninput={(e) => onSearch(e.currentTarget.value)}
        class="w-44 rounded-lg border border-white-300 bg-white-100 px-2.5 py-1 text-xs text-black-900 placeholder:text-black-600 focus:border-blue-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
      />
      <div class="flex overflow-hidden rounded-lg border border-white-300 dark:border-navy-600">
        {#each ["mem", "cpu", "io"] as const as s (s)}
          <button
            type="button"
            class="px-2.5 py-1 text-xs transition-colors {sort === s
              ? 'bg-blue-600 text-white-100'
              : 'bg-white-100 text-black-700 hover:bg-white-200 dark:bg-navy-700 dark:text-black-600 dark:hover:bg-navy-600'}"
            onclick={() => setSort(s)}
          >
            {sortLabel[s]}
          </button>
        {/each}
      </div>
    </div>
  </div>

  {#if error}
    <p class="px-5 py-6 text-center text-xs text-red-600 dark:text-red-400">{error}</p>
  {:else if data && !data.available}
    <p class="px-5 py-6 text-center text-xs text-black-700 dark:text-black-600">
      Process listing is unavailable on this platform.
    </p>
  {:else if data && data.groups.length === 0}
    <p class="px-5 py-6 text-center text-xs text-black-700 dark:text-black-600">
      {query ? `Nothing matches “${query}”.` : "No processes to show."}
    </p>
  {:else if data}
    <div class="overflow-x-auto">
      <table class="w-full table-fixed text-sm">
        <!-- Fixed widths, repeated on the nested member table below, so the
             expanded rows line up with the group row above them. Without
             this each table sizes its own columns and the numbers stagger. -->
        <colgroup>
          <col style="width: 38%" />
          <col style="width: 30%" />
          <col style="width: 16%" />
          <col style="width: 16%" />
        </colgroup>
        <thead>
          <tr class="border-b border-white-300 text-left text-xs uppercase tracking-wide text-black-700 dark:border-navy-600 dark:text-black-600">
            <th class="px-5 py-2 font-medium">Name</th>
            <th class="px-5 py-2 font-medium">
              Memory
              {#if (data?.machine_mem_bytes ?? 0) > 0}
                <!-- Same shape as the CPU header: state the ceiling, so
                     the percentage beside each figure has a stated
                     denominator instead of an assumed one. -->
                <span class="font-normal normal-case text-black-600 dark:text-black-700">
                  of {humanBytes(data!.machine_mem_bytes)}
                </span>
              {/if}
            </th>
            <th class="px-5 py-2 font-medium">
              CPU
              {#if (data?.cpu_cores ?? 0) > 1}
                <!-- Percent of ONE core, so a busy browser legitimately
                     reads 444% here. Without the ceiling stated, that
                     looks like a bug. -->
                <span class="font-normal normal-case text-black-600 dark:text-black-700">
                  of {data!.cpu_cores * 100}%
                </span>
              {/if}
            </th>
            <th class="px-5 py-2 font-medium">Disk</th>
          </tr>
        </thead>
        <tbody>
          {#each data.groups as g (g.name)}
            <tr class="border-b border-white-300 last:border-0 dark:border-navy-600">
              <td class="px-5 py-2">
                {#if g.count > 1}
                  <button
                    type="button"
                    class="flex items-center gap-1 text-left text-black-900 hover:underline dark:text-white-100"
                    onclick={() => toggle(g.name)}
                    aria-expanded={expanded.has(g.name)}
                  >
                    <span class="inline-block text-xs transition-transform {expanded.has(g.name) ? 'rotate-90' : ''}">›</span>
                    <span>{g.name}</span>
                    <span class="text-xs text-black-700 dark:text-black-600">× {g.count}</span>
                  </button>
                {:else}
                  <span class="pl-4 text-black-900 dark:text-white-100">{g.name}</span>
                {/if}
              </td>
              <!-- Bytes and share are one measurement, not two: the
                   percentage is that same number against the machine
                   total. Two columns cost width and made the eye travel
                   to relate figures that belong together. -->
              <td class="px-5 py-2">
                <div class="flex items-center gap-2">
                  <span class="w-20 shrink-0 tabular-nums text-black-900 dark:text-white-100">
                    {humanBytes(g.rss_bytes)}
                  </span>
                  {#if (data?.machine_mem_bytes ?? 0) > 0}
                    <div class="h-1 w-14 shrink-0 overflow-hidden rounded-full bg-white-300 dark:bg-navy-600">
                      <div
                        class="h-full rounded-full bg-blue-600"
                        style="width: {Math.min(100, g.pct_of_machine_mem)}%"
                      ></div>
                    </div>
                    <span class="shrink-0 text-xs tabular-nums text-black-700 dark:text-black-600">
                      {g.pct_of_machine_mem.toFixed(1)}%
                    </span>
                  {/if}
                </div>
              </td>
              <td class="px-5 py-2 tabular-nums text-black-900 dark:text-white-100">
                {humanPct(g.cpu_pct)}
              </td>
              <td class="px-5 py-2 text-xs tabular-nums text-black-700 dark:text-black-600">
                {humanBps(g.io_read_bps + g.io_write_bps)}
              </td>
            </tr>

            {#if expanded.has(g.name)}
              <!-- Member rows share the parent table's columns, so PID
                   sits under Name, memory under Memory, and so on. A
                   nested <table> would size its own columns and stagger. -->
              {#each g.members as m (m.pid)}
                <tr class="border-b border-white-300 bg-white-200/50 text-xs dark:border-navy-600 dark:bg-navy-800/40">
                  <td class="py-1 pl-11 pr-5 tabular-nums text-black-700 dark:text-black-600">
                    pid {m.pid}
                  </td>
                  <td class="px-5 py-1">
                    <!-- Same bytes-plus-share shape as the group row above,
                         so a member can be compared with its parent without
                         reading across differently-built cells. -->
                    <div class="flex items-center gap-2">
                      <span class="w-20 shrink-0 tabular-nums text-black-900 dark:text-white-100">
                        {humanBytes(m.rss_bytes)}
                      </span>
                      <span class="shrink-0 tabular-nums text-black-700 dark:text-black-600">
                        {report_pct(m.rss_bytes)}
                      </span>
                    </div>
                  </td>
                  <td class="px-5 py-1 tabular-nums text-black-700 dark:text-black-600">
                    {humanPct(m.cpu_pct)}
                  </td>
                  <td class="px-5 py-1 tabular-nums text-black-700 dark:text-black-600">
                    {humanBps(m.io_read_bps + m.io_write_bps)}
                  </td>
                </tr>
              {/each}
              {#if g.count > g.members.length}
                <tr class="border-b border-white-300 bg-white-200/50 dark:border-navy-600 dark:bg-navy-800/40">
                  <td colspan="4" class="py-1 pl-11 pr-5 text-xs text-black-700 dark:text-black-600">
                    Showing the {g.members.length} largest of {g.count}.
                  </td>
                </tr>
              {/if}
            {/if}
          {/each}
        </tbody>
      </table>
    </div>

    {#if data.pages > 1}
      <div class="flex items-center justify-between border-t border-white-300 px-5 py-3 dark:border-navy-600">
        <span class="text-xs text-black-700 dark:text-black-600">
          Page {data.page} of {data.pages}
        </span>
        <div class="flex gap-1">
          <button
            type="button"
            class="rounded-lg border border-white-300 px-2.5 py-1 text-xs text-black-700 transition-colors hover:bg-white-200 disabled:opacity-40 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-600"
            disabled={data.page <= 1 || loading}
            onclick={() => goPage(data!.page - 1)}
          >
            Previous
          </button>
          <button
            type="button"
            class="rounded-lg border border-white-300 px-2.5 py-1 text-xs text-black-700 transition-colors hover:bg-white-200 disabled:opacity-40 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-600"
            disabled={data.page >= data.pages || loading}
            onclick={() => goPage(data!.page + 1)}
          >
            Next
          </button>
        </div>
      </div>
    {/if}
  {:else}
    <p class="px-5 py-6 text-center text-xs text-black-700 dark:text-black-600">Loading…</p>
  {/if}
</div>
