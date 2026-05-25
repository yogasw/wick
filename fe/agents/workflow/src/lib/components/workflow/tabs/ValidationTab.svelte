<script lang="ts">
  import type { ValidationReport } from "$lib/api/workflow";

  type Props = { report: ValidationReport | null };
  let { report }: Props = $props();
</script>

{#if !report}
  <p class="text-xs text-black-500 dark:text-white-700">Click <em>Validate</em> in the toolbar to run static checks.</p>
{:else if report.ok && report.issues.length === 0}
  <p class="text-xs text-emerald-600">No issues found.</p>
{:else}
  <ul class="space-y-1 text-xs">
    {#each report.issues as iss}
      <li class="flex gap-2 items-start">
        <span class={iss.severity === "error" ? "text-rose-600" : "text-amber-600"}>
          {iss.severity === "error" ? "✖" : "⚠"}
        </span>
        <div class="flex-1">
          <span class="font-mono">{iss.node ?? "—"}{iss.field ? `.${iss.field}` : ""}</span>:
          {iss.message}
          {#if iss.hint}<span class="text-black-500 italic"> {iss.hint}</span>{/if}
        </div>
      </li>
    {/each}
  </ul>
{/if}
