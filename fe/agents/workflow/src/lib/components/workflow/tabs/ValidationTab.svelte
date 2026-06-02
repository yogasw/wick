<script lang="ts">
  // Validation tab — matches v1 editor_bottom_tab_validation.templ +
  // bottom_tab_validation.js. Rows pulled from the shared
  // `validationReport` store so any save flow auto-refreshes the
  // panel; the focus button scrolls the canvas to the offending
  // node + flashes the selection ring.
  import {
    validationReport,
    validationErrorCount,
    validationWarningCount,
    selectedNodeID,
    detailNodeID,
    detailTriggerID,
    draftWorkflow,
  } from "$lib/stores/editor";

  // Best-effort node id extraction. `node` lands on decorateReport
  // via the Path regex; Path itself is the canonical raw locator if
  // the decoration step is ever skipped.
  function extractNodeID(issue: { node?: string; Path?: string }): string | null {
    if (issue.node) return issue.node;
    if (!issue.Path) return null;
    const m = issue.Path.match(/graph\.nodes\[([^\]]+)\]/);
    return m ? m[1] : null;
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

  // Server validationPayload() bundles errors + warnings as separate
  // arrays already — no FE-side filtering needed.
  const errors = $derived($validationReport?.errors ?? []);
  const warnings = $derived($validationReport?.warnings ?? []);
</script>

{#if !$validationReport}
  <p class="text-xs text-slate-500 dark:text-slate-400 italic">
    Save the draft to run validation. Errors here block publish.
  </p>
{:else if $validationErrorCount === 0 && $validationWarningCount === 0}
  <p class="text-xs text-emerald-600 dark:text-emerald-400">
    ✓ No validation issues. Draft is ready to publish.
  </p>
{:else}
  {#if $validationErrorCount > 0}
    <div class="mb-2">
      <div class="text-xs font-semibold text-rose-600 dark:text-rose-400">
        ✕ {$validationErrorCount} {$validationErrorCount === 1 ? "error" : "errors"} — Publish blocked
      </div>
      <div class="text-[11px] text-slate-500 dark:text-slate-400">
        Draft is saved — fix the errors below before publishing.
      </div>
    </div>
    <ul class="space-y-1 text-xs mb-3">
      {#each errors as iss}
        {@const nodeID = extractNodeID(iss)}
        <li class="flex items-start gap-2 rounded border border-rose-200 dark:border-rose-900/50 bg-rose-50 dark:bg-rose-900/20 px-2 py-1.5">
          <span class="text-rose-500">✕</span>
          <div class="flex-1 min-w-0">
            <div class="font-mono text-[11px] text-rose-700 dark:text-rose-300 break-all">
              {iss.Path ?? iss.node ?? "global"}
            </div>
            <div class="text-slate-700 dark:text-slate-200">{iss.Message}</div>
            {#if iss.hint}
              <div class="text-[11px] italic text-slate-500 dark:text-slate-400 mt-0.5">
                {iss.hint}
              </div>
            {/if}
          </div>
          {#if nodeID}
            <button
              type="button"
              class="text-[11px] text-emerald-600 dark:text-emerald-400 hover:underline shrink-0"
              onclick={() => focusNode(nodeID)}
              title="Open the offending node in the inspector"
            >focus</button>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}

  {#if $validationWarningCount > 0}
    <div class="mb-2">
      <div class="text-xs font-semibold text-amber-600 dark:text-amber-400">
        ⚠ {$validationWarningCount} {$validationWarningCount === 1 ? "warning" : "warnings"}
      </div>
      <div class="text-[11px] text-slate-500 dark:text-slate-400">
        Warnings do not block publish but are worth a look.
      </div>
    </div>
    <ul class="space-y-1 text-xs">
      {#each warnings as iss}
        {@const nodeID = extractNodeID(iss)}
        <li class="flex items-start gap-2 rounded border border-amber-200 dark:border-amber-900/50 bg-amber-50 dark:bg-amber-900/20 px-2 py-1.5">
          <span class="text-amber-500">⚠</span>
          <div class="flex-1 min-w-0">
            <div class="font-mono text-[11px] text-amber-700 dark:text-amber-300 break-all">
              {iss.Path ?? iss.node ?? "global"}
            </div>
            <div class="text-slate-700 dark:text-slate-200">{iss.Message}</div>
            {#if iss.hint}
              <div class="text-[11px] italic text-slate-500 dark:text-slate-400 mt-0.5">
                {iss.hint}
              </div>
            {/if}
          </div>
          {#if nodeID}
            <button
              type="button"
              class="text-[11px] text-emerald-600 dark:text-emerald-400 hover:underline shrink-0"
              onclick={() => focusNode(nodeID)}
            >focus</button>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
{/if}
