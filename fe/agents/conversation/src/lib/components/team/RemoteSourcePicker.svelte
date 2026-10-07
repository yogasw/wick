<script lang="ts">
  /* + Agent › Remote agent: where the agent lives. A2A, Slack and Plugin each
     open their own wizard. */
  import { REMOTE_SOURCES, type RemoteSource } from "../../remoteAgent.js";

  type Props = { value: RemoteSource; onSource: (s: RemoteSource) => void };
  let { value, onSource }: Props = $props();
</script>

<div class="px-6 pt-3" data-testid="remote-source">
  <p class="mb-1 text-xs font-medium text-black-800 dark:text-black-600" id="remote-source-label">Source</p>
  <div class="grid grid-cols-1 gap-2 sm:grid-cols-3" role="radiogroup" aria-labelledby="remote-source-label">
    {#each REMOTE_SOURCES as s (s.value)}
      <button
        type="button"
        role="radio"
        aria-checked={value === s.value}
        title={s.hint}
        data-testid="remote-source-{s.value}"
        class="rounded-xl border px-3 py-2 text-left text-sm {value === s.value
          ? 'border-green-500 text-black-900 dark:text-white-100'
          : 'border-white-300 text-black-800 hover:bg-white-200 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-600'}"
        onclick={() => { if (value !== s.value) onSource(s.value); }}
      >
        <span class="block font-medium">{s.label}</span>
        <span class="block text-[11px]">{s.value === "a2a" ? "A2A protocol" : s.value === "plugin" ? "Service plugin" : "DM · channel · thread"}</span>
      </button>
    {/each}
  </div>
</div>
