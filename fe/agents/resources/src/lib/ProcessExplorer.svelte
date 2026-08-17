<script lang="ts">
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { fetchProcessesE, killProcessE } from "$lib/api.js";
  import CommandLine from "$lib/CommandLine.svelte";
  import RowMenu from "$lib/RowMenu.svelte";
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
      const next = await Effect.runPromise(
        fetchProcessesE(base, { q: query, sort, page }).pipe(Effect.provide(WickClientLayer)),
      );
      data = next;
      error = "";
      pruneExpanded(next);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  // Forget groups that are no longer on the machine — a program that
  // exited, or was uninstalled. Left alone the set only grows, holding
  // names of things that stopped existing for as long as the page is
  // open, and re-expanding them if a same-named process ever returns.
  //
  // Only prunes names absent from an UNFILTERED page: with a search or
  // pagination active, most groups are missing simply because they are
  // not on this page, and dropping them would collapse rows the operator
  // opened as soon as they typed.
  function pruneExpanded(res: ProcessListResponse): void {
    if (query.trim() !== "" || res.pages > 1) return;
    const live = new Set(res.groups.map((g) => g.name));
    const kept = new Set([...expanded].filter((n) => live.has(n)));
    if (kept.size !== expanded.size) expanded = kept;
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

  // Keep in step with the summary cards above, which refresh on the same
  // interval. Left static, the two tables disagreed on screen — the same
  // process showing 196 MB in one and 190 MB in the other — which reads
  // as a bug rather than as one of them being older.
  //
  // Paused while the tab is hidden: nobody is reading it, and each poll
  // walks every process on the machine.
  let started = false;
  let timer: ReturnType<typeof setInterval> | null = null;

  function tick(): void {
    if (typeof document !== "undefined" && document.hidden) return;
    // Skip while a request is still in flight, so a slow machine does not
    // stack overlapping walks.
    if (loading) return;
    void load();
  }

  $effect(() => {
    if (started) return;
    started = true;
    void load();
    timer = setInterval(tick, 10_000);
    return () => {
      if (timer) clearInterval(timer);
    };
  });

  const sortLabel: Record<string, string> = { mem: "Memory", cpu: "CPU", io: "Disk" };

  // A command earns its line only when it says something the name does
  // not. "chrome.exe" under a row already labelled chrome.exe is pure
  // noise, and on a machine with hundreds of processes that noise is most
  // of the table.
  //
  // Kept as a display decision rather than dropping the field server-side:
  // search still matches on the full command even when it is not shown.
  function showCmd(name: string, cmd?: string): string {
    const trimmed = cmd?.trim() ?? "";
    // Identical to the name it sits under: nothing gained, one more line.
    return trimmed === name ? "" : trimmed;
  }

  // Why a given pid cannot be ended, or "" when it can.
  //
  // The server is still the authority — it re-checks and refuses on its
  // own. This exists so the operator reads the rule before clicking, not
  // after: a row that looks like every other one but declines to die is
  // indistinguishable from a broken button.
  function protectedReason(pid: number): string {
    if (data && pid === data.self_pid) {
      return "This is the wick server showing you this page. Ending it would close the dashboard.";
    }
    if (pid === 1) return "pid 1 is the system's init process and cannot be ended from here.";
    return "";
  }

  // A group is protected only if EVERY member is — otherwise the group
  // kill still has work to do, and the server drops the protected ones.
  function groupProtectedReason(members: { pid: number }[]): string {
    if (members.length === 0) return "";
    const reasons = members.map((m) => protectedReason(m.pid));
    return reasons.every((r) => r !== "") ? reasons[0] : "";
  }

  // Ending a process is the only destructive thing this page can do. The
  // server owns the safety rules (never wick, never pid 1, capped group
  // kill) — repeating them here would give two places to keep in sync.
  async function kill(body: { pid?: number; name?: string }): Promise<void> {
    try {
      const res = await Effect.runPromise(
        killProcessE(base, body).pipe(Effect.provide(WickClientLayer)),
      );
      toastOk(res.message + (res.skipped?.length ? ` (${res.skipped.join("; ")})` : ""));
      await load();
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    }
  }

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
        placeholder="Search name or command…"
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
          <col style="width: 34%" />
          <col style="width: 28%" />
          <col style="width: 15%" />
          <col style="width: 15%" />
          <col style="width: 8%" />
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
            <th class="px-2 py-2"><span class="sr-only">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          {#each data.groups as g (g.name)}
            {@const gLocked = groupProtectedReason(g.members)}
            <tr
              class="border-b border-white-300 last:border-0 hover:bg-white-200/60 dark:border-navy-600 dark:hover:bg-navy-800/40"
              title={gLocked || undefined}
            >
              <!-- max-w-0: see the member row below — a cell without an
                   explicit constraint grows to its content, defeating the
                   truncate on the command inside it. -->
              <td class="max-w-0 px-5 py-2">
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
                  {@const cmd = showCmd(g.name, g.members[0]?.cmdline)}
                  <div class="pl-4">
                    <span class="text-black-900 dark:text-white-100">{g.name}</span>
                    {#if gLocked}
                      <!-- Marked inline, where the eye already is. The
                           menu explains why; this only has to say that
                           this row is not an ordinary one. -->
                      <span
                        class="ml-1.5 rounded border border-white-300 px-1 py-px align-middle text-[10px] text-black-700 dark:border-navy-600 dark:text-black-600"
                      >
                        protected
                      </span>
                    {/if}
                    {#if cmd}
                      <!-- A single-process group has no expander, so its
                           command would otherwise be unreachable — and a
                           bare "python3" is exactly the row an operator
                           needs identified. -->
                      <CommandLine {cmd} />
                    {/if}
                  </div>
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
              <td class="px-2 py-2">
                <RowMenu
                  cmd={g.members[0]?.cmdline ?? ""}
                  label={g.name}
                  count={g.count}
                  protectedReason={gLocked}
                  onKill={() => void kill(g.count > 1 ? { name: g.name } : { pid: g.members[0]?.pid })}
                />
              </td>
            </tr>

            {#if expanded.has(g.name)}
              <!-- Member rows share the parent table's columns, so PID
                   sits under Name, memory under Memory, and so on. A
                   nested <table> would size its own columns and stagger. -->
              {#each g.members as m (m.pid)}
                {@const mcmd = showCmd(m.name, m.cmdline)}
                {@const mLocked = protectedReason(m.pid)}
                <tr
                  class="border-b border-white-300 bg-white-200/50 text-xs hover:bg-white-300/60 dark:border-navy-600 dark:bg-navy-800/40 dark:hover:bg-navy-700/50"
                  title={mLocked || undefined}
                >
                  <!-- max-w-0 is what makes truncate work inside a table
                       cell: without an explicit constraint the cell grows
                       to fit its content, so the child has no width to be
                       clipped against and a long command overflows across
                       the columns beside it. With table-fixed the colgroup
                       still sets the real width. -->
                  <td class="max-w-0 py-1 pl-11 pr-5">
                    <div class="flex items-center gap-1.5 tabular-nums text-black-700 dark:text-black-600">
                      <span>pid {m.pid}</span>
                      {#if mLocked}
                        <span
                          class="rounded border border-white-300 px-1 py-px text-[10px] dark:border-navy-600"
                        >
                          protected
                        </span>
                      {/if}
                    </div>
                    {#if mcmd}
                      <!-- The command is what distinguishes one node.exe
                           from another; the name alone cannot. Truncated
                           inline, expandable for the full text. -->
                      <CommandLine cmd={mcmd} />
                    {/if}
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
                  <td class="px-2 py-1">
                    <RowMenu
                      cmd={m.cmdline ?? ""}
                      label="{m.name} (pid {m.pid})"
                      protectedReason={mLocked}
                      onKill={() => void kill({ pid: m.pid })}
                    />
                  </td>
                </tr>
              {/each}
              {#if g.count > g.members.length}
                <tr class="border-b border-white-300 bg-white-200/50 dark:border-navy-600 dark:bg-navy-800/40">
                  <td colspan="5" class="py-1 pl-11 pr-5 text-xs text-black-700 dark:text-black-600">
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
