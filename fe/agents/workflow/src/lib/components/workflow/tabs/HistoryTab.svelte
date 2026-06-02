<script lang="ts">
  // Version history — list of {draft, published} snapshots from the
  // workflow_versions table. View / restore / compare actions wire to
  // /api/workflows/versions endpoints.
  import type { WorkflowVersion } from "$lib/types/workflow";
  import { workflowAPI } from "$lib/api/workflow";

  type Props = {
    workflowID: string;
    versions: WorkflowVersion[];
    onpick?: (id: number) => void;
    onrestore?: (id: number) => void;
  };
  let { workflowID, versions, onpick, onrestore }: Props = $props();

  // Compare mode: at most two versions selected by checkbox. Once two
  // are picked the Compare button activates; clicking opens the diff
  // pane with both bodies side-by-side.
  let selected = $state<number[]>([]);
  let compareOpen = $state(false);
  let compareLoading = $state(false);
  let compareError = $state<string | null>(null);
  let diffFrom = $state<WorkflowVersion | null>(null);
  let diffTo = $state<WorkflowVersion | null>(null);

  function toggleSelect(id: number) {
    if (selected.includes(id)) {
      selected = selected.filter((x) => x !== id);
    } else if (selected.length < 2) {
      selected = [...selected, id];
    } else {
      // Already two picked — replace the older one (FIFO).
      selected = [selected[1], id];
    }
  }

  async function openCompare() {
    if (selected.length !== 2) return;
    // Order ascending by id so the older snapshot lands on the left.
    const [a, b] = [...selected].sort((x, y) => x - y);
    compareLoading = true;
    compareError = null;
    compareOpen = true;
    try {
      const res = await workflowAPI.diffVersions(workflowID, a, b);
      diffFrom = res.from;
      diffTo = res.to;
    } catch (e: any) {
      compareError = e?.message ?? String(e);
    } finally {
      compareLoading = false;
    }
  }

  function closeCompare() {
    compareOpen = false;
    diffFrom = null;
    diffTo = null;
    compareError = null;
  }

  function bodyPretty(v: WorkflowVersion | null): string {
    if (!v) return "";
    try {
      return JSON.stringify(JSON.parse(v.body ?? ""), null, 2);
    } catch {
      return v.body ?? "";
    }
  }
</script>

{#if versions.length === 0}
  <p class="text-xs text-black-500 dark:text-white-700">No versions persisted yet.</p>
{:else}
  <div class="flex items-center justify-between mb-2">
    <span class="text-[11px] text-black-500 dark:text-white-700">
      {selected.length} of 2 selected for compare
    </span>
    <button
      class="text-xs px-2 py-0.5 rounded bg-emerald-500 text-white disabled:opacity-40"
      disabled={selected.length !== 2}
      onclick={openCompare}
    >Compare</button>
  </div>
  <ul class="divide-y divide-white-300 dark:divide-navy-600">
    {#each versions as v}
      <li class="flex items-center gap-3 py-1.5 text-xs">
        <input
          type="checkbox"
          class="accent-emerald-500"
          checked={selected.includes(v.id)}
          onchange={() => toggleSelect(v.id)}
        />
        <span class="px-1.5 py-0.5 rounded text-[10px] uppercase"
              class:bg-emerald-100={v.kind === "published"}
              class:text-emerald-700={v.kind === "published"}
              class:bg-amber-100={v.kind === "draft"}
              class:text-amber-700={v.kind === "draft"}
        >{v.kind}</span>
        <span class="text-black-500 dark:text-white-700 tabular-nums">v{v.id}</span>
        <span class="flex-1 truncate">{v.message ?? "—"}</span>
        <span class="text-black-500 dark:text-white-700">{v.created_at}</span>
        <button class="text-emerald-600" onclick={() => onpick?.(v.id)}>view</button>
        <button class="text-emerald-600" onclick={() => onrestore?.(v.id)}>restore</button>
      </li>
    {/each}
  </ul>
{/if}

{#if compareOpen}
  <div class="fixed inset-0 z-40 flex items-center justify-center bg-black/40">
    <div class="bg-white dark:bg-navy-700 rounded shadow-lg w-[90vw] h-[85vh] flex flex-col">
      <div class="flex items-center justify-between px-3 py-2 border-b border-white-300 dark:border-navy-600">
        <span class="text-sm font-semibold">Compare versions</span>
        <button class="text-xs px-2 py-0.5 rounded bg-slate-200 dark:bg-navy-600" onclick={closeCompare}>Close</button>
      </div>
      {#if compareLoading}
        <div class="flex-1 flex items-center justify-center text-xs">Loading…</div>
      {:else if compareError}
        <div class="flex-1 p-3 text-xs text-red-600">{compareError}</div>
      {:else}
        <div class="flex-1 grid grid-cols-2 gap-2 p-3 overflow-hidden">
          <div class="flex flex-col overflow-hidden">
            <span class="text-[11px] text-black-500 mb-1">
              v{diffFrom?.id} · {diffFrom?.kind} · {diffFrom?.created_at}
            </span>
            <pre class="flex-1 overflow-auto bg-slate-50 dark:bg-navy-800 text-[11px] p-2 rounded">{bodyPretty(diffFrom)}</pre>
          </div>
          <div class="flex flex-col overflow-hidden">
            <span class="text-[11px] text-black-500 mb-1">
              v{diffTo?.id} · {diffTo?.kind} · {diffTo?.created_at}
            </span>
            <pre class="flex-1 overflow-auto bg-slate-50 dark:bg-navy-800 text-[11px] p-2 rounded">{bodyPretty(diffTo)}</pre>
          </div>
        </div>
      {/if}
    </div>
  </div>
{/if}
