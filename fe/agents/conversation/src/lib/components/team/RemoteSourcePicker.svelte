<script lang="ts">
  /* + Agent › Remote agent: where the agent lives. A2A and Slack each open
     their own wizard; HTTP and Plugin show as coming later. */
  import { REMOTE_SOURCES, type RemoteSource } from "../../remoteAgent.js";

  type Props = { value: RemoteSource; onSource: (s: RemoteSource) => void };
  let { value, onSource }: Props = $props();
</script>

<div class="px-6 pt-3" data-testid="remote-source">
  <p class="mb-1 text-xs font-medium text-black-800 dark:text-black-600" id="remote-source-label">Source</p>
  <div class="grid grid-cols-2 gap-2 sm:grid-cols-4" role="radiogroup" aria-labelledby="remote-source-label">
    {#each REMOTE_SOURCES as s (s.value)}
      <button
        type="button"
        role="radio"
        aria-checked={value === s.value}
        disabled={s.disabled}
        title={s.hint}
        data-testid="remote-source-{s.value}"
        class="rounded-xl border px-3 py-2 text-left text-sm disabled:cursor-not-allowed disabled:opacity-60 {value === s.value
          ? 'border-green-500 text-black-900 dark:text-white-100'
          : 'border-white-300 text-black-800 hover:bg-white-200 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-600'}"
        onclick={() => { if (!s.disabled && value !== s.value) onSource(s.value); }}
      >
        <span class="block font-medium">{s.label}</span>
        <span class="block text-[11px] {s.disabled ? 'uppercase tracking-wider' : ''}">{s.disabled ? "Coming later" : s.value === "a2a" ? "A2A protocol" : "DM · channel · thread"}</span>
      </button>
    {/each}
  </div>
</div>
