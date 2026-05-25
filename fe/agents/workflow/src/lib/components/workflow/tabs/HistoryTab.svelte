<script lang="ts">
  // Version history — list of {draft, published} snapshots from the new
  // workflow_versions table. View + restore actions wire to repository
  // endpoints added in Phase DB-3.
  import type { WorkflowVersion } from "$lib/types/workflow";
  type Props = {
    versions: WorkflowVersion[];
    onpick?: (id: number) => void;
    onrestore?: (id: number) => void;
  };
  let { versions, onpick, onrestore }: Props = $props();
</script>

{#if versions.length === 0}
  <p class="text-xs text-black-500 dark:text-white-700">No versions persisted yet.</p>
{:else}
  <ul class="divide-y divide-white-300 dark:divide-navy-600">
    {#each versions as v}
      <li class="flex items-center gap-3 py-1.5 text-xs">
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
