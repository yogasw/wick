<script lang="ts">
  // Version history — list of {draft, published} snapshots from the
  // workflow_versions table. View / restore / compare actions wire to
  // /api/workflows/versions endpoints.
  import type { WorkflowVersion } from "$lib/types/workflow";
  import { workflowAPI } from "$lib/api/workflow";
  import JsonDiff from "../fields/JsonDiff.svelte";

  type Props = {
    workflowID: string;
    versions: WorkflowVersion[];
    onrestore?: (id: number) => void;
    ondelete?: (id: number) => void;
    onclear?: () => void;
  };
  let { workflowID, versions, onrestore, ondelete, onclear }: Props = $props();

  // Compare mode: at most two versions selected by checkbox. Once two
  // are picked the Compare button activates; clicking opens the diff
  // pane with both bodies side-by-side.
  let selected = $state<number[]>([]);
  let compareOpen = $state(false);
  let compareLoading = $state(false);
  let compareError = $state<string | null>(null);
  let diffFrom = $state<WorkflowVersion | null>(null);
  let diffTo = $state<WorkflowVersion | null>(null);
  let viewing = $state<WorkflowVersion | null>(null);

  function confirmDelete(id: number) {
    if (!confirm(`Delete version v${id}? This removes the snapshot permanently.`)) return;
    ondelete?.(id);
    selected = selected.filter((x) => x !== id);
  }

  function confirmClear() {
    if (versions.length === 0) return;
    if (!confirm(`Clear all ${versions.length} history snapshot(s) for this workflow? This cannot be undone.`)) return;
    onclear?.();
    selected = [];
  }

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
  <p class="text-xs text-black-500 dark:text-white-100-700">No versions persisted yet.</p>
{:else}
  <div class="flex items-center justify-between mb-2">
    <span class="text-[11px] text-black-500 dark:text-white-100-700">
      {selected.length} of 2 selected for compare
    </span>
    <div class="flex items-center gap-2">
      <button
        class="text-xs px-2 py-0.5 rounded bg-emerald-500 text-white-100 disabled:opacity-40"
        disabled={selected.length !== 2}
        onclick={openCompare}
      >Compare</button>
      <button
        class="text-xs px-2 py-0.5 rounded border border-red-300 dark:border-red-800 text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/40 disabled:opacity-40"
        disabled={versions.length === 0}
        onclick={confirmClear}
      >Clear all</button>
    </div>
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
        <span class={`px-1.5 py-0.5 rounded text-[10px] uppercase ${v.kind === "published" ? "bg-emerald-100 dark:bg-emerald-900/30 text-emerald-700 dark:text-emerald-400" : "bg-amber-100 dark:bg-amber-900/30 text-amber-700 dark:text-amber-400"}`}>{v.kind}</span>
        <span class="text-black-500 dark:text-white-100-700 tabular-nums">v{v.id}</span>
        <span class="flex-1 truncate">{v.message ?? "—"}</span>
        <span class="text-black-500 dark:text-white-100-700">{v.created_at}</span>
        <button class="text-emerald-600" onclick={() => (viewing = v)}>view</button>
        <button class="text-emerald-600" onclick={() => onrestore?.(v.id)}>restore</button>
        <button class="text-red-600 dark:text-red-400" title="Delete this snapshot" onclick={() => confirmDelete(v.id)}>🗑</button>
      </li>
    {/each}
  </ul>
{/if}

{#if compareOpen}
  <div
    class="fixed inset-0 z-[60] flex items-center justify-center bg-black/60 backdrop-blur-sm"
    role="dialog"
    aria-modal="true"
    tabindex="-1"
    onclick={closeCompare}
    onkeydown={(e) => e.key === "Escape" && closeCompare()}
  >
    <div
      class="bg-white-100 dark:bg-navy-700 rounded-lg shadow-2xl border border-slate-200 dark:border-navy-600 w-[90vw] h-[85vh] flex flex-col"
      role="presentation"
      onclick={(e) => e.stopPropagation()}
    >
      <div class="flex items-center justify-between px-3 py-2 border-b border-white-300 dark:border-navy-600">
        <span class="text-sm font-semibold">Compare versions</span>
        <button class="text-xs px-2 py-0.5 rounded bg-slate-200 dark:bg-navy-600" onclick={closeCompare}>Close</button>
      </div>
      {#if compareLoading}
        <div class="flex-1 flex items-center justify-center text-xs">Loading…</div>
      {:else if compareError}
        <div class="flex-1 p-3 text-xs text-red-600 dark:text-red-400">{compareError}</div>
      {:else}
        <div class="flex-1 min-h-0">
          <JsonDiff
            leftText={bodyPretty(diffFrom)}
            rightText={bodyPretty(diffTo)}
            leftLabel={`v${diffFrom?.id ?? "?"} · ${diffFrom?.kind ?? ""}`}
            rightLabel={`v${diffTo?.id ?? "?"} · ${diffTo?.kind ?? ""}`}
          />
        </div>
      {/if}
    </div>
  </div>
{/if}

{#if viewing}
  <div
    class="fixed inset-0 z-[60] flex items-center justify-center bg-black/60 backdrop-blur-sm"
    role="dialog"
    aria-modal="true"
    tabindex="-1"
    onclick={() => (viewing = null)}
    onkeydown={(e) => e.key === "Escape" && (viewing = null)}
  >
    <div
      class="bg-white-100 dark:bg-navy-700 rounded-lg shadow-2xl border border-slate-200 dark:border-navy-600 w-[80vw] h-[80vh] flex flex-col"
      role="presentation"
      onclick={(e) => e.stopPropagation()}
    >
      <div class="flex items-center justify-between px-3 py-2 border-b border-white-300 dark:border-navy-600">
        <span class="text-sm font-semibold">v{viewing.id} · {viewing.kind} · {viewing.created_at}</span>
        <button class="text-xs px-2 py-0.5 rounded bg-slate-200 dark:bg-navy-600" onclick={() => (viewing = null)}>Close</button>
      </div>
      <pre class="flex-1 overflow-auto bg-slate-50 dark:bg-navy-800 text-[11px] p-2 m-3 rounded">{bodyPretty(viewing)}</pre>
    </div>
  </div>
{/if}
