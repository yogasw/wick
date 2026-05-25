<script lang="ts">
  // AI guard report — dry-run the workflow against rule packs and surface
  // hits. Backend endpoint TBD in Phase 2.
  type GuardHit = { rule: string; node: string; severity: string; message: string };
  type Props = { hits: GuardHit[] | null };
  let { hits }: Props = $props();
</script>

{#if !hits}
  <p class="text-xs text-black-500 dark:text-white-700">Guard scan not run yet.</p>
{:else if hits.length === 0}
  <p class="text-xs text-emerald-600">No guard hits.</p>
{:else}
  <ul class="space-y-1 text-xs">
    {#each hits as h}
      <li class="flex gap-2">
        <span class="text-amber-600">⚠</span>
        <div>
          <span class="font-mono">{h.node}</span> · <b>{h.rule}</b>: {h.message}
        </div>
      </li>
    {/each}
  </ul>
{/if}
