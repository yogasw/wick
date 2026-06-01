<script lang="ts">
  // Guard tab — surfaces dry-run rule-pack hits from
  // /api/workflows/guard/{id}. Mirrors v1's
  // editor_bottom_tab_guard.templ output: per-hit row with rule name,
  // node, message, severity badge + focus button.
  import { detailNodeID, selectedNodeID, detailTriggerID, draftWorkflow } from "$lib/stores/editor";

  type GuardHit = {
    rule: string;
    node?: string;
    severity?: string;
    message: string;
  };

  type Props = { hits: GuardHit[] | null };
  let { hits }: Props = $props();

  function severityBadge(sev?: string): string {
    switch ((sev ?? "warning").toLowerCase()) {
      case "critical":
      case "error":
        return "bg-rose-100 text-rose-800 dark:bg-rose-900/40 dark:text-rose-300";
      case "warning":
      case "warn":
        return "bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300";
      default:
        return "bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300";
    }
  }

  function focusNode(id: string) {
    const wf = $draftWorkflow;
    if (!wf) return;
    const isTrigger = (wf.triggers ?? []).some((t) => t.id === id);
    if (isTrigger) {
      detailTriggerID.set(id);
    } else {
      selectedNodeID.set(id);
      detailNodeID.set(id);
    }
  }
</script>

{#if !hits}
  <p class="text-xs text-slate-500 dark:text-slate-400 italic">
    Guard scan not run yet. Run the workflow once or hit Save to populate.
  </p>
{:else if hits.length === 0}
  <p class="text-xs text-emerald-600 dark:text-emerald-400">
    ✓ No guard violations.
  </p>
{:else}
  <ul class="space-y-1.5 text-xs">
    {#each hits as h}
      <li class="flex items-start gap-2 rounded border border-slate-200 dark:border-slate-700 px-2 py-1.5">
        <span class="text-amber-500">⚠</span>
        <div class="flex-1 min-w-0">
          <div class="flex items-center gap-2 flex-wrap">
            <span class="font-mono text-[11px] text-rose-700 dark:text-rose-300 bg-rose-50 dark:bg-rose-900/30 px-1.5 py-0.5 rounded">
              {h.rule}
            </span>
            {#if h.node}
              <span class="font-mono text-[11px] text-slate-500 dark:text-slate-400">
                @ {h.node}
              </span>
            {/if}
            <span class="px-1.5 py-0.5 rounded text-[10px] uppercase tracking-wide {severityBadge(h.severity)}">
              {h.severity ?? "warning"}
            </span>
          </div>
          <div class="mt-0.5 text-slate-700 dark:text-slate-200">{h.message}</div>
        </div>
        {#if h.node}
          <button
            type="button"
            class="text-[11px] text-emerald-600 dark:text-emerald-400 hover:underline shrink-0"
            onclick={() => focusNode(h.node!)}
          >focus</button>
        {/if}
      </li>
    {/each}
  </ul>
{/if}
