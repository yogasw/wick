<script lang="ts">
  import { onMount } from "svelte";
  import { Breadcrumb, type BreadcrumbItem } from "@wick-fe/common-ui";
  import { apiGetSessionSpawns } from "$lib/api.js";
  import type { SessionSpawns, SpawnLogFileDTO } from "$lib/types.js";
  import SpawnDetail from "$lib/components/SpawnDetail.svelte";

  type Props = {
    base: string;
    id: string;
    onBack: () => void;
    /** Open the log viewer for a runtime log file (server/mcp/…). */
    onOpenLog: (logFile: string) => void;
  };
  let { base, id, onBack, onOpenLog }: Props = $props();

  let data = $state<SessionSpawns | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  // Multiple spawns can be open at once — a Set of expanded file paths.
  let open = $state<Set<string>>(new Set());

  let crumbs = $derived<BreadcrumbItem[]>([
    { label: "Providers", onClick: onBack },
    { label: `Session ${id.slice(0, 8)}`, truncate: true },
  ]);

  function spawnFile(s: SpawnLogFileDTO): string {
    const idx = Math.max(s.Path.lastIndexOf("/"), s.Path.lastIndexOf("\\"));
    return idx >= 0 ? s.Path.slice(idx + 1) : s.Path;
  }
  function toggle(path: string): void {
    const next = new Set(open);
    if (next.has(path)) next.delete(path); else next.add(path);
    open = next;
  }

  onMount(() => {
    apiGetSessionSpawns(base, id)
      .then((d) => {
        data = d;
        // Auto-open the newest spawn so the page lands on useful detail.
        if (d.Spawns.length > 0) open = new Set([d.Spawns[0].Path]);
      })
      .catch((e: unknown) => { error = e instanceof Error ? e.message : String(e); })
      .finally(() => { loading = false; });
  });
</script>

<div class="space-y-6">
  <Breadcrumb items={crumbs} />

  {#if loading}
    <div class="px-5 py-16 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
  {:else if error}
    <div class="rounded-xl border border-error-400 bg-error-100 px-4 py-3 text-sm text-error-800">{error}</div>
  {:else if data}
    <div>
      <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">Session {id.slice(0, 8)}</h1>
      <p class="mt-0.5 font-mono text-xs text-black-700 dark:text-black-600">
        {data.ProviderType}/{data.ProviderName} · {data.Spawns.length} spawn{data.Spawns.length === 1 ? "" : "s"}
      </p>
    </div>

    {#if data.Spawns.length === 0}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-5 py-8 text-center text-sm text-black-700 dark:text-black-600">
        No spawns for this session.
      </div>
    {:else}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
        <table class="w-full text-xs">
          <thead>
            <tr class="border-b border-white-300 dark:border-navy-600 text-black-700 dark:text-black-600">
              <th class="px-5 py-2.5 text-left">Started</th>
              <th class="px-5 py-2.5 text-left">PID</th>
              <th class="px-5 py-2.5 text-left">Status</th>
              <th class="px-5 py-2.5 text-left">First Message</th>
            </tr>
          </thead>
          <tbody>
            {#each data.Spawns as s (s.Path)}
              {@const isOpen = open.has(s.Path)}
              <tr
                class="border-b border-white-300 dark:border-navy-600 hover:bg-white-200 dark:hover:bg-navy-800 cursor-pointer {isOpen ? 'bg-white-200 dark:bg-navy-800' : ''}"
                onclick={() => toggle(s.Path)}
              >
                <td class="px-5 py-2 font-mono text-black-700 dark:text-black-600 whitespace-nowrap">
                  <span class="inline-block w-3 text-black-600">{isOpen ? "▾" : "▸"}</span>
                  {new Date(s.StartedAt).toLocaleString()}
                </td>
                <td class="px-5 py-2 font-mono text-black-700 dark:text-black-600">{s.PID > 0 ? s.PID : "—"}</td>
                <td class="px-5 py-2">
                  {#if !s.ExitReason}
                    <span class="rounded bg-green-100 dark:bg-green-900 px-1.5 py-0.5 text-xs text-green-700 dark:text-green-300">running</span>
                  {:else if s.ExitReason === "unclean"}
                    <span class="rounded bg-red-100 dark:bg-red-900 px-1.5 py-0.5 text-xs text-red-700 dark:text-red-300" title={s.ReasonDetail || "process died without recording an exit"}>unclean exit</span>
                  {:else if s.ExitReason === "error"}
                    <span class="rounded bg-red-100 dark:bg-red-900 px-1.5 py-0.5 text-xs text-red-700 dark:text-red-300" title={s.ReasonDetail}>error{s.ExitCode !== 0 ? ` (${s.ExitCode})` : ""}</span>
                  {:else}
                    <span class="rounded bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 text-xs text-black-700 dark:text-black-600" title={s.ReasonDetail}>{s.ExitReason}</span>
                  {/if}
                </td>
                <td class="px-5 py-2 text-black-700 dark:text-black-600 max-w-xs truncate">{s.FirstUserMessage}</td>
              </tr>
              {#if isOpen}
                <tr class="border-b border-white-300 dark:border-navy-600 bg-white-50 dark:bg-navy-900">
                  <td colspan="4" class="px-5 py-4">
                    <SpawnDetail {base} file={spawnFile(s)} {onOpenLog} embedded />
                  </td>
                </tr>
              {/if}
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</div>
